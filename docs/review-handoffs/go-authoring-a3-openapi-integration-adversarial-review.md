# A3-I adversarial review — sealed OpenAPI bundle integration

## Blocking findings

None.

## Non-blocking risks

None.

## Source evidence review

- Diff inspected: the uncommitted A3-I parcel against
  `4d8b0deb37ac654b7308eda29a318812a737e5a8`, including its integration test
  and builder handoff.
- Handoff inspected:
  `docs/review-handoffs/go-authoring-a3-openapi-integration.md`.
- Provider schemas inspected: the `source-first-v2` fixture's verified,
  manifest-bound provider schema and source report.
- OpenAPI/API contracts inspected: the six-artifact A2 vocabulary, the sealed
  `openapiadapter.Result`, and the strict source-bound diagnostics decoder.
- Provider source inspected: the fixture-local qualified input capture and its
  sealed `sourceanalysis.QualifiedEvidence` path.
- Pack metadata inspected: not applicable.
- Fixtures or snapshots inspected: all operational adapter states, the real
  qualified captured-OpenAPI seam, report/trust/manifest mismatch rejection,
  detached-result mutation, zero-row accounting, and cancellation.
- Missing evidence or review gaps: none found. `CompileQualified` snapshots the
  qualified input, analyzes only its captured OpenAPI status, and gives the
  private compiler a sealed result rather than arbitrary bytes.

## Generated artifact review

- Reports reviewed: the fixed six in-memory source-operation artifacts.
- Schemas reviewed: no schema changes; strict diagnostics decoding remains the
  binding gate.
- Fixtures reviewed: no committed fixture or golden changed.
- Snapshots reviewed: none.
- Count/coverage deltas reviewed: source rows and counts remain unchanged;
  OpenAPI comparison accounting is revalidated against the exact source
  partition, including zero selected rows.
- Artifact drift accepted or rejected: accepted only for
  `openapi-diagnostics.json`. Tests compare the first five artifact byte
  streams for identity across absent, unreadable, malformed, invalid-root,
  degraded, and usable states.

## Verification

The fresh read-only reviewer independently passed `gofmt -l`, focused `go
vet`, focused and race tests, `go mod tidy -diff`, `git diff --check`, and the
four standing RootCatalog/Transform/Topology/Generation byte gates. No
`openapimap` or readiness package enters the sourceoperation dependency
closure.

## Verdict

Approve.

The integration preserves the fixed artifact vocabulary, admits no raw
diagnostics or generic-map input, validates every sealed result against the
exact decoded source report, and keeps absent and unverified compilation on
the source-only path.
