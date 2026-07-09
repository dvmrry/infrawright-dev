"""Shared projection_fill helpers for raw-pull sourced adoption values."""

import copy

from engine import schema_paths
from engine import transform
from engine.tfschema import attr_type, block_has_sensitive, input_block_types
from engine.tfschema import load_resource, resource_input_attrs


class ProjectionFillError(ValueError):
    pass


def has_projection_fill(policy, resource_type):
    return bool(policy and policy.entries(resource_type, "projection_fill"))


def fill_value_from_raw(resource_type, entry, raw_item):
    target = entry["path"]
    source = entry["source"]
    _guard_fill_target(resource_type, target)
    if raw_item is None:
        raise ProjectionFillError(
            "%s projection_fill path %s requires the raw API item"
            % (resource_type, target)
        )
    if not isinstance(raw_item, dict):
        raise ProjectionFillError(
            "%s projection_fill path %s raw API item is not an object"
            % (resource_type, target)
        )
    if source not in raw_item:
        return None
    raw_value = raw_item[source]
    if _empty_fill_value(raw_value):
        return None

    block = load_resource(resource_type)["block"]
    shaped_input = {target: transform.snake_keys(raw_value)}
    drops = []
    shaped = transform.filter_item(
        shaped_input, block, "", drops, resource_top=True)
    coerced = transform.coerce_item(shaped, block)
    if target not in coerced:
        return None
    value = coerced[target]
    if _empty_fill_value(value):
        return None
    return copy.deepcopy(value)


def fill_target_kind(resource_type, target):
    block = load_resource(resource_type)["block"]
    cls = resource_input_attrs(block)
    if target in set(cls["required"] + cls["optional"]):
        return "attribute"
    if target in input_block_types(block):
        return "block"
    return None


def fill_target_block_type(resource_type, target):
    return input_block_types(load_resource(resource_type)["block"])[target]


def _guard_fill_target(resource_type, target):
    status = schema_paths.schema_status(
        resource_type, (target,), block_mode="requiredness")
    if status not in ("required", "optional"):
        raise ProjectionFillError(
            "refusing to projection_fill path %s of %s: not a writable input"
            % (target, resource_type)
        )
    block = load_resource(resource_type)["block"]
    attrs = block.get("attributes") or {}
    if target in attrs and _attr_has_sensitive(attrs[target]):
        raise ProjectionFillError(
            "refusing to projection_fill sensitive path %s of %s"
            % (target, resource_type)
        )
    blocks = input_block_types(block)
    if target in blocks and block_has_sensitive(blocks[target]["block"]):
        raise ProjectionFillError(
            "refusing to projection_fill sensitive block %s of %s"
            % (target, resource_type)
        )


def _attr_has_sensitive(attr):
    if attr.get("sensitive"):
        return True
    nested = attr.get("nested_type")
    if isinstance(nested, dict):
        return _nested_type_has_sensitive(nested)
    # Plain Terraform type encodings do not carry per-member sensitivity in
    # schema JSON; attr-level sensitive above is the authoritative flag.
    attr_type(attr)
    return False


def _nested_type_has_sensitive(nested):
    for attr in (nested.get("attributes") or {}).values():
        if _attr_has_sensitive(attr):
            return True
    return False


def _empty_fill_value(value):
    if value is None or value == "":
        return True
    if isinstance(value, dict):
        return not value
    if isinstance(value, list):
        if not value:
            return True
        return all(_empty_fill_value(item) for item in value)
    return False
