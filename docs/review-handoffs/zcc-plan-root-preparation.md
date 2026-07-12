# Builder Review Handoff: ZCC Plan-Root Preparation Compiler

## Intent

- Add public machine operation `compile_plan_root_preparation` and standalone
  result `infrawright.zcc_plan_root_preparation_candidate` v1.
- Compile one freshly derived exact-five ZCC whole-root candidate from a sorted,
  complete set of provider-observed adoption materialization receipts without
  writing the workspace or executing Python or Terraform at runtime.
- Emit the exact Terraform-1.15.4-formatted `engine.gen_env` `main.tf` bytes,
  staged canonical import bytes, observed module-tree fingerprints, control
  digests, topology, backend-marker intent, and the exact absent-sidecar set.
- Keep the result explicitly candidate-only: it does not qualify readiness,
  validation, plan, apply, refresh, publication, or cutover.
- Preserve Python generation/staging, existing materializers, catalogs, pack
  metadata, provider behavior, and all existing process operations.

## Base / Head

- Base: `0ad438bfd84b6f73d4f46d275947fe4f48750e23`.
- Head: the immutable commit checked out on
  `feature/node-zcc-plan-root-compiler`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff 0ad438bfd84b6f73d4f46d275947fe4f48750e23...HEAD`.

## Files Changed

- Files:
  - `node-src/domain/zcc-plan-root-preparation-contract.ts`
  - `node-src/domain/zcc-plan-root-preparation.ts`
  - `node-src/contracts/zcc-plan-root-preparation-semantics.ts`
  - `node-src/json/python-compatible.ts`
  - `node-src/json/supported-json-graph.ts`
  - `docs/schemas/zcc-plan-root-preparation.schema.json`
  - `node-src/contracts/validators.ts`
  - `node-src/process/types.ts`
  - `node-src/process/execute.ts`
  - `node-src/process/main.ts`
  - `node-src/process/limits.ts`
  - `node-src/process/response-emission.ts`
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
  - `docs/node-process-api.md`
  - `node-tests/zcc-plan-root-preparation.test.ts`
  - `node-tests/json.test.ts`
  - `docs/review-handoffs/zcc-plan-root-preparation.md`
- Files intentionally left untouched:
  - Python `engine.gen_env`, `engine.ops`, deployment, and catalog producers;
  - adoption compilation/comparison/materialization and publisher code;
  - the Terraform command adapter: runtime Terraform execution is not part of
    this operation;
  - pack metadata, committed catalogs, fixtures, snapshots, demo outputs, CI,
    build scripts, and release scripts.

## Source Inputs Consulted

- Provider schemas: no provider schema was changed or newly interpreted. The
  retained receipts already bind provider-observed adoption output.
- OpenAPI/API contracts: none; this change does not map provider operations or
  make API calls.
- Provider source files: none; authenticated provider provenance remains
  outside this candidate compiler.
- Pack metadata: the unchanged ZCC registry/schema and exact-five adoption
  catalog; the unchanged all-Zscaler root catalog supplies whole-root topology.
- Existing docs or design records:
  - `docs/node-process-api.md`;
  - `docs/adversarial-review.md` and both review templates/prompts;
  - existing adoption materialization, root topology, plan fingerprint, path,
    control-evidence, and bounded-reader contracts.
- Other source evidence:
  - `engine/gen_env.py` canonical root rendering and backend behavior;
  - `engine/ops.py` non-state-aware `stage-imports` behavior;
  - live Terraform 1.15.4 formatting differentials, which showed the retained
    no-bindings renderer delta is exactly the formatted `items  =` alignment;
  - real Python `engine.gen_env` plus `engine.ops stage-imports` differentials
    for singleton/grouped roots, local/azurerm backends, default/explicit module
    directories, and paths containing spaces.

## Generated Artifacts

- Reports: the operation returns one content-bearing candidate; no report file
  is written or committed.
- Schemas: new strict standalone candidate schema plus request/response process
  registration and custom semantic joins.
- Fixtures: none committed; tests create private temporary workspaces.
- Snapshots: none.
- Demo or lab outputs: none committed.
- Artifact drift intentionally expected: the production process bundle changes
  when built because the new public operation and schema are reachable. `dist/`
  remains ignored and no generated bundle is committed.

## Expected Delta

- Expected behavior change:
  - one exact resource selector expands to one complete configured root of one
    to five exact-five members;
  - the request must contain sorted, unique materialization receipts exactly
    covering that whole root;
  - the compiler returns exact `main.tf` and staged-import content, observed
    module bindings, source/control joins, backend-marker state/intent, and
    absence evidence without writes;
  - any HCL tfvars mode, binding sidecar, move sidecar, stale lookup, receipt
    disagreement, absolute overlay, unsupported topology/member, unsafe path, module/control/
    artifact mutation, or noncanonical import content fails closed.
- Expected report/count/coverage changes: one selected root; member, module, and
  staged-import counts are equal and mechanically derived in the range 1..5.
  No provider-readiness or coverage percentage is introduced.
- Expected generated-output changes: only the ignored production bundle. The
  new candidate schema is a reviewed source contract, not generated drift.
- Expected no-op areas: Python output, existing materialized artifacts,
  provider state, catalogs, packs, fixtures, plan/apply behavior, and accepted
  shapes for the 14 pre-existing process operations remain unchanged.
- Process-schema architecture note: the redundant top-level `input.oneOf` was
  removed so input is constrained exactly once by the existing
  operation-discriminated `oneOf`. The adoption/root-preparation discriminator
  uses two `if`/`then` branches to avoid amplifying malformed-request errors.
  Review should verify that this changes diagnostics only as intended and does
  not widen any complete process request.

## Review Remediation

- Fresh Sol review of `0ad438b...fa86480` requested changes for three contract
  gaps. This working-tree patch closes all three:
  - candidate semantics now bind the common deployment overlay/tenant,
    `root.env_dir`, marker path, and fixed HCL media types; exact forged
    topology and media-type regressions fail standalone and process-response
    validation;
  - staged HCL is capped at 8 MiB per file/16 MiB aggregate, the complete
    escaped candidate at 24 MiB, and the complete response at 32 MiB using an
    exact non-rendering byte counter; oversize transport errors retain request
    identity;
  - the direct receipt snapshot now preflights one-to-five entries and enforces
    depth/node/property/string-byte ceilings before filesystem access.
- The first remediation re-review found two remaining snapshot accounting
  bypasses plus one nullable-topology semantic bypass. The follow-up patch:
  - charges `LosslessNumber` token bytes before regex/reconstruction and
    preflights wide ordinary records before descriptor/child collection;
  - rejects candidate topology with null tenant directories instead of
    returning early from the custom semantic keyword;
  - adds direct numeric-token, wide-record, standalone-null-topology, and
    process-response-null-topology regressions.
- Fresh final Sol review of `c5c697e...ae4681b` returned **Approve with nits**
  and the independent serialization/snapshot specialist returned **Approve**.
  Their direct probes confirmed cumulative numeric-token accounting and
  pre-descriptor wide-array/record refusal. A requested Fable xHigh rerun of
  the same patch returned only its local session-limit message, so no Fable
  verdict is claimed for the remediation range.
- Fable xHigh approved the original range with nits. Its actionable diagnostic
  nit is also fixed: a present local backend marker now reports
  `PLAN_ROOT_BACKEND_MARKER_MISMATCH`, not the unrelated sidecar refusal. The
  misleading `snapshotReceipts` name is now
  `validateSnapshottedReceipts`.
- The Terraform 1.15.4 differential still skips visibly when that exact binary
  is unavailable. Pinning a hosted CI lane remains follow-up infrastructure;
  frozen byte fixtures and the locally executed exact-version differential
  remain the in-slice gates.

## Invariants Claimed

- Evidence must not be silently dropped: every configured whole-root member
  must have exactly one retained receipt, source binding, module binding, and
  staged import; result semantics and the process result join bind all retained
  receipt fields back to the request.
- Generic matcher evidence must not outrank source-backed evidence: N/A. This
  operation performs no generic/provider matcher inference.
- Source precedence/provenance must remain explicit: provider-observed source
  and adoption-catalog identities come from retained receipts. Module contents
  are labeled `python_fingerprint_v2_observed` and
  `observed_unqualified`, not authenticated provenance.
- Ambiguity must stay classified instead of being coerced to success: partial
  roots, aliases/products, receipt reordering/duplication, alternate formats,
  bindings, moves, stale sidecars, backend disagreement, and changed inputs are
  refusals, never candidate success.
- Provider-readiness counts must stay explainable: N/A. Candidate counts are
  exact structural counts and all qualification fields deny readiness.
- Adoption safety invariants:
  - request options, receipts, and hooks are snapshotted as inert exact graphs
    before filesystem I/O; proxies, accessors, extras, sparse/cyclic graphs,
    and unsupported primitives fail closed;
  - the direct receipt graph is rejected before cloning unless it has one to
    five entries, then is bounded to depth 16, 512 nodes/properties, and 1 MiB
    of aggregate UTF-8 strings;
  - source receipts must be existing aggregate-validated materialization
    receipts, sorted and complete for the freshly expanded root;
  - controls, artifacts, backend marker, sidecar absences, module roots/files,
    and relevant parent authorities are version-bound and rechecked, followed
    by one final synchronous no-yield checkpoint;
  - module-source strings reject HCL/template-sensitive characters while
    preserving Python-compatible same-directory `./.` behavior;
  - success intentionally contains candidate HCL/import contents; errors and
    diagnostics do not echo those contents, absolute paths, credentials, URLs,
    or raw provider data;
  - emitted HCL media types, tenant-directory topology, and serialized
    candidate/response byte ceilings are semantic contract checks, not caller
    conventions;
  - the operation performs no write, provider call, Terraform subprocess,
    Python subprocess, validation, plan, apply, or publication.

## Tests Run

- Commands:
  - `npm run build:test` and focused Node 24.15 execution of
    `.node-test/node-tests/json.test.js` plus
    `.node-test/node-tests/zcc-plan-root-preparation.test.js` with
    `--test-concurrency=2`;
  - adjacent process, materialization semantics, fingerprint, import
    differential, roots, deployment, and catalog suites on Node 24.15 with
    concurrency 2;
  - the changed process/preparation surface on Node 24.14 with concurrency 2;
  - one capped full Node 24.15 replay;
  - `python3 -m unittest -v tests.test_gen_env
    tests.test_ops.OpsStageImportsTest tests.test_ops.OpsGroupedRootCommandTest
    tests.test_deployment tests.test_adoption_catalog`;
  - physically pruned `empty` and `zscaler` temporary snapshots matching CI's
    pack selection, including the complete applicable `make check` targets;
  - exact-byte ZCC collector, adoption, transform, ZIA cohort, ZPA cohort, and
    all-Zscaler root catalog checks;
  - `make check-pack check-pack-set`, `npm run build`, JSON parse of every
    published schema, `bash -n scripts/release.sh`, and `git diff --check`.
- Relevant output summary:
  - focused remediation surface on Node 24.15: 34/34 passed, including four
    live Python/Terraform-1.15.4 differentials;
  - adjacent Node 24.15: 98 total, 97 passed, zero failed, one existing
    Linux-only skip;
  - changed Node 24.14 surface: 66/66 passed;
  - targeted Python: 117/117 passed;
  - physically pruned empty: 867/867 passed;
  - physically pruned Zscaler: 1,381 selected, 1,380 passed, zero failed, one
    opt-in external-provider-source skip; its pack-set, demo, module, tfvars,
    pack, and vendor-boundary remainder exited 0;
  - all six catalog byte gates, pack gates, typecheck/test build, production
    build, schema parse, release syntax, and whitespace checks passed.
- Tests not run and why:
  - the single capped full Node 24.15 replay did run and exited naturally, and
    no failure appeared in captured output, but the tool truncated the terminal
    summary and lost the exit code. It is therefore **inconclusive**, is not
    claimed green, and was not repeated per the requested one-replay cap;
  - full Node 24.14 was not run because the exact changed surface passed there;
  - the remaining non-Zscaler Python pack tests were not run because no Python,
    other-pack, or catalog source changed; focused authoritative Python and both
    physical-pruning profiles cover this slice;
  - the release script itself was not executed because it can create and push a
    tag. Only its syntax and the production bundle/package path were checked,
    consistent with the no-push instruction;
  - no live tenant, credentials, provider, remote backend, protected ADO job,
    plan, apply, or publication was authorized or claimed.

## Known Deferrals

- Deferred work:
  - authenticated module provenance and immutable release/runtime provenance;
  - a future publisher/materializer and its create/replace/crash protocol;
  - reconciliation or retirement of arbitrary pre-existing nonmember root-level
    Terraform files when publishing a changed topology;
  - README/smoke-test generation, expression-binding roots, HCL tfvars, moves,
    state-aware imports, validation, plan, apply, refresh, and qualification;
  - future renderer profiles if Python or Terraform formatting bytes change.
- Reason it is safe to defer: the operation is read-only, exact-profile,
  exact-root, receipt-bound, content-bearing, and labels every downstream
  qualification as not performed/not qualified. It emits a candidate only and
  cannot mutate or publish it.
- Follow-up owner or trigger: downstream protected ADO/publication work, any
  Python renderer or Terraform formatter change, module provenance work, or
  any proposal to interpret this candidate as ready for plan/cutover requires a
  new contract and fresh adversarial review.
- Concurrency boundary: the final no-yield checkpoint closes same-event-loop
  callback gaps but is not an atomic snapshot against another process or worker
  thread. The job must keep the workspace quiescent until the operation exits.
  Unvisited/ignored descendant namespace provenance is not claimed.

## Review Focus

- Highest-risk files or paths:
  - `node-src/domain/zcc-plan-root-preparation.ts`;
  - `node-src/domain/zcc-plan-root-preparation-contract.ts`;
  - `node-src/contracts/zcc-plan-root-preparation-semantics.ts`;
  - the standalone/process schemas and validator registration;
  - process execution/result binding and public error redaction.
- Specific assumptions to attack:
  - malformed or hostile direct callers cannot trigger I/O before exact receipt
    validation and cannot exploit proxies/accessors or later mutation;
  - one selector really expands to one whole root and receipts cannot omit,
    duplicate, reorder, relabel, or add members;
  - the process request schema refactor does not widen any existing operation;
  - local/azurerm marker rules, exact absent sidecars, and receipt artifact paths
    match Python for singleton and grouped roots;
  - control/artifact/module/absence authority replacements and mutate-restore
    attempts cannot survive the final rechecks;
  - returned contents cannot leak through failure envelopes or diagnostics.
- Source evidence the reviewer should verify:
  - `engine/gen_env.py` root bytes, variable names, module source derivation,
    backend comments/key, and exact Terraform-1.15.4 formatting;
  - `engine/ops.py` non-state-aware staging destinations and bytes;
  - exact-five adoption materialization receipt semantics and root catalog;
  - plan-fingerprint v2 module tree semantics and the narrower provenance label.
- Generated artifacts the reviewer should compare:
  - standalone and process request/response schemas plus custom semantics;
  - production bundle reachability/graph guard;
  - singleton/group/local/azurerm/default-explicit-module/space-path Python and
    Terraform differential outputs.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - same-directory `./.`, spaces, quotes/backslashes/control characters,
    `${`/`%{`, explicitly refused absolute/external overlays, symlinked or replaced authorities,
    32 MiB artifact boundaries, empty/oversized modules, stale lookups, any of
    the seven-per-member sidecars, partial grouped roots, forged receipt source
    echoes, coherent result relabeling, malformed imports, and mutation after
    asynchronous rechecks but before return.
