import type { DriftPolicy } from "./drift-policy.js";
import { validateAssessmentPlan } from "./plan-contract.js";
import {
  isJsonRecord as isRecord,
  pythonJsonEqual,
} from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";

export { pythonJsonEqual } from "../json/python-equality.js";

export const CLEAN = "clean" as const;
export const TOLERATED = "clean_with_tolerated_drift" as const;
export const BLOCKED = "blocked" as const;
export const OPAQUE_UPDATE = "<opaque_update>" as const;
export const IDENTITY_CHANGE = "<identity_change>" as const;
export const SENSITIVITY_CHANGE = "<sensitivity_change>" as const;

export type PlanStatus = typeof CLEAN | typeof TOLERATED | typeof BLOCKED;
export type PlanPath = readonly (string | number)[];

export interface PlanFinding {
  readonly status: PlanStatus;
  readonly source: "resource_changes" | "resource_drift";
  readonly address: string;
  readonly actions: readonly string[];
  readonly paths: readonly PlanPath[];
}

export interface PlanClassification {
  readonly status: PlanStatus;
  readonly findings: readonly PlanFinding[];
}

export function diffPaths(
  before: unknown,
  after: unknown,
  path: PlanPath = [],
): PlanPath[] {
  if (pythonJsonEqual(before, after)) {
    return [];
  }
  if (isRecord(before) && isRecord(after)) {
    const paths: PlanPath[] = [];
    const keys = sortedStrings(new Set([...Object.keys(before), ...Object.keys(after)]));
    for (const key of keys) {
      paths.push(...diffPaths(before[key] ?? null, after[key] ?? null, [...path, key]));
    }
    return paths;
  }
  if (Array.isArray(before) && Array.isArray(after)) {
    const paths: PlanPath[] = [];
    const length = Math.max(before.length, after.length);
    for (let index = 0; index < length; index += 1) {
      paths.push(...diffPaths(
        index < before.length ? before[index] : null,
        index < after.length ? after[index] : null,
        [...path, index],
      ));
    }
    return paths;
  }
  return [path];
}

export function truthyPaths(value: unknown, path: PlanPath = []): PlanPath[] {
  if (value === true) {
    return [path];
  }
  if (isRecord(value)) {
    return sortedStrings(Object.keys(value)).flatMap((key) => {
      return truthyPaths(value[key], [...path, key]);
    });
  }
  if (Array.isArray(value)) {
    return value.flatMap((child, index) => truthyPaths(child, [...path, index]));
  }
  return [];
}

function compareText(left: string, right: string): number {
  return sortedStrings([left, right])[0] === left
    ? left === right ? 0 : -1
    : 1;
}

function comparePaths(left: PlanPath, right: PlanPath): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const compared = compareText(String(left[index]), String(right[index]));
    if (compared !== 0) {
      return compared;
    }
  }
  return left.length - right.length;
}

function blocked(
  source: PlanFinding["source"],
  address: string,
  actions: ReadonlySet<string>,
  paths: readonly PlanPath[],
): PlanFinding {
  return {
    status: BLOCKED,
    source,
    address,
    actions: sortedStrings(actions),
    paths,
  };
}

function updatePaths(change: Record<string, unknown>): PlanPath[] {
  const unique = new Map<string, PlanPath>();
  let opaque = false;
  const candidates = [
    ...diffPaths(change.before, change.after),
    ...truthyPaths(change.after_unknown),
  ];
  for (const path of candidates) {
    if (path.length === 0) {
      opaque = true;
    } else {
      unique.set(JSON.stringify(path), path);
    }
  }
  if (!pythonJsonEqual(
    change.before_identity ?? null,
    change.after_identity ?? null,
  )) {
    unique.set(JSON.stringify([IDENTITY_CHANGE]), [IDENTITY_CHANGE]);
  }
  if (!pythonJsonEqual(
    truthyPaths(change.before_sensitive),
    truthyPaths(change.after_sensitive),
  )) {
    unique.set(JSON.stringify([SENSITIVITY_CHANGE]), [SENSITIVITY_CHANGE]);
  }
  if (opaque || unique.size === 0) {
    unique.set(JSON.stringify([OPAQUE_UPDATE]), [OPAQUE_UPDATE]);
  }
  return Array.from(unique.values()).sort(comparePaths);
}

function records(value: unknown, field: string): readonly Record<string, unknown>[] {
  if (value === undefined || value === null) {
    return [];
  }
  if (!Array.isArray(value) || !value.every(isRecord)) {
    throw new TypeError(`${field} must be an array of objects`);
  }
  return value;
}

function classifyChange(
  record: Record<string, unknown>,
  source: PlanFinding["source"],
  policy: DriftPolicy | null,
): PlanFinding[] {
  const rawChange = record.change;
  const change = rawChange === undefined || rawChange === null
    ? {}
    : isRecord(rawChange)
    ? rawChange
    : (() => { throw new TypeError(`${source} change must be an object`); })();
  const rawActions = change.actions;
  if (rawActions === undefined || rawActions === null) {
    return [];
  }
  if (!Array.isArray(rawActions) || !rawActions.every((item) => typeof item === "string")) {
    throw new TypeError(`${source} actions must be an array of strings`);
  }
  const actions = new Set(rawActions);
  if (actions.size === 0 || Array.from(actions).every((action) => action === "no-op")) {
    return [];
  }
  const address = record.address;
  if (typeof address !== "string") {
    throw new TypeError(`${source} address must be a string`);
  }
  const resourceType = record.type;
  if (typeof resourceType !== "string") {
    throw new TypeError(`${source} type must be a string`);
  }
  if (actions.has("delete")) {
    return [blocked(source, address, actions, [["<delete>"]])];
  }
  if (actions.has("create")) {
    return [blocked(source, address, actions, [["<create>"]])];
  }
  if (actions.has("update")) {
    const paths = updatePaths(change);
    const unmatched = paths.filter((candidate) => {
      return policy === null
        || !policy.toleratesPlanPath(resourceType, candidate, "update");
    });
    return unmatched.length > 0
      ? [blocked(source, address, actions, unmatched)]
      : [{ status: TOLERATED, source, address, actions: sortedStrings(actions), paths }];
  }
  return [blocked(source, address, actions, [["<unsupported_action>"]])];
}

/** Python-compatible kernel for already-validated plan objects. */
function classifyPlanUnchecked(
  plan: unknown,
  policy: DriftPolicy | null = null,
): PlanClassification {
  if (!isRecord(plan)) {
    throw new TypeError("plan must be an object");
  }
  const findings: PlanFinding[] = [];
  for (const record of records(plan.resource_changes, "resource_changes")) {
    findings.push(...classifyChange(record, "resource_changes", policy));
  }
  for (const record of records(plan.resource_drift, "resource_drift")) {
    findings.push(...classifyChange(record, "resource_drift", policy));
  }
  const status = findings.some((finding) => finding.status === BLOCKED)
    ? BLOCKED
    : findings.some((finding) => finding.status === TOLERATED)
    ? TOLERATED
    : CLEAN;
  return { status, findings };
}

/** Fail-closed assessment entry point. */
export function classifyPlan(
  plan: unknown,
  policy: DriftPolicy | null = null,
): PlanClassification {
  validateAssessmentPlan(plan);
  return classifyPlanUnchecked(plan, policy);
}
