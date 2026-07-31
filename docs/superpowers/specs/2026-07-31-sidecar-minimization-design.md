# Sidecar minimization: retire the bindings cache, shelve the lookups — design

Follows the reference-tokens work (PR #297). Maintainer direction: fewer
committed files per type; lookups live in a dedicated subdirectory within
the config directory.

## End state (the one-breath story)

Per resource type, the committed surface is: the config
(`<type>.auto.tfvars.json`, carrying tokens), the lookup
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
   schemas (envgen already loads both), and key maps from the lookups
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
     decodes as a token no current edge governs. Lookup membership (prefix
     names a type with a committed lookup, remainder is a key that lookup
     decodes) is the disambiguator that keeps an innocent dotted string
     innocent. The same rule governs the HCL-format refusal, which must not
     fire on a legitimate dotted value at a field no lookup decodes.
   - **Lookup membership is only sound because the producer keeps it sound.**
     Two guards make "a decodable token stays decodable" an enforced
     invariant: `tokenDependents` refuses to RETIRE a lookup while committed
     configs reference its type, and `assertNoLookupKeyStranding` refuses to
     publish a lookup update that would DROP a key committed configs still
     name. The second exists because the first was not enough — an ordinary
     referent re-transform (item renamed or deleted) shrinks the key set
     without removing the lookup. That guard exempts nobody and is scoped to
     one config directory. Two exemptions were tried and both were unsound:
     types the same run also selected (the runners publish each type
     immediately and independently and continue past a later member that
     skips or fails, so the exemption published the new lookup and never
     repaired the dependent), and the compiling type's own config (justified
     by "a type never mints a self-reference", which is about MINTING, while
     gen-env classifies tokens by lookup membership over every string leaf with
     no own-config exception). The own config is now checked on both sides of
     the write — committed on disk and pending output — because the lookup is
     written first and non-transactionally. Removing the exemptions
     reintroduces the referent-rename deadlock, which is accepted below.
4. **Raw-ID derivation stays transform-only.** The derivation trigger is per
   resource type, so one tokenised item pulls its raw-ID siblings through the
   deriver too. Render-derivation therefore runs with
   `BindingContext.TokensOnly`, which skips a raw ID *without consulting the
   lookup* — otherwise a later referent-only transform that added that ID to
   the lookup would silently replace a committed literal with a resolver, with
   no re-transform of the referrer and no way for state/lookup disagreement to
   be caught.
5. `roots/scopepaths` keeps recognising the suffix during migration
   (committed copies exist downstream); removal of the suffix is a later
   cleanup once downstream trees are clean.

## Part B: lookups move to `config/<tenant>/lookups/`

1. `ComputeTransformArtifactPaths.Lookup` becomes
   `<configDir>/lookups/<type>.lookup.json`; the publish step writes there
   and stale-cleans a legacy `<configDir>/<type>.lookup.json`.
2. **Dual-read everywhere** during migration: `resolveLookup` (and the
   render-side lookup-path resolution for the emitted `file()` expression)
   prefer the new path and fall back to the legacy one. The emitted lookup
   local must point at the path where the lookup actually exists, or the
   fallback arm dies at plan time on a tree that has not re-transformed.
3. `roots/scopepaths`: the config-directory matcher currently assumes
   `<tenant>/<file>` (depth 2); it gains the `<tenant>/lookups/<file>`
   shape so changed-path scoping keeps attributing lookup edits to the type.
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
- A referrer's emitted root never changes because a referent's lookup changed.
  Only a re-transform of the referrer rewrites the referrer's leaves.
- A tree mid-migration (old bindings file present, lookup at legacy path)
  renders exactly as today.
- The lookup's plan-time `file()` path always names a file that exists at
  generation time; a missing lookup keeps failing loudly through the
  existing totality/refusal gates, never silently.
- A key that some committed config still names by token cannot leave that
  type's lookup through any pipeline operation — neither by the lookup being
  retired nor by the key being dropped from a lookup that survives. No
  dependent is exempt: not one the same run also selects, and not the
  compiling type's own config, which is checked both as committed on disk
  (the lookup is written before the config, non-transactionally, so a failure
  between the two strands it) and as this compile's pending output (the
  steady state after a successful publish). An exemption is only sound
  inside a successfully preflighted, rollback-capable publication
  transaction, and no invocation path here provides one.

## The HCL lane

Tokens are a JSON-tfvars contract, so an HCL config is refused rather than
leaf-verified. Two rules, because only one of them can be checked:

1. **A value a committed lookup decodes is refused.** Prefix names a type with
   a lookup, remainder is a key that lookup decodes. Membership is checkable, so
   it is checked, and an ordinary dotted string at any field is left alone.
2. **A `<referent>.`-prefixed value at a member that DECLARES a reference
   edge to a lookup-less referent is refused on shape alone.** Membership is not
   checkable without a lookup, and staying silent would let the token ride
   `var.<items>` to a string-typed provider field with no gate having run.
   Confinement is MEMBER-level, not field-level: unparsed HCL cannot say which
   field a quoted value sits at, so what narrows the surface is the candidate
   set — only referents that fields the current pack declares on that member
   point at, and only those with no lookup anywhere. A member declaring no such
   edge is never scanned by this rule, and every other config is untouched.

Together these keep the invariant "an active edge always triggers the token
gates" true for HCL as well as for structurally scanned JSON.

## Accepted residuals

These are named rather than guarded:

- **A lookup deleted or edited by hand.** Nothing in the pipeline can prevent
  it. Once the lookup is gone, a committed token for a type whose pack
  reference edge has ALSO been retired is indistinguishable from an ordinary
  dotted string, and detection degrades to the pack metadata that no longer
  mentions the field. With the edge still declared, the token gates still
  refuse — through the ordinary totality gate for JSON, and through the HCL
  lane's rule 2 for HCL.
- **The referent-rename deadlock.** Renaming or deleting a referent item
  drops its key, and every committed dependent still names it, so the
  referent's transform refuses. Re-transforming the dependent first does not
  help: it re-mints the same departing key from the still-committed lookup. The
  operator must break the tie by hand — update the dependent's committed
  reference to the new key, or delete that config and re-transform it after
  the referent succeeds. This is accepted deliberately. The alternative was a
  selection-scoped exemption, which was silent and left the tree stranded
  whenever a later selected member skipped or failed; loud and
  self-consistent beats quiet and stranded.

## Out of scope

- Removing the `.generated.expressions.json` suffix from scopepaths (after
  downstream trees are clean).
- Any change to operator overlays, tokens, resolvers, probes, or the
  JSON-only contract.
