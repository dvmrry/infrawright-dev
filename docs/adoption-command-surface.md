# Adoption Command Surface

The root `Makefile` is the stable product command surface. Overlay Makefiles may
add local workflows, but they should not redefine the meaning of the core
adoption commands. These targets are thin adapters over the Go `iw` CLI; the
authoritative production inventory is listed in
[Operational Go Runtime](operational-runtime.md).

## Primary Adoption Flow

These commands are the normal import-oracle adoption workflow:

```text
fetch
  -> adopt
  -> gen-modules
  -> gen-env
  -> stage-imports
  -> plan SAVE=1
  -> assert-adoptable
  -> apply
```

Command responsibilities:

| Command | Responsibility |
|---|---|
| `make fetch` | Collect raw provider inventory/detail JSON into `pulls/<tenant>`. |
| `make adopt` | Use Terraform/OpenTofu import as the provider-state oracle and write projected config/import artifacts. |
| `make gen-modules` | Generate the deployment-selected reusable Terraform/OpenTofu modules. |
| `make gen-env` | Generate isolated Terraform/OpenTofu env roots that source the deployment-selected module set. |
| `make stage-imports` | Copy generated `import {}` and `moved {}` blocks into env roots. |
| `make plan SAVE=1` | Run Terraform/OpenTofu plans and save plan artifacts for later gates. |
| `make assert-adoptable` | Classify saved plans as clean, tolerated by explicit policy, or blocked. Guidance annotations never make a blocked plan clean. |
| `make apply` | Reclassify saved plans and apply only when they are clean/import-only or fully tolerated by explicit policy; destructive or non-main workflows still require explicit safety flags. |

Adopt's provider Oracle may execute a mechanically verified import-only plan
against ephemeral local scratch state so provider Read can supply projected
configuration. That local-only transaction cannot create, update, replace, or
destroy remote objects and is distinct from the later deployment `make apply`.
Deployment Apply rechecks and executes only the exact saved `tfplan`.

Supporting adoption commands:

| Command | Responsibility |
|---|---|
| `make unstage-imports` | Remove staged import/move blocks from env roots. |
| `make clean-plans` | Remove saved plan artifacts. |
| `make assert-clean` | Compatibility/no-policy saved-plan gate for no-op or import-only plans. Prefer `assert-adoptable` for adoption workflows that may use drift policy or guidance annotations. |
| `make roots` | Emit the configured root topology as versioned JSON for downstream path-to-root scoping. |
| `make scope-paths` | Map a caller-supplied JSON list of changed paths to affected resources and complete logical roots without invoking a VCS. |
| `make plan-roots` | Enumerate materialized env roots and the exact `tfplan`/`tfplan.sources` artifact locations used for save/restore. |

`make apply` uses the same saved-plan classification semantics as
`make assert-adoptable`. If `assert-adoptable` used `POLICY=<file>` to classify
intentional drift as tolerated, pass the same `POLICY=<file>` to `make apply`.
The legacy `ALLOW_PLAN_CHANGES=1` path is a broad override for intentionally
applying blocked saved plans; it is noisy, should not replace explicit drift
policy, and does not bypass `ALLOW_DESTROY=1` for destructive or replacement
plans.

`make plan SAVE=1` writes a `tfplan.sources` fingerprint next to each saved
`tfplan`. `make assert-clean`, `make assert-adoptable`, and `make apply`
recompute that fingerprint before reading the saved plan and refuse stale or
older-fingerprint plans. The fingerprint covers root Terraform inputs
(`.tf`, `.tf.json`, auto-loaded tfvars, and `.terraform.lock.hcl`), member
tfvars, the effective selected local module trees except transient cache
directories, and the effective remote-backend config digest and state key.
It stores hashes, not backend-config contents or absolute paths.
Every root member must resolve to a local module source; missing or non-local
member sources fail loudly before a fingerprint can be written. Fingerprint
extraction accepts the generated module-block shape only, so comments cannot
shadow an effective source, HCL template escapes cannot remap its path, and
structural edits require regenerating the root.

Creating a new saved plan removes any older plan/fingerprint pair before init,
checks that init-consumed root, module, and backend inputs are unchanged across
init, and checks that the full fingerprint is unchanged across the plan
command. A failed re-plan or an input change during planning therefore leaves
no saved pair to classify or apply. Apply checks the fingerprint both before
and after its own init step.

Re-run `make plan SAVE=1` after any of those inputs change. When planning with
`BACKEND_CONFIG=<file>`, pass the same option to `assert-clean`,
`assert-adoptable`, and `apply`; omitting it or changing its contents makes the
saved plan stale before classification or apply.

## Machine-Readable Downstream Contracts

Downstream delivery and drift pipelines must consume stable JSON instead of
importing internal implementation modules or parsing human stderr.

Emit configured root topology with:

```sh
make roots TENANT=prod RESOURCE="zpa_application_segment zpa_segment_group"
# or: dist/iw roots --tenant prod --resource zpa
```

The topology is derived from deployment configuration and pack metadata; env
roots do not need to exist. It includes logical root labels, sorted members,
provider ownership, the resource-to-root map, and tenant artifact directories
when `TENANT` is supplied. Its schema is
[`docs/schemas/root-topology.schema.json`](schemas/root-topology.schema.json).
Root provider ownership is safe to derive from the first member because
deployment validation rejects mixed-provider roots. Reported directories and
root paths preserve the active deployment overlay: relative overlays produce
repository-relative paths, while supported absolute overlays produce absolute
paths. Consumers must not assume topology paths are checkout-independent.
Malformed deployment JSON, including a non-object top level, and explicit empty
tenant values fail before any topology JSON is emitted.

Map changed paths directly to affected resources and whole logical roots with:

```sh
make scope-paths PATHS_JSON=changed-paths.json
# or: dist/iw scope-paths \
#       --paths-json changed-paths.json
```

`scope-paths` is deliberately VCS-agnostic: it never invokes Git and accepts
only paths supplied by its caller. It understands the active deployment's
config, import, env-root, and module layouts. A deployment-file change scopes
all configured resources because topology or paths may have changed. Each
matched input records its match kinds, any tenant segment encoded by the path, exact
resources, and logical roots. Aggregated root entries always include every root
member plus the subset directly matched by the input paths. Unknown or unrelated
paths are preserved in `unmatched_paths`; they are never silently coerced to an
affected resource. Input paths are normalized and sorted for deterministic
output, and deleted paths remain scopeable because matching does not require
them to exist.
Like the other deployment-aware commands, relative paths are interpreted from
the repository/deployment working directory. Matching compares canonical
absolute and realpath forms so equivalent absolute, `../`-relative, and symlink
spellings scope identically, including for supported external overlays; emitted
`paths` retain the caller's normalized spelling.

The changed-path schema is
[`docs/schemas/changed-path-scope.schema.json`](schemas/changed-path-scope.schema.json).
Downstream owns the Git diff and its policy for `unmatched_paths`; the engine
owns only deployment-layout and resource-to-root semantics.

Enumerate materialized env roots and their plan-artifact pair with:

```sh
make plan-roots TENANT=prod RESOURCE=zpa_application_segment
# or: dist/iw plan-roots \
#       --tenant prod --resource zpa_application_segment
```

Each result includes the tenant, logical root, complete member list, provider,
env directory, exact `tfplan` and `tfplan.sources` paths, existence flags, and
an `artifact_state` of `absent`, `complete`, or `incomplete`. Save pipelines
should archive only a `complete` pair. Restore pipelines must restore both files
to their original enumerated root, then rerun `assert-clean` or
`assert-adoptable` with the same backend configuration; a lone plan or lone
fingerprint is intentionally classified as `incomplete`. `artifact_state`
describes file presence only; the assertion command remains responsible for
validating fingerprint contents, freshness, and plan classification.

The materialized-plan-root schema is
[`docs/schemas/plan-roots.schema.json`](schemas/plan-roots.schema.json).

Write a saved-plan assessment alongside the existing human output with:

```sh
make assert-clean TENANT=prod REPORT=reports/prod-clean.json
make assert-adoptable TENANT=prod POLICY=policy/prod.json \
  REPORT=reports/prod-adoptable.json
```

The report records each logical root, its members, classification, normalized
findings, matching informational guidance, stale drift-policy entries, and the
exact saved-plan SHA-256, plan/Terraform format versions, drift-policy SHA-256,
and validated `tfplan.sources` fingerprint. A blocked classification writes
the report before returning the existing non-zero gate result. `REPORT=-`
writes JSON to stdout. Human stderr and exit semantics remain unchanged.
Policy bytes are parsed and hashed from one read. Each saved plan is hashed
before and after `terraform show`, and the plan, fingerprint, and current plan
sources (plus policy, when supplied) are rechecked immediately before report
publication. A concurrent change writes an error assessment and fails the gate
instead of publishing a successful classification bound to different evidence.
Import-only actions remain part of the internal clean classification evidence;
because `clean` is an aggregate v1 status rather than a reportable finding
status, clean/import-only roots emit an empty `findings` list.

Finding paths and guidance paths deliberately expose two domains. Each
`findings[].paths` and `guidance[].finding_path` is a concrete plan-space path
that retains list indexes, such as `rules[0].id`. The corresponding
`guidance[].matched_plan_path` is the normalized schema-space rule path, such as
`rules[].id`. Downstream joins guidance to a finding with `finding_path`, while
`matched_plan_path` explains which reusable guidance rule matched.

Report creation begins after command-line parsing. An invalid invocation, such
as an unknown option or malformed tenant, fails without creating `REPORT`; a
downstream caller must treat a non-zero exit and missing report as an invocation
error. Once assessment begins, report writing is attempted for every error. If
the target itself is unwritable, a warning is printed while the original
assessment error remains the command result.

The assessment schema is
[`docs/schemas/saved-plan-assessment.schema.json`](schemas/saved-plan-assessment.schema.json).
All published contracts carry `schema_version: 1`; consumers must reject unsupported
versions rather than guessing at field meaning.
The assessment schema intentionally fixes the accepted `tfplan.sources` shape;
a future fingerprint format change requires a coordinated assessment-schema
version update.

For real provider/tenant validation, use the
[Integration Validation Runbook](integration-validation.md) to capture evidence
and classify failures before turning them into engine, pack, collector, or
documentation work.

## Raw Transform Path

`make transform` remains a maintained path for demo generation, pack
development, and workflows that intentionally project raw API bodies directly.
It is not the import-oracle adoption path.

Use `make adopt` when the desired source of truth is provider-imported state.
Use `make transform` only when a pack/workflow explicitly wants raw API fields
projected through registry overrides.

Maintainers comparing those two representations should use the
[Transform/Adopt Parity Diagnostic](transform-adopt-parity.md). Its committed,
sanitized fixtures run the real local transform and oracle-projection paths and
fail closed on new, stale, or still-evidence-gated differences. It is a
diagnostic and does not make either path authoritative.

Full `make transform` and `make adopt` runs process selected resource types in
pack reference order. Explicit `lookup_sources` remain always-on pack outputs.
Cross-state references are enabled by default, so the engine also derives the
lookup needed by each declared referent from the reference's `name_field`.
Setting `cross_state_references` to `false` for a provider disables those
generated bindings and removes mode-scoped sidecars on the next run. A
selective transform of only a referrer consumes the committed referent sidecar
by design, so operators should run the referent first whenever its identity
evidence changed.

Generated tenant config is JSON by default. Set `tfvars_format` to `hcl` in the
active `deployment.json` to write `<resource_type>.auto.tfvars` instead of
`<resource_type>.auto.tfvars.json`; write commands remove the stale
opposite-format artifact so Terraform never auto-loads both. HCL inline comments
are generated from pack reference metadata and lookup sidecars, not from
operator-authored files.

## Singleton Env Roots

Each generated resource type owns exactly one env root:
`envs/<tenant>/<resource_type>/`. Provider and product selectors expand to a
set of singleton roots; they never create shared state. `gen-modules`,
`validate-modules`, `gen-env`, staging, planning, assessment, and Apply all use
the same topology described in [State topology](state-topology.md).

Deployment `roots.<provider>` entries support only
`cross_state_references`. The option is a boolean and defaults to `true`; use
an explicit `false` only when generated cross-state bindings must be disabled.
The retired `strategy`, `groups`, and `bind_references` fields fail validation.

`stage-imports` copies the selected resource's imports and moves files into
its singleton root. Plan and Apply operate once per selected resource root.

An existing generated moves file is durable unresolved migration evidence.
Transform and Adopt preserve it byte-for-byte when a rerun derives no new move
or rederives the same bytes. A different newly derived move set fails closed
before any config, lookup, binding, imports, or moves output is changed. The
engine never removes an existing moves file merely because the imports
baseline has advanced; explicit removal is an operator decision after the
corresponding state migration is confirmed.

With cross-state references enabled, transform/adopt may write
`config/<tenant>/<resource_type>.generated.expressions.json` beside the
resource tfvars. Env generation loads generated bindings first and then
operator-authored `config/<tenant>/<resource_type>.expressions.json`, so a
hand-written binding wins for the same resource path.

Bindings are explicit generated artifacts; tfvars keep the raw IDs and readback still round-trips.

### Cross-state References

Cross-state references are enabled by default. The following explicit settings
are valid but normally unnecessary:

```json
{
  "roots": {
    "zia": { "cross_state_references": true },
    "zpa": { "cross_state_references": true }
  }
}
```

References between roots use `terraform_remote_state` and a generated,
sensitive `infrawright_reference_ids` root output containing only stable config
keys mapped to provider IDs. Complete resource objects are never exported.
Predefined or system identifiers absent from a managed referent lookup remain
literal values with a visible binding diagnostic.

A declared cycle between singleton states fails before root files or Terraform
commands. Resolve the pack reference cycle before adoption.

The first import is dependency ordered, not plan-all/apply-all. Materialize and
apply each referent state before planning its referrers. The existing
`make resources-reference-order` output supplies referent-first resource order.
When `gen-env` selects a referrer, it automatically materializes that root's
complete cross-state referent dependency closure so every producer root contains
the required output. Later plan and Apply commands remain explicitly
referent-first; they do not silently widen a deployment operation.
Do not mutate, replace, or re-adopt a referent between planning and applying a
dependent referrer. A referent-state change invalidates the dependent plan
operationally even though the saved-plan fingerprint covers only the dependent
root's local inputs; regenerate and reassess every affected referrer plan.
After the last import-only Apply, repeat the complete sequence from a fresh
workspace and require no-op plans.

Local roots read sibling `terraform.tfstate` files. Generated `azurerm` roots
reuse the same backend address data passed to `terraform init`, but each data
source derives its own `<tenant>/<referent-root>.tfstate` key. In cross-state
mode `BACKEND_CONFIG` must be a JSON object containing non-secret backend
address fields, for example:

```json
{
  "resource_group_name": "terraform-state",
  "storage_account_name": "example",
  "container_name": "tfstate",
  "use_azuread_auth": true
}
```

The projection into `terraform_remote_state` is a strict allowlist. String
fields are limited to `storage_account_name`, `container_name`,
`resource_group_name`, `subscription_id`, and `tenant_id`; reviewed boolean
fields are limited to `lookup_blob_endpoint`, `use_azuread_auth`, `use_cli`,
`use_msi`, and `use_oidc`. Every other field fails closed. In particular, do
not put `key`, client identifiers, access keys, SAS tokens, client secrets,
OIDC token values or files, MSI endpoints, or certificate paths or credentials
in that file. Credential material remains environment- or
managed-identity-owned. Pass the same file to `stage-imports`, `plan`,
`assert-adoptable`, and `apply`; its bytes remain covered by saved-plan
fingerprinting.

Terraform's `terraform_remote_state` data source grants the referrer access to
the complete referent state snapshot, not only its declared outputs. The
generated root exposes only the minimal ID map to Terraform expressions, but
backend authorization must still treat the referrer as a reader of the full
referent state. Use this mode only where that trust boundary is acceptable.

### Binding Validation Limits

Expression-binding target paths are validated against the pinned provider
schema for both JSON and native-HCL tfvars. Exact canonical numeric selectors
such as `server_groups[0].id` may traverse ordered lists. Wildcard, identity,
negative, quoted, noncanonical, and unordered multi-element set selectors are
rejected. A path that crosses a list without an explicit index also fails
closed. JSON tfvars receive an additional concrete leaf/index existence check;
the engine does not parse HCL tfvars back into data, so a structurally valid but
out-of-range HCL selector fails during Terraform validation or planning.

Generated reference binding derivation remains limited to pack-declared,
source-backed reference fields. Indexed-path support does not infer new nested
references from matching field names and does not expand the current pack
reference inventory by itself.

This capability does not make ZIA URL-filtering `ISOLATE` rules adoptable with
the pinned `zscaler/zia` provider 4.8.0. Version-scoped `unsupported_if`
classification occurs before identity derivation and the import Oracle, while
root expression bindings are applied later during environment generation. The
provider's import Read still does not reconstruct `cbi_profile`; do not remove
that fail-closed classification on the strength of an indexed root binding.

Generated binding skip/fallback semantics:

| Condition | Behavior |
|---|---|
| Referent lookup sidecar is missing | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| ID is absent from the referent lookup | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| Referent lookup sidecar has no `key_by_id` map | Leave the literal ID in tfvars and print a `NOTE bindings:` skip; rerun transform/adopt for the referent to refresh the sidecar. |
| Referent key contains Terraform template interpolation syntax | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| Reference crosses a root boundary with cross-state mode disabled | No generated binding is considered; existing literal/comment behavior applies. |
| Reference crosses a root boundary with cross-state mode enabled | Bind through the referent root's minimal `infrawright_reference_ids` output. |

The saved-plan assessor and exact-plan Apply do not trust that output by name.
For a referent root selected from the loaded pack/deployment context, they bind
the expected referent resource types from the cross-state topology and rebuild
the exact stable-key-to-provider-ID map from Terraform's planned child-module
resources. Only a fully known, sensitive create/update matching that map is
treated as engine-owned plan metadata. Every other non-no-op output remains
outside the saved-plan contract.

## Provider Readiness And Probe Commands

These commands support pack onboarding and API/schema evidence. They are not
tenant adoption commands:

| Command | Responsibility |
|---|---|
| `make reconcile` | Compare one raw API fixture to Terraform schema/OpenAPI evidence. |
| `make openapi-map` | Produce provider-resource to OpenAPI surface mapping evidence. |
| `make source-operation-map` | Derive read/list evidence from provider source and OpenAPI operations. |
| `make source-evidence-eval` | Compare text-scanner evidence against AST-backed source facts. |
| `make provider-probe` | Run a pinned provider readiness recipe and write probe artifacts. |

Provider-readiness outputs can inform pack metadata, but they do not write
tenant config, imports, env roots, or Terraform state.

## Demo And Validation Commands

These commands keep the shipped demo and generators healthy:

| Command | Responsibility |
|---|---|
| `make demo` | Overlay-owned demo workflow from `demo/Makefile`; materializes demo config/import artifacts and local generated modules. |
| `make demo-contract` | Credential-free demo contract check: consumes `dist/iw`, materializes the demo, verifies committed demo config/import artifacts do not drift, rejects stale demo moved-block files, and checks the generated demo module tree. |
| `make check-demo` | Verifies committed demo config/import artifacts do not drift. |
| `make check-modules` | Generates modules in a temporary deployment and checks generator output. |
| `make test` | Runs the complete Go suite. |
| `make check` | Runs the Go suite, demo/module checks, pack validation, and formatting checks. |

The generated demo module tree remains local/ignored. It is not part of the
public committed surface. `make demo-contract` is intentionally not a live
provider import/plan proof; that requires credentials and the primary adoption
flow.

## Collector Boundary

Collectors gather provider data. They do not own adoption semantics.

`make fetch` invokes the Go CLI's shared REST collector coordinator:

```text
dist/iw fetch
```

The Go runtime owns registry selection, pagination, retries, failure
aggregation, and deterministic pull-file output. Resource list/detail metadata
remains in pack registries; built-in product adapters own authentication and URL
composition. A collector may authenticate, page, call list/detail endpoints,
and write raw JSON into `pulls/<tenant>`.

A collector must not:

- decide Terraform schema projection,
- generate tfvars, imports, moved blocks, modules, or env roots,
- mutate drift policy,
- decide plan tolerance,
- mark an adoption as clean,
- render provider configuration,
- hide provider/API fields from advisory reporting.

The adoption engine treats collector output as input evidence. In the
import-oracle path, raw JSON supplies stable keys and import IDs; Terraform or
OpenTofu provider state supplies the projected configuration body. Raw detail
JSON remains useful for static advisory reports and provider labs, especially to
detect API fields that Terraform/provider state cannot see.

`make fetch-diag` is also collector-scoped. It diagnoses TLS/system-trust issues
for fetcher hosts and does not participate in adoption or plan classification.

## Overlay Boundary

Only one overlay is active per command. Use separate deployment files for
separate domains or providers, such as:

```text
overlays/zscaler/deployment.json
overlays/aws/deployment.json
overlays/gcp/deployment.json
```

Then invoke the root commands with matching `OVERLAY` and `DEPLOYMENT` values.
Infrawright does not compose multiple overlays in a single run.
