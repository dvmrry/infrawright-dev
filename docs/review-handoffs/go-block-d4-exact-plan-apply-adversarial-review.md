# Block D4 Exact Saved-Plan Apply — Adversarial Review

## Blocking Findings

### Typed plan gate was absent

- Finding: the first candidate passed only the lossless `canonjson.Value` to
  `ClassifyPlan`; it did not layer the authorized `terraform-json` typed plan
  boundary on top.
- Source evidence: the original `ExactPlanApplyTerraform.Show` returned only
  `canonjson.Value`, unlike D1's dual decode in `internal/adopt/oracle.go`.
- Impact: the Apply safety spine did not independently require
  `tfjson.Plan.Complete != nil && *Complete` as authorized.
- Required change: return typed plus lossless plan views and require the typed
  complete pointer before classification or either unsafe override.
- Regression: missing/false/null/wrong-type complete values and an injected
  typed/raw mismatch all reject with both broad overrides enabled and zero
  Apply calls.

### Post-Apply failures misreported the committed count

- Finding: successful Terraform Apply followed by saved-pair or scratch cleanup
  failure returned `Applied: 0`.
- Source evidence: the original root helper returned only an error and the
  outer loop incremented after the helper returned nil.
- Impact: an operator could mistake a post-effect cleanup error for a pre-Apply
  refusal and retry an already-committed operation.
- Required change: return the committed phase separately and increment the
  result before propagating post-Apply failures.
- Regressions: cleanup refusal and saved-pair removal failure both return
  `Applied: 1`; Apply failure plus cleanup refusal keeps `Applied: 0` and the
  primary error.

### Git branch fallback did not match the bounded process contract

- Finding: the first candidate discarded Git stderr, accepted nil environment
  snapshots, and did not terminate overflowing or descendant-held streams.
- Source evidence: Node's `execFile(..., maxBuffer: 64 * 1024)` applies to both
  streams; the original Go adapter bounded stdout only. The first correction
  continued draining after overflow and could wait forever on inherited pipes.
- Impact: a local Git fallback could authorize a branch Node rejects, or hang
  the Apply command before the branch gate completed.
- Required change: reject nil environments, independently retain at most 64 KiB
  per stream, cancel on first overflow, and bound post-cancel inherited pipes
  with `Cmd.WaitDelay`.
- Regressions: explicit stdout and stderr overflow, a non-exiting direct writer,
  and a descendant retaining output pipes all return `unknown` promptly; an
  allocated empty environment still uses the injected Git fallback.

## Non-Blocking Risks

None remain. The final reviewer nit—only stderr had an explicit overflow
regression—was closed by adding the symmetric stdout case and re-reviewing it.

## Source Evidence Review

- Diff inspected: the complete uncommitted D4 production and test files against
  base `4f59c5e`.
- Handoff inspected: `go-block-d4-exact-plan-apply-review.md`.
- Source inspected: `node-src/domain/exact-plan-apply.ts`, plan-contract,
  evidence, lifecycle, branch, and assessment cleanup primitives.
- Dependency boundary inspected: `terraform-json` typed plan plus unchanged
  `canonjson` lossless classification; existing `terraformcmd` retained.
- Missing evidence or review gaps: none for the fixture-only D4 parcel. CLI and
  frozen-Node differential coverage remain D5 scope.

## Generated Artifact Review

- Reports, schemas, fixtures, and snapshots changed: none.
- Artifact drift: rejected; all four standing RootCatalog, Transform, Topology,
  and Generation byte gates pass unchanged.
- Saved-plan cleanup: only `tfplan` and `tfplan.sources` are removed after a
  successful Apply; an unrelated generated file is preserved by regression.

## Verification

- Focused D4 tests: pass.
- Focused and full assessment race tests: pass.
- `go test -count=1 ./...`: pass.
- `go vet ./...`: pass.
- `gofmt` and `git diff --check`: clean.
- RootCatalog/Transform/Topology/Generation byte gates: pass.
- Two independent no-edit reviews completed; final verdicts are Approve.
- No host Terraform, credentials, provider API, network request, remote state,
  or real/live Apply was used. Every Apply execution was an injected fake or an
  explicit temporary fake executable against ephemeral fixture state.

## Verdict

Approve.

The complete gate is dual typed/lossless and precedes every override; freshness
and multi-root retention remain fail closed; cleanup is randomized and
descriptor-bound; committed Apply counts survive post-effect cleanup errors;
and the branch fallback is independently bounded on both streams without an
inherited-pipe hang.
