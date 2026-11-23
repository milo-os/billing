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
  - [The item: catalog or custom](#the-item-catalog-or-custom)
  - [Service period](#service-period)
  - [Amounts, discounts and tax](#amounts-discounts-and-tax)
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

This enhancement adds a `Charge` resource to billing. Staff add one to a
billing account the way they'd add a line in Stripe's invoice editor: pick
a catalog item (a `ServicePricing` fills in the description, price, unit,
SKU and tax code, any of which staff can change) or type a custom item,
then set a quantity, a **service period** (Stripe's "supply date"), a tax
code, a discount and a note for staff, and choose to put it on the next
invoice or invoice it now.

It also lets a contract's one-time fees, such as a setup fee, carry a
service period of their own, such as the installation date. Charges cover
what's added after a contract starts or to accounts without one; contract
fees cover what's known when the contract is written.

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
  catalog or custom, with a quantity, a service period, a staff note, a
  discount and tax settings. The form works like a line in Stripe's invoice
  editor, so it's familiar to whoever does this in Stripe today.
- A catalog item's price, description and tax code are defaults, not
  locks: staff can change them for one charge, and the line still counts
  as that product in Stripe's reports.
- A contract's one-time fees can have a service period too.
- The charge's service period reaches the Stripe line as `period.start` and
  `period.end`, unchanged.
- The charge is on the account's next invoice with everything else, or on
  an invoice of its own when staff choose "invoice now".
- It shows in the account's invoice preview before it's billed, and staff
  can remove it until a day before its invoice date.
- It's a platform resource, with RBAC and an activity trail.

Non-goals:

- **Credits and refunds.** Negative charges aren't allowed: Stripe Tax won't
  calculate an invoice with negative lines. Credits are `CreditGrant`s, and
  refunds or corrections to an issued invoice are Stripe credit notes.
- **Recurring charges.** A charge is billed once. Something billed every
  month is a contract fee.
- **Editing a charge.** A charge is immutable. To change one, remove it and
  add another, while it's still held (see
  [Removing a charge](#removing-a-charge)).
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
  # The item. Optional: without it, this is a custom item.
  servicePricingRef:
    name: cross-connect-install-v1
  description: Cross-connect install, cage 12   # defaulted from the pricing
  quantity: "2"
  unitAmount: "250.00"      # defaulted from the pricing; here, a lower price
  currency: USD
  tax:
    code: txcd_20030000     # defaulted from the pricing
  discountPercent: "10"     # optional
  servicePeriod:            # Stripe's "supply date"
    start: "2026-12-15"     # dates, inclusive, in UTC
    end: "2026-12-15"       # a single day is allowed
  note: Installed for the new cage; agreed price, ticket OPS-1234
  invoice: NextInvoice      # NextInvoice | Now
  # invoiceGroup:           # optional, invoice-now only: lines sent together
  #   name: install-cage-12
  #   size: 2
status:
  phase: Scheduled          # Pending | Scheduled | Invoiced | Failed
  amount: "500.00"          # quantity × unitAmount, before the discount
  listUnitAmount: "300.00"  # the pricing's own price, when it was changed
  invoiceAt: "2027-01-01T00:00:00Z"
  removableUntil: "2026-12-31T00:00:00Z"   # when the provider puts it on the invoice
  invoiceRef:
    name: ""                # the Invoice once it's issued
  conditions:
    - type: Ready
      status: "True"
      reason: Scheduled
    - type: OnInvoice       # True once it's on the provider's invoice
      status: "False"
      reason: Held
  observedGeneration: 1
```

A custom item has no `servicePricingRef` and states its own description,
price and tax:

```yaml
spec:
  billingAccountRef: {name: acme}
  description: Remote hands, December
  quantity: "3.5"
  unitAmount: "150.00"
  tax:
    code: txcd_20060000
  servicePeriod: {start: "2026-12-01", end: "2026-12-31"}
  note: Three and a half hours on 4 and 11 December
  invoice: NextInvoice
```

### The item: catalog or custom

There's one kind of charge, as there's one kind of line in Stripe's
invoice editor. The item is either a catalog item or a custom one, and a
catalog item only supplies defaults.

| Field | With `servicePricingRef` (catalog item) | Without (custom item) |
|---|---|---|
| `servicePricingRef` | A `OneTime` or `Recurring` pricing; not `Usage`. A list price, or the account's own customer price (`basedOn`). | Not set. |
| `description` | Defaulted from the pricing's `displayName`. Staff can change it, for example to add a cage number. | Required. |
| `unitAmount` | Defaulted from the pricing's `amount`. Staff can change it. | Required. |
| Unit | The pricing's `pricingUnit`, shown in the portal and on the line's metadata. | None. |
| Product and SKU | The pricing's: the line counts as that product in Stripe's reports, at whatever price. The SKU is in metadata only, never in the description. | The [custom charge product](#the-custom-charge-product). |
| `tax` | Defaulted from the pricing's `tax`. Staff can change the code or mark it not taxable. | Optional. Omitted, the account's default tax code applies. |

This is the enhancement's [price override][overrides] rule: the Product says
what was sold, the line says what it cost. When staff change a catalog
item's price, `status.listUnitAmount` keeps the pricing's own, and it goes
on the Stripe line's metadata, so finance can report it against list.

The defaulting webhook copies the pricing's values onto the spec at
creation, for any field staff left empty, so the stored spec states the
line in full. `ServicePricing`s are immutable, so the defaults can't drift,
and the provider never looks the pricing up to know what to bill.

### Service period

`spec.servicePeriod` is the dates the charge is for: Stripe's "supply
date". It's what Stripe prints on the line and what Revenue Recognition
spreads the revenue over. Its type, `ServicePeriod`, is shared with
[one-time contract fees](#service-periods-on-one-time-contract-fees).

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
- **Defaults to the account's current billing period.** Stripe's lines can
  go without a supply date; ours can't, because Revenue Recognition needs
  one on every line. So when staff leave it out, the defaulting webhook
  fills it from the account's billing day on the day the charge is created:
  - the billing day is `status.contract.billingDayOfMonth` when the account
    has a contract that has started, and otherwise its payment terms'
    invoice day (the 1st under the standard terms);
  - the period runs from the most recent billing day on or before today to
    the day before the next one. Months shorter than the billing day use
    their last day, as billing does elsewhere.

  The portal shows this default when "Supply date" is unticked, so what
  staff see is what's stored.

The service period says what the charge is *for*. It doesn't decide *when*
it's invoiced: that's `spec.invoice`. A charge for next quarter added today
is on the next invoice, paid in advance, with next quarter's dates on its
line.

### Amounts, discounts and tax

- `quantity` is a positive decimal string with up to two decimal places
  ("2", "3.5"). `unitAmount` is a decimal string with up to two decimal
  places, zero or more. A zero amount is allowed, for a waived install that
  should still appear on the invoice.
- The amount is `quantity × unitAmount`, and it must come to whole cents.
  The webhook refuses a charge where it doesn't ("3.33 × 0.15"), rather than
  round, because Stripe's rounding is never allowed to change an amount (see
  stripe-provider's "Checking amounts"). `status.amount` records it.
- Stripe's editor can price "per N units" (a pack price). A charge is
  always per unit: a pack is entered as the pack's price, with the quantity
  in packs.
- `currency` is `USD`, and must match the account's.
- `discountPercent` is optional: a percentage off this line, like Stripe's
  "item coupons". It's set once, on the charge, and each system applies it
  once, as a discount and never by lowering the amount:
  - OpenMeter gets it as the rate card's percentage discount, so its line
    has an amount before the discount and a discount beside it, and its
    invoice total is after the discount;
  - Stripe gets it as one of stripe-provider's standing percentage coupons
    on the line (`datum-pct-10`), so reports show the full price and the
    discount apart;
  - the checks compare like with like: each line's amount before the
    discount (`quantity × unitAmount`, OpenMeter's line amount and Stripe's
    item amount must all agree), then the invoice's total after discounts
    (OpenMeter's and Stripe's must agree).

  Nothing else discounts a charge: an Offer's discount
  belongs to OpenMeter's subscription, and a charge isn't part of it. So
  Stripe's "Discountable" switch, which is about invoice-wide coupons, has
  nothing to do here, since we don't use invoice-wide coupons.
- `tax` has the same shape and rule as `ServicePricing.spec.tax`: a `code`
  (one of Stripe's tax codes) and `taxable`. A charge that isn't taxable
  can't have a code, and needs a note saying why.
- Tax is always added on top of the price (Stripe's tax behavior
  `exclusive`), as on every other line: stripe-provider writes every line
  that way, because Stripe Tax won't calculate a line without it. So the
  form has no "Include tax in price" choice. See
  [Open Questions](#open-questions).

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

Several invoice-now charges can go on one invoice together, for an invoice
of several lines sent at once. Each carries the same `spec.invoiceGroup`: a
`name`, unique among the account's charges, and the group's `size`, from 2
to 50. None of them is invoiced until all `size` are added, so the provider
never sends an invoice with only some of its lines. Then they all go on one
invoice of their own. Staff add a group from Add charge by adding more than
one line before invoicing now.

A group whose charges never all arrive waits, each charge `Pending` with
reason `WaitingForInvoiceGroup` and how many of the group are added. Its
charges can still be removed, since none is on an invoice yet; staff remove
the rest and add the lines again.

### Validation

The validating webhook enforces what CEL can't. Rules are checked on
create; the spec is immutable after that.

| Rule | Where |
|---|---|
| `servicePeriod.end >= servicePeriod.start` | CEL |
| Period within the bounds above | Webhook |
| Spec is immutable | CEL (`self == oldSelf`) |
| `billingAccountRef` names an account in the same namespace that isn't being deleted, and whose `InvoiceWriterReady` condition is True | Webhook |
| With `servicePricingRef`: the pricing exists, is `OneTime` or `Recurring`, and is in USD; a customer price belongs to this account's contract | Webhook |
| Without `servicePricingRef`: `description` and `unitAmount` are set | CEL |
| `discountPercent`: more than 0, up to 100, at most two decimal places | CRD |
| `description`: 1 to 256 characters | CRD (OpenMeter's line name allows 256) |
| `quantity × unitAmount` is whole cents | Webhook |
| `tax.code` not set when `tax.taxable` is false; a non-taxable charge has a `note` | CEL |
| `note`: up to 500 characters | CRD (Stripe's metadata value limit) |
| The account's currency is USD | Webhook |
| `invoiceGroup` only with `invoice: Now`; `size` 2 to 50; `name` a DNS label | CEL, CRD |
| An `invoiceGroup` has room, its charges agree on `size`, and none of them is on an invoice yet | Webhook |

The controller in billing checks the same rules again on reconcile, as
billing does for its other resources, and sets `Ready=False` with a reason
if a pricing has since been deleted.

### Status and lifecycle

| Phase | Meaning | Who sets it |
|---|---|---|
| `Pending` | Accepted, not yet on an invoice in the provider. Reasons include `WaitingForCustomer`, and `WaitingForInvoiceWriter` for an account moved back off the writer. | billing, on create; the provider while it waits |
| `Scheduled` | Due on an invoice. `status.invoiceAt` says when it's invoiced. Until `status.removableUntil` the charge is held in billing alone; after that it's on the account's gathering invoice (or a draft, for invoice now), and `OnInvoice` is True. | Provider |
| `Invoiced` | On an issued invoice. `status.invoiceRef` names the `Invoice`. | Provider |
| `Failed` | The provider refused it, with the reason in `Ready`. Staff remove it and add a corrected one. | Provider |

Printer columns: account, description, amount, period start and end,
phase, age.

### Removing a charge

Staff remove a charge by deleting it. Whether they can depends on whether
it's on the provider's invoice yet, which the `OnInvoice` condition says.

- **Before it's on an invoice** (`Pending`, `Failed`, or `Scheduled` before
  `removableUntil`): the delete goes through, and nothing is billed. The
  provider holds a next-invoice charge in billing until a day before its
  invoice date, so there's nothing to take off an invoice.
- **Once it's on an invoice** (`OnInvoice` True, or `Invoiced`): the webhook
  refuses the delete. A mistake is corrected with a Stripe credit note once
  the invoice is issued, as today. The charge stays as the record of what
  was billed. The exception is when its billing account is being deleted,
  or its namespace is terminating: then the delete is let through, so an
  account or organization can always be removed. The `Invoice` resources
  and Stripe keep the record.

The provider never takes a line off an OpenMeter invoice. OpenMeter
(beta.236) can only remove one line by replacing the whole invoice, and
that replace isn't conditional: a line subscription sync adds or changes
between the read and the write is dropped or reverted, and marked
hand-managed so sync doesn't put it back. Holding the charge until shortly
before its invoice date keeps removal possible for most of a charge's life
without that risk.

The provider and the webhook can't race. The provider sets `OnInvoice`
True (reason `Adding`) with an optimistic write before it creates the
line, and the API server runs the webhook's delete check against the
charge as it is at the moment of the delete. So either the delete lands
first, the provider's write conflicts and it never adds the line, or the
condition lands first and the delete is refused.

When the billing account itself is deleted:

- **Charges on an invoice are billed.** The account's finalizer invoices
  its pending lines before it deletes the OpenMeter customer
  (`PrepareInvoicesForCustomerDeletion`). The `Charge` finalizer leaves the
  line for the account's final invoice and lets the charge go.
- **Charges not on an invoice yet aren't billed** (`Pending`, or
  `Scheduled` and still held). They never reached OpenMeter, so there's
  nothing to invoice. The provider lets them go with a warning event and
  an activity line ("not billed: the account was deleted"). The staff
  portal's delete-account confirmation lists them, so staff can add them
  again as invoice now first.
- **Invoiced charges** are deleted with the namespace, as above.

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

A next-invoice charge is held in billing until a day before its
`invoiceAt` (`status.removableUntil`), and only then put on OpenMeter's
invoice. Until then the provider shows it as `Scheduled` and wakes again at
`removableUntil`, or within the hour if the billing period moves. A charge
added inside that last day is put on the invoice straight away, so it can't
be removed. A day leaves room for the provider to be down or retrying
before OpenMeter cuts the period's invoice. If it misses the cut anyway,
`invoiceAt` moves to the end of the next period, and the charge is held
for the invoice after.

When it's time, the provider sets `OnInvoice` True (reason `Adding`), then
creates one pending line with `CreatePendingInvoiceLine`
(`POST /api/v1/billing/customers/{customerId}/invoices/pending-lines`):

| OpenMeter field | From the charge |
|---|---|
| `currency` | `spec.currency` |
| `name` | `spec.description`. Printed on invoices, so never the SKU. |
| `period.from` | `servicePeriod.start` at 00:00 UTC |
| `period.to` | the day after `servicePeriod.end`, at 00:00 UTC (OpenMeter's period end is exclusive) |
| `invoiceAt` | `NextInvoice`: the end of the subscription's current billing period, read through the v3 subscription API. Without a subscription, the next billing day from the rule above. `Now`: the current time. Worked out again on each reconcile until the line exists. |
| `rateCard.price` | A flat price of `status.amount`, `paymentTerm: in_advance` |
| `rateCard.discounts.percentage` | `discountPercent`, when set, so OpenMeter's total matches Stripe's |
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
- **No removal.** The provider never removes a line; see
  [Removing a charge](#removing-a-charge). If OpenMeter refuses the line
  (a 4xx), `OnInvoice` goes back to False and the charge is `Failed`, so
  staff can remove it.

### Invoice now

For `invoice: Now`, there's no hold: as soon as the account and its
OpenMeter customer are ready, the controller sets `OnInvoice` and creates
the line with `invoiceAt` now,
then calls `InvoicePendingLinesAction` with `filters.lineIds` set to that
one line. That makes a standard invoice with only the charge on it; the
filter keeps the account's usage so far off it. The invoice then goes
through the same draft sync to Stripe as every other invoice.

A charge in an invoice group waits, without a line, until all the group's
charges are added. Then each gets its line as above, and once every one of
them has its line, one `InvoicePendingLinesAction` call names all their
lines, making one invoice with the group's lines and nothing else.

OpenMeter holds a new draft for the billing profile's draft period before
issuing it. If that's longer than an "invoice now" should wait, the
controller approves the draft once it's synced. See
[What to confirm on staging first](#what-to-confirm-on-staging-first).

### Status back to the charge

- `Scheduled`, with `invoiceAt` and `removableUntil`, while it's held, and
  then with `OnInvoice` True (reason `Added`) once the line exists.
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
the preview, with its period, once it's on OpenMeter's invoice. A held
charge isn't there yet, so the provider adds each held next-invoice charge
to the preview of fixed charges waiting for later periods, from the charge
itself. A draft that's waiting to be issued is shown without them: they go
on the invoice after it.

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
7. A percentage discount on a manual flat line's rate card is applied, and
   shows as a discount on the line, not a lower amount.

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

A charge never reaches Stripe through any other path, so it never reaches
Stripe without its product, quantity, note and period. And charges
shouldn't pile up while an account waits for the writer: they'd all bill
on its first invoice afterwards, with periods well in the past. So:

- openmeter-provider sets a condition on the `BillingAccount`,
  `InvoiceWriterReady`, True once the account's billing profile is on
  Custom Invoicing.
- The validating webhook refuses a new charge on an account without it,
  with a message saying charges aren't available for the account yet.
- The staff portal hides Add charge on such an account, and says why on
  the Charges card.
- `Pending` with reason `WaitingForInvoiceWriter` remains only for an
  account moved back off the writer after charges were added. The Charges
  card shows those charges as waiting, with the reason.

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
| `amount` | OpenMeter's line amount **before** its discount (`totals.amount`, not `totals.total`), which equals `quantity × unitAmount`. The builder refuses the line if it doesn't, and stripe-provider checks Stripe's item amount, before its coupon, against it. |
| `period` | OpenMeter's line period, end made inclusive. The same rule as every other line, and it gives back the charge's dates (below). |
| `tax` | `spec.tax` |
| `discountPercent` | `spec.discountPercent`, the same percentage OpenMeter applied. The invoice's total after discounts is then checked against OpenMeter's. |
| `note` | `spec.note`. Stripe metadata `datum_note`, never printed. |
| `metadata` | `charge` (namespace/name), `chargeUid`, `addedBy`, `servicePricing`, `listUnitAmount` when the price was changed, `pricingUnit`. stripe-provider adds `datum_sku` from the Product. |

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
- named in openmeter-provider's configuration
  (`invoiceWriter.customChargePricing`), so the invoice writer knows it.

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
- A "Remove" action on rows that aren't on an invoice yet, saying until
  when ("Can be removed until 31 Dec 2026"), with a confirmation that says
  nothing is billed. Rows already on an invoice say they can be corrected
  with a credit note once the invoice is issued.
- "Add charge" on the card, and in the header's Actions menu, shown only to
  staff with Charge Admin.
- An empty state that says charges added here go on the account's next
  invoice.

### Add charge

A dialog laid out like a line in Stripe's invoice editor, so it's familiar
to whoever adds charges in Stripe today. One form for catalog and custom
items:

```text
Item                                         Qty
[ Search the catalog, or type a name   x ]   [ 2        ]

Price
[ US$ 250.00 ]  per unit                     List price US$ 300.00 · Reset

Item options ^
  Tax code       [ Installation services (txcd_20030000)  edit ]
                 [ ] Not taxable
  Discount       [ 10 ] %
  Supply date    [x] [ 15 Dec 2026 ] -> [ 15 Dec 2026 ]
                     This billing period · Next billing period · Today
  Note for staff [ Installed for the new cage; agreed price      ]
                 Never shown to the customer.

When             (o) On the next invoice, 1 Jan 2027   ( ) Invoice now

On the invoice   Cross-connect install, cage 12
                 2 × US$ 250.00, less 10%           US$ 450.00
                 15 Dec 2026 · tax worked out by Stripe
```

- **Item.** One search box over `OneTime` and `Recurring` pricings, reusing
  the contract editor's SKU-aware fee picker (staff-portal#853). Each result
  shows the display name, price per unit, SKU and tax code, with the
  account's own customer prices first. The custom charge pricing is hidden.
  Typing a name that isn't in the catalog offers "Add *name* as a custom
  item". The item's name becomes the description, editable either way.
- **Qty.** Decimal, up to two places.
- **Price.** Filled from a catalog item and editable. When it differs from
  the pricing's, the form shows the list price next to it, with "Reset".
  Required for a custom item. The unit is the pricing's `pricingUnit`, or
  "unit" for a custom item.
- **Item options**, collapsed by default as in Stripe, opened automatically
  when a catalog item brings a tax code:
  - **Tax code**: the item's, or the account default for a custom item. The
    edit control searches Stripe's tax codes by name, as Stripe's does.
    "Not taxable" makes the note required.
  - **Discount**: an optional percentage, Stripe's "item coupons".
  - **Supply date**: start and end pickers, using the contract editor's
    single-date `DatePicker`, in UTC days. Ticked, staff choose the dates,
    with shortcuts. Unticked, the form says "Current billing period, 1 Dec
    to 31 Dec 2026", which is what's stored. The end can't be before the
    start.
  - **Note for staff**: up to 500 characters.
- **Not offered:** "Include tax in price" (tax is always added on top) and
  "Discountable" (there are no invoice-wide coupons). See
  [Amounts, discounts and tax](#amounts-discounts-and-tax).
- **When**: "On the next invoice" with its date, or "Invoice now".
- **Several lines.** "Add another line" keeps the line just filled in and
  opens a fresh one, so one dialog can add several charges. "When" applies
  to all of them. Invoiced now, two or more lines go out as one invoice
  (an invoice group); on the next invoice they join it anyway.
- **Invoice now asks for confirmation.** Saving opens a second step that
  shows the total before tax and says what happens next, by how the account
  pays: "Acme's card ending 4242 is charged US$ 450.00 plus tax straight
  away", or "Acme is sent an invoice for US$ 450.00 plus tax, due in 30
  days". Staff confirm or go back. This stays until the approval question
  ([open question 3](#open-questions)) is settled.
- **Old periods are flagged.** A supply date that ends before the current
  billing period starts gets a warning under the dates ("These dates are
  before the current billing period"), so a typo in the year or month is
  caught. It's a warning, not an error: catch-up charges are allowed.
- **On the invoice**: the line as the customer will read it, without the
  SKU, the list price or the note.

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
not in the first version: a held charge can be previewed and removed,
which covers the same need.

## Service Periods on One-Time Contract Fees

The enhancement says a setup fee's period is the day it's charged, unless
staff give one, such as the installation date. Contract fees
(`Offer.spec.servicePricingRefs[]`) have no such field today; a one-time
fee's line has whatever period OpenMeter gives it. Both are in scope:
charges for what comes up after a contract starts, and fee periods for
what's known when it's written.

**API.** `servicePricingRefs[]` gains an optional `servicePeriod`, the same
`ServicePeriod` type as a charge's, and the Offer's snapshot
(`ServicePricingSnapshot`) carries it, as it does `quantity`,
`billingTiming` and `periods`:

```yaml
spec:
  servicePricingRefs:
    - name: contract-acme-20261201-fee-1     # OneTime: cage setup
      quantity: 1
      servicePeriod: {start: "2027-01-10", end: "2027-01-10"}   # install date
```

- Only allowed on `OneTime` pricings. A recurring fee's period is the
  period it pays for, worked out per invoice.
- The same rules as a charge's: end on or after start, the same bounds.
- Unset, the period is **the day the fee is charged**, not the contract's
  first period. That answers the enhancement's open question; it's how
  Revenue Recognition treats a one-off service.
- It can change while the contract is a draft or hasn't started. Once the
  contract has started, its one-time fees can't change at all (billing
  refuses that amendment, because OpenMeter would charge them again), so
  neither can their periods. An install date that's only known later is a
  charge.

**Validation.** billing's webhook and the Offer controller check what CEL
can't: a supply date only on a `OneTime` fee, with real dates, the end on
or after the start, and at most a charge's longest period. Unlike a
charge's, it isn't checked against today, since an Offer is validated again
on later updates, long after its fees' dates. `status.contract` lists each
one-time fee's supply date.

**Billing.** The fee's period doesn't change when the fee is charged or how
much. OpenMeter's subscription doesn't need to know about it, so the
Offer controller passes nothing new to the provider's plan.

**openmeter-provider.** The line builder (phase 4) already maps each
OpenMeter line back to its contract fee, for its pricing, quantity and
note. For a one-time fee, it sets the `StripeInvoiceLine`'s period from the
snapshot's `servicePeriod`, or to the day the line is invoiced, whatever
period OpenMeter's line has. The contract preview builds its lines the same
way, so it shows the install date before the contract is saved.

**Staff portal.** In the contract editor, a one-time fee row gets the same
"Item options" as Add charge, with only **Supply date** in it, unticked
meaning "the day it's charged". Tax code and discount aren't offered per
fee: a contract fee is taxed by its pricing, so a product is always taxed
the same way, and discounts are the contract's. The contract card and the
cloud portal's contract card show the date next to the fee.

## Rollout

| Step | Repo | Depends on |
|---|---|---|
| 0. Spike: the seven checks above | openmeter-provider (staging) | Nothing |
| 1. `Charge` CRD, webhooks, controller, IAM, activity policies | billing | Nothing. Charges stay `Pending` until a provider acts on them. |
| 2. Custom charge pricing, per environment | pricing components, finance | Finance's SKU and tax code |
| 3. Invoice writer (phase 4) | stripe-provider, openmeter-provider | Its own design, in stripe-provider |
| 4. `Charge` controller (the hold, pending lines, invoice now, status), and the `InvoiceWriterReady` condition | openmeter-provider | 0, 1, 3 |
| 5. Line builder reads charges | openmeter-provider | 3, 4 |
| 6. Charges card and Add charge, shown only on accounts with `InvoiceWriterReady` | staff-portal | 1, and the condition from 3 |
| 7. `servicePeriod` on one-time contract fees, and the contract editor's supply date | billing, staff-portal | Nothing |
| 8. Line builder uses one-time fee periods | openmeter-provider | 3, 7 |

Steps 1, 2, 6 and 7 can be built and merged before the writer, but Add
charge stays hidden, and the webhook refuses charges, on every account
until it's on the writer (`InvoiceWriterReady`). So nothing waits weeks to
bill. Staging accounts get charges as they move to the writer, and
production follows the writer's account-by-account rollout.

Rough size: billing a week and a half (charges and fee periods), the
openmeter-provider controller and line builder changes a week and a half
after the writer, the staff portal a week and a half.

## Testing

- **billing:** webhook tables for every validation rule and default,
  including periods across short months and billing days 29 to 31; envtest
  for immutability and the delete refusal once on an invoice.
- **openmeter-provider:** unit tests for the line mapping and the period
  conversion; the e2e suite against a real OpenMeter for the pending line,
  its period on the issued invoice, invoice now, the hold and its lead
  time, and removal while held, alongside the
  existing invoice scripts.
- **stripe-provider:** nothing new; line periods are covered.
- **staff-portal:** form tests for the defaults, the UTC dates and the
  errors; a browser check on staging.
- **End to end on staging:** a catalog charge at its list price, one at a
  changed price, and a custom charge, each with a
  past period, a future period and a single day, on next invoice and
  invoice now, compared line by line in Stripe test mode: description,
  quantity, unit price, discount, period, tax code and metadata. A
  contract with a setup fee that has an install date, and one without, in
  the contract preview and on its first invoice.

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
- **A service period on `ServicePricing` instead of a new resource.** A
  pricing is a price, mostly a list price shared by every account, and its
  spec is immutable, so a period on it would mean a new pricing per charge.
  It still wouldn't say which account pays, how many, the note or when to
  invoice; today only the account's Offer bills a pricing.
- **Charges as contract one-time fees only.** Works only for accounts with
  a contract, and a one-time fee can't be added once the contract has
  started. Contract fees get their own period (above) for what's known up
  front; charges cover the rest.
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
8. **Tax included in the price.** Stripe's editor can include tax in a
   line's price. Every line we write adds tax on top, as the enhancement
   says. Does finance ever need tax-inclusive charges?
9. **Changing a catalog item's price.** This design lets any Charge Admin
   change it, recording the list price. Should a lower price need a note,
   or a second approval past some discount?

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
[overrides]: https://github.com/datum-cloud/enhancements/blob/main/enhancements/platform/billing/stripe-invoicing/README.md#price-overrides
