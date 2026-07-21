# Singleton-state topology v2 (degrouping roadmap)

Status: AUTHORIZED AND IN PROGRESS. G0 completed the authority handoff on
2026-07-20; implementation is **Go-only** (§3 D6).
The original revision of this document scheduled Node-first parcels
(N1/N2) with an oracle re-pin; that assumed the seven authoring
commands would remain Node indefinitely. The product goal is now full
Node archival: the authoring surface is being ported to Go against the
current frozen Node behavior, so changing Node's topology first would
mean implementing degrouping twice and then archiving the copy we just
changed. Instead, degrouping lands once, directly in Go, after Go
formally becomes the product authority. That gate is now complete.

This document supersedes the "Post-cutover simplification candidate:
retire logical slug grouping" section of
[go-runtime-plan.md](go-runtime-plan.md), which is now stale in two
ways: it assumed the removal would land in Node, and it deferred the
auto-vs-full decision on an oracle-batching counterweight that was
since rejected (terraform-exec review, D1) and never implemented.

## 1. Decision summary

Both grouping mechanisms — automatic slug grouping (`strategy: "slug"`)
and explicit groups — are retired in full. The topology becomes
singleton-state: every generated resource type is its own state unit.

A small internal "state unit" structure is retained with this frozen
invariant, so plan/report/adopt APIs that consume a root-with-members
shape do not need rewriting:

```
state label  == resource type
members      == [resource type]        (always exactly one)
backend key  == <tenant>/<resource-type>.tfstate
```

Cross-state references become the default resolution for declared pack
relationships. Reference cycles fail pack validation. Grouping is no
longer offered as an escape hatch for anything.

## 2. Why now, and why full removal

- **The window is still open.** No persistent grouped state exists: the
  checked-in deployment defaults to one resource per root, the
  Kubernetes qualification state was disposable, and the Zscaler test
  applies ran under singleton roots. After the first production Apply
  under a grouped root, removal becomes a controlled state migration
  forever. (§6 still mandates the inventory that proves this.)
- **Measured surface.** The v1 catalog carries 27 multi-type slug
  groups covering 103 of 151 generated types (largest:
  `zpa_policy` 16, `zia_firewall` 8, `zpa_application` 7). Module
  selection, envgen, staging, and scope resolution all carry many-member
  logic to serve configurations nobody runs.
- **Full, not partial.** Removing only automatic slug derivation keeps
  nearly all plumbing alive via explicit groups. The one recorded
  counterweight — batching oracle work by logical root — was rejected on
  containment grounds and its successor plan (accepted-plan mode, plugin
  cache) does not depend on multi-member roots. Nothing else pays for
  keeping explicit groups.

## 3. Decisions

### D1 — Retirement scope
`strategy` (both values), explicit `groups`, and slug derivation are
removed from the deployment schema, roots derivation, catalog build,
and all consumers. The catalog's `slug_label` field (the grouping key)
is removed. The internal state-unit struct keeps a one-element
`members` list per the §1 invariant; code paths that iterate members
remain valid and become trivially single-iteration.

### D2 — Cross-state references become the default
Today `cross_state_references` and `bind_references` are mutually
exclusive per-provider opt-ins; with neither set, declared references
fall back to literal IDs. After degrouping:

- Declared pack reference edges (currently 7: 4 ZPA, 1 ZIA, 2 ZCC, all
  intra-provider) resolve via cross-state references **by default**.
- `cross_state_references: false` remains valid as an explicit opt-out,
  documented for backends where a root's principal cannot read sibling
  state; the fallback is literal IDs, exactly today's default behavior.
- `bind_references` (same-root co-location) is removed entirely — its
  mechanism cannot exist without multi-member roots.

### D3 — Cycle policy
The current reference graph is acyclic. Pack validation gains a
fail-closed cycle check over declared edges across state units: any
cycle fails validation naming the full cycle path, with remedy text
"resolve one direction via a literal ID or operator expression"
(today's `CYCLE_REMEDY` with the "or disable bind_references" clause
deleted). Grouping is never suggested as a remedy.

### D4 — Removed inputs fail explicitly; no compatibility period
`strategy`, `groups`, and `bind_references` in a deployment, and
`slug_group` in a pack registry, are schema violations that fail
validation with an error naming the removed field and pointing at this
document. Rationale: deployments are repo-controlled, there are no
external consumers, and a silent-ignore period would let a grouped
intent silently produce a different topology — the exact class of
surprise this project fails closed on. `cross_state_references: true`
remains accepted (now redundant) for one release, then warns.

### D5 — Schema versions
- Root catalog: new `catalogs/zscaler-root-catalog.v2.json`, with
  `schema_version` bumped and `slug_label` removed from resource
  entries. The Make variable `ROOT_CATALOG` moves with it. The frozen v1 file
  remains byte-immutable at its existing path only because the frozen Node
  build/rollback lane imports it; it is deleted at Node archive and is never a
  competing product authority.
- Deployment roots config: the code-owned validation contract becomes v2; no
  deployment schema version exists to bump and no new version field is added.
- Topology command outputs (`roots`, `plan-roots`, reports): shape
  unchanged (members arrays now always length 1). This is a deliberate
  API-stability choice, not an oversight.
- Backend keys: unchanged for every singleton root —
  `<tenant>/<resource-type>.tfstate` is already the derived key when
  label == type. Only grouped state (none known) would move.

### D6 — Authority handoff gate, then Go-only implementation
(Amended 2026-07-19; replaces the Node-first oracle re-anchoring
protocol.) Node is never modified for this change. The gate that
unblocks every parcel below:

1. **Authoring authority complete.** The six retained authoring commands
   (reconcile, openapi-map, source-operation-map, source-evidence-eval,
   provider-probe, transform-adopt-parity) are ported under
   [go-authoring-port-roadmap.md](go-authoring-port-roadmap.md). Existing
   OpenAPI-backed behavior passes against the current frozen Node corpus;
   the new source-first/OpenAPI-optional behavior passes its independently
   reviewed source-bound goldens. The version-specific
   `zpa-provider-evidence` command retires into the generic analyzer's
   frozen ZPA corpus, whose pack/schema/source binding checks stay green.
   The implementation portion of this step is complete and accepted after
   fresh adversarial review. The user's external Opus, GPT-5.6 Pro, and Fable
   sequence completed on 2026-07-20 against the candidate culminating at
   `c3e18a67e4b61b90860e02b782342b3e98ebbd80`, with no blocking findings.
   This step is complete.
2. **Final freeze.** The Node runtime is frozen as the immutable v1
   provenance oracle: tagged, bundle SHA recorded, and the full
   differential corpus (including the retiring ZPA validator) run green
   one final time at that commit. Node changes for no reason after this
   point. After the eventual archive the bundle is retained by digest as
   evidence, not executed in CI or shipped as a dependency.
   **Complete:** tag `node-oracle-v1-final` points to
   `047e39e5f2d0d0a1a5415587255200dea775ac0b`; the bundle/checksum/manifest
   digests and final green main run are recorded in
   [go-authority-handoff.md](review-handoffs/go-authority-handoff.md).
3. **Authority transfer.** Go is formally declared the product
   authority in [go-runtime-v2.md](go-runtime-v2.md). The single released
   `iw` binary must expose both the operator surface and all six retained
   authoring commands, with no executable Node fallback.
   **Complete:** [go-runtime-v2.md](go-runtime-v2.md) declares Go product-source
   authority. Release-default routing remains a separate cutover phase.

After the gate, degrouping is implemented directly in Go as a
versioned v2 change. Consequence, stated so nobody trips on it later:
the topology-dependent differential gates (`RootCatalog`, `Topology`,
`Generation`, and topology-touching corpus cases) **retire at the
first v2 parcel** — a v1-frozen Node cannot produce v2 output, so
byte authority transfers from the oracle to committed, adversarially
reviewed goldens generated by Go. Non-topology differential gates may
be retained against the frozen v1 oracle only where their behavior is
provably topology-independent; anything ambiguous retires with the
rest rather than pinning v1 behavior by accident.

## 4. Implementation parcels (Go-only, stacked, each independently reviewed)

- **G0 — Authority handoff gate (complete).** Not a code parcel: authoring
  parity, final Node freeze + tag + last full corpus run, authority
  declaration, differential-gate disposition recorded. Blocks all of
  the below.
- **G1 — Go degroup core (complete 2026-07-20).** `go/internal/deployment` (remove
  `strategy`/`groups`/`bind_references`, retain explicit true/false cross-state
  parsing, D4 errors),
  `roots` (derivation becomes identity over generated types),
  `metadata/{resources,rootcatalog}` (v2 emit, drop `slug_label`).
  Regenerate catalog v2 + demo goldens from Go; goldens reviewed as
  the new byte authority. Remove `slug_group` inputs from
  `packs/zia/registry.json`.
- **G2 — Reference promotion + cycle gate (in progress).** D2 defaulting is applied centrally
  through the deployment binding mode and wired through Transform and `envgen`
  as one artifact-semantic parcel; D3 cycle
  check with tests (synthetic cyclic pack fixture), stale-binding
  message text updated; scope-paths and plan-roots simplified;
  retiring topology-dependent differential gates lands here with the
  replacement golden gates.
- **G3 — Go lifecycle sweep.** `adopt`/staging (per-root memoization
  from `747f613` becomes per-type), `assessment/inputs`, plan/report
  consumers — expected mostly no-op thanks to the retained state-unit
  struct; the review verifies that expectation rather than assuming it.

Per standing workflow: Sonnet implementers, fresh adversarial review
per parcel, reviewer commits.

## 5. Qualification gates (all required before cutover proceeds)

1. Full-pack DAG: gen-env over the complete 151-type surface; module
   selection count and file tree recorded and compared against
   pre-change output (differences must be exactly the degrouping).
2. Artifact goldens: full golden regeneration reviewed as a diff, not
   rubber-stamped — every changed byte must be attributable to D1–D5.
3. Backend keys: derived key set over the full surface proven identical
   to the pre-change singleton key set.
4. Cross-state live: read → import → plan on the test tenant exercising
   at least one edge per provider family (ZPA, ZIA, ZCC) under the new
   default; no-op second plan.
5. Go lifecycle: re-run the Kubernetes disposable qualification
   (adopt → stage → plan --save → assert-adoptable → exact Apply →
   no-op) on the v2 topology.

## 6. State/backend inventory (before parcels land in production use)

Enumerate every backend/tenant in real use (the zscaler-as-code
adoption work is the known consumer). For each: list state keys; flag
any key not matching `<tenant>/<resource-type>.tfstate` and any state
holding more than one resource type. Expected result: none. If any
grouped state is found, the sanctioned migration is **re-adopt** into
singleton roots followed by a verified no-op plan — import-first
adoption makes this equivalent to, and safer than, cross-backend
`terraform state mv` surgery, which is reserved for cases where
re-adopt is impossible. Findings recorded here before G-parcels merge.

## 7. Post-change measurement

Re-run the performance comparison after qualification: degrouping is
expected to remove much of the module-generation amplification on its
own. The deferred performance items (no-op write suppression,
accepted-plan reuse/batching, plugin cache, fetch in-flight budgeting)
stay parked until these numbers exist.
