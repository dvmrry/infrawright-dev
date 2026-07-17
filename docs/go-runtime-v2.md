# Go runtime v2 — scope reset

Status: authoritative plan as of 2026-07-17. Supersedes the byte-compatibility
framing in [go-runtime-plan.md](go-runtime-plan.md) for the **wire/IO layer**.
The artifact layer's byte-exactness and the differential-oracle method are
retained unchanged (see §2).

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

## 3. Keep / Rewrite / Drop inventory

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
| Fetch transport | `resthttp` (7,136) | `internal/httptransport` over `net/http`: standard TLS/URL/proxy, explicit `REQUESTS_CA_BUNDLE`/`SSL_CERT_FILE`, bounded responses, timeouts, provider auth, secret-safe diagnostics. Satisfies the existing `collectors.HttpTransport` seam. **Target ~300–450 LOC.** |
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

Net effect: **~7.6k lines deleted, ~400 written.** The module returns to
stdlib-only. Nothing in the KEEP column changes behavior.

## 4. Command scope: runtime binary vs retained Node tools

The Go binary carries only what an operator/pipeline runs:

`fetch · transform · adopt · gen-env · modules · stage-imports · unstage-imports
· roots · scope-paths · plan-roots · plan · clean-plans · assert-clean ·
assert-adoptable · apply · resources · deployment · check-pack · check-pack-set
· root-catalog`

**Retained as Node repository/maintainer tools** (not ported, not a runtime
dependency): `reconcile · openapi-map · source-operation-map ·
source-evidence-eval · provider-probe · audit-vendor-boundary`. These are
developer-facing authoring/readiness tools; Node stays a *development*
dependency for them. This removes the entire uncertain authoring-port block.

The product requirement is **"no Node required on the operator's machine,"** not
"no Node anywhere in CI." That relaxation alone deletes the make-check
Node-dependency blocker's root cause.

## 5. Vertical-slice checkpoint (go/no-go before any more breadth)

Before further porting, prove one complete flow end to end:

```
fetch → transform → generated config → terraform validate/plan
```

against: mock provider servers · existing artifact fixtures · one read-only live
provider · the Node implementation at the **product-output** level. Then decide:

- Is the Go path materially simpler? (the resthttp deletion is the first proof)
- Are infrastructure-relevant outputs equivalent (byte-gated where §2 requires)?
- Are the wire-layer differences acceptable and documented?
- Is operating the single binary genuinely better?

If any answer is no, stop. This checkpoint is the guard against a second
open-ended migration.

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
3. ✅ **Simplify `terraformcmd` + drop `pyoserr` (`8571d8f`)**: kept isolation,
   process-group kill, timeouts, bounded output, redaction, platform gate,
   executable precedence, the downstream `complete` gate, and all operator error
   codes; dropped the Node path-resolution + PATHEXT unicode-lowering emulation
   (`unicode_lower.go`) and the `[Errno 2]` spelling. Net −1,006 LOC.
2. ✅ **Drop `nodefserr`** (uncommitted — awaiting review): reverted 72
   adoption sites across 10 files (metadata: packs.go, resources.go,
   rootcatalog.go, validation.go; tfrender/transform_artifacts.go incl. its
   batch-rollback path; envgen/environment_generator.go; modulesgen/
   generator.go; collectors/rest.go; cmd/iw/main.go,
   commands_topology.go) to plain Go errors; deleted `internal/nodefserr`
   (package + tests, 1,928 LOC) and its 6 differential test files/fixtures
   (`filesystem_cli_differential_test.go`,
   `rest_filesystem_parity_test.go`, three
   `filesystem_error_differential_test.go` copies,
   `transform_artifacts_fserr_test.go`, plus the
   `capture-node24-transform-filesystem-errors.mjs`/`.json` oracle fixtures);
   updated one surviving non-differential assertion in
   `collectors/rest_test.go` that still expected Node's `EISDIR` wording to
   the Go-native `*fs.PathError` text; removed the stale "Filesystem error
   text decision" divergence section from `go-runtime-plan.md`. Net −4,955
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
Once §7 is clear, the **§5 vertical-slice checkpoint** is the go/no-go before any
further breadth (blocks C/D — plan lifecycle, adopt/apply — under the v2
contract).
