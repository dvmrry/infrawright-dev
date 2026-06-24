"""Diagnostics for provider configuration requirements.

Provider labs can expose drift caused by provider-level defaults rather than
resource projection. This module classifies saved-plan diffs against explicit
pack metadata. It does not render provider configuration, change drift policy,
alter projection, or run Terraform/OpenTofu.
"""
import argparse
import json
import math
import sys

from engine import packs
from engine import schema_paths
from engine.plan_eval import diff_paths, truthy_paths


REMEDIATION_KINDS = set(["provider_argument"])
REMEDIATION_MODES = set([
    "diagnostic_only",
    "required_external",
    "renderable_default",
])
REMEDIATION_KEYS = set(["kind", "mode", "evidence", "safety"])
RENDERABLE_SAFETY_KEYS = [
    "non_sensitive",
    "not_tenant_specific",
    "not_destructive",
]
_NO_VALUE = object()


def validate_requirements(requirements):
    """Validate provider-config requirement metadata.

    This is intentionally validator-only. It returns the normalized
    diagnostics requirements used by this module, but does not render provider
    configuration or change any adoption behavior.
    """
    return _normalize_requirements(requirements)


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
            )
            for path in paths:
                formatted = schema_paths.format_path(path)
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
    seen_settings = {}
    for idx, req in enumerate(requirements or []):
        if not isinstance(req, dict):
            raise ValueError("provider_config requirement %d must be an object" % idx)
        item = dict(req)
        ident = str(item.get("id") or "").strip()
        label = _req_label(idx, ident)
        provider = str(item.get("provider") or "").strip()
        setting = str(item.get("setting") or "").strip()
        reason = str(item.get("reason") or "").strip()
        if not ident:
            raise ValueError("provider_config requirement %d missing id" % idx)
        if not provider:
            raise ValueError("%s: %s missing provider" % (label, ident))
        if not setting:
            raise ValueError("%s: %s missing setting" % (label, ident))
        if not reason:
            raise ValueError("%s: %s missing reason" % (label, ident))
        plan_paths = _normalize_paths(item.get("plan_paths"), ident, label)
        resource_types = _string_list(item.get("resource_types"), "resource_types")
        resource_prefixes = _string_list(
            item.get("resource_prefixes"), "resource_prefixes"
        )
        remediation_mode = _validate_remediation(item, label)
        if "value" not in item and remediation_mode != "required_external":
            raise ValueError("%s: %s missing value" % (label, ident))
        value = item.get("value")
        key = (provider, setting)
        if key in seen_settings:
            _raise_duplicate_setting(
                label,
                seen_settings[key],
                provider,
                setting,
                item.get("value", _NO_VALUE),
                remediation_mode,
            )
        seen_settings[key] = {
            "label": label,
            "value": item.get("value", _NO_VALUE),
            "mode": remediation_mode,
        }
        out.append({
            "id": ident,
            "provider": provider,
            "setting": setting,
            "value": value,
            "reason": reason,
            "plan_paths": set(plan_paths),
            "resource_types": set(resource_types),
            "resource_prefixes": tuple(resource_prefixes),
        })
    return sorted(out, key=lambda req: (req["provider"], req["id"]))


def _normalize_paths(paths, ident, label):
    if not isinstance(paths, list) or not paths:
        raise ValueError(
            "%s: %s plan_paths must be a non-empty list" % (label, ident)
        )
    return sorted(set(
        schema_paths.format_path(schema_paths.parse_report_path(path))
        for path in paths
    ))


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


def _validate_remediation(item, label):
    remediation = item.get("remediation")
    if remediation is None:
        return "diagnostic_only"
    if not isinstance(remediation, dict):
        raise ValueError("%s: remediation must be an object" % label)
    unknown = sorted(set(remediation) - REMEDIATION_KEYS)
    if unknown:
        raise ValueError(
            "%s: unknown remediation key %s" % (label, unknown[0])
        )
    kind = str(remediation.get("kind") or "").strip()
    if not kind:
        raise ValueError("%s: remediation.kind is required" % label)
    if kind not in REMEDIATION_KINDS:
        raise ValueError(
            "%s: unknown remediation kind %s" % (label, kind)
        )
    mode = str(remediation.get("mode") or "").strip()
    if not mode:
        raise ValueError("%s: remediation.mode is required" % label)
    if mode not in REMEDIATION_MODES:
        raise ValueError(
            "%s: unknown remediation mode %s" % (label, mode)
        )
    if mode == "renderable_default":
        _validate_renderable_default(item, remediation, label)
    return mode


def _validate_renderable_default(item, remediation, label):
    if "value" not in item:
        raise ValueError("%s: renderable_default missing value" % label)
    evidence = str(remediation.get("evidence") or "").strip()
    if not evidence:
        raise ValueError(
            "%s: remediation.evidence is required for renderable_default"
            % label
        )
    safety = remediation.get("safety")
    if not isinstance(safety, dict):
        raise ValueError(
            "%s: remediation.safety must be an object for renderable_default"
            % label
        )
    for key in RENDERABLE_SAFETY_KEYS:
        if key not in safety:
            raise ValueError(
                "%s: remediation.safety.%s is required" % (label, key)
            )
        value = safety[key]
        if not isinstance(value, bool):
            raise ValueError(
                "%s: remediation.safety.%s must be boolean true" % (label, key)
            )
        if value is not True:
            raise ValueError(
                "%s: remediation.safety.%s must be true" % (label, key)
            )
    if not _is_json_bool_or_number(item.get("value")):
        raise ValueError(
            "%s: renderable_default value must be a JSON boolean or number"
            % label
        )
    if "resource_types" in item:
        raise ValueError(
            "%s: renderable_default must not include resource_types" % label
        )
    if "resource_prefixes" in item:
        raise ValueError(
            "%s: renderable_default must not include resource_prefixes" % label
        )


def _is_json_bool_or_number(value):
    if isinstance(value, bool):
        return True
    if isinstance(value, int) and not isinstance(value, bool):
        return True
    if isinstance(value, float):
        return math.isfinite(value)
    return False


def _raise_duplicate_setting(label, first, provider, setting, value, mode):
    if first["value"] != value:
        reason = "conflicting values"
    elif first["mode"] != mode:
        reason = "conflicting remediation modes"
    else:
        reason = "duplicate metadata"
    raise ValueError(
        "%s: duplicate provider_config requirement for %s.%s "
        "(previous %s; %s)" % (
            label,
            provider,
            setting,
            first["label"],
            reason,
        )
    )


def _req_label(idx, ident=None):
    if ident:
        return "provider_config requirement %d (%s)" % (idx, ident)
    return "provider_config requirement %d" % idx


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
