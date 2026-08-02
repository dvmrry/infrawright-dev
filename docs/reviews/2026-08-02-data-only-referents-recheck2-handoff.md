# Builder Review Handoff: sixth-recheck closure candidate

This handoff describes exactly the committed candidate under review and
supersedes all earlier versions of this file.

## Base / Head

- Base: `89f8bc19` (sixth-recheck closure).
- Head: the commit carrying this handoff (seventh-recheck closure);
  the pinned hash is recorded in
  docs/reviews/2026-08-02-data-only-referents-review-record.md, written
  immediately after this commit.
- Diff command: `git diff 89f8bc19..HEAD`.
- Exactly four paths change in this closure:
  1. `go/internal/plan/testdata/provider_double_capture/validate_captures.py`
     — rewritten as the EXACT seven-scenario contract: per-scenario
     actions, before/after maps, planned-output agreement, prior-state
     instance maps with canonical addresses, type/mode/name, provider
     address, schema_version 0, requested-name equality, and the
     deterministic v1/v2 IDs; the postcondition check pinned by address
     with top-level and per-instance pass and the exact instance count
     (including empty_for_each's zero-instance check); zero planned data
     resources and zero non-no-op resource changes everywhere; refresh-
     pair identity modulo exactly one timestamp per file. Committed
     capture bytes are unchanged and validate against this contract.
  2. `go/internal/plan/testdata/provider_double_capture/gen-captures.sh`
     — `--recover` now decides a full-set outcome before copying: it
     requires a TRANSACTION record explicitly in `state=promoting`
     (a completed promotion refuses; delete such a backup deliberately),
     exactly one previous-file-or-missing-marker per scenario for all
     seven, refuses incomplete or contradictory inventories without
     altering live files or the backup, restores all seven, validates
     the restored set through the shared validator, and only then
     removes the recovery material; any failure retains the backup.
  3. `go/internal/plan/capture_validation_test.go` — the fault-injection
     suite now exercises the full recovery semantics: full-backup
     restore-and-validate, incomplete-backup refusal with live files
     proven untouched and backup retained, missing-TRANSACTION refusal,
     and completed-promotion refusal; the corrupted-set validator
     refusal test is unchanged.
  4. This handoff.

## Claims corrected from the previous version

- The staged validator is no longer "parse/complete/version + pair
  identity": it is the exact per-scenario contract enumerated above.
- The recovery claim is no longer "restores from backup": it is the
  full-set transactional semantics enumerated above.
- The title and range now name the candidate actually under review.

## Capture set

Unchanged in this closure. Current SHA-256 prefixes (unchanged from the
fifth-recheck regeneration): empty_for_each `c7eefc99653c6152`,
initial_create `7960e936800b6301`, no_op `4ef1a3e9b3852d33`,
refresh_false `0861a1038deaee89`, refresh_id_change `b39ede33d1fd244d`,
refresh_true `0861a1038deaee89` (this generation's pair is byte-identical
including the timestamp), rekey_refusal `896a4502dd4a1cf7`.

## Tests run

- Focused: `go test ./internal/plan/ -run 'TestCaptureValidator|TestCaptureRecovery' -count=1` — pass (all recovery subcases).
- Shared validator over the committed set: `capture set valid`.
- Full corpus `go test ./... -count=1` under canonical GOCACHE/GOTMPDIR
  paths: green; gofmt/vet clean.

## Known deferrals (unchanged)

- Same-case duplicate names introduced tenant-side after publication
  remain a documented provider-source residual.
- The attestation trust boundary is engine-provenance, not writer
  authentication (documented; same trust class as tfplan.sources).
