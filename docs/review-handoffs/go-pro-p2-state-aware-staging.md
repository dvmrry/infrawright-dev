# GPT-5.6 Pro P2: state-aware staging publication

Status: APPROVED by fresh adversarial review. The candidate remains unpushed.

Review result: no blocking findings and no non-blocking risks. The reviewer
independently inspected the diff and handoff and reran the focused tests, race
tests, full Go suite, vet, formatting, and diff checks.

## Intent

- Close the state-aware `stage-imports` path that wrote filtered imports with
  `os.WriteFile`, bypassing the existing descriptor-bound atomic publisher.
- Publish filtered bytes through the same private temporary-file, identity
  recheck, and descriptor-relative rename transaction used by ordinary import
  and moved artifacts.
- Preserve filtered bytes, source permissions, diagnostics, state loading, and
  empty-filter deletion behavior.

## Base / Head

- Base: `7ad6513fa3e34e89352ac2b3e42cede01cf6e277`
- Head: current uncommitted worktree on `feature/go-canonjson-foundation`
- Diff command: `git diff 7ad6513 -- go/internal/adopt/import_staging.go go/internal/adopt/import_staging_test.go docs/review-handoffs/go-pro-p2-state-aware-staging.md`

## Files Changed

- `go/internal/adopt/import_staging.go`
- `go/internal/adopt/import_staging_test.go`
- This handoff.
- Intentionally untouched: filtering rules, Terraform state loading,
  empty-filter deletion, artifact renderers, exact Apply, canonjson, reports,
  schemas, fixtures, provider code, and CLI wiring.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: GPT-5.6 Pro whole-migration report;
  `docs/adversarial-review.md`; the existing transaction and its tests in
  `go/internal/adopt/import_staging.go`.
- Other source evidence: `os.Root` descriptor-relative operations and the
  pre-existing `copyStagingArtifact` transaction.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: no committed fixtures changed.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: a non-empty state-aware filtered import artifact
  is written completely to a randomized private sibling, assigned the source
  artifact's permission bits, identity-checked, and atomically renamed over
  the destination. Destination symlinks and hard links are replaced rather
  than followed or modified.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none; the published filtered bytes are
  unchanged.
- Expected no-op areas: filtering, state-list call count/order, diagnostics,
  empty delta deletion, ordinary import/move publication semantics, and all
  artifact/report bytes.

## Invariants Claimed

- Evidence must not be silently dropped: unchanged; no evidence code touched.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged.
- Ambiguity must stay classified instead of being coerced to success:
  unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: failure before rename preserves the previous
  destination and removes the transaction file; the transaction never follows
  the destination symlink/hard-link inode; the source reader and directory
  descriptor are closed on every pre-commit path; rename remains the sole
  publication commit point.

## Tests Run

- `go test ./internal/adopt -run 'TestStageImports(StateAware|Copy|PostRename|Refuses)' -count=1`
- `go test -race ./internal/adopt -run 'TestStageImports(StateAware|Copy|PostRename|Refuses)' -count=1`
- `go test ./...`
- `go vet ./...`
- `go test ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation' -count=1`
- `gofmt` and `git diff --check`
- Relevant result: all pass. New regressions cover destination symlink,
  destination hard link, injected pre-rename failure with no remnant, and
  source mode `0600` preservation.
- Tests not run and why: no live Terraform/provider test is relevant or
  authorized; publication behavior is exercised against local temporary
  directories only.

## Known Deferrals

- Deferred work: descriptor-binding the selected environment root itself and
  changing empty-filter deletion.
- Reason it is safe to defer: neither behavior was introduced or weakened by
  this parcel; the finding concerns only the `os.WriteFile` publication bypass.
- Follow-up owner or trigger: revisit only under a separately authorized
  staging-root or unstage/removal hardening review.

## Review Focus

- Highest-risk files or paths: reader ownership in `publishStagingArtifact`,
  abort ordering, identity verification, and descriptor-relative rename.
- Specific assumptions to attack: every input is closed exactly once; a close
  or injected failure before rename cannot alter the old destination; source
  mode comes from the same opened source file as filtered content; rename over
  a symlink/hard link does not follow the referenced inode.
- Source evidence the reviewer should verify: the old ordinary-copy path and
  the state-aware `os.WriteFile` branch at the base commit.
- Generated artifacts the reviewer should compare: none beyond exact bytes in
  the focused tests and the standing byte gates.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  none; reject any change to filtering output, state loading, diagnostics, or
  empty-delta accounting.
