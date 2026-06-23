"""Adoption certification advisory report.

This report compares raw API detail shape, oracle-imported provider state, and
projected tfvars shape. It is advisory by design: raw-only paths can indicate
provider-invisible surface, API-only metadata, or fields intentionally outside
Terraform control.
"""

from engine import path_inventory
from engine.drift_policy import parse_path


def build_report(
        resource_type,
        raw_items_by_key,
        oracle_state_by_key,
        projected_items_by_key,
        drift_policy=None,
        required_missing=None,
        sensitive_blocked=None):
    required_missing = required_missing or {}
    sensitive_blocked = sensitive_blocked or {}
    policy_paths = _projection_omit_paths(resource_type, drift_policy)

    items = {}
    keys = sorted(
        set(raw_items_by_key or {})
        | set(oracle_state_by_key or {})
        | set(projected_items_by_key or {})
    )
    for key in keys:
        raw_paths = set(path_inventory.leaf_paths(
            (raw_items_by_key or {}).get(key, {})))
        provider_paths = set(path_inventory.leaf_paths(
            ((oracle_state_by_key or {}).get(key, {}) or {}).get("values") or {}))
        projected_paths = set(path_inventory.leaf_paths(
            (projected_items_by_key or {}).get(key, {})))

        omitted = (policy_paths & provider_paths) - projected_paths
        omitted_by_policy = sorted(omitted)

        raw_only = sorted(raw_paths - provider_paths - projected_paths)
        provider_only = sorted(
            provider_paths - raw_paths - projected_paths - omitted)

        items[key] = {
            "raw_only_paths": raw_only,
            "provider_only_paths": provider_only,
            "projected_paths": sorted(projected_paths),
            "omitted_by_policy": omitted_by_policy,
            "required_missing": sorted(_paths_for_key(required_missing, key)),
            "sensitive_blocked": sorted(_paths_for_key(sensitive_blocked, key)),
        }

    return {
        "resource_type": resource_type,
        "summary": _summary(items),
        "items": items,
    }


def _summary(items):
    counters = {
        "items": len(items),
        "raw_only_paths": 0,
        "provider_only_paths": 0,
        "projected_paths": 0,
        "omitted_by_policy": 0,
        "required_missing": 0,
        "sensitive_blocked": 0,
    }
    for item in items.values():
        for key in sorted(counters):
            if key == "items":
                continue
            counters[key] += len(item.get(key) or [])
    return counters


def _projection_omit_paths(resource_type, drift_policy):
    if drift_policy is None:
        return set()
    entries = []
    if hasattr(drift_policy, "_entries"):
        entries = drift_policy._entries(resource_type, "projection_omit")
    out = set()
    for entry in entries:
        out.add(_format_policy_path(parse_path(entry["path"])))
    return out


def _format_policy_path(path):
    parts = []
    for segment in path:
        if segment == "*":
            if parts:
                parts[-1] = parts[-1] + "[]"
            else:
                parts.append("[]")
        elif isinstance(segment, int):
            if parts:
                parts[-1] = "%s[]" % parts[-1]
            else:
                parts.append("[]")
        else:
            parts.append(str(segment))
    return ".".join(parts) if parts else "<root>"


def _paths_for_key(value, key):
    if not value:
        return []
    if isinstance(value, dict):
        return value.get(key) or []
    return value
