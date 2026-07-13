import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullArtifactParity,
} from "../node-src/contracts/validators.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  ComparePullArtifactsProcessRequest,
  ComparePullArtifactsProcessSuccessResponse,
  ProcessResponse,
} from "../node-src/process/types.js";

const WORKSPACE = process.cwd();
const PROCESS_MAIN = path.join(
  WORKSPACE,
  ".node-test/node-src/process/main.js",
);
const ROOT_CATALOG = path.join(
  WORKSPACE,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "parity_process";
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

interface Fixture {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly pullPath: (resourceType: ZccPullResourceType) => string;
}

function rawPull(resourceType: ZccPullResourceType): string {
  if (resourceType === "zcc_device_cleanup") {
    return '[{"id":"device-1","active":"1"}]\n';
  }
  return readFileSync(
    path.join(WORKSPACE, `tests/fixtures/demo/${resourceType}.json`),
    "utf8",
  );
}

async function withFixture(
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "zcc-parity-process-"));
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  try {
    mkdirSync(pullDirectory, { recursive: true });
    writeFileSync(deploymentPath, '{"overlay":".","roots":{}}\n');
    copyFileSync(ROOT_CATALOG, catalogPath);
    for (const resourceType of RESOURCES) {
      writeFileSync(
        path.join(pullDirectory, `${resourceType}.json`),
        rawPull(resourceType),
      );
    }
    await callback({
      workspace,
      deploymentPath,
      catalogPath,
      pullPath: (resourceType) => {
        return path.join(pullDirectory, `${resourceType}.json`);
      },
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

function materializeWithPython(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): void {
  const pythonPath = process.env.PYTHONPATH;
  const run = spawnSync(
    PYTHON_ORACLE,
    [
      "-m",
      "engine.transform",
      resourceType,
      fixture.pullPath(resourceType),
      TENANT,
    ],
    {
      cwd: fixture.workspace,
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
      env: {
        ...process.env,
        INFRAWRIGHT_DEPLOYMENT: fixture.deploymentPath,
        PYTHONPATH: pythonPath === undefined || pythonPath.length === 0
          ? WORKSPACE
          : `${WORKSPACE}${path.delimiter}${pythonPath}`,
      },
    },
  );
  assert.equal(run.signal, null, run.error?.message);
  assert.equal(run.status, 0, run.stderr);
}

function request(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): ComparePullArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `compare-${resourceType}`,
    operation: "compare_pull_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      reference: "materialized",
      tenant: TENANT,
      resource_type: resourceType,
    },
  };
}

function invoke(compareRequest: ComparePullArtifactsProcessRequest): {
  readonly status: number | null;
  readonly stderr: string;
  readonly stdout: string;
  readonly response: ProcessResponse;
} {
  const run = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: JSON.stringify(compareRequest),
    maxBuffer: 32 * 1024 * 1024,
    env: { ...process.env, INFRAWRIGHT_TERRAFORM_EXECUTABLE: "" },
  });
  assert.equal(run.signal, null, run.error?.message);
  assert.doesNotThrow(() => JSON.parse(run.stdout), run.stderr);
  return {
    status: run.status,
    stderr: run.stderr,
    stdout: run.stdout,
    response: JSON.parse(run.stdout) as ProcessResponse,
  };
}

function requireSuccess(
  response: ProcessResponse,
): asserts response is ComparePullArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "compare_pull_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

test("public comparer proves actual Python writer bytes for all five ZCC resources", async () => {
  await withFixture((fixture) => {
    for (const resourceType of RESOURCES) {
      materializeWithPython(fixture, resourceType);
      const compareRequest = request(fixture, resourceType);
      assert.equal(validateProcessRequest(compareRequest), true, resourceType);
      const invocation = invoke(compareRequest);
      assert.equal(invocation.status, 0, invocation.stdout);
      assert.equal(invocation.stderr, "", resourceType);
      assert.equal(
        validateProcessResponse(invocation.response),
        true,
        JSON.stringify(validateProcessResponse.errors),
      );
      requireSuccess(invocation.response);
      assert.equal(
        validateZccPullArtifactParity(invocation.response.result),
        true,
        JSON.stringify(validateZccPullArtifactParity.errors),
      );
      assert.equal(invocation.response.result.status, "ready", resourceType);
      assert.equal(invocation.response.result.parity.status, "equal", resourceType);
      assert.equal(invocation.response.result.parity.mismatched, 0, resourceType);
      assert.equal(invocation.response.result.parity.missing, 0, resourceType);
      assert.equal(invocation.stdout.includes("content"), false, resourceType);
    }
  });
});

test("public comparer returns exit 3 for a secret-safe materialized mismatch", async () => {
  await withFixture((fixture) => {
    const resourceType = "zcc_failopen_policy";
    materializeWithPython(fixture, resourceType);
    const tfvars = path.join(
      fixture.workspace,
      "config",
      TENANT,
      `${resourceType}.auto.tfvars.json`,
    );
    writeFileSync(tfvars, "parity-process-secret\n");
    const invocation = invoke(request(fixture, resourceType));
    assert.equal(invocation.status, 3, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(validateProcessResponse(invocation.response), true);
    requireSuccess(invocation.response);
    assert.equal(invocation.response.result.status, "review_required");
    assert.equal(invocation.response.result.parity.status, "different");
    assert.equal(invocation.response.result.parity.artifacts.tfvars.status, "mismatch");
    assert.equal(invocation.stdout.includes("parity-process-secret"), false);
    assert.equal(invocation.stdout.includes("content"), false);
  });
});
