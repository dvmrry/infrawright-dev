import unittest

from engine.path_inventory import leaf_paths


class PathInventoryTest(unittest.TestCase):
    def test_extracts_leaf_paths_and_normalizes_list_indexes(self):
        value = {
            "conditions": [
                {
                    "operands": [
                        {"id": "one", "name": "first"},
                        {"id": "two"},
                    ],
                },
            ],
            "enabled": False,
        }

        self.assertEqual(leaf_paths(value), [
            "conditions[].operands[].id",
            "conditions[].operands[].name",
            "enabled",
        ])

    def test_does_not_report_container_paths(self):
        self.assertEqual(leaf_paths({
            "empty_list": [],
            "empty_object": {},
            "name": "prod",
        }), ["name"])

    def test_can_preserve_list_indexes_for_debugging(self):
        self.assertEqual(
            leaf_paths({"rules": [{"id": "a"}, {"id": "b"}]},
                       normalize_lists=False),
            ["rules[0].id", "rules[1].id"],
        )

    def test_preserves_map_keys_as_dotted_segments(self):
        self.assertEqual(
            leaf_paths({"tags": {"DisplayName": "prod", "owner": "net"}}),
            ["tags.DisplayName", "tags.owner"],
        )


if __name__ == "__main__":
    unittest.main()
