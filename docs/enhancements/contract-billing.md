---
status: implemented
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Contract Billing

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Contract Start Date](#contract-start-date)
  - [Moving an Account onto a Contract](#moving-an-account-onto-a-contract)
  - [Amending a Contract](#amending-a-contract)
  - [Contract Fees](#contract-fees)
  - [Contract Usage Pricing](#contract-usage-pricing)
  - [Invoice Requests](#invoice-requests)
  - [Setting Up a Contract](#setting-up-a-contract)
  - [What Customers See](#what-customers-see)
- [Design Details](#design-details)
  - [Validation](#validation)
  - [Provider Requirements](#provider-requirements)
  - [RBAC](#rbac)
  - [Activity](#activity)
- [Next Steps](#next-steps)
  - [End of Term](#end-of-term)
  - [Contract Rates for Customers](#contract-rates-for-customers)
  - [Minimum Spend Commitments](#minimum-spend-commitments)
- [Alternatives](#alternatives)
- [Implementation History](#implementation-history)

Tracked in [#137](https://github.com/milo-os/billing/issues/137), part of
[#134](https://github.com/milo-os/billing/issues/134).

## Summary

Bare metal customers sign contracts: a one-time setup fee, then a fixed monthly
charge over a term such as 36 months, usually paid by invoice. Staff already
build these as a customer's own `Offer` (a `OneTime` and a `Recurring`
`ServicePricing`), assign it with a `BillingEntitlement`, and grant invoice
terms with a `BillingArrangement`.

Two things are missing, and this enhancement adds them:

- **A contract start date.** `BillingEntitlement.spec.contract.startsAt` says
  when the contract starts. The Offer's charges start then and bill on that day
  of each month, instead of from the day the entitlement was created and on the
  1st.
- **A preview of the first invoice.** An `InvoiceRequest` with mode `Preview`
  asks the invoicing provider what the account will be invoiced next. Before a
  contract starts, that's its first invoice: the setup fee, and the first
  month of any fee billed in advance, worked out without creating anything, so
  staff can check them before anything reaches the customer. The first invoice
  itself is sent automatically when the contract starts.

A contract's charges also need to fit what was negotiated, and this enhancement
covers that too:

- **Fees without a service.** A `OneTime` or `Recurring` pricing no longer has
  to name a catalog service, since a setup or support fee often isn't for any
  one service.
- **Fee quantities.** An Offer can charge a fee more than once, such as three
  servers at $1,000 a month, with `quantity` on its pricing reference.
- **Pricing chosen per contract.** Staff pick the platform's usage pricings
  and fees for a contract directly, rather than copying another Offer's, and
  can give the customer their own price with a pricing based on the list one.
- **Fees billed in advance or in arrears.** One-time fees are invoiced when
  they're charged and usage at the end of each month. Each recurring fee is
  billed in advance or in arrears, as the contract says, and the staff portal
  bills new contract fees in arrears, with the month's usage, unless staff
  choose in advance.
- **Changes at any time.** A change to a contract applies when it's made, not
  from the next billing date, so a fee added mid-month is charged from that
  day.

## Motivation

Without a start date, a contract starting on 18 April but entered on 20 March
starts charging on 20 March, and since
[openmeter-provider#65](https://github.com/milo-os/openmeter-provider/pull/65)
every new subscription bills on the 1st: the first invoice carries the setup fee
and a part month, and each later invoice falls on the 1st rather than the
contract's anniversary.

Staff also have no way to see what a new contract will invoice before the first
invoice reaches the customer. Doing so today means using the billing provider
directly, which the epic rules out.

A contract's fees and rates are also harder to set up than they should be:

- Every fee must name a service, so a contract for something without a catalog
  service, such as bare metal or support, needs a Service registered just to
  give its fees a name.
- A fee can be charged only once per contract. Three identical servers need
  three pricings, and three identical invoice lines.
- A contract that bills usage copies the usage pricings of a published Offer.
  A customer who negotiated one rate, such as a lower vCPU price, needs an
  Offer made for them first, and the
  [pricing enhancement](https://github.com/datum-cloud/enhancements/pull/932)
  sends prices negotiated for one customer through a PR to infra.

### Goals

- Let a contract set the day its charges start and bill on.
- Move an existing account onto a contract without a gap in its billing.
- Amend a contract at any time, with the change applying when it's made.
- Bill each recurring fee in advance or in arrears, as the contract says.
- Let staff preview a contract's first invoice, and an account's next one, from
  the staff portal, without access to the billing provider.
- Keep `Invoice` provider-written: a request leads to an invoice, but only the
  provider creates `Invoice` resources.
- Let a contract charge fees that aren't for a catalog service, and charge a
  fee more than once.
- Let staff choose a contract's usage pricings and fees from the platform's,
  and set a customer's own price for either, from the staff portal, keeping
  the list price alongside.

### Non-Goals

- Ending or renewing a contract at the end of its term. Decided below, built
  next.
- Minimum spend commitments, where a customer commits to spending an amount
  over the term. See [Minimum Spend Commitments](#minimum-spend-commitments).
- Changing how a customer's usage is measured. A customer's own rates price the
  same meter in the same units as the list pricing.
- Letting customers request invoices.
- Invoicing usage on request. Usage stays on the normal cycle.
- Billing usage, or a whole Offer, on a period other than monthly. Only fees
  can have their own period.

## Proposal

### Contract Start Date

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: BillingEntitlement
metadata:
  name: acme
  namespace: organization-acme
spec:
  billingAccountRef:
    name: acme
  offerRef:
    name: acme-bare-metal-2026
  contract:
    startsAt: "2026-03-18T00:00:00Z"
```

`contract.startsAt` is when the contract starts, at midnight UTC:

- The Offer's charges start then. A one-time setup fee is charged for that date.
- Monthly charges bill on that day of each month. A contract starting on the
  18th bills on the 18th; one starting on the 29th, 30th or 31st bills on the
  last day of shorter months.
- It can be today or up to a year ahead, but not in the past. Billing providers
  can't start charging retroactively: OpenMeter refuses to start a subscription
  in the past. A contract signed before it's entered starts on the day it's
  entered, or on a later date such as when the hardware is handed over.

A contract is set when the entitlement is created, or added to an existing one
(see [Moving an Account onto a Contract](#moving-an-account-onto-a-contract)).
Once set, it can't be moved or removed: the provider fixes the billing day when
the contract starts, so a later change wouldn't take effect. Entitlements
without a contract are unchanged: charges start when the entitlement is created
and bill on the 1st.

The Offer can be any mix of pricings. A contract that's only a set fee has a
`OneTime` and a `Recurring` pricing; one that also bills usage adds `Usage`
pricings to the same Offer, the platform's or the customer's own (see
[Contract Usage Pricing](#contract-usage-pricing)).

`contract` is a struct so the term and what happens at its end can join it (see
[End of Term](#end-of-term)).

### Moving an Account onto a Contract

Every account has an entitlement from the moment it's created, on the default
Offer, and an account has only one. So an existing customer moves onto a
contract by updating its entitlement: `offerRef` names the contract's Offer and
`contract.startsAt` is added in the same update.

```yaml
spec:
  billingAccountRef:
    name: acme
  offerRef:
    name: acme-bare-metal-2026   # was: default
  contract:
    startsAt: "2026-12-01T00:00:00Z"
```

Until the contract starts, the account is billed as before, so usage before the
start date is billed on the old Offer. On the start date the provider moves the
account onto the contract's Offer and its billing day. The old Offer's charges
stop then, and the contract's first invoice follows as for a new contract.

Deleting the entitlement and creating one with the contract would do the same in
two steps, but the old subscription would end when the entitlement is deleted,
not when the contract starts, so anything used in between wouldn't be billed.

Until the start, `offerRef` already names the contract's Offer, so what reads it,
such as the account's discounts in its status, shows the contract's Offer early.

### Amending a Contract

Changing a contract's charges, such as adding a fee, changing a fee's quantity
or a usage rate, means a new Offer and pointing `offerRef` at it. A contract can
be changed at any time, whatever its billing day or payment terms, and once it
has started the change applies when it's made:

- **Usage** is rated at the old rates up to the change and the new ones after
  it. Both appear on the invoice at the end of the month, as usual.
- **Fees billed in arrears** that are added, removed or changed are charged
  for the part of the month each applied to, by time. A fourth cross connect
  added on 2 May to a contract billed on the 18th is charged from 2 May, on the
  invoice of 18 May, beside the three charged for the whole month.
- **New one-time fees** are invoiced when they're added. A one-time fee is
  charged once: an amendment that leaves it in, such as the setup fee, doesn't
  charge it again, and one that leaves it out doesn't refund it.
- **Fees billed in advance** change from the next billing date, since the
  current month is already invoiced. That covers adding one, removing one,
  changing its quantity or amount, and moving a fee between in advance and in
  arrears. A customer's new rack price, for example, is a new pricing replacing
  the old one, and changing both at the boundary means the current month isn't
  charged twice.

Before a contract starts, its Offer can change freely, and the contract starts
on whichever Offer it names then. Entitlements without a contract also change
immediately, as an account switching Offers in the portal expects.

### Contract Fees

A contract's fees are `OneTime` and `Recurring` `ServicePricing`s, which staff
create for the contract or reuse across contracts.

**Fees don't need a service.** `serviceRef` is optional on `OneTime` and
`Recurring` pricings. A setup fee, a support fee or a rack fee often isn't for
any one catalog service, and a contract shouldn't need a Service registered
just to give its fees one to name. When a fee sets `serviceRef`, it must still
name a Service. `Usage` pricings still require it, since it must name the
service that owns the meter.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ServicePricing
metadata:
  name: dedicated-server-1000
  namespace: milo-system
spec:
  chargeType: Recurring
  currency: USD
  displayName: Dedicated server
  amount: "1000.00"
  interval: monthly
```

**Fees have a quantity.** An Offer's `servicePricingRefs[].quantity` says how
many of a fee the contract charges. It's a whole number from 1 to 10,000,
defaulting to 1, and it's copied into the Offer's snapshot on publish, like the
reference's discount.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: Offer
metadata:
  name: contract-ba-acme-20261105120000
spec:
  launchStage: GA
  chargeTypes: [OneTime, Recurring]
  servicePricingRefs:
    - name: contract-ba-acme-20261105120000-fee-1   # Setup fee, $1,500
    - name: dedicated-server-1000
      quantity: 3
```

- A pricing is a unit price, and how many the customer takes belongs to the
  contract. So three servers at $1,000 a month are one pricing with quantity 3,
  and one "Dedicated server" pricing can serve any number of contracts.
- An Offer can't list the same pricing twice (its references are keyed by
  name), so quantity is how a fee is charged more than once.
- The fee charged is its amount times its quantity. The invoice line is named
  with the quantity, such as "Dedicated server × 3", for $3,000.00, and the
  reference's discount applies to that total.
- `quantity` is for fees only. The Offer controller refuses to publish an Offer
  that sets it on a `Usage` pricing, whose quantity is what was used.

**Fees have their own billing period.** A `Recurring` fee's `interval` is
`monthly`, `quarterly` or `annual`, and its amount is per interval. Offers are
billed monthly, so a contract can charge a monthly server fee and an annual
support fee side by side, each on its own period.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ServicePricing
metadata:
  name: premium-support-annual-v1
  namespace: milo-system
spec:
  chargeType: Recurring
  currency: USD
  displayName: Premium support
  amount: "12000.00"
  interval: annual
```

- A fee's periods are counted from the account's billing day. On a contract
  starting on 18 April, an annual fee is charged every 18 April and a quarterly
  one every 18 January, April, July and October. On an account without a
  contract they're counted from its billing anchor in the same way.
- Each charge goes on the account's invoice for that month, with its monthly
  fees and usage.
- The interval belongs to the pricing, since it changes what the amount means:
  $12,000 a year is a different price from $1,000 a month. A customer's own fee
  `basedOn` a list fee must have the same interval.
- Usage is still billed monthly. Billing usage quarterly would also change the
  usage cycles the portals, Patch and `datumctl billing` show, which all assume
  a month.

**Fees can have a unit.** A fee may set `pricingUnit`, the unit its amount is
per, as a `Usage` pricing does. It's a label only: it doesn't change what's
charged. When set, invoices and portals show the quantity in that unit, so a
fee charged per port reads "Cross connect: 4 port × $300.00" rather than
"Cross connect × 4". Without it, the quantity is a count of the fee.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ServicePricing
metadata:
  name: cross-connect-300
  namespace: milo-system
spec:
  chargeType: Recurring
  currency: USD
  displayName: Cross connect
  amount: "300.00"
  interval: monthly
  pricingUnit: port
```

- The unit belongs to the pricing, since it's what the amount is per, and
  like the rest of the spec it can't be changed once created.
- It's a short label such as `port`, `kW` or `rack`, shown as written, so
  staff choose the form that reads well after a number.

**Fees are billed in advance or in arrears.** When each charge is invoiced
depends on its type:

- A `OneTime` fee is always billed in advance: it's invoiced when it's
  charged, so a setup fee is payable when the contract starts.
- Usage is always billed in arrears: a month's usage is invoiced at the end of
  that month.
- A `Recurring` fee is billed as the Offer's `servicePricingRefs[].billingTiming`
  says. `InArrears` invoices each month's fee at the end of the month, on the
  same invoice as that month's usage; `InAdvance` bills it at the start of
  each month, as is usual for rent-like charges such as rack space. The staff
  portal sets `InArrears` on new contract fees unless staff choose in advance.
  A reference without it is billed in advance, as every fee was before the
  field existed.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: Offer
metadata:
  name: contract-ba-acme-20261110120000
spec:
  launchStage: GA
  chargeTypes: [OneTime, Recurring, Usage]
  servicePricingRefs:
    - name: contract-ba-acme-20261110120000-fee-1   # Setup fee
    - name: rack-space-v1
      billingTiming: InAdvance
    - name: cross-connect-v1
      quantity: 4
      billingTiming: InArrears
    - name: compute-vcpu-v1
```

A contract starting on 18 April with that Offer gets its first invoice on 18
April, with the setup fee and the first month's rack space. On 18 May it gets
the next: April's cross connects and usage, and May's rack space.

- How a fee is billed is a term of the contract, not of the price, so like
  `quantity` it's on the Offer's reference, and one list fee can be billed in
  advance for one customer and in arrears for another.
- `billingTiming` is for `Recurring` fees only. The Offer controller refuses to
  publish an Offer that sets it on a `OneTime` or `Usage` pricing, whose timing
  is fixed.
- It's copied into the fee's snapshot on publish, only when the reference sets
  it. Providers bill a `Recurring` snapshot without it in advance, as they did
  before it existed, so contracts already running keep their timing until
  they're amended.
- Billing never fills in `InArrears` itself. A provider that doesn't read
  `billingTiming` yet bills every fee in advance; if billing recorded
  `InArrears` for such a fee, the provider would invoice the period it had
  already billed in advance again at its end once it started reading the
  field. So the staff portal only offers the choice once openmeter-provider
  reads it.

### Contract Usage Pricing

A contract that bills usage lists `Usage` pricings in its Offer, next to its
fees. Today the staff portal copies them from a published Offer. Instead, staff
choose them directly, and can set the customer's own rates.

**Choosing pricings.** The staff portal lists the platform's usage pricings by
service, newest first for each meter, with the default Offer's selection ticked
to start from. Customer pricings, those with a `basedOn`, aren't listed. Staff
tick the ones the contract bills, and can give each a discount, as references
already allow. Nothing ties the contract's Offer to another Offer: like any
Offer, it lists any usage pricings, one per meter.

**Customer rates.** When a customer negotiates a rate rather than a percentage
off, staff create a `Usage` pricing for them: a copy of the list pricing with
its rates changed, and `basedOn` naming the list pricing. The contract's Offer
lists it in place of the list pricing.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ServicePricing
metadata:
  name: contract-ba-acme-20261105120000-compute-vcpu
  namespace: milo-system
spec:
  chargeType: Usage
  basedOn: compute-vcpu-v1
  serviceRef: compute.datumapis.com
  currency: USD
  displayName: Compute vCPU
  metric: compute.datumapis.com/instance/milli-cpu-seconds
  pricingUnit: vcpu-second
  unitConversion:
    meterUnitsPerPricingUnit: "1000"
    description: >-
      CPU is metered in millicore-seconds and priced per vCPU-second;
      1 vCPU = 1000 millicores.
  rates:
    - match:
        dimension: runtime_class
        value: general-purpose
      flat: "0.000011"   # list: 0.000014
    - flat: "0.000011"
```

- A customer pricing is the list pricing with different amounts. `basedOn`
  names a `Usage` pricing in the same namespace, and everything but the
  amounts must match it: the meter, the pricing unit and its conversion, the
  dimension matches in the same order, and the tier bounds. Only `flat` and
  tier `rate` values change, so the customer's usage is measured and grouped
  exactly as the list price's is.
- `basedOn` can't name a pricing that has a `basedOn` of its own, so it always
  names a list price.
- The Offer's one-price-per-meter rule already keeps a contract from listing a
  customer pricing and its list pricing together.
- The staff portal uses `basedOn` to show each rate beside the list rate.
  Providers rate the snapshot as they would any other: `basedOn` changes
  nothing about billing.
- Invoices show the contract rate as the price. A discount instead shows the
  list price, the discount and the net amount, so it remains the better choice
  when the customer expects to see list prices.
- Like any pricing, a customer pricing can't be changed once created. A new
  rate is a new pricing in a new Offer, applied when the Offer is.

Customer pricings are made in the staff portal, as contract fees already are.
This replaces the [pricing enhancement](https://github.com/datum-cloud/enhancements/pull/932)'s
rule that prices negotiated for one customer go in a private directory in
infra: they reach Milo without a PR, stay out of the public service repos, and
are still checked against their meter on save. The pricing enhancement will be
updated to match.

### Contract Fees from the Price List

The platform may list fees too, such as a monthly price for a server type or a
cross connect. The staff portal lists them beside the usage pricings, by
service, and staff tick the ones a contract charges, each with a quantity and
an optional discount. The contract's Offer refers to the list fee itself.

When a customer negotiates a different amount, staff create a fee for them
`basedOn` the list fee, as with usage rates. It matches the list fee in
everything but its `amount` and `displayName`: the same `chargeType`,
`serviceRef`, `pricingUnit`, `trigger` and `interval`.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: ServicePricing
metadata:
  name: contract-ba-acme-20261106120000-fee-2
  namespace: milo-system
spec:
  chargeType: Recurring
  basedOn: cross-connect-v1
  currency: USD
  displayName: Cross connect
  amount: "250.00"   # list: 300.00
  interval: monthly
  pricingUnit: port
```

- The staff portal shows the list amount beside the customer's wherever it
  shows the fee: in the contract form and on the account's contract.
- An amendment that keeps the customer's amount reuses their fee, as it does
  for usage rates.
- Fees that aren't on the price list, such as a setup fee, are still created
  for the contract without a `basedOn`.

### Invoice Requests

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: InvoiceRequest
metadata:
  name: acme-first-invoice
  namespace: organization-acme
spec:
  billingAccountRef:
    name: acme
  mode: Preview
status:
  phase: Succeeded
  currencyCode: USD
  lines:
    - description: Server installation
      amount: "1500.00"
    - description: Rack space
      period:
        start: "2026-04-18T00:00:00Z"
        end: "2026-05-17T23:59:59Z"
      amount: "1000.00"
  total: "2500.00"
  completedAt: "2026-03-20T10:02:11Z"
  conditions:
    - type: Completed
      status: "True"
      reason: Succeeded
      message: The first invoice, sent when the contract starts on 2026-04-18
```

An `InvoiceRequest` covers the account's **fixed charges**: the `OneTime` and
`Recurring` pricings of its Offer, such as a contract's setup fee and monthly
fee. Usage isn't covered.

- **Preview** reports what the account will be invoiced next, with lines and a
  total before tax, without creating anything. Before a contract starts that's
  its first invoice, worked out from the Offer and the start date: its one-time
  fees and the first month of fees billed in advance. Fees billed in arrears
  aren't on it; they're invoiced at the end of the month. After it starts, it's
  the next invoice: one waiting to be issued, or the charges for later
  periods.
- **Issue** asks the provider to invoice the fixed charges now and send them,
  with `status.invoiceRef` naming the resulting `Invoice`. It's only useful
  with a provider that holds fixed charges until the end of the billing cycle.
  OpenMeter doesn't: it invoices in-advance charges as soon as they're due, so a
  contract's setup fee and first month in advance are invoiced the moment it
  starts, and
  openmeter-provider fails Issue requests as `Unsupported`.

A request is done once its phase is `Succeeded` or `Failed`; to ask again,
create a new one. The spec is immutable, so a preview can't be turned into an
issue. The `Completed` condition gives the outcome: `Succeeded`,
`NothingToInvoice` (an Issue with no fixed charges waiting, for example because
they were already invoiced), `Unsupported` (the provider can't invoice on
request) or `Failed`.

Requests are owned by their billing account and are removed with it.

### Setting Up a Contract

In the staff portal, on the customer's billing account:

1. **Payment terms.** Grant invoice terms with a `BillingArrangement`, with the
   contract's PO number and agreement reference, which print on every invoice.
2. **Offer.** List the contract's fees, each with its amount, how it's charged
   and how many, and a service only if it's for one. Recurring fees are billed
   in arrears unless staff choose in advance. Tick the usage pricings
   the contract bills, if any, with a discount or the customer's own rates for
   each. The portal creates the pricings the contract needs, an Offer with them
   and the usage pricings, publishes it and points the account's
   `BillingEntitlement` at it, adding the contract's start date.
3. **Check.** Before the start date, create a `Preview` request and compare the
   first invoice's lines with the contract.

When the contract starts, the first invoice (setup fee and the first month of
any fee billed in advance) is sent automatically, and later months follow each
cycle, with fees billed in arrears and usage for the month just ended.

### What Customers See

Customers can't read the entitlement or its Offer, so the BillingAccount
controller copies the contract onto `BillingAccount.status.contract`, as it
copies the Offer's discounts onto `status.discounts`. The portal shows it
beside the account's payment terms:

```yaml
status:
  contract:
    startsAt: "2026-04-18T00:00:00Z"
    billingDayOfMonth: 18
    fees:
      - name: acme-setup
        displayName: Setup fee
        serviceRef: compute.datumapis.com
        chargeType: OneTime
        amount: "5000.00"
        currency: USD
      - name: dedicated-server-1000
        displayName: Dedicated server
        chargeType: Recurring
        amount: "1000.00"
        quantity: 3
        currency: USD
        interval: monthly
        billingTiming: InArrears
    usageBilled: true
```

- It's set as soon as the contract is added, so a customer moving onto one sees
  it before it starts. Fees are before discounts, which stay in
  `status.discounts`.
- Each fee's `amount` is its unit amount, and `quantity` how many the contract
  charges, so the portal shows "3 × $1,000.00", or "4 port × $300.00" for a fee
  with a `pricingUnit`. A fee without a service has no `serviceRef`.
- A recurring fee's `billingTiming` says whether it's billed in advance or in
  arrears, so the portal can say when each fee is invoiced. One-time fees
  leave it out.
- An amended contract shows the new Offer's fees as soon as `offerRef`
  changes, although fees billed in advance change from the next billing date.
- Once the contract has started, billing cycles follow `billingDayOfMonth`
  instead of the payment terms' `invoiceDayOfMonth`: in the portal's usage
  cycles, and in the usage tools of Patch and `datumctl billing`.
- The contract's length isn't shown until [End of Term](#end-of-term) adds it.

## Design Details

### Validation

The `BillingEntitlement` webhook checks, when a contract is set (on create, or
added to an existing entitlement), that `contract.startsAt` is midnight UTC,
today or later and no more than a year ahead.
CEL rules on the CRD keep the contract from being removed and `startsAt` from
changing, and the webhook repeats them.

The `InvoiceRequest` webhook adds an owner reference to the billing account and
checks it exists in the same namespace and isn't being deleted. A CEL rule makes
the spec immutable; `mode` is an enum of `Preview` and `Issue`.

For `ServicePricing`:

- A CEL rule requires `serviceRef` on `Usage` pricings only. The webhook checks
  that a fee's `serviceRef` names a Service when it's set, as before.
- `pricingUnit` stays required on `Usage` pricings and optional on fees. The
  schema already accepts it on fees, so nothing changes but its meaning.
- The webhook checks that `basedOn` names a pricing of the same `chargeType`
  in the same namespace that has no `basedOn` of its own, with the same
  `serviceRef`, `pricingUnit` and `currency`. For a `Usage` pricing it also
  checks the same `metric` and `unitConversion`, the same number of rates with
  the same `match` in the same order, and, rate by rate, flat where it's flat
  and the same tier `upTo` bounds where it's tiered; the usual meter checks
  apply too. For a fee it checks the same `trigger` and `interval`, leaving
  `amount` and `displayName` free.

For `Offer`:

- `servicePricingRefs[].quantity` is a whole number from 1 to 10,000.
- `servicePricingRefs[].billingTiming` is an enum of `InAdvance` and
  `InArrears`.
- The Offer controller refuses to publish an Offer with `quantity` on a `Usage`
  pricing, as it refuses one that prices a meter twice, and copies `quantity`
  into each fee's snapshot, defaulting to 1.
- It also refuses one with `billingTiming` on anything but a `Recurring`
  pricing, and copies `billingTiming` into each recurring fee's snapshot when
  the reference sets it.

### Provider Requirements

An invoicing provider:

- Starts charging an entitlement's Offer on `contract.startsAt`, and bills it
  on that day of each month, when set.
- When a contract is added to an entitlement it already bills, keeps billing
  the account as before until `contract.startsAt`, then moves it onto
  `offerRef` and the contract's billing day.
- Once a contract has started, applies a change of `offerRef` when it's
  made: usage, fees billed in arrears and new one-time fees at once, prorated
  by time, and fees billed in advance from the next billing date. It never
  charges a one-time fee twice.
- Bills each `Recurring` fee by its snapshot's `billingTiming`, and one
  without it in advance.
- Fulfils `InvoiceRequest`s for accounts it invoices, writing status only:
  - Lines and total cover the account's fixed charges, after discounts and
    before tax.
  - A Preview creates nothing. Before a contract starts it reports the first
    invoice the contract will get.
  - An Issue creates the invoice from the fixed charges waiting, and records
    the projected `Invoice` in `status.invoiceRef`; with nothing waiting it
    fails with `NothingToInvoice` rather than creating an empty invoice.
- Marks requests it can't fulfil `Failed` with reason `Unsupported`, rather than
  leaving them `Pending`.
- Charges a fee's amount times its snapshot `quantity`, in invoices and
  previews alike. It names the line with the quantity and unit when the fee
  has a `pricingUnit` ("Cross connect: 4 port × $300.00"), and otherwise with
  the quantity when it's more than 1 ("Dedicated server × 3").
- Doesn't need `serviceRef` on a fee.
- Charges a `Recurring` fee once per its `interval`, with periods counted from
  the account's billing day.
- Holds a change to a quarterly or annual fee billed in advance until the end
  of the fee's own period, and never sends it late: a missed boundary waits for
  the next one.

OpenMeter lets a rate card bill on its own cadence when it lines up with the
plan's, as `P3M` and `P1Y` do with the plans' `P1M`. So openmeter-provider gives
each fee's rate card the cadence of its interval: `P1M`, `P3M` or `P1Y`. A card's
periods follow its cadence from the subscription's billing anchor, and OpenMeter
invoices each charge with whatever else is due then.

A quarterly or annual fee billed in advance needs care when a contract is
amended. It should change at the end of its own period, but OpenMeter's
`next_billing_cycle` edit timing is the end of the subscription's monthly
billing period, not the rate card's, and an edit to a running subscription
can't take a custom time. Edited at the next monthly billing date, an annual
fee charged on 18 April would end on, say, 18 July, and the new version would
be charged pro rata from 18 July to the next 18 April. OpenMeter wouldn't credit
the year already invoiced: an issued invoice can't change, and its subscription
sync only records a validation issue against it. The customer would pay twice
for July to April.

Changing plan, as amendments did before, doesn't avoid this. Ending the old
subscription in July leaves the year's invoice standing in the same way, and
the new subscription charges every one-time fee again. A plan change timed for
18 April would be safe, but it would hold back the whole amendment, usage
prices and monthly fees included, for up to a year.

What made the old plan changes safe was their timing: they waited for a
billing date when nothing had been paid in advance. With monthly fees that's
every billing date; with an annual fee it's only the anniversary. So
openmeter-provider keeps that timing for just the fees that need it, and uses
the hold it already has for deferred plan changes (`periodChangeTime` and
`ChangeDue`):

- The rest of the amendment is sent as an edit straight away.
- A change to a quarterly or annual in-advance fee is held, with the end of the
  fee's own period as its boundary instead of the subscription's month.
- It's sent as an edit to that fee alone, not a plan change, so one-time fees
  aren't charged again. Since an edit can't take a custom time, the provider
  sends it during the last monthly billing period before the boundary with
  `next_billing_cycle` timing, which lands on the boundary.
- If the provider misses that month, because it was down, say, the change waits
  for the fee's next period. That's unlike a deferred plan change, which
  `ChangeDue` then makes straight away: once the boundary has passed, the next
  period has been invoiced at the old price, and a late change would charge
  that period twice.

Fees billed in arrears don't need any of this: the old version is charged pro
rata up to the change and the new one from it.

OpenMeter's flat-price rate cards have no quantity, so openmeter-provider sets
the rate card's amount to the fee's amount times its quantity and adds the
quantity, and the unit if there is one, to its name. Rate card keys stay per
pricing, so a fee keeps its key when only its quantity changes. Fees without a
service leave it out of the rate card's description. Customer usage pricings
need no change: the provider rates their snapshot like any other.

Rate cards for `OneTime` fees are in advance, as are `Recurring` fees whose
snapshot says `InAdvance` or leaves `billingTiming` out. Other `Recurring` fees
are in arrears. Plans leave OpenMeter's proration on, its default, so a fee in
arrears is charged by time for a part month.

For openmeter-provider, the start date becomes the subscription's billing
anchor, and the provider subscribes the account on that day (straight away for
today). It waits rather than scheduling a subscription ahead, because OpenMeter
can't delete a scheduled subscription cleanly. OpenMeter then invoices the setup
fee and the first month of fees billed in advance straight away, since
in-advance charges are invoiced as soon as they're due. Before the start, a Preview prices those charges from the Offer
itself; after it, a Preview shows the draft waiting to be issued or the charges
for later periods. Issue requests fail as `Unsupported`.

The provider waits in the same way to move an account onto a contract: the
account's subscription carries on until the start date, then changes plan with
the contract's start date as its new billing anchor. It records on the
entitlement that the contract has started, so a change made after that is
treated as an amendment rather than the move onto the contract.
([openmeter-provider#93](https://github.com/milo-os/openmeter-provider/pull/93))

An amendment doesn't change plan. A plan change in OpenMeter ends the
subscription and starts another, which would:

- invoice the old subscription's usage straight away, on an invoice of its own,
  rather than at the end of the month;
- charge each in-advance fee again for the rest of the month, since OpenMeter
  doesn't credit the part of an issued invoice that the old subscription no
  longer covers;
- charge every one-time fee in the new plan again, as part of a new
  subscription.

Instead the provider edits the account's subscription in place, comparing its
items with the new Offer's rate cards by key:

- One edit, with timing `immediate`, makes the changes that apply at once:
  usage rate cards and fees in arrears that are added, removed or replaced, and
  new one-time fees. OpenMeter closes a replaced item at the edit and prorates
  both versions, and invoices the usage and fees of both on the usual invoice.
- A second, with timing `next_billing_cycle`, makes the changes to fees in
  advance.
- Items whose rate card is unchanged, and one-time fees already on the
  subscription, are left alone, so nothing is charged twice. A one-time fee
  missing from the new Offer stays on the subscription.

The subscription stays on the plan it started with, so the provider records the
Offer it last applied on the entitlement and compares the subscription's items,
not its plan, with that Offer to decide whether it's up to date. An amendment
made while a `next_billing_cycle` edit is still waiting replaces what's
waiting.

This needs OpenMeter's v3 subscription edit API, added after the version
deployed (1.0.0-beta.232): v1 edits can't carry a usage rate card's
`unit_config`. So OpenMeter is upgraded first. To be checked against a running
OpenMeter before relying on it:

- how a second edit replaces a waiting `next_billing_cycle` edit;
- that proration of a fee replaced mid-month rounds each part to the cent,
  which can leave the two parts a cent off the full price;
- that an immediate edit adds no invoice of its own for fees in arrears or
  usage.

### RBAC

`InvoiceRequest` is a new protected resource. `Billing Entitlement Admin`, the
staff role that assigns Offers, gains `create` and `delete`, and `Billing
Entitlement Viewer` gains read access. As with the other staff roles, infra
binds them to staff per resource kind, so `InvoiceRequest` needs its own
binding. Customers aren't granted either.

A contract's fees are its own `ServicePricing`s, which staff create.
`Service Pricing Admin` would also let them change or delete the platform's
pricings, so `Service Pricing Creator` grants `create` (and read) only. Once
created, a pricing's spec is immutable, so a change of fee is a new pricing.
Customer usage pricings are created with the same role, so no new one is
needed. The invoicing provider needs read access to requests and write access
to their status.

### Activity

Creating a request is recorded on the billing account's timeline: "Sam issued an
invoice to billing account acme", or "Sam previewed the next invoice for billing
account acme". Deleting one is recorded too. The provider's status writes are
system actions and aren't shown.

## Next Steps

### End of Term

A contract will carry its term and what happens when it ends. Billing tools
handle this the same way: Stripe subscription schedules either release the
subscription to carry on or cancel it; Chargebee contract terms renew, roll to
month-to-month or cancel; Zuora and Recurly subscriptions either renew for
another term or become evergreen. Bare metal and colocation contracts usually
roll to month-to-month or renew unless notice is given, rather than stopping.

So a contract will choose what happens at the end of its term, defaulting to
**continuing month-to-month at the same price**, with renewing for another term
and stopping as the other choices. Billing will flag the account for renewal 90
days before the end, using the existing payment terms renewal flow, so staff can
renegotiate in time. Stopping by default would stop charging for hardware that
is still running, without anyone deciding to.

Continuing past the term also needs the account to stay payable: if its invoice
terms end with the contract, the renewal flag covers them too.

### Contract Rates for Customers

`status.contract` says whether a contract bills usage, but not at what rates.
A customer with their own rates will want to see them beside the list price,
so a later change can add the contract's usage rates, with the list rate from
`basedOn`, to `status.contract`.

### Minimum Spend Commitments

The [platform enhancement](https://github.com/datum-cloud/enhancements/blob/main/enhancements/platform/README.md)
sketches commitments: a customer commits to spending an amount over a term,
often for a discount, with any shortfall invoiced at the end. That's different
from a contract's fees, which are charged whatever is used, and it isn't built.
OpenMeter rate cards already carry minimum and maximum spend commitments, which
a later design can build on, with the commitment held on the entitlement's
`contract` beside its term.

## Alternatives

**Letting consumers create `Invoice`s.** [Invoicing](./invoicing.md) rejected
modelling `Invoice` as a request, because providers run their own cycles and
own the record. That still holds: `InvoiceRequest` is a separate resource, and
`Invoice` stays provider-written. What has changed is that OpenMeter can invoice
pending lines on demand, so a request can be honoured; providers that can't say
so with `Unsupported`.

**An annotation on `BillingAccount`.** An "invoice now" annotation would avoid
a new resource, but it has nowhere to report the preview's lines or the
outcome, gives no separate permission (anyone who can edit the account could
send an invoice), and records nothing in the activity timeline.

**A start date on the `BillingArrangement`.** The arrangement's `startsAt` is
when payment terms apply, which needn't match when charges start: a contract
customer might pay by card. The start date belongs with the Offer assignment
that the charges come from.

**A catalog Service for contract fees.** Registering a "contract" or "bare
metal" Service would give every fee a `serviceRef` without an API change, but
it's a Service that serves nothing, and customers would see its name on fees
it has nothing to do with.

**Quantity on the `ServicePricing`.** A pricing's spec is its unit price, and
one pricing can be used by several contracts, each with its own quantity. Like
a discount, how many a contract charges belongs on the Offer's reference.

**Listing a fee more than once.** References are keyed by name, and charging
three servers as three lines would repeat the same line on every invoice.

**Rate overrides on the Offer's reference.** Overriding a usage pricing's rates
on `servicePricingRefs` would save creating a pricing, but prices would then
live in two places, the snapshot would have to merge them, and the webhook's
meter checks wouldn't see the rates that are billed. A customer pricing keeps
"a new price is a new `ServicePricing`", and `basedOn` keeps its link to the
list price.

## Implementation History

- 2026-11-02: Enhancement drafted, with the contract start date and
  `InvoiceRequest`.
- 2026-11-02: OpenMeter turned out to invoice in-advance charges as soon as
  they're due, so a contract's first invoice is sent automatically when it
  starts. Preview now centres on the first invoice before the start, and
  openmeter-provider doesn't support Issue.
- 2026-11-03: Contracts can be added to existing entitlements, moving an account
  onto one on its start date, and amendments to a started contract apply from
  the next billing date. Added the `Service Pricing Creator` role.
- 2026-11-04: Contracts are published on `BillingAccount.status.contract` for
  customers, and billing cycles follow a started contract's billing day.
- 2026-11-05: Fees can leave out the service and carry a quantity on the
  Offer's reference. Staff choose a contract's usage pricings directly, and set
  a customer's own rates with a `Usage` pricing `basedOn` the list one.
- 2026-11-05: Fees can set `pricingUnit`, shown with their quantity on
  invoices and in portals.
- 2026-11-06: Staff pick fees from the price list too, and a customer's own
  fee amount is a fee `basedOn` the list one, so the list price stays visible.
- 2026-11-10: Recurring fees are billed in arrears unless the Offer's
  reference says `InAdvance`. A started contract's amendments apply when
  they're made, except to fees in advance, which change from the next billing
  date, and openmeter-provider edits the subscription in place rather than
  changing plan.
- 2026-11-10: Recurring fees can be quarterly or annual, each on its own
  period within an Offer billed monthly.
- 2026-11-12: Billing no longer defaults `billingTiming` to `InArrears`: a
  reference without it is billed in advance, and the staff portal sets
  `InArrears` on new contract fees instead. A default filled in before
  openmeter-provider read the field would have recorded fees it bills in
  advance as `InArrears`, and charged their period again once it did.
