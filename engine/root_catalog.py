"""Build the validated root-resolution catalog consumed by the Node host.

This is a transition boundary, not a second pack loader.  The existing Python
pack and registry loaders remain authoritative while their full validation
surface is migrated.  The Node runtime consumes the resulting versioned data
without invoking Python.
"""
import argparse
import hashlib
import json
import os
import sys

from engine import packs
from engine.registry import load_registry


ROOT_CATALOG_CONTRACT = "infrawright.root_catalog"
ROOT_CATALOG_SCHEMA_VERSION = 1


def _selected_providers(value):
    selected = sorted(set(value or []))
    declared = sorted(set(packs.provider_prefixes().values()))
    unknown = sorted(set(selected) - set(declared))
    if unknown:
        raise ValueError(
            "unknown provider(s): %s" % ", ".join(unknown)
        )
    return selected or declared


def _matching_prefix(resource_type, provider):
    prefixes = packs.provider_prefixes()
    for prefix in sorted(prefixes, key=len, reverse=True):
        if resource_type.startswith(prefix) and prefixes[prefix] == provider:
            return prefix
    return None


def _slug_label(resource_type, provider):
    prefix = _matching_prefix(resource_type, provider)
    if prefix is None:
        return None
    remainder = resource_type[len(prefix):]
    return prefix + remainder.split("_")[0]


def _source_paths(providers):
    root = os.path.abspath(packs.packs_root())
    selected = set(providers)
    out = []
    if not os.path.isdir(root):
        return out
    for name in sorted(os.listdir(root)):
        if name == "_shared":
            continue
        manifest_path = os.path.join(root, name, "pack.json")
        if not os.path.isfile(manifest_path):
            continue
        with open(manifest_path, encoding="utf-8") as f:
            manifest = json.load(f)
        owned = set((manifest.get("provider_prefixes") or {}).values())
        if not owned.intersection(selected):
            continue
        out.append(manifest_path)
        registry_path = os.path.join(root, name, "registry.json")
        if os.path.isfile(registry_path):
            out.append(registry_path)
    return sorted(out)


def _source_digest(paths):
    root = os.path.abspath(packs.packs_root())
    digest = hashlib.sha256()
    relative_paths = []
    for path in paths:
        relative = os.path.relpath(path, root).replace(os.sep, "/")
        relative_paths.append(relative)
        with open(path, "rb") as f:
            content = f.read()
        digest.update(relative.encode("utf-8"))
        digest.update(b"\0")
        digest.update(content)
        digest.update(b"\0")
    return relative_paths, digest.hexdigest()


def root_catalog(providers=None):
    """Return root-resolution facts derived from fully validated pack data."""
    selected = _selected_providers(providers)
    selected_set = set(selected)
    registry = load_registry()
    resources = []
    for resource_type in sorted(registry):
        provider = packs.provider_of(resource_type)
        if provider not in selected_set:
            continue
        entry = registry[resource_type]
        resource = {
            "bare_name": packs.bare_name(resource_type),
            "derived": bool(entry.get("derive")),
            "generated": bool(entry.get("generate")),
            "product": entry["product"],
            "provider": provider,
            "slug_label": _slug_label(resource_type, provider),
            "type": resource_type,
        }
        if "slug_group" in entry:
            resource["slug_group"] = entry["slug_group"]
        resources.append(resource)
    source_files, sources_sha256 = _source_digest(
        _source_paths(selected)
    )
    return {
        "declared_providers": selected,
        "kind": ROOT_CATALOG_CONTRACT,
        "resources": resources,
        "schema_version": ROOT_CATALOG_SCHEMA_VERSION,
        "source_files": source_files,
        "sources_sha256": sources_sha256,
    }


def render_catalog(providers=None):
    return json.dumps(
        root_catalog(providers=providers), indent=2, sort_keys=True
    ) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(prog="python -m engine.root_catalog")
    parser.add_argument(
        "--providers",
        required=True,
        help="comma-separated provider names included in the catalog",
    )
    parser.add_argument("--out")
    parser.add_argument("--check")
    args = parser.parse_args(argv)
    providers = [item for item in args.providers.split(",") if item]
    try:
        text = render_catalog(providers=providers)
        if args.check:
            with open(args.check, encoding="utf-8") as f:
                actual = f.read()
            if actual != text:
                sys.stderr.write(
                    "error: root catalog is stale: %s\n" % args.check
                )
                return 1
            return 0
        if args.out:
            with open(args.out, "w", encoding="utf-8") as f:
                f.write(text)
        else:
            sys.stdout.write(text)
    except (IOError, OSError, ValueError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
