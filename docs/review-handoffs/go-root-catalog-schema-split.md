# Root catalog v1/v2 schema split builder handoff

Status: APPROVED by fresh adversarial review with no blocking or non-blocking
findings. The coordinator has not yet committed or pushed this change.

## Intent

- Preserve the frozen Node v1 root-catalog validator at its immutable import
  path.
- Give the Go singleton-state authority an explicitly versioned v2 schema.
- Repair PR #249 CI without backporting singleton topology into frozen Node or
  weakening either version's validation.
- Keep all catalog, topology, module, evidence, and runtime output bytes
  unchanged.

## Base / Head

- Base: `9707a4a7d3d87ba3a20b2d63be05930d80c94f47`.
- Head: uncommitted working tree on
  `feature/go-authority-singleton-state-v2`.
- Diff command: `git diff 9707a4a7d3d87ba3a20b2d63be05930d80c94f47`.

## Files Changed

- Restored v1 contract: `docs/schemas/root-catalog.schema.json`.
- Added v2 contract: `docs/schemas/root-catalog.v2.schema.json`.
- Pointed the Go-v2 schema gate to the versioned contract:
  `go/internal/metadata/gate_test.go`.
- Made the existing Make distribution gate runtime-selectable without changing
  its Node default, and routed PR CI through the Go candidate while testing the
  complete frozen Node suite from `node-oracle-v1-final`:
  `Makefile`, `demo/Makefile`, and `.github/workflows/check.yml`.
- Corrected historical G1 evidence paths:
  `docs/review-handoffs/go-singleton-state-g1.md`.
- Added this builder handoff.
- Recorded the fresh read-only verdict in
  `docs/review-handoffs/go-root-catalog-schema-split-adversarial-review.md`.
- Intentionally untouched: Node/TypeScript source, Go production source,
  catalog artifacts, module artifacts, release/default runtime routing, frozen
  Node bundle, and provider/evidence inputs.

## Source Inputs Consulted

- Frozen v1 schema bytes from
  `93f04b3^:docs/schemas/root-catalog.schema.json`.
- The v2 schema introduced by `cf5854b` and the v2 golden catalog at
  `catalogs/zscaler-root-catalog.v2.json`.
- Frozen Node validator import in `node-src/contracts/validators.ts`.
- Go v2 gate in `go/internal/metadata/gate_test.go`.
- PR #249 failure mode: frozen Node tests consumed the shared filename after
  it had been changed to schema v2.

## Generated Artifacts

- Schemas: one versioned v2 schema is added; the unversioned path returns
  byte-for-byte to its frozen v1 contents.
- Reports, fixtures, snapshots, demo outputs, catalogs, and module artifacts:
  unchanged.
- Artifact drift intentionally expected: none outside the schema split itself.

## Expected Delta

- Frozen Node continues validating schema-version-1 catalogs with
  `slug_label` through `root-catalog.schema.json`.
- Go validates schema-version-2 catalogs without `slug_label` through
  `root-catalog.v2.schema.json`.
- CI runs the complete Node v1 gate in the immutable tagged tree, then runs
  current-tree Go tests, retained differentials, v2 goldens, and distribution
  matrices through `dist/iw`.
- The Go candidate distribution gate intentionally excludes `check-examples`:
  the checked-in demo remains a v1 rollback artifact until cutover, while the
  complete v2 demo transform is already byte-gated under `make v2-authority`.
  Metadata, every selected module, tfvars formatting, and every pack remain in
  the candidate distribution matrix.
- The repository `make test`/`make check` development gates now follow the
  declared Go product authority. `make check-node` remains the complete v1
  command for the immutable tagged tree. Shipped/default operator command
  routing remains Node until the separately governed cutover decision.
- Node and Go reject the other version at their respective schema boundary.
- No runtime or operator-visible artifact bytes change.

## Invariants Claimed

- The restored v1 schema is byte-identical to the authority-handoff parent.
- The new v2 schema is byte-identical to the schema at the PR base except for
  its versioned `$id`.
- Node source remains frozen and receives no topology backport.
- Go production source remains unchanged.
- Neither validator is disabled, relaxed, or routed around.
- The complete Node `make check-node` contract and v1 demo drift gate remain
  unchanged in the tagged tree; the Go candidate has a separately named
  pre-cutover distribution gate.
- Evidence, provenance, ambiguity, readiness, and adoption safety behavior are
  unchanged.

## Tests Run

- Frozen schema/source closure:
  `git diff --exit-code node-oracle-v1-final -- <frozen inputs>` passed;
  `root-catalog.schema.json` is byte-identical to the tagged v1 schema, and
  the new v2 schema differs from the PR-base schema only in its versioned
  `$id`.
- Focused schema gate:
  `go test ./internal/metadata -run
  'TestRootCatalog(V2ByteGate|SchemasAreVersionExclusive)|TestFrozenV1RootCatalogSHA256'
  -count=1 -v` passed, including both cross-version rejection directions.
- Frozen Node worktree at `node-oracle-v1-final`: `npm ci --ignore-scripts`,
  `npm run check:all`, `npm run build`, and
  `node scripts/test-runtime-release.mjs` passed. The suite reported 793 pass,
  zero fail, and two documented optional skips. The rebuilt bundle SHA-256 was
  exactly `ce48c2c6...` and its checksum file matched.
- Current Go tree: `gofmt -l cmd internal` was empty; `go vet ./...` passed;
  `go test ./... -count=1` passed every package. An initial parallel Node/Go
  run caused one fixture compiler subprocess to be killed for resource
  contention; its immediate isolated rerun and the complete sequential Go run
  both passed.
- `make check`, `make check-root-catalog`, and `make differential` passed.
  The differential run includes every exact v2 authority golden before the
  retained topology-independent v1 comparisons.
- All ten candidate profile distributions passed through `dist/iw`: empty,
  aws, cloudflare, google, netbox, zcc, zia, zpa, ztc, and zscaler.
- `actionlint` v1.7.7 passed `.github/workflows/check.yml`.
- `make check-core` passed against an empty temporary pack root through the Go
  authority.
- `git diff --check` passed.
- Not yet run: hosted PR CI and its two pruned-checkout jobs; those require a
  reviewed commit pushed to PR #249.
- No credentials, provider APIs, remote backends, Kubernetes, or live Apply
  are needed or authorized for this patch.

## Known Deferrals

- Node archive and release cutover remain governed by the existing roadmap.
- The unrelated field-lineage experiment remains separate from PR #249.

## Review Focus

- Prove the v1 schema exactly matches its frozen pre-G1 bytes.
- Prove the v2 schema retains every v2 restriction while using a distinct
  filename and `$id`.
- Verify Node imports only v1 and the Go-v2 gate imports only v2.
- Attack both cross-version rejection directions so this cannot become a
  permissive dual-version schema.
- Confirm no catalog or other generated artifact bytes drifted.
