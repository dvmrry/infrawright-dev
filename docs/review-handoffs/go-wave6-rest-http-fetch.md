# Go Wave 6 REST HTTP Transport and Fetch CLI — Builder Review Handoff

## Intent

- Port the Node 24.15 `rest-http-transport.ts` contract into a goroutine-safe,
  bounded Go transport and connect the existing collector core to the `fetch`
  and `fetch-diag` CLI surfaces.
- Preserve proxy selection, custom trust, redirect, cookie, retry, response
  limit, masking, cleanup, and raw request-target behavior rather than relying
  on `net/http` defaults where they differ from Undici 7.28.0.
- Keep credentialed tenant behavior, performance-report artifacts, and the
  injected test seam outside this parcel's production-side claims.

## Base / Head

- Base: `c32479d6fdee4ce44944c4b8b8971900b97beda3`.
- Head: shared uncommitted working tree on
  `feature/go-canonjson-foundation`.
- Reproducible frozen REST/fetch/doc/gate manifest:
  `b0642b2ac935537155fbf28b5433cfc01907d51bdc982c4379fd63465a897da5`.
  From the repository root, compute it with:

  ```sh
  LC_ALL=C find Makefile docs/go-runtime-plan.md go/go.mod go/go.sum \
    go/vendor go/scripts go/internal/resthttp go/cmd/iw/commands_fetch.go \
    go/cmd/iw/fetch_differential_test.go go/cmd/iw/main.go -type f -print0 \
    | LC_ALL=C sort -z \
    | xargs -0 shasum -a 256 \
    | shasum -a 256
  ```

  The first digest printed (before `-`) is the manifest identity. The command
  covers exactly 77 files and hashes a path-bearing, sorted SHA-256 listing.
  This handoff is intentionally excluded to avoid a self-referential hash;
  every implementation, dependency, vendor, generator-gate, CLI, test, and
  shared plan/Make surface named by the diff command below is included.
- Diff command: inspect `Makefile`, `docs/go-runtime-plan.md`, `go/go.mod`,
  `go/go.sum`, `go/vendor/`, `go/scripts/`, `go/internal/resthttp/`,
  `go/cmd/iw/commands_fetch.go`, `go/cmd/iw/fetch_differential_test.go`, and
  the fetch-specific hunks in `go/cmd/iw/main.go`, including untracked files
  reported by `git status --short`.

## Files Changed

- New `go/internal/resthttp` package: transport, production HTTP/1.1 request
  serializer and bounded response parser, WHATWG URL boundary, proxy selector,
  trust loader, generated Node roots, tough-cookie-compatible parsing, response
  handling, and tests.
- Generated inputs and notices under `go/internal/resthttp`, including the
  145-certificate Node 24.15 bundled root authority and the tldts 7.4.8 public
  suffix trie.
- Go dependency and vendor updates for `golang.org/x/net/idna` v0.57.0 and
  transitive `golang.org/x/text` v0.40.0.
- `go/scripts/check-vendor.sh`, `Makefile`, and the generator `--check` paths
  that make vendor/generated drift part of the standard `make check` gate.
- Fetch/fetch-diag CLI implementation, recorded-transport differential, and
  dispatch wiring under `go/cmd/iw`.
- Reviewed divergence entries in `docs/go-runtime-plan.md`.
- This handoff.
- Intentionally untouched: collector adapter semantics already landed in the
  prior slice; live tenant credentials; performance-report serialization;
  plan/adopt/apply; HTTP/2; and Node startup trust modes that would change the
  frozen root authority.

## Source Inputs Consulted

- Provider schemas: N/A; no provider schema or field mapping changes.
- OpenAPI/API contracts: N/A; transport behavior is below API schema shape.
- Provider source files: N/A.
- Pack metadata: committed pack roots drive the fetch CLI differential, but
  this parcel does not change their contents.
- Existing docs or design records: `docs/go-runtime-plan.md`, the Go port
  handover, and the repository adversarial-review workflow.
- Other source evidence:
  - `node-src/io/rest-http-transport.ts` at the reviewed base (SHA-256
    `786ec0158a23c4766b9dd5a7e3ecd6da02f875a0dbddc6d091604d0c8f256c15`);
  - `node-src/collectors/rest.ts` and `node-src/cli/main.ts` for fetch wiring;
  - `node-tests/rest-http-transport.test.ts` and retained CLI/collector tests;
  - Node v24.15.0, Undici 7.28.0, tough-cookie 6.0.2, Node's bundled
    certificate authority, and live raw-socket probes;
  - Undici 7.28.0 `lib/core/request.js`, `lib/core/util.js`,
    `lib/dispatcher/client-h1.js`, `lib/dispatcher/client.js`,
    `lib/dispatcher/proxy-agent.js`, and `lib/core/connect.js` for Host/SNI,
    reserved headers, ALPN, parser strictness, header accounting, and CONNECT;
  - Undici 7.28.0's bundled llhttp 9.3.0 `src/llhttp.c`, `src/http.c`,
    upstream `src/llhttp/http.ts`, and `src/llhttp/constants.ts` for raw-byte
    Transfer-Encoding recognition, ordered Transfer-Encoding/Content-Length
    flags, Content-Length whitespace, chunk-extension states, and checked
    uint64 chunk-size accumulation;
  - tough-cookie 6.0.2 `dist/index.js` for the C0-only cookie-pair grammar,
    whitespace-normalized attribute keys/values, verbatim request-pair
    serialization, IPv6 domain canonicalization, trailing-dot lookup
    permutations, and MemoryCookieStore observability;
  - WHATWG URL behavior, Node's `NODE_OPTIONS` parsing, Go `crypto/x509`, and
    the pinned `x/net/idna` Unicode 15 tables.

## Generated Artifacts

- Reports: None.
- Schemas: None.
- Fixtures:
  - exact Node bundled-root data and notice;
  - generated tldts 7.4.8 public-suffix trie and license;
  - negative-serial and SAN-less-CN trust fixtures;
  - 25-vector tough-cookie 6.0.2 expiry/jar corpus;
  - tough-cookie 6.0.2 raw name/value, domain-scope, and production-wire
    corpus;
  - Node/Undici raw-string Content-Length, request-header/Host/proxy,
    response-head/CONNECT, and TLS ALPN oracles.
- Snapshots: recorded HTTP exchanges and temporary separate CLI trees only.
- Demo or lab outputs: unchanged.
- Artifact drift intentionally expected: none. Re-running both generators must
  retain SHA-256
  `6c195a48aca240bf5a82ecf54b53f78b59228c58b6800f9ec1608384765a39e9`
  for `node_roots_data.go` and
  `76f9385c2f5a6f725bf05f4fea2a95f864afe65a18b4badca10ba7e8647933b1`
  for `publicsuffix_data.go`.

## Expected Delta

- Production fetch uses the Go HTTP transport with exact reviewed proxy,
  trust, redirect, cookie, retry, limit, diagnostic, and cleanup behavior.
- Every selected HTTP or HTTPS proxy route is CONNECT-tunneled, including a
  plain HTTP target; the inner request uses origin form and preserves reviewed
  malformed-percent/raw-pipe WHATWG bytes without leaking target userinfo.
- Caller Host overrides retain explicit presence, including empty and
  whitespace-only values, emit as validated Latin-1 field bytes, and select
  HTTPS target SNI/certificate identity without changing TCP routing, CONNECT
  authority, or proxy TLS identity.
- Response heads use a bounded Undici-calibrated parser instead of
  `net/http.ReadResponse`: strict informational/upgrade, framing, duplicate,
  obs-fold, and numeric rules apply while raw initial headers remain visible.
  Status codes below 100 and unsolicited 100/101 reject, while 102/103/199 are
  discarded before the final response. Transfer-Encoding uses llhttp's
  bytewise final-candidate recognizer rather than RFC/Unicode token parsing;
  empty repeated fields, ordered Content-Length conflicts, and trailing
  SP-versus-HTAB behavior remain source-defined. Chunk extensions follow the
  pinned tchar/quoted/quoted-pair states, and chunk sizes use checked uint64
  accumulation without a 16-digit cap. Both origin and CONNECT field
  name/value totals reject at 16,384 bytes.
  Chunk trailers and raw chunk-size lines deliberately use that bound even
  though Undici 7.28 accepts oversized trailers and extensions; the runtime-
  plan ledger records these fail-closed safety divergences and their separate
  Node-accepts/Go-rejects regressions.
- `fetch` and `fetch-diag` argument, validation, diagnostic, close-precedence,
  and recorded-response behavior match the Node oracle credential-free.
- Generated output changes are limited to the new root/suffix authorities and
  vendored Go modules; existing metadata, demo, and Terraform bytes do not
  change.
- No-op areas: injected RoundTripper seam values remain raw; no proxy means a
  caller Proxy-Authorization header remains caller data; successful ordinary
  ASCII/Unicode-15 hosts and standard headers retain their source behavior.

## Invariants Claimed

- Evidence must not be silently dropped: fixed failure categories, retryable
  flags, certificate/timeout/connection hints, redirect precedence, response
  limits, and collector diagnostic ordering remain source-defined.
- Generic matcher evidence must not outrank source-backed evidence: the
  special-host, IDNA, TLS, header, cookie, and `NODE_OPTIONS` boundaries are
  each pinned by a Node/Undici/tough-cookie fixture or explicitly fail closed.
- Source precedence/provenance must remain explicit: lowercase proxy variables
  have the source's `Object.hasOwn` precedence; `REQUESTS_CA_BUNDLE` outranks
  `SSL_CERT_FILE`; Node's bundled roots replace the host pool; `NO_PROXY` is
  evaluated against the selected target route.
- Ambiguity must stay classified instead of being coerced to success: unsafe
  host punctuation, post-Unicode-15/status-changed IDNA, mixed-direction Ada
  quirks, unsupported Node trust mutation, malformed complete proxy
  credentials, case-colliding maps, invalid reserved headers, negative serial
  CAs, and SAN-less endpoint certificates fail closed.
- Provider-readiness counts: N/A; fetch result counts remain owned by the
  existing collector core.
- Adoption safety invariants:
  - no request reaches a direct endpoint or proxy before URL/host/header and
    selected-proxy authorization validation completes;
  - caller `Connection` is consumed into exactly one serializer-owned
    `connection` header; Keep-Alive, Transfer-Encoding, Upgrade, Expect, and
    invalid Connection tokens fail before network contact;
  - TLS to a proxy and TLS to an origin advertise only `http/1.1` through
    ALPN, matching Undici's non-H2 connector;
  - authorization and cookies strip on the source-defined redirect boundary;
  - 307/308 POST bodies are never replayed;
  - response bodies are bounded and closed on every terminal path;
  - redirect, declared-limit, streaming-limit, and rejected-CONNECT cleanup
    closes the socket before any framing reader can drain a stalled remainder;
  - `Close` marks the dispatcher closed immediately, waits only for active
    wire/body work, and excludes logical retry/redirect/performance gaps;
  - root and public-suffix return values are defensive copies.

## Tests Run

- Commands and exact outcomes:
  - Final clean requalification on 2026-07-17:

    ```text
    $ make check
    ℹ tests 787
    ℹ pass 785
    ℹ fail 0
    ℹ skipped 2
    ℹ duration_ms 52513.550333
    ok  github.com/dvmrry/infrawright-dev/go/cmd/iw                 30.731s
    ok  github.com/dvmrry/infrawright-dev/go/internal/resthttp     6.898s
    cd go/internal/resthttp && go run generate_node_roots.go --check
    cd go/internal/resthttp && go run generate_publicsuffix.go --check
    [exit 0]

    $ cd go && go test ./...
    ok  github.com/dvmrry/infrawright-dev/go/cmd/iw                 30.020s
    ok  github.com/dvmrry/infrawright-dev/go/internal/artifacts     0.811s
    ok  github.com/dvmrry/infrawright-dev/go/internal/canonjson     0.237s
    ok  github.com/dvmrry/infrawright-dev/go/internal/cliargs       0.771s
    ok  github.com/dvmrry/infrawright-dev/go/internal/collectors    10.331s
    ok  github.com/dvmrry/infrawright-dev/go/internal/deployment    1.130s
    ok  github.com/dvmrry/infrawright-dev/go/internal/envgen        23.173s
    ok  github.com/dvmrry/infrawright-dev/go/internal/metadata      1.949s
    ok  github.com/dvmrry/infrawright-dev/go/internal/modulesgen    17.966s
    ok  github.com/dvmrry/infrawright-dev/go/internal/nodefserr     1.178s
    ok  github.com/dvmrry/infrawright-dev/go/internal/procerr       1.246s
    ok  github.com/dvmrry/infrawright-dev/go/internal/pyoserr       1.119s
    ok  github.com/dvmrry/infrawright-dev/go/internal/pypath        0.769s
    ok  github.com/dvmrry/infrawright-dev/go/internal/pyunicode     4.071s
    ok  github.com/dvmrry/infrawright-dev/go/internal/resthttp      7.446s
    ok  github.com/dvmrry/infrawright-dev/go/internal/roots         0.767s
    ok  github.com/dvmrry/infrawright-dev/go/internal/terraformcmd  8.931s
    ok  github.com/dvmrry/infrawright-dev/go/internal/tfrender      0.726s
    ok  github.com/dvmrry/infrawright-dev/go/internal/transform     1.176s
    ?   github.com/dvmrry/infrawright-dev/go/internal/transformrun  [no test files]

    $ cd go && go test -race ./internal/resthttp/
    ok  github.com/dvmrry/infrawright-dev/go/internal/resthttp  8.081s

    $ make differential
    npm run build:metadata-cli
    > tsc -p tsconfig.json --noEmit && node scripts/build-metadata-cli.mjs
    make check-resthttp-generated-live
    cd go/internal/resthttp && go run generate_node_roots.go --check --live
    cd go/internal/resthttp && go run generate_publicsuffix.go --check --live
    cd go && go test -mod=vendor -count=1 ./cmd/iw -run 'Differential|^TestFetchDiagValidatesHostBeforeTransportSetup$|^TestFetchArgumentContractCredentialFree$|^TestFetchEmptyPackRootMakesNoRequests$'
    ok  github.com/dvmrry/infrawright-dev/go/cmd/iw  26.032s
    ```
  - Offline generated-data tripwire proof used a disposable parcel staging
    tree with no `node_modules` and a first-on-`PATH` `node` shim that exits
    97:

    ```text
    $ test ! -e /private/tmp/infrawright-restfetch-offline/repo/node_modules && \
        env GOCACHE=/private/tmp/infrawright-restfetch-offline/gocache \
        PATH=/private/tmp/infrawright-restfetch-offline/tripwire:/usr/bin:/bin:/usr/sbin:/sbin \
        make -C /private/tmp/infrawright-restfetch-offline/repo --no-print-directory \
        check-resthttp-generated GO=/etc/profiles/per-user/dm/bin/go
    cd go/internal/resthttp && /etc/profiles/per-user/dm/bin/go run generate_node_roots.go --check
    cd go/internal/resthttp && /etc/profiles/per-user/dm/bin/go run generate_publicsuffix.go --check
    [exit 0; node tripwire silent]
    ```

    Appending one byte-changing comment to the staged `node_roots_data.go`
    made the same command fail before any oracle execution with
    `node_roots_data.go SHA-256 = 62b1d7a32cb2973c14db34fdfa8d317b3bc0d3d274932e54b66c0bcf19e56ea0, want 6c195a48aca240bf5a82ecf54b53f78b59228c58b6800f9ec1608384765a39e9; run go generate .`.
    The explicit live target above rederived both committed files from Node
    24.15.0 and the pinned `tldts` sources and compared exact bytes.
  - A Go-only `PATH=/etc/profiles/per-user/dm/bin` scoped run of
    `TestFetchDiagValidatesHostBeforeTransportSetup|TestFetchArgumentContractCredentialFree`
    passed in 1.492s and visibly reported one parent `SKIP` plus ten subtest
    `SKIP` lines with `node not on PATH; the differential lane needs the pinned
    Node 24`.
  - `go test -mod=vendor -count=1 ./internal/resthttp` — pass.
  - `go test -mod=vendor -race -count=1 ./internal/resthttp` — pass.
  - `go test -mod=vendor -count=20 ./internal/resthttp` — pass.
  - `go vet -mod=vendor ./internal/resthttp` — pass.
  - `go vet -mod=vendor ./...` — pass on the complete shared Go tree after
    the final cookie remediation settled.
  - `go generate ./...` twice from `go/internal/resthttp` — pass with both
    generated hashes unchanged.
  - `make check-resthttp-generated` — pass.
  - `make check-go-vendor` — pass, including an exact disposable re-vendor
    comparison and `go test -mod=vendor ./...`.
  - `CGO_ENABLED=0 GOOS={linux,darwin} GOARCH={amd64,arm64} go test
    -mod=vendor -c -o /dev/null ./internal/resthttp` — all four combinations
    pass.
  - `node go/internal/resthttp/testdata/undici_7_28_raw_string_content_length.mjs`
    under Node v24.15.0 — pass; string `+3` fails before network contact.
  - `node go/internal/resthttp/testdata/undici_7_28_response_head.mjs` —
    pass; covers parser strictness, raw headers, origin/CONNECT thresholds,
    uint64 Content-Length acceptance, and oversized trailer/chunk-extension
    acceptance.
  - `node go/internal/resthttp/testdata/undici_7_28_request_headers.mjs` —
    pass; covers Host/proxy wire values, reserved headers, Connection, and
    explicit-empty versus absent User-Agent.
  - `node go/internal/resthttp/testdata/undici_7_28_alpn.mjs` — pass; both
    proxy and origin TLS legs negotiate `http/1.1`.
  - `node go/internal/resthttp/testdata/tough_cookie_6_0_2_values.mjs` —
    pass; covers raw cookie names/values, all recognized attribute-key
    whitespace cases, jar scope, and Undici's outgoing DEL rejection.
  - `bash /Users/dm/.codex/skills/go-error-handling/scripts/check-errors.sh
    --no-bare-return --json internal/resthttp` from `go/` — pass with zero
    string-comparison or log-and-return findings. The optional bare-return
    heuristic reports the expected low-level wire/parser propagation sites.
  - `gofmt -l go/internal/resthttp` — no output; `git diff --check` — pass.
- Fetch CLI verification includes four Go-vs-Node comparisons in
  `go/cmd/iw/fetch_differential_test.go`: recorded transport, diagnostic
  preflight, credential-free arguments, and empty-pack no-wire behavior.
  `TestFetchTransportClosePrecedence` is a separate Go-only test. A fresh
  review approved that CLI surface after host validation moved ahead of
  transport setup and temporary Go binaries became uniquely named under
  ignored `dist/`.
- The offline proof above is deliberately for the generated-data subgate, not
  a claim that the current pre-cutover aggregate `make check` is Node-free.
  The aggregate still runs the active Node CLI and Node test suite; moving it
  to the Go CLI and then adding the whole-repository Node/npm tripwire are
  runtime-plan slices 10 and 11, outside this REST/fetch parcel.
- Tests not run: credentialed live-tenant fetch and production performance
  report generation. They require Block H qualification and Block E,
  respectively; neither contract is silently claimed here.

## Review Remediation

- Two initial fresh-context reviews requested changes. The consolidated fixes
  cover: CONNECT topology; target/proxy authorization; reserved headers;
  tough-cookie expiry; `Close` liveness; case-colliding maps; narrow TLS
  incompatibilities; unsafe hostname punctuation; generated/vendor gates;
  request-location serialization; `NODE_OPTIONS`; and defensive roots.
- Production now owns a narrow HTTP/1.1 serializer because `net/http` cannot
  represent the required raw request-target and reserved-header behavior.
- String `Content-Length: +3` is rejected while `003` canonicalizes to `3`,
  matching a retained Undici 7.28 live probe.
- The first wire recheck then confirmed three blockers: Host override values
  were being mistaken for URL hostnames, `net/http.ReadResponse` accepted and
  rewrote response heads differently from Undici, and origin/CONNECT headers
  were not capped at Node's 16,384-byte default. The remediation preserves
  explicit Host (including empty), derives target TLS identity from it, and
  replaces the stdlib response parser with the bounded raw parser and retained
  Node/Undici probes.
- The behavior recheck confirmed related request and cleanup blockers. The
  serializer now consumes Connection, rejects Keep-Alive/Upgrade, emits an
  explicitly empty User-Agent, and pins ALPN to `http/1.1`. Initial response
  Connection, Transfer-Encoding, Trailer, and Pragma remain source-visible;
  `Cache-Control` is never synthesized. Body destruction and non-200 CONNECT
  rejection close immediately instead of draining. Canonical Content-Length
  values through uint64 max reach the application limit classification.
- The behavior recheck also found that Go cookie serialization quoted or
  dropped tough-cookie-accepted names/values. A collision-free opaque jar
  representation now leaves jar matching/expiry/order intact while restoring
  the exact raw request pair, including production Latin-1 semantics.
- Follow-up live probes corrected the framing-boundary evidence: Undici 7.28
  accepts oversized chunk trailers and chunk extensions even though it caps
  initial origin and CONNECT heads. Go retains the shared 16,384-byte trailer
  parser and caps raw chunk-size lines at the same threshold as the explicit
  fail-closed safety divergences recorded in `docs/go-runtime-plan.md`.
- A final builder QA pass found two additional blockers before freeze. The
  chunk-size extension reader had no progress bound, so it now rejects a raw
  chunk-size line when its byte count reaches 16,384 and closes even if CRLF
  never arrives. Cookie attributes had still been delegated to Go's untrimmed
  attribute-key grammar; the direct tough-cookie 6.0.2 loop now preserves
  trimmed key/value, duplicate/reset, Secure, Domain, Path, HttpOnly, SameSite,
  Expires, and Max-Age semantics. Live corpus vectors plus production redirects
  prove spaced `Secure` prevents HTTPS-to-HTTP leakage and spaced `Domain`
  retains sibling-host scope.
- Two later fresh-context response/cookie reviews found four remaining source
  mismatches. Transfer-Encoding is now classified from raw bytes using
  llhttp's last comma candidate and repeated-field state, including opaque
  parameters/quotes/obs-text and trailing-SP-but-not-HTAB recognition.
  Transfer-Encoding versus Content-Length is processed in wire order: an
  empty/OWS-only TE field before CL is inert, while any TE field after CL and
  any earlier nonempty TE before CL reject. Content-Length likewise accepts
  leading SP/HTAB and trailing SP only. The chunk reader now implements the
  exact tchar, empty-name/value, quoted-string, and quoted-pair grammar with
  checked uint64 arithmetic and arbitrary leading zeros while retaining the
  approved 16,384-byte raw-line bound.
- Cookie storage now wraps the standard jar at the narrow tough-cookie host
  boundary. IPv6 URL hosts and Domain attributes canonicalize to the same
  unbracketed address; multi-label trailing-dot host-only cookies retain
  tough-cookie 6.0.2 MemoryCookieStore's intentionally unreachable behavior;
  dot/no-dot redirects never widen scope; and a separate single-label store
  preserves same-dot host-only lookup while rejecting explicit Domain
  attributes before Go can strip the terminal dot. Retained tough-cookie,
  direct-jar, and real CONNECT redirect vectors cover each direction.
- The final freeze review split generated-data verification into an offline
  pinned-digest `--check` path and an explicit `--check --live` rederivation.
  Both committed output digests and local notice/license digests are checked
  offline; `make check-resthttp-generated-live` and `make differential` retain
  exact Node/tldts source drift detection when the oracle is installed.
- Four oracle-absent fetch paths now report visible `t.Skip` outcomes. The
  named, non-default `differential` target first rebuilds the metadata CLI,
  performs both live generated-data checks, and then runs all four cmd/iw
  Go-vs-Node comparison families.
- Direct tough-cookie 6.0.2 probes corrected the RFC 6761 interpretation:
  multi-label `local`, `example`, `invalid`, `localhost`, and `test` names
  collapse to a nonempty two-label registrable boundary and are accepted;
  only bare `local`, `example`, and `test` are rejected. The retained vectors
  `sub_localhost`, `api_test`, `foo_local`, and `x_example` cover parsing and
  cross-subdomain retrieval. `bare_localhost_parent_boundary` and
  `bare_invalid_parent_boundary` pin tough-cookie's parent-only store,
  overwrite, and deletion behavior for Domain attributes set by descendants.
- The bare-domain compatibility path originally required multiple storage
  calls per response. The final adversarial recheck found that this could let
  concurrent reads or writes observe a partial response update. The jar now
  serializes the complete update and reads with one mutex; the deterministic
  `TestToughCookieJarSerializesResponseCookieUpdates` barrier test and the
  race gate pin that atomicity. The fresh-context reviewer then returned
  `APPROVED`.

## Known Deferrals

- Deferred work: performance recorder/report bytes, live credentialed tenant
  qualification, HTTP connection pooling/performance tuning, and broader Node
  trust modes.
- Reason it is safe to defer: production passes a nil performance recorder;
  the recorded transport proves request/result bytes without secrets; making a
  new network optimization or trust mode now would widen the reviewed oracle.
- Follow-up owner or trigger: Block E owns performance reports; Block H owns
  keyed tenant qualification; any connection-pooling or trust expansion needs
  its own Node wire/trust differential and adversarial review.

## Review Focus

- Highest-risk paths: `production_wire.go`, `response_wire.go`, `transport.go`,
  `proxy.go`, `ca.go`, `cookies.go`, `special_url.go`, both generators, the
  Make/vendor gates, and fetch CLI setup/cleanup precedence.
- Specific assumptions to attack: HTTP-target CONNECT behavior; default-port
  CONNECT headers; TLS-to-proxy then TLS-to-origin; raw target/query bytes;
  caller versus constructor proxy auth; Host presence and Host-derived target
  SNI; Connection/Keep-Alive/Upgrade/User-Agent serialization; ALPN on both TLS
  legs; CL/TE/Trailer/Expect across redirects; status 000/020/099 and
  100/101/102/103/199; duplicate CL; wire-ordered empty/nonempty TE+CL;
  raw-byte final-candidate TE recognition; CL trailing SP versus HTAB;
  obs-fold; non-chunked TE; uint64 CL/chunk sizes; chunk-extension
  tchar/quoted/escape grammar; and the exact 16,384-byte origin and CONNECT
  header boundary; conscious approval of the ledgered chunk
  framing divergences (Undici accepts oversized trailers/extensions, Go
  rejects their respective running total at 16,384);
  raw response-header visibility;
  response-body close timing; retry/Close races; Latin-1 header boundaries;
  cookie name/value/deletion/expiry ordering, whitespace-normalized attribute
  scope, IPv6 Domain attributes, and trailing-dot host-only/cross-dot behavior;
  malformed `NODE_OPTIONS`; and map collision determinism.
- Source evidence the reviewer should verify: owning TS/tests and independent
  live Node 24.15 + pinned-library probes, not regenerated Go output alone.
- Both the final wire recheck and final behavior recheck must explicitly state
  whether they approve the ledgered oversized-trailer and chunk-extension
  divergences; a generic package-pass result is not approval of those
  boundaries.
- Generated artifacts to compare: run both generators in `--check` and normal
  mode, compare exact hashes, and run the vendor drift gate.
- Edge cases that could silently overclaim, remap, drop, or weaken evidence:
  using forward-proxy absolute form instead of CONNECT; sending target userinfo
  as Basic auth; accepting an invalid caller Content-Length; inheriting host
  roots; relaxing bidi/negative-serial/SAN validation; waiting for a sleeping
  logical request during `Close`; or serializing diagnostics with `url.String`.
