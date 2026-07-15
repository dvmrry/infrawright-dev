import assert from "node:assert/strict";
import { cp, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { resolveCollectorAdapters } from "../node-src/collectors/authority.js";
import {
  createZscalerCollectorAdaptersByProviderSource,
  ZSCALER_COLLECTOR_PROVIDER_SOURCES,
} from "../node-src/collectors/zscaler-adapters.js";
import type { CollectorAdapter } from "../node-src/collectors/types.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();

async function pythonArtifacts(directory: string): Promise<string[]> {
  const found: string[] = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const pathname = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "__pycache__") found.push(pathname);
      else found.push(...await pythonArtifacts(pathname));
    } else if (entry.name.endsWith(".py") || entry.name.endsWith(".pyc")) {
      found.push(pathname);
    }
  }
  return found;
}

async function removePythonArtifacts(directory: string): Promise<void> {
  for (const pathname of await pythonArtifacts(directory)) {
    await rm(pathname, { recursive: true, force: true });
  }
}

async function copiedRoot(products: readonly string[]): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-authority-"));
  await cp(
    path.join(ROOT, "packs", "_shared", "zscaler"),
    path.join(directory, "_shared", "zscaler"),
    { recursive: true },
  );
  for (const product of products) {
    await cp(path.join(ROOT, "packs", product), path.join(directory, product), {
      recursive: true,
    });
  }
  await removePythonArtifacts(directory);
  assert.deepEqual(await pythonArtifacts(directory), []);
  return directory;
}

async function rewriteManifest(
  packsRoot: string,
  product: string,
  update: (manifest: Record<string, unknown>) => void,
): Promise<void> {
  const pathname = path.join(packsRoot, product, "pack.json");
  const manifest = JSON.parse(await readFile(pathname, "utf8")) as Record<string, unknown>;
  update(manifest);
  await writeFile(pathname, `${JSON.stringify(manifest, null, 2)}\n`);
}

async function removeProviderScopedUnsupportedRules(
  packsRoot: string,
  product: string,
): Promise<void> {
  const pathname = path.join(packsRoot, product, "registry.json");
  const registry = JSON.parse(await readFile(pathname, "utf8")) as Record<
    string,
    { adopt?: Record<string, unknown> }
  >;
  for (const entry of Object.values(registry)) {
    if (entry.adopt !== undefined) delete entry.adopt.unsupported_if;
  }
  await writeFile(pathname, `${JSON.stringify(registry, null, 2)}\n`);
}

test("committed Zscaler provider sources resolve to the bundled Node collectors", async () => {
  const root = await loadPackRoot({ packsRoot: path.join(ROOT, "packs") });
  const products = new Set(["zcc", "zia", "zpa", "ztc"]);
  const resolved = resolveCollectorAdapters({
    authorities: {
      byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
    },
    products,
    root,
  });
  assert.deepEqual([...resolved.keys()], [...products].sort());
  for (const product of products) {
    const owner = root.packs.providerOwners[product];
    const manifest = root.packs.manifests.find((item) => item.name === owner);
    assert.equal(
      manifest?.providerSources[product],
      ZSCALER_COLLECTOR_PROVIDER_SOURCES[
        product as keyof typeof ZSCALER_COLLECTOR_PROVIDER_SOURCES
      ],
    );
    assert.equal(resolved.get(product)?.product, product);
  }
});

test("a copied Python-free pack root retains its Node collector authority", async () => {
  const packsRoot = await copiedRoot(["zia"]);
  try {
    const root = await loadPackRoot({ packsRoot });
    const resolved = resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      products: new Set(["zia"]),
      root,
    });
    assert.equal(resolved.get("zia")?.product, "zia");
  } finally {
    await rm(packsRoot, { recursive: true, force: true });
  }
});

test("authority resolution is selected-only and fails closed on missing, unknown, or mismatched provider sources", async () => {
  const packsRoot = await copiedRoot(["zcc", "zia"]);
  try {
    await removeProviderScopedUnsupportedRules(packsRoot, "zia");
    await rewriteManifest(packsRoot, "zcc", (manifest) => {
      manifest.provider_sources = { zcc: "example/custom-zcc" };
    });
    let root = await loadPackRoot({ packsRoot });
    assert.equal(resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      products: new Set(["zia"]),
      root,
    }).get("zia")?.product, "zia");
    assert.throws(() => resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      products: new Set(["zcc"]),
      root,
    }), /example\/custom-zcc.*not available/u);

    await rewriteManifest(packsRoot, "zia", (manifest) => {
      manifest.provider_sources = {};
    });
    root = await loadPackRoot({ packsRoot });
    assert.throws(() => resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      products: new Set(["zia"]),
      root,
    }), /without a provider source/u);

    await rewriteManifest(packsRoot, "zia", (manifest) => {
      manifest.provider_sources = {
        zia: ZSCALER_COLLECTOR_PROVIDER_SOURCES.zpa,
      };
    });
    root = await loadPackRoot({ packsRoot });
    assert.throws(() => resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      products: new Set(["zia"]),
      root,
    }), /bound to product "zpa", not selected product "zia"/u);
  } finally {
    await rm(packsRoot, { recursive: true, force: true });
  }
});

test("library callers may supply a custom provider-source adapter without extending the bundled CLI", async () => {
  const packsRoot = await copiedRoot(["zia"]);
  try {
    await removeProviderScopedUnsupportedRules(packsRoot, "zia");
    const providerSource = "example/custom-zia";
    await rewriteManifest(packsRoot, "zia", (manifest) => {
      manifest.provider_sources = { zia: providerSource };
    });
    const root = await loadPackRoot({ packsRoot });
    const adapter: CollectorAdapter = {
      product: "zia",
      async acquire() {
        return { headers: { Accept: "application/json" } };
      },
      composeUrl(input) {
        return new URL(input.path, "https://example.invalid/");
      },
    };
    const resolved = resolveCollectorAdapters({
      authorities: { byProviderSource: new Map([[providerSource, adapter]]) },
      products: new Set(["zia"]),
      root,
    });
    assert.equal(resolved.get("zia"), adapter);
  } finally {
    await rm(packsRoot, { recursive: true, force: true });
  }
});
