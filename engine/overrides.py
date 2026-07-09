"""Override-file vocabulary and structural validation.

Application semantics live in engine.transform.apply_overrides; this module
owns only the authoring contract (known keys + shape) so authoring tools can
validate without importing the transform runtime.
"""
import math
import re


_SNAKE_1 = re.compile(r"(.)([A-Z][a-z]+)")
_SNAKE_2 = re.compile(r"([a-z0-9])([A-Z])")

OVERRIDE_KEYS = frozenset([
    "acknowledged_drops",
    "defaults",
    "divide",
    "drop_if_default",
    "drops",
    "html_escape_fields",
    "identity_fields",
    "import_id",
    "invert_bool",
    "key_field",
    "merge_blocks",
    "no_html_unescape",
    "ranges",
    "references",
    "renames",
    "sample",
    "skip_if",
    "skip_if_lte",
    "sort_lists",
    "split_csv",
    "strip_prefix",
    "value_map",
])


def _snake(name):
    half = _SNAKE_1.sub(r"\1_\2", name)
    return _SNAKE_2.sub(r"\1_\2", half).lower()


def _is_numeric_threshold(value):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return False
    return math.isfinite(value)


def validate_skip_matcher_metadata(data, path=None):
    label = path or "<metadata>"
    fields = []
    for key in ("skip_if", "skip_if_lte"):
        if key not in data:
            continue
        matchers = data[key]
        if not isinstance(matchers, list):
            raise ValueError("%s.%s must be a list" % (label, key))
        for idx, matcher in enumerate(matchers):
            matcher_path = "%s.%s[%d]" % (label, key, idx)
            if not isinstance(matcher, dict):
                raise ValueError("%s must be an object" % matcher_path)
            if not matcher:
                raise ValueError("%s must not be empty" % matcher_path)
            for field, value in matcher.items():
                if not isinstance(field, str) or not field:
                    raise ValueError(
                        "%s field names must be non-empty strings"
                        % matcher_path
                    )
                fields.append((key, field, _snake(field)))
                if key == "skip_if_lte":
                    if not _is_numeric_threshold(value):
                        raise ValueError(
                            "%s.%s threshold must be a finite JSON number"
                            % (matcher_path, field)
                        )
                elif isinstance(value, (dict, list)):
                    raise ValueError(
                        "%s.%s value must be a scalar" % (matcher_path, field)
                    )
    return fields


def _validate_skip_rename_conflicts(data, label, fields):
    renames = data.get("renames") or {}
    if not isinstance(renames, dict):
        return
    renamed = set()
    for old, new in renames.items():
        if isinstance(old, str):
            renamed.add(_snake(old))
        if isinstance(new, str):
            renamed.add(_snake(new))
    conflicts = sorted(
        set(field for _, field, snake_field in fields if snake_field in renamed)
    )
    if conflicts:
        raise ValueError(
            "skip predicates in %s reference renamed field(s) %s; "
            "skip predicates run before transform renames and after adoption "
            "identity renames, so keep skip fields independent of renames"
            % (label, ", ".join(conflicts))
        )


def validate_override_metadata(data, path=None):
    label = path or "<override>"
    if not isinstance(data, dict):
        raise ValueError("override metadata in %s must be an object" % label)
    unknown = sorted(set(data) - OVERRIDE_KEYS)
    if unknown:
        raise ValueError(
            "unknown override key %s in %s" % (unknown[0], label)
        )
    skip_fields = validate_skip_matcher_metadata(data, path=label)
    _validate_skip_rename_conflicts(data, label, skip_fields)
