#!/usr/bin/env python3
"""Audit the committed ZPA v4.4.6 provider-evidence matrix.

The matrix owns the curated semantic claims.  This compact tool does not parse
Go.  It verifies the exact upstream git commit, complete source-file hashes,
and inclusive source-range hashes, while independently deriving the fetch set
and state-shape summaries from committed pack data.
"""
from __future__ import print_function

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if REPO_ROOT not in sys.path:
    sys.path.insert(0, REPO_ROOT)

from engine.adoption_meta import adoption_entry  # noqa: E402
from engine.registry import load_registry  # noqa: E402
from engine.tfschema import (  # noqa: E402
    attr_type,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


REPORT_KIND = "infrawright.zpa_provider_evidence"
REPORT_VERSION = 1
PROVIDER_REF = "v4.4.6"
PROVIDER_COMMIT = "dcf12469a9a8f648be0691c74e9816fc94ec7ddc"
PROVIDER_REPOSITORY = "https://github.com/zscaler/terraform-provider-zpa"
RUNTIME_GATE = "terraform_runtime_evidence_required"
MATRIX = os.path.join(REPO_ROOT, "docs", "evidence", "zpa-provider-v4.4.6.json")

IMPORT_MODES = set(["numeric_or_alternate_lookup", "passthrough"])
NUMERIC_IMPORT_GRAMMARS = set([
    "base10_numeric_id_or_email_id",
    "base10_numeric_id_or_name",
    "base10_numeric_id_or_policy_name",
])
READ_IDENTITIES = set([
    "current_id_lookup_with_response_schema_id",
    "current_id_lookup_without_response_rebind",
    "current_id_matched_in_list",
    "response_id",
    "response_user_id",
])
SCHEMA_ID_SOURCES = set([
    "importer_seeded",
    "not_source_populated",
    "read_response_id",
])
REPORT_KEYS = set([
    "kind", "local_inputs", "provider", "resources", "schema_version",
    "summary",
])
RESOURCE_KEYS = set([
    "exceptions", "fetch", "generated_config", "import", "read_identity",
    "resource_type", "source_evidence", "state_shape",
])
FETCH_KEYS = set(["optional_http_statuses", "pagination", "path"])
IMPORT_KEYS = set([
    "alternate_lookup", "engine_import_id_template", "grammar", "mode",
    "numeric_exactness_requirement",
])
READ_IDENTITY_KEYS = set(["schema_id_attribute", "terraform_instance_id"])
STATE_SHAPE_KEYS = set([
    "attribute_encodings", "block_nesting_modes", "counts",
    "required_input_paths", "sensitive_input_paths", "shape_sha256",
])
NUMERIC_REQUIREMENT = (
    "raw id must be accepted by Go strconv.ParseInt(id, 10, 64) (signed "
    "64-bit, explicit base 10); otherwise the provider treats it as the "
    "alternate lookup key"
)


class EvidenceError(ValueError):
    pass


def _sha256(value):
    return hashlib.sha256(value).hexdigest()


def _read_bytes(path):
    with open(path, "rb") as f:
        return f.read()


def _load_json(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (IOError, OSError, ValueError) as exc:
        raise EvidenceError("failed to read %s: %s" % (path, exc))


def _reject_keys(value, expected, label):
    if not isinstance(value, dict):
        raise EvidenceError("%s must contain an object" % label)
    missing = sorted(expected - set(value))
    unknown = sorted(set(value) - expected)
    if missing or unknown:
        raise EvidenceError(
            "%s keys differ (missing=%s unknown=%s)"
            % (label, missing, unknown)
        )


def _encoding(value):
    if isinstance(value, str):
        return value
    if not isinstance(value, list) or len(value) != 2:
        return json.dumps(value, sort_keys=True, separators=(",", ":"))
    kind, inner = value
    if kind == "object" and isinstance(inner, dict):
        return "object({%s})" % ",".join(
            "%s:%s" % (name, _encoding(inner[name]))
            for name in sorted(inner)
        )
    return "%s(%s)" % (kind, _encoding(inner))


def _state_shape(resource_type):
    counts = {
        "input_attributes": 0,
        "input_blocks": 0,
        "computed_only_attributes": 0,
        "computed_only_blocks": 0,
    }
    encodings = {}
    modes = {}
    required = []
    sensitive = []

    def walk(block, prefix, top=False):
        classified = resource_input_attrs(block) if top else classify_attributes(block)
        attributes = block.get("attributes") or {}
        counts["computed_only_attributes"] += len(classified["computed_only"])
        for status in ("required", "optional"):
            for name in classified[status]:
                path = prefix + name
                counts["input_attributes"] += 1
                encoded = _encoding(attr_type(attributes[name]))
                encodings[encoded] = encodings.get(encoded, 0) + 1
                if status == "required":
                    required.append(path)
                if attributes[name].get("sensitive"):
                    sensitive.append(path)
        blocks = input_block_types(block)
        all_blocks = block.get("block_types") or {}
        counts["computed_only_blocks"] += len(set(all_blocks) - set(blocks))
        for name, block_type in sorted(blocks.items()):
            path = prefix + name
            counts["input_blocks"] += 1
            mode = block_type.get("nesting_mode")
            modes[mode] = modes.get(mode, 0) + 1
            if (block_type.get("min_items") or 0) >= 1:
                required.append(path)
            walk(block_type["block"], path + "[].")

    walk(load_resource(resource_type)["block"], "", True)
    shape = {
        "attribute_encodings": dict(sorted(encodings.items())),
        "block_nesting_modes": dict(sorted(modes.items())),
        "counts": counts,
        "required_input_paths": sorted(required),
        "sensitive_input_paths": sorted(sensitive),
    }
    payload = json.dumps(shape, sort_keys=True, separators=(",", ":")).encode("utf-8")
    shape["shape_sha256"] = _sha256(payload)
    return shape


def _local_inputs():
    pack = os.path.join(REPO_ROOT, "packs", "zpa")
    paths = [
        os.path.join(pack, "pack.json"),
        os.path.join(pack, "registry.json"),
        os.path.join(pack, "schemas", "provider", "zpa.json"),
    ]
    fetched = _fetch_resources()
    for resource_type in fetched:
        override = os.path.join(pack, "overrides", resource_type + ".json")
        if os.path.isfile(override):
            paths.append(override)
    return [
        {
            "path": os.path.relpath(path, REPO_ROOT).replace(os.sep, "/"),
            "sha256": _sha256(_read_bytes(path)),
        }
        for path in sorted(paths)
    ]


def _fetch_resources():
    return sorted(
        resource_type
        for resource_type, entry in load_registry().items()
        if entry.get("product") == "zpa" and "fetch" in entry
    )


def _anchor(value, label):
    expected = set(["end_line", "function", "path", "sha256", "start_line", "url"])
    _reject_keys(value, expected, label)
    if (
        not isinstance(value["path"], str)
        or value["path"].startswith("/")
        or ".." in value["path"].split("/")
        or not isinstance(value["function"], str)
        or not value["function"]
        or type(value["start_line"]) is not int
        or type(value["end_line"]) is not int
        or value["start_line"] < 1
        or value["end_line"] < value["start_line"]
        or not re.match(r"^[0-9a-f]{64}$", value["sha256"])
    ):
        raise EvidenceError("%s is invalid" % label)
    expected_url = "%s/blob/%s/%s#L%d-L%d" % (
        PROVIDER_REPOSITORY,
        PROVIDER_REF,
        value["path"],
        value["start_line"],
        value["end_line"],
    )
    if value["url"] != expected_url:
        raise EvidenceError("%s URL is not pinned to its range" % label)
    return value


def validate_local(report):
    _reject_keys(report, REPORT_KEYS, "report")
    if report["kind"] != REPORT_KIND or report["schema_version"] != REPORT_VERSION:
        raise EvidenceError("unsupported evidence report kind/version")
    provider = report["provider"]
    _reject_keys(provider, set(["commit", "ref", "repository", "source_files"]), "provider")
    if (
        provider["commit"] != PROVIDER_COMMIT
        or provider["ref"] != PROVIDER_REF
        or provider["repository"] != PROVIDER_REPOSITORY
    ):
        raise EvidenceError("provider source pin is unsupported")
    if report["local_inputs"] != _local_inputs():
        raise EvidenceError("local pack/schema input bindings are stale")

    resources = report["resources"]
    if not isinstance(resources, list):
        raise EvidenceError("resources must be a list")
    expected_types = _fetch_resources()
    if [item.get("resource_type") for item in resources] != expected_types:
        raise EvidenceError("fetch-backed resource set/order is stale")
    registry = load_registry()
    source_paths = set()
    for index, item in enumerate(resources):
        resource_type = item.get("resource_type")
        label = "resources[%d]" % index
        _reject_keys(item, RESOURCE_KEYS, label)
        entry = registry[resource_type]
        fetch = entry["fetch"]
        _reject_keys(item["fetch"], FETCH_KEYS, label + ".fetch")
        expected_fetch = {
            "optional_http_statuses": list(fetch.get("optional_http_statuses") or []),
            "pagination": fetch.get("pagination", entry["product"]),
            "path": fetch["path"],
        }
        if item["fetch"] != expected_fetch:
            raise EvidenceError("%s fetch metadata is stale" % resource_type)
        _reject_keys(
            item["state_shape"], STATE_SHAPE_KEYS, label + ".state_shape")
        if item["state_shape"] != _state_shape(resource_type):
            raise EvidenceError("%s state-shape summary is stale" % resource_type)
        imported = item["import"]
        _reject_keys(imported, IMPORT_KEYS, label + ".import")
        if imported.get("mode") not in IMPORT_MODES:
            raise EvidenceError("%s import mode is unsupported" % resource_type)
        if imported.get("engine_import_id_template") != adoption_entry(resource_type)["import_id"]:
            raise EvidenceError("%s engine import template is stale" % resource_type)
        if imported["mode"] == "passthrough":
            if (
                imported["grammar"] != "opaque_provider_id"
                or imported["alternate_lookup"] is not None
                or imported["numeric_exactness_requirement"] != "not_applicable"
            ):
                raise EvidenceError("%s passthrough import claim is inconsistent" % resource_type)
        elif (
            not isinstance(imported["grammar"], str)
            or imported["grammar"] not in NUMERIC_IMPORT_GRAMMARS
        ):
            raise EvidenceError(
                "%s numeric import grammar is unsupported" % resource_type)
        elif imported["numeric_exactness_requirement"] != NUMERIC_REQUIREMENT:
            raise EvidenceError(
                "%s numeric exactness requirement is unsupported"
                % resource_type
            )
        elif (
            not isinstance(imported["alternate_lookup"], str)
            or not imported["alternate_lookup"]
        ):
            raise EvidenceError("%s alternate import claim is inconsistent" % resource_type)
        identity = item["read_identity"]
        _reject_keys(identity, READ_IDENTITY_KEYS, label + ".read_identity")
        if (
            identity.get("terraform_instance_id") not in READ_IDENTITIES
            or identity.get("schema_id_attribute") not in SCHEMA_ID_SOURCES
        ):
            raise EvidenceError("%s read identity claim is unsupported" % resource_type)
        generated = item["generated_config"]
        _reject_keys(generated, set(["qualification"]), label + ".generated_config")
        if generated != {"qualification": RUNTIME_GATE}:
            raise EvidenceError("%s overclaims generated-config evidence" % resource_type)
        exceptions = item["exceptions"]
        if not isinstance(exceptions, list) or exceptions != sorted(set(exceptions)):
            raise EvidenceError("%s exceptions must be sorted and unique" % resource_type)
        evidence = item["source_evidence"]
        _reject_keys(evidence, set(["exceptions", "importer", "read_identity"]), label + ".source_evidence")
        anchors = [
            _anchor(evidence["importer"], label + ".importer"),
            _anchor(evidence["read_identity"], label + ".read_identity"),
        ]
        if sorted(evidence["exceptions"]) != exceptions:
            raise EvidenceError("%s exception anchors are incomplete" % resource_type)
        for code in exceptions:
            anchors.append(_anchor(evidence["exceptions"][code], label + "." + code))
        source_paths.update(anchor["path"] for anchor in anchors)

    source_files = provider["source_files"]
    if not isinstance(source_files, list):
        raise EvidenceError("provider.source_files must be a list")
    file_paths = [item.get("path") for item in source_files]
    if file_paths != sorted(set(file_paths)) or not source_paths.issubset(set(file_paths)):
        raise EvidenceError("provider source-file bindings are incomplete or unordered")
    for item in source_files:
        if set(item) != set(["path", "sha256"]) or not re.match(
                r"^[0-9a-f]{64}$", item.get("sha256", "")):
            raise EvidenceError("provider source-file binding is invalid")

    expected_summary = {
        "fetch_backed_resources": len(resources),
        "generated_config_runtime_gates": len(resources),
        "numeric_or_alternate_importers": sum(
            item["import"]["mode"] == "numeric_or_alternate_lookup"
            for item in resources
        ),
        "passthrough_importers": sum(
            item["import"]["mode"] == "passthrough" for item in resources
        ),
        "resources_with_sensitive_inputs": sum(
            bool(item["state_shape"]["sensitive_input_paths"])
            for item in resources
        ),
        "schema_id_not_source_populated": sum(
            item["read_identity"]["schema_id_attribute"] == "not_source_populated"
            for item in resources
        ),
    }
    if report["summary"] != expected_summary:
        raise EvidenceError("report summary is stale")
    return report


def _git(provider_root, args):
    try:
        output = subprocess.check_output(
            ["git", "-C", provider_root] + list(args),
            stderr=subprocess.STDOUT,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = getattr(exc, "output", b"").decode("utf-8", "replace")
        raise EvidenceError("provider git check failed: %s" % detail.strip())
    return output.decode("utf-8", "replace").strip()


def validate_provider_source(report, provider_root):
    provider_root = os.path.realpath(os.path.abspath(provider_root))
    if _git(provider_root, ["rev-parse", "HEAD"]) != PROVIDER_COMMIT:
        raise EvidenceError("provider checkout is not the pinned commit")
    if _git(provider_root, ["rev-parse", "%s^{commit}" % PROVIDER_REF]) != PROVIDER_COMMIT:
        raise EvidenceError("provider tag does not resolve to the pinned commit")
    paths = [item["path"] for item in report["provider"]["source_files"]]
    if _git(provider_root, ["status", "--porcelain", "--untracked-files=no", "--"] + paths):
        raise EvidenceError("provider evidence source files are modified")
    expected_files = dict(
        (item["path"], item["sha256"])
        for item in report["provider"]["source_files"]
    )
    for relative, expected_sha in sorted(expected_files.items()):
        path = os.path.join(provider_root, relative)
        if not os.path.isfile(path) or _sha256(_read_bytes(path)) != expected_sha:
            raise EvidenceError("provider source binding failed: %s" % relative)
    for resource in report["resources"]:
        evidence = resource["source_evidence"]
        anchors = [evidence["importer"], evidence["read_identity"]]
        anchors.extend(evidence["exceptions"].values())
        for anchor in anchors:
            raw_lines = _read_bytes(
                os.path.join(provider_root, anchor["path"])
            ).splitlines(True)
            if anchor["end_line"] > len(raw_lines):
                raise EvidenceError("provider source range exceeds file: %s" % anchor["path"])
            selected = b"".join(raw_lines[anchor["start_line"] - 1:anchor["end_line"]])
            if _sha256(selected) != anchor["sha256"]:
                raise EvidenceError(
                    "provider source range binding failed: %s:%d-%d"
                    % (anchor["path"], anchor["start_line"], anchor["end_line"])
                )
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description="Audit pinned ZPA provider evidence")
    parser.add_argument("--matrix", default=MATRIX)
    parser.add_argument("--provider-root")
    args = parser.parse_args(argv)
    try:
        report = validate_local(_load_json(args.matrix))
        if args.provider_root:
            validate_provider_source(report, args.provider_root)
        sys.stdout.write(
            "ZPA provider evidence valid (%d resources; source=%s)\n"
            % (len(report["resources"]), "verified" if args.provider_root else "not requested")
        )
        return 0
    except (EvidenceError, IOError, OSError, KeyError, TypeError, ValueError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1


if __name__ == "__main__":
    sys.exit(main())
