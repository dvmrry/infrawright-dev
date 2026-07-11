"""Build the closed ZCC transform catalog consumed by the Node host.

The existing Python pack, override, and provider-schema loaders remain the
authoritative authoring surface during the migration.  This module compiles
the deliberately narrow, versioned subset needed to reproduce the five
fetch-backed ZCC transforms without invoking Python at runtime.
"""
import argparse
import hashlib
import html
import html.entities
import json
import os
import sys

from engine import packs
from engine.registry import load_registry
from engine.tfschema import (
    attr_type,
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)
from engine.transform import load_override


TRANSFORM_CATALOG_CONTRACT = "infrawright.transform_catalog"
TRANSFORM_CATALOG_SCHEMA_VERSION = 1
ZCC_PRODUCT = "zcc"
ZCC_FETCH_RESOURCES = (
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
)

# Every key accepted here either has an explicit catalog representation or is
# proven irrelevant to the tfvars transform kernel.  A newly authored override
# key must therefore stop generation until the Node contract grows with it.
_SUPPORTED_OVERRIDE_KEYS = frozenset([
    "acknowledged_drops",
    "import_id",
    "invert_bool",
    "key_field",
    "no_html_unescape",
    "renames",
    "split_csv",
])
_PRIMITIVE_ENCODINGS = frozenset(["bool", "number", "string"])


def _validate_product(product):
    if product != ZCC_PRODUCT:
        raise ValueError(
            "unsupported transform catalog product %r; expected %r"
            % (product, ZCC_PRODUCT)
        )


def _fetch_resources(product):
    registry = load_registry()
    resources = sorted(
        resource_type
        for resource_type, entry in registry.items()
        if entry.get("product") == product and "fetch" in entry
    )
    _validate_fetch_resources(resources)
    return resources


def _validate_fetch_resources(resources):
    actual = set(resources)
    expected = set(ZCC_FETCH_RESOURCES)
    missing = sorted(expected - actual)
    extra = sorted(actual - expected)
    if missing or extra:
        pieces = []
        if missing:
            pieces.append("missing: %s" % ", ".join(missing))
        if extra:
            pieces.append("unsupported: %s" % ", ".join(extra))
        raise ValueError(
            "fetch-backed ZCC resource set changed (%s); update and review "
            "the transform catalog contract before regenerating"
            % "; ".join(pieces)
        )


def _catalog_encoding(encoding, path):
    if isinstance(encoding, str):
        if encoding not in _PRIMITIVE_ENCODINGS:
            raise ValueError(
                "%s has unsupported primitive encoding %r"
                % (path, encoding)
            )
        return encoding
    if isinstance(encoding, list) and len(encoding) == 2:
        kind, inner = encoding
        if kind == "list" and inner in _PRIMITIVE_ENCODINGS:
            return ["list", inner]
    raise ValueError("%s has unsupported type encoding %r" % (path, encoding))


def _projection(block, path, resource_top=False):
    classification = (
        resource_input_attrs(block)
        if resource_top else classify_attributes(block)
    )
    silently_ignored = sorted(classification["computed_only"])
    if resource_top:
        if silently_ignored != ["id"]:
            raise ValueError(
                "%s must silently ignore only the computed top-level id; "
                "found %r" % (path, silently_ignored)
            )
    elif silently_ignored:
        raise ValueError(
            "%s has unsupported computed-only nested attributes %r"
            % (path, silently_ignored)
        )

    attributes = {}
    schema_attributes = block.get("attributes") or {}
    for name in sorted(
            classification["required"] + classification["optional"]):
        attributes[name] = _catalog_encoding(
            attr_type(schema_attributes[name]), "%s.%s" % (path, name)
        )

    supported_blocks = input_block_types(block)
    all_blocks = block.get("block_types") or {}
    omitted_blocks = sorted(set(all_blocks) - set(supported_blocks))
    if omitted_blocks:
        raise ValueError(
            "%s has unsupported computed-only nested blocks %r"
            % (path, omitted_blocks)
        )
    blocks = {}
    for name, block_type in sorted(supported_blocks.items()):
        nesting_mode = block_type.get("nesting_mode")
        if nesting_mode not in ("single", "list", "set"):
            raise ValueError(
                "%s.%s has unsupported block nesting mode %r"
                % (path, name, nesting_mode)
            )
        blocks[name] = {
            "cardinality": (
                "single" if block_is_single(block_type) else "many"
            ),
            "projection": _projection(
                block_type["block"], "%s.%s[]" % (path, name)
            ),
        }
    return {
        "attributes": attributes,
        "blocks": blocks,
        "silently_ignored_attributes": silently_ignored,
    }


def _supported_override(resource_type, override):
    unsupported = sorted(set(override) - _SUPPORTED_OVERRIDE_KEYS)
    if unsupported:
        raise ValueError(
            "%s uses unsupported transform override key %r; extend and "
            "review the transform catalog contract before regenerating"
            % (resource_type, unsupported[0])
        )
    for field in ("acknowledged_drops", "invert_bool", "split_csv"):
        _override_string_list(resource_type, override, field)
    key_field = override.get("key_field", "name")
    key_fields = key_field if isinstance(key_field, list) else [key_field]
    if not key_fields or any(
            not isinstance(field, str) or not field for field in key_fields):
        raise ValueError(
            "%s key_field must be a non-empty string or list of non-empty "
            "strings" % resource_type
        )
    if len(key_fields) != len(set(key_fields)):
        raise ValueError("%s key_field contains duplicates" % resource_type)
    # Composite identity order is transform-significant: derive_key joins the
    # authored fields in this order.  A source list is already deterministic;
    # sorting it would silently change map keys.
    return list(key_fields)


def _override_string_list(resource_type, override, field):
    if field not in override:
        return []
    value = override[field]
    if not isinstance(value, list):
        raise ValueError("%s.%s must be a list" % (resource_type, field))
    seen = set()
    for index, item in enumerate(value):
        if not isinstance(item, str) or not item:
            raise ValueError(
                "%s.%s[%d] must be a non-empty string"
                % (resource_type, field, index)
            )
        if item in seen:
            raise ValueError(
                "%s.%s duplicates %r" % (resource_type, field, item)
            )
        seen.add(item)
    return sorted(value)


def _html_unescape_passes(resource_type, override):
    enabled = resource_type.startswith(
        packs.unescape_products_for_provider(ZCC_PRODUCT)
    )
    return 2 if enabled and not override.get("no_html_unescape") else 0


def _resource(resource_type):
    override = load_override(resource_type)
    key_fields = _supported_override(resource_type, override)
    return {
        "acknowledged_drops": _override_string_list(
            resource_type, override, "acknowledged_drops"
        ),
        "html_unescape_passes": _html_unescape_passes(
            resource_type, override
        ),
        "invert_bool": _override_string_list(
            resource_type, override, "invert_bool"
        ),
        "key_fields": key_fields,
        "projection": _projection(
            load_resource(resource_type)["block"],
            resource_type,
            resource_top=True,
        ),
        "renames": dict(sorted((override.get("renames") or {}).items())),
        "split_csv": _override_string_list(
            resource_type, override, "split_csv"
        ),
        "type": resource_type,
    }


def _python_html_unescape_compatibility():
    try:
        entities = html.entities.html5
        invalid_references = html._invalid_charrefs
        invalid_codepoints = html._invalid_codepoints
    except AttributeError as exc:
        raise ValueError(
            "Python html.unescape compatibility tables are unavailable: %s"
            % exc
        )
    if not isinstance(entities, dict):
        raise ValueError("html.entities.html5 must be a dictionary")
    if not isinstance(invalid_references, dict):
        raise ValueError("html._invalid_charrefs must be a dictionary")
    return {
        "entities": dict(sorted(entities.items())),
        "invalid_codepoints": sorted(invalid_codepoints),
        "invalid_references": dict(
            (str(codepoint), replacement)
            for codepoint, replacement in sorted(invalid_references.items())
        ),
    }


def _source_paths(resources):
    pack_dir = os.path.abspath(packs.pack_dir_for_provider(ZCC_PRODUCT))
    paths = [
        os.path.join(pack_dir, "pack.json"),
        os.path.join(pack_dir, "registry.json"),
        packs.schema_path_for(ZCC_PRODUCT),
    ]
    paths.extend(
        os.path.join(pack_dir, "overrides", resource_type + ".json")
        for resource_type in resources
    )
    missing = [path for path in paths if not os.path.isfile(path)]
    if missing:
        raise ValueError("transform catalog source is missing: %s" % missing[0])
    return sorted(paths)


def _source_digest(paths):
    root = os.path.abspath(packs.packs_root())
    digest = hashlib.sha256()
    relative_paths = []
    for path in paths:
        relative = os.path.relpath(path, root).replace(os.sep, "/")
        if relative == ".." or relative.startswith("../"):
            raise ValueError(
                "transform catalog source escapes packs root: %s" % path
            )
        relative_paths.append(relative)
        with open(path, "rb") as f:
            content = f.read()
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(content)
        digest.update(b"\0")
    return relative_paths, digest.hexdigest()


def transform_catalog(product):
    """Return the closed, deterministic ZCC transform contract."""
    _validate_product(product)
    resources = _fetch_resources(product)
    source_files, sources_sha256 = _source_digest(_source_paths(resources))
    return {
        "kind": TRANSFORM_CATALOG_CONTRACT,
        "product": product,
        "python_compatibility": {
            "html_unescape": _python_html_unescape_compatibility(),
        },
        "resources": [_resource(resource_type) for resource_type in resources],
        "schema_version": TRANSFORM_CATALOG_SCHEMA_VERSION,
        "source_files": source_files,
        "sources_sha256": sources_sha256,
    }


def render_catalog(product):
    return json.dumps(
        transform_catalog(product), indent=2, sort_keys=True
    ) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(prog="python -m engine.transform_catalog")
    parser.add_argument("--product", required=True)
    output = parser.add_mutually_exclusive_group()
    output.add_argument("--out")
    output.add_argument("--check")
    args = parser.parse_args(argv)
    try:
        text = render_catalog(args.product)
        if args.check:
            with open(args.check, encoding="utf-8") as f:
                actual = f.read()
            if actual != text:
                sys.stderr.write(
                    "error: transform catalog is stale: %s\n" % args.check
                )
                return 1
            return 0
        if args.out:
            with open(args.out, "w", encoding="utf-8") as f:
                f.write(text)
        else:
            sys.stdout.write(text)
    except (IOError, OSError, KeyError, TypeError, ValueError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
