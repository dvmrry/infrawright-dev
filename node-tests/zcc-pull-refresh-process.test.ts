import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  symlinkSync,
  truncateSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullRefreshArtifactSet,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  compileZccPullArtifactsOperation,
  compileZccPullRefreshArtifactsOperation,
  type ZccPullOperationHooks,
} from "../node-src/domain/zcc-pull-operation.js";
import type {
  ZccPullArtifactSet,
  ZccPullResourceType,
  ZccTextArtifact,
} from "../node-src/domain/zcc-pull-artifacts.js";
import type { ZccPullRefreshArtifactSet } from "../node-src/domain/zcc-pull-refresh.js";
import type {
  CompilePullArtifactsProcessRequest,
  CompilePullArtifactsRefreshProcessSuccessResponse,
  ProcessErrorResponse,
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
const TENANT = "refresh_process";
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
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly resourceType: ZccPullResourceType;
  readonly pullPath: string;
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
  resourceType: ZccPullResourceType,
  callback: (fixture: Fixture) => void | Promise<void>,
  options: {
    readonly externalOverlay?: boolean;
    readonly deployment?: unknown;
  } = {},
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "zcc-refresh-process-"));
  const external = options.externalOverlay === true
    ? mkdtempSync(path.join(os.tmpdir(), "zcc-refresh-overlay-"))
    : null;
  const overlay = external ?? workspace;
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  const deployment = options.deployment ?? {
    overlay: external === null ? "." : external,
    roots: {},
  };
  try {
    mkdirSync(pullDirectory, { recursive: true });
    writeFileSync(deploymentPath, `${JSON.stringify(deployment)}\n`);
    copyFileSync(ROOT_CATALOG, catalogPath);
    const pullPath = path.join(pullDirectory, `${resourceType}.json`);
    writeFileSync(pullPath, rawPull(resourceType));
    await callback({
      workspace,
      overlay,
      deploymentPath,
      catalogPath,
      resourceType,
      pullPath,
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
    if (external !== null) {
      rmSync(external, { recursive: true, force: true });
    }
  }
}

function operationOptions(
  fixture: Fixture,
  hooks?: ZccPullOperationHooks,
) {
  return {
    workspace: fixture.workspace,
    deploymentPath: fixture.deploymentPath,
    catalogPath: fixture.catalogPath,
    tenant: TENANT,
    resourceType: fixture.resourceType,
    ...(hooks === undefined ? {} : { hooks }),
  };
}

function absoluteArtifact(fixture: Fixture, artifact: ZccTextArtifact): string {
  return path.isAbsolute(artifact.path)
    ? artifact.path
    : path.resolve(fixture.workspace, artifact.path);
}

function writeArtifact(fixture: Fixture, artifact: ZccTextArtifact): void {
  const destination = absoluteArtifact(fixture, artifact);
  mkdirSync(path.dirname(destination), { recursive: true });
  writeFileSync(destination, artifact.content);
}

function stageCandidate(fixture: Fixture, candidate: ZccPullArtifactSet): void {
  writeArtifact(fixture, candidate.artifacts.tfvars);
  writeArtifact(fixture, candidate.artifacts.imports);
  if (candidate.artifacts.lookup !== null) {
    writeArtifact(fixture, candidate.artifacts.lookup);
  }
}

async function stageBaseline(fixture: Fixture): Promise<ZccPullArtifactSet> {
  const candidate = await compileZccPullArtifactsOperation(
    operationOptions(fixture),
  );
  stageCandidate(fixture, candidate);
  return candidate;
}

function request(fixture: Fixture): CompilePullArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `refresh-${fixture.resourceType}`,
    operation: "compile_pull_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "refresh",
      tenant: TENANT,
      resource_type: fixture.resourceType,
    },
  };
}

function invoke(
  value: CompilePullArtifactsProcessRequest,
): { readonly status: number | null; readonly response: ProcessResponse } {
  const child = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: WORKSPACE,
    input: JSON.stringify(value),
    encoding: "utf8",
    env: { ...process.env, INFRAWRIGHT_TERRAFORM_EXECUTABLE: "" },
  });
  assert.equal(child.signal, null, child.error?.message);
  assert.equal(child.stderr, "");
  assert.doesNotThrow(() => JSON.parse(child.stdout));
  return {
    status: child.status,
    response: JSON.parse(child.stdout) as ProcessResponse,
  };
}

function requireRefreshSuccess(
  response: ProcessResponse,
): asserts response is CompilePullArtifactsRefreshProcessSuccessResponse {
  assert.equal(response.operation, "compile_pull_artifacts");
  assert.equal(response.status, "ok");
  assert.notEqual(response.result, null);
  assert.equal(response.result?.kind, "infrawright.zcc_pull_refresh_artifact_set");
}

function requireError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "compile_pull_artifacts");
  assert.equal(response.status, "error");
  assert.notEqual(response.error, null);
  assert.equal(response.result, null);
}

async function expectFailure(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) => error instanceof ProcessFailure
      && error.code === code
      && !error.message.includes("refresh-private-value"),
  );
}

function artifactSibling(
  candidate: ZccPullArtifactSet,
  suffix: "moves" | "pending" | "hcl" | "generated" | "lookup",
): string {
  const imports = candidate.artifacts.imports.path;
  const config = candidate.artifacts.tfvars.path;
  if (suffix === "moves") {
    return imports.slice(0, -"_imports.tf".length) + "_moves.tf";
  }
  if (suffix === "pending") {
    return imports.slice(0, -"_imports.tf".length) + "_moves.pending.json";
  }
  if (suffix === "hcl") {
    return config.slice(0, -".json".length);
  }
  const directory = path.dirname(config);
  return path.join(
    directory,
    suffix === "generated"
      ? `${candidate.resource_type}.generated.expressions.json`
      : `${candidate.resource_type}.lookup.json`,
  );
}

function absoluteLogical(fixture: Fixture, logicalPath: string): string {
  return path.isAbsolute(logicalPath)
    ? logicalPath
    : path.resolve(fixture.workspace, logicalPath);
}

test("public refresh compiles all five ZCC resources without writing", async (t) => {
  for (const resourceType of RESOURCES) {
    await t.test(resourceType, async () => {
      await withFixture(resourceType, async (fixture) => {
        const baseline = await stageBaseline(fixture);
        const before = {
          tfvars: readFileSync(absoluteArtifact(fixture, baseline.artifacts.tfvars)),
          imports: readFileSync(absoluteArtifact(fixture, baseline.artifacts.imports)),
          lookup: baseline.artifacts.lookup === null
            ? null
            : readFileSync(absoluteArtifact(fixture, baseline.artifacts.lookup)),
        };
        const compileRequest = request(fixture);
        assert.equal(validateProcessRequest(compileRequest), true);
        const invocation = invoke(compileRequest);
        assert.equal(invocation.status, 0, JSON.stringify(invocation.response));
        assert.equal(validateProcessResponse(invocation.response), true);
        requireRefreshSuccess(invocation.response);
        const result = invocation.response.result;
        assert.equal(result.mode, compileRequest.input.mode);
        assert.equal(validateZccPullRefreshArtifactSet(result), true);
        assert.equal(result.status, "ready");
        assert.deepEqual(result.moves, { safe: [], suppressed: [] });
        assert.equal(result.baseline.imports.state, "present");
        assert.equal(result.baseline.tfvars.state, "present");
        assert.equal(
          result.baseline.lookup.state,
          resourceType === "zcc_trusted_network" ? "present" : "absent",
        );
        assert.equal(result.desired.moves.state, "absent");
        assert.equal(
          result.desired.lookup.state,
          resourceType === "zcc_trusted_network" ? "present" : "absent",
        );
        assert.deepEqual(
          readFileSync(absoluteArtifact(fixture, baseline.artifacts.tfvars)),
          before.tfvars,
        );
        assert.deepEqual(
          readFileSync(absoluteArtifact(fixture, baseline.artifacts.imports)),
          before.imports,
        );
        if (baseline.artifacts.lookup !== null && before.lookup !== null) {
          assert.deepEqual(
            readFileSync(absoluteArtifact(fixture, baseline.artifacts.lookup)),
            before.lookup,
          );
        }
      });
    });
  }
});

test("refresh reports safe renames and unsafe suppression with process exits", async () => {
  await withFixture("zcc_forwarding_profile", async (fixture) => {
    writeFileSync(
      fixture.pullPath,
      '[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"}]\n',
    );
    await stageBaseline(fixture);
    writeFileSync(
      fixture.pullPath,
      '[{"id":"1","name":"Renamed Alpha"},{"id":"2","name":"Beta"}]\n',
    );
    const renamed = invoke(request(fixture));
    assert.equal(renamed.status, 0);
    requireRefreshSuccess(renamed.response);
    assert.equal(renamed.response.result.status, "ready");
    assert.deepEqual(renamed.response.result.moves.safe, [{
      from_key: "alpha",
      to_key: "renamed_alpha",
    }]);
    assert.equal(renamed.response.result.desired.moves.state, "present");
    if (renamed.response.result.desired.moves.state === "present") {
      assert.equal(
        existsSync(absoluteLogical(
          fixture,
          renamed.response.result.desired.moves.artifact.path,
        )),
        false,
      );
    }
  });

  await withFixture("zcc_forwarding_profile", async (fixture) => {
    writeFileSync(
      fixture.pullPath,
      '[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"}]\n',
    );
    await stageBaseline(fixture);
    writeFileSync(
      fixture.pullPath,
      '[{"id":"1","name":"Beta"},{"id":"2","name":"Alpha"}]\n',
    );
    const suppressed = invoke(request(fixture));
    assert.equal(suppressed.status, 3);
    requireRefreshSuccess(suppressed.response);
    assert.equal(suppressed.response.result.status, "review_required");
    assert.deepEqual(
      suppressed.response.result.moves.suppressed.map((item) => item.reason),
      ["key_swap", "key_swap"],
    );
    assert.equal(suppressed.response.result.desired.moves.state, "absent");
  });
});

test("trusted-network rename retains lookup bytes and add/remove are not moves", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    writeFileSync(
      fixture.pullPath,
      '[{"id":"501","networkName":"HQ Wired","active":true}]\n',
    );
    await stageBaseline(fixture);
    writeFileSync(
      fixture.pullPath,
      '[{"id":"501","networkName":"東京 &amp; HQ","active":true}]\n',
    );
    const renamed = await compileZccPullRefreshArtifactsOperation(
      operationOptions(fixture),
    );
    assert.equal(renamed.moves.safe.length, 1);
    assert.equal(renamed.moves.safe[0]?.from_key, "hq_wired");
    assert.equal(renamed.moves.safe[0]?.to_key, "amp_hq");
    assert.equal(renamed.desired.lookup.state, "present");
    if (renamed.desired.lookup.state === "present") {
      assert.match(renamed.desired.lookup.artifact.content, /"amp_hq"/u);
    }
  });

  for (const [name, before, after] of [
    [
      "add",
      '[{"id":"1","name":"Alpha"}]\n',
      '[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"}]\n',
    ],
    [
      "remove",
      '[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"}]\n',
      '[{"id":"1","name":"Alpha"}]\n',
    ],
  ] as const) {
    await withFixture("zcc_forwarding_profile", async (fixture) => {
      writeFileSync(fixture.pullPath, before);
      await stageBaseline(fixture);
      writeFileSync(fixture.pullPath, after);
      const result = await compileZccPullRefreshArtifactsOperation(
        operationOptions(fixture),
      );
      assert.equal(result.status, "ready", name);
      assert.equal(result.moves.safe.length, 0, name);
      assert.equal(result.moves.suppressed.length, 0, name);
      assert.equal(result.desired.moves.state, "absent", name);
    });
  }
});

test("refresh requires canonical imports but accepts a present empty baseline", async () => {
  await withFixture("zcc_failopen_policy", async (fixture) => {
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
      "REFRESH_IMPORTS_MISSING",
    );
    const bootstrap = await compileZccPullArtifactsOperation(
      operationOptions(fixture),
    );
    const importsPath = absoluteArtifact(fixture, bootstrap.artifacts.imports);
    mkdirSync(path.dirname(importsPath), { recursive: true });
    writeFileSync(importsPath, "");
    const empty = await compileZccPullRefreshArtifactsOperation(
      operationOptions(fixture),
    );
    assert.equal(empty.baseline.imports.size_bytes, 0);
    assert.equal(empty.status, "ready");

    for (const content of ["# comment\n", "import {\r\n}\r\n"]) {
      writeFileSync(importsPath, content);
      await expectFailure(
        compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
        "REFRESH_IMPORTS_NONCANONICAL",
      );
    }
    writeFileSync(importsPath, Buffer.from([0xff]));
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
      "INVALID_UTF8",
    );
    truncateSync(importsPath, 32 * 1024 * 1024 + 1);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
      "FILE_LIMIT_EXCEEDED",
    );
    rmSync(importsPath, { force: true });
    mkdirSync(importsPath);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
      "REFRESH_IMPORTS_NOT_REGULAR",
    );
    rmSync(importsPath, { recursive: true, force: true });
    const symlinkTarget = path.join(fixture.workspace, "symlink-target");
    writeFileSync(symlinkTarget, "");
    symlinkSync(symlinkTarget, importsPath);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
      "REFRESH_IMPORTS_NOT_REGULAR",
    );
  });
});

test("refresh refuses and rechecks every adjacent unsupported state", async (t) => {
  const cases = [
    ["moves", "REFRESH_MOVES_EXIST"],
    ["pending", "REFRESH_PENDING_MOVES_EXIST"],
    ["hcl", "UNSUPPORTED_REFRESH_HCL_ARTIFACT"],
    ["generated", "UNSUPPORTED_REFRESH_GENERATED_BINDINGS"],
    ["lookup", "UNSUPPORTED_REFRESH_LOOKUP_ARTIFACT"],
  ] as const;
  for (const [kind, code] of cases) {
    await t.test(kind, async () => {
      await withFixture("zcc_failopen_policy", async (fixture) => {
        const baseline = await stageBaseline(fixture);
        const unsupported = absoluteLogical(
          fixture,
          artifactSibling(baseline, kind),
        );
        mkdirSync(path.dirname(unsupported), { recursive: true });
        writeFileSync(unsupported, "refresh-private-value\n");
        await expectFailure(
          compileZccPullRefreshArtifactsOperation(operationOptions(fixture)),
          code,
        );
      });
    });
  }

  for (const [kind] of cases) {
    await withFixture("zcc_failopen_policy", async (fixture) => {
      const baseline = await stageBaseline(fixture);
      const target = absoluteLogical(
        fixture,
        artifactSibling(baseline, kind),
      );
      await expectFailure(
        compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
          beforeFinalRecheck: () => {
            mkdirSync(path.dirname(target), { recursive: true });
            writeFileSync(target, "refresh-private-value\n");
          },
        })),
        "REFRESH_ARTIFACT_CHANGED",
      );
    });
  }
});

test("refresh rechecks present and absent CAS baselines", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const baseline = await stageBaseline(fixture);
    assert.notEqual(baseline.artifacts.lookup, null);
    const tfvars = absoluteArtifact(fixture, baseline.artifacts.tfvars);
    const imports = absoluteArtifact(fixture, baseline.artifacts.imports);
    const lookup = absoluteArtifact(fixture, baseline.artifacts.lookup!);

    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
        beforeFinalRecheck: () => writeFileSync(imports, "refresh-private-value\n"),
      })),
      "REFRESH_IMPORTS_CHANGED",
    );
    stageCandidate(fixture, baseline);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
        afterRefreshCompiled: () => {
          const replacement = `${imports}.replacement`;
          writeFileSync(replacement, readFileSync(imports));
          renameSync(replacement, imports);
        },
      })),
      "REFRESH_IMPORTS_CHANGED",
    );
    stageCandidate(fixture, baseline);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
        beforeFinalRecheck: () => writeFileSync(tfvars, "refresh-private-value\n"),
      })),
      "REFRESH_BASELINE_CHANGED",
    );
    stageCandidate(fixture, baseline);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
        beforeFinalRecheck: () => {
          const replacement = `${lookup}.replacement`;
          writeFileSync(replacement, readFileSync(lookup));
          renameSync(replacement, lookup);
        },
      })),
      "REFRESH_BASELINE_CHANGED",
    );
  });

  await withFixture("zcc_failopen_policy", async (fixture) => {
    const baseline = await stageBaseline(fixture);
    const tfvars = absoluteArtifact(fixture, baseline.artifacts.tfvars);
    unlinkSync(tfvars);
    await expectFailure(
      compileZccPullRefreshArtifactsOperation(operationOptions(fixture, {
        beforeFinalRecheck: () => writeFileSync(tfvars, "appeared\n"),
      })),
      "REFRESH_BASELINE_CHANGED",
    );
  });
});

test("refresh supports deployment-derived external overlays and snapshots options", async () => {
  await withFixture(
    "zcc_failopen_policy",
    async (fixture) => {
      await stageBaseline(fixture);
      const options = operationOptions(fixture, {
        afterInputsBound: () => undefined,
      }) as {
        workspace: string;
        deploymentPath: string;
        catalogPath: string;
        tenant: string;
        resourceType: string;
        hooks: ZccPullOperationHooks;
      };
      const operation = compileZccPullRefreshArtifactsOperation(options);
      options.resourceType = "zcc_web_privacy";
      options.hooks = {
        beforeFinalRecheck: () => {
          throw new Error("mutated hook must not run");
        },
      };
      const result = await operation;
      assert.equal(result.resource_type, "zcc_failopen_policy");
      assert.equal(result.baseline.imports.path.startsWith(fixture.overlay), true);
      const lookupPath = path.join(
        fixture.overlay,
        "config",
        TENANT,
        "zcc_failopen_policy.lookup.json",
      );
      assert.equal(result.baseline.lookup.path, lookupPath);
      assert.equal(result.baseline.lookup.state, "absent");
      assert.equal(result.desired.lookup.state, "absent");
      if (result.desired.lookup.state === "absent") {
        assert.equal(result.desired.lookup.path, lookupPath);
      }
      assert.equal(result.status, "ready");
    },
    { externalOverlay: true },
  );
});
