# Builder Review Handoff: Isolated ZCC Pull Collection

## Intent

- Add the first public Node 24 network operation, `collect_zcc_pull`, for one
  resource at a time from the exact five-resource ZCC collector catalog.
- Move the already-reviewed private OneAPI transport into an integrity-bound,
  directly supervised sibling process. The public parent passes only a bounded
  allowlisted host snapshot over fd 3, accepts one bounded result over fd 4,
  independently validates the returned artifact, and publishes exact Python-
  compatible pull bytes under the request workspace.
- Make the two-file release distribution explicit: the public parent bundle
  embeds the expected child size and SHA-256, retains the verified child bytes,
  and executes those bytes through Node's module stdin rather than executing a
  mutable pathname.
- Preserve the exact Python pull contract, existing collector retry ownership,
  process envelope behavior, compiler/adoption behavior, all committed catalog
  bytes, and every non-ZCC product. This change does not cut over Python or
  claim live-tenant parity.

## Base / Head

- Base: `a0ae8e3bd79f3cebeae6fe486103429a7312e782` (`origin/main` when the
  isolated builder worktree was created).
- Head: the immutable commit checked out on
  `feature/node-zcc-child-collection`; resolve with `git rev-parse HEAD`.
- Diff command:
  `git diff a0ae8e3bd79f3cebeae6fe486103429a7312e782...HEAD`.

## Files Changed

- Public process and contracts:
  - `node-src/process/types.ts`
  - `node-src/process/main.ts`
  - `node-src/process/execute.ts`
  - `node-src/contracts/validators.ts`
  - `node-src/contracts/zcc-pull-collection-semantics.ts`
  - `node-src/domain/zcc-collection-contract.ts`
  - `node-src/domain/zcc-pull-collection.ts`
- Isolated child protocol, supervision, and publication:
  - `node-src/io/zcc-collection-protocol.ts`
  - `node-src/io/zcc-collection-child-runner.ts`
  - `node-src/io/zcc-pull-publisher.ts`
  - `node-src/process/zcc-collector-child.ts`
- Neutral boundary extraction from the reviewed private stack:
  - `node-src/domain/zcc-collector-catalog.ts`
  - `node-src/domain/zcc-collector.ts`
  - `node-src/io/zcc-oneapi-host.ts`
- Distribution and release:
  - `scripts/build-node.mjs`
  - `scripts/release.sh`
  - `.github/workflows/check.yml`
- Schemas and documentation:
  - `docs/schemas/process-request.schema.json`
  - `docs/schemas/process-response.schema.json`
  - `docs/schemas/zcc-pull-collection.schema.json`
  - `docs/node-process-api.md`
  - this handoff.
- Tests:
  - `node-tests/zcc-collection-protocol.test.ts`
  - `node-tests/zcc-collection-child-runner.test.ts`
  - `node-tests/zcc-pull-collection.test.ts`
  - `node-tests/zcc-collector.test.ts`
- Files intentionally left untouched:
  - the private OAuth and Undici transport implementation;
  - every committed collector/adoption/transform/root catalog and fixture;
  - Python collectors, transforms, adoption/oracle behavior, pack metadata,
    Terraform execution, and existing pull compilation/materialization;
  - ZIA, ZPA, ZTC, legacy ZCC auth, custom endpoint support, and non-commercial
    cloud maps.

## Source Inputs Consulted

- Provider schemas: no new provider-schema inference. The exact-five boundary
  is inherited from the reviewed ZCC collector/adoption catalogs.
- OpenAPI/API contracts: no new endpoint extraction. The operation consumes the
  reviewed OneAPI authority, OAuth, redirect, response-bound, and retry
  contracts from `docs/review-handoffs/zcc-oneapi-transport.md`.
- Provider source files: none newly interpreted or changed.
- Pack metadata:
  - `catalogs/zcc-collector-catalog.v1.json`, unchanged byte SHA-256
    `e2e169b5a83dbc240de7b218914332d5f7f3241417e63a8d1663430a2a81f90b`;
  - its frozen source digest
    `d4b8cbef8294e8cb7fd5b17b6efb120b5f8bdc09de8c10e506763547748b11fc`;
  - `packs/_shared/zscaler/collector.py`, `packs/zcc/collector.py`, and the
    existing exact-five adoption and transform catalog boundaries.
- Existing docs or design records:
  - `AGENTS.md`
  - `docs/adversarial-review.md`
  - `docs/adversarial-review-run-prompt.md`
  - `docs/adversarial-review-template.md`
  - `docs/review-handoff-template.md`
  - `docs/node-process-api.md`
  - `docs/review-handoffs/zcc-collector-kernel.md`
  - `docs/review-handoffs/zcc-oneapi-transport.md`
- Other source evidence:
  - Node 24 process/spawn, signal, pipe, filesystem, and module-stdin behavior;
  - the existing repository publisher guard and artifact-binding patterns;
  - the pinned `undici@7.28.0` bundle graph. Its only `worker_threads`
    importers are the two audited `markAsUncloneable` helpers; no Worker is
    constructed.

## Generated Artifacts

- Reports: none.
- Schemas: new strict standalone
  `docs/schemas/zcc-pull-collection.schema.json` plus process request/response
  branches and mandatory custom semantic validation.
- Fixtures: none committed. Tests synthesize frames, pull documents, child
  replacements, directories, targets, and publication races in isolated temp
  roots.
- Snapshots: none.
- Demo or lab outputs: none retained.
- Distribution artifacts: build/release now emit
  `dist/infrawright-zcc-collector-child.mjs` and its SHA-256 beside the public
  bundle. Both remain generated/ignored in the development checkout.
- Artifact drift intentionally expected: process schemas/docs, build/release/CI
  wiring, public operation source, isolated child/publisher source, and tests.
  No catalog, fixture, Python, pack, demo, or Terraform artifact drift is
  expected.

## Expected Delta

- Expected behavior change:
  - a machine-only caller can request `collect_zcc_pull` with mode `oneapi`,
    publication `replace_or_verify_exact`, one ASCII tenant component of at
    most 255 bytes, and exactly one of `zcc_device_cleanup`,
    `zcc_failopen_policy`, `zcc_forwarding_profile`, `zcc_trusted_network`, or
    `zcc_web_privacy`;
  - the request cannot choose an endpoint, URL, credential, proxy, CA, catalog,
    timeout, child path, or output path;
  - `INFRAWRIGHT_ZCC_PULL_OUTPUT_ROOT` must be the existing canonical non-
    symlink request workspace, and the sole artifact is
    `pulls/<tenant>/<resource_type>.json`;
  - a verified private child performs the existing collection and returns
    bounded raw bytes; the parent independently proves exact catalog/resource,
    base64, fatal UTF-8, Python-canonical JSON bytes, count, size, and digest;
  - publication creates an absent target without overwrite, reuses exact bytes,
    or atomically replaces differing bytes under one workspace publisher guard;
  - the content-free complete receipt identifies the artifact path, SHA-256,
    size, item count, and created/replaced/reused action;
  - request/domain failures exit `2`, I/O/internal failures exit `1`, successful
    collection exits `0`, and this operation never exits `3`.
- Expected report/count/coverage changes: no evidence/readiness report or
  coverage accounting changes. The public operation and standalone receipt add
  schema/test surface only.
- Expected generated-output changes: release gains the child bundle and digest;
  the public bundle embeds their exact identity and remains free of Undici,
  OAuth, endpoint, credential-host, and private collector implementation code.
- Expected no-op areas: Python pull bytes, compiler/adoption contracts,
  catalog bytes, Terraform behavior, other products, and all existing public
  operations.

## Invariants Claimed

- Evidence must not be silently dropped:
  - the child returns the exact raw pull document rendered by the reviewed
    collector kernel; the parent reconstructs and compares canonical Python
    JSON bytes before publication;
  - size, digest, count, resource, and catalog-source identity are independently
    joined before the publisher guard is acquired;
  - a differing existing target is fully read, digested, identity-bound, and
    revalidated immediately before rename. A same-inode, same-length mutation
    cannot be classified as exact reuse.
- Generic matcher evidence must not outrank source-backed evidence: N/A. No
  matcher, provider evidence, or transform projection behavior changes.
- Source precedence/provenance must remain explicit: only the embedded exact
  collector catalog can select a resource and path. Neither request nor host
  environment can override catalog, authority, endpoint, output location, or
  publication policy.
- Ambiguity must stay classified instead of being coerced to success:
  malformed frames, duplicate keys, invalid UTF-8, wrong direction/version,
  trailing bytes, invalid child envelopes, identity mismatch, child exit/output
  disagreement, publication race, and cleanup ambiguity all fail with closed
  static codes. No partial child output or publication state becomes success.
- Provider-readiness counts must stay explainable: N/A; this slice adds no
  readiness count.
- Adoption safety invariants:
  - the child is a separate directly supervised process with exact argv, empty
    application environment except `LANG`, `LC_ALL`, and `TZ`, no shell, no
    inherited `execArgv`, ignored stdout/stderr, verified code over stdin,
    credentials over fd 3, and result over fd 4;
  - request and result frames are directional, bounded, fatal-UTF-8, duplicate-
    key-closed, exact-keyed, and trailing-byte-closed;
  - parent code, fd 3, fd 4, and exit are driven concurrently so backpressure
    cannot deadlock. A 310-second monotonic deadline covers spawn through exit;
    timeout kills the direct child and allows only a separate five-second close
    observation window;
  - `SIGTERM`, `SIGINT`, and `SIGHUP` kill the direct child before redelivery.
    Parent `SIGKILL` and an uninterruptible child remain bounded by the ADO
    job/container process tree rather than an overclaimed portable guarantee;
  - every persistent mutation binds open handles plus device/inode identities,
    performs same-directory staged publication and required syncs, and reports
    visible-write or guard-release ambiguity as retryable indeterminate state;
  - collection and compilation have no cross-request lease. Downstream must
    join collection path/hash/size to the compiler source fields;
  - handled failures remove only staging/guard paths whose identities can be
    safely rebound. Crash or cleanup failure may leave a complete staging alias
    and stale guard for the job-owned workspace to discard;
  - credentials, bearer tokens, proxy credentials, CA paths, URLs, response
    bodies, child diagnostics, and nested causes cannot enter the receipt or
    static failure envelope;
  - JavaScript strings cannot be zeroized. Parent and child best-effort clear
    only their mutable owned frame and pull buffers and make no stronger claim.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run build:test`
  - focused collection protocol/runner/publication tests on Node 24.15 and
    Node 24.14;
  - full Node 24.15 and 24.14 with
    `--test-concurrency=2 .node-test/node-tests/*.test.js`;
  - exact four-file OneAPI auth/transport/host/collector replay on both Node
    runtimes;
  - full Python with the exact full pack catalog/profile;
  - examples/demo drift, generated modules, tfvars format, pack metadata,
    vendor-boundary audit, and exact catalog freshness gates;
  - physically pruned exact `empty`, `zpa`, and `zscaler` checkouts through
    `make PACK_PROFILE=packsets/<profile>.json check`;
  - production and release-distribution build checks, bundle-graph guards,
    checksums, and `git diff --check`.
- Relevant output summary:
  - focused collection: 28/28 on Node 24.15 and 28/28 on Node 24.14;
  - full Node 24.15: 781 total, 780 passed, one existing platform skip;
  - full Node 24.14: 781 total, 780 passed, one existing platform skip;
  - exact OneAPI transport replay: 50/50 on each runtime;
  - full Python: 1,400 total, 1,399 passed, one opt-in external provider-source
    skip;
  - physically pruned profiles: empty 867/867; ZPA 941 total with one existing
    external-source skip; Zscaler 1,381 total with the same one skip;
  - full pack selection, demo drift, generated modules, JSON tfvars selection,
    pack metadata, and vendor boundary passed; the vendor audit retained 187
    allowed matches and zero violations;
  - all six collector/adoption/transform/cohort/root catalog byte-freshness
    checks passed;
  - exact `undici@7.28.0` reinstall reported zero vulnerabilities; typecheck,
    production two-bundle build/graph guards, script syntax, SHA-256 copy check,
    relocated no-install/no-network collection smoke, and whitespace checks
    passed;
  - GitHub Actions did not execute. All 17 jobs for PR #187 were refused before
    runner assignment because the account billing/spending limit was reached;
    only the local-equivalent gates recorded here were run.
- Tests not run and why:
  - no credentialed ZCC tenant or actual corporate ADO proxy/inspection-CA run
    is authorized in this builder slice;
  - no Python-to-Node live collection/cutover run is claimed;
  - hosted GitHub Actions were unavailable for the external account-state reason
    above, not because a repository test started and failed.

## Known Deferrals

- Live tenant byte parity for all five endpoints, actual corporate proxy and CA
  proof, selected commercial cloud/vanity authority, and redacted production
  token/response evidence.
- Python collector cutover. This operation is additive and downstream can join
  its receipt to the existing compiler while byte compatibility is evaluated.
- Legacy session/cookie auth, custom OneAPI endpoints, non-commercial cloud
  maps, and products/resources outside the exact five ZCC catalog entries.
- Strong hostile same-UID filesystem security. The publisher detects and fails
  path/identity races on trusted ephemeral runners but portable path operations
  are not an indivisible capability system.
- Atomic parent-death cleanup. An uncatchable parent `SIGKILL` or kernel-
  uninterruptible child can outlive the parent until the ADO job/container tree
  is destroyed.
- Physical erasure of JavaScript credential/token strings.
- Reason it is safe to defer: the operation is closed to one existing catalog,
  uses exact byte verification and isolated publication, changes no Python
  behavior, and makes no live proof or cutover claim. The remaining assumptions
  are explicit deployment validations rather than silently accepted success.
- Follow-up owner or trigger: controlled ADO/live-tenant parity run before any
  default-pipeline cutover; fresh review for each additional auth mode, cloud,
  product, resource catalog, or Node/Undici version.

## Review Focus

- Highest-risk files or paths:
  - `node-src/io/zcc-collection-child-runner.ts`
  - `node-src/process/zcc-collector-child.ts`
  - `node-src/io/zcc-collection-protocol.ts`
  - `node-src/io/zcc-pull-publisher.ts`
  - `node-src/domain/zcc-pull-collection.ts`
  - `scripts/build-node.mjs`
  - process and receipt schema/semantic joins.
- Specific assumptions to attack:
  - retained verified child bytes, rather than a later sibling pathname, are
    what receive the credential frame;
  - fd 3 EPIPE arbitration cannot turn child error or incomplete code delivery
    into success, and concurrent pipe driving cannot deadlock at maximum sizes;
  - timeout, close, signal redelivery, handler removal, child reaping, and pipe
    destruction cover every spawn/write/read/exit ordering without double
    settlement or an accidental success;
  - the child bundle graph cannot import public process/publication/Ajv/
    Terraform/adoption code or construct a Worker; the parent cannot contain
    Undici/OAuth/endpoints/private collector implementation;
  - strict base64 round-trip, fatal UTF-8, canonical Python rendering, exact
    resource/catalog/count/size/hash checks reject every forged child result;
  - caller option/env/resource snapshots cannot be mutated across awaits;
  - output/workspace identity is checked before child, before guard, and inside
    publication, including root or directory replacement;
  - target classification and immediate pre-rename CAS catch same-inode,
    same-length content changes; special files and unsafe aliases never become
    reuse or cleanup victims;
  - create/replace plus later cleanup failure is indeterminate while exact reuse
    retains ordinary cleanup semantics;
  - request/response schemas and custom semantic validation bind mode, policy,
    tenant, exact resource, catalog digest, artifact path, digest, size, count,
    and operation result without structural-only false positives.
- Source evidence the reviewer should verify:
  - exact-five collector catalog/source digest and unchanged Python canonical
    rendering;
  - prior OneAPI transport retry/error/diagnostics guarantees remain intact;
  - Node 24 module-stdin, spawn fd ordering, signals, and filesystem primitives;
  - exact pinned Undici importer graph and no Worker construction.
- Generated artifacts the reviewer should compare:
  - public and child bundle metafiles/graph guards;
  - embedded child size/SHA against generated child bytes;
  - release checksums and two-file outside-checkout smoke;
  - process schemas plus standalone receipt schema/custom semantics.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - a sixth resource, 256-byte tenant, duplicate JSON key, wrong frame magic,
    truncated/oversized/trailing frame, invalid UTF-8, forged base64/canonical
    bytes/count/hash, child output/exit mismatch, fd backpressure, child stall,
    sibling replacement, workspace rollover, directory swap, hard-link alias,
    same-inode mutation, guard contention/cleanup failure, post-link/post-rename
    failure, and an artifact replaced between collection and compilation.
