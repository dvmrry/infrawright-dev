# A3-I coordinator task — sealed adapter-to-bundle integration

## Objective and authority

Layer A3-O's sealed optional-adapter result into A2's fixed six-artifact bundle
without changing source evidence, provenance, summaries, artifact names, or
trust semantics.

- Repository: `/private/tmp/infrawright-go-foundation`
- Branch: `feature/go-canonjson-foundation`
- Base: `4d8b0deb37ac654b7308eda29a318812a737e5a8`
- Governing packages: `go/internal/authoring/sourceoperation`,
  `openapiadapter`, `sourceanalysis`, and `sourcebind`
- Governing contract: `docs/go-authoring-port-roadmap.md` §§3.5–3.6

This parcel adds no CLI, publisher, generic map, provider probe, filesystem
access, network, credentials, Terraform, or live execution.

## Settled architecture

1. `CompileQualified` already snapshots `sourcebind.QualifiedInputs`; analyze
   that snapshot's `OpenAPIStatus` against the exact sealed source report and
   pass the resulting `openapiadapter.Result` into the private compiler.
2. The private compiler may accept an optional sealed result capability, never
   arbitrary OpenAPI diagnostic bytes. With no result it retains the current
   absent renderer for private fixtures and unverified source-only compilation.
3. When a result is present, take its detached canonical bytes and validate
   them against the exact decoded source report before placing them at
   `openapi-diagnostics.json`. A result built from another source report must
   fail closed through the existing source-report SHA/trust/manifest checks.
4. Preserve exactly six names and their current order. For the same source
   evidence and provenance, the first five artifact byte streams must remain
   byte-identical; only `openapi-diagnostics.json` may differ.
5. Adapter operational states—absent, unreadable, malformed, invalid root,
   degraded, usable—remain report states and do not invalidate source evidence.
   Cancellation, invalid source, or an internal adapter invariant remains a
   package error.
6. `CompileUnverified` remains source-only because `UnverifiedInputs` carries no
   OpenAPI status. It must continue emitting visibly unverified absent
   diagnostics and cannot gain qualification.
7. Do not import `openapimap`; the generic legacy matcher is not a bundle or
   readiness input. Every export retains its Node/source-contract doc comment.

Owned writes are limited to:

- `go/internal/authoring/sourceoperation/v2.go`;
- `go/internal/authoring/sourceoperation/bundle_test.go`, `doc.go`, and new
  focused test/golden files under that package; and
- `docs/review-handoffs/go-authoring-a3-openapi-integration.md`.

Do not edit A3-M/adapter files, roadmap/manifest, CLI, Make, fixtures, or module
dependencies. Stop and report if this ownership is insufficient.

## Invariant-to-test matrix

| Class | Inputs and aliases | Expected result | Authority / regression |
|---|---|---|---|
| Source-only baseline | qualified status absent; private compiler without result; unverified input | current absent bytes unchanged | existing bundle goldens |
| Adapter state | unreadable, malformed, invalid root, degraded unrelated defect, usable | exact validated diagnostics state; source bundle still succeeds | A3-O contracts plus integration tests |
| Bundle identity | every adapter state | six names/order; first five bytes identical; only sixth replaceable | byte-by-byte assertions |
| Binding | result built from exact report vs changed report/trust/manifest | exact accepted; mismatch rejected | strict diagnostics decoder |
| Capability boundary | arbitrary bytes, zero result, caller-mutated returned bytes | no arbitrary injection; detached output | compile API and mutation tests |
| Accounting | usable/degraded comparisons and zero selected rows | comparison partition/counts validate without touching source counts | contracts re-decode |
| Qualified production seam | real `QualifiedInputs` snapshot with captured OpenAPI | `CompileQualified` invokes adapter and emits non-absent state | fixture-local end-to-end test |
| Cancellation | cancelled before snapshot, analysis, or compile | wrapped cancellation error; no bundle | focused cancellation tests |
| Authority separation | dependency/import audit | no `openapimap` import and no generic match feeds readiness | `rg`/dependency gate |

The worker must implement every matrix row before declaring review readiness.

## Verification

From `go/`:

```sh
gofmt -l internal/authoring/sourceoperation
go vet ./internal/authoring/sourceoperation
go test -count=1 ./internal/authoring/sourceoperation
go test -race -count=1 ./internal/authoring/sourceoperation
go mod tidy -diff
git diff --check
```

Also run the four standing artifact byte-gates and report exact files/LOC,
artifact byte comparisons, and confirmation that no live/network path exists.
Do not commit or push.

## Stop conditions

Stop rather than redesign if integration requires changing the fixed artifact
vocabulary, accepting raw diagnostic bytes, weakening source/provenance
binding, importing the generic map, or adding CLI/filesystem/dependency scope.
