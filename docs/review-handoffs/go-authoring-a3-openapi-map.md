# A3-M builder review handoff — frozen generic OpenAPI map kernel

## Intent

- What problem does this change solve? Ports the reusable legacy OpenAPI
  resource-map report kernel into `go/internal/authoring/openapimap`.
- What user-visible or maintainer-visible behavior should change? Callers can
  build a detached structured report and render exact Python-compatible bytes
  from an opaque `openapiadapter.Document`, a generic Terraform schema object,
  and an optional generic registry object.
- What behavior must stay unchanged? The frozen v1 report authority, generic
  CRUD matching semantics, code-point ordering, and Python binary64 half-even
  ratios remain unchanged. Generic matches remain diagnostic-only and cannot
  enter source-first readiness.

## Base / Head

- Base: `4d8b0deb37ac654b7308eda29a318812a737e5a8`
- Head: `feature/go-canonjson-foundation` worktree (uncommitted A3-M parcel)
- Diff command: `git diff -- go/internal/authoring/openapimap go/internal/authoring/openapiadapter/legacy_map.go go/internal/authoring/openapiadapter/legacy_map_test.go docs/review-handoffs/go-authoring-a3-openapi-map.md`

## Files Changed

- Files: `go/internal/authoring/openapimap/doc.go`, `map.go`, `map_test.go`;
  `go/internal/authoring/openapiadapter/legacy_map.go`,
  `legacy_map_test.go`; this handoff.
- Files intentionally left untouched: existing adapter production/tests,
  source-first/readiness packages, source-operation integration, CLI, module
  files, fixtures, packs, roadmap, and coordinator manifest.

## Source Inputs Consulted

- Provider schemas: frozen v1 report inputs and committed pack registries.
- OpenAPI/API contracts: `node-src/authoring/openapi-resource-map.ts` and the
  frozen fixture's OpenAPI inputs.
- Provider source files: none; the parcel intentionally has no source-provider
  mapping or source-readiness input.
- Pack metadata: `packs/*/registry.json`, only for frozen replay's Node-equivalent
  default-registry test setup.
- Existing docs or design records: A3-M task card, A3-O adapter APIs, canonical
  JSON renderer, metadata Terraform classification, and reconciliation aliases.
- Other source evidence: `node-tests/fixtures/python-openapi-resource-map-v1.json`
  (SHA-256 `e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c`).

## Generated Artifacts

- Reports: none written; 19 frozen reports are replayed in memory.
- Schemas: none.
- Fixtures: none changed; authority hash asserted in tests.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: adds the generic, sealed legacy map package and a
  narrow detached adapter view.
- Expected report/count/coverage changes: none for frozen v1 inputs; all 19
  frozen reports render byte-for-byte identical to authority.
- Expected generated-output changes: none.
- Expected no-op areas: source-first analysis, provider readiness accounting,
  filesystem/CLI/network behavior, and provider-source classification.

## Remediation Since Initial Review

- Registry optional-field parity now follows JavaScript exactly: product is a
  strict string equality; read status uses JavaScript truthiness; reason and
  downstream fallbacks are nullish; fetch pagination comes only from nested
  `fetch.pagination ?? prefix`; and `operation_id`/`path_kind` use truthiness.
- `APIPrefix` is now a pointer: omitted defaults to `/api/`, while an explicit
  empty string matches all paths. The legacy view treats missing/non-object
  `paths` as empty and retains non-object path-item keys with no methods.
- Detail matching now accepts only one parameter segment with an optional
  single trailing slash. Ratio rounding restores sign after magnitude-based
  Python half-even rounding.

## Invariants Claimed

- Evidence must not be silently dropped: input boundaries require objects;
  report data and adapter-view slices/pointers are detached; every frozen case
  is byte-rendered against the retained authority.
- Generic matcher evidence must not outrank source-backed evidence: the map
  package imports neither `sourceanalysis` nor `sourceoperation`, and exposes
  only generic diagnostic report data.
- Source precedence/provenance must remain explicit: no source-first evidence,
  provenance, or readiness APIs are consumed or produced.
- Ambiguity must stay classified instead of being coerced to success: ties,
  low suffix scores, collection-only matches, registry ambiguity, and special
  resources retain their Node statuses and records.
- Provider-readiness counts must stay explainable: the report recomputes
  resource, family, surface, coverage, registry, and surface-map totals from
  the emitted records.
- Adoption safety invariants: `LegacyMap` exposes only version/title/server
  URLs/path-method inventory/component count, supports cancellation and
  missing top-level `info`, and leaks neither raw graphs nor parser/file APIs.

## Tests Run

- Commands: `gofmt -l internal/authoring/openapimap internal/authoring/openapiadapter`; `go vet ./internal/authoring/openapimap ./internal/authoring/openapiadapter`; `go test -count=1 ./internal/authoring/openapimap ./internal/authoring/openapiadapter`; `go test -race -count=1 ./internal/authoring/openapimap ./internal/authoring/openapiadapter`; `go mod tidy -diff`; `git diff --check`; `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh internal/authoring/openapimap internal/authoring/openapiadapter`.
- Relevant output summary: all listed gates passed; the error checker reported
  `No error handling anti-patterns found`. `map_test.go` verifies authority
  SHA and all 19 frozen reports, plus provider-selection/path/half-even helper
  vectors. Remediation vectors render registry bytes for top-level versus
  nested pagination, absent/non-string product, absent reason, falsy status,
  and falsy optional fields; they also cover omitted versus explicit-empty API
  prefixes, exact detail separators, and negative half-even ratios.
  `legacy_map_test.go` covers missing `info`, lower-case method inventory,
  missing/non-object paths, detached output, and cancellation. Owned Go files
  total 1,936 LOC. Semantic mismatch: none found in frozen replay.
- Tests not run and why: no CLI/filesystem/network/provider-live test is in
  this package-only scope; all replay inputs are in-memory copies.

## Known Deferrals

- Deferred work: CLI argument/output routing and publication remain A6; source
  evidence/readiness composition remains outside A3-M.
- Reason it is safe to defer: the package has no command, file, network, or
  readiness dependency and its generic output is explicitly diagnostic-only.
- Follow-up owner or trigger: A3 coordinator/A6 when composing the sealed
  report into a command or future provider probe.

## Review Focus

- Highest-risk files or paths: `openapimap/map.go` generic match/special/ratio
  and count logic; `openapiadapter/legacy_map.go` boundary narrowing.
- Specific assumptions to attack: default registry absence versus explicit
  empty input; Python binary64 half-even behavior at 1/32 and 1/160; trailing
  detail-path matching; alias/action/parent-resource records; Unicode sorting.
- Source evidence the reviewer should verify: Node mapper and frozen fixture,
  especially all 19 replay rows and the authority hash.
- Generated artifacts the reviewer should compare: in-memory rendered reports
  against frozen `python_report`/`report` values; no artifact files changed.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  ensure no source-first/readiness import is introduced; review nil versus
  explicit empty registry behavior, nonstandard detail paths, product mismatch
  suppression, provider gaps, and detached adapter-view results.
