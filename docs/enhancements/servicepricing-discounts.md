---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# ServicePricing Discounts

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [API](#api)
  - [How It Works](#how-it-works)
  - [Adding and Ending a Discount](#adding-and-ending-a-discount)
- [Design Details](#design-details)
  - [Validation](#validation)
  - [Provider Requirements](#provider-requirements)
  - [Service Catalog](#service-catalog)

Tracked in [#132](https://github.com/milo-os/billing/issues/132).

## Summary

A `ServicePricing` can carry an optional percentage discount. The customer's
invoice shows the service at its list price, a separate discount line, and the
net amount. A 100% discount lets us launch a service at its real price while
charging nothing, and later end the discount without repricing anything.

## Motivation

Today the only way to give a service away is to price it at `0`. The invoice
then shows `0` for the service, so the customer never sees what it is worth,
and charging for it later means repricing the service.

### Goals

- Discount individual services, so a new service can launch free while the
  others are charged.
- Show the list price, the discount and the net amount on the invoice.
- Make it possible to end a discount without touching the rates.

### Non-Goals

- Discounts on the whole Offer.
- Discount expiry dates. Providers can't expire a discount on their own, so
  ending one is an explicit change.
- Free allowances ("first N units free"). Use a `0` rate tier for that.

## Proposal

### API

`ServicePricingSpec` gets an optional `discount`:

```yaml
spec:
  chargeType: Usage
  metric: compute.datumapis.com/instance/cpu-allocated
  pricingUnit: vcpu
  rates:
    - flat: "0.04"
  discount:
    percentage: 100
```

`percentage` is a whole number from `0` to `100`. Omitting `discount` means
no discount.

### How It Works

The discount applies to the service's **whole charge for the period**, after
the rates (flat or tiered) have been applied: 1,000 units at `0.02` is `20.00`,
and a 30% discount takes `6.00` off. It never changes the rates or the usage
that is recorded.

An Offer snapshots the discount with the rest of the `ServicePricing` when it is
published, so the snapshot is as immutable as the rates are. The billing
provider reads the discount from the snapshot.

On the invoice, for a service with 400 units at `0.10` and a 100% discount:

| Line | Amount |
|---|---|
| Service: 400 x $0.10 | $40.00 |
| Service: discount (100%) | -$40.00 |
| **Total** | **$0.00** |

An invoice that nets to `0` is issued and settled like any other, and still
appears as an `Invoice`.

### Adding and Ending a Discount

Because a GA Offer can't change, a discount is added or ended by publishing a
new Offer:

1. Set or remove `discount` on the `ServicePricing`.
2. Publish a new Offer that references it.
3. Point the accounts' `BillingEntitlement`s at the new Offer. The provider
   moves each account's subscription to it immediately, so the next invoice
   reflects the change.

Accounts that stay on the old Offer keep the old discount.

## Design Details

### Validation

- `discount.percentage` must be an integer between `0` and `100`.
- In the first version, `discount` is only allowed when `chargeType` is `Usage`.
  `OneTime` and `Recurring` are rejected until the provider is tested with
  them.
- Applies on create and update of a `ServicePricing`. Published Offers are
  already immutable.

### Provider Requirements

A billing provider that supports discounts must:

- Apply the percentage to the service's whole charge, as described above.
- Show the discount as its own line on the invoice, next to the full-price line.
- Report spend (`UsageQuery`) net of the discount, so it matches the invoice.
- Reject, with a clear condition on the Offer, a discount it can't represent.

Providers that don't support discounts must reject an Offer that has one,
rather than ignore it and bill the full price.

### Service Catalog

`ServicePricing`s are not authored by hand: service-catalog generates them from
each `ServiceConfiguration`'s charges. Adding `discount` to `ServicePricing` is
therefore not enough on its own. service-catalog must receive the same update:

- A usage charge in `ServiceConfiguration` accepts the same `discount`.
- The fan-out copies it onto the generated `ServicePricing`, and removes it
  when it is removed from the charge.
- Its validation applies the same rules as above.
