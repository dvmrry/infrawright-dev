"""Project provider-observed state values into generated module input values."""
from engine.tfschema import (
    block_is_single,
    classify_attributes,
    input_block_types,
    load_resource,
    resource_input_attrs,
)


class ProjectionError(ValueError):
    pass


def project_item(resource_type, state_values, sensitive_values=None, policy=None):
    block = load_resource(resource_type)["block"]
    sensitive_values = sensitive_values or {}
    return _project_block(
        state_values,
        sensitive_values,
        block,
        path=(),
        resource_top=True,
        resource_type=resource_type,
        policy=policy,
    )


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
