# A2 source operation and evaluation — builder handoff

## Intent

- **Problem:** compile A1 source-first evidence into a deterministic,
  complete-set authoring bundle without letting diagnostics, summaries, legacy
  compatibility code, an unverified run, or an OpenAPI adapter alter the
  authoritative source evidence or its readiness counts.
- **User-visible or maintainer-visible change:** the candidate supplies a Go
  package seam for a sealed v2 source-operation bundle containing the
  exact source report, exact input provenance, source-bound diagnostics and
  summaries, and deterministic `absent` OpenAPI diagnostics. It also ports the
  frozen v1 mapper/evaluator/comparator/Markdown behavior into isolated
  compatibility code, guarded by frozen Node authorities.
- **Must stay unchanged:** no authoring command, CLI flag, help text, Make
  target, or `iw` wiring is added; no source-first output is fed into readiness
  or legacy `mapped` consumers; no OpenAPI parsing/comparison is implemented;
  no existing Node fixture or source-first authority is rewritten; no network,
  provider API, credential, or live Terraform access is introduced.

## Base / Head

- **Repository / remote / branch:** `/private/tmp/infrawright-go-foundation`,
  `origin` = `https://github.com/dvmrry/infrawright-dev`, branch
  `feature/go-canonjson-foundation`.
- **Base:** `113bd8e5365d8ba1c1637a994e6a943634229204` (`Implement
  source-first authoring analysis`).
- **Head under review:** the complete A2 working-tree candidate on that base,
  including the `sourceanalysis` extension and new `sourceoperation` package.
  It was reviewed before the coordinator commit; use the resulting A2 commit
  and this base to reconstruct the accepted diff.
- **Diff commands:**

  ```sh
  git diff --check 113bd8e5365d8ba1c1637a994e6a943634229204
  git diff 113bd8e5365d8ba1c1637a994e6a943634229204 -- go/internal/authoring/sourceanalysis
  git diff --no-index /dev/null go/internal/authoring/sourceoperation
  git status --short
  ```

## Files Changed

- **A1/A2 integration and diagnostic-only boundary:**
  - `go/internal/authoring/sourceanalysis/analyze.go`
  - `go/internal/authoring/sourceanalysis/analyze_test.go`
  - `go/internal/authoring/sourceanalysis/doc.go`
- **New A2 v2 bundle compiler:**
  - `go/internal/authoring/sourceoperation/{doc.go,v2.go,diagnostics.go,summary.go,bundle_test.go}`
  - `go/internal/authoring/sourceoperation/testdata/source-first-v2/{source-diagnostics.json,summary.json,summary.md}`
- **New, isolated frozen-v1 compatibility package surfaces:**
  - `go/internal/authoring/sourceoperation/{legacy_compare.go,legacy_eval.go,legacy_eval_test.go,legacy_facts_evidence.go,legacy_mapper.go,legacy_mapper_derive.go,legacy_mapper_support.go,legacy_mapper_test.go,legacy_provider_evidence.go,legacy_sdk_path.go}`
- **Documentation in this candidate:**
  - `docs/go-authoring-port-roadmap.md`
  - `docs/review-handoffs/go-authoring-a2-source-operation.md`
  - `docs/review-handoffs/go-authoring-a2-adversarial-review-mapper.md`
  - `docs/review-handoffs/go-authoring-a2-adversarial-review-bundle.md`
- **Intentionally untouched:** source-provenance and source-evidence schemas;
  `sourcebind` capture/loading; all `node-src`, `node-tests`, and existing
  frozen source-first fixtures; command packages; `iw` help/CLI; Makefiles;
  provider probe; reconcile; OpenAPI parser/adapter; generated Terraform
  outputs; and live-provider paths.

## Source Inputs Consulted

- **Provider schemas:** the synthetic checked-in provider schema selected by
  `tests/fixtures/authoring/source-first-v2/source-provenance-v1.json`; its
  SHA-256 is `4f25680ef6ec3e8d6014e4683edb1e3c47cf54b5c7a0299af7b21ce4a85bfbe3`.
- **OpenAPI/API contracts:** none are an A2 input. The v2 candidate emits only
  the ordinary `absent` document state; it deliberately does not claim
  usable/degraded/unavailable adapter states or compare operations.
- **Provider and SDK source:** the independent synthetic provider/SDK source
  trees and selected eight resource rows from the verified source-first v2
  fixture. The provider identity is `fixture/sourcefirst-provider`, module
  `example.invalid/terraform-provider-sourcefirst`, reproducible revision
  `c37dc3c4bdd98adf61862e76d67803469bd5b35d`, and tree digest
  `d8088f0de778b1a689dc78d20797a1fde5fd7db2fbf01e145d272d1a03257ce6`.
  The SDK is `fixture/sourcefirst-sdk`, module
  `example.invalid/sourcefirst-sdk@v0.0.0`, with tree digest
  `06068ec6c7370d83eefab54b9a7679dd513a3e01a31301b4bd220640a6e35737`.
- **Pack metadata:** none. The synthetic manifest selection and its explicit
  `reviewed_not_applicable` filter are the authority for the selected table.
- **Existing docs/design records:** `docs/go-authoring-port-roadmap.md` §§3.2.1,
  3.3, 3.5, and 3.6; A1 handoff and both A1 adversarial review records; and
  the adversarial-review workflow/templates.
- **Other frozen evidence:** the A1 ZPA endpoint corpus remains a source-only
  analyzer authority, not an A2 output authority. Its manifest SHA-256 is
  `577eaf74544f0d24a52205a13922ca4cc3803701cfda7557da6367e68ead55bc` and
  its hand-authored report SHA-256 is
  `4897d20a680c433473b34459c885c20c2067de12c860640b3730111bbd279039`.
  Frozen Node compatibility authorities are
  `python-source-operation-map-v1.json`
  (`0fc8279c122179047ac8895424d14ccc3922b30e840d48cfae6ec47d2fbdb767`)
  and `python-source-evidence-eval-v1.json`
  (`5f94567238aabfc6522b07863b764719ceef7708bc8f55b8e12db13f88bf299e`).

## Generated Artifacts

- **Reports:** `source-registry.json` retains the exact A1
  `source-evidence-report-v1` bytes; it is not a new registry schema.
  `source-diagnostics.json` and `summary.json` bind its SHA-256 and are exact
  projections of the report. `summary.md` is rendered from that validated
  report. `openapi-diagnostics.json` is the deterministic `absent` projection.
- **Schemas:** no schema changes. Existing strict contracts validate the source
  report/provenance relation and derived OpenAPI diagnostics.
- **Fixtures:** no existing source-first fixture is changed. The new
  hand-authored A2 goldens are
  `go/internal/authoring/sourceoperation/testdata/source-first-v2/source-diagnostics.json`
  (`2e9ff918582baa913c95f9f5f07b4bc38ba537d21433dddf7996f00b1311b79a`),
  `summary.json`
  (`0ae24c2893501dfdfb46f9c23bc92df4a8659ec98bb557ff448fadf5e20b2134`),
  and `summary.md`
  (`fabab5e26d5a131dc930bccac0caf74cce14bcf942f3788921386be3d9db1e16`).
  They are independently transcribed from the frozen synthetic v2 authority:
  manifest `f12cf9a5321e46a776e88da4610bfb834e81cdfb02e708e5d1ecbf2e0996c9c7`,
  input provenance `105428ec55e30e3291a18bac4886c57417a52da5057a74927de576e4daf18ff1`,
  source report `af9f19e3a02957d0607e9f4dd7de74f1911adf8f8373cc9f4d3f2dae8573cdf6`,
  and absent OpenAPI diagnostics
  `c4ea16d5408898a02e022596c4a40e4e3400a21e250641ec378919179919b9c2`.
- **Publication:** none in A2. The candidate returns detached, validated bytes
  only. The reviewed all-or-nothing filesystem publisher is deferred to A6,
  which must state an enforceable ownership/concurrency boundary. Portable
  `os.Root` operations do not protect a pathname transaction from an active
  same-UID writer, so this package makes no atomic replacement or filesystem
  mode claim.
- **Artifact drift intentionally expected:** three new A2 diagnostic/summary
  goldens and six v2 bundle artifacts are new. Existing fixture and Node
  authority bytes must not drift.

## Expected Delta

- **Expected behavior change:** package callers can compile qualified A1
  evidence plus qualified sourcebind inputs into the fixed v2 complete set, or
  compile explicitly unverified evidence only through a distinct sealed
  capability. `Bundle.Artifacts` returns the complete ordered set as detached
  byte slices; it performs no filesystem writes.
- **Expected report/count/coverage changes:** none to the authoritative source
  report. The synthetic bound table remains eight selected rows: two
  `observed_http`, one `observed_sdk_call`, one each `ambiguous`, `dynamic`,
  `unresolved`, `no_source`, and `not_applicable`; source-call total 4,
  endpoint total 2, coverage 2/7. Derived artifacts must copy this exact
  partition and cannot improve or suppress a count.
- **Expected generated-output changes:** new derived diagnostics/summary bytes
  named above and deterministic absent OpenAPI diagnostics. The source registry
  and input provenance are byte-for-byte input retention.
- **Expected no-op areas:** non-absent OpenAPI behavior, source-evidence
  evaluation v2 policy/readiness, CLI/stdout/exit behavior, Make routing, and
  all actual provider-probe/reconcile orchestration.

## Invariants Claimed

- **Evidence must not be silently dropped:** the source registry and provenance
  are exact retained bytes; resource diagnostics preserve every sorted row's
  classification, reason, and chain count. Bundle validation rejects missing,
  duplicate, noncanonical, or altered artifacts.
- **Generic matcher evidence must not outrank source-backed evidence:** no v2
  generic matcher exists in A2. Legacy matching code is compatibility-only and
  cannot feed a v2 bundle or readiness count.
- **Source precedence/provenance must remain explicit:** every derived JSON
  includes source trust, optional manifest hash, and SHA-256 of the exact
  source-report bytes; summary data must equal the report summary. The input
  provenance bytes are preserved without re-rendering.
- **Ambiguity must stay classified instead of being coerced to success:** the
  synthetic ambiguity row remains `multiple_viable_candidates`; derived views
  must retain it and no summary projection can convert it to mapped coverage.
- **Provider-readiness counts must stay explainable:** the source report is the
  only count authority. Derived source diagnostics and summaries are validated
  exact projections; a mutation test changes a derived classification and must
  fail validation. Unverified reports force `legacy_mapped: false` and cannot
  be passed to the qualified compilation API.
- **Adoption safety invariants:** A2 has no Terraform action, network access,
  provider access, or filesystem publication. It therefore cannot replace,
  remove, or partially write an artifact directory.

## Tests Run

- **Commands (all passed from `/private/tmp/infrawright-go-foundation` unless
  noted):**

  ```sh
  cd go
  go test -count=1 ./internal/authoring/sourceanalysis ./internal/authoring/sourceoperation
  go test -race -count=1 ./internal/authoring/sourceanalysis ./internal/authoring/sourceoperation
  go vet ./internal/authoring/sourceanalysis ./internal/authoring/sourceoperation
  git diff --check 113bd8e5365d8ba1c1637a994e6a943634229204
  ```

- **Relevant output summary:** focused tests passed: `sourceanalysis` in
  0.468s and `sourceoperation` in 0.642s; race tests passed in 1.712s and
  3.250s respectively; `go vet` and `git diff --check` were silent/successful.
  The tests directly cover exact source/provenance retention,
  deterministic/portable rendering, malformed or mismatched input rejection,
  separate unverified capability and false legacy mapping, detached bundle
  bytes, and exact fixed-set validation. Frozen v1 tests hash and compare their
  Node authorities.
- **Coordinator acceptance gates:** after the review/fix/recheck loop, the
  coordinator ran `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`,
  `go test -race -count=1 ./internal/authoring/...`, `go mod tidy -diff`, the
  four standing `RootCatalog|Transform|Topology|Generation` byte gates,
  `make differential`, and a full `GOWORK=off GOPROXY=off GOSUMDB=off`
  repository test. All passed. The pinned external ZPA source remained at
  `dcf12469a9a8f648be0691c74e9816fc94ec7ddc`; all four exact endpoint-fixture
  tests passed against SDK v3.8.40.
- **Tests not run and why:** no network, provider credential, live Terraform,
  provider API, or live apply test is valid for this bounded package parcel.
  CLI/Make authoring command checks remain A6 work. The coordinator runs the
  full repository and surviving frozen-Node gates before committing A2.

## Known Deferrals

- **A3 OpenAPI adapter:** only `absent` is emitted. A3 owns parsing, local-ref
  handling, usable/degraded/unavailable document states, comparisons, optional
  maps, and strict-adapter behavior. This is safe because absent is the normal
  source-only state and no adapter result can change source counts.
- **Source-evidence evaluation v2 / readiness consumers:** A2 retains the
  frozen v1 evaluator only as a quarantined differential surface. Any v2
  readiness policy needs a separately reviewed consumer that accepts only
  verified report/provenance identities.
- **CLI/help/Make:** A6 alone owns source-operation-map and source-evidence-eval
  argument parsing, legacy/v2 destination selection, stdout/stderr/exit
  contracts, single `iw` help exposure, Make routing, and filesystem
  publication. Its publisher needs a separately reviewed ownership/concurrency
  contract; it must not claim same-UID safety from portable pathname checks.
  No package API is a substitute for that work.
- **Provider probe and reconcile:** A4/A3 own orchestration and reconciliation;
  the bundle package does not prepare roots, download source, or invoke tools.
- **Final corpus acceptance:** complete. Two independent fresh-context A2
  reviews ran through findings, fixes, focused rechecks, and final approval.

## Builder Finding / Fix Chronology

1. **Trust boundary needed at the analyzer seam:** a diagnostic analysis result
   could not be allowed to masquerade as qualified A1 evidence. The candidate
   added a separate sealed `UnverifiedEvidence` type, `AnalyzeUnverified`, and
   `CompileUnverified`; qualified compilation still accepts only
   `QualifiedEvidence`. Regression coverage rejects zero/inconsistent captures
   and proves every unverified `legacy_mapped` value is false.
2. **Derived reports could become a second count authority:** A2 initially
   needed an explicit rule preventing summary/diagnostic drift from silently
   altering the source partition. It now carries the exact source-report SHA,
   checks source trust/manifest identity, and validates each row plus the exact
   report summary. The regression mutation of derived diagnostics is rejected.
3. **Optional adapter scope needed a hard boundary:** A2 confines itself to
   deterministic `absent` OpenAPI diagnostics with `not_attempted` rows; it
   owns no parser or other document state. This prevents an incomplete adapter
   from blocking or redefining source-only evidence. A3 remains the named owner
   of every other adapter state.
4. **The initial filesystem publisher overclaimed portable safety:** fresh
   review found unavoidable same-UID races between stage creation and binding,
   validation and rename, and identity checks and destructive actions. Rather
   than build another filesystem abstraction or preserve a false guarantee,
   A2 removed publication entirely. A6 now owns that separately reviewed
   concern under an explicit ownership/concurrency contract.
5. **Frozen v1 behavior needed isolation rather than reuse as v2 policy:** the
   mapper/evaluator/comparator and Markdown renderer live under `legacy_*`,
   hash their frozen Node fixture authorities, and have no API path into v2
   source-report compilation or readiness accounting.

This chronology is builder orientation, not acceptance evidence. Fresh
reviewers must verify every item from the working-tree diff, contracts,
fixtures, and test output.

## Review Focus

- **Highest-risk files or paths:** `sourceanalysis/analyze.go`,
  `sourceoperation/v2.go`, `diagnostics.go`, `summary.go`, their
  tests, and the synthetic v2 authority chain.
- **Specific assumptions to attack:**
  - whether `source-registry.json` can be substituted, re-rendered, or made to
    carry anything other than the exact source-evidence-report bytes;
  - whether diagnostics, JSON/Markdown summaries, or absent OpenAPI diagnostics
    can change classifications, reasons, chain counts, or totals while retaining
    a plausible SHA;
  - whether an unverified observation can enter qualified compilation, acquire
    a manifest identity, or produce `legacy_mapped: true`;
  - whether the quarantined legacy mapper/evaluator can be called by a v2
    path or influence readiness; and
  - whether any filesystem write or publication API remains in A2 despite the
    compile-only correction.
- **Source evidence the reviewer should verify:** the synthetic provider/SDK
  source and selected manifest binding; the eight authoritative report rows;
  the corresponding source diagnostics/summary projections; the A1 ZPA hashes
  as unchanged context; and the exact frozen Node fixture hashes in the legacy
  tests.
- **Generated artifacts the reviewer should compare:** the manifest → input
  provenance → source report → absent OpenAPI diagnostics chain; all three new
  A2 goldens; retained source registry/provenance bytes; and the exact ordered,
  detached six-artifact bundle returned by the compiler.
- **Edge cases that could silently overclaim, remap, drop, or weaken evidence:**
  unverified direct HTTP rows, null versus present manifest hashes, canonical
  JSON/trailing Markdown newline drift, duplicate/missing artifact names,
  maliciously altered derived rows, mutable returned byte slices, any hidden
  filesystem write seam, and accidentally treating A2's `absent` state as an
  adapter failure or coverage result.

## Review State

**Accepted after two independent fresh-context adversarial reviews.** The
mapper/evaluator review closed the fourth frozen evaluator-vector gap. The
bundle/artifact review rejected the first filesystem publisher; A2 removed it
and the reviewer approved the compile-only correction. Results are recorded in
`go-authoring-a2-adversarial-review-mapper.md` and
`go-authoring-a2-adversarial-review-bundle.md`. Reviewers made no edits.
