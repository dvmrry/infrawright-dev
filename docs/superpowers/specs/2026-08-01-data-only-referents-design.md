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
  fallback.
- Amendment (Task 5 review round): the "unchanged state-aware
  machinery" claim was incomplete for tokenized roots — a missing
  data-root state fails the terraform_remote_state read before any
  try() evaluates, and tokenized roots skip probing. Ruling: a
  state-aware run probes DATA referents even when tokens are present;
  on unusable state the emitted root is plan-safe (no remote-state
  data block for that referent; lookup-only resolver). State-blind
  rendering and generated-referent behavior are unchanged.
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

**Decided posture: the engine provides the means; CI/CD owns the
refresh.** The engine's whole obligation is to expose each selected
root's data-leaf referents machine-readably on the plan-roots surface
(a straight projection of the cross-state topology it already
maintains), so any CI/CD system can sequence "apply data leaves, then
plan/apply referrers" without discovery. No engine orchestration, no
staleness gating, no scheduling: `iw plan` stays read-only, and refresh
execution, ordering, and cadence are wholly the consuming pipeline's
concern. A pipeline wanting proactive staleness signals can layer them
itself from data-root plans (output-only diffs); nothing in the engine
depends on it doing so.

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

## Verification results (all items closed 2026-08-01)

- **Data-source shape confirmed** from the vendored provider schema:
  `zia_location_groups` takes an optional `name` argument (per-name
  `for_each` works as designed) and exposes `id` as a *number* —
  consistent with ZIA's numeric-ID reference fields — plus a computed
  `predefined` flag.
- **`data_source_schemas` is already vendored** in all four Zscaler
  packs (zia 115 entries, zpa 71, zcc 15, ztc 20). No vendoring work
  needed.
- **Fetch endpoint confirmed** against a real recorded cassette
  (zscaler-skill vendor, `TestLocationGroup.yaml`: `GET
  /zia/api/v1/locations/groups`) and the vendored OpenAPI spec
  (`page`/`pageSize`, max 1000): the registry entry is
  `{"pagination": "zia", "path": "locations/groups"}` — the engine's
  existing `PaginationZia` loop matches exactly.
- **The surface generalizes.** A full sweep of `acknowledged_drops`
  across all four packs found seven more confirmed latent data-only
  referents beyond location groups, all with per-name-lookup data
  sources and no registry type: `zia_firewall_filtering_time_window`,
  `zia_device_groups`, `zia_devices`, `zia_department_management`,
  `zia_group_management`, `zia_forwarding_control_proxy_gateway`, and
  `zpa_extranet_resource_partner` (with `zpa_app_connector_controller`
  as a shape-adaptation MAYBE). Two candidates
  (`zia_datacenters`, `zia_file_type_categories`) have list/filter-
  shaped data sources that do not fit the per-name `for_each` pattern
  and are explicitly out of scope. The sweep also confirmed
  `url_categories` is a resource/data-source *split* (custom generated,
  predefined already typed separately), validating that follow-on as
  structurally distinct, and surfaced one orthogonal gap — cross-pack
  references (`zia_ssl_inspection_rules` → `zpa_application_segment`)
  — which is not a data-only problem and is not addressed here.

## Out of scope (v1)

- Dual-resolution fields ("managed first, else data"): expressible
  later as a layered resolver without merging roots; v1 references name
  exactly one referent.
- Migrating predefined URL categories: the natural second consumer and
  genericity check, but its own change.
- Any refresh orchestration, gating, or scheduling in the engine: the
  engine emits the dependency data (Refresh strategy above); everything
  execution-side belongs to the consuming CI/CD system.
- Provider-neutral fixture work beyond the synthetic pack the engine
  tests require.
- List/filter-shaped data sources (e.g. `zia_datacenters`,
  `zia_file_type_categories`): v1's contract is per-name singleton
  lookup; adapting the module shape for filtered-list data sources is
  a follow-on.
- Cross-pack references (`zia_ssl_inspection_rules` →
  `zpa_application_segment`): a reference-wiring gap orthogonal to
  data-only referents.
- Wiring the seven newly surveyed latent referents: v1 lands the
  mechanism plus `zia_location_groups`; the rest are pack-work
  follow-ons that validate against the same engine surface.
