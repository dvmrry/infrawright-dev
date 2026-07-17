import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadRootCatalog } from "../node-src/domain/catalog.js";
import { loadDeployment } from "../node-src/domain/deployment.js";
import { rootTopology } from "../node-src/domain/roots.js";
import { changedPathScope } from "../node-src/domain/scope-paths.js";
import { planRoots } from "../node-src/domain/plan-roots.js";
import {
  renderLegacyChangedPathScope,
  renderLegacyPlanRoots,
  renderLegacyRootDiagnostics,
  renderLegacyRootTopology,
} from "../node-src/cli/python-compatible-output.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";
import { renderRootCatalog } from "../node-src/metadata/root-catalog.js";

const WORKSPACE = process.cwd();
const CATALOG = path.join(
  WORKSPACE,
  "catalogs/zscaler-root-catalog.v1.json",
);
const AUTHORITY_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/python-differential-v1.json",
);
const AUTHORITY_SHA256 = "339d1cd700aaa15d508292a4bdce651b912fd3485914a9174d4426ca1dbfc739";

interface FrozenBytes {
  readonly base64: string;
  readonly sha256: string;
  readonly size: number;
}

interface FrozenRecord {
  readonly arguments: readonly string[];
  readonly exit_status: number;
  readonly stderr: FrozenBytes;
  readonly stdout: FrozenBytes;
}

interface DifferentialAuthority {
  readonly kind: string;
  readonly record_count: number;
  readonly records: readonly FrozenRecord[];
  readonly schema_version: number;
  readonly suite: string;
}

let recordCursor = 0;
const authorityPromise = readFile(AUTHORITY_PATH).then((bytes) => {
  assert.equal(createHash("sha256").update(bytes).digest("hex"), AUTHORITY_SHA256);
  const authority = JSON.parse(bytes.toString("utf8")) as DifferentialAuthority;
  assert.equal(authority.kind, "python-engine-ops-delegation-authority");
  assert.equal(authority.schema_version, 1);
  assert.equal(authority.suite, "differential");
  assert.equal(authority.record_count, 30);
  assert.equal(authority.records.length, authority.record_count);
  return authority;
});

function frozenText(blob: FrozenBytes): string {
  const bytes = Buffer.from(blob.base64, "base64");
  assert.equal(bytes.length, blob.size);
  assert.equal(createHash("sha256").update(bytes).digest("hex"), blob.sha256);
  return bytes.toString("utf8");
}

async function consumeRecord(arguments_: readonly string[]): Promise<FrozenRecord> {
  const authority = await authorityPromise;
  const record = authority.records[recordCursor];
  assert.ok(record, `missing frozen differential record ${recordCursor}`);
  assert.deepEqual(record.arguments, arguments_, `frozen differential record ${recordCursor}`);
  assert.equal(record.exit_status, 0);
  recordCursor += 1;
  return record;
}

function normalizedText(
  text: string,
  roots: readonly (readonly [string, string, string?])[] = [],
): string {
  const replacements = new Map<string, string>();
  for (const [root, placeholder, relativePlaceholder] of roots) {
    replacements.set(root, placeholder);
    const relative = path.relative(WORKSPACE, root);
    if (relative !== "") replacements.set(relative, relativePlaceholder ?? placeholder);
  }
  let normalized = text;
  for (const [source, target] of [...replacements].sort(([left], [right]) => {
    return right.length - left.length;
  })) {
    normalized = normalized.replaceAll(source, target);
  }
  return normalized;
}

async function checkCatalog(packsRoot?: string): Promise<void> {
  const record = await consumeRecord([
    "-m", "engine.root_catalog", "--providers", "zcc,zia,zpa,ztc",
    "--check", "<REPOSITORY_ROOT>/catalogs/zscaler-root-catalog.v1.json",
  ]);
  assert.equal(frozenText(record.stdout), "");
  assert.equal(frozenText(record.stderr), "");
  const expected = await readFile(CATALOG, "utf8");
  assert.equal(
    await renderRootCatalog(
      await loadPackRoot({ packsRoot: packsRoot ?? path.join(WORKSPACE, "packs") }),
      ["zcc", "zia", "zpa", "ztc"],
    ),
    expected,
  );
}

async function compare(options: {
  deployment: string;
  tenant: string | null;
  selectors: readonly string[];
  packsRoot?: string;
  normalize?: readonly (readonly [string, string, string?])[];
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
  const record = await consumeRecord(args);
  const stdout = frozenText(record.stdout);
  const stderr = frozenText(record.stderr);
  const rendered = normalizedText(renderLegacyRootTopology(nodeResult.topology), options.normalize);
  assert.equal(rendered, stdout);
  assert.equal(normalizedText(renderLegacyRootDiagnostics(nodeResult.diagnostics), options.normalize), stderr);
  assert.deepEqual(JSON.parse(rendered), JSON.parse(stdout));
}

async function compareScope(options: {
  deployment: string;
  paths: readonly string[];
  packsRoot?: string;
  normalize?: readonly (readonly [string, string, string?])[];
}): Promise<void> {
  const catalog = await loadRootCatalog(CATALOG);
  const deployment = await loadDeployment(options.deployment);
  const nodeResult = changedPathScope({
    catalog,
    deployment,
    deploymentPath: options.deployment,
    workspace: WORKSPACE,
    paths: options.paths,
  });
  const record = await consumeRecord([
    "-m", "engine.ops", "scope-paths", "--json", "--paths-json", "-",
  ]);
  const stdout = frozenText(record.stdout);
  const rendered = normalizedText(renderLegacyChangedPathScope(nodeResult), options.normalize);
  assert.equal(rendered, stdout);
  assert.deepEqual(JSON.parse(rendered), JSON.parse(stdout));
}

async function comparePlanRoots(options: {
  deployment: string;
  tenant: string | null;
  selectors: readonly string[];
  packsRoot?: string;
  normalize?: readonly (readonly [string, string, string?])[];
}): Promise<Awaited<ReturnType<typeof planRoots>>> {
  const catalog = await loadRootCatalog(CATALOG);
  const deployment = await loadDeployment(options.deployment);
  const nodeResult = await planRoots({
    catalog,
    deployment,
    workspace: WORKSPACE,
    tenant: options.tenant,
    selectors: options.selectors,
  });
  const args = ["-m", "engine.ops", "plan-roots", "--json"];
  if (options.tenant !== null) {
    args.push("--tenant", options.tenant);
  }
  args.push(...options.selectors);
  const record = await consumeRecord(args);
  const stdout = frozenText(record.stdout);
  const stderr = frozenText(record.stderr);
  const rendered = normalizedText(renderLegacyPlanRoots(nodeResult.result), options.normalize);
  assert.equal(rendered, stdout);
  assert.equal(normalizedText(renderLegacyRootDiagnostics(nodeResult.diagnostics), options.normalize), stderr);
  assert.deepEqual(JSON.parse(rendered), JSON.parse(stdout));
  return nodeResult;
}

test("committed Zscaler catalog is current", async () => {
  await checkCatalog();
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
    await checkCatalog(packsRoot);
    await compare({
      deployment: path.join(directory, "missing.json"),
      tenant: "prod",
      selectors: ["zpa", "zia/url_categories"],
      packsRoot,
    });
    await comparePlanRoots({
      deployment: path.join(directory, "missing.json"),
      tenant: null,
      selectors: ["zpa/application_segment"],
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
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
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
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("ZIA generate-only exclusions preserve exact automatic and explicit topology", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deploymentPath = path.join(directory, "deployment.json");
    await writeFile(deploymentPath, JSON.stringify({
      roots: { zia: { strategy: "slug" } },
    }));
    await compare({ deployment: deploymentPath, tenant: "prod", selectors: ["zia"] });

    const catalog = await loadRootCatalog(CATALOG);
    let deployment = await loadDeployment(deploymentPath);
    let result = rootTopology({
      catalog,
      deployment,
      tenant: "prod",
      selectors: ["zia"],
    });
    const changedMappings = {
      zia_admin_roles: "zia_admin_roles",
      zia_admin_users: "zia_admin_users",
      zia_bandwidth_classes_file_size: "zia_bandwidth_classes_file_size",
      zia_bandwidth_classes_web_conferencing: "zia_bandwidth_classes_web_conferencing",
      zia_cloud_app_control_rule: "zia_cloud_app_control_rule",
      zia_cloud_application_instance: "zia_cloud_application_instance",
      zia_cloud_nss_feed: "zia_cloud_nss_feed",
      zia_sandbox_behavioral_analysis_v2: "zia_sandbox_behavioral_analysis_v2",
      zia_sandbox_file_submission: "zia_sandbox_file_submission",
      zia_traffic_capture_rules: "zia_traffic_capture_rules",
      zia_virtual_service_edge_cluster: "zia_virtual_service_edge_cluster",
      zia_virtual_service_edge_node: "zia_virtual_service_edge_node",
    } as const;
    for (const [resourceType, label] of Object.entries(changedMappings)) {
      assert.equal(result.topology.resource_roots[resourceType], label, resourceType);
    }
    assert.equal(result.topology.resource_roots.zia_bandwidth_classes, "zia_bandwidth");
    assert.equal(result.topology.resource_roots.zia_bandwidth_control_rule, "zia_bandwidth");
    assert.equal(result.topology.roots.some((root) => root.label === "zia_admin"), false);
    assert.deepEqual(
      result.topology.roots.find((root) => root.label === "zia_bandwidth")?.members,
      ["zia_bandwidth_classes", "zia_bandwidth_control_rule"],
    );

    const historicalGroups = {
      zia_admin: ["zia_admin_roles", "zia_admin_users"],
      zia_bandwidth: [
        "zia_bandwidth_classes",
        "zia_bandwidth_classes_file_size",
        "zia_bandwidth_classes_web_conferencing",
        "zia_bandwidth_control_rule",
      ],
      zia_cloud: [
        "zia_cloud_app_control_rule",
        "zia_cloud_application_instance",
        "zia_cloud_nss_feed",
      ],
      zia_sandbox: [
        "zia_sandbox_behavioral_analysis",
        "zia_sandbox_behavioral_analysis_v2",
        "zia_sandbox_file_submission",
        "zia_sandbox_rules",
      ],
      zia_traffic: [
        "zia_traffic_capture_rules",
        "zia_traffic_forwarding_gre_tunnel",
        "zia_traffic_forwarding_static_ip",
        "zia_traffic_forwarding_vpn_credentials",
      ],
      zia_virtual: [
        "zia_virtual_service_edge_cluster",
        "zia_virtual_service_edge_node",
      ],
    } as const;
    await writeFile(deploymentPath, JSON.stringify({
      roots: { zia: { strategy: "slug", groups: historicalGroups } },
    }));
    await compare({ deployment: deploymentPath, tenant: "prod", selectors: ["zia"] });
    deployment = await loadDeployment(deploymentPath);
    result = rootTopology({ catalog, deployment, tenant: "prod", selectors: ["zia"] });
    for (const [label, members] of Object.entries(historicalGroups)) {
      assert.deepEqual(
        result.topology.roots.find((root) => root.label === label)?.members,
        members,
        label,
      );
      for (const member of members) {
        assert.equal(result.topology.resource_roots[member], label, member);
      }
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("changed-path scope bytes match Python across artifact kinds", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({
      roots: {
        zpa: {
          groups: {
            zpa_segments: ["zpa_application_segment", "zpa_segment_group"],
          },
        },
      },
    }));
    await compareScope({
      deployment,
      paths: [
        "./config/prod/zpa_application_segment.auto.tfvars.json",
        "config/prod/zpa_application_segment.auto.tfvars.json",
        "imports/prod/zpa_segment_group_imports.tf",
        "envs/prod/zpa_segments",
        "envs/prod/zpa_segments/terraform.tfstate",
        "modules/zpa_application_segment/main.tf",
        deployment,
        "unrelated/file.txt",
      ],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("changed-path scope bytes match Python for overlay and module_dir", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    const overlay = path.join(directory, "external-overlay");
    const moduleDir = path.join(directory, "external-modules");
    await writeFile(deployment, JSON.stringify({
      overlay,
      module_dir: moduleDir,
      roots: { zpa: { strategy: "slug" } },
    }));
    await compareScope({
      deployment,
      paths: [
        `${overlay}/config/prod/zia_url_filtering_rules.generated.expressions.json`,
        `${overlay}/config/prod/zia_url_filtering_rules.expressions.json`,
        `${overlay}/config/prod/zia_url_filtering_rules.auto.tfvars`,
        `${overlay}/config/prod/zia_url_filtering_rules.lookup.json`,
        `${overlay}/imports/prod/zia_url_filtering_rules_moves.tf`,
        `${moduleDir}/zpa_application_segment/variables.tf`,
      ],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("empty changed-path scope matches Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    await compareScope({
      deployment: path.join(directory, "missing.json"),
      paths: [],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("external overlay spellings and missing leaves match Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  const external = await mkdtemp(path.join(os.tmpdir(), "infrawright-overlay-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({ overlay: external }));
    const absolute = path.join(
      external,
      "config/prod/zpa_segment_group.auto.tfvars.json",
    );
    const relative = path.relative(WORKSPACE, absolute);
    const aliasRoot = path.join(directory, "overlay-alias");
    await symlink(external, aliasRoot);
    const alias = path.join(
      aliasRoot,
      "config/prod/zpa_segment_group.auto.tfvars.json",
    );
    for (const spelling of [absolute, relative, alias]) {
      await compareScope({
        deployment,
        paths: [spelling],
        normalize: [
          [directory, "<NODE_WORKSPACE_ROOT>"],
          [
            external,
            "<OVERLAY_ROOT>",
            spelling === relative ? "../../..<OVERLAY_ROOT>" : "<OVERLAY_ROOT>",
          ],
        ],
      });
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
    await rm(external, { recursive: true, force: true });
  }
});

test("external deployment absolute, relative, and alias spellings match Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({}));
    const relative = path.relative(WORKSPACE, deployment);
    const alias = path.join(directory, "deployment-alias.json");
    await symlink(deployment, alias);
    for (const spelling of [deployment, relative, alias]) {
      await compareScope({
        deployment,
        paths: [spelling],
        normalize: [[
          directory,
          "<NODE_WORKSPACE_ROOT>",
          spelling === relative ? "../../..<NODE_WORKSPACE_ROOT>" : "<NODE_WORKSPACE_ROOT>",
        ]],
      });
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("dangling deployment aliases retain deleted-target scoping parity", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const target = path.join(directory, "deleted-deployment.json");
    const alias = path.join(directory, "deployment-alias.json");
    await symlink(target, alias);
    await compareScope({
      deployment: alias,
      paths: [alias, target],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("dangling targets resolve symlink components before parent traversal", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  const external = await mkdtemp(path.join(os.tmpdir(), "infrawright-target-"));
  try {
    const jumpTarget = path.join(external, "nested");
    await mkdir(jumpTarget);
    await symlink(jumpTarget, path.join(directory, "jump"));

    const deletedDeployment = path.join(external, "deleted-deployment.json");
    const deploymentAlias = path.join(directory, "deployment-alias.json");
    await symlink("jump/../deleted-deployment.json", deploymentAlias);
    await compareScope({
      deployment: deploymentAlias,
      paths: [deploymentAlias, deletedDeployment],
      normalize: [
        [directory, "<NODE_WORKSPACE_ROOT>"],
        [external, "<TARGET_ROOT>"],
      ],
    });

    const deployment = path.join(directory, "deployment.json");
    const deletedOverlay = path.join(external, "deleted-overlay");
    await writeFile(deployment, JSON.stringify({ overlay: deletedOverlay }));
    const overlayAlias = path.join(directory, "overlay-alias");
    await symlink("jump/../deleted-overlay", overlayAlias);
    const relativeArtifact = "config/prod/zpa_segment_group.auto.tfvars.json";
    await compareScope({
      deployment,
      paths: [
        path.join(overlayAlias, relativeArtifact),
        path.join(deletedOverlay, relativeArtifact),
      ],
      normalize: [
        [directory, "<NODE_WORKSPACE_ROOT>"],
        [external, "<TARGET_ROOT>"],
      ],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
    await rm(external, { recursive: true, force: true });
  }
});

test("materialized plan-root artifact states and grouped diagnostics match Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const overlay = path.join(directory, "overlay");
    const deployment = path.join(directory, "deployment.json");
    await writeFile(deployment, JSON.stringify({
      overlay,
      roots: {
        zpa: {
          groups: {
            zpa_segments: ["zpa_application_segment", "zpa_segment_group"],
          },
        },
      },
    }));
    for (const tenant of [
      "absent",
      "complete",
      "directory-impostor",
      "plan-only",
      "sources-only",
    ]) {
      await mkdir(path.join(overlay, "envs", tenant, "zpa_segments"), {
        recursive: true,
      });
    }
    await writeFile(path.join(overlay, "envs/complete/zpa_segments/tfplan"), "plan");
    await writeFile(
      path.join(overlay, "envs/complete/zpa_segments/tfplan.sources"),
      "sources",
    );
    await writeFile(path.join(overlay, "envs/plan-only/zpa_segments/tfplan"), "plan");
    await writeFile(
      path.join(overlay, "envs/sources-only/zpa_segments/tfplan.sources"),
      "sources",
    );
    await mkdir(path.join(
      overlay,
      "envs/directory-impostor/zpa_segments/tfplan",
    ));
    await writeFile(
      path.join(
        overlay,
        "envs/directory-impostor/zpa_segments/tfplan.sources",
      ),
      "sources",
    );
    const linkedRoot = path.join(directory, "linked-root");
    await mkdir(path.join(overlay, "envs/linked"), { recursive: true });
    await mkdir(linkedRoot);
    const linkedPlan = path.join(directory, "linked-plan");
    const linkedSources = path.join(directory, "linked-sources");
    await writeFile(linkedPlan, "plan");
    await writeFile(linkedSources, "sources");
    await symlink(linkedPlan, path.join(linkedRoot, "tfplan"));
    await symlink(linkedSources, path.join(linkedRoot, "tfplan.sources"));
    await symlink(
      linkedRoot,
      path.join(overlay, "envs/linked/zpa_segments"),
    );
    await comparePlanRoots({
      deployment,
      tenant: null,
      selectors: ["zpa_application_segment", "zpa_application_segment"],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
    await comparePlanRoots({
      deployment,
      tenant: "complete",
      selectors: ["zpa"],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("relative unnormalized plan-root overlays preserve Python path bytes", async () => {
  const directory = await mkdtemp(path.join(WORKSPACE, ".node-plan-roots-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    const relative = path.relative(WORKSPACE, directory);
    const overlay = `${relative}//staging/../actual`;
    await writeFile(deployment, JSON.stringify({ overlay }));
    await mkdir(path.join(directory, "staging"));
    await mkdir(path.join(directory, "actual/envs/prod/zpa_application_segment"), {
      recursive: true,
    });
    const compared = await comparePlanRoots({
      deployment,
      tenant: "prod",
      selectors: ["zpa/application_segment"],
      normalize: [[directory, "<PLAN_ROOTS_WORKSPACE>"]],
    });
    assert.equal(compared.result.roots.length, 1);
    assert.equal(
      compared.result.roots[0]?.env_dir,
      `${overlay}/envs/prod/zpa_application_segment`,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("plan-root discovery validates only selected recognized tenant roots", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-node-"));
  try {
    const overlay = path.join(directory, "overlay");
    const deploymentPath = path.join(directory, "deployment.json");
    await writeFile(deploymentPath, JSON.stringify({ overlay }));
    await mkdir(
      path.join(overlay, "envs/bad tenant/zpa_application_segment"),
      { recursive: true },
    );
    const catalog = await loadRootCatalog(CATALOG);
    const deployment = await loadDeployment(deploymentPath);
    await assert.rejects(
      () => planRoots({
        catalog,
        deployment,
        workspace: WORKSPACE,
        tenant: null,
        selectors: ["zpa/application_segment"],
      }),
      /TENANT must match/,
    );
    const ignored = await comparePlanRoots({
      deployment: deploymentPath,
      tenant: null,
      selectors: ["zia/url_categories"],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
    assert.deepEqual(ignored.result.roots, []);
    await mkdir(path.join(overlay, "envs/also bad/unknown_root"), {
      recursive: true,
    });
    const unknownIgnored = await comparePlanRoots({
      deployment: deploymentPath,
      tenant: null,
      selectors: ["ztc/dns_gateway"],
      normalize: [[directory, "<NODE_WORKSPACE_ROOT>"]],
    });
    assert.deepEqual(unknownIgnored.result.roots, []);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("plan_roots validates unknown selectors before semantic root resolution", async () => {
  const catalog = await loadRootCatalog(CATALOG);
  await assert.rejects(
    () => planRoots({
      catalog,
      deployment: {
        overlay: ".",
        roots: { unknown_provider: {} },
      },
      workspace: WORKSPACE,
      tenant: null,
      selectors: ["not_a_resource"],
    }),
    /unknown or non-generated resource selector/,
  );
});

test("frozen differential authority is consumed exactly once in recorded order", async () => {
  const authority = await authorityPromise;
  assert.equal(recordCursor, authority.record_count);
});
