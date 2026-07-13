#!/usr/bin/env node

import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { access, readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";

function fail(message) {
  process.stderr.write(`runtime release verification failed: ${message}\n`);
  process.exit(1);
}

async function requireFile(root, relative) {
  const file = path.join(root, relative);
  let metadata;
  try {
    metadata = await stat(file);
  } catch {
    return fail(`missing ${relative}`);
  }
  if (!metadata.isFile()) fail(`${relative} is not a regular file`);
  return file;
}

function runCli(root, ...arguments_) {
  const cli = path.join(root, "dist", "infrawright-cli.mjs");
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

const root = path.resolve(process.argv[2] ?? ".");
const packageFile = await requireFile(root, "package.json");
await requireFile(root, "package-lock.json");
await requireFile(root, "README.md");
await requireFile(root, "LICENSE");
await requireFile(root, "deployment.json");
const cli = await requireFile(root, "dist/infrawright-cli.mjs");
const checksumFile = await requireFile(root, "dist/infrawright-cli.mjs.sha256");
const fullProfileFile = await requireFile(root, "packsets/full.json");

const packageDocument = JSON.parse(await readFile(packageFile, "utf8"));
if (packageDocument.engines?.node !== ">=24 <25") {
  fail("package.json must require Node >=24 <25");
}
if (packageDocument.bin?.infrawright !== "dist/infrawright-cli.mjs") {
  fail("package.json must expose dist/infrawright-cli.mjs as infrawright");
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

const fullProfile = JSON.parse(await readFile(fullProfileFile, "utf8"));
if (
  fullProfile.kind !== "infrawright.pack-set"
  || fullProfile.version !== 1
  || !Array.isArray(fullProfile.packs)
  || !Array.isArray(fullProfile.shared)
) {
  fail("packsets/full.json is not a version-1 pack set");
}
for (const pack of fullProfile.packs) {
  await requireFile(root, `packs/${pack}/pack.json`);
}
for (const component of fullProfile.shared) {
  try {
    await access(path.join(root, "packs", "_shared", component));
  } catch {
    fail(`missing packs/_shared/${component}`);
  }
}

const profileNames = (await readdir(path.join(root, "packsets")))
  .filter((name) => name.endsWith(".json"))
  .sort();
if (profileNames.length === 0) fail("release contains no pack profiles");
const knownPacks = new Set(fullProfile.packs);
const knownShared = new Set(fullProfile.shared);
for (const name of profileNames) {
  const relative = `packsets/${name}`;
  const document = JSON.parse(await readFile(await requireFile(root, relative), "utf8"));
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

runCli(root, "--help");
runCli(root, "check-pack", "--root", path.join(root, "packs"));
runCli(
  root,
  "check-pack-set",
  "--root",
  path.join(root, "packs"),
  "--profile",
  fullProfileFile,
  "--catalog",
  fullProfileFile,
);
runCli(
  root,
  "deployment",
  "--deployment",
  path.join(root, "deployment.json"),
  "module-dir",
);

process.stdout.write(
  `generic runtime release verified: ${root} (${profileNames.length} profiles)\n`,
);
