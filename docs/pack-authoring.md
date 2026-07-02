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

### `adopt`

Allowed keys:

```text
identity_fields
identity_renames
import_id
key_field
skip_if
```

`key_field` and `import_id`, when present, must be strings.
`identity_fields` and `identity_renames` are string maps. `skip_if`, when
present, must be a list.

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
