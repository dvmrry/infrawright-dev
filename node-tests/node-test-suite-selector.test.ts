import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
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
  requirements?: string,
) {
  const environment = { ...process.env };
  delete environment.NODE_TEST_CONTEXT;
  return spawnSync(process.execPath, [
    SELECTOR,
    mode,
    "--compiled-dir",
    directory,
    ...(requirements === undefined ? [] : ["--requirements", requirements]),
    ...extra,
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment,
  });
}

async function compiledFixture(files: Readonly<Record<string, string>>): Promise<{
  readonly directory: string;
  readonly requirements: string;
}> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-suite-"));
  const directory = path.join(root, "node-tests");
  const requirements = path.join(root, "requirements.json");
  await mkdir(directory);
  await Promise.all(Object.entries(files).map(([name, source]) => {
    return writeFile(path.join(directory, name), source, "utf8");
  }));
  await writeFile(requirements, JSON.stringify({
    kind: "infrawright.node-test-pack-requirements",
    rules: [],
    version: 1,
  }), "utf8");
  return { directory, requirements };
}

test("check discovers an honest Python-oracle file split", async (context) => {
  const fixture = await compiledFixture({
    "import-oracle.test.js": "import test from 'node:test';\ntest('pure', () => {});\n",
    "migration.test.js": "import { PYTHON_ORACLE } from './python-oracle.js';\nvoid PYTHON_ORACLE;\n",
    "operational-runtime-smoke.test.js": "import test from 'node:test';\ntest('pure', () => {});\n",
  });
  context.after(async () => rm(path.dirname(fixture.directory), { recursive: true, force: true }));
  const result = run("check", fixture.directory, ["--json"], fixture.requirements);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, "");
  assert.deepEqual(JSON.parse(result.stdout), {
    excluded: [{ name: "migration.test.js", reason: "imports-python-oracle" }],
    excluded_count: 1,
    excluded_missing_pack_requirements_count: 0,
    excluded_python_oracle_count: 1,
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

  const requirements = path.join(root, "requirements.json");
  await writeFile(requirements, JSON.stringify({
    kind: "infrawright.node-test-pack-requirements",
    rules: [],
    version: 1,
  }), "utf8");
  const result = run("run", directory, [], requirements);
  assert.equal(result.status, 0, result.stderr);
  assert.match(
    result.stdout,
    /selected=1 excluded_python_oracle=1 excluded_missing_pack_requirements=0 total=2/u,
  );
  assert.equal(await readFile(marker, "utf8"), "yes");
  await assert.rejects(readFile(forbidden, "utf8"), /ENOENT/u);
});

test("selected files with hardcoded Python subprocesses fail before execution", async (context) => {
  const fixture = await compiledFixture({
    "unsafe.test.js": [
      "import { spawnSync } from 'node:child_process';",
      "spawnSync('python3', ['-c', 'pass']);",
    ].join("\n"),
  });
  context.after(async () => rm(path.dirname(fixture.directory), { recursive: true, force: true }));
  const result = run("run", fixture.directory, [], fixture.requirements);
  assert.equal(result.status, 1);
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /unsafe\.test\.js: selected Node-only test contains/u);
});

test("pack requirements exclude only declared files and never evaluate them", async (context) => {
  const fixture = await compiledFixture({
    "core.test.js": "import test from 'node:test';\ntest('core', () => {});\n",
    "zia.test.js": "throw new Error('pack-coupled file was evaluated');\n",
  });
  const root = path.dirname(fixture.directory);
  const profile = path.join(root, "profile.json");
  const catalog = path.join(root, "catalog.json");
  context.after(async () => rm(root, { recursive: true, force: true }));
  await writeFile(profile, JSON.stringify({
    kind: "infrawright.pack-set",
    packs: [],
    shared: [],
    version: 1,
  }), "utf8");
  await writeFile(catalog, JSON.stringify({
    kind: "infrawright.pack-set",
    packs: ["zia"],
    shared: ["zscaler"],
    version: 1,
  }), "utf8");
  await writeFile(fixture.requirements, JSON.stringify({
    kind: "infrawright.node-test-pack-requirements",
    rules: [{
      file: "zia.test.js",
      packs: ["zia"],
      reason: "fixture requires ZIA",
      shared: ["zscaler"],
    }],
    version: 1,
  }), "utf8");

  const result = run("run", fixture.directory, [
    "--profile", profile,
    "--catalog", catalog,
    "--json",
  ], fixture.requirements);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /"name": "zia\.test\.js"/u);
  assert.match(result.stdout, /"reason": "missing-pack-requirements"/u);
  assert.match(result.stdout, /"selected_count": 1/u);
  assert.match(result.stdout, /"excluded_missing_pack_requirements_count": 1/u);
});

test("stale pack requirements fail closed before running tests", async (context) => {
  const fixture = await compiledFixture({
    "core.test.js": "import test from 'node:test';\ntest('core', () => {});\n",
  });
  context.after(async () => rm(path.dirname(fixture.directory), { recursive: true, force: true }));
  await writeFile(fixture.requirements, JSON.stringify({
    kind: "infrawright.node-test-pack-requirements",
    rules: [{
      file: "missing.test.js",
      packs: ["zia"],
      reason: "stale fixture",
      shared: [],
    }],
    version: 1,
  }), "utf8");
  const result = run("check", fixture.directory, [], fixture.requirements);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /targets stale or missing file missing\.test\.js/u);
});

test("an entirely pack-excluded suite succeeds without invoking test discovery", async (context) => {
  const fixture = await compiledFixture({
    "zia.test.js": "throw new Error('excluded file was evaluated');\n",
  });
  const root = path.dirname(fixture.directory);
  const profile = path.join(root, "profile.json");
  const catalog = path.join(root, "catalog.json");
  context.after(async () => rm(root, { recursive: true, force: true }));
  await writeFile(profile, JSON.stringify({
    kind: "infrawright.pack-set", packs: [], shared: [], version: 1,
  }), "utf8");
  await writeFile(catalog, JSON.stringify({
    kind: "infrawright.pack-set", packs: ["zia"], shared: [], version: 1,
  }), "utf8");
  await writeFile(fixture.requirements, JSON.stringify({
    kind: "infrawright.node-test-pack-requirements",
    rules: [{
      file: "zia.test.js", packs: ["zia"], reason: "fixture", shared: [],
    }],
    version: 1,
  }), "utf8");
  const result = run("run", fixture.directory, [
    "--profile", profile, "--catalog", catalog,
  ], fixture.requirements);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /selected=0/u);
});

test("repository discovery naturally selects the operational smoke and Oracle tests", () => {
  const directory = path.join(ROOT, ".node-test", "node-tests");
  const result = run("check", directory, [
    "--profile", path.join(ROOT, "packsets", "full.json"),
    "--catalog", path.join(ROOT, "packsets", "full.json"),
    "--json",
  ]);
  assert.equal(result.status, 0, result.stderr);
  const report = JSON.parse(result.stdout) as {
    readonly excluded: readonly {
      readonly name: string;
      readonly reason: string;
    }[];
    readonly excluded_count: number;
    readonly excluded_missing_pack_requirements_count: number;
    readonly excluded_python_oracle_count: number;
    readonly selected: readonly string[];
    readonly selected_count: number;
    readonly total_count: number;
  };
  assert.ok(report.selected.includes("import-oracle.test.js"));
  assert.ok(report.selected.includes("operational-runtime-smoke.test.js"));
  assert.ok(report.selected.includes("node-test-suite-selector.test.js"));
  assert.ok(report.selected.includes("provider-probe-parity.test.js"));
  assert.ok(report.selected.includes("provider-probe.test.js"));
  for (const name of [
    "adopt-runner.test.js",
    "authoring-cli.test.js",
    "authoring-openapi-resource-map.test.js",
    "authoring-reconcile-schema-api.test.js",
    "authoring-sdk-path-evidence.test.js",
    "authoring-source-operation-map.test.js",
    "drift-policy.test.js",
    "environment-generator.test.js",
    "exact-plan-apply.test.js",
    "import-staging.test.js",
    "import-moves-differential.test.js",
    "json.test.js",
    "paths.test.js",
    "plan-eval.test.js",
    "plan-fingerprint.test.js",
    "plan-report.test.js",
    "python-lossless-artifact.test.js",
    "python-lower-151.test.js",
    "rest-collector-python-parity.test.js",
    "transform-adopt-parity.test.js",
    "zpa-provider-evidence.test.js",
    "zscaler-assessment.test.js",
  ]) {
    assert.ok(report.selected.includes(name), name);
  }
  assert.ok(report.selected.includes("rest-collector.test.js"));
  assert.ok(report.selected.includes("zscaler-generic-fetch.test.js"));
  const allTests = readdirSync(directory)
    .filter((name) => name.endsWith(".test.js"))
    .sort();
  const selected = [...report.selected].sort();
  const excluded = report.excluded.map((entry) => entry.name).sort();
  assert.deepEqual([...selected, ...excluded].sort(), allTests);
  assert.equal(new Set([...selected, ...excluded]).size, allTests.length);
  assert.equal(report.selected_count, report.selected.length);
  assert.equal(report.excluded_count, report.excluded.length);
  assert.equal(report.total_count, allTests.length);
  assert.equal(
    report.excluded_missing_pack_requirements_count,
    report.excluded.filter((entry) => {
      return entry.reason === "missing-pack-requirements";
    }).length,
  );
  const oracleImports = allTests.filter((name) => {
    return /^import .* from ["']\.\/python-oracle\.js["'];?$/mu.test(
      readFileSync(path.join(directory, name), "utf8"),
    );
  });
  const oracleExclusions = report.excluded.filter((entry) => {
    return entry.reason === "imports-python-oracle";
  }).map((entry) => entry.name).sort();
  assert.deepEqual(oracleExclusions, oracleImports);
  assert.equal(report.excluded_python_oracle_count, oracleImports.length);

  const reducedResult = run("check", directory, [
    "--profile", path.join(ROOT, "packsets", "zia.json"),
    "--catalog", path.join(ROOT, "packsets", "full.json"),
    "--json",
  ]);
  assert.equal(reducedResult.status, 0, reducedResult.stderr);
  const reduced = JSON.parse(reducedResult.stdout) as {
    readonly excluded: readonly {
      readonly name: string;
      readonly reason: string;
    }[];
    readonly selected: readonly string[];
  };
  for (const name of [
    "authoring-cli.test.js",
    "authoring-reconcile-schema-api.test.js",
    "authoring-sdk-path-evidence.test.js",
    "authoring-source-operation-map.test.js",
    "drift-policy.test.js",
    "import-moves-differential.test.js",
    "json.test.js",
    "paths.test.js",
    "plan-eval.test.js",
    "plan-fingerprint.test.js",
    "plan-report.test.js",
    "python-lossless-artifact.test.js",
    "python-lower-151.test.js",
    "rest-collector-python-parity.test.js",
  ]) {
    assert.ok(reduced.selected.includes(name), name);
  }
  for (const name of [
    "adopt-runner.test.js",
    "authoring-openapi-resource-map.test.js",
    "environment-generator.test.js",
    "exact-plan-apply.test.js",
    "import-staging.test.js",
    "transform-adopt-parity.test.js",
    "zpa-provider-evidence.test.js",
    "zscaler-assessment.test.js",
  ]) {
    assert.ok(reduced.excluded.some((entry) => {
      return entry.name === name && entry.reason === "missing-pack-requirements";
    }), name);
  }

  const zpaResult = run("check", directory, [
    "--profile", path.join(ROOT, "packsets", "zpa.json"),
    "--catalog", path.join(ROOT, "packsets", "full.json"),
    "--json",
  ]);
  assert.equal(zpaResult.status, 0, zpaResult.stderr);
  const zpa = JSON.parse(zpaResult.stdout) as {
    readonly selected: readonly string[];
  };
  assert.ok(zpa.selected.includes("zpa-provider-evidence.test.js"));
});
