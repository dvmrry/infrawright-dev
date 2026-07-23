# Go Runtime v2 Vertical-Slice Checkpoint Review Handoff

## Intent

- Turn §5 of `docs/go-runtime-v2.md` into a bounded, executable checkpoint for
  exactly one resource: `zia_rule_labels`.
- Prove the built Go CLI can run `fetch -> transform -> module/env generation`
  with Node absent from `PATH`, preserve the committed pull/config/import bytes,
  and compose successfully through Terraform's mock-provider plan.
- Keep all production behavior unchanged. This change is test/process
  scaffolding only and does not authorize blocks C/D, live mutation, Adopt,
  plan lifecycle, or Apply work.

## Base / Head

- Base: `863f405922e8d30ae277507a40580ead8c5a28ba`.
- Head: uncommitted working tree on `feature/go-canonjson-foundation`, based on
  `863f405`; nothing is staged, committed, or pushed.
- Diff command:
  `git diff 863f405 -- docs/go-runtime-plan.md docs/go-runtime-v2.md go/cmd/iw/fetch_differential_test.go`
  plus direct inspection of the untracked
  `go/cmd/iw/v2_vertical_slice_test.go` and this handoff.

## Files Changed

- Files:
  - `docs/go-runtime-plan.md`
  - `docs/go-runtime-v2.md`
  - `docs/review-handoffs/go-v2-vertical-slice-checkpoint.md`
  - `go/cmd/iw/fetch_differential_test.go`
  - `go/cmd/iw/v2_vertical_slice_test.go`
- Files intentionally left untouched: production Go packages, pack metadata,
  schemas, demo authorities, Node runtime/oracle artifacts, Make targets, CI,
  provider code, and Terraform state.

## Source Inputs Consulted

- Provider schemas: committed `packs/zia/schemas/provider/zia.json`, consumed
  through production module generation; no schema edits.
- OpenAPI/API contracts: N/A. The hermetic fetch contract comes from committed
  ZIA pack metadata and the existing collector implementation.
- Provider source files: N/A. Terraform 1.15.4 installed the signed
  `zscaler/zia` v4.7.26 provider for schema loading only.
- Pack metadata: `packs/zia/pack.json`, `packs/zia/registry.json`,
  `packs/zia/overrides/zia_rule_labels.json`, `packsets/zia.json`, and shared
  Zscaler pack metadata.
- Existing docs or design records: `docs/go-runtime-plan.md`,
  `docs/go-runtime-v2.md`, and the repository adversarial-review workflow.
- Other source evidence: committed authorities
  `packs/_shared/zscaler/demo/zia_rule_labels.json`,
  `demo/config/demo/zia_rule_labels.auto.tfvars.json`, and
  `demo/imports/demo/zia_rule_labels_imports.tf`; the four committed module
  goldens under `tests/fixtures/gen/zia_rule_labels`; existing
  differential-test helpers and generated Terraform smoke-test contract.
- Oracle lineage: the user-designated remote source of truth is
  `c86b8cafe68493705d4a9130f2a21e6dd05245c7`. The relevant source Git object
  IDs match candidate base `863f405`: `node-src`
  `0151971a495f0bdf1e091ab9458ab83a3a30662a`, `node-tests`
  `f749a6b4d23a0027d178d88c8a3e15e82d3f97fa`, `package.json`
  `10304d9c8369a2358bdb53579d649b0297ca0d3c`, `package-lock.json`
  `596dbbd7c7a3c1deb1f878482dd5ba18e20861d7`, build script
  `b32ad8569b49b560b6ab21cc29000944f475241f`, `tsconfig.json`
  `d1f66605c2a02ef323e50a57f9596796bda8262e`, and `tsconfig.test.json`
  `b975b3fc51a0334fed278a3fe4c1207a1410f584`.

## Generated Artifacts

- Reports: N/A.
- Schemas: N/A.
- Fixtures: N/A; no accepted fixture was changed or added.
- Snapshots: N/A.
- Demo or lab outputs: disposable pull, overlay, module, environment root,
  `.terraform` directory, and provider lock file under test-owned temporary
  directories. They are removed by `testing.T` cleanup and are not committed.
- Oracle build outputs: ignored `dist/infrawright-cli.mjs` and its checksum
  file, rebuilt twice from the source lineage above with identical output. The
  bundle is 3,036,391 bytes with SHA-256
  `b17960a361d1be929abaa37e18312b67cf18f6c291b6e5400f75acd6be1cd065`;
  the checksum file SHA-256 is
  `43dfe6fa352bdfa566de8446349a583ec5b1a867fcba7cc18def7f4055517cba`.
  Ignored `node_modules/` was installed only through `npm ci`.
- Artifact drift intentionally expected: none. The pull, tfvars JSON, and
  imports HCL must be byte-identical to the three committed authorities above.

## Expected Delta

- Expected behavior change: an opt-in test now proves the hermetic §5 chain;
  the default lane skips it visibly. The existing fetch differential helper now
  builds under a temporary package root, allowing a clean checkout without
  ignored `dist/` or prebuilt Node artifacts to run `go test ./...`; its local
  TLS fixture accepts a caller-selected resource URI/body so the checkpoint
  reuses the existing auth/cookie/CA contract instead of duplicating it.
- Expected report/count/coverage changes: none.
- Expected generated-output changes: none.
- Expected no-op areas: all production commands and artifacts. Only tests and
  the controlling plan documents change.

## Invariants Claimed

- Evidence must not be silently dropped: the test rejects unexpected HTTP
  requests, HTTP-contract violations, missing output, extra output, or byte
  drift in the fetch and transform output trees. It also requires an exact
  12-file post-generation manifest and byte-checks the four core module files
  against committed goldens.
- Generic matcher evidence must not outrank source-backed evidence: unchanged;
  N/A to this checkpoint.
- Source precedence/provenance must remain explicit: committed pack metadata
  drives selection/generation, and committed demo files are the byte
  authorities rather than output produced by the candidate during the test.
- Ambiguity must stay classified instead of being coerced to success: any
  command failure, timeout, unexpected request/file, artifact mismatch, or
  missing Terraform proof fails the opt-in test. Terraform JSON is attributed
  to each named run rather than searched as one global transcript.
- Provider-readiness counts must stay explainable: no readiness report or count
  changes.
- Adoption safety invariants: no Adopt or Apply runs. Terraform uses
  `mock_provider "zia" {}` after all ZIA/Zscaler credentials and endpoints are
  removed. Structured events assert zero resource changes for `empty_plan` and
  exactly one expected create with material name/description for `config_plan`;
  the final local-server transcript proves no post-fetch API request occurred.

## Tests Run

- Commands:
  - `npm ci`
  - `npm run build:metadata-cli` (twice)
  - `node scripts/verify-runtime-release.mjs /private/tmp/infrawright-go-foundation`
  - `cd go && go test -count=1 -v ./cmd/iw -run '^(TestRootCatalogDifferentialAgainstNodeOracle|TestTransformDifferentialAgainstNodeOracle|TestTopologyDifferentialAgainstNodeOracle|TestGenerationDifferentialAgainstNodeOracle)$'`
  - `cd go && INFRAWRIGHT_V2_CHECKPOINT=1 go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v`
  - `cd go && go test ./cmd/iw -run '^TestV2TerraformTestEvidenceRejectsMisScopedPlans$' -count=1 -v`
  - `cd go && go test -count=1 ./...`
  - `cd go && go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v`
  - `cd go && test -z "$(gofmt -l .)" && go vet ./...`
  - `cd go && bash /Users/dm/.codex/skills/go-code-review/scripts/pre-review.sh --force ./...`
  - `npm run check:all`
  - `make check-all`
  - `git diff --check`
- Relevant output summary: `npm ci` installed 29 packages and reported zero
  vulnerabilities. Both Node builds were byte-identical, and the runtime
  release verifier passed all 11 profiles. RootCatalog, Transform, Topology,
  and Generation passed with no skips in 12.050 seconds against that oracle.
  After the accepted review fixes, the opt-in chain passed in 7.99 seconds and
  emitted a sanitized transcript. The CGO-disabled, trimpath candidate hash was
  `cf7eb76529ed877c0a92540046956b43e7426aa80b4ce1cf8f74fe74b955315b`.
  Terraform 1.15.4 installed signed
  `zscaler/zia` v4.7.26; its sole lock selection matched the production pack
  pin and its lock hash was
  `5e2d47060a6a1e562a8cdf923cc60035b41700e1b3474943b6b2dff2ce9abb21`;
  validate passed. Structured events proved
  `empty_plan` had zero changes and `config_plan` had exactly one create at
  `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]`
  with the committed name and description; summary was 2 passed, 0 failed,
  0 errored, 0 skipped. The full Go suite, `make check-all`, `gofmt`, and
  `go vet` passed. `npm run check:all` reported 785 passes, zero failures, and
  two explicitly optional external checks skipped. The default focused lane
  recorded the intended skip.
- Tests not run and why: `golangci-lint` is not installed; the pre-review script
  passed with its documented `--force` behavior after `gofmt` and `go vet`
  passed. The live read-only comparison was not run because externally
  confirmed read-only OneAPI credentials are absent; no live request was
  attempted. The rebuilt oracle and newly recorded qualification evidence
  still require fresh independent review before the whole checkpoint can pass.

## Accepted Review Findings

- Per-run plan scope -> global text matching could misattribute actions ->
  decode `terraform test -json -verbose` events by `@testrun` -> assert zero
  `empty_plan` changes and exactly one expected `config_plan` create with the
  committed name/description -> opt-in transcript records both summaries and a
  focused negative regression rejects both swapped-run and extra-action cases.
- Post-fetch request gap -> Terraform inherited fixture credentials/endpoints ->
  derive a credential-free Terraform environment and re-audit the server after
  the final command -> final transcript remains exactly auth plus one GET.
- Extra generation output -> module validation permits extras -> exact 12-file
  manifest plus four committed core-module byte goldens -> opt-in run passes.
- Incomplete subprocess bounds -> checkpoint reused an ordinary `os/exec`
  helper -> route build, candidate, and Terraform commands through the existing
  bounded, process-group-isolating `internal/terraformcmd` runner with a
  five-minute deadline and 4 MiB/1 MiB output bounds -> focused and full tests
  pass.
- Missing evidence transcript -> successful outputs were held only in memory ->
  emit a sanitized candidate/version/provider/lock/validate/per-run summary and
  initially downgrade the unavailable Node-oracle cleanup evidence to blocked
  -> fresh opt-in transcript is visible without dumping the provider schema;
  the later source-bound, reproducible oracle build and four fresh differential
  transcripts now close that non-live evidence gap.
- Focused re-review result: **Approve with nits**, with no blocking finding in
  the remediation. The sole nit was this plan's inaccurate "mounted read-only"
  wording for repository symlinks; it is corrected to state that writes stay in
  temporary roots while pack authorities are referenced through symlinks.
- Final fixture-reuse recheck: **Approve**, with no blocking finding. Reusing
  the existing strict auth/cookie/CA fixture preserved both the default fetch
  differential contract and every checkpoint transcript/credential invariant.

## Known Deferrals

- Deferred work: the three-run live comparison for the singleton
  `zia_end_user_notification` (`accepted Node -> Go -> accepted Node`).
- Reason it is safe to defer: the checkpoint remains blocked and blocks C/D
  remain unauthorized; no readiness or cutover claim is made. The hermetic leg
  makes no live provider call.
- Follow-up owner or trigger: run only after this candidate has an accepted
  clean head and the credentials are externally confirmed read-only. Use the
  recorded Node bundle/checksum above, rebuilding from the same source lineage
  if the ignored artifact is no longer present.
- Deferred work: reproducible offline provider installation.
- Reason it is safe to defer: the opt-in test may use the registry or a pinned
  filesystem mirror and does not enter the default unit lane. A plugin cache
  alone is not treated as an offline proof.
- Follow-up owner or trigger: release/CI qualification before promoting the
  candidate beyond opt-in use.

## Review Focus

- Highest-risk files or paths: `go/cmd/iw/v2_vertical_slice_test.go` and the §5
  acceptance matrix in `docs/go-runtime-v2.md`.
- Specific assumptions to attack: the test cannot compare candidate output to
  output derived by the same candidate; no extra artifact can escape the tree
  checks; Node is unusable in the candidate `PATH`; Terraform cannot contact
  ZIA; structured plan actions cannot be attributed to the wrong run; the plan
  does not overclaim the blocked live evidence or authorize C/D.
- Source evidence the reviewer should verify: the three committed artifact
  authorities, ZIA pack/profile selection, the `zia_rule_labels` fetch path and
  legacy-auth behavior, and the generated mock-provider smoke-test semantics.
- Generated artifacts the reviewer should compare: pull, config, imports,
  four committed module goldens, exact generated manifest, generated smoke
  test, provider lock selection, and structured Terraform summary from a fresh
  run.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  selector widening, pagination beyond the expected single page, extra output
  files, using a repository Node artifact, real-provider credentials/API calls,
  zero-resource plans that pass vacuously, unbounded external commands, and
  equating a skipped or blocked live leg with a passing checkpoint.
