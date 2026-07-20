# A4 provider-probe orchestration: builder review handoff

## Intent

- Implement the authoring A4 library slice: provider-probe recipe loading,
  legacy input preparation, exact frozen legacy artifacts, qualified
  source-first orchestration, and bounded production preparation adapters.
- Preserve the frozen Node provider-probe artifact bytes for the two retained
  legacy cases while adding a categorical local-only v2 mode.
- Keep CLI parsing, public artifact-directory mutation, complete-set
  publication, stale optional-artifact removal, and stdout/stderr contracts in
  A6. A4 returns only a detached in-memory result.
- Never let the legacy generic mapper, remote preparation capabilities, or the
  optional `openapi-map.json` affect source-first readiness evidence.

## Base / Head

- Base: `4abbfa56dc1ad73055ec5c429b8750d5bc73954c`
- Head: uncommitted A4 working tree on `feature/go-canonjson-foundation`
- Diff command: inspect every file under
  `go/internal/authoring/providerprobe/`, the context additions in
  `go/internal/httptransport/` and `go/internal/terraformcmd/`, and this
  handoff. All A4 files are still uncommitted/untracked so the shared worktree,
  not the stale pre-review snapshot, is the review authority.

## Files Changed

- Files:
  - `go/internal/authoring/providerprobe/types.go`
  - `go/internal/authoring/providerprobe/run.go`
  - `go/internal/authoring/providerprobe/recipe.go`
  - `go/internal/authoring/providerprobe/legacy.go`
  - `go/internal/authoring/providerprobe/legacy_openapi_validate.go`
  - `go/internal/authoring/providerprobe/legacy_render.go`
  - `go/internal/authoring/providerprobe/qualified.go`
  - `go/internal/authoring/providerprobe/host.go`
  - `go/internal/authoring/providerprobe/host_process_unix.go`
  - `go/internal/authoring/providerprobe/host_process_windows.go`
  - `go/internal/authoring/providerprobe/host_process_other.go`
  - frozen OpenAPI schema/license/provenance assets and the Node-derived
    validation corpora under `providerprobe/openapi_schemas/` and
    `providerprobe/testdata/`
  - package-local tests for those files
  - `go/internal/httptransport/transport.go` plus cancellation tests
  - `go/internal/terraformcmd/runner.go` plus cancellation tests
  - this handoff
- Files intentionally left untouched: A0-A3 packages, `cmd/iw`, Makefiles,
  Node sources, frozen fixtures, `go.mod`, `go.sum`, and all public artifact
  publishers.

## Source Inputs Consulted

- Provider schemas: both local schema shapes exercised by the frozen
  provider-probe authority; qualified tests use manifest-bound fixture-local
  schemas.
- OpenAPI/API contracts:
  - `node-src/authoring/provider-probe.ts`
  - `node-src/authoring/openapi.ts`
  - `@apidevtools/swagger-parser@12.1.0`,
    `openapi-schemas@2.1.0`, `json-schema-ref-parser@14.0.1`, and
    `swagger-methods@3.0.2`, pinned by source/asset hashes in
    `openapi_schemas/PROVENANCE.md`
  - the accepted A3 `openapiadapter` and `openapimap` APIs
- Provider source files: the frozen fixture provider source and deterministic
  local Git sourcebind fixtures only.
- Pack metadata: none; provider-probe consumes a recipe rather than a pack.
- Existing docs or design records:
  - `docs/go-authoring-port-roadmap.md`, especially sections 3.5, 4, and A4-A6
  - the accepted A1-A3 handoffs and A2 publisher rejection record
  - `/tmp/infrawright-a4/coordinator-manifest.md`
- Other source evidence:
  - `node-tests/provider-probe.test.ts`
  - `node-tests/provider-probe-parity.test.ts`
  - `node-tests/fixtures/python-provider-probe-v1.json`, SHA-256
    `235cdbad249822ee70f3b947feffbc802af3a357a8fdf5d108f2454b78838824`
  - independently generated Node validation oracle: 91 full-pipeline cases
    (90 serializable plus one native-cycle case), 18 direct `spec.js` probes,
    and zero uncovered V8 ranges in the upstream supplemental validator

## Generated Artifacts

- Reports:
  - Legacy v1 returns exactly `source-registry.json`,
    `source-diagnostics.json`, `openapi-map.json`, `summary.json`, and
    `summary.md`, in that order.
  - Qualified v2 returns the existing sealed six-artifact source-operation
    bundle and may append diagnostic-only `openapi-map.json` when a usable
    OpenAPI document can be mapped.
- Schemas: Terraform provider schema capture is private legacy input
  preparation only; it is not a new public generated artifact.
- Fixtures: no frozen fixture changed.
- Snapshots: no committed snapshot changed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none for the two frozen legacy cases
  or the qualified six-artifact core.

## Expected Delta

- Expected behavior change:
  - A library caller can run the legacy v1 provider-probe contract or select
    qualified v2 by providing a non-null `source_provenance` recipe object.
  - Qualified v2 accepts only local manifest-bound roots and has no legacy host
    capability. It composes `sourcebind.LoadVerified` ->
    `RequireQualification` -> `sourceanalysis.Analyze` ->
    `sourceoperation.CompileQualified`.
  - Legacy preparation may download a pinned OpenAPI input, clone a pinned Git
    revision, or capture a Terraform schema through a narrow bounded host.
  - Legacy OpenAPI input now traverses the frozen SwaggerParser-compatible
    version, local-reference, schema, and Swagger-2 semantic validation phases
    before mapping. Qualified v2 retains A3's partial/degraded policy. The
    retained stricter Go safety boundaries are enumerated under Known
    Deferrals; none changes the frozen artifact cases.
- Expected report/count/coverage changes: none. Optional generic-map failure
  omits only that diagnostic artifact and cannot suppress or alter core source
  evidence.
- Expected generated-output changes: none against the frozen overlapping
  authority.
- Expected no-op areas: CLI/help/exit codes, Make routing, output-directory
  publication, readiness gates, Transform/Adopt, existing artifacts, and
  dependencies.

## Invariants Claimed

- Evidence must not be silently dropped: after qualified core compilation,
  optional generic-map failure returns the exact six core artifacts.
- Generic matcher evidence must not outrank source-backed evidence:
  `openapi-map.json` is appended only as a diagnostic artifact and never enters
  `CompileQualified` or readiness accounting.
- Source precedence/provenance must remain explicit: qualified mode can be
  entered only through a verified manifest and `RequireQualification`.
- Ambiguity must stay classified instead of being coerced to success: A4 does
  not change A1-A3 analysis or report types.
- Provider-readiness counts must stay explainable: the six core artifacts are
  direct detached copies of the sealed A2 bundle; A4 performs no count
  projection.
- Adoption safety invariants: not applicable to this slice. No Adopt, plan, or
  apply path is present.
- Capability boundary: presence of any legacy preparation field in a
  qualified recipe fails before source loading, host construction, or host
  invocation.
- Filesystem boundary: A4 creates only a private legacy preparation directory.
  Replacing a prior checkout requires an exact ownership marker and
  descriptor-bound removal under the already-bound work root. A4 never creates
  or removes a public artifact directory.
- Cancellation boundary: provider-probe cancellation reaches in-flight HTTP,
  retry waits, Git process groups, and Terraform process groups; killed children
  are drained and reaped before a raw context error is returned.
- External-system boundary: all tests use local files, injected HTTP
  transports, local fake executables, or deterministic local Git repositories.
  No test inherits credential or proxy variables.

## Tests Run

- Commands:
  - `gofmt -l internal/authoring/providerprobe`
  - `go vet ./...`
  - `go test ./internal/authoring/providerprobe -count=1`
  - `go test ./internal/authoring/providerprobe -count=10`
  - `go test -race ./internal/authoring/providerprobe -count=1`
  - `go test -race ./internal/httptransport ./internal/terraformcmd -count=1`
  - `go test ./internal/authoring/providerprobe -coverprofile=... -count=1`
  - `go test ./...`
  - `go test ./cmd/iw/ -run 'RootCatalog|Transform|Topology|Generation' -count=1`
  - `GOOS=windows GOARCH=amd64 go test -exec=/usr/bin/true
    ./internal/authoring/providerprobe ./internal/httptransport
    ./internal/terraformcmd -run '^$'`
  - `go mod tidy -diff`
  - `git diff --check`
- Relevant output summary:
  - providerprobe tests and race tests pass; statement coverage is 82.7%.
  - both frozen legacy cases compare every artifact byte against the authority.
  - all 90 serializable Node validation cases match accept/reject and rejection
    phase; the native-cycle case and all 18 direct supplemental probes match.
  - full Go test and vet suites pass.
  - all four standing artifact gates pass.
  - module tidy has no delta; A4 adds no dependency.
- Tests not run and why:
  - no live HTTP, remote Git, real Terraform, credentials, provider, or public
    artifact publisher was run; those are outside this fixture-only slice.
  - the Windows packages cross-compile; they were not executed on a Windows
    host. The Windows Git adapter remains intentionally fail-closed.

## Known Deferrals

- Deferred work: CLI wiring, complete-set publication, stale optional-map
  removal, stdout/stderr/exit parity, Make routing, and Node-free smoke.
- Reason it is safe to defer: the accepted A2 review proved the attempted
  portable publisher could not defend its pathname transaction against an
  active same-UID writer. The roadmap assigns a declared ownership/concurrency
  contract and the entire public command boundary to A6.
- Follow-up owner or trigger: A6 after A5.
- Legacy default work-directory naming and preparation-error wording are
  intentionally Go-native safety behavior: randomized private work and
  redacted bounded host failures. A6 must decide the final CLI-visible path
  presentation without weakening those properties.
- Randomly-created preparation directories are returned to successful callers
  for lifecycle ownership. A preparation failure may leave a private randomized
  remnant rather than perform a recursive pathname delete; this is unbounded in
  naming and cannot create a run ceiling.
- Multiple independent validation failures are reported in deterministic
  lexical path order because the strict Go decoder does not retain JSON object
  insertion order. Node reports the first insertion-ordered failure. This does
  not change acceptance, rejection phase, artifact suppression, or retained
  CLI prose.
- Ref-parser's cache can make an invalid extended-reference sibling accepted or
  rejected depending on which repeated reference appears first. The strict Go
  decoder has already discarded that insertion order, so Go deterministically
  applies the sibling and rejects when it is invalid. This is a deliberately
  stricter acceptance divergence, pinned in both reference orders rather than
  an attempt to reproduce traversal-order-dependent behavior.
- Ref-parser also exposes JavaScript native properties while walking a JSON
  Pointer, so pointers to string indices, string `length`, or array `length`
  can resolve. Go limits traversal to JSON objects and canonical array indices
  and rejects those cases in the dereference phase. URI-decoded tokens, the
  raw-slash compatibility fallback, non-pointer hash refs, and traversal
  through intermediate local refs remain compatible and are directly tested.
- Go caps validation graphs and dereference work at 100,000 visited values and
  depth 512; pinned ref-parser has no default node/depth limit. A sufficiently
  oversized source graph or extended-reference expansion may therefore reject
  in Go. Plain-reference targets are memoized like ref-parser, and the tested
  ordinary case of 500 operations sharing a 100-property component stays well
  below the work ceiling rather than being charged once per reference.

## Initial Adversarial Review Remediation

- P0 reusable-work symlink/write escape -> descriptor-bound `os.Root`
  workspace bindings and random sibling atomic replacement -> outside-sentinel
  regressions for inputs, download, schema, and Terraform configuration.
- P1 missing SwaggerParser validation -> exact frozen schemas through the
  existing `jsonschema/v6` engine plus a source-traceable legacy supplemental
  kernel -> independent 91+18-case Node oracle, asset hashes, licenses, no-I/O
  loader, version/ref/numeric/OAS3.1/Swagger2 branch coverage.
- P1 recipe error order and explicit empty `api_prefix` -> Node-phase recipe
  validation and nullish summary defaulting -> cross-invalid precedence and
  full five-artifact empty-prefix differential tests.
- P1 dropped mid-flight cancellation -> context-aware shared HTTP/Terraform
  runners and provider host integration -> blocked RoundTrip, retry wait, Git,
  Terraform process-group, pre-spawn, race, and Windows compile tests.
- Focused re-review ref-parser mismatches -> safe URI token decoding and
  raw-slash fallback, correct non-pointer hash retention, intermediate-ref
  pointer traversal, exact primitive/array extended-ref classes and discarded
  sibling behavior, memoized high-fanout plain refs, and bounded Swagger-2
  circular-required rejection -> local pinned-Node probes plus full-validator
  regression cases for every corrected boundary.

## Review Focus

- Highest-risk files or paths: `legacy_openapi_validate.go`, `legacy.go`,
  `legacy_workspace.go`, `qualified.go`, the shared context runners, and the
  POSIX Git process-group runner.
- Specific assumptions to attack:
  - The full frozen recipe-validation precedence and JavaScript falsey behavior
    are preserved, including null sections, empty primary fields, and unknown
    keys.
  - Python/Node binary64 half-even rounding is faithfully reproduced at the
    `1/160 -> 0.0063` edge.
  - Safe YAML accepts aliases but rejects unsafe tags and duplicate keys.
  - A qualified recipe cannot reach download, clone, or Terraform even through
    empty-string aliases.
  - Optional map construction cannot suppress the six core artifacts.
  - Work/source pathname rebind attempts cannot delete outside the bound work
    root.
  - Git timeout and output-limit paths kill and reap the process group without
    leaking raw command inputs or output.
  - Terraform execution receives only the caller-supplied complete environment
    and uses the existing bounded `terraformcmd` runner.
  - The frozen validation schemas retain OpenAPI properties named `format`
    while disabling only JSON-Schema format assertions.
  - Local refs are resolved without filesystem/network fallback, cycles stay
    bounded, binary64 overflow/underflow matches Node, and rejected validation
    cannot return or publish artifacts.
- Source evidence the reviewer should verify: frozen Node provider-probe
  source/tests, authority digest, A1-A3 sealed capability APIs, and the A2
  publisher review record.
- Generated artifacts the reviewer should compare: all five artifacts in both
  frozen authority cases and all six qualified core artifacts before/after
  optional map failures.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  unavailable/degraded OpenAPI; generic-map provider selection failure;
  post-capture Git/input mutation; legacy aliases in qualified recipes;
  malformed or duplicate JSON/YAML; source/work directory replacement; child
  output overflow and cancellation.
