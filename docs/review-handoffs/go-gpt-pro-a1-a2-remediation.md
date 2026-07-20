# Builder handoff: GPT-Pro A1/A2 remediation

## Intent

- Prevent a selected fetch resource from exposing a previous run's JSON after
  the current run reports an optional skip, request failure, or authentication
  failure.
- Make a relocated Go binary honor `INFRAWRIGHT_PACKAGE_ROOT` and prefer the
  shipped runtime data directories over Node's `package.json` as its discovery
  authority.
- Keep successful fetch artifact bytes, selection order, concurrency,
  diagnostics, exit classifications, unselected files, and all Terraform
  artifact bytes unchanged.

## Base / Head

- Base: `160785bd5b4dfb9fb0893fccb003ac36c1f8c863`
- Head: uncommitted builder working tree based on the base above
- Diff command: `git diff 160785bd5b4dfb9fb0893fccb003ac36c1f8c863 --`

## Files Changed

- Files:
  - `go/internal/collectors/rest.go`
  - `go/internal/collectors/rest_test.go`
  - `go/internal/collectors/helpers_test.go`
  - `go/cmd/iw/main.go`
  - `go/cmd/iw/main_test.go`
  - `docs/go-runtime-v2.md`
- Files intentionally left untouched: transform/adopt consumers, artifact
  renderers, Terraform plan/assessment/Apply code, frozen Node oracle,
  `go.mod`, release wiring, and aggregate fetch-budget policy.

## Source Inputs Consulted

- Provider schemas: None; this change is independent of provider schema
  content.
- OpenAPI/API contracts: None.
- Provider source files: None.
- Pack metadata: production registry selection and optional-status behavior as
  loaded by `go/internal/collectors/rest.go`; no pack files changed.
- Existing docs or design records: `docs/go-runtime-v2.md` compatibility
  contract and `docs/go-cutover-roadmap.md` package-root discovery rule.
- Other source evidence:
  - `node-src/collectors/rest.ts` writes only processed outcomes and preserves
    earlier destinations for skipped/failed outcomes.
  - `node-tests/rest-collector.test.ts` explicitly asserted preservation of
    `stale optional` and `stale failed` files.
  - `go/internal/transformrun/runner.go` and `go/internal/adopt/runner.go` read
    any present `<sourceType>.json` without a per-run freshness token.
  - GPT-Pro's review of exact base SHA identified the sequential stale-evidence
    scenario and missing documented package-root override.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None. The frozen Node oracle remains
  unchanged; the Go-only fetch freshness divergence is recorded in
  `docs/go-runtime-v2.md`.

## Expected Delta

- Expected behavior change: Go validates every selected resource type into a
  jailed single-component destination before authentication. After
  authentication resolution and output-root creation, it invalidates every
  selected destination before the first resource request. Processed resources
  then write current bytes;
  optional skips, request failures, and auth failures leave the selected file
  absent. A directory at a selected destination is refused before resource
  requests. `INFRAWRIGHT_PACKAGE_ROOT` wins when set; otherwise package-root
  discovery prefers ancestors containing both `packs/` and `packsets/`, with
  `package.json` retained only as a last-resort fixture/transition fallback.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: successful selected JSON and every
  committed Terraform artifact remain byte-identical. Previously stale
  selected JSON is intentionally removed; unselected files remain unchanged.
- Expected no-op areas: HTTP/auth semantics, pagination, concurrency and
  diagnostic ordering, transform/adopt rendering, plan/assessment/Apply,
  authoring commands, dependency graph, and frozen-oracle bytes.

## Invariants Claimed

- Evidence must not be silently dropped: current successful evidence is
  written unchanged; only previous-run bytes for resources explicitly selected
  in the current fetch are invalidated. Unselected destinations are retained.
- Generic matcher evidence must not outrank source-backed evidence: Unchanged.
- Source precedence/provenance must remain explicit: Unchanged.
- Ambiguity must stay classified instead of being coerced to success:
  Unchanged.
- Provider-readiness counts must stay explainable: Unchanged.
- Adoption safety invariants: transform/adopt cannot read a previous selected
  destination after a normally completed current fetch skip or failure. The
  invalidation barrier fails closed before resource requests if a destination
  cannot safely be removed.

## Tests Run

- Commands:
  - focused package-root, relocated-binary, optional-skip, auth-failure,
    concurrent-result, invalidation-refusal, and post-barrier write-failure
    tests
  - `test -z "$(gofmt -l .)"`
  - `go vet ./...`
  - `go test ./... -count=1`
  - `go test -race ./internal/collectors -count=1`
  - `go test -race ./cmd/iw -run
    'TestPackageRoot|TestFindPackageRoot|TestRelocatedBinaryUsesExplicitPackageRoot'
    -count=1`
  - `go test ./cmd/iw -run
    'RootCatalog|Transform|Topology|Generation' -count=1`
  - `go mod tidy -diff`
  - Go error-handling anti-pattern checks on both changed production files
- Relevant output summary: all commands passed. The full suite includes the
  hard-fail D5 frozen-oracle digest lane; the explicit artifact gate also
  passed.
- Tests not run and why: no live provider, credentials, cluster, or live
  Terraform execution is relevant or authorized. Colima/Linux qualification
  was not rerun because the changes use portable `os`/`filepath` operations
  already covered by the Go and race suites.

## Known Deferrals

- Deferred work: aggregate fetch page/item/duration budgets from GPT-Pro B1.
- Reason it is safe to defer: it is a separately triaged release qualification
  and is independent of stale selected-file publication.
- Follow-up owner or trigger: address before the stable release gate after
  measuring production-scale provider responses.
- Deferred work: a cross-process fetch generation manifest/atomic current-run
  pointer.
- Reason it is safe to defer: the reported blocker was a normally completed
  fetch returning success for an optional skip while retaining earlier bytes;
  the pre-request invalidation barrier closes that path and also closes normal
  auth/request failure paths. Cross-process concurrent consumers and external
  termination during authentication remain outside the current single-command
  operating contract and should not be invented without a separate design.
- Follow-up owner or trigger: reconsider only if concurrent fetch/consumer
  execution or crash-resumable pull generations become supported behavior.

## Review Remediation Mapping

- Finding: selected destination invalidation could escape the output root or
  erase an unselected sibling when a registry key contained separators and
  `..`.
- Root cause: registry metadata requires only a non-empty resource key, while
  destination construction previously passed that key directly to
  `filepath.Join`.
- Fix: `fetchDestination` now accepts only a single filename component,
  rejects both platform separator spellings and NUL, verifies the computed
  parent equals the cleaned output directory, and runs before authentication.
  The same validated destination is used for invalidation and publication.
- Regression tests: `TestFetchRejectsUnsafeResourceDestinationBeforeAuthOrMutation`
  proves sibling and parent escapes preserve their victims with zero auth and
  zero requests. `TestFetchInvalidationUnlinksSymlinkWithoutTouchingTarget`
  proves a stable symlink is removed without altering its outside target.
- Verification: rerun the focused collector tests, race suite, full Go suite,
  vet, and artifact byte gates, then request a changed-surface re-review.

## Review Focus

- Highest-risk files or paths: `go/internal/collectors/rest.go` destination
  construction/invalidation barrier and the changed stale-file tests.
- Specific assumptions to attack:
  - every selected resource, including failed-auth resources, receives a
    destination and passes the invalidation barrier;
  - traversal-shaped registry keys are rejected before authentication or any
    filesystem mutation and cannot delete siblings or files outside the output
    root;
  - no resource request starts before all selected invalidations succeed;
  - unselected files remain untouched;
  - post-barrier write failures retain deterministic selection-order error
    behavior;
  - explicit package-root override works from a genuinely relocated binary;
  - legacy fixture binaries remain discoverable without outranking real
    runtime-data markers.
- Source evidence the reviewer should verify: the Node write/skip behavior,
  Go transform/adopt input reads, current `FetchResources` exit accounting,
  and `docs/go-cutover-roadmap.md` discovery rule.
- Generated artifacts the reviewer should compare: the four standing artifact
  differential gates and successful fetch bytes.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  optional skip, ordinary HTTP failure, shared-auth failure, preexisting
  directory refusal, unselected sibling preservation, concurrency greater than
  one, and a write failure introduced after the invalidation barrier.
