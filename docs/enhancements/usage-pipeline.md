# Enhancement: Durable Usage Pipeline

**Status:** Draft for stakeholder review
**Author:** Billing platform team
**Scope:** The path from a usage event arriving at the platform edge to a submitted record at an external billing provider. Complements [`MeterDefinition`](../../../service-infrastructure/docs/enhancements/metering-definitions.md) and the existing `BillingAccount` / `BillingAccountBinding` resources. Emission SDK and reconciliation are covered in separate enhancements.

---

## 1. Why this exists

`MeterDefinition` declares *what can be metered*. `BillingAccountBinding` declares *who pays for a project*. Nothing today actually moves a usage event from a service to an invoice line — and no service is billing usage yet.

That gap is the opportunity. If we don't fill it, the first team that needs to bill will call a provider API directly from application code, the second will copy and diverge, and by the third we have three incomplete pipelines to unwind. Defining this layer once — before any team ships usage billing — is materially cheaper than rationalising later.

This document proposes the **durable usage pipeline**: a platform-owned path that accepts usage events at the ingestion edge, attributes them to the right billing account, and reports them to an external provider (Amberflo in v0).

It is about faithful transport of usage. It does **not** calculate money, own the meter catalog, replace observability metrics, define the emission SDK, or own reconciliation against what the provider saw.

---

## 2. Standards alignment

The pipeline sits at the intersection of several emerging standards. Aligning with them now avoids costly migration later and gives service teams familiar contracts.

### Four-layer stack

| Layer | Standard | How we use it |
|-------|----------|---------------|
| **Event envelope** | [CloudEvents v1.0](https://github.com/cloudevents/spec) (CNCF, graduated) | Wire format for all usage events. Native [JetStream binding](https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2) in the Go SDK; HTTP and Kafka bindings available for transport migration. SDKs in Go, Python, Java, Node, C#, Rust — producers import a well-known SDK, not a custom Milo library. |
| **Meter catalog** | [Google Service Control / MetricDescriptor](https://cloud.google.com/service-infrastructure/docs/service-control/reference/rest) pattern | `MeterDefinition` and `MonitoredResourceType` follow Google's model: reverse-DNS meter names, declared dimensions as label keys, `monitoredResourceTypes` linking meters to resource kinds. Billing values are **INT64 only** — integer arithmetic eliminates floating-point drift in financial calculations. The unit (e.g. cpu-*seconds*, not fractional cpu-hours) handles scale. |
| **Units** | [UCUM](https://ucum.org/) (Unified Code for Units of Measure) | Unit format for `MeterDefinition.measurement.unit`. Shared with Google Cloud MetricDescriptor and OpenTelemetry semantic conventions. Machine-readable and unambiguous. |
| **Billing export** | [FinOps FOCUS v1.3](https://focus.finops.org/) (Dec 2025) | Downstream export target. `MeterDefinition.billing` already uses FOCUS terminology (`consumedUnit`, `pricingUnit`). FOCUS does not mandate UCUM — it uses human-readable strings like `"GB-Hours"` — so a UCUM-to-FOCUS unit mapping layer is needed when exporting (v1+). |

### Industry positioning

The usage-based billing ecosystem has converged on a canonical pipeline shape — `Produce → Ingest → Validate → Dedup → Store → Attribute → Aggregate → Rate → Invoice` — with two fundamental approaches to aggregation:

| Approach | Description | Providers | Tradeoff |
|----------|-------------|-----------|----------|
| **Pre-aggregated windows** | Events bucketed into fixed time intervals (1-min, hourly) as they arrive | GCP Marketplace, Amberflo, Stripe+Metronome, OpenMeter, **this pipeline** | Simpler, matches downstream providers. Late-event correction requires bucket recomputation. |
| **Query-time aggregation** | Billing computed on demand from an immutable event log | Orb | Late events and amendments handled trivially. Requires an analytical database (ClickHouse/DuckDB). |

Our design follows the pre-aggregated approach, which matches all three hyperscalers and our v0 provider (Amberflo). The 30-day late-event window and replay capability (section 10) compensate for the correction limitations. Query-time aggregation is evaluated as an open question for v1+ (section 11).

| Capability | This pipeline | Amberflo | Stripe+Metronome | Orb | OpenMeter |
|------------|---------------|----------|-------------------|-----|-----------|
| Event format | CloudEvents | Proprietary | Proprietary | Proprietary | CloudEvents |
| Dedup mechanism | ULID eventID, end-to-end | Ingest-time | Event ID | Idempotency key + DB | Kafka stream (32d window) |
| Aggregation | Hour-bucketed (Sum, Count) | Real-time | Real-time | Query-time | 1-min tumbling windows |
| Durable log | NATS JetStream (v0) | Managed | Managed | Managed | Kafka + ClickHouse |
| Late events | 30-day window + replay | Supported | Supported | Backfill API | Dedup window |

---

## 3. The usage event envelope

Services emit usage events as [CloudEvents v1.0](https://github.com/cloudevents/spec/blob/main/cloudevents/spec.md) over HTTP or gRPC. No custom SDK — producers use the standard CloudEvents SDK for their language.

### Attribute mapping

| CloudEvents attribute | Usage | Example |
|---|---|---|
| `specversion` | Always `"1.0"` | `"1.0"` |
| `id` | ULID, producer-generated, end-to-end dedup key | `"01HQ3XTA4WV6E2C0Y7KQRBZ9MN"` |
| `type` | Meter name — matches a Published `MeterDefinition.spec.meterName` | `"compute.miloapis.com/instance/cpu-seconds"` |
| `source` | URI identifying the producing service | `"//compute.miloapis.com/controllers/instance-reconciler"` |
| `subject` | Project name — the project whose binding determines the payer | `"projects/p-abc"` |
| `time` | RFC 3339 timestamp — when the usage occurred | `"2026-04-14T14:30:00.000Z"` |
| `datacontenttype` | Always `"application/json"` | `"application/json"` |
| `data` | Usage payload (see below) | `{...}` |

### Data payload

```json
{
  "specversion": "1.0",
  "id": "01HQ3XTA4WV6E2C0Y7KQRBZ9MN",
  "type": "compute.miloapis.com/instance/cpu-seconds",
  "source": "//compute.miloapis.com/controllers/instance-reconciler",
  "subject": "projects/p-abc",
  "time": "2026-04-14T14:30:00.000Z",
  "datacontenttype": "application/json",
  "data": {
    "value": "42",
    "dimensions": {
      "region": "us-east-1",
      "tier": "standard"
    },
    "resource": {
      "group": "compute.miloapis.com",
      "kind": "Instance",
      "namespace": "default",
      "name": "instance-123",
      "uid": "a8f3a1b2-...",
      "labels": {
        "region": "us-east-1",
        "tier": "standard"
      }
    }
  }
}
```

### Design decisions

- **`value` is INT64, string-encoded in JSON.** Integer arithmetic eliminates floating-point drift in financial calculations. The unit handles scale: measure cpu-*seconds* rather than fractional cpu-hours. This matches Google's MetricDescriptor constraint (DELTA/INT64 only for billing).
- **`subject` identifies the billing target (project), not `source`.** `source` identifies the producing service. This follows [OpenMeter's convention](https://openmeter.io/docs/metering/events/usage-events), where `subject` is the billing entity. The Gateway can use `subject` for NATS subject-based routing.
- **`resource` is optional metadata.** Not all billable events originate from a single Kubernetes object (e.g., network egress between services, API request counts). When present, `resource` provides drill-down provenance and must conform to the `MonitoredResourceType` schema for the resource's Kind. When absent, the event is still valid for billing.
- **Dedup key is `id` alone, not `source` + `id`.** `id` is a ULID — globally unique by construction. Unlike OpenMeter's composite key, a single-field key simplifies dedup across pipeline stages and at the provider.

### Platform guarantees

- **At-least-once delivery with dedup.** A retry after a crash never double-bills — `id` is the settlement anchor end to end.
- **No silent drops.** Every rejected event surfaces on a health resource; nothing disappears into a log line.
- **2xx only after durable commit.** The caller knows the event is safe before moving on.

---

## 4. Ingestion Gateway

One lightweight Gateway per virtual project control plane, co-resident in the shared API server deployment. This is the only surface producers talk to.

### API surface

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/usage/events` | `POST` | Submit a single CloudEvents usage event |
| `/v1/usage/events:batchIngest` | `POST` | Submit a batch of CloudEvents (JSON array, up to 100 events) |

**Success:** `200 OK` with `{"accepted": <count>}`. Returned only after all events are durably committed to the log.

**Partial rejection:** `207 Multi-Status` with per-event results. Accepted events are committed; rejected events are returned with structured errors:

```json
{
  "accepted": 8,
  "rejected": [
    {"id": "01HQ3XTA4WV6E2C0Y7KQRBZ9MN", "reason": "UNKNOWN_METER", "detail": "no Published MeterDefinition for type 'compute.miloapis.com/nonexistent'"},
    {"id": "01HQ3XTB5XW7F3D1Z8LRSCB0NP", "reason": "UNSUPPORTED_AGGREGATION", "detail": "MeterDefinition 'compute.miloapis.com/instance/peak-memory' uses Max aggregation, which is not supported in the current pipeline version"}
  ]
}
```

**Backpressure:** `429 Too Many Requests` with `Retry-After` header when the durable log is full or write latency exceeds thresholds.

### Validation at the edge

- `id` must parse as ULID.
- `type` must match a Published `MeterDefinition`.
- **Aggregation fail-fast:** the matched `MeterDefinition` must use an aggregation type supported by the current pipeline version (Sum or Count in v0). Events for meters with unsupported aggregation types (Max, Min, UniqueCount, Latest, Average) are rejected synchronously. Events are never accepted into the log if they cannot be aggregated.
- `data.dimensions` keys must be a subset of the meter's declared dimensions.
- `data.value` must parse as INT64.
- If `data.resource` is present: `data.resource.labels` must conform to the `MonitoredResourceType` schema for the resource's Kind (see [`monitored-resource-types.md`](../../../service-infrastructure/docs/enhancements/monitored-resource-types.md); the pipeline is a consumer of that catalog at validation time).

Anything that fails validation is rejected synchronously with a structured error. Anything that passes is written to the durable log before the Gateway returns 2xx.

### Project isolation

Each project's events are published to a project-scoped NATS subject (`billing.usage.{project}.>`). Gateway authorization is scoped per-project using NATS credentials — a compromised or misconfigured Gateway cannot write events for a different project. This isolation boundary is enforced at the transport layer, not just at validation time.

---

## 5. Durable log

NATS JetStream in v0. All pipeline stages downstream of the Gateway read from and write to this log. Events and submission records live here, not in etcd — the [Operator Metering post-mortem](https://github.com/kube-reporting/metering-operator) is the canonical example of what happens when a billing pipeline treats etcd as a data plane.

The log is the source of truth for attribution, aggregation, and provider submission. It is also the surface that the replay workflow targets (see section 10).

CloudEvents are serialized to JetStream using the [CloudEvents NATS JetStream protocol binding](https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2), which provides typed publish/subscribe with at-least-once delivery out of the box. When the pipeline migrates to Kafka/Redpanda (triggered by sustained >10k events/sec), only the transport binding changes — the CloudEvents envelope and all pipeline stages remain identical.

---

## 6. Attribution

For every event, the pipeline looks at all `BillingAccountBinding` resources for the event's project (extracted from `subject`) — both `Active` and `Superseded` — sorted by `status.billingResponsibility.establishedAt`. That sequence forms a timeline of who owed the bill when. The event is attributed to whichever binding's interval contains the event's `time`. Rebinding mid-period is fully supported.

Bindings that still have events referencing their interval cannot be garbage-collected until those events are safely submitted — enforced via a pipeline-owned finalizer on `BillingAccountBinding`.

Events that arrive with no matching binding, or whose binding points at an archived account, are quarantined rather than silently retried or dropped. An operator is paged with the specific reason, and the failure is reflected on `UsageStreamHealth` (see section 8.2).

---

## 7. Aggregation

Attributed events are bucketed by `(customer, meter, hour)`. The aggregation stage joins against the `MeterDefinition` to resolve the aggregation function for each meter. In v0, supported aggregation types are `Sum` and `Count` — covering the near-term meter inventory. Buckets are written to the durable log and consumed by the reporting stage.

Events for meters with unsupported aggregation types never reach this stage — they are rejected at the Gateway (fail-fast, not fail-late). This means every event in the log is guaranteed to be aggregatable by the current pipeline version.

Additional aggregation types (e.g. `Max`, `UniqueCount`) are deferred to v1+.

---

## 8. The declarative surface

Two CRDs owned by this pipeline. All user-visible configuration lives here.

### 8.1 `ProviderIntegration`

One per upstream billing provider. Configures how batches are sent, how local accounts map to the provider's customer IDs, and reporting parameters.

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
  customerMapping:
    strategy: AnnotationLookup
    annotationKey: "billing.miloapis.com/amberflo-customer-id"
status:
  phase: Ready                               # Provisioning | Ready | Degraded | Suspended
  conditions: [...]
```

#### Customer mapping lifecycle

The `customerMapping` annotation is not manually applied — it is managed by the `ProviderIntegration` controller:

1. **Creation.** When a `BillingAccount` transitions to `Ready`, the controller creates a corresponding customer in the provider (e.g., Amberflo `POST /customers`) and writes the returned customer ID back as the annotation on the `BillingAccount`.
2. **Reconciliation.** The controller periodically verifies that every `Ready` `BillingAccount` has a valid provider customer ID annotation. Missing or stale mappings are repaired automatically and surfaced as conditions on `UsageStreamHealth`.
3. **Quarantine on failure.** Events attributed to a `BillingAccount` that is missing its provider customer ID are quarantined — same handling as attribution failures. They are retried automatically once the mapping is established.

### 8.2 `UsageStreamHealth`

A singleton-per-provider liveness CRD. One `kubectl get usagestreamhealth -A` is the operator's health dashboard.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: UsageStreamHealth
metadata:
  name: amberflo-prod
  namespace: billing-system
spec:
  providerIntegrationRef:
    name: amberflo-prod
status:
  ingestionRate: "142.3"                     # events/sec, 1-min rolling average
  backlogDepth: 1204                         # events accepted but not yet submitted to provider
  oldestUnsubmittedEvent: "2026-04-14T13:00:00Z"
  quarantine:
    attributionFailures: 0                   # events with no matching binding
    customerMappingFailures: 3               # events where BillingAccount lacks provider customer ID
  conditions:
    - type: Healthy
      status: "True"
    - type: BacklogGrowing
      status: "False"
    - type: SubmissionDegraded
      status: "False"
    - type: AttributionFailures
      status: "False"
    - type: CustomerMappingFailures
      status: "True"
      reason: MissingProviderCustomerId
      message: "3 events quarantined: BillingAccount 'acct-eu-prod' has no amberflo customer ID"
```

**Status conditions:**

| Condition | True when | Operator response |
|-----------|-----------|-------------------|
| `Healthy` | All other conditions are False | None |
| `BacklogGrowing` | Backlog depth increasing over 3 consecutive 1-min intervals | Investigate provider submission throughput; check `maxRequestsPerSecond` ceiling |
| `SubmissionDegraded` | Provider submission error rate >5% over 5 minutes | Check provider status page; verify credentials in `credentialsRef` |
| `AttributionFailures` | Any events quarantined due to missing bindings | Check for projects without a `BillingAccountBinding` |
| `CustomerMappingFailures` | Any events quarantined due to missing provider customer ID | Check `ProviderIntegration` controller logs; verify provider API access |

`UsageStreamHealth` is observational in v0. Automated circuit-breaking (e.g., pause submission on sustained errors to prevent wasting rate-limit budget) is deferred to v1+.

---

## 9. Deployment shape

Project control planes are virtualized within a shared API server deployment — not separate physical clusters. The pipeline follows that shape:

- **Per-project Ingestion Gateway.** One lightweight Gateway per virtual project control plane, co-resident in the shared deployment. Validates events at the edge, writes to the central log on a project-scoped NATS subject. No cross-cluster network hop.
- **Central data plane.** JetStream-backed durable log, plus stateless controllers for attribution, aggregation, and provider reporting.
- **Declarative surface in the billing cluster.** `ProviderIntegration` and `UsageStreamHealth` CRDs, plus the existing `BillingAccount` / `BillingAccountBinding`. `MeterDefinition` and `MonitoredResourceType` are consumed from the service infrastructure service.

---

## 10. Phased delivery

**v0 — revenue safe, feature minimal (one quarter).**

- Per-project Ingestion Gateway with edge validation and aggregation fail-fast.
- CloudEvents envelope over HTTP; batch endpoint for high-throughput producers.
- JetStream-backed durable log with project-scoped NATS subject isolation.
- Attribution against today's `BillingAccountBinding` shape, with the new retention finalizer.
- Hour-bucketed aggregation for `Sum` and `Count`.
- **Amberflo** as the only provider. `POST /ingest-batch` for submission. Known gaps (undocumented dedup window and rate limits) are compensated by strict local retention and a tunable rate ceiling.
- `ProviderIntegration` with automated customer mapping lifecycle (create on Ready, reconcile periodically).
- `UsageStreamHealth` with conditions for backlog, submission errors, attribution failures, and customer mapping failures.
- **Operator-triggered replay.** Time-range-scoped, single-provider. Replayed events are re-read from the durable log, re-aggregated, and re-submitted with the same idempotent `id` values so the provider deduplicates. Without replay, the first Amberflo outage, credential rotation, or aggregation bug has no recovery path — and the first real incident becomes the first untested recovery.

**Deferred to v1+:**

- Additional providers.
- Additional aggregation types (Max, Min, UniqueCount, Latest, Average).
- Kafka/Redpanda migration (triggered by sustained >10k events/sec). CloudEvents envelope and all pipeline stages are transport-agnostic — only the binding changes.
- Automated circuit-breaking on `UsageStreamHealth`.
- UCUM-to-FOCUS unit mapping for FinOps export.
- gRPC Gateway endpoint (alongside HTTP).

**Non-negotiable, even in v0:**

- CloudEvents v1.0 envelope with ULID `id` for end-to-end idempotency; no shortcuts.
- INT64 values only; no floating-point in the billing path.
- Attribution resolved from the binding graph, never self-declared by services.
- 2xx only after durable commit to the log.
- Retention finalizer on `BillingAccountBinding`.
- Aggregation fail-fast — events for unsupported meter aggregation types are rejected at the Gateway.

---

## 11. Open questions

1. **Amberflo operational envelope.** Rate limits, dedup-window duration, ingest-to-query lag — all undocumented. Calibrate in a dev tenant before v0 ships.
2. **Virtual control plane isolation model.** Exact mechanism (namespace-per-project, vcluster, multi-tenant API server layer) is unsettled and drives Gateway identity, credential scoping, and whether one Gateway Deployment can serve multiple projects.
3. **Second provider.** Stripe completed its acquisition of Metronome (Jan 2026), consolidating the aggregate-first provider space. Orb's query-based model is an architecturally distinct alternative. Product/finance call, not engineering — but the `ProviderIntegration` abstraction supports either direction.
4. **Per-event vs. hour-bucket dedup key at the provider.** Hour-bucket matches all three hyperscalers and is our default; per-event gives a finer audit trail at ~60x the key-space cost.
5. **Pre-aggregated windows vs. query-time aggregation.** The current design commits to pre-aggregated hour-buckets (section 7). Orb's query-time model — billing as a deterministic query over an immutable event log — handles late events and amendments trivially but requires an analytical database. Worth evaluating for v1+ if correction volume is high or if the 30-day late-event window proves insufficient.
6. **FOCUS export timing.** UCUM units (`"s"`, `"By"`, `"GBy.h"`) don't map 1:1 to FOCUS UnitFormat strings (`"Hours"`, `"GB-Hours"`). When does the mapping layer need to ship — with the first enterprise customer, or earlier?
7. **Cross-project billing.** The current design requires `subject` (project) to match the resource's project. This is a deliberate v0 simplification. Shared services billed to a central account (e.g., platform networking costs pro-rated across tenants) will need a different attribution model.

---

## References

- [`service-infrastructure/docs/enhancements/metering-definitions.md`](../../../service-infrastructure/docs/enhancements/metering-definitions.md)
- [`service-infrastructure/docs/enhancements/monitored-resource-types.md`](../../../service-infrastructure/docs/enhancements/monitored-resource-types.md)
- [`api/v1alpha1/billingaccountbinding_types.go`](../../api/v1alpha1/billingaccountbinding_types.go)
- [`internal/controller/billingaccountbinding_controller.go`](../../internal/controller/billingaccountbinding_controller.go)
- CloudEvents Spec — <https://github.com/cloudevents/spec>
- CloudEvents Go SDK (JetStream binding) — <https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2>
- FinOps FOCUS v1.3 — <https://focus.finops.org>
- Google Service Control — <https://cloud.google.com/service-infrastructure/docs/service-control/reference/rest>
- OpenMeter Usage Events (CloudEvents reference) — <https://openmeter.io/docs/metering/events/usage-events>
- Orb Query-Based Billing — <https://docs.withorb.com/architecture/query-based-billing>
- Amberflo Ingest — <https://docs.amberflo.io/reference/post_ingest>
- Amberflo Late Arriving Events — <https://docs.amberflo.io/docs/late-arriving-events>
- Stripe Billing Meters — <https://docs.stripe.com/api/billing/meter-event>
- Metronome Ingest — <https://docs.metronome.com/api/#tag/usage/POST/ingest>
