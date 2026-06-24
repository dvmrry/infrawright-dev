"""Validator for absent/default rule metadata.

This module only parses and validates future ``absent_defaults.rules`` metadata.
It does not normalize projected values, omit values, change projection behavior,
change drift policy, or create a second omission engine.
"""


ACCEPTED_RULE_KEYS = set([
    "id",
    "provider",
    "resource_type",
    "resource_prefix",
    "path",
    "kind",
    "observed_value",
    "action",
    "evidence",
    "reason",
    "plan_path",
    "raw_api_path",
    "provider_state_path",
])

REQUIRED_RULE_KEYS = set([
    "id",
    "provider",
    "path",
    "kind",
    "action",
    "evidence",
    "reason",
])

ALLOWED_KINDS = set([
    "api_absent",
    "api_explicit_default",
    "provider_absent_placeholder",
    "terraform_schema_optional_default",
    "real_configured_falsey",
    "provider_server_side_singleton_default",
    "paid_disabled_or_api_boundary_default",
])

ALLOWED_V1_ACTIONS = set([
    "diagnostic_only",
    "manual_review_required",
    "preserve_explicit_falsey",
])

REJECTED_ACTIONS = set([
    "omit_when_absent_in_api",
    "omit_when_provider_placeholder",
    "drop_empty_values",
    "drop_falsey",
    "normalize_defaults",
])

KIND_ACTION_MATRIX = {
    "api_absent": set(["diagnostic_only", "manual_review_required"]),
    "provider_absent_placeholder": set(["diagnostic_only", "manual_review_required"]),
    "real_configured_falsey": set([
        "preserve_explicit_falsey",
        "diagnostic_only",
        "manual_review_required",
    ]),
    "paid_disabled_or_api_boundary_default": set(["diagnostic_only", "manual_review_required"]),
    "provider_server_side_singleton_default": set(["diagnostic_only", "manual_review_required"]),
    "api_explicit_default": set(["diagnostic_only", "manual_review_required"]),
    "terraform_schema_optional_default": set(["diagnostic_only", "manual_review_required"]),
}

KINDS_REQUIRING_OBSERVED_VALUE = set([
    "provider_absent_placeholder",
    "api_explicit_default",
    "terraform_schema_optional_default",
])

ACTIONS_REQUIRING_OBSERVED_VALUE = set([
    "preserve_explicit_falsey",
])

_NO_VALUE = object()


def validate_absent_default_rules(rules, sensitive_paths=None,
                                    provider_prefixes=None):
    """Validate a list of absent/default rule metadata.

    Returns the validated rules as a list, sorted by (provider, id, path) for
    internal consistency. The returned objects are shallow copies of the input
    rule objects with minimal normalization; no infrastructure values are
    transformed.

    If ``provider_prefixes`` is provided (a mapping of prefix -> provider), the
    validator also checks that ``resource_type`` and ``resource_prefix`` values
    resolve to the declared rule provider.
    """
    if rules is None:
        return []
    if not isinstance(rules, list):
        raise ValueError("absent_defaults.rules must be a list")

    canonical_sensitive = _canonicalize_sensitive_paths(sensitive_paths)

    validated = []
    seen = {}
    for idx, rule in enumerate(rules):
        validated_rule = validate_absent_default_rule(
            rule,
            idx=idx,
            sensitive_paths=canonical_sensitive,
            provider_prefixes=provider_prefixes,
        )
        identity = _rule_identity(validated_rule)
        _check_identity_conflict(identity, validated_rule, seen, idx)
        seen[identity] = validated_rule
        validated.append(validated_rule)

    _check_scope_overlaps(validated)

    return sorted(
        validated,
        key=lambda r: (r.get("provider", ""), r.get("id", ""), r.get("path", "")),
    )


def validate_absent_default_rule(rule, idx=None, sensitive_paths=None,
                                   provider_prefixes=None):
    """Validate a single absent/default rule metadata object.

    Returns the rule object unchanged (a shallow copy) after validation.
    """
    if not isinstance(rule, dict):
        raise ValueError(
            "absent_defaults rule %s must be an object" % _idx_label(idx)
        )

    item = dict(rule)
    label = _rule_label(idx, item.get("id"))

    unknown = sorted(set(item) - ACCEPTED_RULE_KEYS)
    if unknown:
        raise ValueError(
            "%s: unknown rule key %s" % (label, unknown[0])
        )

    missing = sorted(REQUIRED_RULE_KEYS - set(item))
    for key in missing:
        raise ValueError("%s: missing %s" % (label, key))

    for key in ("id", "provider", "path", "kind", "action", "evidence", "reason"):
        value = item.get(key)
        if value is None or (isinstance(value, str) and not value.strip()):
            raise ValueError("%s: missing %s" % (label, key))

    if not isinstance(item.get("id"), str) or not item["id"].strip():
        raise ValueError("%s: missing id" % label)
    if not isinstance(item.get("provider"), str) or not item["provider"].strip():
        raise ValueError("%s: missing provider" % label)
    if not isinstance(item.get("path"), str) or not item["path"].strip():
        raise ValueError("%s: missing path" % label)
    if not isinstance(item.get("kind"), str) or not item["kind"].strip():
        raise ValueError("%s: missing kind" % label)
    if not isinstance(item.get("action"), str) or not item["action"].strip():
        raise ValueError("%s: missing action" % label)
    if not isinstance(item.get("evidence"), str) or not item["evidence"].strip():
        raise ValueError("%s: missing evidence" % label)
    if not isinstance(item.get("reason"), str) or not item["reason"].strip():
        raise ValueError("%s: missing reason" % label)

    item["path"] = _canonicalize_path(item["path"], label, "path")

    kind = item["kind"].strip()
    if kind not in ALLOWED_KINDS:
        raise ValueError("%s: unknown kind %s" % (label, kind))

    action = item["action"].strip()
    if action not in ALLOWED_V1_ACTIONS:
        if action in REJECTED_ACTIONS:
            raise ValueError(
                "%s: action %s is rejected in V1" % (label, action)
            )
        raise ValueError("%s: unknown action %s" % (label, action))

    allowed_actions = KIND_ACTION_MATRIX.get(kind, set())
    if action not in allowed_actions:
        raise ValueError(
            "%s: kind %s does not allow action %s" % (label, kind, action)
        )

    _validate_resource_scope(item, label, provider_prefixes)
    _validate_observed_value(item, label)
    _validate_path_namespace(item, label)
    _validate_evidence_paths(item, label)
    _validate_sensitive_path(item, label, sensitive_paths)

    return item


def _validate_resource_scope(item, label, provider_prefixes):
    has_type = "resource_type" in item
    has_prefix = "resource_prefix" in item

    if has_type and has_prefix:
        raise ValueError(
            "%s: cannot specify both resource_type and resource_prefix" % label
        )
    if not has_type and not has_prefix:
        raise ValueError(
            "%s: missing resource scope (resource_type or resource_prefix)" % label
        )

    for key in ("resource_type", "resource_prefix"):
        if key in item:
            value = item[key]
            if not isinstance(value, str) or not value.strip():
                raise ValueError("%s: missing %s" % (label, key))

    _validate_provider_resource_match(item, label, provider_prefixes)


def _validate_observed_value(item, label):
    kind = item["kind"]
    action = item["action"]
    has_observed = "observed_value" in item

    if kind in KINDS_REQUIRING_OBSERVED_VALUE:
        if not has_observed:
            raise ValueError(
                "%s: kind %s requires observed_value" % (label, kind)
            )
    if action in ACTIONS_REQUIRING_OBSERVED_VALUE:
        if not has_observed:
            raise ValueError(
                "%s: action %s requires observed_value" % (label, action)
            )


def _validate_provider_resource_match(item, label, provider_prefixes):
    if provider_prefixes is None:
        return

    provider = item.get("provider")
    if "resource_type" in item:
        resource_type = item["resource_type"]
        resolved = _resolve_provider(resource_type, provider_prefixes)
        if resolved is None:
            raise ValueError(
                "%s: resource_type %s is not declared in provider_prefixes"
                % (label, resource_type)
            )
        if resolved != provider:
            raise ValueError(
                "%s: resource_type %s resolves to provider %s, not %s"
                % (label, resource_type, resolved, provider)
            )

    if "resource_prefix" in item:
        resource_prefix = item["resource_prefix"]
        declared = provider_prefixes.get(resource_prefix)
        if declared is None:
            raise ValueError(
                "%s: resource_prefix %s is not declared in provider_prefixes"
                % (label, resource_prefix)
            )
        if declared != provider:
            raise ValueError(
                "%s: resource_prefix %s is declared for provider %s, not %s"
                % (label, resource_prefix, declared, provider)
            )


def _resolve_provider(resource_type, provider_prefixes):
    for prefix in sorted(provider_prefixes, key=len, reverse=True):
        if resource_type.startswith(prefix):
            return provider_prefixes[prefix]
    return None


def _validate_path_namespace(item, label):
    path = item.get("path")
    if not isinstance(path, str) or not path.strip():
        raise ValueError("%s: missing path" % label)

    for key in ("raw_api_path", "provider_state_path", "plan_path"):
        if key in item and "path" not in item:
            raise ValueError(
                "%s: %s cannot replace path" % (label, key)
            )


def _validate_evidence_paths(item, label):
    for key in ("plan_path", "raw_api_path", "provider_state_path"):
        if key in item:
            value = item[key]
            if not isinstance(value, str) or not value.strip():
                raise ValueError("%s: %s must be a string" % (label, key))


def _validate_sensitive_path(item, label, sensitive_paths):
    if sensitive_paths is None:
        return
    path = item.get("path")
    if path in set(sensitive_paths):
        raise ValueError(
            "%s: path %s targets a known sensitive path" % (label, path)
        )



def _canonicalize_path(value, label, field):
    from engine import schema_paths
    if not isinstance(value, str) or not value.strip():
        raise ValueError("%s: missing %s" % (label, field))
    try:
        parsed = schema_paths.parse_report_path(value.strip())
    except Exception as exc:
        raise ValueError(
            "%s: %s has unsupported syntax %r (%s)" % (label, field, value, exc)
        )
    if any(segment == "*" for segment in parsed):
        raise ValueError(
            "%s: %s has unsupported syntax %r (bare wildcard segment)" % (
                label, field, value
            )
        )
    return schema_paths.format_path(parsed)


def _canonicalize_sensitive_paths(sensitive_paths):
    from engine import schema_paths
    if sensitive_paths is None:
        return None
    out = set()
    for path in sensitive_paths:
        try:
            out.add(schema_paths.format_path(schema_paths.parse_report_path(path)))
        except Exception:
            out.add(path)
    return out


def _check_scope_overlaps(rules):
    by_provider_path = {}
    for rule in rules:
        key = (rule.get("provider"), rule.get("path"))
        by_provider_path.setdefault(key, []).append(rule)

    for key, group in by_provider_path.items():
        types = [
            rule for rule in group
            if "resource_type" in rule
        ]
        prefixes = [
            rule for rule in group
            if "resource_prefix" in rule
        ]
        if not types or not prefixes:
            continue
        for type_rule in types:
            resource_type = type_rule["resource_type"]
            for prefix_rule in prefixes:
                prefix = prefix_rule["resource_prefix"]
                if resource_type.startswith(prefix):
                    raise ValueError(
                        "absent_defaults rule (%s): resource_type %s overlaps "
                        "resource_prefix %s for %s/%s" % (
                            type_rule.get("id", "?"),
                            resource_type,
                            prefix,
                            key[0],
                            key[1],
                        )
                    )


def _rule_identity(rule):
    provider = rule.get("provider")
    path = rule.get("path")
    if "resource_type" in rule:
        scope = ("type", rule["resource_type"])
    else:
        scope = ("prefix", rule["resource_prefix"])
    return (provider, scope, path)


def _check_identity_conflict(identity, rule, seen, idx):
    if identity not in seen:
        return

    label = _rule_label(idx, rule.get("id"))
    first = seen[identity]
    first_label = _rule_label(None, first.get("id"))
    identity_text = _format_identity(identity)

    if first.get("kind") != rule.get("kind"):
        raise ValueError(
            "%s: conflicting kind for rule %s (previous %s)" % (
                label, identity_text, first_label
            )
        )
    if first.get("action") != rule.get("action"):
        raise ValueError(
            "%s: conflicting action for rule %s (previous %s)" % (
                label, identity_text, first_label
            )
        )
    if first.get("observed_value", _NO_VALUE) != rule.get("observed_value", _NO_VALUE):
        raise ValueError(
            "%s: conflicting observed_value for rule %s (previous %s)" % (
                label, identity_text, first_label
            )
        )

    raise ValueError(
        "%s: duplicate rule for %s (previous %s)" % (
            label, identity_text, first_label
        )
    )


def _format_identity(identity):
    provider, scope, path = identity
    scope_type, scope_value = scope
    if scope_type == "type":
        return "%s/%s/%s" % (provider, scope_value, path)
    return "%s:prefix(%s)/%s" % (provider, scope_value, path)


def _rule_label(idx, ident=None):
    if ident:
        if idx is not None:
            return "absent_defaults rule %d (%s)" % (idx, ident)
        return "absent_defaults rule (%s)" % ident
    if idx is not None:
        return "absent_defaults rule %d" % idx
    return "absent_defaults rule"


def _idx_label(idx):
    if idx is None:
        return ""
    return "%d" % idx
