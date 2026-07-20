# A3-R Builder Handoff — Reconciliation Kernel

## Intent

- What problem does this change solve? It ports the dependency-free,
  non-OpenAPI API/schema/override reconciliation kernel from
  `node-src/authoring/reconcile-schema-api.ts` into
  `go/internal/authoring/reconcile`.
- What user-visible or maintainer-visible behavior should change? Go callers
  can select Terraform resource schemas, normalize API item envelopes and
  OPTIONS metadata, merge metadata, resolve field aliases, and produce the
  frozen reconciliation report in memory with conventional errors. Schema
  selectors now use `*string` for `providerSource`: nil means absent, while a
  non-nil empty string is an explicit source selection and fails if absent.
- What behavior must stay unchanged? The 12 buckets, reasons, paths,
  suggestions, code-point ordering, and report counts replay the frozen
  CPython authority. This parcel adds no CLI, files, network access, OpenAPI
  parser, publication behavior, module dependency, or alternate count/report
  authority.

## Base / Head

- Base: `feature/go-canonjson-foundation` at
  `540be8b719713331763f8a4dd37763de7f38ec05`.
- Head: uncommitted builder worktree; no commit was created.
- Diff command: `git diff --check && git diff -- go/internal/authoring/reconcile docs/review-handoffs/go-authoring-a3-reconcile.md`.

## Files Changed

- Files:
  - `go/internal/authoring/reconcile/doc.go` (5 LOC)
  - `go/internal/authoring/reconcile/reconcile.go` (1158 LOC)
  - `go/internal/authoring/reconcile/reconcile_test.go` (463 LOC)
  - `docs/review-handoffs/go-authoring-a3-reconcile.md` (193 LOC)
- Files intentionally left untouched: all existing files, including the
  coordinator-owned dirty `docs/go-authoring-port-roadmap.md` and
  `docs/review-handoffs/go-authoring-a3-coordinator-manifest.md`; all A3-O,
  A3-M, A3-I, A6, Node, fixture, command, Makefile, and module files.

## Builder Chronology

- Initial implementation replayed the frozen authority and passed the focused
  gates.
- Coordinator integration then identified three non-vector Node-parity gaps:
  numeric primitive matching accepted arbitrary non-numbers, present-null
  renames were omitted, and skipped items preferred a null `name` over `id`.
  This handoff includes the bounded corrections and direct regressions; no
  package boundary or authority fixture changed.
- Fresh adversarial review then identified three source-evidenced boundary
  gaps: explicit empty `providerSource` was conflated with absence, non-finite
  authoring numbers reached shared transform seams, and snake-key collisions
  silently selected a Go-map ordering winner. This revision changes only A3-R:
  pointer presence preserves the Node distinction, recursive input validation
  rejects NaN/+Inf/-Inf, and deterministic path-aware collision validation
  fails closed before normalization.
- Review of that remediation found whole-OPTIONS-envelope validation was too
  broad: Node ignores GET, unknown top-level keys, and non-object POST/PUT/PATCH
  members. A3-R now validates only each object-valued metadata member actually
  processed for POST, PUT, or PATCH, immediately before normalization, with its
  precise `source.actions.METHOD.field` path.

## Source Inputs Consulted

- Provider schemas: frozen report/helper vectors embed Terraform schema inputs
  in `node-tests/fixtures/python-reconcile-schema-api-v1.json`.
- OpenAPI/API contracts: none parsed or added here; the two frozen
  `api_metadata_from_openapi` helper cases are explicitly A3-O.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `AGENTS.md`,
  `docs/go-authoring-port-roadmap.md` §§3.3–3.6 and A3,
  `docs/review-handoffs/go-authoring-a3-coordinator-manifest.md`, and the
  adversarial-review/handoff templates.
- Other source evidence:
  `node-src/authoring/reconcile-schema-api.ts`,
  `node-tests/authoring-reconcile-schema-api.test.ts`, the frozen authority
  fixture (SHA-256
  `464121fe2e7edcc09861ea046c10aa54d4d101145803d5af13adb41b56c5cbd7`),
  and existing `canonjson`, `metadata`, and `transform` Go seams.

## Generated Artifacts

- Reports: none published; reports are in-memory return values only.
- Schemas: none.
- Fixtures: none changed.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: new library-only Go reconciliation APIs.
- Expected report/count/coverage changes: none relative to the frozen A3-R
  authority: 2 `node_live_differential` reports plus 7 retained-unit reports
  replay exactly. The 3 in-scope retained helpers replay exactly. No coverage
  or readiness report is introduced.
- Expected generated-output changes: none.
- Expected no-op areas: OpenAPI extraction/metadata, generic OpenAPI mapping,
  source-operation bundle assembly, all commands and their stdout/stderr/exit
  behavior, filesystem publication, and module dependencies.

## Invariants Claimed

- Evidence must not be silently dropped: every processed leaf records a bucket
  with the Node-authority reason; known drops remain explicit.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  or source evidence behavior is present in this parcel.
- Source precedence/provenance must remain explicit: this library does not
  consume or create source/OpenAPI evidence.
- Ambiguity must stay classified instead of being coerced to success:
  ambiguous provider-schema selection returns an error; it cannot select a
  provider by iteration order.
- Provider-readiness counts must stay explainable: the report owns private
  bucket state and derives all summary/suggestion counts from it; callers
  receive detached maps/slices.
- Adoption safety invariants: no mutation of caller inputs, no retained caller
  maps/slices in the report, no external I/O, and no new dependencies.
- Boundary normalization invariants: a supplied provider source is distinct
  from absence; non-finite Go float64 values and colliding snake_case keys are
  rejected at every map/list depth before shared normalization for reconciliation
  inputs and object-valued POST/PUT/PATCH OPTIONS metadata; colliding keys fail
  closed rather than choosing a map-order-dependent winner.

## Tests Run

- Commands:
  - From `go/`: `gofmt -l internal/authoring/reconcile` (no output).
  - From `go/`: `go test -count=1 ./internal/authoring/reconcile` (pass).
  - From `go/`: `go test -race -count=1 ./internal/authoring/reconcile` (pass).
  - From `go/`: `go vet ./internal/authoring/reconcile` (pass).
  - From `go/`: `go mod tidy -diff` (no output).
  - From repository root: `git diff --check` (no output).
- Relevant output summary: fixture SHA is checked before every replay;
  2/2 Node-live report cases, 7/7 retained report cases, and 3/3 retained
  non-OpenAPI helper cases passed. Focused tests cover malformed input errors,
  ambiguous provider selection, Unicode code-point ordering, and defensive
  return copies. The post-integration direct regressions
  `TestReconcileItemsClassifiesUncoercibleNumberShapeMismatch`,
  `TestReconcileItemsRecordsPresentNullRename`, and
  `TestReconcileItemsSkippedNullNameUsesIDType` also passed. The adversarial
  review regressions `TestProviderSchemaSelectionPreservesProviderSourcePresence`,
  `TestReconcileItemsRejectsNonFiniteAuthoringNumbers`,
  `TestAPIMetadataFromOptionsRejectsNestedNonFiniteAuthoringNumbers`, and
  `TestReconcileItemsRejectsSnakeKeyCollisionsAtEveryDepth` also passed under
  the focused normal and race runs. The OPTIONS-boundary regression tests
  `TestAPIMetadataFromOptionsRejectsProcessedMetadataSnakeKeyCollisions` and
  `TestAPIMetadataFromOptionsIgnoresUnprocessedBoundaryValues` also passed.
- Tests not run and why: package-external/full-repository integration suites
  are not part of this bounded library parcel; no new OpenAPI or CLI behavior
  exists to exercise.

## Known Deferrals

- Deferred work: the two `api_metadata_from_openapi` helper vectors and all
  OpenAPI parsing/field extraction/comparison diagnostics.
- Reason it is safe to defer: A3-O owns that explicit package boundary; this
  package accepts already-normalized metadata without parsing OpenAPI.
- Follow-up owner or trigger: A3-O after the coordinator's Artifactory module
  gate; A6 owns CLI/usage/output/publication behavior.

## Known Limitations

- Go maps do not retain Node JSON-object encounter order. When two raw keys
  normalize to one `SnakeName`, A3-R intentionally returns a conventional
  error rather than claiming Node's encounter-order winner. This fail-closed
  boundary is exercised at top level and nested object depth.
- The `*string` provider-source API is intentionally explicit: callers that
  previously passed `""` to request unqualified discovery must pass nil.
- OPTIONS validation intentionally preserves Node's processing fence: GET
  actions, unknown top-level values, and non-object members are ignored rather
  than recursively validated. Only object-valued POST/PUT/PATCH metadata is
  normalized or subject to finite-number/collision checks.

## Review Focus

- Highest-risk files or paths: `go/internal/authoring/reconcile/reconcile.go`,
  particularly `walkBlock`, `walkAttribute`, override accounting, and
  `ReconciliationReport.AsMap`.
- Specific assumptions to attack: exact bucket/reason/path behavior under
  nested attributes/blocks, default/drop overlap, alias selection precedence,
  snake normalization, `[]` path aliases, and whether transform panics are
  reliably converted to errors without exposing partial reports.
- Source evidence the reviewer should verify: reconcile the control flow and
  ordered branch precedence against
  `node-src/authoring/reconcile-schema-api.ts`; compare all frozen cases rather
  than treating the in-code implementation as authority.
- Generated artifacts the reviewer should compare: none; compare the in-memory
  reports against `python-reconcile-schema-api-v1.json`.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  computed-vs-input aliases, API writable/required metadata, choices and
  relationship objects, empty non-schema fields, malformed schemas/overrides,
  provider source ambiguity, astral-vs-BMP Unicode path sort order, and mutation
  of a map/slice returned by a prior report read.
