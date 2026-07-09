# Generator overrides

Most entries below encode a rule mined from the pinned provider schema,
provider source, API evidence, or provider-lab findings. Overrides are
pack-owned metadata, not engine code.

The current workflow is:

1. Use provider-readiness evidence (`make openapi-map`,
   `make source-operation-map`, `make provider-probe`) and/or live provider
   labs to identify a provider mismatch.
2. Encode the narrow exception in `packs/<provider>/overrides/` or the
   provider pack metadata.
3. Regenerate and verify with `python -m engine.gen_module`, `make check`, and
   `make check-demo`.

If `packs/<provider>/overrides/<resource_type>/main.tf` exists,
`engine.gen_module` uses it verbatim instead of the rendered `main.tf` for that
resource. This is the escape hatch for provider quirks the generator cannot
express. Each override is a carried bug: record why in a comment at the top of
the file, and delete the override (then regenerate) when upstream fixes land.

## Transform override maps

`packs/<provider>/overrides/<resource_type>.json` configures the transform for that
resource (all keys optional): `key_field` (map key source, default
`name`; may be a LIST of fields joined into one slug for composite keys —
e.g. `["type", "name"]` where names are only unique within a type),
`renames` (post-snake-case API→schema names), `drops` (fields
always removed; a DOTTED path like `conditions.operands.name` reaches
inside nested blocks — for fields the API rewrites so a config copy can
never round-trip, e.g. operand display names, zpa#287), `references`
(force `{id,...}` unwrapping), `sort_lists` (list-of-string fields
whose ORDER the provider itself diff-suppresses — zia url_categories
`urls` — sorted so unstable API ordering can't churn drift PRs;
plan-invisible because the provider absorbs order differences), `drop_if_default` (remove a field when it
equals the given value — perma-diff suppression; dotted paths reach
nested blocks the same way), `divide` (field→integer divisor: unit conversion
for fields where the provider schema stores a larger unit than the API
returns and converts internally — e.g. ZIA `size_quota` is KB on the API
but MB in config, so `"divide": {"size_quota": 1024}`; integer division,
applied before `drop_if_default` so a converted 0 still drops),
`invert_bool` (list of fields whose API booleans are INVERTED — ZCC
failopen speaks 0=enabled; coerce-then-flip, mined from the provider's
boolToInverted helpers), `value_map` (field→{api_value: config_value}
bridges for string-enum APIs behind bool/other schemas, e.g.
policy_style NONE/DUAL_POLICY_EVAL→bool), `strip_prefix`
(field→prefix the provider's read strips and its write re-adds, e.g.
source_countries COUNTRY_), `no_html_unescape` (boolean: skip the product-wide ZPA/ZCC HTML
unescape of top-level name/description for THIS resource — for reads
that go through GetAll/list responses, where the SDK's unescapeHTML is
a no-op on the pagination wrapper and STATE keeps the escaped bytes;
e.g. zpa_app_connector_group), `merge_blocks` (list of block names whose API elements the provider's
READ collapses into ONE block with merged list members even though the
schema declares a plain list — the schema lies, the flatten tells the
truth; verify in provider source before adding, e.g. zpa
`server_groups`: N `{id,...}` API objects → `[{id: [all ids]}]`),
`defaults` (field→literal: fill when the API omitted the field or
returned it empty — for required-on-write fields where "unset means
everything", e.g. `"defaults": {"url_categories": ["ANY"]}` on URL
filtering rules; pick the value the PROVIDER's read normalizes to so it
round-trips stably), `ranges` (field→[min, max]: provider RUNTIME validator bounds mined from
provider source — invisible in the schema dump, e.g.
`"ranges": {"size_quota": [10, 100000]}` — size_quota is MB in config),
`split_csv` (list of post-rename fields whose
comma-joined string values become real lists, empties dropped — ZCC
returns list-typed settings this way), `import_id` (format template over
the item's snake_cased original fields, default `"{id}"`), `acknowledged_drops`
(list of dotted drop paths that are expected/known-unmanageable — suppressed
from the drop report so only new provider-coverage surprises surface; the
fields are still removed from the generated tfvars), `skip_if` (list of
matchers; each matcher is a dict of field→value; an item is excluded
entirely when any matcher's pairs all match the snake_cased raw item —
use this for system/predefined objects the provider refuses to manage, e.g.
`"skip_if": [{"default_rule": true}]` drops any item where `default_rule`
is `true`), `skip_if_lte` (list of numeric threshold matchers; an item is
excluded when all listed snake_cased raw fields are less than or equal to
the JSON-number threshold, e.g. `"skip_if_lte": [{"order": 0}]`). Skip
predicates must not reference fields that also appear as either side of
`renames`, because transform and adoption evaluate those steps in opposite
orders. Exceptions are data, not code: prefer an entry here over editing the
transform.

The same JSON file may also carry one GENERATOR key: `sample` (a dict
merged over the generated module test fixture's example item) — for
required attributes with provider-validated enums where the default
`"example"` value cannot pass a mock plan, e.g.
`"sample": {"protocols": ["ANY_RULE"]}`.
