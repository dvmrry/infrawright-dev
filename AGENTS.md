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

Routine docs-only edits, typo fixes, or narrow README updates do not need the
full workflow unless they alter process, claims, generated-output
interpretation, or source-evidence meaning.
