"""Compare raw API bodies with a Terraform provider schema.

This is a pack-readiness helper: every observed API field should be either a
Terraform input, intentionally transformed/dropped by overrides, known
computed-only provider state, or an unknown that requires pack author review.

Stdlib-only, Python 3.6-floor.
"""
import argparse
import json
import os
import sys

from engine import tfschema
from engine.transform import _coerce_primitive
from engine.transform import apply_overrides
from engine.transform import snake
from engine.transform import snake_keys


BUCKETS = (
    "kept",
    "renamed",
    "transformed",
    "defaulted",
    "relationship",
    "dropped_default",
    "dropped_override",
    "dropped_acknowledged",
    "dropped_known",
    "unknown",
    "shape_mismatch",
    "skipped",
)

TRANSFORM_KEYS = (
    "split_csv",
    "sort_lists",
    "references",
    "divide",
    "invert_bool",
    "value_map",
    "strip_prefix",
    "html_escape_fields",
)

READ_ONLY_NAMES = frozenset((
    "_depth",
    "children",
    "created",
    "display",
    "display_url",
    "last_updated",
    "owner",
    "tagged_items",
    "url",
))

READ_ONLY_SUFFIXES = ("_count", "_url")

FIELD_ALIASES = {
    "address": "ip_address",
    "color": "color_hex",
    "face": "rack_face",
    "time_zone": "timezone",
}


def _read_json(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _provider_schema_from_terraform_dump(data, resource_type, provider_source=None):
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
            raise KeyError("provider source %r not found in Terraform schema"
                           % provider_source)
        return provider
    matches = [
        provider for provider in providers.values()
        if resource_type in (provider.get("resource_schemas") or {})
    ]
    if len(matches) == 1:
        return matches[0]
    if not matches:
        raise KeyError("resource type %r not found in Terraform schema"
                       % resource_type)
    raise ValueError(
        "resource type %r appears in multiple provider schemas; pass "
        "--provider-source" % resource_type)


def load_resource_schema(resource_type, schema_path=None, provider_source=None):
    """Load one resource schema from either a pack or a provider schema dump.

    `schema_path` accepts both the committed pack shape:
      {"resource_schemas": {...}}

    and raw `terraform providers schema -json` output:
      {"provider_schemas": {"registry.terraform.io/...": {...}}}
    """
    if schema_path is None:
        return tfschema.load_resource(resource_type)
    data = _read_json(schema_path)
    if "resource_schemas" in data:
        schemas = data["resource_schemas"]
    elif "provider_schemas" in data:
        provider = _provider_schema_from_terraform_dump(
            data, resource_type, provider_source=provider_source)
        schemas = provider.get("resource_schemas") or {}
    else:
        raise ValueError(
            "%s is not a provider schema dump: expected resource_schemas or "
            "provider_schemas" % schema_path)
    if resource_type not in schemas:
        raise KeyError("resource type %r not found in %s"
                       % (resource_type, schema_path))
    return schemas[resource_type]


def load_override_file(path):
    if not path:
        return {}
    return _read_json(path)


def api_items_from(value, source="<api>"):
    if isinstance(value, list):
        return value
    if isinstance(value, dict):
        results = value.get("results")
        if isinstance(results, list):
            return results
        return [value]
    raise ValueError("%s must be a JSON object, list, or NetBox-style "
                     "{results:[...]} wrapper" % source)


def load_api_items(paths):
    items = []
    for path in paths:
        items.extend(api_items_from(_read_json(path), source=path))
    return items


def api_metadata_from_options(value, source="<options>"):
    """Extract writable/read-only API field metadata from a DRF OPTIONS body."""
    if not isinstance(value, dict):
        raise ValueError("%s must be a JSON object" % source)
    actions = value.get("actions") or {}
    fields = {}
    for method in ("POST", "PUT", "PATCH"):
        action = actions.get(method)
        if not isinstance(action, dict):
            continue
        for name, meta in sorted(action.items()):
            if not isinstance(meta, dict):
                continue
            key = snake(name)
            merged = dict(fields.get(key) or {})
            methods = list(merged.get("methods") or [])
            if method not in methods:
                methods.append(method)
            merged.update(snake_keys(meta))
            merged["methods"] = methods
            if not merged.get("read_only"):
                merged["writable"] = True
            fields[key] = merged
    return fields


def _decode_ref_token(token):
    return token.replace("~1", "/").replace("~0", "~")


def _resolve_ref(spec, ref):
    if not ref.startswith("#/"):
        raise ValueError("only local OpenAPI refs are supported: %s" % ref)
    node = spec
    for token in ref[2:].split("/"):
        token = _decode_ref_token(token)
        if isinstance(node, list):
            if not token.isdigit():
                raise KeyError("OpenAPI ref %s indexes list with %r" % (
                    ref, token))
            node = node[int(token)]
        else:
            node = node[token]
    return node


def _resolve_schema(spec, schema):
    schema = schema or {}
    seen = set()
    while isinstance(schema, dict) and "$ref" in schema:
        ref = schema["$ref"]
        if ref in seen:
            raise ValueError("recursive OpenAPI ref: %s" % ref)
        seen.add(ref)
        ref_schema = dict(_resolve_ref(spec, ref))
        for key, value in schema.items():
            if key != "$ref":
                ref_schema[key] = value
        schema = ref_schema
    return schema


def _merge_schema(spec, schema):
    schema = dict(_resolve_schema(spec, schema))
    parts = schema.pop("allOf", None)
    if not parts:
        return schema
    merged = {}
    properties = {}
    required = []
    for part in parts:
        part = _merge_schema(spec, part)
        for key, value in part.items():
            if key == "properties":
                properties.update(value or {})
            elif key == "required":
                required.extend(value or [])
            elif key not in merged:
                merged[key] = value
    properties.update(schema.get("properties") or {})
    required.extend(schema.get("required") or [])
    merged.update(schema)
    if properties:
        merged["properties"] = properties
    if required:
        merged["required"] = sorted(set(required))
    return merged


def _content_schema(content):
    if not isinstance(content, dict):
        return None
    media = content.get("application/json")
    if media is None:
        for _, media in sorted(content.items()):
            if isinstance(media, dict) and "schema" in media:
                break
        else:
            return None
    return media.get("schema")


def _response_schema(spec, operation):
    responses = operation.get("responses") or {}
    response = responses.get("200")
    if response is None:
        for code, candidate in sorted(responses.items()):
            if str(code).startswith("2"):
                response = candidate
                break
    if isinstance(response, dict) and "$ref" in response:
        response = _resolve_ref(spec, response["$ref"])
    if not isinstance(response, dict):
        return None
    if "content" in response:
        return _content_schema(response.get("content"))
    return response.get("schema")


def _request_schema(spec, operation):
    body = operation.get("requestBody")
    if isinstance(body, dict) and "$ref" in body:
        body = _resolve_ref(spec, body["$ref"])
    if isinstance(body, dict):
        schema = _content_schema(body.get("content"))
        if schema:
            return schema
    for param in operation.get("parameters") or []:
        if isinstance(param, dict) and "$ref" in param:
            param = _resolve_ref(spec, param["$ref"])
        if isinstance(param, dict) and param.get("in") == "body":
            return param.get("schema")
    return None


def _openapi_operation(spec, operation_ref):
    if ":" not in operation_ref:
        raise ValueError(
            "OpenAPI operation must be METHOD:/path, got %r" % operation_ref)
    method, path = operation_ref.split(":", 1)
    method = method.lower()
    paths = spec.get("paths") or {}
    if path not in paths or method not in (paths[path] or {}):
        raise KeyError("OpenAPI operation %s:%s not found" % (method.upper(), path))
    return paths[path][method]


def _schema_kind(schema):
    if schema.get("type"):
        return schema.get("type")
    if schema.get("properties"):
        return "object"
    if schema.get("additionalProperties"):
        return "object"
    return None


def _record_openapi_field(fields, path, schema, mode, required=False):
    entry = fields.setdefault(path, {"path": path})
    if mode == "read":
        entry["readable"] = True
    elif mode == "write":
        entry["writable"] = True
        if required:
            entry["required"] = True
    if schema.get("readOnly"):
        entry["read_only"] = True
    if schema.get("writeOnly"):
        entry["write_only"] = True
    kind = _schema_kind(schema)
    if kind:
        types = list(entry.get("schema_types") or [])
        if kind not in types:
            types.append(kind)
        entry["schema_types"] = types


def _flatten_openapi_schema(spec, schema, fields, mode, prefix="", depth=0):
    if depth > 8:
        return
    schema = _merge_schema(spec, schema)
    kind = _schema_kind(schema)
    if kind == "array":
        items = schema.get("items") or {}
        child_prefix = prefix + "[]" if prefix else ""
        _flatten_openapi_schema(spec, items, fields, mode, child_prefix)
        return
    if kind != "object":
        if prefix:
            _record_openapi_field(fields, prefix, schema, mode)
        return
    required = set(schema.get("required") or [])
    for raw_name, raw_prop in sorted((schema.get("properties") or {}).items()):
        prop = _merge_schema(spec, raw_prop)
        name = snake(raw_name)
        path = _path_join(prefix, name)
        _record_openapi_field(
            fields, path, prop, mode,
            required=raw_name in required or name in required)
        prop_kind = _schema_kind(prop)
        if prop_kind == "object" and prop.get("properties"):
            _flatten_openapi_schema(spec, prop, fields, mode, path, depth + 1)
        elif prop_kind == "array":
            items = _merge_schema(spec, prop.get("items") or {})
            if _schema_kind(items) == "object":
                _flatten_openapi_schema(
                    spec, items, fields, mode, path + "[]", depth + 1)


def api_metadata_from_openapi(spec, read_operations=None, write_operations=None):
    fields = {}
    read_operations = read_operations or []
    write_operations = write_operations or []
    for operation_ref in read_operations:
        schema = _response_schema(spec, _openapi_operation(spec, operation_ref))
        if schema:
            _flatten_openapi_schema(spec, schema, fields, "read")
    for operation_ref in write_operations:
        schema = _request_schema(spec, _openapi_operation(spec, operation_ref))
        if schema:
            _flatten_openapi_schema(spec, schema, fields, "write")
    if write_operations:
        for entry in fields.values():
            if (entry.get("readable") and not entry.get("writable")
                    and not entry.get("read_only")):
                entry["response_only"] = True
    return fields


def load_api_metadata(paths, openapi_path=None, openapi_read_operations=None,
                      openapi_write_operations=None):
    fields = {}
    for path in paths or []:
        fields.update(api_metadata_from_options(_read_json(path), source=path))
    if openapi_path:
        fields.update(api_metadata_from_openapi(
            _read_json(openapi_path),
            read_operations=openapi_read_operations,
            write_operations=openapi_write_operations,
        ))
    return fields


def _type_name(value):
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, int) and not isinstance(value, bool):
        return "int"
    if isinstance(value, float):
        return "float"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "list"
    if isinstance(value, dict):
        return "object"
    return type(value).__name__


def _path_join(prefix, name):
    return name if not prefix else prefix + "." + name


def _path_aliases(path):
    aliases = [path]
    no_brackets = path.replace("[]", "")
    if no_brackets != path:
        aliases.append(no_brackets)
    return aliases


def _contains_path(paths, path):
    return any(alias in paths for alias in _path_aliases(path))


def _mapping_value(mapping, path):
    for alias in _path_aliases(path):
        if alias in mapping:
            return True, mapping[alias]
    return False, None


def _matches_default(value, default):
    if (isinstance(default, int) and not isinstance(default, bool)
            and isinstance(value, str)):
        try:
            value = int(value)
        except ValueError:
            pass
    return value == default


def _primitive_matches(value, prim):
    if value is None:
        return True
    if prim == "string":
        return isinstance(value, str)
    if prim == "bool":
        return isinstance(value, bool)
    if prim == "number":
        return (
            isinstance(value, (int, float))
            and not isinstance(value, bool)
        )
    return True


def _primitive_transform_reason(value, prim):
    coerced = _coerce_primitive(value, prim)
    if coerced != value and _primitive_matches(coerced, prim):
        return "coerce_%s_to_%s" % (_type_name(value), prim)
    return None


def _is_read_only_path(path):
    leaf = path.rsplit(".", 1)[-1]
    if leaf in READ_ONLY_NAMES:
        return True
    return any(leaf.endswith(suffix) for suffix in READ_ONLY_SUFFIXES)


def _alias_for(key, keep_attrs, computed_attrs):
    candidates = []
    if key in FIELD_ALIASES:
        candidates.append((FIELD_ALIASES[key], "field_alias"))
    candidates.append((key + "_id", "relationship_id"))
    candidates.append((key + "_ids", "relationship_ids"))
    candidates.append(("rack_" + key, "field_alias"))
    if key.startswith("vc_"):
        candidates.append(("virtual_chassis_" + key[3:], "field_alias"))
    if key.endswith("4"):
        candidates.append((key[:-1] + "v4", "field_alias"))
    if key.endswith("6"):
        candidates.append((key[:-1] + "v6", "field_alias"))
    for candidate, reason in candidates:
        if candidate in keep_attrs:
            return candidate, "input", reason
        if candidate in computed_attrs:
            return candidate, "computed", reason
    return None, None, None


def _relationship_value(value):
    if value is None:
        return True
    if isinstance(value, dict):
        return "id" in value
    if isinstance(value, list):
        return all(isinstance(v, dict) and "id" in v for v in value)
    return False


def _skip_item(item, override):
    for matcher in override.get("skip_if") or []:
        if all(item.get(field) == value for field, value in matcher.items()):
            return True
    return False


class Report(object):
    def __init__(self, resource_type):
        self.resource_type = resource_type
        self.item_count = 0
        self._buckets = dict((bucket, {}) for bucket in BUCKETS)

    def add(self, bucket, path, reason, value=None):
        entries = self._buckets[bucket]
        entry = entries.setdefault(path, {
            "path": path,
            "count": 0,
            "reasons": {},
            "types": {},
        })
        entry["count"] += 1
        entry["reasons"][reason] = entry["reasons"].get(reason, 0) + 1
        entry["types"][_type_name(value)] = (
            entry["types"].get(_type_name(value), 0) + 1)

    def paths(self, bucket):
        return set(self._buckets[bucket])

    def has_unknowns(self):
        return bool(self._buckets["unknown"] or self._buckets["shape_mismatch"])

    def paths_with_reasons(self, bucket, reasons):
        reasons = set(reasons)
        return set(
            path for path, entry in self._buckets[bucket].items()
            if reasons.intersection(entry["reasons"])
        )

    def as_dict(self):
        paths = {}
        for bucket in BUCKETS:
            paths[bucket] = sorted(
                self._buckets[bucket].values(),
                key=lambda entry: entry["path"])
        summary = {}
        unique_paths = {}
        for bucket in BUCKETS:
            summary[bucket] = sum(
                entry["count"] for entry in self._buckets[bucket].values())
            unique_paths[bucket] = len(self._buckets[bucket])
        return {
            "resource_type": self.resource_type,
            "items": self.item_count,
            "summary": {
                "observations": summary,
                "unique_paths": unique_paths,
            },
            "paths": paths,
            "suggestions": {
                "acknowledged_drops": sorted(self.paths("dropped_known")),
                "provider_gaps": sorted(self.paths_with_reasons(
                    "unknown",
                    ("api_required_not_in_provider",
                     "api_writable_not_in_provider"),
                )),
                "review_unknown": sorted(
                    self.paths("unknown") | self.paths("shape_mismatch")),
            },
        }


def _add_leaves(report, bucket, path, value, reason):
    if isinstance(value, dict):
        if not value:
            report.add(bucket, path, reason, value)
            return
        for key in sorted(value):
            _add_leaves(report, bucket, _path_join(path, key), value[key], reason)
        return
    if isinstance(value, list):
        if not value:
            report.add(bucket, path, reason, value)
            return
        if all(isinstance(v, dict) for v in value):
            for elem in value:
                _add_leaves(report, bucket, path + "[]", elem, reason)
        else:
            report.add(bucket, path, reason, value)
        return
    report.add(bucket, path, reason, value)


def _override_bucket(override, path, value, allow_acknowledged=False):
    drops = set(override.get("drops") or [])
    if _contains_path(drops, path):
        return "dropped_override", "override_drop"
    defaults = override.get("drop_if_default") or {}
    found, default = _mapping_value(defaults, path)
    if found and _matches_default(value, default):
        return "dropped_default", "drop_if_default"
    if allow_acknowledged:
        acknowledged = set(override.get("acknowledged_drops") or [])
        if _contains_path(acknowledged, path):
            return "dropped_acknowledged", "acknowledged_drop"
    return None, None


def _mark_or_walk_input_attr(report, path, value, encoding, override,
                             api_metadata=None):
    bucket, reason = _override_bucket(
        override, path, value, allow_acknowledged=False)
    if bucket:
        _add_leaves(report, bucket, path, value, reason)
        return
    _walk_attr_value(report, path, value, encoding, override, api_metadata)


def _api_metadata_for_path(api_metadata, path):
    if not api_metadata:
        return None
    for alias in _path_aliases(path):
        if alias in api_metadata:
            return api_metadata[alias]
    return None


def _drop_absent_non_schema_value(report, path, value):
    if value is None:
        report.add(
            "dropped_known", path,
            "null_non_schema_field", value)
        return True
    if isinstance(value, str) and value == "":
        report.add(
            "dropped_known", path,
            "empty_non_schema_string", value)
        return True
    if isinstance(value, list) and not value:
        report.add(
            "dropped_known", path,
            "empty_non_schema_list", value)
        return True
    if isinstance(value, dict) and not value:
        report.add(
            "dropped_known", path,
            "empty_non_schema_object", value)
        return True
    return False


def _mark_unknown_or_api_known(report, path, value, api_metadata, fallback_reason):
    if _drop_absent_non_schema_value(report, path, value):
        return
    meta = _api_metadata_for_path(api_metadata, path)
    if meta and meta.get("read_only"):
        _add_leaves(report, "dropped_known", path, value, "api_read_only")
    elif meta and meta.get("response_only"):
        _add_leaves(report, "dropped_known", path, value, "api_response_only")
    elif meta and meta.get("writable"):
        reason = (
            "api_required_not_in_provider"
            if meta.get("required")
            else "api_writable_not_in_provider"
        )
        _add_leaves(report, "unknown", path, value, reason)
    elif meta:
        _add_leaves(
            report, "unknown", path, value,
            "api_spec_observed_not_in_provider")
    elif (isinstance(value, dict)
          and "value" in value
          and "label" in value):
        report.add("dropped_known", path, "read_only_choice_object", value)
    else:
        _add_leaves(report, "unknown", path, value, fallback_reason)


def _walk_object_members(report, path, value, members, override, api_metadata=None):
    if not isinstance(value, dict):
        report.add("shape_mismatch", path, "expected_object", value)
        return
    if not value:
        report.add("kept", path, "terraform_input_empty_object", value)
        return
    for key in sorted(value):
        child = _path_join(path, key)
        if key in members:
            _mark_or_walk_input_attr(
                report, child, value[key], members[key], override, api_metadata)
        else:
            bucket, reason = _override_bucket(
                override, child, value[key], allow_acknowledged=True)
            if bucket:
                _add_leaves(report, bucket, child, value[key], reason)
            else:
                _mark_unknown_or_api_known(
                    report, child, value[key], api_metadata,
                    "undeclared_object_member")


def _walk_attr_value(report, path, value, encoding, override, api_metadata=None):
    if isinstance(encoding, str):
        if isinstance(value, dict) and "value" in value and "label" in value:
            report.add("transformed", path, "choice_value", value)
        elif _primitive_matches(value, encoding):
            report.add("kept", path, "terraform_input", value)
        else:
            reason = _primitive_transform_reason(value, encoding)
            if reason:
                report.add("transformed", path, reason, value)
            else:
                report.add("shape_mismatch", path, "expected_" + encoding, value)
        return
    if not (isinstance(encoding, list) and len(encoding) == 2):
        report.add("shape_mismatch", path, "unsupported_type_encoding", value)
        return
    kind, inner = encoding
    if kind == "object" and isinstance(inner, dict):
        _walk_object_members(report, path, value, inner, override, api_metadata)
        return
    if kind == "map":
        if isinstance(value, dict):
            report.add("kept", path, "terraform_input_map", value)
        else:
            report.add("shape_mismatch", path, "expected_map", value)
        return
    if kind in ("list", "set"):
        if not isinstance(value, list):
            report.add("shape_mismatch", path, "expected_" + kind, value)
            return
        if isinstance(inner, str) and any(isinstance(v, dict) for v in value):
            if all(isinstance(v, dict) and ("slug" in v or "name" in v or "id" in v)
                   for v in value):
                report.add(
                    "transformed", path,
                    "object_list_to_%s_%s" % (kind, inner), value)
            else:
                report.add(
                    "shape_mismatch", path,
                    "expected_%s_of_%s" % (kind, inner), value)
            return
        if isinstance(inner, list) and len(inner) == 2 and inner[0] == "object":
            for elem in value:
                _walk_object_members(
                    report, path + "[]", elem, inner[1], override, api_metadata)
            if not value:
                report.add("kept", path, "terraform_input_empty_" + kind, value)
            return
        report.add("kept", path, "terraform_input_" + kind, value)
        return
    report.add("shape_mismatch", path, "unsupported_collection_kind", value)


def _walk_block_value(report, path, value, block_type, override, api_metadata=None):
    block = block_type["block"]
    if tfschema.block_is_single(block_type):
        if isinstance(value, dict):
            _walk_block(
                report, path, value, block, override, resource_top=False,
                api_metadata=api_metadata)
        elif isinstance(value, list) and all(isinstance(v, dict) for v in value):
            for elem in value:
                _walk_block(
                    report, path, elem, block, override, resource_top=False,
                    api_metadata=api_metadata)
        else:
            report.add("shape_mismatch", path, "expected_single_block", value)
        return
    if isinstance(value, list):
        for elem in value:
            if isinstance(elem, dict):
                _walk_block(
                    report, path + "[]", elem, block, override,
                    resource_top=False, api_metadata=api_metadata)
            else:
                report.add("shape_mismatch", path + "[]",
                           "expected_block_object", elem)
        return
    if isinstance(value, dict):
        _walk_block(
            report, path + "[]", value, block, override,
            resource_top=False, api_metadata=api_metadata)
        return
    report.add("shape_mismatch", path, "expected_block_list", value)


def _walk_block(report, prefix, value, block, override, resource_top=False,
                api_metadata=None):
    if not isinstance(value, dict):
        report.add("shape_mismatch", prefix or "$item", "expected_object", value)
        return
    cls = (
        tfschema.resource_input_attrs(block)
        if resource_top else tfschema.classify_attributes(block)
    )
    keep_attrs = set(cls["required"] + cls["optional"])
    computed_attrs = set(cls["computed_only"])
    attrs = block.get("attributes") or {}
    block_types = block.get("block_types") or {}
    input_blocks = tfschema.input_block_types(block)
    for key in sorted(value):
        child = _path_join(prefix, key)
        child_value = value[key]
        if key in keep_attrs:
            _mark_or_walk_input_attr(
                report, child, child_value,
                tfschema.attr_type(attrs[key]), override, api_metadata)
        elif key in computed_attrs or key in attrs:
            bucket, reason = _override_bucket(
                override, child, child_value, allow_acknowledged=True)
            if bucket:
                _add_leaves(report, bucket, child, child_value, reason)
            else:
                _add_leaves(
                    report, "dropped_known", child, child_value,
                    "computed_only_attribute")
        elif key in input_blocks:
            bucket, reason = _override_bucket(
                override, child, child_value, allow_acknowledged=False)
            if bucket:
                _add_leaves(report, bucket, child, child_value, reason)
            else:
                _walk_block_value(
                    report, child, child_value, block_types[key], override,
                    api_metadata=api_metadata)
        elif key in block_types:
            bucket, reason = _override_bucket(
                override, child, child_value, allow_acknowledged=True)
            if bucket:
                _add_leaves(report, bucket, child, child_value, reason)
            else:
                _add_leaves(
                    report, "dropped_known", child, child_value,
                    "non_input_block")
        else:
            bucket, reason = _override_bucket(
                override, child, child_value, allow_acknowledged=True)
            if bucket:
                _add_leaves(report, bucket, child, child_value, reason)
            elif _is_read_only_path(child):
                _add_leaves(
                    report, "dropped_known", child, child_value,
                    "common_read_only")
            else:
                alias, alias_kind, alias_reason = _alias_for(
                    key, keep_attrs, computed_attrs)
                if (alias and alias_kind == "input"
                        and alias_reason.startswith("relationship")
                        and _relationship_value(child_value)):
                    report.add(
                        "relationship", child,
                        "relationship_id:" + alias, child_value)
                elif alias and alias_kind == "input":
                    report.add(
                        "transformed", child,
                        alias_reason + ":" + alias, child_value)
                elif alias and alias_kind == "computed":
                    _add_leaves(
                        report, "dropped_known", child, child_value,
                        "computed_alias:" + alias)
                else:
                    _mark_unknown_or_api_known(
                        report, child, child_value, api_metadata,
                        "no_schema_input_or_override")


def _record_override_actions(report, raw, normalized, override):
    for old, new in sorted((override.get("renames") or {}).items()):
        if old in raw:
            report.add("renamed", old, "renamed_to:" + new, raw[old])
    for name in TRANSFORM_KEYS:
        for field in sorted((override.get(name) or {})):
            if field in raw:
                report.add("transformed", field, name, raw[field])
    for field in sorted((override.get("drops") or [])):
        if "." not in field and field in raw:
            report.add("dropped_override", field, "override_drop", raw[field])
    for field, default in sorted((override.get("drop_if_default") or {}).items()):
        if "." not in field and field in raw and _matches_default(raw[field], default):
            report.add("dropped_default", field, "drop_if_default", raw[field])
    for field in sorted((override.get("defaults") or {})):
        if field not in raw and field in normalized:
            report.add("defaulted", field, "override_default", normalized[field])


def reconcile_items(resource_type, items, resource_schema, override=None,
                    api_metadata=None):
    override = override or {}
    report = Report(resource_type)
    block = resource_schema["block"]
    for raw in items:
        report.item_count += 1
        snake_raw = snake_keys(raw)
        if _skip_item(snake_raw, override):
            report.add(
                "skipped", "$item", "skip_if",
                snake_raw.get("name") or snake_raw.get("id"))
            continue
        normalized = apply_overrides(snake_raw, override)
        _record_override_actions(report, snake_raw, normalized, override)
        _walk_block(
            report, "", normalized, block, override, resource_top=True,
            api_metadata=api_metadata)
    return report


def reconcile(resource_type, api_paths, schema_path=None, provider_source=None,
              override_path=None, api_options_paths=None, openapi_path=None,
              openapi_read_operations=None, openapi_write_operations=None):
    schema = load_resource_schema(
        resource_type, schema_path=schema_path, provider_source=provider_source)
    items = load_api_items(api_paths)
    override = load_override_file(override_path)
    api_metadata = load_api_metadata(
        api_options_paths,
        openapi_path=openapi_path,
        openapi_read_operations=openapi_read_operations,
        openapi_write_operations=openapi_write_operations,
    )
    return reconcile_items(
        resource_type, items, schema, override=override,
        api_metadata=api_metadata)


def _write_report(report, out_path=None):
    text = json.dumps(report.as_dict(), indent=2, sort_keys=True) + "\n"
    if out_path:
        parent = os.path.dirname(out_path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(out_path, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        sys.stdout.write(text)


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Classify raw API fields against a Terraform resource schema")
    parser.add_argument("resource_type")
    parser.add_argument(
        "--api", action="append", required=True,
        help="raw API JSON file; may be a list, object, or {results:[...]} wrapper")
    parser.add_argument(
        "--api-options", action="append",
        help="DRF OPTIONS JSON file for the same API endpoint")
    parser.add_argument(
        "--openapi",
        help="OpenAPI/Swagger JSON file for API read/write metadata")
    parser.add_argument(
        "--openapi-read", action="append",
        help="read operation as METHOD:/path, for example GET:/api/folders/{uid}")
    parser.add_argument(
        "--openapi-write", action="append",
        help="write operation as METHOD:/path; may be passed more than once")
    parser.add_argument(
        "--schema",
        help="provider schema JSON. Omit for resources already present in packs/")
    parser.add_argument(
        "--provider-source",
        help="provider source address/name when --schema has multiple providers")
    parser.add_argument(
        "--override",
        help="override JSON to explain renames, drops, defaults, and coercions")
    parser.add_argument(
        "--out",
        help="write the JSON report to this file instead of stdout")
    parser.add_argument(
        "--fail-on-unknown", action="store_true",
        help="exit non-zero if any unknown or shape-mismatch paths remain")
    args = parser.parse_args(argv)

    try:
        report = reconcile(
            args.resource_type,
            args.api,
            schema_path=args.schema,
            provider_source=args.provider_source,
            override_path=args.override,
            api_options_paths=args.api_options,
            openapi_path=args.openapi,
            openapi_read_operations=args.openapi_read,
            openapi_write_operations=args.openapi_write,
        )
    except Exception as exc:
        sys.stderr.write("error: %s\n" % exc)
        return 2
    _write_report(report, out_path=args.out)
    if args.fail_on_unknown and report.has_unknowns():
        sys.stderr.write(
            "error: %s has unknown API surface; review report\n"
            % args.resource_type)
        return 4
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
