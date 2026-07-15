import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  apiItemsFrom,
  apiMetadataFromOptions,
  providerSchemaFromTerraformDump,
  reconcileItems,
  resourceSchemaFromData,
} from "../node-src/authoring/reconcile-schema-api.js";
import {
  apiMetadataFromOpenApi,
  flattenOpenApiSchema,
  mergeOpenApiSchema,
  resolveLocalRef,
  validateOpenApiDocument,
} from "../node-src/authoring/openapi.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import type { JsonObject } from "../node-src/metadata/validation.js";
import { PYTHON_ORACLE } from "./python-oracle.js";

const SCHEMA: JsonObject = {
  block: {
    attributes: {
      enabled: { optional: true, type: "bool" },
      id: { computed: true, type: "string" },
      metadata: { optional: true, type: ["map", "string"] },
      name: { required: true, type: "string" },
      org_id: { optional: true, type: "string" },
      settings: { optional: true, type: ["object", { mode: "string", priority: "number" }] },
      status: { computed: true, type: "string" },
      tags: { optional: true, type: ["list", "string"] },
    },
    block_types: {
      interfaces: {
        block: { attributes: {
          address: { required: true, type: "string" },
          generated: { computed: true, type: "string" },
          port: { optional: true, type: "number" },
        } },
        nesting_mode: "list",
      },
    },
  },
};

function paths(report: ReturnType<typeof reconcileItems>, bucket: string): Set<string> {
  const data = report.asDict();
  const byBucket = data.paths as Record<string, Array<{ path: string }>>;
  return new Set((byBucket[bucket] ?? []).map((entry) => entry.path));
}

function pythonReport(payload: JsonObject): unknown {
  const script = [
    "import json,sys",
    "from engine import reconcile_schema_api as r",
    "p=json.load(sys.stdin)",
    "m=p.get('metadata')",
    "x=r.reconcile_items(p['resource_type'],p['items'],p['schema'],override=p.get('override'),api_metadata=m)",
    "json.dump(x.as_dict(),sys.stdout,sort_keys=True,separators=(',',':'))",
  ].join(";");
  const result = spawnSync(PYTHON_ORACLE, ["-c", script], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: JSON.stringify(payload),
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

test("API object, list, and results envelopes preserve item forms", () => {
  assert.deepEqual(apiItemsFrom({ name: "one" }), [{ name: "one" }]);
  assert.deepEqual(apiItemsFrom([{ name: "one" }, { name: "two" }]), [{ name: "one" }, { name: "two" }]);
  assert.deepEqual(apiItemsFrom({ results: [{ name: "one" }] }), [{ name: "one" }]);
  assert.throws(() => apiItemsFrom("bad"), /object, list, or NetBox-style/);
});

test("pack and full Terraform schemas select providers and reject ambiguity", () => {
  assert.equal(resourceSchemaFromData({ resource_schemas: { sample_widget: SCHEMA } }, "sample_widget"), SCHEMA);
  const dump: JsonObject = { provider_schemas: {
    "registry.terraform.io/example/one": { resource_schemas: { sample_widget: SCHEMA } },
    "registry.terraform.io/example/two": { resource_schemas: { sample_widget: SCHEMA } },
  } };
  assert.throws(() => providerSchemaFromTerraformDump(dump, "sample_widget"), /multiple provider schemas/);
  assert.equal(
    resourceSchemaFromData(dump, "sample_widget", "example/one"),
    SCHEMA,
  );
});

test("OpenAPI 2/3 schemas resolve refs, allOf, arrays, siblings, and read/write asymmetry", () => {
  const spec: JsonObject = {
    openapi: "3.0.3",
    paths: {
      "/widgets": { post: {
        requestBody: { content: { "application/json": { schema: { $ref: "#/components/schemas/Write" } } } },
        responses: { "204": { description: "ok" } },
      } },
      "/widgets/{id}": { get: { responses: { "201": { content: {
        "application/problem+json": { schema: { $ref: "#/components/schemas/Read" } },
      } } } } },
    },
    components: { schemas: {
      Base: { properties: { name: { type: "string" } }, required: ["name"], type: "object" },
      Read: { allOf: [
        { $ref: "#/components/schemas/Base" },
        { properties: {
          id: { type: "string" },
          nested: { items: { properties: { value: { type: "number" } }, type: "object" }, type: "array" },
        }, type: "object" },
      ] },
      Write: { $ref: "#/components/schemas/Base", properties: {
        name: { type: "string", writeOnly: true },
      } },
    } },
  };
  const metadata = apiMetadataFromOpenApi(spec, {
    readOperations: ["GET:/widgets/{id}"],
    writeOperations: ["POST:/widgets"],
  });
  assert.equal(metadata.name?.required, true);
  assert.equal(metadata.id?.response_only, true);
  assert.equal(metadata["nested[].value"]?.readable, true);
  assert.equal(metadata.name?.write_only, true);
  assert.equal(resolveLocalRef({ a: { "x/y": [{ "~z": 1 }] } }, "#/a/x~1y/0/~0z"), 1);
  assert.deepEqual((mergeOpenApiSchema(spec, { allOf: [
    { properties: { a: { type: "string" } }, required: ["a"] },
    { properties: { b: { type: "number" } }, required: ["b"] },
  ] }).required), ["a", "b"]);

  const swagger: JsonObject = {
    paths: { "/v2/widgets": { post: {
      parameters: [{ in: "body", schema: { $ref: "#/definitions/Write" } }],
      responses: { "200": { schema: { $ref: "#/definitions/Read" } } },
    } } },
    swagger: "2.0",
    definitions: {
      Read: { properties: { result: { type: "string" } }, type: "object" },
      Write: { properties: { value: { type: "string" } }, type: "object" },
    },
  };
  const v2 = apiMetadataFromOpenApi(swagger, {
    readOperations: ["POST:/v2/widgets"], writeOperations: ["POST:/v2/widgets"],
  });
  assert.equal(v2.result?.response_only, true);
  assert.equal(v2.value?.writable, true);
});

test("Swagger Parser validates lossless authoring documents without mutating or resolving refs", async () => {
  const valid: JsonObject = {
    components: { schemas: {
      Widget: { properties: { id: { type: "string" } }, type: "object" },
    } },
    info: { title: "fixture", version: "1" },
    openapi: "3.1.0",
    paths: { "/widgets/{id}": { get: {
      responses: { "200": { content: {
        "application/json": { schema: { $ref: "#/components/schemas/Widget" } },
      }, description: "ok" } },
    } } },
  };
  const before = JSON.stringify(valid);
  await validateOpenApiDocument(valid);
  assert.equal(JSON.stringify(valid), before);

  await assert.rejects(
    validateOpenApiDocument({ openapi: "3.1.0", paths: {} }),
    /OpenAPI validation failed/,
  );
  const lossless = parseDataJsonLosslessly(JSON.stringify({
    components: { schemas: {
      Count: { maximum: 10, minimum: 0, type: "integer" },
      External: { $ref: "resources/count.yml#/Count" },
    } },
    info: { title: "lossless multi-file fixture", version: "1" },
    openapi: "3.0.3",
    paths: {},
  })) as JsonObject;
  const losslessBefore = JSON.stringify(lossless);
  await validateOpenApiDocument(lossless);
  assert.equal(JSON.stringify(lossless), losslessBefore);
});

test("OpenAPI recursion is rejected for direct ref cycles and bounded while flattening nested objects", () => {
  const recursive: JsonObject = { components: { schemas: { Loop: { $ref: "#/components/schemas/Loop" } } } };
  assert.throws(() => mergeOpenApiSchema(recursive, { $ref: "#/components/schemas/Loop" }), /recursive OpenAPI ref/);
  const allOfRecursive: JsonObject = { components: { schemas: {
    Loop: { allOf: [{ $ref: "#/components/schemas/Loop" }] },
  } } };
  assert.throws(
    () => mergeOpenApiSchema(allOfRecursive, { $ref: "#/components/schemas/Loop" }),
    /recursive OpenAPI ref/,
  );
  const arrayRecursive: JsonObject = { components: { schemas: {
    Loop: { items: { $ref: "#/components/schemas/Loop" }, type: "array" },
  } } };
  assert.doesNotThrow(() => flattenOpenApiSchema({
    mode: "read", schema: { $ref: "#/components/schemas/Loop" }, spec: arrayRecursive,
  }));
  let schema: JsonObject = { properties: { leaf: { type: "string" } }, type: "object" };
  for (let index = 0; index < 12; index += 1) schema = { properties: { child: schema }, type: "object" };
  const fields = flattenOpenApiSchema({ mode: "read", schema, spec: {} });
  assert.equal(Object.keys(fields).some((path) => path.split(".").length > 10), false);
});

test("present malformed OpenAPI schemas fail closed at every merge boundary", () => {
  for (const schema of ["invalid", true, 1, [], { allOf: ["invalid"] }]) {
    assert.throws(() => mergeOpenApiSchema({}, schema), /OpenAPI schema must be an object/);
  }
  assert.deepEqual(mergeOpenApiSchema({}, null), {});
});

test("reconciliation covers overrides, skips, nested shapes, relationships, API evidence, and exact Python parity", () => {
  const items = [
    { name: "system", order: 0, ignoredBySkip: "x" },
    {
      apiOnly: "gap",
      created: "now",
      enabled: "1",
      id: "1",
      interfaces: [{ address: "192.0.2.1", generated: "state", mystery: "gap" }],
      metadata: { rack: "r1" },
      name: "core",
      oldOrg: 7,
      settings: { apiOnly: "gap", mode: "managed", priority: 2 },
      status: "active",
      tags: [{ name: "Edge" }],
    },
  ];
  const schema: JsonObject = JSON.parse(JSON.stringify(SCHEMA));
  ((schema.block as JsonObject).attributes as JsonObject).order = { optional: true, type: "number" };
  const override: JsonObject = {
    renames: { old_org: "org_id" },
    skip_if_lte: [{ order: 0 }],
  };
  const metadata = apiMetadataFromOptions({ actions: { POST: {
    apiOnly: { readOnly: false, required: true, type: "string" },
  } } });
  const report = reconcileItems({ apiMetadata: metadata, items, override, resourceSchema: schema, resourceType: "sample_widget" });
  assert(paths(report, "skipped").has("$item"));
  assert(paths(report, "renamed").has("old_org"));
  assert(paths(report, "transformed").has("enabled"));
  assert(paths(report, "relationship").size === 0);
  assert(paths(report, "dropped_known").has("created"));
  assert(paths(report, "shape_mismatch").has("settings.api_only") === false);
  assert(paths(report, "unknown").has("api_only"));
  assert(paths(report, "unknown").has("interfaces[].mystery"));
  assert.deepEqual(report.asDict(), pythonReport({
    items, metadata, override, resource_type: "sample_widget", schema,
  }));
});

test("relationship aliases, acknowledged drops, unknowns, and shape mismatch retain Python buckets", () => {
  const schema: JsonObject = { block: { attributes: {
    name: { required: true, type: "string" },
    site_id: { required: true, type: "number" },
    settings: { optional: true, type: ["object", { mode: "string" }] },
  } } };
  const report = reconcileItems({
    items: [{ acknowledged: "yes", name: "edge", settings: "wrong", site: { id: 10 }, surprise: true }],
    override: { acknowledged_drops: ["acknowledged"] },
    resourceSchema: schema,
    resourceType: "sample_widget",
  });
  assert(paths(report, "relationship").has("site"));
  assert(paths(report, "dropped_acknowledged").has("acknowledged"));
  assert(paths(report, "shape_mismatch").has("settings"));
  assert(paths(report, "unknown").has("surprise"));
});

test("arbitrary-size integers and fractional numbers retain numeric classifications", () => {
  const schema: JsonObject = { block: { attributes: {
    amount: { optional: true, type: "number" }, name: { required: true, type: "string" },
  } } };
  const report = reconcileItems({
    items: [
      { amount: new LosslessNumber("9007199254740993123456789"), name: "wide" },
      { amount: new LosslessNumber("1.25"), name: "fraction" },
    ],
    resourceSchema: schema,
    resourceType: "sample_widget",
  });
  const entries = (report.asDict().paths as Record<string, Array<{ path: string; types: JsonObject }>>).kept ?? [];
  const amount = entries.find((entry) => entry.path === "amount");
  assert.deepEqual(amount?.types, { float: 1, int: 1 });
});

test("report path ordering is exact Python code-point ordering", () => {
  const schema: JsonObject = { block: { attributes: {} } };
  const items = [{ a: 1, z: 1, "ä": 1 }];
  const report = reconcileItems({ items, resourceSchema: schema, resourceType: "sample_widget" });
  assert.deepEqual(report.asDict(), pythonReport({
    items, resource_type: "sample_widget", schema,
  }));
  const unknown = (report.asDict().paths as Record<string, Array<{ path: string }>>).unknown ?? [];
  assert.deepEqual(unknown.map((entry) => entry.path), ["a", "z", "ä"]);
});
