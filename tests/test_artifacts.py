import json
import os
import shutil
import tempfile
import unittest

from engine import artifacts
from engine import packs
from engine import registry


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class ArtifactsSelectorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="artifacts-packs-")
        self.saved_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        os.makedirs(os.path.join(self.tmp, "sample"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "sample", "pack.json"), {
            "provider_prefixes": {"unused_": "sample"},
            "provider_sources": {"sample": "example/sample"},
        })
        _write_json(os.path.join(self.tmp, "sample", "registry.json"), {
            "resource_without_provider_prefix": {
                "generate": True,
                "product": "sample",
            },
            "sample_data_only": {
                "product": "sample",
            },
        })
        packs.reset()
        registry.reload_registry()

    def tearDown(self):
        if self.saved_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.saved_packs
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_product_selector_uses_registry_product_not_prefix(self):
        self.assertEqual(
            artifacts.expand_resources(["sample"]),
            ["resource_without_provider_prefix"],
        )

    def test_non_generated_exact_resource_is_rejected(self):
        with self.assertRaises(ValueError):
            artifacts.expand_resources(["sample_data_only"])

    def test_validate_resource_type_requires_exact_generated_type(self):
        artifacts.validate_resource_type("resource_without_provider_prefix")
        with self.assertRaises(ValueError):
            artifacts.validate_resource_type("../resource_without_provider_prefix")


class ArtifactsPathTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="artifacts-deployment-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")

    def tearDown(self):
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_overlay_keeps_flat_resource_type_paths(self):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            f.write(json.dumps({"overlay": "acme"}))
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        self.assertEqual(artifacts.config_suffix(), artifacts.CONFIG_SUFFIX)
        self.assertEqual(
            artifacts.config_file("tenant", "sample_resource"),
            os.path.join("acme", "config", "tenant",
                         "sample_resource.auto.tfvars.json"),
        )
        self.assertEqual(
            artifacts.env_root("tenant", "sample_resource"),
            os.path.join("acme", "envs", "tenant", "sample_resource"),
        )

    def test_config_file_suffix_follows_hcl_deployment(self):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            f.write(json.dumps({"overlay": "acme", "tfvars_format": "hcl"}))
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        self.assertEqual(artifacts.CONFIG_SUFFIX, ".auto.tfvars.json")
        self.assertEqual(
            artifacts.config_suffix(), artifacts.HCL_CONFIG_SUFFIX
        )
        self.assertEqual(
            artifacts.config_file("tenant", "sample_resource"),
            os.path.join("acme", "config", "tenant",
                         "sample_resource.auto.tfvars"),
        )

    def test_artifacts_does_not_import_ops(self):
        import engine.artifacts
        with open(engine.artifacts.__file__.rstrip("c"), encoding="utf-8") as f:
            source = f.read()
        self.assertNotIn("import ops", source)
        self.assertNotIn("from engine import ops", source)


class ArtifactsRootResolutionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="artifacts-roots-")
        self.saved_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")

    def tearDown(self):
        if self.saved_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.saved_dep
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _deployment(self, roots):
        dep = os.path.join(self.tmp, "deployment.json")
        with open(dep, "w", encoding="utf-8") as f:
            json.dump({"roots": roots}, f)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep

    def test_derived_types_never_join_groups(self):
        self._deployment({"zpa": {"groups": {"zpa_policy_all": [
            "zpa_policy_access_rule",
            "zpa_policy_access_rule_reorder",
        ]}}})
        with self.assertRaises(ValueError) as ctx:
            artifacts.root_label("zpa_policy_access_rule")
        self.assertIn("derived type", str(ctx.exception))

    def test_slug_strategy_excludes_derived_types(self):
        self._deployment({"zpa": {"strategy": "slug"}})
        self.assertEqual(
            artifacts.root_label("zpa_policy_access_rule_reorder"),
            "zpa_policy_access_rule_reorder",
        )
        self.assertEqual(
            artifacts.root_label("zpa_policy_access_rule"), "zpa_policy")

    def test_slug_strategy_excludes_generate_only_types(self):
        self._deployment({"zia": {"strategy": "slug"}})

        self.assertEqual(
            artifacts.root_label("zia_url_categories_predefined"),
            "zia_url_categories_predefined",
        )
        self.assertNotIn(
            "zia_url_categories_predefined",
            artifacts.root_members("zia_url"),
        )
        self.assertIn(
            "zia_url_categories",
            artifacts.root_members("zia_url"),
        )

    def test_explicit_group_may_include_generate_only_type(self):
        self._deployment({
            "zia": {
                "groups": {
                    "zia_url_explicit": [
                        "zia_url_categories",
                        "zia_url_categories_predefined",
                    ],
                },
            },
        })

        self.assertEqual(
            artifacts.root_members("zia_url_explicit"),
            ["zia_url_categories", "zia_url_categories_predefined"],
        )

    def test_no_roots_default_keeps_resource_labels_and_items_var(self):
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = os.devnull
        self.assertEqual(
            artifacts.root_label("zpa_segment_group"),
            "zpa_segment_group",
        )
        self.assertEqual(
            artifacts.root_members("zpa_segment_group"),
            ["zpa_segment_group"],
        )
        self.assertEqual(
            artifacts.env_root("tenant", "zpa_segment_group"),
            os.path.join("envs", "tenant", "zpa_segment_group"),
        )
        self.assertEqual(
            artifacts.tfvars_var_name("zpa_segment_group"),
            "items",
        )
        self.assertIn("zpa_segment_group", artifacts.all_root_labels())

    def test_slug_groups_multiple_members_and_collapses_singletons(self):
        self._deployment({"zpa": {"strategy": "slug"}})

        self.assertEqual(
            artifacts.root_label("zpa_application_segment"),
            "zpa_application",
        )
        self.assertEqual(
            artifacts.root_label("zpa_application_server"),
            "zpa_application",
        )
        self.assertEqual(
            artifacts.root_members("zpa_application"),
            sorted(artifacts.root_members("zpa_application")),
        )
        self.assertIn(
            "zpa_application_segment",
            artifacts.root_members("zpa_application"),
        )
        self.assertEqual(
            artifacts.tfvars_var_name("zpa_application_segment"),
            "zpa_application_segment_items",
        )
        self.assertEqual(
            artifacts.root_label("zpa_segment_group"),
            "zpa_segment_group",
        )
        self.assertEqual(
            artifacts.tfvars_var_name("zpa_segment_group"),
            "items",
        )

    def test_explicit_groups_override_slug_for_their_members(self):
        self._deployment({
            "zpa": {
                "strategy": "slug",
                "groups": {
                    "zpa_custom": [
                        "zpa_application_segment",
                        "zpa_application_server",
                    ],
                },
            },
        })

        self.assertEqual(
            artifacts.root_label("zpa_application_segment"),
            "zpa_custom",
        )
        self.assertEqual(
            artifacts.root_members("zpa_custom"),
            ["zpa_application_segment", "zpa_application_server"],
        )
        self.assertEqual(
            artifacts.root_label("zpa_application_segment_browser_access"),
            "zpa_application",
        )

    def test_explicit_strategy_groups_only_listed_members(self):
        self._deployment({
            "zpa": {
                "groups": {
                    "zpa_custom": [
                        "zpa_application_segment",
                        "zpa_application_server",
                    ],
                },
            },
        })

        self.assertEqual(
            artifacts.root_label("zpa_application_segment"),
            "zpa_custom",
        )
        self.assertEqual(
            artifacts.root_label("zpa_application_segment_browser_access"),
            "zpa_application_segment_browser_access",
        )

    def test_root_resolution_validation_failures(self):
        cases = [
            (
                {"bogus": {}},
                "not a declared provider",
            ),
            (
                {"zpa": {"groups": {"zpa_custom": ["zpa_not_real"]}}},
                "unknown generated resource type",
            ),
            (
                {"zpa": {"groups": {"zpa_custom": ["zia_url_categories"]}}},
                "belongs to provider zia",
            ),
            (
                {
                    "zpa": {
                        "groups": {
                            "zpa_one": ["zpa_segment_group"],
                            "zpa_two": ["zpa_segment_group"],
                        },
                    },
                },
                "more than one roots group",
            ),
            (
                {"zpa": {"groups": {"zpa_segment_group": ["zpa_server_group"]}}},
                "collides with a generated resource type",
            ),
            (
                {"zpa": {"groups": {"bad-label": ["zpa_segment_group"]}}},
                "group labels must match",
            ),
            (
                {
                    "zpa": {"groups": {"shared": ["zpa_segment_group"]}},
                    "zia": {"groups": {"shared": ["zia_rule_labels"]}},
                },
                "collides with another provider group",
            ),
        ]
        for roots, needle in cases:
            self._deployment(roots)
            with self.assertRaises(ValueError) as ctx:
                artifacts.all_root_labels()
            self.assertIn(needle, str(ctx.exception))

    def test_root_resolution_is_deterministic(self):
        self._deployment({"zpa": {"strategy": "slug"}})

        first = (
            artifacts.all_root_labels(),
            artifacts.root_members("zpa_application"),
            artifacts.root_label("zpa_application_segment"),
        )
        second = (
            artifacts.all_root_labels(),
            artifacts.root_members("zpa_application"),
            artifacts.root_label("zpa_application_segment"),
        )

        self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
