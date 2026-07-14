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
- Initial review head: `f0df44ed142ed45bbb456cc4746fe7ce4d1b50fb`
- Remediated implementation head: `bbf5645cbcf0d6e0970cb0ae57fc22480a20f736`
- Evidence-remediation head: `241f5185f43b7da992c5fed97c0e91b74e82dad0`
- Diff command:
  `git diff 04f32acb2099e6f41f4657ed1d4cb3e75890fba8..241f5185f43b7da992c5fed97c0e91b74e82dad0`

## Files Changed

- `node-src/domain/import-oracle.ts`
- `node-src/json/python-equality.ts`
- `node-src/performance/recorder.ts`
- `scripts/compare-performance-reports.mjs`
- `node-tests/import-oracle.test.ts`
- `node-tests/adopt-runner.test.ts`
- `node-tests/json.test.ts`
- `node-tests/performance-tools.test.ts`
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
  the import/read plan. The accepted-plan authorization boundary compares
  decimal/exponent tokens exactly without IEEE-754 coercion and distinguishes
  booleans from numbers. Existing plan-classification and migration-parity
  callers retain their established Python numeric-equality contract. No
  deployment Apply path changed.

## Tests Run

- `npm run typecheck`
- `npm run build`
- `npm run build:test`
- Focused Oracle, state projection, Adopt artifact, performance-tool, and
  Python-disabled operational-runtime tests before review: 36 passed, 0
  failed.
- `python3 -m engine.audit_vendor_boundary`: 187 allowed matches, 0 violations.
- `npm audit --audit-level=high`: 0 vulnerabilities.
- `git diff --check`: passed.
- Initial final gate at `f0df44e`: `npm test`: 1,111 tests; 1,110 passed; 1
  skipped; 0 failed.
- Patch-focused remediated gate at `bbf5645`: typecheck, production build,
  test build, and all 92 direct/transitive callers covering Oracle, exact and
  compatibility JSON equality, plan classification, projection policy,
  assessment guidance, Transform differentials, retained adoption artifacts,
  and performance evidence passed; `git diff --check` passed.
- Evidence-remediation gate at `241f518`: typecheck and test build passed; 45
  focused Oracle, exact/compatibility equality, plan-classification, and
  performance-evidence tests passed after the first edit, then all 6
  performance-tool tests passed after the diagnostic expectation correction;
  `git diff --check` passed.
- Tests not run: live credentials, provider/API/backend calls, live
  plan-versus-state comparison, provider test suite (no provider change), and
  deployment Apply. These are forbidden on this machine or deferred to the
  approved work-machine benchmark.

## Review Findings and Remediation

The fresh-context review of `04f32ac..f0df44e` requested changes for four
blocking findings. All four were accepted and remediated in `bbf5645`:

1. Distinct decimal tokens could compare equal after JavaScript number
   rounding. The accepted-plan gate now uses a dedicated exact-decimal JSON
   comparison; a losslessly parsed `9007199254740992.0` versus
   `9007199254740993.0` plan is rejected. The established Python-compatible
   equality used elsewhere is unchanged.
2. The A/B comparator trusted caller labels. `--oracle-ab` now binds the two
   fixed variant labels to the state source recorded by Adopt, displays the
   observed source, and validates scratch-Apply/state-show command evidence.
3. The fresh-worktree benchmark had no executable candidate runtime. It now
   requires an immutable checksum-verified runtime tree plus trusted build
   attestation binding its digest to the exact candidate commit; no work-side
   npm, TypeScript, `node_modules`, or Python is required.
4. The artifact manifest included Terraform runtime state. It is now captured
   before staging/init/plan and covers deterministic generated inputs only;
   saved plans, fingerprints, provider installations, state, and assessments
   remain separate private evidence.

Accepted non-blocking review improvements also remove synthetic addresses from
new accepted-plan diagnostics and directly assert that the corrected-plan
Terraform command count falls from six to four.

The first patch re-review verified those six corrections, then found three
remaining evidence-composition gaps. They are remediated in `241f518`:

1. Oracle provenance is accepted only on successful, zero-command
   `oracle.state_source` spans, with exactly matching scratch-Apply and
   state-show evidence for every resource family. Misplaced, missing,
   duplicated, or truncated evidence fails.
2. The benchmark compares the packaged checksum with the SHA-256 from the
   trusted build attestation, then runs the verifier committed at `IW_HEAD`;
   the attestation's commit and digest must come from the same record.
3. The final A/B comparator is invoked from the detached `IW_HEAD` worktree,
   never from the caller's current checkout.

The exact-decimal, manifest-timing, corrected-command-count, and diagnostic
privacy fixes were explicitly verified by that re-review. Final patch-only
confirmation of the three evidence changes is pending.

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
