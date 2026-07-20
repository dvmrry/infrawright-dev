# A3-I sealed OpenAPI adapter-to-bundle integration — builder handoff

## Intent

- **Problem:** place A3-O's sealed optional OpenAPI diagnostics in A2's fixed
  six-artifact bundle without allowing OpenAPI state to alter source evidence,
  provenance, source summaries, artifact names, or ordering.
- **User-visible or maintainer-visible change:** `CompileQualified` now
  snapshots the qualified input, analyzes its captured OpenAPI status through
  `openapiadapter.Analyze`, and places only the sealed, source-bound result at
  `openapi-diagnostics.json`. Qualified bundles can therefore carry
  `usable`, `degraded`, or `unavailable` diagnostics as appropriate.
- **Must stay unchanged:** the exact first five artifact byte streams and
  their order; the six-name vocabulary; source counts and summaries;
  `CompileUnverified`'s absent diagnostics; generic-map isolation; and all
  CLI, publication, filesystem, network, credential, Terraform, and live
  provider behavior.

## Base / Head

- **Repository / branch:** `/private/tmp/infrawright-go-foundation`,
  `feature/go-canonjson-foundation`.
- **Base:** `4d8b0deb37ac654b7308eda29a318812a737e5a8`.
- **Head under review:** the uncommitted A3-I working-tree changes on that
  base; this parcel intentionally does not commit or push.
- **Diff command:**

  ```sh
  git diff --check 4d8b0deb37ac654b7308eda29a318812a737e5a8
  git diff 4d8b0deb37ac654b7308eda29a318812a737e5a8 -- \
    go/internal/authoring/sourceoperation \
    docs/review-handoffs/go-authoring-a3-openapi-integration.md
  ```

## Files Changed

- `go/internal/authoring/sourceoperation/v2.go`
- `go/internal/authoring/sourceoperation/bundle_test.go`
- `go/internal/authoring/sourceoperation/openapi_integration_test.go` (new)
- `go/internal/authoring/sourceoperation/doc.go`
- `docs/review-handoffs/go-authoring-a3-openapi-integration.md` (this file)
- **Intentionally untouched:** A3-M/openapimap and adapter implementation;
  contracts, source analysis/binding, roadmap and coordinator manifest; CLI,
  Make, fixtures, module dependencies, publisher, provider probe, and all
  live-system paths.

## Source Inputs Consulted

- **Provider schemas:** synthetic source-first-v2 `provider-schema.json`,
  selected and hash-bound by its source-provenance manifest.
- **OpenAPI/API contracts:** `docs/go-authoring-port-roadmap.md` §§3.5–3.6;
  `contracts.DecodeOpenAPIDiagnosticsReport`; and A3-O's sealed
  `openapiadapter.Result` API.
- **Provider source files:** the checked synthetic source-first-v2 provider
  tree and its two `observed_http` endpoint rows.
- **Pack metadata:** none.
- **Existing docs or design records:** repository `AGENTS.md`, A3-I task card,
  adversarial-review workflow, and builder handoff template.
- **Other source evidence:** the exact canonical A1 source-evidence report and
  sourcebind input-provenance bytes; fixture-local qualified input captures
  use only a local temporary Git repository with the fixture's fixed commit
  metadata.

## Generated Artifacts

- **Reports:** the existing six in-memory artifacts; only
  `openapi-diagnostics.json` is newly replaceable when a sealed result exists.
- **Schemas:** none changed.
- **Fixtures:** no repository fixture, snapshot, or golden changed. Tests use
  fixture-local temporary OpenAPI documents and manifests.
- **Demo or lab outputs:** none.
- **Artifact drift intentionally expected:** only the sixth in-memory artifact
  for non-absent captured OpenAPI states; no committed artifact bytes drift.

## Expected Delta

- **Expected behavior change:** qualified compilation emits strict sealed
  adapter diagnostics for captured OpenAPI input. Operational adapter failures
  remain successful source bundles with an `unavailable` or `degraded` report.
- **Expected report/count/coverage changes:** OpenAPI comparison partition and
  document state may change only in the sixth artifact. Source report,
  provenance, source diagnostics, JSON summary, Markdown summary, and source
  counts remain byte-identical.
- **Expected generated-output changes:** none committed; every adapter result
  is strictly re-decoded against the exact source report before inclusion.
- **Expected no-op areas:** unverified compilation remains source-only absent;
  no generic matcher, readiness, CLI, publisher, or live execution path gains
  an input.

## Invariants Claimed

- **Evidence must not be silently dropped:** the private compiler accepts an
  optional `*openapiadapter.Result`, never raw diagnostic bytes; zero or
  source-mismatched sealed results fail closed.
- **Generic matcher evidence must not outrank source-backed evidence:** the
  sourceoperation package has no `openapimap` import, and no generic-map value
  reaches the bundle or comparison accounting.
- **Source precedence/provenance must remain explicit:** detached result bytes
  are decoded with the exact already-decoded source report, enforcing report
  SHA-256, source trust, and manifest identity before bundle validation.
- **Ambiguity must stay classified instead of being coerced to success:** A3-O
  diagnostics are retained verbatim only after the strict contract decoder
  validates their complete comparison partition.
- **Provider-readiness counts must stay explainable:** usable/degraded and
  zero-selected comparison accounting is contract re-decoded; source counts
  are not read, altered, or replaced by the adapter.
- **Adoption safety invariants:** this package has no network, credentials,
  provider action, Terraform, CLI, filesystem publication, or live execution.

## Tests Run

- **Commands:**

  ```sh
  cd go
  go vet ./internal/authoring/sourceoperation
  go test -count=1 ./internal/authoring/sourceoperation
  go test -race -count=1 ./internal/authoring/sourceoperation
  go mod tidy -diff
  git diff --check
  go test -count=1 -v ./cmd/iw -run '^(TestRootCatalogDifferentialAgainstNodeOracle|TestTransformDifferentialAgainstNodeOracle|TestTopologyDifferentialAgainstNodeOracle|TestGenerationDifferentialAgainstNodeOracle)$'
  ```

- **Relevant output summary:** all focused, race, vet, tidy-diff, and
  diff-check gates passed. The four standing RootCatalog, Transform, Topology,
  and Generation byte gates all passed (12.099s). Focused tests cover absent,
  unreadable, malformed, invalid-root, degraded, and usable diagnostics; exact
  six-name order; first-five byte identity; changed report/manifest/trust
  rejection; zero result and detached-result mutation; zero selected rows; the
  real qualified captured-OpenAPI seam; and cancellation.
- **Tests not run and why:** no CLI/publisher or live provider/Terraform test
  is in this parcel. No network, credentials, or live system is valid or
  needed: the only test subprocess is local `git init/add/commit` over a
  temporary fixture directory, with prompting and global/system config
  disabled.

## Known Deferrals

- **Deferred work:** A4 provider-probe orchestration, readiness policy, and A6
  CLI/publisher behavior. A3-M completed in parallel and remains isolated from
  this bundle.
- **Reason it is safe to defer:** the compiler admits no generic map or raw
  bytes, and it neither publishes nor performs readiness accounting.
- **Follow-up owner or trigger:** the separately reviewed A4 and A6 parcels.

## Review Focus

- **Highest-risk files or paths:** `sourceoperation/v2.go` and
  `openapi_integration_test.go`.
- **Specific assumptions to attack:** result bytes cannot be substituted;
  validation is against the exact report rather than a lookalike; adapter
  operational states do not block source bundle compilation; first-five bytes
  cannot drift; and qualified production compilation really consumes the
  captured `OpenAPIStatus` snapshot.
- **Source evidence the reviewer should verify:** A3-O's Result sealing and
  diagnostic decoder binding, the source-first fixture report/provenance, and
  the six-artifact order in `v2.go`.
- **Generated artifacts the reviewer should compare:** source-only and each
  adapter-state bundle in `TestCompileSealedOpenAPIResultPreservesCoreArtifacts`;
  all first-five byte comparisons must be equal and only the sixth stream may
  differ.
- **Edge cases that could silently overclaim, remap, drop, or weaken
  evidence:** zero result, mutated returned bytes, report/manifest/trust
  mismatch, unverified input, zero selected rows, cancellation, and any import
  of `openapimap`.
