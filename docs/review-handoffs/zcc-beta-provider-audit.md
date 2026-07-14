# Builder Handoff: ZCC Beta Provider Adoption Audit

## Intent

- Pin the exact public ZCC provider and SDK authority used by the current pack.
- Reconcile all seven generated ZCC resources with their Fetch, import, Read,
  lifecycle, pagination, and pack-policy boundaries.
- Provide a bounded downstream evidence matrix without changing runtime,
  provider, pack, catalog, state, or deployment behavior.

## Base / Head

- Base: `3a1cbbfbcc8553f80b0638a5d212d17fdd57ee9b`
- Head: the commit containing this handoff and the ZCC beta provider audit.
- Diff command: `git diff 3a1cbbfbcc8553f80b0638a5d212d17fdd57ee9b..HEAD`

## Files Changed

- Files:
  - `docs/provider-labs/zcc-beta-provider-audit.md`
  - `docs/integration-validation.md`
  - `docs/review-handoffs/zcc-beta-provider-audit.md`
- Files intentionally left untouched: all runtime code, provider code, pack
  metadata, generated catalogs, schemas, fixtures, tests, Make targets, and CI.

## Source Inputs Consulted

- Provider schemas: committed `packs/zcc/schemas/provider/zcc.json` and every
  resource schema at provider `0.1.0-beta.1`.
- OpenAPI/API contracts: provider-generated documentation and pinned SDK
  endpoint/model/paginator implementations; no separate OpenAPI document was
  available in this repository.
- Provider source files: all seven resource implementations and relevant
  acceptance tests at commit `3e7598fc`.
- Pack metadata: ZCC registry, manifest, five Fetch-backed overrides,
  references, lookup sources, and frozen exact-five catalogs.
- Existing docs or design records: integration validation, pack authoring,
  Zscaler quirk inventory, frozen ZCC parity contract, and provider-lab safety
  conventions.
- Other source evidence: sanitized downstream screenshots reporting live 404s,
  one device-cleanup enum mismatch, and one trusted-network numeric import
  failure. Those reports are directional evidence, not retained raw fixtures.

## Generated Artifacts

- Reports: one authored audit and downstream report matrix.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: maintainers receive source-pinned classifications
  and executable live gates; runtime behavior does not change.
- Expected report/count/coverage changes: none until downstream executes the
  matrix.
- Expected generated-output changes: none.
- Expected no-op areas: Fetch, Transform, Adopt, Oracle, modules, roots,
  staging, plan, assessment, Apply, provider, and pack metadata.

## Invariants Claimed

- Evidence must not be silently dropped: unresolved items remain explicit live
  gates, provider-only limitations, or source-acquisition blocks.
- Generic matcher evidence must not outrank source-backed evidence: exact
  matcher candidates are restricted to values and field paths supported by
  provider source plus retained live evidence.
- Source precedence/provenance must remain explicit: provider, SDK, pack,
  runtime, and downstream authorities are recorded separately.
- Ambiguity must stay classified instead of being coerced to success: trusted
  network name import and both gateway-parked sources remain deferred.
- Provider-readiness counts must stay explainable: the downstream matrix names
  fetched, skipped, unsupported, eligible, published, failed, and command
  counts separately.
- Adoption safety invariants: no deployment Apply, provider fork, raw-API
  configuration authority, credential output, or speculative drop is allowed.

## Tests Run

- Commands:
  - `python3 -m unittest tests.test_transform_catalog tests.test_adoption_catalog`
  - `git diff --check`
  - direct source inspection of the pinned provider and SDK worktrees.
- Relevant output summary: 35 catalog tests pass; exploratory unencoded pack
  edits were removed; the branch contains documentation only.
- Tests not run and why: no live ZCC call or Terraform import was run because
  credentials and tenant evidence are downstream-only.

## Known Deferrals

- Deferred work: every production pack policy; notification/posture Fetch;
  trusted-network identity change; provider pagination fixes; frozen exact-five
  catalog evolution or retirement.
- Reason it is safe to defer: current runtime behavior is unchanged and every
  unsafe or unproved resource remains fail-closed or source-less.
- Follow-up owner or trigger: downstream returns complete authority hashes and
  the sanitized matrix; provider-pin changes trigger full requalification.

## Review Focus

- Highest-risk files or paths:
  `docs/provider-labs/zcc-beta-provider-audit.md`.
- Specific assumptions to attack:
  - the exact provider/SDK pin and release status;
  - endpoint and pagination claims;
  - importer behavior and singleton classification;
  - the exact four forwarding-profile omission path families;
  - the distinction between API omission and SDK-tag workarounds;
  - the v1/v2 trusted-network identity requirements;
  - the claim that frozen catalogs cannot silently consume new pack semantics;
  - the report's publication/Oracle-command count semantics.
- Source evidence the reviewer should verify: provider resource and acceptance
  source, SDK models/paginators, local registry/overrides/catalog generators,
  and generic Node metadata/policy support.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  case-insensitive duplicate names, incomplete provider list pages, singleton
  cardinality, JSON string versus number values, optional/computed defaults,
  repeated nested paths, source 404s, and stale exact-five catalog digests.
