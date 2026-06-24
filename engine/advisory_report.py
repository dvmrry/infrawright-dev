"""Adoption advisory report.

This report compares raw API detail shape, oracle-imported provider state, and
projected tfvars shape. It is advisory by design: raw-only paths can indicate
provider-invisible surface, API-only metadata, or fields intentionally outside
Terraform control.

``required_missing`` is a caller-supplied side input. ``sensitive_blocked`` can
be supplied by callers and is also derived from Terraform's ``sensitive_values``
mirror in oracle state. This module does not run state projection or Terraform
validation itself.
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
        oracle_entry = ((oracle_state_by_key or {}).get(key, {}) or {})
        provider_values = oracle_entry.get("values") or {}
        projected_value = (projected_items_by_key or {}).get(key, {})

        raw_paths = set(path_inventory.leaf_paths(
            (raw_items_by_key or {}).get(key, {})))
        provider_paths = set(path_inventory.leaf_paths(provider_values))
        projected_paths = set(path_inventory.leaf_paths(projected_value))
        projected_present_paths = (
            projected_paths | _container_paths(projected_value)
        )
        derived_sensitive_blocked = (
            _sensitive_paths(oracle_entry.get("sensitive_values") or {})
            - projected_present_paths
        )

        omitted = (
            _paths_covered_by_policy(policy_paths, provider_paths)
            - projected_paths
        )
        omitted_by_policy = sorted(omitted)

        raw_only = sorted(raw_paths - provider_paths - projected_paths)
        provider_only = sorted(
            provider_paths - raw_paths - projected_paths - omitted)
        item_sensitive_blocked = (
            set(_paths_for_key(sensitive_blocked, key))
            | derived_sensitive_blocked
        )

        items[key] = {
            "raw_only_paths": raw_only,
            "provider_only_paths": provider_only,
            "projected_paths": sorted(projected_paths),
            "omitted_by_policy": omitted_by_policy,
            "required_missing": sorted(_paths_for_key(required_missing, key)),
            "sensitive_blocked": sorted(item_sensitive_blocked),
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


def _paths_covered_by_policy(policy_paths, observed_paths):
    covered = set()
    for observed_path in observed_paths:
        if any(
                _policy_path_covers_observed(policy_path, observed_path)
                for policy_path in policy_paths):
            covered.add(observed_path)
    return covered


def _policy_path_covers_observed(policy_path, observed_path):
    if policy_path == observed_path:
        return True
    return (
        observed_path.startswith(policy_path + ".")
        or observed_path.startswith(policy_path + "[]")
    )


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


def _sensitive_paths(value):
    out = set()
    _walk_sensitive(value, (), out)
    return out


def _walk_sensitive(value, path, out):
    if isinstance(value, dict):
        for key in sorted(value, key=lambda item: str(item)):
            _walk_sensitive(value[key], path + (str(key),), out)
        return
    if isinstance(value, list):
        for child in value:
            _walk_sensitive(
                child,
                path + (path_inventory.LIST_MARKER,),
                out,
            )
        return
    if value is True:
        out.add(path_inventory.format_path(path))


def _container_paths(value):
    out = set()
    _walk_containers(value, (), out)
    return out


def _walk_containers(value, path, out):
    if isinstance(value, dict):
        if path:
            out.add(path_inventory.format_path(path))
        for key in sorted(value, key=lambda item: str(item)):
            _walk_containers(value[key], path + (str(key),), out)
        return
    if isinstance(value, list):
        if path:
            out.add(path_inventory.format_path(path))
        for child in value:
            _walk_containers(
                child,
                path + (path_inventory.LIST_MARKER,),
                out,
            )


def _paths_for_key(value, key):
    if not value:
        return []
    if isinstance(value, dict):
        return value.get(key) or []
    return value
