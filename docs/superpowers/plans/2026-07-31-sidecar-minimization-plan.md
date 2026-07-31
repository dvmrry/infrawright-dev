# Sidecar Minimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Companion to `docs/superpowers/specs/2026-07-31-sidecar-minimization-design.md`.
Branch from main AFTER PR #297 merges. One PR, two logical commits (Part A,
Part B). AGENTS.md Validation Promotion applies: focused failing regression
before each production edit; full suites at the gate. Adversarial review
required before merge.

## Task A1: envgen render-derivation with committed-file bridge

**Files:** `go/internal/envgen/environment_generator.go` (loadBindingLayers
area), new tests in `go/internal/envgen/reference_resolvers_test.go`.

- [ ] Failing tests: (1) tokenised fixture WITHOUT a committed bindings
  file renders `expression_bindings.tf` byte-identical to the same fixture
  WITH the (token-derived) committed file — the parity pin; (2) committed
  file present but stale still wins the bridge (today's behaviour pinned);
  (3) old-shape (raw-ID) config without a bindings file renders exactly
  today's output (no derivation from IDs at render — raw-ID derivation
  stays transform-only, so render output never depends on lookup contents
  for old-shape trees).
- [ ] Implement: in `loadBindingLayers`, when the generated-bindings file
  is absent and the member's config carries tokens, build the
  `BindingContext` (pack references via `transform.MergedTransformReferences`,
  `SetBlockFields` from the loaded resource schema exactly as
  `transformrun.transformBindingContext` builds it — extract/share that
  helper rather than duplicating it, `ResourceRoots` from the topology,
  mode from the deployment) and call `tfrender.DeriveGeneratedBindings`
  over the loaded items with key maps via `resolveLookup`. Derivation
  notes flow to the existing diagnostics channel.
- [ ] Package suite green; commit
  (`envgen: derive bindings from tokens at render when no cache is committed`).

## Task A2: transform/adopt stop writing the cache

**Files:** `go/internal/tfrender/transform_artifacts.go` (compile/publish),
tests beside the existing publish tests.

- [ ] Failing tests: publish of a tokenised compile writes NO
  `.generated.expressions.json` and stale-cleans a pre-existing one
  (`removed` list names it); the derived `Binding` result is still
  computed in-memory (the minted-coverage assert and totality inputs
  still function).
- [ ] Implement: drop the bindings artifact from the publish/batch
  mutation lists; add the path to stale cleanup. Keep
  `DeriveGeneratedBindings` itself — it is the shared derivation engine
  for compile-time asserts and render-time derivation.
- [ ] Regenerate fixtures (v2 authority tree and demo overlay lose their
  `.generated.expressions.json` files); audit churn = deletions only.
- [ ] Full-suite gate; commit
  (`tfrender: stop committing the derivable bindings cache`).

## Task B1: lookups to `config/<tenant>/lookups/` with dual-read

**Files:** `go/internal/tfrender/transform_artifacts.go`
(`ComputeTransformArtifactPaths`, `resolveLookup`, publish stale-clean),
`go/internal/envgen/environment_generator.go` (`referenceBookLocals` path
resolution), `go/internal/roots/scopepaths.go` (config matcher depth),
tests in each package.

- [ ] Failing tests: (1) paths: `Lookup` lands under `lookups/`;
  (2) dual-read: a lookup at the legacy path still resolves for derivation,
  comments, and the emitted `file()` expression points at the path that
  exists; (3) publish writes the new path and stale-cleans the legacy
  file; (4) scopepaths attributes `config/<tenant>/lookups/<type>.lookup.json`
  to the type (depth-3 config shape).
- [ ] Implement; regenerate fixtures (lookups relocate in demo + authority
  trees).
- [ ] Full-suite gate; commit
  (`tfrender+envgen+roots: lookups live in config/<tenant>/lookups`).

## Task C: docs, CHANGELOG, review

- [ ] CHANGELOG: the two-file end state, the bridge/no-flag-day migration
  (committed caches win until re-transform removes them; lookups dual-read),
  and the one-breath story. Update
  `docs/terraform-expression-bindings.md` and the reference-tokens spec's
  out-of-scope pointer.
- [ ] gofmt/vet/full `go test ./...`/`make check-core`; adversarial review
  (fresh context) before un-drafting the PR.
