import assert from "node:assert/strict";
import {
  mkdtempSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  loadBoundDriftPolicy,
  recheckBoundDriftPolicy,
} from "../node-src/domain/plan-policy.js";
import { ReadBudget } from "../node-src/io/bounded-files.js";

const LIMITS = {
  maxFiles: 4,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 1024n * 1024n,
  maxFileBytes: 1024n * 1024n,
};

function budget(): ReadBudget {
  return new ReadBudget(LIMITS);
}

function failure(error: unknown, code: string): boolean {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return true;
}

test("drift policy is parsed from and later bound to exact stable bytes", async () => {
  const root = mkdtempSync(join(tmpdir(), "node-plan-policy-"));
  try {
    const policyPath = join(root, "policy.json");
    writeFileSync(policyPath, JSON.stringify({
      version: 1,
      resource_types: {
        zpa_sample: {
          plan_tolerate: [{
            path: "status",
            reason: "provider read normalization",
            approved_by: "owner",
          }],
        },
      },
    }));
    const bound = await loadBoundDriftPolicy(policyPath, budget());
    assert.equal(bound.path, policyPath);
    assert.match(bound.file?.sha256 ?? "", /^[0-9a-f]{64}$/);
    assert.equal(
      bound.policy.toleratesPlanPath("zpa_sample", ["status"], "update"),
      true,
    );
    await recheckBoundDriftPolicy(bound, budget());

    const replacement = join(root, "replacement");
    writeFileSync(replacement, JSON.stringify({ version: 1, resource_types: {} }));
    renameSync(replacement, policyPath);
    await assert.rejects(
      recheckBoundDriftPolicy(bound, budget()),
      (error: unknown) => failure(error, "DRIFT_POLICY_CHANGED"),
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("invalid, secret-bearing, relative, and symlinked policies fail safely", async () => {
  const root = mkdtempSync(join(tmpdir(), "node-plan-policy-"));
  try {
    const secret = "policy-secret-c329";
    const policyPath = join(root, "policy.json");
    writeFileSync(policyPath, `{\"version\":1,\"${secret}\":true}`);
    let invalid: unknown;
    try {
      await loadBoundDriftPolicy(policyPath, budget());
    } catch (error: unknown) {
      invalid = error;
    }
    assert.ok(invalid instanceof ProcessFailure);
    assert.equal(invalid.code, "INVALID_DRIFT_POLICY");
    assert.equal(invalid.message.includes(secret), false);
    assert.equal(invalid.message.includes(root), false);

    await assert.rejects(
      loadBoundDriftPolicy("relative/policy.json", budget()),
      (error: unknown) => failure(error, "UNRESOLVED_POLICY_PATH"),
    );
    const link = join(root, "policy-link");
    symlinkSync(policyPath, link);
    await assert.rejects(
      loadBoundDriftPolicy(link, budget()),
      (error: unknown) => failure(error, "SYMLINK_NOT_ALLOWED"),
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("an absent policy has no mutable file evidence", async () => {
  const bound = await loadBoundDriftPolicy(null, budget());
  assert.equal(bound.path, null);
  assert.equal(bound.file, null);
  assert.deepEqual(bound.policy.staleEntries(), []);
  await recheckBoundDriftPolicy(bound, budget());
});
