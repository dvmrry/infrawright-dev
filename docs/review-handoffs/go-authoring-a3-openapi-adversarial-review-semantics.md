# A3-O adversarial review — evidence semantics and accounting

## Blocking Findings

None outstanding.

## Non-Blocking Risks

None outstanding.

Resolved during review and rechecked:

- Raw comparison inventory formerly followed nested `$ref`s and could mark a
  source-backed operation unavailable; it now limits raw proof to the selected
  Path Item chain and operation object. Regression:
  `analysis_boundary_test.go:225`.
- Closed-reference URI aliases (private scheme, encoded fragments/paths, and
  empty-query `ForceQuery`) are rejected in both joining and reading.
  Regressions: `capability_yaml_test.go:13` and
  `capability_yaml_test.go:85`.
- YAML numeric lexemes are preserved for authoritative parsing while a finite
  clone is used for validation. Regression: `capability_yaml_test.go:95`.

## Source Evidence Review

- Diff inspected: Yes. The uncommitted A3-O adapter, contracts, fixtures,
  manifest/roadmap, and module dependency changes were inspected against base
  `42c8932defdb7222562916fefd4ba789ae460338`.
- Handoff inspected: `go-authoring-a3-openapi-adapter.md`.
- Provider schemas inspected: Not applicable; this adapter consumes captured
  OpenAPI evidence.
- OpenAPI/API contracts inspected: Task and roadmap requirements, Go
  diagnostic schema/validator, and Node authority implementation. Exact
  contract validation covers source-report SHA/trust/manifest identity,
  evidence-state constraints, exact endpoint matching, state/count partitions,
  and degraded accounting (`contracts/validate.go:1327`, `:1502`, `:1555`).
- Provider source inspected: Not applicable.
- Pack metadata inspected: Metadata extraction is exact-operation based;
  `$ref` replacement, deterministic response selection, request-body fallback,
  allOf/array flattening, requiredness, and copies were checked
  (`metadata.go:129`, `:190`, `:272`).
- Fixtures or snapshots inspected: Frozen Node fixture
  `python-reconcile-schema-api-v1.json` SHA
  `464121fe2e7edcc09861ea046c10aa54d4d101145803d5af13adb41b56c5cbd7`;
  source `node-src/authoring/openapi.ts` SHA
  `fc50de84ef7fa7762c3961c3ca81c2ad953cd1558bf8661215ab5e359db237d4`.
  Both fixture cases and the Swagger 2 rejection fence are replayed
  (`adapter_test.go:66`, `:153`).
- Missing evidence or review gaps: None material. The adapter preserves
  `observed_http` as the only eligible class, accepts only exact method/path or
  a sole viable segment-template candidate, sorts ambiguity candidates
  deterministically, avoids conflicts, and derives counts/degraded status from
  the six-state partition (`compare.go:71`, `:112`).

## Generated Artifact Review

- Reports reviewed: Canonical diagnostic report sealing, source-evidence SHA
  binding, and strict re-decode (`adapter.go:94`).
- Schemas reviewed: Strict contracts validator and its recomputed
  count/coverage checks.
- Fixtures reviewed: Frozen authority fixture and required-source SHA
  declarations.
- Snapshots reviewed: No separate generated snapshots.
- Count/coverage deltas reviewed: Yes; comparison states are mutually
  partitioned and `degraded` is based only on observed HTTP evidence.
- Artifact drift accepted or rejected: Accepted. The frozen authority cases are
  preserved; the adapter adds regressions for security-boundary and parsing
  semantics without weakening the processing/version fences.

## Verdict

**Approve.**

The implementation now satisfies the reviewed A3-O semantic and security
boundaries: closed captured-file loading is authority- and URI-alias-safe
(including `ForceQuery`), raw comparison does not over-traverse nested
references, output is source-sealed and strict-schema validated, metadata
follows the frozen Node behavior, and ambiguity/count/degraded handling is
deterministic.

Verification passed: `gofmt -l`, focused tests, race tests, `go vet`,
`go mod tidy -diff`, `git diff --check`, and offline
`GOWORK=off GOPROXY=off GOSUMDB=off go test -count=1 ./...`.
