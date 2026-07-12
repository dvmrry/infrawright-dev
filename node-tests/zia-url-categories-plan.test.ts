import assert from "node:assert/strict";
import { chmodSync } from "node:fs";
import {
  access,
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  realpath,
  rename,
  rm,
  stat,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { renderHclQuotedString } from "../node-src/domain/import-moves.js";
import {
  deriveZiaUrlCategoryIdentities,
  ZIA_PROVIDER_SOURCE,
  ZIA_URL_CATEGORIES_RESOURCE_TYPE,
  type ZiaUrlCategoryStateObservation,
} from "../node-src/domain/zia-url-categories.js";
import { runZiaUrlCategoryPlanWorkflow } from "../node-src/io/zia-url-categories-plan.js";

const RAW = Object.freeze([{
  configuredName: "Raw Category Name",
  customCategory: true,
  id: "CUSTOM_01",
  urls: ["one.example"],
}]);

function observations(): readonly ZiaUrlCategoryStateObservation[] {
  return deriveZiaUrlCategoryIdentities(RAW).map((identity) => Object.freeze({
    address: identity.address,
    importId: identity.importId,
    key: identity.key,
    providerName: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
    resourceType: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    sensitiveValues: {},
    values: {
      category_id: identity.importId,
      configured_name: "Provider Normalized Name",
      custom_category: true,
      urls: ["one.example"],
    },
  }));
}

function cleanPlan(importing: boolean): Record<string, unknown> {
  const identity = deriveZiaUrlCategoryIdentities(RAW)[0];
  assert.notEqual(identity, undefined);
  return {
    complete: true,
    errored: false,
    format_version: "1.2",
    output_changes: {},
    resource_changes: importing
      ? [{
          address: `module.${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.${
            ZIA_URL_CATEGORIES_RESOURCE_TYPE
          }.this[${renderHclQuotedString(identity!.key)}]`,
          change: {
            actions: ["no-op"],
            after: { category_id: identity!.importId },
            before: { category_id: identity!.importId },
            importing: { id: identity!.importId },
          },
          mode: "managed",
          provider_name: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
          type: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
        }]
      : [],
    resource_drift: [],
    terraform_version: "1.15.4",
  };
}

async function fakeTerraform(options: {
  readonly failPlan?: boolean;
  readonly failState?: boolean;
  readonly root: string;
  readonly managed: readonly string[];
  readonly plan: Record<string, unknown>;
}): Promise<{ readonly executable: string; readonly log: string }> {
  const executable = path.join(options.root, "terraform-fake.mjs");
  const log = path.join(options.root, "terraform-calls.jsonl");
  const script = [
    `#!${process.execPath}`,
    'import fs from "node:fs";',
    `const log = ${JSON.stringify(log)};`,
    `const managed = ${JSON.stringify(options.managed)};`,
    `const plan = ${JSON.stringify(options.plan)};`,
    `const failPlan = ${options.failPlan === true ? "true" : "false"};`,
    `const failState = ${options.failState === true ? "true" : "false"};`,
    "const args = process.argv.slice(2);",
    "fs.appendFileSync(log, JSON.stringify(args) + '\\n');",
    "const offset = args[0]?.startsWith('-chdir=') ? 1 : 0;",
    "const command = args[offset];",
    "if (command === 'init') {",
    "  fs.writeFileSync('.terraform.lock.hcl', '# fake provider lock\\n');",
    "  if (managed.length > 0 || failState) fs.writeFileSync('terraform.tfstate', '{}\\n');",
    "} else if (command === 'state' && args[offset + 1] === 'list') {",
    "  if (failState) process.exit(24);",
    "  process.stdout.write(managed.length === 0 ? '' : managed.join('\\n') + '\\n');",
    "} else if (command === 'plan') {",
    "  const out = args.find((value) => value.startsWith('-out='));",
    "  if (out === undefined) process.exit(21);",
    "  fs.writeFileSync(out.slice(5), 'opaque saved plan bytes\\n');",
    "  if (failPlan) process.exit(23);",
    "} else if (command === 'show' && args[offset + 1] === '-json') {",
    "  process.stdout.write(JSON.stringify(plan));",
    "} else {",
    "  process.exit(22);",
    "}",
    "",
  ].join("\n");
  await writeFile(executable, script, { encoding: "utf8", mode: 0o700 });
  chmodSync(executable, 0o700);
  return { executable, log };
}

function workflowOptions(workspace: string, terraformExecutable: string) {
  return {
    environment: { PATH: "", PYTHON: "/unavailable/python" },
    tenant: "production-test",
    terraformExecutable,
    workspace,
  };
}

const dependencies = Object.freeze({
  collect: async () => RAW,
  observe: async () => observations(),
});

function failureCode(code: string): (error: unknown) => boolean {
  return (error) => error instanceof ProcessFailure && error.code === code;
}

test("writes a state-aware import plan, v2 fingerprint, and clean Node assessment", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-plan-"));
  try {
    const preexistingEnv = path.join(
      workspace,
      "envs",
      "production-test",
      ZIA_URL_CATEGORIES_RESOURCE_TYPE,
    );
    await mkdir(preexistingEnv, { mode: 0o755, recursive: true });
    await chmod(preexistingEnv, 0o755);
    const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
    const result = await runZiaUrlCategoryPlanWorkflow(
      workflowOptions(workspace, fake.executable),
      dependencies,
    );
    assert.deepEqual(result.staged, { alreadyManaged: 0, imports: 1 });
    assert.equal(result.assessment.status, "clean");
    assert.equal(result.assessment.checked, 1);
    assert.match(await readFile(result.paths.stagedImports, "utf8"), /CUSTOM_01/);
    assert.equal(await readFile(result.paths.plan, "utf8"), "opaque saved plan bytes\n");
    assert.equal((await stat(result.paths.envDir)).mode & 0o777, 0o700);
    assert.equal((await stat(result.paths.plan)).mode & 0o777, 0o600);
    await assert.rejects(access(result.paths.pendingPlan), /ENOENT/);
    assert.deepEqual(JSON.parse(await readFile(result.paths.fingerprint, "utf8")), {
      sha256: result.assessment.roots[0]?.plan_fingerprint.sha256,
      version: 2,
    });
    const persisted = JSON.parse(await readFile(result.paths.assessment, "utf8")) as {
      status: string;
    };
    assert.equal(persisted.status, "clean");
    for (const generated of [
      result.module.moduleMain,
      result.module.moduleVariables,
      result.module.moduleOutputs,
      result.module.moduleVersions,
      result.module.envMain,
    ]) {
      await access(generated);
    }
    const calls = (await readFile(fake.log, "utf8"))
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line) as string[]);
    assert.deepEqual(calls.map((args) => {
      const offset = args[0]?.startsWith("-chdir=") ? 1 : 0;
      return args[offset];
    }), ["init", "plan", "show"]);
    assert.equal(calls.flat().some((value) => value.includes("python")), false);
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("omits import blocks for addresses already managed in state", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-managed-"));
  try {
    const identity = deriveZiaUrlCategoryIdentities(RAW)[0];
    assert.notEqual(identity, undefined);
    const managed = `module.${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.${
      ZIA_URL_CATEGORIES_RESOURCE_TYPE
    }.this[${renderHclQuotedString(identity!.key)}]`;
    const fake = await fakeTerraform({ root: workspace, managed: [managed], plan: cleanPlan(false) });
    const result = await runZiaUrlCategoryPlanWorkflow(
      workflowOptions(workspace, fake.executable),
      dependencies,
    );
    assert.deepEqual(result.staged, { alreadyManaged: 1, imports: 0 });
    await assert.rejects(access(result.paths.stagedImports), /ENOENT/);
    assert.equal(result.assessment.status, "clean");
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("state-aware staging ignores unknown addresses and rejects malformed or failed listing", async (t) => {
  await t.test("partial delta", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-state-partial-"));
    try {
      const raw = [
        ...RAW,
        {
          configuredName: "Second Category",
          customCategory: true,
          id: "CUSTOM_02",
          urls: ["two.example"],
        },
      ];
      const identities = deriveZiaUrlCategoryIdentities(raw);
      const addresses = identities.map((identity) => {
        return `module.${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.${
          ZIA_URL_CATEGORIES_RESOURCE_TYPE
        }.this[${renderHclQuotedString(identity.key)}]`;
      });
      const plan = {
        complete: true,
        errored: false,
        format_version: "1.2",
        output_changes: {},
        resource_changes: identities.map((identity, index) => ({
          address: addresses[index],
          change: {
            actions: ["no-op"],
            after: { category_id: identity.importId },
            before: { category_id: identity.importId },
            ...(index === 1 ? { importing: { id: identity.importId } } : {}),
          },
          mode: "managed",
          provider_name: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
          type: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
        })),
        resource_drift: [],
        terraform_version: "1.15.4",
      };
      const fake = await fakeTerraform({ root: workspace, managed: [addresses[0]!], plan });
      const result = await runZiaUrlCategoryPlanWorkflow(
        workflowOptions(workspace, fake.executable),
        {
          collect: async () => raw,
          observe: async () => identities.map((identity) => ({
            address: identity.address,
            importId: identity.importId,
            key: identity.key,
            providerName: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
            resourceType: ZIA_URL_CATEGORIES_RESOURCE_TYPE,
            sensitiveValues: {},
            values: {
              category_id: identity.importId,
              configured_name: `Provider ${identity.importId}`,
              custom_category: true,
              urls: ["example.invalid"],
            },
          })),
        },
      );
      assert.deepEqual(result.staged, { alreadyManaged: 1, imports: 1 });
      const staged = await readFile(result.paths.stagedImports, "utf8");
      assert.equal(staged.includes("CUSTOM_01"), false);
      assert.equal(staged.includes("CUSTOM_02"), true);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("unknown address", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-state-unknown-"));
    try {
      const fake = await fakeTerraform({
        root: workspace,
        managed: ['module.foreign.foreign.this["other"]'],
        plan: cleanPlan(true),
      });
      const result = await runZiaUrlCategoryPlanWorkflow(
        workflowOptions(workspace, fake.executable),
        dependencies,
      );
      assert.deepEqual(result.staged, { alreadyManaged: 0, imports: 1 });
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("malformed address", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-state-malformed-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [" leading-space"], plan: cleanPlan(true) });
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          dependencies,
        ),
        failureCode("INVALID_ZIA_URL_CATEGORY_STATE_LIST"),
      );
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("state list failure", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-state-fail-"));
    try {
      const fake = await fakeTerraform({
        failState: true,
        root: workspace,
        managed: [],
        plan: cleanPlan(true),
      });
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          dependencies,
        ),
        failureCode("TERRAFORM_COMMAND_FAILED"),
      );
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });
});

test("rejects init and plan input mutation and removes stale plan outputs", async (t) => {
  await t.test("init root mutation", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-init-mutation-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      let observedPaths: { readonly plan: string; readonly fingerprint: string } | null = null;
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          {
            ...dependencies,
            afterInit: async (paths) => {
              observedPaths = paths;
              await writeFile(path.join(paths.envDir, "main.tf"), "# changed during init\n");
            },
          },
        ),
        failureCode("ZIA_URL_CATEGORY_INIT_INPUTS_CHANGED"),
      );
      assert.notEqual(observedPaths, null);
      await assert.rejects(access(observedPaths!.plan), /ENOENT/);
      await assert.rejects(access(observedPaths!.fingerprint), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("plan var-file mutation", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-plan-mutation-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      let observedPaths: { readonly plan: string; readonly fingerprint: string } | null = null;
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          {
            ...dependencies,
            afterPlan: async (paths) => {
              observedPaths = paths;
              await writeFile(
                path.join(workspace, "config", "production-test", "zia_url_categories.auto.tfvars.json"),
                "{}\n",
              );
            },
          },
        ),
        failureCode("ZIA_URL_CATEGORY_PLAN_INPUTS_CHANGED"),
      );
      assert.notEqual(observedPaths, null);
      await assert.rejects(access(observedPaths!.plan), /ENOENT/);
      await assert.rejects(access(observedPaths!.fingerprint), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });
});

test("the existing assessor rejects a stale workflow fingerprint", async () => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-stale-"));
  try {
    const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
    let observed: { readonly assessment: string; readonly fingerprint: string; readonly plan: string } | null = null;
    await assert.rejects(
      runZiaUrlCategoryPlanWorkflow(
        workflowOptions(workspace, fake.executable),
        {
          ...dependencies,
          beforeAssessment: async (paths) => {
            observed = paths;
            await writeFile(
              paths.fingerprint,
              `${JSON.stringify({ version: 2, sha256: "0".repeat(64) })}\n`,
            );
          },
        },
      ),
      failureCode("STALE_PLAN_SOURCES"),
    );
    assert.notEqual(observed, null);
    await assert.rejects(access(observed!.plan), /ENOENT/);
    await assert.rejects(access(observed!.fingerprint), /ENOENT/);
    await assert.rejects(access(observed!.assessment), /ENOENT/);
  } finally {
    await rm(workspace, { force: true, recursive: true });
  }
});

test("rejects foreign root content before Terraform and confines output paths", async (t) => {
  for (const candidate of [
    ["root HCL", path.join("envs", "production-test", ZIA_URL_CATEGORIES_RESOURCE_TYPE, "foreign.tf")],
    ["root auto var", path.join("envs", "production-test", ZIA_URL_CATEGORIES_RESOURCE_TYPE, "foreign.auto.tfvars.json")],
    ["module sidecar", path.join("modules", "zia-v4.7.26", ZIA_URL_CATEGORIES_RESOURCE_TYPE, "foreign.tf")],
  ] as const) {
    await t.test(candidate[0], async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-foreign-file-"));
      try {
        const foreign = path.join(workspace, candidate[1]);
        await mkdir(path.dirname(foreign), { recursive: true });
        await writeFile(foreign, "# foreign\n");
        const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
        await assert.rejects(
          runZiaUrlCategoryPlanWorkflow(
            workflowOptions(workspace, fake.executable),
            dependencies,
          ),
          failureCode("UNSAFE_ZIA_URL_CATEGORY_WORKSPACE"),
        );
        await assert.rejects(access(path.join(
          workspace,
          "envs",
          "production-test",
          ZIA_URL_CATEGORIES_RESOURCE_TYPE,
          "tfplan",
        )), /ENOENT/);
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("directory authority replacement is rejected without touching the external target", async (t) => {
  for (const targetName of ["environment", "module", "config", "private plan"] as const) {
    await t.test(targetName, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-authority-swap-"));
      const outside = await mkdtemp(path.join(os.tmpdir(), "zia-url-authority-external-"));
      try {
        await writeFile(path.join(outside, "sentinel"), "external-stays-exact\n");
        const root = await realpath(workspace);
        const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
        await assert.rejects(
          runZiaUrlCategoryPlanWorkflow(
            workflowOptions(workspace, fake.executable),
            {
              ...dependencies,
              afterPlan: async (paths) => {
                const target = targetName === "environment"
                  ? paths.envDir
                  : targetName === "module"
                  ? path.join(root, "modules", "zia-v4.7.26", ZIA_URL_CATEGORIES_RESOURCE_TYPE)
                  : targetName === "config"
                  ? path.join(root, "config", "production-test")
                  : path.dirname(paths.pendingPlan);
                await rename(target, `${target}.moved`);
                await symlink(outside, target);
              },
            },
          ),
          failureCode("UNSAFE_ZIA_URL_CATEGORY_WORKSPACE"),
        );
        assert.deepEqual(await readdir(outside), ["sentinel"]);
        assert.equal(await readFile(path.join(outside, "sentinel"), "utf8"), "external-stays-exact\n");
      } finally {
        await rm(workspace, { force: true, recursive: true });
        await rm(outside, { force: true, recursive: true });
      }
    });
  }
});

test("assessment evidence rejects foreign or mismatched no-op imports", async (t) => {
  const variants: readonly [string, (plan: Record<string, unknown>) => void][] = [
    ["foreign resource", (plan) => {
      (plan.resource_changes as unknown[]).push({
        address: 'module.zia_url_filtering_rules.zia_url_filtering_rules.this["foreign"]',
        change: {
          actions: ["no-op"],
          after: { id: "foreign" },
          before: { id: "foreign" },
          importing: { id: "foreign" },
        },
        mode: "managed",
        provider_name: `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`,
        type: "zia_url_filtering_rules",
      });
    }],
    ["wrong provider", (plan) => {
      (plan.resource_changes as Record<string, unknown>[])[0]!.provider_name = "example/foreign";
    }],
    ["wrong import ID", (plan) => {
      const change = (plan.resource_changes as Record<string, unknown>[])[0]!.change as Record<string, unknown>;
      change.importing = { id: "CUSTOM_OTHER" };
    }],
    ["previous address", (plan) => {
      (plan.resource_changes as Record<string, unknown>[])[0]!.previous_address = "zia_url_categories.old";
    }],
  ];
  for (const [name, mutate] of variants) {
    await t.test(name, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-plan-scope-"));
      try {
        const plan = structuredClone(cleanPlan(true));
        mutate(plan);
        const fake = await fakeTerraform({ root: workspace, managed: [], plan });
        await assert.rejects(
          runZiaUrlCategoryPlanWorkflow(
            workflowOptions(workspace, fake.executable),
            dependencies,
          ),
          failureCode("ZIA_URL_CATEGORY_PLAN_SCOPE_REJECTED"),
        );
        const envDir = path.join(
          workspace,
          "envs",
          "production-test",
          ZIA_URL_CATEGORIES_RESOURCE_TYPE,
        );
        await assert.rejects(access(path.join(envDir, "tfplan")), /ENOENT/);
        await assert.rejects(access(path.join(envDir, "tfplan.sources")), /ENOENT/);
        await assert.rejects(access(path.join(envDir, "tfplan.assessment.json")), /ENOENT/);
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("hook rejection and an early failed rerun invalidate every saved-plan output", async (t) => {
  await t.test("failed Terraform plan", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-plan-fail-"));
    try {
      const fake = await fakeTerraform({
        failPlan: true,
        root: workspace,
        managed: [],
        plan: cleanPlan(true),
      });
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          dependencies,
        ),
        failureCode("TERRAFORM_COMMAND_FAILED"),
      );
      const envDir = path.join(
        workspace,
        "envs",
        "production-test",
        ZIA_URL_CATEGORIES_RESOURCE_TYPE,
      );
      await assert.rejects(access(path.join(envDir, "tfplan")), /ENOENT/);
      await assert.rejects(access(path.join(envDir, "tfplan.sources")), /ENOENT/);
      await assert.rejects(access(path.join(envDir, ".infrawright", "plan", "tfplan.pending")), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("hook rejection", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-hook-reject-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      let observed: { readonly assessment: string; readonly fingerprint: string; readonly plan: string } | null = null;
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          {
            ...dependencies,
            beforeAssessment: (paths) => {
              observed = paths;
              throw new ProcessFailure({
                code: "TEST_ASSESSMENT_HOOK_REJECTED",
                category: "domain",
                message: "test hook rejected",
              });
            },
          },
        ),
        failureCode("TEST_ASSESSMENT_HOOK_REJECTED"),
      );
      assert.notEqual(observed, null);
      await assert.rejects(access(observed!.plan), /ENOENT/);
      await assert.rejects(access(observed!.fingerprint), /ENOENT/);
      await assert.rejects(access(observed!.assessment), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("assessment persistence failure", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-assessment-write-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      let observed: { readonly assessment: string; readonly fingerprint: string; readonly plan: string } | null = null;
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          {
            ...dependencies,
            beforeAssessment: async (paths) => {
              observed = paths;
              await mkdir(paths.assessment);
            },
          },
        ),
        failureCode("ZIA_URL_CATEGORY_ASSESSMENT_WRITE_FAILED"),
      );
      assert.notEqual(observed, null);
      await assert.rejects(access(observed!.plan), /ENOENT/);
      await assert.rejects(access(observed!.fingerprint), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("early rerun failure", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-rerun-reject-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      const first = await runZiaUrlCategoryPlanWorkflow(
        workflowOptions(workspace, fake.executable),
        dependencies,
      );
      await access(first.paths.plan);
      await access(first.paths.fingerprint);
      await access(first.paths.assessment);
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          {
            ...dependencies,
            collect: async () => {
              throw new ProcessFailure({
                code: "TEST_COLLECTION_FAILED",
                category: "io",
                message: "test collection failed",
              });
            },
          },
        ),
        failureCode("TEST_COLLECTION_FAILED"),
      );
      await assert.rejects(access(first.paths.plan), /ENOENT/);
      await assert.rejects(access(first.paths.fingerprint), /ENOENT/);
      await assert.rejects(access(first.paths.assessment), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });

  await t.test("foreign-content rerun failure", async () => {
    const workspace = await mkdtemp(path.join(os.tmpdir(), "zia-url-rerun-foreign-"));
    try {
      const fake = await fakeTerraform({ root: workspace, managed: [], plan: cleanPlan(true) });
      const first = await runZiaUrlCategoryPlanWorkflow(
        workflowOptions(workspace, fake.executable),
        dependencies,
      );
      await writeFile(path.join(first.paths.envDir, "foreign.tf"), "# foreign\n");
      await assert.rejects(
        runZiaUrlCategoryPlanWorkflow(
          workflowOptions(workspace, fake.executable),
          dependencies,
        ),
        failureCode("UNSAFE_ZIA_URL_CATEGORY_WORKSPACE"),
      );
      await assert.rejects(access(first.paths.plan), /ENOENT/);
      await assert.rejects(access(first.paths.fingerprint), /ENOENT/);
      await assert.rejects(access(first.paths.assessment), /ENOENT/);
      await assert.rejects(access(first.paths.stagedImports), /ENOENT/);
    } finally {
      await rm(workspace, { force: true, recursive: true });
    }
  });
});
