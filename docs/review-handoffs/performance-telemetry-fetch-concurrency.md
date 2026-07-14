# Builder Review Handoff — Performance Telemetry and Concurrent Fetch

## Intent

- Add opt-in, sanitized, deterministic performance evidence for Fetch,
  Adopt/Oracle, and the already-available later operational command stages.
- Replace serial cross-resource Fetch execution with a bounded, product-fair
  worker pool selected by `--concurrency`, while retaining `1` as the default.
- Supply credential-free manifest/comparison tools and exact work-machine
  commands for later live A/B qualification.
- Preserve provider-state projection as the adoption authority, exact artifact
  bytes, resource-local pagination/expansion order, partial-success behavior,
  retry semantics, and all default operational behavior.

## Base / Head

- Base: `8ef3b426e042448911800183a120fc460cc476d4`, accepted draft PR #209 head.
- Head: frozen head of `feature/performance-read-amplification`; the review
  request supplies the resolved commit because a commit cannot contain its own
  hash.
- Diff command:
  `git diff 8ef3b426e042448911800183a120fc460cc476d4...HEAD`.

## Files Changed

- Runtime and CLI:
  - `Makefile`
  - `node-src/cli/main.ts`
  - `node-src/collectors/rest.ts`
  - `node-src/collectors/types.ts`
  - `node-src/collectors/zscaler-adapters.ts`
  - `node-src/domain/adopt-runner.ts`
  - `node-src/domain/import-oracle.ts`
  - `node-src/io/performance-report.ts`
  - `node-src/io/rest-http-transport.ts`
  - `node-src/performance/recorder.ts`
- Benchmark tooling and documentation:
  - `scripts/compare-performance-reports.mjs`
  - `scripts/performance-artifact-manifest.mjs`
  - `docs/integration-validation.md`
  - `docs/performance-benchmark.md`
  - `docs/templates/integration-validation-report.md`
- Tests:
  - `node-tests/import-oracle.test.ts`
  - `node-tests/metadata-cli.test.ts`
  - `node-tests/performance-tools.test.ts`
  - `node-tests/rest-collector.test.ts`
  - `node-tests/rest-http-transport.test.ts`
- Handoff: this file.
- Intentionally untouched: accepted PR #209 and its ancestors, provider
  repositories, provider behavior, pack registries, projection policy,
  artifact formats, Terraform plan/assessment/Apply semantics, Python parity
  oracles, ADO/work-side configuration, credentials, live providers/backends,
  and deployment state.

## Source Inputs Consulted

- Provider schemas: existing loaded schemas only; unchanged.
- OpenAPI/API contracts: existing registry-driven collector behavior and tests;
  no endpoint semantics changed.
- Provider source files: read-only discovery against ZIA provider v4.7.26;
  no provider code is part of this slice and binary provenance remains
  externally unconfirmed.
- Pack metadata: real generic pack/profile/catalog loaders and original fetch
  registry entries.
- Existing docs/design: collector tests, REST transport tests, Oracle/Adopt
  tests, integration-validation documentation, and the repository adversarial
  review workflow.
- Other source evidence: current Node CLI/Make command surface, existing retry
  implementation, Python-compatible renderers, Terraform command funnel, and
  exact retained operational/parity suites.

## Generated Artifacts

- Reports: optional `infrawright-performance-report` JSON written to a private
  same-directory temporary file and atomically renamed. No report is committed.
- Schemas: none; this is an internal report shape, not a public authorization
  protocol.
- Fixtures/snapshots: delayed fake transports and fake Terraform output only;
  no tenant/API/plan/state content is committed.
- Demo/lab outputs: disposable performance reports and hash manifests removed
  by their tests.
- Artifact drift intentionally expected: none. Fetch pulls and adoption output
  must remain exact-byte compatible.

## Expected Delta

- `fetch --concurrency N` accepts positive safe integers through 64. Default
  remains one. `make fetch` forwards optional `FETCH_CONCURRENCY`.
- Independent resource Fetches may overlap. One global bound is enforced and
  products rotate fairly; pagination pages and expansion paths for one
  resource remain sequential.
- Authentication still occurs once per existing auth identity before data
  work. Outcomes and diagnostics are buffered and replayed in original
  selection order, independent of completion order.
- `INFRAWRIGHT_PERFORMANCE_REPORT=<file>` opt-in records static phase/resource/
  product/endpoint-family labels, durations, pages, wire attempts, retries,
  retry delay, 429s, instance counts, and Oracle Terraform command count.
- Normal output and behavior are unchanged when reporting is disabled.
- Credential-free scripts create exact-byte artifact manifests and combine
  report directories into the requested Markdown comparison table.
- No live timing or safe-default claim is made. The runbook requires repeated
  work-machine A/B evidence before changing the default.

## Invariants Claimed

- Evidence must not be silently dropped: every selected resource produces one
  deterministic processed/skipped/failed outcome; unrelated successes persist
  when one resource fails.
- Generic matcher evidence must not outrank source-backed evidence: unchanged;
  no matcher or provider-evidence logic is modified.
- Source precedence/provenance must remain explicit: unchanged; registry
  metadata remains the fetch authority and provider state remains the adoption
  authority.
- Ambiguity must stay classified instead of being coerced to success: unchanged.
- Provider-readiness counts must stay explainable: reports distinguish logical
  pages from physical wire attempts and group attempts by static classification,
  phase, endpoint family, status, product, and resource family.
- Adoption safety invariants: the Oracle still validates an exact import-only
  plan and performs the same ephemeral local-state Apply/show. Telemetry cannot
  authorize or skip a Terraform phase.
- Reports never receive headers, credentials, cookies, request/response bodies,
  import IDs, concrete URLs, tenant URLs, or plan/state content. Report errors
  are static; a primary command failure retains precedence.
- Concurrency does not change per-resource item order, output path, output
  bytes, retry budgets, optional-status handling, or deterministic diagnostics.

## Tests Run

- Focused edit/checkpoint suites:
  - REST collector worker-pool/determinism/retry tests;
  - REST transport attempt/retry telemetry tests;
  - Oracle phase/command-count tests, including corrected-plan and skipped-plan
    paths;
  - Adopt runner and metadata CLI performance-path tests;
  - manifest/comparison tool tests;
  - Python-disabled operational runtime smoke.
- Final local gate:
  - `npm run typecheck` passed;
  - `npm run build` passed;
  - focused affected suites passed;
  - Python-disabled operational runtime smoke passed;
  - vendor-boundary audit passed (187 allowed, 0 violations);
  - `npm audit --audit-level=high` passed (0 vulnerabilities);
  - `git diff --check` passed;
  - post-remediation focused recheck passed: 38 tests, 0 failed;
  - exact patched-head `npm test` passed: 1,105 tests, 1,104 passed,
    1 skipped, 0 failed.
- Tests not run: live credentials, live provider/API/backend, deployment Apply,
  work-side A/B runs, and provider-cache tests. They are forbidden or belong to
  later slices.

## Known Deferrals

- Live Fetch concurrency 1/2/4/8 A/B, repeated timing, 429 behavior, and exact
  live pull hashes await the approved work machine. Default remains one.
- The matching ZIA provider v4.7.26 source is locally available, but no local
  provider binary/archive could be tied to the prior live baseline. Provider
  cache code is deferred until work-side binary provenance is recorded.
- Plan-versus-applied-state and optional accepted-plan Oracle work belongs to
  the next separately reviewed performance slice.
- Provider snapshot/replay and Oracle batching remain feasibility work; this
  slice introduces neither.

## Review Remediation

- Concurrent render/write failures are captured as indexed resource outcomes.
  The lowest selection-index failure is authoritative at every concurrency;
  serial mode stops at that failure, and diagnostics for earlier successful
  writes are preserved.
- Artifact digests now bind the format marker, every explicit root boundary
  and label (including empty roots), file count, and every file record. The
  comparison tool validates the complete manifest, recomputes its digest, and
  rejects differing root sets.
- Report comparison now requires successful, structurally valid reports, one
  report per command, a Fetch report, and the same command set in every
  variant. It cross-checks summary counts and exposes 429/retry evidence in a
  second table. The runbook uses fail-fast shell execution.
- Derived Transform delegation propagates failed/skipped status into the
  per-resource Adopt span instead of reporting success.
- A nonzero command result remains the primary failure if optional report
  writing also fails; the report failure is a warning. Fetch, Transform, and
  Adopt regressions exercise the arbitration.

## Review Focus

- Highest-risk files: `node-src/collectors/rest.ts`,
  `node-src/io/rest-http-transport.ts`, `node-src/performance/recorder.ts`,
  `node-src/domain/import-oracle.ts`, and `node-src/cli/main.ts`.
- Attack whether the worker pool can exceed its bound, multiply a shared
  OneAPI authority, reorder outputs/diagnostics, reacquire auth, cancel valid
  successes, or make retry bursts unbounded.
- Attack request accounting for double-counts, omitted redirects/retries,
  logical-page versus physical-attempt confusion, and nondeterministic ordering.
- Verify no dynamic URL, query, header, body, import ID, raw item, plan/state
  value, or tenant identity reaches a report field or error.
- Verify telemetry cannot change default execution, swallow the primary error,
  authorize an Oracle phase, or claim live performance from fixtures.
- Verify benchmark manifests compare exact files without exposing contents or
  absolute paths and that the runbook isolates every variant.
- Do not review provider caching, accepted-plan state, Oracle batching, or the
  completed migration architecture; those are outside this slice.
