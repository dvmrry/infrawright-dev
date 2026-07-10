"""Validate pack and registry metadata.

This command is a thin authoring gate over the pack/registry validators. It
does not load provider schemas, run collectors, generate Terraform, or change
adoption behavior.
"""
import json
import os
import sys

from engine import packs
from engine import registry
from engine.overrides import validate_override_metadata


def _usage():
    return (
        "usage: python -m engine.check_pack [--pack <name>|PACK=<name>]\n"
    )


def _load_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _discover_pack_names(root):
    if not os.path.isdir(root):
        return []
    out = []
    for name in sorted(os.listdir(root)):
        if name == "_shared":
            continue
        pack_path = os.path.join(root, name, "pack.json")
        if os.path.isfile(pack_path):
            out.append(name)
    return out


def _parse_args(argv):
    argv = list(argv)
    pack = None
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == "--pack":
            i += 1
            if i >= len(argv):
                raise ValueError("--pack requires a value")
            pack = argv[i]
        elif arg.startswith("PACK="):
            pack = arg.split("=", 1)[1]
            if not pack:
                raise ValueError("PACK= requires a value")
        elif arg in ("-h", "--help"):
            raise SystemExit(2)
        else:
            raise ValueError("unknown argument %s" % arg)
        i += 1
    return pack


def _validate_one(root, name):
    pack_dir = os.path.join(root, name)
    pack_path = os.path.join(pack_dir, "pack.json")
    if not os.path.isfile(pack_path):
        raise ValueError("unknown pack %r under %s" % (name, root))
    packs.validate_pack_metadata(_load_json(pack_path), path=pack_path)

    registry_path = os.path.join(pack_dir, "registry.json")
    registry_data = None
    if os.path.isfile(registry_path):
        registry_data = _load_json(registry_path)
        registry.validate_registry(registry_data, path=registry_path)
    _validate_overrides(pack_dir)
    return registry_path, registry_data


def _validate_overrides(pack_dir):
    overrides_dir = os.path.join(pack_dir, "overrides")
    if not os.path.isdir(overrides_dir):
        return
    for name in sorted(os.listdir(overrides_dir)):
        path = os.path.join(overrides_dir, name)
        if os.path.isfile(path) and name.endswith(".json"):
            validate_override_metadata(_load_json(path), path=path)


def validate_packs(pack=None):
    root = packs.packs_root()
    if pack is None:
        names = _discover_pack_names(root)
    else:
        names = [pack]

    registries = []
    for name in names:
        registries.append(_validate_one(root, name))
    if pack is None:
        registry.check_duplicate_resource_types(registries)
    return names


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    try:
        pack = _parse_args(argv)
        names = validate_packs(pack=pack)
    except SystemExit:
        sys.stderr.write(_usage())
        return 2
    except (IOError, OSError, ValueError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1

    if names:
        sys.stdout.write("validated packs: %s\n" % ", ".join(names))
    else:
        sys.stdout.write("validated packs: none\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
