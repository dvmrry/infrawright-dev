import { lstat, realpath, stat } from "node:fs/promises";
import path from "node:path";

import {
  loadBoundAssessmentRootCatalog,
} from "./catalog.js";
import {
  copyAssessmentControlFiles,
  recheckAssessmentControlFiles,
} from "./control-evidence.js";
import {
  loadBoundAssessmentDeployment,
} from "./deployment.js";
import { ProcessFailure } from "./errors.js";
import { pythonPosixJoin, pythonPosixRealpath } from "./paths.js";
import { rootTopology, validateTenant } from "./roots.js";
import { loadZccTransformCatalog } from "./transform-catalog.js";
import type { RootTopologyRoot } from "./types.js";
import {
  compileZccPullArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
  type ZccArtifactTarget,
  type ZccPullArtifactSet,
} from "./zcc-pull-artifacts.js";
import {
  compareZccPullArtifactDigests,
  type ZccMaterializedPullArtifactDigests,
  type ZccPullArtifactDigest,
  type ZccPullArtifactParity,
} from "./zcc-pull-parity.js";
import { requireSupportedZscalerCompileCatalog } from "./zscaler-assessment.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
  type BoundedReadLimits,
  type StableReadHooks,
} from "../io/bounded-files.js";
import { parseZccPullDataJson } from "../json/zcc-pull-data.js";

const PULL_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 1,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 4n * 1024n * 1024n,
  maxFileBytes: 4n * 1024n * 1024n,
};

const MATERIALIZED_ARTIFACT_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 3,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 96n * 1024n * 1024n,
  maxFileBytes: 32n * 1024n * 1024n,
};

export interface ZccPullOperationHooks {
  /** Test seam for descriptor/path mutation during the stable source read. */
  readonly sourceRead?: StableReadHooks;
  /** Test seam after all inputs and the bootstrap absence precondition are bound. */
  readonly afterInputsBound?: () => void | Promise<void>;
  /** Test seam immediately before the final transaction recheck. */
  readonly beforeFinalRecheck?: () => void | Promise<void>;
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

async function canonicalWorkspace(workspace: string): Promise<string> {
  try {
    const canonical = await realpath(workspace);
    const metadata = await stat(canonical);
    if (!metadata.isDirectory()) {
      return fail("INVALID_WORKSPACE", "context.workspace must resolve to a directory");
    }
    return canonical;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("INVALID_WORKSPACE", "context.workspace could not be resolved");
  }
}

function containedPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === "" || (
    relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative)
  );
}

async function canonicalPullSource(options: {
  readonly lexicalPath: string;
  readonly workspace: string;
}): Promise<string> {
  try {
    const canonical = await realpath(options.lexicalPath);
    if (!containedPath(canonical, options.workspace)) {
      return fail("PULL_OUTSIDE_WORKSPACE", "raw pull must resolve inside context.workspace");
    }
    return canonical;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("READ_FAILED", "unable to resolve raw pull", "io");
  }
}

type BootstrapArtifactKind = "imports" | "moves";

type ZccPullArtifactPolicy =
  | { readonly kind: "bootstrap_absent" }
  | { readonly kind: "compare_materialized" };

interface BoundBootstrapArtifactPolicy {
  readonly kind: "bootstrap_absent";
  readonly importsAbsolute: string;
  readonly movesAbsolute: string;
  readonly initialImportsPhysical: string;
  readonly initialMovesPhysical: string;
}

interface BoundMaterializedArtifact {
  readonly absolutePath: string;
  readonly initialPhysicalPath: string;
  readonly digest: ZccPullArtifactDigest | null;
  readonly identity: {
    readonly dev: bigint;
    readonly ino: bigint;
  } | null;
}

interface UnsupportedCompareArtifact {
  readonly absolutePath: string;
  readonly code: string;
  readonly message: string;
}

interface BoundCompareArtifactPolicy {
  readonly kind: "compare_materialized";
  readonly materialized: {
    readonly tfvars: BoundMaterializedArtifact;
    readonly imports: BoundMaterializedArtifact;
    readonly lookup: BoundMaterializedArtifact | null;
  };
  readonly unsupported: readonly UnsupportedCompareArtifact[];
}

type BoundZccPullArtifactPolicy =
  | BoundBootstrapArtifactPolicy
  | BoundCompareArtifactPolicy;

function bootstrapCode(kind: BootstrapArtifactKind, suffix: string): string {
  return `BOOTSTRAP_${kind.toUpperCase()}_${suffix}`;
}

async function assertAbsent(
  filePath: string,
  kind: BootstrapArtifactKind,
): Promise<void> {
  try {
    await lstat(filePath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
    return fail(
      bootstrapCode(kind, "CHECK_FAILED"),
      `unable to verify bootstrap ${kind} precondition`,
      "io",
    );
  }
  fail(
    bootstrapCode(kind, "EXIST"),
    `bootstrap compilation requires the prior ${kind} artifact to be absent`,
  );
}

async function assertCompareArtifactAbsent(
  artifact: UnsupportedCompareArtifact,
): Promise<void> {
  try {
    await lstat(artifact.absolutePath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
    return fail(
      "COMPARE_ARTIFACT_CHECK_FAILED",
      "unable to verify the compare artifact policy",
      "io",
    );
  }
  fail(artifact.code, artifact.message);
}

async function bindMaterializedArtifact(
  absolutePath: string,
  budget: ReadBudget,
): Promise<BoundMaterializedArtifact> {
  const initialPhysicalPath = pythonPosixRealpath(absolutePath);
  let identity: { readonly dev: bigint; readonly ino: bigint };
  try {
    const metadata = await lstat(absolutePath, { bigint: true });
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "COMPARE_ARTIFACT_READ_FAILED",
        "materialized comparison input must be a regular file",
        "io",
      );
    }
    identity = { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return {
        absolutePath,
        initialPhysicalPath,
        digest: null,
        identity: null,
      };
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "COMPARE_ARTIFACT_READ_FAILED",
      "unable to inspect a materialized comparison input",
      "io",
    );
  }
  try {
    const digest = await sha256StableFile(absolutePath, budget);
    const current = await lstat(absolutePath, { bigint: true });
    if (
      !current.isFile()
      || current.isSymbolicLink()
      || current.dev !== identity.dev
      || current.ino !== identity.ino
    ) {
      return fail(
        "COMPARE_ARTIFACT_CHANGED",
        "materialized comparison input changed during comparison",
        "io",
      );
    }
    return {
      absolutePath,
      initialPhysicalPath,
      digest: {
        sha256: digest.sha256,
        size_bytes: Number(digest.size),
      },
      identity,
    };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "COMPARE_ARTIFACT_CHANGED") {
      throw error;
    }
    return fail(
      "COMPARE_ARTIFACT_READ_FAILED",
      "unable to read a stable materialized comparison input",
      "io",
    );
  }
}

function compareArtifactPaths(
  workspace: string,
  target: ZccArtifactTarget,
): {
  readonly tfvars: string;
  readonly imports: string;
  readonly lookup: string | null;
  readonly staleLookup: string | null;
  readonly unsupported: readonly UnsupportedCompareArtifact[];
} {
  const tfvars = resolvedPath(workspace, target.configPath);
  const imports = resolvedPath(workspace, target.importsPath);
  const lookupContractPath = pythonPosixJoin(
    path.posix.dirname(target.configPath),
    `${target.resourceType}.lookup.json`,
  );
  const lookup = target.lookupPath === null
    ? null
    : resolvedPath(workspace, target.lookupPath);
  const staleLookup = target.lookupPath === null
    ? resolvedPath(workspace, lookupContractPath)
    : null;
  const alternateHcl = target.configPath.endsWith(".json")
    ? resolvedPath(workspace, target.configPath.slice(0, -".json".length))
    : target.configPath;
  const moves = target.importsPath.endsWith("_imports.tf")
    ? resolvedPath(
        workspace,
        target.importsPath.slice(0, -"_imports.tf".length) + "_moves.tf",
      )
    : target.importsPath;
  const generatedBindings = resolvedPath(
    workspace,
    pythonPosixJoin(
      path.posix.dirname(target.configPath),
      `${target.resourceType}.generated.expressions.json`,
    ),
  );
  return {
    tfvars,
    imports,
    lookup,
    staleLookup,
    unsupported: [
      {
        absolutePath: moves,
        code: "UNSUPPORTED_COMPARE_MOVES",
        message: "bootstrap comparison does not support materialized move artifacts",
      },
      {
        absolutePath: alternateHcl,
        code: "UNSUPPORTED_COMPARE_HCL_ARTIFACT",
        message: "bootstrap comparison does not support a stale HCL tfvars artifact",
      },
      {
        absolutePath: generatedBindings,
        code: "UNSUPPORTED_COMPARE_GENERATED_BINDINGS",
        message: "bootstrap comparison does not support generated reference bindings",
      },
      ...(staleLookup === null
        ? []
        : [{
            absolutePath: staleLookup,
            code: "UNSUPPORTED_COMPARE_LOOKUP_ARTIFACT",
            message: "bootstrap comparison found a stale lookup artifact",
          }]),
    ],
  };
}

async function bindArtifactPolicy(options: {
  readonly workspace: string;
  readonly target: ZccArtifactTarget;
  readonly policy: ZccPullArtifactPolicy;
}): Promise<BoundZccPullArtifactPolicy> {
  const paths = compareArtifactPaths(options.workspace, options.target);
  if (options.policy.kind === "bootstrap_absent") {
    const moves = paths.unsupported.find(
      (artifact) => artifact.code === "UNSUPPORTED_COMPARE_MOVES",
    );
    if (moves === undefined) {
      return fail("INTERNAL_ERROR", "bootstrap move target is unresolved", "internal");
    }
    const initialImportsPhysical = pythonPosixRealpath(paths.imports);
    const initialMovesPhysical = pythonPosixRealpath(moves.absolutePath);
    await assertAbsent(paths.imports, "imports");
    await assertAbsent(moves.absolutePath, "moves");
    return {
      kind: "bootstrap_absent",
      importsAbsolute: paths.imports,
      movesAbsolute: moves.absolutePath,
      initialImportsPhysical,
      initialMovesPhysical,
    };
  }
  for (const artifact of paths.unsupported) {
    await assertCompareArtifactAbsent(artifact);
  }
  const budget = new ReadBudget(MATERIALIZED_ARTIFACT_READ_LIMITS);
  return {
    kind: "compare_materialized",
    materialized: {
      tfvars: await bindMaterializedArtifact(paths.tfvars, budget),
      imports: await bindMaterializedArtifact(paths.imports, budget),
      lookup: paths.lookup === null
        ? null
        : await bindMaterializedArtifact(paths.lookup, budget),
    },
    unsupported: paths.unsupported,
  };
}

async function assertStillMissing(filePath: string): Promise<void> {
  try {
    await lstat(filePath);
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return;
    }
  }
  fail(
    "COMPARE_ARTIFACT_CHANGED",
    "materialized comparison input changed during comparison",
    "io",
  );
}

async function recheckMaterializedArtifact(
  artifact: BoundMaterializedArtifact,
  budget: ReadBudget,
): Promise<void> {
  if (pythonPosixRealpath(artifact.absolutePath) !== artifact.initialPhysicalPath) {
    return fail(
      "COMPARE_ARTIFACT_CHANGED",
      "materialized comparison input changed during comparison",
      "io",
    );
  }
  if (artifact.digest === null) {
    await assertStillMissing(artifact.absolutePath);
    return;
  }
  if (artifact.identity === null) {
    return fail("INTERNAL_ERROR", "materialized artifact identity is missing", "internal");
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
        "COMPARE_ARTIFACT_CHANGED",
        "materialized comparison input changed during comparison",
        "io",
      );
    }
    const current = await sha256StableFile(artifact.absolutePath, budget);
    const after = await lstat(artifact.absolutePath, { bigint: true });
    if (
      current.sha256 !== artifact.digest.sha256
      || current.size !== BigInt(artifact.digest.size_bytes)
      || after.dev !== artifact.identity.dev
      || after.ino !== artifact.identity.ino
    ) {
      return fail(
        "COMPARE_ARTIFACT_CHANGED",
        "materialized comparison input changed during comparison",
        "io",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "COMPARE_ARTIFACT_CHANGED") {
      throw error;
    }
    return fail(
      "COMPARE_ARTIFACT_CHANGED",
      "materialized comparison input changed during comparison",
      "io",
    );
  }
}

async function recheckArtifactPolicy(
  policy: BoundZccPullArtifactPolicy,
): Promise<void> {
  if (policy.kind === "bootstrap_absent") {
    if (pythonPosixRealpath(policy.importsAbsolute) !== policy.initialImportsPhysical) {
      return fail("BOOTSTRAP_IMPORTS_CHANGED", "bootstrap import target changed", "io");
    }
    if (pythonPosixRealpath(policy.movesAbsolute) !== policy.initialMovesPhysical) {
      return fail("BOOTSTRAP_MOVES_CHANGED", "bootstrap move target changed", "io");
    }
    await assertAbsent(policy.importsAbsolute, "imports");
    await assertAbsent(policy.movesAbsolute, "moves");
    return;
  }
  for (const artifact of policy.unsupported) {
    try {
      await assertCompareArtifactAbsent(artifact);
    } catch {
      return fail(
        "COMPARE_ARTIFACT_CHANGED",
        "compare policy artifact changed during comparison",
        "io",
      );
    }
  }
  const budget = new ReadBudget(MATERIALIZED_ARTIFACT_READ_LIMITS);
  await recheckMaterializedArtifact(policy.materialized.tfvars, budget);
  await recheckMaterializedArtifact(policy.materialized.imports, budget);
  if (policy.materialized.lookup !== null) {
    await recheckMaterializedArtifact(policy.materialized.lookup, budget);
  }
}

function materializedDigests(
  policy: BoundCompareArtifactPolicy,
): ZccMaterializedPullArtifactDigests {
  return {
    tfvars: policy.materialized.tfvars.digest,
    imports: policy.materialized.imports.digest,
    lookup: policy.materialized.lookup?.digest ?? null,
  };
}

function oneRoot(
  roots: readonly RootTopologyRoot[],
  label: string,
): RootTopologyRoot {
  const root = roots.find((candidate) => candidate.label === label);
  if (root === undefined) {
    return fail("INVALID_ROOT_CONFIGURATION", "compiled resource root is missing");
  }
  return root;
}

function resolveArtifactTarget(options: {
  readonly tenant: string;
  readonly resourceType: string;
  readonly deployment: Awaited<ReturnType<typeof loadBoundAssessmentDeployment>>["deployment"];
  readonly catalog: Awaited<ReturnType<typeof loadBoundAssessmentRootCatalog>>["catalog"];
}): ZccArtifactTarget {
  const format = options.deployment.tfvars_format;
  if (format !== undefined && format !== "json" && format !== "hcl") {
    return fail("INVALID_DEPLOYMENT", "deployment tfvars_format must be 'json' or 'hcl'");
  }
  if (format === "hcl") {
    return fail(
      "UNSUPPORTED_TFVARS_FORMAT",
      "bootstrap pull artifact compilation supports JSON tfvars only",
    );
  }
  const { topology } = rootTopology({
    catalog: options.catalog,
    deployment: options.deployment,
    tenant: options.tenant,
    selectors: [options.resourceType],
  });
  const rootLabel = topology.resource_roots[options.resourceType];
  if (rootLabel === undefined || topology.directories === null) {
    return fail("INVALID_ROOT_CONFIGURATION", "compiled resource root is unresolved");
  }
  const root = oneRoot(topology.roots, rootLabel);
  const transformResource = loadZccTransformCatalog().resources.find(
    (candidate) => candidate.type === options.resourceType,
  );
  if (transformResource === undefined) {
    return fail("UNSUPPORTED_COMPILE_RESOURCE", "resource is not supported for compilation");
  }
  const bindReferences = options.deployment.roots.zcc?.bind_references === true;
  if (
    bindReferences
    && Object.values(transformResource.references).some(
      (reference) => root.members.includes(reference.referent),
    )
  ) {
    return fail(
      "UNSUPPORTED_GROUP_BINDINGS",
      "bootstrap compilation does not yet support same-root generated reference bindings",
    );
  }
  const variableName = rootLabel === options.resourceType
    ? "items"
    : `${options.resourceType}_items`;
  const configPath = pythonPosixJoin(
    topology.directories.config,
    `${options.resourceType}.auto.tfvars.json`,
  );
  const importsPath = pythonPosixJoin(
    topology.directories.imports,
    `${options.resourceType}_imports.tf`,
  );
  return {
    tenant: options.tenant,
    resourceType: options.resourceType,
    rootLabel,
    rootMembers: root.members,
    variableName,
    configPath,
    importsPath,
    lookupPath: transformResource.lookup_source === null
      ? null
      : pythonPosixJoin(
          topology.directories.config,
          `${options.resourceType}.lookup.json`,
        ),
  };
}

async function recheckSource(options: {
  readonly canonicalSource: string;
  readonly lexicalSource: string;
  readonly lexicalWorkspace: string;
  readonly canonicalWorkspace: string;
  readonly expected: { readonly sha256: string; readonly size: bigint };
}): Promise<void> {
  try {
    const workspace = await canonicalWorkspace(options.lexicalWorkspace);
    if (workspace !== options.canonicalWorkspace) {
      return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
    }
    const source = await canonicalPullSource({
      lexicalPath: options.lexicalSource,
      workspace,
    });
    if (source !== options.canonicalSource) {
      return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
    }
    const current = await sha256StableFile(
      source,
      new ReadBudget(PULL_READ_LIMITS),
    );
    if (
      current.sha256 !== options.expected.sha256
      || current.size !== options.expected.size
    ) {
      return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "RAW_PULL_CHANGED") {
      throw error;
    }
    return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
  }
}

interface ZccPullArtifactsOperationOptions {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tenant: string;
  readonly resourceType: string;
  readonly hooks?: ZccPullOperationHooks;
}

async function compileZccPullArtifactsWithPolicy(
  options: ZccPullArtifactsOperationOptions,
  artifactPolicy: ZccPullArtifactPolicy,
): Promise<{
  readonly candidate: ZccPullArtifactSet;
  readonly policy: BoundZccPullArtifactPolicy;
}> {
  const request = {
    workspace: options.workspace,
    deploymentPath: options.deploymentPath,
    catalogPath: options.catalogPath,
    tenant: options.tenant,
    resourceType: options.resourceType,
  };
  if (!path.isAbsolute(request.workspace)) {
    fail("INVALID_WORKSPACE", "context.workspace must be an absolute path");
  }
  validateTenant(request.tenant);
  const workspace = await canonicalWorkspace(request.workspace);
  const boundCatalog = await loadBoundAssessmentRootCatalog(request.catalogPath);
  requireSupportedZscalerCompileCatalog(boundCatalog.catalog);
  const boundDeployment = await loadBoundAssessmentDeployment(request.deploymentPath);
  const controls = copyAssessmentControlFiles([
    boundCatalog.file,
    boundDeployment.file,
  ]);
  const target = resolveArtifactTarget({
    tenant: request.tenant,
    resourceType: request.resourceType,
    deployment: boundDeployment.deployment,
    catalog: boundCatalog.catalog,
  });
  const boundPolicy = await bindArtifactPolicy({
    workspace,
    target,
    policy: artifactPolicy,
  });

  const relativeSource = pythonPosixJoin(
    "pulls",
    request.tenant,
    `${request.resourceType}.json`,
  );
  const lexicalSource = path.resolve(workspace, relativeSource);
  const canonicalSource = await canonicalPullSource({
    lexicalPath: lexicalSource,
    workspace,
  });
  const source = await readBoundedUtf8File(
    canonicalSource,
    new ReadBudget(PULL_READ_LIMITS),
    options.hooks?.sourceRead === undefined
      ? {}
      : { hooks: options.hooks.sourceRead },
  );
  let rawItems: readonly unknown[];
  try {
    rawItems = parseZccPullDataJson(source.text);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("INVALID_PULL_DATA", "raw pull is not supported JSON item data");
  }
  await options.hooks?.afterInputsBound?.();
  let result: ZccPullArtifactSet;
  try {
    result = compileZccPullArtifactSet({
      catalog: loadZccTransformCatalog(),
      catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
      rawItems,
      target,
      source: {
        path: relativeSource,
        sha256: source.digest.sha256,
        size_bytes: Number(source.digest.size),
      },
    });
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("INVALID_PULL_DATA", "raw pull could not be compiled");
  }

  await options.hooks?.beforeFinalRecheck?.();
  await recheckSource({
    canonicalSource,
    lexicalSource,
    lexicalWorkspace: request.workspace,
    canonicalWorkspace: workspace,
    expected: source.digest,
  });
  try {
    await recheckAssessmentControlFiles(controls);
  } catch {
    return fail("COMPILE_CONTROL_CHANGED", "compile control input changed", "io");
  }
  await recheckArtifactPolicy(boundPolicy);
  return { candidate: result, policy: boundPolicy };
}

/** Bind, compile, and finally recheck every input without writing artifacts. */
export async function compileZccPullArtifactsOperation(
  options: ZccPullArtifactsOperationOptions,
): Promise<ZccPullArtifactSet> {
  const result = await compileZccPullArtifactsWithPolicy(
    options,
    { kind: "bootstrap_absent" },
  );
  return result.candidate;
}

/** Compare trusted candidate digests with stable materialized artifact reads. */
export async function compareZccPullArtifactsOperation(
  options: ZccPullArtifactsOperationOptions,
): Promise<ZccPullArtifactParity> {
  const result = await compileZccPullArtifactsWithPolicy(
    options,
    { kind: "compare_materialized" },
  );
  if (result.policy.kind !== "compare_materialized") {
    return fail("INTERNAL_ERROR", "comparison artifact policy is unresolved", "internal");
  }
  return compareZccPullArtifactDigests({
    candidate: result.candidate,
    materialized: materializedDigests(result.policy),
  });
}
