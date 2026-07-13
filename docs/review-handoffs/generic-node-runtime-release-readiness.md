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

- Production/release/documentation (15 files; exact additions/deletions are
  reported with the final review request):
  - `.github/workflows/check.yml`
  - `Makefile`
  - `README.md`
  - `demo/Makefile`
  - `docs/adoption-command-surface.md`
  - `docs/integration-validation.md`
  - `docs/node-process-api.md`
  - `docs/operational-runtime.md`
  - `node-src/cli/main.ts`
  - `node-src/domain/plan-report.ts`
  - `node-src/domain/transform-artifacts.ts`
  - `scripts/build-metadata-cli.mjs`
  - `scripts/release.sh`
  - `scripts/test-runtime-release.mjs`
  - `scripts/verify-runtime-release.mjs`
- Tests:
  - `node-tests/adopt-runner.test.ts`
  - `node-tests/cli-bundle.test.ts`
  - `node-tests/metadata-cli.test.ts`
  - `node-tests/operational-runtime-smoke.test.ts`
  - `node-tests/plan-cli.test.ts`
  - `node-tests/plan-report.test.ts`
  - `node-tests/transform-runtime-artifacts.test.ts`
  - `tests/test_makefile_overlay.py`
- Handoff: this file. Tests and this handoff are excluded from the line trigger;
  the final remediation adds nine non-test production/release/documentation
  files to the previously reviewed #208 change set, below the authorized ten-
  file trigger, and remains well below the 1,200-line trigger.
- Intentionally untouched: accepted PR #203/#206/#207 commits, authoring PR
  #200/#201/#202/#204/#205 heads, PR #191/#192 heads, transition catalogs,
  frozen process-host/collector-child code and package entries, Python
  implementation and provider/engine tests, external pipelines, credentials,
  live backends, providers, and deployment state. One retained Python Make
  overlay test is updated to the accepted deployment-precedence contract.

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
- Make now resolves one deployment authority and exports it to every recipe
  and nested Make invocation. All deployment-consuming operational targets are
  exercised with unset, environment-only, Make-only, and conflicting inputs.
- Existing generated moves are durable unresolved evidence: identical or
  empty re-derivations preserve exact bytes, while a different move set fails
  before any persistent artifact changes. Transform and Adopt share the same
  writer; staging and plan consumption are covered.
- Operational selected-module generation and validation expand through the
  deployment's existing root topology, so either grouped member materializes
  the complete module set referenced by `gen-env`.
- The stripped exact-archive smoke runs `make demo-contract` with npm/Python
  tripwires and fake Terraform after operational Python, catalogs,
  `node_modules`, and legacy bundles are removed.

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
- Unresolved generated move evidence is never silently replaced or removed.
  Explicit operator removal is required after confirming the corresponding
  state migration; no receipt, acknowledgement protocol, or state inspection
  was added.

## Tests Run

- Focused pre-review checks:
  - deployment authority, module/root topology, Transform/Adopt artifacts,
    staging, and plan lifecycle: 64/64 passed;
  - generic CLI bundle/inventory and plan-report suites from the original
    readiness slice: 11/11 passed;
  - built relocated Python-disabled operational workflow: 1/1 passed;
  - generic runtime verifier passed across all 11 profiles;
  - retained Python Make/overlay suite after the deployment-precedence update:
    12/12 passed;
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

## Review Remediation

- Finding: Make could export deployment A while explicitly passing deployment
  B to selected CLI commands.
- Fix: the imported `INFRAWRIGHT_DEPLOYMENT` fallback is snapshotted before
  `DEPLOYMENT` is resolved, then the exported `INFRAWRIGHT_DEPLOYMENT` expands
  lazily from `DEPLOYMENT` in the active target context. Command-line, global,
  included-overlay target-specific, and nested Make values therefore remain
  coherent, and every explicit `--deployment` matches. A recording fake CLI
  covers all 16 deployment-consuming targets plus the nested demo override.
- Finding: a second Transform/Adopt could erase the only unapplied move after
  the imports baseline advanced.
- Fix: move derivation and conflict checks precede persistent writes. Existing
  empty/identical re-derivations preserve exact bytes; a different move fails
  closed before config, lookup, binding, imports, or moves change. Transform,
  Adopt, staging, and a subsequent plan are covered.
- Finding: selected module generation was exact-resource while `gen-env`
  selected a complete grouped root.
- Fix: the operational module CLI resolves selected resources through the
  existing root topology. Selecting either member of a two-member group
  generates and validates both, and the resulting root references only present
  modules; ungrouped generation remains exact.
- Finding: `demo-modules` always entered the development npm rebuild path.
- Fix: it depends on the shipped bundle file, while `metadata-cli` remains the
  explicit rebuild target. The exact-archive stripped-runtime smoke now runs
  the complete demo contract under npm/Python tripwires.

- Finding: operational Make targets could treat fresh-checkout source mtimes as
  authority to rebuild the shipped bundle after release staging removed
  `node_modules`.
- Root cause: the same timestamp-driven `metadata-cli` prerequisite served as
  both the runtime existence check and the explicit development rebuild path.
- Fix: operational targets now depend only on the bundle file, whose missing-
  file recipe builds it; `make metadata-cli` is the explicit unconditional
  developer rebuild. The exact-commit release smoke makes a source newer than
  the bundle, installs an npm tripwire, and runs `make resources` successfully
  from the dependency/Python/catalog/legacy-stripped runtime.
- Finding: lazy Terraform adapter resolution allowed `plan --save` to remove a
  saved pair before the low-level unsupported-Windows guard ran.
- Root cause: low-level runner/show guards protected spawn and show preflight,
  but the operational CLI entered domain preparation before requesting its
  lazy adapter.
- Fix: one pre-dispatch guard now rejects every CLI route that can execute
  Terraform (including conditional state-aware staging and module generation),
  while syntactically genuine help and non-executing metadata/rendering routes
  remain portable. Help-looking option values cannot bypass the guard. A
  win32-mocked built-CLI regression proves missing metadata/executable paths
  are not inspected and an existing saved pair remains byte-identical.
- Final patch review finding: the first deployment export used immediate
  assignment, so target-specific `DEPLOYMENT` from an included Makefile could
  leave the exported environment at the global value while argv used the
  target value.
- Final patch review fix: preserve only the imported fallback immediately and
  export the resolved authority recursively. An included-overlay regression
  applies one target-specific deployment to all 16 deployment-consuming
  operational targets and requires environment/argv equality.
- Exact-head CI evidence correction: the retained Make overlay test still
  expected the superseded ambient-environment precedence. It now asserts that
  an explicit `DEPLOYMENT` wins while a nonempty imported
  `INFRAWRIGHT_DEPLOYMENT` remains the fallback when `DEPLOYMENT` is absent;
  no production behavior changed in this correction.

## Historical #194/#195 CI Record

- #194's focused generator review/tests and #195's focused transform review/
  tests passed as recorded in their PR bodies. In both historical workflow
  runs, every matrix job except `node-process-api` succeeded.
- The `node-process-api` jobs were cancelled at the GitHub six-hour ceiling
  while still in `Type, unit, and Python differential checks`; they did not
  report a test assertion failure.
- Downstream commit `f9020577d850f7d172f97a29c3928852d50c866c`
  disabled the HashiCorp Terraform wrapper so stdin-based `terraform fmt -`
  calls reach Terraform directly.
- Those historical branches are intentionally not restacked. The cumulative
  exact corrected-head #208 CI result is supplied in the PR evidence after the
  final commit, because a commit cannot contain its own future CI result.

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
