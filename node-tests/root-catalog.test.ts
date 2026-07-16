import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadPackRoot } from "../node-src/metadata/loader.js";
import {
  buildRootCatalog,
  renderRootCatalog,
} from "../node-src/metadata/root-catalog.js";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
const ZSCALER_PROVIDERS = ["zcc", "zia", "zpa", "ztc"] as const;

async function fullRoot() {
  return loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
}

test("validated Node metadata exactly regenerates the bundled root catalog", async () => {
  const expected = await readFile(
    path.join(ROOT, "catalogs", "zscaler-root-catalog.v1.json"),
    "utf8",
  );
  assert.equal(
    await renderRootCatalog(await fullRoot(), ZSCALER_PROVIDERS),
    expected,
  );
});

test("provider selection scopes resources and source evidence deterministically", async () => {
  const catalog = await buildRootCatalog(await fullRoot(), ["zia", "zcc", "zia"]);
  assert.deepEqual(catalog.declared_providers, ["zcc", "zia"]);
  assert.deepEqual(catalog.source_files, [
    "zcc/pack.json",
    "zcc/registry.json",
    "zia/pack.json",
    "zia/registry.json",
  ]);
  assert.ok(catalog.resources.length > 0);
  assert.ok(catalog.resources.every((resource) => {
    return resource.provider === "zcc" || resource.provider === "zia";
  }));
  assert.match(catalog.sources_sha256, /^[0-9a-f]{64}$/u);
});

test("unknown provider selection fails before catalog publication", async () => {
  await assert.rejects(
    buildRootCatalog(await fullRoot(), ["unknown"]),
    /unknown provider\(s\): unknown/u,
  );
});

test("root-catalog CLI renders, checks, and rejects conflicting destinations", async (context) => {
  const expected = await readFile(
    path.join(ROOT, "catalogs", "zscaler-root-catalog.v1.json"),
    "utf8",
  );
  const common = [
    "root-catalog",
    "--providers", "zcc,zia,zpa,ztc",
    "--profile", path.join(ROOT, "packsets", "full.json"),
    "--catalog", path.join(ROOT, "packsets", "full.json"),
  ];
  const rendered = spawnSync(process.execPath, [CLI, ...common], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(rendered.status, 0, rendered.stderr);
  assert.equal(rendered.stdout, expected);

  const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-root-catalog-"));
  context.after(async () => rm(temporary, { force: true, recursive: true }));
  const stale = path.join(temporary, "stale.json");
  await writeFile(stale, "{}\n", "utf8");
  const checked = spawnSync(process.execPath, [CLI, ...common, "--check", stale], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(checked.status, 1);
  assert.match(checked.stderr, /STALE_ROOT_CATALOG/u);

  const conflict = spawnSync(process.execPath, [
    CLI,
    ...common,
    "--check", stale,
    "--out", path.join(temporary, "output.json"),
  ], { cwd: ROOT, encoding: "utf8" });
  assert.equal(conflict.status, 2);
  assert.match(conflict.stderr, /accepts only one of --out or --check/u);
});
