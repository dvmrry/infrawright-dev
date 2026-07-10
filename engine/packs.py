"""Pack discovery and manifest merge.

The engine carries zero vendor knowledge. Each pack under packs/<name>/
ships a pack.json manifest (provider prefixes, registry sources, unescape
products, scope segments) plus its data (registry.json, overrides/,
schemas/, adoption_status.json). This module discovers packs and exposes
the merged tables the engine reads data from.

Stdlib-only, Python 3.6-floor.
"""
import importlib
import json
import os

from engine import manifest_checks

# The packs/ dir is anchored to the install (engine/.. == repo root), NOT the
# current working directory: importing the engine from any cwd must neither
# crash nor silently resolve to a different packs/. Override with the
# INFRAWRIGHT_PACKS env var (re-read on every call, so tests can swap it).
_DEFAULT_PACKS_ROOT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "packs")

_MANIFESTS = None

PACK_METADATA_KEYS = set([
    "absent_defaults",
    "drift_policy",
    "dynamic_schema",
    "lookup_sources",
    "pin",
    "provider_config",
    "provider_prefixes",
    "provider_sources",
    "references",
    "scope_segments",
    "sensitive_required",
    "unescape_products",
    "vendor",
])

PACK_REQUIRED_KEYS = set()

PACK_DICT_KEYS = set([
    "absent_defaults",
    "drift_policy",
    "dynamic_schema",
    "lookup_sources",
    "provider_config",
    "provider_prefixes",
    "provider_sources",
    "references",
    "scope_segments",
    "sensitive_required",
])

PACK_STRING_KEYS = set(["pin", "vendor"])
PACK_LIST_KEYS = set(["unescape_products"])


def packs_root():
    """Effective packs/ directory: INFRAWRIGHT_PACKS if set, else the
    install's packs/ (anchored to engine/.., never the cwd)."""
    return os.environ.get("INFRAWRIGHT_PACKS") or _DEFAULT_PACKS_ROOT


# Back-compat module attribute for display/reference; prefer packs_root().
PACKS_ROOT = _DEFAULT_PACKS_ROOT


def reset():
    """Drop cached manifests so a later pack change (or an INFRAWRIGHT_PACKS
    swap in tests) is re-discovered. Mirrors registry.reload_registry()."""
    global _MANIFESTS
    _MANIFESTS = None


def _manifests():
    global _MANIFESTS
    if _MANIFESTS is None:
        found = []
        root = packs_root()
        if os.path.isdir(root):
            for name in sorted(os.listdir(root)):
                if name == "_shared":
                    continue
                path = os.path.join(root, name, "pack.json")
                if os.path.isfile(path):
                    with open(path, encoding="utf-8") as f:
                        manifest = json.load(f)
                    validate_pack_metadata(manifest, path=path)
                    manifest["_name"] = name
                    found.append(manifest)
        _MANIFESTS = found
    return _MANIFESTS


def _where(path):
    return path or "pack.json"


def _require_type(data, key, expected, path):
    if key in data and not isinstance(data[key], expected):
        typename = _type_name(expected)
        article = "an" if typename.startswith(("o", "a", "e", "i", "u")) else "a"
        raise ValueError("%s.%s must be %s %s" % (
            path, key, article, typename
        ))


def _type_name(expected):
    if expected is dict:
        return "object"
    if expected is list:
        return "list"
    if expected is str:
        return "string"
    return expected.__name__


def _validate_unescape_products(value, path):
    for idx, item in enumerate(value):
        if not isinstance(item, str) or not item:
            raise ValueError("%s[%d] must be a non-empty string" % (path, idx))


def _validate_lookup_sources(value, path):
    for resource_type, item in value.items():
        if not isinstance(resource_type, str) or not resource_type:
            raise ValueError("%s keys must be non-empty strings" % path)
        if not isinstance(item, dict):
            raise ValueError("%s.%s must be an object" % (path, resource_type))
        manifest_checks.reject_unknown_keys(item, set(["name_field"]), "%s.%s" % (
            path, resource_type
        ))
        manifest_checks.require_keys(item, set(["name_field"]), "%s.%s" % (
            path, resource_type
        ))
        if not isinstance(item["name_field"], str) or not item["name_field"]:
            raise ValueError("%s.%s.name_field must be a non-empty string" % (
                path, resource_type
            ))


def _validate_references(value, path):
    for resource_type, fields in value.items():
        if not isinstance(resource_type, str) or not resource_type:
            raise ValueError("%s keys must be non-empty strings" % path)
        if not isinstance(fields, dict):
            raise ValueError("%s.%s must be an object" % (path, resource_type))
        for field, item in fields.items():
            if not isinstance(field, str) or not field:
                raise ValueError("%s.%s keys must be non-empty strings" % (
                    path, resource_type
                ))
            label = "%s.%s.%s" % (path, resource_type, field)
            if not isinstance(item, dict):
                raise ValueError("%s must be an object" % label)
            manifest_checks.reject_unknown_keys(
                item, set(["name_field", "referent"]), label
            )
            manifest_checks.require_keys(
                item, set(["name_field", "referent"]), label
            )
            for key in ("name_field", "referent"):
                if not isinstance(item[key], str) or not item[key]:
                    raise ValueError("%s.%s must be a non-empty string" % (
                        label, key
                    ))


def _validate_rule_group(data, key, path):
    if key not in data:
        return
    group = data[key]
    manifest_checks.reject_unknown_keys(group, set(["rules"]), "%s.%s" % (path, key))
    if "rules" not in group:
        raise ValueError("%s.%s missing required key rules" % (path, key))
    if not isinstance(group["rules"], list):
        raise ValueError("%s.%s.rules must be a list" % (path, key))


def _validate_provider_config(data, path):
    if "provider_config" not in data:
        return
    group = data["provider_config"]
    manifest_checks.reject_unknown_keys(
        group, set(["requirements"]), "%s.provider_config" % path
    )
    if "requirements" not in group:
        raise ValueError(
            "%s.provider_config missing required key requirements" % path
        )
    if not isinstance(group["requirements"], list):
        raise ValueError("%s.provider_config.requirements must be a list" % path)


def validate_pack_metadata(data, path=None):
    """Validate pack.json metadata vocabulary.

    This is intentionally a small structural gate. Detailed diagnostic-rule
    semantics stay in their lane-specific validators.
    """
    path = _where(path)
    if not isinstance(data, dict):
        raise ValueError("%s must contain a JSON object" % path)
    manifest_checks.reject_unknown_keys(data, PACK_METADATA_KEYS, path)
    manifest_checks.require_keys(data, PACK_REQUIRED_KEYS, path)

    for key in PACK_DICT_KEYS:
        _require_type(data, key, dict, path)
    for key in PACK_STRING_KEYS:
        _require_type(data, key, str, path)
        if key in data and not data[key]:
            raise ValueError("%s.%s must be a non-empty string" % (path, key))
    for key in PACK_LIST_KEYS:
        _require_type(data, key, list, path)

    manifest_checks.validate_string_map(
        data.get("provider_prefixes", {}), "%s.provider_prefixes" % path
    )
    manifest_checks.validate_string_map(
        data.get("provider_sources", {}), "%s.provider_sources" % path
    )
    manifest_checks.validate_string_map(
        data.get("scope_segments", {}), "%s.scope_segments" % path
    )
    if "unescape_products" in data:
        _validate_unescape_products(
            data["unescape_products"], "%s.unescape_products" % path
        )
    if "lookup_sources" in data:
        _validate_lookup_sources(
            data["lookup_sources"], "%s.lookup_sources" % path
        )
    if "references" in data:
        _validate_references(data["references"], "%s.references" % path)
    for key in ("absent_defaults", "dynamic_schema", "sensitive_required"):
        _validate_rule_group(data, key, path)
    if "drift_policy" in data:
        from engine.drift_policy import DriftPolicy
        DriftPolicy(data["drift_policy"], source="%s.drift_policy" % path)
    _validate_provider_config(data, path)
    return data


def provider_prefixes():
    merged = {}
    for m in _manifests():
        merged.update(m.get("provider_prefixes", {}))
    return merged


def provider_sources():
    merged = {}
    for m in _manifests():
        merged.update(m.get("provider_sources", {}))
    return merged


def unescape_products():
    out = []
    for m in _manifests():
        out.extend(m.get("unescape_products", []))
    return tuple(out)


def scope_segments():
    merged = {}
    for m in _manifests():
        merged.update(m.get("scope_segments", {}))
    return merged


def references():
    """Merged reference graph: {referrer_type: {field: {referent, name_field}}}."""
    merged = {}
    for m in _manifests():
        for rt, fields in m.get("references", {}).items():
            merged.setdefault(rt, {}).update(fields)
    return merged


def lookup_sources():
    """Merged {referent_type: {name_field}} — types that emit a .lookup.json sidecar."""
    merged = {}
    for m in _manifests():
        merged.update(m.get("lookup_sources", {}))
    return merged


def product_tokens():
    return sorted(set(provider_prefixes().values()))


def provider_of(resource_type):
    """Resolve a resource type to its provider short-name.

    Longest-match-wins so overlapping prefixes (e.g. zpa_ vs a future
    zpa_lss_) resolve deterministically regardless of manifest discovery
    order. The table VALUE is authoritative; the bare split is only a
    last-resort fallback for a type with no declared prefix.
    """
    prefixes = provider_prefixes()
    for prefix in sorted(prefixes, key=len, reverse=True):
        if resource_type.startswith(prefix):
            return prefixes[prefix]
    return resource_type.split("_", 1)[0]


def bare_name(resource_type):
    """Resource type with its provider prefix stripped for artifact leaf names.

    Falls back to the full type if stripping would leave nothing (a type equal
    to a bare prefix), so an artifact leaf name is never empty."""
    prefixes = provider_prefixes()
    for prefix in sorted(prefixes, key=len, reverse=True):
        if resource_type.startswith(prefix):
            return resource_type[len(prefix):].lstrip("_") or resource_type
    return resource_type


def collector_for(provider):
    """Collector module for a provider pack.

    Provider collectors live at packs/<provider>/collector.py and expose the
    small auth/URL contract consumed by engine.collectors.rest.
    """
    return importlib.import_module("packs.%s.collector" % provider)


# --- per-resource resolution (provider-first) -------------------------------
# Each artifact resolves to the pack that OWNS the resource's provider, via the
# manifest's provider_prefixes. Single-pack today (zia/zpa/zcc all -> the one
# zscaler pack, so these are no-ops); per-provider after the split.

def pack_dir_for_provider(provider):
    """Directory of the pack whose manifest declares `provider`."""
    for m in _manifests():
        if provider in m.get("provider_prefixes", {}).values():
            return os.path.join(packs_root(), m["_name"])
    raise RuntimeError("no pack declares provider %r" % provider)


def vendor_of(provider):
    """Vendor namespace a provider belongs to (pack.json "vendor"), or None for
    a standalone provider with its own top-level pack and no shared vendor lib
    (e.g. a single-token provider like cloudflare)."""
    for m in _manifests():
        if provider in m.get("provider_prefixes", {}).values():
            return m.get("vendor")
    return None


def pack_dir_for_resource(resource_type):
    return pack_dir_for_provider(provider_of(resource_type))


def overrides_dir_for(resource_type):
    return os.path.join(pack_dir_for_resource(resource_type), "overrides")


def schema_path_for(provider):
    return os.path.join(
        pack_dir_for_provider(provider), "schemas", "provider", provider + ".json")


def oracle_provider_config_path(provider):
    """Optional provider config override for oracle scratch roots.

    Default adoption roots use an empty provider block with credentials from
    provider environment variables. Packs that need explicit config can ship:
      packs/<pack>/oracle/<provider>.tf
    """
    path = os.path.join(pack_dir_for_provider(provider), "oracle", provider + ".tf")
    return path if os.path.exists(path) else None


def registry_paths():
    """Every pack's registry.json, which load_registry() merges. One today;
    one per provider after the split."""
    out = []
    root = packs_root()
    if os.path.isdir(root):
        for name in sorted(os.listdir(root)):
            if name == "_shared":
                continue
            p = os.path.join(root, name, "registry.json")
            if os.path.isfile(p):
                out.append(p)
    return out


def provider_pins():
    """{provider: terraform-provider-version} from each pack manifest's pin."""
    out = {}
    for m in _manifests():
        pin = m.get("pin")
        if pin:
            for provider in m.get("provider_prefixes", {}).values():
                out[provider] = pin
    return out


def provider_config_requirements(provider=None):
    """Provider-config diagnostic metadata declared by pack manifests.

    Shape:
      "provider_config": {
        "requirements": [
          {
            "id": "google_disable_attribution_label",
            "provider": "google",
            "setting": "add_terraform_attribution_label",
            "value": false,
            "plan_paths": ["terraform_labels.goog-terraform-provisioned"]
          }
        ]
      }

    This only exposes metadata for diagnostics. It does not render provider
    configuration or change adoption behavior.
    """
    out = []
    for m in _manifests():
        cfg = m.get("provider_config") or {}
        providers = sorted(set((m.get("provider_prefixes") or {}).values()))
        for req in cfg.get("requirements") or []:
            item = dict(req)
            if "provider" not in item and len(providers) == 1:
                item["provider"] = providers[0]
            if provider is None or item.get("provider") == provider:
                out.append(item)
    return out


def _lane_rules(manifest_key, provider, validate):
    out = []
    for m in _manifests():
        cfg = m.get(manifest_key) or {}
        providers = sorted(set((m.get("provider_prefixes") or {}).values()))
        for rule in cfg.get("rules") or []:
            item = dict(rule)
            if "provider" not in item and len(providers) == 1:
                item["provider"] = providers[0]
            if provider is None or item.get("provider") == provider:
                out.append(item)
    return validate(out, provider_prefixes=provider_prefixes())


def absent_default_rules(provider=None):
    """Absent/default rule metadata declared by pack manifests.

    Shape:
      "absent_defaults": {
        "rules": [
          {
            "id": "netbox_device_empty_rack_face_placeholder",
            "provider": "netbox",
            "resource_type": "netbox_device",
            "path": "rack_face",
            "kind": "provider_absent_placeholder",
            "observed_value": "",
            "action": "manual_review_required",
            "evidence": "docs/provider-labs/netbox-pr22.md",
            "reason": "Provider reported an empty string placeholder for an absent optional rack face."
          }
        ]
      }

    This only exposes validated metadata. It does not normalize values, omit
    values, change projection, or change drift policy.
    """
    from engine import absent_defaults_validator
    return _lane_rules(
        "absent_defaults", provider,
        absent_defaults_validator.validate_absent_default_rules,
    )


def dynamic_schema_rules(provider=None):
    """Dynamic-schema rule metadata declared by pack manifests.

    Shape:
      "dynamic_schema": {
        "rules": [
          {
            "id": "cloudflare_ruleset_action_parameters_dynamic_map",
            "provider": "cloudflare",
            "provider_version_constraint": ">= 4.0.0, < 5.0.0",
            "resource_type": "cloudflare_ruleset",
            "path": "rules[].action_parameters",
            "kind": "provider_observed_projection_unsafe",
            "ownership": "server_owned",
            "action": "diagnostic_only",
            "evidence": "docs/provider-labs/cloudflare-free-tier-pr32.md",
            "reason": "Provider exposes a dynamic nested map; schema cannot prove stable projection semantics."
          }
        ]
      }

    This only exposes validated metadata. It does not project paths, omit paths,
    change projection, or change drift policy.
    """
    from engine import dynamic_schema_validator
    return _lane_rules(
        "dynamic_schema", provider,
        dynamic_schema_validator.validate_dynamic_schema_rules,
    )


def sensitive_required_rules(provider=None):
    """Sensitive-required rule metadata declared by pack manifests.

    Shape:
      "sensitive_required": {
        "rules": [
          {
            "id": "grafana_contact_point_webhook_required_sensitive",
            "provider": "grafana",
            "provider_version_constraint": ">= 3.0.0",
            "resource_type": "grafana_contact_point",
            "path": "webhook",
            "kind": "sensitive_required_block",
            "sensitivity": "contains_sensitive_fields",
            "structural_requirement": "one_of_block_required",
            "action": "manual_review_required",
            "evidence": "docs/provider-labs/grafana-pr24.md",
            "reason": "One of the contact-point notifier blocks must be present..."
          }
        ]
      }

    This only exposes validated metadata. It does not project sensitive values,
    render placeholders, omit paths, or change adoption behavior.
    """
    from engine import sensitive_required_validator
    return _lane_rules(
        "sensitive_required", provider,
        sensitive_required_validator.validate_sensitive_required_rules,
    )


def drift_policy_data():
    out = {"version": 1, "resource_types": {}}
    for manifest in _manifests():
        data = manifest.get("drift_policy") or {}
        for resource_type, cfg in (data.get("resource_types") or {}).items():
            merged = out["resource_types"].setdefault(resource_type, {})
            for mode, entries in (cfg or {}).items():
                merged.setdefault(mode, [])
                merged[mode].extend(json.loads(json.dumps(entries)))
    return out


def _component_roots():
    """Installed pack/shared directories that own recursively loaded data."""
    root = packs_root()
    if not os.path.isdir(root):
        return []
    out = []
    for name in sorted(os.listdir(root)):
        path = os.path.join(root, name)
        if not os.path.isdir(path):
            continue
        if name != "_shared":
            out.append(path)
            continue
        for shared_name in sorted(os.listdir(path)):
            shared_path = os.path.join(path, shared_name)
            if os.path.isdir(shared_path):
                out.append(shared_path)
    return out


def adoption_status_paths():
    """Every component-owned adoption_status.json.

    Loose files at the pack root or directly under ``_shared`` have no profile
    identity and are therefore not runtime inputs.
    """
    out = []
    for component_root in _component_roots():
        for dirpath, _dirs, files in os.walk(component_root):
            if "adoption_status.json" in files:
                out.append(os.path.join(dirpath, "adoption_status.json"))
    return sorted(out)


def schema_extract_path():
    """The sole schema-extract/main.tf under packs/ (the schema-dump pin source;
    vendor-shared in _shared/ today)."""
    for component_root in _component_roots():
        for dirpath, _dirs, files in os.walk(component_root):
            if "main.tf" in files and os.path.basename(dirpath) == "schema-extract":
                return os.path.join(dirpath, "main.tf")
    return None
