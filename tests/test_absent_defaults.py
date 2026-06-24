import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import absent_defaults


FAKE_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "rack_face": {"type": "string", "optional": True},
            "scope_id": {"type": "number", "optional": True},
            "hold": {"type": "bool", "optional": True},
            "hold_after": {"type": "string", "optional": True},
            "include_subdomains": {"type": "bool", "optional": True},
            "labels": {"type": ["map", "string"], "optional": True},
            "tags": {"type": ["list", "string"], "optional": True},
            "settings": {
                "type": ["object", {"mode": "string", "enabled": "bool"}],
                "optional": True,
            },
        },
        "block_types": {
            "rules": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "name": {"type": "string", "required": True},
                        "priority": {"type": "number", "optional": True},
                    }
                },
            }
        },
    }
}


def _update(before, after, resource_type="sample_resource"):
    return {
        "address": "module.%s.%s.this[\"item\"]" % (resource_type, resource_type),
        "type": resource_type,
        "change": {"actions": ["update"], "before": before, "after": after},
    }


class AbsentDefaultsTest(unittest.TestCase):
    def setUp(self):
        self.prev_load = absent_defaults.load_resource
        absent_defaults.load_resource = lambda resource_type: FAKE_SCHEMA
        self.tmp = tempfile.mkdtemp(prefix="absent-defaults-")

    def tearDown(self):
        absent_defaults.load_resource = self.prev_load
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, name, data):
        path = os.path.join(self.tmp, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return path

    def _run(self, argv):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            with contextlib.redirect_stderr(stderr):
                code = absent_defaults.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_projected_optional_placeholders_are_candidates(self):
        report = absent_defaults.build_report("sample_resource", {
            "device": {
                "name": "device",
                "rack_face": "",
                "scope_id": 0,
                "labels": {},
                "tags": [],
            }
        })

        paths = dict(
            (item["path"], item)
            for item in report["projected_items"]["device"]
        )
        self.assertEqual(paths["rack_face"]["status"], "absent_default_candidate")
        self.assertEqual(paths["rack_face"]["value_kind"], "empty_string")
        self.assertEqual(paths["scope_id"]["value_kind"], "zero")
        self.assertEqual(paths["labels"]["value_kind"], "empty_object")
        self.assertEqual(paths["tags"]["value_kind"], "empty_list")
        self.assertEqual(report["summary"]["projected_candidates"], 4)

    def test_required_placeholder_is_classified_separately(self):
        report = absent_defaults.build_report("sample_resource", {
            "bad": {"name": "", "rack_face": ""}
        })
        statuses = dict(
            (item["path"], item["status"])
            for item in report["projected_items"]["bad"]
        )
        self.assertEqual(statuses["name"], "required_placeholder_observed")
        self.assertEqual(statuses["rack_face"], "absent_default_candidate")
        self.assertEqual(report["summary"]["required_placeholders"], 1)

    def test_nested_projected_placeholders_normalize_list_paths(self):
        report = absent_defaults.build_report("sample_resource", {
            "rules": {
                "name": "root",
                "rules": [
                    {"name": "one", "priority": 0},
                    {"name": "two", "priority": 2},
                ],
            }
        })
        self.assertEqual(
            report["projected_items"]["rules"],
            [{
                "status": "absent_default_candidate",
                "path": "rules[].priority",
                "value_kind": "zero",
                "schema": "optional",
                "confidence": "medium",
                "source": "projected",
            }],
        )

    def test_plan_absent_default_drift_candidate(self):
        plan = {
            "resource_changes": [
                _update(
                    {
                        "name": "zone",
                        "hold": None,
                        "hold_after": None,
                        "include_subdomains": None,
                    },
                    {
                        "name": "zone",
                        "hold": False,
                        "hold_after": "",
                        "include_subdomains": False,
                    },
                )
            ]
        }

        report = absent_defaults.build_report("sample_resource", plan=plan)

        self.assertEqual(report["summary"]["plan_absent_default_candidates"], 3)
        by_path = dict((item["path"], item) for item in report["plan_changes"])
        self.assertEqual(
            by_path["hold"]["status"],
            "absent_default_drift_candidate",
        )
        self.assertEqual(by_path["hold"]["before_kind"], "null")
        self.assertEqual(by_path["hold"]["after_kind"], "false")
        self.assertEqual(by_path["hold_after"]["after_kind"], "empty_string")
        self.assertEqual(by_path["include_subdomains"]["after_kind"], "false")

    def test_plan_other_update_is_not_absent_default_candidate(self):
        plan = {
            "resource_changes": [
                _update({"name": "old"}, {"name": "new"})
            ]
        }
        report = absent_defaults.build_report("sample_resource", plan=plan)
        self.assertEqual(report["summary"]["plan_absent_default_candidates"], 0)
        self.assertEqual(report["summary"]["plan_other_updates"], 1)
        self.assertEqual(report["plan_changes"][0]["status"], "other_update")

    def test_computed_only_placeholder_update_is_not_optional_candidate(self):
        plan = {
            "resource_changes": [
                _update({"id": None}, {"id": ""})
            ]
        }
        report = absent_defaults.build_report("sample_resource", plan=plan)
        self.assertEqual(report["plan_changes"][0]["schema"], "computed_only")
        self.assertEqual(report["plan_changes"][0]["status"], "placeholder_update")

    def test_cli_builds_report_from_projected_and_plan(self):
        projected = self._write_json("projected.json", {
            "items": {"item": {"name": "item", "rack_face": ""}}
        })
        plan = self._write_json("plan.json", {
            "resource_changes": [
                _update({"name": "item", "hold": None},
                        {"name": "item", "hold": False})
            ]
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--projected", projected,
            "--plan", plan,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["projected_candidates"], 1)
        self.assertEqual(report["summary"]["plan_absent_default_candidates"], 1)


if __name__ == "__main__":
    unittest.main()
