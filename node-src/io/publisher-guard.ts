import { constants, type BigIntStats } from "node:fs";
import {
  lstat,
  open,
  realpath,
  unlink,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";

export const PUBLISHER_GUARD_BASENAME = ".infrawright.publisher.lock";

interface Identity {
  readonly dev: bigint;
  readonly ino: bigint;
}

interface RootBinding {
  readonly path: string;
  readonly handle: FileHandle;
  readonly identity: Identity;
}

interface Guard {
  readonly root: RootBinding;
  readonly path: string;
  readonly handle: FileHandle;
  readonly identity: Identity;
}

function failure(code: string, message: string, retryable = false): ProcessFailure {
  return new ProcessFailure({ code, category: "io", message, retryable });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object" && error !== null && "code" in error
    && typeof error.code === "string" ? error.code : null;
}

function identity(metadata: BigIntStats): Identity {
  return { dev: metadata.dev, ino: metadata.ino };
}

function sameIdentity(left: Identity, right: Identity): boolean {
  return left.dev === right.dev && left.ino === right.ino;
}

function containedPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative !== ""
    && relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative);
}

function commonArtifactAuthority(targets: readonly string[]): string | null {
  if (targets.length === 0 || targets.some((target) => !path.isAbsolute(target))) {
    return null;
  }
  const parents = targets.map((target) => path.dirname(target));
  const first = parents[0];
  if (first === undefined) {
    return null;
  }
  let authority: string = first;
  while (!parents.every((parent) => {
    return parent === authority || containedPath(parent, authority);
  })) {
    const parent = path.dirname(authority);
    if (parent === authority) {
      return null;
    }
    authority = parent;
  }
  return authority;
}

/** Require the configured root to be the unique authority of the target set. */
export function requireExactPublisherAuthority(
  outputRoot: string,
  targets: readonly string[],
): void {
  const authority = commonArtifactAuthority(targets);
  if (
    authority === null
    || authority === path.parse(authority).root
    || authority !== outputRoot
  ) {
    throw failure(
      "OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY",
      "output root must exactly match the artifact target authority",
    );
  }
}

async function verifyRoot(root: RootBinding): Promise<void> {
  const [canonical, pathMetadata, handleMetadata] = await Promise.all([
    realpath(root.path),
    lstat(root.path, { bigint: true }),
    root.handle.stat({ bigint: true }),
  ]);
  if (
    canonical !== root.path
    || !pathMetadata.isDirectory()
    || pathMetadata.isSymbolicLink()
    || !handleMetadata.isDirectory()
    || !sameIdentity(root.identity, identity(pathMetadata))
    || !sameIdentity(root.identity, identity(handleMetadata))
  ) {
    throw new Error("publisher root binding changed");
  }
}

async function bindRoot(outputRoot: string): Promise<RootBinding> {
  if (
    !path.isAbsolute(outputRoot)
    || outputRoot.includes("\0")
    || !outputRoot.isWellFormed()
    || path.resolve(outputRoot) !== outputRoot
    || path.parse(outputRoot).root === outputRoot
  ) {
    throw failure(
      "PUBLISHER_GUARD_FAILED",
      "publisher guard requires a canonical output root",
    );
  }
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      outputRoot,
      constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
    );
    const metadata = await handle.stat({ bigint: true });
    if (!metadata.isDirectory()) {
      throw new Error("publisher root is not a directory");
    }
    const root = { path: outputRoot, handle, identity: identity(metadata) };
    await verifyRoot(root);
    return root;
  } catch (error: unknown) {
    await handle?.close().catch(() => undefined);
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw failure(
      "PUBLISHER_GUARD_FAILED",
      "publisher guard could not bind the output root",
    );
  }
}

async function verifyGuard(guard: Guard): Promise<void> {
  const [pathMetadata, handleMetadata] = await Promise.all([
    lstat(guard.path, { bigint: true }),
    guard.handle.stat({ bigint: true }),
    verifyRoot(guard.root),
  ]);
  if (
    !pathMetadata.isFile()
    || pathMetadata.isSymbolicLink()
    || !handleMetadata.isFile()
    || !sameIdentity(guard.identity, identity(pathMetadata))
    || !sameIdentity(guard.identity, identity(handleMetadata))
  ) {
    throw new Error("publisher guard binding changed");
  }
}

async function closeGuard(guard: Guard): Promise<void> {
  await Promise.all([
    guard.handle.close().catch(() => undefined),
    guard.root.handle.close().catch(() => undefined),
  ]);
}

async function acquire(outputRoot: string): Promise<Guard> {
  const root = await bindRoot(outputRoot);
  const guardPath = path.join(outputRoot, PUBLISHER_GUARD_BASENAME);
  let handle: FileHandle;
  try {
    handle = await open(
      guardPath,
      constants.O_WRONLY
        | constants.O_CREAT
        | constants.O_EXCL
        | constants.O_NOFOLLOW,
      0o600,
    );
  } catch (error: unknown) {
    await root.handle.close().catch(() => undefined);
    if (errorCode(error) === "EEXIST") {
      throw failure(
        "OUTPUT_ROOT_BUSY",
        "output root already has an active or stale publisher guard",
        true,
      );
    }
    throw failure(
      "PUBLISHER_GUARD_FAILED",
      "publisher guard could not be acquired",
    );
  }
  let guard: Guard | null = null;
  try {
    const metadata = await handle.stat({ bigint: true });
    if (!metadata.isFile()) {
      throw new Error("publisher guard is not a regular file");
    }
    guard = {
      root,
      path: guardPath,
      handle,
      identity: identity(metadata),
    };
    await verifyGuard(guard);
    return guard;
  } catch {
    if (guard === null) {
      await Promise.all([
        handle.close().catch(() => undefined),
        root.handle.close().catch(() => undefined),
      ]);
    } else {
      await closeGuard(guard);
    }
    throw failure(
      "PUBLISHER_GUARD_FAILED",
      "publisher guard could not be bound safely",
    );
  }
}

async function release(guard: Guard): Promise<void> {
  try {
    await verifyGuard(guard);
    await unlink(guard.path);
  } catch {
    throw failure(
      "PUBLISHER_GUARD_CLEANUP_FAILED",
      "publisher guard could not be removed safely",
    );
  } finally {
    await closeGuard(guard);
  }
}

function cleanupFailure(
  hasPrimary: boolean,
  primary: unknown,
  cleanup: ProcessFailure,
): ProcessFailure {
  if (!hasPrimary) {
    return cleanup;
  }
  const preserved = primary instanceof ProcessFailure
    ? primary
    : new ProcessFailure({
        code: "INTERNAL_ERROR",
        category: "internal",
        message: "internal process failure",
      });
  return new ProcessFailure({
    code: preserved.code,
    category: preserved.category,
    message: preserved.message,
    retryable: preserved.retryable,
    details: [
      ...preserved.details,
      { path: "$", code: cleanup.code, message: cleanup.message },
    ],
  });
}

/** Serialize one persistent process-host mutation for the complete output root. */
export async function withPublisherGuard<T>(
  outputRoot: string,
  mutation: () => Promise<T>,
): Promise<T> {
  const guard = await acquire(outputRoot);
  let hasPrimary = false;
  let primary: unknown;
  try {
    return await mutation();
  } catch (error: unknown) {
    hasPrimary = true;
    primary = error;
    throw error;
  } finally {
    try {
      await release(guard);
    } catch (error: unknown) {
      const cleanup = error instanceof ProcessFailure
        ? error
        : failure(
            "PUBLISHER_GUARD_CLEANUP_FAILED",
            "publisher guard could not be removed safely",
          );
      throw cleanupFailure(hasPrimary, primary, cleanup);
    }
  }
}
