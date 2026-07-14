"""Single source of truth for which resource types the toolchain knows.

Consumers read the slice they need: generators use generated_types(); the
fetcher uses fetch_entry().
Stdlib-only, Python 3.6-floor — see AGENTS.md rule 5.
"""
import json
import os

from engine import manifest_checks
from engine import packs
from engine.overrides import validate_skip_matcher_metadata

_cache = {}

REGISTRY_RESOURCE_KEYS = set([
    "adopt", "derive", "fetch", "generate", "product", "slug_group",
])
REGISTRY_REQUIRED_RESOURCE_KEYS = set(["product"])
FETCH_KEYS = set([
    "envelope",
    "expand",
    "optional_http_statuses",
    "pagination",
    "path",
    "query",
])
FETCH_REQUIRED_KEYS = set(["pagination", "path"])
DERIVE_KEYS = set(["from", "policy_type"])
DERIVE_REQUIRED_KEYS = set(["from"])
ADOPT_KEYS = set([
    "constant_key",
    "identity_fields",
    "identity_renames",
    "import_id",
    "key_field",
    "skip_if",
    "skip_if_lte",
])


def load_registry():
    """Merge every pack's registry.json. One pack today; one per provider
    after the split — provider 'product' fields keep entries disjoint."""
    if not _cache:
        registries = []
        for path in packs.registry_paths():
            with open(path, encoding="utf-8") as f:
                data = json.load(f)
            validate_registry(data, path=path)
            registries.append((path, data))
        check_duplicate_resource_types(registries)
        merged = {}
        for path, data in registries:
            for resource_type, entry in data.items():
                merged[resource_type] = entry
        _cache.update(merged)
    return _cache


def check_duplicate_resource_types(registries):
    """Fail loudly when two registry files declare the same resource type."""
    owners = {}
    for path, data in registries:
        if data is None:
            continue
        for resource_type in data:
            if resource_type in owners:
                raise ValueError(
                    "%s: duplicate resource type %r already loaded from %s"
                    % (path, resource_type, owners[resource_type])
                )
            owners[resource_type] = path


def _where(path):
    return path or "registry.json"


def _validate_query(value, path):
    if not isinstance(value, dict):
        raise ValueError("%s must be an object" % path)
    for key, item in value.items():
        if not isinstance(key, str):
            raise ValueError("%s keys must be strings" % path)
        if not isinstance(item, (str, int, float, bool)) and item is not None:
            raise ValueError(
                "%s.%s must be a scalar query value" % (path, key)
            )


def _validate_expand(value, path):
    if not isinstance(value, dict):
        raise ValueError("%s must be an object" % path)
    for key, values in value.items():
        if not isinstance(key, str) or not key:
            raise ValueError("%s keys must be non-empty strings" % path)
        if not isinstance(values, list):
            raise ValueError("%s.%s must be a list" % (path, key))
        for idx, item in enumerate(values):
            if not isinstance(item, str) or not item:
                raise ValueError(
                    "%s.%s[%d] must be a non-empty string" % (path, key, idx)
                )


def _validate_statuses(value, path):
    if not isinstance(value, list):
        raise ValueError("%s must be a list" % path)
    for idx, item in enumerate(value):
        if not isinstance(item, int) or isinstance(item, bool):
            raise ValueError("%s[%d] must be an integer" % (path, idx))


def _pagination_values():
    from engine.collectors.rest import pagination_styles
    return pagination_styles()


def _validate_enum(value, allowed, path):
    if value not in allowed:
        raise ValueError(
            "%s unsupported value %r; allowed values: %s"
            % (path, value, ", ".join(sorted(allowed)))
        )


def _validate_fetch(fetch, path):
    if not isinstance(fetch, dict):
        raise ValueError("%s must be an object" % path)
    manifest_checks.reject_unknown_keys(fetch, FETCH_KEYS, path)
    manifest_checks.require_keys(fetch, FETCH_REQUIRED_KEYS, path)
    manifest_checks.require_non_empty_string(
        fetch.get("pagination"), "%s.pagination" % path
    )
    _validate_enum(
        fetch.get("pagination"),
        _pagination_values(),
        "%s.pagination" % path,
    )
    manifest_checks.require_non_empty_string(fetch.get("path"), "%s.path" % path)
    if "envelope" in fetch:
        manifest_checks.require_non_empty_string(
            fetch["envelope"], "%s.envelope" % path
        )
    if "query" in fetch:
        _validate_query(fetch["query"], "%s.query" % path)
    if "expand" in fetch:
        _validate_expand(fetch["expand"], "%s.expand" % path)
    if "optional_http_statuses" in fetch:
        _validate_statuses(
            fetch["optional_http_statuses"],
            "%s.optional_http_statuses" % path,
        )


def _validate_derive(derive, path):
    if not isinstance(derive, dict):
        raise ValueError("%s must be an object" % path)
    manifest_checks.reject_unknown_keys(derive, DERIVE_KEYS, path)
    manifest_checks.require_keys(derive, DERIVE_REQUIRED_KEYS, path)
    manifest_checks.require_non_empty_string(derive.get("from"), "%s.from" % path)
    if "policy_type" in derive:
        # policy_type is provider data carried into the generated reorder
        # resource, not an engine-owned closed enum. Keep it open until pack
        # metadata has a provider-specific vocabulary to validate against.
        manifest_checks.require_non_empty_string(
            derive["policy_type"], "%s.policy_type" % path
        )


def _validate_adopt(adopt, path):
    if not isinstance(adopt, dict):
        raise ValueError("%s must be an object" % path)
    manifest_checks.reject_unknown_keys(adopt, ADOPT_KEYS, path)
    if "constant_key" in adopt and "key_field" in adopt:
        raise ValueError(
            "%s cannot set both constant_key and key_field" % path
        )
    if "constant_key" in adopt and "import_id" not in adopt:
        raise ValueError("%s.constant_key requires import_id" % path)
    for key in ("constant_key", "key_field", "import_id"):
        if key in adopt:
            manifest_checks.require_non_empty_string(
                adopt[key], "%s.%s" % (path, key)
            )
    for key in ("identity_renames", "identity_fields"):
        if key in adopt:
            manifest_checks.validate_string_map(adopt[key], "%s.%s" % (path, key))
    validate_skip_matcher_metadata(adopt, path=path)


def validate_registry(data, path=None):
    """Validate registry.json metadata vocabulary."""
    path = _where(path)
    if not isinstance(data, dict):
        raise ValueError("%s must contain a JSON object" % path)
    for resource_type, entry in data.items():
        if not isinstance(resource_type, str) or not resource_type:
            raise ValueError("%s resource keys must be non-empty strings" % path)
        label = "%s.%s" % (path, resource_type)
        if not isinstance(entry, dict):
            raise ValueError("%s must be an object" % label)
        manifest_checks.reject_unknown_keys(entry, REGISTRY_RESOURCE_KEYS, label)
        manifest_checks.require_keys(entry, REGISTRY_REQUIRED_RESOURCE_KEYS, label)
        if "generate" in entry and not isinstance(entry.get("generate"), bool):
            raise ValueError("%s.generate must be a boolean" % label)
        if "slug_group" in entry and not isinstance(
                entry.get("slug_group"), bool):
            raise ValueError("%s.slug_group must be a boolean" % label)
        manifest_checks.require_non_empty_string(
            entry.get("product"), "%s.product" % label
        )
        if "fetch" in entry:
            _validate_fetch(entry["fetch"], "%s.fetch" % label)
        if "derive" in entry:
            _validate_derive(entry["derive"], "%s.derive" % label)
        if "adopt" in entry:
            _validate_adopt(entry["adopt"], "%s.adopt" % label)
    return data


def generated_types():
    """Resource types with generate=true, sorted."""
    reg = load_registry()
    return sorted(rt for rt, e in reg.items() if e.get("generate"))


def reload_registry():
    """Clear the cache (test isolation helper)."""
    _cache.clear()
    return load_registry()


def fetch_entry(resource_type):
    """Flattened fetch config {product, path, pagination, query?} for a
    resource, or KeyError if it has no fetch wiring."""
    reg = load_registry()
    if resource_type not in reg or "fetch" not in reg[resource_type]:
        raise KeyError(
            "%r has no fetch entry in pack registry metadata" % resource_type
        )
    entry = dict(reg[resource_type]["fetch"])
    entry["product"] = reg[resource_type]["product"]
    return entry


def derive_entry(resource_type):
    """Derive config {from, ...} for a resource whose config is built from
    ANOTHER resource's pull (it has no fetch/import of its own, e.g.
    zpa_policy_access_rule_reorder derived from zpa_policy_access_rule's order),
    or None for a normally-fetched resource."""
    reg = load_registry()
    return reg.get(resource_type, {}).get("derive")


def derived_types():
    """Generated resource types that are derived from another, sorted."""
    reg = load_registry()
    return sorted(rt for rt in generated_types() if reg[rt].get("derive"))
