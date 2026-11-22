---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Charges Staff Add

- [Summary](#summary)
- [What We Have Today](#what-we-have-today)
- [Goals and Non-Goals](#goals-and-non-goals)
- [The Charge Resource](#the-charge-resource)
  - [Catalog and custom charges](#catalog-and-custom-charges)
  - [Service period](#service-period)
  - [Amounts and tax](#amounts-and-tax)
  - [When it's invoiced](#when-its-invoiced)
  - [Validation](#validation)
  - [Status and lifecycle](#status-and-lifecycle)
  - [Removing a charge](#removing-a-charge)
  - [RBAC](#rbac)
  - [Activity](#activity)
- [openmeter-provider: a Pending Line on the Gathering Invoice](#openmeter-provider-a-pending-line-on-the-gathering-invoice)
  - [The pending line](#the-pending-line)
  - [Invoice now](#invoice-now)
  - [Status back to the charge](#status-back-to-the-charge)
  - [Previews](#previews)
  - [What to confirm on staging first](#what-to-confirm-on-staging-first)
- [Reaching Stripe with Its Period](#reaching-stripe-with-its-period)
  - [This depends on the invoice writer](#this-depends-on-the-invoice-writer)
  - [From charge to Stripe line](#from-charge-to-stripe-line)
  - [The period at each hop](#the-period-at-each-hop)
  - [The custom charge product](#the-custom-charge-product)
- [Staff Portal](#staff-portal)
  - [Charges card](#charges-card)
  - [Add charge](#add-charge)
  - [Preview](#preview)
- [Service Periods on One-Time Contract Fees](#service-periods-on-one-time-contract-fees)
- [Rollout](#rollout)
- [Testing](#testing)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)
- [References](#references)

## Summary

Staff add one-off charges to a billing account in the staff portal: a
cross-connect install, a month of remote hands, a correction. Today they do
it by hand in the Stripe dashboard. Those charges have no service period,
no product and no audit trail in the platform, and they don't show in the
account's invoice preview.

This enhancement adds a `Charge` resource to billing. Staff create one on a
billing account, either from the catalog (a `ServicePricing` gives the
price, unit, SKU and tax code) or as a custom charge (description, amount
and tax code). Each has a quantity, a **service period**, an optional note
for staff, and a choice to put it on the next invoice or invoice it now.

openmeter-provider puts the charge on OpenMeter's gathering invoice as a
pending line that keeps its period, so it lands on the same invoice as the
period's usage and fees. When the invoice is written to Stripe, the
charge's line carries its period as Stripe's `period.start` and
`period.end`, which is what Stripe prints as the line's dates and what
Revenue Recognition spreads it over.

It's the "Charges Staff Add" part of
[Writing Our Own Stripe Invoices][enhancement], whose invoicing
architecture is in stripe-provider's [`docs/architecture/invoicing.md`][arch].
That enhancement says the charge resource's design belongs in billing; this
is it.

## What We Have Today

Checked on 2026-11-22, against `main` in each repo.

| Piece | State |
|---|---|
| A charge resource in billing | None. The nearest thing is `CreditGrant`, a staff-created resource on an account. |
| Service periods in billing's API | `InvoicePeriod` on `Invoice` and `InvoiceRequest` lines, read-only, written by the provider. |
| Pending lines in openmeter-provider | It never creates them. It only invoices them early (`InvoicePendingLinesAction`, `internal/openmeter/invoice.go`). OpenMeter beta.236's client has `CreatePendingInvoiceLine`, with a `period` per line. |
| The phase 4 invoice writer | Not started. stripe-provider has `StripeInvoice` for `Preview` and `PreviewDraft` only; `purpose: Invoice` and the invoice controller don't exist. openmeter-provider has no Custom Invoicing. |
| Periods on Stripe lines | `StripeInvoiceLine.period` is required, with CEL `end >= start`. Previews already send `period` to Stripe. |
| Staff portal | The billing account page has a "Preview next invoice" dialog on the contract card, from an `InvoiceRequest` Preview. Line periods show only in contract previews. Nothing adds charges. |
| One-time contract fees | No service period of their own. OpenMeter gives each line a period, and that's what Stripe gets. |

## Goals and Non-Goals

Goals:

- Staff add a charge to any billing account, contract or not, from the
  catalog or custom, with a quantity, a service period, a staff note and
  tax settings.
- The charge's service period reaches the Stripe line as `period.start` and
  `period.end`, unchanged.
- The charge is on the account's next invoice with everything else, or on
  an invoice of its own when staff choose "invoice now".
- It shows in the account's invoice preview before it's billed, and staff
  can remove it until the invoice is issued.
- It's a platform resource, with RBAC and an activity trail.

Non-goals:

- **Credits and refunds.** Negative charges aren't allowed: Stripe Tax won't
  calculate an invoice with negative lines. Credits are `CreditGrant`s, and
  refunds or corrections to an issued invoice are Stripe credit notes.
- **Recurring charges.** A charge is billed once. Something billed every
  month is a contract fee.
- **Editing a charge.** A charge is immutable. To change one, remove it and
  add another.
- **Showing charges to customers in the portal.** Customers see the charge
  on their invoice, as today. The resource itself is staff-only, because it
  holds the staff note.

## The Charge Resource

`Charge` is a namespaced resource in `billing.miloapis.com/v1alpha1`, in
the billing account's namespace, next to its `CreditGrant`s.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: Charge
metadata:
  name: cross-connect-install-7k2p9
  namespace: organization-acme
spec:
  billingAccountRef:
    name: acme
  kind: Catalog
  # Price, unit, SKU, tax and description come from the pricing.
  servicePricingRef:
    name: cross-connect-install-v1
  quantity: "2"
  servicePeriod:
    start: "2026-12-15"     # dates, inclusive, in UTC
    end: "2026-12-15"       # a single day is allowed
  note: Installed for the new cage; ticket OPS-1234
  invoice: NextInvoice      # NextInvoice | Now
  # Filled in by the defaulting webhook for a catalog charge:
  description: Cross-connect install
  unitAmount: "300.00"
  currency: USD
  tax:
    code: txcd_20030000
status:
  phase: Scheduled          # Pending | Scheduled | Invoiced | Failed
  amount: "600.00"
  invoiceAt: "2027-01-01T00:00:00Z"
  invoiceRef:
    name: ""                # the Invoice once it's issued
  conditions:
    - type: Ready
      status: "True"
      reason: Scheduled
  observedGeneration: 1
```

A custom charge has no `servicePricingRef`, or one used only for its
product (see below), and states its own description, amount and tax:

```yaml
spec:
  billingAccountRef: {name: acme}
  kind: Custom
  description: Remote hands, December
  unitAmount: "150.00"
  quantity: "3.5"
  servicePeriod: {start: "2026-12-01", end: "2026-12-31"}
  tax:
    code: txcd_20060000
  note: Three and a half hours on 4 and 11 December
  invoice: NextInvoice
```

### Catalog and custom charges

`spec.kind` is `Catalog` (the default when `servicePricingRef` is set) or
`Custom`.

| | Catalog | Custom |
|---|---|---|
| `servicePricingRef` | Required. A `OneTime` or `Recurring` pricing; not `Usage`. | Optional. Names the catalog item whose Product (and SKU) the line joins, for reporting. Without it, the line uses the [custom charge product](#the-custom-charge-product). |
| `description` | Defaulted from the pricing's `displayName`. Staff may change it, for example to add a cabinet number. | Required. |
| `unitAmount` | Defaulted from the pricing's `amount`, and must equal it. A different price is a custom charge. | Required. |
| Unit | The pricing's `pricingUnit`, shown in the portal. | None. |
| SKU | The pricing's SKU, carried in metadata only. Never in the description. | The named pricing's SKU, if any. |
| `tax` | Defaulted from the pricing's `tax`. Staff may change the code or mark it not taxable. | Optional. Omitted, the account's default tax code applies. |

A catalog charge may name a customer's own price (a pricing with `basedOn`)
as well as a list price. The portal only offers the account's own customer
prices, next to the list.

The defaulting webhook copies the catalog values onto the spec at creation,
so the stored spec states the line in full. `ServicePricing`s are
immutable, so the copy can't drift from the pricing, and the provider never
needs to look the pricing up to know what to bill.

### Service period

`spec.servicePeriod` is the dates the charge is for. It's what Stripe prints
on the line and what Revenue Recognition spreads the revenue over.

- **Dates, not times.** `start` and `end` are `YYYY-MM-DD`, both included,
  and read as UTC days, the way the portals already show billing periods
  and contract dates. A single day has `start` equal to `end`.
- **End on or after start.** Enforced by CEL.
- **Past and future are both fine.** A period can be in the past (work
  already done) or the future (paid in advance), and longer than a billing
  period.
- **Bounded.** The period can't be longer than five years, and can't start
  more than two years ago or end more than five years ahead. That catches
  typos like 2062, and nothing real needs more. See
  [Open Questions](#open-questions).
- **Defaults to the account's current billing period.** When staff leave it
  out, the defaulting webhook fills it from the account's billing day on
  the day the charge is created:
  - the billing day is `status.contract.billingDayOfMonth` when the account
    has a contract that has started, and otherwise its payment terms'
    invoice day (the 1st under the standard terms);
  - the period runs from the most recent billing day on or before today to
    the day before the next one. Months shorter than the billing day use
    their last day, as billing does elsewhere.

  The portal pre-fills its date pickers with the same period, so what staff
  see is what's stored.

The service period says what the charge is *for*. It doesn't decide *when*
it's invoiced: that's `spec.invoice`. A charge for next quarter added today
is on the next invoice, paid in advance, with next quarter's dates on its
line.

### Amounts and tax

- `quantity` is a positive decimal string with up to two decimal places
  ("2", "3.5"). `unitAmount` is a decimal string with up to two decimal
  places, zero or more. A zero amount is allowed, for a waived install that
  should still appear on the invoice.
- The amount is `quantity × unitAmount`, and it must come to whole cents.
  The webhook refuses a charge where it doesn't ("3.33 × 0.15"), rather than
  round, because Stripe's rounding is never allowed to change an amount (see
  stripe-provider's "Checking amounts"). `status.amount` records it.
- `currency` is `USD`, and must match the account's.
- No discount applies. An Offer's discount is OpenMeter's subscription
  discount, and a charge isn't part of the subscription. The amount staff
  enter is what's billed.
- `tax` has the same shape and rule as `ServicePricing.spec.tax`: a `code`
  (one of Stripe's tax codes) and `taxable`. A charge that isn't taxable
  can't have a code, and needs a note saying why.

### When it's invoiced

`spec.invoice` is:

- `NextInvoice` (the default): the charge goes on the invoice the account
  gets at the end of its current billing period, with that period's usage
  and in-arrears fees and the next period's in-advance fees. If the period
  has already closed and its invoice is waiting to be issued, the charge
  goes on the following one. `status.invoiceAt` says when.
- `Now`: the charge is invoiced on its own as soon as the provider has it,
  and sent like any other invoice. For a charge that shouldn't wait, such
  as an install for a customer who pays by card. An invoice below Stripe's
  minimum charge isn't collected on its own: Stripe carries it to the next
  invoice, as it does today.

### Validation

The validating webhook enforces what CEL can't. Rules are checked on
create; the spec is immutable after that.

| Rule | Where |
|---|---|
| `servicePeriod.end >= servicePeriod.start` | CEL |
| Period within the bounds above | Webhook |
| Spec is immutable | CEL (`self == oldSelf`) |
| `billingAccountRef` names an account in the same namespace that isn't being deleted | Webhook |
| Catalog: the pricing exists, is `OneTime` or `Recurring`, and is in USD | Webhook |
| Catalog: `unitAmount` equals the pricing's `amount` | Webhook |
| Custom: `description` and `unitAmount` are set | CEL |
| `description`: 1 to 256 characters | CRD (OpenMeter's line name allows 256) |
| `quantity × unitAmount` is whole cents | Webhook |
| `tax.code` not set when `tax.taxable` is false; a non-taxable charge has a `note` | CEL |
| `note`: up to 500 characters | CRD (Stripe's metadata value limit) |
| The account's currency is USD | Webhook |

The controller in billing checks the same rules again on reconcile, as
billing does for its other resources, and sets `Ready=False` with a reason
if a pricing has since been deleted.

### Status and lifecycle

| Phase | Meaning | Who sets it |
|---|---|---|
| `Pending` | Accepted, not yet on an invoice in the provider. Reasons include `WaitingForCustomer` and `WaitingForInvoiceWriter`. | billing, on create; the provider while it waits |
| `Scheduled` | On the account's gathering invoice (or a draft, for invoice now). `status.invoiceAt` says when it's invoiced. | Provider |
| `Invoiced` | On an issued invoice. `status.invoiceRef` names the `Invoice`. | Provider |
| `Failed` | The provider refused it, with the reason in `Ready`. Staff remove it and add a corrected one. | Provider |

Printer columns: account, description, amount, period start and end,
phase, age.

### Removing a charge

Staff remove a charge by deleting it.

- **Before it's invoiced** (`Pending` or `Scheduled`): the provider's
  finalizer removes the line from OpenMeter, then lets the charge go.
- **Once it's invoiced**: the webhook refuses the delete. The charge is the
  record of what was billed. A mistake on an issued invoice is corrected
  with a Stripe credit note, as today.
- If the invoice is issued between the webhook's check and the finalizer,
  the line can't be removed. The provider lets the charge go anyway, and
  records a warning event and an activity line saying it was already
  invoiced, so staff know to credit it.

Deleting a billing account invoices its pending lines first
(`PrepareInvoicesForCustomerDeletion`), so a scheduled charge is billed, not
lost, when an account closes.

### RBAC

`Charge` is a new protected resource, under the Organization, like
`CreditGrant`.

- **Charge Admin** (`billing.miloapis.com-charge-admin`, new): create, get,
  list, watch, delete. Bound to staff only, per resource kind, as infra
  binds the other staff billing roles.
- **Billing Entitlement Viewer**, the staff read-only role, gains read.
- **Billing Viewer**, which customers get, does **not**: a charge holds the
  staff note. Customers see the charge only as a line on their invoice.
- The defaulting webhook records who added the charge in the
  `billing.miloapis.com/added-by` annotation, from the request's user. It
  goes onto the Stripe line's metadata, for finance.

Who can add charges, and whether large ones need a second approval, is an
[open question](#open-questions). The first version gates on the role only.

### Activity

Activity policies for `Charge` put these on the account's billing
timeline, next to contracts and payment terms:

- "Added a charge: Cross-connect install, 2 × $300.00, 15 Dec 2026"
  (create, by the staff member);
- "Removed a charge: …" (delete);
- "Charge invoiced on INV-0042" (the provider's `ChargeInvoiced` event).

The note isn't copied onto the timeline. It stays on the charge, which only
staff can read.

## openmeter-provider: a Pending Line on the Gathering Invoice

A new `Charge` controller in openmeter-provider owns the charge from
`Pending` on. It only acts on charges whose account's OpenMeter customer
exists.

### The pending line

It creates one pending line with `CreatePendingInvoiceLine`
(`POST /api/v1/billing/customers/{customerId}/invoices/pending-lines`):

| OpenMeter field | From the charge |
|---|---|
| `currency` | `spec.currency` |
| `name` | `spec.description`. Printed on invoices, so never the SKU. |
| `period.from` | `servicePeriod.start` at 00:00 UTC |
| `period.to` | the day after `servicePeriod.end`, at 00:00 UTC (OpenMeter's period end is exclusive) |
| `invoiceAt` | `NextInvoice`: the end of the subscription's current billing period, read through the v3 subscription API. Without a subscription, the next billing day from the rule above. `Now`: the current time. |
| `rateCard.price` | A flat price of `status.amount`, `paymentTerm: in_advance` |
| `rateCard.taxConfig.stripe.code` | `tax.code`, or Stripe's non-taxable code when `taxable` is false |
| `metadata` | `datum_charge` (namespace/name), `datum_charge_uid`, `datum_quantity`, `datum_unit_amount` |

- **One line, flat.** OpenMeter has no quantity on a flat line, so the line
  is the whole amount. The quantity and unit price travel in our metadata,
  and the invoice writer rebuilds them from the charge.
- **Idempotent.** Before creating, the controller lists the customer's
  gathering invoice and adopts a line whose `datum_charge_uid` matches, so a
  lost response never makes two lines. It records the line's ID in an
  annotation (`openmeter.billing.miloapis.com/line-id`), as it records the
  billing profile's ID today.
- **Removal.** The finalizer deletes the line from the gathering invoice, or
  from the draft invoice while OpenMeter holds it for sync. Once the line is
  on an issued invoice, it leaves it, as above.

### Invoice now

For `invoice: Now`, the controller creates the line with `invoiceAt` now,
then calls `InvoicePendingLinesAction` with `filters.lineIds` set to that
one line. That makes a standard invoice with only the charge on it; the
filter keeps the account's usage so far off it. The invoice then goes
through the same draft sync to Stripe as every other invoice.

OpenMeter holds a new draft for the billing profile's draft period before
issuing it. If that's longer than an "invoice now" should wait, the
controller approves the draft once it's synced. See
[What to confirm on staging first](#what-to-confirm-on-staging-first).

### Status back to the charge

- `Scheduled`, with `invoiceAt`, once the line exists.
- `Invoiced`, with `invoiceRef`, once the line is on an issued invoice. The
  controller finds it through the existing invoice sync, which already
  projects each issued OpenMeter invoice to an `Invoice`: a line carrying
  `datum_charge` marks that charge invoiced.
- `Failed` when OpenMeter refuses the line (a 4xx), with OpenMeter's
  message.

### Previews

Nothing to add. The next-invoice preview (`InvoiceRequest` Preview,
`previewNextInvoice`) reads OpenMeter's draft invoice or the gathering
invoice's flat lines. A charge is a flat line on one of them, so it's in
the preview, with its period, as soon as it's `Scheduled`.

### What to confirm on staging first

Before building the controller, a short spike on staging's OpenMeter
(beta.236.1) should confirm:

1. A manual flat pending line with `invoiceAt` at the end of the billing
   period lands on the same invoice as that period's usage and fees, under
   the billing profile's subscription alignment. It's not collected early.
2. Its `period` comes back unchanged on the issued invoice's line, including
   a period that's in the past, in the future, longer than the billing
   period, or a single day.
3. OpenMeter doesn't split a flat line whose period crosses billing periods.
4. A pending line can be deleted from the gathering invoice, and from a
   draft held for sync, through the API.
5. `InvoicePendingLinesAction` with `lineIds` invoices only that line, with
   progressive billing on.
6. How long the billing profile's draft period holds an invoice-now
   invoice, and whether approving it early is allowed.

If (1) doesn't hold, `NextInvoice` sets `invoiceAt` to the period end and
the controller invoices the line with the period's invoice through the
existing early-invoice path. If (3) doesn't hold, a charge whose period
crosses billing periods is still one line on our side, and the writer
merges OpenMeter's split lines back into one by `datum_charge`.

## Reaching Stripe with Its Period

### This depends on the invoice writer

stripe-provider writes every Stripe invoice line. For OpenMeter's invoices,
that's phase 4 of stripe-provider's [invoicing architecture][arch]:

- **stripe-provider:** the invoice controller, for `StripeInvoice`s with
  `purpose: Invoice`, and its webhooks.
- **openmeter-provider:** OpenMeter's Custom Invoicing app on each account's
  billing profile, and the line builder that turns each draft OpenMeter is
  holding into a `StripeInvoice`.

Neither exists yet: `StripeInvoice` only accepts `Preview` and
`PreviewDraft`, and openmeter-provider has no Custom Invoicing. So charges
are phase 5, after it.

Until an account's billing profile is on Custom Invoicing, the `Charge`
controller leaves its charges `Pending` with reason
`WaitingForInvoiceWriter`. A charge never reaches Stripe through any other
path, so it never reaches Stripe without its product, quantity, note and
period. Charges can still be created and previewed in the portal on such
an account; they're scheduled when the account moves to the writer.

### From charge to Stripe line

The line builder (phase 4) maps every OpenMeter line to a
`StripeInvoiceLine`. For a line whose metadata has `datum_charge`, it reads
the `Charge` and builds:

| `StripeInvoiceLine` | From |
|---|---|
| `key` | OpenMeter's line ID |
| `description` | `spec.description` |
| `servicePricingRef` | `spec.servicePricingRef`, or the custom charge pricing |
| `quantity`, `unitAmount` | `spec.quantity`, `spec.unitAmount` |
| `amount` | OpenMeter's line amount, which equals `quantity × unitAmount` by construction. stripe-provider checks it. |
| `period` | OpenMeter's line period, end made inclusive. The same rule as every other line, and it gives back the charge's dates (below). |
| `tax` | `spec.tax` |
| `note` | `spec.note`. Stripe metadata `datum_note`, never printed. |
| `metadata` | `charge` (namespace/name), `chargeUid`, `chargeKind`, `addedBy`, `servicePricing`. stripe-provider adds `datum_sku` from the Product. |

stripe-provider needs nothing new for charges. It writes `period.start` and
`period.end` on every line already, and refuses a line without one.

### The period at each hop

Taking a charge for 15 December 2026 to 14 January 2027:

| Hop | Start | End | Convention |
|---|---|---|---|
| `Charge.spec.servicePeriod` | `2026-12-15` | `2027-01-14` | Dates, both included, UTC |
| OpenMeter line `period` | `2026-12-15T00:00:00Z` | `2027-01-15T00:00:00Z` | End exclusive |
| `StripeInvoiceLine.period` | `2026-12-15T00:00:00Z` | `2027-01-14T23:59:59Z` | Both included: OpenMeter's end less a nanosecond, truncated to the second, as `invoicePeriod` does for `Invoice` |
| Stripe item `period` | the same instants, as Unix timestamps | | Stripe's periods are inclusive |

A single day, `2026-12-15` to `2026-12-15`, is `[15 Dec, 16 Dec)` in
OpenMeter and `15 Dec 00:00:00` to `15 Dec 23:59:59` in Stripe, so no hop
ever sees an empty period.

Stripe prints dates in the Stripe account's time zone. If that's US
Eastern, a period starting at midnight UTC prints a day early on the PDF.
That already affects every line, not only charges; it's tracked as an open
question in the Stripe invoicing rollout.

### The custom charge product

`StripeInvoiceLine.servicePricingRef` is required: every line's Product
comes from a pricing. A custom charge without one uses a **custom charge
pricing**:

- one `OneTime` `ServicePricing` per environment, `custom-charge`, with
  amount `0`, display name "Custom charge", and the SKU and tax code finance
  chooses;
- kept in each environment's own pricing component, never shared between
  staging and production;
- labelled `billing.miloapis.com/purpose: custom-charge`, so the catalog
  picker and contract price lists hide it;
- named in billing's configuration (`charges.customChargePricing`), so the
  provider knows it.

stripe-provider makes its Product like any other. Finance then sees custom
charges as one product in Stripe's reports, and a custom charge that names
a catalog item joins that item's product instead.

## Staff Portal

On the billing account page in datum-cloud/staff-portal.

### Charges card

A new "Charges" card on the account dashboard, beside Invoices:

- One row per charge, newest first: description, quantity × unit price,
  amount, service period ("15 Dec 2026 – 14 Jan 2027", in UTC like the
  invoice periods), and status: "Next invoice, 1 Jan 2027",
  "Invoicing now", "Invoiced on INV-0042" (linking to the invoice), or the
  failure reason.
- The note is shown under the description, marked "Staff only".
- A "Remove" action on rows that aren't invoiced, with a confirmation that
  says the charge comes off the next invoice.
- "Add charge" on the card, and in the header's Actions menu, shown only to
  staff with Charge Admin.
- An empty state that says charges added here go on the account's next
  invoice.

### Add charge

A dialog with two tabs, **From catalog** and **Custom**.

From catalog:

- A searchable picker of `OneTime` and `Recurring` pricings, reusing the
  contract editor's SKU-aware fee picker (staff-portal#853). Each row shows
  the display name, price per unit, SKU and tax code. The account's own
  customer prices are listed first. The custom charge pricing is hidden.
- Description, pre-filled from the pricing and editable.
- Quantity, unit price (read-only) and the line amount.

Custom:

- Description, unit price, quantity and the line amount.
- "Product for reporting", optional: a catalog item whose product the
  charge joins. Without it, "Custom charge".

Both:

- **Service period**: start and end date pickers, using the contract
  editor's single-date `DatePicker`, in UTC days. Pre-filled with the
  account's current billing period, with shortcuts for "This billing
  period", "Next billing period" and "One day". The end picker can't go
  before the start. The dialog shows the period as the invoice will:
  "15 Dec 2026 – 14 Jan 2027".
- **Tax**: the pricing's tax code (or the account default for a custom
  charge), with "Change tax code" and "Not taxable". Not taxable makes the
  note required.
- **Note for staff**: "Never shown to the customer", up to 500 characters.
- **When**: "On the next invoice (1 Jan 2027)" or "Invoice now". Invoice
  now says the customer is invoiced straight away, and charged at once if
  they pay by card.
- A summary of the line as the customer will read it: description,
  quantity × unit price, amount and dates, with "Tax is worked out by Stripe
  on the invoice".

Errors from the webhook (a bad period, cents that don't divide) show on
their fields, the way the contract editor shows its rows' errors since
staff-portal#870.

### Preview

The card's "Preview next invoice" opens the existing invoice preview dialog
for the account, for every account, not only contract ones. A scheduled
charge is a line on it, with its period. With the writer, that preview is
Stripe's, tax included.

Previewing an unsaved charge with Stripe's tax, before it's added, would
need an `InvoiceRequest` with a `chargeDraft`, like `contractDraft`. It's
not in the first version: a scheduled charge can be previewed and removed,
which covers the same need.

## Service Periods on One-Time Contract Fees

The enhancement says a setup fee's period is the day it's charged, unless
staff give one, such as the installation date. Contract fees
(`Offer.spec.servicePricingRefs[]`) have no such field today; a one-time
fee's line has whatever period OpenMeter gives it.

Recommendation:

- **Default to the day it's charged**, not the contract's first period.
  That answers the enhancement's open question, and it's how Revenue
  Recognition treats a one-off service already. The line builder sets it
  for one-time fees, whatever OpenMeter's line says.
- **Add an optional `servicePeriod` to one-time contract fees**, with the
  same type and rules as the charge's. Only allowed on `OneTime` pricings,
  set in the contract editor next to the fee's quantity, and immutable once
  the contract has started, as one-time fees already are. The line builder
  uses it for that fee's line; OpenMeter's subscription doesn't need to
  know.
- **Do it after charges.** Most installation dates aren't known when the
  contract is written, and a one-time fee can't be amended once the
  contract has started. A charge added when the install is done covers that
  case without touching the contract. The field is for the cases where the
  date is known up front.

## Rollout

| Step | Repo | Depends on |
|---|---|---|
| 0. Spike: the six checks above | openmeter-provider (staging) | Nothing |
| 1. `Charge` CRD, webhooks, controller, IAM, activity policies | billing | Nothing. Charges stay `Pending` until a provider acts on them. |
| 2. Custom charge pricing, per environment | pricing components, finance | Finance's SKU and tax code |
| 3. Invoice writer (phase 4) | stripe-provider, openmeter-provider | Its own design, in stripe-provider |
| 4. `Charge` controller: pending lines, invoice now, removal, status | openmeter-provider | 0, 1; schedules charges only on accounts on Custom Invoicing (3) |
| 5. Line builder reads charges | openmeter-provider | 3, 4 |
| 6. Charges card and Add charge | staff-portal | 1. Useful from 4 on staging. |
| 7. One-time fee service periods | billing, openmeter-provider, staff-portal | 5 |

Steps 1, 2 and 6 can go ahead before the writer. On staging, staff can then
add, preview and remove charges, and they're invoiced once staging's
accounts move to the writer. Production follows the writer's account by
account rollout: an account's charges are scheduled when it moves.

Rough size: billing a week, the openmeter-provider controller and line
builder changes a week and a half after the writer, the staff portal a week.

## Testing

- **billing:** webhook tables for every validation rule and default,
  including periods across short months and billing days 29 to 31; envtest
  for immutability and the delete refusal once invoiced.
- **openmeter-provider:** unit tests for the line mapping and the period
  conversion; the e2e suite against a real OpenMeter for the pending line,
  its period on the issued invoice, invoice now and removal, alongside the
  existing invoice scripts.
- **stripe-provider:** nothing new; line periods are covered.
- **staff-portal:** form tests for the defaults, the UTC dates and the
  errors; a browser check on staging.
- **End to end on staging:** a catalog charge and a custom charge with a
  past period, a future period and a single day, on next invoice and
  invoice now, compared line by line in Stripe test mode: description,
  quantity, unit price, period, tax code and metadata.

## Alternatives

- **Keep adding charges by hand in Stripe.** No period, no product, no
  preview, no audit trail. It keeps working for the exceptions.
- **Write charges straight to Stripe as pending invoice items.** Simple, but
  the writer creates invoices with `pending_invoice_items_behavior: exclude`
  so nothing stray joins them, and OpenMeter's invoice would no longer be
  the whole invoice: previews, the `Invoice` projection and the amount check
  all read OpenMeter.
- **Invoice-now charges as their own `StripeInvoice`, without OpenMeter.**
  stripe-provider could write a one-line invoice from the charge directly,
  with `source.system: Billing`. It needs the writer's invoice controller
  but not Custom Invoicing, so it could ship sooner. It's a second path
  into Stripe for the same kind of charge, though, and next-invoice charges
  still need OpenMeter. Worth it only if invoice now is wanted well before
  the writer.
- **Charges as contract one-time fees.** Works only for accounts with a
  contract, can't be added once the contract has started, and makes every
  install a contract amendment.
- **Lines on an `InvoiceRequest` Issue.** Not a durable record, and Issue
  invoices everything waiting, not just the charge.

## Open Questions

1. **Name.** `Charge` reads well (`kubectl get charges`), but billing
   already has `ChargeType`, `ChargeTrigger` and `ChargeInterval` on
   pricings. `BillingCharge` avoids that.
2. **The custom charge product.** A `custom-charge` pricing per environment
   (above), or a `productRef` on `StripeInvoiceLine` naming a fixed
   `StripeProduct`, which keeps a $0 pricing out of the catalog but changes
   stripe-provider's API. Finance also needs to choose its SKU and tax code.
3. **Who can add charges, and up to what amount** without a second
   approval. The first version gates on the Charge Admin role only.
4. **Charges before the writer.** As far as `main` shows, OpenMeter's own
   Stripe app still writes today's invoices. A pending line sent through it
   would reach Stripe with its period, but as a bare amount: no product,
   quantity or note. This design waits for the writer instead. Is that
   right, or are charges wanted sooner?
5. **Future periods.** Should a charge for a later period wait for the
   invoice of that period, rather than the next invoice? This design always
   bills on the next invoice (or now); a third `invoice` choice could add
   it later.
6. **Period bounds.** Five years long, two years back and five ahead. Does
   finance want tighter limits?
7. **Decimal quantities.** Two decimal places, for hours. Enough?
8. **One-time contract fees.** Go ahead with an optional `servicePeriod`
   after charges, defaulting to the day charged?

## References

- [Writing Our Own Stripe Invoices][enhancement] (enhancements), sections
  "Service Periods" and "Charges Staff Add"
- stripe-provider: [`docs/architecture/invoicing.md`][arch],
  `api/v1alpha1/stripeinvoice_types.go`
- openmeter-provider: `internal/openmeter/invoice.go`
  (`invoicePendingLines`), `internal/openmeter/fixedcharges.go`,
  `internal/controller/invoicerequest_controller.go`,
  `internal/controller/invoice_sync.go` (`invoicePeriod`)
- billing: [Contract Billing](contract-billing.md),
  [Billing Activity](billing-activity.md), [Invoicing](invoicing.md),
  [Credits](credits/README.md)
- OpenMeter beta.236 client: `CreatePendingInvoiceLine`,
  `InvoicePendingLinesAction`
- Stripe: [invoice items](https://docs.stripe.com/api/invoiceitems/create)
  (`period`),
  [Revenue Recognition for invoicing](https://docs.stripe.com/revenue-recognition/methodology/subscriptions-and-invoicing)

[enhancement]: https://github.com/datum-cloud/enhancements/blob/main/enhancements/platform/billing/stripe-invoicing/README.md
[arch]: https://github.com/milo-os/stripe-provider/blob/main/docs/architecture/invoicing.md
