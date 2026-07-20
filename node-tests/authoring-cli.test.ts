import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");

interface FrozenCliCase {
  readonly name: string;
  readonly python_exit: number;
  readonly python_stderr: string;
  readonly python_stdout: string;
}

function loadFrozenCliCases(filename: string, expectedSha256: string): readonly FrozenCliCase[] {
  const bytes = readFileSync(path.join(ROOT, "node-tests", "fixtures", filename));
  assert.equal(createHash("sha256").update(bytes).digest("hex"), expectedSha256);
  const authority = JSON.parse(bytes.toString("utf8")) as {
    readonly node_live_differential: { readonly cli_cases: readonly FrozenCliCase[] };
  };
  return authority.node_live_differential.cli_cases;
}

const reconcileCliCases = loadFrozenCliCases(
  "python-reconcile-schema-api-v1.json",
  "fff36234703a253bf903b97c2396a8d2d65a7b50b82407eff752eeb86c521004",
);
const openApiCliCases = loadFrozenCliCases(
  "python-openapi-resource-map-v1.json",
  "9ce98cb64a64c519374d582b2f0572896cdaabe25f26ed048f10b63b13a73efc",
);
const sourceOperationAuthorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-source-operation-map-v1.json",
));
assert.equal(
  createHash("sha256").update(sourceOperationAuthorityBytes).digest("hex"),
  "8838730fce62480c8622131a47ce41e09153ac7ab12fdc752fd62596dc5376f6",
);
const sourceOperationAuthority = JSON.parse(sourceOperationAuthorityBytes.toString("utf8")) as {
  readonly cli_cases: readonly {
    readonly artifacts: { readonly stderr: string; readonly stdout: string };
    readonly name: string;
  }[];
};

interface Fixture {
  readonly api: string;
  readonly facts: string;
  readonly openApi: string;
  readonly root: string;
  readonly schema: string;
  readonly source: string;
}

async function jsonFile(filename: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(filename), { recursive: true });
  await writeFile(filename, `${JSON.stringify(value)}\n`, "utf8");
}

async function fixture(): Promise<Fixture> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-authoring-cli-"));
  const source = path.join(root, "source");
  const schema = path.join(root, "schema.json");
  const api = path.join(root, "api.json");
  const openApi = path.join(root, "openapi.json");
  const facts = path.join(root, "facts.json");
  await mkdir(source, { recursive: true });
  await writeFile(
    path.join(source, "resource_widget.go"),
    "package provider\nfunc resourceWidgetRead() { client.Widgets.Get(ctx, id) }\n",
    "utf8",
  );
  await jsonFile(schema, {
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
  await jsonFile(api, [{ name: "example", settings: { mode: "strict" } }]);
  await jsonFile(openApi, {
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
  await jsonFile(facts, {
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
  return { api, facts, openApi, root, schema, source };
}

function runNode(arguments_: readonly string[], environment?: NodeJS.ProcessEnv) {
  return spawnSync(process.execPath, [CLI, ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment ?? process.env,
  });
}

test("authoring CLI reports remain byte-compatible with frozen Python", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const comparisons = [
    {
      node: ["reconcile", "example_widget", "--api", data.api, "--schema", data.schema],
      authority: reconcileCliCases,
      name: "authoring_cli_reconcile",
    },
    {
      node: [
        "openapi-map", "--schema", data.schema, "--openapi", data.openApi,
        "--resource-prefix", "example", "--api-prefix", "/",
      ],
      authority: openApiCliCases,
      name: "authoring_cli_openapi_map",
    },
  ] as const;
  for (const comparison of comparisons) {
    const node = runNode(comparison.node);
    const matches = comparison.authority.filter((item) => item.name === comparison.name);
    assert.equal(matches.length, 1, comparison.name);
    const expected = matches[0]!;
    assert.equal(node.status, expected.python_exit, node.stderr);
    assert.equal(node.stderr, expected.python_stderr, comparison.name);
    assert.equal(node.stdout, expected.python_stdout, comparison.name);
    if (comparison.name === "authoring_cli_reconcile") {
      assert.match(node.stdout, /settings\.mode/u);
      assert.doesNotMatch(node.stdout, /settings\[\]\.mode/u);
    }
  }
});

test("source-operation CLI output remains byte-compatible with frozen Python", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const result = runNode([
    "source-operation-map",
    "--schema", data.schema,
    "--openapi", data.openApi,
    "--source-root", data.source,
    "--resource-prefix", "example",
    "--source-facts", data.facts,
  ]);
  const matches = sourceOperationAuthority.cli_cases.filter((item) => {
    return item.name === "authoring_cli_stdout";
  });
  assert.equal(matches.length, 1);
  const expected = matches[0]!.artifacts;
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.replaceAll(data.root, "<FIXTURE_ROOT>"), expected.stdout);
  assert.equal(result.stderr.replaceAll(data.root, "<FIXTURE_ROOT>"), expected.stderr);
});

test("source evidence CLI can invoke the AST producer without Python", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const bin = path.join(data.root, "bin");
  const output = path.join(data.root, "generated-eval");
  await mkdir(bin);
  const fakeGo = path.join(bin, "go");
  await writeFile(fakeGo, [
    "#!/bin/sh",
    "out=''",
    "while [ \"$#\" -gt 0 ]; do",
    "  if [ \"$1\" = '--out' ]; then out=$2; shift 2; else shift; fi",
    "done",
    "cp \"$FAKE_SOURCE_FACTS\" \"$out\"",
  ].join("\n"), "utf8");
  await chmod(fakeGo, 0o755);
  const result = runNode([
    "source-evidence-eval", "--schema", data.schema, "--openapi", data.openApi,
    "--source-root", data.source, "--resource-prefix", "example",
    "--ast-tool-dir", data.root, "--out-dir", output,
  ], {
    ...process.env,
    FAKE_SOURCE_FACTS: data.facts,
    PATH: `${bin}${path.delimiter}${process.env.PATH ?? ""}`,
    PYTHON: path.join(bin, "python-must-not-run"),
  });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(
    JSON.parse(await readFile(path.join(output, "source-facts.json"), "utf8")),
    JSON.parse(await readFile(data.facts, "utf8")),
  );
});

test("authoring Make targets use Node when Python is unavailable", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const cli = `${process.execPath} ${CLI}`;
  const python = path.join(data.root, "python-must-not-run");
  const cases = [
    ["reconcile", `RESOURCE=example_widget`, `IN=${data.api}`, `SCHEMA=${data.schema}`],
    ["openapi-map", `SCHEMA=${data.schema}`, `OPENAPI=${data.openApi}`, "RESOURCE_PREFIX=example", "API_PREFIX=/"],
    [
      "source-operation-map", `SCHEMA=${data.schema}`, `OPENAPI=${data.openApi}`,
      `SOURCE_ROOT=${data.source}`, "RESOURCE_PREFIX=example",
    ],
    [
      "source-evidence-eval", `SCHEMA=${data.schema}`, `OPENAPI=${data.openApi}`,
      `SOURCE_ROOT=${data.source}`, `SOURCE_FACTS=${data.facts}`,
      `OUT_DIR=${path.join(data.root, "make-eval")}`, "RESOURCE_PREFIX=example",
    ],
  ];
  for (const arguments_ of cases) {
    const result = spawnSync("make", [
      "--no-print-directory", ...arguments_, `INFRAWRIGHT_CLI=${cli}`, `PYTHON=${python}`,
    ], { cwd: ROOT, encoding: "utf8", env: process.env });
    assert.equal(result.status, 0, `${arguments_[0]}: ${result.stderr}`);
  }
});
