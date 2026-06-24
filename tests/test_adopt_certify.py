import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import adopt_certify
from engine import packs
from engine import registry


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)
    return path


class AdoptCertifyCliTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="adopt-certify-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        os.environ["INFRAWRIGHT_PACKS"] = os.path.join(self.tmp, "packs")
        os.makedirs(os.path.join(
            self.tmp, "packs", "sample", "overrides"), exist_ok=True)
        _write_json(os.path.join(
            self.tmp, "packs", "sample", "pack.json"), {
                "provider_prefixes": {"sample_": "sample"},
                "provider_sources": {"sample": "example/sample"},
            })
        _write_json(os.path.join(
            self.tmp, "packs", "sample", "registry.json"), {
                "sample_resource": {
                    "generate": True,
                    "product": "sample",
                    "adopt": {"key_field": "name", "import_id": "{id}"},
                }
            })
        packs.reset()
        registry.reload_registry()

    def tearDown(self):
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _path(self, name, data):
        return _write_json(os.path.join(self.tmp, name), data)

    def _run(self, argv):
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            with contextlib.redirect_stderr(stderr):
                code = adopt_certify.main(argv)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_cli_builds_report_from_raw_list(self):
        raw = self._path("raw.json", [
            {
                "id": "123",
                "name": "Prod App",
                "apiOnly": {"mode": "strict"},
            }
        ])
        oracle = self._path("oracle.json", {
            "prod_app": {
                "values": {
                    "name": "Prod App",
                    "enabled": True,
                    "provider_default": {"mode": "auto"},
                },
                "sensitive_values": {},
            }
        })
        projected = self._path("projected.json", {
            "items": {
                "prod_app": {
                    "name": "Prod App",
                    "enabled": True,
                }
            }
        })
        policy = self._path("policy.json", {
            "version": 1,
            "resource_types": {
                "sample_resource": {
                    "projection_omit": [
                        {
                            "path": "provider_default.mode",
                            "reason": "provider default",
                            "approved_by": "unit",
                        }
                    ]
                }
            },
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--raw", raw,
            "--oracle-state", oracle,
            "--projected", projected,
            "--policy", policy,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertEqual(report["metadata"], {
            "mode": "static_advisory_diff",
            "oracle_import": "not_run_by_cli",
            "projection": "not_run_by_cli",
            "terraform_plan": "not_run_by_cli",
            "plan_cleanliness": "not_computed_by_cli_use_assert_adoptable",
            "required_missing": "caller_supplied_not_computed_by_cli",
            "sensitive_blocked": (
                "derived_from_oracle_sensitive_values_or_caller_supplied"
            ),
        })
        self.assertEqual(report["summary"]["items"], 1)
        self.assertEqual(report["summary"]["required_missing"], 0)
        self.assertEqual(report["summary"]["sensitive_blocked"], 0)
        self.assertEqual(
            report["items"]["prod_app"]["raw_only_paths"],
            ["api_only.mode", "id"],
        )
        self.assertEqual(
            report["items"]["prod_app"]["omitted_by_policy"],
            ["provider_default.mode"],
        )

    def test_cli_derives_sensitive_blocked_from_oracle_state(self):
        raw = self._path("raw.json", {
            "prod_app": {
                "name": "Prod App",
            }
        })
        oracle = self._path("oracle.json", {
            "prod_app": {
                "values": {
                    "name": "Prod App",
                    "webhook": [{"url": "https://example.test/hook"}],
                },
                "sensitive_values": {
                    "webhook": True,
                },
            }
        })
        projected = self._path("projected.json", {
            "items": {
                "prod_app": {
                    "name": "Prod App",
                }
            }
        })

        code, out, err = self._run([
            "--resource-type", "sample_resource",
            "--raw", raw,
            "--oracle-state", oracle,
            "--projected", projected,
        ])

        self.assertEqual(code, 0, err)
        report = json.loads(out)
        self.assertGreaterEqual(report["summary"]["sensitive_blocked"], 1)
        self.assertEqual(
            report["items"]["prod_app"]["sensitive_blocked"],
            ["webhook"],
        )

    def test_cli_rejects_duplicate_raw_list_keys(self):
        raw = self._path("raw.json", [
            {"id": "1", "name": "Prod App"},
            {"id": "2", "name": "Prod App"},
        ])
        oracle = self._path("oracle.json", {})
        projected = self._path("projected.json", {"items": {}})

        code, _out, err = self._run([
            "--resource-type", "sample_resource",
            "--raw", raw,
            "--oracle-state", oracle,
            "--projected", projected,
        ])

        self.assertEqual(code, 1)
        self.assertIn("duplicate derived raw key 'prod_app'", err)

    def test_cli_rejects_missing_projected_key(self):
        raw = self._path("raw.json", {
            "prod_app": {"name": "Prod App"},
        })
        oracle = self._path("oracle.json", {
            "prod_app": {"values": {"name": "Prod App"}},
        })
        projected = self._path("projected.json", {"items": {}})

        code, _out, err = self._run([
            "--resource-type", "sample_resource",
            "--raw", raw,
            "--oracle-state", oracle,
            "--projected", projected,
        ])

        self.assertEqual(code, 1)
        self.assertIn("projected key mismatch", err)
        self.assertIn("missing keys: prod_app", err)

    def test_docs_state_cli_is_static_and_projection_diagnostics_are_limited(self):
        docs_path = os.path.join(
            os.path.dirname(os.path.dirname(__file__)),
            "docs",
            "adopt-certification.md",
        )
        with open(docs_path, encoding="utf-8") as f:
            text = f.read()

        self.assertIn("static advisory diff", text)
        self.assertIn(
            "does not run oracle import, projection, or Terraform plan",
            text,
        )
        self.assertIn(
            "plan cleanliness comes from `assert-adoptable`",
            text.lower(),
        )
        self.assertIn(
            "`required_missing` is not computed by this CLI",
            text,
        )
        self.assertIn(
            "`sensitive_blocked` can be derived by this CLI from oracle-state "
            "`sensitive_values`",
            text,
        )


if __name__ == "__main__":
    unittest.main()
