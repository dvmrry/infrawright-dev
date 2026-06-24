"""Validator for sensitive-required rule metadata.

This module only parses and validates future ``sensitive_required.rules`` metadata.
It does not project sensitive values, render placeholders, omit paths, change
projection behavior, change drift policy, alter advisory behavior, or change
assert-adoptable status.
"""
from engine import schema_paths


ACCEPTED_RULE_KEYS = set([
    "id",
    "provider",
    "provider_version_constraint",
    "resource_type",
    "resource_prefix",
    "path",
    "kind",
    "sensitivity",
    "structural_requirement",
    "action",
    "evidence",
    "reason",
    "raw_api_path",
    "projected_path",
    "plan_path",
])

REQUIRED_RULE_KEYS = set([
    "id",
    "provider",
    "provider_version_constraint",
    "path",
    "kind",
    "sensitivity",
    "structural_requirement",
    "action",
    "evidence",
    "reason",
])

FORBIDDEN_VALUE_CARRYING_KEYS = set([
    "value",
    "observed_value",
    "placeholder_value",
    "secret",
    "secret_value",
    "sensitive_value",
])

ALLOWED_KINDS = set([
    "sensitive_required_block",
    "sensitive_required_attribute",
    "sensitive_write_only_attribute",
    "sensitive_nested_secret",
    "sensitive_structural_placeholder_required",
])

ALLOWED_SENSITIVITIES = set([
    "sensitive_attribute",
    "sensitive_block",
    "contains_sensitive_fields",
    "write_only_sensitive",
])

ALLOWED_STRUCTURAL_REQUIREMENTS = set([
    "block_required_for_valid_config",
    "attribute_required_for_valid_config",
    "one_of_block_required",
    "parent_block_required",
    "operator_input_required_for_valid_config",
])

ALLOWED_V1_ACTIONS = set([
    "diagnostic_only",
    "manual_review_required",
])

RESERVED_ACTIONS = set([
    "render_placeholder_block",
    "render_placeholder_attribute",
    "preserve_structure_without_secret_candidate",
    "operator_input_required_candidate",
])

FORBIDDEN_ACTIONS = set([
    "project_sensitive",
    "copy_sensitive_from_state",
    "guess_secret",
    "suppress_sensitive_drift",
    "omit_sensitive_block",
    "accept_sensitive_unknown",
    "downgrade_assert_adoptable",
    "render_fake_secret",
])

KIND_SENSITIVITY_MATRIX = {
    "sensitive_required_block": set([
        "sensitive_block",
        "contains_sensitive_fields",
    ]),
    "sensitive_required_attribute": set([
        "sensitive_attribute",
    ]),
    "sensitive_write_only_attribute": set([
        "write_only_sensitive",
    ]),
    "sensitive_nested_secret": set([
        "contains_sensitive_fields",
    ]),
    "sensitive_structural_placeholder_required": set([
        "sensitive_block",
        "contains_sensitive_fields",
        "write_only_sensitive",
    ]),
}

KIND_STRUCTURAL_REQUIREMENT_MATRIX = {
    "sensitive_required_block": set([
        "block_required_for_valid_config",
        "one_of_block_required",
        "parent_block_required",
    ]),
    "sensitive_required_attribute": set([
        "attribute_required_for_valid_config",
    ]),
    "sensitive_write_only_attribute": set([
        "attribute_required_for_valid_config",
        "operator_input_required_for_valid_config",
    ]),
    "sensitive_nested_secret": set([
        "parent_block_required",
        "block_required_for_valid_config",
    ]),
    "sensitive_structural_placeholder_required": set([
        "block_required_for_valid_config",
        "parent_block_required",
        "operator_input_required_for_valid_config",
    ]),
}

_NO_VALUE = object()


def validate_sensitive_required_rules(rules, sensitive_paths=None,
                                     provider_prefixes=None):
    """Validate a list of sensitive-required rule metadata.

    Returns the validated rules as a list, sorted by (provider, id, path) for
    internal consistency. The returned objects are shallow copies with
    canonicalized `path` and stripped `provider_version_constraint`. No
    sensitive values are projected or transformed.
    """
    if rules is None:
        return []
    if not isinstance(rules, list):
        raise ValueError("sensitive_required.rules must be a list")

    canonical_sensitive = _canonicalize_sensitive_paths(sensitive_paths)

    validated = []
    seen = {}
    for idx, rule in enumerate(rules):
        validated_rule = validate_sensitive_required_rule(
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


def validate_sensitive_required_rule(rule, idx=None, sensitive_paths=None,
                                      provider_prefixes=None):
    """Validate a single sensitive-required rule metadata object.

    Returns a shallow copy of the rule with canonicalized `path` and stripped
    `provider_version_constraint`. No sensitive values are projected or
    transformed.
    """
    if not isinstance(rule, dict):
        raise ValueError(
            "sensitive_required rule %s must be an object" % _idx_label(idx)
        )

    item = dict(rule)
    label = _rule_label(idx, item.get("id"))

    _validate_keys(item, label)

    missing = sorted(REQUIRED_RULE_KEYS - set(item))
    for key in missing:
        raise ValueError("%s: missing %s" % (label, key))

    for key in ("id", "provider", "path", "kind", "sensitivity",
                "structural_requirement", "action", "evidence", "reason"):
        value = item.get(key)
        if value is None or not isinstance(value, str) or not value.strip():
            raise ValueError("%s: %s must be a string" % (label, key))

    item["provider"] = item["provider"].strip()
    item["id"] = item["id"].strip()
    item["kind"] = item["kind"].strip()
    item["sensitivity"] = item["sensitivity"].strip()
    item["structural_requirement"] = item["structural_requirement"].strip()
    item["action"] = item["action"].strip()
    item["evidence"] = item["evidence"].strip()
    item["reason"] = item["reason"].strip()

    version = item.get("provider_version_constraint")
    if not isinstance(version, str) or not version.strip():
        raise ValueError("%s: missing provider_version_constraint" % label)
    item["provider_version_constraint"] = version.strip()

    item["path"] = _canonicalize_path(item["path"], label, "path")

    kind = item["kind"]
    if kind not in ALLOWED_KINDS:
        raise ValueError("%s: unknown kind %s" % (label, kind))

    sensitivity = item["sensitivity"]
    if sensitivity not in ALLOWED_SENSITIVITIES:
        raise ValueError("%s: unknown sensitivity %s" % (label, sensitivity))

    structural_requirement = item["structural_requirement"]
    if structural_requirement not in ALLOWED_STRUCTURAL_REQUIREMENTS:
        raise ValueError(
            "%s: unknown structural_requirement %s" % (label, structural_requirement)
        )

    action = item["action"]
    if action not in ALLOWED_V1_ACTIONS:
        if action in RESERVED_ACTIONS:
            raise ValueError("%s: action %s is rejected in V1" % (label, action))
        if action in FORBIDDEN_ACTIONS:
            raise ValueError("%s: action %s is forbidden" % (label, action))
        raise ValueError("%s: unknown action %s" % (label, action))

    _validate_resource_scope(item, label, provider_prefixes)
    _validate_evidence_paths(item, label)
    _validate_sensitive_path(item, label, sensitive_paths)
    _validate_matrix(item, label)

    return item


def _validate_keys(item, label):
    forbidden = sorted(set(item) & FORBIDDEN_VALUE_CARRYING_KEYS)
    if forbidden:
        raise ValueError(
            "%s: forbidden value-carrying key %s" % (label, forbidden[0])
        )
    unknown = sorted(set(item) - ACCEPTED_RULE_KEYS)
    if unknown:
        raise ValueError("%s: unknown rule key %s" % (label, unknown[0]))


def _canonicalize_path(value, label, field):
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
                raise ValueError("%s: %s must be a string" % (label, key))

    _validate_provider_resource_match(item, label, provider_prefixes)


def _validate_provider_resource_match(item, label, provider_prefixes):
    if provider_prefixes is None:
        return

    provider = item.get("provider")
    if "resource_type" in item:
        resource_type = item["resource_type"].strip()
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
        resource_prefix = item["resource_prefix"].strip()
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


def _validate_evidence_paths(item, label):
    for key in ("raw_api_path", "projected_path", "plan_path"):
        if key in item:
            value = item[key]
            if not isinstance(value, str) or not value.strip():
                raise ValueError("%s: %s must be a non-empty string" % (label, key))

    for key in ("raw_api_path", "projected_path", "plan_path"):
        if key in item and "path" not in item:
            raise ValueError("%s: %s cannot replace path" % (label, key))


def _validate_sensitive_path(item, label, sensitive_paths):
    if sensitive_paths is None:
        return
    path = item.get("path")
    if path not in set(sensitive_paths):
        raise ValueError(
            "%s: path %s is not in supplied sensitive_paths" % (label, path)
        )


def _validate_matrix(item, label):
    kind = item["kind"]
    sensitivity = item["sensitivity"]
    structural_requirement = item["structural_requirement"]

    allowed_sensitivities = KIND_SENSITIVITY_MATRIX.get(kind, set())
    if sensitivity not in allowed_sensitivities:
        raise ValueError(
            "%s: kind %s does not allow sensitivity %s" % (label, kind, sensitivity)
        )

    allowed_structural = KIND_STRUCTURAL_REQUIREMENT_MATRIX.get(kind, set())
    if structural_requirement not in allowed_structural:
        raise ValueError(
            "%s: kind %s does not allow structural_requirement %s"
            % (label, kind, structural_requirement)
        )


def _canonicalize_sensitive_paths(sensitive_paths):
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
    by_provider_version_path = {}
    for rule in rules:
        key = (rule.get("provider"), rule.get("provider_version_constraint"), rule.get("path"))
        by_provider_version_path.setdefault(key, []).append(rule)

    for key, group in by_provider_version_path.items():
        types = [rule for rule in group if "resource_type" in rule]
        prefixes = [rule for rule in group if "resource_prefix" in rule]
        if not types or not prefixes:
            continue
        for type_rule in types:
            resource_type = type_rule["resource_type"].strip()
            for prefix_rule in prefixes:
                prefix = prefix_rule["resource_prefix"].strip()
                if resource_type.startswith(prefix):
                    raise ValueError(
                        "sensitive_required rule (%s): resource_type %s overlaps "
                        "resource_prefix %s for %s/%s/%s" % (
                            type_rule.get("id", "?"),
                            resource_type,
                            prefix,
                            key[0],
                            key[1],
                            key[2],
                        )
                    )


def _rule_identity(rule):
    provider = rule.get("provider")
    version = rule.get("provider_version_constraint")
    path = rule.get("path")
    if "resource_type" in rule:
        scope = ("type", rule["resource_type"].strip())
    else:
        scope = ("prefix", rule["resource_prefix"].strip())
    return (provider, version, scope, path)


def _check_identity_conflict(identity, rule, seen, idx):
    if identity not in seen:
        return

    label = _rule_label(idx, rule.get("id"))
    first = seen[identity]
    first_label = _rule_label(None, first.get("id"))
    identity_text = _format_identity(identity)

    for key in ("kind", "sensitivity", "structural_requirement", "action",
                "evidence", "raw_api_path", "projected_path", "plan_path"):
        if first.get(key, _NO_VALUE) != rule.get(key, _NO_VALUE):
            raise ValueError(
                "%s: conflicting %s for rule %s (previous %s)" % (
                    label, key, identity_text, first_label
                )
            )

    raise ValueError(
        "%s: duplicate rule for %s (previous %s)" % (
            label, identity_text, first_label
        )
    )


def _format_identity(identity):
    provider, version, scope, path = identity
    scope_type, scope_value = scope
    if scope_type == "type":
        return "%s/%s/%s/%s" % (provider, version, scope_value, path)
    return "%s/%s:prefix(%s)/%s" % (provider, version, scope_value, path)


def _rule_label(idx, ident=None):
    if ident:
        if idx is not None:
            return "sensitive_required rule %d (%s)" % (idx, ident)
        return "sensitive_required rule (%s)" % ident
    if idx is not None:
        return "sensitive_required rule %d" % idx
    return "sensitive_required rule"


def _idx_label(idx):
    if idx is None:
        return ""
    return "%d" % idx
