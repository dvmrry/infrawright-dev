"""Pack discovery and manifest merge.

The engine carries zero vendor knowledge. Each pack under packs/<name>/
ships a pack.json manifest (provider prefixes, registry sources, unescape
products, scope segments) plus its data (registry.json, overrides/,
schemas/, adoption_status.json). This module discovers packs and exposes
the merged tables plus the active pack root the engine reads data from.

Stdlib-only, Python 3.6-floor.
"""
import importlib
import json
import os

# The packs/ dir is anchored to the install (engine/.. == repo root), NOT the
# current working directory: importing the engine from any cwd must neither
# crash nor silently resolve to a different packs/. Override with the
# INFRAWRIGHT_PACKS env var (re-read on every call, so tests can swap it).
_DEFAULT_PACKS_ROOT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "packs")

_MANIFESTS = None


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
                path = os.path.join(root, name, "pack.json")
                if os.path.isfile(path):
                    with open(path, encoding="utf-8") as f:
                        manifest = json.load(f)
                    manifest["_name"] = name
                    found.append(manifest)
        _MANIFESTS = found
    return _MANIFESTS


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


def collector_for(provider):
    """Collector module for a provider pack.

    Provider collectors live at packs/<provider>/collector.py and expose the
    small auth/URL contract consumed by collectors.rest.
    """
    return importlib.import_module("packs.%s.collector" % provider)


def _registry_packs():
    roots = []
    root = packs_root()
    if os.path.isdir(root):
        for name in sorted(os.listdir(root)):
            if os.path.isfile(os.path.join(root, name, "registry.json")):
                roots.append(os.path.join(root, name))
    return roots


def pack_root():
    """The active pack's directory — the sole pack shipping a registry.json.

    Single-pack by construction in Phase 1. If a SECOND registry-bearing pack
    is added, this RAISES rather than silently shadowing one pack with the
    alphabetically-first: multi-pack data resolution must be per-resource
    (owning pack derived from the registry 'product' field), which lands with
    the second pack (Phase 2).
    """
    roots = _registry_packs()
    if not roots:
        raise RuntimeError("no pack with a registry.json under %r" % packs_root())
    if len(roots) > 1:
        names = ", ".join(os.path.basename(r) for r in roots)
        raise RuntimeError(
            "multiple registry-bearing packs (%s): single active-pack "
            "resolution is ambiguous. Per-resource resolution by the registry "
            "'product' field is required for multi-pack (Phase 2)." % names)
    return roots[0]


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


def pack_dir_for_resource(resource_type):
    return pack_dir_for_provider(provider_of(resource_type))


def overrides_dir_for(resource_type):
    return os.path.join(pack_dir_for_resource(resource_type), "overrides")


def schema_path_for(provider):
    return os.path.join(
        pack_dir_for_provider(provider), "schemas", "provider", provider + ".json")


def registry_paths():
    """Every pack's registry.json, which load_registry() merges. One today;
    one per provider after the split."""
    out = []
    root = packs_root()
    if os.path.isdir(root):
        for name in sorted(os.listdir(root)):
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


def adoption_status_paths():
    """Every adoption_status.json under packs/ (vendor-shared in _shared/ today;
    per-provider later). load_status merges them."""
    out = []
    root = packs_root()
    if os.path.isdir(root):
        for dirpath, _dirs, files in os.walk(root):
            if "adoption_status.json" in files:
                out.append(os.path.join(dirpath, "adoption_status.json"))
    return sorted(out)


def schema_extract_path():
    """The sole schema-extract/main.tf under packs/ (the schema-dump pin source
    for `make schemas`; vendor-shared in _shared/ today)."""
    root = packs_root()
    if os.path.isdir(root):
        for dirpath, _dirs, files in os.walk(root):
            if "main.tf" in files and os.path.basename(dirpath) == "schema-extract":
                return os.path.join(dirpath, "main.tf")
    return None
