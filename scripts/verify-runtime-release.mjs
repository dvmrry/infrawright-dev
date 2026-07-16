#!/usr/bin/env node

import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { access, lstat, readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";

function fail(message) {
  process.stderr.write(`runtime release verification failed: ${message}\n`);
  process.exit(1);
}

async function requireFile(file, label) {
  let metadata;
  try {
    metadata = await stat(file);
  } catch {
    return fail(`missing ${label}`);
  }
  if (!metadata.isFile()) fail(`${label} is not a regular file`);
  return file;
}

function runCli(root, cli, ...arguments_) {
  const result = spawnSync(process.execPath, [cli, ...arguments_], {
    cwd: path.dirname(root),
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: "",
      INFRAWRIGHT_PACKS: "",
      INFRAWRIGHT_PACK_PROFILE: "",
      NODE_PATH: "",
      PYTHON: path.join(root, ".python-must-not-run"),
      PYTHONPATH: "",
    },
    maxBuffer: 8 * 1024 * 1024,
    timeout: 30_000,
  });
  if (result.status !== 0) {
    fail(
      `CLI ${arguments_.join(" ")} exited ${String(result.status)}:\n${result.stdout}${result.stderr}`,
    );
  }
  return result;
}

function parseArguments(arguments_) {
  let root = ".";
  const selected = {};
  for (let index = 0; index < arguments_.length;) {
    const argument = arguments_[index];
    if (!argument.startsWith("-") && index === 0) {
      root = argument;
      index += 1;
      continue;
    }
    if (
      argument === "--root"
      || argument === "--profile"
      || argument === "--catalog"
      || argument === "--deployment"
    ) {
      const value = arguments_[index + 1];
      if (value === undefined || value.length === 0) {
        fail(`${argument} requires a value`);
      }
      if (argument === "--root") root = value;
      else selected[argument.slice(2)] = value;
      index += 2;
      continue;
    }
    fail(`unknown argument ${argument}`);
  }
  return { root: path.resolve(root), selected };
}

function selectedPath(root, value, fallback) {
  return path.resolve(root, value ?? fallback);
}

const parsed = parseArguments(process.argv.slice(2));
const root = parsed.root;
const packageFile = await requireFile(
  path.join(root, "package.json"),
  "package.json package-root metadata",
);
const deploymentFile = await requireFile(
  selectedPath(root, parsed.selected.deployment, "deployment.json"),
  "deployment input",
);
const cli = await requireFile(
  path.join(root, "dist", "infrawright-cli.mjs"),
  "dist/infrawright-cli.mjs",
);
if ((await lstat(cli)).isSymbolicLink()) {
  fail("dist/infrawright-cli.mjs must not be a symbolic link");
}
const checksumFile = await requireFile(
  path.join(root, "dist", "infrawright-cli.mjs.sha256"),
  "dist/infrawright-cli.mjs.sha256",
);
const packsRoot = path.join(root, "packs");
const profileFile = await requireFile(
  selectedPath(root, parsed.selected.profile, "packsets/full.json"),
  "pack profile",
);
const catalogFile = await requireFile(
  selectedPath(root, parsed.selected.catalog, "packsets/full.json"),
  "pack catalog",
);
const releaseCatalogFile = await requireFile(
  path.join(root, "packsets", "full.json"),
  "release pack catalog",
);

const packageDocument = JSON.parse(await readFile(packageFile, "utf8"));
if (packageDocument.engines?.node !== ">=24 <25") {
  fail("package.json must require Node >=24 <25");
}
if (packageDocument.bin?.iw !== "dist/infrawright-cli.mjs") {
  fail("package.json must expose dist/infrawright-cli.mjs as iw");
}
if (path.resolve(root, packageDocument.bin.iw) !== cli) {
  fail("package.json iw binary does not resolve to the verified CLI");
}
if (packageDocument.bin?.infrawright !== "dist/infrawright-cli.mjs") {
  fail("package.json must retain infrawright as a compatibility alias");
}
let discoveredRoot = path.dirname(cli);
while (true) {
  try {
    await access(path.join(discoveredRoot, "package.json"));
    break;
  } catch {
    const parent = path.dirname(discoveredRoot);
    if (parent === discoveredRoot) {
      fail("generic CLI cannot discover its package root");
    }
    discoveredRoot = parent;
  }
}
if (discoveredRoot !== root) {
  fail("generic CLI discovers a package root different from the verified runtime root");
}
if (Number(process.versions.node.split(".")[0]) !== 24) {
  fail(`verification requires Node 24, found ${process.versions.node}`);
}

const checksum = await readFile(checksumFile, "ascii");
const match = /^([0-9a-f]{64})  infrawright-cli\.mjs\n$/u.exec(checksum);
if (match === null) fail("generic CLI checksum has an invalid format");
const actual = createHash("sha256").update(await readFile(cli)).digest("hex");
if (match?.[1] !== actual) fail("generic CLI checksum does not match the bundle");
if (process.platform !== "win32" && ((await stat(cli)).mode & 0o111) === 0) {
  fail("generic CLI bundle is not executable");
}

const fullProfile = JSON.parse(await readFile(releaseCatalogFile, "utf8"));
if (
  fullProfile.kind !== "infrawright.pack-set"
  || fullProfile.version !== 1
  || !Array.isArray(fullProfile.packs)
  || !Array.isArray(fullProfile.shared)
) {
  fail("packsets/full.json is not a version-1 pack set");
}
for (const pack of fullProfile.packs) {
  await requireFile(path.join(packsRoot, pack, "pack.json"), `pack ${pack}`);
}
for (const component of fullProfile.shared) {
  try {
    await access(path.join(packsRoot, "_shared", component));
  } catch {
    fail(`missing packs/_shared/${component}`);
  }
}

const profilesRoot = path.join(root, "packsets");
const profileNames = (await readdir(profilesRoot))
  .filter((name) => name.endsWith(".json"))
  .sort();
if (profileNames.length === 0) fail("release contains no pack profiles");
const knownPacks = new Set(fullProfile.packs);
const knownShared = new Set(fullProfile.shared);
for (const name of profileNames) {
  const relative = `packsets/${name}`;
  const document = JSON.parse(await readFile(
    await requireFile(path.join(profilesRoot, name), relative),
    "utf8",
  ));
  if (
    document.kind !== "infrawright.pack-set"
    || document.version !== 1
    || !Array.isArray(document.packs)
    || !Array.isArray(document.shared)
    || document.packs.some((item) => !knownPacks.has(item))
    || document.shared.some((item) => !knownShared.has(item))
  ) {
    fail(`${relative} is not a subset of the full version-1 pack catalog`);
  }
}

const help = runCli(root, cli, "--help");
for (const command of [
  "fetch",
  "adopt",
  "gen-env",
  "stage-imports",
  "plan",
  "assert-adoptable",
  "apply",
]) {
  if (!help.stdout.includes(command)) {
    fail(`CLI help is missing operational command ${command}`);
  }
}
const defaultResources = runCli(
  root,
  cli,
  "resources",
  "--order=references",
  "--resource",
  "zia_url_categories",
);
if (defaultResources.stdout !== "zia_url_categories\n") {
  fail("CLI default package-root inputs returned unexpected resources");
}
runCli(root, cli, "check-pack", "--root", packsRoot);
runCli(
  root,
  cli,
  "check-pack-set",
  "--root",
  packsRoot,
  "--profile",
  profileFile,
  "--catalog",
  catalogFile,
);
runCli(
  root,
  cli,
  "deployment",
  "--deployment",
  deploymentFile,
  "module-dir",
);

process.stdout.write(
  `generic runtime release verified: ${root} (${profileNames.length} profiles)\n`,
);
