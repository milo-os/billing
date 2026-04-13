# Billing Service

A Kubernetes-native billing service for managing billing accounts and project bindings. Built with CRDs, controller-runtime, and validating webhooks.

## Architecture

- **CRD-based**: Uses kubebuilder v4 CRDs (not aggregated API server)
- **Controller-runtime**: Reconcilers for lifecycle management
- **Multicluster-aware**: Uses `sigs.k8s.io/multicluster-runtime` for cluster discovery
- **Webhooks**: Validating and defaulting webhooks for business rules

## API Group

- Group: `billing.miloapis.com`
- Version: `v1alpha1`
- Resources: `BillingAccount`, `BillingAccountBinding`

## Repo Layout

```
billing/
├── cmd/billing/main.go          # Binary entrypoint
├── api/v1alpha1/                 # CRD type definitions
├── internal/
│   ├── config/                   # Operator configuration
│   ├── controller/               # Reconcilers
│   ├── validation/               # Validation logic
│   └── webhook/v1alpha1/         # Admission webhooks
├── config/                       # Kustomize manifests
│   ├── base/                     # Core resources
│   ├── components/               # Optional components
│   └── overlays/                 # Environment-specific
├── hack/                         # Scripts and boilerplate
└── test/e2e/                     # Chainsaw E2E tests
```

## Key Design Decisions

1. **CRDs over aggregated API server** -- billing accounts are standard CRUD; no need for custom storage backends.
2. **Multicluster-aware controllers** -- reconcilers use `mcreconcile.Request` and `mcmanager.Manager`.
3. **Immutable bindings** -- BillingAccountBinding spec is immutable; re-binding creates a new binding and supersedes the old one.
4. **Finalizers for deletion safety** -- BillingAccount cannot be deleted while active bindings exist.
5. **Webhook + controller defense-in-depth** -- business rules enforced at admission (webhook) and reconciliation (controller).

## Reference Services

- **compute** (`go.datum.net/compute`) -- primary pattern reference for CRD/webhook/controller structure
- **quota** (`go.miloapis.com/quota`) -- reference for Taskfile, module naming, deployment patterns

## Verification Commands

```bash
task build                    # Build binary
task test                     # Run tests
task lint                     # Run linter
task generate                 # Run code generation
task manifests                # Generate CRD/RBAC/webhook manifests
```
