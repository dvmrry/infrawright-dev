"""Tests for the adoption metadata inventory report."""
import json
import os
import shutil
import tempfile
import unittest

from engine import adoption_inventory_report
from engine import packs


class RealPackInventoryTest(unittest.TestCase):
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

    def _find(self, report, cls=None, **kwargs):
        if cls is not None:
            kwargs["class"] = cls
        matches = []
        for item in report.get("inventory", []):
            if all(item.get(k) == v for k, v in kwargs.items()):
                matches.append(item)
        return matches

    def test_aggregates_google_provider_config_metadata(self):
        report = adoption_inventory_report.build_report()
        items = self._find(report, provider="google", cls="provider_config")
        self.assertEqual(len(items), 1)
        item = items[0]
        self.assertEqual(item["kind"], "provider_argument")
        self.assertEqual(item["action"], "required_external")
        self.assertEqual(item["behavior_effect"], "guidance_only")
        self.assertEqual(item["setting"], "add_terraform_attribution_label")
        self.assertEqual(item["resource_types"], [
            "google_bigquery_dataset",
            "google_pubsub_subscription",
            "google_pubsub_topic",
        ])

    def test_aggregates_netbox_absent_default_metadata(self):
        report = adoption_inventory_report.build_report()
        items = self._find(report, provider="netbox", cls="absent_default")
        self.assertTrue(items)
        item = self._find(report, provider="netbox", cls="absent_default",
                          path="rack_face")[0]
        self.assertEqual(item["kind"], "provider_absent_placeholder")
        self.assertEqual(item["action"], "manual_review_required")
        self.assertEqual(item["behavior_effect"], "validation_only")
        self.assertEqual(item["observed_value"], "")

    def test_aggregates_cloudflare_absent_default_metadata(self):
        report = adoption_inventory_report.build_report()
        item = self._find(report, provider="cloudflare", cls="absent_default",
                          path="hold")[0]
        self.assertEqual(item["kind"], "provider_server_side_singleton_default")
        self.assertEqual(item["action"], "manual_review_required")

    def test_aggregates_cloudflare_dynamic_schema_metadata(self):
        report = adoption_inventory_report.build_report()
        item = self._find(report, provider="cloudflare", cls="dynamic_schema",
                          path="data.flags")[0]
        self.assertEqual(item["kind"], "provider_observed_projection_unsafe")
        self.assertEqual(item["action"], "manual_review_required")
        self.assertEqual(item["ownership"], "unknown")
        self.assertEqual(item["provider_version_constraint"], "5.21.1")

    def test_filter_by_provider(self):
        report = adoption_inventory_report.build_report(provider="netbox")
        for item in report["inventory"]:
            self.assertEqual(item["provider"], "netbox")
        self.assertTrue(report["inventory"])

    def test_filter_by_class(self):
        report = adoption_inventory_report.build_report(metadata_class="dynamic_schema")
        for item in report["inventory"]:
            self.assertEqual(item["class"], "dynamic_schema")
        self.assertTrue(report["inventory"])

    def test_filter_by_resource_type(self):
        report = adoption_inventory_report.build_report(resource_type="netbox_device")
        for item in report["inventory"]:
            self.assertTrue(
                item.get("resource_type") == "netbox_device" or
                "netbox_device" in (item.get("resource_types") or [])
            )
        self.assertTrue(report["inventory"])

    def test_deterministic_json_output(self):
        report1 = adoption_inventory_report.build_report()
        report2 = adoption_inventory_report.build_report()
        self.assertEqual(
            adoption_inventory_report.to_json(report1),
            adoption_inventory_report.to_json(report2),
        )
        self.assertEqual(
            report1["inventory"][0]["provider"],
            report2["inventory"][0]["provider"],
        )

    def test_markdown_output_includes_provider_class_action_evidence(self):
        report = adoption_inventory_report.build_report()
        md = adoption_inventory_report.to_markdown(report)
        self.assertIn("Provider", md)
        self.assertIn("Class", md)
        self.assertIn("Action", md)
        self.assertIn("Evidence", md)
        self.assertIn("cloudflare", md)

    def test_summary_counts_by_class(self):
        report = adoption_inventory_report.build_report()
        by_class = report["summary"]["by_class"]
        self.assertIn("provider_config", by_class)
        self.assertIn("absent_default", by_class)
        self.assertIn("dynamic_schema", by_class)

    def test_no_cross_class_warnings_for_committed_metadata(self):
        report = adoption_inventory_report.build_report()
        warnings = [d for d in report["diagnostics"] if d["severity"] == "warning"]
        self.assertEqual(warnings, [], "committed metadata should not overlap across classes")

    def test_does_not_call_behavior_or_terraform(self):
        # This test documents the boundary: the report is read-only metadata
        # aggregation. It does not import projection, drift, or execution modules.
        report = adoption_inventory_report.build_report()
        self.assertIn("inventory", report)
        self.assertIn("diagnostics", report)
        for item in report["inventory"]:
            self.assertIn(item["behavior_effect"], ("guidance_only", "validation_only"))


class SyntheticDiagnosticTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()

    def tearDown(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_pack(self, name, manifest):
        d = os.path.join(self.tmp, name)
        os.makedirs(d)
        with open(os.path.join(d, "pack.json"), "w", encoding="utf-8") as f:
            json.dump(manifest, f)
        packs.reset()

    def test_warning_for_absent_default_and_dynamic_schema_same_path(self):
        self._write_pack("dup", {
            "provider_prefixes": {"test_": "test"},
            "provider_sources": {"test": "example/test"},
            "absent_defaults": {
                "rules": [{
                    "id": "test_absent",
                    "provider": "test",
                    "resource_type": "test_resource",
                    "path": "overlap_path",
                    "kind": "provider_absent_placeholder",
                    "observed_value": "",
                    "action": "manual_review_required",
                    "evidence": "docs/lab.md",
                    "reason": "absent overlap",
                }]
            },
            "dynamic_schema": {
                "rules": [{
                    "id": "test_dynamic",
                    "provider": "test",
                    "provider_version_constraint": "1.0.0",
                    "resource_type": "test_resource",
                    "path": "overlap_path",
                    "kind": "provider_observed_projection_unsafe",
                    "ownership": "unknown",
                    "action": "manual_review_required",
                    "evidence": "docs/lab.md",
                    "reason": "dynamic overlap",
                }]
            },
        })
        report = adoption_inventory_report.build_report()
        warnings = [d for d in report["diagnostics"] if d["severity"] == "warning"]
        self.assertTrue(warnings)
        self.assertTrue(any(
            "absent_default" in d.get("classes", []) and
            "dynamic_schema" in d.get("classes", [])
            for d in warnings
        ))

    def test_warning_for_provider_config_plan_path_matching_metadata_path(self):
        self._write_pack("pc", {
            "provider_prefixes": {"test_": "test"},
            "provider_sources": {"test": "example/test"},
            "provider_config": {
                "requirements": [{
                    "id": "test_pc",
                    "provider": "test",
                    "setting": "some_setting",
                    "value": False,
                    "reason": "plan path matches metadata path",
                    "resource_types": ["test_resource"],
                    "plan_paths": ["metadata_path"],
                }]
            },
            "absent_defaults": {
                "rules": [{
                    "id": "test_absent",
                    "provider": "test",
                    "resource_type": "test_resource",
                    "path": "metadata_path",
                    "kind": "provider_absent_placeholder",
                    "observed_value": "",
                    "action": "manual_review_required",
                    "evidence": "docs/lab.md",
                    "reason": "path overlap",
                }]
            },
        })
        report = adoption_inventory_report.build_report()
        warnings = [d for d in report["diagnostics"] if d["severity"] == "warning"]
        self.assertTrue(warnings)
        self.assertTrue(any(
            "provider_config" in d.get("classes", []) and
            "absent_default" in d.get("classes", [])
            for d in warnings
        ))

    def test_info_for_shared_evidence_across_classes(self):
        self._write_pack("shared", {
            "provider_prefixes": {"test_": "test"},
            "provider_sources": {"test": "example/test"},
            "provider_config": {
                "requirements": [{
                    "id": "test_pc",
                    "provider": "test",
                    "setting": "some_setting",
                    "value": False,
                    "reason": "shared evidence",
                    "resource_types": ["test_resource"],
                    "plan_paths": ["plan_path"],
                    "remediation": {
                        "kind": "provider_argument",
                        "mode": "required_external",
                        "evidence": "docs/shared.md",
                        "reason": "shared evidence"
                    }
                }]
            },
            "absent_defaults": {
                "rules": [{
                    "id": "test_absent",
                    "provider": "test",
                    "resource_type": "test_resource",
                    "path": "other_path",
                    "kind": "provider_absent_placeholder",
                    "observed_value": "",
                    "action": "manual_review_required",
                    "evidence": "docs/shared.md",
                    "reason": "shared evidence",
                }]
            },
        })
        report = adoption_inventory_report.build_report()
        infos = [d for d in report["diagnostics"] if d["severity"] == "info"]
        self.assertTrue(infos)
        self.assertTrue(any(
            d.get("evidence") == "docs/shared.md"
            for d in infos
        ))


class CLISmokeTest(unittest.TestCase):
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

    def _run_cli(self, args):
        import subprocess
        cmd = ["python3", "scripts/adoption-inventory-report.py"] + args
        result = subprocess.run(
            cmd,
            cwd=os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            capture_output=True,
            text=True,
        )
        return result

    def test_cli_json_output(self):
        result = self._run_cli(["--format", "json", "--provider", "cloudflare"])
        self.assertEqual(result.returncode, 0)
        self.assertIn("cloudflare", result.stdout)
        json.loads(result.stdout)  # validates JSON

    def test_cli_markdown_output(self):
        result = self._run_cli(["--format", "markdown", "--provider", "google"])
        self.assertEqual(result.returncode, 0)
        self.assertIn("google", result.stdout)
        self.assertIn("|", result.stdout)


if __name__ == "__main__":
    unittest.main()
