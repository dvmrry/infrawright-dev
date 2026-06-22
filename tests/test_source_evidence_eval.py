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
            "resource_references": [],
            "identifier_references": [],
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
        self.assertIn("Shortcomings", markdown)
        self.assertIn("mapped_read_without_list", markdown)

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
        self.assertEqual(
            evaluation["shortcomings"]["summary"]["regression"],
            1)
        self.assertEqual(
            evaluation["shortcomings"]["summary"]["source_files_without_operation_calls"],
            1)
        self.assertEqual(
            evaluation["shortcomings"]["severity_summary"]["gap"],
            2)

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

    def test_summarizes_candidate_shortcomings_by_reason(self):
        ast_report = {
            "registry": {
                "example_ambiguous": {
                    "status": "ambiguous_source_operation",
                    "reason": "ambiguous_source_operation",
                    "source": {
                        "files": ["resource_ambiguous.go"],
                        "candidate_count": 2,
                    },
                },
                "example_graphql": {
                    "status": "graphql_source",
                    "reason": "graphql_source",
                    "source": {
                        "files": ["resource_graphql.go"],
                    },
                },
                "example_missing": {
                    "status": "unmapped",
                    "reason": "resource_file_not_found",
                    "source": {},
                },
                "example_no_match": {
                    "status": "unmapped",
                    "reason": "no_source_operation_match",
                    "source": {
                        "files": ["resource_no_match.go"],
                        "client_call_count": 1,
                        "client_calls": ["Projects.Get"],
                    },
                },
                "example_no_calls": {
                    "status": "unmapped",
                    "reason": "no_source_operation_match",
                    "source": {
                        "files": ["resource_no_calls.go"],
                    },
                },
                "example_read_only": {
                    "status": "mapped",
                    "source": {
                        "files": ["resource_read_only.go"],
                    },
                    "read": {
                        "path": "/read-only/{id}",
                        "operation_id": "GetReadOnly",
                    },
                },
            },
            "diagnostics": [],
        }
        evaluation = {
            "changes": [
                {
                    "resource": "example_missing",
                    "classification": "regression",
                    "classification_reason": "mapped_to_unmapped",
                    "before": {"status": "mapped"},
                    "after": {"status": "unmapped"},
                },
            ],
        }

        shortcomings = source_evidence_eval.summarize_shortcomings(
            ast_report, evaluation)

        self.assertEqual(shortcomings["summary"]["regression"], 1)
        self.assertEqual(
            shortcomings["summary"]["ambiguous_source_operation"], 1)
        self.assertEqual(shortcomings["summary"]["graphql_source"], 1)
        self.assertEqual(shortcomings["summary"]["resource_file_not_found"], 1)
        self.assertEqual(
            shortcomings["summary"]["calls_without_openapi_match"], 1)
        self.assertEqual(
            shortcomings["summary"]["source_files_without_operation_calls"], 1)
        self.assertEqual(shortcomings["summary"]["mapped_read_without_list"], 1)
        self.assertEqual(
            shortcomings["buckets"]["mapped_read_without_list"]["severity"],
            "notice")
        self.assertEqual(
            shortcomings["buckets"]["ambiguous_source_operation"]["severity"],
            "review")
        self.assertEqual(
            shortcomings["buckets"]["calls_without_openapi_match"]
            ["resources"][0]["client_calls"],
            ["Projects.Get"])
        self.assertEqual(shortcomings["severity_summary"], {
            "gap": 4,
            "notice": 2,
            "review": 1,
        })

    def test_markdown_caps_change_rows_and_prioritizes_regressions(self):
        changes = []
        for index in range(source_evidence_eval.MAX_MARKDOWN_CHANGE_ROWS + 5):
            changes.append({
                "resource": "example_%03d" % index,
                "classification": "acceptable",
                "classification_reason": "source_files_narrowed",
                "before": {"status": "mapped", "read_path": "/old"},
                "after": {"status": "mapped", "read_path": "/old"},
            })
        changes.append({
            "resource": "example_regression",
            "classification": "regression",
            "classification_reason": "mapped_to_unmapped",
            "before": {"status": "mapped", "read_path": "/old"},
            "after": {"status": "unmapped"},
        })
        markdown = source_evidence_eval.render_markdown({
            "summary": {
                "regressions": 1,
                "review_required": 0,
                "acceptable": len(changes) - 1,
                "unchanged": 0,
            },
            "changes": changes,
        })

        self.assertIn("example_regression", markdown)
        self.assertIn("Showing `100` of `106` changes", markdown)
        self.assertNotIn("example_104", markdown)


if __name__ == "__main__":
    unittest.main()
