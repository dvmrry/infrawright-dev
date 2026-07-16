import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  apiItemsFrom,
  apiMetadataFromOptions,
  providerSchemaFromTerraformDump,
  reconcileItems,
  resourceSchemaFromData,
  type ApiMetadata,
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

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
const AUTHORITY_SHA256 = "e44663ac77b8bc7be8b2af65f2bf39e7f6dbca12b7d79805b9fa133e99f7c9ff";

interface FrozenReconcileInput {
  readonly api_metadata?: ApiMetadata | null;
  readonly items: readonly unknown[];
  readonly metadata?: ApiMetadata | null;
  readonly override?: JsonObject | null;
  readonly resource_type: string;
  readonly schema: JsonObject;
}

interface FrozenReportCase {
  readonly input: FrozenReconcileInput;
  readonly name: string;
  readonly report: JsonObject;
}

interface FrozenNodeReportCase {
  readonly input: FrozenReconcileInput;
  readonly name: string;
  readonly python_report: JsonObject;
}

interface FrozenHelperCase {
  readonly input: JsonObject;
  readonly name: string;
  readonly output: unknown;
}

interface FrozenCliCase {
  readonly artifacts: Readonly<Record<string, string>>;
  readonly exit: number;
  readonly input: {
    readonly argv: readonly string[];
    readonly files: readonly {
      readonly bytes: string;
      readonly option: string;
      readonly path: string;
    }[];
  };
  readonly name: string;
  readonly stderr: string;
  readonly stdout: string;
}

interface FrozenAuthority {
  readonly kind: string;
  readonly node_live_differential: {
    readonly report_cases: readonly FrozenNodeReportCase[];
  };
  readonly retained_unittest: {
    readonly cli_cases: readonly FrozenCliCase[];
    readonly helper_cases: readonly FrozenHelperCase[];
    readonly report_cases: readonly FrozenReportCase[];
    readonly tests_run: number;
  };
  readonly version: number;
}

const authorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-reconcile-schema-api-v1.json",
));
assert.equal(
  createHash("sha256").update(authorityBytes).digest("hex"),
  AUTHORITY_SHA256,
  "frozen CPython reconcile authority changed without re-adjudication",
);
const authority = JSON.parse(authorityBytes.toString("utf8")) as FrozenAuthority;
assert.equal(authority.kind, "infrawright.python-reconcile-schema-api-authority");
assert.equal(authority.version, 1);

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

function replayReport(input: FrozenReconcileInput): JsonObject {
  const apiMetadata = input.api_metadata ?? input.metadata ?? undefined;
  return reconcileItems({
    ...(apiMetadata === undefined ? {} : { apiMetadata }),
    items: input.items,
    ...(input.override === undefined || input.override === null
      ? {} : { override: input.override }),
    resourceSchema: input.schema,
    resourceType: input.resource_type,
  }).asDict();
}

function frozenNodeReport(name: string): JsonObject {
  const matches = authority.node_live_differential.report_cases.filter((item) => item.name === name);
  assert.equal(matches.length, 1, `expected one frozen Node differential named ${name}`);
  return matches[0]!.python_report;
}

function replayHelper(frozen: FrozenHelperCase): unknown {
  if (frozen.name.startsWith("api_metadata_from_options:")) {
    const input = frozen.input as unknown as {
      readonly source: string;
      readonly value: JsonObject;
    };
    return apiMetadataFromOptions(input.value, input.source);
  }
  if (frozen.name.startsWith("load_resource_schema:")) {
    const input = frozen.input as unknown as {
      readonly provider_source: string | null;
      readonly resource_type: string;
      readonly schema: { readonly json: JsonObject };
    };
    return resourceSchemaFromData(
      input.schema.json,
      input.resource_type,
      input.provider_source ?? undefined,
    );
  }
  if (frozen.name.startsWith("api_metadata_from_openapi:")) {
    const input = frozen.input as unknown as {
      readonly read_operations: readonly string[];
      readonly spec: JsonObject;
      readonly write_operations: readonly string[];
    };
    return apiMetadataFromOpenApi(input.spec, {
      readOperations: input.read_operations,
      writeOperations: input.write_operations,
    });
  }
  assert.fail(`unsupported frozen reconcile helper ${frozen.name}`);
}

async function materializeCliCase(
  frozen: FrozenCliCase,
  root: string,
): Promise<readonly string[]> {
  const paths = new Map<string, string>();
  for (const input of frozen.input.files) {
    const filename = path.join(root, input.path);
    await mkdir(path.dirname(filename), { recursive: true });
    await writeFile(filename, input.bytes, "utf8");
    paths.set(input.path, filename);
  }
  return frozen.input.argv.map((argument) => {
    if (paths.has(argument)) return paths.get(argument)!;
    if (argument.startsWith(".reconcile-openapi-authority/")) {
      return path.join(root, argument);
    }
    return argument;
  });
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
  const lossless = parseDataJsonLosslessly(`{
    "components": {"schemas": {
      "Count": {
        "maximum": 1e400,
        "minimum": 900719925474099312345678901,
        "type": "integer"
      },
      "External": {"$ref": "resources/count.yml#/Count"}
    }},
    "info": {"title": "lossless multi-file fixture", "version": "1"},
    "openapi": "3.0.3",
    "paths": {}
  }`) as JsonObject;
  const losslessBefore = JSON.stringify(lossless);
  await validateOpenApiDocument(lossless);
  assert.equal(JSON.stringify(lossless), losslessBefore);
  const count = (((lossless.components as JsonObject).schemas as JsonObject).Count as JsonObject);
  assert.equal(String(count.maximum), "1e400");
  assert.equal(String(count.minimum), "900719925474099312345678901");
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
  assert.deepEqual(report.asDict(), frozenNodeReport("comprehensive_reconciliation"));
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
  assert.deepEqual(report.asDict(), frozenNodeReport("codepoint_path_ordering"));
  const unknown = (report.asDict().paths as Record<string, Array<{ path: string }>>).unknown ?? [];
  assert.deepEqual(unknown.map((entry) => entry.path), ["a", "z", "ä"]);
});

test("all retained Python reconcile reports remain exact", async (context) => {
  assert.equal(authority.retained_unittest.tests_run, 9);
  assert.equal(authority.retained_unittest.report_cases.length, 7);
  for (const frozen of authority.retained_unittest.report_cases) {
    await context.test(frozen.name, () => {
      assert.deepEqual(replayReport(frozen.input), frozen.report);
    });
  }
});

test("all retained Python reconcile helpers remain exact", async (context) => {
  assert.equal(authority.retained_unittest.helper_cases.length, 5);
  for (const frozen of authority.retained_unittest.helper_cases) {
    await context.test(frozen.name, () => {
      assert.deepEqual(replayHelper(frozen), frozen.output);
    });
  }
});

test("retained reconcile CLI artifacts retain exact Python bytes through built Node", async (context) => {
  assert.equal(authority.retained_unittest.cli_cases.length, 1);
  for (const frozen of authority.retained_unittest.cli_cases) {
    await context.test(frozen.name, async (caseContext) => {
      const root = await mkdtemp(path.join(os.tmpdir(), "reconcile-frozen-cli-"));
      caseContext.after(async () => rm(root, { force: true, recursive: true }));
      const arguments_ = await materializeCliCase(frozen, root);
      const result = spawnSync(process.execPath, [CLI, "reconcile", ...arguments_], {
        cwd: ROOT,
        encoding: "utf8",
        env: { ...process.env, PYTHON: path.join(root, "python-must-not-run") },
      });
      assert.equal(result.status, frozen.exit, result.stderr);
      assert.equal(result.stdout, frozen.stdout);
      assert.equal(result.stderr, frozen.stderr);
      assert.deepEqual(Object.keys(frozen.artifacts), ["report"]);
      const outputIndex = arguments_.indexOf("--out");
      assert.notEqual(outputIndex, -1, "frozen CLI case must record --out");
      const output = arguments_[outputIndex + 1];
      assert.ok(output, "frozen CLI case must record an --out value");
      assert.equal(await readFile(output, "utf8"), frozen.artifacts.report);
    });
  }
});
