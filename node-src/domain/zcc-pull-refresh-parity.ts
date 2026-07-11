import { lstat, realpath, stat } from "node:fs/promises";
import path from "node:path";

import {
  validateZccPullRefreshParity,
  validateZccPullRefreshParitySeed,
} from "../contracts/validators.js";
import {
  ReadBudget,
  readBoundedFileBytes,
  sha256StableFile,
  type BoundedReadLimits,
} from "../io/bounded-files.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  recheckAssessmentControlFiles,
} from "./control-evidence.js";
import { ProcessFailure } from "./errors.js";
import { pythonPosixRealpath } from "./paths.js";
import {
  compilePreparedZccPullRefreshArtifactsTransaction,
  prepareZccPullRefreshParity,
  recheckZccParityTargetParents,
  type ZccPullRefreshParityPrepared,
  type ZccPullRefreshCompilationTransaction,
  type ZccPullOperationHooks,
} from "./zcc-pull-operation.js";
import type {
  ZccPullResourceType,
} from "./zcc-pull-artifacts.js";
import {
  zccRefreshEvidenceDigest,
  zccPullRefreshParityRequestSha,
} from "./zcc-pull-refresh-fingerprints.js";
import type {
  ZccPullRefreshArtifactSet,
  ZccRefreshBaselineState,
  ZccRefreshDesiredArtifact,
} from "./zcc-pull-refresh.js";

const MAX_SEED_BYTES = 256 * 1024;
const MAX_DIRECT_JSON_DEPTH = 128;
const MAX_DIRECT_JSON_NODES = 50_000;
const MAX_DIRECT_JSON_ARRAY_ITEMS = 50_000;
const MAX_DIRECT_JSON_APPROX_BYTES = 512 * 1024;
const REFERENCE_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 7,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 128n * 1024n * 1024n,
  maxFileBytes: 32n * 1024n * 1024n,
};

export interface ZccPullRefreshParityContext {
  readonly workspace: string;
  readonly deployment: string;
  readonly root_catalog: string;
}

export type ZccPullRefreshParityEvidenceState =
  | { readonly state: "absent" }
  | {
      readonly state: "present";
      readonly sha256: string;
      readonly size_bytes: number;
    };

export type ZccPullRefreshParityDesiredState =
  | { readonly state: "absent" }
  | {
      readonly state: "present";
      readonly media_type: string;
      readonly encoding: "utf-8";
      readonly sha256: string;
      readonly size_bytes: number;
    };

export interface ZccPullRefreshNeutralEvidence {
  readonly source: {
    readonly sha256: string;
    readonly size_bytes: number;
  };
  readonly catalog: ZccPullRefreshArtifactSet["catalog"];
  readonly root: ZccPullRefreshArtifactSet["root"];
  readonly baseline: {
    readonly tfvars: ZccPullRefreshParityEvidenceState;
    readonly imports: ZccPullRefreshParityEvidenceState;
    readonly lookup: ZccPullRefreshParityEvidenceState;
    readonly moves: ZccPullRefreshParityEvidenceState;
    readonly pending_moves: ZccPullRefreshParityEvidenceState;
    readonly alternate_hcl: ZccPullRefreshParityEvidenceState;
    readonly generated_bindings: ZccPullRefreshParityEvidenceState;
  };
  readonly desired: {
    readonly tfvars: ZccPullRefreshParityDesiredState;
    readonly imports: ZccPullRefreshParityDesiredState;
    readonly lookup: ZccPullRefreshParityDesiredState;
    readonly moves: ZccPullRefreshParityDesiredState;
    readonly pending_moves: { readonly state: "absent" };
    readonly alternate_hcl: { readonly state: "absent" };
    readonly generated_bindings: { readonly state: "absent" };
  };
  readonly status: "ready" | "review_required";
  readonly unexpected_drops: readonly string[];
  readonly moves: {
    readonly safe_count: number;
    readonly suppressed_count: number;
  };
  readonly decision_sha256: string;
  readonly evidence_sha256: string;
}

export interface ZccPullRefreshParityBindingEvidence {
  readonly request_sha256: string;
  readonly binding_sha256: string;
  readonly deployment_semantics_sha256: string;
  readonly controls: {
    readonly deployment: ZccPullRefreshParityEvidenceState;
    readonly root_catalog: Exclude<ZccPullRefreshParityEvidenceState, { state: "absent" }>;
  };
}

export interface ZccPullRefreshParitySeed {
  readonly kind: "infrawright.zcc_pull_refresh_parity_seed";
  readonly schema_version: 1;
  readonly mode: "refresh";
  readonly reference: "materialized_twin";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly bindings: {
    readonly candidate: ZccPullRefreshParityBindingEvidence;
    readonly reference_twin: ZccPullRefreshParityBindingEvidence;
  };
  readonly candidate: ZccPullRefreshNeutralEvidence & {
    readonly baseline_fingerprint_sha256: string;
    readonly transition_sha256: string;
  };
  readonly reference_twin: ZccPullRefreshNeutralEvidence;
  readonly differences: readonly string[];
  readonly status: "ready" | "review_required";
  readonly seed_sha256: string;
}

export interface ZccPullRefreshParityEntry {
  readonly expected: ZccPullRefreshParityDesiredState;
  readonly observed: ZccPullRefreshParityEvidenceState;
  readonly status: "match" | "mismatch" | "missing" | "unexpected";
}

export interface ZccPullRefreshParity {
  readonly kind: "infrawright.zcc_pull_refresh_parity";
  readonly schema_version: 1;
  readonly mode: "refresh";
  readonly reference: "materialized_twin";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly seed: ZccPullRefreshParitySeed;
  readonly candidate: ZccPullRefreshParitySeed["candidate"];
  readonly parity: {
    readonly status: "equal" | "different";
    readonly matched: number;
    readonly mismatched: number;
    readonly missing: number;
    readonly unexpected: number;
    readonly artifacts: {
      readonly tfvars: ZccPullRefreshParityEntry;
      readonly imports: ZccPullRefreshParityEntry;
      readonly lookup: ZccPullRefreshParityEntry;
      readonly moves: ZccPullRefreshParityEntry;
      readonly pending_moves: ZccPullRefreshParityEntry;
      readonly alternate_hcl: ZccPullRefreshParityEntry;
      readonly generated_bindings: ZccPullRefreshParityEntry;
    };
  };
  readonly status: "ready" | "review_required";
  readonly assertion_sha256: string;
}

export interface ZccPullRefreshParityHooks {
  readonly candidate?: ZccPullOperationHooks;
  readonly reference?: ZccPullOperationHooks;
  readonly afterReferenceBound?: () => void | Promise<void>;
  readonly beforeCandidateFinalCas?: () => void | Promise<void>;
  readonly beforeReferenceFinalCas?: () => void | Promise<void>;
}

interface PrimitiveRequest {
  readonly candidate: ZccPullRefreshParityContext;
  readonly reference: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
}

interface FileIdentity {
  readonly dev: bigint;
  readonly ino: bigint;
}

interface BoundReferenceArtifact {
  readonly logicalName: ArtifactRole;
  readonly absolutePath: string;
  readonly physicalPath: string;
  readonly identity: FileIdentity | null;
  readonly evidence: ZccPullRefreshParityEvidenceState;
}

type ArtifactRole =
  | "tfvars"
  | "imports"
  | "lookup"
  | "moves"
  | "pending_moves"
  | "alternate_hcl"
  | "generated_bindings";

const ARTIFACT_ROLES = [
  "tfvars",
  "imports",
  "lookup",
  "moves",
  "pending_moves",
  "alternate_hcl",
  "generated_bindings",
] as const satisfies readonly ArtifactRole[];

function fail(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "domain",
): never {
  throw new ProcessFailure({ code, category, message });
}

function ownValue(value: Readonly<Record<string, unknown>>, key: string): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity input must be inert data");
  }
  return descriptor.value;
}

function optionalOwnValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) {
    return undefined;
  }
  if (!("value" in descriptor)) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity input must be inert data");
  }
  return descriptor.value;
}

function inertRecord(value: unknown): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity input must be an object");
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity input must be plain data");
  }
  return value as Readonly<Record<string, unknown>>;
}

function primitiveString(value: unknown): string {
  if (
    typeof value !== "string"
    || !value.isWellFormed()
    || value.includes("\0")
    || Buffer.byteLength(value, "utf8") > 4096
  ) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity strings are invalid");
  }
  return value;
}

function contextSnapshot(value: unknown): ZccPullRefreshParityContext {
  const record = inertRecord(value);
  return Object.freeze({
    workspace: primitiveString(ownValue(record, "workspace")),
    deployment: primitiveString(ownValue(record, "deployment")),
    root_catalog: primitiveString(ownValue(record, "root_catalog")),
  });
}

function snapshotRequest(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly referenceContext: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
}): PrimitiveRequest {
  const input = inertRecord(options as unknown);
  const resourceType = primitiveString(ownValue(input, "resourceType"));
  if (![
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ].includes(resourceType)) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "unsupported ZCC parity resource");
  }
  return Object.freeze({
    candidate: contextSnapshot(ownValue(input, "context")),
    reference: contextSnapshot(ownValue(input, "referenceContext")),
    tenant: primitiveString(ownValue(input, "tenant")),
    resourceType: resourceType as ZccPullResourceType,
  });
}

function optionalHook(
  value: Readonly<Record<string, unknown>>,
  key: string,
): (() => void | Promise<void>) | undefined {
  const candidate = optionalOwnValue(value, key);
  if (candidate === undefined) {
    return undefined;
  }
  if (typeof candidate !== "function") {
    return fail("INVALID_REFRESH_PARITY_INPUT", "refresh parity hooks must be functions");
  }
  return candidate as () => void | Promise<void>;
}

function stableReadHooksSnapshot(value: unknown): ZccPullOperationHooks["sourceRead"] {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const afterOpen = optionalHook(hooks, "afterOpen");
  const beforeFinalStat = optionalHook(hooks, "beforeFinalStat");
  return Object.freeze({
    ...(afterOpen === undefined ? {} : { afterOpen }),
    ...(beforeFinalStat === undefined ? {} : { beforeFinalStat }),
  });
}

function pullHooksSnapshot(value: unknown): ZccPullOperationHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const sourceRead = stableReadHooksSnapshot(optionalOwnValue(hooks, "sourceRead"));
  const priorImportsRead = stableReadHooksSnapshot(
    optionalOwnValue(hooks, "priorImportsRead"),
  );
  const afterInputsBound = optionalHook(hooks, "afterInputsBound");
  const beforeFinalRecheck = optionalHook(hooks, "beforeFinalRecheck");
  const afterRefreshCompiled = optionalHook(hooks, "afterRefreshCompiled");
  return Object.freeze({
    ...(sourceRead === undefined ? {} : { sourceRead }),
    ...(priorImportsRead === undefined ? {} : { priorImportsRead }),
    ...(afterInputsBound === undefined ? {} : { afterInputsBound }),
    ...(beforeFinalRecheck === undefined ? {} : { beforeFinalRecheck }),
    ...(afterRefreshCompiled === undefined ? {} : { afterRefreshCompiled }),
  });
}

function parityHooksSnapshot(value: unknown): ZccPullRefreshParityHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  const hooks = inertRecord(value);
  const candidate = pullHooksSnapshot(optionalOwnValue(hooks, "candidate"));
  const reference = pullHooksSnapshot(optionalOwnValue(hooks, "reference"));
  const afterReferenceBound = optionalHook(hooks, "afterReferenceBound");
  const beforeCandidateFinalCas = optionalHook(hooks, "beforeCandidateFinalCas");
  const beforeReferenceFinalCas = optionalHook(hooks, "beforeReferenceFinalCas");
  return Object.freeze({
    ...(candidate === undefined ? {} : { candidate }),
    ...(reference === undefined ? {} : { reference }),
    ...(afterReferenceBound === undefined ? {} : { afterReferenceBound }),
    ...(beforeCandidateFinalCas === undefined ? {} : { beforeCandidateFinalCas }),
    ...(beforeReferenceFinalCas === undefined ? {} : { beforeReferenceFinalCas }),
  });
}

class DirectJsonBudget {
  private nodes = 0;
  private arrayItems = 0;
  private approximateBytes = 0;

  node(value: unknown): void {
    this.nodes += 1;
    if (this.nodes > MAX_DIRECT_JSON_NODES) {
      fail("INVALID_REFRESH_PARITY_SEED", "parity seed exceeds the node budget");
    }
    if (typeof value === "string") {
      this.bytes(value);
    }
  }

  array(length: number): void {
    this.arrayItems += length;
    if (this.arrayItems > MAX_DIRECT_JSON_ARRAY_ITEMS) {
      fail("INVALID_REFRESH_PARITY_SEED", "parity seed exceeds the array budget");
    }
  }

  bytes(value: string): void {
    this.approximateBytes += Buffer.byteLength(value, "utf8");
    if (this.approximateBytes > MAX_DIRECT_JSON_APPROX_BYTES) {
      fail("INVALID_REFRESH_PARITY_SEED", "parity seed exceeds the string budget");
    }
  }
}

function snapshotInertJson(
  value: unknown,
  depth = 1,
  ancestors: Set<object> = new Set<object>(),
  budget: DirectJsonBudget = new DirectJsonBudget(),
): unknown {
  budget.node(value);
  if (
    value === null
    || typeof value === "boolean"
    || typeof value === "string"
    || typeof value === "number"
  ) {
    if (typeof value === "string" && !value.isWellFormed()) {
      return fail("INVALID_REFRESH_PARITY_SEED", "parity seed contains invalid Unicode");
    }
    if (typeof value === "number" && (!Number.isSafeInteger(value) || Object.is(value, -0))) {
      return fail("INVALID_REFRESH_PARITY_SEED", "parity seed contains invalid numbers");
    }
    return value;
  }
  if (typeof value !== "object" || value === null || depth > MAX_DIRECT_JSON_DEPTH) {
    return fail("INVALID_REFRESH_PARITY_SEED", "parity seed is not bounded JSON");
  }
  if (ancestors.has(value)) {
    return fail("INVALID_REFRESH_PARITY_SEED", "parity seed must be acyclic JSON");
  }
  ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      budget.array(value.length);
      return Array.from({ length: value.length }, (_, index) => {
        const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
        if (descriptor === undefined || !("value" in descriptor)) {
          return fail("INVALID_REFRESH_PARITY_SEED", "parity seed must be inert JSON");
        }
        return snapshotInertJson(descriptor.value, depth + 1, ancestors, budget);
      });
    }
    const record = inertRecord(value);
    const keys = Object.keys(record);
    budget.array(keys.length);
    const output: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    for (const key of keys) {
      if (!key.isWellFormed()) {
        return fail("INVALID_REFRESH_PARITY_SEED", "parity seed contains invalid Unicode");
      }
      budget.bytes(key);
      output[key] = snapshotInertJson(
        ownValue(record, key),
        depth + 1,
        ancestors,
        budget,
      );
    }
    return output;
  } finally {
    ancestors.delete(value);
  }
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((item) => immutableCopy(item)));
  }
  if (typeof value === "object" && value !== null) {
    const output: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy((value as Readonly<Record<string, unknown>>)[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

function pathFreeBaseline(value: ZccRefreshBaselineState): ZccPullRefreshParityEvidenceState {
  return value.state === "absent"
    ? { state: "absent" }
    : { state: "present", sha256: value.sha256, size_bytes: value.size_bytes };
}

function pathFreeDesired(value: ZccRefreshDesiredArtifact): ZccPullRefreshParityDesiredState {
  if (value.state === "absent") {
    return { state: "absent" };
  }
  return {
    state: "present",
    media_type: value.artifact.media_type,
    encoding: value.artifact.encoding,
    sha256: value.artifact.sha256,
    size_bytes: value.artifact.size_bytes,
  };
}

function decisionProjection(refresh: ZccPullRefreshArtifactSet): unknown {
  return {
    kind: "infrawright.zcc_pull_refresh_path_neutral_decision",
    schema_version: 1,
    product: refresh.product,
    resource_type: refresh.resource_type,
    tenant: refresh.tenant,
    source: {
      sha256: refresh.source.sha256,
      size_bytes: refresh.source.size_bytes,
    },
    catalog: refresh.catalog,
    root: refresh.root,
    baseline: {
      tfvars: pathFreeBaseline(refresh.baseline.tfvars),
      imports: pathFreeBaseline(refresh.baseline.imports),
      lookup: pathFreeBaseline(refresh.baseline.lookup),
      moves: pathFreeBaseline(refresh.baseline.moves),
      pending_moves: pathFreeBaseline(refresh.baseline.pending_moves),
      alternate_hcl: pathFreeBaseline(refresh.baseline.alternate_hcl),
      generated_bindings: pathFreeBaseline(refresh.baseline.generated_bindings),
    },
    status: refresh.status,
    unexpected_drops: refresh.unexpected_drops,
    moves: refresh.moves,
    desired: {
      tfvars: pathFreeDesired(refresh.desired.tfvars),
      imports: pathFreeDesired(refresh.desired.imports),
      lookup: pathFreeDesired(refresh.desired.lookup),
      moves: pathFreeDesired(refresh.desired.moves),
      pending_moves: { state: "absent" },
      alternate_hcl: { state: "absent" },
      generated_bindings: { state: "absent" },
    },
  };
}

export function zccPullRefreshNeutralEvidence(
  refresh: ZccPullRefreshArtifactSet,
): ZccPullRefreshNeutralEvidence {
  const projection = decisionProjection(refresh) as {
    readonly source: ZccPullRefreshNeutralEvidence["source"];
    readonly catalog: ZccPullRefreshNeutralEvidence["catalog"];
    readonly root: ZccPullRefreshNeutralEvidence["root"];
    readonly baseline: ZccPullRefreshNeutralEvidence["baseline"];
    readonly desired: ZccPullRefreshNeutralEvidence["desired"];
  };
  const decisionSha = zccRefreshEvidenceDigest(projection);
  const evidenceWithoutDigest = {
    source: projection.source,
    catalog: projection.catalog,
    root: projection.root,
    baseline: projection.baseline,
    desired: projection.desired,
    status: refresh.status,
    unexpected_drops: [...refresh.unexpected_drops],
    moves: {
      safe_count: refresh.moves.safe.length,
      suppressed_count: refresh.moves.suppressed.length,
    },
    decision_sha256: decisionSha,
  };
  return {
    ...evidenceWithoutDigest,
    evidence_sha256: zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_path_neutral_evidence",
      schema_version: 1,
      ...evidenceWithoutDigest,
    }),
  };
}

function controlEvidence(
  control: ZccPullRefreshCompilationTransaction["binding"]["controls"][number],
): ZccPullRefreshParityEvidenceState {
  return control.digest === null
    ? { state: "absent" }
    : {
        state: "present",
        sha256: control.digest.sha256,
        size_bytes: Number(control.digest.size),
      };
}

function containedPath(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === "" || (
    relative !== ".."
    && !relative.startsWith(`..${path.sep}`)
    && !path.isAbsolute(relative)
  );
}

function regionsOverlap(left: string, right: string): boolean {
  return containedPath(left, right) || containedPath(right, left);
}

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate) ? candidate : path.resolve(workspace, candidate);
}

function targetPaths(
  workspace: string,
  target: ZccPullRefreshCompilationTransaction["binding"]["target"],
): Readonly<Record<ArtifactRole, string>> {
  const imports = resolveContextPath(workspace, target.importsPath);
  const config = resolveContextPath(workspace, target.configPath);
  const lookupLogical = target.lookupPath ?? `${path.posix.dirname(target.configPath)}`
    + `/${target.resourceType}.lookup.json`;
  return {
    tfvars: config,
    imports,
    lookup: resolveContextPath(workspace, lookupLogical),
    moves: imports.slice(0, -"_imports.tf".length) + "_moves.tf",
    pending_moves: imports.slice(0, -"_imports.tf".length) + "_moves.pending.json",
    alternate_hcl: config.slice(0, -".json".length),
    generated_bindings: resolveContextPath(
      workspace,
      `${path.posix.dirname(target.configPath)}/${target.resourceType}.generated.expressions.json`,
    ),
  };
}

interface TransactionSnapshot {
  readonly request: ZccPullRefreshParityBindingEvidence;
  readonly workspace: string;
  readonly authority: string;
  readonly workspaceIdentity: FileIdentity;
  readonly authorityIdentity: FileIdentity;
  readonly targetPaths: Readonly<Record<ArtifactRole, string>>;
  readonly presentArtifactIdentities: readonly FileIdentity[];
}

interface PhysicalIsolationSnapshot {
  readonly workspace: string;
  readonly authority: string;
  readonly workspaceIdentity: FileIdentity;
  readonly authorityIdentity: FileIdentity;
  readonly presentArtifactIdentities: readonly FileIdentity[];
}

export type ZccPullRefreshParitySnapshotBinding = Omit<
  ZccPullRefreshCompilationTransaction["binding"],
  "baselineInputs"
> & {
  readonly baselineInputs?: ZccPullRefreshCompilationTransaction["binding"]["baselineInputs"];
};

type SnapshotBinding = ZccPullRefreshParitySnapshotBinding;

async function directoryIdentity(directory: string, code: string): Promise<FileIdentity> {
  try {
    const metadata = await lstat(directory, { bigint: true });
    if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
      return fail(code, "refresh parity authorities must be canonical directories");
    }
    return { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(code, "refresh parity authority could not be bound");
  }
}

async function ancestorIdentityChain(directory: string): Promise<readonly unknown[]> {
  const parsed = path.parse(directory);
  const relative = path.relative(parsed.root, directory);
  const components = relative === "" ? [] : relative.split(path.sep);
  const chain: unknown[] = [];
  let current = parsed.root;
  for (const component of ["", ...components]) {
    if (component !== "") {
      current = path.join(current, component);
    }
    const metadata = await lstat(current, { bigint: true }).catch(() => null);
    if (metadata === null || !metadata.isDirectory() || metadata.isSymbolicLink()) {
      return fail(
        "REFRESH_PARITY_BINDING_CHANGED",
        "refresh parity directory ancestry changed",
        "io",
      );
    }
    chain.push({
      physical_path: current,
      dev: metadata.dev.toString(),
      ino: metadata.ino.toString(),
    });
  }
  return chain;
}

async function transactionSnapshot(
  primitive: PrimitiveRequest,
  side: "candidate" | "reference",
  binding: SnapshotBinding,
): Promise<TransactionSnapshot> {
  const requested = side === "candidate" ? primitive.candidate : primitive.reference;
  const lexicalWorkspace = path.resolve(requested.workspace);
  if (
    !path.isAbsolute(requested.workspace)
    || lexicalWorkspace !== requested.workspace
    || binding.canonicalWorkspace !== requested.workspace
    || requested.workspace === path.parse(requested.workspace).root
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity workspaces must be canonical, non-root absolute directories",
    );
  }
  const workspaceIdentity = await directoryIdentity(
    binding.canonicalWorkspace,
    "INVALID_REFRESH_PARITY_ISOLATION",
  );
  const overlay = binding.deployment.overlay;
  if (typeof overlay !== "string") {
    return fail("INVALID_REFRESH_PARITY_ISOLATION", "deployment overlay must be a string");
  }
  const lexicalAuthority = overlay === "."
    ? binding.canonicalWorkspace
    : resolveContextPath(binding.canonicalWorkspace, overlay);
  let authority: string;
  try {
    authority = await realpath(lexicalAuthority);
    const metadata = await stat(authority);
    if (!metadata.isDirectory()) {
      return fail("INVALID_REFRESH_PARITY_ISOLATION", "artifact authority is not a directory");
    }
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("INVALID_REFRESH_PARITY_ISOLATION", "artifact authority is missing");
  }
  if (
    authority !== lexicalAuthority
    || authority === path.parse(authority).root
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "artifact authorities must be canonical, non-root directories",
    );
  }
  if (
    authority !== binding.canonicalWorkspace
    && regionsOverlap(authority, binding.canonicalWorkspace)
    && !containedPath(authority, binding.canonicalWorkspace)
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "an artifact authority must not contain its own workspace",
    );
  }
  const authorityIdentity = await directoryIdentity(
    authority,
    "INVALID_REFRESH_PARITY_ISOLATION",
  );
  const deploymentControl = binding.controls[1];
  const catalogControl = binding.controls[0];
  if (deploymentControl === undefined || catalogControl === undefined) {
    return fail("INTERNAL_ERROR", "refresh compile controls are incomplete", "internal");
  }
  const controls = [
    [binding.deploymentPath, deploymentControl],
    [binding.catalogPath, catalogControl],
  ] as const;
  const controlIdentities = new Map<string, unknown>();
  for (const [controlPath, control] of controls) {
    const lexical = path.resolve(controlPath);
    const physical = control.digest === null
      ? pythonPosixRealpath(lexical)
      : await realpath(lexical).catch(() => "");
    if (
      physical !== lexical
      || !containedPath(physical, binding.canonicalWorkspace)
    ) {
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity controls must resolve inside their own workspace",
      );
    }
    if (control.digest !== null) {
      const identity = await lstat(physical, { bigint: true }).catch(() => null);
      if (identity === null || !identity.isFile() || identity.isSymbolicLink()) {
        return fail("REFRESH_PARITY_CONTROL_CHANGED", "refresh parity control changed", "io");
      }
      controlIdentities.set(control.path, {
        dev: identity.dev.toString(),
        ino: identity.ino.toString(),
      });
    } else {
      controlIdentities.set(control.path, null);
    }
  }
  const paths = targetPaths(binding.canonicalWorkspace, binding.target);
  const physicalTargets = await Promise.all(ARTIFACT_ROLES.map(async (role) => {
    const lexicalParent = path.dirname(paths[role]);
    const physicalParent = await realpath(lexicalParent).catch(() => "");
    if (physicalParent !== lexicalParent) {
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity artifact parents must be canonical directories",
      );
    }
    return path.join(physicalParent, path.basename(paths[role]));
  }));
  if (
    new Set(physicalTargets).size !== physicalTargets.length
    || physicalTargets.some((candidate) => !containedPath(candidate, authority))
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity artifact targets must be distinct beneath their authority",
    );
  }
  const sourceMetadata = await lstat(binding.source.canonicalPath, { bigint: true }).catch(
    () => null,
  );
  if (
    sourceMetadata === null
    || !sourceMetadata.isFile()
    || sourceMetadata.isSymbolicLink()
    || !containedPath(binding.source.canonicalPath, binding.canonicalWorkspace)
    || binding.source.canonicalPath !== path.resolve(
      binding.canonicalWorkspace,
      binding.source.logicalPath,
    )
  ) {
    return fail("REFRESH_PARITY_SOURCE_CHANGED", "refresh parity source changed", "io");
  }
  const currentTargetParents = [];
  for (const parent of binding.targetParents) {
    const parentMetadata = await lstat(parent.path, { bigint: true }).catch(() => null);
    if (
      parentMetadata === null
      || !parentMetadata.isDirectory()
      || parentMetadata.isSymbolicLink()
      || await realpath(parent.path).catch(() => "") !== parent.path
    ) {
      return fail(
        "REFRESH_PARITY_BINDING_CHANGED",
        "refresh parity target binding changed",
        "io",
      );
    }
    const ancestors = [];
    for (const ancestor of parent.ancestors) {
      const metadata = await lstat(ancestor.path, { bigint: true }).catch(() => null);
      if (
        metadata === null
        || !metadata.isDirectory()
        || metadata.isSymbolicLink()
        || await realpath(ancestor.path).catch(() => "") !== ancestor.path
      ) {
        return fail(
          "REFRESH_PARITY_BINDING_CHANGED",
          "refresh parity target binding changed",
          "io",
        );
      }
      ancestors.push({
        physical_path: ancestor.path,
        dev: metadata.dev.toString(),
        ino: metadata.ino.toString(),
      });
    }
    currentTargetParents.push({
      physical_path: parent.path,
      dev: parentMetadata.dev.toString(),
      ino: parentMetadata.ino.toString(),
      ancestors,
    });
  }
  const bindingProjection = {
    kind: "infrawright.zcc_pull_refresh_parity_binding",
    schema_version: 1,
    canonical_workspace: binding.canonicalWorkspace,
    workspace_identity: {
      dev: workspaceIdentity.dev.toString(),
      ino: workspaceIdentity.ino.toString(),
    },
    workspace_ancestors: await ancestorIdentityChain(binding.canonicalWorkspace),
    artifact_authority: authority,
    authority_identity: {
      dev: authorityIdentity.dev.toString(),
      ino: authorityIdentity.ino.toString(),
    },
    authority_ancestors: await ancestorIdentityChain(authority),
    controls: binding.controls.map((control) => ({
      physical_path: pythonPosixRealpath(control.path),
      identity: controlIdentities.get(control.path),
      evidence: controlEvidence(control),
    })),
    source: {
      physical_path: binding.source.canonicalPath,
      dev: sourceMetadata.dev.toString(),
      ino: sourceMetadata.ino.toString(),
      sha256: binding.source.sha256,
      size_bytes: Number(binding.source.size),
    },
    targets: ARTIFACT_ROLES.map((role) => ({
      role,
      physical_path: physicalTargets[ARTIFACT_ROLES.indexOf(role)],
    })),
    target_parents: currentTargetParents,
  };
  const presentArtifactIdentities: FileIdentity[] = [];
  const inputs = binding.baselineInputs;
  if (inputs !== undefined) {
    for (const item of [inputs.tfvars, inputs.imports, inputs.lookup]) {
      if ("identity" in item && item.identity !== null) {
        presentArtifactIdentities.push({ ...item.identity });
      }
    }
  }
  return {
    request: {
      request_sha256: zccPullRefreshParityRequestSha({
        context: requested,
        tenant: primitive.tenant,
        resourceType: primitive.resourceType,
      }),
      binding_sha256: zccRefreshEvidenceDigest(bindingProjection),
      deployment_semantics_sha256: zccRefreshEvidenceDigest({
        kind: "infrawright.zcc_pull_refresh_deployment_semantics",
        schema_version: 1,
        deployment: {
          ...binding.deployment,
          overlay: "<artifact_authority>",
        },
      }),
      controls: {
        deployment: controlEvidence(deploymentControl),
        root_catalog: controlEvidence(catalogControl) as Exclude<
          ZccPullRefreshParityEvidenceState,
          { state: "absent" }
        >,
      },
    },
    workspace: binding.canonicalWorkspace,
    authority,
    workspaceIdentity,
    authorityIdentity,
    targetPaths: paths,
    presentArtifactIdentities,
  };
}

/**
 * Recompute the content-free binding evidence used by a refresh parity
 * assertion. This deliberately excludes materialized artifact bytes so it
 * remains stable while an asserted transition is durably published.
 */
export async function snapshotZccPullRefreshBindingEvidence(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly binding: ZccPullRefreshParitySnapshotBinding;
}): Promise<ZccPullRefreshParityBindingEvidence> {
  const input = inertRecord(options as unknown);
  const context = contextSnapshot(ownValue(input, "context"));
  const tenant = primitiveString(ownValue(input, "tenant"));
  const rawResourceType = primitiveString(ownValue(input, "resourceType"));
  if (![
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ].includes(rawResourceType)) {
    return fail("INVALID_REFRESH_PARITY_INPUT", "unsupported ZCC parity resource");
  }
  const resourceType = rawResourceType as ZccPullResourceType;
  const primitive: PrimitiveRequest = {
    candidate: context,
    reference: context,
    tenant,
    resourceType,
  };
  const binding = ownValue(input, "binding") as ZccPullRefreshParitySnapshotBinding;
  return (await transactionSnapshot(
    primitive,
    "candidate",
    binding,
  )).request;
}

function identityKey(identity: FileIdentity): string {
  return `${identity.dev.toString()}:${identity.ino.toString()}`;
}

function assertIsolated(
  candidate: PhysicalIsolationSnapshot,
  reference: PhysicalIsolationSnapshot,
): void {
  for (const left of [candidate.workspace, candidate.authority]) {
    for (const right of [reference.workspace, reference.authority]) {
      if (regionsOverlap(left, right)) {
        return fail(
          "INVALID_REFRESH_PARITY_ISOLATION",
          "candidate and reference physical regions must not overlap",
        );
      }
    }
  }
  const candidateRegions = [
    candidate.workspaceIdentity,
    candidate.authorityIdentity,
  ].map(identityKey);
  const referenceRegions = new Set([
    reference.workspaceIdentity,
    reference.authorityIdentity,
  ].map(identityKey));
  if (candidateRegions.some((identity) => referenceRegions.has(identity))) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "candidate and reference regions must not share directory identities",
    );
  }
  const candidateIdentities = new Set(candidate.presentArtifactIdentities.map(identityKey));
  if (reference.presentArtifactIdentities.some(
    (identity) => candidateIdentities.has(identityKey(identity)),
  )) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "candidate and reference artifacts must not share hard-link identities",
    );
  }
}

async function preparedIsolationSnapshot(
  prepared: ZccPullRefreshParityPrepared,
  requireMaterializedBaseline = true,
): Promise<PhysicalIsolationSnapshot> {
  const workspaceIdentity = await directoryIdentity(
    prepared.workspace,
    "INVALID_REFRESH_PARITY_ISOLATION",
  );
  const overlay = prepared.deployment.overlay;
  if (typeof overlay !== "string") {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "refresh parity artifact authority is invalid",
    );
  }
  const authority = overlay === "."
    ? prepared.workspace
    : resolveContextPath(prepared.workspace, overlay);
  const authorityIdentity = await directoryIdentity(
    authority,
    "INVALID_REFRESH_PARITY_ISOLATION",
  );
  const paths = targetPaths(prepared.workspace, prepared.target);
  const presentArtifactIdentities: FileIdentity[] = [];
  for (const role of (requireMaterializedBaseline ? [
    "tfvars",
    "imports",
    ...(prepared.target.lookupPath === null ? [] : ["lookup" as const]),
  ] as const : [])) {
    const metadata = await lstat(paths[role], { bigint: true }).catch(() => null);
    if (
      metadata === null
      || !metadata.isFile()
      || metadata.isSymbolicLink()
    ) {
      return fail(
        "INVALID_REFRESH_PARITY_ISOLATION",
        "refresh parity requires a fully materialized run-one baseline",
      );
    }
    presentArtifactIdentities.push({ dev: metadata.dev, ino: metadata.ino });
  }
  return {
    workspace: prepared.workspace,
    authority,
    workspaceIdentity,
    authorityIdentity,
    presentArtifactIdentities,
  };
}

function sameJson(left: unknown, right: unknown): boolean {
  return zccRefreshEvidenceDigest(left) === zccRefreshEvidenceDigest(right);
}

function differences(
  candidate: ZccPullRefreshNeutralEvidence,
  reference: ZccPullRefreshNeutralEvidence,
): string[] {
  const output: string[] = [];
  for (const name of ["source", "catalog", "root", "status", "unexpected_drops"] as const) {
    if (!sameJson(candidate[name], reference[name])) {
      output.push(name);
    }
  }
  for (const role of ARTIFACT_ROLES) {
    if (!sameJson(candidate.baseline[role], reference.baseline[role])) {
      output.push(`baseline.${role}`);
    }
    if (!sameJson(candidate.desired[role], reference.desired[role])) {
      output.push(`desired.${role}`);
    }
  }
  if (candidate.moves.safe_count !== reference.moves.safe_count) {
    output.push("moves.safe_count");
  }
  if (candidate.moves.suppressed_count !== reference.moves.suppressed_count) {
    output.push("moves.suppressed_count");
  }
  if (candidate.decision_sha256 !== reference.decision_sha256) {
    output.push("decision_sha256");
  }
  return sortedStrings(new Set(output));
}

function candidateEvidence(refresh: ZccPullRefreshArtifactSet): ZccPullRefreshParitySeed["candidate"] {
  return {
    ...zccPullRefreshNeutralEvidence(refresh),
    baseline_fingerprint_sha256: refresh.baseline.fingerprint_sha256,
    transition_sha256: refresh.transition_sha256,
  };
}

function seedDigest(value: Omit<ZccPullRefreshParitySeed, "seed_sha256">): string {
  return zccRefreshEvidenceDigest({
    kind: "infrawright.zcc_pull_refresh_parity_seed_digest",
    schema_version: 1,
    seed: value,
  });
}

function assertionDigest(value: Omit<ZccPullRefreshParity, "assertion_sha256">): string {
  return zccRefreshEvidenceDigest({
    kind: "infrawright.zcc_pull_refresh_parity_assertion_digest",
    schema_version: 1,
    assertion: value,
  });
}

function operationOptions(
  context: ZccPullRefreshParityContext,
  primitive: PrimitiveRequest,
) {
  return {
    workspace: context.workspace,
    deploymentPath: resolveContextPath(context.workspace, context.deployment),
    catalogPath: resolveContextPath(context.workspace, context.root_catalog),
    tenant: primitive.tenant,
    resourceType: primitive.resourceType,
  };
}

async function exactSnapshot(
  primitive: PrimitiveRequest,
  side: "candidate" | "reference",
  transaction: ZccPullRefreshCompilationTransaction,
  expected: TransactionSnapshot,
  beforeFinalCas?: () => void | Promise<void>,
): Promise<TransactionSnapshot> {
  const current = await transactionSnapshot(primitive, side, transaction.binding);
  if (!sameJson(current.request, expected.request)) {
    return fail(
      "REFRESH_PARITY_INPUT_CHANGED",
      "refresh parity transaction inputs changed",
      "io",
    );
  }
  await beforeFinalCas?.();
  await transaction.recheckInputs();
  return current;
}

/** Seed two isolated, still-materialized refresh twins before Python runs. */
export async function seedZccPullRefreshParityOperation(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly referenceContext: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly hooks?: ZccPullRefreshParityHooks;
}): Promise<ZccPullRefreshParitySeed> {
  const inputRecord = inertRecord(options as unknown);
  const primitive = snapshotRequest(options);
  const hooks = parityHooksSnapshot(optionalOwnValue(inputRecord, "hooks"));
  const candidatePrepared = await prepareZccPullRefreshParity(
    operationOptions(primitive.candidate, primitive),
  );
  const referencePrepared = await prepareZccPullRefreshParity(
    operationOptions(primitive.reference, primitive),
  );
  const candidatePreRead = await preparedIsolationSnapshot(candidatePrepared);
  const referencePreRead = await preparedIsolationSnapshot(referencePrepared);
  assertIsolated(candidatePreRead, referencePreRead);
  const candidateTransaction = await compilePreparedZccPullRefreshArtifactsTransaction(
    candidatePrepared,
    hooks?.candidate,
  );
  const candidateSnapshot = await transactionSnapshot(
    primitive,
    "candidate",
    candidateTransaction.binding,
  );
  const referenceTransaction = await compilePreparedZccPullRefreshArtifactsTransaction(
    referencePrepared,
    hooks?.reference,
  );
  const referenceSnapshot = await transactionSnapshot(
    primitive,
    "reference",
    referenceTransaction.binding,
  );
  assertIsolated(candidateSnapshot, referenceSnapshot);
  const finalReferenceSnapshot = await exactSnapshot(
    primitive,
    "reference",
    referenceTransaction,
    referenceSnapshot,
    hooks?.beforeReferenceFinalCas,
  );
  const finalCandidateSnapshot = await exactSnapshot(
    primitive,
    "candidate",
    candidateTransaction,
    candidateSnapshot,
    hooks?.beforeCandidateFinalCas,
  );
  assertIsolated(finalCandidateSnapshot, finalReferenceSnapshot);

  const candidate = candidateEvidence(candidateTransaction.result);
  const referenceTwin = zccPullRefreshNeutralEvidence(referenceTransaction.result);
  const delta = differences(candidate, referenceTwin);
  if (
    finalCandidateSnapshot.request.deployment_semantics_sha256
    !== finalReferenceSnapshot.request.deployment_semantics_sha256
  ) {
    delta.push("controls.deployment");
  }
  if (!sameJson(
    finalCandidateSnapshot.request.controls.root_catalog,
    finalReferenceSnapshot.request.controls.root_catalog,
  )) {
    delta.push("controls.root_catalog");
  }
  const sortedDelta = sortedStrings(new Set(delta));
  const status = candidate.status === "ready"
      && referenceTwin.status === "ready"
      && candidate.moves.suppressed_count === 0
      && referenceTwin.moves.suppressed_count === 0
      && sortedDelta.length === 0
    ? "ready" as const
    : "review_required" as const;
  const withoutDigest = {
    kind: "infrawright.zcc_pull_refresh_parity_seed" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    reference: "materialized_twin" as const,
    product: "zcc" as const,
    resource_type: primitive.resourceType,
    tenant: primitive.tenant,
    bindings: {
      candidate: finalCandidateSnapshot.request,
      reference_twin: finalReferenceSnapshot.request,
    },
    candidate,
    reference_twin: referenceTwin,
    differences: sortedDelta,
    status,
  };
  const seed: ZccPullRefreshParitySeed = {
    ...withoutDigest,
    seed_sha256: seedDigest(withoutDigest),
  };
  if (Buffer.byteLength(JSON.stringify(seed), "utf8") > MAX_SEED_BYTES) {
    return fail("REFRESH_PARITY_SEED_TOO_LARGE", "refresh parity seed exceeds 256 KiB");
  }
  if (!validateZccPullRefreshParitySeed(seed)) {
    return fail("INVALID_OPERATION_RESULT", "refresh parity seed failed its contract", "internal");
  }
  return immutableCopy(seed) as ZccPullRefreshParitySeed;
}

function errorCode(error: unknown): string | null {
  return typeof error === "object" && error !== null && "code" in error
      && typeof error.code === "string"
    ? error.code
    : null;
}

async function bindReferenceArtifact(
  role: ArtifactRole,
  absolutePath: string,
  budget: ReadBudget,
): Promise<BoundReferenceArtifact> {
  const physicalPath = pythonPosixRealpath(absolutePath);
  let identity: FileIdentity;
  try {
    const metadata = await lstat(absolutePath, { bigint: true });
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      return fail(
        "REFRESH_PARITY_REFERENCE_NOT_REGULAR",
        "reference artifacts must be regular non-symlink files",
        "io",
      );
    }
    identity = { dev: metadata.dev, ino: metadata.ino };
  } catch (error: unknown) {
    if (errorCode(error) === "ENOENT") {
      return {
        logicalName: role,
        absolutePath,
        physicalPath,
        identity: null,
        evidence: { state: "absent" },
      };
    }
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("REFRESH_PARITY_REFERENCE_READ_FAILED", "reference artifact read failed", "io");
  }
  try {
    const digest = await sha256StableFile(absolutePath, budget);
    const current = await lstat(absolutePath, { bigint: true });
    if (
      !current.isFile()
      || current.isSymbolicLink()
      || current.dev !== identity.dev
      || current.ino !== identity.ino
      || pythonPosixRealpath(absolutePath) !== physicalPath
    ) {
      return fail("REFRESH_PARITY_REFERENCE_CHANGED", "reference artifact changed", "io");
    }
    return {
      logicalName: role,
      absolutePath,
      physicalPath,
      identity,
      evidence: {
        state: "present",
        sha256: digest.sha256,
        size_bytes: Number(digest.size),
      },
    };
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail("REFRESH_PARITY_REFERENCE_READ_FAILED", "reference artifact read failed", "io");
  }
}

async function recheckReferenceArtifacts(
  artifacts: readonly BoundReferenceArtifact[],
): Promise<void> {
  const budget = new ReadBudget(REFERENCE_READ_LIMITS);
  for (const artifact of artifacts) {
    if (pythonPosixRealpath(artifact.absolutePath) !== artifact.physicalPath) {
      return fail("REFRESH_PARITY_REFERENCE_CHANGED", "reference artifact changed", "io");
    }
    if (artifact.identity === null) {
      try {
        await lstat(artifact.absolutePath);
      } catch (error: unknown) {
        if (errorCode(error) === "ENOENT") {
          continue;
        }
      }
      return fail("REFRESH_PARITY_REFERENCE_CHANGED", "reference artifact changed", "io");
    }
    const current = await bindReferenceArtifact(
      artifact.logicalName,
      artifact.absolutePath,
      budget,
    );
    if (
      current.identity === null
      || current.identity.dev !== artifact.identity.dev
      || current.identity.ino !== artifact.identity.ino
      || !sameJson(current.evidence, artifact.evidence)
    ) {
      return fail("REFRESH_PARITY_REFERENCE_CHANGED", "reference artifact changed", "io");
    }
  }
}

async function bindPostPythonReference(options: {
  readonly primitive: PrimitiveRequest;
  readonly seed: ZccPullRefreshParitySeed;
  readonly prepared: ZccPullRefreshParityPrepared;
}): Promise<{
  readonly snapshot: TransactionSnapshot;
  readonly artifacts: readonly BoundReferenceArtifact[];
  readonly binding: SnapshotBinding;
}> {
  const prepared = options.prepared;
  const source = await readBoundedFileBytes(
    prepared.source.lexicalPath,
    new ReadBudget({
      ...REFERENCE_READ_LIMITS,
      maxFiles: 1,
      maxTotalBytes: 4n * 1024n * 1024n,
      maxFileBytes: 4n * 1024n * 1024n,
    }),
    { followSymlinks: false },
  );
  if (
    source.identity.dev !== prepared.source.identity.dev
    || source.identity.ino !== prepared.source.identity.ino
  ) {
    return fail("REFRESH_PARITY_SOURCE_CHANGED", "refresh parity source changed", "io");
  }
  const postPythonBinding: SnapshotBinding = {
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
  };
  const snapshot = await transactionSnapshot(
    options.primitive,
    "reference",
    postPythonBinding,
  );
  if (!sameJson(snapshot.request, options.seed.bindings.reference_twin)) {
    return fail("REFRESH_PARITY_SEED_STALE", "reference binding no longer matches the seed");
  }
  const budget = new ReadBudget(REFERENCE_READ_LIMITS);
  const artifacts: BoundReferenceArtifact[] = [];
  const commitOrderedRoles = [
    ...ARTIFACT_ROLES.filter((role) => role !== "imports"),
    "imports" as const,
  ];
  for (const role of commitOrderedRoles) {
    artifacts.push(await bindReferenceArtifact(role, snapshot.targetPaths[role], budget));
  }
  return { snapshot, artifacts, binding: postPythonBinding };
}

async function finalReferenceCas(options: {
  readonly primitive: PrimitiveRequest;
  readonly expected: TransactionSnapshot;
  readonly binding: SnapshotBinding;
  readonly artifacts: readonly BoundReferenceArtifact[];
  readonly beforeFinalCas?: () => void | Promise<void>;
}): Promise<TransactionSnapshot> {
  const current = await transactionSnapshot(
    options.primitive,
    "reference",
    options.binding,
  );
  if (!sameJson(current.request, options.expected.request)) {
    return fail(
      "REFRESH_PARITY_REFERENCE_CHANGED",
      "reference binding changed",
      "io",
    );
  }
  await options.beforeFinalCas?.();
  try {
    await recheckAssessmentControlFiles(options.binding.controls);
  } catch {
    return fail(
      "REFRESH_PARITY_REFERENCE_CHANGED",
      "reference control changed",
      "io",
    );
  }
  const source = await readBoundedFileBytes(
    options.binding.source.canonicalPath,
    new ReadBudget({
      ...REFERENCE_READ_LIMITS,
      maxFiles: 1,
      maxTotalBytes: 4n * 1024n * 1024n,
      maxFileBytes: 4n * 1024n * 1024n,
    }),
    { followSymlinks: false },
  ).catch(() => null);
  if (
    source === null
    || source.identity.dev !== options.binding.source.identity.dev
    || source.identity.ino !== options.binding.source.identity.ino
    || source.digest.sha256 !== options.binding.source.sha256
    || source.digest.size !== options.binding.source.size
  ) {
    return fail("REFRESH_PARITY_SOURCE_CHANGED", "refresh parity source changed", "io");
  }
  await recheckZccParityTargetParents(options.binding.targetParents);
  // Imports is the commit marker and is deliberately rechecked last.
  await recheckReferenceArtifacts(options.artifacts);
  return current;
}

function parityEntry(
  expected: ZccPullRefreshParityDesiredState,
  observed: ZccPullRefreshParityEvidenceState,
): ZccPullRefreshParityEntry {
  let status: ZccPullRefreshParityEntry["status"];
  if (expected.state === "absent") {
    status = observed.state === "absent" ? "match" : "unexpected";
  } else if (observed.state === "absent") {
    status = "missing";
  } else {
    status = expected.sha256 === observed.sha256
        && expected.size_bytes === observed.size_bytes
      ? "match"
      : "mismatch";
  }
  return { expected, observed, status };
}

/** Assert post-Python bytes against one complete ready two-twin seed. */
export async function compareZccPullRefreshParityOperation(options: {
  readonly context: ZccPullRefreshParityContext;
  readonly referenceContext: ZccPullRefreshParityContext;
  readonly tenant: string;
  readonly resourceType: ZccPullResourceType;
  readonly seed: ZccPullRefreshParitySeed;
  readonly hooks?: ZccPullRefreshParityHooks;
}): Promise<ZccPullRefreshParity> {
  const inputRecord = inertRecord(options as unknown);
  const primitive = snapshotRequest(options);
  const hooks = parityHooksSnapshot(optionalOwnValue(inputRecord, "hooks"));
  const copiedSeed = snapshotInertJson(
    ownValue(inputRecord, "seed"),
  ) as ZccPullRefreshParitySeed;
  if (
    Buffer.byteLength(JSON.stringify(copiedSeed), "utf8") > MAX_SEED_BYTES
    || !validateZccPullRefreshParitySeed(copiedSeed)
    || copiedSeed.status !== "ready"
  ) {
    return fail(
      "INVALID_REFRESH_PARITY_SEED",
      "refresh comparison requires one complete ready seed",
    );
  }
  if (
    copiedSeed.tenant !== primitive.tenant
    || copiedSeed.resource_type !== primitive.resourceType
    || copiedSeed.bindings.candidate.request_sha256
      !== zccPullRefreshParityRequestSha({
        context: primitive.candidate,
        tenant: primitive.tenant,
        resourceType: primitive.resourceType,
      })
    || copiedSeed.bindings.reference_twin.request_sha256
      !== zccPullRefreshParityRequestSha({
        context: primitive.reference,
        tenant: primitive.tenant,
        resourceType: primitive.resourceType,
      })
    || copiedSeed.seed_sha256 !== seedDigest((() => {
      const { seed_sha256: _ignored, ...withoutDigest } = copiedSeed;
      return withoutDigest;
    })())
  ) {
    return fail("INVALID_REFRESH_PARITY_SEED", "refresh parity seed join failed");
  }

  const candidatePrepared = await prepareZccPullRefreshParity(
    operationOptions(primitive.candidate, primitive),
  );
  const referencePrepared = await prepareZccPullRefreshParity({
    ...operationOptions(primitive.reference, primitive),
    requireMaterializedBaseline: false,
  });
  const candidatePreRead = await preparedIsolationSnapshot(candidatePrepared);
  const referencePreRead = await preparedIsolationSnapshot(referencePrepared, false);
  assertIsolated(candidatePreRead, referencePreRead);

  const candidateTransaction = await compilePreparedZccPullRefreshArtifactsTransaction(
    candidatePrepared,
    hooks?.candidate,
  );
  const candidateSnapshot = await transactionSnapshot(
    primitive,
    "candidate",
    candidateTransaction.binding,
  );
  const candidate = candidateEvidence(candidateTransaction.result);
  if (
    !sameJson(candidateSnapshot.request, copiedSeed.bindings.candidate)
    || !sameJson(candidate, copiedSeed.candidate)
  ) {
    return fail("REFRESH_PARITY_SEED_STALE", "candidate transition no longer matches the seed");
  }

  const reference = await bindPostPythonReference({
    primitive,
    seed: copiedSeed,
    prepared: referencePrepared,
  });
  assertIsolated(candidateSnapshot, reference.snapshot);
  const candidateIdentitySet = new Set(
    candidateSnapshot.presentArtifactIdentities.map(identityKey),
  );
  if (reference.artifacts.some(
    (artifact) => artifact.identity !== null
      && candidateIdentitySet.has(identityKey(artifact.identity)),
  )) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "candidate and reference artifacts must not share hard-link identities",
    );
  }
  await hooks?.afterReferenceBound?.();
  const finalReferenceSnapshot = await finalReferenceCas({
    primitive,
    expected: reference.snapshot,
    binding: reference.binding,
    artifacts: reference.artifacts,
    ...(hooks?.beforeReferenceFinalCas === undefined
      ? {}
      : { beforeFinalCas: hooks.beforeReferenceFinalCas }),
  });
  assertIsolated(candidateSnapshot, finalReferenceSnapshot);
  if (reference.artifacts.some(
    (artifact) => artifact.identity !== null
      && candidateIdentitySet.has(identityKey(artifact.identity)),
  )) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "candidate and reference artifacts must not share hard-link identities",
    );
  }
  const finalCandidateSnapshot = await exactSnapshot(
    primitive,
    "candidate",
    candidateTransaction,
    candidateSnapshot,
    hooks?.beforeCandidateFinalCas,
  );
  assertIsolated(finalCandidateSnapshot, finalReferenceSnapshot);
  if (reference.artifacts.some(
    (artifact) => artifact.identity !== null
      && new Set(finalCandidateSnapshot.presentArtifactIdentities.map(identityKey))
        .has(identityKey(artifact.identity)),
  )) {
    return fail(
      "INVALID_REFRESH_PARITY_ISOLATION",
      "candidate and reference artifacts must not share hard-link identities",
    );
  }

  const observed = Object.fromEntries(
    reference.artifacts.map((artifact) => [artifact.logicalName, artifact.evidence]),
  ) as Readonly<Record<ArtifactRole, ZccPullRefreshParityEvidenceState>>;
  const artifacts = {
    tfvars: parityEntry(candidate.desired.tfvars, observed.tfvars),
    imports: parityEntry(candidate.desired.imports, observed.imports),
    lookup: parityEntry(candidate.desired.lookup, observed.lookup),
    moves: parityEntry(candidate.desired.moves, observed.moves),
    pending_moves: parityEntry(candidate.desired.pending_moves, observed.pending_moves),
    alternate_hcl: parityEntry(candidate.desired.alternate_hcl, observed.alternate_hcl),
    generated_bindings: parityEntry(
      candidate.desired.generated_bindings,
      observed.generated_bindings,
    ),
  };
  const values = Object.values(artifacts);
  const matched = values.filter((entry) => entry.status === "match").length;
  const mismatched = values.filter((entry) => entry.status === "mismatch").length;
  const missing = values.filter((entry) => entry.status === "missing").length;
  const unexpected = values.filter((entry) => entry.status === "unexpected").length;
  const parityStatus = matched === ARTIFACT_ROLES.length ? "equal" as const : "different" as const;
  const status = candidate.status === "ready" && parityStatus === "equal"
    ? "ready" as const
    : "review_required" as const;
  const withoutDigest = {
    kind: "infrawright.zcc_pull_refresh_parity" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    reference: "materialized_twin" as const,
    product: "zcc" as const,
    resource_type: primitive.resourceType,
    tenant: primitive.tenant,
    seed: copiedSeed,
    candidate,
    parity: {
      status: parityStatus,
      matched,
      mismatched,
      missing,
      unexpected,
      artifacts,
    },
    status,
  };
  const result: ZccPullRefreshParity = {
    ...withoutDigest,
    assertion_sha256: assertionDigest(withoutDigest),
  };
  if (!validateZccPullRefreshParity(result)) {
    return fail(
      "INVALID_OPERATION_RESULT",
      "refresh parity assertion failed its contract",
      "internal",
    );
  }
  return immutableCopy(result) as ZccPullRefreshParity;
}
