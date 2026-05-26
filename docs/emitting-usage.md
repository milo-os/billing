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

Two cluster-scoped resources, both in API group `billing.miloapis.com/v1alpha1`:

- `MonitoredResourceType` — names a Kubernetes Kind whose lifecycle
  produces billable signal (e.g. `compute.miloapis.com/Instance`), and the
  closed set of dimension labels events for that Kind may carry.
- `MeterDefinition` — names a specific signal aggregated over a billing
  period (e.g. `compute.miloapis.com/instance/cpu-seconds`), references one
  or more `MonitoredResourceType`s, and declares its aggregation, unit, and
  FOCUS billing terms.

Both follow a `Draft → Published → Deprecated → Retired` lifecycle. Only
`Published` meters are accepted by the ingestion gateway.

### Example: `MonitoredResourceType`

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: MonitoredResourceType
metadata:
  name: compute-instance
  labels:
    app.kubernetes.io/managed-by: compute-team
spec:
  resourceTypeName: compute.miloapis.com/Instance
  displayName: Compute Instance
  description: A running compute workload instance.
  phase: Published
  gvk:
    group: compute.miloapis.com
    kind: Instance
  labels:
    - name: region
      required: true
    - name: instance.type
      required: true
    - name: resource.tier
      required: false
```

### Example: `MeterDefinition`

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: MeterDefinition
metadata:
  name: compute-instance-cpu-seconds
  labels:
    app.kubernetes.io/managed-by: compute-team
spec:
  meterName: compute.miloapis.com/instance/cpu-seconds
  displayName: Compute Instance CPU Time
  description: |
    CPU time consumed by running compute instances, measured at 1-minute
    resolution.
  phase: Published
  monitoredResourceTypes:
    - compute.miloapis.com/Instance
  measurement:
    aggregation: Sum
    unit: s            # UCUM
    dimensions:
      - region
      - instance.type
      - resource.tier
  billing:
    consumedUnit: s    # FOCUS terminology
    pricingUnit: h
```

Ship both manifests as part of your service's Milo control-plane
kustomization. Once reconciled, your meter is visible to the staff portal
and the provider sync controller will register it with the external
billing provider.

### Immutability you should know about

- `MeterDefinition.spec.meterName`,
  `spec.measurement.aggregation`, and `spec.measurement.unit` are
  immutable after creation. A breaking change ships as a **new**
  `MeterDefinition` with a versioned `meterName` (e.g.
  `…/cpu-seconds/v2`).
- `MonitoredResourceType.spec.resourceTypeName` and `spec.gvk` are
  immutable.
- Adding an optional dimension/label is additive. Adding a required one,
  or removing any declared one, is breaking — ship as a new resource.

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
