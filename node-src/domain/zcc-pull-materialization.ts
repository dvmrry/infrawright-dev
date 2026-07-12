import { createHash, randomBytes } from "node:crypto";
import { constants, type BigIntStats } from "node:fs";
import {
  link,
  lstat,
  mkdir,
  open,
  realpath,
  unlink,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";

import {
  schemaErrorDetails,
  validateZccPullArtifactSet,
  validateZccPullArtifactParity,
} from "../contracts/validators.js";
import { requireExactPublisherAuthority } from "../io/publisher-guard.js";
import { ProcessFailure } from "./errors.js";
import { pythonPosixRealpath } from "./paths.js";
import type {
  ZccPullResourceType,
  ZccPullArtifactSet,
  ZccTextArtifact,
} from "./zcc-pull-artifacts.js";
import {
  compareZccPullArtifactDigests,
  type ZccPullArtifactParity,
} from "./zcc-pull-parity.js";

const MAX_ARTIFACT_BYTES = 32 * 1024 * 1024;
const MAX_ARTIFACTS = 3;
const MAX_TARGET_BYTES = 4096;
const MAX_TARGET_COMPONENTS = 64;
const MAX_TARGET_COMPONENT_BYTES = 255;
const TEMP_ATTEMPTS = 16;

export type ZccPullMaterializedArtifactName = "imports" | "lookup" | "tfvars";

/** Common byte-bearing shape shared by the two immutable ZCC bootstrap lanes. */
export interface ZccBootstrapMaterializationCandidate {
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly artifacts: {
    readonly tfvars: ZccTextArtifact;
    readonly imports: ZccTextArtifact;
    readonly lookup: ZccTextArtifact | null;
  };
}

export interface ZccBootstrapMaterializationResultOptions<
  Candidate extends ZccBootstrapMaterializationCandidate,
  Verification,
> {
  readonly candidate: Candidate;
  readonly created: readonly ZccPullMaterializedArtifactName[];
  readonly reused: readonly ZccPullMaterializedArtifactName[];
  readonly verification: Verification;
}

interface MetadataBinding {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}

interface DirectoryBinding {
  readonly absolutePath: string;
  metadata: MetadataBinding;
}

interface FileBinding {
  readonly absolutePath: string;
  readonly metadata: MetadataBinding;
}

interface PreparedArtifact {
  readonly name: ZccPullMaterializedArtifactName;
  readonly descriptor: ZccTextArtifact;
  readonly bytes: Buffer;
  readonly absolutePath: string;
  readonly parentPath: string;
  readonly initial: FileBinding | null;
}

interface StagedArtifact {
  readonly artifact: PreparedArtifact;
  readonly tempPath: string;
  metadata: MetadataBinding;
  aliasPresent: boolean;
}

interface BoundAuthority {
  readonly root: DirectoryBinding;
  readonly directories: Map<string, DirectoryBinding>;
}

export interface ZccPullMaterializationHooks {
  readonly afterPreflight?: () => void | Promise<void>;
  readonly afterDirectoriesReady?: () => void | Promise<void>;
  /** Test seam after exclusive temp creation but before its identity is bound. */
  readonly afterTempOpen?: (
    name: ZccPullMaterializedArtifactName,
    index: number,
  ) => void | Promise<void>;
  readonly afterStaged?: () => void | Promise<void>;
  readonly beforePrepublishRecheck?: () => void | Promise<void>;
  readonly beforePublish?: (
    name: ZccPullMaterializedArtifactName,
    index: number,
  ) => void | Promise<void>;
  /** Test seam after link(2) succeeds but before any post-link verification. */
  readonly afterLink?: (
    name: ZccPullMaterializedArtifactName,
    index: number,
  ) => void | Promise<void>;
  readonly afterPublish?: (
    name: ZccPullMaterializedArtifactName,
    index: number,
  ) => void | Promise<void>;
  readonly beforePostpublishRecheck?: () => void | Promise<void>;
}

export interface ZccPullArtifactMaterialization {
  readonly kind: "infrawright.zcc_pull_artifact_materialization";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly product: "zcc";
  readonly resource_type: ZccPullArtifactSet["resource_type"];
  readonly tenant: string;
  readonly status: "complete";
  readonly publication: {
    readonly policy: "create_or_verify_exact";
    readonly created: readonly ZccPullMaterializedArtifactName[];
    readonly reused: readonly ZccPullMaterializedArtifactName[];
  };
  readonly verification: ZccPullArtifactParity;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "domain",
  retryable = false,
): never {
  throw new ProcessFailure({ code, category, message, retryable });
}

function errorCode(error: unknown): string | null {
  return typeof error === "object"
    && error !== null
    && "code" in error
    && typeof error.code === "string"
    ? error.code
    : null;
}

function metadataOf(metadata: BigIntStats): MetadataBinding {
  return {
    dev: metadata.dev,
    ino: metadata.ino,
    size: metadata.size,
    mtimeNs: metadata.mtimeNs,
    ctimeNs: metadata.ctimeNs,
  };
}

function sameMetadata(left: MetadataBinding, right: MetadataBinding): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.size === right.size
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

function sameObject(left: MetadataBinding, right: MetadataBinding): boolean {
  return left.dev === right.dev && left.ino === right.ino;
}

function containedPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative !== ""
    && relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative);
}

function safeJsonValue(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => safeJsonValue(entry)).join(",")}]`;
  }
  const record = value as Readonly<Record<string, unknown>>;
  return `{${Object.keys(record).sort().map((key) => {
    return `${JSON.stringify(key)}:${safeJsonValue(record[key])}`;
  }).join(",")}}`;
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableCopy(entry)));
  }
  if (value !== null && typeof value === "object") {
    const result: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      result[key] = immutableCopy(
        (value as Readonly<Record<string, unknown>>)[key],
      );
    }
    return Object.freeze(result);
  }
  return value;
}

function snapshotMaterializationValues(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullArtifactParity;
}): {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullArtifactParity;
} {
  if (!validateZccPullArtifactSet(options.candidate)) {
    throw new ProcessFailure({
      code: "INVALID_ZCC_ARTIFACT_CANDIDATE",
      category: "internal",
      message: "compiled pull artifact candidate failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactSet.errors),
    });
  }
  if (!validateZccPullArtifactParity(options.assertion)) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_ASSERTION",
      category: "domain",
      message: "the parity assertion failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactParity.errors),
    });
  }
  return {
    candidate: immutableCopy(options.candidate) as ZccPullArtifactSet,
    assertion: immutableCopy(options.assertion) as ZccPullArtifactParity,
  };
}

async function invokeHook(hook: (() => void | Promise<void>) | undefined): Promise<void> {
  if (hook === undefined) {
    return;
  }
  try {
    await hook();
  } catch {
    fail("MATERIALIZATION_HOOK_FAILED", "materialization test hook failed", "internal");
  }
}

async function invokeArtifactHook(
  hook: ((
    name: ZccPullMaterializedArtifactName,
    index: number,
  ) => void | Promise<void>) | undefined,
  name: ZccPullMaterializedArtifactName,
  index: number,
): Promise<void> {
  if (hook === undefined) {
    return;
  }
  try {
    await hook(name, index);
  } catch {
    fail("MATERIALIZATION_HOOK_FAILED", "materialization test hook failed", "internal");
  }
}

async function bindAuthority(outputRoot: string): Promise<BoundAuthority> {
  if (
    !path.isAbsolute(outputRoot)
    || outputRoot.includes("\0")
    || path.resolve(outputRoot) !== outputRoot
    || path.parse(outputRoot).root === outputRoot
  ) {
    return fail(
      "INVALID_MATERIALIZATION_AUTHORITY",
      "output_root must be a canonical absolute non-root directory",
      "io",
    );
  }
  try {
    const canonical = await realpath(outputRoot);
    const metadata = await lstat(outputRoot, { bigint: true });
    if (
      canonical !== outputRoot
      || !metadata.isDirectory()
      || metadata.isSymbolicLink()
    ) {
      return fail(
        "INVALID_MATERIALIZATION_AUTHORITY",
        "output_root must be an existing canonical non-symlink directory",
        "io",
      );
    }
    const root: DirectoryBinding = {
      absolutePath: outputRoot,
      metadata: metadataOf(metadata),
    };
    return { root, directories: new Map([[outputRoot, root]]) };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "INVALID_MATERIALIZATION_AUTHORITY",
      "output_root could not be bound as a write authority",
      "io",
    );
  }
}

async function refreshDirectory(binding: DirectoryBinding): Promise<void> {
  try {
    const current = await lstat(binding.absolutePath, { bigint: true });
    const metadata = metadataOf(current);
    if (
      !current.isDirectory()
      || current.isSymbolicLink()
      || !sameObject(binding.metadata, metadata)
    ) {
      return fail(
        "MATERIALIZATION_DIRECTORY_CHANGED",
        "a bound materialization directory changed",
        "io",
      );
    }
    binding.metadata = metadata;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "MATERIALIZATION_DIRECTORY_CHANGED",
      "a bound materialization directory changed",
      "io",
    );
  }
}

async function recheckDirectory(binding: DirectoryBinding): Promise<void> {
  try {
    const current = await lstat(binding.absolutePath, { bigint: true });
    if (
      !current.isDirectory()
      || current.isSymbolicLink()
      || !sameMetadata(binding.metadata, metadataOf(current))
    ) {
      return fail(
        "MATERIALIZATION_DIRECTORY_CHANGED",
        "a bound materialization directory changed",
        "io",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "MATERIALIZATION_DIRECTORY_CHANGED",
      "a bound materialization directory changed",
      "io",
    );
  }
}

async function recheckDirectories(authority: BoundAuthority): Promise<void> {
  const bindings = [...authority.directories.values()].sort(
    (left, right) => left.absolutePath.length - right.absolutePath.length,
  );
  for (const binding of bindings) {
    await recheckDirectory(binding);
  }
}

function resolveAuthorizedPath(
  outputRoot: string,
  pathBase: string,
  nominatedPath: string,
): string {
  if (nominatedPath.includes("\0")) {
    return fail(
      "MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target is outside output_root",
    );
  }
  const absolute = path.isAbsolute(nominatedPath)
    ? path.resolve(nominatedPath)
    : path.resolve(pathBase, nominatedPath);
  if (!containedPath(absolute, outputRoot)) {
    return fail(
      "MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target is outside output_root",
    );
  }
  const relative = path.relative(outputRoot, absolute);
  const components = relative.split(path.sep).filter(Boolean);
  if (
    Buffer.byteLength(relative, "utf8") > MAX_TARGET_BYTES
    || components.length > MAX_TARGET_COMPONENTS
    || components.some(
      (component) => Buffer.byteLength(component, "utf8") > MAX_TARGET_COMPONENT_BYTES,
    )
  ) {
    return fail(
      "MATERIALIZATION_PATH_LIMIT_EXCEEDED",
      "an artifact target exceeds the bounded path contract",
    );
  }
  const physical = pythonPosixRealpath(absolute);
  if (!containedPath(physical, outputRoot)) {
    return fail(
      "MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target does not resolve beneath output_root",
    );
  }
  return absolute;
}

async function bindExistingAncestors(
  authority: BoundAuthority,
  targetPath: string,
): Promise<void> {
  const parentPath = path.dirname(targetPath);
  const relative = path.relative(authority.root.absolutePath, parentPath);
  let current = authority.root.absolutePath;
  for (const component of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    const prior = authority.directories.get(current);
    if (prior !== undefined) {
      await recheckDirectory(prior);
      continue;
    }
    try {
      const metadata = await lstat(current, { bigint: true });
      if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
        return fail(
          "MATERIALIZATION_DIRECTORY_UNSAFE",
          "an artifact parent is not a regular directory",
          "io",
        );
      }
      authority.directories.set(current, {
        absolutePath: current,
        metadata: metadataOf(metadata),
      });
    } catch (error: unknown) {
      if (errorCode(error) === "ENOENT") {
        return;
      }
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "MATERIALIZATION_DIRECTORY_CHECK_FAILED",
        "an artifact parent could not be inspected",
        "io",
      );
    }
  }
}

async function absentTarget(targetPath: string): Promise<void> {
  try {
    await lstat(targetPath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
    return fail(
      "MATERIALIZATION_TARGET_CHECK_FAILED",
      "an unsupported artifact target could not be inspected",
      "io",
    );
  }
  return fail(
    "UNSUPPORTED_MATERIALIZATION_RESIDUE",
    "bootstrap materialization found an unsupported prior artifact",
  );
}

async function readHandleBytes(handle: FileHandle, size: number): Promise<Buffer> {
  const bytes = Buffer.allocUnsafe(size);
  let offset = 0;
  while (offset < size) {
    const result = await handle.read(bytes, offset, size - offset, offset);
    if (result.bytesRead <= 0) {
      return fail(
        "MATERIALIZATION_FILE_CHANGED",
        "an artifact file changed while it was verified",
        "io",
      );
    }
    offset += result.bytesRead;
  }
  const tail = Buffer.allocUnsafe(1);
  const after = await handle.read(tail, 0, 1, size);
  if (after.bytesRead !== 0) {
    return fail(
      "MATERIALIZATION_FILE_CHANGED",
      "an artifact file changed while it was verified",
      "io",
    );
  }
  return bytes;
}

async function bindExactFile(
  targetPath: string,
  expected: Buffer,
  prior: MetadataBinding | null = null,
): Promise<FileBinding | null> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      targetPath,
      constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW,
    );
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return null;
    }
    return fail(
      "MATERIALIZATION_TARGET_UNSAFE",
      "an artifact target is not a stable regular file",
      "io",
    );
  }
  try {
    const beforeStat = await handle.stat({ bigint: true });
    const before = metadataOf(beforeStat);
    if (
      !beforeStat.isFile()
      || before.size !== BigInt(expected.length)
      || (prior !== null && !sameMetadata(prior, before))
    ) {
      return fail(
        prior === null
          ? "MATERIALIZATION_TARGET_MISMATCH"
          : "MATERIALIZATION_TARGET_CHANGED",
        prior === null
          ? "an existing artifact does not exactly match the candidate"
          : "an artifact target changed during materialization",
        "io",
      );
    }
    const bytes = await readHandleBytes(handle, expected.length);
    const afterStat = await handle.stat({ bigint: true });
    const pathStat = await lstat(targetPath, { bigint: true });
    const after = metadataOf(afterStat);
    const pathMetadata = metadataOf(pathStat);
    if (
      !pathStat.isFile()
      || pathStat.isSymbolicLink()
      || !sameMetadata(before, after)
      || !sameMetadata(before, pathMetadata)
      || !bytes.equals(expected)
    ) {
      return fail(
        prior === null
          ? "MATERIALIZATION_TARGET_MISMATCH"
          : "MATERIALIZATION_TARGET_CHANGED",
        prior === null
          ? "an existing artifact does not exactly match the candidate"
          : "an artifact target changed during materialization",
        "io",
      );
    }
    return { absolutePath: targetPath, metadata: before };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "MATERIALIZATION_TARGET_CHECK_FAILED",
      "an artifact target could not be verified",
      "io",
    );
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function assertStillAbsent(targetPath: string): Promise<void> {
  try {
    await lstat(targetPath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
  }
  return fail(
    "MATERIALIZATION_TARGET_CHANGED",
    "an artifact target changed during materialization",
    "io",
  );
}

async function refreshExactFile(
  targetPath: string,
  expected: Buffer,
  prior: MetadataBinding,
): Promise<FileBinding> {
  const current = await bindExactFile(targetPath, expected);
  if (current === null || !sameObject(prior, current.metadata)) {
    return fail(
      "MATERIALIZATION_TARGET_CHANGED",
      "an artifact target changed during materialization",
      "io",
    );
  }
  return current;
}

function artifactBytes(candidate: ZccBootstrapMaterializationCandidate): readonly {
  readonly name: ZccPullMaterializedArtifactName;
  readonly descriptor: ZccTextArtifact;
  readonly bytes: Buffer;
}[] {
  const entries: Array<{
    readonly name: ZccPullMaterializedArtifactName;
    readonly descriptor: ZccTextArtifact;
    readonly bytes: Buffer;
  }> = [
    {
      name: "imports",
      descriptor: candidate.artifacts.imports,
      bytes: Buffer.from(candidate.artifacts.imports.content, "utf8"),
    },
    ...(candidate.artifacts.lookup === null
      ? []
      : [{
          name: "lookup" as const,
          descriptor: candidate.artifacts.lookup,
          bytes: Buffer.from(candidate.artifacts.lookup.content, "utf8"),
        }]),
    {
      name: "tfvars",
      descriptor: candidate.artifacts.tfvars,
      bytes: Buffer.from(candidate.artifacts.tfvars.content, "utf8"),
    },
  ];
  if (entries.length > MAX_ARTIFACTS) {
    return fail(
      "MATERIALIZATION_LIMIT_EXCEEDED",
      "candidate exceeds the artifact-count limit",
    );
  }
  let total = 0;
  for (const entry of entries) {
    total += entry.bytes.length;
    const digest = createHash("sha256").update(entry.bytes).digest("hex");
    if (
      entry.bytes.length !== entry.descriptor.size_bytes
      || digest !== entry.descriptor.sha256
    ) {
      return fail(
        "INVALID_ZCC_ARTIFACT_CANDIDATE",
        "compiled candidate content does not match its descriptor",
        "internal",
      );
    }
  }
  if (total > MAX_ARTIFACT_BYTES) {
    return fail(
      "MATERIALIZATION_LIMIT_EXCEEDED",
      "candidate exceeds the aggregate artifact-size limit",
    );
  }
  return entries;
}

function cleanParity(candidate: ZccPullArtifactSet): ZccPullArtifactParity {
  return compareZccPullArtifactDigests({
    candidate,
    materialized: {
      tfvars: {
        sha256: candidate.artifacts.tfvars.sha256,
        size_bytes: candidate.artifacts.tfvars.size_bytes,
      },
      imports: {
        sha256: candidate.artifacts.imports.sha256,
        size_bytes: candidate.artifacts.imports.size_bytes,
      },
      lookup: candidate.artifacts.lookup === null
        ? null
        : {
            sha256: candidate.artifacts.lookup.sha256,
            size_bytes: candidate.artifacts.lookup.size_bytes,
          },
    },
  });
}

function requireReadyAssertion(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullArtifactParity;
}): ZccPullArtifactParity {
  const verification = cleanParity(options.candidate);
  if (
    options.candidate.status !== "ready"
    || verification.status !== "ready"
    || verification.parity.status !== "equal"
  ) {
    return fail(
      "MATERIALIZATION_CANDIDATE_REVIEW_REQUIRED",
      "only a ready bootstrap candidate can be materialized",
    );
  }
  if (!validateZccPullArtifactParity(options.assertion)) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_ASSERTION",
      category: "domain",
      message: "the parity assertion failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactParity.errors),
    });
  }
  if (
    options.assertion.status !== "ready"
    || options.assertion.parity.status !== "equal"
    || safeJsonValue(options.assertion) !== safeJsonValue(verification)
  ) {
    return fail(
      "MATERIALIZATION_ASSERTION_MISMATCH",
      "the ready parity assertion does not exactly match the fresh candidate",
    );
  }
  return verification;
}

function unsupportedPaths(
  outputRoot: string,
  pathBase: string,
  candidate: ZccBootstrapMaterializationCandidate,
): readonly string[] {
  const imports = candidate.artifacts.imports.path;
  const tfvars = candidate.artifacts.tfvars.path;
  const configDirectory = path.posix.dirname(tfvars);
  const moves = imports.endsWith("_imports.tf")
    ? `${imports.slice(0, -"_imports.tf".length)}_moves.tf`
    : `${imports}.moves`;
  const pendingMoves = imports.endsWith("_imports.tf")
    ? `${imports.slice(0, -"_imports.tf".length)}_moves.pending.json`
    : `${imports}.moves.pending.json`;
  const alternateHcl = tfvars.endsWith(".json")
    ? tfvars.slice(0, -".json".length)
    : `${tfvars}.hcl`;
  const generated = path.posix.join(
    configDirectory,
    `${candidate.resource_type}.generated.expressions.json`,
  );
  const staleLookup = candidate.artifacts.lookup === null
    ? path.posix.join(
        configDirectory,
        `${candidate.resource_type}.lookup.json`,
      )
    : null;
  return [
    moves,
    pendingMoves,
    alternateHcl,
    generated,
    ...(staleLookup === null ? [] : [staleLookup]),
  ]
    .map((target) => resolveAuthorizedPath(outputRoot, pathBase, target));
}

async function prepareArtifacts(options: {
  readonly authority: BoundAuthority;
  readonly pathBase: string;
  readonly candidate: ZccBootstrapMaterializationCandidate;
}): Promise<{
  readonly artifacts: readonly PreparedArtifact[];
  readonly unsupported: readonly string[];
}> {
  const artifacts = artifactBytes(options.candidate).map((entry) => {
    const absolutePath = resolveAuthorizedPath(
      options.authority.root.absolutePath,
      options.pathBase,
      entry.descriptor.path,
    );
    return {
      ...entry,
      absolutePath,
      parentPath: path.dirname(absolutePath),
      initial: null,
    };
  });
  const unsupported = unsupportedPaths(
    options.authority.root.absolutePath,
    options.pathBase,
    options.candidate,
  );
  requireExactPublisherAuthority(
    options.authority.root.absolutePath,
    artifacts.map((artifact) => artifact.absolutePath),
  );
  for (const target of [
    ...artifacts.map((artifact) => artifact.absolutePath),
    ...unsupported,
  ]) {
    await bindExistingAncestors(options.authority, target);
  }
  for (const target of unsupported) {
    await absentTarget(target);
  }
  const prepared: PreparedArtifact[] = [];
  for (const artifact of artifacts) {
    prepared.push({
      ...artifact,
      initial: await bindExactFile(artifact.absolutePath, artifact.bytes),
    });
  }
  const firstMissing = prepared.findIndex((artifact) => artifact.initial === null);
  if (
    firstMissing >= 0
    && prepared.slice(firstMissing).some((artifact) => artifact.initial !== null)
  ) {
    return fail(
      "INVALID_MATERIALIZATION_PREFIX",
      "existing exact artifacts do not form a valid publication prefix",
    );
  }
  return { artifacts: prepared, unsupported };
}

async function ensureDirectory(
  authority: BoundAuthority,
  directoryPath: string,
): Promise<void> {
  const relative = path.relative(authority.root.absolutePath, directoryPath);
  let current = authority.root.absolutePath;
  for (const component of relative.split(path.sep).filter(Boolean)) {
    const parent = authority.directories.get(current);
    if (parent === undefined) {
      return fail("INTERNAL_ERROR", "materialization parent binding is missing", "internal");
    }
    await recheckDirectory(parent);
    const child = path.join(current, component);
    const prior = authority.directories.get(child);
    if (prior !== undefined) {
      await recheckDirectory(prior);
      current = child;
      continue;
    }
    try {
      await mkdir(child, { mode: 0o777 });
    } catch (error: unknown) {
      if (errorCode(error) !== "EEXIST") {
        return fail(
          "MATERIALIZATION_DIRECTORY_CREATE_FAILED",
          "an artifact directory could not be created",
          "io",
        );
      }
    }
    await refreshDirectory(parent);
    try {
      const metadata = await lstat(child, { bigint: true });
      if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
        return fail(
          "MATERIALIZATION_DIRECTORY_UNSAFE",
          "an artifact parent is not a regular directory",
          "io",
        );
      }
      authority.directories.set(child, {
        absolutePath: child,
        metadata: metadataOf(metadata),
      });
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "MATERIALIZATION_DIRECTORY_CREATE_FAILED",
        "an artifact directory could not be bound",
        "io",
      );
    }
    current = child;
  }
}

async function writeAll(handle: FileHandle, bytes: Buffer): Promise<void> {
  let offset = 0;
  while (offset < bytes.length) {
    const result = await handle.write(bytes, offset, bytes.length - offset, null);
    if (result.bytesWritten <= 0) {
      return fail(
        "MATERIALIZATION_STAGE_FAILED",
        "an artifact could not be staged",
        "io",
      );
    }
    offset += result.bytesWritten;
  }
}

async function cleanupUnboundStage(
  parent: DirectoryBinding,
  handle: FileHandle,
  tempPath: string,
): Promise<boolean> {
  try {
    const opened = await handle.stat({ bigint: true });
    const current = await lstat(tempPath, { bigint: true });
    if (
      !opened.isFile()
      || !current.isFile()
      || current.isSymbolicLink()
      || !sameObject(metadataOf(opened), metadataOf(current))
    ) {
      return false;
    }
    await unlink(tempPath);
    await refreshDirectory(parent);
    return true;
  } catch (error: unknown) {
    if (errorCode(error) !== "ENOENT") {
      return false;
    }
    try {
      const opened = await handle.stat({ bigint: true });
      if (opened.nlink !== 0n) {
        return false;
      }
      await refreshDirectory(parent);
      return true;
    } catch {
      return false;
    }
  }
}

async function stageArtifact(
  authority: BoundAuthority,
  artifact: PreparedArtifact,
  staged: StagedArtifact[],
  afterTempOpen: ZccPullMaterializationHooks["afterTempOpen"],
  index: number,
): Promise<StagedArtifact> {
  const parent = authority.directories.get(artifact.parentPath);
  if (parent === undefined) {
    return fail("INTERNAL_ERROR", "materialization parent binding is missing", "internal");
  }
  await recheckDirectory(parent);
  let handle: FileHandle | null = null;
  let tempPath = "";
  for (let attempt = 0; attempt < TEMP_ATTEMPTS; attempt += 1) {
    tempPath = path.join(
      artifact.parentPath,
      `.infrawright-${randomBytes(16).toString("hex")}.tmp`,
    );
    try {
      handle = await open(
        tempPath,
        constants.O_RDWR
          | constants.O_CREAT
          | constants.O_EXCL
          | constants.O_NOFOLLOW,
        0o666,
      );
      break;
    } catch (error: unknown) {
      if (errorCode(error) !== "EEXIST") {
        return fail(
          "MATERIALIZATION_STAGE_FAILED",
          "an artifact could not be staged",
          "io",
        );
      }
    }
  }
  if (handle === null) {
    return fail(
      "MATERIALIZATION_STAGE_FAILED",
      "an artifact could not be staged",
      "io",
    );
  }
  let registered = false;
  try {
    await invokeArtifactHook(afterTempOpen, artifact.name, index);
    const opened = await handle.stat({ bigint: true });
    if (!opened.isFile()) {
      return fail(
        "MATERIALIZATION_STAGE_FAILED",
        "an artifact could not be staged safely",
        "io",
      );
    }
    const stage: StagedArtifact = {
      artifact,
      tempPath,
      metadata: metadataOf(opened),
      aliasPresent: true,
    };
    staged.push(stage);
    registered = true;
    await refreshDirectory(parent);
    await writeAll(handle, artifact.bytes);
    await handle.sync();
    const after = await handle.stat({ bigint: true });
    const afterMetadata = metadataOf(after);
    const bytes = await readHandleBytes(handle, artifact.bytes.length);
    const pathStat = await lstat(tempPath, { bigint: true });
    if (
      !after.isFile()
      || !pathStat.isFile()
      || pathStat.isSymbolicLink()
      || after.size !== BigInt(artifact.bytes.length)
      || !sameMetadata(afterMetadata, metadataOf(pathStat))
      || !bytes.equals(artifact.bytes)
    ) {
      return fail(
        "MATERIALIZATION_STAGE_FAILED",
        "an artifact staging file changed during verification",
        "io",
      );
    }
    stage.metadata = afterMetadata;
    return stage;
  } catch (error: unknown) {
    if (
      !registered
      && !(await cleanupUnboundStage(parent, handle, tempPath))
    ) {
      return fail(
        "MATERIALIZATION_CLEANUP_FAILED",
        "an unbound staging alias could not be removed safely",
        "io",
      );
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "MATERIALIZATION_STAGE_FAILED",
      "an artifact could not be staged",
      "io",
    );
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function verifyStaged(stage: StagedArtifact): Promise<void> {
  if (!stage.aliasPresent) {
    return fail("INTERNAL_ERROR", "staged artifact alias is missing", "internal");
  }
  const current = await bindExactFile(
    stage.tempPath,
    stage.artifact.bytes,
    stage.metadata,
  );
  if (current === null) {
    return fail(
      "MATERIALIZATION_STAGE_CHANGED",
      "an artifact staging file changed before publication",
      "io",
    );
  }
}

async function cleanupStages(
  authority: BoundAuthority,
  stages: readonly StagedArtifact[],
): Promise<boolean> {
  let clean = true;
  for (const stage of stages) {
    if (!stage.aliasPresent) {
      continue;
    }
    try {
      const current = await lstat(stage.tempPath, { bigint: true });
      if (
        !current.isFile()
        || current.isSymbolicLink()
        || !sameObject(stage.metadata, metadataOf(current))
      ) {
        clean = false;
        continue;
      }
      await unlink(stage.tempPath);
      stage.aliasPresent = false;
      const parent = authority.directories.get(stage.artifact.parentPath);
      if (parent === undefined) {
        clean = false;
      } else {
        await refreshDirectory(parent);
      }
    } catch (error: unknown) {
      if (errorCode(error) === "ENOENT") {
        // The bound inode may have been renamed to an unknown alias. Without
        // a live descriptor there is no safe way to distinguish that from an
        // unlink, so fail cleanup closed instead of claiming it was removed.
        clean = false;
      } else {
        clean = false;
      }
    }
  }
  return clean;
}

async function syncParent(directoryPath: string): Promise<void> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(directoryPath, constants.O_RDONLY);
    await handle.sync();
  } catch {
    return fail(
      "MATERIALIZATION_SYNC_FAILED",
      "an artifact parent could not be synchronized",
      "io",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function recheckPrepared(options: {
  readonly authority: BoundAuthority;
  readonly artifacts: readonly PreparedArtifact[];
  readonly unsupported: readonly string[];
  readonly stages: readonly StagedArtifact[];
  readonly requireComplete: boolean;
}): Promise<void> {
  await recheckDirectories(options.authority);
  for (const target of [
    ...options.artifacts.map((artifact) => artifact.absolutePath),
    ...options.unsupported,
  ]) {
    const physical = pythonPosixRealpath(target);
    if (!containedPath(physical, options.authority.root.absolutePath)) {
      return fail(
        "MATERIALIZATION_TARGET_CHANGED",
        "an artifact target changed during materialization",
        "io",
      );
    }
  }
  for (const target of options.unsupported) {
    await absentTarget(target);
  }
  for (const artifact of options.artifacts) {
    if (artifact.initial !== null) {
      const current = await bindExactFile(
        artifact.absolutePath,
        artifact.bytes,
        artifact.initial.metadata,
      );
      if (current === null) {
        return fail(
          "MATERIALIZATION_TARGET_CHANGED",
          "an artifact target changed during materialization",
          "io",
        );
      }
      continue;
    }
    if (options.requireComplete) {
      const stage = options.stages.find(
        (candidate) => candidate.artifact.name === artifact.name,
      );
      if (stage === undefined) {
        return fail("INTERNAL_ERROR", "published artifact binding is missing", "internal");
      }
      const current = await bindExactFile(
        artifact.absolutePath,
        artifact.bytes,
        stage.metadata,
      );
      if (current === null) {
        return fail(
          "MATERIALIZATION_TARGET_CHANGED",
          "a published artifact target changed",
          "io",
        );
      }
    } else {
      await assertStillAbsent(artifact.absolutePath);
    }
  }
  if (!options.requireComplete) {
    for (const stage of options.stages) {
      await verifyStaged(stage);
    }
  }
}

async function publishStage(
  authority: BoundAuthority,
  stage: StagedArtifact,
  onLinked: () => void,
  afterLink: ZccPullMaterializationHooks["afterLink"],
  index: number,
): Promise<void> {
  const parent = authority.directories.get(stage.artifact.parentPath);
  if (parent === undefined) {
    return fail("INTERNAL_ERROR", "materialization parent binding is missing", "internal");
  }
  await assertStillAbsent(stage.artifact.absolutePath);
  await recheckDirectory(parent);
  try {
    await link(stage.tempPath, stage.artifact.absolutePath);
  } catch {
    return fail(
      "MATERIALIZATION_PUBLISH_FAILED",
      "an artifact could not be published without replacement",
      "io",
    );
  }
  onLinked();
  await invokeArtifactHook(afterLink, stage.artifact.name, index);
  await refreshDirectory(parent);
  const published = await refreshExactFile(
    stage.artifact.absolutePath,
    stage.artifact.bytes,
    stage.metadata,
  );
  stage.metadata = published.metadata;
  try {
    await unlink(stage.tempPath);
    stage.aliasPresent = false;
  } catch {
    return fail(
      "MATERIALIZATION_PUBLISH_FAILED",
      "a staging alias could not be removed after publication",
      "io",
    );
  }
  await refreshDirectory(parent);
  const unlinked = await refreshExactFile(
    stage.artifact.absolutePath,
    stage.artifact.bytes,
    stage.metadata,
  );
  stage.metadata = unlinked.metadata;
  await syncParent(stage.artifact.parentPath);
}

/**
 * @internal Shared no-replacement publication kernel for immutable ZCC
 * bootstrap bytes.
 *
 * Callers must snapshot and validate their candidate/assertion synchronously
 * before entering this function. The kernel owns only filesystem authority,
 * fixed ordering, retry-forward publication, and final assertion revalidation.
 */
export async function materializeReadyZccBootstrapArtifacts<
  Candidate extends ZccBootstrapMaterializationCandidate,
  Verification,
  Result,
>(options: {
  readonly outputRoot: string;
  readonly pathBase: string;
  readonly candidate: Candidate;
  readonly asserted: Verification;
  readonly recheckInputs: () => Promise<void>;
  readonly cleanVerification: (candidate: Candidate) => Verification;
  readonly verificationReady: (verification: Verification) => boolean;
  readonly buildResult: (
    options: ZccBootstrapMaterializationResultOptions<Candidate, Verification>,
  ) => Result;
  readonly hooks?: ZccPullMaterializationHooks;
}): Promise<Result> {
  const candidate = options.candidate;
  const asserted = options.asserted;
  const outputRoot = options.outputRoot;
  const pathBase = options.pathBase;
  const recheckInputs = options.recheckInputs;
  const cleanVerification = options.cleanVerification;
  const verificationReady = options.verificationReady;
  const buildResult = options.buildResult;
  const hooks = options.hooks === undefined
    ? undefined
    : Object.freeze({ ...options.hooks });
  const authority = await bindAuthority(outputRoot);
  const prepared = await prepareArtifacts({
    authority,
    pathBase,
    candidate,
  });
  await invokeHook(hooks?.afterPreflight);

  const missing = prepared.artifacts.filter((artifact) => artifact.initial === null);
  const staged: StagedArtifact[] = [];
  let published = 0;
  try {
    for (const directoryPath of new Set(missing.map((artifact) => artifact.parentPath))) {
      await ensureDirectory(authority, directoryPath);
    }
    await invokeHook(hooks?.afterDirectoriesReady);
    for (const artifact of missing) {
      await stageArtifact(
        authority,
        artifact,
        staged,
        hooks?.afterTempOpen,
        staged.length,
      );
    }
    await invokeHook(hooks?.afterStaged);
    await invokeHook(hooks?.beforePrepublishRecheck);
    await recheckInputs();
    await recheckPrepared({
      authority,
      artifacts: prepared.artifacts,
      unsupported: prepared.unsupported,
      stages: staged,
      requireComplete: false,
    });
    for (let index = 0; index < staged.length; index += 1) {
      const stage = staged[index];
      if (stage === undefined) {
        return fail("INTERNAL_ERROR", "staged artifact order is incomplete", "internal");
      }
      await invokeArtifactHook(
        hooks?.beforePublish,
        stage.artifact.name,
        index,
      );
      await publishStage(
        authority,
        stage,
        () => {
          published += 1;
        },
        hooks?.afterLink,
        index,
      );
      await invokeArtifactHook(
        hooks?.afterPublish,
        stage.artifact.name,
        index,
      );
    }
    await invokeHook(hooks?.beforePostpublishRecheck);
    await recheckInputs();
    await recheckPrepared({
      authority,
      artifacts: prepared.artifacts,
      unsupported: prepared.unsupported,
      stages: staged,
      requireComplete: true,
    });
    const verification = cleanVerification(candidate);
    if (
      !verificationReady(verification)
      || safeJsonValue(verification) !== safeJsonValue(asserted)
    ) {
      return fail(
        "MATERIALIZATION_FINAL_VERIFICATION_FAILED",
        "materialized artifacts failed their fresh parity verification",
        "io",
      );
    }
    const created = missing.map((artifact) => artifact.name).sort();
    const reused = prepared.artifacts
      .filter((artifact) => artifact.initial !== null)
      .map((artifact) => artifact.name)
      .sort();
    return immutableCopy(buildResult({
      candidate,
      created,
      reused,
      verification,
    })) as Result;
  } catch (error: unknown) {
    const clean = await cleanupStages(authority, staged);
    if (published > 0) {
      return fail(
        "MATERIALIZATION_INDETERMINATE",
        "materialization stopped after publishing an exact artifact prefix; retry to finish forward",
        "io",
        true,
      );
    }
    if (!clean) {
      return fail(
        "MATERIALIZATION_CLEANUP_FAILED",
        "materialization failed before publication and staging cleanup was incomplete",
        "io",
      );
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("MATERIALIZATION_FAILED", "materialization failed safely", "io");
  }
}

/** Publish one freshly compiled, independently asserted ZCC bootstrap set. */
export async function materializeReadyZccPullArtifacts(options: {
  readonly outputRoot: string;
  readonly pathBase: string;
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullArtifactParity;
  readonly recheckInputs: () => Promise<void>;
  readonly hooks?: ZccPullMaterializationHooks;
}): Promise<ZccPullArtifactMaterialization> {
  // Library callers can retain and mutate their objects while this operation
  // awaits filesystem work. Snapshot every data/function reference before the
  // first await so publication remains bound to the assertion checked here.
  const snapshot = snapshotMaterializationValues({
    candidate: options.candidate,
    assertion: options.assertion,
  });
  const candidate = snapshot.candidate;
  const asserted = requireReadyAssertion({
    candidate,
    assertion: snapshot.assertion,
  });
  const outputRoot = options.outputRoot;
  const pathBase = options.pathBase;
  const recheckInputs = options.recheckInputs;
  const hooks = options.hooks === undefined
    ? undefined
    : Object.freeze({ ...options.hooks });
  return materializeReadyZccBootstrapArtifacts({
    outputRoot,
    pathBase,
    candidate,
    asserted,
    recheckInputs,
    cleanVerification: cleanParity,
    verificationReady: (verification) => verification.status === "ready",
    buildResult: ({ candidate: current, created, reused, verification }) => ({
      kind: "infrawright.zcc_pull_artifact_materialization",
      schema_version: 1,
      mode: "bootstrap",
      product: "zcc",
      resource_type: current.resource_type,
      tenant: current.tenant,
      status: "complete",
      publication: {
        policy: "create_or_verify_exact",
        created,
        reused,
      },
      verification,
    }),
    ...(hooks === undefined ? {} : { hooks }),
  });
}
