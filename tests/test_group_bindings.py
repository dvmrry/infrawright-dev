import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

from engine import artifacts
from engine import expression_bindings
from engine import gen_env
from engine import group_bindings
from engine import lookup
from engine import packs
from engine import transform


class GroupBindingsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="group-bindings-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        self.saved_reference_manifest = lookup.reference_manifest
        self.saved_lookup_sources = lookup.lookup_sources
        self.saved_pack_lookup_sources = packs.lookup_sources
        self.tenant = "tenant"

    def tearDown(self):
        lookup.reference_manifest = self.saved_reference_manifest
        lookup.lookup_sources = self.saved_lookup_sources
        packs.lookup_sources = self.saved_pack_lookup_sources
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _deployment(self, members, provider="zpa"):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            json.dump({
                "overlay": self.tmp,
                "roots": {
                    provider: {
                        "groups": {"%s_custom" % provider: members},
                        "bind_references": True,
                    },
                },
            }, f)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep

    def _patch_refs(self, resource_type, field, referent):
        lookup.reference_manifest = lambda: {
            resource_type: {
                field: {
                    "referent": referent,
                    "name_field": "name",
                },
            },
        }

    def _write_lookup(self, referent, mapping):
        path = lookup.lookup_path(self.tenant, referent)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(mapping, f, indent=2, sort_keys=True)
        return path

    def _capture_derive(self, resource_type, items):
        old_err = sys.stderr
        sys.stderr = io.StringIO()
        try:
            data = group_bindings.derive(resource_type, items, self.tenant)
            stderr = sys.stderr.getvalue()
        finally:
            sys.stderr = old_err
        return data, stderr

    def test_derives_bound_scalar_reference(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup("zpa_segment_group", {"sg-1": "Segment One"})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"name": "App", "segment_group_id": "sg-1"},
        })

        expr = (
            data["resources"]["zpa_application_segment.app"]
            ["segment_group_id"]["expression"]
        )
        self.assertEqual(
            expr,
            'module.zpa_segment_group.name_to_id["Segment One"]',
        )
        self.assertEqual(
            expression_bindings.parse_bindings(
                data, "zpa_application_segment")[0]["expression"],
            expr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_display_name_with_interpolation_is_skipped_not_emitted(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group", {"sg-1": "Bad ${uuid()} Name"})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"name": "App", "segment_group_id": "sg-1"},
        })

        self.assertEqual(data.get("resources"), {})
        self.assertIn("template interpolation", stderr)
        self.assertIn("unsafe_name=1", stderr)

    def test_list_with_null_or_nonstring_element_is_not_bound(self):
        self._deployment(["zpa_application_server", "zpa_server_group"])
        self._patch_refs(
            "zpa_application_server", "app_server_group_ids", "zpa_server_group")
        self._write_lookup("zpa_server_group", {"sg-1": "Group One"})

        data, stderr = self._capture_derive("zpa_application_server", {
            "server": {"app_server_group_ids": ["sg-1", None, 42]},
        })

        # Fail closed: the raw list stays in tfvars; no fabricated "None"/"42".
        self.assertEqual(data.get("resources"), {})
        self.assertNotIn('"None"', stderr)
        self.assertIn("unbindable_list=1", stderr)

    def test_derives_bound_list_reference_expression(self):
        self._deployment(["zpa_application_server", "zpa_server_group"])
        self._patch_refs(
            "zpa_application_server", "app_server_group_ids", "zpa_server_group")
        self._write_lookup(
            "zpa_server_group",
            {"sg-2": "Group Two", "sg-1": "Group One"},
        )

        data, stderr = self._capture_derive("zpa_application_server", {
            "server": {"app_server_group_ids": ["sg-1", "sg-2"]},
        })

        expr = (
            data["resources"]["zpa_application_server.server"]
            ["app_server_group_ids"]["expression"]
        )
        self.assertEqual(
            expr,
            '[module.zpa_server_group.name_to_id["Group One"], '
            'module.zpa_server_group.name_to_id["Group Two"]]',
        )
        self.assertEqual(
            expression_bindings.parse_bindings(
                data, "zpa_application_server")[0]["expression"],
            expr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_server: 2 bound, 0 skipped\n",
            stderr,
        )

    def test_missing_lookup_skip_note(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        missing = lookup.lookup_path(self.tenant, "zpa_segment_group")

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.segment_group_id skipped; "
            "lookup for zpa_segment_group is missing at %s\n" % missing,
            stderr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 0 bound, 1 skipped "
            "(missing_lookup=1)\n",
            stderr,
        )

    def test_id_absent_skip_note(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup("zpa_segment_group", {"sg-2": "Other"})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.app.segment_group_id "
            "value 'sg-1' skipped; id is absent from zpa_segment_group lookup\n",
            stderr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 0 bound, 1 skipped "
            "(id_absent=1)\n",
            stderr,
        )

    def test_unknown_display_name_skip_note(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup("zpa_segment_group", {"sg-1": lookup.UNKNOWN})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.app.segment_group_id "
            "value 'sg-1' skipped; display name is <unknown>\n",
            stderr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 0 bound, 1 skipped "
            "(unknown_name=1)\n",
            stderr,
        )

    def test_nonunique_display_name_skip_note(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group",
            {"sg-1": "Duplicate", "sg-2": "Duplicate"},
        )

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.app.segment_group_id "
            "value 'sg-1' skipped; display name 'Duplicate' maps to multiple "
            "zpa_segment_group ids\n",
            stderr,
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 0 bound, 1 skipped "
            "(nonunique_name=1)\n",
            stderr,
        )

    def test_referent_without_name_to_id_skip_note(self):
        self._deployment(
            ["zia_url_categories", "zia_url_filtering_rules"],
            provider="zia",
        )
        self._patch_refs(
            "zia_url_filtering_rules", "url_categories", "zia_url_categories")
        self._write_lookup("zia_url_categories", {"cat-1": "Category One"})

        data, stderr = self._capture_derive("zia_url_filtering_rules", {
            "rule": {"url_categories": ["cat-1"]},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zia_url_filtering_rules.url_categories skipped; "
            "zia_url_categories module does not emit name_to_id\n",
            stderr,
        )
        self.assertIn(
            "NOTE bindings: zia_url_filtering_rules: 0 bound, 1 skipped "
            "(name_to_id_unavailable=1)\n",
            stderr,
        )

    def test_deterministic_rendering(self):
        self._deployment(["zpa_application_server", "zpa_server_group"])
        self._patch_refs(
            "zpa_application_server", "app_server_group_ids", "zpa_server_group")
        self._write_lookup(
            "zpa_server_group",
            {"sg-2": "Group Two", "sg-1": "Group One"},
        )
        items = {
            "b": {"app_server_group_ids": ["sg-2"]},
            "a": {"app_server_group_ids": ["sg-1"]},
        }

        first, _stderr = self._capture_derive("zpa_application_server", items)
        second, _stderr = self._capture_derive("zpa_application_server", items)

        self.assertEqual(group_bindings.render(first), group_bindings.render(second))

    def test_write_generated_removes_empty_stale_file(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        path = artifacts.generated_expression_bindings_file(
            self.tenant, "zpa_application_segment")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write("{}\n")

        written = group_bindings.write_generated(
            "zpa_application_segment", {}, self.tenant)

        self.assertIsNone(written)
        self.assertFalse(os.path.exists(path))


class GroupBindingsEndToEndTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="group-bindings-e2e-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        self.saved_reference_manifest = lookup.reference_manifest
        self.saved_lookup_sources = lookup.lookup_sources
        self.saved_pack_lookup_sources = packs.lookup_sources
        self.tenant = "tenant"

    def tearDown(self):
        lookup.reference_manifest = self.saved_reference_manifest
        lookup.lookup_sources = self.saved_lookup_sources
        packs.lookup_sources = self.saved_pack_lookup_sources
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, name, data):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def _configure(self):
        module_dir = os.path.join(self.tmp, "modules", "default")
        os.makedirs(module_dir)
        source_modules = os.path.join(os.getcwd(), "demo", "modules", "default")
        for resource_type in ("zpa_application_segment", "zpa_segment_group"):
            shutil.copytree(
                os.path.join(source_modules, resource_type),
                os.path.join(module_dir, resource_type),
            )
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            json.dump({
                "overlay": self.tmp,
                "module_dir": module_dir,
                "roots": {
                    "zpa": {
                        "groups": {
                            "zpa_custom": [
                                "zpa_application_segment",
                                "zpa_segment_group",
                            ],
                        },
                        "bind_references": True,
                    },
                },
            }, f)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        lookup.reference_manifest = lambda: {
            "zpa_application_segment": {
                "segment_group_id": {
                    "referent": "zpa_segment_group",
                    "name_field": "name",
                },
            },
        }
        lookup.lookup_sources = lambda: {
            "zpa_segment_group": {"name_field": "name"},
        }
        packs.lookup_sources = lambda: {
            "zpa_segment_group": {"name_field": "name"},
        }

    def test_transform_then_gen_env_wires_group_local_binding(self):
        self._configure()
        segment_input = self._write_json("zpa_segment_group.json", [{
            "id": "sg-1",
            "name": "Segment One",
            "enabled": True,
        }])
        app_input = self._write_json("zpa_application_segment.json", [{
            "id": "app-1",
            "name": "App One",
            "domainNames": ["app.example.com"],
            "segmentGroupId": "sg-1",
        }])

        self.assertEqual(
            transform.main(["zpa_segment_group", segment_input, self.tenant]),
            0,
        )
        self.assertEqual(
            transform.main(["zpa_application_segment", app_input, self.tenant]),
            0,
        )

        with open(
            artifacts.config_file(self.tenant, "zpa_application_segment"),
            encoding="utf-8",
        ) as f:
            tfvars = json.load(f)
        self.assertEqual(
            tfvars["zpa_application_segment_items"]["app_one"]
            ["segment_group_id"],
            "sg-1",
        )

        generated_path = artifacts.generated_expression_bindings_file(
            self.tenant, "zpa_application_segment")
        with open(generated_path, encoding="utf-8") as f:
            generated = json.load(f)
        expression = (
            generated["resources"]["zpa_application_segment.app_one"]
            ["segment_group_id"]["expression"]
        )
        self.assertEqual(
            expression,
            'module.zpa_segment_group.name_to_id["Segment One"]',
        )

        out_root = os.path.join(self.tmp, "generated-envs")
        gen_env.generate_env(
            self.tenant,
            out_root=out_root,
            fmt=False,
            selectors=["zpa_application_segment"],
        )
        root = os.path.join(out_root, self.tenant, "zpa_custom")
        with open(os.path.join(root, "main.tf"), encoding="utf-8") as f:
            main_tf = f.read()
        self.assertIn(
            "items = local.infrawright_zpa_application_segment_"
            "expression_bound_items",
            main_tf,
        )
        with open(
            os.path.join(root, "expression_bindings.tf"),
            encoding="utf-8",
        ) as f:
            bindings_tf = f.read()
        self.assertIn(
            'segment_group_id = '
            'module.zpa_segment_group.name_to_id["Segment One"]',
            bindings_tf,
        )

        if shutil.which("terraform") is None:
            self.skipTest("terraform not on PATH - env root validate is optional")
        init = subprocess.run(
            ["terraform", "init", "-backend=false", "-input=false"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        if init.returncode != 0:
            self.skipTest("terraform init unavailable:\n%s" % init.stdout)
        validate = subprocess.run(
            ["terraform", "validate"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        self.assertEqual(validate.returncode, 0, validate.stdout)


if __name__ == "__main__":
    unittest.main()
