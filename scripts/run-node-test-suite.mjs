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
const PACK_SET_KIND = "infrawright.pack-set";
const REQUIREMENTS_KIND = "infrawright.node-test-pack-requirements";
const DEFAULT_CATALOG = path.join(REPOSITORY, "packsets", "full.json");
const DEFAULT_PROFILE = path.join(REPOSITORY, "packsets", "full.json");
const DEFAULT_REQUIREMENTS = path.join(
  REPOSITORY,
  "node-tests",
  "pack-test-requirements.json",
);
const COMPONENT_NAME = /^[a-z0-9][a-z0-9_-]*$/u;
const PYTHON_ORACLE_IMPORT = /^[\t ]*(?:import\b|[}]?[\t ]*from\b)[^\r\n]*["']\.\/python-oracle\.js["']/mu;
const HARDCODED_PYTHON_SUBPROCESS = /^(?![\t ]*(?:\/\/|\*))[^\r\n"'`]*(?:\b|\.)(?:execFile|execFileSync|spawn|spawnSync)\s*\(\s*["'`]python(?:3(?:\.\d+)*)?["'`]/mu;

function usage(message) {
  if (message !== undefined) process.stderr.write(`error: ${message}\n`);
  process.stderr.write(
    "usage: node scripts/run-node-test-suite.mjs [run|check|list] "
    + "[--compiled-dir <dir>] [--profile <file>] [--catalog <file>] "
    + "[--requirements <file>] [--json] [-- <node --test option> ...]\n",
  );
  process.exit(message === undefined ? 0 : 2);
}

function parseArguments(arguments_) {
  let mode = "run";
  let compiledDirectory = path.join(REPOSITORY, ".node-test", "node-tests");
  let profile = path.resolve(
    process.env.PACK_PROFILE?.trim() || DEFAULT_PROFILE,
  );
  let catalog = path.resolve(
    process.env.PACK_CATALOG?.trim() || DEFAULT_CATALOG,
  );
  let requirements = DEFAULT_REQUIREMENTS;
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
    } else if (
      argument === "--profile"
      || argument === "--catalog"
      || argument === "--requirements"
    ) {
      const value = arguments_[index + 1];
      if (value === undefined || value.length === 0) {
        usage(`${argument} requires a value`);
      }
      const resolved = path.resolve(value);
      if (argument === "--profile") profile = resolved;
      if (argument === "--catalog") catalog = resolved;
      if (argument === "--requirements") requirements = resolved;
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
  return {
    catalog,
    compiledDirectory,
    forwarded,
    json,
    mode,
    profile,
    requirements,
  };
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function exactKeys(value, expected, label) {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(`${label} must contain exactly: ${wanted.join(", ")}`);
  }
}

function stringList(value, label) {
  if (!Array.isArray(value)) throw new Error(`${label} must be an array`);
  let previous;
  for (const item of value) {
    if (typeof item !== "string" || !COMPONENT_NAME.test(item)) {
      throw new Error(`${label} entries must be lowercase component names`);
    }
    if (previous !== undefined && previous >= item) {
      throw new Error(`${label} entries must be sorted and unique`);
    }
    previous = item;
  }
  return value;
}

async function jsonDocument(file, label) {
  let source;
  try {
    source = await readFile(file, "utf8");
  } catch (error) {
    throw new Error(
      `unable to read ${label} at ${file}: `
      + `${error instanceof Error ? error.message : String(error)}`,
    );
  }
  let value;
  try {
    value = JSON.parse(source);
  } catch (error) {
    throw new Error(
      `invalid JSON in ${label} at ${file}: `
      + `${error instanceof Error ? error.message : String(error)}`,
    );
  }
  if (!isRecord(value)) throw new Error(`${label} at ${file} must be an object`);
  return value;
}

async function packSet(file, label) {
  const value = await jsonDocument(file, label);
  exactKeys(value, ["kind", "packs", "shared", "version"], label);
  if (value.kind !== PACK_SET_KIND || value.version !== 1) {
    throw new Error(`${label} must be ${PACK_SET_KIND} version 1`);
  }
  return {
    packs: stringList(value.packs, `${label}.packs`),
    shared: stringList(value.shared, `${label}.shared`),
  };
}

function missingItems(required, active) {
  const installed = new Set(active);
  return required.filter((item) => !installed.has(item));
}

async function loadSelection(options, files, pythonOracleFiles) {
  const [profile, catalog, document] = await Promise.all([
    packSet(options.profile, "pack profile"),
    packSet(options.catalog, "pack catalog"),
    jsonDocument(options.requirements, "Node test requirements"),
  ]);
  const missingProfilePacks = missingItems(profile.packs, catalog.packs);
  const missingProfileShared = missingItems(profile.shared, catalog.shared);
  if (missingProfilePacks.length > 0 || missingProfileShared.length > 0) {
    throw new Error(
      "pack profile is outside the catalog: "
      + `packs=${missingProfilePacks.join(",") || "none"}; `
      + `shared=${missingProfileShared.join(",") || "none"}`,
    );
  }

  exactKeys(document, ["kind", "rules", "version"], "Node test requirements");
  if (document.kind !== REQUIREMENTS_KIND || document.version !== 1) {
    throw new Error(`Node test requirements must be ${REQUIREMENTS_KIND} version 1`);
  }
  if (!Array.isArray(document.rules)) {
    throw new Error("Node test requirements.rules must be an array");
  }

  const available = new Set(files);
  const pythonOracle = new Set(pythonOracleFiles);
  const rules = new Map();
  let previous;
  for (const [index, rule] of document.rules.entries()) {
    const label = `Node test requirements.rules[${index}]`;
    if (!isRecord(rule)) throw new Error(`${label} must be an object`);
    exactKeys(rule, ["file", "packs", "reason", "shared"], label);
    if (
      typeof rule.file !== "string"
      || path.basename(rule.file) !== rule.file
      || !/^[A-Za-z0-9][A-Za-z0-9._-]*\.test\.js$/u.test(rule.file)
    ) {
      throw new Error(`${label}.file must be an exact compiled *.test.js basename`);
    }
    if (previous !== undefined && previous >= rule.file) {
      throw new Error("Node test requirement files must be sorted and unique");
    }
    previous = rule.file;
    const packs = stringList(rule.packs, `${label}.packs`);
    const shared = stringList(rule.shared, `${label}.shared`);
    if (packs.length === 0 && shared.length === 0) {
      throw new Error(`${label} must require at least one pack or shared component`);
    }
    if (typeof rule.reason !== "string" || rule.reason.trim().length === 0) {
      throw new Error(`${label}.reason must be a nonempty string`);
    }
    const unknownPacks = missingItems(packs, catalog.packs);
    const unknownShared = missingItems(shared, catalog.shared);
    if (unknownPacks.length > 0 || unknownShared.length > 0) {
      throw new Error(
        `${label} names components outside the catalog: `
        + `packs=${unknownPacks.join(",") || "none"}; `
        + `shared=${unknownShared.join(",") || "none"}`,
      );
    }
    if (!available.has(rule.file)) {
      throw new Error(`${label} targets stale or missing file ${rule.file}`);
    }
    if (pythonOracle.has(rule.file)) {
      throw new Error(`${label} redundantly targets a Python-oracle test`);
    }
    rules.set(rule.file, { packs, shared });
  }
  return { profile, rules };
}

async function discover(options) {
  const { compiledDirectory } = options;
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

  const sources = new Map();
  const pythonOracleFiles = [];
  for (const name of files) {
    const file = path.join(compiledDirectory, name);
    const source = await readFile(file, "utf8");
    sources.set(name, { file, source });
    if (PYTHON_ORACLE_IMPORT.test(source)) {
      pythonOracleFiles.push(name);
    }
  }

  const selection = await loadSelection(options, files, pythonOracleFiles);
  const pythonOracle = new Set(pythonOracleFiles);
  const selected = [];
  const excluded = [];
  const violations = [];
  for (const name of files) {
    const { file, source } = sources.get(name);
    if (pythonOracle.has(name)) {
      excluded.push({ file, name, reason: "imports-python-oracle" });
      continue;
    }
    const rule = selection.rules.get(name);
    if (rule !== undefined) {
      const missingPacks = missingItems(rule.packs, selection.profile.packs);
      const missingShared = missingItems(rule.shared, selection.profile.shared);
      if (missingPacks.length > 0 || missingShared.length > 0) {
        excluded.push({ file, name, reason: "missing-pack-requirements" });
        continue;
      }
    }
    selected.push({ file, name });
    if (HARDCODED_PYTHON_SUBPROCESS.test(source)) {
      violations.push({ file, name, reason: "hardcoded-python-subprocess" });
    }
  }
  return { excluded, selected, total: files.length, violations };
}

function reportFor(discovery) {
  const pythonOracleCount = discovery.excluded.filter(({ reason }) => {
    return reason === "imports-python-oracle";
  }).length;
  const missingPackCount = discovery.excluded.filter(({ reason }) => {
    return reason === "missing-pack-requirements";
  }).length;
  return {
    excluded: discovery.excluded.map(({ name, reason }) => ({ name, reason })),
    excluded_count: discovery.excluded.length,
    excluded_missing_pack_requirements_count: missingPackCount,
    excluded_python_oracle_count: pythonOracleCount,
    selected: discovery.selected.map(({ name }) => name),
    selected_count: discovery.selected.length,
    total_count: discovery.total,
  };
}

function renderSummary(discovery) {
  const report = reportFor(discovery);
  return `node-test-suite: selected=${discovery.selected.length} `
    + `excluded_python_oracle=${report.excluded_python_oracle_count} `
    + `excluded_missing_pack_requirements=${report.excluded_missing_pack_requirements_count} `
    + `total=${discovery.total}`;
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
    discovery = await discover(options);
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
  if (discovery.selected.length === 0) return;
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
