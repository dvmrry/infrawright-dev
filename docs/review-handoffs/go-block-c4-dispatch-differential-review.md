# Block C4 Dispatch and Differential Review Handoff

## Intent

- Finish the credential-free Block C command surface by wiring the already
  reviewed `plan`, `clean-plans`, `assert-clean`, and `assert-adoptable`
  command functions into the Go entry point.
- Preserve Node's pre-dispatch Terraform-platform gate, including the shallow
  standalone-help exception.
- Add a deliberately bounded direct Go-versus-Node differential: one saved
  plan/cleanup round trip, one byte-exact clean assessment report, and five
  inexpensive parse/exit cases.
- Keep real provider/API work and broader Block C/D behavior out of scope.

## Base / Head

- Base: `6d48af435bf98ecfc391bcdd6076e182770fc2aa`
- Head: uncommitted working tree
- Diff commands:
  - `git diff -- go/cmd/iw/main.go`
  - `git diff --no-index /dev/null go/cmd/iw/block_c4_differential_test.go`
- SHA-256 after the bounded review fix:
  - `go/cmd/iw/main.go`: `850256eca91c167782a6a0014ad74b111e6d57544c750e068bd5b22dcf9d8e83`
  - `go/cmd/iw/block_c4_differential_test.go`: `0f204f3e1fca2fc02e99e8e2b8da142db3884be8d739435b4855e1530b58aad5`

## Files Changed

- Files:
  - `go/cmd/iw/main.go`
  - `go/cmd/iw/block_c4_differential_test.go`
- Files intentionally left untouched: command implementations, plan and
  assessment packages, Terraform runner, HTTP/transport/filesystem layers,
  Node source/tests, dependencies, fixtures, and generated artifacts.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: a temporary synthetic pack is used only by the differential.
- Existing docs or design records:
  - `docs/go-runtime-v2.md`
  - `docs/review-handoffs/go-block-c-plan-lifecycle.md`
  - `docs/adversarial-review.md`
- Other source evidence:
  - `node-src/cli/main.ts`, especially the Terraform option/flag sets,
    `hasStandaloneTerraformHelp`, `requiresTerraformExecution`, and main
    dispatch.
  - The frozen `dist/infrawright-cli.mjs` Node oracle.
  - Previously reviewed Go command, lifecycle, assessment, and Terraform
    packages at the base.

## Generated Artifacts

- Reports: a temporary `assert-clean --report` output is compared byte for
  byte with the Node oracle.
- Schemas: none.
- Fixtures: only mode-restricted temporary test fixtures.
- Snapshots: temporary `tfplan` and `tfplan.sources` files are compared byte
  for byte; neither is committed.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: none. Only the private randomized
  assessment-snapshot pathname in the fake-Terraform argv transcript is
  normalized. Exit status, stdout, stderr, report bytes, and every other argv
  line remain exact.

## Expected Delta

- Expected behavior change: the four Block C commands become reachable from
  Go production dispatch. Terraform-capable commands take the source-pinned
  platform gate before parsing unless their arguments contain standalone help;
  `clean-plans` remains ungated.
- Expected report/count/coverage changes: none outside the new direct test.
- Expected generated-output changes: none.
- Expected no-op areas: all non-Block-C command behavior, including loud
  pre-cutover handling for still-unported commands.

## Invariants Claimed

- Evidence must not be silently dropped: saved plan, fingerprint, assessment
  report, and Terraform argv are compared directly with the Node oracle.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: production dispatch calls
  the already reviewed source-bound command implementations without changing
  their inputs.
- Ambiguity must stay classified instead of being coerced to success: the
  existing legacy command shim still owns usage/failure classification.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: the `complete === true`, evidence binding,
  freshness, and report contracts remain in the reviewed downstream packages;
  this patch adds no bypass.
- The drift-policy `projection_omit_if` internal marker must not enter REPORT
  bytes. The direct clean REPORT remains byte-exact against Node; the distinct
  marker-producing condition is pinned by the existing
  `TestProjectionScopeMarkersNeverEnterReportBytes` lower-layer test.
- No HTTP, filesystem, process, or runtime emulation layer and no dependency
  are added.

## Tests Run

- Commands:
  - focused Block C4 corpus
  - `go test -race ./cmd/iw -count=1`
  - `go test ./... -count=1`
  - `go vet ./...`
  - Windows amd64 compile via `go test -c`
  - `gofmt` and `git diff --check`
- Relevant output summary: all passed. The direct differential verifies exact
  plan/save/clean artifacts and streams, exact assessment REPORT bytes, and
  five parse/exit cases against the frozen Node bundle, including a
  mode-distinguishing `assert-adoptable --policy ... --help` case.
- Tests not run and why:
  - No live API/provider test: explicitly outside this credential-free parcel.
  - No broad new direct corpus: existing reviewed command/internal tests cover
    blocked, tolerated, adoptable, no-plan, invalid-selector, and missing-
    Terraform semantics.
  - No unsupported-platform runtime execution: the predicate is table-tested
    and the CLI cross-compiles for Windows.

## Known Deferrals

- Deferred work: the real read-only provider/API checkpoint and all later
  blocks not authorized by Block C.
- Reason it is safe to defer: this parcel consumes only local inputs and fake
  Terraform; it adds no provider request path.
- Follow-up owner or trigger: explicit user authorization and credentials for
  the already-defined live singleton leg.

## Review Focus

- Highest-risk files or paths: `go/cmd/iw/main.go` and the report comparison in
  `go/cmd/iw/block_c4_differential_test.go`.
- Specific assumptions to attack:
  - The shallow help scan consumes option values exactly like Node, including
    treating `--help` as a value when it follows a value-taking option.
  - Platform gating occurs before parsing even for still-unported Terraform
    commands, but never for standalone help or `clean-plans`.
  - Dispatch through `legacyPlanLifecycleCommand` retains usage exit `2` and
    failure classification.
  - Assessment argv normalization can hide only the private randomized
    snapshot pathname, not executable, ordering, flags, CWD, or other drift.
- Source evidence the reviewer should verify: the cited Node functions and
  dispatch, plus the existing Go legacy shim and command functions.
- Generated artifacts the reviewer should compare: exact `tfplan`,
  `tfplan.sources`, and REPORT bytes exercised by the focused corpus.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  help after unknown/positional tokens, missing option values, state-aware
  staging, `modules generate`, and accidental REPORT marker leakage.
