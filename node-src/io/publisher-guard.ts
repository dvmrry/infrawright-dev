import { constants } from "node:fs";
import { lstat, open, realpath, unlink, type FileHandle } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";

export const PUBLISHER_GUARD_BASENAME = ".infrawright.publisher.lock";

interface Guard {
  readonly path: string;
  readonly handle: FileHandle;
}

function failure(code: string, message: string, retryable = false): ProcessFailure {
  return new ProcessFailure({ code, category: "io", message, retryable });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object" && error !== null && "code" in error
    && typeof error.code === "string" ? error.code : null;
}

async function lockPath(outputRoot: string): Promise<string> {
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
  try {
    const [canonical, metadata] = await Promise.all([
      realpath(outputRoot),
      lstat(outputRoot),
    ]);
    if (
      canonical !== outputRoot
      || !metadata.isDirectory()
      || metadata.isSymbolicLink()
    ) {
      throw new Error("invalid output root");
    }
  } catch {
    throw failure(
      "PUBLISHER_GUARD_FAILED",
      "publisher guard could not bind the output root",
    );
  }
  return path.join(outputRoot, PUBLISHER_GUARD_BASENAME);
}

async function acquire(outputRoot: string): Promise<Guard> {
  const guardPath = await lockPath(outputRoot);
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
  return { path: guardPath, handle };
}

async function release(guard: Guard): Promise<void> {
  try {
    await unlink(guard.path);
  } catch {
    throw failure(
      "PUBLISHER_GUARD_CLEANUP_FAILED",
      "publisher guard could not be removed safely",
    );
  } finally {
    await guard.handle.close().catch(() => undefined);
  }
}

function cleanupFailure(primary: unknown, cleanup: ProcessFailure): ProcessFailure {
  if (primary === null) {
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
  let primary: unknown = null;
  try {
    return await mutation();
  } catch (error: unknown) {
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
      throw cleanupFailure(primary, cleanup);
    }
  }
}
