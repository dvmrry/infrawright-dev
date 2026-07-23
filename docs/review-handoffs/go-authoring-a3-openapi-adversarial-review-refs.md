# A3-O adversarial review — parsing and reference boundary

## Blocking Findings

None. The prior empty-query alias finding is remediated.

- `adapter.go:207` rejects `ForceQuery` in the reader.
- `adapter.go:302` rejects it before relative-reference joining.
- Selected path-item `item.json?` is unavailable
  (`capability_yaml_test.go:91`); nested `other.json?` is degraded
  (`analysis_boundary_test.go:242`).

## Non-Blocking Risks

None.

Multi-document YAML was reassessed: the task requires duplicate-key rejection
and passage through the strict JSON-tree boundary, but does not require a
one-document YAML-stream policy. The adapter's parser and kin validation both
operate on the same converted in-memory document, so this is not a finding.

## Source Evidence Review

- Diff inspected: `go.mod`, `go.sum`, A3 task/handoff/manifest updates, and all
  adapter production and test files.
- Handoff inspected: Yes.
- Provider schemas inspected: Not applicable.
- OpenAPI/API contracts inspected: A3 task, roadmap, diagnostics contracts,
  retained Node helper, and kin loader behavior.
- Provider source inspected: Not applicable.
- Pack metadata inspected: Not applicable.
- Fixtures or snapshots inspected: Frozen reconcile fixture and metadata
  compatibility tests.
- Missing evidence or review gaps: None material.

Verified controls include:

- Per-validation random virtual authority plus userinfo/opaque/path/query/
  fragment guards (`adapter.go:189`).
- Literal-space captured keys accepted while encoded path/fragment aliases fail
  (`capability_yaml_test.go:55`).
- YAML preserves overflow and huge-number lexemes without mutating captured
  input (`adapter.go:244`, `capability_yaml_test.go:95`).
- Required closure is limited to path-item resolution and operation identity
  (`compare.go:12`).
- Metadata accepts only `#/...` fragment-local refs (`compare.go:173`,
  `metadata_test.go:60`).

Recheck gates passed: focused and race adapter tests, `go vet`,
`go mod tidy -diff`, offline package/full-suite tests, authoring race tests,
targeted command tests, and `git diff --check`.

## Generated Artifact Review

- Reports reviewed: Sealed in-memory canonical diagnostics.
- Schemas reviewed: Diagnostics decode/seal contract.
- Fixtures reviewed: No changed fixtures.
- Snapshots reviewed: None.
- Count/coverage deltas reviewed: No generated deltas in this parcel.
- Artifact drift accepted or rejected: No drift.

## Verdict

**Approve.**

The prior blocker is fully addressed with guards and state-specific
regressions. The closed loader, YAML, metadata, comparison boundary,
immutability, and offline-build checks now satisfy the A3-O task.
