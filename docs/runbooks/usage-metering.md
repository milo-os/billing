# Usage Metering

Diagnostic runbook for the usage metering pipeline:
[`MeterDefinition`](../../api/v1alpha1/meterdefinition_types.go) →
emission SDK → ingestion gateway → billing consumer → openmeter-provider
submission consumer → OpenMeter. Four scenarios:

1. [Usage missing](#1-usage-missing)
2. [Wrong account attributed](#2-wrong-account-attributed)
3. [Meter not registered](#3-meter-not-registered)
4. [Provider lag](#4-provider-lag)

`datumctl` queries against billing resources target the **milo control
plane**. Org-scoped resources (`BillingAccount`, `BillingAccountBinding`)
live in `organization-<orgname>`. Cluster-scoped resources
(`MeterDefinition`, `MonitoredResourceType`) have no namespace. The
gateway and consumer run in `billing-system`; openmeter-provider and
OpenMeter run in `openmeter-system`. `kubectl` commands that read logs
from or exec into those workloads target the infrastructure cluster they
run on.

Billing and openmeter-provider serve their metrics over HTTPS with
authentication, so query the metrics below in Prometheus rather than by
curling a pod.

Component map:

| Stage | Deployment / namespace | Key metric |
|---|---|---|
| Ingest validation | `billing-gateway` (`billing-system`) | `billing_ingestion_rejections_total{reason}` |
| Unbound usage dropped at ingest | `billing-gateway` (`billing-system`) | `billing_ingestion_dropped_total{reason="attribution_failure"}` |
| Central validation + attribution | `billing-controller-manager` (`billing-system`) | `billing_validation_rejections_total{project,reason}` |
| Malformed event drop | `billing-controller-manager` (`billing-system`) | `billing_consumer_malformed_total{reason}` |
| Customer, meter, plan and subscription sync to OpenMeter | `openmeter-provider-manager` (`openmeter-system`) | Events on the synced resource (`Synced` / `SyncFailed`) |
| Usage submission to OpenMeter | `submission-consumer` (`openmeter-system`) | `billing_submission_requests_total{status}`, `billing_pipeline_oldest_unsubmitted_seconds` |
| Usage reads (`UsageQuery`) | `openmeter-usage-apiserver` (`openmeter-system`) | — |

The submission consumer keeps the metric names amberflo-provider used, so
dashboards and alerts built on them still work.

Quarantine reasons emitted by the consumer:

| Reason | Meaning |
|---|---|
| `unknown_meter` | Event `type` doesn't match any `Published`/`Deprecated` `MeterDefinition`. |
| `invalid_dimensions` | Event carries dimension keys not declared on the matched `MeterDefinition`. |
| `attribution_failure` | No `Active` `BillingAccountBinding` for the event's project, or the matched account is not `Ready`. Rare now that the gateway drops unbound usage (§2.3). |
| `invalid_value` | `data.value` is missing or isn't an integer string. |
| `malformed_data` | Event data doesn't match the wire schema for another reason (for example a dimension value that isn't a string). |

NATS subject scheme:

```
billing.usage.<project>.ingest                 # raw, post-gateway
billing.usage.<project>.valid                  # post-attribution, ready for provider
billing.usage.<project>.quarantine.<reason>    # held for operator action
```

---

## 1. Usage missing

Reported as: "The customer used the service, but I see nothing in the
usage view." Both portals' usage views read
[`UsageQuery`](../enhancements/usage-query.md), which
openmeter-provider's usage API server answers from OpenMeter.

### 1.1 Triage: where did the event stop?

```promql
# Did the gateway accept anything for this project?
sum(rate(billing_ingestion_events_total{project="<project>"}[1h]))

# Is the gateway dropping usage because a project has no billable account?
sum by (reason) (rate(billing_ingestion_dropped_total[1h]))

# Did the consumer quarantine anything?
sum by (reason) (rate(billing_validation_rejections_total{project="<project>"}[1h]))
```

- Gateway accepting nothing for the project → either it's dropping the
  project's usage (check the binding, §2.3) or events never reached the
  platform (§1.2, SDK / Vector path).
- Gateway accepting, consumer rejecting → consumer is quarantining.
  Jump to §1.3.
- Both healthy → events made it past the consumer. Jump to §4
  (provider lag).

### 1.2 Service is not emitting (or events never leave the node)

The emission SDK writes synchronously to the node-local Vector Agent. If
the Agent is down, `Record()` returns an error to the caller — usually
surfaced in service logs.

```promql
# Caller-side: the SDK's two error counters. Both should be near zero in a
# healthy service; sustained increments mean events are not making it to
# Vector.
sum(rate(billing_sdk_record_errors_total{namespace="<service-ns>"}[1h]))
sum(rate(billing_sdk_rejected_total{namespace="<service-ns>"}[1h]))
```

```sh
# Vector DaemonSet status (per-node):
kubectl -n logging get ds vector -o wide
kubectl -n logging logs ds/vector --tail=200 | grep -i billing
```

What the two counters mean:

- `billing_sdk_record_errors_total` — `Record()` returned an error
  *after* exhausting its retry budget. Almost always means the local
  Vector Agent is unreachable. Confirm with the Vector logs on the
  affected node.
- `billing_sdk_rejected_total` — events permanently dropped by the
  endpoint (HTTP 4xx, not 429). The CloudEvent envelope was malformed
  in a way structural validation missed — a producer bug. Check
  service logs for the rejection detail and fix the call site.

If both counters are zero but no events are arriving, the service
isn't calling `Record()` at all. The SDK doesn't expose a success
counter, so confirm from service logs or by exercising the path
manually — common causes are a misconfigured endpoint or a feature
gate that hasn't been enabled in this environment.

### 1.3 Consumer quarantined the events

Each quarantine reason is a separate NATS subject. Inspect the queue
depth per reason:

```sh
# Stream-level: how deep is the quarantine?
kubectl -n nats exec -it sts/nats -- nats stream info BILLING_USAGE \
  --filter-subject='billing.usage.<project>.quarantine.>'
```

The rejection counter from §1.1, broken down by reason, says *which*
quarantine to inspect. Route by reason:

| Top reason | Go to |
|---|---|
| `unknown_meter` | [§3](#3-meter-not-registered) |
| `invalid_dimensions` | The service is emitting dimensions not declared on the `MeterDefinition`. Either add the dimension to the meter (additive, safe) or fix the caller. |
| `invalid_value`, `malformed_data` | A producer bug: the event data doesn't match the wire schema. Fix the call site; see the [developer guide](../emitting-usage.md). |
| `attribution_failure` | [§2](#2-wrong-account-attributed), specifically §2.3 — no `Active` binding for the project. |

### 1.4 Replay quarantined events

> **Heads up:** an operator-triggered replay tool is described in the
> pipeline design but **not yet implemented** — there is no `billing
> replay` subcommand today. See the open risk in
> [usage-pipeline.md](../enhancements/usage-pipeline.md#risks-and-mitigations)
> ("Quarantine with no replay trigger"). Until that ships, replay is a
> manual operation against NATS JetStream.

Once the root cause is fixed (`ServiceConfiguration` published, binding
created, dimension declared), an operator with NATS access can
re-publish quarantined events onto the ingest subject. Sketch of the
manual flow:

```sh
# Subscribe to the quarantine subject for the affected project + reason,
# re-publish each message body to billing.usage.<project>.ingest.
kubectl -n nats exec -it sts/nats -- \
  nats stream view BILLING_USAGE \
  --subject='billing.usage.<project>.quarantine.<reason>' \
  --start-time=<rfc3339-time>
# …then re-publish the payloads to billing.usage.<project>.ingest, e.g.
# via `nats pub` or a one-off script. ULID-based dedup prevents double
# counting.
```

End-to-end idempotency means a replay can't double-bill: every event
carries the same ULID it was assigned at emission, and that ULID is the
dedup anchor at every downstream stage, including OpenMeter.

> Replay window is **30 days**. Events older than that are quarantined
> permanently — see [usage-pipeline.md](../enhancements/usage-pipeline.md#30-day-late-event-window).

Usage the gateway dropped for an unbound project (§2.3) never reached
NATS, so there is nothing to replay.

Until the replay tool ships, treat any large-scale replay as an
engineering-assisted operation rather than a routine support task.

---

## 2. Wrong account attributed

Reported as: "Usage for project Foo is showing up against the wrong
`BillingAccount`."

### 2.1 Find the active binding

```sh
datumctl -n organization-<orgname> get billingaccountbindings \
  -o custom-columns=NAME:.metadata.name,ACCOUNT:.spec.billingAccountRef.name,PROJECT:.spec.projectRef.name,PHASE:.status.phase
```

There should be exactly one `Active` binding per project. Any others for
the same project should be `Superseded`.

Two failure modes:

- **Wrong `Active` binding.** Create a new binding pointing at the
  correct account. The old one auto-transitions to `Superseded`; new
  usage flows to the new account from
  `status.billingResponsibility.establishedAt` forward.
- **No `Active` binding (project never bound).** See §2.3.

### 2.2 Verify the establishedAt cutover

When a project is rebound mid-period, usage attribution splits at the
new binding's `establishedAt`. Confirm both:

```sh
datumctl -n organization-<orgname> get billingaccountbinding <new-binding> \
  -o jsonpath='{.status.billingResponsibility.establishedAt}'

datumctl -n organization-<orgname> get billingaccountbinding <old-binding> \
  -o jsonpath='{.status.phase}'   # expect: Superseded
```

Usage timestamped *before* `establishedAt` is correctly attributed to the
old account. This is by design — do not "fix" historical attribution by
deleting bindings. The audit trail of who-paid-when lives in the binding
history.

### 2.3 Project has no binding at all

```sh
datumctl -n organization-<orgname> get billingaccountbindings \
  -o json \
  | jq '.items[] | select(.spec.projectRef.name=="<project>")'
```

If this returns empty, the gateway drops every event for the project
before it reaches NATS (counted in
`billing_ingestion_dropped_total{reason="attribution_failure"}`) and
answers HTTP 200 so Vector doesn't retry. The same happens while the
bound account is not `Ready`. That usage is not billed and can't be
replayed. Events that get past the gateway anyway (for example while its
cache catches up) are quarantined by the consumer with
`attribution_failure`, and those can be replayed (§1.4).

Create the binding (see the [operator
guide](https://github.com/datum-cloud/staff-portal/blob/main/docs/operators/billing-accounts.md));
usage is billed from then on.

### 2.4 Audit trail

The `BillingAccountBinding` create/delete is in the milo apiserver audit
log. Pull by `objectRef.resource=billingaccountbindings` and the org
namespace. This is the artifact for an incident write-up or customer
comms — never reconstruct the history from `Active`/`Superseded` state
alone, since that state is mutated by the controller.

---

## 3. Meter not registered

Reported as: "The service says it's emitting `<meter-name>` but nothing
is landing." Or: quarantine reason `unknown_meter` is dominant in §1.3.

### 3.1 Does the `MeterDefinition` exist?

```sh
datumctl get meterdefinitions \
  -o custom-columns=NAME:.metadata.name,METER:.spec.meterName,PHASE:.spec.phase
```

The consumer matches the event's CloudEvents `type` to
`MeterDefinition.spec.meterName`. If your meter is missing here, the
service team needs to ship it (see the [developer
guide](../emitting-usage.md)).

### 3.2 Is the meter `Published`?

The consumer only accepts events for `MeterDefinition`s in `Published`
(and `Deprecated`) phase. A `Draft` meter exists in the catalog but its
events are quarantined.

```sh
datumctl get meterdefinition <name> -o jsonpath='{.spec.phase}'
```

If `Draft`, the meter is still under development. Either bump it to
`Published`:

```sh
datumctl patch meterdefinition <name> --type=merge -p '{"spec":{"phase":"Published"}}'
```

…or accept that quarantine until the service team is ready.

### 3.3 Is the consumer's cache fresh?

The consumer holds an informer-backed in-memory `MeterDefinitionCache` to
keep the hot path free of API server calls. A meter just transitioned to
`Published` should be picked up within a few seconds; if it isn't, the
informer may be wedged.

```sh
kubectl -n billing-system logs deploy/billing-controller-manager --tail=200 \
  | grep -i 'meterdefinition\|cache'
```

A healthy log shows periodic informer sync messages. If you see repeated
informer errors against the milo apiserver, that's a control-plane
issue, not a billing one — escalate.

### 3.4 Provider sync (OpenMeter side)

Even if the consumer accepts events for a `Published` meter, OpenMeter
doesn't count them until openmeter-provider has created the meter there.
The meter's OpenMeter key (its slug) is the `meterName` lowercased, with
every run of other characters turned into `_`
(`compute.miloapis.com/instance/cpu-seconds` becomes
`compute_miloapis_com_instance_cpu_seconds`). Names longer than 64
characters are cut short and end in a hash. The meter's event type is
the `meterName` itself, so events route by name, not by slug.

openmeter-provider records the outcome as Events on the `MeterDefinition`:

```sh
datumctl describe meterdefinition <name>   # look for Synced or SyncFailed

kubectl -n openmeter-system logs deploy/openmeter-provider-manager --tail=200 \
  | grep -i '<meter-name>'
```

A `SyncFailed` message starts with `Transient`, `Permanent` or `Invalid`.
`Transient` retries on its own. The other two need a change: for example
two `meterName`s that turn into the same slug, or a meter OpenMeter
can't express. Escalate with the meter name and the event message.

---

## 4. Provider lag

Reported as: "Usage view shows yesterday's numbers, not today's."

The pipeline does not guarantee real-time. The submission consumer
batches and submits asynchronously, and OpenMeter aggregates with its own
latency. A small lag (minutes) is normal. Multi-hour lag is not.

### 4.1 Is it the pipeline or the provider?

The single most useful number is
`billing_pipeline_oldest_unsubmitted_seconds` — a gauge tracking the age
of the oldest fetched-but-unacked event in the current submission batch.
A sustained non-zero value means the submission consumer is stalled.

```promql
max(billing_pipeline_oldest_unsubmitted_seconds)
sum by (status) (rate(billing_submission_requests_total[15m]))
```

```sh
# NATS-side: depth of the valid stream feeding the submission consumer
kubectl -n nats exec -it sts/nats -- nats stream info BILLING_USAGE \
  --filter-subject='billing.usage.*.valid'
```

`billing_submission_requests_total` carries a `status` label:

| `status` | Meaning | The event |
|---|---|---|
| `success` | OpenMeter accepted it | acked |
| `transient` | 5xx, 429 or network error | retried |
| `config_error` | OpenMeter refused the request for a reason that isn't the event's (401/403/404/...), such as a wrong URL | retried |
| `unattributed` | The submission consumer can't find the `BillingAccount` billing attributed the event to (not in its cache yet, or the event carries no attribution) | retried |
| `permanent` | Malformed, no subject, or OpenMeter rejected it as invalid (400/422) | **dropped** |
| `max_deliveries_exceeded` | JetStream stopped redelivering after the maximum number of attempts | **not submitted; replay it** |

Read it like:

- `success` dominant, gauge near 0 → healthy; the lag is OpenMeter's or
  the reader's.
- `transient` climbing, gauge climbing → OpenMeter is overloaded or
  unreachable; the consumer is backing off and retrying. Check OpenMeter
  (§4.2).
- `config_error` at any sustained rate → needs an operator: check the
  OpenMeter URL in openmeter-provider's config and that OpenMeter is up.
- `unattributed` sustained → the consumer can't resolve billing
  accounts; check its logs (§4.2).
- `permanent` climbing → events are being acked and dropped. Inspect
  logs to learn which payloads.
- `max_deliveries_exceeded` → some usage gave up retrying and was not
  submitted. Fix the cause, then replay it from NATS (§1.4).
- Valid-stream depth high, gauge near 0 → submission consumer isn't
  pulling. Check the deployment is running and connected to NATS.

### 4.2 Submission errors

```sh
kubectl -n openmeter-system logs deploy/submission-consumer --tail=200 \
  | grep -i 'error\|transient\|discarding\|not attributed'

# OpenMeter itself
kubectl -n openmeter-system get pods
```

Common patterns:

- **`transient OpenMeter ingest error`** — OpenMeter returned 5xx/429 or
  couldn't be reached. Retries on its own; if it persists, check the
  OpenMeter pods and the Kafka and ClickHouse behind them.
- **`OpenMeter refused the ingest request for a non-event reason`** —
  counted as `config_error`. The consumer is pointed at the wrong URL, or
  OpenMeter is refusing it.
- **`usage event not attributed to a billing account yet`** — counted as
  `unattributed`. The event names a `BillingAccount` the consumer can't
  find. Usually clears once the account reaches its cache; if not,
  confirm the account exists and the project's binding is in the same
  namespace.
- **`OpenMeter rejected usage event as invalid; discarding it`** —
  counted as `permanent`. The log line carries the event ID, subject and
  type.

### 4.3 Confirm against OpenMeter

The usage views read `UsageQuery`, which openmeter-provider answers from
OpenMeter. To rule out a portal or `UsageQuery` bug, query OpenMeter
directly. The submission consumer sets each event's subject to the
`BillingAccount` UID, which is also the account's OpenMeter customer key.

1. Look up the org's `BillingAccount.metadata.uid`:
   ```sh
   datumctl -n organization-<orgname> get billingaccount <name> \
     -o jsonpath='{.metadata.uid}'
   ```
2. OpenMeter's API has no authentication and only openmeter-provider
   can reach it, so port-forward to it and query the meter (its slug is
   described in §3.4):
   ```sh
   kubectl -n openmeter-system port-forward svc/openmeter-api 8888:80
   curl -s 'localhost:8888/api/v1/meters/<meter-slug>/query?subject=<account-uid>&windowSize=DAY'
   ```
   Only read through this API. openmeter-provider owns everything in
   OpenMeter, and a change made by hand is either overwritten or breaks
   its sync.
3. If OpenMeter shows the data but the portal doesn't → a `UsageQuery`
   or portal bug. If neither shows it → provider lag or an upstream
   pipeline issue.

---

## Quick reference

| Symptom | First check |
|---|---|
| Gateway accepting nothing for the project | Binding (§2.3), then service emission / Vector (§1.2) |
| `billing_ingestion_dropped_total{reason="attribution_failure"}` rising | A project has no `Active` binding to a `Ready` account (§2.3) |
| Gateway accepting, nothing in the usage view | Consumer quarantine depth by reason (§1.3) |
| `unknown_meter` quarantine | `MeterDefinition` exists and `Published` (§3) |
| `invalid_dimensions` quarantine | Service emitting undeclared dimension keys (§1.3 table) |
| `invalid_value` / `malformed_data` quarantine | Producer bug in the event data (§1.3 table) |
| `attribution_failure` quarantine | `BillingAccountBinding` `Active` for the project (§2.3) |
| Wrong `BillingAccount` showing usage | `Active` binding currently points to the wrong account (§2.1) |
| `MeterDefinition` has a `SyncFailed` event | openmeter-provider can't create the meter in OpenMeter (§3.4) |
| Usage view lag > a few minutes | Pipeline vs. provider queue depth (§4.1) |
| `billing_submission_requests_total{status="config_error"}` | OpenMeter URL or availability (§4.1, §4.2) |
| `billing_submission_requests_total{status="permanent"}` climbing | Submission consumer dropping events OpenMeter rejected (§4.1) |
| `billing_submission_requests_total{status="max_deliveries_exceeded"}` | Usage gave up retrying; fix and replay (§4.1, §1.4) |
| `billing_pipeline_oldest_unsubmitted_seconds` sustained non-zero | Submission consumer stalled (§4.1) |
| Portal and OpenMeter disagree | Query OpenMeter directly with the account UID and meter slug (§4.3) |

## Cross-references

- Pipeline design: [usage-pipeline.md](../enhancements/usage-pipeline.md)
- Usage reads: [usage-query.md](../enhancements/usage-query.md)
- Service developer guide: [emitting-usage.md](../emitting-usage.md)
- Provider: [openmeter-provider](https://github.com/milo-os/openmeter-provider)
- Operator guide:
  [staff-portal/docs/operators/billing-accounts.md](https://github.com/datum-cloud/staff-portal/blob/main/docs/operators/billing-accounts.md)
