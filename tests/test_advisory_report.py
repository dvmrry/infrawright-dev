import unittest

from engine.advisory_report import build_report
from engine.drift_policy import DriftPolicy


class AdvisoryReportTest(unittest.TestCase):
    def test_builds_raw_provider_projected_advisory(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "metadata.generate_name",
                            "reason": "provider-side generated name",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        report = build_report(
            "sample_resource",
            {
                "prod_app": {
                    "name": "Prod App",
                    "description": "raw",
                    "enabled": True,
                    "cbi_profile": {"id": "cbi-1"},
                    "security_extra": {"mode": "strict"},
                    "metadata": {"generate_name": "prod-generated"},
                }
            },
            {
                "prod_app": {
                    "values": {
                        "name": "Prod App",
                        "description": "provider",
                        "enabled": True,
                        "metadata": {"generate_name": "prod-generated"},
                        "provider_default": {"enabled": True},
                    },
                    "sensitive_values": {},
                }
            },
            {
                "prod_app": {
                    "name": "Prod App",
                    "description": "provider",
                    "enabled": True,
                }
            },
            policy,
        )

        self.assertEqual(report["resource_type"], "sample_resource")
        self.assertEqual(report["summary"], {
            "items": 1,
            "raw_only_paths": 2,
            "provider_only_paths": 1,
            "projected_paths": 3,
            "omitted_by_policy": 1,
            "required_missing": 0,
            "sensitive_blocked": 0,
        })
        self.assertEqual(
            report["items"]["prod_app"]["raw_only_paths"],
            ["cbi_profile.id", "security_extra.mode"],
        )
        self.assertEqual(
            report["items"]["prod_app"]["provider_only_paths"],
            ["provider_default.enabled"],
        )
        self.assertEqual(
            report["items"]["prod_app"]["projected_paths"],
            ["description", "enabled", "name"],
        )
        self.assertEqual(
            report["items"]["prod_app"]["omitted_by_policy"],
            ["metadata.generate_name"],
        )

    def test_accepts_required_and_sensitive_side_inputs(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {"prod_app": {"values": {"name": "Prod"}}},
            {"prod_app": {"name": "Prod"}},
            required_missing={"prod_app": ["settings.mode"]},
            sensitive_blocked={"prod_app": ["secret"]},
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["required_missing"], ["settings.mode"])
        self.assertEqual(item["sensitive_blocked"], ["secret"])
        self.assertEqual(report["summary"]["required_missing"], 1)
        self.assertEqual(report["summary"]["sensitive_blocked"], 1)

    def test_projection_omit_does_not_suppress_raw_only_path(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "cbi_profile.id",
                            "reason": "provider cannot observe this field",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod", "cbi_profile": {"id": "cbi-1"}}},
            {"prod_app": {"values": {"name": "Prod"}}},
            {"prod_app": {"name": "Prod"}},
            policy,
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["raw_only_paths"], ["cbi_profile.id"])
        self.assertEqual(item["omitted_by_policy"], [])

    def test_projection_omit_classifies_provider_observed_unprojected_path(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "metadata.generate_name",
                            "reason": "provider-generated value",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        report = build_report(
            "sample_resource",
            {
                "prod_app": {
                    "name": "Prod",
                    "metadata": {"generate_name": "prod-generated"},
                }
            },
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "metadata": {"generate_name": "prod-generated"},
                    }
                }
            },
            {"prod_app": {"name": "Prod"}},
            policy,
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["raw_only_paths"], [])
        self.assertEqual(item["provider_only_paths"], [])
        self.assertEqual(item["omitted_by_policy"], ["metadata.generate_name"])


if __name__ == "__main__":
    unittest.main()
