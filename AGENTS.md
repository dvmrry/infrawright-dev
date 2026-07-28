# Repository Agent Instructions

## Repository Context Safety

Before branching, editing files, staging, committing, pushing, opening a PR,
marking a PR ready, or merging, compare the user's requested repository,
project, branch, and PR context with the active workspace and git remote.

If the user is clearly referencing a different repo, PR series, project, or
workspace than the current one, stop and say so before making changes. Do not
perform repo-changing actions until the user confirms the intended workspace.

When a prompt names a repository explicitly, treat repo verification as a
required preflight check.

## Adversarial Review

Use the Codex-only adversarial review workflow in
[docs/adversarial-review.md](docs/adversarial-review.md) for high-risk
agent-built changes. This is process scaffolding only; do not add hard hooks,
CI gates, Claude/Fable configs, Opus-specific files, or PR-template changes
unless the user explicitly asks for them.

The builder must stop at "ready for adversarial review" and must not
self-approve high-risk changes. The builder produces a handoff using
[docs/review-handoff-template.md](docs/review-handoff-template.md).

The reviewer must run in a fresh Codex context, use
[docs/adversarial-review-run-prompt.md](docs/adversarial-review-run-prompt.md),
and record findings with
[docs/adversarial-review-template.md](docs/adversarial-review-template.md).
The reviewer must not edit files or implement fixes.

Treat these changes as review-required:

- OpenAPI extraction, parsing, normalization, validation, or emitted structure.
- Provider source-operation mapping.
- Provider-readiness logic, reports, coverage, or count accounting.
- Generated evidence, reports, schemas, fixtures, or snapshots.
- Golden fixtures or snapshot drift.
- Generic matcher versus source-backed evidence behavior.
- Source precedence, provenance, or ambiguity classification.
- Adapter-specific provider edge cases.
- Code that can silently drop, overclaim, remap, or weaken evidence.
- Identity-key derivation or identity-to-state-address mapping.
- Import-ID derivation or import-ID-to-state-address mapping.
- `import {}` generation, filtering, staging, preservation, reconciliation, or removal.
- `moved {}` generation, preservation, suppression, reconciliation, or removal.
- Saved-plan classification, including policy-tolerance decisions.
- Apply guardrails, saved-plan authority checks, or destructive-action controls.

Routine docs-only edits, typo fixes, or narrow README updates do not need the
full workflow unless they alter process, claims, generated-output
interpretation, or source-evidence meaning.

## Validation Promotion

- Full-corpus suites are promotion gates, not discovery tools. Before running
  one, add and pass an independently runnable focused regression for every new
  invariant, and prove that regression fails against the pre-fix behavior or a
  faithful unsafe mutation.
- If a full-corpus gate fails, reproduce the defect with a focused command
  before editing or rerunning the corpus. Do not rerun checks already covered
  by a passing superset on the same commit.
- After one attempted fix fails to establish the same invariant, stop patching
  and return to design before another broad validation pass.
- During a provider refresh, adding a key to `overrideKeys` extends the pack
  contract vocabulary. Stop and obtain explicit scope direction before making
  that change; touching a generic implementation file alone is review context,
  not an automatic stop.
