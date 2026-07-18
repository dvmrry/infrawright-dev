# Block C3 Saved-Plan Report Review Handoff

## Intent

- Port saved-plan assessment report construction, schema-v1 structural/semantic validation, and report I/O to Go.
- Preserve exact REPORT JSON bytes, error-detail ordering, guidance joining/sorting/dedup, status/count derivation, and safe atomic/stdout writes.
- Keep assessment orchestration/runner/CLI, plan code, source metadata, and Node oracle artifacts unchanged.

## Base / Head

- Base: committed dependency tip `a6d6b4a`.
- Head: uncommitted working-tree parcel; review only the six files listed below.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/assessment/report.go`, `report_test.go`, `semantics.go`, `semantics_test.go`, `report_io.go`, `report_io_test.go`.
- Files intentionally left untouched: eval/policy/guidance; assessment inputs/orchestration/runner; CLI; plan/control-evidence; Node sources; frozen fixtures; JSON schema.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: guidance values consumed through the accepted guidance API.
- Existing docs or design records: `docs/review-handoffs/go-block-c-plan-lifecycle.md`; `docs/schemas/saved-plan-assessment.schema.json`.
- Other source evidence: `node-src/domain/plan-report.ts`; `node-src/contracts/saved-plan-assessment-semantics.ts`; `node-src/io/assessment-report.ts`; `node-tests/plan-report.test.ts`; frozen `python-plan-report-v1.json`; existing Go canonical artifact renderer/procerr.

## Generated Artifacts

- Reports: exact clean, tolerated, blocked, and error report construction/bytes are tested.
- Schemas: existing schema-v1 hand-ported; schema file unchanged.
- Fixtures: existing frozen Python report fixture consumed unchanged; SHA-256 `df9d09b903bf60d34ad567f213bd1ddbb1e8bf2aaf1fc71c49be9a050a3e343c`.
- Snapshots: None.
- Demo or lab outputs: temporary atomic/stdout report writes in tests.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains report object construction, v1 validation/semantics, exact rendering, and atomic/stdout output APIs.
- Expected report/count/coverage changes: none versus the frozen Node/CPython authority.
- Expected generated-output changes: runtime Go emits the same Python-compatible report bytes; no tracked output changes.
- Expected no-op areas: policy/eval/guidance behavior; orchestration/CLI; plan lifecycle/evidence; Node artifacts/schema.

## Invariants Claimed

- Evidence must not be silently dropped: roots/findings/guidance/stale policy/error phases are structurally and semantically validated before rendering.
- Generic matcher evidence must not outrank source-backed evidence: final report consumes classified findings and accepted guidance; joining requires exactly one blocked finding path.
- Source precedence/provenance must remain explicit: finding source/order, guidance lane/order, policy hash, plan fingerprint, and stale-policy identity remain in contract positions.
- Ambiguity must stay classified instead of being coerced to success: structural/semantic contradictions fail with ordered bounded details; status/count inconsistencies reject.
- Provider-readiness counts must stay explainable: summary counts/status are derived and cross-validated against roots.
- Adoption safety invariants: success/error phase separation; blocked/tolerated/clean consistency; exact policy evidence rules; no internal guidance sort key or drift scope marker may enter report bytes.

## Tests Run

- Commands: focused report corpus repeated three times; focused `-race`; `go test -count=1 ./...`; `go vet ./...`; `gofmt`; zero-dependency listing; Node report oracle.
- Relevant output summary: all passed; Node 9/9; all three status report bytes and `1.0` provenance match frozen fixture; marker non-leak exponent case passes.
- Tests not run and why: no live API/provider call applies; report construction is fixture/domain driven.

## Known Deferrals

- Deferred work: assessment transaction/runner/CLI integration.
- Reason it is safe to defer: report APIs are not yet reachable from the Go CLI; this parcel fully validates its own objects and bytes.
- Follow-up owner or trigger: remaining C3 transaction/runner parcels after adversarial acceptance.

## Review Focus

- Highest-risk files or paths: schema subset/error ordering; semantic append order; report object/key/array ordering; guidance join/sort/dedup/deep clone; error phase; atomic I/O sanitization.
- Specific assumptions to attack: missing/conditional schema behavior; 64-error truncation; structural-before-semantic diagnostics; clean/tolerated/blocked aggregate branches; root/member/finding uniqueness; policy evidence and stale entries; arbitrary guidance values/depth; concrete/schema path formatting; `1.0`/unsafe numbers; exact trailing newline; stdout versus file error boundaries.
- Source evidence the reviewer should verify: all three Node sources, schema, focused Node tests, frozen fixture, and canonical renderer directly.
- Generated artifacts the reviewer should compare: full exact bytes for all frozen reports and `1.0`; schema contradiction detail order; temporary file mode/content/atomic behavior.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: deduping non-identical guidance; leaking `sort_key` or projection scope marker; accepting inconsistent counts/status; losing findings/guidance during normalization; changing array order while sorting object keys; sanitizing a render failure that Node exposes or leaking filesystem paths Node sanitizes.
