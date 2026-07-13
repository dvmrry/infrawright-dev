# Generic Node Module Generator — Builder Review Handoff

## Intent

- Port the generic product logic in `engine/gen_module.py` to ordinary Node 24
  library functions that render one seven-file Terraform module, generate one
  or every active generated-resource module, and validate a generated tree.
- Consume active profiles, resource inventory, provider ownership/source/pin,
  schemas, JSON overrides, resource-owned HCL overrides, and deployment module
  directories through the generic loader introduced by PR #193.
- Add a thin `infrawright modules generate|validate` adapter and switch only
  `make check-modules`, isolated demo module generation, demo tree validation,
  and the unused `MODULE_DIR` compatibility query from Python to Node.
- Preserve byte-for-byte Python generator output after the same Terraform
  formatting step, including the temporary Python regeneration wording.
- Leave Fetch, transform, Oracle/adopt, environment/root generation, grouping,
  references, import staging, plan/assessment, Apply, and PR #191/#192 untouched.

## Base / Head

- Base: `09cce9f158977dcf03f6c3f3a2169848d70a6ba7` (draft PR #193 head).
- Head: working tree on `feature/node-module-generator`.
- Diff command:
  `git diff 09cce9f158977dcf03f6c3f3a2169848d70a6ba7` plus untracked files from
  `git status --short`.

## Files Changed

- Files:
  - `node-src/modules/generator.ts`
  - `node-src/metadata/loader.ts`
  - `node-src/metadata/resources.ts`
  - `node-src/cli/main.ts`
  - `Makefile`
  - `demo/Makefile`
  - `node-tests/module-generator.test.ts`
  - `node-tests/metadata-cli.test.ts`
  - `tests/test_makefile_overlay.py`
  - `.github/workflows/check.yml`
  - this handoff
- Files intentionally left untouched:
  - `engine/gen_module.py`, `engine/tfschema.py`, and their Python tests remain
    as the protected migration oracle.
  - Existing pack profiles, manifests, registries, provider schemas, JSON
    overrides, committed HCL goldens, and deployment documents remain the only
    metadata and fixture authorities.
  - No generated module tree is committed.
  - All non-module Python operational paths and PR #191/#192 are untouched.

## Source Inputs Consulted

- Provider schemas: every active committed schema reached through
  `loadPackRoot().loadResourceSchema(...)`; representative direct review of
  ZCC, ZIA, ZPA, and ZTC schema shapes.
- OpenAPI/API contracts: N/A; no collection or API behavior.
- Provider source files: N/A; no provider execution or source mapping.
- Pack metadata:
  - every profile under `packsets/`;
  - provider ownership, source, and pin from every active `pack.json`;
  - all active `registry.json` inventories and JSON overrides;
  - the documented `overrides/<resource>/main.tf` path, currently exercised by
    a disposable synthetic pack because no committed pack has such a file.
- Existing docs or design records:
  - `docs/adversarial-review.md` and its templates;
  - `packs/_shared/zscaler/overrides-README.md`;
  - the user-approved fixed subsystem scope.
- Other source evidence:
  - `engine/gen_module.py` and module-relevant `engine/tfschema.py` helpers;
  - `tests/test_gen_module.py`, `tests/test_tfschema.py`, and all 68 committed
    HCL goldens under `tests/fixtures/gen/`.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures: None added or changed.
- Snapshots: None.
- Demo or lab outputs:
  - ignored `demo/modules/default` generated and validated with Python pointed
    at a nonexistent executable;
  - disposable full Python and Node module trees generated independently.
- Artifact drift intentionally expected: None. The independently generated
  151-module trees were byte-identical.

## Expected Delta

- Expected behavior change:
  - Node callers can render/generate/validate the existing module contract;
  - the thin CLI honors explicit `--terraform`, then `TF`, then `terraform`;
  - module output defaults to the deployment-selected module directory;
  - `make check-modules`, `demo-modules`, and demo contract validation use Node
    and work while Python is unavailable.
- Expected report/count/coverage changes: None.
- Expected generated-output changes: None: full-corpus output must be byte
  identical to Python after Terraform formatting.
- Expected no-op areas: every non-module operational stage, Terraform roots,
  grouping, references, state, provider execution, plan, and Apply.

## Invariants Claimed

- Evidence must not be silently dropped: every active `generate=true` resource
  must receive exactly the expected seven paths; missing paths fail tree
  validation with concrete diagnostics.
- Generic matcher evidence must not outrank source-backed evidence: N/A.
- Source precedence/provenance must remain explicit: profile/pack loader owns
  resource inventory and provider facts; pack metadata owns source/pin; schema
  owns types; JSON and resource-owned HCL overrides retain their existing
  precedence; deployment owns the default output directory.
- Ambiguity must stay classified instead of being coerced to success: unknown
  resources, missing/contradictory source metadata, missing pins, missing schema
  members, unsupported type/nesting modes, bad samples, formatter failures, and
  incomplete trees fail loudly.
- Provider-readiness counts must stay explainable: N/A.
- Adoption safety invariants:
  - computed-only and deprecated non-required inputs are excluded;
  - the top-level optional+computed provider identity `id` is excluded while
    nested `id` inputs remain;
  - single/max-one/list/set dynamic block semantics match Python;
  - output sensitivity, deprecated projection, and conditional `name_to_id`
    match Python;
  - provider source and exact schema-associated manifest pin are required;
  - ordinary writes and Terraform formatting retain Python trust assumptions.

## Tests Run

- Commands:
  - `npm run typecheck`
  - `npm run build:test`
  - focused Node generator tests for representative resources and failures
  - complete `module-generator.test.ts`
  - `metadata-loader.test.ts` and `metadata-cli.test.ts`
  - `npm run build:metadata-cli`
  - `python3 -m unittest -v tests.test_makefile_overlay`
  - independently generate the complete Python and Node trees and `diff -rq`
  - `PYTHON=/definitely/missing/python make check-modules`
  - Python-disabled `demo-modules` followed by Node tree validation
- Relevant output summary:
  - final combined generator/metadata-loader/CLI suite: 46 passed, 0 failed;
  - focused Python generator and Make integration: 50 passed, 0 failed;
  - full profile: 151 modules / 1,057 files;
  - physically reduced profiles: zscaler 151, ZIA 74, ZPA 54, ZCC 7, ZTC
    16, empty and each proof-of-concept provider 0;
  - all 68 existing committed HCL goldens matched after Terraform formatting;
  - independent complete Python and Node outputs: 151 modules byte-identical;
  - both Python-disabled module-generation paths passed;
  - final full Node suite: 872 passed, 1 platform-specific skip, 0 failed;
  - production bundles built, vendor-boundary violations were 0, and
    `git diff --check` passed.
- Tests not run and why:
  - GitHub Actions has not run yet because this is still the unpublished
    working tree; the comprehensive profile/platform matrix should run once on
    the stacked draft PR.
  - No credentials, network, provider import, Terraform root, plan, or Apply
    run: all are explicit scope exclusions.

## Review Remediation

- Finding: fractional numeric JSON sample overrides rendered in Python but
  failed in Node after the generic loader converted them to ordinary binary64
  numbers and the lossless artifact renderer rejected non-integral numbers.
  - Root cause: override JSON used the loader's default numeric mode, which
    preserves only out-of-safe-range integers; exact Python float spelling
    requires the original JSON numeric token.
  - Fix: override documents now preserve every numeric token as
    `LosslessNumber`; existing override validators already accept that numeric
    representation. The sample renderer canonicalizes those tokens through
    Python's finite-float spelling rules.
  - Regression test: a disposable override with `0.5`, `-0.0`, and `1e-6`
    must emit `0.5`, `-0.0`, and `1e-06` exactly.
  - Verification: focused numeric-loader, fractional-sample, existing sample
    override, and resource-owned override tests pass with typecheck.
- CI integration found during remediation: the main Node test job now installs
  Terraform before `npm run check`, because the new Node golden suite invokes
  the real formatter just as the Python golden authority does. This keeps the
  68 golden checks active rather than weakening them with an environment skip.

## Known Deferrals

- Deferred work:
  - replace temporary `engine.gen_module`/Python wording in generated headers
    and README only after Node fully replaces Python and fixture churn is
    separately approved;
  - generic transform and artifact rendering;
  - generic collector and product adapters;
  - generic provider Oracle/adopt;
  - generic environment/root generation;
  - generic import staging;
  - generic plan/assessment coordination;
  - Apply and final runtime cutover.
- Reason it is safe to defer: generated bytes and all later runtime paths stay
  unchanged; Python remains a test-only migration oracle for this subsystem.
- Follow-up owner or trigger: the fixed horizontal migration sequence after
  this draft is reviewed; this PR must stop before transform work.

## Review Focus

- Highest-risk files or paths:
  - `node-src/modules/generator.ts`
  - `node-src/metadata/loader.ts`
  - `node-src/metadata/resources.ts`
  - module CLI/Make integration
- Specific assumptions to attack:
  - required-before-optional ordering and Python string ordering;
  - top-level versus nested `id` handling;
  - recursive all-computed block omission;
  - single and `max_items=1` object shapes and iteration wrapping;
  - framework `nested_type` list/set/map/object recursion;
  - deprecated projection member ordering and recursive sensitivity;
  - `name_to_id` requiring a required `name` plus any `id`;
  - provider owner/source/pin joins for reduced roots;
  - shallow sample override and resource-owned HCL main precedence;
  - formatter injection/error behavior, seven-file completeness, and no Python
    invocation from switched Make/demo paths.
- Source evidence the reviewer should verify: `engine/gen_module.py`, the
  module-relevant portions of `engine/tfschema.py`, active pack metadata and
  schemas, and the existing Python tests.
- Generated artifacts the reviewer should compare: all 68 committed HCL
  goldens and an independently generated complete Python/Node corpus.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  optional+computed/deprecated/sensitive fields, nested IDs, computed-only
  blocks, required nested blocks, unsupported schema modes, duplicate or
  absent provider metadata, reduced pack roots, custom main/sample overrides,
  empty profiles, missing module paths, and Terraform formatting failures.
