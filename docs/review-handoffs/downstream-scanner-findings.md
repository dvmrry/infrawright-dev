# Downstream Scanner Findings Review Handoff

## Intent

- Evaluate four downstream scanner findings against `origin/main` rather than
  treating scanner suggestions as confirmed defects.
- Preserve the provider-agnostic engine boundary: no core regression may load,
  name, mirror, or otherwise depend on a committed real-provider corpus.
- Add focused coverage for supported wildcard nested-block omission, pin the
  deliberate exact-index deferral, and exercise the whole physical
  pack-load-to-generated-environment path using only a test-owned neutral pack.
- Keep production behavior and every real provider pack, schema, fixture,
  snapshot, and generated artifact unchanged.

## Finding Dispositions

| Finding | Disposition | Remediation or evidence |
|---|---|---|
| `unsupported_if` rejects a rule containing both `match` and `match_any_nonempty` | Not a defect | The pack-authoring contract requires exactly one predicate. Loader and runtime tests already reject the mixed form. No behavior change. |
| Generated block omission ignores nested indexes or sibling counts | Not reproduced for supported wildcard selectors; exact-index rewriting is deliberately deferred | The rewriter records each block's sibling index in its descendant path. One exact-output test covers two repeated parents and three repeated children; another proves `optional_block[0].child` makes zero generated-config edits and is not marked by that phase because a stable collection index does not yet exist. The authoring contract now states that boundary. |
| Topology tests lost committed-provider corpus coverage | Narrow whole-chain coverage concern accepted; proposed real-provider remedy rejected | Disk-backed synthetic pack loading and pack-to-module generation already had coverage. The new value is one corpus-independent test spanning `LoadPackRoot -> GenerateActiveModules -> GenerateEnvironmentRoots -> cross-state binding`. It loads a committed test-only neutral pack, generates the consumer dependency closure, and asserts the source output, consumer remote state, expression binding, and the fixture-specific smoke override. It never compares a generic graph with a provider graph. |
| `module_single_blocks` marker can leak through a reused schema | Not reproduced | The renderer deep-clones maps and slices, keeps the marked schema inside an unexported render context, and already tests that the cached provider schema contains neither the old `max_items` overlay nor the marker. No behavior change. |

## Base / Head

- Base: `origin/main` at
  `2e28c5e04a841fe7ecc29ba612d21b4298a1a61a`.
- Head: branch `fix/downstream-scanner-findings`, currently represented by the
  working tree over the base above.
- Tracked diff command: `git diff origin/main -- docs/pack-authoring.md go/internal/adopt/generated_config_policy_test.go`.
- New-file review command: `git status --short` followed by direct inspection
  of `docs/review-handoffs/downstream-scanner-findings.md` and
  `go/internal/envgen/provider_neutral_reference_test.go` plus
  `go/internal/envgen/testdata/provider_neutral_reference/`.

## Files Changed

- Files:
  - `docs/pack-authoring.md`
  - `go/internal/adopt/generated_config_policy_test.go`
  - `go/internal/envgen/provider_neutral_reference_test.go`
  - `go/internal/envgen/testdata/provider_neutral_reference/README.md`
  - `go/internal/envgen/testdata/provider_neutral_reference/packs/reference.packset.json`
  - `go/internal/envgen/testdata/provider_neutral_reference/packs/fixture/pack.json`
  - `go/internal/envgen/testdata/provider_neutral_reference/packs/fixture/registry.json`
  - `go/internal/envgen/testdata/provider_neutral_reference/packs/fixture/schemas/provider/fixture.json`
  - `docs/review-handoffs/downstream-scanner-findings.md`
- Files intentionally left untouched: all production Go files; all real
  provider packs, schemas, overrides, evidence, generated artifacts, fixtures,
  and snapshots; the pre-existing untracked `reports/` directory.

## Source Inputs Consulted

- Provider schemas: only the new test-owned `fixture_source` and
  `fixture_consumer` schema. No real provider schema is loaded by either new
  test.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: the new test-owned neutral pack plus existing pack-authoring
  validation rules. Real pack metadata inspected during triage is not an input
  to the final regression.
- Existing docs or design records: `docs/pack-authoring.md`,
  `docs/adversarial-review.md`, and PR #263's provider-decoupling contract.
- Other source evidence: `AdoptionUnsupportedRules`, `validateAdopt`,
  `rewriteGeneratedConfig`, `applyModuleSingleBlocks`, their existing focused
  tests, and the full call graph through the unexported module render context.
- External design reference: HashiCorp Terraform's providerless
  `internal/command/e2etest/testdata/terraform-managed-data/main.tf`, repository
  commit `12f1c196099fd23c62f8234ea9a43a50ce3dd0f2`, Git blob
  `271888e6a10a7ab0542d9403aca19be96546f0c9`, plus the official
  `terraform_data` built-in-resource contract. No external HCL file is copied.
  The Go test writes its own two-resource configuration at runtime and treats
  it as providerless execution evidence, not a substitute topology oracle.

## Generated Artifacts

- Reports: None.
- Schemas: one minimal test-only fixture schema under the envgen testdata tree.
- Fixtures: one minimal test-only packset and pack with exactly two generated
  resource types and one declared reference edge.
- Snapshots: None.
- Demo or lab outputs: None committed. The focused test generates modules and
  environment roots only below `t.TempDir()`.
- Artifact drift intentionally expected: only the new test-owned fixture files
  listed above.

## Expected Delta

- Expected behavior change: None; this is test hardening and finding triage.
- Expected report/count/coverage changes: four additional Go regression tests
  (including one Terraform-backed execution test) and one Terraform parsing
  subtest; no product report, provider-readiness, or inventory count changes.
- Expected generated-output changes: None outside temporary test directories.
- Expected no-op areas: runtime adoption classification, generated-config
  rewriting, module rendering, pack loading, topology resolution, and every
  real-provider artifact.

## Invariants Claimed

- Evidence must not be silently dropped: no evidence or production path is
  modified.
- Generic matcher evidence must not outrank source-backed evidence: the
  documented exclusive predicate contract remains unchanged.
- Source precedence/provenance must remain explicit: no source or provenance
  data is modified.
- Ambiguity must stay classified instead of being coerced to success: mixed
  unsupported predicates continue to fail validation.
- Provider-readiness counts must stay explainable: no readiness inputs or
  counts change.
- Adoption safety invariant: a wildcard nested-block omission removes every
  matching repeated child, preserves nonmatching parent siblings, records the
  expected edit count, and marks its policy entry matched.
- Exact-index safety invariant: generated-config rewriting does not guess that
  `[0]` identifies a stable HCL sibling; it makes zero edits and leaves the
  exact-index policy entry unmarked by that phase.
- Module safety invariant: module singleton markers remain detached from cached
  provider schema authority.
- Test-boundary invariant: the new environment regression loads only a
  test-owned neutral pack whose reserved `registry.invalid` source must never
  be initialized. It does not depend on or assert anything about a real
  provider.
- Cross-state invariant: selecting `fixture_consumer` expands
  `fixture_source`, emits a stable-key output from the source root, and binds
  the consumer through declared remote state.
- Terraform execution invariant: Terraform parses the neutral generated HCL;
  a separately runtime-written `terraform_data` graph initializes, validates,
  and plans with only `terraform.io/builtin/terraform`, while retaining a real
  source-to-consumer expression dependency.

## Tests Run

- Commands:
  - `go test ./internal/envgen -run '^(TestProviderNeutralReferenceBuildExercisesPackLoadAndCrossStateGeneration|TestProviderlessTerraformDataReferencePlansWithBuiltInProvider)$' -count=1 -v`
  - `go test ./internal/adopt -run '^TestGeneratedConfigProjectionOmit(NestedRepeatedBlocks|DefersExactIndexedBlock)$' -count=1 -v`
  - `go test ./internal/adopt -run '^TestAdoptionUnsupportedRuleRejectsAmbiguousPredicateWithoutLoaderValidation$' -count=1 -v`
  - `go test ./internal/metadata -run '^TestUnsupportedAdoptionMetadataClosedVersionScopedForbiddenOnDerived$' -count=1 -v`
  - `go test ./internal/modulesgen -run '^(TestCloneModuleSchemaValueDetachesNestedMapsAndSlices|TestModuleSingleBlocksConstrainGeneratedShapeWithoutMutatingProviderSchema)$' -count=1 -v`
  - `INFRAWRIGHT_PACKS=<empty-temp-dir> go test ./internal/envgen -run '^(TestProviderNeutralReferenceBuildExercisesPackLoadAndCrossStateGeneration|TestProviderlessTerraformDataReferencePlansWithBuiltInProvider)$' -count=1 -v`
  - `go test -count=1 ./...`
  - `go vet ./...`
  - `gofmt -d .`
  - `git diff --check`
- Relevant output summary: every focused test passed; the exact uncached full
  Go suite passed; vet, formatting, JSON parsing, provider-boundary scanning,
  and tracked/untracked whitespace checks were clean.
- Focused regression and pre-fix/unsafe-mutation proof:
  - Removing the sibling index from descendant block paths in an isolated base
    archive made the nested omission test fail with zero edits instead of
    three, retained all three child blocks, and reported the policy entry
    stale.
  - Allowing an exact-index selector into generated-config rewriting made the
    deferral test edit the first repeated parent and mark the entry matched.
  - Removing the neutral fixture's sole `references` entry in an isolated copy
    made the environment regression fail closed: the generated consumer
    binding to `fixture_source` was not declared by pack metadata.
  - Replacing the runtime `terraform_data.consumer` input reference with a
    literal still produced a valid plan but made the execution test fail its
    source-dependency assertion.
- Providerless Terraform execution is now part of the Go test suite rather
  than a one-off shell transcript. The test runs `init`, `validate`, `plan`,
  and `show -json`, then verifies that exactly two creates use
  `terraform.io/builtin/terraform` and that the consumer expression references
  the source output. No external provider appears in the plan.
- Promotion efficiency: four builder full-corpus invocations occurred across
  the redesign and review correction: the obsolete real-provider candidate
  passed once; the first neutral tree passed once with Go's cache enabled and
  once with `-count=1`; the revised review-corrected tree then ran exactly once
  with `-count=1` and passed in about 24 seconds. Independent reviewers' own
  verification runs are not counted here. The cached run was not treated as a
  promotion gate.
- Tests not run and why: no live-provider, committed-real-provider, or external
  provider-plugin initialization test is appropriate for this regression. The
  Terraform-only checks use the package's existing skip-when-unavailable local
  convention; the primary CI `make check` job installs Terraform 1.15.4 before
  running `go test -count=1 ./...`, so they execute in the promotion gate.

## Known Deferrals

- Deferred work: no production rewrites for findings 1, 2, or 4; no
  real-provider compatibility assertion for finding 3; no attempt to initialize
  the neutral fixture's deliberately invalid provider source.
- Reason it is safe to defer: findings 1 and 4 are contradicted by the
  documented contract and deep-clone boundary. Finding 2's supported wildcard
  behavior and unsupported generated-config exact-index behavior are both
  pinned explicitly. Finding 3's narrower whole-chain generation concern is
  covered by the neutral committed fixture; Terraform parses that generated
  HCL, while the separate built-in-provider plan qualifies providerless CLI
  execution without pretending to plan the fake-provider modules.
- Follow-up owner or trigger: provider-owning downstream suites remain
  responsible for their provider corpus. Reconsider engine behavior only for a
  provider-neutral reproducer.

## Review Focus

- Highest-risk files or paths: `rewriteGeneratedConfig` path construction and
  the new testdata boundary between provider-neutral engine contracts and
  downstream provider compatibility.
- Specific assumptions to attack: the nested omission test must distinguish
  both parent and child sibling instances; the exact-index test must prove
  deliberate deferral rather than accidental no-op; the environment regression
  must load its physical test pack, expand the declared dependency, and never
  touch a real provider or compare against the existing synthetic graph.
- Source evidence the reviewer should verify: the exact-one predicate contract,
  `parent.Counts[name]` and indexed descendant path construction, deep cloning
  of nested maps and slices, and `LoadPackRoot -> GenerateActiveModules ->
  GenerateEnvironmentRoots` in the new neutral test.
- Generated artifacts the reviewer should compare: the neutral fixture pack,
  registry, and schema only.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  repeated parent blocks, multiple repeated child blocks under one parent,
  stale exact-index policy-entry accounting, an absent neutral reference edge,
  an override assertion that matches the wrong block, a providerless plan with
  no source-to-consumer dependency, or an accidental real-provider source in
  the test fixture.
