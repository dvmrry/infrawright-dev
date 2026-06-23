"""Identity/import metadata for oracle-backed adoption.

This is deliberately narrower than transform overrides:
- stable key derivation
- import ID derivation
- item skipping for unmanageable system objects

It must not decide Terraform field coverage.
"""
from engine import transform
from engine.registry import load_registry


def adoption_entry(resource_type):
    """Return normalized adoption metadata for a resource type.

    New packs should prefer registry.json:
      "adopt": {
        "key_field": "name",
        "import_id": "{id}",
        "identity_renames": {"vpnConnectionId": "id"}
      }

    Existing packs can fall back to transform overrides for first-branch
    compatibility. Only identity/import fields are read from those overrides.
    """
    reg = load_registry().get(resource_type, {})
    explicit = reg.get("adopt") or {}
    override = transform.load_override(resource_type)
    return {
        "key_field": explicit.get("key_field", override.get("key_field", "name")),
        "import_id": explicit.get("import_id", override.get("import_id", "{id}")),
        "identity_renames": explicit.get(
            "identity_renames", override.get("renames", {})
        ),
        "skip_if": explicit.get("skip_if", override.get("skip_if", [])),
    }


def identity_item(raw, resource_type):
    """Return a snake_cased item suitable for key/import-id derivation only."""
    meta = adoption_entry(resource_type)
    item = transform.snake_keys(raw)
    for old, new in sorted((meta.get("identity_renames") or {}).items()):
        old_snake = transform.snake(old)
        new_snake = transform.snake(new)
        if old_snake in item:
            item[new_snake] = item.pop(old_snake)
    return item


def skip_identity_item(item, meta):
    for matcher in meta.get("skip_if") or []:
        if all(item.get(transform.snake(k)) == v for k, v in matcher.items()):
            return True
    return False


def derive_key_from_identity(item, meta):
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


def _path_value(item, path):
    cur = item
    for segment in str(path).split("."):
        segment = transform.snake(segment)
        if not isinstance(cur, dict) or segment not in cur:
            return _MISSING
        cur = cur[segment]
    return cur
