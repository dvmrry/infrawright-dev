import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync, type SpawnSyncReturns } from "node:child_process";
import { chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";

const ROOT = process.cwd();
const RESOURCE = "sample_resource";

function command(
  executable: string,
  arguments_: readonly string[],
  environment: NodeJS.ProcessEnv,
): SpawnSyncReturns<string> {
  return spawnSync(executable, [...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment,
  });
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

async function writeTerraform(file: string, plan: object): Promise<void> {
  await writeFile(file, [
    "#!/bin/sh",
    `printf '%s' ${shellLiteral(JSON.stringify(plan))}`,
    "",
  ].join("\n"), { mode: 0o700 });
  await chmod(file, 0o700);
}

function plan(change: object): object {
  return {
    format_version: "1.2",
    terraform_version: "1.15.4",
    complete: true,
    errored: false,
    resource_changes: [{
      address: 'sample_resource.this["one"]',
      type: RESOURCE,
      change,
    }],
    output_changes: {},
  };
}

async function fixture(
  context: { after(callback: () => Promise<unknown> | unknown): void },
): Promise<{
  readonly workspace: string;
  readonly packs: string;
  readonly profile: string;
  readonly deployment: string;
  readonly envDir: string;
  readonly terraform: string;
  readonly environment: NodeJS.ProcessEnv;
}> {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "assessment-cli-"));
  context.after(() => rm(workspace, { recursive: true, force: true }));
  const packs = path.join(workspace, "packs");
  const profile = path.join(workspace, "pack-set.json");
  const deployment = path.join(workspace, "deployment.json");
  const envDir = path.join(workspace, "envs", "tenant", RESOURCE);
  const moduleDir = path.join(workspace, "modules", RESOURCE);
  const varFile = path.join(
    workspace,
    "config",
    "tenant",
    `${RESOURCE}.auto.tfvars.json`,
  );
  const terraform = path.join(workspace, "terraform-fake");
  await writeJson(path.join(packs, "sample", "pack.json"), {
    pin: "1.0.0",
    provider_prefixes: { sample_: "sample" },
    provider_sources: { sample: "example/sample" },
    vendor: "sample",
    provider_config: {
      requirements: [{
        id: "sample_attribution",
        setting: "add_attribution",
        value: false,
        reason: "provider adds attribution",
        plan_paths: ["terraform_labels.attribution"],
        remediation: {
          kind: "provider_argument",
          mode: "required_external",
          evidence: "sample.md",
        },
      }],
    },
  });
  await writeJson(path.join(packs, "sample", "registry.json"), {
    [RESOURCE]: { generate: true, product: "sample" },
  });
  await writeJson(profile, {
    kind: "infrawright.pack-set",
    version: 1,
    packs: ["sample"],
    shared: [],
  });
  await writeJson(deployment, { overlay: workspace, roots: {} });
  await mkdir(envDir, { recursive: true });
  await mkdir(moduleDir, { recursive: true });
  await mkdir(path.dirname(varFile), { recursive: true });
  await writeFile(path.join(moduleDir, "main.tf"), "# module\n", "utf8");
  await writeFile(path.join(envDir, "main.tf"), [
    `module "${RESOURCE}" {`,
    `  source = "${path.relative(envDir, moduleDir)}"`,
    `  items = var.${RESOURCE}_items`,
    "}",
    "",
  ].join("\n"), "utf8");
  await writeFile(varFile, "{}\n", "utf8");
  await writeFile(path.join(envDir, "tfplan"), "opaque plan bytes\n", {
    mode: 0o600,
  });
  await writeFile(path.join(envDir, "tfplan.sources"), `${JSON.stringify(
    await planFingerprintV2({
      envDir,
      varFiles: [varFile],
      memberTypes: [RESOURCE],
      backendConfig: null,
      backendKey: null,
    }),
  )}\n`, { mode: 0o600 });
  return {
    workspace,
    packs,
    profile,
    deployment,
    envDir,
    terraform,
    environment: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deployment,
      INFRAWRIGHT_PACKS: packs,
      INFRAWRIGHT_PACK_PROFILE: profile,
      TF: terraform,
    },
  };
}

test("operational assessment reports, diagnostics, and exits match Python", async (context) => {
  const item = await fixture(context);
  const built = command(
    process.execPath,
    ["scripts/build-metadata-cli.mjs"],
    item.environment,
  );
  assert.equal(built.status, 0, built.stderr);
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const cases = [
    {
      name: "clean",
      operation: "assert-clean",
      plan: plan({ actions: ["no-op"], before: {}, after: {} }),
      policy: null,
    },
    {
      name: "blocked guidance",
      operation: "assert-adoptable",
      plan: plan({
        actions: ["update"],
        before: { terraform_labels: {} },
        after: { terraform_labels: { attribution: "true" } },
      }),
      policy: null,
    },
    {
      name: "tolerated",
      operation: "assert-adoptable",
      plan: plan({
        actions: ["update"],
        before: { status: "old" },
        after: { status: "new" },
      }),
      policy: {
        version: 1,
        resource_types: {
          [RESOURCE]: {
            plan_tolerate: [{
              path: "status",
              reason: "consumer accepts status",
              approved_by: "unit",
            }],
          },
        },
      },
    },
  ] as const;
  for (const selected of cases) {
    await context.test(selected.name, async () => {
      await writeTerraform(item.terraform, selected.plan);
      const pythonReport = path.join(item.workspace, `${selected.name}.python.json`);
      const nodeReport = path.join(item.workspace, `${selected.name}.node.json`);
      const policyPath = selected.policy === null
        ? null
        : path.join(item.workspace, `${selected.name}.policy.json`);
      if (policyPath !== null) await writeJson(policyPath, selected.policy);
      const pythonArguments = [
        "-m", "engine.ops", selected.operation,
        "--tenant", "tenant",
        "--report", pythonReport,
        ...(policyPath === null ? [] : ["--policy", policyPath]),
      ];
      const nodeArguments = [
        cli,
        selected.operation,
        "--tenant", "tenant",
        "--report", nodeReport,
        "--terraform", item.terraform,
        "--root", item.packs,
        "--profile", item.profile,
        "--catalog", item.profile,
        "--deployment", item.deployment,
        ...(policyPath === null ? [] : ["--policy", policyPath]),
      ];
      const python = command(PYTHON_ORACLE, pythonArguments, item.environment);
      const node = command(process.execPath, nodeArguments, item.environment);
      assert.equal(node.status, python.status, node.stderr);
      assert.equal(node.stdout, python.stdout, selected.name);
      assert.equal(node.stderr, python.stderr, selected.name);
      assert.equal(
        await readFile(nodeReport, "utf8"),
        await readFile(pythonReport, "utf8"),
        selected.name,
      );
    });
  }
});

test("Make assessment targets run with Python deliberately unavailable", async (context) => {
  const item = await fixture(context);
  await writeTerraform(item.terraform, plan({
    actions: ["no-op"],
    before: {},
    after: {},
  }));
  const environment = {
    ...item.environment,
    PATH: process.env.PATH,
    PYTHON: "/definitely/missing/python",
  };
  for (const target of ["assert-clean", "assert-adoptable"]) {
    const result = command("make", [
      "--no-print-directory",
      `DEPLOYMENT=${item.deployment}`,
      `PACK_PROFILE=${item.profile}`,
      `PACK_CATALOG=${item.profile}`,
      `TF=${item.terraform}`,
      "TENANT=tenant",
      target,
    ], environment);
    assert.equal(result.status, 0, `${target}\n${result.stdout}\n${result.stderr}`);
    assert.equal(result.stderr.includes("/definitely/missing/python"), false);
  }
});

test("no-saved-plan CLI failure and error report match Python without Terraform", async (context) => {
  const item = await fixture(context);
  await rm(path.join(item.envDir, "tfplan"));
  await rm(path.join(item.envDir, "tfplan.sources"));
  const pythonReport = path.join(item.workspace, "no-plans.python.json");
  const nodeReport = path.join(item.workspace, "no-plans.node.json");
  const python = command(PYTHON_ORACLE, [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
  ], { ...item.environment, TF: "/definitely/missing/terraform" });
  const node = command(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "assert-clean",
    "--tenant", "tenant",
    "--report", nodeReport,
    "--terraform", "/definitely/missing/terraform",
    "--root", item.packs,
    "--profile", item.profile,
    "--catalog", item.profile,
    "--deployment", item.deployment,
  ], item.environment);
  assert.equal(node.status, python.status);
  assert.equal(node.status, 1);
  assert.equal(node.stdout, python.stdout);
  assert.equal(node.stderr, python.stderr);
  assert.equal(await readFile(nodeReport, "utf8"), await readFile(pythonReport, "utf8"));
});

test("missing-fingerprint assessment failure retains the report contract", async (context) => {
  const item = await fixture(context);
  await writeTerraform(item.terraform, plan({
    actions: ["no-op"],
    before: {},
    after: {},
  }));
  await rm(path.join(item.envDir, "tfplan.sources"));
  const report = path.join(item.workspace, "missing-fingerprint.json");
  const result = command(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "assert-clean",
    "--tenant", "tenant",
    "--report", report,
    "--terraform", item.terraform,
    "--root", item.packs,
    "--profile", item.profile,
    "--catalog", item.profile,
    "--deployment", item.deployment,
  ], item.environment);
  assert.equal(result.status, 1);
  const parsed = JSON.parse(await readFile(report, "utf8")) as {
    summary: { status: string };
    error: { kind: string; message: string };
  };
  assert.equal(parsed.summary.status, "error");
  assert.equal(parsed.error.kind, "assessment_error");
  assert.ok(parsed.error.message.length > 0);
});

test("selection failures retain the Python error-report contract", async (context) => {
  const item = await fixture(context);
  const pythonReport = path.join(item.workspace, "selection.python.json");
  const nodeReport = path.join(item.workspace, "selection.node.json");
  const python = command(PYTHON_ORACLE, [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
    "missing_resource",
  ], item.environment);
  const node = command(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "assert-clean",
    "--tenant", "tenant",
    "--report", nodeReport,
    "--resource", "missing_resource",
    "--terraform", item.terraform,
    "--root", item.packs,
    "--profile", item.profile,
    "--catalog", item.profile,
    "--deployment", item.deployment,
  ], item.environment);
  assert.equal(node.status, python.status);
  assert.equal(node.stdout, python.stdout);
  assert.equal(node.stderr, python.stderr);
  assert.equal(await readFile(nodeReport, "utf8"), await readFile(pythonReport, "utf8"));
});

test("assessment CLI retains usage exits and writes loader/parser error reports", async (context) => {
  const item = await fixture(context);
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");

  const duplicate = command(process.execPath, [
    cli,
    "assert-clean",
    "--report", path.join(item.workspace, "first.json"),
    "--report", path.join(item.workspace, "second.json"),
  ], item.environment);
  assert.equal(duplicate.status, 2);
  assert.equal(duplicate.stderr, "error: --report may be specified only once\n");

  const malformedReport = path.join(item.workspace, "malformed-deployment.json");
  await writeFile(item.deployment, "{bad json\n", "utf8");
  const malformed = command(process.execPath, [
    cli,
    "assert-clean",
    "--tenant", "tenant",
    "--report", malformedReport,
    "--root", item.packs,
    "--profile", item.profile,
    "--catalog", item.profile,
    "--deployment", item.deployment,
  ], item.environment);
  assert.equal(malformed.status, 2);
  const loaderReport = JSON.parse(await readFile(malformedReport, "utf8")) as {
    summary: { status: string };
    error: { kind: string };
  };
  assert.equal(loaderReport.summary.status, "error");
  assert.equal(loaderReport.error.kind, "assessment_error");
});

test("invalid Terraform show JSON retains the legacy diagnostic, report, and exit", async (context) => {
  const item = await fixture(context);
  await writeFile(item.terraform, "#!/bin/sh\nprintf '%s' invalid-json\n", {
    mode: 0o700,
  });
  await chmod(item.terraform, 0o700);
  const pythonReport = path.join(item.workspace, "invalid-show.python.json");
  const nodeReport = path.join(item.workspace, "invalid-show.node.json");
  const python = command(PYTHON_ORACLE, [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
  ], { ...item.environment, TF: item.terraform });
  const node = command(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "assert-clean",
    "--tenant", "tenant",
    "--report", nodeReport,
    "--terraform", item.terraform,
    "--root", item.packs,
    "--profile", item.profile,
    "--catalog", item.profile,
    "--deployment", item.deployment,
  ], item.environment);
  assert.equal(node.status, python.status);
  assert.equal(node.status, 2);
  assert.equal(node.stdout, python.stdout);
  assert.equal(node.stderr, python.stderr);
  assert.equal(await readFile(nodeReport, "utf8"), await readFile(pythonReport, "utf8"));
});

test("invalid policy and missing Terraform retain legacy diagnostics and reports", async (context) => {
  const item = await fixture(context);
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const invalidPolicy = path.join(item.workspace, "invalid-policy.json");
  await writeFile(invalidPolicy, '{"version":1,"resource_types":oops}\n', "utf8");

  for (const selected of [
    {
      name: "invalid-policy",
      operation: "assert-adoptable",
      policy: invalidPolicy,
      terraform: item.terraform,
    },
    {
      name: "missing-terraform",
      operation: "assert-clean",
      policy: null,
      terraform: "/definitely/missing/terraform",
    },
  ]) {
    const pythonReport = path.join(item.workspace, `${selected.name}.python.json`);
    const nodeReport = path.join(item.workspace, `${selected.name}.node.json`);
    const pythonArgs = [
      "-m", "engine.ops", selected.operation,
      "--tenant", "tenant",
      "--report", pythonReport,
      ...(selected.policy === null ? [] : ["--policy", selected.policy]),
    ];
    const nodeArgs = [
      cli,
      selected.operation,
      "--tenant", "tenant",
      "--report", nodeReport,
      "--terraform", selected.terraform,
      "--root", item.packs,
      "--profile", item.profile,
      "--catalog", item.profile,
      "--deployment", item.deployment,
      ...(selected.policy === null ? [] : ["--policy", selected.policy]),
    ];
    const python = command(PYTHON_ORACLE, pythonArgs, {
      ...item.environment,
      TF: selected.terraform,
    });
    const node = command(process.execPath, nodeArgs, item.environment);
    assert.equal(node.status, python.status, selected.name);
    assert.equal(node.stdout, python.stdout, selected.name);
    assert.equal(node.stderr, python.stderr, selected.name);
    assert.equal(
      await readFile(nodeReport, "utf8"),
      await readFile(pythonReport, "utf8"),
      selected.name,
    );
  }
});

test("full, empty, provider, Zscaler, and reduced profiles reach generic assessment", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "assessment-profiles-"));
  context.after(() => rm(workspace, { recursive: true, force: true }));
  const deployment = path.join(workspace, "deployment.json");
  await writeJson(deployment, { overlay: workspace, roots: {} });
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  for (const name of ["full", "empty", "aws", "zscaler", "zia"]) {
    const profile = path.join(ROOT, "packsets", `${name}.json`);
    const packs = name === "full"
      ? path.join(ROOT, "packs")
      : path.join(workspace, `packs-${name}`);
    if (name !== "full") {
      const selection = JSON.parse(await readFile(profile, "utf8")) as {
        packs: string[];
        shared: string[];
      };
      await mkdir(packs, { recursive: true });
      for (const pack of selection.packs) {
        await cp(path.join(ROOT, "packs", pack), path.join(packs, pack), {
          recursive: true,
        });
      }
      for (const shared of selection.shared) {
        await cp(
          path.join(ROOT, "packs", "_shared", shared),
          path.join(packs, "_shared", shared),
          { recursive: true },
        );
      }
    }
    const result = command(process.execPath, [
      cli,
      "assert-clean",
      "--tenant", "tenant",
      "--terraform", "/definitely/missing/terraform",
      "--root", packs,
      "--profile", profile,
      "--catalog", path.join(ROOT, "packsets", "full.json"),
      "--deployment", deployment,
    ], process.env);
    assert.equal(result.status, 1, `${name}\n${result.stderr}`);
    assert.match(result.stderr, /no saved plans to check/u);
    assert.equal(result.stderr.includes("resolve Terraform"), false);
  }
});
