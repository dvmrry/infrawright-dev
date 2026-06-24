"""Shared assert-adoptable guidance annotation helpers.

This module only normalizes and renders additive guidance annotations for
blocked assert-adoptable output. It does not classify plans, change drift
policy, alter projection, mutate provider configuration, or execute Terraform.
"""
import json


LANE_PROVIDER_CONFIG = "provider_config"
LANE_ABSENT_DEFAULT = "absent_default"
STATUS_EFFECT_BLOCKED = "informational only; plan remains blocked"

_LANE_ORDER = {
    LANE_PROVIDER_CONFIG: 0,
    LANE_ABSENT_DEFAULT: 1,
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


def annotations_for_finding_path(annotations, finding, path):
    """Return sorted annotations for a blocked finding path."""
    from engine import schema_paths

    key = (
        finding.get("source"),
        finding.get("address"),
        schema_paths.format_path(path),
    )
    return sort_annotations([
        annotation for annotation in (annotations or [])
        if _annotation_key(annotation) == key
    ])


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
    if provider_config:
        _print_provider_config(provider_config, write)
    if absent_default:
        _print_absent_default(absent_default, write)


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
