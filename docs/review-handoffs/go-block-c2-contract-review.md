# Block C2 Saved-Plan Contract Review Handoff

## Intent

- Port the saved-plan assessment contract from `node-src/domain/plan-contract.ts` to Go.
- Preserve the exact fail-closed validation semantics needed before plan classification, including `complete === true`, `errored === false`, the 100,000 combined change-record ceiling, Terraform numeric equality, and reference-output authorization.
- Keep Terraform JSON parsing, saved-plan evidence, lifecycle, assessment/report, CLI behavior, and all existing artifact bytes unchanged.

## Base / Head

- Base: `98682a739af92011beb71ddd54872aac23860e3f`
- Head: uncommitted working-tree parcel; review only the two files listed below against the base.
- Diff command: `git diff --no-index /dev/null go/internal/plan/contract.go` and `git diff --no-index /dev/null go/internal/plan/contract_test.go`

## Files Changed

- Files: `go/internal/plan/contract.go`, `go/internal/plan/contract_test.go`
- Files intentionally left untouched: all existing plan fingerprint files; evidence and lifecycle parcels; `go/internal/assessment/**`; CLI; Node oracle sources; fixtures; schemas.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/plan-contract.ts`; relevant contract cases in `node-tests/plan-eval.test.ts`; `node-tests/cross-state-terraform.test.ts`; existing `go/internal/canonjson` equality behavior; existing `go/internal/terraformcmd` plan decoding.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains `ValidateAssessmentPlan`, `AssessmentPlanError`, `AssessmentPlanContract`, and `MaxAssessmentChangeRecords` for Block C consumers.
- Expected report/count/coverage changes: None yet; assessment/report orchestration is not in this parcel.
- Expected generated-output changes: None.
- Expected no-op areas: Node behavior and sources; existing Go artifact bytes; transport/fetch; lifecycle/evidence/report/CLI.

## Invariants Claimed

- Evidence must not be silently dropped: malformed or incomplete plans fail closed before classification; reference-output changes require provider-observed authorization.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success: unsupported action sequences, unknown values, failed checks, importing ambiguity, and unauthorized outputs reject.
- Provider-readiness counts must stay explainable: the combined resource-change and drift ceiling is exactly 100,000.
- Adoption safety invariants: missing, null, false, numeric, or string `complete` never passes; only literal `true` passes. Boolean and number equality remain distinct under Terraform equality.

## Tests Run

- Commands: focused contract tests; focused tests repeated 20 times; focused `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; `git diff --check`.
- Relevant output summary: all passed; zero third-party Go dependencies.
- Tests not run and why: real Terraform cross-state test was not run; the parcel uses source-derived structural fixtures and does not invoke Terraform.

## Known Deferrals

- Deferred work: mapping `AssessmentPlanError` to the public assessment `ProcessFailure`, report integration, CLI integration, and lifecycle execution.
- Reason it is safe to defer: this parcel exposes the validator only; no production CLI path invokes it yet.
- Follow-up owner or trigger: C3 assessment orchestration after this API is accepted.

## Review Focus

- Highest-risk files or paths: `ValidateAssessmentPlan`; recursive masks; no-op equality; reference-output authorization; exact validation order around `complete` and `errored`.
- Specific assumptions to attack: missing versus null versus false; own-property requirements; 100,000/100,001 boundary; duplicate actions/types; create/update/no-op reference output; empty reference-module configuration authority.
- Source evidence the reviewer should verify: `node-src/domain/plan-contract.ts` directly, not this handoff.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: accepting `complete:false`; accepting unknown no-op masks; number/bool conflation; authorizing an engine output without observed IDs; accepting a same-looking but structurally incomplete reference module.
