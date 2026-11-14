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
  - [Customer and Staff Experience](#customer-and-staff-experience)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Current Billing Model](#current-billing-model)
  - [API](#api)
  - [Behavioral Rules](#behavioral-rules)
  - [Catalog Integration](#catalog-integration)
  - [Delivery Scope](#delivery-scope)
  - [Acceptance Criteria](#acceptance-criteria)
  - [Decisions to Confirm Before Implementation](#decisions-to-confirm-before-implementation)
  - [Technical Readiness](#technical-readiness)
- [Production Readiness Review Questionnaire](#production-readiness-review-questionnaire)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)

## Summary

BillableService represents a service we sell, whether or not it appears in the
public Service Catalog. It gives related setup, recurring, and usage charges a
shared billing name while allowing billing to describe a service differently
from the catalog.

Staff can sell private services through the existing billing workflow. Customers
see clear service names and charges, and their billing history stays unchanged
when catalog listings or service names change. Billing works independently of
Service Catalog.

## Motivation

Not everything we sell is ready for the public catalog. A dedicated-server
contract, support package, or early service may need billing before it has a
public listing. A catalog service may also need several billing names to match
how customers buy it.

Standalone fees are already possible, but they lack a shared service identity.
We need a consistent way to group and describe these charges without creating
catalog entries just to support billing.

### Goals

- Sell services before or without a public catalog listing.
- Group setup, recurring, and usage charges under one billing service.
- Use billing names that match the customer agreement.
- Preserve agreed prices, billing dates, and invoice history.
- Let private services enter the catalog without replacing customer agreements.
- Keep billing independent of catalog availability.

### Non-Goals

- A second public catalog, checkout, or service provisioning system.
- New charge types, tax rules, or revenue accounting.
- Changes to how usage is measured or permission to charge twice for the same usage.
- Customer-managed service catalogs; staff manage billing services initially.
- A new way to purchase or assign services outside the existing billing workflow.

## Proposal

Give staff a reusable billing service with a name and description. Staff attach
prices to it and sell it through the existing offer and account workflow.
One service can have setup, recurring, and usage charges, each with its own
invoice-line description.

The public catalog can use these billing services, but billing does not need a
catalog listing to operate. Publishing a private service later does not require
recreating its billing setup or customer agreements.

### User Stories

- As a billing operator, I can sell installation and monthly dedicated-server
  service before it appears in the public catalog.
- As a product operator, I can use different billing names for parts of one
  catalog service to match how customers buy them.
- As a customer, I can understand which service each charge belongs to, and
  later name changes do not rewrite my past invoices.
- As a platform operator, I can publish a private service without disrupting
  existing customer billing.

### Customer and Staff Experience

Staff select a billing service when setting up prices, then use the existing
offer workflow to agree charges with a customer. For example, “Dedicated Server”
can include “Server installation” and “Monthly dedicated server” charges.
Invoice previews show the agreed names, quantities, and amounts before billing.

Customers see only the service details relevant to their account. Creating a
billing service does not make it public, grant service access, provision it, or
charge anyone by itself.

Staff can retire a service to stop creating new prices and offers for it.
Existing agreements continue. Already published offers remain assignable;
a broader stop-sales policy is outside this proposal.

### Notes/Constraints/Caveats

This is a proposal, not a released feature. Existing customers are not moved
automatically. Adoption must preserve their agreed terms and must not repeat
setup charges or interrupt service.

### Risks and Mitigations

- **Unexpected charges:** check invoice previews and confirm that moving an
  account neither repeats fees nor changes billing dates.
- **Confusing names:** keep service names and individual charge descriptions
  distinct, and preserve the names recorded with existing agreements.
- **Private service disclosure:** limit management to staff and show customers
  only their own billing details.
- **Disrupted existing billing:** introduce the change gradually and stop new
  adoption if invoice amounts or billing behavior differ from expectations.

## Design Details

### Current Billing Model

ServicePricing defines prices, Offer groups prices and quantities, and
BillingEntitlement assigns an Offer to a BillingAccount. BillableService adds
identity without replacing those responsibilities.

Legacy serviceRef values identify catalog services. Usage pricing checks them
against billing meter labels; fixed fees currently query catalog Services when
a reference is supplied. Remove that catalog lookup during this change.

Prices and published Offer snapshots are immutable. Customer-specific basedOn
prices preserve the source identity. Account views carry serviceRef, while
usage views currently derive names from meter prefixes. These consumers need
to support the new identity without changing historical records.

The dependency stays one-way: Service Catalog uses the billing API. Billing
must not import catalog types, read or watch catalog resources, or require the
catalog API to be installed. Service Catalog owns all catalog-to-billing mappings.

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
“Monthly dedicated server”. Meter references are keyed by name. Catalog,
billing, and meter names do not need to match.

### Behavioral Rules

#### References and Metering

- New explicit references must resolve to an Active BillableService. Admission
  validates the actual referenced objects, not just a potentially stale Ready
  condition. Invalid billing references are also surfaced by reconciliation.
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
- Meter allowlists can change for future prices/publications.
  Revalidate them when an Offer is published, including for an older price.
  Such edits must not invalidate or rerate an already published Offer.

#### Snapshots and History

Extend each ServicePricingSnapshot with an optional resolved BillableService
snapshot containing its name, UID, displayName, and description.
Snapshot at Offer publication; continue snapshotting the pricing
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
cleanup. Catalog deletion or downtime must not affect billing publication,
validation, or ongoing charges. Billing does not check catalog resources or
use them as owners of billing resources.

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

Service Catalog owns any mapping between its Services and BillableServices.
It can map one Service to several billing identities or several Services to
one identity. Billing neither stores nor validates these mappings.
Staff-managed BillableServices can select the same billing meters without
being reconciled back to a catalog display name.
Define field ownership before enabling automation; do not let two controllers
overwrite the same metadata or meter list. No owner reference should cause
catalog deletion to garbage-collect billing identities.

If a private service later enters the catalog, reference its existing
BillableService from the catalog side. Do not create a replacement billing identity
or migrate contracts just to publish marketing/catalog information.

### Delivery Scope

#### Milestone 1: Independent Billing Identity

- Implement the resource, explicit pricing reference, meter allowlists,
  admission rules, lifecycle controller, and guarded deletion.
- Extend Offer snapshot publication and customer-price validation.
- Support staff permissions, activity history, and setup guidance.
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

- A staff user creates a BillableService, adds
  setup/monthly prices, publishes an Offer, and assigns it to an account.
  Invoice preview and invoice issuance preserve quantities and line labels.
- A metered standalone service works without a catalog ownership label;
  nonexistent, unassociated, or Retired meters are rejected on new publication.
- Two BillableServices mapped by one catalog Service retain independent
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
- With no catalog API installed or catalog permissions granted, billing can
  create and validate services and prices, publish Offers, and continue billing.
  Cover both the new reference path and legacy serviceRef prices. Billing must
  have no catalog API imports, lookups, watches, or reference validation.

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

### API Alternatives

Do not redefine serviceRef in place: that would make old and new identities
ambiguous in published snapshots. Use an explicit billableServiceRef and keep
legacy readers. Do not move prices or account assignments onto BillableService;
ServicePricing, Offer, and BillingEntitlement retain those responsibilities.

### Technical Readiness

These answers describe the proposed implementation. Tests, capacity measurements,
and operational thresholds remain prerequisites for implementation approval.

#### Feature Enablement and Rollback

##### How can this feature be enabled / disabled in a live cluster?

- [x] Other
  - Enable through additive CRD/controller deployment, dual-reader provider
    upgrades, and explicit opt-in authoring of billableServiceRef prices.
  - Disable new authoring and catalog generation while retaining the CRD,
    existing objects, controllers, and readers needed by active Offers.
  - No control-plane downtime or node reprovisioning is intended. Verify rolling
    deployment compatibility before production enablement.

##### Does enabling the feature change any default behavior?

No existing price or account is migrated automatically. Catalog-generated new
prices adopt the new reference only after the catalog writer is upgraded.

##### Can the feature be disabled once it has been enabled (i.e. can we roll back the enablement)?

New authoring can be stopped. Removing support or downgrading to binaries that
discard the new fields is unsafe once active Offers contain new snapshots.
Existing billing must continue using dual readers.

##### What happens if we reenable the feature if it was previously rolled back?

Resume authoring against retained identities and snapshots. Reconciliation must
not duplicate default BillableServices, provider subscriptions, or setup charges.

##### Are there any tests for feature enablement/disablement?

Not yet. Add end-to-end coverage for legacy-only operation, mixed references
across Offers, stopping new authoring, continued existing billing, and reenablement.

#### Rollout, Upgrade and Rollback Planning

1. **Support old and new Offers.** Add BillableService with staff permissions
   and activity history. Update providers and billing views to accept both
   formats before publishing Offers with the new billing identity.
2. **Support two explicit paths.** During migration accept either legacy
   serviceRef or billableServiceRef, never both. Usage requires one; existing
   unassociated fixed fees remain valid. Treat legacy serviceRef as a stored
   identifier, not a catalog API reference. Keep the existing meter-label check
   for legacy Usage prices using billing's MeterDefinition. Remove catalog
   existence checks for legacy fees; those strings no longer prove a catalog
   Service exists. Neither path may call the catalog API.
3. **Seed identities and update writers.** Inventory existing catalog identities,
   meters, prices, provider integrations, and downstream consumers. Create
   deterministic defaults, establish meter associations, then update catalog
   generation and staff tooling. Catalog-side tooling supplies mappings; billing
   does not discover them by querying catalog resources. All billing authoring
   and validation must work without the catalog API installed.
4. **Publish new prices and Offers.** Do not mutate immutable ServicePricing
   specs or published Offer snapshots. Create successors where migration is
   wanted and move accounts through the existing entitlement/contract amendment
   path, preserving its effective-date semantics. Migration alone must not
   re-trigger setup fees or create replacement provider subscriptions.
5. **Deprecate, then consider removal.** Stop new legacy authoring only after
   external writers are upgraded. Keep old snapshot readers for historical
   billing. Removing serviceRef or requiring BillableService for all fixed fees
   is a later, explicitly versioned compatibility decision.

##### How can a rollout or rollback fail? Can it impact already running workloads?

Old providers can ignore new identity fields; incompatible schemas can discard
them; naming collisions or invalid meter associations can block publication.
Incorrect provider identity handling can duplicate charges. Require provider
conformance before account migrations and deploy readers before writers.
Provisioned workloads should be unaffected because billing identity does not
control access, but billing failures can affect downstream account workflows.

##### What specific metrics should inform a rollback?

Track admission failures, reconciliation errors, Offer publication latency,
provider synchronization failures, and invoice-preview discrepancies against
the pre-rollout baseline. Duplicate charges or changed historical amounts are
stop-rollout conditions. Metric names and alert thresholds remain to be agreed
with the owning teams; none are claimed to exist specifically for this feature.

##### Were upgrade and rollback tested? Was the upgrade->downgrade->upgrade path tested?

No implementation exists yet. Test reader-first upgrades and writer-disable /
reenable cycles before rollout. Downgrade to a pre-support binary is not a
supported rollback once new-format Offers are active.

##### Is the rollout accompanied by any deprecations and/or removals of features, APIs, fields of API types, flags, etc.?

The API changes are additive. Remove the legacy fee check that queries catalog
Services, along with billing's catalog read permissions. Legacy serviceRef
authoring is a later deprecation candidate; historical readers remain.

#### Monitoring Requirements

##### How can an operator determine if the feature is in use by workloads?

Inspect BillableService resources, prices with billableServiceRef, and published
Offer snapshots referenced by BillingEntitlements. Resource existence alone
does not establish that an account is being charged.

##### How can someone using this feature know that it is working for their instance?

- [x] API .status
  - Proposed condition: `Ready` on BillableService, with observedGeneration
    matching metadata.generation and diagnostics for invalid references.
- [x] Other
  - Confirm the assigned Offer's snapshot and account-specific invoice preview
    carry the intended service identity, quantities, labels, and charges.

##### What are the reasonable SLOs (Service Level Objectives) for the enhancement?

Preserve existing publication and billing objectives without introducing an
extra runtime lookup for rating published Offers. Numeric availability and
latency targets must be established from current baselines before rollout.

##### What are the SLIs (Service Level Indicators) an operator can use to determine the health of the service?

Proposed indicators are reference-validation success rate, reconciliation error
rate, time to Ready, Offer publication latency, provider synchronization success,
and invoice-preview correctness. Billing controllers and providers should expose
aggregate measurements; concrete metric names remain an implementation decision.

##### Are there any missing metrics that would be useful to have to improve observability of this feature?

Inventory existing instrumentation first. Add missing counters for unresolved
references and publication failures, plus latency measurements for reconciliation
and publication. Avoid customer/account IDs as metric labels.

#### Dependencies

##### Does this feature depend on any specific services running in the cluster?

It depends on the billing API, admission webhooks, controllers, IAM enforcement,
and providers that consume Offer snapshots. Service Catalog is a billing API
client, not a billing dependency. Its absence must not block billing operations,
including legacy price validation. Unavailable billing dependencies should block new
validation/publication rather than silently accept unresolved identities;
existing published snapshots remain self-contained for service identity.

#### Scalability

##### Will enabling / using this feature result in any new API calls?

Yes: BillableService watches and reference lookups during price creation, Offer
publication, and deletion checks. Use cached indexes for reverse references.
There are no catalog API calls on any billing path.

##### Will enabling / using this feature result in introducing new API types?

Yes: cluster-scoped BillableService and its list type, plus reference and snapshot
fields on existing billing APIs.

##### Will enabling / using this feature result in any new calls to the cloud provider?

No infrastructure cloud-provider calls are proposed. Billing-provider adapters
may need product metadata synchronization; verify this in their owning repos.

##### Will enabling / using this feature result in increasing size or count of the existing API objects?

Yes: Offer snapshots and account projections gain billing metadata, and migration
creates successor prices and Offers. Establish field/list bounds and measure
large Offers against API object-size limits before implementation approval.

##### Will enabling / using this feature result in increasing time taken by any operations covered by existing SLIs/SLOs?

Reference validation and publication add lookup work. Rating published Offers
must not add a live BillableService lookup. Benchmark admission and publication.

##### Will enabling / using this feature result in non-negligible increase of resource usage in any components?

Controllers gain caches/indexes and providers process larger snapshots. The
impact is not measured yet; load-test representative service, meter, and Offer
counts before setting capacity expectations.

##### Can enabling / using this feature result in resource exhaustion of some node resources (PIDs, sockets, inodes, etc.)?

No per-service processes or persistent sockets are proposed. Unbounded lists,
snapshots, or reconciliation retries could still exhaust memory or API capacity;
bound inputs, use indexes, and retain rate-limited reconciliation.

#### Troubleshooting

##### How does this feature react if the API server is unavailable?

New validation, authoring, and publication cannot complete. Controllers retry
when connectivity returns. Do not claim uninterrupted billing: existing provider
behavior governs outages, although service identity in published snapshots
requires no new live lookup.

##### What are other known failure modes?

Anticipated failures include missing/retired references, stale readiness,
catalog-controller naming collisions, permission errors, old provider readers,
and incorrect account-specific meter mappings. Surface reference failures in
admission responses and status; preserve prior published snapshots.

##### What steps should be taken if SLOs are not being met to determine the problem?

Check resource conditions and observedGeneration, referenced billing meters,
controller ownership and permissions, the published snapshot, and
provider versions/logs. Compare invoice previews before further assignments.
Stop new authoring/migrations if correctness is in doubt; do not rewrite
historical snapshots as an operational repair.

## Production Readiness Review Questionnaire

Not yet tested. The full questionnaire and technical rollout plan are under
[Technical Readiness](#technical-readiness). Product release checks are:

- **Enablement and rollback:** staff opt in; stopping adoption must not stop
  billing for existing customers.
- **Rollout:** preserve prices, billing dates, and history, with no duplicate fees.
- **Monitoring:** confirm correct invoice previews and timely billing; pause
  adoption if charges differ from the agreement.
- **Dependencies:** selling and billing must work without Service Catalog.
- **Capacity:** support expected service and customer volumes without slowing
  down billing. Capacity testing is still required.
- **Support:** staff need clear failure messages and a way to check an affected
  customer's agreement and invoice before making changes.

## Implementation History

- 2026-11-13: Initial proposal using the Datum enhancement template. The v0
  milestone is a proposal, not a shipped release. No implementation is complete.

## Drawbacks

- Staff have another concept to manage alongside catalog services and prices.
- Different catalog and billing names may confuse staff without clear guidance.
- Adoption requires coordination across catalog, billing, and invoicing teams.
- Supporting existing agreements alongside the new approach adds ongoing work.

## Alternatives

**Keep using standalone fees:** enough for one-off charges, but does not provide
consistent service names or grouping across setup, recurring, and usage charges.

**Create hidden catalog services:** avoids public listings but still makes
billing depend on catalog setup and structure.

**Use catalog names for billing:** simpler, but does not support services sold
before listing or agreements that describe services differently.

**Create a separate sales workflow:** adds complexity when existing pricing,
offers, and account assignment already cover the purchase process.

## Infrastructure Needed

No new external service is proposed. Existing billing and invoicing systems
need to support the change and be tested before customer adoption.
