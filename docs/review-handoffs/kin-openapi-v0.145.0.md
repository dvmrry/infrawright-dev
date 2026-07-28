# kin-openapi v0.145.0 Review Handoff

## Intent

- Upgrade `github.com/getkin/kin-openapi` from vulnerable v0.140.0 to patched
  v0.145.0. The existing PR #257 proposed v0.144.0, which remains affected by
  GHSA-mh7x-f8wq-4jhx and must not be merged.
- Make the adapter fail closed as degraded, without panicking, for the
  advisory's self-referential `additionalProperties` document.
- Pin the validation classifications that changed after v0.140.0: duplicate
  schema `required` entries and duplicate root tag names are invalid, while an
  OpenAPI 3.1 operation may omit `responses` and an OpenAPI 3.0 operation may
  not.
- Preserve source-backed operation comparisons when the OpenAPI document is
  degraded. Preserve valid resolved `additionalProperties` references and
  ordinary recursive schemas as usable.

## Base / Head

- Base: `2b552b98e47f5c38cf11c3fb5463fb13c5a63c6b` (`origin/main` after PR #267)
- Head: the reviewed tip of `agent/kin-openapi-v0.145.0`; the reviewer must
  resolve and record its immutable commit with `git rev-parse HEAD` before
  reviewing.
- Diff command:
  `git diff 2b552b98e47f5c38cf11c3fb5463fb13c5a63c6b...HEAD`

## Files Changed

- Files: `go/go.mod`, `go/go.sum`,
  `go/internal/authoring/openapiadapter/analysis_boundary_test.go`, and this
  handoff.
- Files intentionally left untouched: production adapter code, provider
  schemas, packs, generated evidence, fixtures, snapshots, and the unrelated
  untracked `reports/` directory.

## Source Inputs Consulted

- Provider schemas: None; this is a provider-neutral OpenAPI loader boundary.
- OpenAPI/API contracts: OpenAPI 3.0.3 and 3.1.0 documents embedded in focused
  boundary tests; no real provider contract or repository-specific topology
  was introduced.
- Provider source files: None.
- Pack metadata: None.
- Existing docs or design records: `AGENTS.md`,
  `docs/adversarial-review.md`, and the existing OpenAPI adapter boundary
  tests.
- Other source evidence:
  - GHSA-mh7x-f8wq-4jhx, which marks versions through v0.144.0 affected and
    v0.145.0 patched:
    <https://github.com/getkin/kin-openapi/security/advisories/GHSA-mh7x-f8wq-4jhx>
  - v0.145.0 release:
    <https://github.com/getkin/kin-openapi/releases/tag/v0.145.0>
  - Upstream advisory fix commit:
    <https://github.com/getkin/kin-openapi/commit/88aa64c7cbd03ecadbb419c473bdcaa8b0124c6b>
  - Upstream validation behavior commits:
    <https://github.com/getkin/kin-openapi/commit/6c01290ed1ab01f6c34d8d8f81e1761e44f3c108>
    and
    <https://github.com/getkin/kin-openapi/commit/b5bb6bc70017d044257bf1dffac92dcc8930a800>.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None; test documents are provider-neutral inline inputs.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: malformed self-referential
  `additionalProperties` input returns degraded diagnostics instead of
  crashing the process. The adapter adopts the post-v0.140 validation rules
  listed under Intent.
- Expected report/count/coverage changes: OpenAPI documents newly rejected by
  those validation rules classify as degraded; source-backed operation
  comparisons remain present and corroborated.
- Expected generated-output changes: None.
- Expected no-op areas: production adapter logic, provider-specific behavior,
  pack topology, schemas, evidence provenance, and generated outputs.

## Invariants Claimed

- Evidence must not be silently dropped: every classification case asserts
  that the source-backed `widget` comparison remains corroborated, including
  degraded-document cases.
- Generic matcher evidence must not outrank source-backed evidence: unchanged;
  the test supplies source-backed endpoint evidence and checks its comparison.
- Source precedence/provenance must remain explicit: unchanged; the dependency
  upgrade does not alter evidence selection or provenance.
- Ambiguity must stay classified instead of being coerced to success: unsafe
  documents are degraded and expose no usable document; legitimate references
  remain usable.
- Provider-readiness counts must stay explainable: unchanged; no readiness or
  count-accounting code changed.
- Adoption safety invariants: loader/validator failures stay operational
  diagnostics rather than process panics, and no degraded document is exposed
  as usable.

## Tests Run

- Commands:
  - `go test -count=1 -run '^TestAnalyzeKinOpenAPILoaderSelfReferenceFailsClosed$' ./internal/authoring/openapiadapter`
  - `go test -count=1 -run '^TestAnalyzeKinOpenAPIValidationClassifications$' ./internal/authoring/openapiadapter`
  - `go test -count=25 -run '^(TestAnalyzeKinOpenAPILoaderSelfReferenceFailsClosed|TestAnalyzeKinOpenAPIValidationClassifications)$' ./internal/authoring/openapiadapter`
  - `go test -race -count=1 -run '^(TestAnalyzeKinOpenAPILoaderSelfReferenceFailsClosed|TestAnalyzeKinOpenAPIValidationClassifications)$' ./internal/authoring/openapiadapter`
  - `go test -count=1 ./internal/authoring/openapiadapter ./internal/authoring/openapimap ./internal/authoring/sourceoperation ./internal/providerprobe`
  - `go mod verify`
  - `go test -count=1 ./openapi3 -run '^TestGHS_mh7x_f8wq_4jhx$'` from the downloaded v0.145.0 module
  - `make check`
  - `go vet ./...`
  - `test -z "$(gofmt -l go)"`
  - `git diff --check`
- Relevant output summary: all post-upgrade focused, repeated, race, consumer,
  upstream-advisory, module, formatting, vet, and repository promotion checks
  passed. `make check` ran the complete Go suite successfully.
- Focused regression and pre-fix/unsafe-mutation proof: before upgrading, the
  self-reference regression crashed v0.140.0 with `SIGSEGV` inside the loader,
  reached through `openapiadapter.validateClosed` and `Analyze`. The
  classification table also failed in three distinct directions: OpenAPI 3.1
  without responses was degraded instead of usable, while duplicate
  `required` entries and duplicate root tags were usable instead of degraded.
  The same focused tests pass on v0.145.0. The legitimate resolved-reference
  and recursive-schema controls also pass.
- Promotion efficiency: approximately 7 minutes from the first corrected
  candidate tree to the review-ready tree; one full-corpus sweep was attempted
  and it passed.
- Tests not run and why: `govulncheck` is not installed and is not a repository
  promotion gate; the exact upstream advisory regression was run instead.
  Hosted PR checks, including pruned-profile and vertical-slice jobs, require a
  published PR and remain for GitHub CI.

## Known Deferrals

- Deferred work: None required for this replacement. Hosted checks remain to
  execute after publication.
- Reason it is safe to defer: local promotion gates and the exact upstream
  advisory regression pass; hosted checks exercise repository CI profiles.
- Follow-up owner or trigger: the replacement PR's required GitHub checks.

## Review Focus

- Highest-risk files or paths:
  `go/internal/authoring/openapiadapter/analysis_boundary_test.go` and the
  transitive module changes in `go/go.mod` and `go/go.sum`.
- Specific assumptions to attack: v0.145.0 must be the first patched release;
  the advisory reproducer must reach Infrawright's real loader boundary; tests
  must distinguish usable from degraded in both directions; degraded input
  must retain source-backed comparison evidence.
- Source evidence the reviewer should verify: the advisory affected/patched
  ranges, upstream patch and regression, and the validation behavior commits
  linked above.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  OpenAPI 3.0 versus 3.1 missing responses, duplicate validation fields,
  adversarial `additionalProperties` references, valid resolved
  `additionalProperties`, and ordinary recursive schemas.
