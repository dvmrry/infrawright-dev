# Provider Readiness Probes

Provider probes turn a pinned Terraform provider, provider source tree, and
published OpenAPI document into repeatable onboarding evidence.

They do not use provider credentials. The probe only answers whether the
provider schema, Go source evidence, and published API contract line up well
enough to investigate a provider without starting from manual triage.

## Recipe Shape

A probe recipe is a local JSON file whose `source_provenance` section embeds
the qualified source-provenance manifest (authoring artifact contract §3.2.1):
a pinned provider revision with per-file SHA-256 bindings, go.mod/go.sum
bindings, the Terraform schema binding, every analyzed SDK tree binding, the
resource selection, and an optional pinned OpenAPI document binding. Recipes
without `source_provenance` are rejected: the legacy download-and-clone
contract, which resolved floating inputs at probe time, was retired together
with the proof-of-concept recipes that used it. The repository intentionally
commits no recipes; probes run against local, explicitly pinned checkouts.

YAML handling, network fetching, and Terraform schema capture from the legacy
contract are gone with it: every input reaches the probe as a local file bound
by the manifest.

## Running

```bash
make provider-probe \
  RECIPE=local/provider-probes/example/recipe.json \
  WORK_DIR=local/provider-probes/example \
  OUT=reports/provider-probes/example-summary.json \
  MARKDOWN=reports/provider-probes/example-summary.md
```

`WORK_DIR` is required; the sealed artifact set is published under
`<WORK_DIR>/artifacts/`. The important artifacts are:

- `summary.md`: human-readable probe result.
- `summary.json`: compact machine-readable summary.
- `source-registry.json`: source-derived read/list evidence.
- `source-diagnostics.json`: mapper diagnostics for mapped, ambiguous, and
  unmapped resources.
- `input-provenance.json`: the verified input bindings the evidence was
  computed from.
- `openapi-diagnostics.json`: OpenAPI document state and conflicts.
- `openapi-map.json` (optional): generic and registry-backed OpenAPI coverage
  when the recipe binds an OpenAPI document.

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
