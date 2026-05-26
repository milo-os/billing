# Usage Metering

Diagnostic runbook for the usage metering pipeline:
[`MeterDefinition`](../../api/v1alpha1/meterdefinition_types.go) →
emission SDK → ingestion gateway → billing consumer → provider submission
controller → Amberflo. Four scenarios:

1. [Usage missing in Amberflo](#1-usage-missing-in-amberflo)
2. [Wrong account attributed](#2-wrong-account-attributed)
3. [Meter not registered](#3-meter-not-registered)
4. [Provider lag](#4-provider-lag)

All `datumctl` queries target the **milo control plane**. Org-scoped
resources (`BillingAccount`, `BillingAccountBinding`) live in
`organization-<orgname>`. Cluster-scoped resources (`MeterDefinition`,
`MonitoredResourceType`) have no namespace. The gateway, consumer, and
provider components live in the `billing-system` namespace.

Component map:

| Stage | Deployment (in `billing-system`) | Key metric |
|---|---|---|
| Ingest validation | `billing-gateway` | `billing_ingestion_rejections_total{reason}` |
| Central validation + attribution | `billing-controller-manager` | `billing_validation_rejections_total{project,reason}` |
| Malformed event drop | `billing-controller-manager` | `billing_consumer_malformed_total{reason}` |
| Provider submission | `billing-amberflo-provider` | provider-side counters |

Quarantine reasons emitted by the consumer:

| Reason | Meaning |
|---|---|
| `unknown_meter` | Event `type` doesn't match any `Published`/`Deprecated` `MeterDefinition`. |
| `invalid_dimensions` | Event carries dimension keys not declared on the matched `MeterDefinition`. |
| `attribution_failure` | No `Active` `BillingAccountBinding` for the event's project, or the matched account is not `Ready`. |

NATS subject scheme:

```
billing.usage.<project>.ingest                 # raw, post-gateway
billing.usage.<project>.valid                  # post-attribution, ready for provider
billing.usage.<project>.quarantine.<reason>    # held for operator action
```

---

## 1. Usage missing in Amberflo

Reported as: "The customer used the service, but I see nothing in
Amberflo (or in the staff-portal usage view, which reads from Amberflo)."

### 1.1 Triage: where did the event stop?

```sh
# Did the gateway accept anything for this project?
datumctl -n billing-system exec -it deploy/billing-gateway -- \
  curl -s localhost:8080/metrics \
  | grep 'billing_ingestion_events_total.*project="<project>"'

# Did the consumer quarantine anything?
datumctl -n billing-system exec -it deploy/billing-controller-manager -- \
  curl -s localhost:8080/metrics \
  | grep 'billing_validation_rejections_total.*project="<project>"'
```

- Gateway counter at 0 → events never reached the platform. Jump to
  §1.2 (SDK / Vector path).
- Gateway counter non-zero, no `valid` activity → consumer is dropping
  or quarantining. Jump to §1.3.
- Both counters healthy → events made it past the consumer. Jump to §4
  (provider lag).

### 1.2 Service is not emitting (or events never leave the node)

The emission SDK writes synchronously to the node-local Vector Agent. If
the Agent is down, `Record()` returns an error to the caller — usually
surfaced in service logs.

```sh
# Caller-side: is the service actually calling Record()? Check service logs
# for "recording usage" errors or look at the SDK's own dead-letter counter:
datumctl -n <service-ns> exec -it deploy/<service> -- \
  curl -s localhost:8080/metrics \
  | grep 'billing_sdk_dead_letter_total\|billing_sdk_record_total'

# Vector DaemonSet status (per-node):
datumctl -n logging get ds vector -o wide
datumctl -n logging logs ds/vector --tail=200 | grep -i billing
```

If the SDK shows zero `Record` calls, the service isn't emitting. Check
the service's deployment for the Recorder construction and the call
sites — common causes are a misconfigured endpoint or a feature gate
that hasn't been enabled in this environment.

### 1.3 Consumer quarantined the events

Each quarantine reason is a separate NATS subject. Inspect the queue
depth per reason:

```sh
# Stream-level: how deep is the quarantine?
datumctl -n nats exec -it sts/nats -- nats stream info BILLING_USAGE \
  --filter-subject='billing.usage.<project>.quarantine.>'
```

Look at the rejection counter broken down by reason to know *which*
quarantine to inspect:

```sh
datumctl -n billing-system exec -it deploy/billing-controller-manager -- \
  curl -s localhost:8080/metrics \
  | grep 'billing_validation_rejections_total' \
  | grep 'project="<project>"'
```

Route by reason:

| Top reason | Go to |
|---|---|
| `unknown_meter` | [§3](#3-meter-not-registered) |
| `invalid_dimensions` | The service is emitting dimensions not declared on the `MeterDefinition`. Either add the dimension to the meter (additive, safe) or fix the caller. |
| `attribution_failure` | [§2](#2-wrong-account-attributed), specifically §2.3 — no `Active` binding for the project. |

### 1.4 Replay quarantined events

Once the root cause is fixed (meter published, binding created, dimension
declared), replay the affected quarantine subject:

```sh
datumctl -n billing-system exec -it deploy/billing-controller-manager -- \
  billing replay \
    --project=<project> \
    --reason=<reason> \
    --since=<rfc3339-time>
```

The pipeline re-reads the quarantined events from the durable log and
re-publishes them onto `billing.usage.<project>.ingest`. ULID-based dedup
prevents double counting if the events made it through partially the
first time.

> Replay window is **30 days**. Events older than that are quarantined
> permanently — see [usage-pipeline.md](../enhancements/usage-pipeline.md#30-day-late-event-window).

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
- **No `Active` binding (project never bound).** Usage was quarantined
  with `attribution_failure`. Create the binding, then replay (§1.4).

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
  -l "" -o json \
  | jq '.items[] | select(.spec.projectRef.name=="<project>")'
```

If this returns empty, every event for the project is being quarantined
with `attribution_failure`. Create the binding (see the [operator
guide](https://github.com/datum-cloud/staff-portal/blob/main/docs/operators/billing-accounts.md))
and replay (§1.4).

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
datumctl -n billing-system logs deploy/billing-controller-manager --tail=200 \
  | grep -i 'meterdefinition\|cache'
```

A healthy log shows periodic informer sync messages. If you see repeated
informer errors against the milo apiserver, that's a control-plane
issue, not a billing one — escalate.

### 3.4 Provider sync (Amberflo side)

Even if the consumer accepts events for a `Published` meter, they will
not land in Amberflo until the provider has synced the meter as a
`meterApiName`. The provider uses `MeterDefinition.metadata.uid` (not
`spec.meterName`) as the Amberflo `meterApiName` for charset/length
safety.

```sh
datumctl -n billing-system logs deploy/billing-amberflo-provider --tail=200 \
  | grep -i '<meter-name>\|<meter-uid>'
```

Look for "meter created" / "meter updated" log lines for the relevant
UID. If absent, the provider hasn't synced this meter — usually a
provider credentials or Amberflo API issue. Escalate with the meter
name, UID, and the provider's recent logs.

---

## 4. Provider lag

Reported as: "Usage view shows yesterday's numbers, not today's." Or:
"Amberflo is behind by several hours."

The pipeline does not guarantee real-time. The provider submission
controller batches and submits asynchronously; Amberflo itself ingests
and processes with its own latency. A small lag (minutes) is normal.
Multi-hour lag is not.

### 4.1 Is it the pipeline or the provider?

```sh
# Pipeline-side: events leaving the consumer's "valid" stream
datumctl -n nats exec -it sts/nats -- nats stream info BILLING_USAGE \
  --filter-subject='billing.usage.*.valid'

# Provider-side: queue depth and submission errors
datumctl -n billing-system exec -it deploy/billing-amberflo-provider -- \
  curl -s localhost:8080/metrics \
  | grep -E 'amberflo_(submission_lag_seconds|submission_errors_total|queue_depth)'
```

- High pipeline queue depth, low provider queue depth → upstream
  (consumer or NATS) is the bottleneck. Inspect consumer logs.
- Low pipeline queue depth, high provider queue depth → provider
  controller is the bottleneck. Inspect provider logs.
- Both low, but Amberflo console still behind → Amberflo-side
  processing lag. Confirm in the Amberflo console; if multi-hour,
  open a vendor ticket.

### 4.2 Provider submission errors

```sh
datumctl -n billing-system logs deploy/billing-amberflo-provider --tail=200 \
  | grep -i 'error\|429\|5[0-9][0-9]'
```

Common patterns:

- **`429`** — Amberflo rate-limiting. The provider retries with backoff;
  if persistent, reduce batch frequency or coordinate with Amberflo on
  rate limits.
- **`401`/`403`** — provider credentials issue. The Amberflo API key
  lives in a Kubernetes Secret in `billing-system`; check it hasn't
  rotated or been wiped.
- **`unknown customerId`** — `BillingAccount` reached the consumer but
  its provider-side sync hasn't created the Amberflo customer yet.
  Check `BillingAccount.status.phase` (§ operator guide).

### 4.3 Confirm in the Amberflo console

The staff-portal usage view queries the Amberflo `/usage` endpoint with
`customerId` = `BillingAccount.metadata.uid`. To rule out a staff-portal
caching/query bug, hit the Amberflo console directly:

1. Look up the org's `BillingAccount.metadata.uid`:
   ```sh
   datumctl -n organization-<orgname> get billingaccount <name> \
     -o jsonpath='{.metadata.uid}'
   ```
2. In the Amberflo console, query usage filtered by that `customerId`
   and the same meter `meterApiName` (which is the
   `MeterDefinition.metadata.uid`).
3. If Amberflo shows the data but the staff portal doesn't → staff
   portal bug. If neither shows it → provider lag or upstream pipeline
   issue.

---

## Quick reference

| Symptom | First check |
|---|---|
| Nothing in the usage view, gateway counter at 0 | Service is not emitting / Vector is down (§1.2) |
| Nothing in the usage view, gateway counter healthy | Consumer quarantine depth by reason (§1.3) |
| `unknown_meter` quarantine | `MeterDefinition` exists and `Published` (§3) |
| `invalid_dimensions` quarantine | Service emitting undeclared dimension keys (§1.3 table) |
| `attribution_failure` quarantine | `BillingAccountBinding` `Active` for the project (§2.3) |
| Wrong `BillingAccount` showing usage | `Active` binding currently points to the wrong account (§2.1) |
| Amberflo lag > a few minutes | Pipeline vs. provider queue depth (§4.1) |
| Provider logs show 401/403 | Amberflo API key Secret in `billing-system` (§4.2) |
| Staff portal and Amberflo console disagree | Confirm against Amberflo directly with raw `customerId`/`meterApiName` (§4.3) |

## Cross-references

- Pipeline design: [usage-pipeline.md](../enhancements/usage-pipeline.md)
- Service developer guide: [emitting-usage.md](../emitting-usage.md)
- Operator guide:
  [staff-portal/docs/operators/billing-accounts.md](https://github.com/datum-cloud/staff-portal/blob/main/docs/operators/billing-accounts.md)
