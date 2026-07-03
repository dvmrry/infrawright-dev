"""Spec-driven validation and helpers for diagnostic metadata lanes.

The lane rules are diagnostic-only metadata. This module validates their
shared shape and preserves each lane's public vocabulary, message text, and
identity semantics through plain spec dictionaries.
"""

ABSENT_DEFAULTS_ACCEPTED_RULE_KEYS = set([
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

ABSENT_DEFAULTS_REQUIRED_RULE_KEYS = set([
    "id",
    "provider",
    "path",
    "kind",
    "action",
    "evidence",
    "reason",
])

ABSENT_DEFAULTS_ALLOWED_KINDS = set([
    "api_absent",
    "api_explicit_default",
    "provider_absent_placeholder",
    "terraform_schema_optional_default",
    "real_configured_falsey",
    "provider_server_side_singleton_default",
    "paid_disabled_or_api_boundary_default",
])

ABSENT_DEFAULTS_ALLOWED_V1_ACTIONS = set([
    "diagnostic_only",
    "manual_review_required",
    "preserve_explicit_falsey",
])

ABSENT_DEFAULTS_REJECTED_ACTIONS = set([
    "omit_when_absent_in_api",
    "omit_when_provider_placeholder",
    "drop_empty_values",
    "drop_falsey",
    "normalize_defaults",
])

ABSENT_DEFAULTS_KIND_ACTION_MATRIX = {
    "api_absent": set(["diagnostic_only", "manual_review_required"]),
    "provider_absent_placeholder": set([
        "diagnostic_only",
        "manual_review_required",
    ]),
    "real_configured_falsey": set([
        "preserve_explicit_falsey",
        "diagnostic_only",
        "manual_review_required",
    ]),
    "paid_disabled_or_api_boundary_default": set([
        "diagnostic_only",
        "manual_review_required",
    ]),
    "provider_server_side_singleton_default": set([
        "diagnostic_only",
        "manual_review_required",
    ]),
    "api_explicit_default": set(["diagnostic_only", "manual_review_required"]),
    "terraform_schema_optional_default": set([
        "diagnostic_only",
        "manual_review_required",
    ]),
}

ABSENT_DEFAULTS_KINDS_REQUIRING_OBSERVED_VALUE = set([
    "provider_absent_placeholder",
    "api_explicit_default",
    "terraform_schema_optional_default",
])

ABSENT_DEFAULTS_ACTIONS_REQUIRING_OBSERVED_VALUE = set([
    "preserve_explicit_falsey",
])


DYNAMIC_SCHEMA_ACCEPTED_RULE_KEYS = set([
    "id",
    "provider",
    "provider_version_constraint",
    "resource_type",
    "resource_prefix",
    "path",
    "kind",
    "ownership",
    "action",
    "evidence",
    "reason",
    "raw_api_path",
    "projected_path",
    "plan_path",
])

DYNAMIC_SCHEMA_REQUIRED_RULE_KEYS = set([
    "id",
    "provider",
    "provider_version_constraint",
    "path",
    "kind",
    "ownership",
    "action",
    "evidence",
    "reason",
])

DYNAMIC_SCHEMA_ALLOWED_KINDS = set([
    "provider_state_only",
    "provider_computed_map",
    "freeform_object",
    "opaque_json_blob",
    "map_key_discovered_after_import",
    "unstable_collection_identity",
    "schema_unknown_but_provider_observed",
    "raw_api_only_provider_blind",
    "provider_observed_projection_unsafe",
])

DYNAMIC_SCHEMA_ALLOWED_OWNERSHIPS = set([
    "user_owned",
    "provider_computed",
    "server_owned",
    "unknown",
])

DYNAMIC_SCHEMA_ALLOWED_V1_ACTIONS = set([
    "diagnostic_only",
    "manual_review_required",
])

DYNAMIC_SCHEMA_REJECTED_ACTIONS = set([
    "preserve_observed_scalar",
    "projection_omit_candidate",
])

DYNAMIC_SCHEMA_KIND_OWNERSHIP_MATRIX = {
    "provider_state_only": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "provider_computed_map": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "freeform_object": set([
        "user_owned",
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "opaque_json_blob": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "map_key_discovered_after_import": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "unstable_collection_identity": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "schema_unknown_but_provider_observed": set([
        "user_owned",
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
    "raw_api_only_provider_blind": set(["unknown"]),
    "provider_observed_projection_unsafe": set([
        "provider_computed",
        "server_owned",
        "unknown",
    ]),
}


SENSITIVE_REQUIRED_ACCEPTED_RULE_KEYS = set([
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

SENSITIVE_REQUIRED_REQUIRED_RULE_KEYS = set([
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

SENSITIVE_REQUIRED_FORBIDDEN_VALUE_CARRYING_KEYS = set([
    "value",
    "observed_value",
    "placeholder_value",
    "secret",
    "secret_value",
    "sensitive_value",
])

SENSITIVE_REQUIRED_ALLOWED_KINDS = set([
    "sensitive_required_block",
    "sensitive_required_attribute",
    "sensitive_write_only_attribute",
    "sensitive_nested_secret",
    "sensitive_structural_placeholder_required",
])

SENSITIVE_REQUIRED_ALLOWED_SENSITIVITIES = set([
    "sensitive_attribute",
    "sensitive_block",
    "contains_sensitive_fields",
    "write_only_sensitive",
])

SENSITIVE_REQUIRED_ALLOWED_STRUCTURAL_REQUIREMENTS = set([
    "block_required_for_valid_config",
    "attribute_required_for_valid_config",
    "one_of_block_required",
    "parent_block_required",
    "operator_input_required_for_valid_config",
])

SENSITIVE_REQUIRED_ALLOWED_V1_ACTIONS = set([
    "diagnostic_only",
    "manual_review_required",
])

SENSITIVE_REQUIRED_RESERVED_ACTIONS = set([
    "render_placeholder_block",
    "render_placeholder_attribute",
    "preserve_structure_without_secret_candidate",
    "operator_input_required_candidate",
])

SENSITIVE_REQUIRED_FORBIDDEN_ACTIONS = set([
    "project_sensitive",
    "copy_sensitive_from_state",
    "guess_secret",
    "suppress_sensitive_drift",
    "omit_sensitive_block",
    "accept_sensitive_unknown",
    "downgrade_assert_adoptable",
    "render_fake_secret",
])

SENSITIVE_REQUIRED_KIND_SENSITIVITY_MATRIX = {
    "sensitive_required_block": set([
        "sensitive_block",
        "contains_sensitive_fields",
    ]),
    "sensitive_required_attribute": set(["sensitive_attribute"]),
    "sensitive_write_only_attribute": set(["write_only_sensitive"]),
    "sensitive_nested_secret": set(["contains_sensitive_fields"]),
    "sensitive_structural_placeholder_required": set([
        "sensitive_block",
        "contains_sensitive_fields",
        "write_only_sensitive",
    ]),
}

SENSITIVE_REQUIRED_KIND_STRUCTURAL_REQUIREMENT_MATRIX = {
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


def validate_rules(spec, rules, sensitive_paths=None, provider_prefixes=None):
    """Validate a list of rule metadata for one lane spec."""
    if rules is None:
        return []
    if not isinstance(rules, list):
        raise ValueError(spec["list_error"])

    canonical_sensitive = _canonicalize_sensitive_paths(sensitive_paths)

    validated = []
    seen = {}
    for idx, rule in enumerate(rules):
        validated_rule = validate_rule(
            spec,
            rule,
            idx=idx,
            sensitive_paths=canonical_sensitive,
            provider_prefixes=provider_prefixes,
        )
        identity = _rule_identity(spec, validated_rule)
        _check_identity_conflict(spec, identity, validated_rule, seen, idx)
        seen[identity] = validated_rule
        validated.append(validated_rule)

    _check_scope_overlaps(spec, validated)

    return sorted(
        validated,
        key=lambda r: (r.get("provider", ""), r.get("id", ""), r.get("path", "")),
    )


def validate_rule(spec, rule, idx=None, sensitive_paths=None,
                  provider_prefixes=None):
    """Validate a single rule metadata object for one lane spec."""
    if not isinstance(rule, dict):
        raise ValueError(
            "%s %s must be an object" % (spec["label"], _idx_label(idx))
        )

    item = dict(rule)
    label = _rule_label(spec, idx, item.get("id"))

    _validate_keys(spec, item, label)
    _validate_required_keys(spec, item, label)
    _validate_string_keys(spec, item, label)
    _strip_declared_keys(spec, item)
    _validate_version(spec, item, label)

    item["path"] = _canonicalize_path(item["path"], label, "path")

    _validate_enums(spec, item, label)
    _validate_action(spec, item, label)

    if spec["matrix_position"] == "before_resource":
        _validate_matrices(spec, item, label)

    _validate_resource_scope(spec, item, label, provider_prefixes)

    for extra in spec["extra_checks"]:
        extra(item, label)

    _validate_evidence_paths(spec, item, label)
    for key in spec["canonicalized_evidence_keys"]:
        if key in item:
            item[key] = _canonicalize_path(item[key], label, key)

    _validate_sensitive_path(spec, item, label, sensitive_paths)

    if spec["matrix_position"] == "after_sensitive":
        _validate_matrices(spec, item, label)

    return item


def rule_matches(rule, provider, resource_type):
    if rule.get("provider") != provider:
        return False
    if "resource_type" in rule:
        return rule["resource_type"] == resource_type
    prefix = rule.get("resource_prefix")
    return bool(prefix and resource_type.startswith(prefix))


def rule_plan_path(rule):
    from engine import schema_paths

    path = rule.get("plan_path") or rule.get("path")
    try:
        return schema_paths.format_path(schema_paths.parse_report_path(path))
    except Exception:
        return path


def _validate_keys(spec, item, label):
    forbidden_keys = spec["forbidden_keys"]
    if forbidden_keys:
        forbidden = sorted(set(item) & forbidden_keys[0])
        if forbidden:
            raise ValueError(forbidden_keys[1] % (label, forbidden[0]))

    unknown = sorted(set(item) - spec["accepted_keys"])
    if unknown:
        raise ValueError("%s: unknown rule key %s" % (label, unknown[0]))


def _validate_required_keys(spec, item, label):
    missing = sorted(spec["required_keys"] - set(item))
    for key in missing:
        raise ValueError("%s: missing %s" % (label, key))


def _validate_string_keys(spec, item, label):
    mode = spec["string_error_mode"]
    for key in spec["string_keys"]:
        value = item.get(key)
        if mode == "must_be_string":
            if value is None or not isinstance(value, str) or not value.strip():
                raise ValueError("%s: %s must be a string" % (label, key))
            continue
        if mode == "missing_for_non_string":
            if value is None or not isinstance(value, str) or not value.strip():
                raise ValueError("%s: missing %s" % (label, key))
            continue
        if value is None or (isinstance(value, str) and not value.strip()):
            raise ValueError("%s: missing %s" % (label, key))
        if not isinstance(value, str):
            raise ValueError("%s: %s must be a string" % (label, key))


def _strip_declared_keys(spec, item):
    for key in spec["strip_keys"]:
        if key in item and isinstance(item[key], str):
            item[key] = item[key].strip()


def _validate_version(spec, item, label):
    if not spec["requires_version"]:
        return
    version = item.get("provider_version_constraint")
    if not isinstance(version, str) or not version.strip():
        raise ValueError("%s: missing provider_version_constraint" % label)
    item["provider_version_constraint"] = version.strip()


def _validate_enums(spec, item, label):
    for field, allowed in spec["enums"]:
        value = _field_value(item, field)
        if value not in allowed:
            raise ValueError("%s: unknown %s %s" % (label, field, value))
        if field in spec["normalize_enum_keys"]:
            item[field] = value


def _validate_action(spec, item, label):
    action = _field_value(item, "action")
    if action not in spec["action_allowed"]:
        rejected = spec["action_rejected"]
        if rejected and action in rejected[0]:
            raise ValueError(rejected[1] % (label, action))
        forbidden = spec["action_forbidden"]
        if forbidden and action in forbidden[0]:
            raise ValueError(forbidden[1] % (label, action))
        raise ValueError("%s: unknown action %s" % (label, action))
    if "action" in spec["normalize_enum_keys"]:
        item["action"] = action


def _validate_resource_scope(spec, item, label, provider_prefixes):
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
                if spec["scope_error_mode"] == "must_be_string":
                    raise ValueError("%s: %s must be a string" % (label, key))
                raise ValueError("%s: missing %s" % (label, key))

    _validate_provider_resource_match(spec, item, label, provider_prefixes)


def _validate_provider_resource_match(spec, item, label, provider_prefixes):
    if provider_prefixes is None:
        return

    provider = item.get("provider")
    if "resource_type" in item:
        resource_type = _scope_value(spec, item, "resource_type")
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
        resource_prefix = _scope_value(spec, item, "resource_prefix")
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


def _validate_evidence_paths(spec, item, label):
    for key in spec["evidence_path_keys"]:
        if key in item:
            value = item[key]
            if not isinstance(value, str) or not value.strip():
                raise ValueError(spec["evidence_path_error"] % (label, key))

    for key in spec["evidence_path_keys"]:
        if key in item and "path" not in item:
            raise ValueError("%s: %s cannot replace path" % (label, key))


def _validate_sensitive_path(spec, item, label, sensitive_paths):
    if sensitive_paths is None:
        return
    path = item.get("path")
    if spec["sensitive_polarity"] == "require":
        if path not in set(sensitive_paths):
            raise ValueError(spec["sensitive_path_error"] % (label, path))
        return
    if path in set(sensitive_paths):
        raise ValueError(spec["sensitive_path_error"] % (label, path))


def _validate_matrices(spec, item, label):
    for field_a, field_b, matrix, template in spec["matrices"]:
        value_a = _field_value(item, field_a)
        value_b = _field_value(item, field_b)
        allowed = matrix.get(value_a, set())
        if value_b not in allowed:
            raise ValueError(template % (label, value_a, value_b))


def _validate_observed_value(item, label):
    kind = item["kind"]
    action = item["action"]
    has_observed = "observed_value" in item

    if kind in ABSENT_DEFAULTS_KINDS_REQUIRING_OBSERVED_VALUE:
        if not has_observed:
            raise ValueError(
                "%s: kind %s requires observed_value" % (label, kind)
            )
    if action in ABSENT_DEFAULTS_ACTIONS_REQUIRING_OBSERVED_VALUE:
        if not has_observed:
            raise ValueError(
                "%s: action %s requires observed_value" % (label, action)
            )


def _validate_path_namespace(item, label):
    path = item.get("path")
    if not isinstance(path, str) or not path.strip():
        raise ValueError("%s: missing path" % label)

    for key in ("raw_api_path", "provider_state_path", "plan_path"):
        if key in item and "path" not in item:
            raise ValueError(
                "%s: %s cannot replace path" % (label, key)
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


def _check_scope_overlaps(spec, rules):
    by_key = {}
    for rule in rules:
        key = _overlap_key(spec, rule)
        by_key.setdefault(key, []).append(rule)

    for key, group in by_key.items():
        types = [rule for rule in group if "resource_type" in rule]
        prefixes = [rule for rule in group if "resource_prefix" in rule]
        if not types or not prefixes:
            continue
        for type_rule in types:
            resource_type = type_rule["resource_type"].strip()
            for prefix_rule in prefixes:
                prefix = prefix_rule["resource_prefix"].strip()
                if resource_type.startswith(prefix):
                    values = (
                        type_rule.get("id", "?"),
                        resource_type,
                        prefix,
                    ) + tuple(key)
                    raise ValueError(spec["overlap_error"] % values)


def _overlap_key(spec, rule):
    if spec["requires_version"]:
        return (
            rule.get("provider"),
            rule.get("provider_version_constraint"),
            rule.get("path"),
        )
    return (rule.get("provider"), rule.get("path"))


def _rule_identity(spec, rule):
    provider = rule.get("provider")
    path = rule.get("path")
    if "resource_type" in rule:
        scope = ("type", rule["resource_type"].strip())
    else:
        scope = ("prefix", rule["resource_prefix"].strip())
    if spec["requires_version"]:
        version = rule.get("provider_version_constraint")
        return (provider, version, scope, path)
    return (provider, scope, path)


def _check_identity_conflict(spec, identity, rule, seen, idx):
    if identity not in seen:
        return

    label = _rule_label(spec, idx, rule.get("id"))
    first = seen[identity]
    first_label = _rule_label(spec, None, first.get("id"))
    identity_text = _format_identity(spec, identity)

    for key in spec["conflict_keys"]:
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


def _format_identity(spec, identity):
    if spec["requires_version"]:
        provider, version, scope, path = identity
        scope_type, scope_value = scope
        if scope_type == "type":
            return "%s/%s/%s/%s" % (provider, version, scope_value, path)
        return "%s/%s:prefix(%s)/%s" % (
            provider, version, scope_value, path
        )
    provider, scope, path = identity
    scope_type, scope_value = scope
    if scope_type == "type":
        return "%s/%s/%s" % (provider, scope_value, path)
    return "%s:prefix(%s)/%s" % (provider, scope_value, path)


def _rule_label(spec, idx, ident=None):
    label = spec["label"]
    if ident:
        if idx is not None:
            return "%s %d (%s)" % (label, idx, ident)
        return "%s (%s)" % (label, ident)
    if idx is not None:
        return "%s %d" % (label, idx)
    return label


def _idx_label(idx):
    if idx is None:
        return ""
    return "%d" % idx


def _field_value(item, key):
    value = item[key]
    if isinstance(value, str):
        return value.strip()
    return value


def _scope_value(spec, item, key):
    value = item[key]
    if spec["provider_scope_strip"] and isinstance(value, str):
        return value.strip()
    return value


ABSENT_DEFAULTS_SPEC = {
    "name": "absent_defaults",
    "label": "absent_defaults rule",
    "list_error": "absent_defaults.rules must be a list",
    "accepted_keys": ABSENT_DEFAULTS_ACCEPTED_RULE_KEYS,
    "required_keys": ABSENT_DEFAULTS_REQUIRED_RULE_KEYS,
    "string_keys": (
        "id", "provider", "path", "kind", "action", "evidence", "reason",
    ),
    "string_error_mode": "missing_for_non_string",
    "strip_keys": (),
    "requires_version": False,
    "enums": (("kind", ABSENT_DEFAULTS_ALLOWED_KINDS),),
    "normalize_enum_keys": set([]),
    "action_allowed": ABSENT_DEFAULTS_ALLOWED_V1_ACTIONS,
    "action_rejected": (
        ABSENT_DEFAULTS_REJECTED_ACTIONS,
        "%s: action %s is rejected in V1",
    ),
    "action_forbidden": None,
    "forbidden_keys": None,
    "evidence_path_keys": (
        "plan_path", "raw_api_path", "provider_state_path",
    ),
    "evidence_path_error": "%s: %s must be a string",
    "canonicalized_evidence_keys": ("plan_path",),
    "matrices": ((
        "kind",
        "action",
        ABSENT_DEFAULTS_KIND_ACTION_MATRIX,
        "%s: kind %s does not allow action %s",
    ),),
    "matrix_position": "before_resource",
    "sensitive_polarity": "forbid",
    "sensitive_path_error": "%s: path %s targets a known sensitive path",
    "scope_error_mode": "missing",
    "provider_scope_strip": False,
    "conflict_keys": ("kind", "action", "observed_value"),
    "extra_checks": (_validate_observed_value, _validate_path_namespace),
    "overlap_error": (
        "absent_defaults rule (%s): resource_type %s overlaps "
        "resource_prefix %s for %s/%s"
    ),
}

DYNAMIC_SCHEMA_SPEC = {
    "name": "dynamic_schema",
    "label": "dynamic_schema rule",
    "list_error": "dynamic_schema.rules must be a list",
    "accepted_keys": DYNAMIC_SCHEMA_ACCEPTED_RULE_KEYS,
    "required_keys": DYNAMIC_SCHEMA_REQUIRED_RULE_KEYS,
    "string_keys": (
        "id", "provider", "path", "kind", "ownership", "action",
        "evidence", "reason",
    ),
    "string_error_mode": "missing_then_string",
    "strip_keys": ("provider", "id"),
    "requires_version": True,
    "enums": (
        ("kind", DYNAMIC_SCHEMA_ALLOWED_KINDS),
        ("ownership", DYNAMIC_SCHEMA_ALLOWED_OWNERSHIPS),
    ),
    "normalize_enum_keys": set(["kind", "ownership", "action"]),
    "action_allowed": DYNAMIC_SCHEMA_ALLOWED_V1_ACTIONS,
    "action_rejected": (
        DYNAMIC_SCHEMA_REJECTED_ACTIONS,
        "%s: action %s is rejected in V1",
    ),
    "action_forbidden": None,
    "forbidden_keys": None,
    "evidence_path_keys": ("raw_api_path", "projected_path", "plan_path"),
    "evidence_path_error": "%s: %s must be a non-empty string",
    "canonicalized_evidence_keys": (),
    "matrices": ((
        "kind",
        "ownership",
        DYNAMIC_SCHEMA_KIND_OWNERSHIP_MATRIX,
        "%s: kind %s does not allow ownership %s",
    ),),
    "matrix_position": "after_sensitive",
    "sensitive_polarity": "forbid",
    "sensitive_path_error": "%s: path %s targets a known sensitive path",
    "scope_error_mode": "missing",
    "provider_scope_strip": True,
    "conflict_keys": (
        "kind", "ownership", "action", "evidence", "raw_api_path",
        "projected_path", "plan_path",
    ),
    "extra_checks": (),
    "overlap_error": (
        "dynamic_schema rule (%s): resource_type %s overlaps "
        "resource_prefix %s for %s/%s/%s"
    ),
}

SENSITIVE_REQUIRED_SPEC = {
    "name": "sensitive_required",
    "label": "sensitive_required rule",
    "list_error": "sensitive_required.rules must be a list",
    "accepted_keys": SENSITIVE_REQUIRED_ACCEPTED_RULE_KEYS,
    "required_keys": SENSITIVE_REQUIRED_REQUIRED_RULE_KEYS,
    "string_keys": (
        "id", "provider", "path", "kind", "sensitivity",
        "structural_requirement", "action", "evidence", "reason",
    ),
    "string_error_mode": "must_be_string",
    "strip_keys": (
        "provider", "id", "kind", "sensitivity", "structural_requirement",
        "action", "evidence", "reason",
    ),
    "requires_version": True,
    "enums": (
        ("kind", SENSITIVE_REQUIRED_ALLOWED_KINDS),
        ("sensitivity", SENSITIVE_REQUIRED_ALLOWED_SENSITIVITIES),
        (
            "structural_requirement",
            SENSITIVE_REQUIRED_ALLOWED_STRUCTURAL_REQUIREMENTS,
        ),
    ),
    "normalize_enum_keys": set([
        "kind", "sensitivity", "structural_requirement", "action",
    ]),
    "action_allowed": SENSITIVE_REQUIRED_ALLOWED_V1_ACTIONS,
    "action_rejected": (
        SENSITIVE_REQUIRED_RESERVED_ACTIONS,
        "%s: action %s is rejected in V1",
    ),
    "action_forbidden": (
        SENSITIVE_REQUIRED_FORBIDDEN_ACTIONS,
        "%s: action %s is forbidden",
    ),
    "forbidden_keys": (
        SENSITIVE_REQUIRED_FORBIDDEN_VALUE_CARRYING_KEYS,
        "%s: forbidden value-carrying key %s",
    ),
    "evidence_path_keys": ("raw_api_path", "projected_path", "plan_path"),
    "evidence_path_error": "%s: %s must be a non-empty string",
    "canonicalized_evidence_keys": (),
    "matrices": (
        (
            "kind",
            "sensitivity",
            SENSITIVE_REQUIRED_KIND_SENSITIVITY_MATRIX,
            "%s: kind %s does not allow sensitivity %s",
        ),
        (
            "kind",
            "structural_requirement",
            SENSITIVE_REQUIRED_KIND_STRUCTURAL_REQUIREMENT_MATRIX,
            "%s: kind %s does not allow structural_requirement %s",
        ),
    ),
    "matrix_position": "after_sensitive",
    "sensitive_polarity": "require",
    "sensitive_path_error": (
        "%s: path %s is not in supplied sensitive_paths"
    ),
    "scope_error_mode": "must_be_string",
    "provider_scope_strip": True,
    "conflict_keys": (
        "kind", "sensitivity", "structural_requirement", "action",
        "evidence", "raw_api_path", "projected_path", "plan_path",
    ),
    "extra_checks": (),
    "overlap_error": (
        "sensitive_required rule (%s): resource_type %s overlaps "
        "resource_prefix %s for %s/%s/%s"
    ),
}

LANES = {
    "absent_defaults": ABSENT_DEFAULTS_SPEC,
    "dynamic_schema": DYNAMIC_SCHEMA_SPEC,
    "sensitive_required": SENSITIVE_REQUIRED_SPEC,
}
