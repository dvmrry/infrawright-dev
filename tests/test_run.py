"""Tests for the active-pack-aware unittest selector."""
import os
import unittest

from engine import pack_set
from tests import run


class TestRunnerSelectionTest(unittest.TestCase):
    def _suite_and_rules(self):
        class SampleTest(unittest.TestCase):
            def test_core(self):
                pass

            def test_pack(self):
                pass

        discovered = unittest.defaultTestLoader.loadTestsFromTestCase(
            SampleTest
        )
        tests = list(run.iter_tests(discovered))
        suite = unittest.TestSuite(tests)
        pack_test_id = next(
            test.id() for test in tests if test.id().endswith(".test_pack")
        )
        rules = [{
            "prefix": pack_test_id + ".",
            "packs": ["sample"],
            "shared": [],
            "reason": "sample pack assertion",
        }]
        return suite, rules

    def test_missing_pack_omits_only_declared_test(self):
        suite, rules = self._suite_and_rules()
        selection = run.select_tests(
            suite, rules, {"packs": [], "shared": []}
        )
        self.assertEqual(len(selection["selected"]), 1)
        self.assertEqual(len(selection["omitted"]), 1)
        self.assertTrue(selection["omitted"][0]["id"].endswith("test_pack"))

    def test_present_pack_runs_every_test(self):
        suite, rules = self._suite_and_rules()
        selection = run.select_tests(
            suite, rules,
            {"packs": ["sample"], "shared": []},
        )
        self.assertEqual(len(selection["selected"]), 2)
        self.assertEqual(selection["omitted"], [])

    def test_stale_rule_is_rejected(self):
        suite, _rules = self._suite_and_rules()
        rules = [{
            "prefix": "tests.test_missing.Class.",
            "packs": ["sample"],
            "shared": [],
            "reason": "stale",
        }]
        with self.assertRaisesRegex(
                run.TestRequirementsError, "stale test requirement"):
            run.select_tests(
                suite, rules, {"packs": [], "shared": []}
            )

    def test_missing_module_requirement_prevents_import(self):
        rules = [{
            "prefix": "tests.test_pack_only.",
            "packs": ["sample"],
            "shared": [],
            "reason": "pack-only module",
        }]

        class Loader(object):
            def __init__(self):
                self.loaded = []

            def loadTestsFromName(self, name):
                self.loaded.append(name)
                return unittest.TestSuite()

        loader = Loader()
        result = run.load_selected_modules(
            ["tests.test_core", "tests.test_pack_only"],
            rules,
            {"packs": [], "shared": []},
            loader=loader,
        )
        self.assertEqual(loader.loaded, ["tests.test_core"])
        self.assertEqual(
            result["omitted_modules"][0]["module"],
            "tests.test_pack_only",
        )
        self.assertEqual(
            result["matched_rules"], {"tests.test_pack_only."}
        )

    def test_requirement_typo_outside_catalog_is_rejected(self):
        rules = [{
            "prefix": "tests.test_pack_only.",
            "packs": ["typo"],
            "shared": [],
            "reason": "bad reference",
        }]
        with self.assertRaisesRegex(
                pack_set.PackSetError, "unknown packs: typo"):
            run.validate_rule_catalog(
                rules,
                {"packs": ["known"], "shared": []},
                "requirements.json",
            )

    def test_committed_zia_cohort_source_gates_compose_by_profile(self):
        rules = run.load_requirements(os.path.join(
            os.path.dirname(__file__), "pack-test-requirements.json"
        ))
        module = "tests.test_zia_transform_cohort_catalog"
        class_name = ".ZiaTransformCohortCatalogTest."
        methods = [
            "test_cli_resource_selection_is_an_exact_byte_gate",
            "test_committed_catalog_matches_exact_rendered_bytes",
            "test_default_zcc_generation_remains_byte_identical",
            "test_selection_rejects_duplicates_and_wrong_product",
        ]
        source_rules = [
            rule for rule in rules
            if rule["prefix"].startswith(module + class_name)
        ]
        self.assertEqual(len(source_rules), 4)

        class NamedTest(unittest.TestCase):
            def __init__(self, test_id):
                unittest.TestCase.__init__(self, "runTest")
                self._test_id = test_id

            def runTest(self):
                pass

            def id(self):
                return self._test_id

        expected = {
            "empty": ([], []),
            "zcc": (["zcc"], methods[2:]),
            "zia": (["zia"], methods[:2]),
            "zscaler": (["zcc", "zia"], methods),
        }
        for profile, (packs, selected_methods) in sorted(expected.items()):
            with self.subTest(profile=profile):
                suite = unittest.TestSuite([
                    NamedTest(module + class_name + method)
                    for method in methods
                ])
                selection = run.select_tests(
                    suite,
                    source_rules,
                    {"packs": packs, "shared": []},
                )
                selected = sorted(
                    test.id().rsplit(".", 1)[-1]
                    for test in selection["selected"]
                )
                self.assertEqual(selected, sorted(selected_methods))
                self.assertEqual(
                    len(selection["omitted"]),
                    len(methods) - len(selected_methods),
                )

        artifact_only = run.requirements_for(
            module + class_name
            + "test_catalog_is_the_closed_source_bound_cohort",
            rules,
        )
        self.assertEqual(artifact_only["packs"], [])
        self.assertEqual(artifact_only["shared"], [])

    def test_committed_mixed_ops_classes_have_per_test_provider_requirements(self):
        rules = run.load_requirements(os.path.join(
            os.path.dirname(__file__), "pack-test-requirements.json"
        ))
        expected = {
            "OpsEnvDiscoveryTest.test_explicit_tenant_resolves_under_active_overlay":
                ("zia", []),
            "OpsEnvDiscoveryTest.test_grouped_root_discovery_and_member_selection_note":
                ("zpa", ["zscaler"]),
            "OpsEnvDiscoveryTest.test_malformed_deployment_does_not_fall_back_to_root_envs":
                ("zia", []),
            "OpsEnvDiscoveryTest.test_no_tenant_discovery_uses_only_active_overlay_envs":
                ("zia", []),
            "OpsEnvDiscoveryTest.test_no_tenant_discovery_uses_root_for_dot_overlay":
                ("zia", []),
            "OpsEnvDiscoveryTest.test_no_tenant_discovery_uses_root_when_no_overlay":
                ("zia", []),
            "OpsStageImportsTest.test_grouped_stage_imports_copies_each_member_file_to_shared_root":
                ("zpa", ["zscaler"]),
            "OpsStageImportsTest.test_stage_imports_copies_flat_resource_type_file":
                ("zia", []),
            "OpsStageImportsTest."
            "test_stage_imports_mentions_transform_or_adopt_when_sources_missing":
                ("zia", []),
        }
        for suffix, (pack, shared) in sorted(expected.items()):
            with self.subTest(test=suffix):
                required = run.requirements_for(
                    "tests.test_ops.%s" % suffix, rules
                )
                self.assertEqual(required["packs"], [pack])
                self.assertEqual(required["shared"], shared)


if __name__ == "__main__":
    unittest.main()
