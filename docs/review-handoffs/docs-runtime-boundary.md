# Documentation Runtime-Boundary Review Handoff

## Intent

- Make `docs/` human-facing documentation rather than a Go-test or runtime
  authority.
- Keep ZPA provider evidence with the optional ZPA pack so a downstream
  checkout may omit that pack without retaining or satisfying ZPA-only tests.
- Keep the generated CLI golden beside the test that owns it.
- Preserve direct deployment validation, generated CLI contents, ZPA evidence
  contents, and behavior for installations that include the ZPA pack.

## Base / Head

- Base: `acc8c46749cd25da3dd55a2475d46e830a2ffbe6`
- Head: `9da67453eb0ac33934dc3e32cf2fec38295c1e8f`
- Diff command:
  `git diff acc8c46749cd25da3dd55a2475d46e830a2ffbe6..9da67453eb0ac33934dc3e32cf2fec38295c1e8f`

## Files Changed

- Moved `docs/evidence/zpa-provider-v4.4.9.json` to
  `packs/zpa/evidence/zpa-provider-v4.4.9.json` and updated its test and prose
  references.
- Moved `docs/cli-reference.md` to
  `go/cmd/iw/testdata/cli-reference.md` and updated its freshness test and
  prose link.
- Made `go/internal/authoring/zpacorpus` skip pack-bound tests only when
  `packs/zpa` is absent; a present pack with missing evidence still fails.
- Removed two deployment tests that parsed or grepped prose. The existing
  table-driven test remains the direct authority for rejection of `strategy`,
  `groups`, and `bind_references`.
- Added a test-only repository-boundary check for repository-relative `docs/`
  path literals in Go tests.
- Updated repository-layout documentation.
- Files intentionally left untouched: production Go code, provider schemas,
  overrides, pack manifests, fixture bodies, generated CLI content, provider
  evidence content, workflows, and Terraform/module behavior.

## Source Inputs Consulted

- Provider schemas: none; no schema semantics change.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: the installed-pack boundary at `packs/zpa` and the existing
  ZPA corpus matrix consumer.
- Existing docs or design records: `docs/repo-surface.md`,
  `docs/pack-authoring.md`, `docs/operational-runtime.md`, and the repository
  adversarial-review contract.
- Other source evidence: direct deployment validation tests and the CLI golden
  freshness test.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: the ZPA matrix was relocated without content changes. Its old and
  new SHA-256 are both
  `fe60ad3e18ac1687d9dfe8aca4e7bff4e6bb5a05941690588c667e444c1dbc3f`.
- Snapshots: the CLI golden was relocated without content changes. Its old and
  new SHA-256 are both
  `bdca7d49275da495aa68443d91959c4642a865951439f303e6f53aa0c5e3086f`.
- Demo or lab outputs: none.
- Artifact drift intentionally expected: path-only 100% Git renames; no byte
  drift.

## Expected Delta

- Expected behavior change: ZPA corpus tests skip when the ZPA pack directory
  is intentionally absent. A present ZPA pack still requires its evidence.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none beyond the CLI golden's path.
- Expected no-op areas: production behavior, installed-ZPA checks, deployment
  field validation, CLI rendering, all other cloud packs, transforms,
  adoption, and module generation.

## Invariants Claimed

- Evidence must not be silently dropped: installed ZPA still fails if its
  evidence file is absent or invalid; only absence of the whole pack skips.
- Generic matcher evidence must not outrank source-backed evidence: unchanged.
- Source precedence/provenance must remain explicit: the ZPA matrix bytes and
  digest are unchanged and now live under the pack they describe.
- Ambiguity must stay classified instead of being coerced to success:
  unchanged.
- Provider-readiness counts must stay explainable: unchanged.
- Adoption safety invariants: unchanged.
- Go tests must not make prose under `docs/` an executable filesystem
  dependency.

## Tests Run

- Commands:
  - `go test -count=1 -v ./cmd/iw -run '^TestGoTestsDoNotReadDocumentationTree$'`
  - `go test -count=1 ./cmd/iw`
  - `go test -count=1 ./cmd/iw ./internal/deployment ./internal/authoring/zpacorpus`
  - `go test -count=1 -v ./internal/authoring/zpacorpus` in a registered
    temporary worktree with `packs/zpa` physically absent
  - `make check`
  - `git diff --cached --check` before the implementation commit
- Relevant output summary: all final-tip focused, affected-package, and full
  promotion checks passed. In the no-ZPA checkout, pack-bound tests skipped
  with `ZPA pack is not installed`; generic transition/source tests continued
  to execute and pass.
- Focused regression and pre-fix/unsafe-mutation proof: adding a temporary Go
  test that called `os.ReadFile(filepath.Join("..", "..", "..", "docs",
  "state-topology.md"))` made the boundary test fail and identify
  `internal/metadata/docs_read_regression_test.go:10:53`; removing the mutation
  restored the pass. The physical no-ZPA worktree proves the pack-scope skip
  rather than mocking it.
- Promotion efficiency: first-edit wall-clock was not captured. Two full
  `make check` sweeps passed: the first preceded a material simplification of
  the new boundary test, and the second promoted the frozen final
  implementation. No full sweep was used to diagnose a failure.
- Tests not run and why: the Terraform vertical-slice checkpoint was not run;
  no production, generator, Terraform, module-schema, fixture-content, or
  module-output semantics changed.

## Known Deferrals

- Deferred work: no attempt to make the boundary check a string/data-flow
  analyzer for dynamically assembled paths.
- Reason it is safe to defer: it is a cheap convention guard for ordinary
  repository-relative test paths, while the actual known prose dependencies
  were removed and the moved authorities have direct freshness/integrity
  tests.
- Follow-up owner or trigger: extend only if a real bypass recurs; do not grow
  a policy engine speculatively.

## Review Focus

- Highest-risk files or paths: `go/internal/authoring/zpacorpus/gate_test.go`,
  `go/cmd/iw/repository_boundary_test.go`, the two 100% renames, and deleted
  deployment prose tests.
- Specific assumptions to attack:
  - Removing `packs/zpa` skips only ZPA pack-bound tests and does not couple CI
    to another cloud/provider inventory.
  - Keeping `packs/zpa` while removing or corrupting its matrix fails closed.
  - Direct behavior tests still reject all three retired deployment fields.
  - The CLI freshness test still compares the rendered command tree with the
    relocated golden.
  - The repository-boundary guard is small, filterable, not docs-prose
    authority, and demonstrably fails against the unsafe mutation.
- Source evidence the reviewer should verify: old/new blobs and SHA-256 values,
  all path references, pack-present/pack-absent branches, and retained direct
  deployment tests.
- Generated artifacts the reviewer should compare: byte identity of the ZPA
  matrix and CLI golden.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  treating a missing matrix as an absent pack; skipping generic helper tests;
  stale old paths; or replacing direct behavior coverage with prose checks.

## Final Review Result

- Fresh read-only review of implementation head
  `9da67453eb0ac33934dc3e32cf2fec38295c1e8f`: **Approve**.
- Blocking findings: none.
- Non-blocking risks: none.
- The reviewer independently confirmed both path-only artifact renames, no
  stale old-path references, installed-pack fail-closed evidence handling,
  absent-pack scoping, retained direct deployment behavior coverage, the
  boundary guard's mutation logic, and no production, count, schema, or
  other-cloud inventory drift.
