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
            "sensitive_present": 0,
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
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(item["sensitive_blocked"], ["secret"])
        self.assertEqual(report["summary"]["required_missing"], 1)
        self.assertEqual(report["summary"]["sensitive_present"], 0)
        self.assertEqual(report["summary"]["sensitive_blocked"], 1)

    def test_derives_sensitive_block_marker_missing_from_projected(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "webhook": [{"url": "https://example.test/hook"}],
                    },
                    "sensitive_values": {"webhook": True},
                }
            },
            {"prod_app": {"name": "Prod"}},
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(item["sensitive_blocked"], ["webhook"])
        self.assertEqual(report["summary"]["sensitive_blocked"], 1)

    def test_derives_sensitive_leaf_marker_missing_from_projected(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "secure_json_data_encoded": "secret",
                    },
                    "sensitive_values": {
                        "secure_json_data_encoded": True,
                    },
                }
            },
            {"prod_app": {"name": "Prod"}},
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(
            item["sensitive_blocked"],
            ["secure_json_data_encoded"],
        )

    def test_truthy_non_boolean_sensitive_markers_are_ignored(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "one_marker": "secret",
                        "string_marker": "secret",
                    },
                    "sensitive_values": {
                        "one_marker": 1,
                        "string_marker": "true",
                    },
                }
            },
            {"prod_app": {"name": "Prod"}},
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(item["sensitive_blocked"], [])
        self.assertEqual(report["summary"]["sensitive_present"], 0)
        self.assertEqual(report["summary"]["sensitive_blocked"], 0)

    def test_derives_sensitive_list_leaf_with_normalized_path(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "webhook": [{"url": "https://example.test/hook"}],
                    },
                    "sensitive_values": {
                        "webhook": [{"url": True}],
                    },
                }
            },
            {"prod_app": {"name": "Prod"}},
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(item["sensitive_blocked"], ["webhook[].url"])

    def test_projected_sensitive_leaf_is_reported_present_not_blocked(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "secure_json_data_encoded": "secret",
                    },
                    "sensitive_values": {
                        "secure_json_data_encoded": True,
                    },
                }
            },
            {
                "prod_app": {
                    "name": "Prod",
                    "secure_json_data_encoded": "managed",
                }
            },
        )

        item = report["items"]["prod_app"]
        self.assertEqual(
            item["sensitive_present"],
            ["secure_json_data_encoded"],
        )
        self.assertEqual(item["sensitive_blocked"], [])
        self.assertEqual(report["summary"]["sensitive_present"], 1)
        self.assertEqual(report["summary"]["sensitive_blocked"], 0)

    def test_projected_sensitive_block_is_reported_present_not_blocked(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "webhook": [{"url": "https://example.test/hook"}],
                    },
                    "sensitive_values": {"webhook": True},
                }
            },
            {
                "prod_app": {
                    "name": "Prod",
                    "webhook": [{"url": "managed"}],
                }
            },
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], ["webhook"])
        self.assertEqual(item["sensitive_blocked"], [])

    def test_projected_sensitive_list_leaf_is_reported_present(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "webhook": [{"url": "https://example.test/hook"}],
                    },
                    "sensitive_values": {
                        "webhook": [{"url": True}],
                    },
                }
            },
            {
                "prod_app": {
                    "name": "Prod",
                    "webhook": [{"url": "managed"}],
                }
            },
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], ["webhook[].url"])
        self.assertEqual(item["sensitive_blocked"], [])

    def test_caller_supplied_sensitive_blocked_is_unioned(self):
        report = build_report(
            "sample_resource",
            {"prod_app": {"name": "Prod"}},
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "secure_json_data_encoded": "secret",
                    },
                    "sensitive_values": {
                        "secure_json_data_encoded": True,
                    },
                }
            },
            {"prod_app": {"name": "Prod"}},
            sensitive_blocked={
                "prod_app": ["manual.secret", "secure_json_data_encoded"],
            },
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["sensitive_present"], [])
        self.assertEqual(
            item["sensitive_blocked"],
            ["manual.secret", "secure_json_data_encoded"],
        )

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

    def test_container_projection_omit_does_not_suppress_raw_only_path(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "cbi_profile",
                            "reason": "provider cannot observe this block",
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

    def test_projection_omit_if_counts_as_policy_covered_omission(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit_if": [
                        {
                            "path": "ports[*].end",
                            "values": [0],
                            "reason": "provider sentinel",
                            "approved_by": "unit",
                        }
                    ],
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "provider diff guard",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })

        report = build_report(
            "sample_resource",
            {
                "prod_app": {
                    "name": "Prod",
                    "ports": [{"start": 443, "end": 0}],
                }
            },
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "ports": [{"start": 443, "end": 0}],
                        "res_categories": ["CAT_A"],
                    }
                }
            },
            {
                "prod_app": {
                    "name": "Prod",
                    "ports": [{"start": 443}],
                }
            },
            policy,
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["omitted_by_policy"], ["ports[].end"])
        self.assertEqual(item["provider_only_paths"], ["res_categories[]"])

    def test_container_projection_omit_classifies_provider_observed_leaves(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "webhook",
                            "reason": "provider marks notifier block sensitive",
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
                    "webhook": [
                        {
                            "url": "https://example.test/hook",
                            "vendor_only": "raw",
                        }
                    ],
                }
            },
            {
                "prod_app": {
                    "values": {
                        "name": "Prod",
                        "webhook": [
                            {
                                "uid": "notifier-1",
                                "url": "https://example.test/hook",
                            }
                        ],
                    }
                }
            },
            {"prod_app": {"name": "Prod"}},
            policy,
        )

        item = report["items"]["prod_app"]
        self.assertEqual(item["raw_only_paths"], ["webhook[].vendor_only"])
        self.assertEqual(item["provider_only_paths"], [])
        self.assertEqual(
            item["omitted_by_policy"],
            ["webhook[].uid", "webhook[].url"],
        )
        self.assertEqual(report["summary"]["omitted_by_policy"], 2)


if __name__ == "__main__":
    unittest.main()
