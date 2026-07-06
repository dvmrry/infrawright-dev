"""Override-file vocabulary and structural validation.

Application semantics live in engine.transform.apply_overrides; this module
owns only the authoring contract (known keys + shape) so authoring tools can
validate without importing the transform runtime.
"""

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
    "sort_lists",
    "split_csv",
    "strip_prefix",
    "value_map",
])


def validate_override_metadata(data, path=None):
    label = path or "<override>"
    if not isinstance(data, dict):
        raise ValueError("override metadata in %s must be an object" % label)
    unknown = sorted(set(data) - OVERRIDE_KEYS)
    if unknown:
        raise ValueError(
            "unknown override key %s in %s" % (unknown[0], label)
        )
