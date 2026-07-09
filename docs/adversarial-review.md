# Adversarial Review Workflow

This is a lightweight Codex-only workflow for high-risk agent-built changes. It
creates a stop point at "ready for adversarial review" instead of letting the
builder self-approve its own work.

This workflow is process scaffolding only. Do not add hard hooks, CI
enforcement, Claude/Fable configs, Opus-specific files, or PR-template churn as
part of adopting it unless a separate change explicitly asks for that.

## When To Use It

Use adversarial review for changes touching:

- OpenAPI extraction, parsing, normalization, validation, or emitted structure.
- Provider source-operation mapping.
- Provider-readiness logic, reports, coverage, or count accounting.
- Generated evidence, reports, schemas, fixtures, or snapshots.
- Golden fixtures or snapshot drift.
- Generic matcher versus source-backed evidence behavior.
- Source precedence, provenance, or ambiguity classification.
- Adapter-specific provider edge cases.
- Code that can silently drop, overclaim, remap, or weaken evidence.

Routine docs-only edits, typo fixes, or narrow README updates do not need the
full process unless they alter process, claims, generated-output
interpretation, or source-evidence meaning.

For especially high-risk changes, run two independent fresh-context reviews
before final acceptance. Examples include broad mapper rewrites, source
precedence changes, provenance changes, ambiguity classification changes, and
large generated artifact churn.

## Builder Contract

The builder:

- Must not self-approve a high-risk change.
- Stops at "ready for adversarial review."
- Produces a handoff from
  [the builder handoff template](review-handoff-template.md).
- Identifies the base and head under review.
- Lists source inputs consulted, generated artifacts, expected deltas,
  invariants, tests run, known deferrals, and review focus.
- Treats its implementation summary as orientation only, not as evidence.

## Reviewer Contract

The reviewer:

- Runs in a fresh Codex context: a new thread or reviewer agent that did not
  implement the change and does not share the builder's implementation
  conversation state.
- Starts from
  [the reviewer run prompt](adversarial-review-run-prompt.md).
- May inspect the diff, handoff, source artifacts, and test commands.
- Must not rely on the builder's implementation summary as evidence.
- Must not edit files or implement fixes.
- Assumes the change is wrong until source-verifiable evidence says otherwise.
- Treats missing source evidence as a review gap.
- Prefers high-confidence findings over broad speculation.
- Rejects stubs, no-op implementations, weakened tests, or comments that paper
  over uncertainty.

Findings must be source-verifiable. Each blocking finding should point to the
diff, source artifact, generated artifact, fixture, test, or command output that
shows the problem.

## Review Loop

1. The builder implements the change and stops before acceptance.
2. The builder completes
   [the handoff template](review-handoff-template.md).
3. A fresh-context reviewer runs
   [the reusable prompt](adversarial-review-run-prompt.md).
4. The reviewer records results with
   [the review template](adversarial-review-template.md).
5. For each accepted finding, the builder maps:
   `finding -> root cause -> fix -> regression test -> verification`.
6. The builder asks the reviewer to recheck only the changed surface.
7. Final acceptance or merge verdict comes after the review/fix loop.

If review catches a repeatable class of miss, update the handoff, run prompt, or
checklist so the next review starts sharper.

## Templates

- [Builder review handoff template](review-handoff-template.md)
- [Adversarial review template](adversarial-review-template.md)
- [Adversarial reviewer run prompt](adversarial-review-run-prompt.md)
