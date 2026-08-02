#!/usr/bin/env python3
"""Semantic validation for the provider-double capture set.

Shared between gen-captures.sh (pre-promotion staged validation) and the
focused Go regression, so the script and the test suite cannot drift on
what a coherent capture set means. No `assert` statements: every check is
an unconditional test with a nonzero exit, immune to PYTHONOPTIMIZE.
"""
import json
import re
import sys

QUALIFIED_TERRAFORM_VERSION = "1.15.4"
FORMAT_VERSION = "1.2"
SCENARIOS = [
    "initial_create",
    "no_op",
    "refresh_id_change",
    "rekey_refusal",
    "empty_for_each",
    "refresh_false",
    "refresh_true",
]


def fail(message):
    print("capture validation failed: " + message, file=sys.stderr)
    sys.exit(1)


def load(root, scenario):
    path = root + "/" + scenario + "/show.json"
    try:
        with open(path) as handle:
            return json.load(handle), open(path).read()
    except Exception as error:  # noqa: BLE001 - report and refuse
        fail("%s: %s" % (path, error))


def data_instances(show):
    out = {}
    prior = show.get("prior_state") or {}
    values = prior.get("values") or {}
    root = values.get("root_module") or {}
    for child in root.get("child_modules") or []:
        for resource in child.get("resources") or []:
            if resource.get("mode") == "data":
                out[resource.get("index")] = (resource.get("values") or {}).get("id")
    return out


def output_change(show):
    return (show.get("output_changes") or {}).get("iw_reference_ids") or {}


def common(scenario, show):
    if show.get("terraform_version") != QUALIFIED_TERRAFORM_VERSION:
        fail("%s terraform_version %r is not %s" % (scenario, show.get("terraform_version"), QUALIFIED_TERRAFORM_VERSION))
    if show.get("format_version") != FORMAT_VERSION:
        fail("%s format_version %r is not %s" % (scenario, show.get("format_version"), FORMAT_VERSION))
    if show.get("complete") is not True:
        fail("%s is not complete" % scenario)
    if show.get("errored") is not False:
        fail("%s errored" % scenario)
    for check in show.get("checks") or []:
        if check.get("status") != "pass":
            fail("%s carries a non-passing lifecycle check" % scenario)
        for instance in check.get("instances") or []:
            if instance.get("status") != "pass":
                fail("%s carries a non-passing check instance" % scenario)


def after_map(show):
    after = output_change(show).get("after") or {}
    return (after.get("capture_item") or {}) if isinstance(after, dict) else {}


def normalize_timestamp(raw, scenario):
    matches = re.findall(r'"timestamp":"[^"]*"', raw)
    if len(matches) != 1:
        fail("%s must carry exactly one timestamp, found %d" % (scenario, len(matches)))
    return re.sub(r'"timestamp":"[^"]*"', '"timestamp":"<t>"', raw)


def main():
    if len(sys.argv) != 2:
        fail("usage: validate_captures.py <capture-root>")
    root = sys.argv[1]
    shows, raws = {}, {}
    for scenario in SCENARIOS:
        shows[scenario], raws[scenario] = load(root, scenario)
        common(scenario, shows[scenario])

    if sorted(data_instances(shows["initial_create"])) != ["group_one", "group_two"]:
        fail("initial_create must observe exactly group_one and group_two")
    if sorted(after_map(shows["initial_create"])) != ["group_one", "group_two"]:
        fail("initial_create output must carry both item keys")
    if output_change(shows["no_op"]).get("actions") != ["no-op"]:
        fail("no_op output action must be no-op")
    if output_change(shows["refresh_id_change"]).get("actions") != ["update"]:
        fail("refresh_id_change output action must be update")
    if data_instances(shows["empty_for_each"]) != {}:
        fail("empty_for_each must observe no data instances")
    rekey_keys = list(after_map(shows["rekey_refusal"]))
    if not rekey_keys or not all(key.startswith("changed-") for key in rekey_keys):
        fail("rekey_refusal output keys must carry the changed- prefix")
    for scenario in ("refresh_false", "refresh_true"):
        if output_change(shows[scenario]).get("actions") != ["update"]:
            fail("%s output action must be update" % scenario)
        instances = data_instances(shows[scenario])
        if after_map(shows[scenario]) != {key: value for key, value in instances.items()}:
            fail("%s output must equal its provider-observed instance IDs" % scenario)
    if normalize_timestamp(raws["refresh_false"], "refresh_false") != normalize_timestamp(raws["refresh_true"], "refresh_true"):
        fail("refresh pair differs beyond the timestamp")
    print("capture set valid")


if __name__ == "__main__":
    main()
