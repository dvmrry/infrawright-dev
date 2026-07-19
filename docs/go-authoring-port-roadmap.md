# Go authoring port roadmap

Status: DECIDED DESIGN, ready for adversarial review — implementation is the
first leg of the authority-handoff gate in
[singleton-state-topology-v2.md](singleton-state-topology-v2.md). This document
does not authorize degrouping or Node archive by itself.

The authoring port is **source-first**. Its primary evidence chain is:

```text
Terraform resource
  -> provider registration and Read/Import callback
  -> provider call site
  -> pinned SDK package/function or receiver method
  -> HTTP method and path template
  -> optional OpenAPI operation corroboration
```

OpenAPI is valuable when a complete authoritative specification exists, but it
is not required. In particular, Zscaler has no complete published OpenAPI
authority; locally scraped specifications are corroborating inputs, not the
source of truth for what the Terraform provider executes.

## 1. Command decision

Six current authoring commands are retained for the Go authority handoff:

| Command | Go contract |
|---|---|
| `reconcile` | Retain the API-object/Terraform-schema/override core. OpenAPI augmentation becomes optional. |
| `openapi-map` | Retain as an explicitly OpenAPI-backed authoring view. It cannot outrank source-backed evidence. |
| `source-operation-map` | Promote to the primary provider-to-SDK-to-HTTP mapper. OpenAPI becomes optional enrichment rather than a required argument. |
| `source-evidence-eval` | Port the existing comparison/classification and Markdown behavior. Use it during the source-first transition and retain it as a developer comparison tool. |
| `provider-probe` | Retain as orchestration over pinned provider schema/source/SDK inputs. OpenAPI becomes an optional recipe section. |
| `transform-adopt-parity` | Port independently against the frozen Node fixture/report authority. |

`zpa-provider-evidence` is **retired as an executable command**. It exists
because the Python archive reproduced a one-version evidence validator in Node,
not because Infrawright needs a permanent ZPA-specific subsystem. Preserve
`docs/evidence/zpa-provider-v4.4.6.json` and its review history as a frozen
qualification corpus. Reuse its 16 resources, 17 source files, and 45 source
anchors to attack the generic analyzer; do not port the 600-line
version-specific validator.

Retiring the command does not mean leaving the matrix unchecked. A dedicated
generic **static source-binding gate** retains the current validator's exact
checks: provider repository/ref/commit/tag; clean matrix-bound tracked source
files (the current gate intentionally ignores unrelated and untracked files);
whole-source-file and inclusive source-range hashes; exact matrix shape and
resource ordering; effective pack/schema/registry/override inputs; and the
curated fetch/import/Read-identity/state-shape/sensitivity/exception claim
shapes and anchors; and the fail-closed runtime-evidence labels. Those checks
remain source-bound corpus validation rather than a shipped command hard-coded
to one provider release. Its report labels these as curated corpus assertions,
never analyzer-derived endpoint results.

The matrix contains curated semantic claims the current generic analyzer does
not infer (import grammar, identity rebinding, sensitive paths, and exceptional
Read behavior). Those claims remain historical evidence and focused pack/runtime
test inputs. Passing the matrix corpus must never be described as automatically
proving those semantics from AST.

A separate **endpoint-evidence gate** qualifies the new analyzer. Its fixture
pins both provider and SDK roots, repository/module identities, revisions or
module-tree digests, relative-file hashes, callback and call-site provenance,
and the expected source-first classification for every selected resource.
This new independently reviewed fixture—not the curated semantic anchors in the
v4.4.6 matrix—is the authority for provider-to-SDK-to-HTTP results. The two
gates fail independently: preserving the old matrix cannot qualify an endpoint
mapping, and a valid endpoint fixture cannot waive a stale matrix binding.

## 2. Current substrate and gap

`tools/source-evidence-ast` already uses Go's parser and AST to emit:

- Go files, packages, imports, functions, and module requirements;
- Terraform resource registrations and resource references;
- Read callback fields;
- selector calls such as `client.Repositories.Get`;
- imported package calls such as `locationmanagement.Get`, including import
  path; and
- direct raw REST calls such as `client.NewRequest("GET", ...)`.

The Node layer currently associates these facts with resource files. Its SDK
path recovery is not AST-based: it scans text for receiver functions, `path`
assignments, `fmt.Sprintf`, and `NewRequest`. It does not robustly follow
top-level package functions, wrappers, helper calls, or general path
construction. Finally, the current report requires an OpenAPI operation before
it can classify a source mapping as complete.

The Go port closes that gap rather than reproducing it.

## 3. Source-first analysis contract

### 3.1 Provider analysis

For each Terraform resource selected from the provider schema:

1. Resolve its registration and constructor.
2. Resolve Read and Import callbacks, including local helper indirection.
3. Attribute provider files and functions to the resource.
4. Record direct raw HTTP calls and calls into imported SDK packages or client
   receiver chains.
5. Bind every fact to a portable source path, function, and source position.

Registration authority is closed: analysis accepts only an exact reachable
HashiCorp `plugin.Serve` `ProviderFunc`, or an exact root-package `Provider()`
factory when no reachable Serve authority exists. A package-scope `resources`
map is never an authority by itself. Imported calls are likewise preserved as
unresolved dispatch unless their package is Go standard library or an explicit
Terraform framework/tooling allowlist; arbitrary external calls cannot vanish
from the candidate set.

The analyzer consumes an explicit, pinned provider source root. It does not
download source, consult the network, or infer a provider version from an
uncontrolled workspace.

### 3.2 SDK analysis

Given an explicit pinned SDK root and its module identity:

1. Resolve provider import paths to SDK packages.
2. Resolve top-level package functions and receiver methods.
3. Follow bounded, source-local helper calls required to reach request
   construction.
4. Evaluate only a reviewed closed set of path-building expressions, initially
   string constants, constant identifiers, concatenation, `fmt.Sprintf`, and
   path joining.
5. Extract HTTP method and path template plus source provenance.

Unsupported data flow, multiple possible callees, dynamic dispatch, dynamic
paths, missing source, and multiple endpoint candidates remain explicit
unresolved or ambiguous outcomes. They never fall through to a guessed success.

Use Go parser facilities rather than another hand-written Go lexer. Start with
`go/parser`, `go/ast`, and `go/token` over explicit source roots; add `go/types`
only for a concrete local symbol/type-resolution gap. Add
`golang.org/x/tools/go/packages` only if a focused prototype proves the stdlib
source-root model cannot resolve a required retained corpus. SSA is not part of
the initial design. Revalidate any added module in Artifactory and review its
`go list`/subprocess, module-loading, timeout, output-bound, and no-public-
fallback behavior before adoption. Do not preserve the current hand-written
`go.mod` parser; use `golang.org/x/mod/modfile` if module metadata remains a
required fact, otherwise omit that fact.

### 3.2.1 Qualified source provenance

`source-operation-map`, `source-evidence-eval`, and `provider-probe` share one
local-only `source-provenance-v1` manifest contract. Qualified source-first mode
requires `--source-manifest PATH` (the probe recipe carries the equivalent
field). The manifest contains:

- provider repository and module identity, revision and tree digest, and the
  relative path plus SHA-256 for every provider file analyzed;
- provider `go.mod`/`go.sum` bindings used to resolve SDK identity;
- schema relative path and SHA-256;
- for each SDK used, module path/version, repository identity, revision or
  module-tree digest, and every analyzed SDK relative path plus SHA-256; and
- manifest version and the selected-resource/filter inputs that define the
  evidence table.

Absolute checkout paths never enter the portable manifest or its digest. Roots
are supplied locally and the command performs no clone, download, or network
resolution. It verifies identities and hashes before analysis, parses the same
stable bytes it hashed, and fails closed before publishing source artifacts if
the revision, module, schema, tree, file bytes, or selected-resource binding is
wrong. The result records the manifest hash and `source_trust: verified`.

`sdk_source_missing` is not an inference from an arbitrary missing provider
`go.mod` requirement. It is authorized only by the optional
`unavailable_sdks` manifest entries, each of which names the exact SDK module
path **and the provider-required version**. Ordinary unavailable framework or
tooling dependencies are not service-SDK evidence and must not produce a
resource-level missing-SDK classification. If a listed unavailable SDK call
coexists with an otherwise viable chain, the row still fails closed as
`ambiguous` with `multiple_viable_candidates` and retains **all** canonical
chains, including the authorized missing-source chain. `no_source` applies
only when every canonical chain is `sdk_source_missing`; no candidate is ever
selected or silently hidden.

Ad-hoc local analysis remains possible only with explicit
`--allow-unverified-source`. Such a report records `source_trust: unverified`,
may show diagnostic classifications, but is rejected by provider-readiness,
qualification, handoff, and legacy-`mapped` consumers. A bare source root with
neither the manifest nor the explicit unverified flag is a usage error (exit
2). Frozen Node command vectors run only through the legacy compatibility mode
described in §3.5; their unchanged bytes are historical differential evidence,
not qualified source-first output.

### 3.3 Evidence precedence

Evidence order is structural:

1. Direct provider raw HTTP method/path.
2. Provider call resolved through pinned SDK source to HTTP method/path.
3. Provider SDK symbol resolved without a recoverable HTTP path.
4. Generic name/schema suggestions.

An OpenAPI match may corroborate levels 1 or 2, or provide a clearly labeled
suggestion at level 4. A suggestion is never evidence and never counts as a
mapped endpoint. OpenAPI cannot replace, contradict, or outrank a source-backed
path. Conflicts are reported, not reconciled silently.

Every selected resource has exactly one row and exactly one of the following
classifications. Summary counts are recomputed only from that authoritative
resource-keyed table:

| Classification | Meaning | Source call | Observed endpoint | Endpoint denominator |
|---|---|---:|---:|---:|
| `observed_http` | One source-backed Read chain ends at a recoverable HTTP method and path template. | yes | yes | yes |
| `observed_sdk_call` | One source-backed Read chain reaches a pinned SDK symbol but no HTTP path is recoverable. | yes | no | yes |
| `ambiguous` | Multiple candidate chains remain—including an authorized missing-SDK chain mixed with viable, dynamic, or unresolved evidence—and none may be selected. | separate count | no | yes |
| `dynamic` | A source request exists but its method/path cannot be reduced by the reviewed expression set. | yes | no | yes |
| `unresolved` | Source exists, but the Read-rooted chain cannot be resolved far enough to assert a request. | no | no | yes |
| `no_source` | Required pinned provider or SDK source is absent for this resource. | no | no | yes |
| `not_applicable` | A reviewed reason says endpoint analysis does not apply to this selected schema entry. | no | no | no |

Normative totals are `selected_total`, one count for each classification,
`applicable_total = selected_total - not_applicable`,
`source_call_observed_total = observed_http + observed_sdk_call + dynamic`, and
`endpoint_observed_total = observed_http`. Endpoint coverage is
`endpoint_observed_total / applicable_total`; a zero denominator is rendered
explicitly as not applicable, never as 100 percent. Ambiguous candidates are
reported separately and never enter either numerator. `no_source` remains in
the denominator so missing pinned evidence cannot improve coverage. Resource
keys are sorted and unique, and the seven classification counts must sum to
`selected_total`.

OpenAPI document and comparison accounting is separate from this source
partition and follows §3.6. It cannot change a source classification,
source-call count, endpoint numerator, or denominator. In a **verified** new
source-first report, the legacy `mapped` projection is true if and only if the
row is `observed_http`; `observed_sdk_call` and every unverified row are never
mapped. Frozen v1 serializers keep their Node meaning only inside their
differential vectors and are never fed back into source-first readiness counts.

### 3.4 Optional OpenAPI adapter

When usable OpenAPI is supplied:

- validate and resolve it through a maintained Go OpenAPI library rather than a
  new hand-written parser;
- match normalized HTTP method/path templates after the source chain exists;
- preserve source-only evidence even when the document is partial, scraped,
  invalid for an unrelated operation, or lacks the path;
- report source/OpenAPI agreement, absence, conflict, and ambiguity separately;
  and
- retain current Node output bytes for the frozen OpenAPI-backed differential
  corpus until the authority handoff.

`kin-openapi` is the preferred candidate, subject to current Artifactory
availability/version verification and a compatibility probe against the
retained JSON and YAML fixtures. Library validation must not turn an optional,
partial specification into a blocker for source-only evidence.
External `$ref` resolution and network/file fetching are disabled inside the
adapter; all inputs are explicit local files prepared by the caller.

### 3.5 Command and artifact modes

The new source-first contract uses a versioned bundle mode; it does not invent
sidecar paths around the current CLI one file at a time.

For `source-operation-map`, `--artifact-dir DIR` selects v2 bundle mode and is
required for source-first output. It is mutually exclusive with the legacy
`--out`, `--diagnostics`, and `--source-facts-compare` destinations. Success
prints no report JSON to stdout. The command stages and publishes one
all-or-nothing directory containing `source-registry.json`,
`source-diagnostics.json`, `summary.json`, `summary.md`,
`input-provenance.json`, and `openapi-diagnostics.json`, plus the optional
OpenAPI map/comparison files defined in §3.6. Replacing that complete set also
removes stale optional artifacts from a prior run. Warnings use stderr.
Passing either `--source-manifest` or `--allow-unverified-source` selects v2
mode and therefore also requires `--artifact-dir`; omitting it is a usage error
(exit 2).

For `source-evidence-eval`, the existing `--out-dir` is the bundle directory
when a source manifest or `--allow-unverified-source` selects v2 mode.
`provider-probe` uses its recipe output directory and the same complete-set
transaction. Both publish the same named core artifacts plus their documented
evaluation/probe-specific files.

The current `source-operation-map` `--out`/`--diagnostics`/stdout behavior and
current OpenAPI-backed `source-evidence-eval` artifact set remain a distinct
legacy compatibility mode solely for the frozen differential vectors. That
mode requires the current OpenAPI input, emits no new sidecars, preserves exact
Node bytes and exits, and is categorically ineligible for v2 readiness or
handoff evidence. Mixing legacy destinations with v2 bundle flags is a usage
error (exit 2).

### 3.6 Optional OpenAPI failure isolation

Source analysis completes before the optional adapter. Adapter state cannot
change a source-derived classification, provenance chain, or source aggregate.
The report records exactly one report-level document state:

| Document state | Contract |
|---|---|
| `absent` | No OpenAPI input was selected; ordinary source-only mode. |
| `usable` | The explicit local document and the local refs required for comparison are valid. A missing operation is `missing_path`, not adapter failure. |
| `degraded` | An unrelated operation is invalid, but the exact local portion needed for comparison is resolvable. The defect and its scope are reported. |
| `unavailable` | The input is unreadable, malformed, has an invalid root shape, or the required local ref cannot be resolved. Source results remain valid and a stable reason is recorded. |

Separately, every selected resource has exactly one comparison state:

| Comparison state | Contract |
|---|---|
| `not_attempted` | Document state is `absent` or `unavailable`; no comparison ran. |
| `not_comparable` | Document is `usable` or `degraded`, but the source row is not `observed_http`. |
| `corroborated` | One eligible source endpoint agrees with one OpenAPI operation. |
| `missing_path` | The usable/degraded document has no operation for the eligible source endpoint. |
| `ambiguous` | More than one OpenAPI operation remains viable for the eligible source endpoint. |
| `conflict` | A trusted shared identity or explicit binding positively asserts a different method/path. |

A failed name/token heuristic is missing or ambiguous, never conflict. If the
document state is `absent` or `unavailable`, `not_attempted_total` equals
`selected_total` and all other comparison totals are zero. If it is `usable` or
`degraded`, `comparison_eligible_total` equals `observed_http`; the four
eligible outcomes sum to that total, `not_comparable_total` equals
`selected_total - comparison_eligible_total`, and `not_attempted_total` is
zero. All six per-resource comparison counts always sum to `selected_total`,
including when it is zero. `degraded_comparison_total` equals
`comparison_eligible_total` only for document state `degraded`, otherwise zero;
it is an annotation, not a seventh outcome. No document or comparison state
changes the source classification or source counts.

In the v2 bundle modes from §3.5, `source-operation-map`,
`source-evidence-eval`, and `provider-probe` always emit their complete source
artifact set when source analysis succeeds. Absent, partial, degraded, or
unavailable OpenAPI never suppresses those artifacts. An
unavailable explicitly supplied document emits one deterministic warning and
`openapi-diagnostics.json`, omits all OpenAPI-map/comparison artifacts, removes
any stale optional artifacts during atomic publication, and exits zero by
default because the adapter is optional. A future strict-OpenAPI flag may exit
nonzero only after publishing the same source artifacts and diagnostics.
Usable or degraded input emits the optional map/comparison set atomically;
source/OpenAPI conflict is reported and exits nonzero under the existing
fail-on-regression gate, otherwise zero. Existing frozen OpenAPI-backed vectors
retain their exact Node exit/output behavior.

The standalone `openapi-map` command remains strict rather than becoming a
source fallback. `--openapi` is required. Unreadable, malformed, invalid, or
unresolvable-local-ref input emits one deterministic error, exits 1, writes no
stdout JSON, and does not replace `--out`. A usable partial document emits its
map with missing-coverage diagnostics and exits zero. CLI usage errors remain
exit 2.

## 4. Provider probe v2

A qualified probe always requires the shared §3.2.1 manifest binding its
Terraform provider/schema and provider source.
SDK source is optional but required for a provider-to-SDK-to-HTTP claim when the
provider does not issue the HTTP request directly. OpenAPI is optional.

On successful source analysis the probe atomically publishes this core set:

- source-derived registry;
- source diagnostics, including ambiguity/unresolved reasons;
- summary JSON and Markdown;
- complete input provenance and hashes; and
- `openapi-diagnostics.json`, including when its state is `absent`.

With usable or degraded OpenAPI it atomically publishes an additional OpenAPI
map and explicit comparison set. With unavailable OpenAPI it publishes only the
core set and guarantees no stale optional artifact survives. Absence of OpenAPI
is ordinary source-only mode, not an error and not an implied coverage failure.
Existing OpenAPI-backed GitHub and DigitalOcean recipes remain differential
cases; a source-only Zscaler fixture qualifies the new path without credentials
or network access.

## 5. Implementation parcels

Each parcel stops for fresh adversarial review. The source mapper and precedence
parcels require two independent fresh reviews because they can silently remap or
overclaim provider evidence.

### A0 — Contract and corpus foundation (solo)

- Freeze the current six retained Node command cases and their bundle SHA.
- Record `zpa-provider-evidence` retirement; remove it from the Go handoff
  command count without deleting its matrix or historical Node implementation
  before the final freeze.
- Define the versioned source-first evidence/report schemas and exact
  classification/count vocabulary, including the normative partition and
  legacy projection in §3.3 and the comparison partition in §3.6.
- Define and validate the shared §3.2.1 source-provenance manifest for all three
  source-first commands, including verified and explicit-unverified trust
  behavior. Add fixture manifests that bind provider source, SDK source,
  schema, optional OpenAPI, and expected evidence independently of the
  candidate generator.
- Port the ZPA matrix's exact static source-binding checks into one generic
  corpus gate; preserve the matrix bytes and reject stale provider revision,
  tag, dirty tracked source, whole-file/range, pack/schema/registry/override,
  ordering, curated-claim/anchor, and runtime-label inputs without exposing a
  ZPA-specific CLI.
- Add a distinct endpoint-evidence fixture pinned to provider and SDK
  repository/module identities and source hashes. It alone qualifies analyzer
  endpoint classifications; curated matrix semantics are not promoted.

The source-only v2 schema has no Node byte authority. Existing OpenAPI-backed
arguments and artifacts remain differential-gated against Node; new source-only
goldens are hand-authored or independently reviewed from pinned source inputs
and require two fresh adversarial reviews. Candidate-generated expectations are
not acceptable evidence.

### A1 — Provider and SDK AST engine (solo foundation)

- Move/absorb `tools/source-evidence-ast` into a reusable Go authoring package.
- Root provider evidence at the resource's Read callback and traverse only
  statically reachable same-package helpers; Create/Delete-only calls found in
  an associated file cannot support a Read claim.
- Add SDK package-function resolution plus bounded path extraction. Receiver
  methods are linked only where their construction/type is statically proven;
  otherwise they remain explicit unlinked calls.
- Emit Read-rooted provider-to-SDK-to-HTTP evidence independently of OpenAPI.
  This is source-operation tracing, not an AST-derived provider-to-SDK mapping
  guessed from an OpenAPI document. OpenAPI remains optional corroboration;
  that distinction matters for Zscaler, where no complete published OpenAPI
  authority exists.
- Treat a raw HTTP endpoint as recovered only when the reached request-builder
  declaration directly proves an exact `net/http.NewRequest` or
  `net/http.NewRequestWithContext` sink with the method/path formals in the
  required positions. A name or signature such as `NewRequest` or
  `NewRequestDo` is not evidence. Zscaler's
  `NewRequestDo` → `ExecuteRequest` → `buildRequest` wrapper is therefore an
  `observed_sdk_call` / `endpoint_not_recovered` result in A1, pending a
  separately reviewed bounded wrapper/dataflow qualification.
- Qualify against synthetic patterns and the frozen ZPA corpus without claiming
  automatic proof of its hand-curated semantic labels.
- Keep A1 as a reusable qualified-input package foundation. CLI wiring and
  explicit unverified-mode behavior belong to A2; A1 does not mint
  qualification from ad-hoc roots.

### A2 — Source operation and evaluation (after A1)

- Port `source-operation-map` around the source-first engine.
- Port `source-evidence-eval` classification and Markdown.
- Preserve frozen OpenAPI-backed Node behavior where inputs overlap; add the
  new source-only v2 corpus and fail-closed ambiguity/count gates.

### A3 — Reconcile and optional OpenAPI adapter (parallel with A2 after A0)

- Port the API/schema/override reconciliation core.
- Integrate the reviewed OpenAPI library for optional augmentation and the
  standalone `openapi-map` command.
- Preserve raw/source evidence precedence and exact retained report bytes.

### A4 — Provider probe orchestration (after A2 and A3)

- Port recipe loading, pinned local/download preparation, Terraform schema
  capture, artifact publication, and summary rendering.
- Add source-only recipes; keep network disabled in tests.
- Retain private temporary directories, bounded subprocess output, redaction,
  and atomic complete-set publication.

### A5 — Transform/Adopt parity (parallel with A1–A4)

- Port fixture validation, structural differences, replay, report generation,
  and CLI behavior using the existing Go Transform and Adopt kernels.
- Keep it package-disjoint from provider-readiness evidence.

### A6 — CLI, Make, and handoff gate (last)

- Wire the six retained commands and their argument/env/exit/output contracts.
- Assert all six names appear in the single `iw` help surface and execute
  through that binary with no Node path or second authoring executable.
- Route authoring Make targets to Go.
- Remove `zpa-provider-evidence` from active help/Make/release routing while
  preserving the frozen Node v1 oracle and matrix history.
- Run the final full authoring differential corpus, Node-free command smoke, and
  authority-handoff ceremony before degrouping begins.

## 6. Acceptance bar

- Existing Node differentials remain byte-identical for overlapping command
  inputs, outputs, reports, artifacts, diagnostics, and exit codes.
- Source-only mapping works with no OpenAPI file and emits deterministic,
  provenance-bound evidence.
- Direct provider REST, SDK receiver methods, SDK top-level package functions,
  Read-callback helper chains and cycles, Create/Delete-only helper calls,
  same-named methods across packages, wrapper requests, multiple candidates,
  dynamic paths, missing SDK source, partial scraped OpenAPI, and
  source/OpenAPI conflict are all covered.
- Only `observed_http` counts as an endpoint or as the source-first legacy
  `mapped` projection. Every classification transition is exhaustively tested
  against §3.3 totals and denominator. Only manifest-verified rows can enter
  readiness or the projection; explicit unverified runs are rejected there.
- Absent, malformed, unreadable, invalid-local-ref, partial, degraded,
  missing-path, ambiguous, and conflicting OpenAPI fixtures—including zero
  selected resources—prove the §3.6 document/comparison sums, degraded count,
  artifact, diagnostic, and exit contracts.
- `source-operation-map` bundle tests exercise source-only, usable, and
  unavailable OpenAPI over a pre-existing artifact directory, proving the
  §3.5 destination/stdout rules, complete-set atomicity, and stale-artifact
  removal while the legacy vectors remain byte-identical.
- The frozen ZPA matrix remains byte-unchanged and passes its independent exact
  static source-binding gate. A separate provider+SDK-pinned endpoint fixture
  qualifies the analyzer; curated semantic claims remain labeled as curated.
- Mutation tests independently reject wrong provider revision/tag, dirty or
  changed provider files/ranges, pack/schema/registry/override drift, SDK root
  or module identity/tree drift, and endpoint method/path/call-site drift.
- Each source-first command rejects a post-manifest schema, provider revision,
  bound provider file, SDK module, or SDK tree mutation without network access;
  explicit unverified mode remains visibly ineligible for qualification.
- Provider probe fixture tests have no network, credentials, live provider, or
  live tenant access. Terraform, Git, and downloader seams are injected or
  fixture-local.
- `gofmt`, `go vet`, full Go tests, race tests for any concurrent orchestration,
  module dependency audit, and the surviving frozen-Node differentials pass.
- No authoring artifact is produced through `canonjson`/`tfrender` replacement;
  authoring report rendering remains explicit and byte-gated where parity is
  retained.

## 7. Deferrals and non-goals

- Do not infer importer grammar, Terraform identity rebinding, sensitivity, or
  flatten/expand semantics merely because an SDK endpoint was resolved.
- Do not synthesize or scrape a new OpenAPI specification as part of the port.
- Do not contact a provider tenant or run live Apply.
- Do not add general whole-program analysis if a bounded source-root analyzer
  covers the retained corpus; unresolved is safer than speculative reachability.
- Do not start singleton-state topology work until A6 completes the formal
  authority handoff.
