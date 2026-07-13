# Node Environment-Portability Review Handoff

## Intent

- What problem does this change solve?
  - It gives work machines an explicit checksum-verifying path for an already
    built `dist/infrawright-cli.mjs` without npm, source compilation, or Python.
  - It diagnoses whether a configured npm registry can reproduce the pinned
    source build, derives the required mirror inventory from `package-lock.json`,
    and reports missing exact packages without exposing registry credentials.
  - It makes every retained Node/Python differential use the same explicitly
    selected and version-checked Python oracle.
- What user-visible or maintainer-visible behavior should change?
  - `make verify-runtime` verifies the prebuilt runtime without a `dist` build
    prerequisite.
  - `make source-build-preflight` reports source-build registry readiness;
    `node scripts/build-environment-preflight.mjs --manifest` prints the
    lockfile-derived mirror inventory.
  - Migration tests honor nonempty `PYTHON`, then fall back to `python3` and
    `python`, while accepting only the repository-authorized Python/UCD pairs.
- What behavior must stay unchanged?
  - All operational CLI semantics, bundle format, dependencies, Node 24
    support, provider adapters, artifacts, Terraform lifecycle, and accepted
    PR #208 runtime behavior.
  - No public-registry fallback, automatic install/download, credential access,
    provider/backend contact, or deployment Apply.

## Base / Head

- Base: `529033429f723853c7b6b2c31d5f533a8b99c075`
- Head: `feature/node-environment-portability` (the exact frozen commit is
  supplied in the review prompt)
- Diff command:
  `git diff 529033429f723853c7b6b2c31d5f533a8b99c075...HEAD`

## Files Changed

- Files:
  - `Makefile`
  - `README.md`
  - `docs/integration-validation.md`
  - `docs/operational-runtime.md`
  - `scripts/build-environment-preflight.mjs`
  - `scripts/verify-runtime-release.mjs`
  - `scripts/test-runtime-release.mjs`
  - `node-tests/python-oracle.ts`
  - `node-tests/python-oracle.test.ts`
  - `node-tests/build-environment-preflight.test.ts`
  - 35 existing Node differential test files whose Python executable selection
    changed mechanically to the shared helper.
- Files intentionally left untouched:
  - `node-src/**`, `package.json`, `package-lock.json`, Fetch, Transform, Adopt,
    Oracle, modules, roots, staging, plan, assessment, Apply, provider adapters,
    ADO/work-side configuration, authoring-stack code, and PRs #191/#192.

## Source Inputs Consulted

- Provider schemas: N/A.
- OpenAPI/API contracts: N/A.
- Provider source files: N/A.
- Pack metadata: `packsets/full.json` and selected pack manifests as the existing
  runtime verifier's input contract; their contents do not change.
- Existing docs or design records:
  - `docs/operational-runtime.md`
  - `docs/integration-validation.md`
  - `docs/python-lower-unicode-contract.md`
  - `docs/adversarial-review.md` and its templates.
- Other source evidence:
  - `package.json`, `package-lock.json`, npm lockfile-v3 metadata, existing
    release/build scripts, and the existing runtime release verifier/smoke.
  - The retained Python lowercase authority accepts Python 3.12/UCD 15.0.0 and
    Python 3.13/UCD 15.1.0.

## Generated Artifacts

- Reports: Source-build preflight console result, generated on demand.
- Schemas: None.
- Fixtures: Fake configured-registry and fake Python executable test fixtures,
  generated only in temporary directories.
- Snapshots: None.
- Demo or lab outputs: Existing exact-archive runtime smoke only; no live lab.
- Artifact drift intentionally expected:
  - No committed runtime artifact drift. The final build must reproduce and
    verify the ordinary bundle/checksum release contract.
  - The mirror manifest is deterministically derived from the lockfile and is
    intentionally not a second manually maintained dependency catalog.

## Expected Delta

- Expected behavior change:
  - Prebuilt runtime qualification is explicit and independent of npm/Python.
  - Restricted-registry build failures identify exact missing package versions
    and direct operators to the verified prebuilt artifact.
  - All 63 Python launch sites in 35 retained Node tests consistently select
    the migration oracle.
- Expected report/count/coverage changes:
  - Current lockfile manifest: 15 ordinary packages; two platform packages for
    each supported darwin/linux arm64/x64 pair; one install-script package
    (`esbuild@0.25.12`).
- Expected generated-output changes: None to operational artifacts.
- Expected no-op areas: Every production library and CLI route under
  `node-src/**`; provider, pack, and Terraform behavior.

## Invariants Claimed

- Evidence must not be silently dropped:
  - Registry lookup failures are classified as missing, mismatched, or
    unresolved and emitted with exact package name/version.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit:
  - Mirror inventory and expected integrity come only from the pinned lockfile;
    configured npm registry resolution is checked against them.
- Ambiguity must stay classified instead of being coerced to success:
  - Non-404 resolution failures do not become “missing” or “available”.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - No adoption, plan, Apply, provider, backend, or credential-bearing path is
    changed or exercised.
  - Runtime verification invokes the accepted CLI only for help, metadata,
    pack, and deployment validation.

## Tests Run

- Commands:
  - `npm run build:test`
  - `node --test .node-test/node-tests/python-oracle.test.js .node-test/node-tests/build-environment-preflight.test.js`
  - `PATH=/usr/bin:/bin PYTHON=/run/current-system/sw/bin/python3 /run/current-system/sw/bin/node --test .node-test/node-tests/zia-transform-cohort.test.js .node-test/node-tests/zpa-transform-cohort.test.js .node-test/node-tests/pull-transform-differential.test.js`
  - `node scripts/build-environment-preflight.mjs --manifest`
  - `node scripts/build-environment-preflight.mjs --platform darwin --arch arm64`
  - `node scripts/verify-runtime-release.mjs <accepted-#208-tree> ...`
  - Final gate commands and exact results will be recorded before review.
- Relevant output summary:
  - Focused preflight/oracle suite: 8 passed.
  - ZIA/ZPA/transform differentials with unsupported system Python and explicit
    supported `PYTHON`: 14 passed.
  - Current public-registry darwin/arm64 preflight: source build available;
    exact platform pair is `@esbuild/darwin-arm64@0.25.12` and
    `@typescript/typescript-darwin-arm64@7.0.2`.
  - Simulated restricted-registry miss reports exactly
    `@esbuild/darwin-arm64@0.25.12` and redacts configured credentials and raw
    registry stderr.
- Tests not run and why:
  - No real restricted corporate registry is reachable from this workspace;
    its exact missing set must be obtained by running the credential-safe
    preflight there.
  - No live provider/backend or deployment Apply is authorized.

## Known Deferrals

- Deferred work:
  - Mirror absent packages into the restricted corporate registry.
  - Run the preflight and no-build runtime qualification on the work machine.
- Reason it is safe to defer:
  - Runtime use consumes the checksum-verified prebuilt bundle and does not
    require source-build dependencies.
- Follow-up owner or trigger:
  - Registry/platform owner when internal source compilation becomes required;
    work-side operator for external qualification.

## Review Focus

- Highest-risk files or paths:
  - `scripts/build-environment-preflight.mjs`
  - `scripts/verify-runtime-release.mjs`
  - `scripts/test-runtime-release.mjs`
  - `node-tests/python-oracle.ts`
- Specific assumptions to attack:
  - No credential-bearing registry values or raw npm diagnostics can escape.
  - Exact version and integrity are checked against the lockfile.
  - No public registry fallback or install-script execution occurs.
  - `verify-runtime` cannot cause Make to build the bundle.
  - The stripped runtime truly excludes npm, npx, Python, source, and
    `node_modules` while still exercising operational CLI and fake Terraform.
  - Explicit `PYTHON` paths, including paths with spaces, are executed directly
    and unsupported Python/UCD combinations fail before a differential.
- Source evidence the reviewer should verify:
  - `package-lock.json` package metadata and supported platform package pairs;
    `package.json` Node/build contract; existing release script; existing
    Python/UCD authority.
- Generated artifacts the reviewer should compare:
  - `--manifest` output against `package-lock.json`.
  - Fake restricted-registry failure output for redaction and exact package
    identity.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  - Registry 404 versus transport/auth failure; mismatched integrity;
    scoped-registry bypass; optional package omission; credential-bearing URLs;
    explicit missing `PYTHON`; fallback order; unsupported Unicode authority;
    runtime package-root metadata omitted from a stripped tree.
