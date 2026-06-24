import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import dynamic_schema


FAKE_SCHEMA = {
    "block": {
        "attributes": {
            "id": {"type": "string", "computed": True},
            "name": {"type": "string", "required": True},
            "data": {"type": ["map", "string"], "optional": True},
            "settings": {"type": "dynamic", "optional": True},
            "assets": {
                "type": [
                    "object",
                    {
                        "config": ["object", {}],
                        "known": "string",
                    },
                ],
                "optional": True,
            },
        },
        "block_types": {
            "rules": {
                "nesting_mode": "list",
                "block": {
                    "attributes": {
                        "action": {"type": "string", "required": True},
                        "metadata": {"type": ["map", "string"], "optional": True},
                    }
                },
            },
            "status": {
                "nesting_mode": "single",
                "block": {
                    "attributes": {
                        "enabled": {"type": "bool", "computed": True},
                    }
                },
            },
        },
    }
}


class DynamicSchemaTest(unittest.TestCase):
    def setUp(self):
        self.prev_load = dynamic_schema.load_resource
        dynamic_schema.load_resource = lambda resource_type: FAKE_SCHEMA
        self.tmp = tempfile.mkdtemp(prefix="dynamic-schema-")

    def tearDown(self):
        dynamic_schema.load_resource = self.prev_load
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
                code = dynamic_schema.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_map_leaf_reports_pack_schema_gap(self):
        out = dynamic_schema.classify_path("sample_resource", "data.flags")
        self.assertEqual(out["status"], "pack_schema_gap")
        self.assertEqual(out["classification"], "map_key")
        self.assertEqual(out["schema_path"], "data")

    def test_dynamic_leaf_reports_pack_schema_gap(self):
        out = dynamic_schema.classify_path("sample_resource", "settings.mode")
        self.assertEqual(out["status"], "pack_schema_gap")
        self.assertEqual(out["classification"], "dynamic_value")
        self.assertEqual(out["schema_path"], "settings")

    def test_open_object_member_reports_pack_schema_gap(self):
        out = dynamic_schema.classify_path(
            "sample_resource",
            "assets.config.run_worker_first",
        )
        self.assertEqual(out["status"], "pack_schema_gap")
        self.assertEqual(out["classification"], "open_object_member")
        self.assertEqual(out["schema_path"], "assets.config")

    def test_known_schema_path_is_not_a_gap(self):
        out = dynamic_schema.classify_path("sample_resource", "assets.known")
        self.assertEqual(out["status"], "schema_known")
        self.assertEqual(out["classification"], "attribute")

    def test_nested_block_map_path_reports_pack_schema_gap(self):
        out = dynamic_schema.classify_path(
            "sample_resource",
            "rules[*].metadata.provider_key",
        )
        self.assertEqual(out["status"], "pack_schema_gap")
        self.assertEqual(out["classification"], "map_key")
        self.assertEqual(out["schema_path"], "rules[].metadata")

    def test_computed_only_path_is_classified(self):
        out = dynamic_schema.classify_path("sample_resource", "id")
        self.assertEqual(out["status"], "schema_computed_only")
        self.assertEqual(out["classification"], "computed_only")

    def test_unknown_top_level_path_is_classified(self):
        out = dynamic_schema.classify_path("sample_resource", "missing.path")
        self.assertEqual(out["status"], "unknown_schema_path")
        self.assertEqual(out["classification"], "unknown_segment")

    def test_build_report_includes_policy_projection_omit_paths(self):
        policy_path = self._write_json("policy.json", {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "data.flags",
                            "reason": "temporary lab prune",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--policy", policy_path,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["pack_schema_gap"], 1)
        self.assertEqual(
            report["paths"]["data.flags"]["classification"],
            "map_key",
        )

    def test_cli_accepts_paths_json(self):
        paths_json = self._write_json("paths.json", {
            "sample_resource": [
                "data.flags",
                "assets.known",
            ]
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--paths-json", paths_json,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["paths"], 2)
        self.assertEqual(report["summary"]["pack_schema_gap"], 1)
        self.assertEqual(report["summary"]["schema_known"], 1)


if __name__ == "__main__":
    unittest.main()
