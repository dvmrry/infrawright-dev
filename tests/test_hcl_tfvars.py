"""Tests for fmt-canonical HCL tfvars rendering."""
import json
import os
import shutil
import subprocess
import tempfile
import unittest

from engine import hcl_tfvars
from engine import lookup
from engine import packs
from engine import transform


def _write_json(path, data):
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")


class HclTfvarsRenderTest(unittest.TestCase):
    def test_empty_items(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl({}),
            hcl_tfvars.HEADER + "\nitems = {}\n",
        )

    def test_namespaced_var_name(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(
                {"rule": {"name": "Rule"}},
                var_name="sample_resource_items",
            ),
            hcl_tfvars.HEADER + "\n"
            "sample_resource_items = {\n"
            "  \"rule\" = {\n"
            "    name = \"Rule\"\n"
            "  }\n"
            "}\n",
        )

    def test_var_name_must_be_bare_identifier(self):
        with self.assertRaises(ValueError):
            hcl_tfvars.render_tfvars_hcl({}, var_name="bad-name")

    def test_mixed_scalar_types(self):
        items = {
            "rule": {
                "enabled": True,
                "name": "Rule",
                "nothing": None,
                "ratio": 0.5,
                "retries": 3,
            },
        }

        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(items),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"rule\" = {\n"
            "    enabled = true\n"
            "    name    = \"Rule\"\n"
            "    nothing = null\n"
            "    ratio   = 0.5\n"
            "    retries = 3\n"
            "  }\n"
            "}\n",
        )

    def test_string_escaping_delegates_to_transform(self):
        value = "prefix ${var.name} \"quoted\""

        self.assertIn(
            "    value = %s\n" % transform.hcl_string_literal(value),
            hcl_tfvars.render_tfvars_hcl({"rule": {"value": value}}),
        )

    def test_bare_and_quoted_attribute_keys(self):
        items = {
            "rule": {
                "normal": "bare",
                "hyphen-key": "dash",
                "123abc": "leading",
            },
        }

        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(items),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"rule\" = {\n"
            "    \"123abc\"     = \"leading\"\n"
            "    \"hyphen-key\" = \"dash\"\n"
            "    normal       = \"bare\"\n"
            "  }\n"
            "}\n",
        )

    def test_attribute_equals_alignment(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl({
                "r": {"a": 1, "long_name": 2},
            }),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"r\" = {\n"
            "    a         = 1\n"
            "    long_name = 2\n"
            "  }\n"
            "}\n",
        )

    def test_multiline_list_uses_trailing_commas(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl({
                "r": {
                    "categories": ["CUSTOM_01", "ANY"],
                    "name": "Rule",
                },
            }),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"r\" = {\n"
            "    categories = [\n"
            "      \"CUSTOM_01\",\n"
            "      \"ANY\",\n"
            "    ]\n"
            "    name = \"Rule\"\n"
            "  }\n"
            "}\n",
        )

    def test_nested_object(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl({
                "r": {
                    "settings": {
                        "long_name": "x",
                        "short": True,
                    },
                },
            }),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"r\" = {\n"
            "    settings = {\n"
            "      long_name = \"x\"\n"
            "      short     = true\n"
            "    }\n"
            "  }\n"
            "}\n",
        )

    def test_scalar_trailing_comment(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(
                {"r": {"category_id": "CUSTOM_01"}},
                comments={("r", "category_id"): "Finance"},
            ),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"r\" = {\n"
            "    category_id = \"CUSTOM_01\" # Finance\n"
            "  }\n"
            "}\n",
        )

    def test_list_element_trailing_comments(self):
        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(
                {"r": {"category_ids": ["A", "CUSTOM_02"]}},
                comments={
                    ("r", "category_ids", 0): "Finance",
                    ("r", "category_ids", 1): "HR",
                },
            ),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"r\" = {\n"
            "    category_ids = [\n"
            "      \"A\",         # Finance\n"
            "      \"CUSTOM_02\", # HR\n"
            "    ]\n"
            "  }\n"
            "}\n",
        )

    def test_comment_text_must_be_single_line(self):
        with self.assertRaises(ValueError):
            hcl_tfvars.render_tfvars_hcl(
                {"r": {"category_id": "CUSTOM_01"}},
                comments={("r", "category_id"): "Finance\nOps"},
            )

    def test_full_file_golden(self):
        items = {
            "block risky": {
                "enabled": True,
                "labels": ["CUSTOM_01"],
                "metadata": {"owner": "team-a"},
                "order": 10,
            },
        }
        comments = {
            ("block risky", "labels", 0): "Finance",
        }

        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(items, comments=comments),
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"block risky\" = {\n"
            "    enabled = true\n"
            "    labels = [\n"
            "      \"CUSTOM_01\", # Finance\n"
            "    ]\n"
            "    metadata = {\n"
            "      owner = \"team-a\"\n"
            "    }\n"
            "    order = 10\n"
            "  }\n"
            "}\n",
        )

    def test_deterministic_rendering(self):
        items = {
            "b": {"name": "B"},
            "a": {"name": "A"},
        }

        self.assertEqual(
            hcl_tfvars.render_tfvars_hcl(items),
            hcl_tfvars.render_tfvars_hcl(items),
        )

    def test_terraform_fmt_accepts_rendered_fixture(self):
        text = hcl_tfvars.render_tfvars_hcl(
            {
                "block risky": {
                    "enabled": True,
                    "labels": ["CUSTOM_01", "CUSTOM_02"],
                    "metadata": {"owner": "team-a", "priority": 1},
                    "ratio": 0.5,
                },
            },
            comments={
                ("block risky", "labels", 0): "Finance",
                ("block risky", "labels", 1): "HR",
            },
        )
        self.assertTerraformFmtNoop(text)

    def assertTerraformFmtNoop(self, text):
        if shutil.which("terraform") is None:
            self.skipTest("terraform not on PATH - HCL fmt calibration")
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "test.auto.tfvars")
            with open(path, "w", encoding="utf-8") as f:
                f.write(text)
            out = subprocess.run(
                ["terraform", "fmt", "-check", "test.auto.tfvars"],
                cwd=tmp,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
        self.assertEqual(
            out.returncode, 0, out.stdout.decode("utf-8", "replace"))


class HclTfvarsDeriveCommentsTest(unittest.TestCase):
    def setUp(self):
        self.pack_root = tempfile.mkdtemp(prefix="hcl-tfvars-packs-")
        self.config_root = tempfile.mkdtemp(prefix="hcl-tfvars-config-")
        self._prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.pack_root
        self._write_pack()
        packs.reset()
        self.addCleanup(self._restore)

    def _restore(self):
        if self._prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self._prev_packs
        packs.reset()
        shutil.rmtree(self.pack_root, ignore_errors=True)
        shutil.rmtree(self.config_root, ignore_errors=True)

    def _write_pack(self):
        _write_json(os.path.join(self.pack_root, "sample", "pack.json"), {
            "lookup_sources": {
                "sample_category": {"name_field": "display_name"},
            },
            "provider_prefixes": {"sample_": "sample"},
            "references": {
                "sample_rule": {
                    "category_id": {
                        "name_field": "display_name",
                        "referent": "sample_category",
                    },
                    "category_ids": {
                        "name_field": "display_name",
                        "referent": "sample_category",
                    },
                    "missing_category_id": {
                        "name_field": "display_name",
                        "referent": "sample_missing",
                    },
                },
            },
        })

    def _write_lookup(self, tenant, referent, mapping):
        _write_json(
            os.path.join(self.config_root, tenant, referent + lookup.LOOKUP_SUFFIX),
            mapping,
        )

    def test_derive_comments_resolves_reference_fields(self):
        self._write_lookup("tenant", "sample_category", {
            "CUSTOM_01": "Finance",
            "CUSTOM_02": "HR",
        })
        items = {
            "rule": {
                "category_id": "CUSTOM_01",
                "category_ids": ["CUSTOM_02", "BUILT_IN", "CUSTOM_99"],
                "missing_category_id": "CUSTOM_01",
                "name": "Rule",
            },
        }

        self.assertEqual(
            hcl_tfvars.derive_comments(
                "sample_rule", items, "tenant", config_root=self.config_root),
            {
                ("rule", "category_id"): "Finance",
                ("rule", "category_ids", 0): "HR",
                ("rule", "category_ids", 1): "BUILT_IN",
                ("rule", "category_ids", 2): lookup.UNKNOWN,
            },
        )

    def test_derive_comments_omits_missing_lookup_file(self):
        items = {
            "rule": {
                "missing_category_id": "CUSTOM_01",
                "name": "Rule",
            },
        }

        self.assertEqual(
            hcl_tfvars.derive_comments(
                "sample_rule", items, "tenant", config_root=self.config_root),
            {},
        )

    def test_derive_comments_returns_empty_without_references(self):
        self.assertEqual(
            hcl_tfvars.derive_comments(
                "sample_category", {"c": {"id": "CUSTOM_01"}}, "tenant",
                config_root=self.config_root),
            {},
        )

    def test_derive_comments_replaces_newline_in_display_name(self):
        self._write_lookup("tenant", "sample_category", {
            "CUSTOM_01": "Line\nBreak",
        })

        self.assertEqual(
            hcl_tfvars.derive_comments(
                "sample_rule",
                {"rule": {"category_id": "CUSTOM_01"}},
                "tenant",
                config_root=self.config_root),
            {("rule", "category_id"): "Line Break"},
        )

    def test_derive_comments_loads_each_referent_once(self):
        self._write_lookup("tenant", "sample_category", {
            "CUSTOM_01": "Finance",
            "CUSTOM_02": "HR",
        })
        calls = []
        original = hcl_tfvars.lookup.load_lookup

        def fake_load_lookup(tenant, referent, config_root=None):
            calls.append((tenant, referent, config_root))
            return original(tenant, referent, config_root=config_root)

        hcl_tfvars.lookup.load_lookup = fake_load_lookup
        self.addCleanup(setattr, hcl_tfvars.lookup, "load_lookup", original)

        hcl_tfvars.derive_comments(
            "sample_rule",
            {
                "one": {
                    "category_id": "CUSTOM_01",
                    "category_ids": ["CUSTOM_02"],
                },
                "two": {
                    "category_id": "CUSTOM_02",
                    "category_ids": ["CUSTOM_01"],
                },
            },
            "tenant",
            config_root=self.config_root,
        )

        self.assertEqual(calls, [
            ("tenant", "sample_category", self.config_root),
        ])

    def test_lookup_resolve_display_public_wrapper(self):
        self.assertEqual(
            lookup.resolve_display("CUSTOM_01", {"CUSTOM_01": "Finance"}),
            "Finance",
        )
        self.assertEqual(lookup.resolve_display("BUILT_IN", {}), "BUILT_IN")
        self.assertEqual(lookup.resolve_display("CUSTOM_99", {}), lookup.UNKNOWN)

    def test_end_to_end_derived_comments_golden(self):
        self._write_lookup("tenant", "sample_category", {
            "CUSTOM_01": "Finance",
            "CUSTOM_02": "HR",
        })
        items = {
            "allow": {
                "category_id": "CUSTOM_01",
                "category_ids": ["CUSTOM_02", "BUILT_IN"],
                "enabled": True,
            },
            "block": {
                "category_id": "CUSTOM_99",
                "category_ids": ["CUSTOM_01"],
                "enabled": False,
            },
        }
        rendered = hcl_tfvars.render_tfvars_hcl(
            items,
            comments=hcl_tfvars.derive_comments(
                "sample_rule", items, "tenant", config_root=self.config_root),
        )

        self.assertEqual(
            rendered,
            hcl_tfvars.HEADER + "\n"
            "items = {\n"
            "  \"allow\" = {\n"
            "    category_id = \"CUSTOM_01\" # Finance\n"
            "    category_ids = [\n"
            "      \"CUSTOM_02\", # HR\n"
            "      \"BUILT_IN\",  # BUILT_IN\n"
            "    ]\n"
            "    enabled = true\n"
            "  }\n"
            "  \"block\" = {\n"
            "    category_id = \"CUSTOM_99\" # <unknown>\n"
            "    category_ids = [\n"
            "      \"CUSTOM_01\", # Finance\n"
            "    ]\n"
            "    enabled = false\n"
            "  }\n"
            "}\n",
        )
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)


class RendererCoverageGapTest(unittest.TestCase):
    """Regression pins for paths verified against real terraform fmt in
    review: alignment-run splits, nested containers in lists, empty
    containers, sibling comment columns, and the non-finite float guard."""

    def _body(self, rendered):
        self.assertTrue(rendered.startswith(hcl_tfvars.HEADER + "\n"))
        return rendered[len(hcl_tfvars.HEADER) + 1:]

    def test_multiline_value_splits_alignment_runs_on_both_sides(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"aa": 1, "bb": 22, "mm": ["x"], "yy": 3, "zzzz": 44}}
        )
        self.assertEqual(
            self._body(rendered),
            'items = {\n'
            '  "item" = {\n'
            '    aa = 1\n'
            '    bb = 22\n'
            '    mm = [\n'
            '      "x",\n'
            '    ]\n'
            '    yy   = 3\n'
            '    zzzz = 44\n'
            '  }\n'
            '}\n',
        )
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_list_of_objects_renders_multiline_elements(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"objs": [{"a": 1}, {"b": 2}]}}
        )
        self.assertEqual(
            self._body(rendered),
            'items = {\n'
            '  "item" = {\n'
            '    objs = [\n'
            '      {\n'
            '        a = 1\n'
            '      },\n'
            '      {\n'
            '        b = 2\n'
            '      },\n'
            '    ]\n'
            '  }\n'
            '}\n',
        )
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_list_of_objects_element_comment_lands_on_closer(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"objs": [{"a": 1}]}},
            comments={("item", "objs", 0): "annotated"},
        )
        self.assertIn("      }, # annotated\n", rendered)
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_list_of_lists_renders_nested_elements(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"nested": [["u", "v"], ["w"]]}}
        )
        self.assertEqual(
            self._body(rendered),
            'items = {\n'
            '  "item" = {\n'
            '    nested = [\n'
            '      [\n'
            '        "u",\n'
            '        "v",\n'
            '      ],\n'
            '      [\n'
            '        "w",\n'
            '      ],\n'
            '    ]\n'
            '  }\n'
            '}\n',
        )
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_empty_containers_stay_inline_and_aligned(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"empty_list": [], "empty_obj": {}, "z": 1}}
        )
        self.assertEqual(
            self._body(rendered),
            'items = {\n'
            '  "item" = {\n'
            '    empty_list = []\n'
            '    empty_obj  = {}\n'
            '    z          = 1\n'
            '  }\n'
            '}\n',
        )
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_sibling_scalar_comments_share_a_column(self):
        rendered = hcl_tfvars.render_tfvars_hcl(
            {"item": {"a": 1, "much_longer": 22}},
            comments={
                ("item", "a"): "short",
                ("item", "much_longer"): "long",
            },
        )
        self.assertIn("    a           = 1  # short\n", rendered)
        self.assertIn("    much_longer = 22 # long\n", rendered)
        HclTfvarsRenderTest.assertTerraformFmtNoop(self, rendered)

    def test_non_finite_floats_fail_loudly(self):
        for value in (float("nan"), float("inf"), float("-inf")):
            with self.assertRaises(ValueError):
                hcl_tfvars.render_tfvars_hcl({"item": {"bad": value}})


if __name__ == "__main__":
    unittest.main()
