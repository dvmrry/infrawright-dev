# infrawright

**Driftless infrastructure management across providers.**

infrawright reads a live provider's resources and emits modular Terraform / OpenTofu
that **imports what already exists without recreating it** — typed `map(object)`
variables, native `import {}` blocks, and **identity-keyed `moved {}` reconciliation**
so console renames and key changes resolve as *moves*, never destroy/recreate. A clean
`terraform plan` against your real state is the contract.

The core adoption/codegen contract is provider-agnostic: provider-specific
enumeration, identity, schema, and diagnostic metadata live in **packs** under
`packs/<name>/`. Zscaler is the reference pack; Cloudflare, Google, AWS, and
NetBox provide additional provider-lab and metadata evidence. Engine-edge
vendor behavior belongs in pack metadata rather than the generic runtime.

## Primary Adoption Workflow

Use the import-oracle path when Terraform/OpenTofu provider state should be the
configuration truth:

```bash
make fetch TENANT=<tenant> RESOURCE=<resource-or-provider>
make adopt IN=pulls/<tenant> TENANT=<tenant> RESOURCE=<resource-or-provider>
make gen-modules RESOURCE=<resource-or-provider>  # selected grouped roots include every member
make gen-env TENANT=<tenant> RESOURCE=<resource-or-provider>
make stage-imports TENANT=<tenant> RESOURCE=<resource-or-provider>
make plan TENANT=<tenant> RESOURCE=<resource-or-provider> SAVE=1
make assert-adoptable TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
make apply TENANT=<tenant> RESOURCE=<resource-or-provider> POLICY=<file>
```

What each step owns:

| Command | Responsibility |
|---|---|
| `make fetch` | Gathers raw provider/API evidence into `pulls/<tenant>`. |
| `make adopt` | Uses Terraform/OpenTofu import and provider state as the projection oracle, then writes config/import artifacts. |
| `make gen-modules` | Generates reusable Terraform modules; a selected grouped-root member expands to the complete root. |
| `make gen-env` | Generates isolated env roots that source the selected module set. |
| `make stage-imports` | Stages generated `import {}` and `moved {}` blocks into env roots. |
| `make plan SAVE=1` | Produces saved plan artifacts for the safety gates. |
| `make assert-adoptable` | Classifies saved plans as clean, explicitly policy-tolerated, or blocked. |
| `make apply` | Reclassifies saved plans and applies only clean/import-only or explicitly policy-tolerated plans. |

See [Adoption Command Surface](docs/adoption-command-surface.md) for the
complete command contract and collector boundary, and
[Import Oracle Adoption](docs/import-oracle.md) for the oracle workflow and
consumer drift-policy format. Use the
[Integration Validation Runbook](docs/integration-validation.md) when proving
the workflow against real provider credentials or a controlled tenant.

## Safety Model

The fragile part of adopting IaC over live infrastructure is the Terraform **state** — a
resource key that shifts between runs reads as *destroy + recreate*, which on a real
tenant is an outage, not a diff. infrawright is built to keep the state stable:

- **Stable identity-derived keys** — the same live resource maps to the same `["key"]`
  every run, so its state address never moves.
- **Durable `moved {}` reconciliation** — when a key *does* change, it is
  emitted as a move, not a recreate. Rerunning Transform or Adopt preserves
  unresolved move bytes; a different rename fails closed until an operator
  confirms the state migration and explicitly removes the old move artifact.
- **Deterministic, verified output** — `make check` proves the committed demo
  config/import artifacts do not drift and that the module generator still
  renders every resource type.
- **Provider state as adoption oracle** — raw API/read data supplies evidence,
  stable keys, and import IDs; Terraform/OpenTofu provider state supplies the
  projected configuration body.
- **Fail-loud unsafe cases** — sensitive, ambiguous, unsupported, or
  provider-blind surfaces block or report explicitly instead of being silently
  rendered.
- **Explicit policy only** — drift tolerance is consumer-owned policy; guidance
  annotations can explain blocked paths but cannot make blocked plans clean.

The acceptance bar isn't "0 to change" — it's **0 to destroy, 0 to create** after import.

High-risk agent-built changes to evidence, provider-readiness, OpenAPI mapping,
or generated artifacts use the
[Adversarial Review Workflow](docs/adversarial-review.md) before final
acceptance.

## Quickstart

```bash
make check      # Python-independent Node/runtime gate
make demo       # materialize the demo tenant (no credentials needed)
make demo-contract  # credential-free demo artifact/module contract check
```

During the Python archive window, `npm run check:all` additionally runs the
retained Python-oracle migration differentials. Python is not required by the
operational CLI.

### Runtime requirements

Node 24 and Terraform/OpenTofu are required for the operational adoption
runtime. Linux is the production Terraform target, macOS is supported for
development and testing, and Windows Terraform execution is unsupported and
rejected before preflight or spawn. Naturally portable metadata/rendering code
may still run on Windows without implying Terraform support.

`make demo-contract` is the local no-credentials proof for the shipped demo: it
materializes the demo overlay, verifies committed demo config/import artifacts
do not drift, checks there are no stale demo moved-block files, and validates
the generated demo module tree. It consumes the shipped bundle without an npm
rebuild or Python runtime. It does not run live provider import or
Terraform/OpenTofu plan; the live plan contract begins with the primary
adoption workflow above and requires real provider credentials.

The primary runtime is the executable, dependency-bundled
`dist/infrawright-cli.mjs`, exposed as `iw` (with `infrawright` retained as a
compatibility alias), with checksum
`dist/infrawright-cli.mjs.sha256`. Verify an approved prebuilt runtime with
`make verify-runtime`; this checks its checksum, Node 24, command surface, and
selected pack/profile/deployment inputs without invoking npm or Python. The
runtime tree also retains its package-root `package.json` as bundle-location
metadata; verification rejects a nearer package file that would change the
bundle's discovered root. Downstream execution needs Node 24 and Terraform for
Terraform operations, but neither `npm install`, `npm ci`, `node_modules`,
TypeScript, esbuild, tsgo, nor Python.

Rebuilding from source is a separate maintainer contract: it requires the
pinned packages and platform binaries in `package-lock.json`. Run
`make source-build-preflight` to check the configured registry without
installing anything, or generate the exact mirror inventory with
`node scripts/build-environment-preflight.mjs --manifest`. Until a restricted
corporate registry mirrors that inventory, use the approved prebuilt bundle;
the repository does not bypass the configured registry or fall back to the
public npm registry. Python remains in this repository for retained
compatibility tests, migration differentials, and optional diagnostic/research
tools. No
live-provider, live-backend, or deployment-Apply qualification is claimed by
the credential-free repository tests.

See [Operational Node Runtime](docs/operational-runtime.md) for the authoritative
Make/CLI inventory, release contract, support policy, external qualification
checklist, and retained-architecture inventory. The unused legacy process host,
ZCC collector child, and exact-five migration protocols were retired after a
repository and downstream-consumer inventory found no callers.

## Layout

| Path | Role |
|------|------|
| `engine/` | retained Python implementations used by tests, differentials, and maintainer/migration tools; not required by the operational Node runtime |
| `node-src/` | generic typed Node 24 operational library and `iw` CLI |
| `catalogs/` | frozen versioned transition catalogs retained for migration consumers; not required by the generic CLI |
| `packs/<name>/` | provider metadata: `pack.json`, collection registry, overrides, and schemas; retained Python collector modules may coexist but are not runtime dependencies |
| `[<overlay>/]config/<tenant>/<resource_type>.auto.tfvars[.json]` | generated tenant config; `deployment.json` `tfvars_format` selects `json` by default or opt-in `hcl` |
| `[<overlay>/]imports/<tenant>/<resource_type>_imports.tf` | generated import blocks |
| `[<overlay>/]envs/<tenant>/<root_label>/` | generated Terraform roots; `<root_label>` is the resource type by default, or an opt-in grouped root label |
| `<module_dir>/<resource_type>/` | generated Terraform modules for the selected deployment module set |

There is one generated output layout. `overlay` is an optional free-form prefix
owned by the adopter. The shipped `deployment.json` points at the `demo/`
overlay, so demo artifacts live under `demo/config/demo` and
`demo/imports/demo` while real deployments can choose their own overlay prefix.
Generated env roots resolve module sources from deployment-configured
`module_dir`; the shipped demo generates `demo/modules/default` on demand.
See [Repository Surface](docs/repo-surface.md) for the keep/prune policy across
core, demo, packs, tools, release scripts, and archived docs.
See [Pack Authoring Contract](docs/pack-authoring.md) for the current validated
`pack.json` and `registry.json` vocabulary.

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

## Provider Readiness

Provider-readiness tooling is maintainer infrastructure. It helps decide whether
a provider/resource is worth pack and lab work before adoption evidence exists.
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
pack enumeration paths. Source evidence entries carry hop chains so a Go port
or AST-backed analyzer can emit the same JSON contract with stronger source
evidence; the shipped `tools/source-evidence-ast/` helper is the current
source-analysis prototype. `special` covers non-CRUD
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

Provider readiness indexes should be generated from provider schema, surface
rules, and evidence files rather than hand-maintained tables. Resource
inventories come from provider schema, surfaces are classified by small
declarative rules, and live/static evidence files determine whether each
resource is `static_mapped`, `read_observed`, `write_sampled`,
`import_verified`, `not_tested_paid`, `not_tested_risky`, or `not_applicable`.
Humans classify surfaces; tools classify resources and evidence so indexes do
not drift by hand.

## Status

**0.1 — Zscaler** (`zia` · `zpa` · `zcc`): reproduces its demo tenant byte-identically
through the agnosticized engine; provider-first packs; identity-keyed reconciliation.
Additional provider labs and metadata cover Cloudflare, Google, AWS, NetBox,
and Grafana evidence without making those packs production-certified.

## License

[FSL-1.1-Apache-2.0](LICENSE) — source-available; converts to Apache 2.0 two years after
each release.
