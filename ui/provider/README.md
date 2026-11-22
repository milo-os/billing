# Billing staff-portal plugin

A Module Federation remote that staff-portal's plugin host loads at runtime
(`app/modules/plugins/` there). It declares one extension today:

- **`portal.page/service`** at path `""` (`FleetOverview`,
  `src/pages/fleet-overview.tsx`): the reserved "replace the built-in
  Overview" convention, so the fleet-wide billing overview is the landing
  page of `/admin/service-catalog/billing-miloapis-com`. staff-portal keeps
  its built-in page (pricing, meters, quota, conditions) under a **Service
  details** nav item whenever a plugin overrides Overview.

Part of [milo-os/billing#134](https://github.com/milo-os/billing/issues/134).
The per-account pages under `/customers/billing-accounts` stay in
staff-portal for now: the plugin host has no customer-, org- or
account-scoped mount for a plugin to own them, so moving them first needs a
new extension type there.

## What the overview shows

Top to bottom, by how soon someone has to act on it:

Every section renders at once with its own title, its body a skeleton of
its content until the data arrives, as staff-portal's billing-account
dashboard does. Usage arrives last: it's a fan-out of queries.

1. **Needs attention**: past-due accounts; accounts with projects that pay
   by card but have nothing to charge; cards expired or expiring within 30
   days; payment terms applications waiting for review; invoice arrangements
   and credit grants ending within 30 days.
2. **Money**: collected and invoiced in the last 30 days (against the 30
   before), outstanding (open, not yet due), past due, collection rate (of
   invoices from the last 180 days that are paid or past due, the share
   paid), and DSO (outstanding ÷
   invoiced in the last 90 days × 90).
3. **Invoiced vs collected** by month for 12 months, and **receivables by
   age** (not yet due, 1–30, 31–60, 61–90, 90+ days), split card / invoice
   terms.
4. **Collections**: past-due invoices, biggest amount × days late first.
5. **Top accounts** by invoiced (12 months), lifetime paid or outstanding,
   with the provider's estimate of each one's current cycle
   (`BillingAccountBalance`).
6. **Usage this month** by service against the same point last month
   (`UsageQuery`), and **accounts** by phase, billing mode, offer, and
   active credit grants.

### Where the numbers come from

Everything is read in the browser through staff-portal's `/api/internal`
proxy with the staff member's own access; there is no plugin backend.

- `BillingAccount`, `Invoice`, `PaymentMethod`, `BillingArrangementApplication`,
  `BillingEntitlement`, `Offer`, `CreditGrant` and `Organization` are each
  listed across every organization in one paginated call on the platform API.
  Only the first three are required; without access to the others their
  sections hide.
- Invoices follow staff-portal's per-account rules (`src/lib/invoices.ts`):
  stale Amberflo and old-OpenMeter invoices are skipped, a missing phase
  counts as Open, and what's owed is `amountDue` (or `total` before the
  provider sets it). Invoices carry no issue date, so the period's end
  stands in for it.
- Money is in the currency most accounts bill in. Invoices in any other
  currency are counted in a note and left out, never summed across
  currencies.
- Usage: a platform-scope `UsageQuery` must name its billing accounts, at
  most 50, and merges them. The fleet is read in batches of 50 accounts that
  have projects, totals only, and meters roll up to services by the meter
  name's domain. Spend is what the provider prices from each account's
  plan: after the offer's discounts, before tax and credits. A 100% discount
  prices real usage at $0, so the page says when usage is recorded but
  unpriced rather than calling it "no usage".
- `BillingAccountBalance` has no cross-namespace list, so it is read one
  account at a time, only for the ten top accounts on screen.

### What it can't show

Milo has no payment attempts or decline reasons, no refunds, and no MRR or
subscription object, so failed-payment recovery, involuntary churn and MRR
movement aren't on the page. Committed recurring revenue becomes possible
once contract fees are on `BillingAccount.status`.

## Dependencies

The plugin renders inside staff-portal, which shares its own copies of
`react`, `react-dom`, `react-router`, `@tanstack/react-query` and the
datum-ui `badge`, `button`, `card`, `icons` and `skeleton` subpaths
(`federation-host.ts` there); those always win at runtime. So:

- Pin `react`, `react-dom`, `react-router`, `@tanstack/react-query`,
  `recharts` and `lucide-react` to the exact versions in staff-portal's
  `package-lock.json`, so types and behaviour here are what runs there.
  `recharts` 3.2 has no `responsive` prop: size charts with
  `ResponsiveContainer`, as staff-portal does.
- Keep `@datum-cloud/datum-ui` on the latest release. Shared subpaths
  render with staff-portal's version regardless, so only import datum-ui
  pieces that version already has.

## Local dev

Use Node 26 (`.nvmrc`) and pnpm 12 (`packageManager` in `package.json`).
Both are enforced: installing on an older Node, or with npm, yarn or bun,
fails (`engines` and `devEngines`, `engineStrict` in `pnpm-workspace.yaml`,
and a `preinstall` check). Node 25+ doesn't ship corepack, so install pnpm
once with `npm install --global pnpm@12`, from outside this directory.

```
pnpm install
pnpm test             # the fleet maths (src/lib/*.test.ts), with vitest
pnpm run typecheck
pnpm run build && pnpm run preview   # built dist/ at :5299, for staff-portal
pnpm run dev          # standalone harness at :5299
```

The container serves `dist/` with `serve.ts`, a dependency-free Node HTTPS
server that Node 26 runs as TypeScript directly. To try it locally, point it
at a cert and key: `TLS_DIR=./certs PORT=8443 pnpm run serve`.

The standalone harness has no staff-portal proxy, so data calls fail. Open
`http://localhost:5299/?fixtures` to answer them from a made-up fleet
(`src/lib/fixtures.ts`). It also has none of the host's CSS, so check
styling in staff-portal.

To load it in a local staff-portal, serve the **built** plugin (`dev` emits
host-absolute chunk URLs that 404 through staff-portal's asset proxy) and
register it with `PORTAL_PLUGINS_JSON` (the simple `PORTAL_PLUGINS` form
can't carry the `serviceRef` a service page needs):

```
PORTAL_PLUGINS_JSON='[{"slug":"billing-miloapis-com","assets":{"baseURL":"http://localhost:5299"},"serviceRef":{"name":"billing-miloapis-com"}}]' npm run dev
```

then open `/admin/service-catalog/billing-miloapis-com`.

## Deployment

- Image `ghcr.io/milo-os/billing-provider-ui-plugin`, built from this
  directory by `.github/workflows/publish.yaml` and stamped into the
  `billing-kustomize` bundle.
- `config/components/provider-ui-plugin`: a Deployment and Service serving
  `dist/` over in-cluster TLS (`serve.ts`, cert-manager CSI). Infra adds it
  to the billing manager's Flux Kustomization.
- `config/milo/service-configuration.yaml` sets
  `spec.userInterface.provider.assets.baseURL` to the Service's in-cluster
  URL. service-catalog fans that out to a `ProviderPortalPlugin`, which
  staff-portal watches. staff-portal fetches the assets server-side and
  already trusts `datum-control-plane-ca`, so there's no `caBundle`. In a new
  environment, deploy the component before this registration reaches it, or
  staff-portal's billing Overview points at a host that doesn't exist.
