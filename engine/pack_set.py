"""Validate installed pack sets and pack requirements.

Pack discovery remains owned by :mod:`engine.packs`.  This module adds an
explicit distribution contract around that discovery so an intentionally
small pack root is distinguishable from an accidentally incomplete one.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import re
import sys

from engine import packs
from engine import manifest_checks


PACK_SET_KIND = "infrawright.pack-set"
REQUIREMENTS_KIND = "infrawright.pack-requirements"
FORMAT_VERSION = 1
_KEYS = set(["kind", "version", "packs", "shared"])
_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9_-]*$")


class PackSetError(ValueError):
    pass


def _load_json(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (IOError, OSError, ValueError) as exc:
        raise PackSetError("failed to read %s: %s" % (path, exc))


def validate_names(value, label):
    if not isinstance(value, list):
        raise PackSetError("%s must be a list" % label)
    seen = set()
    for index, name in enumerate(value):
        item_label = "%s[%d]" % (label, index)
        if not isinstance(name, str) or not _NAME_RE.match(name):
            raise PackSetError(
                "%s must be a lowercase pack name" % item_label
            )
        if name in seen:
            raise PackSetError("%s duplicates %r" % (label, name))
        seen.add(name)
    if value != sorted(value):
        raise PackSetError("%s must be sorted" % label)
    return list(value)


def validate_document(data, path, expected_kind):
    if not isinstance(data, dict):
        raise PackSetError("%s must contain a JSON object" % path)
    try:
        manifest_checks.reject_unknown_keys(data, _KEYS, path)
        manifest_checks.require_keys(data, _KEYS, path)
    except ValueError as exc:
        raise PackSetError(str(exc))
    if data["kind"] != expected_kind:
        raise PackSetError(
            "%s.kind must be %r" % (path, expected_kind)
        )
    if type(data["version"]) is not int or data["version"] != FORMAT_VERSION:
        raise PackSetError(
            "%s.version must be %d" % (path, FORMAT_VERSION)
        )
    return {
        "kind": data["kind"],
        "version": data["version"],
        "packs": validate_names(data["packs"], "%s.packs" % path),
        "shared": validate_names(data["shared"], "%s.shared" % path),
    }


def load_document(path, expected_kind):
    path = os.path.abspath(path)
    return validate_document(_load_json(path), path, expected_kind)


def discover_pack_names(root=None):
    """Every installed top-level pack directory, manifested or not.

    Runtime loaders consume registry and adoption-status inputs from the pack
    tree independently of ``pack.json``. Counting directories is therefore the
    fail-closed distribution boundary: a stale partial pack cannot be invisible
    to an exact profile while remaining visible to a runtime loader.
    """
    root = os.path.abspath(root or packs.packs_root())
    if not os.path.isdir(root):
        return []
    return sorted(
        name for name in os.listdir(root)
        if name != "_shared" and os.path.isdir(os.path.join(root, name))
    )


def discover_shared_names(root=None):
    root = os.path.abspath(root or packs.packs_root())
    shared_root = os.path.join(root, "_shared")
    if not os.path.isdir(shared_root):
        return []
    return sorted(
        name for name in os.listdir(shared_root)
        if os.path.isdir(os.path.join(shared_root, name))
    )


def active_selection(root=None):
    root = os.path.abspath(root or packs.packs_root())
    return {
        "packs": discover_pack_names(root),
        "shared": discover_shared_names(root),
    }


def _delta(expected, actual):
    expected_set = set(expected)
    actual_set = set(actual)
    return {
        "missing": sorted(expected_set - actual_set),
        "extra": sorted(actual_set - expected_set),
    }


def validate_known_selection(selection, catalog, label):
    errors = []
    for key in ("packs", "shared"):
        unknown = sorted(set(selection[key]) - set(catalog[key]))
        if unknown:
            errors.append("unknown %s: %s" % (key, ", ".join(unknown)))
    if errors:
        raise PackSetError("%s is outside the pack catalog; %s" % (
            label, "; ".join(errors)
        ))


def validate_active_pack_set(profile_path, root=None, catalog_path=None):
    profile = load_document(profile_path, PACK_SET_KIND)
    if catalog_path is not None:
        catalog = load_document(catalog_path, PACK_SET_KIND)
        validate_known_selection(profile, catalog, profile_path)
    active = active_selection(root)
    pack_delta = _delta(profile["packs"], active["packs"])
    shared_delta = _delta(profile["shared"], active["shared"])
    errors = []
    for label, delta in (("packs", pack_delta), ("shared", shared_delta)):
        if delta["missing"]:
            errors.append(
                "missing %s: %s" % (label, ", ".join(delta["missing"]))
            )
        if delta["extra"]:
            errors.append(
                "undeclared %s: %s" % (label, ", ".join(delta["extra"]))
            )
    if errors:
        raise PackSetError("pack set mismatch; %s" % "; ".join(errors))
    try:
        packs.validate_shared_dependencies(
            pack_names=profile["packs"], root=root
        )
    except ValueError as exc:
        raise PackSetError(str(exc))
    return {"profile": profile, "active": active}


def check_requirements(requirements_path, root=None, catalog_path=None):
    requirements = load_document(requirements_path, REQUIREMENTS_KIND)
    if catalog_path is not None:
        catalog = load_document(catalog_path, PACK_SET_KIND)
        validate_known_selection(requirements, catalog, requirements_path)
    active = active_selection(root)
    missing = {
        "packs": sorted(set(requirements["packs"]) - set(active["packs"])),
        "shared": sorted(
            set(requirements["shared"]) - set(active["shared"])
        ),
    }
    return {
        "requirements": requirements,
        "active": active,
        "missing": missing,
        "available": not missing["packs"] and not missing["shared"],
    }


def default_profile_path():
    return os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "packsets",
        "full.json",
    )


def _print_selection(prefix, selection):
    sys.stdout.write(
        "%s packs=[%s] shared=[%s]\n"
        % (
            prefix,
            ",".join(selection["packs"]),
            ",".join(selection["shared"]),
        )
    )


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="python -m engine.pack_set",
        description="Validate an exact pack set or example requirements.",
    )
    parser.add_argument(
        "--profile",
        default=os.environ.get("INFRAWRIGHT_PACK_PROFILE")
        or default_profile_path(),
    )
    parser.add_argument("--catalog", default=default_profile_path())
    parser.add_argument("--requirements")
    parser.add_argument("--root", default=None, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    try:
        if args.requirements:
            result = check_requirements(
                args.requirements, root=args.root,
                catalog_path=args.catalog,
            )
            if result["available"]:
                _print_selection("requirements satisfied:", result["active"])
                return 0
            pieces = []
            for label in ("packs", "shared"):
                if result["missing"][label]:
                    pieces.append(
                        "%s=%s"
                        % (label, ",".join(result["missing"][label]))
                    )
            sys.stdout.write("requirements unavailable: %s\n" % " ".join(pieces))
            return 3
        result = validate_active_pack_set(
            args.profile, root=args.root, catalog_path=args.catalog
        )
    except PackSetError as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    _print_selection("validated pack set:", result["active"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
