# Provider Readiness Probes

Provider probes turn a pinned Terraform provider, provider source tree, and
published OpenAPI document into repeatable onboarding evidence.

They do not use provider credentials. The probe only answers whether the
provider schema, Go source evidence, and published API contract line up well
enough to investigate a provider without starting from manual triage.

## Recipe Shape

Recipes live under `recipes/providers/` and pin:

- `provider_source` and `provider_version`: the Terraform provider schema to
  inspect.
- `source`: the matching provider repository and tag, or a local source path.
- `openapi`: the published OpenAPI document, or a local OpenAPI path.
- `resource_prefix` and `api_prefix`: provider-specific matching hints.

Remote OpenAPI URLs should point at immutable commits or released artifacts, not
floating branches. The provider schema and source tree are version-pinned, so
the API contract input should be pinned as well or probe results can drift
without a recipe change.

If `terraform_schema.path` is omitted, the probe renders a temporary Terraform
configuration and runs:

```bash
terraform init -backend=false
terraform providers schema -json
```

YAML OpenAPI specs are converted to JSON with Ruby's standard YAML support.

## Running

```bash
make provider-probe RECIPE=recipes/providers/github.json
make provider-probe RECIPE=recipes/providers/digitalocean.json
```

By default, outputs are written under:

```text
/tmp/infrawright-provider-probes/<provider>/artifacts/
```

The important artifacts are:

- `summary.md`: human-readable probe result.
- `summary.json`: compact machine-readable summary.
- `source-registry.json`: source-derived read/list evidence.
- `source-diagnostics.json`: mapper diagnostics for mapped, ambiguous, and
  unmapped resources.
- `openapi-map.json`: full generic and registry-backed OpenAPI coverage report.
  Its `surface_map` section is the stable resource-to-API surface contract.

Use `WORK_DIR`, `OUT`, and `MARKDOWN` to copy summaries somewhere explicit:

```bash
make provider-probe \
  RECIPE=recipes/providers/github.json \
  WORK_DIR=/tmp/infrawright-provider-probes/github \
  OUT=/tmp/github-probe-summary.json \
  MARKDOWN=/tmp/github-probe-summary.md
```

## Reading Results

Treat `registry_read_coverage` as the headline OpenAPI signal because it is
backed by provider source evidence. Treat `generic_openapi_map` as candidate
generation only.

For machine consumption, prefer `openapi-map.json.surface_map.records`: it keeps
generic CRUD candidates, curated fetch paths, and source-derived read paths as
separate evidence records with stable `match_status` values.

Ambiguous and unmapped resources are not hidden. They mean the source evidence
collector could not identify one clear read operation, or the selected path did
not exist in the OpenAPI document. Those buckets are where adapter work should
start.
