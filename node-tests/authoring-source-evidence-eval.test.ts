import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
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

const AUTHORITY_PATH = path.join(
  process.cwd(),
  "node-tests",
  "fixtures",
  "python-source-evidence-eval-v1.json",
);
const AUTHORITY_SHA256 = "dbb55a83ba411d2394ef159d7a940ab772d626306b36032f0cb6dde232a0827c";
const CLI = path.join(process.cwd(), ".node-test", "node-src", "cli", "main.js");

interface FrozenEvaluationCase {
  readonly evaluation: JsonObject;
  readonly markdown: string;
}

interface FrozenCliCase {
  readonly artifacts: Readonly<Record<string, string>>;
  readonly stdout_without_artifacts: Readonly<Record<string, unknown>>;
}

interface FrozenAuthority {
  readonly cases: Readonly<Record<string, unknown>>;
  readonly provenance: JsonObject;
  readonly schema_version: number;
}

let authorityPromise: Promise<FrozenAuthority> | undefined;

async function frozenAuthority(): Promise<FrozenAuthority> {
  authorityPromise ??= (async () => {
    const bytes = await readFile(AUTHORITY_PATH);
    assert.equal(createHash("sha256").update(bytes).digest("hex"), AUTHORITY_SHA256);
    const authority = JSON.parse(bytes.toString("utf8")) as FrozenAuthority;
    assert.equal(authority.schema_version, 1);
    assert.deepEqual(authority.provenance, {
      baseline_commit: "501bd09384aa2e825342083141abc11789ed9bb1",
      normalization: {
        placeholder: "<FIXTURE_ROOT>",
        rule: "replace the exact absolute authoring CLI fixture root in artifact bytes and stdout string values; no other normalization",
      },
      python: "3.13.13",
      python_implementation: "cpython",
      source_blobs_sha256: {
        "engine/openapi_resource_map.py": "6026a4d25eaa4a2d5d669c32a8d9dbdd7de29f1bf1f8ad9b25c6ed5ded513770",
        "engine/reconcile_schema_api.py": "23deac644d9688df034cbd7f19d8bfcbcea15c3eb7a5109a89debc576037b7ea",
        "engine/sdk_path_evidence.py": "b2bd536010df6cfab10bfe1001a0d9990797ea6505387c7a1f02890cb3df0406",
        "engine/source_evidence_eval.py": "9e0e17686c5f6ec4cd3da4d67a9160ec3b45c2782926439c708d545a7606dfab",
        "engine/source_operation_map.py": "343e756d19c0ed32e51c33cb7885fb103f4bd98f43b54748dc11a8febe4426c4",
        "engine/tfschema.py": "12057bb1ec2922659afeaf1d4220283d66d67309ca047199bd7babeb32d05117",
        "node-src/authoring/openapi-resource-map.ts": "d3c338ac8efb34a55186681eb65a9adea31a4798cd67b6571feec7ec4d71a3f5",
        "node-src/authoring/openapi.ts": "fc50de84ef7fa7762c3961c3ca81c2ad953cd1558bf8661215ab5e359db237d4",
        "node-src/authoring/provider-source-evidence.ts": "eb399182907201dabf016df4a6a030b207519f6b73e1d36535a0c5f176b5bdb0",
        "node-src/authoring/reconcile-schema-api.ts": "d0a5f0fbadab3a9d3e40088c7ae9ec6200d927ee415f0d238aaf894dd405977c",
        "node-src/authoring/sdk-path-evidence.ts": "e90aaaa3547541fe99dfbca6c178be0e78a97423e7be8f45eef9369164ac1306",
        "node-src/authoring/source-evidence-eval.ts": "4909491eb43146672ebe2456262128869d7536913d3b94d5fe88a38b33152726",
        "node-src/authoring/source-operation-map.ts": "571c3d3cf2413c185be2ac46eca05fe9f33b528aa439182ad972165303e0f6a9",
        "node-src/json/control.ts": "420582b852b3baa22d6bcc6220fc7ffaf620099f432af1681a67689f33d604c1",
        "node-src/json/python-compatible.ts": "54505a9d508f103fd40af7897508edf86dc0c8bd0028e98d178c1fb9e79749e07",
        "node-src/metadata/terraform-schema.ts": "bee44a3c9ff079acdb39c3e2c3dc636d86cbfe3b92ff51ecd5a75c62a71a1fec",
        "node-src/metadata/validation.ts": "7022a90888e263735eba798bc9ee73b666d7d484f85b61dbaa843c705d174842",
        "node-tests/authoring-cli.test.ts": "99b573e7de95af5872fc1d8118a092cff5befc1160ea848cc4dcd495f3997f3c",
        "node-tests/authoring-source-evidence-eval.test.ts": "e856697074f0be50840d0b76e9eb67ce0819c1dc7fc827c04c820af1a523a488",
      },
      unicode_database: "15.1.0",
    });
    return authority;
  })();
  return authorityPromise;
}

async function frozenEvaluation(name: string): Promise<FrozenEvaluationCase> {
  const authority = await frozenAuthority();
  const value = authority.cases[name];
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  const evaluationCase = value as Readonly<Record<string, unknown>>;
  assert.ok(evaluationCase.evaluation !== null && typeof evaluationCase.evaluation === "object");
  assert.equal(typeof evaluationCase.markdown, "string");
  return evaluationCase as unknown as FrozenEvaluationCase;
}

async function frozenCliCase(): Promise<FrozenCliCase> {
  const authority = await frozenAuthority();
  const value = authority.cases.authoring_cli_artifact_set;
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  return value as FrozenCliCase;
}

async function writeJson(filename: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(filename), { recursive: true });
  await writeFile(filename, `${JSON.stringify(value)}\n`, "utf8");
}

async function cliFixture(): Promise<{
  readonly facts: string;
  readonly openApi: string;
  readonly root: string;
  readonly schema: string;
  readonly source: string;
}> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-authoring-cli-"));
  const source = path.join(root, "source");
  const schema = path.join(root, "schema.json");
  const openApi = path.join(root, "openapi.json");
  const facts = path.join(root, "facts.json");
  await mkdir(source, { recursive: true });
  await writeFile(
    path.join(source, "resource_widget.go"),
    "package provider\nfunc resourceWidgetRead() { client.Widgets.Get(ctx, id) }\n",
    "utf8",
  );
  await writeJson(schema, {
    resource_schemas: {
      example_widget: {
        block: {
          attributes: { name: { required: true, type: "string" } },
          block_types: {
            settings: {
              block: { attributes: { mode: { optional: true, type: "string" } } },
              max_items: 1,
              nesting_mode: "list",
            },
          },
        },
      },
    },
  });
  await writeJson(openApi, {
    info: { title: "authoring CLI fixture", version: "1" },
    openapi: "3.0.3",
    paths: {
      "/widgets": {
        get: { operationId: "ListWidgets", responses: { 200: { description: "ok" } } },
        post: { operationId: "CreateWidget", responses: { 200: { description: "ok" } } },
      },
      "/widgets/{id}": {
        get: { operationId: "GetWidget", responses: { 200: { description: "ok" } } },
      },
    },
  });
  await writeJson(facts, {
    files: [{ imports: [], package: "provider", path: "resource_widget.go" }],
    functions: [],
    identifier_references: [],
    package_calls: [],
    raw_rest_calls: [],
    read_callbacks: [],
    resource_references: [],
    resource_registrations: [],
    selector_calls: [{
      file: "resource_widget.go",
      parts: ["client", "Widgets", "Get"],
      symbol: "client.Widgets.Get",
    }],
    source_root: source,
  });
  return { facts, openApi, root, schema, source };
}

function runCli(arguments_: readonly string[]) {
  return spawnSync(process.execPath, [CLI, ...arguments_], {
    cwd: process.cwd(),
    encoding: "utf8",
    env: process.env,
  });
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
  const evaluation = evaluateSourceEvidence({ registry: {} }, candidate, compare); const expected = await frozenEvaluation("classifications_shortcomings_markdown");
  assert.deepEqual(evaluation, expected.evaluation); assert.equal(renderSourceEvidenceMarkdown(evaluation), expected.markdown);
  assert.deepEqual(classifySourceEvidenceChange(changes[3] as JsonObject), { classification: "acceptable", reason: "source_files_narrowed" });
});

test("Markdown prioritizes regressions, caps rows, and remains byte-exact", async () => {
  const many: JsonObject[] = Array.from({ length: MAX_MARKDOWN_CHANGE_ROWS + 5 }, (_, index) => ({ resource: `example_${String(index).padStart(3, "0")}`, before: { status: "mapped", read_path: "/old", files: ["old.go", "extra.go"] }, after: { status: "mapped", read_path: "/old", files: ["old.go"] } }));
  many.push({ resource: "example_regression", before: { status: "mapped", read_path: "/old", files: ["old.go"] }, after: { status: "unmapped", files: ["old.go"] } });
  const compare: JsonObject = { changes: many, summary: { resources: many.length, unchanged: 0, control: {}, candidate: {} } }; const empty: JsonObject = { registry: {}, diagnostics: [], summary: {} };
  const evaluation = evaluateSourceEvidence(empty, empty, compare); const expected = await frozenEvaluation("markdown_change_cap"); const markdown = renderSourceEvidenceMarkdown(evaluation);
  assert.deepEqual(evaluation, expected.evaluation);
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
  const evaluation = evaluateSourceEvidence({}, candidateReport, compare); const expected = await frozenEvaluation("explicit_null_metrics"); const markdown = renderSourceEvidenceMarkdown(evaluation);
  assert.deepEqual(evaluation, expected.evaluation); assert.equal(markdown, expected.markdown); assert.match(markdown, /\| `resources` \| `None` \| `None` \|/u);
  const bucket = (((evaluation.shortcomings as JsonObject).buckets as JsonObject).resource_file_not_found as JsonObject).resources as JsonObject[]; assert.equal(bucket[0]?.client_call_count, null);
});

test("source-evidence CLI emits every exact frozen artifact without Python", async (context) => {
  const data = await cliFixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const output = path.join(data.root, "python-eval");
  const result = runCli([
    "source-evidence-eval",
    "--schema", data.schema,
    "--openapi", data.openApi,
    "--source-root", data.source,
    "--resource-prefix", "example",
    "--source-facts", data.facts,
    "--out-dir", output,
  ]);
  assert.equal(result.status, 0, result.stderr);
  const expected = await frozenCliCase();
  assert.deepEqual(Object.keys(expected.artifacts).sort(), [
    "ast-report.json",
    "control-report.json",
    "source-evidence-eval.json",
    "source-evidence-eval.md",
    "source-facts-compare.json",
  ]);
  for (const [filename, expectedBytes] of Object.entries(expected.artifacts)) {
    assert.equal(
      (await readFile(path.join(output, filename), "utf8")).replaceAll(
        data.root,
        "<FIXTURE_ROOT>",
      ),
      expectedBytes,
      filename,
    );
  }
  const evaluation = JSON.parse(result.stdout) as Record<string, unknown>;
  delete evaluation.artifacts;
  assert.deepEqual(evaluation, expected.stdout_without_artifacts);
});

test("--fail-on-regression returns one after publishing diagnostic artifacts", async (context) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "source-eval-regression-"));
  context.after(async () => rm(root, { force: true, recursive: true }));
  const source = path.join(root, "provider");
  const schema = path.join(root, "schema.json");
  const openApi = path.join(root, "openapi.json");
  const facts = path.join(root, "facts.json");
  const output = path.join(root, "eval");
  await mkdir(source, { recursive: true });
  await writeFile(
    path.join(source, "resource_example_project.go"),
    "package provider\nfunc readProject() { client.ProjectsAPI.ProjectsRetrieve(ctx, id) }\n",
    "utf8",
  );
  await writeJson(schema, {
    provider_schemas: {
      "registry.terraform.io/example/example": {
        resource_schemas: {
          example_project: {
            block: { attributes: { name: { required: true, type: "string" } } },
          },
        },
      },
    },
  });
  await writeJson(openApi, {
    info: { title: "regression fixture", version: "1" },
    openapi: "3.0.3",
    paths: {
      "/projects/{id}": {
        get: {
          operationId: "ProjectsRetrieve",
          responses: { 200: { description: "ok" } },
        },
      },
    },
  });
  await writeJson(facts, {
    source_root: source,
    files: [{ path: "resource_example_project.go", package: "provider", imports: [] }],
    functions: [],
    resource_registrations: [],
    resource_references: [],
    identifier_references: [],
    read_callbacks: [],
    selector_calls: [],
    package_calls: [],
    raw_rest_calls: [],
  });
  const result = runCli([
    "source-evidence-eval",
    "--schema", schema,
    "--openapi", openApi,
    "--source-root", source,
    "--provider-source", "registry.terraform.io/example/example",
    "--resource-prefix", "example",
    "--source-facts", facts,
    "--out-dir", output,
    "--fail-on-regression",
  ]);
  assert.equal(result.status, 1, result.stderr);
  assert.equal(result.stderr, "");
  const evaluation = JSON.parse(
    await readFile(path.join(output, "source-evidence-eval.json"), "utf8"),
  ) as JsonObject;
  assert.equal((evaluation.summary as JsonObject).regressions, 1);
  assert.equal((evaluation.changes as JsonObject[])[0]?.classification_reason, "mapped_to_unmapped");
  const shortcomings = evaluation.shortcomings as JsonObject;
  assert.equal((shortcomings.summary as JsonObject).regression, 1);
  assert.equal((shortcomings.summary as JsonObject).source_files_without_operation_calls, 1);
  assert.equal((shortcomings.severity_summary as JsonObject).gap, 2);
  for (const filename of [
    "ast-report.json",
    "control-report.json",
    "source-evidence-eval.json",
    "source-evidence-eval.md",
    "source-facts-compare.json",
  ]) {
    assert.equal(typeof await readFile(path.join(output, filename), "utf8"), "string");
  }
});
