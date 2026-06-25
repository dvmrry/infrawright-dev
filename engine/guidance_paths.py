"""Shared path candidates for assert-adoptable guidance collectors.

This module only identifies plan paths that are safe for informational guidance
matching. It does not classify plans, change drift policy, alter projection,
omit values, or render guidance.
"""
from engine import schema_paths
from engine.plan_eval import diff_paths, truthy_paths


def guidance_candidate_paths(plan, resource_type):
    """Yield deterministic value-drift/unknown-after candidate records.

    Guidance lanes use these paths to decide whether already-blocked plan paths
    can receive informational annotations. Sensitivity-only paths are
    deliberately excluded; plan classification may still block on them, but they
    are not value-shape guidance evidence.
    """
    records = []
    for source in ("resource_changes", "resource_drift"):
        for rc in plan.get(source) or []:
            if rc.get("type") != resource_type:
                continue
            change = rc.get("change") or {}
            if "update" not in set(change.get("actions") or []):
                continue
            before = change.get("before")
            paths = (
                set(diff_paths(before, change.get("after")))
                | set(truthy_paths(change.get("after_unknown")))
            )
            for path in paths:
                records.append({
                    "source": source,
                    "address": rc.get("address"),
                    "resource_type": rc.get("type"),
                    "before": before,
                    "path": path,
                    "formatted_path": schema_paths.format_path(path),
                })
    for record in sorted(records, key=_record_sort_key):
        yield record


def _record_sort_key(record):
    return (
        record.get("source") or "",
        record.get("address") or "",
        record.get("formatted_path") or "",
        tuple(str(segment) for segment in record.get("path") or ()),
    )
