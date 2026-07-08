import unittest

from engine.drift_policy import DriftPolicy
from engine import state_project
from engine.state_project import ProjectionError, project_item


FAKE_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "description": {"type": "string", "optional": True},
            "enabled": {"type": "bool", "optional": True},
            "count": {"type": "number", "optional": True},
            "labels": {"type": ["map", "string"], "optional": True},
            "labels_copy": {"type": ["map", "string"], "optional": True},
            "dest_ip_categories": {"type": ["set", "string"], "optional": True},
            "res_categories": {"type": ["set", "string"], "optional": True},
            "optional_null": {"type": "string", "optional": True},
            "computed_only": {"type": "string", "computed": True},
        },
        "block_types": {
            "settings": {
                "nesting_mode": "single",
                "block": {
                    "attributes": {
                        "mode": {"type": "string", "required": True},
                        "flag": {"type": "bool", "optional": True},
                        "computed_nested": {"type": "string", "computed": True},
                    }
                },
            },
            "rules": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "name": {"type": "string", "required": True},
                        "order": {"type": "number", "optional": True},
                        "computed_rule": {"type": "string", "computed": True},
                    }
                },
            },
            "ports": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "start": {"type": "number", "optional": True},
                        "end": {"type": "number", "optional": True},
                    }
                },
            },
        },
    }
}


class StateProjectTest(unittest.TestCase):
    def setUp(self):
        self.prev = state_project.load_resource
        self.prev_schema_paths = state_project.schema_paths.load_resource
        state_project.load_resource = lambda resource_type: FAKE_SCHEMA
        state_project.schema_paths.load_resource = (
            lambda resource_type: FAKE_SCHEMA
        )

    def tearDown(self):
        state_project.load_resource = self.prev
        state_project.schema_paths.load_resource = self.prev_schema_paths

    def test_projects_schema_inputs_and_preserves_false_zero_empty_list(self):
        out = project_item("sample_resource", {
            "id": "123",
            "name": "Prod",
            "description": "",
            "enabled": False,
            "count": 0,
            "optional_null": None,
            "computed_only": "ignored",
            "rules": [],
        })
        self.assertEqual(out, {
            "name": "Prod",
            "description": "",
            "enabled": False,
            "count": 0,
            "rules": [],
        })

    def test_required_missing_fails(self):
        with self.assertRaises(ProjectionError):
            project_item("sample_resource", {"description": "x"})

    def test_nested_single_and_list_blocks_project_recursively(self):
        out = project_item("sample_resource", {
            "name": "Prod",
            "settings": {
                "mode": "strict",
                "flag": False,
                "computed_nested": "ignored",
            },
            "rules": [
                {"name": "one", "order": 0, "computed_rule": "ignored"},
                {"name": "two", "order": 1},
            ],
        })
        self.assertEqual(out["settings"], {"mode": "strict", "flag": False})
        self.assertEqual(
            out["rules"],
            [{"name": "one", "order": 0}, {"name": "two", "order": 1}],
        )

    def test_projection_omit_removes_optional_paths(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "description",
                            "reason": "test",
                            "approved_by": "unit",
                        },
                        {
                            "path": "rules[*].order",
                            "reason": "test",
                            "approved_by": "unit",
                        },
                    ]
                }
            },
        })
        out = project_item("sample_resource", {
            "name": "Prod",
            "description": "drop",
            "rules": [{"name": "one", "order": 7}],
        }, policy=policy)
        self.assertEqual(out, {"name": "Prod", "rules": [{"name": "one"}]})

    def test_projection_omit_optional_sensitive_attribute_drops_without_error(self):
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
                    ]
                }
            },
        })

        out = project_item(
            "sample_resource",
            {"name": "Prod", "description": "secret"},
            sensitive_values={"description": True},
            policy=policy,
        )

        self.assertEqual(out, {"name": "Prod"})
        self.assertEqual(policy.stale_entries(modes=("projection_omit",)), [])

    def test_projection_omit_required_path_fails(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {"path": "name", "reason": "test", "approved_by": "unit"}
                    ]
                }
            },
        })
        with self.assertRaisesRegex(
                ProjectionError,
                "policy cannot projection_omit required path name"):
            project_item("sample_resource", {"name": "Prod"}, policy=policy)

    def test_projection_sync_fills_absent_target(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "dest_ip_categories": ["CAT_A"],
        }, policy=policy)

        self.assertEqual(out["res_categories"], ["CAT_A"])
        self.assertEqual(policy.stale_entries(modes=("projection_sync",)), [])

    def test_projection_sync_fills_empty_list_target(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "dest_ip_categories": ["CAT_A"],
            "res_categories": [],
        }, policy=policy)

        self.assertEqual(out["res_categories"], ["CAT_A"])
        self.assertEqual(policy.stale_entries(modes=("projection_sync",)), [])

    def test_projection_sync_noops_when_target_already_equals_source(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "dest_ip_categories": ["CAT_A"],
            "res_categories": ["CAT_A"],
        }, policy=policy)

        self.assertEqual(out["res_categories"], ["CAT_A"])
        self.assertEqual(
            policy.stale_entries(modes=("projection_sync",)),
            [("sample_resource", "projection_sync", "res_categories")],
        )

    def test_projection_sync_never_overwrites_different_target(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "dest_ip_categories": ["CAT_A"],
            "res_categories": ["CAT_B"],
        }, policy=policy)

        self.assertEqual(out["res_categories"], ["CAT_B"])
        self.assertEqual(
            policy.stale_entries(modes=("projection_sync",)),
            [("sample_resource", "projection_sync", "res_categories")],
        )

    def test_projection_sync_deepcopy_isolates_target_from_source(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "labels_copy",
                            "source_path": "labels",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "labels": {"app": "api"},
        }, policy=policy)
        out["labels_copy"]["app"] = "worker"

        self.assertEqual(out["labels"], {"app": "api"})
        self.assertEqual(out["labels_copy"], {"app": "worker"})

    def test_projection_omit_if_removes_matching_sentinel_leaf(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit_if": [
                        {
                            "path": "ports[*].end",
                            "values": [0],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "ports": [
                {"start": 443, "end": 0},
                {"end": 0},
                {"start": 80, "end": 81},
            ],
        }, policy=policy)

        self.assertEqual(
            out["ports"],
            [{"start": 443}, {}, {"start": 80, "end": 81}],
        )
        self.assertEqual(
            policy.stale_entries(modes=("projection_omit_if",)),
            [],
        )

    def test_projection_omit_if_uses_strict_json_equality(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit_if": [
                        {
                            "path": "enabled",
                            "values": [0],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "enabled": False,
        }, policy=policy)

        self.assertEqual(out["enabled"], False)
        self.assertEqual(
            policy.stale_entries(modes=("projection_omit_if",)),
            [("sample_resource", "projection_omit_if", "enabled")],
        )

    def test_projection_omit_if_required_path_fails(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit_if": [
                        {
                            "path": "name",
                            "values": ["Prod"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        with self.assertRaisesRegex(
                ProjectionError,
                "refusing to conditionally omit required attribute name "
                "of sample_resource"):
            project_item("sample_resource", {"name": "Prod"}, policy=policy)

    def test_projection_sync_non_input_target_fails(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "computed_only",
                            "source_path": "description",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        with self.assertRaisesRegex(
                ProjectionError,
                "not a writable input attribute"):
            project_item("sample_resource", {
                "name": "Prod",
                "description": "source",
            }, policy=policy)

    def test_projection_sync_schema_type_mismatch_fails(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "description",
                            "source_path": "count",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        with self.assertRaisesRegex(ProjectionError, "schema types differ"):
            project_item("sample_resource", {
                "name": "Prod",
                "count": 1,
            }, policy=policy)

    def test_projection_sync_then_omit_if_can_strip_synced_value(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "description",
                            "source_path": "optional_null",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "projection_omit_if": [
                        {
                            "path": "description",
                            "values": ["synced sentinel"],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "optional_null": "synced sentinel",
        }, policy=policy)

        self.assertEqual(out, {
            "name": "Prod",
            "optional_null": "synced sentinel",
        })
        self.assertEqual(
            policy.stale_entries(
                modes=("projection_sync", "projection_omit_if")
            ),
            [],
        )

    def test_combined_projection_policy_applies_in_fixed_order(self):
        policy = DriftPolicy({
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_sync": [
                        {
                            "target_path": "res_categories",
                            "source_path": "dest_ip_categories",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "projection_omit_if": [
                        {
                            "path": "ports[*].end",
                            "values": [0],
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "projection_omit": [
                        {
                            "path": "description",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                    "plan_tolerate": [
                        {
                            "path": "labels[\"app\"]",
                            "reason": "test",
                            "approved_by": "unit",
                        }
                    ],
                }
            },
        })

        out = project_item("sample_resource", {
            "name": "Prod",
            "description": "drop",
            "dest_ip_categories": ["CAT_A"],
            "labels": {"app": "api"},
            "ports": [{"start": 443, "end": 0}],
        }, policy=policy)
        policy.tolerates_plan_path(
            "sample_resource", ("labels", "app"), "update"
        )

        self.assertEqual(out, {
            "name": "Prod",
            "dest_ip_categories": ["CAT_A"],
            "labels": {"app": "api"},
            "ports": [{"start": 443}],
            "res_categories": ["CAT_A"],
        })
        self.assertEqual(policy.stale_entries(), [])

    def test_sensitive_input_fails_closed(self):
        with self.assertRaises(ProjectionError):
            project_item(
                "sample_resource",
                {"name": "Prod", "description": "secret"},
                sensitive_values={"description": True},
            )

    def test_nested_sensitive_input_fails_closed(self):
        with self.assertRaises(ProjectionError):
            project_item(
                "sample_resource",
                {"name": "Prod", "settings": {"mode": "secret"}},
                sensitive_values={"settings": {"mode": True}},
            )

    def test_single_block_list_shaped_sensitive_input_fails_closed(self):
        with self.assertRaises(ProjectionError):
            project_item(
                "sample_resource",
                {"name": "Prod", "settings": [{"mode": "secret"}]},
                sensitive_values={"settings": [{"mode": True}]},
            )

    def test_sensitive_list_element_fails_closed(self):
        with self.assertRaises(ProjectionError):
            project_item(
                "sample_resource",
                {"name": "Prod", "rules": [{"name": "secret"}]},
                sensitive_values={"rules": [True]},
            )

    def test_all_false_sensitive_map_does_not_false_positive(self):
        out = project_item(
            "sample_resource",
            {
                "name": "Prod",
                "labels": {"app": "grafana"},
            },
            sensitive_values={
                "labels": {"app": False},
            },
        )
        self.assertEqual(out["labels"], {"app": "grafana"})


if __name__ == "__main__":
    unittest.main()
