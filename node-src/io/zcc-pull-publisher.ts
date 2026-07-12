import { createHash, randomBytes } from "node:crypto";
import { constants } from "node:fs";
import {
  lstat,
  link,
  mkdir,
  open,
  realpath,
  rename,
  unlink,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";
import { types as utilTypes } from "node:util";

import { ProcessFailure } from "../domain/errors.js";
import { validateTenant } from "../domain/roots.js";
import type { ZccCollectionResourceType } from "../domain/zcc-collection-contract.js";

export type ZccPullPublicationAction = "created" | "replaced" | "reused";
export const MAX_ZCC_PULL_TARGET_COMPONENT_BYTES = 255;

export interface ZccPullPublisherHooks {
  readonly afterStageBound?: (context: {
    readonly stagePath: string;
    readonly targetPath: string;
  }) => void | Promise<void>;
  readonly afterTargetClassified?: (context: {
    readonly stagePath: string;
    readonly targetPath: string;
    readonly state: "absent" | "different" | "exact";
  }) => void | Promise<void>;
  readonly afterVisiblePublication?: (context: {
    readonly action: "created" | "replaced";
    readonly targetPath: string;
  }) => void | Promise<void>;
}

export interface PreparedZccPullPublication {
  readonly workspace: string;
  readonly targetPath: string;
  readonly relativePath: string;
  readonly workspaceIdentity: {
    readonly dev: bigint;
    readonly ino: bigint;
  };
}

const TYPED_ARRAY_PROTOTYPE = Object.getPrototypeOf(Uint8Array.prototype) as object;
const TYPED_ARRAY_BUFFER_GETTER = Object.getOwnPropertyDescriptor(
  TYPED_ARRAY_PROTOTYPE,
  "buffer",
)?.get;
const TYPED_ARRAY_BYTE_LENGTH_GETTER = Object.getOwnPropertyDescriptor(
  TYPED_ARRAY_PROTOTYPE,
  "byteLength",
)?.get;
const ARRAY_BUFFER_DETACHED_GETTER = Object.getOwnPropertyDescriptor(
  ArrayBuffer.prototype,
  "detached",
)?.get;
const ARRAY_BUFFER_RESIZABLE_GETTER = Object.getOwnPropertyDescriptor(
  ArrayBuffer.prototype,
  "resizable",
)?.get;

function intrinsicGetter<T>(
  getter: ((this: unknown) => unknown) | undefined,
  receiver: unknown,
): T {
  if (getter === undefined) throw new Error("required Node 24 intrinsic is absent");
  return Reflect.apply(getter, receiver, []) as T;
}

function failure(
  code: string,
  message: string,
  retryable = false,
): ProcessFailure {
  return new ProcessFailure({ category: "io", code, message, retryable });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object" && error !== null && "code" in error
    && typeof error.code === "string" ? error.code : null;
}

/** Validate the host-only authority and derive the sole v1 target. */
export async function prepareZccPullPublication(options: {
  readonly workspace: string;
  readonly outputRoot: string | null;
  readonly tenant: string;
  readonly resourceType: ZccCollectionResourceType;
}): Promise<PreparedZccPullPublication> {
  const workspace = options.workspace;
  const outputRoot = options.outputRoot;
  const tenant = options.tenant;
  const resourceType = options.resourceType;
  validateTenant(tenant);
  if (Buffer.byteLength(tenant, "utf8") > MAX_ZCC_PULL_TARGET_COMPONENT_BYTES) {
    throw new ProcessFailure({
      code: "INVALID_TENANT",
      category: "domain",
      message: "TENANT exceeds the pull target component bound",
    });
  }
  if (outputRoot === null) {
    throw failure(
      "ZCC_PULL_OUTPUT_ROOT_NOT_CONFIGURED",
      "ZCC pull publication requires a trusted output root",
    );
  }
  if (
    !path.isAbsolute(workspace)
    || !path.isAbsolute(outputRoot)
    || workspace.includes("\0")
    || outputRoot.includes("\0")
    || !workspace.isWellFormed()
    || !outputRoot.isWellFormed()
    || path.resolve(workspace) !== workspace
    || path.resolve(outputRoot) !== outputRoot
    || path.parse(workspace).root === workspace
  ) {
    throw failure(
      "ZCC_PULL_OUTPUT_ROOT_INVALID",
      "ZCC pull output authority must be one canonical workspace",
    );
  }
  try {
    const [workspaceCanonical, outputCanonical, workspaceStat, outputStat] =
      await Promise.all([
        realpath(workspace),
        realpath(outputRoot),
        lstat(workspace, { bigint: true }),
        lstat(outputRoot, { bigint: true }),
      ]);
    if (
      workspaceCanonical !== workspace
      || outputCanonical !== outputRoot
      || workspace !== outputRoot
      || workspaceCanonical !== outputCanonical
      || !workspaceStat.isDirectory()
      || !outputStat.isDirectory()
      || workspaceStat.isSymbolicLink()
      || outputStat.isSymbolicLink()
      || workspaceStat.dev !== outputStat.dev
      || workspaceStat.ino !== outputStat.ino
    ) {
      throw new Error("authority mismatch");
    }
    const relativePath = path.posix.join(
      "pulls",
      tenant,
      `${resourceType}.json`,
    );
    return Object.freeze({
      workspace,
      targetPath: path.join(
        workspace,
        "pulls",
        tenant,
        `${resourceType}.json`,
      ),
      relativePath,
      workspaceIdentity: Object.freeze({
        dev: workspaceStat.dev,
        ino: workspaceStat.ino,
      }),
    });
  } catch {
    throw failure(
      "ZCC_PULL_OUTPUT_ROOT_INVALID",
      "ZCC pull output authority must exactly equal the canonical workspace",
    );
  }
}

/** Rebind the pre-collection workspace before the publisher guard is created. */
export async function recheckPreparedZccPullPublication(
  prepared: PreparedZccPullPublication,
): Promise<void> {
  const workspace = prepared.workspace;
  const expectedDev = prepared.workspaceIdentity.dev;
  const expectedIno = prepared.workspaceIdentity.ino;
  try {
    const [canonical, metadata] = await Promise.all([
      realpath(workspace),
      lstat(workspace, { bigint: true }),
    ]);
    if (
      canonical !== workspace
      || metadata.isSymbolicLink()
      || !metadata.isDirectory()
      || metadata.dev !== expectedDev
      || metadata.ino !== expectedIno
    ) {
      throw new Error("workspace authority changed");
    }
  } catch {
    throw failure(
      "ZCC_PULL_OUTPUT_ROOT_CHANGED",
      "ZCC pull output authority changed during collection",
    );
  }
}

interface BoundDirectory {
  readonly path: string;
  readonly handle: FileHandle;
  readonly dev: bigint;
  readonly ino: bigint;
}

async function bindDirectory(directoryPath: string): Promise<BoundDirectory> {
  const handle = await open(
    directoryPath,
    constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
  );
  try {
    const [metadata, pathname, canonical] = await Promise.all([
      handle.stat({ bigint: true }),
      lstat(directoryPath, { bigint: true }),
      realpath(directoryPath),
    ]);
    if (
      canonical !== directoryPath
      || !metadata.isDirectory()
      || !pathname.isDirectory()
      || pathname.isSymbolicLink()
      || metadata.dev !== pathname.dev
      || metadata.ino !== pathname.ino
    ) {
      throw new Error("directory binding failed");
    }
    return { path: directoryPath, handle, dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    await handle.close().catch(() => undefined);
    throw error;
  }
}

async function verifyDirectory(directory: BoundDirectory): Promise<void> {
  const [handleStat, pathStat, canonical] = await Promise.all([
    directory.handle.stat({ bigint: true }),
    lstat(directory.path, { bigint: true }),
    realpath(directory.path),
  ]);
  if (
    canonical !== directory.path
    || !handleStat.isDirectory()
    || !pathStat.isDirectory()
    || pathStat.isSymbolicLink()
    || handleStat.dev !== directory.dev
    || handleStat.ino !== directory.ino
    || pathStat.dev !== directory.dev
    || pathStat.ino !== directory.ino
  ) {
    throw new Error("publication directory changed");
  }
}

async function ensureFinalDirectory(
  prepared: PreparedZccPullPublication,
  root: BoundDirectory,
): Promise<BoundDirectory> {
  const pulls = path.join(prepared.workspace, "pulls");
  const tenant = path.dirname(prepared.targetPath);
  await verifyDirectory(root);
  await mkdir(pulls, { mode: 0o777 }).catch((error: unknown) => {
    if (errorCode(error) !== "EEXIST") throw error;
  });
  await root.handle.sync();
  await verifyDirectory(root);
  const pullsBinding = await bindDirectory(pulls);
  try {
    await mkdir(tenant, { mode: 0o777 }).catch((error: unknown) => {
      if (errorCode(error) !== "EEXIST") throw error;
    });
    await pullsBinding.handle.sync();
    await verifyDirectory(pullsBinding);
    const final = await bindDirectory(tenant);
    try {
      await verifyDirectory(root);
      return final;
    } catch (error: unknown) {
      await final.handle.close().catch(() => undefined);
      throw error;
    }
  } finally {
    await pullsBinding.handle.close().catch(() => undefined);
  }
}

async function writeAll(handle: FileHandle, bytes: Uint8Array): Promise<void> {
  let offset = 0;
  while (offset < bytes.byteLength) {
    const { bytesWritten } = await handle.write(
      bytes,
      offset,
      bytes.byteLength - offset,
      offset,
    );
    if (bytesWritten <= 0) throw new Error("short publication write");
    offset += bytesWritten;
  }
}

async function readExact(handle: FileHandle, size: number): Promise<Buffer> {
  const bytes = Buffer.allocUnsafe(size);
  let offset = 0;
  while (offset < size) {
    const { bytesRead } = await handle.read(bytes, offset, size - offset, offset);
    if (bytesRead <= 0) {
      bytes.fill(0);
      throw new Error("short publication read");
    }
    offset += bytesRead;
  }
  return bytes;
}

interface BoundTarget {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
  readonly sha256: string;
  readonly exact: boolean;
}

async function classifyTarget(
  targetPath: string,
  expected: Uint8Array,
  expectedSha: string,
): Promise<BoundTarget | null> {
  let metadata;
  try {
    metadata = await lstat(targetPath, { bigint: true });
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") return null;
    throw error;
  }
  if (metadata.isSymbolicLink() || !metadata.isFile()) {
    throw failure(
      "ZCC_PULL_PUBLICATION_TARGET_INVALID",
      "ZCC pull target must be absent or a regular non-symlink file",
    );
  }
  if (metadata.size > BigInt(4 * 1024 * 1024)) {
    throw failure(
      "ZCC_PULL_PUBLICATION_TARGET_INVALID",
      "existing ZCC pull target exceeds the supported bound",
    );
  }
  const handle = await open(targetPath, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const opened = await handle.stat({ bigint: true });
    if (opened.dev !== metadata.dev || opened.ino !== metadata.ino) {
      throw new Error("target identity changed");
    }
    const bytes = await readExact(handle, Number(metadata.size));
    try {
      const [after, pathAfter] = await Promise.all([
        handle.stat({ bigint: true }),
        lstat(targetPath, { bigint: true }),
      ]);
      if (
        after.dev !== metadata.dev || after.ino !== metadata.ino
        || after.size !== metadata.size || after.mtimeNs !== metadata.mtimeNs
        || after.ctimeNs !== metadata.ctimeNs
        || pathAfter.dev !== metadata.dev || pathAfter.ino !== metadata.ino
        || pathAfter.size !== metadata.size
        || pathAfter.mtimeNs !== metadata.mtimeNs
        || pathAfter.ctimeNs !== metadata.ctimeNs
      ) {
        throw new Error("target changed during classification");
      }
      const observedSha = createHash("sha256").update(bytes).digest("hex");
      return {
        dev: metadata.dev,
        ino: metadata.ino,
        size: metadata.size,
        mtimeNs: metadata.mtimeNs,
        ctimeNs: metadata.ctimeNs,
        sha256: observedSha,
        exact: metadata.size === BigInt(expected.byteLength)
          && observedSha === expectedSha
          && bytes.equals(Buffer.from(
            expected.buffer,
            expected.byteOffset,
            expected.byteLength,
          )),
      };
    } finally {
      bytes.fill(0);
    }
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function verifyBoundTarget(
  targetPath: string,
  expected: BoundTarget,
): Promise<void> {
  const metadata = await lstat(targetPath, { bigint: true });
  const handle = await open(targetPath, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const opened = await handle.stat({ bigint: true });
    if (
      metadata.isSymbolicLink()
      || !metadata.isFile()
      || !opened.isFile()
      || metadata.dev !== expected.dev
      || metadata.ino !== expected.ino
      || metadata.size !== expected.size
      || metadata.mtimeNs !== expected.mtimeNs
      || metadata.ctimeNs !== expected.ctimeNs
      || opened.dev !== expected.dev
      || opened.ino !== expected.ino
      || opened.size !== expected.size
      || opened.mtimeNs !== expected.mtimeNs
      || opened.ctimeNs !== expected.ctimeNs
    ) {
      throw new Error("publication target changed before replacement");
    }
    const bytes = await readExact(handle, Number(expected.size));
    try {
      if (createHash("sha256").update(bytes).digest("hex") !== expected.sha256) {
        throw new Error("publication target bytes changed before replacement");
      }
    } finally {
      bytes.fill(0);
    }
  } finally {
    await handle.close().catch(() => undefined);
  }
}

interface BoundStage {
  readonly path: string;
  readonly handle: FileHandle;
  readonly dev: bigint;
  readonly ino: bigint;
}

async function bindStage(pathValue: string, handle: FileHandle): Promise<BoundStage> {
  const [opened, pathname] = await Promise.all([
    handle.stat({ bigint: true }),
    lstat(pathValue, { bigint: true }),
  ]);
  if (
    !opened.isFile()
    || !pathname.isFile()
    || pathname.isSymbolicLink()
    || opened.dev !== pathname.dev
    || opened.ino !== pathname.ino
  ) {
    throw new Error("publication stage binding failed");
  }
  return { path: pathValue, handle, dev: opened.dev, ino: opened.ino };
}

async function verifyStage(
  stage: BoundStage,
  bytes: Uint8Array,
  sha256: string,
): Promise<void> {
  const [opened, pathname] = await Promise.all([
    stage.handle.stat({ bigint: true }),
    lstat(stage.path, { bigint: true }),
  ]);
  if (
    !opened.isFile()
    || !pathname.isFile()
    || pathname.isSymbolicLink()
    || opened.dev !== stage.dev
    || opened.ino !== stage.ino
    || pathname.dev !== stage.dev
    || pathname.ino !== stage.ino
    || opened.size !== BigInt(bytes.byteLength)
    || pathname.size !== opened.size
  ) {
    throw new Error("publication stage changed");
  }
  const reread = await readExact(stage.handle, bytes.byteLength);
  try {
    if (
      createHash("sha256").update(reread).digest("hex") !== sha256
      || !reread.equals(Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength))
    ) {
      throw new Error("publication stage bytes changed");
    }
  } finally {
    reread.fill(0);
  }
}

async function unlinkBoundStage(stage: BoundStage): Promise<void> {
  const [opened, pathname] = await Promise.all([
    stage.handle.stat({ bigint: true }),
    lstat(stage.path, { bigint: true }),
  ]);
  if (
    opened.dev !== stage.dev
    || opened.ino !== stage.ino
    || pathname.dev !== stage.dev
    || pathname.ino !== stage.ino
    || pathname.isSymbolicLink()
    || !pathname.isFile()
  ) {
    throw failure(
      "ZCC_PULL_PUBLICATION_CLEANUP_FAILED",
      "ZCC pull staging alias could not be rebound for cleanup",
    );
  }
  await unlink(stage.path);
}

async function verifyFinalTarget(options: {
  readonly directory: BoundDirectory;
  readonly targetPath: string;
  readonly bytes: Uint8Array;
  readonly sha256: string;
}): Promise<void> {
  await verifyDirectory(options.directory);
  const pathStat = await lstat(options.targetPath, { bigint: true });
  if (pathStat.isSymbolicLink() || !pathStat.isFile()) {
    throw new Error("final target is not regular");
  }
  const handle = await open(
    options.targetPath,
    constants.O_RDONLY | constants.O_NOFOLLOW,
  );
  try {
    const opened = await handle.stat({ bigint: true });
    if (
      opened.dev !== pathStat.dev
      || opened.ino !== pathStat.ino
      || opened.size !== BigInt(options.bytes.byteLength)
    ) {
      throw new Error("final target identity changed");
    }
    const reread = await readExact(handle, options.bytes.byteLength);
    try {
      if (
        createHash("sha256").update(reread).digest("hex") !== options.sha256
        || !reread.equals(Buffer.from(
          options.bytes.buffer,
          options.bytes.byteOffset,
          options.bytes.byteLength,
        ))
      ) {
        throw new Error("final target bytes changed");
      }
      await handle.sync();
    } finally {
      reread.fill(0);
    }
  } finally {
    await handle.close().catch(() => undefined);
  }
  await options.directory.handle.sync();
  await verifyDirectory(options.directory);
}

/** Publish one pull with create/reuse/atomic-replace retry-forward semantics. */
export async function publishZccPull(options: {
  readonly prepared: PreparedZccPullPublication;
  readonly bytes: Uint8Array;
  readonly sha256: string;
  readonly hooks?: ZccPullPublisherHooks;
}): Promise<ZccPullPublicationAction> {
  // Snapshot caller-owned data synchronously before the first await.
  const prepared = Object.freeze({
    workspace: options.prepared.workspace,
    targetPath: options.prepared.targetPath,
    relativePath: options.prepared.relativePath,
    workspaceIdentity: Object.freeze({
      dev: options.prepared.workspaceIdentity.dev,
      ino: options.prepared.workspaceIdentity.ino,
    }),
  });
  if (
    options.bytes === null
    || typeof options.bytes !== "object"
    || utilTypes.isProxy(options.bytes)
    || !utilTypes.isUint8Array(options.bytes)
    || (
      Object.getPrototypeOf(options.bytes) !== Uint8Array.prototype
      && Object.getPrototypeOf(options.bytes) !== Buffer.prototype
    )
  ) {
    throw failure(
      "ZCC_PULL_PUBLICATION_INPUT_INVALID",
      "ZCC pull publication input is invalid",
    );
  }
  const backing = intrinsicGetter<unknown>(TYPED_ARRAY_BUFFER_GETTER, options.bytes);
  const byteLength = intrinsicGetter<number>(
    TYPED_ARRAY_BYTE_LENGTH_GETTER,
    options.bytes,
  );
  if (
    utilTypes.isSharedArrayBuffer(backing)
    || !utilTypes.isArrayBuffer(backing)
    || intrinsicGetter<boolean>(ARRAY_BUFFER_DETACHED_GETTER, backing)
    || intrinsicGetter<boolean>(ARRAY_BUFFER_RESIZABLE_GETTER, backing)
    || !Number.isSafeInteger(byteLength)
    || byteLength < 0
    || byteLength > 4 * 1024 * 1024
  ) {
    throw failure(
      "ZCC_PULL_PUBLICATION_INPUT_INVALID",
      "ZCC pull publication input is invalid",
    );
  }
  // Buffer.from(Uint8Array) copies; do not retain caller-owned backing memory.
  const bytes = Buffer.from(options.bytes);
  const sha256 = options.sha256;
  const hooks: ZccPullPublisherHooks | undefined = options.hooks === undefined
    ? undefined
    : Object.freeze({
        ...(options.hooks.afterStageBound === undefined
          ? {}
          : { afterStageBound: options.hooks.afterStageBound }),
        ...(options.hooks.afterTargetClassified === undefined
          ? {}
          : { afterTargetClassified: options.hooks.afterTargetClassified }),
        ...(options.hooks.afterVisiblePublication === undefined
          ? {}
          : { afterVisiblePublication: options.hooks.afterVisiblePublication }),
      });
  if (
    !/^[0-9a-f]{64}$/.test(sha256)
    || createHash("sha256").update(bytes).digest("hex") !== sha256
    || path.dirname(path.dirname(prepared.targetPath))
      !== path.join(prepared.workspace, "pulls")
  ) {
    bytes.fill(0);
    throw failure(
      "ZCC_PULL_PUBLICATION_INPUT_INVALID",
      "ZCC pull publication input is invalid",
    );
  }
  let root: BoundDirectory | null = null;
  let directory: BoundDirectory | null = null;
  let stage: BoundStage | null = null;
  let stageHandle: FileHandle | null = null;
  let stageExists = false;
  let visibleMutation = false;
  let primary: unknown = null;
  let cleanup: ProcessFailure | null = null;
  let action: ZccPullPublicationAction | null = null;
  try {
    root = await bindDirectory(prepared.workspace);
    if (
      root.dev !== prepared.workspaceIdentity.dev
      || root.ino !== prepared.workspaceIdentity.ino
    ) {
      throw failure(
        "ZCC_PULL_OUTPUT_ROOT_CHANGED",
        "ZCC pull output authority changed during collection",
      );
    }
    directory = await ensureFinalDirectory(prepared, root);
    const stagePath = path.join(
      directory.path,
      `.infrawright-zcc-pull-${randomBytes(16).toString("hex")}.tmp`,
    );
    await verifyDirectory(root);
    await verifyDirectory(directory);
    stageHandle = await open(
      stagePath,
      constants.O_RDWR
        | constants.O_CREAT
        | constants.O_EXCL
        | constants.O_NOFOLLOW,
      0o600,
    );
    stageExists = true;
    stage = await bindStage(stagePath, stageHandle);
    await writeAll(stage.handle, bytes);
    await stage.handle.sync();
    await verifyStage(stage, bytes, sha256);
    await hooks?.afterStageBound?.({
      stagePath: stage.path,
      targetPath: prepared.targetPath,
    });
    await verifyDirectory(root);
    await verifyDirectory(directory);
    const state = await classifyTarget(prepared.targetPath, bytes, sha256);
    await hooks?.afterTargetClassified?.({
      stagePath: stage.path,
      targetPath: prepared.targetPath,
      state: state === null ? "absent" : state.exact ? "exact" : "different",
    });
    if (state?.exact === true) {
      action = "reused";
      await verifyStage(stage, bytes, sha256);
      await unlinkBoundStage(stage);
      stageExists = false;
    } else if (state === null) {
      await verifyStage(stage, bytes, sha256);
      await verifyDirectory(root);
      await verifyDirectory(directory);
      try {
        await link(stage.path, prepared.targetPath);
      } catch (error: unknown) {
        if (errorCode(error) !== "EEXIST") throw error;
        const raced = await classifyTarget(prepared.targetPath, bytes, sha256);
        if (raced?.exact !== true) {
          throw new Error("target appeared with foreign bytes");
        }
        action = "reused";
        await unlinkBoundStage(stage);
        stageExists = false;
      }
      if (action === null) {
        visibleMutation = true;
        action = "created";
        await hooks?.afterVisiblePublication?.({
          action,
          targetPath: prepared.targetPath,
        });
        await unlinkBoundStage(stage);
        stageExists = false;
      }
    } else {
      await verifyBoundTarget(prepared.targetPath, state);
      await verifyStage(stage, bytes, sha256);
      await verifyDirectory(root);
      await verifyDirectory(directory);
      await verifyBoundTarget(prepared.targetPath, state);
      await rename(stage.path, prepared.targetPath);
      stageExists = false;
      visibleMutation = true;
      action = "replaced";
      await hooks?.afterVisiblePublication?.({
        action,
        targetPath: prepared.targetPath,
      });
    }
    await verifyFinalTarget({
      directory,
      targetPath: prepared.targetPath,
      bytes,
      sha256,
    });
    await verifyDirectory(root);
  } catch (error: unknown) {
    primary = error;
  } finally {
    if (stageExists) {
      if (stage === null) {
        cleanup = failure(
          "ZCC_PULL_PUBLICATION_CLEANUP_FAILED",
          "ZCC pull staging alias could not be bound for safe cleanup",
        );
      } else {
        try {
          await unlinkBoundStage(stage);
          stageExists = false;
        } catch (error: unknown) {
          cleanup = error instanceof ProcessFailure
            ? error
            : failure(
                "ZCC_PULL_PUBLICATION_CLEANUP_FAILED",
                "ZCC pull staging alias could not be removed safely",
              );
        }
      }
    }
    await Promise.all([
      (stage?.handle ?? stageHandle)?.close().catch(() => undefined),
      directory?.handle.close().catch(() => undefined),
      root?.handle.close().catch(() => undefined),
    ]);
    bytes.fill(0);
  }
  if (visibleMutation && (primary !== null || cleanup !== null)) {
    throw new ProcessFailure({
      code: "ZCC_PULL_PUBLICATION_INDETERMINATE",
      category: "io",
      message: "ZCC pull publication may have advanced and must be retried unchanged",
      retryable: true,
      details: cleanup === null ? [] : [{
        path: "$",
        code: cleanup.code,
        message: cleanup.message,
      }],
    });
  }
  if (cleanup !== null) {
    throw cleanup;
  }
  if (primary !== null) {
    if (primary instanceof ProcessFailure) throw primary;
    throw failure(
      "ZCC_PULL_PUBLICATION_FAILED",
      "ZCC pull publication failed before visibility",
    );
  }
  if (action === null) {
    throw failure(
      "ZCC_PULL_PUBLICATION_FAILED",
      "ZCC pull publication did not produce a final action",
    );
  }
  return action;
}
