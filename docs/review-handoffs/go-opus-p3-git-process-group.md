# Exact-Apply Git branch-probe process-group hardening

Status: APPROVED after changed-surface adversarial re-review. The candidate is
intentionally uncommitted and unpushed.

## Intent

- Solve the one confirmed code-level P3 from the Opus 4.8 whole-refactor
  review: cancellation of the bounded `git rev-parse` branch probe killed only
  its direct process, so a descendant holding inherited pipes could survive.
- On supported POSIX systems, isolate the probe in its own process group, kill
  that group on context cancellation, and require successful final group
  cleanup after `Wait` reaps the direct process.
- Preserve every operator-visible behavior: success still returns the trimmed
  branch; any start, output, cancellation, wait, or cleanup failure still
  resolves to `"unknown"`, which exact Apply refuses outside an explicit
  non-main override.

## Base / Head

- Base: `64419ea3c2277078f9eb81f7db914bc59acd18dd`
- Head: current uncommitted worktree on `feature/go-canonjson-foundation`
- Diff command: `git diff 64419ea --`

## Files Changed

- `go/internal/assessment/exact_plan_apply.go`
- `go/internal/assessment/apply_git_process_posix.go`
- `go/internal/assessment/apply_git_process_other.go`
- `go/internal/assessment/exact_plan_apply_process_posix_test.go`
- This handoff.
- Intentionally untouched: Terraform execution/process-group code, exact-Apply
  classification and gates, HTTP redirects, differential corpora, artifacts,
  reports, schemas, packs, and provider/source authoring code.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: Opus 4.8 whole-refactor review result;
  `docs/adversarial-review.md`; this repository's process-group implementation
  in `go/internal/terraformcmd/process_group_unix.go`.
- Other source evidence: Go 1.26 `os/exec.CommandContext`, `Cmd.Cancel`, and
  `Cmd.WaitDelay` behavior; the existing oversized-stream and
  descendant-held-pipe tests in `exact_plan_apply_test.go`.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: a descendant in the Git probe's POSIX process
  group is killed when overflow cancels the probe or when the direct process
  exits while a descendant remains.
- Expected failure change: a non-`ESRCH` final process-group cleanup error now
  forces the probe result to `"unknown"`; it cannot coexist with an accepted
  branch.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: branch resolution, failure result, exact-Apply safety
  gates, saved-plan bytes, and non-POSIX direct-process cancellation.

## Invariants Claimed

- Evidence must not be silently dropped: unchanged; no evidence code touched.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged.
- Ambiguity must stay classified instead of being coerced to success:
  unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: the branch probe remains bounded and fail-closed;
  its direct process is reaped by `Cmd.Wait`; the POSIX group cleanup cannot
  turn a failed probe into an accepted branch or reach Terraform Apply.

## Tests Run

- `go test -count=1 ./internal/assessment -run 'TestGitApplyBranch' -v`
- `go test -race -count=1 ./internal/assessment -run 'TestGitApplyBranch'`
- `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh --no-bare-return internal/assessment`
- `go test -count=1 ./...`
- `go test -race -count=1 ./internal/assessment`
- `go vet ./...`
- `go test -count=1 ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'`
- Focused result: all existing branch-probe tests pass; the new regression
  proves a nominal 30-second descendant no longer exists roughly 130 ms after
  overflow cancellation.
- Request-changes remediation result: ten focused repetitions and three
  race-enabled repetitions pass. Added regressions prove (1) final cleanup
  kills a detached, stdio-redirected descendant after its successful parent
  exits and (2) an injected non-`ESRCH` final-cleanup error forces `"unknown"`.
- Full result: the complete module suite, assessment race suite, vet, and the
  four standing artifact byte gates are green.

## Known Deferrals

- Deferred work: no process-group emulation on non-POSIX platforms.
- Reason it is safe to defer: those platforms retain Go's direct-process
  cancellation and the existing fail-closed `"unknown"` result; Windows is
  outside the qualified exact-Apply execution surface.
- Follow-up owner or trigger: reconsider only if exact Apply is qualified on a
  non-POSIX platform.

## Review Focus

- Highest-risk paths: `runGitApplyBranch`, the POSIX `Cmd.Cancel` replacement,
  and post-`Run` group cleanup.
- Attack races between context cancellation, direct-process exit, `WaitDelay`,
  and the deferred second group kill.
- Verify `ESRCH` is treated as an already-finished process and that other group
  kill errors cannot produce a successful branch.
- Verify the regression observes descendant disappearance rather than only a
  bounded parent return.
- Confirm no exact-Apply gate, error/exit classification, artifact byte, or
  live-Apply test boundary changed.

## First Review Finding and Remediation

- Verdict: REQUEST CHANGES because the first candidate discarded errors from
  deferred post-`Wait` group cleanup. A successful direct probe could therefore
  return an accepted branch even if a descendant group could not be killed.
- Root cause: `cleanupApplyGitProcessGroup` returned no result and the deferred
  call was documented as best-effort despite the claimed fail-closed contract.
- Fix: final cleanup now returns an error, normalizes an already-empty group to
  success, and a named result is overwritten with `"unknown"` on every other
  cleanup error.
- Tests: added the requested injected-failure regression and an actual
  successful-parent deferred-cleanup regression, alongside the existing
  overflow-cancellation test.
- Changed-surface verdict: APPROVE. The reviewer verified root-cause closure,
  the unchanged exact-Apply branch gate, non-POSIX compilation, and all focused
  tests with no new blocking findings.
