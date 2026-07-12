"""Build the private, closed ZCC Node collector catalog.

This authoring-time tool binds the Node collector kernel to the exact five
fetch-backed ZCC resources and the committed Python OneAPI collector seams.
Node never loads pack files at runtime.
"""
import argparse
import hashlib
import json
import os
import sys


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CATALOG_KIND = "infrawright.zcc_collector_catalog"
SCHEMA_VERSION = 1
PRODUCT = "zcc"
PROVIDER_SOURCE = "zscaler/zcc"
PROVIDER_VERSION = "0.1.0-beta.1"
AUDIENCE = "https://api.zscaler.com"
PAGE_SIZE = 1000
COHORT_RESOURCES = (
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
)
SOURCE_FILES = (
    "engine/collectors/rest/__init__.py",
    "packs/_shared/zscaler/collector.py",
    "packs/zcc/collector.py",
    "packs/zcc/pack.json",
    "packs/zcc/registry.json",
)
RESOURCE_KEYS = frozenset(["fetch", "generate", "product"])
FETCH_KEYS = frozenset(["envelope", "pagination", "path"])
RESOURCE_CONTRACTS = {
    "zcc_device_cleanup": {
        "envelope": None,
        "page_size": None,
        "pagination": "single",
        "path": "zcc/papi/public/v1/getDeviceCleanupInfo",
    },
    "zcc_failopen_policy": {
        "envelope": None,
        "page_size": PAGE_SIZE,
        "pagination": "zia",
        "path": "zcc/papi/public/v1/webFailOpenPolicy/listByCompany",
    },
    "zcc_forwarding_profile": {
        "envelope": None,
        "page_size": PAGE_SIZE,
        "pagination": "zia",
        "path": "zcc/papi/public/v1/webForwardingProfile/listByCompany",
    },
    "zcc_trusted_network": {
        "envelope": "trustedNetworkContracts",
        "page_size": PAGE_SIZE,
        "pagination": "zia",
        "path": "zcc/papi/public/v1/webTrustedNetwork/listByCompany",
    },
    "zcc_web_privacy": {
        "envelope": None,
        "page_size": None,
        "pagination": "single",
        "path": "zcc/papi/public/v1/getWebPrivacyInfo",
    },
}


def _read_json(relative_path):
    with open(os.path.join(ROOT, relative_path), encoding="utf-8") as f:
        return json.load(f)


def _source_digest():
    digest = hashlib.sha256()
    for relative_path in SOURCE_FILES:
        path = os.path.join(ROOT, relative_path)
        if not os.path.isfile(path):
            raise ValueError("collector source is missing: %s" % relative_path)
        with open(path, "rb") as f:
            content = f.read()
        digest.update(relative_path.encode("utf-8"))
        digest.update(b"\0")
        digest.update(content)
        digest.update(b"\0")
    return digest.hexdigest()


def _resource(resource_type, entry):
    if not isinstance(entry, dict):
        raise ValueError("registry is missing %s" % resource_type)
    unknown_resource_keys = sorted(set(entry) - RESOURCE_KEYS)
    if unknown_resource_keys:
        raise ValueError(
            "%s registry metadata has unsupported key %s"
            % (resource_type, unknown_resource_keys[0])
        )
    if entry.get("product") != PRODUCT or entry.get("generate") is not True:
        raise ValueError("%s is not an enabled ZCC resource" % resource_type)
    fetch = entry.get("fetch")
    if not isinstance(fetch, dict):
        raise ValueError("%s is not fetch-backed" % resource_type)
    unknown_fetch_keys = sorted(set(fetch) - FETCH_KEYS)
    if unknown_fetch_keys:
        raise ValueError(
            "%s fetch metadata has unsupported key %s"
            % (resource_type, unknown_fetch_keys[0])
        )
    pagination = fetch.get("pagination")
    if pagination not in ("single", "zia"):
        raise ValueError(
            "%s has unsupported pagination %r"
            % (resource_type, pagination)
        )
    path = fetch.get("path")
    if not isinstance(path, str) or not path or path.startswith("/"):
        raise ValueError("%s has an invalid OneAPI path" % resource_type)
    envelope = fetch.get("envelope")
    if resource_type == "zcc_trusted_network":
        if envelope != "trustedNetworkContracts":
            raise ValueError("trusted-network envelope drifted")
    elif envelope is not None:
        raise ValueError("%s unexpectedly declares an envelope" % resource_type)
    resource = {
        "envelope": envelope,
        "method": "GET",
        "page_size": PAGE_SIZE if pagination == "zia" else None,
        "pagination": pagination,
        "path": path,
        "type": resource_type,
    }
    expected = dict(RESOURCE_CONTRACTS[resource_type])
    expected.update({"method": "GET", "type": resource_type})
    if resource != expected:
        raise ValueError("%s fetch contract drifted" % resource_type)
    return resource


def build_catalog():
    manifest = _read_json("packs/zcc/pack.json")
    registry = _read_json("packs/zcc/registry.json")
    if not isinstance(registry, dict):
        raise ValueError("ZCC registry must be an object")
    fetch_backed = sorted(
        resource_type for resource_type, entry in registry.items()
        if isinstance(entry, dict) and "fetch" in entry
    )
    if fetch_backed != list(COHORT_RESOURCES):
        raise ValueError(
            "ZCC fetch cohort must remain exactly %s"
            % ", ".join(COHORT_RESOURCES)
        )
    provider = (manifest.get("provider_sources") or {}).get(PRODUCT)
    version = manifest.get("pin")
    if (
            provider != PROVIDER_SOURCE
            or version != PROVIDER_VERSION
            or manifest.get("provider_prefixes") != {"zcc_": "zcc"}
            or manifest.get("requires_shared") != ["zscaler"]):
        raise ValueError("ZCC pack provider metadata drifted")
    return {
        "kind": CATALOG_KIND,
        "oneapi": {
            "audience": AUDIENCE,
            "cloud_gateway_template": "https://api.{cloud}.zsapi.net",
            "cloud_token_host_template": (
                "https://{vanity}.zslogin{cloud}.net"
            ),
            "mode": "oneapi",
            "production_gateway": "https://api.zsapi.net",
            "production_token_host_template": (
                "https://{vanity}.zslogin.net"
            ),
            "token_path": "/oauth2/v1/token",
        },
        "product": PRODUCT,
        "provider": {
            "source": provider,
            "version": version,
        },
        "resources": [
            _resource(resource_type, registry.get(resource_type))
            for resource_type in COHORT_RESOURCES
        ],
        "schema_version": SCHEMA_VERSION,
        "source_files": list(SOURCE_FILES),
        "sources_sha256": _source_digest(),
    }


def render_catalog():
    return json.dumps(build_catalog(), indent=2, sort_keys=True) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="python3 tools/zcc_collector_catalog.py"
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
                    "error: ZCC collector catalog is stale: %s\n"
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
