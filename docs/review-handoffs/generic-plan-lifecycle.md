# Generic Root Queries and Plan Lifecycle — Builder Review Handoff

## Intent

- Port the non-assessment root and saved-plan lifecycle from `engine/ops.py`
  to ordinary typed Node library functions and thin CLI adapters.
- Switch `make roots`, `make scope-paths`, `make plan-roots`, `make plan`, and
  `make clean-plans` to Node without changing their product-independent
  behavior or output contracts.
- Correct the shared Terraform runner so Oracle retains its explicit five
  minute default while ordinary plan has no artificial deadline, explicit
  long deadlines do not overflow JavaScript timers, executable resolution
  uses platform path semantics, and plan output streams to the caller.
- Preserve the existing fingerprint-v2 saved-plan pair for the later
  assessment and Apply slices.
- Keep assessment, guidance, Apply, provider/product orchestration, live
  credentials, backends, and external pipelines out of this change.

## Base / Head

- Base: `be5e4d04cc33b08b051519a1e2753a716dc20d40`, the accepted head of
  draft PR #199.
- Head: frozen review commit on `feature/node-plan-lifecycle`.
- Diff command:
  `git diff be5e4d04cc33b08b051519a1e2753a716dc20d40..HEAD`.

## Files Changed

- Files:
  - `Makefile`
  - `node-src/cli/main.ts`
  - `node-src/domain/adopt-runner.ts`
  - `node-src/domain/import-oracle.ts`
  - `node-src/domain/plan-lifecycle.ts`
  - `node-src/domain/plan-roots.ts`
  - `node-src/domain/scope-paths.ts`
  - `node-src/io/terraform-command.ts`
  - `node-src/io/terraform-show.ts`
  - `node-tests/import-oracle.test.ts`
  - `node-tests/plan-cli.test.ts`
  - `node-tests/plan-lifecycle.test.ts`
  - `node-tests/terraform-command.test.ts`
  - `node-tests/terraform-show.test.ts`
  - this handoff
- Files intentionally left untouched:
  - `engine/ops.py` remains the parity reference and supplies assessment and
    Apply until their authorized later slices.
  - Plan assessment, drift guidance, report schemas, Apply, work-side/ADO
    definitions, and live provider/backend configuration are unchanged.
  - Draft PRs #191 and #192 and all frozen ZCC process-host operations are
    untouched.

## Source Inputs Consulted

- Provider schemas: existing loaded pack schemas are exercised through the
  metadata loader, but this change does not interpret or modify their fields.
- OpenAPI/API contracts: N/A; no API collection or mapping behavior changes.
- Provider source files: N/A; no provider source behavior changes.
- Pack metadata:
  - original `packs/*/pack.json`, registries, references, generated-resource
    flags, provider ownership, and `packsets/` profiles through
    `loadPackRoot(...)`;
  - real ZIA metadata in the physically reduced CLI differential fixture;
  - existing full, empty, provider, Zscaler, and reduced-profile test inputs.
- Existing docs or design records:
  - `docs/adversarial-review.md` and its templates;
  - the accepted generic metadata, environment-root, and import-staging stack.
- Other source evidence:
  - `engine/ops.py` resource ordering, root topology, changed-path scoping,
    materialized-root discovery, `cmd_plan`, fingerprint-v2 helpers, and
    `cmd_clean_plans`;
  - existing Node root, path, fingerprint, Terraform runner/show, metadata,
    environment, and staging implementations;
  - retained Python tests in `tests/test_ops.py` and
    `tests/test_makefile_overlay.py`.

## Generated Artifacts

- Reports: None.
- Schemas: None; the existing root topology, changed-path scope, plan-roots,
  and fingerprint-v2 shapes are reused without a new contract.
- Fixtures: disposable real-metadata roots and fake-Terraform workspaces only;
  none are committed.
- Snapshots: None.
- Demo or lab outputs: None retained.
- Artifact drift intentionally expected: None. Query JSON and
  `tfplan.sources` bytes match Python; `tfplan` additionally receives the
  already-approved user-private mode where supported.

## Expected Delta

- Expected behavior change:
  - the five named Make targets invoke `dist/infrawright-cli.mjs` and do not
    invoke Python at runtime;
  - typed loaded-metadata variants bind the existing root and path algorithms
    to the active pack root and deployment;
  - deployment plan and future Apply callers have no artificial Terraform
    timeout unless they explicitly provide one;
  - Oracle keeps its explicit 300-second default and accepts positive practical
    durations beyond the removed ten-minute ceiling;
  - Terraform executable selection supports POSIX, Windows, relative-explicit,
    and PATH forms and validates the resolved file;
  - normal Terraform plan output is bounded and streamed without a shell.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: only a real `tfplan` plus its existing
  fingerprint-v2 sidecar when `SAVE=1`; no new artifact type or schema.
- Expected no-op areas: collection, transformation, adoption projection,
  module/root generation, import staging, assessment, guidance, Apply, and all
  provider-specific behavior.

## Invariants Claimed

- Evidence must not be silently dropped: a saved plan is accepted only as the
  exact pair `tfplan` and `tfplan.sources`; prior pairs and failed/mutated
  attempts are cleaned rather than left partially authoritative.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  evidence matcher changes.
- Source precedence/provenance must remain explicit: resource/root facts come
  from the active metadata loader and deployment; selectors do not consult a
  transition catalog.
- Ambiguity must stay classified instead of being coerced to success: invalid
  selectors, invalid tenants, partial grouped config, missing backend config,
  missing saved output, and input mutation all fail closed.
- Provider-readiness counts must stay explainable: N/A; no readiness report or
  count changes.
- Adoption safety invariants:
  - plan operates on complete selected roots and uses the deployment's actual
    config format and backend key `<tenant>/<root-label>.tfstate`;
  - init-input identity is checked around init and fingerprint-v2 inputs are
    checked around plan;
  - no Terraform Apply or remote mutation is introduced;
  - `clean-plans` removes only the saved-plan pair.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run build`
  - compiled affected root/path/fingerprint/runner/show/Oracle/staging/adopt/
    CLI/plan suites
  - `python3 -m unittest tests.test_ops tests.test_makefile_overlay`
  - Python/Node query and fingerprint differentials
  - Python-disabled Make integration using fake Terraform
  - `python3 -m engine.audit_vendor_boundary`
  - `git diff --check`
  - `npm test` once in the successful final gate
- Relevant output summary:
  - affected Node checkpoint: 133 tests, 132 passed, 1 platform skip, 0 failed;
  - retained Python ops/Make suite: 131 passed, 0 failed;
  - timeout remediation including frozen ZCC callers: 55 passed, 0 failed;
  - vendor-boundary audit: 187 allowed matches, 0 violations;
  - full Node suite: 1,020 tests, 1,019 passed, 1 platform skip, 0 failed;
  - exact query stdout/stderr differentials and Python-disabled Make paths
    passed against a physically reduced real ZIA pack root;
  - no credential, network, live backend, provider, or Apply operation ran.
- Tests not run and why:
  - no real Terraform backend/provider run: acceptance explicitly uses fake
    Terraform and requires no credentials;
  - no assessment or Apply tests as acceptance for this slice: those commands
    remain on their existing Python paths and are later authorized slices.

## Known Deferrals

- Deferred work:
  - generic saved-plan assessment and guidance;
  - generic exact-plan Apply;
  - operational runtime/release cutover readiness;
  - external live backend and credential validation.
- Reason it is safe to defer: the saved plan and fingerprint-v2 pair are the
  unchanged boundary; assessment and Apply remain on their existing Python
  implementation until their own reviewed slices.
- Follow-up owner or trigger: the fixed stacked sequence after this review and
  draft PR; Slice 4 remains blocked because the supplied authorization text is
  truncated before its acceptance/review/scope tail.

## Review Focus

- Highest-risk files or paths:
  - `node-src/domain/plan-lifecycle.ts`
  - `node-src/io/terraform-command.ts`
  - loaded variants in `plan-roots.ts` and `scope-paths.ts`
  - CLI and Make wiring
- Specific assumptions to attack:
  - selected resources must expand to complete materialized roots and emit the
    whole-root note exactly once per selected materialized root;
  - JSON/HCL tfvars selection, grouped partial-config failure, backend config,
    state key, and absolute Terraform arguments must match Python;
  - saved-plan cleanup must cover prior artifacts, failed Terraform, missing
    output, and both init/plan mutation windows without deleting unrelated
    files;
  - fingerprint-v2 bytes and input sets must remain exact;
  - no-deadline and very long explicit timeouts must avoid timer overflow and
    remain independent of adapter-owned mocked clocks;
  - inherited output must stream both channels, enforce bounds, reap process
    groups, and fail nonzero without leaking captured output into errors;
  - executable resolution must not confuse Windows separators, relative paths,
    or PATH lookup.
- Source evidence the reviewer should verify: the exact Python functions and
  retained tests listed above, plus existing generic Node primitives rather
  than this handoff's summary.
- Generated artifacts the reviewer should compare: query JSON bytes,
  Terraform argv logs, and `tfplan.sources` bytes from disposable fixtures.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  reduced pack roots, invalid discovered tenant names, grouped roots across
  selectors, missing/partial configs, remote backend omission, plan output
  mutation, stale prior pairs, partial plan files, executable aliases, output
  backpressure, and explicit empty tenant values.
