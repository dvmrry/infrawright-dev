import unittest

from engine.drift_policy import DriftPolicy
from engine.plan_eval import (
    BLOCKED,
    CLEAN,
    OPAQUE_UPDATE,
    TOLERATED,
    classify_plan,
    diff_paths,
    format_path,
)


def _update(before, after):
    return {
        "address": "sample_resource.this",
        "type": "sample_resource",
        "change": {"actions": ["update"], "before": before, "after": after},
    }


class PlanEvalTest(unittest.TestCase):
    def _paths(self, result):
        return result["findings"][0]["paths"]

    def test_update_without_policy_is_blocked(self):
        result = classify_plan({"resource_changes": [
            _update({"status": "UP"}, {"status": "DOWN"})
        ]})
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [("status",)])

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
        self.assertEqual(
            set(self._paths(result)),
            set([("vgw_telemetry", 0, "last_status_change")]),
        )

    def test_create_delete_replace_are_blocked_but_import_create_is_clean(self):
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
        replace = {
            "address": "sample_resource.this",
            "type": "sample_resource",
            "change": {
                "actions": ["delete", "create"],
                "before": {"name": "x"},
                "after": {"name": "y"},
            },
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
        self.assertEqual(classify_plan({"resource_changes": [replace]})["status"], BLOCKED)
        self.assertEqual(classify_plan({"resource_changes": [importing]})["status"], CLEAN)

    def test_unknown_paths_are_blocked_without_policy(self):
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
        result = classify_plan(plan)
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [("token",)])

    def test_update_ignores_unchanged_sensitive_marker_paths(self):
        plan = {
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {"name": "old", "secret": "same"},
                    "after": {"name": "new", "secret": "same"},
                    "before_sensitive": {"secret": True},
                    "after_sensitive": {"secret": True},
                },
            }]
        }
        result = classify_plan(plan)
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [("name",)])

    def test_sensitive_changed_path_is_reported_when_diff_is_visible(self):
        plan = {
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {"secret": "old"},
                    "after": {"secret": "new"},
                    "before_sensitive": {"secret": True},
                    "after_sensitive": {"secret": True},
                },
            }]
        }
        result = classify_plan(plan)
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [("secret",)])

    def test_update_with_no_extracted_paths_is_opaque_and_blocked(self):
        result = classify_plan({"resource_changes": [
            _update({"status": "same"}, {"status": "same"})
        ]})
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [(OPAQUE_UPDATE,)])
        self.assertEqual(format_path(self._paths(result)[0]), "<opaque_update>")

    def test_root_unknown_update_is_opaque_and_blocked(self):
        plan = {
            "resource_changes": [{
                "address": "sample_resource.this",
                "type": "sample_resource",
                "change": {
                    "actions": ["update"],
                    "before": {"name": "same"},
                    "after": {"name": "same"},
                    "after_unknown": True,
                },
            }]
        }
        result = classify_plan(plan)
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [(OPAQUE_UPDATE,)])

    def test_policy_does_not_accidentally_tolerate_opaque_update(self):
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
        result = classify_plan({"resource_changes": [
            _update({"status": "same"}, {"status": "same"})
        ]}, policy=policy)
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [(OPAQUE_UPDATE,)])

    def test_unsupported_action_is_blocked(self):
        result = classify_plan({"resource_changes": [{
            "address": "sample_resource.this",
            "type": "sample_resource",
            "change": {
                "actions": ["read"],
                "before": {"status": "old"},
                "after": {"status": "new"},
            },
        }]})
        self.assertEqual(result["status"], BLOCKED)
        self.assertEqual(self._paths(result), [("<unsupported_action>",)])

    def test_diff_paths_walks_dicts_and_lists(self):
        self.assertEqual(
            diff_paths({"a": [{"b": 1}]}, {"a": [{"b": 2}]}),
            [("a", 0, "b")],
        )


if __name__ == "__main__":
    unittest.main()
