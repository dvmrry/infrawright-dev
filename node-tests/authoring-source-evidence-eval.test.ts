import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  classifySourceEvidenceChange,
  evaluateSourceEvidence,
  MAX_MARKDOWN_CHANGE_ROWS,
  renderSourceEvidenceMarkdown,
} from "../node-src/authoring/source-evidence-eval.js";
import { compareSourceOperationReports, deriveSourceOperationRegistry } from "../node-src/authoring/source-operation-map.js";
import type { JsonObject } from "../node-src/metadata/validation.js";
import { PYTHON_ORACLE } from "./python-oracle.js";

async function pythonEvaluation(compare: JsonObject, candidate: JsonObject): Promise<JsonObject> {
  const root = await mkdtemp(path.join(os.tmpdir(), "source-eval-python-")); const input = path.join(root, "input.json");
  await writeFile(input, JSON.stringify({ candidate, compare }));
  const script = ["import json,sys", "from engine import source_evidence_eval as e", "x=json.load(open(sys.argv[1]))", "v=e.classify_comparison(x['compare'])", "v['shortcomings']=e.summarize_shortcomings(x['candidate'],v)", "json.dump({'evaluation':v,'markdown':e.render_markdown(v)},sys.stdout,sort_keys=True,separators=(',',':'))"].join(";");
  const result = spawnSync(PYTHON_ORACLE, ["-c", script, input], { cwd: process.cwd(), encoding: "utf8" }); await rm(root, { force: true, recursive: true }); assert.equal(result.status, 0, result.stderr); return JSON.parse(result.stdout) as JsonObject;
}

const changes: JsonObject[] = [
  { resource: "mapped_unmapped", before: { status: "mapped", read_path: "/a", files: ["a.go"] }, after: { status: "unmapped", files: ["a.go"] } },
  { resource: "mapped_path", before: { status: "mapped", read_path: "/a", files: ["a.go"] }, after: { status: "mapped", read_path: "/b", files: ["a.go"] } },
  { resource: "files_zero", before: { status: "unmapped", files: ["a.go"] }, after: { status: "unmapped", files: [] } },
  { resource: "files_narrow", before: { status: "mapped", read_path: "/a", files: ["a.go", "b.go"] }, after: { status: "mapped", read_path: "/a", files: ["a.go"] } },
  { resource: "files_changed", before: { status: "mapped", read_path: "/a", files: ["a.go"] }, after: { status: "mapped", read_path: "/a", files: ["b.go"] } },
  { resource: "new_mapping", before: { status: "unmapped", files: ["a.go"] }, after: { status: "mapped", read_path: "/a", files: ["a.go"] } },
  { resource: "ambiguous", before: { status: "mapped", read_path: "/a", files: ["a.go"] }, after: { status: "ambiguous_source_operation", read_path: "/a", files: ["a.go"] } },
  { resource: "list", before: { status: "mapped", read_path: "/a", list_path: "/list-a", files: [] }, after: { status: "mapped", read_path: "/a", list_path: "/list-b", files: [] } },
  { resource: "read", before: { status: "unmapped", read_path: "/a", files: [] }, after: { status: "unmapped", read_path: "/b", files: [] } },
  { resource: "status", before: { status: "graphql_source", files: [] }, after: { status: "unmapped", files: [] } },
  { resource: "diagnostic", before: { status: "unmapped", candidate_count: 0, files: [] }, after: { status: "unmapped", candidate_count: 1, files: [] } },
];

const candidate: JsonObject = { diagnostics: [{ resource: "no_reason", hits: [{ client_symbol: "Widgets.Get", operation_id: "GetWidget", method: "GET", path: "/widgets/{id}", read_score: 50 }] }], registry: {
  ambiguous: { status: "ambiguous_source_operation", reason: "ambiguous_source_operation", source: { files: ["ambiguous.go"], candidate_count: 2 }, candidates: [{ client_symbol: "One.Get", method: "GET", operation_id: "GetOne", path: "/one/{id}", path_kind: "detail", source_role: "read", read_score: 50, list_score: 10 }] },
  graphql: { status: "graphql_source", reason: "graphql_source", source: { files: ["graphql.go"] } },
  missing: { status: "unmapped", reason: "resource_file_not_found", source: {} },
  no_match: { status: "unmapped", reason: "no_source_operation_match", source: { files: ["calls.go"], client_call_count: 1, client_calls: ["Widgets.Get"] } },
  no_calls: { status: "unmapped", reason: "no_source_operation_match", source: { files: ["empty.go"] } },
  no_reason: { status: "unmapped", reason: null, source: { files: ["unknown.go"], candidate_count: 1 } },
  read_only: { status: "mapped", reason: null, source: { files: ["read.go"] }, read: { path: "/read/{id}", operation_id: "GetRead" } },
}, summary: { resources: 7, mapped: 1, ambiguous: 1, graphql_source: 1, unmapped: 4, resources_with_source_files: 6 } };

test("all classifications, shortcomings, and Markdown are exact Python-compatible", async () => {
  const compare: JsonObject = { changes, summary: { resources: changes.length, unchanged: 2, control: { resources: 11, mapped: 5 }, candidate: candidate.summary } };
  const evaluation = evaluateSourceEvidence({ registry: {} }, candidate, compare); const expected = await pythonEvaluation(compare, candidate);
  assert.deepEqual(evaluation, expected.evaluation); assert.equal(renderSourceEvidenceMarkdown(evaluation), expected.markdown);
  assert.deepEqual(classifySourceEvidenceChange(changes[3] as JsonObject), { classification: "acceptable", reason: "source_files_narrowed" });
});

test("Markdown prioritizes regressions, caps rows, and remains byte-exact", async () => {
  const many: JsonObject[] = Array.from({ length: MAX_MARKDOWN_CHANGE_ROWS + 5 }, (_, index) => ({ resource: `example_${String(index).padStart(3, "0")}`, before: { status: "mapped", read_path: "/old", files: ["old.go", "extra.go"] }, after: { status: "mapped", read_path: "/old", files: ["old.go"] } }));
  many.push({ resource: "example_regression", before: { status: "mapped", read_path: "/old", files: ["old.go"] }, after: { status: "unmapped", files: ["old.go"] } });
  const compare: JsonObject = { changes: many, summary: { resources: many.length, unchanged: 0, control: {}, candidate: {} } }; const empty: JsonObject = { registry: {}, diagnostics: [], summary: {} };
  const evaluation = evaluateSourceEvidence(empty, empty, compare); const expected = await pythonEvaluation(compare, empty); const markdown = renderSourceEvidenceMarkdown(evaluation);
  assert.equal(markdown, expected.markdown); assert.match(markdown, /Showing `100` of `106` changes/u); assert.match(markdown, /example_regression/u); assert.doesNotMatch(markdown, /example_104/u); assert.ok(markdown.endsWith("\n"));
});

test("in-memory coordinator evaluates Slice-2 text and AST reports", async (context) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "source-eval-node-")); context.after(async () => rm(root, { force: true, recursive: true })); await writeFile(path.join(root, "resource_project.go"), "package provider\n");
  const provider = "registry.terraform.io/example/example"; const schema: JsonObject = { provider_schemas: { [provider]: { resource_schemas: { example_project: { block: { attributes: {} } } } } } }; const openapi: JsonObject = { paths: { "/projects/{id}": { get: { operationId: "ProjectsRetrieve" } } } };
  const facts: JsonObject = { source_root: root, files: [{ path: "resource_project.go", package: "provider", imports: [] }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [], selector_calls: [{ file: "resource_project.go", symbol: "client.ProjectsAPI.ProjectsRetrieve", parts: ["client", "ProjectsAPI", "ProjectsRetrieve"] }] };
  const control = await deriveSourceOperationRegistry({ schemaData: schema, openApi: openapi, sourceRoot: root, providerSource: provider, resourcePrefix: "example" }); const ast = await deriveSourceOperationRegistry({ schemaData: schema, openApi: openapi, sourceRoot: root, providerSource: provider, resourcePrefix: "example", sourceFacts: facts });
  const evaluation = evaluateSourceEvidence(control, ast, compareSourceOperationReports(control, ast)); assert.equal((evaluation.summary as JsonObject).review_required, 1); assert.match(renderSourceEvidenceMarkdown(evaluation), /new_mapping/u);
});

test("explicit null metrics remain None instead of becoming measured zeroes", async () => {
  const compare: JsonObject = { changes: [], summary: { resources: null, unchanged: null, control: { resources: null, mapped: null }, candidate: { resources: null, mapped: null } } };
  const candidateReport: JsonObject = { diagnostics: [], registry: { missing: { status: "unmapped", reason: "resource_file_not_found", source: { candidate_count: null, client_call_count: null, package_call_count: null, raw_rest_call_count: null } } }, summary: { resources: null, mapped: null } };
  const evaluation = evaluateSourceEvidence({}, candidateReport, compare); const expected = await pythonEvaluation(compare, candidateReport); const markdown = renderSourceEvidenceMarkdown(evaluation);
  assert.deepEqual(evaluation, expected.evaluation); assert.equal(markdown, expected.markdown); assert.match(markdown, /\| `resources` \| `None` \| `None` \|/u);
  const bucket = (((evaluation.shortcomings as JsonObject).buckets as JsonObject).resource_file_not_found as JsonObject).resources as JsonObject[]; assert.equal(bucket[0]?.client_call_count, null);
});
