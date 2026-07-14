import assert from "node:assert/strict";
import test from "node:test";

import { rootTopology } from "../node-src/domain/roots.js";
import type { Deployment, RootCatalog } from "../node-src/domain/types.js";

const catalog: RootCatalog = {
  kind: "infrawright.root_catalog",
  schema_version: 1,
  declared_providers: ["zpa"],
  resources: [
    {
      type: "zpa_alpha_one",
      product: "zpa",
      provider: "zpa",
      bare_name: "alpha_one",
      slug_label: "zpa_alpha",
      generated: true,
      derived: false,
    },
    {
      type: "zpa_alpha_two",
      product: "zpa",
      provider: "zpa",
      bare_name: "alpha_two",
      slug_label: "zpa_alpha",
      generated: true,
      derived: false,
    },
    {
      type: "zpa_derived_reorder",
      product: "zpa",
      provider: "zpa",
      bare_name: "derived_reorder",
      slug_label: "zpa_derived",
      generated: true,
      derived: true,
    },
    {
      type: "zpa_known_only",
      product: "zpa",
      provider: "zpa",
      bare_name: "known_only",
      slug_label: "zpa_known",
      generated: false,
      derived: false,
    },
    {
      type: "zpa_alpha_reference",
      product: "zpa",
      provider: "zpa",
      bare_name: "alpha_reference",
      slug_label: "zpa_alpha",
      generated: true,
      derived: false,
      slug_group: false,
    },
  ],
  source_files: ["zpa/pack.json", "zpa/registry.json"],
  sources_sha256: "0".repeat(64),
};

test("slug selection returns the entire root and a structured diagnostic", () => {
  const deployment: Deployment = {
    overlay: "tenant-data//../stable",
    roots: { zpa: { strategy: "slug" } },
  };
  const result = rootTopology({
    catalog,
    deployment,
    tenant: "prod",
    selectors: ["zpa_alpha_one"],
  });
  assert.deepEqual(result.topology.roots, [
    {
      label: "zpa_alpha",
      provider: "zpa",
      members: ["zpa_alpha_one", "zpa_alpha_two"],
      env_dir: "tenant-data//../stable/envs/prod/zpa_alpha",
    },
  ]);
  assert.deepEqual(result.topology.resource_roots, {
    zpa_alpha_one: "zpa_alpha",
    zpa_alpha_two: "zpa_alpha",
  });
  assert.deepEqual(result.diagnostics, [
    {
      level: "note",
      code: "WHOLE_ROOT_SELECTION",
      message: "selecting zpa_alpha_one selects whole root zpa_alpha; also operating on zpa_alpha_two",
      selected_members: ["zpa_alpha_one"],
      root: "zpa_alpha",
      additional_members: ["zpa_alpha_two"],
    },
  ]);
});

test("derived and pack-excluded resources remain separate under slug grouping", () => {
  const result = rootTopology({
    catalog,
    deployment: { overlay: ".", roots: { zpa: { strategy: "slug" } } },
    tenant: null,
    selectors: ["zpa"],
  });
  assert.deepEqual(
    result.topology.roots.map((root) => root.label),
    ["zpa_alpha", "zpa_alpha_reference", "zpa_derived_reorder"],
  );
  assert.equal(result.topology.directories, null);
  assert.ok(result.topology.roots.every((root) => root.env_dir === null));
  assert.deepEqual(
    result.topology.resource_roots,
    {
      zpa_alpha_one: "zpa_alpha",
      zpa_alpha_two: "zpa_alpha",
      zpa_alpha_reference: "zpa_alpha_reference",
      zpa_derived_reorder: "zpa_derived_reorder",
    },
  );
});

test("known non-generated and unknown selectors fail closed", () => {
  for (const selector of ["zpa_known_only", "zpa_missing"]) {
    assert.throws(
      () => rootTopology({
        catalog,
        deployment: { overlay: ".", roots: {} },
        tenant: null,
        selectors: [selector],
      }),
      /unknown or non-generated resource selector/,
    );
  }
});

test("library boundary rejects invalid tenants without relying on the host", () => {
  for (const tenant of ["", ".", "..", "bad/tenant", "é"]) {
    assert.throws(
      () => rootTopology({
        catalog,
        deployment: { overlay: ".", roots: {} },
        tenant,
        selectors: [],
      }),
      /TENANT must match/,
    );
  }
});

test("explicit groups reject derived and cross-provider members", () => {
  assert.throws(
    () => rootTopology({
      catalog,
      deployment: {
        overlay: ".",
        roots: {
          zpa: { groups: { combined: ["zpa_derived_reorder"] } },
        },
      },
      tenant: null,
      selectors: [],
    }),
    /derived type/,
  );
  assert.throws(
    () => rootTopology({
      catalog,
      deployment: {
        overlay: ".",
        roots: { other: { strategy: "explicit" } },
      },
      tenant: null,
      selectors: [],
    }),
    /not a declared provider/,
  );
});

test("explicit groups may include a generate-only type", () => {
  const result = rootTopology({
    catalog,
    deployment: {
      overlay: ".",
      roots: {
        zpa: {
          groups: {
            zpa_explicit: ["zpa_alpha_one", "zpa_alpha_reference"],
          },
        },
      },
    },
    tenant: null,
    selectors: ["zpa_alpha_one"],
  });
  assert.deepEqual(result.topology.roots[0]?.members, [
    "zpa_alpha_one",
    "zpa_alpha_reference",
  ]);
});
