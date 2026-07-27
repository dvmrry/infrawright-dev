# Generic CI Scope Review Handoff

## Intent

- Remove compiled-in cloud/profile and module-count inventories from generic CI.
- Derive committed profiles from `packs/*.packset.json` and derive generated
  module/root sets from the metadata selected by each profile.
- Allow a downstream distribution to remove unrelated provider packs and their
  profile documents without editing generic workflow or corpus inventories.
- Skip runtime suites for documentation/report-only diffs while preserving the
  two required status-context names.
- Preserve production behavior, pack contents, generated artifacts, and
  provider-specific behavioral contracts for packs that are installed.

## Base / Head

- Base: `d48ad6eed1577b516099eb47fa5cff397957cdd5`
- Head: `488ab450edf89b901a0c534fc7eb376a908238f6`
- Diff command: `git diff d48ad6eed1577b516099eb47fa5cff397957cdd5..488ab450edf89b901a0c534fc7eb376a908238f6`

## Files Changed

- Workflow: `.github/workflows/check.yml`.
- Distribution documentation: `docs/pack-distributions.md`.
- Test-only inventory/profile assertions in `go/cmd/iw`,
  `go/internal/assessment`, `go/internal/envgen`, `go/internal/metadata`,
  `go/internal/modulesgen`, and `go/internal/roots`.
- Files intentionally untouched: production Go code, pack contents, provider
  schemas, overrides, fixtures/snapshots, demos, Make targets, branch
  protection, and provider-refresh transition logic.

## Source Inputs Consulted

- Pack metadata: `packs/*.packset.json`, selected pack manifests, registry
  `generate` flags, and `metadata.LoadPackRoot` behavior.
- Process: `docs/adversarial-review.md` and its handoff/reviewer templates.
- CI configuration: required contexts are `full-go-check` and
  `Go v2 vertical-slice checkpoint`, with admin enforcement.
- No provider schemas, provider source, or OpenAPI contracts were used; their
  semantics do not change in this branch.

## Expected Delta

- CI discovers profile names from the checkout instead of enumerating AWS,
  Cloudflare, Google, NetBox, or Zscaler profiles.
- Every generic corpus assertion compares the actual generated filesystem or
  output labels with the exact resource set selected by metadata; fixed `151`
  and `1057` inventory assertions are removed.
- Provider-specific real-pack guidance cases remain acceptance contracts when
  their pack is installed and skip individually when it is absent.
- Documentation and reports (`docs/**`, `reports/**`, top-level Markdown, and
  `.github/*.md`) do not run runtime suites. Markdown fixtures below runtime
  source trees, such as `go/**/testdata/summary.md`, still do.
- A scope/discovery failure forces both required jobs to run and fail naturally
  instead of allowing required contexts to disappear behind a skipped need.
- No product output, schema, fixture, snapshot, demo, or readiness count changes.

## Invariants Claimed

- The installed pack/profile set, not an upstream cloud list, defines CI scope.
- A removed provider pack cannot remain required through a generic corpus test.
- Generated module validation is an exact filesystem set comparison: missing
  and extra module directories/files both fail.
- Arbitrary profile filenames are data, never interpolated into Go `-run`
  regular expressions.
- Changed-path and profile discovery fail closed.
- Production evidence, transform, adoption, and module-generation behavior are
  unchanged.

## Review Findings and Corrections

The first read-only review froze at `6002155260734e7c4f94876c19201495d767701c`
and requested changes. The correction commit is
`2a4d3e0d27d98dc59299eb4f45b3bcf0f6bac9ca`.

1. Scope failure could skip required jobs. Both required job conditions now use
   `always()` and run whenever the scope job did not succeed.
2. Changed-path discovery hid rename sources and could fail silently. It now
   captures `git diff --name-only --no-renames` and exits on discovery failure.
   Documentation/report-only skipping is deliberate product direction; runtime
   testdata Markdown is not classified as documentation.
3. Raw profile names were used as regex fragments. The duplicate matrix-level
   Go subtest invocation was removed; the full Go gate already discovers and
   exercises every committed profile without regex interpolation.
4. Module-tree equality did not inspect unexpected filesystem entries. A
   test-only inventory now compares exact directories/files, with mutations
   proving extra content is rejected.
5. A reduced-checkout diagnostic found one remaining catalog assumption in the
   assessment guidance test. It no longer manufactures metadata for absent
   AWS/Google/Cloudflare resources; those contracts run only when installed.

A second fresh review froze at
`2a4d3e0d27d98dc59299eb4f45b3bcf0f6bac9ca` and found two remaining blockers.
The final correction is `488ab450edf89b901a0c534fc7eb376a908238f6`.

6. Starting required jobs after a failed scope job did not force their
   conclusions to fail. Each required job now begins with a conditional
   failure step tied directly to `needs.scope.result`; subsequent expensive
   steps do not run after that failure.
7. Guidance-only provider packs do not have registry resources, so resource
   presence made all three upstream guidance cases skip. Installation now
   comes from the selected pack manifests; the test synthesizes only the
   resource descriptor the guidance API needs for an installed pack.

## Tests Run

- Focused Go tests for changed metadata, module generation, roots, environment
  generation, CLI corpus, and assessment guidance contracts: passed.
- Module-tree mutation test plus full/current-profile generation tests: passed
  in 1.188s.
- Workflow parsed with `yq`; inline classifier proofs covered docs/reports,
  runtime testdata Markdown, mixed code/docs, and top-level documentation.
- Workflow structure checks confirmed both required jobs contain the
  scope-result failure step and an explicit `exit 1`.
- Historical rename proof confirmed `--no-renames` exposes both old and new
  evidence paths.
- Exact v2 checkpoint at the first implementation commit: all metadata-derived
  modules and 20 demo roots passed in 402.48s.
- `make check` at the first implementation commit: passed; slowest Go package
  was `cmd/iw` at 26.446s.
- Downstream-shape proof after corrections: in a real temporary Git worktree,
  AWS, Cloudflare, Google, and NetBox pack directories/profile documents were
  physically absent and `full.packset.json` selected only the Zscaler packs.
  Complete `make check` passed; `cmd/iw` took 23.494s.
- The upstream real-pack guidance test executed and passed all Google, AWS, and
  Cloudflare cases without skips. The physically reduced Zscaler-only checkout
  skipped exactly those three absent-pack cases. Mutating the installed AWS
  rule ID made its focused case fail with the expected mismatch.
- The expensive v2 checkpoint was not repeated after test/workflow-only
  corrections; the affected generation assertions were exercised directly and
  the checkpoint remains the GitHub promotion gate.

## Explicit Tradeoff

- A manual edit solely under `docs/**` or `reports/**`, including generated CLI
  reference or provider-evidence documents, does not run Go/Terraform suites.
  Code changes that alter those generated documents still run the suites and
  their freshness tests. This boundary is intentional user direction, not an
  inference that all evidence is runtime-irrelevant.

## Known Deferrals

- No genericization of ZPA-specific refresh-transition dispositions.
- No Terraform test parallelization.
- No relocation of generated docs/evidence into runtime testdata.
- These are unrelated to removing cloud inventory coupling and would expand
  this correction into provider-refresh or repository-layout work.

## Review Focus

- Inspect the cumulative base-to-head diff and the correction range
  `2a4d3e0d27d98dc59299eb4f45b3bcf0f6bac9ca..488ab450edf89b901a0c534fc7eb376a908238f6`.
- Attack scope-job failure propagation, rename handling, zero-profile behavior,
  and documentation/runtime classification.
- Confirm no raw profile string reaches a Go regex.
- Confirm exact module-tree comparison rejects both extras and omissions rather
  than comparing an output with itself.
- Confirm an installed pack retains its real-pack guidance contract while an
  intentionally absent pack cannot fail generic CI.
- Confirm there is no production, pack, schema, fixture, snapshot, or generated
  output drift.

## Final Review Result

- Fresh read-only review of implementation head
  `488ab450edf89b901a0c534fc7eb376a908238f6`: **Approve**.
- Blocking findings: none.
- The reviewer independently confirmed required-job scope-failure propagation,
  docs/report classification, all three installed guidance contracts without
  skips, manifest-based absent-pack behavior, reduced-profile generation,
  exact module-tree checks, and no production/generated-artifact drift.
