---
status: provisional
stage: alpha
latest-milestone: "v0"
---

<!-- omit from toc -->
# Offer Discounts

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [API](#api)
  - [How It Works](#how-it-works)
  - [Per-Customer Discounts and Lifecycle](#per-customer-discounts-and-lifecycle)
- [Design Details](#design-details)
  - [Validation](#validation)
  - [Provider Requirements](#provider-requirements)
  - [Service Catalog](#service-catalog)

Tracked in [#132](https://github.com/milo-os/billing/issues/132).

## Summary

An `Offer` bundles `ServicePricing`s into a named tier and can carry optional
percentage discounts. A discount can apply across the entire Offer or to
specific services within it. The customer's invoice shows each service at its
list price, a separate discount line, and the net amount.

Because discounts live on `Offer`s (and accounts bind to an Offer via
`BillingEntitlement`), per-customer pricing and promotional tiers are trivial:
customers with negotiated discounts receive their own Offer or promotional
tier without altering service definitions or touching `service-catalog`.

## Motivation

Today the only way to give a service away is to price it at `0` in
`ServicePricing`. The invoice then shows `0`, so the customer never sees what
the service is worth, and charging for it later means repricing the service.

Placing discounts on `ServicePricing` also makes per-customer discounts
awkward: because `ServicePricing` resources are generated globally from
`ServiceConfiguration`s, discounting a service discounts it for every customer
on the platform.

Defining discounts at the `Offer` level solves this:

- `ServicePricing` remains the clean list price catalog authored by services.
- Platform owners can author standard tiers (no discount), promotional tiers
  (e.g., 100% off a newly launched service), or customer-specific tiers
  (e.g., 20% off for Acme Corp).
- Assigning, changing, or ending a discount is as simple as switching an
  account's `BillingEntitlement` to a different `Offer`.
- `service-catalog` remains completely decoupled from commercial discounting.

### Goals

- Support discounts on the entire Offer (e.g., 20% off all usage services).
- Support discounts on individual services within the Offer (e.g., 100% off a
  new compute service while storage is billed at list price).
- Support per-customer discounts cleanly by assigning customer-specific or
  tier-specific Offers via `BillingEntitlement`.
- Show the list price, the discount, and the net amount on the invoice.
- Make it possible to start, modify, or end a discount without changing base
  rates.

### Non-Goals

- Discount expiry dates. Providers do not expire plan discounts on their own,
  so changing or ending a discount is an explicit Offer update or reassignment.
- Free allowances ("first N units free"). Use a `0` rate tier for that.
- Custom discounts defined directly on `BillingEntitlement`. An `Offer` remains
  the versioned, auditable, snapshotted price package.

## Proposal

### API

`OfferSpec` gets an optional `discount` (applies to all services in the
Offer), and `ServicePricingRef` gets an optional `discount` (overrides or sets
the discount for that specific service):

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: Offer
metadata:
  name: payg-launch-promo
spec:
  launchStage: Draft
  chargeTypes:
    - Usage
  # Optional: default discount for all services in this Offer
  discount:
    percentage: 20
  servicePricingRefs:
    # Specific service override (e.g. 100% off during preview)
    - name: compute-cpu
      discount:
        percentage: 100
    # Inherits the Offer-level 20% discount
    - name: object-storage
```

When published to `GA`, the controller snapshots the resolved discount onto
each `ServicePricingSnapshot` in `spec.servicePricings`:

```yaml
spec:
  launchStage: GA
  chargeTypes:
    - Usage
  servicePricings:
    - name: compute-cpu
      discount:
        percentage: 100
      spec: ...
    - name: object-storage
      discount:
        percentage: 20
      spec: ...
```

`percentage` is a whole number from `0` to `100`. Omitting `discount` means
no discount.

### How It Works

The discount applies to the service's **whole charge for the period**, after
the rates (flat or tiered) have been applied: 1,000 units at `0.02` is `20.00`,
and a 20% discount takes `4.00` off. It never changes the rates or the usage
that is recorded.

When an Offer transitions to `GA`, the controller snapshots the resolved
discount alongside the `ServicePricing` spec, ensuring the Offer is completely
immutable. The billing provider reads the discounts from the snapshot.

On the invoice, for a service with 400 units at `0.10` and a 100% discount:

| Line | Amount |
|---|---|
| Service: 400 x $0.10 | $40.00 |
| Service: discount (100%) | -$40.00 |
| **Total** | **$0.00** |

An invoice that nets to `0` is issued and settled like any other, and still
appears as an `Invoice`.

### Per-Customer Discounts and Lifecycle

Because discounts belong to `Offer`s:

1. **Standard Tier:** A public Offer (e.g. `payg`) with list prices and no
   discounts.
2. **Promotional Tier:** An Offer (e.g. `payg-preview`) with a 100% discount
   on preview services.
3. **Per-Customer Tier:** A negotiated Offer (e.g. `payg-acme-corp`) with
   specific customer discounts.

Managing discounts for an account:

1. Set the account's `BillingEntitlement.spec.offerRef` to the desired Offer.
2. The provider moves the account's subscription to it immediately, so the
   next invoice reflects the change.
3. To end a customer's discount, point their `BillingEntitlement` back to the
   standard Offer.

Accounts that stay on an older Offer keep their existing terms.

## Design Details

### Validation

- `discount.percentage` must be an integer between `0` and `100`.
- Percentage discounts apply across all charge types (`Usage`, `OneTime`,
  `Recurring`).
- Validated on create and update of Draft `Offer`s. Published GA Offers are
  already immutable.

### Provider Requirements

A billing provider that supports discounts must:

- Apply the percentage to the service's whole charge, as described above.
- Show the discount as its own line on the invoice, next to the full-price line.
- Report spend (`UsageQuery`) net of the discount, so it matches the invoice.
- Reject, with a clear condition on the Offer, a discount it cannot represent.

Providers that don't support discounts must reject an Offer that has one,
rather than ignore it and bill the full price.

### Service Catalog

With Offer-level discounts, **`service-catalog` requires no changes**:

- `ServiceConfiguration` defines service charges and list prices without
  needing discount fields.
- `service-catalog` fans out standard `ServicePricing` resources as before.
- Commercial packaging, customer tiers, and discounting live entirely within
  the billing domain on `Offer` resources.
