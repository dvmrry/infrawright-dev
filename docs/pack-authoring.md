# Pack Authoring Contract

Infrawright packs are provider metadata bundles under the effective packs root.
They describe provider prefixes, Terraform provider sources, resource registry
entries, optional lookup metadata, and diagnostic/adoption metadata consumed by
the engine. Packs do not silently change projection, drift policy, plan
classification, or Terraform/OpenTofu execution behavior.

## Location

By default, packs live under:

```text
packs/<name>/
```

Machine-readable evidence that is meaningful only with one provider pack lives
under `packs/<name>/evidence/`. It travels with that pack in a reduced
distribution and is not generic runtime metadata unless pack-specific tooling
explicitly loads it.

Set `INFRAWRIGHT_PACKS=/path/to/packs` to validate or run against a different
packs root. The effective root is authoritative for manifest discovery,
registries, schemas, overrides, and shared pack data. For every selected Fetch resource, it resolves
the resource's actual provider owner and existing `provider_sources`
declaration before binding a caller-approved `CollectorAdapter`.
The bundled CLI recognizes its shipped Zscaler provider sources; a library
caller may inject an adapter for a different provider source. An unknown
provider source fails before credentials or transport are initialized.

For a deliberately reduced pack root, pair that setting with an exact
`PACK_PROFILE`. See [Pack Distributions And Modular Checks](pack-distributions.md)
for the profile contract, reduced-root checks, and CI expectations.

Validate packs with:

```bash
make dist/iw
make check-pack
make check-pack PACK=zia

dist/iw check-pack
dist/iw check-pack --pack zia
```

## `pack.json`

`pack.json` is optional only in the sense that a directory without `pack.json`
is not discovered as a pack. A discovered pack may be a partial manifest, so
there are no universal required top-level keys today. When a metadata group is
present, its current validated vocabulary is closed.

Allowed top-level keys:

```text
absent_defaults
drift_policy
dynamic_schema
lookup_sources
pin
provider_config
provider_prefixes
provider_sources
references
requires_shared
scope_segments
sensitive_required
unescape_products
vendor
```

Simple type rules:

| Key | Type |
|---|---|
| `pin` | string |
| `vendor` | string |
| `provider_prefixes` | object of string -> string |
| `provider_sources` | object of string -> string |
| `scope_segments` | object of string -> string |
| `requires_shared` | sorted list of lowercase shared-component names |
| `unescape_products` | list of strings |
| `lookup_sources` | object |
| `references` | object |
| `provider_config` | object |
| `absent_defaults` | object |
| `dynamic_schema` | object |
| `sensitive_required` | object |

Nested required keys when the group is present:

| Group | Required shape |
|---|---|
| `lookup_sources.<resource>` | object with `name_field` string |
| `references.<resource>.<field>` | object with `referent` string and `name_field` string |
| `provider_config` | object with `requirements` list |
| `absent_defaults` | object with `rules` list |
| `dynamic_schema` | object with `rules` list |
| `sensitive_required` | object with `rules` list |

A `references.<resource>.<field>` entry may also carry `referent_id_field`,
naming which attribute of the referent the edge's values actually cite.
Omitting it means the referent's own `id`, byte-identical to every pack
written before this key existed. Declare it when the referrer's provider
schema stores a different, provider-modeled identifier for the same resource
-- for example a `set(number)` field that holds the referent's numeric `val`
rather than its string `id`. The value must be identifier-shaped and must not
be the literal `"id"` (omit the key instead); it is also refused on an edge
whose referent is a data referent, since alternate id spaces are a
generated-referent-only feature in this version.

Reference tokens are self-describing about which space they name: a
canonical edge (no `referent_id_field`) still commits the bare
`<referent>.<key>` token, but a `referent_id_field`-declaring edge commits
the explicit `<referent>.<key>.<field>` form instead -- for example
`zia_url_categories.blocked_sites.val` -- so a committed config names its
own space rather than relying on the pack's current declaration to decode
it. `<field>` is read from the token itself at resolve time; a bare or
`.id` token always means the canonical space, and a committed token whose
suffix disagrees with what the edge declares is refused rather than
resolved through either space.

Detailed diagnostic rule semantics are validated by their lane-specific
validators. The pack structural validator only checks the containing vocabulary
and simple types.

`requires_shared` declares pack-data dependencies under
`<packs-root>/_shared/<name>`. Pack validation, exact pack-profile validation,
and metadata loading fail when a declared component is absent. Distribution
profiles and example/test requirements should list those components explicitly
in their `shared` arrays so the required closure remains visible. The four
Zscaler provider packs, for example, each declare `requires_shared: ["zscaler"]`.

## `registry.json`

`registry.json` maps Terraform resource type to resource metadata. A pack may
omit `registry.json`; the existing engine treats such packs as metadata-only.

Allowed per-resource keys:

```text
adopt
derive
fetch
generate
product
```

Required per-resource keys:

```text
product
```

`generate`, when present, must be a boolean.

### `fetch`

Allowed keys:

```text
envelope
expand
follow_paths
merge_paths
optional_http_statuses
pagination
path
query
```

Required when `fetch` is present:

```text
pagination
path
```

`merge_paths` lists additional endpoints whose single-object payloads merge
into the base path's object before transform sees the item -- the shape for a
provider whose SDK combines several singleton calls into one settings read
(ZIA `security` plus `security/advanced`, for example). It requires
`pagination: "single"`, cannot be combined with `expand`, and every listed
path must be distinct from the base path and each other. A merged endpoint
returning anything but one JSON object fails the fetch, and a key returned by
more than one endpoint fails loudly rather than letting either value win.

`follow_paths` lists follow-up collections read once per base item, filling a
placeholder from that item's own field -- the shape for a provider whose
listing endpoint returns only part of the tree. ZIA locations are the case:
`locations` returns parent locations only, so sublocations come from
`locations/{id}/sublocations` per parent, exactly as the vendor SDK walks
them. Each entry is `{"path": ..., "from_field": ...}`, the path must contain
`{<from_field>}` exactly once, and follow items are concatenated after the
base items in declared order. A base item lacking the field is skipped
rather than failing, an empty follow response is normal, and any follow
request failure fails the whole fetch so a partial inventory never reads as
deletion. `follow_paths` cannot be combined with `expand` (both fan one path
over many values) or with `merge_paths` (the singleton-object surface). Note
the cost model: one extra request per base item.

`optional_http_statuses` must be a list of integers. `expand` is an object of
string keys to string-list values. `query` is an object of scalar query values.
`pagination` must be one of the implemented REST collector styles:

```text
single
zcc_v2
zia
zpa
```

### `derive`

Allowed keys:

```text
from
policy_type
```

Required when `derive` is present:

```text
from
```

`policy_type`, when present, is provider data emitted into the derived
resource config. It is not currently validated as a closed engine enum.

### `adopt`

Allowed keys:

```text
constant_key
identity_fields
identity_renames
import_id
key_field
skip_if
skip_if_lte
unsupported_if
```

`constant_key`, `key_field`, and `import_id`, when present, must be strings.
`identity_fields` and `identity_renames` are string maps. `skip_if` and
`skip_if_lte`, when present, must be lists of non-empty matcher objects;
`skip_if_lte` thresholds must be JSON numbers.

`unsupported_if` is a list of source-backed, provider-version-scoped rules
that fail adoption before identity derivation or any provider Oracle call. A
rule must select exactly one generic predicate:

- `match` is an object of exact JSON-scalar comparisons; every named field
  must match.
- `match_any_nonempty` is a list of raw field names; the rule matches when any
  named field is populated. Missing, null, empty-string, empty-list, and
  empty-object values do not match. Other scalar values, including `false` and
  zero, are populated values.

Both predicate forms compare against snake-cased raw input before identity
renames. Every rule also requires `provider.source`, `provider.version`, a
non-empty `reason`, and one or more immutable `evidence` links. Provider scope
must equal the active pack pin, so a provider refresh must explicitly
revalidate each rule.

### `key_field_references`

A composite `key_field` disambiguates objects whose names repeat, but composing
a raw ID puts a tenant-specific number into a reviewed artifact and re-keys
every existing object that carries a sentinel value for that field.
`key_field_references` resolves a component through a referenced object's name
instead:

```json
{
  "key_field": ["parent_id", "name"],
  "key_field_references": {
    "parent_id": {"referent": "zia_location_management", "name_field": "name"}
  }
}
```

A ZIA sublocation then keys as `global_psen_iot_device_segments` rather than
`137397251_iot_device_segments`. Each declared component must be one of the
type's own `key_field` entries; `id_field` defaults to `id`.

A resolving component that does not resolve -- absent, null, a sentinel like
ZIA's `parent_id` 0 on a top-level location, or an ID naming nothing in the
batch -- contributes nothing to the key rather than composing a placeholder.
That is what keeps a top-level object's key byte-identical to the plain-name
key it already had: only objects that actually have a parent gain a prefix, so
declaring this surface does not re-key existing committed config.

Resolution is within the type's own items in this version: key derivation runs
inside the transform kernel and the adoption identity pass, neither of which
can read another type's committed lookup, so a cross-type `referent` is
refused rather than silently resolving nothing. Both lanes derive keys through
separate code paths and are pinned to agree.

`constant_key` is for identity-less singleton resources: resources where the
provider has one object per tenant and the read payload has no natural `id`,
`name`, or other stable key field. The value is used verbatim as the generated
tfvars/import key, and the adoption path rejects it when the read produces more
than one item after skip predicates. It requires an explicit `import_id`; use a
literal `import_id` when the provider imports the singleton by a fixed ID:

```json
{
  "adopt": {
    "constant_key": "settings",
    "import_id": "settings"
  }
}
```

Do not set `constant_key` and `key_field` in the same `adopt` block.

Do not use transform override `defaults` to make singleton adoption work. Defaults
are projection/normalization metadata for transformed items; singleton key
derivation belongs in registry `adopt` metadata.

### `drift_policy`

Pack manifests may declare reviewed adoption-time projection policy for
provider-specific read/write inconsistencies:

```json
{
  "drift_policy": {
    "version": 1,
    "resource_types": {
      "sample_widget": {
        "projection_fill": [
          {
            "path": "write_profile",
            "source": "rawProfile",
            "reason": "Provider read omits a write-required field; raw pull carries it.",
            "approved_by": "pack-owner"
          }
        ]
      }
    }
  }
}
```

This pack policy is merged into `make adopt` / `iw adopt` only. Saved-plan
classification and apply still use the operator-supplied `POLICY=<file>`; pack
metadata must not silently tolerate plan drift. Keep pack declarations narrow,
source-backed, and provider-version-specific in their reason text. Do not use
pack policy for tenant secrets, synthetic defaults, placeholders, or
environment-specific choices.

Generated import-configuration rewriting deliberately defers
`projection_omit` and `projection_omit_if` selectors containing an exact
numeric index, such as `rules[0].order`. Terraform has not established stable
collection indexes at that phase, so choosing one generated HCL sibling would
be unsafe. The rewriter makes no generated-config edit and does not mark the
entry during that phase; use a wildcard selector such as `rules[*].order` when
every repeated sibling must be omitted. Provider validation or planning
remains responsible for any exact-index case that cannot be rewritten safely.
Generated-config use of an exact-index `drop_if_default` override path is
deferred by the same safeguard.

This is a generic authoring example, not current ZIA policy. The pinned ZIA
4.8.0 pack classifies URL-filtering ISOLATE rules as version-scoped
unsupported before Oracle and intentionally has no `cbi_profile`
`projection_fill`.

## Overrides

Transform override files live at:

```text
packs/<name>/overrides/<resource_type>.json
```

An override file is optional. If it is missing, the engine uses empty/default
override behavior for that resource. When an override file is present, unknown
top-level keys fail validation so typos do not silently become no-ops.

Overrides are explicit pack-authored projection and normalization metadata.
They do not change drift policy, plan classification, provider configuration,
adoption status, or Terraform/OpenTofu execution behavior. Use `drift_policy`
only for reviewed adoption-time projection exceptions. Do not store secret
values in overrides.

Allowed top-level keys:

<!-- override-key-table:start -->
| Key | Meaning |
|---|---|
| `acknowledged_drops` | Dotted dropped paths that are known and suppressed from the transform drop report. The fields are still removed from generated tfvars. |
| `defaults` | Field-to-literal defaults filled when the API omits a field or returns `null`, `""`, or `[]`; use only for provider-normalized round-trip values. |
| `divide` | Field-to-integer divisor for read-side unit conversion before default dropping; divisors must be non-zero. |
| `drop_if_default` | Field-to-value map for removing fields whose normalized value equals the configured default. Dotted nested-block attribute paths are supported. |
| `drops` | Fields or dotted nested-block attribute paths to remove from projected config. Dotted paths are applied during schema filtering. |
| `html_escape_fields` | Top-level string fields to HTML-escape after normal override transforms, matching provider read behavior for specific resources. |
| `identity_fields` | Identity/import aliases copied from raw or normalized item paths for oracle adoption metadata fallback. Prefer `registry.json` `adopt.identity_fields` for new packs. |
| `import_id` | Brace-format template used to render Terraform import IDs from the normalized item, defaulting to `{id}`. |
| `invert_bool` | Fields whose API boolean/int meaning is inverted relative to Terraform config; values are coerced to bool and flipped. |
| `key_field` | Field name or list of field names used to derive the stable `items` map key, defaulting to `name`. |
| `merge_blocks` | Nested block names whose API list elements should be merged into one block before schema coercion. |
| `no_html_unescape` | Boolean opt-out from product-wide ZPA/ZCC top-level `name`/`description` HTML unescaping. |
| `ranges` | Provider runtime-validator bounds used by module/sample generation; not applied as transform-time value rewriting. |
| `references` | Field map that forces `{id, ...}` object references or lists of references to unwrap to IDs during transform. |
| `renames` | Post-snake-case API-field to Terraform-schema-field rename map, applied before other field transforms. |
| `sample` | Module-generation sample overrides for required attributes whose generated example value would not be valid. |
| `skip_if` | List of matchers; an item is skipped entirely when any matcher matches all listed snake-cased raw fields. |
| `skip_if_lte` | List of numeric threshold matchers; an item is skipped entirely when any matcher has all listed snake-cased raw fields less than or equal to the configured threshold. |
| `sort_lists` | Top-level list-of-string fields sorted for stable output where provider behavior makes ordering plan-invisible. Dotted paths are not supported. |
| `split_csv` | Post-rename fields whose comma-joined string values are split into real lists with empty parts removed. |
| `strip_prefix` | Field-to-prefix map for removing provider-added read prefixes from strings or lists of strings. |
| `value_map` | Field-to-value map for converting API enum/string values to Terraform config values. Unmapped values pass through. |
<!-- override-key-table:end -->

Skip predicates run before transform `renames`, while adoption identity fallback
applies `renames` before checking skip predicates. To keep transform and
adoption in lockstep, an override skip matcher must not reference a field that
appears as either the source or destination of `renames`.

Current transform order is:

1. snake-case raw API keys
2. product HTML-unescape of top-level `name` and `description`, unless
   `no_html_unescape` is set
3. `skip_if` / `skip_if_lte`
4. `renames`
5. `split_csv`
6. `sort_lists`
7. top-level `drops`
8. `references`
9. `divide`
10. `invert_bool`
11. `value_map`
12. `strip_prefix`
13. `defaults`
14. `drop_if_default`
15. `html_escape_fields`
16. `key_field` key derivation
17. schema filtering/coercion, including `merge_blocks`, dotted `drops`, and
    dotted `drop_if_default`
18. `import_id` import block rendering

Naming caveat: pack-level `references` in `pack.json` and override-level
`references` in `overrides/<resource_type>.json` are unrelated concepts. The
pack-level form describes lookup sidecars; the override-level form unwraps
API reference objects during transform.

## Duplicate Resource Types

When validating all packs, duplicate resource types across registry files fail
loudly. The engine must not silently choose the last registry entry for a
resource type.

Provider ownership is also unique. Two installed packs may not map prefixes to
the same provider token, and two packs may not claim the same provider prefix.
Pack validation, exact profile validation, and collector resolution reject the
conflict with both pack names rather than selecting code by directory order.

## Provider Pin Bumps

Bumping a pack's `pin` invalidates every committed claim and snapshot that is
version-bound to it. The supported refresh order is:

1. **Re-verify the human claims at the new tag.** Registry
   `adopt.unsupported_if` entries pin `provider.version`; each one is a
   source-verified claim and must be re-checked against the new provider
   source before its version string moves. The committed parity fixtures
   (`tests/fixtures/parity/*.json` and the in-tree
   `zpa_application_segment_microtenant`) carry `provenance` blocks with
   `provider_version` and blob-ref source URLs pinned to the tag; re-read the
   cited ranges at the new tag, then update the version strings and URL tags
   (and line anchors, if the source moved). Fixture validation fail-closes
   until these match the active pin.
2. **Regenerate the derived snapshots.** `make regen-compatibility-fixtures`
   reruns the three compatibility gates in their explicit update mode
   (`IW_UPDATE_FIXTURES=1`): the module HCL hash snapshot
   (`go/internal/modulesgen/testdata/module_hcl_compatibility.json`), the
   parity compatibility capture
   (`go/internal/authoring/transformadoptparity/testdata/parity_compatibility.json`),
   and the frozen ZPA matrix's effective-input bindings. Each gate rewrites
   its snapshot plus the paired SHA-256 constant in its own test source, so
   evidence changes always surface as reviewable Go diffs. Update mode is
   byte-idempotent when nothing changed; review the diff before committing.
3. **ZPA is special.** The frozen matrix
   (`packs/zpa/evidence/zpa-provider-v4.4.10.json`) is a reviewed evidence
   corpus for one exact provider version, and its gate asserts currency
   against the active pin. The update mode refuses to re-bind it when the zpa
   pin has moved past the reviewed version: either hold the zpa pin at the
   reviewed version, or perform the matrix re-capture described in
   [ZPA Provider Evidence](zpa-provider-evidence.md) (new ref/commit,
   source-file bindings, anchors, and re-reviewed claims). A hash refresh is
   never a substitute for that review.

Snapshot membership never regenerates: adding resources, files, or fixtures
to any of these snapshots stays a reviewed hand edit.

## Boundaries

Pack metadata configures provider identity, resource enumeration metadata,
lookup sidecars, provider-readiness evidence, and adoption metadata. It does not
silently change:

- transform or projection semantics
- drift policy
- plan classification
- Terraform/OpenTofu execution behavior
- generic collector behavior

Provider-specific collector behavior remains code, not declarative metadata.
In the maintained runtime it is an ordinary typed Go collector adapter that
owns authentication and URL composition. The pack that declares a provider
token in `provider_prefixes` and its Terraform source in `provider_sources`
owns that product's registry metadata; its directory name need not equal the
provider token. The CLI resolves only provider sources for which it ships an
adapter and verifies that each selected resource's registry product matches
that adapter. Resources sharing a product cannot span different provider
sources.
Custom sources require a library caller to supply the matching adapter.

Product collection is implemented only by typed Go adapters; pack roots
contain no executable collector shims.
