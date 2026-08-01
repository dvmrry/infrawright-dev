# Data-Only Referents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tenant objects exposed only as Terraform data sources become
first-class reference targets, delivered end to end for
`zia_location_groups` per
docs/superpowers/specs/2026-08-01-data-only-referents-design.md.

**Architecture:** A data-only referent is an ordinary singleton root
fulfilling the same `iw_reference_ids` output contract as a generated
root, so reference machinery (tokens, bindings, remote-state wiring,
state-aware fallback) is untouched. New behavior is confined to: the
registry surface (`data_referent`), transform (fetch + lookup, never
imports/moves), modulesgen (a data module), topology/lifecycle
acceptance, and a plan-roots projection of data-leaf referents for
CI/CD refresh sequencing.

**Tech Stack:** Go (go/ module), canonjson value model, synthetic pack
fixtures (pattern: `syntheticRootForTopology` in
go/internal/envgen/pack_scope_test.go).

## Global Constraints

- Byte-discipline: all committed-artifact bytes render through
  canonjson/tfrender renderers; no `encoding/json` marshaling of
  artifacts.
- Fail closed: malformed metadata refuses loudly at load; no silent
  fallthrough to "not generated".
- Every new invariant needs a focused regression PROVEN to fail against
  pre-fix behavior or a faithful unsafe mutation before the full suite
  runs (AGENTS.md Validation Promotion).
- Full-corpus `go test ./...` is a promotion gate, not discovery.
- Commits: imperative mood, no Co-Authored-By.
- Worker contract: implement ONE task, leave work uncommitted, report
  the diff and test output; the coordinator commits. Do not touch
  files outside the task's Files list.
- This plan's Tasks 3, 5, and 7 touch generated-output/golden surfaces:
  the branch is review-required under AGENTS.md (Codex adversarial
  review) before merge; the coordinator owns that gate.

---

### Task 1: Registry surface — `data_referent`

**Files:**
- Modify: `go/internal/metadata/resources.go` (allowed-keys list at
  ~line 19; validation near the `generate` handling at ~line 528)
- Modify: `go/internal/metadata/resource_set.go` (descriptor field)
- Modify: `go/internal/roots/roots.go` (`loadedResourceShape`, ~line
  163)
- Test: `go/internal/metadata/loader_test.go`,
  `go/internal/roots/roots_test.go`

**Interfaces:**
- Consumes: existing registry entry validation
  (`resources.go` allowlist `"adopt", "derive", "fetch",
  "fetch_skip_reason", "generate", "product"`).
- Produces: registry key `data_referent` (bool); descriptor field
  `metadata.ResourceDescriptor.DataReferent bool`; helper
  `metadata.LoadedResourceMetadata` surfaces the flag the same way
  `generate` is surfaced today (via `Registry["data_referent"]`);
  `loadedResourceShape` sets `DataReferent` on the descriptor.

**Validation contract (exact):** `data_referent` must be boolean if
present. `data_referent: true` REQUIRES `fetch` and FORBIDS `generate:
true`, `adopt`, and `derive` on the same entry — each violation is its
own load-time error naming the resource type and the offending key
pair. `generate` absent/false plus `data_referent` absent stays valid
(metadata-only entries unchanged).

- [ ] **Step 1: Write failing loader tests** — table-driven cases in
  `loader_test.go`: (a) valid `{"data_referent": true, "fetch":
  {"pagination": "zia", "path": "locations/groups"}, "product": "zia"}`
  loads and `Registry["data_referent"] == true`; (b) `data_referent:
  true` without `fetch` refuses, error contains `data_referent` and
  `fetch`; (c) `data_referent: true` with `generate: true` refuses;
  (d) with `adopt` refuses; (e) with `derive` refuses; (f)
  `"data_referent": "yes"` (non-bool) refuses.
- [ ] **Step 2: Run, verify all six fail** (`go test ./internal/metadata/
  -run TestRegistryDataReferent -count=1`; expect FAIL: unknown key
  `data_referent` today).
- [ ] **Step 3: Implement** — add `"data_referent"` to the allowlist;
  add the four validation rules beside the existing `generate`
  validation; add `DataReferent bool` to `ResourceDescriptor`; set it
  in `loadedResourceShape` from `resource.Registry["data_referent"]`.
- [ ] **Step 4: Run metadata + roots suites, verify pass.**
- [ ] **Step 5: Report diff** (coordinator commits).

### Task 2: Topology — data-referent types are roots and referents

**Files:**
- Modify: `go/internal/roots/roots.go` (`indexLoadedPackRoot` /
  `resourceIndex` — the `generated` set gates root membership today)
- Modify: `go/internal/envgen/reference_topology.go`
  (`generatedNonDerived`, ~line 124 — referent acceptance)
- Test: `go/internal/roots/roots_test.go`,
  `go/internal/envgen/reference_topology_test.go` (or the file where
  cross-state topology tests live — follow existing placement)

**Interfaces:**
- Consumes: Task 1's `DataReferent` descriptor field and
  `Registry["data_referent"]`.
- Produces: `roots.LoadedRootTopology` materializes a singleton root
  for each data-referent type exactly as for generated types; envgen's
  cross-state topology accepts a data-referent type as a referent
  (edge referrer→data-root validates). Data-referent types are NEVER
  selectable as transform/adopt/plan *referrers* — only referents and
  roots.

- [ ] **Step 1: Failing tests** — synthetic pack (extend the
  `writeSyntheticTopologyPack` fixture pattern) with a
  `data_referent: true` type; assert (a) `LoadedRootTopology` includes
  its singleton root; (b) a reference edge from a generated referrer
  to it survives topology validation; (c) it does not appear in
  generated-referrer enumerations.
- [ ] **Step 2: Verify failures** (data type currently invisible: no
  root materialized).
- [ ] **Step 3: Implement** — include data-referent types in the root
  index alongside generated ones (a parallel `dataReferents` string
  set on `resourceIndex`); extend envgen's referent acceptance so a
  referent qualifies when `generated-non-derived OR data_referent`.
- [ ] **Step 4: Run roots + envgen suites, verify pass.**
- [ ] **Step 5: Report diff.**

### Task 3: Modulesgen — the data module

**Files:**
- Create: `go/internal/modulesgen/data_module.go`
- Modify: `go/internal/modulesgen/generator.go`
  (`GenerateActiveModules` ~line 1063, `ActiveGeneratedResourceTypes`
  ~line 992, `ValidateGeneratedModuleTree` ~line 1077)
- Test: `go/internal/modulesgen/data_module_test.go`

**Interfaces:**
- Consumes: `metadata.LoadedPackRoot` (data-referent registry entries;
  provider schema `data_source_schemas[type]`; the pack's
  `lookup_sources[type].name_field` for the instantiation argument).
- Produces: `GenerateActiveModules` also emits a module directory per
  data-referent type; new
  `ActiveDataReferentResourceTypes(root metadata.LoadedPackRoot)
  []string` (sorted, mirroring `ActiveGeneratedResourceTypes`);
  `ValidateGeneratedModuleTree` accepts data modules.

**Rendered main.tf contract (exact bytes modulo provider header,
matching the existing module header/format conventions):**

```hcl
data "zia_location_groups" "items" {
  for_each = var.items
  name     = each.value.name
}

output "iw_reference_ids" {
  value = { zia_location_groups = { for k, d in data.zia_location_groups.items : k => d.id } }
}
```

`variables.tf` declares `var.items` as the same opaque items map shape
generated modules use. The `name` attribute comes from
`lookup_sources[type].name_field` (load error if absent for a
data-referent type — fail closed, do not default to "name"). The data
source must exist in `data_source_schemas` (load error if absent).

- [ ] **Step 1: Failing tests** — synthetic pack with the data type;
  assert rendered `main.tf` matches the contract above byte-for-byte
  (hand-pinned golden, two items in the fixture so `for_each` ordering
  is observable), `variables.tf` present, and error cases: missing
  `lookup_sources` name_field refuses; missing data_source_schemas
  entry refuses.
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement** `renderDataModule` in data_module.go using
  the existing header/formatter helpers; wire into
  `GenerateActiveModules`.
- [ ] **Step 4: Run modulesgen suite + `terraform fmt -check` lane if
  the suite has one; verify pass.**
- [ ] **Step 5: Report diff.**

### Task 4: Transform — fetch + lookup, never imports/moves

**Files:**
- Modify: `go/internal/transform/selection.go` (`TransformSourceType`
  ~line 218 refuses non-generated; data-referent types must be
  fetchable/selectable)
- Modify: `go/internal/transformrun/runner.go` (publication path:
  config tfvars + lookup sidecar only)
- Test: `go/internal/transformrun/` (follow existing runner test
  placement)

**Interfaces:**
- Consumes: Tasks 1-2 (flag + descriptor); existing fetch/pagination
  machinery (registry `fetch` is already generation-independent).
- Produces: transform over a data-referent type writes
  `<type>.auto.tfvars.json` (name-keyed items carrying ONLY the
  lookup_sources name_field) and `lookups/<type>.lookup.json`, and
  provably never writes an imports file or moves file for it.

- [ ] **Step 1: Failing tests** — runner test with a fake collector
  feeding two location-group-shaped objects: assert config items keyed
  by derived key with only the name field; lookup sidecar maps
  id<->key<->name; `imports/` contains nothing for the type; no moves
  file. Include the never-imports assertion as its own test so its
  failure names the invariant.
- [ ] **Step 2: Verify failures** (selection currently refuses
  non-generated types).
- [ ] **Step 3: Implement** — selection admits data-referent types
  (source type = itself; no derive interplay by Task 1's validation);
  runner branch skips imports/moves/rename derivation for them and
  applies the keep-only-name-field drop policy by construction.
- [ ] **Step 4: Run transform/transformrun suites, verify pass.**
- [ ] **Step 5: Report diff.**

### Task 5: Envgen + lifecycle acceptance, end to end

**Files:**
- Modify: `go/internal/envgen/environment_generator.go` (only if a
  gate assumes referent roots are generated — expected minimal)
- Modify: `go/internal/plan/lifecycle.go` (imports-only skip logic,
  `lifecycleRootIsDerived` neighborhood ~line 322: data roots have no
  imports and must not be misclassified)
- Modify: `go/internal/assessment/` classification ONLY if a plan with
  zero resource changes (outputs-only) is currently refused — verify
  first, change nothing if acceptance already holds
- Test: `go/internal/envgen/data_referent_test.go` (new),
  `go/internal/plan/lifecycle_test.go`

**Interfaces:**
- Consumes: Tasks 1-4. Fixture: synthetic pack with generated referrer
  `sample_rule` carrying reference `"groups.id": {"referent":
  "sample_groups_data", "name_field": "name"}` and data-referent type
  `sample_groups_data` (provider-neutral naming in engine fixtures).
- Produces: end-to-end proof that the reference machinery is untouched:
  committed tokens on the referrer resolve to
  `try(data.terraform_remote_state.sample_groups_data.outputs.iw_reference_ids.sample_groups_data["<key>"], local.iw_reference_lookup_sample_groups_data["<key>"])`
  exactly as against a generated referent.

- [ ] **Step 1: Failing/characterizing tests** — (a) generate the
  referrer root against the data referent: expression_bindings.tf
  carries the resolver above (compare against the identical fixture
  with a generated referent — the two referrer outputs must be
  byte-identical modulo referent type name); (b) state-aware absent
  state falls back to lookup (reuse `absentProbe` pattern from
  state_aware_test.go); (c) lifecycle: a data root is not skipped as
  derived/non-importable NOR expected to carry imports; (d) plan
  classification accepts an outputs-only plan (characterize current
  behavior first; only fix if it refuses).
- [ ] **Step 2: Verify which fail** (a-b should pass already if Tasks
  2-3 are correct — they are the "machinery untouched" proof; c-d are
  the acceptance risks).
- [ ] **Step 3: Implement only what (c)/(d) demand.** No speculative
  changes: if everything passes, this task's deliverable is the test
  file plus a report saying so.
- [ ] **Step 4: Run envgen + plan + assessment suites, verify pass.**
- [ ] **Step 5: Report diff.**

### Task 6: Plan-roots surface — data-leaf referents for CI/CD

**Files:**
- Modify: `go/internal/roots/planroots.go` (`MaterializedPlanRoot`,
  ~line 45)
- Modify: the plan-roots JSON emission path in `go/cmd/iw` (follow
  where MaterializedPlanRoot serializes today)
- Test: `go/internal/roots/planroots_test.go`

**Interfaces:**
- Consumes: cross-state topology edges (referrer root → referent root)
  plus Task 2's data-root identification.
- Produces: `MaterializedPlanRoot.DataReferents []string` — sorted,
  deduplicated labels of data-referent roots that any member of this
  root references; empty slice (never nil) when none. Serialized in
  plan-roots output so CI/CD sequences "apply data leaves, then
  plan/apply referrers" with zero discovery. The engine does NOT
  orchestrate, gate, or schedule refreshes (spec: Refresh strategy).

- [ ] **Step 1: Failing test** — fixture with referrer root whose
  member references a data-referent type: materialized root carries
  `DataReferents: ["sample_groups_data"]`; a root with no data
  references carries `[]`.
- [ ] **Step 2: Verify failure** (field absent).
- [ ] **Step 3: Implement** the projection at materialization time.
- [ ] **Step 4: Run roots + cmd suites, verify pass.**
- [ ] **Step 5: Report diff.**

### Task 7: ZIA pack wiring — `zia_location_groups`

**Files:**
- Modify: `packs/zia/registry.json` (add the entry from the spec,
  verbatim: `{"data_referent": true, "fetch": {"pagination": "zia",
  "path": "locations/groups"}, "product": "zia"}`)
- Modify: `packs/zia/pack.json` (`lookup_sources` gains
  `"zia_location_groups": {"name_field": "name"}`; `references` gains
  `"location_groups.id": {"referent": "zia_location_groups",
  "name_field": "name"}` on `zia_url_filtering_rules`,
  `zia_cloud_app_control_rule`, `zia_dlp_web_rules`,
  `zia_ssl_inspection_rules`)
- Modify: `packs/zia/overrides/{zia_url_filtering_rules,zia_cloud_app_control_rule,zia_dlp_web_rules,zia_ssl_inspection_rules}.json`
  (remove `location_groups.name` from `acknowledged_drops` — the
  reference now owns the field)
- NOT modified: `zia_location_management`'s own
  `static_location_groups.name` / `dynamiclocation_groups` drops stay
  (referencing from location-management is follow-on pack work)
- Test: the pack-qualification lane's existing zia coverage picks this
  up; add/refresh expectations only where that lane's suite demands

**Interfaces:**
- Consumes: everything above, unchanged.
- Produces: the four rule types resolve location-group references
  through tokens/lookups/data-root remote state on a real pack.

- [ ] **Step 1: Apply the pack edits exactly as specified.**
- [ ] **Step 2: Run the zia pack-qualification/check lanes**
  (`iw check-pack` + the transform corpora tests that cover zia rule
  types); expected: schema path validation passes against
  `data_source_schemas`; any golden/stderr drift is REPORTED, not
  auto-accepted — list every changed byte for coordinator triage.
- [ ] **Step 3: Report diff + drift inventory** (coordinator triages,
  commits, and owns the adversarial-review gate — pack goldens are
  review-required surfaces).

---

## Self-review notes

- Spec coverage: invariants 1-4 → Tasks 1-5; invariant 5 + Refresh
  strategy → Task 6; pack motivating case → Task 7; separation rule →
  Task 1 validation (mutual exclusion) + Task 2 (referent-only role);
  acceptance criteria map to Task 5's four test groups; out-of-scope
  items have no tasks (correct).
- Ordering: strictly 1→2→3→4→5→6→7; Tasks 3 and 4 are independent of
  each other (parallelizable after 2); 5 needs 3+4; 6 needs 2; 7 needs
  all.
- Engine fixtures are provider-neutral (`sample_*`); provider names
  appear only in Task 7 pack work and Task 3's golden (which pins the
  real zia data source deliberately — it is the motivating contract).
  If the reviewer prefers a neutral Task 3 golden, swap the fixture
  type name; the mechanism is name-agnostic.
