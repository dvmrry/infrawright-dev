"""Active-pack-aware unittest runner.

The full distribution still executes every discovered test.  Reduced pack
roots omit only tests with an explicit requirement rule, while unmarked tests
remain core tests and continue to run.  A reduced-profile CI job therefore
catches any new undeclared pack coupling.
"""
import argparse
import json
import os
import re
import sys
import unittest

from engine import pack_set
from engine import manifest_checks


REQUIREMENTS_KIND = "infrawright.test-pack-requirements"
FORMAT_VERSION = 1
_DOCUMENT_KEYS = set(["kind", "version", "rules"])
_RULE_KEYS = set(["prefix", "packs", "shared", "reason"])
_PREFIX_RE = re.compile(r"^tests\.test_[A-Za-z0-9_.]+\.$")


class TestRequirementsError(ValueError):
    pass


def _load_json(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (IOError, OSError, ValueError) as exc:
        raise TestRequirementsError("failed to read %s: %s" % (path, exc))


def _names(value, label):
    try:
        return pack_set.validate_names(value, label)
    except pack_set.PackSetError as exc:
        raise TestRequirementsError(str(exc))


def validate_requirements(data, path):
    if not isinstance(data, dict):
        raise TestRequirementsError("%s must contain a JSON object" % path)
    try:
        manifest_checks.reject_unknown_keys(data, _DOCUMENT_KEYS, path)
        manifest_checks.require_keys(data, _DOCUMENT_KEYS, path)
    except ValueError as exc:
        raise TestRequirementsError(str(exc))
    if data["kind"] != REQUIREMENTS_KIND:
        raise TestRequirementsError(
            "%s.kind must be %r" % (path, REQUIREMENTS_KIND)
        )
    if type(data["version"]) is not int or data["version"] != FORMAT_VERSION:
        raise TestRequirementsError(
            "%s.version must be %d" % (path, FORMAT_VERSION)
        )
    if not isinstance(data["rules"], list):
        raise TestRequirementsError("%s.rules must be a list" % path)
    out = []
    prefixes = set()
    for index, rule in enumerate(data["rules"]):
        label = "%s.rules[%d]" % (path, index)
        if not isinstance(rule, dict):
            raise TestRequirementsError("%s must be an object" % label)
        try:
            manifest_checks.reject_unknown_keys(rule, _RULE_KEYS, label)
            manifest_checks.require_keys(rule, _RULE_KEYS, label)
        except ValueError as exc:
            raise TestRequirementsError(str(exc))
        prefix = rule["prefix"]
        if not isinstance(prefix, str) or not _PREFIX_RE.match(prefix):
            raise TestRequirementsError(
                "%s.prefix must be a test id prefix ending in '.'" % label
            )
        if prefix in prefixes:
            raise TestRequirementsError(
                "%s.prefix duplicates %r" % (label, prefix)
            )
        prefixes.add(prefix)
        if not isinstance(rule["reason"], str) or not rule["reason"].strip():
            raise TestRequirementsError(
                "%s.reason must be a non-empty string" % label
            )
        required_packs = _names(rule["packs"], "%s.packs" % label)
        required_shared = _names(rule["shared"], "%s.shared" % label)
        if not required_packs and not required_shared:
            raise TestRequirementsError(
                "%s must require at least one pack or shared component" % label
            )
        out.append({
            "prefix": prefix,
            "packs": required_packs,
            "shared": required_shared,
            "reason": rule["reason"].strip(),
        })
    if [rule["prefix"] for rule in out] != sorted(
            rule["prefix"] for rule in out):
        raise TestRequirementsError("%s.rules must be sorted by prefix" % path)
    return out


def load_requirements(path):
    path = os.path.abspath(path)
    return validate_requirements(_load_json(path), path)


def validate_rule_catalog(rules, catalog, path):
    referenced = {"packs": set(), "shared": set()}
    for rule in rules:
        referenced["packs"].update(rule["packs"])
        referenced["shared"].update(rule["shared"])
    pack_set.validate_known_selection(
        {
            "packs": sorted(referenced["packs"]),
            "shared": sorted(referenced["shared"]),
        },
        catalog,
        path,
    )


def iter_tests(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            for test in iter_tests(item):
                yield test
        else:
            yield item


def requirements_for(test_id, rules):
    required = {"packs": set(), "shared": set(), "rules": []}
    for rule in rules:
        prefix = rule["prefix"]
        if test_id == prefix.rstrip(".") or test_id.startswith(prefix):
            required["packs"].update(rule["packs"])
            required["shared"].update(rule["shared"])
            required["rules"].append(rule["prefix"])
    return {
        "packs": sorted(required["packs"]),
        "shared": sorted(required["shared"]),
        "rules": sorted(required["rules"]),
    }


def _missing_requirements(required, active):
    return {
        "packs": sorted(set(required["packs"]) - set(active["packs"])),
        "shared": sorted(
            set(required["shared"]) - set(active["shared"])
        ),
    }


def discover_test_modules(start_dir, top_level_dir):
    start_dir = os.path.abspath(start_dir)
    top_level_dir = os.path.abspath(top_level_dir)
    out = []
    for dirpath, dirnames, filenames in os.walk(start_dir):
        dirnames[:] = sorted(
            name for name in dirnames
            if name != "__pycache__" and not name.startswith(".")
        )
        for filename in sorted(filenames):
            if not filename.startswith("test") or not filename.endswith(".py"):
                continue
            path = os.path.join(dirpath, filename)
            relpath = os.path.relpath(path, top_level_dir)
            out.append(relpath[:-3].replace(os.sep, "."))
    return sorted(out)


def load_selected_modules(module_names, rules, active, loader=None):
    loader = loader or unittest.defaultTestLoader
    selected = []
    omitted = []
    matched_rules = set()
    for module_name in module_names:
        prefix = module_name + "."
        module_rules = [rule for rule in rules if rule["prefix"] == prefix]
        required = {
            "packs": sorted(set(
                name for rule in module_rules for name in rule["packs"]
            )),
            "shared": sorted(set(
                name for rule in module_rules for name in rule["shared"]
            )),
            "rules": [rule["prefix"] for rule in module_rules],
        }
        missing = _missing_requirements(required, active)
        if missing["packs"] or missing["shared"]:
            omitted.append({
                "module": module_name,
                "missing_packs": missing["packs"],
                "missing_shared": missing["shared"],
            })
            matched_rules.update(required["rules"])
            continue
        selected.append(loader.loadTestsFromName(module_name))
    return {
        "suite": unittest.TestSuite(selected),
        "omitted_modules": omitted,
        "matched_rules": matched_rules,
    }


def select_tests(suite, rules, active, pre_matched_rules=None):
    all_tests = list(iter_tests(suite))
    matched_rules = set(pre_matched_rules or [])
    selected = []
    omitted = []
    active_packs = set(active["packs"])
    active_shared = set(active["shared"])
    for test in all_tests:
        test_id = test.id()
        required = requirements_for(test_id, rules)
        matched_rules.update(required["rules"])
        missing_packs = sorted(set(required["packs"]) - active_packs)
        missing_shared = sorted(set(required["shared"]) - active_shared)
        if missing_packs or missing_shared:
            omitted.append({
                "id": test_id,
                "missing_packs": missing_packs,
                "missing_shared": missing_shared,
            })
        else:
            selected.append(test)
    stale = sorted(
        rule["prefix"] for rule in rules
        if rule["prefix"] not in matched_rules
    )
    if stale:
        raise TestRequirementsError(
            "stale test requirement prefixes: %s" % ", ".join(stale)
        )
    return {
        "suite": unittest.TestSuite(selected),
        "selected": selected,
        "omitted": omitted,
        "total": len(all_tests),
    }


def _default_requirements_path():
    return os.path.join(
        os.path.dirname(os.path.abspath(__file__)),
        "pack-test-requirements.json",
    )


def main(argv=None):
    parser = argparse.ArgumentParser(prog="python -m tests.run")
    parser.add_argument("-v", "--verbose", action="store_true")
    parser.add_argument(
        "--requirements", default=_default_requirements_path()
    )
    parser.add_argument(
        "--catalog", default=pack_set.default_profile_path()
    )
    parser.add_argument("--start-dir", default="tests")
    parser.add_argument("--top-level-dir", default=".")
    args = parser.parse_args(argv)
    try:
        rules = load_requirements(args.requirements)
        catalog = pack_set.load_document(
            args.catalog, pack_set.PACK_SET_KIND
        )
        validate_rule_catalog(rules, catalog, args.requirements)
        active = pack_set.active_selection()
        modules = load_selected_modules(
            discover_test_modules(args.start_dir, args.top_level_dir),
            rules,
            active,
        )
        selection = select_tests(
            modules["suite"], rules, active,
            pre_matched_rules=modules["matched_rules"],
        )
    except (
            ImportError, OSError, TestRequirementsError,
            pack_set.PackSetError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    sys.stdout.write(
        "test pack selection: packs=[%s] shared=[%s] "
        "selected=%d omitted_tests=%d omitted_modules=%d imported_total=%d\n"
        % (
            ",".join(active["packs"]),
            ",".join(active["shared"]),
            len(selection["selected"]),
            len(selection["omitted"]),
            len(modules["omitted_modules"]),
            selection["total"],
        )
    )
    runner = unittest.TextTestRunner(verbosity=2 if args.verbose else 1)
    result = runner.run(selection["suite"])
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
