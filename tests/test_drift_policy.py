import unittest

from engine.drift_policy import DriftPolicy, DriftPolicyError, parse_path


class DriftPolicyTest(unittest.TestCase):
    def test_parse_supported_paths(self):
        self.assertEqual(parse_path("foo.bar"), ("foo", "bar"))
        self.assertEqual(parse_path("foo[*].bar"), ("foo", "*", "bar"))
        self.assertEqual(parse_path("foo[0].bar"), ("foo", 0, "bar"))
        self.assertEqual(parse_path('tags["Name"]'), ("tags", "Name"))

    def test_validation_requires_reason_and_approver(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "projection_omit": [{"path": "name", "approved_by": "unit"}]
                    }
                },
            })
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({
                "version": 1,
                "resource_types": {
                    "sample_resource": {
                        "plan_tolerate": [{"path": "name", "reason": "test"}]
                    }
                },
            })

    def test_unsupported_version_fails(self):
        with self.assertRaises(DriftPolicyError):
            DriftPolicy({"version": 2, "resource_types": {}})

    def test_matching_and_stale_entries(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {"path": "description", "reason": "test", "approved_by": "unit"}
                    ],
                    "plan_tolerate": [
                        {
                            "path": "rules[*].status",
                            "actions": ["update"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })
        self.assertTrue(policy.projection_omits("sample_resource", ("description",)))
        self.assertTrue(
            policy.tolerates_plan_path("sample_resource", ("rules", 0, "status"), "update")
        )
        self.assertFalse(
            policy.tolerates_plan_path("sample_resource", ("rules", "0", "status"), "update")
        )
        self.assertEqual(policy.stale_entries(), [])

    def test_stale_entries_reports_unmatched_policy(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "description",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "plan_tolerate": [
                        {
                            "path": "rules[*].status",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                },
                "other_resource": {
                    "plan_tolerate": [
                        {
                            "path": "status",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })
        self.assertEqual(
            policy.stale_entries(),
            [
                ("other_resource", "plan_tolerate", "status"),
                ("sample_resource", "projection_omit", "description"),
                ("sample_resource", "plan_tolerate", "rules[*].status"),
            ],
        )
        self.assertEqual(
            policy.stale_entries(
                resource_types={"sample_resource"}, modes=("plan_tolerate",)
            ),
            [("sample_resource", "plan_tolerate", "rules[*].status")],
        )


if __name__ == "__main__":
    unittest.main()
