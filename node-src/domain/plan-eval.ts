import { LosslessNumber } from "lossless-json";

import type { DriftPolicy } from "./drift-policy.js";
import { validateAssessmentPlan } from "./plan-contract.js";
import { sortedStrings } from "../json/python-compatible.js";

export const CLEAN = "clean" as const;
export const TOLERATED = "clean_with_tolerated_drift" as const;
export const BLOCKED = "blocked" as const;
export const OPAQUE_UPDATE = "<opaque_update>" as const;

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object"
    && value !== null
    && !Array.isArray(value)
    && !(value instanceof LosslessNumber);
}

function pythonTruthy(value: unknown): boolean {
  if (value instanceof LosslessNumber) {
    const numeric = numericValue(value);
    return numeric?.kind === "integer"
      ? numeric.integer !== 0n
      : numeric?.float !== 0;
  }
  if (value === null || value === false || value === 0 || value === "") {
    return false;
  }
  if (Array.isArray(value)) {
    return value.length > 0;
  }
  if (isRecord(value)) {
    return Object.keys(value).length > 0;
  }
  return value !== undefined;
}

interface NumericValue {
  readonly kind: "integer" | "float";
  readonly integer?: bigint;
  readonly float?: number;
}

function numericValue(value: unknown): NumericValue | null {
  if (typeof value === "boolean") {
    return { kind: "integer", integer: value ? 1n : 0n };
  }
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    if (/^-?(?:0|[1-9][0-9]*)$/.test(token)) {
      return { kind: "integer", integer: BigInt(token) };
    }
    return { kind: "float", float: Number(token) };
  }
  if (typeof value === "number") {
    if (Number.isSafeInteger(value) && !Object.is(value, -0)) {
      return { kind: "integer", integer: BigInt(value) };
    }
    return { kind: "float", float: value };
  }
  return null;
}

function numericallyEqual(left: NumericValue, right: NumericValue): boolean {
  if (left.kind === "integer" && right.kind === "integer") {
    return left.integer === right.integer;
  }
  if (left.kind === "float" && right.kind === "float") {
    return left.float === right.float;
  }
  const integer = left.kind === "integer" ? left.integer : right.integer;
  const float = left.kind === "float" ? left.float : right.float;
  return integer !== undefined
    && float !== undefined
    && Number.isFinite(float)
    && Number.isInteger(float)
    && BigInt(float) === integer;
}

/** Match Python JSON equality without truncating integer tokens. */
export function pythonJsonEqual(left: unknown, right: unknown): boolean {
  const leftNumber = numericValue(left);
  const rightNumber = numericValue(right);
  if (leftNumber !== null || rightNumber !== null) {
    return leftNumber !== null
      && rightNumber !== null
      && numericallyEqual(leftNumber, rightNumber);
  }
  if (left === null || right === null) {
    return left === right;
  }
  if (typeof left === "string" || typeof right === "string") {
    return typeof left === "string" && left === right;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => pythonJsonEqual(value, right[index]));
  }
  if (isRecord(left) || isRecord(right)) {
    if (!isRecord(left) || !isRecord(right)) {
      return false;
    }
    const leftKeys = sortedStrings(Object.keys(left));
    const rightKeys = sortedStrings(Object.keys(right));
    return leftKeys.length === rightKeys.length
      && leftKeys.every((key, index) => key === rightKeys[index])
      && leftKeys.every((key) => pythonJsonEqual(left[key], right[key]));
  }
  return left === right;
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
  const importing = pythonTruthy(change.importing) || pythonTruthy(record.importing);
  if (importing && Array.from(actions).every((action) => action === "create")) {
    return [{ status: CLEAN, source, address, actions: sortedStrings(actions), paths: [] }];
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
