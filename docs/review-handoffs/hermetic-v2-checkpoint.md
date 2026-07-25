# Hermetic V2 Checkpoint Builder Handoff

## Intent

- Make `TestV2VerticalSliceCheckpoint` independent of the developer or CI
  runner's existing Go module cache. The checkpoint previously selected a fresh
  `GOMODCACHE` and immediately attempted a `GOPROXY=off` candidate build, which
  failed before the black-box runtime assertions could run.
- Provision that exact test-owned cache first with `go mod download` from the
  repository's `go/` module root. Provisioning uses the explicit public-only
  `https://proxy.golang.org` proxy (without a direct fallback) and
  `sum.golang.org`; the subsequent candidate build uses the same cache with
  `GOPROXY=off` and `GOSUMDB=off`.
- Add an independent GitHub Actions job that runs the opt-in Go v2 checkpoint
  and its focused hermetic-build contract tests with the repository's existing
  pinned Go and Terraform versions.
- Keep the Node-free runtime `PATH` construction and assertion in
  `v2IsolatedPath` unchanged. Keep Terraform provider-plugin caching and the
  wider Slice 1 and Slice 3 work out of this checkpoint.

## Base / Head

- Base: `ce7cd7722db8c4795310fd4a56a9e6823c712a4d`, verified as both the starting
  `HEAD` and `feature/zia-provider-4.8.0` before editing.
- Head: implementation commit
  `cde4b9796aeb54e9e23c8e782fad510589d8d9d5` on
  `feature/hermetic-v2-checkpoint`.
- Handoff carrier: the docs-only successor commit containing this file. It is
  intentionally outside the implementation head so this handoff can name the
  exact immutable code commit under review.
- Diff command:
  `git diff ce7cd7722db8c4795310fd4a56a9e6823c712a4d...cde4b9796aeb54e9e23c8e782fad510589d8d9d5`.

## Adversarial Review Finding Resolution

- Accepted blocking finding: the initial CI command selected only
  `TestV2VerticalSliceCheckpoint`, excluding both
  `TestV2BuildGoBinary...` hermetic-contract regressions. No other workflow ran
  Go tests, so a warm `setup-go` cache could let the happy path pass after an
  ambient cache/proxy regression.
- Root cause: CI selection was scoped to the opt-in end-to-end test rather than
  the complete, bounded v2 hermetic checkpoint cohort.
- Fix: implementation-fix commit
  `cde4b9796aeb54e9e23c8e782fad510589d8d9d5` changes the workflow regex to
  `^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$`. This selects the two
  current build-contract tests plus the real checkpoint without broadening to
  all `cmd/iw` tests or Slice 1.
- Regression verification: the exact workflow command,
  `INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`,
  emitted `=== RUN` and `--- PASS` for exactly these three top-level tests:
  `TestV2BuildGoBinaryDownloadsModulesBeforeOfflineBuild`,
  `TestV2BuildGoBinaryDistinguishesProvisioningAndOfflineBuildFailures`, and
  `TestV2VerticalSliceCheckpoint`. It emitted no skip and the package passed.
- Accepted nit: the initial 20-minute Actions job retained Go's default
  10-minute test timeout, so Go could preempt the intended outer job bound.
- Root cause: the job timeout was explicit but the inner `go test` timeout was
  not.
- Fix: the same implementation-fix commit adds `-timeout=18m`, preserving the
  20-minute Actions timeout as the outer bound.
- Regression verification: the exact command above includes `-timeout=18m`
  and completed all three tests successfully in approximately 13.5 seconds.
- Focused reviewer recheck diff:
  `git diff 46148260f40b26970ed0cddb84a3981cdf2d2228...cde4b9796aeb54e9e23c8e782fad510589d8d9d5`.

## Files Changed

- Files:
  - `go/cmd/iw/v2_vertical_slice_test.go`: explicit module-cache provisioning,
    offline candidate build, phase-specific errors, and focused regressions.
  - `.github/workflows/check.yml`: independent `v2-checkpoint` integration-test
    job with Go 1.26.5, Terraform 1.15.4, bounded hermetic-contract selection,
    and an 18-minute inner test timeout below the 20-minute job timeout.
  - `docs/review-handoffs/hermetic-v2-checkpoint.md`: this builder handoff,
    carried in the following docs-only commit.
- Files intentionally left untouched: provider/OpenAPI evidence, packs,
  generated fixtures and snapshots, production Go code, the implementation of
  `v2IsolatedPath`, and Terraform plugin-cache setup in `v2Environment`.

## Source Inputs Consulted

- Provider schemas: none; the checkpoint consumes the already committed ZIA
  4.8.0 schema and makes no schema claim or change.
- OpenAPI/API contracts: none.
- Provider source files: none.
- Pack metadata: existing `packs/zia/pack.json` and the ZIA pack/profile are
  exercised by the unchanged checkpoint flow but were not edited.
- Existing docs or design records: `AGENTS.md`,
  `docs/adversarial-review.md`, `docs/review-handoff-template.md`,
  `docs/adversarial-review-run-prompt.md`, and
  `docs/adversarial-review-template.md`.
- Other source evidence:
  - `go/go.mod` contains a public module graph and pins Go 1.26.5.
  - `go/go.sum` provides committed checksums for that graph.
  - `.github/workflows/check.yml` already pins Go 1.26.5 and Terraform 1.15.4
    without private Go-module credentials.
  - The unchanged bounded-command runner supplies a complete child environment
    rather than merging the host environment.
  - Baseline command
    `INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -run '^TestV2VerticalSliceCheckpoint$' ./cmd/iw`
    failed at the offline candidate `go build` with the fresh empty cache.

The strict public proxy is therefore an explicit policy for the repository's
current public, checksum-pinned dependency graph, rather than an inherited
developer setting. `GOENV=off`, `GOWORK=off`, and `GOTOOLCHAIN=local` remain
explicit. Provisioning alone uses `GOFLAGS=-modcacherw`, which preserves Go's
verified module contents while leaving the test-owned cache removable by
`testing.TempDir` on macOS; the offline build resets `GOFLAGS` to empty.

## Generated Artifacts

- Reports: none.
- Schemas: none.
- Fixtures: none.
- Snapshots: none.
- Demo or lab outputs: none retained. The candidate binary, module cache,
  Terraform provider install, and provider lock are temporary test artifacts.
- Artifact drift intentionally expected: none.

## Expected Delta

- Expected behavior change: the checkpoint downloads the committed Go module
  graph into a fresh cache and then successfully compiles the candidate with
  network module resolution disabled. Provisioning and offline-build failures
  are distinguishable and retain their underlying error with `errors.Is` /
  `errors.As` support.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: black-box fetch/transform/module/gen-env behavior,
  Terraform init/validate/test assertions, Node absence, provider selection,
  source evidence, adoption behavior, and provider-readiness accounting.

## Invariants Claimed

- Evidence must not be silently dropped: N/A; no evidence extraction,
  transformation, or reporting code changed.
- Generic matcher evidence must not outrank source-backed evidence: N/A; no
  matcher or precedence logic changed.
- Source precedence/provenance must remain explicit: N/A; no source evidence
  changed. This handoff records the repository inputs used to choose the Go
  provisioning policy.
- Ambiguity must stay classified instead of being coerced to success: N/A; no
  classification logic changed.
- Provider-readiness counts must stay explainable: no readiness inputs or
  counts changed.
- Adoption safety invariants: the production pack, transforms, generated
  configuration assertions, provider lock check, and Terraform plan-evidence
  checks are unchanged and still pass end to end.
- Hermetic build invariants:
  - `go mod download` runs from `<repository>/go` with a fresh explicit
    `GOMODCACHE`, `GOPROXY=https://proxy.golang.org`, and
    `GOSUMDB=sum.golang.org`.
  - `go build` runs only after download, from `<repository>/go/cmd/iw`, against
    that same cache with `GOPROXY=off`, `GOSUMDB=off`, and empty `GOFLAGS`.
  - Host `GOMODCACHE`, `GOPROXY`, and `GOSUMDB` values are not inherited or
    inspected. No ambient-cache diagnostic is included.
  - `v2IsolatedPath` still supplies Terraform and asserts that Node is absent;
    the built candidate still runs only in that environment.
  - CI selects the bounded v2 hermetic checkpoint cohort: the two current
    `TestV2BuildGoBinary...` contract tests and `TestV2VerticalSliceCheckpoint`.
    Go's 18-minute timeout remains inside the 20-minute Actions job timeout.

## Tests Run

- Commands:
  - `INFRAWRIGHT_V2_CHECKPOINT=1 go test -count=1 -timeout=18m -v -run '^TestV2(BuildGoBinary.*|VerticalSliceCheckpoint)$' ./cmd/iw`
  - `go test -count=1 ./cmd/iw`
  - `gofmt -d .`
  - `go vet ./...`
  - `git diff --check`
- Relevant output summary:
  - Focused command-orchestration and phase-error regressions pass. They prove
    download-before-build ordering, module-root provisioning, an identical
    fresh cache across phases, explicit public provisioning settings, offline
    build settings, cleanable cache permissions, and phase-specific wrapped
    failures.
  - The exact CI command runs and passes all three intended top-level tests and
    their two phase-error subtests. No test is skipped.
  - The real checkpoint passes on local `darwin/arm64` with candidate SHA-256
    `f9baf80dc01248d7c6c5933c6fd9917c4f1db0d23e9e91e52f2718a576eba685`.
    Terraform 1.15.4 initializes ZIA 4.8.0, validates the configuration, and
    reports two passing test runs with no failures, errors, or skips. The
    generated local provider lock SHA-256 is
    `412febe7aee5c511311c4f091c1befa6375886c838cade7d7da95caad9e20748`.
  - The ordinary `cmd/iw` suite passes. Formatting, vet, and diff checks produce
    no output.
  - The supplemental Go error checker reports one pre-existing
    string-inspection pattern in `TestV2TerraformTestEvidenceRejectsMisScopedPlans`;
    the implementation introduces no such finding and does not broaden into
    that existing test.
- Tests not run and why: the GitHub-hosted job itself was not run because this
  branch was not pushed by authorization. `actionlint` was not available
  locally; the workflow change uses the same pinned setup actions and inputs as
  the existing jobs.

## Known Deferrals

- Deferred work: Slice 1 runtime/PATH changes.
- Reason it is safe to defer: `v2IsolatedPath` is unchanged and its Node-absence
  assertion passes in the completed checkpoint.
- Follow-up owner or trigger: the separately scoped Slice 1 change, if still
  required.
- Deferred work: Slice 3 Terraform provider-plugin cache provisioning.
- Reason it is safe to defer: this checkpoint changes only Go module-cache
  provisioning; existing Terraform init behavior succeeds in the focused run.
- Follow-up owner or trigger: the separately scoped Slice 3/plugin-cache change.
- Deferred work: private Go module support.
- Reason it is safe to defer: the current committed module graph is public and
  checksum-pinned. A private dependency would require an explicit proxy and
  credential policy rather than silent host inheritance.
- Follow-up owner or trigger: any future change that adds a private module to
  `go/go.mod`.

## Review Focus

- Highest-risk files or paths: `v2BuildGoBinaryWithRunner` and its environment
  construction in `go/cmd/iw/v2_vertical_slice_test.go`; the new
  `v2-checkpoint` job in `.github/workflows/check.yml`.
- Specific assumptions to attack:
  - `go mod download` receives the exact cache later used by `go build`.
  - No direct or ambient proxy fallback can enter the provisioning phase.
  - The candidate build cannot resolve missing modules from the network.
  - `-modcacherw` affects provisioning cleanup only and is cleared for build.
  - Distinct failure phases cannot collapse back into the generic bounded
    command diagnostic.
  - The CI job actually runs the opt-in checkpoint with an unwrapped Terraform
    binary, both focused build-contract tests, and the intended inner/outer
    timeout ordering.
- Source evidence the reviewer should verify: `go/go.mod`, `go/go.sum`, the
  complete-environment contract in `go/internal/terraformcmd`, and existing
  pinned action inputs in `.github/workflows/check.yml`.
- Generated artifacts the reviewer should compare: none.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  none in provider evidence. For this test-only change, attack an unavailable
  proxy, an incomplete module cache, a failed offline build, future private
  dependencies, and accidental reintroduction of host Go environment state.
