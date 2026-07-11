import { stat } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  ReadBudget,
  readBoundedFileBytes,
  sha256StableFile,
  type BoundedReadLimits,
  type StableFileDigest,
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
): Promise<BoundAssessmentControlText> {
  validatePath(filePath);
  const source = await readBoundedFileBytes(
    filePath,
    new ReadBudget(CONTROL_READ_LIMITS),
    { followSymlinks: true },
  );
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(source.bytes);
  } catch {
    fail("INVALID_UTF8", "assessment control input is not valid UTF-8");
  }
  return {
    text,
    file: { path: filePath, digest: source.digest },
  };
}

export async function bindOptionalAssessmentControlText(
  filePath: string,
): Promise<BoundAssessmentControlText> {
  validatePath(filePath);
  try {
    return await bindRequiredAssessmentControlText(filePath);
  } catch (error: unknown) {
    if (!(error instanceof ProcessFailure) || error.code !== "READ_FAILED") {
      throw error;
    }
    try {
      await stat(filePath);
    } catch (metadataError: unknown) {
      if (errorCode(metadataError) === "ENOENT") {
        return { text: null, file: { path: filePath, digest: null } };
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
    };
  });
}

async function assertStillAbsent(filePath: string): Promise<void> {
  try {
    await stat(filePath);
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
      await assertStillAbsent(file.path);
      continue;
    }
    let current: StableFileDigest;
    try {
      current = await sha256StableFile(file.path, budget, {
        followSymlinks: true,
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
  }
}
