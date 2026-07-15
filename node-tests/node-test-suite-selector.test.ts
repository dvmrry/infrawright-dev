import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();
const SELECTOR = path.join(ROOT, "scripts", "run-node-test-suite.mjs");

function run(
  mode: "check" | "list" | "run",
  directory: string,
  extra: readonly string[] = [],
) {
  const environment = { ...process.env };
  delete environment.NODE_TEST_CONTEXT;
  return spawnSync(process.execPath, [
    SELECTOR,
    mode,
    "--compiled-dir",
    directory,
    ...extra,
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment,
  });
}

async function compiledFixture(files: Readonly<Record<string, string>>): Promise<string> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-suite-"));
  const directory = path.join(root, "node-tests");
  await mkdir(directory);
  await Promise.all(Object.entries(files).map(([name, source]) => {
    return writeFile(path.join(directory, name), source, "utf8");
  }));
  return directory;
}

test("check discovers an honest Python-oracle file split", async (context) => {
  const directory = await compiledFixture({
    "import-oracle.test.js": "import test from 'node:test';\ntest('pure', () => {});\n",
    "migration.test.js": "import { PYTHON_ORACLE } from './python-oracle.js';\nvoid PYTHON_ORACLE;\n",
    "operational-runtime-smoke.test.js": "import test from 'node:test';\ntest('pure', () => {});\n",
  });
  context.after(async () => rm(path.dirname(directory), { recursive: true, force: true }));
  const result = run("check", directory, ["--json"]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, "");
  assert.deepEqual(JSON.parse(result.stdout), {
    excluded: [{ name: "migration.test.js", reason: "imports-python-oracle" }],
    excluded_count: 1,
    selected: [
      "import-oracle.test.js",
      "operational-runtime-smoke.test.js",
    ],
    selected_count: 2,
    total_count: 3,
  });
});

test("run executes selected files and never evaluates excluded oracle files", async (context) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-suite-run-"));
  const directory = path.join(root, "node-tests");
  const marker = path.join(root, "selected");
  const forbidden = path.join(root, "excluded");
  await mkdir(directory);
  await writeFile(path.join(directory, "selected.test.js"), [
    "import test from 'node:test';",
    "import { writeFileSync } from 'node:fs';",
    `test('selected', () => writeFileSync(${JSON.stringify(marker)}, 'yes'));`,
  ].join("\n"), "utf8");
  await writeFile(path.join(directory, "excluded.test.js"), [
    "import { PYTHON_ORACLE } from './python-oracle.js';",
    "import { writeFileSync } from 'node:fs';",
    `writeFileSync(${JSON.stringify(forbidden)}, PYTHON_ORACLE);`,
  ].join("\n"), "utf8");
  context.after(async () => rm(root, { recursive: true, force: true }));

  const result = run("run", directory);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /selected=1 excluded_python_oracle=1 total=2/u);
  assert.equal(await readFile(marker, "utf8"), "yes");
  await assert.rejects(readFile(forbidden, "utf8"), /ENOENT/u);
});

test("selected files with hardcoded Python subprocesses fail before execution", async (context) => {
  const directory = await compiledFixture({
    "unsafe.test.js": [
      "import { spawnSync } from 'node:child_process';",
      "spawnSync('python3', ['-c', 'pass']);",
    ].join("\n"),
  });
  context.after(async () => rm(path.dirname(directory), { recursive: true, force: true }));
  const result = run("run", directory);
  assert.equal(result.status, 1);
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /unsafe\.test\.js: selected Node-only test contains/u);
});

test("repository discovery naturally selects the operational smoke and Oracle tests", () => {
  const directory = path.join(ROOT, ".node-test", "node-tests");
  const result = run("check", directory, ["--json"]);
  assert.equal(result.status, 0, result.stderr);
  const report = JSON.parse(result.stdout) as {
    readonly excluded_count: number;
    readonly selected: readonly string[];
    readonly selected_count: number;
    readonly total_count: number;
  };
  assert.ok(report.selected.includes("import-oracle.test.js"));
  assert.ok(report.selected.includes("operational-runtime-smoke.test.js"));
  assert.ok(report.selected.includes("node-test-suite-selector.test.js"));
  assert.ok(report.selected_count > 0);
  assert.ok(report.excluded_count > 0);
  assert.equal(report.selected_count + report.excluded_count, report.total_count);
});
