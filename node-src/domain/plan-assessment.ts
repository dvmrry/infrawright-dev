import { chmod, lstat, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  copyAssessmentControlFiles,
  recheckAssessmentControlFiles,
  type BoundAssessmentControlFile,
} from "./control-evidence.js";
import {
  copySavedPlanAssessmentContext,
  recheckSavedPlanAssessmentContext,
  type SavedPlanAssessmentContext,
} from "./plan-assessment-inputs.js";
import type { StalePolicyEntry } from "./drift-policy.js";
import {
  cleanupSavedPlanEvidence,
  prepareSavedPlanEvidence,
  recheckSavedPlanEvidence,
  type SavedPlanEvidence,
} from "./plan-evidence.js";
import {
  BLOCKED,
  CLEAN,
  classifyPlan,
  TOLERATED,
  type PlanFinding,
  type PlanStatus,
} from "./plan-eval.js";
import { AssessmentPlanError } from "./plan-contract.js";
import {
  DriftPolicyLoadFailure,
  loadBoundDriftPolicy,
  recheckBoundDriftPolicy,
} from "./plan-policy.js";
import type { PlanFingerprintV2 } from "./plan-fingerprint.js";
import {
  buildSavedPlanAssessmentErrorReport,
  buildSavedPlanAssessmentReport,
  type AssessmentMode,
  type AssessmentReportRequest,
  type SavedPlanAssessmentReport,
} from "./plan-report.js";
import {
  DEFAULT_BOUNDED_READ_LIMITS,
  ReadBudget,
  type BoundedReadLimits,
} from "../io/bounded-files.js";
import {
  DEFAULT_TERRAFORM_SHOW_LIMITS,
  terraformShowPlan,
  type TerraformShowLimits,
} from "../io/terraform-show.js";
import { isJsonRecord } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";

export interface SavedPlanAssessmentRootInput {
  readonly tenant: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly envDir: string;
  readonly savedPlanPath: string;
  readonly fingerprintPath: string;
  readonly varFiles: readonly string[];
}

export interface SavedPlanAssessmentOptions {
  readonly terraformExecutable: string;
  readonly roots: readonly SavedPlanAssessmentRootInput[];
  readonly backendConfig: string | null;
  readonly policyPath: string | null;
  readonly controlFiles?: readonly BoundAssessmentControlFile[];
  readonly context?: SavedPlanAssessmentContext;
  readonly sourceLimits?: BoundedReadLimits;
  readonly savedPlanLimits?: BoundedReadLimits;
  readonly policyLimits?: BoundedReadLimits;
  readonly terraformShowLimits?: TerraformShowLimits;
  readonly operationTimeoutMs?: number;
  readonly maxRetainedSnapshotBytes?: bigint;
  readonly resultLimits?: SavedPlanAssessmentResultLimits;
}

export interface SavedPlanAssessmentResultLimits {
  readonly maxFindings: number;
  readonly maxPaths: number;
  readonly maxMetadataBytes: number;
}

export interface AssessmentFinding extends PlanFinding {
  readonly resource_type: string | null;
}

export interface AssessedSavedPlanRoot {
  readonly tenant: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly status: PlanStatus;
  readonly plan: {
    readonly sha256: string;
    readonly format_version: string | null;
    readonly terraform_version: string | null;
  };
  readonly plan_fingerprint: PlanFingerprintV2;
  readonly findings: readonly AssessmentFinding[];
}

export interface SavedPlanAssessmentCore {
  readonly status: PlanStatus;
  readonly checked: number;
  readonly clean: number;
  readonly tolerated: number;
  readonly blocked: number;
  readonly policy_sha256: string | null;
  readonly roots: readonly AssessedSavedPlanRoot[];
  readonly stale_policy: readonly StalePolicyEntry[];
}

export type SavedPlanAssessmentErrorKind =
  | "assessment_error"
  | "no_saved_plans"
  | "policy_error";

/** A safe failure plus the reportable roots completed before it occurred. */
export class SavedPlanAssessmentFailure extends ProcessFailure {
  readonly reportKind: SavedPlanAssessmentErrorKind;
  readonly partial: SavedPlanAssessmentCore;

  constructor(options: {
    readonly failure: ProcessFailure;
    readonly reportKind: SavedPlanAssessmentErrorKind;
    readonly partial: SavedPlanAssessmentCore;
  }) {
    super({
      code: options.failure.code,
      category: options.failure.category,
      message: options.failure.message,
      retryable: options.failure.retryable,
      details: options.failure.details,
    });
    this.name = "SavedPlanAssessmentFailure";
    this.reportKind = options.reportKind;
    this.partial = options.partial;
  }
}

export const MAX_SAVED_PLAN_ASSESSMENT_ROOTS = 1_000;
export const MAX_RETAINED_PLAN_SNAPSHOT_BYTES = 2n * 1024n * 1024n * 1024n;
export const MAX_SAVED_PLAN_ASSESSMENT_TIMEOUT_MS = 60 * 60 * 1_000;
export const MAX_SAVED_PLAN_ASSESSMENT_FINDINGS = 100_000;
export const MAX_SAVED_PLAN_ASSESSMENT_PATHS = 250_000;
export const MAX_SAVED_PLAN_ASSESSMENT_METADATA_BYTES = 8 * 1024 * 1024;
const DEFAULT_SAVED_PLAN_ASSESSMENT_TIMEOUT_MS = 10 * 60 * 1_000;
const DEFAULT_ASSESSMENT_RESULT_LIMITS: SavedPlanAssessmentResultLimits = {
  maxFindings: MAX_SAVED_PLAN_ASSESSMENT_FINDINGS,
  maxPaths: MAX_SAVED_PLAN_ASSESSMENT_PATHS,
  maxMetadataBytes: MAX_SAVED_PLAN_ASSESSMENT_METADATA_BYTES,
};

const DEFAULT_SAVED_PLAN_LIMITS: BoundedReadLimits = {
  maxFiles: 16,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 2n * 1024n * 1024n * 1024n,
  maxFileBytes: 512n * 1024n * 1024n,
};

const DEFAULT_POLICY_LIMITS: BoundedReadLimits = {
  maxFiles: 2,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: 32n * 1024n * 1024n,
  maxFileBytes: 16n * 1024n * 1024n,
};

const TENANT = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;
const ROOT_LABEL = /^[a-z0-9_]+$/;
const RESOURCE_TYPE = /^[A-Za-z_][A-Za-z0-9_]*$/;

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function metadata(plan: Record<string, unknown>, field: string): string | null {
  const value = plan[field];
  return typeof value === "string" ? value : null;
}

function validateRootInputs(roots: readonly SavedPlanAssessmentRootInput[]): void {
  if (roots.length > MAX_SAVED_PLAN_ASSESSMENT_ROOTS) {
    fail(
      "TOO_MANY_SAVED_PLANS",
      "saved-plan assessment exceeds the root-count limit",
    );
  }
  const seen = new Set<string>();
  for (const root of roots) {
    if (
      !TENANT.test(root.tenant)
      || !ROOT_LABEL.test(root.label)
      || root.members.length === 0
      || root.members.some((member) => !RESOURCE_TYPE.test(member))
      || new Set(root.members).size !== root.members.length
      || !path.isAbsolute(root.envDir)
      || !path.isAbsolute(root.savedPlanPath)
      || !path.isAbsolute(root.fingerprintPath)
      || root.varFiles.some((file) => !path.isAbsolute(file))
    ) {
      fail("INVALID_ASSESSMENT_ROOT", "saved-plan root input is invalid");
    }
    const key = `${root.tenant}\0${root.label}`;
    if (seen.has(key)) {
      fail("DUPLICATE_ASSESSMENT_ROOT", "saved-plan root was selected more than once");
    }
    seen.add(key);
  }
}

function copyReadLimits(limits: BoundedReadLimits): BoundedReadLimits {
  return {
    maxFiles: limits.maxFiles,
    maxDirectories: limits.maxDirectories,
    maxDirectoryEntries: limits.maxDirectoryEntries,
    maxDepth: limits.maxDepth,
    maxTotalBytes: limits.maxTotalBytes,
    maxFileBytes: limits.maxFileBytes,
  };
}

function limitsWithin(
  limits: BoundedReadLimits,
  maximum: BoundedReadLimits,
): boolean {
  return Number.isSafeInteger(limits.maxFiles)
    && limits.maxFiles > 0
    && Number.isSafeInteger(limits.maxDirectories)
    && limits.maxDirectories > 0
    && Number.isSafeInteger(limits.maxDirectoryEntries)
    && limits.maxDirectoryEntries > 0
    && Number.isSafeInteger(limits.maxDepth)
    && limits.maxDepth >= 0
    && limits.maxTotalBytes > 0n
    && limits.maxFileBytes > 0n
    && limits.maxFiles <= maximum.maxFiles
    && limits.maxDirectories <= maximum.maxDirectories
    && limits.maxDirectoryEntries <= maximum.maxDirectoryEntries
    && limits.maxDepth <= maximum.maxDepth
    && limits.maxTotalBytes <= maximum.maxTotalBytes
    && limits.maxFileBytes <= maximum.maxFileBytes;
}

function copyAssessmentOptions(
  options: SavedPlanAssessmentOptions,
): SavedPlanAssessmentOptions {
  return {
    terraformExecutable: options.terraformExecutable,
    roots: options.roots.map((root) => ({
      tenant: root.tenant,
      label: root.label,
      members: [...root.members],
      envDir: root.envDir,
      savedPlanPath: root.savedPlanPath,
      fingerprintPath: root.fingerprintPath,
      varFiles: [...root.varFiles],
    })).sort((left, right) => {
      const leftKey = `${left.tenant}\0${left.label}`;
      const rightKey = `${right.tenant}\0${right.label}`;
      return leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0;
    }),
    backendConfig: options.backendConfig,
    policyPath: options.policyPath,
    controlFiles: copyAssessmentControlFiles(options.controlFiles ?? []),
    ...(options.context === undefined
      ? {}
      : { context: copySavedPlanAssessmentContext(options.context) }),
    sourceLimits: copyReadLimits(
      options.sourceLimits ?? DEFAULT_BOUNDED_READ_LIMITS,
    ),
    savedPlanLimits: copyReadLimits(
      options.savedPlanLimits ?? DEFAULT_SAVED_PLAN_LIMITS,
    ),
    policyLimits: copyReadLimits(
      options.policyLimits ?? DEFAULT_POLICY_LIMITS,
    ),
    ...(options.terraformShowLimits === undefined
      ? {}
      : {
          terraformShowLimits: {
            timeoutMs: options.terraformShowLimits.timeoutMs,
            maxStdoutBytes: options.terraformShowLimits.maxStdoutBytes,
            maxStderrBytes: options.terraformShowLimits.maxStderrBytes,
          },
        }),
    operationTimeoutMs: options.operationTimeoutMs
      ?? DEFAULT_SAVED_PLAN_ASSESSMENT_TIMEOUT_MS,
    maxRetainedSnapshotBytes: options.maxRetainedSnapshotBytes
      ?? MAX_RETAINED_PLAN_SNAPSHOT_BYTES,
    resultLimits: {
      maxFindings: options.resultLimits?.maxFindings
        ?? DEFAULT_ASSESSMENT_RESULT_LIMITS.maxFindings,
      maxPaths: options.resultLimits?.maxPaths
        ?? DEFAULT_ASSESSMENT_RESULT_LIMITS.maxPaths,
      maxMetadataBytes: options.resultLimits?.maxMetadataBytes
        ?? DEFAULT_ASSESSMENT_RESULT_LIMITS.maxMetadataBytes,
    },
  };
}

function resourceTypes(plan: Record<string, unknown>): Map<string, string> {
  const result = new Map<string, string>();
  for (const source of ["resource_changes", "resource_drift"] as const) {
    const changes = plan[source];
    if (!Array.isArray(changes)) {
      continue;
    }
    for (const change of changes) {
      if (
        isJsonRecord(change)
        && typeof change.address === "string"
        && typeof change.type === "string"
      ) {
        result.set(`${source}\0${change.address}`, change.type);
      }
    }
  }
  return result;
}

function attachResourceTypes(
  plan: Record<string, unknown>,
  findings: readonly PlanFinding[],
): AssessmentFinding[] {
  const types = resourceTypes(plan);
  return findings.map((finding) => ({
    ...finding,
    resource_type: types.get(`${finding.source}\0${finding.address}`) ?? null,
  }));
}

function findingMetadataBytes(finding: AssessmentFinding): number {
  let bytes = Buffer.byteLength(finding.source, "utf8")
    + Buffer.byteLength(finding.address, "utf8")
    + (finding.resource_type === null
      ? 0
      : Buffer.byteLength(finding.resource_type, "utf8"));
  for (const action of finding.actions) {
    bytes += Buffer.byteLength(action, "utf8");
  }
  for (const findingPath of finding.paths) {
    for (const segment of findingPath) {
      bytes += Buffer.byteLength(String(segment), "utf8");
    }
  }
  return bytes;
}

function totalStatus(clean: number, tolerated: number, blocked: number): PlanStatus {
  if (blocked > 0) {
    return BLOCKED;
  }
  if (tolerated > 0) {
    return TOLERATED;
  }
  return CLEAN;
}

function assessmentCore(
  roots: readonly AssessedSavedPlanRoot[],
  policySha256: string | null,
  stalePolicy: readonly StalePolicyEntry[],
): SavedPlanAssessmentCore {
  const clean = roots.filter((root) => root.status === CLEAN).length;
  const tolerated = roots.filter((root) => root.status === TOLERATED).length;
  const blocked = roots.filter((root) => root.status === BLOCKED).length;
  return {
    status: totalStatus(clean, tolerated, blocked),
    checked: roots.length,
    clean,
    tolerated,
    blocked,
    policy_sha256: policySha256,
    roots: [...roots],
    stale_policy: stalePolicy.map((entry) => ({ ...entry })),
  };
}

function safeFailure(error: unknown): ProcessFailure {
  if (error instanceof ProcessFailure) {
    return error;
  }
  if (error instanceof AssessmentPlanError) {
    return new ProcessFailure({
      code: "INVALID_ASSESSMENT_PLAN",
      category: "domain",
      message: "saved plan is outside the supported assessment contract",
    });
  }
  return new ProcessFailure({
    code: "ASSESSMENT_FAILED",
    category: "internal",
    message: "saved-plan assessment failed",
  });
}

function withCleanupDetail(
  failure: ProcessFailure,
  cleanup: ProcessFailure,
): ProcessFailure {
  return new ProcessFailure({
    code: failure.code,
    category: failure.category,
    message: failure.message,
    retryable: failure.retryable,
    details: [
      ...failure.details,
      {
        path: "/",
        code: cleanup.code,
        message: "private assessment cleanup also failed",
      },
    ],
  });
}

function remainingTime(deadline: number): number {
  const remaining = deadline - Date.now();
  if (remaining <= 0) {
    fail("ASSESSMENT_TIMEOUT", "saved-plan assessment exceeded its execution deadline");
  }
  return remaining;
}

async function recheckAssessmentContext(
  options: SavedPlanAssessmentOptions,
): Promise<void> {
  const controlFiles = options.controlFiles ?? [];
  await recheckAssessmentControlFiles(controlFiles);
  if (options.context !== undefined) {
    await recheckSavedPlanAssessmentContext(options.context, options.roots);
  }
  await recheckAssessmentControlFiles(controlFiles);
}

function showLimits(
  configured: TerraformShowLimits | undefined,
  deadline: number,
): TerraformShowLimits {
  const limits = configured ?? DEFAULT_TERRAFORM_SHOW_LIMITS;
  return {
    timeoutMs: Math.max(1, Math.min(limits.timeoutMs, remainingTime(deadline))),
    maxStdoutBytes: limits.maxStdoutBytes,
    maxStderrBytes: limits.maxStderrBytes,
  };
}

/**
 * Assess all selected roots as one evidence transaction. Raw plan values stay
 * local to this function; the returned core contains only report-safe metadata
 * and normalized classifier findings.
 */
async function runSavedPlanAssessment<T>(
  options: SavedPlanAssessmentOptions,
  finalize: (core: SavedPlanAssessmentCore) => T,
): Promise<T> {
  const assessed: AssessedSavedPlanRoot[] = [];
  let stalePolicy: readonly StalePolicyEntry[] = [];
  let policySha256: string | null = null;
  let reportKind: SavedPlanAssessmentErrorKind = "assessment_error";
  let capturedOptions: SavedPlanAssessmentOptions | null = null;
  let temporary: string | null = null;
  let temporaryIdentity: { readonly dev: bigint; readonly ino: bigint } | null = null;
  const evidence: SavedPlanEvidence[] = [];
  let completed: T | undefined;
  let hasCompleted = false;
  let primaryFailure: ProcessFailure | null = null;
  try {
    if (options.roots.length > MAX_SAVED_PLAN_ASSESSMENT_ROOTS) {
      fail(
        "TOO_MANY_SAVED_PLANS",
        "saved-plan assessment exceeds the root-count limit",
      );
    }
    capturedOptions = copyAssessmentOptions(options);
    validateRootInputs(capturedOptions.roots);
    const operationTimeoutMs = capturedOptions.operationTimeoutMs as number;
    const maxRetainedSnapshotBytes = capturedOptions.maxRetainedSnapshotBytes as bigint;
    const resultLimits = capturedOptions.resultLimits as SavedPlanAssessmentResultLimits;
    if (
      !Number.isSafeInteger(operationTimeoutMs)
      || operationTimeoutMs <= 0
      || operationTimeoutMs > MAX_SAVED_PLAN_ASSESSMENT_TIMEOUT_MS
      || maxRetainedSnapshotBytes <= 0n
      || maxRetainedSnapshotBytes > MAX_RETAINED_PLAN_SNAPSHOT_BYTES
      || !Number.isSafeInteger(resultLimits.maxFindings)
      || resultLimits.maxFindings <= 0
      || resultLimits.maxFindings > MAX_SAVED_PLAN_ASSESSMENT_FINDINGS
      || !Number.isSafeInteger(resultLimits.maxPaths)
      || resultLimits.maxPaths <= 0
      || resultLimits.maxPaths > MAX_SAVED_PLAN_ASSESSMENT_PATHS
      || !Number.isSafeInteger(resultLimits.maxMetadataBytes)
      || resultLimits.maxMetadataBytes <= 0
      || resultLimits.maxMetadataBytes > MAX_SAVED_PLAN_ASSESSMENT_METADATA_BYTES
    ) {
      fail("INVALID_ASSESSMENT_LIMIT", "saved-plan assessment limits are invalid");
    }
    const deadline = Date.now() + operationTimeoutMs;
    if (
      !path.isAbsolute(capturedOptions.terraformExecutable)
      || (
        capturedOptions.backendConfig !== null
        && !path.isAbsolute(capturedOptions.backendConfig)
      )
      || (
        capturedOptions.policyPath !== null
        && !path.isAbsolute(capturedOptions.policyPath)
      )
    ) {
      fail("UNRESOLVED_ASSESSMENT_PATH", "saved-plan assessment paths must be absolute");
    }
    const sourceLimits = capturedOptions.sourceLimits as BoundedReadLimits;
    const savedPlanLimits = capturedOptions.savedPlanLimits as BoundedReadLimits;
    const policyLimits = capturedOptions.policyLimits as BoundedReadLimits;
    if (
      !limitsWithin(sourceLimits, DEFAULT_BOUNDED_READ_LIMITS)
      || !limitsWithin(savedPlanLimits, DEFAULT_SAVED_PLAN_LIMITS)
      || !limitsWithin(policyLimits, DEFAULT_POLICY_LIMITS)
    ) {
      fail(
        "INVALID_ASSESSMENT_LIMIT",
        "saved-plan read limits cannot exceed the hard transaction ceilings",
      );
    }
    const controlFiles = capturedOptions.controlFiles ?? [];
    await recheckAssessmentContext(capturedOptions);
    remainingTime(deadline);
    reportKind = "policy_error";
    const boundPolicy = await loadBoundDriftPolicy(
      capturedOptions.policyPath,
      new ReadBudget(policyLimits),
    );
    policySha256 = boundPolicy.file?.sha256 ?? null;
    remainingTime(deadline);
    reportKind = "assessment_error";
    if (capturedOptions.roots.length === 0) {
      fail("NO_SAVED_PLANS", "no saved plans were selected for assessment");
    }
    temporary = await mkdtemp(path.join(tmpdir(), "infrawright-assessment-"));
    await chmod(temporary, 0o700);
    const temporaryStat = await lstat(temporary, { bigint: true });
    if (!temporaryStat.isDirectory() || temporaryStat.isSymbolicLink()) {
      fail("UNSAFE_SNAPSHOT_DIRECTORY", "assessment temporary directory is unsafe");
    }
    temporaryIdentity = { dev: temporaryStat.dev, ino: temporaryStat.ino };
    let retainedSnapshotBytes = 0n;
    let findingCount = 0;
    let findingPathCount = 0;
    let findingMetadataByteCount = 0;
    for (const root of capturedOptions.roots) {
      remainingTime(deadline);
      const remainingSnapshotBytes = maxRetainedSnapshotBytes
        - retainedSnapshotBytes;
      if (remainingSnapshotBytes <= 0n) {
        fail(
          "PLAN_SNAPSHOT_BUDGET_EXCEEDED",
          "saved-plan snapshots exceed the transaction byte limit",
        );
      }
      const captureSavedPlanLimits: BoundedReadLimits = {
        ...savedPlanLimits,
        maxFileBytes: savedPlanLimits.maxFileBytes < remainingSnapshotBytes
          ? savedPlanLimits.maxFileBytes
          : remainingSnapshotBytes,
      };
      const captured = await prepareSavedPlanEvidence({
        savedPlanPath: root.savedPlanPath,
        fingerprintPath: root.fingerprintPath,
        fingerprintInput: {
          envDir: root.envDir,
          varFiles: root.varFiles,
          memberTypes: root.members,
          backendConfig: capturedOptions.backendConfig,
          backendKey: capturedOptions.backendConfig === null
            ? null
            : `${root.tenant}/${root.label}.tfstate`,
        },
        snapshotDirectory: temporary,
        fingerprintBudget: new ReadBudget(sourceLimits),
        savedPlanBudget: new ReadBudget(captureSavedPlanLimits),
      });
      evidence.push(captured);
      retainedSnapshotBytes += captured.snapshot.size;
      if (retainedSnapshotBytes > maxRetainedSnapshotBytes) {
        fail(
          "PLAN_SNAPSHOT_BUDGET_EXCEEDED",
          "saved-plan snapshots exceed the transaction byte limit",
        );
      }
      const plan = await terraformShowPlan({
        terraformExecutable: capturedOptions.terraformExecutable,
        envDir: root.envDir,
        snapshotPath: captured.snapshot.path,
        limits: showLimits(capturedOptions.terraformShowLimits, deadline),
      });
      remainingTime(deadline);
      await recheckAssessmentControlFiles(controlFiles);
      remainingTime(deadline);
      await recheckSavedPlanEvidence({
        evidence: captured,
        fingerprintBudget: new ReadBudget(sourceLimits),
        savedPlanBudget: new ReadBudget(savedPlanLimits),
      });
      const classification = classifyPlan(plan, boundPolicy.policy);
      remainingTime(deadline);
      if (!isJsonRecord(plan)) {
        fail("INVALID_ASSESSMENT_PLAN", "Terraform show did not emit a plan object");
      }
      await recheckSavedPlanEvidence({
        evidence: captured,
        fingerprintBudget: new ReadBudget(sourceLimits),
        savedPlanBudget: new ReadBudget(savedPlanLimits),
      });
      const findings = attachResourceTypes(plan, classification.findings);
      findingCount += findings.length;
      findingPathCount += findings.reduce((count, finding) => {
        return count + finding.paths.length;
      }, 0);
      findingMetadataByteCount += findings.reduce((count, finding) => {
        return count + findingMetadataBytes(finding);
      }, 0);
      if (
        findingCount > resultLimits.maxFindings
        || findingPathCount > resultLimits.maxPaths
        || findingMetadataByteCount > resultLimits.maxMetadataBytes
      ) {
        fail(
          "ASSESSMENT_RESULT_LIMIT_EXCEEDED",
          "saved-plan assessment exceeds the report metadata limit",
        );
      }
      assessed.push({
        tenant: root.tenant,
        label: root.label,
        members: sortedStrings(root.members),
        status: classification.status,
        plan: {
          sha256: captured.originalPlan.sha256,
          format_version: metadata(plan, "format_version"),
          terraform_version: metadata(plan, "terraform_version"),
        },
        plan_fingerprint: captured.fingerprintFile.fingerprint,
        findings,
      });
    }

    const checkedTypes = new Set(capturedOptions.roots.flatMap((root) => root.members));
    stalePolicy = boundPolicy.policy.staleEntries({
      resourceTypes: checkedTypes,
      modes: ["plan_tolerate"],
    });
    for (const captured of evidence) {
      remainingTime(deadline);
      await recheckSavedPlanEvidence({
        evidence: captured,
        fingerprintBudget: new ReadBudget(sourceLimits),
        savedPlanBudget: new ReadBudget(savedPlanLimits),
      });
    }
    await recheckBoundDriftPolicy(boundPolicy, new ReadBudget(policyLimits));
    await recheckAssessmentContext(capturedOptions);
    for (const captured of evidence) {
      remainingTime(deadline);
      await recheckSavedPlanEvidence({
        evidence: captured,
        fingerprintBudget: new ReadBudget(sourceLimits),
        savedPlanBudget: new ReadBudget(savedPlanLimits),
      });
    }
    await recheckBoundDriftPolicy(boundPolicy, new ReadBudget(policyLimits));
    await recheckAssessmentControlFiles(controlFiles);
    remainingTime(deadline);

    const core = assessmentCore(assessed, policySha256, stalePolicy);
    completed = finalize(core);
    remainingTime(deadline);
    if (
      typeof completed === "object"
      && completed !== null
      && "then" in completed
    ) {
      fail(
        "INVALID_ASSESSMENT_FINALIZER",
        "saved-plan assessment finalization must be synchronous",
      );
    }
    hasCompleted = true;
  } catch (error: unknown) {
    if (error instanceof DriftPolicyLoadFailure) {
      policySha256 = error.file.sha256;
    }
    primaryFailure = safeFailure(error);
    if (primaryFailure.code === "NO_SAVED_PLANS") {
      reportKind = "no_saved_plans";
    }
  } finally {
    let cleanupFailure: ProcessFailure | null = null;
    for (const captured of evidence) {
      try {
        await cleanupSavedPlanEvidence(captured);
      } catch (error: unknown) {
        cleanupFailure ??= safeFailure(error);
      }
    }
    if (temporary !== null && cleanupFailure === null) {
      try {
        const current = await lstat(temporary, { bigint: true });
        if (
          temporaryIdentity === null
          || !current.isDirectory()
          || current.isSymbolicLink()
          || current.dev !== temporaryIdentity.dev
          || current.ino !== temporaryIdentity.ino
        ) {
          throw new ProcessFailure({
            code: "ASSESSMENT_CLEANUP_REFUSED",
            category: "io",
            message: "private assessment directory changed before cleanup",
          });
        }
        await rm(temporary, { recursive: true, force: true });
      } catch (error: unknown) {
        cleanupFailure = error instanceof ProcessFailure
          ? error
          : new ProcessFailure({
              code: "ASSESSMENT_CLEANUP_FAILED",
              category: "io",
              message: "unable to remove private assessment files",
            });
      }
    }
    if (cleanupFailure !== null) {
      primaryFailure = primaryFailure === null
        ? cleanupFailure
        : withCleanupDetail(primaryFailure, cleanupFailure);
    }
  }
  if (primaryFailure !== null) {
    throw new SavedPlanAssessmentFailure({
      failure: primaryFailure,
      reportKind,
      partial: assessmentCore(assessed, policySha256, stalePolicy),
    });
  }
  if (!hasCompleted) {
    throw new SavedPlanAssessmentFailure({
      failure: new ProcessFailure({
        code: "ASSESSMENT_FAILED",
        category: "internal",
        message: "saved-plan assessment failed",
      }),
      reportKind: "assessment_error",
      partial: assessmentCore(assessed, policySha256, stalePolicy),
    });
  }
  return completed as T;
}

/** Inspect the report-safe kernel without claiming a publishable report. */
export async function assessSavedPlans(
  options: SavedPlanAssessmentOptions,
): Promise<SavedPlanAssessmentCore> {
  return runSavedPlanAssessment(options, (core) => core);
}

export interface SavedPlanAssessmentReportOutcome {
  readonly report: SavedPlanAssessmentReport;
  readonly failure: SavedPlanAssessmentFailure | null;
}

/** Build the versioned report synchronously inside the final evidence window. */
export async function assessSavedPlansReport(options: {
  readonly assessment: SavedPlanAssessmentOptions;
  readonly mode: AssessmentMode;
  readonly request: AssessmentReportRequest;
}): Promise<SavedPlanAssessmentReportOutcome> {
  const mode = options.mode;
  const request = {
    tenant: options.request.tenant,
    selectors: [...options.request.selectors],
    policy: options.request.policy,
  };
  if (
    (mode === "assert-clean"
      && (request.policy !== null || options.assessment.policyPath !== null))
    || (mode === "assert-adoptable"
      && (request.policy === null) !== (options.assessment.policyPath === null))
  ) {
    fail("INVALID_ASSESSMENT_REQUEST", "assessment mode and policy input disagree");
  }
  try {
    const report = await runSavedPlanAssessment(
      options.assessment,
      (core) => buildSavedPlanAssessmentReport({
        mode,
        request,
        core,
      }),
    );
    return { report, failure: null };
  } catch (error: unknown) {
    if (!(error instanceof SavedPlanAssessmentFailure)) {
      throw error;
    }
    return {
      report: buildSavedPlanAssessmentErrorReport({
        mode,
        request,
        partial: error.partial,
        error: { kind: error.reportKind, message: error.message },
      }),
      failure: error,
    };
  }
}
