---
status: implementable
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Contract Renewals

- [Summary](#summary)
- [What we have today](#what-we-have-today)
- [Goals](#goals)
- [Non-goals](#non-goals)
- [Design](#design)
  - [A contract's term](#a-contracts-term)
  - [What happens at the end](#what-happens-at-the-end)
  - [Changing the term](#changing-the-term)
  - [Fees with their own periods](#fees-with-their-own-periods)
  - [Payment terms tied to the contract](#payment-terms-tied-to-the-contract)
  - [The renewal flag](#the-renewal-flag)
  - [What customers see](#what-customers-see)
  - [What staff see](#what-staff-see)
- [Design details](#design-details)
  - [Validation](#validation)
  - [Billing's controllers](#billings-controllers)
  - [openmeter-provider](#openmeter-provider)
  - [Activity](#activity)
- [Alternatives considered](#alternatives-considered)
- [Rollout](#rollout)
- [Testing](#testing)
- [Open questions](#open-questions)
- [Implementation History](#implementation-history)

Tracked in [#292](https://github.com/milo-os/billing/issues/292), part of
[#282](https://github.com/milo-os/billing/issues/282). This builds on
[Contract Billing](contract-billing.md) and settles its
[End of Term](contract-billing.md#end-of-term).

## Summary

A contract has a start date but no end. Staff note the term in the contract's
fees, as `periods` on each recurring fee, and when the last period passes
the fee simply stops, whatever the customer agreed should happen next.

This enhancement gives a contract a term and a choice of what happens when
it ends:

- **Month to month** (the default): the contract carries on at the same
  prices, one month at a time, until someone decides otherwise.
- **Renew**: another term starts at the same prices, and again at the end of
  that one.
- **Stop**: the contract's charges stop, and the account is billed on an
  ordinary Offer from then on.

Billing flags the account for renewal 90 days before the term ends, so staff
can agree what comes next in time. Payment terms can be tied to the contract,
so they last as long as it does and end when it stops. Both portals show the
term and what happens at its end.

## What we have today

- `BillingEntitlement.spec.contract` has `startsAt` and `billingDay`. Nothing
  says how long the contract runs. `contract` is a struct so a term can join
  it.
- A recurring fee can run for a set number of periods
  (`servicePricingRefs[].periods`, [#246](https://github.com/milo-os/billing/pull/246)).
  openmeter-provider removes the fee from the subscription so it isn't
  charged after its last period
  ([openmeter-provider#111](https://github.com/milo-os/openmeter-provider/pull/111)),
  and both portals show "36 months, until 18 April 2029"
  ([staff-portal#826](https://github.com/datum-cloud/staff-portal/pull/826),
  [cloud-portal#1815](https://github.com/datum-cloud/cloud-portal/pull/1815)).
  Staff use this for the term today, so at the end of a three-year contract
  the server fees stop while the servers keep running.
- Payment terms (`BillingArrangement`) have an optional `endsAt`. Staff
  granting terms for a contract set it to the contract's end, or leave it
  open. The fleet overview lists invoice terms ending within 30 days, and the
  cloud portal tells owners 30 days before their terms end, with "Apply to
  renew".
- Nothing sends an email when terms or contracts end.
- openmeter-provider never schedules a subscription ahead, because OpenMeter
  can't delete a scheduled subscription cleanly. It cancels subscriptions
  only when an entitlement or account is deleted, and always immediately.
  OpenMeter's cancel does accept a time, and a scheduled cancel can be
  undone.

## Goals

- Give a contract a term, counted in months from its start, and say what
  happens when it ends.
- Never stop charging for a contract without someone choosing to: a contract
  carries on month to month unless staff choose otherwise.
- Never charge in advance for a period after a contract stops.
- Flag contracts for renewal 90 days before their term ends, and keep
  flagging those running month to month.
- Let payment terms last as long as the contract they were granted for.
- Show the term, its end and what happens then to staff and customers.

## Non-goals

- Changing prices at renewal automatically. A renewal at new prices is an
  amendment, made by staff as today (see [Open questions](#open-questions)).
- Notice periods: the customer's deadline for giving notice before a term
  renews. The renewal flag gives staff 90 days; recording each contract's
  notice period is left for later.
- Emails about a term ending. None are sent for payment terms either.
- Customers renewing or stopping a contract themselves. Contracts are agreed
  with sales, and staff record what was agreed.
- Turning anything off when a contract stops. Stopping ends the contract's
  charges; returning hardware and suspending projects are separate.
- A second contract on the same account after one stops (see
  [Open questions](#open-questions)).

## Design

### A contract's term

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
    startsAt: "2026-04-18T00:00:00Z"
    term:
      months: 36
      atEnd: MonthToMonth
```

`contract.term` is optional. A contract without one runs until it's changed,
as every contract does today.

- `months` is the term's length, from 1 to 120. Quotes give the term in
  months, so it carries over unchanged.
- The term is counted the way a fee's `periods` are, so its end is always a
  billing date: from `startsAt` for a contract billed on its start date, or
  from its first billing date for one billed on the invoice day, so the
  prorated first period isn't counted. A 36-month term starting on 18 April
  2026 ends on 18 April 2029. Billing works it out with the same function as
  a fee's end (`validation.ContractFeeEndsAt`).
- `atEnd` is what happens when the term ends: `MonthToMonth` (the default),
  `Renew` or `Stop`.

### What happens at the end

**Month to month.** Nothing changes in what's charged. The subscription keeps
running with the same fees and usage rates, and the contract is now out of
term: it stays flagged for renewal until staff agree a new term or stop it.

To stop a contract that's already month to month, staff set `atEnd: Stop`
and `stopsAt`, the date it stops, since the term's end has passed:

```yaml
term:
  months: 36
  atEnd: Stop
  stopsAt: "2029-08-18T00:00:00Z"
  offerAfterRef:
    name: payg-v6
```

- `stopsAt` must be a billing date on or after the contract's next one, so
  the month already billed is billed in full.
- It must also end the period of every quarterly and annual fee billed in
  advance, counted as the fee's periods are. Those fees keep renewing for a
  whole period while the contract runs month to month, and a stop partway
  through one would leave the customer having paid for time after it. A
  contract with an annual rack fee billed in advance on 18 April can stop on
  18 April only, not at the end of any month.
- Quarterly and annual fees billed in arrears don't hold the stop back: they
  are charged for the part period up to it, by time, as they are when a
  contract is amended.
- The staff portal offers the dates that are allowed: the next billing date
  for a contract with only monthly fees, or the next date each in-advance
  fee's period ends ("18 April 2030, when the annual rack fee's year ends").
- `stopsAt` is only for a contract that's month to month. A contract in its
  term stops at the term's end, and stopping one before then is ending it
  early, which this design leaves out.

So a contract set to stop has one **stop date**: its term's end, or `stopsAt`
once it's month to month. Everything that acts on a stop below uses that
date.

**Renew.** A new term starts on the old one's end, at the same prices. Nothing
changes in what's charged either; what changes is the date the term ends, and
so the date the renewal flag comes back. `renewMonths` sets the length of each
renewal, defaulting to `months`, since a three-year contract often renews a
year at a time:

```yaml
term:
  months: 36
  atEnd: Renew
  renewMonths: 12
```

Renewals are counted from the contract's start, not chained from one
another, so the ends stay on the contract's billing day: 18 April 2029, then
18 April 2030, and so on. Billing doesn't store which term the contract is
in; it works it out from `startsAt`, `months`, `renewMonths` and the time.

**Stop.** The contract's charges stop at the end of the term (or on `stopsAt`
for a contract already month to month), and the account moves onto
`offerAfterRef`, an ordinary Offer, from that moment:

```yaml
term:
  months: 36
  atEnd: Stop
  offerAfterRef:
    name: payg-v6
```

- The staff portal fills `offerAfterRef` with the default Offer, and staff
  can pick another. It's required with `Stop` and not allowed otherwise.
- At the end, billing's BillingEntitlement controller sets `offerRef` to
  `offerAfterRef`. Everything that reads `offerRef` (the account's prices
  and discounts, service entitlements, the portals) then follows the Offer
  the account is actually billed on.
- openmeter-provider cancels the contract's subscription at the end and
  subscribes the account to `offerAfterRef`, billed on the account's invoice
  day like any account without a contract (see
  [openmeter-provider](#openmeter-provider)).
- Fees billed in advance are never charged past the end: the term's end is a
  whole number of every recurring fee's periods, and `stopsAt` ends every
  in-advance fee's period (see [Validation](#validation)), so each fee's last
  charge covers the period ending on the stop date.
- The contract stays on the entitlement, ended. It can't be removed, as
  today, and its term can't change any more.
- Usage up to the end is billed at the contract's rates on the contract's
  last invoice, and usage after it on the new Offer.

### Changing the term

Staff can change the term until the contract stops: when the customer gives
notice, agrees to renew early, or signs a new term while month to month.

- `atEnd`, `stopsAt` and `offerAfterRef` can change at any time before the
  stop date. If the provider has already scheduled the stop, it undoes it.
- `months` and `renewMonths` can change too, so long as the current term
  still ends on or after the contract's next billing date. A term can't be
  shortened into the month already billed.
- Renewals are worked out from the start, so changing `renewMonths` on a
  contract that has already renewed changes the past terms' ends too, and
  with them `currentTermStartsAt` and the count of renewals. Nothing already
  billed changes, and the next-billing-date rule keeps the current term from
  ending in a month that's billed. The staff portal shows the new current
  term's end before saving.
- A term can be added to a contract that's already running, which is how the
  contracts running today get one. It can be removed, which makes the
  contract open-ended again, until the contract stops.
- A new term agreed while month to month is a longer `months`. The staff
  portal asks for the new term's length and works out `months` so it starts
  on the next billing date: a contract that ran 36 months and then two month
  to month, renewing for a year, has `months: 50`. This keeps the end
  something billing can recompute from the contract alone.

### Fees with their own periods

[Contract Billing](contract-billing.md#end-of-term) left two questions about
a fee's `periods` for this design.

**May a fee run past the contract's end?** Yes, while the contract carries on.
A contract that renews yearly can still have a server lease that runs for
three years. With `Stop`, no: a fee can't end after the contract does, since
everything stops at the end. Billing refuses a contract whose term stops
before one of its fees ends.

**Does a fee that ends carry on month to month like the contract?** No. A
fee's `periods` is its own end, and it stops then, whatever happens to the
contract. Fees that should run for as long as the contract does leave
`periods` out: the term decides when they end. `periods` is for what's
shorter than the contract, such as a first year of premium support, or
hardware paid off in instalments.

So the staff portal changes its "Renewals" help text to say that an empty
value means "for as long as the contract runs", and when staff give a
contract a term, it offers to clear `periods` on fees whose end is the term's
end. Clearing it is an amendment that only changes when a fee ends, so
nothing is charged twice.

### Payment terms tied to the contract

Payment terms granted for a contract should last as long as it does. Today
staff set the terms' `endsAt` to the contract's end, and if the contract
carries on month to month the account loses its invoice terms while it's
still being billed under the contract.

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: BillingArrangement
spec:
  billingAccountRef:
    name: acme
  type: Invoice
  invoice:
    netDays: 30
    purchaseOrderNumber: PO-4471
  endsWithContract: true
  reason: Contract signed 2026-03-02
```

- `endsWithContract: true` ends the terms when the account's contract stops,
  at the same moment. While the contract runs, in its term, renewed or month
  to month, the terms stay in effect.
- Staff can still end the terms early with `endsAt`, as today. The terms end
  at whichever comes first.
- It needs the account to have a contract. It can be set or cleared at any
  time, so staff can tie the terms granted for contracts running today.
- `status.paymentArrangement` gains `endsWithContract`. Its `endsAt` is the
  contract's end once the contract is set to stop, and empty while it will
  carry on, so the cloud portal's 30-day notice and the fleet overview's
  "terms ending" item work unchanged.
- Terms with no end can't be renewed by application today. Tied terms count
  as having no end until the contract is set to stop.
- For overlap checks, tied terms run until the contract stops, so new terms
  can't be scheduled after them until it's set to stop.

When the contract stops and its terms end, the account needs a card or new
terms, as when any terms end: `PaymentReady` turns `False` with reason
`ArrangementEnded`, and running workloads aren't stopped. openmeter-provider
subscribes an account only once it can pay, so it won't subscribe the
account to `offerAfterRef` until then, and usage in between isn't billed. The
staff portal warns about this when staff set a contract to stop and the
account has no card.

### The renewal flag

A new BillingAccount condition, `ContractRenewalDue`, tells staff a contract
needs a decision. Only accounts whose contract has a term have it, so the
conditions of every other account are unchanged:

| Status | Reason | When |
|---|---|---|
| `True` | `TermEndingSoon` | The current term ends within 90 days, whatever `atEnd` says |
| `True` | `MonthToMonth` | The term has ended and the contract carries on month to month |
| `False` | `InTerm` | The current term ends more than 90 days from now |
| `False` | `Stopped` | The contract has stopped |
| `True` | `StopBlocked` | The contract is set to stop, but the account can't move onto `offerAfterRef`: checked from 90 days before the stop, and after it until billing has moved the account |

- It's set by billing's BillingAccount controller, which already works out
  `status.contract`, and which requeues itself for the moment the flag or the
  term changes.
- It clears itself when staff act: a longer term moves the end more than 90
  days out, and a stop clears it at the end. A contract that is set to stop
  stays flagged until it does, since staff may still keep the customer.
- A renewing contract is flagged before each renewal, so staff can revisit
  prices before another term starts.
- 90 days is a constant in billing, shared by every contract.

This is the same shape as the flow for payment terms ending, made longer
because a contract takes longer to renegotiate: a condition and a date on the
account, an attention item in the fleet overview, and a notice to customers.
Staff see it first; customers see the end of their term all along, and a
notice from 90 days.

### What customers see

`BillingAccount.status.contract` gains the term:

```yaml
status:
  contract:
    startsAt: "2026-04-18T00:00:00Z"
    billingDayOfMonth: 18
    billingDay: StartDate
    term:
      months: 36
      atEnd: Renew
      renewMonths: 12
      phase: InTerm
      currentTermStartsAt: "2029-04-18T00:00:00Z"
      endsAt: "2030-04-18T00:00:00Z"
      renewals: 1
      renewalDueAt: "2030-01-18T00:00:00Z"
    fees: [...]
```

- `phase` is `InTerm`, `MonthToMonth` or `Stopped`.
- `currentTermStartsAt` and `endsAt` are the current term's. Month to month,
  `endsAt` is when the last term ended. Stopped, it's when the contract
  stopped.
- `renewals` counts the renewals so far, for `Renew` only.
- `renewalDueAt` is 90 days before `endsAt`, when the flag turns on.
- `stopsAt` is the stop date, for a contract set to stop: the term's end, or
  the spec's `stopsAt` once it's month to month.
- `offerAfterRef` isn't published: what the account moves to is shown by its
  prices once it has moved.
- A stopped contract keeps `startsAt`, its billing day and its term, and has
  no fees, since the account's Offer is no longer the contract's. The cloud
  portal says "Your contract ended on 18 April 2029".
- Once the contract stops, billing cycles go back to the account's invoice
  day, in the portals and in the usage tools of Patch and `datumctl billing`
  (`usagereport.CycleTerms`).

In the cloud portal, the contract card
(`app/features/billing/cards/contract-card.tsx`) shows the term and what
happens at its end:

- "36-month term, until 18 April 2029. Then it carries on month to month."
- "36-month term, until 18 April 2029. Then it renews for 12 months at a
  time."
- "36-month term, until 18 April 2029. Your contract ends then."
- Month to month: "Your 36-month term ended on 18 April 2029. Your contract
  carries on month to month."

From `renewalDueAt`, the card's footer adds a notice in the same style as the
payment terms card's: "Your contract's term ends on 18 April 2029. Contact
your account manager to talk about what happens next." There's no apply
button: renewals are agreed with sales.

Tied payment terms show "Valid while your contract runs" on the payment terms
card in place of an end date, or the contract's end once it's set to stop.

### What staff see

In the staff portal:

- **Contract editor** (`components/contract-editor/`): a Term section with
  the length in months, what happens at the end (three choices, month to
  month first), the renewal length for Renew and the Offer to move to for
  Stop, defaulted to the default Offer. For a contract that's month to
  month, Stop also asks for the stop date, from the dates allowed. The
  summary shows the term's end. A
  contract that's running can have its term added or changed from the same
  page, without changing its fees.
- **Contract card** on the account dashboard: the term, its end, the choice,
  and how many times it has renewed. A badge when `ContractRenewalDue` is
  `True`: "Renewal due 18 April 2029", or "Month to month since 18 April
  2029".
- **Attention banner** (`lib/account-health.ts`): a `contractRenewal` check
  from the condition, beside the card expiry and credit limit checks.
- **Payment terms dialog**: "Ends with the contract" as an alternative to an
  end date, offered when the account has a contract. Accounts whose terms end
  on the day their contract's term does are offered the switch.
- **Fleet overview** (billing's `ui/provider`): two attention items beside
  "invoice arrangements ending within 30 days": "N contracts up for renewal
  within 90 days" and "N contracts running month to month", each listing the
  accounts.

## Design details

### Validation

On `BillingEntitlement`, by CRD schema and CEL:

- `term.months` and `term.renewMonths` are whole numbers from 1 to 120.
- `term.atEnd` is an enum of `MonthToMonth`, `Renew` and `Stop`, defaulting
  to `MonthToMonth`.
- `renewMonths` only with `Renew`; `offerAfterRef` required with `Stop` and
  not allowed otherwise; `stopsAt` only with `Stop`, at midnight UTC.

By the webhook, when the term or `offerRef` changes:

- `months`, and `renewMonths` with `Renew`, must be a whole number of every
  recurring fee's interval in the Offer: a multiple of 3 with a quarterly fee,
  of 12 with an annual one. Otherwise the term would end partway through a
  period the customer had paid for in advance, or an annual fee would renew
  on a different day from the contract.
- With `Stop`, no fee in the Offer may end after the term does.
- The current term must end on or after the contract's next billing date
  (the start, before the contract starts).
- `stopsAt` is only allowed once the term has ended, and is then required
  with `Stop`. It must be a billing date on or after the contract's next one,
  and a whole number of periods, counted as fees' periods are, of every
  quarterly or annual fee billed in advance.
- `offerAfterRef` must name an assignable Offer with no `OneTime` fees.
- Once the contract has stopped (`atEnd` is `Stop` and the stop date has
  passed), the term can't change, except `offerAfterRef` until billing has
  moved the account onto it (see [A stop billing can't make](#billings-controllers)).
  Until then `offerRef` can only change to `offerAfterRef`, so an Offer
  picked by hand isn't overwritten when billing moves the account.

The Offer webhook can't see the contract, so an amendment that adds an annual
fee to a contract whose term isn't a multiple of 12 is refused when
`offerRef` is pointed at it, not when the Offer is created.

On `BillingArrangement`, the webhook checks that `endsWithContract` is only
set on an account whose entitlement has a contract.

### Billing's controllers

- **BillingAccount controller.** Works out `status.contract.term` and the
  `ContractRenewalDue` condition from the entitlement and the time, and
  requeues at the next of `renewalDueAt`, `endsAt` and `stopsAt`. It records the
  transitions as events (see [Activity](#activity)), once each, from the
  change in `status.contract.term`.
- **BillingEntitlement controller.** On the stop date, sets `spec.offerRef`
  to `offerAfterRef`. It's the only place billing writes an entitlement's
  spec, and the audit log records it as billing's service account. It
  requeues itself for the stop date.
- **A stop billing can't make.** `offerAfterRef` is checked when the term is
  set to stop, which may be years before the stop, and the Offer may be
  retired or gain a one-time fee in between. Then the webhook refuses the
  controller's change. So the BillingAccount controller checks the Offer
  hourly from 90 days before the stop, and sets `ContractRenewalDue` to
  `StopBlocked` with the reason while it can't be used. If the stop comes and
  the change is refused, the BillingEntitlement controller records a
  `ContractStopBlocked` warning on the account and tries again hourly. Until
  the account moves, it's still billed the contract's fees, and
  `status.contract` keeps showing them. Staff fix it by pointing
  `offerAfterRef` at another Offer, which stays editable until the move.
- **BillingArrangement controller.** An arrangement's phase now depends on
  the contract when `endsWithContract` is set: `Ended` from the contract's
  stop. It records `PaymentTermsEnded` then, as for an arrangement that
  reaches its own `endsAt`, and watches BillingEntitlements so a change to
  the term is picked up.
- **usagereport.** `CycleTerms` follows the invoice day again once a
  contract has stopped.

### openmeter-provider

**Month to month and Renew** need nothing. The subscription carries on with
the same items, and a fee with `periods` still ends at its own end, as today.

**Stop** follows the pattern openmeter-provider already uses to end a fee: act
during the last monthly billing period, so nothing is charged in advance after
the end, and nothing is scheduled earlier than it has to be.

1. The provider works out the stop date as billing does: the term's end, or
   `stopsAt` for a contract already month to month. When `atEnd` is `Stop`
   and the current monthly billing period ends on the stop date, it cancels
   the subscription timed at that date. For a contract month to month, that
   is as soon as `stopsAt` is set, if it's the next billing date. It records
   the time on the entitlement (`openmeter.miloapis.com/contractStopAt`) and
   reports `ContractStopScheduled`.
2. If staff change `atEnd`, `stopsAt` or the term before the stop date, the
   provider undoes the scheduled cancel, as it already does before
   cancelling again, and removes the annotation.
3. On the stop date, it subscribes the account to `offerAfterRef` from that
   moment, billed on the account's invoice day like any new subscription,
   once the account can pay, and doesn't wait for billing to change
   `offerRef`. When `offerRef` then names
   the same Offer, there's nothing left to do. It doesn't treat that
   `offerRef` change as an amendment.
4. It records that the contract has stopped
   (`openmeter.miloapis.com/contractStoppedAt`), so later Offer changes are
   handled as for an account without a contract.

Cancelling and subscribing again is a plan change done in two steps. Doing it
as a plan change timed at the end, the way the provider moves an account onto
a contract, would mean nothing cancels the subscription if the provider is
late, and OpenMeter would invoice the next month's in-advance fees at the
boundary. With the cancel scheduled a month ahead, OpenMeter itself stops
charging at the end, and a late provider means the account is briefly without
a subscription instead:

- OpenMeter refuses to start a subscription more than about two minutes in
  the past, so if the provider is late (down over the boundary, say), the new
  subscription starts when it gets there, and usage between the end and then
  isn't billed. The provider reports `ContractStoppedLate` with the gap, for
  staff to decide whether to charge it.
- If the provider misses the last month entirely and the end passes with the
  subscription still running, it cancels straight away and reports
  `ContractStoppedLate` with the period charged after the end, for staff to
  credit, as it does for a fee that ends late.

The last invoice comes from the cancelled subscription at the end: usage and
fees in arrears for the last month, as usual. One-time fees aren't charged
again, since `offerAfterRef` can't have any.

Contract amendments are made by changing plan at the end of the billing
period today, with in-place subscription edits only for ending fees. Neither
affects renewals: Month to month and Renew don't change the subscription, and
Stop ends it rather than amending it. The provider holds an amendment for a
period that ends on or after the stop, since there's nothing left to amend.

To be checked against a running OpenMeter (beta.236) before relying on it:

- that a cancel timed at a future billing date issues the last invoice as
  usual and charges nothing in advance for the period after it;
- that undoing a scheduled cancel leaves the subscription billing as before;
- that a new subscription for the same customer can start at the moment the
  old one is cancelled, with no overlap in usage.

### Activity

Recorded on the BillingAccount by billing:

| Reason | Scope | Summary |
|---|---|---|
| `ContractRenewalDue` | Staff only | The contract's term ends on {date}, and it will {carry on month to month / renew for N months / stop} |
| `ContractTermEnded` | Organization | The contract's term ended, and it carries on month to month |
| `ContractRenewed` | Organization | The contract renewed for N months, until {date} |
| `ContractStopped` | Organization | The contract ended on {date} |

And `ContractStopBlocked` (staff only, a warning) when billing can't move
the account onto `offerAfterRef` at the stop.

Recorded on the BillingEntitlement by openmeter-provider, staff only:
`ContractStopScheduled`, and `ContractStoppedLate` as a warning.

Changes staff make to the term are recorded from the audit log, as for other
entitlement changes: "Sam set the contract to stop at the end of its term".

## Alternatives considered

**An end date on the contract.** `contract.endsAt` would say the same thing
as a length, but staff could set a date that isn't a billing date, and the
last period would be a part one. Quotes give the term in months, and fees
already count periods, so the term does too.

**The term as a fee's `periods`.** That's what staff do today, and it's why a
contract's fees stop at the end of the term without anyone deciding. A term
belongs to the contract; a fee's periods are for fees that end sooner.

**Stopping by default.** It's simpler, since nothing carries on past the end,
but it stops charging for hardware that's still running without anyone
deciding to. Billing tools that support contracts default to carrying on or
renewing, and bare metal and colocation contracts usually do the same.

**Leaving `offerRef` on the contract's Offer after it stops.** The provider
would bill `offerAfterRef`, but everything that reads `offerRef` (the
account's prices and discounts, service entitlements, both portals) would
show the contract's prices after they stopped applying.

**Removing the contract when it stops.** The entitlement would look like any
account's, but the contract's start, term and end would disappear with it,
and the CEL rule that keeps a contract from being removed exists so that a
started contract isn't undone by mistake.

**Recording each renewal.** Billing could write the current term to the
entitlement's status at each renewal. Working it out from the start and the
lengths is simpler, can't drift, and is how fee ends already work.

**Renewing through `BillingArrangementApplication`.** Customers renew payment
terms by applying again. Contracts are agreed with sales, outside the portal,
so staff record the outcome on the contract instead.

## Rollout

1. **billing**: `contract.term`, `endsWithContract`, `status.contract.term`,
   `status.paymentArrangement.endsWithContract`, the `ContractRenewalDue`
   condition, the controllers, the activity policies and `usagereport`, with
   the staff guide updated. Release. Month to month and Renew work from this
   release, since the provider has nothing to do for them. Setting a term to
   `Stop` is refused while the `ContractStop` feature gate is off, its
   default, so nothing can stop a contract before the provider handles it.
2. **openmeter-provider**: Stop, with its e2e and time-travel tests, on the
   billing release. Release, and pin it in production. Then turn on
   `ContractStop` in billing.
3. **Fleet overview** (billing `ui/provider`): the two attention items. Can
   ship with step 1.
4. **staff-portal**: the term in the contract editor and card, the attention
   check, tied payment terms and the "Renewals" help text. Stop is offered
   only once the openmeter-provider release from step 2 is running, as
   `billingTiming` was: before then, billing would move the account onto
   `offerAfterRef` at the end, and the provider would treat it as an
   amendment, charging the contract's in-advance fees for another month.
5. **cloud-portal**: the term and notice on the contract card, and tied
   payment terms on the payment terms card, after the client is regenerated
   from staging.
6. **Contracts running today.** Staff give each one its term and choice in
   the staff portal, clear `periods` on fees whose end is the term's end, and
   tie its payment terms. A `datumctl` query lists contracts without a term,
   and fees whose end matches a likely term, to work through. Until they do,
   those fees stop at their end, as they would today.

No new roles: the term is part of the entitlement, which `Billing
Entitlement Admin` already edits, and tied terms part of the arrangement.
Infra needs only the usual version pins.

## Testing

- **Unit (billing)**: term ends for both billing days, month-end clamping
  (a contract starting on the 31st), renewals counted from the start, the
  phase and flag at each boundary with a fixed clock, validation tables, and
  an arrangement's phase with a tied contract.
- **envtest (billing)**: the BillingAccount controller requeues for
  `renewalDueAt` and `endsAt` and records each event once; the
  BillingEntitlement controller moves `offerRef` at a stop and not before;
  tied terms end with the contract.
- **Chainsaw e2e (billing)**: the webhook refusals, and `status.contract.term`
  for each choice.
- **openmeter-provider e2e against OpenMeter**, using the time-travel suite
  (`test/timetravel/contract`): a contract with monthly in-advance, monthly
  in-arrears and annual fees set to Stop, checking the last invoice, nothing
  charged after the end, and the new subscription's first invoice; Stop
  changed back to month to month after the cancel is scheduled; a contract
  month to month with an annual in-advance fee stopped on `stopsAt`, at the
  end of the fee's year; a provider
  stopped over the boundary; Month to month and Renew billing on past the end
  unchanged.
- **Staging**: a test contract with a one-month term set to Stop, watched
  through its end and its last invoice, before Stop is offered in production.

## Open questions

1. **Renewing at new prices.** A renewal often comes with a price change.
   Amendments apply when they're made, except to fees billed in advance,
   which change from the next billing date, so staff can make the amendment
   in the term's last month and have in-advance fees change on the renewal
   date. Usage rates and fees in arrears would change on the day it's made.
   Is that enough, or do we need amendments scheduled for a date?
2. **Notice periods.** Should a contract record the customer's notice period,
   and flag the account that long before the end instead of 90 days?
3. **Telling customers.** Should owners get an email 90 and 30 days before the
   term ends, and when it ends? There's no email for payment terms ending
   either; both could come together.
4. **Month to month for good.** A contract month to month stays flagged until
   staff agree a new term or stop it. Do staff need a way to mark it as
   reviewed, so the fleet overview only shows the ones that need action?
5. **Another contract after a stop.** A stopped contract stays on the
   entitlement, and a contract can't be replaced. Bringing a customer back
   onto a new contract would need either replacing a stopped contract, or a
   history of contracts. Not needed yet.
6. **Gaps after a late stop.** Should the provider charge the usage between
   the end and a late new subscription to the old subscription, if OpenMeter
   allows it, rather than reporting the gap?

## Implementation History

- 2026-11-25: Enhancement drafted.
- 2026-11-25: A contract that's month to month stops on `stopsAt`, a billing
  date that ends every quarterly and annual in-advance fee's period, rather
  than at the next billing date.
- 2026-11-26: Design approved
  ([#296](https://github.com/milo-os/billing/pull/296)). Billing's term,
  renewal flag, stop and activity built behind the `ContractStop` feature
  gate; payment terms tied to the contract follow separately.
