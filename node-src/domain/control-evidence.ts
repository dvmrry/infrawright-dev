import { lstat, stat } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  ReadBudget,
  readBoundedFileBytes,
  sha256StableFile,
  type BoundedReadLimits,
  type StableFileDigest,
  type StableFileIdentity,
} from "../io/bounded-files.js";

const MAX_CONTROL_FILES = 8;
const CONTROL_READ_LIMITS: BoundedReadLimits = {
  maxFiles: MAX_CONTROL_FILES,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 64n * 1024n * 1024n,
  maxFileBytes: 16n * 1024n * 1024n,
};

export interface BoundAssessmentControlFile {
  readonly path: string;
  readonly digest: StableFileDigest | null;
  readonly identity?: StableFileIdentity | null;
  readonly followSymlinks?: boolean;
}

export interface BoundAssessmentControlText {
  readonly text: string | null;
  readonly file: BoundAssessmentControlFile;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object"
    && error !== null
    && "code" in error
    && typeof error.code === "string"
    ? error.code
    : null;
}

function validatePath(filePath: string): void {
  if (!path.isAbsolute(filePath) || filePath.includes("\0")) {
    fail(
      "UNRESOLVED_ASSESSMENT_CONTROL_PATH",
      "assessment control inputs require resolved absolute paths",
    );
  }
}

export async function bindRequiredAssessmentControlText(
  filePath: string,
  options: { readonly followSymlinks?: boolean } = {},
): Promise<BoundAssessmentControlText> {
  validatePath(filePath);
  const source = await readBoundedFileBytes(
    filePath,
    new ReadBudget(CONTROL_READ_LIMITS),
    { followSymlinks: options.followSymlinks ?? true },
  );
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(source.bytes);
  } catch {
    fail("INVALID_UTF8", "assessment control input is not valid UTF-8");
  }
  return {
    text,
    file: {
      path: filePath,
      digest: source.digest,
      identity: source.identity,
      ...(options.followSymlinks === false ? { followSymlinks: false } : {}),
    },
  };
}

export async function bindOptionalAssessmentControlText(
  filePath: string,
  options: { readonly followSymlinks?: boolean } = {},
): Promise<BoundAssessmentControlText> {
  validatePath(filePath);
  try {
    return await bindRequiredAssessmentControlText(filePath, options);
  } catch (error: unknown) {
    if (!(error instanceof ProcessFailure) || error.code !== "READ_FAILED") {
      throw error;
    }
    try {
      if (options.followSymlinks === false) {
        await lstat(filePath);
      } else {
        await stat(filePath);
      }
    } catch (metadataError: unknown) {
      if (errorCode(metadataError) === "ENOENT") {
        return {
          text: null,
          file: {
            path: filePath,
            digest: null,
            identity: null,
            ...(options.followSymlinks === false ? { followSymlinks: false } : {}),
          },
        };
      }
    }
    throw error;
  }
}

export function copyAssessmentControlFiles(
  files: readonly BoundAssessmentControlFile[],
): BoundAssessmentControlFile[] {
  if (files.length > MAX_CONTROL_FILES) {
    fail(
      "TOO_MANY_ASSESSMENT_CONTROL_FILES",
      "saved-plan assessment exceeds the control-file limit",
    );
  }
  const seen = new Set<string>();
  return files.map((file) => {
    validatePath(file.path);
    if (seen.has(file.path)) {
      fail(
        "DUPLICATE_ASSESSMENT_CONTROL_FILE",
        "saved-plan assessment control files must be unique",
      );
    }
    seen.add(file.path);
    if (
      file.digest !== null
      && (
        !/^[0-9a-f]{64}$/.test(file.digest.sha256)
        || file.digest.size < 0n
        || file.digest.size > CONTROL_READ_LIMITS.maxFileBytes
      )
    ) {
      fail(
        "INVALID_ASSESSMENT_CONTROL_FILE",
        "saved-plan assessment control binding is invalid",
      );
    }
    return {
      path: file.path,
      digest: file.digest === null ? null : { ...file.digest },
      ...(file.identity === undefined
        ? {}
        : { identity: file.identity === null ? null : { ...file.identity } }),
      ...(file.followSymlinks === undefined
        ? {}
        : { followSymlinks: file.followSymlinks }),
    };
  });
}

async function assertStillAbsent(filePath: string, followSymlinks: boolean): Promise<void> {
  try {
    if (followSymlinks) {
      await stat(filePath);
    } else {
      await lstat(filePath);
    }
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
    fail(
      "ASSESSMENT_CONTROL_CHANGED",
      "saved-plan assessment control input changed during assessment",
    );
  }
  fail(
    "ASSESSMENT_CONTROL_CHANGED",
    "saved-plan assessment control input changed during assessment",
  );
}

export async function recheckAssessmentControlFiles(
  files: readonly BoundAssessmentControlFile[],
): Promise<void> {
  const budget = new ReadBudget(CONTROL_READ_LIMITS);
  for (const file of files) {
    if (file.digest === null) {
      await assertStillAbsent(file.path, file.followSymlinks ?? true);
      continue;
    }
    const followSymlinks = file.followSymlinks ?? true;
    let before: Awaited<ReturnType<typeof stat>>;
    try {
      before = followSymlinks
        ? await stat(file.path, { bigint: true })
        : await lstat(file.path, { bigint: true });
      if (!before.isFile() || (!followSymlinks && before.isSymbolicLink())) {
        throw new Error("not regular");
      }
      if (
        file.identity !== undefined
        && file.identity !== null
        && (before.dev !== file.identity.dev || before.ino !== file.identity.ino)
      ) {
        throw new Error("identity changed");
      }
    } catch {
      fail(
        "ASSESSMENT_CONTROL_CHANGED",
        "saved-plan assessment control input changed during assessment",
      );
    }
    let current: StableFileDigest;
    try {
      current = await sha256StableFile(file.path, budget, {
        followSymlinks,
      });
    } catch {
      fail(
        "ASSESSMENT_CONTROL_CHANGED",
        "saved-plan assessment control input changed during assessment",
      );
    }
    if (
      current.sha256 !== file.digest.sha256
      || current.size !== file.digest.size
    ) {
      fail(
        "ASSESSMENT_CONTROL_CHANGED",
        "saved-plan assessment control input changed during assessment",
      );
    }
    try {
      const after = followSymlinks
        ? await stat(file.path, { bigint: true })
        : await lstat(file.path, { bigint: true });
      if (
        !after.isFile()
        || (!followSymlinks && after.isSymbolicLink())
        || after.dev !== before.dev
        || after.ino !== before.ino
      ) {
        throw new Error("identity changed");
      }
    } catch {
      fail(
        "ASSESSMENT_CONTROL_CHANGED",
        "saved-plan assessment control input changed during assessment",
      );
    }
  }
}
