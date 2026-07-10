import { constants as bufferConstants } from "node:buffer";
import { createHash, randomBytes } from "node:crypto";
import { constants, type BigIntStats } from "node:fs";
import {
  lstat,
  open,
  rm,
  stat,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "../domain/errors.js";

const READ_CHUNK_BYTES = 1024 * 1024;

export interface BoundedReadLimits {
  readonly maxFiles: number;
  readonly maxDirectories: number;
  readonly maxDirectoryEntries: number;
  readonly maxDepth: number;
  readonly maxTotalBytes: bigint;
  readonly maxFileBytes: bigint;
}

export const DEFAULT_BOUNDED_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 50_000,
  maxDirectories: 10_000,
  maxDirectoryEntries: 100_000,
  maxDepth: 128,
  maxTotalBytes: 2n * 1024n * 1024n * 1024n,
  maxFileBytes: 16n * 1024n * 1024n,
};

export interface StableReadHooks {
  /** Test seam for deterministic race simulation; production callers omit it. */
  readonly afterOpen?: () => void | Promise<void>;
  /** Test seam for deterministic final-recheck simulation. */
  readonly beforeFinalStat?: () => void | Promise<void>;
}

export interface StableReadOptions {
  readonly followSymlinks?: boolean;
  readonly hooks?: StableReadHooks;
}

export interface StableFileDigest {
  readonly sha256: string;
  readonly size: bigint;
}

export interface StableFileSnapshot extends StableFileDigest {
  readonly path: string;
}

interface FileIdentity {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}

interface ConsumedFile extends StableFileDigest {
  readonly chunks: readonly Buffer[];
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "io",
): never {
  throw new ProcessFailure({ code, category, message });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object"
    && error !== null
    && "code" in error
    && typeof error.code === "string"
    ? error.code
    : null;
}

function identity(value: BigIntStats): FileIdentity {
  return {
    dev: value.dev,
    ino: value.ino,
    size: value.size,
    mtimeNs: value.mtimeNs,
    ctimeNs: value.ctimeNs,
  };
}

function sameIdentity(left: FileIdentity, right: FileIdentity): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.size === right.size
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

export class ReadBudget {
  readonly limits: BoundedReadLimits;
  private consumedFiles = 0;
  private consumedDirectories = 0;
  private consumedDirectoryEntries = 0;
  private consumedBytes = 0n;

  constructor(limits: BoundedReadLimits = DEFAULT_BOUNDED_READ_LIMITS) {
    if (
      !Number.isSafeInteger(limits.maxFiles)
      || limits.maxFiles <= 0
      || !Number.isSafeInteger(limits.maxDirectories)
      || limits.maxDirectories <= 0
      || !Number.isSafeInteger(limits.maxDirectoryEntries)
      || limits.maxDirectoryEntries <= 0
      || !Number.isSafeInteger(limits.maxDepth)
      || limits.maxDepth < 0
      || limits.maxTotalBytes <= 0n
      || limits.maxFileBytes <= 0n
    ) {
      fail("INVALID_READ_LIMIT", "bounded read limits must be positive", "domain");
    }
    this.limits = limits;
  }

  reserve(size: bigint): void {
    if (size < 0n || size > this.limits.maxFileBytes) {
      fail("FILE_LIMIT_EXCEEDED", "input file exceeds the configured size limit");
    }
    if (this.consumedFiles + 1 > this.limits.maxFiles) {
      fail("FILE_COUNT_EXCEEDED", "input exceeds the configured file-count limit");
    }
    if (this.consumedBytes + size > this.limits.maxTotalBytes) {
      fail("BYTE_BUDGET_EXCEEDED", "input exceeds the configured byte limit");
    }
    this.consumedFiles += 1;
    this.consumedBytes += size;
  }

  enterDirectory(depth: number): void {
    if (!Number.isSafeInteger(depth) || depth < 0 || depth > this.limits.maxDepth) {
      fail("DIRECTORY_DEPTH_EXCEEDED", "input exceeds the directory-depth limit");
    }
    if (this.consumedDirectories + 1 > this.limits.maxDirectories) {
      fail("DIRECTORY_COUNT_EXCEEDED", "input exceeds the directory-count limit");
    }
    this.consumedDirectories += 1;
  }

  reserveDirectoryEntry(): void {
    if (this.consumedDirectoryEntries + 1 > this.limits.maxDirectoryEntries) {
      fail("DIRECTORY_ENTRY_LIMIT_EXCEEDED", "input exceeds the directory-entry limit");
    }
    this.consumedDirectoryEntries += 1;
  }

  get files(): number {
    return this.consumedFiles;
  }

  get bytes(): bigint {
    return this.consumedBytes;
  }

  get directories(): number {
    return this.consumedDirectories;
  }

  get directoryEntries(): number {
    return this.consumedDirectoryEntries;
  }
}

async function openStableFile(
  filePath: string,
  followSymlinks: boolean,
): Promise<FileHandle> {
  const noFollow = followSymlinks ? 0 : constants.O_NOFOLLOW;
  try {
    return await open(
      filePath,
      constants.O_RDONLY | constants.O_NONBLOCK | noFollow,
    );
  } catch (error: unknown) {
    if (!followSymlinks && errorCode(error) === "ELOOP") {
      return fail("SYMLINK_NOT_ALLOWED", "input file must not be a symbolic link");
    }
    return fail("READ_FAILED", "unable to open input file");
  }
}

async function pathIdentity(
  filePath: string,
  followSymlinks: boolean,
): Promise<FileIdentity> {
  try {
    const metadata = followSymlinks
      ? await stat(filePath, { bigint: true })
      : await lstat(filePath, { bigint: true });
    if (!metadata.isFile() || (!followSymlinks && metadata.isSymbolicLink())) {
      return fail("FILE_CHANGED", "input file changed while it was read");
    }
    return identity(metadata);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("FILE_CHANGED", "input file changed while it was read");
  }
}

async function writeAll(
  handle: FileHandle,
  chunk: Buffer,
  length: number,
): Promise<void> {
  let offset = 0;
  while (offset < length) {
    const result = await handle.write(chunk, offset, length - offset, null);
    if (result.bytesWritten <= 0) {
      fail("SNAPSHOT_FAILED", "unable to write plan snapshot");
    }
    offset += result.bytesWritten;
  }
}

async function consumeStableFile(options: {
  filePath: string;
  budget: ReadBudget;
  readOptions?: StableReadOptions;
  collect?: boolean;
  onChunk?: (chunk: Buffer, length: number) => void | Promise<void>;
}): Promise<ConsumedFile> {
  const handle = await openStableFile(
    options.filePath,
    options.readOptions?.followSymlinks ?? false,
  );
  try {
    const beforeStat = await handle.stat({ bigint: true });
    if (!beforeStat.isFile()) {
      return fail("NOT_REGULAR_FILE", "input must be a regular file");
    }
    const before = identity(beforeStat);
    options.budget.reserve(before.size);
    await options.readOptions?.hooks?.afterOpen?.();

    const hasher = createHash("sha256");
    const chunks: Buffer[] = [];
    const buffer = Buffer.allocUnsafe(READ_CHUNK_BYTES);
    let consumed = 0n;
    while (true) {
      const result = await handle.read(buffer, 0, buffer.length, null);
      if (result.bytesRead === 0) {
        break;
      }
      consumed += BigInt(result.bytesRead);
      if (consumed > before.size) {
        return fail("FILE_CHANGED", "input file changed while it was read");
      }
      const chunk = buffer.subarray(0, result.bytesRead);
      hasher.update(chunk);
      if (options.collect === true) {
        chunks.push(Buffer.from(chunk));
      }
      await options.onChunk?.(chunk, result.bytesRead);
    }
    await options.readOptions?.hooks?.beforeFinalStat?.();
    const afterStat = await handle.stat({ bigint: true });
    const after = identity(afterStat);
    const current = await pathIdentity(
      options.filePath,
      options.readOptions?.followSymlinks ?? false,
    );
    if (
      consumed !== before.size
      || !sameIdentity(before, after)
      || !sameIdentity(before, current)
    ) {
      return fail("FILE_CHANGED", "input file changed while it was read");
    }
    return { sha256: hasher.digest("hex"), size: consumed, chunks };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("READ_FAILED", "unable to read input file");
  } finally {
    await handle.close().catch(() => undefined);
  }
}

export async function sha256StableFile(
  filePath: string,
  budget: ReadBudget,
  options: StableReadOptions = {},
): Promise<StableFileDigest> {
  const result = await consumeStableFile({ filePath, budget, readOptions: options });
  return { sha256: result.sha256, size: result.size };
}

export async function readBoundedUtf8File(
  filePath: string,
  budget: ReadBudget,
  options: StableReadOptions = {},
): Promise<{ readonly text: string; readonly digest: StableFileDigest }> {
  const result = await consumeStableFile({
    filePath,
    budget,
    readOptions: options,
    collect: true,
  });
  if (result.size > BigInt(bufferConstants.MAX_LENGTH)) {
    return fail("FILE_LIMIT_EXCEEDED", "input file exceeds the decoder size limit");
  }
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      Buffer.concat(result.chunks),
    );
  } catch {
    return fail("INVALID_UTF8", "input file is not valid UTF-8", "domain");
  }
  return {
    text,
    digest: { sha256: result.sha256, size: result.size },
  };
}

async function assertPrivateDirectory(directory: string): Promise<void> {
  try {
    const metadata = await lstat(directory, { bigint: true });
    if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
      return fail("UNSAFE_SNAPSHOT_DIRECTORY", "snapshot directory is not private");
    }
    if ((metadata.mode & 0o077n) !== 0n) {
      return fail("UNSAFE_SNAPSHOT_DIRECTORY", "snapshot directory is not private");
    }
    if (
      typeof process.geteuid === "function"
      && metadata.uid !== BigInt(process.geteuid())
    ) {
      return fail("UNSAFE_SNAPSHOT_DIRECTORY", "snapshot directory is not private");
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("SNAPSHOT_FAILED", "unable to inspect snapshot directory");
  }
}

export async function snapshotStableFile(options: {
  readonly sourcePath: string;
  readonly privateDirectory: string;
  readonly budget: ReadBudget;
  readonly readOptions?: StableReadOptions;
}): Promise<StableFileSnapshot> {
  await assertPrivateDirectory(options.privateDirectory);
  const snapshotPath = path.join(
    options.privateDirectory,
    `plan-${randomBytes(16).toString("hex")}`,
  );
  let destination: FileHandle | null = null;
  try {
    destination = await open(
      snapshotPath,
      constants.O_WRONLY
        | constants.O_CREAT
        | constants.O_EXCL
        | constants.O_NOFOLLOW,
      0o600,
    );
    await destination.chmod(0o600);
    const result = await consumeStableFile({
      filePath: options.sourcePath,
      budget: options.budget,
      ...(options.readOptions === undefined
        ? {}
        : { readOptions: options.readOptions }),
      onChunk: async (chunk, length) => {
        if (destination === null) {
          return fail("SNAPSHOT_FAILED", "unable to write plan snapshot");
        }
        await writeAll(destination, chunk, length);
      },
    });
    await destination.sync();
    await destination.close();
    destination = null;
    return { path: snapshotPath, sha256: result.sha256, size: result.size };
  } catch (error: unknown) {
    await destination?.close().catch(() => undefined);
    await rm(snapshotPath, { force: true }).catch(() => undefined);
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("SNAPSHOT_FAILED", "unable to create plan snapshot");
  }
}
