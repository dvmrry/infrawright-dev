"""Validator for dynamic-schema rule metadata.

This module only parses and validates future ``dynamic_schema.rules`` metadata.
It does not project dynamic-schema paths, omit paths, change projection behavior,
change drift policy, create a second omission engine, or alter advisory or
assert-adoptable behavior.
"""
from engine import lanes


_SPEC = lanes.LANES["dynamic_schema"]

ACCEPTED_RULE_KEYS = _SPEC["accepted_keys"]
REQUIRED_RULE_KEYS = _SPEC["required_keys"]
ALLOWED_KINDS = _SPEC["enums"][0][1]
ALLOWED_OWNERSHIPS = _SPEC["enums"][1][1]
ALLOWED_V1_ACTIONS = _SPEC["action_allowed"]
REJECTED_ACTIONS = _SPEC["action_rejected"][0]
KIND_OWNERSHIP_MATRIX = _SPEC["matrices"][0][2]


def validate_dynamic_schema_rules(rules, sensitive_paths=None,
                                  provider_prefixes=None):
    """Validate a list of dynamic-schema rule metadata.

    Returns the validated rules as a list, sorted by (provider, id, path) for
    internal consistency. The returned objects are shallow copies with
    canonicalized `path` and stripped `provider_version_constraint`. No
    infrastructure values are transformed.
    """
    return lanes.validate_rules(
        _SPEC,
        rules,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )


def validate_dynamic_schema_rule(rule, idx=None, sensitive_paths=None,
                                  provider_prefixes=None):
    """Validate a single dynamic-schema rule metadata object.

    Returns a shallow copy of the rule with canonicalized `path` and stripped
    `provider_version_constraint`.
    """
    return lanes.validate_rule(
        _SPEC,
        rule,
        idx=idx,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )
