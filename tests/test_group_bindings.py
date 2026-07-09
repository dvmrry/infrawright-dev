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
from engine import gen_module
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
        lookup.lookup_sources = lambda: {
            referent: {"name_field": "name"},
        }
        packs.lookup_sources = lambda: {
            referent: {"name_field": "name"},
        }

    def _write_lookup(self, referent, mapping, keys=None, legacy=False):
        path = lookup.lookup_path(self.tenant, referent)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        if legacy:
            data = mapping
        else:
            if keys is None:
                keys = dict(
                    (ident, transform.slugify(display))
                    for ident, display in mapping.items()
                    if display != lookup.UNKNOWN
                )
            data = {
                lookup.LOOKUP_BY_ID: mapping,
                lookup.LOOKUP_KEY_BY_ID: keys,
            }
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, sort_keys=True)
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
            'module.zpa_segment_group.items["segment_one"].id',
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

    def test_name_field_name_still_binds(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup("zpa_segment_group", {"sg-1": "Segment One"})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"name": "App", "segment_group_id": "sg-1"},
        })

        self.assertEqual(
            data["resources"]["zpa_application_segment.app"]
            ["segment_group_id"]["expression"],
            'module.zpa_segment_group.items["segment_one"].id',
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_referent_name_field_mismatch_still_binds_by_key(self):
        self._deployment(
            ["zia_url_categories", "zia_url_filtering_rules"],
            provider="zia",
        )
        lookup.reference_manifest = lambda: {
            "zia_url_filtering_rules": {
                "url_categories": {
                    "referent": "zia_url_categories",
                    "name_field": "configured_name",
                },
            },
        }
        lookup.lookup_sources = lambda: {
            "zia_url_categories": {"name_field": "configured_name"},
        }
        packs.lookup_sources = lambda: {
            "zia_url_categories": {"name_field": "configured_name"},
        }
        self._write_lookup("zia_url_categories", {"cat-1": "Category One"})

        data, stderr = self._capture_derive("zia_url_filtering_rules", {
            "rule": {"url_categories": ["cat-1"]},
        })

        expr = (
            data["resources"]["zia_url_filtering_rules.rule"]
            ["url_categories"]["expression"]
        )
        self.assertEqual(
            expr,
            '[module.zia_url_categories.items["category_one"].id]',
        )
        self.assertIn(
            "NOTE bindings: zia_url_filtering_rules: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_referent_key_with_interpolation_is_skipped_not_emitted(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group",
            {"sg-1": "Safe Display"},
            keys={"sg-1": "bad_${uuid()}_key"},
        )

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"name": "App", "segment_group_id": "sg-1"},
        })

        self.assertEqual(data.get("resources"), {})
        self.assertIn("template interpolation", stderr)
        self.assertIn("unsafe_key=1", stderr)

    def test_list_with_null_or_non_scalar_element_is_not_bound(self):
        self._deployment(["zpa_application_server", "zpa_server_group"])
        self._patch_refs(
            "zpa_application_server", "app_server_group_ids", "zpa_server_group")
        self._write_lookup("zpa_server_group", {"sg-1": "Group One"})

        data, stderr = self._capture_derive("zpa_application_server", {
            "server": {"app_server_group_ids": ["sg-1", None, {"id": "sg-2"}]},
        })

        # Fail closed: the raw list stays in tfvars; no fabricated "None".
        self.assertEqual(data.get("resources"), {})
        self.assertNotIn('"None"', stderr)
        self.assertIn("unbindable_list=1", stderr)

    def test_list_with_bool_element_is_not_bound(self):
        self._deployment(["zpa_application_server", "zpa_server_group"])
        self._patch_refs(
            "zpa_application_server", "app_server_group_ids", "zpa_server_group")
        self._write_lookup("zpa_server_group", {"True": "Group One"})

        data, stderr = self._capture_derive("zpa_application_server", {
            "server": {"app_server_group_ids": [True]},
        })

        # bool is an int subclass in Python, but not a provider ID scalar here.
        self.assertEqual(data.get("resources"), {})
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
            '[module.zpa_server_group.items["group_one"].id, '
            'module.zpa_server_group.items["group_two"].id]',
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

    def test_derives_bound_numeric_list_reference_expression(self):
        self._deployment(["zcc_forwarding_profile", "zcc_trusted_network"],
                         provider="zcc")
        lookup.reference_manifest = lambda: {
            "zcc_forwarding_profile": {
                "trusted_network_ids": {
                    "referent": "zcc_trusted_network",
                    "name_field": "network_name",
                },
                "trusted_network_ids_selected": {
                    "referent": "zcc_trusted_network",
                    "name_field": "network_name",
                },
            },
        }
        lookup.lookup_sources = lambda: {
            "zcc_trusted_network": {"name_field": "network_name"},
        }
        packs.lookup_sources = lambda: {
            "zcc_trusted_network": {"name_field": "network_name"},
        }
        self._write_lookup(
            "zcc_trusted_network",
            {"19281": "Trusted One", "19282": "Trusted Two"},
        )

        data, stderr = self._capture_derive("zcc_forwarding_profile", {
            "forwarding": {
                "trusted_network_ids": [19281, 19282],
                "trusted_network_ids_selected": [19282],
            },
        })

        trusted_expr = (
            data["resources"]["zcc_forwarding_profile.forwarding"]
            ["trusted_network_ids"]["expression"]
        )
        self.assertEqual(
            trusted_expr,
            '[module.zcc_trusted_network.items["trusted_one"].id, '
            'module.zcc_trusted_network.items["trusted_two"].id]',
        )
        selected_expr = (
            data["resources"]["zcc_forwarding_profile.forwarding"]
            ["trusted_network_ids_selected"]["expression"]
        )
        self.assertEqual(
            selected_expr,
            '[module.zcc_trusted_network.items["trusted_two"].id]',
        )
        self.assertEqual(
            sorted(
                binding["expression"]
                for binding in expression_bindings.parse_bindings(
                    data, "zcc_forwarding_profile")
            ),
            sorted([trusted_expr, selected_expr]),
        )
        self.assertIn(
            "NOTE bindings: zcc_forwarding_profile: 3 bound, 0 skipped\n",
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

    def test_unknown_display_name_still_binds_by_key(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group",
            {"sg-1": lookup.UNKNOWN},
            keys={"sg-1": "segment_one"},
        )

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(
            data["resources"]["zpa_application_segment.app"]
            ["segment_group_id"]["expression"],
            'module.zpa_segment_group.items["segment_one"].id',
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_nonunique_display_name_still_binds_by_key(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group",
            {"sg-1": "Duplicate", "sg-2": "Duplicate"},
            keys={"sg-1": "first", "sg-2": "second"},
        )

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(
            data["resources"]["zpa_application_segment.app"]
            ["segment_group_id"]["expression"],
            'module.zpa_segment_group.items["first"].id',
        )
        self.assertIn(
            "NOTE bindings: zpa_application_segment: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_referent_without_name_to_id_still_binds_by_key(self):
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

        self.assertEqual(
            data["resources"]["zia_url_filtering_rules.rule"]
            ["url_categories"]["expression"],
            '[module.zia_url_categories.items["category_one"].id]',
        )
        self.assertIn(
            "NOTE bindings: zia_url_filtering_rules: 1 bound, 0 skipped\n",
            stderr,
        )

    def test_legacy_lookup_without_key_map_skips_loudly(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment", "segment_group_id", "zpa_segment_group")
        self._write_lookup(
            "zpa_segment_group", {"sg-1": "Segment One"}, legacy=True)

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {"segment_group_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.segment_group_id skipped; "
            "lookup for zpa_segment_group has no key_by_id map\n",
            stderr,
        )
        self.assertIn("key_map_unavailable=1", stderr)

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

    def test_self_reference_is_skipped_with_summary_reason(self):
        self._deployment(["zpa_segment_group"])
        self._patch_refs(
            "zpa_segment_group", "parent_id", "zpa_segment_group")
        self._write_lookup("zpa_segment_group", {"sg-1": "Parent"})

        data, stderr = self._capture_derive("zpa_segment_group", {
            "child": {"name": "Child", "parent_id": "sg-1"},
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_segment_group.parent_id skipped; "
            "self-referential bindings would create a Terraform cycle\n",
            stderr,
        )
        self.assertIn("self_reference=1", stderr)

    def test_nested_reference_field_is_skipped_loudly(self):
        self._deployment(["zpa_application_segment", "zpa_segment_group"])
        self._patch_refs(
            "zpa_application_segment",
            "settings.segment_group_id",
            "zpa_segment_group",
        )
        self._write_lookup("zpa_segment_group", {"sg-1": "Segment One"})

        data, stderr = self._capture_derive("zpa_application_segment", {
            "app": {
                "name": "App",
                "settings.segment_group_id": "sg-1",
            },
        })

        self.assertEqual(data, {"resources": {}})
        self.assertIn(
            "NOTE bindings: zpa_application_segment.settings.segment_group_id "
            "skipped; nested reference fields are unsupported\n",
            stderr,
        )
        self.assertIn("nested_field_unsupported=1", stderr)


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
        for resource_type in ("zpa_application_segment", "zpa_segment_group"):
            gen_module.generate_module(resource_type, out_root=module_dir, fmt=False)
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
            'module.zpa_segment_group.items["segment_one"].id',
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
            'module.zpa_segment_group.items["segment_one"].id',
            bindings_tf,
        )
        if shutil.which("terraform") is None:
            self.skipTest("terraform not on PATH - env root validate is optional")
        init = subprocess.run(
            ["terraform", "init", "-backend=false", "-input=false"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        if init.returncode != 0:
            self.skipTest("terraform init unavailable:\n%s" % init.stdout)
        validate = subprocess.run(
            ["terraform", "validate"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        self.assertEqual(validate.returncode, 0, validate.stdout)

    def test_duplicate_referent_names_do_not_break_validate(self):
        self._configure()
        for resource_type, data in (
                ("zpa_segment_group", {
                    "zpa_segment_group_items": {
                        "first": {
                            "enabled": True,
                            "name": "Segment One",
                        },
                        "second": {
                            "enabled": True,
                            "name": "Segment One",
                        },
                    },
                }),
                ("zpa_application_segment", {
                    "zpa_application_segment_items": {
                        "app": {
                            "domain_names": ["app.example.com"],
                            "name": "App One",
                            "segment_group_id": "sg-1",
                        },
                    },
                }),
        ):
            path = artifacts.config_file(self.tenant, resource_type)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as f:
                json.dump(data, f)

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
            'module "zpa_segment_group"',
            main_tf,
        )
        self.assertFalse(os.path.exists(os.path.join(
            root, "expression_bindings.tf",
        )))

        if shutil.which("terraform") is None:
            self.skipTest("terraform not on PATH - env root validate is optional")
        init = subprocess.run(
            ["terraform", "init", "-backend=false", "-input=false"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        if init.returncode != 0:
            self.skipTest("terraform init unavailable:\n%s" % init.stdout)
        validate = subprocess.run(
            ["terraform", "validate"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        self.assertEqual(validate.returncode, 0, validate.stdout)
        test = subprocess.run(
            ["terraform", "test"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            universal_newlines=True,
        )
        self.assertEqual(test.returncode, 0, test.stdout)


if __name__ == "__main__":
    unittest.main()
