# Builder Review Handoff: Adopt Pack Default Omissions

## Intent

- Fix Adopt/Oracle handling when provider Read emits an optional sentinel that
  the same provider rejects in generated configuration. The reproduced case is
  `zia_url_filtering_rules.size_quota = 0`.
- Apply the pack's existing `drop_if_default` declaration to Terraform's
  generated import configuration before the corrected plan and to projected
  provider state before adopted tfvars are written.
- Preserve the provider as the configuration Oracle. Do not apply raw-API
  transforms such as renames, division, value maps, prefix stripping, or
  boolean inversion to provider-observed state.
- Preserve drift-policy matching, stale-entry accounting, Oracle import-only
  authorization, raw identity ownership, and artifact rendering.

## Base / Head

- Base: `6f2eedab4c89756f363b4267bf4f43a32ff3f2b4` (draft PR #212 head)
- Initial reviewed head: `792d43bf8188d6ce1fb52b87340701a074fb92c5`
- Remediated head: `a3c4c204744307a8a5f525ead669db66248a1190`
- Diff command: `git diff 6f2eedab4c89756f363b4267bf4f43a32ff3f2b4..a3c4c204744307a8a5f525ead669db66248a1190`

## Files Changed

- Runtime: `engine/adopt.py`, `engine/import_oracle.py`,
  `engine/state_project.py`, `node-src/domain/generated-config-policy.ts`,
  `node-src/domain/pull-transform.ts`, `node-src/domain/state-project.ts`.
- Tests: `tests/test_adopt.py`, `tests/test_import_oracle.py`,
  `tests/test_state_project.py`, `node-tests/generated-config-policy.test.ts`,
  `node-tests/import-oracle.test.ts`, `node-tests/state-project.test.ts`.
- Files intentionally left untouched: pack overrides and schemas, Transform
  behavior, Oracle authorization, artifact renderers, provider code, modules,
  roots, staging, plan assessment, Apply, and pipeline configuration.

## Source Inputs Consulted

- Provider schemas: committed schemas resolved for every generated resource;
  all 13 current `drop_if_default` paths were checked and are optional.
- OpenAPI/API contracts: none.
- Provider source files: none; provider behavior is supplied by the reproduced
  downstream Terraform failure and is simulated fail-closed in tests.
- Pack metadata: all committed `packs/*/overrides/*.json`, especially
  `packs/zia/overrides/zia_url_filtering_rules.json`.
- Existing docs or design records: `docs/adversarial-review.md` and its
  templates.
- Other source evidence: existing Python and Node Transform implementations,
  which define the authoritative `drop_if_default` comparison semantics.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none persisted; fake Terraform tests generate scratch HCL.
- Snapshots: none.
- Demo or lab outputs: none committed.
- Artifact drift intentionally expected: adopted tfvars omit only optional
  leaves whose provider-observed value matches a committed pack
  `drop_if_default` value.

## Expected Delta

- Expected behavior change: initial generated import configuration has matching
  optional sentinels removed, forcing the existing corrected-plan path; final
  provider state projection removes the same sentinels before artifact output.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: for affected Adopt resources only,
  matching sentinel leaves are absent rather than explicitly emitted.
- Expected no-op areas: Transform output, nonmatching values, resources without
  `drop_if_default`, drift-policy stale accounting, identities/import IDs,
  sensitive masks, Oracle import-only checks, and every non-Adopt workflow.

## Invariants Claimed

- Evidence must not be silently dropped: only committed pack defaults on
  schema-optional paths may be removed; non-optional paths fail closed.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: pack metadata is the sole
  source of the additional omission rule.
- Ambiguity must stay classified instead of being coerced to success: unknown
  generated HCL values are retained; unmatched values are retained.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: provider Read and import-only Terraform plan
  remain authoritative; raw API evidence still decides identity/import ID only;
  no remote mutation or deployment Apply is added.

## Tests Run

- `npm run typecheck -- --pretty false`
- `npm run build:test -- --pretty false`
- `node --test .node-test/node-tests/generated-config-policy.test.js .node-test/node-tests/import-oracle.test.js .node-test/node-tests/state-project.test.js .node-test/node-tests/adopt-runner.test.js`
- `python3 -m unittest tests.test_import_oracle tests.test_state_project tests.test_adopt`
- `git diff --check`
- Ad hoc schema audit: 13 current pack `drop_if_default` paths checked; zero
  non-optional paths.
- Relevant output summary before review: 54 focused Node tests passed; 99
  focused Python tests passed; typecheck and whitespace checks passed.
- Accepted review nit remediation: pack defaults now precede overlapping
  `projection_omit_if` entries at both generated-HCL and final-state boundaries,
  preserving consistent drift-policy stale accounting. The patch-focused gate
  passed 40 Node tests and 100 Python tests.
- Tests not run and why: full repository gates are deferred until accepted
  adversarial findings are remediated.

## Known Deferrals

- Deferred work: live tenant rerun of the reported ZIA URL filtering adoption.
- Reason it is safe to defer: repository acceptance is credential-free; the
  exact provider inconsistency is represented by fake Terraform failing the
  initial generated-config plan and accepting the corrected plan only after
  omission.
- Follow-up owner or trigger: downstream qualification after this branch is
  published.
- `ranges` metadata is not interpreted as a transform. Existing Transform does
  not use it to mutate values; `drop_if_default` is the operative pack rule.

## Review Focus

- Highest-risk files or paths: both generated-config policy implementations and
  both provider-state projection implementations.
- Specific assumptions to attack: omission happens at both boundaries; pack
  defaults use exact Transform comparison semantics; numeric collection indexes
  are ignored only for pack dotted paths; drift policy accounting is unchanged;
  required paths fail closed.
- Source evidence the reviewer should verify: `zia_url_filtering_rules.json`,
  Python `_matches_default`, Node `matchesTransformDefault`, and schema status
  for all current pack defaults.
- Generated artifacts the reviewer should compare: corrected fake generated HCL
  and final projected tfvars objects.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  bool-versus-number equality inherited from Transform, string/integer default
  coercion, repeated blocks, nonmatching values, unknown HCL expressions,
  policy ordering, and resources with no override.

## Review Result

- Fresh-context verdict on `792d43b`: Approve with nits; no blocking findings.
- Accepted nit: generated HCL previously ordered an overlapping
  `projection_omit_if` before the pack default, while final state projection
  applied the pack default first. No committed metadata overlapped, but future
  stale-policy accounting could diverge.
- Root cause: the generated-config entry list grouped all policy omissions
  before pack omissions instead of mirroring projection order.
- Fix: order generated-config removals as `projection_omit`, pack
  `drop_if_default`, then `projection_omit_if` in both runtimes.
- Regression: overlapping pack and conditional policy entries remove the value
  through the pack rule and leave the drift-policy entry stale in both Python
  and Node.
- Patch re-review target: `792d43b..a3c4c20`.
- Patch re-review verdict: Approve; no remaining findings. The reviewer verified
  both runtime orderings, preserved unconditional omission precedence, unchanged
  fill order, and meaningful stale-accounting regressions.
- Final gate: production build passed; complete `npm test` passed 1,139 tests
  with one platform skip and zero failures; vendor-boundary audit reported 187
  allowed matches and zero violations; whitespace remained clean.
