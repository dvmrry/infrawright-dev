import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
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
  validateZccPullCollection,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_RESOURCE_TYPES,
  type ZccCollectionResourceType,
} from "../node-src/domain/zcc-collection-contract.js";
import { collectZccPullOperation } from "../node-src/domain/zcc-pull-collection.js";
import { compileZccPullArtifactsOperation } from "../node-src/domain/zcc-pull-operation.js";
import { parseZccPullDataJson } from "../node-src/json/zcc-pull-data.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";
import type { ZccCollectionChildSuccessResponse } from "../node-src/io/zcc-collection-protocol.js";
import {
  prepareZccPullPublication,
  publishZccPull,
} from "../node-src/io/zcc-pull-publisher.js";

const REPOSITORY = process.cwd();
const TENANT = "collection_test";

function canonicalPull(resourceType: ZccCollectionResourceType): string {
  const source = resourceType === "zcc_device_cleanup"
    ? '[{"active":"1","id":"device-1"}]\n'
    : readFileSync(
        path.join(REPOSITORY, `tests/fixtures/demo/${resourceType}.json`),
        "utf8",
      );
  return renderPythonLosslessArtifactJson(parseZccPullDataJson(source));
}

function success(
  resourceType: ZccCollectionResourceType,
  canonical: string,
): ZccCollectionChildSuccessResponse {
  const bytes = Buffer.from(canonical, "utf8");
  return {
    kind: "infrawright.zcc_collection_child_response",
    schema_version: 1,
    status: "ok",
    artifact: {
      body_base64: bytes.toString("base64"),
      catalog_sources_sha256: ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
      data_requests: 1,
      item_count: parseZccPullDataJson(canonical).length,
      resource_type: resourceType,
      sha256: createHash("sha256").update(bytes).digest("hex"),
      size_bytes: bytes.length,
      transport_attempts: 1,
    },
  };
}

async function withWorkspace(
  callback: (workspace: string) => Promise<void>,
): Promise<void> {
  const lexical = mkdtempSync(path.join(os.tmpdir(), "zcc-collection-"));
  const workspace = realpathSync(lexical);
  try {
    writeFileSync(
      path.join(workspace, "deployment.json"),
      `${JSON.stringify({ overlay: ".", roots: {} })}\n`,
    );
    copyFileSync(
      path.join(REPOSITORY, "catalogs/zscaler-root-catalog.v1.json"),
      path.join(workspace, "catalog.json"),
    );
    await callback(workspace);
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

test("exact-five fake collection creates Python bytes consumed unchanged by the compiler", async () => {
  for (const resourceType of ZCC_COLLECTION_RESOURCE_TYPES) {
    await withWorkspace(async (workspace) => {
      const canonical = canonicalPull(resourceType);
      const receipt = await collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
        environment: {},
        collectChild: async () => success(resourceType, canonical),
      });
      assert.equal(receipt.publication.action, "created");
      assert.equal(receipt.artifact.path, `pulls/${TENANT}/${resourceType}.json`);
      assert.equal(validateZccPullCollection(receipt), true);
      assert.equal(
        readFileSync(path.join(workspace, receipt.artifact.path), "utf8"),
        canonical,
      );
      const compiled = await compileZccPullArtifactsOperation({
        workspace,
        deploymentPath: path.join(workspace, "deployment.json"),
        catalogPath: path.join(workspace, "catalog.json"),
        tenant: TENANT,
        resourceType,
      });
      assert.equal(compiled.source.sha256, receipt.artifact.sha256);
      assert.equal(compiled.source.size_bytes, receipt.artifact.size_bytes);
    });
  }
});

test("single-file publication reuses exact bytes and atomically replaces different bytes", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const first = canonicalPull(resourceType);
    const run = async (canonical: string) => collectZccPullOperation({
      workspace,
      outputRoot: workspace,
      tenant: TENANT,
      resourceType,
      environment: {},
      collectChild: async () => success(resourceType, canonical),
    });
    assert.equal((await run(first)).publication.action, "created");
    assert.equal((await run(first)).publication.action, "reused");
    const replacement = renderPythonLosslessArtifactJson([
      { id: "replacement", enabled: true },
    ]);
    assert.equal((await run(replacement)).publication.action, "replaced");
    assert.equal(
      readFileSync(path.join(workspace, "pulls", TENANT, `${resourceType}.json`), "utf8"),
      replacement,
    );
  });
});

test("child and protocol failures occur before the publisher guard and preserve prior pull", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const directory = path.join(workspace, "pulls", TENANT);
    const target = path.join(directory, `${resourceType}.json`);
    mkdirSync(directory, { recursive: true });
    writeFileSync(target, "prior\n");
    await assert.rejects(
      collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
        environment: {},
        collectChild: async () => {
          throw new ProcessFailure({
            code: "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
            category: "io",
            message: "deadline",
            retryable: true,
          });
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    );
    assert.equal(readFileSync(target, "utf8"), "prior\n");
    assert.equal(
      (() => {
        try { readFileSync(path.join(workspace, ".infrawright.publisher.lock")); return true; }
        catch { return false; }
      })(),
      false,
    );
  });
});

test("special targets and cooperating-writer contention fail closed", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const canonical = canonicalPull(resourceType);
    const directory = path.join(workspace, "pulls", TENANT);
    const target = path.join(directory, `${resourceType}.json`);
    mkdirSync(directory, { recursive: true });
    const referent = path.join(workspace, "foreign.json");
    writeFileSync(referent, canonical);
    symlinkSync(referent, target);
    await assert.rejects(
      collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
        environment: {},
        collectChild: async () => success(resourceType, canonical),
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_PULL_PUBLICATION_TARGET_INVALID",
    );
    rmSync(target);
    writeFileSync(path.join(workspace, ".infrawright.publisher.lock"), "busy");
    await assert.rejects(
      collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
        environment: {},
        collectChild: async () => success(resourceType, canonical),
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "OUTPUT_ROOT_BUSY" && error.retryable,
    );
  });
});

test("workspace rollover during the child call cannot redirect publication", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const canonical = canonicalPull(resourceType);
    const moved = `${workspace}-original`;
    await assert.rejects(
      collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
        environment: {},
        collectChild: async () => {
          const { renameSync } = await import("node:fs");
          renameSync(workspace, moved);
          mkdirSync(workspace);
          return success(resourceType, canonical);
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_PULL_OUTPUT_ROOT_CHANGED",
    );
    assert.equal(
      (() => {
        try {
          readFileSync(path.join(workspace, "pulls", TENANT, `${resourceType}.json`));
          return true;
        } catch { return false; }
      })(),
      false,
    );
    assert.deepEqual(readdirSync(workspace), []);
    rmSync(workspace, { recursive: true, force: true });
    const { renameSync } = await import("node:fs");
    renameSync(moved, workspace);
  });
});

test("receipt semantic contract rejects a foreign digest and cross-resource path", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const receipt = await collectZccPullOperation({
      workspace,
      outputRoot: workspace,
      tenant: TENANT,
      resourceType,
      environment: {},
      collectChild: async () => success(resourceType, canonicalPull(resourceType)),
    });
    assert.equal(validateZccPullCollection(receipt), true);
    assert.equal(validateZccPullCollection({
      ...receipt,
      catalog_sources_sha256: "0".repeat(64),
    }), false);
    assert.equal(validateZccPullCollection({
      ...receipt,
      artifact: { ...receipt.artifact, path: `pulls/${TENANT}/zcc_device_cleanup.json` },
    }), false);
  });
});

test("process schemas close the collection request and join the receipt operation", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const request = {
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "collection-schema",
      operation: "collect_zcc_pull",
      context: {
        workspace,
        deployment: "unused.json",
        root_catalog: "unused.json",
      },
      input: {
        mode: "oneapi",
        publication: "replace_or_verify_exact",
        tenant: TENANT,
        resource_type: resourceType,
      },
    };
    assert.equal(validateProcessRequest(request), true);
    assert.equal(validateProcessRequest({
      ...request,
      input: { ...request.input, output_path: "/tmp/hostile" },
    }), false);
    assert.equal(validateProcessRequest({
      ...request,
      input: { ...request.input, mode: "legacy" },
    }), false);
    const result = await collectZccPullOperation({
      workspace,
      outputRoot: workspace,
      tenant: TENANT,
      resourceType,
      environment: {},
      collectChild: async () => success(resourceType, canonicalPull(resourceType)),
    });
    assert.equal(validateProcessResponse({
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "collect_zcc_pull",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    }), true);
    assert.equal(validateProcessResponse({
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "roots",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    }), false);
  });
});

test("overlong tenant fails before child or filesystem publication", async () => {
  await withWorkspace(async (workspace) => {
    let childCalls = 0;
    await assert.rejects(
      collectZccPullOperation({
        workspace,
        outputRoot: workspace,
        tenant: "a".repeat(256),
        resourceType: "zcc_web_privacy",
        environment: {},
        collectChild: async () => {
          childCalls += 1;
          return success("zcc_web_privacy", canonicalPull("zcc_web_privacy"));
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "INVALID_TENANT"
        && error.category === "domain",
    );
    assert.equal(childCalls, 0);
    assert.equal(validateProcessRequest({
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "overlong",
      operation: "collect_zcc_pull",
      context: {
        workspace,
        deployment: "unused",
        root_catalog: "unused",
      },
      input: {
        mode: "oneapi",
        publication: "replace_or_verify_exact",
        tenant: "a".repeat(256),
        resource_type: "zcc_web_privacy",
      },
    }), false);
    assert.equal(
      (() => {
        try { readdirSync(path.join(workspace, "pulls")); return true; }
        catch { return false; }
      })(),
      false,
    );
  });
});

function pullBytes(canonical: string): { bytes: Buffer; sha256: string } {
  const bytes = Buffer.from(canonical, "utf8");
  return {
    bytes,
    sha256: createHash("sha256").update(bytes).digest("hex"),
  };
}

test("stage alias and directory swaps never publish or unlink foreign paths", async () => {
  for (const swapDirectory of [false, true]) {
    await withWorkspace(async (workspace) => {
      const resourceType = "zcc_web_privacy";
      const prepared = await prepareZccPullPublication({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
      });
      const candidate = pullBytes(canonicalPull(resourceType));
      let foreignPath = "";
      let movedPath = "";
      await assert.rejects(
        publishZccPull({
          prepared,
          ...candidate,
          hooks: {
            afterStageBound({ stagePath }) {
              if (swapDirectory) {
                const directory = path.dirname(stagePath);
                movedPath = `${directory}-moved`;
                renameSync(directory, movedPath);
                mkdirSync(directory);
              } else {
                movedPath = `${stagePath}.original`;
                renameSync(stagePath, movedPath);
                writeFileSync(stagePath, "foreign-stage");
                foreignPath = stagePath;
              }
            },
          },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "ZCC_PULL_PUBLICATION_CLEANUP_FAILED",
      );
      assert.equal(
        (() => {
          try { readFileSync(prepared.targetPath); return true; }
          catch { return false; }
        })(),
        false,
      );
      if (!swapDirectory) {
        assert.equal(readFileSync(foreignPath, "utf8"), "foreign-stage");
        assert.equal(readFileSync(movedPath, "utf8"), canonicalPull(resourceType));
      } else {
        assert.deepEqual(readdirSync(path.dirname(prepared.targetPath)), []);
        assert.equal(readdirSync(movedPath).length > 0, true);
      }
      candidate.bytes.fill(0);
    });
  }
});

test("same-inode target mutation is detected before replacement", async () => {
  await withWorkspace(async (workspace) => {
    const resourceType = "zcc_web_privacy";
    const prepared = await prepareZccPullPublication({
      workspace,
      outputRoot: workspace,
      tenant: TENANT,
      resourceType,
    });
    mkdirSync(path.dirname(prepared.targetPath), { recursive: true });
    const desiredText = renderPythonLosslessArtifactJson([{ id: "new" }]);
    const priorText = renderPythonLosslessArtifactJson([{ id: "old" }]);
    const mutatedText = renderPythonLosslessArtifactJson([{ id: "bad" }]);
    assert.equal(Buffer.byteLength(desiredText), Buffer.byteLength(priorText));
    assert.equal(Buffer.byteLength(priorText), Buffer.byteLength(mutatedText));
    writeFileSync(prepared.targetPath, priorText);
    const before = realpathSync(prepared.targetPath);
    const candidate = pullBytes(desiredText);
    await assert.rejects(
      publishZccPull({
        prepared,
        ...candidate,
        hooks: {
          afterTargetClassified({ state, targetPath }) {
            assert.equal(state, "different");
            writeFileSync(targetPath, mutatedText);
          },
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_PULL_PUBLICATION_FAILED",
    );
    assert.equal(realpathSync(prepared.targetPath), before);
    assert.equal(readFileSync(prepared.targetPath, "utf8"), mutatedText);
    candidate.bytes.fill(0);
  });
});

test("post-link and post-rename faults are retryable and unchanged retry reuses", async () => {
  for (const initial of [null, "prior"] as const) {
    await withWorkspace(async (workspace) => {
      const resourceType = "zcc_web_privacy";
      const prepared = await prepareZccPullPublication({
        workspace,
        outputRoot: workspace,
        tenant: TENANT,
        resourceType,
      });
      if (initial !== null) {
        mkdirSync(path.dirname(prepared.targetPath), { recursive: true });
        writeFileSync(prepared.targetPath, renderPythonLosslessArtifactJson([{ id: initial }]));
      }
      const canonical = canonicalPull(resourceType);
      const candidate = pullBytes(canonical);
      await assert.rejects(
        publishZccPull({
          prepared,
          ...candidate,
          hooks: {
            afterVisiblePublication() {
              throw new Error("injected post-visibility failure");
            },
          },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "ZCC_PULL_PUBLICATION_INDETERMINATE"
          && error.retryable,
      );
      assert.equal(readFileSync(prepared.targetPath, "utf8"), canonical);
      assert.equal(await publishZccPull({ prepared, ...candidate }), "reused");
      candidate.bytes.fill(0);
    });
  }
});

test("physically disjoint workspace roots publish concurrently", async () => {
  await withWorkspace(async (left) => {
    await withWorkspace(async (right) => {
      const resourceType = "zcc_web_privacy";
      const canonical = canonicalPull(resourceType);
      const [leftReceipt, rightReceipt] = await Promise.all(
        [left, right].map(async (workspace) => {
          return collectZccPullOperation({
            workspace,
            outputRoot: workspace,
            tenant: TENANT,
            resourceType,
            environment: {},
            collectChild: async () => {
              await new Promise((resolve) => setTimeout(resolve, 10));
              return success(resourceType, canonical);
            },
          });
        }),
      );
      assert.ok(leftReceipt !== undefined && rightReceipt !== undefined);
      assert.equal(leftReceipt.publication.action, "created");
      assert.equal(rightReceipt.publication.action, "created");
    });
  });
});

test("operation snapshots caller-owned coordinates before its first await", async () => {
  await withWorkspace(async (workspace) => {
    const canonical = canonicalPull("zcc_web_privacy");
    const environment: Record<string, string> = {
      ZSCALER_CLIENT_SECRET: "original",
    };
    const mutable: Parameters<typeof collectZccPullOperation>[0] & {
      tenant: string;
      resourceType: ZccCollectionResourceType;
    } = {
      workspace,
      outputRoot: workspace,
      tenant: TENANT,
      resourceType: "zcc_web_privacy",
      environment,
      collectChild: async () => {
        mutable.tenant = "retargeted";
        mutable.resourceType = "zcc_device_cleanup";
        environment.ZSCALER_CLIENT_SECRET = "mutated";
        return success("zcc_web_privacy", canonical);
      },
    };
    const receipt = await collectZccPullOperation(mutable);
    assert.equal(receipt.tenant, TENANT);
    assert.equal(receipt.resource_type, "zcc_web_privacy");
    assert.equal(
      readFileSync(path.join(workspace, receipt.artifact.path), "utf8"),
      canonical,
    );
    assert.equal(
      (() => {
        try { readFileSync(path.join(workspace, "pulls/retargeted/zcc_device_cleanup.json")); return true; }
        catch { return false; }
      })(),
      false,
    );
  });
});

test("guard release failure is indeterminate only after create or replace visibility", async () => {
  for (const initial of ["absent", "different", "exact"] as const) {
    await withWorkspace(async (workspace) => {
      const resourceType = "zcc_web_privacy";
      const canonical = canonicalPull(resourceType);
      const target = path.join(workspace, "pulls", TENANT, `${resourceType}.json`);
      if (initial !== "absent") {
        mkdirSync(path.dirname(target), { recursive: true });
        writeFileSync(
          target,
          initial === "exact"
            ? canonical
            : renderPythonLosslessArtifactJson([{ id: "prior" }]),
        );
      }
      let swapped = false;
      const swapGuard = (): void => {
        if (swapped) return;
        swapped = true;
        const guard = path.join(workspace, ".infrawright.publisher.lock");
        renameSync(guard, `${guard}.saved`);
        writeFileSync(guard, "foreign-guard");
      };
      await assert.rejects(
        collectZccPullOperation({
          workspace,
          outputRoot: workspace,
          tenant: TENANT,
          resourceType,
          environment: {},
          collectChild: async () => success(resourceType, canonical),
          publicationHooks: initial === "exact"
            ? { afterTargetClassified: swapGuard }
            : { afterVisiblePublication: swapGuard },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === (initial === "exact"
            ? "PUBLISHER_GUARD_CLEANUP_FAILED"
            : "ZCC_PULL_PUBLICATION_INDETERMINATE")
          && error.retryable === (initial !== "exact"),
      );
      assert.equal(readFileSync(target, "utf8"), canonical);
    });
  }
});
