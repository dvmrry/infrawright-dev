import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  REFERENCE_BACKEND_ENVIRONMENT,
  referenceBackendEnvironment,
} from "../node-src/domain/reference-backend.js";
import {
  cleanPlans,
  createPlanTerraform,
  planEnvironmentRoots,
  type PlanTerraform,
  type PlanTerraformRequest,
} from "../node-src/domain/plan-lifecycle.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import type { Deployment } from "../node-src/domain/types.js";

const ROOT = process.cwd();
const ZIA_RESOURCE = "zia_url_categories";
const ZIA_SECOND = "zia_admin_users";
const DERIVED = "zpa_policy_access_rule_reorder";
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
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-plan-lifecycle-"));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeText(file: string, text: string): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, text, "utf8");
}

function deployment(roots: Deployment["roots"] = {}): Deployment {
  return { overlay: ".", roots };
}

function envDirectory(workspace: string, label: string): string {
  return path.join(workspace, "envs", "tenant", label);
}

function configPath(workspace: string, resourceType: string): string {
  return path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`);
}

function hclConfigPath(workspace: string, resourceType: string): string {
  return path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars`);
}

async function writeRoot(options: {
  readonly backend?: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly workspace: string;
}): Promise<string> {
  const directory = envDirectory(options.workspace, options.label);
  const lines = [
    ...(options.backend === undefined
      ? []
      : ["terraform {", `  backend "${options.backend}" {}`, "}", ""]),
  ];
  for (const resourceType of options.members) {
    const moduleDirectory = path.join(options.workspace, "modules", resourceType);
    await writeText(path.join(moduleDirectory, "main.tf"), "# module\n");
    lines.push(
      `module "${resourceType}" {`,
      `  source = "${path.relative(directory, moduleDirectory)}"`,
      `  items = var.${resourceType}_items`,
      "}",
      "",
    );
  }
  await writeText(path.join(directory, "main.tf"), `${lines.join("\n")}\n`);
  return directory;
}

class FakeTerraform implements PlanTerraform {
  readonly initialized: PlanTerraformRequest[] = [];
  readonly planned: PlanTerraformRequest[] = [];
  onInitialize?: (request: PlanTerraformRequest) => Promise<void> | void;
  onPlan?: (request: PlanTerraformRequest) => Promise<void> | void;

  async initialize(request: PlanTerraformRequest): Promise<void> {
    this.initialized.push(request);
    await this.onInitialize?.(request);
  }

  async plan(request: PlanTerraformRequest): Promise<void> {
    this.planned.push(request);
    if (request.save) await writeFile(path.join(request.directory, "tfplan"), "opaque-plan");
    await this.onPlan?.(request);
  }
}

function assertFailure(error: unknown, code: string): ProcessFailure {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return error;
}

test("saved local plan leaves an exact private pair and clean-plans removes only that pair", async (context) => {
  const workspace = await temporaryDirectory(context);
  const directory = await writeRoot({ label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
  const config = configPath(workspace, ZIA_RESOURCE);
  await writeText(config, `{"${ZIA_RESOURCE}_items":{}}\n`);
  await writeText(path.join(directory, "tfplan"), "stale-plan");
  await writeText(path.join(directory, "tfplan.sources"), "stale-sources\n");
  await writeText(path.join(directory, "report.json"), "{}\n");
  await writeText(path.join(directory, ".terraform.lock.hcl"), "# lock\n");
  const terraform = new FakeTerraform();
  const diagnostics: string[] = [];

  const result = await planEnvironmentRoots({
    deployment: deployment(),
    importsOnly: false,
    onDiagnostic: (message) => diagnostics.push(message),
    root: await committedRoot(),
    save: true,
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    terraform,
    workspace,
  });

  assert.deepEqual(result, { planned: 1 });
  assert.equal(terraform.initialized.length, 1);
  assert.equal(terraform.planned.length, 1);
  assert.deepEqual(terraform.planned[0]?.varFiles, [config]);
  assert.equal(terraform.planned[0]?.backendConfig, undefined);
  assert.deepEqual(diagnostics, [`== plan ${ZIA_RESOURCE}`]);
  const sources = await readFile(path.join(directory, "tfplan.sources"), "utf8");
  assert.match(sources, /^\{"sha256": "[0-9a-f]{64}", "version": 2\}\n$/u);
  if (process.platform !== "win32") {
    assert.equal((await stat(path.join(directory, "tfplan"))).mode & 0o777, 0o600);
  }

  const cleanDiagnostics: string[] = [];
  assert.deepEqual(await cleanPlans({
    deployment: deployment(),
    onDiagnostic: (message) => cleanDiagnostics.push(message),
    root: await committedRoot(),
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    workspace,
  }), { removed: 1 });
  assert.deepEqual(cleanDiagnostics, [
    `removed envs/tenant/${ZIA_RESOURCE}/tfplan`,
    `removed envs/tenant/${ZIA_RESOURCE}/tfplan.sources`,
    "1 stale plan(s) removed",
  ]);
  assert.equal(await readFile(path.join(directory, "report.json"), "utf8"), "{}\n");
  assert.equal(await readFile(path.join(directory, ".terraform.lock.hcl"), "utf8"), "# lock\n");
  assert.deepEqual(await cleanPlans({
    deployment: deployment(),
    root: await committedRoot(),
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    workspace,
  }), { removed: 0 });
});

test("generic Terraform adapter emits exact backend, var-file, and saved-plan argv", async (context) => {
  const workspace = await temporaryDirectory(context);
  const executable = path.join(workspace, "terraform-fake");
  const log = path.join(workspace, "terraform.log");
  await writeText(executable, [
    "#!/bin/sh",
    "test \"$TF_VAR_CROSS_STATE\" = 'request-value' || exit 91",
    "printf '%s\\n' \"$*\" >> \"$TF_LOG_FILE\"",
    "exit 0",
    "",
  ].join("\n"));
  await chmod(executable, 0o700);
  const adapter = createPlanTerraform({
    environment: { TF_LOG_FILE: log },
    terraformExecutable: executable,
  });
  const request: PlanTerraformRequest = {
    backendConfig: path.join(workspace, "backend.hcl"),
    backendKey: "tenant/grouped.tfstate",
    directory: workspace,
    environment: { TF_VAR_CROSS_STATE: "request-value" },
    save: true,
    varFiles: [path.join(workspace, "a.tfvars"), path.join(workspace, "b.tfvars")],
  };
  await adapter.initialize(request);
  await adapter.plan(request);
  assert.equal(await readFile(log, "utf8"), [
    `init -input=false -reconfigure -backend-config=${request.backendConfig} -backend-config=key=${request.backendKey}`,
    `plan -input=false -var-file=${request.varFiles[0]} -var-file=${request.varFiles[1]} -out=tfplan`,
    "",
  ].join("\n"));
});

test("generic Terraform adapter suppresses init stdout and preserves init stderr", async (context) => {
  const workspace = await temporaryDirectory(context);
  for (const exitCode of [0, 37]) {
    const executable = path.join(workspace, `terraform-init-${exitCode}`);
    await writeText(executable, [
      "#!/bin/sh",
      "printf '%s' 'hidden-init-stdout'",
      "printf '%s' 'visible-init-stderr' >&2",
      `exit ${exitCode}`,
      "",
    ].join("\n"));
    await chmod(executable, 0o700);
    const adapter = createPlanTerraform({ environment: {}, terraformExecutable: executable });
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
      const request: PlanTerraformRequest = {
        directory: workspace,
        save: false,
        varFiles: [],
      };
      if (exitCode === 0) await adapter.initialize(request);
      else await assert.rejects(adapter.initialize(request), (error) => {
        assertFailure(error, "TERRAFORM_COMMAND_FAILED");
        return true;
      });
    } finally {
      process.stdout.write = originalStdout;
      process.stderr.write = originalStderr;
    }
    assert.equal(stdout, "");
    assert.equal(stderr, "visible-init-stderr");
  }
});

test("remote backend uses the exact absolute config and tenant/root state key", async (context) => {
  const workspace = await temporaryDirectory(context);
  await writeRoot({ backend: "azurerm", label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
  await writeText(configPath(workspace, ZIA_RESOURCE), `{"${ZIA_RESOURCE}_items":{}}\n`);
  const backend = path.join(workspace, "backend.hcl");
  await writeText(backend, "storage_account_name = \"example\"\n");

  await assert.rejects(
    planEnvironmentRoots({
      deployment: deployment(),
      importsOnly: false,
      root: await committedRoot(),
      save: false,
      selectors: [ZIA_RESOURCE],
      tenant: "tenant",
      terraform: new FakeTerraform(),
      workspace,
    }),
    (error) => {
      assertFailure(error, "BACKEND_CONFIG_REQUIRED");
      return true;
    },
  );

  const terraform = new FakeTerraform();
  await planEnvironmentRoots({
    backendConfig: "backend.hcl",
    deployment: deployment(),
    importsOnly: false,
    root: await committedRoot(),
    save: false,
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    terraform,
    workspace,
  });
  assert.equal(terraform.initialized[0]?.backendConfig, backend);
  assert.equal(terraform.initialized[0]?.backendKey, `tenant/${ZIA_RESOURCE}.tfstate`);
  await assert.rejects(readFile(path.join(envDirectory(workspace, ZIA_RESOURCE), "tfplan")));
});

test("cross-state plans derive a non-secret azurerm remote-state variable from JSON backend config", async (context) => {
  const workspace = await temporaryDirectory(context);
  const resourceType = "zpa_application_segment";
  const directory = await writeRoot({ backend: "azurerm", label: resourceType, members: [resourceType], workspace });
  await writeText(
    path.join(directory, "main.tf"),
    `${await readFile(path.join(directory, "main.tf"), "utf8")}variable "infrawright_remote_state_backend_config" {\n  type = any\n}\n`,
  );
  await writeText(configPath(workspace, resourceType), `{"${resourceType}_items":{}}\n`);
  const backend = path.join(workspace, "backend.json");
  await writeText(backend, JSON.stringify({
    container_name: "tfstate",
    storage_account_name: "example",
    use_azuread_auth: true,
  }));
  const terraform = new FakeTerraform();
  await planEnvironmentRoots({
    backendConfig: backend,
    deployment: deployment({ zpa: { cross_state_references: true } }),
    importsOnly: false,
    root: await committedRoot(),
    save: false,
    selectors: [resourceType],
    tenant: "tenant",
    terraform,
    workspace,
  });
  assert.deepEqual(
    JSON.parse(terraform.planned[0]?.environment?.TF_VAR_infrawright_remote_state_backend_config ?? ""),
    {
      container_name: "tfstate",
      storage_account_name: "example",
      use_azuread_auth: true,
    },
  );
  assert.equal(terraform.planned[0]?.backendKey, `tenant/${resourceType}.tfstate`);

  const racingTerraform = new FakeTerraform();
  racingTerraform.onInitialize = async () => {
    await writeText(backend, JSON.stringify({
      container_name: "changed",
      storage_account_name: "example",
      use_azuread_auth: true,
    }));
  };
  await assert.rejects(
    planEnvironmentRoots({
      backendConfig: backend,
      deployment: deployment({ zpa: { cross_state_references: true } }),
      importsOnly: false,
      root: await committedRoot(),
      save: false,
      selectors: [resourceType],
      tenant: "tenant",
      terraform: racingTerraform,
      workspace,
    }),
    (error) => {
      assertFailure(error, "INIT_INPUTS_CHANGED");
      return true;
    },
  );
  assert.deepEqual(racingTerraform.planned, []);

  await writeText(backend, 'storage_account_name = "example"\n');
  const invalidTerraform = new FakeTerraform();
  await assert.rejects(
    planEnvironmentRoots({
      backendConfig: backend,
      deployment: deployment({ zpa: { cross_state_references: true } }),
      importsOnly: false,
      root: await committedRoot(),
      save: false,
      selectors: [resourceType],
      tenant: "tenant",
      terraform: invalidTerraform,
      workspace,
    }),
    (error) => {
      assertFailure(error, "INVALID_REFERENCE_BACKEND_CONFIG");
      return true;
    },
  );
  assert.deepEqual(invalidTerraform.initialized, []);
  assert.deepEqual(invalidTerraform.planned, []);

  await writeText(backend, JSON.stringify({
    client_secret: "must-not-enter-state",
    storage_account_name: "example",
  }));
  await assert.rejects(
    planEnvironmentRoots({
      backendConfig: backend,
      deployment: deployment({ zpa: { cross_state_references: true } }),
      importsOnly: false,
      root: await committedRoot(),
      save: false,
      selectors: [resourceType],
      tenant: "tenant",
      terraform: new FakeTerraform(),
      workspace,
    }),
    (error) => {
      const failure = assertFailure(error, "UNSAFE_REFERENCE_BACKEND_CONFIG");
      assert.doesNotMatch(failure.message, /must-not-enter-state/u);
      return true;
    },
  );
});

test("cross-state backend projection allowlists only reviewed non-secret AzureRM fields", async (context) => {
  const workspace = await temporaryDirectory(context);
  const backend = path.join(workspace, "backend.json");
  const allowed = {
    container_name: "tfstate",
    lookup_blob_endpoint: false,
    resource_group_name: "state-rg",
    storage_account_name: "example",
    subscription_id: "00000000-0000-0000-0000-000000000001",
    tenant_id: "00000000-0000-0000-0000-000000000002",
    use_azuread_auth: true,
    use_cli: false,
    use_msi: false,
    use_oidc: true,
  };
  await writeText(backend, JSON.stringify(allowed));
  const environment = await referenceBackendEnvironment(backend);
  assert.deepEqual(
    JSON.parse(environment[REFERENCE_BACKEND_ENVIRONMENT] ?? "null"),
    allowed,
  );

  for (const key of [
    "access_key",
    "client_id",
    "oidc_token_file_path",
    "oidc_token",
    "oidc_request_token",
    "client_secret_file_path",
    "client_certificate_path",
    "key",
    "msi_endpoint",
    "sas_token",
    "unknown_authentication_material",
  ]) {
    const secret = `must-not-echo-${key}`;
    await writeText(backend, JSON.stringify({
      container_name: "tfstate",
      [key]: secret,
      storage_account_name: "example",
    }));
    await assert.rejects(
      referenceBackendEnvironment(backend),
      (error) => {
        const failure = assertFailure(error, "UNSAFE_REFERENCE_BACKEND_CONFIG");
        assert.doesNotMatch(failure.message, new RegExp(secret, "u"));
        return true;
      },
      key,
    );
  }
});

test("cross-state backend projection enforces field types and a stable 64 KiB read", async (context) => {
  const workspace = await temporaryDirectory(context);
  const backend = path.join(workspace, "backend.json");
  for (const document of [
    { container_name: true, storage_account_name: "example" },
    { container_name: "tfstate", storage_account_name: "example", use_oidc: "true" },
  ]) {
    await writeText(backend, JSON.stringify(document));
    await assert.rejects(
      referenceBackendEnvironment(backend),
      (error) => {
        assertFailure(error, "INVALID_REFERENCE_BACKEND_CONFIG");
        return true;
      },
    );
  }

  await writeText(backend, `{"storage_account_name":"${"x".repeat(64 * 1024)}"}`);
  await assert.rejects(
    referenceBackendEnvironment(backend),
    (error) => {
      assertFailure(error, "INVALID_REFERENCE_BACKEND_CONFIG");
      return true;
    },
  );
});

test("HCL config selection and no-config skipping preserve deployment behavior", async (context) => {
  const workspace = await temporaryDirectory(context);
  await writeRoot({ label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
  const hcl = hclConfigPath(workspace, ZIA_RESOURCE);
  await writeText(hcl, `${ZIA_RESOURCE}_items = {}\n`);
  const terraform = new FakeTerraform();
  await planEnvironmentRoots({
    deployment: { overlay: ".", roots: {}, tfvars_format: "hcl" },
    importsOnly: false,
    root: await committedRoot(),
    save: false,
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    terraform,
    workspace,
  });
  assert.deepEqual(terraform.planned[0]?.varFiles, [hcl]);

  await rm(hcl);
  const diagnostics: string[] = [];
  await assert.rejects(planEnvironmentRoots({
    deployment: { overlay: ".", roots: {}, tfvars_format: "hcl" },
    importsOnly: false,
    onDiagnostic: (message) => diagnostics.push(message),
    root: await committedRoot(),
    save: false,
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    terraform: new FakeTerraform(),
    workspace,
  }), (error) => {
    assertFailure(error, "NO_ROOTS_PLANNED");
    return true;
  });
  assert.deepEqual(diagnostics, [`skip ${ZIA_RESOURCE} (no config/tenant/${ZIA_RESOURCE}.auto.tfvars)`]);
});

test("grouped roots fail before Terraform when only some member configs exist", async (context) => {
  const workspace = await temporaryDirectory(context);
  const label = "zia_pair";
  const roots = { zia: { groups: { [label]: [ZIA_RESOURCE, ZIA_SECOND] } } };
  await writeRoot({ label, members: [ZIA_RESOURCE, ZIA_SECOND], workspace });
  await writeText(configPath(workspace, ZIA_RESOURCE), `{"${ZIA_RESOURCE}_items":{}}\n`);
  const terraform = new FakeTerraform();
  await assert.rejects(
    planEnvironmentRoots({
      deployment: deployment(roots),
      importsOnly: false,
      root: await committedRoot(),
      save: false,
      selectors: [ZIA_RESOURCE],
      tenant: "tenant",
      terraform,
      workspace,
    }),
    (error) => {
      const failure = assertFailure(error, "MISSING_GROUP_CONFIG");
      assert.match(failure.message, /zia_admin_users\.auto\.tfvars\.json/u);
      return true;
    },
  );
  assert.deepEqual(terraform.initialized, []);
  assert.deepEqual(terraform.planned, []);
});

test("init and plan mutations both fail closed and remove the saved pair", async (context) => {
  for (const phase of ["init", "plan"] as const) {
    await context.test(phase, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), `infrawright-plan-${phase}-`));
      try {
        const directory = await writeRoot({ label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
        const config = configPath(workspace, ZIA_RESOURCE);
        await writeText(config, `{"${ZIA_RESOURCE}_items":{}}\n`);
        const terraform = new FakeTerraform();
        if (phase === "init") {
          terraform.onInitialize = async () => {
            await writeFile(
              path.join(workspace, "modules", ZIA_RESOURCE, "main.tf"),
              "# changed during init\n",
              "utf8",
            );
          };
        } else {
          terraform.onPlan = async () => {
            await writeFile(config, `{"${ZIA_RESOURCE}_items":{"changed":{}}}\n`, "utf8");
          };
        }
        await assert.rejects(
          planEnvironmentRoots({
            deployment: deployment(),
            importsOnly: false,
            root: await committedRoot(),
            save: true,
            selectors: [ZIA_RESOURCE],
            tenant: "tenant",
            terraform,
            workspace,
          }),
          (error) => {
            assertFailure(
              error,
              phase === "init" ? "INIT_INPUTS_CHANGED" : "PLAN_INPUTS_CHANGED",
            );
            return true;
          },
        );
        await assert.rejects(readFile(path.join(directory, "tfplan")));
        await assert.rejects(readFile(path.join(directory, "tfplan.sources")));
        if (phase === "init") assert.equal(terraform.planned.length, 0);
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("imports-only skips roots containing a derived member and reports no planned roots", async (context) => {
  const workspace = await temporaryDirectory(context);
  await mkdir(envDirectory(workspace, DERIVED), { recursive: true });
  await writeText(configPath(workspace, DERIVED), `{"${DERIVED}_items":{}}\n`);
  const diagnostics: string[] = [];
  const terraform = new FakeTerraform();
  await assert.rejects(
    planEnvironmentRoots({
      deployment: deployment(),
      importsOnly: true,
      onDiagnostic: (message) => diagnostics.push(message),
      root: await committedRoot(),
      save: false,
      selectors: [DERIVED],
      tenant: "tenant",
      terraform,
      workspace,
    }),
    (error) => {
      assertFailure(error, "NO_ROOTS_PLANNED");
      return true;
    },
  );
  assert.deepEqual(diagnostics, [
    `skip ${DERIVED} (IMPORTS_ONLY: derived/non-importable member ${DERIVED})`,
  ]);
  assert.deepEqual(terraform.initialized, []);
});

test("failed Terraform plan removes a partial saved pair", async (context) => {
  const workspace = await temporaryDirectory(context);
  const directory = await writeRoot({ label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
  await writeText(configPath(workspace, ZIA_RESOURCE), `{"${ZIA_RESOURCE}_items":{}}\n`);
  const terraform = new FakeTerraform();
  terraform.onPlan = () => {
    throw new Error("fake plan failed");
  };
  await assert.rejects(planEnvironmentRoots({
    deployment: deployment(),
    importsOnly: false,
    root: await committedRoot(),
    save: true,
    selectors: [ZIA_RESOURCE],
    tenant: "tenant",
    terraform,
    workspace,
  }), /fake plan failed/u);
  await assert.rejects(readFile(path.join(directory, "tfplan")));
  await assert.rejects(readFile(path.join(directory, "tfplan.sources")));
});

test("failed init and missing saved output both remove partial plan artifacts", async (context) => {
  for (const phase of ["init", "missing-plan"] as const) {
    await context.test(phase, async () => {
      const workspace = await mkdtemp(path.join(os.tmpdir(), `infrawright-plan-${phase}-`));
      try {
        const directory = await writeRoot({ label: ZIA_RESOURCE, members: [ZIA_RESOURCE], workspace });
        await writeText(configPath(workspace, ZIA_RESOURCE), `{"${ZIA_RESOURCE}_items":{}}\n`);
        const terraform: PlanTerraform = {
          initialize: async () => {
            if (phase === "init") {
              await writeText(path.join(directory, "tfplan"), "partial-plan");
              await writeText(path.join(directory, "tfplan.sources"), "partial-sources");
              throw new Error("fake init failed");
            }
          },
          plan: async () => undefined,
        };
        await assert.rejects(planEnvironmentRoots({
          deployment: deployment(),
          importsOnly: false,
          root: await committedRoot(),
          save: true,
          selectors: [ZIA_RESOURCE],
          tenant: "tenant",
          terraform,
          workspace,
        }), phase === "init" ? /fake init failed/u : (error) => {
          assertFailure(error, "MISSING_SAVED_PLAN");
          return true;
        });
        await assert.rejects(readFile(path.join(directory, "tfplan")));
        await assert.rejects(readFile(path.join(directory, "tfplan.sources")));
      } finally {
        await rm(workspace, { force: true, recursive: true });
      }
    });
  }
});

test("clean-plans without a tenant removes selected pairs across tenants", async (context) => {
  const workspace = await temporaryDirectory(context);
  for (const tenant of ["alpha", "beta"]) {
    const directory = path.join(workspace, "envs", tenant, ZIA_RESOURCE);
    await writeText(path.join(directory, "tfplan"), tenant);
    await writeText(path.join(directory, "tfplan.sources"), "{}\n");
  }
  const diagnostics: string[] = [];
  assert.deepEqual(await cleanPlans({
    deployment: deployment(),
    onDiagnostic: (message) => diagnostics.push(message),
    root: await committedRoot(),
    selectors: [ZIA_RESOURCE],
    tenant: null,
    workspace,
  }), { removed: 2 });
  assert.equal(diagnostics.at(-1), "2 stale plan(s) removed");
  await assert.rejects(readFile(path.join(workspace, "envs", "alpha", ZIA_RESOURCE, "tfplan")));
  await assert.rejects(readFile(path.join(workspace, "envs", "beta", ZIA_RESOURCE, "tfplan")));
});
