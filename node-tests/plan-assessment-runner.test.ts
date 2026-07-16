import assert from "node:assert/strict";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { loadBoundAssessmentDeployment } from "../node-src/domain/deployment.js";
import { runSavedPlanAssertion } from "../node-src/domain/plan-assessment-runner.js";
import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";
import type { Deployment } from "../node-src/domain/types.js";
import type { LoadedPackRoot } from "../node-src/metadata/loader.js";

function root(manifest: Record<string, unknown> = {}): LoadedPackRoot {
  return {
    packs: {
      manifests: [{
        name: "sample",
        directory: "/packs/sample",
        path: "/packs/sample/pack.json",
        data: manifest,
        providerPrefixes: { sample_: "sample" },
        providerSources: { sample: "example/sample" },
        requiresShared: [],
      }],
      providerPrefixes: { sample_: "sample" },
      providerSources: { sample: "example/sample" },
      providerOwners: { sample: "sample" },
      root: "/packs",
    },
    resources: new Map([[
      "sample_resource",
      {
        type: "sample_resource",
        product: "sample",
        provider: "sample",
        pack: "sample",
        registry: { generate: true },
        override: null,
      },
    ]]),
  } as unknown as LoadedPackRoot;
}

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function executable(directory: string, plan: object): string {
  return scriptExecutable(
    directory,
    `printf '%s' ${shellLiteral(JSON.stringify(plan))}`,
  );
}

function scriptExecutable(directory: string, body: string): string {
  const file = join(directory, "terraform-fake");
  writeFileSync(file, `#!/bin/sh\n${body}\n`, {
    mode: 0o700,
  });
  chmodSync(file, 0o700);
  return file;
}

function terraformPlan(change: object): object {
  return {
    format_version: "1.2",
    terraform_version: "1.15.4",
    complete: true,
    errored: false,
    resource_changes: [{
      address: 'sample_resource.this["one"]',
      type: "sample_resource",
      change,
    }],
    output_changes: {},
  };
}

async function fixture(
  callback: (options: {
    readonly workspace: string;
    readonly deployment: Deployment;
    readonly envDir: string;
    readonly varFile: string;
  }) => Promise<void>,
): Promise<void> {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-runner-"));
  try {
    const envDir = join(workspace, "envs", "tenant", "sample_resource");
    const moduleDir = join(workspace, "modules", "sample_resource");
    const varFile = join(
      workspace,
      "config",
      "tenant",
      "sample_resource.auto.tfvars.json",
    );
    mkdirSync(envDir, { recursive: true });
    mkdirSync(moduleDir, { recursive: true });
    mkdirSync(join(workspace, "config", "tenant"), { recursive: true });
    writeFileSync(join(moduleDir, "main.tf"), "# module\n");
    writeFileSync(join(envDir, "main.tf"), [
      'module "sample_resource" {',
      `  source = "${relative(envDir, moduleDir)}"`,
      "  items = var.sample_resource_items",
      "}",
      "",
    ].join("\n"));
    writeFileSync(varFile, "{}\n");
    writeFileSync(join(envDir, "tfplan"), "opaque saved plan\n", { mode: 0o600 });
    writeFileSync(join(envDir, "tfplan.sources"), `${JSON.stringify(
      await planFingerprintV2({
        envDir,
        varFiles: [varFile],
        memberTypes: ["sample_resource"],
        backendConfig: null,
        backendKey: null,
      }),
    )}\n`);
    await callback({
      workspace,
      deployment: { overlay: workspace, roots: {} },
      envDir,
      varFile,
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

test("assert-adoptable writes matched guidance but keeps a blocked plan blocked", async () => {
  await fixture(async ({ workspace, deployment }) => {
    const reportPath = join(workspace, "reports", "assessment.json");
    const diagnostics: string[] = [];
    const terraform = executable(workspace, terraformPlan({
      actions: ["update"],
      before: { status: "old" },
      after: { status: "new" },
    }));
    let caught: unknown;
    try {
      await runSavedPlanAssertion({
        workspace,
        deployment,
        root: root({
          dynamic_schema: {
            rules: [{
              id: "sample_status",
              path: "status",
              kind: "provider_observed_projection_unsafe",
              ownership: "unknown",
              action: "manual_review_required",
              evidence: "sample.md",
              reason: "status needs review",
              resource_type: "sample_resource",
              provider_version_constraint: "1.0.0",
            }],
          },
        }),
        mode: "assert-adoptable",
        tenant: "tenant",
        selectors: [],
        terraformExecutable: terraform,
        backendConfig: null,
        policyPath: null,
        reportPath,
        onDiagnostic: (message) => diagnostics.push(message),
      });
    } catch (error: unknown) {
      caught = error;
    }
    assert.ok(caught instanceof ProcessFailure);
    assert.equal(caught.code, "PLAN_NOT_ADOPTABLE");
    const report = JSON.parse(readFileSync(reportPath, "utf8")) as {
      roots: Array<{ guidance: Array<Record<string, unknown>> }>;
      summary: { status: string };
    };
    assert.equal(report.summary.status, "blocked");
    assert.deepEqual(report.roots[0]?.guidance.map((entry) => ({
      lane: entry.lane,
      matched: entry.matched_plan_path,
      finding: entry.finding_path,
    })), [{ lane: "dynamic_schema", matched: "status", finding: "status" }]);
    assert.ok(diagnostics.includes("BLOCKED: tenant/sample_resource"));
    assert.ok(diagnostics.includes("  Dynamic-schema guidance:"));
  });
});

test("assert-clean writes its exact report to stdout and emits the success diagnostic", async () => {
  await fixture(async ({ workspace, deployment }) => {
    const diagnostics: string[] = [];
    let stdout = "";
    await runSavedPlanAssertion({
      workspace,
      deployment,
      root: root(),
      mode: "assert-clean",
      tenant: "tenant",
      selectors: [],
      terraformExecutable: executable(workspace, terraformPlan({
        actions: ["no-op"],
        before: {},
        after: {},
      })),
      backendConfig: null,
      policyPath: null,
      reportPath: "-",
      onDiagnostic: (message) => diagnostics.push(message),
      stdout: (text) => { stdout += text; },
    });
    const report = JSON.parse(stdout) as { mode: string; summary: { status: string } };
    assert.equal(report.mode, "assert-clean");
    assert.equal(report.summary.status, "clean");
    assert.deepEqual(diagnostics, [
      "all 1 saved plan(s) clean (no-op/imports only)",
    ]);
  });
});

test("missing plans preserve the original no-saved-plans failure and error report", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-runner-empty-"));
  try {
    const reportPath = join(workspace, "assessment.json");
    let caught: unknown;
    try {
      await runSavedPlanAssertion({
        workspace,
        deployment: { overlay: workspace, roots: {} },
        root: root(),
        mode: "assert-clean",
        tenant: "tenant",
        selectors: [],
        terraformExecutable: "/missing/terraform",
        backendConfig: null,
        policyPath: null,
        reportPath,
      });
    } catch (error: unknown) {
      caught = error;
    }
    assert.ok(caught instanceof ProcessFailure);
    assert.equal(caught.code, "NO_SAVED_PLANS");
    assert.equal(
      caught.message,
      "no saved plans to check - run make plan SAVE=1 first",
    );
    const report = JSON.parse(readFileSync(reportPath, "utf8")) as {
      error: { kind: string; message: string };
    };
    assert.deepEqual(report.error, {
      kind: "no_saved_plans",
      message: "no saved plans to check - run make plan SAVE=1 first",
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});

test("policy errors precede topology loading and retain policy evidence", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-policy-preflight-"));
  try {
    const policyPath = join(workspace, "policy.json");
    const reportPath = join(workspace, "assessment.json");
    writeFileSync(policyPath, '{"version":1,"resource_types":');
    let loaded = false;
    let caught: unknown;
    try {
      await runSavedPlanAssertion({
        workspace,
        loadInputs: async () => {
          loaded = true;
          throw new ProcessFailure({
            code: "UNKNOWN_RESOURCE_SELECTOR",
            category: "domain",
            message: "unknown selector",
          });
        },
        mode: "assert-adoptable",
        tenant: "tenant",
        selectors: ["missing"],
        terraformExecutable: async () => {
          throw new Error("Terraform must not be resolved");
        },
        backendConfig: null,
        policyPath,
        reportPath,
      });
    } catch (error: unknown) {
      caught = error;
    }
    assert.equal(loaded, false);
    assert.ok(caught instanceof ProcessFailure);
    assert.equal(caught.code, "INVALID_DRIFT_POLICY");
    const report = JSON.parse(readFileSync(reportPath, "utf8")) as {
      request: { policy_sha256: string | null };
      error: { kind: string };
    };
    assert.equal(report.error.kind, "policy_error");
    assert.match(report.request.policy_sha256 ?? "", /^[0-9a-f]{64}$/u);
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});

test("lazy input failures and invalid tenant requests still write error reports", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-input-report-"));
  try {
    for (const [tenant, name] of [["tenant", "load"], ["", "tenant"]] as const) {
      const reportPath = join(workspace, `${name}.json`);
      const diagnostics: string[] = [];
      let caught: unknown;
      try {
        await runSavedPlanAssertion({
          workspace,
          loadInputs: async () => {
            if (name === "load") {
              throw new ProcessFailure({
                code: "INVALID_DEPLOYMENT",
                category: "domain",
                message: "deployment is not valid JSON",
              });
            }
            return {
              deployment: { overlay: workspace, roots: {} },
              root: root(),
            };
          },
          mode: "assert-clean",
          tenant,
          selectors: [],
          terraformExecutable: "/missing/terraform",
          backendConfig: null,
          policyPath: null,
          reportPath,
          onDiagnostic: (message) => diagnostics.push(message),
        });
      } catch (error: unknown) {
        caught = error;
      }
      assert.ok(caught instanceof ProcessFailure);
      const report = JSON.parse(readFileSync(reportPath, "utf8")) as {
        request: { tenant: string | null };
        summary: { status: string };
      };
      assert.equal(report.request.tenant, tenant);
      assert.equal(report.summary.status, "error");
      assert.equal(diagnostics.some((line) => line.startsWith("WARNING:")), false);
    }
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});

test("deployment control changes during Terraform show cannot publish clean", async () => {
  await fixture(async ({ workspace }) => {
    const deploymentPath = join(workspace, "deployment.json");
    const reportPath = join(workspace, "assessment.json");
    writeFileSync(deploymentPath, `${JSON.stringify({ overlay: workspace, roots: {} })}\n`);
    const changed = JSON.stringify({ overlay: join(workspace, "moved"), roots: {} });
    const terraform = scriptExecutable(workspace, [
      `printf '%s\\n' ${shellLiteral(changed)} > ${shellLiteral(deploymentPath)}`,
      `printf '%s' ${shellLiteral(JSON.stringify(terraformPlan({
        actions: ["no-op"],
        before: {},
        after: {},
      })))}`,
    ].join("\n"));
    let caught: unknown;
    try {
      await runSavedPlanAssertion({
        workspace,
        loadInputs: async () => {
          const bound = await loadBoundAssessmentDeployment(deploymentPath);
          return {
            deployment: bound.deployment,
            root: root(),
            controlFiles: [bound.file],
          };
        },
        mode: "assert-clean",
        tenant: "tenant",
        selectors: [],
        terraformExecutable: terraform,
        backendConfig: null,
        policyPath: null,
        reportPath,
      });
    } catch (error: unknown) {
      caught = error;
    }
    assert.ok(caught instanceof ProcessFailure);
    assert.equal(caught.code, "ASSESSMENT_CONTROL_CHANGED");
    const report = JSON.parse(readFileSync(reportPath, "utf8")) as {
      summary: { status: string };
      error: { kind: string };
    };
    assert.equal(report.summary.status, "error");
    assert.equal(report.error.kind, "assessment_error");
  });
});
