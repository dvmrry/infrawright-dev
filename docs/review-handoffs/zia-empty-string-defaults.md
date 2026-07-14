# Builder Review Handoff: ZIA Empty-String Defaults

## Intent

- Remove provider-observed empty-string sentinels from adopted tfvars for ZIA
  Firewall Network Service `tag` and Browser Control
  `plugin_check_frequency`.
- Keep strict JSON default matching unchanged; do not equate `null` and `""`.
- Preserve every nonempty value and all unrelated pack behavior.
- Give the approved downstream environment a bounded requalification procedure
  for the corrected fields and the separately observed stale ZIA root.

## Base / Head

- Base: `a6430677a5bde8c5968b76ab1354a399193a628e`
- Head: `7a078d30ce490d3a62e16f8641bc3d5905a0120d`
- Diff command: `git diff a6430677a5bde8c5968b76ab1354a399193a628e..7a078d30ce490d3a62e16f8641bc3d5905a0120d`

## Files Changed

- `packs/zia/overrides/zia_firewall_filtering_network_service.json`
- `packs/zia/overrides/zia_browser_control_policy.json`
- `node-tests/adopt-runner.test.ts`
- `node-tests/generic-transform-core.test.ts`
- `node-tests/state-project.test.ts`
- `node-tests/metadata-loader.test.ts`
- `node-tests/transform-runtime-artifacts.test.ts`
- `tests/test_transform.py`
- `docs/provider-labs/zia-adoption-followup-runbook.md`
- Files intentionally left untouched: Node production code, Python production
  code, provider schema, ZIA registry, drift policy, ZPA metadata, generated
  modules, Terraform roots, and the runtime bundle.

## Source Inputs Consulted

- Provider schemas: committed ZIA 4.7.26 schema; Network Service `tag` is
  Optional+Computed and Browser Control `plugin_check_frequency` is Optional.
- OpenAPI/API contracts: none.
- Provider source files: no provider fork or provider change. The downstream
  evidence observed both fields as empty strings after provider Read.
- Pack metadata: exact committed ZIA overrides and registry.
- Existing docs or design records: the Zscaler adoption follow-up runbook and
  its returned authority hashes/counts.
- Other source evidence: downstream pre-change adopted-tfvars counts were 18
  for `tag=""` and 1 for `plugin_check_frequency=""`; both Adopt commands
  exited zero. Raw, Transform, generated-before, and generated-after counts
  were zero and are not claimed as positive policy execution evidence.

## Generated Artifacts

- Reports: no tenant report committed.
- Schemas: none.
- Fixtures: no tenant or provider-state fixture committed; sanitized objects
  are inline in tests.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: corrected Adopt tfvars omit the two
  empty-string fields; all nonempty values remain.

## Expected Delta

- Expected behavior change: active pack metadata drops the exact `""`
  sentinel in Transform, generated-config policy when present, and final Adopt
  provider-state projection.
- Expected report/count/coverage changes: committed override count 72 -> 73;
  downstream adopted-tfvars counts should change 18 -> 0 and 1 -> 0.
- Expected generated-output changes: only objects carrying the exact two empty
  strings lose those fields.
- Expected no-op areas: `null`, nonempty strings, Network Service port ends,
  matcher semantics, provider behavior, Terraform execution, and ZPA.

## Invariants Claimed

- Evidence must not be silently dropped: each rule is restricted to one
  schema-writable field and exact empty-string equality.
- Generic matcher evidence must not outrank source-backed evidence: no generic
  matcher change was made.
- Source precedence/provenance must remain explicit: runbook binds the complete
  CLI digest and pack hashes before rerunning Adopt.
- Ambiguity must stay classified instead of being coerced to success: zero raw
  counts do not distinguish absent, null, or another value; stale materialized
  roots remain separate from current topology.
- Provider-readiness counts must stay explainable: override inventory count is
  updated from 72 to 73.
- Adoption safety invariants: no deployment Apply is authorized; only the
  existing exact import-only local Oracle transaction may run downstream.

## Tests Run

- `npm run build:test`
- Focused Node suites for Adopt, Transform, state projection, metadata loading,
  and active override compilation: 75 passed.
- Focused Python override/Oracle/projection/Adopt suites: 14 passed.
- `make check-pack`: passed.
- `npm run typecheck`: passed.
- `npm test`: 1,155 passed, 2 expected override-count assertions failed after
  the new file increased the inventory; both assertions were corrected and
  their two complete suites then passed 41/41. No unrelated failure occurred.
- `git diff --check`: passed.
- Tests not run: live provider qualification; delegated to the approved work
  environment by the updated runbook.

## Known Deferrals

- Deferred work: downstream fresh-lane Adopt rerun for both resources.
- Reason it is safe to defer: credentials and tenant evidence are unavailable
  in repository CI; the candidate is exact-field metadata plus synthetic
  provider-state coverage.
- Follow-up owner or trigger: approved downstream operator; acceptance is
  adopted-tfvars counts 0/0 with Adopt exit zero.
- Deferred work: ZPA source-less automatic grouping correction.
- Reason it is safe to defer: separate topology/state-key surface, unrelated
  to these two ZIA values.

## Review Focus

- Highest-risk files or paths: the two ZIA overrides and final Adopt artifact
  regression test.
- Specific assumptions to attack: whether `""` is the exact observed provider
  state, whether either field can be required, whether nonempty values survive,
  and whether the runbook overclaims raw or generated-policy evidence.
- Source evidence the reviewer should verify: strict Node/Python equality,
  schema status, downstream count interpretation, and pack loader wiring.
- Generated artifacts the reviewer should compare: the sanitized Adopt tfvars
  test output only.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  explicit raw `null`, nonempty strings, zero generated-HCL matches, stale root
  materialization, and a mismatched CLI digest.
