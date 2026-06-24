"""Shared path and Terraform schema helpers for diagnostic modules.

This module is intentionally low-level plumbing. It normalizes report paths and
answers small schema questions, but it does not decide diagnostic policy.
"""
import re

from engine import path_inventory
from engine.tfschema import (
    attr_type,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


LIST_MARKER = path_inventory.LIST_MARKER


def parse_report_path(path):
    """Parse a dotted report path and normalize collection selectors.

    Supported collection spellings are ``foo[]``, ``foo[*]``, and ``foo[0]``.
    All format back to ``foo[]``. Dots are always path separators; map keys
    containing literal dots need a future escaped or JSON-pointer syntax.
    """
    if path == "<root>":
        return ()
    out = []
    for raw in str(path).split("."):
        if raw == "":
            raise ValueError("empty path segment in %r" % path)
        if raw == "*":
            out.append(LIST_MARKER)
            continue
        match = re.match(r"^(.*)\[(\*|\d*)\]$", raw)
        if match:
            name, _index = match.groups()
            if not name:
                raise ValueError("empty collection path segment in %r" % path)
            out.append(name)
            out.append(LIST_MARKER)
        else:
            out.append(raw)
    return tuple(out)


def normalize_path(path):
    """Return a tuple path with numeric indexes and ``*`` normalized to ``[]``."""
    if isinstance(path, str):
        return parse_report_path(path)
    return tuple(
        LIST_MARKER
        if isinstance(segment, int) or segment == "*" else segment
        for segment in path
    )


def format_path(path):
    """Format a path using the normalized diagnostic ``[]`` list marker."""
    return path_inventory.format_path(normalize_path(path))


def strip_collection_selector(path):
    """Remove a leading collection selector from a path tail if present."""
    if path and (
            path[0] == LIST_MARKER
            or path[0] == "*"
            or isinstance(path[0], int)):
        return path[1:]
    return path


def container_paths(value):
    """Return formatted paths for dict/list containers in a JSON-like value."""
    out = set()
    _walk_containers(value, (), out)
    return out


def _walk_containers(value, path, out):
    if isinstance(value, dict):
        if path:
            out.add(format_path(path))
        for key in sorted(value, key=lambda item: str(item)):
            _walk_containers(value[key], path + (str(key),), out)
        return
    if isinstance(value, list):
        if path:
            out.add(format_path(path))
        for child in value:
            _walk_containers(child, path + (LIST_MARKER,), out)


def schema_status(resource_type, path, block_mode="block"):
    """Return required/optional/computed_only/block/unknown for a schema path.

    ``block_mode`` controls a whole nested block path:
      - ``"block"`` preserves older absent-default behavior.
      - ``"requiredness"`` reports required/optional from block min_items.
    """
    block = load_resource(resource_type)["block"]
    return schema_status_for_block(block, path, True, block_mode)


def schema_status_for_block(block, path, resource_top=True, block_mode="block"):
    path = normalize_path(path)
    return _schema_status_block(block, path, resource_top, block_mode)


def _schema_status_block(block, path, resource_top, block_mode):
    if not path:
        return "block"
    segment = path[0]
    if not isinstance(segment, str) or segment == LIST_MARKER:
        return "unknown"
    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    attrs = block.get("attributes") or {}
    blocks = input_block_types(block)
    if segment in cls["required"] or segment in cls["optional"]:
        base = "required" if segment in cls["required"] else "optional"
        if len(path) == 1:
            return base
        return _schema_status_encoding(attr_type(attrs[segment]), path[1:], base)
    if segment in blocks:
        bt = blocks[segment]
        if len(path) == 1 and block_mode == "requiredness":
            return "required" if (bt.get("min_items") or 0) >= 1 else "optional"
        remaining = strip_collection_selector(path[1:])
        return _schema_status_block(
            bt["block"], remaining, resource_top=False, block_mode=block_mode
        )
    if segment in attrs or segment in (block.get("block_types") or {}):
        return "computed_only"
    return "unknown"


def _schema_status_encoding(encoding, path, base):
    if not path:
        return base
    if isinstance(encoding, list) and len(encoding) == 2:
        kind, inner = encoding
        if kind in ("list", "set"):
            return _schema_status_encoding(
                inner, strip_collection_selector(path), base
            )
        if kind == "map":
            return base
        if kind == "object" and isinstance(inner, dict):
            child = path[0]
            if isinstance(child, str) and child in inner:
                return _schema_status_encoding(inner[child], path[1:], base)
    return "unknown"
