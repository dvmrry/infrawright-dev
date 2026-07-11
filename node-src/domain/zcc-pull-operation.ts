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

/** Bind, compile, and finally recheck every input without writing artifacts. */
export async function compileZccPullArtifactsOperation(options: {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tenant: string;
  readonly resourceType: string;
  readonly hooks?: ZccPullOperationHooks;
}): Promise<ZccPullArtifactSet> {
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
  const importsAbsolute = resolvedPath(workspace, target.importsPath);
  const movesAbsolute = importsAbsolute.slice(0, -"_imports.tf".length)
    + "_moves.tf";
  const initialImportsPhysical = pythonPosixRealpath(importsAbsolute);
  const initialMovesPhysical = pythonPosixRealpath(movesAbsolute);
  await assertAbsent(importsAbsolute, "imports");
  await assertAbsent(movesAbsolute, "moves");

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
  if (pythonPosixRealpath(importsAbsolute) !== initialImportsPhysical) {
    return fail("BOOTSTRAP_IMPORTS_CHANGED", "bootstrap import target changed", "io");
  }
  if (pythonPosixRealpath(movesAbsolute) !== initialMovesPhysical) {
    return fail("BOOTSTRAP_MOVES_CHANGED", "bootstrap move target changed", "io");
  }
  await assertAbsent(importsAbsolute, "imports");
  await assertAbsent(movesAbsolute, "moves");
  return result;
}
