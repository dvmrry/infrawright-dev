# Block C1 Saved-Plan Evidence Review Handoff

## Intent

- Port `node-src/domain/plan-evidence.ts` to Go on top of the accepted fingerprint and stable-file substrate.
- Bind a saved plan, its raw source-fingerprint file, current source inputs, a private snapshot, and exact snapshot identity across prepare/recheck/cleanup barriers.
- Preserve serial budget/failure precedence and scrub private snapshots safely without unlinking or redirectable path cleanup.
- Keep fingerprint production code, lifecycle, assessment/report, CLI, transport/fetch, and Node behavior unchanged.

## Base / Head

- Base: `98682a739af92011beb71ddd54872aac23860e3f`
- Head: uncommitted working-tree parcel; review only the eight evidence files listed below.
- Diff command: inspect each new evidence file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/plan/evidence.go`, `evidence_test.go`, `evidence_cleanup_posix.go`, `evidence_cleanup_posix_test.go`, `evidence_cleanup_unsupported.go`, `evidence_identity_darwin.go`, `evidence_identity_linux.go`, `evidence_identity_unsupported.go`.
- Files intentionally left untouched: fingerprint production/tests; contract/lifecycle; assessment/report; CLI; artifacts; Node oracle sources; fixtures/schemas.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: N/A.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`.
- Other source evidence: `node-src/domain/plan-evidence.ts`; `node-tests/plan-evidence.test.ts`; assessment/apply call sites; existing Go `artifacts`, `canonjson`, `procerr`, and accepted C1 fingerprint APIs.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: runtime private saved-plan snapshot behavior is implemented/tested; no tracked snapshot files.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None; four standing artifact byte gates remain identical.

## Expected Delta

- Expected behavior change: Go gains saved fingerprint reading, evidence preparation/recheck, and exact-object idempotent snapshot cleanup.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: None tracked; runtime private snapshot is truncated and fsynced, never unlinked, during cleanup.
- Expected no-op areas: fingerprint bytes/production; Node sources; lifecycle/assessment/report/CLI; transport/fetch.

## Invariants Claimed

- Evidence must not be silently dropped: all five stale/change classes are checked at the source-prescribed serial barriers; raw fingerprint bytes and current recomputation both matter.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: captured input paths/arrays and file digests are defensively copied; raw fingerprint-file formatting changes count even when parsed value is equal.
- Ambiguity must stay classified instead of being coerced to success: inactive/forged/copied evidence, changed snapshot identity, unsupported cleanup platform, and dual preparation/cleanup failures fail closed.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: exact pointer binding; original/fingerprint same-byte replacement accepted per Node; snapshot inode replacement rejected; cleanup verifies directory and descriptor identity, truncates+fsyncs, and never unlinks.

## Tests Run

- Commands: focused plan tests; repeated attack/budget tests 20 times; focused `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; diff checks; four artifact differential gates; Linux and unsupported FreeBSD cross-compile; `go list -m all`.
- Relevant output summary: all passed; artifact gates had no skips; zero third-party dependencies.
- Tests not run and why: no real provider/API test applies; evidence uses temporary local fixtures and the accepted frozen fingerprint authority.

## Known Deferrals

- Deferred work: assessment/apply orchestration consumption and CLI wiring.
- Reason it is safe to defer: this parcel only exposes the binding primitive and has no production CLI entry yet.
- Follow-up owner or trigger: C3 assessment and later exact-apply/adopt block after their authorization.

## Review Focus

- Highest-risk files or paths: prepare/recheck operation order; exact-object active binding; composite cleanup; POSIX descriptor cleanup; unsupported-platform failure.
- Specific assumptions to attack: numeric version `2` spellings; absolute lexical `..`; same-byte replacement distinctions; raw fingerprint formatting mutation; all five invalidations; exact budget charges/failure precedence; concurrent/repeated cleanup; directory/snapshot/symlink/FIFO replacement; no-unlink guarantee; error redaction and categories.
- Source evidence the reviewer should verify: `plan-evidence.ts` and its direct tests/call sites, not this handoff.
- Generated artifacts the reviewer should compare: four standing byte gates and cleanup file manifest/content.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: using structural rather than exact-pointer binding; accepting a copied evidence value; inode-binding original/fingerprint contrary to Node; failing to inode-bind snapshot; path-based unlink/truncate; changed source after one barrier; cleanup error replacing the primary failure; unsupported target accidentally succeeding.
