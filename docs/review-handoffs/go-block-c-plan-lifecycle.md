# Block C handover — plan lifecycle (for GPT Sol)

Date: 2026-07-17. From: Claude (reviewer/coordinator). To: Sol Ultra,
coordinating GPT implementor agents. Self-contained. Normative specs:
[go-runtime-v2.md](../go-runtime-v2.md) (the scope contract) and the Node
source under `node-src/domain/plan-*.ts` (the oracle). Where this brief and
those disagree, the source wins; fix both.

## 0. Prime directive — Block C is a BYTE-EXACT block

Unlike the wire/IO layer v2 re-scoped to Go-native, Block C is squarely inside
the **artifact layer**: plan fingerprints, evidence digests, saved-plan
classifications, and the `REPORT=` JSON are all in the v2 §2 "preserve exactly"
column. **Full byte parity against the Node oracle applies here.** The
differential-oracle method governs the whole block. The only Go-native
allowances are the same as everywhere: filesystem-error *wording* (already
Go-native — do not reintroduce nodefserr), internal syscall sequences, and
diagnostic-only formatting that never reaches a report or digest.

## 1. Where this sits

- v2 scope reset is complete and accepted (module stdlib-only, transport on
  net/http, ~20k lines of emulation removed). Branch `feature/go-canonjson-
  foundation` @ `c17bfee`, clean.
- PR 247 reconciled + accepted: Terraform env ceiling 4096, lossless
  drift-policy numeric parity. Adopt-policy merge is deferred to **Block D**
  (not C) — its building block `isSupportedDriftPolicyVersion` is already ported.
- Block C is the last large domain rock before the CLI is operator-complete.
  After C: Block D (adopt/oracle/apply), then E (small commands/perf), then the
  §5 vertical-slice live half and cutover.

## 2. Entry conditions — ALL MET (verified 2026-07-17)

1. ✅ `terraformcmd` done (`c32479d`, simplified in `8571d8f`): process
   isolation, timeouts, bounded output, redaction, platform gate, executable
   precedence, and `TerraformShowPlan` decode — with the `complete`-field gate
   **deliberately absent** here (it belongs to Block C, see §4). Do not add it
   to terraformcmd; consume `TerraformShowPlan` and gate downstream.
2. ✅ `artifacts` (bounded-files) done: `ReadBudget` with a documented
   **serial-charging** convention (`budget.go:31` — charge `Reserve`/
   `EnterDirectory`/`ReserveDirectoryEntry` single-threaded; observe counters
   only after the barrier), `nodeMaximumStringLength` pinned to `536_870_888`
   (`api.go:13`), `os.Root`-jailed TOCTOU snapshots, `-race`-soaked. Plan
   fingerprint/evidence MUST charge serially per this convention — the
   determinism rule forbids goroutine-influenced diagnostics.
3. ✅ drift-policy runtime matching API ported (PR 247): `NewDriftPolicy`,
   `ParsePolicyPath`, `PolicySelectorMatches`, `NormalizePolicyPath`, and the
   `*DriftPolicy` matcher methods (`ProjectionOmits`/`ToleratesPlanPath`/
   `StaleEntries`/`MarkMatched` — confirm the exact exported surface in
   `go/internal/metadata/driftpolicy.go` before wiring). Assessment consumes
   these directly; do not re-port.
4. ✅ `canonjson` (renderers, lossless numbers, equality) and `procerr` (error
   spine + CLI renderer) are the byte/error substrate — reuse, never
   reimplement.

## 3. Source surface + seams

New Go packages to create (keep them DISJOINT for parallel work):

| Node source | LOC | → Go package |
|---|---:|---|
| `plan-fingerprint.ts` | 719 | `internal/plan` (fingerprint.go) |
| `plan-evidence.ts` | 616 | `internal/plan` (evidence.go) |
| `plan-contract.ts` | 485 | `internal/plan` (contract.go — the `complete` gate @ :463) |
| `plan-lifecycle.ts` | 455 | `internal/plan` (lifecycle.go) |
| `plan-assessment.ts` | 960 | `internal/assessment` |
| `plan-assessment-runner.ts` | 460 | `internal/assessment` |
| `plan-assessment-inputs.ts` | 446 | `internal/assessment` |
| `plan-report.ts` | 508 | `internal/assessment` (report.go — byte-exact REPORT) |
| `plan-eval.ts` / `plan-policy.ts` | 343 | `internal/assessment` |
| `contracts/saved-plan-assessment-semantics.ts` + `validators.ts` | 436 | `internal/assessment` (semantics.go) |
| `docs/schemas/saved-plan-assessment.schema.json` | — | embedded, hand-validated |

Seams it plugs into (all done): `terraformcmd.TerraformShowPlan` (plan JSON),
`artifacts` (bounded/TOCTOU reads of saved plans + sources), `metadata`
DriftPolicy runtime API, `canonjson` (render/equality), `roots`
(plan-roots enumeration — done).

## 4. The three load-bearing invariants (attack these in review)

1. **The `complete`-field fail-closed gate.** `plan-contract.ts:463`:
   `if (plan.complete !== true) fail("plan must be complete before
   assessment")`. This is the safety spine — a `terraform plan` that didn't
   fully resolve must NOT be classified as clean/adoptable. Port it in
   `contract.go`, and add a differential test feeding a `complete:false` plan
   that asserts rejection at exactly this boundary (mirror
   `terraformcmd/show_test.go`'s `complete:false` fixture, which proves the plan
   parses but is NOT auto-gated upstream).
2. **TOCTOU freshness.** `plan-evidence.ts` carries `STALE_PLAN_SOURCES`,
   `PLAN_SOURCES_CHANGED`, `SNAPSHOT_DIRECTORY_CHANGED`, `PLAN_SNAPSHOT_CHANGED`;
   `plan-lifecycle.ts` carries `PLAN_INPUTS_CHANGED` stale detection with
   artifact removal on error. These fail-closed classes must port exactly and
   charge the ReadBudget serially. A saved plan whose sources changed since
   capture must be rejected, not assessed.
3. **REPORT byte-exactness + the drift-marker watch-item.** The `--report`
   JSON is byte-compared against the Node oracle. **Specifically re-confirm**
   (flagged in the PR 247 review): the drift-policy `projection_omit_if` scope
   marker (`driftJSONScalarMarker`/`driftNumericScalarMarker` in
   `metadata/driftpolicy.go`) is a same-run dedup key today and must NOT leak
   into REPORT bytes — its `float:`/`integer:` spelling diverges from Node's
   `String()` and is only inert while it stays internal. When `plan-report`
   lands, prove by test that the marker bytes never appear in `REPORT` output.

## 5. Dependency decision — hand-port schema validation, stay zero-dep

The assessment path validates against `saved-plan-assessment.schema.json` and
the semantics keywords, and **schema-error `details` content is REPORT bytes**
(byte-exact). The module returned to **zero third-party deps** in the v2 reset —
do not reintroduce `santhosh-tekuri/jsonschema` casually. Recommendation:
**hand-port** the small schema + semantics validation (2 relevant schemas),
matching the Node ajv error-detail text on the ported vectors, keeping the
module stdlib-only. If a genuine blocker emerges, escalate before adding a dep —
it must clear the v2 dependency bar (a strictness wrapper that preserves every
fail-closed check), not merely be convenient.

## 6. Parcels (fan-out plan)

Sequenced in three waves; parcels within a wave are file-disjoint.

**Wave C1 (solo) — `internal/plan` fingerprint + evidence.** The deterministic
core everything downstream reads. Port `plan-fingerprint.ts` (HCL structure
scanning, heredoc rejection, module-source fail-closed, backend/init-sources
SHA capture) and `plan-evidence.ts` (the four TOCTOU classes). Charge
ReadBudget serially. Gate: unit vectors + the retained terraform-core
plan/state fixtures; every fingerprint/digest byte-identical.

**Wave C2 (parallel with C3) — `internal/plan` contract + lifecycle.** Port
`plan-contract.ts` (the `complete` gate) and `plan-lifecycle.ts`
(`PLAN_INPUTS_CHANGED`, artifact removal on error, the save/imports-only plan
flow over `terraformcmd`). Depends on C1's fingerprint types.

**Wave C3 (parallel with C2) — `internal/assessment`.** Port assessment +
runner + inputs + eval + policy + report + semantics + hand-validated schema.
Consumes C1 evidence types (via their exported Go types) and the DriftPolicy
runtime API. This is the byte-exact REPORT + classification parcel — the
hardest for parity. Prove the drift-marker non-leak (§4.3).

**Wave C4 (last) — CLI + differential.** Wire `plan`, `clean-plans`,
`assert-clean`, `assert-adoptable` into `cmd/iw` (arg shapes: `plan
--imports-only --save --backend-config --terraform`; `assert-*
--report <file|-> --policy --backend-config`; the legacy usage-code exit
shim already exists). Add the differential corpus: run Go vs the frozen Node
oracle on saved-plan fixtures — classifications, REPORT bytes, exit codes
byte-identical. No credentials: assessment/report run on fixture plans; the
live `terraform plan` half waits for the §5 checkpoint's keyed run.

## 7. Conventions (carry forward — non-negotiable)

- TS source is the spec; port oddities; every exported symbol's doc comment
  names its Node source file; message strings verbatim where they reach bytes.
- Probe ambiguous semantics against the compiled TS (`npx esbuild <file>
  --bundle --format=esm --external:lossless-json` — the `--external` flag is
  mandatory, bundling lossless-json breaks `instanceof`); commit probe fixtures
  with the command in a provenance comment.
- Map/set iteration that reaches bytes → `canonjson.SortedStrings`; load-bearing
  Node orderings preserved with fixed slices; document unordered-safe loops.
- Concurrency only behind collect-then-emit barriers; ReadBudget charged
  serially (§2.2). `-race` anything concurrent.
- Fail closed: unmapped/ambiguous → reject, never silent fallback.
- Per-command env-read asymmetry (`||` vs nullish) is per-command; check each.
- Do NOT commit or push; a coordinator reviews and commits each parcel. Flag
  any out-of-parcel edit in your report (additive-only crossings may be
  accepted; silent ones are not). If an agent dies mid-parcel, the finisher's
  first task is a line-by-line audit of inherited files vs the TS — never trust
  a mid-fix state.

## 8. Block acceptance bar

- `gofmt -l` clean, `go vet ./...` clean, `go test ./...` green, module still
  **zero third-party deps**.
- The standing proof: the four artifact byte-gates (RootCatalog, Transform,
  Topology, Generation) stay byte-identical after every parcel — Block C must
  not perturb them.
- New byte-gates: saved-plan classification + REPORT bytes + exit codes
  byte-identical vs the frozen Node oracle on fixture plans, no skips.
- The three §4 invariants proven by dedicated tests (complete-gate rejection,
  the four TOCTOU stale classes, drift-marker non-leak into REPORT).
- Then Block C is done; Block D (adopt/oracle/apply) is next under the same
  contract.

## 9. Watchlist (ranked)

1. REPORT byte parity — ajv error-detail text is the hard part; decide the
   hand-port early, don't discover a mismatch at the assessment gate.
2. The `complete`-gate must be un-bypassable — it is the safety spine.
3. Drift-marker non-leak into REPORT (§4.3) — the PR 247 forward watch-item,
   now testable.
4. TOCTOU determinism — serial ReadBudget charging or the failure-attribution
   race violates the determinism rule.
5. Oracle drift — this branch's node-src IS the oracle; if main moves, rebuild
   `dist/infrawright-cli.mjs` and re-run the full corpus before continuing.
