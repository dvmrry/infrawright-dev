import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullArtifactMaterialization,
} from "../node-src/contracts/validators.js";
import type {
  ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import type { ZccPullArtifactParity } from "../node-src/domain/zcc-pull-parity.js";
import type {
  ComparePullArtifactsProcessRequest,
  ComparePullArtifactsProcessSuccessResponse,
  MaterializePullArtifactsProcessRequest,
  MaterializePullArtifactsProcessSuccessResponse,
  ProcessErrorResponse,
  ProcessRequest,
  ProcessResponse,
} from "../node-src/process/types.js";

const REPOSITORY = process.cwd();
const PROCESS_MAIN = path.join(
  REPOSITORY,
  ".node-test/node-src/process/main.js",
);
const ROOT_CATALOG = path.join(
  REPOSITORY,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "materialize_process";
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

interface Fixture {
  readonly workspace: string;
  readonly overlay: string;
  readonly outputRoot: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly pullPath: (resourceType: ZccPullResourceType) => string;
}

interface Invocation {
  readonly status: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly response: ProcessResponse;
}

function rawPull(resourceType: ZccPullResourceType): string {
  if (resourceType === "zcc_device_cleanup") {
    return '[{"id":"device-1","active":"1"}]\n';
  }
  return readFileSync(
    path.join(REPOSITORY, `tests/fixtures/demo/${resourceType}.json`),
    "utf8",
  );
}

function createFixture(prefix: string, overlay = "artifacts"): Fixture {
  const workspace = mkdtempSync(path.join(os.tmpdir(), prefix));
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  const outputRootCandidate = overlay === "."
    ? workspace
    : path.isAbsolute(overlay)
      ? overlay
      : path.join(workspace, overlay);
  mkdirSync(pullDirectory, { recursive: true });
  mkdirSync(outputRootCandidate, { recursive: true });
  const outputRoot = realpathSync(outputRootCandidate);
  writeFileSync(
    deploymentPath,
    `${JSON.stringify({ overlay, roots: {} })}\n`,
  );
  copyFileSync(ROOT_CATALOG, catalogPath);
  for (const resourceType of RESOURCES) {
    writeFileSync(
      path.join(pullDirectory, `${resourceType}.json`),
      rawPull(resourceType),
    );
  }
  return {
    workspace,
    overlay,
    outputRoot,
    deploymentPath,
    catalogPath,
    pullPath: (resourceType) => {
      return path.join(pullDirectory, `${resourceType}.json`);
    },
  };
}

async function withFixturePair(
  callback: (oracle: Fixture, target: Fixture) => void | Promise<void>,
): Promise<void> {
  const oracle = createFixture("zcc-materialize-oracle-");
  const target = createFixture("zcc-materialize-target-");
  try {
    await callback(oracle, target);
  } finally {
    rmSync(oracle.workspace, { recursive: true, force: true });
    rmSync(target.workspace, { recursive: true, force: true });
  }
}

function pythonMaterialize(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): void {
  const pythonPath = process.env.PYTHONPATH;
  const run = spawnSync(
    "python3",
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
          ? REPOSITORY
          : `${REPOSITORY}${path.delimiter}${pythonPath}`,
      },
    },
  );
  assert.equal(run.signal, null, run.error?.message);
  assert.equal(run.status, 0, run.stderr);
}

function compareRequest(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): ComparePullArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `compare-materialize-${resourceType}`,
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

function materializeRequest(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
  assertion: ZccPullArtifactParity,
): MaterializePullArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `materialize-${resourceType}`,
    operation: "materialize_pull_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      publication: "create_or_verify_exact",
      tenant: TENANT,
      resource_type: resourceType,
      assertion,
    },
  };
}

function invoke(
  request: ProcessRequest,
  outputRoot?: string,
): Invocation {
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    INFRAWRIGHT_TERRAFORM_EXECUTABLE: "",
  };
  if (outputRoot === undefined) {
    delete env.INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT;
  } else {
    env.INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT = outputRoot;
  }
  const run = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: REPOSITORY,
    encoding: "utf8",
    input: JSON.stringify(request),
    maxBuffer: 32 * 1024 * 1024,
    env,
  });
  assert.equal(run.signal, null, run.error?.message);
  assert.doesNotThrow(() => JSON.parse(run.stdout), run.stderr);
  return {
    status: run.status,
    stdout: run.stdout,
    stderr: run.stderr,
    response: JSON.parse(run.stdout) as ProcessResponse,
  };
}

function requireCompareSuccess(
  response: ProcessResponse,
): asserts response is ComparePullArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "compare_pull_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireMaterializeSuccess(
  response: ProcessResponse,
): asserts response is MaterializePullArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "materialize_pull_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireMaterializeError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "materialize_pull_artifacts");
  assert.equal(response.status, "error");
  assert.equal(response.result, null);
  assert.notEqual(response.error, null);
}

function artifactPaths(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): Readonly<Record<"imports" | "tfvars" | "lookup", string | null>> {
  return {
    imports: path.join(
      fixture.outputRoot,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    ),
    tfvars: path.join(
      fixture.outputRoot,
      "config",
      TENANT,
      `${resourceType}.auto.tfvars.json`,
    ),
    lookup: resourceType === "zcc_trusted_network"
      ? path.join(
          fixture.outputRoot,
          "config",
          TENANT,
          `${resourceType}.lookup.json`,
        )
      : null,
  };
}

function applicableNames(
  resourceType: ZccPullResourceType,
): readonly ("imports" | "lookup" | "tfvars")[] {
  return resourceType === "zcc_trusted_network"
    ? ["imports", "lookup", "tfvars"]
    : ["imports", "tfvars"];
}

function temporaryAliases(root: string): readonly string[] {
  const found: string[] = [];
  const visit = (directory: string): void => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const entryPath = path.join(directory, entry.name);
      if (entry.name.startsWith(".infrawright-") && entry.name.endsWith(".tmp")) {
        found.push(entryPath);
      }
      if (entry.isDirectory() && !entry.isSymbolicLink()) {
        visit(entryPath);
      }
    }
  };
  visit(root);
  return found;
}

test("public materializer reproduces Python bytes for all exact ZCC resources", async () => {
  await withFixturePair((oracle, target) => {
    const assertions = new Map<ZccPullResourceType, ZccPullArtifactParity>();
    const expected = new Map<ZccPullResourceType, Map<string, Buffer>>();
    for (const resourceType of RESOURCES) {
      pythonMaterialize(oracle, resourceType);
      const comparison = invoke(compareRequest(oracle, resourceType));
      assert.equal(comparison.status, 0, comparison.stdout);
      assert.equal(comparison.stderr, "");
      requireCompareSuccess(comparison.response);
      assert.equal(comparison.response.result.status, "ready", resourceType);
      assertions.set(resourceType, comparison.response.result);
      const bytes = new Map<string, Buffer>();
      const paths = artifactPaths(oracle, resourceType);
      for (const name of applicableNames(resourceType)) {
        const artifactPath = paths[name];
        assert.notEqual(artifactPath, null);
        bytes.set(name, readFileSync(artifactPath as string));
      }
      expected.set(resourceType, bytes);
    }

    for (const resourceType of RESOURCES) {
      const assertion = assertions.get(resourceType);
      assert.notEqual(assertion, undefined);
      const request = materializeRequest(
        target,
        resourceType,
        assertion as ZccPullArtifactParity,
      );
      assert.equal(
        validateProcessRequest(request),
        true,
        JSON.stringify(validateProcessRequest.errors),
      );
      const first = invoke(request, target.outputRoot);
      assert.equal(first.status, 0, first.stdout);
      assert.equal(first.stderr, "", resourceType);
      assert.equal(
        validateProcessResponse(first.response),
        true,
        JSON.stringify(validateProcessResponse.errors),
      );
      requireMaterializeSuccess(first.response);
      assert.equal(
        validateZccPullArtifactMaterialization(first.response.result),
        true,
        JSON.stringify(validateZccPullArtifactMaterialization.errors),
      );
      assert.deepEqual(
        first.response.result.publication.created,
        applicableNames(resourceType),
      );
      assert.deepEqual(first.response.result.publication.reused, []);
      assert.equal(first.response.result.verification.status, "ready");
      assert.equal(first.response.result.verification.parity.status, "equal");
      assert.equal(first.stdout.includes("content"), false);
      assert.equal(first.stdout.includes(target.outputRoot), false);

      const paths = artifactPaths(target, resourceType);
      const expectedBytes = expected.get(resourceType);
      assert.notEqual(expectedBytes, undefined);
      for (const name of applicableNames(resourceType)) {
        const artifactPath = paths[name];
        assert.notEqual(artifactPath, null);
        assert.deepEqual(
          readFileSync(artifactPath as string),
          expectedBytes?.get(name),
          `${resourceType}/${name}`,
        );
      }

      const retry = invoke(request, target.outputRoot);
      assert.equal(retry.status, 0, retry.stdout);
      requireMaterializeSuccess(retry.response);
      assert.deepEqual(retry.response.result.publication.created, []);
      assert.deepEqual(
        retry.response.result.publication.reused,
        applicableNames(resourceType),
      );
    }

    const expectedFileMode = 0o666 & ~process.umask();
    const expectedDirectoryMode = 0o777 & ~process.umask();
    const representative = artifactPaths(target, "zcc_failopen_policy");
    assert.equal(
      statSync(representative.imports as string).mode & 0o777,
      expectedFileMode,
    );
    for (const directory of [
      path.join(target.outputRoot, "config"),
      path.join(target.outputRoot, "config", TENANT),
      path.join(target.outputRoot, "imports"),
      path.join(target.outputRoot, "imports", TENANT),
    ]) {
      assert.equal(statSync(directory).mode & 0o777, expectedDirectoryMode);
    }
    assert.equal(
      lstatSync(path.join(target.workspace, "artifacts")).isSymbolicLink(),
      false,
    );
    assert.equal(
      path.join(target.outputRoot, "artifacts") === target.outputRoot,
      false,
    );
    assert.equal(
      readdirSync(target.outputRoot).includes("artifacts"),
      false,
      "relative overlay must not be applied twice",
    );
    assert.deepEqual(temporaryAliases(target.outputRoot), []);
  });
});

test("public materializer fails before mutation for missing authority and bad assertions", async () => {
  await withFixturePair((oracle, target) => {
    const resourceType = "zcc_failopen_policy";
    pythonMaterialize(oracle, resourceType);
    const comparison = invoke(compareRequest(oracle, resourceType));
    assert.equal(comparison.status, 0, comparison.stdout);
    requireCompareSuccess(comparison.response);
    const assertion = comparison.response.result;

    const request = materializeRequest(target, resourceType, assertion);
    const missingAuthority = invoke(request);
    assert.equal(missingAuthority.status, 1, missingAuthority.stdout);
    assert.equal(missingAuthority.stderr, "");
    assert.equal(validateProcessResponse(missingAuthority.response), true);
    requireMaterializeError(missingAuthority.response);
    assert.equal(
      missingAuthority.response.error.code,
      "MATERIALIZE_OUTPUT_ROOT_NOT_CONFIGURED",
    );
    assert.equal(missingAuthority.response.error.category, "io");
    assert.equal(missingAuthority.stdout.includes(target.outputRoot), false);
    assert.equal(
      readdirSync(target.outputRoot).includes("config"),
      false,
    );

    const wrongResource = materializeRequest(
      target,
      "zcc_web_privacy",
      assertion,
    );
    assert.equal(validateProcessRequest(wrongResource), false);
    const mismatch = invoke(wrongResource, target.outputRoot);
    assert.equal(mismatch.status, 2, mismatch.stdout);
    assert.equal(mismatch.stderr, "");
    requireMaterializeError(mismatch.response);
    assert.equal(mismatch.response.error.code, "INVALID_REQUEST");
    assert.equal(mismatch.stdout.includes(target.outputRoot), false);
    assert.equal(mismatch.stdout.includes("content"), false);
    assert.equal(
      readdirSync(target.outputRoot).includes("config"),
      false,
    );

    const nonReady = structuredClone(request) as MaterializePullArtifactsProcessRequest;
    const mutableAssertion = nonReady.input.assertion as unknown as {
      status: string;
    };
    mutableAssertion.status = "review_required";
    const invalid = invoke(nonReady, target.outputRoot);
    assert.equal(invalid.status, 2, invalid.stdout);
    requireMaterializeError(invalid.response);
    assert.equal(invalid.response.error.code, "INVALID_REQUEST");
    assert.equal(
      readdirSync(target.outputRoot).includes("imports"),
      false,
    );
  });
});
