import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import sensitive_required


FAKE_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "password": {"type": "string", "required": True},
            "token": {"type": "string", "optional": True},
        },
        "block_types": {
            "credentials": {
                "nesting_mode": "single",
                "min_items": 1,
                "block": {
                    "attributes": {
                        "secret": {"type": "string", "required": True},
                        "label": {"type": "string", "optional": True},
                    }
                },
            },
            "webhook": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "url": {"type": "string", "required": True},
                        "uid": {"type": "string", "optional": True},
                    }
                },
            },
        },
    }
}


class SensitiveRequiredTest(unittest.TestCase):
    def setUp(self):
        self.prev_load = sensitive_required.load_resource
        sensitive_required.load_resource = lambda resource_type: FAKE_SCHEMA
        self.tmp = tempfile.mkdtemp(prefix="sensitive-required-")

    def tearDown(self):
        sensitive_required.load_resource = self.prev_load
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
                code = sensitive_required.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_schema_required_sensitive_attribute_is_confirmed(self):
        report = sensitive_required.build_report("sample_resource", {
            "item": {
                "values": {"name": "item", "password": "secret"},
                "sensitive_values": {"password": True},
            }
        }, projected_items_by_key={"item": {"name": "item"}})

        marker = report["items"]["item"][0]
        self.assertEqual(marker["path"], "password")
        self.assertEqual(marker["status"], "sensitive_required_schema")
        self.assertEqual(marker["schema"], "required")
        self.assertEqual(marker["required_evidence"], ["schema"])
        self.assertEqual(report["summary"]["sensitive_required_schema"], 1)

    def test_validation_required_optional_block_is_confirmed(self):
        report = sensitive_required.build_report("sample_resource", {
            "contact": {
                "values": {
                    "name": "contact",
                    "password": "ignored",
                    "webhook": [{"url": "https://example.test/hook"}],
                },
                "sensitive_values": {"webhook": True},
            }
        }, projected_items_by_key={
            "contact": {"name": "contact", "password": "manual"}
        }, required_paths={"contact": ["webhook"]})

        marker = report["items"]["contact"][0]
        self.assertEqual(marker["path"], "webhook")
        self.assertEqual(marker["marker"], "container")
        self.assertEqual(marker["status"], "sensitive_required_validation")
        self.assertEqual(marker["schema"], "optional")
        self.assertEqual(marker["required_evidence"], ["validation"])

    def test_optional_sensitive_leaf_is_structural_candidate(self):
        report = sensitive_required.build_report("sample_resource", {
            "item": {
                "values": {"name": "item", "password": "manual", "token": "secret"},
                "sensitive_values": {"token": True},
            }
        }, projected_items_by_key={"item": {"name": "item", "password": "manual"}})

        marker = report["items"]["item"][0]
        self.assertEqual(marker["path"], "token")
        self.assertEqual(marker["status"], "sensitive_structural_candidate")
        self.assertEqual(marker["schema"], "optional")
        self.assertEqual(marker["required_evidence"], [])

    def test_projected_sensitive_path_is_classified_present(self):
        report = sensitive_required.build_report("sample_resource", {
            "item": {
                "values": {"name": "item", "password": "manual", "token": "secret"},
                "sensitive_values": {"token": True},
            }
        }, projected_items_by_key={
            "item": {"name": "item", "password": "manual", "token": "redacted"}
        })

        marker = report["items"]["item"][0]
        self.assertEqual(marker["status"], "sensitive_present")
        self.assertEqual(marker["projected"], "present")
        self.assertEqual(report["summary"]["sensitive_present"], 1)

    def test_sensitive_list_leaf_normalizes_path_and_schema(self):
        report = sensitive_required.build_report("sample_resource", {
            "contact": {
                "values": {
                    "name": "contact",
                    "password": "manual",
                    "webhook": [{"url": "https://example.test/hook"}],
                },
                "sensitive_values": {"webhook": [{"url": True}]},
            }
        }, projected_items_by_key={
            "contact": {"name": "contact", "password": "manual"}
        })

        marker = report["items"]["contact"][0]
        self.assertEqual(marker["path"], "webhook[].url")
        self.assertEqual(marker["marker"], "leaf")
        self.assertEqual(marker["schema"], "required")
        self.assertEqual(marker["status"], "sensitive_required_schema")

    def test_cli_builds_report_with_required_path(self):
        oracle = self._write_json("oracle.json", {
            "contact": {
                "values": {
                    "name": "contact",
                    "password": "manual",
                    "webhook": [{"url": "https://example.test/hook"}],
                },
                "sensitive_values": {"webhook": True},
            }
        })
        projected = self._write_json("projected.json", {
            "items": {"contact": {"name": "contact", "password": "manual"}}
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--oracle-state", oracle,
            "--projected", projected,
            "--required-path", "webhook",
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["sensitive_required_validation"], 1)
        self.assertEqual(
            report["items"]["contact"][0]["status"],
            "sensitive_required_validation",
        )


if __name__ == "__main__":
    unittest.main()
