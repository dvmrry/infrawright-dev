import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync, type SpawnSyncReturns } from "node:child_process";
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  applyExactSavedPlans,
  createExactPlanApplyTerraform,
  currentApplyBranch,
  type ExactPlanApplyTerraform,
} from "../node-src/domain/exact-plan-apply.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";
import type { PlanTerraformRequest } from "../node-src/domain/plan-lifecycle.js";
import type { Deployment } from "../node-src/domain/types.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const RESOURCE = "zia_url_categories";
const SECOND_RESOURCE = "zia_admin_users";
let packRootPromise: Promise<LoadedPackRoot> | undefined;

function committedRoot(): Promise<LoadedPackRoot> {
  packRootPromise ??= loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  return packRootPromise;
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-exact-apply-"));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeText(file: string, text: string): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, text, "utf8");
}

function plan(changes: readonly unknown[] = []): object {
  return {
    format_version: "1.2",
    terraform_version: "1.15.4",
    complete: true,
    errored: false,
    resource_changes: changes,
    output_changes: {},
  };
}

function change(
  actions: readonly string[],
  options: {
    readonly before?: unknown;
    readonly after?: unknown;
    readonly importing?: Readonly<Record<string, unknown>>;
  } = {},
): object {
  return {
    address: `${RESOURCE}.this["one"]`,
    type: RESOURCE,
    change: {
      actions,
      ...(options.before === undefined ? {} : { before: options.before }),
      ...(options.after === undefined ? {} : { after: options.after }),
      ...(options.importing === undefined ? {} : { importing: options.importing }),
    },
  };
}

interface Fixture {
  readonly backendConfig: string | null;
  readonly deployment: Deployment;
  readonly envDir: string;
  readonly fingerprintPath: string;
  readonly members: readonly string[];
  readonly root: LoadedPackRoot;
  readonly tfplan: string;
  readonly varFiles: readonly string[];
  readonly workspace: string;
}

async function fixture(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  options: {
    readonly backend?: boolean;
    readonly grouped?: boolean;
    readonly writePlan?: boolean;
  } = {},
): Promise<Fixture> {
  const workspace = await temporaryDirectory(context);
  const grouped = options.grouped ?? false;
  const members = grouped ? [RESOURCE, SECOND_RESOURCE] : [RESOURCE];
  const label = grouped ? "zia_group" : RESOURCE;
  const envDir = path.join(workspace, "envs", "tenant", label);
  const moduleLines: string[] = [];
  const varFiles: string[] = [];
  for (const member of members) {
    const moduleDirectory = path.join(workspace, "modules", member);
    await writeText(path.join(moduleDirectory, "main.tf"), "# module\n");
    moduleLines.push(
      `module "${member}" {`,
      `  source = "${path.relative(envDir, moduleDirectory)}"`,
      `  items = var.${member}_items`,
      "}",
      "",
    );
    const varFile = path.join(
      workspace,
      "config",
      "tenant",
      `${member}.auto.tfvars.json`,
    );
    await writeText(varFile, `{"${member}_items":{}}\n`);
    varFiles.push(varFile);
  }
  const backendConfig = options.backend === true
    ? path.join(workspace, "backend.hcl")
    : null;
  if (backendConfig !== null) {
    await writeText(backendConfig, "storage_account_name = \"example\"\n");
  }
  await writeText(path.join(envDir, "main.tf"), [
    ...(backendConfig === null
      ? []
      : ["terraform {", "  backend \"azurerm\" {}", "}", ""]),
    ...moduleLines,
  ].join("\n"));
  const tfplan = path.join(envDir, "tfplan");
  const fingerprintPath = path.join(envDir, "tfplan.sources");
  if (options.writePlan !== false) {
    await writeFile(tfplan, "opaque saved plan\n", { mode: 0o600 });
    await writeFile(fingerprintPath, `${JSON.stringify(await planFingerprintV2({
      envDir,
      varFiles,
      memberTypes: members,
      backendConfig,
      backendKey: backendConfig === null ? null : `tenant/${label}.tfstate`,
    }))}\n`, { mode: 0o600 });
  }
  const deployment: Deployment = {
    overlay: workspace,
    roots: grouped
      ? { zia: { groups: { [label]: members } } }
      : {},
  };
  return {
    backendConfig,
    deployment,
    envDir,
    fingerprintPath,
    members,
    root: await committedRoot(),
    tfplan,
    varFiles,
    workspace,
  };
}

class FakeTerraform implements ExactPlanApplyTerraform {
  readonly initialized: PlanTerraformRequest[] = [];
  readonly shown: { readonly directory: string; readonly snapshotPath: string }[] = [];
  readonly applied: { readonly directory: string }[] = [];
  currentPlan: unknown = plan();
  onInitialize?: (request: PlanTerraformRequest) => Promise<void> | void;
  onShow?: () => Promise<void> | void;
  onApply?: () => Promise<void> | void;

  async initialize(request: PlanTerraformRequest): Promise<void> {
    this.initialized.push(request);
    await this.onInitialize?.(request);
  }

  async show(request: {
    readonly directory: string;
    readonly snapshotPath: string;
  }): Promise<unknown> {
    this.shown.push(request);
    assert.equal((await stat(request.snapshotPath)).isFile(), true);
    await this.onShow?.();
    return this.currentPlan;
  }

  async apply(request: { readonly directory: string }): Promise<void> {
    this.applied.push(request);
    await this.onApply?.();
  }
}

function assertFailure(error: unknown, code: string): ProcessFailure {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return error;
}

async function run(
  item: Fixture,
  terraform: FakeTerraform,
  options: {
    readonly allowDestroy?: boolean;
    readonly allowNonMain?: boolean;
    readonly allowPlanChanges?: boolean;
    readonly branch?: string;
    readonly mainBranch?: string;
    readonly policyPath?: string;
    readonly selectors?: readonly string[];
    readonly tenant?: string | null;
  } = {},
): Promise<{ readonly diagnostics: readonly string[]; readonly result: { readonly applied: number } }> {
  const diagnostics: string[] = [];
  const result = await applyExactSavedPlans({
    allowDestroy: options.allowDestroy ?? false,
    allowNonMain: options.allowNonMain ?? false,
    allowPlanChanges: options.allowPlanChanges ?? false,
    backendConfig: item.backendConfig,
    currentBranch: async () => options.branch ?? "main",
    loadInputs: async () => ({ deployment: item.deployment, root: item.root }),
    mainBranch: options.mainBranch ?? null,
    onDiagnostic: (message) => diagnostics.push(message),
    policyPath: options.policyPath ?? null,
    selectors: options.selectors ?? [],
    tenant: options.tenant === undefined ? "tenant" : options.tenant,
    terraform,
    workspace: item.workspace,
  });
  return { diagnostics, result };
}

test("branch resolution preserves CI priority and git fallback", async () => {
  const fallback = async (): Promise<string> => "git-branch";
  assert.equal(await currentApplyBranch({
    cwd: ROOT,
    environment: {
      BUILD_SOURCEBRANCH: "refs/heads/ado",
      GITHUB_REF: "refs/heads/github",
      BITBUCKET_BRANCH: "bitbucket",
    },
    gitBranch: fallback,
  }), "ado");
  assert.equal(await currentApplyBranch({
    cwd: ROOT,
    environment: { GITHUB_REF: "refs/heads/github", BITBUCKET_BRANCH: "bitbucket" },
    gitBranch: fallback,
  }), "github");
  assert.equal(await currentApplyBranch({
    cwd: ROOT,
    environment: { BITBUCKET_BRANCH: "bitbucket" },
    gitBranch: fallback,
  }), "bitbucket");
  assert.equal(await currentApplyBranch({
    cwd: ROOT,
    environment: {},
    gitBranch: fallback,
  }), "git-branch");
  assert.equal(await currentApplyBranch({
    cwd: ROOT,
    environment: {},
    gitBranch: async () => { throw new Error("no git"); },
  }), "unknown");
});

test("clean import-only Apply names tfplan and removes only the saved pair", async (context) => {
  const item = await fixture(context);
  const keep = path.join(item.envDir, "imports.generated.tf");
  await writeText(keep, "# keep\n");
  await writeFile(item.fingerprintPath, `${JSON.stringify(await planFingerprintV2({
    envDir: item.envDir,
    varFiles: item.varFiles,
    memberTypes: item.members,
    backendConfig: null,
    backendKey: null,
  }))}\n`, { mode: 0o600 });
  const terraform = new FakeTerraform();
  terraform.currentPlan = plan([change(["create"], { importing: { id: "123" } })]);
  const completed = await run(item, terraform);

  assert.deepEqual(completed.result, { applied: 1 });
  assert.deepEqual(completed.diagnostics, [`== apply tenant/${RESOURCE}`]);
  assert.equal(terraform.initialized.length, 1);
  assert.equal(terraform.shown.length, 1);
  assert.deepEqual(terraform.applied, [{ directory: item.envDir }]);
  await assert.rejects(readFile(item.tfplan), /ENOENT/u);
  await assert.rejects(readFile(item.fingerprintPath), /ENOENT/u);
  assert.equal(await readFile(keep, "utf8"), "# keep\n");
});

test("branch, missing-plan, stale-after-init, and Apply-failure gates retain evidence", async (context) => {
  await context.test("branch refusal precedes input loading", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    let loaded = false;
    await assert.rejects(
      applyExactSavedPlans({
        allowDestroy: false,
        allowNonMain: false,
        allowPlanChanges: false,
        backendConfig: null,
        currentBranch: async () => "feature",
        loadInputs: async () => {
          loaded = true;
          return { deployment: item.deployment, root: item.root };
        },
        mainBranch: null,
        policyPath: null,
        selectors: [],
        tenant: "tenant",
        terraform,
        workspace: item.workspace,
      }),
      (error) => {
        assert.match(assertFailure(error, "APPLY_BRANCH_REFUSED").message, /'feature'/u);
        return true;
      },
    );
    assert.equal(loaded, false);
    assert.equal(terraform.initialized.length, 0);
    assert.doesNotReject(readFile(item.tfplan));
  });

  await context.test("explicit non-main and main override", async () => {
    const first = await fixture(context);
    const firstTerraform = new FakeTerraform();
    await run(first, firstTerraform, { allowNonMain: true, branch: "feature" });
    const second = await fixture(context);
    const secondTerraform = new FakeTerraform();
    await run(second, secondTerraform, { branch: "release", mainBranch: "release" });
  });

  await context.test("missing saved plans", async () => {
    const item = await fixture(context, { writePlan: false });
    const terraform = new FakeTerraform();
    await assert.rejects(run(item, terraform), (error) => {
      assertFailure(error, "NO_SAVED_PLANS");
      return true;
    });
    assert.equal(terraform.initialized.length, 0);
  });

  await context.test("init mutation", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    terraform.onInitialize = () => writeText(path.join(item.envDir, ".terraform.lock.hcl"), "# changed\n");
    await assert.rejects(run(item, terraform), (error) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "STALE_PLAN_SOURCES");
      return true;
    });
    assert.equal(terraform.shown.length, 0);
    assert.equal(terraform.applied.length, 0);
    assert.equal(await readFile(item.tfplan, "utf8"), "opaque saved plan\n");
  });

  await context.test("Terraform Apply failure", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    terraform.onApply = () => { throw new Error("apply failed"); };
    await assert.rejects(run(item, terraform), /apply failed/u);
    assert.equal(await readFile(item.tfplan, "utf8"), "opaque saved plan\n");
    assert.match(await readFile(item.fingerprintPath, "utf8"), /"version":2/u);
  });

  await context.test("saved plan mutation after show", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    terraform.onShow = () => writeText(item.tfplan, "replaced after assessment\n");
    await assert.rejects(run(item, terraform), (error) => {
      assertFailure(error, "SAVED_PLAN_CHANGED");
      return true;
    });
    assert.equal(terraform.applied.length, 0);
    assert.equal(await readFile(item.tfplan, "utf8"), "replaced after assessment\n");
    assert.match(await readFile(item.fingerprintPath, "utf8"), /"version":2/u);
  });
});

test("policy, blocked, broad-override, and destroy gates match legacy Apply", async (context) => {
  await context.test("tolerated update", async () => {
    const item = await fixture(context);
    const policyPath = path.join(item.workspace, "policy.json");
    await writeText(policyPath, `${JSON.stringify({
      version: 1,
      resource_types: {
        [RESOURCE]: {
          plan_tolerate: [{
            path: "status",
            reason: "unit",
            approved_by: "unit",
          }],
        },
      },
    })}\n`);
    const terraform = new FakeTerraform();
    terraform.currentPlan = plan([change(["update"], {
      before: { status: "old" },
      after: { status: "new" },
    })]);
    const completed = await run(item, terraform, { policyPath });
    assert.ok(completed.diagnostics.includes(
      `TOLERATED: tenant/${RESOURCE} saved plan has consumer-tolerated drift`,
    ));
  });

  await context.test("blocked update retains pair", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    terraform.currentPlan = plan([change(["update"], {
      before: { status: "old" },
      after: { status: "new" },
    })]);
    await assert.rejects(run(item, terraform), (error) => {
      assertFailure(error, "APPLY_BLOCKED_PLAN_REFUSED");
      return true;
    });
    assert.equal(terraform.applied.length, 0);
    assert.equal(await readFile(item.tfplan, "utf8"), "opaque saved plan\n");
  });

  await context.test("broad override is loud", async () => {
    const item = await fixture(context);
    const terraform = new FakeTerraform();
    terraform.currentPlan = plan([change(["update"], {
      before: { status: "old" },
      after: { status: "new" },
    })]);
    const completed = await run(item, terraform, { allowPlanChanges: true });
    assert.match(completed.diagnostics.join("\n"), /broad legacy override/u);
    assert.match(completed.diagnostics.join("\n"), /applying BLOCKED/u);
  });

  await context.test("destroy needs both overrides", async () => {
    const refused = await fixture(context);
    const refusedTerraform = new FakeTerraform();
    refusedTerraform.currentPlan = plan([change(["delete", "create"], {
      before: { status: "old" },
      after: { status: "new" },
    })]);
    await assert.rejects(
      run(refused, refusedTerraform, { allowPlanChanges: true }),
      (error) => {
        assertFailure(error, "APPLY_DESTROY_REFUSED");
        return true;
      },
    );
    const allowed = await fixture(context);
    const allowedTerraform = new FakeTerraform();
    allowedTerraform.currentPlan = refusedTerraform.currentPlan;
    await run(allowed, allowedTerraform, {
      allowDestroy: true,
      allowPlanChanges: true,
    });
    assert.equal(allowedTerraform.applied.length, 1);
  });
});

test("grouped remote root uses whole-root membership and exact backend init", async (context) => {
  const item = await fixture(context, { backend: true, grouped: true });
  const terraform = new FakeTerraform();
  await run(item, terraform, { selectors: [RESOURCE] });
  assert.equal(terraform.initialized.length, 1);
  assert.deepEqual(terraform.initialized[0], {
    backendConfig: item.backendConfig,
    backendKey: "tenant/zia_group.tfstate",
    directory: item.envDir,
    save: false,
    varFiles: [],
  });
  assert.deepEqual(terraform.applied, [{ directory: item.envDir }]);
});

test("multiple roots apply in stable order after prior successful cleanup", async (context) => {
  const item = await fixture(context);
  const secondEnv = path.join(item.workspace, "envs", "tenant", SECOND_RESOURCE);
  const secondModule = path.join(item.workspace, "modules", SECOND_RESOURCE);
  const secondConfig = path.join(
    item.workspace,
    "config",
    "tenant",
    `${SECOND_RESOURCE}.auto.tfvars.json`,
  );
  await writeText(path.join(secondModule, "main.tf"), "# module\n");
  await writeText(path.join(secondEnv, "main.tf"), [
    `module "${SECOND_RESOURCE}" {`,
    `  source = "${path.relative(secondEnv, secondModule)}"`,
    `  items = var.${SECOND_RESOURCE}_items`,
    "}",
    "",
  ].join("\n"));
  await writeText(secondConfig, `{"${SECOND_RESOURCE}_items":{}}\n`);
  await writeText(path.join(secondEnv, "tfplan"), "second opaque plan\n");
  await writeText(path.join(secondEnv, "tfplan.sources"), `${JSON.stringify(
    await planFingerprintV2({
      envDir: secondEnv,
      varFiles: [secondConfig],
      memberTypes: [SECOND_RESOURCE],
      backendConfig: null,
      backendKey: null,
    }),
  )}\n`);
  const terraform = new FakeTerraform();
  const completed = await run(item, terraform);
  assert.deepEqual(completed.result, { applied: 2 });
  assert.deepEqual(terraform.applied.map((entry) => path.basename(entry.directory)), [
    SECOND_RESOURCE,
    RESOURCE,
  ]);
  await assert.rejects(readFile(path.join(secondEnv, "tfplan")), /ENOENT/u);
  await assert.rejects(readFile(item.tfplan), /ENOENT/u);
});

test("multi-root Apply stops at failure and preserves failed and later pairs", async (context) => {
  const item = await fixture(context);
  const addRoot = async (resourceType: string): Promise<{
    readonly envDir: string;
    readonly fingerprint: string;
    readonly savedPlan: string;
  }> => {
    const envDir = path.join(item.workspace, "envs", "tenant", resourceType);
    const moduleDirectory = path.join(item.workspace, "modules", resourceType);
    const config = path.join(
      item.workspace,
      "config",
      "tenant",
      `${resourceType}.auto.tfvars.json`,
    );
    await writeText(path.join(moduleDirectory, "main.tf"), "# module\n");
    await writeText(path.join(envDir, "main.tf"), [
      `module "${resourceType}" {`,
      `  source = "${path.relative(envDir, moduleDirectory)}"`,
      `  items = var.${resourceType}_items`,
      "}",
      "",
    ].join("\n"));
    await writeText(config, `{"${resourceType}_items":{}}\n`);
    const savedPlan = path.join(envDir, "tfplan");
    const fingerprint = path.join(envDir, "tfplan.sources");
    await writeText(savedPlan, `${resourceType} opaque plan\n`);
    await writeText(fingerprint, `${JSON.stringify(await planFingerprintV2({
      envDir,
      varFiles: [config],
      memberTypes: [resourceType],
      backendConfig: null,
      backendKey: null,
    }))}\n`);
    return { envDir, fingerprint, savedPlan };
  };
  const first = await addRoot(SECOND_RESOURCE);
  const later = await addRoot("zia_workload_groups");
  const terraform = new FakeTerraform();
  terraform.onApply = () => {
    if (terraform.applied.length === 2) throw new Error("second root failed");
  };

  await assert.rejects(run(item, terraform), /second root failed/u);

  assert.deepEqual(terraform.applied.map((entry) => path.basename(entry.directory)), [
    SECOND_RESOURCE,
    RESOURCE,
  ]);
  await assert.rejects(readFile(first.savedPlan), /ENOENT/u);
  await assert.rejects(readFile(first.fingerprint), /ENOENT/u);
  assert.equal(await readFile(item.tfplan, "utf8"), "opaque saved plan\n");
  assert.match(await readFile(item.fingerprintPath, "utf8"), /"version":2/u);
  assert.equal(
    await readFile(later.savedPlan, "utf8"),
    "zia_workload_groups opaque plan\n",
  );
  assert.match(await readFile(later.fingerprint, "utf8"), /"version":2/u);
});

test("real adapter streams init/Apply and invokes the exact saved plan", async (context) => {
  const workspace = await temporaryDirectory(context);
  const executable = path.join(workspace, "terraform-fake");
  const savedPlan = path.join(workspace, "tfplan");
  const log = path.join(workspace, "terraform.log");
  const planJson = path.join(workspace, "plan.json");
  await writeText(savedPlan, "opaque\n");
  await writeText(planJson, JSON.stringify(plan()));
  await writeText(executable, [
    "#!/bin/sh",
    `printf '%s\\n' "$*" >> '${log}'`,
    "if [ \"$2\" = show ]; then",
    `  cat '${planJson}'`,
    "fi",
    "if [ \"$1\" = init ]; then",
    "  printf '%s' hidden-init-stdout",
    "  printf '%s' visible-init-stderr >&2",
    "fi",
    "if [ \"$1\" = apply ]; then",
    "  printf '%s' visible-apply-stdout",
    "  printf '%s' visible-apply-stderr >&2",
    "fi",
    "",
  ].join("\n"));
  await chmod(executable, 0o700);
  const adapter = createExactPlanApplyTerraform({ environment: {}, terraformExecutable: executable });
  let stdout = "";
  let stderr = "";
  const originalStdout = process.stdout.write;
  const originalStderr = process.stderr.write;
  process.stdout.write = ((chunk: string | Uint8Array) => {
    stdout += Buffer.from(chunk).toString("utf8");
    return true;
  }) as typeof process.stdout.write;
  process.stderr.write = ((chunk: string | Uint8Array) => {
    stderr += Buffer.from(chunk).toString("utf8");
    return true;
  }) as typeof process.stderr.write;
  try {
    await adapter.initialize({ directory: workspace, save: false, varFiles: [] });
    assert.deepEqual(await adapter.show({ directory: workspace, snapshotPath: savedPlan }), plan());
    await adapter.apply({ directory: workspace });
  } finally {
    process.stdout.write = originalStdout;
    process.stderr.write = originalStderr;
  }
  assert.equal(stdout, "visible-apply-stdout");
  assert.equal(stderr, "visible-init-stderrvisible-apply-stderr");
  const lines = (await readFile(log, "utf8")).trim().split("\n");
  assert.equal(lines[0], "init -input=false");
  assert.match(lines[1] ?? "", /^-chdir=.* show -json .*tfplan$/u);
  assert.equal(lines[2], "apply -input=false tfplan");
});

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

test("Make Apply is Python-disabled and Node/Python blocked diagnostics agree", async (context) => {
  const item = await fixture(context);
  const deploymentPath = path.join(item.workspace, "deployment.json");
  const executable = path.join(item.workspace, "terraform-fake");
  const planJson = path.join(item.workspace, "plan.json");
  const log = path.join(item.workspace, "terraform.log");
  await writeText(deploymentPath, `${JSON.stringify(item.deployment)}\n`);
  const blockedPlan = plan([change(["update"], {
    before: { status: "old" },
    after: { status: "new" },
  })]);
  await writeText(planJson, JSON.stringify(blockedPlan));
  await writeText(executable, [
    "#!/bin/sh",
    `printf '%s\\n' "$*" >> '${log}'`,
    "if [ \"$2\" = show ]; then",
    `  cat '${planJson}'`,
    "fi",
    "",
  ].join("\n"));
  await chmod(executable, 0o700);
  const environment = {
    ...process.env,
    BUILD_SOURCEBRANCH: "refs/heads/main",
    INFRAWRIGHT_DEPLOYMENT: deploymentPath,
    PYTHON: "/definitely/missing/python",
    TF: executable,
  };
  const built = command(process.execPath, ["scripts/build-metadata-cli.mjs"], environment);
  assert.equal(built.status, 0, built.stderr);
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const invalidTenant = command(process.execPath, [
    cli,
    "apply",
    "--tenant", "",
    "--deployment", deploymentPath,
  ], {
    ...environment,
    BUILD_SOURCEBRANCH: "refs/pull/207/merge",
  });
  assert.equal(invalidTenant.status, 2);
  assert.match(invalidTenant.stderr, /TENANT must match/u);
  assert.doesNotMatch(invalidTenant.stderr, /only merged main config gets applied/u);

  const node = command(process.execPath, [
    cli,
    "apply",
    "--tenant", "tenant",
    "--resource", RESOURCE,
    "--terraform", executable,
    "--profile", path.join(ROOT, "packsets", "full.json"),
    "--catalog", path.join(ROOT, "packsets", "full.json"),
    "--deployment", deploymentPath,
  ], environment);
  const python = command(PYTHON_ORACLE, [
    "-m", "engine.ops", "apply",
    "--tenant", "tenant",
    RESOURCE,
  ], environment);
  assert.equal(node.status, python.status);
  assert.equal(node.stdout, python.stdout);
  assert.equal(node.stderr, python.stderr);
  assert.equal(await readFile(item.tfplan, "utf8"), "opaque saved plan\n");

  await writeText(planJson, JSON.stringify(plan([change(["create"], {
    importing: { id: "123" },
  })])));
  const make = command("make", [
    "--no-print-directory",
    `DEPLOYMENT=${deploymentPath}`,
    `PACK_PROFILE=${path.join(ROOT, "packsets", "full.json")}`,
    `PACK_CATALOG=${path.join(ROOT, "packsets", "full.json")}`,
    `TF=${executable}`,
    "TENANT=tenant",
    `RESOURCE=${RESOURCE}`,
    "apply",
  ], environment);
  assert.equal(make.status, 0, make.stderr);
  assert.match(make.stderr, new RegExp(`== apply tenant/${RESOURCE}`, "u"));
  await assert.rejects(readFile(item.tfplan), /ENOENT/u);
  await assert.rejects(readFile(item.fingerprintPath), /ENOENT/u);
});
