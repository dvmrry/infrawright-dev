import {
  lstatSync,
  realpathSync,
  statSync,
  type BigIntStats,
} from "node:fs";
import { lstat, realpath } from "node:fs/promises";
import path from "node:path";
import { types as utilTypes } from "node:util";

import { validateZccAdoptionArtifactMaterialization } from "../contracts/validators.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
  type BoundedReadLimits,
} from "../io/bounded-files.js";
import {
  pythonCompatibleJsonByteLength,
  sortedStrings,
  type JsonValue,
} from "../json/python-compatible.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import { loadBoundAssessmentRootCatalog } from "./catalog.js";
import {
  copyAssessmentControlFiles,
  recheckAssessmentControlFiles,
  type BoundAssessmentControlFile,
} from "./control-evidence.js";
import { loadBoundAssessmentDeployment } from "./deployment.js";
import { ProcessFailure } from "./errors.js";
import { parseGeneratedImports } from "./import-moves.js";
import { PLAN_FINGERPRINT_VERSION, treeFingerprints } from "./plan-fingerprint.js";
import { pythonPosixJoin } from "./paths.js";
import { rootTopology, validateTenant } from "./roots.js";
import type {
  FileFingerprint,
} from "./plan-fingerprint.js";
import type {
  RootTopology,
  RootTopologyRoot,
  WholeRootDiagnostic,
} from "./types.js";
import type { ZccAdoptionArtifactMaterialization } from "./zcc-adoption-materialization.js";
import type { ZccApplicableAdoptionArtifactParity } from "./zcc-adoption-artifact-parity.js";
import { resolveZccArtifactTarget } from "./zcc-pull-operation.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";
import { requireSupportedZscalerCompileCatalog } from "./zscaler-assessment.js";
import {
  renderZccPlanRootMain,
  zccPlanRootAbsentSidecarPaths,
  zccPlanRootMaterializationSha256,
  zccPlanRootModuleSource,
  zccPlanRootSha256,
  zccPlanRootTreeSha256,
  MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
  MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES,
  MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES,
  ZCC_PLAN_ROOT_PREPARATION_PROFILE,
  ZCC_PLAN_ROOT_RENDERER_PROFILE,
  ZCC_PLAN_ROOT_RESOURCE_TYPES,
} from "./zcc-plan-root-preparation-contract.js";
export {
  MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
  MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES,
  MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES,
  renderZccPlanRootMain,
  zccPlanRootAbsentSidecarPaths,
  ZCC_PLAN_ROOT_PREPARATION_PROFILE,
  ZCC_PLAN_ROOT_RENDERER_PROFILE,
  ZCC_PLAN_ROOT_RESOURCE_TYPES,
} from "./zcc-plan-root-preparation-contract.js";

const RESOURCE_TYPES = new Set<string>(ZCC_PLAN_ROOT_RESOURCE_TYPES);
const MATERIALIZATION_SNAPSHOT_LIMITS = Object.freeze({
  maxDepth: 16,
  maxNodes: 512,
  maxProperties: 512,
  maxStringBytes: 1024 * 1024,
});
const ARTIFACT_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 32,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 512n * 1024n * 1024n,
  maxFileBytes: 32n * 1024n * 1024n,
};
const MODULE_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 50_000,
  maxDirectories: 10_000,
  maxDirectoryEntries: 100_000,
  maxDepth: 128,
  maxTotalBytes: 512n * 1024n * 1024n,
  maxFileBytes: 16n * 1024n * 1024n,
};

export interface ZccPlanRootTextArtifact {
  readonly path: string;
  readonly media_type: "text/x-hcl" | "text/plain";
  readonly encoding: "utf-8";
  readonly sha256: string;
  readonly size_bytes: number;
  readonly content: string;
}

export interface ZccPlanRootModuleBinding {
  readonly resource_type: ZccPullResourceType;
  readonly logical_path: string;
  readonly source: string;
  readonly fingerprint_version: typeof PLAN_FINGERPRINT_VERSION;
  readonly semantics: "python_fingerprint_v2_observed";
  readonly module_provenance: "observed_unqualified";
  readonly tree_sha256: string;
  readonly file_count: number;
  readonly files: readonly {
    readonly path: string;
    readonly sha256: string;
  }[];
}

export interface ZccPlanRootSourceBinding {
  readonly resource_type: ZccPullResourceType;
  readonly materialization_sha256: string;
  readonly provider_observed_source: {
    readonly path: string;
    readonly sha256: string;
    readonly size_bytes: number;
  };
  readonly adoption_catalog: {
    readonly kind: "infrawright.adoption_catalog";
    readonly schema_version: 1;
    readonly sha256: string;
    readonly sources_sha256: string;
  };
  readonly materialized_artifacts: {
    readonly tfvars: ZccPlanRootDigest;
    readonly imports: ZccPlanRootDigest;
    readonly lookup: ZccPlanRootDigest | null;
  };
}

export interface ZccPlanRootDigest {
  readonly path: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccPlanRootPreparationCandidate {
  readonly kind: "infrawright.zcc_plan_root_preparation_candidate";
  readonly schema_version: 1;
  readonly profile: typeof ZCC_PLAN_ROOT_PREPARATION_PROFILE;
  readonly mode: "bootstrap";
  readonly product: "zcc";
  readonly tenant: string;
  readonly selector: { readonly resource_type: ZccPullResourceType };
  readonly backend: "local" | "azurerm";
  readonly status: "candidate_only";
  readonly qualification: {
    readonly readiness: "not_qualified";
    readonly validation: "not_performed";
    readonly plan: "not_performed";
    readonly apply: "not_performed";
    readonly refresh: "not_performed";
    readonly publication: "not_performed";
    readonly cutover: "not_qualified";
  };
  readonly renderer: {
    readonly profile: typeof ZCC_PLAN_ROOT_RENDERER_PROFILE;
    readonly python_source: "engine.gen_env";
    readonly reference_terraform_version: "1.15.4";
    readonly terraform_executed: false;
  };
  readonly controls: {
    readonly deployment: ZccPlanRootDigest;
    readonly root_catalog: ZccPlanRootDigest;
  };
  readonly topology: RootTopology;
  readonly backend_marker: {
    readonly path: string;
    readonly observed_state: "absent" | "exact";
    readonly desired: ZccPlanRootTextArtifact | null;
  };
  readonly absent_sidecars: readonly string[];
  readonly sources: readonly ZccPlanRootSourceBinding[];
  readonly modules: readonly ZccPlanRootModuleBinding[];
  readonly root: {
    readonly label: string;
    readonly provider: "zcc";
    readonly members: readonly ZccPullResourceType[];
    readonly env_dir: string;
    readonly artifacts: {
      readonly main_tf: ZccPlanRootTextArtifact;
      readonly staged_imports: readonly ZccPlanRootTextArtifact[];
    };
  };
  readonly summary: {
    readonly selected: 1;
    readonly roots: 1;
    readonly members: number;
    readonly modules: number;
    readonly staged_imports: number;
  };
}

export interface CompileZccPlanRootPreparationOptions {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly profile: typeof ZCC_PLAN_ROOT_PREPARATION_PROFILE;
  readonly mode: "bootstrap";
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly backend: "local" | "azurerm";
  readonly materializations: readonly ZccAdoptionArtifactMaterialization[];
  readonly hooks?: {
    readonly afterInputsBound?: () => void | Promise<void>;
    readonly beforeFinalRecheck?: () => void | Promise<void>;
    readonly afterAsyncRechecks?: () => void | Promise<void>;
  };
}

interface PathVersion {
  readonly dev: bigint;
  readonly ino: bigint;
  readonly size: bigint;
  readonly mtimeNs: bigint;
  readonly ctimeNs: bigint;
}

interface WorkspaceBinding {
  readonly path: string;
  readonly version: PathVersion;
}

interface BoundArtifact {
  readonly logicalPath: string;
  readonly absolutePath: string;
  readonly digest: ZccPlanRootDigest;
  readonly version: PathVersion;
  readonly authority: BoundAbsent["ancestor"];
  readonly text: string;
}

interface BoundModule {
  readonly binding: ZccPlanRootModuleBinding;
  readonly absolutePath: string;
  readonly version: PathVersion;
  readonly authority: BoundAbsent["ancestor"];
  readonly fingerprints: readonly FileFingerprint[];
  readonly fileVersions: readonly {
    readonly path: string;
    readonly version: PathVersion;
  }[];
}

interface BoundAbsent {
  readonly path: string;
  readonly ancestor: {
    readonly path: string;
    readonly version: PathVersion;
  };
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

function version(metadata: BigIntStats): PathVersion {
  return {
    dev: metadata.dev,
    ino: metadata.ino,
    size: metadata.size,
    mtimeNs: metadata.mtimeNs,
    ctimeNs: metadata.ctimeNs,
  };
}

function sameVersion(left: PathVersion, right: PathVersion): boolean {
  return left.dev === right.dev
    && left.ino === right.ino
    && left.size === right.size
    && left.mtimeNs === right.mtimeNs
    && left.ctimeNs === right.ctimeNs;
}

function syncVersion(filePath: string, followSymlinks = false): PathVersion {
  const metadata = followSymlinks
    ? statSync(filePath, { bigint: true })
    : lstatSync(filePath, { bigint: true });
  return version(metadata);
}

function invalidOptions(): never {
  return fail(
    "INVALID_PLAN_ROOT_INPUT",
    "plan-root preparation inputs must be exact inert values",
  );
}

function inertRecord(
  value: unknown,
  allowed: ReadonlySet<string>,
): Readonly<Record<string, PropertyDescriptor>> {
  if (
    typeof value !== "object"
    || value === null
    || utilTypes.isProxy(value)
  ) {
    return invalidOptions();
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return invalidOptions();
  }
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string" || !allowed.has(key))) {
    return invalidOptions();
  }
  const descriptors: Record<string, PropertyDescriptor> = Object.create(null) as Record<
    string,
    PropertyDescriptor
  >;
  for (const key of keys as string[]) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
    ) {
      return invalidOptions();
    }
    descriptors[key] = descriptor;
  }
  return descriptors;
}

function required(
  descriptors: Readonly<Record<string, PropertyDescriptor>>,
  key: string,
): unknown {
  const descriptor = descriptors[key];
  return descriptor === undefined ? invalidOptions() : descriptor.value;
}

function snapshotCompileOptions(
  value: CompileZccPlanRootPreparationOptions,
): CompileZccPlanRootPreparationOptions {
  const descriptors = inertRecord(value, new Set([
    "workspace",
    "deploymentPath",
    "catalogPath",
    "profile",
    "mode",
    "tenant",
    "resourceType",
    "backend",
    "materializations",
    "hooks",
  ]));
  const rawMaterializations = required(descriptors, "materializations");
  if (
    (
      (typeof rawMaterializations === "object" && rawMaterializations !== null)
      || typeof rawMaterializations === "function"
    )
    && utilTypes.isProxy(rawMaterializations)
  ) {
    return invalidOptions();
  }
  if (
    !Array.isArray(rawMaterializations)
    || rawMaterializations.length < 1
    || rawMaterializations.length > 5
  ) {
    return invalidOptions();
  }
  const materializations = snapshotPlainJsonGraph(
    rawMaterializations,
    MATERIALIZATION_SNAPSHOT_LIMITS,
  );
  if (!materializations.ok || !Array.isArray(materializations.value)) {
    return invalidOptions();
  }
  const stringsByName = new Map<string, string>();
  for (const [name, maxLength] of [
    ["workspace", 4096],
    ["deploymentPath", 4096],
    ["catalogPath", 4096],
    ["profile", 256],
    ["mode", 64],
    ["tenant", 255],
    ["resourceType", 256],
    ["backend", 64],
  ] as const) {
    const candidate = required(descriptors, name);
    if (
      typeof candidate !== "string"
      || candidate.length === 0
      || candidate.length > maxLength
      || candidate.includes("\0")
      || !candidate.isWellFormed()
    ) {
      return invalidOptions();
    }
    stringsByName.set(name, candidate);
  }
  const hooksValue = descriptors.hooks?.value;
  let hooks: CompileZccPlanRootPreparationOptions["hooks"];
  if (hooksValue !== undefined) {
    const hookDescriptors = inertRecord(hooksValue, new Set([
      "afterInputsBound",
      "beforeFinalRecheck",
      "afterAsyncRechecks",
    ]));
    const captured: {
      afterInputsBound?: () => void | Promise<void>;
      beforeFinalRecheck?: () => void | Promise<void>;
      afterAsyncRechecks?: () => void | Promise<void>;
    } = {};
    for (const name of [
      "afterInputsBound",
      "beforeFinalRecheck",
      "afterAsyncRechecks",
    ] as const) {
      const hook = hookDescriptors[name]?.value;
      if (hook !== undefined) {
        if (typeof hook !== "function") {
          return invalidOptions();
        }
        captured[name] = hook as () => void | Promise<void>;
      }
    }
    hooks = Object.freeze(captured);
  }
  const output = {
    workspace: stringsByName.get("workspace"),
    deploymentPath: stringsByName.get("deploymentPath"),
    catalogPath: stringsByName.get("catalogPath"),
    profile: stringsByName.get("profile"),
    mode: stringsByName.get("mode"),
    tenant: stringsByName.get("tenant"),
    resourceType: stringsByName.get("resourceType"),
    backend: stringsByName.get("backend"),
    materializations: materializations.value,
    ...(hooks === undefined ? {} : { hooks }),
  };
  return Object.freeze(output) as CompileZccPlanRootPreparationOptions;
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

export function zccPlanRootTextArtifact(
  logicalPath: string,
  mediaType: ZccPlanRootTextArtifact["media_type"],
  content: string,
): ZccPlanRootTextArtifact {
  return Object.freeze({
    path: logicalPath,
    media_type: mediaType,
    encoding: "utf-8" as const,
    sha256: zccPlanRootSha256(content),
    size_bytes: Buffer.byteLength(content, "utf8"),
    content,
  });
}

function contained(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative);
}

async function bindWorkspace(workspace: string): Promise<WorkspaceBinding> {
  if (!path.isAbsolute(workspace) || path.resolve(workspace) !== workspace) {
    return fail("INVALID_PLAN_ROOT_WORKSPACE", "workspace must be canonical and absolute");
  }
  try {
    const metadata = await lstat(workspace, { bigint: true });
    if (
      !metadata.isDirectory()
      || metadata.isSymbolicLink()
      || await realpath(workspace) !== workspace
    ) {
      throw new Error("not canonical");
    }
    return { path: workspace, version: version(metadata) };
  } catch {
    return fail(
      "INVALID_PLAN_ROOT_WORKSPACE",
      "workspace must be a canonical regular directory",
      "io",
    );
  }
}

async function recheckWorkspace(binding: WorkspaceBinding): Promise<void> {
  try {
    const metadata = await lstat(binding.path, { bigint: true });
    if (
      !metadata.isDirectory()
      || metadata.isSymbolicLink()
      || !sameVersion(version(metadata), binding.version)
      || await realpath(binding.path) !== binding.path
    ) {
      throw new Error("changed");
    }
  } catch {
    return fail(
      "PLAN_ROOT_INPUT_CHANGED",
      "workspace changed during plan-root preparation",
      "io",
    );
  }
}

function logicalFromAbsolute(workspace: string, absolutePath: string): string {
  return path.relative(workspace, absolutePath).split(path.sep).join("/") || ".";
}

function resolveLogical(workspace: string, logicalPath: string): string {
  if (
    typeof logicalPath !== "string"
    || logicalPath.length === 0
    || logicalPath.includes("\0")
    || !logicalPath.isWellFormed()
    || path.posix.isAbsolute(logicalPath)
    || path.posix.normalize(logicalPath) !== logicalPath
    || logicalPath === ".."
    || logicalPath.startsWith("../")
  ) {
    return fail("INVALID_PLAN_ROOT_LOGICAL_PATH", "plan-root input path is not canonical");
  }
  const absolute = path.resolve(workspace, logicalPath);
  if (!contained(absolute, workspace)) {
    return fail("PLAN_ROOT_PATH_OUTSIDE_WORKSPACE", "plan-root input is outside workspace");
  }
  return absolute;
}

async function requireCanonicalControl(
  workspace: string,
  candidate: string,
): Promise<string> {
  const absolute = path.isAbsolute(candidate) ? candidate : path.resolve(workspace, candidate);
  if (!contained(absolute, workspace) || path.resolve(absolute) !== absolute) {
    return fail("INVALID_PLAN_ROOT_CONTROL", "control input must be workspace-contained");
  }
  try {
    const metadata = await lstat(absolute);
    if (
      !metadata.isFile()
      || metadata.isSymbolicLink()
      || await realpath(absolute) !== absolute
    ) {
      throw new Error("not canonical");
    }
    return absolute;
  } catch {
    return fail(
      "INVALID_PLAN_ROOT_CONTROL",
      "control input must be a canonical regular file",
      "io",
    );
  }
}

function controlDigest(
  workspace: string,
  file: BoundAssessmentControlFile,
): ZccPlanRootDigest {
  if (file.digest === null) {
    return fail("INVALID_PLAN_ROOT_CONTROL", "required control input is absent", "io");
  }
  const size = Number(file.digest.size);
  if (!Number.isSafeInteger(size) || size < 0) {
    return fail("INVALID_PLAN_ROOT_CONTROL", "control input size is invalid", "internal");
  }
  return {
    path: logicalFromAbsolute(workspace, file.path),
    sha256: file.digest.sha256,
    size_bytes: size,
  };
}

function bindControlVersions(
  controls: readonly BoundAssessmentControlFile[],
): readonly { readonly path: string; readonly version: PathVersion }[] {
  return controls.map((control) => {
    try {
      const metadata = lstatSync(control.path, { bigint: true });
      if (
        !metadata.isFile()
        || metadata.isSymbolicLink()
        || realpathSync(control.path) !== control.path
        || control.identity === undefined
        || control.identity === null
        || control.identity.dev !== metadata.dev
        || control.identity.ino !== metadata.ino
      ) {
        throw new Error("not bound");
      }
      return { path: control.path, version: version(metadata) };
    } catch {
      return fail(
        "PLAN_ROOT_CONTROL_CHANGED",
        "control input changed while it was bound",
        "io",
      );
    }
  });
}

function expectedDigest(
  entry: ZccApplicableAdoptionArtifactParity,
): ZccPlanRootDigest {
  if (
    entry.status !== "equal"
    || entry.reference.sha256 === null
    || entry.reference.size_bytes === null
  ) {
    return fail(
      "PLAN_ROOT_MATERIALIZATION_NOT_READY",
      "plan-root preparation requires exact ready materialized artifacts",
    );
  }
  return {
    path: entry.reference.path,
    sha256: entry.reference.sha256,
    size_bytes: entry.reference.size_bytes,
  };
}

async function bindArtifact(
  workspace: string,
  expected: ZccPlanRootDigest,
  budget: ReadBudget,
): Promise<BoundArtifact> {
  const absolutePath = resolveLogical(workspace, expected.path);
  try {
    const before = await lstat(absolutePath, { bigint: true });
    if (
      !before.isFile()
      || before.isSymbolicLink()
      || await realpath(absolutePath) !== absolutePath
    ) {
      throw new Error("not canonical");
    }
    const content = await readBoundedUtf8File(absolutePath, budget, {
      followSymlinks: false,
    });
    const after = await lstat(absolutePath, { bigint: true });
    if (
      content.identity.dev !== before.dev
      || content.identity.ino !== before.ino
      || !sameVersion(version(after), version(before))
      || content.digest.sha256 !== expected.sha256
      || content.digest.size !== BigInt(expected.size_bytes)
    ) {
      return fail(
        "PLAN_ROOT_MATERIALIZATION_MISMATCH",
        "materialized artifact does not match its complete receipt",
        "io",
      );
    }
    return {
      logicalPath: expected.path,
      absolutePath,
      digest: expected,
      version: version(before),
      authority: nearestExistingAncestor(workspace, absolutePath),
      text: content.text,
    };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "PLAN_ROOT_MATERIALIZATION_READ_FAILED",
      "unable to bind a canonical materialized artifact",
      "io",
    );
  }
}

async function recheckArtifact(artifact: BoundArtifact, budget: ReadBudget): Promise<void> {
  try {
    const before = await lstat(artifact.absolutePath, { bigint: true });
    const digest = await sha256StableFile(artifact.absolutePath, budget, {
      followSymlinks: false,
    });
    const after = await lstat(artifact.absolutePath, { bigint: true });
    const authority = await lstat(artifact.authority.path, { bigint: true });
    if (
      !before.isFile()
      || before.isSymbolicLink()
      || !sameVersion(version(before), artifact.version)
      || !sameVersion(version(after), artifact.version)
      || !authority.isDirectory()
      || authority.isSymbolicLink()
      || !sameVersion(version(authority), artifact.authority.version)
      || await realpath(artifact.authority.path) !== artifact.authority.path
      || await realpath(artifact.absolutePath) !== artifact.absolutePath
      || digest.sha256 !== artifact.digest.sha256
      || digest.size !== BigInt(artifact.digest.size_bytes)
    ) {
      throw new Error("changed");
    }
  } catch {
    return fail(
      "PLAN_ROOT_INPUT_CHANGED",
      "materialized artifact changed during plan-root preparation",
      "io",
    );
  }
}

function nearestExistingAncestor(
  workspace: string,
  absolutePath: string,
): BoundAbsent["ancestor"] {
  let current = path.dirname(absolutePath);
  while (contained(current, workspace)) {
    try {
      const metadata = lstatSync(current, { bigint: true });
      if (
        !metadata.isDirectory()
        || metadata.isSymbolicLink()
        || realpathSync(current) !== current
      ) {
        throw new Error("not canonical");
      }
      return { path: current, version: version(metadata) };
    } catch (error: unknown) {
      if (errorCode(error) !== "ENOENT") {
        return fail(
          "PLAN_ROOT_ABSENCE_CHECK_FAILED",
          "forbidden sidecar ancestry is not canonical",
          "io",
        );
      }
    }
    const parent = path.dirname(current);
    if (parent === current) {
      break;
    }
    current = parent;
  }
  return fail(
    "PLAN_ROOT_ABSENCE_CHECK_FAILED",
    "forbidden sidecar has no workspace-contained authority",
    "io",
  );
}

async function assertAbsent(
  workspace: string,
  logicalPath: string,
  policy: {
    readonly checkCode: string;
    readonly checkMessage: string;
    readonly presentCode: string;
    readonly presentMessage: string;
  } = {
    checkCode: "PLAN_ROOT_ABSENCE_CHECK_FAILED",
    checkMessage: "unable to inspect forbidden sidecar",
    presentCode: "UNSUPPORTED_PLAN_ROOT_SIDECAR",
    presentMessage: "plan-root preparation requires expression, HCL, and move sidecars to be absent",
  },
): Promise<BoundAbsent> {
  const absolute = resolveLogical(workspace, logicalPath);
  try {
    await lstat(absolute);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return {
        path: absolute,
        ancestor: nearestExistingAncestor(workspace, absolute),
      };
    }
    return fail(policy.checkCode, policy.checkMessage, "io");
  }
  return fail(policy.presentCode, policy.presentMessage);
}

async function recheckAbsent(paths: readonly BoundAbsent[]): Promise<void> {
  for (const candidate of paths) {
    try {
      await lstat(candidate.path);
    } catch (error: unknown) {
      if (errorCode(error) === "ENOENT") {
        try {
          const ancestor = await lstat(candidate.ancestor.path, { bigint: true });
          if (
            !ancestor.isDirectory()
            || ancestor.isSymbolicLink()
            || !sameVersion(version(ancestor), candidate.ancestor.version)
            || await realpath(candidate.ancestor.path) !== candidate.ancestor.path
          ) {
            throw new Error("changed");
          }
          continue;
        } catch {
          return fail(
            "PLAN_ROOT_INPUT_CHANGED",
            "forbidden sidecar authority changed during plan-root preparation",
            "io",
          );
        }
      }
    }
    return fail(
      "PLAN_ROOT_INPUT_CHANGED",
      "a forbidden sidecar changed during plan-root preparation",
      "io",
    );
  }
}

function moduleDir(deployment: {
  readonly overlay: unknown;
  readonly module_dir?: unknown;
}): string {
  if (typeof deployment.overlay !== "string") {
    return fail("INVALID_PLAN_ROOT_DEPLOYMENT", "deployment overlay must be a string");
  }
  if (deployment.module_dir !== undefined) {
    if (
      typeof deployment.module_dir !== "string"
      || deployment.module_dir.length === 0
      || deployment.module_dir.includes("\0")
      || !deployment.module_dir.isWellFormed()
    ) {
      return fail("INVALID_PLAN_ROOT_MODULE_DIR", "deployment module_dir must be a path string");
    }
    return deployment.module_dir;
  }
  return deployment.overlay === "."
    ? "modules"
    : pythonPosixJoin(deployment.overlay, "modules/default");
}

function moduleFileVersions(
  root: string,
  fingerprints: readonly FileFingerprint[],
): readonly { readonly path: string; readonly version: PathVersion }[] {
  return fingerprints.map(([relativePath]) => {
    const filePath = path.resolve(root, relativePath);
    const metadata = statSync(filePath, { bigint: true });
    if (!metadata.isFile()) {
      return fail(
        "PLAN_ROOT_MODULE_READ_FAILED",
        "observed module fingerprint is no longer a regular file",
        "io",
      );
    }
    return { path: filePath, version: version(metadata) };
  });
}

function sameVersionSet(
  left: readonly { readonly path: string; readonly version: PathVersion }[],
  right: readonly { readonly path: string; readonly version: PathVersion }[],
): boolean {
  return left.length === right.length
    && left.every((entry, index) => {
      const candidate = right[index];
      return candidate !== undefined
        && entry.path === candidate.path
        && sameVersion(entry.version, candidate.version);
    });
}

async function bindModule(options: {
  readonly workspace: string;
  readonly moduleBase: string;
  readonly envDir: string;
  readonly resourceType: ZccPullResourceType;
  readonly budget: ReadBudget;
  readonly verificationBudget: ReadBudget;
}): Promise<BoundModule> {
  const base = path.isAbsolute(options.moduleBase)
    ? path.resolve(options.moduleBase)
    : path.resolve(options.workspace, options.moduleBase);
  const absolutePath = path.resolve(base, options.resourceType);
  if (!contained(absolutePath, options.workspace)) {
    return fail(
      "PLAN_ROOT_MODULE_OUTSIDE_WORKSPACE",
      "derived module root must be workspace-contained",
    );
  }
  try {
    const metadata = await lstat(absolutePath, { bigint: true });
    if (
      !metadata.isDirectory()
      || metadata.isSymbolicLink()
      || await realpath(absolutePath) !== absolutePath
    ) {
      throw new Error("not canonical");
    }
    const fingerprints = await treeFingerprints(absolutePath, options.budget);
    if (fingerprints.length === 0) {
      return fail(
        "PLAN_ROOT_MODULE_EMPTY",
        "derived module root must contain observed fingerprint files",
      );
    }
    const fileVersions = moduleFileVersions(absolutePath, fingerprints);
    const verificationFingerprints = await treeFingerprints(
      absolutePath,
      options.verificationBudget,
    );
    const verifiedVersions = moduleFileVersions(absolutePath, verificationFingerprints);
    const verifiedRoot = await lstat(absolutePath, { bigint: true });
    if (
      JSON.stringify(verificationFingerprints) !== JSON.stringify(fingerprints)
      || !sameVersionSet(fileVersions, verifiedVersions)
      || !sameVersion(version(metadata), version(verifiedRoot))
    ) {
      return fail(
        "PLAN_ROOT_INPUT_CHANGED",
        "module observation changed while it was bound",
        "io",
      );
    }
    const files = fingerprints.map(([filePath, digest]) => ({
      path: filePath,
      sha256: digest,
    }));
    const logicalPath = logicalFromAbsolute(options.workspace, absolutePath);
    const binding: ZccPlanRootModuleBinding = {
      resource_type: options.resourceType,
      logical_path: logicalPath,
      source: zccPlanRootModuleSource(options.envDir, logicalPath),
      fingerprint_version: PLAN_FINGERPRINT_VERSION,
      semantics: "python_fingerprint_v2_observed",
      module_provenance: "observed_unqualified",
      tree_sha256: zccPlanRootTreeSha256(files),
      file_count: files.length,
      files,
    };
    return {
      binding,
      absolutePath,
      version: version(metadata),
      authority: nearestExistingAncestor(options.workspace, absolutePath),
      fingerprints,
      fileVersions: verifiedVersions,
    };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "PLAN_ROOT_MODULE_READ_FAILED",
      "unable to bind the derived module root",
      "io",
    );
  }
}

async function recheckModule(
  module: BoundModule,
  budget: ReadBudget,
): Promise<readonly { readonly path: string; readonly version: PathVersion }[]> {
  try {
    const beforeRoot = await lstat(module.absolutePath, { bigint: true });
    const beforeFiles = moduleFileVersions(module.absolutePath, module.fingerprints);
    const fingerprints = await treeFingerprints(module.absolutePath, budget);
    const afterFiles = moduleFileVersions(module.absolutePath, fingerprints);
    const afterRoot = await lstat(module.absolutePath, { bigint: true });
    const authority = await lstat(module.authority.path, { bigint: true });
    if (
      !beforeRoot.isDirectory()
      || beforeRoot.isSymbolicLink()
      || !sameVersion(version(beforeRoot), module.version)
      || !sameVersion(version(afterRoot), module.version)
      || !sameVersionSet(beforeFiles, module.fileVersions)
      || !sameVersionSet(afterFiles, module.fileVersions)
      || !authority.isDirectory()
      || authority.isSymbolicLink()
      || !sameVersion(version(authority), module.authority.version)
      || await realpath(module.authority.path) !== module.authority.path
      || await realpath(module.absolutePath) !== module.absolutePath
      || JSON.stringify(fingerprints) !== JSON.stringify(module.fingerprints)
    ) {
      throw new Error("changed");
    }
    return afterFiles;
  } catch {
    return fail(
      "PLAN_ROOT_INPUT_CHANGED",
      "module observation changed during plan-root preparation",
      "io",
    );
  }
}

function finalCoherentCas(options: {
  readonly workspace: WorkspaceBinding;
  readonly controls: readonly { readonly path: string; readonly version: PathVersion }[];
  readonly artifacts: readonly BoundArtifact[];
  readonly absences: readonly BoundAbsent[];
  readonly modules: readonly BoundModule[];
  readonly moduleFileVersions: readonly (readonly {
    readonly path: string;
    readonly version: PathVersion;
  }[])[];
}): void {
  try {
    const workspace = lstatSync(options.workspace.path, { bigint: true });
    if (
      !workspace.isDirectory()
      || workspace.isSymbolicLink()
      || !sameVersion(version(workspace), options.workspace.version)
      || realpathSync(options.workspace.path) !== options.workspace.path
    ) {
      throw new Error("workspace changed");
    }
    for (const control of options.controls) {
      const metadata = lstatSync(control.path, { bigint: true });
      if (
        !metadata.isFile()
        || metadata.isSymbolicLink()
        || !sameVersion(version(metadata), control.version)
        || realpathSync(control.path) !== control.path
      ) {
        throw new Error("control changed");
      }
    }
    for (const artifact of options.artifacts) {
      const metadata = lstatSync(artifact.absolutePath, { bigint: true });
      const authority = lstatSync(artifact.authority.path, { bigint: true });
      if (
        !metadata.isFile()
        || metadata.isSymbolicLink()
        || !sameVersion(version(metadata), artifact.version)
        || realpathSync(artifact.absolutePath) !== artifact.absolutePath
        || !authority.isDirectory()
        || authority.isSymbolicLink()
        || !sameVersion(version(authority), artifact.authority.version)
        || realpathSync(artifact.authority.path) !== artifact.authority.path
      ) {
        throw new Error("artifact changed");
      }
    }
    for (const absent of options.absences) {
      try {
        lstatSync(absent.path);
        throw new Error("absence changed");
      } catch (error: unknown) {
        if (errorCode(error) !== "ENOENT") {
          throw error;
        }
      }
      const ancestor = lstatSync(absent.ancestor.path, { bigint: true });
      if (
        !ancestor.isDirectory()
        || ancestor.isSymbolicLink()
        || !sameVersion(version(ancestor), absent.ancestor.version)
        || realpathSync(absent.ancestor.path) !== absent.ancestor.path
      ) {
        throw new Error("absence authority changed");
      }
    }
    for (const [index, module] of options.modules.entries()) {
      const root = lstatSync(module.absolutePath, { bigint: true });
      const authority = lstatSync(module.authority.path, { bigint: true });
      if (
        !root.isDirectory()
        || root.isSymbolicLink()
        || !sameVersion(version(root), module.version)
        || realpathSync(module.absolutePath) !== module.absolutePath
        || !authority.isDirectory()
        || authority.isSymbolicLink()
        || !sameVersion(version(authority), module.authority.version)
        || realpathSync(module.authority.path) !== module.authority.path
      ) {
        throw new Error("module root changed");
      }
      const files = options.moduleFileVersions[index];
      if (files === undefined || !sameVersionSet(files, module.fileVersions)) {
        throw new Error("module file binding changed");
      }
      for (const file of files) {
        if (!sameVersion(syncVersion(file.path, true), file.version)) {
          throw new Error("module file changed");
        }
      }
    }
  } catch {
    return fail(
      "PLAN_ROOT_INPUT_CHANGED",
      "a bound input changed before the preparation result was committed",
      "io",
    );
  }
}

function requireRoot(
  topology: RootTopology,
  resourceType: ZccPullResourceType,
): RootTopologyRoot {
  const label = topology.resource_roots[resourceType];
  const root = topology.roots.find((candidate) => candidate.label === label);
  if (label === undefined || root === undefined || root.env_dir === null) {
    return fail("INVALID_PLAN_ROOT_TOPOLOGY", "selected plan root is unresolved");
  }
  return root;
}

function validateSnapshottedReceipts(
  receipts: readonly ZccAdoptionArtifactMaterialization[],
): ZccAdoptionArtifactMaterialization[] {
  if (
    !Array.isArray(receipts)
    || receipts.length < 1
    || receipts.length > 5
  ) {
    return fail("INVALID_PLAN_ROOT_MATERIALIZATIONS", "one to five receipts are required");
  }
  const snapshot = receipts as ZccAdoptionArtifactMaterialization[];
  for (const receipt of snapshot) {
    if (!validateZccAdoptionArtifactMaterialization(receipt)) {
      return fail(
        "INVALID_PLAN_ROOT_MATERIALIZATION",
        "materialization receipt failed its complete ready/equal contract",
      );
    }
  }
  return snapshot;
}

/** Compile one singular whole-root candidate without writing or running Terraform. */
export async function compileZccPlanRootPreparation(
  options: CompileZccPlanRootPreparationOptions,
): Promise<{
  readonly result: ZccPlanRootPreparationCandidate;
  readonly diagnostics: readonly WholeRootDiagnostic[];
}> {
  options = snapshotCompileOptions(options);
  if (
    options.profile !== ZCC_PLAN_ROOT_PREPARATION_PROFILE
    || options.mode !== "bootstrap"
    || !RESOURCE_TYPES.has(options.resourceType)
    || (options.backend !== "local" && options.backend !== "azurerm")
  ) {
    return fail(
      "UNSUPPORTED_PLAN_ROOT_PROFILE",
      "plan-root preparation accepts only the frozen exact-five bootstrap profile",
    );
  }
  validateTenant(options.tenant);
  const receipts = validateSnapshottedReceipts(options.materializations);
  const receiptTypes = receipts.map((receipt) => receipt.resource_type);
  if (!sameStrings(receiptTypes, sortedStrings(new Set(receiptTypes)))) {
    return fail(
      "INVALID_PLAN_ROOT_MATERIALIZATIONS",
      "materialization receipts must be sorted and unique",
    );
  }
  if (receipts.some((receipt) => receipt.tenant !== options.tenant)) {
    return fail(
      "PLAN_ROOT_MATERIALIZATION_CONTEXT_MISMATCH",
      "materialization receipt tenant does not match the request",
    );
  }

  const workspace = await bindWorkspace(options.workspace);
  const deploymentPath = await requireCanonicalControl(workspace.path, options.deploymentPath);
  const catalogPath = await requireCanonicalControl(workspace.path, options.catalogPath);
  const boundDeployment = await loadBoundAssessmentDeployment(deploymentPath, {
    followSymlinks: false,
  });
  const boundCatalog = await loadBoundAssessmentRootCatalog(catalogPath, {
    followSymlinks: false,
  });
  const controls = copyAssessmentControlFiles([
    boundDeployment.file,
    boundCatalog.file,
  ]);
  const controlVersions = bindControlVersions(controls);
  requireSupportedZscalerCompileCatalog(boundCatalog.catalog);
  if (
    boundDeployment.deployment.tfvars_format !== undefined
    && boundDeployment.deployment.tfvars_format !== "json"
  ) {
    return fail("UNSUPPORTED_PLAN_ROOT_TFVARS_FORMAT", "plan-root preparation supports JSON only");
  }
  if (
    typeof boundDeployment.deployment.overlay !== "string"
    || path.posix.isAbsolute(boundDeployment.deployment.overlay)
  ) {
    return fail(
      "UNSUPPORTED_PLAN_ROOT_OVERLAY",
      "plan-root preparation requires a repository-relative deployment overlay",
    );
  }
  if (boundDeployment.deployment.roots.zcc?.bind_references === true) {
    return fail(
      "UNSUPPORTED_PLAN_ROOT_BINDINGS",
      "plan-root preparation requires bind_references false or absent",
    );
  }
  const topologyResult = rootTopology({
    catalog: boundCatalog.catalog,
    deployment: boundDeployment.deployment,
    tenant: options.tenant,
    selectors: [options.resourceType],
  });
  const topology = topologyResult.topology;
  const root = requireRoot(topology, options.resourceType);
  if (
    root.provider !== "zcc"
    || root.members.length < 1
    || root.members.length > 5
    || root.members.some((member) => !RESOURCE_TYPES.has(member))
  ) {
    return fail(
      "UNSUPPORTED_PLAN_ROOT_MEMBERS",
      "whole-root expansion must remain entirely inside the exact-five profile",
    );
  }
  const members = root.members as readonly ZccPullResourceType[];
  if (!sameStrings(receiptTypes, members)) {
    return fail(
      "PLAN_ROOT_MATERIALIZATION_COVERAGE_MISMATCH",
      "sorted materialization receipts must exactly cover every whole-root member",
    );
  }

  const importDigests = receipts.map((receipt) => {
    return expectedDigest(receipt.verification.parity.artifacts.imports);
  });
  if (
    importDigests.some((digest) => {
      return digest.size_bytes > MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES;
    })
    || importDigests.reduce((total, digest) => total + digest.size_bytes, 0)
      > MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES
  ) {
    return fail(
      "PLAN_ROOT_CANDIDATE_TOO_LARGE",
      "staged imports exceed the plan-root candidate byte budget",
    );
  }

  const artifactBudget = new ReadBudget(ARTIFACT_READ_LIMITS);
  const sources: ZccPlanRootSourceBinding[] = [];
  const boundArtifacts: BoundArtifact[] = [];
  const stagedImports: ZccPlanRootTextArtifact[] = [];
  for (const [index, resourceType] of members.entries()) {
    const receipt = receipts[index];
    if (receipt === undefined || receipt.resource_type !== resourceType) {
      return fail("PLAN_ROOT_MATERIALIZATION_COVERAGE_MISMATCH", "receipt join changed");
    }
    const target = resolveZccArtifactTarget({
      tenant: options.tenant,
      resourceType,
      deployment: boundDeployment.deployment,
      catalog: boundCatalog.catalog,
    });
    if (
      receipt.verification.root.label !== root.label
      || !sameStrings(receipt.verification.root.members, members)
      || receipt.verification.root.variable_name !== target.variableName
    ) {
      return fail(
        "PLAN_ROOT_MATERIALIZATION_ROOT_MISMATCH",
        "materialization receipt does not match the freshly derived whole root",
      );
    }
    const tfvarsDigest = expectedDigest(receipt.verification.parity.artifacts.tfvars);
    const importsDigest = importDigests[index];
    if (importsDigest === undefined) {
      return fail("PLAN_ROOT_MATERIALIZATION_COVERAGE_MISMATCH", "receipt join changed");
    }
    const lookupEntry = receipt.verification.parity.artifacts.lookup;
    const lookupDigest = lookupEntry.status === "not_applicable"
      ? null
      : expectedDigest(lookupEntry);
    if (
      tfvarsDigest.path !== target.configPath
      || importsDigest.path !== target.importsPath
      || (lookupDigest === null ? null : lookupDigest.path) !== target.lookupPath
      || (lookupDigest === null) !== (target.lookupPath === null)
    ) {
      return fail(
        "PLAN_ROOT_MATERIALIZATION_PATH_MISMATCH",
        "materialization receipt paths do not match the deployment layout",
      );
    }
    const tfvars = await bindArtifact(workspace.path, tfvarsDigest, artifactBudget);
    const imports = await bindArtifact(workspace.path, importsDigest, artifactBudget);
    const lookup = lookupDigest === null
      ? null
      : await bindArtifact(workspace.path, lookupDigest, artifactBudget);
    parseGeneratedImports(resourceType, imports.text);
    boundArtifacts.push(tfvars, imports, ...(lookup === null ? [] : [lookup]));
    stagedImports.push(zccPlanRootTextArtifact(
      pythonPosixJoin(root.env_dir ?? "", path.posix.basename(importsDigest.path)),
      "text/x-hcl",
      imports.text,
    ));
    sources.push({
      resource_type: resourceType,
      materialization_sha256: zccPlanRootMaterializationSha256(receipt),
      provider_observed_source: { ...receipt.verification.source },
      adoption_catalog: { ...receipt.verification.catalog },
      materialized_artifacts: {
        tfvars: tfvarsDigest,
        imports: importsDigest,
        lookup: lookupDigest,
      },
    });
  }

  const absentSidecars = zccPlanRootAbsentSidecarPaths(
    topology,
    members,
    root.env_dir ?? "",
  );
  const absentAbsolute: BoundAbsent[] = [];
  for (const sidecar of absentSidecars) {
    absentAbsolute.push(await assertAbsent(workspace.path, sidecar));
  }

  if (topology.directories === null || root.env_dir === null) {
    return fail("INVALID_PLAN_ROOT_TOPOLOGY", "tenant directories are unresolved");
  }
  const markerPath = pythonPosixJoin(topology.directories.envs, ".backend");
  const markerAbsolute = resolveLogical(workspace.path, markerPath);
  const markerDesired = options.backend === "azurerm"
    ? zccPlanRootTextArtifact(markerPath, "text/plain", "azurerm\n")
    : null;
  let markerObserved: "absent" | "exact" = "absent";
  let boundMarker: BoundArtifact | null = null;
  let absentMarker: BoundAbsent | null = null;
  if (options.backend === "local") {
    absentMarker = await assertAbsent(workspace.path, markerPath, {
      checkCode: "PLAN_ROOT_BACKEND_MARKER_CHECK_FAILED",
      checkMessage: "unable to inspect backend marker",
      presentCode: "PLAN_ROOT_BACKEND_MARKER_MISMATCH",
      presentMessage: "local backend requires the tenant backend marker to be absent",
    });
  } else {
    try {
      await lstat(markerAbsolute);
      if (markerDesired === null) {
        return fail("PLAN_ROOT_BACKEND_MARKER_MISMATCH", "backend marker policy is unresolved");
      }
      boundMarker = await bindArtifact(workspace.path, markerDesired, artifactBudget);
      markerObserved = "exact";
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        throw error;
      }
      if (errorCode(error) !== "ENOENT") {
        return fail("PLAN_ROOT_BACKEND_MARKER_CHECK_FAILED", "unable to inspect backend marker", "io");
      }
      absentMarker = {
        path: markerAbsolute,
        ancestor: nearestExistingAncestor(workspace.path, markerAbsolute),
      };
    }
  }

  const moduleBudget = new ReadBudget(MODULE_READ_LIMITS);
  const moduleVerificationBudget = new ReadBudget(MODULE_READ_LIMITS);
  const moduleBase = moduleDir(boundDeployment.deployment);
  const boundModules: BoundModule[] = [];
  for (const resourceType of members) {
    boundModules.push(await bindModule({
      workspace: workspace.path,
      moduleBase,
      envDir: root.env_dir,
      resourceType,
      budget: moduleBudget,
      verificationBudget: moduleVerificationBudget,
    }));
  }
  const moduleSources = new Map(
    boundModules.map((module) => [module.binding.resource_type, module.binding.source]),
  );
  const main = zccPlanRootTextArtifact(
    pythonPosixJoin(root.env_dir, "main.tf"),
    "text/x-hcl",
    renderZccPlanRootMain({
      tenant: options.tenant,
      label: root.label,
      members,
      backend: options.backend,
      moduleSources,
    }),
  );

  await options.hooks?.afterInputsBound?.();
  await options.hooks?.beforeFinalRecheck?.();
  await recheckWorkspace(workspace);
  await recheckAssessmentControlFiles(controls);
  await recheckAbsent(absentAbsolute);
  const artifactRecheckBudget = new ReadBudget(ARTIFACT_READ_LIMITS);
  for (const artifact of boundArtifacts) {
    await recheckArtifact(artifact, artifactRecheckBudget);
  }
  if (absentMarker !== null) {
    await recheckAbsent([absentMarker]);
  } else {
    if (boundMarker === null) {
      return fail("PLAN_ROOT_BACKEND_MARKER_MISMATCH", "backend marker binding is unresolved", "internal");
    }
    await recheckArtifact(boundMarker, artifactRecheckBudget);
  }
  const moduleRecheckBudget = new ReadBudget(MODULE_READ_LIMITS);
  const moduleFileVersions: Array<readonly {
    readonly path: string;
    readonly version: PathVersion;
  }[]> = [];
  for (const module of boundModules) {
    moduleFileVersions.push(await recheckModule(module, moduleRecheckBudget));
  }
  await options.hooks?.afterAsyncRechecks?.();
  finalCoherentCas({
    workspace,
    controls: controlVersions,
    artifacts: [
      ...boundArtifacts,
      ...(boundMarker === null ? [] : [boundMarker]),
    ],
    absences: [
      ...absentAbsolute,
      ...(absentMarker === null ? [] : [absentMarker]),
    ],
    modules: boundModules,
    moduleFileVersions,
  });

  const result: ZccPlanRootPreparationCandidate = {
    kind: "infrawright.zcc_plan_root_preparation_candidate",
    schema_version: 1,
    profile: ZCC_PLAN_ROOT_PREPARATION_PROFILE,
    mode: "bootstrap",
    product: "zcc",
    tenant: options.tenant,
    selector: { resource_type: options.resourceType },
    backend: options.backend,
    status: "candidate_only",
    qualification: {
      readiness: "not_qualified",
      validation: "not_performed",
      plan: "not_performed",
      apply: "not_performed",
      refresh: "not_performed",
      publication: "not_performed",
      cutover: "not_qualified",
    },
    renderer: {
      profile: ZCC_PLAN_ROOT_RENDERER_PROFILE,
      python_source: "engine.gen_env",
      reference_terraform_version: "1.15.4",
      terraform_executed: false,
    },
    controls: {
      deployment: controlDigest(workspace.path, boundDeployment.file),
      root_catalog: controlDigest(workspace.path, boundCatalog.file),
    },
    topology,
    backend_marker: {
      path: markerPath,
      observed_state: markerObserved,
      desired: markerDesired,
    },
    absent_sidecars: absentSidecars,
    sources,
    modules: boundModules.map((module) => module.binding),
    root: {
      label: root.label,
      provider: "zcc",
      members,
      env_dir: root.env_dir,
      artifacts: {
        main_tf: main,
        staged_imports: stagedImports,
      },
    },
    summary: {
      selected: 1,
      roots: 1,
      members: members.length,
      modules: boundModules.length,
      staged_imports: stagedImports.length,
    },
  };
  if (
    pythonCompatibleJsonByteLength(
      result as unknown as JsonValue,
      MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
    ) > MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES
  ) {
    return fail(
      "PLAN_ROOT_CANDIDATE_TOO_LARGE",
      "plan-root candidate exceeds its serialized byte budget",
    );
  }
  return { result, diagnostics: topologyResult.diagnostics };
}
