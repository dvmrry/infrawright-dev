# Block D2 import staging — builder review handoff

Status: **ready for fresh-context adversarial review; not accepted or
committed.** This is the D2 parcel only. No credential, provider API, real
Terraform backend, or Apply operation was used or made reachable by its tests.

## Intent

- Solve the missing Go `stage-imports` / `unstage-imports` library layer and the
  state-aware generated-import filter needed by Block D.
- Preserve Node's exact traversal, diagnostic, counter, newline/BOM, backend
  preflight, Terraform argv, and byte-copy behavior.
- Keep the already-qualified `canonjson` / `tfrender` artifact producers
  unchanged. D2 stages their import/moved bytes; it does not re-render them and
  does not use `hclwrite`.
- Keep CLI parsing, environment reads, exit codes, Adopt orchestration,
  pending-move policy, Oracle behavior, and Apply outside this parcel.

## Base / Head

- Base: `fcce46606781cfa5ce81bc92d0fee92c20aeb46e`
- Head: uncommitted working tree on the same base; the coordinator owns review
  and commit construction.
- Diff command after isolating/staging only D2 paths:
  `git diff --cached -- go/internal/adopt/import_filter.go go/internal/adopt/import_filter_test.go go/internal/adopt/import_staging.go go/internal/adopt/import_staging_test.go docs/review-handoffs/go-block-d2-import-staging-review.md`

## Files Changed

- Files:
  - `go/internal/adopt/import_filter.go` — 192 lines.
  - `go/internal/adopt/import_staging.go` — 643 lines.
  - `go/internal/adopt/import_filter_test.go` — 160 lines.
  - `go/internal/adopt/import_staging_test.go` — 655 lines.
  - This handoff.
- Production/test LOC: 835/815, 1,650 total before this handoff.
- Files intentionally left untouched:
  - all D1 Oracle/generated-config-policy files and `internal/adopt/doc.go`;
  - D3 Adopt runner, adoption metadata/policy, state loaders, and their seams;
  - `go/internal/tfrender`, `go/internal/terraformcmd`, `go/internal/roots`,
    `go/internal/deployment`, `go/internal/metadata`, and `go/cmd/iw`;
  - `go.mod` / `go.sum`.

## Source Inputs Consulted

- Provider schemas: N/A; D2 does not inspect provider schema or state values.
- OpenAPI/API contracts: N/A; D2 performs no provider request.
- Provider source files: N/A.
- Pack metadata: existing `metadata.LoadedPackRoot` and
  `roots.LoadedRootTopology` contracts; tests use minimal provider/resource
  shapes to exercise singleton and explicit-group topology.
- Existing docs or design records:
  - `docs/review-handoffs/go-block-d-adopt-apply.md`;
  - `docs/go-runtime-v2.md` dependency and compatibility boundary;
  - `docs/adoption-command-surface.md` whole-root staging contract;
  - `docs/adversarial-review.md` and its builder template.
- Other source evidence:
  - `node-src/domain/import-staging.ts:1-341` in full;
  - `node-src/domain/import-moves.ts:188-280` for the generated-import filter;
  - `node-tests/import-staging.test.ts:117-618` for frozen filter, staging,
    newline/BOM, state, grouping, unstaging, and Terraform adapter vectors;
  - `node-tests/transform-runtime-artifacts.test.ts:610-653` for durable moved
    evidence staging;
  - existing Go `tfrender` render/path/publish code and `terraformcmd` runner.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: no new committed fixture; the frozen Node expected byte strings are
  asserted directly in the focused Go tests.
- Snapshots: None.
- Demo or lab outputs: None retained; tests use `t.TempDir` only.
- Artifact drift intentionally expected: None. Imports and moved files are
  copied exactly from existing `tfrender` output.

## Expected Delta

- Expected behavior change:
  - Go now exposes CLI-independent `StageImports`, `UnstageImports`,
    `FilterGeneratedImports`, and the injected staging Terraform adapter.
  - State-aware staging initializes each selected import-bearing root, lists
    state, and removes only exact already-managed generated import blocks.
  - A failed `terraform state list` retains all imports, matching Node.
  - Staged artifacts are completely prepared under a randomized name in the
    destination directory and atomically published only after copy, mode,
    close, and descriptor-bound identity checks all succeed.
- Expected report/count/coverage changes: None. D2 produces no REPORT or
  readiness/evidence count.
- Expected generated-output changes: no generator changes; staged destination
  files now reproduce source import/moved bytes, with filtered import bytes in
  state-aware mode.
- Expected no-op areas: Oracle, adoption classification/projection, pack/user
  policy merge, transform rendering, plan assessment, CLI, and Apply.

## Invariants Claimed

- Evidence must not be silently dropped: state-list failure is the explicit
  keep-all path; only an exact captured state address removes a generated
  import block; source artifacts are never modified.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: source paths come only
  from `tfrender.ComputeTransformArtifactPaths` over the validated deployment
  and selected loaded topology.
- Ambiguity must stay classified instead of being coerced to success: malformed
  generated blocks, invalid UTF-8, missing Terraform injection, missing backend
  config, invalid tenant/topology, and absent source artifacts fail explicitly.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - traversal is root/member/imports-then-moves ordered;
  - whole-root selection is retained;
  - state-awareness never changes moves;
  - remote-backend detection precedes all Terraform calls;
  - Terraform uses the existing bounded non-shell runner with an environment
    snapshot and exact init/state-list argv;
  - a staging-copy failure before atomic publication cannot truncate or
    partially replace an existing destination and cannot leave a new partial
    destination;
  - no test invokes a real Terraform binary or any Apply operation.

## Tests Run

- Commands:
  - `gofmt -l` over the four owned Go files — empty.
  - `git diff --check` — passed.
  - `go test -count=1 import_filter.go import_staging.go import_filter_test.go import_staging_test.go` — passed.
  - `go test -race -count=1 import_filter.go import_staging.go import_filter_test.go import_staging_test.go` — passed.
  - `go vet import_filter.go import_staging.go import_filter_test.go import_staging_test.go` — passed.
  - Go error checker with `--no-bare-return` over each owned Go file — no
    anti-patterns found.
  - `go test -count=1 ./internal/tfrender -run 'TestImportMovesDifferentialProbe|TestWriteTransformArtifactsDetectsRename|TestCompileTransformArtifactsRejectsConflictingMoveEvidence|TestCompileAndPublishPreserveLegacyArtifactBytesAndLifecycle' -v` — passed.
  - `go test -count=1 -v ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'` — all four standing Node differential gates passed.
- Relevant output summary:
  - Frozen filter vectors cover managed/kept blocks, quoted braces/escapes,
    mixed HCL, non-import text, Python whitespace, CR and Unicode line-anchor
    exclusions, and malformed/unterminated refusal.
  - Stage tests cover ordinary imports/moves, missing roots, no artifacts,
    whole-root groups and diagnostic order, missing Terraform, backend preflight
    before calls, exact filtering, empty delta, failed state-list keep-all,
    CR/CRLF/BOM behavior, source-byte preservation, exact copy modes, and
    unstage source preservation. Six deterministic publication-failure cases
    inject errors after copy, chmod, and close with both an existing and absent
    destination; each proves preservation/no partial publication and no
    temporary remnant. A post-rename close-error seam proves the committed
    publication returns success, and a hostile symlink rebind proves identity
    refusal preserves both the destination and outside target.
  - Adapter test uses only a local fake executable and proves exact argv,
    failed-state tolerance, and environment snapshotting.
- Tests not run and why:
  - `go test ./internal/adopt`, its package race form, `go test ./...`, and full
    `go vet ./...` are temporarily blocked by concurrent uncommitted D3 work:
    `runner.go` currently has an unused `roots` import and missing
    `ProjectProviderState` / `ProjectProviderStateOptions` symbols. D2's exact
    file-list forms above compile and pass independently. Re-run all module
    gates after D3 completes; do not change D3 from this parcel.

## Known Deferrals

- Deferred work: D5 CLI parsing/wiring, environment-read asymmetry, usage-exit
  shim, and end-to-end CLI differential.
- Reason it is safe to defer: the D2 API is fully injectable and independently
  tests domain/filesystem/Terraform-adapter behavior without a CLI or live
  backend.
- Follow-up owner or trigger: D5 after D1-D4 review/commit sequence.

## Adversarial Review Loop

- Blocking finding: the first implementation opened the final destination with
  truncation before copying, so a read/copy/write failure could destroy an
  existing staged artifact or expose a partial new artifact.
- Root cause: publication began before the replacement bytes and mode were
  fully prepared and before descriptor close succeeded.
- Correction: `copyStagingArtifact` now opens an `os.Root` bound to the
  destination directory, creates a cryptographically randomized temporary file
  there with exclusive creation, copies and applies the source mode, captures
  the temporary file identity, closes both data descriptors, and verifies the
  same regular non-symlink entry through the directory root before atomically
  renaming it over the final basename. No final destination is opened before
  that rename.
- Abort behavior: every pre-publication failure closes open descriptors,
  removes only the temporary basename through the bound directory root, and
  reports cleanup errors together with the primary failure. The old
  destination therefore remains byte/mode intact, while an absent destination
  remains absent.
- Regression proof: `TestStageImportsCopyFailureNeverPublishesPartialArtifact`
  deterministically injects failures after copy, chmod, and close, each with an
  existing and absent destination. All six cases prove source preservation,
  destination preservation/non-creation, and removal of the randomized
  temporary entry.
- Post-correction gates: isolated focused test, isolated race test, and isolated
  vet passed; focused `tfrender` lifecycle/differential tests passed; all four
  standing `RootCatalog|Transform|Topology|Generation` byte-gates passed.
- Second blocking finding: after the atomic rename had committed publication,
  the first correction returned `root.Close()` directly. A close error could
  therefore report operation failure even though the final destination had
  already changed successfully.
- Commit-point correction: the successful same-directory rename is now
  explicitly the transaction commit point. The directory root is still closed,
  but its post-commit close error is deliberately non-fatal because publication
  cannot be rolled back and returning it would be a false failure. The
  `closeAfterRename` test seam closes the real root and then injects an error;
  `TestStageImportsPostRenameCloseFailureDoesNotReportFalseFailure` proves the
  result remains successful with exact committed bytes.
- Hostile-rebind proof: `TestStageImportsRefusesReboundTemporaryArtifact`
  replaces the closed temporary file with a symlink to an outside victim before
  verification. The descriptor-bound identity check refuses publication,
  cleanup removes the symlink without following it, the prior destination is
  preserved, and the victim remains untouched.

## Review Focus

- Highest-risk files or paths: `import_filter.go`'s Python-whitespace and
  block/address scanners; `import_staging.go`'s state-list splitter, atomic
  staging publication, and stage/unstage side-effect order.
- Specific assumptions to attack:
  - Node regex `\x85` denotes Unicode U+0085, not a raw invalid UTF-8 byte;
  - only LF anchors generated import blocks, while state-list accepts the full
    wide line-separator set;
  - BOM bytes are retained and can prevent the first block from matching;
  - `copyFile` source mode is reproduced by the destination `Chmod`;
  - a failure before same-directory atomic rename never changes or creates the
    final destination, and descriptor-bound abort cleanup never follows a
    rebound temporary pathname;
  - failed state-list means keep all, not failure or empty delta;
  - state-aware processing is imports-only and backend preflight is earlier
    than `init`.
- Source evidence the reviewer should verify: the exact Node lines and test
  ranges listed under Source Inputs Consulted, independent of this summary.
- Generated artifacts the reviewer should compare: frozen filtered import
  strings, ordinary staged import/moved bytes, source-byte preservation, and
  existing `tfrender` import/move differential output.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  malformed quoted strings, braces inside strings, BOM/CR/CRLF/NEL/LS/PS,
  empty/interior state lines, grouped roots, no env root, stale destination,
  backend-config absence, state-list nonzero exit, and unstage selection scope.
