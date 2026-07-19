# A3 coordinator manifest — reconciliation and optional OpenAPI

## Repository and objective

- Repository: `/private/tmp/infrawright-go-foundation`
- Branch: `feature/go-canonjson-foundation`
- Base: `540be8b719713331763f8a4dd37763de7f38ec05`
- Objective: port the reconciliation and optional OpenAPI package layer while
  preserving source-first precedence and the frozen Node compatibility
  authorities. A3 adds no CLI and publishes no files.
- Current parcel status: A3-R is implemented and accepted after fresh-context
  adversarial review. A3-O remains blocked on the internal-only Artifactory
  probe below; A3-M and A3-I remain downstream of A3-O.

## Settled architecture

1. `go/internal/authoring/reconcile` owns API/schema/override reconciliation
   and the shared field-alias function. It does not parse OpenAPI or publish.
2. `go/internal/authoring/openapiadapter` owns strict OpenAPI parsing,
   validation, closed local-ref resolution, normalized operations/field
   metadata, and source-first comparison diagnostics. It consumes only the
   detached bytes captured by `sourcebind`; it has no path-based read API.
3. `go/internal/authoring/openapimap` owns the frozen generic v1 map kernel. It
   may consume adapter data and `reconcile` aliases, but it is legacy/probe
   output and is ineligible for v2 readiness or source classification.
4. `sourceoperation` remains the v2 bundle assembler. A sealed adapter result
   may replace only `openapi-diagnostics.json`; arbitrary caller bytes cannot
   mint or replace artifacts.
5. The v2 source-operation bundle always has the same six artifact names.
   `openapi-diagnostics.json` is the comparison artifact. There is no separate
   v2 comparison file, and `openapi-map.json` is not a source-first artifact.
6. A3 is library-only. A6 owns every authoring CLI flag, command, help string,
   stdout/stderr/exit contract, Make target, and filesystem publisher.

## Load-bearing invariants

- Provider/SDK source evidence is primary. OpenAPI can corroborate, report a
  missing path or ambiguity, or record a conflict supported by a trusted
  shared identity/explicit binding; it cannot change source classifications or
  counts.
- Only an `observed_http` row is comparison-eligible. Name/token heuristics can
  never manufacture a conflict.
- Optional-adapter failure preserves the exact source report and complete
  six-file bundle.
- External URL and arbitrary filesystem `$ref` reads are impossible. The
  adapter resolves only manifest-bound local files already captured in memory.
- Frozen legacy report bytes remain exact, but legacy generic matches cannot be
  consumed as qualified evidence.

## Dependency decision

- Exact candidate: `github.com/getkin/kin-openapi v0.140.0`.
- Upstream probe: downloaded successfully with Go 1.26.3; module checksum
  `h1:JFn675aXRFjyiZKa/BFWploGldQlI0gobp4J5k0EZ2g=` and `go.mod` checksum
  `h1:lISrB64F0CPcuDJ3LdtPTMJBY8VENjR9wJBdrcT6J3g=`. The module declares Go
  1.25. `go test -run '^$' ./openapi3 ./openapi2 ./openapi2conv` compiles all
  three packages under Go 1.26.3. The full upstream test suite is not an
  Infrawright gate: it includes network-dependent cases and fixtures
  that try to write inside Go's read-only module cache.
- Internal status: unverified. This host exposes only public/direct module
  access. A3-O must not edit `go.mod` until the exact version and module graph
  have been fetched through the internal Artifactory-backed `GOPROXY`.
- Loader rule: a custom `ReadFromURIFunc` bypasses the library's
  `IsExternalRefsAllowed` guard. A3-O therefore must install a closed reader
  that serves only the manifest-bound in-memory map and rejects every other
  scheme/path; it must never delegate to the library default reader.
- Validation is two-phase. A lossless generic JSON/YAML tree first enforces the
  ref allowlist and identifies the exact operation/ref closure needed by the
  source comparison. Full `kin-openapi` load and validation then establishes
  `usable`. Because the library eagerly resolves and validates the whole
  document, a full-load failure may become `degraded` only when the raw pass
  proves every comparison-required closure independently resolvable; a defect
  in a required closure is `unavailable`. The raw pass classifies scope but is
  not a second OpenAPI validator.
- Preserve the retained Swagger 2 field-metadata behavior. Use kin-openapi's
  `openapi2` model and `openapi2conv` for the current single-document/internal-
  ref behavior; do not send Swagger 2 bytes to `openapi3.Loader`. Multi-file
  Swagger 2 refs are not a frozen capability and must fail closed unless the
  bounded raw resolver can prove and inline them without changing `$ref`
  sibling semantics. That limitation must be explicit in tests and the A3
  handoff rather than silently routed through OAS3.
- The validation clone may replace out-of-range lossless numeric constraints
  with finite same-sign values, matching the Node validator's validation-only
  graph. The authoritative raw tree and captured bytes retain the exact number
  lexemes; the sanitized graph never becomes an emitted artifact.

Run this from a work machine with the internal proxy URL supplied explicitly;
do not include a `direct` or public fallback:

```sh
export A3_ARTIFACTORY_GOPROXY='https://<internal-artifactory>/api/go/<repo>'
case "$A3_ARTIFACTORY_GOPROXY" in
  *proxy.golang.org*|*direct*|off|'') echo 'internal-only GOPROXY required' >&2; exit 1 ;;
esac
A3_PROBE_ROOT="$(mktemp -d)"
A3_MODULE_CACHE="$(mktemp -d)"
cd "$A3_PROBE_ROOT"
go mod init infrawright.local/kin-openapi-probe
go mod edit -require=github.com/getkin/kin-openapi@v0.140.0
GOMODCACHE="$A3_MODULE_CACHE" GOWORK=off \
  GOPROXY="$A3_ARTIFACTORY_GOPROXY" GOSUMDB=off \
  go list -mod=mod -deps github.com/getkin/kin-openapi/openapi3
GOMODCACHE="$A3_MODULE_CACHE" GOWORK=off GOPROXY=off GOSUMDB=off \
  go list -mod=readonly -deps github.com/getkin/kin-openapi/openapi3
GOMODCACHE="$A3_MODULE_CACHE" GOWORK=off GOPROXY=off GOSUMDB=off \
  go list -m all
```

The coordinator records the sanitized proxy identity, exact module list, and
success of the offline second pass before A3-O begins. The probe directories
contain only disposable module metadata/cache and may then be removed.

## Work manifest and integration order

| ID | Deliverable | Owned files | Dependencies | Focused verification |
|---|---|---|---|---|
| A3-R | Reconciliation kernel and frozen report/helper tests | new `go/internal/authoring/reconcile/**` | none | all non-OpenAPI helper vectors and all report vectors in `python-reconcile-schema-api-v1.json` |
| A3-O | Strict adapter, field metadata, source-first comparison diagnostics | new `go/internal/authoring/openapiadapter/**`, `go/go.mod`, `go/go.sum` | recorded Artifactory check | JSON/YAML, local refs, unavailable/degraded, no external reads, comparison partition |
| A3-M | Frozen generic OpenAPI map kernel | new `go/internal/authoring/openapimap/**` | A3-R, A3-O | all report vectors in `python-openapi-resource-map-v1.json`; no readiness imports |
| A3-I | Sealed adapter-to-bundle integration | `go/internal/authoring/sourceoperation/{v2.go,bundle_test.go,doc.go}` and adapter goldens | A3-O | exact six names; source/provenance bytes unchanged; non-absent diagnostics validated |

Integration order is A3-R, then A3-O after the Artifactory gate, then A3-M and
A3-I in parallel, followed by coordinator integration and fresh review.

## Authority and gates

- Reconciliation authority:
  `node-tests/fixtures/python-reconcile-schema-api-v1.json`, SHA-256
  `464121fe2e7edcc09861ea046c10aa54d4d101145803d5af13adb41b56c5cbd7`.
- OpenAPI-map authority:
  `node-tests/fixtures/python-openapi-resource-map-v1.json`, SHA-256
  `e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c`.
- Node sources: `node-src/authoring/reconcile-schema-api.ts`,
  `node-src/authoring/openapi.ts`, and
  `node-src/authoring/openapi-resource-map.ts`.
- Every parcel: `gofmt`, focused `go test`, focused `go test -race`, `go vet`,
  `go mod tidy -diff`, and `git diff --check`.
- Integrated A3: `go test ./...`, authoring race tests, offline
  `GOPROXY=off` tests, the four standing artifact byte gates, and the frozen
  Node differential gates that do not require network.
- This surface is review-required under `AGENTS.md`. Builders stop at ready for
  review; a fresh Codex reviewer records findings and does not edit files.

## Explicit exclusions

- No CLI, Make, help, publication, provider download/preparation, live API,
  credentials, Terraform, commit, or push.
- No new source-evidence classification, readiness denominator, generic
  matcher authority, or artifact filename.
- No A4 provider-probe orchestration, A5 transform/adopt work, or A6 command
  wiring.
