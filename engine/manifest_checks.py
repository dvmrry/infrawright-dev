"""Shared strict-shape validators for pack and registry metadata."""


def reject_unknown_keys(data, allowed, path):
    unknown = sorted(set(data) - allowed)
    if unknown:
        raise ValueError("%s: unknown key %s" % (path, unknown[0]))


def require_keys(data, required, path):
    missing = sorted(required - set(data))
    if missing:
        raise ValueError("%s: missing required key %s" % (path, missing[0]))


def require_non_empty_string(value, path):
    if not isinstance(value, str) or not value:
        raise ValueError("%s must be a non-empty string" % path)


def validate_string_map(value, path):
    if not isinstance(value, dict):
        raise ValueError("%s must be an object" % path)
    for key, item in value.items():
        if not isinstance(key, str) or not key:
            raise ValueError("%s keys must be non-empty strings" % path)
        if not isinstance(item, str) or not item:
            raise ValueError("%s.%s must be a non-empty string" % (path, key))
