# Provider Readiness Probes

Provider probes turn a pinned Terraform provider, provider source tree, and
published OpenAPI document into repeatable onboarding evidence.

They do not use provider credentials. The probe only answers whether the
provider schema, Go source evidence, and published API contract line up well
enough to investigate a provider without starting from manual triage.

## Recipe Shape

Recipes live under `docs/recipes/providers/` and pin:

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
The probe coordinator, source mapper, OpenAPI mapper, artifact renderer, and
CLI are Node 24 code; Ruby is used only for the existing safe YAML-to-JSON
conversion and Python is not part of the probe execution path.

## Running

```bash
make provider-probe RECIPE=docs/recipes/providers/github.json
make provider-probe RECIPE=docs/recipes/providers/digitalocean.json
```

Set `PYTHON` to a failing tripwire when qualifying the migrated path; neither
the Make target nor `infrawright provider-probe` consults it.

By default, outputs are written under:

```text
local/provider-probes/<provider>/artifacts/
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
  RECIPE=docs/recipes/providers/github.json \
  WORK_DIR=local/provider-probes/github \
  OUT=reports/provider-probes/github-summary.json \
  MARKDOWN=reports/provider-probes/github-summary.md
```

Keep `local/provider-probes/` and any generated `reports/provider-probes/`
outputs uncommitted unless a PR is explicitly adding sanitized evidence.

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
