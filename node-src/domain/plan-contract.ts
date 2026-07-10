import { LosslessNumber } from "lossless-json";

const FORMAT_VERSION = /^1\.[0-9]+$/;
const RESOURCE_TYPE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const ACTION_SEQUENCES = new Set([
  '["no-op"]',
  '["create"]',
  '["read"]',
  '["update"]',
  '["delete","create"]',
  '["create","delete"]',
  '["delete"]',
  '["forget"]',
  '["create","forget"]',
]);
const CHECK_STATUSES = new Set(["pass", "unknown", "fail", "error"]);

export const MAX_ASSESSMENT_CHANGE_RECORDS = 100_000;

export class AssessmentPlanError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AssessmentPlanError";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object"
    && value !== null
    && !Array.isArray(value)
    && !(value instanceof LosslessNumber);
}

function fail(message: string): never {
  throw new AssessmentPlanError(message);
}

function validateImportMarker(value: unknown, where: string): void {
  if (value === undefined || value === null) {
    return;
  }
  if (!isRecord(value)) {
    fail(`${where} importing marker must be an object`);
  }
  const hasId = typeof value.id === "string" && value.id.length > 0;
  const hasIdentity = isRecord(value.identity)
    && Object.keys(value.identity).length > 0;
  if (!hasId && !hasIdentity) {
    fail(`${where} importing marker must contain an id or identity`);
  }
}

function validateChangeRecord(value: unknown, where: string): void {
  if (!isRecord(value)) {
    fail(`${where} must be an object`);
  }
  if (typeof value.address !== "string" || value.address.length === 0) {
    fail(`${where}.address must be a non-empty string`);
  }
  if (typeof value.type !== "string" || !RESOURCE_TYPE.test(value.type)) {
    fail(`${where}.type must be a Terraform resource type`);
  }
  if (!isRecord(value.change)) {
    fail(`${where}.change must be an object`);
  }
  const actions = value.change.actions;
  if (
    !Array.isArray(actions)
    || actions.length === 0
    || !actions.every((action) => typeof action === "string")
  ) {
    fail(`${where}.change.actions must be a non-empty string array`);
  }
  if (new Set(actions).size !== actions.length) {
    fail(`${where}.change.actions must not contain duplicates`);
  }
  if (!ACTION_SEQUENCES.has(JSON.stringify(actions))) {
    fail(`${where}.change.actions is not a supported Terraform action sequence`);
  }
  if (
    (actions[0] === "update" || actions[0] === "no-op")
    && (!Object.hasOwn(value.change, "before") || !Object.hasOwn(value.change, "after"))
  ) {
    fail(`${where}.change must bind before and after values`);
  }
  validateImportMarker(value.change.importing, `${where}.change`);
  validateImportMarker(value.importing, where);
}

function records(
  value: unknown,
  field: "resource_changes" | "resource_drift",
): readonly unknown[] {
  if (value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    fail(`${field} must be an array`);
  }
  return value;
}

function validateEmptyArray(plan: Record<string, unknown>, field: string): void {
  const value = plan[field];
  if (value === undefined) {
    return;
  }
  if (!Array.isArray(value)) {
    fail(`${field} must be an array`);
  }
  if (value.length > 0) {
    fail(`${field} is not supported by saved-plan assessment`);
  }
}

function validateOutputChanges(value: unknown): void {
  if (value === undefined) {
    return;
  }
  if (!isRecord(value)) {
    fail("output_changes must be an object");
  }
  for (const change of Object.values(value)) {
    if (!isRecord(change) || !Array.isArray(change.actions)) {
      fail("output_changes entries must contain actions");
    }
    if (change.actions.length !== 1 || change.actions[0] !== "no-op") {
      fail("non-no-op output changes are not supported by saved-plan assessment");
    }
  }
}

function validateCheckStatus(value: unknown, where: string): void {
  if (!isRecord(value)) {
    fail(`${where} must be an object`);
  }
  const status = value.status;
  if (typeof status !== "string" || !CHECK_STATUSES.has(status)) {
    fail(`${where}.status is invalid`);
  }
  if (status === "fail" || status === "error") {
    fail("failed Terraform checks are not supported by saved-plan assessment");
  }
}

function validateChecks(value: unknown): void {
  if (value === undefined) {
    return;
  }
  if (!Array.isArray(value)) {
    fail("checks must be an array");
  }
  for (let checkIndex = 0; checkIndex < value.length; checkIndex += 1) {
    const check = value[checkIndex];
    validateCheckStatus(check, `checks[${checkIndex}]`);
    if (!isRecord(check) || check.instances === undefined) {
      continue;
    }
    if (!Array.isArray(check.instances)) {
      fail(`checks[${checkIndex}].instances must be an array`);
    }
    for (let instanceIndex = 0; instanceIndex < check.instances.length; instanceIndex += 1) {
      validateCheckStatus(
        check.instances[instanceIndex],
        `checks[${checkIndex}].instances[${instanceIndex}]`,
      );
    }
  }
}

/**
 * Validate the narrow Terraform plan surface consumed by saved-plan assessment.
 * Unknown object properties remain allowed for forward-compatible 1.x additions.
 */
export function validateAssessmentPlan(plan: unknown): asserts plan is Record<string, unknown> {
  if (!isRecord(plan)) {
    fail("plan must be an object");
  }
  if (typeof plan.format_version !== "string" || !FORMAT_VERSION.test(plan.format_version)) {
    fail("plan format_version must be a supported 1.x version");
  }
  if (plan.complete !== true) {
    fail("plan must be complete before assessment");
  }
  if (plan.errored !== false) {
    fail("errored plans cannot be assessed");
  }
  const changes = records(plan.resource_changes, "resource_changes");
  const drift = records(plan.resource_drift, "resource_drift");
  if (changes.length + drift.length > MAX_ASSESSMENT_CHANGE_RECORDS) {
    fail(`plan exceeds ${MAX_ASSESSMENT_CHANGE_RECORDS} change records`);
  }
  for (let index = 0; index < changes.length; index += 1) {
    validateChangeRecord(changes[index], `resource_changes[${index}]`);
  }
  for (let index = 0; index < drift.length; index += 1) {
    validateChangeRecord(drift[index], `resource_drift[${index}]`);
  }
  validateOutputChanges(plan.output_changes);
  validateEmptyArray(plan, "action_invocations");
  validateEmptyArray(plan, "deferred_changes");
  validateEmptyArray(plan, "deferred_action_invocations");
  validateChecks(plan.checks);
}
