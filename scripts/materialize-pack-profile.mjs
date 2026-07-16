#!/usr/bin/env node

import { parseArgs } from "node:util";
import {
  cp,
  lstat,
  mkdir,
  readFile,
  readdir,
  rm,
} from "node:fs/promises";
import path from "node:path";

const PACK_SET_KEYS = ["kind", "packs", "shared", "version"];
const PACK_SET_KIND = "infrawright.pack-set";
const COMPONENT_NAME = /^[a-z0-9][a-z0-9_-]*$/u;

function fail(message) {
  throw new Error(message);
}

function object(value, label) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    fail(`${label} must be an object`);
  }
  return value;
}

function names(value, label) {
  if (!Array.isArray(value)) fail(`${label} must be a list`);
  const result = [];
  const seen = new Set();
  for (const [index, item] of value.entries()) {
    if (typeof item !== "string" || !COMPONENT_NAME.test(item)) {
      fail(`${label}[${index}] must be a lowercase pack name`);
    }
    if (seen.has(item)) fail(`${label} duplicates ${JSON.stringify(item)}`);
    seen.add(item);
    result.push(item);
  }
  const sorted = [...result].sort();
  if (result.some((item, index) => item !== sorted[index])) {
    fail(`${label} must be sorted`);
  }
  return result;
}

async function readJson(filename, label = filename) {
  let value;
  try {
    value = JSON.parse(await readFile(filename, "utf8"));
  } catch (error) {
    fail(`${label} is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
  return value;
}

async function loadProfile(filename) {
  const value = object(await readJson(filename), filename);
  const keys = Object.keys(value).sort();
  const missing = PACK_SET_KEYS.filter((key) => !Object.hasOwn(value, key));
  const unknown = keys.filter((key) => !PACK_SET_KEYS.includes(key));
  if (missing.length > 0) fail(`${filename} is missing keys: ${missing.join(", ")}`);
  if (unknown.length > 0) fail(`${filename} has unknown keys: ${unknown.join(", ")}`);
  if (value.kind !== PACK_SET_KIND) {
    fail(`${filename}.kind must be ${JSON.stringify(PACK_SET_KIND)}`);
  }
  if (value.version !== 1) fail(`${filename}.version must be 1`);
  return {
    packs: names(value.packs, `${filename}.packs`),
    shared: names(value.shared, `${filename}.shared`),
  };
}

async function requireDirectory(candidate, label) {
  let status;
  try {
    status = await lstat(candidate);
  } catch (error) {
    if (error && typeof error === "object" && error.code === "ENOENT") {
      fail(`${label} does not exist: ${candidate}`);
    }
    throw error;
  }
  if (status.isSymbolicLink()) fail(`${label} must not be a symbolic link: ${candidate}`);
  if (!status.isDirectory()) fail(`${label} is not a directory: ${candidate}`);
}

async function rejectSymlinks(root, label) {
  const entries = (await readdir(root, { withFileTypes: true }))
    .sort((left, right) => left.name.localeCompare(right.name));
  for (const entry of entries) {
    const candidate = path.join(root, entry.name);
    const status = await lstat(candidate);
    if (status.isSymbolicLink()) {
      fail(`${label} contains unsafe symbolic link: ${candidate}`);
    }
    if (status.isDirectory()) await rejectSymlinks(candidate, label);
  }
}

async function validatePackManifest(packsRoot, pack, selectedShared) {
  const filename = path.join(packsRoot, pack, "pack.json");
  const manifest = object(await readJson(filename, filename), filename);
  if (!Object.hasOwn(manifest, "requires_shared")) return;
  const required = names(manifest.requires_shared, `${filename}.requires_shared`);
  const missing = required.filter((name) => !selectedShared.has(name));
  if (missing.length > 0) {
    fail(`pack ${pack} requires unselected shared component(s): ${missing.join(", ")}`);
  }
}

async function validateSelection(packsRoot, profile) {
  await requireDirectory(packsRoot, "packs root");
  if (profile.shared.length > 0) {
    await requireDirectory(path.join(packsRoot, "_shared"), "shared packs root");
  }
  for (const pack of profile.packs) {
    await requireDirectory(path.join(packsRoot, pack), `selected pack ${pack}`);
  }
  for (const shared of profile.shared) {
    await requireDirectory(
      path.join(packsRoot, "_shared", shared),
      `selected shared component ${shared}`,
    );
  }
  for (const pack of profile.packs) {
    await rejectSymlinks(path.join(packsRoot, pack), `selected pack ${pack}`);
  }
  for (const shared of profile.shared) {
    await rejectSymlinks(
      path.join(packsRoot, "_shared", shared),
      `selected shared component ${shared}`,
    );
  }
  const selectedShared = new Set(profile.shared);
  for (const pack of profile.packs) {
    await validatePackManifest(packsRoot, pack, selectedShared);
  }
}

function inside(candidate, parent) {
  const relative = path.relative(parent, candidate);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== "..");
}

async function copyProfile(packsRoot, destination, profile) {
  await validateSelection(packsRoot, profile);
  if (inside(destination, packsRoot) || inside(packsRoot, destination)) {
    fail(`copy destination must not overlap the source packs root: ${destination}`);
  }
  await mkdir(destination, { recursive: true });
  for (const pack of profile.packs) {
    await cp(path.join(packsRoot, pack), path.join(destination, pack), {
      errorOnExist: true,
      force: false,
      recursive: true,
    });
  }
  if (profile.shared.length > 0) {
    const sharedDestination = path.join(destination, "_shared");
    await mkdir(sharedDestination);
    for (const shared of profile.shared) {
      await cp(
        path.join(packsRoot, "_shared", shared),
        path.join(sharedDestination, shared),
        { errorOnExist: true, force: false, recursive: true },
      );
    }
  }
}

async function pruneProfile(packsRoot, profile) {
  await validateSelection(packsRoot, profile);
  await rejectSymlinks(packsRoot, "packs root");
  const selectedPacks = new Set(profile.packs);
  const entries = await readdir(packsRoot, { withFileTypes: true });
  for (const entry of entries) {
    if (entry.isDirectory() && entry.name !== "_shared" && !selectedPacks.has(entry.name)) {
      await rm(path.join(packsRoot, entry.name), { force: true, recursive: true });
    }
  }

  const sharedRoot = path.join(packsRoot, "_shared");
  if (profile.shared.length === 0) {
    await rm(sharedRoot, { force: true, recursive: true });
    return;
  }
  const selectedShared = new Set(profile.shared);
  const sharedEntries = await readdir(sharedRoot, { withFileTypes: true });
  for (const entry of sharedEntries) {
    if (entry.isDirectory() && !selectedShared.has(entry.name)) {
      await rm(path.join(sharedRoot, entry.name), { force: true, recursive: true });
    }
  }
}

function requiredOption(values, name) {
  const value = values[name];
  if (typeof value !== "string" || value.trim() === "") {
    fail(`--${name} is required`);
  }
  return path.resolve(value);
}

async function main() {
  const parsed = parseArgs({
    allowPositionals: true,
    options: {
      destination: { type: "string" },
      profile: { type: "string" },
      "packs-root": { type: "string" },
    },
    strict: true,
  });
  if (parsed.positionals.length !== 1 || !["copy", "prune"].includes(parsed.positionals[0])) {
    fail("usage: materialize-pack-profile.mjs <copy|prune> --profile FILE --packs-root DIR [--destination DIR]");
  }
  const mode = parsed.positionals[0];
  const profilePath = requiredOption(parsed.values, "profile");
  const packsRoot = requiredOption(parsed.values, "packs-root");
  const profile = await loadProfile(profilePath);
  if (mode === "copy") {
    await copyProfile(packsRoot, requiredOption(parsed.values, "destination"), profile);
  } else {
    if (parsed.values.destination !== undefined) fail("--destination is only valid with copy");
    await pruneProfile(packsRoot, profile);
  }
}

main().catch((error) => {
  process.stderr.write(`error: ${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
