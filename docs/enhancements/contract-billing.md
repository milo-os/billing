---
status: provisional
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
  - [Invoice Requests](#invoice-requests)
  - [Setting Up a Contract](#setting-up-a-contract)
- [Design Details](#design-details)
  - [Validation](#validation)
  - [Provider Requirements](#provider-requirements)
  - [RBAC](#rbac)
  - [Activity](#activity)
- [Next Steps](#next-steps)
  - [End of Term](#end-of-term)
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
  contract starts, that's its first invoice: the setup fee and the first month,
  worked out without creating anything, so staff can check them before anything
  reaches the customer. The first invoice itself is sent automatically when the
  contract starts.

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

### Goals

- Let a contract set the day its charges start and bill on.
- Move an existing account onto a contract without a gap in its billing.
- Amend a contract's charges from the next billing date, so each invoice
  carries one price.
- Let staff preview a contract's first invoice, and an account's next one, from
  the staff portal, without access to the billing provider.
- Keep `Invoice` provider-written: a request leads to an invoice, but only the
  provider creates `Invoice` resources.

### Non-Goals

- Ending or renewing a contract at the end of its term. Decided below, built
  next.
- Letting customers request invoices.
- Invoicing usage on request. Usage stays on the normal cycle.

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
`OneTime` and a `Recurring` pricing; one that also bills usage adds the
platform's `Usage` pricings to the same Offer.

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

Changing a contract's charges, such as its monthly fee, means a new Offer and
pointing `offerRef` at it. For an entitlement with a contract that has started,
the provider applies the new Offer from the account's next billing date: the
current month is billed as agreed, and each invoice carries one price.
Entitlements without a contract still change immediately, as an account
switching Offers in the portal expects.

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
    - description: Dedicated server
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
  its first invoice, worked out from the Offer and the start date. After it
  starts, it's the next invoice: one waiting to be issued, or the charges for
  later periods.
- **Issue** asks the provider to invoice the fixed charges now and send them,
  with `status.invoiceRef` naming the resulting `Invoice`. It's only useful
  with a provider that holds fixed charges until the end of the billing cycle.
  OpenMeter doesn't: it invoices in-advance charges as soon as they're due, so a
  contract's setup fee and first month are invoiced the moment it starts, and
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
2. **Offer.** Create the customer's pricings for the setup fee and the monthly
   charge, and an Offer with them, plus the platform's usage pricings if the
   contract bills usage too. Publish the Offer and point the account's
   `BillingEntitlement` at it, adding the contract's start date.
3. **Check.** Before the start date, create a `Preview` request and compare the
   first invoice's lines with the contract.

When the contract starts, the first invoice (setup fee and first month) is sent
automatically, and later months follow each cycle.

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

### Provider Requirements

An invoicing provider:

- Starts charging an entitlement's Offer on `contract.startsAt`, and bills it
  on that day of each month, when set.
- When a contract is added to an entitlement it already bills, keeps billing
  the account as before until `contract.startsAt`, then moves it onto
  `offerRef` and the contract's billing day.
- Once a contract has started, applies a change of `offerRef` from the
  account's next billing date.
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

For openmeter-provider, the start date becomes the subscription's billing
anchor, and the provider subscribes the account on that day (straight away for
today). It waits rather than scheduling a subscription ahead, because OpenMeter
can't delete a scheduled subscription cleanly. OpenMeter then invoices the setup
fee and first month straight away, since in-advance charges are invoiced as soon
as they're due. Before the start, a Preview prices those charges from the Offer
itself; after it, a Preview shows the draft waiting to be issued or the charges
for later periods. Issue requests fail as `Unsupported`.

The provider waits in the same way to move an account onto a contract: the
account's subscription carries on until the start date, then changes plan with
the contract's start date as its new billing anchor. For an amendment it waits
until the end of the subscription's current billing period and changes plan
then, timed at that boundary so the old Offer bills the whole period. It records
on the entitlement that the contract has started, so a change made after that
is treated as an amendment rather than the move onto the contract.

### RBAC

`InvoiceRequest` is a new protected resource. `Billing Entitlement Admin`, the
staff role that assigns Offers, gains `create` and `delete`, and `Billing
Entitlement Viewer` gains read access. As with the other staff roles, infra
binds them to staff per resource kind, so `InvoiceRequest` needs its own
binding. Customers aren't granted either.

A contract's fees are its own `ServicePricing`s, which staff create.
`Service Pricing Admin` would also let them change or delete the platform's
pricings, so `Service Pricing Creator` grants `create` (and read) only. Once
created, a pricing's spec is immutable, so a change of fee is a new pricing. The invoicing provider needs read
access to requests and write access to their status.

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
