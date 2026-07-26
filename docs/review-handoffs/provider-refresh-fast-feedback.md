# Provider Refresh Fast-Feedback Handoff

## Intent

- Reduce provider-refresh diagnosis time by making the decisive Terraform
  contract independently runnable before the full generated corpus.
- Make every ZPA 4.4.6-to-4.4.9 schema transition carry an exact, committed
  disposition, with both missing and stale entries rejected.
- Record a small validation-promotion contract: focused mutation-proven tests
  discover defects; full suites promote a candidate once.
- Preserve production behavior, generated module output, provider pins, pack
  metadata, and the existing review gate.

## Base / Head

- Base: `f0701aabc6b58b9d09c5fa13a558c079bc20e444` (the pushed, reviewed
  `feature/zpa-provider-4.4.9` tip).
- Original implementation: `38f0ae1de838c6e8de5fa2b4722df9ee30d617a9`.
- Review correction: `8ef47bf91a19c26fb2cff0491ad926c5377163df`.
- Updated-base merge: `6913c25d8040585c0df47588cc4e6bca92099171`.
- Handoff: the branch tip has a documentation-only commit updating this file
  after the updated-base merge.
- Diff command: `git diff f0701aabc6b58b9d09c5fa13a558c079bc20e444..6913c25d8040585c0df47588cc4e6bca92099171`.

## Files Changed

- `go/cmd/iw/v2_vertical_slice_test.go`: generates only the ZPA portal module
  and runs its real-provider-shape contract before the checkpoint's binary
  build and 151-module sweep.
- `go/internal/modulesgen/generator_test.go`: adds the independently runnable,
  synthetic, pack-independent Terraform contract for singleton module input
  encoding. It initializes no provider and performs no registry download.
- `go/internal/authoring/zpacorpus/provider_refresh_test.go`: commits the exact
  16 schema-transition dispositions and enforces them against a semantic
  projection of the provider schema.
- `AGENTS.md`, `docs/adversarial-review.md`, and
  `docs/review-handoff-template.md`: add the narrow promotion, mutation-proof,
  and measurement rules.
- `docs/review-handoffs/zpa-provider-v4.4.9.md`: records the 99-minute/seven-
  sweep baseline contemporaneously reconstructed from that refresh.
- Files intentionally left untouched: production generator, metadata,
  transform, and pack code; provider schemas and overrides; CI workflows;
  generated modules, fixtures, and snapshots.

## Source Inputs Consulted

- Provider schemas: ZPA 4.4.9 at
  `packs/zpa/schemas/provider/zpa.json`; the same file at base commit
  `5db8ff747f5cdbe768e60905d581d0738ea24127` for the ZPA 4.4.6 semantic
  projection.
- OpenAPI/API contracts: none; this change does not alter API mappings.
- Provider source files: no new provider-source claims were introduced. The
  disposition text preserves the evidence decisions recorded in
  `docs/review-handoffs/zpa-provider-v4.4.9.md`.
- Pack metadata: `packs/zpa/zpa.packset.json`, `packs/zpa`, and
  `packs/_shared/zscaler`, loaded through the production metadata loader by the
  focused test.
- Existing docs or design records: `docs/adversarial-review.md`, the builder
  and reviewer templates, and the ZPA 4.4.9 review handoff.
- Other source evidence: `go/internal/modulesgen` generation behavior, the
  synthetic-pack helper, the empty-pack distribution contract, and the
  existing V2 checkpoint Terraform harness.

## Generated Artifacts

- Reports: none.
- Schemas: none changed.
- Fixtures: none changed.
- Snapshots: none changed.
- Demo or lab outputs: none committed. The cheap contract generates one
  synthetic module in a temporary directory; the checkpoint generates only
  `zpa_policy_portal_access_rule` before entering its existing corpus setup.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: `TestModuleSingleBlocksTerraformCardinality` runs
  without a pack, provider plugin, network access, checkpoint opt-in, CLI
  build, fetch fixture, deployment, or full module generation. It skips when
  Terraform is absent, matching the repository's existing formatter
  cross-checks. The full checkpoint executes the real ZPA integration first.
  A schema refresh now fails until its semantic transitions and dispositions
  form an exact, well-formed set.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: runtime CLI behavior, pack selection, transform and
  adoption, provider pins, generated modules, and module snapshots.

## Invariants Claimed

- Evidence must not be silently dropped: the current ZPA semantic projection,
  with every disposition reversed, must reproduce the pinned ZPA 4.4.6
  projection hash. An optional previous-schema input also compares the literal
  transition set and reports missing or stale dispositions. The always-on path
  rejects empty dispositions, duplicate paths, no-op entries, current-value
  mismatches, and empty before/after values.
- Generic matcher evidence must not outrank source-backed evidence: unchanged;
  no matcher or evidence-precedence code changed.
- Source precedence/provenance must remain explicit: unchanged; disposition
  text does not create new evidence claims.
- Ambiguity must stay classified instead of being coerced to success:
  unchanged.
- Provider-readiness counts must stay explainable: unchanged; no registry or
  readiness output changed.
- Adoption safety invariants: unchanged; no adoption or transform path changed.
- Terraform cardinality: one `privileged_portal_capabilities` element plans;
  two elements and the prior keyed-object bypass fail before provider
  execution. The semantic projection retains every non-documentation schema
  field recursively, including nested attribute types, `sensitive`,
  `deprecated`, and resource schema versions.

## Tests Run

- Commands:
  - `go test -count=1 -run '^TestModuleSingleBlocksTerraformCardinality$' ./internal/modulesgen`
  - `ZPA_PREVIOUS_PROVIDER_SCHEMA=/tmp/infrawright-zpa-446-schema.XXXXXX.json go test -count=1 -run '^TestProvider449SchemaTransitionDispositionsAreExact$' ./internal/authoring/zpacorpus`
  - `go test -count=1 -run '^(TestProvider449SchemaTransitionDispositionsAreExact|TestRefreshTransitionDispositionsRejectMalformedEntries|TestRefreshSemanticProjectionIncludesGeneratorConsumedAttributeSemantics)$' ./internal/authoring/zpacorpus`
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache make check`
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `/usr/bin/env -u TF PATH=/etc/profiles/per-user/dm/bin:/usr/bin:/bin /run/current-system/sw/bin/make check-core`
  - `go vet ./...`; `gofmt -l` on all changed Go files; `git diff --check`.
- Relevant output summary: the synthetic Terraform contract passed in under 1
  second with no provider installation; exact 16-transition comparison passed
  in under 1 second; `make check` passed the initial candidate in 49.66
  seconds; the sole full promotion passed 151/151 generated modules, 20/20
  demo roots, and the HCL-tfvars plan in 399.07 seconds. After review
  correction, `make check-core` passed against a physical empty pack root with
  Terraform absent from `PATH` in 27.49 seconds. Vet, formatting, and diff
  hygiene passed.
- Updated-base integration: merging the reviewed ZPA tip preserved all four
  remediation commits and left the child diff at the same eight files, 864
  additions, and 14 deletions. The focused singleton clone/cardinality tests
  and the disposition well-formedness/projection tests all passed in 2.6
  seconds; `git diff --check` and worktree status were clean. The full corpus
  was not repeated because the parent correction and child implementation had
  already passed their promotion gates independently and the merge was clean.
- Focused regression and pre-fix/unsafe-mutation proof:
  - Replacing the tuple singleton marker with the previous `max_items = 1`
    object behavior made the synthetic contract fail its accepted singleton
    case with `object required, but have tuple`; restoring the safe
    implementation made it pass.
  - Emptying one committed disposition made the normal, no-environment schema
    test fail immediately. Synthetic guards also reject exact/conflicting
    duplicate paths and no-op entries. The previous path mutation still
    reports the current-value/hash mismatch plus missing/stale transitions.
- Promotion efficiency: first candidate
  `38f0ae1de838c6e8de5fa2b4722df9ee30d617a9` at 16:54:38 -0400 to corrected
  review-ready implementation `8ef47bf91a19c26fb2cff0491ad926c5377163df`
  at 17:19:15 -0400 was 24m37s. One full-corpus Terraform sweep was attempted;
  no sweep was repeated after review findings.
- Tests not run and why: no credentialed provider or tenant tests were run;
  runtime behavior and provider data did not change. The full checkpoint and
  `make check` were not repeated after the test-only correction: the affected
  contracts were reproduced with focused commands and `check-core`, as the
  new promotion rules require.

## Review Corrections

- Unconditional pack-specific test -> the cheap test coupled the Go suite to
  repository ZPA and provider download -> moved the invariant to a synthetic
  pack-independent generator contract, retained real ZPA only in the opt-in
  checkpoint -> proved with the Terraform-absent empty-root `check-core` run.
- Optional-only disposition validation -> well-formedness lived below the
  previous-schema environment guard and the semantic projection was too
  narrow -> made validation unconditional and recursively retained all
  non-documentation schema semantics -> proved with malformed-ledger mutations
  and synthetic `sensitive`, `deprecated`, and nested-type transitions.

## Known Deferrals

- Deferred work: cross-family transition tests, Terraform sweep parallelism,
  plugin-cache concurrency, a standalone schema-delta CLI, additional CI jobs,
  and a hand-maintained run ledger.
- Reason it is safe to defer: none is required to make the two observed failure
  modes cheap and deterministic. The transition walker is schema-shaped rather
  than Zscaler-shaped, while the committed dispositions intentionally live
  beside the ZPA refresh test that owns them.
- Follow-up owner or trigger: reconsider only when a later provider refresh
  supplies measured evidence that the focused contract plus exact transition
  set did not isolate the defect cheaply.

## Review Focus

- Highest-risk files or paths:
  `go/internal/authoring/zpacorpus/provider_refresh_test.go` and the focused
  Terraform contract in `go/internal/modulesgen/generator_test.go`.
- Specific assumptions to attack: the synthetic test has no pack/provider
  dependency and its counterexamples fail on the asserted tuple diagnostic;
  the real ZPA checkpoint still runs first; reversing well-formed dispositions
  plus the baseline hash rejects all undispositioned and stale transitions;
  recursive semantic projection is deterministic; and the process additions
  remain narrow enough to be followed.
- Source evidence the reviewer should verify: the exact base/current schema
  transition set, current portal module generation path, and the original
  unsafe cardinality encoding.
- Generated artifacts the reviewer should compare: the temporary focused
  module against the production-generated portal module shape; no committed
  artifact drift is expected.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  added/removed nested blocks, ownership-only attribute changes, duplicate or
  empty dispositions, absent-versus-empty projection values, and environments
  where Terraform is not available.
