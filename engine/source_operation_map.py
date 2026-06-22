"""Derive read-path registry entries from provider source and OpenAPI.

This is a best-effort bridge for Go Terraform providers that call generated
OpenAPI clients. It scans provider source files for Terraform resource type
strings, then looks for OpenAPI operation IDs referenced as Go identifiers in
those files. GET operations become source/read entries that
`openapi_resource_map` can use for registry-backed coverage.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import re
import sys

from engine import openapi_resource_map


GO_FILE_SUFFIX = ".go"
AMBIGUITY_SCORE_DELTA = 5


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _write_json(data, path=None):
    text = json.dumps(data, indent=2, sort_keys=True) + "\n"
    if path:
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        sys.stdout.write(text)


def _canonical(value):
    return re.sub(r"[^a-z0-9]", "", value.lower())


def _operation_aliases(operation_id):
    aliases = set([_canonical(operation_id)])
    if operation_id.lower().startswith("route"):
        aliases.add(_canonical(operation_id[5:]))
    for alias in list(aliases):
        aliases.add(alias + "withresponse")
    return sorted(a for a in aliases if a)


def _operation_index(spec):
    operations = []
    for path, path_obj in sorted((spec.get("paths") or {}).items()):
        for method, operation in sorted((path_obj or {}).items()):
            if not isinstance(operation, dict):
                continue
            operation_id = operation.get("operationId")
            if not operation_id:
                continue
            operations.append({
                "method": method.upper(),
                "path": path,
                "operation_id": operation_id,
                "aliases": _operation_aliases(operation_id),
            })
    return operations


def _source_files(source_root):
    for root, dirs, files in os.walk(source_root):
        dirs[:] = [
            d for d in dirs
            if d not in (".git", "vendor", ".terraform", "node_modules")
        ]
        for filename in files:
            if not filename.endswith(GO_FILE_SUFFIX):
                continue
            if filename.endswith("_test.go"):
                continue
            yield os.path.join(root, filename)


def _resource_files(source_root, resource_names):
    resources = dict((name, []) for name in resource_names)
    for path in _source_files(source_root):
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except UnicodeDecodeError:
            continue
        for resource in resource_names:
            if '"%s"' % resource in text:
                resources[resource].append(path)
    for paths in resources.values():
        paths.sort()
    return resources


def _is_identifier_start(char):
    return char == "_" or char.isalpha()


def _is_identifier_part(char):
    return char == "_" or char.isalnum()


def _skip_quoted(text, index, quote):
    index += 1
    while index < len(text):
        char = text[index]
        if char == "\\" and quote != "`":
            index += 2
            continue
        if char == quote:
            return index + 1
        index += 1
    return index


def _go_identifier_tokens(text):
    """Return Go identifiers while ignoring comments and string/rune literals."""
    tokens = set()
    index = 0
    while index < len(text):
        char = text[index]
        nxt = text[index + 1] if index + 1 < len(text) else ""
        if char == "/" and nxt == "/":
            index = text.find("\n", index + 2)
            if index == -1:
                break
            continue
        if char == "/" and nxt == "*":
            end = text.find("*/", index + 2)
            index = len(text) if end == -1 else end + 2
            continue
        if char in ('"', "'", "`"):
            index = _skip_quoted(text, index, char)
            continue
        if _is_identifier_start(char):
            start = index
            index += 1
            while index < len(text) and _is_identifier_part(text[index]):
                index += 1
            tokens.add(_canonical(text[start:index]))
            continue
        index += 1
    return tokens


def _base_tokens(resource_type, resource_prefix):
    tokens = openapi_resource_map._base_tokens(resource_type, resource_prefix)
    drop = set(("cloud", "apps", "asserts", "k6", "machine", "learning",
                "oncall", "synthetic", "monitoring"))
    return [token for token in tokens if token not in drop]


def _path_parts(path):
    return [part for part in path.strip("/").split("/") if part]


def _is_path_parameter(part):
    return part.startswith("{") and part.endswith("}")


def _operation_words(operation_id):
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", operation_id)
    value = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1 \2", value)
    value = re.sub(r"[^A-Za-z0-9]+", " ", value)
    return [part.lower() for part in value.split() if part]


def _is_list_operation(operation_id):
    words = _operation_words(operation_id)
    if "list" in words or "search" in words:
        return True
    return "get" in words and "all" in words


def _path_kind(operation):
    if _is_list_operation(operation["operation_id"]):
        return "list"
    parts = _path_parts(operation["path"])
    if parts and _is_path_parameter(parts[-1]):
        return "detail"
    return "list"


def _candidate_score(resource_type, resource_prefix, operation):
    path = operation["path"]
    operation_id = operation["operation_id"]
    score = 0
    for token in _base_tokens(resource_type, resource_prefix):
        if _canonical(token) in _canonical(path):
            score += 5
    if "{" in path:
        score += 30
    lowered = operation_id.lower()
    if _is_list_operation(operation_id):
        score -= 10
    if lowered.startswith(("get", "retrieve", "read", "routeget")):
        score += 10
    if path.endswith("/search") or "/search/" in path:
        score -= 20
    return score


def _list_candidate_score(resource_type, resource_prefix, operation):
    path = operation["path"]
    operation_id = operation["operation_id"]
    score = 0
    for token in _base_tokens(resource_type, resource_prefix):
        if _canonical(token) in _canonical(path):
            score += 5
    lowered = operation_id.lower()
    if _is_list_operation(operation_id):
        score += 20
    parts = _path_parts(path)
    if parts and _is_path_parameter(parts[-1]):
        score -= 20
    else:
        score += 15
    if lowered.startswith(("get", "retrieve", "read", "routeget")):
        score += 5
    return score


def _operation_entry(hit, evidence_kind, source_files):
    return {
        "evidence_kind": evidence_kind,
        "confidence": "high",
        "method": hit["method"],
        "operation_id": hit["operation_id"],
        "path": hit["path"],
        "path_kind": hit["path_kind"],
        "hops": [
            {
                "kind": "provider_call",
                "client_symbol": hit["operation_id"],
                "matched_aliases": hit.get("matched_aliases", []),
                "source_files": source_files,
            },
            {
                "kind": "openapi_operation",
                "operation_id": hit["operation_id"],
                "method": hit["method"],
                "path": hit["path"],
            },
        ],
    }


def _candidate_entry(hit):
    return {
        "method": hit["method"],
        "path": hit["path"],
        "operation_id": hit["operation_id"],
        "path_kind": hit["path_kind"],
        "read_score": hit["read_score"],
        "list_score": hit["list_score"],
    }


def _select_hit(hits, role):
    if role == "list":
        candidates = [hit for hit in hits if hit["path_kind"] == "list"]
        score_key = "list_score"
    else:
        candidates = list(hits)
        score_key = "read_score"
    candidates.sort(key=lambda hit: (
        -hit[score_key],
        hit["path"],
        hit["operation_id"],
    ))
    if not candidates:
        return None, []
    best = candidates[0]
    ambiguous = [
        hit for hit in candidates[1:]
        if (hit["path_kind"] == best["path_kind"]
            and best[score_key] - hit[score_key] <= AMBIGUITY_SCORE_DELTA)
    ]
    if ambiguous:
        return None, [best] + ambiguous[:4]
    return best, []


def _load_resource_schemas(schema_path, provider_source=None):
    provider = openapi_resource_map._provider_from_schema(
        _read_json(schema_path), provider_source=provider_source)
    return provider.get("resource_schemas") or {}


def derive_registry(schema_path, openapi_path, source_root,
                    provider_source=None, resource_prefix=""):
    resource_schemas = _load_resource_schemas(
        schema_path, provider_source=provider_source)
    resource_names = sorted(resource_schemas)
    files_by_resource = _resource_files(source_root, resource_names)
    operations = _operation_index(_read_json(openapi_path))

    registry = {}
    diagnostics = []
    resources_with_source_files = 0
    for resource in resource_names:
        source_paths = files_by_resource.get(resource) or []
        if source_paths:
            resources_with_source_files += 1
        source_text = []
        for path in source_paths:
            with open(path, encoding="utf-8") as f:
                source_text.append(f.read())
        source_identifiers = _go_identifier_tokens("\n".join(source_text))

        hits = []
        for operation in operations:
            if operation["method"] != "GET":
                continue
            matched_aliases = sorted(
                alias for alias in operation["aliases"]
                if alias in source_identifiers)
            if matched_aliases:
                hit = dict(operation)
                hit["matched_aliases"] = matched_aliases
                hit["path_kind"] = _path_kind(operation)
                hit["read_score"] = _candidate_score(
                    resource, resource_prefix, operation)
                hit["list_score"] = _list_candidate_score(
                    resource, resource_prefix, operation)
                hits.append(hit)

        hits.sort(key=lambda hit: (
            -hit["read_score"],
            hit["path"],
            hit["operation_id"],
        ))

        read_hit, read_ambiguous = _select_hit(hits, "read")
        list_hit, list_ambiguous = _select_hit(hits, "list")
        status = "unmapped"
        if read_ambiguous:
            status = "ambiguous_source_operation"
        elif read_hit:
            status = "mapped"
            source_files = [
                os.path.relpath(path, source_root)
                for path in source_paths
            ]
            registry[resource] = {
                "product": resource_prefix,
                "surface": resource_prefix,
                "status": "mapped",
                "read": _operation_entry(read_hit, "read", source_files),
                "source": {
                    "candidate_count": len(hits),
                    "files": source_files,
                },
            }
            if list_hit and list_hit["path"] != read_hit["path"]:
                registry[resource]["list"] = _operation_entry(
                    list_hit, "list", source_files)
            if list_ambiguous:
                registry[resource]["source"]["list_ambiguous"] = [
                    _candidate_entry(hit) for hit in list_ambiguous
                ]

        diagnostics.append({
            "resource": resource,
            "status": status,
            "reason": (
                "resource_file_not_found"
                if not source_paths else None
            ),
            "files": [
                os.path.relpath(path, source_root)
                for path in source_paths
            ],
            "ambiguous": [
                _candidate_entry(hit) for hit in read_ambiguous
            ],
            "hits": [
                _candidate_entry(hit)
                for hit in hits[:10]
            ],
        })

    ambiguous = sum(
        1 for item in diagnostics
        if item["status"] == "ambiguous_source_operation")
    return {
        "summary": {
            "resources": len(resource_names),
            "resources_with_source_files": resources_with_source_files,
            "resources_without_source_files": (
                len(resource_names) - resources_with_source_files),
            "mapped": len(registry),
            "ambiguous": ambiguous,
            "unmapped": len(resource_names) - len(registry) - ambiguous,
        },
        "registry": registry,
        "diagnostics": diagnostics,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Derive read registry from provider source operations")
    parser.add_argument("--schema", required=True, help="Terraform provider schema JSON")
    parser.add_argument("--openapi", required=True, help="OpenAPI/Swagger JSON")
    parser.add_argument("--source-root", required=True, help="Provider source root")
    parser.add_argument("--provider-source", help="Provider source address")
    parser.add_argument("--resource-prefix", default="", help="Resource name prefix/product")
    parser.add_argument("--out", help="Write source/read registry JSON to this file")
    parser.add_argument("--diagnostics", help="Write diagnostics JSON to this file")
    args = parser.parse_args(argv)
    try:
        report = derive_registry(
            args.schema,
            args.openapi,
            args.source_root,
            provider_source=args.provider_source,
            resource_prefix=args.resource_prefix,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2

    if args.diagnostics:
        _write_json({
            "summary": report["summary"],
            "diagnostics": report["diagnostics"],
        }, path=args.diagnostics)
    _write_json(report["registry"], path=args.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
