# Generic Metadata Loader — Builder Review Handoff

## Intent

- Replace the generic Python pack-set, pack-authoring, registry, override,
  provider-schema, and deployment-query loading seam with ordinary TypeScript
  library functions over the existing repository metadata.
- Expose one composed `loadPackRoot(...)` result whose `resources` map joins
  each registry entry to its provider owner and optional existing override.
- Add a thin `infrawright` CLI and move the existing Make metadata checks and
  deployment queries that this PR can actually satisfy to Node.
- Preserve physically reduced pack roots, exact profile selection, shared
  dependency closure, provider ownership, resource/override metadata, schema
  lookup, deployment paths, and existing error/exit behavior where externally
  relevant.
- Keep every Fetch, transform, Oracle/adopt, module/environment generator,
  import-staging, plan/assess, and apply operation unchanged and Python-backed.
- Do not change or consume the frozen PR #191/#192 product workflows.

## Base / Head

- Base: `154a74305d58f4ee88e2de21fb9cb8d826b45d70`
  (`origin/main` when the worktree was created).
- Head: working tree on `feature/node-generic-metadata-loader`.
- Diff command: `git diff 154a74305d58f4ee88e2de21fb9cb8d826b45d70`
  plus the untracked files listed by `git status --short`.

## Files Changed

- Files:
  - `node-src/metadata/validation.ts`
  - `node-src/metadata/packs.ts`
  - `node-src/metadata/resources.ts`
  - `node-src/metadata/loader.ts`
  - `node-src/domain/deployment.ts`
  - `node-src/cli/main.ts`
  - `scripts/build-metadata-cli.mjs`
  - `node-tests/metadata-loader.test.ts`
  - `node-tests/metadata-cli.test.ts`
  - `node-tests/deployment.test.ts`
  - `Makefile`
  - `package.json`
  - `package-lock.json`
  - `.github/workflows/check.yml`
  - `tests/test_check_pack.py`
  - `tests/test_makefile_overlay.py`
  - `docs/pack-authoring.md`
  - this handoff
- Files intentionally left untouched:
  - Existing `packsets/`, `packs/*/pack.json`, registries, overrides, provider
    schemas, and `deployment.json` remain the only metadata model.
  - Existing transition catalogs and the frozen ZCC process host remain
    unchanged.
  - PR #191 and PR #192 remain open, draft, and untouched.
  - All product collectors, transforms, Oracle/adopt code, generators,
    Terraform lifecycle operations, and pipeline definitions outside the
    metadata-check setup remain unchanged.

## Source Inputs Consulted

- Provider schemas: all existing `packs/*/schemas/provider/*.json`; committed
  counts checked for ZCC (7), ZIA (74), ZPA (55), and ZTC (16).
- OpenAPI/API contracts: N/A; no API or network behavior changes.
- Provider source files: N/A; no provider execution or source mapping changes.
- Pack metadata:
  - all documents under `packsets/`;
  - every `packs/*/pack.json`;
  - every `packs/*/registry.json`;
  - all 59 existing override JSON documents;
  - the shared-component layout under `packs/_shared/`.
- Existing docs or design records:
  - `docs/adversarial-review.md` and its templates;
  - `docs/pack-authoring.md`;
  - the user-approved horizontal migration baseline.
- Other source evidence:
  - Python reference implementations in `engine/pack_set.py`,
    `engine/packs.py`, `engine/registry.py`, `engine/overrides.py`,
    `engine/tfschema.py`, `engine/deployment.py`, and `engine/check_pack.py`;
  - their existing Python tests.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None.
- Snapshots: None.
- Demo or lab outputs: None retained; `make check` used disposable outputs.
- Artifact drift intentionally expected: None. `dist/` and `.node-test/` are
  ignored build products and are not part of the diff.

## Expected Delta

- Expected behavior change:
  - TypeScript callers can load the original metadata through
    `loadPackRoot(...)` and access `resources.get(resourceType)` directly.
  - `make check-pack`, `make check-pack-set`, the requirements decision in
    `check-examples`, deployment queries in `check-tfvars-fmt`, and the demo
    contract's module-directory query now invoke the thin Node CLI.
  - Those metadata commands require Node 24 and pinned npm dependencies; the
    affected CI jobs install them, including inside physically pruned
    worktrees.
- Expected report/count/coverage changes: None. The composed full root exposes
  the existing 151 registry resources and 59 overrides.
- Expected generated-output changes: None.
- Expected no-op areas: network calls, credentials, provider execution,
  generated Terraform bytes, adoption behavior, transition catalogs, plan
  assessment, and apply behavior.

## Invariants Claimed

- Evidence must not be silently dropped: manifest, registry, and override
  vocabularies remain closed; manifestless top-level pack directories still
  count at the exact-profile boundary; missing schemas and misspelled resources
  fail rather than disappear.
- Generic matcher evidence must not outrank source-backed evidence: N/A; this
  PR does not evaluate provider evidence or matching rules.
- Source precedence/provenance must remain explicit: explicit CLI arguments
  override environment-selected pack/deployment paths, which override the
  installed defaults; provider ownership comes only from `provider_prefixes`.
- Ambiguity must stay classified instead of being coerced to success: duplicate
  provider/prefix ownership and duplicate registry resources fail; shared
  dependencies must exist; profile missing/extra selections remain errors.
- Provider-readiness counts must stay explainable: N/A; no readiness report is
  changed.
- Adoption safety invariants: no adoption operation is introduced. Registry
  `adopt` and override identity metadata are loaded and structurally validated
  without applying behavior.

## Tests Run

- Commands:
  - `npm ci --ignore-scripts`
  - `npm run typecheck`
  - `npm run build`
  - `npm test`
  - focused compiled tests for metadata loader, CLI, and deployment
  - `make PYTHON=false check-pack check-pack-set check-tfvars-fmt`
  - `make check`
  - focused Python reference suites for pack-set, pack, registry, deployment,
    schema, and Make integration
  - `git diff --check`
- Relevant output summary:
  - full Node suite before the final composition-only loader addition: 843
    passed, 1 skipped, 0 failed;
  - final focused loader/CLI/deployment suite: 30 passed, 0 failed;
  - full Python/Make gate: 1400 passed, 1 skipped, 0 failed; module, demo,
    formatting, pack, and vendor-boundary checks all passed;
  - production process and metadata CLI bundles built successfully;
  - Python-disabled metadata Make commands passed;
  - broad test materialized and loaded `empty`, every individual provider,
    `zscaler`, and `full` from physically reduced pack roots.
- Tests not run and why:
  - No credentials, network, provider, or live Terraform run: the PR changes
    metadata loading only and the approved scope explicitly excludes them.
  - No hostile-filesystem review: explicitly excluded for this subsystem
    review; ordinary reduced-root and existing symlink-following semantics are
    retained.

## Review Remediation

- Finding: empty pack-selection environment variables selected the current
  directory instead of the installed defaults.
  - Root cause: CLI precedence used nullish fallback while Python uses falsey
    fallback for `INFRAWRIGHT_PACKS` and `INFRAWRIGHT_PACK_PROFILE`.
  - Fix: empty environment values now fall back to the installed pack root and
    full profile; explicit non-empty CLI arguments still win.
  - Regression test: `metadata-cli.test.ts` executes both commands with empty
    environment variables and requires the committed full selection.
  - Verification: focused suite and Python-disabled Make metadata commands pass.
- Finding: ordinary `JSON.parse` rounded wide integers and lost the lexical
  distinction between pack-set versions `1` and `1.0`.
  - Root cause: JavaScript's number graph was accepted as the metadata graph
    without inspecting numeric source tokens.
  - Fix: the Node 24 JSON reviver source preserves out-of-safe-range and
    non-finite tokens as `LosslessNumber`; safe integers/floats remain ordinary
    numbers. Pack-set documents preserve every numeric token through version
    validation and accept only the integer token `1`.
  - Regression test: `metadata-loader.test.ts` requires exact retention of a
    registry and override integer `9007199254740993`, keeps
    `9007199254740991` numeric, and rejects lexical version `1.0`.
  - Verification: focused suite, typecheck, bundle build, and whitespace gate
    pass.
- Non-blocking review items folded in:
  - CLI usage/help now has explicit exit 0/2 handling and tests;
  - `package-lock.json` records the new `infrawright` executable;
  - direct CLI authoring docs now build the metadata bundle after install.
- Focused recheck finding: lossless numeric wrappers passed the JSON-object
  predicate.
  - Root cause: `isObject` excluded arrays/null but did not constrain the
    prototype or exclude `LosslessNumber`.
  - Fix: metadata objects must now have `Object.prototype` or a null prototype;
    lossless numbers remain scalar metadata values only.
  - Regression test: a wide numeric token supplied as `fetch.query` now fails
    with `query must be an object`.

## Known Deferrals

- Deferred work:
  - schema interpretation and module generation;
  - transform/artifact rendering;
  - collector framework and product adapters;
  - generic Oracle/adopt;
  - environment generation and import staging;
  - plan/assessment and apply coordination;
  - the existing Make `MODULE_DIR` compatibility default and all operational
    Python recipes not named in Expected Delta;
  - a generic `infrawright resources` command, because replacing the current
    operational resource selection also requires reference-order semantics
    outside this loader PR.
- Reason it is safe to defer: each remains on the unchanged Python path. The
  new loader emits no artifacts and authorizes no provider or Terraform action.
- Follow-up owner or trigger: the fixed horizontal subsystem sequence, starting
  with the generic module generator only after this loader PR is reviewed.

## Review Focus

- Highest-risk files or paths:
  - `node-src/metadata/packs.ts`
  - `node-src/metadata/resources.ts`
  - `node-src/metadata/loader.ts`
  - `node-src/cli/main.ts`
  - metadata-related Make and CI changes
- Specific assumptions to attack:
  - exact profile handling must count manifestless directories;
  - provider and prefix collisions must never become last-writer-wins;
  - reduced pack roots must not fall back to omitted repository packs;
  - single-pack `check-pack` behavior must not accidentally validate or merge
    another pack's registry while ownership/shared closure remains global;
  - registry, override, and schema misspellings must fail rather than produce an
    incomplete resource view;
  - `loadPackRoot.resources` must join the existing entries without silently
    changing provider, product, owner, or override identity;
  - deployment overlay/module/tenant paths must retain current valid-input
    behavior;
  - Make must build the CLI before use in normal, reduced-root, and physically
    pruned CI checkouts.
- Source evidence the reviewer should verify: the Python reference modules and
  existing pack/profile documents listed above.
- Generated artifacts the reviewer should compare: None.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  sorted/duplicate profile names, boolean format versions, missing shared
  components, duplicate providers/prefixes/resources, invalid pagination,
  identity skip predicates, override rename conflicts, provider-schema and
  resource misspellings, and requirements-unavailable exit code 3.
