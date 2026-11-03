---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Restricting and Suspending Accounts for Non-Payment

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Stages](#stages)
  - [Card Accounts](#card-accounts)
  - [Accounts on Invoice Terms](#accounts-on-invoice-terms)
  - [Getting Back in Good Standing](#getting-back-in-good-standing)
  - [Holds](#holds)
- [Design Details](#design-details)
  - [When an Account Is Overdue](#when-an-account-is-overdue)
  - [Payment Standing](#payment-standing)
  - [Restricting](#restricting)
  - [Suspending](#suspending)
  - [Deletion](#deletion)
  - [Configuration](#configuration)
  - [Notifications](#notifications)
  - [Staff Portal and Cloud Portal](#staff-portal-and-cloud-portal)
  - [RBAC](#rbac)
  - [Activity](#activity)
- [Open Questions](#open-questions)
- [Alternatives](#alternatives)
- [Implementation History](#implementation-history)

## Summary

Billing knows when an account stops paying: its invoices go `PastDue`
(`InvoicingReady=False`) and a failing card degrades `PaymentReady`. Nothing
acts on it. An account can owe money indefinitely and keep running, and keep
adding, resources.

This enhancement adds a policy for non-payment, in stages:

1. **Notify**: the customer and staff are told, and nothing changes.
2. **Restrict**: no new resources, while running ones carry on.
3. **Suspend**: the account's projects are suspended with Milo's existing
   `ProjectSuspension` (reason `Billing`), which pauses workloads and blocks
   writes, keeping data and configuration.

Card accounts move through the stages automatically. Accounts on invoice terms
(including contracts) are notified automatically, but staff decide whether to
restrict or suspend them. Paying lifts everything automatically. Nothing is
ever deleted for non-payment without a person deciding to.

## Motivation

Self-serve accounts pay by card. When a card fails, Stripe retries it and
emails the customer, but if it never succeeds the account keeps its projects,
its running workloads and its ability to create more. That is where runaway
bills and fraud cost the most.

Accounts on invoice terms are different. They are approved by staff, often
under a signed agreement, and pay by bank transfer, which is routinely late
for reasons that have nothing to do with intent (an accounts payable cycle, a
missing PO). Switching off a bare metal customer's hardware over a late wire
would cost far more than the invoice.

[Invoicing](./invoicing.md) left this as "a policy decision for a separate
enhancement", and [Billing Arrangements](./billing-arrangements.md) has staff
"end terms or suspend projects" for an account that doesn't pay, with no way
to do so. This is that enhancement.

### Goals

- Stop an unpaid card account's bill from growing, then pause it, without
  staff having to act.
- Tell staff when an account on invoice terms is late, and let them restrict
  or suspend it from the staff portal.
- Lift every restriction and suspension automatically when the account pays.
- Never delete a customer's projects for non-payment automatically.
- Build on Milo's project suspension rather than adding a second mechanism.

### Non-Goals

- Retrying failed card payments. Stripe's retries (Smart Retries) and its
  emails stay as they are.
- Collections, late fees and write-offs. Contract terms cover them.
- Suspending for fraud or abuse, which have their own reasons and owners.
- Credit limits. Spending past an invoice-terms account's limit is an alert
  for staff ([Billing Arrangements](./billing-arrangements.md)); it isn't
  non-payment.

## Proposal

### Stages

| Stage | What changes for the customer | Reversible |
|---|---|---|
| **Good standing** | Nothing. | — |
| **Overdue** | Emails and a banner in the cloud portal. Staff see it on the billing account. | — |
| **Restricted** | No new projects, and no new resources or scaling up in existing ones. Running resources carry on. | Yes, at once |
| **Suspended** | The account's projects are suspended: workloads paused, writes blocked, data and configuration kept. | Yes, as projects resume |

An account only moves forward one stage at a time, and only on time spent
overdue, so a single payment that fails and then succeeds never restricts
anything.

### Card Accounts

For an account paying by card (no active invoice terms), counting from the
due date of its oldest unpaid invoice:

| Day | Stage |
|---|---|
| 0 | **Overdue.** Stripe has been retrying the card. The customer is emailed and sees a banner. |
| 7 | **Restricted** automatically. |
| 21 | **Suspended** automatically. |

The days are defaults (see [Configuration](#configuration)).

### Accounts on Invoice Terms

For an account with an active Invoice `BillingArrangement`, counting from the
due date (net terms) of its oldest unpaid invoice:

| Day | What happens |
|---|---|
| 0 | **Overdue.** The customer's accounts payable contact is reminded. |
| 7 | Reminder again, and the account is flagged for staff: "Invoice INV-123 is 7 days overdue". |
| 14 | Flagged as needing a decision. |

Billing never restricts or suspends an account on invoice terms by itself.
Staff can, at any point after it's overdue, from the staff portal; they can
also end the account's terms, which makes it a card account from then on.

### Getting Back in Good Standing

When every overdue invoice is paid (or voided), the account returns to good
standing straight away: restrictions are lifted, and billing lifts the
project suspensions it created. Projects resume as Milo reinstates them.

Staff can also lift a restriction or suspension by hand, for example when a
payment is on its way.

### Holds

Staff can put an account **on hold** for a reason and until a date, for a
dispute or a promised payment. While on hold, the account is still shown as
overdue, but it doesn't move to a later stage, and nothing billing restricted
or suspended is reapplied. The hold ends on its date, or when staff remove
it.

## Design Details

### When an Account Is Overdue

An invoice is overdue once it's `PastDue`, which providers set after its due
date (`Invoice.status.dueDate`). The account's **overdue days** count from
the due date of its oldest invoice that is still `PastDue`. Paying or voiding
that invoice moves the count to the next one, or ends it.

`Void` invoices never count, nor do invoices a provider no longer updates
(the stale invoices the portals already hide).

### Payment Standing

The billing controller sets a new `BillingAccount` condition,
`PaymentStanding`:

| Status | Reason | Meaning |
|---|---|---|
| True | `Good` | Nothing overdue. |
| False | `Overdue` | Overdue, nothing restricted. |
| False | `Restricted` | Restricted. |
| False | `Suspended` | Projects suspended for non-payment. |
| False | `OnHold` | Overdue, held by staff. |

The message names the oldest overdue invoice, how long it's been overdue and,
for automatic stages, when the next one starts. `status.paymentStanding`
records the stage, since when, and the next stage's date, so the portals don't
recompute them.

`InvoicingReady` and `PaymentReady` keep their meaning. While an account is
suspended for non-payment, its phase is `Suspended`, which billing already
defines and nothing sets yet; it returns to `Ready` when the account is back
in good standing. A `Suspended` account can't have projects bound to it
([design](../design.md#account-suspension)).

### Restricting

Restricting means the account can't grow its bill: no new projects bound to
it and no new or larger resources in its projects. Running resources aren't
touched. Milo has no such mode today (see [Open Questions](#open-questions)).
Two candidates:

- **Quota.** Billing caps the account's projects' quota grants at what they
  use now. Quota already gates creation for every service, so nothing new is
  needed per service, but it touches every grant and needs a way back.
- **A restricted suspension.** Milo's suspension admission plugin gains a mode
  that denies creates and scale-ups but allows updates and deletes, without
  pausing workloads. One mechanism for both stages, but a change to Milo and
  to what providers' hooks mean.

Billing also stops new `BillingAccountBinding`s to a restricted account, in
its webhook.

### Suspending

To suspend an account, billing creates a `ProjectSuspension` for each project
bound to it:

```yaml
apiVersion: resourcemanager.miloapis.com/v1alpha1
kind: ProjectSuspension
metadata:
  name: billing-ba-acme-default-project
  labels:
    billing.miloapis.com/billing-account: ba-acme
spec:
  projectRef:
    name: default-project
  reason: Billing
  reinstateAuthority: Operator
  requestedBy: system:billing
  description: Invoice INV-123 is 21 days overdue
```

Milo then marks the project `Suspended`, blocks writes to it, and runs each
service's Suspend hook to pause its workloads; services mark their
`ServiceConsumer` `Paused`. Data and configuration stay. To lift a
suspension, billing deletes its `ProjectSuspension`s (labelled with the
account); suspensions for other reasons stay.

`reinstateAuthority: Operator` keeps the customer from lifting a billing
suspension themselves, while billing (an operator) lifts it when they pay.

### Deletion

Milo deletes a project that stays suspended past its retention window
(`--project-suspension-retention-window-days`, 30 by default), whatever the
reason, after countdown emails. For billing that would delete a customer's
projects 30 days after suspension with no one deciding to.

So this enhancement needs Milo to exempt `Billing` suspensions from automatic
deletion (as it already can exempt reasons from the warning emails), and
billing not to suspend anything until that's in place. Deleting a suspended
account's projects stays a staff decision, made in the staff portal and
recorded.

### Configuration

The billing operator config gains:

```yaml
nonPaymentEnforcement:
  enabled: false           # off until Milo exempts Billing suspensions
  card:
    restrictAfter: 168h    # 7 days overdue
    suspendAfter: 504h     # 21 days overdue
  invoiceTerms:
    flagAfter: 168h        # 7 days overdue
    decisionAfter: 336h    # 14 days overdue
```

With `enabled: false`, billing still sets `PaymentStanding` (`Overdue`) and
sends notifications, but never restricts or suspends, so the signal can be
watched in production first.

### Notifications

Emails go through Milo's notification `Email`s, with templates in
`datum-cloud/email-templates`, as the payment terms emails do:

- to the billing contact (and the invoice email): overdue, restricted,
  suspended, back in good standing. Each names the invoice and links to it;
  the restricted email says when suspension follows.
- to support: when a card account is suspended, and when an account on
  invoice terms needs a decision.

Stripe's own reminder emails continue alongside these.

### Staff Portal and Cloud Portal

- **Staff portal:** the billing account shows its standing and the next
  stage's date. Actions restrict, suspend, lift and put on hold, each with a
  reason. A worklist shows accounts on invoice terms that need a decision.
- **Cloud portal:** a banner for overdue, restricted and suspended accounts,
  linking to the invoice and to paying it (Stripe's hosted invoice page).
  Creating a project or resource in a restricted account explains why it's
  refused.

### RBAC

Staff actions are recorded on a staff-only resource, like
`BillingArrangement`, since the customer can edit their own `BillingAccount`:
a `BillingEnforcement` in the account's namespace (`Restrict`, `Suspend`,
`Lift` or `Hold`, with a reason and, for a hold, an end date). A new role,
`Billing Enforcement Admin`, creates them, and infra binds it to staff.
Customers can read their account's standing but not create these.

Billing's operator needs `create` and `delete` on `ProjectSuspension`s, and
read on `Project`s, in Milo.

### Activity

Each change of standing is recorded on the billing account's timeline, with
who or what caused it: "Billing restricted billing account acme: invoice
INV-123 is 7 days overdue", "Sam lifted the suspension of billing account
acme", "Sam put billing account acme on hold until 30 November: payment
promised".

## Open Questions

1. **How to restrict.** Quota caps or a restricted mode of Milo's
   suspension? Restricting is only useful if it stops new resources in every
   service.
2. **Exempting `Billing` from deletion.** A per-reason exemption in Milo's
   escalation controller, or a field on `ProjectSuspension`?
3. **When card invoices fall due.** Card invoices are charged at once, but
   `PastDue` follows the invoice's due date, which for a card account may be
   net 30. Should overdue days for a card account count from the first failed
   charge instead?
4. **Timings.** Are 7 and 21 days right for cards, and 7 and 14 for flagging
   accounts on invoice terms?
5. **Accounts shared by several organizations' projects.** Suspending an
   account suspends every project bound to it, which may span teams.
6. **Free and credit-funded accounts.** An account whose invoices are paid by
   credits never goes overdue; confirm nothing here applies to it.

## Alternatives

**Suspend only, no restriction stage.** Simpler, but the first thing a card
customer would notice is their workloads stopping, rather than being unable
to add more.

**Staff decide for every account.** Safer, but self-serve accounts are where
the volume is, and staff would be paging through failed cards.

**Act on `PaymentReady` alone.** A failed card degrades `PaymentReady` before
anything is overdue, but customers often fix it within Stripe's retries. Time
overdue is the fairer measure.

**A new suspension mechanism in billing.** Milo's `ProjectSuspension` already
pauses workloads through every service's hooks, blocks writes and reinstates
cleanly. Billing should use it.

## Implementation History

- 2026-11-03: Enhancement drafted.
