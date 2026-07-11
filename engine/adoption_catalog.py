"""Build the closed ZCC adoption catalog consumed by the Node host.

The Python pack registry, adoption metadata resolver, and committed provider
schema remain the authoritative authoring surface during the migration.  This
module compiles the deliberately narrow, versioned identity and writable-state
projection contract for the five fetch-backed ZCC resources.  It does not run
Terraform, inspect credentials, or expose provider state.
"""
import argparse
import hashlib
import json
import math
import os
import re
import string
import sys

from engine import packs
from engine.adoption_meta import adoption_entry
from engine.registry import load_registry
from engine.tfschema import (
    attr_type,
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)
from engine.transform import snake


ADOPTION_CATALOG_CONTRACT = "infrawright.adoption_catalog"
ADOPTION_CATALOG_SCHEMA_VERSION = 1
ZCC_PRODUCT = "zcc"
ZCC_ADOPTION_RESOURCES = (
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
)

_PRIMITIVE_ENCODINGS = frozenset(["bool", "number", "string"])
_FIELD_NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")
_FIELD_PATH_RE = re.compile(
    r"^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$"
)


def _validate_product(product):
    if product != ZCC_PRODUCT:
        raise ValueError(
            "unsupported adoption catalog product %r; expected %r"
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
    expected = set(ZCC_ADOPTION_RESOURCES)
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
            "the adoption catalog contract before regenerating"
            % "; ".join(pieces)
        )


def _pack_manifest(product):
    pack_dir = os.path.abspath(packs.pack_dir_for_provider(product))
    manifest_path = os.path.join(pack_dir, "pack.json")
    with open(manifest_path, encoding="utf-8") as f:
        manifest = json.load(f)
    if not isinstance(manifest, dict):
        raise ValueError(
            "adoption catalog pack manifest must contain an object"
        )
    return manifest


def _provider_metadata(product, manifest):
    owners = set((manifest.get("provider_prefixes") or {}).values())
    if product not in owners:
        raise ValueError(
            "adoption catalog pack does not own provider %r" % product
        )
    source = (manifest.get("provider_sources") or {}).get(product)
    version = manifest.get("pin")
    if not isinstance(source, str) or not source:
        raise ValueError(
            "adoption catalog provider %r has no source pin" % product
        )
    if not isinstance(version, str) or not version:
        raise ValueError(
            "adoption catalog provider %r has no version pin" % product
        )
    return {
        "name": product,
        "source": source,
        "version": version,
    }


def _field_path(value, label):
    if not isinstance(value, str) or not value:
        raise ValueError("%s must be a non-empty string" % label)
    normalized = ".".join(snake(segment) for segment in value.split("."))
    if not _FIELD_PATH_RE.match(normalized):
        raise ValueError(
            "%s %r does not normalize to a supported field path"
            % (label, value)
        )
    return normalized


def _key_fields(resource_type, meta):
    constant_key = meta.get("constant_key")
    raw = meta.get("key_field", "name")
    if constant_key is not None:
        if not isinstance(constant_key, str) or not constant_key:
            raise ValueError(
                "%s adopt.constant_key must be a non-empty string"
                % resource_type
            )
        return constant_key, []
    fields = raw if isinstance(raw, list) else [raw]
    if not fields:
        raise ValueError(
            "%s adopt.key_field must not be empty" % resource_type
        )
    normalized = [
        _field_path(field, "%s adopt.key_field" % resource_type)
        for field in fields
    ]
    if len(normalized) != len(set(normalized)):
        raise ValueError(
            "%s adopt.key_field contains duplicate normalized paths"
            % resource_type
        )
    return None, normalized


def _identity_renames(resource_type, value):
    if not isinstance(value, dict):
        raise ValueError(
            "%s adopt.identity_renames must be an object" % resource_type
        )
    out = {}
    for old, new in sorted(value.items()):
        old_name = _field_path(
            old, "%s adopt.identity_renames source" % resource_type
        )
        new_name = _field_path(
            new, "%s adopt.identity_renames target" % resource_type
        )
        if "." in old_name or "." in new_name:
            raise ValueError(
                "%s adopt.identity_renames supports top-level fields only"
                % resource_type
            )
        if old_name in out and out[old_name] != new_name:
            raise ValueError(
                "%s adopt.identity_renames has a normalized source collision"
                % resource_type
            )
        out[old_name] = new_name
    targets = list(out.values())
    if len(targets) != len(set(targets)):
        raise ValueError(
            "%s adopt.identity_renames has a normalized target collision"
            % resource_type
        )
    if set(out).intersection(targets):
        raise ValueError(
            "%s adopt.identity_renames contains an order-dependent chain"
            % resource_type
        )
    return dict(sorted(out.items()))


def _identity_fields(resource_type, value):
    if not isinstance(value, dict):
        raise ValueError(
            "%s adopt.identity_fields must be an object" % resource_type
        )
    out = {}
    for alias, path in sorted(value.items()):
        alias_name = _field_path(
            alias, "%s adopt.identity_fields alias" % resource_type
        )
        if "." in alias_name:
            raise ValueError(
                "%s adopt.identity_fields aliases must be top-level fields"
                % resource_type
            )
        if alias_name in out:
            raise ValueError(
                "%s adopt.identity_fields has a normalized alias collision"
                % resource_type
            )
        out[alias_name] = _field_path(
            path, "%s adopt.identity_fields.%s" % (resource_type, alias_name)
        )
    return dict(sorted(out.items()))


def _matcher_value(value, label, numeric):
    if numeric:
        if (
            isinstance(value, bool)
            or not isinstance(value, (int, float))
            or not math.isfinite(value)
        ):
            raise ValueError("%s must be a finite JSON number" % label)
        return value
    if isinstance(value, (dict, list)):
        raise ValueError("%s must be a scalar JSON value" % label)
    if value is not None and not isinstance(value, (bool, int, float, str)):
        raise ValueError("%s must be a scalar JSON value" % label)
    if isinstance(value, float) and not math.isfinite(value):
        raise ValueError("%s must be a finite JSON number" % label)
    return value


def _skip_matchers(resource_type, name, value, numeric=False):
    if not isinstance(value, list):
        raise ValueError("%s adopt.%s must be a list" % (resource_type, name))
    out = []
    for index, matcher in enumerate(value):
        label = "%s adopt.%s[%d]" % (resource_type, name, index)
        if not isinstance(matcher, dict) or not matcher:
            raise ValueError("%s must be a non-empty object" % label)
        normalized = {}
        for field, expected in sorted(matcher.items()):
            field_name = _field_path(field, "%s field" % label)
            if "." in field_name:
                raise ValueError("%s supports top-level fields only" % label)
            if field_name in normalized:
                raise ValueError("%s has a normalized field collision" % label)
            normalized[field_name] = _matcher_value(
                expected, "%s.%s" % (label, field_name), numeric
            )
        out.append(dict(sorted(normalized.items())))
    return out


def _append_import_literal(segments, literal):
    if not literal:
        return
    if segments and "literal" in segments[-1]:
        segments[-1]["literal"] += literal
    else:
        segments.append({"literal": literal})


def _compile_import_id(resource_type, template, available_fields):
    if not isinstance(template, str) or not template:
        raise ValueError(
            "%s adopt.import_id must be a non-empty string" % resource_type
        )
    segments = []
    try:
        parsed = string.Formatter().parse(template)
        for literal, field, format_spec, conversion in parsed:
            _append_import_literal(segments, literal)
            if field is None:
                continue
            if not field:
                raise ValueError("positional or empty replacement field")
            if conversion is not None:
                raise ValueError("conversions are unsupported")
            if format_spec:
                raise ValueError("format specs are unsupported")
            normalized = _field_path(
                field, "%s adopt.import_id field" % resource_type
            )
            if "." in normalized or not _FIELD_NAME_RE.match(normalized):
                raise ValueError("nested replacement fields are unsupported")
            if normalized not in available_fields:
                raise ValueError(
                    "field %r is unavailable in adoption identity"
                    % normalized
                )
            segments.append({"field": normalized})
    except ValueError as exc:
        raise ValueError(
            "%s adopt.import_id template %r is unsupported: %s"
            % (resource_type, template, exc)
        )
    if not segments:
        raise ValueError(
            "%s adopt.import_id must produce at least one segment"
            % resource_type
        )
    return {"segments": segments, "template": template}


def _identity(resource_type, block):
    meta = adoption_entry(resource_type)
    expected_keys = set([
        "constant_key",
        "identity_fields",
        "identity_renames",
        "import_id",
        "key_field",
        "skip_if",
        "skip_if_lte",
    ])
    unexpected = sorted(set(meta) - expected_keys)
    if unexpected:
        raise ValueError(
            "%s adoption metadata exposes unsupported key %r"
            % (resource_type, unexpected[0])
        )
    constant_key, key_fields = _key_fields(resource_type, meta)
    renames = _identity_renames(
        resource_type, meta.get("identity_renames") or {}
    )
    fields = _identity_fields(
        resource_type, meta.get("identity_fields") or {}
    )
    available = set((block.get("attributes") or {}).keys())
    available.update(renames.values())
    available.update(fields)
    available.update(path.split(".", 1)[0] for path in key_fields)
    return {
        "constant_key": constant_key,
        "identity_fields": fields,
        "identity_renames": renames,
        "import_id": _compile_import_id(
            resource_type, meta.get("import_id", "{id}"), available
        ),
        "key_fields": key_fields,
        "skip_if": _skip_matchers(
            resource_type, "skip_if", meta.get("skip_if") or []
        ),
        "skip_if_lte": _skip_matchers(
            resource_type,
            "skip_if_lte",
            meta.get("skip_if_lte") or [],
            numeric=True,
        ),
    }


def _catalog_encoding(encoding, path):
    if isinstance(encoding, str):
        if encoding not in _PRIMITIVE_ENCODINGS:
            raise ValueError(
                "%s has unsupported primitive encoding %r" % (path, encoding)
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
    attributes = {}
    schema_attributes = block.get("attributes") or {}
    for status in ("required", "optional"):
        for name in classification[status]:
            attribute = schema_attributes[name]
            attributes[name] = {
                "encoding": _catalog_encoding(
                    attr_type(attribute), "%s.%s" % (path, name)
                ),
                "provider_sensitive": bool(attribute.get("sensitive")),
                "status": status,
            }

    supported_blocks = input_block_types(block)
    all_blocks = block.get("block_types") or {}
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
            "nesting_mode": nesting_mode,
            "projection": _projection(
                block_type["block"], "%s.%s[]" % (path, name)
            ),
            "status": (
                "required"
                if (block_type.get("min_items") or 0) >= 1
                else "optional"
            ),
        }
    return {
        "attributes": dict(sorted(attributes.items())),
        "blocks": blocks,
        "computed_only_attributes": sorted(classification["computed_only"]),
        "computed_only_blocks": sorted(set(all_blocks) - set(supported_blocks)),
    }


def _resource(resource_type, lookup_sources):
    block = load_resource(resource_type)["block"]
    lookup_source = lookup_sources.get(resource_type)
    if lookup_source is not None:
        name_field = lookup_source.get("name_field")
        if not isinstance(name_field, str) or not _FIELD_NAME_RE.match(
                snake(name_field)):
            raise ValueError(
                "%s lookup source has an unsupported name_field"
                % resource_type
            )
        lookup_source = {"name_field": snake(name_field)}
    return {
        "identity": _identity(resource_type, block),
        "lookup_source": lookup_source,
        "projection": _projection(block, resource_type, resource_top=True),
        "type": resource_type,
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
        raise ValueError("adoption catalog source is missing: %s" % missing[0])
    return sorted(paths)


def _source_digest(paths):
    root = os.path.abspath(packs.packs_root())
    digest = hashlib.sha256()
    relative_paths = []
    for path in paths:
        relative = os.path.relpath(path, root).replace(os.sep, "/")
        if relative == ".." or relative.startswith("../"):
            raise ValueError(
                "adoption catalog source escapes packs root: %s" % path
            )
        relative_paths.append(relative)
        with open(path, "rb") as f:
            content = f.read()
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(content)
        digest.update(b"\0")
    return relative_paths, digest.hexdigest()


def adoption_catalog(product):
    """Return the closed, deterministic ZCC adoption contract."""
    _validate_product(product)
    resources = _fetch_resources(product)
    source_files, sources_sha256 = _source_digest(_source_paths(resources))
    manifest = _pack_manifest(product)
    lookup_sources = manifest.get("lookup_sources") or {}
    if not isinstance(lookup_sources, dict):
        raise ValueError(
            "adoption catalog pack lookup_sources must contain an object"
        )
    return {
        "kind": ADOPTION_CATALOG_CONTRACT,
        "product": product,
        "provider": _provider_metadata(product, manifest),
        "resources": [
            _resource(resource_type, lookup_sources)
            for resource_type in resources
        ],
        "schema_version": ADOPTION_CATALOG_SCHEMA_VERSION,
        "source_files": source_files,
        "sources_sha256": sources_sha256,
    }


def render_catalog(product):
    return json.dumps(
        adoption_catalog(product), indent=2, sort_keys=True
    ) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(prog="python -m engine.adoption_catalog")
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
                    "error: adoption catalog is stale: %s\n" % args.check
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
