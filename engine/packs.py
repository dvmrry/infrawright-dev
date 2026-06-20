"""Pack discovery and manifest merge.

The engine carries zero vendor knowledge. Each pack under packs/<name>/
ships a pack.json manifest (provider prefixes, registry sources, unescape
products, scope segments) plus its data (registry.json, overrides/,
schemas/, adoption_status.json). This module discovers packs and exposes
the merged tables plus the active pack root the engine reads data from.

Stdlib-only, Python 3.6-floor.
"""
import json
import os

PACKS_ROOT = "packs"

_MANIFESTS = None


def _manifests():
    global _MANIFESTS
    if _MANIFESTS is None:
        found = []
        if os.path.isdir(PACKS_ROOT):
            for name in sorted(os.listdir(PACKS_ROOT)):
                path = os.path.join(PACKS_ROOT, name, "pack.json")
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


def product_tokens():
    return sorted(set(provider_prefixes().values()))


def provider_of(resource_type):
    for prefix, provider in provider_prefixes().items():
        if resource_type.startswith(prefix):
            return provider
    return resource_type.split("_", 1)[0]


def pack_root():
    """The active pack's directory. Phase 1 is single-pack: the sole pack
    shipping a registry.json. Phase 2 (multiple packs) replaces this with
    per-resource resolution via provider_of()."""
    roots = []
    if os.path.isdir(PACKS_ROOT):
        for name in sorted(os.listdir(PACKS_ROOT)):
            if os.path.isfile(os.path.join(PACKS_ROOT, name, "registry.json")):
                roots.append(os.path.join(PACKS_ROOT, name))
    if not roots:
        raise RuntimeError("no pack with a registry.json under %r" % PACKS_ROOT)
    return roots[0]
