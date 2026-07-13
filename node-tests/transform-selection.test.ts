import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import {
  referenceOrder,
  selectTransformResources,
  transformSourceType,
} from "../node-src/domain/transform-selection.js";

interface TestPack {
  readonly name: string;
  readonly manifest: Readonly<Record<string, unknown>>;
  readonly registry: Readonly<Record<string, unknown>>;
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

async function syntheticRoot(packs: readonly TestPack[]): Promise<{
  readonly directory: string;
  readonly root: LoadedPackRoot;
}> {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-selection-"));
  for (const pack of packs) {
    await writeJson(path.join(directory, pack.name, "pack.json"), pack.manifest);
    await writeJson(path.join(directory, pack.name, "registry.json"), pack.registry);
  }
  return { directory, root: await loadPackRoot({ packsRoot: directory }) };
}

test("selector expansion is referent-first and otherwise alphabetic", async (context) => {
  const fixture = await syntheticRoot([{
    name: "sample",
    manifest: {
      provider_prefixes: { sample_: "sample" },
      references: {
        sample_a_referrer: {
          referent_id: { name_field: "name", referent: "sample_b_referent" },
        },
      },
    },
    registry: {
      sample_a_referrer: { generate: true, product: "sample" },
      sample_aa_unrelated: { generate: true, product: "sample" },
      sample_b_referent: { generate: true, product: "sample" },
      sample_data_only: { product: "sample" },
    },
  }]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.deepEqual(selectTransformResources({
    root: fixture.root,
    selectors: ["sample"],
  }), {
    resourceTypes: [
      "sample_aa_unrelated",
      "sample_b_referent",
      "sample_a_referrer",
    ],
    notes: [],
  });
});

test("duplicate inputs collapse and references outside the selection are ignored", async (context) => {
  const fixture = await syntheticRoot([{
    name: "sample",
    manifest: {
      provider_prefixes: { sample_: "sample" },
      references: {
        sample_a: { target: { name_field: "name", referent: "sample_b" } },
      },
    },
    registry: {
      sample_a: { generate: true, product: "sample" },
      sample_b: { generate: true, product: "sample" },
    },
  }]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.deepEqual(referenceOrder({
    root: fixture.root,
    resourceTypes: ["sample_a", "sample_a"],
  }), { resourceTypes: ["sample_a"], notes: [] });
});

test("Tarjan cycle members produce one exact note and alphabetic break", async (context) => {
  const fixture = await syntheticRoot([{
    name: "sample",
    manifest: {
      provider_prefixes: { sample_: "sample" },
      references: {
        sample_cycle_a: {
          other_id: { name_field: "name", referent: "sample_cycle_b" },
        },
        sample_cycle_b: {
          other_id: { name_field: "name", referent: "sample_cycle_a" },
        },
        sample_downstream: {
          cycle_id: { name_field: "name", referent: "sample_cycle_b" },
        },
      },
    },
    registry: {
      sample_cycle_a: { generate: true, product: "sample" },
      sample_cycle_b: { generate: true, product: "sample" },
      sample_downstream: { generate: true, product: "sample" },
    },
  }]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.deepEqual(referenceOrder({
    root: fixture.root,
    resourceTypes: ["sample_downstream", "sample_cycle_b", "sample_cycle_a"],
  }), {
    resourceTypes: ["sample_cycle_a", "sample_cycle_b", "sample_downstream"],
    notes: [
      "NOTE: reference order cycle detected among sample_cycle_a, "
        + "sample_cycle_b; breaking alphabetically\n",
    ],
  });
});

test("a self-reference is a cycle member", async (context) => {
  const fixture = await syntheticRoot([{
    name: "sample",
    manifest: {
      provider_prefixes: { sample_: "sample" },
      references: {
        sample_self: {
          parent_id: { name_field: "name", referent: "sample_self" },
        },
      },
    },
    registry: { sample_self: { generate: true, product: "sample" } },
  }]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.deepEqual(referenceOrder({
    root: fixture.root,
    resourceTypes: ["sample_self"],
  }), {
    resourceTypes: ["sample_self"],
    notes: [
      "NOTE: reference order cycle detected among sample_self; breaking alphabetically\n",
    ],
  });
});

test("active pack reference tables merge with Python's later-field overwrite", async (context) => {
  const fixture = await syntheticRoot([
    {
      name: "alpha",
      manifest: {
        provider_prefixes: { sample_: "sample" },
        references: {
          sample_a_referrer: {
            target: { name_field: "name", referent: "sample_z_referent" },
          },
        },
      },
      registry: {
        sample_a_referrer: { generate: true, product: "sample" },
        sample_z_referent: { generate: true, product: "sample" },
      },
    },
    {
      name: "beta",
      manifest: {
        provider_prefixes: { other_: "other" },
        references: {
          sample_a_referrer: {
            target: { name_field: "name", referent: "other_z_referent" },
          },
        },
      },
      registry: {
        other_z_referent: { generate: true, product: "other" },
      },
    },
  ]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.deepEqual(referenceOrder({
    root: fixture.root,
    resourceTypes: [
      "sample_z_referent",
      "sample_a_referrer",
      "other_z_referent",
    ],
  }), {
    resourceTypes: [
      "other_z_referent",
      "sample_a_referrer",
      "sample_z_referent",
    ],
    notes: [],
  });
});

test("derived resources resolve their source pull while normal resources resolve themselves", async (context) => {
  const fixture = await syntheticRoot([{
    name: "sample",
    manifest: { provider_prefixes: { sample_: "sample" } },
    registry: {
      sample_source: { generate: true, product: "sample" },
      sample_derived: {
        derive: { from: "sample_source", policy_type: "ACCESS_POLICY" },
        generate: true,
        product: "sample",
      },
      sample_data_only: { product: "sample" },
    },
  }]);
  context.after(() => rm(fixture.directory, { recursive: true, force: true }));

  assert.equal(transformSourceType(fixture.root, "sample_source"), "sample_source");
  assert.equal(transformSourceType(fixture.root, "sample_derived"), "sample_source");
  assert.throws(
    () => transformSourceType(fixture.root, "sample_data_only"),
    /unknown or non-generated/,
  );
  assert.throws(
    () => transformSourceType(fixture.root, "sample_missing"),
    /unknown or non-generated/,
  );
});
