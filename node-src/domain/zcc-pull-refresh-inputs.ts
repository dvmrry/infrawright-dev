import { lstat } from "node:fs/promises";
import path from "node:path";

import {
  ReadBudget,
  readBoundedFileBytes,
  readBoundedUtf8File,
  sha256StableFile,
  type BoundedReadLimits,
  type StableReadHooks,
} from "../io/bounded-files.js";
import { parseGeneratedImports } from "./import-moves.js";
import { ProcessFailure } from "./errors.js";
import { pythonPosixJoin, pythonPosixRealpath } from "./paths.js";
import type { ZccArtifactTarget } from "./zcc-pull-artifacts.js";
import type { ZccRefreshBaselineInputState } from "./zcc-pull-refresh.js";

const REFRESH_BASELINE_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 3,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 96n * 1024n * 1024n,
  maxFileBytes: 32n * 1024n * 1024n,
};

export interface BoundZccRefreshImports {
  readonly absolutePath: string;
  readonly logicalPath: string;
  readonly initialPhysicalPath: string;
  readonly identity: {
    readonly dev: bigint;
    readonly ino: bigint;
  };
  readonly text: string;
  readonly digest: {
    readonly sha256: string;
    readonly size: bigint;
  };
}

export interface BoundZccRefreshOptionalArtifact {
  readonly absolutePath: string;
  readonly logicalPath: string;
  readonly initialPhysicalPath: string;
  readonly identity: {
    readonly dev: bigint;
    readonly ino: bigint;
  } | null;
  readonly bytes: Buffer | null;
  readonly digest: {
    readonly sha256: string;
    readonly size: bigint;
  } | null;
}

export interface BoundZccRefreshAbsentArtifact {
  readonly absolutePath: string;
  readonly logicalPath: string;
  readonly initialPhysicalPath: string;
  readonly code: string;
  readonly message: string;
}

export interface BoundZccRefreshInputs {
  readonly tfvars: BoundZccRefreshOptionalArtifact;
  readonly imports: BoundZccRefreshImports;
  readonly lookup:
    | BoundZccRefreshOptionalArtifact
    | BoundZccRefreshAbsentArtifact;
  readonly moves: BoundZccRefreshAbsentArtifact;
  readonly pendingMoves: BoundZccRefreshAbsentArtifact;
  readonly alternateHcl: BoundZccRefreshAbsentArtifact;
  readonly generatedBindings: BoundZccRefreshAbsentArtifact;
  readonly unsupported: readonly BoundZccRefreshAbsentArtifact[];
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "domain",
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

function resolvedPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

function siblingPath(
  importsPath: string,
  suffix: "_moves.tf" | "_moves.pending.json",
): string {
  if (!importsPath.endsWith("_imports.tf")) {
    return fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "refresh imports target does not use the canonical suffix",
    );
  }
  return importsPath.slice(0, -"_imports.tf".length) + suffix;
}

function absentArtifact(options: {
  readonly workspace: string;
  readonly logicalPath: string;
  readonly code: string;
  readonly message: string;
}): BoundZccRefreshAbsentArtifact {
  const absolutePath = resolvedPath(options.workspace, options.logicalPath);
  return {
    absolutePath,
    logicalPath: options.logicalPath,
    initialPhysicalPath: pythonPosixRealpath(absolutePath),
    code: options.code,
    message: options.message,
  };
}

async function bindAbsentArtifact(
  artifact: BoundZccRefreshAbsentArtifact,
): Promise<void> {
  try {
    await lstat(artifact.absolutePath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
    return fail(
      "REFRESH_ARTIFACT_CHECK_FAILED",
      "unable to verify a refresh artifact precondition",
      "io",
    );
  }
  return fail(artifact.code, artifact.message);
}

async function bindImports(options: {
  readonly absolutePath: string;
  readonly logicalPath: string;
  readonly resourceType: string;
  readonly budget: ReadBudget;
  readonly readHooks?: StableReadHooks;
}): Promise<BoundZccRefreshImports> {
  const initialPhysicalPath = pythonPosixRealpath(options.absolutePath);
  let identity: { readonly dev: bigint; readonly ino: bigint };
  try {
    const metadata = await lstat(options.absolutePath, { bigint: true });
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "REFRESH_IMPORTS_NOT_REGULAR",
        "refresh imports baseline must be a regular non-symlink file",
      );
    }
    identity = { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return fail(
        "REFRESH_IMPORTS_MISSING",
        "refresh compilation requires a present prior imports artifact",
      );
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "REFRESH_IMPORTS_READ_FAILED",
      "unable to inspect the refresh imports baseline",
      "io",
    );
  }

  let snapshot: Awaited<ReturnType<typeof readBoundedUtf8File>>;
  try {
    snapshot = await readBoundedUtf8File(
      options.absolutePath,
      options.budget,
      options.readHooks === undefined ? {} : { hooks: options.readHooks },
    );
    const current = await lstat(options.absolutePath, { bigint: true });
    if (
      !current.isFile()
      || current.isSymbolicLink()
      || current.dev !== identity.dev
      || current.ino !== identity.ino
      || pythonPosixRealpath(options.absolutePath) !== initialPhysicalPath
    ) {
      return fail(
        "REFRESH_IMPORTS_CHANGED",
        "refresh imports baseline changed during compilation",
        "io",
      );
    }
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && (
        error.code === "INVALID_UTF8"
        || error.code === "FILE_LIMIT_EXCEEDED"
        || error.code === "BYTE_BUDGET_EXCEEDED"
        || error.code === "REFRESH_IMPORTS_CHANGED"
      )
    ) {
      throw error;
    }
    return fail(
      "REFRESH_IMPORTS_CHANGED",
      "refresh imports baseline changed during compilation",
      "io",
    );
  }

  try {
    parseGeneratedImports(options.resourceType, snapshot.text);
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && error.code === "IMPORT_MOVE_LIMIT_EXCEEDED"
    ) {
      throw error;
    }
    return fail(
      "REFRESH_IMPORTS_NONCANONICAL",
      "refresh imports baseline is not canonical Infrawright output",
    );
  }
  return {
    absolutePath: options.absolutePath,
    logicalPath: options.logicalPath,
    initialPhysicalPath,
    identity,
    text: snapshot.text,
    digest: snapshot.digest,
  };
}

async function bindOptionalArtifact(options: {
  readonly absolutePath: string;
  readonly logicalPath: string;
  readonly budget: ReadBudget;
}): Promise<BoundZccRefreshOptionalArtifact> {
  const initialPhysicalPath = pythonPosixRealpath(options.absolutePath);
  let identity: { readonly dev: bigint; readonly ino: bigint };
  try {
    const metadata = await lstat(options.absolutePath, { bigint: true });
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "REFRESH_BASELINE_NOT_REGULAR",
        "refresh baseline artifacts must be regular non-symlink files",
      );
    }
    identity = { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return {
        absolutePath: options.absolutePath,
        logicalPath: options.logicalPath,
        initialPhysicalPath,
        identity: null,
        bytes: null,
        digest: null,
      };
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "REFRESH_BASELINE_READ_FAILED",
      "unable to inspect a refresh baseline artifact",
      "io",
    );
  }

  try {
    const snapshot = await readBoundedFileBytes(
      options.absolutePath,
      options.budget,
    );
    const current = await lstat(options.absolutePath, { bigint: true });
    if (
      !current.isFile()
      || current.isSymbolicLink()
      || current.dev !== identity.dev
      || current.ino !== identity.ino
      || pythonPosixRealpath(options.absolutePath) !== initialPhysicalPath
    ) {
      return fail(
        "REFRESH_BASELINE_CHANGED",
        "refresh baseline artifact changed during compilation",
        "io",
      );
    }
    return {
      absolutePath: options.absolutePath,
      logicalPath: options.logicalPath,
      initialPhysicalPath,
      identity,
      bytes: Buffer.from(snapshot.bytes),
      digest: snapshot.digest,
    };
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && (
        error.code === "FILE_LIMIT_EXCEEDED"
        || error.code === "BYTE_BUDGET_EXCEEDED"
        || error.code === "REFRESH_BASELINE_CHANGED"
      )
    ) {
      throw error;
    }
    return fail(
      "REFRESH_BASELINE_CHANGED",
      "refresh baseline artifact changed during compilation",
      "io",
    );
  }
}

/** Bind every raw-transform refresh baseline and adjacent refusal state. */
export async function bindZccRefreshInputs(options: {
  readonly workspace: string;
  readonly target: ZccArtifactTarget;
  readonly priorImportsRead?: StableReadHooks;
}): Promise<BoundZccRefreshInputs> {
  const moves = absentArtifact({
    workspace: options.workspace,
    logicalPath: siblingPath(options.target.importsPath, "_moves.tf"),
    code: "REFRESH_MOVES_EXIST",
    message: "refresh compilation refuses a pre-existing moves artifact",
  });
  const pendingMoves = absentArtifact({
    workspace: options.workspace,
    logicalPath: siblingPath(
      options.target.importsPath,
      "_moves.pending.json",
    ),
    code: "REFRESH_PENDING_MOVES_EXIST",
    message: "refresh compilation refuses an in-flight move transition",
  });
  const alternateHcl = absentArtifact({
    workspace: options.workspace,
    logicalPath: options.target.configPath.endsWith(".json")
      ? options.target.configPath.slice(0, -".json".length)
      : options.target.configPath,
    code: "UNSUPPORTED_REFRESH_HCL_ARTIFACT",
    message: "refresh compilation refuses a stale HCL tfvars artifact",
  });
  const generatedBindings = absentArtifact({
    workspace: options.workspace,
    logicalPath: pythonPosixJoin(
      path.posix.dirname(options.target.configPath),
      `${options.target.resourceType}.generated.expressions.json`,
    ),
    code: "UNSUPPORTED_REFRESH_GENERATED_BINDINGS",
    message: "refresh compilation refuses generated reference bindings",
  });
  const staleLookup = options.target.lookupPath === null
    ? absentArtifact({
        workspace: options.workspace,
        logicalPath: pythonPosixJoin(
          path.posix.dirname(options.target.configPath),
          `${options.target.resourceType}.lookup.json`,
        ),
        code: "UNSUPPORTED_REFRESH_LOOKUP_ARTIFACT",
        message: "refresh compilation refuses a stale lookup artifact",
      })
    : null;
  const unsupported = [
    alternateHcl,
    generatedBindings,
    ...(staleLookup === null ? [] : [staleLookup]),
  ];
  await bindAbsentArtifact(moves);
  await bindAbsentArtifact(pendingMoves);
  for (const artifact of unsupported) {
    await bindAbsentArtifact(artifact);
  }

  const budget = new ReadBudget(REFRESH_BASELINE_READ_LIMITS);
  const imports = await bindImports({
    absolutePath: resolvedPath(options.workspace, options.target.importsPath),
    logicalPath: options.target.importsPath,
    resourceType: options.target.resourceType,
    budget,
    ...(options.priorImportsRead === undefined
      ? {}
      : { readHooks: options.priorImportsRead }),
  });
  const tfvars = await bindOptionalArtifact({
    absolutePath: resolvedPath(options.workspace, options.target.configPath),
    logicalPath: options.target.configPath,
    budget,
  });
  const lookup = options.target.lookupPath === null
    ? staleLookup
    : await bindOptionalArtifact({
        absolutePath: resolvedPath(options.workspace, options.target.lookupPath),
        logicalPath: options.target.lookupPath,
        budget,
      });
  if (lookup === null) {
    return fail("INTERNAL_ERROR", "refresh lookup target is unresolved", "internal");
  }
  return {
    tfvars,
    imports,
    lookup,
    moves,
    pendingMoves,
    alternateHcl,
    generatedBindings,
    unsupported,
  };
}

async function recheckImports(
  imports: BoundZccRefreshImports,
  budget: ReadBudget,
): Promise<void> {
  if (pythonPosixRealpath(imports.absolutePath) !== imports.initialPhysicalPath) {
    return fail(
      "REFRESH_IMPORTS_CHANGED",
      "refresh imports baseline changed during compilation",
      "io",
    );
  }
  try {
    const before = await lstat(imports.absolutePath, { bigint: true });
    if (
      !before.isFile()
      || before.isSymbolicLink()
      || before.dev !== imports.identity.dev
      || before.ino !== imports.identity.ino
    ) {
      return fail(
        "REFRESH_IMPORTS_CHANGED",
        "refresh imports baseline changed during compilation",
        "io",
      );
    }
    const current = await sha256StableFile(imports.absolutePath, budget);
    const after = await lstat(imports.absolutePath, { bigint: true });
    if (
      current.sha256 !== imports.digest.sha256
      || current.size !== imports.digest.size
      || after.dev !== imports.identity.dev
      || after.ino !== imports.identity.ino
    ) {
      return fail(
        "REFRESH_IMPORTS_CHANGED",
        "refresh imports baseline changed during compilation",
        "io",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "REFRESH_IMPORTS_CHANGED") {
      throw error;
    }
    return fail(
      "REFRESH_IMPORTS_CHANGED",
      "refresh imports baseline changed during compilation",
      "io",
    );
  }
}

async function recheckOptionalArtifact(
  artifact: BoundZccRefreshOptionalArtifact,
  budget: ReadBudget,
): Promise<void> {
  if (pythonPosixRealpath(artifact.absolutePath) !== artifact.initialPhysicalPath) {
    return fail(
      "REFRESH_BASELINE_CHANGED",
      "refresh baseline artifact changed during compilation",
      "io",
    );
  }
  if (artifact.digest === null) {
    try {
      await lstat(artifact.absolutePath);
    } catch (error: unknown) {
      if (errorCode(error) === "ENOENT") {
        return;
      }
    }
    return fail(
      "REFRESH_BASELINE_CHANGED",
      "refresh baseline artifact changed during compilation",
      "io",
    );
  }
  if (artifact.identity === null || artifact.bytes === null) {
    return fail("INTERNAL_ERROR", "refresh baseline binding is incomplete", "internal");
  }
  try {
    const before = await lstat(artifact.absolutePath, { bigint: true });
    if (
      !before.isFile()
      || before.isSymbolicLink()
      || before.dev !== artifact.identity.dev
      || before.ino !== artifact.identity.ino
    ) {
      return fail(
        "REFRESH_BASELINE_CHANGED",
        "refresh baseline artifact changed during compilation",
        "io",
      );
    }
    const current = await sha256StableFile(artifact.absolutePath, budget);
    const after = await lstat(artifact.absolutePath, { bigint: true });
    if (
      current.sha256 !== artifact.digest.sha256
      || current.size !== artifact.digest.size
      || after.dev !== artifact.identity.dev
      || after.ino !== artifact.identity.ino
    ) {
      return fail(
        "REFRESH_BASELINE_CHANGED",
        "refresh baseline artifact changed during compilation",
        "io",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "REFRESH_BASELINE_CHANGED") {
      throw error;
    }
    return fail(
      "REFRESH_BASELINE_CHANGED",
      "refresh baseline artifact changed during compilation",
      "io",
    );
  }
}

async function recheckAbsentArtifact(
  artifact: BoundZccRefreshAbsentArtifact,
): Promise<void> {
  if (pythonPosixRealpath(artifact.absolutePath) !== artifact.initialPhysicalPath) {
    return fail(
      "REFRESH_ARTIFACT_CHANGED",
      "refresh artifact precondition changed during compilation",
      "io",
    );
  }
  try {
    await lstat(artifact.absolutePath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
  }
  return fail(
    "REFRESH_ARTIFACT_CHANGED",
    "refresh artifact precondition changed during compilation",
    "io",
  );
}

/** Recheck the complete refresh CAS baseline with one aggregate budget. */
export async function recheckZccRefreshInputs(
  inputs: BoundZccRefreshInputs,
): Promise<void> {
  const budget = new ReadBudget(REFRESH_BASELINE_READ_LIMITS);
  await recheckImports(inputs.imports, budget);
  await recheckOptionalArtifact(inputs.tfvars, budget);
  if ("digest" in inputs.lookup) {
    await recheckOptionalArtifact(inputs.lookup, budget);
  }
  await recheckAbsentArtifact(inputs.moves);
  await recheckAbsentArtifact(inputs.pendingMoves);
  for (const artifact of inputs.unsupported) {
    await recheckAbsentArtifact(artifact);
  }
}

export function zccRefreshBaselineInput(
  artifact: BoundZccRefreshOptionalArtifact,
): ZccRefreshBaselineInputState {
  if (artifact.bytes === null) {
    return { path: artifact.logicalPath, state: "absent" };
  }
  return {
    path: artifact.logicalPath,
    state: "present",
    content: Buffer.from(artifact.bytes),
  };
}
