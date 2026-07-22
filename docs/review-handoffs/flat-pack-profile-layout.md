# Builder Handoff: Flat Pack Profile Layout

## Intent

- Establish the current `packs/*.packset.json` layout by colocating exact
  distribution profiles with the pack directories they select.
- Prove mechanically that every currently committed profile selection is
  derivable from pack name, `vendor`, and `requires_shared` metadata.
- Preserve exact-profile validation, reduced distributions, the immutable
  Node-v1 rollback lane, and the independent `full` distribution lock.

## Base / Head

- Base: `5b9abaeae23dfcc457aa3aeb8416249375eb97d7`
- Head: the commit containing this handoff on
  `feature/flatten-pack-layout`.
- Diff command: `git diff 5b9abaeae23dfcc457aa3aeb8416249375eb97d7..HEAD`

## Files Changed

- Files:
  - 11 byte-identical current profiles added as `packs/<name>.packset.json`;
  - the existing `packsets/<name>.json` files retained as frozen Node-v1
    compatibility copies;
  - Go CLI defaults, Go tests, Make targets, CI, profile materialization, and
    candidate runtime-release verification updated for the current paths;
  - `go/internal/metadata/loader_test.go` gains the exhaustive derivability
    proof;
  - a parity test and candidate-release checks prevent the compatibility copies
    from drifting from the current profiles;
  - active pack-distribution, runtime, repository-surface, roadmap, and
    operator documentation updated.
- Files intentionally left untouched:
  - all pack manifests, registries, schemas, overrides, collectors, and shared
    component content;
  - generated root catalog and provider/source evidence;
  - `node-src/`, `node-tests/`, the Node-v1 authority manifest, its immutable
    test constants, and the frozen bundle identity;
  - historical review handoffs, whose old commands describe their historical
    revisions;
  - untracked `reports/` and ignored local Python caches.

## Source Inputs Consulted

- Provider schemas: existing committed schemas only; no schema content was
  changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: every committed `packs/*/pack.json`, specifically pack name,
  `vendor`, and `requires_shared`.
- Existing docs or design records: `docs/pack-distributions.md`, runtime and
  repository-surface docs, and the adversarial-review workflow.
- Other source evidence: the `node-oracle-v1-final` source-diff gate and frozen
  bundle/authority contract.

## Generated Artifacts

- Reports: none committed.
- Schemas: none.
- Fixtures: none changed.
- Snapshots: none changed.
- Demo or lab outputs: none committed.
- Artifact drift intentionally expected: none in frozen authorities. Each new
  flat profile is byte-identical to its corresponding Node-v1 compatibility
  document.

## Expected Delta

- Expected behavior change: current Go/Make callers use
  `packs/<name>.packset.json`. Transitional releases contain both layouts so
  the frozen Node-v1 executable continues to resolve `packsets/<name>.json`.
  Pack discovery still considers directories only, so colocated profile files
  are not packs.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: profile membership, exact distribution enforcement,
  resource metadata, provider readiness, source evidence, reconciliation,
  Transform, Adopt, and generated Terraform semantics.

## Invariants Claimed

- Evidence must not be silently dropped: exact profile validation remains
  independent of installed directories; `full` is not synthesized from the
  root it validates.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: unchanged; profile and
  catalog paths remain independently selectable.
- Ambiguity must stay classified instead of being coerced to success:
  unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: unchanged; the release smoke test remains
  Python-disabled and uses external selected pack directories.
- Frozen compatibility: `node-src/`, `node-tests/`, frozen authorities, and the
  Node-v1 bundle identity remain byte-unchanged; both profile layouts must have
  identical names and bytes.
- Derivability: `empty` selects none, `full` selects all manifests, each
  single-pack profile selects its same-named manifest, and `zscaler` selects
  every manifest whose vendor is Zscaler; all include the exact
  `requires_shared` union.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `go vet ./...`
  - `go test ./...`
  - focused Go metadata, CLI, environment, module, and frozen-authority tests
  - frozen-source diff against `node-oracle-v1-final`
  - `make check-candidate-distribution`
  - `node scripts/test-runtime-release.mjs`
  - `node scripts/materialize-pack-profile.mjs copy --profile
    packs/zia.packset.json ...` followed by Go `check-pack-set`
  - `git diff --check`
  - byte comparison of every current profile with its compatibility copy.
- Relevant output summary:
  - frozen-source diff against `node-oracle-v1-final` is empty;
  - rebuilt frozen Node-v1 bundle SHA-256 remains
    `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`;
  - typecheck, vet, the complete Go suite, frozen authority verification,
    retained v1 differentials/v2 authorities, profile parity/derivability, and
    the current candidate distribution gate pass;
  - candidate committed-tree release staging passes with the frozen Node-v1
    executable and both profile layouts.
- Tests not run and why: the complete mutable Node test suite was not rerun
  after remediation because every frozen Node source, test, and fixture is
  byte-unchanged and that suite has 20 independently reproduced baseline
  failures. The frozen-source diff, oracle digest, retained differentials, and
  candidate release smoke cover this integration boundary. No credentialed
  provider tests were in scope.

## Known Deferrals

- Deferred work: removing `packsets/` and replacing committed profiles with
  generated ephemeral files.
- Reason it is safe to defer: derivation is now proven, but generating the
  `full` expectation from an already damaged installed root would make a
  deletion invisible. A future generator needs an independent inventory
  authority before the exact lock can be removed.
- Follow-up owner or trigger: remove the compatibility directory with the Node
  archival/cutover parcel; revisit generation when a source-independent pack
  inventory can generate and verify the lock deterministically.

## Review Focus

- Highest-risk files or paths: `go/internal/metadata/loader_test.go`, Go
  package-root/default path wiring, workflow changes, and runtime release
  scripts.
- Specific assumptions to attack:
  - every current Go/Make pack-profile consumer was migrated without changing
    the frozen Node lane;
  - profile files cannot be mistaken for installed pack directories;
  - reduced/profile-materialized roots preserve exact selection behavior;
  - transitional runtime archives include byte-identical profiles in both
    layouts;
  - derivation uses only committed metadata and handles shared dependencies;
  - retaining `full` really prevents accidental deletion from self-validating.
- Source evidence the reviewer should verify: every pack manifest's name,
  vendor, and shared requirements, plus all 11 profile selections.
- Generated artifacts the reviewer should compare: none; verify the frozen
  Node bundle and authority identity are unchanged.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  manifestless directories, profile-like directories, unknown future aggregate
  profiles, nested or transitive shared requirements, a missing profile file,
  pruned roots retaining unrelated profile documents, and stale historical
  commands being mistaken for active wiring.

## Initial Review And Remediation

- Initial verdict: request changes for repository-authority blockers; the
  physical layout and derivability design were accepted.
- Accepted findings:
  - the first patch rewrote immutable Node-v1 source, fixtures, authority pins,
    and bundle identity;
  - the PR frozen-source and authority gates would therefore fail;
  - changed candidate release scripts were not exercised by branch CI.
- Remediation:
  - restored every frozen Node-v1 input and authority byte-for-byte;
  - retained `packsets/*.json` as compatibility copies and added exact parity
    checks in Go and the candidate release verifier;
  - retained `packs/*.packset.json` as the current Go/Make layout;
  - added a candidate-commit release-staging job separate from the immutable
    Node-oracle job;
  - deferred physical removal of `packsets/` to Node archival.
