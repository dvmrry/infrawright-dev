import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadDeployment } from "../node-src/domain/deployment.js";

test("deployment loader preserves the Python missing and empty defaults", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "deployment-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    const emptyLoaded = await loadDeployment(deployment);
    assert.equal(emptyLoaded.overlay, ".");
    assert.equal(emptyLoaded.module_dir, undefined);
    assert.deepEqual(Object.keys(emptyLoaded.roots), []);
    await writeFile(deployment, " \n\t");
    const loaded = await loadDeployment(deployment);
    assert.equal(loaded.overlay, ".");
    assert.equal(loaded.module_dir, undefined);
    assert.deepEqual(Object.keys(loaded.roots), []);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("deployment loader defaults omitted overlay and module_dir", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({ roots: {} }));
    const loaded = await loadDeployment(deployment);
    assert.equal(loaded.overlay, ".");
    assert.equal(loaded.module_dir, undefined);
    assert.deepEqual(Object.keys(loaded.roots), []);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("deployment loader preserves tfvars_format for operation-scoped validation", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({ tfvars_format: "hcl" }));
    assert.equal((await loadDeployment(deployment)).tfvars_format, "hcl");
    await writeFile(deployment, JSON.stringify({ tfvars_format: "future" }));
    assert.equal((await loadDeployment(deployment)).tfvars_format, "future");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("deployment loader fails closed on malformed root configuration", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "deployment-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    for (const value of [
      [],
      { roots: [] },
      { roots: { zpa: [] } },
      { roots: { zpa: { strategy: "surprise" } } },
      { roots: { zpa: { groups: { empty: [] } } } },
      { roots: { zpa: { unknown: true } } },
    ]) {
      await writeFile(deployment, JSON.stringify(value));
      await assert.rejects(() => loadDeployment(deployment));
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("deployment dictionaries do not treat prototype names specially", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "deployment-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(
      deployment,
      '{"roots":{"zpa":{"groups":{"__proto__":["zpa_alpha_one"]}}}}',
    );
    const loaded = await loadDeployment(deployment);
    assert.deepEqual(Object.keys(loaded.roots), ["zpa"]);
    assert.deepEqual(
      Object.keys(loaded.roots.zpa?.groups ?? {}),
      ["__proto__"],
    );
    await writeFile(
      deployment,
      '{"roots":{"zpa":{"groups":{"__proto__":["one"],"__proto__":["two"]}}}}',
    );
    await assert.rejects(() => loadDeployment(deployment));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
