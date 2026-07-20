# Block C3 Assessment Inputs Review Handoff

## Intent

- Port generic and loaded-pack assessment input materialization/resolution from `plan-assessment-inputs.ts` to Go.
- Preserve immutable caller snapshots, deterministic root/member/var-file/reference-output order, exact path rules, deferred validation, control-file copying distinctions, and redacted context rechecks.
- Keep evaluation/policy/guidance/report/transaction/runner/CLI, plan code, and Node behavior unchanged.

## Base / Head

- Base: committed tip `a6d6b4a`.
- Head: uncommitted working-tree parcel; review only `go/internal/assessment/inputs.go` and `inputs_test.go`.
- Diff command: inspect each new file with `git diff --no-index /dev/null <file>`.

## Files Changed

- Files: `go/internal/assessment/inputs.go`, `go/internal/assessment/inputs_test.go`.
- Files intentionally left untouched: other assessment files; plan/control-evidence; runner/CLI; docs; Node sources/fixtures.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: loaded deployment/catalog/root metadata through existing APIs.
- Existing docs or design records: Block C handoff.
- Other source evidence: `node-src/domain/plan-assessment-inputs.ts`; `node-tests/plan-assessment-inputs.test.ts`; relevant context-mutation tests in `plan-assessment.test.ts`; existing roots/envgen/tfrender/controlevidence APIs.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None changed.
- Snapshots: None.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains generic/loaded assessment root materializers and recheck closures.
- Expected report/count/coverage changes: None yet.
- Expected generated-output changes: None.
- Expected no-op areas: report/evaluation/guidance/transaction/CLI; plan lifecycle/evidence; Node sources.

## Invariants Claimed

- Evidence must not be silently dropped: only selected roots with existing saved plans materialize; control-file evidence is copied with source-defined fields.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: root/member/var-file/reference-output order and generic-versus-loaded control identity handling are preserved.
- Ambiguity must stay classified instead of being coerced to success: invalid format/overlay/workspace/path/context mutation fail with exact source errors/redaction.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants: mutable deployment/catalog/options are snapshotted at source-defined times; generic captures before await, loaded retains its source-defined after-await asymmetry; re-materialization failures collapse to `ASSESSMENT_CONTEXT_CHANGED`.

## Tests Run

- Commands: focused/package tests; focused repeated 20 times; focused race; full Go; vet; gofmt; zero-dependency listing; Node focused inputs oracle.
- Relevant output summary: all passed; Node 7/7.
- Tests not run and why: no live provider/API call applies.

## Known Deferrals

- Deferred work: assessment transaction/runner/CLI integration.
- Reason it is safe to defer: input APIs are not yet reachable from production Go CLI.
- Follow-up owner or trigger: assessment transaction parcel consumes accepted input resolvers.

## Review Focus

- Highest-risk files or paths: snapshot/copy timing; root construction/order/equality; generic versus loaded controls; deferred format validation; relative path resolution; redacted recheck closure.
- Specific assumptions to attack: absent/json/hcl/null/invalid tfvars formats; validation only when a saved plan is selected; overlay type; absolute workspace; generic config paths versus loaded transform paths; full-topology reference-output types; missing vs empty ordering; caller mutation across await; loaded timing asymmetry; optional identity/followSymlinks copy.
- Source evidence the reviewer should verify: Node source/tests and existing Go root/path/control APIs directly.
- Generated artifacts the reviewer should compare: exact materialized option structs/order; no report bytes yet.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence: sorting caller arrays that must retain order; validating invalid format too early with zero plans; retaining mutable maps/slices; resolving absolute paths relative to workspace; preserving identity in generic mode or dropping it in loaded mode contrary to source; leaking re-materialization errors.
