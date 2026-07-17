import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  baseResourceTokens,
  buildOpenApiResourceMap,
  canonicalOpenApiPathParts,
  canonicalSegmentSlug,
  collectionPaths,
  fetchPathVariants,
  matchRegistryPath,
  pluralizeToken,
  providerFromSchema,
} from "../node-src/authoring/openapi-resource-map.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
const AUTHORITY_SHA256 = "e4e25a12a871c895364bce16fe05a8bcd94debd1eddc53de9fc75ca82bc8ce3c";
const authorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-openapi-resource-map-v1.json",
));
assert.equal(createHash("sha256").update(authorityBytes).digest("hex"), AUTHORITY_SHA256);

interface RecordedJsonFile {
  readonly bytes: string;
  readonly json: JsonObject;
  readonly path: string;
}

interface ReportInput {
  readonly api_prefix: string;
  readonly openapi: JsonObject | RecordedJsonFile;
  readonly provider_source: string | null;
  readonly registry_data: JsonObject | null;
  readonly resource_prefix: string;
  readonly schema: JsonObject | RecordedJsonFile;
}

interface ReportCase {
  readonly input: ReportInput;
  readonly name: string;
  readonly report?: JsonObject;
  readonly python_report?: JsonObject;
}

interface CliCase {
  readonly artifacts: { readonly report: string };
  readonly exit: number;
  readonly input: {
    readonly argv: readonly string[];
    readonly files: readonly {
      readonly bytes: string;
      readonly path: string;
    }[];
  };
  readonly name: string;
  readonly stderr: string;
  readonly stdout: string;
}

interface DifferentialCliCase {
  readonly input: {
    readonly files: Readonly<Record<string, readonly string[]>>;
    readonly options: Readonly<Record<string, readonly string[]>>;
    readonly positional: readonly string[];
  };
  readonly name: string;
  readonly python_exit: number;
  readonly python_stderr: string;
  readonly python_stdout: string;
}

const authority = JSON.parse(authorityBytes.toString("utf8")) as {
  readonly node_live_differential: {
    readonly cli_cases: readonly DifferentialCliCase[];
    readonly report_cases: readonly ReportCase[];
  };
  readonly retained_unittest: {
    readonly cli_cases: readonly CliCase[];
    readonly report_cases: readonly ReportCase[];
  };
};

function schema(attributes: JsonObject = { name: { required: true, type: "string" } }): JsonObject {
  return { block: { attributes } };
}

function reportCase(cases: readonly ReportCase[], name: string): ReportCase {
  const matches = cases.filter((item) => item.name === name);
  assert.equal(matches.length, 1, name);
  return matches[0]!;
}

function recordedJson(value: JsonObject | RecordedJsonFile): JsonObject {
  return Object.hasOwn(value, "json") ? (value as RecordedJsonFile).json : value as JsonObject;
}

function defaultRegistryData(): JsonObject {
  const output: JsonObject = {};
  const packs = path.join(ROOT, "packs");
  for (const name of readdirSync(packs).sort()) {
    const filename = path.join(packs, name, "registry.json");
    if (!existsSync(filename)) continue;
    Object.assign(output, JSON.parse(readFileSync(filename, "utf8")) as JsonObject);
  }
  return output;
}

function buildRecordedReport(input: ReportInput): JsonObject {
  return buildOpenApiResourceMap({
    apiPrefix: input.api_prefix,
    openApi: recordedJson(input.openapi),
    ...(input.provider_source === null ? {} : { providerSource: input.provider_source }),
    registryData: input.registry_data ?? defaultRegistryData(),
    resourcePrefix: input.resource_prefix,
    schemaData: recordedJson(input.schema),
  });
}

test("path inventory canonicalizes parameters, prefixes, suffixes, and irregular plurals", () => {
  const spec: JsonObject = { paths: {
    "/api/v1/addresses": { get: {} },
    "/api/v1/addresses/{addressId}": { get: {} },
    "/api/v1/no-read": { delete: {} },
  } };
  assert.deepEqual(collectionPaths(spec, "/api/v1/"), ["/api/v1/addresses"]);
  assert.deepEqual(canonicalOpenApiPathParts("/api/v1/addresses/{addressId}"), ["api", "v1", "addresses", "{}"]);
  assert.equal(pluralizeToken("address"), "addresses");
  assert.equal(pluralizeToken("chassis"), "chassis");
  assert.equal(canonicalSegmentSlug("appConnectorGroup"), "app-connector-group");
  assert.deepEqual(baseResourceTokens("zpa_app_connector_group", "zpa"), ["app", "connector", "group"]);
  assert.deepEqual(fetchPathVariants("/api/v1/zcc/devices/{id}", "zcc", "/api/v1/").map((item) => item.variant), [
    "exact", "api_prefix_stripped", "api_prefix_stripped_product_prefix_stripped",
  ]);
  assert.deepEqual(matchRegistryPath(spec, "/api/v1/", "/addresses", "example"), {
    match: "exact", openapi_path: "/api/v1/addresses", variant: "exact",
  });
});

test("provider selection accepts committed and full dumps and rejects ambiguity", () => {
  const committed = { resource_schemas: { example_widget: schema() } };
  assert.equal(providerFromSchema(committed), committed);
  const full: JsonObject = { provider_schemas: {
    "registry.terraform.io/example/one": committed,
    "registry.terraform.io/example/two": committed,
  } };
  assert.throws(() => providerFromSchema(full), /multiple providers/);
  assert.equal(providerFromSchema(full, "example/one"), committed);
});

test("generic mapping report is exactly Python-compatible", () => {
  const providerSource = "registry.terraform.io/example/example";
  const schemaData: JsonObject = { provider_schemas: { [providerSource]: {
    provider: { block: { attributes: {
      api_url: { description: "API URL", optional: true, type: "string" },
      token: { optional: true, sensitive: true, type: "string" },
    } } },
    resource_schemas: {
      example_address: schema({ name: { required: true, type: "string" } }),
      example_folder: schema({ title: { required: true, type: "string" }, uid: { optional: true, type: "string" } }),
      example_missing: schema(),
      example_project_action: schema(),
      example_widget: schema(),
    },
  } } };
  const write = (fields: JsonObject): JsonObject => ({ requestBody: { content: {
    "application/json": { schema: { properties: fields, type: "object" } },
  } }, responses: { "200": { description: "ok" } } });
  const openApi: JsonObject = {
    info: { title: "Example API" }, openapi: "3.0.3",
    paths: {
      "/api/v1/addresses": { get: { responses: { "200": { description: "ok" } } }, post: write({ name: { type: "string" } }) },
      "/api/v1/addresses/{id}": { get: { responses: { "200": { description: "ok" } } }, patch: write({ name: { type: "string" } }) },
      "/api/v1/folders": { get: { responses: { "200": { description: "ok" } } }, post: write({ title: { type: "string" }, uid: { type: "string" } }) },
      "/api/v1/folders/{uid}": { get: { responses: { "200": { description: "ok" } } }, put: write({ title: { type: "string" } }) },
      "/api/v1/projectActions": { get: { responses: { "200": { description: "ok" } } }, post: write({ name: { type: "string" } }) },
      "/api/v1/a/widgets": { get: { responses: { "200": { description: "ok" } } }, post: write({ name: { type: "string" } }) },
      "/api/v1/a/widgets/{id}": { get: { responses: { "200": { description: "ok" } } } },
      "/api/v1/b/widgets": { get: { responses: { "200": { description: "ok" } } }, post: write({ name: { type: "string" } }) },
      "/api/v1/b/widgets/{id}": { get: { responses: { "200": { description: "ok" } } } },
    },
  };
  const registryData: JsonObject = {
    example_address: { fetch: { path: "/example/addresses/{id}" }, product: "example" },
    example_folder: { fetch: { pagination: "single", path: "/api/v1/example/folders" }, product: "example" },
    example_missing: { product: "example", read: { operation_id: "GET:/missing", path: "/missing", path_kind: "collection" }, status: "mapped" },
  };
  const options = {
    apiPrefix: "/api/v1/", openApi, providerSource,
    registryData, resourcePrefix: "example", schemaData,
  };
  const node = buildOpenApiResourceMap(options);
  const expected = reportCase(authority.node_live_differential.report_cases, "generic_mapping");
  assert.deepEqual(node, expected.python_report);
});

test("ZTC aliases and action-shaped resources choose current operations", () => {
  const providerSource = "registry.terraform.io/zscaler/ztc";
  const schemaData: JsonObject = { provider_schemas: { [providerSource]: { resource_schemas: {
    ztc_activation_status: schema({ status: { optional: true, type: "string" } }),
    ztc_dns_forwarding_gateway: schema(),
  } } } };
  const openApi: JsonObject = { info: { title: "Zscaler Cloud & Branch Connector" }, openapi: "3.0.3", paths: {
    "/dns-gateways": { get: {}, post: {} },
    "/dns-gateways/{id}": { get: {}, put: {} },
    "/ecAdminActivateStatus": { get: {} },
    "/ecAdminActivateStatus/activate": { put: {} },
  } };
  const report = buildOpenApiResourceMap({ apiPrefix: "/", openApi, providerSource, resourcePrefix: "ztc", schemaData });
  const byResource = Object.fromEntries((report.resources as JsonObject[]).map((item) => [item.resource, item]));
  assert.equal(byResource.ztc_dns_forwarding_gateway?.status, "matched");
  assert.equal((byResource.ztc_dns_forwarding_gateway?.candidates as JsonObject[])[0]?.matched_segment, "dns-gateways");
  assert.equal(byResource.ztc_activation_status?.status, "special");
  assert.deepEqual(byResource.ztc_activation_status?.write_operations, ["PUT:/ecAdminActivateStatus/activate"]);
  const expected = reportCase(
    authority.node_live_differential.report_cases,
    "ztc_aliases_and_action_resource",
  );
  assert.deepEqual(report, expected.python_report);
});

test("wrong-product OpenAPI evidence cannot satisfy registry coverage", () => {
  const report = buildOpenApiResourceMap({
    apiPrefix: "/", openApi: { info: { title: "Zscaler Private Access API" }, paths: {
      "/devices": { get: {} },
    } },
    providerSource: "registry.terraform.io/zscaler/zcc",
    registryData: { zcc_device: { fetch: { path: "/devices" }, product: "zcc" } },
    resourcePrefix: "zcc",
    schemaData: { resource_schemas: { zcc_device: schema() } },
  });
  const coverage = report.registry_fetch_coverage as JsonObject;
  assert.deepEqual((coverage.summary as JsonObject).matched, 0);
  assert.equal((coverage.resources as JsonObject[])[0]?.reason, "openapi_product_mismatch");
});

test("parent-scoped allocations and derived assignments are exact Python-compatible", () => {
  const providerSource = "registry.terraform.io/example/netbox";
  const schemaData: JsonObject = { provider_schemas: { [providerSource]: { resource_schemas: {
    netbox_available_ip_address: schema({
      description: { optional: true, type: "string" }, ip_address: { computed: true, type: "string" },
      prefix_id: { optional: true, type: "number" },
    }),
    netbox_device_primary_ip: schema({
      device_id: { required: true, type: "number" }, ip_address_id: { required: true, type: "number" },
      ip_address_version: { optional: true, type: "number" },
    }),
    netbox_device_interface_primary_mac_address: schema({
      interface_id: { required: true, type: "number" }, mac_address_id: { required: true, type: "number" },
    }),
  } } } };
  const response = { content: { "application/json": { schema: { properties: {
    primaryIp4: { type: "number" }, primaryIp6: { type: "number" },
    primaryMacAddress: { type: "number" },
  }, type: "object" } } } };
  const request = { requestBody: { content: { "application/json": { schema: { properties: {
    primaryIp4: { type: "number" }, primaryIp6: { type: "number" },
    primaryMacAddress: { type: "number" },
  }, type: "object" } } } }, responses: { "200": response } };
  const openApi: JsonObject = { openapi: "3.0.3", paths: {
    "/api/dcim/devices/": { get: {} },
    "/api/dcim/devices/{id}/": { get: { responses: { "200": response } }, patch: request },
    "/api/dcim/interfaces/": { get: {} },
    "/api/dcim/interfaces/{id}/": { get: { responses: { "200": response } }, patch: request },
    "/api/ipam/prefixes/{id}/available-ips/": { post: {
      requestBody: { content: { "application/json": { schema: { properties: { description: { type: "string" } }, type: "object" } } } },
      responses: { "200": { description: "ok" } },
    } },
  }, components: { schemas: {} } };
  const options = { apiPrefix: "/api/", openApi, providerSource, registryData: {}, resourcePrefix: "netbox", schemaData };
  const node = buildOpenApiResourceMap(options);
  const expected = reportCase(
    authority.node_live_differential.report_cases,
    "parent_scoped_allocations",
  );
  assert.deepEqual(node, expected.python_report);
});

test("computed API fields retain writable relationship aliases before suppression", () => {
  const providerSource = "registry.terraform.io/example/example";
  const schemaData: JsonObject = { provider_schemas: { [providerSource]: { resource_schemas: {
    example_widget: schema({
      site: { computed: true, type: "number" },
      site_id: { optional: true, type: "number" },
    }),
  } } } };
  const openApi: JsonObject = { openapi: "3.0.3", paths: {
    "/widgets": { get: {}, post: { requestBody: { content: { "application/json": {
      schema: { properties: { site: { type: "number" } }, type: "object" },
    } } } } },
    "/widgets/{id}": { get: {} },
  } };
  const options = { apiPrefix: "/", openApi, providerSource, registryData: {}, resourcePrefix: "example", schemaData };
  const report = buildOpenApiResourceMap(options);
  const expected = reportCase(
    authority.node_live_differential.report_cases,
    "computed_relationship_alias",
  );
  assert.deepEqual(report, expected.python_report);
  const contract = ((report.resources as JsonObject[])[0]?.static_contract as JsonObject);
  assert.deepEqual(contract.aliased_top_level_paths, [{
    api_path: "site", reason: "relationship_id", terraform_path: "site_id",
  }]);
});

test("generic and registry ratios use Python half-even four-place rounding", () => {
  for (const [total, expected] of [[32, 0.0312], [160, 0.0063]] as const) {
    const providerSource = "registry.terraform.io/example/example";
    const resources: JsonObject = { example_folder: schema() };
    const registry: JsonObject = { example_folder: { fetch: { path: "/folders" }, product: "example" } };
    for (let index = 1; index < total; index += 1) {
      const name = `example_missing_${String(index).padStart(3, "0")}`;
      resources[name] = schema();
      registry[name] = { fetch: { path: `/missing/${index}` }, product: "example" };
    }
    const schemaData: JsonObject = { provider_schemas: { [providerSource]: { resource_schemas: resources } } };
    const openApi: JsonObject = { openapi: "3.0.3", paths: {
      "/folders": { get: {}, post: {} }, "/folders/{id}": { get: {} },
    } };
    const options = { apiPrefix: "/", openApi, providerSource, registryData: registry, resourcePrefix: "example", schemaData };
    const report = buildOpenApiResourceMap(options);
    const expectedReport = reportCase(
      authority.node_live_differential.report_cases,
      `half_even_ratio_${total}`,
    );
    assert.deepEqual(report, expectedReport.python_report);
    assert.equal((report.coverage as JsonObject).coverage_ratio, expected);
    assert.equal(((report.registry_fetch_coverage as JsonObject).summary as JsonObject).coverage_ratio, expected);
  }
});

test("all retained Python OpenAPI resource-map reports remain exact", () => {
  assert.equal(authority.retained_unittest.report_cases.length, 13);
  for (const item of authority.retained_unittest.report_cases) {
    assert.deepEqual(buildRecordedReport(item.input), item.report, item.name);
  }
});

test("frozen Python OpenAPI resource-map CLI contract remains exact", async (context) => {
  const matches = authority.node_live_differential.cli_cases.filter((item) => {
    return item.name === "authoring_cli_openapi_map";
  });
  assert.equal(matches.length, 1);
  const item = matches[0]!;
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-openapi-authority-"));
  context.after(async () => rm(directory, { force: true, recursive: true }));

  const arguments_: string[] = [];
  for (const [option, values] of Object.entries(item.input.files)) {
    assert.equal(values.length, 1, option);
    const filename = path.join(directory, `${option.slice(2)}.json`);
    await writeFile(filename, values[0]!, "utf8");
    arguments_.push(option, filename);
  }
  for (const [option, values] of Object.entries(item.input.options)) {
    for (const value of values) arguments_.push(option, value);
  }
  arguments_.push(...item.input.positional);

  const result = spawnSync(process.execPath, [CLI, "openapi-map", ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(result.status, item.python_exit, result.stderr);
  assert.equal(result.stdout, item.python_stdout);
  assert.equal(result.stderr, item.python_stderr);
});

test("retained Python CLI input without required OpenAPI info fails current validation", async (context) => {
  const matches = authority.retained_unittest.cli_cases.filter((item) => {
    return item.name === "cli:test_cli_registry_fetch_coverage_strips_api_prefix#2";
  });
  assert.equal(matches.length, 1);
  const item = matches[0]!;
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-openapi-invalid-authority-"));
  context.after(async () => rm(directory, { force: true, recursive: true }));

  const replacements = new Map<string, string>();
  for (const file of item.input.files) {
    const filename = path.join(directory, path.basename(file.path));
    await writeFile(filename, file.bytes, "utf8");
    replacements.set(file.path, filename);
  }
  const outIndex = item.input.argv.indexOf("--out") + 1;
  assert.ok(outIndex > 0);
  replacements.set(item.input.argv[outIndex]!, path.join(directory, "report.json"));
  const arguments_ = item.input.argv.map((value) => replacements.get(value) ?? value);

  const result = spawnSync(process.execPath, [CLI, "openapi-map", ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(item.exit, 0);
  assert.equal(result.status, 1);
  assert.equal(result.stdout, "");
  assert.equal(
    result.stderr,
    "error: OpenAPI validation failed: Swagger schema validation failed.\n"
      + "  #/ must have required property 'info'\n\n",
  );
});
