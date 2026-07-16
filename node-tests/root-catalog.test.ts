import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
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
const PACKS_ROOT = path.resolve(
  process.env.INFRAWRIGHT_PACKS?.trim() || path.join(ROOT, "packs"),
);
const PACK_PROFILE = path.resolve(
  process.env.PACK_PROFILE?.trim()
    || process.env.INFRAWRIGHT_PACK_PROFILE?.trim()
    || path.join(ROOT, "packsets", "full.json"),
);
const PACK_CATALOG = path.resolve(
  process.env.PACK_CATALOG?.trim()
    || path.join(ROOT, "packsets", "full.json"),
);

async function activeRoot() {
  return loadPackRoot({
    packsRoot: PACKS_ROOT,
    profilePath: PACK_PROFILE,
    catalogPath: PACK_CATALOG,
  });
}

async function writeJson(pathname: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(pathname), { recursive: true });
  await writeFile(pathname, JSON.stringify(value), "utf8");
}

async function evidenceDigest(
  root: string,
  relativePaths: readonly string[],
): Promise<string> {
  const digest = createHash("sha256");
  for (const relative of relativePaths) {
    digest.update(relative, "utf8");
    digest.update(Buffer.of(0));
    digest.update(await readFile(path.join(root, relative)));
    digest.update(Buffer.of(0));
  }
  return digest.digest("hex");
}

test("validated Node metadata exactly regenerates the bundled root catalog", async () => {
  const expected = await readFile(
    path.join(ROOT, "catalogs", "zscaler-root-catalog.v1.json"),
    "utf8",
  );
  assert.equal(
    await renderRootCatalog(await activeRoot(), ZSCALER_PROVIDERS),
    expected,
  );
});

test("provider selection scopes resources and source evidence deterministically", async () => {
  const catalog = await buildRootCatalog(await activeRoot(), ["zia", "zcc", "zia"]);
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
    buildRootCatalog(await activeRoot(), ["unknown"]),
    /unknown provider\(s\): unknown/u,
  );
});

test("catalog rendering preserves Python ASCII escaping for non-ASCII metadata", async (context) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-root-catalog-unicode-"));
  context.after(async () => rm(temporary, { force: true, recursive: true }));
  await writeJson(path.join(temporary, "sample", "pack.json"), {
    provider_prefixes: { x_: "π" },
  });
  await writeJson(path.join(temporary, "sample", "registry.json"), {
    "x_é": { generate: true, product: "café" },
  });
  const sourceFiles = ["sample/pack.json", "sample/registry.json"];
  const digest = await evidenceDigest(temporary, sourceFiles);
  const rendered = await renderRootCatalog(await loadPackRoot({
    packsRoot: temporary,
  }));
  assert.equal(rendered, `{
  "declared_providers": [
    "\\u03c0"
  ],
  "kind": "infrawright.root_catalog",
  "resources": [
    {
      "bare_name": "\\u00e9",
      "derived": false,
      "generated": true,
      "product": "caf\\u00e9",
      "provider": "\\u03c0",
      "slug_label": "x_\\u00e9",
      "type": "x_\\u00e9"
    }
  ],
  "schema_version": 1,
  "source_files": [
    "sample/pack.json",
    "sample/registry.json"
  ],
  "sources_sha256": "${digest}"
}
`);
});

test("a selected pack without a registry contributes manifest evidence and no resources", async (context) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "infrawright-root-catalog-no-registry-"));
  context.after(async () => rm(temporary, { force: true, recursive: true }));
  await writeJson(path.join(temporary, "sample", "pack.json"), {
    provider_prefixes: { sample_: "sample" },
  });
  const catalog = await buildRootCatalog(await loadPackRoot({ packsRoot: temporary }));
  assert.deepEqual(catalog.declared_providers, ["sample"]);
  assert.deepEqual(catalog.resources, []);
  assert.deepEqual(catalog.source_files, ["sample/pack.json"]);
  assert.equal(
    catalog.sources_sha256,
    await evidenceDigest(temporary, ["sample/pack.json"]),
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
    "--root", PACKS_ROOT,
    "--profile", PACK_PROFILE,
    "--catalog", PACK_CATALOG,
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
