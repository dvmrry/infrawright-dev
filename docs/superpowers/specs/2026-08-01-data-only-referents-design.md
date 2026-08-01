# Data-Only Referents

Design for expressing tenant objects that exist only as Terraform data
sources — no manageable resource — as first-class reference targets in
the engine. Motivating case: ZIA location groups. `zia_location_management`
is generated, but location groups are exposed by the zscaler/zia provider
only as a data source, so today `location_groups.name` sits in
`acknowledged_drops` for four rule types (`zia_url_filtering_rules`,
`zia_cloud_app_control_rule`, `zia_dlp_web_rules`,
`zia_ssl_inspection_rules`) and `static_location_groups.name` /
`dynamiclocation_groups` are dropped on `zia_location_management` itself.
Rules keep raw tenant IDs as opaque literals: no lookup, no name
comments, no tokens, no cross-tenant portability.

## Hard invariants

These are the contract; mechanisms below serve them.

1. Referrer fields can declare a data-only referent with the existing
   reference grammar and get the full reference treatment: name-keyed
   tokens in committed config, lookup sidecars, HCL name comments, and
   render-time binding derivation.
2. Data-only lookups never share state with managed roots. A data-only
   referent gets its own root and its own state file; refreshing it can
   never contend with or lock a managed root's state.
3. A data-only surface is always its own registry type. When a
   conceptual object has both a managed resource and a data source
   (e.g. custom vs predefined ZIA URL categories), they are two types,
   two roots, two lookups. They never merge. A reference names exactly
   one referent type.
4. Resolution failures are loud. A referenced group that vanished from
   the tenant fails the data root's plan/apply with a data-source
   lookup error; it must never silently misresolve.
5. Freshness is event-driven, not schedule-driven. Because nothing in
   the managed lifecycle naturally touches a data root, correctness
   comes from refreshing data leaves when their referrers are planned
   or applied (see Refresh strategy); any scheduled activity is
   visibility-only.

## Architecture

The design's core move: a data-only referent is an ordinary root that
fulfills the same output contract as a generated root, so every
downstream consumer — token minting, binding derivation,
`terraform_remote_state` wiring, the `try(remote_state, lookup)`
resolver, state-aware fallback, the totality gate — is untouched. Only
the machinery that produces the root's contents changes.

### Pack surface

- Registry: a data-only type is an entry with `fetch` and no
  `generate`, marked `"data_referent": true`:

  ```json
  "zia_location_groups": {
    "data_referent": true,
    "fetch": {"pagination": "zia", "path": "locations/groups"},
    "product": "zia"
  }
  ```

  (The fetch path shown is illustrative; the real endpoint is a pack
  qualification detail.)
- References: unchanged grammar on the referrers, e.g.
  `"location_groups.id": {"referent": "zia_location_groups",
  "name_field": "name"}`.
- Schemas: binding-path validation against a data-only referent reads
  the provider schema's `data_source_schemas` section. Whether the
  vendored schemas carry that section is a verification item; vendoring
  it where missing is pack work, not engine work.

### Transform lane

Fetch all objects of the type — the data root enumerates the tenant's
full set, not just currently-referenced keys — and mint the standard
name-keyed `items` tfvars plus the lookup sidecar. No imports file and
no moves: nothing is managed. Default drop policy is aggressive by
construction: keep only what the data source needs to instantiate
(the name field); everything else is dropped.

### Module and root

Modulesgen emits a data module instead of a resource module:

```hcl
data "zia_location_groups" "items" {
  for_each = var.items
  name     = each.value.name
}

output "iw_reference_ids" {
  value = { zia_location_groups = { for k, d in data.zia_location_groups.items : k => d.id } }
}
```

Per-key `for_each` (rather than one list-all read, even if the provider
offers one) is deliberate: a missing group fails precisely, naming its
key, and outputs stay keyed. The data source's instantiation argument
(`name` above) is declared per-type in the pack's existing
`lookup_sources` surface (which already carries per-type `name_field`),
not inferred from any one referrer's reference spec. The type joins
root topology as an ordinary singleton root with its own state.

### Operational semantics

- Applying the data root snapshots the tenant's groups into its state;
  referrers resolve through that snapshot with committed-lookup
  fallback (existing state-aware machinery, unchanged).
- Applying a data root is safe by construction: its plan can never
  create or destroy anything; it can only refresh the snapshot.
- `terraform plan` on the data root is the drift detector for
  data-only objects: output-only diffs mean the tenant's set changed.

## Refresh strategy

Two triggers with different owners:

1. **Refresh-on-use (correctness, HARD).** When a root that references
   data-only leaves is planned or applied, those leaves are refreshed
   (applied) first. The dependency graph already exists in the engine —
   the cross-state reference topology enumerates referrer-root ->
   referent-root edges — so no discovery is needed; ordering is a
   consumption of existing data.
2. **Scheduled drift visibility (optional).** A periodic `plan` (never
   apply) over data roots surfaces tenant-side changes when no referrer
   activity would. Visibility only; correctness never depends on it.

**OPEN — mechanism for trigger 1** (deliberately undecided; CI-side
thinking in progress):

- (a) Engine-emitted ordering: the plan-roots surface exposes each
  selected root's data-leaf referents machine-readably; CI sequences
  "apply data leaves, then plan/apply referrers". Keeps plan read-only;
  CI owns execution order.
- (b) Engine-orchestrated: `iw plan` over a referrer automatically
  applies its data-leaf referents first, justified by apply-safety by
  construction. Simplest CI, but a plan command that writes (data-root)
  state violates plan-is-read-only expectations.
- (c) Staleness gate: plan refuses (or loudly warns) when a referenced
  data root's snapshot differs from a fresh data-root plan. Keeps
  plan read-only and forces the refresh to be explicit.

Leaning (a), possibly with (c)'s warning as a complement; decision
deferred to the CI/CD design.

## Acceptance criteria (engine)

- Plan classification and assessment accept a root whose plans contain
  zero resource changes (outputs-only diffs).
- Lifecycle gates that assume imports or resources (imports-only skip
  logic, artifact-state classification) treat data roots correctly.
- Reference machinery passes its existing suites unchanged with a
  data-only referent substituted for a generated one in a synthetic
  pack fixture.
- Transform on a data-only type mints config + lookup and provably
  never writes imports or moves.
- The four ZIA rule types resolve `location_groups` tokens end to end
  against a synthetic data-only fixture (engine test) and the real pack
  (provider qualification lane, not the engine suite).

## Verification items (before implementation planning)

- Confirm the real `zia_location_groups` data-source argument shape
  (per-name lookup assumed) and its id attribute.
- Confirm vendored provider schemas include `data_source_schemas`; if
  absent, add vendoring to the pack workstream.
- Confirm the fetch endpoint and pagination for location groups.
- Survey which existing `acknowledged_drops` entries across packs are
  actually latent data-only referents (predefined URL categories,
  ZIA time windows, device groups, etc.) to validate the surface
  generalizes — survey only, no scope expansion.

## Out of scope (v1)

- Dual-resolution fields ("managed first, else data"): expressible
  later as a layered resolver without merging roots; v1 references name
  exactly one referent.
- Migrating predefined URL categories: the natural second consumer and
  genericity check, but its own change.
- Scheduled refresh cadence and the CI mechanism decision (OPEN above).
- Provider-neutral fixture work beyond the synthetic pack the engine
  tests require.
