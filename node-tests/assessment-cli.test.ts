import assert from "node:assert/strict";
import { spawnSync, type SpawnSyncReturns } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { assertCliFailureExtendsLegacy } from "./cli-failure-assertions.js";
import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";

const ROOT = process.cwd();
const RESOURCE = "sample_resource";
const AUTHORITY_SHA256 = "c6b46d67c75b38a171c072713a621ada1188a74e8e9f485eb063199331d04aff";
const ASSESSMENT_ROOT = "<ASSESSMENT_CLI_ROOT>";

interface FrozenBytes {
  readonly base64: string;
  readonly sha256: string;
  readonly size: number;
}

interface FrozenRecord {
  readonly arguments: readonly string[];
  readonly exit_status: number;
  readonly report_artifacts: readonly {
    readonly blob: string;
    readonly path: string;
  }[];
  readonly stderr: FrozenBytes;
  readonly stdout: FrozenBytes;
}

interface FrozenAuthority {
  readonly content_blobs: Readonly<Record<string, FrozenBytes>>;
  readonly kind: string;
  readonly record_count: number;
  readonly records: readonly FrozenRecord[];
  readonly schema_version: number;
  readonly suite: string;
}

const authorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-assessment-cli-v1.json",
));
assert.equal(
  createHash("sha256").update(authorityBytes).digest("hex"),
  AUTHORITY_SHA256,
  "frozen CPython assessment CLI authority changed without re-adjudication",
);
const authority = JSON.parse(authorityBytes.toString("utf8")) as FrozenAuthority;
assert.equal(authority.kind, "python-engine-ops-delegation-authority");
assert.equal(authority.schema_version, 1);
assert.equal(authority.suite, "assessment-cli");
assert.equal(authority.record_count, 8);
assert.equal(authority.records.length, authority.record_count);
assert.equal(
  new Set(authority.records.map((record) => JSON.stringify(record.arguments))).size,
  authority.record_count,
  "frozen assessment CLI invocations must map uniquely",
);

function normalizeAuthorityPath(value: string, workspace: string): string {
  return value.replaceAll(workspace, ASSESSMENT_ROOT);
}

function frozenText(value: FrozenBytes): string {
  const bytes = Buffer.from(value.base64, "base64");
  assert.equal(bytes.length, value.size);
  assert.equal(createHash("sha256").update(bytes).digest("hex"), value.sha256);
  return bytes.toString("utf8");
}

function frozenRecord(
  index: number,
  pythonArguments: readonly string[],
  workspace: string,
): FrozenRecord {
  const record = authority.records[index];
  assert.ok(record, `missing frozen assessment CLI record ${index}`);
  assert.deepEqual(
    record.arguments,
    pythonArguments.map((value) => normalizeAuthorityPath(value, workspace)),
    `frozen assessment CLI invocation ${index}`,
  );
  return record;
}

function frozenReport(record: FrozenRecord, pythonReport: string, workspace: string): string {
  assert.equal(record.report_artifacts.length, 1);
  const artifact = record.report_artifacts[0]!;
  assert.equal(artifact.path, normalizeAuthorityPath(pythonReport, workspace));
  const blob = authority.content_blobs[artifact.blob];
  assert.ok(blob, `missing frozen assessment report blob ${artifact.blob}`);
  return frozenText(blob);
}

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
      authorityIndex: 0,
      name: "clean",
      operation: "assert-clean",
      plan: plan({ actions: ["no-op"], before: {}, after: {} }),
      policy: null,
    },
    {
      authorityIndex: 1,
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
      authorityIndex: 2,
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
      const legacy = frozenRecord(selected.authorityIndex, pythonArguments, item.workspace);
      const node = command(process.execPath, nodeArguments, item.environment);
      const legacyStdout = frozenText(legacy.stdout);
      const legacyStderr = frozenText(legacy.stderr);
      assert.equal(node.status, legacy.exit_status, node.stderr);
      assert.equal(node.stdout, legacyStdout, selected.name);
      if (selected.name === "blocked guidance") {
        assertCliFailureExtendsLegacy(node.stderr, legacyStderr, {
          category: "domain",
          code: "PLAN_NOT_ADOPTABLE",
          retryable: false,
        }, selected.name);
      } else {
        assert.equal(node.stderr, legacyStderr, selected.name);
      }
      assert.equal(
        normalizeAuthorityPath(await readFile(nodeReport, "utf8"), item.workspace),
        frozenReport(legacy, pythonReport, item.workspace),
        selected.name,
      );
    });
  }
});

test("Make assessment targets run with Python deliberately unavailable", async (context) => {
  const item = await fixture(context);
  const cleanPlan = plan({
    actions: ["no-op"],
    before: {},
    after: {},
  });
  const terraformData = path.join(item.workspace, "terraform-data");
  const terraformConfig = path.join(item.workspace, "terraform.rc");
  await writeFile(item.terraform, [
    "#!/bin/sh",
    `if [ "$TF_DATA_DIR" != '${terraformData}' ]; then exit 41; fi`,
    `if [ "$TF_CLI_CONFIG_FILE" != '${terraformConfig}' ]; then exit 42; fi`,
    'if [ "${TF_CLI_ARGS_show+x}" = x ]; then exit 43; fi',
    'if [ "${TF_LOG+x}" = x ]; then exit 44; fi',
    'if [ "${TF_REATTACH_PROVIDERS+x}" = x ]; then exit 45; fi',
    'if [ "${ZIA_USERNAME+x}" = x ]; then exit 46; fi',
    `printf '%s' ${shellLiteral(JSON.stringify(cleanPlan))}`,
    "",
  ].join("\n"), { mode: 0o700 });
  await chmod(item.terraform, 0o700);
  const environment = {
    ...item.environment,
    PATH: process.env.PATH,
    PYTHON: "/definitely/missing/python",
    TF_CLI_ARGS_show: "-destroy",
    TF_CLI_CONFIG_FILE: terraformConfig,
    TF_DATA_DIR: terraformData,
    TF_LOG: "TRACE",
    TF_REATTACH_PROVIDERS: "provider-process-secret",
    ZIA_USERNAME: "provider-secret",
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
  const pythonArguments = [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
  ];
  const legacy = frozenRecord(3, pythonArguments, item.workspace);
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
  const legacyStdout = frozenText(legacy.stdout);
  const legacyStderr = frozenText(legacy.stderr);
  assert.equal(node.status, legacy.exit_status);
  assert.equal(node.status, 1);
  assert.equal(node.stdout, legacyStdout);
  assertCliFailureExtendsLegacy(node.stderr, legacyStderr, {
    category: "domain",
    code: "NO_SAVED_PLANS",
    retryable: false,
  });
  assert.equal(
    normalizeAuthorityPath(await readFile(nodeReport, "utf8"), item.workspace),
    frozenReport(legacy, pythonReport, item.workspace),
  );
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
  const pythonArguments = [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
    "missing_resource",
  ];
  const legacy = frozenRecord(4, pythonArguments, item.workspace);
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
  assert.equal(node.status, legacy.exit_status);
  assert.equal(node.stdout, frozenText(legacy.stdout));
  assert.equal(node.stderr, frozenText(legacy.stderr));
  assert.equal(
    normalizeAuthorityPath(await readFile(nodeReport, "utf8"), item.workspace),
    frozenReport(legacy, pythonReport, item.workspace),
  );
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
  const pythonArguments = [
    "-m", "engine.ops", "assert-clean",
    "--tenant", "tenant",
    "--report", pythonReport,
  ];
  const legacy = frozenRecord(5, pythonArguments, item.workspace);
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
  assert.equal(node.status, legacy.exit_status);
  assert.equal(node.status, 2);
  assert.equal(node.stdout, frozenText(legacy.stdout));
  assert.equal(node.stderr, frozenText(legacy.stderr));
  assert.equal(
    normalizeAuthorityPath(await readFile(nodeReport, "utf8"), item.workspace),
    frozenReport(legacy, pythonReport, item.workspace),
  );
});

test("invalid policy and missing Terraform retain legacy diagnostics and reports", async (context) => {
  const item = await fixture(context);
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const invalidPolicy = path.join(item.workspace, "invalid-policy.json");
  await writeFile(invalidPolicy, '{"version":1,"resource_types":oops}\n', "utf8");

  for (const selected of [
    {
      authorityIndex: 6,
      name: "invalid-policy",
      operation: "assert-adoptable",
      policy: invalidPolicy,
      terraform: item.terraform,
    },
    {
      authorityIndex: 7,
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
    const legacy = frozenRecord(selected.authorityIndex, pythonArgs, item.workspace);
    const node = command(process.execPath, nodeArgs, item.environment);
    const legacyStdout = frozenText(legacy.stdout);
    const legacyStderr = frozenText(legacy.stderr);
    assert.equal(node.status, legacy.exit_status, selected.name);
    assert.equal(node.stdout, legacyStdout, selected.name);
    if (selected.name === "missing-terraform") {
      assertCliFailureExtendsLegacy(node.stderr, legacyStderr, {
        category: "internal",
        code: "ASSESSMENT_FAILED",
        retryable: false,
      }, selected.name);
    } else {
      assert.equal(node.stderr, legacyStderr, selected.name);
    }
    assert.equal(
      normalizeAuthorityPath(await readFile(nodeReport, "utf8"), item.workspace),
      frozenReport(legacy, pythonReport, item.workspace),
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
