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
  symlink,
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

function runExpectedFailure(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, {
    cwd: options.cwd ?? repository,
    encoding: "utf8",
    env: options.env ?? process.env,
    maxBuffer: 32 * 1024 * 1024,
    timeout: options.timeout ?? 120_000,
  });
  if (result.status === 0) {
    throw new Error(`${command} ${arguments_.join(" ")} unexpectedly succeeded`);
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
const gitExecutable = run("sh", ["-c", "command -v git"]).stdout.trim();
if (gitExecutable.length === 0) throw new Error("release staging smoke requires git");
if (run(gitExecutable, ["status", "--porcelain", "--untracked-files=no"]).stdout !== "") {
  throw new Error("release staging smoke requires a committed Slice-4 tree");
}
const makeExecutable = run("sh", ["-c", "command -v make"]).stdout.trim();
if (makeExecutable.length === 0) throw new Error("release staging smoke requires make");

const commit = run(gitExecutable, ["rev-parse", "HEAD"]).stdout.trim();
const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-runtime-release-"));
const archive = path.join(temporary, "slice.tar");
const stage = path.join(temporary, "archive");
const generic = path.join(temporary, "generic-runtime");
const intercept = path.join(temporary, "intercept");

try {
  await mkdir(stage);
  run(gitExecutable, ["archive", "--format=tar", `--output=${archive}`, commit]);
  run("tar", ["-xf", archive, "-C", stage]);
  run("npm", ["ci", "--ignore-scripts"], { cwd: stage, timeout: 240_000 });
  run("npm", ["run", "build"], { cwd: stage, timeout: 240_000 });

  const packed = JSON.parse(run("npm", [
    "pack",
    "--ignore-scripts",
    "--json",
    "--silent",
    "--pack-destination",
    temporary,
  ], { cwd: stage }).stdout);
  if (
    !Array.isArray(packed)
    || packed.length !== 1
    || typeof packed[0] !== "object"
    || packed[0] === null
  ) {
    throw new Error("npm pack did not describe exactly one runtime package");
  }
  const packedFilename = packed[0].filename;
  if (
    typeof packedFilename !== "string"
    || packedFilename.length === 0
    || path.basename(packedFilename) !== packedFilename
  ) {
    throw new Error("npm pack returned an unsafe runtime package filename");
  }
  const packedPaths = new Set(
    Array.isArray(packed[0].files)
      ? packed[0].files.map((file) => file?.path).filter((file) => typeof file === "string")
      : [],
  );
  for (const required of [
    "dist/infrawright-cli.mjs",
    "dist/infrawright-cli.mjs.sha256",
  ]) {
    if (!packedPaths.has(required)) {
      throw new Error(`npm package omits required runtime file ${required}`);
    }
  }
  const packedArchive = path.join(temporary, packedFilename);
  const installedPrefix = path.join(temporary, "installed-cli");
  run("npm", [
    "install",
    "--global",
    "--ignore-scripts",
    "--no-audit",
    "--no-fund",
    "--no-package-lock",
    "--prefix",
    installedPrefix,
    packedArchive,
  ], { cwd: temporary, timeout: 240_000 });
  const iwHelp = run(path.join(installedPrefix, "bin", "iw"), ["--help"], {
    cwd: temporary,
  });
  const compatibilityHelp = run(
    path.join(installedPrefix, "bin", "infrawright"),
    ["--help"],
    { cwd: temporary },
  );
  if (iwHelp.stderr !== "" || compatibilityHelp.stderr !== "") {
    throw new Error("installed CLI aliases wrote unexpected help diagnostics");
  }
  if (iwHelp.stdout !== compatibilityHelp.stdout) {
    throw new Error("installed CLI aliases do not expose identical help output");
  }
  if (
    !iwHelp.stdout.startsWith("usage:\n")
    || !iwHelp.stdout.includes("  iw check-pack")
    || /^  infrawright /mu.test(iwHelp.stdout)
  ) {
    throw new Error("installed CLI help does not present iw as the canonical command");
  }

  await rm(path.join(stage, "node_modules"), { force: true, recursive: true });
  await rm(path.join(stage, ".node-test"), { force: true, recursive: true });
  run(process.execPath, [path.join(stage, "scripts", "verify-runtime-release.mjs"), stage], {
    cwd: temporary,
  });

  await mkdir(generic);
  for (const relative of [
    "Makefile",
    "package.json",
    "deployment.json",
    "demo",
    "packs",
    "packsets",
  ]) {
    await cp(path.join(stage, relative), path.join(generic, relative), {
      recursive: true,
    });
  }
  await mkdir(path.join(generic, "dist"));
  await mkdir(path.join(generic, "scripts"));
  for (const relative of [
    "dist/infrawright-cli.mjs",
    "dist/infrawright-cli.mjs.sha256",
    "scripts/verify-runtime-release.mjs",
  ]) {
    await cp(path.join(stage, relative), path.join(generic, relative));
  }
  await removePythonFiles(generic);
  await Promise.all([
    mustBeAbsent(path.join(generic, "node_modules")),
    mustBeAbsent(path.join(generic, "node-src")),
    mustBeAbsent(path.join(generic, "node-tests")),
    mustBeAbsent(path.join(generic, "package-lock.json")),
    mustBeAbsent(path.join(generic, "tsconfig.json")),
    mustBeAbsent(path.join(generic, "tsconfig.test.json")),
    mustBeAbsent(path.join(generic, "engine")),
    mustBeAbsent(path.join(generic, "catalogs")),
    mustBeAbsent(path.join(generic, "dist", "infrawright.mjs")),
    mustBeAbsent(path.join(generic, "dist", "infrawright-zcc-collector-child.mjs")),
  ]);

  run(gitExecutable, ["init", "--quiet"], { cwd: generic });
  run(gitExecutable, ["config", "user.email", "runtime-smoke@example.invalid"], { cwd: generic });
  run(gitExecutable, ["config", "user.name", "Runtime Smoke"], { cwd: generic });
  run(gitExecutable, ["add", "demo/config/demo", "demo/imports/demo"], { cwd: generic });
  run(gitExecutable, ["commit", "--quiet", "-m", "runtime smoke baseline"], { cwd: generic });

  await mkdir(intercept);
  const forbiddenToolLog = path.join(temporary, "forbidden-tool-invoked");
  const shim = `#!/bin/sh\nprintf '%s\\n' "$0 $*" >> ${shellLiteral(forbiddenToolLog)}\nexit 97\n`;
  for (const name of [
    "npm",
    "npm-must-not-run",
    "npx",
    "npx-must-not-run",
    "python",
    "python3",
    "python-must-not-run",
  ]) {
    const file = path.join(intercept, name);
    await writeFile(file, shim, "utf8");
    await chmod(file, 0o755);
  }
  const gitShim = path.join(intercept, "git");
  await writeFile(gitShim, `#!/bin/sh\nexec ${shellLiteral(gitExecutable)} "$@"\n`, "utf8");
  await chmod(gitShim, 0o755);
  const findExecutable = run("sh", ["-c", "command -v find"]).stdout.trim();
  if (findExecutable.length === 0) throw new Error("release staging smoke requires find");
  const findShim = path.join(intercept, "find");
  await writeFile(findShim, `#!/bin/sh\nexec ${shellLiteral(findExecutable)} "$@"\n`, "utf8");
  await chmod(findShim, 0o755);
  const terraform = path.join(intercept, "terraform");
  await writeFile(
    terraform,
    "#!/bin/sh\nif [ \"$1\" = fmt ]; then\n  if [ \"${2-}\" = - ]; then /bin/cat; fi\n  exit 0\nfi\nexit 96\n",
    "utf8",
  );
  await chmod(terraform, 0o755);
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
  run(makeExecutable, [
    "--no-print-directory",
    "--silent",
    "verify-runtime",
    `NODE=${process.execPath}`,
    `NPM=${path.join(intercept, "npm-must-not-run")}`,
    `PYTHON=${path.join(intercept, "python-must-not-run")}`,
    "SHELL=/bin/sh",
    "DEPLOYMENT=deployment.json",
    "PACK_PROFILE=packsets/full.json",
    "PACK_CATALOG=packsets/full.json",
  ], { cwd: generic, env: environment });
  const verifier = path.join(generic, "scripts", "verify-runtime-release.mjs");
  const externalProfiles = path.join(temporary, "external-profile");
  await mkdir(externalProfiles);
  await cp(
    path.join(generic, "packsets", "full.json"),
    path.join(externalProfiles, "selected.json"),
  );
  await cp(
    path.join(generic, "deployment.json"),
    path.join(externalProfiles, "unrelated.json"),
  );
  run(process.execPath, [
    verifier,
    generic,
    "--profile",
    path.join(externalProfiles, "selected.json"),
  ], { cwd: temporary, env: environment });

  await writeFile(path.join(generic, "packsets", "broken.json"), "{}\n", "utf8");
  const brokenProfile = runExpectedFailure(process.execPath, [verifier, generic], {
    cwd: temporary,
    env: environment,
  });
  if (!brokenProfile.stderr.includes("packsets/broken.json is not a subset")) {
    throw new Error(`unexpected malformed-profile diagnostic: ${brokenProfile.stderr}`);
  }
  await rm(path.join(generic, "packsets", "broken.json"));

  await cp(path.join(generic, "package.json"), path.join(generic, "dist", "package.json"));
  const shadowRoot = runExpectedFailure(process.execPath, [verifier, generic], {
    cwd: temporary,
    env: environment,
  });
  if (!shadowRoot.stderr.includes("package root different from the verified runtime root")) {
    throw new Error(`unexpected shadow-package diagnostic: ${shadowRoot.stderr}`);
  }
  await rm(path.join(generic, "dist", "package.json"));

  const externalCli = runExpectedFailure(process.execPath, [
    verifier,
    generic,
    "--cli",
    path.join(stage, "dist", "infrawright-cli.mjs"),
  ], { cwd: temporary, env: environment });
  if (!externalCli.stderr.includes("unknown argument --cli")) {
    throw new Error(`unexpected incoherent-selector diagnostic: ${externalCli.stderr}`);
  }

  const runtimeCli = path.join(generic, "dist", "infrawright-cli.mjs");
  await rm(runtimeCli);
  await symlink(path.join(stage, "dist", "infrawright-cli.mjs"), runtimeCli);
  const symlinkedCli = runExpectedFailure(process.execPath, [verifier, generic], {
    cwd: temporary,
    env: environment,
  });
  if (!symlinkedCli.stderr.includes("infrawright-cli.mjs must not be a symbolic link")) {
    throw new Error(`unexpected symlinked-CLI diagnostic: ${symlinkedCli.stderr}`);
  }
  await rm(runtimeCli);
  await cp(path.join(stage, "dist", "infrawright-cli.mjs"), runtimeCli);
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
  const makeResult = run(makeExecutable, [
    "--no-print-directory",
    "--silent",
    "resources",
    "OVERLAY=",
    `NODE=${process.execPath}`,
    `NPM=${path.join(intercept, "npm-must-not-run")}`,
    "SHELL=/bin/sh",
    `PACK_PROFILE=${path.join(generic, "packsets", "full.json")}`,
    `PACK_CATALOG=${path.join(generic, "packsets", "full.json")}`,
    "RESOURCE=zia_url_categories",
  ], { cwd: generic, env: environment });
  if (makeResult.stdout !== "zia_url_categories\n") {
    throw new Error(`unexpected no-install Make output: ${makeResult.stdout}`);
  }
  const demoResult = run(makeExecutable, [
    "--no-print-directory",
    "--silent",
    "demo-contract",
    `NODE=${process.execPath}`,
    `NPM=${path.join(intercept, "npm-must-not-run")}`,
    `PYTHON=${path.join(intercept, "python-must-not-run")}`,
    `TF=${terraform}`,
    "SHELL=/bin/sh",
    "OVERLAY=demo",
    "DEPLOYMENT=demo/deployment.json",
    "DEMO_DEPLOYMENT=demo/deployment.json",
    "PACK_PROFILE=packsets/full.json",
    "PACK_CATALOG=packsets/full.json",
  ], { cwd: generic, env: environment, timeout: 240_000 });
  if (!demoResult.stdout.includes("demo-contract: committed demo config/imports and generated modules are in sync")) {
    throw new Error(`unexpected stripped demo-contract output: ${demoResult.stdout}`);
  }
  await Promise.all([
    access(path.join(generic, "demo", "modules", "default", "zia_url_categories", "main.tf")),
    access(path.join(generic, "demo", "config", "demo", "zia_url_categories.auto.tfvars.json")),
    access(path.join(generic, "demo", "imports", "demo", "zia_url_categories_imports.tf")),
  ]);
  await mustBeAbsent(forbiddenToolLog);
  process.stdout.write(`local runtime release smoke passed for ${commit}\n`);
} finally {
  if (process.env.INFRAWRIGHT_KEEP_RELEASE_SMOKE !== "1") {
    await rm(temporary, { force: true, recursive: true });
  } else {
    process.stderr.write(`retained release smoke at ${temporary}\n`);
  }
}
