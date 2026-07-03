"""Validator for sensitive-required rule metadata.

This module only parses and validates future ``sensitive_required.rules`` metadata.
It does not project sensitive values, render placeholders, omit paths, change
projection behavior, change drift policy, alter advisory behavior, or change
assert-adoptable status.
"""
from engine import lanes


_SPEC = lanes.LANES["sensitive_required"]

ACCEPTED_RULE_KEYS = _SPEC["accepted_keys"]
REQUIRED_RULE_KEYS = _SPEC["required_keys"]
FORBIDDEN_VALUE_CARRYING_KEYS = _SPEC["forbidden_keys"][0]
ALLOWED_KINDS = _SPEC["enums"][0][1]
ALLOWED_SENSITIVITIES = _SPEC["enums"][1][1]
ALLOWED_STRUCTURAL_REQUIREMENTS = _SPEC["enums"][2][1]
ALLOWED_V1_ACTIONS = _SPEC["action_allowed"]
RESERVED_ACTIONS = _SPEC["action_rejected"][0]
FORBIDDEN_ACTIONS = _SPEC["action_forbidden"][0]
KIND_SENSITIVITY_MATRIX = _SPEC["matrices"][0][2]
KIND_STRUCTURAL_REQUIREMENT_MATRIX = _SPEC["matrices"][1][2]


def validate_sensitive_required_rules(rules, sensitive_paths=None,
                                     provider_prefixes=None):
    """Validate a list of sensitive-required rule metadata.

    Returns the validated rules as a list, sorted by (provider, id, path) for
    internal consistency. The returned objects are shallow copies with
    canonicalized `path` and stripped `provider_version_constraint`. No
    sensitive values are projected or transformed.
    """
    return lanes.validate_rules(
        _SPEC,
        rules,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )


def validate_sensitive_required_rule(rule, idx=None, sensitive_paths=None,
                                      provider_prefixes=None):
    """Validate a single sensitive-required rule metadata object.

    Returns a shallow copy of the rule with canonicalized `path` and stripped
    `provider_version_constraint`. No sensitive values are projected or
    transformed.
    """
    return lanes.validate_rule(
        _SPEC,
        rule,
        idx=idx,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )
