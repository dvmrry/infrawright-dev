# Generic Saved-Plan Assessment and Guidance — Builder Review Handoff

## Intent

- Port the operational behavior of `engine.ops.cmd_assert_clean` and
  `engine.ops.cmd_assert_adoptable` to ordinary typed Node library functions
  and thin CLI/Make adapters.
- Reuse the existing Node saved-plan evidence transaction, classifier, drift
  policy, fingerprint-v2, report normalizer, and concrete/schema path split.
- Load provider-configuration, absent-default, and dynamic-schema guidance
  from the original active pack manifests, match it only to concrete blocked
  findings, and keep it informational and failure-isolated.
- Switch `make assert-clean` and `make assert-adoptable` to Node with exact
  retained Python report, diagnostic, and exit behavior.
- Keep Apply, re-planning, provider/product orchestration, live credentials,
  real backends, external pipelines, new schemas, and authorization receipts
  out of this change.

## Base / Head

- Base: `e023ce2ec43498f179981dbfa750d3101bb2b273`, the accepted amended head
  of draft PR #203.
- Head: frozen review commit on
  `feature/node-generic-saved-plan-assessment`.
- Diff command:
  `git diff e023ce2ec43498f179981dbfa750d3101bb2b273..HEAD`.

## Files Changed

- Files:
  - `Makefile`
  - `node-src/cli/main.ts`
  - `node-src/domain/assessment-guidance.ts`
  - `node-src/domain/plan-assessment-inputs.ts`
  - `node-src/domain/plan-assessment-runner.ts`
  - `node-src/domain/plan-assessment.ts`
  - `node-src/domain/plan-policy.ts`
  - `node-src/domain/plan-report.ts`
  - `node-src/io/assessment-report.ts`
  - `node-src/io/terraform-command.ts`
  - `node-src/io/terraform-show.ts`
  - `node-src/json/control.ts`
  - `node-src/json/python-compatible.ts`
  - `node-src/metadata/packs.ts`
  - `node-tests/assessment-cli.test.ts`
  - `node-tests/assessment-guidance.test.ts`
  - `node-tests/plan-assessment-runner.test.ts`
  - `node-tests/plan-assessment.test.ts`
  - `node-tests/plan-report.test.ts`
  - `node-tests/json.test.ts`
  - this handoff
- Files intentionally left untouched:
  - `engine/ops.py`, `engine/adoption_guidance.py`, `engine/provider_config.py`,
    `engine/guidance_paths.py`, and the Python lane validators remain retained
    parity references.
  - `make apply` remains Python-backed until the separately authorized exact-
    plan Apply slice.
  - Collection, Transform, Adopt, module/root generation, import staging,
    plan creation, provider/product code, external pipelines, and live
    configuration are unchanged.
  - Draft PRs #191 and #192 and frozen ZCC process-host operations are
    untouched.
- Scope metrics: thirteen production TypeScript files plus one Make adapter;
  1,872 net production lines. Tests and this handoff are excluded. The
  authorized stop trigger is 14 production files / approximately 3,000 net
  production lines; this slice reaches but does not exceed the file trigger.

## Source Inputs Consulted

- Provider schemas: N/A; classification consumes validated Terraform plan
  JSON and does not interpret provider schemas.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata:
  - original active `packs/*/pack.json` provider-configuration,
    absent-default, and dynamic-schema lanes through `loadPackRoot(...)`;
  - real Google, AWS, and Cloudflare guidance manifests in direct loader-
    backed tests;
  - full, empty, AWS, Zscaler, and physically reduced ZIA profile fixtures.
- Existing docs or design records:
  - `docs/adversarial-review.md` and its handoff/review templates;
  - the accepted generic plan-lifecycle handoff and existing saved-plan
    assessment report schema.
- Other source evidence:
  - `engine/ops.py` assessment commands and report lifecycle;
  - `engine/adoption_guidance.py`, `engine/provider_config.py`,
    `engine/guidance_paths.py`, `engine/lanes.py`, and the lane validators;
  - existing Node plan evidence, classifier, drift-policy, fingerprint,
    report, bounded-file, and Terraform-show implementations;
  - retained Python assessment, guidance, provider-config, lane, classifier,
    Make, and ops tests.

## Generated Artifacts

- Reports: only disposable v1 saved-plan assessment reports in tests.
- Schemas: None; the existing `infrawright.saved_plan_assessment` schema
  version 1 remains strict and unchanged. As in Python, the narrow best-effort
  preflight error writer can record an empty raw tenant when tenant validation
  itself is the failure; it cannot represent a successful assessment.
- Fixtures: disposable fake-Terraform workspaces only; none are committed.
- Snapshots: private temporary saved-plan snapshots created and scrubbed by
  the existing assessment core.
- Demo or lab outputs: None retained.
- Artifact drift intentionally expected: None. Retained Node/Python report
  bytes are exact for clean, blocked-with-guidance, tolerated, no-plan,
  selection-error, and assessment-error paths.

## Expected Delta

- Expected behavior change:
  - `infrawright assert-clean` and `infrawright assert-adoptable` are generic
    CLI commands backed by the Node library;
  - the corresponding Make targets no longer invoke Python;
  - active deployment/root selection resolves saved plans and existing config
    artifacts through the real metadata loader;
  - blocked `assert-adoptable` results receive stable, deduplicated guidance
    joined to each concrete finding path while retaining the schema path that
    matched it;
  - reports use same-directory private temporary files and atomic rename;
  - pre-assessment selector failures write an error report when requested,
    while an unrepresentable or unwritable error report cannot mask the
    original assessment failure;
  - Terraform executable lookup is lazy when no saved plan exists, preserving
    the Python no-plan diagnostic without requiring Terraform.
  - drift policy is bound before topology and Terraform resolution, and its
    digest is retained in failures that occur during later input selection;
  - deployment loading is bound into the assessment transaction, and both the
    deployment bytes and loaded root topology are rechecked before success;
  - pack/deployment loader failures now enter the requested best-effort error-
    report lifecycle;
  - authoritative absent-default/dynamic lane validation runs after provider
    filtering, including vocabulary matrices, provider scope, duplicate and
    overlap checks, so malformed metadata cannot be presented as evidence or
    suppress another provider's valid lane;
  - repeated `--report`, invalid policy, and invalid Terraform JSON preserve
    their legacy status-2 classes;
  - valid guidance is no longer rejected by an implementation-only 10,000-
    entry cap absent from Python or the v1 schema.
  - syntactically invalid policy and Terraform JSON preserve Python's
    value-safe `JSONDecodeError` location text, while semantic policy errors
    retain the existing redacted diagnostic;
  - missing Terraform preserves the retained Python `ENOENT` diagnostic;
  - guidance JSON retains original numeric-token provenance so finite floats,
    including integer-valued `1.0`, render byte-identically to Python;
  - bracketed collection selectors remain supported while true bare `*` path
    segments are rejected by the authoritative lane validator.
- Expected report/count/coverage changes: None. Classification and summary
  counts remain owned by the existing core; guidance cannot change them.
- Expected generated-output changes: no new artifact or schema; only the
  already-existing optional assessment report is written.
- Expected no-op areas: plan creation, Apply, collection, transformation,
  adoption projection, environment generation, import staging, provider
  behavior, and external pipelines.

## Invariants Claimed

- Evidence must not be silently dropped: every selected saved plan requires a
  current fingerprint-v2 sidecar, is hashed before `terraform show`, and has
  plan, fingerprint, policy, and control evidence rechecked before success.
- Generic matcher evidence must not outrank source-backed evidence: guidance
  comes only from original active pack metadata and is emitted only after the
  existing classifier has already blocked an exact source/address/path.
- Source precedence/provenance must remain explicit: user drift policy alone
  controls `plan_tolerate`; pack guidance is explanatory and never merged into
  classification policy.
- Ambiguity must stay classified instead of being coerced to success: missing
  plans/fingerprints, malformed plan/policy data, input mutation, stale
  policies, and unmatched changes retain their existing failure or blocked
  state.
- Provider-readiness counts must stay explainable: N/A; this change adds no
  readiness count or provider coverage claim.
- Adoption safety invariants:
  - `assert-clean` permits only no-op/import-only classifications;
  - `assert-adoptable` permits clean or explicitly user-tolerated drift and
    refuses blocked plans;
  - bool and number remain distinct Terraform values;
  - guidance collection is lane-isolated, informational, and cannot authorize
    Apply;
  - raw plan values never enter the report beyond already-normalized finding
    metadata and source-backed guidance values;
  - no Terraform Apply or remote mutation occurs.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run build`
  - compiled assessment/classifier/evidence/fingerprint/policy/report/
    guidance/Terraform-show/CLI suites
  - `python3 -m unittest` over retained assessment, guidance, provider-config,
    absent-default, dynamic-schema, drift-policy, plan-eval, Make, and ops
    modules
  - exact Python/Node report, stdout, stderr, and exit differentials
  - Python-disabled Make integration with fake Terraform
  - `python3 -m engine.audit_vendor_boundary`
  - `git diff --check`
  - `npm test` once in the clean final gate
- Relevant output summary:
  - affected Node final gate: 119 tests, 118 passed, 1 platform skip, 0 failed;
  - retained Python final gate: 332 passed, 0 failed;
  - full Node suite: 1,042 tests, 1,041 passed, 1 platform skip, 0 failed;
  - vendor-boundary audit: 187 allowed matches, 0 violations;
  - clean production build and whitespace check passed;
  - exact Python/Node report/diagnostic/exit differentials passed for clean,
    blocked guidance, tolerated policy, no plans, and selector failures;
  - Python-disabled `make assert-clean` and `make assert-adoptable` passed;
  - no credential, network, live backend, provider, or Apply operation ran.
- Pre-review defects caught and remediated:
  - selection failures initially escaped the error-report lifecycle; the
    runner now writes the retained error contract best-effort;
  - that remediation initially attempted to validate an invalid request even
    when no report was requested, masking an empty-tenant diagnostic; report
    construction is now conditional and the exact Make regression passes;
  - a worktree-only dependency symlink caused the production self-containment
    guard to see another worktree; a local clean dependency install fixed the
    environment without changing tracked files.
- Formal-review findings accepted and remediated in one batch:
  - operational assessment now binds deployment bytes and re-materializes the
    selected loaded-root topology during the final evidence checks, preventing
    a changed overlay/config selection from publishing clean;
  - pack and deployment loading moved inside the runner's error-report
    lifecycle, including invalid raw tenant requests;
  - policy validation/hash binding now precedes selector and Terraform
    resolution, with policy mutation detected between preflight and the core;
  - invalid policy/plan JSON and duplicate `--report` recover legacy exit and
    option behavior, while missing Terraform retains its real resolution
    diagnostic instead of a generic internal message;
  - absent-default and dynamic-schema guidance now applies the retained Python
    validator semantics, provider-first failure isolation, matrices, scope,
    duplicate/overlap, and unknown-key rejection;
  - the Python-only guidance-count cap mismatch was removed, and report
    construction still drops invalid explanatory guidance rather than losing
    an assessment report;
  - finite guidance floats and sorted composite human diagnostics now use the
    retained Python JSON spelling for the supported contract surface.
- Post-remediation focused verification:
  - 46/46 compiled Node tests passed, including new regressions for each
    accepted finding and the existing exact Python/Node operational cases.
  - the retained Python assessment/guidance corpus passed 332/332;
  - the complete Node suite passed with 1,049 successes, one platform skip,
    and zero failures (1,050 total);
  - the vendor-boundary audit remained clean at 187 allowed matches and zero
    violations; whitespace, typecheck, and production CLI build passed.
- Patch-review residuals accepted and remediated in the second and final batch:
  - bare-wildcard lane paths now fail like Python without rejecting `[]` or
    `[*]` collection selectors;
  - report and human-diagnostic number rendering uses the existing Python
    binary64 formatter and selectively retained manifest numeric tokens, so
    `1e-6`, `1e20`, and token-authored `1.0` retain Python spelling;
  - invalid policy JSON, invalid Terraform-show JSON, and missing Terraform
    now match retained Python stderr and error-report bytes exactly;
  - syntax diagnostics remain value-safe, and semantic/UTF-8 policy failures
    remain redacted so user-controlled policy keys cannot leak.
- Second-remediation verification:
  - the direct residual corpus passed 71/71;
  - the security/parity retry passed 30/30;
  - exact Python/Node CLI differentials passed for invalid policy JSON,
    invalid Terraform JSON, and missing Terraform;
  - the retained Python corpus passed 332/332;
  - typecheck, production build, vendor audit, and whitespace passed;
  - the clean full-Node retry on the final code passed 1,052 tests with one
    platform skip and zero failures (1,053 total).
- Tests not run and why:
  - no real Terraform backend/provider or credentials: acceptance explicitly
    uses fake Terraform and requires no live evidence;
  - no deployment Apply: forbidden for this slice;
  - no external work-side/ADO pipeline: out of repository scope.

## Known Deferrals

- Deferred work:
  - generic exact-plan Apply and its Make cutover;
  - operational runtime/release cutover readiness;
  - external read-only/live integration evidence.
- Reason it is safe to defer: the existing saved-plan/fingerprint pair is the
  unchanged handoff. `make apply` remains on Python until its own reviewed
  slice; this change cannot perform Apply.
- Follow-up owner or trigger: continue the fixed stacked sequence only after
  this slice is accepted. Slice 4 remains blocked because the supplied prompt
  ends midway through its documentation section and omits the remaining
  acceptance/review/scope/final-stop instructions.

## Review Focus

- Highest-risk files or paths:
  - `node-src/domain/assessment-guidance.ts`
  - `node-src/domain/plan-assessment-runner.ts`
  - guidance integration in `node-src/domain/plan-assessment.ts`
  - loaded-root selection in `plan-assessment-inputs.ts`
  - report writer, CLI, and Make wiring
- Specific assumptions to attack:
  - every selected `tfplan` is bound to the exact current fingerprint inputs,
    plan bytes, policy bytes, and final evidence rechecks;
  - no-plan and policy-error precedence, grouped-root selection, config format,
    backend config identity, and state key match Python;
  - provider, absent-default, and dynamic-schema rules are validated and
    matched using the original provider/resource/path semantics;
  - list-indexed findings preserve concrete `finding_path` while guidance
    retains normalized `matched_plan_path`;
  - bool/number comparison, unknown-after paths, resource-drift records,
    sorting, and deduplication match Python;
  - malformed or unrepresentable guidance fails to no annotation and cannot
    alter classification or suppress the assessment report;
  - report writes are atomic, output `-` remains exact, and report failures do
    not mask primary assessment failures on error paths;
  - CLI/Make status classes and diagnostics remain exact, including invalid
    tenant/selector, missing Terraform, no plans, and blocked results;
  - no Python invocation remains in either production assessment Make path;
  - no Apply, new schema, receipt, catalog, publisher, or product-specific
    assumption entered this slice.
- Source evidence the reviewer should verify: the Python modules, original
  pack manifests, existing report schema, and retained tests listed above.
- Generated artifacts the reviewer should compare: exact disposable report
  bytes from `node-tests/assessment-cli.test.ts` and report-contract fixtures
  in `node-tests/plan-report.test.ts`.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  sensitivity-only changes, numeric/list paths, map keys with punctuation,
  duplicate guidance, malformed lane metadata, partial multi-root failures,
  stale policy entries, mutation during `terraform show`, missing
  fingerprints, stdout/error report handling, and report-write failure.
