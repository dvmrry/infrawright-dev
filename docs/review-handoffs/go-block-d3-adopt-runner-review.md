# Block D3 Adopt Runner — Builder Review Handoff

## Intent

- Solve: port `adoption-meta.ts`, `state-project.ts`, and `adopt-runner.ts`,
  including PR 247's pack/user adoption-policy merge and logical-root Oracle
  batching.
- Behavior change: the Go runtime can classify and derive adoption identities,
  project provider-observed state, load merged adoption policy, and orchestrate
  per-resource or logical-root adoption through injected D1 state loaders.
- Must stay unchanged: `canonjson`/`tfrender` remain the sole committed-byte
  renderers; existing `terraformcmd` and D1 Oracle behavior remain untouched;
  D2 staging and `cmd/iw` remain untouched; no live-provider Apply is added or
  qualified here.

This is a builder handoff, not approval. The change is ready for fresh-context
adversarial review.

## Base / Head

- Base: `9b669a0` (the reviewed D2 commit on this branch)
- Head: uncommitted working tree on
  `feature/go-canonjson-foundation` atop `9b669a0`; the coordinator must stage
  only the D3 files below before using an ordinary staged diff.
- Diff command after staging the listed files: `git diff --cached --
  go/internal/adopt/adoption_meta.go go/internal/adopt/adoption_meta_test.go
  go/internal/adopt/policy.go go/internal/adopt/policy_test.go
  go/internal/adopt/runner.go go/internal/adopt/runner_loaders.go
  go/internal/adopt/runner_test.go go/internal/adopt/state_project.go
  go/internal/adopt/state_project_test.go
  go/internal/metadata/driftpolicy_adopt.go
  go/internal/transformrun/adopt_seams.go
  go/internal/tfrender/adopt_seams.go
  docs/review-handoffs/go-block-d3-adopt-runner-review.md`

## Files Changed

- Production:
  - `go/internal/adopt/adoption_meta.go` (567 lines)
  - `go/internal/adopt/policy.go` (123 lines)
  - `go/internal/adopt/runner.go` (878 lines)
  - `go/internal/adopt/runner_loaders.go` (81 lines)
  - `go/internal/adopt/state_project.go` (788 lines)
  - `go/internal/metadata/driftpolicy_adopt.go` (8 lines)
  - `go/internal/transformrun/adopt_seams.go` (47 lines)
  - `go/internal/tfrender/adopt_seams.go` (13 lines)
- Tests:
  - `go/internal/adopt/adoption_meta_test.go` (320 lines)
  - `go/internal/adopt/policy_test.go` (96 lines)
  - `go/internal/adopt/runner_test.go` (685 lines)
  - `go/internal/adopt/state_project_test.go` (182 lines)
- Total D3 Go delta: 3,788 new lines; no existing Go source was modified.
- Files intentionally left untouched: D1 Oracle/generated-config-policy files,
  D2 import staging/filter files, `cmd/iw`, existing renderer implementation,
  `go.mod`, and `go.sum`.

## Source Inputs Consulted

- Provider schemas: committed pack schemas through
  `metadata.LoadedPackRoot.LoadResourceSchema`; synthetic source-shaped schemas
  in state-projection and runner tests.
- OpenAPI/API contracts: N/A; D3 consumes already-fetched JSON only.
- Provider source files: N/A; D3 never calls a provider API.
- Pack metadata: active manifest order, registry `adopt`, legacy override
  fallback, references, logical roots, and `drop_if_default`.
- Existing docs/design records:
  `docs/review-handoffs/go-block-d-adopt-apply.md`,
  `docs/go-runtime-v2.md`, `docs/adversarial-review.md`.
- Other source evidence:
  - `node-src/domain/adoption-meta.ts`
  - `node-src/domain/state-project.ts`
  - `node-src/domain/adopt-runner.ts`
  - `node-src/domain/drift-policy.ts`
  - `node-tests/adoption-meta.test.ts`
  - `node-tests/state-project.test.ts`
  - `node-tests/adopt-runner.test.ts`
  - `node-tests/fixtures/zia-adoption-classification-v4.7.26.json`

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none added; the committed ZIA adoption-classification fixture is
  consumed directly.
- Snapshots: none committed.
- Demo/lab outputs: temporary test-only config/import trees under `t.TempDir`.
- Artifact drift intentionally expected: none. D3 routes imports and tfvars
  through existing `tfrender`; the four standing byte gates remain green.

## Expected Delta

- Expected behavior change:
  - source-order system-skip/unsupported classification and exact lossless
    identity derivation;
  - PR 247 version-one pack/user policy merge;
  - exact state-projection order: schema projection, `projection_sync`,
    `projection_fill`, pack `drop_if_default`, `projection_omit_if`;
  - deterministic per-resource and logical-root adopt orchestration;
  - root-wide fail-closed preflight, exact Oracle key coverage, repeated pending
    move checks, collect/compile/publish batching, and sequential isolation
    diagnostics after a batch-loader failure.
- Expected report/count changes: D3 emits Node-compatible adoption diagnostics
  and terminal counts; D5 owns CLI exit-code differential coverage.
- Expected generated-output changes: new Go runner output should be the same
  bytes existing `tfrender` emits for equivalent transform results.
- Expected no-op areas: D1, D2, plan lifecycle, existing artifact renderers,
  dependency graph, and CLI wiring.

## Adversarial Review Findings Resolved

- Nil drift policies are rejected before classification, state loading, or
  publication by both public runner entry points.
- Raw input is recursively checked for `snake_case` key collisions before
  classification or identity derivation, including both top-level JSON source
  orders and nested identity fields.
- Adoption `identity_fields` and `identity_renames` reject aliases that
  normalize to the same key.
- Classification counts survive identity-derivation failures in both the
  per-resource and logical-root paths; the tests assert exact diagnostic/count
  ordering, including Node's failing-member-first root order.
- A nil `RunAdoptBatch` environment snapshots `processEnvironment()`; an
  explicit empty map remains empty. In contrast, the default state and batch
  loader constructors require an explicit environment, clone it, eagerly
  validate `INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS`, and return an error before a
  callable loader is exposed.
- Root-level tests now prove: unsupported input blocks every loader, external
  referents force member-loader fallback, pending moves appearing after the
  batch load block publication, one-member isolation failure preserves exact
  diagnostic ordering, and unexpected Oracle resources reject the entire root.
- Diagnostic `name: null` falls back to `id`; metadata precedence, duplicate
  derived key/import ID, and strict boolean/number/null matching tests were
  strengthened.

## Invariants Claimed

- Evidence must not be silently dropped: unsupported classifications block the
  entire relevant logical root before any state loader or publication; exact
  Oracle key mismatch is an error.
- Generic matcher evidence must not outrank source-backed evidence: strict
  scalar matching keeps booleans distinct from numeric one/zero; system skips
  run before static unsupported rules exactly as the TS source specifies.
- Source precedence/provenance must remain explicit: registry adoption metadata
  overrides legacy fields; active pack policy merges in manifest order and user
  policy is independently validated before append.
- Ambiguity must stay classified instead of being coerced to success: malformed
  metadata, duplicate keys/import IDs, mixed providers, missing/unexpected state,
  schema type mismatch, repeated-block sync, sensitive state, and pending moves
  all fail closed.
- Provider-readiness counts must stay explainable: fetched, system-skipped,
  unsupported, eligible, published, and failed are derived from one preflight
  classification and emitted in deterministic candidate order.
- Adoption safety invariants:
  - no goroutines; Read/state loading and diagnostics remain serial;
  - no Terraform or API resolution occurs in `RunAdoptBatch`; loaders are
    injected;
  - default loaders reject a nil environment, clone an explicit environment,
    eagerly validate timeout configuration, and launch nothing when
    constructed;
  - logical-root output is fully projected and compiled before batch publish;
  - pending moves are checked before state loading, after projection, and again
    immediately before publish;
  - functional state-loading tests use injected loaders and ephemeral local
    paths; the default-loader tests stop at eager constructor validation, before
    any command, credentials, real API, or Terraform Apply can be reached.

## Dependency Use

- D3 adds no dependency and does not change `go.mod`/`go.sum`.
- D3 directly uses no external library. It consumes D1's typed
  `OracleStateObject`/`OracleBatchState`, which already use the authorized
  `terraform-json` dependency.
- `canonjson` and `tfrender` remain hand-rolled and unchanged for all
  byte-reaching iteration/rendering.
- The current module at this base contains `terraform-json v0.28.0` plus its
  existing minimal transitive set. The remaining authorized Block D libraries
  belong to their consuming parcels; D3 did not add speculative imports.

## Tests Run

- `gofmt -l` over every D3 Go file: clean, no output.
- `go test ./internal/adopt -count=1`: pass (`ok`, 1.938s).
- `go vet ./...`: pass before the constructor-asymmetry follow-up;
  `go vet ./internal/adopt`: pass after it.
- `go test ./... -count=1`: pass across all packages before the
  constructor-asymmetry follow-up.
- `go test ./internal/adopt -run
  'RunAdoptBatchNilEnvironment|DefaultAdoptionLoaders' -count=1`: pass (`ok`,
  0.200s) after the follow-up.
- `go test ./internal/adopt -count=1`: pass (`ok`, 2.103s) after the follow-up.
- `go test -race ./internal/adopt -count=1`: pass (`ok`, 3.264s) after the
  follow-up.
- `go test ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation' -count=1`:
  pass (`ok`, 12.807s) after the follow-up; standing artifact bytes remain
  byte-identical.
- `bash /Users/dm/.codex/skills/go-code-review/scripts/pre-review.sh --force
  ./...`: gofmt and vet pass; `golangci-lint` skipped because it is not installed.
- Focused D3 tests prove:
  - all 33 committed adoption metadata entries resolve;
  - the committed ZIA v4.7.26 classification fixture matches keep/skip/
    unsupported counts;
  - wide-number, escaped-template, non-ASCII fallback, malformed metadata,
    normalized raw-key/metadata-alias collisions, duplicate derived identities,
    and validation-before-loader behavior;
  - exact version-one merge including `10e-1`/`1.0` and near-one rejection;
  - nested computed removal, required/sensitive rejection, sync/fill/default/
    conditional-omit order, collection-element removal, and sync guards;
  - nil-policy rejection; `RunAdoptBatch` nil-versus-explicit-empty environment
    semantics; and explicit-environment rejection plus eager timeout validation
    by both default loader constructors;
  - pending-move appearance after member or batch loading blocks publication;
  - logical-root batch publication is atomic and deterministic across repeated
    runs;
  - root-wide unsupported input, unexpected Oracle resources, projection
    failure, and batch-loader failure publish nothing;
  - external referents force deterministic member-loader fallback; isolation
    probes remain sequential and exact one-member failure diagnostics precede
    the root summary;
  - identity-derivation failures retain exact classification counts in both
    orchestration paths.
- Tests not run: a D5 CLI exit-code corpus and a real-provider/credentialed
  flow, because they are outside D3 and live provider Apply is explicitly not
  authorized. No Terraform binary was invoked by D3 tests.

## Known Deferrals

- D5 owns `cmd/iw` wiring and full CLI stdout/stderr/exit-code differential
  fixtures.
- Controlled Apply qualification against live provider state remains a separate
  human-gated event and is not authorized by this work.
- D4 owns exact-plan-apply scratch handling and ALLOW/current-branch gates.
- A unified Terraform invocation path remains the recorded future decision; D3
  reuses D1 and does not replace working `terraformcmd` code.

## Review Focus

- Highest-risk paths: `runner.go` logical-root rollback/fallback and atomic
  publication; `state_project.go` recursive sensitivity and collection mutation;
  `policy.go` exact numeric version handling; `adoption_meta.go` precedence and
  duplicate detection.
- Assumptions to attack:
  - every map-derived diagnostic or byte-reaching walk is sorted where order is
    observable;
  - root-wide unsupported/malformed input can never reach a loader;
  - an external referent that is not yet handled disables root batching without
    leaking buffered diagnostics or count mutations;
  - batch failure isolation cannot publish member output;
  - pending-move appearance in each TOCTOU window blocks mutation;
  - projection-sync type/shape guards exactly match TS for nested map/object/
    list/set encodings;
  - recursive array element removal returns the resized slice to its parent;
  - policy entry match accounting survives the pack/user merge.
- Source evidence to verify: the three TS source files listed above, PR 247's
  `isSupportedDriftPolicyVersion` merge branch, and the committed ZIA fixture.
- Generated artifacts to compare: the temporary logical-root config/import
  trees from `TestRunAdoptBatchLogicalRootPublishesAtomicallyAndDeterministically`
  and the standing four artifact gates.
- Silent-risk edge cases: strict bool/number equality, unsafe integers,
  singleton constants, identity rename/alias overwrite, sensitive masks,
  repeated nested blocks, root-wide partial selection, unexpected/missing
  Oracle results, and compile/publish failures after successful state loading.
