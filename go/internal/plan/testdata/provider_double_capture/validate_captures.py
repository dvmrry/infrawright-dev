#!/usr/bin/env python3
"""Exact semantic validation for the provider-double capture set.

Shared between gen-captures.sh (pre-promotion staged validation, and the
post-recovery check of --recover) and the focused Go regressions, so the
script and the test suite cannot drift on what a coherent capture set
means. Every expectation below is deterministic: the provider double
derives IDs from INFRAWRIGHT_CAPTURE_ID_VERSION plus the requested name,
so regeneration changes only the single top-level timestamp per file.

No `assert` statements: every check is an unconditional test with a
nonzero exit, immune to PYTHONOPTIMIZE.

ROLE BOUNDARY: this validator is a pre-promotion sanity layer for the
fixture set, not a reimplementation of the assessment authorization
contract. The authoritative contract is go/internal/plan/contract.go
and its focused Go regressions (which consume these same fixtures); a
capture accepted here but refused by the contract tests fails the Go
suite, which is the promotion gate of record. Extending this file
toward full contract parity is deliberately out of scope.
"""
import json
import re
import sys

QUALIFIED_TERRAFORM_VERSION = "1.15.4"
FORMAT_VERSION = "1.2"
PROVIDER = "registry.terraform.io/infrawright/capture"
CHECK_ADDRESS = "module.capture_item.data.capture_item.items"
V1 = {"group_one": "8a3fc945636370e1", "group_two": "59befd796acedd57"}
V2 = {"group_one": "018da47922f5094d"}
NAMES = {"group_one": "Location Group", "group_two": "Another Location Group"}

# Per-scenario exact contract: output action, before/after maps, and the
# prior-state data instances (index -> id) the scenario must observe.
SCENARIOS = {
    "initial_create": {
        "actions": ["create"],
        "before": None,
        "after": {"group_one": V1["group_one"], "group_two": V1["group_two"]},
        "instances": {"group_one": V1["group_one"], "group_two": V1["group_two"]},
    },
    "no_op": {
        "actions": ["no-op"],
        "before": {"group_one": V1["group_one"]},
        "after": {"group_one": V1["group_one"]},
        "instances": {"group_one": V1["group_one"]},
    },
    "refresh_id_change": {
        "actions": ["update"],
        "before": {"group_one": V1["group_one"]},
        "after": {"group_one": V2["group_one"]},
        "instances": {"group_one": V2["group_one"]},
    },
    "rekey_refusal": {
        "actions": ["update"],
        "before": {"group_one": V1["group_one"]},
        "after": {"changed-group_one": V1["group_one"]},
        "instances": {"group_one": V1["group_one"]},
    },
    "empty_for_each": {
        "actions": ["create"],
        "before": None,
        "after": {},
        "instances": {},
    },
    "refresh_false": {
        "actions": ["update"],
        "before": {"group_one": V1["group_one"]},
        "after": {"group_one": V2["group_one"]},
        "instances": {"group_one": V2["group_one"]},
    },
    "refresh_true": {
        "actions": ["update"],
        "before": {"group_one": V1["group_one"]},
        "after": {"group_one": V2["group_one"]},
        "instances": {"group_one": V2["group_one"]},
    },
}


def fail(message):
    print("capture validation failed: " + message, file=sys.stderr)
    sys.exit(1)


def load(root, scenario):
    path = root + "/" + scenario + "/show.json"
    try:
        with open(path) as handle:
            raw = handle.read()
        return json.loads(raw), raw
    except Exception as error:  # noqa: BLE001 - report and refuse
        fail("%s: %s" % (path, error))


def data_instances(show, scenario):
    out = {}
    prior = show.get("prior_state") or {}
    values = prior.get("values") or {}
    root = values.get("root_module") or {}
    for child in root.get("child_modules") or []:
        for resource in child.get("resources") or []:
            if resource.get("mode") != "data":
                continue
            index = resource.get("index")
            expected_address = 'module.capture_item.data.capture_item.items["%s"]' % index
            if resource.get("address") != expected_address:
                fail("%s data resource address %r is not canonical" % (scenario, resource.get("address")))
            if resource.get("type") != "capture_item" or resource.get("name") != "items":
                fail("%s data resource type/name %r/%r are not the provider double's" % (scenario, resource.get("type"), resource.get("name")))
            if resource.get("provider_name") != PROVIDER:
                fail("%s data resource provider %r is not the provider double" % (scenario, resource.get("provider_name")))
            if resource.get("schema_version") != 0:
                fail("%s data resource schema_version %r is not 0" % (scenario, resource.get("schema_version")))
            values_map = resource.get("values") or {}
            if values_map.get("name") != NAMES.get(index):
                fail("%s instance %s name %r does not match the requested name %r" % (scenario, index, values_map.get("name"), NAMES.get(index)))
            out[index] = values_map.get("id")
    return out


def output_change(show):
    return (show.get("output_changes") or {}).get("iw_reference_ids") or {}


def wrap(mapping):
    return None if mapping is None else {"capture_item": mapping}


def planned_data_resource_count(show):
    planned = (show.get("planned_values") or {}).get("root_module") or {}
    count = len(planned.get("resources") or [])
    for child in planned.get("child_modules") or []:
        count += len(child.get("resources") or [])
    return count


def non_noop_resource_changes(show):
    count = 0
    for change in show.get("resource_changes") or []:
        actions = (change.get("change") or {}).get("actions")
        if actions != ["no-op"]:
            count += 1
    return count


def validate_scenario(scenario, expected, show):
    if show.get("terraform_version") != QUALIFIED_TERRAFORM_VERSION:
        fail("%s terraform_version %r is not %s" % (scenario, show.get("terraform_version"), QUALIFIED_TERRAFORM_VERSION))
    if show.get("format_version") != FORMAT_VERSION:
        fail("%s format_version %r is not %s" % (scenario, show.get("format_version"), FORMAT_VERSION))
    if show.get("complete") is not True:
        fail("%s is not complete" % scenario)
    if show.get("errored") is not False:
        fail("%s errored" % scenario)

    checks = show.get("checks") or []
    if len(checks) != 1:
        fail("%s must carry exactly the data-module postcondition check, found %d" % (scenario, len(checks)))
    check = checks[0]
    if ((check.get("address") or {}).get("to_display")) != CHECK_ADDRESS:
        fail("%s check address %r is not the postcondition" % (scenario, (check.get("address") or {}).get("to_display")))
    if check.get("status") != "pass":
        fail("%s check status %r is not pass" % (scenario, check.get("status")))
    instances = check.get("instances") or []
    if len(instances) != len(expected["instances"]):
        fail("%s check instances = %d, want %d" % (scenario, len(instances), len(expected["instances"])))
    for instance in instances:
        if instance.get("status") != "pass":
            fail("%s check instance status %r is not pass" % (scenario, instance.get("status")))

    observed = data_instances(show, scenario)
    if observed != expected["instances"]:
        fail("%s prior-state instances %r do not match the deterministic contract %r" % (scenario, observed, expected["instances"]))

    change = output_change(show)
    if change.get("actions") != expected["actions"]:
        fail("%s output actions %r != %r" % (scenario, change.get("actions"), expected["actions"]))
    if change.get("before") != wrap(expected["before"]):
        fail("%s output before %r != %r" % (scenario, change.get("before"), wrap(expected["before"])))
    if change.get("after") != wrap(expected["after"]):
        fail("%s output after %r != %r" % (scenario, change.get("after"), wrap(expected["after"])))

    planned_outputs = ((show.get("planned_values") or {}).get("outputs") or {}).get("iw_reference_ids") or {}
    if planned_outputs.get("value") != wrap(expected["after"]):
        fail("%s planned output value %r contradicts the output change" % (scenario, planned_outputs.get("value")))

    if planned_data_resource_count(show) != 0:
        fail("%s carries planned data resources; Terraform 1.15.4 places plan-time reads in prior_state" % scenario)
    if non_noop_resource_changes(show) != 0:
        fail("%s carries non-no-op resource changes" % scenario)


def normalize_timestamp(raw, scenario):
    matches = re.findall(r'"timestamp":"[^"]*"', raw)
    if len(matches) != 1:
        fail("%s must carry exactly one timestamp, found %d" % (scenario, len(matches)))
    return re.sub(r'"timestamp":"[^"]*"', '"timestamp":"<t>"', raw)


def main():
    if len(sys.argv) != 2:
        fail("usage: validate_captures.py <capture-root>")
    root = sys.argv[1]
    raws = {}
    for scenario, expected in SCENARIOS.items():
        show, raws[scenario] = load(root, scenario)
        validate_scenario(scenario, expected, show)
    if normalize_timestamp(raws["refresh_false"], "refresh_false") != normalize_timestamp(raws["refresh_true"], "refresh_true"):
        fail("refresh pair differs beyond the timestamp")
    print("capture set valid")


if __name__ == "__main__":
    main()
