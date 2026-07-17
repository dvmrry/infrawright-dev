# Go v2 work-machine test handover

This is an execution handover for Opus on the approved work machine. Test the
credential-free Go v2 checkpoint exactly as written, report results, and do not
repair or widen the implementation during the test run.

## Scope and immutable candidate

- Repository branch: `feature/go-canonjson-foundation`
- Runtime/checkpoint candidate commit:
  `5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1`
- Candidate base: `821e9b4c251c10af333990460b88f29793f4865e`,
  the merge of main/PR 247 into the previously qualified feature branch.
- The original pre-247 candidate `ade9442` passed this handover on the work
  machine. This revision requalifies the post-247 Go parity candidate.
- The branch tip may be one documentation-only commit newer than the candidate.
- This run is entirely credential-free. Do not test a real API, source provider
  credentials, run Adopt, stage imports, run a real-provider plan, or Apply.

This run is intended to prove that a fresh machine can rebuild the accepted
Node comparison oracle, reproduce the artifact gates, and execute the Go-only
operator slice against a local TLS fixture and Terraform mock provider. It does
not approve blocks C/D or complete the live-provider checkpoint.

## Opus operating rules

1. Work from a clean checkout. Do not edit, format, generate into tracked
   paths, commit, or push.
2. Run commands from the repository root unless a command explicitly changes
   directory.
3. Stop on the first failure. Preserve the failing command and sanitized output
   but never include credentials, environment values, raw live pulls, or full
   provider schemas in the report.
4. A skip in any of the four named differential gates or the opt-in vertical
   slice is a failure. The two known optional external skips in the complete
   Node suite are not part of this checkpoint.
5. Do not substitute a different branch, candidate SHA, resource, provider
   version, or test selector.

## Checkout preflight

```sh
git fetch origin feature/go-canonjson-foundation
git switch feature/go-canonjson-foundation
git pull --ff-only origin feature/go-canonjson-foundation
git status --short
git show -s --format='%H %s' 5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1
git diff --stat 5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1..HEAD
```

Expected:

- `git status --short` is empty.
- The pinned commit subject is
  `Align Go policy and Terraform bounds with PR 247`.
- The candidate-to-tip diff contains only `docs/go-runtime-v2.md`, this
  handover, and `docs/review-handoffs/go-pr247-parity-triage.md`. If it contains
  code, fixtures, pack metadata, schemas, or another plan change, stop and
  report that the test target moved.

Record these versions before testing:

```sh
go version
node --version
npm --version
terraform version
```

The qualifying run used Go 1.26.3, Node 24.15.0, npm 11.12.1, and Terraform
1.15.4 on Darwin arm64. Use those exact versions where practical. Network
access is required for `npm ci` and Terraform's signed `zscaler/zia` 4.7.26
provider installation; no provider API access is required.

## Rebuild and verify the Node oracle

```sh
npm ci
npm run build:metadata-cli
shasum -a 256 dist/infrawright-cli.mjs dist/infrawright-cli.mjs.sha256
node scripts/verify-runtime-release.mjs "$(pwd)"
```

Expected SHA-256 values:

```text
fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a  dist/infrawright-cli.mjs
df3709d7ab96761792ee6557d12c315351db83ee69fbf78bc0bed79a9ac45946  dist/infrawright-cli.mjs.sha256
```

The runtime verifier must pass all 11 profiles. A bundle hash mismatch is a
failure; record the tool versions and stop rather than accepting new bytes.

## Run the focused PR 247 Go parity regressions

```sh
cd go
go test -count=1 -v ./internal/terraformcmd ./internal/metadata \
  -run 'TestRunTerraformCommandAcceptsCISizedEnvironment|TestCommandValidationExactBoundaries|TestDriftPolicyAcceptsLosslessNumericPolicyAndRejectsEquivalentDuplicateScopes|TestDriftPolicyNumericDuplicateScopeMarkers'
cd ..
```

Required results:

- A real child process receives all 500 CI-style environment entries.
- Exactly 4,096 environment entries pass; 4,097 fail; the 256-KiB aggregate
  byte limit still fails closed.
- Lossless numeric policy versions/values are retained without rounding.
- Equivalent integral, fractional, signed-zero, and unsafe-integer spellings
  cannot evade duplicate-scope detection, while booleans, strings, and unequal
  numbers remain distinct.

## Run the four artifact byte-gates

```sh
cd go
go test -count=1 -v ./cmd/iw -run '^(TestRootCatalogDifferentialAgainstNodeOracle|TestTransformDifferentialAgainstNodeOracle|TestTopologyDifferentialAgainstNodeOracle|TestGenerationDifferentialAgainstNodeOracle)$'
cd ..
```

All four named tests and their subtests must pass with no skip. These are the
standing proof that the scope reset did not move infrastructure artifact bytes.

## Run the Go-only vertical slice

```sh
cd go
INFRAWRIGHT_V2_CHECKPOINT=1 \
  go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v
cd ..
```

Required transcript facts:

- A CGO-disabled Go candidate SHA-256 is printed.
- Terraform installs signed `zscaler/zia` 4.7.26 and the generated lock selects
  only that provider.
- `terraform validate` succeeds.
- `empty_plan` passes with zero resource changes.
- `config_plan` passes with exactly one create at
  `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]`.
- The created object has name `TestLabel_VCR_Integration` and description
  `Test Description for VCR`.
- The summary is 2 passed, 0 failed, 0 errored, 0 skipped.

The test itself gives the candidate a `PATH` containing Terraform but no Node,
npm, or npx; strips all ZIA/Zscaler credentials before Terraform; and rejects
any post-fetch API request.

## Run the remaining credential-free qualification

```sh
cd go
go test -count=1 ./...
test -z "$(gofmt -l .)"
go vet ./...
cd ..
npm run check:all
make check-all
git diff --check
git status --short
```

Expected:

- All Go packages pass.
- Formatting and vet are clean.
- The complete Node lane reports 788 passes, zero failures, and two optional
  external skips.
- `make check-all` succeeds, including pack-set, module generation, demo drift,
  formatting, metadata, and root-catalog checks.
- Final tracked status remains clean. Ignored `node_modules/`, `dist/`, and test
  build output are expected.

## Optional binary smoke check

This checks packaging only and must not call a provider:

```sh
mkdir -p /tmp/infrawright-go-v2-smoke
cd go
CGO_ENABLED=0 go build -trimpath -o /tmp/infrawright-go-v2-smoke/iw ./cmd/iw
/tmp/infrawright-go-v2-smoke/iw --help
cd ..
```

Do not run `fetch` against a configured endpoint from this binary during this
handover.

## Result report for the originating task

Return one compact report containing:

```text
Verdict: PASS or FAIL
Branch tip:
Candidate commit verified: yes/no
OS/architecture:
Go / Node / npm / Terraform versions:
Oracle bundle SHA-256:
Runtime verifier profiles:
Four differential gates: PASS/FAIL, skips:
Vertical slice: PASS/FAIL, candidate SHA-256, provider lock SHA-256:
Full Go suite: PASS/FAIL:
Node check: pass/fail/skip counts:
make check-all: PASS/FAIL:
Final tracked status clean: yes/no
First failing command and sanitized diagnostic, if any:
```

Do not call the overall §5 checkpoint PASS: the real read-only provider leg and
the final full-evidence adversarial review remain outstanding.
