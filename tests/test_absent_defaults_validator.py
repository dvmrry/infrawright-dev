"""Exhaustive tests for absent/default rule metadata validation."""
import json
import os
import shutil
import tempfile
import unittest

from engine import absent_defaults_validator
from engine import packs


_DELETE = object()


def _base_rule(**overrides):
    rule = {
        "id": "netbox_device_empty_rack_face_placeholder",
        "provider": "netbox",
        "resource_type": "netbox_device",
        "path": "rack_face",
        "kind": "provider_absent_placeholder",
        "observed_value": "",
        "action": "manual_review_required",
        "evidence": "docs/provider-labs/netbox-pr22.md",
        "reason": "Provider reported an empty string placeholder for an absent optional rack face.",
    }
    for key, value in overrides.items():
        if value is _DELETE:
            rule.pop(key, None)
        else:
            rule[key] = value
    return rule


class AbsentDefaultsValidatorPositiveTest(unittest.TestCase):

    def test_netbox_provider_absent_placeholder_manual_review(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="netbox_device_empty_rack_face_placeholder",
                kind="provider_absent_placeholder",
                action="manual_review_required",
                observed_value="",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["id"], "netbox_device_empty_rack_face_placeholder")

    def test_netbox_provider_absent_placeholder_diagnostic_only(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="netbox_device_empty_rack_face_placeholder",
                kind="provider_absent_placeholder",
                action="diagnostic_only",
                observed_value="",
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["action"], "diagnostic_only")

    def test_cloudflare_server_side_singleton_default_diagnostic_only(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="cloudflare_zone_hold_hold_default",
                provider="cloudflare",
                resource_type="cloudflare_zone_hold",
                path="hold",
                kind="provider_server_side_singleton_default",
                action="diagnostic_only",
                observed_value=_DELETE,
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertNotIn("observed_value", rules[0])

    def test_cloudflare_server_side_singleton_default_manual_review(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="cloudflare_zone_hold_hold_default",
                provider="cloudflare",
                resource_type="cloudflare_zone_hold",
                path="hold",
                kind="provider_server_side_singleton_default",
                action="manual_review_required",
                observed_value=_DELETE,
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertEqual(rules[0]["action"], "manual_review_required")

    def test_real_configured_falsey_preserve(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="real_falsey_hold",
                provider="cloudflare",
                resource_type="cloudflare_zone_hold",
                path="hold",
                kind="real_configured_falsey",
                action="preserve_explicit_falsey",
                observed_value=False,
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertIs(rules[0]["observed_value"], False)

    def test_observed_value_type_strict_distinct_falsey_values(self):
        for value in (0, "0", "", None, [], {}):
            with self.subTest(value=value):
                rules = absent_defaults_validator.validate_absent_default_rules([
                    _base_rule(observed_value=value),
                ])
                self.assertEqual(len(rules), 1)
                self.assertEqual(rules[0]["observed_value"], value)

    def test_class_level_api_absent_without_observed_value(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(
                id="api_absent_rack_face",
                kind="api_absent",
                action="diagnostic_only",
                observed_value=_DELETE,
            ),
        ])
        self.assertEqual(len(rules), 1)
        self.assertNotIn("observed_value", rules[0])

    def test_resource_type_matches_provider_with_prefixes(self):
        rules = absent_defaults_validator.validate_absent_default_rules(
            [_base_rule(provider="netbox", resource_type="netbox_device")],
            provider_prefixes={"netbox_": "netbox"},
        )
        self.assertEqual(len(rules), 1)

    def test_resource_prefix_matches_provider_with_prefixes(self):
        rules = absent_defaults_validator.validate_absent_default_rules(
            [_base_rule(provider="netbox", resource_type=_DELETE,
                        resource_prefix="netbox_")],
            provider_prefixes={"netbox_": "netbox"},
        )
        self.assertEqual(len(rules), 1)

    def test_optional_evidence_paths_must_be_strings(self):
        for key in ("plan_path", "raw_api_path", "provider_state_path"):
            with self.subTest(key=key):
                rules = absent_defaults_validator.validate_absent_default_rules([
                    _base_rule(**{key: "valid.path"}),
                ])
                self.assertEqual(rules[0][key], "valid.path")

    def test_path_canonicalizes_numeric_index(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(path="foo[0].bar"),
        ])
        self.assertEqual(rules[0]["path"], "foo[].bar")

    def test_plan_path_canonicalizes_numeric_index(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(plan_path="foo[0].bar"),
        ])
        self.assertEqual(rules[0]["plan_path"], "foo[].bar")

    def test_path_canonicalizes_wildcard_index(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(path="foo[*].bar"),
        ])
        self.assertEqual(rules[0]["path"], "foo[].bar")


class AbsentDefaultsValidatorNegativeTest(unittest.TestCase):
    def _assert_invalid(self, rule, text, rules=None, sensitive_paths=None,
                        provider_prefixes=None):
        with self.assertRaises(ValueError) as ctx:
            absent_defaults_validator.validate_absent_default_rules(
                rules or [rule],
                sensitive_paths=sensitive_paths,
                provider_prefixes=provider_prefixes,
            )
        err = str(ctx.exception)
        self.assertIn("absent_defaults rule", err)
        self.assertIn(text, err)

    def test_both_resource_type_and_prefix(self):
        self._assert_invalid(
            _base_rule(resource_prefix="netbox_"),
            "cannot specify both resource_type and resource_prefix",
        )

    def test_omit_when_absent_in_api_rejected(self):
        self._assert_invalid(
            _base_rule(action="omit_when_absent_in_api"),
            "action omit_when_absent_in_api is rejected in V1",
        )

    def test_omit_when_provider_placeholder_rejected(self):
        self._assert_invalid(
            _base_rule(action="omit_when_provider_placeholder"),
            "action omit_when_provider_placeholder is rejected in V1",
        )

    def test_drop_empty_values_rejected(self):
        self._assert_invalid(
            _base_rule(action="drop_empty_values"),
            "action drop_empty_values is rejected in V1",
        )

    def test_drop_falsey_rejected(self):
        self._assert_invalid(
            _base_rule(action="drop_falsey"),
            "action drop_falsey is rejected in V1",
        )

    def test_normalize_defaults_rejected(self):
        self._assert_invalid(
            _base_rule(action="normalize_defaults"),
            "action normalize_defaults is rejected in V1",
        )

    def test_missing_evidence(self):
        self._assert_invalid(_base_rule(evidence=_DELETE), "missing evidence")

    def test_missing_reason(self):
        self._assert_invalid(_base_rule(reason=_DELETE), "missing reason")

    def test_missing_observed_value_provider_absent_placeholder(self):
        self._assert_invalid(
            _base_rule(observed_value=_DELETE),
            "kind provider_absent_placeholder requires observed_value",
        )

    def test_missing_observed_value_api_explicit_default(self):
        self._assert_invalid(
            _base_rule(
                kind="api_explicit_default",
                action="diagnostic_only",
                observed_value=_DELETE,
            ),
            "kind api_explicit_default requires observed_value",
        )

    def test_missing_observed_value_terraform_schema_optional_default(self):
        self._assert_invalid(
            _base_rule(
                kind="terraform_schema_optional_default",
                action="diagnostic_only",
                observed_value=_DELETE,
            ),
            "kind terraform_schema_optional_default requires observed_value",
        )

    def test_missing_observed_value_preserve_explicit_falsey(self):
        self._assert_invalid(
            _base_rule(
                kind="real_configured_falsey",
                action="preserve_explicit_falsey",
                observed_value=_DELETE,
            ),
            "action preserve_explicit_falsey requires observed_value",
        )

    def test_out_of_matrix_api_absent_preserve_explicit_falsey(self):
        self._assert_invalid(
            _base_rule(
                kind="api_absent",
                action="preserve_explicit_falsey",
                observed_value=False,
            ),
            "kind api_absent does not allow action preserve_explicit_falsey",
        )

    def test_out_of_matrix_real_configured_falsey_omit_when_absent_in_api(self):
        # omit actions are rejected before the matrix check
        self._assert_invalid(
            _base_rule(
                kind="real_configured_falsey",
                action="omit_when_absent_in_api",
            ),
            "action omit_when_absent_in_api is rejected in V1",
        )

    def test_duplicate_identical_rule_rejected(self):
        rule = _base_rule()
        self._assert_invalid(
            None,
            "duplicate rule",
            rules=[rule, rule],
        )

    def test_same_identity_different_action_rejected(self):
        self._assert_invalid(
            None,
            "conflicting action",
            rules=[
                _base_rule(),
                _base_rule(action="diagnostic_only"),
            ],
        )

    def test_same_identity_different_kind_rejected(self):
        self._assert_invalid(
            None,
            "conflicting kind",
            rules=[
                _base_rule(),
                _base_rule(kind="api_explicit_default"),
            ],
        )

    def test_same_identity_different_observed_value_rejected(self):
        self._assert_invalid(
            None,
            "conflicting observed_value",
            rules=[
                _base_rule(observed_value=""),
                _base_rule(observed_value="0"),
            ],
        )

    def test_overlapping_type_and_prefix_same_provider_path_rejected(self):
        self._assert_invalid(
            None,
            "overlaps resource_prefix",
            rules=[
                _base_rule(resource_type="netbox_device"),
                _base_rule(resource_type=_DELETE, resource_prefix="netbox_"),
            ],
        )

    def test_unknown_rule_key_rejected(self):
        self._assert_invalid(
            _base_rule(extra_field=True),
            "unknown rule key extra_field",
        )

    def test_path_required_even_when_raw_api_path_provided(self):
        self._assert_invalid(
            _base_rule(
                path=_DELETE,
                raw_api_path="rack_face",
            ),
            "missing path",
        )

    def test_sensitive_path_rejected_when_static_metadata_provided(self):
        self._assert_invalid(
            _base_rule(path="password"),
            "targets a known sensitive path",
            sensitive_paths={"password"},
        )

    def test_sensitive_path_canonical_match_rejects_rule_canonical(self):
        self._assert_invalid(
            _base_rule(path="foo[].bar"),
            "targets a known sensitive path",
            sensitive_paths={"foo[0].bar"},
        )

    def test_sensitive_path_canonical_match_rejects_sensitive_canonical(self):
        self._assert_invalid(
            _base_rule(path="foo[0].bar"),
            "targets a known sensitive path",
            sensitive_paths={"foo[].bar"},
        )

    def test_bare_wildcard_segment_rejected(self):
        self._assert_invalid(
            _base_rule(path="*.bar"),
            "bare wildcard segment",
        )

    def test_unsupported_path_syntax_rejected(self):
        self._assert_invalid(
            _base_rule(path="foo..bar"),
            "unsupported syntax",
        )

    def test_duplicate_canonical_equivalent_paths_rejected(self):
        self._assert_invalid(
            None,
            "duplicate rule",
            rules=[
                _base_rule(path="foo[0].bar"),
                _base_rule(path="foo[].bar"),
            ],
        )

    def test_conflict_canonical_equivalent_paths_rejected(self):
        self._assert_invalid(
            None,
            "conflicting observed_value",
            rules=[
                _base_rule(path="foo[0].bar", observed_value=""),
                _base_rule(path="foo[].bar", observed_value="0"),
            ],
        )

    def test_rejects_if_sensitive_not_inverted_accept_if_sensitive(self):
        # Regression: absent/default must reject known sensitive paths, not accept
        # them like sensitive-required does.
        with self.assertRaises(ValueError) as ctx:
            absent_defaults_validator.validate_absent_default_rules(
                [_base_rule(path="password")],
                sensitive_paths={"password"},
            )
        self.assertIn("targets a known sensitive path", str(ctx.exception))

    def test_resource_type_mismatch_rejected_with_prefixes(self):
        self._assert_invalid(
            _base_rule(
                provider="cloudflare",
                resource_type="netbox_device",
            ),
            "resource_type netbox_device resolves to provider netbox, not cloudflare",
            provider_prefixes={"netbox_": "netbox", "cloudflare_": "cloudflare"},
        )

    def test_resource_prefix_mismatch_rejected_with_prefixes(self):
        self._assert_invalid(
            _base_rule(
                provider="cloudflare",
                resource_type=_DELETE,
                resource_prefix="netbox_",
            ),
            "resource_prefix netbox_ is declared for provider netbox, not cloudflare",
            provider_prefixes={"netbox_": "netbox", "cloudflare_": "cloudflare"},
        )

    def test_unknown_resource_type_rejected_with_prefixes(self):
        self._assert_invalid(
            _base_rule(resource_type="unknown_resource"),
            "resource_type unknown_resource is not declared in provider_prefixes",
            provider_prefixes={"netbox_": "netbox"},
        )

    def test_unknown_resource_prefix_rejected_with_prefixes(self):
        self._assert_invalid(
            _base_rule(
                resource_type=_DELETE,
                resource_prefix="unknown_",
            ),
            "resource_prefix unknown_ is not declared in provider_prefixes",
            provider_prefixes={"netbox_": "netbox"},
        )

    def test_standalone_validator_skips_provider_match_when_no_prefixes(self):
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(provider="cloudflare", resource_type="netbox_device"),
        ])
        self.assertEqual(len(rules), 1)

    def test_optional_evidence_paths_must_be_strings(self):
        for key in ("plan_path", "raw_api_path", "provider_state_path"):
            with self.subTest(key=key):
                self._assert_invalid(
                    _base_rule(**{key: 123}),
                    "%s must be a string" % key,
                )

    def test_empty_string_value_is_valid_observed_value(self):
        # Positive subtest already covers this; this test ensures it does not
        # get confused with missing observed_value.
        rules = absent_defaults_validator.validate_absent_default_rules([
            _base_rule(observed_value=""),
        ])
        self.assertEqual(rules[0]["observed_value"], "")


class AbsentDefaultsPacksAccessorTest(unittest.TestCase):
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

    def test_accessor_returns_empty_when_no_absent_defaults(self):
        self._write_pack("a", {"provider_prefixes": {"a_": "a"}})
        packs.reset()
        self.assertEqual(packs.absent_default_rules(), [])

if __name__ == "__main__":
    unittest.main()
