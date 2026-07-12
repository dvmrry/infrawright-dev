# Builder Review Handoff: Provider-Observed ZCC Adoption Materializer

## Intent

- Add one bootstrap-only machine operation, `materialize_adoption_artifacts`,
  after the provider-observed adoption comparison.
- Accept only the complete protected `ready`/`equal`
  `infrawright.zcc_adoption_artifact_parity` v1 assertion, run a second fresh
  provider oracle in the target workspace, and publish only an exact matching
  candidate.
- Reuse the existing create-or-verify-exact writer mechanics without changing
  their `imports -> applicable lookup -> tfvars` order, retry-forward prefix,
  authority, crash, or cleanup behavior.
- Return a content-free receipt and preserve plan, apply, live-tenant, and
  cutover qualification as separate downstream gates.
- Do not change Python, catalogs, provider projection/oracle semantics, refresh
  transitions, or any non-ZCC product.

## Base / Head

- Base: `45bc7f96a0f7699e48d3d883973a816b13853bf9`.
- Head: the checked-out commit on `feature/node-zcc-adoption-materializer`;
  resolve it with `git rev-parse HEAD`.
- Diff command:
  `git diff 45bc7f96a0f7699e48d3d883973a816b13853bf9...HEAD`.

## Files Changed

- Public request, response, receipt, and semantic contracts:
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
  - `docs/schemas/zcc-adoption-artifact-materialization.schema.json`
  - `node-src/contracts/validators.ts`
  - `node-src/contracts/zcc-adoption-materialization-semantics.ts`
  - `node-src/contracts/zcc-adoption-operation-semantics.ts`
  - `node-src/process/types.ts`
  - `node-src/process/execute.ts`
  - `node-src/process/main.ts`
- Provider-observed operation and publication:
  - `node-src/domain/zcc-adoption-materialization.ts`
  - `node-src/domain/zcc-adoption-operation.ts`
  - `node-src/domain/zcc-pull-operation.ts`
  - `node-src/domain/zcc-pull-materialization.ts`
- Tests:
  - `node-tests/zcc-adoption-materialization-semantics.test.ts`
  - `node-tests/zcc-adoption-materializer.test.ts`
  - `node-tests/zcc-adoption-process.test.ts`
  - `node-tests/zia-transform-cohort.test.ts`
- Documentation:
  - `README.md`
  - `docs/node-process-api.md`
  - `docs/adr/0001-publisher-ownership.md`
  - `docs/integration-validation.md`
  - this handoff.
- Files intentionally left untouched:
  - Python engine/adoption/materialization and Make behavior;
  - ZCC adoption/transform catalogs, provider schema/source evidence, and
    dependency lock;
  - adoption projection, oracle core, Terraform runner, concrete adapters, and
    provider-lock implementation;
  - refresh publisher, pending marker, acknowledgement, plan/apply, packs,
    fixtures, snapshots, and golden outputs.

## Source Inputs Consulted

- Provider schemas:
  - unchanged `packs/zcc/schemas/provider/zcc.json`, reached only through the
    existing exact adoption catalog and provider-observed projection.
- OpenAPI/API contracts: N/A; no REST extraction, endpoint, or source-operation
  mapping changes.
- Provider source files: unchanged reviewed ZCC `0.1.0-beta.1` provider source
  evidence inherited through the existing oracle/catalog contract.
- Pack metadata:
  - unchanged `catalogs/zcc-adoption-catalog.v1.json`;
  - unchanged `catalogs/zscaler-root-catalog.v1.json`;
  - unchanged transform catalog only for target/layout resolution.
- Existing docs or design records:
  - `AGENTS.md`
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
  - `docs/node-process-api.md`
  - `docs/adr/0001-publisher-ownership.md`
  - prior bootstrap/refresh publisher and adoption comparison handoffs.
- Other source evidence:
  - existing `engine.adopt` Python writer invoked only from tests;
  - `node-tests/fixtures/zcc-adoption-projection-corpus.v1.json` and
    `tests/fixtures/parity/zcc_failopen_policy_inversion.json` through the
    existing fake-provider differential;
  - existing bootstrap materializer crash, race, and authority tests retained
    as regression coverage for the shared writer kernel.

## Generated Artifacts

- Reports: new content-free
  `infrawright.zcc_adoption_artifact_materialization` v1 receipt.
- Schemas: new
  `docs/schemas/zcc-adoption-artifact-materialization.schema.json`; process
  request/response unions are extended.
- Fixtures: none added or changed.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: none outside a successful explicitly
  asserted process invocation.

## Expected Delta

- Expected behavior change:
  - one complete ready adoption parity assertion can authorize a distinct
    bootstrap create-or-verify-exact publication;
  - both adoption-oracle host authority and exact output-root authority are
    required before workspace I/O;
  - the publisher guard covers the complete binder, fresh oracle, and write;
  - the target source, deployment, catalog, provider observation, adoption
    catalog/root/layout, and artifact digests must reproduce the assertion;
  - missing files are created in fixed prefix order and exact prefix/full
    retries are reused; no existing target is replaced.
- Expected report/count/coverage changes:
  - one result kind and process branch;
  - applicable role partition is two for ordinary resources and three only for
    `zcc_trusted_network`;
  - all five resources are differentially covered in singleton and all-five
    grouped roots with Python-before/Node/Python-after bytes.
- Expected generated-output changes: one schema and production-bundle protocol
  markers only; no committed config/import output changes.
- Expected no-op areas: existing pull materialization behavior and result,
  compare/compile output, refresh, acknowledgement, Python, catalogs, plan,
  apply, and non-ZCC operations.

## Invariants Claimed

- Evidence must not be silently dropped:
  - the complete ready parity assertion is retained in the receipt as fresh
    verification;
  - result semantics recompute the tenant/resource join and the sorted,
    disjoint, complete created/reused partition;
  - nested adoption parity semantics continue to enforce source/catalog/root,
    coordinate, applicability, count, and equality joins.
- Generic matcher evidence must not outrank source-backed evidence: N/A; the
  existing exact provider-observed projection and Python reference remain the
  only authorities.
- Source precedence/provenance must remain explicit:
  - request supplies only tenant/resource and the protected assertion;
  - source, controls, target, adoption catalog, provider state, executable,
    environment, scratch, timeout, and output root remain derived or host-owned;
  - the operation reruns provider observation and exactly reproduces the full
    assertion instead of trusting caller bytes or individual digests.
- Ambiguity must stay classified instead of being coerced to success:
  - non-ready/cross-bound assertions, fresh mismatch, non-prefix targets,
    foreign bytes, symlinks/special files, wrong authority, moves/pending moves,
    HCL, generated bindings, stale lookup, provider failures, and races fail
    closed without replacement;
  - materialization never maps a difference to exit 3 or partial success.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  provider coverage accounting changes.
- Adoption safety invariants:
  - exact-five, JSON, no-generated-binding, bootstrap-only scope;
  - provider oracle and assertion values are descriptor-safe inert snapshots;
  - the output-root guard is acquired before binder/oracle reads;
  - fixed publication order is imports, applicable lookup, tfvars;
  - each leaf is no-overwrite atomic while the set remains retry-forward;
  - post-link failure is retryable indeterminate and never rolls back by path;
  - result/error output contains no contents, provider state, import IDs,
    credentials, child diagnostics, scratch/staging names, or output-root field;
  - receipt is not plan/apply/live/cutover evidence.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run build:test`
  - focused adoption materializer/process/semantic, legacy pull materializer,
    and production-bundle suites
  - `node --test --test-concurrency=2 .node-test/node-tests/*.test.js` on
    Node `v24.15.0`, Unicode `16.0`
  - bundled Node `v24.14.0`, Unicode `17.0`, with the same complete compiled
    suite and concurrency cap
  - `make test`
  - physically pruned `empty`, `zpa`, and `zscaler` checkouts with
    `make PACK_PROFILE=packsets/<profile>.json check`
  - `python3 -m engine.audit_vendor_boundary`
  - exact-byte checks for the ZCC collector, adoption, and transform catalogs,
    the exact three-resource ZIA transform cohort, the exact ZPA transform
    cohort, and the Zscaler root catalog
  - `npm run build`
  - `git diff --check`
- Relevant output summary:
  - focused materializer/process/semantic and compatibility surface: 88/88
    passed;
  - each Node runtime: 717 total, 716 passed, one existing platform skip, zero
    failed;
  - Python: 1,400 total, 1,399 passed, one opt-in external-source skip;
  - pruned profiles: empty 867/867; ZPA 940 passed plus one opt-in skip;
    Zscaler 1,380 passed plus one opt-in skip;
  - vendor boundary: 187 allowed matches, zero violations;
  - all six catalog byte gates, typecheck, build-test, production build, and
    whitespace gates passed.
- Tests not run and why: live provider/tenant, ADO, and remote-backend plan/apply
  are outside this credential-free repository slice and remain downstream
  evidence.

## Known Deferrals

- Deferred work: live-tenant provider parity and saved-plan cleanliness.
- Reason it is safe to defer: the receipt explicitly claims only fresh bytes and
  durable publication; the documented workflow still requires environment
  generation, staging, saved plan, and `assert-adoptable`.
- Follow-up owner or trigger: controlled integration-validation lane after this
  change passes fresh-context adversarial review.
- Deferred work: provider-observed replacement/refresh and moves.
- Reason it is safe to defer: v1 never replaces and refuses all move/pending
  residue; the raw-transform refresh assertion is not reused as provider-
  observed authority.
- Follow-up owner or trigger: separately designed provider-observed refresh
  assertion and transition protocol.
- Deferred work: automatic stale-guard/staging cleanup after abrupt death.
- Reason it is safe to defer: the host never guesses ownership or consumes
  random aliases; operator proof or disposal of the complete job root is
  required before retry.
- Follow-up owner or trigger: deployment/runbook work, not this protocol slice.

## Review Focus

- Highest-risk files or paths:
  - `node-src/domain/zcc-adoption-materialization.ts`
  - the shared-kernel split in `node-src/domain/zcc-pull-materialization.ts`
  - candidate-only binding in `node-src/domain/zcc-pull-operation.ts`
  - `node-src/domain/zcc-adoption-operation.ts`
  - process request/result schemas and custom semantics
  - `node-src/process/execute.ts` guard scope.
- Specific assumptions to attack:
  - both host authorities are snapshotted and guard acquisition precedes every
    workspace/oracle read;
  - the compile and compare binders were not weakened;
  - candidate-only binding omits only target-as-evidence checks while source and
    controls remain CAS-bound through publication;
  - the full assertion, including source/catalog/root/layout, is reproduced;
  - retained/cyclic/proxy/accessor inputs cannot run hooks or leak values;
  - pull materialization ordering, codes, cleanup, and result remain unchanged;
  - exact existing targets must be a valid imports/lookup/tfvars prefix;
  - exit 0/1/2 behavior cannot accidentally produce review exit 3.
- Source evidence the reviewer should verify:
  - existing adoption comparison constructor and semantic validator;
  - existing oracle host hardening and cleanup boundary;
  - existing publisher guard and bootstrap materializer tests;
  - actual Python-before/after bytes used by the public process differential.
- Generated artifacts the reviewer should compare:
  - new standalone receipt schema against the TypeScript result and every
    custom semantic rule;
  - process request/response branches against runtime dispatch and error
    identity;
  - production bundle reachability without catalog/fixture drift.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - a ready assertion from a different source/control/root/provider read;
  - same bytes under different logical coordinates or adoption catalog;
  - non-applicable versus trusted-network lookup;
  - an exact full set versus exact prefix versus non-prefix/foreign state;
  - crash after link, after final link, or during postpublication input recheck;
  - wrong/ancestor output authority and active/stale guard before provider use;
  - absolute overlay coordinates in embedded parity versus forbidden new
    physical scratch/output fields;
  - empty pulls that skip Terraform but still require assertion and write
    authority;
  - receipt validation that accidentally skips nested parity semantics.
