# A2 adversarial review — mapper and evaluator

## Blocking Findings

- Finding: the original publisher created a staging directory and only bound
  its identity in a later operation. A writer able to change the parent could
  replace that name before binding, causing A2 to operate on and later remove
  a directory it did not create.
- Source evidence: the original candidate's `bundle.go` separated `Mkdir` from
  `bindDirectory`; its stage-rebind test attacked only after binding.
- Impact: the claimed fail-closed publication boundary was not enforceable.
- Required change: eliminate the pre-bind adoption window or remove the
  overclaimed publisher.
- Suggested regression test or verification: prove that A2 has no filesystem
  publication surface. The accepted correction removed the publisher entirely;
  publication is now separately owned by A6.

## Non-Blocking Risks

- Risk: the frozen evaluator authority contained four cases, while the first
  test candidate replayed only three and did not lock the complete case set.
- Source evidence: `authoring_cli_artifact_set` was present in
  `python-source-evidence-eval-v1.json` but absent from the package replay.
- Why it is non-blocking: the missing vector was a coverage gap, not an observed
  behavior mismatch; CLI routing remains A6 work.
- Suggested follow-up: replay the embedded evaluator JSON and Markdown and
  assert the exact authority case vocabulary. The builder added that test; the
  reviewer re-ran it and approved the correction.

## Source Evidence Review

- Diff inspected: A2 working tree from base
  `113bd8e5365d8ba1c1637a994e6a943634229204`, including `sourceanalysis` and
  the complete new `sourceoperation` package.
- Handoff inspected: `go-authoring-a2-source-operation.md`.
- Provider schemas inspected: frozen fixture schema bindings used by the A1/A2
  source-first corpus.
- OpenAPI/API contracts inspected: frozen v1 mapper/evaluator authorities;
  A2's v2 path was confirmed to emit only the ordinary `absent` state.
- Provider source inspected: the synthetic source-first fixture and the
  mapper's provider-source discovery/replay inputs.
- Pack metadata inspected: not an A2 input.
- Fixtures or snapshots inspected: all 39 frozen derive cases, all 10 named
  in-memory Node differential cases, and all four evaluator cases.
- Missing evidence or review gaps: none after the evaluator replay amendment.

## Generated Artifact Review

- Reports reviewed: frozen legacy mapper/evaluator JSON and Markdown outputs;
  v2 report use was checked for isolation from legacy mapping.
- Schemas reviewed: existing source evidence/provenance contracts; no schema
  change in A2.
- Fixtures reviewed: both frozen Node authority fixtures and their pinned
  SHA-256 values.
- Snapshots reviewed: no existing snapshot was changed.
- Count/coverage deltas reviewed: legacy code has no call path into v2 source
  counts; unverified evidence forces every `legacy_mapped` value false.
- Artifact drift accepted or rejected: no frozen-authority drift; the added v2
  goldens were left to the independent bundle/artifact review.

## Verdict

- Approve

Verdict rationale: exact replay covers the 39 derive, 10 in-memory, and four
evaluator authorities; legacy behavior is quarantined from v2 readiness; and
the publication blocker was removed rather than papered over. Focused tests,
race tests, vet, and diff checks passed. The reviewer made no edits.
