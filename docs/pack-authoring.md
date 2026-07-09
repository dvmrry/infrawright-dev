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

Set `INFRAWRIGHT_PACKS=/path/to/packs` to validate or run against a different
packs root. This uses the same discovery behavior as the engine.

Validate packs with:

```bash
make check-pack
make check-pack PACK=zia

python -m engine.check_pack
python -m engine.check_pack --pack zia
python -m engine.check_pack PACK=zia
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

Detailed diagnostic rule semantics are validated by their lane-specific
validators. The pack structural validator only checks the containing vocabulary
and simple types.

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
```

`constant_key`, `key_field`, and `import_id`, when present, must be strings.
`identity_fields` and `identity_renames` are string maps. `skip_if` and
`skip_if_lte`, when present, must be lists of non-empty matcher objects;
`skip_if_lte` thresholds must be JSON numbers.

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
      "zia_url_filtering_rules": {
        "projection_fill": [
          {
            "path": "cbi_profile",
            "source": "cbiProfile",
            "reason": "Provider read omits a write-required field; raw pull carries it.",
            "approved_by": "zscaler-adoption"
          }
        ]
      }
    }
  }
}
```

This pack policy is merged into `make adopt` / `engine.adopt` only. Saved-plan
classification and apply still use the operator-supplied `POLICY=<file>`; pack
metadata must not silently tolerate plan drift. Keep pack declarations narrow,
source-backed, and provider-version-specific in their reason text. Do not use
pack policy for tenant secrets, synthetic defaults, placeholders, or
environment-specific choices.

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
| `import_id` | Python format string used to render Terraform import IDs from the normalized item, defaulting to `{id}`. |
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

## Boundaries

Pack metadata configures provider identity, resource enumeration metadata,
lookup sidecars, provider-readiness evidence, and adoption metadata. It does not
silently change:

- transform or projection semantics
- drift policy
- plan classification
- Terraform/OpenTofu execution behavior
- collector behavior

Collectors remain separate from generic pack metadata in the current system.
Provider-specific collector code may live in a pack, but `pack.json` and
`registry.json` do not define out-of-tree collector loading.
