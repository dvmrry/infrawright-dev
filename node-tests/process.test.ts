import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
} from "../node-src/contracts/validators.js";
import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../node-src/json/python-compatible.js";

const WORKSPACE = process.cwd();
const PROCESS_MAIN = path.join(
  WORKSPACE,
  ".node-test/node-src/process/main.js",
);

function invoke(
  input: string | Buffer,
  cwd = WORKSPACE,
  environment: Readonly<Record<string, string>> = {},
) {
  const env = { ...process.env };
  delete env.INFRAWRIGHT_TERRAFORM_EXECUTABLE;
  Object.assign(env, environment);
  return spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd,
    input,
    env,
    encoding: Buffer.isBuffer(input) ? undefined : "utf8",
  });
}

function assessmentPlan(change: object): string {
  return JSON.stringify({
    format_version: "1.2",
    terraform_version: "1.15.4",
    complete: true,
    errored: false,
    resource_changes: [{
      address: 'zpa_application_segment.this["one"]',
      type: "zpa_application_segment",
      change,
    }],
    output_changes: {},
  });
}

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

async function withAssessmentFixture(
  planJson: string,
  callback: (fixture: {
    readonly workspace: string;
    readonly terraform: string;
    readonly request: Record<string, unknown>;
    readonly envDir: string;
    readonly varFile: string;
    readonly planPath: string;
    readonly planJson: string;
    readonly policyPath: string | null;
  }) => void | Promise<void>,
  policy: object | null = null,
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "infrawright-process-assess-"));
  try {
    const resourceType = "zpa_application_segment";
    const envDir = path.join(workspace, "envs", "tenant", resourceType);
    const moduleDir = path.join(workspace, "modules", resourceType);
    const configDir = path.join(workspace, "config", "tenant");
    mkdirSync(envDir, { recursive: true });
    mkdirSync(moduleDir, { recursive: true });
    mkdirSync(configDir, { recursive: true });
    writeFileSync(path.join(workspace, "deployment.json"), JSON.stringify({
      overlay: ".",
      roots: {},
    }));
    writeFileSync(path.join(moduleDir, "main.tf"), "# module\n");
    writeFileSync(path.join(envDir, "main.tf"), [
      `module "${resourceType}" {`,
      `  source = "../../../modules/${resourceType}"`,
      `  items = var.${resourceType}_items`,
      "}",
      "",
    ].join("\n"));
    const varFile = path.join(configDir, `${resourceType}.auto.tfvars.json`);
    writeFileSync(varFile, "{}\n");
    const planPath = path.join(envDir, "tfplan");
    writeFileSync(planPath, "opaque plan bytes\n", {
      mode: 0o600,
    });
    writeFileSync(
      path.join(envDir, "tfplan.sources"),
      `${JSON.stringify(await planFingerprintV2({
        envDir,
        varFiles: [varFile],
        memberTypes: [resourceType],
        backendConfig: null,
        backendKey: null,
      }))}\n`,
    );
    const terraform = path.join(workspace, "terraform-fake");
    writeFileSync(
      terraform,
      `#!/bin/sh\nprintf '%s' ${shellLiteral(planJson)}\n`,
      { mode: 0o700 },
    );
    chmodSync(terraform, 0o700);
    const policyPath = policy === null ? null : path.join(workspace, "policy.json");
    if (policyPath !== null) {
      writeFileSync(policyPath, JSON.stringify(policy));
    }
    await callback({
      workspace,
      terraform,
      envDir,
      varFile,
      planPath,
      planJson,
      policyPath,
      request: {
        kind: "infrawright.process_request",
        schema_version: 1,
        request_id: "assessment",
        operation: "assess_saved_plans",
        context: {
          workspace,
          deployment: "deployment.json",
          root_catalog: path.join(
            WORKSPACE,
            "catalogs/zscaler-root-catalog.v1.json",
          ),
        },
        input: {
          mode: policy === null ? "assert-clean" : "assert-adoptable",
          tenant: "tenant",
          selectors: ["zpa/application_segment"],
          backend_config: null,
          policy: policy === null ? null : "policy.json",
        },
      },
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

const PYTHON_ASSESSMENT_REPORT = String.raw`
import hashlib
import json
import sys
from engine import ops
from engine.drift_policy import DriftPolicy
from engine.plan_eval import classify_plan

i = json.load(sys.stdin)
plan = json.loads(i["plan_json"])
policy = DriftPolicy(None)
policy_sha = None
if i["policy_path"] is not None:
    with open(i["policy_path"], "rb") as f:
        policy_bytes = f.read()
    policy_sha = hashlib.sha256(policy_bytes).hexdigest()
    policy = DriftPolicy(json.loads(policy_bytes.decode("utf-8")))
result = classify_plan(plan, policy=policy)
report = ops._new_assessment_report(i["mode"], {
    "tenant": "tenant",
    "selectors": ["zpa/application_segment"],
    "policy": None if i["mode"] == "assert-clean" else "policy.json",
})
report["request"]["policy_sha256"] = policy_sha
with open(i["plan_path"], "rb") as f:
    plan_sha = hashlib.sha256(f.read()).hexdigest()
fingerprint = ops._plan_fingerprint(
    i["env_dir"], [i["var_file"]], ["zpa_application_segment"],
    backend_config=None, backend_key=None,
)
ops._append_root_assessment(
    report, "tenant", "zpa_application_segment",
    ["zpa_application_segment"], plan, result, plan_sha, fingerprint,
    guidance=[],
)
for resource_type, mode, path in policy.stale_entries(
        resource_types={"zpa_application_segment"},
        modes=("plan_tolerate",)):
    report["stale_policy"].append({
        "resource_type": resource_type,
        "mode": mode,
        "path": path,
    })
counts = {
    "clean": 1 if result["status"] == "clean" else 0,
    "tolerated": 1 if result["status"] == "clean_with_tolerated_drift" else 0,
    "blocked": 1 if result["status"] == "blocked" else 0,
}
ops._finish_assessment_report(
    report, counts["clean"], counts["tolerated"], counts["blocked"])
sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
`;

function pythonAssessmentReport(fixture: {
  readonly envDir: string;
  readonly varFile: string;
  readonly planPath: string;
  readonly planJson: string;
  readonly policyPath: string | null;
}, mode: "assert-clean" | "assert-adoptable"): {
  readonly report: unknown;
  readonly bytes: string;
} {
  const result = spawnSync("python3", ["-c", PYTHON_ASSESSMENT_REPORT], {
    input: JSON.stringify({
      env_dir: fixture.envDir,
      var_file: fixture.varFile,
      plan_path: fixture.planPath,
      plan_json: fixture.planJson,
      policy_path: fixture.policyPath,
      mode,
    }),
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  return {
    report: JSON.parse(result.stdout),
    bytes: result.stdout,
  };
}

function assertPythonAssessmentParity(
  actual: unknown,
  expected: { readonly report: unknown; readonly bytes: string },
): void {
  assert.deepEqual(actual, expected.report);
  assert.equal(
    renderPythonCompatibleJson(actual as JsonValue),
    expected.bytes,
  );
}

test("process host emits one structured roots response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-roots",
    operation: "roots",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      tenant: "prod",
      selectors: ["zpa/application_segment"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.kind, "infrawright.process_response");
  assert.equal(response.request_id, "test-roots");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.equal(response.result.kind, "infrawright.root_topology");
  assert.ok(String(result.stdout).endsWith("\n"));
});

test("process host emits one structured scope_paths response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-scope-paths",
    operation: "scope_paths",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      paths: ["config/prod/zpa_application_segment.auto.tfvars.json"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.request_id, "test-scope-paths");
  assert.equal(response.operation, "scope_paths");
  assert.equal(response.status, "ok");
  assert.deepEqual(response.diagnostics, []);
  assert.equal(response.result.kind, "infrawright.changed_path_scope");
});

test("process host emits one structured plan_roots response", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "test-plan-roots",
    operation: "plan_roots",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      tenant: "not-materialized",
      selectors: ["zpa/application_segment"],
    },
  };
  const result = invoke(JSON.stringify(request));
  assert.equal(result.status, 0, String(result.stderr));
  assert.equal(String(result.stderr), "");
  const response = JSON.parse(String(result.stdout));
  assert.equal(response.request_id, "test-plan-roots");
  assert.equal(response.operation, "plan_roots");
  assert.equal(response.result.kind, "infrawright.plan_roots");
  assert.deepEqual(response.result.roots, []);
});

test("assessment process emits clean and blocked Zscaler reports with gate exits", async (t) => {
  await t.test("clean", async () => {
    await withAssessmentFixture(assessmentPlan({
      actions: ["no-op"],
      before: { status: "same" },
      after: { status: "same" },
    }), (fixture) => {
      const result = invoke(JSON.stringify(fixture.request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: fixture.terraform,
      });
      assert.equal(result.status, 0, String(result.stderr));
      assert.equal(String(result.stderr), "");
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      const forged = structuredClone(response);
      forged.result.summary.checked = 999;
      forged.result.summary.clean = 999;
      assert.equal(validateProcessResponse(forged), false);
      assert.ok(validateProcessResponse.errors?.some((error) => {
        return error.keyword === "x-infrawright-report-semantics"
          && error.instancePath === "/result/summary/checked";
      }));
      const duplicate = structuredClone(response);
      duplicate.result.roots.push(structuredClone(duplicate.result.roots[0]));
      duplicate.result.summary.checked = 2;
      duplicate.result.summary.clean = 2;
      assert.equal(validateProcessResponse(duplicate), false);
      assert.ok(validateProcessResponse.errors?.some((error) => {
        return error.keyword === "x-infrawright-report-semantics"
          && error.instancePath === "/result/roots/1";
      }));
      assert.equal(response.status, "ok");
      assert.equal(response.operation, "assess_saved_plans");
      assert.equal(response.result.kind, "infrawright.saved_plan_assessment");
      assert.equal(response.result.summary.status, "clean");
      assert.deepEqual(response.result.roots[0].guidance, []);
      assertPythonAssessmentParity(
        response.result,
        pythonAssessmentReport(fixture, "assert-clean"),
      );
    });
  });

  await t.test("blocked", async () => {
    await withAssessmentFixture(assessmentPlan({
      actions: ["update"],
      before: { status: "old" },
      after: { status: "new" },
    }), (fixture) => {
      const result = invoke(JSON.stringify(fixture.request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: fixture.terraform,
      });
      assert.equal(result.status, 3, String(result.stderr));
      assert.equal(String(result.stderr), "");
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      assert.equal(response.status, "ok");
      assert.equal(response.result.summary.status, "blocked");
      assert.deepEqual(response.result.roots[0].guidance, []);
      assertPythonAssessmentParity(
        response.result,
        pythonAssessmentReport(fixture, "assert-clean"),
      );
    });
  });
});

test("assessment process emits tolerated and error reports without losing stdout", async (t) => {
  const policy = {
    version: 1,
    resource_types: {
      zpa_application_segment: {
        plan_tolerate: [{
          path: "status",
          reason: "fixture",
          approved_by: "unit",
        }, {
          path: "unused",
          reason: "stale fixture",
          approved_by: "unit",
        }],
      },
    },
  };
  await t.test("tolerated", async () => {
    await withAssessmentFixture(assessmentPlan({
      actions: ["update"],
      before: { status: "old" },
      after: { status: "new" },
    }), (fixture) => {
      const result = invoke(JSON.stringify(fixture.request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: fixture.terraform,
      });
      assert.equal(result.status, 0, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      assert.equal(
        response.result.summary.status,
        "clean_with_tolerated_drift",
      );
      assert.match(response.result.request.policy_sha256, /^[0-9a-f]{64}$/);
      assert.deepEqual(response.result.stale_policy, [{
        resource_type: "zpa_application_segment",
        mode: "plan_tolerate",
        path: "unused",
      }]);
      assertPythonAssessmentParity(
        response.result,
        pythonAssessmentReport(fixture, "assert-adoptable"),
      );
    }, policy);
  });

  await t.test("assessment error", async () => {
    await withAssessmentFixture("invalid-json", ({ request, terraform }) => {
      const result = invoke(JSON.stringify(request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      assert.equal(String(result.stderr), "");
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      assert.equal(response.status, "ok");
      assert.equal(response.result.summary.status, "error");
      assert.equal(response.result.error.kind, "assessment_error");
    });
  });
});

test("assessment process requires host Terraform and the exact Zscaler catalog", async (t) => {
  await withAssessmentFixture(assessmentPlan({
    actions: ["no-op"],
    before: {},
    after: {},
  }), async ({ request, terraform, workspace }) => {
    await t.test("missing host configuration", () => {
      const result = invoke(JSON.stringify(request));
      assert.equal(result.status, 1);
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.status, "error");
      assert.equal(response.result, null);
      assert.equal(response.error.code, "TERRAFORM_NOT_CONFIGURED");
    });

    await t.test("unsupported catalog", () => {
      const expectedPath = path.join(
        WORKSPACE,
        "catalogs/zscaler-root-catalog.v1.json",
      );
      const changed = JSON.parse(readFileSync(expectedPath, "utf8"));
      changed.sources_sha256 = "0".repeat(64);
      const changedPath = path.join(workspace, "changed-catalog.json");
      writeFileSync(changedPath, JSON.stringify(changed));
      const changedRequest = {
        ...request,
        context: {
          ...(request.context as Record<string, unknown>),
          root_catalog: changedPath,
        },
      };
      const result = invoke(JSON.stringify(changedRequest), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 2);
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.status, "error");
      assert.equal(response.error.code, "UNSUPPORTED_ASSESSMENT_CATALOG");
    });

    await t.test("no saved plans remains a versioned assessment error", () => {
      const missingRequest = {
        ...request,
        input: {
          ...(request.input as Record<string, unknown>),
          tenant: "missing",
        },
      };
      const result = invoke(JSON.stringify(missingRequest), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1);
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.status, "ok");
      assert.equal(response.result.summary.status, "error");
      assert.equal(response.result.error.kind, "no_saved_plans");
    });
  });
});

test("assessment process binds deployment and catalog controls through show", async (t) => {
  const plan = assessmentPlan({
    actions: ["no-op"],
    before: {},
    after: {},
  });

  await t.test("deployment mutation", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const deploymentPath = path.join(workspace, "deployment.json");
      writeFileSync(terraform, [
        "#!/bin/sh",
        `printf '%s' ${shellLiteral(JSON.stringify({
          overlay: ".",
          roots: {},
          tfvars_format: "hcl",
        }))} > ${shellLiteral(deploymentPath)}`,
        `printf '%s' ${shellLiteral(plan)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      const result = invoke(JSON.stringify(request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      assert.equal(response.status, "ok");
      assert.equal(response.result.summary.status, "error");
      assert.equal(response.result.error.kind, "assessment_error");
      assert.match(response.result.error.message, /control input changed/);
      assert.equal(String(result.stdout).includes(workspace), false);
    });
  });

  await t.test("catalog mutation", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const sourceCatalogPath = path.join(
        WORKSPACE,
        "catalogs/zscaler-root-catalog.v1.json",
      );
      const catalogPath = path.join(workspace, "catalog.json");
      const changedCatalog = JSON.parse(readFileSync(sourceCatalogPath, "utf8"));
      writeFileSync(catalogPath, readFileSync(sourceCatalogPath));
      changedCatalog.sources_sha256 = "0".repeat(64);
      writeFileSync(terraform, [
        "#!/bin/sh",
        `printf '%s' ${shellLiteral(JSON.stringify(changedCatalog))} > ${shellLiteral(catalogPath)}`,
        `printf '%s' ${shellLiteral(plan)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      const changedRequest = {
        ...request,
        context: {
          ...(request.context as Record<string, unknown>),
          root_catalog: catalogPath,
        },
      };
      const result = invoke(JSON.stringify(changedRequest), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(validateProcessResponse(response), true);
      assert.equal(response.status, "ok");
      assert.equal(response.result.summary.status, "error");
      assert.equal(response.result.error.kind, "assessment_error");
      assert.match(response.result.error.message, /control input changed/);
      assert.equal(String(result.stdout).includes(workspace), false);
    });
  });

  await t.test("missing deployment created during show", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const deploymentPath = path.join(workspace, "deployment.json");
      rmSync(deploymentPath);
      writeFileSync(terraform, [
        "#!/bin/sh",
        `printf '%s' ${shellLiteral(JSON.stringify({
          overlay: ".",
          roots: {},
          tfvars_format: "hcl",
        }))} > ${shellLiteral(deploymentPath)}`,
        `printf '%s' ${shellLiteral(plan)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      const result = invoke(JSON.stringify(request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.result.summary.status, "error");
      assert.match(response.result.error.message, /control input changed/);
    });
  });

  await t.test("dangling deployment symlink retains missing-file semantics", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const deploymentPath = path.join(workspace, "deployment.json");
      const targetPath = path.join(workspace, "deployment-target.json");
      rmSync(deploymentPath);
      symlinkSync(targetPath, deploymentPath);
      const result = invoke(JSON.stringify(request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 0, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.result.summary.status, "clean");
    });
  });

  await t.test("dangling deployment target created during show", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const deploymentPath = path.join(workspace, "deployment.json");
      const targetPath = path.join(workspace, "deployment-target.json");
      rmSync(deploymentPath);
      symlinkSync(targetPath, deploymentPath);
      writeFileSync(terraform, [
        "#!/bin/sh",
        `printf '%s' ${shellLiteral(JSON.stringify({
          overlay: ".",
          roots: {},
          tfvars_format: "hcl",
        }))} > ${shellLiteral(targetPath)}`,
        `printf '%s' ${shellLiteral(plan)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      const result = invoke(JSON.stringify(request), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.result.summary.status, "error");
      assert.match(response.result.error.message, /control input changed/);
    });
  });

  await t.test("newly materialized selected plan changes assessment context", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const addedPlan = path.join(
        workspace,
        "envs",
        "tenant",
        "zpa_segment_group",
        "tfplan",
      );
      mkdirSync(path.dirname(addedPlan), { recursive: true });
      writeFileSync(terraform, [
        "#!/bin/sh",
        `printf '%s' 'new plan' > ${shellLiteral(addedPlan)}`,
        `printf '%s' ${shellLiteral(plan)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      const expandedRequest = {
        ...request,
        input: {
          ...(request.input as Record<string, unknown>),
          selectors: ["zpa"],
        },
      };
      const result = invoke(JSON.stringify(expandedRequest), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 1, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.result.summary.status, "error");
      assert.match(response.result.error.message, /context changed/);
      assert.equal(String(result.stdout).includes(addedPlan), false);
    });
  });

  await t.test("bound control decoding preserves legacy BOM behavior", async () => {
    await withAssessmentFixture(plan, ({ request, terraform, workspace }) => {
      const bom = Buffer.from([0xef, 0xbb, 0xbf]);
      const deploymentPath = path.join(workspace, "deployment.json");
      writeFileSync(
        deploymentPath,
        Buffer.concat([bom, readFileSync(deploymentPath)]),
      );
      const sourceCatalogPath = path.join(
        WORKSPACE,
        "catalogs/zscaler-root-catalog.v1.json",
      );
      const catalogPath = path.join(workspace, "catalog.json");
      writeFileSync(
        catalogPath,
        Buffer.concat([bom, readFileSync(sourceCatalogPath)]),
      );
      const changedRequest = {
        ...request,
        context: {
          ...(request.context as Record<string, unknown>),
          root_catalog: catalogPath,
        },
      };
      const result = invoke(JSON.stringify(changedRequest), WORKSPACE, {
        INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
      });
      assert.equal(result.status, 0, String(result.stderr));
      const response = JSON.parse(String(result.stdout));
      assert.equal(response.result.summary.status, "clean");
    });
  });
});

test("assessment process defines selector errors before invalid-policy reports", async () => {
  await withAssessmentFixture(assessmentPlan({
    actions: ["no-op"],
    before: {},
    after: {},
  }), ({ request, terraform, policyPath }) => {
    assert.notEqual(policyPath, null);
    writeFileSync(policyPath as string, "{");
    const invalidRequest = {
      ...request,
      input: {
        ...(request.input as Record<string, unknown>),
        selectors: ["not_a_resource"],
      },
    };
    const result = invoke(JSON.stringify(invalidRequest), WORKSPACE, {
      INFRAWRIGHT_TERRAFORM_EXECUTABLE: terraform,
    });
    assert.equal(result.status, 2, String(result.stderr));
    const response = JSON.parse(String(result.stdout));
    assert.equal(response.status, "error");
    assert.equal(response.result, null);
    assert.equal(response.error.code, "UNKNOWN_RESOURCE_SELECTOR");
  }, { version: 1, resource_types: {} });
});

test("plan_roots rejects unknown selectors before reading deployment", () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "infrawright-process-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    writeFileSync(deployment, "{");
    const result = invoke(JSON.stringify({
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "plan-roots-order",
      operation: "plan_roots",
      context: {
        workspace: WORKSPACE,
        deployment,
        root_catalog: "catalogs/zscaler-root-catalog.v1.json",
      },
      input: {
        tenant: null,
        selectors: ["not_a_resource"],
      },
    }));
    assert.equal(result.status, 2);
    assert.equal(
      JSON.parse(String(result.stdout)).error.code,
      "UNKNOWN_RESOURCE_SELECTOR",
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("scope_paths resolves relative contract paths from context.workspace", () => {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "workspace-scoping",
    operation: "scope_paths",
    context: {
      workspace: WORKSPACE,
      deployment: "missing-deployment.json",
      root_catalog: "catalogs/zscaler-root-catalog.v1.json",
    },
    input: {
      paths: ["config/prod/zpa_application_segment.auto.tfvars.json"],
    },
  };
  const result = invoke(JSON.stringify(request), path.dirname(WORKSPACE));
  assert.equal(result.status, 0, String(result.stderr));
  const response = JSON.parse(String(result.stdout));
  assert.deepEqual(response.result.affected_resources, ["zpa_application_segment"]);
});

test("process host rejects malformed and schema-invalid requests structurally", () => {
  const malformed = invoke("{");
  assert.equal(malformed.status, 2);
  assert.equal(JSON.parse(String(malformed.stdout)).error.code, "INVALID_JSON");
  assert.equal(String(malformed.stderr), "");

  const invalid = invoke(JSON.stringify({
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "invalid",
    operation: "roots",
    context: {},
    input: {},
    surprise: true,
  }));
  assert.equal(invalid.status, 2);
  const response = JSON.parse(String(invalid.stdout));
  assert.equal(response.request_id, "invalid");
  assert.equal(response.operation, "roots");
  assert.equal(response.error.code, "INVALID_REQUEST");
  assert.ok(response.error.details.length > 0);
  assert.equal(String(invalid.stderr), "");

  const duplicate = invoke(
    '{"kind":"infrawright.process_request","schema_version":1,'
    + '"request_id":"duplicate","request_id":"duplicate",'
    + '"operation":"roots","context":{"workspace":"/tmp",'
    + '"deployment":"deployment.json","root_catalog":"catalog.json"},'
    + '"input":{"tenant":null,"selectors":[]}}',
  );
  assert.equal(duplicate.status, 2);
  assert.equal(
    JSON.parse(String(duplicate.stdout)).error.code,
    "INVALID_JSON",
  );
});

test("process host rejects invalid UTF-8 without replacement", () => {
  const result = invoke(Buffer.from([0xff]));
  assert.equal(result.status, 2);
  assert.equal(
    JSON.parse(String(result.stdout)).error.code,
    "INVALID_UTF8",
  );
});

test("response schema forbids success diagnostics on errors", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "mixed",
    operation: "roots",
    status: "error",
    diagnostics: [
      {
        level: "note",
        code: "WHOLE_ROOT_SELECTION",
        message: "not valid on an error",
        selected_members: ["one"],
        root: "group",
        additional_members: ["two"],
      },
    ],
    result: null,
    error: {
      code: "INVALID_REQUEST",
      category: "request",
      message: "bad request",
      retryable: false,
      details: [],
    },
  }), false);
});

test("request schema binds each operation to its input shape", () => {
  const context = {
    workspace: WORKSPACE,
    deployment: "deployment.json",
    root_catalog: "catalog.json",
  };
  const base = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "shape",
    context,
  };
  assert.equal(validateProcessRequest({
    ...base,
    operation: "roots",
    input: { paths: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "plan_roots",
    input: { paths: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "scope_paths",
    input: { tenant: null, selectors: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "assess_saved_plans",
    input: { tenant: null, selectors: [] },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "assess_saved_plans",
    input: {
      mode: "assert-clean",
      tenant: null,
      selectors: [],
      backend_config: null,
      policy: null,
      terraform_executable: "/request-controlled/code",
    },
  }), false);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "assess_saved_plans",
    input: {
      mode: "assert-clean",
      tenant: null,
      selectors: [],
      backend_config: null,
      policy: null,
    },
  }), true);
  assert.equal(validateProcessRequest({
    ...base,
    operation: "assess_saved_plans",
    input: {
      mode: "assert-clean",
      tenant: null,
      selectors: [],
      backend_config: null,
      policy: "not-allowed-for-clean.json",
    },
  }), false);
});

test("response schema binds scope_paths success to an empty diagnostic set", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "scope-diagnostic",
    operation: "scope_paths",
    status: "ok",
    diagnostics: [
      {
        level: "note",
        code: "WHOLE_ROOT_SELECTION",
        message: "not valid for scope_paths",
        selected_members: ["one"],
        root: "group",
        additional_members: ["two"],
      },
    ],
    result: {
      kind: "infrawright.changed_path_scope",
      schema_version: 1,
      paths: [],
      path_matches: [],
      unmatched_paths: [],
      affected_resources: [],
      affected_roots: [],
    },
    error: null,
  }), false);
});

test("response schema binds plan_roots to its result contract", () => {
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "cross-operation",
    operation: "roots",
    status: "ok",
    diagnostics: [],
    result: {
      kind: "infrawright.plan_roots",
      schema_version: 1,
      request: { tenant: null, selectors: [] },
      roots: [],
    },
    error: null,
  }), false);
});
