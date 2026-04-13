# Billing Service: User Onboarding & Billing Accounts

## Context

Enhancement PR datum-cloud/enhancements#539 defines functional requirements for billing Datum Cloud consumers. PR datum-cloud/enhancements#253 details the BillingAccount and BillingAccountBinding resource design. The billing service directory is currently empty.

This plan bootstraps the billing service using **CRDs with controller-runtime** (not an aggregated API server). The resources are standard CRUD with lifecycle controllers and admission validation -- a natural fit for the CRD pattern.

**Primary reference**: `compute` service -- kubebuilder v4, CRDs, webhooks, controller-runtime, multicluster support.

---

## Key Decisions

| Decision | Value | Rationale |
|----------|-------|-----------|
| Module path | `go.miloapis.com/billing` | Follows `go.miloapis.com/quota` convention |
| API group | `billing.miloapis.com` | Matches `quota.miloapis.com` convention |
| API version | `v1alpha1` | Standard Kubernetes alpha version convention |
| Domain | `miloapis.com` | For kubebuilder PROJECT file |
| Build system | Taskfile.yaml | Matches quota service pattern |
| Framework | controller-runtime + kubebuilder | CRDs, webhooks, reconcilers |
| Go version | 1.24.0 | Matches compute service |

## Resources

| Resource | Scope | Purpose |
|----------|-------|---------|
| `BillingAccount` | Namespaced (org) | Payment profile, contacts, currency, payment terms |
| `BillingAccountBinding` | Namespaced (org) | Links a project to a billing account |

---

## Architectural Notes

**Multicluster-aware controllers**: Following the compute service pattern, all controllers use `mcreconcile.Request` (multicluster request), `mcmanager.Manager`, and `mcbuilder.ControllerManagedBy(mgr)`. The reconciler stores a reference to `mcmanager.Manager` to resolve cluster context via `mgr.GetCluster(ctx, req.ClusterName)`. Webhooks register via `mgr.GetLocalManager()`.

**Taskfile with controller-gen**: This is a hybrid of the quota Taskfile structure and compute's controller-gen usage. The `generate` task runs `controller-gen object` for deepcopy generation. The `manifests` task runs `controller-gen rbac crd webhook` for CRD/RBAC/webhook manifest generation. Neither existing Taskfile does exactly this combination, so these tasks are defined fresh.

---

## Phase 1: Repository Skeleton

Compilable binary with controller-runtime manager, no CRDs yet.

**Create:**
- `PROJECT` -- kubebuilder v4 project config (domain: `miloapis.com`, repo: `go.miloapis.com/billing`)
- `go.mod` -- module `go.miloapis.com/billing`, Go 1.24.0, deps: controller-runtime v0.21.0, k8s.io/apimachinery v0.33.2, multicluster-runtime v0.21.0-alpha.8
- `cmd/billing/main.go` -- controller-runtime manager entrypoint following compute's pattern (scheme init, flag parsing, config loading, errgroup, multicluster provider, health probes). Binary will be named `billing`.
- `internal/config/config.go` -- operator config struct (`BillingOperator`) with MetricsServer, WebhookServer, Discovery fields
- `internal/config/groupversion_info.go` -- config scheme registration
- `internal/config/zz_generated.deepcopy.go` -- generated
- `internal/config/zz_generated.defaults.go` -- generated
- `Taskfile.yaml` -- tasks: generate, manifests, build, test, lint, dev:build, dev:load, dev:setup, dev:deploy, dev:redeploy, e2e (adapted from quota Taskfile, using controller-gen for CRD/RBAC/webhook generation)
- `Dockerfile` -- multi-stage golang:1.24 -> distroless, builds `cmd/billing/main.go` to binary named `billing`
- `hack/boilerplate.go.txt` -- AGPL license header
- `.golangci.yml` -- linting config
- `CLAUDE.md` -- architecture decisions and repo layout

**Verify:** `task build` compiles; `./bin/billing` starts and serves health probes.

---

## Phase 2: BillingAccount CRD Types

Define the BillingAccount API type with kubebuilder markers.

**BillingAccount spec:**
- `currencyCode` (string, required, ISO 4217 -- immutable after Ready via XValidation)
- `paymentTerms` (optional): `netDays` (int32), `invoiceFrequency` (enum: Monthly/Quarterly/Annual), `invoiceDayOfMonth` (int32, 1-28)
- `paymentProfile` (optional): `type` (string, e.g. "CreditCard"), `externalID` (string)
- `contactInfo` (optional): `email` (string), `name` (string)

**BillingAccount status:**
- `phase` (enum: Provisioning/Ready/Incomplete/Suspended/Archived)
- `conditions` ([]metav1.Condition) -- condition types: `Ready`
- `linkedProjectsCount` (int32)
- `observedGeneration` (int64)

**Create:**
- `api/v1alpha1/groupversion_info.go` -- `+groupName=billing.miloapis.com`, SchemeBuilder, AddToScheme
- `api/v1alpha1/billingaccount_types.go` -- BillingAccount + BillingAccountList with kubebuilder markers (`+kubebuilder:object:root=true`, `+kubebuilder:subresource:status`, `+kubebuilder:printcolumn` for phase/currency/projects, `+kubebuilder:validation` on fields)
- `api/v1alpha1/zz_generated.deepcopy.go` -- generated via `task generate`

**Update:**
- `cmd/billing/main.go` -- register billing scheme in `init()`
- `PROJECT` -- add BillingAccount resource entry

**Verify:** `task generate && task manifests` produces CRD YAML at `config/base/crd/bases/billing.miloapis.com_billingaccounts.yaml`; `task build` compiles.

---

## Phase 3: BillingAccountBinding CRD Types

Define the BillingAccountBinding API type.

**BillingAccountBinding spec:**
- `billingAccountRef` (required): `name` (string) -- immutable via XValidation
- `projectRef` (required): `name` (string) -- immutable via XValidation

**BillingAccountBinding status:**
- `phase` (enum: Active/Superseded)
- `conditions` ([]metav1.Condition) -- condition types: `Bound`
- `billingResponsibility`: `establishedAt` (*metav1.Time), `currentAccount` (string)
- `observedGeneration` (int64)

**Create:**
- `api/v1alpha1/billingaccountbinding_types.go` -- BillingAccountBinding + BillingAccountBindingList with markers (`+kubebuilder:object:root=true`, `+kubebuilder:subresource:status`, printcolumns for phase/account/project, XValidation for spec immutability on update)

**Update:**
- `PROJECT` -- add BillingAccountBinding resource entry

**Verify:** `task generate && task manifests` produces both CRD YAMLs; `task build` compiles.

---

## Phase 4: BillingAccount Controller

Controller that manages BillingAccount phase transitions and linkedProjectsCount.

**Reconcile logic:**
1. Get the BillingAccount
2. Phase transitions (all valid state changes):
   - Provisioning + paymentProfile set -> Ready
   - Provisioning + no paymentProfile -> Incomplete
   - Incomplete + paymentProfile added -> Ready
   - Ready + paymentProfile removed -> Incomplete
   - Ready/Incomplete -> Suspended (triggered by external status update, e.g., payment failure)
   - Suspended -> Ready (triggered by external status update after issue resolved)
   - Ready/Incomplete/Suspended -> Archived (triggered by external status update; pre-condition: no active bindings)
3. **Finalizer**: Add a finalizer to prevent deletion while active BillingAccountBindings reference this account. On finalization, check for active bindings and block deletion if any exist.
4. List active BillingAccountBindings referencing this account (via field index on `spec.billingAccountRef.name`) -> update `status.linkedProjectsCount`
5. Set `Ready` condition based on phase
6. Update `status.observedGeneration`

**Note on Suspended/Archived transitions**: For the initial implementation, Suspended and Archived transitions are triggered by external status updates (e.g., an admin or payment system updating the status subresource). Automated suspension on payment failure will be implemented when invoicing/payments are added.

**Create:**
- `internal/controller/billingaccount_controller.go` -- reconciler with `SetupWithManager`, RBAC markers
- `internal/controller/indexers.go` -- field indexers for `spec.billingAccountRef.name` and `spec.projectRef.name` on BillingAccountBinding

**Update:**
- `cmd/billing/main.go` -- register controller with manager

**Verify:** `task build && task test`; create BillingAccount without paymentProfile (phase=Incomplete); add paymentProfile (phase=Ready); condition reflects state.

---

## Phase 5: BillingAccountBinding Controller

Controller that manages binding lifecycle and superseding logic.

**Reconcile logic:**
1. Get the BillingAccountBinding
2. If phase is Superseded or being deleted, skip
3. Set `billingResponsibility.establishedAt` if not already set (to binding creation time)
4. Set `billingResponsibility.currentAccount` from spec
5. List other Active bindings for the same `spec.projectRef.name` (via field index)
6. For each older Active binding (by creation timestamp): set phase=Superseded
7. Set `Bound` condition to True
8. Enqueue the referenced BillingAccount for reconciliation (to update linkedProjectsCount)

**Create:**
- `internal/controller/billingaccountbinding_controller.go` -- reconciler with `SetupWithManager`, RBAC markers, watches BillingAccountBinding with enqueue for BillingAccount

**Update:**
- `cmd/billing/main.go` -- register controller with manager

**Verify:** Create account + binding (Bound condition=True, phase=Active); create second binding for same project (old one Superseded, new one Active); account linkedProjectsCount updates.

---

## Phase 6: Validating Webhook

Webhook enforcing cross-resource business rules on BillingAccountBinding create.

**Validation rules (on CREATE):**
### BillingAccount Webhook

**Defaulting (on CREATE):**
- Set default `paymentTerms` if not provided (e.g., netDays=30, invoiceFrequency=Monthly, invoiceDayOfMonth=1)

**Validation rules (on CREATE):**
- `currencyCode` must be a valid ISO 4217 code
- If `paymentProfile` is set, `type` must be non-empty
- If `contactInfo` is set, `email` must be valid format

**Validation rules (on UPDATE):**
- `currencyCode` is immutable once phase has transitioned past Provisioning

**Validation rules (on DELETE):**
- Reject deletion if active BillingAccountBindings reference this account (belt-and-suspenders with the controller finalizer)

### BillingAccountBinding Webhook

**Validation rules (on CREATE):**
1. **Account readiness**: Referenced BillingAccount must exist in same namespace and have phase=Ready
2. **Single binding**: No other Active BillingAccountBinding for the same `spec.projectRef.name` in the namespace
3. **Organization boundary**: BillingAccount referenced must be in the same namespace (enforced by the namespaced ref pattern -- billingAccountRef.name is namespace-local)

**Validation rules (on UPDATE):**
- Reject any spec changes (immutability enforced by XValidation markers on CRD, but belt-and-suspenders in webhook)

**Create:**
- `internal/webhook/v1alpha1/billingaccount_webhook.go` -- implements `admission.CustomDefaulter` and `admission.CustomValidator`
- `internal/webhook/v1alpha1/billingaccountbinding_webhook.go` -- implements `admission.CustomValidator`
- `internal/validation/billingaccount.go` -- BillingAccount validation logic (testable independent of webhook)
- `internal/validation/billingaccount_test.go` -- table-driven tests
- `internal/validation/billingaccountbinding.go` -- BillingAccountBinding validation logic (testable independent of webhook)
- `internal/validation/billingaccountbinding_test.go` -- table-driven tests for each rule

**Update:**
- `cmd/billing/main.go` -- register both webhooks with manager (guarded by `webhookServer != nil` check, matching compute pattern)
- `PROJECT` -- add webhook config for both resources

**Verify:** `task manifests` generates webhook config at `config/base/webhook/manifests.yaml`; `task test` passes validation tests; integration: create Ready account + binding (succeeds); duplicate binding for same project (rejected); binding to non-Ready account (rejected); delete account with active bindings (rejected).

---

## Phase 7: Kustomize Configuration

Kubernetes deployment manifests following compute's kustomize structure.

**Create:**
- `config/base/crd/kustomization.yaml` + `config/base/crd/kustomizeconfig.yaml` -- CRD kustomization with caBundle injection
- `config/base/crd/bases/` -- generated CRD YAMLs (from `task manifests`)
- `config/base/webhook/kustomization.yaml` + `config/base/webhook/kustomizeconfig.yaml` -- webhook config
- `config/base/webhook/manifests.yaml` -- generated ValidatingWebhookConfiguration
- `config/base/manager/kustomization.yaml` -- manager deployment, service, service account
- `config/base/manager/manager.yaml` -- Deployment spec
- `config/base/manager/service.yaml` -- Service for webhook
- `config/base/manager/service_account.yaml`
- `config/base/manager/config.yaml` -- default BillingOperator config
- `config/base/certmanager/kustomization.yaml` -- cert-manager Certificate + Issuer for webhook TLS
- `config/components/controller_rbac/` -- ClusterRole, ClusterRoleBinding, metrics auth (generated from RBAC markers + manual additions)
- `config/components/leader_election/` -- leader election Role + RoleBinding
- `config/components/iam/` -- ProtectedResource definitions + IAM roles for billing resources
- `config/overlays/dev/kustomization.yaml` -- composes base + components for local dev
- `config/overlays/dev/config.yaml` -- dev config (single cluster, webhooks enabled)
- `config/samples/` -- sample BillingAccount and BillingAccountBinding YAMLs

**Verify:** `kustomize build config/overlays/dev` renders valid YAML; CRDs apply to cluster; full onboarding flow works.

---

## Phase 8: Tests

**Create:**
- `internal/controller/billingaccount_controller_test.go` -- envtest-based tests for phase transitions, linkedProjectsCount
- `internal/controller/billingaccountbinding_controller_test.go` -- envtest-based tests for superseding, status updates
- `internal/validation/billingaccountbinding_test.go` -- unit tests for validation rules (account readiness, single binding, immutability)
- `internal/controller/suite_test.go` -- envtest suite setup (shared across controller tests)

**Verify:** `task test` passes all tests.

---

## End-to-End Onboarding Flow

```
1. Consumer creates BillingAccount in their org namespace
   -> controller sets phase: Incomplete (no payment profile)
   -> Ready condition: False

2. Consumer updates account with paymentProfile + contactInfo
   -> controller transitions phase: Ready
   -> Ready condition: True

3. Consumer creates BillingAccountBinding (projectRef + billingAccountRef)
   -> webhook validates: account is Ready, no existing Active binding for project
   -> controller sets phase: Active, Bound condition: True
   -> BillingAccount controller increments linkedProjectsCount

4. (Optional) Consumer re-binds project to different account
   -> webhook validates: new account is Ready
   -> controller supersedes old binding (phase: Superseded)
   -> new binding becomes Active
```

---

## Files Summary

```
billing/
├── PROJECT
├── go.mod / go.sum
├── Taskfile.yaml
├── Dockerfile
├── CLAUDE.md
├── .golangci.yml
├── hack/boilerplate.go.txt
├── cmd/
│   └── billing/
│       └── main.go
├── api/
│   └── v1alpha1/
│       ├── groupversion_info.go
│       ├── billingaccount_types.go
│       ├── billingaccountbinding_types.go
│       └── zz_generated.deepcopy.go
├── internal/
│   ├── config/
│   │   ├── config.go
│   │   ├── groupversion_info.go
│   │   ├── zz_generated.deepcopy.go
│   │   └── zz_generated.defaults.go
│   ├── controller/
│   │   ├── billingaccount_controller.go
│   │   ├── billingaccount_controller_test.go
│   │   ├── billingaccountbinding_controller.go
│   │   ├── billingaccountbinding_controller_test.go
│   │   ├── indexers.go
│   │   └── suite_test.go
│   ├── validation/
│   │   ├── billingaccount.go
│   │   ├── billingaccount_test.go
│   │   ├── billingaccountbinding.go
│   │   └── billingaccountbinding_test.go
│   └── webhook/
│       └── v1alpha1/
│           ├── billingaccount_webhook.go
│           └── billingaccountbinding_webhook.go
├── config/
│   ├── base/
│   │   ├── crd/
│   │   ├── webhook/
│   │   ├── manager/
│   │   └── certmanager/
│   ├── components/
│   │   ├── controller_rbac/
│   │   ├── leader_election/
│   │   └── iam/
│   ├── overlays/
│   │   └── dev/
│   └── samples/
└── docs/
```
