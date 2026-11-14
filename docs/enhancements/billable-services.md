---
status: provisional
stage: alpha
latest-milestone: "v0"
---

# Billable Services

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [API](#api)
  - [Behavioral Rules](#behavioral-rules)
  - [Catalog Integration](#catalog-integration)
  - [Delivery Scope](#delivery-scope)
  - [Acceptance Criteria](#acceptance-criteria)
  - [Decisions to Confirm Before Implementation](#decisions-to-confirm-before-implementation)
- [Production Readiness Review Questionnaire](#production-readiness-review-questionnaire)
  - [Feature Enablement and Rollback](#feature-enablement-and-rollback)
  - [Rollout, Upgrade and Rollback Planning](#rollout-upgrade-and-rollback-planning)
  - [Monitoring Requirements](#monitoring-requirements)
  - [Dependencies](#dependencies)
  - [Scalability](#scalability)
  - [Troubleshooting](#troubleshooting)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)

## Summary

Introduce `billing.miloapis.com/v1alpha1.BillableService`: a stable identity for
**what a provider sells**, independent of how it is advertised, provisioned,
or priced. A BillableService can exist without a public Service Catalog entry,
and several BillableServices can represent different commercial aspects of
one catalog Service.

Keep the existing commercial flow:

```text
Catalog Service (optional association)
  └── BillableService ← ServicePricing ← Offer ← BillingEntitlement
                           ↑                         └── BillingAccount
                    MeterDefinition
                    (usage pricing only)
```

BillableService supplies billing identity and descriptive metadata.
ServicePricing continues to define a price, Offer selects prices and quantities,
and BillingEntitlement assigns an Offer to an account. Creating a BillableService
does not publish a catalog entry, grant access, provision resources, or charge
a customer.

This is a scope proposal, not an implemented API. External provider and catalog
changes below are integration requirements; their implementations were not
inspected as part of this repository review.

## Motivation

- `ServicePricing.spec.serviceRef` is a catalog `Service.spec.serviceName`, not
  a Kubernetes object reference. Usage pricing requires it and validates it
  against the meter's `services.miloapis.com/service` label.
- OneTime and Recurring prices already allow no service reference. When one is
  supplied, validation lists catalog Services to find a matching serviceName.
  Consequently, a standalone fee is possible today, but a reusable independent
  billing-service identity is not.
- Prices are immutable. Published Offers snapshot pricing specs, and an Offer
  can price a given usage meter only once. Customer-specific `basedOn` prices
  preserve the source price's identity and shape while changing amounts.
- BillingAccount contract and discount projections carry the existing
  `serviceRef`. The provider UI's usage rollup derives service labels from
  meter-name prefixes rather than an explicit billing identity.

Relevant implementation points:
`api/v1alpha1/servicepricing_types.go`, `api/v1alpha1/pricing_types.go`,
`api/v1alpha1/meterdefinition_types.go`, `internal/validation/servicepricing.go`,
`internal/validation/offer.go`, `internal/controller/offer_controller.go`,
`internal/controller/billingaccount_controller.go`, and
`ui/provider/src/lib/usage.ts`.

### Goals

- Sell private or pre-catalog services through the existing billing workflow.
- Give related setup, recurring, and usage prices a shared service identity.
- Use billing names and groupings that differ from public catalog names.
- Support one catalog Service mapping to several BillableServices, and a
  BillableService associated with several catalog Services for reporting.
- Preserve historical commercial terms and existing usage-meter identities.
- Keep catalog publication and service access separate from billing setup.

### Non-Goals

- A second public catalog, shopping cart, order system, or provisioning API.
- A new account-level purchase/subscription resource or multiple simultaneous
  Offers per account; continue composing purchases into the account's Offer.
- New charge types, tax calculation, revenue recognition, or provider SKU IDs
  in the core API.
- Splitting one meter's usage into several charges within an Offer, changing
  meter aggregation, or rewriting usage events.
- Tenant-authored private product catalogs. Initial authoring is platform staff
  or trusted controller only; consumer visibility is a separate permission.

## Proposal

Introduce a platform-managed BillableService resource and an explicit reference
from ServicePricing. Catalog associations are optional. Snapshot the billing
identity when an Offer is published so later metadata changes do not change
what an account purchased. Adopt the new path incrementally alongside legacy
catalog references.

### User Stories

- As a billing operator, I can sell setup and monthly dedicated-server services
  before they have a public catalog entry.
- As a product operator, I can represent one catalog service as several billing
  services with distinct names and meter selections.
- As a customer, I see consistent service identities and invoice-line labels
  without later catalog edits rewriting my billing history.
- As a platform operator, I can bring a private service into the catalog without
  replacing its billing identity or recreating customer contracts.

### Notes/Constraints/Caveats

This proposal retains the existing one-Offer-per-account model, immutable prices,
and immutable published Offer snapshots. BillableService does not grant service
access. Provider and catalog integration work must be coordinated across their
owning repositories before production adoption.

### Risks and Mitigations

- **Changed historical charges or labels:** snapshot billing identity and retain
  dual readers; never reinterpret the existing serviceRef field.
- **Duplicate metered charges:** retain one-price-per-meter-per-Offer validation
  even when several BillableServices allow the same meter.
- **Private offering disclosure:** restrict authoring and discovery to staff;
  expose only authorized account-specific billing views to consumers.
- **Controller ownership conflicts:** establish deterministic naming and field
  ownership, reject collisions, and avoid catalog-driven garbage collection.
- **Unsafe provider migration:** require conformance tests for stable provider
  identifiers, effective dates, and one-time charge idempotency before rollout.

## Design Details

### API

Proposed resource:

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: BillableService
# Cluster-scoped; prices stay in namespaced ServicePricing resources.
metadata:
  # Stable billing ID, separate from the catalog's serviceName.
  name: dedicated-server
spec:
  # Required billing name. Edits affect only newly published Offers.
  displayName: Dedicated Server
  # Optional customer-facing description. No internal notes.
  description: Dedicated hardware with installation and monthly operation.
  # Defaults to Active. Can move to Retired, but not back.
  phase: Active
  # Optional Service object references: [{name: dedicated-compute}].
  # Names must be unique. Linking does not publish or grant access.
  catalogServiceRefs: []
  # Allowed MeterDefinition references: [{name: server-hours}].
  # Names must be unique. Leave empty for fee-only services.
  meterRefs: []
# Written by the controller, not the author.
status:
  # Reports readiness and broken references.
  conditions: []
  # Last metadata.generation checked by the controller.
  observedGeneration: 1
```

Prices use `ServicePricing.spec.billableServiceRef: {name: dedicated-server}`.
Each price keeps its own invoice-line label, such as “Server installation” or
“Monthly dedicated server”. Both reference lists are keyed by name. Catalog,
billing, and meter names do not need to match.

### Behavioral Rules

#### References and Metering

- New explicit references must resolve to an Active BillableService. Admission
  validates the actual referenced objects, not just a potentially stale Ready
  condition. Invalid associations are also surfaced by reconciliation.
- A Usage pricing's metric must resolve to an existing, non-Retired meter, as
  today. On the new reference path, that meter must appear in the BillableService's
  explicit meterRefs; do not require catalog-label equality.
- Preserve existing dimension, rate-shape, and unit-conversion checks. Meter
  labels remain provenance, and event routing and meterName remain unchanged.
- Allow a meter to appear in several BillableServices for alternate commercial
  packaging. Preserve the existing one-price-per-meter-per-Offer constraint
  across all BillableServices so an account is not charged twice for that meter.
- Fee prices require no meter. A BillableService can have any combination of
  Usage, OneTime, and Recurring prices.
- A `basedOn` pricing must preserve its source's billableServiceRef. A negotiated
  rate is another price for the same service, not a new billing identity.
- Associations and meter allowlists can change for future prices/publications.
  Revalidate them when an Offer is published, including for an older price.
  Such edits must not invalidate or rerate an already published Offer.

#### Snapshots and History

Extend each ServicePricingSnapshot with an optional resolved BillableService
snapshot containing its name, UID, displayName, description, and catalog
associations. Snapshot at Offer publication; continue snapshotting the pricing
spec, including the new reference, as today.

Providers and billing read models must use the published snapshot, not a live
BillableService lookup, for purchased service identity and presentation. A
rename affects newly published Offers only. Existing invoice lines, published
Offers, rates, quantities, and customer contract terms must not change.

Project the new identity separately into account contract/discount views and
appropriate usage/invoice read models. Do not silently reinterpret the old
serviceRef field. Service spend grouping must use the applicable account's
published pricing mapping, not a global meter-to-service map: the same meter
can have different commercial identities in different Offers. Historical
queries must use the applicable historical pricing, with legacy fallback where
no BillableService snapshot exists. Unpriced usage retains meter-based grouping.

#### Lifecycle and Access

Retirement blocks new prices and new Offer publications using the service; it
does not stop existing charges. In v1 it also does not prevent assigning an
already published Offer. A universal sales cutoff is deliberately not implied
by this lifecycle and would require a separate Offer/assignment policy.

Reject deletion while any ServicePricing or published Offer references the
BillableService. Prefer retirement and retained identities over destructive
cleanup. Do not cascade deletion from catalog Services: deleting or removing a
catalog association must not delete billing products or terminate billing.
Missing catalog associations block new publication until repaired, not ongoing
rating of immutable snapshots.

Staff can manage BillableServices; catalog automation receives narrowly scoped
permissions for resources it manages. Consumers receive only the billing
details associated with their own account through authorized read models, not
cluster-wide discovery of private offerings. Customer-safe metadata still
requires authorization; “not in the public catalog” is not an access control.

### Catalog Integration

The catalog controller should ensure a default BillableService for each
catalog Service that generates billing prices, then generate prices referencing
it. Use a deterministic naming policy and explicit managed-by metadata.
Detect collisions and fail visibly rather than adopting a staff-managed object.

The default mapping is a convenience, not a one-to-one domain constraint.
Staff-managed BillableServices can associate with the same catalog Service and
select its meters without being reconciled back to the catalog's display name.
Define field ownership before enabling automation; do not let two controllers
overwrite the same metadata or meter list. No owner reference should cause
catalog deletion to garbage-collect billing identities.

If a private service later enters the catalog, attach the catalog association
to its existing BillableService. Do not create a replacement billing identity
or migrate contracts just to publish marketing/catalog information.

### Delivery Scope

#### Milestone 1: Independent Billing Identity

- Implement the resource, explicit pricing reference, meter allowlists,
  admission rules, lifecycle controller, and guarded deletion.
- Extend Offer snapshot publication and customer-price validation.
- Add generated artifacts, sample manifests, operator permissions, protected
  resource registration, activity policy, and staff authoring documentation.
- Update account projections and provider contracts for dual-format consumption.
- Prove a standalone setup fee plus monthly fee can be assigned and invoiced
  without creating a catalog Service. Prove standalone metered pricing as well.

#### Milestone 2: Catalog Adoption and Customer Presentation

- Update service-catalog generation in its owning repository and migrate
  representative catalog-backed offerings without changing meter identities.
- Update invoicing/metering providers in their owning repositories and verify
  invoice previews, live charges, history, and setup-fee idempotency.
- Add a staff service selector/filter using billing identity; keep existing
  pricing and Offer composition workflows rather than adding a second purchase UI.
- Replace meter-prefix service grouping where commercial pricing is available;
  retain legacy and unpriced-usage fallback behavior.

Both milestones are needed for full adoption; backend resource support alone
does not deliver consistent billing presentation. No public catalog UI changes
are required to begin selling standalone services.

### Acceptance Criteria

- A staff user creates a BillableService with no catalog association, adds
  setup/monthly prices, publishes an Offer, and assigns it to an account.
  Invoice preview and invoice issuance preserve quantities and line labels.
- A metered standalone service works without a catalog ownership label;
  nonexistent, unassociated, or Retired meters are rejected on new publication.
- Two BillableServices associated with one catalog Service retain independent
  billing labels. The same meter cannot be priced twice in one Offer.
- Creating a service or assigning billing terms does not create catalog access
  entitlements or make an unpublished service publicly discoverable.
- Missing/retired references, mixed legacy/new references, and `basedOn`
  identity changes fail validation. Reference edits are checked on publication.
- Metadata edits and retirement leave published Offers and historical invoices
  intact. In-use deletion is blocked; catalog deletion does not cascade.
- Existing usage prices and unassociated fixed fees continue to function.
  Existing Offers need no rewrite, and migrating an account does not duplicate
  charges, change meter keys, or unexpectedly alter billing dates.
- Authorization tests prevent consumers from enumerating staff/private services;
  provider conformance tests cover both snapshot versions and historical reads.

### Decisions to Confirm Before Implementation

- **Scope:** cluster-wide platform-managed services are proposed. If providers
  need isolated authoring namespaces, revisit scope and reference shape now.
- **Catalog generation:** agree on deterministic names, collision handling,
  field ownership, and explicit mappings with the service-catalog owners.
- **Retirement:** confirm that preventing new publications, while honoring
  existing published Offers, matches the intended operational workflow.
- **Metadata:** confirm whether service-level grouping is sufficient for the
  first release; accounting categories and tax identifiers are not included.
- **Provider readiness:** inventory every consumer of serviceRef and agree on
  dual-read rollout and provider identity preservation before switching writers.

## Production Readiness Review Questionnaire

These answers describe the proposed implementation. Tests, capacity measurements,
and operational thresholds remain prerequisites for implementation approval.

### Feature Enablement and Rollback

#### How can this feature be enabled / disabled in a live cluster?

- [x] Other
  - Enable through additive CRD/controller deployment, dual-reader provider
    upgrades, and explicit opt-in authoring of billableServiceRef prices.
  - Disable new authoring and catalog generation while retaining the CRD,
    existing objects, controllers, and readers needed by active Offers.
  - No control-plane downtime or node reprovisioning is intended. Verify rolling
    deployment compatibility before production enablement.

#### Does enabling the feature change any default behavior?

No existing price or account is migrated automatically. Catalog-generated new
prices adopt the new reference only after the catalog writer is upgraded.

#### Can the feature be disabled once it has been enabled (i.e. can we roll back the enablement)?

New authoring can be stopped. Removing support or downgrading to binaries that
discard the new fields is unsafe once active Offers contain new snapshots.
Existing billing must continue using dual readers.

#### What happens if we reenable the feature if it was previously rolled back?

Resume authoring against retained identities and snapshots. Reconciliation must
not duplicate default BillableServices, provider subscriptions, or setup charges.

#### Are there any tests for feature enablement/disablement?

Not yet. Add end-to-end coverage for legacy-only operation, mixed references
across Offers, stopping new authoring, continued existing billing, and reenablement.

### Rollout, Upgrade and Rollback Planning

1. **Add the resource and tolerant readers.** Ship CRD, client/applyconfiguration
   generation, validation, controller, IAM/RBAC, discovery, activity policies,
   and optional reference/snapshot fields. Upgrade providers and read models
   to accept both legacy and new Offers before producing new snapshots.
2. **Support two explicit paths.** During migration accept either legacy
   serviceRef or billableServiceRef, never both. Usage requires one; existing
   unassociated fixed fees remain valid. Preserve existing catalog validation
   on the legacy path and apply the new rules only on the new path.
3. **Seed identities and update writers.** Inventory existing catalog identities,
   meters, prices, provider integrations, and downstream consumers. Create
   deterministic defaults, establish meter associations, then update catalog
   generation and staff tooling. Standalone authoring must work with no catalog
   Service and must not require listing catalog Services when no association
   is provided.
4. **Publish new prices and Offers.** Do not mutate immutable ServicePricing
   specs or published Offer snapshots. Create successors where migration is
   wanted and move accounts through the existing entitlement/contract amendment
   path, preserving its effective-date semantics. Migration alone must not
   re-trigger setup fees or create replacement provider subscriptions.
5. **Deprecate, then consider removal.** Stop new legacy authoring only after
   external writers are upgraded. Keep old snapshot readers for historical
   billing. Removing serviceRef or requiring BillableService for all fixed fees
   is a later, explicitly versioned compatibility decision.

#### How can a rollout or rollback fail? Can it impact already running workloads?

Old providers can ignore new identity fields; incompatible schemas can discard
them; naming collisions or invalid meter associations can block publication.
Incorrect provider identity handling can duplicate charges. Require provider
conformance before account migrations and deploy readers before writers.
Provisioned workloads should be unaffected because billing identity does not
control access, but billing failures can affect downstream account workflows.

#### What specific metrics should inform a rollback?

Track admission failures, reconciliation errors, Offer publication latency,
provider synchronization failures, and invoice-preview discrepancies against
the pre-rollout baseline. Duplicate charges or changed historical amounts are
stop-rollout conditions. Metric names and alert thresholds remain to be agreed
with the owning teams; none are claimed to exist specifically for this feature.

#### Were upgrade and rollback tested? Was the upgrade->downgrade->upgrade path tested?

No implementation exists yet. Test reader-first upgrades and writer-disable /
reenable cycles before rollout. Downgrade to a pre-support binary is not a
supported rollback once new-format Offers are active.

#### Is the rollout accompanied by any deprecations and/or removals of features, APIs, fields of API types, flags, etc.?

The initial release is additive. Legacy serviceRef authoring is a later
deprecation candidate, not an initial removal; historical readers remain.

### Monitoring Requirements

#### How can an operator determine if the feature is in use by workloads?

Inspect BillableService resources, prices with billableServiceRef, and published
Offer snapshots referenced by BillingEntitlements. Resource existence alone
does not establish that an account is being charged.

#### How can someone using this feature know that it is working for their instance?

- [x] API .status
  - Proposed condition: `Ready` on BillableService, with observedGeneration
    matching metadata.generation and diagnostics for invalid references.
- [x] Other
  - Confirm the assigned Offer's snapshot and account-specific invoice preview
    carry the intended service identity, quantities, labels, and charges.

#### What are the reasonable SLOs (Service Level Objectives) for the enhancement?

Preserve existing publication and billing objectives without introducing an
extra runtime lookup for rating published Offers. Numeric availability and
latency targets must be established from current baselines before rollout.

#### What are the SLIs (Service Level Indicators) an operator can use to determine the health of the service?

Proposed indicators are reference-validation success rate, reconciliation error
rate, time to Ready, Offer publication latency, provider synchronization success,
and invoice-preview correctness. Billing controllers and providers should expose
aggregate measurements; concrete metric names remain an implementation decision.

#### Are there any missing metrics that would be useful to have to improve observability of this feature?

Inventory existing instrumentation first. Add missing counters for unresolved
references and publication failures, plus latency measurements for reconciliation
and publication. Avoid customer/account IDs as metric labels.

### Dependencies

#### Does this feature depend on any specific services running in the cluster?

It depends on the billing API, admission webhooks, controllers, IAM enforcement,
and providers that consume Offer snapshots. Catalog integration additionally
requires service-catalog, but standalone services without catalog associations
must not require catalog reads. Unavailable dependencies should block new
validation/publication rather than silently accept unresolved identities;
existing published snapshots remain self-contained for service identity.

### Scalability

#### Will enabling / using this feature result in any new API calls?

Yes: BillableService watches and reference lookups during price creation, Offer
publication, and deletion checks. Use cached indexes for reverse references and
avoid full catalog scans on the standalone path.

#### Will enabling / using this feature result in introducing new API types?

Yes: cluster-scoped BillableService and its list type, plus reference and snapshot
fields on existing billing APIs.

#### Will enabling / using this feature result in any new calls to the cloud provider?

No infrastructure cloud-provider calls are proposed. Billing-provider adapters
may need product metadata synchronization; verify this in their owning repos.

#### Will enabling / using this feature result in increasing size or count of the existing API objects?

Yes: Offer snapshots and account projections gain billing metadata, and migration
creates successor prices and Offers. Establish field/list bounds and measure
large Offers against API object-size limits before implementation approval.

#### Will enabling / using this feature result in increasing time taken by any operations covered by existing SLIs/SLOs?

Reference validation and publication add lookup work. Rating published Offers
must not add a live BillableService lookup. Benchmark admission and publication.

#### Will enabling / using this feature result in non-negligible increase of resource usage in any components?

Controllers gain caches/indexes and providers process larger snapshots. The
impact is not measured yet; load-test representative service, meter, and Offer
counts before setting capacity expectations.

#### Can enabling / using this feature result in resource exhaustion of some node resources (PIDs, sockets, inodes, etc.)?

No per-service processes or persistent sockets are proposed. Unbounded lists,
snapshots, or reconciliation retries could still exhaust memory or API capacity;
bound inputs, use indexes, and retain rate-limited reconciliation.

### Troubleshooting

#### How does this feature react if the API server is unavailable?

New validation, authoring, and publication cannot complete. Controllers retry
when connectivity returns. Do not claim uninterrupted billing: existing provider
behavior governs outages, although service identity in published snapshots
requires no new live lookup.

#### What are other known failure modes?

Anticipated failures include missing/retired references, stale readiness,
catalog-controller naming collisions, permission errors, old provider readers,
and incorrect account-specific meter mappings. Surface reference failures in
admission responses and status; preserve prior published snapshots.

#### What steps should be taken if SLOs are not being met to determine the problem?

Check resource conditions and observedGeneration, referenced meters and catalog
objects, controller ownership and permissions, the published snapshot, and
provider versions/logs. Compare invoice previews before further assignments.
Stop new authoring/migrations if correctness is in doubt; do not rewrite
historical snapshots as an operational repair.

## Implementation History

- 2026-11-13: Initial scope proposal and alignment with the Datum enhancement
  template. `v0` identifies this initial proposal milestone, not a shipped release.
  No runtime implementation or production-readiness testing has been completed.

## Drawbacks

- Adds another resource for operators to understand and manage.
- Requires coordinated changes across billing, catalog, providers, and read models.
- Dual reference paths and immutable historical snapshots extend compatibility
  obligations beyond the initial migration.
- Explicit meter associations and deletion protection add reconciliation and
  lifecycle complexity that unassociated fixed fees do not require.

## Alternatives

**Continue unassociated fixed fees:** already solves ad hoc charging, but not
stable service identity, service grouping, or catalog-independent usage pricing.

**Create hidden catalog Services:** retains billing's dependence on catalog
structure and still conflates commercial identity with operational identity.

**Redefine serviceRef in place:** appears smaller, but makes old and new
identifiers ambiguous and risks changing the meaning of immutable snapshots.
An explicit new reference makes compatibility and migration auditable.

**Put prices or account assignments on BillableService:** duplicates
ServicePricing, Offer, and BillingEntitlement responsibilities without solving
an additional requirement. Keep this resource narrowly focused on identity.

## Infrastructure Needed

No new external infrastructure is proposed. Use the existing Kubernetes API,
billing controller/webhook deployment, IAM, and provider integration environment.
Integration testing needs catalog-backed and standalone fixtures plus provider
sandbox coverage for invoice previews and one-time-charge idempotency.
