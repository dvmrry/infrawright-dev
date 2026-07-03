import json
import os
import shutil
import tempfile
import unittest

from engine import lanes
from engine import packs


def _minimal_rule(lane_name):
    rule = {
        "id": "r1",
        "provider": "demo",
        "resource_type": "demo_thing",
        "path": "field",
        "action": "diagnostic_only",
        "evidence": "docs/x.md",
        "reason": "because",
    }
    if lane_name == "absent_defaults":
        rule["kind"] = "api_absent"
    elif lane_name == "dynamic_schema":
        rule.update({
            "kind": "provider_state_only",
            "ownership": "provider_computed",
            "provider_version_constraint": ">= 1.0",
        })
    else:
        rule.update({
            "kind": "sensitive_required_attribute",
            "sensitivity": "sensitive_attribute",
            "structural_requirement": "attribute_required_for_valid_config",
            "provider_version_constraint": ">= 1.0",
        })
    return rule


def _sensitive_paths(lane_name, path="field"):
    if lane_name == "sensitive_required":
        return [path]
    return None


def _pack_rule(lane_name, provider, include_provider=False):
    rule = _minimal_rule(lane_name)
    rule["id"] = "%s_rule" % provider
    rule["resource_type"] = "%s_thing" % provider
    if include_provider:
        rule["provider"] = provider
    else:
        rule.pop("provider", None)
    return rule


class SharedSkeletonTest(unittest.TestCase):
    def _lanes(self):
        return sorted(lanes.LANES)

    def test_none_rules_validates_empty(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.assertEqual(lanes.validate_rules(lanes.LANES[name], None), [])

    def test_empty_rules_validates_empty(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.assertEqual(lanes.validate_rules(lanes.LANES[name], []), [])

    def test_rules_not_list(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(lanes.LANES[name], {})
                self.assertIn("must be a list", str(ctx.exception))

    def test_rule_not_object(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(lanes.LANES[name], [None])
                self.assertIn("must be an object", str(ctx.exception))

    def test_missing_id(self):
        self._assert_missing_key("id")

    def test_missing_provider(self):
        self._assert_missing_key("provider")

    def test_missing_path(self):
        self._assert_missing_key("path")

    def test_missing_kind(self):
        self._assert_missing_key("kind")

    def test_missing_action(self):
        self._assert_missing_key("action")

    def _assert_missing_key(self, key):
        for name in self._lanes():
            with self.subTest(lane=name, key=key):
                rule = _minimal_rule(name)
                rule.pop(key, None)
                sens = _sensitive_paths(name)
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn("missing %s" % key, str(ctx.exception))

    def test_missing_resource_scope(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule.pop("resource_type", None)
                sens = _sensitive_paths(name)
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn("missing resource scope", str(ctx.exception))

    def test_unknown_kind(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule["kind"] = "unknown_kind"
                sens = _sensitive_paths(name)
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn("unknown kind", str(ctx.exception))

    def test_unknown_action(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule["action"] = "unknown_action"
                sens = _sensitive_paths(name)
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn("unknown action", str(ctx.exception))

    def test_optional_evidence_paths_accepted(self):
        evidence_by_lane = {
            "absent_defaults": {
                "plan_path": "field",
                "raw_api_path": "field",
                "provider_state_path": "field",
            },
            "dynamic_schema": {
                "raw_api_path": "field",
                "projected_path": "field",
                "plan_path": "field",
            },
            "sensitive_required": {
                "raw_api_path": "field",
                "projected_path": "field",
                "plan_path": "field",
            },
        }
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule.update(evidence_by_lane[name])
                sens = _sensitive_paths(name)
                out = lanes.validate_rules(
                    lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertEqual(len(out), 1)

    def test_resource_prefix_scope_accepted(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule.pop("resource_type", None)
                rule["resource_prefix"] = "demo_"
                sens = _sensitive_paths(name)
                out = lanes.validate_rules(
                    lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertEqual(out[0]["resource_prefix"], "demo_")

    def test_minimal_rule_validates_in_every_lane(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                spec = lanes.LANES[name]
                sens = ["field"] if name == "sensitive_required" else None
                out = lanes.validate_rules(
                    spec, [_minimal_rule(name)], sensitive_paths=sens)
                self.assertEqual(len(out), 1)

    def test_unknown_key_rejected_in_every_lane(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule["surprise"] = "x"
                sens = ["field"] if name == "sensitive_required" else None
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn("unknown rule key surprise", str(ctx.exception))

    def test_duplicate_identity_rejected_in_every_lane(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                sens = ["field"] if name == "sensitive_required" else None
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule, dict(rule)],
                        sensitive_paths=sens)
                self.assertIn("duplicate rule", str(ctx.exception))

    def test_scope_values_are_stripped_in_identity_everywhere(self):
        # Deliberate-change #2: harmonized strip across lanes.
        for name in self._lanes():
            with self.subTest(lane=name):
                first = _minimal_rule(name)
                second = _minimal_rule(name)
                second["resource_type"] = " demo_thing "
                sens = ["field"] if name == "sensitive_required" else None
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [first, second],
                        sensitive_paths=sens)
                self.assertIn("duplicate rule", str(ctx.exception))

    def test_both_scopes_rejected_everywhere(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                rule["resource_prefix"] = "demo_"
                sens = ["field"] if name == "sensitive_required" else None
                with self.assertRaises(ValueError) as ctx:
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens)
                self.assertIn(
                    "cannot specify both resource_type and resource_prefix",
                    str(ctx.exception))

    def test_sensitive_polarity_forbid_vs_require(self):
        forbid = _minimal_rule("dynamic_schema")
        with self.assertRaises(ValueError):
            lanes.validate_rules(
                lanes.LANES["dynamic_schema"], [forbid],
                sensitive_paths=["field"])
        require = _minimal_rule("sensitive_required")
        with self.assertRaises(ValueError):
            lanes.validate_rules(
                lanes.LANES["sensitive_required"], [require],
                sensitive_paths=["other_field"])

    def test_provider_prefix_mismatch_rejected_everywhere(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                rule = _minimal_rule(name)
                sens = ["field"] if name == "sensitive_required" else None
                with self.assertRaises(ValueError):
                    lanes.validate_rules(
                        lanes.LANES[name], [rule], sensitive_paths=sens,
                        provider_prefixes={"demo_": "otherprov"})


class RuleMatchTest(unittest.TestCase):
    def test_type_match_and_prefix_match(self):
        self.assertTrue(lanes.rule_matches(
            {"provider": "p", "resource_type": "p_a"}, "p", "p_a"))
        self.assertTrue(lanes.rule_matches(
            {"provider": "p", "resource_prefix": "p_"}, "p", "p_a"))
        self.assertFalse(lanes.rule_matches(
            {"provider": "q", "resource_type": "p_a"}, "p", "p_a"))

    def test_plan_path_prefers_plan_path_and_normalizes(self):
        self.assertEqual(
            lanes.rule_plan_path({"plan_path": "a[0].b", "path": "x"}), "a[].b")
        self.assertEqual(lanes.rule_plan_path({"path": "x"}), "x")


class LanePacksAccessorTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.prev = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        packs.reset()

    def tearDown(self):
        if self.prev is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _lanes(self):
        return sorted(lanes.LANES)

    def _accessor(self, lane_name):
        if lane_name == "absent_defaults":
            return packs.absent_default_rules
        if lane_name == "dynamic_schema":
            return packs.dynamic_schema_rules
        return packs.sensitive_required_rules

    def _write_pack(self, name, provider, lane_name, rule):
        path = os.path.join(self.tmp, name)
        os.makedirs(path)
        with open(os.path.join(path, "pack.json"), "w", encoding="utf-8") as f:
            json.dump({
                "provider_prefixes": {"%s_" % provider: provider},
                lane_name: {"rules": [rule]},
            }, f)
        packs.reset()

    def test_accessor_reads_and_validates_rules(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.tearDown()
                self.setUp()
                self._write_pack(
                    "demo", "demo", name, _pack_rule(name, "demo"))
                rules = self._accessor(name)()
                self.assertEqual(len(rules), 1)
                self.assertEqual(rules[0]["provider"], "demo")
                self.assertEqual(rules[0]["id"], "demo_rule")

    def test_accessor_filters_by_provider(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.tearDown()
                self.setUp()
                self._write_pack(
                    "demo", "demo", name, _pack_rule(name, "demo"))
                self._write_pack(
                    "other", "other", name, _pack_rule(name, "other"))
                rules = self._accessor(name)("demo")
                self.assertEqual([r["id"] for r in rules], ["demo_rule"])

    def test_accessor_raises_on_invalid_rules(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.tearDown()
                self.setUp()
                rule = _pack_rule(name, "demo")
                rule["kind"] = "unknown_kind"
                self._write_pack("demo", "demo", name, rule)
                with self.assertRaises(ValueError) as ctx:
                    self._accessor(name)()
                self.assertIn("unknown kind", str(ctx.exception))

    def test_accessor_raises_on_provider_resource_mismatch(self):
        for name in self._lanes():
            with self.subTest(lane=name):
                self.tearDown()
                self.setUp()
                rule = _pack_rule(name, "demo", include_provider=True)
                rule["provider"] = "other"
                self._write_pack("demo", "demo", name, rule)
                with self.assertRaises(ValueError) as ctx:
                    self._accessor(name)()
                self.assertIn("not other", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
