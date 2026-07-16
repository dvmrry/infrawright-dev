# Final-stack Qualification Refinements Review Handoff

## Intent

- Close the two accepted final-head refinements specific to rollout honesty and
  the installed `iw` compatibility surface.
- Define a conservative allowlisted pre-production cross-state cohort, state
  the unbound referent-state dependency explicitly, and prove that `npm pack`
  installs working `iw` and `infrawright` aliases for the same bundle.
- Preserve the existing no-build runtime proof and make no operational engine,
  provider, state, plan, assessment, or Apply semantic change.

## Base / Head

- Base: `99a5fc2a0a8d3435ab848d444dc2cc05f1dacd0c`
- Implementation head: `da7078493f5554dc6acd16dd82142a0d258f1095`
- Exact rebased head used for the final local gates:
  `da7078493f5554dc6acd16dd82142a0d258f1095`
- Review diff: `git diff 99a5fc2a0a8d3435ab848d444dc2cc05f1dacd0c..HEAD`
- This handoff correction is the only change after the exact-head local gates.

## Files Changed

- Files:
  - `README.md`
  - `docs/integration-validation.md`
  - `docs/node-process-api.md`
  - `docs/operational-runtime.md`
  - `docs/provider-labs/cross-state-reference-qualification.md`
  - `docs/provider-probes.md`
  - `node-src/cli/main.ts`
  - `node-tests/cli-bundle.test.ts`
  - `package-lock.json`
  - `package.json`
  - `scripts/test-runtime-release.mjs`
  - `scripts/verify-runtime-release.mjs`
  - this review handoff
- Files intentionally left untouched: packs, providers, Terraform topology,
  saved-plan contracts, assessment, and exact Apply.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: the already reviewed ZPA reference declarations and ZIA
  version-scoped unsupported classification; unchanged here.
- Existing docs or design records: the cross-state qualification runbook,
  integration validation guide, Zscaler adoption quirk inventory, and runtime
  release smoke.
- Other source evidence: accepted independent cumulative-stack review and the
  current `package.json` `bin` mapping.

## Generated Artifacts

- Reports: N/A.
- Schemas: N/A.
- Fixtures: N/A.
- Snapshots: N/A.
- Demo or lab outputs: a disposable npm tarball and installation prefix created
  and deleted by the runtime release smoke.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: `iw` is the canonical operational CLI name while
  `infrawright` remains a compatibility alias for the same bundled entrypoint.
  The release smoke now requires the packed artifact to contain the CLI bundle
  and checksum, installs it through the configured build registry into a
  temporary prefix, and proves both aliases return identical canonical `iw`
  help. The separate stripped-runtime smoke continues to prove that executing
  the shipped bundle needs neither npm nor Python.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: CLI usage and diagnostics now spell the
  canonical command `iw`; Terraform and adoption artifacts are unchanged.
- Expected no-op areas: every shipped runtime operation other than its command
  spelling and all Terraform behavior.

## Invariants Claimed

- Evidence must not be silently dropped: known live gaps remain explicit gates
  rather than being generalized into a production-readiness claim.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: the cohort admits only
  declared reference pairs and does not infer provider-name relationships.
- Ambiguity must stay classified instead of being coerced to success: every
  pair outside the table remains excluded; version-scoped unsupported objects
  retain resource-level fail-closed behavior.
- Provider-readiness counts must stay explainable: no counts change.
- Adoption safety invariants: saved referrer plans are explicitly documented as
  not bound to a referent state version; serialized use and plan invalidation
  are mandatory until dependency binding or an engine-owned lock exists.

## Tests Run

- Commands:
  - `node --check scripts/test-runtime-release.mjs`
  - `node scripts/test-runtime-release.mjs`
  - `npm run check:all`
  - `make check`
  - `git diff --check`
- Relevant output summary: on exact rebased head `da70784`, full Node 1,284
  passed, 0 failed, 1 skipped; repository gate 850 passed, 0 failed; pack
  validation and vendor-boundary audit passed. The exact-archive runtime release
  smoke also passed at `da70784`, including temporary installation through the
  configured registry and both aliases. The only subsequent edit is this
  handoff correction; the reviewer should rerun the focused runtime smoke on
  the final handoff head.
- Tests not run and why: live final-head cross-state qualification remains the
  downstream pre-production gate and requires approved credentials/backend;
  no live provider/backend/Apply was authorized here.

## Known Deferrals

- Deferred work: exact-release-head scalar qualification, a destroyed-workspace
  no-op rerun, and the three indexed-list ZPA pairs including invalid-index
  cases.
- Reason it is safe to defer: no pair is labeled supported before those exact
  gates pass; cross-state mode remains opt-in.
- Follow-up owner or trigger: approved downstream pre-production qualification.
- Deferred work: dependency state lineage/serial/object-version/output binding
  or an engine-owned referent/referrer transaction lock.
- Reason it is safe to defer: the initial cohort requires concurrency one, no
  intervening referent mutation, short-lived plans, and immediate dependent
  plan invalidation.
- Follow-up owner or trigger: before promotion beyond the bounded pilot.

## Review Focus

- Highest-risk files or paths:
  `docs/provider-labs/cross-state-reference-qualification.md` and
  `scripts/test-runtime-release.mjs`.
- Specific assumptions to attack: the cohort does not overstate old live
  evidence; every known named exclusion remains excluded; documentation does
  not imply dependency-state binding; npm installation remains build/test-only;
  the stripped no-npm/no-Python runtime smoke is unchanged.
- Source evidence the reviewer should verify: current pack-declared reference
  pairs, provider 4.7.26 holds, and the package `bin` mapping.
- Generated artifacts the reviewer should compare: packed file listing must
  contain `dist/infrawright-cli.mjs` and its checksum.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  an undeclared pair appearing supported, an indexed pair inheriting scalar
  evidence, a changed referent surviving plan reuse, an unsafe npm-reported
  filename, missing packed runtime files, divergent alias output, or npm/Python
  becoming a stripped-runtime dependency.
