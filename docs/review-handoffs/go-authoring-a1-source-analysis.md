# A1 source analysis — builder handoff

## Intent

- **Problem:** establish a reusable, local-only, source-first analyzer for the
  authoring handoff. The analyzer must trace each selected Terraform resource
  from the provider registration and Read callback into pinned SDK source
  without allowing an OpenAPI document, a name heuristic, or an unchecked
  checkout to manufacture evidence.
- **User-visible or maintainer-visible change:** A1 supplies sealed,
  deterministic `source-evidence-report-v1` bytes for qualified source inputs
  and a separately pinned ZPA endpoint-evidence corpus. It also makes a
  resource-level missing-SDK result possible only when the manifest expressly
  records that exact provider-required SDK module/version as unavailable.
- **Must stay unchanged:** no authoring CLI is wired by this parcel; no
  network, clone, download, provider API, credential, or live Terraform access
  is introduced; A1 does not change frozen Node vectors, replace OpenAPI
  evidence, or infer curated import/identity/sensitivity/exception claims.

## Base / Head

- **Base:** `31895dbe0c45b834b637e152f031d1dcd1f250d1` (`Seal source authoring authority gaps`).
- **Head:** uncommitted A0.2/A1 working-tree candidate, frozen for adversarial
  review on top of the base above. Reviewers must inspect `git status --short`
  as well as the tracked diff because the new analyzer and ZPA corpus paths are
  intentionally untracked until acceptance.
- **Diff command:** `git diff --stat 31895dbe0c45b834b637e152f031d1dcd1f250d1`; also inspect untracked A1 paths with `git status --short` and `git diff --no-index /dev/null PATH`.

## Files Changed

- **Source-provenance contract:**
  - `go/internal/authoring/contracts/{types.go,validate.go,contracts_test.go}`
  - `go/internal/authoring/contracts/schemas/source-provenance-v1.schema.json`
  - `go/internal/authoring/sourcebind/{copy.go,load.go,load_test.go}`
- **A1 analyzer:**
  - `go/internal/authoring/sourceanalysis/{doc.go,analyze.go,analyze_test.go,hostile_test.go}`
- **Independent ZPA endpoint corpus:**
  - `go/internal/authoring/zpacorpus/endpoint_test.go`
  - `tests/fixtures/authoring/zpa-v4.4.6-endpoint-v1/**`
- **Synthetic qualified fixture refresh:**
  - `go/internal/authoring/a0fixture/fixture_test.go`
  - `tests/fixtures/authoring/source-first-v2/**` (only the source bytes,
    manifest, and hand-authored expected artifacts required to prove the raw
    `net/http` sink rule).
- **Documentation:** this handoff and the A0.2/A1 posture in
  `docs/go-authoring-port-roadmap.md`.
- **Intentionally untouched:** command wiring, `source-operation-map`,
  `source-evidence-eval`, `provider-probe`, OpenAPI adapter/parser, pack
  authoring semantics, generated Terraform artifacts, and all live-provider
  paths.

## Source Inputs Consulted

- **Provider schemas:** the checked-in ZPA provider schema bound by
  `tests/fixtures/authoring/zpa-v4.4.6-endpoint-v1/source-provenance-v1.json`.
- **OpenAPI/API contracts:** none are required by A1. The absence of a complete
  published Zscaler OpenAPI authority is a design constraint; any scraped
  document remains optional later corroboration, not source-mapping authority.
- **Provider source files:** the manifest pins ZPA provider `v4.4.6`, commit
  `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`, the plugin entry point, and all
  tracked non-test top-level `zpa/*.go` files needed for registration and
  same-package Read/helper closure.
- **SDK source files:** the manifest pins
  `github.com/zscaler/zscaler-sdk-go/v3@v3.8.40`, its selected service files,
  service/client edge, `zparequests.go`, and the generic paging helper. Its
  normal module-cache source has no Git metadata, so the manifest uses the
  reviewed subset-tree digest and individual file hashes.
- **Pack metadata:** the endpoint fixture's selected-resource binding is the
  authority for its 16 resource rows.
- **Existing docs/design records:** `docs/go-authoring-port-roadmap.md`,
  `docs/evidence/zpa-provider-v4.4.6.json`, source-provenance/report schemas,
  and the frozen Node authority context.

## Generated Artifacts

- **Reports:** hand-authored
  `tests/fixtures/authoring/zpa-v4.4.6-endpoint-v1/expected/source-evidence-report-v1.json`,
  plus the refreshed synthetic source-first report.
- **Schemas:** `source-provenance-v1` gains optional
  `unavailable_sdks` bindings with exact `module_path` and `module_version`.
- **Fixtures:** the ZPA endpoint fixture binds source hashes, module/provider
  identity, selection, expected classification partition, call anchors, and
  report/input digests; it is independent of analyzer-generated expectations.
- **Snapshots:** the analyzer receives a single defensive copy of qualified
  inputs and never reopens the source roots after qualification.
- **Demo or lab outputs:** none.
- **Artifact drift intentionally expected:** source-first synthetic provenance
  and report hashes change because its SDK builders now contain actual direct
  `net/http.NewRequest` sinks. No committed Terraform artifact bytes are in
  scope.

## Expected Delta

- **Expected behavior change:** qualified source analysis can trace a reachable
  plugin `Serve` authority or exact root-package `Provider()` authority (never
  a bare package-scope `resources` map), selected registration, Read callback,
  same-package helpers, SDK package functions and statically proven receiver
  chains. Unbound external calls remain explicit unresolved dispatch unless
  they are Go stdlib or an explicit Terraform framework/tooling package. It
  produces exactly one sorted evidence row per selected resource.
- **Expected report/count/coverage changes:** the ZPA fixture independently
  expects 15 `observed_sdk_call` rows and one `ambiguous` policy-access row;
  no `observed_http` endpoint is claimed at the bounded
  `NewRequestDo` wrapper. The final expected bytes and digests must be checked
  from source, not regenerated from the candidate.
- **Expected generated-output changes:** new source-first report fixtures,
  schema field, and canonical manifest/input/report digest assertions. No
  legacy Node report or Terraform artifact change is expected.
- **Expected no-op areas:** no CLI/unverified mode, no OpenAPI classification,
  no provider/schema semantic inference, no source download, and no live I/O.

## Invariants Claimed

- **Evidence must not be silently dropped:** unknown same-package or unbound
  external calls, unresolved dispatch, cap exhaustion, missing qualified
  source, dynamic path construction, and competing paths remain explicit
  classification/reason outcomes. A missing SDK terminal is emitted only for a
  manifest-authorized unavailable exact module/version, not for arbitrary
  missing `go.mod` requirements.
- **Generic matcher evidence must not outrank source-backed evidence:** A1 has
  no generic matcher or OpenAPI authority. It starts from Read-rooted provider
  source and only reports a raw endpoint after a direct exact `net/http`
  constructor proof.
- **Source precedence/provenance must remain explicit:** provider registration,
  Read callback, calls, SDK symbols, source locations, manifest/input hashes,
  module identities, and source trust are rendered as bounded qualified facts.
  The analyzer accepts only `sourcebind.QualifiedInputs`, whose loader-owned
  snapshot cannot be minted from ad-hoc roots.
- **Ambiguity must stay classified instead of being coerced to success:**
  multiple viable chains, including ZPA policy prerequisite plus object lookup,
  remain `ambiguous`; an authorized `sdk_source_missing` chain mixed with any
  viable, dynamic, or unresolved candidate is likewise `ambiguous` and all
  canonical chains remain visible. No candidate is chosen.
- **Provider-readiness counts must stay explainable:** report rows are keyed,
  sorted, and contract-validated; totals are recomputed from the authoritative
  partition. `observed_sdk_call` is source-call evidence, not endpoint
  evidence.
- **Adoption safety invariants:** not applicable; A1 performs no Terraform
  import/plan/apply and no live-provider I/O.

## Tests Run

- **Commands:** final commands and outputs are coordinator-owned and must be
  recorded before review. Minimum expected gate set:

  ```sh
  cd go
  gofmt -l ./internal/authoring
  go test -count=1 ./internal/authoring/...
  go test -race -count=1 ./internal/authoring/contracts ./internal/authoring/sourcebind ./internal/authoring/sourceanalysis ./internal/authoring/zpacorpus
  go vet ./internal/authoring/...
  go mod tidy -diff
  ZPA_PROVIDER_SOURCE=/absolute/pinned/terraform-provider-zpa \
  ZPA_SDK_SOURCE=/absolute/pinned/zscaler-sdk-go-v3.8.40 \
  go test -count=1 ./internal/authoring/zpacorpus -run EndpointFixture -v
  ```

  Run the repository-wide Go, race, vet, `git diff --check`, and surviving
  frozen-Node differential gates after the focused set.
- **Relevant output summary:** coordinator verification is green: repository-
  wide Go tests and vet; authoring race tests; formatting; tidy-diff; the four
  standing artifact byte-gates; the frozen Node differential; a serial
  `GOWORK=off GOPROXY=off GOSUMDB=off go test -count=1 ./...`; and the explicit
  local pinned-source ZPA gate. The recorded pinned-root ZPA baseline is
  byte-identical to the hand-authored authority: 15 `observed_sdk_call`, one
  `ambiguous`, and zero recovered HTTP endpoints; it must be rerun only with
  roots matching the manifest revision and tree. One initial offline run was intentionally
  discarded because it was incorrectly run concurrently with `make
  differential` and the two test processes raced over shared temporary
  `dist/iw-go-diff*` executables; both gates passed when run serially.
- **Tests not run and why:** no network, provider credential, cluster, live
  Terraform, or live Apply test is valid for this parcel.

## Known Deferrals

- **Unverified source and CLI mode:** A2 owns command wiring and the explicit
  diagnostic-only unverified path. A1 intentionally has only the qualified
  analyzer seam.
- **Zscaler wrapper/dataflow recovery:** the bounded A1 proof stops at
  `NewRequestDo`; `NewRequestDo → ExecuteRequest → buildRequest` is not
  direct-`net/http` evidence. A separately reviewed bounded wrapper/dataflow
  design is required before it may produce `observed_http`.
- **OpenAPI adapter and corroboration:** A3 owns optional OpenAPI handling;
  A1 remains independent because Zscaler lacks a complete published authority.
- **Whole-program/SSA analysis:** out of scope. The analyzer fails closed at
  unsupported/dynamic/capped dispatch rather than speculating.
- **Curated ZPA semantic labels:** importer grammar, state identity, sensitive
  paths, and exceptional Read semantics remain in the historical matrix; an
  endpoint chain does not prove them.

## Review Focus

- **Highest-risk files or paths:** `sourceanalysis/analyze.go`,
  `contracts/validate.go`, `sourcebind/load.go`, the direct-sink hostile tests,
  `zpacorpus/endpoint_test.go`, its manifest, and its hand-authored report.
- **Specific assumptions to attack:**
  - reachable plugin `Serve`/root-`Provider()` registration authority (and no
    bare `resources` fallback) plus callback extraction;
  - Read-root reachability versus Create/Delete decoys;
  - local/helper/receiver resolution, type/field chains, same-package unknown
    calls, cycles, duplicate maps, branches, and cap behavior;
  - whether any `NewRequest`/`NewRequestDo` name or signature can still yield a
    raw endpoint without direct exact `net/http.NewRequest` or
    `NewRequestWithContext` evidence;
  - whether `unavailable_sdks` can misclassify a framework dependency or a
    version-mismatched SDK as resource missing source, or hide a coexisting
    endpoint/dynamic/unresolved chain instead of preserving ambiguity;
  - candidate fanout: utility calls must not become fake alternate endpoint
    chains, while true multiple request chains remain ambiguous.
- **Source evidence the reviewer should verify:** the exact ZPA `ResourcesMap`,
  selected resource constructors/Read callbacks, provider→SDK call-site
  anchors, each SDK terminal, policy-access prerequisite/object dual chain, and
  direct `net/http` sink synthetic fixtures.
- **Generated artifacts the reviewer should compare:** canonical manifest,
  input-provenance and report SHA assertions; independently transcribed
  ZPA expected rows/locations/counts; synthetic expected report bytes; schema
  compatibility when `unavailable_sdks` is omitted.
- **Edge cases that could silently overclaim, remap, drop, or weaken evidence:**
  nested literals and constant-false branches, package shadowing of builtins,
  dynamic paths/format verbs, unsafe type guesses, absent/mismatched SDK roots,
  selection filtering, duplicate/unsupported registrations, output
  determinism, post-qualification source mutation, and report rows that leak
  an endpoint through a wrapper.
