# Billing Service

The Billing Service manages how organizations pay for the services they use on the Milo platform. It provides billing accounts, links projects to those accounts, and enforces the business rules that keep billing data consistent and reliable.

## Why This Exists

Milo is a business operating system for B2B service providers -- a control plane that helps them build, price, sell, and operate managed services. Billing is a foundational piece of Milo's **Monetize** horizon, connecting service consumption to payment. Without it, there's no way to charge for the resources organizations use.

This service owns two core concepts:

- **Billing Accounts** -- the entity that receives charges. An organization can have multiple billing accounts (e.g., for different currencies or business units). Each account tracks its payment profile, payment terms, and lifecycle phase.

- **Billing Account Bindings** -- the link between a project and the billing account responsible for paying for it. Every project that consumes resources must be bound to a billing account. Bindings are immutable; re-assigning a project to a different account creates a new binding and supersedes the old one.

## How It Fits Into Milo

```
Organization
├── Billing Account(s)        ← this service
│   └── Payment profile, terms, currency
├── Project A
│   └── Binding → Billing Account   ← this service
├── Project B
│   └── Binding → Billing Account   ← this service
└── ...
```

Upstream, the **Service Catalog** and **Entitlements** determine what services a project can access and at what tier. Downstream, **Usage Pipeline**, **Invoicing**, and **Payment Processing** use billing account data to meter consumption, generate invoices, and collect payment.

This service sits in the middle: it answers the question *"who pays for this project?"* so that every other billing-related system has a clear, authoritative answer.

## Key Behaviors

- A billing account progresses through lifecycle phases (Provisioning, Ready, Suspended, Archived) based on whether a valid payment profile is attached.
- A project can only be bound to a billing account that is in the Ready phase.
- A billing account cannot be deleted while it still has active project bindings.
- Each project has exactly one active billing account binding at a time.

## Development

```bash
task build       # Build the binary
task test        # Run tests
task lint        # Run linter
task generate    # Run code generation
task manifests   # Generate CRD, RBAC, and webhook manifests
```
