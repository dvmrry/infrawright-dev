# CI Pack-Scope Discovery Review Handoff

## Intent

- Make repository Go tests run against the pack directories that are physically
  installed, including an empty pack root and every partial committed profile.
- Keep engine contracts active through synthetic packs; skip only assertions
  whose meaning depends on a specific unavailable committed pack.
- Add a non-required CI matrix that physically replaces `packs/` with each
  committed profile before running `go test -count=1 -json ./...`.
- Preserve production behavior, pack contents, schemas, overrides, fixtures,
  snapshots, and generated outputs.

This branch does not claim that every provider family has equivalent fixtures.
It claims only that an absent pack cannot make unrelated engine tests fail.

## Base / Implementation Head

- Base: `0a11d15ebe9e8021f84c4a352980b09a066169ac`
- Published implementation head before this handoff rename:
  `273e0d65c02b9d4922e98598382a791f39b8c132`
- Diff command:
  `git diff 0a11d15ebe9e8021f84c4a352980b09a066169ac..273e0d65c02b9d4922e98598382a791f39b8c132`

## Acceptance Contract

For every profile in `packs/*.packset.json`—`aws`, `cloudflare`, `empty`,
`full`, `google`, `netbox`, `zcc`, `zia`, `zpa`, `zscaler`, and `ztc`—CI must:

1. retain all profile documents;
2. physically replace the checkout's `packs/` directory with only that
   profile's declared packs and shared entries; and
3. pass unfiltered `cd go && go test -count=1 -json ./...`.

`make check-core`, `INFRAWRIGHT_PACKS` redirection, the existing pack-profile
matrix, focused package tests, and this handoff are not substitutes for that
acceptance contract.

## Scope

- Workflow: `.github/workflows/check.yml`.
- Test-only changes under `go/cmd/iw` and `go/internal`.
- No non-test Go file changed.
- No pack, provider schema, override, evidence fixture, snapshot, generated
  module, or runtime documentation changed.
- No `overrideKeys` entry was added.

## Gate-First Evidence

The physically pruned matrix was committed and published before remediation.
Its initial local reproduction failed every profile:

| profile | status | tests | skips |
|---|---:|---:|---:|
| aws | fail | 2716 | 12 |
| cloudflare | fail | 2716 | 12 |
| empty | fail | 2716 | 12 |
| full | fail | 2778 | 3 |
| google | fail | 2716 | 12 |
| netbox | fail | 2716 | 12 |
| zcc | fail | 2716 | 12 |
| zia | fail | 2716 | 12 |
| zpa | fail | 2719 | 3 |
| zscaler | fail | 2719 | 3 |
| ztc | fail | 2716 | 12 |

The resulting work queue covered `cmd/iw`, `roots`, `openapimap`,
`transformadoptparity`, `transform`, `metadata`, `adopt`, `collectors`,
`tfrender`, `modulesgen`, `assessment`, and `envgen`. The first repository-wide
promotion run also exposed self-contained `a0fixture` Git tests whose macOS
sandbox warning is unrelated to pack availability; those tests were not
changed.

## Conversion Rules Applied

- Engine behavior uses fabricated pack roots with the minimum resource,
  schema, registry, reference, or override shape required by the assertion.
- A committed-pack compatibility contract checks its required pack paths and
  skips only when those paths are absent.
- Profile loops run each profile as a subtest, so one unavailable profile does
  not blanket-skip unrelated assertions.
- Tests and assertions were not deleted or weakened to make partial profiles
  pass.
- Production code was not changed.

## Focused Verification

- Each converted package passed unfiltered against all eleven physically
  pruned profiles before promotion.
- `envgen`: 47 tests per profile; 0 skips for `full`, 6 for `zscaler`, and
  12–14 for narrower profiles.
- `cmd/iw`: 352 tests for partial profiles and 359 for `full`; with the one
  local loopback-only test excluded, all profiles passed and full had one
  skip while partial profiles had four.
- The physically empty repository-wide promotion run passed every pack-scope
  assertion. Local macOS sandbox restrictions prevent listener-based TLS/proxy
  tests from running and can inject a `git confstr()` warning under the broad
  concurrent run; these are environment limitations, not accepted evidence.

## CI Structure

- `scope` discovers profile names from `packs/*.packset.json`.
- `pruned-pack-go-tests` copies the selected profile to a temporary directory,
  removes the checkout's original `packs/`, moves the pruned directory into
  place, and runs the complete Go suite.
- `Pruned pack Go tests` is a single aggregate status intended for branch
  protection after all eleven matrix entries pass unfiltered.
- The prior `pack-profiles` matrix remains a distribution check; it is not
  treated as proof of physical decoupling.

## Outstanding Promotion Requirement

At this implementation head, the eleven-profile unfiltered GitHub Actions run
is still required. Do not mark the aggregate context required and do not treat
this branch as complete until the run supplies the final per-profile
pass/fail, test-count, and skip-count table.

## Review Focus

- Verify the workflow truly removes the original checkout `packs/` directory.
- Look for test helpers that find repository packs directly and still assume a
  provider family is present.
- Challenge synthetic fixtures for sufficient behavioral shape and committed
  pack skips for excessive scope.
- Confirm no test was removed, no production code changed, and no pack/schema/
  override/generated artifact drift entered the branch.
- Treat the eleven-profile unfiltered result as the only completion evidence.
