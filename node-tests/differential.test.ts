import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadRootCatalog } from "../node-src/domain/catalog.js";
import { loadDeployment } from "../node-src/domain/deployment.js";
import { rootTopology } from "../node-src/domain/roots.js";
import {
  renderLegacyRootDiagnostics,
  renderLegacyRootTopology,
} from "../node-src/process/legacy.js";

const WORKSPACE = process.cwd();
const CATALOG = path.join(
  WORKSPACE,
  "catalogs/zscaler-root-catalog.v1.json",
);

async function compare(options: {
  deployment: string;
  tenant: string | null;
  selectors: readonly string[];
  packsRoot?: string;
}): Promise<void> {
  const catalog = await loadRootCatalog(CATALOG);
  const deployment = await loadDeployment(options.deployment);
  const nodeResult = rootTopology({
    catalog,
    deployment,
    tenant: options.tenant,
    selectors: options.selectors,
  });
  const args = ["-m", "engine.ops", "roots", "--json"];
  if (options.tenant !== null) {
    args.push("--tenant", options.tenant);
  }
  args.push(...options.selectors);
  const python = spawnSync("python3", args, {
    cwd: WORKSPACE,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: options.deployment,
      INFRAWRIGHT_PACKS: options.packsRoot ?? path.join(WORKSPACE, "packs"),
    },
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(renderLegacyRootTopology(nodeResult.topology), python.stdout);
  assert.equal(renderLegacyRootDiagnostics(nodeResult.diagnostics), python.stderr);
  assert.deepEqual(nodeResult.topology, JSON.parse(python.stdout));
}

test("committed Zscaler catalog is current", () => {
  const check = spawnSync(
    "python3",
    [
      "-m",
      "engine.root_catalog",
      "--providers",
      "zcc,zia,zpa,ztc",
      "--check",
      CATALOG,
    ],
    { cwd: WORKSPACE, encoding: "utf8" },
  );
  assert.equal(check.status, 0, check.stderr);
});

test("pruned Zscaler pack root produces the same catalog and topology", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const packsRoot = path.join(directory, "packs");
    await mkdir(path.join(packsRoot, "_shared"), { recursive: true });
    for (const pack of ["zcc", "zia", "zpa", "ztc"]) {
      await cp(
        path.join(WORKSPACE, "packs", pack),
        path.join(packsRoot, pack),
        { recursive: true },
      );
    }
    await cp(
      path.join(WORKSPACE, "packs/_shared/zscaler"),
      path.join(packsRoot, "_shared/zscaler"),
      { recursive: true },
    );
    const check = spawnSync(
      "python3",
      [
        "-m",
        "engine.root_catalog",
        "--providers",
        "zcc,zia,zpa,ztc",
        "--check",
        CATALOG,
      ],
      {
        cwd: WORKSPACE,
        encoding: "utf8",
        env: { ...process.env, INFRAWRIGHT_PACKS: packsRoot },
      },
    );
    assert.equal(check.status, 0, check.stderr);
    await compare({
      deployment: path.join(directory, "missing.json"),
      tenant: "prod",
      selectors: ["zpa", "zia/url_categories"],
      packsRoot,
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("default and selected Zscaler topology bytes match Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const missing = path.join(directory, "missing-deployment.json");
    await compare({ deployment: missing, tenant: null, selectors: [] });
    await compare({
      deployment: missing,
      tenant: "prod",
      selectors: ["zpa/application_segment", "zpa/application_segment"],
    });
    await compare({
      deployment: missing,
      tenant: "prod",
      selectors: ["zia"],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("grouping, notes, and unnormalized overlay paths match Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({
      overlay: "artifacts//staging/../current",
      roots: {
        zpa: {
          strategy: "explicit",
          groups: {
            zpa_segments: [
              "zpa_segment_group",
              "zpa_application_segment",
            ],
          },
        },
      },
    }));
    await compare({
      deployment,
      tenant: "prod",
      selectors: ["zpa_application_segment"],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("empty deployments, Python-falsey overlays, and slug roots match", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, "  \n");
    await compare({ deployment, tenant: null, selectors: ["zcc"] });

    await writeFile(deployment, JSON.stringify({ overlay: [] }));
    await compare({
      deployment,
      tenant: "prod",
      selectors: ["ztc/dns_gateway"],
    });

    await writeFile(deployment, JSON.stringify({
      overlay: path.join(directory, "absolute-overlay"),
      roots: { zpa: { strategy: "slug" } },
    }));
    await compare({
      deployment,
      tenant: "prod",
      selectors: ["zpa_application_segment"],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
