"""Shared assert-adoptable guidance annotation helpers.

This module only normalizes and renders additive guidance annotations for
blocked assert-adoptable output. It does not classify plans, change drift
policy, alter projection, mutate provider configuration, or execute Terraform.
"""
import json


LANE_PROVIDER_CONFIG = "provider_config"
LANE_ABSENT_DEFAULT = "absent_default"
LANE_DYNAMIC_SCHEMA = "dynamic_schema"
STATUS_EFFECT_BLOCKED = "informational only; plan remains blocked"

_LANE_ORDER = {
    LANE_PROVIDER_CONFIG: 0,
    LANE_ABSENT_DEFAULT: 1,
    LANE_DYNAMIC_SCHEMA: 2,
}


def safe_collect_guidance(collector, *args, **kwargs):
    """Run a guidance collector and fail closed to no annotations."""
    try:
        return list(collector(*args, **kwargs) or [])
    except Exception:
        return []


def provider_config_annotation(source, address, matched_plan_path, provider,
                               resource_type, setting, expected_value, mode,
                               reason, evidence):
    """Return a normalized provider-config guidance annotation."""
    return {
        "lane": LANE_PROVIDER_CONFIG,
        "provider": provider,
        "resource_type": resource_type,
        "address": address,
        "source": source,
        "matched_plan_path": matched_plan_path,
        "status_effect": STATUS_EFFECT_BLOCKED,
        "setting": setting,
        "expected_value": expected_value,
        "mode": mode,
        "reason": reason,
        "evidence": evidence,
        "sort_key": (
            _LANE_ORDER[LANE_PROVIDER_CONFIG],
            provider or "",
            setting or "",
            matched_plan_path or "",
        ),
    }


def absent_default_annotation(source, address, matched_plan_path, provider,
                              resource_type, rule, kind, action,
                              observed_value, reason, evidence):
    """Return a normalized absent/default guidance annotation."""
    return {
        "lane": LANE_ABSENT_DEFAULT,
        "provider": provider,
        "resource_type": resource_type,
        "address": address,
        "source": source,
        "matched_plan_path": matched_plan_path,
        "status_effect": STATUS_EFFECT_BLOCKED,
        "rule": rule,
        "kind": kind,
        "action": action,
        "observed_value": observed_value,
        "reason": reason,
        "evidence": evidence,
        "sort_key": (
            _LANE_ORDER[LANE_ABSENT_DEFAULT],
            provider or "",
            resource_type or "",
            matched_plan_path or "",
            rule or "",
        ),
    }


def dynamic_schema_annotation(source, address, matched_plan_path, provider,
                              resource_type, rule, kind, ownership, action,
                              provider_version_constraint, reason, evidence):
    """Return a normalized dynamic-schema guidance annotation."""
    return {
        "lane": LANE_DYNAMIC_SCHEMA,
        "provider": provider,
        "resource_type": resource_type,
        "address": address,
        "source": source,
        "matched_plan_path": matched_plan_path,
        "status_effect": STATUS_EFFECT_BLOCKED,
        "rule": rule,
        "kind": kind,
        "ownership": ownership,
        "action": action,
        "provider_version_constraint": provider_version_constraint,
        "reason": reason,
        "evidence": evidence,
        "sort_key": (
            _LANE_ORDER[LANE_DYNAMIC_SCHEMA],
            provider or "",
            resource_type or "",
            matched_plan_path or "",
            rule or "",
        ),
    }


def annotations_for_finding_path(annotations, finding, path):
    """Return matched annotations joined to the concrete finding path."""
    from engine import paths
    from engine import schema_paths

    key = (
        finding.get("source"),
        finding.get("address"),
        schema_paths.format_path(path),
    )
    matched = []
    for annotation in annotations or []:
        if _annotation_key(annotation) != key:
            continue
        joined = dict(annotation)
        joined["finding_path"] = paths.format_path(path)
        matched.append(joined)
    return sort_annotations(matched)


def sort_annotations(annotations):
    return sorted(annotations or [], key=_sort_key)


def print_guidance_sections(annotations, write):
    """Render guidance sections in the existing assert-adoptable format."""
    annotations = sort_annotations(annotations)
    provider_config = [
        a for a in annotations
        if a.get("lane") == LANE_PROVIDER_CONFIG
    ]
    absent_default = [
        a for a in annotations
        if a.get("lane") == LANE_ABSENT_DEFAULT
    ]
    dynamic_schema = [
        a for a in annotations
        if a.get("lane") == LANE_DYNAMIC_SCHEMA
    ]
    if provider_config:
        _print_provider_config(provider_config, write)
    if absent_default:
        _print_absent_default(absent_default, write)
    if dynamic_schema:
        _print_dynamic_schema(dynamic_schema, write)


def _print_provider_config(annotations, write):
    write("  Provider configuration guidance:\n")
    for item in annotations:
        write("    - provider: %s\n" % item.get("provider"))
        write("      setting: %s\n" % item.get("setting"))
        if item.get("expected_value") is not None:
            write(
                "      expected value: %s\n"
                % json.dumps(item.get("expected_value"), sort_keys=True)
            )
        write("      mode: %s\n" % item.get("mode"))
        write("      matched plan path: %s\n" % item.get("matched_plan_path"))
        write("      reason: %s\n" % item.get("reason"))
        if item.get("evidence"):
            write("      evidence: %s\n" % item.get("evidence"))
        write("      status: %s\n" % item.get("status_effect"))


def _print_absent_default(annotations, write):
    write("  Absent/default guidance:\n")
    for item in annotations:
        write("    - rule: %s\n" % item.get("rule"))
        write("      provider: %s\n" % item.get("provider"))
        write("      resource type: %s\n" % item.get("resource_type"))
        write("      kind: %s\n" % item.get("kind"))
        write("      action: %s\n" % item.get("action"))
        if "observed_value" in item:
            write(
                "      observed value: %s\n"
                % json.dumps(item.get("observed_value"), sort_keys=True)
            )
        write("      matched plan path: %s\n" % item.get("matched_plan_path"))
        write("      reason: %s\n" % item.get("reason"))
        if item.get("evidence"):
            write("      evidence: %s\n" % item.get("evidence"))
        write("      status: %s\n" % item.get("status_effect"))


def _print_dynamic_schema(annotations, write):
    write("  Dynamic-schema guidance:\n")
    for item in annotations:
        write("    - rule: %s\n" % item.get("rule"))
        write("      provider: %s\n" % item.get("provider"))
        write("      resource type: %s\n" % item.get("resource_type"))
        write("      kind: %s\n" % item.get("kind"))
        write("      ownership: %s\n" % item.get("ownership"))
        write("      action: %s\n" % item.get("action"))
        if item.get("provider_version_constraint"):
            write(
                "      provider version constraint: %s\n"
                % item.get("provider_version_constraint")
            )
        write("      matched plan path: %s\n" % item.get("matched_plan_path"))
        write("      reason: %s\n" % item.get("reason"))
        if item.get("evidence"):
            write("      evidence: %s\n" % item.get("evidence"))
        write("      status: %s\n" % item.get("status_effect"))


def _annotation_key(annotation):
    return (
        annotation.get("source"),
        annotation.get("address"),
        annotation.get("matched_plan_path"),
    )


def _sort_key(annotation):
    return annotation.get("sort_key") or (
        _LANE_ORDER.get(annotation.get("lane"), 99),
        annotation.get("provider", ""),
        annotation.get("resource_type", ""),
        annotation.get("matched_plan_path", ""),
    )
