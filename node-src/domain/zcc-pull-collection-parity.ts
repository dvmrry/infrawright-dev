import path from "node:path";
import { types as utilTypes } from "node:util";
import {
  fstatSync,
  lstatSync,
  realpathSync,
} from "node:fs";
import { lstat, realpath } from "node:fs/promises";

import {
  validateZccPullCollection,
  validateZccPullCollectionParity,
} from "../contracts/validators.js";
import {
  ReadBudget,
  readBoundedFileBytes,
} from "../io/bounded-files.js";
import {
  bindDirectory,
  type BoundDirectory,
} from "../io/zcc-pull-publisher.js";
import { parseZccPullDataJson } from "../json/zcc-pull-data.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import { ProcessFailure } from "./errors.js";
import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_RESOURCE_TYPES,
  type ZccCollectionResourceType,
} from "./zcc-collection-contract.js";
import type { ZccPullCollectionReceipt } from "./zcc-pull-collection.js";

const MAX_FILE_BYTES = 4n * 1024n * 1024n;
const TOTAL_FILES = ZCC_COLLECTION_RESOURCE_TYPES.length * 3;
const TENANT_PATTERN = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;

export interface ZccPullCollectionParityTuple {
  readonly sha256: string;
  readonly size_bytes: number;
  readonly item_count: number;
}

export type ZccPullCollectionParityStatus =
  | "equal"
  | "different"
  | "unstable_reference";

export interface ZccPullCollectionParityResource {
  readonly resource_type: ZccCollectionResourceType;
  readonly path: string;
  readonly before: ZccPullCollectionParityTuple;
  readonly node: ZccPullCollectionParityTuple;
  readonly after: ZccPullCollectionParityTuple;
  readonly status: ZccPullCollectionParityStatus;
}

export interface ZccPullCollectionParity {
  readonly kind: "infrawright.zcc_pull_collection_parity";
  readonly schema_version: 1;
  readonly reference: "python_stability_window";
  readonly product: "zcc";
  readonly catalog_sources_sha256: string;
  readonly tenant: string;
  readonly status: ZccPullCollectionParityStatus;
  readonly counts: {
    readonly total: 5;
    readonly equal: number;
    readonly different: number;
    readonly unstable_reference: number;
  };
  readonly resources: readonly ZccPullCollectionParityResource[];
}

export interface CompareZccPullCollectionOptions {
  readonly context: {
    readonly node_workspace: string;
    readonly python_before_workspace: string;
    readonly python_after_workspace: string;
  };
  readonly reference: "python_stability_window";
  readonly tenant: string;
  readonly receipts: readonly ZccPullCollectionReceipt[];
  /** Trusted test seam. Public process requests cannot supply hooks. */
  readonly hooks?: {
    readonly afterInputsBound?: () => void | Promise<void>;
    readonly afterArtifactRechecked?: (index: number) => void | Promise<void>;
  };
}

type DirectoryLevel = "workspace" | "pulls" | "tenant";

interface BoundParityDirectory {
  readonly level: DirectoryLevel;
  readonly path: string;
  readonly directory: BoundDirectory;
  readonly version: FileVersion;
}

interface BoundWorkspace {
  readonly role: "before" | "node" | "after";
  readonly path: string;
  readonly directories: readonly BoundParityDirectory[];
}

interface BoundArtifact {
  readonly absolutePath: string;
  readonly identity: FileVersion;
  readonly tuple: ZccPullCollectionParityTuple;
}

interface FileVersion {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}

function fail(
  code: string,
  message: string,
  category: "request" | "domain" | "io" | "internal" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function dataValue(record: object, key: string): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(record, key);
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity input must be inert data",
      "request",
    );
  }
  return descriptor.value;
}

function dataRecord(value: unknown): Readonly<Record<string, unknown>> {
  if (
    typeof value !== "object"
    || value === null
    || Array.isArray(value)
    || utilTypes.isProxy(value)
    || (Object.getPrototypeOf(value) !== Object.prototype
      && Object.getPrototypeOf(value) !== null)
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity input must be inert data",
      "request",
    );
  }
  return value as Readonly<Record<string, unknown>>;
}

function exactKeys(
  value: Readonly<Record<string, unknown>>,
  required: readonly string[],
  optional: readonly string[] = [],
): void {
  const keys = Reflect.ownKeys(value);
  const allowed = new Set([...required, ...optional]);
  if (
    keys.some((key) => typeof key !== "string" || !allowed.has(key))
    || required.some((key) => !keys.includes(key))
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity input has an unsupported shape",
      "request",
    );
  }
  for (const key of keys as string[]) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
        "collection parity input must be inert data",
        "request",
      );
    }
  }
}

function dataString(value: unknown): string {
  if (typeof value !== "string") {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity input contains an invalid string",
      "request",
    );
  }
  return value;
}

function snapshotReceipt(value: unknown): ZccPullCollectionReceipt {
  const receipt = dataRecord(value);
  exactKeys(receipt, [
    "kind",
    "schema_version",
    "mode",
    "product",
    "tenant",
    "resource_type",
    "status",
    "catalog_sources_sha256",
    "artifact",
    "publication",
  ]);
  const artifact = dataRecord(dataValue(receipt, "artifact"));
  exactKeys(artifact, [
    "path",
    "media_type",
    "encoding",
    "sha256",
    "size_bytes",
    "item_count",
  ]);
  const publication = dataRecord(dataValue(receipt, "publication"));
  exactKeys(publication, ["policy", "action"]);
  if (!validateZccPullCollection(value)) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_RECEIPT",
      "collection parity receipt failed its versioned contract",
      "request",
    );
  }
  const snapshot = snapshotPlainJsonGraph(value, { maxDepth: 8 });
  if (!snapshot.ok) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_RECEIPT",
      "collection parity receipt failed its versioned contract",
      "request",
    );
  }
  return snapshot.value as ZccPullCollectionReceipt;
}

function snapshotReceipts(value: unknown): readonly ZccPullCollectionReceipt[] {
  if (
    !Array.isArray(value)
    || utilTypes.isProxy(value)
    || Object.getPrototypeOf(value) !== Array.prototype
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_RECEIPTS",
      "collection parity requires the complete ordered receipt set",
      "request",
    );
  }
  const lengthDescriptor = Object.getOwnPropertyDescriptor(value, "length");
  if (
    lengthDescriptor === undefined
    || !("value" in lengthDescriptor)
    || lengthDescriptor.value !== ZCC_COLLECTION_RESOURCE_TYPES.length
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_RECEIPTS",
      "collection parity requires the complete ordered receipt set",
      "request",
    );
  }
  const expectedKeys = new Set([
    "length",
    ...ZCC_COLLECTION_RESOURCE_TYPES.map((_, index) => String(index)),
  ]);
  const ownKeys = Reflect.ownKeys(value);
  if (
    ownKeys.length !== expectedKeys.size
    || ownKeys.some((key) => typeof key !== "string" || !expectedKeys.has(key))
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_RECEIPTS",
      "collection parity receipt set must be inert data",
      "request",
    );
  }
  return Object.freeze(ZCC_COLLECTION_RESOURCE_TYPES.map((_, index) => {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_RECEIPTS",
        "collection parity receipt set must be inert data",
        "request",
      );
    }
    return snapshotReceipt(descriptor.value);
  }));
}

function logicalPath(tenant: string, resourceType: ZccCollectionResourceType): string {
  return `pulls/${tenant}/${resourceType}.json`;
}

function validateReceiptSet(
  tenant: string,
  receipts: readonly ZccPullCollectionReceipt[],
): void {
  for (const [index, resourceType] of ZCC_COLLECTION_RESOURCE_TYPES.entries()) {
    const receipt = receipts[index];
    if (
      receipt === undefined
      || receipt.tenant !== tenant
      || receipt.resource_type !== resourceType
      || receipt.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256
      || receipt.artifact.path !== logicalPath(tenant, resourceType)
    ) {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_RECEIPTS",
        "collection parity receipts must match the complete ordered request scope",
        "request",
      );
    }
  }
}

function regionsOverlap(left: string, right: string): boolean {
  const leftToRight = path.relative(left, right);
  const rightToLeft = path.relative(right, left);
  const contains = (relative: string): boolean => relative === ""
    || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
  return contains(leftToRight) || contains(rightToLeft);
}

function readLimits() {
  return {
    maxFiles: TOTAL_FILES,
    maxDirectories: 1,
    maxDirectoryEntries: 1,
    maxDepth: 0,
    maxTotalBytes: MAX_FILE_BYTES * BigInt(TOTAL_FILES),
    maxFileBytes: MAX_FILE_BYTES,
  } as const;
}

function fileVersion(metadata: {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}): FileVersion {
  return {
    dev: metadata.dev,
    ino: metadata.ino,
    size: metadata.size,
    mtimeNs: metadata.mtimeNs,
    ctimeNs: metadata.ctimeNs,
  };
}

function sameFileVersion(left: FileVersion, right: FileVersion): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.size === right.size
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

function identityKey(value: Pick<FileVersion, "dev" | "ino">): string {
  return `${value.dev}:${value.ino}`;
}

async function captureDirectoryVersion(
  directory: BoundDirectory,
): Promise<FileVersion> {
  const [handleStat, pathStat, canonical] = await Promise.all([
    directory.handle.stat({ bigint: true }),
    lstat(directory.path, { bigint: true }),
    realpath(directory.path),
  ]);
  const handleVersion = fileVersion(handleStat);
  const pathVersion = fileVersion(pathStat);
  if (
    canonical !== directory.path
    || !handleStat.isDirectory()
    || !pathStat.isDirectory()
    || pathStat.isSymbolicLink()
    || handleVersion.dev !== directory.dev
    || handleVersion.ino !== directory.ino
    || !sameFileVersion(handleVersion, pathVersion)
  ) {
    throw new Error("directory binding changed");
  }
  return handleVersion;
}

async function bindParityDirectory(
  level: DirectoryLevel,
  directoryPath: string,
): Promise<BoundParityDirectory> {
  let directory: BoundDirectory | undefined;
  try {
    directory = await bindDirectory(directoryPath);
    return {
      level,
      path: directoryPath,
      directory,
      version: await captureDirectoryVersion(directory),
    };
  } catch {
    await directory?.handle.close().catch(() => undefined);
    if (level === "workspace") {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
        "collection parity workspaces must be canonical non-symlink directories",
        "request",
      );
    }
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_READ_FAILED",
      "collection parity input directories could not be read",
      "io",
    );
  }
}

async function bindWorkspace(
  role: BoundWorkspace["role"],
  candidate: string,
  tenant: string,
): Promise<BoundWorkspace> {
  if (
    candidate.includes("\0")
    || !candidate.isWellFormed()
    || !path.isAbsolute(candidate)
    || path.resolve(candidate) !== candidate
    || path.parse(candidate).root === candidate
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
      "collection parity workspaces must be canonical non-root absolute directories",
      "request",
    );
  }
  const directories: BoundParityDirectory[] = [];
  try {
    directories.push(await bindParityDirectory("workspace", candidate));
    directories.push(await bindParityDirectory("pulls", path.join(candidate, "pulls")));
    directories.push(await bindParityDirectory(
      "tenant",
      path.join(candidate, "pulls", tenant),
    ));
    return { role, path: candidate, directories };
  } catch (error: unknown) {
    await Promise.all(directories.map(async (bound) =>
      bound.directory.handle.close().catch(() => undefined)));
    throw error;
  }
}

function allDirectories(
  workspaces: readonly BoundWorkspace[],
): readonly BoundParityDirectory[] {
  return workspaces.flatMap((workspace) => workspace.directories);
}

function assertWorkspacePathIsolation(
  workspaces: readonly (readonly [BoundWorkspace["role"], string])[],
): void {
  for (let left = 0; left < workspaces.length; left += 1) {
    for (let right = left + 1; right < workspaces.length; right += 1) {
      const first = workspaces[left];
      const second = workspaces[right];
      if (
        first === undefined
        || second === undefined
        || regionsOverlap(first[1], second[1])
      ) {
        return fail(
          "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
          "collection parity workspaces must be pairwise disjoint",
          "request",
        );
      }
    }
  }
}

function assertWorkspaceIsolation(workspaces: readonly BoundWorkspace[]): void {
  const directoryIdentities = new Set<string>();
  for (const workspace of workspaces) {
    for (const bound of workspace.directories) {
      const key = identityKey(bound.version);
      if (directoryIdentities.has(key)) {
        return fail(
          "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
          "collection parity directories must be physically distinct",
          "request",
        );
      }
      directoryIdentities.add(key);
    }
  }
  for (let left = 0; left < workspaces.length; left += 1) {
    for (let right = left + 1; right < workspaces.length; right += 1) {
      const first = workspaces[left];
      const second = workspaces[right];
      if (
        first === undefined
        || second === undefined
        || regionsOverlap(first.path, second.path)
      ) {
        return fail(
          "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
          "collection parity workspaces must be pairwise physically disjoint",
          "request",
        );
      }
    }
  }
}

function assertArtifactIsolation(
  workspaces: readonly BoundWorkspace[],
  artifacts: ReadonlyMap<string, BoundArtifact>,
): void {
  const identities = new Set(
    allDirectories(workspaces).map((bound) => identityKey(bound.version)),
  );
  for (const artifact of artifacts.values()) {
    const key = identityKey(artifact.identity);
    if (identities.has(key)) {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
        "collection parity inputs must be physically distinct",
        "request",
      );
    }
    identities.add(key);
  }
}

async function verifyParityDirectory(bound: BoundParityDirectory): Promise<void> {
  try {
    const current = await captureDirectoryVersion(bound.directory);
    if (!sameFileVersion(current, bound.version)) {
      throw new Error("directory version changed");
    }
  } catch {
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
      "collection parity input directory changed during comparison",
      "io",
    );
  }
}

async function verifyAllDirectories(
  workspaces: readonly BoundWorkspace[],
): Promise<void> {
  for (const bound of allDirectories(workspaces)) {
    await verifyParityDirectory(bound);
  }
}

function finalSynchronousCheckpoint(
  workspaces: readonly BoundWorkspace[],
  artifacts: ReadonlyMap<string, BoundArtifact>,
): void {
  try {
    for (const bound of allDirectories(workspaces)) {
      const handleStat = fstatSync(bound.directory.handle.fd, { bigint: true });
      const pathStat = lstatSync(bound.path, { bigint: true });
      const handleVersion = fileVersion(handleStat);
      const pathVersion = fileVersion(pathStat);
      if (
        realpathSync(bound.path) !== bound.path
        || !handleStat.isDirectory()
        || !pathStat.isDirectory()
        || pathStat.isSymbolicLink()
        || !sameFileVersion(handleVersion, bound.version)
        || !sameFileVersion(pathVersion, bound.version)
      ) {
        throw new Error("directory version changed");
      }
    }
    for (const artifact of artifacts.values()) {
      const metadata = lstatSync(artifact.absolutePath, { bigint: true });
      if (
        realpathSync(artifact.absolutePath) !== artifact.absolutePath
        || !metadata.isFile()
        || metadata.isSymbolicLink()
        || !sameFileVersion(fileVersion(metadata), artifact.identity)
      ) {
        throw new Error("artifact version changed");
      }
    }
  } catch {
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
      "collection parity input changed during comparison",
      "io",
    );
  }
}

async function currentFileVersion(
  absolutePath: string,
  code: string,
): Promise<FileVersion> {
  try {
    const metadata = await lstat(absolutePath, { bigint: true });
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(code, "collection parity artifact changed during comparison", "io");
    }
    return fileVersion(metadata);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(code, "collection parity artifact changed during comparison", "io");
  }
}

async function readArtifact(
  workspace: BoundWorkspace,
  tenant: string,
  resourceType: ZccCollectionResourceType,
  budget: ReadBudget,
): Promise<BoundArtifact> {
  const absolutePath = path.join(workspace.path, "pulls", tenant, `${resourceType}.json`);
  if (await realpath(absolutePath).catch(() => "") !== absolutePath) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_ARTIFACT",
      "collection parity artifacts must be canonical non-symlink files",
      "io",
    );
  }
  const before = await currentFileVersion(
    absolutePath,
    "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
  );
  const content = await readBoundedFileBytes(
    absolutePath,
    budget,
    { followSymlinks: false },
  );
  try {
    const after = await currentFileVersion(
      absolutePath,
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
    );
    if (
      !sameFileVersion(before, after)
      || content.identity.dev !== before.dev
      || content.identity.ino !== before.ino
      || content.digest.size !== before.size
      || await realpath(absolutePath).catch(() => "") !== absolutePath
    ) {
      return fail(
        "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
        "collection parity input changed during comparison",
        "io",
      );
    }
    let text: string;
    try {
      text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(content.bytes);
    } catch {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_PARITY_ARTIFACT",
        "collection parity artifact is not valid UTF-8",
      );
    }
    let items: readonly unknown[];
    try {
      items = parseZccPullDataJson(text);
    } catch {
      return fail(
        "INVALID_ZCC_PULL_COLLECTION_PARITY_ARTIFACT",
        "collection parity artifact is not a bounded lossless JSON list",
      );
    }
    if (renderPythonLosslessArtifactJson(items) !== text) {
      return fail(
        "NONCANONICAL_ZCC_PULL_COLLECTION_PARITY_ARTIFACT",
        "collection parity artifact is not exact Python canonical JSON",
      );
    }
    return {
      absolutePath,
      identity: before,
      tuple: {
        sha256: content.digest.sha256,
        size_bytes: Number(content.digest.size),
        item_count: items.length,
      },
    };
  } finally {
    content.bytes.fill(0);
  }
}

async function recheckArtifact(
  original: BoundArtifact,
  budget: ReadBudget,
): Promise<void> {
  if (await realpath(original.absolutePath).catch(() => "") !== original.absolutePath) {
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
      "collection parity input changed during comparison",
      "io",
    );
  }
  const before = await currentFileVersion(
    original.absolutePath,
    "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
  );
  if (!sameFileVersion(before, original.identity)) {
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
      "collection parity input changed during comparison",
      "io",
    );
  }
  const current = await readBoundedFileBytes(
    original.absolutePath,
    budget,
    { followSymlinks: false },
  );
  try {
    const after = await currentFileVersion(
      original.absolutePath,
      "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
    );
    if (
      !sameFileVersion(after, original.identity)
      || current.identity.dev !== original.identity.dev
      || current.identity.ino !== original.identity.ino
      || current.digest.sha256 !== original.tuple.sha256
      || Number(current.digest.size) !== original.tuple.size_bytes
      || await realpath(original.absolutePath).catch(() => "") !== original.absolutePath
    ) {
      return fail(
        "ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED",
        "collection parity input changed during comparison",
        "io",
      );
    }
  } finally {
    current.bytes.fill(0);
  }
}

function tupleEqual(
  left: ZccPullCollectionParityTuple,
  right: ZccPullCollectionParityTuple,
): boolean {
  return left.sha256 === right.sha256
    && left.size_bytes === right.size_bytes
    && left.item_count === right.item_count;
}

function statusFor(
  before: ZccPullCollectionParityTuple,
  node: ZccPullCollectionParityTuple,
  after: ZccPullCollectionParityTuple,
): ZccPullCollectionParityStatus {
  if (!tupleEqual(before, after)) return "unstable_reference";
  return tupleEqual(before, node) ? "equal" : "different";
}

function closeWorkspaces(workspaces: readonly BoundWorkspace[]): Promise<unknown[]> {
  return Promise.all(allDirectories(workspaces).map(async (bound) =>
    bound.directory.handle.close().catch(() => undefined)));
}

/** Compare exact-five Node pull bytes against a stable Python-before/after window. */
export async function compareZccPullCollectionOperation(
  options: CompareZccPullCollectionOptions,
): Promise<ZccPullCollectionParity> {
  const outer = dataRecord(options);
  exactKeys(outer, ["context", "reference", "tenant", "receipts"], ["hooks"]);
  const rawContext = dataRecord(dataValue(outer, "context"));
  exactKeys(rawContext, [
    "node_workspace",
    "python_before_workspace",
    "python_after_workspace",
  ]);
  dataString(dataValue(rawContext, "node_workspace"));
  dataString(dataValue(rawContext, "python_before_workspace"));
  dataString(dataValue(rawContext, "python_after_workspace"));
  const contextSnapshot = snapshotPlainJsonGraph(rawContext, {
    maxDepth: 3,
  });
  if (!contextSnapshot.ok) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity context must be inert data",
      "request",
    );
  }
  const context = dataRecord(contextSnapshot.value);
  const reference = dataString(dataValue(outer, "reference"));
  const tenant = dataString(dataValue(outer, "tenant"));
  const receipts = snapshotReceipts(dataValue(outer, "receipts"));
  const hooksDescriptor = Object.getOwnPropertyDescriptor(outer, "hooks");
  let afterInputsBound: (() => void | Promise<void>) | undefined;
  let afterArtifactRechecked: (
    (index: number) => void | Promise<void>
  ) | undefined;
  if (hooksDescriptor !== undefined) {
    const hooks = dataRecord(hooksDescriptor.value);
    exactKeys(hooks, [], ["afterInputsBound", "afterArtifactRechecked"]);
    const hookDescriptor = Object.getOwnPropertyDescriptor(hooks, "afterInputsBound");
    if (hookDescriptor !== undefined) {
      if (!("value" in hookDescriptor) || typeof hookDescriptor.value !== "function") {
        return fail(
          "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
          "collection parity test hook is invalid",
          "request",
        );
      }
      afterInputsBound = hookDescriptor.value as () => void | Promise<void>;
    }
    const recheckHookDescriptor = Object.getOwnPropertyDescriptor(
      hooks,
      "afterArtifactRechecked",
    );
    if (recheckHookDescriptor !== undefined) {
      if (
        !("value" in recheckHookDescriptor)
        || typeof recheckHookDescriptor.value !== "function"
      ) {
        return fail(
          "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
          "collection parity test hook is invalid",
          "request",
        );
      }
      afterArtifactRechecked = recheckHookDescriptor.value as (
        index: number,
      ) => void | Promise<void>;
    }
  }
  if (
    reference !== "python_stability_window"
    || tenant.length > 255
    || !TENANT_PATTERN.test(tenant)
  ) {
    return fail(
      "INVALID_ZCC_PULL_COLLECTION_PARITY_INPUT",
      "collection parity request scope is invalid",
      "request",
    );
  }
  validateReceiptSet(tenant, receipts);

  const workspaceValues = [
    ["before", dataString(dataValue(context, "python_before_workspace"))],
    ["node", dataString(dataValue(context, "node_workspace"))],
    ["after", dataString(dataValue(context, "python_after_workspace"))],
  ] as const;
  assertWorkspacePathIsolation(workspaceValues);
  const workspaces: BoundWorkspace[] = [];
  try {
    for (const [role, candidate] of workspaceValues) {
      workspaces.push(await bindWorkspace(role, candidate, tenant));
    }
    assertWorkspaceIsolation(workspaces);
    await verifyAllDirectories(workspaces);
    const budget = new ReadBudget(readLimits());
    const artifacts = new Map<string, BoundArtifact>();
    for (const workspace of workspaces) {
      for (const resourceType of ZCC_COLLECTION_RESOURCE_TYPES) {
        const artifact = await readArtifact(workspace, tenant, resourceType, budget);
        artifacts.set(`${workspace.role}:${resourceType}`, artifact);
      }
    }
    assertArtifactIsolation(workspaces, artifacts);

    for (const [index, resourceType] of ZCC_COLLECTION_RESOURCE_TYPES.entries()) {
      const node = artifacts.get(`node:${resourceType}`);
      const receipt = receipts[index];
      if (
        node === undefined
        || receipt === undefined
        || receipt.artifact.sha256 !== node.tuple.sha256
        || receipt.artifact.size_bytes !== node.tuple.size_bytes
        || receipt.artifact.item_count !== node.tuple.item_count
      ) {
        return fail(
          "ZCC_PULL_COLLECTION_RECEIPT_MISMATCH",
          "collection receipt does not bind the compared Node artifact",
        );
      }
    }

    await afterInputsBound?.();
    await verifyAllDirectories(workspaces);
    const recheckBudget = new ReadBudget(readLimits());
    let artifactIndex = 0;
    for (const artifact of artifacts.values()) {
      await recheckArtifact(artifact, recheckBudget);
      await afterArtifactRechecked?.(artifactIndex);
      artifactIndex += 1;
    }
    await verifyAllDirectories(workspaces);
    finalSynchronousCheckpoint(workspaces, artifacts);

    const resources = ZCC_COLLECTION_RESOURCE_TYPES.map((resourceType) => {
      const before = artifacts.get(`before:${resourceType}`)?.tuple;
      const node = artifacts.get(`node:${resourceType}`)?.tuple;
      const after = artifacts.get(`after:${resourceType}`)?.tuple;
      if (before === undefined || node === undefined || after === undefined) {
        return fail(
          "INTERNAL_ERROR",
          "collection parity artifact set is incomplete",
          "internal",
        );
      }
      return {
        resource_type: resourceType,
        path: logicalPath(tenant, resourceType),
        before,
        node,
        after,
        status: statusFor(before, node, after),
      } as const;
    });
    for (const resource of resources) {
      Object.freeze(resource.before);
      Object.freeze(resource.node);
      Object.freeze(resource.after);
      Object.freeze(resource);
    }
    Object.freeze(resources);
    const counts = Object.freeze({
      total: 5 as const,
      equal: resources.filter((resource) => resource.status === "equal").length,
      different: resources.filter((resource) => resource.status === "different").length,
      unstable_reference: resources.filter(
        (resource) => resource.status === "unstable_reference",
      ).length,
    });
    const status: ZccPullCollectionParityStatus = counts.unstable_reference > 0
      ? "unstable_reference"
      : counts.different > 0
        ? "different"
        : "equal";
    const result: ZccPullCollectionParity = Object.freeze({
      kind: "infrawright.zcc_pull_collection_parity",
      schema_version: 1,
      reference: "python_stability_window",
      product: "zcc",
      catalog_sources_sha256: ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
      tenant,
      status,
      counts,
      resources,
    });
    if (!validateZccPullCollectionParity(result)) {
      return fail(
        "INVALID_OPERATION_RESULT",
        "collection parity result failed its versioned contract",
        "internal",
      );
    }
    return result;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail(
      "ZCC_PULL_COLLECTION_PARITY_READ_FAILED",
      "collection parity inputs could not be read",
      "io",
    );
  } finally {
    await closeWorkspaces(workspaces);
  }
}
