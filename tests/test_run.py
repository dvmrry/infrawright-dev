"""Tests for the active-pack-aware unittest selector."""
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


if __name__ == "__main__":
    unittest.main()
