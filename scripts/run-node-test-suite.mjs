#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const REPOSITORY = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const MODES = new Set(["check", "list", "run"]);
const PYTHON_ORACLE_IMPORT = /^[\t ]*(?:import\b|[}]?[\t ]*from\b)[^\r\n]*["']\.\/python-oracle\.js["']/mu;
const HARDCODED_PYTHON_SUBPROCESS = /^(?![\t ]*(?:\/\/|\*))[^\r\n"'`]*(?:\b|\.)(?:execFile|execFileSync|spawn|spawnSync)\s*\(\s*["'`]python(?:3(?:\.\d+)*)?["'`]/mu;

function usage(message) {
  if (message !== undefined) process.stderr.write(`error: ${message}\n`);
  process.stderr.write(
    "usage: node scripts/run-node-test-suite.mjs [run|check|list] "
    + "[--compiled-dir <dir>] [--json] [-- <node --test option> ...]\n",
  );
  process.exit(message === undefined ? 0 : 2);
}

function parseArguments(arguments_) {
  let mode = "run";
  let compiledDirectory = path.join(REPOSITORY, ".node-test", "node-tests");
  let json = false;
  let forwarded = [];
  let index = 0;
  if (arguments_[0] !== undefined && !arguments_[0].startsWith("-")) {
    mode = arguments_[0];
    if (!MODES.has(mode)) usage(`unknown mode ${mode}`);
    index = 1;
  }
  while (index < arguments_.length) {
    const argument = arguments_[index];
    if (argument === "--") {
      forwarded = arguments_.slice(index + 1);
      index = arguments_.length;
    } else if (argument === "--compiled-dir") {
      const value = arguments_[index + 1];
      if (value === undefined || value.length === 0) {
        usage("--compiled-dir requires a value");
      }
      compiledDirectory = path.resolve(value);
      index += 2;
    } else if (argument === "--json") {
      json = true;
      index += 1;
    } else if (argument === "--help" || argument === "-h") {
      usage();
    } else {
      usage(`unknown argument ${String(argument)}`);
    }
  }
  if (mode !== "run" && forwarded.length > 0) {
    usage("forwarded node --test options are valid only in run mode");
  }
  return { compiledDirectory, forwarded, json, mode };
}

async function discover(compiledDirectory) {
  let entries;
  try {
    entries = await readdir(compiledDirectory, { withFileTypes: true });
  } catch (error) {
    throw new Error(
      `unable to read compiled Node tests at ${compiledDirectory}: `
      + `${error instanceof Error ? error.message : String(error)}`,
    );
  }
  const files = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".test.js"))
    .map((entry) => entry.name)
    .sort();
  if (files.length === 0) {
    throw new Error(`no compiled *.test.js files found at ${compiledDirectory}`);
  }

  const selected = [];
  const excluded = [];
  const violations = [];
  for (const name of files) {
    const file = path.join(compiledDirectory, name);
    const source = await readFile(file, "utf8");
    if (PYTHON_ORACLE_IMPORT.test(source)) {
      excluded.push({ file, name, reason: "imports-python-oracle" });
      continue;
    }
    selected.push({ file, name });
    if (HARDCODED_PYTHON_SUBPROCESS.test(source)) {
      violations.push({ file, name, reason: "hardcoded-python-subprocess" });
    }
  }
  return { excluded, selected, total: files.length, violations };
}

function reportFor(discovery) {
  return {
    excluded: discovery.excluded.map(({ name, reason }) => ({ name, reason })),
    excluded_count: discovery.excluded.length,
    selected: discovery.selected.map(({ name }) => name),
    selected_count: discovery.selected.length,
    total_count: discovery.total,
  };
}

function renderSummary(discovery) {
  return `node-test-suite: selected=${discovery.selected.length} `
    + `excluded_python_oracle=${discovery.excluded.length} total=${discovery.total}`;
}

function renderViolations(violations) {
  return violations.map(({ name }) => {
    return `${name}: selected Node-only test contains a hardcoded Python subprocess invocation`;
  }).join("\n");
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  let discovery;
  try {
    discovery = await discover(options.compiledDirectory);
  } catch (error) {
    process.stderr.write(`node-test-suite: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exit(1);
  }
  if (discovery.violations.length > 0) {
    process.stderr.write(`${renderViolations(discovery.violations)}\n`);
    process.exit(1);
  }

  if (options.json) {
    process.stdout.write(`${JSON.stringify(reportFor(discovery), null, 2)}\n`);
  } else if (options.mode === "list") {
    for (const { name } of discovery.selected) process.stdout.write(`selected ${name}\n`);
    for (const { name } of discovery.excluded) process.stdout.write(`excluded ${name}\n`);
    process.stdout.write(`${renderSummary(discovery)}\n`);
  } else {
    process.stdout.write(`${renderSummary(discovery)}\n`);
  }

  if (options.mode !== "run") return;
  const result = spawnSync(process.execPath, [
    "--test",
    ...options.forwarded,
    ...discovery.selected.map(({ file }) => file),
  ], {
    cwd: REPOSITORY,
    env: { ...process.env, INFRAWRIGHT_NODE_ONLY_TESTS: "1" },
    stdio: "inherit",
  });
  if (result.error !== undefined) {
    process.stderr.write(`node-test-suite: unable to start Node test runner: ${result.error.message}\n`);
    process.exit(1);
  }
  process.exit(result.status ?? 1);
}

await main();
