# Block C4 Assessment Commands Review Handoff

## Intent

- Add the assert-clean/assert-adoptable CLI adapters over the committed
  saved-plan assertion runner and bound deployment loader.
- Preserve exact option grammar/defaults, policy-first lazy loading, Terraform
  precedence/laziness, deployment control evidence, diagnostics, and report
  routing.
- Leave top-level dispatch/platform gating, plan/clean commands, usage, domain
  packages, Node sources, provider/API behavior, transport, and dependencies
  unchanged.

## Base / Head

- Base: `822e577` (`Port saved-plan assertion runner to Go`).
- Head: uncommitted two-file assessment-command parcel.
- Diff command: `git diff --no-index /dev/null` for each new file below.

## Files Changed

- Files: `go/cmd/iw/commands_assessment.go` and
  `go/cmd/iw/commands_assessment_test.go`.
- Files intentionally left untouched: `main.go`, usage, plan/clean command
  parcel, committed runner/deployment/assessment internals, docs/schemas,
  Node, transport, dependencies.

## Source Inputs Consulted

- Provider schemas, OpenAPI/API contracts, provider source: N/A.
- Pack metadata: credential-free active-pack fixture.
- Existing docs/design: Block C/C4 handoff and accepted runner/deployment
  reviews.
- Other source evidence: Node `assessmentCliOptions`/`assessmentCommand`, Node
  assessment CLI tests, committed Go runner, bound deployment loader, pack/
  deployment/CLI/Terraform helpers.

## Generated Artifacts

- Reports: existing exact report bytes are routed to a file or stdout; tests
  inspect runtime outputs only.
- Schemas and tracked fixtures/snapshots: unchanged.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: direct command functions parse and execute
  assert-clean/assert-adoptable, but remain unreachable until later dispatch.
- Expected report/count/coverage changes: none versus Node.
- Expected generated-output changes: none; only routing becomes available.
- Expected no-op areas: report construction/schema, classification,
  transaction cleanup, top-level exit mapping/platform gate, provider/API and
  generation artifacts.

## Invariants Claimed

- Evidence must not be silently dropped: lazy input loading binds the absolute
  deployment path and includes its `BoundAssessmentControlFile`.
- Generic/source precedence: accepted runner/guidance/policy APIs remain
  authoritative.
- Ambiguity must stay classified: clean rejects `--policy`; duplicate report/
  tenant options reject; adoptable policy uses last option; legacy codes remain
  unmodified for the later dispatch shim.
- Adoption safety invariants: runner policy preflight occurs before pack,
  deployment, topology, or Terraform resolution; `TF` and `PATH` are read only
  inside the lazy resolver; explicit `--terraform` wins; zero roots skip it;
  diagnostics go to stderr and exact report bytes to stdout/file.

## Tests Run

- Commands: focused/count-five/race assessment command tests; full Go/full
  race; vet; compiled Node assessment CLI oracle.
- Relevant output summary: all Go gates passed; Node 12/12 passed; no
  dependency or tracked artifact changes.
- Tests not run and why: main dispatch/differential is the next parcel; no real
  provider/API call belongs here.

## Known Deferrals

- Deferred work: `main.go` dispatch and top-level Terraform platform pre-gate,
  full direct Go-vs-Node CLI differential corpus, real provider leg.
- Reason safe to defer: the functions are unreachable from `run` until the
  coordinator wires them; direct tests cover composition.
- Follow-up owner/trigger: integrate only after fresh adversarial approval.
- Deliberate Go-native operational difference: pack and deployment load
  sequentially; Node uses `Promise.all`. Do not claim deterministic parity for
  simultaneous independent loader failures.

## Review Focus

- Highest-risk files: lazy runner option construction and fixture command
  harness.
- Assumptions to attack: no TF/PATH/pack/deployment read before policy
  preflight; relative deployment `filepath.Abs` parity; control file included;
  environment read timing; explicit-empty values; repeat/duplicate grammar;
  report trailing newline/order and write errors; legacy errors not translated
  early.
- Source evidence: exact Node command functions/tests and committed Go APIs.
- Generated artifacts: exact stdout/file report bytes and diagnostics.
- Edge cases that could weaken evidence: unbound deployment parsing, eager
  Terraform resolution, resolving for zero roots, policy after bad topology,
  lost report error, or a blocked result returned as success.
