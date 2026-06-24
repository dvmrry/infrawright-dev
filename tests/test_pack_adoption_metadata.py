"""Validate committed pack adoption metadata loads through the pack accessors.

This is a metadata-only smoke test. It does not test behavior changes because the
metadata is guidance/validation only.
"""
import os
import unittest

from engine import packs


class PackAdoptionMetadataTest(unittest.TestCase):
    def setUp(self):
        self._prev = os.environ.get("INFRAWRIGHT_PACKS")
        if self._prev is not None:
            del os.environ["INFRAWRIGHT_PACKS"]
        packs.reset()

    def tearDown(self):
        if self._prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self._prev
        packs.reset()

    def test_google_provider_config_metadata_validates(self):
        reqs = packs.provider_config_requirements("google")
        self.assertTrue(reqs)
        req = next(r for r in reqs if r["id"] == "google_disable_attribution_label")
        self.assertEqual(req["provider"], "google")
        self.assertEqual(req["setting"], "add_terraform_attribution_label")
        self.assertEqual(req["value"], False)
        self.assertIn("terraform_labels.goog-terraform-provisioned", req["plan_paths"])

    def test_netbox_absent_default_metadata_validates(self):
        rules = packs.absent_default_rules("netbox")
        self.assertTrue(rules)
        ids = {r["id"] for r in rules}
        self.assertIn("netbox_device_empty_rack_face_placeholder", ids)
        rack_face = next(r for r in rules if r["id"] == "netbox_device_empty_rack_face_placeholder")
        self.assertEqual(rack_face["kind"], "provider_absent_placeholder")
        self.assertEqual(rack_face["action"], "manual_review_required")
        self.assertEqual(rack_face["observed_value"], "")

    def test_cloudflare_absent_default_metadata_validates(self):
        rules = packs.absent_default_rules("cloudflare")
        self.assertTrue(rules)
        ids = {r["id"] for r in rules}
        self.assertIn("cloudflare_zone_hold_singleton_default", ids)
        hold = next(r for r in rules if r["id"] == "cloudflare_zone_hold_singleton_default")
        self.assertEqual(hold["kind"], "provider_server_side_singleton_default")
        self.assertEqual(hold["action"], "manual_review_required")

    def test_cloudflare_dynamic_schema_metadata_validates(self):
        rules = packs.dynamic_schema_rules("cloudflare")
        self.assertTrue(rules)
        ids = {r["id"] for r in rules}
        self.assertIn("cloudflare_dns_record_data_flags_dynamic", ids)
        self.assertIn("cloudflare_workers_script_assets_run_worker_first_dynamic", ids)
        dns = next(r for r in rules if r["id"] == "cloudflare_dns_record_data_flags_dynamic")
        self.assertEqual(dns["kind"], "provider_observed_projection_unsafe")
        self.assertEqual(dns["action"], "manual_review_required")
        self.assertEqual(dns["provider_version_constraint"], "5.21.1")

    def test_zscaler_packs_still_have_no_adoption_metadata(self):
        self.assertEqual(packs.provider_config_requirements("zcc"), [])
        self.assertEqual(packs.absent_default_rules("zcc"), [])
        self.assertEqual(packs.dynamic_schema_rules("zcc"), [])


class PackMetadataBehaviorInvariantTest(unittest.TestCase):
    """System-level invariant: committed pack metadata must not authorize behavior.

    Validators, guidance, and reporting may exist, but no committed rule should
    use a mode or action that authorizes projection, omission, drift tolerance,
    provider rendering, assert-adoptable downgrade, secret handling, or
    placeholder rendering. These invariants must be updated only by a future
    behavior PR that explicitly promotes a narrow, reviewed action.
    """

    def setUp(self):
        self._prev = os.environ.get("INFRAWRIGHT_PACKS")
        if self._prev is not None:
            del os.environ["INFRAWRIGHT_PACKS"]
        packs.reset()

    def tearDown(self):
        if self._prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self._prev
        packs.reset()

    def _provider_config_evidence(self, req):
        remediation = req.get("remediation") or {}
        return remediation.get("evidence") or req.get("evidence")

    def _format_provider_config_offense(self, req, mode):
        return (
            "provider_config requirement %s for provider %s uses "
            "behavior-authorizing mode %s (evidence: %s)"
            % (req.get("id", req.get("setting", "?")), req.get("provider", "?"),
               mode, self._provider_config_evidence(req) or "none")
        )

    def _format_rule_offense(self, lane, rule, action):
        return (
            "%s rule %s for provider %s uses behavior-authorizing action %s "
            "(evidence: %s)"
            % (lane, rule.get("id", "?"), rule.get("provider", "?"),
               action, rule.get("evidence") or "none")
        )

    def test_provider_config_modes_are_guidance_only(self):
        # provider_config modes remain non-behavioral unless a future behavior PR
        # explicitly promotes one. renderable_default is still guidance-only today.
        allowed = set(["required_external", "renderable_default"])
        for req in packs.provider_config_requirements():
            remediation = req.get("remediation") or {}
            mode = remediation.get("mode")
            self.assertIn(
                mode, allowed,
                self._format_provider_config_offense(req, mode)
            )

    def test_absent_default_actions_are_non_remediating(self):
        allowed = set(["diagnostic_only", "manual_review_required", "preserve_explicit_falsey"])
        rejected = set([
            "omit_when_absent_in_api",
            "omit_when_provider_placeholder",
            "drop_empty_values",
            "drop_falsey",
            "normalize_defaults",
        ])
        for rule in packs.absent_default_rules():
            action = rule.get("action")
            self.assertIn(action, allowed,
                          self._format_rule_offense("absent_default", rule, action))
            self.assertNotIn(action, rejected,
                             self._format_rule_offense("absent_default", rule, action))

    def test_dynamic_schema_actions_are_non_remediating(self):
        allowed = set(["diagnostic_only", "manual_review_required"])
        rejected = set(["preserve_observed_scalar", "projection_omit_candidate"])
        for rule in packs.dynamic_schema_rules():
            action = rule.get("action")
            self.assertIn(action, allowed,
                          self._format_rule_offense("dynamic_schema", rule, action))
            self.assertNotIn(action, rejected,
                             self._format_rule_offense("dynamic_schema", rule, action))

    def test_sensitive_required_actions_are_non_remediating(self):
        allowed = set(["diagnostic_only", "manual_review_required"])
        rejected = set([
            "render_placeholder_block",
            "render_placeholder_attribute",
            "preserve_structure_without_secret_candidate",
            "operator_input_required_candidate",
            "project_sensitive",
            "copy_sensitive_from_state",
            "guess_secret",
            "suppress_sensitive_drift",
            "omit_sensitive_block",
            "accept_sensitive_unknown",
            "downgrade_assert_adoptable",
            "render_fake_secret",
        ])
        for rule in packs.sensitive_required_rules():
            action = rule.get("action")
            self.assertIn(action, allowed,
                          self._format_rule_offense("sensitive_required", rule, action))
            self.assertNotIn(action, rejected,
                             self._format_rule_offense("sensitive_required", rule, action))

    def test_no_sensitive_required_pack_metadata_exists_yet(self):
        # Grafana remains manual-review/unclassified. Update this test when the
        # first lab-proven sensitive-required pack metadata lands.
        self.assertEqual(packs.sensitive_required_rules(), [])

    def test_all_committed_metadata_has_evidence(self):
        # Behavior planning must remain lab/evidence-driven. Every committed
        # metadata item must reference a lab doc or sanitized fixture.
        for req in packs.provider_config_requirements():
            self.assertTrue(
                self._provider_config_evidence(req),
                "provider_config requirement %s for provider %s has no evidence"
                % (req.get("id", req.get("setting", "?")), req.get("provider", "?"))
            )
        for rule in packs.absent_default_rules():
            self.assertTrue(
                rule.get("evidence"),
                "absent_default rule %s for provider %s has no evidence"
                % (rule.get("id", "?"), rule.get("provider", "?"))
            )
        for rule in packs.dynamic_schema_rules():
            self.assertTrue(
                rule.get("evidence"),
                "dynamic_schema rule %s for provider %s has no evidence"
                % (rule.get("id", "?"), rule.get("provider", "?"))
            )


if __name__ == "__main__":
    unittest.main()
