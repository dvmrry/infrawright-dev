# Adoption Command Surface

The root `Makefile` is the stable product command surface. Overlay Makefiles may
add local workflows, but they should not redefine the meaning of the core
adoption commands.

## Primary Adoption Flow

These commands are the normal import-oracle adoption workflow:

```text
fetch
  -> adopt
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
| `make gen-env` | Generate isolated Terraform/OpenTofu env roots that source the deployment-selected module set. |
| `make stage-imports` | Copy generated `import {}` and `moved {}` blocks into env roots. |
| `make plan SAVE=1` | Run Terraform/OpenTofu plans and save plan artifacts for later gates. |
| `make assert-adoptable` | Classify saved plans as clean, tolerated by explicit policy, or blocked. Guidance annotations never make a blocked plan clean. |
| `make apply` | Reclassify saved plans and apply only when they are clean/import-only or fully tolerated by explicit policy; destructive or non-main workflows still require explicit safety flags. |

Supporting adoption commands:

| Command | Responsibility |
|---|---|
| `make unstage-imports` | Remove staged import/move blocks from env roots. |
| `make clean-plans` | Remove saved plan artifacts. |
| `make assert-clean` | Compatibility/no-policy saved-plan gate for no-op or import-only plans. Prefer `assert-adoptable` for adoption workflows that may use drift policy or guidance annotations. |

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
pre-fingerprint plans; re-run `make plan SAVE=1` after root membership,
generated env files, staged imports/moves, expression bindings, or member
tfvars change.

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

Full `make transform` and `make adopt` runs process selected resource types in
pack reference order, so a referent lookup sidecar is refreshed before same-root
referrers derive generated bindings. A selective transform of only a referrer
derives bindings from the committed referent sidecar by design; backfill
pipelines commit sidecars, and operators should re-run the referent first when
that referent changed.

Generated tenant config is JSON by default. Set `tfvars_format` to `hcl` in the
active `deployment.json` to write `<resource_type>.auto.tfvars` instead of
`<resource_type>.auto.tfvars.json`; write commands remove the stale
opposite-format artifact so Terraform never auto-loads both. HCL inline comments
are generated from pack reference metadata and lookup sidecars, not from
operator-authored files.

## Grouped Env Roots

By default each generated resource type gets its own env root:
`envs/<tenant>/<resource_type>/`. Deployments may opt in to grouped roots with
a `roots` block in `deployment.json`:

```json
{
  "roots": {
    "zpa": {
      "strategy": "slug",
      "groups": {
        "zpa_app": [
          "zpa_segment_group",
          "zpa_server_group",
          "zpa_application_segment"
        ]
      },
      "bind_references": false
    }
  }
}
```

`roots` keys are provider names declared by pack `provider_prefixes` values. A
provider entry supports:

| Key | Meaning |
|---|---|
| `strategy` | `"explicit"` or `"slug"`; absent means `"explicit"`. |
| `groups` | Optional map of `<root_label>` to resource type list. Listed members share `envs/<tenant>/<root_label>/`. |
| `bind_references` | Optional boolean, default `false`; when true, generate group-local expression bindings for pack-declared references whose referent is in the same grouped root. |

Explicit `groups` always win for their listed members. With `strategy: "slug"`,
remaining resource types are grouped by provider prefix plus the first token
after the prefix: `zpa_application_segment` maps to `zpa_application`. Slug
groups with only one member collapse back to the ungrouped resource-type root,
so enabling slug grouping does not create singleton topology churn.

Grouped root members still keep per-resource config and import filenames.
Inside the shared root, each grouped member uses a namespaced tfvars variable
such as `zpa_segment_group_items`; ungrouped roots continue to use `items`.

Operations are whole-root. Selecting any member of a grouped root selects the
entire root, because Terraform state cannot safely apply part of one root. The
CLI prints:

```text
NOTE: selecting <member> selects whole root <root_label>; also operating on <other_members>
```

`stage-imports` copies every selected root member's
`<resource_type>_imports.tf` and `<resource_type>_moves.tf` into the shared root.
`plan` passes one `-var-file` for each member config file that exists and emits
a skip note for missing member config. `assert-clean`, `assert-adoptable`,
`clean-plans`, and `apply` operate once per root plan.

When `bind_references` is true, transform/adopt may write
`config/<tenant>/<resource_type>.generated.expressions.json` beside the
resource tfvars. Env generation loads generated bindings first and then
operator-authored `config/<tenant>/<resource_type>.expressions.json`, so a
hand-written binding wins for the same resource path. Generated bindings only
target same-root references and resolve them through sibling module outputs:
`module.<referent_type>.name_to_id["<display_name>"][0]`.

Bindings are explicit generated artifacts; tfvars keep the raw IDs and readback still round-trips.

### Binding Validation Limits

When `tfvars_format: hcl` is active, operator-authored expression sidecars are
validated by Terraform/OpenTofu at plan time only. The engine does not parse HCL
tfvars back into data; generated bindings are leaf-exact by construction before
the HCL file is written. Generated reference binding derivation supports only
top-level reference fields. Dotted or nested referent paths are not derived.

Generated binding skip/fallback semantics:

| Condition | Behavior |
|---|---|
| Referent lookup sidecar is missing | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| ID is absent from the referent lookup | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| Lookup display name is `<unknown>` | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| Lookup display name maps to more than one referent ID | Leave the literal ID in tfvars and print a `NOTE bindings:` skip to avoid ambiguous `name_to_id` lookups. |
| Referent module does not emit `name_to_id` | Leave the literal ID in tfvars and print a `NOTE bindings:` skip. |
| Referent lookup sidecar uses a `name_field` other than `name` | Leave the literal ID in tfvars and print a `NOTE bindings:` skip because generated `name_to_id` outputs are keyed by `name`. |
| Reference crosses a group/root boundary | No generated binding is considered; existing literal/comment behavior applies. |

Group membership is fixed at first import. Changing it later means a fresh re-bootstrap of the affected types into new state — there is no regroup tooling, by design.

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
| `make demo-contract` | Credential-free demo contract check: materializes the demo, verifies committed demo config/import artifacts do not drift, rejects stale demo moved-block files, and checks the generated demo module tree. |
| `make check-demo` | Verifies committed demo config/import artifacts do not drift. |
| `make check-modules` | Generates modules in a temporary deployment and checks generator output. |
| `make test` | Runs unit tests. |
| `make check` | Runs unit tests, demo drift checks, module generator checks, pack validation, and vendor-boundary audit. |

The generated demo module tree remains local/ignored. It is not part of the
public committed surface. `make demo-contract` is intentionally not a live
provider import/plan proof; that requires credentials and the primary adoption
flow.

## Collector Boundary

Collectors gather provider data. They do not own adoption semantics.

`make fetch` currently invokes the shared REST collector entrypoint:

```text
python -m engine.collectors.rest
```

That entrypoint is product code, but provider-specific collection behavior
belongs in packs and pack-owned helpers. A collector may know how to authenticate,
page, call list/detail endpoints, and write raw JSON into `pulls/<tenant>`.

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
