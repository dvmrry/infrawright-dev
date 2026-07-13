# Builder Review Handoff — Generic Exact-Plan Apply

## Intent

- Solve: port the generic operational behavior of `engine.ops.cmd_apply` to
  the typed Node library and thin CLI, then switch `make apply` to Node.
- User-visible change: `infrawright apply` and `make apply` now select saved
  plan roots, enforce branch/policy/destroy gates, initialize Terraform,
  classify the saved plan, apply exactly `tfplan`, and clean the saved pair on
  success without invoking Python.
- Must stay unchanged: optional tenant/selectors, whole-root behavior, branch
  environment priority, main-branch overrides, user policy semantics, backend
  key convention, broad override warnings, failure retention, Python root
  iteration behavior, and separate import/move unstaging.

## Base / Head

- Base: `5675552ee032875b616dc77dd57bd5051c5d6484` (draft PR #206).
- Head: frozen head of `feature/node-exact-plan-apply`; the review request
  supplies the resolved commit because a commit cannot contain its own hash.
- Diff command: `git diff 5675552ee032875b616dc77dd57bd5051c5d6484...HEAD`.

## Files Changed

- Production:
  - `Makefile`
  - `node-src/cli/main.ts`
  - `node-src/domain/exact-plan-apply.ts`
  - `node-src/domain/plan-assessment-inputs.ts`
  - `node-src/domain/plan-assessment.ts`
  - `node-src/domain/plan-eval.ts`
  - `node-src/domain/plan-lifecycle.ts`
- Tests:
  - `node-tests/exact-plan-apply.test.ts`
  - `node-tests/plan-eval.test.ts`
- Handoff: this file.
- Intentionally untouched: Python implementation, staged import/move
  lifecycle, planning, report schemas, deployment Apply pipelines, provider
  and resource implementations, draft PRs #191/#192, and all external ADO
  definitions.

## Source Inputs Consulted

- Provider schemas: N/A; this is generic saved-plan orchestration.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: real loaded pack registry/provider/root metadata through
  `loadPackRoot`; no new inventory or catalog.
- Existing docs/design: the authorized Slice-3 contract and
  `docs/adversarial-review.md`.
- Other source evidence:
  - `engine/ops.py`: `_current_branch`, `cmd_apply`, `_destroy_count`,
    `_assert_saved_plan_fresh`, `_check_backend`.
  - `engine/plan_eval.py` and retained Apply/fingerprint/policy tests.
  - Existing Node root selection, fingerprint/evidence, drift policy,
    classifier, Terraform runner/show, backend, and saved-pair primitives.

## Generated Artifacts

- Reports/schemas/fixtures/snapshots: no new persistent contract or generated
  artifact. Private saved-plan snapshots are existing in-process evidence and
  are scrubbed/removed before return.
- Demo/lab outputs: none.
- Expected artifact drift: successful Apply removes only `tfplan` and
  `tfplan.sources`; every failure retains both. Staged imports/moves remain.

## Expected Delta

- `make apply` invokes the generic Node CLI with Python unavailable.
- A new thin `infrawright apply` command exposes existing Apply options plus
  the standard injected Terraform path used by repository tests/pipelines.
- Import-only creates carrying a non-empty Terraform import marker classify as
  clean, restoring Python classifier behavior required by both assessment and
  Apply.
- No report/count/coverage contract changes.
- No plan, re-plan, generated-config Apply, provider call, credential use, or
  live deployment Apply occurs in repository verification.

## Invariants Claimed

- Evidence is not silently dropped: the plan/fingerprint/current inputs,
  deployment control file, loaded root context, and user policy are rechecked
  after init and immediately before Apply.
- The plan shown for classification is a private snapshot bound to the exact
  original plan; the original is rehashed immediately before Terraform is
  allowed to apply relative `tfplan`.
- Apply argv is exactly `apply -input=false tfplan`; no re-plan or directory
  Apply exists.
- Blocked plans require the explicit broad override. Delete/replace plans also
  require the independent destroy override.
- Failed init/show/classification/Apply retains the saved pair and stops root
  iteration. Successful roots remove only their pair, and later roots remain
  selectable after earlier cleanup.
- Root grouping and backend keys remain deployment-owned and whole-root.
- Generic matcher/source precedence/provenance/ambiguity/provider-readiness
  invariants are N/A; this change does not touch those systems.

## Tests Run

- Focused Node checkpoint:
  - typecheck and test compilation passed;
  - 109 affected tests passed with one platform skip;
  - branch priority/fallback, main and non-main gates, no-plan failure,
    import-only, tolerated/blocked/destroy paths, broad override, init and
    post-show mutations, exact argv, streaming, grouped roots, backend args,
    multi-root cleanup, failure retention, Python diagnostic differential,
    and Python-disabled Make were exercised.
- Retained Python checkpoint:
  - 82/82 fingerprint, Apply-safety, classifier, and drift-policy tests passed.
- Final local gate (2026-07-13 11:09:02–11:11:32 EDT): typecheck and both
  production builds passed; the affected Node corpus passed 109 tests with one
  platform skip; the retained Python corpus passed 82/82; vendor audit found
  187 allowed matches and zero violations; whitespace passed; the one full
  Node suite passed 1,070 tests with one platform skip and zero failures
  (1,071 total).
- Tests not run: no real Terraform provider, backend, credential, or live
  deployment Apply is authorized for this slice.

## Known Deferrals

- Real deployment Apply evidence is explicitly forbidden for repository
  acceptance and belongs to later external pipeline validation.
- Operational runtime/release cutover is Slice 4 and is not implemented here.
- The supplied Slice-4 prompt is truncated before its full acceptance/review/
  stop requirements, so it cannot be started without the missing tail.
- Old Python implementation remains as retained migration evidence/tests; its
  production Make route is replaced.

## Review Remediation

- Finding: the initial test corpus proved single-root failure retention and
  all-success multi-root cleanup separately, but did not directly compose root
  N failure with prior/later root lifecycle.
- Root cause: the builder checkpoint fixed the successful multi-root context
  recheck but stopped one case short of the full Python iteration contract.
- Fix: production code was unchanged; a three-root regression now fails the
  second Apply and proves the first pair is removed, the failed and later pairs
  remain, and the later root is never attempted.
- Verification: typecheck, test compilation, whitespace, and the patch-focused
  Apply suite passed 19/19.
- CI finding: the pack-profile jobs run against a pull-request merge ref. An
  explicitly empty `TENANT=` therefore reached the valid-request branch gate
  before the CLI rejected the malformed option, masking the stable
  `INVALID_TENANT` diagnostic required by the Make overlay contract.
- CI fix: the thin Apply CLI now validates a supplied tenant while parsing the
  request, before entering the unchanged branch-first domain transaction.
  Valid requests retain the reviewed branch/policy/input order. A regression
  exercises `--tenant ""` under `refs/pull/207/merge` and proves the tenant
  diagnostic wins without loading Apply inputs.
- CI verification: the exact retained Make overlay regression passed;
  typecheck and test compilation passed; and the focused Apply suite passed
  19/19.

## Review Focus

- Highest risk: `node-src/domain/exact-plan-apply.ts`, the import-only branch in
  `plan-eval.ts`, and CLI/Make option wiring.
- Attack branch-resolution priority and fallback; policy-before-input order;
  root selection after a prior successful cleanup; plan/fingerprint/policy/
  deployment mutation around init/show; backend key/config behavior; destroy
  and broad override interaction; exact relative `tfplan` argv; and retention
  on every failure.
- Verify no re-plan, receipt, transaction protocol, new schema/catalog,
  provider/resource assumption, automatic unstaging, or real Apply is hidden
  in the diff.
- Compare the retained Python Apply safety tests and fake-Terraform command
  logs rather than relying on this summary.
