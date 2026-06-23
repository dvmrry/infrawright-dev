import unittest

from engine.drift_policy import DriftPolicy
from engine.plan_eval import BLOCKED, CLEAN, TOLERATED, classify_plan, diff_paths


def _update(before, after):
    return {
        "address": "sample_resource.this",
        "type": "sample_resource",
        "change": {"actions": ["update"], "before": before, "after": after},
    }


class PlanEvalTest(unittest.TestCase):
    def test_update_without_policy_is_blocked(self):
        result = classify_plan({"resource_changes": [
            _update({"status": "UP"}, {"status": "DOWN"})
        ]})
        self.assertEqual(result["status"], BLOCKED)

    def test_policy_tolerated_update_is_clean_with_tolerated_drift(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [
                        {
                            "path": "vgw_telemetry[*].status",
                            "actions": ["update"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })
        result = classify_plan({"resource_changes": [
            _update(
                {"vgw_telemetry": [{"status": "UP"}]},
                {"vgw_telemetry": [{"status": "DOWN"}]},
            )
        ]}, policy=policy)
        self.assertEqual(result["status"], TOLERATED)

    def test_resource_drift_uses_same_policy_classification(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [
                        {
                            "path": "status",
                            "actions": ["update"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })
        result = classify_plan({"resource_drift": [
            _update({"status": "UP"}, {"status": "DOWN"})
        ]}, policy=policy)
        self.assertEqual(result["status"], TOLERATED)
        self.assertEqual(result["findings"][0]["source"], "resource_drift")

    def test_partial_policy_match_is_blocked(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "plan_tolerate": [
                        {
                            "path": "vgw_telemetry[*].status",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })
        result = classify_plan({"resource_changes": [
            _update(
                {"vgw_telemetry": [{"status": "UP", "last_status_change": "old"}]},
                {"vgw_telemetry": [{"status": "DOWN", "last_status_change": "new"}]},
            )
        ]}, policy=policy)
        self.assertEqual(result["status"], BLOCKED)

    def test_create_delete_are_blocked_but_import_create_is_clean(self):
        create = {
            "address": "sample_resource.this",
            "type": "sample_resource",
            "change": {"actions": ["create"], "before": None, "after": {"name": "x"}},
        }
        delete = {
            "address": "sample_resource.this",
            "type": "sample_resource",
            "change": {"actions": ["delete"], "before": {"name": "x"}, "after": None},
        }
        importing = {
            "address": "sample_resource.this",
            "type": "sample_resource",
            "change": {
                "actions": ["create"],
                "before": None,
                "after": {"name": "x"},
                "importing": {"id": "123"},
            },
        }
        self.assertEqual(classify_plan({"resource_changes": [create]})["status"], BLOCKED)
        self.assertEqual(classify_plan({"resource_changes": [delete]})["status"], BLOCKED)
        self.assertEqual(classify_plan({"resource_changes": [importing]})["status"], CLEAN)

    def test_unknown_or_sensitive_paths_are_blocked_without_policy(self):
        plan = {
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {"name": "old"},
                    "after": {"name": "old"},
                    "after_unknown": {"token": True},
                },
            }]
        }
        self.assertEqual(classify_plan(plan)["status"], BLOCKED)

    def test_diff_paths_walks_dicts_and_lists(self):
        self.assertEqual(
            diff_paths({"a": [{"b": 1}]}, {"a": [{"b": 2}]}),
            [("a", 0, "b")],
        )


if __name__ == "__main__":
    unittest.main()
