import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
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

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
const AUTHORITY_SHA256 = "0fc8279c122179047ac8895424d14ccc3922b30e840d48cfae6ec47d2fbdb767";
const RESURRECTION =
  "See docs/python-oracle-contracts.md for the exact clean-checkout resurrection command.";

interface FrozenDeriveCase {
  readonly input: {
    readonly openapi: JsonObject;
    readonly provider_source: string | null;
    readonly resource_filter: readonly string[] | null;
    readonly resource_prefix: string;
    readonly schema: JsonObject;
    readonly sdk_files: Readonly<Record<string, string>> | null;
    readonly sdk_root: string | null;
    readonly source_facts: JsonObject | null;
    readonly source_files: Readonly<Record<string, string>> | null;
    readonly source_root: string;
    readonly source_root_exists: boolean;
  };
  readonly name: string;
  readonly report: JsonObject;
}

interface FrozenCliCase {
  readonly artifacts: Readonly<Record<string, string>>;
  readonly exit_status: number;
  readonly name: string;
  readonly stderr: string;
  readonly stdout: string;
}

interface FrozenNamedReport {
  readonly name: string;
  readonly report: JsonObject;
}

interface FrozenAuthority {
  readonly cli_cases: readonly FrozenCliCase[];
  readonly derive_cases: readonly FrozenDeriveCase[];
  readonly helper_cases: Readonly<Record<string, unknown>>;
  readonly node_differential_cases: readonly FrozenNamedReport[];
  readonly provenance: JsonObject;
  readonly schema_version: number;
}

const authorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-source-operation-map-v1.json",
));
assert.equal(
  createHash("sha256").update(authorityBytes).digest("hex"),
  AUTHORITY_SHA256,
  "frozen CPython source-operation authority changed",
);
const authority = JSON.parse(authorityBytes.toString("utf8")) as FrozenAuthority;
assert.equal(authority.schema_version, 2);
assert.deepEqual(authority.provenance, {
  baseline_commit: "7d90752ac4b800c5509b380d02dc828749f891a6",
  normalization: {
    placeholder: "<FIXTURE_ROOT>",
    rule: "replace each retained unittest temporary root in complete report values and CLI artifact bytes; no other normalization",
  },
  python: "3.13.13",
  python_implementation: "cpython",
  source_blobs_sha256: {
    "engine/openapi_resource_map.py": "6026a4d25eaa4a2d5d669c32a8d9dbdd7de29f1bf1f8ad9b25c6ed5ded513770",
    "engine/reconcile_schema_api.py": "23deac644d9688df034cbd7f19d8bfcbcea15c3eb7a5109a89debc576037b7ea",
    "engine/sdk_path_evidence.py": "b2bd536010df6cfab10bfe1001a0d9990797ea6505387c7a1f02890cb3df0406",
    "engine/source_operation_map.py": "343e756d19c0ed32e51c33cb7885fb103f4bd98f43b54748dc11a8febe4426c4",
    "engine/tfschema.py": "12057bb1ec2922659afeaf1d4220283d66d67309ca047199bd7babeb32d05117",
    "node-src/authoring/cli.ts": "d3c5a27296880da3413fbf5d5213defd8de15c0dd40fb45b1c0808b1ff9fccd1",
    "node-src/authoring/openapi-resource-map.ts": "d3c338ac8efb34a55186681eb65a9adea31a4798cd67b6571feec7ec4d71a3f5",
    "node-src/authoring/openapi.ts": "fc50de84ef7fa7762c3961c3ca81c2ad953cd1558bf8661215ab5e359db237d4",
    "node-src/authoring/provider-source-evidence.ts": "eb399182907201dabf016df4a6a030b207519f6b73e1d36535a0c5f176b5bdb0",
    "node-src/authoring/reconcile-schema-api.ts": "d0a5f0fbadab3a9d3e40088c7ae9ec6200d927ee415f0d238aaf894dd405977c",
    "node-src/authoring/sdk-path-evidence.ts": "e90aaaa3547541fe99dfbca6c178be0e78a97423e7be8f45eef9369164ac1306",
    "node-src/authoring/source-operation-map.ts": "571c3d3cf2413c185be2ac46eca05fe9f33b528aa439182ad972165303e0f6a9",
    "node-src/json/python-compatible.ts": "54505a9d508f103fd40af7897508edf86d0c8bd0028e98d178c1fb9e79749e07",
    "node-src/metadata/terraform-schema.ts": "bee44a3c9ff079acdb39c3e2c3dc636d86cbfe3b92ff51ecd5a75c62a71a1fec",
    "node-src/metadata/validation.ts": "b8cbc7b930ac4ee8da7dae5a4625a13d1f4902f67c75127e0e222c983c3b5693",
    "node-tests/authoring-cli.test.ts": "0247a7f3710b1f94a57c60f97f9ea3ed929c4a30be379e364d7d62576ba980f3",
    "node-tests/authoring-sdk-path-evidence.test.ts": "2ac9c2512daa9a5d5d300028e2c3f7ac2c1da45858ae8efc67e2265e084aa0a6",
    "node-tests/authoring-source-operation-map.test.ts": "80461adad1b994fdba1f4f5907dd85d473bcbe23b18431aa11a0b3923ac389fd",
    "tests/test_sdk_path_evidence.py": "ef6d455b71be3958767df232b5b70004db92e587e663dc63176221a72995e9ad",
    "tests/test_source_operation_map.py": "673a0cb4e0b3eb711449e83c8a7b31a4f6e28174f247b49ad0547aa5e3c7ccc4",
  },
  generator_sha256: "4a3df279ba4f4b561373e57aebd13a297161ffb5f3cea0000896a46bc884a12a",
  resurrection: RESURRECTION,
  unicode_database: "15.1.0",
});

function normalizeAuthorityPaths(value: unknown, replacements: Readonly<Record<string, string>>): unknown {
  if (typeof value === "string") {
    let normalized = value;
    for (const [actual, replacement] of Object.entries(replacements)) {
      normalized = normalized.replaceAll(actual, replacement);
    }
    return normalized;
  }
  if (Array.isArray(value)) return value.map((item) => normalizeAuthorityPaths(item, replacements));
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [
      key,
      normalizeAuthorityPaths(item, replacements),
    ]));
  }
  return value;
}

async function writeTree(
  root: string,
  files: Readonly<Record<string, string>> | null,
  createRoot: boolean,
): Promise<void> {
  if (createRoot) await mkdir(root, { recursive: true });
  for (const [relative, contents] of Object.entries(files ?? {})) {
    const filename = path.join(root, relative);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, contents, "utf8");
  }
}

function materializeAuthorityPath(value: string, fixtureRoot: string): string {
  return value.replaceAll("<FIXTURE_ROOT>", fixtureRoot);
}

function assertNodeDifferential(
  name: string,
  actual: JsonObject,
  replacements: Readonly<Record<string, string>>,
): void {
  const matches = authority.node_differential_cases.filter((item) => item.name === name);
  assert.equal(matches.length, 1, `expected one frozen Node differential named ${name}`);
  assert.deepEqual(normalizeAuthorityPaths(actual, replacements), matches[0]!.report);
}

test("all retained Python source-operation reports remain exact", async (context) => {
  assert.equal(authority.derive_cases.length, 39);
  for (const frozen of authority.derive_cases) {
    await context.test(frozen.name, async (caseContext) => {
      const root = await mkdtemp(path.join(os.tmpdir(), "source-operation-frozen-"));
      caseContext.after(async () => rm(root, { force: true, recursive: true }));
      const sourceRoot = materializeAuthorityPath(frozen.input.source_root, root);
      const sdkRoot = frozen.input.sdk_root === null
        ? undefined
        : materializeAuthorityPath(frozen.input.sdk_root, root);
      await writeTree(sourceRoot, frozen.input.source_files, frozen.input.source_root_exists);
      if (sdkRoot !== undefined) await writeTree(sdkRoot, frozen.input.sdk_files, true);
      const sourceFacts = frozen.input.source_facts === null
        ? undefined
        : normalizeAuthorityPaths(frozen.input.source_facts, { "<FIXTURE_ROOT>": root }) as JsonObject;
      const report = await deriveSourceOperationRegistry({
        openApi: frozen.input.openapi,
        ...(frozen.input.provider_source === null ? {} : { providerSource: frozen.input.provider_source }),
        resourcePrefix: frozen.input.resource_prefix,
        ...(frozen.input.resource_filter === null ? {} : { resources: frozen.input.resource_filter }),
        schemaData: frozen.input.schema,
        ...(sdkRoot === undefined ? {} : { sdkRoot }),
        ...(sourceFacts === undefined ? {} : { sourceFacts }),
        sourceRoot,
      });
      assert.deepEqual(
        normalizeAuthorityPaths(report, { [root]: "<FIXTURE_ROOT>" }),
        frozen.report,
      );
    });
  }
});

test("source-operation CLI artifacts retain exact Python bytes", async (context) => {
  assert.equal(authority.cli_cases.length, 3);
  for (const frozen of authority.cli_cases.filter((item) => item.name !== "authoring_cli_stdout")) {
    await context.test(frozen.name, async (caseContext) => {
      const root = await mkdtemp(path.join(os.tmpdir(), "source-operation-cli-frozen-"));
      caseContext.after(async () => rm(root, { force: true, recursive: true }));
      const derive = authority.derive_cases.find((item) => item.name === `${frozen.name}#1`);
      assert.ok(derive, `missing replay input for ${frozen.name}`);
      const sourceRoot = materializeAuthorityPath(derive.input.source_root, root);
      await writeTree(sourceRoot, derive.input.source_files, derive.input.source_root_exists);
      const schema = path.join(root, "schema.json");
      const openapi = path.join(root, "openapi.json");
      await writeFile(schema, JSON.stringify(derive.input.schema), "utf8");
      await writeFile(openapi, JSON.stringify({
        info: { title: "frozen source-operation CLI authority", version: "1" },
        ...derive.input.openapi,
      }), "utf8");
      const arguments_ = [
        CLI,
        "source-operation-map",
        "--schema", schema,
        "--openapi", openapi,
        "--source-root", sourceRoot,
        "--resource-prefix", derive.input.resource_prefix,
      ];
      if (derive.input.provider_source !== null) arguments_.push("--provider-source", derive.input.provider_source);
      if (derive.input.source_facts !== null) {
        const facts = path.join(root, "facts.json");
        const value = normalizeAuthorityPaths(derive.input.source_facts, { "<FIXTURE_ROOT>": root });
        await writeFile(facts, JSON.stringify(value), "utf8");
        arguments_.push("--source-facts", facts);
      }
      const artifactPaths: Record<string, string> = {};
      for (const option of Object.keys(frozen.artifacts)) {
        const filename = path.join(root, `${option.slice(2)}.json`);
        artifactPaths[option] = filename;
        arguments_.push(option, filename);
      }
      const result = spawnSync(process.execPath, arguments_, {
        cwd: ROOT,
        encoding: "utf8",
        env: { ...process.env, PYTHON: path.join(root, "python-must-not-run") },
      });
      assert.equal(result.status, frozen.exit_status, result.stderr);
      assert.equal(
        normalizeAuthorityPaths(result.stdout, { [root]: "<FIXTURE_ROOT>" }),
        frozen.stdout,
        "stdout differs from frozen Python CLI bytes",
      );
      assert.equal(
        normalizeAuthorityPaths(result.stderr, { [root]: "<FIXTURE_ROOT>" }),
        frozen.stderr,
        "stderr differs from frozen Python CLI bytes",
      );
      for (const [option, expected] of Object.entries(frozen.artifacts)) {
        const bytes = await readFile(artifactPaths[option]!, "utf8");
        assert.equal(
          normalizeAuthorityPaths(bytes, { [root]: "<FIXTURE_ROOT>" }),
          expected,
          `${option} differs from frozen CPython bytes`,
        );
      }
    });
  }
});


async function fixture(files: Readonly<Record<string, string>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "source-map-node-"));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative); await mkdir(path.dirname(filename), { recursive: true }); await writeFile(filename, contents);
  }
  return root;
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

test("detail path-kind guards remain compatible with the Python authority", async (context) => {
  const cases = [
    { name: "playlist", operationId: "getPlaylist", path: "/playlists/{uid}" },
    { name: "product_search", operationId: "ai-search-fetch-instance", path: "/accounts/{account_id}/ai-search/instances/{id}" },
    { name: "product_list", operationId: "zero-trust-lists-zero-trust-list-details", path: "/accounts/{account_id}/gateway/lists/{list_id}" },
  ] as const;
  for (const item of cases) {
    await context.test(item.name, async (caseContext) => {
      const root = await fixture({
        "resource_widget.go": `package provider\nvar name = "example_widget"\nfunc read() { client.NewRequest("GET", "${item.path}", nil) }\n`,
      });
      caseContext.after(async () => rm(root, { force: true, recursive: true }));
      const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: {
        example_widget: { block: { attributes: {} } },
      } } } };
      const report = await deriveSourceOperationRegistry({
        openApi: { paths: { [item.path]: { get: { operationId: item.operationId } } } },
        providerSource: PROVIDER,
        resourcePrefix: "example",
        schemaData: schema,
        sourceRoot: root,
      });
      const entry = (report.registry as JsonObject).example_widget as JsonObject;
      assert.equal((entry.read as JsonObject).path_kind, authority.helper_cases[`path_kind_${item.name}`]);
    });
  }
});

test("AST selector tokens do not invent combined suffixes", async (context) => {
  const root = await fixture({ "resource_widget.go": "package provider\n" });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: {
    example_widget: { block: { attributes: {} } },
  } } } };
  const facts: JsonObject = {
    source_root: root,
    files: [{ imports: [], package: "provider", path: "resource_widget.go" }],
    functions: [], resource_registrations: [], resource_references: [], identifier_references: [],
    read_callbacks: [], package_calls: [], raw_rest_calls: [],
    selector_calls: [{
      file: "resource_widget.go",
      function: "Read",
      parts: ["r", "client", "IAM", "UserGroups", "Members", "List"],
      symbol: "r.client.IAM.UserGroups.Members.List",
    }],
  };
  const report = await deriveSourceOperationRegistry({
    openApi: { paths: { "/unrelated/{id}": { get: { operationId: "MembersList" } } } },
    providerSource: PROVIDER,
    resourcePrefix: "example",
    schemaData: schema,
    sourceFacts: facts,
    sourceRoot: root,
  });
  assert.equal(((report.registry as JsonObject).example_widget as JsonObject).status, "unmapped");
  assert.ok((authority.helper_cases.ast_identifier_tokens as readonly string[]).includes("rclientiamusergroupsmemberslist"));
  assert.equal((authority.helper_cases.ast_identifier_tokens as readonly string[]).includes("memberslist"), false);
});

test("text scanner produces an exact Python-compatible full report", async (context) => {
  const root = await fixture({ "internal/resource_folder.go": SOURCE }); context.after(async () => rm(root, { force: true, recursive: true }));
  const options = { openApi: OPENAPI, providerSource: PROVIDER, resourcePrefix: "example", schemaData: SCHEMA, sourceRoot: root } as const;
  const report = await deriveSourceOperationRegistry(options);
  assertNodeDifferential("text_scanner", report, { [root]: "<FIXTURE_ROOT>/text_scanner" });
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
  assertNodeDifferential("ast_facts", candidate, { [root]: "<FIXTURE_ROOT>/ast_facts" });
  const control = await deriveSourceOperationRegistry({ openApi: OPENAPI, providerSource: PROVIDER, resourcePrefix: "example", schemaData: SCHEMA, sourceRoot: root });
  const comparison = compareSourceOperationReports(control, candidate);
  assertNodeDifferential("ast_facts_comparison", comparison, { [root]: "<FIXTURE_ROOT>/ast_facts" });
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
  assertNodeDifferential("source_layout", report, { [root]: "<FIXTURE_ROOT>/source_layout" });
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
  assertNodeDifferential("sdk_text", textReport, {
    [root]: "<FIXTURE_ROOT>/sdk_text",
    [sdkRoot]: "<FIXTURE_ROOT>/sdk_text-sdk",
  });
  const facts: JsonObject = { source_root: root, files: [{ path: "resource_widget.go", imports: [], package: "provider" }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [], selector_calls: [
    { file: "resource_widget.go", parts: ["client", "Widgets", "Get"], symbol: "client.Widgets.Get" },
    { file: "resource_widget.go", parts: ["client", "Widgets", "Create"], symbol: "client.Widgets.Create" },
  ] };
  const factsReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sdkRoot, sourceFacts: facts, sourceRoot: root });
  assertNodeDifferential("sdk_facts", factsReport, {
    [root]: "<FIXTURE_ROOT>/sdk_facts",
    [sdkRoot]: "<FIXTURE_ROOT>/sdk_facts-sdk",
  });
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
  assertNodeDifferential("ambiguity_relationship", report, { [root]: "<FIXTURE_ROOT>/ambiguity_relationship" });
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
  assertNodeDifferential("escaped_rest_text", textReport, { [root]: "<FIXTURE_ROOT>/escaped_rest_text" });
  assert.equal(((((textReport.registry as JsonObject).example_widget as JsonObject).read as JsonObject).hops as JsonObject[])[0]?.raw_rest_path, "/widgets/{arg}");
  const facts: JsonObject = { source_root: root, files: [{ path: "resource_widget.go", imports: [], package: "provider" }], functions: [], resource_registrations: [], resource_references: [], identifier_references: [], read_callbacks: [], package_calls: [], raw_rest_calls: [], selector_calls: [
    { file: "resource_widget.go", parts: [], symbol: "client.Widgets.Get" },
  ] };
  const factsReport = await deriveSourceOperationRegistry({ openApi: openapi, providerSource: PROVIDER, resourcePrefix: "example", schemaData: schema, sourceFacts: facts, sourceRoot: root });
  assertNodeDifferential("escaped_rest_facts", factsReport, { [root]: "<FIXTURE_ROOT>/escaped_rest_facts" });
  assert.equal(((factsReport.registry as JsonObject).example_widget as JsonObject).status, "mapped");
});

test("AST REST facts with unresolved paths do not invent root-path evidence", async (context) => {
  const root = await fixture({ "resource_widget.go": `package provider
func read() { client.NewRequest("GET", dynamicPath, nil) }
` });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const schema: JsonObject = { provider_schemas: { [PROVIDER]: { resource_schemas: {
    example_widget: { block: { attributes: {} } },
  } } } };
  const openapi: JsonObject = { paths: {
    "/widgets/{id}": { get: { operationId: "GetWidget" } },
  } };
  const facts: JsonObject = {
    source_root: root,
    files: [{ path: "resource_widget.go", imports: [], package: "provider" }],
    functions: [],
    resource_registrations: [],
    resource_references: [],
    identifier_references: [],
    read_callbacks: [],
    package_calls: [],
    raw_rest_calls: [{
      file: "resource_widget.go",
      function: "read",
      method: "GET",
      symbol: "client.NewRequest",
    }],
    selector_calls: [],
  };
  const report = await deriveSourceOperationRegistry({
    openApi: openapi,
    providerSource: PROVIDER,
    resourcePrefix: "example",
    schemaData: schema,
    sourceFacts: facts,
    sourceRoot: root,
  });
  assertNodeDifferential("unresolved_rest_facts", report, { [root]: "<FIXTURE_ROOT>/unresolved_rest_facts" });
  const source = ((report.registry as JsonObject).example_widget as JsonObject).source as JsonObject;
  assert.equal(Object.hasOwn(source, "raw_rest_call_count"), false);
  assert.equal(Object.hasOwn(source, "raw_rest_calls"), false);
});
