import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { PYTHON_ORACLE } from "./python-oracle.js";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");

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
        block: { attributes: { name: { required: true, type: "string" } } },
      },
    },
  });
  await jsonFile(api, [{ name: "example" }]);
  await jsonFile(openApi, {
    openapi: "3.0.3",
    paths: {
      "/widgets": {
        get: { operationId: "ListWidgets" },
        post: { operationId: "CreateWidget" },
      },
      "/widgets/{id}": { get: { operationId: "GetWidget" } },
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

function runPython(module: string, arguments_: readonly string[]) {
  return spawnSync(PYTHON_ORACLE, ["-m", module, ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
    env: process.env,
  });
}

test("authoring CLI reports remain byte-compatible with Python", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const comparisons = [
    {
      node: ["reconcile", "example_widget", "--api", data.api, "--schema", data.schema],
      python: ["example_widget", "--api", data.api, "--schema", data.schema],
      module: "engine.reconcile_schema_api",
    },
    {
      node: [
        "openapi-map", "--schema", data.schema, "--openapi", data.openApi,
        "--resource-prefix", "example", "--api-prefix", "/",
      ],
      python: [
        "--schema", data.schema, "--openapi", data.openApi,
        "--resource-prefix", "example", "--api-prefix", "/",
      ],
      module: "engine.openapi_resource_map",
    },
    {
      node: [
        "source-operation-map", "--schema", data.schema, "--openapi", data.openApi,
        "--source-root", data.source, "--resource-prefix", "example",
        "--source-facts", data.facts,
      ],
      python: [
        "--schema", data.schema, "--openapi", data.openApi,
        "--source-root", data.source, "--resource-prefix", "example",
        "--source-facts", data.facts,
      ],
      module: "engine.source_operation_map",
    },
  ] as const;
  for (const comparison of comparisons) {
    const node = runNode(comparison.node);
    const python = runPython(comparison.module, comparison.python);
    assert.equal(node.status, 0, node.stderr);
    assert.equal(python.status, 0, python.stderr);
    assert.equal(node.stderr, python.stderr, comparison.module);
    assert.equal(node.stdout, python.stdout, comparison.module);
  }
});

test("source evidence evaluation writes the Python-compatible artifact set", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const nodeOutput = path.join(data.root, "node-eval");
  const pythonOutput = path.join(data.root, "python-eval");
  const common = [
    "--schema", data.schema, "--openapi", data.openApi,
    "--source-root", data.source, "--resource-prefix", "example",
    "--source-facts", data.facts,
  ];
  const node = runNode(["source-evidence-eval", ...common, "--out-dir", nodeOutput]);
  const python = runPython(
    "engine.source_evidence_eval",
    [...common, "--out-dir", pythonOutput],
  );
  assert.equal(node.status, 0, node.stderr);
  assert.equal(python.status, 0, python.stderr);
  for (const filename of [
    "ast-report.json", "control-report.json", "source-facts-compare.json",
    "source-evidence-eval.md",
  ]) {
    assert.equal(
      await readFile(path.join(nodeOutput, filename), "utf8"),
      await readFile(path.join(pythonOutput, filename), "utf8"),
      filename,
    );
  }
  const nodeEvaluation = JSON.parse(node.stdout) as Record<string, unknown>;
  const pythonEvaluation = JSON.parse(python.stdout) as Record<string, unknown>;
  delete nodeEvaluation.artifacts;
  delete pythonEvaluation.artifacts;
  assert.deepEqual(nodeEvaluation, pythonEvaluation);
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
