import assert from "node:assert/strict";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { validateZccPullArtifactParity } from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  compareZccPullArtifactsOperation,
  compileZccPullArtifactsOperation,
  type ZccPullOperationHooks,
} from "../node-src/domain/zcc-pull-operation.js";
import type {
  ZccPullArtifactSet,
  ZccPullResourceType,
  ZccTextArtifact,
} from "../node-src/domain/zcc-pull-artifacts.js";

const WORKSPACE = process.cwd();
const ROOT_CATALOG = path.join(
  WORKSPACE,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "parity_test";

interface Fixture {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly resourceType: ZccPullResourceType;
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
  deployment: unknown = { overlay: ".", roots: {} },
): Promise<void> {
  const workspace = mkdtempSync(path.join(os.tmpdir(), "zcc-pull-parity-"));
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  const pullDirectory = path.join(workspace, "pulls", TENANT);
  try {
    mkdirSync(pullDirectory, { recursive: true });
    writeFileSync(deploymentPath, `${JSON.stringify(deployment)}\n`);
    copyFileSync(ROOT_CATALOG, catalogPath);
    writeFileSync(
      path.join(pullDirectory, `${resourceType}.json`),
      rawPull(resourceType),
    );
    await callback({ workspace, deploymentPath, catalogPath, resourceType });
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

function operationOptions(
  fixture: Fixture,
  hooks?: ZccPullOperationHooks,
): {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tenant: string;
  readonly resourceType: string;
  readonly hooks?: ZccPullOperationHooks;
} {
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
  return path.resolve(fixture.workspace, artifact.path);
}

function writeArtifact(fixture: Fixture, artifact: ZccTextArtifact): void {
  const destination = absoluteArtifact(fixture, artifact);
  mkdirSync(path.dirname(destination), { recursive: true });
  writeFileSync(destination, artifact.content);
}

function materialize(
  fixture: Fixture,
  candidate: ZccPullArtifactSet,
  kinds: readonly ("tfvars" | "imports" | "lookup")[] = [
    "tfvars",
    "imports",
    "lookup",
  ],
): void {
  for (const kind of kinds) {
    const artifact = candidate.artifacts[kind];
    if (artifact !== null) {
      writeArtifact(fixture, artifact);
    }
  }
}

async function candidate(fixture: Fixture): Promise<ZccPullArtifactSet> {
  return compileZccPullArtifactsOperation(operationOptions(fixture));
}

async function expectFailure(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) => error instanceof ProcessFailure
      && error.code === code
      && !error.message.includes("parity-secret-value"),
  );
}

test("materialized bootstrap artifacts produce an immutable digest-only parity report", async (t) => {
  for (const resourceType of [
    "zcc_device_cleanup",
    "zcc_trusted_network",
  ] as const) {
    await t.test(resourceType, async () => {
      await withFixture(resourceType, async (fixture) => {
        const compiled = await candidate(fixture);
        materialize(fixture, compiled);

        const report = await compareZccPullArtifactsOperation(
          operationOptions(fixture),
        );
        assert.equal(
          validateZccPullArtifactParity(report),
          true,
          JSON.stringify(validateZccPullArtifactParity.errors),
        );
        const applicable = compiled.artifacts.lookup === null ? 2 : 3;
        assert.equal(report.kind, "infrawright.zcc_pull_artifact_parity");
        assert.equal(report.schema_version, 1);
        assert.equal(report.mode, "bootstrap");
        assert.equal(report.reference, "materialized");
        assert.equal(report.product, "zcc");
        assert.equal(report.resource_type, resourceType);
        assert.equal(report.tenant, TENANT);
        assert.deepEqual(report.source, compiled.source);
        assert.deepEqual(report.catalog, compiled.catalog);
        assert.deepEqual(report.root, compiled.root);
        assert.equal(report.candidate.status, "ready");
        assert.deepEqual(report.candidate.unexpected_drops, []);
        assert.equal(report.status, "ready");
        assert.equal(report.parity.status, "equal");
        assert.equal(report.parity.matched, applicable);
        assert.equal(report.parity.mismatched, 0);
        assert.equal(report.parity.missing, 0);
        assert.equal(report.parity.artifacts.tfvars.status, "match");
        assert.equal(report.parity.artifacts.imports.status, "match");
        assert.equal(
          report.parity.artifacts.lookup.status,
          compiled.artifacts.lookup === null ? "not_applicable" : "match",
        );
        if (compiled.artifacts.lookup === null) {
          assert.equal(report.parity.artifacts.lookup.path, null);
          assert.equal(report.parity.artifacts.lookup.expected, null);
          assert.equal(report.parity.artifacts.lookup.observed, null);
        }
        assert.equal(JSON.stringify(report).includes("content"), false);
        assert.ok(Object.isFrozen(report));
        assert.ok(Object.isFrozen(report.parity.artifacts));
      });
    });
  }
});

test("parity counts mismatched and missing artifacts without emitting bytes", async () => {
  await withFixture("zcc_trusted_network", async (fixture) => {
    const compiled = await candidate(fixture);
    materialize(fixture, compiled, ["tfvars", "lookup"]);
    writeFileSync(
      absoluteArtifact(fixture, compiled.artifacts.tfvars),
      "parity-secret-value\n",
    );

    const report = await compareZccPullArtifactsOperation(
      operationOptions(fixture),
    );
    assert.equal(
      validateZccPullArtifactParity(report),
      true,
      JSON.stringify(validateZccPullArtifactParity.errors),
    );
    assert.equal(report.status, "review_required");
    assert.deepEqual(
      {
        status: report.parity.status,
        matched: report.parity.matched,
        mismatched: report.parity.mismatched,
        missing: report.parity.missing,
      },
      { status: "different", matched: 1, mismatched: 1, missing: 1 },
    );
    assert.equal(report.parity.artifacts.tfvars.status, "mismatch");
    assert.notEqual(report.parity.artifacts.tfvars.observed, null);
    assert.equal(report.parity.artifacts.imports.status, "missing");
    assert.equal(report.parity.artifacts.imports.observed, null);
    assert.equal(report.parity.artifacts.lookup.status, "match");
    assert.equal(JSON.stringify(report).includes("parity-secret-value"), false);
    assert.equal(JSON.stringify(report).includes("content"), false);
  });
});

test("candidate review remains independent from byte parity", async () => {
  await withFixture("zcc_device_cleanup", async (fixture) => {
    const pullPath = path.join(
      fixture.workspace,
      "pulls",
      TENANT,
      "zcc_device_cleanup.json",
    );
    writeFileSync(
      pullPath,
      JSON.stringify([{
        id: "device-1",
        active: "1",
        futureSecret: "parity-secret-value",
      }]),
    );
    const compiled = await candidate(fixture);
    assert.equal(compiled.status, "review_required");
    materialize(fixture, compiled);

    const report = await compareZccPullArtifactsOperation(
      operationOptions(fixture),
    );
    assert.equal(
      validateZccPullArtifactParity(report),
      true,
      JSON.stringify(validateZccPullArtifactParity.errors),
    );
    assert.equal(report.parity.status, "equal");
    assert.equal(report.status, "review_required");
    assert.equal(report.candidate.status, "review_required");
    assert.deepEqual(report.candidate.unexpected_drops, ["future_secret"]);
    assert.equal(JSON.stringify(report).includes("parity-secret-value"), false);
  });
});

test("compare policy refuses unsupported bootstrap-adjacent artifacts", async (t) => {
  const cases = [
    {
      name: "moves",
      relativePath: `imports/${TENANT}/zcc_device_cleanup_moves.tf`,
      code: "UNSUPPORTED_COMPARE_MOVES",
    },
    {
      name: "HCL alternate",
      relativePath: `config/${TENANT}/zcc_device_cleanup.auto.tfvars`,
      code: "UNSUPPORTED_COMPARE_HCL_ARTIFACT",
    },
    {
      name: "generated bindings",
      relativePath: `config/${TENANT}/zcc_device_cleanup.generated.expressions.json`,
      code: "UNSUPPORTED_COMPARE_GENERATED_BINDINGS",
    },
    {
      name: "stale lookup",
      relativePath: `config/${TENANT}/zcc_device_cleanup.lookup.json`,
      code: "UNSUPPORTED_COMPARE_LOOKUP_ARTIFACT",
    },
  ] as const;
  for (const fixtureCase of cases) {
    await t.test(fixtureCase.name, async () => {
      await withFixture("zcc_device_cleanup", async (fixture) => {
        const compiled = await candidate(fixture);
        materialize(fixture, compiled);
        const unsupported = path.join(fixture.workspace, fixtureCase.relativePath);
        mkdirSync(path.dirname(unsupported), { recursive: true });
        writeFileSync(unsupported, "parity-secret-value\n");
        await expectFailure(
          compareZccPullArtifactsOperation(operationOptions(fixture)),
          fixtureCase.code,
        );
      });
    });
  }
});

test("compare policy reuses HCL and same-root binding refusals", async (t) => {
  await t.test("HCL deployment", async () => {
    await withFixture(
      "zcc_device_cleanup",
      async (fixture) => {
        await expectFailure(
          compareZccPullArtifactsOperation(operationOptions(fixture)),
          "UNSUPPORTED_TFVARS_FORMAT",
        );
      },
      { overlay: ".", tfvars_format: "hcl", roots: {} },
    );
  });

  await t.test("same-root generated reference bindings", async () => {
    await withFixture(
      "zcc_forwarding_profile",
      async (fixture) => {
        await expectFailure(
          compareZccPullArtifactsOperation(operationOptions(fixture)),
          "UNSUPPORTED_GROUP_BINDINGS",
        );
      },
      {
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
      },
    );
  });
});

test("trusted-network comparison preserves lexical root overlays", async () => {
  for (const prefix of ["/", "//", "///"]) {
    await withFixture(
      "zcc_trusted_network",
      async (fixture) => {
        const report = await compareZccPullArtifactsOperation(
          operationOptions(fixture),
        );
        assert.equal(report.status, "review_required", prefix);
        assert.equal(report.parity.status, "different", prefix);
        assert.equal(report.parity.missing, 3, prefix);
        assert.equal(
          report.parity.artifacts.tfvars.path,
          `${prefix}config/${TENANT}/zcc_trusted_network.auto.tfvars.json`,
        );
        assert.equal(
          report.parity.artifacts.imports.path,
          `${prefix}imports/${TENANT}/zcc_trusted_network_imports.tf`,
        );
        assert.equal(
          report.parity.artifacts.lookup.path,
          `${prefix}config/${TENANT}/zcc_trusted_network.lookup.json`,
        );
      },
      { overlay: prefix, roots: {} },
    );
  }
});

test("comparison rejects symlinks and transaction races", async (t) => {
  await t.test("materialized symlink", async () => {
    await withFixture("zcc_device_cleanup", async (fixture) => {
      const compiled = await candidate(fixture);
      materialize(fixture, compiled);
      const tfvars = absoluteArtifact(fixture, compiled.artifacts.tfvars);
      const target = path.join(fixture.workspace, "symlink-target");
      writeFileSync(target, compiled.artifacts.tfvars.content);
      unlinkSync(tfvars);
      symlinkSync(target, tfvars);
      await expectFailure(
        compareZccPullArtifactsOperation(operationOptions(fixture)),
        "COMPARE_ARTIFACT_READ_FAILED",
      );
    });
  });

  await t.test("present artifact mutation", async () => {
    await withFixture("zcc_device_cleanup", async (fixture) => {
      const compiled = await candidate(fixture);
      materialize(fixture, compiled);
      await expectFailure(compareZccPullArtifactsOperation(operationOptions(
        fixture,
        {
          beforeFinalRecheck: () => {
            writeFileSync(
              absoluteArtifact(fixture, compiled.artifacts.tfvars),
              "parity-secret-value\n",
            );
          },
        },
      )), "COMPARE_ARTIFACT_CHANGED");
    });
  });

  await t.test("identical-byte artifact replacement", async () => {
    await withFixture("zcc_device_cleanup", async (fixture) => {
      const compiled = await candidate(fixture);
      materialize(fixture, compiled);
      await expectFailure(compareZccPullArtifactsOperation(operationOptions(
        fixture,
        {
          beforeFinalRecheck: () => {
            const target = absoluteArtifact(fixture, compiled.artifacts.tfvars);
            const replacement = `${target}.replacement`;
            writeFileSync(replacement, compiled.artifacts.tfvars.content);
            renameSync(replacement, target);
          },
        },
      )), "COMPARE_ARTIFACT_CHANGED");
    });
  });

  await t.test("missing artifact appears", async () => {
    await withFixture("zcc_device_cleanup", async (fixture) => {
      const compiled = await candidate(fixture);
      materialize(fixture, compiled, ["tfvars"]);
      await expectFailure(compareZccPullArtifactsOperation(operationOptions(
        fixture,
        {
          beforeFinalRecheck: () => {
            writeArtifact(fixture, compiled.artifacts.imports);
          },
        },
      )), "COMPARE_ARTIFACT_CHANGED");
    });
  });
});
