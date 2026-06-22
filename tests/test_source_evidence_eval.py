import json
import os
import shutil
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO

from engine import source_evidence_eval


class SourceEvidenceEvalTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="source-evidence-eval-")

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

    def _schema(self):
        return {
            "provider_schemas": {
                "registry.terraform.io/example/example": {
                    "resource_schemas": {
                        "example_project": {
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
        }

    def _openapi(self):
        return {
            "openapi": "3.0.3",
            "paths": {
                "/projects/{id}": {
                    "get": {
                        "operationId": "ProjectsRetrieve",
                        "responses": {"200": {"description": "ok"}},
                    },
                },
            },
        }

    def _facts(self, selector_calls):
        return {
            "source_root": os.path.join(self.tmp, "provider"),
            "files": [
                {
                    "path": "resource_example_project.go",
                    "package": "provider",
                    "imports": [],
                },
            ],
            "functions": [],
            "resource_registrations": [],
            "read_callbacks": [],
            "selector_calls": selector_calls,
            "package_calls": [],
            "raw_rest_calls": [],
        }

    def test_run_eval_writes_artifacts_and_classifies_new_mapping(self):
        schema_path = self._write_json("schema.json", self._schema())
        openapi_path = self._write_json("openapi.json", self._openapi())
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_example_project.go", "package provider\n")
        facts_path = self._write_json("facts.json", self._facts([
            {
                "file": "resource_example_project.go",
                "function": "read",
                "symbol": "client.ProjectsAPI.ProjectsRetrieve",
                "parts": ["client", "ProjectsAPI", "ProjectsRetrieve"],
            },
        ]))
        out_dir = os.path.join(self.tmp, "eval")

        evaluation = source_evidence_eval.run_eval(
            schema_path,
            openapi_path,
            source_root,
            out_dir,
            provider_source="registry.terraform.io/example/example",
            resource_prefix="example",
            source_facts_path=facts_path,
        )

        self.assertEqual(evaluation["summary"]["regressions"], 0)
        self.assertEqual(evaluation["summary"]["review_required"], 1)
        self.assertEqual(
            evaluation["changes"][0]["classification_reason"],
            "new_mapping")
        self.assertTrue(os.path.isfile(os.path.join(out_dir, "control-report.json")))
        self.assertTrue(os.path.isfile(os.path.join(out_dir, "ast-report.json")))
        self.assertTrue(os.path.isfile(os.path.join(out_dir, "source-facts-compare.json")))
        self.assertTrue(os.path.isfile(os.path.join(out_dir, "source-evidence-eval.json")))
        with open(os.path.join(out_dir, "source-evidence-eval.md"), encoding="utf-8") as f:
            markdown = f.read()
        self.assertIn("Source Evidence A/B Evaluation", markdown)
        self.assertIn("new_mapping", markdown)

    def test_cli_fail_on_regression_returns_nonzero(self):
        schema_path = self._write_json("schema.json", self._schema())
        openapi_path = self._write_json("openapi.json", self._openapi())
        source_root = os.path.join(self.tmp, "provider")
        self._write("provider/resource_example_project.go", """
package provider

func readProject() {
    client.ProjectsAPI.ProjectsRetrieve(ctx, id)
}
""")
        facts_path = self._write_json("facts.json", self._facts([]))
        out_dir = os.path.join(self.tmp, "eval")

        with redirect_stdout(StringIO()):
            rc = source_evidence_eval.main([
                "--schema", schema_path,
                "--openapi", openapi_path,
                "--source-root", source_root,
                "--provider-source", "registry.terraform.io/example/example",
                "--resource-prefix", "example",
                "--source-facts", facts_path,
                "--out-dir", out_dir,
                "--fail-on-regression",
            ])

        self.assertEqual(rc, 1)
        with open(os.path.join(out_dir, "source-evidence-eval.json"), encoding="utf-8") as f:
            evaluation = json.load(f)
        self.assertEqual(evaluation["summary"]["regressions"], 1)
        self.assertEqual(
            evaluation["changes"][0]["classification_reason"],
            "mapped_to_unmapped")

    def test_classifies_file_narrowing_as_acceptable(self):
        change = {
            "before": {
                "status": "mapped",
                "read_path": "/projects/{id}",
                "files": ["resource.go", "extra.go"],
            },
            "after": {
                "status": "mapped",
                "read_path": "/projects/{id}",
                "files": ["resource.go"],
            },
        }

        verdict = source_evidence_eval.classify_change(change)

        self.assertEqual(verdict["classification"], "acceptable")
        self.assertEqual(verdict["reason"], "source_files_narrowed")


if __name__ == "__main__":
    unittest.main()
