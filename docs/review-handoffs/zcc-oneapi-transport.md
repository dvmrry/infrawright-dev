# Builder Review Handoff: Private ZCC OneAPI Transport

## Intent

- Add the first real Node 24 network boundary for the private exact-five ZCC
  collector kernel: OneAPI client-credentials OAuth, bounded authenticated GET
  transport, corporate proxy/CA support, one overall deadline, and deterministic
  dispatcher cleanup.
- Keep credentials exclusively in an explicit private host environment
  snapshot. Never add them, the bearer token, proxy credentials, CA paths,
  response bodies, URLs, or nested network causes to a process request/result,
  artifact, file, log, or diagnostic.
- Use a pinned Node-compatible HTTP library rather than hand-rolling HTTP,
  proxy tunneling, TLS, or connection pooling.
- Keep the complete host/transport/auth surface private and excluded from the
  production process bundle. Add no process operation, request/response schema,
  publisher, materializer, Python replacement, or cutover claim.

## Base / Head

- Base: `45bc7f96a0f7699e48d3d883973a816b13853bf9` (merged private collector
  kernel).
- Head: the immutable commit checked out on
  `feature/node-zcc-oneapi-transport`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff 45bc7f96a0f7699e48d3d883973a816b13853bf9...HEAD`.

## Files Changed

- Private OAuth contract:
  - `node-src/domain/zcc-oneapi-auth.ts`
- Private network and host boundaries:
  - `node-src/io/zcc-oneapi-transport.ts`
  - `node-src/io/zcc-oneapi-host.ts`
- Closed collector failure bridge and shared body bound:
  - `node-src/domain/zcc-collector.ts`
- Dependency lock:
  - `package.json`
  - `package-lock.json`
- Regressions:
  - `node-tests/zcc-oneapi-auth.test.ts`
  - `node-tests/zcc-oneapi-transport.test.ts`
  - `node-tests/zcc-oneapi-host.test.ts`
  - `node-tests/zcc-collector.test.ts`
- Documentation:
  - this handoff.
- Files intentionally left untouched:
  - `node-src/process/main.ts`, `execute.ts`, process types, and every public
    request/response schema;
  - all publisher, materializer, refresh, adoption, and parity operations;
  - Python collector behavior, pack metadata, committed catalogs, fixtures,
    provider evidence, and generated artifacts;
  - ZIA, ZPA, ZTC, legacy auth, and non-commercial host maps;
  - production bundle entry points and release behavior.

## Source Inputs Consulted

- Provider schemas: none changed or interpreted.
- OpenAPI/API contracts:
  - Zscaler OneAPI client-credentials documentation and current access-token
    lifetime documentation;
  - fixed OAuth audience `https://api.zscaler.com` and the existing committed
    exact-five ZCC catalog.
- Provider source files: none changed. This slice does not infer provider
  projection or source-operation behavior.
- Pack metadata and Python authority:
  - `packs/_shared/zscaler/collector.py`, especially `_zslogin_host`,
    `_oneapi_gateway`, `acquire`, and `build_headers`;
  - `engine/collectors/rest/__init__.py`, especially `_request_with_retry`,
    `_retry_delay`, `ca_bundle_path`, and `real_opener`;
  - `packs/zcc/collector.py`;
  - `catalogs/zcc-collector-catalog.v1.json` through the existing validated
    embedded loader.
- Existing docs or design records:
  - `docs/review-handoffs/zcc-collector-kernel.md`
  - `docs/adversarial-review.md`
  - `docs/review-handoff-template.md`
- Other source evidence:
  - Node 24 TLS certificate APIs and abort primitives;
  - Undici 7.28.0 `EnvHttpProxyAgent`, `ProxyAgent`, `Agent`, `Client`, request,
    dispatcher lifecycle, connector, body stream, and maximum-response source
    and type declarations;
  - current Zscaler Go SDK token response shape (`access_token`, `token_type`,
    `expires_in`) as corroboration only. Local Python/catalog behavior remains
    the endpoint/form authority.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Dependency artifact: exact `undici` `7.28.0` lock entry.
- Artifact drift intentionally expected: only dependency manifests, private
  source/tests, and this handoff. Every catalog, schema, fixture, Python output,
  and production bundle behavior remains unchanged.

## Expected Delta

- Expected behavior change:
  - a private Node caller can provide an explicit allowlisted host-environment
    snapshot and collect one exact ZCC resource through real OneAPI OAuth and
    authenticated Undici transport;
  - token requests use the existing token authority, HTTP POST,
    `application/x-www-form-urlencoded`, `grant_type=client_credentials`,
    client ID/secret, and fixed audience; only status 200 is accepted;
  - token bodies are fatal-UTF-8, duplicate-key-closed, limited to 64 KiB, and
    require a non-empty header-safe token plus a 60-86,400 second `expires_in`;
  - tokens refresh lazily with a 30-second monotonic skew and single-flight
    acquisition; one data 401 forces at most one refresh/replay;
  - auth 429 gets the existing five-wait schedule; data 429 is returned to the
    collector kernel and therefore never double-retries;
  - successful data bodies stream into one bounded accumulator and preserve
    exact bytes; error and redirect bodies are destroyed without rendering;
  - direct, destination-through-proxy, and HTTPS-proxy TLS all receive the
    additive default/custom CA set, strict verification, and TLS 1.2 minimum;
  - proxy selection is snapshotted once with lowercase precedence, HTTPS-to-
    HTTP fallback only when HTTPS is absent, and explicit empty strings that
    prevent later ambient environment fallback;
  - one 300-second monotonic abort authority covers CA loading, OAuth, request
    and body waits, auth/data retry sleeps, refresh, kernel parsing/rendering,
    and the final checkpoint. Dispatcher cleanup gets a separate five-second
    close/destroy window.
- Expected report/count/coverage changes: none. Private numeric adapter stats
  exist for tests only and are not added to the collected artifact contract.
- Expected generated-output changes: none.
- Expected no-op areas: production process API/bundle, Python behavior,
  catalogs, Terraform/adoption behavior, publication, and non-ZCC products.

## Invariants Claimed

- Evidence must not be silently dropped: successful response bytes are passed
  unchanged to the already-reviewed lossless kernel. HTTP error bodies are not
  evidence and are intentionally discarded before static status failures.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or evidence behavior changes.
- Source precedence/provenance must remain explicit: the adapter authorizes
  only URLs reconstructed from the selected resource in the embedded exact
  catalog. It cannot attach a bearer token to a different origin, path, query,
  resource, method, userinfo URL, or caller-invented request.
- Ambiguity must stay classified instead of being coerced to success: malformed
  credentials, token JSON, token lifetime, HTTP response shape, CA bundle,
  proxy URL, redirect, unsupported environment name, status, and timeout all
  fail closed with a static code.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  coverage count changes.
- Adoption safety invariants:
  - credentials, bearer tokens, proxy credentials, response/error bodies, CA
    paths, URLs, and underlying cause messages cannot enter a returned failure;
  - only the explicit environment allowlist is read; the modules never read
    `process.env`, and explicit empty proxy options prevent Undici from doing so;
  - `NODE_TLS_REJECT_UNAUTHORIZED` and other TLS-disable inputs are rejected;
  - default Node trust remains present when a custom bundle is selected;
  - redirects are never followed, so neither form credentials nor bearer
    tokens can be forwarded to a redirect target;
  - valid `Content-Length` is checked first but never trusted as the sole bound;
    intrinsic typed-array length is checked before any chunk copy, and the
    streaming accumulator has fixed 64 KiB/4 MiB storage plus a 32,768-chunk
    ceiling rather than an unbounded chunk metadata or iteration surface;
  - every body is consumed or destroyed; socket/dispatcher lifecycle is
    explicit and bounded;
  - arbitrary adapter failures and forged runtime codes cannot escape the
    collector's static failure table. The sole ProcessFailure admitted across
    the retry-clock boundary is the timeout code, which is recreated from the
    kernel-owned static contract with details discarded;
  - JavaScript strings cannot be physically zeroized. The claim is no
    persistence or externalization; credential and token references are
    cleared after the private operation.

## Tests Run

- Commands completed before the final full matrix:
  - `npm run typecheck`
  - `npm run build:test`
  - Node 24.15 focused:
    `node --test .node-test/node-tests/zcc-oneapi-auth.test.js
    .node-test/node-tests/zcc-oneapi-transport.test.js
    .node-test/node-tests/zcc-oneapi-host.test.js
    .node-test/node-tests/zcc-collector.test.js`
  - Node 24.14 focused through `npx --yes node@24.14.0 --test ...`
  - `npm run build`
  - `git diff --check`
- Final serialized matrix:
  - current Node 24.15:
    `node --test --test-concurrency=2 .node-test/node-tests/*.test.js`
  - Node 24.14:
    `npx --yes node@24.14.0 --test --test-concurrency=2
    .node-test/node-tests/*.test.js`
  - full Python:
    `INFRAWRIGHT_PACKS="$PWD/packs" make
    PACK_CATALOG="$PWD/packsets/full.json"
    PACK_PROFILE="$PWD/packsets/full.json" test`
  - complete distribution tail: examples/demo drift, generated modules,
    tfvars format, all pack metadata, and vendor-boundary audit;
  - freshness checks for ZCC collector/adoption/transform, the selected ZIA
    cohort, ZPA cohort, and all-Zscaler root catalogs;
  - physically pruned `empty`, `zpa`, and `zscaler` worktrees from a synthetic
    commit containing the complete staged change, each through its exact
    `make PACK_PROFILE=packsets/<profile>.json check` gate;
  - final `npm ci --ignore-scripts`, `npm run typecheck`, `npm run build`,
    `git diff --cached --check`, and `git diff --check`.
- Relevant focused output summary:
  - Node 24.15: 42/42 focused tests passed after all remediations;
  - Node 24.14: 42/42 focused tests passed on the final exact focused set;
  - full Node 24.15: 729 total, 728 passed, one existing platform skip;
  - full Node 24.14: 729 total, 728 passed, one existing platform skip;
  - full Python: 1,400 total, 1,399 passed, one opt-in external provider-source
    skip;
  - physically pruned profiles: empty 867/867; ZPA 941 total with one existing
    external-source skip; Zscaler 1,381 total with the same one skip;
  - full pack selection, demo/generator/metadata gates passed; vendor boundary
    retained 187 allowed matches and zero violations;
  - every catalog freshness command passed with no byte drift;
  - exact `undici@7.28.0` lock reinstall reported zero vulnerabilities;
  - typecheck, production build/private-bundle exclusion, and both whitespace
    checks passed;
  - exact form/header/authority, auth and data 429 ownership, one 401 replay,
    fractional monotonic lifetime, redirect refusal, fatal UTF-8/duplicate JSON,
    status/body destruction, 64 KiB/4 MiB stream bounds, pre-copy oversized
    chunks, 20,000 accepted zero chunks, excessive-fragmentation rejection,
    ambient proxy mutation, additive CA, invalid/
    oversized/FIFO CA, proxy CONNECT stall, request/body/retry deadline stalls,
    cleanup fallback, and secret-free failure cases passed.
- Tests not run and why:
  - no credentialed ZCC tenant or corporate ADO proxy/CA run is authorized in
    this private builder slice;
  - no public operation exists to exercise through the production bundle.

## Known Deferrals

- Live ADO evidence for the actual corporate proxy, inspection CA, selected
  cloud/vanity token authority, and a redacted real token response shape.
- A public process operation/schema and host-owned environment snapshot from
  `process.env`; this PR deliberately exposes no request surface.
- Live tenant proof for all five endpoints and a later byte-parity/cutover
  result against the Python collector.
- Exact user-agent and raw form percent-encoding parity. Form field values and
  order are frozen; Python's incidental encoding bytes are not.
- Non-commercial/government host maps, custom OneAPI host overrides, legacy
  cookies/session auth, and products other than ZCC.
- The strict documented `expires_in` range is a private pre-publication
  contract. A redacted live response must confirm it before public wiring.
- Reason it is safe to defer: every new module is unreachable from the process
  entry point and excluded from the production bundle. No credentials or
  network are reachable without a direct private source import.
- Follow-up owner or trigger: controlled ADO/live proof, then a separately
  reviewed private collector-operation or parity slice.

## Review Focus

- Highest-risk files or paths:
  - `node-src/io/zcc-oneapi-host.ts`
  - `node-src/io/zcc-oneapi-transport.ts`
  - `node-src/domain/zcc-oneapi-auth.ts`
  - the trusted-failure bridge in `node-src/domain/zcc-collector.ts`
- Specific assumptions to attack:
  - Undici 7.28 `request` does not follow redirects without a redirect
    interceptor, and the explicit 3xx checks destroy bodies before failure;
  - `EnvHttpProxyAgent` receives explicit strings even when empty and cannot
    re-read ambient proxy/NO_PROXY state after construction;
  - its internal ProxyAgent CONNECT client actually receives the custom
    connection/header/body bounds through `clientFactory`;
  - additive CA reaches direct `connect`, destination `requestTls`, and
    `proxyTls`, while proxy credentials never enter diagnostics;
  - exact catalog URL authorization happens before token acquisition or bearer
    attachment and cannot be bypassed with alternate query ordering, symbols,
    accessors, proxies, or revoked proxies;
  - token acquisition cannot leak the form/body/URL through nested Undici
    errors, redirects, invalid responses, or cleanup failures;
  - a 401 cannot replay more than once, auth cannot retry more than five 429s,
    and data 429 cannot retry both inside and outside the kernel;
  - fractional `performance.now()` values remain valid and refresh at the
    intended 30-second boundary;
  - the response limit is enforced using intrinsic length before allocation/
    copy and cannot be bypassed through one huge chunk, later overflow,
    incorrect Content-Length, zero-length fragmentation, resizable/detached/
    shared buffers, or a stalled body;
  - overall abort listener registration has no already-aborted race, and
    kernel retry sleeps preserve the static timeout code without relaying the
    originating ProcessFailure;
  - FIFO/custom CA inputs cannot block past the deadline and invalid PEM cannot
    be silently accepted because default roots make the combined context valid;
  - primary failure always wins over cleanup failure, while success never
    returns before verified close.
- Source evidence the reviewer should verify:
  - local Python endpoint/form/backoff/CA behavior listed above;
  - embedded exact ZCC catalog paths and pagination;
  - Undici 7.28 EnvHttpProxyAgent environment fallback and ProxyAgent
    `clientFactory`/TLS routing source;
  - Node 24 default CA and `X509Certificate` behavior.
- Generated artifacts the reviewer should compare: none. Confirm all catalogs,
  schemas, fixtures, and Python outputs are unchanged.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - proxy credentials and malformed proxy URLs; NO_PROXY mutation; redirects;
    duplicate token keys; weird token control characters; token expiry races;
    concurrent refresh; 401/429 interaction; oversized first/later chunks;
    zero-chunk streams; content-length mismatch; abort while registering a
    retry wait; FIFO/rotating CA files; cleanup close/destroy races; arbitrary
    thrown ProcessFailure codes/messages; and accidental process-main imports.

## Builder Remediation During Implementation

- Independent early read-only audits found and the builder remediated:
  - response chunks were copied before the remaining-byte bound and stored in
    an unbounded chunk array;
  - fractional monotonic timestamps were rejected by an integer-only lease
    check;
  - symbol keys were sorted before type rejection at two object boundaries;
  - a FIFO CA path could block in `open` before regular-file validation;
  - ProxyAgent's internal CONNECT client did not inherit the 30-second bounds;
  - explicit empty proxy/NO_PROXY values and ambient-mutation regressions were
    required to prevent EnvHttpProxyAgent fallback;
  - retry sleep had an abort-listener registration race;
  - the trusted error factory accepted an out-of-union runtime code without an
    own-table lookup.
- Each item has a targeted regression in the focused 42-test set. This section
  is orientation only; the required fresh reviewer must verify the final diff
  independently and must not treat the early partial audit as approval.
