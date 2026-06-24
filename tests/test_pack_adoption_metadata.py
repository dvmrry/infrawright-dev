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


if __name__ == "__main__":
    unittest.main()
