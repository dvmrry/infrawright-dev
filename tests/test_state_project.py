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
        },
    }
}


class StateProjectTest(unittest.TestCase):
    def setUp(self):
        self.prev = state_project.load_resource
        state_project.load_resource = lambda resource_type: FAKE_SCHEMA

    def tearDown(self):
        state_project.load_resource = self.prev

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
        with self.assertRaises(ProjectionError):
            project_item("sample_resource", {"name": "Prod"}, policy=policy)

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
