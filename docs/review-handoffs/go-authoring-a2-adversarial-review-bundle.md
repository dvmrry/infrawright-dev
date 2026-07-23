# A2 adversarial review — bundle and generated artifacts

## Blocking Findings

- Finding: the original directory publisher could promote staged files after
  they changed, and its stage, target, backup, restore, and cleanup operations
  retained pathname check-to-action races against an active same-UID writer.
- Source evidence: staged bytes were validated before the final directory
  identity check, but not made immutable; ordinary `os.Root.Rename` and
  `Remove` remained overwrite-capable pathname actions after separate checks.
- Impact: publication could return success for bytes other than the validated
  bundle or overwrite/remove a rebound path, contradicting its integrity and
  ownership claims.
- Required change: use an enforceable ownership/concurrency model and portable
  primitives that support the claims, or remove publication from A2.
- Suggested regression test or verification: confirm there is no A2 publisher,
  filesystem transaction helper, production write path, or publication claim.
  The accepted correction took that smaller route and assigns publication to
  A6 for separate review.

## Non-Blocking Risks

- Risk: the original publisher could ignore cancellation after its final
  pre-commit check.
- Source evidence: cancellation was checked before commit preparation but not
  at the later rename boundaries.
- Why it is non-blocking: removal of the entire publisher eliminated the path.
- Suggested follow-up: A6 must define cancellation semantics together with its
  explicit publication ownership/concurrency contract.

## Source Evidence Review

- Diff inspected: A2 working tree from base
  `113bd8e5365d8ba1c1637a994e6a943634229204`, before and after the compile-only
  correction.
- Handoff inspected: `go-authoring-a2-source-operation.md`, including the
  corrected compile-only boundary.
- Provider schemas inspected: the source-first v2 fixture's pinned schema and
  source report binding.
- OpenAPI/API contracts inspected: the existing absent-document diagnostics
  contract; no adapter/parser behavior was introduced.
- Provider source inspected: the synthetic pinned provider/SDK fixture through
  the A1 report and provenance chain.
- Pack metadata inspected: not an A2 input.
- Fixtures or snapshots inspected: source-first expected report/provenance,
  three new hand-authored v2 goldens, and their recorded hashes.
- Missing evidence or review gaps: none after the obsolete publication review
  bullets were removed from the handoff.

## Generated Artifact Review

- Reports reviewed: `source-registry.json`, `source-diagnostics.json`,
  `summary.json`, `summary.md`, `input-provenance.json`, and absent
  `openapi-diagnostics.json`.
- Schemas reviewed: existing strict source-evidence, provenance, and OpenAPI
  diagnostics contracts; no schema delta.
- Fixtures reviewed: the three new A2 goldens independently against the frozen
  A1 fixture and recorded hashes.
- Snapshots reviewed: no existing snapshot drift.
- Count/coverage deltas reviewed: the derived views retain the eight-row
  partition, source-call total 4, endpoint total 2, and coverage 2/7; mutation
  tests reject an altered derived classification.
- Artifact drift accepted or rejected: the three new derived goldens are
  accepted; all existing source and Node authorities remain unchanged.

## Verdict

- Approve

Verdict rationale: A2 now exposes only capability-gated compilation, private
raw input, detached artifact copies, and exact six-artifact validation. There
is no publisher or filesystem write path left to overclaim. Focused tests,
race tests, and vet passed; the handoff nit was corrected. The reviewer made no
edits.
