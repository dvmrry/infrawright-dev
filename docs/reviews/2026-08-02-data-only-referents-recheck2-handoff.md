# Builder Review Handoff: fourth-recheck closure candidate

Supersedes the earlier pre-coordinator versions of this file, which
described uncommitted working trees. This handoff describes the exact
committed candidate under review.

## Base / Head

- Base: `472316ef` (second-recheck closure).
- Intermediate: `f375ca3d` (third-recheck closure: attestation trust
  boundary documented, refresh-pair reframed, data-module name
  postcondition, transactional script v1).
- Head: the commit carrying this handoff (fourth-recheck closure).
- Diff command: `git diff 472316ef..HEAD`.

## What the fourth-recheck closure changes

- Capture tests are mandatory: a missing or under-shaped committed
  capture FAILS the focused gate (no skips), so the fixture set cannot
  be weakened silently.
- The refresh-pair regression pins the full semantic matrix before the
  byte comparison: Terraform/format versions, complete/errored, zero
  failing lifecycle checks, output action update with the exact v1
  before and v2 after ID maps, the exact prior-state data address and
  ID, zero planned data resources, zero non-no-op resource changes,
  and byte identity after normalizing exactly one timestamp.
- Version qualification is exact: plan creation and attestation
  validation accept exactly Terraform 1.15.4 (the only capture-
  qualified release), not the 1.15.x range. Widening requires
  re-running the capture matrix under the new release first (see the
  capture README).
- gen-captures.sh refuses and strips every `TF_CLI_ARGS*` variant
  (including `TF_CLI_ARGS_apply`, which reaches the four
  state-establishing applies), sets `LC_ALL=C`, validates the complete
  staged set semantically (parse, complete, not errored, qualified
  version, refresh-pair identity modulo one timestamp) BEFORE touching
  any tracked fixture, and keeps its backup set plus a TRANSACTION
  record outside the trap-deleted work directory so interruption
  mid-promotion leaves deterministic recovery state. The backup is
  removed only after complete promotion.

## Regeneration record

- Command: `sh go/internal/plan/testdata/provider_double_capture/gen-captures.sh`
  run by the coordinator with no inherited `TF_CLI_ARGS*`, Terraform
  v1.15.4 (script-enforced), provider double built from the committed
  module. Result: `ALL-CAPTURED`, backup directory removed (complete
  promotion).
- All seven committed captures were regenerated in this run (the
  third-recheck postcondition changes the data-module configuration,
  so every capture legitimately drifts to include Terraform `checks`
  records; initial_create additionally carries two items).
- Post-run SHA-256 prefixes:
  - empty_for_each `f18a91036ee9e8c0`
  - initial_create `4347fab4a0988c25`
  - no_op `de95d2c0becc56be`
  - refresh_false `59b5050d3dd8c0e3`
  - refresh_id_change `3e536f681f6fddad`
  - refresh_true `2032a23ddc8f2814`
  - rekey_refusal `be99377b9bc7b7ea`
  - refresh_false and refresh_true differ only in the single
    timestamp (script-validated during promotion; test-validated by
    the pair regression).

## Files changed in this closure

- go/internal/plan/attestation.go (exact 1.15.4 qualification set with
  the widening procedure documented)
- go/internal/plan/lifecycle.go (same exact-version check at plan
  creation)
- go/internal/plan/contract_data_referent_test.go (mandatory captures;
  refresh-pair semantic matrix)
- go/internal/plan/evidence_test.go (version-message expectation)
- go/internal/plan/testdata/provider_double_capture/gen-captures.sh
  (environment allowlist, staged validation, recoverable transaction)
- all seven capture show.json files (regenerated as recorded above)
- this handoff (rewritten for the committed candidate; prior
  deferral statements that are now complete were removed)

## Tests run after regeneration

- Focused: `go test ./internal/plan/ -count=1` — pass, including
  TestValidateAssessmentPlanAcceptsProviderDoubleCaptures (all
  scenarios, no skips), the refresh-pair matrix regression, and the
  rekey refusal.
- Full corpus: `go test ./... -count=1` — green locally (coordinator
  environment, including the two loopback-listener tests worker
  sandboxes cannot run); gofmt/vet clean.

## Known deferrals (current)

- Same-case duplicate names introduced tenant-side after publication
  remain a documented provider-source residual (the postcondition
  catches case-different matches; transform refuses case-fold
  collisions in committed artifacts).
- The attestation's trust boundary is engine-provenance, not writer
  authentication (documented in attestation.go and the spec; same
  trust class as tfplan.sources).
