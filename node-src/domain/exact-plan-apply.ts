import { execFile } from "node:child_process";
import { chmod, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import type { BoundAssessmentControlFile } from "./control-evidence.js";
import {
  copyAssessmentControlFiles,
  recheckAssessmentControlFiles,
} from "./control-evidence.js";
import type { Deployment } from "./types.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { ProcessFailure } from "./errors.js";
import {
  copyLoadedSavedPlanAssessmentContext,
  materializeLoadedSavedPlanAssessmentRoots,
  recheckLoadedSavedPlanAssessmentContext,
  type LoadedSavedPlanAssessmentContext,
} from "./plan-assessment-inputs.js";
import {
  DEFAULT_POLICY_LIMITS,
  DEFAULT_SAVED_PLAN_LIMITS,
  type SavedPlanAssessmentRootInput,
} from "./plan-assessment.js";
import {
  cleanupSavedPlanEvidence,
  prepareSavedPlanEvidence,
  recheckSavedPlanEvidence,
  type SavedPlanEvidence,
} from "./plan-evidence.js";
import { BLOCKED, classifyPlan, TOLERATED } from "./plan-eval.js";
import {
  loadBoundDriftPolicy,
  recheckBoundDriftPolicy,
} from "./plan-policy.js";
import {
  createPlanTerraform,
  removeSavedPlanArtifacts,
  requireBackendConfiguration,
  type PlanTerraformRequest,
} from "./plan-lifecycle.js";
import {
  DEFAULT_BOUNDED_READ_LIMITS,
  ReadBudget,
} from "../io/bounded-files.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../io/terraform-command.js";
import {
  operationalTerraformShowEnvironment,
  terraformShowPlan,
  type TerraformShowLimits,
} from "../io/terraform-show.js";
import { isJsonRecord } from "../json/python-equality.js";

export interface ExactPlanApplyTerraform {
  initialize(request: PlanTerraformRequest): Promise<void>;
  show(request: {
    readonly directory: string;
    readonly snapshotPath: string;
  }): Promise<unknown>;
  apply(request: { readonly directory: string }): Promise<void>;
}

export interface ExactPlanApplyInputs {
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly controlFiles?: readonly BoundAssessmentControlFile[];
}

export interface ExactPlanApplyResult {
  readonly applied: number;
}

export interface ExactPlanApplyOptions {
  readonly workspace: string;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly backendConfig: string | null;
  readonly policyPath: string | null;
  readonly mainBranch: string | null;
  readonly allowDestroy: boolean;
  readonly allowNonMain: boolean;
  readonly allowPlanChanges: boolean;
  readonly currentBranch: () => Promise<string>;
  readonly loadInputs: () => Promise<ExactPlanApplyInputs>;
  readonly terraform: ExactPlanApplyTerraform;
  readonly onDiagnostic?: (message: string) => void;
}

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function definedEnvironment(environment: NodeJS.ProcessEnv): Readonly<Record<string, string>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, value] of Object.entries(environment)) {
    if (value !== undefined) output[key] = value;
  }
  return output;
}

/** Adapt the common Terraform runner to init/show/exact-plan Apply. */
export function createExactPlanApplyTerraform(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly limits?: TerraformCommandLimits;
  readonly showLimits?: TerraformShowLimits;
  readonly terraformExecutable: string;
}): ExactPlanApplyTerraform {
  const environment = definedEnvironment(options.environment);
  const showEnvironment = operationalTerraformShowEnvironment(environment);
  const planTerraform = createPlanTerraform({
    environment,
    terraformExecutable: options.terraformExecutable,
    ...(options.limits === undefined ? {} : { limits: options.limits }),
  });
  return {
    initialize: (request) => planTerraform.initialize(request),
    show: (request) => terraformShowPlan({
      envDir: request.directory,
      environment: showEnvironment,
      snapshotPath: request.snapshotPath,
      terraformExecutable: options.terraformExecutable,
      ...(options.showLimits === undefined ? {} : { limits: options.showLimits }),
    }),
    apply: async (request) => {
      await runTerraformCommand({
        argv: ["apply", "-input=false", "tfplan"],
        cwd: request.directory,
        environment,
        ...(options.limits === undefined ? {} : { limits: options.limits }),
        output: "inherit",
        terraformExecutable: options.terraformExecutable,
      });
    },
  };
}

async function gitBranch(cwd: string): Promise<string> {
  return new Promise((resolve) => {
    execFile(
      "git",
      ["rev-parse", "--abbrev-ref", "HEAD"],
      { cwd, encoding: "utf8", maxBuffer: 64 * 1024 },
      (error, stdout) => resolve(error === null ? stdout.trim() : "unknown"),
    );
  });
}

/** Resolve the current branch with the legacy CI-environment priority. */
export async function currentApplyBranch(options: {
  readonly cwd: string;
  readonly environment: NodeJS.ProcessEnv;
  readonly gitBranch?: (cwd: string) => Promise<string>;
}): Promise<string> {
  const ref = options.environment.BUILD_SOURCEBRANCH
    || options.environment.GITHUB_REF
    || options.environment.BITBUCKET_BRANCH
    || "";
  if (ref !== "") {
    return ref.split("refs/heads/").at(-1) as string;
  }
  try {
    return await (options.gitBranch ?? gitBranch)(options.cwd);
  } catch {
    return "unknown";
  }
}

function pythonStringRepr(value: string): string {
  return `'${value
    .replaceAll("\\", "\\\\")
    .replaceAll("'", "\\'")
    .replaceAll("\t", "\\t")
    .replaceAll("\r", "\\r")
    .replaceAll("\n", "\\n")}'`;
}

function destroyCount(plan: unknown): number {
  if (!isJsonRecord(plan)) return 0;
  let count = 0;
  for (const field of ["resource_changes", "resource_drift"] as const) {
    const changes = plan[field];
    if (!Array.isArray(changes)) continue;
    for (const record of changes) {
      if (
        isJsonRecord(record)
        && isJsonRecord(record.change)
        && Array.isArray(record.change.actions)
        && record.change.actions.includes("delete")
      ) {
        count += 1;
      }
    }
  }
  return count;
}

function backendRequest(
  backendConfig: string | null,
  root: SavedPlanAssessmentRootInput,
): Pick<PlanTerraformRequest, "backendConfig" | "backendKey"> {
  return backendConfig === null
    ? {}
    : {
        backendConfig,
        backendKey: `${root.tenant}/${root.label}.tfstate`,
      };
}

async function recheckApplyEvidence(options: {
  readonly context: LoadedSavedPlanAssessmentContext;
  readonly controlFiles: readonly BoundAssessmentControlFile[];
  readonly evidence: SavedPlanEvidence;
  readonly expectedRoots: readonly SavedPlanAssessmentRootInput[];
  readonly policy: Awaited<ReturnType<typeof loadBoundDriftPolicy>>;
}): Promise<void> {
  await recheckAssessmentControlFiles(options.controlFiles);
  await recheckLoadedSavedPlanAssessmentContext(options.context, options.expectedRoots);
  await recheckSavedPlanEvidence({
    evidence: options.evidence,
    fingerprintBudget: new ReadBudget(DEFAULT_BOUNDED_READ_LIMITS),
    savedPlanBudget: new ReadBudget(DEFAULT_SAVED_PLAN_LIMITS),
  });
  await recheckBoundDriftPolicy(
    options.policy,
    new ReadBudget(DEFAULT_POLICY_LIMITS),
  );
  await recheckAssessmentControlFiles(options.controlFiles);
}

async function applyRoot(options: {
  readonly backendConfig: string | null;
  readonly context: LoadedSavedPlanAssessmentContext;
  readonly controlFiles: readonly BoundAssessmentControlFile[];
  readonly expectedRoots: readonly SavedPlanAssessmentRootInput[];
  readonly policy: Awaited<ReturnType<typeof loadBoundDriftPolicy>>;
  readonly root: SavedPlanAssessmentRootInput;
  readonly terraform: ExactPlanApplyTerraform;
  readonly allowDestroy: boolean;
  readonly allowPlanChanges: boolean;
  readonly onDiagnostic: (message: string) => void;
}): Promise<void> {
  const temporary = await mkdtemp(path.join(tmpdir(), "infrawright-apply-"));
  await chmod(temporary, 0o700);
  let evidence: SavedPlanEvidence | null = null;
  let primaryFailure: unknown = null;
  try {
    const backend = backendRequest(options.backendConfig, options.root);
    evidence = await prepareSavedPlanEvidence({
      savedPlanPath: options.root.savedPlanPath,
      fingerprintPath: options.root.fingerprintPath,
      fingerprintInput: {
        envDir: options.root.envDir,
        varFiles: options.root.varFiles,
        memberTypes: options.root.members,
        backendConfig: options.backendConfig,
        backendKey: options.backendConfig === null
          ? null
          : `${options.root.tenant}/${options.root.label}.tfstate`,
      },
      snapshotDirectory: temporary,
      fingerprintBudget: new ReadBudget(DEFAULT_BOUNDED_READ_LIMITS),
      savedPlanBudget: new ReadBudget(DEFAULT_SAVED_PLAN_LIMITS),
    });
    await requireBackendConfiguration({
      ...(options.backendConfig === null
        ? {}
        : { backendConfig: options.backendConfig }),
      directory: options.root.envDir,
      label: options.root.label,
    });
    options.onDiagnostic(`== apply ${options.root.tenant}/${options.root.label}`);
    await options.terraform.initialize({
      ...backend,
      directory: options.root.envDir,
      save: false,
      varFiles: [],
    });
    await recheckApplyEvidence({
      context: options.context,
      controlFiles: options.controlFiles,
      evidence,
      expectedRoots: options.expectedRoots,
      policy: options.policy,
    });
    const plan = await options.terraform.show({
      directory: options.root.envDir,
      snapshotPath: evidence.snapshot.path,
    });
    const classification = classifyPlan(plan, options.policy.policy, {
      ...(options.root.referenceOutputTypes === undefined
        ? {}
        : { referenceOutputTypes: options.root.referenceOutputTypes }),
    });
    const destroys = destroyCount(plan);
    if (
      classification.status === BLOCKED
      && destroys > 0
      && !options.allowDestroy
    ) {
      fail(
        "APPLY_DESTROY_REFUSED",
        `${options.root.tenant}/${options.root.label} saved plan destroys (or replaces) `
          + `${destroys} resource(s) - refused`,
      );
    }
    if (classification.status === BLOCKED && !options.allowPlanChanges) {
      fail(
        "APPLY_BLOCKED_PLAN_REFUSED",
        `${options.root.tenant}/${options.root.label} saved plan is blocked by untolerated `
          + "changes; refused. Run assert-adoptable for review, pass POLICY=<file> for "
          + "explicit tolerated drift, or use --allow-plan-changes only as a broad unsafe "
          + "override.",
      );
    }
    if (classification.status === TOLERATED) {
      options.onDiagnostic(
        `TOLERATED: ${options.root.tenant}/${options.root.label} saved plan has `
          + "consumer-tolerated drift",
      );
    } else if (classification.status === BLOCKED) {
      options.onDiagnostic(
        `WARNING: applying BLOCKED ${options.root.tenant}/${options.root.label} saved plan `
          + "because --allow-plan-changes was set",
      );
    }
    await recheckApplyEvidence({
      context: options.context,
      controlFiles: options.controlFiles,
      evidence,
      expectedRoots: options.expectedRoots,
      policy: options.policy,
    });
    await options.terraform.apply({ directory: options.root.envDir });
    await removeSavedPlanArtifacts(options.root.envDir);
  } catch (error: unknown) {
    primaryFailure = error;
  } finally {
    let cleanupFailure: unknown = null;
    if (evidence !== null) {
      try {
        await cleanupSavedPlanEvidence(evidence);
      } catch (error: unknown) {
        cleanupFailure = error;
      }
    }
    try {
      await rm(temporary, { force: true, recursive: true });
    } catch (error: unknown) {
      cleanupFailure ??= error;
    }
    if (primaryFailure === null && cleanupFailure !== null) {
      primaryFailure = cleanupFailure;
    }
  }
  if (primaryFailure !== null) throw primaryFailure;
}

/** Apply only selected, current, classified saved plans in Python root order. */
export async function applyExactSavedPlans(
  options: ExactPlanApplyOptions,
): Promise<ExactPlanApplyResult> {
  if (!path.isAbsolute(options.workspace)) {
    fail("INVALID_WORKSPACE", "Apply workspace must be absolute");
  }
  if (
    (options.backendConfig !== null && !path.isAbsolute(options.backendConfig))
    || (options.policyPath !== null && !path.isAbsolute(options.policyPath))
  ) {
    fail("UNRESOLVED_APPLY_PATH", "saved-plan Apply paths must be absolute");
  }
  const mainBranch = options.mainBranch || "main";
  const branch = await options.currentBranch();
  if (branch !== mainBranch && !options.allowNonMain) {
    fail(
      "APPLY_BRANCH_REFUSED",
      `apply refused from ${pythonStringRepr(branch)} - only merged ${mainBranch} config gets `
        + "applied (use ALLOW_NON_MAIN=1 for an intentional exception)",
    );
  }
  const policy = await loadBoundDriftPolicy(
    options.policyPath,
    new ReadBudget(DEFAULT_POLICY_LIMITS),
  );
  const onDiagnostic = options.onDiagnostic ?? (() => undefined);
  if (options.allowPlanChanges) {
    onDiagnostic(
      "WARNING: --allow-plan-changes is a broad legacy override for BLOCKED saved plans; "
        + "prefer POLICY=<file> for explicit tolerated drift.",
    );
  }
  const inputs = await options.loadInputs();
  const controlFiles = copyAssessmentControlFiles(inputs.controlFiles ?? []);
  await recheckAssessmentControlFiles(controlFiles);
  const context = copyLoadedSavedPlanAssessmentContext({
    workspace: options.workspace,
    deployment: inputs.deployment,
    root: inputs.root,
    tenant: options.tenant,
    selectors: options.selectors,
  });
  const selected = await materializeLoadedSavedPlanAssessmentRoots(context);
  for (const diagnostic of selected.diagnostics) {
    onDiagnostic(`NOTE: ${diagnostic.message}`);
  }
  if (selected.roots.length === 0) {
    fail("NO_SAVED_PLANS", "no saved plans found - run make plan SAVE=1 first");
  }
  let applied = 0;
  for (let index = 0; index < selected.roots.length; index += 1) {
    const root = selected.roots[index] as SavedPlanAssessmentRootInput;
    await applyRoot({
      allowDestroy: options.allowDestroy,
      allowPlanChanges: options.allowPlanChanges,
      backendConfig: options.backendConfig,
      context,
      controlFiles,
      expectedRoots: selected.roots.slice(index),
      onDiagnostic,
      policy,
      root,
      terraform: options.terraform,
    });
    applied += 1;
  }
  return Object.freeze({ applied });
}
