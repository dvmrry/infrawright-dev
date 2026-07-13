import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "./errors.js";
import type {
  AssessmentFinding,
  AssessedSavedPlanRoot,
  SavedPlanAssessmentCore,
} from "./plan-assessment.js";
import { BLOCKED, type PlanPath, type PlanStatus } from "./plan-eval.js";
import type { PlanFingerprintV2 } from "./plan-fingerprint.js";
import type { StalePolicyEntry } from "./drift-policy.js";
import type { JsonValue } from "../json/python-compatible.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  schemaErrorDetails,
  validateSavedPlanAssessment,
} from "../contracts/validators.js";

export type AssessmentMode = "assert-clean" | "assert-adoptable";
export type AssessmentErrorKind =
  | "assessment_error"
  | "no_saved_plans"
  | "policy_error";
export type Guidance = Readonly<Record<string, JsonValue>>;

export interface AssessmentGuidanceGroup {
  readonly tenant: string;
  readonly label: string;
  readonly entries: readonly Readonly<Record<string, unknown>>[];
}

export interface AssessmentReportRequest {
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly policy: string | null;
}

export interface NormalizedAssessmentFinding {
  readonly status: PlanStatus;
  readonly source: AssessmentFinding["source"];
  readonly address: string | null;
  readonly resource_type: string | null;
  readonly actions: readonly string[];
  readonly paths: readonly string[];
}

export interface AssessmentReportRoot {
  readonly tenant: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly status: PlanStatus;
  readonly plan: AssessedSavedPlanRoot["plan"];
  readonly plan_fingerprint: PlanFingerprintV2;
  readonly findings: readonly NormalizedAssessmentFinding[];
  readonly guidance: readonly Guidance[];
}

export interface SavedPlanAssessmentReport {
  readonly kind: "infrawright.saved_plan_assessment";
  readonly schema_version: 1;
  readonly mode: AssessmentMode;
  readonly request: {
    readonly tenant: string | null;
    readonly selectors: readonly string[];
    readonly policy: string | null;
    readonly policy_sha256: string | null;
  };
  readonly summary: {
    readonly status: PlanStatus | "error";
    readonly checked: number;
    readonly clean: number;
    readonly tolerated: number;
    readonly blocked: number;
  };
  readonly roots: readonly AssessmentReportRoot[];
  readonly stale_policy: readonly StalePolicyEntry[];
  readonly error?: {
    readonly kind: string;
    readonly message: string;
  };
}

const GUIDANCE_LANES = new Set([
  "provider_config",
  "absent_default",
  "dynamic_schema",
]);
const ASSESSMENT_ERROR_KINDS = new Set<AssessmentErrorKind>([
  "assessment_error",
  "no_saved_plans",
  "policy_error",
]);

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function validatedReport(
  report: SavedPlanAssessmentReport,
): SavedPlanAssessmentReport {
  if (!validateSavedPlanAssessment(schemaValidationValue(report))) {
    throw new ProcessFailure({
      code: "INVALID_ASSESSMENT_REPORT",
      category: "internal",
      message: "saved-plan assessment report is outside schema version 1",
      details: schemaErrorDetails(validateSavedPlanAssessment.errors),
    });
  }
  return report;
}

function schemaValidationValue(value: unknown): unknown {
  if (value instanceof LosslessNumber) {
    const parsed = Number(value.toString());
    return Number.isFinite(parsed) ? parsed : 0;
  }
  if (Array.isArray(value)) return value.map(schemaValidationValue);
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, child]) => {
      return [key, schemaValidationValue(child)];
    }));
  }
  return value;
}

export function formatConcretePlanPath(path: PlanPath): string {
  if (path.length === 0) {
    return "<root>";
  }
  const parts: string[] = [];
  for (const segment of path) {
    if (segment === "[]" || segment === "*") {
      if (parts.length === 0) {
        parts.push("[]");
      } else {
        parts[parts.length - 1] = `${parts[parts.length - 1] ?? ""}[]`;
      }
    } else if (typeof segment === "number") {
      if (parts.length === 0) {
        parts.push(`[${segment}]`);
      } else {
        parts[parts.length - 1] = `${parts[parts.length - 1] ?? ""}[${segment}]`;
      }
    } else {
      parts.push(String(segment));
    }
  }
  return parts.join(".");
}

export function formatSchemaPlanPath(path: PlanPath): string {
  return formatConcretePlanPath(path.map((segment) => {
    return typeof segment === "number" || segment === "*" ? "[]" : segment;
  }));
}

function cloneJson(
  value: unknown,
  state: { nodes: number },
  depth = 0,
): JsonValue {
  state.nodes += 1;
  if (depth > 64) {
    return fail("INVALID_ASSESSMENT_GUIDANCE", "assessment guidance is too complex");
  }
  if (
    value === null
    || typeof value === "string"
    || typeof value === "boolean"
  ) {
    return value;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return fail("INVALID_ASSESSMENT_GUIDANCE", "assessment guidance is not JSON");
    }
    return value;
  }
  if (value instanceof LosslessNumber) {
    return new LosslessNumber(value.toString()) as unknown as JsonValue;
  }
  if (Array.isArray(value)) {
    return value.map((child) => cloneJson(child, state, depth + 1));
  }
  if (typeof value === "object" && value !== null) {
    const output: Record<string, JsonValue> = {};
    for (const [key, child] of Object.entries(value)) {
      if (child === undefined) {
        return fail("INVALID_ASSESSMENT_GUIDANCE", "assessment guidance is not JSON");
      }
      Object.defineProperty(output, key, {
        configurable: true,
        enumerable: true,
        value: cloneJson(child, state, depth + 1),
        writable: true,
      });
    }
    return output;
  }
  return fail("INVALID_ASSESSMENT_GUIDANCE", "assessment guidance is not JSON");
}

function normalizedFinding(finding: AssessmentFinding): NormalizedAssessmentFinding {
  return {
    status: finding.status,
    source: finding.source,
    address: finding.address,
    resource_type: finding.resource_type,
    actions: [...finding.actions],
    paths: finding.paths.map(formatConcretePlanPath),
  };
}

function guidanceForRoot(
  root: AssessedSavedPlanRoot,
  entries: readonly Readonly<Record<string, unknown>>[],
  cloneState: { nodes: number },
): Guidance[] {
  const blocked = new Map<string, string[]>();
  for (const finding of root.findings) {
    if (finding.status !== BLOCKED) {
      continue;
    }
    for (const planPath of finding.paths) {
      const key = JSON.stringify([
        finding.source,
        finding.address,
        formatConcretePlanPath(planPath),
      ]);
      const schemas = blocked.get(key) ?? [];
      schemas.push(formatSchemaPlanPath(planPath));
      blocked.set(key, schemas);
    }
  }
  const normalized = entries.map((entry) => {
    const matching = typeof entry.source === "string"
      && typeof entry.address === "string"
      && typeof entry.finding_path === "string"
      ? blocked.get(JSON.stringify([
          entry.source,
          entry.address,
          entry.finding_path,
        ])) ?? []
      : [];
    if (
      !GUIDANCE_LANES.has(String(entry.lane))
      || typeof entry.source !== "string"
      || typeof entry.address !== "string"
      || typeof entry.finding_path !== "string"
      || typeof entry.matched_plan_path !== "string"
      || typeof entry.status_effect !== "string"
      || Object.hasOwn(entry, "sort_key")
      || matching.length !== 1
      || matching[0] !== entry.matched_plan_path
    ) {
      return fail(
        "INVALID_ASSESSMENT_GUIDANCE",
        "assessment guidance is not joined to a blocked finding",
      );
    }
    return cloneJson(entry, cloneState) as Guidance;
  });
  normalized.sort(compareGuidance);
  const seen = new Set<string>();
  return normalized.filter((entry) => {
    const marker = guidanceMarker(entry);
    if (seen.has(marker)) {
      return false;
    }
    seen.add(marker);
    return true;
  });
}

const LANE_ORDER: Readonly<Record<string, number>> = {
  provider_config: 0,
  absent_default: 1,
  dynamic_schema: 2,
};

function guidanceText(entry: Guidance, key: string): string {
  const value = entry[key];
  if (value === undefined || value === null) {
    return "";
  }
  if (typeof value !== "string") {
    return fail("INVALID_ASSESSMENT_GUIDANCE", "assessment guidance sort fields are invalid");
  }
  return value;
}

function guidanceSortKey(entry: Guidance): readonly (number | string)[] {
  const lane = guidanceText(entry, "lane");
  if (lane === "provider_config") {
    return [
      LANE_ORDER[lane] ?? 99,
      guidanceText(entry, "provider"),
      guidanceText(entry, "setting"),
      guidanceText(entry, "matched_plan_path"),
    ];
  }
  return [
    LANE_ORDER[lane] ?? 99,
    guidanceText(entry, "provider"),
    guidanceText(entry, "resource_type"),
    guidanceText(entry, "matched_plan_path"),
    guidanceText(entry, "rule"),
  ];
}

function compareGuidance(left: Guidance, right: Guidance): number {
  const leftKey = guidanceSortKey(left);
  const rightKey = guidanceSortKey(right);
  for (let index = 0; index < Math.max(leftKey.length, rightKey.length); index += 1) {
    const leftPart = leftKey[index] ?? "";
    const rightPart = rightKey[index] ?? "";
    if (leftPart === rightPart) {
      continue;
    }
    if (typeof leftPart === "number" && typeof rightPart === "number") {
      return leftPart - rightPart;
    }
    return sortedStrings([String(leftPart), String(rightPart)])[0] === String(leftPart)
      ? -1
      : 1;
  }
  return 0;
}

function guidanceMarker(value: JsonValue): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "string" || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    return Object.is(value, -0) ? "-0" : JSON.stringify(value);
  }
  if (value instanceof LosslessNumber) {
    return value.toString();
  }
  if (Array.isArray(value)) {
    return `[${value.map(guidanceMarker).join(",")}]`;
  }
  const objectValue = value as Readonly<Record<string, JsonValue>>;
  return `{${sortedStrings(Object.keys(objectValue)).map((key) => {
    return `${JSON.stringify(key)}:${guidanceMarker(objectValue[key] as JsonValue)}`;
  }).join(",")}}`;
}

function rootKey(root: Pick<AssessedSavedPlanRoot, "tenant" | "label">): string {
  return JSON.stringify([root.tenant, root.label]);
}

function buildRoots(
  core: SavedPlanAssessmentCore,
  guidance: readonly AssessmentGuidanceGroup[],
): AssessmentReportRoot[] {
  const roots = new Set(core.roots.map(rootKey));
  const byRoot = new Map<string, readonly Readonly<Record<string, unknown>>[]>();
  for (const group of guidance) {
    const key = rootKey(group);
    if (!roots.has(key) || byRoot.has(key)) {
      return fail(
        "INVALID_ASSESSMENT_GUIDANCE",
        "assessment guidance references an unknown or duplicate root",
      );
    }
    byRoot.set(key, group.entries);
  }
  const cloneState = { nodes: 0 };
  return core.roots.map((root) => {
    const derived = root.findings.some((finding) => finding.status === BLOCKED)
      ? BLOCKED
      : root.findings.some((finding) => {
          return finding.status === "clean_with_tolerated_drift";
        })
      ? "clean_with_tolerated_drift"
      : "clean";
    if (
      root.status !== derived
      || root.findings.some((finding) => finding.status === "clean")
    ) {
      return fail(
        "INVALID_ASSESSMENT_REPORT",
        "assessment root status and findings are inconsistent",
      );
    }
    return {
      tenant: root.tenant,
      label: root.label,
      members: [...root.members],
      status: derived,
      plan: { ...root.plan },
      plan_fingerprint: { ...root.plan_fingerprint },
      findings: root.findings.map(normalizedFinding),
      guidance: guidanceForRoot(
        root,
        byRoot.get(rootKey(root)) ?? [],
        cloneState,
      ),
    };
  });
}

function counts(roots: readonly AssessmentReportRoot[]) {
  return {
    checked: roots.length,
    clean: roots.filter((root) => root.status === "clean").length,
    tolerated: roots.filter((root) => {
      return root.status === "clean_with_tolerated_drift";
    }).length,
    blocked: roots.filter((root) => root.status === "blocked").length,
  };
}

function statusFromCounts(summary: ReturnType<typeof counts>): PlanStatus {
  return summary.blocked > 0
    ? "blocked"
    : summary.tolerated > 0
    ? "clean_with_tolerated_drift"
    : "clean";
}

export function buildSavedPlanAssessmentReport(options: {
  readonly mode: AssessmentMode;
  readonly request: AssessmentReportRequest;
  readonly core: SavedPlanAssessmentCore;
  readonly guidance?: readonly AssessmentGuidanceGroup[];
}): SavedPlanAssessmentReport {
  if (
    (options.mode === "assert-clean" && options.request.policy !== null)
    || (options.request.policy === null && options.core.policy_sha256 !== null)
    || (options.request.policy !== null && options.core.policy_sha256 === null)
  ) {
    return fail("INVALID_ASSESSMENT_REPORT", "assessment request and policy evidence disagree");
  }
  const roots = buildRoots(options.core, options.guidance ?? []);
  const summaryCounts = counts(roots);
  if (
    roots.length === 0
    || summaryCounts.checked !== options.core.checked
    || summaryCounts.clean !== options.core.clean
    || summaryCounts.tolerated !== options.core.tolerated
    || summaryCounts.blocked !== options.core.blocked
    || statusFromCounts(summaryCounts) !== options.core.status
  ) {
    return fail("INVALID_ASSESSMENT_REPORT", "assessment summary counts are inconsistent");
  }
  return validatedReport({
    kind: "infrawright.saved_plan_assessment",
    schema_version: 1,
    mode: options.mode,
    request: {
      tenant: options.request.tenant,
      selectors: [...options.request.selectors],
      policy: options.mode === "assert-clean" ? null : options.request.policy,
      policy_sha256: options.mode === "assert-clean"
        ? null
        : options.core.policy_sha256,
    },
    summary: {
      status: statusFromCounts(summaryCounts),
      ...summaryCounts,
    },
    roots,
    stale_policy: options.core.stale_policy.map((entry) => ({ ...entry })),
  });
}

export function buildSavedPlanAssessmentErrorReport(options: {
  readonly mode: AssessmentMode;
  readonly request: AssessmentReportRequest;
  readonly partial: SavedPlanAssessmentCore;
  readonly error: { readonly kind: AssessmentErrorKind; readonly message: string };
  readonly guidance?: readonly AssessmentGuidanceGroup[];
}): SavedPlanAssessmentReport {
  if (
    options.error.kind.length === 0
    || options.error.message.length === 0
    || !ASSESSMENT_ERROR_KINDS.has(options.error.kind)
    || (options.mode === "assert-clean" && options.request.policy !== null)
  ) {
    return fail("INVALID_ASSESSMENT_REPORT", "assessment error input is invalid");
  }
  const roots = buildRoots(
    options.partial,
    options.guidance ?? [],
  );
  return validatedReport({
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
    summary: { status: "error", ...counts(roots) },
    roots,
    stale_policy: options.partial.stale_policy.map((entry) => ({ ...entry })),
    error: { ...options.error },
  });
}
