# Builder Review Handoff: test-audit cleanup branch

Per docs/review-handoff-template.md. Builder: Claude (Fable 5), branch
`claude/test-audit-cleanup-ddd009`. High-risk trigger: golden-fixture
change (rewritten set-block render-derivation golden) and
generated-binding validation change.

## Intent

- What problem does this change solve? Executes the safe tier of the
  2026-08-01 test/cruft audit: removes production code with no callers
  (tfrender batch subsystem, async-finalizer guard,
  ResourceDescriptor.Derived, envgen transformed-items model), relocates
  16 test-only command facades into a test file, and deletes or
  strengthens tautological / non-discriminating tests.
- What user-visible or maintainer-visible behavior should change?
  None at the CLI or artifact level. Test suite shrinks by ~2,000 LOC.
- What behavior must stay unchanged? All committed-artifact bytes
  (tfvars, imports, moves, lookups, generated roots), all binding
  validation error codes/messages/ordering, all sequential
  compile/publish semantics, all CLI command behavior.

## Base / Head

- Base: `faa132b1c1177ea34a24ba669b9448cc7f77441e` (main)
- Head: `aecc1c6` (claude/test-audit-cleanup-ddd009, 7 commits)
- Diff command: `git diff faa132b1...aecc1c6` (run inside `go/`'s parent
  worktree)

## Files Changed

- Files: 34 files, +206/−2,157. Production: tfrender
  transform_artifacts.go + doc.go, assessment assessment.go, roots
  roots.go, metadata resource_set.go, envgen expression_bindings.go +
  environment_generator.go + reference_topology.go, canonjson
  artifact.go, 7 cmd/iw command files. Tests: corresponding _test.go
  files plus new cmd/iw/command_facades_test.go and rewritten
  envgen/set_block_render_derivation_test.go.
- Files intentionally left untouched: fingerprint_hcl.go and its tests
  (wrong-behavior findings deferred to the adversarial-review workflow),
  all provider corpora and archive fixtures
  (environment_roots_compatibility.json, OpenAPI oracle), import_staging
  delimiter handling, terraformcmd timeout contract, docs/superpowers
  historical specs/plans that name the old ApplyExpressionBindings API.

## Source Inputs Consulted

- Provider schemas: None (no schema content changed).
- OpenAPI/API contracts: None.
- Provider source files: None.
- Pack metadata: Synthetic in-test pack fixtures only
  (syntheticSetBlockPackRoot, provider_neutral_reference testdata).
- Existing docs or design records: docs/superpowers specs for reference
  tokens and sidecar minimization (read to confirm the batch subsystem
  and transformed-items model were port artifacts, not roadmap items).
- Other source evidence: repo-wide grep for production callers of every
  deleted symbol (recorded per-commit in commit messages).

## Generated Artifacts

- Reports: None changed.
- Schemas: None changed.
- Fixtures: None changed on disk.
- Snapshots: One test golden REWRITTEN, not regenerated:
  set_block_render_derivation_test.go's expected expression_bindings.tf
  moved from engine-derived-both-sides comparison to a hand-pinned
  literal (setBlockExpectedBindingsTF).
- Demo or lab outputs: None changed.
- Artifact drift intentionally expected: None. Any drift in committed
  artifacts is a defect.

## Expected Delta

- Expected behavior change: None in production paths.
- Expected report/count/coverage changes: Test count drops (deleted
  tautological tests, batch-subsystem tests, one benchmark). No
  provider-readiness or evidence count changes.
- Expected generated-output changes: None.
- Expected no-op areas: All envgen generation output bytes; all
  tfrender sequential publish bytes; all CLI exit codes and stdout.

## Invariants Claimed

- Evidence must not be silently dropped: No evidence-path code changed;
  assessment change removes only an unreachable guard after the
  finalize step.
- Generic matcher evidence must not outrank source-backed evidence: N/A
  (no matcher changes).
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success:
  N/A.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: Lookup-key stranding refusals retained and
  still covered via sequential CompileTransformArtifacts tests; the two
  deleted reference_tokens tests pinned batch-membership semantics of
  the deleted batch entry point only.
- Additional claims to attack:
  1. ApplyExpressionBindings' transformed map had no production
     consumer; ValidateExpressionBindingTargets preserves the identical
     path-walk, error messages, and error ordering, and parse-time
     validateExpression (parseBinding) already covers the sentinel
     constructor's expression re-validation.
  2. The hand-pinned set-block golden observes both the tfrender
     composite-expression seam and the envgen resolver-template seam.
  3. Every deleted symbol has zero remaining references in go/.

## Tests Run

- Commands: `go build ./...`, `go vet ./...`, `go test ./...` (all
  packages), plus focused runs per touched package during development.
- Relevant output summary: All green at head; vet clean; gofmt clean.
- Focused regression and pre-fix/unsafe-mutation proof: Three unsafe
  mutations against
  TestSetBlockRenderDerivationEmitsCompleteLeafExpression:
  (a) tfrender bindSetBlockField composite render `"["` -> `"[ "`:
  FAILS the test; (b) envgen resolver template
  `try(${0}, local.iw_reference_lookup_${1}[${2}])` gains a `, null`
  arm: FAILS; (c) [reproduced independently by an earlier reviewer]
  removing set-block registration: FAILS with list-index rendering.
  Note one rejected inert mutation: changing the element join separator
  passes because the fixture list has a single element — reviewers
  should treat separator-sensitive coverage as absent, not proven.
- Promotion efficiency: first candidate commit to corrected
  review-ready tip within one working session (2026-08-01); full-suite
  `go test ./...` sweeps: 3 (all passing; no failed or interrupted
  attempts).
- Tests not run and why: No live-tenant or external-audit lanes (skip in
  standard runs; unaffected paths).

## Known Deferrals

- Deferred work: fingerprint HCL-semantics fixes; metadata.ResourceSet
  lane removal (needs ~22 roots tests retargeted to loaded-pack
  fixtures); provider-coupling of the set-block fixture (still names
  zia_* types); duplicate-suite consolidations; runtime optimizations
  (shared iw test binary, in-process fmt); archive/oracle policy
  decisions.
- Reason it is safe to defer: Each is behavior-changing or
  policy-dependent; this branch is deletions/relocations of verified
  dead or non-discriminating surface only.
- Follow-up owner or trigger: Task chips filed for fingerprint fix and
  ResourceSet lane; remainder awaits maintainer triage of the audit
  report.

## Review Focus

- Highest-risk files or paths: go/internal/envgen/expression_bindings.go
  (validation rewrite); go/internal/envgen/set_block_render_derivation_test.go
  (new hand-pinned golden); go/internal/tfrender/transform_artifacts.go
  (large deletion adjacent to the live sequential publish path).
- Specific assumptions to attack: the three "Invariants Claimed"
  additional claims above; also that deleted reference_tokens batch
  tests carried no invariant not covered sequentially.
- Source evidence the reviewer should verify: production caller greps
  for CompileTransformArtifactBatch / PublishCompiledTransformArtifactBatch /
  ApplyExpressionBindings / asyncAssessmentFinalizerValue /
  ResourceDescriptor.Derived; parse-time validateExpression coverage in
  parseBinding.
- Generated artifacts the reviewer should compare: the hand-pinned
  setBlockExpectedBindingsTF against a fresh generation run.
- Edge cases that could silently overclaim, remap, drop, or weaken
  evidence: a binding-validation behavior difference reachable through
  validateBindingsAgainstConfig; loss of the committed-cache-wins
  coverage for set-block shapes (claimed covered by
  TestCommittedBindingsCacheWinsOverDerivation, which is shape-agnostic).
