# infrawright

**Driftless infrastructure management across providers.**

infrawright reads a live provider's resources and emits modular Terraform / OpenTofu
that **imports what already exists without recreating it** — typed `map(object)`
variables, native `import {}` blocks, and **identity-keyed `moved {}` reconciliation**
so console renames and key changes resolve as *moves*, never destroy/recreate. A clean
`terraform plan` against your real state is the contract.

The engine carries **zero vendor knowledge**. Each provider is a **pack** under
`packs/<name>/` supplying its own collector, registry, overrides, and schema — the same
engine drives any provider. Zscaler is the reference pack; Cloudflare, Google,
AWS, and NetBox provide additional provider-lab and metadata evidence.

## Why it's safe to point at production

The fragile part of adopting IaC over live infrastructure is the Terraform **state** — a
resource key that shifts between runs reads as *destroy + recreate*, which on a real
tenant is an outage, not a diff. infrawright is built to keep the state stable:

- **Stable identity-derived keys** — the same live resource maps to the same `["key"]`
  every run, so its state address never moves.
- **Automatic `moved {}` reconciliation** — when a key *does* change, it's emitted as a
  move, not a recreate.
- **Deterministic, verified output** — `make check` proves the committed demo
  config/import artifacts do not drift and that the module generator still
  renders every resource type.

The acceptance bar isn't "0 to change" — it's **0 to destroy, 0 to create** after import.

## Layout

| Path | Role |
|------|------|
| `engine/` | vendor-agnostic: transform → modular TF + `import` + `moved` reconciliation; includes the shared REST collector |
| `packs/<name>/` | a provider bundle: `pack.json` + `registry.json` + `overrides/` + `schemas/` + collector |
| `[<overlay>/]config/<tenant>/<resource_type>.auto.tfvars.json` | generated tenant config |
| `[<overlay>/]imports/<tenant>/<resource_type>_imports.tf` | generated import blocks |
| `[<overlay>/]envs/<tenant>/<resource_type>/` | generated per-resource Terraform roots |
| `<module_dir>/<resource_type>/` | generated Terraform modules for the selected deployment module set |

There is one generated output layout. `overlay` is an optional free-form prefix
owned by the adopter. The shipped `deployment.json` points at the `demo/`
overlay, so demo artifacts live under `demo/config/demo` and
`demo/imports/demo` while real deployments can choose their own overlay prefix.
Generated env roots resolve module sources from deployment-configured
`module_dir`; the shipped demo generates `demo/modules/default` on demand.
See [Repository Surface](docs/repo-surface.md) for the keep/prune policy across
core, demo, packs, tools, release scripts, and archived docs.

The root `Makefile` is the stable product command surface. Deployment-specific
workflow targets can live in optional extension Makefiles:

```
local.mk
<overlay>/Makefile
<overlay>/local.mk
```

Those files are included when present. The shipped demo uses `demo/Makefile` for
demo-owned example workflows without making them part of the root command
contract. `make check-demo` pins `demo/deployment.json` so the shipped demo can
still be verified even when a local deployment points somewhere else.

Only one overlay is active for a command. Use separate deployment files for
separate domains, such as `overlays/zscaler/deployment.json`,
`overlays/aws/deployment.json`, and `overlays/gcp/deployment.json`, then run
with the matching `OVERLAY` and `DEPLOYMENT`. Infrawright does not compose
multiple overlays in one run. See
[Adoption Command Surface](docs/adoption-command-surface.md) for the command
contract and collector boundary.

## Quickstart

```
make check      # full gate: unit tests + demo drift check + module generator smoke
make demo       # materialize the demo tenant (no credentials needed)
```

## Provider Readiness

Before a new pack graduates, compare raw API readback against the Terraform
provider schema and make every observed field explainable. Start with the
surface map, because one Terraform provider can span multiple API products:

```
make openapi-map \
  SCHEMA=tmp/netbox-provider-schema.json \
  OPENAPI=tmp/netbox-openapi.json \
  PROVIDER_SOURCE=registry.terraform.io/e-breuninger/netbox \
  RESOURCE_PREFIX=netbox \
  OUT=reports/readiness/netbox-openapi-map.json

make source-operation-map \
  SCHEMA=tmp/grafana-core-schema.json \
  OPENAPI=tmp/grafana-api-merged.json \
  SOURCE_ROOT=tmp/terraform-provider-grafana \
  PROVIDER_SOURCE=registry.terraform.io/grafana/grafana \
  RESOURCE_PREFIX=grafana \
  OUT=reports/readiness/grafana-core-read-registry.json

make reconcile \
  RESOURCE=netbox_site \
  IN=pulls/netbox/netbox_site.json \
  SCHEMA=tmp/netbox-provider-schema.json \
  OPENAPI=tmp/netbox-openapi.json \
  OPENAPI_READ='GET:/api/dcim/sites/{id}/' \
  OPENAPI_WRITE='POST:/api/dcim/sites/ PUT:/api/dcim/sites/{id}/' \
  OUT=reports/reconcile/netbox_site.json \
  STRICT=1
```

`openapi-map` gives the denominator first: every Terraform resource is
classified as `matched`, `special`, `ambiguous`, or `unmatched` against
published API endpoints, with static write-field gap candidates and coverage
warnings for wrong or partial API surfaces. When a pack registry exists, also
read `registry_fetch_coverage`: it checks the pack's actual `fetch.path` values
against OpenAPI GET paths and is the stronger signal for the currently
fetch-backed surface. For providers that do not have a pack yet,
`source-operation-map` can derive a temporary read registry from Go provider
source files that call generated OpenAPI clients. Read `registry_read_coverage`
for that source-backed evidence; read `registry_fetch_coverage` only for real
pack enumeration paths. Source evidence entries carry hop chains so a future Go
source analyzer can emit the same JSON contract with stronger AST-backed
evidence. `special` covers non-CRUD
resources such as parent-scoped allocation actions and parent-field
relationship assignments.
`reconcile` then classifies observed API paths as Terraform inputs,
override-driven renames/drops, computed-only provider state, API
response-only/read-only fields, or unknown API surface. Published
OpenAPI/Swagger specs should be the first metadata source; NetBox/DRF-style
`API_OPTIONS` files are also supported when available. See
[Provider Readiness](docs/provider-readiness.md) for the surface-router workflow
and the Grafana/Zscaler lessons.

Writable API fields missing from the Terraform schema are highlighted under
`suggestions.provider_gaps`. `STRICT=1` fails while unknown or shape-mismatched
paths remain, so pack authors only adjudicate the small unresolved set instead
of manually sifting whole API bodies. Live import/plan evidence comes after the
published API pass, mainly to prove provider semantics or produce actionable
upstream evidence when the published contract is wrong.

Provider readiness should also grow a generated surface-coverage index: resource
inventories come from provider schema, surfaces are classified by small
declarative rules, and live/static evidence files determine whether each
resource is `static_mapped`, `read_observed`, `write_sampled`,
`import_verified`, `not_tested_paid`, `not_tested_risky`, or `not_applicable`.
Humans classify surfaces; tools classify resources and evidence so the index
does not drift by hand.

## Import Oracle Adoption

The adoption path can use Terraform/OpenTofu import as a provider-state oracle:

```
make adopt IN=pulls/<tenant> TENANT=<tenant> RESOURCE=<type>
```

See [docs/import-oracle.md](docs/import-oracle.md) for the workflow, OpenTofu
usage, and consumer drift policy format.

## Status

**0.1 — Zscaler** (`zia` · `zpa` · `zcc`): reproduces its demo tenant byte-identically
through the agnosticized engine; provider-first packs; identity-keyed reconciliation.
Additional provider labs and metadata cover Cloudflare, Google, AWS, NetBox,
and Grafana evidence without making those packs production-certified.

## License

[FSL-1.1-Apache-2.0](LICENSE) — source-available; converts to Apache 2.0 two years after
each release.
