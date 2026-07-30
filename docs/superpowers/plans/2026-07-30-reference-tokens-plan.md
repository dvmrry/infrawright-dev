# Reference Tokens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Companion to `docs/superpowers/specs/2026-07-30-reference-tokens-design.md`.
Read it first; this plan assumes its verified pipeline trace, the measured
type boundary, and the §5 surface verdicts.

**Goal:** Declared reference fields commit as qualified tokens
(`"<referent_type>.<key>"`) instead of raw tenant IDs; gen-env resolves
every token to a state lookup or a sidecar-ID fallback before the module
boundary.

**Architecture:** One substitution site in
`tfrender.CompileTransformArtifacts` (after sidecar compile, before config
render) covers transform and adopt identically; `DeriveGeneratedBindings`
consumes tokens directly; envgen's expression local becomes total over
tokenised leaves with a sidecar-backed ID fallback; module types stay
provider-strict as the loud final guard (root `type = any` is already
shipped).

**PR cut (stacked):**
- **PR-A** — tfrender: substitution + token-consuming derivation + parity
  fixture regeneration (Tasks 1-2).
- **PR-B** — envgen: total local, token fallback, unresolvable-token
  abort; sidecar-removal guard; HCL comment display (Tasks 3-4).
- **PR-C** — CHANGELOG, docs, migration notes (Task 5).

## Global Constraints

- AGENTS.md Validation Promotion: every new invariant gets a focused
  regression demonstrated failing (or via a stated faithful mutation)
  before the production edit; full suites only at each PR's gate.
- Substitution applies only when the value is present in the referent's
  `key_by_id`; sentinels/system constants, unknown IDs, self-references
  stay literal (today's mixing rule, one layer earlier).
- Tokens must pass the `${`/`%{` interpolation guard.
- No token may reach a module boundary unresolved out of gen-env; an
  unresolvable token aborts generation naming the token.
- Old-shape (ID) values remain valid inputs indefinitely — no flag day.
- This change is adversarial-review-required (alters committed evidence
  shape); each PR stops at "ready for adversarial review".

---

### Task 1: tfrender — token substitution at P1

**Files:**
- Modify: `go/internal/tfrender/transform_artifacts.go`
  (`CompileTransformArtifacts` — hoist `lookupKeyMaps` above
  `renderDeploymentTfvars`; new `substituteReferenceTokens`)
- Test: `go/internal/tfrender/transform_artifacts_test.go` (or a new
  `reference_tokens_test.go` beside it)

**Interfaces:**
- Produces: `substituteReferenceTokens(items map[string]map[string]any,
  references map[string]TransformReferenceSpec, setBlockFields
  map[string]int, lookupKeys map[string]map[string]string)
  (notes []string)` — in-place rewrite; token spelling
  `spec.Referent + "." + key`.

- [ ] **Step 1: failing tests.** Table-driven over the decision table the
  spec pins (reuse the traversal fixtures the existing
  `DeriveGeneratedBindings` tests use so dotted paths and set-blocks are
  the same shapes):
  - scalar ID in `key_by_id` → token
  - list mixing: `["ANY", <known id>, <unknown id>]` →
    `["ANY", "<referent>.<key>", <unknown id>]`
  - set-block nested field (the `SetBlockFields` index path) → tokenised
    at the leaf
  - referent lookup missing (`lookupKeys[referent] == nil`) → untouched +
    note (visible, not silent)
  - self-reference (`resourceType == spec.Referent`) → untouched
  - value already a token (idempotent re-run over tokenised input) →
    untouched
  - number-typed ID (json.Number) → token (string) — the type change is
    deliberate
- [ ] **Step 2: run, verify failure** (function undefined).
- [ ] **Step 3: implement.** Walk `context.References` exactly as
  `DeriveGeneratedBindings` does (same `fieldCandidates` /
  `bindSetBlockField` traversal so the two passes can never disagree on
  which leaves are reference leaves); rewrite in place before
  `renderDeploymentTfvars`; sidecar compile at `:1533` stays above.
- [ ] **Step 4: run, verify pass; then the package suite.**
- [ ] **Step 5: commit** (`tfrender: commit reference fields as qualified
  tokens, not tenant IDs`).

### Task 2: DeriveGeneratedBindings consumes tokens

**Files:**
- Modify: `go/internal/tfrender/transform_artifacts.go` (`resolve`,
  ~`:738-762`)
- Test: same test files as Task 1

- [ ] **Step 1: failing tests.**
  - token with matching `<spec.Referent>.` prefix and known key →
    expression `data.terraform_remote_state.<root>.outputs.infrawright_reference_ids.<referent>["<key>"]`
    (the ID→key hop gone)
  - token with wrong type prefix → skipped, counted under a new
    `token_referent_mismatch` reason, note names the token
  - token whose key is unknown to the referent → skipped, counted
    (`token_key_unknown`), note names the token
  - plain ID → existing behaviour retained (migration path)
- [ ] **Step 2: verify failure. Step 3: implement** (prefix-strip against
  the known `spec.Referent`; key membership via the `key_by_id` value
  set, built once per referent). **Step 4: verify pass. Step 5:**
  regenerate transform-adopt parity fixtures and any goldens the suite
  flags; run `go test ./go/internal/tfrender/ ./go/cmd/iw/`; commit
  (`tfrender: derive bindings from tokens directly`).

### Task 3: envgen — render purity: readers and resolvers only

> **Reframed by the maintainer mid-execution (governs over anything
> contradicting it elsewhere in this plan):** the renderer must not
> generate or own any lookup maps. It emits only remote-state readers and
> resolver expressions. All key→ID truth lives in the producing state
> outputs. No semantic sidecars, no locally maintained maps, render mode
> only.

**Files:**
- Modify: `go/internal/envgen/environment_generator.go`,
  `go/internal/envgen/expression_bindings.go`
- Test: `go/internal/envgen/state_aware_test.go`,
  `environment_generator_test.go`

**Interfaces:**
- Produces: for any root whose loaded config contains reference tokens,
  gen-env emits (a) the `terraform_remote_state` reader for each
  referent root and (b) **lookup-first resolver expressions**
  (maintainer's preferred ordering — state truth wins whenever it
  exists, the sidecar literal is purely a fallback that retires
  naturally as referents get applied):

  `try(data.terraform_remote_state.<referent_root>.outputs.infrawright_reference_ids.<referent>["<key>"], local.<book>.<referent>["<key>"])`

- The fallback arm reads the committed lookup sidecar **at plan time**
  via a `fileexists()`-guarded `jsondecode(file(...))` local — the
  renderer emits only expressions and never inlines a value from any
  map (render purity holds). The sidecar gains a producer-side
  `id_by_key` map (trivial extension in the compile step that already
  writes `key_by_id`), so the plan-time expression indexes directly by
  key instead of inverting in HCL.
- Failure semantics (measured 2026-07-30): try() arm order is
  irrelevant to the null-state case — the reader is read during plan
  regardless, and an absent LOCAL state file hard-errors the read
  (`defaults` does not rescue it). On azurerm a missing blob is
  expected to read as empty state (matching `state pull`'s
  synthesized-empty behavior), in which case the fallback arm serves
  unapplied referents automatically and this design is complete with no
  unknown failure modes. **CONFIRMED downstream (tf-zscaler, 2026-07-30):
  the azurerm reader on a missing blob reads as empty state; remote state
  resolves correctly when applied, and the book fallback served
  unapplied referents non-blocking.** The probe-gated reader-emission
  contingency is retired unused. Local-backend bootstrap = apply
  referent first or accept the loud error (documented corner, and the
  CHANGELOG must say so — flagged by the adversarial review).

- [ ] **Step 1: failing tests.**
  - tokenised config → emitted local resolves every token leaf via the
    remote-state expression; reader blocks present; **no sidecar file is
    read** (fixture provides none)
  - root with tokens never passes bare `var.<name>` to the module
    (the `environment_generator.go:458` passthrough is unreachable when
    tokens exist)
  - token whose referent root is outside the topology → refused with
    the existing malformed-evidence error class (validation gates
    unchanged)
  - untokenised (old-shape) config → byte-identical output to today
    (no-flag-day regression; bindings-file path keeps working as the
    migration bridge)
- [ ] **Step 2: verify failure. Step 3: implement.** Token scan over
  loaded config items (same reference-leaf classification as the
  derivation layer); resolver emission through the existing
  expression-binding machinery — tokens are just another source of
  bindings whose expressions are computed from the token itself, no map
  consulted. **Step 4: verify pass + package suite. Step 5: commit**
  (`envgen: resolve reference tokens against remote state only`).

### Task 4: guards and display

**Files:**
- Modify: `go/internal/tfrender/transform_artifacts.go`
  (`RemoveLookupWhenAbsent` path; `deriveHclComments`/`displayFor`)
- Test: alongside existing publish/comment tests

- [ ] **Step 1: failing tests.** (a) sidecar removal is refused (loud
  error naming the dependents) while any committed artifact in the tenant
  holds tokens for that referent; (b) HCL comments for a tokenised value
  resolve token → key → display name via the sidecar instead of
  `<unknown>`.
- [ ] **Step 2-4: implement, verify, package suite.**
- [ ] **Step 5: commit** (`tfrender: guard the sidecar tokens depend on;
  token-aware display comments`).

### Task 5: promotion gates and release surface

- [ ] `gofmt -l go/` empty; `go vet ./go/...` clean; `go test ./go/...`
  green; `make check-core`.
- [ ] CHANGELOG: the shape change, the no-flag-day migration (next
  transform/adopt rewrites IDs→tokens; `assert-adoptable` stays green;
  rename-churn note), the loud failure modes.
- [ ] Docs: `docs/terraform-expression-bindings.md` and
  `docs/state-topology.md` gain the token contract; spec/plan
  cross-links.
- [ ] Self-review against the spec's invariants; then STOP at "ready for
  adversarial review" per AGENTS.md and dispatch the reviewer per PR.
