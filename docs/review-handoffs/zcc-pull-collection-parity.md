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

## Adversarial Review Remediation

- The first adversarial review inspected immutable head
  `a5a71684e01dfffeb18108fc9e2c3a69837071b9` and requested changes.
- This follow-up head addresses all four blocking groups from that review:
  - bind and full-version-check all nine workspace/`pulls`/tenant directories,
    and reject physical identity reuse across those directories and 15 files;
  - add one common final synchronous checkpoint after every asynchronous
    artifact reread;
  - make bounded-reader byte ownership and abnormal-exit clearing explicit;
  - exercise `unstable_reference` exit `3` and missing-input exit `1` in the
    public process matrix.
- This builder handoff does not self-approve the remediation. The immutable
  follow-up head requires a fresh read-only adversarial review.

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
  - `node-src/io/bounded-files.ts` now retains collected-chunk ownership until
    transfer, clears untransferred chunks on abnormal exits, and clears the
    byte snapshot owned by the UTF-8 helper after decoding.
- Documentation and tests:
  - `docs/node-process-api.md`
  - `node-tests/bounded-files.test.ts`
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
  - the operation binds open handles and full
    device/inode/size/mtime/ctime versions for the workspace, `pulls`, and
    tenant directories in all three roles, then globally rejects reused
    physical identities across all nine directories and 15 files;
  - after all asynchronous content rechecks, one final synchronous no-yield
    checkpoint rechecks all nine directory handle/path/canonical/version
    bindings and all 15 file path/canonical/version bindings before synchronous
    result construction;
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
  - the bounded reader clears scratch and untransferred collected chunks on
    abnormal exits. The UTF-8 helper clears its internally owned byte snapshot;
    the raw-byte helper intentionally transfers ownership to its caller.
    JavaScript strings make no erasure claim;
  - report digests are content joins, not authentication. Protected ADO
    artifact provenance remains an external requirement.

## Tests Run

- Commands:
  - `npm run typecheck` and `npm run build:test`;
  - focused bounded-reader, collection parity, and pull-publication suites on
    Node 24.15.0 and Node 24.14.0;
  - full Node 24.15.0 using
    `node --test --test-concurrency=2 .node-test/node-tests/*.test.js`;
  - `npm run build` production parent/child graph checks;
  - JSON parse every published schema, `bash -n scripts/release.sh`, and
    `git diff --check`.
- Relevant output summary:
  - focused Node 24.15.0: 44/44 passed;
  - focused Node 24.14.0: 44/44 passed;
  - full Node 24.15.0: 799 total, 798 passed, zero failed, one existing
    platform skip;
  - typecheck, test build, production two-bundle build/graph guards, release
    shell syntax, schema JSON parsing, and whitespace passed.
- Tests not run and why:
  - full Node 24.14 was not repeated because the changed shared behavior passed
    the exact 44-test focused set on that runtime; the requested full replay was
    reserved for focused failure or runtime divergence;
  - full Python and physically pruned pack-profile suites were not repeated
    because the remediation changes only Node comparator/reader code, Node
    tests, and documentation; no Python, pack, catalog, schema, or fixture file
    changed;
  - no live credentialed tenant collection was authorized in this artifact-only
    slice;
  - no protected ADO job, corporate proxy/private CA, hosted CI, or external
    artifact-provenance system was available locally;
  - no live executor, runtime archive, transcript, HMAC, qualification, or
    cutover claim is made.

## Known Deferrals

- Deferred work:
  - protected ADO YAML/orchestration for Python-before, Node, Python-after;
  - live tenant, proxy, CA, credential, and provider-churn evidence;
  - transcript capture/replay, Python execution, HMAC, immutable runtime
    archives, report authentication, batch comparison, materialization, broad
    cutover aggregation, and qualification claims.
- The final no-yield checkpoint closes same-event-loop callback scheduling
  gaps but is not an atomic snapshot against any concurrent writer, including
  another process or worker thread. Protected ADO must keep all three private
  workspaces quiescent until the comparison exits.
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
  - cross-role hardlinks and reused descendant-directory identities cannot
    alias evidence;
  - mutate, replace, move-and-restore at workspace/`pulls`/tenant level, root
    rollover, or mutation of an early-rechecked artifact during a later reread
    cannot survive the final identity/version CAS;
  - collected chunks are cleared when a read fails after collection, while the
    UTF-8 helper clears its transferred byte snapshot on invalid decode;
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
