# Block C3 Assessment Guidance Review Handoff

## Intent

- Port `node-src/domain/assessment-guidance.ts` to Go.
- Produce explanatory, informational-only guidance for blocked plan findings from loaded-pack manifest evidence, preserving all three lanes, eligibility, ordering inputs, path expansion, and lossless values.
- Keep final report joining/sorting/exact-entry dedup, assessment orchestration, schemas, runner/CLI, plan code, and Node sources unchanged.

## Base / Head

- Base: current committed dependency tip `2e14d94` (original Block C parcel base `98682a739af92011beb71ddd54872aac23860e3f`).
- Head: uncommitted working-tree parcel; review only the two files listed below.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/assessment/guidance.go`, `go/internal/assessment/guidance_test.go`.
- Files intentionally left untouched: existing eval/policy; assessment orchestration/report/semantics/runner; CLI; plan/control-evidence; Node oracle sources; fixtures/schemas.

## Source Inputs Consulted

- Provider schemas: loaded pack metadata/manifests only; no live schemas changed.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: manifest provider-config, absent-default, and dynamic-schema guidance records, including real Google/AWS/Cloudflare pack vectors.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/assessment-guidance.ts`; `node-tests/assessment-guidance.test.ts`; `plan-report.ts` guidance integration; existing Go eval/path/equality and metadata loader APIs.

## Generated Artifacts

- Reports: None generated in this parcel; returned groups will later enter report construction.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains immutable guidance-source snapshots and blocked-finding guidance collection.
- Expected report/count/coverage changes: None yet; report joining/dedup is deferred.
- Expected generated-output changes: None tracked.
- Expected no-op areas: assessment report bytes; orchestration/runner/CLI; plan lifecycle/evidence; Node behavior.

## Invariants Claimed

- Evidence must not be silently dropped: malformed records are isolated according to source behavior; valid guidance from other lanes/manifests remains available.
- Generic matcher evidence must not outrank source-backed evidence: guidance is emitted only from reviewed pack manifest records that match blocked source-backed finding paths.
- Source precedence/provenance must remain explicit: all three lanes and their provider/resource/path evidence remain distinct in returned entries.
- Ambiguity must stay classified instead of being coerced to success: guidance is informational only and never changes a blocked classification; invalid/unrepresentable entries fail closed or are isolated per source.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: Terraform bool/number distinction and lossless number tokens are preserved; `after_unknown`, drift findings, and concrete/schema path expansion follow Node; collector duplicates remain for report-owned exact-entry dedup.

## Tests Run

- Commands: focused Go tests; focused tests repeated 20 times; focused race; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; diff checks; `go list -m all`; Node build and focused guidance tests.
- Relevant output summary: all Go gates pass; zero third-party dependencies; Node guidance oracle 7/7; real-pack vectors pass.
- Tests not run and why: no live provider/API call applies; guidance is metadata/plan-fixture driven.

## Known Deferrals

- Deferred work: final report joining/sort/dedup, schema validation, orchestration, runner, and CLI.
- Reason it is safe to defer: guidance remains unreachable from production Go output until those parcels land; it cannot alter plan status.
- Follow-up owner or trigger: C3 report parcel must reuse `AssessmentGuidanceGroup` and own exact-entry dedup/report bytes.

## Review Focus

- Highest-risk files or paths: lane validation and matching; candidate-path expansion; lossless value cloning; group/entry shape; defensive metadata snapshots.
- Specific assumptions to attack: all three lanes; malformed record isolation; provider/resource/path vocab; duplicate/overlap rules; bool versus number; unsafe numeric lexemes; FEFF trimming; `resource_drift`; `after_unknown`; update+blocked eligibility; concrete/schema path cross-products; duplicate collector outputs intentionally retained.
- Source evidence the reviewer should verify: `assessment-guidance.ts`, focused Node tests, plan-report integration, and real pack metadata directly.
- Generated artifacts the reviewer should compare: returned full entry shapes/order and Node 7-case oracle; no final report bytes yet.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: emitting guidance for clean/tolerated findings; allowing guidance to change status; swallowing an entire source because one record is malformed; coercing booleans/numbers; deduping too early; retaining mutable pack maps/slices; leaking invalid raw metadata through errors.
