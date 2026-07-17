# Go runtime port plan

Status: draft specification. Blocked on completion of the
[Python archive plan](python-archive-plan.md) (in flight as of PR #239) and on
live-provider qualification of the Node operational path. Suggested location:
`docs/go-runtime-plan.md`.

## Goal

Replace the Node 24 runtime with a single static Go binary that is
**byte-compatible** with the Node `iw` CLI and can be dropped in behind the
existing command contract with no consumer-visible change. The Node
implementation becomes the differential oracle for the Go port exactly as
Python was for Node, and is archived by the same evidence-preserving method
once the Go runtime is qualified.

End state:

- One `iw` binary per platform (darwin/linux, amd64/arm64), cross-compiled,
  checksummed, and signed; no Node, npm, or `node_modules` anywhere in the
  runtime, build-verification, or release path.
- The full repository gate passes with `node` and `npm` pointed at failing
  tripwires (the same proof pattern as the Python archive).
- Terraform/OpenTofu remains the only external runtime dependency.

The drop-in seam already exists: `Makefile` routes every operational target
through `INFRAWRIGHT_CLI ?= $(NODE) dist/infrawright-cli.mjs`. Cutover is that
one variable changing to the Go binary. The Make command surface does not
change in this plan; absorbing the Make chain into `iw` is a separate,
post-cutover decision.

## Preconditions (hard gates before the first Go slice)

1. Python archive complete: no tracked `.py`, tripwired gates green.
2. The frozen-evidence suites from archive steps land as **runtime-agnostic
   byte contracts** — they are the primary spec for the Go port and must not
   encode Node-specific access paths.
3. Live-provider qualification of the Node path recorded (read-only keys),
   so Go parity is measured against a *qualified* baseline.
4. A tagged **oracle release**: the last qualified `dist/infrawright-cli.mjs`
   + `.sha256` + pinned Node 24 version, referenced by tag. All Go
   differential tests run against this frozen artifact, not against a moving
   main.

## The byte-compatibility contract

"Byte-compatible" means the differential corpus compares these surfaces
byte-for-byte between Node oracle and Go candidate, per command, on identical
inputs:

| Surface | Examples | Notes |
|---|---|---|
| Generated artifacts | `*.auto.tfvars.json`, HCL tfvars, `*_imports.tf`, moved blocks, lookup sidecars, generated modules, env roots, root catalog | The canonical JSON renderer (sorted keys, `\uXXXX` escaping above 0x7F, Python float repr, lossless integers, 2-space indent) and the HCL renderers are the load-bearing components |
| Reports | assessment `REPORT=` JSON, performance report, authoring reports (reconcile, openapi-map, source-operation-map, source-evidence-eval) | Includes schema-error `details` content and 64-entry truncation sentinel |
| stdout/stderr | listings, roots/scope/plan-roots JSON, fetch preamble and masking, transform drop diagnostics, error lines (code + details rendering per the operator-correctness contract) | Diagnostic *ordering* is part of the contract (see concurrency rule) |
| Exit codes | per command, including usage=2, pack-set skip=3, drops-check semantics | Enumerate from the operator-failure-semantics slice; freeze as a table in this doc before phase 1 |
| Digests | plan fingerprints, `sources_sha256` NUL framing, decision/evidence digests | Any framing re-derivation is a spec bug, not an improvement opportunity |
| Filesystem behavior | pulls tree bytes, atomic write/rename patterns, freshness/TOCTOU checks, `O_NOFOLLOW`-class guards | Port semantics, not syscall sequences; differential compares resulting trees + failure classes |

Allowed divergences: an explicit, reviewed list in this document. It starts
**empty**. `--help` text is the only candidate expected to be proposed, and it
still requires an entry here before it may diverge.

### Filesystem error text decision (2026-07)

Filesystem error wording is part of the byte contract; the Go port does not
accept host-native `os.PathError` text as a divergence. Raw Node `SystemError`
forms must live behind operation-aware wrappers in `internal/nodefserr`;
deliberately Python-compatible spellings such as Terraform executable
resolution remain separate, narrow constructors in `internal/pyoserr`. Both
use fixed English text and are added only for source-pinned call sites. They
are not blanket CLI rewriters: code that maps an I/O failure to a fixed
`ProcessFailure` continues
to emit that fixed failure instead. Raw Node path quoting must not reuse the
Terraform resolver's Python escaping rules. Additional errno or two-path forms
require a frozen Node vector before they are added. Call sites must supply the
requested operation/path context when Node reports it; Go's `PathError` may
instead name an offending ancestor or even classify the host failure
differently, so a top-level error-string translator is insufficient.

Names such as `python-compatible`, `python-number`, and `python-lower-15.1`
remain: they describe frozen byte and Unicode semantics that the Go port
implements identically. Same non-goal as the Python archive — no renaming to
remove a word.

## Foundational design decision: dynamic JSON tree, not structs

Go's biggest risk for this codebase is `encoding/json` struct marshaling:
float64 coercion and absent-vs-null-vs-zero conflation — precisely the
distinctions this product exists to preserve. The spec therefore mandates:

- **`internal/canonjson`** ports the `node-src/json/` model as a dynamic
  value tree: object (ordered map), array, string, bool, null, and a
  **lossless number** that preserves the source token (decode via
  `json.Decoder.UseNumber()`; `json.Number` is the raw literal). Absence is
  map-key absence, never a zero value.
- All artifact and contract paths operate on this tree, as the TS code
  already does (its `unknown`-walking guards). Static structs and ecosystem
  parsing libraries are an ergonomics play for **after** parity, and only
  where a strictness wrapper preserves every fail-closed check (e.g.
  `terraform-json` does not know about the `complete`-field fail-closed
  gate — the hand-ported strict decode keeps that job in v1).
- Rendering implements the Python-compatible emitter over this tree: code
  point key sort, ASCII escaping, float repr (including `-0.0`, `1e-06`
  forms), indent, trailing newline — proven by the exhaustive binary64
  digest fixture from the archive plan, not by sampling.

The same phase ports the frozen Unicode tables (`lower-15.1` generated
deltas, HTML entity tables via in-repo data, not `x/text`), digest framing,
and Python-compatible path semantics.

**Spike 0 (go/no-go):** before any other work, `internal/canonjson` must
re-render the committed demo tfvars corpus and the exhaustive number/Unicode
digests byte-identically. If this spike fails structurally, the port stops at
zero sunk cost.

## Concurrency determinism rule

Goroutines must never influence output bytes or diagnostic order. Concurrency
is permitted only behind collect-then-emit barriers, and the fetch scheduler's
fairness/ordering semantics (bounded concurrency, round-robin product
scheduling, ordered diagnostics) are ported as *specified behavior*, not
re-invented. Any output whose order the Node implementation derives from
completion timing must be reproduced with the same rule, or the rule must be
made deterministic on both sides first (Node-side change, before the port).

## Package layout and layering

```
cmd/iw/                 main; version via -ldflags
internal/cli/           arguments (parseArgs-adapter semantics: duplicate
                        rejection, next-token binding incl. leading '-',
                        help cutoff), command glue, usage, exit codes
internal/canonjson/     json/ port + unicode tables + digest framing
internal/contracts/     embedded JSON Schemas + validator + custom keyword
                        functions + 64-entry error-detail truncation
internal/metadata/      loader, packs, resources, terraform-schema,
                        validation, root-catalog
internal/transform/     pull-transform kernel, artifacts, runner, selection
internal/plan/          fingerprints, evidence, lifecycle, assessment,
                        policy, report, exact-plan apply
internal/adopt/         adopt-runner, oracle, generated-config policy,
                        import staging/moves
internal/roots/         roots, scope-paths, plan-roots, environment
                        generation, modules generator
internal/collectors/    fetch engine, retry, selection, diagnostics
internal/zscaler/       product adapters, auth modes, masking
internal/terraformcmd/  terraform invocation: args, env snapshot, limits,
                        timeouts, process groups, platform gate
internal/artifacts/     bounded files, atomic writes, reports
```

Layering is enforced mechanically from day one (`depguard` or an
import-boundary test in the same spirit as `audit-vendor-boundary`) — the
Node tree's convention-only layering is a known gap; the port fixes it for
free. A vendor-boundary audit for `internal/` (no provider tokens outside
`internal/zscaler` + allowlist) lands in phase 1, not as a follow-up.

## Library policy

Stdlib-first. Initial allowlist, all vendored (`go mod vendor`):

| Dependency | Replaces | Constraint |
|---|---|---|
| `golang.org/x/sync` (errgroup) | ad-hoc concurrency | — |
| `santhosh-tekuri/jsonschema/v6` | ajv | must support 2020-12 + custom keyword functions; error-detail *content* parity verified by fixtures |
| `net/http`, `crypto/x509`, `net/http/cookiejar` | undici, tough-cookie | proxy env semantics and `REQUESTS_CA_BUNDLE`/`SSL_CERT_FILE` loading must match the transport contract; cross-origin redirect header stripping and 307/308 body-replay refusal are ported behavior |
| ~~(authoring phase only) `kin-openapi` or hand-port~~ | ~~swagger-parser~~ | Dropped 2026-07: the authoring slice goes AST-first (stdlib `go/ast` + `go/parser` today); typed package loading is gated below, and dedicated OpenAPI matching surfaces are skipped rather than ported (slice 8). The non-OpenAPI `reconcile` core remains in scope. |

In this plan, `go/packages` means the external module import
`golang.org/x/tools/go/packages`; it is not part of the Go standard library.
It is absent from both the initial allowlist above and `go/go.mod`. The current
standalone collector (`tools/source-evidence-ast`) has no module requirements
and imports `go/ast` and `go/parser`, not `go/packages`. Slice 8 must not add
`go/packages` until its dependency and subprocess behavior are reviewed and the
allowlist is deliberately updated. This gate does not change the AST-first
direction.

Explicitly deferred past parity: `hashicorp/terraform-exec`,
`terraform-json`, `hclwrite`, `zscaler-sdk-go`. Each changes bytes or
behavior surface and belongs to a post-cutover ergonomics phase with its own
differential evidence. HCL rendering is hand-ported (it is already a
byte-exact hand renderer in Node; `hclwrite` output differs).

## Interpolation-escaping contract (2026-07 adjudication)

From the `zia_dlp_notification_templates` ADOPT-FAIL analysis. The renderer
matrix is fixed and pinned by `go/internal/tfrender/
interpolation_escaping_test.go` plus the `interpolation-literals`
differential case:

- **JSON tfvars** (`.auto.tfvars.json`): string values byte-verbatim,
  never `${`/`%{`-munged — JSON tfvars are literal, Terraform does not
  HCL-lex them. Provider-canonical `$${X}` stays two dollars; raw `${X}`
  stays one dollar.
- **HCL surfaces** (`tfvars_format=hcl`, import/moved `.tf` blocks):
  `RenderHclQuotedString` escapes exactly once, mechanically, from the
  value handed in (`${`→`$${`, `%{`→`%%{`); upstream stages must hand it
  RAW/provider-canonical values and never pre-escape.
- Provenance: the unconditional-escape defect class lives in the retiring
  Python engine (`engine/transform.py:886` applying HCL quoting inside the
  transform path); Node main and this Go tree are both conformant. The
  live no-op-plan proof for the DLP template class runs during keyed
  qualification.

Go toolchain pinned via `go.mod` `toolchain` directive; version bumps are
build-critical changes, mirroring the Node 24 pin discipline.

## Differential harness

Mirror of the Python→Node method, with the Node oracle in the test-only
role:

- `go-tests` differential runner: executes oracle (`node <pinned bundle>`)
  and candidate (`iw`) with identical argv/env/fixture trees; compares
  stdout, stderr, exit code, and resulting artifact tree bytes. Corpus:
  demo tenant, transform fixtures, plan/assessment fixtures, authoring
  fixtures — the frozen-evidence corpora produced by the Python archive are
  reused directly.
- Oracle resolution honors `NODE` / pinned version probing and is
  structurally excluded from the default gate (suite-selector pattern), so
  `make check` stays oracle-independent; CI runs the differential lane
  explicitly — same shape as `check:all` vs `test:node` today.
- End-state proof inverts it: node/npm tripwires on PATH while the full gate
  runs (the archive plan's step-4 pattern, with `node` substituted for
  `python`).
- Every slice ships with a review handoff in the existing adversarial-review
  format; the reviewer's standing brief includes the archive plan's
  self-comparison warning: a differential that accidentally compares Go to
  Go (or frozen fixture regenerated by the candidate) is evidence
  destruction.

## Authoring AST-only contract gate (slice 8)

The current implementation and fixtures do not yet provide the AST-only
authority that slice 8 needs:

- `node-src/authoring/cli.ts` requires `--openapi` for
  `source-evidence-eval` and runs both the text control and AST candidate
  through `deriveSourceOperationRegistry`, whose reports map source evidence
  to OpenAPI operations.
- `node-src/authoring/provider-probe.ts` prepares and validates the recipe's
  OpenAPI input, calls `deriveSourceOperationRegistry` and
  `buildOpenApiResourceMap`, and emits `openapi-map.json`.
- The frozen `node-tests/fixtures/python-source-evidence-eval-v1.json` retains
  reusable pure evaluator/classification and Markdown cases, but its
  `authoring_cli_artifact_set` command vector is OpenAPI-bound. The
  `python-provider-probe-v1.json` artifact set is likewise OpenAPI-bound. The
  reusable cases remain authority; they must not disappear merely because the
  command contract is re-anchored. The repository has no reviewed Node
  AST-only oracle and frozen authority for replacement command, recipe,
  report, and artifact behavior.

Before slice 8 implementation begins, a Node-first AST-only contract and its
frozen authority must be designed and adversarially reviewed; the Go port must
consume that authority rather than invent it. The same gate must resolve
release routing for the dedicated `openapi-map` surface, OpenAPI-matching
`source-operation-map`, and `reconcile`'s optional OpenAPI augmentation: how
they route while Node remains, and whether they are retired or explicitly
revived before the no-Node archive. `reconcile` itself is not an OpenAPI-only
command: its required API input plus schema/pack core works without `--openapi`
and remains a byte-parity port target. The `INFRAWRIGHT_CLI` cutover and
full-command sweep must not silently strand either that retained core or the
explicitly skipped surfaces.

Any Node-first contract change must deliberately re-anchor the pinned Node
oracle and differential corpus (or prove that the relevant command bytes did
not change). Legacy command vectors may be replaced or retired only through
that reviewed evidence transition; excluding them from the differential suite
is not an acceptable way to make the AST-only gate pass.

The intended slice remains AST-first. Its required scope still includes the
source-facts work and a source-verifiable string-mutation lint over provider
flatten/expand and schema callbacks (`ReplaceAll`/regular-expression mutations
of Terraform string fields), with per-resource/field evidence; the DLP dollar
escape remains the first retained case. This paragraph preserves the
deliverable, not a yet-unreviewed output schema.

## Ordered slices

Each slice is a stacked PR with differential evidence, sized for the
established build→adversarial-review→merge loop.

| # | Slice | Parity gate |
|---|---|---|
| 0 | `canonjson` spike: tree, lossless numbers, renderer, Unicode tables, digests | demo tfvars corpus + exhaustive number/Unicode digests byte-identical — **go/no-go** |
| 1 | `contracts` + `metadata` (loader, packs, resources, terraform-schema) | pack validation verdicts + schema-error details match on fixture corpus |
| 2 | `root-catalog` end-to-end (first full command) | committed catalog byte-identity; `--check` semantics; exit/stderr parity |
| 3 | `transform` kernel + artifacts + runner | demo + transform fixture corpora byte-identical, incl. drop diagnostics and exit semantics |
| 4 | `roots`/`scope-paths`/`plan-roots` + env generation + modules generator | topology JSON, generated roots, module trees byte-identical; `check-modules` green via Go |
| 5 | `collectors` + `zscaler` adapters | recorded-transport fixture parity: pagination queries, retry schedule, masking, preamble, failure hints, pulls-tree bytes |
| 6 | `terraformcmd` + `adopt`/oracle + generated-config policy + staging | oracle scratch-flow parity on fixture plans; import/move artifact bytes; freshness/TOCTOU failure classes |
| 7 | `plan` lifecycle/assessment/apply | fingerprints, saved-plan classification, reports, apply gating parity on retained plan/state fixtures |
| 8 | authoring commands, AST-first (2026-07 downstream decision): after the contract gates above, absorb `tools/source-evidence-ast` natively and grow reviewed AST evidence for Go-SDK↔Terraform surface review; port `source-evidence-eval` and `provider-probe` against that authority; port the non-OpenAPI API/schema/override core of `reconcile`; add the required string-mutation lint. Dedicated `openapi-map`, OpenAPI matching in `source-operation-map`, and `reconcile`'s optional OpenAPI augmentation are SKIPPED pending the routing/retirement decision. | reviewed Node-first AST-only frozen authority; retained non-OpenAPI reconcile corpus; AST evidence and string-mutation corpora; skipped-surface release routing resolved |
| 9 | CLI shell completion: full argument/usage/exit-code surface, `fetch-diag`, remaining commands | full retained-command differential sweep green; skipped-command routing enforced |
| 10 | Release engineering: goreleaser, checksums+signing, version embedding, CI lanes, `INFRAWRIGHT_CLI` cutover flag day | skipped-command routing resolved; `make check` green with Go CLI; runtime-release smoke rewritten for the binary |
| 11 | Node archive by the standard method: freeze Node-oracle evidence, delete Node source/build/CI/release surface, tripwire proof | archive-plan step-4 checklist, node-substituted |

Estimated effort (from the measured Python→Node baseline, ~40–50% of that
port's mass, no new parity methodology to invent): **6–10 agent build-days;
2–4 weeks wall-clock**, dominated by review bandwidth and the final live
re-qualification, not codegen.

## Acceptance (mirrors archive-plan step 4)

1. Full differential corpus green against the pinned Node oracle.
2. `make check` / pack-profile / pruned-checkout / reduced-root gates green
   with the Go `INFRAWRIGHT_CLI`.
3. Runtime-release smoke: binary relocated, no node/npm/node_modules, PATH
   tripwires silent.
4. Demo, modules, root catalog, and frozen fixtures current.
5. Live-provider re-qualification of fetch→adopt→plan→assert→apply with
   read-only-first policy on the qualification tenant.
6. Fresh adversarial review per slice plus a final whole-port review focused
   on silently weakened evidence.

## Non-goals (v1)

- Behavior or artifact changes of any kind, including "obvious" improvements
  found during porting (file them; fix on both sides post-cutover or not at
  all).
- Ecosystem library adoption that alters bytes (`terraform-exec`,
  `terraform-json`, `hclwrite`, `zscaler-sdk-go`) — post-cutover phase.
- Make-chain replacement, `iw`-native task runner, or CLI surface redesign.
- Renaming `python-*` semantic modules.
- Windows support expansion beyond the current best-effort posture.
- Performance work beyond what parity requires (record wins; don't chase).

## Open decisions (resolve before phase 0)

1. **Location**: `go/` module in this repo (recommended: keeps fixtures,
   packs, docs, and review flow unified) vs. a fresh repo.
2. **Module path** and binary artifact naming per platform.
3. **Exit-code table**: freeze the authoritative per-command table into this
   doc from the operator-failure-semantics implementation before slice 1.
4. **Oracle tag**: which Node release becomes the pinned oracle, and whether
   it is re-pinned if Node-side fixes land mid-port (recommendation: re-pin
   deliberately, never track main).
5. **`--help` byte-parity**: pin or add the one allowed divergence.
6. **Signing scheme** (minisign vs cosign) — decide in slice 10, noted here
   so it is not re-litigated per slice.

## Post-cutover simplification candidate: retire logical slug grouping (2026-07)

Ported as-is for byte parity (it is a topology dimension pinned by the
committed root catalog, variable naming, env layout, and whole-root
scoping — not a skippable module). Removal is scheduled, and its window
is deadline-shaped: with no consumers AND no applied state under grouped
addresses yet (no real Apply has run), degrouping today is a pure
code+goldens change. That stops being true at the FIRST real Apply,
which keyed qualification will perform — after that, removal becomes an
identity-keyed moved{} migration over live tenant state, forever.

Preferred slot: in NODE, immediately after the Python archive completes
and BEFORE keyed qualification applies anything. Ship as a deliberate
behavior change with adversarial review: regenerate demo goldens and the
root catalog as schema v2 (slug fields removed, labels ≡ resource types,
variable name always "items"), then simplify the Go tree to match
(bounded deletions in roots, root-catalog, envgen, scope-paths) and
re-anchor the differential corpus.

Gates that remain regardless of state:

1. Cross-root references must have a qualified DEFAULT mechanism first —
   promote #225 cross-state bindings or the inferred lookup-sidecar path
   from opt-in; grouping's co-location was the fallback being removed.
2. An explicit auto-vs-full decision: removing only automatic slug
   derivation keeps most plumbing alive via explicit groups; the
   simplification only pays in full if explicit groups retire too. Check
   the oracle-batching/root-count performance counterweight (#212
   batches by logical root) before committing.
