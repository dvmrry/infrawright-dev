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
   - **Added after adversarial review:** those gates were all keyed on the
     CURRENT pack's reference edges, which the committed cache used to
     backstop — a stale cache's selector reached
     `validateRemoteStateReferences` and was refused as an undeclared edge.
     With no cache committed, a retired edge made every token gate skip the
     field, so gen-env additionally runs an edge-independent scan over
     *every* string leaf of each member's config and refuses any value that
     decodes as a token no current edge governs. Book membership (prefix
     names a type with a committed book, remainder is a key that book
     decodes) is the disambiguator that keeps an innocent dotted string
     innocent. The same rule governs the HCL-format refusal, which must not
     fire on a legitimate dotted value at a field no book decodes.
   - **Book membership is only sound because the producer keeps it sound.**
     Two guards make "a decodable token stays decodable" an enforced
     invariant: `tokenDependents` refuses to RETIRE a book while committed
     configs reference its type, and `assertNoBookKeyStranding` refuses to
     publish a book update that would DROP a key committed configs still
     name. The second exists because the first was not enough — an ordinary
     referent re-transform (item renamed or deleted) shrinks the key set
     without removing the book. That guard is scoped to the current run:
     referents are transformed before their referrers, so a dependent this
     run also rewrites is transient, and refusing on it would deadlock
     (transforming the referrer first re-mints the same key from the
     still-committed book).
4. **Raw-ID derivation stays transform-only.** The derivation trigger is per
   resource type, so one tokenised item pulls its raw-ID siblings through the
   deriver too. Render-derivation therefore runs with
   `BindingContext.TokensOnly`, which skips a raw ID *without consulting the
   book* — otherwise a later referent-only transform that added that ID to
   the book would silently replace a committed literal with a resolver, with
   no re-transform of the referrer and no way for state/book disagreement to
   be caught.
5. `roots/scopepaths` keeps recognising the suffix during migration
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

- For an all-token config, emitted roots for an unchanged tree are
  byte-identical whichever path produced the bindings (bridge vs derived) —
  pinned by the parity test. For a MIXED config (tokenised and raw-ID leaves
  side by side) the two paths differ, correctly and by design: the committed
  cache carries whatever raw-ID bindings the transform that wrote it derived,
  and the bridge keeps serving them; render-derivation emits token bindings
  only, leaving raw-ID leaves as the literals they are. Bridge-wins is the
  migration contract; derived-emits-tokens-only is the render-purity
  contract; a mixed tree reconciles the two by re-transforming.
- A referrer's emitted root never changes because a referent's book changed.
  Only a re-transform of the referrer rewrites the referrer's leaves.
- A tree mid-migration (old bindings file present, book at legacy path)
  renders exactly as today.
- The book's plan-time `file()` path always names a file that exists at
  generation time; a missing book keeps failing loudly through the
  existing totality/refusal gates, never silently.
- A key that some committed config still names by token cannot leave that
  type's book through any pipeline operation — neither by the book being
  retired nor by the key being dropped from a book that survives.

## Accepted residuals

These are named rather than guarded, because the guards above cover the
pipeline's own writes and nothing else:

- **A book deleted or edited by hand.** Nothing in the pipeline can prevent
  it. Once the book is gone, a committed token for a type whose pack
  reference edge has ALSO been retired is indistinguishable from an ordinary
  dotted string, and detection degrades to the pack metadata that no longer
  mentions the field. With the edge still declared, the ordinary totality
  gate still refuses.
- **A dependent skipped mid-run.** The key-shrinkage guard treats every type
  in the run's scope as a dependent this run will repair. A type whose
  transform is then skipped for want of a pull file is not repaired, and its
  stale token survives that run.

## Out of scope

- Removing the `.generated.expressions.json` suffix from scopepaths (after
  downstream trees are clean).
- Any change to operator overlays, tokens, resolvers, probes, or the
  JSON-only contract.
