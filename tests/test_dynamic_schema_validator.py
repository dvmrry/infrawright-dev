"""Exhaustive tests for dynamic-schema rule metadata validation."""
import json
import os
import shutil
import tempfile
import unittest

from engine import dynamic_schema_validator
from engine import packs


_DELETE = object()


def _base_rule(**overrides):
    rule = {
        "id": "cloudflare_ruleset_action_parameters_dynamic_map",
        "provider": "cloudflare",
        "provider_version_constraint": ">= 4.0.0, < 5.0.0",
        "resource_type": "cloudflare_ruleset",
        "path": "rules[].action_parameters",
        "kind": "provider_observed_projection_unsafe",
        "ownership": "server_owned",
        "action": "diagnostic_only",
        "evidence": "docs/provider-labs/cloudflare-free-tier-pr32.md",
        "reason": "Provider exposes a dynamic nested map; schema cannot prove stable projection semantics.",
    }
    for key, value in overrides.items():
        if value is _DELETE:
            rule.pop(key, None)
        else:
            rule[key] = value
    return rule


class DynamicSchemaValidatorPositiveTest(unittest.TestCase):

    def test_valid_provider_observed_projection_unsafe(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["id"], "cloudflare_ruleset_action_parameters_dynamic_map")
        self.assertEqual(rules[0]["kind"], "provider_observed_projection_unsafe")
        self.assertEqual(rules[0]["ownership"], "server_owned")
        self.assertEqual(rules[0]["action"], "diagnostic_only")

    def test_valid_freeform_object_user_owned(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(
                id="freeform_user_owned",
                kind="freeform_object",
                ownership="user_owned",
                action="manual_review_required",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["ownership"], "user_owned")
        self.assertEqual(rules[0]["action"], "manual_review_required")

    def test_valid_schema_unknown_user_owned(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(
                id="schema_unknown_user_owned",
                kind="schema_unknown_but_provider_observed",
                ownership="user_owned",
                action="diagnostic_only",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["kind"], "schema_unknown_but_provider_observed")

    def test_valid_raw_api_only_provider_blind(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(
                id="raw_api_only",
                kind="raw_api_only_provider_blind",
                ownership="unknown",
                action="diagnostic_only",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["ownership"], "unknown")

    def test_provider_version_constraint_stripped(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(provider_version_constraint="  >= 4.0.0, < 5.0.0  "),
        ])
        self.assertEqual(rules[0]["provider_version_constraint"], ">= 4.0.0, < 5.0.0")

    def test_path_canonicalization_numeric_index(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(path="rules[0].action_parameters"),
        ])
        self.assertEqual(rules[0]["path"], "rules[].action_parameters")

    def test_path_canonicalization_wildcard_index(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(path="rules[*].action_parameters"),
        ])
        self.assertEqual(rules[0]["path"], "rules[].action_parameters")

    def test_provider_resource_match_accepted(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules(
            [_base_rule()],
            provider_prefixes={"cloudflare_": "cloudflare"},
        )
        self.assertEqual(len(rules), 1)

    def test_standalone_validator_skips_provider_match(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(provider="netbox", resource_type="netbox_device"),
        ])
        self.assertEqual(len(rules), 1)

    def test_packs_accessor_returns_empty_when_no_metadata(self):
        self._tmp = tempfile.mkdtemp()
        self._prev = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self._tmp
        packs.reset()
        self.addCleanup(self._restore)
        os.makedirs(os.path.join(self._tmp, "a"))
        with open(os.path.join(self._tmp, "a", "pack.json"), "w", encoding="utf-8") as f:
            json.dump({"provider_prefixes": {"a_": "a"}}, f)
        self.assertEqual(packs.dynamic_schema_rules(), [])

    def _restore(self):
        if self._prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self._prev
        packs.reset()
        shutil.rmtree(self._tmp, ignore_errors=True)


class DynamicSchemaValidatorNegativeTest(unittest.TestCase):
    def _assert_invalid(self, rule, text, rules=None, sensitive_paths=None,
                        provider_prefixes=None):
        with self.assertRaises(ValueError) as ctx:
            dynamic_schema_validator.validate_dynamic_schema_rules(
                rules or [rule],
                sensitive_paths=sensitive_paths,
                provider_prefixes=provider_prefixes,
            )
        err = str(ctx.exception)
        self.assertIn("dynamic_schema rule", err)
        self.assertIn(text, err)

    def test_unknown_key(self):
        self._assert_invalid(_base_rule(extra=True), "unknown rule key extra")

    def test_missing_provider_version_constraint(self):
        self._assert_invalid(
            _base_rule(provider_version_constraint=_DELETE),
            "missing provider_version_constraint",
        )

    def test_empty_provider_version_constraint(self):
        self._assert_invalid(
            _base_rule(provider_version_constraint="  "),
            "missing provider_version_constraint",
        )

    def test_provider_version_constraint_must_be_string_number_rejected(self):
        self._assert_invalid(
            _base_rule(provider_version_constraint=123),
            "missing provider_version_constraint",
        )

    def test_provider_version_constraint_must_be_string_list_rejected(self):
        self._assert_invalid(
            _base_rule(provider_version_constraint=[]),
            "missing provider_version_constraint",
        )

    def test_provider_version_constraint_must_be_string_object_rejected(self):
        self._assert_invalid(
            _base_rule(provider_version_constraint={}),
            "missing provider_version_constraint",
        )

    def test_both_resource_type_and_prefix(self):
        self._assert_invalid(
            _base_rule(resource_prefix="cloudflare_"),
            "cannot specify both resource_type and resource_prefix",
        )

    def test_evidence_path_without_path(self):
        self._assert_invalid(
            _base_rule(path=_DELETE, raw_api_path="api.path"),
            "missing path",
        )

    def test_unsupported_path_syntax(self):
        self._assert_invalid(
            _base_rule(path="rules.*.action_parameters"),
            "unsupported syntax",
        )

    def test_missing_ownership(self):
        self._assert_invalid(_base_rule(ownership=_DELETE), "missing ownership")

    def test_unknown_ownership(self):
        self._assert_invalid(
            _base_rule(ownership="invalid"),
            "unknown ownership",
        )

    def test_preserve_observed_scalar_rejected(self):
        self._assert_invalid(
            _base_rule(action="preserve_observed_scalar"),
            "action preserve_observed_scalar is rejected in V1",
        )

    def test_projection_omit_candidate_rejected(self):
        self._assert_invalid(
            _base_rule(action="projection_omit_candidate"),
            "action projection_omit_candidate is rejected in V1",
        )

    def test_project_dynamic_rejected(self):
        self._assert_invalid(_base_rule(action="project_dynamic"), "unknown action")

    def test_accept_unknown_rejected(self):
        self._assert_invalid(_base_rule(action="accept_unknown"), "unknown action")

    def test_ignore_schema_gap_rejected(self):
        self._assert_invalid(_base_rule(action="ignore_schema_gap"), "unknown action")

    def test_drop_dynamic_rejected(self):
        self._assert_invalid(_base_rule(action="drop_dynamic"), "unknown action")

    def test_user_owned_rejected_provider_state_only(self):
        self._assert_invalid(
            _base_rule(kind="provider_state_only", ownership="user_owned"),
            "kind provider_state_only does not allow ownership user_owned",
        )

    def test_user_owned_rejected_provider_computed_map(self):
        self._assert_invalid(
            _base_rule(kind="provider_computed_map", ownership="user_owned"),
            "kind provider_computed_map does not allow ownership user_owned",
        )

    def test_user_owned_rejected_opaque_json_blob(self):
        self._assert_invalid(
            _base_rule(kind="opaque_json_blob", ownership="user_owned"),
            "kind opaque_json_blob does not allow ownership user_owned",
        )

    def test_user_owned_rejected_map_key_discovered(self):
        self._assert_invalid(
            _base_rule(kind="map_key_discovered_after_import", ownership="user_owned"),
            "kind map_key_discovered_after_import does not allow ownership user_owned",
        )

    def test_user_owned_rejected_unstable_collection(self):
        self._assert_invalid(
            _base_rule(kind="unstable_collection_identity", ownership="user_owned"),
            "kind unstable_collection_identity does not allow ownership user_owned",
        )

    def test_user_owned_rejected_raw_api_only(self):
        self._assert_invalid(
            _base_rule(kind="raw_api_only_provider_blind", ownership="user_owned"),
            "kind raw_api_only_provider_blind does not allow ownership user_owned",
        )

    def test_duplicate_identical_rule_rejected(self):
        rule = _base_rule()
        self._assert_invalid(None, "duplicate rule", rules=[rule, rule])

    def test_same_identity_different_kind_rejected(self):
        self._assert_invalid(
            None,
            "conflicting kind",
            rules=[
                _base_rule(),
                _base_rule(kind="provider_state_only"),
            ],
        )

    def test_same_identity_different_action_rejected(self):
        self._assert_invalid(
            None,
            "conflicting action",
            rules=[
                _base_rule(),
                _base_rule(action="manual_review_required"),
            ],
        )

    def test_same_identity_different_ownership_rejected(self):
        self._assert_invalid(
            None,
            "conflicting ownership",
            rules=[
                _base_rule(ownership="server_owned"),
                _base_rule(ownership="provider_computed"),
            ],
        )

    def test_same_identity_different_evidence_rejected(self):
        self._assert_invalid(
            None,
            "conflicting evidence",
            rules=[
                _base_rule(evidence="docs/a.md"),
                _base_rule(evidence="docs/b.md"),
            ],
        )

    def test_same_identity_different_raw_api_path_rejected(self):
        self._assert_invalid(
            None,
            "conflicting raw_api_path",
            rules=[
                _base_rule(raw_api_path="api.a"),
                _base_rule(raw_api_path="api.b"),
            ],
        )

    def test_same_identity_different_projected_path_rejected(self):
        self._assert_invalid(
            None,
            "conflicting projected_path",
            rules=[
                _base_rule(projected_path="projected.a"),
                _base_rule(projected_path="projected.b"),
            ],
        )

    def test_same_identity_different_plan_path_rejected(self):
        self._assert_invalid(
            None,
            "conflicting plan_path",
            rules=[
                _base_rule(plan_path="plan.a"),
                _base_rule(plan_path="plan.b"),
            ],
        )

    def test_semantically_similar_version_is_distinct(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(provider_version_constraint=">= 4.0.0, < 5.0.0"),
            _base_rule(provider_version_constraint=">=4.0.0,<5.0.0"),
        ])
        self.assertEqual(len(rules), 2)

    def test_overlapping_type_prefix_same_version_path_rejected(self):
        self._assert_invalid(
            None,
            "overlaps resource_prefix",
            rules=[
                _base_rule(resource_type="cloudflare_ruleset"),
                _base_rule(resource_type=_DELETE, resource_prefix="cloudflare_"),
            ],
        )

    def test_overlapping_type_prefix_different_version_accepted(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules([
            _base_rule(resource_type="cloudflare_ruleset"),
            _base_rule(
                resource_type=_DELETE,
                resource_prefix="cloudflare_",
                provider_version_constraint=">= 5.0.0, < 6.0.0",
            ),
        ])
        self.assertEqual(len(rules), 2)

    def test_provider_resource_mismatch_rejected(self):
        self._assert_invalid(
            _base_rule(provider="netbox", resource_type="cloudflare_ruleset"),
            "resource_type cloudflare_ruleset resolves to provider cloudflare, not netbox",
            provider_prefixes={"cloudflare_": "cloudflare"},
        )

    def test_unknown_resource_type_rejected(self):
        self._assert_invalid(
            _base_rule(resource_type="unknown_resource"),
            "resource_type unknown_resource is not declared in provider_prefixes",
            provider_prefixes={"cloudflare_": "cloudflare"},
        )

    def test_unknown_resource_prefix_rejected(self):
        self._assert_invalid(
            _base_rule(
                resource_type=_DELETE,
                resource_prefix="unknown_",
            ),
            "resource_prefix unknown_ is not declared in provider_prefixes",
            provider_prefixes={"cloudflare_": "cloudflare"},
        )

    def test_sensitive_path_exact_match_rejected(self):
        self._assert_invalid(
            _base_rule(path="config.secret"),
            "targets a known sensitive path",
            sensitive_paths={"config.secret"},
        )

    def test_sensitive_descendant_does_not_reject_ancestor(self):
        rules = dynamic_schema_validator.validate_dynamic_schema_rules(
            [_base_rule(path="config")],
            sensitive_paths={"config.secret"},
        )
        self.assertEqual(len(rules), 1)

    def test_evidence_fields_must_be_non_empty_strings(self):
        for key in ("raw_api_path", "projected_path", "plan_path"):
            with self.subTest(key=key):
                self._assert_invalid(
                    _base_rule(**{key: ""}),
                    "%s must be a non-empty string" % key,
                )

    def test_evidence_fields_must_be_strings(self):
        for key in ("raw_api_path", "projected_path", "plan_path"):
            with self.subTest(key=key):
                self._assert_invalid(
                    _base_rule(**{key: 123}),
                    "%s must be a non-empty string" % key,
                )

    def test_sensitive_path_canonicalized_match(self):
        self._assert_invalid(
            _base_rule(path="rules[0].action_parameters"),
            "targets a known sensitive path",
            sensitive_paths={"rules[].action_parameters"},
        )


class DynamicSchemaPacksAccessorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        self.addCleanup(self._restore)
        packs.reset()

    def _restore(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_pack(self, name, manifest, with_registry=False):
        d = os.path.join(self.tmp, name)
        os.makedirs(d)
        with open(os.path.join(d, "pack.json"), "w", encoding="utf-8") as f:
            json.dump(manifest, f)
        if with_registry:
            with open(os.path.join(d, "registry.json"), "w", encoding="utf-8") as f:
                json.dump({}, f)

    def test_accessor_infers_provider_from_single_provider_manifest(self):
        self._write_pack("a", {
            "provider_prefixes": {"a_": "a"},
            "dynamic_schema": {
                "rules": [{
                    "id": "a_dynamic",
                    "resource_type": "a_thing",
                    "path": "dynamic",
                    "kind": "provider_observed_projection_unsafe",
                    "ownership": "unknown",
                    "action": "diagnostic_only",
                    "provider_version_constraint": ">= 1.0.0",
                    "evidence": "docs/a.md",
                    "reason": "dynamic",
                }]
            },
        })
        packs.reset()
        rules = packs.dynamic_schema_rules()
        self.assertEqual(rules[0]["provider"], "a")

if __name__ == "__main__":
    unittest.main()
