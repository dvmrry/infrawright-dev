"""Validator for absent/default rule metadata.

This module only parses and validates future ``absent_defaults.rules`` metadata.
It does not normalize projected values, omit values, change projection behavior,
change drift policy, or create a second omission engine.
"""
from engine import lanes


_SPEC = lanes.LANES["absent_defaults"]

ACCEPTED_RULE_KEYS = _SPEC["accepted_keys"]
REQUIRED_RULE_KEYS = _SPEC["required_keys"]
ALLOWED_KINDS = _SPEC["enums"][0][1]
ALLOWED_V1_ACTIONS = _SPEC["action_allowed"]
REJECTED_ACTIONS = _SPEC["action_rejected"][0]
KIND_ACTION_MATRIX = _SPEC["matrices"][0][2]
KINDS_REQUIRING_OBSERVED_VALUE = (
    lanes.ABSENT_DEFAULTS_KINDS_REQUIRING_OBSERVED_VALUE
)
ACTIONS_REQUIRING_OBSERVED_VALUE = (
    lanes.ABSENT_DEFAULTS_ACTIONS_REQUIRING_OBSERVED_VALUE
)


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
    return lanes.validate_rules(
        _SPEC,
        rules,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )


def validate_absent_default_rule(rule, idx=None, sensitive_paths=None,
                                   provider_prefixes=None):
    """Validate a single absent/default rule metadata object.

    Returns the rule object unchanged (a shallow copy) after validation.
    """
    return lanes.validate_rule(
        _SPEC,
        rule,
        idx=idx,
        sensitive_paths=sensitive_paths,
        provider_prefixes=provider_prefixes,
    )
