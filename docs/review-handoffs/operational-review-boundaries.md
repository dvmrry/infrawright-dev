# Operational Review Boundaries — Builder Handoff

## Intent

- Clarify that Infrawright's adoption/codegen core is provider-neutral while
  live collection requires a matching compiled adapter.
- Add operational state-safety paths to the existing fresh-context adversarial
  review policy.
- Remove stale Node/Python runtime language from the active collection,
  transport, and HTML-decoder paths now that the repository ships one Go
  runtime.
- Rename three collector-private compatibility helpers to domain-specific names
  while pinning their existing coercion behavior with direct tests.
- Preserve collection, transport, transformation, pack, and generated-output
  behavior except for four operator diagnostics whose remediation language is
  now runtime-neutral.

## Base / Head

- Base: `9b0b40a84871fd325c25e0c0a92c1348e51664cf` (`origin/main` after PR
  #260).
- Initial refreshed implementation head:
  `e3e46a66b68ab07d7c826809b135642cdad0ebb1`.
- Review-correction implementation head:
  `eda73fb25d38edfc6453d5ed8809921d9f5cb7ae`.
- Coercion-test implementation head:
  `7e518b034fb5925285ea83334c656c6dce326af5`.
- Head: the handoff-only commit immediately after the implementation head; the
  exact hash is supplied to the reviewer and contains no implementation change.
- Diff command: `git diff 9b0b40a84871fd325c25e0c0a92c1348e51664cf..<exact-review-head>`.
- Refresh base (2026-07-31): `origin/main` after PR #300 (HTTP default
  timeout 30s -> 60s); the branch merged that tip, resolving the one genuine
  conflict in `go/internal/httptransport/transport.go` in main's favor
  (`DefaultTimeoutMs = 60_000`, now pinned by a regression test).
- Current refresh base (2026-08-02, supersedes the coordinates above for any
  new review): `origin/main` at `a9498345`, which carries PR #301 (test-audit
  cleanup: tfrender batch subsystem and non-discriminating tests removed) and
  PR #302 (Terraform diagnostics propagated through terraformcmd). The merge
  was conflict-free; the timeout pin and its regression test are unchanged.
  Diff command: `git diff origin/main...<exact-review-head>` against current
  main.

## Files Changed

- Files:
  - Review policy and public boundary documentation: `AGENTS.md`, `README.md`,
    `docs/adversarial-review.md`, and
    `docs/adversarial-review-run-prompt.md`.
  - This handoff:
    `docs/review-handoffs/operational-review-boundaries.md`.
  - Collection and CLI boundary wording/tests: `go/cmd/iw/{main.go,
    commands_fetch.go,authoring_commands_test.go,v2_vertical_slice_test.go}`
    and the affected files under `go/internal/collectors/`.
  - Go transport boundary wording: `go/internal/httptransport/{doc.go,
    errors.go,transport.go}`.
  - Generic unrelated-file fixture:
    `go/internal/metadata/loader_test.go`.
  - Runtime-neutral HTML-decoder diagnostics and focused proof:
    `go/internal/transform/{kernel.go,kernel_test.go,overrides.go}`.
- Files intentionally left untouched: pack metadata, provider schemas, generated
  artifacts, demo outputs, workflows, and compatibility algorithms whose names
  document exact byte/ordering semantics rather than an executable dependency.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: committed `provider_sources` declarations in
  `packs/*/pack.json`.
- Existing docs or design records: `docs/adoption-command-surface.md`,
  `docs/adversarial-review.md`, `docs/adversarial-review-run-prompt.md`, and
  `docs/review-handoff-template.md`.
- Other source evidence: `go/cmd/iw/commands_fetch.go`,
  `go/internal/collectors/{authority.go,rest.go,rest_diagnostics.go,types.go,
  zscaler_adapters.go}`, `go/internal/httptransport/`, and
  `go/internal/transform/{kernel.go,overrides.go}`.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: four operator errors no longer name retired
  runtimes or a transport parcel that already exists. Missing collector
  authority, missing probe transport, and missing HTML decoder still fail
  closed at the same points.
- Expected report/count/coverage changes: one focused two-case transform
  regression plus 27 direct collector-coercion cases; no production count or
  coverage-accounting change.
- Expected generated-output changes: None.
- Expected no-op areas: provider collection requests, authentication, URL
  composition, retry schedule, pagination, transformation output, pack loading,
  adoption/codegen, Terraform output, and generated artifacts.

## Invariants Claimed

- Evidence must not be silently dropped: unchanged.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged; pack provider
  sources resolve only through caller-approved compiled adapters.
- Ambiguity must stay classified instead of being coerced to success: unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: identity keys, import IDs/blocks, moved blocks,
  saved-plan classification, and apply authority now explicitly require the
  existing fresh-context adversarial-review process.
- Runtime boundary: no JavaScript or Python source/runtime files or executable
  invocations were introduced. Collection, fetch command assembly, and the Go
  HTTP transport invoke no Node/Python runtime after this change; historical
  provenance comments naming the retired TS/Node sources remain (see Known
  Deferrals) and are matched by a text audit for runtime references.
- Pagination coercion: nil, false, empty strings/lists/maps, and numeric zero
  remain falsy; integer coercion continues to truncate finite fractional values
  toward zero and reject unsupported or non-finite inputs.

## Tests Run

- Commands:
  - `go test -count=1 ./internal/collectors -run '^(TestResolveCollectorAdaptersFailsClosedOnMismatchedProviderSources|TestProbeRestHostRequiresAnInjectedTransport|TestResolveCollectorAdaptersAgainstCopiedRoot|TestGenericFetchCollectsRealZccAndZtcRegistriesFromCopiedRoot)$'`
  - `go test -count=1 -run '^(TestCollectorTruthyVocabulary|TestCollectorIntVocabulary)$' ./internal/collectors`
  - `go test -count=1 ./internal/transform -run '^TestTransformHTMLDecoderErrorsAreRuntimeNeutral$'`
  - `go test -count=1 ./internal/collectors ./internal/httptransport ./internal/metadata ./cmd/iw`
  - `make check`
  - `go vet ./...`
  - `test -z "$(gofmt -l go/cmd go/internal)"`
  - `git diff --check`
  - `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh --no-bare-return go/internal/collectors`
  - targeted `rg` audits for tracked runtime files, executable invocations, and
    active Node/Python wording in collection/fetch/transport sources.
- Relevant output summary: all focused tests, affected packages, the complete Go
  and distribution gate, vet, formatting, whitespace, and error-handling scan
  passed. The targeted runtime-file/invocation audits found none; the active
  collection/fetch/transport source audit is empty. The final promotion sweep
  includes the direct collector-coercion tables.
- Focused regression and pre-fix/unsafe-mutation proof:
  - Before the authority fix, the focused test failed only because the actual
    error ended in `matching injected Node adapter` instead of the neutral
    compiled-adapter remediation.
  - Before the probe fix, the focused test failed only because the actual error
    still said `until the rest-http-transport parcel lands`.
  - Before the HTML fix, both transform subtests failed on distinct stale
    Python-specific diagnostics. Both pass after the production strings changed.
  - An unsafe truthiness mutation that treated empty strings/lists/maps and
    numeric zero as truthy failed five named direct cases.
  - Replacing truncation with rounding failed both positive JSON-number and
    negative float cases on distinct assertions.
- Promotion efficiency: approximately 60 hours 41 minutes from the original
  candidate commit to the coercion-test implementation commit; three known
  full-corpus `make check` sweeps across the original handoff and two refreshes,
  with no failed or interrupted sweep recorded.
- Tests not run and why: no live provider or tenant tests; the change does not
  alter provider requests or require credentials.

## Known Deferrals

- Deferred work: beyond the three collector-private helpers renamed here, a
  repository-wide mechanical rename of compatibility symbols and provenance
  comments that still describe exact historical string, numeric, Unicode, and
  ordering semantics.
- Reason it is safe to defer: those references encode current observable
  compatibility rules and are not runtime files, subprocess calls, fallbacks, or
  operator remediation. Renaming them is a broad review-only cleanup and should
  not obscure this PR's operational boundary correction.
- Follow-up owner or trigger: perform as a dedicated cleanup PR with no behavior
  delta, repository-wide symbol accounting, and full mutation/promotion proof.

## Review Focus

- Highest-risk files or paths: `README.md`, the three review-policy lists,
  `go/internal/collectors/authority.go`,
  `go/internal/collectors/{rest.go,rest_test.go,rest_diagnostics.go}`, and
  `go/internal/transform/overrides.go`.
- Specific assumptions to attack: whether adapters own only authentication and
  URL composition; whether the generic coordinator/transport own the remaining
  duties; whether each new state-safety trigger belongs in mandatory review;
  whether any comment cleanup concealed an executable change; and whether each
  neutral diagnostic remains actionable and fail-closed. Verify the new direct
  coercion tables pin the renamed helpers' complete production vocabulary.
- Source evidence the reviewer should verify: adapter construction in
  `commands_fetch.go`, provider-source resolution in `authority.go`, transport
  construction in `go/internal/httptransport`, and HTML decoder injection through
  `TransformLoadedItemsOptions`.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  wording that implies pack metadata alone enables live fetch; a review list that
  omits identity/import/move/classification/apply paths; accepting an unavailable
  adapter or nil transport/decoder instead of failing; or a test that asserts only
  a substring and would allow retired-runtime guidance to return.

## Prior Review Resolution (Not Current Approval)

- The original review found that README collapsed adapter, coordinator, and
  transport duties. The corrected text assigns authentication/URL composition
  to compiled adapters and pagination/retries/failure/output to generic Go code.
- It found that only `AGENTS.md` carried the new operational triggers. The same
  list now appears in `AGENTS.md`, `docs/adversarial-review.md`, and the run
  prompt.
- It found import-ID and import-block lifecycle coverage missing. Both are now
  explicit review triggers.
- The current-main refresh found stale runtime-specific diagnostics and a stale
  handoff. The implementation and this handoff address those findings; a fresh
  reviewer must independently approve the exact refreshed tip.
- The refreshed review found that `CollectorContext` documentation overclaimed
  that every absent optional field uses a product default. The correction now
  states the real contract: an empty field is absent, and each adapter decides
  whether absence selects a fallback or fails closed.
- A later independent review found that the three private compatibility-helper
  renames were broader than the summary implied and that `collectorTruthy` and
  `collectorInt` lacked direct tests. The scope now names all three renames, and
  direct tables plus unsafe-mutation proof pin the behavior of both pagination
  coercion helpers.
- A second current-main refresh merged `origin/main` at 6e38d88 ("Rename
  emitted Terraform identifiers from infrawright_ to iw_", #299) with no
  conflicts and no content changes to this branch's edits; the full gate
  suite (`go build`, `go vet`, `gofmt`, `go test ./... -count=1`,
  `make check`) was re-run green on the merged tip. A fresh reviewer must
  still independently approve that exact tip.
