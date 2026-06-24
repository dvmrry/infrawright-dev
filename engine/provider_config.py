"""Diagnostics for provider configuration requirements.

Provider labs can expose drift caused by provider-level defaults rather than
resource projection. This module classifies saved-plan diffs against explicit
pack metadata. It does not render provider configuration, change drift policy,
alter projection, or run Terraform/OpenTofu.
"""
import argparse
import json
import re
import sys

from engine import packs
from engine import path_inventory
from engine.plan_eval import diff_paths, truthy_paths


def build_report(provider=None, resource_type=None, plan=None, requirements=None):
    """Classify saved-plan changes against provider-config metadata."""
    if provider is None and resource_type:
        provider = packs.provider_of(resource_type)
    reqs = _normalize_requirements(
        requirements
        if requirements is not None else packs.provider_config_requirements(provider)
    )
    changes = _plan_changes(plan or {}, reqs, provider, resource_type)
    return {
        "provider": provider,
        "resource_type": resource_type,
        "summary": _summary(reqs, changes),
        "requirements": [_public_requirement(req) for req in reqs],
        "plan_changes": changes,
    }


def _plan_changes(plan, requirements, provider, resource_type):
    out = []
    for source in ("resource_changes", "resource_drift"):
        for rc in plan.get(source) or []:
            rc_type = rc.get("type")
            if resource_type and rc_type != resource_type:
                continue
            rc_provider = _resource_provider(rc_type)
            if provider and rc_provider != provider:
                continue
            change = rc.get("change") or {}
            if "update" not in set(change.get("actions") or []):
                continue
            paths = sorted(
                set(diff_paths(change.get("before"), change.get("after")))
                | set(truthy_paths(change.get("after_unknown")))
                | set(truthy_paths(change.get("before_sensitive")))
                | set(truthy_paths(change.get("after_sensitive")))
            )
            for path in paths:
                formatted = _fmt(path)
                matched = [
                    req for req in requirements
                    if _matches_requirement(req, rc_provider, rc_type, formatted)
                ]
                if matched:
                    for req in matched:
                        out.append(_matched_change(source, rc, formatted, req))
                else:
                    out.append(_unmatched_change(source, rc, formatted))
    return out


def _matched_change(source, rc, path, req):
    return {
        "status": "provider_config_requirement",
        "source": source,
        "address": rc.get("address"),
        "resource_type": rc.get("type"),
        "path": path,
        "requirement": req["id"],
        "setting": req["setting"],
        "value": req["value"],
        "reason": req["reason"],
    }


def _unmatched_change(source, rc, path):
    return {
        "status": "unmatched_plan_change",
        "source": source,
        "address": rc.get("address"),
        "resource_type": rc.get("type"),
        "path": path,
        "reason": (
            "plan path is not covered by pack provider_config metadata"
        ),
    }


def _matches_requirement(req, provider, resource_type, path):
    if req["provider"] != provider:
        return False
    if path not in req["plan_paths"]:
        return False
    resource_types = req.get("resource_types")
    if resource_types and resource_type not in resource_types:
        return False
    prefixes = req.get("resource_prefixes")
    if prefixes and (
            not resource_type
            or not any(resource_type.startswith(p) for p in prefixes)):
        return False
    return True


def _normalize_requirements(requirements):
    out = []
    for idx, req in enumerate(requirements or []):
        if not isinstance(req, dict):
            raise ValueError("provider_config requirement %d must be an object" % idx)
        item = dict(req)
        ident = str(item.get("id") or "").strip()
        provider = str(item.get("provider") or "").strip()
        setting = str(item.get("setting") or "").strip()
        reason = str(item.get("reason") or "").strip()
        if not ident:
            raise ValueError("provider_config requirement %d missing id" % idx)
        if not provider:
            raise ValueError("%s missing provider" % ident)
        if not setting:
            raise ValueError("%s missing setting" % ident)
        if "value" not in item:
            raise ValueError("%s missing value" % ident)
        if not reason:
            raise ValueError("%s missing reason" % ident)
        plan_paths = _normalize_paths(item.get("plan_paths"), ident)
        resource_types = _string_list(item.get("resource_types"), "resource_types")
        resource_prefixes = _string_list(
            item.get("resource_prefixes"), "resource_prefixes"
        )
        out.append({
            "id": ident,
            "provider": provider,
            "setting": setting,
            "value": item["value"],
            "reason": reason,
            "plan_paths": set(plan_paths),
            "resource_types": set(resource_types),
            "resource_prefixes": tuple(resource_prefixes),
        })
    return sorted(out, key=lambda req: (req["provider"], req["id"]))


def _normalize_paths(paths, ident):
    if not isinstance(paths, list) or not paths:
        raise ValueError("%s plan_paths must be a non-empty list" % ident)
    return sorted(set(_fmt(_parse_report_path(path)) for path in paths))


def _parse_report_path(path):
    out = []
    for raw in str(path).split("."):
        if raw == "":
            raise ValueError("empty path segment in %r" % path)
        match = re.match(r"^(.*)\[(\*|\d*)\]$", raw)
        if match:
            name, index = match.groups()
            if not name:
                raise ValueError("empty collection path segment in %r" % path)
            out.append(name)
            out.append(
                path_inventory.LIST_MARKER
                if index in ("", "*") else int(index)
            )
        else:
            out.append(raw)
    return tuple(out)


def _string_list(value, name):
    if value is None:
        return []
    if not isinstance(value, list):
        raise ValueError("provider_config %s must be a list" % name)
    out = []
    for item in value:
        text = str(item).strip()
        if not text:
            raise ValueError("provider_config %s contains an empty value" % name)
        out.append(text)
    return sorted(set(out))


def _public_requirement(req):
    return {
        "id": req["id"],
        "provider": req["provider"],
        "setting": req["setting"],
        "value": req["value"],
        "reason": req["reason"],
        "plan_paths": sorted(req["plan_paths"]),
        "resource_types": sorted(req["resource_types"]),
        "resource_prefixes": list(req["resource_prefixes"]),
    }


def _summary(requirements, changes):
    matched = [
        item for item in changes
        if item["status"] == "provider_config_requirement"
    ]
    unmatched = [
        item for item in changes
        if item["status"] == "unmatched_plan_change"
    ]
    matched_ids = set(item["requirement"] for item in matched)
    return {
        "requirements": len(requirements),
        "plan_changes": len(changes),
        "provider_config_matches": len(matched),
        "unmatched_plan_changes": len(unmatched),
        "matched_requirements": len(matched_ids),
        "unmatched_requirements": len(requirements) - len(matched_ids),
    }


def _resource_provider(resource_type):
    if not resource_type:
        return None
    try:
        return packs.provider_of(resource_type)
    except Exception:
        return resource_type.split("_", 1)[0]


def _fmt(path):
    normalized = tuple(
        path_inventory.LIST_MARKER if isinstance(segment, int) else segment
        for segment in path
    )
    return path_inventory.format_path(normalized)


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=(
            "Classify saved-plan diffs caused by provider-level configuration "
            "requirements. This command is diagnostic only; it does not render "
            "provider config, alter projection, change drift policy, or run "
            "Terraform/OpenTofu."
        ))
    parser.add_argument("--provider")
    parser.add_argument("--resource-type")
    parser.add_argument("--plan", required=True)
    args = parser.parse_args(argv)

    try:
        if not args.provider and not args.resource_type:
            raise ValueError("provide --provider or --resource-type")
        report = build_report(
            provider=args.provider,
            resource_type=args.resource_type,
            plan=_read_json(args.plan),
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
