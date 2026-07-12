"""Build the private, closed ZPA Node transform-cohort catalog.

The cohort deliberately contains only fetch-backed resources whose current
Python transform has no resource override and whose provider-schema encodings
are already understood by the product-neutral Node kernel.  The tool is an
authoring-time producer only; Node never reads packs or schemas at runtime.
"""
import argparse
import hashlib
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

from engine import transform_catalog


CATALOG_KIND = "infrawright.zpa_transform_cohort_catalog"
SCHEMA_VERSION = 1
PRODUCT = "zpa"
PROVIDER_SOURCE = "zscaler/zpa"
PROVIDER_VERSION = "4.4.6"
PROVIDER_COMMIT = "dcf12469a9a8f648be0691c74e9816fc94ec7ddc"
COHORT_RESOURCES = (
    "zpa_pra_console_controller",
    "zpa_pra_portal_controller",
)
SOURCE_FILES = (
    "catalogs/zcc-transform-catalog.v1.json",
    "docs/evidence/zpa-provider-v4.4.6.json",
    "packs/zpa/pack.json",
    "packs/zpa/registry.json",
    "packs/zpa/schemas/provider/zpa.json",
)
ABSENT_OVERRIDE_FILES = tuple(
    "packs/zpa/overrides/%s.json" % resource_type
    for resource_type in COHORT_RESOURCES
)


def _read_json(relative_path):
    with open(os.path.join(ROOT, relative_path), encoding="utf-8") as f:
        return json.load(f)


def _source_digest():
    digest = hashlib.sha256()
    for relative_path in SOURCE_FILES:
        path = os.path.join(ROOT, relative_path)
        if not os.path.isfile(path):
            raise ValueError("catalog source is missing: %s" % relative_path)
        with open(path, "rb") as f:
            content = f.read()
        digest.update(relative_path.encode("utf-8"))
        digest.update(b"\0")
        digest.update(content)
        digest.update(b"\0")
    return digest.hexdigest()


def _evidence_rows():
    evidence = _read_json("docs/evidence/zpa-provider-v4.4.6.json")
    rows = dict(
        (row["resource_type"], row)
        for row in evidence.get("resources") or []
    )
    missing = sorted(set(COHORT_RESOURCES) - set(rows))
    if missing:
        raise ValueError("provider evidence is missing %s" % missing[0])
    return evidence, rows


def _resource(resource_type, core_resource, registry_entry, evidence_row):
    raw_fetch = registry_entry.get("fetch") or {}
    fetch = {
        "optional_http_statuses": list(
            raw_fetch.get("optional_http_statuses") or []
        ),
        "pagination": raw_fetch.get("pagination"),
        "path": raw_fetch.get("path"),
    }
    if fetch != evidence_row.get("fetch"):
        raise ValueError(
            "%s fetch metadata disagrees with provider evidence"
            % resource_type
        )
    qualification = (
        evidence_row.get("generated_config") or {}
    ).get("qualification")
    if qualification != "terraform_runtime_evidence_required":
        raise ValueError(
            "%s generated-config qualification must remain gated"
            % resource_type
        )
    shape_hash = (evidence_row.get("state_shape") or {}).get("shape_sha256")
    if (
            not isinstance(shape_hash, str)
            or len(shape_hash) != 64
            or any(character not in "0123456789abcdef"
                   for character in shape_hash)):
        raise ValueError("%s has no state-shape evidence hash" % resource_type)
    resource = dict(core_resource)
    resource["provider_evidence"] = {
        "fetch": fetch,
        "generated_config_qualification": qualification,
        "state_shape_sha256": shape_hash,
    }
    return resource


def build_catalog():
    for relative_path in ABSENT_OVERRIDE_FILES:
        if os.path.exists(os.path.join(ROOT, relative_path)):
            raise ValueError(
                "%s now has an override; review kernel support before "
                "regenerating" % relative_path
            )
    manifest = _read_json("packs/zpa/pack.json")
    registry = _read_json("packs/zpa/registry.json")
    evidence, evidence_rows = _evidence_rows()
    core_catalog = transform_catalog.transform_resource_cohort(
        PRODUCT, list(COHORT_RESOURCES)
    )
    core_resources = dict(
        (resource["type"], resource)
        for resource in core_catalog["resources"]
    )
    provider = (manifest.get("provider_sources") or {}).get(PRODUCT)
    version = manifest.get("pin")
    evidence_provider = evidence.get("provider") or {}
    if (
            provider != PROVIDER_SOURCE
            or version != PROVIDER_VERSION
            or evidence_provider.get("ref") != "v" + PROVIDER_VERSION
            or evidence_provider.get("commit") != PROVIDER_COMMIT):
        raise ValueError("ZPA pack and provider evidence pins disagree")
    resources = []
    for resource_type in COHORT_RESOURCES:
        entry = registry.get(resource_type)
        if not isinstance(entry, dict) or entry.get("product") != PRODUCT:
            raise ValueError("registry is missing %s" % resource_type)
        resources.append(_resource(
            resource_type,
            core_resources[resource_type],
            entry,
            evidence_rows[resource_type],
        ))
    return {
        "absent_override_files": list(ABSENT_OVERRIDE_FILES),
        "kind": CATALOG_KIND,
        "product": PRODUCT,
        "provider": {
            "evidence_commit": PROVIDER_COMMIT,
            "source": provider,
            "version": version,
        },
        "python_compatibility_source": SOURCE_FILES[0],
        "resources": resources,
        "schema_version": SCHEMA_VERSION,
        "source_files": list(SOURCE_FILES),
        "sources_sha256": _source_digest(),
    }


def render_catalog():
    return json.dumps(build_catalog(), indent=2, sort_keys=True) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="python3 tools/zpa_transform_cohort_catalog.py"
    )
    output = parser.add_mutually_exclusive_group(required=True)
    output.add_argument("--out")
    output.add_argument("--check")
    args = parser.parse_args(argv)
    try:
        text = render_catalog()
        if args.check:
            with open(args.check, encoding="utf-8") as f:
                actual = f.read()
            if actual != text:
                sys.stderr.write(
                    "error: ZPA transform cohort catalog is stale: %s\n"
                    % args.check
                )
                return 1
            return 0
        with open(args.out, "w", encoding="utf-8") as f:
            f.write(text)
    except (IOError, OSError, KeyError, TypeError, ValueError) as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
