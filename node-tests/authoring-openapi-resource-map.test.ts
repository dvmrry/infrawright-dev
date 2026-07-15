import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
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
import { PYTHON_ORACLE } from "./python-oracle.js";

function schema(attributes: JsonObject = { name: { required: true, type: "string" } }): JsonObject {
  return { block: { attributes } };
}

function pythonReport(payload: JsonObject): unknown {
  const script = [
    "import json,sys,tempfile,os",
    "from engine import openapi_resource_map as m",
    "p=json.load(sys.stdin)",
    "d=tempfile.mkdtemp()",
    "sp=os.path.join(d,'schema.json')",
    "op=os.path.join(d,'openapi.json')",
    "open(sp,'w').write(json.dumps(p['schema']))",
    "open(op,'w').write(json.dumps(p['openapi']))",
    "r=m.build_report(sp,op,provider_source=p.get('provider_source'),resource_prefix=p.get('resource_prefix',''),api_prefix=p.get('api_prefix','/api/'),registry_data=p.get('registry_data'))",
    "json.dump(r,sys.stdout,sort_keys=True,separators=(',',':'))",
  ].join(";");
  const result = spawnSync(PYTHON_ORACLE, ["-c", script], {
    cwd: process.cwd(), encoding: "utf8", input: JSON.stringify(payload),
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
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
  const python = pythonReport({
    api_prefix: options.apiPrefix, openapi: openApi, provider_source: providerSource,
    registry_data: registryData, resource_prefix: "example", schema: schemaData,
  });
  assert.deepEqual(node, python);
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
  assert.deepEqual(report, pythonReport({
    api_prefix: "/", openapi: openApi, provider_source: providerSource,
    registry_data: {}, resource_prefix: "ztc", schema: schemaData,
  }));
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
  assert.deepEqual(node, pythonReport({
    api_prefix: "/api/", openapi: openApi, provider_source: providerSource,
    registry_data: {}, resource_prefix: "netbox", schema: schemaData,
  }));
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
  assert.deepEqual(report, pythonReport({
    api_prefix: "/", openapi: openApi, provider_source: providerSource,
    registry_data: {}, resource_prefix: "example", schema: schemaData,
  }));
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
    assert.deepEqual(report, pythonReport({
      api_prefix: "/", openapi: openApi, provider_source: providerSource,
      registry_data: registry, resource_prefix: "example", schema: schemaData,
    }));
    assert.equal((report.coverage as JsonObject).coverage_ratio, expected);
    assert.equal(((report.registry_fetch_coverage as JsonObject).summary as JsonObject).coverage_ratio, expected);
  }
});
