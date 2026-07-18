# Block C3 Bounded Assessment Remnants Review Handoff

## Intent

- Bound the portable scrubbed-inode remnants deliberately retained by the
  accepted saved-plan assessment transaction.
- Permit runner and CLI adoption without allowing successful assessments to
  accumulate unbounded temporary-directory entries.
- Preserve descriptor-bound content scrubbing, fail-closed cleanup, zero
  ordinary results on failure, and the rule that no existing pathname is
  inspected, removed, or reused.

## Base / Head

- Base: `4754e29` (`Port saved-plan assessment transaction to Go`).
- Head: uncommitted working-tree parcel.
- Diff command: `git diff -- go/internal/assessment/assessment.go` plus
  `git diff --no-index /dev/null` for the two new files listed below.

## Files Changed

- Files: `go/internal/assessment/assessment.go`,
  `go/internal/assessment/assessment_remnants.go`, and
  `go/internal/assessment/assessment_remnants_test.go`; this handoff records
  the parcel and review-finding resolution.
- Files intentionally left untouched: assessment cleanup, transaction,
  report, input, evaluation, policy, and guidance implementations; runner and
  CLI; plan/control evidence; Node sources; dependencies.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: Block C handoff, the accepted Block C3
  assessment transaction handoff/review, and its mandatory pre-runner bounded
  remnant gate.
- Other source evidence: accepted assessment transaction and descriptor-bound
  cleanup implementation; Go `os.Mkdir` atomic creation semantics.

## Generated Artifacts

- Reports: None changed.
- Schemas: None changed.
- Fixtures: None changed.
- Snapshots: runtime-only scrubbed saved-plan snapshot remnants.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: production assessments claim one of 32
  deterministic private slots under `os.TempDir`; the 33rd transaction fails
  closed until an operator removes retained slots.
- Permission behavior: `os.Mkdir` requests mode `0700` atomically. The process
  umask may make a newly claimed slot stricter. Production performs no
  pathname `chmod`, so a same-UID replacement or symlink cannot redirect a
  post-claim permission mutation.
- Expected report/count/coverage changes: none before exhaustion; exhaustion
  is a fixed redacted I/O failure with a zero ordinary assessment result.
- Expected generated-output changes: None.
- Expected no-op areas: saved-plan bytes, classifications, report bytes,
  diagnostics, existing cleanup order, evidence barriers, and all Node code.

## Invariants Claimed

- Evidence must not be silently dropped: slot exhaustion occurs before saved
  plan evidence or snapshot preparation.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success: all
  non-`EEXIST` creation errors and full-slot exhaustion return the same fixed
  fail-closed `ASSESSMENT_TEMPORARY_DIRECTORY_UNAVAILABLE` I/O failure.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: slots are claimed atomically with `os.Mkdir`
  requesting mode `0700`, which the process umask may only make stricter;
  production never widens permissions with a pathname `chmod`; existing
  files, directories, and symlinks are skipped without inspection, mutation,
  deletion, or reuse; concurrent processes cannot claim the same slot;
  ordinary results and partial roots remain zero when creation fails before
  assessment begins. A restrictive umask may make a slot unusable, in which
  case assessment fails safely and that claimed slot remains consumed.
- Operational bound: at most 32 assessment-created directories and 32,000
  scrubbed zero-length snapshot inodes can remain, using the existing
  1,000-root transaction ceiling. Slot capacity is not automatically renewed.

## Tests Run

- Commands: focused slot and transaction tests; focused 20-run repeat; focused
  race tests; `go test -count=1 ./...`; `go vet ./...`; gofmt/diff checks;
  Linux, FreeBSD, and Windows assessment-package cross-compilation; focused
  Node `plan-assessment` suite.
- Relevant output summary: all Go gates passed; all cross-compiles passed;
  Node 17/17 passed; the post-claim replacement regression leaves the injected
  symlink and its `0755` target unchanged and performs zero evidence-preparation
  calls; no dependency changes.
- Tests not run and why: no real provider/API call is involved in temporary
  evidence storage; no credentialed test is relevant to this parcel. Tests do
  not mutate the process-global umask because that would race unrelated test
  work; direct mode checks prove claimed directories are never broader than
  the requested `0700`, and source inspection proves there is no widening
  pathname operation.

## Review Finding Resolution

- Blocking finding: the original parcel retained the assessment transaction's
  pathname `os.Chmod(temporary, 0o700)` after the deterministic slot claim. A
  same-UID actor could swap the claimed leaf for a symlink before that call and
  redirect the mode change to a replacement target.
- Exact fix: removed the post-claim pathname `chmod`. `os.Mkdir(candidate,
  0o700)` is now the only production permission operation. The umask may make
  the new directory stricter but production never widens it afterward.
- Regression: a production-order hook atomically creates the claimed path,
  renames it, installs a symlink to a fixture target, and returns. Assessment
  rejects the unsafe directory before evidence preparation and proves the
  symlink target remains at `0755`. This test would fail against the original
  post-claim `chmod` implementation.

## Known Deferrals

- Deferred work: automatic janitor or safe slot reuse.
- Reason it is safe to defer: capacity is hard bounded and exhaustion fails
  before sensitive snapshot creation; no pathname deletion or reuse is
  attempted. The operational cost is explicit: the 33rd transaction requires
  operator cleanup.
- Follow-up owner or trigger: revisit only if 32 retained transactions is too
  small operationally or a portable descriptor-bound directory-deletion API
  becomes available; do not add a pathname janitor as an incidental fix.

## Review Focus

- Highest-risk files or paths: `assessment_remnants.go` and the production
  `makeTemporary` hook in `assessment.go`.
- Specific assumptions to attack: `EEXIST` classification on every target
  platform; atomicity across goroutines/processes; deterministic capacity;
  local pre-seeding with files/symlinks/directories; mode no broader than the
  requested `0700`; absence of post-claim pathname permission changes; fixed
  error redaction; exhaustion ordering before evidence preparation; no
  mutation of prior slots or a swapped symlink target.
- Source evidence the reviewer should verify: existing assessment transaction
  call order and cleanup contract; Go filesystem semantics for the supported
  build targets.
- Generated artifacts the reviewer should compare: no tracked artifacts;
  inspect retained runtime entries for zero-length regular snapshots only.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  treating permission/I/O failures as occupied slots, following a pre-seeded
  symlink, duplicate concurrent claims, leaking the temp root in errors,
  creating evidence before capacity failure, or accidentally deleting an
  attacker replacement.
