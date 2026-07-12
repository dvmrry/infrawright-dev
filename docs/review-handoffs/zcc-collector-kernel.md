# Builder Review Handoff: Private ZCC OneAPI Collector Kernel

## Intent

- Add a private, pure Node foundation for collecting only the exact five ZCC
  resources already supported by the adoption catalog.
- Freeze their source-operation mapping to committed ZCC pack metadata and the
  current Python OneAPI collector seams.
- Accept only an injected, already-authenticated data transport and injected
  retry clock. Return an immutable in-memory canonical JSON string plus
  bounded metadata; perform no network, credential, environment, filesystem,
  process, or publication effects.
- Match Python `json.dump(..., indent=2, sort_keys=True) + "\n"` bytes without
  truncating large numeric tokens, decoding HTML entities, or changing
  Unicode.
- Keep the public process request/response schemas, dispatcher, main host,
  existing Python collector, and production bundle behavior unchanged.

## Base / Head

- Base: `d13fdec6e367014fa5312a0d50d908807b55369e` (merged PR #183).
- Head: the immutable commit checked out on
  `feature/node-zcc-collector-kernel`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff d13fdec6e367014fa5312a0d50d908807b55369e...HEAD`.

## Files Changed

- Source-bound private catalog:
  - `catalogs/zcc-collector-catalog.v1.json`
  - `tools/zcc_collector_catalog.py`
  - `node-src/domain/zcc-collector-catalog.ts`
- Pure private collector kernel:
  - `node-src/domain/zcc-collector.ts`
- Regressions and differentials:
  - `node-tests/zcc-collector.test.ts`
  - `tests/test_zcc_collector_catalog.py`
  - `tests/pack-test-requirements.json`
- Documentation:
  - this handoff.
- Files intentionally left untouched:
  - process request/response schemas, unions, dispatcher, and main host;
  - real HTTP/fetch/Undici implementation and credential/environment loading;
  - Python collector behavior and pack registry/metadata;
  - pull/adoption materialization, refresh, parity, and publication;
  - ZIA, ZPA, ZTC, and every non-ZCC resource;
  - production bundle entry points.

## Source Inputs Consulted

- Provider schemas: no provider schema was changed. The five-resource boundary
  is the same boundary already frozen by the ZCC adoption catalog.
- OpenAPI/API contracts: no new external OpenAPI input. Exact request mapping
  comes from committed pack/Python collector authority listed below.
- Provider source files:
  - `packs/zcc/collector.py`
  - `packs/_shared/zscaler/collector.py`
  - `engine/collectors/rest/__init__.py`
- Pack metadata:
  - `packs/zcc/pack.json`
  - `packs/zcc/registry.json`
  - exact provider `zscaler/zcc` version `0.1.0-beta.1`;
  - exact five fetch-backed resources and no sixth fetch mapping.
- Existing docs or design records:
  - `docs/review-handoffs/zcc-public-adoption-operation.md`
  - `docs/review-handoffs/zcc-adoption-oracle-host-hardening.md`
  - `docs/adversarial-review.md`
- Other source evidence:
  - `tests/fixtures/demo/zcc_device_cleanup.json`
  - `tests/fixtures/demo/zcc_failopen_policy.json`
  - `tests/fixtures/demo/zcc_forwarding_profile.json`
  - `tests/fixtures/demo/zcc_trusted_network.json`
  - `tests/fixtures/demo/zcc_web_privacy.json`
  - existing lossless parser, Python-compatible renderer, graph validator, and
    pull-complexity guard in `node-src/json/`.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none added; existing five demo pulls are used directly.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Catalog:
  - new `infrawright.zcc_collector_catalog` v1;
  - generated source digest
    `d4b8cbef8294e8cb7fd5b17b6efb120b5f8bdc09de8c10e506763547748b11fc`;
  - committed catalog byte SHA-256
    `e2e169b5a83dbc240de7b218914332d5f7f3241417e63a8d1663430a2a81f90b`.
- Artifact drift intentionally expected: only the new private catalog and
  handoff. No existing catalog, schema, fixture, demo, or generated output
  changes.

## Expected Delta

- Expected behavior change:
  - private Node callers can derive exact production/non-production OneAPI
    data and token URLs with fixed audience `https://api.zscaler.com`;
  - private Node callers can collect one exact ZCC resource through an
    injected authenticated transport and injected retry clock;
  - paged endpoints request `pageSize=1000`, stop on a short page, accept at
    most 50,000 object items, and make at most 51 logical page requests;
  - only HTTP 429 retries, five times after the first attempt, using numeric
    `Retry-After` capped at 30 seconds or 1/2/4/8/16-second fallback waits;
  - singleton endpoints accept either one object or a list of objects;
  - trusted networks require the exact `trustedNetworkContracts` list
    envelope;
  - output is an immutable canonical JSON string and immutable metadata only.
- Expected report/count/coverage changes: none. The private metadata reports
  logical data requests and actual transport attempts but creates no readiness
  or cutover claim.
- Expected generated-output changes: one new source-bound private catalog.
- Expected no-op areas: public process API/bundle, Python collection,
  Terraform/adoption oracle, materializers, refresh, parity, publishers, and
  every other provider/product.

## Invariants Claimed

- Evidence must not be silently dropped: every returned item must be an
  object; scalar items, malformed pages, wrong/missing envelopes, duplicate
  JSON keys, non-finite number spellings, invalid UTF-8, and structural/size
  overflow fail closed.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or evidence ranking changes.
- Source precedence/provenance must remain explicit: runtime validation pins
  each resource's exact path, method, pagination, page size, envelope,
  provider source/version, source-file list, and source digest. The authoring
  tool reads this checkout directly and ignores `INFRAWRIGHT_PACKS`.
- Ambiguity must stay classified instead of being coerced to success: there is
  no fallback product, path, pagination mode, envelope, or generic pack loader.
  Future fetch metadata or any sixth fetch-backed resource fails generation.
- Provider-readiness counts must stay explainable: N/A; this is not a
  provider-readiness or live-qualification report.
- Adoption safety invariants:
  - exact five resources only;
  - OneAPI only; fixed audience and Python-compatible cloud/vanity gateway
    rules;
  - 4 MiB per response body, 4 MiB aggregate response bytes, 4 MiB final
    canonical bytes, 50,000 items, the inherited 128-level/250,000-token parser
    guard (the generic response wrapper consumes one depth level), and 51
    logical requests;
  - a logical request may make at most six transport attempts, so the closed
    worst case is 306 injected transport calls before a later host deadline;
  - large integer lexemes stay exact; finite float lexemes use the existing
    Python-compatible binary64 spelling;
  - HTML entities remain literal source data; no unescape is performed;
  - transport, HTTP, rate-limit, retry-clock, parser, and shape errors never
    include URLs, endpoints, response bodies, tokens, or injected diagnostics;
  - even a secret-bearing `ProcessFailure` thrown by an adapter is normalized;
  - no parsed item graph escapes and no persistent write occurs.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run check` with Node `v24.15.0`, Unicode `16.0`
  - bundled Node `v24.14.0`, Unicode `17.0`: `npm run check`
  - `npm run build`
  - `node --test .node-test/node-tests/zcc-collector.test.js`
  - `python3 -m unittest tests.test_zcc_collector_catalog -v`
  - `python3 tools/zcc_collector_catalog.py --check catalogs/zcc-collector-catalog.v1.json`
  - `make check`
  - physically pruned exact `empty`, `zpa`, and `zscaler` checkouts:
    `make PACK_PROFILE=packsets/<profile>.json check`
  - `git diff --check`
- Relevant output summary:
  - full Node on each runtime: 685 total, 684 passed, one existing platform
    skip, zero failed;
  - collector/catalog focus: 11/11 Node and 6/6 Python passed;
  - full Python: 1,400 total, 1,399 passed, one opt-in external provider-source
    skip, zero failed;
  - full distribution gates: generated modules validated, JSON tfvars format
    selected, all packs validated, vendor boundary 187 allowed/zero violations;
  - physically pruned profiles: empty 867/867; ZPA 940 passed plus one existing
    skip; Zscaler 1,380 passed plus one existing skip;
  - Python canonical-byte differential passed for all five existing fixtures;
  - 50,000-item/51-page boundary, 429 scheduling/exhaustion, malformed and
    bounded response matrix, large IDs, Unicode/HTML, and value-free failures
    passed.
- Tests not run and why:
  - no live credentialed ZCC tenant call was authorized or available;
  - no real Node transport exists in this intentionally pure slice.

## Known Deferrals

- Real HTTP/Undici transport, OAuth request body/token exchange, bearer header,
  credential/environment authority, and token lifetime/refresh.
- Node 24 `proxyEnv`, additive corporate CA trust, redirect policy, abort
  semantics, streaming body limits before buffering, socket cleanup, and
  transport-specific diagnostics/redaction.
- One overall monotonic transaction deadline spanning auth, retries, all pages,
  parsing, rendering, and cleanup. The kernel closes logical request and retry
  counts but deliberately does not own wall-clock authority.
- Public process request/response operation, process schemas, dispatcher/main
  wiring, filesystem publication, materialization, refresh, parity, and
  downstream cutover.
- Live tenant proof for every endpoint/cloud and any API behavior beyond the
  committed Python collector contract.
- Reason it is safe to defer: this module is private, excluded from the
  production process bundle, accepts only injected transport/clock functions,
  returns in-memory data, and makes no live or cutover claim.
- Follow-up owner or trigger: a separate reviewed host-adapter/public-operation
  slice after transport design review and controlled credentialed evidence.

## Review Focus

- Highest-risk files or paths:
  - `tools/zcc_collector_catalog.py`
  - `catalogs/zcc-collector-catalog.v1.json`
  - `node-src/domain/zcc-collector-catalog.ts`
  - `node-src/domain/zcc-collector.ts`
- Specific assumptions to attack:
  - the exact five registry entries, paths, GET method, pagination, and trusted
    envelope agree with the committed Python collection path;
  - production and non-production gateway/token host derivation exactly match
    Python, especially `zslogin{cloud}.net` without an extra dot;
  - 50 full 1,000-item pages require one final empty page but can never accept
    item 50,001;
  - only 429 retries, five waits occur before attempt six, numeric overflow
    clamps like Python, and non-429 statuses never sleep;
  - response bytes are counted once globally across retries and pages;
  - body bounds are checked before the kernel copies transport bytes;
  - singleton/list/envelope normalization cannot admit scalar items;
  - the existing lossless parser/renderer composition rejects duplicate keys,
    invalid UTF-8, non-finite numbers, over-depth/token data, and canonical
    expansion beyond 4 MiB without truncation;
  - no thrown adapter error, response body, Retry-After value, URL, cloud,
    vanity, or item data can enter a returned diagnostic.
- Source evidence the reviewer should verify:
  - all five `packs/zcc/registry.json` fetch entries;
  - ZCC pack provider pin/source/shared dependency;
  - shared Python `_oneapi_gateway`, `_zslogin_host`, audience, `compose_url`,
    `_request_with_retry`, `paginate_zia`, and `paginate_single` behavior.
- Generated artifacts the reviewer should compare:
  - regenerated catalog versus committed bytes and fixed source digest;
  - production bundle metafile must exclude both private collector modules and
    the catalog.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - swapped paths or pagination between resources;
  - an added sixth fetch-backed resource or future registry fetch key;
  - exact-multiple pagination, 429 on the terminator, huge/negative numeric
    `Retry-After`, malformed envelope metadata, duplicate keys, scalar items,
    non-finite/huge numbers, Unicode surrogate/UTF-8 boundaries, and canonical
    expansion;
  - proxy/accessor/hidden/symbol response fields and secret-bearing adapter
    failures.
