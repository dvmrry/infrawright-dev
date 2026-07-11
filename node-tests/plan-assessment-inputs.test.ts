import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { resolveSavedPlanAssessmentOptions } from "../node-src/domain/plan-assessment-inputs.js";
import type { Deployment, RootCatalog } from "../node-src/domain/types.js";

const CATALOG: RootCatalog = {
  kind: "infrawright.root_catalog",
  schema_version: 1,
  declared_providers: ["zpa"],
  resources: [{
    type: "zpa_sample",
    product: "zpa",
    provider: "zpa",
    bare_name: "sample",
    slug_label: null,
    generated: true,
    derived: false,
  }],
  source_files: [],
  sources_sha256: "0".repeat(64),
};

function failure(error: unknown, code: string): boolean {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return true;
}

test("materialized plan roots resolve exact JSON and HCL assessment inputs", async (t) => {
  for (const format of ["json", "hcl"] as const) {
    await t.test(format, async () => {
      const workspace = mkdtempSync(join(tmpdir(), "assessment-inputs-"));
      try {
        const envDir = join(workspace, "envs", "tenant", "zpa_sample");
        mkdirSync(envDir, { recursive: true });
        writeFileSync(join(envDir, "tfplan"), "plan\n");
        const deployment: Deployment = {
          overlay: ".",
          roots: {},
          tfvars_format: format,
        };
        const resolved = await resolveSavedPlanAssessmentOptions({
          workspace,
          deployment,
          catalog: CATALOG,
          tenant: "tenant",
          selectors: [],
          terraformExecutable: "/opt/terraform",
          backendConfig: "backend.hcl",
          policyPath: "policy.json",
        });
        assert.deepEqual(resolved.roots, [{
          tenant: "tenant",
          label: "zpa_sample",
          members: ["zpa_sample"],
          envDir,
          savedPlanPath: join(envDir, "tfplan"),
          fingerprintPath: join(envDir, "tfplan.sources"),
          varFiles: [join(
            workspace,
            "config",
            "tenant",
            `zpa_sample.auto.tfvars${format === "json" ? ".json" : ""}`,
          )],
        }]);
        assert.equal(resolved.backendConfig, join(workspace, "backend.hcl"));
        assert.equal(resolved.policyPath, join(workspace, "policy.json"));
        assert.equal(resolved.terraformExecutable, "/opt/terraform");
      } finally {
        rmSync(workspace, { recursive: true, force: true });
      }
    });
  }
});

test("resolver returns no roots without plans and defers tfvars validation until needed", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-inputs-"));
  try {
    const deployment = {
      overlay: ".",
      roots: {},
      tfvars_format: "unsupported",
    };
    const empty = await resolveSavedPlanAssessmentOptions({
      workspace,
      deployment,
      catalog: CATALOG,
      tenant: "tenant",
      selectors: [],
      terraformExecutable: "/opt/terraform",
      backendConfig: null,
      policyPath: null,
    });
    assert.deepEqual(empty.roots, []);

    const envDir = join(workspace, "envs", "tenant", "zpa_sample");
    mkdirSync(envDir, { recursive: true });
    writeFileSync(join(envDir, "tfplan"), "plan\n");
    await assert.rejects(
      resolveSavedPlanAssessmentOptions({
        workspace,
        deployment,
        catalog: CATALOG,
        tenant: "tenant",
        selectors: [],
        terraformExecutable: "/opt/terraform",
        backendConfig: null,
        policyPath: null,
      }),
      (error: unknown) => failure(error, "INVALID_DEPLOYMENT"),
    );
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});

test("resolver rejects an explicit null tfvars format when a plan is selected", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-inputs-"));
  try {
    const envDir = join(workspace, "envs", "tenant", "zpa_sample");
    mkdirSync(envDir, { recursive: true });
    writeFileSync(join(envDir, "tfplan"), "plan\n");
    await assert.rejects(
      resolveSavedPlanAssessmentOptions({
        workspace,
        deployment: { overlay: ".", roots: {}, tfvars_format: null },
        catalog: CATALOG,
        tenant: "tenant",
        selectors: [],
        terraformExecutable: "/trusted/terraform",
        backendConfig: null,
        policyPath: null,
      }),
      (error: unknown) => {
        return error instanceof ProcessFailure
          && error.code === "INVALID_DEPLOYMENT";
      },
    );
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});

test("resolver snapshots mutable options before asynchronous discovery", async () => {
  const workspace = mkdtempSync(join(tmpdir(), "assessment-inputs-"));
  try {
    const envDir = join(workspace, "envs", "tenant", "zpa_sample");
    mkdirSync(envDir, { recursive: true });
    writeFileSync(join(envDir, "tfplan"), "plan\n");
    const deployment = {
      overlay: ".",
      roots: {},
      tfvars_format: "json",
    };
    const options = {
      workspace,
      deployment,
      catalog: CATALOG,
      tenant: "tenant",
      selectors: [] as string[],
      terraformExecutable: "/opt/terraform",
      backendConfig: "backend.hcl" as string | null,
      policyPath: "policy.json" as string | null,
    };
    const pending = resolveSavedPlanAssessmentOptions(options);
    deployment.overlay = "mutated";
    deployment.tfvars_format = "hcl";
    options.workspace = "/mutated-workspace";
    options.backendConfig = null;
    options.policyPath = null;
    options.selectors.push("zpa/other");
    const resolved = await pending;
    assert.equal(resolved.roots[0]?.envDir, envDir);
    assert.equal(
      resolved.roots[0]?.varFiles[0],
      join(workspace, "config", "tenant", "zpa_sample.auto.tfvars.json"),
    );
    assert.equal(resolved.backendConfig, join(workspace, "backend.hcl"));
    assert.equal(resolved.policyPath, join(workspace, "policy.json"));
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});
