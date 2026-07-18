# Block C3 Plan Evaluation and Policy Review Handoff

## Intent

- Port saved-plan classification and policy binding from `plan-eval.ts` and `plan-policy.ts` to Go.
- Preserve exact finding/status/order, lossless numeric comparison, drift-policy tolerance/stale accounting, and stable policy-file evidence/redaction.
- Keep assessment orchestration/report/guidance/runner, CLI, plan lifecycle/evidence, and Node sources unchanged.

## Base / Head

- Base: `98682a739af92011beb71ddd54872aac23860e3f`
- Head: uncommitted working-tree parcel consuming the concurrently added contract API; review only the five files listed below.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/assessment/doc.go`, `eval.go`, `eval_test.go`, `policy.go`, `policy_test.go`.
- Files intentionally left untouched: assessment orchestration/report/guidance/runner; CLI; plan package; control-evidence; Node oracle sources; fixtures/schemas.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: metadata drift-policy runtime API is consumed without modification.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/plan-eval.ts`; `node-src/domain/plan-policy.ts`; `node-tests/plan-eval.test.ts`; `node-tests/plan-policy.test.ts`; frozen `node-tests/fixtures/python-plan-eval-v1.json`; Go `plan.ValidateAssessmentPlan`, `canonjson`, `metadata`, and `artifacts`.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: existing frozen Python plan-eval fixture consumed unchanged; SHA-256 pinned as `83924f81dc073e2dc9fef5f20ec96331fa674db09de9ab3bfac9b8770df0eaf8`.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains validated plan classification and stable drift-policy binding/recheck APIs.
- Expected report/count/coverage changes: None yet; later report code will consume the returned findings/status/stale entries.
- Expected generated-output changes: None.
- Expected no-op areas: orchestration/report/guidance/CLI; plan lifecycle/evidence; Node sources; frozen fixtures.

## Invariants Claimed

- Evidence must not be silently dropped: contract validation precedes policy matching; partial tolerance retains unmatched paths; every match attempt still contributes stale-policy identity accounting.
- Generic matcher evidence must not outrank source-backed evidence: findings are derived from Terraform plan records and bound policy entries; policy never invents a clean result outside matching rules.
- Source precedence/provenance must remain explicit: resource changes precede resource drift; record order is retained; policy evidence binds raw sha/size and rechecks serially.
- Ambiguity must stay classified instead of being coerced to success: unknown/sensitive/opaque values, unsupported actions, and unmatched paths remain visible/blocked according to source behavior.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: validation before classification; delete/create precedence; import-only create handling; missing/null and bool/number distinctions; policy read/parse failures are redacted exactly where Node redacts them.

## Tests Run

- Commands: focused Go tests repeated 20 times; focused `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; `go list -m all`; `npm run build:test`; compiled Node plan-eval/plan-policy tests.
- Relevant output summary: 16/16 frozen CPython fixture cases pass; Node focused suite 20/20; all Go gates pass; zero third-party Go dependencies.
- Tests not run and why: no real provider/API call applies; classification is exercised against frozen and direct Terraform JSON fixtures.

## Known Deferrals

- Deferred work: assessment transaction, guidance, report/schema rendering, runner, and CLI.
- Reason it is safe to defer: this parcel exposes classification/policy primitives only and is not yet reachable through the Go CLI.
- Follow-up owner or trigger: remaining C3 parcels after this API is accepted.

## Review Focus

- Highest-risk files or paths: classification ordering and path diffing; tolerance matching/stale accounting; policy bind/recheck error boundaries.
- Specific assumptions to attack: validation occurs first; Python bool/int/float/unsafe-integer equality; missing/null; Unicode key order; string-index lexicographic order (`10` before `2`); identity/sensitivity/opaque sentinels; delete/create/import-only precedence; partial tolerance; all-match stale updates; policy BOM/no-follow/absolute/nil-path behavior; same-byte replacement; secret/path redaction.
- Source evidence the reviewer should verify: both Node source files, focused Node tests, and frozen fixture directly.
- Generated artifacts the reviewer should compare: all 16 frozen expected results and fixture SHA.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: classifying invalid plan before contract rejection; treating booleans as numbers; suppressing unmatched paths after partial tolerance; failing to count matched identities for stale policy; reordering drift vs changes; accepting policy mutation; leaking parser/source paths through generic failures.
