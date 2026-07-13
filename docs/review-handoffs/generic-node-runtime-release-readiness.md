# Builder Review Handoff — Generic Node Runtime and Release Readiness

## Intent

- Prove that the repository's production adoption command surface is owned by
  the generic Node 24 library and `infrawright` CLI and does not require Python
  at runtime.
- Make `dist/infrawright-cli.mjs` plus its SHA-256 checksum the primary
  no-install runtime artifact, stage and verify it safely, and exercise one
  bounded import-only workflow through the relocated built bundle.
- Preserve Python tests, differential oracles, probes, authoring/research
  tools, and all frozen ZCC migration artifacts. Preserve every prior accepted
  operational commit and both untouched authoring and historical ZIA stacks.
- This change establishes repository/release readiness only. It must not claim
  live qualification, switch ADO, contact a real provider/backend, or perform a
  deployment mutation.

## Base / Head

- Base: `163f4a60f049fe06849d0a93a576ddc75f9d7e13`, accepted PR #207 head.
- Head: frozen head of `feature/node-runtime-release-readiness`; the review
  request supplies the resolved commit because a commit cannot contain its own
  hash.
- Accepted ancestry independently preflighted before edits:
  `e023ce2ec43498f179981dbfa750d3101bb2b273` (#203) ->
  `b574ac8c3d3778c4f54b95b289f9d001f7a52021` (#206) -> base (#207).
- Diff command:
  `git diff 163f4a60f049fe06849d0a93a576ddc75f9d7e13...HEAD`.

## Files Changed

- Production/release/documentation (13 files, approximately 540 net lines):
  - `.github/workflows/check.yml`
  - `Makefile`
  - `README.md`
  - `docs/adoption-command-surface.md`
  - `docs/integration-validation.md`
  - `docs/node-process-api.md`
  - `docs/operational-runtime.md`
  - `node-src/domain/plan-report.ts`
  - `package.json`
  - `scripts/build-metadata-cli.mjs`
  - `scripts/release.sh`
  - `scripts/test-runtime-release.mjs`
  - `scripts/verify-runtime-release.mjs`
- Tests:
  - `node-tests/cli-bundle.test.ts`
  - `node-tests/operational-runtime-smoke.test.ts`
  - `node-tests/plan-report.test.ts`
- Handoff: this file. Tests and this handoff are excluded from the line trigger;
  this handoff is the fourteenth non-test changed file if counted toward the
  file trigger, so neither authorized trigger is exceeded.
- Intentionally untouched: accepted PR #203/#206/#207 commits, authoring PR
  #200/#201/#202/#204/#205 heads, PR #191/#192 heads, transition catalogs,
  frozen process-host/collector-child code and package entries, Python
  implementation and tests, external pipelines, credentials, live backends,
  providers, and deployment state.

## Source Inputs Consulted

- Provider schemas: the existing ZIA URL Categories schema selected through
  the real pack/profile loader in the disposable operational smoke; unchanged.
- OpenAPI/API contracts: N/A; the smoke uses the existing real ZIA collection
  registry against a loopback HTTPS fixture.
- Provider source files: N/A; no provider or resource behavior is changed.
- Pack metadata: existing `packs/zia`, `packs/_shared/zscaler`, `packsets/zia.json`,
  `packsets/full.json`, and the normal generic metadata loader/validators.
- Existing docs/design: `docs/adversarial-review.md`, its handoff/review
  templates, the accepted #203/#206/#207 handoffs, current command-surface,
  integration, release, and frozen process-API documentation.
- Other source evidence: actual root `Makefile` expansion, built CLI help,
  `package.json`/lockfile, existing build/release scripts, committed ZIA URL
  Categories API/transform golden fixtures, and the accepted plan/fingerprint/
  assessment/exact-Apply implementations.

## Generated Artifacts

- Reports: one disposable v1 saved-plan assessment report from the fake
  import-only smoke; no report is committed.
- Schemas: none.
- Fixtures/snapshots: no new persistent source fixture. Disposable pull,
  tfvars, imports, lookup, module, root, `tfplan`, fingerprint, and command-log
  files are removed after the smoke.
- Release outputs: disposable exact-commit archives build
  `dist/infrawright-cli.mjs` and `dist/infrawright-cli.mjs.sha256`; `dist/`
  remains ignored and no built bundle is committed.
- Artifact drift intentionally expected: the production build now always
  emits the generic checksum; release staging force-adds the generic bundle and
  checksum in addition to unchanged legacy artifacts. No committed golden byte
  changes are expected.

## Expected Delta

- `deployment`, `resources`, reference-ordered resources, module generation,
  and module validation gain thin Make adapters over already-implemented
  generic CLI/library behavior.
- One marked documentation table becomes the authoritative 22-command
  operational inventory. A test asks Make itself to expand every recipe and
  cross-checks built CLI help; it rejects Python or bypass routes.
- The generic bundle gains a conventional SHA-256 file. A stdlib verifier
  checks the executable/package/profile/pack contract and exercises a relocated
  bundle with no `node_modules`.
- A local-only `git archive` smoke builds an exact committed tree, removes
  runtime install/test output and then Python, `engine/`, transition catalogs,
  and legacy bundles from a copy, and proves the generic runtime still works.
- CI runs that safe staging smoke. It never invokes the tag/push release path.
- The built CLI smoke performs metadata validation, module generation and
  validation, loopback ZIA Fetch, Transform, root generation, staging, saved
  plan, assessment, exact fake Apply, and unstaging with Python fail shims.
- A real import-only plan produces internal clean classification evidence.
  Since v1 permits `clean` only as an aggregate status, `plan-report.ts` now
  omits clean findings at the report boundary while preserving the classifier,
  root result, exact-plan authorization, and strict v1 schema.
- Documentation now identifies the generic bundle as primary and frozen ZCC
  process artifacts as retained legacy migration infrastructure.

## Invariants Claimed

- Evidence is not silently dropped: the smoke checks the saved plan bytes and
  fingerprint survive assessment unchanged, the report binds both, and exact
  Apply removes only `tfplan` and `tfplan.sources` after success.
- Apply invokes exactly `apply -input=false tfplan`, never replans, and the
  fake executable performs no remote mutation.
- Clean import-only actions remain available to internal classification and
  Apply authorization even though non-reportable clean findings are omitted
  from the strict v1 report.
- The primary runtime works from outside the checkout with explicit pack,
  profile, catalog, and deployment paths; it needs Node 24 but no runtime npm
  install, `node_modules`, Python, transition catalogs, or collector-child.
- The authoritative Make inventory cannot pass via a single-line grep: it
  exercises aliases, target-specific variables, continuations, and fully
  expanded recipes through Make.
- Linux remains the production Terraform platform, macOS is supported for
  development/testing, and Windows Terraform execution remains rejected by
  the accepted pre-preflight guard. No Windows support machinery is added.
- Generic matcher/source precedence/provenance/provider-readiness invariants
  are unchanged; this slice does not alter those systems or claim additional
  provider coverage.

## Tests Run

- Focused pre-review checks:
  - generic CLI bundle/inventory and plan-report suites: 11/11 passed;
  - built relocated Python-disabled operational workflow: 1/1 passed;
  - generic runtime verifier passed across all 11 profiles;
  - JavaScript syntax and whitespace checks passed.
- The final exact-commit gate runs once after this handoff is committed:
  typecheck, production build, generic verifier, local release staging/relocated
  smoke, vendor-boundary audit, whitespace, production dependency audit, and
  one complete `npm test`. Its exact result is supplied with the review request.
- Tests intentionally not run: live credentials, real provider, live backend,
  deployment Apply, external ADO/work-side lanes, tags, pushes, and public
  release operations are forbidden for this slice.

## Known Deferrals

- Independent final operational-stack review follows this draft PR.
- Read-only live qualification and the separately authorized import-only Apply
  plus fresh-workspace no-op proof are external work-environment steps.
- Main integration, authoring-stack rebase/integration, and any cleanup/archive
  of Python or frozen migration architecture require separate approval.
- Unknown external consumers of the legacy bundles/process host are why their
  artifacts and separately labeled release guard remain intact.

## Review Focus

- Review only operational command completeness, Python runtime independence,
  bundle/checksum self-containment, safe exact-commit release staging,
  relocated execution, the fake end-to-end workflow, exact Apply argv,
  platform-policy consistency, qualification/cutover claim boundaries, legacy
  preservation, and scope compliance.
- Attack the marked inventory parser with aliases, target-specific variables,
  prerequisites, multiline recipes, and Python hidden through variable
  expansion. Verify each listed route exists in built help.
- Attack the release verifier/staging smoke for accidental reliance on the
  original checkout, `node_modules`, Python, catalogs, or legacy bundles, and
  verify it cannot create a tag or release.
- Verify the operational smoke uses the built relocated bundle, loopback-only
  Fetch, disposable inputs, real artifact paths, exact saved-pair assessment,
  one plan, one exact Apply, selective cleanup, and no Python invocation.
- Verify filtering clean findings is only an emitted-contract adaptation and
  cannot convert blocked/tolerated roots or guidance into clean.
- Do not demand deletion of frozen migration architecture, review every ZCC
  operation, require credentials/ADO/Windows Terraform, add signing or
  provenance infrastructure, redesign packaging, or port authoring tools.
