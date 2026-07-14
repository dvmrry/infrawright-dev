# Builder Review Handoff: Experimental Accepted-Plan Oracle State

## Intent

- Reduce an Oracle transaction by two Terraform commands when the accepted
  import-only plan already contains complete provider-observed values.
- Add an opt-in `INFRAWRIGHT_ORACLE_STATE_SOURCE=accepted-plan` mode. The
  default remains `applied-state`.
- Preserve the provider and Terraform plan as the configuration authority.
- Keep Fetch, identity/import derivation, generated-config policy, provider
  schema projection, artifact rendering, deployment planning, and Apply
  behavior unchanged.

## Base / Head

- Base: `04f32acb2099e6f41f4657ed1d4cb3e75890fba8`
- Implementation head: `803e612be3a08059de551d9ca0032ef509295bc7`
- Diff command:
  `git diff 04f32acb2099e6f41f4657ed1d4cb3e75890fba8..803e612be3a08059de551d9ca0032ef509295bc7`

## Files Changed

- `node-src/domain/import-oracle.ts`
- `node-src/performance/recorder.ts`
- `scripts/compare-performance-reports.mjs`
- `node-tests/import-oracle.test.ts`
- `node-tests/adopt-runner.test.ts`
- `docs/import-oracle.md`
- `docs/performance-benchmark.md`
- `docs/templates/integration-validation-report.md`
- Files intentionally left untouched: providers, collectors, pack metadata,
  provider schemas, projection policy, artifact renderers, plan assessment,
  exact-plan Apply, deployment/pipeline configuration, and accepted migration
  PRs.

## Source Inputs Consulted

- Provider schemas: existing committed schemas exercised by
  `state-project.test.ts`; no schema changed.
- OpenAPI/API contracts: N/A.
- Provider source files: ZIA provider `v4.7.26` source commit
  `6e6509f001ca71adcedfd4884250d09227395bf0` and SDK `v3.8.40` source commit
  `4371c9bab44d852526721b4b5999e2471dda5198` were inspected only for the
  documented cache/snapshot handoff. No provider code changed, and no installed
  binary was proven to match those sources.
- Pack metadata: committed full profile, provider ownership/source/pins,
  schemas, drift policy, and four retained adoption fixtures.
- Existing docs or design records: `docs/import-oracle.md`,
  `docs/performance-benchmark.md`, `docs/adversarial-review.md`.
- Other source evidence: Terraform 1.15.4 structural plan/state pair in
  `node-tests/fixtures/terraform-import-structure-v1.15.4.json`; this fixture
  explicitly is not live provider evidence.

## Generated Artifacts

- Reports: performance reports gain a static `oracle_state_source` label and
  skipped zero-command spans for scratch Apply/state show in accepted-plan
  mode.
- Schemas: none.
- Fixtures: no committed fixture bytes changed.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. All four retained Python
  adoption fixtures produce byte-identical config/import artifacts through
  applied-state and accepted-plan observations.

## Expected Delta

- Expected behavior change: explicit `accepted-plan` extracts observations from
  the exact accepted import plan and skips scratch Apply/state show only when
  planned values, change before/after, prior state, sensitivity masks, and
  expected resource coverage agree with no unknowns.
- Expected report/count/coverage changes: uncorrected Oracle command count is
  five to three; corrected-plan count is six to four. This is structural
  fixture evidence, not a live speed claim.
- Expected generated-output changes: none.
- Expected no-op areas: unset/blank/`applied-state` retains the existing
  transaction, including scratch Apply and state show.

## Invariants Claimed

- Evidence must not be silently dropped: missing planned/prior state, missing
  values or masks, child/extra/deposed/tainted resources, unknown leaves, or any
  cross-surface mismatch rejects accepted-plan mode.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the retained Terraform
  pair proves JSON structure only; live provider equivalence is deferred and
  required before changing the default.
- Ambiguity must stay classified instead of being coerced to success: there is
  no fallback. Accepted-plan ambiguity fails and the operator must rerun with
  applied-state.
- Provider-readiness counts must stay explainable: performance reports identify
  the selected source and zero-command skipped phases.
- Adoption safety invariants: exact import IDs/actions/addresses/provider/type
  validation still runs before state extraction. The provider still executes
  the import/read plan. Terraform JSON equality preserves lossless numbers and
  distinguishes booleans from numbers. No deployment Apply path changed.

## Tests Run

- `npm run typecheck`
- `npm run build`
- `npm run build:test`
- Focused Oracle, state projection, Adopt artifact, performance-tool, and
  Python-disabled operational-runtime tests: 36 passed, 0 failed.
- `python3 -m engine.audit_vendor_boundary`: 187 allowed matches, 0 violations.
- `npm audit --audit-level=high`: 0 vulnerabilities.
- `git diff --check`: passed.
- `npm test`: 1,111 tests; 1,110 passed; 1 skipped; 0 failed.
- Tests not run: live credentials, provider/API/backend calls, live
  plan-versus-state comparison, provider test suite (no provider change), and
  deployment Apply. These are forbidden on this machine or deferred to the
  approved work-machine benchmark.

## Known Deferrals

- Live same-cohort `applied-state` versus `accepted-plan` comparison, including
  provider-observed values/sensitivity masks, exact artifacts, final plan,
  assessment, requests, commands, and wall time.
- Provider cache prototype until the exact baseline binary path, digest, build
  metadata, lock entry, and override/mirror provenance are recorded.
- Provider-wide Oracle batching until telemetry shows provider startup/init or
  repeated cache initialization remains material after request reduction and
  accepted-plan testing.
- Safe trigger: approved work-machine read-only A/B evidence; default remains
  unchanged until then.

## Review Focus

- Highest-risk files: `node-src/domain/import-oracle.ts` and the plan/state
  fixtures in `node-tests/import-oracle.test.ts`.
- Attack whether any Terraform plan shape can skip scratch Apply/state show
  without exact import-only authorization, complete provider observations,
  exact sensitivity evidence, and no unknowns.
- Verify equality remains strict for object keys/order-sensitive arrays,
  null-versus-absent, wide numbers, and bool-versus-number.
- Verify the prior-state and planned-value envelopes cannot admit extra, child,
  wrong-provider/type/mode, deposed, tainted, duplicate, or missing resources.
- Verify accepted-plan errors and telemetry contain no import IDs, raw values,
  plan/state contents, or tenant-identifying data.
- Verify all applied-state behavior and persistent artifact bytes remain
  unchanged.
- Verify the benchmark documentation does not overclaim provider equivalence,
  a live speedup, provider-binary provenance, cache feasibility, or batching
  readiness.
