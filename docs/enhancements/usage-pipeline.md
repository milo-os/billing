# Enhancement: Durable Usage Pipeline

**Status:** Draft for stakeholder review
**Author:** Billing platform team
**Scope:** The path from a service emitting a usage event to an invoice line on an external billing provider. Complements [`MeterDefinition`](../../../service-infrastructure/docs/enhancements/metering-definitions.md) and the existing `BillingAccount` / `BillingAccountBinding` resources.

---

## 1. Why this exists

`MeterDefinition` declares *what can be metered*. `BillingAccountBinding` declares *who pays for a project*. Nothing today actually moves a usage event from a service to an invoice line — and no service is billing usage yet.

That gap is the opportunity. If we don't fill it, the first team that needs to bill will call a provider API directly from application code, the second will copy and diverge, and by the third we have three incomplete pipelines to unwind. Defining this layer once — before any team ships usage billing — is materially cheaper than rationalising later.

This document proposes the **durable usage pipeline**: a platform-owned path that accepts usage events from services, attributes them to the right billing account, reports them to an external provider (Amberflo in v0), and reconciles against what the provider actually saw.

It is about faithful transport of usage. It does **not** calculate money, own the meter catalog, or replace observability metrics.

---

## 2. What services see

Services emit a small, versioned envelope. No shared runtime dependency, no OpenTelemetry coupling — just JSON or Protobuf over HTTP/gRPC.

```yaml
eventID:    "01HQ3XTA4WV6E2C0Y7KQRBZ9MN"     # ULID, producer-generated, end-to-end dedup key
meterName:  "compute.miloapis.com/instance/cpu-seconds"
timestamp:  "2026-04-14T14:30:00.000Z"
projectRef:
  name: "p-abc"                              # project whose binding determines the payer
value:      "42"
dimensions:                                  # pricing axes, must match MeterDefinition
  region: "us-east-1"
  tier:   "standard"
resource:                                    # what emitted the event
  ref:
    projectRef: { name: "p-abc" }            # must equal event.projectRef
    group:     "compute.miloapis.com"
    kind:      "Instance"
    namespace: "default"
    name:      "instance-123"
    uid:       "a8f3a1b2-..."                # distinguishes recreated-same-name objects
  labels:                                    # point-in-time descriptive snapshot
    region: "us-east-1"
    tier:   "standard"
```

What the platform guarantees in exchange:

- **Disk-backed spool** in the SDK. If the platform is unreachable, the service keeps running and events replay when the path recovers.
- **At-least-once delivery with dedup.** A retry after a crash never double-bills — `eventID` is the settlement anchor end to end.
- **No silent drops.** Every rejected event surfaces on a health resource; nothing disappears into a log line.

Validation at the edge:

- `eventID` must parse as ULID.
- `meterName` must match a Published `MeterDefinition`.
- `dimensions` keys must be a subset of the meter's declared dimensions.
- `resource.ref.projectRef` must equal the event's `projectRef` (a service cannot attribute another project's resources to its own usage).
- `resource` labels must match the governing `MonitoredResourceType` schema.

Anything that fails validation is rejected synchronously with a structured error. Anything that passes is durable before the service gets its 2xx.

---

## 3. Attribution in one paragraph

For every event, the pipeline looks at all `BillingAccountBinding` resources for the event's project — both `Active` and `Superseded` — sorted by `status.billingResponsibility.establishedAt`. That sequence forms a timeline of who owed the bill when. The event is attributed to whichever binding's interval contains `event.timestamp`. Rebinding mid-period is fully supported; bindings that still have events referencing their interval cannot be garbage-collected until those events are safely submitted (enforced via a pipeline-owned finalizer on `BillingAccountBinding`).

Events that arrive with no matching binding, or whose binding points at an archived account, are quarantined rather than silently retried or dropped. An operator is paged with the specific reason.

---

## 4. The declarative surface

Three new CRDs. Every piece of user-visible configuration lives here; the rest of the pipeline is workloads and a durable log.

### 4.1 `ProviderIntegration`

One per upstream billing provider. Configures how batches are sent, how local consumers map to the provider's customer IDs, and how reconciliation runs.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ProviderIntegration
metadata:
  name: amberflo-prod
  namespace: billing-system
spec:
  integrationName: "amberflo.production"     # canonical identifier, immutable once Published
  provider:
    kind: Amberflo                           # Amberflo | Stripe | Metronome | Orb | Lago | OpenMeter
    credentialsRef:
      name: amberflo-api-key
  reporting:
    batchSize: 100
    flushIntervalSeconds: 300
    lateEventWindowHours: 720
    maxRequestsPerSecond: 20                 # conservative default; tunable per provider
  reconciliation:
    mode: aggregate                          # aggregate | event
    intervalSeconds: 3600
  customerMapping:
    strategy: AnnotationLookup
    annotationKey: "billing.miloapis.com/amberflo-customer-id"
status:
  phase: Ready                               # Provisioning | Ready | Degraded | Suspended
  conditions: [...]
  lastReconciledAt: "..."
```

### 4.2 `MonitoredResourceType`

Governance CRD that declares which Kubernetes Kinds are billable, who owns them, and which descriptive labels may appear on events emitted against them. This is the cardinality firewall — labels outside the declared schema are rejected at the edge. Owned by the service infrastructure service and designed in detail in [`monitored-resource-types.md`](../../../service-infrastructure/docs/enhancements/monitored-resource-types.md); the billing pipeline is a consumer, matching events against the catalog at edge validation time.

### 4.3 `ReconciliationReport`

The authoritative audit record when finance asks *"what did we send to Amberflo on April 14 and what did they see?"* One per hourly reconciliation pass per provider. The CRD carries **signal** (summary + top drifts); the full discrepancy list is a pointer into the durable log so the object is always writable even on a bad day.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ReconciliationReport
metadata:
  name: amberflo-prod-2026-04-15-14
spec:
  providerIntegrationRef: { name: amberflo-prod }
  window:
    startTime: "2026-04-15T13:00:00Z"
    endTime:   "2026-04-15T14:00:00Z"
status:
  summary:
    bucketsChecked: 10234                    # (customer, meter, hour) tuples
    bucketsMatched: 10231
    bucketsDrifted: 3
    pastProviderWindow: 0
  topDrifts:                                 # bounded N ≤ 50, ordered by magnitude
    - bucket: { customer: c-7, meter: "compute.instance.cpu-seconds", hour: "2026-04-15T13:00:00Z" }
      localTotal:    "42.0"
      providerTotal: "41.0"
      candidateEventCount: 17
  detailRef:                                 # full discrepancy list lives outside etcd
    backend: usage-log
    stream:  reconciliation.details
    offset:  "12345678-12345999"
  conditions: [...]
```

Also shipped: `UsageStreamHealth`, a singleton-per-provider liveness CRD — ingestion rate, backlog depth, oldest unsubmitted event, attribution-failure count. One `kubectl get usagestreamhealth -A` is the operator's health dashboard.

---

## 5. Deployment shape

Project control planes are virtualized within a shared API server deployment — not separate physical clusters. The pipeline follows that shape:

- **Per-project Ingestion Gateway.** One lightweight Gateway per virtual project control plane, co-resident in the shared deployment. Validates events at the edge, buffers locally, forwards to the central log. No cross-cluster network hop.
- **Central data plane.** A durable log (NATS JetStream in v0, Kafka/Redpanda when we exceed ~10k events/sec), plus stateless controllers for attribution, aggregation, reporting, and reconciliation.
- **Declarative surface in the billing cluster.** The three CRDs above, plus `BillingAccount` / `BillingAccountBinding`. `MeterDefinition` and `MonitoredResourceType` are consumed from the service infrastructure service.

Events and submission records live in the durable log, not in etcd. This is deliberate — the [Operator Metering post-mortem](https://github.com/kube-reporting/metering-operator) is the canonical example of what happens when a billing pipeline treats etcd as a data plane.

Reconciliation runs aggregate-first: one provider query per `(customer, meter, hour)` bucket per hour, with drill-down into the local submission index only on drift. Per-event reconciliation does not scale against any provider and is never used.

---

## 6. Phased delivery

**v0 — revenue safe, feature minimal (one quarter).**

- Emission SDK in Go, with disk-backed spool.
- Per-project Ingestion Gateway.
- JetStream-backed durable log.
- Attribution against today's `BillingAccountBinding` shape, with the new retention finalizer.
- Hour-bucketed aggregation for `Sum` and `Count` (covers the near-term meter inventory).
- **Amberflo** as the only provider. `POST /ingest-batch` for emission, `POST /usage` for aggregate reconciliation. Known gaps (no per-event GET, undocumented dedup window and rate limits) are compensated by strict local retention and a tunable rate ceiling.
- `ProviderIntegration`, `MonitoredResourceType`, `ReconciliationReport`, `UsageStreamHealth` CRDs.

**Deferred to v1+:**

- Additional providers, additional aggregations, non-Go SDKs.
- Kafka/Redpanda migration (triggered by sustained >10k events/sec).
- Operator-triggerable replay workflow.
- A portable `correctionOf` adjustment flow (v0 uses provider-native tools for the rare correction).

**Non-negotiable, even in v0:**

- End-to-end idempotency keys; no shortcuts.
- Attribution resolved from the binding graph, never self-declared by services.
- 2xx only after durable commit.
- `ReconciliationReport` as a CRD from day one.
- Retention finalizer on `BillingAccountBinding`.

---

## 7. Open questions

1. **Amberflo operational envelope.** Rate limits, dedup-window duration, ingest→query lag — all undocumented. Calibrate in a dev tenant before v0 ships.
2. **Virtual control plane isolation model.** Exact mechanism (namespace-per-project, vcluster, multi-tenant API server layer) is unsettled and drives Gateway identity, credential scoping, and whether one Gateway Deployment can serve multiple projects.
3. **Second provider.** Metronome is the strongest aggregate-reconciliation peer. Product/finance call, not engineering.
4. **Per-event vs. hour-bucket dedup key at the provider.** Hour-bucket matches all three hyperscalers and is our default; per-event gives a finer audit trail at ~60× the key-space cost.
5. **Long-term audit archive.** `ReconciliationReport` summaries retain 90 days; detail retention inherits the log's 30-day floor. The 7-year SOX window lives somewhere else — BigQuery, Snowflake, Iceberg-on-S3? Platform-level choice.
6. **Replay in v0 or v1?** An operator-triggerable replay path adds ~2 weeks. Excluding it means the first real incident is the first replay.

---

## References

- [`service-infrastructure/docs/enhancements/metering-definitions.md`](../../../service-infrastructure/docs/enhancements/metering-definitions.md)
- [`service-infrastructure/docs/enhancements/monitored-resource-types.md`](../../../service-infrastructure/docs/enhancements/monitored-resource-types.md)
- [`api/v1alpha1/billingaccountbinding_types.go`](../../api/v1alpha1/billingaccountbinding_types.go)
- [`internal/controller/billingaccountbinding_controller.go`](../../internal/controller/billingaccountbinding_controller.go)
- Amberflo Ingest — <https://docs.amberflo.io/reference/post_ingest>
- Amberflo Usage Query — <https://docs.amberflo.io/reference/post_usage>
- Amberflo Late Arriving Events — <https://docs.amberflo.io/docs/late-arriving-events>
- Stripe Billing Meters — <https://docs.stripe.com/api/billing/meter-event>
- Metronome Ingest — <https://docs.metronome.com/api/#tag/usage/POST/ingest>
