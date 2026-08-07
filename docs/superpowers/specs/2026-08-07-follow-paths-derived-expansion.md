# Follow Paths: Expansion Derived From A Fetch Result

Design for fetch paths whose expansion values are discovered from the
fetch itself rather than declared as a literal list. Motivating case: ZIA
sublocations, which live at `locations/{parent_id}/sublocations` and are
absent from the `locations` list, so a tenant's sublocations are invisible
to the engine today (one downstream tenant: 141 sublocations across 7
parent locations; another: 29 parents, likely several hundred).

## The provider-shape finding that sets the design

The request arrived proposing a new registry type `zia_location_sublocation`
whose `expand` draws parent IDs from a sibling resource
(`{"from_resource": "zia_location_management", "field": "id"}`), and noted
the type/nesting question as unverified. It is now verified, and the answer
changes the mechanism.

**The ZIA provider has no sublocation resource type.** `zia_location_management`
models both locations and sublocations, distinguished by an optional
`parent_id`:

- provider registers exactly one type (`provider.go:161`, no sublocation
  entry); the vendored schema carries `parent_id` as an optional number on
  `zia_location_management`;
- Read resolves either kind through
  `locationmanagement.GetLocationOrSublocationByID`
  (`resource_zia_location_management.go:507`), and Create/Update pass
  `ParentID` (`:714`);
- the SDK's `GetAllSublocations` is exactly the proposed traversal — list
  parents via `GetAll`, then read `/locations/{id}/sublocations` per parent
  — which exists precisely because `/locations` returns parents only.

A registry type must name a real Terraform resource type: generation and
transform both load `resource_schemas[<type>]`. `zia_location_sublocation`
has no such entry and never will, so that type cannot be created.
Sublocations belong to the existing `zia_location_management` type, as
items carrying `parent_id`.

**Consequence for the mechanism.** The expansion source is not a sibling
resource; it is the type's own base fetch result. That removes the largest
item from the original scope — cross-type fetch ordering. Fetch stays
independent per resource type, the scheduler is untouched, and no
`FetchEntry` needs access to another resource's items.

## Hard invariants

1. A fetch may declare follow-up paths whose placeholder values come from
   the base fetch's own items. The result is one item list for the type:
   base items, then follow items, in declared order.
2. Absent declaration changes nothing. Every existing fetch entry, its
   request sequence, and its item order are byte-identical.
3. Failures stay loud. A follow request that fails fails the resource's
   fetch, exactly as a base page failure does; it never yields a partial
   inventory that later reads as deletion.
4. Follow paths are a collection surface and stay distinct from
   `merge_paths`, which is a singleton-object surface: follow items are
   concatenated as separate objects, never merged key-wise.

## Proposed shape

```json
"zia_location_management": {
  "fetch": {
    "pagination": "zia",
    "path": "locations",
    "follow_paths": [
      {"path": "locations/{id}/sublocations", "from_field": "id"}
    ]
  }
}
```

- `from_field` names the base item field supplying the placeholder value;
  the placeholder token is `{<from_field>}` and must appear exactly once
  (the existing one-placeholder restriction carries over unchanged).
- Values must be JSON scalars, rendered with the canonical number tokens
  the collector already uses for query scalars, and must pass the same
  expansion safety check literal expansions pass before percent-encoding.
- Follow requests use the entry's own pagination style and query.
- A base item missing the field is skipped, not an error: a leaf resource
  legitimately has nothing to follow.
- An empty follow response is normal (a parent with no children).
- `optional_http_statuses` applies to follow requests, so a tenant whose
  childless parents 404 is declarable rather than fatal.

`follow_paths` and the literal `expand` are mutually exclusive on one
entry: both fan one path over N values, and composing them has no single
obvious traversal order. `merge_paths` is likewise excluded, since it is
the singleton surface.

## Consequences worth deciding before shipping

- **Request count is linear in base items.** 7 parents costs 7 extra
  requests; a thousand-location tenant costs a thousand. Acceptable for
  the motivating case, and the existing scheduler concurrency applies, but
  the spec should say plainly that this is the cost model rather than
  letting an operator discover it.
- **Key collisions become reachable.** Sublocation names are unique within
  a parent, not across the tenant, so two parents each owning a
  "Guest-WiFi" sublocation now land in one flat key space. Adoption already
  fails closed on this (`adoption_meta.go:622` duplicate derived key), so
  the risk is a blocked adoption rather than silent corruption -- but the
  pack likely needs a composite key for this type, decided against real
  tenant data before the fetch change is turned on for a tenant.
- **`parent_id` stays a literal tenant ID.** It is a schema attribute and
  is not in the type's `acknowledged_drops`, so it projects into committed
  config as-is. Making it portable would need a self-referential reference
  edge, which the binding machinery refuses outright (same-type references
  are skipped as would-be Terraform cycles). Out of scope here; worth its
  own decision if cross-tenant portability of sublocations matters.

## Surfaces

1. `metadata/resources.go`: `fetchKeys` gains `follow_paths`; validation of
   entry shape, one-placeholder rule, mutual exclusion with `expand` and
   `merge_paths`, path safety.
2. `collectors/rest.go`: `FetchEntry.FollowPaths`; parsing in `fetchEntry`;
   traversal in `FetchResource` after the base collection, reusing the
   existing pagination dispatch per follow request; collector-boundary
   revalidation mirroring `mergedFetchPaths` (the second half of the same
   single source of truth).
3. Pack: `zia_location_management` declares the follow path; keying and
   drops re-verified against a real tenant pull.
4. Docs: `pack-authoring.md` fetch section.

## Out of scope

- Cross-resource expansion (`from_resource`). No case requires it once
  sublocations resolve within their own type, and it would pull fetch
  ordering into the scheduler.
- Making `parent_id` a portable reference.
- ZTW's own `locationmanagement` service, which has the same shape and can
  adopt this mechanism later if its pack needs it.
