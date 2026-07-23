# Builder Review Handoff: Oracle Exact-Import Diagnostics

## Intent

- Make an exact-import Oracle refusal actionable without weakening the
  import-only scratch-Apply gate.
- Point operators to the existing explicit retained-workdir diagnostic path.
- Stop echoing an unexpected plan-supplied resource address.
- Record a private live report of a provider-scoped DLP template limitation
  without treating it as reproducible evidence or an engine defect.

## Base / Head

- Base: `ece2b0c5efa00c2afb2e537aa20733a0bd93cee0`
- Head: `1b544da0bf549f378cce662d8d9603047d59596d`
- Diff command: `git diff ece2b0c5efa00c2afb2e537aa20733a0bd93cee0..1b544da0bf549f378cce662d8d9603047d59596d`

## Files Changed

- `node-src/domain/import-oracle.ts`
- `node-tests/import-oracle.test.ts`
- `docs/zscaler-adoption-quirk-inventory.md`
- Files intentionally left untouched: Oracle plan authorization conditions,
  generated-config policy, state projection, Apply, pack overrides, provider
  code, provider schema, and deployment topology.

## Source Inputs Consulted

- Provider schemas: committed ZIA 4.7.26 schema only for resource/field
  orientation; no schema behavior was changed.
- OpenAPI/API contracts: none.
- Provider source files: none; the DLP item remains reported live evidence.
- Pack metadata: current ZIA registry and overrides, read-only.
- Existing docs or design records: `docs/import-oracle.md` retained-workdir
  warning and `docs/zscaler-adoption-quirk-inventory.md` evidence levels.
- Other source evidence: an operator screenshot of a private live run reported
  the opaque Oracle refusal and the DLP dollar-placeholder amplification. No
  raw tenant values, commands, or reproducible artifacts were retained here.

## Generated Artifacts

- Reports: none generated.
- Schemas: none.
- Fixtures: synthetic plan evidence is inline in the focused Node tests.
- Snapshots: none.
- Demo or lab outputs: none committed.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: exact-import-plan refusals append the existing
  `INFRAWRIGHT_KEEP_ORACLE=1` recovery hint. Unexpected plan-supplied addresses
  render as `<unexpected address>`.
- Expected report/count/coverage changes: one reported provider-limitation row.
- Expected generated-output changes: none.
- Expected no-op areas: exact-plan authorization, scratch Apply reachability,
  cleanup/retention semantics, provider projection, artifacts, and plans.

## Invariants Claimed

- Evidence must not be silently dropped: the original Oracle error category is
  retained and the safety gate still rejects the same plans.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  or source precedence changed.
- Source precedence/provenance must remain explicit: DLP evidence is marked
  reported/private and below the reproducible evidence bar.
- Ambiguity must stay classified instead of being coerced to success: no
  provider workaround or adoption support is inferred from the report.
- Provider-readiness counts must stay explainable: no readiness count changed.
- Adoption safety invariants: no create, update, replace, destroy, drift,
  malformed, or non-exact plan can reach scratch Apply.

## Tests Run

- `npm run build:test`
- `node --test .node-test/node-tests/import-oracle.test.js`: 23 passed.
- `npm run typecheck`: passed.
- `git diff --check`: passed.
- Tests not run: live provider qualification and full Node suite; neither is
  required to validate a diagnostic-only error-message change before review.

## Known Deferrals

- Deferred work: schema-whitelisted top-level drift attribute names.
- Reason it is safe to defer: raw generic paths can expose tenant-controlled
  map keys; the requested minimum retained-workdir hint is safe and complete.
- Follow-up trigger: only after a schema-backed, bounded, value-free design is
  justified by continuing operator pain.
- Deferred work: downstream's five local ZIA overrides and deployment/docs
  edits.
- Reason it is safe to defer: those changes are not present in any local
  worktree; upstream must inspect their exact diff rather than infer them from
  a screenshot.

## Review Focus

- Highest-risk files or paths: `assertImportOnlyBatchPlan` error construction
  and the reported-evidence inventory row.
- Specific assumptions to attack: whether the hint can leak any plan data,
  whether an unexpected address remains observable, whether the safety gate or
  scratch Apply reachability changed, and whether the DLP row overclaims.
- Source evidence the reviewer should verify: existing keep-workdir warning,
  focused fake-Terraform transaction, and evidence-level preamble.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  wrong import ID, unexpected address, sensitive values, nested tenant map
  keys, update actions, and missing reproducible provider evidence.
