# A3-O coordinator task — closed OpenAPI adapter

## Scope and authority

- Repository: `/private/tmp/infrawright-go-foundation`
- Branch: `feature/go-canonjson-foundation`
- Base: `42c8932defdb7222562916fefd4ba789ae460338`
- Dependency: `github.com/getkin/kin-openapi v0.140.0`, confirmed available in
  the user's internal Artifactory on 2026-07-19.
- Owned implementation: new
  `go/internal/authoring/openapiadapter/**`, `go/go.mod`, and `go/go.sum`.
- No CLI, Make, filesystem publication, sourceoperation integration, provider
  access, credentials, Terraform, or live systems.

The TypeScript sources remain the compatibility specification for retained
field metadata. The v2 source-first comparison contract is defined by
`docs/go-authoring-port-roadmap.md` §3.6 and the existing
`contracts.OpenAPIDiagnosticsReport` semantic validator.

## Package boundary

The package exposes three capabilities:

1. A sealed analysis result built from a `sourcebind.OpenAPIStatus` and a
   validated `contracts.SourceEvidenceReport`. Its canonical bytes are
   defensively copied and already pass
   `contracts.DecodeOpenAPIDiagnosticsReport`. Adapter failures are represented
   by `document_state` and `reason_code`; they do not become a package error or
   invalidate source evidence. Package errors are reserved for an invalid
   source report, cancellation, or an internal invariant failure.
2. A strict, immutable parsed document for downstream A3-M use. It exposes a
   detached, deterministically sorted operation inventory and no reader,
   pathname, URL-fetch, or mutable raw-map capability.
3. Field metadata extraction for explicit read/write operation references,
   returning detached `reconcile.APIMetadata`. It preserves the retained
   `apiMetadataFromOpenApi` helper behavior, including OpenAPI 3
   response/request schemas and the current single-document Swagger 2
   body/response case. This helper boundary parses the selected version and
   resolves only the requested operation/schema closure, but does not require
   whole-document validation: the frozen helper inputs intentionally omit
   otherwise-required top-level `info`, just as the Node helper does. A6 must
   compose strict document validation separately where the CLI contract does.

Every exported function that can parse or validate accepts `context.Context`
first. Every exported slice, map, byte buffer, and pointer-bearing result is
copied on ingress and egress.

## Parsing and dependency rule

- Pin `kin-openapi` at exactly `v0.140.0`; run `go mod tidy` and record the
  minimal transitive graph. Do not add another OpenAPI implementation.
- `github.com/oasdiff/yaml v0.1.0` may be imported only as kin-openapi's own
  already-pinned YAML decoding dependency, to convert captured YAML into the
  generic JSON tree used by the raw closure pass. It is not a second OpenAPI
  validator.
- `github.com/oasdiff/yaml3 v0.0.13`, the parser already pinned beneath that
  decoder, may be imported directly only to overlay exact plain-scalar numeric
  lexemes onto the generic tree. This prevents overflow and large-integer
  rounding; it is not a second YAML or OpenAPI implementation.
- JSON passes through `canonjson.ParseDataJSONLosslessly` so duplicate keys,
  depth, and number lexemes remain controlled. YAML conversion must reject
  duplicate keys and then pass through the same strict JSON tree boundary.
- A validation-only clone replaces only numeric lexemes that overflow
  `float64` with finite same-sign values. Authoritative captured bytes and the
  raw tree are never mutated or emitted from that clone.
- OpenAPI 3 full validation uses a fresh kin loader for every invocation.
  Single-document Swagger 2 is accepted only by the bounded field-metadata
  compatibility path using the strict raw requested-closure extractor. It does
  not run a whole-document v2 conversion, because doing so would inspect and
  potentially reject unrelated operations outside the frozen helper's
  processing fence.
  Source-first diagnostics require OpenAPI 3; a Swagger 2 root is
  `unavailable/invalid_root` there.

## Closed local-reference boundary

The first captured file is the root document; remaining files are the complete
allowlist. Revalidate and copy every path, byte slice, and SHA-256 before use.
Reject duplicate identities, including the root repeated as a local ref.

The kin loader receives a custom in-memory `ReadFromURIFunc`. It must never call
the default reader. It accepts only the package's private virtual scheme and an
exact canonical key in the copied allowlist, and returns another byte copy.

Before kin loading, inspect every `$ref` reached by the raw closure resolver.
An external document reference is allowed only when its relative path resolves
from the current document to an exact allowlisted captured path. Reject URL
schemes, authority/userinfo/opaque URLs, absolute paths, backslashes, NUL,
queries, percent encoding, empty or duplicate path segments, and `.`/`..`
aliases. A fragment must be empty or a valid JSON Pointer; reject invalid
`~` escapes and encoded aliases. Internal fragments and allowlisted local-file
fragments are resolved with an explicit recursion/depth guard. There is no OS,
HTTP, environment, or fallback-read seam.

## Document-state algorithm

1. No selected input (`Available=false`, `Err=nil`) produces `absent` and
   `not_attempted` for every resource.
2. A captured-read error produces `unavailable/unreadable` and
   `not_attempted` for every resource.
3. Root syntax failure produces `unavailable/malformed`. A non-object root,
   non-OpenAPI-3 version, missing/invalid `info` object (including its required
   title/version strings), or missing/invalid root `paths` object produces
   `unavailable/invalid_root`. These root invariants are checked before the
   degraded decision, so a whole-document failure caused by a broken root can
   never be mislabeled as an unrelated-operation defect.
4. Build the exact comparison-required closure for every `observed_http`
   source row from the raw tree. A required missing, unsafe, cyclic, malformed,
   or unresolvable local ref produces `unavailable/local_ref_unresolved`.
5. Run the full closed kin load and validation. Success produces `usable`.
   Failure produces `degraded/degraded_unrelated_operation` only if the raw
   pass independently proved every comparison-required path item and operation
   object resolvable. The raw pass extracts only method/path/operationId needed
   by the comparison; it does not claim whole-document OpenAPI validity.

The report must remain valid for zero selected resources and for a degraded
document with zero comparison-eligible rows.

## Comparison algorithm

- Non-`observed_http` source rows are `not_comparable` whenever the document is
  usable or degraded.
- Inventory path operations deterministically by uppercase method, literal
  OpenAPI path template, and optional non-empty operationId.
- A viable endpoint match requires an identical method and identical slash
  segments, except that two whole-segment `{parameter}` tokens may use
  different names. No prefix, substring, name, token, or resource heuristic is
  permitted.
- Zero viable operations is `missing_path`.
- Two or more viable operations is `ambiguous`, with sorted unique literal
  candidates and `source_endpoint` basis.
- One exact literal method/path match is `corroborated` with
  `source_endpoint` basis.
- If the sole viable match differs only in parameter names, normalize that
  corroborating candidate's path to the source endpoint path. This is the only
  representation accepted by the existing report validator; retain its
  operationId and do not use this normalization for ambiguous candidates.
- A3-O emits no `conflict`: the current manifest binds document bytes but has
  no reviewed resource-to-operation identity capable of authorizing
  `trusted_shared_identity` or `explicit_binding`. A failed comparison is
  missing or ambiguous, never conflict. Adding a conflict binding is a later
  contract change, not a free-form option in this parcel.

Recompute all six counts from the completed row map and bind the exact
canonical source-report SHA. Call the existing contracts validator before
sealing the result.

## Retained field-metadata behavior

Port only the behavior behind `apiMetadataFromOpenApi` from
`node-src/authoring/openapi.ts`:

- exact `METHOD:/path` operation lookup;
- first 200 or code-point-sorted 2xx response, application/json or first
  code-point-sorted media entry carrying a schema;
- OpenAPI 3 requestBody and Swagger 2 `in: body` request schemas;
- local JSON Pointer refs, `$ref` siblings, `allOf`, arrays, required fields,
  readOnly/writeOnly, response-only derivation, and the existing depth limits;
- `reconcile`'s shared `SnakeName` behavior and detached field maps.

Do not make this helper stricter than its Node source by validating unrelated
operations or requiring top-level `info`. Syntax errors, an unsupported version,
an explicitly requested missing operation, and a ref/schema defect inside the
requested closure still fail. Unrelated document defects remain outside this
helper's processing fence.

Replay both frozen `api_metadata_from_openapi` helpers from
`python-reconcile-schema-api-v1.json` and retain the current Node Swagger 2
unit scenario. Do not implement the standalone `openapi-map` kernel here.

## Required tests

- JSON and YAML usable documents, mixed JSON/YAML local refs, and fresh-loader
  race coverage.
- Every forbidden URI/path class above, unlisted canonical refs, root/local-ref
  duplicate identities, SHA mismatch, and caller mutation before/after calls.
  Query rejection includes a bare trailing `?` (`url.URL.ForceQuery`), and
  encoded-alias rejection covers both paths and fragments.
- Internal and external JSON Pointer success, invalid tokens, out-of-range
  arrays, cycles, and recursion/depth limits.
- `absent`, `unreadable`, `malformed`, `invalid_root`, required-ref unavailable,
  unrelated-operation degraded, and zero-row reports.
- Exact, parameter-name-only, missing-method/path, and multi-candidate
  comparisons with deterministic operation ordering and all partition sums.
- A proof that no conflict can be minted and no network/file reader exists.
- Both frozen metadata helpers, the retained Swagger 2 case, lossless overflow
  validation without input mutation, allOf/array/ref siblings, and read/write
  asymmetry.

## Gates and stopping point

From `go/`:

```sh
gofmt -l internal/authoring/openapiadapter
go vet ./internal/authoring/openapiadapter
go test -count=1 ./internal/authoring/openapiadapter
go test -race -count=1 ./internal/authoring/openapiadapter
go mod tidy -diff
GOWORK=off GOPROXY=off GOSUMDB=off go test -count=1 ./...
go test -race -count=1 ./internal/authoring/...
go test -count=1 ./cmd/iw -run 'RootCatalog|Transform|Topology|Generation'
```

Also run `git diff --check` and the Go error-handling checker. Produce the
repository builder handoff. Stop ready for two fresh-context adversarial
reviews: one attacks parsing/ref/failure isolation; the other attacks source
precedence/comparison accounting/field-metadata parity. Do not commit or push.
