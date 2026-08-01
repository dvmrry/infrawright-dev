# Adversarial Review 1: test-audit cleanup branch

Recorded verbatim from the fresh-context Codex review of
`faa132b1..aecc1c6` (before the handoff commit `5f661f1`), per
docs/adversarial-review-template.md structure. Reviewer made no
repository changes.

## Verdict: Request changes

Two independently reproduced blockers make the branch not merge-ready.

## Blocking finding 1: state-aware filtering can launder conflicting bindings

Commit `a4564af` replaced the mutating `ApplyExpressionBindings`
validation with independent target checks in
`ValidateExpressionBindingTargets`.

For overlapping bindings:

- Operator parent: `server_groups = local.operator_services`
- Generated child: `server_groups[0].id = data.terraform_remote_state...`

The observed behavior is:

| Mode | Base `faa132b1` | Head `aecc1c6` |
|---|---|---|
| State-blind | Rejects before output | Rejects later, after writing `main.tf` |
| State-aware, absent state | Rejects before probing | **Succeeds after dropping the generated child** |

The base mutating walk replaces the parent first, causing child traversal
to fail with `indexes a non-list value`. The new walk validates both
paths independently against untouched config. State-aware filtering then
removes the child before rendering detects the conflict.

This directly violates the invariant documented in
`environment_generator.go:1769-1779`: filtering must not convert
malformed evidence into a successful literal fallback.

Required remediation:

- Explicitly reject ancestor/descendant binding conflicts across the
  complete merged binding set before state filtering or output writes.
- Add the exact mixed operator/generated regression.
- Assert state-blind and state-aware runs produce the same pre-probe
  refusal and that the state probe is never called.
- Prove the test fails against `aecc1c6`.

## Blocking finding 2: a load-bearing comparator regression was deleted

Commit `c816f18` deletes `TestEqualTreesComparesKeySets`, but
`equalTrees` remains the oracle behind six complete generated-tree
comparisons.

The deleted test specifically guarded a historical defect where
same-sized trees with different empty-file names compared equal. The
reviewer restored that exact unsafe comparator in a disposable head
export and ran `go test ./internal/envgen -count=1`: every remaining
envgen test passed. The deleted test was therefore the sole
discriminator; it was not tautological or ceremonial.

Required remediation:

- Restore `TestEqualTreesComparesKeySets`, or replace all six helper
  calls with direct full-map comparisons such as `cmp.Diff`.
- Require the historical left-key-only mutation to fail.

## Non-blocking findings

- The set-block golden is stronger than the previous self-derived
  comparison for its single-member case, but it does not cover
  multi-member separators. Removing the `", "` separator in
  `renderSetBlockLeaf` leaves all current set-block tests green while
  producing invalid adjacent object expressions. A two-member golden
  should pin delimiter and order.
- The set-block fixture is provider-coded (`zia_*`, `zscaler/zia`) but
  remains a synthetic pack; it does not load or couple against the
  committed real provider corpus. Provider-neutral naming remains a
  worthwhile follow-up.
- Live comments still reference removed behavior:
  `environment_generator.go:42` names `applyExpressionBindings`;
  `transform_artifacts.go:3-7`, `1733-1734`, and `2117-2119` describe
  the deleted batch subsystem.
- `TransformArtifactCompileOptions.LookupOverrides` now has no
  production assignment after batch deletion; only tests populate it.
  Document it as a deliberate test seam or remove the remaining dead
  plumbing.

## Confirmed sound changes

- Transactional batch compilation/publication had no production
  callers; live transform/adopt paths use the retained sequential APIs.
- The asynchronous assessment-finalizer guard was unreachable through
  both concrete production finalizers.
- `ResourceDescriptor.Derived` was never read.
- All 16 command facades were moved unchanged into test scope.
- The deleted standalone `terraform_data` test exercised Terraform but
  no repository production code.
- The canonjson lossless fixture gate still covers all 29 applicable
  flat and lookup fixtures.
- No committed reports, schemas, fixtures, snapshots, provider
  metadata, or artifact bytes changed.

## Validation (reviewer-run)

- `go test ./...` — pass; `go build ./...` — pass; `go vet ./...` —
  pass; `gofmt` — clean; `git diff --check` — clean.
- Both set-block seam mutations — correctly caught.
- Parent/child state-aware reproducer — base rejects, head succeeds.
- Historical `equalTrees` mutation — all remaining envgen tests
  incorrectly pass.
- The green suite does not cover either blocker.
