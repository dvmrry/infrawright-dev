# Builder Response to Adversarial Review 1

Per docs/adversarial-review.md step 5, each accepted finding maps
`finding -> root cause -> fix -> regression test -> verification`.
Head under recheck: branch `claude/test-audit-cleanup-ddd009` after this
response's commits (base unchanged: `faa132b1`).

## Blocking finding 1 — state-aware filtering launders conflicting bindings

- Finding: accepted as reported.
- Root cause: the validation-only rewrite preserved every per-binding
  check of the mutating walk but lost an emergent property: applying the
  ancestor binding's sentinel broke the descendant binding's traversal,
  so ancestor/descendant pairs in the merged set were refused at the
  pre-filter config gate. Independent validation let both pass; the
  conflict then surfaced only at render time (state-blind, after
  writes) or never (state-aware absent-state, descendant dropped).
- Fix: `validateExpressionBindingTargets` now refuses ancestor/descendant
  `PathParts` overlaps within the merged set explicitly
  (`pathPartsIsStrictPrefix`), before the per-target walk, with a
  deterministic "conflicting expression binding X overlaps Y" message.
  This fires at the same pre-filter, pre-write gate the base semantics
  fired at (`validateBindingsAgainstConfig`).
- Regression test:
  `TestStateAwareStillRefusesOverlappingParentAndChildBindings`
  (state_aware_test.go) pins the reviewer's exact mixed shape (operator
  `server_groups` parent + generated `server_groups[0].id` child),
  asserts the state-blind and state-aware refusals are byte-identical,
  that no `main.tf` is written in either mode, and that the state probe
  is consulted zero times. Unit cases for object-path, list-index, and
  non-conflicting sibling-index pairs were added to
  `TestBindingPathValidationRejectsUnknownMissingConflicts`.
- Verification: the regression was run against the unfixed head first
  and failed both ways (state-blind: `main.tf` written before refusal;
  state-aware: generation returned nil error). With the fix it passes,
  and the full envgen suite passes, confirming no legitimate fixture
  relied on overlapping bindings.

## Blocking finding 2 — load-bearing comparator regression deleted

- Finding: accepted as reported. The builder's "tautological" judgment
  was wrong: the test guards the comparator contract itself, and the
  reviewer proved it was the sole discriminator.
- Root cause: the deletion pass classified a meta-test of the oracle as
  proving only `reflect.DeepEqual` semantics, missing that the test's
  subject is `equalTrees`'s contract, not its current implementation.
- Fix: `TestEqualTreesComparesKeySets` restored verbatim.
- Regression test: the restored test is the regression.
- Verification: the historical left-key-only comparator mutation was
  re-applied; the restored test fails against it and passes after
  revert.

## Non-blocking findings

- Two-member set-block golden: the fixture now carries two set members
  (`service_one`, `service_two`); the hand-pinned bytes include the
  `", "` member delimiter and member order. The previously inert
  join-separator mutation (`", "` -> `","`) now fails the golden;
  verified applied-then-reverted.
- Stale live comments: transform_artifacts.go's file header, the
  key-stranding exemption rationale, and LookupKeyMaps's doc no longer
  describe the deleted batch subsystem; environment_generator.go's
  port-mapping comment now notes applyExpressionBindings maps to the
  validation-only `ValidateExpressionBindingTargets`.
- `LookupOverrides`: documented on
  `TransformArtifactCompileOptions` as a deliberate test seam (its only
  production writer was the removed batch compiler); production callers
  leave it nil. Removal of the plumbing was deferred: it threads through
  the lookup-resolution chain (`lookupKeyMaps`/`resolveLookup`), which
  is adoption-safety-adjacent and deserves its own focused change.
- Provider-neutral fixture naming: deferred with the existing
  provider-corpus relocation follow-up, as the review allowed.

## Observation surfaced by the fix (pre-existing, both base and head)

`validateBindingsAgainstConfig` skips validation entirely for HCL-format
tfvars ("validation reads json only"), in base and head alike, so the
new conflict gate — like every config-gate check — does not run on the
HCL lane; those conflicts surface at render time. This is base-parity
behavior, not a regression introduced or fixed by this branch; noted
here as a candidate follow-up.

## Verification summary at response head

`gofmt` clean; `go build ./...`, `go vet ./...`, `go test ./...` all
pass. Recheck request scope per review-loop step 6: the changed surface
only — `expression_bindings.go` (conflict gate), `state_aware_test.go`
(regression + restored guard), `expression_bindings_test.go` (unit
conflict cases), `set_block_render_derivation_test.go` (two-member
golden), and the comment/doc-seam edits in `transform_artifacts.go` /
`environment_generator.go`.

---

# Recheck 1 verdict and Builder Response 2

Recheck verdict: Request changes — one blocker: the all-pairs overlap
preflight ran before individual target validation, changing promised
validation error messages and ordering (an earlier binding's
missing-target-leaf refusal was displaced by a later pair's overlap;
even a standalone overlap reported the new conflict message instead of
the mutating walk's shape-specific one). The recheck confirmed both
original blockers genuinely closed.

## Response (commit 8c8f55c)

- Finding: accepted.
- Root cause: the preflight loop evaluated all pairs before any
  per-target walk, inverting first-failing-binding-wins ordering.
- Fix: overlap detection moved inside the sequential walk (per binding,
  against its predecessors, before that binding's own walk). This
  matches the mutating walk's precedence for the ancestor-first
  direction exactly: the sentinel collision always fired at
  ancestor-leaf depth during the descendant's in-order walk, before any
  deeper error of its own.
- Regression tests: the earlier-target-error-beats-later-overlap case
  (the reviewer's `aaa` shape) and the reverse
  (earlier-overlap-beats-later-target-error), both in
  TestBindingPathValidationRejectsUnknownMissingConflicts.
- Verification: the preflight shape was re-applied as a mutation and
  fails the new precedence case; reverted, the full suite passes.

## Contract deltas awaiting explicit maintainer approval

Two deliberate divergences from the mutating walk remain (documented on
validateExpressionBindingTargets); both are refusals of the same
malformed evidence, differing in message or direction only:

1. Message: a standalone overlap reports "conflicting expression binding
   X overlaps Y" instead of the mutation artifact the sentinel happened
   to produce ("indexes a non-list value" / "missing parent path").
2. Direction-independence: the mutating walk refused only when the
   ancestor was applied before the descendant in merged order, and
   silently accepted the descendant-first order; the gate now refuses
   both orders.
