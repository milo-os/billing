---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# What Happens When a Customer Doesn't Pay

- [Summary](#summary)
- [The Problem](#the-problem)
- [Principles](#principles)
- [How It Works for Customers](#how-it-works-for-customers)
  - [Paying by Card](#paying-by-card)
  - [Paying by Invoice](#paying-by-invoice)
  - [Paying Up](#paying-up)
- [Scenarios](#scenarios)
- [What Staff Can Do](#what-staff-can-do)
- [What Customers See](#what-customers-see)
- [Before We Switch It On](#before-we-switch-it-on)
- [Questions for the Team](#questions-for-the-team)
- [Options We Ruled Out](#options-we-ruled-out)
- [Appendix: How It Works](#appendix-how-it-works)
- [Implementation History](#implementation-history)

## Summary

Today nothing happens when a customer stops paying. Their projects keep
running, they can keep creating more, and the bill keeps growing.

This proposes a fair, predictable process in three steps:

1. **Reminders.** We tell the customer, and staff can see it. Nothing else
   changes.
2. **Paused growth.** The customer can't create anything new, but everything
   they already run keeps working.
3. **Paused service.** Their projects are paused. Nothing is deleted, and it
   all comes back when they pay.

Customers paying by card go through these steps automatically. Customers on
invoice terms, including bare metal contracts, get reminders automatically,
but a person decides anything beyond that. Paying always restores everything
straight away, and nothing is ever deleted without a person deciding to.

## The Problem

**Card customers.** When a card payment fails, Stripe retries it and emails
the customer. If it never succeeds, nothing else happens: the customer keeps
everything, and can keep adding more. This is where runaway bills and fraud
cost us most.

**Invoice customers.** Customers on invoice terms are businesses we've
approved, often under a signed agreement, who pay by bank transfer. Late
payment is usually an accounts payable delay or a missing PO, not bad intent.
Switching off a bare metal customer's servers over a late transfer would do
far more damage than the invoice is worth. Today, though, staff don't even
hear that an invoice is late.

## Principles

- **Predictable.** Customers know what will happen and when, before it
  happens.
- **Proportionate.** Stop the bill growing before stopping anything running.
- **Reversible.** Paying restores everything immediately, without a support
  ticket.
- **A person decides the serious things.** Nothing is deleted automatically,
  and invoice customers are never paused without a person deciding to.
- **Staff can step in.** Staff can pause the process for a customer who's
  disputing an invoice or has promised payment.

## How It Works for Customers

### Paying by Card

Counted from the day their oldest unpaid invoice was due:

| When | What happens |
|---|---|
| **Day 0** | We email them. A banner in the cloud portal asks them to update their card. Stripe keeps retrying. |
| **Day 7** | **Paused growth.** They can't create new projects or resources, or scale up. Everything running carries on. We email to say so, and to say when their service will be paused. |
| **Day 21** | **Paused service.** Their projects are paused: workloads stop, nothing can be changed, and their data and settings are kept. We email them, and support is told. |

A card that fails once and then goes through never gets past the first email.

### Paying by Invoice

Counted from the invoice's due date, which is after their payment terms (net
30, say):

| When | What happens |
|---|---|
| **Day 0** | We remind their accounts payable contact. |
| **Day 7** | Another reminder. The account is flagged for staff: "Invoice INV-123 is 7 days overdue." |
| **Day 14** | Flagged as needing a decision. |

From then on, staff decide what to do: chase the customer, give them more
time, end their invoice terms (so they pay by card from then on), or pause
growth or service.

### Paying Up

When everything overdue is paid, the account is back in good standing
straight away. Paused growth is lifted, paused projects start again, and we
send a confirmation. There's no need to contact support.

## Scenarios

**A startup's card expires.** Their invoice fails on the 1st. They get an
email and see a banner, and update the card on the 3rd. Stripe's next retry
succeeds and nothing else happens.

**A card customer disappears.** The invoice fails and nobody responds. On day
7 they can't create anything new. On day 21 their projects are paused, and
support is told. If they come back and pay in month three, everything starts
again. If they never come back, staff decide whether to delete their projects.

**A bare metal customer's payment is late.** Their net 30 invoice passes its
due date because the PO wasn't raised. Their AP contact gets reminders, and on
day 7 staff see the flag. Staff call the customer, who promises to pay next
week, so staff put the account on hold until then. Nothing is paused, and the
servers keep running.

**A customer disputes an invoice.** Staff put the account on hold while it's
sorted out. The reminders stop moving it forward, and nothing is paused.

**An invoice customer stops paying altogether.** After a decision on day 14,
staff end their invoice terms and pause growth. If it still isn't paid, staff
pause service, and then decide about deletion under the contract.

## What Staff Can Do

On the billing account in the staff portal:

- See where the account stands: good standing, overdue, growth paused or
  service paused. See how long it's been, and what happens next and when.
- Pause growth or service by hand, with a reason.
- Lift a pause by hand, for example when a payment is on its way.
- Put the account on hold until a date, so it doesn't move to the next step.
- See a list of invoice customers waiting for a decision.

Every change is recorded on the account's activity timeline, with who made
it and why.

## What Customers See

- **Emails** at each step, naming the invoice, the amount and what happens
  next, with a link to pay. Stripe's own invoice reminders continue too.
- **A banner in the cloud portal** while they're overdue, with a link to pay
  the invoice.
- **A clear message** when they try to create something while growth is
  paused, explaining why and how to fix it.

## Before We Switch It On

**Milo deletes paused projects after 30 days.** Milo already has project
suspension, which this builds on. But today any project left suspended for 30
days is deleted automatically, whatever the reason. That would mean deleting
a customer's projects a month after pausing them for non-payment, with
nobody deciding to. Milo needs to stop doing that for billing before any of
this is switched on.

**Watch before acting.** The first release only marks accounts as overdue and
sends reminders, so we can check who would be paused, and when, before
anything actually is.

## Questions for the Team

1. **Are the timings right?** Day 7 and day 21 for card customers, and day 7
   and day 14 to flag invoice customers.
2. **When does a card customer's clock start?** From when the payment first
   fails, or from the invoice's due date? Today a card invoice's due date may
   be 30 days after it's issued.
3. **How do we pause growth?** Milo can fully pause a project today, but it
   can't yet just stop new resources. We need to choose how to build that.
4. **Accounts used by several teams.** Pausing an account pauses every
   project it pays for, which might belong to different teams.
5. **Who gets the emails?** Just the billing contact, or project owners too?
6. **Free and credit-funded accounts.** These shouldn't ever be affected;
   we should confirm.

## Options We Ruled Out

**Skip paused growth and go straight to pausing service.** It's simpler, but
the first thing a card customer would notice is their service stopping.

**Have staff decide for every customer.** It's safer, but most customers pay
by card, and staff would spend their time chasing failed cards.

**Act as soon as a card fails.** Customers often fix a card within Stripe's
retries, so waiting a few days is fairer.

**Build a separate pause in billing.** Milo's project suspension already
pauses every service cleanly and restores it, so we reuse it.

## Appendix: How It Works

For engineers; the proposal above doesn't depend on these details. The
full technical design is in
[nonpayment-suspension/architecture.md](./nonpayment-suspension/architecture.md).

- **Knowing an account is overdue.** Billing already marks invoices `PastDue`
  after their due date, and sets `InvoicingReady=False` on the account. The
  clock counts from the due date of the oldest invoice still overdue. Void
  invoices, and old ones nothing updates any more, don't count.
- **Recording where an account stands.** A new `PaymentStanding` condition on
  `BillingAccount`, with the reasons `Good`, `Overdue`, `Restricted`,
  `Suspended` and `OnHold`. The status also records the date of the next step.
  While service is paused, the account's phase is `Suspended`, so no new
  projects can be bound to it.
- **Pausing service.** Billing creates a Milo `ProjectSuspension` for each
  project bound to the account, with reason `Billing`, `reinstateAuthority:
  Operator` and the account as a label. Milo then blocks writes, and each
  service pauses its workloads. Lifting the pause deletes those suspensions.
- **Pausing growth.** This is still to be decided (question 3). The options are
  capping the account's projects' quota at what they use now, or a mode of
  Milo's suspension that only blocks creates and scale-ups. Billing also stops
  new projects being bound to the account.
- **Staff actions.** These are recorded on a staff-only resource in the
  account's namespace (pause, lift, hold), since customers can edit their own
  `BillingAccount`. A new staff role creates them.
- **Settings.** These go in the billing operator config: on or off (off at
  first), and the timings for card and invoice customers.
- **Emails.** These go through Milo's notification emails, with templates in
  `datum-cloud/email-templates`, as the payment terms emails do.
- **Deletion.** Milo's escalation controller needs to exempt `Billing`
  suspensions from automatic deletion after the retention window
  (`--project-suspension-retention-window-days`, 30 by default).

## Implementation History

- 2026-11-03: Enhancement drafted.
- 2026-11-03: Rewritten around customers, staff and scenarios. Technical
  detail moved to the appendix.
- 2026-11-05: Architecture added in
  [nonpayment-suspension/architecture.md](./nonpayment-suspension/architecture.md).
