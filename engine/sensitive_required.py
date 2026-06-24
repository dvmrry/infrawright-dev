"""Diagnostics for sensitive provider state that may be structurally required.

This module is advisory only. It classifies oracle-state ``sensitive_values``
against projected tfvars, Terraform schema requiredness, and optional
caller-supplied validation-required paths. It does not write secrets, generate
placeholders, alter projection, or run Terraform/OpenTofu.
"""
import argparse
import json
import sys

from engine import path_inventory
from engine import schema_paths
from engine.tfschema import load_resource


def build_report(
        resource_type,
        oracle_state_by_key,
        projected_items_by_key=None,
        required_paths=None):
    projected_items_by_key = projected_items_by_key or {}
    required_paths_by_key = _normalize_required_paths(required_paths)
    items = {}
    for key in sorted(oracle_state_by_key or {}):
        oracle_entry = (oracle_state_by_key or {}).get(key) or {}
        provider_values = oracle_entry.get("values") or {}
        projected = projected_items_by_key.get(key) or {}
        required_for_key = (
            required_paths_by_key.get(key, set())
            | required_paths_by_key.get("*", set())
        )
        items[key] = _classify_item(
            resource_type,
            provider_values,
            oracle_entry.get("sensitive_values") or {},
            projected,
            required_for_key,
        )
    return {
        "resource_type": resource_type,
        "summary": _summary(items),
        "items": items,
    }


def _classify_item(resource_type, provider_values, sensitive_values, projected,
                   required_paths):
    projected_present = (
        set(path_inventory.leaf_paths(projected))
        | schema_paths.container_paths(projected)
    )
    block = load_resource(resource_type)["block"]
    out = []
    for marker in _sensitive_markers(sensitive_values, provider_values):
        path = marker["path"]
        schema = schema_paths.schema_status_for_block(
            block,
            schema_paths.parse_report_path(path),
            block_mode="requiredness",
        )
        required_evidence = []
        if schema == "required":
            required_evidence.append("schema")
        if path in required_paths:
            required_evidence.append("validation")
        projected_status = "present" if path in projected_present else "absent"
        if projected_status == "present":
            status = "sensitive_present"
        elif "validation" in required_evidence:
            status = "sensitive_required_validation"
        elif "schema" in required_evidence:
            status = "sensitive_required_schema"
        else:
            status = "sensitive_structural_candidate"
        out.append({
            "path": path,
            "status": status,
            "schema": schema,
            "marker": marker["marker"],
            "projected": projected_status,
            "required_evidence": required_evidence,
            "reason": _reason(status, path),
        })
    return out


def _sensitive_markers(sensitive_values, provider_values):
    out = []
    _walk_sensitive(sensitive_values, provider_values, (), out)
    return sorted(out, key=lambda item: item["path"])


def _walk_sensitive(sensitive_value, provider_value, path, out):
    if sensitive_value is True:
        out.append({
            "path": schema_paths.format_path(path),
            "marker": (
                "container"
                if isinstance(provider_value, (dict, list)) else "leaf"
            ),
        })
        return
    if isinstance(sensitive_value, dict):
        provider_dict = provider_value if isinstance(provider_value, dict) else {}
        for key in sorted(sensitive_value, key=lambda item: str(item)):
            _walk_sensitive(
                sensitive_value[key],
                provider_dict.get(key),
                path + (str(key),),
                out,
            )
        return
    if isinstance(sensitive_value, list):
        provider_list = provider_value if isinstance(provider_value, list) else []
        for idx, child in enumerate(sensitive_value):
            provider_child = provider_list[idx] if idx < len(provider_list) else None
            _walk_sensitive(
                child,
                provider_child,
                path + (schema_paths.LIST_MARKER,),
                out,
            )


def _normalize_required_paths(value):
    out = {}
    if not value:
        return out
    if isinstance(value, dict):
        for key, paths in sorted(value.items()):
            out[str(key)] = set(_format_required_path(path) for path in paths)
    else:
        out["*"] = set(_format_required_path(path) for path in value)
    return out


def _format_required_path(path):
    if isinstance(path, str):
        return schema_paths.format_path(schema_paths.parse_report_path(path))
    return schema_paths.format_path(path)


def _summary(items):
    counters = {
        "items": len(items),
        "sensitive_markers": 0,
        "sensitive_required_schema": 0,
        "sensitive_required_validation": 0,
        "sensitive_structural_candidate": 0,
        "sensitive_present": 0,
    }
    for markers in items.values():
        counters["sensitive_markers"] += len(markers)
        for marker in markers:
            status = marker["status"]
            if status not in counters:
                counters[status] = 0
            counters[status] += 1
    return counters


def _reason(status, path):
    if status == "sensitive_required_validation":
        return (
            "%s is sensitive, absent from projected config, and caller-supplied "
            "validation evidence says it is structurally required" % path
        )
    if status == "sensitive_required_schema":
        return (
            "%s is sensitive, absent from projected config, and required by "
            "Terraform schema" % path
        )
    if status == "sensitive_present":
        return "%s is sensitive but present in projected config" % path
    return (
        "%s is sensitive and absent from projected config; review whether it "
        "is structurally required before adoption" % path
    )


def _read_oracle_state(path):
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict):
        raise ValueError("--oracle-state must be a JSON object keyed by item")
    for key, value in data.items():
        if not isinstance(value, dict):
            raise ValueError("oracle_state[%r] must be an object" % key)
        if not isinstance(value.get("values"), dict):
            raise ValueError("oracle_state[%r].values must be an object" % key)
    return data


def _read_projected(path):
    if not path:
        return {}
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict) or not isinstance(data.get("items"), dict):
        raise ValueError("--projected must be tfvars JSON with an items object")
    return data["items"]


def _read_required_json(path):
    if not path:
        return {}
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        return data
    raise ValueError("--required-json must be a path list or map of key to paths")


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=(
            "Classify sensitive provider-observed paths that may be "
            "structurally required. This command is static-only; it does not "
            "write secrets, generate placeholders, run projection, or run "
            "Terraform/OpenTofu."
        ))
    parser.add_argument("--resource-type", required=True)
    parser.add_argument("--oracle-state", required=True)
    parser.add_argument("--projected")
    parser.add_argument(
        "--required-path", action="append", default=[],
        help="Validation-required path to apply to every item; may repeat."
    )
    parser.add_argument(
        "--required-json",
        help="JSON list of required paths or object keyed by item key."
    )
    args = parser.parse_args(argv)

    try:
        required_paths = _read_required_json(args.required_json)
        if args.required_path:
            if isinstance(required_paths, dict):
                required_paths = dict(required_paths)
                required_paths.setdefault("*", [])
                required_paths["*"] = required_paths["*"] + args.required_path
            else:
                required_paths = list(required_paths or []) + args.required_path
        report = build_report(
            args.resource_type,
            _read_oracle_state(args.oracle_state),
            _read_projected(args.projected),
            required_paths=required_paths,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
