# Sidecar minimization: retire the bindings cache, shelve the books — design

Follows the reference-tokens work (PR #297). Maintainer direction: fewer
committed files per type; lookups live in a dedicated subdirectory within
the config directory.

## End state (the one-breath story)

Per resource type, the committed surface is: the config
(`<type>.auto.tfvars.json`, carrying tokens), the book
(`lookups/<type>.lookup.json`), and — rarely — a hand-written operator
overlay (`<type>.expressions.json`). Nothing else.

## Part A: retire `.generated.expressions.json`

With tokens in the config, the derived binding is a pure function of
(token, pack edges, schema) — the committed bindings file is a cache of
something the renderer can compute. Changes:

1. **gen-env derives at render.** Where today `loadBindingLayers` reads the
   committed generated-bindings file, envgen instead calls the same
   `tfrender.DeriveGeneratedBindings` over the loaded config items, with a
   `BindingContext` built from pack references and the loaded resource
   schemas (envgen already loads both), and key maps from the books
   (gating/derivation input, consistent with the settled purity contract).
   Identical derivation code path = identical expressions by construction;
   a parity test byte-compares the emitted `expression_bindings.tf` between
   the bridge path and the derived path over the same fixture.
2. **Migration bridge, no flag day.** A committed
   `.generated.expressions.json` still present wins (today's exact path) so
   downstream trees keep working untouched; when absent, render-derivation
   serves. Transform/adopt stop *writing* the file and stale-clean any
   committed copy on their next run (the publish step's existing
   stale-artifact machinery), so the file disappears tenant-by-tenant as
   they re-transform.
3. **Gates unchanged.** The leaf-granular totality gate, foreign-token
   refusal, disabled-mode refusal, and JSON-only contract all operate on
   whichever binding set is in effect.
4. `roots/scopepaths` keeps recognising the suffix during migration
   (committed copies exist downstream); removal of the suffix is a later
   cleanup once downstream trees are clean.

## Part B: books move to `config/<tenant>/lookups/`

1. `ComputeTransformArtifactPaths.Lookup` becomes
   `<configDir>/lookups/<type>.lookup.json`; the publish step writes there
   and stale-cleans a legacy `<configDir>/<type>.lookup.json`.
2. **Dual-read everywhere** during migration: `resolveLookup` (and the
   render-side book-path resolution for the emitted `file()` expression)
   prefer the new path and fall back to the legacy one. The emitted book
   local must point at the path where the book actually exists, or the
   fallback arm dies at plan time on a tree that has not re-transformed.
3. `roots/scopepaths`: the config-directory matcher currently assumes
   `<tenant>/<file>` (depth 2); it gains the `<tenant>/lookups/<file>`
   shape so changed-path scoping keeps attributing book edits to the type.
4. The retirement guard (`tokenDependents`) scans tfvars only — unaffected.

## Invariants

- Emitted roots for an unchanged tree are byte-identical whichever path
  produced the bindings (bridge vs derived) — pinned by the parity test.
- A tree mid-migration (old bindings file present, book at legacy path)
  renders exactly as today.
- The book's plan-time `file()` path always names a file that exists at
  generation time; a missing book keeps failing loudly through the
  existing totality/refusal gates, never silently.

## Out of scope

- Removing the `.generated.expressions.json` suffix from scopepaths (after
  downstream trees are clean).
- Any change to operator overlays, tokens, resolvers, probes, or the
  JSON-only contract.
