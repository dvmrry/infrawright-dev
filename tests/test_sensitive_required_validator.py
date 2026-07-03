"""Exhaustive tests for sensitive-required rule metadata validation."""
import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine import sensitive_required_validator


_DELETE = object()


def _base_rule(**overrides):
    rule = {
        "id": "grafana_contact_point_webhook_required_sensitive",
        "provider": "grafana",
        "provider_version_constraint": ">= 3.0.0",
        "resource_type": "grafana_contact_point",
        "path": "webhook",
        "kind": "sensitive_required_block",
        "sensitivity": "contains_sensitive_fields",
        "structural_requirement": "one_of_block_required",
        "action": "manual_review_required",
        "evidence": "docs/provider-labs/grafana-pr24.md",
        "reason": "One of the contact-point notifier blocks must be present.",
    }
    for key, value in overrides.items():
        if value is _DELETE:
            rule.pop(key, None)
        else:
            rule[key] = value
    return rule


class SensitiveRequiredValidatorPositiveTest(unittest.TestCase):

    def test_valid_sensitive_required_block(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["id"], "grafana_contact_point_webhook_required_sensitive")
        self.assertEqual(rules[0]["kind"], "sensitive_required_block")
        self.assertEqual(rules[0]["sensitivity"], "contains_sensitive_fields")
        self.assertEqual(rules[0]["structural_requirement"], "one_of_block_required")
        self.assertEqual(rules[0]["action"], "manual_review_required")

    def test_valid_sensitive_required_attribute(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(
                id="sensitive_attr",
                kind="sensitive_required_attribute",
                sensitivity="sensitive_attribute",
                structural_requirement="attribute_required_for_valid_config",
                path="webhook.url",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["kind"], "sensitive_required_attribute")

    def test_valid_sensitive_write_only_attribute(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(
                id="write_only_attr",
                kind="sensitive_write_only_attribute",
                sensitivity="write_only_sensitive",
                structural_requirement="operator_input_required_for_valid_config",
                path="webhook.token",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["kind"], "sensitive_write_only_attribute")

    def test_valid_sensitive_nested_secret(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(
                id="nested_secret",
                kind="sensitive_nested_secret",
                sensitivity="contains_sensitive_fields",
                structural_requirement="parent_block_required",
                path="webhook.secret",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["kind"], "sensitive_nested_secret")

    def test_valid_sensitive_structural_placeholder_required(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(
                id="placeholder_required",
                kind="sensitive_structural_placeholder_required",
                sensitivity="sensitive_block",
                structural_requirement="block_required_for_valid_config",
                path="webhook",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["kind"], "sensitive_structural_placeholder_required")

    def test_provider_version_constraint_stripped(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(provider_version_constraint="  >= 3.0.0  "),
        ])
        self.assertEqual(rules[0]["provider_version_constraint"], ">= 3.0.0")

    def test_path_canonicalization_numeric_index(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(path="webhook[0].url"),
        ])
        self.assertEqual(rules[0]["path"], "webhook[].url")

    def test_path_canonicalization_wildcard_index(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(path="webhook[*].url"),
        ])
        self.assertEqual(rules[0]["path"], "webhook[].url")

    def test_provider_resource_match_accepted(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(),
        ], provider_prefixes={"grafana_": "grafana"})
        self.assertEqual(len(rules), 1)

    def test_static_sensitive_path_exact_match_accepted(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(),
        ], sensitive_paths=["webhook"])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["path"], "webhook")

    def test_static_sensitive_path_canonical_match_accepted(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(path="webhook[0].url"),
        ], sensitive_paths=["webhook[].url"])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["path"], "webhook[].url")

    def test_sensitive_path_check_skipped_when_none(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(path="not_in_any_static_set"),
        ], sensitive_paths=None)
        self.assertEqual(len(rules), 1)


class SensitiveRequiredValidatorNegativeTest(unittest.TestCase):

    def test_unknown_key(self):
        with self.assertRaisesRegex(ValueError, "unknown rule key"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(extra_key="bad"),
            ])

    def test_forbidden_value_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key value"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(value="secret"),
            ])

    def test_forbidden_observed_value_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key observed_value"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(observed_value=""),
            ])

    def test_forbidden_placeholder_value_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key placeholder_value"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(placeholder_value="x"),
            ])

    def test_forbidden_secret_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key secret"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(secret="x"),
            ])

    def test_forbidden_secret_value_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key secret_value"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(secret_value="x"),
            ])

    def test_forbidden_sensitive_value_key(self):
        with self.assertRaisesRegex(ValueError, "forbidden value-carrying key sensitive_value"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(sensitive_value="x"),
            ])

    def test_missing_provider_version_constraint(self):
        with self.assertRaisesRegex(ValueError, "missing provider_version_constraint"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(provider_version_constraint=_DELETE),
            ])

    def test_missing_sensitivity(self):
        with self.assertRaisesRegex(ValueError, "missing sensitivity"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(sensitivity=_DELETE),
            ])

    def test_missing_structural_requirement(self):
        with self.assertRaisesRegex(ValueError, "missing structural_requirement"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(structural_requirement=_DELETE),
            ])

    def test_missing_evidence(self):
        with self.assertRaisesRegex(ValueError, "missing evidence"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(evidence=_DELETE),
            ])

    def test_missing_reason(self):
        with self.assertRaisesRegex(ValueError, "missing reason"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(reason=_DELETE),
            ])

    def test_non_string_required_field(self):
        with self.assertRaisesRegex(ValueError, "id must be a string"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(id=123),
            ])

    def test_whitespace_only_required_field(self):
        with self.assertRaisesRegex(ValueError, "id must be a string"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(id="   "),
            ])

    def test_both_resource_scopes(self):
        with self.assertRaisesRegex(ValueError, "cannot specify both"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(resource_prefix="grafana_"),
            ])

    def test_missing_path_with_evidence_path(self):
        with self.assertRaisesRegex(ValueError, "missing path"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(path=_DELETE, projected_path="projected.webhook"),
            ])

    def test_unsupported_path_syntax(self):
        with self.assertRaisesRegex(ValueError, "unsupported syntax"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(path="webhook..url"),
            ])

    def test_bare_wildcard_segment(self):
        with self.assertRaisesRegex(ValueError, "bare wildcard segment"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(path="*.url"),
            ])

    def test_unknown_sensitivity(self):
        with self.assertRaisesRegex(ValueError, "unknown sensitivity"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(sensitivity="unknown_sensitivity"),
            ])

    def test_unknown_structural_requirement(self):
        with self.assertRaisesRegex(ValueError, "unknown structural_requirement"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(structural_requirement="unknown_requirement"),
            ])

    def test_reserved_render_placeholder_block(self):
        with self.assertRaisesRegex(ValueError, "action render_placeholder_block is rejected in V1"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="render_placeholder_block"),
            ])

    def test_reserved_render_placeholder_attribute(self):
        with self.assertRaisesRegex(ValueError, "action render_placeholder_attribute is rejected in V1"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="render_placeholder_attribute"),
            ])

    def test_reserved_preserve_structure_without_secret_candidate(self):
        with self.assertRaisesRegex(ValueError, "action preserve_structure_without_secret_candidate is rejected in V1"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="preserve_structure_without_secret_candidate"),
            ])

    def test_reserved_operator_input_required_candidate(self):
        with self.assertRaisesRegex(ValueError, "action operator_input_required_candidate is rejected in V1"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="operator_input_required_candidate"),
            ])

    def test_forbidden_project_sensitive(self):
        with self.assertRaisesRegex(ValueError, "action project_sensitive is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="project_sensitive"),
            ])

    def test_forbidden_copy_sensitive_from_state(self):
        with self.assertRaisesRegex(ValueError, "action copy_sensitive_from_state is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="copy_sensitive_from_state"),
            ])

    def test_forbidden_guess_secret(self):
        with self.assertRaisesRegex(ValueError, "action guess_secret is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="guess_secret"),
            ])

    def test_forbidden_suppress_sensitive_drift(self):
        with self.assertRaisesRegex(ValueError, "action suppress_sensitive_drift is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="suppress_sensitive_drift"),
            ])

    def test_forbidden_omit_sensitive_block(self):
        with self.assertRaisesRegex(ValueError, "action omit_sensitive_block is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="omit_sensitive_block"),
            ])

    def test_forbidden_accept_sensitive_unknown(self):
        with self.assertRaisesRegex(ValueError, "action accept_sensitive_unknown is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="accept_sensitive_unknown"),
            ])

    def test_forbidden_downgrade_assert_adoptable(self):
        with self.assertRaisesRegex(ValueError, "action downgrade_assert_adoptable is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="downgrade_assert_adoptable"),
            ])

    def test_forbidden_render_fake_secret(self):
        with self.assertRaisesRegex(ValueError, "action render_fake_secret is forbidden"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(action="render_fake_secret"),
            ])

    def test_out_of_matrix_sensitivity(self):
        with self.assertRaisesRegex(ValueError, "kind sensitive_required_block does not allow sensitivity sensitive_attribute"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(sensitivity="sensitive_attribute"),
            ])

    def test_out_of_matrix_structural_requirement(self):
        with self.assertRaisesRegex(ValueError, "kind sensitive_required_block does not allow structural_requirement attribute_required_for_valid_config"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(structural_requirement="attribute_required_for_valid_config"),
            ])

    def test_duplicate_rule(self):
        with self.assertRaisesRegex(ValueError, "duplicate rule"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(),
            ])

    def test_conflicting_kind(self):
        with self.assertRaisesRegex(ValueError, "conflicting kind"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    kind="sensitive_structural_placeholder_required",
                    sensitivity="sensitive_block",
                    structural_requirement="block_required_for_valid_config",
                ),
            ])

    def test_conflicting_sensitivity(self):
        with self.assertRaisesRegex(ValueError, "conflicting sensitivity"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    sensitivity="sensitive_block",
                ),
            ])

    def test_conflicting_structural_requirement(self):
        with self.assertRaisesRegex(ValueError, "conflicting structural_requirement"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    structural_requirement="block_required_for_valid_config",
                ),
            ])

    def test_conflicting_action(self):
        with self.assertRaisesRegex(ValueError, "conflicting action"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    action="diagnostic_only",
                ),
            ])

    def test_conflicting_evidence(self):
        with self.assertRaisesRegex(ValueError, "conflicting evidence"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    evidence="docs/other.md",
                ),
            ])

    def test_conflicting_raw_api_path(self):
        with self.assertRaisesRegex(ValueError, "conflicting raw_api_path"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(raw_api_path="a"),
                _base_rule(
                    id="second",
                    raw_api_path="b",
                ),
            ])

    def test_conflicting_projected_path(self):
        with self.assertRaisesRegex(ValueError, "conflicting projected_path"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(projected_path="a"),
                _base_rule(
                    id="second",
                    projected_path="b",
                ),
            ])

    def test_conflicting_plan_path(self):
        with self.assertRaisesRegex(ValueError, "conflicting plan_path"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(plan_path="a"),
                _base_rule(
                    id="second",
                    plan_path="b",
                ),
            ])

    def test_same_identity_different_reason_rejected_as_duplicate(self):
        with self.assertRaisesRegex(ValueError, "duplicate rule"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
                _base_rule(
                    id="second",
                    reason="different reason",
                ),
            ])

    def test_overlapping_resource_type_and_prefix_same_version_path(self):
        with self.assertRaisesRegex(ValueError, "overlaps resource_prefix"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(resource_type="grafana_contact_point"),
                _base_rule(
                    id="prefix_rule",
                    resource_type=_DELETE,
                    resource_prefix="grafana_",
                ),
            ])

    def test_overlapping_resource_type_and_prefix_different_version_accepted(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(resource_type="grafana_contact_point"),
            _base_rule(
                id="prefix_rule",
                resource_type=_DELETE,
                resource_prefix="grafana_",
                provider_version_constraint="4.0.0",
            ),
        ])
        self.assertEqual(len(rules), 2)

    def test_semantically_similar_version_string_distinct_identity(self):
        rules = sensitive_required_validator.validate_sensitive_required_rules([
            _base_rule(provider_version_constraint=">= 3.0.0, < 4.0.0"),
            _base_rule(
                id="second",
                provider_version_constraint=">=3.0.0,<4.0.0",
            ),
        ])
        self.assertEqual(len(rules), 2)
        self.assertEqual(rules[0]["provider_version_constraint"], ">= 3.0.0, < 4.0.0")
        self.assertEqual(rules[1]["provider_version_constraint"], ">=3.0.0,<4.0.0")

    def test_provider_resource_mismatch(self):
        with self.assertRaisesRegex(ValueError, "resource_type grafana_contact_point resolves to provider grafana, not other"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(provider="other"),
            ], provider_prefixes={"grafana_": "grafana"})

    def test_unknown_resource_type(self):
        with self.assertRaisesRegex(ValueError, "resource_type unknown_type is not declared in provider_prefixes"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(resource_type="unknown_type"),
            ], provider_prefixes={"grafana_": "grafana"})

    def test_unknown_resource_prefix(self):
        with self.assertRaisesRegex(ValueError, "resource_prefix other_ is not declared in provider_prefixes"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(
                    resource_type=_DELETE,
                    resource_prefix="other_",
                ),
            ], provider_prefixes={"grafana_": "grafana"})

    def test_resource_prefix_declared_for_wrong_provider(self):
        with self.assertRaisesRegex(ValueError, "resource_prefix grafana_ is declared for provider grafana, not other"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(
                    provider="other",
                    resource_type=_DELETE,
                    resource_prefix="grafana_",
                ),
            ], provider_prefixes={"grafana_": "grafana"})

    def test_sensitive_path_not_present(self):
        with self.assertRaisesRegex(ValueError, "path webhook is not in supplied sensitive_paths"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(),
            ], sensitive_paths=["other"])

    def test_sensitive_descendant_does_not_satisfy_ancestor(self):
        with self.assertRaisesRegex(ValueError, "path webhook\\.url is not in supplied sensitive_paths"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(path="webhook.url"),
            ], sensitive_paths=["webhook"])

    def test_sensitive_ancestor_does_not_satisfy_descendant(self):
        with self.assertRaisesRegex(ValueError, "path webhook is not in supplied sensitive_paths"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(path="webhook"),
            ], sensitive_paths=["webhook.url"])

    def test_evidence_only_path_must_be_string(self):
        with self.assertRaisesRegex(ValueError, "raw_api_path must be a non-empty string"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(raw_api_path=123),
            ])

    def test_evidence_only_path_must_be_non_empty(self):
        with self.assertRaisesRegex(ValueError, "raw_api_path must be a non-empty string"):
            sensitive_required_validator.validate_sensitive_required_rules([
                _base_rule(raw_api_path="   "),
            ])


class SensitiveRequiredPacksAccessorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.prev = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()

    def tearDown(self):
        if self.prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_pack(self, name, manifest):
        d = os.path.join(self.tmp, name)
        os.makedirs(d)
        with open(os.path.join(d, "pack.json"), "w", encoding="utf-8") as f:
            json.dump(manifest, f)
        packs.reset()

    def test_accessor_returns_empty_when_no_metadata(self):
        self.assertEqual(packs.sensitive_required_rules(), [])
        self.assertEqual(packs.sensitive_required_rules("grafana"), [])

    def test_accessor_infers_provider_from_single_provider_manifest(self):
        self._write_pack("grafana", {
            "provider_prefixes": {"grafana_": "grafana"},
            "provider_sources": {"grafana": "grafana/grafana"},
            "sensitive_required": {
                "rules": [{
                    "id": "grafana_contact_point_webhook",
                    "provider_version_constraint": ">= 3.0.0",
                    "resource_type": "grafana_contact_point",
                    "path": "webhook",
                    "kind": "sensitive_required_block",
                    "sensitivity": "contains_sensitive_fields",
                    "structural_requirement": "one_of_block_required",
                    "action": "manual_review_required",
                    "evidence": "docs/provider-labs/grafana-pr24.md",
                    "reason": "Notifier block required.",
                }]
            },
        })
        rules = packs.sensitive_required_rules()
        self.assertEqual(rules[0]["provider"], "grafana")

if __name__ == "__main__":
    unittest.main()
