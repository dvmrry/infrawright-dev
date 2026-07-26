# Terraform Semantics Gate Builder Handoff

## Intent

- Extend the existing opt-in Go v2 checkpoint instead of adding a shell
  utility or a second test harness.
- Make the required `Go v2 vertical-slice checkpoint` job prove provider
  semantics across every generated module (151 resources), every resource in
  the committed demo-config corpus (20 roots generated from the shared raw
  fixtures), and one native-HCL tfvars transform-to-plan path.
- Reject quiet coverage loss: Terraform JSON must report the exact expected
  run names, every run must pass, and the summary must contain no failed,
  errored, or skipped tests.
- Reuse a CI-persisted Terraform provider-plugin cache while retaining the
  existing per-test temporary fallback, isolated HOME/TMPDIR, Node-free PATH,
  mock providers, and credential filtering.
- Correct three ZTC module samples that the real v0.2.0 provider validators
  reject, and stop emitting an impossible `empty_plan` for JSON environment
  roots whose expression overlay directly indexes configured item keys.
- Keep adoption, apply, import, generated production module structure,
  provider/OpenAPI evidence, and the eleven pack-profile matrix jobs
  unchanged.

## Base / Head

- Base: `aa062eb584a40c8ba93ab3f0dca17e9881d20f4d` (`origin/main` when the
  branch was created).
- Head: implementation commit
  `27e29cabfafd02f3c8e91cdfdfb2ed2dbd107c58` on
  `feature/terraform-semantics-gate`.
- Handoff carrier: the docs-only successor commit containing this file. It is
  intentionally outside the implementation head so the handoff can name the
  exact immutable code commit under review.
- Diff command:
  `git diff aa062eb584a40c8ba93ab3f0dca17e9881d20f4d...27e29cabfafd02f3c8e91cdfdfb2ed2dbd107c58`.

## Files Changed

- `.github/workflows/check.yml`
  - gives only the existing non-matrix v2 checkpoint a persisted
    `TF_PLUGIN_CACHE_DIR`;
  - pins `actions/cache` v6.1.0 by full commit SHA;
  - updates every existing checkout/setup-terraform use to the official
    Node-24 releases, as previously requested for the next workflow edit.
- `go/cmd/iw/v2_vertical_slice_test.go`
  - honors an explicit plugin cache with a temporary fallback;
  - parses Terraform test JSON once and requires exact completed runs and
    zero skips;
  - generates and runs all 151 module mock-provider suites;
  - transforms the shared demo fixture corpus into a temporary overlay,
    compares its resource set to the committed demo config corpus, generates
    all 20 roots, and runs every smoke suite;
  - runs a separate native-HCL transform and passes the generated `.auto.tfvars`
    through a verbose mock-provider plan, asserting the exact rule-label
    resource address and values.
- `go/internal/envgen/environment_generator.go`
  - omits `empty_plan` only when expression bindings make empty items invalid
    and a JSON `config_plan` exists to replace it.
- `go/internal/envgen/environment_generator_test.go`
  - pins the bound-JSON and HCL-fallback smoke-test behavior.
- `packs/ztc/overrides/ztc_activation_status.json`
- `packs/ztc/overrides/ztc_forwarding_gateway.json`
- `packs/ztc/overrides/ztc_traffic_forwarding_rule.json`
  - supply provider-valid sample enum values.
- `go/internal/metadata/loader_test.go`
  - updates the exact full-profile override count from 74 to 76 for the two
    newly materialized override documents.
- Files intentionally left untouched: production adoption/transform/apply
  code, provider schemas, registries, OpenAPI/source-operation evidence,
  generated compatibility fixtures and snapshots, demo committed outputs,
  Makefiles, and pack-profile matrix composition.

## Source Inputs Consulted

- Provider schemas: all committed provider schemas behind
  `packs/full.packset.json`, exercised by generated module planning.
- OpenAPI/API contracts: none; no API mapping or collection behavior changed.
- Provider source files: official `zscaler/terraform-provider-ztc` v0.2.0 at
  commit `6516b4a032ef4a5ece183a0f42a5026b11ac94ca`:
  - `ztc/resource_ztc_activation_status.go` accepts `ADM_LOGGED_IN` and rejects
    the prior sample `ACTIVE`;
  - `ztc/resource_ztc_forwarding_gateway.go` requires `primary_type` and
    `secondary_type` from a closed enum containing `AUTO`;
  - `ztc/resource_ztc_traffic_forwarding_rule.go` requires `forward_method`
    from a closed enum containing `DIRECT`.
- Provider examples/docs: the same v0.2.0 source uses `AUTO` for both gateway
  types and `DIRECT` for the basic direct forwarding-rule example.
- Pack metadata: `packs/full.packset.json`, `packs/zscaler.packset.json`, all
  active pack manifests and generated resources, and the committed demo config
  and shared Zscaler raw fixture directories.
- Existing docs/design records: `AGENTS.md`, the adversarial-review workflow
  and templates, and
  `docs/review-handoffs/hermetic-v2-checkpoint.md`.
- GitHub Actions sources, checked through the official repositories/releases:
  - `actions/checkout` v7.0.1 at
    `3d3c42e5aac5ba805825da76410c181273ba90b1` (`node24`);
  - `hashicorp/setup-terraform` v4.0.1 at
    `dfe3c3f87815947d99a8997f908cb6525fc44e9e` (`node24`);
  - `actions/cache` v6.1.0 at
    `55cc8345863c7cc4c66a329aec7e433d2d1c52a9` (`node24`).

## Generated Artifacts

- Reports: none retained.
- Schemas: none changed.
- Fixtures: no committed fixture bytes changed. The full compatibility tests
  pass unchanged.
- Snapshots: none changed.
- Demo/lab outputs: all module trees, transformed config, environment roots,
  HCL tfvars, lock files, and `.terraform` directories are test-owned
  temporary artifacts and are removed after the run.
- Artifact drift intentionally expected:
  - generated `tests/sample.auto.tfvars.json` values change for the three ZTC
    resources named above;
  - a JSON environment root with expression bindings and materialized config
    now contains only `config_plan`, not the invalid `empty_plan` plus
    `config_plan` pair;
  - ordinary unbound JSON roots still contain both runs;
  - HCL roots retain their existing `empty_plan` fallback because the
    production generator still cannot embed native HCL tfvars in a generated
    variables block. The checkpoint supplies the generated HCL artifact via
    Terraform's real `-var-file` interface instead.

## Expected Delta

- Expected behavior change: CI's already-required v2 checkpoint expands from
  one ZIA rule-label root to the full generated Terraform semantic surface.
- Expected report/count/coverage changes: 151 module suites, 20 demo-root
  suites, and one HCL config plan are now mandatory. Full-profile override
  entries increase 74 -> 76.
- Expected generated-output changes: only the three ZTC sample inputs and the
  bound-JSON smoke-test run set described above.
- Expected no-op areas: provider resource inventory (151), provider versions,
  registry enablement, production module main/variables/outputs/versions HCL,
  transform payloads, adoption, imports/moves, plan classification, apply
  guardrails, evidence/provenance, and all eleven profile jobs.

## Invariants Claimed

- Evidence must not be silently dropped: exact completed Terraform run names
  are checked against an independently derived expected set; missing, extra,
  failed, errored, or skipped runs fail the checkpoint.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or source-evidence logic changed.
- Source precedence/provenance must remain explicit: ZTC sample corrections
  are derived from the pinned provider's validators and examples, not inferred
  from a green test alone.
- Ambiguity must stay classified instead of being coerced to success: N/A; no
  classification logic changed. Terraform process errors and malformed or
  incomplete JSON evidence fail closed.
- Provider-readiness counts must stay explainable: inventory remains 151; only
  the explicit override-document count changes by two.
- Adoption safety invariants: adoption/import/apply code is untouched. All
  Terraform plans use mock providers and temporary state; the checkpoint never
  applies and receives no real provider credentials.
- Generic environment-generator invariant: the expression-binding rule is
  provider-neutral. It depends only on whether a generated root has bindings,
  materialized JSON config, and therefore a runnable replacement config plan.
- Hermeticity invariant: the candidate Go build remains independently
  provisioned then offline, PATH still contains Terraform but no Node, HOME and
  TMPDIR remain test-owned, provider credentials are filtered, and an ambient
  plugin cache is used only when explicitly supplied through
  `TF_PLUGIN_CACHE_DIR`.

## Tests Run

- Commands:
  - `TF_PLUGIN_CACHE_DIR=/tmp/.../plugin-cache INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `make check`
  - `go vet ./...`
  - focused envgen/parser/plugin-cache regressions in `go test`
  - `git diff --check`
  - local YAML parse of `.github/workflows/check.yml` (Ruby/Psych; `actionlint`
    was not installed).
- Relevant output summary:
  - the exact implementation-head checkpoint passed in 389.37 seconds with
    candidate binary SHA-256
    `3a0f37b3929284705ebb5a43ab6eb85af7ef531cb0eb0ea494f4e5e843b3e3a8`;
  - it passed 151/151 generated module suites and 20/20 demo environment
    suites with no failed, errored, or skipped run;
  - HCL test passed one exact `config_plan`, creating
    `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]`
    with the expected `name` and `description`;
  - `make check`, vet, formatting, focused tests, YAML parse, and diff hygiene
    pass.
- Tests not run and why:
  - the changed GitHub-hosted workflow has not run because the branch is not
    pushed before adversarial review;
  - no live tenant/provider create, read, or apply is run because these suites
    intentionally use Terraform mock providers and test only schema/planning
    semantics;
  - a cold GitHub cache timing has not been measured locally. The complete
    local run is below the existing 18-minute Go and 20-minute job bounds.

## Known Deferrals

- Generated native-HCL smoke tests still cannot ingest config without an
  explicit `terraform test -var-file`; the checkpoint covers that interface
  directly rather than changing all emitted HCL test files.
- Provider acceptance tests and live tenant no-op plans remain separate from
  credential-free generated Terraform semantics.
- No provider inventories, new resources, or additional demo fixtures are
  added in this slice.

## Review Focus

- Verify the 151-module discovery/count cannot quietly accept a reduced or
  extra generated corpus, and that every module test must report exactly
  `defaults_plan=pass` with zero skips.
- Verify the demo resource set is derived independently from committed demo
  config, compared to candidate transform output and generated root
  directories, and then actually passed to Terraform.
- Attack the bound-root rule: `empty_plan` should be omitted only when direct
  expression-key indexing makes it invalid and a JSON `config_plan` replaces
  it. HCL/unconfigured roots must not become zero-run suites.
- Verify the HCL case plans the generated `.auto.tfvars` via Terraform's real
  `-var-file`, excludes the ordinary generated smoke directory, and asserts the
  exact resource/value evidence rather than only an exit code.
- Verify the explicit plugin cache does not reintroduce host HOME, PATH,
  credentials, Go module cache, Terraform state, or generated-output
  dependence.
- Verify the cache key covers all four active provider pins and is confined to
  the non-matrix checkpoint job.
- Verify the three ZTC samples against pinned v0.2.0 source rather than accepting
  the chosen enums merely because mock plans pass.
- Evaluate whether the requested Node-24 action upgrades introduce any
  incompatible input/checkout behavior across the four existing jobs.
