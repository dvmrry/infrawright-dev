import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullArtifactSet,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  compileZccPullArtifactsOperation,
  type ZccPullOperationHooks,
} from "../node-src/domain/zcc-pull-operation.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  CompilePullArtifactsProcessRequest,
  CompilePullArtifactsProcessSuccessResponse,
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
const TENANT = "process_test";
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
  readonly artifactPath: (
    resourceType: ZccPullResourceType,
    kind: "imports" | "moves",
  ) => string;
}

function demoPull(resourceType: ZccPullResourceType): string {
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
  deployment: unknown = { overlay: ".", roots: {} },
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "zcc-pull-process-"));
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  try {
    mkdirSync(pullDirectory, { recursive: true });
    writeFileSync(deploymentPath, `${JSON.stringify(deployment)}\n`);
    copyFileSync(ROOT_CATALOG, catalogPath);
    for (const resourceType of RESOURCES) {
      writeFileSync(
        path.join(pullDirectory, `${resourceType}.json`),
        demoPull(resourceType),
      );
    }
    await callback({
      workspace,
      deploymentPath,
      catalogPath,
      pullPath: (resourceType) => {
        return path.join(pullDirectory, `${resourceType}.json`);
      },
      artifactPath: (resourceType, kind) => {
        return path.join(
          workspace,
          "imports",
          TENANT,
          `${resourceType}_${kind}.tf`,
        );
      },
    });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

function request(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
  requestId = `compile-${resourceType}`,
): CompilePullArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: requestId,
    operation: "compile_pull_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      tenant: TENANT,
      resource_type: resourceType,
    },
  };
}

function invoke(
  compileRequest: CompilePullArtifactsProcessRequest,
): {
  readonly status: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly response: ProcessResponse;
} {
  const result = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: WORKSPACE,
    input: JSON.stringify(compileRequest),
    encoding: "utf8",
    env: { ...process.env, INFRAWRIGHT_TERRAFORM_EXECUTABLE: "" },
  });
  assert.equal(result.signal, null, result.error?.message);
  assert.doesNotThrow(() => JSON.parse(result.stdout), result.stderr);
  return {
    status: result.status,
    stdout: result.stdout,
    stderr: result.stderr,
    response: JSON.parse(result.stdout) as ProcessResponse,
  };
}

function requireSuccess(
  response: ProcessResponse,
): asserts response is CompilePullArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "compile_pull_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "compile_pull_artifacts");
  assert.equal(response.status, "error");
  assert.equal(response.result, null);
  assert.notEqual(response.error, null);
}

function expectedDemoArtifact(
  resourceType: ZccPullResourceType,
  suffix: "tfvars.json" | "imports.tf",
): string | null {
  if (resourceType === "zcc_device_cleanup") {
    return null;
  }
  const stem = suffix === "tfvars.json"
    ? `${resourceType}.tfvars.json`
    : `${resourceType}_imports.tf`;
  return readFileSync(
    path.join(WORKSPACE, "tests/fixtures/demo-expected", stem),
    "utf8",
  );
}

async function expectOperationFailure(
  operation: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(
    operation,
    (error: unknown) => error instanceof ProcessFailure
      && error.code === code
      && !error.message.includes("process-secret-value"),
  );
}

test("compile_pull_artifacts publicly compiles every exact ZCC bootstrap resource", async () => {
  await withFixture((fixture) => {
    for (const resourceType of RESOURCES) {
      const compileRequest = request(fixture, resourceType);
      assert.equal(validateProcessRequest(compileRequest), true, resourceType);

      const invocation = invoke(compileRequest);
      assert.equal(invocation.status, 0, invocation.stdout);
      assert.equal(invocation.stderr, "", resourceType);
      assert.ok(invocation.stdout.endsWith("\n"), resourceType);
      assert.equal(
        validateProcessResponse(invocation.response),
        true,
        JSON.stringify(validateProcessResponse.errors),
      );
      requireSuccess(invocation.response);
      assert.equal(
        validateZccPullArtifactSet(invocation.response.result),
        true,
        JSON.stringify(validateZccPullArtifactSet.errors),
      );

      const result = invocation.response.result;
      assert.equal(result.resource_type, resourceType);
      assert.equal(result.tenant, TENANT);
      assert.equal(result.status, "ready");
      assert.deepEqual(result.unexpected_drops, []);
      assert.deepEqual(result.root, {
        label: resourceType,
        members: [resourceType],
        variable_name: "items",
      });
      assert.equal(
        result.source.path,
        `pulls/${TENANT}/${resourceType}.json`,
      );
      const pullBytes = readFileSync(fixture.pullPath(resourceType));
      assert.equal(result.source.size_bytes, pullBytes.length);
      assert.equal(
        result.source.sha256,
        createHash("sha256").update(pullBytes).digest("hex"),
      );
      assert.equal(
        result.artifacts.tfvars.path,
        `config/${TENANT}/${resourceType}.auto.tfvars.json`,
      );
      assert.equal(
        result.artifacts.imports.path,
        `imports/${TENANT}/${resourceType}_imports.tf`,
      );

      const expectedTfvars = expectedDemoArtifact(resourceType, "tfvars.json");
      const expectedImports = expectedDemoArtifact(resourceType, "imports.tf");
      if (expectedTfvars !== null && expectedImports !== null) {
        assert.equal(result.artifacts.tfvars.content, expectedTfvars, resourceType);
        assert.equal(result.artifacts.imports.content, expectedImports, resourceType);
      } else {
        assert.match(result.artifacts.tfvars.content, /"device_1"/);
        assert.match(result.artifacts.imports.content, /id = "device-1"/);
      }

      if (resourceType === "zcc_trusted_network") {
        assert.notEqual(result.artifacts.lookup, null);
        assert.equal(
          result.artifacts.lookup?.path,
          `config/${TENANT}/zcc_trusted_network.lookup.json`,
        );
        assert.equal(
          result.artifacts.lookup?.content,
          readFileSync(
            path.join(
              WORKSPACE,
              "demo/config/demo/zcc_trusted_network.lookup.json",
            ),
            "utf8",
          ),
        );
      } else {
        assert.equal(result.artifacts.lookup, null);
      }
    }

    const invalidRequest = structuredClone(
      request(fixture, "zcc_device_cleanup"),
    ) as CompilePullArtifactsProcessRequest & {
      input: CompilePullArtifactsProcessRequest["input"] & {
        source_path?: string;
      };
    };
    invalidRequest.input.source_path = "caller-controlled.json";
    assert.equal(validateProcessRequest(invalidRequest), false);
  });
});

test("unexpected API surface is review-required, secret-safe, and exits 3", async () => {
  await withFixture((fixture) => {
    const secret = "process-secret-value";
    writeFileSync(
      fixture.pullPath("zcc_device_cleanup"),
      JSON.stringify([{
        id: "device-1",
        active: "1",
        unexpectedApiField: secret,
      }]),
    );
    const invocation = invoke(request(fixture, "zcc_device_cleanup"));
    assert.equal(invocation.status, 3, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(validateProcessResponse(invocation.response), true);
    requireSuccess(invocation.response);
    assert.equal(invocation.response.result.status, "review_required");
    assert.deepEqual(
      invocation.response.result.unexpected_drops,
      ["unexpected_api_field"],
    );
    assert.equal(invocation.stdout.includes(secret), false);
  });
});

test("nested forwarding-profile null surface is review-required and value-safe", async () => {
  await withFixture((fixture) => {
    const planted = "process-secret-value";
    writeFileSync(
      fixture.pullPath("zcc_forwarding_profile"),
      JSON.stringify([{
        id: "profile-1",
        name: "Nested surface",
        unifiedTunnel: [
          { id: 0, futureSecret: null },
          { id: planted },
        ],
      }]),
    );

    const invocation = invoke(request(fixture, "zcc_forwarding_profile"));
    assert.equal(invocation.status, 3, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(
      validateProcessResponse(invocation.response),
      true,
      JSON.stringify(validateProcessResponse.errors),
    );
    requireSuccess(invocation.response);
    assert.equal(
      validateZccPullArtifactSet(invocation.response.result),
      true,
      JSON.stringify(validateZccPullArtifactSet.errors),
    );
    assert.equal(invocation.response.result.status, "review_required");
    assert.deepEqual(
      invocation.response.result.unexpected_drops,
      ["unified_tunnel[].future_secret"],
    );
    assert.equal(invocation.stdout.includes(planted), false);
  });
});

test("merged single-block null surface remains review-required and value-safe", async () => {
  await withFixture((fixture) => {
    const planted = "process-secret-value";
    writeFileSync(
      fixture.pullPath("zcc_forwarding_profile"),
      JSON.stringify([{
        id: "profile-1",
        name: "Merged nested surface",
        unifiedTunnel: [{
          id: planted,
          systemProxyData: [
            { id: "0", futureSecret: null },
            { id: "0" },
          ],
        }],
      }]),
    );

    const invocation = invoke(request(fixture, "zcc_forwarding_profile"));
    assert.equal(invocation.status, 3, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(
      validateProcessResponse(invocation.response),
      true,
      JSON.stringify(validateProcessResponse.errors),
    );
    requireSuccess(invocation.response);
    assert.equal(
      validateZccPullArtifactSet(invocation.response.result),
      true,
      JSON.stringify(validateZccPullArtifactSet.errors),
    );
    assert.equal(invocation.response.result.status, "review_required");
    assert.deepEqual(
      invocation.response.result.unexpected_drops,
      ["unified_tunnel[].system_proxy_data.future_secret"],
    );
    assert.equal(invocation.stdout.includes(planted), false);
  });
});

test("trusted-network provider identities fail closed without value leakage", async (t) => {
  await t.test("duplicate nonblank identities", async () => {
    await withFixture((fixture) => {
      const planted = "process-secret-value";
      writeFileSync(
        fixture.pullPath("zcc_trusted_network"),
        JSON.stringify([
          { id: planted, networkName: "Distinct key one" },
          { id: planted, networkName: "Distinct key two" },
        ]),
      );

      const invocation = invoke(request(fixture, "zcc_trusted_network"));
      assert.equal(invocation.status, 2, invocation.stdout);
      assert.equal(invocation.stderr, "");
      assert.equal(
        validateProcessResponse(invocation.response),
        true,
        JSON.stringify(validateProcessResponse.errors),
      );
      requireError(invocation.response);
      assert.equal(invocation.response.error.code, "INVALID_ZCC_PULL_DATA");
      assert.equal(invocation.stdout.includes(planted), false);
    });
  });

  await t.test("whitespace-only identity", async () => {
    await withFixture((fixture) => {
      const planted = "process-secret-value";
      writeFileSync(
        fixture.pullPath("zcc_trusted_network"),
        JSON.stringify([{ id: " \t\r\n", networkName: planted }]),
      );

      const invocation = invoke(request(fixture, "zcc_trusted_network"));
      assert.equal(invocation.status, 2, invocation.stdout);
      assert.equal(invocation.stderr, "");
      assert.equal(
        validateProcessResponse(invocation.response),
        true,
        JSON.stringify(validateProcessResponse.errors),
      );
      requireError(invocation.response);
      assert.equal(invocation.response.error.code, "INVALID_ZCC_PULL_DATA");
      assert.equal(invocation.stdout.includes(planted), false);
    });
  });
});

test("bootstrap refuses prior imports and moves artifacts", async (t) => {
  for (const fixtureCase of [
    { kind: "imports", code: "BOOTSTRAP_IMPORTS_EXIST" },
    { kind: "moves", code: "BOOTSTRAP_MOVES_EXIST" },
  ] as const) {
    await t.test(fixtureCase.kind, async () => {
      await withFixture((fixture) => {
        const artifactPath = fixture.artifactPath(
          "zcc_device_cleanup",
          fixtureCase.kind,
        );
        mkdirSync(path.dirname(artifactPath), { recursive: true });
        writeFileSync(artifactPath, "# prior bootstrap state\n");
        const invocation = invoke(request(fixture, "zcc_device_cleanup"));
        assert.equal(invocation.status, 2, invocation.stdout);
        assert.equal(invocation.stderr, "");
        assert.equal(validateProcessResponse(invocation.response), true);
        requireError(invocation.response);
        assert.equal(invocation.response.error.code, fixtureCase.code);
      });
    });
  }
});

test("pull invocation failures are closed, versioned, and value-safe", async (t) => {
  const cases = [
    {
      name: "missing",
      setup: (fixture: Fixture) => {
        unlinkSync(fixture.pullPath("zcc_device_cleanup"));
      },
      status: 1,
      code: "READ_FAILED",
    },
    {
      name: "invalid JSON",
      setup: (fixture: Fixture) => {
        writeFileSync(
          fixture.pullPath("zcc_device_cleanup"),
          '[{"id":"process-secret-value"}',
        );
      },
      status: 2,
      code: "INVALID_PULL_DATA_JSON",
    },
    {
      name: "non-array JSON",
      setup: (fixture: Fixture) => {
        writeFileSync(
          fixture.pullPath("zcc_device_cleanup"),
          '{"value":"process-secret-value"}',
        );
      },
      status: 2,
      code: "INVALID_PULL_DATA_JSON",
    },
  ] as const;

  for (const fixtureCase of cases) {
    await t.test(fixtureCase.name, async () => {
      await withFixture((fixture) => {
        fixtureCase.setup(fixture);
        const invocation = invoke(request(fixture, "zcc_device_cleanup"));
        assert.equal(invocation.status, fixtureCase.status, invocation.stdout);
        assert.equal(invocation.stderr, "");
        assert.equal(validateProcessResponse(invocation.response), true);
        requireError(invocation.response);
        assert.equal(invocation.response.error.code, fixtureCase.code);
        assert.equal(invocation.stdout.includes("process-secret-value"), false);
      });
    });
  }
});

test("unsupported HCL and generated same-root bindings fail before emission", async (t) => {
  await t.test("HCL tfvars", async () => {
    await withFixture((fixture) => {
      const invocation = invoke(request(fixture, "zcc_device_cleanup"));
      assert.equal(invocation.status, 2, invocation.stdout);
      assert.equal(validateProcessResponse(invocation.response), true);
      requireError(invocation.response);
      assert.equal(invocation.response.error.code, "UNSUPPORTED_TFVARS_FORMAT");
    }, { overlay: ".", tfvars_format: "hcl", roots: {} });
  });

  await t.test("same-root reference bindings", async () => {
    await withFixture((fixture) => {
      const invocation = invoke(request(fixture, "zcc_forwarding_profile"));
      assert.equal(invocation.status, 2, invocation.stdout);
      assert.equal(validateProcessResponse(invocation.response), true);
      requireError(invocation.response);
      assert.equal(invocation.response.error.code, "UNSUPPORTED_GROUP_BINDINGS");
    }, {
      overlay: ".",
      roots: {
        zcc: {
          strategy: "explicit",
          bind_references: true,
          groups: {
            zcc_bootstrap: [
              "zcc_forwarding_profile",
              "zcc_trusted_network",
            ],
          },
        },
      },
    });
  });
});

test("direct operation rechecks raw, control, and bootstrap precondition races", async (t) => {
  const run = (
    fixture: Fixture,
    hooks: ZccPullOperationHooks,
  ): Promise<unknown> => compileZccPullArtifactsOperation({
    workspace: fixture.workspace,
    deploymentPath: fixture.deploymentPath,
    catalogPath: fixture.catalogPath,
    tenant: TENANT,
    resourceType: "zcc_device_cleanup",
    hooks,
  });

  await t.test("raw source mutation", async () => {
    await withFixture(async (fixture) => {
      await expectOperationFailure(run(fixture, {
        beforeFinalRecheck: () => {
          writeFileSync(
            fixture.pullPath("zcc_device_cleanup"),
            '[{"id":"process-secret-value"}]\n',
          );
        },
      }), "RAW_PULL_CHANGED");
    });
  });

  await t.test("deployment control mutation", async () => {
    await withFixture(async (fixture) => {
      await expectOperationFailure(run(fixture, {
        beforeFinalRecheck: () => {
          writeFileSync(
            fixture.deploymentPath,
            '{"overlay":"changed","roots":{}}\n',
          );
        },
      }), "COMPILE_CONTROL_CHANGED");
    });
  });

  await t.test("root-catalog control mutation", async () => {
    await withFixture(async (fixture) => {
      await expectOperationFailure(run(fixture, {
        beforeFinalRecheck: () => {
          writeFileSync(fixture.catalogPath, "{}\n");
        },
      }), "COMPILE_CONTROL_CHANGED");
    });
  });

  await t.test("imports appears before commit", async () => {
    await withFixture(async (fixture) => {
      await expectOperationFailure(run(fixture, {
        beforeFinalRecheck: () => {
          const importsPath = fixture.artifactPath(
            "zcc_device_cleanup",
            "imports",
          );
          mkdirSync(path.dirname(importsPath), { recursive: true });
          writeFileSync(importsPath, "# process-secret-value\n");
        },
      }), "BOOTSTRAP_IMPORTS_EXIST");
    });
  });
});
