import { createHash } from "node:crypto";

import { ProcessFailure } from "./errors.js";
import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES,
  type ZccCollectionResourceType,
} from "./zcc-collection-contract.js";
import { parseZccPullDataJson } from "../json/zcc-pull-data.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import {
  runZccCollectionChildProcess,
  type ZccCollectionChildRunnerOptions,
} from "../io/zcc-collection-child-runner.js";
import {
  prepareZccPullPublication,
  publishZccPull,
  recheckPreparedZccPullPublication,
  type ZccPullPublisherHooks,
} from "../io/zcc-pull-publisher.js";
import { withPublisherGuard } from "../io/publisher-guard.js";
import type { ZccCollectionChildSuccessResponse } from "../io/zcc-collection-protocol.js";

export interface ZccPullCollectionReceipt {
  readonly kind: "infrawright.zcc_pull_collection";
  readonly schema_version: 1;
  readonly mode: "oneapi";
  readonly product: "zcc";
  readonly tenant: string;
  readonly resource_type: ZccCollectionResourceType;
  readonly status: "complete";
  readonly catalog_sources_sha256: string;
  readonly artifact: {
    readonly path: string;
    readonly media_type: "application/json";
    readonly encoding: "utf-8";
    readonly sha256: string;
    readonly size_bytes: number;
    readonly item_count: number;
  };
  readonly publication: {
    readonly policy: "replace_or_verify_exact";
    readonly action: "created" | "replaced" | "reused";
  };
}

const STRICT_BASE64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

function invalidChildResult(): never {
  throw new ProcessFailure({
    code: "ZCC_COLLECTION_CHILD_RESULT_INVALID",
    category: "internal",
    message: "ZCC collection child result failed independent validation",
  });
}

function validateCollectedBytes(options: {
  readonly resourceType: ZccCollectionResourceType;
  readonly artifact: {
    readonly body_base64: string;
    readonly catalog_sources_sha256: string;
    readonly data_requests: number;
    readonly item_count: number;
    readonly resource_type: ZccCollectionResourceType;
    readonly sha256: string;
    readonly size_bytes: number;
    readonly transport_attempts: number;
  };
}): { readonly bytes: Buffer; readonly itemCount: number; readonly sha256: string } {
  const artifact = options.artifact;
  if (
    artifact.resource_type !== options.resourceType
    || artifact.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256
    || !STRICT_BASE64.test(artifact.body_base64)
  ) {
    return invalidChildResult();
  }
  const bytes = Buffer.from(artifact.body_base64, "base64");
  try {
    if (
      bytes.toString("base64") !== artifact.body_base64
      || bytes.byteLength !== artifact.size_bytes
      || createHash("sha256").update(bytes).digest("hex") !== artifact.sha256
    ) {
      return invalidChildResult();
    }
    let text: string;
    try {
      text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
    } catch {
      return invalidChildResult();
    }
    let items: readonly unknown[];
    try {
      items = parseZccPullDataJson(text);
      if (
        items.length !== artifact.item_count
        || renderPythonLosslessArtifactJson(items) !== text
      ) {
        return invalidChildResult();
      }
    } catch {
      return invalidChildResult();
    }
    return { bytes, itemCount: items.length, sha256: artifact.sha256 };
  } catch (error: unknown) {
    bytes.fill(0);
    throw error;
  }
}

export interface CollectZccPullOperationOptions {
  readonly workspace: string;
  readonly outputRoot: string | null;
  readonly tenant: string;
  readonly resourceType: ZccCollectionResourceType;
  readonly environment: Readonly<Record<string, string>>;
  /** Trusted builder-test seam; public callers cannot supply it. */
  readonly childRunner?: ZccCollectionChildRunnerOptions;
  readonly collectChild?: () => Promise<ZccCollectionChildSuccessResponse>;
  readonly publicationHooks?: ZccPullPublisherHooks;
}

function snapshotEnvironment(
  value: Readonly<Record<string, string>>,
): Readonly<Record<string, string>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalidChildResult();
  }
  const allowed = new Set<string>(ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES);
  const snapshot = Object.create(null) as Record<string, string>;
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !allowed.has(key)) return invalidChildResult();
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
    ) return invalidChildResult();
    snapshot[key] = descriptor.value;
  }
  return Object.freeze(snapshot);
}

function snapshotChildRunner(
  value: ZccCollectionChildRunnerOptions | undefined,
): ZccCollectionChildRunnerOptions | undefined {
  if (value === undefined) return undefined;
  const identity = value.childIdentity;
  return Object.freeze({
    ...(identity === undefined ? {} : {
      childIdentity: Object.freeze({
        path: identity.path,
        sha256: identity.sha256,
        size_bytes: identity.size_bytes,
      }),
    }),
    ...(value.timeoutMs === undefined ? {} : { timeoutMs: value.timeoutMs }),
    ...(value.reapTimeoutMs === undefined ? {} : { reapTimeoutMs: value.reapTimeoutMs }),
    ...(value.spawnProcess === undefined ? {} : { spawnProcess: value.spawnProcess }),
  });
}

/** Collect in isolation, validate independently, then publish under root guard. */
export async function collectZccPullOperation(
  options: CollectZccPullOperationOptions,
): Promise<ZccPullCollectionReceipt> {
  const workspace = options.workspace;
  const outputRoot = options.outputRoot;
  const tenant = options.tenant;
  const resourceType = options.resourceType;
  const environment = snapshotEnvironment(options.environment);
  const childRunner = snapshotChildRunner(options.childRunner);
  const collectChild = options.collectChild;
  const publicationHooks: ZccPullPublisherHooks | undefined =
    options.publicationHooks === undefined
      ? undefined
      : Object.freeze({
          ...(options.publicationHooks.afterStageBound === undefined
            ? {}
            : { afterStageBound: options.publicationHooks.afterStageBound }),
          ...(options.publicationHooks.afterTargetClassified === undefined
            ? {}
            : {
                afterTargetClassified:
                  options.publicationHooks.afterTargetClassified,
              }),
          ...(options.publicationHooks.afterVisiblePublication === undefined
            ? {}
            : {
                afterVisiblePublication:
                  options.publicationHooks.afterVisiblePublication,
              }),
        });
  const prepared = await prepareZccPullPublication({
    workspace,
    outputRoot,
    tenant,
    resourceType,
  });
  const child = collectChild === undefined
    ? await runZccCollectionChildProcess({
        environment,
        resourceType,
        ...(childRunner === undefined ? {} : { runner: childRunner }),
      })
    : await collectChild();
  const validated = validateCollectedBytes({
    resourceType,
    artifact: child.artifact,
  });
  try {
    await recheckPreparedZccPullPublication(prepared);
    let visibleAction: "created" | "replaced" | null = null;
    let action: "created" | "replaced" | "reused";
    try {
      action = await withPublisherGuard(prepared.workspace, async () => {
        const published = await publishZccPull({
          prepared,
          bytes: validated.bytes,
          sha256: validated.sha256,
          ...(publicationHooks === undefined ? {} : { hooks: publicationHooks }),
        });
        if (published === "created" || published === "replaced") {
          visibleAction = published;
        }
        return published;
      });
    } catch (error: unknown) {
      if (
        visibleAction !== null
        && error instanceof ProcessFailure
        && error.code === "PUBLISHER_GUARD_CLEANUP_FAILED"
      ) {
        throw new ProcessFailure({
          code: "ZCC_PULL_PUBLICATION_INDETERMINATE",
          category: "io",
          message: "ZCC pull publication advanced but its guard cleanup failed",
          retryable: true,
          details: [{ path: "$", code: error.code, message: error.message }],
        });
      }
      throw error;
    }
    return Object.freeze({
      kind: "infrawright.zcc_pull_collection",
      schema_version: 1,
      mode: "oneapi",
      product: "zcc",
      tenant,
      resource_type: resourceType,
      status: "complete",
      catalog_sources_sha256: ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
      artifact: Object.freeze({
        path: prepared.relativePath,
        media_type: "application/json",
        encoding: "utf-8",
        sha256: validated.sha256,
        size_bytes: validated.bytes.byteLength,
        item_count: validated.itemCount,
      }),
      publication: Object.freeze({
        policy: "replace_or_verify_exact",
        action,
      }),
    });
  } finally {
    validated.bytes.fill(0);
  }
}
