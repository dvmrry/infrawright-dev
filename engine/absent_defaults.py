"""Diagnostics for absent/default placeholder adoption drift.

Provider labs have shown providers sometimes represent absent optional values
as concrete placeholders such as "", 0, false, or empty collections. This
module classifies those shapes from static fixtures only. It does not change
projection, drift policy, plan gates, or generated config.
"""
import argparse
import json
import sys

from engine import path_inventory
from engine.plan_eval import diff_paths
from engine.tfschema import (
    attr_type,
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


def build_report(resource_type, projected_items_by_key=None, plan=None):
    projected_items_by_key = projected_items_by_key or {}
    projected = {}
    for key in sorted(projected_items_by_key):
        projected[key] = _projected_candidates(
            resource_type,
            projected_items_by_key[key],
        )
    plan_changes = _plan_candidates(resource_type, plan or {})
    return {
        "resource_type": resource_type,
        "summary": _summary(projected, plan_changes),
        "projected_items": projected,
        "plan_changes": plan_changes,
    }


def _projected_candidates(resource_type, item):
    block = load_resource(resource_type)["block"]
    out = []
    _walk_projected_block(
        item or {},
        block,
        path=(),
        resource_top=True,
        out=out,
    )
    return out


def _walk_projected_block(value, block, path, resource_top, out):
    if not isinstance(value, dict):
        return
    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    attrs = block.get("attributes") or {}
    for name in sorted(set(cls["required"]) | set(cls["optional"])):
        if name not in value:
            continue
        required = name in set(cls["required"])
        child_path = path + (name,)
        child = value[name]
        kind = absent_kind(child)
        if kind is not None:
            out.append(_projected_result(child_path, kind, required))
            continue
        _walk_typed_value(child, attr_type(attrs[name]), child_path, required, out)

    for name, bt in sorted(input_block_types(block).items()):
        if name not in value:
            continue
        required = (bt.get("min_items") or 0) >= 1
        child_path = path + (name,)
        child = value[name]
        kind = absent_kind(child)
        if kind is not None:
            out.append(_projected_result(child_path, kind, required))
            continue
        if block_is_single(bt):
            if isinstance(child, dict):
                _walk_projected_block(
                    child, bt["block"], child_path, resource_top=False, out=out
                )
        elif isinstance(child, list):
            for element in child:
                _walk_projected_block(
                    element,
                    bt["block"],
                    child_path + (path_inventory.LIST_MARKER,),
                    resource_top=False,
                    out=out,
                )


def _walk_typed_value(value, encoding, path, required, out):
    if isinstance(encoding, list) and len(encoding) == 2:
        kind, inner = encoding
        if kind in ("list", "set") and isinstance(value, list):
            for element in value:
                child_path = path + (path_inventory.LIST_MARKER,)
                child_kind = absent_kind(element)
                if child_kind is not None:
                    out.append(_projected_result(child_path, child_kind, required))
                else:
                    _walk_typed_value(element, inner, child_path, required, out)
        elif kind == "map" and isinstance(value, dict):
            for key, child in sorted(value.items()):
                child_path = path + (str(key),)
                child_kind = absent_kind(child)
                if child_kind is not None:
                    out.append(_projected_result(child_path, child_kind, required))
                else:
                    _walk_typed_value(child, inner, child_path, required, out)
        elif kind == "object" and isinstance(inner, dict) and isinstance(value, dict):
            for key, child in sorted(value.items()):
                if key not in inner:
                    continue
                child_path = path + (str(key),)
                child_kind = absent_kind(child)
                if child_kind is not None:
                    out.append(_projected_result(child_path, child_kind, required))
                else:
                    _walk_typed_value(child, inner[key], child_path, required, out)


def _projected_result(path, kind, required):
    return {
        "status": (
            "required_placeholder_observed"
            if required else "absent_default_candidate"
        ),
        "path": _fmt(path),
        "value_kind": kind,
        "schema": "required" if required else "optional",
        "confidence": _confidence(kind),
        "source": "projected",
    }


def _plan_candidates(resource_type, plan):
    out = []
    for source in ("resource_changes", "resource_drift"):
        for rc in plan.get(source) or []:
            if rc.get("type") != resource_type:
                continue
            change = rc.get("change") or {}
            if "update" not in set(change.get("actions") or []):
                continue
            before = change.get("before")
            after = change.get("after")
            for path in sorted(set(diff_paths(before, after))):
                before_value = _path_value(before, path)
                after_value = _path_value(after, path)
                before_kind = absent_kind(before_value)
                after_kind = absent_kind(after_value)
                schema = _schema_status(resource_type, path)
                if before_kind is not None or after_kind is not None:
                    status = (
                        "absent_default_drift_candidate"
                        if schema == "optional" else "placeholder_update"
                    )
                    confidence = _plan_confidence(before_kind, after_kind, schema)
                else:
                    status = "other_update"
                    confidence = "none"
                out.append({
                    "status": status,
                    "source": source,
                    "address": rc.get("address"),
                    "path": _fmt(path),
                    "schema": schema,
                    "before_kind": before_kind or "value",
                    "after_kind": after_kind or "value",
                    "confidence": confidence,
                })
    return out


def absent_kind(value):
    if value is None or value is _MISSING:
        return "null"
    if value == "":
        return "empty_string"
    if value == "0":
        return "string_zero"
    if isinstance(value, bool):
        if value is False:
            return "false"
        return None
    if isinstance(value, (int, float)) and value == 0:
        return "zero"
    if value == []:
        return "empty_list"
    if value == {}:
        return "empty_object"
    return None


def _confidence(kind):
    if kind in ("empty_string", "zero", "string_zero"):
        return "medium"
    if kind in ("empty_list", "empty_object", "false", "null"):
        return "low"
    return "none"


def _plan_confidence(before_kind, after_kind, schema):
    if schema != "optional":
        return "low"
    kinds = set(k for k in (before_kind, after_kind) if k)
    if kinds & set(["empty_string", "zero", "string_zero"]):
        return "medium"
    return "low"


def _schema_status(resource_type, path):
    block = load_resource(resource_type)["block"]
    return _schema_status_block(block, path, resource_top=True)


def _schema_status_block(block, path, resource_top):
    if not path:
        return "block"
    segment = path[0]
    if not isinstance(segment, str) or segment == path_inventory.LIST_MARKER:
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
        remaining = _strip_collection_selector(path[1:])
        return _schema_status_block(
            blocks[segment]["block"], remaining, resource_top=False
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
                inner, _strip_collection_selector(path), base
            )
        if kind == "map":
            return base
        if kind == "object" and isinstance(inner, dict):
            child = path[0]
            if isinstance(child, str) and child in inner:
                return _schema_status_encoding(inner[child], path[1:], base)
    return "unknown"


def _strip_collection_selector(path):
    if path and (path[0] == path_inventory.LIST_MARKER or isinstance(path[0], int)):
        return path[1:]
    return path


_MISSING = object()


def _path_value(value, path):
    cur = value
    for segment in path:
        if isinstance(segment, int):
            if not isinstance(cur, list) or segment >= len(cur):
                return _MISSING
            cur = cur[segment]
        elif isinstance(cur, dict) and segment in cur:
            cur = cur[segment]
        else:
            return _MISSING
    return cur


def _fmt(path):
    normalized = tuple(
        path_inventory.LIST_MARKER if isinstance(segment, int) else segment
        for segment in path
    )
    return path_inventory.format_path(normalized)


def _summary(projected, plan_changes):
    projected_count = sum(len(items) for items in projected.values())
    required = sum(
        1 for items in projected.values() for item in items
        if item["status"] == "required_placeholder_observed"
    )
    plan_candidates = [
        item for item in plan_changes
        if item["status"] == "absent_default_drift_candidate"
    ]
    return {
        "items": len(projected),
        "projected_candidates": projected_count,
        "required_placeholders": required,
        "plan_changes": len(plan_changes),
        "plan_absent_default_candidates": len(plan_candidates),
        "plan_other_updates": len(
            [item for item in plan_changes if item["status"] == "other_update"]
        ),
    }


def _read_projected(path):
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict) or not isinstance(data.get("items"), dict):
        raise ValueError("--projected must be tfvars JSON with an items object")
    return data["items"]


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=(
            "Classify absent/default placeholder candidates from static "
            "projected tfvars and saved plan JSON. This command does not run "
            "projection, normalize values, change drift policy, or run "
            "Terraform/OpenTofu."
        ))
    parser.add_argument("--resource-type", required=True)
    parser.add_argument("--projected")
    parser.add_argument("--plan")
    args = parser.parse_args(argv)

    try:
        if not args.projected and not args.plan:
            raise ValueError("provide --projected, --plan, or both")
        projected = _read_projected(args.projected) if args.projected else {}
        plan = _read_json(args.plan) if args.plan else {}
        report = build_report(args.resource_type, projected, plan)
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
