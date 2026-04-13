# Billing Service Design

## Why This Exists

Milo is a business operating system for B2B service providers -- a control plane
that helps them build, price, sell, and operate managed services. The billing
service is a foundational piece of Milo's monetization capability, connecting
service consumption to payment.

Without billing, there is no authoritative answer to the question: *"who pays
for this project?"* Every other financial operation -- usage metering, invoicing,
payment processing, revenue reporting -- depends on having a clear, auditable
record of billing responsibility.

## Goals

1. **Establish financial ownership.** Every project that consumes resources must
   have an unambiguous payer. The billing service makes this relationship
   explicit and enforceable.

2. **Support real-world billing structures.** Organizations are not monolithic.
   A single organization may need multiple billing accounts to model
   departments, regions, currencies, or client sub-accounts. The service
   supports multiple billing accounts per organization from day one.

3. **Provide an auditable billing history.** When billing responsibility
   changes -- a project moves from one account to another -- the system
   preserves a complete record. Old bindings are superseded, not deleted,
   so there is always a clear timeline of who was responsible and when.

4. **Enforce consistency at the platform level.** Business rules like "a
   project can only be billed to a Ready account" and "only one active
   billing relationship per project" are enforced by the system, not by
   convention or hope.

5. **Lay the foundation for monetization.** Billing accounts are the anchor
   point for everything that comes next: entitlements, invoices, payments,
   usage-based charging, and financial reporting.

## Non-Goals (for this milestone)

- **Invoicing and payments.** Generating invoices, collecting payments, and
  reconciling ledgers are downstream capabilities that build on top of billing
  accounts. They are not part of this initial scope.

- **Usage metering.** Tracking how much compute, storage, or network a project
  consumes is the responsibility of the telemetry service. The billing service
  will eventually consume that data, but does not produce it.

- **Pricing and entitlements.** Configuring what a service costs and what a
  billing account is entitled to use are separate concerns handled by the
  service catalog and entitlement systems.

- **Commitments and contracts.** Term-based deals, volume discounts, and
  enterprise agreements will be layered on later.

## How It Fits Into Milo

```
┌─────────────────────────────────────────────────────┐
│                    Organization                      │
│                                                      │
│  ┌──────────────┐  ┌──────────────┐                 │
│  │   Billing     │  │   Billing     │                │
│  │  Account A    │  │  Account B    │                │
│  │  (USD, Net30) │  │  (EUR, Net60) │                │
│  └──────┬───────┘  └──────┬───────┘                 │
│         │                  │                          │
│         ▼                  ▼                          │
│  ┌─────────────┐   ┌─────────────┐  ┌────────────┐ │
│  │  Project X   │   │  Project Y   │  │ Project Z  │ │
│  │  (bound to A)│   │  (bound to B)│  │ (unbound)  │ │
│  └─────────────┘   └─────────────┘  └────────────┘ │
└─────────────────────────────────────────────────────┘
```

**Upstream** (feeds into billing):
- **Resource Manager** provides organizations and projects
- **IAM** controls who can create and manage billing accounts
- **Quota** enforces resource limits that billing accounts are charged for

**Downstream** (consumes billing data):
- **Telemetry** reports usage per project, rolled up by billing account
- **Invoicing** generates monthly invoices for each billing account
- **Payments** charges the payment profile attached to the account
- **Reporting** aggregates spend across accounts for financial visibility

## Core Concepts

### Billing Account

A billing account represents the entity responsible for paying for service
consumption within an organization. It captures:

- **Currency** -- the ISO 4217 currency code for all charges (immutable after
  the account is activated, to prevent mid-period currency confusion)
- **Payment profile** -- a reference to the payment method (e.g., credit card)
  that will be charged for invoices
- **Payment terms** -- the commercial schedule: how many days after invoice to
  pay, how often invoices are generated, and on what day of the month
- **Billing contact** -- who receives billing notifications and invoices

A billing account progresses through lifecycle phases:

| Phase | Meaning |
|-------|---------|
| **Provisioning** | Account just created, not yet validated |
| **Incomplete** | Missing required configuration (e.g., no payment profile) |
| **Ready** | Fully configured; projects can be bound to it |
| **Suspended** | Temporarily disabled (e.g., payment failure, compliance hold) |
| **Archived** | Permanently closed; read-only for historical access |

An organization can have multiple billing accounts. A common pattern is separate
accounts for different currencies, business units, or cost centers.

### Billing Account Binding

A binding is the link between a project and the billing account that pays for
it. Bindings answer the question: *"where do charges for this project go?"*

Key properties:

- **One active binding per project.** A project has exactly one payer at any
  given time. This is enforced at the platform level.
- **Immutable once created.** You cannot change which account or project a
  binding refers to. To reassign a project, you create a new binding -- the
  old one is automatically marked as superseded.
- **Auditable history.** Superseded bindings are preserved, not deleted. This
  provides a complete timeline of billing responsibility for compliance and
  dispute resolution.
- **Account must be Ready.** You can only bind a project to a billing account
  that has been fully set up (has a payment profile, is not suspended or
  archived).

## User Journeys

### New Customer Onboarding

A consumer signs up for Datum Cloud and creates an organization. Before they
can start using services, they need to set up billing:

1. Create a billing account with their preferred currency
2. Attach a payment method (credit card)
3. Optionally configure payment terms and billing contact
4. Bind their project(s) to the billing account

At this point, the organization is ready to consume services and be charged
for usage.

### Reassigning a Project

A customer restructures their organization and needs to move a project from
one billing account to another (e.g., from the US billing account to the EU
one):

1. Create a new binding pointing the project to the new account
2. The old binding is automatically superseded
3. Charges for the project now go to the new account
4. The old binding remains in the system for audit purposes

### Account Suspension

A customer's payment method fails repeatedly. The service provider suspends
the billing account:

1. Account phase transitions to Suspended
2. Existing project bindings remain in place (services may be degraded but
   billing responsibility is preserved)
3. No new projects can be bound to the suspended account
4. Once the customer resolves the payment issue, the account is reactivated

### Account Closure

A customer leaves the platform:

1. All project bindings must be removed first (projects reassigned or deleted)
2. Outstanding charges must be settled
3. Account transitions to Archived
4. Historical data remains accessible for compliance and reporting

## What Comes Next

This initial scope establishes the billing account foundation. Future work
builds on top of it:

- **Entitlements** -- what services and features a billing account is entitled
  to consume, including pricing tiers (Free, Pro, Enterprise)
- **Usage pipeline** -- collecting and aggregating consumption metrics from
  the telemetry service for billing purposes
- **Invoicing** -- generating monthly invoices with line items for each service
  consumed, downloadable in PDF format
- **Payment processing** -- charging the payment profile for outstanding
  invoices, with retry logic and escalation for failures
- **Financial reporting** -- spend visibility across accounts, projects, and
  services for both service providers and consumers
