# Root catalog v1/v2 schema split adversarial review

## Blocking Findings

None.

## Non-Blocking Risks

None.

## Source Evidence Review

- Inspected the complete uncommitted diff against `9707a4a`, including both
  new files, without editing the workspace.
- Verified the v1 schema is byte-identical to both `93f04b3^` and
  `node-oracle-v1-final` and that the selected frozen Node source/test/build
  closure has zero diff from the tag.
- Verified Node imports only the unversioned v1 schema and no Node or Go
  production source changes.
- Verified v1 requires schema version 1 and `slug_label`; v2 requires schema
  version 2 and rejects `slug_label`. The v2 schema equals the PR-base schema
  after changing only `$id`, and both cross-version rejection directions pass.
- Verified CI rebuilds and fully tests Node from `node-oracle-v1-final`, checks
  its exact bundle digest, then runs current Go formatting, vet, full tests,
  retained differentials, v2 authorities, and candidate distribution gates.
- Verified every pinned Action SHA resolves to its stated release and
  `actionlint` passes.
- Verified `make check` and `make test` follow Go product authority while
  default operator/release routing remains Node.
- Verified the candidate gate excludes only the still-v1 checked-in demo. The
  complete Go-v2 demo input remains covered by an exact 46-file authority
  golden, and profile/pruned jobs validate actual selected filesystem roots.
- Provider schemas, OpenAPI contracts, source evidence, readiness reports,
  and pack metadata have no patch delta.
- Hosted CI and its pruned jobs remain the only evidence unavailable before a
  reviewed commit is pushed.

## Generated Artifact Review

- Reports, fixtures, snapshots, catalogs, and count/coverage outputs have no
  patch drift.
- The v1/v2 schema provenance and semantic exclusivity were independently
  verified.
- The only accepted artifact delta is the intentional versioned schema split.

## Verification

- Focused schema/version tests: pass.
- `gofmt`, `go vet ./...`, and full uncached `go test -count=1 ./...`: pass.
- `make differential`, the full-profile Go candidate distribution, and
  `make check-root-catalog`: pass.
- `actionlint` v1.7.7 and `git diff --check`: pass.
- Frozen-source and artifact non-drift checks: pass.

## Verdict

**Approve.** The patch restores the exact frozen v1 contract, introduces a
source-identical versioned v2 contract, preserves strict version separation,
and correctly splits frozen-Node and current-Go CI without weakening authority,
distribution, or artifact gates.
