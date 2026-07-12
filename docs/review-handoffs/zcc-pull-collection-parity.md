# Builder Review Handoff: ZCC Pull Collection Stability Window

## Intent

- Add public machine operation `compare_zcc_pull_collection` and standalone
  result `infrawright.zcc_pull_collection_parity` v1.
- Compare the exact five Node pull artifacts against an authoritative
  Python-before/Python-after stability window without executing Python,
  contacting the provider, or accepting caller-supplied conclusions.
- Give protected ADO pipelines a strict raw-collection byte contract before
  downstream compiler/adoption parity work proceeds.
- Preserve every existing collection, compilation, adoption, refresh,
  publication, Python, pack, catalog, and provider behavior.

## Base / Head

- Base: `1ce9e3cf8f91456eb1f0c5691a99a2770b705774`.
- Head: the immutable commit checked out on
  `feature/node-zcc-parity-qualification`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff 1ce9e3cf8f91456eb1f0c5691a99a2770b705774...HEAD`.

## Files Changed

- Comparator and contracts:
  - `node-src/domain/zcc-pull-collection-parity.ts`
  - `node-src/contracts/zcc-pull-collection-parity-semantics.ts`
  - `docs/schemas/zcc-pull-collection-parity.schema.json`
- Public process wiring:
  - `node-src/process/types.ts`
  - `node-src/process/execute.ts`
  - `node-src/process/main.ts`
  - `node-src/contracts/validators.ts`
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
- Reused filesystem primitives:
  - `node-src/io/zcc-pull-publisher.ts` exports its existing internal stable
    directory binder/verifier without changing behavior;
  - `node-src/io/bounded-files.ts` now best-effort clears its scratch and
    intermediate collected byte buffers after reads.
- Documentation and tests:
  - `docs/node-process-api.md`
  - `node-tests/zcc-pull-collection-parity.test.ts`
  - this handoff.
- Files intentionally left untouched:
  - Python collectors, transforms, adoption/oracle code, and pack metadata;
  - collector/adoption/transform/root catalogs and committed fixtures;
  - private OneAPI authentication, transport, child protocol, and publication
    behavior;
  - ADO YAML, transcript capture, runtime archives, HMAC evidence,
    materialization, and cutover aggregation.

## Source Inputs Consulted

- Provider schemas: none newly interpreted.
- OpenAPI/API contracts: none newly extracted. This operation reads already
  materialized pull artifacts only.
- Provider source files: none.
- Pack metadata:
  - the frozen exact-five order from
    `node-src/domain/zcc-collection-contract.ts`;
  - collector source digest
    `d4b8cbef8294e8cb7fd5b17b6efb120b5f8bdc09de8c10e506763547748b11fc`;
  - existing `infrawright.zcc_pull_collection` receipt contract.
- Existing docs or design records:
  - `AGENTS.md`
  - `docs/adversarial-review.md`
  - `docs/adversarial-review-run-prompt.md`
  - `docs/adversarial-review-template.md`
  - `docs/review-handoff-template.md`
  - `docs/node-process-api.md`
  - `docs/review-handoffs/zcc-collection-child.md`
  - `docs/review-handoffs/zcc-collector-kernel.md`
- Other source evidence:
  - Python `engine.collectors.rest.fetch_all`, including its real
    `json.dump(..., indent=2, sort_keys=True) + "\n"` writer;
  - existing duplicate-key-closed lossless pull parser, Python-compatible
    renderer, bounded no-follow reader, and directory binding logic.

## Generated Artifacts

- Reports: new runtime-only `infrawright.zcc_pull_collection_parity` v1;
  none committed.
- Schemas: new strict standalone schema and strict process request/response
  branches with mandatory custom semantic validation.
- Fixtures: none committed; tests create all workspaces and artifacts in
  temporary directories and invoke the actual Python writer for the success
  lane.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Artifact drift intentionally expected: process/schema/docs/test source and
  production parent bundle bytes. No catalog, fixture, demo, Python, pack, or
  Terraform artifact drift is expected.

## Expected Delta

- Expected behavior change:
  - a caller supplies exactly three canonical, absolute, non-root, pairwise
    physically disjoint workspaces; one bounded ASCII tenant; fixed reference
    `python_stability_window`; and five complete Node collection receipts in
    exact frozen order;
  - all 15 derived `pulls/<tenant>/<resource>.json` files must be regular,
    non-symlink, at most 4 MiB, fatal UTF-8, duplicate-key-closed lossless JSON
    lists, at most 50,000 items, and byte-exact Python canonical JSON;
  - the operation binds and rechecks workspace handles plus file
    device/inode/size/mtime/ctime/digest across the complete comparison window;
  - Node file path/hash/size/count/catalog/resource/tenant coordinates must
    join every receipt;
  - Python-before/Python-after tuple inequality yields
    `unstable_reference` regardless of Node; otherwise Node equality yields
    `equal` and inequality yields `different`;
  - all-equal exits `0`; either finding exits `3`; ordinary request/domain
    failures exit `2`; I/O/internal failures exit `1`.
- Expected report/count/coverage changes: the report adds derived exact-five
  counts only. Empty resources can compare equal but create no coverage or
  qualification claim.
- Expected generated-output changes: only the ignored production parent bundle
  changes. The private child bundle remains build-graph isolated.
- Expected no-op areas: provider I/O, Python output bytes, existing receipts,
  compilation/adoption results, persistent artifacts, and all non-ZCC paths.

## Invariants Claimed

- Evidence must not be silently dropped:
  - partial, missing, duplicate, reordered, cross-tenant, cross-resource, or
    foreign-catalog receipt sets fail before workspace I/O;
  - all 15 files are mandatory and each full tuple is retained in the report;
  - receipt/file mismatch is a closed domain failure rather than a parity
    finding.
- Generic matcher evidence must not outrank source-backed evidence: N/A. No
  matcher or provider-source evidence changes.
- Source precedence/provenance must remain explicit: the report and all five
  receipts bind the one frozen collector source digest; callers cannot supply
  another catalog/build/evidence classification.
- Ambiguity must stay classified instead of being coerced to success: reference
  movement takes precedence over Node equality or inequality; malformed bytes,
  mutation, aliasing, and receipt disagreement are failures, not equality.
- Provider-readiness counts must stay explainable: N/A. This result has only
  mechanically derived parity counts and makes no readiness claim.
- Adoption safety invariants:
  - request and result contracts are closed structurally and semantically;
  - the direct library seam rejects proxy, accessor, sparse, non-plain, cyclic,
    extra-key, and unsupported retained graphs instead of normalizing them;
  - arbitrary paths, URLs, credentials, statuses, counts, runtime identities,
    or caller hashes outside validated receipts are not accepted;
  - result/error envelopes contain no absolute paths, workspace names, raw
    bodies, credentials, URLs, or diagnostics;
  - every mutable owned content buffer, including bounded-reader scratch and
    intermediate chunks, is cleared best-effort. JavaScript strings make no
    erasure claim;
  - report digests are content joins, not authentication. Protected ADO
    artifact provenance remains an external requirement.

## Tests Run

- Commands:
  - focused typecheck/build and combined parity/collection suites;
  - full Node 24.15 and 24.14 using
    `node --test --test-concurrency=2 .node-test/node-tests/*.test.js`;
  - `npm run build` production parent/child graph checks;
  - `make check-all`;
  - physically pruned exact `empty`, `zcc`, and `zscaler` copies through
    `make PACK_PROFILE=packsets/<profile>.json check`;
  - explicit collector/adoption/transform/ZPA-cohort/root catalog byte checks;
  - release-script syntax, release-required-file presence, production bundle
    SHA-256 generation, JSON schema parse checks, and `git diff --check`.
- Relevant output summary:
  - focused parity plus existing collection: 27/27 passed before final
    hardening; final parity matrix is 13/13 within each full run;
  - Node 24.15: 794 total, 793 passed, zero failed, one existing platform skip;
  - Node 24.14: 794 total, 793 passed, zero failed, one existing platform skip;
  - full Python: 1,400 total, 1,399 passed, zero failed, one opt-in external
    provider-source skip;
  - physically pruned: empty 867/867; ZCC 911/911; Zscaler 1,381 total,
    1,380 passed, one existing external-source skip;
  - full pack selection, demo drift, generated modules, JSON tfvars, pack
    metadata, and vendor boundary passed; vendor audit retained 187 allowed
    matches and zero violations;
  - catalog checks, production two-bundle build/graph guards, release shell
    syntax/required-file checks, checksums, schemas, and whitespace passed.
- Tests not run and why:
  - no live credentialed tenant collection was authorized in this artifact-only
    slice;
  - no protected ADO job, corporate proxy/private CA, hosted CI, or external
    artifact-provenance system was available locally;
  - no live executor, runtime archive, transcript, HMAC, qualification, or
    cutover claim is made.
- Intermediate corrected gate:
  - the first uncapped wrapper replay exposed one request-diagnostic-count
    regression after adding a separate operation branch (794 total, one fail,
    one skip). The schema dispatch was collapsed without weakening the new
    context/input join; the exact targeted regression passed, and both final
    capped full-runtime replays above passed.
  - the first rsync-only Zscaler-pruned harness omitted `.git`, so its demo test
    could not execute `git status`. The Git-aware physical-pruning replay above
    passed; this was a harness correction, not a product change.

## Known Deferrals

- Deferred work:
  - protected ADO YAML/orchestration for Python-before, Node, Python-after;
  - live tenant, proxy, CA, credential, and provider-churn evidence;
  - transcript capture/replay, Python execution, HMAC, immutable runtime
    archives, report authentication, batch comparison, materialization, broad
    cutover aggregation, and qualification claims.
- Reason it is safe to defer: this operation is read-only, exact-five,
  artifact-bound, closed to a stable Python reference window, and explicitly
  refuses to interpret equality as runtime/executor/cutover qualification.
- Follow-up owner or trigger: downstream protected ADO integration and fresh
  review before any qualification or default-pipeline cutover claim.

## Review Focus

- Highest-risk files or paths:
  - `node-src/domain/zcc-pull-collection-parity.ts`
  - `node-src/contracts/zcc-pull-collection-parity-semantics.ts`
  - the three process/standalone schemas
  - bounded-reader buffer clearing and directory-binder exports.
- Specific assumptions to attack:
  - receipt rejection really precedes filesystem I/O;
  - operation-conditioned process contexts cannot validate against an old
    operation;
  - nested/same/symlink/root workspaces and symlinked artifact ancestors cannot
    alias evidence;
  - mutate, replace, mutate-and-restore, or root rollover cannot survive the
    final identity/digest/metadata CAS;
  - `unstable_reference` always outranks a Node match;
  - direct exported operation inputs cannot use proxies/accessors/extras to
    launder retained status or hash claims.
- Source evidence the reviewer should verify: exact Python writer bytes, frozen
  resource order/source digest, receipt custom semantics, and existing bounded
  reader/parser/renderer behavior.
- Generated artifacts the reviewer should compare: request schema, response
  schema, standalone schema, and the production parent/private-child bundle
  separation.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  empty lists, same-content replacement, in-place restore, receipt tuple
  forgery, duplicate JSON keys, canonical expansion, count/size boundaries,
  request-result cross-pairs, and any wording that turns artifact equality into
  live executor or cutover qualification.
