# Full Go CI Check Review Handoff

## Intent

- Add a dedicated, non-matrix GitHub Actions job that runs the repository's
  complete `make check` gate.
- Put the repository-pinned Terraform `1.15.4` binary on `PATH` so the
  modulesgen and envgen full-corpus formatter differential tests execute
  instead of self-skipping.
- Keep the existing formatting/vet job and pack-profile matrix behavior
  unchanged; do not use Go's `-short` mode or split the Make target in CI.

## Base / Head

- Base: target PR base `feature/zia-provider-4.8.0` at
  `ce7cd7722db8c4795310fd4a56a9e6823c712a4d`.
- Head: implementation commit
  `1bd3f4ac79904bb22d38c0465ff407f0ffc1484c` on
  `feature/ci-full-go-check`.
- Diff command:
  `git diff ce7cd7722db8c4795310fd4a56a9e6823c712a4d...1bd3f4ac79904bb22d38c0465ff407f0ffc1484c`.
  This handoff is committed separately so it can name the immutable
  implementation head exactly.

## Files Changed

- Files: `.github/workflows/check.yml`; this companion handoff is orientation
  material outside the implementation diff named above.
- Files intentionally left untouched: `Makefile`, all Go implementation and
  tests, the existing `check` job, and every line of the existing
  `pack-profiles` job and its matrix.

## Source Inputs Consulted

- Provider schemas: none; no provider behavior or schema changed.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: `packs/full.packset.json` only indirectly through the existing
  `make check` behavior; no pack metadata changed.
- Existing docs or design records: `AGENTS.md`,
  `docs/adversarial-review.md`, `docs/review-handoff-template.md`,
  `docs/adversarial-review-run-prompt.md`, and
  `docs/adversarial-review-template.md`.
- Other source evidence: `.github/workflows/check.yml` for the established
  action SHAs, Go `1.26.5`, Terraform `1.15.4`, and wrapper setting;
  `Makefile` for the exact `check`, `check-distribution`, and `test-go` graph;
  `go/internal/modulesgen/generator_test.go` and
  `go/internal/envgen/environment_generator_test.go` for the full-corpus
  Terraform lookup and `testing.Short()` skip conditions.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none retained; `make check` verified the committed demo
  without producing tracked drift.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: pull requests and pushes covered by `check.yml`
  gain one independent Ubuntu job that installs Go and Terraform, then runs
  exact `make check` from the repository root.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: production behavior, Go tests, Make targets, pack
  selection, and all 11 existing pack-profile matrix entries remain unchanged.

## Invariants Claimed

- Evidence must not be silently dropped: no evidence code or artifact changes;
  the full unshortened test suite is added to CI as a detection gate.
- Generic matcher evidence must not outrank source-backed evidence: no matcher
  or evidence-ranking code changes.
- Source precedence/provenance must remain explicit: no source or provenance
  changes.
- Ambiguity must stay classified instead of being coerced to success: no
  classification changes.
- Provider-readiness counts must stay explainable: no readiness or count
  changes.
- Adoption safety invariants: no adoption code or fixtures change; the new job
  invokes the existing complete repository gate without weaker flags.

## Tests Run

- Commands:
  - `terraform version`
  - `make check`
  - `cd go && go test -count=1 -v ./internal/modulesgen -run '^TestHCLFormatterMatchesTerraformAcrossFullGeneratedCorpus$'`
  - `cd go && go test -count=1 -v ./internal/envgen -run '^TestFullProfileTreeGeneratesAllRoots$'`
  - `test -z "$(gofmt -l go/cmd go/internal)"`
  - `cd go && go vet ./...`
  - structural `yq` assertions over the new job and existing matrix
  - `git diff --check`
- Relevant output summary: local Terraform was `v1.15.4` on `darwin_arm64`;
  `make check` passed with the full `go test -count=1 ./...` suite. Both focused
  verbose tests emitted `=== RUN` followed by `--- PASS`, proving they ran and
  did not skip. YAML parsing showed no strategy on `full-go-check`, exact
  `make check`, Terraform `1.15.4` with `terraform_wrapper: false`, and the
  unchanged matrix
  `[empty, aws, cloudflare, google, netbox, zcc, zia, zpa, ztc, zscaler, full]`.
  Formatting, vet, and whitespace checks passed.
- Tests not run and why: no GitHub-hosted Ubuntu Actions execution was possible
  without pushing or opening a PR, which this builder is explicitly forbidden
  to do. `actionlint` was not installed locally; `yq` successfully parsed and
  structurally asserted the changed workflow instead.

## Known Deferrals

- Deferred work: observe the new job on the target PR's GitHub-hosted runner.
- Reason it is safe to defer: the same exact Make target passed locally with
  the pinned Terraform version present, and the workflow uses already
  established action pins and settings.
- Follow-up owner or trigger: PR author/reviewer when this local commit series
  is pushed through the authorized publication workflow.

## Review Focus

- Highest-risk files or paths: `.github/workflows/check.yml`, specifically the
  new `full-go-check` job.
- Specific assumptions to attack: GitHub recognizes it as an independent
  non-matrix job; Terraform is on `PATH` with the wrapper disabled before
  `make check`; the step invokes exact `make check` with no `-short`; the
  existing pack-profile strategy and materialization steps are byte-unchanged.
- Source evidence the reviewer should verify: compare action pins and tool
  versions with the adjacent existing jobs; trace `make check` through
  `test-go`; inspect both formatter differential skip conditions and executable
  lookup helpers.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  an unintended matrix attachment, a missing Terraform executable, the setup
  action's wrapper altering subprocess behavior, or a command other than exact
  unshortened `make check` would weaken the intended gate.
