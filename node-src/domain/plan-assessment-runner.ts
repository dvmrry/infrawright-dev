import path from "node:path";
import { LosslessNumber } from "lossless-json";

import type { Deployment } from "./types.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import type { BoundAssessmentControlFile } from "./control-evidence.js";
import { writeAssessmentReport } from "../io/assessment-report.js";
import { assessmentGuidanceSource } from "./assessment-guidance.js";
import { ProcessFailure } from "./errors.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  canonicalPythonNumberToken,
  pythonFiniteFloatToken,
} from "../json/python-number.js";
import { resolveLoadedSavedPlanAssessment } from "./plan-assessment-inputs.js";
import {
  assessSavedPlansReport,
  preflightSavedPlanAssessmentPolicy,
  type SavedPlanAssessmentCore,
} from "./plan-assessment.js";
import { DriftPolicyLoadFailure } from "./plan-policy.js";
import type {
  AssessmentMode,
  AssessmentReportRequest,
  AssessmentReportRoot,
  Guidance,
  SavedPlanAssessmentReport,
} from "./plan-report.js";
import { buildSavedPlanAssessmentErrorReport } from "./plan-report.js";

interface RunSavedPlanAssertionCommonOptions {
  readonly backendConfig: string | null;
  readonly mode: AssessmentMode;
  readonly onDiagnostic?: (message: string) => void;
  readonly policyPath: string | null;
  readonly reportPath: string | null;
  readonly selectors: readonly string[];
  readonly stdout?: (text: string) => void;
  readonly tenant: string | null;
  readonly terraformExecutable: string | (() => Promise<string>);
  readonly workspace: string;
}

export interface SavedPlanAssertionInputs {
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly controlFiles?: readonly BoundAssessmentControlFile[];
}

export type RunSavedPlanAssertionOptions = RunSavedPlanAssertionCommonOptions & (
  | (SavedPlanAssertionInputs & { readonly loadInputs?: never })
  | {
      readonly deployment?: never;
      readonly root?: never;
      readonly controlFiles?: never;
      readonly loadInputs: () => Promise<SavedPlanAssertionInputs>;
    }
);

function pythonJsonString(value: string): string {
  return JSON.stringify(value).replace(/[\u0080-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function json(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError("diagnostic value is not JSON");
    const token = Number.isSafeInteger(value) && !Object.is(value, -0)
      ? String(value)
      : pythonFiniteFloatToken(value);
    if (token === null) throw new TypeError("diagnostic value is not JSON");
    return token;
  }
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token === null) throw new TypeError("diagnostic value is not JSON");
    return token;
  }
  if (typeof value === "string") return pythonJsonString(value);
  if (Array.isArray(value)) return `[${value.map(json).join(", ")}]`;
  if (typeof value === "object" && value !== null) {
    return `{${sortedStrings(Object.keys(value)).map((key) => {
      const child = (value as Record<string, unknown>)[key];
      return `${pythonJsonString(key)}: ${json(child)}`;
    }).join(", ")}}`;
  }
  throw new TypeError("diagnostic value is not JSON");
}

function text(entry: Guidance, field: string): string {
  const value = entry[field];
  return value === undefined || value === null ? "None" : String(value);
}

function emitGuidance(
  guidance: readonly Guidance[],
  emit: (message: string) => void,
): void {
  const lanes = [
    ["provider_config", "Provider configuration guidance:"],
    ["absent_default", "Absent/default guidance:"],
    ["dynamic_schema", "Dynamic-schema guidance:"],
  ] as const;
  for (const [lane, heading] of lanes) {
    const entries = guidance.filter((entry) => entry.lane === lane);
    if (entries.length === 0) continue;
    emit(`  ${heading}`);
    for (const entry of entries) {
      if (lane === "provider_config") {
        emit(`    - provider: ${text(entry, "provider")}`);
        emit(`      setting: ${text(entry, "setting")}`);
        if (entry.expected_value !== null && entry.expected_value !== undefined) {
          emit(`      expected value: ${json(entry.expected_value)}`);
        }
        emit(`      mode: ${text(entry, "mode")}`);
      } else if (lane === "absent_default") {
        emit(`    - rule: ${text(entry, "rule")}`);
        emit(`      provider: ${text(entry, "provider")}`);
        emit(`      resource type: ${text(entry, "resource_type")}`);
        emit(`      kind: ${text(entry, "kind")}`);
        emit(`      action: ${text(entry, "action")}`);
        if (Object.hasOwn(entry, "observed_value")) {
          emit(`      observed value: ${json(entry.observed_value)}`);
        }
      } else {
        emit(`    - rule: ${text(entry, "rule")}`);
        emit(`      provider: ${text(entry, "provider")}`);
        emit(`      resource type: ${text(entry, "resource_type")}`);
        emit(`      kind: ${text(entry, "kind")}`);
        emit(`      ownership: ${text(entry, "ownership")}`);
        emit(`      action: ${text(entry, "action")}`);
        if (entry.provider_version_constraint) {
          emit(
            `      provider version constraint: ${text(entry, "provider_version_constraint")}`,
          );
        }
      }
      emit(`      matched plan path: ${text(entry, "matched_plan_path")}`);
      emit(`      reason: ${text(entry, "reason")}`);
      if (entry.evidence) emit(`      evidence: ${text(entry, "evidence")}`);
      emit(`      status: ${text(entry, "status_effect")}`);
    }
  }
}

function emitFindings(
  root: AssessmentReportRoot,
  includeGuidance: boolean,
  emit: (message: string) => void,
): void {
  for (const finding of root.findings) {
    emit(
      `  ${finding.address ?? "None"} ${finding.actions.join(",")} ${finding.status}`,
    );
    for (const planPath of finding.paths) emit(`    - ${planPath}`);
  }
  if (includeGuidance) emitGuidance(root.guidance, emit);
}

function emitAssessment(
  report: SavedPlanAssessmentReport,
  emit: (message: string) => void,
): void {
  if (report.mode === "assert-clean") {
    for (const root of report.roots) {
      if (root.status === "clean") continue;
      emit(
        `NOT CLEAN: ${root.tenant}/${root.label} plan contains `
          + `${root.findings.length} change(s) beyond imports`,
      );
    }
    return;
  }
  for (const root of report.roots) {
    if (root.status === "blocked") {
      emit(`BLOCKED: ${root.tenant}/${root.label}`);
      emitFindings(root, true, emit);
    } else if (root.status === "clean_with_tolerated_drift") {
      emit(`TOLERATED: ${root.tenant}/${root.label}`);
      emitFindings(root, false, emit);
    }
  }
  for (const stale of report.stale_policy) {
    emit(
      `STALE DRIFT POLICY: ${stale.resource_type} ${stale.mode} `
        + `${stale.path} matched no path`,
    );
  }
}

function blockedFailure(report: SavedPlanAssessmentReport): ProcessFailure {
  if (report.mode === "assert-clean") {
    return new ProcessFailure({
      code: "PLAN_NOT_CLEAN",
      category: "domain",
      message: "tenant moved since fetch (or transform disagrees) - do not auto-merge",
    });
  }
  return new ProcessFailure({
    code: "PLAN_NOT_ADOPTABLE",
    category: "domain",
    message: `${report.summary.blocked} saved plan(s) blocked by untolerated changes`,
  });
}

function safeFailure(error: unknown): ProcessFailure {
  if (error instanceof ProcessFailure) return error;
  if (error instanceof Error && error.name === "MetadataError") {
    return new ProcessFailure({
      code: "INVALID_ASSESSMENT_INPUT",
      category: "request",
      message: error.message,
    });
  }
  return new ProcessFailure({
    code: "ASSESSMENT_FAILED",
    category: "internal",
    message: error instanceof Error && error.message.length > 0
      ? error.message
      : "saved-plan assessment failed",
  });
}

const EMPTY_ASSESSMENT: SavedPlanAssessmentCore = {
  status: "clean",
  checked: 0,
  clean: 0,
  tolerated: 0,
  blocked: 0,
  policy_sha256: null,
  roots: [],
  stale_policy: [],
};
const VALID_TENANT = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;

function emptyAssessment(policySha256: string | null): SavedPlanAssessmentCore {
  return { ...EMPTY_ASSESSMENT, policy_sha256: policySha256 };
}

function buildPreflightErrorReport(options: {
  readonly mode: AssessmentMode;
  readonly request: AssessmentReportRequest;
  readonly partial: SavedPlanAssessmentCore;
  readonly error: {
    readonly kind: "assessment_error" | "policy_error";
    readonly message: string;
  };
}): SavedPlanAssessmentReport {
  try {
    return buildSavedPlanAssessmentErrorReport(options);
  } catch (error: unknown) {
    if (
      !(error instanceof ProcessFailure)
      || error.code !== "INVALID_ASSESSMENT_REPORT"
      || typeof options.request.tenant !== "string"
      || VALID_TENANT.test(options.request.tenant)
      || options.partial.roots.length !== 0
    ) {
      throw error;
    }
    // Python records the raw invalid invocation before tenant validation. The
    // published schema remains strict; this narrow best-effort error record is
    // intentionally not revalidated and cannot represent a successful result.
    return {
      kind: "infrawright.saved_plan_assessment",
      schema_version: 1,
      mode: options.mode,
      request: {
        tenant: options.request.tenant,
        selectors: [...options.request.selectors],
        policy: options.mode === "assert-clean" ? null : options.request.policy,
        policy_sha256: options.mode === "assert-clean"
          ? null
          : options.partial.policy_sha256,
      },
      summary: {
        status: "error",
        checked: 0,
        clean: 0,
        tolerated: 0,
        blocked: 0,
      },
      roots: [],
      stale_policy: [],
      error: { ...options.error },
    };
  }
}

async function writeErrorReportBestEffort(options: {
  readonly emit: (message: string) => void;
  readonly path: string | null;
  readonly report: SavedPlanAssessmentReport;
  readonly stdout?: (text: string) => void;
}): Promise<void> {
  try {
    await writeAssessmentReport({
      path: options.path,
      report: options.report,
      ...(options.stdout === undefined ? {} : { stdout: options.stdout }),
    });
  } catch (writeError: unknown) {
    options.emit(
      `WARNING: could not write assessment error report ${json(options.path)}: `
        + `${writeError instanceof Error ? writeError.message : String(writeError)}; `
        + "preserving original assessment error",
    );
  }
}

function emitReportWarning(
  emit: (message: string) => void,
  reportPath: string | null,
  error: unknown,
): void {
  emit(
    `WARNING: could not write assessment error report ${json(reportPath)}: `
      + `${error instanceof Error ? error.message : String(error)}; `
      + "preserving original assessment error",
  );
}

/** Operational assert-clean/assert-adoptable behavior over the generic core. */
export async function runSavedPlanAssertion(
  options: RunSavedPlanAssertionOptions,
): Promise<void> {
  const emit = options.onDiagnostic ?? (() => undefined);
  const request = {
    tenant: options.tenant,
    selectors: [...options.selectors],
    policy: options.mode === "assert-clean" ? null : options.policyPath,
  };
  const policyPath = options.mode === "assert-clean" || options.policyPath === null
    ? null
    : path.resolve(options.workspace, options.policyPath);
  const unresolvedTerraform = path.resolve(
    options.workspace,
    ".infrawright-unresolved-terraform",
  );
  let policySha256: string | null = null;
  try {
    const preflight = await preflightSavedPlanAssessmentPolicy(policyPath);
    policySha256 = preflight.file?.sha256 ?? null;
  } catch (error: unknown) {
    const failure = safeFailure(error);
    const failedPolicySha256 = error instanceof DriftPolicyLoadFailure
      ? error.file.sha256
      : null;
    if (options.reportPath !== null) {
      await writeErrorReportBestEffort({
        emit,
        path: options.reportPath,
        report: buildPreflightErrorReport({
          mode: options.mode,
          request,
          partial: emptyAssessment(failedPolicySha256),
          error: { kind: "policy_error", message: failure.message },
        }),
        ...(options.stdout === undefined ? {} : { stdout: options.stdout }),
      });
    }
    throw failure;
  }
  let inputs: SavedPlanAssertionInputs;
  let resolved: Awaited<ReturnType<typeof resolveLoadedSavedPlanAssessment>>;
  let terraformExecutable: string;
  try {
    inputs = options.loadInputs === undefined
      ? {
          deployment: options.deployment,
          root: options.root,
          ...(options.controlFiles === undefined
            ? {}
            : { controlFiles: options.controlFiles }),
        }
      : await options.loadInputs();
    resolved = await resolveLoadedSavedPlanAssessment({
      workspace: options.workspace,
      deployment: inputs.deployment,
      root: inputs.root,
      tenant: options.tenant,
      selectors: options.selectors,
      terraformExecutable: typeof options.terraformExecutable === "string"
        ? options.terraformExecutable
        : unresolvedTerraform,
      backendConfig: options.backendConfig,
      policyPath,
      ...(inputs.controlFiles === undefined
        ? {}
        : { controlFiles: inputs.controlFiles }),
    });
    for (const diagnostic of resolved.diagnostics) emit(`NOTE: ${diagnostic.message}`);
    terraformExecutable = typeof options.terraformExecutable === "string"
      ? options.terraformExecutable
      : resolved.assessment.roots.length === 0
      ? unresolvedTerraform
      : await options.terraformExecutable();
  } catch (error: unknown) {
    const failure = safeFailure(error);
    if (options.reportPath !== null) {
      try {
        await writeErrorReportBestEffort({
          emit,
          path: options.reportPath,
          report: buildPreflightErrorReport({
            mode: options.mode,
            request,
            partial: emptyAssessment(policySha256),
            error: { kind: "assessment_error", message: failure.message },
          }),
          ...(options.stdout === undefined ? {} : { stdout: options.stdout }),
        });
      } catch (reportError: unknown) {
        emitReportWarning(emit, options.reportPath, reportError);
      }
    }
    throw failure;
  }
  const outcome = await assessSavedPlansReport({
    assessment: {
      ...resolved.assessment,
      terraformExecutable,
      expectedPolicySha256: policySha256,
    },
    mode: options.mode,
    request,
    ...(options.mode === "assert-adoptable"
      ? { guidanceSource: assessmentGuidanceSource(inputs.root) }
      : {}),
  });
  emitAssessment(outcome.report, emit);
  if (outcome.failure !== null) {
    await writeErrorReportBestEffort({
      emit,
      path: options.reportPath,
      report: outcome.report,
      ...(options.stdout === undefined ? {} : { stdout: options.stdout }),
    });
    throw outcome.failure;
  }
  await writeAssessmentReport({
    path: options.reportPath,
    report: outcome.report,
    ...(options.stdout === undefined ? {} : { stdout: options.stdout }),
  });
  if (outcome.report.summary.blocked > 0) throw blockedFailure(outcome.report);
  if (options.mode === "assert-clean") {
    emit(`all ${outcome.report.summary.checked} saved plan(s) clean (no-op/imports only)`);
  } else if (outcome.report.summary.tolerated > 0) {
    emit(
      `${outcome.report.summary.tolerated} saved plan(s) adoptable with `
        + "consumer-tolerated drift",
    );
  } else {
    emit(`all ${outcome.report.summary.checked} saved plan(s) clean`);
  }
}
