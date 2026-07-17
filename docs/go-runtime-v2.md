# Go runtime v2 — scope reset

Status: authoritative plan as of 2026-07-17. Where it conflicts with
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
| `cliargs` `procerr` `deployment` `pypath` | 1,829 | structural |
| `pyunicode` | 670 | **audit**: keep lower-15.1 + html-unescape where they feed artifact keys/values; drop any part only reaching wire/error text |

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

## 4. Command scope: runtime binary vs retained Node tools

The Go binary carries only what an operator/pipeline runs:

`fetch · fetch-diag · transform · adopt · gen-env · modules · stage-imports · unstage-imports
· roots · scope-paths · plan-roots · plan · clean-plans · assert-clean ·
assert-adoptable · apply · resources · deployment · check-pack · check-pack-set
· root-catalog`

**Retained as Node repository/maintainer tools** (not ported, not a runtime
dependency): `reconcile · openapi-map · source-operation-map ·
source-evidence-eval · provider-probe · zpa-provider-evidence ·
transform-adopt-parity · audit-vendor-boundary`. These are
developer-facing authoring/readiness tools; Node stays a *development*
dependency for them. This removes the entire uncertain authoring-port block.

The product requirement is **"no Node required on the operator's machine,"** not
"no Node anywhere in CI." That relaxation alone deletes the make-check
Node-dependency blocker's root cause.

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
| One live read-only provider | Exactly `zia_end_user_notification` through ZIA OneAPI, concurrency 1: accepted Node → Go candidate → accepted Node, each into a fresh mode-0700 root. This singleton performs one `GET /zia/api/v1/eun` with no pagination. All three runs exit 0 and emit only `zia_end_user_notification.json`; Node-before equals Node-after and Go bytes equal both. No Adopt, import, plan, Apply, selector widening, retry/429, or mutation is permitted. Credentials and raw pulls remain private and are deleted. | Candidate and Node SHAs; tool/provider/pack versions; exit statuses; item count, byte size, SHA-256/tree manifest; masked diagnostic classification and secret-scan result. | **BLOCKED locally — externally confirmed read-only credentials are absent; no live call was attempted** |
| Fresh adversarial review | A fresh Codex reviewer follows `docs/adversarial-review.md`, reviews the complete checkpoint evidence without editing, and leaves no unresolved blocking finding. | Review handoff and recorded findings using the repository templates. | **Hermetic implementation: Approve; newly recorded oracle/qualification evidence is ready for fresh review; final full-evidence review remains required after the live leg** |

Run the hermetic leg explicitly; the default test lane records a visible skip
so provider installation is never smuggled into ordinary unit tests:

```sh
cd go
INFRAWRIGHT_V2_CHECKPOINT=1 \
  go test ./cmd/iw -run '^TestV2VerticalSliceCheckpoint$' -count=1 -v
```

Recorded non-live evidence on 2026-07-17:

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

The checkpoint passes only when every required leg is PASS. A skip,
inconclusive live snapshot, Node self-drift, secret-scan hit, unexpected wire or
filesystem behavior, artifact mismatch, or undocumented §2 difference is a
failure—not a waiver. Passing must answer yes from recorded evidence: the Go
path stayed stdlib-based and materially smaller; infrastructure bytes are
equivalent; every wire difference is within §2 and documented; and the no-Node
operator path works end to end.

Until all legs pass and the fresh adversarial review completes, blocks C/D
(Adopt/import/oracle and plan/apply breadth) are **not authorized**.

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
§7 is complete at `863f405`. The **§5 checkpoint is now the only authorized
implementation work**; blocks C/D remain closed under the v2 contract until
the complete matrix passes and receives fresh adversarial review.
