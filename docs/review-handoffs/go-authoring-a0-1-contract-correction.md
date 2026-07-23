# Builder review handoff: Go authoring A0.1 contract correction

## Intent

- Close two representation gaps found during the read-only A1 design audit.
- Bind `reviewed_not_applicable` to the qualified source manifest instead of
  allowing the future analyzer to infer or accept that policy out of band.
- Represent a provider Read call into an SDK package whose source is absent
  from the qualified input without inventing a callee or misclassifying it as
  generic unresolved dispatch.
- Keep all existing synthetic evidence rows, classifications, call chains,
  counts, and endpoint claims unchanged.

## Base / Head

- Base: `86638439a3697af1fc516ed5db422637a0487361`
- Head: uncommitted worktree on `feature/go-canonjson-foundation`
- Diff command: `git diff 86638439a3697af1fc516ed5db422637a0487361 --`

## Files Changed

- `go/internal/authoring/contracts/contracts_test.go`
- `go/internal/authoring/contracts/schemas/source-evidence-report-v1.schema.json`
- `go/internal/authoring/contracts/types.go`
- `go/internal/authoring/contracts/validate.go`
- `tests/fixtures/authoring/source-first-v2/source-provenance-v1.json`
- `tests/fixtures/authoring/source-first-v2/expected/input-provenance.json`
- `tests/fixtures/authoring/source-first-v2/expected/source-evidence-report-v1.json`
- `tests/fixtures/authoring/source-first-v2/expected/openapi-diagnostics-v1.json`
- `tests/fixtures/authoring/source-first-v2/expected/README.md`
- This handoff.
- Files intentionally left untouched: provider/SDK fixture source, `sourcebind`,
  candidate analyzer code, CLI code, OpenAPI implementation, and Node source.

## Source Inputs Consulted

- Provider schemas: the synthetic eight-resource schema already committed in
  A0; no schema bytes changed.
- OpenAPI/API contracts: only the existing absent-OpenAPI diagnostic contract;
  no parser or API behavior changed.
- Provider source files: the committed synthetic provider, specifically the
  selected `sourcefirst_not_applicable` row and the provider-to-SDK call shape
  that A1 must eventually report.
- Pack metadata: N/A.
- Existing docs or design records: `docs/go-authoring-port-roadmap.md`, the A0
  handoff/reviews, and the three A1 read-only audit reports.
- Other source evidence: the qualified-input manifest and its exact embedded
  copy in input provenance.

## Generated Artifacts

- Reports: expected source report and absent-OpenAPI diagnostics were changed
  only to follow their new input/manifest digest links.
- Schemas: the source-report schema adds the closed call-step enum value
  `sdk_source_missing`.
- Fixtures: the independent authority author added the exact selection filter
  `{"name":"reviewed_not_applicable","values":["sourcefirst_not_applicable"]}`.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected:
  - manifest SHA-256: `8d65f243e15f1128f5c32f17c726550ca83f177d30c0ca6bf5d4adf9bbcab99f`
  - input SHA-256: `04ef5f42d6155e877cb6cd29c3fb68211eb3e2e5b9a3a6bcdfaf7ba12283a40f`
  - source report SHA-256: `b98e6252ae0578b4f3abeb993d5ab7d48982fa7b3a3c810f2ffeda47da9e83bc`
  - OpenAPI diagnostics SHA-256: `e323c91a9b1f6ad7b74a8179f0abf6cd71a5923c14b452883ffc3715f9838ca2`

## Expected Delta

- Expected behavior change: contracts can now preserve an exact provider
  Read-rooted missing-SDK callsite as a terminal `sdk_source_missing` step.
- Expected report/count/coverage changes: none. The new step is not a viable
  success and contributes to neither source-call nor endpoint numerators.
- Expected generated-output changes: only the manifest filter and transitive
  canonical digest links listed above.
- Expected no-op areas: all evidence rows/chains/counts, provider and SDK fixture
  source, OpenAPI comparison semantics, and runtime/operator behavior.

## Invariants Claimed

- Evidence must not be silently dropped: missing SDK source retains the exact
  provider caller and callsite rather than collapsing to an empty row.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  or OpenAPI behavior exists in this correction.
- Source precedence/provenance must remain explicit: the new step is legal only
  when no manifest-bound SDK owns its valid import path; caller/callsite remain
  provider-bound.
- Ambiguity must stay classified instead of being coerced to success: the new
  step is terminal, non-viable, and cannot supply an ambiguous success chain.
- Provider-readiness counts must stay explainable: all seven classification
  counts and both numerators are unchanged.
- Adoption safety invariants: N/A.

## Tests Run

- Commands:
  - `gofmt -l ./internal/authoring`
  - `go test -count=1 ./internal/authoring/contracts ./internal/authoring/a0fixture ./internal/authoring/sourcebind`
  - `go test -race -count=1 ./internal/authoring/contracts ./internal/authoring/a0fixture ./internal/authoring/sourcebind`
  - `go vet ./internal/authoring/contracts ./internal/authoring/a0fixture ./internal/authoring/sourcebind`
  - `git diff --check`
- Relevant output summary: all commands passed; formatter and diff checks were
  clean.
- The full repository suite, authoring race suite, offline suite, `go vet`,
  `go mod tidy -diff`, and the standing `RootCatalog|Transform|Topology|Generation`
  byte gates also passed before the review fix. Focused normal/race/vet checks
  were repeated after the fix; the full suite will be repeated before commit.
- Tests not run and why: none required for this pre-commit handoff.

## Known Deferrals

- The input contract binds analyzed SDK source roots but not the provider's
  entire parsed dependency graph. The contract proves that the reported import
  has no bound SDK owner; A1 must additionally emit it only from an actual
  provider AST import/call. Full dependency-graph provenance is not required by
  this narrow correction.
- Import callbacks may be indexed internally by A1 but do not enter the v1
  source-evidence report or affect Read endpoint classification.
- Candidate AST analysis, optional OpenAPI corroboration, command integration,
  and readiness remain A1+ work.

## Review Focus

- Highest-risk files or paths: `contracts/validate.go`, the source-report
  schema, and the four transitive canonical digest links.
- Specific assumptions to attack: whether `sdk_source_missing` can be used
  nonterminally, with a resolved/bound SDK, outside provider scope, with SDK or
  endpoint evidence, or in a success numerator; whether a package-path-only
  caller/callee forgery can bypass edge adjacency.
- Source evidence the reviewer should verify: exact filter binding, exact
  manifest/input equality, and absence of a manifest SDK owner for accepted
  missing-source tests.
- Generated artifacts the reviewer should compare: semantic equality of every
  fixture field after removing only the authorized filter and digest links.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  arbitrary unbound imports, forged package identity, bound nested SDK modules,
  wrong row/chain reason, forbidden endpoint/SDKCall, and count inflation.
