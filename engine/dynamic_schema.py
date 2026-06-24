"""Diagnostics for dynamic or open Terraform provider schema paths.

This module is intentionally advisory. It classifies paths observed during
provider labs, especially paths that had to be manually pruned from temporary
schema fixtures, without changing projection, rendering, or plan behavior.
"""
import argparse
import json
import sys

from engine import path_inventory
from engine.drift_policy import DriftPolicy, parse_path
from engine.tfschema import (
    attr_type,
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


def build_report(resource_type, paths=None, drift_policy=None):
    """Classify candidate dynamic-schema paths for one resource type."""
    all_paths = set(paths or [])
    all_paths.update(_projection_omit_paths(resource_type, drift_policy))
    results = {}
    for path in sorted(all_paths):
        results[path] = classify_path(resource_type, path)
    return {
        "resource_type": resource_type,
        "summary": _summary(results),
        "paths": results,
    }


def classify_path(resource_type, path):
    parsed = parse_path(path)
    block = load_resource(resource_type)["block"]
    out = _classify_block(block, parsed, (), resource_top=True)
    out["path"] = path
    return out


def _classify_block(block, parts, schema_path, resource_top):
    if not parts:
        return _result(
            "schema_known", "block", schema_path,
            "path resolves to a Terraform schema block"
        )
    segment = parts[0]
    if not isinstance(segment, str) or segment == "*":
        return _unknown(schema_path, segment)

    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    inputs = set(cls["required"]) | set(cls["optional"])
    block_types = input_block_types(block)
    attrs = block.get("attributes") or {}
    if segment in inputs:
        enc = attr_type(attrs[segment])
        return _classify_encoding(enc, parts[1:], schema_path + (segment,))
    if segment in block_types:
        bt = block_types[segment]
        remaining = _strip_collection_selector(parts[1:])
        child_schema_path = schema_path + (segment,)
        if not block_is_single(bt):
            child_schema_path = child_schema_path + (path_inventory.LIST_MARKER,)
        return _classify_block(
            bt["block"], remaining, child_schema_path, resource_top=False
        )
    if segment in attrs or segment in (block.get("block_types") or {}):
        return _result(
            "schema_computed_only", "computed_only", schema_path + (segment,),
            "path starts at provider-computed schema member %s"
            % _fmt(schema_path + (segment,))
        )
    return _unknown(schema_path, segment)


def _classify_encoding(enc, parts, schema_path):
    if not parts:
        return _result(
            "schema_known", "attribute", schema_path,
            "path resolves to Terraform schema attribute %s" % _fmt(schema_path)
        )
    if enc == "dynamic":
        return _gap(
            "dynamic_value", schema_path,
            "path descends into dynamic Terraform attribute %s" % _fmt(schema_path)
        )
    if isinstance(enc, str):
        return _result(
            "unknown_schema_path", "primitive_child", schema_path,
            "path descends below primitive Terraform attribute %s" % _fmt(schema_path)
        )
    if not (isinstance(enc, list) and len(enc) == 2):
        return _result(
            "unknown_schema_path", "unsupported_encoding", schema_path,
            "path reaches unsupported Terraform type encoding at %s" % _fmt(schema_path)
        )

    kind, inner = enc
    if kind in ("list", "set"):
        remaining = _strip_collection_selector(parts)
        if not remaining:
            return _result(
                "schema_known", kind, schema_path,
                "path resolves to Terraform %s attribute %s" % (kind, _fmt(schema_path))
            )
        return _classify_encoding(
            inner, remaining, schema_path + (path_inventory.LIST_MARKER,)
        )
    if kind == "map":
        return _gap(
            "map_key", schema_path,
            "path descends into Terraform map attribute %s; map keys are not "
            "enumerated by provider schema" % _fmt(schema_path)
        )
    if kind == "object" and isinstance(inner, dict):
        if not inner:
            return _gap(
                "open_object_member", schema_path,
                "path descends into object attribute %s with no declared "
                "members" % _fmt(schema_path)
            )
        child = parts[0]
        if not isinstance(child, str) or child == "*":
            return _unknown(schema_path, child)
        if child not in inner:
            return _gap(
                "unknown_object_member", schema_path,
                "path uses object member %s below %s, but the provider schema "
                "does not declare that member" % (child, _fmt(schema_path))
            )
        return _classify_encoding(inner[child], parts[1:], schema_path + (child,))
    return _result(
        "unknown_schema_path", "unsupported_encoding", schema_path,
        "path reaches unsupported Terraform type encoding at %s" % _fmt(schema_path)
    )


def _strip_collection_selector(parts):
    if parts and (parts[0] == "*" or isinstance(parts[0], int)):
        return parts[1:]
    return parts


def _gap(classification, schema_path, reason):
    return _result("pack_schema_gap", classification, schema_path, reason)


def _unknown(schema_path, segment):
    return _result(
        "unknown_schema_path", "unknown_segment", schema_path,
        "schema has no member %r below %s" % (segment, _fmt(schema_path))
    )


def _result(status, classification, schema_path, reason):
    return {
        "status": status,
        "classification": classification,
        "schema_path": _fmt(schema_path),
        "reason": reason,
    }


def _summary(results):
    counters = {
        "paths": len(results),
        "schema_known": 0,
        "pack_schema_gap": 0,
        "schema_computed_only": 0,
        "unknown_schema_path": 0,
    }
    for item in results.values():
        status = item.get("status")
        if status not in counters:
            counters[status] = 0
        counters[status] += 1
    return counters


def _projection_omit_paths(resource_type, drift_policy):
    if drift_policy is None:
        return []
    if not hasattr(drift_policy, "_entries"):
        return []
    return [
        entry["path"]
        for entry in drift_policy._entries(resource_type, "projection_omit")
    ]


def _fmt(path):
    return path_inventory.format_path(path)


def _read_paths_json(path, resource_type):
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if isinstance(data, list):
        return [str(item) for item in data]
    if isinstance(data, dict):
        if resource_type in data:
            value = data[resource_type]
        else:
            value = data.get("paths")
        if isinstance(value, list):
            return [str(item) for item in value]
    raise ValueError(
        "--paths-json must be a list of paths or an object with paths/resource key"
    )


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=(
            "Classify provider-lab dynamic schema paths. This command is "
            "diagnostic only; it does not project state, render HCL, or run "
            "Terraform/OpenTofu."
        ))
    parser.add_argument("--resource-type", required=True)
    parser.add_argument(
        "--path", action="append", default=[],
        help="Candidate path to classify; may be repeated."
    )
    parser.add_argument("--paths-json")
    parser.add_argument(
        "--policy",
        help="Optional drift policy; projection_omit paths are classified too."
    )
    args = parser.parse_args(argv)

    try:
        paths = list(args.path or [])
        if args.paths_json:
            paths.extend(_read_paths_json(args.paths_json, args.resource_type))
        policy = DriftPolicy.load(args.policy)
        if not paths and not policy._entries(args.resource_type, "projection_omit"):
            raise ValueError("provide at least one --path, --paths-json, or --policy")
        report = build_report(args.resource_type, paths, drift_policy=policy)
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
