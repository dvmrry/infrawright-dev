# Block C2 Plan Lifecycle and Reference-Backend Review Handoff

## Intent

- Port `plan-lifecycle.ts` and its narrow `reference-backend.ts` prerequisite to Go.
- Preserve the exact saved-plan lifecycle: deterministic root selection, init/plan sequencing, input freshness checks, safe saved-artifact finalization/cleanup, and reviewed non-secret cross-state backend environment projection.
- Keep assessment contract/evidence/report/CLI, transport/fetch, existing generated config, and Node behavior unchanged.

## Base / Head

- Base: `98682a739af92011beb71ddd54872aac23860e3f`
- Head: uncommitted working-tree parcel; review only the four files listed below against the base.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/plan/lifecycle.go`, `go/internal/plan/lifecycle_test.go`, `go/internal/plan/reference_backend.go`, `go/internal/plan/reference_backend_test.go`.
- Files intentionally left untouched: plan fingerprint/evidence/contract; assessment/report; CLI; artifacts substrate; Node oracle sources; fixtures and schemas.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: existing loaded pack/root APIs were consumed without modification.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/plan-lifecycle.ts`; `node-src/domain/reference-backend.ts`; `node-tests/plan-lifecycle.test.ts`; existing Go `roots`, `tfrender`, `terraformcmd`, `artifacts`, `canonjson`, and C1 fingerprint APIs.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: None.
- Demo or lab outputs: saved `tfplan.sources` bytes are tested, not checked in.
- Artifact drift intentionally expected: None; `tfplan.sources` must be exactly `{"sha256": "<hex>", "version": 2}\n`.

## Expected Delta

- Expected behavior change: Go gains plan/clean lifecycle APIs, Terraform adapter construction, backend preflight, and safe cross-state backend environment projection.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: runtime Go can create/remove saved-plan artifacts; no tracked artifact changes.
- Expected no-op areas: assessment/report/adopt; contract validation; fetch/transport; existing generated config; Node CLI.

## Invariants Claimed

- Evidence must not be silently dropped: saved plans are finalized only after pre/post fingerprints agree; stale, missing, failed, or partially written artifacts are cleaned.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: generated config, var files, backend config, cross-state inputs, and C1 fingerprint inputs retain source order and are rechecked at the same serial barriers as Node.
- Ambiguity must stay classified instead of being coerced to success: partial grouped config, missing backend config, missing Terraform output, and changed init/plan inputs fail closed.
- Provider-readiness counts must stay explainable: planned/removed results count roots, not individual files.
- Adoption safety invariants: old saved pairs are removed before save preflight where Node does so; imports-only roots skip before deletion; non-save runs preserve saved pairs; later-root failure does not roll back earlier completed roots.

## Tests Run

- Commands: focused lifecycle/reference tests repeated 10 times; plan package `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; `git diff --check`; original compiled Node plan-lifecycle suite.
- Relevant output summary: Go gates passed; compiled Node suite passed 18/18; zero third-party Go dependencies.
- Tests not run and why: no real cloud/provider API call applies. Terraform execution uses a fake adapter in Go tests; the existing Node oracle suite was run.

## Known Deferrals

- Deferred work: CLI wiring and exit classification; assessment contract complete gate remains downstream; real Terraform end-to-end checkpoint is outside this parcel.
- Reason it is safe to defer: these APIs are not reachable from the Go CLI until C4; contract acceptance remains separately enforced before assessment.
- Follow-up owner or trigger: C4 CLI after C2/C3 acceptance; existing final checkpoint for real Terraform.

## Review Focus

- Highest-risk files or paths: `PlanEnvironmentRoots`; save finalization/cleanup; cross-state rechecks; reference-backend allowlist and JSON bytes.
- Specific assumptions to attack: exact Terraform argv/environment precedence; save/non-save and imports-only deletion order; no-config/group partial behavior; four freshness failures; chmod/write/missing-output cleanup; symlink and size behavior in backend config; secrets never enter accepted config or diagnostics.
- Source evidence the reviewer should verify: both Node source files and `node-tests/plan-lifecycle.test.ts` directly.
- Generated artifacts the reviewer should compare: exact `tfplan.sources` bytes and file modes; post-failure file manifest.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: accepting stale inputs; leaving one half of a failed saved pair; deleting existing artifacts on a non-save run; following an unsafe config without the source-prescribed rule; accepting credential fields; reordering var files or Terraform calls.
