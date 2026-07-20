# Go runtime v2 — scope reset

Status: authoritative plan as of 2026-07-18. Where it conflicts with
[go-runtime-plan.md](go-runtime-plan.md), this v2 plan controls the wire/IO
contract, runtime-versus-maintainer command scope, CI and cutover strategy, and
Node archive timing. The old plan remains useful implementation inventory, but
its full-command port, flag-day cutover, no-Node-CI, and immediate-archive
requirements are superseded. The artifact layer's byte-exactness and the
differential-oracle method are retained unchanged (see §2).

## 1. The miss, stated plainly

"Byte-compatible with the Node `iw` CLI" was adopted as a blanket contract. For
generated infrastructure it is exactly right. For the HTTP client it silently
promoted every Node/Undici implementation accident — CONNECT wire framing,
tough-cookie's RFC 6761 handling, WHATWG URL edge cases, Node's bundled root
certificate inventory, malformed-header acceptance, Node's filesystem-error
wording — into permanent, hand-maintained requirements. The result: a
7,136-line `internal/resthttp` package (the largest in the repo), ~6,000 lines
of which reproduce library internals no operator's infrastructure depends on.

The correct test was never "does Node do this?" It is: **would changing this
alter infrastructure, evidence, automation, or an operator decision?** If not,
compatibility is not required. This document re-scopes around that test.

## 2. The compatibility contract (product, not Node)

| Preserve **exactly** (byte-level, differential-gated) | Allowed to change (Go-native) |
|---|---|
| Generated Terraform/config bytes: tfvars(.json/hcl), imports, moved blocks, lookup sidecars, modules, env roots, root catalog | Node/undici raw wire serialization |
| Canonical JSON rendering (sorted keys, ASCII escaping, **float repr into artifacts**, lossless ints, indent) | tough-cookie / cookie-jar internals |
| **Number rendering into artifacts** (`1e-06`, `-0.0` forms) — a tfvars value drift is a plan diff | WHATWG URL edge / malformed-URL behavior |
| Unicode-15.1 lowercase + html-unescape **where they feed artifact keys/values** | Node bundled-root certificate inventory |
| Plan fingerprints, `sources_sha256` framing, report schemas, safety classifications | Malformed-header acceptance |
| `complete`-field fail-closed gate, plan/adopt/apply safety invariants | Node filesystem-error **wording** |
| Command names, exit-code meanings, deterministic resource selection/ordering | Exact retry **timing**; diagnostic-only number/float formatting |
| Authentication masking, secret redaction; pagination, retry **limits**, response **bounds** | Node-specific error strings; internal syscall sequences |

Correction to the initial reset proposal: "floating-point formatting" is **not**
uniformly changeable. Number rendering *into artifacts* stays exact (it is
infrastructure bytes); number formatting in *logs/diagnostics* may drift. Same
for the Unicode/html-unescape emulation — keep the parts that reach committed
artifact bytes, drop the parts that only ever touched wire/error text.

The differential-oracle **method** is retained: the Node CLI remains the oracle
for the artifact layer, and the existing byte-gate corpus (root-catalog,
transform demo tree, gen-env, modules, topology) stays green. What changes is
that the wire/IO layer is validated at the **behavioral/product** level
(controlled servers, recorded provider responses, product-output equivalence),
not by reproducing bytes.

### 2.1 Dependency boundary — consume/orchestrate with libraries; render exactly

Libraries for **CONSUMING and ORCHESTRATING** — `terraform-exec` (running
Terraform), `terraform-json` (decoding Terraform plan/state), `jsonschema`
(schema validation), `zscaler-sdk-go` (provider auth/transport) — because no
operator-visible bytes are produced, so no byte-parity requirement. **HAND-ROLL**
only where the output is committed artifact bytes that must match existing
state: the canonical JSON renderer (`canonjson`) and HCL generation (`tfrender`).
`hclwrite` AST construction does not replace `tfrender` because its emitted
bytes differ from committed goldens. Token-only `hclwrite.Format` may normalize
source already emitted by `tfrender` when a fail-closed parse precedes it and
the complete artifact corpus remains byte-identical to Terraform formatting.
Raw provider readback for evidence stays raw (`zscaler-sdk-go` used for
auth/transport, **NOT** for normalizing evidence bytes).

This rule supersedes the original plan's blanket deferral of these libraries
"past parity." Library decoding and orchestration do not replace Infrawright's
fail-closed product gates: typed Terraform values are inputs to the explicit
`complete === true` and adopt/apply checks, and schema-library errors must pass
through the project's deterministic report adapter anywhere their content
reaches operator-visible bytes. A small dependency footprint is an outcome,
not an architectural goal.

Two completed implementations stay sunk: the existing `terraformcmd`
invocation path and `canonjson` strict decoder are working and low-ROI to
replace. Reconsider `terraformcmd` only if authorized Block D work using
`terraform-exec` makes one unified invocation path clearly worth the migration;
that is an open design decision, not work authorized by this plan correction.

## 3. Keep / Rewrite / Drop inventory (cleanup complete)

**KEEP — the genuinely valuable Go work (byte-exact where it matters):**

| Package | LOC | Role |
|---|---:|---|
| `canonjson` | 1,866 | canonical JSON + Python-number repr — artifact bytes. Crown jewel. |
| `metadata` | 4,302 | loader/packs/resources/terraform-schema/root-catalog |
| `transform` + `tfrender` + `transformrun` | 6,959 | transform kernel + artifact rendering + batch runner |
| `envgen` + `modulesgen` | 4,456 | env-root + module generation |
| `roots` | 1,805 | topology / scope-paths / plan-roots |
| `collectors` | 3,154 | **fetch engine** — pagination, retry policy, masking, failure hints, adapters. Sits on the `HttpTransport` seam; keep the engine, swap what's under the seam. |
| `artifacts` | 1,985 | bounded-files/snapshots — safety (TOCTOU, `os.Root` jail). Keep. |
| `procerr` `deployment` `pypath` | 1,076 | retained command/runtime support; the byte-parity-era `cliargs` parser was retired in A7 in favor of Cobra |
| `pyunicode` | 670 | **audit**: keep lower-15.1 + html-unescape where they feed artifact keys/values; drop any part only reaching wire/error text |

Cobra owns command parsing, command discovery, help, shell completion, and the
generated CLI reference. Those are operator-orchestration surfaces, not
committed infrastructure/evidence bytes, so their presentation follows Cobra
rather than the frozen Node `parseArgs` and static-usage wording. Domain output,
artifact/report bytes, environment precedence, exit classifications, and every
adopt/Apply safety gate remain separately qualified contracts.

**REWRITE — around Go-native infrastructure:**

| Target | From | To |
|---|---|---|
| Fetch transport | `resthttp` (7,136 production LOC; 14,498 incl. tests/data) | `internal/httptransport` over `net/http`: standard TLS/URL/proxy, explicit `REQUESTS_CA_BUNDLE`/`SSL_CERT_FILE`, bounded responses, timeouts, provider auth, secret-safe diagnostics. Satisfies the existing `collectors.HttpTransport` seam. **Landed: 821 production LOC.** |
| `terraformcmd` path/error surface | 2,076 (incl. `unicode_lower.go`, PATHEXT WHATWG resolution, Node error-string emulation) | Keep process isolation, timeouts, bounded output, redaction, the `complete` gate. Drop exact Node path-resolution + error-wording emulation. |

**DROP — compatibility emulation with no product justification:**

| Package/file | LOC | Why |
|---|---:|---|
| `resthttp/{node_roots_data,node_roots,publicsuffix_data,generate_*}.go` | ~2,972 | Node cert bundle + tldts trie. `net/http` uses the system trust store + explicit CA bundle. |
| `resthttp/special_url.go` | 780 | WHATWG URL parser. `net/url` suffices. |
| `resthttp/cookies.go` | 767 | tough-cookie emulation. `net/http/cookiejar` if a jar is needed at all (legacy ZIA auth — verify it's actually required). |
| `resthttp/{response_wire,production_wire}.go` | 1,121 | raw HTTP/1.1 wire. `net/http` owns this. |
| `nodefserr` + its 5 `*filesystem*_differential_test.go` files | 517 + tests | Filesystem-error **wording** is explicitly allowed to change. Revert adoption sites to plain Go errors. |
| `pyoserr` | 31 | `[Errno 2]` Python spelling for missing terraform binary — collapse to a plain Go error. |
| vendored `golang.org/x/net` + `x/text` | — | Only pulled in by `special_url.go`'s idna. Removing the WHATWG parser returns the module to **zero third-party deps**. |

Actual cleanup result: approximately **20,000 lines of hand-written emulation
and its tests removed** across the three waves, with an 821-LOC production
transport in their place. The transport wave separately removed **37,324 lines of generated
vendored `x/net`/`x/text` source**. The module is stdlib-only. Nothing in the
KEEP column changed artifact behavior; all four artifact byte-gates remained
byte-identical.

## 4. Command scope: one Go authority

The single released `iw` binary carries the complete retained command surface.
Its operator/pipeline commands are:

`fetch · fetch-diag · transform · adopt · gen-env · modules · stage-imports · unstage-imports
· roots · scope-paths · plan-roots · plan · clean-plans · assert-clean ·
assert-adoptable · apply · resources · deployment · check-pack · check-pack-set
· root-catalog`

Six authoring commands are also part of that same `iw` binary before the
authority handoff:
`reconcile · openapi-map · source-operation-map · source-evidence-eval ·
provider-probe · transform-adopt-parity`. Their controlling design is
[go-authoring-port-roadmap.md](go-authoring-port-roadmap.md): provider and SDK
source produce the primary HTTP-path evidence, while OpenAPI is optional
corroboration and cannot outrank source.

`zpa-provider-evidence` does not become a seventh Go command. Its pinned v4.4.6
matrix and binding checks become a frozen qualification corpus for the generic
source analyzer. `audit-vendor-boundary` is a repository verification
obligation rather than a released CLI surface; any still-required import/token
boundary is implemented as a Go/CI check before Node archive.

Existing Node-backed command behavior remains the differential authority until
handoff. New source-only behavior has no Node equivalent and therefore uses an
independently reviewed, source-bound golden corpus. At full archive Node is
neither a runtime nor CI execution dependency; its final bundle digest and
historical fixtures remain provenance only.

The A6 handoff gate asserts that all six authoring names appear in `iw` help,
route through that binary, and have no executable Node fallback. There is no
second authoring binary and no post-handoff Node command lane.

The A6 implementation candidate now satisfies the local six-name routing,
frozen differential, artifact-publication, and Node-free command gates and has
passed fresh adversarial review. This is not yet the formal authority transfer:
the final Node freeze/tag and product-authority declaration remain pending the
external Opus, GPT-5.6 Pro, and Fable review sequence.

## 5. Vertical-slice checkpoint (go/no-go before any more breadth)

The checkpoint is deliberately one resource wide:

```
zia_rule_labels: local TLS fetch → transform → module/env generation
                → terraform init/validate/test (mock provider)
```

It does not authorize Adopt, import-oracle/staging, plan-lifecycle, or Apply
work. The required acceptance matrix is:

| Leg | Objective pass condition | Evidence | Status |
|---|---|---|---|
| Cleanup prerequisite | `863f405` is the candidate base; module is stdlib-only; RootCatalog, Transform, Topology, and Generation byte-gates are identical to an accepted Node oracle. | Commit SHA, accepted Node bundle/checksum, and four gate transcripts. | **PASS locally — oracle rebuilt reproducibly from the accepted remote source lineage; all four gates passed at `863f405` with no skips** |
| Hermetic product chain | The opt-in `TestV2VerticalSliceCheckpoint` in `go/cmd/iw/v2_vertical_slice_test.go` uses a temporary-CA local TLS server and accepts only legacy-ZIA auth followed by `GET /api/v1/ruleLabels?page=1&pageSize=1000`. It uses a temporary ZIA-only pack root containing the complete production `packs/zia` and `packs/_shared/zscaler` trees with `packsets/zia.json`. Go `fetch` must equal `packs/_shared/zscaler/demo/zia_rule_labels.json`; Go `transform` must equal committed `demo/config/demo/zia_rule_labels.auto.tfvars.json` and `demo/imports/demo/zia_rule_labels_imports.tf`; the four core module files must equal committed `tests/fixtures/gen/zia_rule_labels`; the complete generated overlay must match an exact 12-file manifest. No unexpected request or output is allowed, including after Terraform. | Visible `INFRAWRIGHT_V2_CHECKPOINT=1` transcript, full-tree/manifest checks, and exact byte comparisons; all writes stay in mode-restricted temporary roots while pack authorities are referenced through repository symlinks. | **PASS locally; focused adversarial review approved** |
| Terraform composition | In the generated env root, bounded `terraform init -backend=false -input=false -no-color`, `terraform validate -no-color`, and `terraform test -no-color -verbose -json` all exit 0. The structured events must prove `empty_plan` has zero resource changes and `config_plan` has exactly one `create` at `module.zia_rule_labels.zia_rule_labels.this["testlabel_vcr_integration"]` with the committed name/description, followed by an exact 2-pass/0-failure summary. The ZIA pack pin must equal the sole generated provider-lock selection. Provider installation may use the registry or a pinned filesystem mirror; all ZIA/Zscaler credentials/endpoints are removed before Terraform and no post-fetch API request is allowed. | Sanitized transcript with candidate hash, Terraform version, signed provider selection, lock hash, validation result, per-run structured plan summary, and final HTTP transcript. | **PASS locally; focused adversarial review approved** |
| No-Node candidate | Build a CGO-disabled candidate in a temporary package root, then run the entire hermetic chain with a sanitized `PATH` containing Terraform but no usable `node`, `npm`, or `npx`; invoke only the candidate and Terraform. Any attempted name-based Node execution fails the leg. | Sanitized environment manifest and the same product/Terraform transcript. | **PASS locally; integrated into the hermetic test** |
| One live read-only provider | Exactly `zia_end_user_notification` through ZIA OneAPI, concurrency 1: accepted Node → Go candidate → accepted Node, each into a fresh mode-0700 root. This singleton performs one `GET /zia/api/v1/eun` with no pagination. All three runs exit 0 and emit only `zia_end_user_notification.json`; Node-before equals Node-after and Go bytes equal both. No Adopt, import, plan, Apply, selector widening, retry/429, or mutation is permitted. Credentials and raw pulls remain private and are deleted. | Candidate and Node SHAs; tool/provider/pack versions; exit statuses; item count, byte size, SHA-256/tree manifest; masked diagnostic classification and secret-scan result. | **PASS by user-accepted external field evidence on 2026-07-18; the external transcript/manifests, credentials, and private raw pulls are not stored in this checkout** |
| Fresh adversarial review | A fresh Codex reviewer follows `docs/adversarial-review.md`, reviews the complete checkpoint evidence without editing, and leaves no unresolved blocking finding. | Review handoff and recorded findings using the repository templates. | **PASS for the hermetic and post-247 candidates; the user accepted the external live read-path evidence and explicitly authorized Block D at `b6f6e66` on 2026-07-18** |

Run the hermetic leg explicitly; the default test lane records a visible skip
so provider installation is never smuggled into ordinary unit tests:

```sh
cd go
INFRAWRIGHT_V2_CHECKPOINT=1 \
  go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v
```

Original pre-247 non-live evidence recorded on 2026-07-17:

- The user-designated remote source of truth is
  `c86b8cafe68493705d4a9130f2a21e6dd05245c7`. Its `node-src`, `node-tests`,
  package manifests, build script, and TypeScript configurations have the same
  Git object IDs as candidate base `863f405`; only the intervening Go cleanup
  and its Makefile gates differ. `npm ci` reported 29 installed packages and
  zero vulnerabilities.
- Two independent `npm run build:metadata-cli` runs under Node 24.15.0/npm
  11.12.1 produced the same 3,036,391-byte bundle:
  `b17960a361d1be929abaa37e18312b67cf18f6c291b6e5400f75acd6be1cd065`.
  The checksum file's SHA-256 is
  `43dfe6fa352bdfa566de8446349a583ec5b1a867fcba7cc18def7f4055517cba`;
  `verify-runtime-release.mjs` passed all 11 profiles.
- A fresh `go test -count=1 -v ./cmd/iw -run` invocation selecting exactly the
  RootCatalog, Transform, Topology, and Generation differential tests passed
  all four gates with no skips in 12.050 seconds.
- The opt-in hermetic checkpoint passed in 7.99 seconds. The candidate SHA-256
  was `cf7eb76529ed877c0a92540046956b43e7426aa80b4ce1cf8f74fe74b955315b`;
  Terraform 1.15.4 installed signed `zscaler/zia` 4.7.26, validation passed,
  `empty_plan` had zero changes, and `config_plan` had exactly the expected
  create. The provider lock SHA-256 was
  `5e2d47060a6a1e562a8cdf923cc60035b41700e1b3474943b6b2dff2ce9abb21`.
- `go test -count=1 ./...`, `npm run check:all`, `make check-all`, `gofmt`, and
  `go vet ./...` all passed. The Node lane reported 785 passes, zero failures,
  and two explicitly optional external checks skipped; neither skip is part of
  this checkpoint's required non-live evidence.

Post-247 reconciliation evidence recorded on 2026-07-17:

- `origin/main` differed from the qualified feature branch only by merged PR
  247. Merge commit `821e9b4` brings that reviewed Node authority into the
  branch without rewriting the original work-machine-tested history.
- Candidate `5e7d02d1ce700dd01d54759f040dd1c4cc6e2cc1` ports the two PR 247
  behaviors with existing Go kernels: the Terraform environment count bound
  rises from 256 to 4,096 while the 256-KiB byte bound remains, and drift-policy
  validation accepts exact lossless numeric values while rejecting equivalent
  duplicate numeric scopes. The pack/user policy merge remains explicitly
  deferred to the still-unauthorized Go Adopt block.
- Two independent Node 24.15.0 builds produced the same post-247 bundle:
  `fd4593c300cde3e8e0ef43153ef4c741b4c542be9165770bbe339d66385c7b2a`.
  The checksum-file SHA-256 is
  `df3709d7ab96761792ee6557d12c315351db83ee69fbf78bc0bed79a9ac45946`;
  the runtime verifier again passed all 11 profiles.
- RootCatalog, Transform, Topology, and Generation passed against the refreshed
  oracle with no skips in 12.163 seconds. The focused PR 247 Node files passed
  55 tests with no skips or failures; the Go environment, exact-version,
  wide-number, signed-zero, scalar-separation, and duplicate-scope regressions
  passed.
- The hermetic checkpoint passed in 9.13 seconds. Candidate binary SHA-256:
  `419e966397a15fe4b4df9240fde8b021ffcb0e865e039258f13f779a1553d4f4`;
  provider lock SHA-256:
  `5e2d47060a6a1e562a8cdf923cc60035b41700e1b3474943b6b2dff2ce9abb21`.
  Terraform validation and both exact plan assertions passed.
- Full Go, `npm run check:all`, and `make check-all` passed. The expanded Node
  lane reported 788 passes, zero failures, and two known optional external
  skips. gofmt, `go vet ./...`, and the pre-review script passed; its optional
  `golangci-lint` step was unavailable.
- Fresh adversarial review of `5e7d02d` returned **Approve** after independently
  reproducing the oracle and gates. The reviewer confirmed Go/Node marker text
  differences are inert because the marker is confined to same-run duplicate
  detection. Forward watch-item: when block C adds plan-report behavior,
  re-confirm that internal marker bytes do not enter reports.
- The credential-free work-machine report was accepted by the user together
  with the focused PR 247 reconciliation and adversarial review. That closes
  the non-live rerun question; it does not supply or waive the still-missing
  live-provider evidence.

UTF-16 contract reconciliation evidence recorded on 2026-07-20:

- Node and Go now reject unpaired UTF-16 surrogate units in every decoded JSON
  key and value with the same reason and raw UTF-16 source offset. Valid pairs,
  literal astral text, U+FFFD, ordinary malformed-JSON priority, numeric
  handling, and valid artifact bytes remain unchanged.
- Two consecutive Node 24.15.0 builds produced the same 3,040,955-byte bundle:
  `ce48c2c6a1cc01254866c5a7eb98b3eef1c90e6c45b69aff7df7aed80c822fa2`.
  The checksum-file SHA-256 is
  `b955f56a128a590f7811472959ce580cb344ed4fe400906377e6a2e30263f63e`.
  The active authoring-authority manifest SHA-256 is
  `c9485be8b0c7a805247d54250c700c562ba8f32fa60f9e35ceb1b6c6e6671612`.
- The complete Node lane passed 793 tests with zero failures and two known
  optional external skips. The complete Go suite, vet, A6/D5 frozen-oracle
  lanes, and RootCatalog, Transform, Topology, and Generation byte gates
  passed against the rebuilt authority.

The checkpoint passes only when every required leg is PASS. A skip,
inconclusive live snapshot, Node self-drift, secret-scan hit, unexpected wire or
filesystem behavior, artifact mismatch, or undocumented §2 difference is a
failure—not a waiver. Passing must answer yes from recorded evidence: the Go
path stayed stdlib-based and materially smaller; infrastructure bytes are
equivalent; every wire difference is within §2 and documented; and the no-Node
operator path works end to end.

On 2026-07-18 the user accepted the external live read-path evidence and
explicitly authorized Block D (Adopt/import/oracle/staging/exact saved-plan
Apply) at `b6f6e66`. That authorization covered implementation and fixture/
local-fake differential proof only. It did **not** authorize a Terraform Apply
against real provider state or a live tenant; controlled live Apply remains a
separate human-gated qualification event.

## 6. Gradual cutover — Node is not archived on day one

1. Node remains the current release + rollback path.
2. Go runs in semantic-comparison mode against the artifact oracle.
3. Qualify read-only provider ops (keyed, read-only first).
4. Qualify Terraform planning without Apply.
5. Release Go as an **opt-in** candidate binary.
6. Default to Go after real use.
7. Retain frozen Node for at least one stabilization period.
8. Archive Node only after Go has proven itself in operation.

The `INFRAWRIGHT_CLI` Make variable is the cutover seam; it flips per-stage, not
flag-day.

## 7. Cleanup execution

Each a reviewed commit on `feature/go-canonjson-foundation`.

1. ✅ **Wire rewrite (`6a4f8ae`)**: `internal/httptransport` (821 LOC) over
   `net/http` satisfies `collectors.HttpTransport`; `resthttp` deleted (−14,498
   LOC incl. tests); `x/net`/`x/text`/`go/vendor`/check-vendor gate all removed;
   module back to zero third-party deps. Behavioral test corpus preserved
   (CA-bundle-load failure, proxy-from-env, redirect cap + cross-origin
   stripping + 307/308 refusal, response bound, masking, close, legacy-ZIA
   cookie jar). Two documented behavior changes (both improvements): lazy proxy
   resolution (fetch-diag degrades to a masked FAIL instead of crashing);
   `Location: //` empty-authority edge dropped. Artifact byte-gates confirmed
   byte-identical vs the Node oracle.
2. ✅ **Simplify `terraformcmd` + drop `pyoserr` (`8571d8f`)**: kept isolation,
   process-group kill, timeouts, bounded output, redaction, platform gate,
   executable precedence, the downstream `complete` gate, and all operator error
   codes; dropped the Node path-resolution + PATHEXT unicode-lowering emulation
   (`unicode_lower.go`) and the `[Errno 2]` spelling. Net −1,006 LOC.
3. ✅ **Drop `nodefserr` (`863f405`)**: reverted 72
   adoption sites across 10 files (metadata: packs.go, resources.go,
   rootcatalog.go, validation.go; tfrender/transform_artifacts.go incl. its
   batch-rollback path; envgen/environment_generator.go; modulesgen/
   generator.go; collectors/rest.go; cmd/iw/main.go,
   commands_topology.go) to plain Go errors; deleted `internal/nodefserr`
   (package + tests, 1,928 LOC), six filesystem differential/parity test files,
   and two oracle fixtures
   (`filesystem_cli_differential_test.go`,
   `rest_filesystem_parity_test.go`, three
   `filesystem_error_differential_test.go` copies,
   `transform_artifacts_fserr_test.go`, plus the
   `capture-node24-transform-filesystem-errors.mjs`/`.json` oracle fixtures);
   updated one surviving non-differential assertion in
   `collectors/rest_test.go` that still expected Node's `EISDIR` wording to
   the Go-native `*fs.PathError` text; removed the stale "Filesystem error
   text decision" divergence section from `go-runtime-plan.md`. Net **−4,939**
   LOC. `metadata.propagateFilesystemError`'s panic/recover routing and
   `tfrender`'s reverse-order rollback aggregation are untouched — only the
   wrapped error text was dropped. gofmt/vet/build/test all green; the four
   artifact byte-gates (RootCatalog, Transform, Topology, Generation)
   confirmed byte-identical vs the Node oracle.
4. ✅ **Audit `pyunicode`**: KEEP ENTIRELY. Both exports feed committed artifact
   bytes — `PythonLower151` → `SnakeName`/`SlugifyTransformKey` (transform), and
   the slug is the tfvars map key = the Terraform **state address** (a drift here
   is destroy/recreate); `PythonHTMLUnescapeGeneric` → the transform kernel's
   HTML-unescape of provider field **values**. Nothing to drop; this emulation is
   load-bearing, squarely in the §2 "preserve exactly" column.

After each cleanup commit, the full artifact byte-gate corpus (RootCatalog,
Transform, Topology, Generation vs the Node oracle) must stay byte-identical —
that is the standing proof the reset touched nothing infrastructure depends on.
§7 is complete at `863f405`. Block C completed at `3daaf07`: the saved-plan
lifecycle, bound evidence, assessment/report path, four command entry points,
and bounded direct Node/Go differential all landed under fresh adversarial
review. After the user accepted the external live read-path evidence, Block D
was separately authorized at `b6f6e66` and completed through `714302e` in five
reviewed parcels: bounded import Oracle, transactional import staging, adopt
orchestration and policy merge, exact saved-plan Apply, and CLI/frozen-oracle
differential wiring. `terraform-json v0.28.0` is the sole direct module
dependency; the existing bounded `terraformcmd` runner and byte-exact
`canonjson`/`tfrender` paths remain in place. The four standing artifact byte
gates remain identical. No Block D test used credentials, a provider API,
remote state, or a real/live Apply; that controlled qualification remains
explicitly unauthorized until a later human decision.
