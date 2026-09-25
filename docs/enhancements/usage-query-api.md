---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Billable Usage API

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [How It Works](#how-it-works)
  - [User Stories](#user-stories)
  - [Key Capabilities](#key-capabilities)
  - [Notes / Constraints / Caveats](#notes--constraints--caveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [API Surface](#api-surface)
  - [Response Schema](#response-schema)
    - [Field Inventory: What Every Current Portal Field Maps To](#field-inventory-what-every-current-portal-field-maps-to)
    - [Provider Portability Guarantee](#provider-portability-guarantee)
  - [Server Architecture](#server-architecture)
  - [Provider Query Interface](#provider-query-interface)
  - [Authentication](#authentication)
  - [Migration Path](#migration-path)
- [Implementation History](#implementation-history)
- [Future Work](#future-work)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
  - [Billing Service Holds the Amberflo Key Directly](#billing-service-holds-the-amberflo-key-directly)
  - [Shared Client Package Instead of a Server API](#shared-client-package-instead-of-a-server-api)
- [References](#references)

## Summary

Introduce a `billing usage-api` server — sibling to the existing `billing
gateway` — that exposes usage and cost data as normalized, FOCUS-aligned rows
scoped by billing account. It is the read-side counterpart to the durable
[usage pipeline][usage-pipeline]: the pipeline gets usage *into* an external
provider without any caller touching provider credentials; this proposal gets
usage *out* the same way. `cloud-portal`, `staff-portal`, and eventually
`datumctl` become normal authenticated callers of one endpoint instead of each
holding a provider API key and re-implementing normalization independently.

## Motivation

Today, usage data flows out of the platform through a path that bypasses the
billing service entirely. Both `cloud-portal` (customer-facing) and
`staff-portal` (internal ops) hold a server-side Amberflo API key
(`AMBERFLO_API_KEY` / `amberfloApiKey`) and call Amberflo's `/usage` endpoint
directly, passing `customerId = BillingAccount.metadata.uid` and
`meterApiName = MeterDefinition.metadata.uid`. The [usage-metering
runbook][runbook] documents this explicitly: *"The staff-portal usage view
queries the Amberflo `/usage` endpoint with `customerId` =
`BillingAccount.metadata.uid`."* `cloud-portal` does the same thing.

This is the exact coupling the [usage pipeline enhancement][usage-pipeline]
already ruled out on the write side: *"The Provider Submission Controller is
the only component that holds provider credentials or speaks the provider
API."* That principle was never applied to reads. The result:

- **A shared, unscoped provider credential sits in a customer-facing BFF.**
  `cloud-portal`'s server environment holds an Amberflo key capable of
  querying usage for any customer, not just the one making the request. A
  portal compromise exposes every customer's usage data, not just the
  requester's.
- **Normalization is duplicated and drifting.** `cloud-portal`'s
  `usage.server.ts` independently resolves `MeterDefinition` catalog metadata,
  aggregates Amberflo's daily series, builds per-dimension breakdowns,
  zero-fills empty windows, and derives a human-readable "service group" from
  the meter name prefix. `staff-portal` does its own version of the same
  logic. Every frontend that wants usage data re-derives Datum's own meter
  catalog from Amberflo's response shape.
- **No path exists for `datumctl`.** There is no CLI usage command today
  because there is nothing for it to call that doesn't require an Amberflo
  key of its own.
- **Provider swap risk is real but invisible on the read side.** If Amberflo
  is ever replaced, the write path degrades gracefully (deploy a new
  Submission Controller). The read path breaks in every frontend that talks
  to Amberflo directly.

Cloudflare's [Billable Usage API][cloudflare-post] is useful prior art for the
shape of the fix, not just the motivation: a stable, versioned endpoint that
returns FOCUS-aligned field names, decoupling API consumers from the
underlying metering and rating implementation.

### Goals

- Ship a new, authenticated `GET` endpoint on the billing service, scoped by
  billing account, that returns normalized usage rows — no caller needs to
  know Amberflo exists.
- **Field-complete relative to what the portals show today.** Every field
  `cloud-portal`/`staff-portal` currently surface from Amberflo has a home in
  the normalized schema (see [Field Inventory](#field-inventory-what-every-current-portal-field-maps-to))
  so the migration is not a regression, and no provider-specific identifier
  (Amberflo's `meterApiName`/`customerId`) leaks into the public response —
  see [Provider Portability Guarantee](#provider-portability-guarantee).
- Keep the billing service itself provider-agnostic: it never holds an
  Amberflo credential. It calls a new **internal** query interface that
  `amberflo-provider` implements, symmetric with how the write path already
  isolates provider credentials to the Submission Controller.
- Support all three callers explicitly in scope: `cloud-portal` (customer
  session), `staff-portal` (staff session), and `datumctl` (a Datum
  service-account/API token) — via whatever bearer-token/IAM identity these
  callers already present to the Milo control plane, not a new shared secret.
- Give `cloud-portal` and `staff-portal` a migration path off direct Amberflo
  access, ending with `AMBERFLO_API_KEY` / `amberfloApiKey` removed from both.
- Ship `datumctl usage` in the same rollout wave as the endpoint — not a
  deferred follow-up. The CLI is the first genuinely programmatic caller and
  the clearest test that the API is provider-agnostic and self-serve-shaped,
  not just a portal-shaped internal shim.

### Non-Goals

- **Implementing the `amberflo-provider`-side query endpoint.** That repo is
  not present in this workspace; this document defines the interface contract
  it must satisfy, but the implementation is a companion change tracked
  separately.
- **Writing the `datumctl usage` Go code itself.** Lives in
  `datum-cloud/datumctl` ([companion issue][datumctl-usage-issue]), but is
  scoped to ship alongside this endpoint, not after it — see
  [Migration Path](#migration-path).
- **Implementing the `cloud-portal` / `staff-portal` migration PRs.** Tracked
  as companion follow-ups in those repos.
- **Platform-side usage aggregation or storage.** Already flagged as deferred
  future work in [usage-pipeline.md][usage-pipeline]; this proposal continues
  relying on provider-side (Amberflo) aggregation for v0.
- **Cost and pricing data.** Depends on the pricing engine (rates, tiers,
  currencies) landing separately. Cost fields are reserved in the response
  schema but not populated until that ships.
- **Full FOCUS conformance, multi-provider routing, sub-daily data.** Same v0
  boundary the pipeline enhancement already drew for the write path.
- **Quota (`AllowanceBucket`) limit/used join.** Stays a portal-side concern
  for now; candidate future work.

## Proposal

### How It Works

A caller — `cloud-portal`, `staff-portal`, or `datumctl` — makes an
authenticated `GET` request to the billing service's usage API, scoped to a
billing account and an optional time range. The server resolves the billing
account and its meter catalog (`MeterDefinition`) from the existing CRD read
path, translates the request into a query against `amberflo-provider`'s
internal query interface (the only component that still speaks to Amberflo),
and normalizes the response into FOCUS-aligned rows before returning it.

```
Caller            billing usage-api         amberflo-provider          Amberflo
(cloud-portal/    (this repo)               (separate repo/           (external)
 staff-portal/                               deployment)
 datumctl)
   │                    │                        │                       │
   │ GET .../usage      │                        │                       │
   │───────────────────▶│                        │                       │
   │                    │ resolve BillingAccount │                       │
   │                    │ + MeterDefinition       │                       │
   │                    │ catalog (CRD reads)     │                       │
   │                    │                        │                       │
   │                    │ POST /internal/v1/     │                       │
   │                    │ usage:query            │                       │
   │                    │───────────────────────▶│                       │
   │                    │                        │  POST /usage          │
   │                    │                        │───────────────────────▶│
   │                    │                        │◀───────────────────────│
   │                    │◀───────────────────────│                       │
   │                    │ normalize to FOCUS-    │                       │
   │                    │ aligned rows           │                       │
   │◀───────────────────│                        │                       │
```

### User Stories

#### Story 1: Customer Checks Spend from the Portal

As a customer, I open the usage dashboard in `cloud-portal` and see today's
consumption per service, broken down by region. The portal calls the billing
service's usage endpoint with my billing account ID and gets back normalized
rows — it never touches Amberflo directly, and the credential that can see my
usage is scoped to my session, not a global key that could see anyone's.

#### Story 2: Staff Investigates a Billing Discrepancy

As platform staff, I need to see the same usage data a customer sees to
triage a support ticket. `staff-portal` calls the same endpoint with a staff
session token and gets the same normalized shape `cloud-portal` would show
the customer — no separate Amberflo integration to maintain.

#### Story 3: A Script Pulls Usage via the CLI

As a customer running cost automation, I run `datumctl billing usage
--billing-account=ba-abc --from=2026-08-01` and get the same normalized rows
back as JSON, suitable for piping into a script — the first programmatic path
to usage data that doesn't require staff to run a one-off query against
Amberflo's console.

### Key Capabilities

#### Provider-Agnostic by Construction

The billing service's usage API never imports an Amberflo client and never
sees an Amberflo credential. If Amberflo is replaced, only
`amberflo-provider`'s implementation of the internal query interface changes
— every caller of the public endpoint is unaffected.

#### One Normalization, Not Three

Meter catalog resolution, service-name derivation, and row shaping happen
once, server-side, using the same `MeterDefinition` catalog the billing
service already owns. `cloud-portal`, `staff-portal`, and `datumctl` all get
the same normalized shape instead of each re-deriving it from Amberflo's
response.

#### Scoped, Not Shared, Credentials

Callers authenticate with the identity they already carry to the Milo control
plane (customer session, staff session, or service-account token). No caller
needs — or can obtain — a credential capable of reading another customer's
usage.

### Notes / Constraints / Caveats

#### Cost Fields Are Reserved, Not Populated

The response schema includes `contractedCost` and `billingCurrency` fields
from day one, matching the FOCUS field names `MeterDefinition.spec.billing`
already borrows (`consumedUnit`, `pricingUnit`). They are `null` until the
pricing engine ships rates, so the schema does not need a breaking change once
cost data becomes available.

#### Still Bounded by Provider Latency

This proposal does not change how fast usage lands in Amberflo — see the
runbook's [provider lag section][runbook-lag]. A query against a period that
hasn't finished submitting yet returns partial data, same as querying Amberflo
directly today.

#### Cross-Repo Dependency

This document defines the interface `amberflo-provider` must implement, but
does not implement it. The public endpoint cannot return real data until that
companion change ships. See [Provider Query Interface](#provider-query-interface).

### Risks and Mitigations

- **Companion work spans four repos** (`amberflo-provider`, `cloud-portal`,
  `staff-portal`, `datum-cloud/datumctl`) that this document does not control.
  Mitigation: define the interface contract precisely enough that each repo's
  owners can implement their side independently and in parallel, rather than
  serializing on one team.
- **Auth model needs an answer this document doesn't fully have.** Whether
  Milo IAM already has a scoped read permission analogous to Cloudflare's
  "Billing Read," or one needs to be defined, is an open question — see
  [Future Work](#future-work). Mitigation: ship v0 accepting the same
  identity these callers already present for CRD reads, and layer a scoped
  permission on top once the pattern exists, rather than blocking on it.

## Design Details

### API Surface

| Endpoint | Method | Description |
|----------|--------|--------------|
| `/v1/billing-accounts/{billingAccountId}/usage` | `GET` | Usage rows for the billing account, optionally filtered by `from`, `to`, `project`, `meter` |

Query parameters:

| Parameter | Required | Description |
|---|---|---|
| `from` | No | Start of the time range (RFC 3339). Defaults to the start of the current billing cycle. |
| `to` | No | End of the time range (RFC 3339). Defaults to now. |
| `project` | No | Scope to a single project bound to this billing account. |
| `meter` | No | Scope to a single `MeterDefinition.spec.meterName`. Repeatable. |
| `groupBy` | No | A dimension key declared on the meter (e.g. `region`). When set, rows fan out one per distinct value, replacing today's separate `fetchMeterBreakdown` round-trip. |

Modeled on Cloudflare's `GET
/accounts/$ACCOUNT_ID/billable-usage?from=...&to=...` — a single resource
scoped by the paying entity, with optional date filtering.

### Response Schema

Field names borrow from FOCUS, matching the precedent already set by
`MeterDefinition.spec.billing` (`consumedUnit`, `pricingUnit`):

```json
{
  "result": [
    {
      "meterName": "compute.miloapis.com/instance/cpu-seconds",
      "displayName": "CPU Seconds",
      "description": "Total vCPU-seconds consumed by an instance.",
      "serviceId": "compute.miloapis.com",
      "serviceName": "Compute",
      "chargePeriodStart": "2026-08-01T00:00:00Z",
      "chargePeriodEnd": "2026-08-02T00:00:00Z",
      "consumedQuantity": 86400,
      "unit": "s",
      "aggregation": "Sum",
      "consumedUnit": "s",
      "pricingUnit": "h",
      "dimensions": { "region": "us-east-1" },
      "projectId": "p-abc",
      "contractedCost": null,
      "billingCurrency": null
    }
  ]
}
```

- `meterName` / `displayName` / `description` — from the `MeterDefinition`
  catalog, not Amberflo's `meterApiName` (`metadata.uid`). Callers never see
  the provider's internal ID.
- `serviceId` / `serviceName` — the meter name's reverse-DNS prefix and its
  humanized title (`compute.miloapis.com` → `Compute`), the same derivation
  `cloud-portal` does today (`serviceDomainFromMeterName` /
  `humanizeServiceGroup` in `usage.server.ts`), moved server-side so every
  caller gets the same value instead of each frontend re-deriving it.
- `chargePeriodStart` / `chargePeriodEnd` — one row per charge period
  (daily, matching Amberflo's current granularity and Cloudflare's stated v0
  granularity for most products). A caller reconstructs a time series for a
  meter by sorting its rows by `chargePeriodStart`.
- `unit` / `aggregation` — `MeterDefinition.spec.measurement.unit` (UCUM) and
  `.aggregation` (`Sum`, `Max`, …). This is the field the portals currently
  display as "unit" — it is catalog data, not an Amberflo response field, so
  it is already provider-agnostic today.
- `consumedUnit` / `pricingUnit` — `MeterDefinition.spec.billing`'s FOCUS
  fields, included for exports/alignment even where they match `unit` 1:1.
- `contractedCost` / `billingCurrency` — reserved, `null` until pricing ships.
- `dimensions` — the group's dimension key/value map for this row (Amberflo's
  `group.groupInfo`, normalized). Querying with a `groupBy` dimension fans a
  meter out into one row set per distinct value, mirroring today's
  `fetchMeterBreakdown` — a caller reconstructs a breakdown by grouping
  returned rows on `dimensions[<key>]`.
- `projectId` — present only when the query is scoped to a project or the
  underlying meter carries the platform's `project_name` system dimension
  (`MeterDefinition.status.systemDimensions`).

#### Field Inventory: What Every Current Portal Field Maps To

Audited against `cloud-portal`'s and `staff-portal`'s `MeterSeries` /
`UsageFetchResult` types (identical shape in both) to confirm nothing
currently shown to a user is lost in the migration:

| Portal field today | Sourced from | Normalized field |
|---|---|---|
| `meterApiName` | Amberflo (`metadata.uid`) | **Dropped by design** — provider-specific. Replaced by `meterName`, which is stable across providers. |
| `meterName` | `MeterDefinition.spec.meterName` | `meterName` |
| `label` | portal-computed | `displayName` |
| `values[].{timestamp,value}` | Amberflo `/usage` response | one row per `{chargePeriodStart, consumedQuantity}` |
| `description` | `MeterDefinition.spec.description` | `description` |
| `unit` | `MeterDefinition.spec.measurement.unit` | `unit` |
| `aggregation` | `MeterDefinition.spec.measurement.aggregation` | `aggregation` |
| `dimensions` (declared keys) | `MeterDefinition.spec.measurement.dimensions` | not repeated per row — already provider-agnostic catalog data, available from the existing `MeterDefinition` list |
| `groupId` / `groupTitle` | portal-computed from `meterName` | `serviceId` / `serviceName` |
| `limit` / `used` | `AllowanceBucket` (quota system) | **Out of scope**, unchanged — see [Non-Goals](#non-goals). Not usage data, and not provider-affected. |
| `breakdowns[].series[].{groupValue,values}` | Amberflo `/usage` `group.groupInfo` | rows with `dimensions[<key>] == groupValue`, grouped client-side |
| `UsageGroup{id,title,meterApiNames}` | portal-computed | unchanged — derivable from `serviceId`/`serviceName` across returned rows, or from the `MeterDefinition` list directly |
| `status` (`unconfigured`, `no-billing-account`, …) | portal-computed | **Out of scope** — an HTTP error/status code from this endpoint (e.g. `404` for no billing account) replaces the ad hoc enum; portals map it to their own UI state |
| `billingCycles` / `selectedBillingCycle` | `BillingAccount.spec.paymentTerms` | unchanged — already a provider-agnostic CRD read, not usage data |

The only fields genuinely sourced from Amberflo today are the raw
`{timestamp, value}` points and the dimension `groupInfo` map — everything
else already comes from Datum's own `MeterDefinition` catalog or portal-side
computation. Those two are exactly what `chargePeriodStart/End` +
`consumedQuantity` + `dimensions` normalize, which is the whole surface a
provider swap could otherwise break.

#### Provider Portability Guarantee

No field in this response may be a provider-specific identifier or shape.
Concretely: `meterApiName` and `customerId` (Amberflo's internal IDs) never
appear in the public response — only `meterName` (Datum's own catalog key)
and `billingAccountId` (already the request's path parameter) do. If
Amberflo is replaced, `amberflo-provider`'s successor implements the same
[Provider Query Interface](#provider-query-interface) and every field in this
schema keeps meaning exactly what it means today — a caller cannot detect the
swap from the response shape.

### Server Architecture

A new `billing usage-api` subcommand, added alongside `billing gateway` in
`cmd/billing/cmd/`, mirroring `internal/gateway/server.go`'s shape: a
controller-runtime manager used only for `/healthz` / `/readyz` / `/metrics`,
plus a plain `net/http.ServeMux` for the actual API surface (new package,
e.g. `internal/usageapi/`). Unlike the gateway, this server needs read access
to `BillingAccount`, `BillingAccountBinding`, and `MeterDefinition` — it reuses
the same informer-cache pattern `internal/controller/consumer` already
maintains for the same lookups, rather than hitting the API server per
request.

### Provider Query Interface

The contract `amberflo-provider` must implement, so its implementation isn't
guessing:

```
POST /internal/v1/usage:query

{
  "customerId": "<BillingAccount.metadata.uid>",
  "meterApiNames": ["<MeterDefinition.metadata.uid>", ...],
  "startTime": "2026-08-01T00:00:00Z",
  "endTime": "2026-08-02T00:00:00Z",
  "groupBy": ["region"]
}
```

returning Amberflo's native series shape (`clientMeters[].values[]`,
`group.groupInfo`) unchanged — `amberflo-provider` proxies and authenticates
the call to Amberflo; it does not need to know about FOCUS or the public
response schema. Normalization happens exclusively in `billing usage-api`.
This endpoint is internal-only, reachable from the billing service's network
identity, never exposed outside the cluster — the same trust boundary the
existing `submission-consumer` already operates inside.

### Authentication

The public endpoint accepts whatever bearer-token/IAM identity each caller
already presents to the Milo control plane today:

| Caller | Existing identity |
|---|---|
| `cloud-portal` | End-user session, forwarded as it already is for CRD reads |
| `staff-portal` | Staff session |
| `datumctl` | Datum service-account/API token |

No new credential type ships in v0. Whether a scoped `Billing Read`-style
permission (per Cloudflare's model) should gate this endpoint, versus the
caller's general read access to the billing account, is an open question for
Milo IAM owners — see [Future Work](#future-work).

### Migration Path

1. `billing usage-api` ships and is deployed alongside the gateway.
2. In the same wave, `datumctl` gains a `usage` subcommand calling the new
   endpoint. It is the first caller to depend on nothing but the public API —
   no session cookie, no portal BFF — and the cleanest way to validate the
   endpoint's auth and response shape are self-serve-ready before the portals
   migrate.
3. `cloud-portal`'s `usage.server.ts` / `org-usage.server.ts` are rewritten to
   call the new endpoint instead of Amberflo's `/usage`; `AMBERFLO_API_KEY`
   and `amberfloApiKey` are removed from its environment once verified.
4. `staff-portal`'s usage view follows the same migration.

Steps 2–4 are companion changes in their respective repos, not part of this
enhancement's implementation, but 2 ships alongside 1 rather than after it.

## Implementation History

- 2026-08-04: Initial enhancement proposal.

## Future Work

- **Self-serve customer API tokens.** A scoped permission (e.g. `Billing
  Read`, mirroring Cloudflare's model) that lets a customer mint a token for
  this endpoint directly, rather than only being reachable through
  `cloud-portal`'s session or `datumctl`'s service-account flow.
- **Platform-side aggregation.** Once the pipeline's own deferred aggregation
  layer ([usage-pipeline.md][usage-pipeline] Future Work) ships, this API's
  normalization can read from platform-owned storage instead of proxying
  every query to `amberflo-provider` live.
- **Quota join.** Surface `AllowanceBucket` limit/used alongside consumed
  quantity, matching what `cloud-portal` currently joins client-side.
- **UCUM-to-FOCUS unit mapping**, once FOCUS conformance matters for exports
  — same open item already flagged in the pipeline enhancement.

## Drawbacks

- **A new always-on read path to maintain.** Unlike CRD reads, which come free
  from the aggregated API server, this is hand-written HTTP surface with its
  own caching, latency, and failure modes to operate.
- **Cross-repo rollout.** The full benefit (both portals and the CLI off
  direct Amberflo access) requires four independent repos to ship their side;
  partial rollout leaves some callers still holding the Amberflo key until
  their migration lands.

## Alternatives

### Billing Service Holds the Amberflo Key Directly

Skip `amberflo-provider` and have `billing usage-api` call Amberflo itself.

**Rejected because:** it duplicates provider knowledge in two places (this
server and the existing Submission Controller) and reintroduces the exact
credential-isolation violation this proposal exists to fix, just moved one
hop over.

### Shared Client Package Instead of a Server API

Publish a shared TypeScript/Go client package that wraps Amberflo's API and
have each caller (`cloud-portal`, `staff-portal`, `datumctl`) import it
directly, instead of standing up a new server.

**Rejected because:** every caller still ends up holding an Amberflo
credential, and `datumctl` — a customer-run binary — cannot safely hold a
platform provider secret at all. A server-side API is the only option that
keeps the credential off every caller's machine.

## References

- [Cloudflare — Billable Usage API][cloudflare-post]
- [Usage Pipeline enhancement][usage-pipeline]
- [Payment Methods enhancement][payment-methods] — precedent for
  generic-resource + provider-specific-controller normalization
- [Usage Metering runbook][runbook]
- [Emitting Usage guide][emitting-usage]
- [FinOps FOCUS v1.3][focus]
- [`datumctl usage` companion issue][datumctl-usage-issue]

[cloudflare-post]: https://blog.cloudflare.com/billable-usage-api/
[usage-pipeline]: ./usage-pipeline.md
[payment-methods]: ./payment-methods.md
[runbook]: ../runbooks/usage-metering.md
[runbook-lag]: ../runbooks/usage-metering.md#4-provider-lag
[emitting-usage]: ../emitting-usage.md
[datumctl-usage-issue]: https://github.com/datum-cloud/datumctl/issues/266
[focus]: https://focus.finops.org
