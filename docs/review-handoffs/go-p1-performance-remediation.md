# Builder Review Handoff: Go P1 Performance Remediation

## Intent

- What problem does this change solve?
  - Remove three measured amplification paths in the Go runtime: a fixed 1 MiB
    verification buffer for tiny saved-plan snapshots, rebuilding complete
    remote-state validation indexes once per generated root, and running
    `terraform init` plus `terraform state list` once per member of a logical
    deployment root during state-aware import staging.
- What user-visible or maintainer-visible behavior should change?
  - State-aware staging takes one coherent Terraform state snapshot per logical
    root. A root with `M` import-bearing members now makes two Terraform calls
    (`init`, `state list`) instead of `2*M` calls.
  - Tiny saved-plan snapshot verification allocates a size-bounded buffer rather
    than the 1 MiB read ceiling.
  - Multi-root environment generation builds its topology/declared-edge indexes
    once per invocation rather than once per root.
- What behavior must stay unchanged?
  - Staged import/move bytes, diagnostics, source traversal, destination writes,
    errors, and exit codes.
  - Snapshot digest, size, private-mode, descriptor-identity, and freshness
    enforcement.
  - Environment-generation bytes, ordering, remote-state validation rules, and
    exact error text.
  - No live API, provider, backend, state, or infrastructure behavior is in
    scope.

## Base / Head

- Base: `5d5e98cf3c5277438ab04219f9240380bc1fa188`
- Head: uncommitted working tree on
  `feature/go-canonjson-foundation` atop the base above
- Diff command: `git diff 5d5e98cf3c5277438ab04219f9240380bc1fa188 -- go/internal/adopt/import_staging.go go/internal/adopt/import_staging_test.go go/internal/artifacts/snapshot.go go/internal/artifacts/snapshot_posix_test.go go/internal/envgen/environment_generator.go go/internal/envgen/environment_generator_test.go go/internal/metadata/loader.go docs/review-handoffs/go-p1-performance-remediation.md`

## Files Changed

- Files:
  - `go/internal/adopt/import_staging.go`
  - `go/internal/adopt/import_staging_test.go`
  - `go/internal/artifacts/snapshot.go`
  - `go/internal/artifacts/snapshot_posix_test.go`
  - `go/internal/envgen/environment_generator.go`
  - `go/internal/envgen/environment_generator_test.go`
  - `go/internal/metadata/loader.go` (comment-only correction to the P0 cache
    rationale after re-reading the Node loader)
  - `docs/review-handoffs/go-p1-performance-remediation.md`
- Files intentionally left untouched:
  - `go/internal/artifacts/stable_file.go`; this change reuses the P0
    `stableReadBufferSize` helper without changing it.
  - Artifact renderers (`canonjson`, `tfrender`) and all generated-output code.
  - Provider collection/auth/transport and all live execution paths.
  - No-op module write suppression; it changes observable filesystem/mtime
    behavior and needs separate measurement and authorization.

## Source Inputs Consulted

- Provider schemas: N/A; no provider schema content or interpretation changed.
- OpenAPI/API contracts: N/A; no API surface changed.
- Provider source files: N/A; no provider execution changed.
- Pack metadata: existing fixture pack metadata exercised by the full envgen and
  CLI differential tests; no metadata file changed.
- Existing docs or design records:
  - `docs/go-runtime-v2.md`
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
  - The preceding P0 buffer/cache remediation at `7963f74`.
- Other source evidence:
  - `node-src/domain/import-staging.ts` for staging traversal, state filtering,
    diagnostic ordering, and failure behavior.
  - `node-src/domain/environment-generator.ts` for the exact composite
    remote-state edge key and validation/error order.
  - `node-src/metadata/loader.ts`, `node-src/metadata/provider-schema.ts`, and
    the `readJson` path for the corrected comment in `metadata/loader.go`.
  - Existing Go artifact snapshot identity/private-mode implementation and
    frozen-Node CLI differential corpus.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None added or changed.
- Snapshots: None committed or changed; only snapshot verification scratch
  allocation was changed.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None. Staged import/move artifacts and
  all four standing artifact gates must remain byte-identical.

## Expected Delta

- Expected behavior change:
  - `verifyStableSnapshotDestination` uses `min(expected size, 1 MiB)`, with a
    one-byte floor for empty files. The tiny-file benchmark reports about
    6.9-7.0 microseconds/op, 768 B/op, and 8 allocs/op; the old path
    structurally allocated the full 1 MiB buffer.
  - `GenerateEnvironmentRoots` builds one invocation-scoped
    `remoteStateReferenceValidationIndex`. The full-profile envgen allocation
    profile fell from the performance sweep's approximately 61.7 MB to
    45,690.11 kB; `validateRemoteStateReferences` no longer appears as a
    material allocation site.
  - State-aware import staging lazily takes one state snapshot per selected
    logical root and reuses its parsed addresses for every member. The new
    grouped-root test proves two calls total, not two calls per member, while
    independently verifying both filtered artifact byte streams.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: None.
- Expected no-op areas:
  - Non-state-aware staging.
  - Roots with no import artifact, and missing environment roots, do not invoke
    Terraform.
  - Snapshot files and returned digests/identities.
  - Remote-state validation results and error messages.

## Invariants Claimed

- Evidence must not be silently dropped:
  - No evidence source or read is removed. Snapshot verification still hashes
    the complete expected byte range and revalidates descriptor identity,
    size, mode, and digest before accepting it.
- Generic matcher evidence must not outrank source-backed evidence:
  - N/A; evidence matching and precedence are untouched.
- Source precedence/provenance must remain explicit:
  - N/A; source selection and provenance are untouched.
- Ambiguity must stay classified instead of being coerced to success:
  - N/A; classifications are untouched.
- Provider-readiness counts must stay explainable:
  - N/A; readiness reports/counts are untouched.
- Adoption safety invariants:
  - State-aware staging still fails closed if Terraform or backend
    configuration is required and unavailable.
  - `Initialize` and `ListState` errors still abort immediately.
  - A `ListState` result with `Success == false` still yields an empty managed
    set, matching the prior behavior; that result is now coherent across the
    root rather than re-queried for each member.
  - The state snapshot is taken lazily at the same first import-processing
    point, after source existence and environment-root checks, preserving
    preceding diagnostics and failure order.
  - State is read only. The optimization does not import, apply, or mutate it.
  - A single coherent address set can become externally stale while later
    members are filtered, but the old per-member approach could instead observe
    a mixed-time view. Downstream plan/assert/apply freshness gates remain the
    authority before mutation.

## Tests Run

- Commands:
  - `gofmt` on all changed Go files.
  - `go test -count=1 ./internal/artifacts ./internal/envgen ./internal/adopt`
  - `go vet ./...`
  - `go test -race -count=1 ./internal/adopt ./internal/artifacts ./internal/envgen`
  - `go test -count=3 -run '^$' -bench '^BenchmarkVerifyStableSnapshotDestinationTinyFile$' -benchmem ./internal/artifacts`
  - `go test -count=3 -run '^$' -bench '^BenchmarkValidateRemoteStateReferencesSharedIndex$' -benchmem ./internal/envgen`
  - `go test -count=1 ./internal/envgen -run '^TestFullProfileTreeGeneratesAllRoots$' -memprofile /private/tmp/iw-p1-envgen.mem`
  - `go tool pprof -top /private/tmp/infrawright-go-foundation/go/envgen.test /private/tmp/iw-p1-envgen.mem`
  - `go test -count=1 ./cmd/iw -run '^TestBlockD5StagingArtifactBytesAndExitDifferentialAgainstFrozenNodeOracle$'`
  - `go test -count=1 ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'`
  - `go test -count=1 ./...`
  - `gofmt -l` and `git diff --check` (final formatting/patch hygiene gate).
- Relevant output summary:
  - Focused packages, vet, and all changed-package race tests passed.
  - Snapshot benchmark: approximately 6.9-7.0 us/op, 768 B/op,
    8 allocs/op.
  - Shared-index benchmark: approximately 72.7-73.2 ns/op, 64 B/op,
    1 alloc/op.
  - Full-profile envgen allocation profile: 45,690.11 kB total; the former
    per-root validator-map reconstruction is absent from the top allocation
    sites.
  - Frozen Node staging differential passed.
  - RootCatalog, Transform, Topology, and Generation byte gates passed.
  - Full Go suite passed across every package.
  - Profiling scratch (`envgen.test` and the memory profile) was removed after
    measurement and is not part of the diff.
- Tests not run and why:
  - No live provider/API/backend/Terraform tests were run: they are outside this
    fixture/local-only remediation and not authorized.
  - No live apply was run or made reachable.

## Known Deferrals

- Deferred work:
  - Suppressing no-op module/artifact rewrites.
  - Cross-process/provider API caching, import batching, accepted-plan reuse,
    provider-wide concurrency, and fetch in-flight memory budgeting.
  - Replacing the existing Terraform invocation or artifact renderers.
- Reason it is safe to defer:
  - None is required for correctness of these three isolated improvements.
    Several change freshness, filesystem side effects, or provider behavior and
    require separate evidence and authorization.
- Follow-up owner or trigger:
  - Revisit only as separately measured and scoped performance parcels. In
    particular, no-op writes require an explicit decision about mtime and
    watcher semantics; provider/import optimizations require freshness and
    upstream-provider analysis.

## Review Focus

- Highest-risk files or paths:
  - `go/internal/adopt/import_staging.go`, because it changes the timing and
    multiplicity of Terraform state observations that determine staged import
    bytes.
  - `go/internal/artifacts/snapshot.go`, because it participates in saved-plan
    freshness and exact-apply safety.
  - `go/internal/envgen/environment_generator.go`, because remote-state
    validation protects generated infrastructure references.
- Specific assumptions to attack:
  - Verify state is loaded exactly once per logical root, not globally and not
    once per selected resource, including grouped roots, failed `Success`
    results, missing env roots, absent import sources, and mixed import/move
    artifacts.
  - Verify lazy loading preserves the old first-error/diagnostic order and that
    later members cannot mutate the cached address slice.
  - Verify a root-coherent state snapshot is compatible with the plan/assert
    freshness contract and does not weaken a fail-closed boundary.
  - Verify the one-byte floor is required for `io.CopyBuffer`, while empty and
    growing/truncated files still fail or pass exactly as before.
  - Verify the right-sized buffer is scrubbed on every exit path and all final
    descriptor identity/private-mode checks remain after the read.
  - Verify the shared envgen maps are invocation-local/read-only, preserve the
    exact NUL-delimited Node key, and do not alter validation/error order.
- Source evidence the reviewer should verify:
  - The named Node staging and environment-generator functions above, plus the
    prior Go tests around backend preflight, failed state-list results, empty
    deltas, root expansion, and diagnostic order.
- Generated artifacts the reviewer should compare:
  - Frozen-Node `stage-imports` differential bytes/exits and the four standing
    artifact byte gates. No golden update is expected or permitted.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - External state changing between root snapshot and later member filtering.
  - A non-success state-list result accidentally retried or treated as a
    successful managed set.
  - Empty saved plans causing a zero-length `io.CopyBuffer` buffer.
  - Snapshot growth/truncation or descriptor replacement during verification.
  - A missing root/member/declared edge taking a different envgen error path or
    changing exact error text.
