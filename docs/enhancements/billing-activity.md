---
status: implementable
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Billing Activity

- [Summary](#summary)
- [Motivation](#motivation)
- [Design](#design)
  - [Who records what](#who-records-what)
  - [Event contract](#event-contract)
  - [Reasons](#reasons)
  - [Visibility](#visibility)
  - [Recording each transition once](#recording-each-transition-once)
  - [Policies](#policies)
- [Provider checklist](#provider-checklist)
- [Testing](#testing)
- [Future Work](#future-work)

## Summary

The activity timeline records what people do to billing resources from the
audit log. Nothing a person does explains what happened to a bill, though:
invoices are written by the invoicing provider, and payment outcomes come from
the payment processor. This enhancement defines the Kubernetes Events that
billing and its providers record for invoices, payment methods and billing
accounts, and the ActivityPolicies that turn them into timeline entries, so
support, staff and account owners can answer "what happened with this bill?"
without opening Stripe or the invoicing vendor.

Tracking issue: [milo-os/billing#207](https://github.com/milo-os/billing/issues/207).

## Motivation

- **Audit rules can't see it.** Only the provider writes `Invoice`, as a
  service account, and every billing audit rule skips `system:` users. The
  status patches that remain don't say why anything changed.
- **Only the provider knows why.** Billing can see an invoice move from
  `Open` to `PastDue`, but not that a charge was declined for insufficient
  funds and will be retried on the 5th. Diffing status can't recover that.
- **The timeline must stay vendor-agnostic**, in the same way `Invoice` is.

## Design

### Who records what

| Resource | Reasons | Recorded by |
| --- | --- | --- |
| `Invoice` | `InvoiceIssued`, `InvoicePaymentSucceeded`, `InvoicePaymentFailed`, `InvoicePaymentActionRequired`, `InvoicePartiallyPaid`, `InvoicePastDue`, `InvoiceVoided`, `InvoiceCurrencyMismatch` | the invoicing provider (openmeter-provider) |
| `PaymentMethod` | `PaymentMethodAttached`, `PaymentMethodFailed` | the payment method provider (stripe-provider) |
| `PaymentMethod` | `PaymentMethodExpiringSoon`, `PaymentMethodExpired` | billing, from the card expiry on status |
| `BillingAccount` | `InvoicingPastDue`, `InvoicingCurrent` | billing, when `InvoicingReady` flips |
| `BillingAccount` | `DefaultPaymentMethodChanged` | billing, when it makes an account's only card the default |
| `BillingArrangement` | `PaymentTermsEnded` | billing, when terms reach `endsAt` on their own |

People changing the default payment method or ending payment terms are
recorded from the audit log, as before. The events cover only what happens
without a person.

Card expiry is billing's, not the provider's, because the expiry is already
on `PaymentMethod` status whichever provider confirmed the card.

### Event contract

The reasons and annotation keys are constants in
[`api/v1alpha1/events.go`](../../api/v1alpha1/events.go). Emitters import
them; they don't copy strings.

An Event is a core/v1 Event on the resource it describes (recorded with
`record.EventRecorder.AnnotatedEventf`; the events/v1 recorder can't set
annotations). Its detail goes in annotations with the
`event.billing.miloapis.com/` prefix:

| Annotation | Value |
| --- | --- |
| `billing-account` | BillingAccount name |
| `invoice` | Invoice name |
| `period-start` | invoice period start, RFC 3339 UTC |
| `currency` | ISO 4217 code |
| `amount` | total for issued, paid and voided invoices; the amount attempted for payments |
| `amount-paid`, `amount-due` | decimal strings, as on Invoice status |
| `due-date`, `next-attempt` | RFC 3339 UTC |
| `decline-reason` | a `DeclineReason` |
| `payment-method` | PaymentMethod name |
| `card-brand`, `card-last4` | as on `PaymentMethodCardDetails` |
| `card-expiry` | first instant of the card's expiry month, RFC 3339 UTC |
| `ends-at` | when payment terms ended, RFC 3339 UTC |

The policies render summaries from these annotations, never from the event
message. The wording, the date format and what a customer sees therefore
stay in billing, and a provider can't leak vendor text into the timeline by
accident. The message is a plain fallback for `kubectl describe`, and it
must not contain raw vendor messages, identifiers or decline codes either.

`DeclineReason` is the small normalized set the issue asked for:
`Declined`, `InsufficientFunds`, `ExpiredCard`, `AuthenticationRequired` and
`ProcessingError`. Providers map their vendor's codes onto it. Anything that
doesn't map is `Declined` if the issuer refused it, or `ProcessingError` if
the attempt never reached a decision.

### Reasons

| Reason | When | Summary |
| --- | --- | --- |
| `InvoiceIssued` | Invoice first issued as `Open` | The October 2026 invoice for billing account acme was issued: USD 482.19, due 30 November 2026 |
| `InvoicePaymentSucceeded` | moves to `Paid` | The October 2026 invoice for billing account acme was paid: USD 482.19 |
| `InvoicePaymentFailed` | each declined or failed charge attempt | Payment of USD 482.19 for the October 2026 invoice for billing account acme was declined (insufficient funds). Next attempt on 5 November 2026 |
| `InvoicePaymentActionRequired` | the charge needs 3-D Secure / SCA | Payment of USD 482.19 for the October 2026 invoice for billing account acme needs the card holder to confirm it |
| `InvoicePartiallyPaid` | `amountPaid` > 0 and `amountDue` > 0 | USD 200.00 was received for the October 2026 invoice for billing account acme; USD 282.19 is still due |
| `InvoicePastDue` | moves to `PastDue` | The October 2026 invoice for billing account acme is past due: USD 482.19 outstanding |
| `InvoiceVoided` | moves to `Void` | The October 2026 invoice for billing account acme was voided |
| `InvoiceCurrencyMismatch` | `Ready=False`, reason `CurrencyMismatch` | (staff only) The October 2026 invoice for billing account acme is in EUR, which does not match the billing account's currency |
| `PaymentMethodAttached` | the card is confirmed and chargeable | Visa ending 4242 was added to billing account acme and is ready to use |
| `PaymentMethodFailed` | card setup declined or failed | Payment method card-1 could not be added to billing account acme: the card was declined (insufficient funds) |
| `PaymentMethodExpiringSoon` | an Active card's expiry month begins | Visa ending 4242 on billing account acme expires at the end of November 2026 |
| `PaymentMethodExpired` | the expiry month has ended | Visa ending 4242 on billing account acme has expired |
| `DefaultPaymentMethodChanged` | billing makes the only card the default | Visa ending 4242 is now the default payment method for billing account acme |
| `InvoicingPastDue` | `InvoicingReady` becomes `False/PastDue` | Billing account acme is past due on invoice acme-2026-10 |
| `InvoicingCurrent` | `InvoicingReady` returns to `True` | Billing account acme is no longer past due |
| `PaymentTermsEnded` | terms reach `endsAt` without anyone editing them | Payment terms for billing account acme ended on 30 September 2027 |

"Expiring soon" means the expiry month has begun. Monthly invoices are
charged after the month closes, so a card in its expiry month will already
have expired when the next invoice is charged.

### Visibility

These are the decisions on the questions the issue left open.

- **Amounts are shown.** The billing account's timeline is read by the
  organization's members, who can already read its invoices, and an entry
  that says only "a payment failed" doesn't answer the question. This is
  unlike `BillingArrangement.spec.reason`, which is staff's internal note.
- **Decline detail is normalized.** Customers see the `DeclineReason`
  wording only, never a vendor decline code or message.
- **Staff-only events are kept off the customer timeline by scope.** Milo
  places an Event on a tenant's timeline from its
  `platform.miloapis.com/scope.type` and `scope.name` annotations.
  Controllers write as service accounts, which Milo doesn't scope, so an
  emitter adds them with `OrganizationScopeAnnotations(namespace)`. An Event
  without them stays on the platform timeline, which only staff read.
  `InvoiceCurrencyMismatch` is recorded that way.

### Recording each transition once

Retried webhooks, requeues and restarts must not produce a second "paid".

- **Billing** records after its status write succeeds, and only when the
  stored state shows a transition (the `InvoicingReady` condition before and
  after, or a `BillingArrangement` going from `Active` to `Ended` while its
  generation equals its observed generation). A failed write retries without
  having recorded anything. Card expiry notices are written to the
  `billing.miloapis.com/expiry-notice` annotation with an optimistic lock
  before the event is recorded.
- **Providers** record the transitions they've already emitted on the
  resource (a provider-prefixed annotation, for example) and compare before
  recording. `InvoicePaymentFailed` can legitimately repeat, once per
  attempt, so providers key it on the attempt.

### Policies

`config/components/activity-policies/` gains an `Invoice` policy, and the
`PaymentMethod`, `BillingAccount` and `BillingArrangement` policies gain
`eventRules`. They follow the `BillingArrangement` style: name the billing
account (linked), link the subject, use human dates, and guard every
annotation a summary reads, because a summary that errors sends the event to
the dead-letter queue. Every reason also has a fallback rule that needs only
the reason, so an event missing its detail still reaches the timeline.

The `BillingAccount` policy also gets an audit rule for a person setting
`spec.defaultPaymentMethodRef` with a merge patch, so the timeline says "made
payment method card-1 the default" instead of "updated billing account".

## Provider checklist

1. Import `go.miloapis.com/billing/api/v1alpha1` for the reasons, annotation
   keys and `DeclineReason`.
2. Get a recorder with `mgr.GetEventRecorderFor(...)` and record with
   `AnnotatedEventf` on the `Invoice` or `PaymentMethod`.
3. Merge `OrganizationScopeAnnotations(obj.GetNamespace())` into the
   annotations, except for staff-only reasons.
4. Format times with `EventTime` and amounts exactly as on Invoice status.
5. Map vendor decline codes to `DeclineReason`; never pass them through.
6. Record each transition once, as described above.
7. Grant the controller `create` and `patch` on core `events`.

## Testing

Billing's e2e cluster doesn't run the Activity service, so
`test/activitypolicy` evaluates the policies the way the Activity service
does (rules in order, first match wins, summaries as `{{ CEL }}` templates)
against sample Events, and asserts on the rendered summaries and links. The
controllers' unit tests assert that each transition records its event once.

## Future Work

- `InvoiceRefunded`, `InvoiceCreditNoteIssued`, `InvoiceDisputed` and
  `InvoiceDisputeResolved`, once Invoice has the status for them.
- `CreditLimitReached`, once the `BillingArrangement` credit limit is
  enforced.
- Credits, suspension for non-payment, and spend limits, once those
  resources exist.
