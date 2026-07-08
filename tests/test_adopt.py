import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

from engine import adopt
from engine import packs
from engine import registry


def _write_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f)


class AdoptCommandTest(unittest.TestCase):
    def setUp(self):
        self.cwd = os.getcwd()
        self.tmp = tempfile.mkdtemp(prefix="adopt-command-")
        self.prev_packs = os.environ.get("INFRAWRIGHT_PACKS")
        self.prev_dep = os.environ.get("INFRAWRIGHT_DEPLOYMENT")
        os.chdir(self.tmp)
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = os.devnull
        os.environ["INFRAWRIGHT_PACKS"] = os.path.join(self.tmp, "packs")
        os.makedirs(os.path.join(self.tmp, "packs", "sample"), exist_ok=True)
        _write_json(os.path.join(self.tmp, "packs", "sample", "pack.json"), {
            "provider_prefixes": {"sample_": "sample"},
            "provider_sources": {"sample": "example/sample"},
        })
        _write_json(os.path.join(self.tmp, "packs", "sample", "registry.json"), {
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {"key_field": "name", "import_id": "{id}"},
            }
        })
        packs.reset()
        registry.reload_registry()
        self.prev_import_state = adopt.import_state
        self.prev_project_item = adopt.project_item

    def tearDown(self):
        adopt.import_state = self.prev_import_state
        adopt.project_item = self.prev_project_item
        os.chdir(self.cwd)
        if self.prev_packs is None:
            os.environ.pop("INFRAWRIGHT_PACKS", None)
        else:
            os.environ["INFRAWRIGHT_PACKS"] = self.prev_packs
        if self.prev_dep is None:
            os.environ.pop("INFRAWRIGHT_DEPLOYMENT", None)
        else:
            os.environ["INFRAWRIGHT_DEPLOYMENT"] = self.prev_dep
        packs.reset()
        registry.reload_registry()
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _assert_no_adopt_outputs(self):
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "config")))
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "imports")))
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "envs")))
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "lookup")))

    def test_adopt_outputs_provider_observed_state_not_raw_noise(self):
        input_path = os.path.join(self.tmp, "api.json")
        _write_json(input_path, [
            {"id": "123", "name": "Prod App", "apiOnlyNoise": "x"}
        ])

        def fake_import_state(resource_type, key_to_import_id):
            self.assertEqual(resource_type, "sample_resource")
            self.assertEqual(key_to_import_id, {"prod_app": "123"})
            return {
                "prod_app": {
                    "values": {
                        "name": "Prod App",
                        "description": "from provider",
                        "provider_only_default": True,
                    },
                    "sensitive_values": {},
                }
            }

        def fake_project_item(resource_type, state_values, sensitive_values=None, policy=None):
            return {
                "name": state_values["name"],
                "description": state_values["description"],
            }

        adopt.import_state = fake_import_state
        adopt.project_item = fake_project_item
        self.assertEqual(adopt.main(["sample_resource", input_path, "tenant"]), 0)

        with open(
                os.path.join(
                    "config", "tenant", "sample_resource.auto.tfvars.json"
                ),
                encoding="utf-8",
        ) as f:
            data = json.load(f)
        self.assertEqual(data["items"]["prod_app"]["description"], "from provider")
        self.assertNotIn("api_only_noise", data["items"]["prod_app"])
        self.assertNotIn("provider_only_default", data["items"]["prod_app"])

        with open(
                os.path.join("imports", "tenant", "sample_resource_imports.tf"),
                encoding="utf-8",
        ) as f:
            imports = f.read()
        self.assertIn('id = "123"', imports)
        self.assertIn('this["prod_app"]', imports)

    def test_empty_pull_writes_empty_outputs_without_oracle(self):
        input_path = os.path.join(self.tmp, "empty.json")
        _write_json(input_path, [])

        def fail_import_state(resource_type, key_to_import_id):
            raise AssertionError("empty adoption should not call import oracle")

        adopt.import_state = fail_import_state
        self.assertEqual(adopt.main(["sample_resource", input_path, "tenant"]), 0)
        with open(
                os.path.join(
                    "config", "tenant", "sample_resource.auto.tfvars.json"
                ),
                encoding="utf-8",
        ) as f:
            data = json.load(f)
        self.assertEqual(data, {"items": {}})
        with open(
                os.path.join("imports", "tenant", "sample_resource_imports.tf"),
                encoding="utf-8",
        ) as f:
            self.assertEqual(f.read(), "")

    def test_hcl_deployment_writes_hcl_config_and_removes_stale_json(self):
        from engine import hcl_tfvars

        dep = os.path.join(self.tmp, "deployment.json")
        _write_json(dep, {"tfvars_format": "hcl"})
        os.environ["INFRAWRIGHT_DEPLOYMENT"] = dep
        input_path = os.path.join(self.tmp, "api.json")
        _write_json(input_path, [
            {"id": "123", "name": "Prod App"}
        ])
        stale_json = os.path.join(
            "config", "tenant", "sample_resource.auto.tfvars.json")
        os.makedirs(os.path.dirname(stale_json), exist_ok=True)
        with open(stale_json, "w", encoding="utf-8") as f:
            f.write('{"items": {"stale": {}}}\n')

        def fake_import_state(resource_type, key_to_import_id):
            return {
                "prod_app": {
                    "values": {"name": "Prod App"},
                    "sensitive_values": {},
                }
            }

        def fake_project_item(resource_type, state_values,
                              sensitive_values=None, policy=None):
            return {"name": state_values["name"]}

        adopt.import_state = fake_import_state
        adopt.project_item = fake_project_item
        self.assertEqual(adopt.main(["sample_resource", input_path, "tenant"]), 0)

        hcl_path = os.path.join(
            "config", "tenant", "sample_resource.auto.tfvars")
        self.assertTrue(os.path.exists(hcl_path), hcl_path)
        self.assertFalse(os.path.exists(stale_json), stale_json)
        with open(hcl_path, encoding="utf-8") as f:
            self.assertTrue(f.read().startswith(hcl_tfvars.HEADER))

    def test_json_deployment_removes_stale_hcl_config(self):
        input_path = os.path.join(self.tmp, "api.json")
        _write_json(input_path, [
            {"id": "123", "name": "Prod App"}
        ])
        stale_hcl = os.path.join(
            "config", "tenant", "sample_resource.auto.tfvars")
        os.makedirs(os.path.dirname(stale_hcl), exist_ok=True)
        with open(stale_hcl, "w", encoding="utf-8") as f:
            f.write("items = {}\n")

        def fake_import_state(resource_type, key_to_import_id):
            return {
                "prod_app": {
                    "values": {"name": "Prod App"},
                    "sensitive_values": {},
                }
            }

        def fake_project_item(resource_type, state_values,
                              sensitive_values=None, policy=None):
            return {"name": state_values["name"]}

        adopt.import_state = fake_import_state
        adopt.project_item = fake_project_item
        self.assertEqual(adopt.main(["sample_resource", input_path, "tenant"]), 0)

        json_path = os.path.join(
            "config", "tenant", "sample_resource.auto.tfvars.json")
        self.assertTrue(os.path.exists(json_path), json_path)
        self.assertFalse(os.path.exists(stale_hcl), stale_hcl)

    def test_duplicate_alias_derived_import_ids_fail_before_oracle(self):
        _write_json(os.path.join(self.tmp, "packs", "sample", "registry.json"), {
            "sample_resource": {
                "generate": True,
                "product": "sample",
                "adopt": {
                    "key_field": "name",
                    "identity_fields": {"import_id": "uuid"},
                },
            }
        })
        registry.reload_registry()
        called = []

        def fail_import_state(resource_type, key_to_import_id):
            called.append((resource_type, key_to_import_id))
            raise AssertionError("duplicate import IDs should fail before oracle")

        adopt.import_state = fail_import_state
        with self.assertRaises(ValueError) as ctx:
            adopt.adopt_items([
                {"name": "One", "uuid": "SECRET-IMPORT-ID"},
                {"name": "Two", "uuid": "SECRET-IMPORT-ID"},
            ], "sample_resource")

        self.assertEqual(called, [])
        msg = str(ctx.exception)
        self.assertIn("sample_resource duplicate import_id", msg)
        self.assertIn("one", msg)
        self.assertIn("two", msg)
        self.assertNotIn("SECRET-IMPORT-ID", msg)

    def test_malformed_json_input_reports_clean_error(self):
        input_path = os.path.join(self.tmp, "bad.json")
        with open(input_path, "w", encoding="utf-8") as f:
            f.write('{"bad": ')
        stderr = io.StringIO()

        with contextlib.redirect_stderr(stderr):
            code = adopt.main(["sample_resource", input_path, "tenant"])

        self.assertEqual(code, 1)
        msg = stderr.getvalue()
        self.assertIn("error: failed to parse", msg)
        self.assertIn("bad.json", msg)
        self.assertIn("line", msg)

    def test_invalid_tenant_rejected_before_writing_outputs(self):
        input_path = os.path.join(self.tmp, "api.json")
        _write_json(input_path, [])

        for tenant in ("../../etc", "../x", "bad/tenant", "/absolute/path"):
            with self.subTest(tenant=tenant):
                with self.assertRaises(ValueError) as ctx:
                    adopt.main(["sample_resource", input_path, tenant])
                self.assertIn("TENANT must match", str(ctx.exception))
                self._assert_no_adopt_outputs()

    def test_invalid_resource_type_rejected_before_writing_outputs(self):
        input_path = os.path.join(self.tmp, "api.json")
        _write_json(input_path, [])

        with self.assertRaises(ValueError) as ctx:
            adopt.main(["../sample_resource", input_path, "tenant"])

        self.assertIn(
            "RESOURCE must be an exact generated resource type", str(ctx.exception)
        )
        self._assert_no_adopt_outputs()


if __name__ == "__main__":
    unittest.main()
