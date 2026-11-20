---
status: implementable
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Stripe Invoice Activity

- [Summary](#summary)
- [What we have today](#what-we-have-today)
- [Gaps](#gaps)
- [Design](#design)
  - [Which events, and who records them](#which-events-and-who-records-them)
  - [What each line says](#what-each-line-says)
  - [Sent: the webhook](#sent-the-webhook)
  - [Invoice changed: due dates only](#invoice-changed-due-dates-only)
  - [Uncollectible](#uncollectible)
  - [The staff portal card](#the-staff-portal-card)
- [Alternatives considered](#alternatives-considered)
- [Rollout](#rollout)
- [Testing](#testing)
- [Open questions](#open-questions)

## Summary

Staff open the Stripe dashboard to see what happened to an invoice: when it
was finalized, emailed to the customer (and emailed again), changed, paid,
or written off. This enhancement puts those events on the organization's
billing activity, next to the rest of
[Billing Activity](billing-activity.md), so the staff portal's
"Organization billing activity" card answers "did we send it?" without
leaving Datum.

Most of it already exists. The changes are one new event recorded from a
Stripe webhook (`InvoiceSent`), two reasons the invoicing provider can
derive from what it already projects (`InvoiceDueDateChanged`,
`InvoiceMarkedUncollectible`), and a filter change in the staff portal
that currently hides every event-sourced entry.

## What we have today

- **Billing owns the contract.** Reasons, annotation keys and the
  ActivityPolicies are in this repo
  ([`api/v1alpha1/events.go`](../../api/v1alpha1/events.go),
  `config/components/activity-policies/`). Providers record core/v1 Events
  with `event.billing.miloapis.com/` annotations and the organization's
  scope annotations. The Activity service turns them into timeline entries
  with billing's `eventRules`.
- **openmeter-provider records the invoice lifecycle** on the Milo
  `Invoice` it projects (`internal/controller/invoice_events.go`):
  `InvoiceIssued`, `InvoicePaymentSucceeded`, `InvoicePaymentFailed`,
  `InvoicePaymentActionRequired`, `InvoicePartiallyPaid`, `InvoicePastDue`,
  `InvoiceVoided`. It reads both OpenMeter and Stripe, every 10 minutes per
  account, and records from the stored status before and after each write,
  so each transition is recorded once. It covers OpenMeter's invoices and
  the ones staff create in Stripe.
- **stripe-provider receives Stripe webhooks** at `/webhooks/stripe`. It
  handles only `setup_intent.*` today and records `PaymentMethodAttached` and
  `PaymentMethodFailed`. Unhandled types get a 200. Deduplication is an
  in-memory cache of event IDs.
- **OpenMeter's invoices reach that endpoint.** A Stripe webhook endpoint
  receives every event of the types it's subscribed to in its account,
  whoever created the object. OpenMeter's Stripe app issues invoices in the
  same account stripe-provider manages customers in, so subscribing the
  endpoint to an `invoice.*` type delivers OpenMeter's invoices as well as
  staff-created ones. Today the endpoint isn't subscribed to any.

## Gaps

Against the Stripe dashboard's invoice events:

| Stripe shows | Timeline today |
| --- | --- |
| A draft invoice was finalized | `InvoiceIssued` (the same moment for us: an invoice is projected once it's issued) |
| The invoice was sent | **Nothing.** Nothing in `Invoice` status says it was emailed |
| The invoice was sent again | **Nothing** |
| The invoice has changed | **Nothing** (see below: mostly noise) |
| Paid | `InvoicePaymentSucceeded` |
| Payment failed | `InvoicePaymentFailed`, `InvoicePaymentActionRequired` |
| Voided | `InvoiceVoided` |
| Marked uncollectible | **Wrong:** recorded as `InvoiceVoided`, because uncollectible maps to phase `Void` |

And one gap that hides all of the above: the staff portal card filters on
`changeSource: human`, so event-sourced entries, which the Activity
service classifies as `system`, never show there. Today the card shows
none of the invoice events billing already records.

"The invoice has changed" (`invoice.updated`) fires for almost anything:
Stripe attaching a payment intent, a PDF being generated, and our own
writes. When openmeter-provider stamps `milo_invoice` and
`milo_billing_account` into a finalized invoice's metadata, Stripe shows
"has changed" about half a minute after it was sent. A line that says only
"changed" would be noise. The one post-finalization change worth a line is
the due date.

## Design

### Which events, and who records them

The rule from [Billing Activity](billing-activity.md) holds: whoever
projects the `Invoice` records its lifecycle, and webhooks add only what
the projection can't see.

| Reason | New? | Recorded by | On | From |
| --- | --- | --- | --- | --- |
| `InvoiceIssued` | no | openmeter-provider | `Invoice` | phase first set |
| `InvoiceSent` | **yes** | stripe-provider | `BillingAccount` | `invoice.sent` webhook |
| `InvoiceDueDateChanged` | **yes** | openmeter-provider | `Invoice` | `status.dueDate` changes while Open or PastDue |
| `InvoicePaymentSucceeded`, `InvoicePaymentFailed`, `InvoicePaymentActionRequired`, `InvoicePartiallyPaid`, `InvoicePastDue` | no | openmeter-provider | `Invoice` | as today |
| `InvoiceVoided` | no, but narrowed | openmeter-provider | `Invoice` | phase becomes Void from a voided invoice |
| `InvoiceMarkedUncollectible` | **yes** | openmeter-provider | `Invoice` | phase becomes Void from an uncollectible invoice |

stripe-provider doesn't also record paid, failed or voided from webhooks.
openmeter-provider already does, from the same facts, and two recorders
would give two "paid" lines. When stripe-provider writes invoices itself
([invoicing architecture][invoicing]), its webhooks update `StripeInvoice`
status, and openmeter-provider still projects the `Invoice` and records
from it, so nothing moves.

All of them carry the organization's scope annotations, so they're on the
organization's timeline: the staff card and the customer's own activity in
the cloud portal. They show amounts, as the existing invoice events do,
and never an email address.

### What each line says

New annotations in `events.go`:

| Annotation | Value |
| --- | --- |
| `invoice-number` | the number printed on the invoice, which the customer quotes |
| `previous-due-date` | RFC 3339 UTC |

openmeter-provider adds `invoice-number` to its invoice events where it
knows the number (it's on the `Invoice` as
`openmeter.miloapis.com/stripeInvoiceNumber`). The new `Invoice` rules
append it to the period ("the October 2026 invoice (ACME-0042)") when
present. The existing rules are unchanged for now.

| Reason | Summary (examples, made-up data) |
| --- | --- |
| `InvoiceSent` | Invoice ACME-0042 for billing account acme was emailed: USD 1250.00, due 18 December 2026 |
| `InvoiceSent`, with a Milo invoice | Invoice ACME-0042 (linked) for billing account acme was emailed: USD 1250.00, due 18 December 2026 |
| `InvoiceDueDateChanged` | The due date of the October 2026 invoice (ACME-0042) for billing account acme moved from 18 December 2026 to 8 January 2027 |
| `InvoiceMarkedUncollectible` | The October 2026 invoice (ACME-0042) for billing account acme was marked uncollectible: USD 1250.00 unpaid |

Each send is its own line, so a re-send reads as a second "was emailed"
on a later date. We don't try to label "sent again": Stripe's event
doesn't say whether it's the first send or who clicked, and a guess based
on timing would sometimes be wrong.

As with every billing reason, each gets a fallback rule needing only the
reason, so an event missing its detail still reaches the timeline.

### Sent: the webhook

stripe-provider handles `invoice.sent`:

1. **Decode** the invoice: ID, number, customer, currency, total, due date,
   status and metadata.
2. **Skip** invoices with `datum_purpose: PreviewDraft` (they're never
   meant to be sent; if someone sends one anyway, it isn't a bill).
3. **Find the account.** A field index on `StripeCustomer`
   `status.stripeCustomerId` gives the namespace and `billingAccountRef`.
   No `StripeCustomer` means not one of ours: 200, nothing recorded.
4. **Find the Milo invoice, if there is one.** The Stripe invoice's
   `milo_invoice` metadata names it. It's absent on the first send:
   Stripe emails the invoice the moment it's finalized, before
   openmeter-provider has projected it and stamped the metadata. So the
   event names it when it can, and the policy links it then.
5. **Record the Event** on the `BillingAccount`.

Why the `BillingAccount` and not the `Invoice`: the account is always
resolvable when the webhook arrives, and the invoice usually isn't yet.
Recording on whichever exists would split one reason across two
policies, and holding the webhook until the projection catches up would
turn ordinary sends into handler errors and alerts. The card lists the
organization's billing activity in one feed, so the subject only changes
the link.

**Recording once.** The in-memory dedupe doesn't survive a restart or a
second replica, and Stripe redelivers anything it isn't sure we got. So
this Event is created directly, not through the `EventRecorder`, with a
name derived from the Stripe event ID (`<account>.<first 16 hex of
sha256(evt_…)>`). A redelivery gets `AlreadyExists` and is a no-op. Its
timestamps are the Stripe event's `created`, so the line says when the
email went out, not when we processed it. The recorder can do neither: it
names Events randomly and stamps them now. The Event otherwise looks like
a recorded one (reporting controller `stripe-provider`, type `Normal`),
and the message is a plain fallback with no vendor IDs.

Errors reading the cache or creating the Event return 500 so Stripe
retries. Anything else is a 200.

### Invoice changed: due dates only

openmeter-provider already projects `status.dueDate`. When it changes on an
Invoice that was Open or PastDue before and after the write, it records
`InvoiceDueDateChanged` with both dates. That's the same
before-and-after comparison as the other reasons, so no webhook and no
extra state.

Nothing else from `invoice.updated` goes on the timeline. Amount changes
after finalization come from credit notes, which are already future work
in [Billing Activity](billing-activity.md#future-work) (`InvoiceCreditNoteIssued`).

### Uncollectible

openmeter-provider maps both voided and uncollectible to phase `Void`, and
today records `InvoiceVoided` for both. It knows which it is (OpenMeter's
status, or Stripe's for staff-created invoices), so on the change to `Void`
it records `InvoiceMarkedUncollectible` for an uncollectible invoice and
`InvoiceVoided` only for a voided one. The amount is the unpaid balance it
already reports as `amountDue`.

No new phase: billing's `Void` already means "no longer collected", and the
portals don't need to tell the two apart beyond the timeline.

### The staff portal card

`BillingAccountActivityCard` drops `changeSource: 'human'`. It keeps the
`billing.miloapis.com` API group and the organization's namespace.

That doesn't let controller noise in. Billing's audit rules all skip
`system:` users, and its event rules match only billing's own reasons, with
no catch-all. The only `system` entries in that filter are the timeline
events. The card's title stays; its doc comment changes from "only changes
people made" to people's changes and what happened to the organization's
bills.

## Alternatives considered

- **stripe-provider records every invoice event from webhooks.** It owns
  Stripe, but openmeter-provider already records the lifecycle from the
  same facts. Two recorders would double entries, or need a hand-off rule
  per reason. Only "sent" is invisible to the projection, so only it comes
  from a webhook.
- **openmeter-provider polls Stripe's Events API for `invoice.sent`.** No
  webhook subscription, and it owns the `Invoice`. But it would add a
  second polling loop against Stripe from the provider we're moving Stripe
  calls out of, with a 10-minute lag and a 30-day window.
- **A new CR the Activity service indexes.** The Activity service ingests
  audit logs and Kubernetes Events. A CR would only be visible through its
  audit log, written by a service account, which the policies skip. Events
  are the supported path.
- **Record on the `Invoice`, retrying the webhook until it exists.** Covered
  above: it makes every first send a handler error for up to 10 minutes.

## Rollout

1. **billing:** the three reasons and two annotations in `events.go`; rules
   in the `Invoice` and `BillingAccount` policies; `test/activitypolicy`
   cases; fold the reasons into [Billing Activity](billing-activity.md)'s
   tables. Released first, because the providers import the constants.
2. **openmeter-provider:** `InvoiceDueDateChanged`, `InvoiceMarkedUncollectible`,
   `invoice-number` on its invoice events. Bump billing.
3. **stripe-provider:** the `invoice.sent` handler, the `StripeCustomer`
   index and deterministic Events. Bump billing.
4. **Stripe:** subscribe the staging webhook endpoint to `invoice.sent`,
   check the next send, then production. This is a dashboard change; ask
   before making it.
5. **staff-portal:** drop the `changeSource` filter. Independent of the
   others; it starts showing the invoice events that already exist.

## Testing

- billing: `test/activitypolicy` renders each new reason with and without
  its optional annotations, and the fallbacks.
- openmeter-provider: `invoiceStatusEvents` cases for a due-date change
  (recorded once; not for Draft, Paid or Void), uncollectible vs voided,
  and `invoice-number`.
- stripe-provider: handler tests for a sent invoice with and without
  `milo_invoice`, an unknown customer, a preview draft, a redelivered event
  (one Event), and an API error (500).
- Staging: send a test-mode invoice from the Stripe dashboard to a staging
  customer and check the entry on the staff card.

## Open questions

- **Should `InvoiceMarkedUncollectible` be staff-only?** Writing an invoice
  off is a collections decision, and the customer may still owe it.
  Staff-only events go to the platform timeline, though, which the staff
  card doesn't read (it reads the organization's control plane). The
  proposal keeps it on the organization's timeline with neutral wording.
- **Does Stripe emit `invoice.sent` in test mode** for a send from the
  dashboard? It should; staging will tell us before production is
  subscribed.

[invoicing]: https://github.com/milo-os/stripe-provider/blob/main/docs/architecture/invoicing.md
