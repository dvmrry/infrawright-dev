import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  discoverSdkGoFiles,
  extractBalancedGoBody,
  extractSdkPaths,
  goCodeWithoutComments,
  matchOpenApiBySdkPath,
  matchSdkEvidenceToOpenApi,
  normalizeSdkPathSegments,
  splitGoCallArguments,
} from "../node-src/authoring/sdk-path-evidence.js";
import { runAuthoringCommand } from "../node-src/authoring/cli.js";
import { deriveSourceOperationRegistry } from "../node-src/authoring/source-operation-map.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const AUTHORITY_PATH = path.join(
  process.cwd(),
  "node-tests",
  "fixtures",
  "python-sdk-path-evidence-v1.json",
);
const AUTHORITY_SHA256 = "5414a3eb08b07e41c4ba9680da79268aa742e8a8ded005d34c0321d5b1d6f477";
const RESURRECTION =
  "See docs/python-oracle-contracts.md for the exact clean-checkout resurrection command.";

interface FrozenAuthority {
  readonly cases: Readonly<Record<string, unknown>>;
  readonly provenance: JsonObject;
  readonly schema_version: number;
}

interface FrozenCliCase {
  readonly diagnostics_bytes: string;
  readonly exit_code: number;
  readonly registry_bytes: string;
  readonly report: JsonObject;
  readonly stderr: string;
  readonly stdout: string;
}

interface ExtractedReport {
  readonly evidence: Readonly<Record<string, JsonObject>>;
  readonly unresolved: Readonly<Record<string, JsonObject>>;
}

let authorityPromise: Promise<FrozenAuthority> | undefined;

async function frozenAuthority(): Promise<FrozenAuthority> {
  authorityPromise ??= (async () => {
    const bytes = await readFile(AUTHORITY_PATH);
    assert.equal(createHash("sha256").update(bytes).digest("hex"), AUTHORITY_SHA256);
    const authority = JSON.parse(bytes.toString("utf8")) as FrozenAuthority;
    assert.equal(authority.schema_version, 1);
    assert.deepEqual(authority.provenance, {
      baseline_commit: "7d90752ac4b800c5509b380d02dc828749f891a6",
      generator_sha256: "4a3df279ba4f4b561373e57aebd13a297161ffb5f3cea0000896a46bc884a12a",
      normalization: "none; scanner and source-operation reports contain only SDK-root-relative paths",
      python: "3.13.13",
      python_implementation: "cpython",
      resurrection: RESURRECTION,
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
        "node-src/metadata/validation.ts": "7022a90888e263735eba798bc9ee73b666d7d484f85b61dbaa843c705d174842",
        "node-tests/authoring-cli.test.ts": "0247a7f3710b1f94a57c60f97f9ea3ed929c4a30be379e364d7d62576ba980f3",
        "node-tests/authoring-sdk-path-evidence.test.ts": "2ac9c2512daa9a5d5d300028e2c3f7ac2c1da45858ae8efc67e2265e084aa0a6",
        "node-tests/authoring-source-operation-map.test.ts": "80461adad1b994fdba1f4f5907dd85d473bcbe23b18431aa11a0b3923ac389fd",
        "tests/test_sdk_path_evidence.py": "ef6d455b71be3958767df232b5b70004db92e587e663dc63176221a72995e9ad",
        "tests/test_source_operation_map.py": "673a0cb4e0b3eb711449e83c8a7b31a4f6e28174f247b49ad0547aa5e3c7ccc4",
      },
      unicode_database: "15.1.0",
    });
    return authority;
  })();
  return authorityPromise;
}

function frozenScannerCase(authority: FrozenAuthority, name: string): ExtractedReport {
  const value = authority.cases[name];
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  return value as ExtractedReport;
}

function frozenSourceOperationCase(authority: FrozenAuthority, name: string): JsonObject {
  const reports = authority.cases.source_operation_reports;
  assert.ok(reports !== null && typeof reports === "object" && !Array.isArray(reports));
  const value = (reports as Readonly<Record<string, unknown>>)[name];
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  return value as JsonObject;
}

function frozenCliCase(authority: FrozenAuthority): FrozenCliCase {
  const value = authority.cases.cli_sdk_root;
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  return value as FrozenCliCase;
}

async function fixture(files: Readonly<Record<string, string | Uint8Array>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "sdk-evidence-node-"));
  for (const [relative, contents] of Object.entries(files)) {
    const filename = path.join(root, relative);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, contents);
  }
  return root;
}

const SOURCE = String.raw`package sdk

import ("context"; "fmt"; "net/http")

const widgetsBasePath = "v2/widgets"
const (
  groupedBasePath = "v2/grouped"
)

type WidgetsServiceOp struct { client *Client }
type GroupedClient struct { client *Client }
type RawAPI struct { client *Client }

func (s *WidgetsServiceOp) Get(ctx context.Context, widgetID int) error {
  // path := "ignored"
  quoted := "brace } retained by body scanner"
  _ = quoted
  path := fmt.Sprintf("%s/%d", widgetsBasePath, widgetID)
  path = fmt.Sprintf("%s/%s/%v", path, "literal", complexID())
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}

func (s WidgetsServiceOp) List(ctx context.Context) error {
  path := widgetsBasePath
  _, err := s.client.NewRequest(ctx, "GET", path, nil)
  return err
}

func (s *GroupedClient) Read(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", groupedBasePath, id)
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}

func (s *RawAPI) Create(ctx context.Context) error {
  path := widgetsBasePath
  raw := ` + "`quoted { raw }`" + String.raw`
  _ = raw
  _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil)
  return err
}

func (s *WidgetsService) MissingMethod(ctx context.Context, id string) error {
  path := fmt.Sprintf("%s/%s", widgetsBasePath, id)
  return nil
}

func (s *WidgetsService) MissingPath(ctx context.Context) error {
  _, err := s.client.NewRequest(ctx, http.MethodGet, unknown, nil)
  return err
}
`;

const TESTS_ONLY = String.raw`package sdk
const testsBasePath = "v2/tests"
type TestsServiceOp struct { client *Client }
func (s *TestsServiceOp) Get(ctx context.Context) error {
  path := testsBasePath
  _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
  return err
}
`;

const DIGITALOCEAN_PROVIDER = "registry.terraform.io/digitalocean/digitalocean";
const DOMAINS_SDK = String.raw`package godo
const domainsBasePath = "v2/domains"
type DomainsServiceOp struct { client *Client }
func (s *DomainsServiceOp) Get(ctx context.Context, domain string) error { path := fmt.Sprintf("%s/%s", domainsBasePath, domain); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *DomainsServiceOp) List(ctx context.Context) error { path := domainsBasePath; _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;
const DROPLETS_SDK = String.raw`package godo
const dropletsBasePath = "v2/droplets"
type DropletsServiceOp struct { client *Client }
func (s *DropletsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", dropletsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;
const VPCS_SDK = String.raw`package godo
const vpcsBasePath = "v2/vpcs"
type VPCsServiceOp struct { client *Client }
func (s *VPCsServiceOp) Get(ctx context.Context, vpcID string) error { path := fmt.Sprintf("%s/%s", vpcsBasePath, vpcID); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;
const RESERVED_IPS_SDK = String.raw`package godo
const reservedIPsBasePath = "v2/reserved_ips"
type ReservedIPsServiceOp struct { client *Client }
func (s *ReservedIPsServiceOp) Get(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s", reservedIPsBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
func (s *ReservedIPsServiceOp) Assign(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s/actions", reservedIPsBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodPost, path, nil); return err }
`;
const RESERVED_IPV6S_SDK = String.raw`package godo
const reservedIPv6sBasePath = "v2/reserved_ipv6"
type ReservedIPV6sServiceOp struct { client *Client }
func (s *ReservedIPV6sServiceOp) Get(ctx context.Context, ip string) error { path := fmt.Sprintf("%s/%s", reservedIPv6sBasePath, ip); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;
const ACTIONS_SDK = String.raw`package godo
const actionsBasePath = "v2/actions"
type ActionsServiceOp struct { client *Client }
func (s *ActionsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", actionsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;
const THINGS_SDK = String.raw`package godo
const thingsBasePath = "v2/things"
type ThingsServiceOp struct { client *Client }
func (s *ThingsServiceOp) Get(ctx context.Context, id int) error { path := fmt.Sprintf("%s/%d", thingsBasePath, id); _, err := s.client.NewRequest(ctx, http.MethodGet, path, nil); return err }
`;

function digitalOceanSchema(resource: string): JsonObject {
  return { provider_schemas: { [DIGITALOCEAN_PROVIDER]: { resource_schemas: {
    [resource]: { block: { attributes: { name: { required: true, type: "string" } } } },
  } } } };
}

async function sdkRegistryReport(options: {
  readonly openApiPaths: JsonObject;
  readonly providerCalls: string;
  readonly resource: string;
  readonly sdkFiles?: Readonly<Record<string, string>>;
  readonly sourceFacts?: (sourceRoot: string, relativeSource: string) => JsonObject;
}): Promise<JsonObject> {
  const bare = options.resource.slice("digitalocean_".length);
  const relativeSource = `${bare}/resource_${options.resource}.go`;
  const files: Record<string, string> = {
    [`provider/${relativeSource}`]: `package ${bare}\nfunc read() {\n${options.providerCalls}\n}\n`,
  };
  for (const [name, contents] of Object.entries(options.sdkFiles ?? {})) {
    files[`sdk/${name}`] = contents;
  }
  const root = await fixture(files);
  try {
    const sourceRoot = path.join(root, "provider");
    const sdkRoot = options.sdkFiles === undefined ? undefined : path.join(root, "sdk");
    return await deriveSourceOperationRegistry({
      openApi: { openapi: "3.0.3", paths: options.openApiPaths },
      providerSource: DIGITALOCEAN_PROVIDER,
      resourcePrefix: "digitalocean",
      schemaData: digitalOceanSchema(options.resource),
      ...(sdkRoot === undefined ? {} : { sdkRoot }),
      ...(options.sourceFacts === undefined ? {} : {
        sourceFacts: options.sourceFacts(sourceRoot, relativeSource),
      }),
      sourceRoot,
    });
  } finally {
    await rm(root, { force: true, recursive: true });
  }
}

test("scanner report is exact Python-compatible across supported path shapes", async (context) => {
  const root = await fixture({
    ".git/ignored.go": SOURCE,
    "a/widgets.go": SOURCE,
    "a/widgets_test.go": SOURCE,
    "test/ignored.go": SOURCE,
    "testdata/ignored.go": SOURCE,
    "tests/only.go": TESTS_ONLY,
    "z/invalid.go": new Uint8Array([0xff, 0xfe, 0xfd]),
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const report = await extractSdkPaths(root);
  assert.deepEqual(report, frozenScannerCase(await frozenAuthority(), "supported_path_shapes"));
  assert.equal(report.evidence["Widgets.Get"]?.path_template, "v2/widgets/{widgetID}/literal/{param}");
  assert.equal(report.evidence["Widgets.List"]?.source_role, "list");
  assert.equal(report.evidence["Grouped.Read"]?.path_template, "v2/grouped/{id}");
  assert.equal(report.evidence["Raw.Create"]?.method, "POST");
  assert.equal(report.evidence["Tests.Get"]?.path_template, "v2/tests");
  assert.equal(report.unresolved["Widgets.MissingMethod"]?.reason, "method_not_detected");
  assert.equal(report.unresolved["Widgets.MissingPath"]?.reason, "path_template_not_found");
});

test("missing SDK root preserves the frozen Python empty-report contract", async () => {
  const authority = await frozenAuthority();
  const expected = frozenScannerCase(authority, "missing_root");
  assert.deepEqual(await extractSdkPaths(undefined), expected);
  assert.deepEqual(
    await extractSdkPaths(path.join(os.tmpdir(), "infrawright-sdk-root-does-not-exist")),
    expected,
  );
});

test("discovery excludes ignored directories/tests and uses portable deterministic ordering", async (context) => {
  const root = await fixture({
    "a/ä.go": "package a",
    "a/z.go": "package a",
    "b/a.go": "package b",
    "b/a_test.go": "package b",
    "tests/included.go": "package tests",
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const files = await discoverSdkGoFiles(root);
  assert.deepEqual(files.map((filename) => path.relative(root, filename).split(path.sep).join("/")), [
    "a/z.go", "a/ä.go", "b/a.go", "tests/included.go",
  ]);
});

test("balanced bodies ignore quoted braces and reject malformed bodies", () => {
  const code = String.raw`{"}", '}', ` + "`}`" + String.raw`, { nested := true }}`;
  assert.equal(extractBalancedGoBody(code, 0)[0], String.raw`"}", '}', ` + "`}`" + String.raw`, { nested := true }`);
  assert.equal(extractBalancedGoBody("{ malformed", 0)[0], undefined);
  assert.deepEqual(splitGoCallArguments(String.raw`"%s/%s", base, fn(a, "x,y")`), [
    String.raw`"%s/%s"`, " base", String.raw` fn(a, "x,y")`,
  ]);
  assert.equal(goCodeWithoutComments("a/* x\ny */b// z\nc"), "a\nb\nc");
});

test("OpenAPI path matching ignores parameter names but preserves method and ambiguity", () => {
  const operations: JsonObject[] = [
    { method: "GET", operation_id: "one", path: "/v2/widgets/{widget_id}" },
    { method: "POST", operation_id: "action", path: "/v2/widgets/{widget_id}" },
  ];
  assert.equal(matchOpenApiBySdkPath(operations, "v2/widgets/{id}").operation?.operation_id, "one");
  assert.equal(matchOpenApiBySdkPath(operations, "/v2/widgets/{id}", "POST").operation?.operation_id, "action");
  assert.deepEqual(normalizeSdkPathSegments("v2/Widgets/{name}"), ["v2", "widgets", "{param}"]);
  const ambiguous = matchOpenApiBySdkPath([
    ...operations,
    { method: "GET", operation_id: "two", path: "/v2/widgets/{other}" },
  ], "v2/widgets/{id}");
  assert.equal(ambiguous.operation, undefined);
  assert.deepEqual(ambiguous.ambiguous.map((item) => item.operation_id), ["one", "two"]);
});

test("combined matching separates unique, ambiguous, missing, and action evidence", () => {
  const extracted = {
    evidence: {
      "Widgets.Get": { client_symbol: "Widgets.Get", method: "GET", path_template: "v2/widgets/{id}", sdk_file: "widgets.go", source_role: "read" as const },
      "Widgets.List": { client_symbol: "Widgets.List", method: "GET", path_template: "v2/widgets", sdk_file: "widgets.go", source_role: "list" as const },
      "Widgets.Post": { client_symbol: "Widgets.Post", method: "POST", path_template: "v2/widgets/{id}/actions", sdk_file: "widgets.go", source_role: null },
    },
    unresolved: {},
  };
  const report = matchSdkEvidenceToOpenApi(extracted, [
    { method: "GET", operation_id: "list", path: "/v2/widgets" },
    { method: "GET", operation_id: "detail-a", path: "/v2/widgets/{a}" },
    { method: "GET", operation_id: "detail-b", path: "/v2/widgets/{b}" },
    { method: "POST", operation_id: "action", path: "/v2/widgets/{id}/actions" },
  ]);
  assert.deepEqual((report.matched as JsonObject[]).map((item) => item.client_symbol), ["Widgets.List", "Widgets.Post"]);
  const unresolved = report.unresolved as JsonObject[];
  assert.equal(unresolved[0]?.reason, "ambiguous_openapi_path");
  assert.equal((unresolved[0]?.candidates as JsonObject[]).length, 2);
});

test("SDK path authority preserves domain, integer-ID, and VPC source-operation reports", async () => {
  const authority = await frozenAuthority();
  const reports = {
    domain: await sdkRegistryReport({
      openApiPaths: {
        "/v2/domains": { get: { responses: { 200: { description: "ok" } } } },
        "/v2/domains/{domain_name}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Domains.Get(ctx, name)",
      resource: "digitalocean_domain",
      sdkFiles: { "domains.go": DOMAINS_SDK },
    }),
    droplet: await sdkRegistryReport({
      openApiPaths: {
        "/v2/droplets/{droplet_id}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Droplets.Get(ctx, id)",
      resource: "digitalocean_droplet",
      sdkFiles: { "droplets.go": DROPLETS_SDK },
    }),
    vpc: await sdkRegistryReport({
      openApiPaths: {
        "/v2/vpcs/{vpc_id}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.VPCs.Get(ctx, id)",
      resource: "digitalocean_vpc",
      sdkFiles: { "vpcs.go": VPCS_SDK },
    }),
  };
  for (const [name, report] of Object.entries(reports)) {
    assert.deepEqual(report, frozenSourceOperationCase(authority, name));
  }
});

test("SDK authority preserves read/action separation for text, AST, and helper-action calls", async () => {
  const authority = await frozenAuthority();
  const openApiPaths = {
    "/v2/reserved_ips/{ip}": { get: { responses: { 200: { description: "ok" } } } },
    "/v2/reserved_ips/{ip}/actions": { post: { responses: { 201: { description: "ok" } } } },
  };
  const providerCalls = "client.ReservedIPs.Get(ctx, name)\nclient.ReservedIPs.Assign(ctx, name)";
  const textReport = await sdkRegistryReport({
    openApiPaths,
    providerCalls,
    resource: "digitalocean_reserved_ip",
    sdkFiles: { "reserved_ips.go": RESERVED_IPS_SDK },
  });
  assert.deepEqual(textReport, frozenSourceOperationCase(authority, "reserved_ip_action"));

  const astReport = await sdkRegistryReport({
    openApiPaths,
    providerCalls: "",
    resource: "digitalocean_reserved_ip",
    sdkFiles: { "reserved_ips.go": RESERVED_IPS_SDK },
    sourceFacts: (sourceRoot, relativeSource) => ({
      files: [{ package: "reserved_ip", path: relativeSource }],
      functions: [],
      identifier_references: [],
      package_calls: [],
      raw_rest_calls: [],
      read_callbacks: [],
      resource_references: [{ file: relativeSource, resource: "digitalocean_reserved_ip" }],
      resource_registrations: [],
      selector_calls: [
        { file: relativeSource, function: "read", parts: ["client", "ReservedIPs", "Get"], symbol: "client.ReservedIPs.Get" },
        { file: relativeSource, function: "read", parts: ["client", "ReservedIPs", "Assign"], symbol: "client.ReservedIPs.Assign" },
      ],
      source_root: sourceRoot,
    }),
  });
  assert.deepEqual(astReport, frozenSourceOperationCase(authority, "ast_sdk_action"));

  const helperReport = await sdkRegistryReport({
    openApiPaths: {
      "/v2/actions/{action_id}": { get: { responses: { 200: { description: "ok" } } } },
      "/v2/reserved_ipv6/{reserved_ipv6}": { get: { responses: { 200: { description: "ok" } } } },
    },
    providerCalls: "client.Actions.Get(ctx, actionID)\nclient.ReservedIPV6s.Get(ctx, name)",
    resource: "digitalocean_reserved_ipv6",
    sdkFiles: { "action.go": ACTIONS_SDK, "reserved_ipv6.go": RESERVED_IPV6S_SDK },
  });
  assert.deepEqual(
    helperReport,
    frozenSourceOperationCase(authority, "helper_action_disambiguation"),
  );
});

test("SDK authority preserves unresolved diagnostics and no-SDK fuzzy fallback", async () => {
  const authority = await frozenAuthority();
  const cases = {
    fuzzy_fallback: await sdkRegistryReport({
      openApiPaths: {
        "/v2/domains": { get: { responses: { 200: { description: "ok" } } } },
        "/v2/domains/{domain_name}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Domains.Get(ctx, name)",
      resource: "digitalocean_domain",
    }),
    unresolved_ambiguous_path: await sdkRegistryReport({
      openApiPaths: {
        "/v2/things/{a}": { get: { responses: { 200: { description: "ok" } } } },
        "/v2/things/{b}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Things.Get(ctx, id)",
      resource: "digitalocean_thing",
      sdkFiles: { "things.go": THINGS_SDK },
    }),
    unresolved_openapi_path: await sdkRegistryReport({
      openApiPaths: {
        "/v2/something_else": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Domains.Get(ctx, name)",
      resource: "digitalocean_domain",
      sdkFiles: { "domains.go": DOMAINS_SDK },
    }),
    unresolved_symbol: await sdkRegistryReport({
      openApiPaths: {
        "/v2/domains/{domain_name}": { get: { responses: { 200: { description: "ok" } } } },
      },
      providerCalls: "client.Domains.Get(ctx, name)",
      resource: "digitalocean_domain",
      sdkFiles: { "vpcs.go": VPCS_SDK },
    }),
  };
  for (const [name, report] of Object.entries(cases)) {
    assert.deepEqual(report, frozenSourceOperationCase(authority, name));
  }
});

test("source-operation-map CLI preserves frozen --sdk-root report and exact artifacts", async (context) => {
  const root = await fixture({
    "openapi.json": `${JSON.stringify({
      info: { title: "SDK authority", version: "1" },
      openapi: "3.0.3",
      paths: {
        "/v2/domains/{domain_name}": { get: { responses: { 200: { description: "ok" } } } },
      },
    })}\n`,
    "provider/domain/resource_digitalocean_domain.go": "package domain\nfunc read() { client.Domains.Get(ctx, name) }\n",
    "schema.json": `${JSON.stringify(digitalOceanSchema("digitalocean_domain"))}\n`,
    "sdk/domains.go": DOMAINS_SDK,
  });
  context.after(async () => rm(root, { force: true, recursive: true }));
  const diagnostics = path.join(root, "diagnostics.json");
  const registry = path.join(root, "registry.json");
  let stdout = "";
  let stderr = "";
  const exitCode = await runAuthoringCommand({
    arguments: [
      "--schema", path.join(root, "schema.json"),
      "--openapi", path.join(root, "openapi.json"),
      "--source-root", path.join(root, "provider"),
      "--provider-source", DIGITALOCEAN_PROVIDER,
      "--resource-prefix", "digitalocean",
      "--sdk-root", path.join(root, "sdk"),
      "--out", registry,
      "--diagnostics", diagnostics,
    ],
    command: "source-operation-map",
    repositoryRoot: process.cwd(),
    stderr: (text) => { stderr += text; },
    stdout: (text) => { stdout += text; },
  });
  const expected = frozenCliCase(await frozenAuthority());
  assert.equal(exitCode, expected.exit_code);
  assert.equal(stdout, expected.stdout);
  assert.equal(stderr, expected.stderr);
  assert.equal(await readFile(registry, "utf8"), expected.registry_bytes);
  assert.equal(await readFile(diagnostics, "utf8"), expected.diagnostics_bytes);
  assert.deepEqual({
    diagnostics: JSON.parse(await readFile(diagnostics, "utf8")),
    registry: JSON.parse(await readFile(registry, "utf8")),
  }, {
    diagnostics: {
      diagnostics: expected.report.diagnostics,
      summary: expected.report.summary,
    },
    registry: expected.report.registry,
  });
});
