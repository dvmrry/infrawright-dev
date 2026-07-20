# A3-M coordinator task — frozen generic OpenAPI map kernel

## Objective and authority

Port the reusable behavior behind Node's `openapi-map` command into a library
package. Preserve the frozen v1 report authority exactly, but keep the generic
matcher categorically ineligible for source-first readiness.

- Repository: `/private/tmp/infrawright-go-foundation`
- Branch: `feature/go-canonjson-foundation`
- Base: `4d8b0deb37ac654b7308eda29a318812a737e5a8`
- Node specification: `node-src/authoring/openapi-resource-map.ts`
- Frozen authority: `node-tests/fixtures/python-openapi-resource-map-v1.json`
- Authority SHA-256:
  `e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c`

This parcel is package-only. It adds no CLI, filesystem publication, network,
credentials, provider access, readiness accounting, source classification, or
bundle integration.

## Settled architecture

1. Add `go/internal/authoring/openapimap` for the frozen matcher/report kernel.
   It may depend on `openapiadapter`, `reconcile`, `metadata`, `canonjson`, and
   ordinary stdlib packages. It must not import `sourceanalysis`,
   `sourceoperation`, provider-readiness code, or command packages.
2. Do not expose or copy the adapter's raw parsing graph. Add one new
   `openapiadapter` file defining a typed, detached legacy-map view containing
   only: version, optional title, string server URLs, deterministic path/method
   inventory, and component-schema count. The view must be built from a
   `Document`, accept context, deep-copy pointer/slice outputs, and expose no
   `$ref`, schema map, pathname, reader, or arbitrary object.
3. The legacy view is intentionally available from `ParseForMetadata`'s
   requested-closure document. Frozen v1 inputs may omit top-level `info`; do
   not route A3-M through strict source-first `Analyze` and do not make the
   legacy kernel stricter than its Node authority.
4. Schema and optional registry inputs are detached generic JSON objects. The
   package validates object shapes at every consumed boundary, preserves
   provider-source presence (`nil` discovers; supplied empty is explicit), and
   never mutates caller data.
5. Return a sealed report that can supply detached structured data for future
   provider-probe composition and exact Python-compatible rendered bytes for
   A6. Use the existing `canonjson` renderer and code-point ordering. Do not add
   another JSON renderer.
6. Port Node semantics, including generic CRUD scoring, product/surface hints,
   registry fetch/read coverage, special resources, provider-gap metadata,
   surface-map records, and Python half-even four-decimal ratios. Do not
   simplify output shape merely because the kernel is legacy-only.
7. Every export has a doc comment naming its Node source. No commits or pushes.

Owned writes are limited to:

- new `go/internal/authoring/openapimap/**`;
- new `go/internal/authoring/openapiadapter/legacy_map.go` and one new focused
  test file for that view; and
- `docs/review-handoffs/go-authoring-a3-openapi-map.md`.

Do not edit existing adapter production/tests, A3-I files, roadmap/manifest,
CLI, Make, fixtures, or module dependencies. Stop and report if this ownership
is insufficient.

## Invariant-to-test matrix

| Class | Inputs and aliases | Expected result | Authority / regression |
|---|---|---|---|
| Provider selection | direct resource schema; one provider; exact source; unique suffix; absent vs explicit empty; multiple providers | exact Node selection or fail closed | Node source plus focused tables |
| Path inventory | lower-case HTTP methods; parameters; prefix stripping; product-prefix stripping; suffix matching | deterministic candidates and ranks | frozen report cases plus helper tables |
| CRUD classification | exact plural; suffix plural; ties; low score; collection without detail | matched, ambiguous, or exact reason/status | frozen authority |
| Special resources | ZTC alias/action; allocation action; primary IP/MAC relationship | exact special shape and static contract | frozen/retained vectors |
| Metadata | read/write schemas; aliases; computed-only fields; provider gaps | exact top-level lists/counts | `Document.Metadata` plus frozen authority |
| Registry coverage | fetch/read; product mismatch; mapped/unmapped/ambiguous/GraphQL; parameter-name variants | exact resource rows, warnings, ratios | frozen authority plus focused tables |
| Float parity | zero denominator; half-even 32 and 160 cases; ordinary ratios | exact four-decimal Python rounding and rendered spelling | frozen half-even vectors |
| Ordering/accounting | Unicode keys; equal scores; families/surfaces/diagnostics/records | code-point sorted output; all totals recompute | byte render and count assertions |
| Legacy-view boundary | missing `info`; detached slices/pointers; cancellation; caller mutation | bounded view works without raw-map leakage | focused adapter-view tests |
| Authority separation | dependency/import audit | no source-first/readiness consumer can import generic matches as evidence | `go list -deps` and `rg` gate |

The worker must implement every matrix row before declaring review readiness.

## Verification

From `go/`:

```sh
gofmt -l internal/authoring/openapimap internal/authoring/openapiadapter
go vet ./internal/authoring/openapimap ./internal/authoring/openapiadapter
go test -count=1 ./internal/authoring/openapimap ./internal/authoring/openapiadapter
go test -race -count=1 ./internal/authoring/openapimap ./internal/authoring/openapiadapter
go mod tidy -diff
git diff --check
```

Replay every frozen report case, verify the authority hash, run the Go
error-handling checker, and report files/LOC plus any semantic mismatch. Do not
implement the frozen CLI case; A6 owns argument/output routing.

## Stop conditions

Stop rather than redesign if exact parity appears to require exposing the raw
adapter graph, importing source-first readiness code, changing an accepted A3-O
file, adding a dependency, or widening CLI/filesystem scope.
