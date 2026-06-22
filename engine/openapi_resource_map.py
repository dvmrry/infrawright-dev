"""Map Terraform provider resources to OpenAPI CRUD endpoints.

This is the first pass of provider readiness coverage: deterministic endpoint
mapping and static contract comparison. It does not require live credentials.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import re
import sys

from engine import tfschema
from engine import reconcile_schema_api as reconcile


HTTP_METHODS = frozenset(("get", "post", "put", "patch", "delete"))
SURFACE_HINT_ATTR_RE = re.compile(
    r"(?:^|_)(?:url|uri|host|endpoint|token|auth|cloud|region|realm)(?:$|_)")

IRREGULAR_PLURALS = {
    "address": "addresses",
    "chassis": "chassis",
}

RESOURCE_SEGMENT_ALIASES = {
    "ztc": {
        "dns-forwarding-gateway": ("dns-gateways",),
        "forwarding-gateway": ("gateways",),
        "ip-pool-groups": ("ip-groups",),
        "provisioning-url": ("prov-url",),
        "traffic-forwarding-dns-rule": ("ec-dns",),
        "traffic-forwarding-log-rule": ("self",),
        "traffic-forwarding-rule": ("ec-rdr",),
    },
}

ACTION_RESOURCE_ALIASES = {
    "ztc_activation_status": {
        "surface": "ecAdminActivateStatus",
        "read_operations": ("GET:/ecAdminActivateStatus",),
        "write_operations": ("PUT:/ecAdminActivateStatus/activate",),
    },
}

OPENAPI_PRODUCT_MARKERS = {
    "zia": ("zia", "internet access"),
    "zpa": ("zpa", "private access"),
    "zcc": ("zcc", "client connector"),
    "ztc": ("ztc", "ztw", "zcloudconnector", "cloud & branch connector",
            "cloud and branch connector"),
}


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


def _provider_from_schema(data, provider_source=None):
    if "resource_schemas" in data:
        return data
    providers = data.get("provider_schemas") or {}
    if provider_source:
        provider = providers.get(provider_source)
        if provider is None:
            matches = [
                schema for source, schema in providers.items()
                if source.endswith("/" + provider_source)
            ]
            if len(matches) == 1:
                provider = matches[0]
        if provider is None:
            raise KeyError("provider source %r not found" % provider_source)
        return provider
    if len(providers) == 1:
        return list(providers.values())[0]
    raise ValueError("schema has multiple providers; pass --provider-source")


def _methods(path_obj):
    return sorted(k for k in (path_obj or {}) if k.lower() in HTTP_METHODS)


def _strip_prefix(value, prefix):
    if prefix and value.startswith(prefix):
        return value[len(prefix):]
    return value


def _path_parts(path, api_prefix):
    path = _strip_prefix(path, api_prefix).strip("/")
    return [p for p in path.split("/") if p]


def _canonical_path_parts(path):
    path = path.strip("/")
    out = []
    for part in path.split("/"):
        if not part:
            continue
        if _is_path_parameter(part):
            out.append("{}")
        else:
            out.append(part)
    return out


def _is_path_parameter(part):
    return part.startswith("{") and part.endswith("}")


def _collection_paths(spec, api_prefix):
    paths = spec.get("paths") or {}
    out = []
    for path, path_obj in sorted(paths.items()):
        if not path.startswith(api_prefix):
            continue
        parts = _path_parts(path, api_prefix)
        if not parts or _is_path_parameter(parts[-1]):
            continue
        methods = _methods(path_obj)
        if "get" not in methods and "post" not in methods:
            continue
        out.append(path)
    return out


def _openapi_read_paths(spec, api_prefix):
    out = []
    for path, path_obj in sorted((spec.get("paths") or {}).items()):
        if not path.startswith(api_prefix):
            continue
        if "get" not in _methods(path_obj):
            continue
        out.append((path, _canonical_path_parts(_strip_prefix(path, api_prefix))))
    return out


def _fetch_path_variants(fetch_path, product, api_prefix="/"):
    parts = _canonical_path_parts(fetch_path)
    api_parts = _canonical_path_parts(api_prefix)
    variants = []
    if parts:
        variants.append((parts, "exact"))
    if api_parts and parts[:len(api_parts)] == api_parts:
        variants.append((parts[len(api_parts):], "api_prefix_stripped"))

    for base_parts, base_variant in list(variants):
        if product and base_parts and base_parts[0].lower() == product.lower():
            if base_variant == "exact":
                product_variant = "product_prefix_stripped"
            else:
                product_variant = base_variant + "_product_prefix_stripped"
            variants.append((
                base_parts[1:],
                product_variant,
            ))

    seen = set()
    for variant_parts, variant in variants:
        key = (tuple(variant_parts), variant)
        if variant_parts and key not in seen:
            seen.add(key)
            yield variant_parts, variant


def _path_match(fetch_parts, openapi_parts):
    def same(left, right):
        return left == right or left == "{}" or right == "{}"

    def parts_equal(left, right):
        return len(left) == len(right) and all(
            same(a, b) for a, b in zip(left, right))

    if parts_equal(fetch_parts, openapi_parts):
        return "exact"
    if (fetch_parts and len(openapi_parts) >= len(fetch_parts)
            and parts_equal(openapi_parts[-len(fetch_parts):], fetch_parts)):
        return "suffix"
    return None


def _match_registry_fetch_path(spec, api_prefix, fetch_path, product):
    read_paths = _openapi_read_paths(spec, api_prefix)
    matches = []
    for fetch_parts, variant in _fetch_path_variants(
            fetch_path, product, api_prefix):
        if not fetch_parts:
            continue
        for openapi_path, openapi_parts in read_paths:
            match_kind = _path_match(fetch_parts, openapi_parts)
            if match_kind:
                matches.append({
                    "openapi_path": openapi_path,
                    "match": match_kind,
                    "variant": variant,
                })
    if not matches:
        return None
    matches.sort(key=lambda m: (
        {"exact": 0, "suffix": 1}.get(m["match"], 2),
        {
            "exact": 0,
            "api_prefix_stripped": 1,
            "product_prefix_stripped": 2,
            "api_prefix_stripped_product_prefix_stripped": 3,
        }.get(m["variant"], 4),
        m["openapi_path"],
    ))
    return matches[0]


def _openapi_product_text(spec):
    parts = [(spec.get("info") or {}).get("title") or ""]
    for server in spec.get("servers") or []:
        if isinstance(server, dict):
            parts.append(server.get("url") or "")
    return " ".join(parts).lower()


def _detected_openapi_products(spec):
    text = _openapi_product_text(spec)
    detected = set()
    for product, markers in OPENAPI_PRODUCT_MARKERS.items():
        for marker in markers:
            if marker in text:
                detected.add(product)
                break
    return detected


def _openapi_matches_resource_prefix(spec, resource_prefix):
    if resource_prefix not in OPENAPI_PRODUCT_MARKERS:
        return True
    detected = _detected_openapi_products(spec)
    if not detected:
        return True
    return resource_prefix in detected


def _detail_paths(spec, collection_path):
    paths = spec.get("paths") or {}
    separator = "" if collection_path.endswith("/") else "/"
    pattern = re.compile(
        "^%s%s\\{[^/]+\\}/?$" % (re.escape(collection_path), separator))
    return sorted(p for p in paths if pattern.match(p))


def _pluralize_token(token):
    if token in IRREGULAR_PLURALS:
        return IRREGULAR_PLURALS[token]
    if token.endswith("y") and len(token) > 1 and token[-2] not in "aeiou":
        return token[:-1] + "ies"
    if token.endswith(("s", "x", "ch", "sh")):
        return token + "es"
    return token + "s"


def _pluralize_slug(slug):
    parts = slug.split("-")
    if not parts:
        return slug
    parts[-1] = _pluralize_token(parts[-1])
    return "-".join(parts)


def _singularize_token(token):
    if token == "addresses":
        return "address"
    if token.endswith("ies") and len(token) > 3:
        return token[:-3] + "y"
    if token.endswith("ches") or token.endswith("shes"):
        return token[:-2]
    if token.endswith("xes") and len(token) > 3:
        return token[:-2]
    if token.endswith("ses") and len(token) > 3:
        return token[:-2]
    if token.endswith("s") and not token.endswith("ss"):
        return token[:-1]
    return token


def _singularize_slug(slug):
    parts = slug.split("-")
    if not parts:
        return slug
    parts[-1] = _singularize_token(parts[-1])
    return "-".join(parts)


def _base_tokens(resource_type, resource_prefix):
    base = resource_type
    prefix = resource_prefix + "_"
    if resource_prefix and base.startswith(prefix):
        base = base[len(prefix):]
    return [p for p in base.split("_") if p]


def _slug(tokens):
    return "-".join(tokens)


def _canonical_segment_slug(value):
    """Normalize OpenAPI path segments to Terraform resource slug style."""
    value = re.sub(r"([a-z0-9])([A-Z])", r"\1-\2", value)
    value = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1-\2", value)
    value = re.sub(r"[^A-Za-z0-9]+", "-", value)
    value = value.strip("-").lower()
    return value


def _resource_slug_candidates(resource_type, resource_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    candidates = {}
    for start in range(len(tokens)):
        slug = _slug(tokens[start:])
        if not slug:
            continue
        base_score = 120 if start == 0 else 80 - start
        candidates.setdefault(slug, base_score - 8)
        candidates.setdefault(_pluralize_slug(slug), base_score)
    base_slug = _slug(tokens)
    for alias in RESOURCE_SEGMENT_ALIASES.get(resource_prefix, {}).get(base_slug, ()):
        candidates[alias] = max(candidates.get(alias, 0), 150)
    return candidates


def _app_hint(tokens, path_parts):
    if not tokens or not path_parts:
        return 0
    app = path_parts[0]
    token = tokens[0]
    if app == token or _singularize_slug(app) == token:
        return 12
    return 0


def _schema_surface_hint(resource_schema, surface):
    inputs, _ = _schema_inputs(resource_schema)
    if surface == "dcim" and "device_id" in inputs:
        return 25
    if surface == "virtualization" and "virtual_machine_id" in inputs:
        return 25
    return 0


def _method_score(spec, collection_path, detail_path):
    paths = spec.get("paths") or {}
    collection_methods = set(_methods(paths.get(collection_path)))
    detail_methods = set(_methods(paths.get(detail_path))) if detail_path else set()
    score = 0
    if "get" in detail_methods:
        score += 10
    if "post" in collection_methods:
        score += 6
    if detail_methods.intersection(("put", "patch")):
        score += 6
    return score


def _has_method(spec, path, method):
    path_obj = (spec.get("paths") or {}).get(path) or {}
    return method.lower() in path_obj


def _best_detail_path(spec, collection_path):
    details = _detail_paths(spec, collection_path)
    if len(details) == 1:
        return details[0]
    if details:
        return details[0]
    return None


def _action_slug_candidates(tokens):
    slug = _slug(tokens)
    candidates = {
        "available-" + _pluralize_slug(slug),
    }
    if tokens == ["ip"] or tokens == ["ip", "address"]:
        candidates.add("available-ips")
    return candidates


def _parent_slug_candidates(field, object_tokens):
    base = field[:-3] if field.endswith("_id") else field
    tokens = [p for p in base.split("_") if p]
    if not tokens:
        return set()
    slug = _slug(tokens)
    candidates = {
        slug,
        _pluralize_slug(slug),
    }
    if tokens[0] == "parent" and len(tokens) > 1:
        parent_slug = _slug(tokens[1:])
        candidates.add(parent_slug)
        candidates.add(_pluralize_slug(parent_slug))
    if tokens == ["ip", "range"]:
        candidates.add("ip-ranges")
    if tokens == ["virtual", "machine"]:
        candidates.add("virtual-machines")
    if tokens == ["group"] and object_tokens:
        candidates.add(_slug(object_tokens) + "-groups")
        candidates.add(_pluralize_slug(_slug(object_tokens)) + "-groups")
    return candidates


def _allocation_action_match(spec, resource_type, resource_schema,
                             resource_prefix, api_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    if not tokens or tokens[0] != "available" or len(tokens) < 2:
        return None
    object_tokens = tokens[1:]
    action_segments = _action_slug_candidates(object_tokens)
    inputs, computed = _schema_inputs(resource_schema)
    parent_fields = sorted(
        f for f in inputs
        if f.endswith("_id") and f not in computed
    )
    parent_slugs = {}
    for field in parent_fields:
        for slug in _parent_slug_candidates(field, object_tokens):
            parent_slugs.setdefault(slug, []).append(field)

    actions = []
    for path, path_obj in sorted((spec.get("paths") or {}).items()):
        if not path.startswith(api_prefix) or not path.endswith("/"):
            continue
        if "post" not in _methods(path_obj):
            continue
        parts = _path_parts(path, api_prefix)
        if len(parts) < 3:
            continue
        action_segment = parts[-1]
        if action_segment not in action_segments:
            continue
        if not (parts[-2].startswith("{") and parts[-2].endswith("}")):
            continue
        parent_segment = parts[-3]
        fields = sorted(parent_slugs.get(parent_segment) or [])
        if parent_slugs and not fields:
            continue
        actions.append({
            "path": path,
            "operation": "POST:" + path,
            "parent_collection_segment": parent_segment,
            "parent_id_fields": fields,
            "action_segment": action_segment,
        })

    if not actions:
        return None

    canonical = resource_type.replace("_available_", "_", 1)
    surface = _path_parts(actions[0]["path"], api_prefix)[0]
    write_ops = [a["operation"] for a in actions]
    return {
        "status": "special",
        "special_type": "allocation_action",
        "surface": surface,
        "reason": "parent_scoped_openapi_action",
        "collection_path": None,
        "detail_path": None,
        "canonical_resource": canonical,
        "actions": actions,
        "static_contract": _static_action_contract(
            spec, resource_schema, write_ops),
        "candidates": [],
    }


def _parent_collection_candidates(parent_slug, spec, api_prefix):
    candidates = []
    wanted = _pluralize_slug(parent_slug)
    for collection_path in _collection_paths(spec, api_prefix):
        parts = _path_parts(collection_path, api_prefix)
        if not parts or parts[-1] != wanted:
            continue
        detail_path = _best_detail_path(spec, collection_path)
        if detail_path and (_has_method(spec, detail_path, "patch")
                            or _has_method(spec, detail_path, "put")):
            candidates.append((collection_path, detail_path, parts[0]))
    return candidates


def _primary_ip_assignment_match(spec, resource_type, resource_schema,
                                 resource_prefix, api_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    if len(tokens) < 2 or tokens[-2:] != ["primary", "ip"]:
        return None
    inputs, computed = _schema_inputs(resource_schema)
    if "ip_address_id" not in inputs:
        return None

    parent_fields = []
    if "device_id" in inputs:
        parent_fields.append(("device_id", "device"))
    if "virtual_machine_id" in inputs:
        parent_fields.append(("virtual_machine_id", "virtual-machine"))
    if not parent_fields:
        return None

    assignments = []
    for parent_field, parent_slug in parent_fields:
        for collection_path, detail_path, surface in _parent_collection_candidates(
                parent_slug, spec, api_prefix):
            write_ops = []
            if _has_method(spec, detail_path, "patch"):
                write_ops.append("PATCH:" + detail_path)
            if _has_method(spec, detail_path, "put"):
                write_ops.append("PUT:" + detail_path)
            metadata = reconcile.api_metadata_from_openapi(
                spec,
                read_operations=["GET:" + detail_path],
                write_operations=write_ops,
            )
            writable_primary = sorted(
                p for p, meta in metadata.items()
                if p in ("primary_ip4", "primary_ip6") and meta.get("writable")
            )
            if not writable_primary:
                continue
            assignments.append({
                "parent_collection_path": collection_path,
                "parent_detail_path": detail_path,
                "parent_id_field": parent_field,
                "ip_address_id_field": "ip_address_id",
                "version_field": (
                    "ip_address_version"
                    if "ip_address_version" in inputs else None
                ),
                "write_operations": write_ops,
                "write_fields": writable_primary,
                "surface": surface,
            })

    if not assignments:
        return None

    parent_resource = resource_prefix + "_" + (
        "device" if assignments[0]["parent_id_field"] == "device_id"
        else "virtual_machine"
    )
    return {
        "status": "special",
        "special_type": "derived_relationship",
        "surface": assignments[0]["surface"],
        "reason": "parent_field_assignment",
        "collection_path": None,
        "detail_path": None,
        "canonical_parent_resource": parent_resource,
        "assignments": assignments,
        "candidates": [],
    }


def _primary_mac_assignment_match(spec, resource_type, resource_schema,
                                  resource_prefix, api_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    if len(tokens) < 4 or tokens[-3:] != ["primary", "mac", "address"]:
        return None
    inputs, computed = _schema_inputs(resource_schema)
    if "interface_id" not in inputs or "mac_address_id" not in inputs:
        return None

    if tokens[:2] == ["device", "interface"]:
        expected_surface = "dcim"
        parent_resource = resource_prefix + "_device_interface"
    elif tokens[:3] == ["virtual", "machine", "interface"]:
        expected_surface = "virtualization"
        parent_resource = resource_prefix + "_interface"
    else:
        return None

    assignments = []
    for collection_path, detail_path, surface in _parent_collection_candidates(
            "interface", spec, api_prefix):
        if surface != expected_surface:
            continue
        write_ops = []
        if _has_method(spec, detail_path, "patch"):
            write_ops.append("PATCH:" + detail_path)
        if _has_method(spec, detail_path, "put"):
            write_ops.append("PUT:" + detail_path)
        metadata = reconcile.api_metadata_from_openapi(
            spec,
            read_operations=["GET:" + detail_path],
            write_operations=write_ops,
        )
        writable_primary = sorted(
            p for p, meta in metadata.items()
            if p == "primary_mac_address" and meta.get("writable")
        )
        if not writable_primary:
            continue
        assignments.append({
            "parent_collection_path": collection_path,
            "parent_detail_path": detail_path,
            "parent_id_field": "interface_id",
            "mac_address_id_field": "mac_address_id",
            "write_operations": write_ops,
            "write_fields": writable_primary,
            "surface": surface,
        })

    if not assignments:
        return None

    return {
        "status": "special",
        "special_type": "derived_relationship",
        "surface": assignments[0]["surface"],
        "reason": "parent_field_assignment",
        "collection_path": None,
        "detail_path": None,
        "canonical_parent_resource": parent_resource,
        "canonical_child_resource": resource_prefix + "_mac_address",
        "assignments": assignments,
        "candidates": [],
    }


def _aliased_action_match(spec, resource_type, resource_schema, api_prefix):
    alias = ACTION_RESOURCE_ALIASES.get(resource_type)
    if not alias:
        return None
    paths = spec.get("paths") or {}
    read_ops = []
    for op in alias.get("read_operations", ()):
        method, path = op.split(":", 1)
        if path.startswith(api_prefix) and method.lower() in paths.get(path, {}):
            read_ops.append(op)
    write_ops = []
    for op in alias.get("write_operations", ()):
        method, path = op.split(":", 1)
        if path.startswith(api_prefix) and method.lower() in paths.get(path, {}):
            write_ops.append(op)
    if not read_ops and not write_ops:
        return None
    return {
        "status": "special",
        "special_type": "aliased_action",
        "surface": alias["surface"],
        "reason": "provider_resource_maps_to_openapi_action",
        "collection_path": None,
        "detail_path": None,
        "read_operations": read_ops,
        "write_operations": write_ops,
        "static_contract": _static_operations_contract(
            spec, resource_schema, read_ops, write_ops),
        "candidates": [],
    }


def _special_resource_match(spec, resource_type, resource_schema,
                            resource_prefix, api_prefix):
    return (
        _allocation_action_match(
            spec, resource_type, resource_schema, resource_prefix, api_prefix)
        or _primary_ip_assignment_match(
            spec, resource_type, resource_schema, resource_prefix, api_prefix)
        or _primary_mac_assignment_match(
            spec, resource_type, resource_schema, resource_prefix, api_prefix)
        or _aliased_action_match(
            spec, resource_type, resource_schema, api_prefix)
    )


def _match_resource(spec, resource_type, resource_schema, resource_prefix, api_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    slug_candidates = _resource_slug_candidates(resource_type, resource_prefix)
    candidates = []
    for collection_path in _collection_paths(spec, api_prefix):
        parts = _path_parts(collection_path, api_prefix)
        if not parts:
            continue
        segment = _canonical_segment_slug(parts[-1])
        if segment not in slug_candidates:
            continue
        detail_path = _best_detail_path(spec, collection_path)
        app_hint = _app_hint(tokens, parts)
        confidence = "exact_plural" if segment == _pluralize_slug(_slug(tokens)) else "suffix_plural"
        score = (
            slug_candidates[segment]
            + app_hint
            + _schema_surface_hint(resource_schema, parts[0] if parts else None)
            + _method_score(spec, collection_path, detail_path)
        )
        if confidence == "suffix_plural" and app_hint == 0:
            score -= 60
        candidates.append({
            "collection_path": collection_path,
            "detail_path": detail_path,
            "score": score,
            "surface": parts[0] if parts else None,
            "matched_segment": segment,
            "confidence": confidence,
        })
    candidates.sort(key=lambda c: (-c["score"], c["collection_path"]))
    if not candidates:
        return {
            "status": "unmatched",
            "candidates": [],
            "reason": "no_openapi_collection_path_match",
        }
    top_score = candidates[0]["score"]
    tied = [c for c in candidates if c["score"] == top_score]
    if len(tied) > 1:
        return {
            "status": "ambiguous",
            "candidates": tied[:5],
            "reason": "multiple_equal_score_matches",
        }
    status = "matched"
    reason = None
    if candidates[0]["score"] < 60:
        status = "unmatched"
        reason = "low_confidence_suffix_match"
    elif candidates[0]["detail_path"] is None:
        status = "unmatched"
        reason = "matched_collection_has_no_standard_detail_path"
    return {
        "status": status,
        "collection_path": candidates[0]["collection_path"] if status == "matched" else None,
        "detail_path": candidates[0]["detail_path"] if status == "matched" else None,
        "surface": candidates[0]["surface"] if status == "matched" else None,
        "confidence": candidates[0]["confidence"],
        "score": candidates[0]["score"],
        "reason": reason,
        "candidates": candidates[:5],
    }


def _first_path_segment(path):
    return path.replace("[]", "").split(".", 1)[0]


def _schema_inputs(resource_schema):
    block = resource_schema["block"]
    cls = tfschema.resource_input_attrs(block)
    attrs = set(cls["required"] + cls["optional"])
    computed = set(cls["computed_only"])
    blocks = set(tfschema.input_block_types(block))
    return attrs | blocks, computed


def _provider_config_surface_hints(provider):
    block = (provider.get("provider") or {}).get("block") or {}
    attrs = block.get("attributes") or {}
    hints = []
    for name in sorted(attrs):
        meta = attrs.get(name) or {}
        if SURFACE_HINT_ATTR_RE.search(name):
            hints.append({
                "name": name,
                "sensitive": bool(meta.get("sensitive")),
                "description": meta.get("description"),
            })
    return hints


def _resource_family(resource_type, resource_prefix):
    tokens = _base_tokens(resource_type, resource_prefix)
    if not tokens:
        return "unknown"
    return tokens[0]


def _openapi_path_profile(spec, api_prefix):
    paths = spec.get("paths") or {}
    matching_paths = [
        path for path in paths
        if path.startswith(api_prefix)
    ]
    first_segments = {}
    collection_segments = {}
    for path in matching_paths:
        parts = _path_parts(path, api_prefix)
        concrete = [
            _canonical_segment_slug(part)
            for part in parts
            if not _is_path_parameter(part)
        ]
        if concrete:
            first = concrete[0]
            first_segments[first] = first_segments.get(first, 0) + 1
            collection = concrete[-1]
            collection_segments[collection] = (
                collection_segments.get(collection, 0) + 1)
    def top_items(counts):
        return [
            {"segment": key, "paths": value}
            for key, value in sorted(
                counts.items(), key=lambda item: (-item[1], item[0]))[:25]
        ]
    return {
        "title": (spec.get("info") or {}).get("title"),
        "servers": [
            server.get("url")
            for server in (spec.get("servers") or [])
            if isinstance(server, dict) and server.get("url")
        ],
        "path_count_for_api_prefix": len(matching_paths),
        "top_first_segments": top_items(first_segments),
        "top_collection_segments": top_items(collection_segments),
    }


def _coverage_diagnostics(summary, family_coverage, openapi_profile,
                          provider_config_hints):
    resources = summary["resources"]
    covered = summary["matched"] + summary["special"]
    coverage_ratio = (float(covered) / resources) if resources else 0.0
    warnings = []
    if openapi_profile["path_count_for_api_prefix"] == 0:
        warnings.append({
            "code": "api_prefix_matches_no_paths",
            "message": (
                "The selected API prefix matches zero OpenAPI paths. "
                "Check whether the spec stores the product base path in "
                "servers[] instead of paths[]."),
        })
    if resources and coverage_ratio < 0.25:
        warnings.append({
            "code": "low_openapi_resource_coverage",
            "message": (
                "Fewer than 25% of Terraform resources mapped to this "
                "OpenAPI document. This often means the spec is the wrong "
                "product surface, only a partial surface, or the provider "
                "contains orchestration resources that do not map to CRUD "
                "collections."),
            "coverage_ratio": round(coverage_ratio, 4),
        })
    if resources and provider_config_hints and coverage_ratio < 0.75:
        warnings.append({
            "code": "provider_config_suggests_multiple_surfaces",
            "message": (
                "Provider configuration exposes URL/token/cloud-style knobs "
                "while OpenAPI coverage is incomplete. Classify resources by "
                "surface before field-level reconciliation."),
            "hint_attributes": [
                hint["name"] for hint in provider_config_hints
            ],
        })
    uncovered = []
    for family, counts in sorted(family_coverage.items()):
        total = sum(counts.values())
        covered_family = counts.get("matched", 0) + counts.get("special", 0)
        if total and covered_family == 0:
            uncovered.append({
                "family": family,
                "resources": total,
                "statuses": dict(sorted(counts.items())),
            })
    if uncovered:
        warnings.append({
            "code": "uncovered_resource_families",
            "message": (
                "At least one Terraform resource family had no mapped "
                "OpenAPI CRUD endpoint."),
            "families": uncovered[:50],
        })
    return {
        "coverage_ratio": round(coverage_ratio, 4),
        "covered_resources": covered,
        "family_coverage": {
            family: dict(sorted(counts.items()))
            for family, counts in sorted(family_coverage.items())
        },
        "warnings": warnings,
    }


def _static_operations_contract(spec, resource_schema, read_ops, write_ops):
    metadata = reconcile.api_metadata_from_openapi(
        spec, read_operations=read_ops, write_operations=write_ops)
    inputs, computed = _schema_inputs(resource_schema)
    write_paths = sorted(
        p for p, meta in metadata.items()
        if meta.get("writable") and "." not in p.replace("[]", ".")
    )
    read_paths = sorted(
        p for p, meta in metadata.items()
        if meta.get("readable") and "." not in p.replace("[]", ".")
    )
    response_only = sorted(
        p for p, meta in metadata.items()
        if (meta.get("response_only") or meta.get("read_only"))
        and "." not in p.replace("[]", ".")
    )
    provider_gaps = []
    aliases = []
    for path in write_paths:
        top = _first_path_segment(path)
        if top in inputs:
            continue
        alias, alias_kind, alias_reason = reconcile._alias_for(top, inputs, computed)
        if alias and alias_kind == "input":
            aliases.append({
                "api_path": path,
                "terraform_path": alias,
                "reason": alias_reason,
            })
            continue
        if top in computed:
            continue
        provider_gaps.append(path)
    return {
        "read_operations": read_ops,
        "write_operations": write_ops,
        "read_top_level_paths": read_paths,
        "write_top_level_paths": write_paths,
        "response_only_top_level_paths": response_only,
        "aliased_top_level_paths": aliases,
        "provider_gap_top_level_paths": provider_gaps,
        "summary": {
            "read_top_level": len(read_paths),
            "write_top_level": len(write_paths),
            "response_only_top_level": len(response_only),
            "aliased_top_level": len(aliases),
            "provider_gap_top_level": len(provider_gaps),
        },
    }


def _static_contract(spec, resource_schema, collection_path, detail_path):
    write_ops = []
    paths = spec.get("paths") or {}
    if collection_path and "post" in (paths.get(collection_path) or {}):
        write_ops.append("POST:" + collection_path)
    if detail_path:
        detail_methods = paths.get(detail_path) or {}
        if "put" in detail_methods:
            write_ops.append("PUT:" + detail_path)
        if "patch" in detail_methods:
            write_ops.append("PATCH:" + detail_path)
    read_ops = []
    if detail_path and "get" in (paths.get(detail_path) or {}):
        read_ops.append("GET:" + detail_path)
    return _static_operations_contract(
        spec, resource_schema, read_ops, write_ops)


def _static_action_contract(spec, resource_schema, write_ops):
    metadata = reconcile.api_metadata_from_openapi(
        spec, read_operations=[], write_operations=write_ops)
    inputs, computed = _schema_inputs(resource_schema)
    write_paths = sorted(
        p for p, meta in metadata.items()
        if meta.get("writable") and "." not in p.replace("[]", ".")
    )
    provider_gaps = []
    aliases = []
    for path in write_paths:
        top = _first_path_segment(path)
        if top in inputs:
            continue
        alias, alias_kind, alias_reason = reconcile._alias_for(top, inputs, computed)
        if alias and alias_kind == "input":
            aliases.append({
                "api_path": path,
                "terraform_path": alias,
                "reason": alias_reason,
            })
            continue
        if top in computed:
            continue
        provider_gaps.append(path)
    return {
        "write_operations": write_ops,
        "write_top_level_paths": write_paths,
        "aliased_top_level_paths": aliases,
        "provider_gap_top_level_paths": provider_gaps,
        "summary": {
            "write_top_level": len(write_paths),
            "aliased_top_level": len(aliases),
            "provider_gap_top_level": len(provider_gaps),
        },
    }


def _load_default_registry(resource_prefix, registry_data):
    if not resource_prefix:
        return {}
    elif registry_data is None:
        try:
            from engine.registry import load_registry
            return load_registry()
        except Exception:
            return {}
    return registry_data


def _registry_path_coverage(spec, api_prefix, resource_prefix, registry_data,
                            entry_key, summary_key, warning_prefix):
    registry_data = _load_default_registry(resource_prefix, registry_data)

    resources = []
    product_match = _openapi_matches_resource_prefix(spec, resource_prefix)
    for resource_type, entry in sorted((registry_data or {}).items()):
        if entry.get("product") != resource_prefix:
            continue
        path_entry = entry.get(entry_key)
        if not path_entry:
            continue
        registry_path = path_entry.get("path")
        if not registry_path:
            continue
        if entry.get("status") and entry.get("status") != "mapped":
            resources.append({
                "resource": resource_type,
                "status": entry.get("status"),
                "reason": entry.get("status"),
            })
            continue
        match = None
        if product_match:
            match = _match_registry_fetch_path(
                spec, api_prefix, registry_path, resource_prefix)
        item = {
            "resource": resource_type,
            entry_key + "_path": registry_path,
        }
        if entry_key == "fetch":
            item["pagination"] = path_entry.get("pagination", resource_prefix)
        if path_entry.get("operation_id"):
            item["operation_id"] = path_entry.get("operation_id")
        if path_entry.get("path_kind"):
            item["path_kind"] = path_entry.get("path_kind")
        if match:
            item.update({
                "status": "matched",
                "openapi_path": match["openapi_path"],
                "match": match["match"],
                "variant": match["variant"],
            })
        else:
            item.update({
                "status": "unmatched",
                "reason": (
                    "openapi_product_mismatch"
                    if not product_match
                    else "fetch_path_not_found_in_openapi_get_paths"
                ),
            })
        resources.append(item)

    matched = sum(1 for item in resources if item["status"] == "matched")
    ambiguous = sum(
        1 for item in resources
        if item["status"] == "ambiguous_source_operation")
    total = len(resources)
    coverage_ratio = float(matched) / total if total else None
    warnings = []
    if total and not product_match:
        warnings.append({
            "code": warning_prefix + "_openapi_product_mismatch",
            "message": (
                "The OpenAPI document advertises a different known product "
                "than the resource prefix; registry path suffix matches "
                "were suppressed."),
            "detected_products": sorted(_detected_openapi_products(spec)),
        })
    if total and matched < total:
        warnings.append({
            "code": warning_prefix + "_paths_missing_from_openapi",
            "message": (
                "At least one registry path was not present as an "
                "OpenAPI GET path."),
            "resources": [
                item["resource"] for item in resources
                if item["status"] != "matched"
            ][:50],
        })
    return {
        "summary": {
            summary_key: total,
            "matched": matched,
            "ambiguous": ambiguous,
            "unmatched": total - matched - ambiguous,
            "coverage_ratio": (
                round(coverage_ratio, 4)
                if coverage_ratio is not None else None
            ),
        },
        "warnings": warnings,
        "resources": resources,
    }


def _registry_fetch_coverage(spec, api_prefix, resource_prefix,
                             registry_data=None):
    coverage = _registry_path_coverage(
        spec, api_prefix, resource_prefix, registry_data,
        "fetch", "fetch_resources", "registry_fetch")
    coverage["summary"].pop("ambiguous", None)
    for warning in coverage["warnings"]:
        if warning["code"] == "registry_fetch_openapi_product_mismatch":
            warning["code"] = "registry_openapi_product_mismatch"
    return coverage


def _registry_read_coverage(spec, api_prefix, resource_prefix,
                            registry_data=None):
    return _registry_path_coverage(
        spec, api_prefix, resource_prefix, registry_data,
        "read", "read_resources", "registry_read")


def build_report(schema_path, openapi_path, provider_source=None,
                 resource_prefix="", api_prefix="/api/", registry_data=None):
    provider = _provider_from_schema(
        _read_json(schema_path), provider_source=provider_source)
    resource_schemas = provider.get("resource_schemas") or {}
    spec = _read_json(openapi_path)
    resources = []
    family_coverage = {}
    summary = {
        "resources": len(resource_schemas),
        "matched": 0,
        "special": 0,
        "ambiguous": 0,
        "unmatched": 0,
        "static_provider_gap_resources": 0,
    }
    surfaces = {}
    for resource_type in sorted(resource_schemas):
        item = {
            "resource": resource_type,
        }
        match = _match_resource(
            spec, resource_type, resource_schemas[resource_type],
            resource_prefix, api_prefix)
        if match["status"] != "matched":
            special = _special_resource_match(
                spec, resource_type, resource_schemas[resource_type],
                resource_prefix, api_prefix)
            if special is not None:
                match = special
        item.update(match)
        summary[match["status"]] += 1
        family = _resource_family(resource_type, resource_prefix)
        family_counts = family_coverage.setdefault(family, {})
        family_counts[match["status"]] = family_counts.get(match["status"], 0) + 1
        if match["status"] == "matched":
            item["static_contract"] = _static_contract(
                spec,
                resource_schemas[resource_type],
                match["collection_path"],
                match["detail_path"],
            )
        if item.get("static_contract", {}).get("provider_gap_top_level_paths"):
            summary["static_provider_gap_resources"] += 1
        if match["status"] in ("matched", "special"):
            surface = item.get("surface") or "unknown"
            surfaces[surface] = surfaces.get(surface, 0) + 1
        resources.append(item)
    openapi_profile = _openapi_path_profile(spec, api_prefix)
    provider_config_hints = _provider_config_surface_hints(provider)
    return {
        "provider_source": provider_source,
        "resource_prefix": resource_prefix,
        "api_prefix": api_prefix,
        "openapi": {
            "version": spec.get("openapi") or spec.get("swagger"),
            "path_count": len(spec.get("paths") or {}),
            "schema_count": len((spec.get("components") or {}).get("schemas") or {}),
            "profile": openapi_profile,
        },
        "provider_config_hints": provider_config_hints,
        "summary": summary,
        "coverage": _coverage_diagnostics(
            summary, family_coverage, openapi_profile,
            provider_config_hints),
        "registry_fetch_coverage": _registry_fetch_coverage(
            spec, api_prefix, resource_prefix, registry_data=registry_data),
        "registry_read_coverage": _registry_read_coverage(
            spec, api_prefix, resource_prefix, registry_data=registry_data),
        "surfaces": dict(sorted(surfaces.items())),
        "resources": resources,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Map Terraform resources to OpenAPI CRUD endpoints")
    parser.add_argument("--schema", required=True, help="Terraform provider schema JSON")
    parser.add_argument("--openapi", required=True, help="OpenAPI/Swagger JSON")
    parser.add_argument("--provider-source", help="Provider source address")
    parser.add_argument("--resource-prefix", default="", help="Resource name prefix")
    parser.add_argument("--api-prefix", default="/api/", help="OpenAPI path prefix")
    parser.add_argument(
        "--registry",
        help=(
            "Optional registry JSON for pack fetch-path or source read-path "
            "coverage"))
    parser.add_argument("--out", help="Write report to this file")
    args = parser.parse_args(argv)
    try:
        registry_data = _read_json(args.registry) if args.registry else None
        report = build_report(
            args.schema,
            args.openapi,
            provider_source=args.provider_source,
            resource_prefix=args.resource_prefix,
            api_prefix=args.api_prefix,
            registry_data=registry_data,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    _write_json(report, path=args.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
