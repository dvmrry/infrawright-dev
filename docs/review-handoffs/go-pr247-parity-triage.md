# Go PR 247 Parity Triage Review Handoff

Review status: **Approve**. The fresh-context result is recorded in
`docs/review-handoffs/go-pr247-parity-triage-review.md`. Its sole forward
watch-item is to re-confirm during block C that the internal duplicate-scope
marker never enters plan-report bytes.

## Intent

- Reconcile merged PR 247 (`3e3fa3bb419966a3f81e304118509c3f88ae1d5d`)
  into `feature/go-canonjson-foundation` without rewriting the previously
  qualified branch history.
- Carry the two PR 247 behaviors that already have Go runtime kernels: permit
  CI-sized Terraform child environments and align Go drift-policy validation
  with the new lossless numeric source authority.
- Do not invent the third behavior, pack/user adoption-policy merging, before
  the Go Adopt block is authorized and implemented.
- Preserve every existing argument/output/environment-byte bound, policy
  source order, artifact byte contract, and credential-free checkpoint gate.

## Base / Head

- Base: `821e9b4c251c10af333990460b88f29793f4865e` (`Merge main after
  downstream adoption fixes`), which merges
  PR 247 and no other main-only change into the feature branch.
- Head/candidate: `5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1` on
  `feature/go-canonjson-foundation`. A later branch tip may update only the
  evidence and work-machine handover documents.
- Diff command:
  `git diff 821e9b4c251c10af333990460b88f29793f4865e..5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1`.

## Files Changed

- Runtime:
  - `go/internal/metadata/driftpolicy.go`
  - `go/internal/terraformcmd/api.go`
- Tests:
  - `go/internal/metadata/driftpolicy_runtime_test.go`
  - `go/internal/terraformcmd/runner_test.go`
  - `go/internal/terraformcmd/validation_test.go`
- Review handoff:
  - `docs/review-handoffs/go-pr247-parity-triage.md`
- Files intentionally left untouched: Go command breadth, Adopt orchestration,
  pack/user policy loading and merge, provider adapters, pack metadata,
  schemas, generated artifacts, Terraform output/redaction logic, and live API
  behavior.

## Source Inputs Consulted

- Provider schemas: none.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: existing pack drift-policy validation consumers; no pack data
  changed.
- Existing docs or design records: `docs/go-runtime-v2.md`,
  `docs/adversarial-review.md`, and
  `docs/review-handoffs/downstream-adoption-blockers.md` from PR 247.
- Other source evidence:
  - PR 247 implementation commit
    `a96746ed2b599779fedffa052c8f2950d40c73dd`.
  - `node-src/io/terraform-command.ts` and its 4,096-entry/256-KiB contract.
  - `node-src/domain/drift-policy.ts` functions
    `isSupportedDriftPolicyVersion`, `numericScalarMarker`, and
    `jsonScalarMarker`.
  - Existing Go `canonjson.TerraformJSONExactlyEqual`, which already ports the
    Node exact-decimal version comparison without binary64 rounding.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: ignored Node bundle and checksum rebuilt twice from the
  merged source. Bundle SHA-256:
  `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`;
  checksum-file SHA-256:
  `df3709d7ab96761792ee6557d12c315351db83ee69fbf78bc0bed79a9ac45946`.
- Artifact drift intentionally expected: the ignored Node oracle bundle changes
  because PR 247 changes bundled Node source. RootCatalog, Transform, Topology,
  Generation, committed demo artifacts, and generated module/env bytes must not
  drift and did not drift.

## Expected Delta

- Expected behavior change:
  - Go Terraform commands accept up to 4,096 environment entries while keeping
    the existing 256-KiB aggregate byte cap and all key/value validation.
  - Go drift policies accept exact lossless numeric spellings of version one,
    retain lossless scalar values, reject precision-rounded near-one versions,
    and treat numerically equivalent `projection_omit_if` scopes such as `0`
    and `0.0` as duplicates.
- Expected report/count/coverage changes: two focused Go regressions; no report
  schema or readiness-count change.
- Expected generated-output changes: none.
- Expected no-op areas: fetch, transform, module/env generation, catalog,
  Terraform command arguments/output/timeouts/redaction/process isolation,
  policy matching/stale accounting, and all provider behavior.

## Invariants Claimed

- Evidence must not be silently dropped: lossless policy scalars remain
  `json.Number` values after validation and defensive snapshotting.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: the Go change follows the
  merged PR 247 Node authority. Pack/user adoption-policy merge precedence is
  not claimed or implemented in Go.
- Ambiguity must stay classified instead of being coerced to success: exact
  decimal equality accepts `10e-1` but rejects
  `1.0000000000000000000000001`; booleans and strings remain invalid versions;
  equivalent numeric duplicate scopes fail closed.
- Provider-readiness counts must stay explainable: N/A; no readiness evidence
  or counts change.
- Adoption safety invariants: the environment byte limit remains 256 KiB;
  4,097 entries remain invalid; non-finite native numeric policy values remain
  invalid; no Go adoption merge, Oracle authorization, import, plan, or Apply
  path is changed.

## Tests Run

- Commands:
  - Focused Go Terraform/drift-policy tests selecting the new regressions and
    adjacent validation vectors.
  - `npm ci`.
  - `npm run build:metadata-cli` twice plus SHA-256 comparison.
  - `node scripts/verify-runtime-release.mjs "$(pwd)"`.
  - The four named Go-vs-Node RootCatalog, Transform, Topology, and Generation
    differential gates.
  - `npm run build:test && node --test` for the PR 247 drift-policy,
    adopt-runner, and Terraform-command suites.
  - `INFRAWRIGHT_V2_CHECKPOINT=1 go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v`.
  - `go test -count=1 ./...`.
  - `npm run check:all`.
  - `make check-all`.
  - `test -z "$(gofmt -l .)"`, `go vet ./...`, and the Go pre-review script
    with `--force` because `golangci-lint` is not installed.
- Relevant output summary:
  - Both oracle builds were byte-identical and the runtime verifier passed all
    11 profiles.
  - All four differential gates passed with no skips in 12.163 seconds.
  - The three focused Node files passed 55 tests with no failures or skips.
  - The hermetic checkpoint passed in 9.13 seconds. Candidate SHA-256:
    `419e966397a15fe4b4df9240fde8b021ffcb0e865e039258f13f779a1553d4f4`;
    provider lock SHA-256:
    `5e2d47060a6a1e562a8cdf923cc60035b41700e1b3474943b6b2dff2ce9abb21`.
  - Full Go passed. Full Node passed 788 tests, failed zero, and skipped the two
    known optional external tests. `make check-all`, gofmt, and vet passed.
  - An additional Go marker corpus passed integral/fractional equivalent
    spellings, signed zero, unsafe integers, scalar-type separation, unequal
    fractions, rounded-float separation, and non-finite-native rejection.
- Tests not run and why: the real read-only provider comparison was not run;
  this triage is credential-free. `golangci-lint` is not installed. Fresh
  adversarial review has not run; this handoff is the builder stop for it.

## Known Deferrals

- Deferred work: Go pack/user adoption-policy merging equivalent to PR 247's
  `mergePolicyData` and `loadAdoptionPolicy` regression.
- Reason it is safe to defer: this branch still forbids block C and has no Go
  Adopt orchestration or file-backed adoption-policy loader in which to place
  that behavior. Inventing a disconnected merge helper would create dead code
  and false confidence. Node retains the reviewed production behavior.
- Follow-up owner or trigger: when §5 passes and block C is explicitly
  authorized, port the Node pack-first/user-second merge and its wide-number
  regression as part of the actual Go Adopt slice.
- Deferred work: live read-only provider proof and final full-evidence review.
- Reason it is safe to defer: no cutover or block-C/D acceptance claim is made.
- Follow-up owner or trigger: the existing §5 process.

## Review Focus

- Highest-risk files or paths: `go/internal/metadata/driftpolicy.go`, especially
  numeric scalar markers and exact version acceptance; secondarily the retained
  environment byte bound in `go/internal/terraformcmd`.
- Specific assumptions to attack:
  - Every Node numeric-marker equivalence class maps to one Go marker without
    collapsing booleans, strings, null, or unequal finite values.
  - Integer tokens beyond JavaScript's safe range stay exact, while fractional
    lossless tokens follow Node's intentional binary64 marker behavior.
  - Signed zero, exponent spellings, huge exponents, non-finite native floats,
    and 4,096/4,097 environment boundaries remain fail-safe.
  - The missing Go adoption merge is clearly deferred rather than silently
    claimed.
- Source evidence the reviewer should verify: PR 247 commit `a96746e`, its
  handoff and three Node regressions, Go `canonjson` exact equality, and the
  changed Go tests.
- Generated artifacts the reviewer should compare: the post-247 Node bundle
  hashes and the four fresh differential transcripts; no committed generated
  artifact should differ.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  exact-decimal near-one versions, unsafe integers, integral float spellings,
  signed zero, marker collisions between scalar types, excessive environment
  bytes hidden inside an allowed entry count, and accidentally treating this
  parity maintenance as authorization for Go Adopt work.
