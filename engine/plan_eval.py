"""Classify Terraform/OpenTofu plan JSON for adoption gates."""
from engine.drift_policy import DriftPolicy

CLEAN = "clean"
TOLERATED = "clean_with_tolerated_drift"
BLOCKED = "blocked"
OPAQUE_UPDATE = "<opaque_update>"
_OPAQUE_UPDATE_PATH = (OPAQUE_UPDATE,)


def classify_plan(plan, policy=None):
    policy = policy or DriftPolicy(None)
    findings = []
    for rc in plan.get("resource_changes") or []:
        findings.extend(_classify_change(rc, policy, source="resource_changes"))
    for rc in plan.get("resource_drift") or []:
        findings.extend(_classify_change(rc, policy, source="resource_drift"))
    blocked = [f for f in findings if f["status"] == BLOCKED]
    tolerated = [f for f in findings if f["status"] == TOLERATED]
    if blocked:
        return {"status": BLOCKED, "findings": findings}
    if tolerated:
        return {"status": TOLERATED, "findings": findings}
    return {"status": CLEAN, "findings": findings}


def _classify_change(rc, policy, source):
    change = rc.get("change") or {}
    actions = set(change.get("actions") or [])
    resource_type = rc.get("type")
    address = rc.get("address")
    if not actions or actions <= set(["no-op"]):
        return []
    if (change.get("importing") or rc.get("importing")) and actions <= set(["create"]):
        return [{
            "status": CLEAN,
            "source": source,
            "address": address,
            "actions": sorted(actions),
            "paths": [],
        }]
    if actions & set(["delete"]):
        return [_blocked(source, address, actions, [("<delete>",)])]
    if actions & set(["create"]):
        return [_blocked(source, address, actions, [("<create>",)])]
    if actions & set(["update"]):
        paths = _update_paths(change)
        unmatched = [
            p for p in paths
            if not policy.tolerates_plan_path(resource_type, p, "update")
        ]
        if unmatched:
            return [_blocked(source, address, actions, unmatched)]
        return [{
            "status": TOLERATED,
            "source": source,
            "address": address,
            "actions": sorted(actions),
            "paths": paths,
        }]
    return [_blocked(source, address, actions, [("<unsupported_action>",)])]


def _update_paths(change):
    paths = set()
    opaque = False
    for path in (
            list(diff_paths(change.get("before"), change.get("after")))
            + list(truthy_paths(change.get("after_unknown")))):
        if path:
            paths.add(path)
        else:
            opaque = True
    if opaque or not paths:
        paths.add(_OPAQUE_UPDATE_PATH)
    return sorted(paths, key=_path_sort_key)


def _blocked(source, address, actions, paths):
    return {
        "status": BLOCKED,
        "source": source,
        "address": address,
        "actions": sorted(actions),
        "paths": paths,
    }


def _path_sort_key(path):
    return tuple(str(segment) for segment in path)


def diff_paths(before, after, path=()):
    if before == after:
        return []
    if isinstance(before, dict) and isinstance(after, dict):
        out = []
        for key in sorted(set(before) | set(after)):
            out.extend(diff_paths(before.get(key), after.get(key), path + (key,)))
        return out
    if isinstance(before, list) and isinstance(after, list):
        out = []
        max_len = max(len(before), len(after))
        for i in range(max_len):
            b = before[i] if i < len(before) else None
            a = after[i] if i < len(after) else None
            out.extend(diff_paths(b, a, path + (i,)))
        return out
    return [path]


def truthy_paths(value, path=()):
    if value is True:
        return [path]
    if isinstance(value, dict):
        out = []
        for key in sorted(value):
            out.extend(truthy_paths(value[key], path + (key,)))
        return out
    if isinstance(value, list):
        out = []
        for idx, child in enumerate(value):
            out.extend(truthy_paths(child, path + (idx,)))
        return out
    return []
