---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Invoice Numbers

- [Summary](#summary)
- [What we have today](#what-we-have-today)
- [Goals](#goals)
- [Non-goals](#non-goals)
- [Design](#design)
  - [The format](#the-format)
  - [Customer numbers](#customer-numbers)
  - [Handing out a number](#handing-out-a-number)
  - [When a customer gets a number](#when-a-customer-gets-a-number)
  - [Customers who already have invoices](#customers-who-already-have-invoices)
  - [Staging and production](#staging-and-production)
  - [Running out](#running-out)
- [Alternatives considered](#alternatives-considered)
- [Rollout](#rollout)
- [Testing](#testing)
- [Open questions](#open-questions)

## Summary

Invoice numbers look like `K7QX2M4P-0003` today: a random code for the
customer and a count of their invoices. Customers find that hard to read
out, type into a payment reference or match against their records.

This enhancement makes every new invoice number all digits, in the form
`1042-0003`: the customer's number, then the count of that customer's
invoices. stripe-provider gives each customer a number the first time
it finalizes an invoice for them, and Stripe does the rest.

## What we have today

- **Stripe numbers every invoice.** When a draft is finalized, Stripe
  sets its number to the customer's `invoice_prefix`, a hyphen and the
  customer's next sequence number (`next_invoice_sequence`, four digits,
  starting at `0001`). The Stripe account uses per-customer numbering,
  Stripe's default for a US account.
- **Prefixes are random.** stripe-provider creates Stripe customers
  (`internal/stripe/client.go`, `EnsureCustomer`) without an
  `invoice_prefix`, so Stripe makes one up: eight uppercase letters and
  digits.
- **Only stripe-provider finalizes invoices.** The invoice writer
  (`internal/controller/stripeinvoice_writer.go`) syncs a
  `StripeInvoice`'s draft and then calls `FinalizeInvoice` once
  `spec.finalize` is set. Previews and preview drafts are never finalized,
  so they never take a number.
- **Everything else copies Stripe's number.** stripe-provider records it
  in `StripeInvoice.status.number`. openmeter-provider passes it to
  OpenMeter and to the Milo `Invoice` (`status.number`), and both portals
  display that field. Neither portal parses it.

What Stripe allows
([Invoice numbering](https://docs.stripe.com/invoicing/customize#invoice-numbering)):

- A prefix is 1 to 12 uppercase letters **or digits**, so an all-digit
  prefix is valid.
- A prefix must be unique in the account and can never be reused, even
  after its customer is deleted.
- A new or changed prefix applies to invoices finalized from then on.
  Invoices already finalized keep their numbers.

## Goals

- New invoice numbers are all digits: `NNNN-NNNN`.
- The first half identifies the customer and stays the same on every
  invoice they get.
- No two customers ever get the same number, including after a crash, a
  restart or a deleted account.
- Customers who are never invoiced don't use up a number.

## Non-goals

- Renumbering invoices that are already finalized. Stripe doesn't allow
  it, and their numbers are already on customers' records.
- One sequence across all customers (see
  [Alternatives considered](#alternatives-considered)).
- Showing customer numbers in the portals. They appear in every invoice
  number, which is enough for now.

## Design

### The format

`<customer number>-<invoice sequence>`, for example `1042-0003`:

- **Customer number:** a decimal number with no leading zeros, starting
  at 1001, so it's four digits until customer 9999.
- **Invoice sequence:** Stripe's own per-customer count, four digits
  with leading zeros.

The provider sets only the prefix. Stripe still assigns the sequence and
the full number, and still guarantees the number is unique.

### Customer numbers

Each customer's number is recorded in two places in stripe-provider's
API:

- `StripeCustomer.status.customerNumber` holds the account's number once
  it has one. `StripeCustomer` is already one per billing account and
  owned by it.
- `StripeProviderConfig.status.lastCustomerNumber` holds the highest
  number handed out. It's the counter, one per Stripe account.

A new, optional field on the config turns the feature on and sets where
numbering starts:

```yaml
apiVersion: stripe.billing.miloapis.com/v1alpha1
kind: StripeProviderConfig
metadata:
  name: default
spec:
  invoiceNumbers:
    firstCustomerNumber: 1001
```

With `invoiceNumbers` unset, the provider leaves prefixes alone, as it
does today.

### Handing out a number

stripe-provider runs with leader election, so one process does all the
writing. It hands out a number under a single lock:

1. Read the config uncached and take
   `max(lastCustomerNumber, firstCustomerNumber - 1) + 1`.
2. Write the new `lastCustomerNumber` to the config's status as an
   update, not a patch, so a conflicting write fails and the step starts
   again.
3. Write the number to the account's `StripeCustomer.status`.
4. Set the Stripe customer's `invoice_prefix` to the number.

The counter moves before anything uses the number, so a crash between
steps can skip a number but never hand one out twice. Skipped numbers
are fine: nothing needs customer numbers to be gapless.

There are two more guards:

- **The counter is lost.** Status isn't backed up, so a recreated config
  starts with an empty counter. Before handing out the first number after
  starting, the provider lists every `StripeCustomer` uncached and raises
  the counter to the highest `customerNumber` it finds.
- **Stripe refuses the prefix.** If Stripe says the prefix is taken, for
  example by a deleted customer whose `StripeCustomer` is gone, the
  provider records that, takes the next number and tries again. Stripe's
  uniqueness rule is the last line of defense, not the first.

### When a customer gets a number

The invoice writer gives a customer a number just before it first
finalizes one of their invoices: in `reconcileInvoice`, once
`spec.finalize` is set and the draft has synced, and before
`FinalizeInvoice`. Once `StripeCustomer.status.customerNumber` is set,
this step does nothing.

Waiting until then means:

- Accounts that sign up and never get invoiced, including fraud and test
  sign-ups, don't use up numbers that can never be reused.
- Previews don't use numbers.
- The number is on the Stripe customer before Stripe numbers the invoice.

If setting the prefix fails, the provider doesn't finalize. It reports
the error on the `Finalized` condition, as it does for any other Stripe
error, and retries. An invoice is never finalized with a random prefix
because the numbering step failed.

Invoices that staff create by hand in the Stripe dashboard use whatever
prefix the customer has. That's a random one until the provider first
finalizes an invoice for them.

### Customers who already have invoices

No backfill job is needed. An existing customer gets a number the next
time the provider finalizes one of their invoices, and their earlier
invoices keep their random-prefix numbers.

Stripe keeps counting the customer's sequence from where it was. A
customer whose last invoice was `K7QX2M4P-0003` gets `1042-0004` next,
not `1042-0001`. Whether Stripe lets us reset the sequence after a
prefix change is an [open question](#open-questions). Production has
few invoiced customers today, so this affects few people either way.

### Staging and production

Staging bills a Stripe test mode and production a live mode, each with
its own `StripeProviderConfig` and so its own counter. Stripe doesn't say
whether test and live mode share prefixes. Until we know, staging starts
at `firstCustomerNumber: 90001`, so its numbers can't collide with
production's and are easy to spot as test invoices.

### Running out

There's no hard limit:

- **Customer numbers:** 1001 to 9999 gives 8,999 invoiced customers with
  four digits. Customer 10000 gets `10000-0001`. Stripe allows prefixes
  up to 12 characters, so the only change is a longer number.
- **Invoice sequences:** a customer billed monthly, plus a few charges
  invoiced straight away, takes centuries to reach 9999. Stripe doesn't
  document what happens after that; the open questions cover it.

## Alternatives considered

- **One sequence for the whole Stripe account.** In Stripe's invoice
  settings, a numeric account prefix gives `1000-0001`, `1000-0002`
  across all customers, with no code. It's the simplest option, but every
  customer can see roughly how many invoices Datum issues, and the number
  doesn't tell support whose invoice it is. It remains a fallback if this
  design stalls.
- **Setting the number on each invoice ourselves.** Stripe's API accepts
  a `number` on a draft. We'd then own uniqueness, and gap-free numbering
  where the law requires it, across every way an invoice gets made,
  including the Stripe dashboard. Stripe already does that for us.
- **Numbering customers when the Stripe customer is created.** That's
  simpler, but every sign-up that adds a card would use a number forever,
  whether or not it's ever invoiced.
- **A customer number owned by billing.** Billing could assign a number
  on each `BillingAccount` and show it in both portals, independent of
  Stripe. That makes sense if customer numbers become a product concept
  of their own. For invoice numbers alone, uniqueness is a rule of each
  Stripe account, so the provider that owns the Stripe account should
  own the counter. Moving it to billing later only means the provider
  reads the number instead of choosing it.

## Rollout

1. **stripe-provider:** the `invoiceNumbers` spec field, the two status
   fields, number allocation and the step before finalizing. It's off
   while the field is unset, so it can be released ahead of any config
   change.
2. **Staging:** set `firstCustomerNumber: 90001` on staging's config. A
   staff charge invoiced straight away for a test account should come out
   as `90001-NNNN`.
3. **Production:** release stripe-provider, bump its pin in infra, then
   set `firstCustomerNumber: 1001`.
4. **Stripe settings:** leave invoice numbering on "per customer" in both
   modes. Switching to account-wide numbering would ignore customer
   prefixes.

## Testing

- **Unit tests:**
  - numbers handed out in order from `firstCustomerNumber`;
  - a conflicting counter write retried;
  - the counter raised from the `StripeCustomer` high-water mark;
  - a refused prefix skipped;
  - a customer that already has a number left alone.
- **Envtest:** two invoices for different accounts finalized together
  get different numbers, and a restart in the middle of a hand-out never
  reuses a number.
- **Staging:** issue invoices for two accounts and check that the
  numbers in Stripe, `StripeInvoice.status.number`, the Milo `Invoice`
  and both portals match `9000N-NNNN`.

## Open questions

- **Test and live mode:** do they share prefixes? If not, staging can
  start at 1001 too.
- **Resetting the sequence:** can an existing customer's
  `next_invoice_sequence` go back to 1 after their prefix changes? Stripe
  only lets it rise past numbers already used, but it doesn't say whether
  that is per prefix.
- **Past 9999:** what does Stripe do after a customer's 9,999th invoice?
- **Leadership's format:** is `1042-0004` (customer number, then count)
  the format leadership wants, or should the second half have more
  digits?
