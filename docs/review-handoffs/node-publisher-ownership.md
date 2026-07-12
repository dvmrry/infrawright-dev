# Builder Review Handoff: Node Publisher Ownership

## Intent

- Prevent two persistent Node process-host mutations from publishing into the
  same physical output root at the same time.
- Define the deployment boundary that makes concurrent same-branch ADO jobs
  safe: one complete, job-owned workspace and canonical output root per job.
- Make same-root contention fail immediately and value-safely with stable code
  `OUTPUT_ROOT_BUSY`, while disjoint output roots remain parallel-capable.
- Preserve every existing process schema, generated-artifact byte, publication
  order, retry-forward rule, and refresh pending-marker semantic.
- Do not remove or weaken any existing TOCTOU, CAS, inode, ancestor, crash
  recovery, or final-byte check in this change.

## Base / Head

- Base: `7fe7b6c7e5263f5708d7fc378a15ea03d3c6a984`
- Remediation base: `0004c29e7a3504e6fcb9a49cffafe824700046aa`
- Head: checked-out checkpoint on `feature/node-publisher-ownership`; resolve
  with `git rev-parse HEAD`
- Diff command:
  `git diff 7fe7b6c7e5263f5708d7fc378a15ea03d3c6a984..HEAD`
- Changed-surface command:
  `git diff 0004c29e7a3504e6fcb9a49cffafe824700046aa..HEAD`

## Files Changed

- Files:
  - `docs/adr/0001-publisher-ownership.md`
  - `docs/integration-validation.md`
  - `docs/node-process-api.md`
  - `docs/review-handoffs/node-publisher-ownership.md`
  - `node-src/domain/zcc-pull-materialization.ts`
  - `node-src/domain/zcc-pull-refresh-materialization.ts`
  - `node-src/io/publisher-guard.ts`
  - `node-src/process/execute.ts`
  - `node-tests/publisher-guard.test.ts`
  - `node-tests/zcc-pull-materializer-process.test.ts`
  - `node-tests/zcc-pull-refresh-materializer.test.ts`
- Files intentionally left untouched:
  - All JSON schemas, generated catalogs, fixtures, snapshots, and golden
    outputs.
  - All existing TOCTOU/CAS/crash-recovery implementation; the two
    materialization cores only add a pre-mutation exact-authority check.
  - Python and Make behavior.
  - Azure DevOps YAML; this repository has no ADO pipeline definition, so the
    accepted convention is documented for downstream adoption.

## Source Inputs Consulted

- Provider schemas: N/A; no provider contract changes.
- OpenAPI/API contracts: N/A; no API extraction or normalization changes.
- Provider source files: N/A; no provider operation mapping changes.
- Pack metadata: Existing Zscaler topology and materialization tests only; pack
  metadata is unchanged.
- Existing docs or design records:
  - `AGENTS.md`
  - `docs/adversarial-review.md`
  - `docs/node-process-api.md`
  - `docs/integration-validation.md`
- Other source evidence:
  - `node-src/process/execute.ts` for the three public persistent mutation
    dispatches.
  - Existing bootstrap/refresh publication and acknowledgement implementations
    for the complete output-root and pending-marker boundaries.
  - Repository search found no ADO YAML, so no downstream job-path claim is
    presented as already deployed.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None retained.
- Artifact drift intentionally expected: None.

## Expected Delta

- Expected behavior change:
  - Public bootstrap materialization, refresh materialization, and refresh
    acknowledgement exclusively create `.infrawright.publisher.lock` at the
    complete canonical `INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT` for the duration of
    the mutation.
  - A pre-existing guard fails immediately with retryable I/O code
    `OUTPUT_ROOT_BUSY`; it is neither read nor removed.
  - The configured output root must exactly equal the common authority of the
    resolved artifact target set. A containing ancestor fails before artifact
    mutation with non-retryable I/O code `OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY`.
  - Guard cleanup binds open root/guard handles and their device/inode identity,
    and refuses pathname replacement or root rollover rather than unlinking the
    observed foreign path.
  - Guard removal failure becomes terminal. A primary `ProcessFailure` retains
    its code/category/message and gains a cleanup detail; an unexpected primary
    (including `null`, `undefined`, or a non-Error primitive) remains the generic
    `INTERNAL_ERROR` contract with the cleanup detail.
  - Different physical output roots remain concurrently writable.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: None; the guard is transient coordination,
  not an artifact or parity input.
- Expected no-op areas: Read-only process operations, Python writers, Terraform
  behavior, refresh pending-marker protocol, artifact schemas, bytes, and
  ordering.

## Invariants Claimed

- Evidence must not be silently dropped: No evidence path changes; authority
  and guard failures occur before artifact mutation begins.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: N/A.
- Ambiguity must stay classified instead of being coerced to success: A guard
  pathname is deliberately classified as active-or-stale contention; the host
  never guesses ownership or auto-breaks it.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - The lock unit is the complete canonical output root, not a tenant, resource,
    or leaf artifact.
  - A raw ancestor cannot form a second lock namespace for artifacts beneath an
    exact relative or absolute overlay authority.
  - Guard acquisition uses exclusive no-follow creation and never waits.
  - Cleanup re-proves root handle/path and guard handle/path identity before a
    pathname unlink. Under the documented trusted-runner model, deterministic
    foreign replacements are never removed.
  - The process guard coordinates Node mutations only; job-owned physical-root
    isolation and serialized pipeline steps remain the workflow-long ownership
    guarantee across Python, Node, and Terraform.
  - The refresh pending marker remains the durable publication/apply/
    acknowledgement protocol fence.
  - Cleanup after success and failure removes the transient guard; a failed
    cleanup fails closed and never reports mutation success.
  - A stale guard is removed only by an explicit owner/operator action after
    establishing no publisher is active, or by discarding an exclusively owned
    job root.

## Tests Run

- Commands:
  - `npm ci --ignore-scripts`
  - `npm run check`
  - `npm run build`
  - `make test`
  - `make audit-vendor-boundary`
  - `git diff --check`
  - Focused compiled tests for the publisher guard and both public
    materialization hosts during implementation.
- Relevant output summary:
  - Final Node gate: 601 tests, 600 passed, 1 expected platform skip, 0 failed.
  - Python gate: 1,365 passed, 0 failed.
  - Final production bundle build succeeded.
  - Vendor boundary: 192 allowed matches, 0 violations.
  - Dependency install reported 0 vulnerabilities and added no dependencies.
  - Focused remediation passes cover identity-safe cleanup, root rollover,
    killed-process stale guards, exact/ancestor authority, and all three public
    mutation paths. The final guard-only pass was 16/16 and the corrected
    bootstrap process pass was 9/9 before the full gate.
  - One intermediate full run hit an unrelated `descendant.pid` timing failure
    in `terraform-command.test`; that file immediately passed 18/18 in
    isolation, and the final complete rerun above passed.
  - Diff whitespace check passed.
- Tests not run and why:
  - Live ADO concurrency was not run because this repository contains no ADO
    pipeline definition or downstream build-host authority.

## Known Deferrals

- Deferred work:
  - Apply the documented job-specific path convention in each downstream ADO
    pipeline and retain two concurrent same-branch job logs proving distinct
    canonical roots.
  - Only after that evidence, separately review any reduction of existing
    bootstrap publication TOCTOU checks.
  - Python and Terraform do not consume this Node lock; the job-owned root and
    serialized workflow remain their coordination boundary.
  - A killed process can leave a stale guard. There is intentionally no PID,
    timeout, wait, or automatic stale-lock break in this slice.
  - Portable Node has no descriptor-relative conditional unlink. The final
    identity recheck plus pathname unlink is not an absolute security boundary
    against a hostile same-UID replacement in that last syscall window.
- Reason it is safe to defer:
  - This change is additive and fail-closed. It deletes no existing defense and
    does not claim downstream ADO isolation is already deployed.
- Follow-up owner or trigger:
  - Downstream pipeline owner for ADO deployment/evidence.
  - A separately scoped Phase 3a proposal after that evidence exists.

## Review Focus

- Highest-risk files or paths:
  - `node-src/io/publisher-guard.ts`
  - Exact-authority checks in `node-src/domain/zcc-pull-materialization.ts` and
    `node-src/domain/zcc-pull-refresh-materialization.ts`
  - Persistent branches in `node-src/process/execute.ts`
  - Cleanup/error-precedence tests in `node-tests/publisher-guard.test.ts`
- Specific assumptions to attack:
  - All three and only the persistent public Node mutations are guarded.
  - The guard is acquired for the complete output root before domain mutation.
  - Same-root contention is immediate, stable, value-safe, and leaves an
    existing guard and all artifacts untouched.
  - Cleanup failure cannot turn a failed mutation into a different apparent
    success or expose private guard contents.
  - A regular guard replacement and both root-rollover shapes are preserved,
    including when the replacement path points back to the acquired guard inode.
  - Thrown `null`, `undefined`, and non-Error values remain primary failures when
    cleanup also fails.
  - Requiring a canonical existing output root is compatible with the current
    process-host authority contract.
  - Requiring the exact target-set authority preserves overlay `.`, relative
    overlays, and absolute external overlays while rejecting only ancestors.
  - Direct private publisher helpers are not accidentally presented as guarded
    public interfaces.
- Source evidence the reviewer should verify:
  - Process dispatch has no fourth persistent mutation path.
  - Refresh pending-marker semantics remain independent and unchanged.
  - ADO documentation distinguishes the process guard from workflow-long
    physical-root ownership.
- Generated artifacts the reviewer should compare: Confirm schemas, fixtures,
  snapshots, catalogs, and bundled protocol bytes have no intentional drift.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - A stale file versus a live owner (both must remain `OUTPUT_ROOT_BUSY`).
  - Same Git branch in disjoint roots versus distinct tenants in one root.
  - Non-canonical, symlinked, missing, or filesystem-root output authorities.
  - Canonical ancestor output roots targeting an exact nested overlay.
  - Mutation success plus guard cleanup failure; domain failure plus cleanup
    failure; unexpected failure plus cleanup failure.
  - Abrupt process termination leaving the guard in place.
  - The documented irreducible final lstat-to-unlink race must not be mistaken
    for hostile-local-writer protection.
