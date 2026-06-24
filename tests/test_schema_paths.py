import unittest

from engine import schema_paths


FAKE_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "description": {"type": "string", "optional": True},
            "labels": {"type": ["map", "string"], "optional": True},
            "settings": {
                "type": ["object", {"mode": "string"}],
                "optional": True,
            },
        },
        "block_types": {
            "rules": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "action": {"type": "string", "required": True},
                        "priority": {"type": "number", "optional": True},
                    }
                },
            },
            "credentials": {
                "nesting_mode": "single",
                "min_items": 1,
                "block": {
                    "attributes": {
                        "secret": {"type": "string", "required": True},
                    }
                },
            },
        },
    }
}


class SchemaPathsTest(unittest.TestCase):
    def setUp(self):
        self.prev_load = schema_paths.load_resource
        schema_paths.load_resource = lambda resource_type: FAKE_SCHEMA

    def tearDown(self):
        schema_paths.load_resource = self.prev_load

    def test_collection_path_spellings_format_the_same(self):
        self.assertEqual(
            schema_paths.format_path(schema_paths.parse_report_path("foo[].bar")),
            "foo[].bar",
        )
        self.assertEqual(
            schema_paths.format_path(schema_paths.parse_report_path("foo[*].bar")),
            "foo[].bar",
        )
        self.assertEqual(
            schema_paths.format_path(schema_paths.parse_report_path("foo[0].bar")),
            "foo[].bar",
        )

    def test_adjacent_collection_selectors_normalize(self):
        self.assertEqual(
            schema_paths.parse_report_path("foo[][]"),
            ("foo", schema_paths.LIST_MARKER, schema_paths.LIST_MARKER),
        )
        self.assertEqual(
            schema_paths.parse_report_path("foo[0][1]"),
            ("foo", schema_paths.LIST_MARKER, schema_paths.LIST_MARKER),
        )
        self.assertEqual(
            schema_paths.parse_report_path("foo[*][0]"),
            ("foo", schema_paths.LIST_MARKER, schema_paths.LIST_MARKER),
        )
        self.assertEqual(
            schema_paths.format_path(schema_paths.parse_report_path("foo[0][1]")),
            "foo[][]",
        )

    def test_quoted_map_selectors_format_to_dotted_diagnostics(self):
        self.assertEqual(
            schema_paths.parse_report_path('tags["env"]'),
            ("tags", "env"),
        )
        self.assertEqual(
            schema_paths.format_path(
                schema_paths.parse_report_path(
                    'terraform_labels["goog-terraform-provisioned"]'
                )
            ),
            "terraform_labels.goog-terraform-provisioned",
        )

    def test_numeric_path_tuple_formats_with_list_marker(self):
        self.assertEqual(schema_paths.format_path(("foo", 0, "bar")), "foo[].bar")

    def test_dotted_map_key_behavior_is_path_segments(self):
        parsed = schema_paths.parse_report_path("labels.a.b")
        self.assertEqual(parsed, ("labels", "a", "b"))
        self.assertEqual(schema_paths.format_path(parsed), "labels.a.b")

    def test_container_paths_include_dicts_and_lists(self):
        self.assertEqual(
            schema_paths.container_paths({
                "rules": [
                    {"metadata": {"owner": "lab"}},
                ],
            }),
            set(["rules", "rules[]", "rules[].metadata"]),
        )

    def test_schema_status_for_top_level_attributes(self):
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "id"),
            "computed_only",
        )
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "name"),
            "required",
        )
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "description"),
            "optional",
        )

    def test_schema_status_for_block_modes(self):
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "rules"),
            "block",
        )
        self.assertEqual(
            schema_paths.schema_status(
                "sample_resource",
                "rules",
                block_mode="requiredness",
            ),
            "optional",
        )
        self.assertEqual(
            schema_paths.schema_status(
                "sample_resource",
                "credentials",
                block_mode="requiredness",
            ),
            "required",
        )

    def test_repeated_block_child_required_attr_is_required(self):
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "rules[].action"),
            "required",
        )
        self.assertEqual(
            schema_paths.schema_status("sample_resource", "rules[0].priority"),
            "optional",
        )

    def test_strip_collection_selector(self):
        self.assertEqual(
            schema_paths.strip_collection_selector(
                (schema_paths.LIST_MARKER, "name")
            ),
            ("name",),
        )
        self.assertEqual(
            schema_paths.strip_collection_selector((0, "name")),
            ("name",),
        )


if __name__ == "__main__":
    unittest.main()
