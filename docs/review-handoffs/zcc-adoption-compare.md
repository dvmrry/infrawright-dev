# Builder Review Handoff: Public ZCC Adoption Artifact Comparison

## Intent

- Add a public, machine-only `compare_adoption_artifacts` operation for the
  exact five-resource ZCC adoption catalog.
- Bind one existing Python-materialized bootstrap reference, run one fresh
  provider-observed Node candidate through the already-hardened adoption
  oracle, and return only logical artifact coordinates and digests.
- Keep `compile_adoption_artifacts` strict: its absent-import/bootstrap gates
  are unchanged. Comparison uses a separate binder because existing imports
  are reference evidence in this lane.
- Return `infrawright.zcc_adoption_artifact_parity` v1 with per-role equality
  and aggregate `ready` or `review_required`. Mismatch is exit 3; I/O, domain,
  provider-contract, timeout, and cleanup failures remain errors.
- Do not run Python in production, materialize, publish, collect REST, plan,
  apply, expose provider state, or broaden to ZIA/ZPA.

## Base / Head

- Base: `d13fdec6e367014fa5312a0d50d908807b55369e` (merged PR #183).
- Head: the checked-out commit on `feature/node-zcc-adoption-compare`; resolve
  it with `git rev-parse HEAD`.
- Diff command:
  `git diff d13fdec6e367014fa5312a0d50d908807b55369e...HEAD`.

## Files Changed

- Public process request/response contract and host dispatch:
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
  - `node-src/process/types.ts`
  - `node-src/process/execute.ts`
  - `node-src/process/main.ts`
- Comparison contract, construction, and semantic validation:
  - `docs/schemas/zcc-adoption-artifact-parity.schema.json`
  - `node-src/domain/zcc-adoption-artifact-parity.ts`
  - `node-src/contracts/zcc-adoption-parity-semantics.ts`
  - `node-src/contracts/zcc-adoption-operation-semantics.ts`
  - `node-src/contracts/validators.ts`
- Comparison-specific binding and provider-observed operation:
  - `node-src/domain/zcc-pull-operation.ts`
  - `node-src/domain/zcc-adoption-operation.ts`
- Tests:
  - `node-tests/zcc-adoption-artifact-semantics.test.ts`
  - `node-tests/zcc-adoption-process.test.ts`
  - `node-tests/zia-transform-cohort.test.ts`
- Documentation:
  - `README.md`
  - `docs/node-process-api.md`
  - this handoff.
- Files intentionally left untouched:
  - ZCC adoption/transform catalog bytes, pack metadata, provider schema,
    provider source evidence, provider lock, and projection semantics;
  - hardened Terraform runner, show parser, oracle core, and concrete adapters;
  - Python adoption/oracle/materialization production code;
  - pull materializers, refresh transitions, publishers, collectors, plans,
    apply, and ZIA/ZPA private cohorts.

## Source Inputs Consulted

- Provider schemas:
  - unchanged `packs/zcc/schemas/provider/zcc.json`, reached through the exact
    committed adoption catalog and existing provider-observed projection.
- OpenAPI/API contracts: N/A; no REST operation or endpoint mapping is added.
- Provider source files:
  - unchanged reviewed `zscaler/zcc` `0.1.0-beta.1` dependency and provider
    evidence inherited from PR #183.
- Pack metadata:
  - `catalogs/zcc-adoption-catalog.v1.json`, SHA-256
    `ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7`.
  - `catalogs/zscaler-root-catalog.v1.json` for exact deployment topology.
  - existing transform catalog only through target/layout resolution; the
    parity result is explicitly bound to the adoption catalog instead.
- Existing docs or design records:
  - `docs/node-process-api.md`.
  - `docs/review-handoffs/zcc-public-adoption-operation.md`.
  - `docs/review-handoffs/zcc-adoption-oracle-host-hardening.md`.
  - `docs/zcc-adoption-oracle-parity-contract.md`.
- Other source evidence:
  - `node-tests/fixtures/zcc-adoption-projection-corpus.v1.json`.
  - `tests/fixtures/parity/zcc_failopen_policy_inversion.json`.
  - actual Python `engine.adopt` materialization in singleton and all-five
    grouped workspaces, used only by tests as the external byte authority.

## Generated Artifacts

- Reports: none.
- Schemas:
  - new `zcc-adoption-artifact-parity.schema.json`.
- Fixtures: none added or changed; existing lossless, source-backed fixtures
  drive the fake-Terraform/Python differential.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: none. Catalog, fixture, demo, pack,
  provider-lock, and Python-produced canonical bytes remain unchanged.

## Expected Delta

- Expected behavior change:
  - v1 process requests may select `compare_adoption_artifacts` with exactly
    `mode: bootstrap`, `reference: materialized`, tenant, and one exact ZCC
    resource;
  - host-only Terraform executable, scratch root, and closed credential/proxy/
    certificate environment authority remain identical to PR #183;
  - materialized tfvars/imports and trusted-network lookup are bound before
    provider execution and rechecked after successful oracle cleanup;
  - missing or unequal applicable references produce a complete digest-only
    review report and exit 3;
  - exact equality produces `ready` and exit 0 without persistent writes.
- Expected report/count/coverage changes:
  - one distinct result kind and process branch;
  - applicable role count is two for non-trusted-network resources and three
    for trusted network; counts derive from per-role status.
- Expected generated-output changes:
  - one new JSON schema and production-bundle reachability marker only.
- Expected no-op areas:
  - compile-adoption bootstrap-absence policy, raw pull comparison, refresh,
    materialization, publication, acknowledgement, assessment, topology,
    Python runtime, catalogs, and every non-ZCC product.

## Invariants Claimed

- Evidence must not be silently dropped:
  - every applicable candidate/reference role has a logical path, size,
    SHA-256, and derived equality status;
  - trusted-network lookup is required/applicable and every other lookup is
    explicitly non-applicable with stale sidecars rejected;
  - aggregate counts/status are semantically recomputed.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or evidence ranking changes.
- Source precedence/provenance must remain explicit:
  - result catalog and sources digests are exact constants;
  - source path/digest/size, root membership, variable, layout, reference
    coordinates, and reference digests all come from bound workspace inputs;
  - the request cannot supply candidate bytes, state, source path, executable,
    environment, timeout, catalog hash, temp root, or output root.
- Ambiguity must stay classified instead of being coerced to success:
  - missing/unequal ordinary references are `review_required`;
  - special files, symlinks, races, malformed provider evidence, unsupported
    layout, HCL, generated bindings, and provider failures remain errors.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  coverage accounting changes.
- Adoption safety invariants:
  - exact-five, bootstrap-only, JSON/no-generated-binding scope;
  - Python is external test/reference authority and is never invoked by the
    production operation;
  - provider state, import IDs, artifact contents, credentials, scratch paths,
    and child diagnostics do not enter the parity result or errors;
  - reference/source/control/required-absence evidence is captured before the
    oracle and rechecked after successful cleanup;
  - comparison never writes, replaces, or removes a caller artifact;
  - `compile_adoption_artifacts` still rejects existing imports/moves/pending
    state and was not weakened to serve comparison.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run test` on Node `v24.15.0`, Unicode `16.0`
  - Node `v24.14.0`, Unicode `17.0` against
    `.node-test/node-tests/*.test.js`
  - focused adoption/process/semantic/bundle suites
  - `make test`
  - physically pruned `empty`, `zpa`, and `zscaler` pack roots with their exact
    committed profiles
  - `python3 -m engine.audit_vendor_boundary`
  - ZCC adoption and transform catalog exact-byte checks
  - exact three-resource ZIA transform-cohort catalog check
  - exact ZPA transform-cohort catalog check
  - `npm run build`
  - `git diff --check`
- Relevant output summary:
  - focused comparison/adoption/semantic/bundle surface: 25/25 passed;
  - each Node runtime: 683 total, 682 passed, one existing platform skip;
  - Python: 1,394 total, 1,393 passed, one opt-in external-source skip;
  - pruned profiles: empty 867/867; ZPA 940 passed plus one opt-in skip;
    Zscaler 1,374 passed plus one opt-in skip;
  - vendor boundary: 187 allowed matches, zero violations;
  - catalog, build, typecheck, and whitespace gates passed.
- Tests not run and why:
  - no live credentialed ZCC tenant/provider import was authorized or
    available. The structurally exact fake and Python-materialized references
    are merge evidence, not live-provider qualification.

## Known Deferrals

- Live exact-provider comparison on a credentialed ZCC tenant for all five
  resources.
- Cryptographic attestation of which external process created the materialized
  reference; v1 binds bytes and coordinates, not writer identity.
- Saved-plan/apply qualification and any assertion-bound materializer for a
  provider-observed candidate.
- REST collection, HCL, generated bindings, refresh/moves, other resources,
  ZIA/ZPA adoption, OpenTofu, or provider/Terraform upgrades.
- Reason it is safe to defer:
  - `ready` is documented and schema-commented as byte equality only; the
    operation is read-only and Python/downstream plan gates remain authoritative.
- Follow-up owner or trigger:
  - controlled live-tenant evidence and the later adoption cutover workflow.

## Review Focus

- Highest-risk files or paths:
  - comparison-specific binder in `node-src/domain/zcc-pull-operation.ts`;
  - transaction ordering in `node-src/domain/zcc-adoption-operation.ts`;
  - new parity constructor/schema/semantic validator;
  - process result/request joins and exit-3 routing;
  - all-five Python-materialized fake-Terraform differential.
- Specific assumptions to attack:
  - existing imports must be accepted only in comparison and must remain
    forbidden in compile-adoption;
  - all reference bytes and absence gates must bind before provider execution
    and recheck only after successful cleanup;
  - missing reference must be review status while special/racy files stay
    errors;
  - candidate/reference digest equality, applicability, counts, and top status
    must be impossible to forge under standalone schema validation;
  - no pull-parity or adoption-candidate result can cross-pair with this process
    operation;
  - no caller values, provider state, import IDs, contents, credentials, or
    physical paths can enter the result or failure output.
- Source evidence the reviewer should verify:
  - exact adoption catalog/source digests and five resources;
  - inherited provider lock/version/plan/state transaction;
  - Python artifact paths and bytes for singleton/grouped layouts;
  - trusted-network-only lookup behavior.
- Generated artifacts the reviewer should compare:
  - new parity schema against actual constructor output and process envelope;
  - bundle metafile/markers for public reachability;
  - no catalog/fixture/demo drift.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - absent, replaced, symlinked, or mutated reference files;
  - pull/control/reference mutation during apply and cleanup;
  - Unicode/HTML identities, fail-open inversion, and unbounded provider integer
    lexemes;
  - grouped roots, HCL/generated-binding refusal, stale/non-applicable lookup,
    moves/pending transition, malformed provider joins, timeout/cleanup
    precedence, and empty identity sets.
