# Builder Review Handoff: Downstream Adoption Blockers

## Intent

- Allow generic Terraform adoption to run on CI agents whose inherited child
  environment contains more than 256 entries while retaining the existing
  aggregate environment byte bound.
- Allow file-backed adoption drift policies to retain losslessly parsed JSON
  numbers without rejecting the version, weakening numeric duplicate checks,
  or dropping active pack policy during the merge.
- Preserve the default Oracle diagnostic redaction contract, Terraform command
  argument/output bounds, pack-policy precedence, stale-entry accounting, and
  every non-adoption policy-loading path.

## Base / Head

- Base: `6c6bf2bae159a5ee9857d39e4f15367666c7bbd9` (`origin/main`)
- Head: `agent/fix-downstream-adoption-blockers` (review the branch tip)
- Diff command: `git diff 6c6bf2bae159a5ee9857d39e4f15367666c7bbd9..agent/fix-downstream-adoption-blockers`

## Files Changed

- Runtime: `node-src/io/terraform-command.ts`,
  `node-src/domain/drift-policy.ts`, `node-src/domain/adopt-runner.ts`.
- Tests: `node-tests/terraform-command.test.ts`,
  `node-tests/drift-policy.test.ts`, `node-tests/adopt-runner.test.ts`.
- Review handoff: `docs/review-handoffs/downstream-adoption-blockers.md`.
- Files intentionally left untouched: Oracle stderr/redaction behavior,
  provider adapters, pack metadata, schemas, generated artifacts, installers,
  Apply behavior, and pipeline configuration.

## Source Inputs Consulted

- Provider schemas: none.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: current pack drift-policy structure, including
  `packs/zia/pack.json`.
- Existing docs or design records: `docs/import-oracle.md`,
  `docs/adversarial-review.md`, and the review templates.
- Other source evidence: downstream evidence of 300-500-entry CI
  environments; the archived Python merge behavior at
  `154a743:engine/drift_policy.py`; current lossless parsing and exact Terraform
  numeric equality helpers.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none persisted; tests use temporary policy files and process
  environments.
- Snapshots: none.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: Terraform child environments permit up to 4,096
  entries but remain limited to 256 KiB; file-backed adoption policies accept
  exact numeric version-one spellings and lossless scalar values; active pack
  and user policy entries are both retained.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: only adoption output governed by a
  previously rejected file policy can now be produced.
- Expected no-op areas: saved-plan policy loading, pack-only policy loading,
  native policy values, Oracle authorization/redaction, provider state
  equality, Terraform argv/output bounds, and all generated evidence.

## Invariants Claimed

- Evidence must not be silently dropped: pack policy remains present after a
  valid lossless user-policy merge; a focused regression asserts this.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: pack entries are merged
  before user entries using the existing order; neither source is replaced.
- Ambiguity must stay classified instead of being coerced to success: version
  values that only round to one are rejected; booleans and strings remain
  invalid.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: numeric conditional-omit duplicate scopes retain
  Terraform number semantics, including integral-equivalent spellings; the
  environment byte cap and all environment-key/value validation remain active.

## Tests Run

- `npm ci`
- `npm run build:test`
- `node --test .node-test/node-tests/drift-policy.test.js .node-test/node-tests/adopt-runner.test.js .node-test/node-tests/terraform-command.test.js`
- `npm run check`
- `node --test .node-test/node-tests/collector-authority.test.js .node-test/node-tests/operational-runtime-smoke.test.js .node-test/node-tests/zscaler-generic-fetch.test.js`
- `npm test`
- `git diff --check`
- Relevant output summary: 55 focused tests passed. The first full check
  typechecked successfully and reached 790 tests, but six unrelated copied-pack
  checks found a pre-existing ignored `_shared/zscaler/__pycache__`. After that
  cache was moved out of the checkout, all seven affected tests passed and the
  clean full suite passed 788 tests with two platform/external skips and zero
  failures.
- Tests not run and why: live downstream CI and provider adoption were not run;
  repository validation is credential-free and directly reproduces both
  pre-Terraform failures.

## Known Deferrals

- Deferred work: opt-in raw Oracle stderr diagnostics and safer propagation of
  pre-spawn failure reasons.
- Reason it is safe to defer: this change removes the two deterministic
  adoption blockers without weakening the existing default secret-redaction
  boundary.
- Follow-up owner or trigger: a separate upstream diagnostics design issue.

## Review Focus

- Highest-risk files or paths: `node-src/domain/drift-policy.ts` and
  `node-src/domain/adopt-runner.ts`.
- Specific assumptions to attack: exact numeric version-one comparison does
  not accept booleans, strings, or precision-rounded near-one values; version
  normalization cannot discard pack policy; scalar markers agree with the
  existing Terraform numeric equality contract; 4,096 entries cannot bypass
  the 256 KiB bound.
- Source evidence the reviewer should verify: archived Python merge order,
  `parseDataJsonLosslessly`, `terraformJsonExactlyEqual`, and
  `terraformJsonEqual` numeric behavior.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  `0` versus `0.0`, signed zero, exponent spellings, integers beyond JavaScript
  safe range, non-finite native numbers, invalid policy versions, conflicting
  pack/user entries, and environments at both count and byte boundaries.
