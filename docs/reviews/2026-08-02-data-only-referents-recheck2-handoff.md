# Builder Review Handoff: fourth-recheck closure candidate

Supersedes the earlier pre-coordinator versions of this file, which
described uncommitted working trees. This handoff describes the exact
committed candidate under review.

## Base / Head

- Base: `f375ca3d` (third-recheck closure), per the review's declared
  recheck scope. The prior closure commit `18f07aa7` and this
  fifth-recheck closure commit together form the delta under review.
- Head: the commit carrying this handoff.
- Diff command: `git diff f375ca3d..HEAD`.
- The seven show.json changes from f375ca3d are semantically
  timestamp-only where regenerated in the same configuration; the
  fifth-recheck run regenerated all seven under the shared validator
  (hashes below).

## What the fourth- and fifth-recheck closures change

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
- Post-run SHA-256 prefixes (fifth-recheck regeneration):
  - c7eefc99653c6152 empty_for_each/show.json
  - 7960e936800b6301 initial_create/show.json
  - 4ef1a3e9b3852d33 no_op/show.json
  - 0861a1038deaee89 refresh_false/show.json
  - b39ede33d1fd244d refresh_id_change/show.json
  - 0861a1038deaee89 refresh_true/show.json
  - 896a4502dd4a1cf7 rekey_refusal/show.json
  - refresh_false and refresh_true differ only in the single timestamp
    (validator- and test-enforced).

## Fifth-recheck additions

- The refresh-pair matrix now also pins exact format_version 1.2, the
  exact postcondition check (address, top-level and instance pass), the
  planned output value, and the prior-state resource's provider,
  schema_version, and full values map.
- Exact-version qualification carries discriminating tables refusing
  1.15.0/1.15.3/1.15.5/1.15.999/1.14.9/1.16.0 at both the attestation
  and plan-creation parsers; the attestation comment and capture README
  say exactly 1.15.4.
- validate_captures.py is the single shared semantic validator (no
  assert statements; PYTHONOPTIMIZE refused by the script) covering the
  full seven-scenario matrix, exercised pre-promotion by the script and
  directly by fault-injection Go tests (corrupted-set refusal;
  --recover restores a backed-up set from its TRANSACTION record and
  refuses recovery without one).

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
