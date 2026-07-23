# Block C3 Saved-Plan Assertion Runner Review Handoff

## Intent

- Port `node-src/domain/plan-assessment-runner.ts` as a narrow Go
  orchestration layer over the accepted assessment primitives.
- Preserve policy-before-input/topology/Terraform ordering, zero-root lazy
  Terraform behavior, assessment diagnostics, report-before-failure ordering,
  and best-effort error-report publication.
- Keep CLI parsing, deployment loading, plan commands, provider/API behavior,
  transport, and dependencies unchanged.

## Base / Head

- Base: `202f5d7` (`Bind deployment evidence for assessment`).
- Head: uncommitted working-tree runner parcel.
- Diff command: `git diff --no-index /dev/null` for each new file below.
- Builder SHA-256: `runner.go`
  `b617921c7fe0535844b7802d967be5645df6c3d097a03d82a746c6b3f9170787`;
  `runner_test.go`
  `b753ee6556cdefcfaab015f9b8b845511e31b9eac552a9c876939faf9e174cda`.

## Corrective Review History

- The first fresh adversarial review returned Changes Required on two high
  findings; this handoff describes the corrected parcel for a fresh re-review.
- Assert-adoptable now snapshots guidance exactly once immediately after
  successful input/topology materialization and before any diagnostic or
  Terraform-resolver callback. Assert-clean performs zero guidance-source
  calls. A mutation regression proves both callbacks cannot alter the snapshot
  delivered to assessment and verifies the exact call/event order.
- The runner error boundary now rejects typed-nil `*procerr.ProcessFailure`,
  typed-nil `*metadata.MetadataError`, arbitrary typed-nil error interfaces,
  and panicking `Error()` implementations without panicking or leaking text.
  Each maps to fixed internal failure `ASSESSMENT_FAILED` / `saved-plan
  assessment failed`; nonnil process failures retain all fields and detached
  details.

## Files Changed

- Files: `go/internal/assessment/runner.go` and
  `go/internal/assessment/runner_test.go`.
- Files intentionally left untouched: accepted assessment/remnant/report/
  input/eval/policy/guidance files; deployment loader; CLI; Node sources;
  transport; dependencies; all tracked fixtures.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: full active-pack metadata used by the credential-free
  production vertical-slice test.
- Existing docs or design records: Block C handoff and accepted lower-level
  parcel handoffs/reviews.
- Other source evidence: `node-src/domain/plan-assessment-runner.ts`,
  `node-tests/plan-assessment-runner.test.ts`, and accepted Go assessment input,
  policy, guidance, report, report-I/O, transaction, deployment, metadata,
  control-evidence, procerr, and canonjson APIs.

## Generated Artifacts

- Reports: success/error reports are published through the accepted report-I/O
  API; exact stdout/file bytes are test outputs only.
- Schemas: None changed.
- Fixtures: None tracked; tests construct credential-free topology and fake
  Terraform inputs.
- Snapshots: runtime assessment snapshots use the accepted bounded-remnant
  policy.
- Demo or lab outputs: None.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: Go gains `SavedPlanAssertionInputs`,
  `RunSavedPlanAssertionOptions`, and `RunSavedPlanAssertion` for operational
  assert-clean/assert-adoptable orchestration.
- Expected report/count/coverage changes: none versus the Node oracle.
- Expected generated-output changes: exact report stdout/file bytes and
  diagnostic lines become reachable through the runner API.
- Expected no-op areas: classifications, report construction/schema,
  transaction barriers, temp cleanup, CLI exit rendering, plan lifecycle,
  provider/API and artifact generation.

## Invariants Claimed

- Evidence must not be silently dropped: runner delegates assessment and
  typed partial-root failures to the accepted transaction without converting
  a failure into success.
- Generic matcher evidence must not outrank source-backed evidence: guidance
  and drift policy use accepted source-backed APIs.
- Source precedence/provenance must remain explicit: policy preflight occurs
  before lazy inputs, topology, diagnostics, and Terraform resolution.
- Ambiguity must stay classified instead of being coerced to success:
  ambiguous/missing Go union inputs fail request validation; unexpected errors
  cross a safe `ProcessFailure` boundary; blocked reports fail only after the
  report is written.
- Provider-readiness counts must stay explainable: runner does not alter report
  counts or roots.
- Adoption safety invariants: unresolved Terraform sentinel is used during
  topology materialization; zero roots never call the executable resolver;
  assessment diagnostics precede report output; success report-write failure
  is fatal; error-report failure emits a warning and preserves the original
  error; assert-clean/adoptable blocked classifications retain distinct codes.

## Tests Run

- Commands: corrected focused runner/diagnostic/error-boundary tests repeated
  five times; focused race; full assessment package through
  `go test -count=1 ./...`; `go vet ./...`; Linux, Windows, and FreeBSD amd64
  cross-compilation; focused Node runner oracle; gofmt; error-flow lint; module
  dependency listing and diff whitespace checks.
- Relevant output summary: all Go gates passed; Node runner 6/6 passed; module
  remains zero-dependency; a credential-free production vertical slice using
  loaded topology, fake Terraform, assessment, and exact stdout report passed.
- Tests not run and why: CLI wiring/differential corpus is the next parcel; no
  real provider/API call is required or authorized here.

## Known Deferrals

- Deferred work: bound deployment loader, four CLI commands, CLI exit/output
  differential corpus, and the real provider/backend leg.
- Reason it is safe to defer: runner accepts already-loaded/bound inputs and is
  not yet dispatched by the Go CLI; lower-level behavior is covered directly.
- Follow-up owner or trigger: C4 consumes this runner after adversarial
  approval and adds the deployment binding plus credential-free CLI corpus.

## Review Focus

- Highest-risk files or paths: `runner.go` preflight/error-report branches,
  diagnostic JSON/guidance rendering, blocked failure path, and Go union
  adaptation.
- Corrective surfaces to re-check first: guidance snapshot timing/cardinality
  around both externally supplied callbacks; typed-nil and panic containment at
  both lazy-input and Terraform-resolver boundaries; and exact field/detail
  retention for nonnil process failures.
- Specific assumptions to attack: policy precedence; invalid-tenant error-only
  report exception; zero-root resolver suppression; report write before typed
  or blocked failure; best-effort warning not masking original failure;
  successful report-write failure; typed partial preservation; input copying;
  nil/empty path distinctions; numeric/string diagnostic parity.
- Source evidence the reviewer should verify: Node runner source and tests plus
  every accepted Go API called from production hooks.
- Generated artifacts the reviewer should compare: exact report stdout/file
  bytes and every diagnostic/guidance line on the frozen vectors.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  resolving Terraform before topology; calling it for zero roots; emitting a
  success line before report publication; losing policy digest on load error;
  broadening the invalid-tenant schema bypass; converting a typed transaction
  failure to a generic success-looking result; or allowing report errors to
  mask an assessment failure.
