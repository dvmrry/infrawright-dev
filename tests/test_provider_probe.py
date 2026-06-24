import json
import os
import shutil
import tempfile
import unittest

from engine import provider_probe


class ProviderProbeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="provider-probe-")

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _write(self, name, text):
        path = os.path.join(self.tmp, name)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def _write_json(self, name, data):
        return self._write(name, json.dumps(data))

    def _write_fixture_recipe(self):
        self._write_json("schema.json", {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_folder": {
                            "block": {
                                "attributes": {
                                    "name": {
                                        "type": "string",
                                        "required": True,
                                    },
                                },
                            },
                        },
                    },
                },
            },
        })
        self._write_json("openapi.json", {
            "openapi": "3.0.3",
            "paths": {
                "/api/folders": {
                    "get": {
                        "operationId": "RouteGetFolders",
                        "responses": {"200": {"description": "ok"}},
                    },
                    "post": {
                        "responses": {"200": {"description": "ok"}},
                    },
                },
                "/api/folders/{uid}": {
                    "get": {
                        "operationId": "RouteGetFolder",
                        "responses": {"200": {"description": "ok"}},
                    },
                    "patch": {
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        })
        self._write("provider/internal/resource_folder.go", """
package internal

func resourceFolder() {
    resourceName := "example_folder"
    _ = resourceName
    client.Provisioning.GetFolders(ctx)
    client.Provisioning.GetFolder("abc")
}
""")
        return self._write_json("recipe.json", {
            "name": "example",
            "provider_source": "registry.terraform.io/example/example",
            "provider_version": "1.2.3",
            "resource_prefix": "example",
            "api_prefix": "/api/",
            "terraform_schema": {
                "path": "schema.json",
            },
            "source": {
                "path": "provider",
            },
            "openapi": {
                "path": "openapi.json",
                "format": "json",
            },
        })

    def test_probe_writes_repeatable_artifacts_from_local_recipe(self):
        recipe_path = self._write_fixture_recipe()
        work_dir = os.path.join(self.tmp, "work")

        result = provider_probe.run_probe(recipe_path, work_dir=work_dir)

        artifacts = result["artifacts"]
        self.assertTrue(os.path.exists(artifacts["source_registry"]))
        self.assertTrue(os.path.exists(artifacts["source_diagnostics"]))
        self.assertTrue(os.path.exists(artifacts["openapi_map"]))
        self.assertTrue(os.path.exists(artifacts["summary"]))
        self.assertTrue(os.path.exists(artifacts["markdown"]))
        summary = result["summary"]
        self.assertEqual(summary["source_evidence"]["mapped"], 1)
        self.assertEqual(summary["registry_read_coverage"]["read_resources"], 1)
        self.assertEqual(summary["registry_read_coverage"]["matched"], 1)
        self.assertEqual(summary["registry_read_coverage"]["coverage_ratio"], 1.0)
        self.assertEqual(summary["openapi_operation_profile"]["operations"], 4)
        self.assertEqual(summary["openapi_operation_profile"]["get_operations"], 2)
        with open(artifacts["source_registry"], encoding="utf-8") as f:
            registry = json.load(f)
        self.assertEqual(
            registry["example_folder"]["read"]["path"],
            "/api/folders/{uid}")
        with open(artifacts["markdown"], encoding="utf-8") as f:
            markdown = f.read()
        self.assertIn("# Provider Probe: example", markdown)
        self.assertIn("registry read coverage", markdown)

    def test_cli_can_copy_summary_outputs(self):
        recipe_path = self._write_fixture_recipe()
        out_path = os.path.join(self.tmp, "summary-copy.json")
        markdown_path = os.path.join(self.tmp, "summary-copy.md")

        rc = provider_probe.main([
            recipe_path,
            "--work-dir", os.path.join(self.tmp, "work-cli"),
            "--out", out_path,
            "--markdown", markdown_path,
        ])

        self.assertEqual(rc, 0)
        with open(out_path, encoding="utf-8") as f:
            summary = json.load(f)
        self.assertEqual(summary["provider"]["name"], "example")
        with open(markdown_path, encoding="utf-8") as f:
            markdown = f.read()
        self.assertIn("Provider Probe: example", markdown)

    def test_terraform_schema_hcl_uses_hcl_string_literals(self):
        hcl = provider_probe._terraform_schema_hcl(
            {"source": "example/example", "version": "1.2.3"},
            "registry.terraform.io/example/example",
            None,
        )

        self.assertIn('source = "example/example"', hcl)
        self.assertIn('version = "1.2.3"', hcl)
        self.assertNotIn("'example/example'", hcl)

    def test_committed_recipes_pin_remote_openapi_urls(self):
        repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        for name in ("digitalocean", "github"):
            with self.subTest(name=name):
                path = os.path.join(repo, "recipes", "providers", name + ".json")
                with open(path, encoding="utf-8") as f:
                    recipe = json.load(f)
                url = recipe["openapi"]["url"]
                self.assertNotIn("/main/", url)
                self.assertNotIn("/master/", url)
