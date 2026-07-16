# Cross-state Backend Remediation Review Handoff

## Intent

- Close the accepted final-stack blocker in which an AzureRM backend JSON
  denylist admitted unreviewed or credential-bearing fields such as
  `oidc_token_file_path` into generated `terraform_remote_state` configuration.
- Replace the ordinary preflight/read pair with one bounded stable UTF-8 read,
  and regenerate the source-bound authorities invalidated by PR #225 pack
  metadata.
- Keep cross-state references opt-in, preserve environment-owned credentials,
  preserve the accepted binding and saved-plan contracts, and avoid changing
  provider, root, state-key, or expression semantics.

## Base / Head

- Base: `ece2b0c5efa00c2afb2e537aa20733a0bd93cee0`
- Head: `a91936cc3d8d961b6a71650ab85ac7e2e1005187`
- Diff command:
  `git diff ece2b0c5efa00c2afb2e537aa20733a0bd93cee0..a91936cc3d8d961b6a71650ab85ac7e2e1005187`

## Files Changed

- Files:
  - `.github/workflows/check.yml`
  - `catalogs/zpa-transform-cohort-catalog.v1.json`
  - `catalogs/zscaler-root-catalog.v1.json`
  - `docs/adoption-command-surface.md`
  - `docs/evidence/zpa-provider-v4.4.6.json`
  - `docs/provider-labs/cross-state-reference-qualification.md`
  - `node-src/domain/reference-backend.ts`
  - `node-tests/plan-lifecycle.test.ts`
  - `tests/test_zpa_transform_cohort_catalog.py`
- Files intentionally left untouched: cross-state topology, expression
  rendering, saved-plan contracts, exact Apply, provider adapters, pack
  reference metadata, and every descendant PR.

## Source Inputs Consulted

- Provider schemas: existing committed ZPA provider 4.4.6 evidence authority;
  no provider schema content changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: `packs/zpa/pack.json`, whose new reference declarations in
  the reviewed PR are the only source-input change behind the regenerated
  digests.
- Existing docs or design records: `docs/adoption-command-surface.md`,
  `docs/provider-labs/cross-state-reference-qualification.md`, and HashiCorp's
  AzureRM backend contract.
- Other source evidence: the repository bounded-file reader contract and the
  accepted external review of cumulative PR #228.

## Generated Artifacts

- Reports: N/A.
- Schemas: N/A.
- Fixtures: N/A.
- Snapshots:
  - `catalogs/zscaler-root-catalog.v1.json`
  - `catalogs/zpa-transform-cohort-catalog.v1.json`
  - `docs/evidence/zpa-provider-v4.4.6.json`
- Demo or lab outputs: N/A; no credentials, backend, provider, or live state
  were accessed.
- Artifact drift intentionally expected: source-binding digests only. Resource
  rows, provider evidence, transform cohort membership, counts, and topology
  are unchanged.

## Expected Delta

- Expected behavior change: AzureRM reference-backend JSON accepts only five
  reviewed non-empty string address fields and five reviewed boolean behavior
  fields. Unknown fields and wrong types fail closed before plan execution.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none for accepted inputs; invalid backend
  inputs now fail before `terraform_remote_state` input is emitted.
- Expected no-op areas: Fetch, Transform, Adopt projection, modules, roots,
  staging, plan assessment, exact Apply, and all non-cross-state deployments.

## Invariants Claimed

- Evidence must not be silently dropped: the three invalidated generated
  authorities are regenerated from their actual sources and remain exact.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: the evidence and cohort
  files retain their source lists and only their calculated digests move.
- Ambiguity must stay classified instead of being coerced to success: every
  unknown backend key is rejected rather than guessed safe.
- Provider-readiness counts must stay explainable: all resource rows and counts
  are byte-identical apart from source digests.
- Adoption safety invariants: backend credentials remain environment-owned;
  tenant/root state keys remain engine-derived; no raw configuration value is
  included in an unsafe-field diagnostic; the config is read once with a
  64-KiB stable-read bound.

## Tests Run

- Commands:
  - `npm run build:test`
  - `node --test .node-test/node-tests/plan-lifecycle.test.js`
  - `node --test .node-test/node-tests/differential.test.js .node-test/node-tests/zpa-transform-cohort.test.js`
  - `python3 -m unittest tests.test_zpa_provider_evidence tests.test_zpa_transform_cohort_catalog`
  - `python3 tools/zpa_provider_evidence.py`
  - exact regeneration and `cmp` for both generated catalogs
  - `npm run check:all`
  - `make check`
  - `git diff --check`
- Relevant output summary: focused plan lifecycle 18/18; focused Node
  authority/differential 24/24; Python authority 20 passed and 1 skipped;
  full Node 1,278 passed, 0 failed, 1 skipped; repository gate 845 passed,
  0 failed; pack requirements, pack validation, and vendor-boundary audit
  passed.
- Tests not run and why: no live backend/provider test because this remediation
  is credential-free and does not authorize backend access or deployment
  Apply. GitHub exact-head CI is pending until review acceptance and push.

## Known Deferrals

- Deferred work: live final-head scalar qualification, a fresh-workspace
  no-op rerun, and the indexed-list cohort.
- Reason it is safe to defer: cross-state mode remains explicit and the
  qualification runbook already treats those as pre-production evidence gates.
- Follow-up owner or trigger: approved downstream qualification before broad
  production use.
- Deferred work: bind a saved referrer plan to the precise referent state
  version or enforce a common lock during the plan/apply interval.
- Reason it is safe to defer: current opt-in documentation forbids referent
  mutation in that interval; production expansion remains gated on the live
  qualification cohort.
- Follow-up owner or trigger: cross-state promotion beyond the bounded pilot.
- Deferred work: immutable SHA pinning for third-party GitHub Actions.
- Reason it is safe to defer: Terraform itself is now pinned exactly to
  1.15.4; action pinning may be supplied by organization policy and is not
  part of the runtime correction.
- Follow-up owner or trigger: repository supply-chain policy decision.

## Review Focus

- Highest-risk files or paths: `node-src/domain/reference-backend.ts` and the
  three regenerated source-bound authorities.
- Specific assumptions to attack: the allowlist is sufficient but not broad;
  string/boolean types match Terraform's AzureRM backend contract; bounded-read
  error translation cannot admit a changed/special/oversized file; diagnostics
  cannot expose values.
- Source evidence the reviewer should verify: every accepted field is
  non-secret address/behavior configuration; `key`, client IDs, tokens,
  token/certificate paths, MSI endpoints, and unknown authentication material
  are excluded.
- Generated artifacts the reviewer should compare: confirm the source-digest
  fields are the only generated changes and independently regenerate them.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  symlink targets, malformed UTF-8, a 64-KiB boundary, unknown keys, wrong
  scalar types, secret-bearing diagnostics, stale source hashes, and Terraform
  version drift in CI.
