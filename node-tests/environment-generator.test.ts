import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  access,
  cp,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { loadDeployment } from "../node-src/domain/deployment.js";
import {
  generateEnvironmentRoots,
  renderEnvironmentMain,
} from "../node-src/domain/environment-generator.js";
import { loadedRootTopology } from "../node-src/domain/roots.js";
import { terraformHclFormatter } from "../node-src/modules/generator.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const TERRAFORM = process.env.TF || "terraform";
let fullRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(profile = "full.json", packsRoot = path.join(ROOT, "packs")): Promise<LoadedPackRoot> {
  if (profile === "full.json" && packsRoot === path.join(ROOT, "packs")) {
    fullRoot ??= loadPackRoot({
      packsRoot,
      profilePath: path.join(ROOT, "packsets", profile),
      catalogPath: path.join(ROOT, "packsets", "full.json"),
    });
    return fullRoot;
  }
  return loadPackRoot({
    packsRoot,
    profilePath: path.join(ROOT, "packsets", profile),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  prefix: string,
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

async function reducedPackRootForProfile(parent: string, profile: string): Promise<string> {
  const document = JSON.parse(
    await readFile(path.join(ROOT, "packsets", profile), "utf8"),
  ) as { readonly packs: readonly string[]; readonly shared: readonly string[] };
  const destination = path.join(parent, `packs-${profile.replace(/\.json$/u, "")}`);
  await mkdir(destination, { recursive: true });
  for (const name of document.packs) {
    await cp(path.join(ROOT, "packs", name), path.join(destination, name), { recursive: true });
  }
  for (const name of document.shared) {
    await mkdir(path.join(destination, "_shared"), { recursive: true });
    await cp(
      path.join(ROOT, "packs", "_shared", name),
      path.join(destination, "_shared", name),
      { recursive: true },
    );
  }
  return destination;
}

async function snapshotTree(directory: string): Promise<Readonly<Record<string, string>>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  const visit = async (current: string): Promise<void> => {
    for (const entry of await readdir(current, { withFileTypes: true })) {
      const candidate = path.join(current, entry.name);
      if (entry.isDirectory()) await visit(candidate);
      else if (entry.isFile()) output[path.relative(directory, candidate)] = await readFile(candidate, "utf8");
    }
  };
  await visit(directory);
  return output;
}

async function pythonThenNodeTree(options: {
  readonly backend?: string;
  readonly deployment: Readonly<Record<string, unknown>>;
  readonly prepare?: (workspace: string) => Promise<void>;
  readonly selectors: readonly string[];
  readonly tenant?: string;
}, context: { after(callback: () => Promise<unknown> | unknown): void }): Promise<{
  readonly diagnostics: readonly string[];
  readonly tree: Readonly<Record<string, string>>;
}> {
  const workspace = await temporaryDirectory(context, "infrawright-gen-env-differential-");
  const tenant = options.tenant ?? "tenant";
  const deploymentPath = path.join(workspace, "deployment.json");
  const outputRoot = path.join(workspace, "generated");
  await writeJson(deploymentPath, {
    overlay: workspace,
    module_dir: path.join(workspace, "modules"),
    ...options.deployment,
  });
  await options.prepare?.(workspace);
  const python = spawnSync(PYTHON_ORACLE, [
    "-c",
    [
      "import json, sys",
      "from engine.gen_env import generate_env",
      "opts=json.loads(sys.argv[1])",
      "generate_env(opts['tenant'], out_root=opts['out'], fmt=True, backend=opts.get('backend'), selectors=opts['selectors'])",
    ].join(";"),
    JSON.stringify({
      tenant,
      out: outputRoot,
      selectors: options.selectors,
      ...(options.backend === undefined ? {} : { backend: options.backend }),
    }),
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deploymentPath,
      INFRAWRIGHT_PACK_PROFILE: path.join(ROOT, "packsets", "full.json"),
    },
  });
  assert.equal(python.status, 0, `${python.stdout}\n${python.stderr}`);
  const expected = await snapshotTree(outputRoot);
  await rm(outputRoot, { force: true, recursive: true });
  const diagnostics: string[] = [];
  await generateEnvironmentRoots({
    ...(options.backend === undefined ? {} : { backend: options.backend }),
    deployment: await loadDeployment(deploymentPath),
    formatHcl: terraformHclFormatter({ executable: TERRAFORM }),
    onDiagnostic: (message) => diagnostics.push(message),
    outputRoot,
    root: await committedRoot(),
    selectors: options.selectors,
    tenant,
  });
  assert.deepEqual(await snapshotTree(outputRoot), expected);
  return { diagnostics, tree: expected };
}

test("ungrouped main rendering is byte-identical to the legacy Python golden", async () => {
  const root = await committedRoot();
  const deployment = { overlay: ".", roots: {} } as const;
  const topology = loadedRootTopology({
    deployment,
    root,
    selectors: ["zpa_segment_group"],
    tenant: "zs2",
  }).topology;
  const actual = renderEnvironmentMain({
    deployment,
    environmentDirectory: "envs/zs2/zpa_segment_group",
    label: "zpa_segment_group",
    members: ["zpa_segment_group"],
    root,
    tenant: "zs2",
    topology,
  });
  assert.equal(actual, [
    "# GENERATED by engine.gen_env for tenant 'zs2' — do not edit.",
    "# Regenerate: make gen-env TENANT=zs2",
    "",
    "terraform {",
    '  required_version = ">= 1.5"',
    "  required_providers {",
    "    zpa = {",
    '      source = "zscaler/zpa"',
    "    }",
    "  }",
    "  # local state — opt into remote state with",
    "  # make gen-env TENANT=zs2 BACKEND=azurerm",
    "}",
    "",
    'provider "zpa" {',
    "  # credentials via provider environment variables",
    "}",
    "",
    'variable "items" {',
    "  # opaque at the root; the module enforces the strict type.",
    "  type = any",
    "}",
    "",
    'module "zpa_segment_group" {',
    '  source = "../../../modules/zpa_segment_group"',
    "  items = var.items",
    "}",
    "",
  ].join("\n"));
});

test("complete generated root trees match Python for ungrouped, grouped/bound, singleton HCL, and slug roots", async (context) => {
  await pythonThenNodeTree({
    deployment: {},
    prepare: async (workspace) => writeJson(
      path.join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"),
      { items: { example: { configured_name: "Example", custom_category: true, urls: [] } } },
    ),
    selectors: ["zia_url_categories"],
  }, context);

  const grouped = await pythonThenNodeTree({
    backend: "azurerm",
    deployment: {
      roots: {
        zpa: {
          bind_references: true,
          groups: {
            zpa_custom: ["zpa_application_segment", "zpa_segment_group"],
          },
        },
      },
    },
    prepare: async (workspace) => {
      const config = path.join(workspace, "config", "tenant");
      await writeJson(path.join(config, "zpa_application_segment.auto.tfvars.json"), {
        zpa_application_segment_items: { app: { segment_group_id: "sg-1" } },
      });
      await writeJson(path.join(config, "zpa_segment_group.auto.tfvars.json"), {
        zpa_segment_group_items: { group: { description: "Group", enabled: true, name: "Group" } },
      });
      await writeJson(path.join(config, "zpa_application_segment.generated.expressions.json"), {
        resources: {
          "zpa_application_segment.app": {
            segment_group_id: { expression: 'module.zpa_segment_group.items["generated"].id' },
          },
        },
      });
      await writeJson(path.join(config, "zpa_application_segment.expressions.json"), {
        resources: {
          "zpa_application_segment.app": {
            segment_group_id: { expression: 'module.zpa_segment_group.items["operator"].id' },
          },
        },
      });
    },
    selectors: ["zpa_application_segment"],
  }, context);
  assert.equal(grouped.tree["tenant/.backend"], "azurerm\n");
  assert.match(grouped.tree["tenant/zpa_custom/expression_bindings.tf"] ?? "", /operator/);
  assert.doesNotMatch(grouped.tree["tenant/zpa_custom/expression_bindings.tf"] ?? "", /generated/);

  const hcl = await pythonThenNodeTree({
    deployment: {
      tfvars_format: "hcl",
      roots: { zpa: { groups: { zpa_solo: ["zpa_segment_group"] } } },
    },
    prepare: async (workspace) => {
      const config = path.join(workspace, "config", "tenant");
      await mkdir(config, { recursive: true });
      await writeFile(path.join(config, "zpa_segment_group.auto.tfvars"), "zpa_segment_group_items = {}\n");
      await writeJson(path.join(config, "zpa_segment_group.expressions.json"), {
        resources: {
          "zpa_segment_group.group": {
            description: { expression: "var.description" },
          },
        },
      });
    },
    selectors: ["zpa_segment_group"],
  }, context);
  assert.equal(hcl.diagnostics.some((message) => message.includes("hcl tfvars; validation reads json only")), true);
  assert.doesNotMatch(hcl.tree["tenant/zpa_solo/tests/smoke.tftest.hcl"] ?? "", /config_plan/);

  const slug = await pythonThenNodeTree({
    deployment: { roots: { zia: { strategy: "slug" } } },
    selectors: ["zia_url_categories"],
  }, context);
  assert.match(slug.tree["tenant/zia_url/main.tf"] ?? "", /module "zia_url_filtering_rules"/);
  assert.doesNotMatch(
    slug.tree["tenant/zia_url/main.tf"] ?? "",
    /module "zia_url_categories_predefined"/,
  );
});

test("the complete full-profile generated root tree is byte-identical to Python", async (context) => {
  const result = await pythonThenNodeTree({
    deployment: {},
    selectors: [],
    tenant: "full-profile-parity",
  }, context);
  assert.equal(Object.keys(result.tree).length, 151 * 3);
});

test("operator precedence, stale generated filtering, stale HCL removal, and cycles are fail-closed", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-gen-env-bindings-");
  const deploymentPath = path.join(workspace, "deployment.json");
  await writeJson(deploymentPath, {
    overlay: workspace,
    roots: {
      zpa: {
        bind_references: true,
        groups: { zpa_custom: ["zpa_application_segment", "zpa_server_group"] },
      },
    },
  });
  const config = path.join(workspace, "config", "tenant");
  await writeJson(path.join(config, "zpa_application_segment.auto.tfvars.json"), {
    zpa_application_segment_items: { app: { description: "literal", segment_group_id: "sg-1" } },
  });
  await writeJson(path.join(config, "zpa_application_segment.generated.expressions.json"), {
    resources: {
      "zpa_application_segment.app": {
        description: { expression: 'module.zpa_server_group.items["server"].id' },
        segment_group_id: { expression: 'module.zpa_segment_group.items["stale"].id' },
      },
    },
  });
  const output = path.join(workspace, "generated");
  const diagnostics: string[] = [];
  await generateEnvironmentRoots({
    deployment: await loadDeployment(deploymentPath),
    formatHcl: async (source) => source,
    onDiagnostic: (message) => diagnostics.push(message),
    outputRoot: output,
    root: await committedRoot(),
    selectors: ["zpa_application_segment"],
    tenant: "tenant",
  });
  const overlay = await readFile(path.join(output, "tenant", "zpa_custom", "expression_bindings.tf"), "utf8");
  assert.match(overlay, /module\.zpa_server_group/);
  assert.doesNotMatch(overlay, /module\.zpa_segment_group/);
  assert.equal(diagnostics.some((message) => message.includes("target zpa_segment_group not in root members")), true);

  const deployment = JSON.parse(await readFile(deploymentPath, "utf8")) as Record<string, unknown>;
  ((deployment.roots as { zpa: Record<string, unknown> }).zpa).bind_references = false;
  await writeJson(deploymentPath, deployment);
  await generateEnvironmentRoots({
    deployment: await loadDeployment(deploymentPath),
    formatHcl: async (source) => source,
    onDiagnostic: (message) => diagnostics.push(message),
    outputRoot: output,
    root: await committedRoot(),
    selectors: ["zpa_application_segment"],
    tenant: "tenant",
  });
  await assert.rejects(
    () => access(path.join(output, "tenant", "zpa_custom", "expression_bindings.tf")),
    /ENOENT/,
  );
  assert.equal(diagnostics.some((message) => message.includes("bind_references disabled")), true);

  ((deployment.roots as { zpa: Record<string, unknown> }).zpa).bind_references = true;
  (deployment.roots as { zpa: Record<string, unknown> }).zpa.groups = {
    zpa_cycle: ["zpa_application_segment", "zpa_segment_group"],
  };
  await writeJson(deploymentPath, deployment);
  await writeJson(path.join(config, "zpa_segment_group.auto.tfvars.json"), {
    zpa_segment_group_items: { group: { description: "literal" } },
  });
  await writeJson(path.join(config, "zpa_application_segment.expressions.json"), {
    resources: {
      "zpa_application_segment.app": {
        description: { expression: 'module.zpa_segment_group.items["group"].id' },
      },
    },
  });
  await writeJson(path.join(config, "zpa_segment_group.expressions.json"), {
    resources: {
      "zpa_segment_group.group": {
        description: { expression: 'module.zpa_application_segment.items["app"].id' },
      },
    },
  });
  const cycleDeployment = await loadDeployment(deploymentPath);
  const cycleRoot = await committedRoot();
  await assert.rejects(
    () => generateEnvironmentRoots({
      deployment: cycleDeployment,
      formatHcl: async (source) => source,
      outputRoot: output,
      root: cycleRoot,
      selectors: ["zpa_application_segment"],
      tenant: "tenant",
    }),
    /expression binding cycle detected.*resolve one direction/,
  );
});

test("dangling artifact paths retain Python existence and stale-file semantics", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-gen-env-dangling-");
  const deploymentPath = path.join(workspace, "deployment.json");
  const outputRoot = path.join(workspace, "generated");
  await writeJson(deploymentPath, {
    module_dir: path.join(workspace, "modules"),
    overlay: workspace,
    roots: { zia: { bind_references: true } },
  });
  const configDirectory = path.join(workspace, "config", "tenant");
  await mkdir(configDirectory, { recursive: true });
  for (const name of [
    "zia_url_categories.auto.tfvars.json",
    "zia_url_categories.expressions.json",
    "zia_url_categories.generated.expressions.json",
  ]) {
    await symlink(`missing-${name}`, path.join(configDirectory, name));
  }
  const expressionPath = path.join(
    outputRoot,
    "tenant",
    "zia_url_categories",
    "expression_bindings.tf",
  );
  const backendPath = path.join(outputRoot, "tenant", ".backend");
  const seedDanglingOutputs = async (): Promise<void> => {
    await mkdir(path.dirname(expressionPath), { recursive: true });
    await symlink("missing-expression-bindings.tf", expressionPath);
    await symlink("missing-backend", backendPath);
  };
  await seedDanglingOutputs();
  const python = spawnSync(PYTHON_ORACLE, [
    "-c",
    "import sys; from engine.gen_env import generate_env; generate_env('tenant', out_root=sys.argv[1], selectors=['zia_url_categories'])",
    outputRoot,
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deploymentPath,
      INFRAWRIGHT_PACK_PROFILE: path.join(ROOT, "packsets", "full.json"),
    },
  });
  assert.equal(python.status, 0, `${python.stdout}\n${python.stderr}`);
  const expected = await snapshotTree(outputRoot);
  assert.doesNotMatch(expected["tenant/zia_url_categories/main.tf"] ?? "", /backend "/);
  assert.doesNotMatch(expected["tenant/zia_url_categories/tests/smoke.tftest.hcl"] ?? "", /config_plan/);
  assert.equal((await lstat(expressionPath)).isSymbolicLink(), true);
  assert.equal((await lstat(backendPath)).isSymbolicLink(), true);

  await rm(path.join(outputRoot, "tenant", "zia_url_categories"), { force: true, recursive: true });
  await rm(backendPath, { force: true });
  await seedDanglingOutputs();
  await generateEnvironmentRoots({
    deployment: await loadDeployment(deploymentPath),
    formatHcl: terraformHclFormatter({ executable: TERRAFORM }),
    outputRoot,
    root: await committedRoot(),
    selectors: ["zia_url_categories"],
    tenant: "tenant",
  });
  assert.deepEqual(await snapshotTree(outputRoot), expected);
  assert.equal((await lstat(expressionPath)).isSymbolicLink(), true);
  assert.equal((await lstat(backendPath)).isSymbolicLink(), true);
});

test("invalid Python-incompatible expression whitespace cannot partially rewrite a root", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-gen-env-whitespace-");
  const deploymentPath = path.join(workspace, "deployment.json");
  await writeJson(deploymentPath, { overlay: workspace, roots: {} });
  await writeJson(path.join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"), {
    items: { example: { configured_name: "Example" } },
  });
  await writeJson(path.join(workspace, "config", "tenant", "zia_url_categories.expressions.json"), {
    resources: {
      "zia_url_categories.example": {
        configured_name: { expression: "[\uFEFF]" },
      },
    },
  });
  const rootDirectory = path.join(workspace, "envs", "tenant", "zia_url_categories");
  const mainPath = path.join(rootDirectory, "main.tf");
  await mkdir(rootDirectory, { recursive: true });
  await writeFile(mainPath, "preexisting root\n", "utf8");
  const invalidDeployment = await loadDeployment(deploymentPath);
  const invalidRoot = await committedRoot();
  await assert.rejects(
    () => generateEnvironmentRoots({
      deployment: invalidDeployment,
      formatHcl: async (source) => source,
      root: invalidRoot,
      selectors: ["zia_url_categories"],
      tenant: "tenant",
    }),
    /outside the v1 allowlist/,
  );
  assert.equal(await readFile(mainPath, "utf8"), "preexisting root\n");
});

test("backend marker survives regeneration and profile/reduced-pack variants generate without Python", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-gen-env-profiles-");
  const deployment = { overlay: workspace, roots: {} } as const;
  const output = path.join(workspace, "generated");
  const root = await committedRoot();
  await generateEnvironmentRoots({
    backend: "azurerm",
    deployment,
    formatHcl: async (source) => source,
    outputRoot: output,
    root,
    selectors: ["zia_url_categories"],
    tenant: "tenant",
  });
  await generateEnvironmentRoots({
    deployment,
    formatHcl: async (source) => source,
    outputRoot: output,
    root,
    selectors: ["zia_url_categories"],
    tenant: "tenant",
  });
  assert.match(await readFile(path.join(output, "tenant", "zia_url_categories", "main.tf"), "utf8"), /backend "azurerm"/);

  const explicitEmpty = path.join(workspace, "empty-backend");
  const emptyBackendResult = await generateEnvironmentRoots({
    backend: "",
    deployment,
    formatHcl: async (source) => source,
    outputRoot: explicitEmpty,
    root,
    selectors: ["zia_url_categories"],
    tenant: "tenant",
  });
  assert.equal(emptyBackendResult.backend, null);
  assert.doesNotMatch(
    await readFile(path.join(explicitEmpty, "tenant", "zia_url_categories", "main.tf"), "utf8"),
    /backend "/,
  );

  const cases = [
    ["full.json", "zia_url_categories"],
    ["empty.json", null],
    ["aws.json", null],
    ["cloudflare.json", null],
    ["google.json", null],
    ["netbox.json", null],
    ["zia.json", "zia_url_categories"],
    ["zpa.json", "zpa_segment_group"],
    ["zcc.json", "zcc_failopen_policy"],
    ["ztc.json", "ztc_account_groups"],
    ["zscaler.json", "zia_url_categories"],
  ] as const;
  for (const [profile, selector] of cases) {
    const packsRoot = await reducedPackRootForProfile(workspace, profile);
    const selectedRoot = await committedRoot(profile, packsRoot);
    const target = path.join(workspace, profile);
    const result = await generateEnvironmentRoots({
      deployment: { overlay: workspace, roots: {} },
      formatHcl: async (source) => source,
      outputRoot: target,
      root: selectedRoot,
      selectors: selector === null ? [] : [selector],
      tenant: "profile",
    });
    assert.equal(result.roots.length, selector === null ? 0 : 1, profile);
  }

  const reduced = path.join(workspace, "reduced-packs");
  await mkdir(path.join(reduced, "_shared"), { recursive: true });
  await cp(path.join(ROOT, "packs", "zcc"), path.join(reduced, "zcc"), { recursive: true });
  await cp(path.join(ROOT, "packs", "_shared", "zscaler"), path.join(reduced, "_shared", "zscaler"), { recursive: true });
  const reducedRoot = await committedRoot("zcc.json", reduced);
  const reducedResult = await generateEnvironmentRoots({
    deployment: { overlay: workspace, roots: {} },
    formatHcl: async (source) => source,
    outputRoot: path.join(workspace, "reduced-output"),
    root: reducedRoot,
    selectors: ["zcc_failopen_policy"],
    tenant: "reduced",
  });
  assert.equal(reducedResult.roots.length, 1);
});

test("make gen-env is Python-disabled and writes a real formatted root", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-make-gen-env-");
  const deploymentPath = path.join(workspace, "deployment.json");
  await writeJson(deploymentPath, { overlay: workspace, roots: {} });
  const packsRoot = await reducedPackRootForProfile(workspace, "zia.json");
  const result = spawnSync("make", [
    "gen-env",
    "TENANT=tenant",
    "RESOURCE=zia_url_categories",
    `TF=${TERRAFORM}`,
    "PACK_PROFILE=packsets/zia.json",
    "PACK_CATALOG=packsets/full.json",
    "PYTHON=/python-must-not-run",
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deploymentPath,
      INFRAWRIGHT_PACKS: packsRoot,
    },
  });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  assert.equal(`${result.stdout}${result.stderr}`.includes("python-must-not-run"), false);
  const main = await readFile(path.join(workspace, "envs", "tenant", "zia_url_categories", "main.tf"), "utf8");
  assert.match(main, /source = "zscaler\/zia"/);
  await access(path.join(workspace, "envs", "tenant", "zia_url_categories", "README.md"));
  await access(path.join(workspace, "envs", "tenant", "zia_url_categories", "tests", "smoke.tftest.hcl"));
});
