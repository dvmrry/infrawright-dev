import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import packs
from engine import provider_config


def _update(resource_type, before, after):
    return {
        "address": "module.%s.%s.this[\"item\"]" % (resource_type, resource_type),
        "type": resource_type,
        "change": {"actions": ["update"], "before": before, "after": after},
    }


class ProviderConfigDiagnosticsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="provider-config-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = self.tmp
        self._write_pack()
        packs.reset()

    def tearDown(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        packs.reset()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write_json(self, path, data):
        full = os.path.join(self.tmp, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as f:
            json.dump(data, f)
        return full

    def _write_pack(self, requirements=None):
        if requirements is None:
            requirements = [{
                "id": "sample_disable_attribution_label",
                "setting": "add_sample_attribution_label",
                "value": False,
                "reason": "Provider adds a management label by default.",
                "resource_types": ["sample_topic"],
                "plan_paths": ["terraform_labels.goog-terraform-provisioned"],
            }]
        self._write_json("sample/pack.json", {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
            "provider_config": {"requirements": requirements},
        })
        packs.reset()

    def _run(self, argv):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            with contextlib.redirect_stderr(stderr):
                code = provider_config.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_pack_requirement_matches_plan_drift(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 0)
        change = report["plan_changes"][0]
        self.assertEqual(change["status"], "provider_config_requirement")
        self.assertEqual(change["setting"], "add_sample_attribution_label")
        self.assertEqual(change["value"], False)

    def test_same_path_without_metadata_is_not_inferred(self):
        self._write_pack(requirements=[])
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 0)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)
        self.assertEqual(
            report["plan_changes"][0]["status"],
            "unmatched_plan_change",
        )

    def test_requirement_resource_scope_is_respected(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_other",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        }

        report = provider_config.build_report(provider="sample", plan=plan)

        self.assertEqual(report["summary"]["provider_config_matches"], 0)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)

    def test_resource_type_filter_limits_plan_changes(self):
        plan = {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                ),
                _update("sample_topic", {"name": "old"}, {"name": "new"}),
                _update("sample_other", {"name": "old"}, {"name": "new"}),
            ]
        }

        report = provider_config.build_report(
            resource_type="sample_topic",
            plan=plan,
        )

        self.assertEqual(report["provider"], "sample")
        self.assertEqual(report["summary"]["plan_changes"], 2)
        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(report["summary"]["unmatched_plan_changes"], 1)

    def test_bad_metadata_fails_loudly(self):
        self._write_pack(requirements=[{
            "id": "sample_bad",
            "setting": "missing_plan_paths",
            "value": False,
            "reason": "bad fixture",
        }])

        with self.assertRaises(ValueError) as ctx:
            provider_config.build_report(provider="sample", plan={})

        self.assertIn(
            "sample_bad plan_paths must be a non-empty list",
            str(ctx.exception),
        )

    def test_cli_reads_pack_metadata_and_plan(self):
        plan_path = self._write_json("plan.json", {
            "resource_changes": [
                _update(
                    "sample_topic",
                    {"terraform_labels": {}},
                    {
                        "terraform_labels": {
                            "goog-terraform-provisioned": "true",
                        }
                    },
                )
            ]
        })

        code, out, err = self._run([
            "--provider", "sample",
            "--plan", plan_path,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["summary"]["provider_config_matches"], 1)
        self.assertEqual(
            report["requirements"][0]["setting"],
            "add_sample_attribution_label",
        )


if __name__ == "__main__":
    unittest.main()
