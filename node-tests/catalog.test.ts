import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadRootCatalog } from "../node-src/domain/catalog.js";

function catalog(resourceProvider: string) {
  return {
    kind: "infrawright.root_catalog",
    schema_version: 1,
    declared_providers: ["zpa"],
    resources: [
      {
        type: `${resourceProvider}_sample`,
        product: resourceProvider,
        provider: resourceProvider,
        bare_name: "sample",
        slug_label: `${resourceProvider}_sample`,
        generated: true,
        derived: false,
      },
    ],
    source_files: ["zpa/pack.json", "zpa/registry.json"],
    sources_sha256: "0".repeat(64),
  };
}

test("catalog rejects resources owned by undeclared providers", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "catalog-node-"));
  try {
    const file = path.join(directory, "catalog.json");
    await writeFile(file, JSON.stringify(catalog("zia")));
    await assert.rejects(
      () => loadRootCatalog(file),
      (error: unknown) => {
        assert.equal(
          (error as { code?: string }).code,
          "INVALID_ROOT_CATALOG",
        );
        assert.match(String(error), /resource providers must be declared: zia/);
        return true;
      },
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("catalog rejects identical and prototype-like duplicate keys", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "catalog-node-"));
  try {
    const file = path.join(directory, "catalog.json");
    const valid = JSON.stringify(catalog("zpa"));
    await writeFile(
      file,
      valid.replace(
        '"schema_version":1',
        '"schema_version":1,"schema_version":1',
      ),
    );
    await assert.rejects(() => loadRootCatalog(file));
    await writeFile(
      file,
      valid.replace(
        '"resources":',
        '"__proto__":{},"__proto__":{},"resources":',
      ),
    );
    await assert.rejects(() => loadRootCatalog(file));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
