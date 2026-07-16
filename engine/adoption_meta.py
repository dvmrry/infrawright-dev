"""Identity/import metadata for oracle-backed adoption.

This is deliberately narrower than transform overrides:
- stable key derivation
- import ID derivation
- item skipping for unmanageable system objects

It must not decide Terraform field coverage.
"""
from engine import packs
from engine import transform
from engine.registry import load_registry


def adoption_entry(resource_type):
    """Return normalized adoption metadata for a resource type.

    New packs should prefer registry.json:
      "adopt": {
        "key_field": "name",
        "constant_key": "settings",
        "import_id": "{id}",
        "identity_renames": {"vpnConnectionId": "id"},
        "identity_fields": {"import_id": "uuid"}
      }

    Existing packs can fall back to transform overrides for first-branch
    compatibility. Only identity/import fields are read from those overrides.
    """
    reg = load_registry().get(resource_type, {})
    explicit = reg.get("adopt") or {}
    override = transform.load_override(resource_type)
    identity_fields = _identity_fields(explicit, override)
    if "import_id" in explicit:
        import_id = explicit["import_id"]
    elif "import_id" in override:
        import_id = override["import_id"]
    elif "import_id" in identity_fields:
        import_id = "{import_id}"
    else:
        import_id = "{id}"
    return {
        "constant_key": explicit.get("constant_key"),
        "key_field": explicit.get("key_field", override.get("key_field", "name")),
        "import_id": import_id,
        "identity_renames": explicit.get(
            "identity_renames", override.get("renames", {})
        ),
        "identity_fields": identity_fields,
        "skip_if": explicit.get("skip_if", override.get("skip_if", [])),
        "skip_if_lte": explicit.get(
            "skip_if_lte", override.get("skip_if_lte", [])
        ),
    }


def unsupported_rules(resource_type):
    """Return unsupported adoption rules after enforcing provider pin scope."""
    reg = load_registry().get(resource_type, {})
    explicit = reg.get("adopt") or {}
    rules = explicit.get("unsupported_if") or []
    provider = packs.provider_of(resource_type)
    expected_source = packs.provider_sources().get(provider)
    expected_version = packs.provider_pins().get(provider)
    for index, rule in enumerate(rules):
        scoped = rule["provider"]
        label = "%s.adopt.unsupported_if[%d].provider" % (
            resource_type, index)
        if scoped["source"] != expected_source:
            raise ValueError(
                "%s.source %r does not match active provider source %r"
                % (label, scoped["source"], expected_source)
            )
        if scoped["version"] != expected_version:
            raise ValueError(
                "%s.version %r does not match active provider pin %r"
                % (label, scoped["version"], expected_version)
            )
    return rules


def classify_raw_items(raw_items, resource_type):
    """Classify raw input before identity shaping or Terraform execution."""
    meta = adoption_entry(resource_type)
    rules = unsupported_rules(resource_type)
    result = {"eligible": [], "skipped": [], "unsupported": []}
    for raw in raw_items:
        item = transform.snake_keys(raw)
        reason = transform.skip_item_match_reason(item, meta)
        if reason:
            result["skipped"].append({"item": item, "reason": reason})
            continue
        matched = None
        for rule in rules:
            if transform.strict_json_scalar_matcher_matches(
                    item, rule["match"]):
                matched = rule
                break
        if matched is not None:
            result["unsupported"].append({"item": item, "rule": matched})
            continue
        result["eligible"].append(raw)
    return result


def identity_item(raw, resource_type):
    """Return a snake_cased item suitable for key/import-id derivation only."""
    meta = adoption_entry(resource_type)
    item = transform.snake_keys(raw)
    raw_item = dict(item)
    for old, new in sorted((meta.get("identity_renames") or {}).items()):
        old_snake = transform.snake(old)
        new_snake = transform.snake(new)
        if old_snake in item:
            item[new_snake] = item.pop(old_snake)
    for alias, path in sorted((meta.get("identity_fields") or {}).items()):
        value = _path_value(raw_item, path)
        if value is _MISSING:
            value = _path_value(item, path)
        if value is _MISSING:
            raise KeyError(
                "%s adopt.identity_fields.%s path %r missing from item"
                % (resource_type, alias, path)
            )
        if alias in item and item[alias] != value:
            raise ValueError(
                "%s adopt.identity_fields.%s path %r would overwrite existing "
                "field %r (%r != %r)"
                % (resource_type, alias, path, alias, item[alias], value)
            )
        item[alias] = value
    return item


def skip_identity_item(item, meta):
    return skip_identity_item_reason(item, meta) is not None


def skip_identity_item_reason(item, meta):
    return transform.skip_item_match_reason(item, meta)


def derive_key_from_identity(item, meta):
    constant_key = meta.get("constant_key")
    if constant_key is not None:
        if not isinstance(constant_key, str) or not constant_key:
            raise ValueError("adopt.constant_key must be a non-empty string")
        return constant_key
    field = meta.get("key_field", "name")
    fields = field if isinstance(field, list) else [field]
    parts = []
    for name in fields:
        value = _path_value(item, name)
        if value is _MISSING:
            raise KeyError(
                "key field %r missing from item; set adopt.key_field or "
                "override key_field" % name
            )
        parts.append(str(value))
    slug = transform.slugify(" ".join(parts))
    if slug == "":
        ident = item.get("id")
        if ident is None:
            raise ValueError(
                "derived key is empty for %s (value(s) %r have no ASCII "
                "letters/digits) and item has no 'id' to fall back on"
                % (fields, parts)
            )
        slug = "id_%s" % transform.slugify(str(ident))
    return slug


def derive_import_id_from_identity(item, meta, resource_type, key):
    template = meta.get("import_id", "{id}")
    try:
        return template.format(**item)
    except KeyError as exc:
        raise ValueError(
            "import_id template %r for %s item %r references missing field %s"
            % (template, resource_type, key, exc)
        )


_MISSING = object()


def _identity_fields(explicit, override):
    fields = explicit.get("identity_fields", override.get("identity_fields", {}))
    if fields is None:
        return {}
    if not isinstance(fields, dict):
        raise ValueError("adopt.identity_fields must be an object")
    out = {}
    for alias, path in sorted(fields.items()):
        alias_snake = transform.snake(alias)
        if not alias_snake:
            raise ValueError("adopt.identity_fields contains an empty alias")
        out[alias_snake] = str(path)
    return out


def _path_value(item, path):
    cur = item
    for segment in str(path).split("."):
        segment = transform.snake(segment)
        if not isinstance(cur, dict) or segment not in cur:
            return _MISSING
        cur = cur[segment]
    return cur
