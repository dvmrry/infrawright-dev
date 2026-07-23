# A3-O builder handoff — closed OpenAPI adapter

## Intent

- Add the library-only, in-memory OpenAPI adapter required for isolated v2
  source-first diagnostics and retained API field metadata.
- Source evidence remains authoritative: OpenAPI can corroborate, report a
  missing path, or report ambiguity; this parcel cannot mint `conflict`.
- No CLI, publication, filesystem/network reads, provider access, or
  sourceoperation integration is added.

## Base / Head

- Base: `42c8932defdb7222562916fefd4ba789ae460338`
- Head: uncommitted worktree on `feature/go-canonjson-foundation`
- Diff command: `git diff -- go/go.mod go/go.sum docs/go-authoring-port-roadmap.md docs/review-handoffs` plus direct inspection of untracked `go/internal/authoring/openapiadapter/**` and A3-O handoff/task docs.

## Files Changed

- Builder files: `go/go.mod`, `go/go.sum`, new
  `go/internal/authoring/openapiadapter/**`, and this handoff. Coordinator
  records: `docs/go-authoring-port-roadmap.md`, the A3 manifest, and the new
  A3-O task card.
- Files intentionally left untouched: contracts, sourcebind, reconcile,
  sourceoperation, CLI, Make, and frozen fixtures.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: `docs/go-authoring-port-roadmap.md` §3.6 and
  `go/internal/authoring/contracts` OpenAPI diagnostics validator.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs/design records: A3 coordinator manifest and A3-O task.
- Other source evidence: `node-src/authoring/openapi.ts` and
  `node-tests/fixtures/python-reconcile-schema-api-v1.json`
  (`464121fe2e7edcc09861ea046c10aa54d4d101145803d5af13adb41b56c5cbd7`).

## Generated Artifacts

- Reports: only sealed, canonical `contracts.OpenAPIDiagnosticsReport` bytes
  returned in memory.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none; A3-I owns bundle integration.

## Expected Delta

- Expected behavior change: callers can analyze a captured OpenAPI 3 document
  with a private in-memory kin loader, receive sealed diagnostics, and extract
  retained OpenAPI 3/Swagger 2 field metadata.
- Expected report/count/coverage changes: only the optional diagnostics
  artifact can change; all six comparison counts are recomputed from rows.
- Expected generated-output changes: none in this parcel.
- Expected no-op areas: all source classifications, source counts, provenance,
  bundle names, and CLI behavior.

## Invariants Claimed

- Evidence must not be silently dropped: source bytes are rendered and SHA-256
  bound before diagnostics are sealed and decoded again.
- Generic matcher evidence must not outrank source-backed evidence: only
  `observed_http` source endpoints are compared.
- Source precedence/provenance must remain explicit: diagnostics copy source
  trust/manifest and retain the exact canonical source report hash.
- Ambiguity must stay classified instead of being coerced to success: viable
  candidates are sorted; two or more are `ambiguous`, and no conflict is emitted.
- Provider-readiness counts must stay explainable: contracts recompute the
  six-state partition and degraded annotation during sealing.
- Adoption safety invariants: captured paths/SHA values are revalidated and
  copied; raw references are checked before kin; the reader accepts only a
  package-private virtual URI and copies sanitized validation bytes.

## Tests Run

- Commands: `gofmt -l internal/authoring/openapiadapter`; `go vet
  ./...`; `go test -count=1
  ./internal/authoring/openapiadapter`; `go test -race -count=1
  ./internal/authoring/openapiadapter`; `go mod tidy -diff`; `GOWORK=off
  GOPROXY=off GOSUMDB=off go test -count=1 ./...`; `go test -race -count=1
  ./internal/authoring/...`; `go test -count=1 ./cmd/iw -run
  'RootCatalog|Transform|Topology|Generation'`; `git diff --check`; and
  `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh
  internal/authoring/openapiadapter`.
- Relevant output summary: all listed gates passed; the error checker reported
  `No error handling anti-patterns found` after remediation. The package has
  1,093 lines of tests covering usable JSON/YAML, mixed JSON/YAML captured refs,
  closed-reference attacks, every document state, comparison partitions and
  source-report binding, defensive copies, concurrent fresh loaders, both
  frozen metadata cases, and the retained Swagger 2 case.
- Tests not run and why: no external/network tests exist or are appropriate;
  every test uses copied in-memory bytes and the closed virtual reader.

## Known Deferrals

- None inside A3-O. A3-M and A3-I remain separate parcels under the coordinator
  manifest. Both fresh-context adversarial reviews approved the remediated
  parcel; their reports are recorded beside this handoff.

## Review Focus

- Highest-risk files or paths: `adapter.go` closed kin loader and root/failure
  isolation; `compare.go` raw local-reference and source endpoint comparison;
  `metadata.go` Node parity and Swagger 2 compatibility.
- Specific assumptions to attack: kin's URI join/load behavior with fragments;
  raw relative-ref normalization; degraded classification after unrelated kin
  validation failure; parameter-name-only normalization; aliasing of returned
  bytes/maps.
- Source evidence the reviewer should verify: exact source report SHA binding,
  source trust/manifest copying, observed-http-only comparison eligibility,
  and inability to emit `conflict`.
- Generated artifacts the reviewer should compare: rendered diagnostics against
  `contracts.DecodeOpenAPIDiagnosticsReport` and the source report render.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  unlisted or aliased refs, malformed unreferenced files, pointer cycles,
  unsupported root versions, numeric overflow sanitization, ambiguous template
  paths, response-only metadata, and `$ref` siblings.
