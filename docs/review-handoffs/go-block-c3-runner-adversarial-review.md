# Block C3 Saved-Plan Assertion Runner Adversarial Review

## Blocking Findings

None remaining after the review/fix/re-review loop.

The parity reviewer approved the Node ordering, diagnostics, and report bytes.
The safety reviewer found two high-severity Go-boundary defects:

1. `LoadedPackRoot` was shallow-copied and source-backed guidance was not
   snapshotted until after externally supplied diagnostic and Terraform
   resolver callbacks. A callback could mutate caller-owned manifest data and
   change guidance after topology had already been materialized.
2. The safe-failure mapper could dereference a typed-nil
   `*procerr.ProcessFailure` or `*metadata.MetadataError`, and could invoke a
   panicking `Error` method, crashing instead of returning a sanitized report.

The runner now snapshots adoptable guidance exactly once after successful
topology resolution and before either callback; clean mode makes zero guidance
calls. The failure boundary detects typed-nil values and contains panicking
error methods, returning fixed internal `ASSESSMENT_FAILED` /
`saved-plan assessment failed` while preserving a detached copy of every
field/detail for nonnil process failures. Direct callback-mutation and typed-
nil/panicking-error regressions cover both input loading and lazy Terraform
resolution. The finding reviewer re-ran the corrected parcel and approved it.

## Non-Blocking Risks

Concurrent mutation of a caller-owned `LoadedPackRoot` while the synchronous
runner is actively reading it is not promised as supported. The accepted fix
seals the sequential public-callback hazard through the immutable guidance
snapshot without adding a large general-purpose metadata deep copier.

Runner-specific numeric diagnostics use the accepted `canonjson` layer. The
runner fixture covers lossless `1.0` and Unicode; canonjson's accepted vectors
cover negative zero, unsafe integers, and scientific-notation boundaries.

## Source Evidence Review

- Diff inspected: `runner.go`, `runner_test.go`, and builder handoff against
  current base `202f5d7`.
- Handoff inspected: yes, including corrective history and exact hashes.
- Node source/tests inspected: runner implementation and 6-case oracle.
- Accepted Go APIs inspected: policy preflight, input resolution, assessment
  transaction, guidance snapshot, report construction/I/O, procerr, canonjson.
- Provider schemas, OpenAPI/API contracts, provider source: N/A.
- Pack metadata: active-pack guidance used by the credential-free production
  vertical slice and callback mutation regression.
- Missing evidence or review gaps: CLI dispatch/differential and real provider
  leg remain explicitly separate.

Verification after fixes: focused runner/corrective tests repeated five times,
focused race, full assessment/full Go, vet, formatting/diff checks,
Linux/Windows/FreeBSD cross-compilation, Node runner 6/6, zero dependency
drift, and a credential-free production topology/fake-Terraform/report slice.
The coordinator independently re-ran the corrective focused/race tests.

## Generated Artifact Review

- Reports reviewed: exact stdout and file report bytes; policy/input error
  reports; typed transaction failures; blocked and successful reports.
- Schemas and tracked fixtures: unchanged.
- Artifact drift accepted or rejected: no drift.

## Verdict

Approve.

Verdict rationale: both high findings are fixed with direct regressions and no
new abstraction. Policy precedence, zero-root laziness, diagnostic/report
ordering, typed partials, blocked codes, and exact output remain intact.
