# Block C3 Assessment Transaction Review Handoff

## Intent

- Port the saved-plan assessment transaction from `plan-assessment.ts` to Go.
- Preserve policy/context/evidence precedence, per-root and final recheck barriers, Terraform show/classification/guidance integration, result ceilings, partial-root error reporting, and secure cleanup.
- Keep input/eval/policy/guidance/report implementations, runner/CLI, plan primitives, and Node sources unchanged.

## Base / Head

- Base: committed dependency tip `814f6dc` (including the approved report API).
- Head: uncommitted working-tree parcel; review only the assessment transaction and cleanup files listed below.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/assessment/assessment.go`, `assessment_test.go`, `assessment_cleanup.go`, `assessment_cleanup_darwin.go`, `assessment_cleanup_linux.go`, `assessment_cleanup_unsupported.go`, and `assessment_cleanup_unsupported_test.go`.
- Files intentionally left untouched: inputs/eval/policy/guidance/report/semantics/report I/O; plan/control-evidence; runner/CLI/docs; Node sources/fixtures.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: accepted guidance/policy APIs consumed through package seams.
- Existing docs or design records: Block C handoff and accepted parcel handoffs.
- Other source evidence: `node-src/domain/plan-assessment.ts`; `node-tests/plan-assessment.test.ts`; accepted Go inputs/eval/policy/guidance/control/evidence/contract/report APIs.

## Generated Artifacts

- Reports: success/error report objects built through the report parcel API; no tracked outputs.
- Schemas: None changed.
- Fixtures: None changed.
- Snapshots: runtime private saved-plan snapshots/temp directory only.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains assessment transaction/result/failure APIs and report wrapper.
- Expected report/count/coverage changes: none versus Node; partial roots and phase-specific errors are preserved.
- Expected generated-output changes: None tracked.
- Expected no-op areas: input/materialization, classification/policy/guidance/report bytes, plan evidence, runner/CLI.

## Invariants Claimed

- Evidence must not be silently dropped: four evidence rechecks per completed root and two final evidence-policy-context windows precede finalization.
- Generic matcher evidence must not outrank source-backed evidence: classification consumes validated saved plans, bound source fingerprints, policy, and control evidence.
- Source precedence/provenance must remain explicit: policy preflight precedes context; roots sort by tenant-NUL-label; findings retain source and resource types; partial completed roots remain in failures.
- Ambiguity must stay classified instead of being coerced to success: `complete:false`, mutations, ceiling violations, asynchronous finalizers, cleanup failures, and directory swaps fail closed.
- Provider-readiness counts must stay explainable: result/root/finding/guidance/resource-type ceilings are enforced at source-defined barriers.
- Adoption safety invariants: prepare/show/recheck/classify ordering; private temp identity; ordered scrub/no-unlink cleanup; primary+cleanup composite preservation; redacted public error phase/kind/message.
- Portable cleanup tradeoff: snapshot contents are descriptor-truncated and synced, then the bound directory is inspected through `os.Root` for the exact regular, zero-length, dev/inode-matching manifest. No pathname-based unlink or recursive removal occurs. Successful runs deliberately retain the private `0700` directory and zero-length snapshot inodes rather than risk deleting a replacement path.

## Tests Run

- Commands: focused verbose; focused repeat 10 times; focused race; full Go; vet; gofmt/diff checks; zero-dependency listing; Node build and plan-assessment suite.
- Relevant output summary: focused/repeated/race/package/full-repository/vet/formatting and cross-compilation passed against approved report commit `814f6dc`; Node 17/17; exact 64-MiB logical control integration completes in 0.09s under 30s and timeout follow-up is pinned.
- Tests not run and why: no live provider/API call; fake Terraform show transaction mirrors Node focused suite.

## Known Deferrals

- Deferred work: runner/CLI integration, a bounded trusted-janitor or equivalent remnant policy, and the real read-only provider leg.
- Reason it is safe to defer: transaction files remain uncommitted and unreachable from the Go CLI; retained remnants contain no plan bytes and operational adoption is explicitly gated.
- Follow-up owner or trigger: before runner/CLI adoption, choose and test an owner for the retained scrubbed-inode lifecycle so repeated assessments cannot accumulate unbounded temporary-directory inodes.

## Review Focus

- Highest-risk files or paths: preflight/validation order; root transaction barriers; final double window; partial-root failure; cleanup composite; result ceilings; report wrapper.
- Specific assumptions to attack: option snapshot before awaits; policy-before-context/zero roots; tenant-NUL-label order; complete:false mapping; exact counts of control/evidence/policy/context rechecks; mutation timing; four evidence reads; finalizer must be synchronous; guidance degrades only where Node does; cleanup always runs in order and preserves primary details; 64-MiB two-pass deadline behavior.
- Source evidence the reviewer should verify: Node assessment source/tests and every consumed accepted/in-review Go seam directly.
- Generated artifacts the reviewer should compare: partial/success/error report objects through report builder; cleanup file manifest; no final bytes owned here.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: finalizing after stale context; losing an already completed root; building a success report after cleanup failure; accepting async finalizer; skipping an evidence barrier; surfacing raw policy/path/secret errors; deleting redirected snapshot paths; exceeding ceilings after report allocation.
