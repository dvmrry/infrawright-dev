import unittest

from engine.filter_imports import filter_imports
from engine.transform import hcl_string_literal
from engine.transform import render_imports


def _address(resource_type, key):
    return "module.%s.%s.this[%s]" % (
        resource_type,
        resource_type,
        hcl_string_literal(key),
    )


class FilterImportsTest(unittest.TestCase):
    RESOURCE = "zia_fake"

    def _imports(self, originals):
        return render_imports(self.RESOURCE, originals, {})

    def test_normal_import_block_filtering_still_works(self):
        text = self._imports({
            "already_managed": {"id": "101"},
            "needs_import": {"id": "102"},
        })

        out, kept, skipped = filter_imports(
            text, [_address(self.RESOURCE, "already_managed")]
        )

        self.assertEqual(kept, 1)
        self.assertEqual(skipped, 1)
        self.assertIn(_address(self.RESOURCE, "needs_import"), out)
        self.assertNotIn(_address(self.RESOURCE, "already_managed"), out)

    def test_import_id_with_closing_brace_is_removed_as_complete_block(self):
        text = self._imports({"danger": {"id": "abc}def"}})

        out, kept, skipped = filter_imports(
            text, [_address(self.RESOURCE, "danger")]
        )

        self.assertEqual(out, "")
        self.assertEqual(kept, 0)
        self.assertEqual(skipped, 1)

    def test_import_id_with_closing_brace_is_kept_without_truncation(self):
        text = self._imports({"danger": {"id": "abc}def"}})

        out, kept, skipped = filter_imports(text, [])

        self.assertEqual(out, text)
        self.assertEqual(kept, 1)
        self.assertEqual(skipped, 0)

    def test_import_key_with_escaped_quote_space_and_brace_is_handled(self):
        key = 'bad" } key'
        text = self._imports({key: {"id": "101"}})

        out, kept, skipped = filter_imports(
            text, [_address(self.RESOURCE, key)]
        )

        self.assertEqual(out, "")
        self.assertEqual(kept, 0)
        self.assertEqual(skipped, 1)

    def test_escaped_newline_tab_and_backslash_are_handled(self):
        key = "line\nkey\ttail\\"
        text = self._imports({key: {"id": "id\nwith\ttab\\and}brace"}})

        out, kept, skipped = filter_imports(
            text, [_address(self.RESOURCE, key)]
        )

        self.assertEqual(out, "")
        self.assertEqual(kept, 0)
        self.assertEqual(skipped, 1)

    def test_multiple_blocks_remove_only_managed_import_and_preserve_other_text(self):
        managed = self._imports({"managed": {"id": "101"}})
        keep = self._imports({"keep": {"id": "102"}})
        prefix = 'resource "x" "y" {\n  value = "not an import } block"\n}\n'
        middle = "locals {\n  keep = true\n}\n"
        suffix = "# tail comment\n"
        text = prefix + managed + middle + keep + suffix

        out, kept, skipped = filter_imports(
            text, [_address(self.RESOURCE, "managed")]
        )

        self.assertEqual(out, prefix + middle + keep + suffix)
        self.assertEqual(kept, 1)
        self.assertEqual(skipped, 1)

    def test_non_import_hcl_block_with_brace_in_string_is_not_removed(self):
        text = 'resource "x" "y" {\n  value = "abc}def"\n}\n'

        out, kept, skipped = filter_imports(text, ["resource.x.y"])

        self.assertEqual(out, text)
        self.assertEqual(kept, 0)
        self.assertEqual(skipped, 0)

    def test_malformed_import_block_raises_without_corrupting_output(self):
        text = (
            'import {\n'
            '  to = module.zia_fake.zia_fake.this["danger"]\n'
            '  id = "abc}def"\n'
        )

        with self.assertRaisesRegex(ValueError, "unterminated generated import"):
            filter_imports(text, [_address(self.RESOURCE, "danger")])


if __name__ == "__main__":
    unittest.main()
