import { createHash, randomBytes } from "node:crypto";
import { constants, type BigIntStats } from "node:fs";
import {
  link,
  lstat,
  open,
  realpath,
  rename,
  unlink,
  type FileHandle,
} from "node:fs/promises";
import path from "node:path";
import { TextDecoder } from "node:util";

import {
  schemaErrorDetails,
  validateZccPullArtifactSet,
  validateZccPullRefreshArtifactSet,
  validateZccPullRefreshMaterialization,
  validateZccPullRefreshParity,
  validateZccPullRefreshPendingTransition,
} from "../contracts/validators.js";
import { ProcessFailure } from "./errors.js";
import {
  deriveImportMoves,
  renderMovedBlocks,
} from "./import-moves.js";
import { pythonPosixRealpath } from "./paths.js";
import type {
  ZccPullArtifactSet,
  ZccTextArtifact,
} from "./zcc-pull-artifacts.js";
import {
  zccRefreshBaselineFingerprint,
  zccRefreshEvidenceDigest,
  zccRefreshTransitionFingerprint,
} from "./zcc-pull-refresh-fingerprints.js";
import {
  zccPullRefreshNeutralEvidence,
  type ZccPullRefreshParity,
  type ZccPullRefreshParityBindingEvidence,
} from "./zcc-pull-refresh-parity.js";
import type { ZccPullRefreshArtifactSet } from "./zcc-pull-refresh.js";
import {
  classifyZccRefreshTransition,
  type ZccPullRefreshPendingTransition,
  type ZccRefreshContentState,
  type ZccRefreshPayloadRole,
  type ZccRefreshTransitionState,
} from "./zcc-pull-refresh-transition.js";

const MAX_FILE_BYTES = 32 * 1024 * 1024;
const MAX_READ_BYTES = 128 * 1024 * 1024 + 64 * 1024;
const MAX_STAGE_PAYLOAD_BYTES = 128 * 1024 * 1024;
const MAX_MARKER_BYTES = 64 * 1024;
const MAX_ASSERTION_BYTES = 1024 * 1024;
const MAX_CANDIDATE_BYTES = 128 * 1024 * 1024;
const MAX_JSON_DEPTH = 128;
const MAX_JSON_NODES = 100_000;
const MAX_TARGET_BYTES = 4096;
const MAX_TARGET_COMPONENTS = 64;
const MAX_TARGET_COMPONENT_BYTES = 255;
const TEMP_ATTEMPTS = 16;

type ArtifactRole =
  | ZccRefreshPayloadRole
  | "pending_moves"
  | "alternate_hcl"
  | "generated_bindings";

const FINAL_READ_ORDER = [
  "lookup",
  "moves",
  "tfvars",
  "pending_moves",
  "alternate_hcl",
  "generated_bindings",
  "imports",
] as const satisfies readonly ArtifactRole[];

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

interface BoundContent {
  readonly state: ZccRefreshContentState;
  readonly bytes: Buffer | null;
  readonly metadata: MetadataBinding | null;
}

interface ArtifactSpec {
  readonly role: ArtifactRole;
  readonly logicalPath: string;
  readonly absolutePath: string;
  readonly parentPath: string;
  readonly baseline: ZccRefreshContentState;
  readonly desired: ZccRefreshContentState;
  desiredBytes: Buffer | null;
}

interface StagedPayload {
  readonly role: ZccRefreshPayloadRole | "pending_moves";
  readonly spec: ArtifactSpec;
  readonly tempPath: string;
  readonly bytes: Buffer;
  metadata: MetadataBinding;
  aliasPresent: boolean;
}

interface JsonBudget {
  nodes: number;
  bytes: number;
  readonly maxBytes: number;
}

export interface ZccPullRefreshMaterializationHooks {
  readonly afterBound?: () => void | Promise<void>;
  readonly afterStage?: (
    role: ZccRefreshPayloadRole | "pending_moves",
    index: number,
  ) => void | Promise<void>;
  readonly beforeMarkerLink?: () => void | Promise<void>;
  readonly afterMarkerLink?: () => void | Promise<void>;
  readonly afterMarkerSync?: () => void | Promise<void>;
  readonly beforePublish?: (
    role: ZccRefreshPayloadRole,
    index: number,
  ) => void | Promise<void>;
  readonly afterPublish?: (
    role: ZccRefreshPayloadRole,
    index: number,
  ) => void | Promise<void>;
  readonly afterPublishParentSync?: (
    role: ZccRefreshPayloadRole,
    index: number,
  ) => void | Promise<void>;
  readonly beforeMarkerRemove?: () => void | Promise<void>;
  readonly afterMarkerRemove?: () => void | Promise<void>;
  readonly beforeFinalCas?: () => void | Promise<void>;
}

export interface ZccPullRefreshMaterialization {
  readonly kind: "infrawright.zcc_pull_refresh_materialization";
  readonly schema_version: 1;
  readonly mode: "refresh";
  readonly product: "zcc";
  readonly resource_type: ZccPullArtifactSet["resource_type"];
  readonly tenant: string;
  readonly status: "complete" | "awaiting_apply";
  readonly publication: {
    readonly policy: "replace_or_verify_exact_imports_last";
    readonly advanced: readonly ZccRefreshPayloadRole[];
  };
  readonly transition: {
    readonly initial: ZccRefreshTransitionState;
    readonly final: "already_complete" | "committed";
    readonly next_action: "none" | "apply_moves_then_ack";
  };
  readonly verification: {
    readonly candidate_request_sha256: string;
    readonly assertion_sha256: string;
    readonly baseline_fingerprint_sha256: string;
    readonly transition_sha256: string;
    readonly artifacts: Readonly<Record<ArtifactRole, ZccRefreshContentState>>;
  };
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

function metadataOf(value: BigIntStats): MetadataBinding {
  return {
    dev: value.dev,
    ino: value.ino,
    size: value.size,
    mtimeNs: value.mtimeNs,
    ctimeNs: value.ctimeNs,
  };
}

function sameIdentity(left: MetadataBinding, right: MetadataBinding): boolean {
  return left.dev === right.dev && left.ino === right.ino;
}

function sameMetadata(left: MetadataBinding, right: MetadataBinding): boolean {
  return sameIdentity(left, right)
    && left.size === right.size
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

function sameState(left: ZccRefreshContentState, right: ZccRefreshContentState): boolean {
  return left.state === "absent"
    ? right.state === "absent"
    : right.state === "present"
      && left.sha256 === right.sha256
      && left.size_bytes === right.size_bytes;
}

function stateFor(bytes: Buffer): ZccRefreshContentState {
  return {
    state: "present",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
  };
}

function sameJson(left: unknown, right: unknown): boolean {
  try {
    return zccRefreshEvidenceDigest(left) === zccRefreshEvidenceDigest(right);
  } catch {
    return false;
  }
}

function snapshotJson(
  value: unknown,
  budget: JsonBudget,
  depth = 1,
  ancestors: Set<object> = new Set<object>(),
): unknown {
  budget.nodes += 1;
  if (budget.nodes > MAX_JSON_NODES || depth > MAX_JSON_DEPTH) {
    return fail("MATERIALIZATION_INPUT_LIMIT_EXCEEDED", "refresh materialization input is too large");
  }
  if (value === null || typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || Object.is(value, -0)) {
      return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input is not JSON");
    }
    return value;
  }
  if (typeof value === "string") {
    if (!value.isWellFormed()) {
      return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input contains invalid Unicode");
    }
    budget.bytes += Buffer.byteLength(value, "utf8");
    if (budget.bytes > budget.maxBytes) {
      return fail("MATERIALIZATION_INPUT_LIMIT_EXCEEDED", "refresh materialization input is too large");
    }
    return value;
  }
  if (typeof value !== "object" || value === null || ancestors.has(value)) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be acyclic inert JSON");
  }
  ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      const output: unknown[] = [];
      for (let index = 0; index < value.length; index += 1) {
        const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
        if (descriptor === undefined || !("value" in descriptor)) {
          return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be inert JSON");
        }
        output.push(snapshotJson(descriptor.value, budget, depth + 1, ancestors));
      }
      return Object.freeze(output);
    }
    const prototype = Object.getPrototypeOf(value) as unknown;
    if (prototype !== Object.prototype && prototype !== null) {
      return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be plain JSON");
    }
    const output: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    for (const key of Object.keys(value)) {
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (descriptor === undefined || !("value" in descriptor) || !key.isWellFormed()) {
        return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be inert JSON");
      }
      budget.bytes += Buffer.byteLength(key, "utf8");
      output[key] = snapshotJson(descriptor.value, budget, depth + 1, ancestors);
    }
    return Object.freeze(output);
  } finally {
    ancestors.delete(value);
  }
}

/** Snapshot and validate a caller-owned assertion before the first await. */
export function snapshotZccPullRefreshMaterializationAssertion(
  assertion: unknown,
): ZccPullRefreshParity {
  const snapshot = snapshotJson(assertion, {
    nodes: 0,
    bytes: 0,
    maxBytes: MAX_ASSERTION_BYTES,
  }) as ZccPullRefreshParity;
  if (!validateZccPullRefreshParity(snapshot)) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_ASSERTION",
      category: "domain",
      message: "refresh parity assertion failed its versioned contract",
      details: schemaErrorDetails(validateZccPullRefreshParity.errors),
    });
  }
  return snapshot;
}

function snapshotCandidate(candidate: unknown): ZccPullArtifactSet {
  const snapshot = snapshotJson(candidate, {
    nodes: 0,
    bytes: 0,
    maxBytes: MAX_CANDIDATE_BYTES,
  }) as ZccPullArtifactSet;
  if (!validateZccPullArtifactSet(snapshot)) {
    throw new ProcessFailure({
      code: "INVALID_REFRESH_MATERIALIZATION_CANDIDATE",
      category: "domain",
      message: "raw refresh candidate failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactSet.errors),
    });
  }
  return snapshot;
}

function snapshotBinding(value: unknown): ZccPullRefreshParityBindingEvidence {
  return snapshotJson(value, {
    nodes: 0,
    bytes: 0,
    maxBytes: MAX_ASSERTION_BYTES,
  }) as ZccPullRefreshParityBindingEvidence;
}

async function invokeHook(hook: (() => void | Promise<void>) | undefined): Promise<void> {
  if (hook === undefined) {
    return;
  }
  try {
    await hook();
  } catch {
    return fail("REFRESH_MATERIALIZATION_HOOK_FAILED", "refresh materialization test hook failed", "internal");
  }
}

async function invokeRoleHook(
  hook: ((role: ZccRefreshPayloadRole, index: number) => void | Promise<void>) | undefined,
  role: ZccRefreshPayloadRole,
  index: number,
): Promise<void> {
  if (hook === undefined) {
    return;
  }
  try {
    await hook(role, index);
  } catch {
    return fail("REFRESH_MATERIALIZATION_HOOK_FAILED", "refresh materialization test hook failed", "internal");
  }
}

async function invokeStageHook(
  hook: ((
    role: ZccRefreshPayloadRole | "pending_moves",
    index: number,
  ) => void | Promise<void>) | undefined,
  role: ZccRefreshPayloadRole | "pending_moves",
  index: number,
): Promise<void> {
  if (hook === undefined) {
    return;
  }
  try {
    await hook(role, index);
  } catch {
    return fail("REFRESH_MATERIALIZATION_HOOK_FAILED", "refresh materialization test hook failed", "internal");
  }
}

function containedPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative !== ""
    && relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative);
}

async function bindOutputRoot(outputRoot: string): Promise<DirectoryBinding> {
  if (
    typeof outputRoot !== "string"
    || !path.isAbsolute(outputRoot)
    || outputRoot.includes("\0")
    || path.resolve(outputRoot) !== outputRoot
    || path.parse(outputRoot).root === outputRoot
  ) {
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_AUTHORITY",
      "output_root must be a canonical absolute non-root directory",
      "io",
    );
  }
  try {
    const [canonical, metadata] = await Promise.all([
      realpath(outputRoot),
      lstat(outputRoot, { bigint: true }),
    ]);
    if (
      canonical !== outputRoot
      || !metadata.isDirectory()
      || metadata.isSymbolicLink()
    ) {
      return fail(
        "INVALID_REFRESH_MATERIALIZATION_AUTHORITY",
        "output_root must be an existing canonical non-symlink directory",
        "io",
      );
    }
    return { absolutePath: outputRoot, metadata: metadataOf(metadata) };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "INVALID_REFRESH_MATERIALIZATION_AUTHORITY",
      "output_root could not be bound as a write authority",
      "io",
    );
  }
}

function resolveAuthorizedPath(
  outputRoot: string,
  pathBase: string,
  nominatedPath: string,
): string {
  if (
    typeof nominatedPath !== "string"
    || nominatedPath.includes("\0")
    || !nominatedPath.isWellFormed()
  ) {
    return fail(
      "REFRESH_MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target is outside output_root",
    );
  }
  const absolute = path.isAbsolute(nominatedPath)
    ? path.resolve(nominatedPath)
    : path.resolve(pathBase, nominatedPath);
  if (!containedPath(absolute, outputRoot)) {
    return fail(
      "REFRESH_MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target is outside output_root",
    );
  }
  const relative = path.relative(outputRoot, absolute);
  const components = relative.split(path.sep).filter(Boolean);
  if (
    Buffer.byteLength(relative, "utf8") > MAX_TARGET_BYTES
    || components.length > MAX_TARGET_COMPONENTS
    || components.some((component) => {
      return Buffer.byteLength(component, "utf8") > MAX_TARGET_COMPONENT_BYTES;
    })
  ) {
    return fail(
      "REFRESH_MATERIALIZATION_PATH_LIMIT_EXCEEDED",
      "an artifact target exceeds the bounded path contract",
    );
  }
  if (!containedPath(pythonPosixRealpath(absolute), outputRoot)) {
    return fail(
      "REFRESH_MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY",
      "an artifact target does not resolve beneath output_root",
    );
  }
  return absolute;
}

async function bindParents(
  root: DirectoryBinding,
  targets: readonly string[],
): Promise<Map<string, DirectoryBinding>> {
  const bindings = new Map<string, DirectoryBinding>([[root.absolutePath, root]]);
  const parents = [...new Set(targets.map((target) => path.dirname(target)))].sort();
  for (const parentPath of parents) {
    if (!containedPath(parentPath, root.absolutePath) && parentPath !== root.absolutePath) {
      return fail("REFRESH_MATERIALIZATION_TARGET_OUTSIDE_AUTHORITY", "an artifact parent is outside output_root");
    }
    try {
      const [canonical, metadata] = await Promise.all([
        realpath(parentPath),
        lstat(parentPath, { bigint: true }),
      ]);
      if (
        canonical !== parentPath
        || !metadata.isDirectory()
        || metadata.isSymbolicLink()
      ) {
        return fail(
          "REFRESH_MATERIALIZATION_DIRECTORY_UNSAFE",
          "an artifact parent is not a canonical regular directory",
          "io",
        );
      }
      bindings.set(parentPath, {
        absolutePath: parentPath,
        metadata: metadataOf(metadata),
      });
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "REFRESH_MATERIALIZATION_DIRECTORY_UNSAFE",
        "an artifact parent could not be bound",
        "io",
      );
    }
  }
  return bindings;
}

async function recheckParent(
  binding: DirectoryBinding,
  identityOnly: boolean,
): Promise<MetadataBinding> {
  try {
    const current = await lstat(binding.absolutePath, { bigint: true });
    const metadata = metadataOf(current);
    if (
      !current.isDirectory()
      || current.isSymbolicLink()
      || (identityOnly
        ? !sameIdentity(binding.metadata, metadata)
        : !sameMetadata(binding.metadata, metadata))
    ) {
      return fail(
        "REFRESH_MATERIALIZATION_DIRECTORY_CHANGED",
        "a bound artifact directory changed",
        "io",
      );
    }
    return metadata;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "REFRESH_MATERIALIZATION_DIRECTORY_CHANGED",
      "a bound artifact directory changed",
      "io",
    );
  }
}

async function refreshParent(binding: DirectoryBinding): Promise<void> {
  binding.metadata = await recheckParent(binding, true);
}

async function syncParent(parentPath: string): Promise<void> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(parentPath, constants.O_RDONLY | constants.O_NOFOLLOW);
    await handle.sync();
  } catch {
    return fail(
      "REFRESH_MATERIALIZATION_SYNC_FAILED",
      "an artifact parent could not be synchronized",
      "io",
    );
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function readHandleBytes(handle: FileHandle, size: number): Promise<Buffer> {
  const bytes = Buffer.allocUnsafe(size);
  let offset = 0;
  while (offset < size) {
    const result = await handle.read(bytes, offset, size - offset, offset);
    if (result.bytesRead <= 0) {
      return fail("REFRESH_MATERIALIZATION_FILE_CHANGED", "an artifact changed while read", "io");
    }
    offset += result.bytesRead;
  }
  const tail = Buffer.allocUnsafe(1);
  if ((await handle.read(tail, 0, 1, size)).bytesRead !== 0) {
    return fail("REFRESH_MATERIALIZATION_FILE_CHANGED", "an artifact changed while read", "io");
  }
  return bytes;
}

async function readBoundContent(
  absolutePath: string,
  budget: { bytes: number },
  prior?: MetadataBinding | null,
  maxBytes = MAX_FILE_BYTES,
): Promise<BoundContent> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      absolutePath,
      constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW,
    );
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      if (prior !== undefined && prior !== null) {
        return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "an artifact target changed", "io");
      }
      return { state: { state: "absent" }, bytes: null, metadata: null };
    }
    return fail(
      "REFRESH_MATERIALIZATION_TARGET_UNSAFE",
      "an artifact target is not a stable regular file",
      "io",
    );
  }
  try {
    const beforeStat = await handle.stat({ bigint: true });
    const before = metadataOf(beforeStat);
    if (
      !beforeStat.isFile()
      || before.size > BigInt(maxBytes)
      || (prior !== undefined && prior !== null && !sameMetadata(prior, before))
    ) {
      return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "an artifact target changed", "io");
    }
    budget.bytes += Number(before.size);
    if (budget.bytes > MAX_READ_BYTES) {
      return fail("REFRESH_MATERIALIZATION_READ_LIMIT_EXCEEDED", "refresh artifact reads exceed the aggregate limit");
    }
    const bytes = await readHandleBytes(handle, Number(before.size));
    const [afterStat, pathStat] = await Promise.all([
      handle.stat({ bigint: true }),
      lstat(absolutePath, { bigint: true }),
    ]);
    const after = metadataOf(afterStat);
    const atPath = metadataOf(pathStat);
    if (
      !pathStat.isFile()
      || pathStat.isSymbolicLink()
      || !sameMetadata(before, after)
      || !sameMetadata(before, atPath)
    ) {
      return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "an artifact target changed", "io");
    }
    return { state: stateFor(bytes), bytes, metadata: before };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("REFRESH_MATERIALIZATION_TARGET_CHECK_FAILED", "an artifact target could not be verified", "io");
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function readAllStates(
  specs: Readonly<Record<ArtifactRole, ArtifactSpec>>,
  prior?: Readonly<Record<ArtifactRole, BoundContent>>,
): Promise<Readonly<Record<ArtifactRole, BoundContent>>> {
  const output = {} as Record<ArtifactRole, BoundContent>;
  const budget = { bytes: 0 };
  for (const role of FINAL_READ_ORDER) {
    output[role] = await readBoundContent(
      specs[role].absolutePath,
      budget,
      prior?.[role].metadata,
      role === "pending_moves" ? MAX_MARKER_BYTES : MAX_FILE_BYTES,
    );
  }
  return Object.freeze(output);
}

function expectedMovesPath(importsPath: string): string {
  if (!importsPath.endsWith("_imports.tf")) {
    return fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "imports path is not canonical");
  }
  return importsPath.slice(0, -"_imports.tf".length) + "_moves.tf";
}

function expectedPendingPath(importsPath: string): string {
  if (!importsPath.endsWith("_imports.tf")) {
    return fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "imports path is not canonical");
  }
  return importsPath.slice(0, -"_imports.tf".length) + "_moves.pending.json";
}

function contentState(value: {
  readonly state: "absent";
} | {
  readonly state: "present";
  readonly sha256: string;
  readonly size_bytes: number;
}): ZccRefreshContentState {
  return value.state === "absent"
    ? { state: "absent" }
    : {
        state: "present",
        sha256: value.sha256,
        size_bytes: value.size_bytes,
      };
}

function artifactBytes(artifact: ZccTextArtifact): Buffer {
  if (
    typeof artifact.content !== "string"
    || !artifact.content.isWellFormed()
  ) {
    return fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "candidate artifact is not supported UTF-8 text", "internal");
  }
  const bytes = Buffer.from(artifact.content, "utf8");
  if (
    bytes.length > MAX_FILE_BYTES
    || bytes.length !== artifact.size_bytes
    || createHash("sha256").update(bytes).digest("hex") !== artifact.sha256
  ) {
    return fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "candidate artifact content does not match its descriptor", "internal");
  }
  return bytes;
}

function candidateDesiredState(
  artifact: ZccTextArtifact | null,
): ZccRefreshContentState {
  return artifact === null
    ? { state: "absent" }
    : {
        state: "present",
        sha256: artifact.sha256,
        size_bytes: artifact.size_bytes,
      };
}

function requireReadyInputs(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullRefreshParity;
  readonly expectedBinding: ZccPullRefreshParityBindingEvidence;
}): void {
  const { candidate, assertion, expectedBinding } = options;
  if (
    assertion.status !== "ready"
    || assertion.parity.status !== "equal"
    || assertion.seed.status !== "ready"
    || assertion.seed.differences.length !== 0
    || assertion.candidate.status !== "ready"
    || assertion.candidate.unexpected_drops.length !== 0
    || assertion.candidate.moves.suppressed_count !== 0
    || candidate.status !== "ready"
    || candidate.unexpected_drops.length !== 0
  ) {
    return fail(
      "REFRESH_MATERIALIZATION_REVIEW_REQUIRED",
      "only a complete ready refresh assertion can authorize publication",
    );
  }
  if (
    assertion.product !== "zcc"
    || candidate.product !== "zcc"
    || assertion.resource_type !== candidate.resource_type
    || assertion.tenant !== candidate.tenant
    || !sameJson(assertion.seed.candidate, assertion.candidate)
    || !sameJson(assertion.seed.bindings.candidate, expectedBinding)
    || !sameJson({
      sha256: candidate.source.sha256,
      size_bytes: candidate.source.size_bytes,
    }, assertion.candidate.source)
    || !sameJson(candidate.catalog, assertion.candidate.catalog)
    || !sameJson(candidate.root, assertion.candidate.root)
  ) {
    return fail(
      "REFRESH_MATERIALIZATION_ASSERTION_MISMATCH",
      "refresh candidate does not exactly join the ready assertion",
    );
  }
}

function buildSpecs(options: {
  readonly outputRoot: string;
  readonly pathBase: string;
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullRefreshParity;
}): Readonly<Record<ArtifactRole, ArtifactSpec>> {
  const { candidate, assertion } = options;
  const tfvars = candidate.artifacts.tfvars;
  const imports = candidate.artifacts.imports;
  const lookup = candidate.artifacts.lookup;
  const lookupPath = lookup?.path
    ?? `${path.posix.dirname(tfvars.path)}/${candidate.resource_type}.lookup.json`;
  const movesPath = expectedMovesPath(imports.path);
  const pendingPath = expectedPendingPath(imports.path);
  const alternatePath = tfvars.path.endsWith(".json")
    ? tfvars.path.slice(0, -".json".length)
    : fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "tfvars path is not canonical");
  const generatedPath = `${path.posix.dirname(tfvars.path)}/${candidate.resource_type}.generated.expressions.json`;

  const tfvarsBytes = artifactBytes(tfvars);
  const importsBytes = artifactBytes(imports);
  const lookupBytes = lookup === null ? null : artifactBytes(lookup);
  const totalDesiredBytes = tfvarsBytes.length
    + importsBytes.length
    + (lookupBytes?.length ?? 0);
  if (totalDesiredBytes > MAX_STAGE_PAYLOAD_BYTES) {
    return fail("REFRESH_MATERIALIZATION_STAGE_LIMIT_EXCEEDED", "desired refresh artifacts exceed the aggregate staging limit");
  }

  const expectedDesired = assertion.candidate.desired;
  if (
    !sameState(candidateDesiredState(tfvars), contentState(expectedDesired.tfvars))
    || !sameState(candidateDesiredState(imports), contentState(expectedDesired.imports))
    || !sameState(candidateDesiredState(lookup), contentState(expectedDesired.lookup))
    || (expectedDesired.tfvars.state === "present" && (
      expectedDesired.tfvars.media_type !== tfvars.media_type
      || expectedDesired.tfvars.encoding !== tfvars.encoding
    ))
    || (expectedDesired.imports.state === "present" && (
      expectedDesired.imports.media_type !== imports.media_type
      || expectedDesired.imports.encoding !== imports.encoding
    ))
    || (lookup !== null && expectedDesired.lookup.state === "present" && (
      expectedDesired.lookup.media_type !== lookup.media_type
      || expectedDesired.lookup.encoding !== lookup.encoding
    ))
  ) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "candidate artifact descriptors do not join the assertion");
  }
  const baseline = assertion.candidate.baseline;
  if (
    baseline.moves.state !== "absent"
    || baseline.pending_moves.state !== "absent"
    || baseline.alternate_hcl.state !== "absent"
    || baseline.generated_bindings.state !== "absent"
    || expectedDesired.pending_moves.state !== "absent"
    || expectedDesired.alternate_hcl.state !== "absent"
    || expectedDesired.generated_bindings.state !== "absent"
  ) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "refresh assertion contains unsupported residue states");
  }

  const definitions: Readonly<Record<ArtifactRole, {
    readonly nominatedPath: string;
    readonly baseline: ZccRefreshContentState;
    readonly desired: ZccRefreshContentState;
    readonly bytes: Buffer | null;
  }>> = {
    tfvars: {
      nominatedPath: tfvars.path,
      baseline: contentState(baseline.tfvars),
      desired: contentState(expectedDesired.tfvars),
      bytes: tfvarsBytes,
    },
    imports: {
      nominatedPath: imports.path,
      baseline: contentState(baseline.imports),
      desired: contentState(expectedDesired.imports),
      bytes: importsBytes,
    },
    lookup: {
      nominatedPath: lookupPath,
      baseline: contentState(baseline.lookup),
      desired: contentState(expectedDesired.lookup),
      bytes: lookupBytes,
    },
    moves: {
      nominatedPath: movesPath,
      baseline: contentState(baseline.moves),
      desired: contentState(expectedDesired.moves),
      bytes: null,
    },
    pending_moves: {
      nominatedPath: pendingPath,
      baseline: { state: "absent" },
      desired: { state: "absent" },
      bytes: null,
    },
    alternate_hcl: {
      nominatedPath: alternatePath,
      baseline: { state: "absent" },
      desired: { state: "absent" },
      bytes: null,
    },
    generated_bindings: {
      nominatedPath: generatedPath,
      baseline: { state: "absent" },
      desired: { state: "absent" },
      bytes: null,
    },
  };
  const output = {} as Record<ArtifactRole, ArtifactSpec>;
  const seen = new Set<string>();
  for (const [role, definition] of Object.entries(definitions) as Array<[
    ArtifactRole,
    typeof definitions[ArtifactRole],
  ]>) {
    const absolutePath = resolveAuthorizedPath(
      options.outputRoot,
      options.pathBase,
      definition.nominatedPath,
    );
    if (seen.has(absolutePath)) {
      return fail("INVALID_REFRESH_MATERIALIZATION_CANDIDATE", "refresh artifact targets must be distinct");
    }
    seen.add(absolutePath);
    output[role] = {
      role,
      logicalPath: definition.nominatedPath,
      absolutePath,
      parentPath: path.dirname(absolutePath),
      baseline: definition.baseline,
      desired: definition.desired,
      desiredBytes: definition.bytes,
    };
  }
  return Object.freeze(output);
}

function baselineWithPaths(
  specs: Readonly<Record<ArtifactRole, ArtifactSpec>>,
): ZccPullRefreshArtifactSet["baseline"] {
  const withPath = (role: ArtifactRole): { readonly path: string } & ZccRefreshContentState => ({
    path: specs[role].logicalPath,
    ...specs[role].baseline,
  });
  const states = {
    tfvars: withPath("tfvars"),
    imports: withPath("imports"),
    lookup: withPath("lookup"),
    moves: withPath("moves"),
    pending_moves: withPath("pending_moves"),
    alternate_hcl: withPath("alternate_hcl"),
    generated_bindings: withPath("generated_bindings"),
  };
  return {
    ...states,
    fingerprint_sha256: zccRefreshBaselineFingerprint(states),
  } as ZccPullRefreshArtifactSet["baseline"];
}

function textArtifact(absolutePath: string, content: string): ZccTextArtifact {
  const bytes = Buffer.from(content, "utf8");
  return {
    path: absolutePath,
    media_type: "text/x-hcl",
    encoding: "utf-8",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
    content,
  };
}

function rederiveTransition(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullRefreshParity;
  readonly specs: Readonly<Record<ArtifactRole, ArtifactSpec>>;
  readonly baselineImportsBytes: Buffer;
}): Buffer | null {
  let baselineImports: string;
  try {
    baselineImports = new TextDecoder("utf-8", { fatal: true }).decode(
      options.baselineImportsBytes,
    );
  } catch {
    return fail("INVALID_REFRESH_MATERIALIZATION_BASELINE", "baseline imports are not valid UTF-8");
  }
  const derivation = deriveImportMoves(
    options.candidate.resource_type,
    baselineImports,
    options.candidate.artifacts.imports.content,
  );
  const safe = derivation.moves.map((move) => ({
    from_key: move.oldKey,
    to_key: move.newKey,
  }));
  const suppressed = derivation.suppressed.map((item) => ({
    from_key: item.oldKey,
    to_key: item.newKey,
    reason: item.reason,
  }));
  const movesArtifact = safe.length === 0
    ? null
    : textArtifact(
        options.specs.moves.logicalPath,
        renderMovedBlocks(options.candidate.resource_type, derivation.moves),
      );
  const baseline = baselineWithPaths(options.specs);
  if (baseline.fingerprint_sha256 !== options.assertion.candidate.baseline_fingerprint_sha256) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "asserted baseline fingerprint does not join artifact targets");
  }
  const withoutTransition = {
    kind: "infrawright.zcc_pull_refresh_artifact_set" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    product: "zcc" as const,
    resource_type: options.candidate.resource_type,
    tenant: options.candidate.tenant,
    source: options.candidate.source,
    catalog: options.candidate.catalog,
    root: options.candidate.root,
    baseline,
    status: options.candidate.status === "ready"
        && options.candidate.unexpected_drops.length === 0
        && suppressed.length === 0
      ? "ready" as const
      : "review_required" as const,
    unexpected_drops: [...options.candidate.unexpected_drops],
    moves: { safe, suppressed },
    desired: {
      tfvars: { state: "present" as const, artifact: {
        ...options.candidate.artifacts.tfvars,
      } },
      imports: { state: "present" as const, artifact: {
        ...options.candidate.artifacts.imports,
      } },
      lookup: options.candidate.artifacts.lookup === null
        ? { path: options.specs.lookup.logicalPath, state: "absent" as const }
        : { state: "present" as const, artifact: {
            ...options.candidate.artifacts.lookup,
          } },
      moves: movesArtifact === null
        ? { path: options.specs.moves.logicalPath, state: "absent" as const }
        : { state: "present" as const, artifact: movesArtifact },
    },
  };
  const refresh: ZccPullRefreshArtifactSet = {
    ...withoutTransition,
    transition_sha256: zccRefreshTransitionFingerprint(withoutTransition),
  };
  if (!validateZccPullRefreshArtifactSet(refresh)) {
    throw new ProcessFailure({
      code: "INVALID_REFRESH_MATERIALIZATION_DERIVATION",
      category: "internal",
      message: "rederived refresh transition failed its versioned contract",
      details: schemaErrorDetails(validateZccPullRefreshArtifactSet.errors),
    });
  }
  const expectedNeutral = {
    source: options.assertion.candidate.source,
    catalog: options.assertion.candidate.catalog,
    root: options.assertion.candidate.root,
    baseline: options.assertion.candidate.baseline,
    desired: options.assertion.candidate.desired,
    status: options.assertion.candidate.status,
    unexpected_drops: options.assertion.candidate.unexpected_drops,
    moves: options.assertion.candidate.moves,
    decision_sha256: options.assertion.candidate.decision_sha256,
    evidence_sha256: options.assertion.candidate.evidence_sha256,
  };
  if (
    refresh.transition_sha256 !== options.assertion.candidate.transition_sha256
    || !sameJson(zccPullRefreshNeutralEvidence(refresh), expectedNeutral)
  ) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "rederived refresh transition does not join the assertion");
  }
  const actualMoves = movesArtifact === null ? { state: "absent" as const } : stateFor(
    Buffer.from(movesArtifact.content, "utf8"),
  );
  if (!sameState(actualMoves, options.specs.moves.desired)) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "rederived moves do not join the assertion");
  }
  return movesArtifact === null ? null : Buffer.from(movesArtifact.content, "utf8");
}

function sortedJson(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => sortedJson(item));
  }
  if (typeof value === "object" && value !== null) {
    const input = value as Readonly<Record<string, unknown>>;
    const output: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    for (const key of Object.keys(input).sort()) {
      output[key] = sortedJson(input[key]);
    }
    return output;
  }
  return value;
}

function markerBytes(marker: ZccPullRefreshPendingTransition): Buffer {
  if (!validateZccPullRefreshPendingTransition(marker)) {
    throw new ProcessFailure({
      code: "INVALID_REFRESH_PENDING_TRANSITION",
      category: "internal",
      message: "pending transition failed its versioned contract",
      details: schemaErrorDetails(validateZccPullRefreshPendingTransition.errors),
    });
  }
  const bytes = Buffer.from(`${JSON.stringify(sortedJson(marker), null, 2)}\n`, "utf8");
  if (bytes.length > MAX_MARKER_BYTES) {
    return fail("REFRESH_MATERIALIZATION_STAGE_LIMIT_EXCEEDED", "pending transition exceeds the file limit", "internal");
  }
  return bytes;
}

async function writeAll(handle: FileHandle, bytes: Buffer): Promise<void> {
  let offset = 0;
  while (offset < bytes.length) {
    const result = await handle.write(bytes, offset, bytes.length - offset, null);
    if (result.bytesWritten <= 0) {
      return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact could not be staged", "io");
    }
    offset += result.bytesWritten;
  }
}

async function stagePayload(options: {
  readonly role: ZccRefreshPayloadRole | "pending_moves";
  readonly spec: ArtifactSpec;
  readonly bytes: Buffer;
  readonly parent: DirectoryBinding;
  readonly stages: StagedPayload[];
  readonly stageBudget: { bytes: number };
}): Promise<StagedPayload> {
  if (options.role === "pending_moves") {
    if (options.bytes.length > MAX_MARKER_BYTES) {
      return fail("REFRESH_MATERIALIZATION_STAGE_LIMIT_EXCEEDED", "pending transition exceeds its staging limit");
    }
  } else {
    options.stageBudget.bytes += options.bytes.length;
    if (options.stageBudget.bytes > MAX_STAGE_PAYLOAD_BYTES) {
      return fail("REFRESH_MATERIALIZATION_STAGE_LIMIT_EXCEEDED", "refresh payload staging exceeds the aggregate limit");
    }
  }
  await recheckParent(options.parent, true);
  let handle: FileHandle | null = null;
  let tempPath = "";
  for (let attempt = 0; attempt < TEMP_ATTEMPTS; attempt += 1) {
    tempPath = path.join(
      options.spec.parentPath,
      `.infrawright-refresh-${randomBytes(16).toString("hex")}.tmp`,
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
        return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact could not be staged", "io");
      }
    }
  }
  if (handle === null) {
    return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact could not be staged", "io");
  }
  let registered = false;
  try {
    const opened = await handle.stat({ bigint: true });
    if (!opened.isFile()) {
      return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact staging inode is unsafe", "io");
    }
    const stage: StagedPayload = {
      role: options.role,
      spec: options.spec,
      tempPath,
      bytes: options.bytes,
      metadata: metadataOf(opened),
      aliasPresent: true,
    };
    options.stages.push(stage);
    registered = true;
    await refreshParent(options.parent);
    await writeAll(handle, options.bytes);
    await handle.sync();
    const afterStat = await handle.stat({ bigint: true });
    const pathStat = await lstat(tempPath, { bigint: true });
    const after = metadataOf(afterStat);
    const bytes = await readHandleBytes(handle, options.bytes.length);
    if (
      !afterStat.isFile()
      || !pathStat.isFile()
      || pathStat.isSymbolicLink()
      || !sameMetadata(after, metadataOf(pathStat))
      || !bytes.equals(options.bytes)
    ) {
      return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact staging file changed", "io");
    }
    stage.metadata = after;
    return stage;
  } catch (error: unknown) {
    if (!registered) {
      await unlink(tempPath).catch(() => undefined);
      await refreshParent(options.parent).catch(() => undefined);
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("REFRESH_MATERIALIZATION_STAGE_FAILED", "an artifact could not be staged", "io");
  } finally {
    await handle.close().catch(() => undefined);
  }
}

async function cleanupStages(
  stages: readonly StagedPayload[],
  parents: ReadonlyMap<string, DirectoryBinding>,
): Promise<boolean> {
  let complete = true;
  for (const stage of stages) {
    if (!stage.aliasPresent) {
      continue;
    }
    try {
      const current = await lstat(stage.tempPath, { bigint: true });
      if (
        !current.isFile()
        || current.isSymbolicLink()
        || !sameIdentity(stage.metadata, metadataOf(current))
      ) {
        complete = false;
        continue;
      }
      await unlink(stage.tempPath);
      stage.aliasPresent = false;
      const parent = parents.get(stage.spec.parentPath);
      if (parent === undefined) {
        complete = false;
      } else {
        await refreshParent(parent);
      }
    } catch {
      complete = false;
    }
  }
  return complete;
}

async function verifyStage(stage: StagedPayload): Promise<void> {
  if (!stage.aliasPresent) {
    return fail("REFRESH_MATERIALIZATION_STAGE_CHANGED", "a staging alias disappeared before publication", "io");
  }
  const current = await readBoundContent(
    stage.tempPath,
    { bytes: 0 },
    stage.metadata,
    stage.role === "pending_moves" ? MAX_MARKER_BYTES : MAX_FILE_BYTES,
  );
  if (
    current.bytes === null
    || current.metadata === null
    || !current.bytes.equals(stage.bytes)
    || !sameIdentity(current.metadata, stage.metadata)
  ) {
    return fail("REFRESH_MATERIALIZATION_STAGE_CHANGED", "a staging file changed before publication", "io");
  }
}

async function verifyExactState(
  spec: ArtifactSpec,
  expected: ZccRefreshContentState,
  expectedIdentity?: MetadataBinding | null,
): Promise<BoundContent> {
  const current = await readBoundContent(
    spec.absolutePath,
    { bytes: 0 },
    undefined,
    spec.role === "pending_moves" ? MAX_MARKER_BYTES : MAX_FILE_BYTES,
  );
  if (
    !sameState(current.state, expected)
    || (expectedIdentity !== undefined
      && expectedIdentity !== null
      && (current.metadata === null || !sameIdentity(expectedIdentity, current.metadata)))
    || (expectedIdentity === null && current.metadata !== null)
  ) {
    return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "an artifact target changed", "io");
  }
  return current;
}

async function syncFile(absolutePath: string): Promise<void> {
  let handle: FileHandle | null = null;
  try {
    handle = await open(
      absolutePath,
      constants.O_RDONLY | constants.O_NONBLOCK | constants.O_NOFOLLOW,
    );
    await handle.sync();
  } catch {
    return fail("REFRESH_MATERIALIZATION_SYNC_FAILED", "an artifact could not be synchronized", "io");
  } finally {
    await handle?.close().catch(() => undefined);
  }
}

async function finishLinkedStage(
  stage: StagedPayload,
  parent: DirectoryBinding,
): Promise<void> {
  const published = await verifyExactState(stage.spec, stateFor(stage.bytes), stage.metadata);
  if (published.metadata === null) {
    return fail("INTERNAL_ERROR", "published artifact identity is missing", "internal");
  }
  stage.metadata = published.metadata;
  await syncFile(stage.spec.absolutePath);
  try {
    await unlink(stage.tempPath);
    stage.aliasPresent = false;
  } catch {
    return fail("REFRESH_MATERIALIZATION_PUBLISH_FAILED", "a staging alias could not be removed", "io");
  }
  await refreshParent(parent);
  const final = await verifyExactState(stage.spec, stateFor(stage.bytes), stage.metadata);
  if (final.metadata === null) {
    return fail("INTERNAL_ERROR", "published artifact identity is missing", "internal");
  }
  stage.metadata = final.metadata;
}

async function publishMarker(options: {
  readonly stage: StagedPayload;
  readonly parent: DirectoryBinding;
  readonly hooks?: ZccPullRefreshMaterializationHooks;
}): Promise<void> {
  await verifyStage(options.stage);
  await verifyExactState(options.stage.spec, { state: "absent" }, null);
  await recheckParent(options.parent, true);
  await invokeHook(options.hooks?.beforeMarkerLink);
  await verifyStage(options.stage);
  await verifyExactState(options.stage.spec, { state: "absent" }, null);
  await recheckParent(options.parent, true);
  try {
    await link(options.stage.tempPath, options.stage.spec.absolutePath);
  } catch {
    return fail("REFRESH_MATERIALIZATION_MARKER_PUBLISH_FAILED", "pending transition could not be published without replacement", "io");
  }
  await invokeHook(options.hooks?.afterMarkerLink);
  await refreshParent(options.parent);
  await finishLinkedStage(options.stage, options.parent);
  await syncParent(options.stage.spec.parentPath);
  await invokeHook(options.hooks?.afterMarkerSync);
}

async function publishRole(options: {
  readonly stage: StagedPayload | null;
  readonly spec: ArtifactSpec;
  readonly current: BoundContent;
  readonly parent: DirectoryBinding;
  readonly onMutated: () => void;
}): Promise<void> {
  await verifyExactState(options.spec, options.current.state, options.current.metadata);
  await recheckParent(options.parent, true);
  if (options.spec.desired.state === "absent") {
    if (options.current.state.state === "absent") {
      return;
    }
    try {
      await unlink(options.spec.absolutePath);
      options.onMutated();
    } catch {
      return fail("REFRESH_MATERIALIZATION_PUBLISH_FAILED", "an obsolete artifact could not be removed", "io");
    }
    await refreshParent(options.parent);
    return;
  }
  const stage = options.stage;
  if (stage === null) {
    return fail("INTERNAL_ERROR", "desired refresh payload was not staged", "internal");
  }
  await verifyStage(stage);
  if (options.current.state.state === "absent") {
    try {
      await link(stage.tempPath, options.spec.absolutePath);
      options.onMutated();
    } catch {
      return fail("REFRESH_MATERIALIZATION_PUBLISH_FAILED", "an artifact could not be published without replacement", "io");
    }
    await refreshParent(options.parent);
    await finishLinkedStage(stage, options.parent);
    return;
  }
  try {
    await rename(stage.tempPath, options.spec.absolutePath);
    stage.aliasPresent = false;
    options.onMutated();
  } catch {
    return fail("REFRESH_MATERIALIZATION_PUBLISH_FAILED", "an artifact could not be replaced", "io");
  }
  await refreshParent(options.parent);
  const published = await verifyExactState(options.spec, stateFor(stage.bytes), stage.metadata);
  if (published.metadata === null) {
    return fail("INTERNAL_ERROR", "published artifact identity is missing", "internal");
  }
  stage.metadata = published.metadata;
  await syncFile(options.spec.absolutePath);
}

function ownFunction<T extends (...args: never[]) => unknown>(
  value: unknown,
  key: string,
): T | undefined {
  if (typeof value !== "object" || value === null) {
    return undefined;
  }
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) {
    return undefined;
  }
  if (!("value" in descriptor) || typeof descriptor.value !== "function") {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization callbacks must be inert functions");
  }
  return descriptor.value as T;
}

function inertOptionsRecord(value: unknown): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be an object");
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be plain data");
  }
  return value as Readonly<Record<string, unknown>>;
}

function ownDataValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
  optional = false,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined && optional) {
    return undefined;
  }
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input must be inert data");
  }
  return descriptor.value;
}

function snapshotHooks(value: unknown): ZccPullRefreshMaterializationHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization hooks are invalid");
  }
  const afterBound = ownFunction<() => void | Promise<void>>(value, "afterBound");
  const afterStage = ownFunction<NonNullable<ZccPullRefreshMaterializationHooks["afterStage"]>>(value, "afterStage");
  const beforeMarkerLink = ownFunction<() => void | Promise<void>>(value, "beforeMarkerLink");
  const afterMarkerLink = ownFunction<() => void | Promise<void>>(value, "afterMarkerLink");
  const afterMarkerSync = ownFunction<() => void | Promise<void>>(value, "afterMarkerSync");
  const beforePublish = ownFunction<NonNullable<ZccPullRefreshMaterializationHooks["beforePublish"]>>(value, "beforePublish");
  const afterPublish = ownFunction<NonNullable<ZccPullRefreshMaterializationHooks["afterPublish"]>>(value, "afterPublish");
  const afterPublishParentSync = ownFunction<NonNullable<ZccPullRefreshMaterializationHooks["afterPublishParentSync"]>>(value, "afterPublishParentSync");
  const beforeMarkerRemove = ownFunction<() => void | Promise<void>>(value, "beforeMarkerRemove");
  const afterMarkerRemove = ownFunction<() => void | Promise<void>>(value, "afterMarkerRemove");
  const beforeFinalCas = ownFunction<() => void | Promise<void>>(value, "beforeFinalCas");
  return Object.freeze({
    ...(afterBound === undefined ? {} : { afterBound }),
    ...(afterStage === undefined ? {} : { afterStage }),
    ...(beforeMarkerLink === undefined ? {} : { beforeMarkerLink }),
    ...(afterMarkerLink === undefined ? {} : { afterMarkerLink }),
    ...(afterMarkerSync === undefined ? {} : { afterMarkerSync }),
    ...(beforePublish === undefined ? {} : { beforePublish }),
    ...(afterPublish === undefined ? {} : { afterPublish }),
    ...(afterPublishParentSync === undefined ? {} : { afterPublishParentSync }),
    ...(beforeMarkerRemove === undefined ? {} : { beforeMarkerRemove }),
    ...(afterMarkerRemove === undefined ? {} : { afterMarkerRemove }),
    ...(beforeFinalCas === undefined ? {} : { beforeFinalCas }),
  });
}

function markerFor(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullRefreshParity;
  readonly expectedBinding: ZccPullRefreshParityBindingEvidence;
  readonly specs: Readonly<Record<ArtifactRole, ArtifactSpec>>;
}): ZccPullRefreshPendingTransition {
  const marker: ZccPullRefreshPendingTransition = {
    kind: "infrawright.zcc_pull_refresh_pending_transition",
    schema_version: 1,
    mode: "refresh",
    product: "zcc",
    resource_type: options.candidate.resource_type,
    tenant: options.candidate.tenant,
    candidate_request_sha256: options.expectedBinding.request_sha256,
    assertion_sha256: options.assertion.assertion_sha256,
    baseline_fingerprint_sha256: options.assertion.candidate.baseline_fingerprint_sha256,
    transition_sha256: options.assertion.candidate.transition_sha256,
    safe_move_count: options.assertion.candidate.moves.safe_count,
    desired_move: options.specs.moves.desired,
  };
  if (!validateZccPullRefreshPendingTransition(marker)) {
    throw new ProcessFailure({
      code: "INVALID_REFRESH_PENDING_TRANSITION",
      category: "internal",
      message: "pending transition failed its versioned contract",
      details: schemaErrorDetails(validateZccPullRefreshPendingTransition.errors),
    });
  }
  return Object.freeze(marker);
}

function markerObservation(current: BoundContent, expectedBytes: Buffer): "absent" | "exact" | "foreign" {
  if (current.state.state === "absent") {
    return "absent";
  }
  return current.bytes !== null && current.bytes.equals(expectedBytes)
    ? "exact"
    : "foreign";
}

function classifyCurrent(
  specs: Readonly<Record<ArtifactRole, ArtifactSpec>>,
  current: Readonly<Record<ArtifactRole, BoundContent>>,
  expectedMarkerBytes: Buffer,
) {
  return classifyZccRefreshTransition({
    baseline: {
      lookup: specs.lookup.baseline,
      moves: specs.moves.baseline,
      tfvars: specs.tfvars.baseline,
      imports: specs.imports.baseline,
    },
    desired: {
      lookup: specs.lookup.desired,
      moves: specs.moves.desired,
      tfvars: specs.tfvars.desired,
      imports: specs.imports.desired,
    },
    current: {
      lookup: current.lookup.state,
      moves: current.moves.state,
      tfvars: current.tfvars.state,
      imports: current.imports.state,
    },
    reserved: {
      alternate_hcl: current.alternate_hcl.state,
      generated_bindings: current.generated_bindings.state,
    },
    marker: markerObservation(current.pending_moves, expectedMarkerBytes),
  });
}

async function recheckExternalBindings(options: {
  readonly expectedBinding: ZccPullRefreshParityBindingEvidence;
  readonly currentBinding: () => Promise<ZccPullRefreshParityBindingEvidence>;
  readonly recheckImmutableInputs: () => Promise<void>;
}): Promise<void> {
  try {
    await options.recheckImmutableInputs();
    const current = snapshotBinding(await options.currentBinding());
    if (!sameJson(current, options.expectedBinding)) {
      return fail("REFRESH_MATERIALIZATION_BINDING_CHANGED", "refresh materialization binding changed", "io");
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("REFRESH_MATERIALIZATION_BINDING_CHANGED", "refresh materialization binding changed", "io");
  }
}

function exactClassification(
  left: ReturnType<typeof classifyCurrent>,
  right: ReturnType<typeof classifyCurrent>,
): boolean {
  return sameJson(left, right);
}

function verifyPublishedPayloadStates(
  current: Readonly<Record<ArtifactRole, BoundContent>>,
  specs: Readonly<Record<ArtifactRole, ArtifactSpec>>,
): void {
  for (const role of ["lookup", "moves", "tfvars", "imports"] as const) {
    if (!sameState(current[role].state, specs[role].desired)) {
      return fail("REFRESH_MATERIALIZATION_FINAL_VERIFICATION_FAILED", "refresh payloads are not exactly desired", "io");
    }
  }
  if (
    current.alternate_hcl.state.state !== "absent"
    || current.generated_bindings.state.state !== "absent"
  ) {
    return fail("REFRESH_MATERIALIZATION_FINAL_VERIFICATION_FAILED", "refresh reserved artifacts are invalid", "io");
  }
}

/**
 * Durably publish one ready ZCC refresh assertion. Payloads advance in the
 * fixed lookup -> moves -> tfvars -> imports order behind an immutable marker.
 */
export async function materializeReadyZccPullRefresh(options: {
  readonly outputRoot: string;
  readonly pathBase: string;
  readonly candidate: ZccPullArtifactSet;
  readonly assertion: ZccPullRefreshParity;
  readonly expectedBinding: ZccPullRefreshParityBindingEvidence;
  readonly currentBinding: () => Promise<ZccPullRefreshParityBindingEvidence>;
  readonly recheckImmutableInputs: () => Promise<void>;
  readonly hooks?: ZccPullRefreshMaterializationHooks;
}): Promise<ZccPullRefreshMaterialization> {
  // Snapshot all caller-owned data and callback references before the first
  // await; a retained caller reference must not retarget an asserted write.
  const input = inertOptionsRecord(options as unknown);
  const candidate = snapshotCandidate(ownDataValue(input, "candidate"));
  const assertion = snapshotZccPullRefreshMaterializationAssertion(
    ownDataValue(input, "assertion"),
  );
  const expectedBinding = snapshotBinding(ownDataValue(input, "expectedBinding"));
  const outputRoot = ownDataValue(input, "outputRoot");
  const pathBase = ownDataValue(input, "pathBase");
  const currentBinding = ownFunction<
    () => Promise<ZccPullRefreshParityBindingEvidence>
  >(input, "currentBinding");
  const recheckImmutableInputs = ownFunction<() => Promise<void>>(
    input,
    "recheckImmutableInputs",
  );
  const hooks = snapshotHooks(ownDataValue(input, "hooks", true));
  if (
    typeof outputRoot !== "string"
    || typeof pathBase !== "string"
    || typeof currentBinding !== "function"
    || typeof recheckImmutableInputs !== "function"
  ) {
    return fail("INVALID_MATERIALIZATION_INPUT", "refresh materialization input is invalid");
  }
  requireReadyInputs({ candidate, assertion, expectedBinding });
  const root = await bindOutputRoot(outputRoot);
  const specs = buildSpecs({ outputRoot, pathBase, candidate, assertion });
  const parents = await bindParents(
    root,
    Object.values(specs).map((spec) => spec.absolutePath),
  );
  const initialCurrent = await readAllStates(specs);
  const baseline = baselineWithPaths(specs);
  if (baseline.fingerprint_sha256 !== assertion.candidate.baseline_fingerprint_sha256) {
    return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "asserted baseline fingerprint does not join materialization paths");
  }
  const marker = markerFor({ candidate, assertion, expectedBinding, specs });
  const expectedMarkerBytes = markerBytes(marker);
  const initial = classifyCurrent(specs, initialCurrent, expectedMarkerBytes);
  if (initial.state === "ambiguous") {
    return fail(
      "AMBIGUOUS_REFRESH_MATERIALIZATION_STATE",
      `refresh materialization state is ambiguous: ${initial.reason}`,
      "io",
    );
  }

  // When imports is still baseline-compatible, deterministically reconstruct
  // the transition from the original imports and exact-join the assertion.
  // Once imports is desired, the exact marker and desired prefix carry the old
  // move evidence; re-deriving from the new imports would erase that evidence.
  if (sameState(initialCurrent.imports.state, specs.imports.baseline)) {
    if (initialCurrent.imports.bytes === null) {
      return fail("INVALID_REFRESH_MATERIALIZATION_BASELINE", "baseline imports artifact is missing");
    }
    specs.moves.desiredBytes = rederiveTransition({
      candidate,
      assertion,
      specs,
      baselineImportsBytes: initialCurrent.imports.bytes,
    });
  } else if (
    !sameState(initialCurrent.imports.state, specs.imports.desired)
    || (initial.state !== "committed" && initial.state !== "already_complete")
  ) {
    return fail("AMBIGUOUS_REFRESH_MATERIALIZATION_STATE", "refresh imports do not prove an asserted transition", "io");
  }

  await invokeHook(hooks?.afterBound);
  for (const parent of parents.values()) {
    await recheckParent(parent, false);
  }
  await recheckExternalBindings({ expectedBinding, currentBinding, recheckImmutableInputs });
  const rebound = await readAllStates(specs, initialCurrent);
  const reboundClassification = classifyCurrent(specs, rebound, expectedMarkerBytes);
  if (!exactClassification(initial, reboundClassification)) {
    return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "refresh payload state changed after binding", "io");
  }

  const stages: StagedPayload[] = [];
  const stageByRole = new Map<ZccRefreshPayloadRole, StagedPayload>();
  const stageBudget = { bytes: 0 };
  let markerStage: StagedPayload | null = null;
  let mutationCount = 0;
  let markerLinkedByThisRun = false;
  let durableBoundary = markerObservation(
    rebound.pending_moves,
    expectedMarkerBytes,
  ) === "exact";
  const needsMarker = markerObservation(rebound.pending_moves, expectedMarkerBytes) === "absent"
    && initial.remaining.length > 0;
  try {
    if (needsMarker) {
      const markerSpec: ArtifactSpec = {
        ...specs.pending_moves,
        desired: stateFor(expectedMarkerBytes),
        desiredBytes: expectedMarkerBytes,
      };
      const parent = parents.get(markerSpec.parentPath);
      if (parent === undefined) {
        return fail("INTERNAL_ERROR", "pending marker parent binding is missing", "internal");
      }
      markerStage = await stagePayload({
        role: "pending_moves",
        spec: markerSpec,
        bytes: expectedMarkerBytes,
        parent,
        stages,
        stageBudget,
      });
      await invokeStageHook(hooks?.afterStage, "pending_moves", 0);
    }
    for (const [index, role] of initial.remaining.entries()) {
      const spec = specs[role];
      if (spec.desired.state === "absent") {
        continue;
      }
      if (spec.desiredBytes === null) {
        return fail("INTERNAL_ERROR", "desired refresh payload bytes are unavailable", "internal");
      }
      if (!sameState(stateFor(spec.desiredBytes), spec.desired)) {
        return fail("REFRESH_MATERIALIZATION_ASSERTION_MISMATCH", "desired payload bytes do not join the assertion", "internal");
      }
      const parent = parents.get(spec.parentPath);
      if (parent === undefined) {
        return fail("INTERNAL_ERROR", "payload parent binding is missing", "internal");
      }
      const stage = await stagePayload({
        role,
        spec,
        bytes: spec.desiredBytes,
        parent,
        stages,
        stageBudget,
      });
      stageByRole.set(role, stage);
      await invokeStageHook(hooks?.afterStage, role, index + (needsMarker ? 1 : 0));
    }

    // Test hooks and untrusted ambient processes can mutate a hidden alias.
    // Rebind every stage after all staging hooks and again immediately before
    // its atomic link/rename so foreign bytes can never cross the fence.
    for (const stage of stages) {
      await verifyStage(stage);
    }

    await recheckExternalBindings({ expectedBinding, currentBinding, recheckImmutableInputs });
    for (const parent of parents.values()) {
      await recheckParent(parent, true);
    }
    const prepublish = await readAllStates(specs, rebound);
    const prepublishClassification = classifyCurrent(specs, prepublish, expectedMarkerBytes);
    if (!exactClassification(initial, prepublishClassification)) {
      return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "refresh payload state changed before publication", "io");
    }

    if (markerStage !== null) {
      const markerParent = parents.get(markerStage.spec.parentPath);
      if (markerParent === undefined) {
        return fail("INTERNAL_ERROR", "pending marker parent binding is missing", "internal");
      }
      await publishMarker({
        stage: markerStage,
        parent: markerParent,
        hooks: {
          ...hooks,
          afterMarkerLink: async () => {
            markerLinkedByThisRun = true;
            durableBoundary = true;
            await invokeHook(hooks?.afterMarkerLink);
          },
        },
      });
    }

    const advanced: ZccRefreshPayloadRole[] = [];
    let current = prepublish;
    for (const [index, role] of initial.remaining.entries()) {
      const spec = specs[role];
      const parent = parents.get(spec.parentPath);
      if (parent === undefined) {
        return fail("INTERNAL_ERROR", "payload parent binding is missing", "internal");
      }
      await invokeRoleHook(hooks?.beforePublish, role, index);
      await publishRole({
        stage: stageByRole.get(role) ?? null,
        spec,
        current: current[role],
        parent,
        onMutated: () => {
          mutationCount += 1;
          durableBoundary = true;
        },
      });
      await invokeRoleHook(hooks?.afterPublish, role, index);
      await syncParent(spec.parentPath);
      await invokeRoleHook(hooks?.afterPublishParentSync, role, index);
      const refreshedRole = await verifyExactState(spec, spec.desired);
      current = Object.freeze({ ...current, [role]: refreshedRole });
      advanced.push(role);
    }

    const hasMoves = specs.moves.desired.state === "present";
    if (!hasMoves) {
      const markerCurrent = await readBoundContent(
        specs.pending_moves.absolutePath,
        { bytes: 0 },
        undefined,
        MAX_MARKER_BYTES,
      );
      if (markerObservation(markerCurrent, expectedMarkerBytes) === "exact") {
        await invokeHook(hooks?.beforeMarkerRemove);
        const markerParent = parents.get(specs.pending_moves.parentPath);
        if (markerParent === undefined) {
          return fail("INTERNAL_ERROR", "pending marker parent binding is missing", "internal");
        }
        await recheckExternalBindings({ expectedBinding, currentBinding, recheckImmutableInputs });
        for (const parent of parents.values()) {
          await recheckParent(parent, true);
        }
        const beforeMarkerRemoval = await readAllStates(specs);
        verifyPublishedPayloadStates(beforeMarkerRemoval, specs);
        if (markerObservation(beforeMarkerRemoval.pending_moves, expectedMarkerBytes) !== "exact") {
          return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "pending transition changed before removal", "io");
        }
        await verifyExactState(
          specs.pending_moves,
          stateFor(expectedMarkerBytes),
          markerCurrent.metadata,
        );
        await recheckParent(markerParent, true);
        try {
          await unlink(specs.pending_moves.absolutePath);
          mutationCount += 1;
          durableBoundary = true;
        } catch {
          return fail("REFRESH_MATERIALIZATION_MARKER_REMOVE_FAILED", "pending transition could not be removed", "io");
        }
        await invokeHook(hooks?.afterMarkerRemove);
        await refreshParent(markerParent);
        await syncParent(specs.pending_moves.parentPath);
      } else if (markerObservation(markerCurrent, expectedMarkerBytes) !== "absent") {
        return fail("REFRESH_MATERIALIZATION_TARGET_CHANGED", "pending transition changed before removal", "io");
      }
    }

    await invokeHook(hooks?.beforeFinalCas);
    await recheckExternalBindings({ expectedBinding, currentBinding, recheckImmutableInputs });
    for (const parent of parents.values()) {
      await recheckParent(parent, true);
    }
    const final = await readAllStates(specs);
    verifyPublishedPayloadStates(final, specs);
    if (
      hasMoves
        ? markerObservation(final.pending_moves, expectedMarkerBytes) !== "exact"
        : final.pending_moves.state.state !== "absent"
    ) {
      return fail("REFRESH_MATERIALIZATION_FINAL_VERIFICATION_FAILED", "refresh fence is invalid", "io");
    }
    const result: ZccPullRefreshMaterialization = {
      kind: "infrawright.zcc_pull_refresh_materialization",
      schema_version: 1,
      mode: "refresh",
      product: "zcc",
      resource_type: candidate.resource_type,
      tenant: candidate.tenant,
      status: hasMoves ? "awaiting_apply" : "complete",
      publication: {
        policy: "replace_or_verify_exact_imports_last",
        advanced: Object.freeze([...advanced]),
      },
      transition: {
        initial: initial.state,
        final: hasMoves ? "committed" : "already_complete",
        next_action: hasMoves ? "apply_moves_then_ack" : "none",
      },
      verification: {
        candidate_request_sha256: expectedBinding.request_sha256,
        assertion_sha256: assertion.assertion_sha256,
        baseline_fingerprint_sha256: assertion.candidate.baseline_fingerprint_sha256,
        transition_sha256: assertion.candidate.transition_sha256,
        artifacts: Object.freeze({
          tfvars: final.tfvars.state,
          imports: final.imports.state,
          lookup: final.lookup.state,
          moves: final.moves.state,
          pending_moves: final.pending_moves.state,
          alternate_hcl: final.alternate_hcl.state,
          generated_bindings: final.generated_bindings.state,
        }),
      },
    };
    if (!validateZccPullRefreshMaterialization(result)) {
      throw new ProcessFailure({
        code: "INVALID_REFRESH_MATERIALIZATION_RESULT",
        category: "internal",
        message: "refresh materialization result failed its versioned contract",
        details: schemaErrorDetails(validateZccPullRefreshMaterialization.errors),
      });
    }
    const clean = await cleanupStages(stages, parents);
    if (!clean) {
      return fail("REFRESH_MATERIALIZATION_CLEANUP_FAILED", "refresh staging cleanup was incomplete", "io");
    }
    return Object.freeze(result);
  } catch (error: unknown) {
    const clean = await cleanupStages(stages, parents);
    if (mutationCount > 0 || markerLinkedByThisRun) {
      return fail(
        "REFRESH_MATERIALIZATION_INDETERMINATE",
        "refresh materialization stopped after crossing a durable boundary; retry to finish forward",
        "io",
        true,
      );
    }
    if (!clean) {
      return fail(
        "REFRESH_MATERIALIZATION_CLEANUP_FAILED",
        "refresh materialization failed before publication and staging cleanup was incomplete",
        "io",
      );
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    if (durableBoundary) {
      return fail(
        "REFRESH_MATERIALIZATION_INDETERMINATE",
        "refresh materialization stopped after crossing a durable boundary; retry to finish forward",
        "io",
        true,
      );
    }
    return fail("REFRESH_MATERIALIZATION_FAILED", "refresh materialization failed safely", "io");
  }
}
