# Builder Review Handoff: Public ZCC Adoption Artifact Operation

## Intent

- Add a public, machine-only `compile_adoption_artifacts` operation for the
  exact five-resource ZCC adoption catalog.
- Bind the existing derived bootstrap pull, deployment, root catalog, JSON
  artifact layout, no-generated-binding rule, and absent imports/moves/pending
  transition before running the already-hardened provider import/read oracle.
- Return only the existing projected
  `infrawright.zcc_adoption_artifact_set` v1 candidate. Do not expose raw pull
  values, provider state, credentials, import IDs outside generated import
  bytes, scratch paths, child diagnostics, or host authority in the request.
- Keep `compile_pull_artifacts` unchanged as the raw-transform operation. Add
  no REST collection, materialization, persistent canonical writes, refresh,
  move derivation, HCL output, drift policy, or generated reference bindings.
- Success means provider-observed candidate only. It does not claim Python
  parity, saved-plan cleanliness, destroy safety, apply readiness, live-tenant
  qualification, or cutover readiness.

## Base / Head

- Base: `c1b3c3a18d20b9c466729248489ae98cc9a6b979` (reviewed ZCC oracle host
  hardening head).
- Head: the immutable commit checked out on
  `feature/node-zcc-public-adoption-operation`; resolve with
  `git rev-parse HEAD`.
- Diff command:
  `git diff c1b3c3a18d20b9c466729248489ae98cc9a6b979...HEAD`.

## Files Changed

- Public process contract and host:
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
  - `node-src/process/types.ts`
  - `node-src/process/execute.ts`
  - `node-src/process/main.ts`
- Adoption operation, artifact contract, and semantic validation:
  - `node-src/domain/zcc-adoption-operation.ts`
  - `docs/schemas/zcc-adoption-artifact-set.schema.json`
  - `node-src/contracts/zcc-adoption-operation-semantics.ts`
  - `node-src/contracts/zcc-pull-artifact-semantics.ts`
  - `node-src/contracts/validators.ts`
  - `node-src/domain/zcc-adoption-artifacts.ts`
- Shared input authority and host environment closure:
  - `node-src/domain/zcc-pull-operation.ts`
  - `node-src/io/zcc-adoption-oracle-adapters.ts`
- Regressions and differentials:
  - `node-tests/zcc-adoption-artifact-semantics.test.ts`
  - `node-tests/zcc-adoption-process.test.ts`
  - `node-tests/zia-transform-cohort.test.ts`
- Documentation:
  - `README.md`
  - `docs/node-process-api.md`
  - this handoff.
- Files intentionally left untouched:
  - ZCC adoption and transform catalog bytes;
  - provider schema, overrides, registry, source mapping, and provider lock;
  - Python adoption/oracle production code;
  - collectors and REST clients;
  - publication/materialization/refresh/move implementations;
  - ZIA/ZPA private cohort implementations and evidence.

## Source Inputs Consulted

- Provider schemas:
  - unchanged `packs/zcc/schemas/provider/zcc.json` through the exact committed
    adoption catalog and existing projection implementation.
- OpenAPI/API contracts: N/A; the operation consumes an already-fetched pull
  and performs no REST collection or API mapping.
- Provider source files:
  - unchanged reviewed ZCC provider source evidence and exact
    `zscaler/zcc` `0.1.0-beta.1` dependency authority inherited from the base.
- Pack metadata:
  - `catalogs/zcc-adoption-catalog.v1.json`, SHA-256
    `ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7`.
  - `catalogs/zcc-transform-catalog.v1.json`, unchanged raw-compiler authority.
  - `catalogs/zscaler-root-catalog.v1.json`, exact supported topology input.
- Existing docs or design records:
  - `docs/node-process-api.md`.
  - `docs/review-handoffs/zcc-adoption-oracle-foundation.md`.
  - `docs/review-handoffs/zcc-adoption-oracle-host-hardening.md`.
  - `docs/zcc-adoption-oracle-parity-contract.md`.
- Other source evidence:
  - `node-tests/fixtures/zcc-adoption-projection-corpus.v1.json` for four
    synthetic sanitized resource observations.
  - `tests/fixtures/parity/zcc_failopen_policy_inversion.json` for the
    source-derived fail-open inversion case.
  - existing Python `engine.adopt`, `engine.import_oracle`, artifact layout,
    lookup, grouping, and rendering implementations used as the independent
    differential side.

## Generated Artifacts

- Reports: none.
- Schemas:
  - new `zcc-adoption-artifact-set.schema.json`, reusing only the public
    resource/source/root/artifact descriptor definitions while pinning the
    adoption result kind and catalog provenance.
- Fixtures: none added. Existing sanitized/source-derived fixtures feed the
  operation-level fake Terraform differential.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: none. ZCC adoption/transform catalog,
  demo, provider-lock, and Python-produced artifact bytes remain unchanged.

## Expected Delta

- Expected behavior change:
  - v1 process requests may select `compile_adoption_artifacts` with only
    `mode: bootstrap`, tenant, and one exact ZCC resource in the input;
  - the host supplies a nested optional authority using
    `INFRAWRIGHT_TERRAFORM_EXECUTABLE`,
    `INFRAWRIGHT_ZCC_ADOPTION_TEMP_ROOT`, and the existing closed
    credential/proxy/certificate environment allowlist;
  - nonempty pulls run the exact hardened Terraform transaction and return the
    provider-observed artifact candidate without persistent writes;
  - empty identity sets return empty candidate artifacts without touching the
    Terraform executable or scratch authority;
  - caller pull/deployment/root catalog and bootstrap absence gates are bound
    before the oracle and rechecked after successful oracle cleanup.
- Expected report/count/coverage changes: one new request/result union branch;
  no provider-readiness, parity-report, or coverage count change.
- Expected generated-output changes: one new public result schema. No catalog,
  fixture, snapshot, demo, tfvars, imports, or lookup byte drift.
- Expected no-op areas: raw `compile_pull_artifacts`, refresh, compare,
  materialize, acknowledge, assessment, topology, and all non-ZCC products.

## Invariants Claimed

- Evidence must not be silently dropped: the new result semantic pass binds
  source/layout/catalog/root/variable identity, canonical descriptor bytes and
  digests, tfvars/import keys, unique import IDs, and trusted-network-only
  lookup maps. The operation separately binds request mode/tenant/resource to
  the result.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or source-evidence ranking changes.
- Source precedence/provenance must remain explicit: result catalog SHA and
  sources SHA are exact constants; pull path/digest/size and root resolution
  are derived from bound workspace inputs; Terraform/provider authority stays
  host-owned.
- Ambiguity must stay classified instead of being coerced to success: malformed
  plan/state/provider joins, unsupported resource/layout, sensitive projection,
  and any source/control/bootstrap race fail closed. There is no
  `review_required` adoption success state.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  count changes.
- Adoption safety invariants:
  - exact five-resource, bootstrap-only, JSON/no-generated-binding scope;
  - source path is always `pulls/<tenant>/<resource>.json`;
  - no request field selects executable, environment, credential, state,
    import ID, catalog, lock, timeout, temporary root, source, or output root;
  - provider state remains in private scratch/in-process memory and is never a
    request or response field;
  - existing imports, moves, and pending transition are absent before and after
    the oracle transaction;
  - final source/control/bootstrap recheck occurs only after successful oracle
    cleanup, never in `finally`, so it cannot mask a primary oracle/cleanup
    failure;
  - host errors, child diagnostics, credentials, pull/provider/state values,
    import IDs, generated scratch bytes, and scratch paths do not enter errors;
  - no caller-owned path is written, replaced, or removed.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run test` with Node `v24.15.0`, Unicode `16.0`
  - `/Users/dm/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node --test .node-test/node-tests/*.test.js`
    with Node `v24.14.0`, Unicode `17.0`
  - `make test`
  - `npm run build`
  - `python3 -m engine.audit_vendor_boundary`
  - `python3 -m engine.adoption_catalog --product zcc --check catalogs/zcc-adoption-catalog.v1.json`
  - `python3 -m engine.transform_catalog --product zcc --check catalogs/zcc-transform-catalog.v1.json`
  - exact three-resource ZIA transform-catalog check
  - exact ZPA transform-cohort catalog check
  - `make test` against physically pruned exact `empty`, `zpa`, and `zscaler`
    pack roots with their committed profiles
  - `git diff --check`
- Relevant output summary:
  - full Node on each runtime: 674 total, 673 passed, one existing platform
    skip, zero failed;
  - full Python: 1,394 total, 1,393 passed, one opt-in external provider-source
    skip, zero failed;
  - operation fail-closed/differential matrix: 8/8 passed;
  - adoption artifact/result semantics: 2/2 passed;
  - extracted-binder regression focus: 87/87 passed;
  - physically pruned profiles: empty 867/867; ZPA 940 passed plus one opt-in
    skip; Zscaler 1,374 passed plus one opt-in skip;
  - vendor boundary: 187 allowed matches, zero violations;
  - all catalog, build, and whitespace gates passed.
- Tests not run and why:
  - no live credentialed ZCC tenant/provider import was authorized or available.
    The structurally exact fake-Terraform differential is merge evidence only,
    not live-provider qualification.

## Adversarial Review Remediation

- Review checkpoint:
  `7fbbd94026c88fbe391d6460588cd39c92880b0a`.
- Blocking finding: the fake Terraform fixture parsed provider-state evidence
  with native `JSON.parse` and re-emitted it through native `JSON.stringify`.
  That rounded `auto_purge_days: 900719925474099312345678902` to an exponent
  form before either Python or Node observed the state, so the all-five
  operation differential did not prove its claimed lossless provider-number
  path.
- Root cause: raw pull evidence used the lossless parser, but the independently
  built state map used ordinary JavaScript numbers and object serialization.
- Fix: build provider `values` and `sensitive_values` as pre-rendered lossless
  JSON fragments, retain both valid-ID and malformed-ID fragments, and
  concatenate only the state resource/envelope JSON around those fragments.
  Plan/provider/missing-resource failure scenarios remain independently
  selectable.
- Regression: in both singleton and grouped Python-before/Node/Python-after
  runs, all three tfvars artifacts must contain the exact decimal token
  `900719925474099312345678902` and must not contain an exponent-form
  `auto_purge_days` value.
- Verification: the focused public operation, concrete adapter, oracle core,
  projection differential, and Terraform-show suites passed 101/101 on both
  Node 24.15/Unicode 16 and Node 24.14/Unicode 17; typecheck and whitespace
  checks also passed.

## Known Deferrals

- Live credentialed ZCC import/read for all five resources on the exact pinned
  Terraform/provider build.
- Independent host-derived parity report and downstream cutover qualification.
- Saved-plan/apply validation of returned candidates and any materialization
  policy for provider-observed results.
- REST collection, batching, refresh/moves, HCL, generated bindings, drift
  policy, other Zscaler resources/products, OpenTofu, Terraform/provider
  upgrades, and additional platforms.
- Reason it is safe to defer: the operation returns a read-only candidate and
  documentation/schema explicitly deny parity, clean/apply, live, and cutover
  claims. Python and downstream gates remain authoritative.
- Follow-up owner or trigger: controlled credentialed tenant evidence and the
  separate parity/cutover workstream.

## Review Focus

- Highest-risk files or paths:
  - extracted binding in `node-src/domain/zcc-pull-operation.ts`;
  - public transaction order in `node-src/domain/zcc-adoption-operation.ts`;
  - host authority construction in `node-src/process/main.ts` and snapshot in
    `node-src/process/execute.ts`;
  - adoption schema and reused/custom semantics;
  - fake-Terraform/Python-before/Node/Python-after differential.
- Specific assumptions to attack:
  - `mode: bootstrap` request refinement must not create an ambiguous schema
    union or admit refresh;
  - authority must be copied once and no inherited Terraform configuration may
    reach children;
  - post-oracle source/control/bootstrap recheck must run after cleanup on
    success but never mask a primary error;
  - empty identity sets must not inspect executable/temp-root files or spawn;
  - adoption result must not be accepted under a pull operation or mismatched
    tenant/resource.
- Source evidence the reviewer should verify:
  - exact five resources and adoption catalog/source digests;
  - provider lock/version/plan/state authority inherited from the reviewed
    base;
  - Python `engine.adopt` artifact path and byte behavior in both grouped and
    singleton deployments.
- Generated artifacts the reviewer should compare:
  - new artifact schema against actual result fields;
  - process request/response branches against TypeScript unions and dispatch;
  - production bundle reachability for public ZCC oracle inputs while ZIA/ZPA
    private cohorts remain excluded.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - source/control/import/move/pending mutation during apply;
  - malformed/future plan or state envelopes and wrong provider/state IDs;
  - HCL, grouped same-root bindings, unsupported resources, existing
    transitions, large IDs, Unicode/HTML, fail-open inversion, and
    trusted-network provider display-name lookup;
  - timeout versus protection/cleanup precedence and secret/path leakage;
  - schema-valid but cross-paired request/result documents.
