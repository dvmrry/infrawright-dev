import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  readdirSync,
  renameSync,
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
  validateZccPullRefreshMaterialization,
  validateZccPullRefreshPendingTransition,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import { zccRefreshEvidenceDigest } from "../node-src/domain/zcc-pull-refresh-fingerprints.js";
import {
  type ZccPullRefreshMaterialization,
} from "../node-src/domain/zcc-pull-refresh-materialization.js";
import {
  compareZccPullRefreshParityOperation,
  seedZccPullRefreshParityOperation,
  type ZccPullRefreshParity,
} from "../node-src/domain/zcc-pull-refresh-parity.js";
import {
  materializeZccPullRefreshOperation,
  type ZccPullRefreshPublisherOperationHooks,
} from "../node-src/domain/zcc-pull-refresh-publisher-operation.js";
import type { ProcessResponse } from "../node-src/process/types.js";

const REPOSITORY = process.cwd();
const PROCESS_MAIN = path.join(
  REPOSITORY,
  ".node-test/node-src/process/main.js",
);
const ROOT_CATALOG = path.join(
  REPOSITORY,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "refresh_materialize";
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

type ArtifactRole =
  | "tfvars"
  | "imports"
  | "lookup"
  | "moves"
  | "pending_moves"
  | "alternate_hcl"
  | "generated_bindings";

interface Twin {
  readonly workspace: string;
  readonly deployment: string;
  readonly catalog: string;
}

interface Scenario {
  readonly candidate: Twin;
  readonly reference: Twin;
  readonly resourceType: ZccPullResourceType;
  readonly assertion: ZccPullRefreshParity;
  readonly candidateBaseline: Readonly<Record<ArtifactRole, Buffer | null>>;
  readonly referenceFinal: Readonly<Record<ArtifactRole, Buffer | null>>;
}

function twin(prefix: string): Twin {
  const workspace = realpathSync(mkdtempSync(path.join(os.tmpdir(), `${prefix}-`)));
  const deployment = path.join(workspace, "deployment.json");
  const catalog = path.join(workspace, "catalog.json");
  writeFileSync(deployment, '{"overlay":".","roots":{}}\n');
  copyFileSync(ROOT_CATALOG, catalog);
  return { workspace, deployment, catalog };
}

function context(value: Twin) {
  return {
    workspace: value.workspace,
    deployment: "deployment.json",
    root_catalog: "catalog.json",
  } as const;
}

function pullPath(value: Twin, resourceType: ZccPullResourceType): string {
  return path.join(value.workspace, "pulls", TENANT, `${resourceType}.json`);
}

function raw(resourceType: ZccPullResourceType): string {
  return resourceType === "zcc_device_cleanup"
    ? '[{"active":"1","id":"device-1"}]\n'
    : readFileSync(
        path.join(REPOSITORY, `tests/fixtures/demo/${resourceType}.json`),
        "utf8",
      );
}

function writePull(
  value: Twin,
  resourceType: ZccPullResourceType,
  contents: string | readonly unknown[],
): void {
  const destination = pullPath(value, resourceType);
  mkdirSync(path.dirname(destination), { recursive: true });
  writeFileSync(
    destination,
    typeof contents === "string" ? contents : `${JSON.stringify(contents)}\n`,
  );
}

function runPython(value: Twin, resourceType: ZccPullResourceType): void {
  const pythonPath = process.env.PYTHONPATH;
  const run = spawnSync(
    "python3",
    ["-m", "engine.transform", resourceType, pullPath(value, resourceType), TENANT],
    {
      cwd: value.workspace,
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
      env: {
        ...process.env,
        INFRAWRIGHT_DEPLOYMENT: value.deployment,
        PYTHONPATH: pythonPath === undefined || pythonPath.length === 0
          ? REPOSITORY
          : `${REPOSITORY}${path.delimiter}${pythonPath}`,
      },
    },
  );
  assert.equal(run.signal, null, run.error?.message);
  assert.equal(run.status, 0, run.stderr);
}

function artifactPaths(
  value: Twin,
  resourceType: ZccPullResourceType,
): Readonly<Record<ArtifactRole, string>> {
  const imports = path.join(
    value.workspace,
    "imports",
    TENANT,
    `${resourceType}_imports.tf`,
  );
  const tfvars = path.join(
    value.workspace,
    "config",
    TENANT,
    `${resourceType}.auto.tfvars.json`,
  );
  return {
    tfvars,
    imports,
    lookup: path.join(
      value.workspace,
      "config",
      TENANT,
      `${resourceType}.lookup.json`,
    ),
    moves: imports.slice(0, -"_imports.tf".length) + "_moves.tf",
    pending_moves: imports.slice(0, -"_imports.tf".length)
      + "_moves.pending.json",
    alternate_hcl: tfvars.slice(0, -".json".length),
    generated_bindings: path.join(
      value.workspace,
      "config",
      TENANT,
      `${resourceType}.generated.expressions.json`,
    ),
  };
}

function artifactSnapshot(
  value: Twin,
  resourceType: ZccPullResourceType,
): Readonly<Record<ArtifactRole, Buffer | null>> {
  const result = {} as Record<ArtifactRole, Buffer | null>;
  const paths = artifactPaths(value, resourceType);
  for (const role of Object.keys(paths) as ArtifactRole[]) {
    result[role] = existsSync(paths[role]) ? readFileSync(paths[role]) : null;
  }
  return result;
}

function tempAliases(root: string): readonly string[] {
  const found: string[] = [];
  const visit = (directory: string): void => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      if (entry.name.startsWith(".infrawright-") && entry.name.endsWith(".tmp")) {
        found.push(absolute);
      }
      if (entry.isDirectory() && !entry.isSymbolicLink()) {
        visit(absolute);
      }
    }
  };
  visit(root);
  return found.sort();
}

async function prepareScenario(
  resourceType: ZccPullResourceType,
  baseline: string | readonly unknown[],
  next: string | readonly unknown[],
): Promise<Scenario> {
  const candidate = twin(`zcc-refresh-publish-c-${resourceType}`);
  const reference = twin(`zcc-refresh-publish-r-${resourceType}`);
  try {
    writePull(candidate, resourceType, baseline);
    writePull(reference, resourceType, baseline);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    const candidateBaseline = artifactSnapshot(candidate, resourceType);

    writePull(candidate, resourceType, next);
    writePull(reference, resourceType, next);
    const seed = await seedZccPullRefreshParityOperation({
      context: context(candidate),
      referenceContext: context(reference),
      tenant: TENANT,
      resourceType,
    });
    assert.equal(seed.status, "ready");
    runPython(reference, resourceType);
    const assertion = await compareZccPullRefreshParityOperation({
      context: context(candidate),
      referenceContext: context(reference),
      tenant: TENANT,
      resourceType,
      seed,
    });
    assert.equal(assertion.status, "ready");
    assert.equal(assertion.parity.status, "equal");
    return {
      candidate,
      reference,
      resourceType,
      assertion,
      candidateBaseline,
      referenceFinal: artifactSnapshot(reference, resourceType),
    };
  } catch (error: unknown) {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
    throw error;
  }
}

function cleanupScenario(scenario: Scenario): void {
  rmSync(scenario.candidate.workspace, { recursive: true, force: true });
  rmSync(scenario.reference.workspace, { recursive: true, force: true });
}

function publish(
  scenario: Scenario,
  hooks?: ZccPullRefreshPublisherOperationHooks,
): Promise<ZccPullRefreshMaterialization> {
  return materializeZccPullRefreshOperation({
    context: context(scenario.candidate),
    tenant: TENANT,
    resourceType: scenario.resourceType,
    assertion: scenario.assertion,
    outputRoot: scenario.candidate.workspace,
    ...(hooks === undefined ? {} : { hooks }),
  });
}

function refreshMaterializeRequest(scenario: Scenario) {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `refresh-materialize-${scenario.resourceType}`,
    operation: "materialize_pull_artifacts",
    context: context(scenario.candidate),
    input: {
      mode: "refresh",
      publication: "replace_or_verify_exact_imports_last",
      tenant: TENANT,
      resource_type: scenario.resourceType,
      assertion: scenario.assertion,
    },
  } as const;
}

function invokeHost(
  scenario: Scenario,
): { readonly status: number | null; readonly stderr: string; readonly stdout: string; readonly response: ProcessResponse } {
  const request = refreshMaterializeRequest(scenario);
  assert.equal(
    validateProcessRequest(request),
    true,
    JSON.stringify(validateProcessRequest.errors),
  );
  const run = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: REPOSITORY,
    input: JSON.stringify(request),
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    env: {
      ...process.env,
      INFRAWRIGHT_TERRAFORM_EXECUTABLE: "",
      INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: scenario.candidate.workspace,
    },
  });
  assert.equal(run.signal, null, run.error?.message);
  assert.doesNotThrow(() => JSON.parse(run.stdout), run.stderr);
  const response = JSON.parse(run.stdout) as ProcessResponse;
  assert.equal(
    validateProcessResponse(response),
    true,
    JSON.stringify(validateProcessResponse.errors),
  );
  return {
    status: run.status,
    stderr: run.stderr,
    stdout: run.stdout,
    response,
  };
}

async function expectFailure(
  operation: Promise<unknown>,
): Promise<ProcessFailure> {
  try {
    await operation;
  } catch (error: unknown) {
    assert.ok(error instanceof ProcessFailure);
    return error;
  }
  assert.fail("expected refresh materialization failure");
}

test("refresh publisher reproduces all five Python no-move finals byte for byte", async () => {
  for (const resourceType of RESOURCES) {
    const scenario = await prepareScenario(resourceType, [], raw(resourceType));
    try {
      const result = await publish(scenario);
      assert.equal(
        validateZccPullRefreshMaterialization(result),
        true,
        JSON.stringify(validateZccPullRefreshMaterialization.errors),
      );
      assert.equal(result.status, "complete", resourceType);
      assert.equal(result.transition.final, "already_complete", resourceType);
      assert.equal(result.transition.next_action, "none", resourceType);
      assert.equal(result.publication.advanced.at(-1), "imports", resourceType);
      assert.deepEqual(
        artifactSnapshot(scenario.candidate, resourceType),
        scenario.referenceFinal,
        resourceType,
      );
      assert.deepEqual(tempAliases(scenario.candidate.workspace), []);
    } finally {
      cleanupScenario(scenario);
    }
  }
});

test("an unchanged imports role is verified as the fence without being rewritten", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(
    resourceType,
    [{ active: "1", id: "device-1" }],
    [{ active: "0", id: "device-1" }],
  );
  try {
    assert.deepEqual(
      scenario.candidateBaseline.imports,
      scenario.referenceFinal.imports,
    );
    const before = lstatSync(artifactPaths(scenario.candidate, resourceType).imports, {
      bigint: true,
    });
    const result = await publish(scenario);
    const after = lstatSync(artifactPaths(scenario.candidate, resourceType).imports, {
      bigint: true,
    });
    assert.equal(result.status, "complete");
    assert.equal(result.publication.advanced.includes("imports"), false);
    assert.equal(before.dev, after.dev);
    assert.equal(before.ino, after.ino);
    assert.deepEqual(
      artifactSnapshot(scenario.candidate, resourceType),
      scenario.referenceFinal,
    );
  } finally {
    cleanupScenario(scenario);
  }
});

test("a safe rename retains the exact move and marker and returns awaiting_apply", async () => {
  const resourceType = "zcc_forwarding_profile";
  const scenario = await prepareScenario(
    resourceType,
    [{ id: "rename-id", name: "Before" }],
    [{ id: "rename-id", name: "After" }],
  );
  try {
    assert.equal(scenario.assertion.candidate.moves.safe_count, 1);
    const result = await publish(scenario);
    assert.equal(result.status, "awaiting_apply");
    assert.equal(result.transition.final, "committed");
    assert.equal(result.transition.next_action, "apply_moves_then_ack");
    assert.equal(result.publication.advanced.at(-1), "imports");
    const paths = artifactPaths(scenario.candidate, resourceType);
    assert.deepEqual(readFileSync(paths.moves), scenario.referenceFinal.moves);
    const marker = JSON.parse(readFileSync(paths.pending_moves, "utf8")) as unknown;
    assert.equal(
      validateZccPullRefreshPendingTransition(marker),
      true,
      JSON.stringify(validateZccPullRefreshPendingTransition.errors),
    );
    assert.equal(JSON.stringify(marker).includes("rename-id"), false);
    assert.equal(JSON.stringify(marker).includes("Before"), false);
    assert.equal(JSON.stringify(marker).includes("After"), false);

    const markerBytes = readFileSync(paths.pending_moves, "utf8");
    const retry = await publish(scenario);
    assert.equal(retry.status, "awaiting_apply");
    assert.deepEqual(retry.publication.advanced, []);
    assert.equal(readFileSync(paths.pending_moves, "utf8"), markerBytes);
  } finally {
    cleanupScenario(scenario);
  }
});

test("refresh process request semantics bind context coordinates to the assertion", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const request = {
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "refresh-materialize-request-join",
      operation: "materialize_pull_artifacts",
      context: context(scenario.candidate),
      input: {
        mode: "refresh",
        publication: "replace_or_verify_exact_imports_last",
        tenant: TENANT,
        resource_type: resourceType,
        assertion: scenario.assertion,
      },
    } as const;
    assert.equal(
      validateProcessRequest(request),
      true,
      JSON.stringify(validateProcessRequest.errors),
    );
    const replay = structuredClone(request) as unknown as {
      context: { deployment: string };
    };
    replay.context.deployment = "./deployment.json";
    assert.equal(validateProcessRequest(replay), false);
    assert.ok((validateProcessRequest.errors ?? []).some((error) => {
      const params = error.params as { readonly rule?: unknown };
      return params.rule === "request_hash_join";
    }));
  } finally {
    cleanupScenario(scenario);
  }
});

test("every durable crash prefix retries forward in imports-last order", async () => {
  const resourceType = "zcc_trusted_network";
  const boundaries = [
    { name: "marker", remaining: ["lookup", "tfvars", "imports"] },
    { name: "lookup", remaining: ["tfvars", "imports"] },
    { name: "tfvars", remaining: ["imports"] },
    { name: "imports", remaining: [] },
  ] as const;
  for (const boundary of boundaries) {
    const scenario = await prepareScenario(resourceType, [], raw(resourceType));
    try {
      let fired = false;
      const hooks: ZccPullRefreshPublisherOperationHooks = boundary.name === "marker"
        ? {
            afterMarkerSync: () => {
              if (!fired) {
                fired = true;
                throw new Error("private crash marker");
              }
            },
          }
        : {
            afterPublishParentSync: (role) => {
              if (!fired && role === boundary.name) {
                fired = true;
                throw new Error("private crash payload");
              }
            },
          };
      const error = await expectFailure(publish(scenario, hooks));
      assert.equal(error.code, "REFRESH_MATERIALIZATION_INDETERMINATE", boundary.name);
      assert.equal(error.retryable, true, boundary.name);
      assert.equal(error.message.includes("private crash"), false, boundary.name);
      assert.equal(fired, true, boundary.name);
      assert.deepEqual(tempAliases(scenario.candidate.workspace), [], boundary.name);

      const retry = await publish(scenario);
      assert.equal(retry.status, "complete", boundary.name);
      assert.deepEqual(retry.publication.advanced, boundary.remaining, boundary.name);
      assert.deepEqual(
        artifactSnapshot(scenario.candidate, resourceType),
        scenario.referenceFinal,
        boundary.name,
      );
    } finally {
      cleanupScenario(scenario);
    }
  }
});

test("foreign marker, reserved artifacts, and markerless early imports fail without clobber", async () => {
  const resourceType = "zcc_device_cleanup";
  for (const kind of [
    "foreign-marker",
    "alternate-hcl",
    "generated-bindings",
    "early-imports",
  ] as const) {
    const scenario = await prepareScenario(resourceType, [], raw(resourceType));
    try {
      const paths = artifactPaths(scenario.candidate, resourceType);
      let protectedPath: string;
      let protectedBytes: Buffer;
      if (kind === "foreign-marker") {
        protectedPath = paths.pending_moves;
        protectedBytes = Buffer.from("foreign-marker-private-value\n");
      } else if (kind === "alternate-hcl") {
        protectedPath = paths.alternate_hcl;
        protectedBytes = Buffer.from("reserved-hcl-private-value\n");
      } else if (kind === "generated-bindings") {
        protectedPath = paths.generated_bindings;
        protectedBytes = Buffer.from("reserved-bindings-private-value\n");
      } else {
        protectedPath = paths.imports;
        const desiredImports = scenario.referenceFinal.imports;
        assert.notEqual(desiredImports, null);
        protectedBytes = desiredImports as Buffer;
      }
      mkdirSync(path.dirname(protectedPath), { recursive: true });
      writeFileSync(protectedPath, protectedBytes);
      const error = await expectFailure(publish(scenario));
      assert.equal(error.code, "AMBIGUOUS_REFRESH_MATERIALIZATION_STATE", kind);
      assert.equal(readFileSync(protectedPath).equals(protectedBytes), true, kind);
      assert.deepEqual(tempAliases(scenario.candidate.workspace), [], kind);
    } finally {
      cleanupScenario(scenario);
    }
  }
});

test("concurrent marker creation is never replaced", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const markerPath = artifactPaths(scenario.candidate, resourceType).pending_moves;
    const foreign = "concurrent-marker-private-value\n";
    const error = await expectFailure(publish(scenario, {
      beforeMarkerLink: () => {
        writeFileSync(markerPath, foreign);
      },
    }));
    assert.equal(error.code, "REFRESH_MATERIALIZATION_TARGET_CHANGED");
    assert.equal(readFileSync(markerPath, "utf8"), foreign);
    assert.deepEqual(tempAliases(scenario.candidate.workspace), []);
    assert.deepEqual(
      artifactSnapshot(scenario.candidate, resourceType).tfvars,
      scenario.candidateBaseline.tfvars,
    );
  } finally {
    cleanupScenario(scenario);
  }
});

test("marker replacement immediately before removal is detected and never unlinked", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const markerPath = artifactPaths(scenario.candidate, resourceType).pending_moves;
    const foreign = "replacement-marker-private-value\n";
    const error = await expectFailure(publish(scenario, {
      beforeMarkerRemove: () => {
        unlinkSync(markerPath);
        writeFileSync(markerPath, foreign);
      },
    }));
    assert.equal(error.code, "REFRESH_MATERIALIZATION_INDETERMINATE");
    assert.equal(error.retryable, true);
    assert.equal(readFileSync(markerPath, "utf8"), foreign);
    assert.deepEqual(tempAliases(scenario.candidate.workspace), []);
  } finally {
    cleanupScenario(scenario);
  }
});

test("pre-fence failure removes all staging aliases and preserves the baseline", async () => {
  const resourceType = "zcc_trusted_network";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    let staged = false;
    const error = await expectFailure(publish(scenario, {
      afterStage: () => {
        if (!staged) {
          staged = true;
          throw new Error("staging-private-value");
        }
      },
    }));
    assert.equal(error.code, "REFRESH_MATERIALIZATION_HOOK_FAILED");
    assert.equal(error.message.includes("staging-private-value"), false);
    assert.equal(staged, true);
    assert.deepEqual(tempAliases(scenario.candidate.workspace), []);
    assert.deepEqual(
      artifactSnapshot(scenario.candidate, resourceType),
      scenario.candidateBaseline,
    );
  } finally {
    cleanupScenario(scenario);
  }
});

test("mutated or replaced staging aliases can never become canonical artifacts", async () => {
  const resourceType = "zcc_device_cleanup";
  for (const kind of ["mutate-after-stage", "replace-before-link"] as const) {
    const scenario = await prepareScenario(resourceType, [], raw(resourceType));
    let escaped = "";
    try {
      const privateBytes = `${kind}-private-value\n`;
      let changed = false;
      const alterAlias = (): void => {
        if (changed) {
          return;
        }
        changed = true;
        const aliases = tempAliases(scenario.candidate.workspace);
        assert.ok(aliases.length > 0);
        const alias = kind === "replace-before-link"
          ? aliases.find((candidate) => {
              return readFileSync(candidate, "utf8").includes(
                "infrawright.zcc_pull_refresh_pending_transition",
              );
            })
          : aliases[0];
        assert.notEqual(alias, undefined);
        const selected = alias as string;
        if (kind === "replace-before-link") {
          escaped = `${selected}.escaped`;
          renameSync(selected, escaped);
        }
        writeFileSync(selected, privateBytes);
      };
      const error = await expectFailure(publish(scenario, kind === "mutate-after-stage"
        ? { afterStage: alterAlias }
        : { beforeMarkerLink: alterAlias }));
      assert.ok(new Set([
        "REFRESH_MATERIALIZATION_STAGE_CHANGED",
        "REFRESH_MATERIALIZATION_TARGET_CHANGED",
        "REFRESH_MATERIALIZATION_CLEANUP_FAILED",
      ]).has(error.code), `${kind}: ${error.code}`);
      const paths = artifactPaths(scenario.candidate, resourceType);
      assert.equal(existsSync(paths.pending_moves), false, kind);
      assert.deepEqual(
        artifactSnapshot(scenario.candidate, resourceType),
        scenario.candidateBaseline,
        kind,
      );
      for (const canonical of Object.values(paths)) {
        if (existsSync(canonical)) {
          assert.equal(readFileSync(canonical, "utf8").includes(privateBytes.trim()), false);
        }
      }
    } finally {
      if (escaped !== "") {
        rmSync(escaped, { force: true });
      }
      for (const alias of tempAliases(scenario.candidate.workspace)) {
        rmSync(alias, { force: true });
      }
      cleanupScenario(scenario);
    }
  }
});

test("pre-sync crash hooks remain retry-forward at every durable syscall boundary", async () => {
  const resourceType = "zcc_trusted_network";
  const boundaries = [
    { name: "after-marker-link", remaining: ["lookup", "tfvars", "imports"] },
    { name: "after-publish", remaining: ["tfvars", "imports"] },
    { name: "after-marker-remove", remaining: [] },
  ] as const;
  for (const boundary of boundaries) {
    const scenario = await prepareScenario(resourceType, [], raw(resourceType));
    try {
      let fired = false;
      const hooks: ZccPullRefreshPublisherOperationHooks =
        boundary.name === "after-marker-link"
          ? {
              afterMarkerLink: () => {
                if (!fired) {
                  fired = true;
                  throw new Error("pre-sync-marker-private-value");
                }
              },
            }
          : boundary.name === "after-publish"
            ? {
                afterPublish: (role) => {
                  if (!fired && role === "lookup") {
                    fired = true;
                    throw new Error("pre-sync-payload-private-value");
                  }
                },
              }
            : {
                afterMarkerRemove: () => {
                  if (!fired) {
                    fired = true;
                    throw new Error("post-remove-private-value");
                  }
                },
              };
      const error = await expectFailure(publish(scenario, hooks));
      assert.equal(error.code, "REFRESH_MATERIALIZATION_INDETERMINATE", boundary.name);
      assert.equal(error.retryable, true, boundary.name);
      assert.equal(fired, true, boundary.name);
      assert.deepEqual(tempAliases(scenario.candidate.workspace), [], boundary.name);
      const retry = await publish(scenario);
      assert.equal(retry.status, "complete", boundary.name);
      assert.deepEqual(retry.publication.advanced, boundary.remaining, boundary.name);
    } finally {
      cleanupScenario(scenario);
    }
  }
});

test("a retry with an exact pre-existing marker survives a post-publish failure", async () => {
  const resourceType = "zcc_trusted_network";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const first = await expectFailure(publish(scenario, {
      afterMarkerSync: () => {
        throw new Error("first-private-value");
      },
    }));
    assert.equal(first.code, "REFRESH_MATERIALIZATION_INDETERMINATE");
    const markerPath = artifactPaths(scenario.candidate, resourceType).pending_moves;
    const markerBytes = readFileSync(markerPath);

    let failedAfterPublish = false;
    const second = await expectFailure(publish(scenario, {
      afterPublish: (role) => {
        if (!failedAfterPublish && role === "lookup") {
          failedAfterPublish = true;
          throw new Error("second-private-value");
        }
      },
    }));
    assert.equal(second.code, "REFRESH_MATERIALIZATION_INDETERMINATE");
    assert.equal(second.retryable, true);
    assert.equal(readFileSync(markerPath).equals(markerBytes), true);

    const retry = await publish(scenario);
    assert.equal(retry.status, "complete");
    assert.deepEqual(retry.publication.advanced, ["tfvars", "imports"]);
    assert.deepEqual(
      artifactSnapshot(scenario.candidate, resourceType),
      scenario.referenceFinal,
    );
  } finally {
    cleanupScenario(scenario);
  }
});

test("desired-import retry retains move evidence instead of rederiving it away", async () => {
  const resourceType = "zcc_forwarding_profile";
  const scenario = await prepareScenario(
    resourceType,
    [{ id: "rename-id", name: "Before" }],
    [{ id: "rename-id", name: "After" }],
  );
  try {
    let crashed = false;
    const error = await expectFailure(publish(scenario, {
      afterPublishParentSync: (role) => {
        if (!crashed && role === "imports") {
          crashed = true;
          throw new Error("desired-import-private-value");
        }
      },
    }));
    assert.equal(error.code, "REFRESH_MATERIALIZATION_INDETERMINATE");
    const paths = artifactPaths(scenario.candidate, resourceType);
    const moveBytes = readFileSync(paths.moves);
    const markerBytes = readFileSync(paths.pending_moves);
    const retry = await publish(scenario);
    assert.equal(retry.status, "awaiting_apply");
    assert.deepEqual(retry.publication.advanced, []);
    assert.equal(readFileSync(paths.moves).equals(moveBytes), true);
    assert.equal(readFileSync(paths.pending_moves).equals(markerBytes), true);
  } finally {
    cleanupScenario(scenario);
  }
});

test("inert snapshot rejects a rotating assertion getter before operation I/O", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const assertion = structuredClone(scenario.assertion);
    const differences = assertion.seed.differences as string[];
    let getterCalls = 0;
    Object.defineProperty(differences, "0", {
      configurable: true,
      enumerable: true,
      get: () => {
        getterCalls += 1;
        return getterCalls % 2 === 0 ? "source" : "catalog";
      },
    });
    differences.length = 1;
    let afterInputsBound = 0;
    const error = await expectFailure(materializeZccPullRefreshOperation({
      context: context(scenario.candidate),
      tenant: TENANT,
      resourceType,
      assertion,
      outputRoot: scenario.candidate.workspace,
      hooks: {
        afterInputsBound: () => {
          afterInputsBound += 1;
        },
      },
    }));
    assert.equal(error.code, "INVALID_MATERIALIZATION_INPUT");
    assert.equal(getterCalls, 0);
    assert.equal(afterInputsBound, 0);
    assert.deepEqual(
      artifactSnapshot(scenario.candidate, resourceType),
      scenario.candidateBaseline,
    );
  } finally {
    cleanupScenario(scenario);
  }
});

test("outer assertion rehash cannot bless tampered nested transition evidence", async () => {
  const resourceType = "zcc_device_cleanup";
  const scenario = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const assertion = structuredClone(scenario.assertion) as unknown as {
      assertion_sha256: string;
      candidate: { transition_sha256: string };
      [key: string]: unknown;
    };
    assertion.candidate.transition_sha256 = "f".repeat(64);
    const { assertion_sha256: _ignored, ...withoutDigest } = assertion;
    assertion.assertion_sha256 = zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_parity_assertion_digest",
      schema_version: 1,
      assertion: withoutDigest,
    });
    let afterInputsBound = 0;
    const error = await expectFailure(materializeZccPullRefreshOperation({
      context: context(scenario.candidate),
      tenant: TENANT,
      resourceType,
      assertion: assertion as unknown as ZccPullRefreshParity,
      outputRoot: scenario.candidate.workspace,
      hooks: {
        afterInputsBound: () => {
          afterInputsBound += 1;
        },
      },
    }));
    assert.equal(error.code, "INVALID_MATERIALIZATION_ASSERTION");
    assert.equal(afterInputsBound, 0);
  } finally {
    cleanupScenario(scenario);
  }
});

test("process host routes refresh success and fail-closed workspace errors", async () => {
  const resourceType = "zcc_device_cleanup";
  const success = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const invocation = invokeHost(success);
    assert.equal(invocation.status, 0, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(invocation.response.status, "ok");
    assert.equal(
      invocation.response.result?.kind,
      "infrawright.zcc_pull_refresh_materialization",
    );
    assert.equal(
      (invocation.response.result as ZccPullRefreshMaterialization).status,
      "complete",
    );
    assert.deepEqual(
      artifactSnapshot(success.candidate, resourceType),
      success.referenceFinal,
    );
  } finally {
    cleanupScenario(success);
  }

  const failure = await prepareScenario(resourceType, [], raw(resourceType));
  try {
    const privateValue = "process-foreign-marker-private-value";
    const markerPath = artifactPaths(failure.candidate, resourceType).pending_moves;
    writeFileSync(markerPath, `${privateValue}\n`);
    const invocation = invokeHost(failure);
    assert.equal(invocation.status, 1, invocation.stdout);
    assert.equal(invocation.stderr, "");
    assert.equal(invocation.response.status, "error");
    assert.equal(
      invocation.response.error?.code,
      "AMBIGUOUS_REFRESH_MATERIALIZATION_STATE",
    );
    assert.equal(invocation.stdout.includes(privateValue), false);
    assert.equal(readFileSync(markerPath, "utf8"), `${privateValue}\n`);
  } finally {
    cleanupScenario(failure);
  }
});
