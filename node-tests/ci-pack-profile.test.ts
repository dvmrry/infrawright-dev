import assert from "node:assert/strict";
import {
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const ROOT = process.cwd();
const SCRIPT = path.join(ROOT, "scripts", "materialize-pack-profile.mjs");

async function writeJson(filename: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(filename), { recursive: true });
  await writeFile(filename, `${JSON.stringify(value)}\n`, "utf8");
}

async function makePack(root: string, name: string, required: string[] = []): Promise<void> {
  await writeJson(path.join(root, name, "pack.json"), {
    ...(required.length === 0 ? {} : { requires_shared: required }),
  });
  await writeFile(path.join(root, name, "payload.txt"), `${name}\n`, "utf8");
}

function run(...args: string[]) {
  return spawnSync(process.execPath, [SCRIPT, ...args], {
    cwd: ROOT,
    encoding: "utf8",
    env: { ...process.env, PYTHON: "/usr/bin/false" },
  });
}

test("CI pack profile helper copies only selected packs and shared components", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-pack-copy-"));
  try {
    const packs = path.join(directory, "packs");
    const destination = path.join(directory, "selected");
    const profile = path.join(directory, "profile.json");
    await makePack(packs, "one", ["common"]);
    await makePack(packs, "two");
    await mkdir(path.join(packs, "_shared", "common"), { recursive: true });
    await writeFile(path.join(packs, "_shared", "common", "shared.txt"), "shared\n");
    await mkdir(path.join(packs, "_shared", "unused"), { recursive: true });
    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["one"],
      shared: ["common"],
    });

    const result = run("copy", "--profile", profile, "--packs-root", packs, "--destination", destination);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual((await readdir(destination)).sort(), ["_shared", "one"]);
    assert.equal(await readFile(path.join(destination, "one", "payload.txt"), "utf8"), "one\n");
    assert.deepEqual((await readdir(path.join(destination, "_shared"))).sort(), ["common"]);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("CI pack profile helper prunes a checkout to the exact selection", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-pack-prune-"));
  try {
    const packs = path.join(directory, "packs");
    const profile = path.join(directory, "profile.json");
    await makePack(packs, "one");
    await makePack(packs, "two");
    await mkdir(path.join(packs, "_shared", "common"), { recursive: true });
    await mkdir(path.join(packs, "_shared", "unused"), { recursive: true });
    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["two"],
      shared: ["common"],
    });

    const result = run("prune", "--profile", profile, "--packs-root", packs);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual((await readdir(packs)).sort(), ["_shared", "two"]);
    assert.deepEqual((await readdir(path.join(packs, "_shared"))).sort(), ["common"]);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("CI pack profile helper validates profiles before mutating files", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-pack-invalid-"));
  try {
    const packs = path.join(directory, "packs");
    const profile = path.join(directory, "profile.json");
    await makePack(packs, "one", ["common"]);
    await makePack(packs, "two");
    await mkdir(path.join(packs, "_shared", "common"), { recursive: true });
    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["one"],
      shared: [],
    });

    const result = run("prune", "--profile", profile, "--packs-root", packs);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /requires unselected shared component\(s\): common/u);
    assert.deepEqual((await readdir(packs)).sort(), ["_shared", "one", "two"]);

    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["../one"],
      shared: [],
    });
    const traversal = run("prune", "--profile", profile, "--packs-root", packs);
    assert.equal(traversal.status, 1);
    assert.match(traversal.stderr, /must be a lowercase pack name/u);
    assert.deepEqual((await readdir(packs)).sort(), ["_shared", "one", "two"]);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("CI pack profile helper rejects nested selected-pack and shared symlinks before copy", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-pack-copy-symlink-"));
  try {
    const packs = path.join(directory, "packs");
    const profile = path.join(directory, "profile.json");
    const destination = path.join(directory, "selected");
    const outside = path.join(directory, "outside.txt");
    await writeFile(outside, "outside\n");
    await makePack(packs, "one", ["common"]);
    await mkdir(path.join(packs, "one", "nested"));
    await symlink(outside, path.join(packs, "one", "nested", "unsafe"));
    await mkdir(path.join(packs, "_shared", "common", "nested"), { recursive: true });
    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["one"],
      shared: ["common"],
    });

    const packResult = run(
      "copy", "--profile", profile, "--packs-root", packs, "--destination", destination,
    );
    assert.equal(packResult.status, 1);
    assert.match(packResult.stderr, /selected pack one contains unsafe symbolic link/u);
    await assert.rejects(() => readdir(destination), { code: "ENOENT" });

    await rm(path.join(packs, "one", "nested", "unsafe"));
    await symlink(outside, path.join(packs, "_shared", "common", "nested", "unsafe"));
    const sharedResult = run(
      "copy", "--profile", profile, "--packs-root", packs, "--destination", destination,
    );
    assert.equal(sharedResult.status, 1);
    assert.match(
      sharedResult.stderr,
      /selected shared component common contains unsafe symbolic link/u,
    );
    await assert.rejects(() => readdir(destination), { code: "ENOENT" });
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("CI pack profile helper preflights the complete tree before prune mutation", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "iw-pack-prune-symlink-"));
  try {
    const packs = path.join(directory, "packs");
    const profile = path.join(directory, "profile.json");
    const outside = path.join(directory, "outside");
    await makePack(packs, "one");
    await makePack(packs, "unselected");
    await mkdir(outside);
    await symlink(outside, path.join(packs, "_shared"));
    await writeJson(profile, {
      kind: "infrawright.pack-set",
      version: 1,
      packs: ["one"],
      shared: [],
    });

    const result = run("prune", "--profile", profile, "--packs-root", packs);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /packs root contains unsafe symbolic link/u);
    assert.deepEqual((await readdir(packs)).sort(), ["_shared", "one", "unselected"]);
    assert.equal(await readFile(path.join(packs, "unselected", "payload.txt"), "utf8"), "unselected\n");
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});
