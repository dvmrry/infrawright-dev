"""Build closed transform catalogs consumed by the Node host.

The existing Python pack, override, and provider-schema loaders remain the
authoritative authoring surface during the migration.  This module compiles
the deliberately narrow, versioned subsets needed to reproduce reviewed
transforms without invoking Python at runtime.  The default contract remains
the exact existing five-resource catalog; explicit resource selection emits
a compact product cohort for private migration differentials.
"""
import argparse
import hashlib
import html
import html.entities
import json
import os
import re
import string
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
TRANSFORM_RESOURCE_COHORT_CONTRACT = "infrawright.transform_resource_cohort"
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
_COHORT_ENCODED_OVERRIDE_KEYS = frozenset(["sort_lists"])
_COHORT_AUTHORING_ONLY_OVERRIDE_KEYS = frozenset(["sample"])
_PRIMITIVE_ENCODINGS = frozenset(["bool", "number", "string"])
_FIELD_NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")


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
        if kind in ("set", "map") and inner == "string":
            return [kind, inner]
    raise ValueError("%s has unsupported type encoding %r" % (path, encoding))


def _projection(
        block, path, resource_top=False,
        allow_reported_top_computed=False):
    classification = (
        resource_input_attrs(block)
        if resource_top else classify_attributes(block)
    )
    computed_only = sorted(classification["computed_only"])
    silently_ignored = ["id"] if "id" in computed_only else []
    if resource_top:
        if not allow_reported_top_computed and computed_only != ["id"]:
            raise ValueError(
                "%s must silently ignore only the computed top-level id; "
                "found %r" % (path, computed_only)
            )
        if allow_reported_top_computed and "id" not in computed_only:
            raise ValueError(
                "%s must expose a computed top-level id" % path
            )
    elif computed_only:
        raise ValueError(
            "%s has unsupported computed-only nested attributes %r"
            % (path, computed_only)
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


def _supported_override(
        resource_type, override, additional_keys=frozenset()):
    supported = _SUPPORTED_OVERRIDE_KEYS | frozenset(additional_keys)
    unsupported = sorted(set(override) - supported)
    if unsupported:
        raise ValueError(
            "%s uses unsupported transform override key %r; extend and "
            "review the transform catalog contract before regenerating"
            % (resource_type, unsupported[0])
        )
    for field in (
            "acknowledged_drops", "invert_bool", "split_csv"):
        _override_string_list(resource_type, override, field)
    if "sort_lists" in additional_keys:
        _override_string_list(resource_type, override, "sort_lists")
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


def _html_unescape_passes(
        resource_type, override, product=None):
    owner = product or packs.provider_of(resource_type)
    enabled = resource_type.startswith(
        packs.unescape_products_for_provider(owner)
    )
    return 2 if enabled and not override.get("no_html_unescape") else 0


def _normalized_original_fields(block, override):
    fields = set((block.get("attributes") or {}).keys())
    for field in override.get("acknowledged_drops") or []:
        if "." not in field and "[" not in field:
            fields.add(field)
    for old, new in (override.get("renames") or {}).items():
        fields.discard(old)
        fields.add(new)
    return fields


def _append_import_literal(segments, literal):
    if not literal:
        return
    if segments and "literal" in segments[-1]:
        segments[-1]["literal"] += literal
    else:
        segments.append({"literal": literal})


def _compile_import_id(resource_type, override, available_fields):
    template = override.get("import_id", "{id}")
    if not isinstance(template, str) or not template:
        raise ValueError(
            "%s import_id must be a non-empty string" % resource_type
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
            if not _FIELD_NAME_RE.match(field):
                raise ValueError(
                    "field %r must be a snake_case top-level name" % field
                )
            if field not in available_fields:
                raise ValueError(
                    "field %r is unavailable in normalized originals" % field
                )
            segments.append({"field": field})
    except ValueError as exc:
        raise ValueError(
            "%s import_id template %r is unsupported: %s"
            % (resource_type, template, exc)
        )
    if not segments:
        raise ValueError(
            "%s import_id template must produce at least one segment"
            % resource_type
        )
    return {"segments": segments, "template": template}


def _lookup_source(resource_type, metadata, projection):
    source = metadata.get(resource_type)
    if source is None:
        return None
    name_field = source.get("name_field")
    if (
        not isinstance(name_field, str)
        or not _FIELD_NAME_RE.match(name_field)
    ):
        raise ValueError(
            "%s lookup_source.name_field must be a snake_case field"
            % resource_type
        )
    if name_field not in projection["attributes"]:
        raise ValueError(
            "%s lookup_source.name_field %r is absent from its projection"
            % (resource_type, name_field)
        )
    return {"name_field": name_field}


def _resource_references(
        resource_type, metadata, lookup_sources, projection, resource_types):
    references = {}
    for field, reference in sorted((metadata.get(resource_type) or {}).items()):
        if field not in projection["attributes"]:
            raise ValueError(
                "%s reference field %r is absent from its projection"
                % (resource_type, field)
            )
        referent = reference.get("referent")
        if referent not in resource_types:
            raise ValueError(
                "%s reference field %r has unsupported referent %r"
                % (resource_type, field, referent)
            )
        if referent not in lookup_sources:
            raise ValueError(
                "%s reference field %r targets %s without lookup_source"
                % (resource_type, field, referent)
            )
        name_field = reference.get("name_field")
        if (
            not isinstance(name_field, str)
            or not _FIELD_NAME_RE.match(name_field)
        ):
            raise ValueError(
                "%s reference field %r name_field must be snake_case"
                % (resource_type, field)
            )
        references[field] = {
            "name_field": name_field,
            "referent": referent,
        }
    return references


def _resource(
        resource_type, resource_types, lookup_sources, references,
        product=None, cohort=False):
    override = load_override(resource_type)
    additional_keys = frozenset()
    if cohort:
        # `sample` is consumed only by engine.gen_module when rendering module
        # examples; engine.transform.apply_overrides never reads it. Keep the
        # source file in the cohort digest, but explicitly exclude this
        # authoring-only value from the runtime contract rather than silently
        # admitting it into the encoded override vocabulary.
        override = dict(
            (key, value)
            for key, value in override.items()
            if key not in _COHORT_AUTHORING_ONLY_OVERRIDE_KEYS
        )
        additional_keys = _COHORT_ENCODED_OVERRIDE_KEYS
    key_fields = _supported_override(
        resource_type, override, additional_keys=additional_keys
    )
    block = load_resource(resource_type)["block"]
    projection = _projection(
        block,
        resource_type,
        resource_top=True,
        allow_reported_top_computed=cohort,
    )
    resource = {
        "acknowledged_drops": _override_string_list(
            resource_type, override, "acknowledged_drops"
        ),
        "html_unescape_passes": _html_unescape_passes(
            resource_type, override, product=product
        ),
        "invert_bool": _override_string_list(
            resource_type, override, "invert_bool"
        ),
        "import_id": _compile_import_id(
            resource_type,
            override,
            _normalized_original_fields(block, override),
        ),
        "key_fields": key_fields,
        "lookup_source": _lookup_source(
            resource_type, lookup_sources, projection
        ),
        "projection": projection,
        "references": _resource_references(
            resource_type,
            references,
            lookup_sources,
            projection,
            resource_types,
        ),
        "renames": dict(sorted((override.get("renames") or {}).items())),
        "split_csv": _override_string_list(
            resource_type, override, "split_csv"
        ),
        "type": resource_type,
    }
    sort_lists = (
        _override_string_list(resource_type, override, "sort_lists")
        if cohort else []
    )
    if sort_lists:
        resource["sort_lists"] = sort_lists
    return resource


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


def _source_paths(
        resources, product=None, allow_missing_overrides=False):
    if not resources:
        raise ValueError("transform catalog requires at least one resource")
    owner = product or packs.provider_of(resources[0])
    pack_dir = os.path.abspath(packs.pack_dir_for_provider(owner))
    paths = [
        os.path.join(pack_dir, "pack.json"),
        os.path.join(pack_dir, "registry.json"),
        packs.schema_path_for(owner),
    ]
    override_paths = [
        os.path.join(pack_dir, "overrides", resource_type + ".json")
        for resource_type in resources
    ]
    paths.extend(
        path for path in override_paths
        if not allow_missing_overrides or os.path.isfile(path)
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
    resource_types = frozenset(resources)
    source_files, sources_sha256 = _source_digest(_source_paths(resources))
    lookup_sources = packs.lookup_sources_for_provider(product)
    references = packs.references_for_provider(product)
    return {
        "kind": TRANSFORM_CATALOG_CONTRACT,
        "product": product,
        "python_compatibility": {
            "html_unescape": _python_html_unescape_compatibility(),
        },
        "resources": [
            _resource(
                resource_type,
                resource_types,
                lookup_sources,
                references,
            )
            for resource_type in resources
        ],
        "schema_version": TRANSFORM_CATALOG_SCHEMA_VERSION,
        "source_files": source_files,
        "sources_sha256": sources_sha256,
    }


def _selected_resources(product, resources):
    if not isinstance(product, str) or not product:
        raise ValueError("transform cohort product must be a non-empty string")
    if not resources:
        raise ValueError("transform cohort requires at least one resource")
    if len(resources) != len(set(resources)):
        raise ValueError("transform cohort resources must be unique")
    selected = sorted(resources)
    registry = load_registry()
    for resource_type in selected:
        entry = registry.get(resource_type)
        if not isinstance(entry, dict):
            raise ValueError(
                "transform cohort resource %r is absent from the registry"
                % resource_type
            )
        if entry.get("product") != product:
            raise ValueError(
                "transform cohort resource %s belongs to product %r, not %r"
                % (resource_type, entry.get("product"), product)
            )
        if "fetch" not in entry:
            raise ValueError(
                "transform cohort resource %s is not fetch-backed"
                % resource_type
            )
    return selected


def transform_resource_cohort(product, resources):
    """Return a compact, deterministic contract for explicit resources."""
    selected = _selected_resources(product, resources)
    resource_types = frozenset(selected)
    source_files, sources_sha256 = _source_digest(
        _source_paths(
            selected,
            product=product,
            allow_missing_overrides=True,
        )
    )
    lookup_sources = packs.lookup_sources_for_provider(product)
    references = packs.references_for_provider(product)
    return {
        "kind": TRANSFORM_RESOURCE_COHORT_CONTRACT,
        "product": product,
        "resources": [
            _resource(
                resource_type,
                resource_types,
                lookup_sources,
                references,
                product=product,
                cohort=True,
            )
            for resource_type in selected
        ],
        "schema_version": TRANSFORM_CATALOG_SCHEMA_VERSION,
        "source_files": source_files,
        "sources_sha256": sources_sha256,
    }


def render_catalog(product, resources=None):
    catalog = (
        transform_catalog(product)
        if resources is None
        else transform_resource_cohort(product, resources)
    )
    return json.dumps(
        catalog, indent=2, sort_keys=True
    ) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(prog="python -m engine.transform_catalog")
    parser.add_argument("--product", required=True)
    parser.add_argument(
        "--resource", action="append", dest="resources",
        help="emit a compact explicit-resource cohort (repeatable)",
    )
    output = parser.add_mutually_exclusive_group()
    output.add_argument("--out")
    output.add_argument("--check")
    args = parser.parse_args(argv)
    try:
        text = render_catalog(args.product, resources=args.resources)
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
