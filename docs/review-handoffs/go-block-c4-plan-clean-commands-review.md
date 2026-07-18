# Block C4 Plan and Clean-Plans Commands Review Handoff

## Intent

- Add the `plan` and `clean-plans` CLI composition functions over the accepted
  plan lifecycle and Terraform adapter APIs.
- Preserve argument/default/env semantics, lazy Terraform creation, lifecycle
  options, diagnostics, saved-pair behavior, and cleanup scope.
- Leave dispatch/platform gate, assessment commands, usage, lifecycle/domain
  packages, Node, provider/API behavior, transport, and dependencies unchanged.

## Base / Head

- Base: `822e577` (`Port saved-plan assertion runner to Go`).
- Head: uncommitted two-file command parcel.
- Builder SHA-256: `commands_plan.go`
  `60f2063d5dc55e08ece5a8dfc905097a80b2cb4462189b3e91f2b2561f0963a9`;
  `commands_plan_test.go`
  `a5aece902e95e2c241d0456eb5e96ac9e325e54629454686999255d349f9feb1`.
- Diff command: `git diff --no-index /dev/null` for each new file.

## Files Changed

- Files: `go/cmd/iw/commands_plan.go` and `commands_plan_test.go`.
- Files intentionally left untouched: `main.go`, usage, assessment command
  parcel, plan lifecycle/Terraform internals, Node, docs/schemas, transport,
  dependencies.

## Source Inputs Consulted

- Provider schemas, OpenAPI/API contracts, provider source, pack metadata:
  N/A beyond credential-free pack fixtures.
- Existing docs/design: Block C/C4 handoff and accepted lifecycle reviews.
- Other source evidence: Node plan/clean command functions and top-level
  platform/help/legacy dispatch, Node plan CLI/lifecycle tests, committed Go
  lifecycle/Terraform APIs and CLI helpers.

## Generated Artifacts

- Reports/schemas/tracked fixtures: unchanged.
- Runtime saved plan pair: exact `tfplan` and `tfplan.sources` behavior remains
  lifecycle-owned and is checked by tests.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change: callable but undispatched plan/clean functions.
- Expected report/count/coverage changes: clean removal result/diagnostics only,
  matching Node.
- Expected generated-output changes: saved pair becomes reachable through the
  command function; exact bytes/modes remain unchanged.
- Expected no-op areas: top-level dispatch/platform precedence, assessment,
  lifecycle internals, provider/API and generation artifacts.

## Invariants Claimed

- Evidence must not be silently dropped: `--save` delegates exact fingerprint/
  plan pairing to the accepted lifecycle; cleanup removes only the pair.
- Generic/source precedence/provider readiness: N/A.
- Ambiguity must stay classified: plan requires tenant but permits explicit
  empty for downstream validation; resource repeats accumulate; most values
  use last-wins; legacy failure translation stays at the caller shim.
- Adoption safety invariants: Terraform resolution/factory creation is lazy
  until the first lifecycle initialization; explicit `--terraform` wins over
  `TF`; `Plan` before `Initialize` fails; clean-plans never resolves Terraform
  or enters the platform gate; diagnostics use stderr with one newline;
  unrelated files survive cleanup.

## Tests Run

- Commands: focused/focused-race; full Go/full race; vet; error-flow,
  formatting/whitespace; Linux/Windows CGO-disabled cross-build; Node plan
  CLI/lifecycle oracle.
- Relevant output summary: all Go gates passed; Node 22/22 passed; no
  dependency changes.
- Tests not run and why: dispatch/platform differential is the next parcel; no
  provider/API credentials are involved.

## Known Deferrals

- Deferred: `main.go` dispatch and top-level platform/help pre-gate, assessment
  dispatch, combined CLI differential, real provider leg.
- Reason safe: functions remain unreachable from production dispatch.
- Follow-up trigger: integrate only after fresh adversarial approval.

## Review Focus

- Highest-risk files: lazy Terraform adapter and option composition.
- Assumptions to attack: option multiplicity/empty tenant; `--terraform > TF`
  timing; no resolution before initialization; no platform/Terraform path for
  clean; stream/newline behavior; raw fake-Terraform argv/order; saved pair
  `0600` modes/fingerprint; exact cleanup count and unrelated-file survival.
- Source evidence: exact Node functions/tests and accepted Go lifecycle APIs.
- Generated artifacts: runtime saved pair bytes/modes only.
- Edge cases that could weaken safety: plan before init, cached resolver error,
  accidental clean recursion, eager Terraform lookup, or command-local
  platform gate changing help/malformed-arg precedence.
