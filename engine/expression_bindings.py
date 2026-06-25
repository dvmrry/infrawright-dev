"""Terraform expression bindings for generated env roots.

Bindings replace selected literal item paths with Terraform expressions at the
root composition layer. They never fetch or store secret values.
"""
import json
import re


PATH_SEGMENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
EXACT_VAR_EXPR = re.compile(r"^var\.([A-Za-z_][A-Za-z0-9_]*)$")
IDENT = r"[A-Za-z_][A-Za-z0-9_]*"
SELECTOR_TAIL = r'(?:\.%s|\["[A-Za-z_][A-Za-z0-9_-]*"\])*' % IDENT
ALLOWED_EXPRS = (
    re.compile(r"^var\.%s$" % IDENT),
    re.compile(r"^local\.%s$" % IDENT),
    re.compile(r"^data\.%s\.%s%s$" % (IDENT, IDENT, SELECTOR_TAIL)),
    re.compile(r"^module\.%s%s$" % (IDENT, SELECTOR_TAIL)),
)
CONTROL_CHARS = re.compile(r"[\x00-\x1f\x7f]")


class HclExpression(object):
    """Terraform expression sentinel.

    This is deliberately not a string subclass so expression values cannot be
    confused with literal strings during rendering.
    """

    def __init__(self, expression):
        self.expression = _validate_expression(expression, "HclExpression")

    def __repr__(self):
        return "HclExpression(%r)" % self.expression

    def __eq__(self, other):
        return (
            isinstance(other, HclExpression)
            and self.expression == other.expression
        )


def _hcl_string(value):
    return json.dumps(value)


def _hcl_key(value):
    if PATH_SEGMENT.match(value):
        return value
    return _hcl_string(value)


def _validate_expression(expr, context):
    if not isinstance(expr, str) or not expr:
        raise ValueError("%s expression must be a non-empty string" % context)
    if CONTROL_CHARS.search(expr):
        raise ValueError("%s expression must not contain control characters" % context)
    if not any(pattern.match(expr) for pattern in ALLOWED_EXPRS):
        raise ValueError(
            "%s expression %r is outside the v1 allowlist "
            "(allowed roots: var., local., data., module.)" % (context, expr)
        )
    return expr


def _parse_path(path, context):
    if not isinstance(path, str) or not path:
        raise ValueError("%s path must be a non-empty dotted string" % context)
    parts = path.split(".")
    for part in parts:
        if not PATH_SEGMENT.match(part):
            raise ValueError(
                "%s path %r has unsupported segment %r; v1 supports "
                "dotted object attributes only" % (context, path, part)
            )
    return tuple(parts)


def _parse_binding(address, path, value, resource_type):
    context = "%s.%s" % (address, path)
    if not isinstance(value, dict):
        raise ValueError("%s binding must be an object" % context)
    allowed = set(("expression", "sensitive", "reason"))
    unknown = sorted(set(value) - allowed)
    if unknown:
        raise ValueError(
            "%s binding has unknown key(s): %s" % (context, ", ".join(unknown))
        )
    if "expression" not in value:
        raise ValueError("%s binding is missing expression" % context)
    sensitive = value.get("sensitive", False)
    if not isinstance(sensitive, bool):
        raise ValueError("%s sensitive must be a boolean" % context)
    reason = value.get("reason")
    if reason is not None and not isinstance(reason, str):
        raise ValueError("%s reason must be a string when present" % context)
    prefix = resource_type + "."
    if not isinstance(address, str) or not address.startswith(prefix):
        raise ValueError(
            "%s address must be %s<key>" % (context, prefix)
        )
    key = address[len(prefix):]
    if not key:
        raise ValueError("%s address has empty resource key" % context)
    if CONTROL_CHARS.search(key):
        raise ValueError("%s resource key must not contain control characters" % context)
    path_parts = _parse_path(path, context)
    return {
        "address": address,
        "key": key,
        "path": ".".join(path_parts),
        "path_parts": path_parts,
        "expression": _validate_expression(value["expression"], context),
        "sensitive": sensitive,
        "reason": reason,
    }


def parse_bindings(data, resource_type):
    """Return sorted binding records for one Terraform resource type."""
    if data is None:
        return []
    if not isinstance(data, dict):
        raise ValueError("expression bindings must be a JSON object")
    unknown = sorted(set(data) - set(("resources",)))
    if unknown:
        raise ValueError(
            "expression bindings have unknown top-level key(s): %s"
            % ", ".join(unknown)
        )
    resources = data.get("resources", {})
    if not isinstance(resources, dict):
        raise ValueError("expression bindings resources must be an object")
    bindings = []
    seen = set()
    for address, paths in sorted(resources.items()):
        if not isinstance(paths, dict):
            raise ValueError("%s bindings must be an object" % address)
        for path, binding in sorted(paths.items()):
            parsed = _parse_binding(address, path, binding, resource_type)
            key = (parsed["key"], parsed["path"])
            if key in seen:
                raise ValueError(
                    "duplicate expression binding for %s.%s"
                    % (parsed["address"], parsed["path"])
                )
            seen.add(key)
            bindings.append(parsed)
    return bindings


def load_bindings(path, resource_type):
    with open(path, encoding="utf-8") as f:
        return parse_bindings(json.load(f), resource_type)


def variable_declarations(bindings):
    """Return {variable_name: sensitive_bool} for exact var.foo bindings."""
    variables = {}
    for binding in bindings:
        match = EXACT_VAR_EXPR.match(binding["expression"])
        if not match:
            continue
        name = match.group(1)
        variables[name] = variables.get(name, False) or binding["sensitive"]
    return dict(sorted(variables.items()))


def _binding_tree(bindings):
    by_key = {}
    for binding in bindings:
        cur = by_key.setdefault(binding["key"], {})
        parts = list(binding["path_parts"])
        for part in parts[:-1]:
            existing = cur.get(part)
            if existing is None:
                existing = {}
                cur[part] = existing
            if not isinstance(existing, dict):
                raise ValueError(
                    "conflicting expression binding below %s.%s"
                    % (binding["address"], binding["path"])
                )
            cur = existing
        leaf = parts[-1]
        if leaf in cur:
            raise ValueError(
                "conflicting expression binding below %s.%s"
                % (binding["address"], binding["path"])
            )
        cur[leaf] = binding["expression"]
    return by_key


def apply_bindings(items, bindings):
    """Return a copy of items with exact path leaves replaced by expressions.

    V1 supports object paths only. Intermediate parent objects must already
    exist, and the target leaf must already exist. Arbitrary object
    construction is not supported at env-root composition time.
    """
    out = json.loads(json.dumps(items))
    for binding in bindings or []:
        key = binding["key"]
        if key not in out:
            raise ValueError(
                "expression binding references unknown resource address %s"
                % binding["address"]
            )
        cur = out[key]
        parts = list(binding["path_parts"])
        for part in parts[:-1]:
            if not isinstance(cur, dict) or part not in cur:
                raise ValueError(
                    "expression binding %s.%s has missing parent path"
                    % (binding["address"], binding["path"])
                )
            cur = cur[part]
        if not isinstance(cur, dict):
            raise ValueError(
                "expression binding %s.%s parent is not an object"
                % (binding["address"], binding["path"])
            )
        if parts[-1] not in cur:
            raise ValueError(
                "expression binding %s.%s has missing target leaf"
                % (binding["address"], binding["path"])
            )
        cur[parts[-1]] = HclExpression(binding["expression"])
    return out


def render_hcl_value(value, indent=0):
    """Render a small native-HCL value fragment for tests and overlays."""
    if isinstance(value, HclExpression):
        return value.expression
    if isinstance(value, str):
        return _hcl_string(value)
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "null"
    if isinstance(value, (int, float)):
        return repr(value)
    if isinstance(value, list):
        return "[%s]" % ", ".join(render_hcl_value(v, indent) for v in value)
    if isinstance(value, dict):
        pad = " " * indent
        child_pad = " " * (indent + 2)
        lines = ["{"]
        for key, child in sorted(value.items()):
            lines.append(
                "%s%s = %s"
                % (child_pad, _hcl_key(key), render_hcl_value(child, indent + 2))
            )
        lines.append("%s}" % pad)
        return "\n".join(lines)
    raise TypeError("cannot render %r as HCL" % (value,))


def to_tf_json_value(value):
    """Convert values for .tf.json config rendering.

    Terraform JSON syntax represents expressions as interpolation-only strings.
    Literal strings remain literal strings.
    """
    if isinstance(value, HclExpression):
        return "${%s}" % value.expression
    if isinstance(value, list):
        return [to_tf_json_value(v) for v in value]
    if isinstance(value, dict):
        return dict((k, to_tf_json_value(v)) for k, v in sorted(value.items()))
    return value


def _render_merge(base_expr, tree, indent):
    pad = " " * indent
    inner_pad = " " * (indent + 2)
    lines = ["merge(%s, {" % base_expr]
    for name, value in sorted(tree.items()):
        if isinstance(value, dict):
            child_ref = "%s.%s" % (base_expr, name)
            child_base = "try(%s, null) == null ? {} : %s" % (
                child_ref,
                child_ref,
            )
            child_expr = _render_merge(child_base, value, indent + 2)
            lines.append("%s%s = %s" % (inner_pad, name, child_expr))
        else:
            lines.append("%s%s = %s" % (inner_pad, name, value))
    lines.append("%s})" % pad)
    return "\n".join(lines)


def _render_key_binding(key, tree, indent):
    base = 'var.items[%s]' % _hcl_string(key)
    return _render_merge(base, tree, indent)


def render_hcl(bindings):
    """Render Terraform HCL that merges expression bindings into var.items."""
    bindings = list(bindings or [])
    if not bindings:
        return ""
    variables = variable_declarations(bindings)
    sections = [
        "# GENERATED by engine.gen_env from expression bindings — do not edit.",
        "# Regenerate: make gen-env TENANT=<tenant>",
        "",
    ]
    for name, sensitive in sorted(variables.items()):
        sections.append('variable "%s" {' % name)
        sections.append("  type = string")
        if sensitive:
            sections.append("  sensitive = true")
        sections.append("}")
        sections.append("")

    sections.append("locals {")
    sections.append("  infrawright_expression_bound_items = merge(var.items, {")
    for key, tree in sorted(_binding_tree(bindings).items()):
        rendered = _render_key_binding(key, tree, 4)
        rendered = rendered.replace("\n", "\n    ")
        sections.append("    %s = %s" % (_hcl_string(key), rendered))
    sections.append("  })")
    sections.append("}")
    sections.append("")
    return "\n".join(sections)
