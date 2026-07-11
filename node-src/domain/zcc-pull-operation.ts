import { lstat, realpath, stat } from "node:fs/promises";
import path from "node:path";

import {
  schemaErrorDetails,
  validateZccPullArtifactParity,
} from "../contracts/validators.js";
import {
  loadBoundAssessmentRootCatalog,
} from "./catalog.js";
import {
  copyAssessmentControlFiles,
  recheckAssessmentControlFiles,
  type BoundAssessmentControlFile,
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
  compileZccPullRefreshCandidateArtifactSet,
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
import {
  materializeReadyZccPullArtifacts,
  type ZccPullArtifactMaterialization,
  type ZccPullMaterializationHooks,
} from "./zcc-pull-materialization.js";
import {
  compileZccPullRefreshArtifactSet,
  type ZccPullRefreshArtifactSet,
} from "./zcc-pull-refresh.js";
import {
  bindZccRefreshInputs,
  recheckZccRefreshInputs,
  zccRefreshBaselineInput,
  type BoundZccRefreshInputs,
} from "./zcc-pull-refresh-inputs.js";
import { requireSupportedZscalerCompileCatalog } from "./zscaler-assessment.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
  type BoundedReadLimits,
  type StableReadHooks,
} from "../io/bounded-files.js";
import { parseZccPullDataJson } from "../json/zcc-pull-data.js";
import { sortedStrings } from "../json/python-compatible.js";

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
  /** Test seam for descriptor/path mutation during the prior-imports read. */
  readonly priorImportsRead?: StableReadHooks;
  /** Test seam after all inputs and the selected mode preconditions are bound. */
  readonly afterInputsBound?: () => void | Promise<void>;
  /** Test seam immediately before the final transaction recheck. */
  readonly beforeFinalRecheck?: () => void | Promise<void>;
  /** Test seam after refresh derivation and before its final CAS recheck. */
  readonly afterRefreshCompiled?: () => void | Promise<void>;
}

export interface ZccParityTargetParentBinding {
  readonly path: string;
  readonly identity: {
    readonly dev: bigint;
    readonly ino: bigint;
  };
  readonly ancestors: readonly {
    readonly path: string;
    readonly identity: {
      readonly dev: bigint;
      readonly ino: bigint;
    };
  }[];
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

async function canonicalWorkspace(
  workspace: string,
  strictParity = false,
): Promise<string> {
  try {
    const canonical = await realpath(workspace);
    const metadata = await stat(canonical);
    if (
      !metadata.isDirectory()
      || (strictParity && (
        canonical !== workspace
        || workspace !== path.resolve(workspace)
        || workspace === path.parse(workspace).root
      ))
    ) {
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

export async function preflightZccParityInputPath(options: {
  readonly filePath: string;
  readonly workspace: string;
  readonly required: boolean;
}): Promise<void> {
  const lexical = path.resolve(options.filePath);
  const physical = pythonPosixRealpath(lexical);
  if (
    lexical !== options.filePath
    || physical !== lexical
    || !containedPath(physical, options.workspace)
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity inputs must be canonical and workspace-contained",
    );
  }
  try {
    const metadata = await lstat(lexical);
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity inputs must be regular non-symlink files",
      );
    }
  } catch (error: unknown) {
    if (!options.required && errorCode(error) === "ENOENT") {
      return;
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity input preflight failed",
    );
  }
}

export async function preflightZccParityArtifactTarget(options: {
  readonly workspace: string;
  readonly deployment: Awaited<ReturnType<typeof loadBoundAssessmentDeployment>>["deployment"];
  readonly target: ZccArtifactTarget;
  readonly requireMaterializedBaseline?: boolean;
}): Promise<readonly ZccParityTargetParentBinding[]> {
  const overlay = options.deployment.overlay;
  if (typeof overlay !== "string") {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity deployment authority is invalid",
    );
  }
  const lexicalAuthority = overlay === "."
    ? options.workspace
    : resolvedPath(options.workspace, overlay);
  let authority: string;
  try {
    authority = await realpath(lexicalAuthority);
    const metadata = await lstat(lexicalAuthority);
    if (
      authority !== lexicalAuthority
      || authority === path.parse(authority).root
      || !metadata.isDirectory()
      || metadata.isSymbolicLink()
    ) {
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity artifact authority is invalid",
      );
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity artifact authority is invalid",
    );
  }
  if (
    authority !== options.workspace
    && containedPath(options.workspace, authority)
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity artifact authority is invalid",
    );
  }
  const paths = compareArtifactPaths(options.workspace, options.target);
  const allTargets = [
    paths.tfvars,
    paths.imports,
    ...(paths.lookup === null ? [] : [paths.lookup]),
    ...paths.unsupported.map((artifact) => artifact.absolutePath),
  ].filter((value): value is string => value !== null);
  if (
    new Set(allTargets).size !== allTargets.length
    || allTargets.some((target) => !containedPath(target, authority))
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity artifact targets are invalid",
    );
  }
  const parentPaths = sortedStrings(new Set(allTargets.map((target) => path.dirname(target))));
  const parents: ZccParityTargetParentBinding[] = [];
  for (const parentPath of parentPaths) {
    try {
      const canonical = await realpath(parentPath);
      const metadata = await lstat(parentPath, { bigint: true });
      if (
        canonical !== parentPath
        || !metadata.isDirectory()
        || metadata.isSymbolicLink()
        || !containedPath(parentPath, authority)
      ) {
        return fail(
          "INVALID_REFRESH_PARITY_ISOLATION",
          "refresh parity artifact parents are invalid",
        );
      }
      parents.push({
        path: parentPath,
        identity: { dev: metadata.dev, ino: metadata.ino },
        ancestors: await (async () => {
          const relative = path.relative(authority, parentPath);
          const components = relative === "" ? [] : relative.split(path.sep);
          const chain: {
            path: string;
            identity: { dev: bigint; ino: bigint };
          }[] = [];
          let current = authority;
          for (const component of ["", ...components]) {
            if (component !== "") {
              current = path.join(current, component);
            }
            const ancestor = await lstat(current, { bigint: true });
            if (!ancestor.isDirectory() || ancestor.isSymbolicLink()) {
              return fail(
                "INVALID_REFRESH_PARITY_ISOLATION",
                "refresh parity artifact ancestry is invalid",
              );
            }
            chain.push({
              path: current,
              identity: { dev: ancestor.dev, ino: ancestor.ino },
            });
          }
          return chain;
        })(),
      });
    } catch (error: unknown) {
      if (error instanceof ProcessFailure) {
        throw error;
      }
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity artifact parents are invalid",
      );
    }
  }
  if (options.requireMaterializedBaseline !== false) {
    for (const requiredPath of [
      paths.tfvars,
      paths.imports,
      ...(paths.lookup === null ? [] : [paths.lookup]),
    ]) {
      try {
        const metadata = await lstat(requiredPath);
        if (!metadata.isFile() || metadata.isSymbolicLink()) {
          throw new Error("not regular");
        }
      } catch {
        return fail(
          "INVALID_REFRESH_PARITY_ISOLATION",
          "refresh parity requires a fully materialized run-one baseline",
        );
      }
    }
  }
  return parents;
}

export async function recheckZccParityTargetParents(
  parents: readonly ZccParityTargetParentBinding[],
): Promise<void> {
  for (const parent of parents) {
    for (const ancestor of parent.ancestors) {
      try {
        const metadata = await lstat(ancestor.path, { bigint: true });
        if (
          !metadata.isDirectory()
          || metadata.isSymbolicLink()
          || metadata.dev !== ancestor.identity.dev
          || metadata.ino !== ancestor.identity.ino
          || await realpath(ancestor.path) !== ancestor.path
        ) {
          throw new Error("changed");
        }
      } catch {
        return fail(
          "REFRESH_PARITY_TARGET_PARENT_CHANGED",
          "refresh parity artifact parent changed",
          "io",
        );
      }
    }
    try {
      const metadata = await lstat(parent.path, { bigint: true });
      if (
        !metadata.isDirectory()
        || metadata.isSymbolicLink()
        || metadata.dev !== parent.identity.dev
        || metadata.ino !== parent.identity.ino
        || await realpath(parent.path) !== parent.path
      ) {
        throw new Error("changed");
      }
    } catch {
      return fail(
        "REFRESH_PARITY_TARGET_PARENT_CHANGED",
        "refresh parity artifact parent changed",
        "io",
      );
    }
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
  | { readonly kind: "compare_materialized" }
  | { readonly kind: "refresh_prior_imports" }
  | { readonly kind: "candidate_only" };

interface BoundCandidateOnlyArtifactPolicy {
  readonly kind: "candidate_only";
}

interface BoundBootstrapArtifactPolicy {
  readonly kind: "bootstrap_absent";
  readonly importsAbsolute: string;
  readonly movesAbsolute: string;
  readonly initialImportsPhysical: string;
  readonly initialMovesPhysical: string;
  readonly pendingMovesAbsolute: string;
  readonly initialPendingMovesPhysical: string;
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

interface BoundRefreshArtifactPolicy {
  readonly kind: "refresh_prior_imports";
  readonly inputs: BoundZccRefreshInputs;
}

type BoundZccPullArtifactPolicy =
  | BoundBootstrapArtifactPolicy
  | BoundCompareArtifactPolicy
  | BoundRefreshArtifactPolicy
  | BoundCandidateOnlyArtifactPolicy;

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
  const pendingMoves = target.importsPath.endsWith("_imports.tf")
    ? resolvedPath(
        workspace,
        target.importsPath.slice(0, -"_imports.tf".length)
          + "_moves.pending.json",
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
        absolutePath: pendingMoves,
        code: "UNSUPPORTED_PENDING_MOVES",
        message: "pull artifact compilation refuses an in-flight move transition",
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
  readonly priorImportsRead?: StableReadHooks;
}): Promise<BoundZccPullArtifactPolicy> {
  if (options.policy.kind === "candidate_only") {
    return { kind: "candidate_only" };
  }
  const paths = compareArtifactPaths(options.workspace, options.target);
  if (options.policy.kind === "bootstrap_absent") {
    const moves = paths.unsupported.find(
      (artifact) => artifact.code === "UNSUPPORTED_COMPARE_MOVES",
    );
    const pendingMoves = paths.unsupported.find(
      (artifact) => artifact.code === "UNSUPPORTED_PENDING_MOVES",
    );
    if (moves === undefined || pendingMoves === undefined) {
      return fail("INTERNAL_ERROR", "bootstrap move targets are unresolved", "internal");
    }
    const initialImportsPhysical = pythonPosixRealpath(paths.imports);
    const initialMovesPhysical = pythonPosixRealpath(moves.absolutePath);
    const initialPendingMovesPhysical = pythonPosixRealpath(
      pendingMoves.absolutePath,
    );
    await assertAbsent(paths.imports, "imports");
    await assertAbsent(moves.absolutePath, "moves");
    await assertCompareArtifactAbsent(pendingMoves);
    return {
      kind: "bootstrap_absent",
      importsAbsolute: paths.imports,
      movesAbsolute: moves.absolutePath,
      initialImportsPhysical,
      initialMovesPhysical,
      pendingMovesAbsolute: pendingMoves.absolutePath,
      initialPendingMovesPhysical,
    };
  }
  if (options.policy.kind === "refresh_prior_imports") {
    return {
      kind: "refresh_prior_imports",
      inputs: await bindZccRefreshInputs({
        workspace: options.workspace,
        target: options.target,
        ...(options.priorImportsRead === undefined
          ? {}
          : { priorImportsRead: options.priorImportsRead }),
      }),
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
  if (policy.kind === "candidate_only") {
    return;
  }
  if (policy.kind === "bootstrap_absent") {
    if (pythonPosixRealpath(policy.importsAbsolute) !== policy.initialImportsPhysical) {
      return fail("BOOTSTRAP_IMPORTS_CHANGED", "bootstrap import target changed", "io");
    }
    if (pythonPosixRealpath(policy.movesAbsolute) !== policy.initialMovesPhysical) {
      return fail("BOOTSTRAP_MOVES_CHANGED", "bootstrap move target changed", "io");
    }
    if (
      pythonPosixRealpath(policy.pendingMovesAbsolute)
      !== policy.initialPendingMovesPhysical
    ) {
      return fail(
        "BOOTSTRAP_PENDING_MOVES_CHANGED",
        "bootstrap pending-move target changed",
        "io",
      );
    }
    await assertAbsent(policy.importsAbsolute, "imports");
    await assertAbsent(policy.movesAbsolute, "moves");
    try {
      await assertCompareArtifactAbsent({
        absolutePath: policy.pendingMovesAbsolute,
        code: "UNSUPPORTED_PENDING_MOVES",
        message: "pull artifact compilation refuses an in-flight move transition",
      });
    } catch {
      return fail(
        "BOOTSTRAP_PENDING_MOVES_CHANGED",
        "bootstrap pending-move target changed",
        "io",
      );
    }
    return;
  }
  if (policy.kind === "refresh_prior_imports") {
    await recheckZccRefreshInputs(policy.inputs);
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

export function resolveZccArtifactTarget(options: {
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
      "pull artifact compilation supports JSON tfvars only",
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
      "pull artifact compilation does not support same-root generated reference bindings",
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
  const target: ZccArtifactTarget = {
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
  const paths = [
    target.configPath,
    target.importsPath,
    ...(target.lookupPath === null ? [] : [target.lookupPath]),
  ];
  if (paths.some((candidate) => candidate.includes("\0") || !candidate.isWellFormed())) {
    return fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "artifact target path contains unsupported Unicode",
    );
  }
  return target;
}

async function recheckSource(options: {
  readonly canonicalSource: string;
  readonly lexicalSource: string;
  readonly lexicalWorkspace: string;
  readonly canonicalWorkspace: string;
  readonly expected: { readonly sha256: string; readonly size: bigint };
  readonly expectedIdentity?: { readonly dev: bigint; readonly ino: bigint };
  readonly strictParity?: boolean;
}): Promise<void> {
  try {
    const workspace = await canonicalWorkspace(
      options.lexicalWorkspace,
      options.strictParity === true,
    );
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
    const before = await lstat(source, { bigint: true });
    if (
      !before.isFile()
      || (options.strictParity === true && before.isSymbolicLink())
      || (
        options.expectedIdentity !== undefined
        && (
          before.dev !== options.expectedIdentity.dev
          || before.ino !== options.expectedIdentity.ino
        )
      )
    ) {
      return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
    }
    const current = await sha256StableFile(
      source,
      new ReadBudget(PULL_READ_LIMITS),
      { followSymlinks: options.strictParity !== true },
    );
    const after = await lstat(source, { bigint: true });
    if (
      current.sha256 !== options.expected.sha256
      || current.size !== options.expected.size
      || after.dev !== before.dev
      || after.ino !== before.ino
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

export interface ZccPullRefreshCompilationTransaction {
  readonly result: ZccPullRefreshArtifactSet;
  readonly binding: {
    readonly lexicalWorkspace: string;
    readonly canonicalWorkspace: string;
    readonly deploymentPath: string;
    readonly catalogPath: string;
    readonly deployment: Awaited<ReturnType<typeof loadBoundAssessmentDeployment>>["deployment"];
    readonly controls: readonly BoundAssessmentControlFile[];
    readonly target: ZccArtifactTarget;
    readonly source: {
      readonly logicalPath: string;
      readonly canonicalPath: string;
      readonly sha256: string;
      readonly size: bigint;
      readonly identity: { readonly dev: bigint; readonly ino: bigint };
    };
    readonly targetParents: readonly ZccParityTargetParentBinding[];
    readonly baselineInputs: BoundZccRefreshInputs;
  };
  readonly recheckInputs: () => Promise<void>;
}

export interface ZccPullRefreshParityPrepared {
  readonly request: {
    readonly workspace: string;
    readonly deploymentPath: string;
    readonly catalogPath: string;
    readonly tenant: string;
    readonly resourceType: string;
  };
  readonly workspace: string;
  readonly deployment: Awaited<ReturnType<typeof loadBoundAssessmentDeployment>>["deployment"];
  readonly controls: readonly BoundAssessmentControlFile[];
  readonly target: ZccArtifactTarget;
  readonly targetParents: readonly ZccParityTargetParentBinding[];
  readonly source: {
    readonly logicalPath: string;
    readonly lexicalPath: string;
    readonly identity: { readonly dev: bigint; readonly ino: bigint };
  };
}

interface CompiledZccPullTransaction {
  readonly candidate: ZccPullArtifactSet;
  readonly policy: BoundZccPullArtifactPolicy;
  readonly pathBase: string;
  readonly binding: {
    readonly lexicalWorkspace: string;
    readonly canonicalWorkspace: string;
    readonly deploymentPath: string;
    readonly catalogPath: string;
    readonly deployment: Awaited<ReturnType<typeof loadBoundAssessmentDeployment>>["deployment"];
    readonly controls: readonly BoundAssessmentControlFile[];
    readonly target: ZccArtifactTarget;
    readonly source: {
      readonly logicalPath: string;
      readonly canonicalPath: string;
      readonly sha256: string;
      readonly size: bigint;
      readonly identity: { readonly dev: bigint; readonly ino: bigint };
    };
    readonly targetParents: readonly ZccParityTargetParentBinding[];
  };
  readonly recheckInputs: () => Promise<void>;
}

export interface ZccPullMaterializationOperationHooks
  extends ZccPullOperationHooks, ZccPullMaterializationHooks {}

async function compileZccPullArtifactsWithPolicy(
  options: ZccPullArtifactsOperationOptions,
  artifactPolicy: ZccPullArtifactPolicy,
): Promise<CompiledZccPullTransaction> {
  const hooks = options.hooks === undefined
    ? undefined
    : Object.freeze({
        sourceRead: options.hooks.sourceRead === undefined
          ? undefined
          : Object.freeze({ ...options.hooks.sourceRead }),
        priorImportsRead: options.hooks.priorImportsRead === undefined
          ? undefined
          : Object.freeze({ ...options.hooks.priorImportsRead }),
        afterInputsBound: options.hooks.afterInputsBound,
        beforeFinalRecheck: options.hooks.beforeFinalRecheck,
        afterRefreshCompiled: options.hooks.afterRefreshCompiled,
      });
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
  const relativeSource = pythonPosixJoin(
    "pulls",
    request.tenant,
    `${request.resourceType}.json`,
  );
  const lexicalSource = path.resolve(workspace, relativeSource);
  const boundCatalog = await loadBoundAssessmentRootCatalog(request.catalogPath);
  requireSupportedZscalerCompileCatalog(boundCatalog.catalog);
  const boundDeployment = await loadBoundAssessmentDeployment(request.deploymentPath);
  const controls = copyAssessmentControlFiles([
    boundCatalog.file,
    boundDeployment.file,
  ]);
  const target = resolveZccArtifactTarget({
    tenant: request.tenant,
    resourceType: request.resourceType,
    deployment: boundDeployment.deployment,
    catalog: boundCatalog.catalog,
  });
  const targetParents: readonly ZccParityTargetParentBinding[] = [];
  const boundPolicy = await bindArtifactPolicy({
    workspace,
    target,
    policy: artifactPolicy,
    ...(hooks?.priorImportsRead === undefined
      ? {}
      : { priorImportsRead: hooks.priorImportsRead }),
  });

  const canonicalSource = await canonicalPullSource({
    lexicalPath: lexicalSource,
    workspace,
  });
  const source = await readBoundedUtf8File(
    canonicalSource,
    new ReadBudget(PULL_READ_LIMITS),
    {
      ...(hooks?.sourceRead === undefined ? {} : { hooks: hooks.sourceRead }),
    },
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
  await hooks?.afterInputsBound?.();
  let result: ZccPullArtifactSet;
  try {
    const compileCandidate = artifactPolicy.kind === "refresh_prior_imports"
      ? compileZccPullRefreshCandidateArtifactSet
      : compileZccPullArtifactSet;
    result = compileCandidate({
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

  const recheckInputs = async (): Promise<void> => {
    await recheckSource({
      canonicalSource,
      lexicalSource,
      lexicalWorkspace: request.workspace,
      canonicalWorkspace: workspace,
      expected: source.digest,
      expectedIdentity: source.identity,
      strictParity: false,
    });
    try {
      await recheckAssessmentControlFiles(controls);
    } catch {
      return fail("COMPILE_CONTROL_CHANGED", "compile control input changed", "io");
    }
    await recheckArtifactPolicy(boundPolicy);
    await recheckZccParityTargetParents(targetParents);
  };
  await hooks?.beforeFinalRecheck?.();
  await recheckInputs();
  return {
    candidate: result,
    policy: boundPolicy,
    pathBase: workspace,
    binding: {
      lexicalWorkspace: request.workspace,
      canonicalWorkspace: workspace,
      deploymentPath: request.deploymentPath,
      catalogPath: request.catalogPath,
      deployment: boundDeployment.deployment,
      controls,
      target,
      source: {
        logicalPath: relativeSource,
        canonicalPath: canonicalSource,
        sha256: source.digest.sha256,
        size: source.digest.size,
        identity: source.identity,
      },
      targetParents,
    },
    recheckInputs,
  };
}

/** Strictly prepare both parity sides before either baseline or raw pull is read. */
export async function prepareZccPullRefreshParity(options: {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tenant: string;
  readonly resourceType: string;
  readonly requireMaterializedBaseline?: boolean;
}): Promise<ZccPullRefreshParityPrepared> {
  const request = Object.freeze({
    workspace: options.workspace,
    deploymentPath: options.deploymentPath,
    catalogPath: options.catalogPath,
    tenant: options.tenant,
    resourceType: options.resourceType,
  });
  if (!path.isAbsolute(request.workspace)) {
    return fail("INVALID_WORKSPACE", "context.workspace must be an absolute path");
  }
  validateTenant(request.tenant);
  const workspace = await canonicalWorkspace(request.workspace, true);
  const relativeSource = pythonPosixJoin(
    "pulls",
    request.tenant,
    `${request.resourceType}.json`,
  );
  const lexicalSource = path.resolve(workspace, relativeSource);
  await preflightZccParityInputPath({
    filePath: request.catalogPath,
    workspace,
    required: true,
  });
  await preflightZccParityInputPath({
    filePath: request.deploymentPath,
    workspace,
    required: false,
  });
  await preflightZccParityInputPath({
    filePath: lexicalSource,
    workspace,
    required: true,
  });
  const sourceMetadata = await lstat(lexicalSource, { bigint: true });
  const boundCatalog = await loadBoundAssessmentRootCatalog(
    request.catalogPath,
    { followSymlinks: false },
  );
  requireSupportedZscalerCompileCatalog(boundCatalog.catalog);
  const boundDeployment = await loadBoundAssessmentDeployment(
    request.deploymentPath,
    { followSymlinks: false },
  );
  const controls = copyAssessmentControlFiles([
    boundCatalog.file,
    boundDeployment.file,
  ]);
  const target = resolveZccArtifactTarget({
    tenant: request.tenant,
    resourceType: request.resourceType,
    deployment: boundDeployment.deployment,
    catalog: boundCatalog.catalog,
  });
  const targetParents = await preflightZccParityArtifactTarget({
    workspace,
    deployment: boundDeployment.deployment,
    target,
    ...(options.requireMaterializedBaseline === undefined
      ? {}
      : { requireMaterializedBaseline: options.requireMaterializedBaseline }),
  });
  return Object.freeze({
    request,
    workspace,
    deployment: boundDeployment.deployment,
    controls,
    target,
    targetParents,
    source: {
      logicalPath: relativeSource,
      lexicalPath: lexicalSource,
      identity: { dev: sourceMetadata.dev, ino: sourceMetadata.ino },
    },
  });
}

async function compilePreparedZccPullRefreshCandidate(options: {
  readonly prepared: ZccPullRefreshParityPrepared;
  readonly hooks?: ZccPullOperationHooks;
}): Promise<CompiledZccPullTransaction> {
  const hooks = options.hooks === undefined
    ? undefined
    : Object.freeze({
        sourceRead: options.hooks.sourceRead === undefined
          ? undefined
          : Object.freeze({ ...options.hooks.sourceRead }),
        priorImportsRead: options.hooks.priorImportsRead === undefined
          ? undefined
          : Object.freeze({ ...options.hooks.priorImportsRead }),
        afterInputsBound: options.hooks.afterInputsBound,
        beforeFinalRecheck: options.hooks.beforeFinalRecheck,
        afterRefreshCompiled: options.hooks.afterRefreshCompiled,
      });
  const prepared = options.prepared;
  const boundPolicy = await bindArtifactPolicy({
    workspace: prepared.workspace,
    target: prepared.target,
    policy: { kind: "refresh_prior_imports" },
    ...(hooks?.priorImportsRead === undefined
      ? {}
      : { priorImportsRead: hooks.priorImportsRead }),
  });
  const source = await readBoundedUtf8File(
    prepared.source.lexicalPath,
    new ReadBudget(PULL_READ_LIMITS),
    {
      followSymlinks: false,
      ...(hooks?.sourceRead === undefined ? {} : { hooks: hooks.sourceRead }),
    },
  );
  if (
    source.identity.dev !== prepared.source.identity.dev
    || source.identity.ino !== prepared.source.identity.ino
  ) {
    return fail("RAW_PULL_CHANGED", "raw pull changed during compilation", "io");
  }
  let rawItems: readonly unknown[];
  try {
    rawItems = parseZccPullDataJson(source.text);
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("INVALID_PULL_DATA", "raw pull is not supported JSON item data");
  }
  await hooks?.afterInputsBound?.();
  let candidate: ZccPullArtifactSet;
  try {
    candidate = compileZccPullRefreshCandidateArtifactSet({
      catalog: loadZccTransformCatalog(),
      catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
      rawItems,
      target: prepared.target,
      source: {
        path: prepared.source.logicalPath,
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
  const recheckInputs = async (): Promise<void> => {
    await recheckSource({
      canonicalSource: prepared.source.lexicalPath,
      lexicalSource: prepared.source.lexicalPath,
      lexicalWorkspace: prepared.request.workspace,
      canonicalWorkspace: prepared.workspace,
      expected: source.digest,
      expectedIdentity: source.identity,
      strictParity: true,
    });
    try {
      await recheckAssessmentControlFiles(prepared.controls);
    } catch {
      return fail("COMPILE_CONTROL_CHANGED", "compile control input changed", "io");
    }
    await recheckArtifactPolicy(boundPolicy);
    await recheckZccParityTargetParents(prepared.targetParents);
  };
  await hooks?.beforeFinalRecheck?.();
  await recheckInputs();
  return {
    candidate,
    policy: boundPolicy,
    pathBase: prepared.workspace,
    binding: {
      lexicalWorkspace: prepared.request.workspace,
      canonicalWorkspace: prepared.workspace,
      deploymentPath: prepared.request.deploymentPath,
      catalogPath: prepared.request.catalogPath,
      deployment: prepared.deployment,
      controls: prepared.controls,
      target: prepared.target,
      source: {
        logicalPath: prepared.source.logicalPath,
        canonicalPath: prepared.source.lexicalPath,
        sha256: source.digest.sha256,
        size: source.digest.size,
        identity: source.identity,
      },
      targetParents: prepared.targetParents,
    },
    recheckInputs,
  };
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

/** Compile one read-only raw-transform refresh transition from prior imports. */
export async function compileZccPullRefreshArtifactsOperation(
  options: ZccPullArtifactsOperationOptions,
): Promise<ZccPullRefreshArtifactSet> {
  return (await compileZccPullRefreshArtifactsTransaction(options)).result;
}

/** Internal transaction seam used by the two-workspace parity protocol. */
export async function compileZccPullRefreshArtifactsTransaction(
  options: ZccPullArtifactsOperationOptions,
): Promise<ZccPullRefreshCompilationTransaction> {
  const afterRefreshCompiled = options.hooks?.afterRefreshCompiled;
  const result = await compileZccPullArtifactsWithPolicy(
    options,
    { kind: "refresh_prior_imports" },
  );
  return finishZccPullRefreshTransaction(result, afterRefreshCompiled);
}

/** Compile from a strict preparation shared with the opposite parity twin. */
export async function compilePreparedZccPullRefreshArtifactsTransaction(
  prepared: ZccPullRefreshParityPrepared,
  hooks?: ZccPullOperationHooks,
): Promise<ZccPullRefreshCompilationTransaction> {
  const result = await compilePreparedZccPullRefreshCandidate({
    prepared,
    ...(hooks === undefined ? {} : { hooks }),
  });
  return finishZccPullRefreshTransaction(result, hooks?.afterRefreshCompiled);
}

async function finishZccPullRefreshTransaction(
  result: CompiledZccPullTransaction,
  afterRefreshCompiled?: () => void | Promise<void>,
): Promise<ZccPullRefreshCompilationTransaction> {
  if (result.policy.kind !== "refresh_prior_imports") {
    return fail("INTERNAL_ERROR", "refresh artifact policy is unresolved", "internal");
  }
  const inputs = result.policy.inputs;
  const lookupBaseline = "digest" in inputs.lookup
    ? zccRefreshBaselineInput(inputs.lookup)
    : { path: inputs.lookup.logicalPath, state: "absent" as const };
  const refresh = compileZccPullRefreshArtifactSet({
    candidate: result.candidate,
    baselineImports: {
      path: inputs.imports.logicalPath,
      content: inputs.imports.text,
    },
    baselineTfvars: zccRefreshBaselineInput(inputs.tfvars),
    baselineLookup: lookupBaseline,
    movesPath: inputs.moves.logicalPath,
    pendingMovesPath: inputs.pendingMoves.logicalPath,
    alternateHclPath: inputs.alternateHcl.logicalPath,
    generatedBindingsPath: inputs.generatedBindings.logicalPath,
  });
  await afterRefreshCompiled?.();
  await result.recheckInputs();
  return {
    result: refresh,
    binding: {
      ...result.binding,
      baselineInputs: inputs,
    },
    recheckInputs: result.recheckInputs,
  };
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

/** Recompile, bind, and publish one independently asserted ready bootstrap set. */
export async function materializeZccPullArtifactsOperation(options: {
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tenant: string;
  readonly resourceType: string;
  readonly assertion: ZccPullArtifactParity;
  readonly outputRoot: string;
  readonly hooks?: ZccPullMaterializationOperationHooks;
}): Promise<ZccPullArtifactMaterialization> {
  if (!validateZccPullArtifactParity(options.assertion)) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_ASSERTION",
      category: "domain",
      message: "the parity assertion failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactParity.errors),
    });
  }
  const hooks = options.hooks === undefined
    ? undefined
    : Object.freeze({ ...options.hooks });
  const request = {
    workspace: options.workspace,
    deploymentPath: options.deploymentPath,
    catalogPath: options.catalogPath,
    tenant: options.tenant,
    resourceType: options.resourceType,
    ...(hooks === undefined ? {} : { hooks }),
  };
  const assertion = structuredClone(options.assertion);
  const outputRoot = options.outputRoot;
  const result = await compileZccPullArtifactsWithPolicy(
    request,
    { kind: "candidate_only" },
  );
  return materializeReadyZccPullArtifacts({
    outputRoot,
    pathBase: result.pathBase,
    candidate: result.candidate,
    assertion,
    recheckInputs: result.recheckInputs,
    ...(hooks === undefined ? {} : { hooks }),
  });
}
