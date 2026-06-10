# Emitting Usage Events

Billing in Datum is not a service-by-service integration. A service team
declares what it measures via a `MeterDefinition` (and the
`MonitoredResourceType` it counts), then emits `UsageEvent`s through the
platform's emission SDK. The pipeline handles CloudEvents transformation,
durability, attribution, and provider submission. This document shows
service developers how to (1) declare a meter and (2) emit usage events
against it.

For the pipeline design itself, see
[usage-pipeline.md](./enhancements/usage-pipeline.md).

## Declaring what you measure

Service teams author a **single** cluster-scoped `ServiceConfiguration`
in `services.miloapis.com/v1alpha1`. It is the only document you author;
the services-operator fans it out into the downstream billing CRDs
(`billing.miloapis.com/MeterDefinition` and `MonitoredResourceType`).
You should not author those directly — they're operator-visible
artifacts for runbook and inspection use.

A `ServiceConfiguration` declares three things:

- **`spec.monitoredResourceTypes[]`** — the Kubernetes Kinds your
  service emits usage against, plus the closed set of labels each
  Kind's events may carry. Keyed by `.type`.
- **`spec.metrics[]`** — metric descriptors with a UCUM `unit` and a
  `kind` (e.g. `Delta`). Keyed by `.name`.
- **`spec.billing.consumerDestinations[]`** — routes metrics to
  monitored resource types. A metric only becomes a `MeterDefinition`
  if it appears here; metrics without a billing destination are
  quota-only.

Optionally also `spec.quota.{limits,metricRules}` for quota
enforcement (out of scope for this guide).

### Example

```yaml
apiVersion: services.miloapis.com/v1alpha1
kind: ServiceConfiguration
metadata:
  name: compute-miloapis-com
  labels:
    app.kubernetes.io/managed-by: compute-team
spec:
  serviceRef:
    name: compute-miloapis-com   # name of the cluster-scoped Service resource
  phase: Published                # Draft → Published → Deprecated → Retired
  monitoredResourceTypes:
    - type: compute.miloapis.com/Instance
      displayName: Compute Instance
      description: A running compute workload instance.
      gvk:
        group: compute.miloapis.com
        kind: Instance
      labels:
        - name: region
          description: Geographic region the instance runs in.
        - name: instance.type
          description: Instance machine type.
  metrics:
    - name: compute.miloapis.com/instance/cpu-seconds
      displayName: CPU Seconds
      description: Total vCPU-seconds consumed by an instance.
      kind: Delta
      unit: s   # UCUM
  billing:
    consumerDestinations:
      - monitoredResourceType: compute.miloapis.com/Instance
        metrics:
          - compute.miloapis.com/instance/cpu-seconds
```

Ship the manifest as part of your service's Milo control-plane
kustomization. The webhook resolves `spec.serviceRef` to the
`Service.spec.serviceName` and enforces that every `metrics[].name` and
`monitoredResourceTypes[].type` is prefixed with it.

### Lifecycle

`spec.phase` follows `Draft → Published → Deprecated → Retired`
forward-only. **`Draft` documents are not fanned out**, so emitting
against a meter whose `ServiceConfiguration` is still `Draft` will
quarantine with `unknown_meter` (see the runbook). Bump to `Published`
once your meters are ready.

### Immutability you should know about

- `spec.metrics[].name`, `kind`, and `unit` are immutable once the
  `ServiceConfiguration` is `Published`. A breaking change ships as a
  new metric name (e.g. `…/cpu-seconds/v2`).
- `spec.monitoredResourceTypes[].type` and `gvk` are immutable once
  `Published`.
- Adding an optional label or a new metric is additive and safe.
  Removing or renaming either is breaking — version the name.

### What the fan-out produces (operator-visible)

After the services-operator reconciles a `Published`
`ServiceConfiguration`, you will see corresponding
`billing.miloapis.com/MeterDefinition` and `MonitoredResourceType`
objects with `app.kubernetes.io/managed-by=services-operator`. These
are read-only as far as your service is concerned — do not patch them
directly. Edit the `ServiceConfiguration` and let the fan-out catch up.

`MeterDefinition.spec.meterName` is set to
`ServiceConfiguration.spec.metrics[].name` 1:1, so the canonical name
you emit against in code (next section) matches what you declared
here.

## Emitting events

Import the emission SDK and call `Record` once per billable observation.
The SDK validates the event, wraps it in a CloudEvents envelope with a
ULID identifier, and forwards it to the node-local Vector Agent for
durable forwarding to the central ingestion gateway.

```go
import (
    "context"

    "go.miloapis.com/billing/emission"
)

recorder, err := emission.NewUsageRecorder(
    emission.WithEndpoint("http://localhost:9880/cloudevents"),
)
if err != nil {
    return fmt.Errorf("constructing usage recorder: %w", err)
}

err = recorder.Record(ctx, emission.UsageEvent{
    Meter:   "compute.miloapis.com/instance/cpu-seconds",
    Project: emission.ProjectRef{Name: "p-abc"},
    Source:  "//compute.miloapis.com/controllers/instance-reconciler",
    Quantity: 42,
    Dimensions: map[string]string{
        "region":        "us-east-1",
        "instance.type": "n1-standard-4",
    },
    Resource: &emission.ResourceRef{
        Group:     "compute.miloapis.com",
        Kind:      "Instance",
        Namespace: "default",
        Name:      "instance-123",
        UID:       instance.UID,
    },
    OccurredAt: observedAt,
})
if err != nil {
    return fmt.Errorf("recording usage: %w", err)
}
```

### What the SDK does

1. **Structural validation, synchronous.** Required fields, `Source` URI
   shape, `Project.Name` shape. A failure here is a producer bug — `Record`
   returns immediately and nothing is written.
2. **CloudEvents envelope.** Generates a ULID `id`, maps `Meter` →
   `type`, `Project` → `subject` (`projects/<name>`), `Source` and
   `OccurredAt` to their CloudEvents attributes; `Quantity`, `Dimensions`,
   and `Resource` populate `data`.
3. **Durable handoff to Vector.** POSTs the event over localhost HTTP to
   the node's Vector Agent. Retries transient failures with bounded
   exponential backoff. `Record` returns success only after the Agent
   acknowledges the write to its disk buffer.
4. **Error handling stays with you.** If `Record` returns an error, the
   SDK has *not* buffered the event in memory. Re-queue from your
   controller, retry on the next reconcile, or surface the error — but
   do not assume the event is in flight.

### What you do not do

- **Do not import a CloudEvents SDK.** The emission SDK owns the wire
  format. If you need a new field, file an enhancement against
  [usage-pipeline.md](./enhancements/usage-pipeline.md).
- **Do not call the ingestion gateway directly.** The SDK → Vector Agent
  → Gateway path is what provides Tier 1 disk durability. Bypassing
  Vector loses that guarantee.
- **Do not attribute usage yourself.** `subject` is a project, never a
  billing account. The billing controllers attribute project → account
  using the `BillingAccountBinding` graph at processing time.
- **Do not use floating point.** `Quantity` is `int64`. Measure
  cpu-*seconds*, not cpu-hours; bytes, not gibibytes. The meter's
  `consumedUnit`/`pricingUnit` handle scale.

## Emission contract

| Outcome | When |
|---|---|
| `Record` returns `nil` | Event durably committed to the local Vector Agent buffer. Pipeline owns delivery from here. |
| `Record` returns `ValidationError` | Producer bug: required field missing or malformed. Fix the call site — do not retry. |
| `Record` returns any other error | Vector Agent unreachable after retry budget exhausted. Re-queue from your reconciler. |
| Event silently absent from the provider | Either (a) `MeterDefinition` not `Published` yet, (b) project has no `BillingAccountBinding`, or (c) event was quarantined upstream. See the [usage-metering runbook](./runbooks/usage-metering.md). |

A returned `nil` is not "the customer will be billed" — it is "the
platform now owns durable delivery of this event." Quarantine, attribution
gaps, and provider lag are visible to operators via metrics, never via
your `Record` call.

## Non-goals for service developers

- **Owning the meter catalog format.** `MeterDefinition` /
  `MonitoredResourceType` are platform-owned CRDs. Use them; do not
  fork.
- **Provider-specific code.** No service imports the Amberflo SDK (or
  any successor). Vendor swaps are a provider-controller change, not a
  service-code change.
- **Pricing.** Rates, tiers, currencies, and invoice generation live in
  the pricing engine. Meters carry FOCUS `consumedUnit` / `pricingUnit`
  hints; the pricing engine consumes them.
- **Aggregation in your service.** Emit raw observations at your
  natural cadence. The pipeline (and provider) handle aggregation per
  the meter's `measurement.aggregation`.

## Cross-references

- Pipeline design and event envelope details:
  [usage-pipeline.md](./enhancements/usage-pipeline.md)
- Operator guide for creating a `BillingAccount`, binding projects, and
  verifying usage in the provider:
  [staff-portal/docs/operators/billing-accounts.md](https://github.com/datum-cloud/staff-portal/blob/main/docs/operators/billing-accounts.md)
- Diagnosing missing usage, wrong attribution, or unregistered meters:
  [usage-metering runbook](./runbooks/usage-metering.md).
