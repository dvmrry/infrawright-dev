# Block C4 Bound Assessment Deployment Review Handoff

## Intent

- Port `loadBoundAssessmentDeployment` as the narrow seam that parses the
  deployment from the same stable bytes later rechecked as assessment control
  evidence.
- Preserve optional-missing defaults, exact digest/identity binding, symlink
  policy, bounded reads, redacted failures, and zero results on validation
  failure.
- Keep CLI, runner, existing parser/control-evidence internals, schemas,
  provider/API behavior, transport, and dependencies unchanged.

## Base / Head

- Base: `5a51cd0` (`Bound assessment temporary remnants`).
- Head: uncommitted working-tree parcel.
- Diff command: inspect the two new `assessment` files with
  `git diff --no-index /dev/null`; inspect the package-comment deletion with
  `git diff -- go/internal/deployment/deployment.go`.

## Files Changed

- Files: `go/internal/deployment/assessment.go`,
  `go/internal/deployment/assessment_test.go`, and removal of the now-stale
  “deliberately not ported” paragraph in `deployment.go`.
- Files intentionally left untouched: deployment parser/loader behavior,
  control-evidence implementation, assessment runner, CLI, schemas, Node
  sources, dependencies.

## Source Inputs Consulted

- Provider schemas, OpenAPI/API contracts, provider source, pack metadata:
  N/A.
- Existing docs or design records: Block C/C4 handoff and accepted
  control-evidence contract.
- Other source evidence: `node-src/domain/deployment.ts:294-305`,
  `node-src/domain/control-evidence.ts`, `node-src/io/bounded-files.ts`,
  existing Go `deploymentFromText` and
  `controlevidence.BindOptionalAssessmentControlText`, Node deployment tests,
  and the Node runner deployment-mutation case.

## Generated Artifacts

- Reports, schemas, fixtures, snapshots, demo outputs: None changed.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: new `BoundAssessmentDeployment` and
  `LoadBoundAssessmentDeployment` API returns a validated deployment plus the
  exact bound control-file evidence for its source.
- Expected report/count/coverage and generated-output changes: None.
- Expected no-op areas: existing deployment parsing/defaults, control-evidence
  recheck behavior, runner/CLI, provider/API, generated artifacts.

## Invariants Claimed

- Evidence must not be silently dropped: parsed deployment and evidence derive
  from one stable bounded read; present bytes retain path, digest, size, file
  identity, and symlink policy; missing files bind path absence.
- Generic matcher/source precedence/provider-readiness: N/A.
- Ambiguity must stay classified instead of being coerced to success: invalid
  JSON/metadata/UTF-8/path/type/size fail with existing structured errors and a
  zero ordinary result.
- Adoption safety invariants: later creation or mutation fails
  `ASSESSMENT_CONTROL_CHANGED`; zero-value bind options preserve Node's default
  symlink-follow behavior; explicit no-follow is retained by value, not through
  a caller-owned pointer; no source content/path leaks from sanitized failures.

## Tests Run

- Commands: focused deployment tests; deployment race; deployment/control/
  assessment packages; full Go; full race; vet; Linux/Darwin/Windows cross-
  compilation; Node deployment and runner tests.
- Relevant output summary: all Go gates passed; Node 13/13 passed; no
  dependencies changed.
- Tests not run and why: no provider/API or CLI behavior belongs to this seam.

## Known Deferrals

- Deferred work: runner/CLI wiring and credential-free CLI differential.
- Reason it is safe to defer: the loader is additive and unreachable from CLI
  dispatch until C4 wiring.
- Follow-up owner or trigger: consume after fresh adversarial approval.

## Review Focus

- Highest-risk files or paths: `assessment.go` stable-read-to-parse handoff and
  panic recovery; present/absent evidence tests.
- Specific assumptions to attack: returned deployment and digest describe the
  same bytes; missing detection is ENOENT-only; validation panic returns zero;
  default/explicit symlink policies match Node; options pointers do not escape;
  all limits/redaction remain in the accepted control-evidence layer.
- Source evidence the reviewer should verify: exact Node function and existing
  Go parser/control-evidence APIs.
- Generated artifacts to compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  parsing a second read, accepting a directory/symlink under no-follow,
  dropping absent-file binding, leaking bytes/path on error, or returning a
  nonzero deployment after validation panic.
