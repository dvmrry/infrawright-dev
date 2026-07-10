import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, relative } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  cleanupSavedPlanEvidence,
  prepareSavedPlanEvidence,
  recheckSavedPlanEvidence,
  type PrepareSavedPlanEvidenceOptions,
  type SavedPlanEvidence,
} from "../node-src/domain/plan-evidence.js";
import {
  planFingerprintV2,
  type PlanFingerprintInput,
} from "../node-src/domain/plan-fingerprint.js";
import { ReadBudget } from "../node-src/io/bounded-files.js";

const SOURCE_LIMITS = {
  maxFiles: 1_000,
  maxDirectories: 1_000,
  maxDirectoryEntries: 10_000,
  maxDepth: 64,
  maxTotalBytes: 64n * 1024n * 1024n,
  maxFileBytes: 8n * 1024n * 1024n,
};

const PLAN_LIMITS = {
  maxFiles: 16,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 64n * 1024n * 1024n,
  maxFileBytes: 32n * 1024n * 1024n,
};

interface Fixture {
  readonly root: string;
  readonly envDir: string;
  readonly planPath: string;
  readonly fingerprintPath: string;
  readonly snapshotDirectory: string;
  readonly fingerprintInput: PlanFingerprintInput;
}

async function withTemp(
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "node-plan-evidence-"));
  try {
    const envDir = join(root, "envs", "tenant", "zpa_custom");
    const moduleDir = join(root, "modules", "zpa_segment_group");
    const planPath = join(envDir, "tfplan");
    const fingerprintPath = join(envDir, "tfplan.sources");
    const snapshotDirectory = join(root, "snapshots");
    mkdirSync(envDir, { recursive: true });
    mkdirSync(moduleDir, { recursive: true });
    mkdirSync(snapshotDirectory, { mode: 0o700 });
    chmodSync(snapshotDirectory, 0o700);
    writeFileSync(
      join(envDir, "main.tf"),
      [
        'module "zpa_segment_group" {',
        `  source = "${relative(envDir, moduleDir)}"`,
        "  items = var.zpa_segment_group_items",
        "}",
        "",
      ].join("\n"),
    );
    writeFileSync(join(moduleDir, "main.tf"), "# module\n");
    writeFileSync(planPath, "opaque-plan-secret-bytes\n");
    const fingerprintInput: PlanFingerprintInput = {
      envDir,
      memberTypes: ["zpa_segment_group"],
      varFiles: [],
    };
    writeFileSync(
      fingerprintPath,
      `${JSON.stringify(await planFingerprintV2(fingerprintInput))}\n`,
    );
    await callback({
      root,
      envDir,
      planPath,
      fingerprintPath,
      snapshotDirectory,
      fingerprintInput,
    });
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
}

function prepareOptions(fixture: Fixture): PrepareSavedPlanEvidenceOptions {
  return {
    savedPlanPath: fixture.planPath,
    fingerprintPath: fixture.fingerprintPath,
    fingerprintInput: fixture.fingerprintInput,
    snapshotDirectory: fixture.snapshotDirectory,
    fingerprintBudget: new ReadBudget(SOURCE_LIMITS),
    savedPlanBudget: new ReadBudget(PLAN_LIMITS),
  };
}

async function prepare(fixture: Fixture): Promise<SavedPlanEvidence> {
  return prepareSavedPlanEvidence(prepareOptions(fixture));
}

function recheck(evidence: SavedPlanEvidence): Promise<void> {
  return recheckSavedPlanEvidence({
    evidence,
    fingerprintBudget: new ReadBudget(SOURCE_LIMITS),
    savedPlanBudget: new ReadBudget(PLAN_LIMITS),
  });
}

function assertFailure(error: unknown, code: string): boolean {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return true;
}

function sha256(content: Buffer): string {
  return createHash("sha256").update(content).digest("hex");
}

test("prepares, binds, rechecks, and cleans up saved-plan evidence", async () => {
  await withTemp(async (fixture) => {
    const evidence = await prepare(fixture);
    const plan = readFileSync(fixture.planPath);
    assert.equal(evidence.originalPlan.sha256, sha256(plan));
    assert.equal(evidence.originalPlan.size, BigInt(plan.length));
    assert.equal(evidence.snapshot.sha256, evidence.originalPlan.sha256);
    assert.equal(evidence.snapshot.size, evidence.originalPlan.size);
    assert.deepEqual(readFileSync(evidence.snapshot.path), plan);
    assert.equal(existsSync(evidence.snapshot.path), true);

    await recheck(evidence);
    await cleanupSavedPlanEvidence(evidence);
    assert.equal(existsSync(evidence.snapshot.path), true);
    assert.equal(statSync(evidence.snapshot.path).size, 0);
    await cleanupSavedPlanEvidence(evidence);
  });
});

test("missing, malformed, duplicate-key, and extra-key fingerprints fail closed", async (t) => {
  await withTemp(async (fixture) => {
    await t.test("missing", async () => {
      rmSync(fixture.fingerprintPath);
      await assert.rejects(
        prepareSavedPlanEvidence(prepareOptions(fixture)),
        (error: unknown) => assertFailure(error, "READ_FAILED"),
      );
    });
  });

  for (const [name, text, code] of [
    ["malformed", "{not-json", "INVALID_PLAN_SOURCES_JSON"],
    [
      "duplicate-key",
      '{"version":2,"version":2,"sha256":"' + "0".repeat(64) + '"}',
      "INVALID_PLAN_SOURCES_JSON",
    ],
    [
      "extra-key",
      JSON.stringify({ version: 2, sha256: "0".repeat(64), extra: true }),
      "INVALID_PLAN_SOURCES",
    ],
    [
      "uppercase-digest",
      JSON.stringify({ version: 2, sha256: "A".repeat(64) }),
      "INVALID_PLAN_SOURCES",
    ],
  ] as const) {
    await t.test(name, async () => {
      await withTemp(async (fixture) => {
        writeFileSync(fixture.fingerprintPath, text);
        await assert.rejects(
          prepareSavedPlanEvidence(prepareOptions(fixture)),
          (error: unknown) => assertFailure(error, code),
        );
      });
    });
  }
});

test("a source fingerprint that does not match current inputs is stale", async () => {
  await withTemp(async (fixture) => {
    writeFileSync(
      fixture.fingerprintPath,
      JSON.stringify({ version: 2, sha256: "0".repeat(64) }),
    );
    await assert.rejects(
      prepareSavedPlanEvidence(prepareOptions(fixture)),
      (error: unknown) => assertFailure(error, "STALE_PLAN_SOURCES"),
    );
  });
});

test("original saved-plan mutation and replacement invalidate evidence", async (t) => {
  for (const mode of ["mutation", "replacement"] as const) {
    await t.test(mode, async () => {
      await withTemp(async (fixture) => {
        const evidence = await prepare(fixture);
        if (mode === "mutation") {
          writeFileSync(fixture.planPath, "changed plan bytes\n");
        } else {
          const replacement = join(fixture.root, "replacement-plan");
          writeFileSync(replacement, "replacement plan bytes\n");
          renameSync(replacement, fixture.planPath);
        }
        await assert.rejects(
          recheck(evidence),
          (error: unknown) => assertFailure(error, "SAVED_PLAN_CHANGED"),
        );
      });
    });
  }
});

test("fingerprint replacement and current-source changes invalidate evidence", async (t) => {
  await t.test("fingerprint replacement", async () => {
    await withTemp(async (fixture) => {
      const evidence = await prepare(fixture);
      const replacement = join(fixture.root, "replacement.sources");
      writeFileSync(
        replacement,
        JSON.stringify({ version: 2, sha256: "1".repeat(64) }),
      );
      renameSync(replacement, fixture.fingerprintPath);
      await assert.rejects(
        recheck(evidence),
        (error: unknown) => assertFailure(error, "PLAN_SOURCES_CHANGED"),
      );
    });
  });

  await t.test("source change", async () => {
    await withTemp(async (fixture) => {
      const evidence = await prepare(fixture);
      writeFileSync(
        join(fixture.envDir, "main.tf"),
        `${readFileSync(join(fixture.envDir, "main.tf"), "utf8")}# changed source\n`,
      );
      await assert.rejects(
        recheck(evidence),
        (error: unknown) => assertFailure(error, "STALE_PLAN_SOURCES"),
      );
    });
  });
});

test("snapshot mutation invalidates its independent binding", async () => {
  await withTemp(async (fixture) => {
    const evidence = await prepare(fixture);
    writeFileSync(evidence.snapshot.path, "changed snapshot bytes\n");
    await assert.rejects(
      recheck(evidence),
      (error: unknown) => assertFailure(error, "PLAN_SNAPSHOT_CHANGED"),
    );
  });
});

test("cleanup cannot be redirected outside the bound snapshot directory", async () => {
  await withTemp(async (fixture) => {
    const evidence = await prepare(fixture);
    const victim = join(fixture.root, "must-not-delete");
    writeFileSync(victim, "keep\n");
    await assert.rejects(
      cleanupSavedPlanEvidence({
        ...evidence,
        snapshot: { ...evidence.snapshot, path: victim },
      }),
      (error: unknown) => assertFailure(error, "INVALID_SNAPSHOT_BINDING"),
    );
    assert.equal(existsSync(victim), true);
    await cleanupSavedPlanEvidence(evidence);
  });
});

test("cleanup refuses a renamed-directory symlink swap without deleting a victim", async () => {
  await withTemp(async (fixture) => {
    const evidence = await prepare(fixture);
    const movedDirectory = `${fixture.snapshotDirectory}-moved`;
    const victimDirectory = join(fixture.root, "victim-directory");
    const victim = join(victimDirectory, basename(evidence.snapshot.path));
    mkdirSync(victimDirectory);
    writeFileSync(victim, "must survive\n");
    renameSync(fixture.snapshotDirectory, movedDirectory);
    symlinkSync(victimDirectory, fixture.snapshotDirectory, "dir");

    await assert.rejects(
      cleanupSavedPlanEvidence(evidence),
      (error: unknown) => assertFailure(error, "SNAPSHOT_CLEANUP_REFUSED"),
    );
    assert.equal(existsSync(victim), true);

    rmSync(fixture.snapshotDirectory);
    renameSync(movedDirectory, fixture.snapshotDirectory);
    await cleanupSavedPlanEvidence(evidence);
  });
});

test("cleanup refuses snapshot replacement and preserves both files", async () => {
  await withTemp(async (fixture) => {
    const evidence = await prepare(fixture);
    const original = `${evidence.snapshot.path}.original`;
    renameSync(evidence.snapshot.path, original);
    writeFileSync(evidence.snapshot.path, "replacement must survive\n");
    await assert.rejects(
      cleanupSavedPlanEvidence(evidence),
      (error: unknown) => assertFailure(error, "SNAPSHOT_CLEANUP_REFUSED"),
    );
    assert.equal(existsSync(original), true);
    assert.equal(existsSync(evidence.snapshot.path), true);

    rmSync(evidence.snapshot.path);
    renameSync(original, evidence.snapshot.path);
    await cleanupSavedPlanEvidence(evidence);
  });
});

test("diagnostics never disclose planted paths or file content", async () => {
  await withTemp(async (fixture) => {
    const plantedSecret = "super-secret-plan-token-7f6b";
    const plantedPath = join(fixture.root, "secret-tenant-name");
    mkdirSync(dirname(plantedPath), { recursive: true });
    writeFileSync(
      fixture.fingerprintPath,
      `{\"version\":2,\"sha256\":\"${plantedSecret}\",\"path\":\"${plantedPath}\"}`,
    );
    let failure: unknown;
    try {
      await prepareSavedPlanEvidence(prepareOptions(fixture));
    } catch (error: unknown) {
      failure = error;
    }
    assert.ok(failure instanceof ProcessFailure);
    const diagnostic = JSON.stringify({
      code: failure.code,
      details: failure.details,
      message: failure.message,
    });
    assert.equal(diagnostic.includes(plantedSecret), false);
    assert.equal(diagnostic.includes(plantedPath), false);
    assert.equal(diagnostic.includes(fixture.root), false);

    writeFileSync(
      fixture.fingerprintPath,
      `${JSON.stringify(await planFingerprintV2(fixture.fingerprintInput))}\n`,
    );
    failure = undefined;
    try {
      await prepareSavedPlanEvidence({
        ...prepareOptions(fixture),
        fingerprintInput: {
          ...fixture.fingerprintInput,
          memberTypes: [`zpa_${plantedSecret}`],
        },
      });
    } catch (error: unknown) {
      failure = error;
    }
    assert.ok(failure instanceof ProcessFailure);
    assert.equal(failure.code, "SOURCE_FINGERPRINT_FAILED");
    const sourceDiagnostic = JSON.stringify({
      code: failure.code,
      details: failure.details,
      message: failure.message,
    });
    assert.equal(sourceDiagnostic.includes(plantedSecret), false);
    assert.equal(sourceDiagnostic.includes(fixture.root), false);
  });
});

test("all evidence paths must already be absolute", async () => {
  await withTemp(async (fixture) => {
    await assert.rejects(
      prepareSavedPlanEvidence({
        ...prepareOptions(fixture),
        fingerprintInput: {
          ...fixture.fingerprintInput,
          envDir: "relative/env",
        },
      }),
      (error: unknown) => assertFailure(error, "UNRESOLVED_EVIDENCE_PATH"),
    );
  });
});
