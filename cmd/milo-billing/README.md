# milo-billing

The [datumctl](https://github.com/datum-cloud/datumctl) billing plugin.
`datumctl billing usage` shows metered usage and spend for an organization or
project. It reads
`usage.billing.miloapis.com/v1alpha1` `UsageQuery` with your datumctl session,
the same API the cloud portal's usage page uses.

```sh
datumctl plugin install billing
```

## Usage

```sh
# Usage and spend for the current billing cycle
datumctl billing usage

# The previous billing cycle, for one project
datumctl billing usage --cycle previous --project my-project

# Break each meter down by project, or by any dimension the meter declares
datumctl billing usage --by project_name
datumctl billing usage --by region

# Usage per day over a custom range, as JSON
datumctl billing usage --daily --from 2026-10-01 --to 2026-10-15 -o json

# The meters usage is reported for, with their units and dimensions
datumctl billing meters
```

```text
Usage for organization acme, current billing cycle (1 Oct - 1 Nov 2026)

SERVICE      METER                                USAGE        SPEND
Compute      Compute Instance Memory Seconds      272 GB·h     $4.48
Compute      Compute Instance Milli-CPU Seconds   136 vCPU·h   $6.85
Networking   ALB Egress                           42.8 MB      $0
Networking   ALB Requests                         15,210       $0.02

Spend this period: $11.35
9 meters with no usage hidden; use --all to show them.
```

- **Scope.** With a project selected (in your context or with `--project`),
  the query is made in the project's context and only sees that project's
  usage. Otherwise it reads the organization's.
- **Billing cycles** come from the billing account's payment terms, like the
  portal's cycle picker; `--from`/`--to` read any range instead.
- **Spend** is priced the way the invoice prices it, before taxes, credits
  and discounts. It is per meter, so `--by` and `--daily` show usage only.
- **`-o json|yaml`** prints the API's `UsageQuery`, spec and status.

## Development

The plugin is its own Go module, so the datumctl SDK's dependencies don't
move the operator's; it builds against this repo's API types through a
`replace`.

```sh
cd cmd/milo-billing
go test ./...
go build -o milo-billing .

# Run it outside datumctl with the context datumctl would inject
DATUM_API_HOST=api.staging.env.datum.net \
DATUM_CREDENTIALS_HELPER="$(which datumctl)" \
DATUM_ORG=my-org \
  ./milo-billing
```

Publishing a GitHub release runs `.github/workflows/release-plugin.yaml`:
goreleaser attaches the per-platform archives, and a PR bumps
`plugins/billing.yaml` in the [milo-os/cli-plugins](https://github.com/milo-os/cli-plugins)
catalog. That action only updates an existing entry, so the first catalog
entry is added by hand.
