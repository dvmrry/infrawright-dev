"""Cross-class inventory report over validated adoption metadata.

This module is read-only. It aggregates provider-config, absent/default, and
dynamic-schema metadata from pack manifests and presents it in a single report.
It does not project, omit, change drift policy, alter assert-adoptable, render
provider configuration, run Terraform/OpenTofu, or enforce cross-design rules.
"""
import json

from engine import packs


_CLASSES = set(["provider_config", "absent_default", "dynamic_schema"])


def build_report(provider=None, resource_type=None, metadata_class=None):
    """Return a read-only inventory report over committed adoption metadata.

    The report contains:
      - inventory: normalized list of metadata items across all lanes
      - diagnostics: cross-class overlap/info observations (never failures)
    """
    inventory = build_inventory(
        provider=provider,
        resource_type=resource_type,
        metadata_class=metadata_class,
    )
    diagnostics = _diagnostics(inventory)
    return {
        "inventory": inventory,
        "diagnostics": diagnostics,
        "summary": {
            "total": len(inventory),
            "by_class": _count_by(inventory, "class"),
            "by_provider": _count_by(inventory, "provider"),
            "diagnostic_warnings": len([d for d in diagnostics if d["severity"] == "warning"]),
            "diagnostic_info": len([d for d in diagnostics if d["severity"] == "info"]),
        },
    }


def build_inventory(provider=None, resource_type=None, metadata_class=None):
    """Normalize all committed metadata into a common inventory shape."""
    items = []
    items.extend(_provider_config_items())
    items.extend(_absent_default_items())
    items.extend(_dynamic_schema_items())

    if provider:
        items = [i for i in items if i.get("provider") == provider]
    if metadata_class:
        items = [i for i in items if i.get("class") == metadata_class]
    if resource_type:
        items = [
            i for i in items
            if _matches_resource_type(i, resource_type)
        ]

    return _sort_inventory(items)


def _provider_config_items():
    out = []
    for req in packs.provider_config_requirements():
        remediation = req.get("remediation") or {}
        item = {
            "provider": req["provider"],
            "class": "provider_config",
            "kind": remediation.get("kind", "provider_argument"),
            "action": remediation.get("mode", "diagnostic_only"),
            "behavior_effect": "guidance_only",
            "evidence": remediation.get("evidence") or _evidence_from_reason(req.get("reason", "")),
            "reason": req["reason"],
            "source": req,
            "setting": req.get("setting"),
            "value": req.get("value"),
            "resource_types": sorted(req.get("resource_types") or []),
            "resource_prefixes": sorted(req.get("resource_prefixes") or []),
            "plan_paths": sorted(req.get("plan_paths") or []),
        }
        out.append(item)
    return out


def _absent_default_items():
    out = []
    for rule in packs.absent_default_rules():
        item = {
            "provider": rule["provider"],
            "class": "absent_default",
            "kind": rule["kind"],
            "action": rule["action"],
            "behavior_effect": "validation_only",
            "evidence": rule["evidence"],
            "reason": rule["reason"],
            "source": rule,
            "path": rule.get("path"),
            "observed_value": rule.get("observed_value"),
        }
        if "resource_type" in rule:
            item["resource_type"] = rule["resource_type"]
        if "resource_prefix" in rule:
            item["resource_prefix"] = rule["resource_prefix"]
        out.append(item)
    return out


def _dynamic_schema_items():
    out = []
    for rule in packs.dynamic_schema_rules():
        item = {
            "provider": rule["provider"],
            "class": "dynamic_schema",
            "kind": rule["kind"],
            "action": rule["action"],
            "behavior_effect": "validation_only",
            "evidence": rule["evidence"],
            "reason": rule["reason"],
            "source": rule,
            "path": rule.get("path"),
            "ownership": rule.get("ownership"),
            "provider_version_constraint": rule.get("provider_version_constraint"),
        }
        if "resource_type" in rule:
            item["resource_type"] = rule["resource_type"]
        if "resource_prefix" in rule:
            item["resource_prefix"] = rule["resource_prefix"]
        out.append(item)
    return out


def _matches_resource_type(item, resource_type):
    if item.get("resource_type") == resource_type:
        return True
    if resource_type in (item.get("resource_types") or []):
        return True
    prefix = item.get("resource_prefix")
    if prefix and resource_type.startswith(prefix):
        return True
    for p in item.get("resource_prefixes") or []:
        if resource_type.startswith(p):
            return True
    return False


def _sort_inventory(items):
    def key(item):
        provider = item.get("provider", "")
        cls = item.get("class", "")
        scope = item.get("resource_type") or item.get("resource_prefix") or ""
        if not scope and item.get("resource_types"):
            scope = item["resource_types"][0]
        if not scope and item.get("resource_prefixes"):
            scope = item["resource_prefixes"][0]
        path_or_setting = item.get("path") or item.get("setting") or ""
        ident = item.get("source", {}).get("id", "")
        return (provider, cls, scope, path_or_setting, ident)
    return sorted(items, key=key)


def _diagnostics(inventory):
    out = []
    out.extend(_absent_dynamic_overlap_warnings(inventory))
    out.extend(_provider_config_path_warnings(inventory))
    out.extend(_shared_evidence_info(inventory))
    return _sort_diagnostics(out)


def _absent_dynamic_overlap_warnings(inventory):
    out = []
    absent = _resource_path_keys(inventory, "absent_default")
    dynamic = _resource_path_keys(inventory, "dynamic_schema")
    for key in sorted(absent & dynamic):
        provider, scope_type, scope_value, path = key
        out.append({
            "severity": "warning",
            "provider": provider,
            scope_type: scope_value,
            "path": path,
            "classes": ["absent_default", "dynamic_schema"],
            "message": (
                "same provider/resource scope/path appears in both "
                "absent_default and dynamic_schema"
            ),
        })
    return out


def _resource_path_keys(inventory, cls):
    out = set()
    for item in inventory:
        if item.get("class") != cls:
            continue
        provider = item.get("provider")
        path = item.get("path")
        if not path:
            continue
        scope = _item_scope(item)
        if scope:
            out.add((provider, scope[0], scope[1], path))
    return out


def _item_scope(item):
    if item.get("resource_type"):
        return ("resource_type", item["resource_type"])
    if item.get("resource_prefix"):
        return ("resource_prefix", item["resource_prefix"])
    return None


def _provider_config_path_warnings(inventory):
    out = []
    pc_items = [i for i in inventory if i.get("class") == "provider_config"]
    other_paths = _path_index_by_provider(inventory, exclude_class="provider_config")
    for item in pc_items:
        provider = item.get("provider")
        for path in item.get("plan_paths") or []:
            matches = other_paths.get((provider, path), set())
            for cls in sorted(matches):
                out.append({
                    "severity": "warning",
                    "provider": provider,
                    "path": path,
                    "classes": ["provider_config", cls],
                    "message": (
                        "provider-config plan_path exactly matches a %s path" % cls
                    ),
                })
    return out


def _path_index_by_provider(inventory, exclude_class):
    out = {}
    for item in inventory:
        if item.get("class") == exclude_class:
            continue
        provider = item.get("provider")
        path = item.get("path")
        if not path:
            continue
        key = (provider, path)
        out.setdefault(key, set()).add(item["class"])
    return out


def _shared_evidence_info(inventory):
    out = []
    evidence_to_classes = {}
    for item in inventory:
        evidence = item.get("evidence")
        if not evidence:
            continue
        evidence_to_classes.setdefault(evidence, set()).add(item["class"])
    for evidence in sorted(evidence_to_classes):
        classes = sorted(evidence_to_classes[evidence])
        if len(classes) > 1:
            out.append({
                "severity": "info",
                "evidence": evidence,
                "classes": classes,
                "message": "same evidence doc is used by multiple classes",
            })
    return out


def _sort_diagnostics(diagnostics):
    def key(d):
        return (
            d["severity"],
            d.get("provider", ""),
            d.get("resource_type", ""),
            d.get("resource_prefix", ""),
            d.get("path", ""),
            d.get("evidence", ""),
            d["message"],
        )
    return sorted(diagnostics, key=key)


def _count_by(items, key):
    counts = {}
    for item in items:
        value = item.get(key, "unknown")
        counts[value] = counts.get(value, 0) + 1
    return counts


def _evidence_from_reason(reason):
    # Provider-config items without explicit remediation evidence have no
    # dedicated evidence doc; leave it empty rather than invent one.
    return ""


def to_json(report, indent=2):
    """Return deterministic JSON for the report."""
    return json.dumps(
        report,
        indent=indent,
        sort_keys=True,
        ensure_ascii=False,
        default=str,
    )


def to_markdown(report):
    """Return a human-readable markdown table for the inventory."""
    lines = []
    lines.append("# Adoption Metadata Inventory")
    lines.append("")
    summary = report.get("summary", {})
    lines.append("- Total items: %d" % summary.get("total", 0))
    lines.append("- Warnings: %d" % summary.get("diagnostic_warnings", 0))
    lines.append("- Info: %d" % summary.get("diagnostic_info", 0))
    lines.append("")
    lines.append("| Provider | Class | Scope | Path/Setting | Action | Behavior | Evidence |")
    lines.append("|---|---|---|---|---|---|---|")
    for item in report.get("inventory", []):
        provider = item.get("provider", "")
        cls = item.get("class", "")
        scope = _markdown_scope(item)
        path_or_setting = item.get("path") or item.get("setting") or ""
        action = item.get("action", "")
        behavior = item.get("behavior_effect", "")
        evidence = item.get("evidence", "")
        lines.append(
            "| %s | %s | %s | %s | %s | %s | %s |" % (
                provider, cls, scope, path_or_setting, action, behavior, evidence
            )
        )

    diagnostics = report.get("diagnostics", [])
    if diagnostics:
        lines.append("")
        lines.append("## Cross-class diagnostics")
        lines.append("")
        lines.append("| Severity | Provider | Scope | Path/Evidence | Classes | Message |")
        lines.append("|---|---|---|---|---|---|")
        for d in diagnostics:
            severity = d.get("severity", "")
            provider = d.get("provider", "")
            scope = d.get("resource_type") or d.get("resource_prefix") or d.get("evidence") or ""
            path_or_evidence = d.get("path") or d.get("evidence") or ""
            classes = ", ".join(d.get("classes", []))
            message = d.get("message", "")
            lines.append(
                "| %s | %s | %s | %s | %s | %s |" % (
                    severity, provider, scope, path_or_evidence, classes, message
                )
            )

    return "\n".join(lines) + "\n"


def _markdown_scope(item):
    if item.get("resource_type"):
        return item["resource_type"]
    if item.get("resource_prefix"):
        return item["resource_prefix"]
    if item.get("resource_types"):
        return ", ".join(item["resource_types"])
    if item.get("resource_prefixes"):
        return ", ".join(item["resource_prefixes"])
    return ""
