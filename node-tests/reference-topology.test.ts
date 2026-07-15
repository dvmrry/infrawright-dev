import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import {
  crossStateDependencyClosure,
  crossStateReferenceTopology,
} from "../node-src/domain/reference-topology.js";
import { loadedRootTopology } from "../node-src/domain/roots.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const PACKS_ROOT = path.resolve(
  process.env.INFRAWRIGHT_PACKS?.trim() || path.join(ROOT, "packs"),
);
const PACK_PROFILE = path.resolve(
  process.env.PACK_PROFILE?.trim() || path.join(ROOT, "packsets", "full.json"),
);
const PACK_CATALOG = path.resolve(
  process.env.PACK_CATALOG?.trim() || path.join(ROOT, "packsets", "full.json"),
);

async function committedRoot(): Promise<LoadedPackRoot> {
  return loadPackRoot({
    packsRoot: PACKS_ROOT,
    profilePath: PACK_PROFILE,
    catalogPath: PACK_CATALOG,
  });
}

test("cross-state topology keeps singleton dependencies and collapses explicit groups", async () => {
  const root = await committedRoot();
  const singletonDeployment = {
    overlay: ".",
    roots: { zpa: { cross_state_references: true } },
  } as const;
  const singletonTopology = loadedRootTopology({
    deployment: singletonDeployment,
    root,
    selectors: [],
    tenant: "tenant",
  }).topology;
  const singleton = crossStateReferenceTopology({
    deployment: singletonDeployment,
    root,
    topology: singletonTopology,
  });
  assert.deepEqual(singleton.edges, [{
    field: "segment_group_id",
    referent: "zpa_segment_group",
    referentRoot: "zpa_segment_group",
    referrer: "zpa_application_segment",
    referrerRoot: "zpa_application_segment",
  }]);
  assert.deepEqual(
    [...(singleton.dependenciesByRoot.get("zpa_application_segment") ?? [])],
    ["zpa_segment_group"],
  );
  assert.deepEqual(
    crossStateDependencyClosure(
      ["zpa_application_segment"],
      singleton.dependenciesByRoot,
    ),
    ["zpa_application_segment", "zpa_segment_group"],
  );

  const groupedDeployment = {
    overlay: ".",
    roots: {
      zpa: {
        cross_state_references: true,
        groups: { zpa_app: ["zpa_application_segment", "zpa_segment_group"] },
      },
    },
  } as const;
  const grouped = crossStateReferenceTopology({
    deployment: groupedDeployment,
    root,
    topology: loadedRootTopology({
      deployment: groupedDeployment,
      root,
      selectors: [],
      tenant: "tenant",
    }).topology,
  });
  assert.deepEqual(grouped.edges, []);
  assert.equal(grouped.dependenciesByRoot.size, 0);
});

test("cross-state topology rejects declared root cycles before generation", async () => {
  const root = await committedRoot();
  const manifests = root.packs.manifests.map((manifest) => {
    if (manifest.name !== "zpa") return manifest;
    return {
      ...manifest,
      data: {
        ...manifest.data,
        references: {
          ...(manifest.data.references as Readonly<Record<string, unknown>>),
          zpa_segment_group: {
            application_id: { name_field: "name", referent: "zpa_application_segment" },
          },
        },
      },
    };
  });
  const cyclicRoot = {
    ...root,
    packs: { ...root.packs, manifests },
  } as LoadedPackRoot;
  const deployment = {
    overlay: ".",
    roots: { zpa: { cross_state_references: true } },
  } as const;
  const topology = loadedRootTopology({
    deployment,
    root: cyclicRoot,
    selectors: [],
    tenant: "tenant",
  }).topology;
  assert.throws(
    () => crossStateReferenceTopology({ deployment, root: cyclicRoot, topology }),
    /cross-state reference cycle detected.*explicitly group every member/u,
  );
});
