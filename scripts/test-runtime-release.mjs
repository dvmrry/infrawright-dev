#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import {
  access,
  chmod,
  cp,
  mkdtemp,
  mkdir,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function shellLiteral(value) {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function run(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, {
    cwd: options.cwd ?? repository,
    encoding: "utf8",
    env: options.env ?? process.env,
    maxBuffer: 32 * 1024 * 1024,
    timeout: options.timeout ?? 120_000,
  });
  if (result.status !== 0) {
    throw new Error(
      `${command} ${arguments_.join(" ")} exited ${String(result.status)}:\n${result.stdout}${result.stderr}`,
    );
  }
  return result;
}

async function removePythonFiles(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const candidate = path.join(directory, entry.name);
    if (entry.isDirectory()) await removePythonFiles(candidate);
    else if (entry.isFile() && entry.name.endsWith(".py")) await rm(candidate);
  }
}

async function mustBeAbsent(file) {
  try {
    await access(file);
  } catch {
    return;
  }
  throw new Error(`unexpected runtime dependency remains: ${file}`);
}

if (process.platform === "win32") {
  throw new Error("the local release staging smoke requires a macOS/Linux release host");
}
if (run("git", ["status", "--porcelain", "--untracked-files=no"]).stdout !== "") {
  throw new Error("release staging smoke requires a committed Slice-4 tree");
}

const commit = run("git", ["rev-parse", "HEAD"]).stdout.trim();
const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-runtime-release-"));
const archive = path.join(temporary, "slice.tar");
const stage = path.join(temporary, "archive");
const generic = path.join(temporary, "generic-runtime");
const intercept = path.join(temporary, "intercept");

try {
  await mkdir(stage);
  run("git", ["archive", "--format=tar", `--output=${archive}`, commit]);
  run("tar", ["-xf", archive, "-C", stage]);
  run("npm", ["ci", "--ignore-scripts"], { cwd: stage, timeout: 240_000 });
  run("npm", ["run", "build"], { cwd: stage, timeout: 240_000 });
  await rm(path.join(stage, "node_modules"), { force: true, recursive: true });
  await rm(path.join(stage, ".node-test"), { force: true, recursive: true });
  run(process.execPath, [path.join(stage, "scripts", "verify-runtime-release.mjs"), stage], {
    cwd: temporary,
  });

  await cp(stage, generic, { recursive: true });
  await Promise.all([
    rm(path.join(generic, "engine"), { force: true, recursive: true }),
    rm(path.join(generic, "catalogs"), { force: true, recursive: true }),
    rm(path.join(generic, "dist", "infrawright.mjs"), { force: true }),
    rm(path.join(generic, "dist", "infrawright.mjs.sha256"), { force: true }),
    rm(path.join(generic, "dist", "infrawright-zcc-collector-child.mjs"), { force: true }),
    rm(path.join(generic, "dist", "infrawright-zcc-collector-child.mjs.sha256"), { force: true }),
  ]);
  await removePythonFiles(generic);
  await Promise.all([
    mustBeAbsent(path.join(generic, "node_modules")),
    mustBeAbsent(path.join(generic, "engine")),
    mustBeAbsent(path.join(generic, "catalogs")),
    mustBeAbsent(path.join(generic, "dist", "infrawright.mjs")),
    mustBeAbsent(path.join(generic, "dist", "infrawright-zcc-collector-child.mjs")),
  ]);

  await mkdir(intercept);
  const pythonLog = path.join(temporary, "python-invoked");
  const shim = `#!/bin/sh\nprintf '%s\\n' "$0 $*" >> ${shellLiteral(pythonLog)}\nexit 97\n`;
  for (const name of ["python", "python3", "python-must-not-run"]) {
    const file = path.join(intercept, name);
    await writeFile(file, shim, "utf8");
    await chmod(file, 0o755);
  }
  const environment = {
    ...process.env,
    INFRAWRIGHT_DEPLOYMENT: "",
    INFRAWRIGHT_PACKS: "",
    INFRAWRIGHT_PACK_PROFILE: "",
    NODE_PATH: "",
    PATH: intercept,
    PYTHON: path.join(intercept, "python-must-not-run"),
    PYTHONPATH: "",
  };
  run(process.execPath, [
    path.join(generic, "scripts", "verify-runtime-release.mjs"),
    generic,
  ], { cwd: temporary, env: environment });
  const resourceResult = run(process.execPath, [
    path.join(generic, "dist", "infrawright-cli.mjs"),
    "resources",
    "--order=references",
    "--resource",
    "zia_url_categories",
    "--root",
    path.join(generic, "packs"),
    "--profile",
    path.join(generic, "packsets", "full.json"),
    "--catalog",
    path.join(generic, "packsets", "full.json"),
  ], { cwd: temporary, env: environment });
  if (resourceResult.stdout !== "zia_url_categories\n") {
    throw new Error(`unexpected relocated resource output: ${resourceResult.stdout}`);
  }
  await mustBeAbsent(pythonLog);
  process.stdout.write(`local runtime release smoke passed for ${commit}\n`);
} finally {
  if (process.env.INFRAWRIGHT_KEEP_RELEASE_SMOKE !== "1") {
    await rm(temporary, { force: true, recursive: true });
  } else {
    process.stderr.write(`retained release smoke at ${temporary}\n`);
  }
}
