"""Project provider-observed state values into generated module input values."""
import copy

from engine import paths
from engine import schema_paths
from engine.drift_policy import parse_path
from engine.ops import _same_json_value
from engine.tfschema import (
    attr_type,
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


class ProjectionError(ValueError):
    pass


def project_item(resource_type, state_values, sensitive_values=None, policy=None):
    """Project one provider state object into tfvars input shape.

    projection_omit applies inline during schema projection and may suppress
    sensitive, absent, or optional fields. Post-projection policy then applies
    projection_sync followed by projection_omit_if.
    """
    block = load_resource(resource_type)["block"]
    sensitive_values = sensitive_values or {}
    out = _project_block(
        state_values,
        sensitive_values,
        block,
        path=(),
        resource_top=True,
        resource_type=resource_type,
        policy=policy,
    )
    if policy:
        _apply_projection_policy(resource_type, out, policy)
    return out


def _project_block(values, sens, block, path, resource_top, resource_type, policy):
    if sens is True:
        raise ProjectionError(
            "sensitive input path %s cannot be written to generated tfvars "
            "without an explicit secret-handling policy" % _fmt_path(path)
        )
    if not isinstance(values, dict):
        raise ProjectionError("state path %s is not an object" % _fmt_path(path))
    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    required = set(cls["required"])
    optional = set(cls["optional"])
    inputs = required | optional
    block_types = input_block_types(block)
    out = {}

    for name in sorted(inputs):
        child_path = path + (name,)
        if policy and policy.projection_omits(resource_type, child_path):
            if name in required:
                raise ProjectionError(
                    "policy cannot projection_omit required path %s"
                    % _fmt_path(child_path)
                )
            continue
        if _is_sensitive_attr(sens, name):
            raise ProjectionError(
                "sensitive input path %s cannot be written to generated tfvars "
                "without an explicit secret-handling policy" % _fmt_path(child_path)
            )
        if name not in values or values.get(name) is None:
            if name in required:
                raise ProjectionError(
                    "required state path missing: %s" % _fmt_path(child_path)
                )
            continue
        out[name] = values[name]

    for name, bt in sorted(block_types.items()):
        child_path = path + (name,)
        required_block = (bt.get("min_items") or 0) >= 1
        if policy and policy.projection_omits(resource_type, child_path):
            if required_block:
                raise ProjectionError(
                    "policy cannot projection_omit required block %s"
                    % _fmt_path(child_path)
                )
            continue
        if name not in values or values.get(name) is None:
            if required_block:
                raise ProjectionError(
                    "required state path missing: %s" % _fmt_path(child_path)
                )
            continue

        inner = bt["block"]
        value = values[name]
        sens_child = sens.get(name) if isinstance(sens, dict) else {}
        if sens_child is True:
            raise ProjectionError(
                "sensitive input path %s cannot be written to generated tfvars "
                "without an explicit secret-handling policy" % _fmt_path(child_path)
            )
        if block_is_single(bt):
            single = _single_value(value)
            if single is not None:
                out[name] = _project_block(
                    single,
                    _single_sens(sens_child, child_path),
                    inner,
                    child_path,
                    resource_top=False,
                    resource_type=resource_type,
                    policy=policy,
                )
            elif required_block:
                raise ProjectionError(
                    "required state path missing: %s" % _fmt_path(child_path)
                )
        else:
            if not isinstance(value, list):
                raise ProjectionError(
                    "state path %s is not a list" % _fmt_path(child_path)
                )
            out[name] = [
                _project_block(
                    v,
                    _list_sens(sens_child, idx),
                    inner,
                    child_path + (idx,),
                    resource_top=False,
                    resource_type=resource_type,
                    policy=policy,
                )
                for idx, v in enumerate(value)
                if isinstance(v, dict)
            ]
    return out


def _single_value(value):
    if isinstance(value, dict):
        return value
    if isinstance(value, list):
        if not value:
            return None
        if len(value) == 1 and isinstance(value[0], dict):
            return value[0]
    raise ProjectionError("single nested block had unsupported state shape")


def _single_sens(sens_child, path):
    if sens_child is True:
        return True
    if isinstance(sens_child, dict):
        return sens_child
    if isinstance(sens_child, list):
        if not sens_child:
            return {}
        if len(sens_child) == 1:
            return sens_child[0] or {}
        raise ProjectionError(
            "single nested block had unsupported sensitive shape at %s"
            % _fmt_path(path)
        )
    return {}


def _list_sens(sens_child, idx):
    if isinstance(sens_child, list) and idx < len(sens_child):
        return sens_child[idx] or {}
    if isinstance(sens_child, dict):
        return sens_child
    return {}


def _any_sensitive(value):
    if value is True:
        return True
    if isinstance(value, dict):
        return any(_any_sensitive(v) for v in value.values())
    if isinstance(value, list):
        return any(_any_sensitive(v) for v in value)
    return False


def _is_sensitive_attr(sens, name):
    if not isinstance(sens, dict):
        return False
    return _any_sensitive(sens.get(name))


def _apply_projection_policy(resource_type, out, policy):
    _apply_projection_sync(resource_type, out, policy)
    _apply_projection_omit_if(resource_type, out, policy)


def _apply_projection_sync(resource_type, out, policy):
    for entry in policy.entries(resource_type, "projection_sync"):
        target_path = parse_path(entry["target_path"])
        source_path = parse_path(entry["source_path"])
        _guard_projection_sync(resource_type, entry, target_path, source_path)

        target_present, target_value = _path_value(out, target_path)
        if target_present and not _is_absent_or_empty(target_value):
            continue

        source_present, source_value = _path_value(out, source_path)
        if not source_present or _is_absent_or_empty(source_value):
            continue

        _set_path(out, target_path, copy.deepcopy(source_value))
        policy.mark_matched(entry)


def _apply_projection_omit_if(resource_type, out, policy):
    for entry in policy.entries(resource_type, "projection_omit_if"):
        selector = parse_path(entry["path"])
        if schema_paths.schema_status(resource_type, selector) == "required":
            raise ProjectionError(
                "refusing to conditionally omit required attribute %s of %s"
                % (entry["path"], resource_type)
            )
        removed = _remove_matching_leaves(
            out,
            selector,
            lambda value, values=entry["values"]: any(
                _same_json_value(value, candidate) for candidate in values
            ),
        )
        if removed:
            policy.mark_matched(entry)


def _guard_projection_sync(resource_type, entry, target_path, source_path):
    target_status = schema_paths.schema_status(resource_type, target_path)
    if target_status not in ("required", "optional"):
        raise ProjectionError(
            "refusing to projection_sync target attribute %s of %s: "
            "not a writable input attribute"
            % (entry["target_path"], resource_type)
        )

    _guard_projection_sync_path_shape(
        resource_type, "target_path", entry["target_path"], target_path
    )
    _guard_projection_sync_path_shape(
        resource_type, "source_path", entry["source_path"], source_path
    )

    target_type = _schema_terminal_type(resource_type, target_path)
    source_type = _schema_terminal_type(resource_type, source_path)
    if target_type != source_type:
        raise ProjectionError(
            "refusing to projection_sync target %s from source %s of %s: "
            "schema types differ (%r != %r)"
            % (
                entry["target_path"],
                entry["source_path"],
                resource_type,
                target_type,
                source_type,
            )
        )


def _guard_projection_sync_path_shape(resource_type, field, raw_path, path):
    block = load_resource(resource_type)["block"]
    _guard_projection_sync_block_shape(
        block,
        tuple(path),
        resource_top=True,
        resource_type=resource_type,
        field=field,
        raw_path=raw_path,
    )


def _guard_projection_sync_block_shape(
        block, path, resource_top, resource_type, field, raw_path):
    if len(path) <= 1:
        return
    segment = path[0]
    if not isinstance(segment, str):
        return
    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    attrs = block.get("attributes") or {}
    if segment in cls["required"] or segment in cls["optional"]:
        _guard_projection_sync_encoding_shape(
            attr_type(attrs[segment]),
            path[1:],
            resource_type=resource_type,
            field=field,
            raw_path=raw_path,
            segment=segment,
        )
        return
    blocks = input_block_types(block)
    if segment in blocks:
        bt = blocks[segment]
        if not block_is_single(bt):
            _projection_sync_shape_error(
                resource_type,
                field,
                raw_path,
                segment,
                "is a repeated block",
            )
        _guard_projection_sync_block_shape(
            bt["block"],
            path[1:],
            resource_top=False,
            resource_type=resource_type,
            field=field,
            raw_path=raw_path,
        )


def _guard_projection_sync_encoding_shape(
        encoding, path, resource_type, field, raw_path, segment):
    if not path:
        return
    if isinstance(encoding, list) and len(encoding) == 2:
        kind, inner = encoding
        if kind in ("list", "set"):
            _projection_sync_shape_error(
                resource_type,
                field,
                raw_path,
                segment,
                "is a %s-typed attribute" % kind,
            )
        if kind == "map":
            if len(path) > 1:
                _guard_projection_sync_encoding_shape(
                    inner,
                    path[1:],
                    resource_type=resource_type,
                    field=field,
                    raw_path=raw_path,
                    segment=path[0],
                )
            return
        if kind == "object" and isinstance(inner, dict):
            child = path[0]
            if isinstance(child, str) and child in inner:
                _guard_projection_sync_encoding_shape(
                    inner[child],
                    path[1:],
                    resource_type=resource_type,
                    field=field,
                    raw_path=raw_path,
                    segment=child,
                )


def _projection_sync_shape_error(
        resource_type, field, raw_path, segment, detail):
    raise ProjectionError(
        "refusing to projection_sync %s %s of %s: non-terminal segment %s "
        "%s, not an object-shaped container"
        % (field, raw_path, resource_type, segment, detail)
    )


def _path_value(value, path):
    cur = value
    for segment in path:
        if not isinstance(segment, str):
            return False, None
        if not isinstance(cur, dict) or segment not in cur:
            return False, None
        cur = cur[segment]
    return True, cur


def _set_path(value, path, replacement):
    cur = value
    for segment in path[:-1]:
        if segment not in cur or cur[segment] is None:
            cur[segment] = {}
        elif not isinstance(cur[segment], dict):
            raise ProjectionError(
                "cannot projection_sync through non-object path %s"
                % _fmt_path(path)
            )
        cur = cur[segment]
    cur[path[-1]] = replacement


def _is_absent_or_empty(value):
    return (
        value is None
        or (isinstance(value, (dict, list)) and not value)
    )


def _remove_matching_leaves(value, selector, predicate, path=()):
    removed = 0
    if isinstance(value, dict):
        for key in sorted(list(value), key=str):
            child_path = path + (str(key),)
            child = value[key]
            if (
                    _is_leaf(child)
                    and paths.selector_matches(selector, child_path)
                    and predicate(child)):
                del value[key]
                removed += 1
                continue
            removed += _remove_matching_leaves(
                child, selector, predicate, child_path
            )
        return removed
    if isinstance(value, list):
        for idx in range(len(value) - 1, -1, -1):
            child_path = path + (idx,)
            child = value[idx]
            if (
                    _is_leaf(child)
                    and paths.selector_matches(selector, child_path)
                    and predicate(child)):
                del value[idx]
                removed += 1
                continue
            removed += _remove_matching_leaves(
                child, selector, predicate, child_path
            )
    return removed


def _is_leaf(value):
    return not isinstance(value, (dict, list))


def _schema_terminal_type(resource_type, path):
    block = load_resource(resource_type)["block"]
    return _schema_type_for_block(block, tuple(path), resource_top=True)


def _schema_type_for_block(block, path, resource_top):
    if not path:
        return None
    segment = path[0]
    if not isinstance(segment, str):
        return None
    cls = resource_input_attrs(block) if resource_top else classify_attributes(block)
    attrs = block.get("attributes") or {}
    if segment in cls["required"] or segment in cls["optional"]:
        return _schema_type_for_encoding(attr_type(attrs[segment]), path[1:])
    blocks = input_block_types(block)
    if segment in blocks:
        remaining = schema_paths.strip_collection_selector(path[1:])
        return _schema_type_for_block(
            blocks[segment]["block"], remaining, resource_top=False
        )
    return None


def _schema_type_for_encoding(encoding, path):
    if not path:
        return encoding
    if isinstance(encoding, list) and len(encoding) == 2:
        kind, inner = encoding
        if kind in ("list", "set"):
            return _schema_type_for_encoding(
                inner, schema_paths.strip_collection_selector(path)
            )
        if kind == "map":
            return _schema_type_for_encoding(inner, path[1:])
        if kind == "object" and isinstance(inner, dict):
            child = path[0]
            if isinstance(child, str) and child in inner:
                return _schema_type_for_encoding(inner[child], path[1:])
    return None


def _fmt_path(path):
    if not path:
        return "<root>"
    out = []
    for p in path:
        if isinstance(p, int):
            out[-1] = "%s[%d]" % (out[-1], p)
        else:
            out.append(str(p))
    return ".".join(out)
