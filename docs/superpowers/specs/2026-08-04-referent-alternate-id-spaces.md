# Referent Alternate ID Spaces

Design for reference edges whose referrers cite a referent attribute other
than the resource's `id`. Motivating case: ZIA URL categories carry two
provider-modeled identifiers — `id` (string, `"CUSTOM_56"`, what the
endpoint and Terraform resource key on) and `val` (number, Computed, set on
Read; a stable API-assigned sequence with holes, not a positional index).
`zia_url_filtering_rules.url_categories` is `set(string)` and cites `id`
directly; `zia_dlp_web_rules.url_categories.id` is `set(number)` and cites
`val`. Same referent, two ID spaces, and the engine hardcodes one of them
in three independent places:

1. Sidecar keys: `lookupIdentity(merged["id"])`
   (`tfrender/transform_artifacts.go:464`), so `key_by_id`/`id_by_key` are
   always keyed by `id`.
2. Cross-state output: the referent root publishes
   `{ for key, item in module.<type>.items : key => item.id }`
   (`envgen/environment_generator.go:394`), so `iw_reference_ids` resolves
   to `id`.
3. Edge grammar: a reference spec carries `referent` and `name_field` only
   (`metadata/packs.go:460`); there is no way to say which referent
   attribute the referrer's values are drawn from.

A `val`-citing edge therefore never tokenizes — declared edge, correct
`name_field`, transform data present, and every value stays a literal ID.
Downstream impact when filed: 26 of 182 references on the DLP web rules of
one downstream tenant, blocking clean adoption of that type. The gap
generalizes: any provider that exposes a resource under one identifier but
references it by another hits this; Zscaler does it at least twice (the
`set(string)` vs `set(number)` split on the same field name).

## Hard invariants

1. An edge can declare which referent attribute its values cite. With the
   declaration in place, those values get the full reference treatment end
   to end: name-keyed tokens in committed config, sidecar decode, binding
   derivation, and plan-time resolution to the cited attribute's value.
2. Absent declaration means `id`. Every existing pack, sidecar, binding,
   and generated root is byte-identical.
3. Tokens stay `<referent>.<key>`. One key space per referent; which space
   an edge cites changes what the token decodes TO, never the token.
4. Resolution failures stay loud and fail-closed: an edge citing a space
   the referent's committed sidecar does not carry derives nothing and is
   reported through the existing missing-lookup skip class; it never binds
   past or mis-resolves.

## Design decisions

- **The edge selects the space; the referent publishes the union.** The
  reference spec gains an optional `referent_id_field` (default `"id"`).
  The set of spaces a referent publishes is the union of
  `referent_id_field` values across its active edges. The variance lives in
  the referrer's provider schema (that is where `set(string)` vs
  `set(number)` split), so the declaration belongs on the edge; but the
  sidecar and outputs are per-referent shared artifacts, so publication is
  the union and both citing forms share one sidecar.
- **Sidecar shape is additive.** The canonical `by_id` / `id_by_key` /
  `key_by_id` maps stay exactly as they are (id space). A referent with
  declared alternate spaces additionally publishes
  `"spaces": {"<field>": {"id_by_key": {...}, "key_by_id": {...}}}`.
  Readers that predate the field ignore it; the strict parser gains the
  optional key. The builder already has the data: sidecar rendering merges
  `Originals` (the raw fetch payload) over the projection, and
  `lookupIdentity` already canonicalizes integer identities.
- **Output shape is a sibling, not a mutation.** The referent root
  publishes `iw_reference_ids_<field>` (e.g. `iw_reference_ids_val`) next
  to `iw_reference_ids`, one map per declared space, built from the same
  `module.<type>.items` value (the module already exposes the whole
  resource; no modulesgen change). The existing `iw_reference_ids`
  contract — including saved-plan reference authorization — is untouched.
- **Resolver parity.** Bindings for a `<field>`-citing edge emit the
  canonical selector against `iw_reference_ids_<field>`; envgen emits a
  per-space lookup local (`iw_reference_lookup_<referent>_<field>`) from
  the sidecar's space maps and applies the same
  `try(remote-state, lookup)` rewrite and lookup-only policies.
- **Generated referents only (v1).** `referent_id_field` on an edge whose
  referent is a data referent is refused at pack validation. Data-referent
  alternate spaces are expressible later through the same surface if a
  case appears.
- **Adoption identity is untouched.** Import IDs, moves, and the oracle
  continue to use the resource's own identity metadata; alternate spaces
  exist only on the reference-resolution path.

## Surfaces (the union, enumerated)

1. `metadata/packs.go`: edge grammar `{referent, name_field,
   referent_id_field}`; identifier-safe field name; data-referent refusal.
2. `tfrender` sidecar render + parse: per-space maps from the merged
   originals; `TransformLookupData` gains `Spaces`; old sidecars parse
   unchanged (absent spaces = declared-space edges report missing lookup).
3. `tfrender` compile: `lookupKeyMaps` keyed by (referent, space);
   `substituteReferenceTokens` and `DeriveGeneratedBindings` select the
   edge's space; `bindValue` emits the per-space selector. Minted-token
   coverage and lookup-key-stranding gates operate on keys and are
   unchanged.
4. `envgen`: referent-root output emission (site 2 above), per-space
   lookup locals, selector pattern and resolver rewrite, render-time
   binding derivation (shared `TransformBindingContext`, so transform and
   gen-env stay identical by construction).
5. `assessment`/saved-plan: reference authorization admits the sibling
   outputs for generated referents through the same planned-values
   evidence path; published schema updated if it enumerates output names.
6. Pack work (rides after the engine): declare `referent_id_field: "val"`
   on the `val`-citing ZIA rule-type edges to `zia_url_categories`.
7. Docs: pack-authoring grammar, terraform-expression-bindings, a note in
   adoption-command-surface.

## Acceptance criteria (engine)

- A synthetic pack with one referent and two referrers — one citing `id`,
  one citing an alternate numeric space — tokenizes both referrers to the
  SAME tokens, and gen-env resolves each through its own space: the `id`
  referrer via `iw_reference_ids` and the alternate via
  `iw_reference_ids_<field>`, both with committed-lookup fallback.
- A declared-space edge against a sidecar without that space derives
  nothing, reports the missing lookup, and leaves values literal.
- Every existing test passes byte-identically with no pack declaring the
  field (default-`id` no-op proof).
- Pack validation refuses `referent_id_field` on data-referent edges and
  non-identifier field names.

## Out of scope (v1)

- Data-referent alternate spaces.
- Publishing spaces not declared by any active edge.
- Cross-pack references (tracked separately; orthogonal).
- Migrating any live tenant's committed config (downstream re-transform
  picks tokens up once the pack declares the space).
