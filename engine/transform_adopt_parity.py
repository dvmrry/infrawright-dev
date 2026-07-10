"""Credential-free transform/adopt parity diagnostics.

Fixtures pair sanitized raw API items with source-derived or sanitized
post-Read provider state for the same import identities. The diagnostic runs
the real transform pipeline and the real adopt projection pipeline, then
requires every difference to match an exact, value-bound classification.

This module is diagnostic-only. It does not import provider state, change
projection policy, or decide which representation is correct.
"""
import copy
import hashlib
import json
import math
import os
import sys

from engine import adopt
from engine import artifacts
from engine import packs
from engine import transform
from engine.drift_policy import DriftPolicy


REPORT_KIND = "infrawright.transform_adopt_parity"
REPORT_VERSION = 1
FIXTURE_VERSION = 1

_FIXTURE_KEYS = frozenset((
    "fixture_version",
    "name",
    "resource_type",
    "provenance",
    "raw_items",
    "provider_state",
    "expected_differences",
))
_PROVENANCE_KEYS = frozenset((
    "status",
    "provider_version",
    "sources",
    "dependency_sources",
    "local_sources",
    "sanitized",
    "note",
))
_DEPENDENCY_SOURCE_KEYS = frozenset(("name", "version", "url"))
_STATE_KEYS = frozenset(("values", "sensitive_values"))
_EXPECTATION_KEYS = frozenset((
    "path",
    "transform",
    "adopt",
    "classification",
    "disposition",
    "reason",
    "evidence",
))
_SIDE_KEYS = frozenset(("present", "value"))
_PROVENANCE_STATUSES = frozenset(("source_derived", "sanitized_live"))
_CLASSIFICATIONS = frozenset((
    "semantic_mismatch",
    "validation_asymmetry",
    "representational_difference",
    "provider_normalization",
    "other",
))
_DISPOSITIONS = frozenset(("accepted", "evidence_gate"))
_REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class ParityFixtureError(ValueError):
    pass


def _reject_unknown_keys(data, allowed, where):
    unknown = sorted(key for key in data if key not in allowed)
    if unknown:
        raise ParityFixtureError(
            "%s has unknown key %s" % (where, unknown[0])
        )


def _require_keys(data, required, where):
    missing = sorted(key for key in required if key not in data)
    if missing:
        raise ParityFixtureError(
            "%s is missing required key %s" % (where, missing[0])
        )


def _require_non_empty_string(value, where):
    if not isinstance(value, str) or not value:
        raise ParityFixtureError("%s must be a non-empty string" % where)


def _validate_json_value(value, where):
    if value is None or type(value) in (str, bool, int):
        return
    if isinstance(value, float):
        if not math.isfinite(value):
            raise ParityFixtureError("%s contains a non-finite number" % where)
        return
    if isinstance(value, list):
        for index, child in enumerate(value):
            _validate_json_value(child, "%s[%d]" % (where, index))
        return
    if isinstance(value, dict):
        for key, child in value.items():
            if not isinstance(key, str):
                raise ParityFixtureError(
                    "%s contains a non-string object key" % where
                )
            _validate_json_value(child, "%s.%s" % (where, key))
        return
    raise ParityFixtureError(
        "%s contains unsupported JSON value %s"
        % (where, type(value).__name__)
    )


def _validate_side(side, where):
    if not isinstance(side, dict):
        raise ParityFixtureError("%s must be an object" % where)
    _reject_unknown_keys(side, _SIDE_KEYS, where)
    _require_keys(side, frozenset(("present",)), where)
    if type(side["present"]) is not bool:
        raise ParityFixtureError("%s.present must be a boolean" % where)
    if side["present"] and "value" not in side:
        raise ParityFixtureError(
            "%s.value is required when present is true" % where
        )
    if not side["present"] and "value" in side:
        raise ParityFixtureError(
            "%s.value must be absent when present is false" % where
        )
    if "value" in side:
        _validate_json_value(side["value"], "%s.value" % where)


def _validate_provenance(provenance, resource_type, where):
    if not isinstance(provenance, dict):
        raise ParityFixtureError("%s must be an object" % where)
    _reject_unknown_keys(provenance, _PROVENANCE_KEYS, where)
    _require_keys(
        provenance,
        frozenset((
            "status",
            "provider_version",
            "sources",
            "dependency_sources",
            "local_sources",
            "sanitized",
            "note",
        )),
        where,
    )
    if provenance["status"] not in _PROVENANCE_STATUSES:
        raise ParityFixtureError(
            "%s.status must be one of %s"
            % (where, ", ".join(sorted(_PROVENANCE_STATUSES)))
        )
    _require_non_empty_string(
        provenance["provider_version"], "%s.provider_version" % where
    )
    provider = packs.provider_of(resource_type)
    pin = packs.provider_pins().get(provider)
    if pin is None:
        raise ParityFixtureError(
            "%s resource provider %s has no pack pin" % (where, provider)
        )
    if provenance["provider_version"] != pin:
        raise ParityFixtureError(
            "%s.provider_version %s does not match active %s pack pin %s"
            % (where, provenance["provider_version"], provider, pin)
        )
    for field in ("sources", "local_sources"):
        values = provenance[field]
        if not isinstance(values, list) or not values:
            raise ParityFixtureError(
                "%s.%s must be a non-empty list" % (where, field)
            )
        for index, value in enumerate(values):
            _require_non_empty_string(
                value, "%s.%s[%d]" % (where, field, index)
            )
        if len(values) != len(set(values)):
            raise ParityFixtureError(
                "%s.%s must not contain duplicates" % (where, field)
            )
    for index, source in enumerate(provenance["sources"]):
        if not _pinned_github_source(
                source, None, provenance["provider_version"]):
            raise ParityFixtureError(
                "%s.sources[%d] must use a GitHub blob ref pinned to "
                "provider version %s"
                % (
                    where,
                    index,
                    provenance["provider_version"],
                )
            )
    dependencies = provenance["dependency_sources"]
    if not isinstance(dependencies, list):
        raise ParityFixtureError(
            "%s.dependency_sources must be a list" % where
        )
    dependency_urls = set()
    for index, dependency in enumerate(dependencies):
        label = "%s.dependency_sources[%d]" % (where, index)
        if not isinstance(dependency, dict):
            raise ParityFixtureError("%s must be an object" % label)
        _reject_unknown_keys(dependency, _DEPENDENCY_SOURCE_KEYS, label)
        _require_keys(dependency, _DEPENDENCY_SOURCE_KEYS, label)
        for field in ("name", "version", "url"):
            _require_non_empty_string(
                dependency[field], "%s.%s" % (label, field)
            )
        if not _pinned_github_source(
                dependency["url"], dependency["name"], dependency["version"]):
            raise ParityFixtureError(
                "%s.url must reference %s at version %s"
                % (label, dependency["name"], dependency["version"])
            )
        if dependency["url"] in dependency_urls:
            raise ParityFixtureError(
                "%s.dependency_sources must not contain duplicate URLs"
                % where
            )
        dependency_urls.add(dependency["url"])
    for index, source in enumerate(provenance["local_sources"]):
        normalized = os.path.normpath(source)
        if (os.path.isabs(source) or normalized == os.pardir
                or normalized.startswith(os.pardir + os.sep)):
            raise ParityFixtureError(
                "%s.local_sources[%d] must stay within the repository"
                % (where, index)
            )
        if not os.path.isfile(os.path.join(_REPO_ROOT, normalized)):
            raise ParityFixtureError(
                "%s.local_sources[%d] does not exist: %s"
                % (where, index, source)
            )
    if provenance["sanitized"] is not True:
        raise ParityFixtureError(
            "%s.sanitized must be true; live/private state is not accepted"
            % where
        )
    _require_non_empty_string(provenance["note"], "%s.note" % where)


def _pinned_github_source(url, repository, version):
    github = "https://github.com/"
    if not url.startswith(github):
        return False
    remainder = url[len(github):]
    marker = "/blob/"
    if marker not in remainder:
        return False
    actual_repository, remainder = remainder.split(marker, 1)
    if not actual_repository or "/" not in remainder:
        return False
    if repository is not None and actual_repository != repository:
        return False
    ref, source_path = remainder.split("/", 1)
    return (
        ref in (version, "v%s" % version)
        and bool(source_path)
        and not source_path.startswith("#")
    )


def _validate_expectations(expectations, allowed_evidence, where):
    if not isinstance(expectations, list):
        raise ParityFixtureError("%s must be a list" % where)
    seen_paths = set()
    for index, entry in enumerate(expectations):
        label = "%s[%d]" % (where, index)
        if not isinstance(entry, dict):
            raise ParityFixtureError("%s must be an object" % label)
        _reject_unknown_keys(entry, _EXPECTATION_KEYS, label)
        _require_keys(entry, _EXPECTATION_KEYS, label)
        path = entry["path"]
        if not isinstance(path, str) or (path and not path.startswith("/")):
            raise ParityFixtureError(
                "%s.path must be an RFC 6901 JSON pointer" % label
            )
        if path in seen_paths:
            raise ParityFixtureError(
                "%s contains duplicate path %s" % (where, path)
            )
        seen_paths.add(path)
        _validate_side(entry["transform"], "%s.transform" % label)
        _validate_side(entry["adopt"], "%s.adopt" % label)
        if entry["classification"] not in _CLASSIFICATIONS:
            raise ParityFixtureError(
                "%s.classification must be one of %s"
                % (label, ", ".join(sorted(_CLASSIFICATIONS)))
            )
        if entry["disposition"] not in _DISPOSITIONS:
            raise ParityFixtureError(
                "%s.disposition must be one of %s"
                % (label, ", ".join(sorted(_DISPOSITIONS)))
            )
        _require_non_empty_string(entry["reason"], "%s.reason" % label)
        evidence = entry["evidence"]
        if not isinstance(evidence, list) or not evidence:
            raise ParityFixtureError(
                "%s.evidence must be a non-empty list" % label
            )
        for evidence_index, source in enumerate(evidence):
            _require_non_empty_string(
                source,
                "%s.evidence[%d]" % (label, evidence_index),
            )
            if source not in allowed_evidence:
                raise ParityFixtureError(
                    "%s.evidence[%d] is not declared by fixture provenance"
                    % (label, evidence_index)
                )
        if len(evidence) != len(set(evidence)):
            raise ParityFixtureError(
                "%s.evidence must not contain duplicates" % label
            )


def validate_fixture(data, where="parity fixture"):
    if not isinstance(data, dict):
        raise ParityFixtureError("%s must contain an object" % where)
    _reject_unknown_keys(data, _FIXTURE_KEYS, where)
    _require_keys(data, _FIXTURE_KEYS, where)
    if (type(data["fixture_version"]) is not int
            or data["fixture_version"] != FIXTURE_VERSION):
        raise ParityFixtureError(
            "%s has unsupported fixture_version %r"
            % (where, data["fixture_version"])
        )
    _require_non_empty_string(data["name"], "%s.name" % where)
    _require_non_empty_string(
        data["resource_type"], "%s.resource_type" % where
    )
    try:
        artifacts.validate_resource_type(data["resource_type"])
    except ValueError as exc:
        raise ParityFixtureError(str(exc))
    _validate_provenance(
        data["provenance"],
        data["resource_type"],
        "%s.provenance" % where,
    )
    raw_items = data["raw_items"]
    if not isinstance(raw_items, list) or not raw_items:
        raise ParityFixtureError("%s.raw_items must be a non-empty list" % where)
    for index, raw in enumerate(raw_items):
        if not isinstance(raw, dict):
            raise ParityFixtureError(
                "%s.raw_items[%d] must be an object" % (where, index)
            )
        _validate_json_value(raw, "%s.raw_items[%d]" % (where, index))
    provider_state = data["provider_state"]
    if not isinstance(provider_state, dict) or not provider_state:
        raise ParityFixtureError(
            "%s.provider_state must be a non-empty object" % where
        )
    for import_id, state in provider_state.items():
        _require_non_empty_string(
            import_id, "%s.provider_state key" % where
        )
        label = "%s.provider_state.%s" % (where, import_id)
        if not isinstance(state, dict):
            raise ParityFixtureError("%s must be an object" % label)
        _reject_unknown_keys(state, _STATE_KEYS, label)
        _require_keys(state, frozenset(("values",)), label)
        if not isinstance(state["values"], dict):
            raise ParityFixtureError("%s.values must be an object" % label)
        _validate_json_value(state["values"], "%s.values" % label)
        if "sensitive_values" in state:
            _validate_json_value(
                state["sensitive_values"], "%s.sensitive_values" % label
            )
    _validate_expectations(
        data["expected_differences"],
        set(data["provenance"]["sources"])
        | set(
            entry["url"]
            for entry in data["provenance"]["dependency_sources"]
        )
        | set(data["provenance"]["local_sources"]),
        "%s.expected_differences" % where,
    )
    return data


def load_fixture(path):
    try:
        with open(path, encoding="utf-8") as handle:
            data = json.load(handle, parse_constant=_reject_json_constant)
    except ValueError as exc:
        if isinstance(exc, ParityFixtureError):
            raise
        raise ParityFixtureError("%s is not valid JSON: %s" % (path, exc))
    return validate_fixture(data, where=path)


def _reject_json_constant(value):
    raise ParityFixtureError("non-standard JSON constant %s is not allowed" % value)


def _fixture_state_loader(provider_state):
    def load(resource_type, key_to_import_id, policy=None, raw_items=None):
        del resource_type, policy, raw_items
        requested = set(str(value) for value in key_to_import_id.values())
        available = set(provider_state)
        missing = sorted(requested - available)
        extra = sorted(available - requested)
        if missing:
            raise ParityFixtureError(
                "provider_state is missing import id %s" % missing[0]
            )
        if extra:
            raise ParityFixtureError(
                "provider_state has unreferenced import id %s" % extra[0]
            )
        return dict(
            (key, provider_state[str(import_id)])
            for key, import_id in key_to_import_id.items()
        )
    return load


def _pointer_segment(value):
    return str(value).replace("~", "~0").replace("/", "~1")


def _json_pointer(path):
    if not path:
        return ""
    return "/" + "/".join(_pointer_segment(value) for value in path)


def _side(present, value=None):
    out = {"present": bool(present)}
    if present:
        out["value"] = value
    return out


def _json_differences(left, right, path=()):
    if type(left) is not type(right):
        return [{
            "path": _json_pointer(path),
            "transform": _side(True, left),
            "adopt": _side(True, right),
        }]
    if isinstance(left, dict):
        out = []
        for key in sorted(set(left) | set(right)):
            if key not in left:
                out.append({
                    "path": _json_pointer(path + (key,)),
                    "transform": _side(False),
                    "adopt": _side(True, right[key]),
                })
            elif key not in right:
                out.append({
                    "path": _json_pointer(path + (key,)),
                    "transform": _side(True, left[key]),
                    "adopt": _side(False),
                })
            else:
                out.extend(_json_differences(
                    left[key], right[key], path + (key,)
                ))
        return out
    if isinstance(left, list):
        out = []
        limit = max(len(left), len(right))
        for index in range(limit):
            if index >= len(left):
                out.append({
                    "path": _json_pointer(path + (index,)),
                    "transform": _side(False),
                    "adopt": _side(True, right[index]),
                })
            elif index >= len(right):
                out.append({
                    "path": _json_pointer(path + (index,)),
                    "transform": _side(True, left[index]),
                    "adopt": _side(False),
                })
            else:
                out.extend(_json_differences(
                    left[index], right[index], path + (index,)
                ))
        return out
    if _canonical_json(left) != _canonical_json(right):
        return [{
            "path": _json_pointer(path),
            "transform": _side(True, left),
            "adopt": _side(True, right),
        }]
    return []


def _canonical_json(value):
    return json.dumps(
        value,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    )


def _pointer_tokens(pointer):
    if pointer == "":
        return []
    if not isinstance(pointer, str) or not pointer.startswith("/"):
        raise ParityFixtureError("difference path is not a JSON pointer")
    return [
        token.replace("~1", "/").replace("~0", "~")
        for token in pointer[1:].split("/")
    ]


def _pointer_parent(root, tokens):
    current = root
    for token in tokens[:-1]:
        if isinstance(current, dict):
            if token not in current:
                raise ParityFixtureError(
                    "difference path parent %s is missing" % token
                )
            current = current[token]
        elif isinstance(current, list):
            try:
                index = int(token)
            except ValueError:
                raise ParityFixtureError(
                    "difference path list index %s is invalid" % token
                )
            if index < 0 or index >= len(current):
                raise ParityFixtureError(
                    "difference path list index %s is out of range" % token
                )
            current = current[index]
        else:
            raise ParityFixtureError(
                "difference path traverses a scalar at %s" % token
            )
    return current, tokens[-1]


def _set_pointer(root, pointer, value):
    tokens = _pointer_tokens(pointer)
    if not tokens:
        return copy.deepcopy(value)
    parent, token = _pointer_parent(root, tokens)
    if isinstance(parent, dict):
        parent[token] = copy.deepcopy(value)
        return root
    if isinstance(parent, list):
        try:
            index = int(token)
        except ValueError:
            raise ParityFixtureError(
                "difference path list index %s is invalid" % token
            )
        if index == len(parent):
            parent.append(copy.deepcopy(value))
        elif 0 <= index < len(parent):
            parent[index] = copy.deepcopy(value)
        else:
            raise ParityFixtureError(
                "difference path list index %s is out of range" % token
            )
        return root
    raise ParityFixtureError(
        "difference path parent is not a container at %s" % pointer
    )


def _delete_pointer(root, pointer):
    tokens = _pointer_tokens(pointer)
    if not tokens:
        raise ParityFixtureError("difference cannot delete the report root")
    parent, token = _pointer_parent(root, tokens)
    if isinstance(parent, dict):
        if token not in parent:
            raise ParityFixtureError(
                "difference delete path %s is missing" % pointer
            )
        del parent[token]
        return root
    if isinstance(parent, list):
        try:
            index = int(token)
        except ValueError:
            raise ParityFixtureError(
                "difference path list index %s is invalid" % token
            )
        if index < 0 or index >= len(parent):
            raise ParityFixtureError(
                "difference delete index %s is out of range" % token
            )
        del parent[index]
        return root
    raise ParityFixtureError(
        "difference delete parent is not a container at %s" % pointer
    )


def _apply_reported_differences(transform_payload, differences):
    reconstructed = copy.deepcopy(transform_payload)
    for entry in differences:
        if entry["adopt"]["present"]:
            reconstructed = _set_pointer(
                reconstructed,
                entry["path"],
                entry["adopt"]["value"],
            )
    for entry in reversed(differences):
        if not entry["adopt"]["present"]:
            reconstructed = _delete_pointer(
                reconstructed, entry["path"]
            )
    return reconstructed


def _difference_key(entry):
    return json.dumps(
        {
            "path": entry["path"],
            "transform": entry["transform"],
            "adopt": entry["adopt"],
        },
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    )


def _render_sha256(items):
    rendered = transform.render_tfvars(items)
    return rendered, hashlib.sha256(rendered.encode("utf-8")).hexdigest()


def compare_fixture(data):
    validate_fixture(data)
    resource_type = data["resource_type"]
    raw_items = data["raw_items"]
    transform_items, _originals, drops = transform.transform_items(
        raw_items,
        resource_type,
        transform.load_override(resource_type),
    )
    policy = DriftPolicy.load_for_adoption(None)
    adopt_items, _identities = adopt.adopt_items(
        raw_items,
        resource_type,
        policy=policy,
        state_loader=_fixture_state_loader(data["provider_state"]),
    )

    transform_rendered, transform_sha = _render_sha256(transform_items)
    adopt_rendered, adopt_sha = _render_sha256(adopt_items)
    byte_equal = transform_rendered == adopt_rendered
    transform_payload = {"items": transform_items}
    adopt_payload = {"items": adopt_items}
    actual = _json_differences(transform_payload, adopt_payload)
    reconstructed = _apply_reported_differences(transform_payload, actual)
    reconstructed_rendered = transform.render_tfvars(reconstructed["items"])
    unaccounted_byte_difference = (
        reconstructed_rendered != adopt_rendered
    )
    expected = dict(
        (_difference_key(entry), entry)
        for entry in data["expected_differences"]
    )
    differences = []
    for entry in actual:
        key = _difference_key(entry)
        classification = expected.pop(key, None)
        out = dict(entry)
        if classification is None:
            out["status"] = "unclassified"
        else:
            out["status"] = "classified"
            for field in (
                    "classification", "disposition", "reason", "evidence"):
                out[field] = classification[field]
        differences.append(out)
    stale = sorted(
        expected.values(),
        key=lambda entry: (
            entry["path"],
            json.dumps(entry, sort_keys=True, allow_nan=False),
        ),
    )
    unclassified = len([
        entry for entry in differences if entry["status"] == "unclassified"
    ])
    classified = len(differences) - unclassified
    evidence_gates = len([
        entry for entry in differences
        if entry.get("disposition") == "evidence_gate"
    ])
    accepted = len([
        entry for entry in differences
        if entry.get("disposition") == "accepted"
    ])
    if unclassified or stale or drops or unaccounted_byte_difference:
        result = "review_required"
    elif evidence_gates:
        result = "evidence_gates"
    elif differences:
        result = "classified_differences"
    else:
        result = "equal"
    return {
        "name": data["name"],
        "resource_type": resource_type,
        "provenance": data["provenance"],
        "result": result,
        "outputs": {
            "byte_equal": byte_equal,
            "unaccounted_byte_difference": unaccounted_byte_difference,
            "transform_sha256": transform_sha,
            "adopt_sha256": adopt_sha,
        },
        "differences": differences,
        "stale_expectations": stale,
        "transform_unacknowledged_drops": sorted(drops),
        "summary": {
            "differences": len(differences),
            "classified": classified,
            "unclassified": unclassified,
            "evidence_gates": evidence_gates,
            "accepted": accepted,
            "stale_expectations": len(stale),
            "unacknowledged_drops": len(drops),
            "unaccounted_byte_differences": (
                1 if unaccounted_byte_difference else 0
            ),
        },
    }


def build_report(fixtures):
    names = set()
    results = []
    for fixture in fixtures:
        validate_fixture(fixture)
        if fixture["name"] in names:
            raise ParityFixtureError(
                "duplicate fixture name %s" % fixture["name"]
            )
        names.add(fixture["name"])
        results.append(compare_fixture(fixture))
    results.sort(key=lambda entry: entry["name"])
    summary = {
        "fixtures": len(results),
        "equal": len([entry for entry in results if entry["result"] == "equal"]),
        "classified_differences": len([
            entry for entry in results
            if entry["result"] == "classified_differences"
        ]),
        "evidence_gate_fixtures": len([
            entry for entry in results
            if entry["result"] == "evidence_gates"
        ]),
        "review_required": len([
            entry for entry in results
            if entry["result"] == "review_required"
        ]),
        "differences": sum(entry["summary"]["differences"] for entry in results),
        "classified": sum(entry["summary"]["classified"] for entry in results),
        "unclassified": sum(entry["summary"]["unclassified"] for entry in results),
        "evidence_gates": sum(entry["summary"]["evidence_gates"] for entry in results),
        "accepted": sum(entry["summary"]["accepted"] for entry in results),
        "stale_expectations": sum(
            entry["summary"]["stale_expectations"] for entry in results
        ),
        "unacknowledged_drops": sum(
            entry["summary"]["unacknowledged_drops"] for entry in results
        ),
        "unaccounted_byte_differences": sum(
            entry["summary"]["unaccounted_byte_differences"]
            for entry in results
        ),
    }
    return {
        "kind": REPORT_KIND,
        "report_version": REPORT_VERSION,
        "result": (
            "review_required" if summary["review_required"] else
            "evidence_gates" if summary["evidence_gate_fixtures"] else
            "classified_differences" if summary["classified_differences"] else
            "equal"
        ),
        "summary": summary,
        "fixtures": results,
    }


def render_report(report):
    return json.dumps(
        report, indent=2, sort_keys=True, allow_nan=False
    ) + "\n"


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    if not argv or any(arg.startswith("-") for arg in argv):
        sys.stderr.write(
            "usage: python -m engine.transform_adopt_parity "
            "<fixture.json> [<fixture.json> ...]\n"
        )
        return 2
    try:
        report = build_report([load_fixture(path) for path in argv])
    except (OSError, ValueError, KeyError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    sys.stdout.write(render_report(report))
    return 1 if report["result"] in (
        "review_required", "evidence_gates"
    ) else 0


if __name__ == "__main__":
    sys.exit(main())
