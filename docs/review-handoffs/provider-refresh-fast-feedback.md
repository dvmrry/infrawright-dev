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

- Base: `22b5b95ccd90132efb5446798ad6eddc91b7bc99` (the pushed, unmerged
  `feature/zpa-provider-4.4.9` tip).
- Head: `38f0ae1de838c6e8de5fa2b4722df9ee30d617a9`.
- Diff command: `git diff 22b5b95ccd90132efb5446798ad6eddc91b7bc99..38f0ae1de838c6e8de5fa2b4722df9ee30d617a9`.

## Files Changed

- `go/cmd/iw/v2_vertical_slice_test.go`: extracts the ZPA portal cardinality
  contract into a standalone Terraform test that generates one module and
  runs before the checkpoint's binary build and 151-module sweep.
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
- Other source evidence: `go/internal/modulesgen` generation behavior and the
  existing V2 checkpoint Terraform harness.

## Generated Artifacts

- Reports: none.
- Schemas: none changed.
- Fixtures: none changed.
- Snapshots: none changed.
- Demo or lab outputs: none committed. The focused test generates only
  `zpa_policy_portal_access_rule` in a temporary directory.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: `TestZPAPortalCapabilityCardinalityTerraform` runs
  without `INFRAWRIGHT_V2_CHECKPOINT`, CLI build, fetch fixture, deployment, or
  full module generation. The full checkpoint executes the same contract
  first. A schema refresh now fails until its semantic transitions and
  dispositions form an exact set.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: runtime CLI behavior, pack selection, transform and
  adoption, provider pins, generated modules, and module snapshots.

## Invariants Claimed

- Evidence must not be silently dropped: the current ZPA semantic projection,
  with every disposition reversed, must reproduce the pinned ZPA 4.4.6
  projection hash. An optional previous-schema input also compares the literal
  transition set and reports missing or stale dispositions.
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
  execution.

## Tests Run

- Commands:
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache go test -count=1 -run '^TestZPAPortalCapabilityCardinalityTerraform$' ./cmd/iw`
  - `ZPA_PREVIOUS_PROVIDER_SCHEMA=/tmp/infrawright-zpa-446-schema.XXXXXX.json go test -count=1 -run '^TestProvider449SchemaTransitionDispositionsAreExact$' ./internal/authoring/zpacorpus`
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache make check`
  - `TF_PLUGIN_CACHE_DIR=/tmp/infrawright-terraform-plugin-cache INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `go vet ./...`; `gofmt -l` on both changed Go files; `git diff --check`.
- Relevant output summary: focused portal contract passed in about 3 seconds;
  exact 16-transition comparison passed in under 1 second; `make check` passed
  in 49.66 seconds; the sole full promotion passed 151/151 generated modules,
  20/20 demo roots, and the HCL-tfvars plan in 399.07 seconds. Vet, formatting,
  and diff hygiene passed.
- Focused regression and pre-fix/unsafe-mutation proof:
  - Replacing the tuple singleton marker with the previous `max_items = 1`
    object behavior made the standalone Terraform contract fail; restoring the
    safe implementation made it pass.
  - Mutating one committed transition path made the schema test report the
    current-value mismatch, reconstructed-hash mismatch, missing real
    transition, and stale fake transition; restoring it made the test pass.
- Promotion efficiency: the first candidate and corrected review-ready
  implementation are both `38f0ae1de838c6e8de5fa2b4722df9ee30d617a9`
  (zero post-candidate correction time); one full-corpus Terraform sweep was
  attempted and it passed.
- Tests not run and why: no credentialed provider or tenant tests were run;
  runtime behavior and provider data did not change. `make check` constituents
  were not rerun individually after the passing superset.

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
  Terraform helpers in `go/cmd/iw/v2_vertical_slice_test.go`.
- Specific assumptions to attack: the focused test truly sheds checkpoint
  prerequisites; its counterexamples fail for semantic reasons rather than
  incidental setup; reversing dispositions plus the baseline hash rejects all
  undispositioned and stale transitions; JSON semantic projection is stable;
  and the process additions remain narrow enough to be followed.
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
