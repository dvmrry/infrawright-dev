# Task 5 Builder Review Handoff: Data-Only Referents

## Intent

- What problem does this change solve?

  It closes the five blocking findings in the Task 5 review for data-only
  referent evidence, state-aware absent-state rendering, contract coverage,
  and lifecycle acceptance.

- What user-visible or maintainer-visible behavior should change?

  Loaded reference-output metadata now carries an explicit managed/data
  evidence kind into saved-plan assessment. A managed contract authorizes only
  managed instances; a data contract authorizes only exact data instances with
  scalar IDs. A tokenized data referent whose state probe is unusable renders a
  lookup-only resolver and no unusable remote-state data block. Data roots are
  accepted by the real imports-only planning lifecycle.

- What behavior must stay unchanged?

  State-blind environment generation remains byte-identical for the existing
  generated/data characterization, generated referents keep their resolver
  behavior, and the managed branch's pre-existing loose address/ID parsing is
  intentionally unchanged.

## Base / Head

- Base: `19d3c6d0` (`Publish data-referent transform artifacts through the shared lifecycle`)
- Head: uncommitted working tree on `claude/data-only-referents`
- Diff command: `git diff 19d3c6d0 --`

## Files Changed

- Files:

  - `go/internal/plan/contract.go`
  - `go/internal/plan/contract_test.go`
  - `go/internal/plan/contract_data_referent_test.go`
  - `go/internal/plan/lifecycle_test.go`
  - `go/internal/plan/testdata/offline_remote_state_capture/{main.tf,data/main.tf,referent_state.json,show.json}`
  - `go/internal/assessment/{inputs.go,assessment.go,exact_plan_apply.go}`
  - `go/internal/assessment/{inputs_test.go,exact_plan_apply_test.go,data_referent_test.go}`
  - `go/internal/envgen/environment_generator.go`
  - `go/internal/envgen/data_referent_test.go`
  - this handoff

- Files intentionally left untouched:

  - Concurrent Task 6 scope: `go/internal/roots`, `go/internal/refedges`,
    `go/internal/envgen/reference_topology.go`, and `cmd/iw`.

## Source Inputs Consulted

- Provider schemas: The existing provider-neutral test schemas and loaded pack
  metadata used by the plan, assessment, and envgen fixtures.
- OpenAPI/API contracts: None; this change does not alter provider mapping.
- Provider source files: None; this change consumes existing registry metadata
  and Terraform plan shapes.
- Pack metadata: `data_referent` registry metadata and `OutputsByRoot`
  materialization paths.
- Existing docs or design records: The full Task 5 review report,
  `docs/superpowers/plans/2026-08-01-data-only-referents.md`, and the last four
  commits in the repository.
- Other source evidence: A real Terraform 1.15.4 offline
  `terraform show -json` capture produced with the builtin
  `terraform_remote_state` data source and a local state fixture.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: `go/internal/plan/testdata/offline_remote_state_capture/show.json`
  and its Terraform source/state inputs.
- Snapshots: None.
- Demo or lab outputs: None committed; ignored Terraform/cache working files
  were not staged.
- Artifact drift intentionally expected: The state-aware absent-data case
  intentionally removes that referent's remote-state block and changes its
  binding to lookup-only. State-blind bytes are not expected to drift.

## Expected Delta

- Expected behavior change: Evidence authorization is mode-bound and exact for
  data resources; absent tokenized data state is rendered plan-safely; data
  roots participate in imports-only planning.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: Only the state-aware unusable-data-state
  case changes from `try(remote, lookup)` plus a remote-state block to a
  lookup-only expression with no corresponding block.
- Expected no-op areas: Managed evidence parsing, generated referent fallback,
  state-blind environment bytes, and unrelated Task 6 work.

## Invariants Claimed

- Evidence must not be silently dropped: A declared output type must have one
  explicit managed/data kind; data resource evidence must match its type, name,
  mode, exact address, index, and scalar ID shape.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or source-operation logic changes.
- Source precedence/provenance must remain explicit: N/A; no source precedence
  changes.
- Ambiguity must stay classified instead of being coerced to success: N/A; no
  ambiguity classifier changes.
- Provider-readiness counts must stay explainable: N/A; no readiness/count
  changes.
- Adoption safety invariants: A missing data referent state cannot be hidden
  behind a Terraform `try()` around a data block that Terraform must read
  before evaluating the expression.

## Tests Run

- Commands:

  - `go test ./internal/plan/ ./internal/assessment/ ./internal/envgen/ -count=1`
  - `gofmt -l` over every Go file under `go` (clean)
  - `git diff --check`

- Relevant output summary:

  - `internal/plan`: pass
  - `internal/assessment`: pass
  - `internal/envgen`: pass

- Focused regression and pre-fix/unsafe-mutation proof:

  - A mode-agnostic contract mutation failed the mode authorization test at
    the managed-contract/data-instance case (`error = nil`, expected
    unauthorized-mode refusal); the restored mode-bound implementation passes
    all managed/data/mixed/empty/valid cases.
  - A faithful old DATA parser mutation failed the exactness test: it accepted
    address/index mismatch, trailing address material, and wrong top-level
    name; boolean/object/array IDs reached output-mismatch validation instead
    of being rejected as invalid instances. The restored exact parser passes
    the expanded matrix.
  - A mutation restoring the old `probe != nil && !tokensPresent` gate failed
    the state-aware absent-data test: probe calls were `0` instead of `1`, the
    binding retained `try(data.terraform_remote_state...)`, and `main.tf`
    retained the remote-state block. The restored exception passes.
  - The focused plan tests include empty-data valid and wrong-mode cases and a
    positive fixture derived from the real offline Terraform capture.
  - The lifecycle test runs `PlanEnvironmentRoots` with `ImportsOnly: true` and
    asserts one initialization, one plan, no imports file, and the saved-plan
    artifact pair.

- Promotion efficiency: Not measured; no full-corpus sweep was run. The
  requested focused package gate was run after the independent mutation
  proofs.
- Tests not run and why: The repository-wide full corpus was not run; the
  requested validation scope is `internal/plan`, `internal/assessment`, and
  `internal/envgen`, and concurrent Task 6 files were outside this worker's
  scope.

## Known Deferrals

- Deferred work: Tightening the managed branch's existing prefix-based
  address/index parsing and its string-only ID rule.
- Reason it is safe to defer: The review explicitly requires preserving the
  managed branch semantics in this task and treating that hardening as a
  separate follow-up. The new DATA branch has independent exact parsing and
  scalar validation.
- Follow-up owner or trigger: A separate review/task for managed evidence
  parser hardening.

## Review Focus

- Highest-risk files or paths: `go/internal/plan/contract.go`,
  `go/internal/assessment/inputs.go`,
  `go/internal/envgen/environment_generator.go`, the offline Terraform
  capture, and the focused adversarial tests.
- Specific assumptions to attack:

  - Every loaded `OutputsByRoot` type has matching resource metadata and a
    boolean `data_referent` registry value.
  - The exact DATA address is the Terraform canonical form using
    `strconv.Quote(index)`.
  - Terraform data IDs exposed through the lossless JSON parser are strings or
    `json.Number`.
  - Removing the surviving DATA remote-state reference after rewriting is
    sufficient to prevent an unusable data block from rendering.

- Source evidence the reviewer should verify: The loaded metadata projection,
  the `ReferenceOutputType` contract, the exact DATA address/name/mode checks,
  the tokenized data probe exception and reference-list rebuild, and the real
  Terraform `show -json` capture's `prior_state` resource shape.
- Generated artifacts the reviewer should compare: The data-only absent-state
  `main.tf` and `expression_bindings.tf`, the state-blind data/generated
  normalized binding bytes, and `show.json`.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  mixed managed/data instances, empty opposite-mode configuration, forged
  address/index pairs, trailing address material, malformed IDs, wrong
  top-level resource names, duplicate keys, and an unusable data state with
  committed tokens.
