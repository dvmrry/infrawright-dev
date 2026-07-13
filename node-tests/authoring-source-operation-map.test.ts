import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  compareSourceOperationReports,
  deriveSourceOperationRegistry,
  openApiOperationInventory,
  operationAliases,
} from "../node-src/authoring/source-operation-map.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

async function fixture(files: Readonly<Record<string, string>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "source-map-node-"));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative); await mkdir(path.dirname(filename), { recursive: true }); await writeFile(filename, contents);
  }
  return root;
}

async function pythonReport(options: {
  root: string; schema: JsonObject; openapi: JsonObject; providerSource?: string; prefix?: string;
  resources?: readonly string[]; facts?: JsonObject; sdkRoot?: string;
}): Promise<JsonObject> {
  const schemaPath = path.join(path.dirname(options.root), `${path.basename(options.root)}-schema.json`);
  const openapiPath = path.join(path.dirname(options.root), `${path.basename(options.root)}-openapi.json`);
  await writeFile(schemaPath, JSON.stringify(options.schema)); await writeFile(openapiPath, JSON.stringify(options.openapi));
  const script = [
    "import json,sys", "from engine import source_operation_map as s",
    "o=json.load(open(sys.argv[3]))",
    "r=s.derive_registry(sys.argv[1],sys.argv[2],o['root'],provider_source=o.get('providerSource'),resource_prefix=o.get('prefix',''),source_facts=o.get('facts'),resource_filter=o.get('resources'),sdk_root=o.get('sdkRoot'))",
    "json.dump(r,sys.stdout,sort_keys=True,separators=(',',':'))",
  ].join(";");
  const payload = path.join(path.dirname(options.root), `${path.basename(options.root)}-options.json`);
  await writeFile(payload, JSON.stringify({ ...options, schema: undefined, openapi: undefined }));
  const result = spawnSync("python3", ["-c", script, schemaPath, openapiPath, payload], { cwd: process.cwd(), encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr); return JSON.parse(result.stdout) as JsonObject;
}

const PROVIDER = "registry.terraform.io/example/example";
const SCHEMA: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: {
  example_folder: { block: { attributes: { name: { required: true, type: "string" } } } },
} } } };
const OPENAPI: JsonObject = { openapi: "3.0.3", paths: {
  "/api/folders": { get: { operationId: "RouteGetFolders" } },
  "/api/folders/{uid}": { get: { operationId: "RouteGetFolder" } },
} };
const SOURCE = `package internal
func resourceFolder() {
  name := "example_folder"
  _ = name
  client.Provisioning.GetFolders(ctx)
  client.Provisioning.GetFolder("abc")
}
`;

test("operation inventory and aliases preserve explicit and synthetic operations", () => {
  const inventory = openApiOperationInventory({ paths: { "/widgets": { get: {}, post: { operationId: "CreateWidget" }, parameters: [] } } });
  assert.deepEqual(inventory, [
    { aliases: ["getwidgets", "getwidgetswithresponse", "retrievewidretrieves", "retrievewidretrieveswithresponse"], method: "GET", operation_id: "GET /widgets", operation_id_source: "synthetic_path", path: "/widgets" },
    { aliases: ["createwidget", "createwidgetwithresponse", "createwidretrieve", "createwidretrievewithresponse"], method: "POST", operation_id: "CreateWidget", operation_id_source: "openapi", path: "/widgets" },
  ]);
  assert.ok(operationAliases("RouteRetrieveWidget").includes("getwidget"));
  assert.ok(operationAliases("RouteRetrieveWidget").includes("routereadwidgetwithresponse"));
});

test("text scanner produces an exact Python-compatible full report", async (context) => {
  const root = await fixture({ "internal/resource_folder.go": SOURCE }); context.after(async () => rm(root, { force: true, recursive: true }));
  const options = { openApi: OPENAPI, providerSource: PROVIDER, resourcePrefix: "example", schemaData: SCHEMA, sourceRoot: root } as const;
  const report = await deriveSourceOperationRegistry(options);
  assert.deepEqual(report, await pythonReport({ root, schema: SCHEMA, openapi: OPENAPI, providerSource: PROVIDER, prefix: "example" }));
  assert.equal((report.registry as JsonObject).example_folder && ((report.registry as JsonObject).example_folder as JsonObject).status, "mapped");
});

test("AST facts produce exact registry, diagnostics, and comparison reports", async (context) => {
  const root = await fixture({ "resource_folder.go": "package provider\n" }); context.after(async () => rm(root, { force: true, recursive: true }));
  const facts: JsonObject = {
    source_root: root, files: [{ path: "resource_folder.go", package: "provider", imports: [] }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [],
    selector_calls: [
      { file: "resource_folder.go", function: "read", parts: ["client", "Provisioning", "GetFolders"], symbol: "client.Provisioning.GetFolders" },
      { file: "resource_folder.go", function: "read", parts: ["client", "Provisioning", "GetFolder"], symbol: "client.Provisioning.GetFolder" },
    ],
  };
  const candidate = await deriveSourceOperationRegistry({ openApi: OPENAPI, providerSource: PROVIDER, resourcePrefix: "example", schemaData: SCHEMA, sourceFacts: facts, sourceRoot: root });
  const expected = await pythonReport({ facts, root, schema: SCHEMA, openapi: OPENAPI, providerSource: PROVIDER, prefix: "example" });
  assert.deepEqual(candidate, expected);
  const control = await deriveSourceOperationRegistry({ openApi: OPENAPI, providerSource: PROVIDER, resourcePrefix: "example", schemaData: SCHEMA, sourceRoot: root });
  const comparison = compareSourceOperationReports(control, candidate);
  const pyScript = "import json,sys;from engine import source_operation_map as s;a=json.load(open(sys.argv[1]));b=json.load(open(sys.argv[2]));json.dump(s.compare_registry_reports(a,b),sys.stdout,sort_keys=True,separators=(',',':'))";
  const before = `${root}-before.json`; const after = `${root}-after.json`; await writeFile(before, JSON.stringify(control)); await writeFile(after, JSON.stringify(candidate));
  const result = spawnSync("python3", ["-c", pyScript, before, after], { cwd: process.cwd(), encoding: "utf8" }); assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(comparison, JSON.parse(result.stdout));
});

test("selected resources and malformed facts fail clearly", async (context) => {
  const root = await fixture({}); context.after(async () => rm(root, { force: true, recursive: true }));
  await assert.rejects(deriveSourceOperationRegistry({ openApi: OPENAPI, resources: ["missing"], schemaData: SCHEMA, sourceRoot: root }), /resources not found.*missing/u);
  await assert.rejects(deriveSourceOperationRegistry({ openApi: OPENAPI, schemaData: SCHEMA, sourceFacts: {}, sourceRoot: root }), /malformed source facts/u);
});

test("source layout discovery and non-selector evidence match Python exactly", async (context) => {
  const root = await fixture({
    "provider.go": `package provider
import project "example.com/provider/internal/project"
var resources = map[string]func(){"example_registered": resourceRegistered, "example_packaged": project.NewResource}
func resourceRegistered() { _ = &Resource{Read: readRegistered} }
`,
    "registered/read.go": `package registered
func readRegistered() { client.Registered.GetRegistered(ctx) }
`,
    "internal/project/resource.go": `package project
func NewResource() { client.Packaged.GetPackaged(ctx) }
`,
    "internal/project/data_source_skip.go": `package project
func ignored() { client.Wrong.GetWrong(ctx) }
`,
    "internal/services/service/resource.go": `package service
func read() { client.Service.GetService(ctx) }
`,
    "internal/framework/resources/framework.go": `package resources
import external "example.net/sdk"
func read() { external.GetFramework(ctx) }
`,
    "resource_raw.go": `package provider
import ("fmt"; "net/http")
func readRaw() { _, _ = client.NewRequest(http.MethodGet, fmt.Sprintf("/raw/%s", id), nil) }
`,
    "resource_graphql.go": `package provider
import "github.com/shurcooL/githubv4"
func readGraphql() { githubv4.NewRequest() }
`,
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const names = ["registered", "packaged", "service", "framework", "raw", "graphql"];
  const schemas = Object.fromEntries(names.map((name) => [`example_${name}`, { block: { attributes: {} } }]));
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: schemas } } };
  const openapi: JsonObject = { paths: Object.fromEntries(names.filter((name) => name !== "graphql").map((name) => [`/${name}/{id}`, { get: { operationId: `Get${name[0]?.toUpperCase()}${name.slice(1)}` } }])) };
  const report = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sourceRoot: root });
  assert.deepEqual(report, await pythonReport({ root, schema, openapi, providerSource: PROVIDER, prefix: "example" }));
  assert.equal(((report.registry as JsonObject).example_graphql as JsonObject).status, "graphql_source");
  assert.equal((((report.registry as JsonObject).example_raw as JsonObject).source as JsonObject).raw_rest_call_count, 1);
});

test("SDK path evidence wins fuzzy scoring and records actions for text and facts", async (context) => {
  const root = await fixture({ "resource_widget.go": `package provider
func read() { client.Widgets.Get(ctx, id); client.Widgets.Create(ctx) }
` });
  const sdkRoot = await fixture({ "widgets.go": `package sdk
const widgetsBasePath = "v2/widgets"
type WidgetsServiceOp struct { client *Client }
func (s *WidgetsServiceOp) Get(ctx context.Context, id string) error { path := fmt.Sprintf("%s/%s", widgetsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *WidgetsServiceOp) Create(ctx context.Context) error { path := widgetsBasePath; _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }
` });
  context.after(async () => { await rm(root, { force: true, recursive: true }); await rm(sdkRoot, { force: true, recursive: true }); });
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: { example_widget: { block: { attributes: {} } } } } } };
  const openapi: JsonObject = { paths: { "/v2/widgets/{widget_id}": { get: { operationId: "GetWidget" } }, "/v2/widgets": { post: { operationId: "CreateWidget" } } } };
  const textReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sdkRoot, sourceRoot: root });
  assert.deepEqual(textReport, await pythonReport({ root, schema, openapi, providerSource: PROVIDER, prefix: "example", sdkRoot }));
  const facts: JsonObject = { source_root: root, files: [{ path: "resource_widget.go", imports: [], package: "provider" }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [], selector_calls: [
    { file: "resource_widget.go", parts: ["client", "Widgets", "Get"], symbol: "client.Widgets.Get" },
    { file: "resource_widget.go", parts: ["client", "Widgets", "Create"], symbol: "client.Widgets.Create" },
  ] };
  const factsReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sdkRoot, sourceFacts: facts, sourceRoot: root });
  assert.deepEqual(factsReport, await pythonReport({ facts, root, schema, openapi, providerSource: PROVIDER, prefix: "example", sdkRoot }));
  const source = ((factsReport.registry as JsonObject).example_widget as JsonObject).source as JsonObject;
  assert.deepEqual((source.sdk_action_paths as JsonObject[]).map((item) => item.method), ["POST"]);
});

test("ambiguity and relationship-list read selection match Python", async (context) => {
  const root = await fixture({
    "resource_thing.go": `package provider
var name = "example_thing"
func read() { client.ThingsAPI.GetThing(ctx, id); client.ThingsAPI.RetrieveThing(ctx, uid) }
`,
    "resource_repository_topics.go": `package provider
var name = "example_repository_topics"
func read() { client.Repositories.ListAllTopics(ctx, owner, repo, nil) }
`,
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: {
    example_thing: { block: { attributes: {} } }, example_repository_topics: { block: { attributes: {} } },
  } } } };
  const openapi: JsonObject = { paths: {
    "/things/{id}": { get: { operationId: "GetThing" } }, "/things/{uid}": { get: { operationId: "RetrieveThing" } },
    "/repos/{owner}/{repo}/topics": { get: { operationId: "repos/get-all-topics" } },
  } };
  const report = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sourceRoot: root });
  assert.deepEqual(report, await pythonReport({ root, schema, openapi, providerSource: PROVIDER, prefix: "example" }));
  assert.equal(((report.registry as JsonObject).example_thing as JsonObject).status, "ambiguous_source_operation");
  assert.equal((((report.registry as JsonObject).example_repository_topics as JsonObject).read as JsonObject).evidence_kind, "relationship_list_read");
});

test("escaped raw REST paths and empty selector parts retain exact Python evidence", async (context) => {
  const root = await fixture({ "resource_widget.go": String.raw`package provider
func read() { client.NewRequest("GET", "\x2fwidgets\u002f%s", nil) }
` });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: { example_widget: { block: { attributes: {} } } } } } };
  const openapi: JsonObject = { paths: { "/widgets/{id}": { get: { operationId: "GetWidget" } } } };
  const textReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sourceRoot: root });
  assert.deepEqual(textReport, await pythonReport({ root, schema, openapi, providerSource: PROVIDER, prefix: "example" }));
  assert.equal(((((textReport.registry as JsonObject).example_widget as JsonObject).read as JsonObject).hops as JsonObject[])[0]?.raw_rest_path, "/widgets/{arg}");
  const facts: JsonObject = { source_root: root, files: [{ path: "resource_widget.go", imports: [], package: "provider" }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [], selector_calls: [
    { file: "resource_widget.go", parts: [], symbol: "client.Widgets.Get" },
  ] };
  const factsReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sourceFacts: facts, sourceRoot: root });
  assert.deepEqual(factsReport, await pythonReport({ facts, root, schema, openapi, providerSource: PROVIDER, prefix: "example" }));
  assert.equal(((factsReport.registry as JsonObject).example_widget as JsonObject).status, "mapped");
});
