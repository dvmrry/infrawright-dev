import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  assessSavedPlans,
  assessSavedPlansReport,
  MAX_SAVED_PLAN_ASSESSMENT_ROOTS,
  type SavedPlanAssessmentOptions,
  type SavedPlanAssessmentRootInput,
} from "../node-src/domain/plan-assessment.js";
import { planFingerprintV2 } from "../node-src/domain/plan-fingerprint.js";

interface Fixture {
  readonly root: string;
  readonly envDir: string;
  readonly planPath: string;
  readonly fingerprintPath: string;
  readonly assessmentRoot: SavedPlanAssessmentRootInput;
}

async function withFixture(
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "node-plan-assessment-"));
  try {
    const envDir = join(root, "envs", "tenant", "zpa_sample");
    const moduleDir = join(root, "modules", "zpa_sample");
    const planPath = join(envDir, "tfplan");
    const fingerprintPath = join(envDir, "tfplan.sources");
    mkdirSync(envDir, { recursive: true });
    mkdirSync(moduleDir, { recursive: true });
    writeFileSync(join(moduleDir, "main.tf"), "# module\n");
    writeFileSync(join(envDir, "main.tf"), [
      'module "zpa_sample" {',
      `  source = "${relative(envDir, moduleDir)}"`,
      "  items = var.zpa_sample_items",
      "}",
      "",
    ].join("\n"));
    writeFileSync(planPath, "opaque saved plan bytes\n", { mode: 0o600 });
    const assessmentRoot: SavedPlanAssessmentRootInput = {
      tenant: "tenant",
      label: "zpa_sample",
      members: ["zpa_sample"],
      envDir,
      savedPlanPath: planPath,
      fingerprintPath,
      varFiles: [],
    };
    writeFileSync(
      fingerprintPath,
      `${JSON.stringify(await planFingerprintV2({
        envDir,
        varFiles: [],
        memberTypes: assessmentRoot.members,
        backendConfig: null,
        backendKey: null,
      }))}\n`,
    );
    await callback({ root, envDir, planPath, fingerprintPath, assessmentRoot });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

function executable(root: string, body: string): string {
  const file = join(root, "terraform-fake");
  writeFileSync(file, `#!/bin/sh\n${body}\n`, { mode: 0o700 });
  chmodSync(file, 0o700);
  return file;
}

function plan(change: object): string {
  return JSON.stringify({
    format_version: "1.2",
    terraform_version: "1.15.4",
    complete: true,
    errored: false,
    resource_changes: [{
      address: "zpa_sample.this[\"one\"]",
      type: "zpa_sample",
      change,
    }],
    output_changes: {},
  });
}

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

function options(
  fixture: Fixture,
  terraformExecutable: string,
  policyPath: string | null = null,
): SavedPlanAssessmentOptions {
  return {
    terraformExecutable,
    roots: [fixture.assessmentRoot],
    backendConfig: null,
    policyPath,
  };
}

function failure(error: unknown, code: string): boolean {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return true;
}

test("one transaction assesses clean plan metadata without returning plan values", async () => {
  await withFixture(async (fixture) => {
    const marker = join(fixture.root, "snapshot-path");
    const planJson = plan({
      actions: ["no-op"],
      before: { credential: "raw-secret-24e1" },
      after: { credential: "raw-secret-24e1" },
    });
    const fake = executable(fixture.root, [
      `printf '%s' "$4" > ${shellLiteral(marker)}`,
      `printf '%s' ${shellLiteral(planJson)}`,
    ].join("\n"));
    const result = await assessSavedPlans(options(fixture, fake));
    assert.deepEqual(
      {
        status: result.status,
        checked: result.checked,
        clean: result.clean,
        tolerated: result.tolerated,
        blocked: result.blocked,
      },
      { status: "clean", checked: 1, clean: 1, tolerated: 0, blocked: 0 },
    );
    assert.equal(result.roots[0]?.plan.format_version, "1.2");
    assert.equal(result.roots[0]?.plan.terraform_version, "1.15.4");
    assert.match(result.roots[0]?.plan.sha256 ?? "", /^[0-9a-f]{64}$/);
    assert.deepEqual(result.roots[0]?.findings, []);
    assert.equal(JSON.stringify(result).includes("raw-secret-24e1"), false);
    const snapshotPath = readFileSync(marker, "utf8");
    assert.equal(existsSync(snapshotPath), false);
    assert.equal(existsSync(dirname(snapshotPath)), false);
  });
});

test("policy bytes drive tolerated classification and stale-entry reporting", async () => {
  await withFixture(async (fixture) => {
    const policyPath = join(fixture.root, "policy.json");
    writeFileSync(policyPath, JSON.stringify({
      version: 1,
      resource_types: {
        zpa_sample: {
          plan_tolerate: [
            { path: "status", reason: "known", approved_by: "owner" },
            { path: "unused", reason: "stale", approved_by: "owner" },
          ],
        },
      },
    }));
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(plan({
      actions: ["update"],
      before: { status: "old" },
      after: { status: "new" },
    }))}`);
    const result = await assessSavedPlans(options(fixture, fake, policyPath));
    assert.equal(result.status, "clean_with_tolerated_drift");
    assert.equal(result.tolerated, 1);
    assert.match(result.policy_sha256 ?? "", /^[0-9a-f]{64}$/);
    assert.deepEqual(result.roots[0]?.findings, [{
      status: "clean_with_tolerated_drift",
      source: "resource_changes",
      address: 'zpa_sample.this["one"]',
      actions: ["update"],
      paths: [["status"]],
      resource_type: "zpa_sample",
    }]);
    assert.deepEqual(result.stale_policy, [{
      resource_type: "zpa_sample",
      mode: "plan_tolerate",
      path: "unused",
    }]);
  });
});

test("plan and policy replacement during Terraform show invalidate the transaction", async (t) => {
  await t.test("plan mutation", async () => {
    await withFixture(async (fixture) => {
      const fake = executable(fixture.root, [
        `printf '%s' changed > ${shellLiteral(fixture.planPath)}`,
        `printf '%s' ${shellLiteral(plan({ actions: ["no-op"], before: {}, after: {} }))}`,
      ].join("\n"));
      await assert.rejects(
        assessSavedPlans(options(fixture, fake)),
        (error: unknown) => failure(error, "SAVED_PLAN_CHANGED"),
      );
    });
  });

  await t.test("policy mutation", async () => {
    await withFixture(async (fixture) => {
      const policyPath = join(fixture.root, "policy.json");
      writeFileSync(policyPath, JSON.stringify({ version: 1, resource_types: {} }));
      const fake = executable(fixture.root, [
        `printf '%s' '{"version":1,"resource_types":{"changed":{}}}' > ${shellLiteral(policyPath)}`,
        `printf '%s' ${shellLiteral(plan({ actions: ["no-op"], before: {}, after: {} }))}`,
      ].join("\n"));
      await assert.rejects(
        assessSavedPlans(options(fixture, fake, policyPath)),
        (error: unknown) => failure(error, "DRIFT_POLICY_CHANGED"),
      );
    });
  });
});

test("invalid selection and secret-bearing Terraform output fail without disclosure", async () => {
  await assert.rejects(
    assessSavedPlans({
      terraformExecutable: "/missing/terraform",
      roots: [],
      backendConfig: null,
      policyPath: null,
    }),
    (error: unknown) => failure(error, "NO_SAVED_PLANS"),
  );

  await withFixture(async (fixture) => {
    const secret = "terraform-json-secret-0b72";
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(secret)}`);
    let invalid: unknown;
    try {
      await assessSavedPlans(options(fixture, fake));
    } catch (error: unknown) {
      invalid = error;
    }
    assert.ok(invalid instanceof ProcessFailure);
    assert.equal(invalid.code, "INVALID_TERRAFORM_SHOW_JSON");
    assert.equal(invalid.message.includes(secret), false);
    assert.equal(invalid.message.includes(fixture.root), false);
  });
});

test("assessment snapshots caller-owned arrays and limits before yielding", async () => {
  await withFixture(async (fixture) => {
    const members = ["zpa_sample"];
    const limits = {
      timeoutMs: 120_000,
      maxStdoutBytes: 8 * 1024 * 1024,
      maxStderrBytes: 1024 * 1024,
    };
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(plan({
      actions: ["no-op"],
      before: {},
      after: {},
    }))}`);
    const pending = assessSavedPlans({
      ...options(fixture, fake),
      roots: [{ ...fixture.assessmentRoot, members }],
      terraformShowLimits: limits,
    });
    members[0] = "mutated_after_call";
    limits.maxStdoutBytes = 1;
    const result = await pending;
    assert.deepEqual(result.roots[0]?.members, ["zpa_sample"]);
  });
});

test("assessment snapshots caller-owned materialization context before yielding", async () => {
  await withFixture(async (fixture) => {
    const varFile = join(
      fixture.root,
      "config",
      "tenant",
      "zpa_sample.auto.tfvars.json",
    );
    mkdirSync(dirname(varFile), { recursive: true });
    writeFileSync(varFile, "{}\n");
    const assessmentRoot = {
      ...fixture.assessmentRoot,
      varFiles: [varFile],
    };
    writeFileSync(
      fixture.fingerprintPath,
      `${JSON.stringify(await planFingerprintV2({
        envDir: fixture.envDir,
        varFiles: [varFile],
        memberTypes: assessmentRoot.members,
        backendConfig: null,
        backendKey: null,
      }))}\n`,
    );
    const context = {
      workspace: fixture.root,
      deployment: { overlay: ".", roots: {} },
      catalog: {
        kind: "infrawright.root_catalog" as const,
        schema_version: 1 as const,
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
        source_files: [] as string[],
        sources_sha256: "0".repeat(64),
      },
      tenant: "tenant",
      selectors: [] as string[],
    };
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(plan({
      actions: ["no-op"],
      before: {},
      after: {},
    }))}`);
    const pending = assessSavedPlans({
      ...options(fixture, fake),
      roots: [assessmentRoot],
      context,
    });
    context.deployment.overlay = "mutated";
    context.catalog.resources[0]!.type = "zpa_mutated";
    context.selectors.push("zpa/mutated");
    const result = await pending;
    assert.equal(result.status, "clean");
    assert.deepEqual(result.roots[0]?.members, ["zpa_sample"]);
  });
});

test("assessment rejects an excessive root set before filesystem work", async () => {
  const roots = Array.from(
    { length: MAX_SAVED_PLAN_ASSESSMENT_ROOTS + 1 },
    (_, index): SavedPlanAssessmentRootInput => ({
      tenant: "tenant",
      label: `root_${index}`,
      members: ["zpa_sample"],
      envDir: "/missing/env",
      savedPlanPath: "/missing/tfplan",
      fingerprintPath: "/missing/tfplan.sources",
      varFiles: [],
    }),
  );
  await assert.rejects(
    assessSavedPlans({
      terraformExecutable: "/missing/terraform",
      roots,
      backendConfig: null,
      policyPath: null,
    }),
    (error: unknown) => failure(error, "TOO_MANY_SAVED_PLANS"),
  );
});

test("invalid read limits fail before policy phase classification", async () => {
  const outcome = await assessSavedPlansReport({
    assessment: {
      terraformExecutable: "/missing/terraform",
      roots: [],
      backendConfig: null,
      policyPath: null,
      policyLimits: {
        maxFiles: 0,
        maxDirectories: 1,
        maxDirectoryEntries: 1,
        maxDepth: 0,
        maxTotalBytes: 1n,
        maxFileBytes: 1n,
      },
    },
    mode: "assert-clean",
    request: { tenant: "tenant", selectors: [], policy: null },
  });
  assert.equal(outcome.failure?.code, "INVALID_ASSESSMENT_LIMIT");
  assert.equal(outcome.report.error?.kind, "assessment_error");
});

test("retained snapshot cap is enforced before copying an oversized plan", async () => {
  await withFixture(async (fixture) => {
    const fake = executable(fixture.root, "exit 99");
    await assert.rejects(
      assessSavedPlans({
        ...options(fixture, fake),
        maxRetainedSnapshotBytes: 1n,
      }),
      (error: unknown) => failure(error, "FILE_LIMIT_EXCEEDED"),
    );
  });
});

test("aggregate report metadata limits fail closed inside the evidence transaction", async () => {
  await withFixture(async (fixture) => {
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(plan({
      actions: ["update"],
      before: { status: "old" },
      after: { status: "new" },
    }))}`);
    const outcome = await assessSavedPlansReport({
      assessment: {
        ...options(fixture, fake),
        resultLimits: {
          maxFindings: 1,
          maxPaths: 1,
          maxMetadataBytes: 1,
        },
      },
      mode: "assert-clean",
      request: { tenant: "tenant", selectors: [], policy: null },
    });
    assert.equal(outcome.failure?.code, "ASSESSMENT_RESULT_LIMIT_EXCEEDED");
    assert.equal(outcome.report.error?.kind, "assessment_error");
    assert.deepEqual(outcome.report.roots, []);
  });
});

test("report outcome retains completed roots when a later root fails", async () => {
  await withFixture(async (fixture) => {
    const marker = join(fixture.root, "show-count");
    const clean = plan({ actions: ["no-op"], before: {}, after: {} });
    const fake = executable(fixture.root, [
      `if [ -f ${shellLiteral(marker)} ]; then`,
      "  printf '%s' invalid-json",
      "else",
      `  : > ${shellLiteral(marker)}`,
      `  printf '%s' ${shellLiteral(clean)}`,
      "fi",
    ].join("\n"));
    const first = fixture.assessmentRoot;
    const second = { ...first, label: "zpa_second" };
    const outcome = await assessSavedPlansReport({
      assessment: {
        ...options(fixture, fake),
        roots: [second, first],
      },
      mode: "assert-clean",
      request: { tenant: "tenant", selectors: [], policy: null },
    });
    assert.ok(outcome.failure !== null);
    assert.equal(outcome.failure.code, "INVALID_TERRAFORM_SHOW_JSON");
    assert.equal(outcome.report.summary.status, "error");
    assert.deepEqual(outcome.report.summary, {
      status: "error",
      checked: 1,
      clean: 1,
      tolerated: 0,
      blocked: 0,
    });
    assert.deepEqual(outcome.report.roots.map((root) => root.label), ["zpa_sample"]);
    assert.deepEqual(outcome.report.error, {
      kind: "assessment_error",
      message: "Expecting value: line 1 column 1 (char 0)",
    });
  });
});

test("zero-root reports preserve policy-error precedence and invalid-policy hash", async () => {
  const root = mkdtempSync(join(tmpdir(), "node-plan-assessment-policy-"));
  try {
    const policyPath = join(root, "policy.json");
    const bytes = Buffer.from("{\"version\":1,\"resource_types\":", "utf8");
    writeFileSync(policyPath, bytes);
    const invalid = await assessSavedPlansReport({
      assessment: {
        terraformExecutable: "/missing/terraform",
        roots: [],
        backendConfig: null,
        policyPath,
      },
      mode: "assert-adoptable",
      request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    });
    assert.equal(invalid.failure?.code, "INVALID_DRIFT_POLICY");
    assert.equal(invalid.report.error?.kind, "policy_error");
    assert.equal(
      invalid.report.request.policy_sha256,
      createHash("sha256").update(bytes).digest("hex"),
    );
    assert.deepEqual(invalid.report.roots, []);

    const invalidUtf8 = Buffer.from([0xff, 0xfe, 0xfd]);
    writeFileSync(policyPath, invalidUtf8);
    const undecodable = await assessSavedPlansReport({
      assessment: {
        terraformExecutable: "/missing/terraform",
        roots: [],
        backendConfig: null,
        policyPath,
      },
      mode: "assert-adoptable",
      request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    });
    assert.equal(undecodable.failure?.code, "INVALID_DRIFT_POLICY");
    assert.equal(undecodable.report.error?.kind, "policy_error");
    assert.equal(
      undecodable.report.request.policy_sha256,
      createHash("sha256").update(invalidUtf8).digest("hex"),
    );

    const absent = await assessSavedPlansReport({
      assessment: {
        terraformExecutable: "/missing/terraform",
        roots: [],
        backendConfig: null,
        policyPath: null,
      },
      mode: "assert-clean",
      request: { tenant: "tenant", selectors: [], policy: null },
    });
    assert.equal(absent.failure?.code, "NO_SAVED_PLANS");
    assert.equal(absent.report.error?.kind, "no_saved_plans");
    assert.deepEqual(absent.report.summary, {
      status: "error",
      checked: 0,
      clean: 0,
      tolerated: 0,
      blocked: 0,
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("report wrapper snapshots mode and request metadata before yielding", async () => {
  await withFixture(async (fixture) => {
    const fake = executable(fixture.root, `printf '%s' ${shellLiteral(plan({
      actions: ["no-op"],
      before: {},
      after: {},
    }))}`);
    const request = { tenant: "tenant", selectors: [] as string[], policy: null };
    const wrapper: {
      assessment: SavedPlanAssessmentOptions;
      mode: "assert-clean" | "assert-adoptable";
      request: typeof request;
    } = {
      assessment: options(fixture, fake),
      mode: "assert-clean",
      request,
    };
    const pending = assessSavedPlansReport(wrapper);
    wrapper.mode = "assert-adoptable";
    request.tenant = "mutated";
    request.selectors.push("mutated");
    const outcome = await pending;
    assert.equal(outcome.failure, null);
    assert.equal(outcome.report.mode, "assert-clean");
    assert.deepEqual(outcome.report.request, {
      tenant: "tenant",
      selectors: [],
      policy: null,
      policy_sha256: null,
    });
  });
});
