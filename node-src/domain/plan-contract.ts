import {
  isJsonRecord as isRecord,
  terraformJsonEqual,
} from "../json/python-equality.js";
import { INFRAWRIGHT_REFERENCE_OUTPUT } from "./reference-topology.js";

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

export interface AssessmentPlanContract {
  readonly referenceOutputTypes?: readonly string[];
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
}

function validateBooleanMask(value: unknown, where: string): void {
  if (typeof value === "boolean") {
    return;
  }
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index += 1) {
      validateBooleanMask(value[index], `${where}[${index}]`);
    }
    return;
  }
  if (isRecord(value)) {
    for (const [key, child] of Object.entries(value)) {
      validateBooleanMask(child, `${where}.${key}`);
    }
    return;
  }
  fail(`${where} must be a recursive boolean mask`);
}

function booleanMaskHasTrue(value: unknown): boolean {
  if (value === true) {
    return true;
  }
  if (Array.isArray(value)) {
    return value.some(booleanMaskHasTrue);
  }
  return isRecord(value) && Object.values(value).some(booleanMaskHasTrue);
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
  if (Object.hasOwn(value, "importing")) {
    fail(`${where}.importing is not part of the Terraform resource-change contract`);
  }
  if (
    (actions[0] === "update" || actions[0] === "no-op")
    && (!Object.hasOwn(value.change, "before") || !Object.hasOwn(value.change, "after"))
  ) {
    fail(`${where}.change must bind before and after values`);
  }
  if (
    actions.length === 1
    && actions[0] === "no-op"
    && !terraformJsonEqual(value.change.before, value.change.after)
  ) {
    fail(`${where}.change no-op values must be identical`);
  }
  if (Object.hasOwn(value.change, "after_unknown")) {
    validateBooleanMask(value.change.after_unknown, `${where}.change.after_unknown`);
    if (
      actions.length === 1
      && actions[0] === "no-op"
      && booleanMaskHasTrue(value.change.after_unknown)
    ) {
      fail(`${where}.change no-op must not contain unknown values`);
    }
  }
  for (const field of ["before_sensitive", "after_sensitive"] as const) {
    if (Object.hasOwn(value.change, field)) {
      validateBooleanMask(value.change[field], `${where}.change.${field}`);
    }
  }
  if (
    actions.length === 1
    && actions[0] === "no-op"
    && (
      !terraformJsonEqual(
        value.change.before_identity ?? null,
        value.change.after_identity ?? null,
      )
      || !terraformJsonEqual(
        value.change.before_sensitive ?? {},
        value.change.after_sensitive ?? {},
      )
    )
  ) {
    fail(`${where}.change no-op metadata must be identical`);
  }
  validateImportMarker(value.change.importing, `${where}.change`);
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

function referenceOutputValue(
  plan: Record<string, unknown>,
  resourceTypes: readonly string[],
): Record<string, Record<string, string>> {
  if (
    resourceTypes.length === 0
    || new Set(resourceTypes).size !== resourceTypes.length
    || resourceTypes.some((resourceType) => !RESOURCE_TYPE.test(resourceType))
  ) {
    fail("reference output contract must contain unique Terraform resource types");
  }
  const plannedValues = plan.planned_values;
  if (!isRecord(plannedValues) || !isRecord(plannedValues.root_module)) {
    fail("reference output authorization requires planned root-module values");
  }
  const rootModule = plannedValues.root_module;
  const childModules = rootModule.child_modules;
  if (childModules !== undefined && !Array.isArray(childModules)) {
    fail("planned child modules must be an array");
  }
  const expected: Record<string, Record<string, string>> = Object.create(null) as Record<
    string,
    Record<string, string>
  >;
  for (const resourceType of resourceTypes) {
    const address = `module.${resourceType}`;
    const ids: Record<string, string> = Object.create(null) as Record<string, string>;
    const matches = (childModules ?? []).filter((child) => {
      return isRecord(child) && child.address === address;
    });
    if (matches.length > 1 || (matches.length === 1 && !isRecord(matches[0]))) {
      fail(`reference output authorization permits at most one ${address} child module`);
    }
    if (matches.length === 1) {
      const child = matches[0];
      const resources = child.resources ?? [];
      if (!Array.isArray(resources)) {
        fail(`${address}.resources must be an array`);
      }
      for (const rawResource of resources) {
        if (!isRecord(rawResource)) {
          fail(`${address}.resources entries must be objects`);
        }
        if (rawResource.mode !== "managed" || rawResource.type !== resourceType) continue;
        if (
          typeof rawResource.address !== "string"
          || !rawResource.address.startsWith(`${address}.${resourceType}.this[`)
          || typeof rawResource.index !== "string"
          || !isRecord(rawResource.values)
          || typeof rawResource.values.id !== "string"
        ) {
          fail(`${address} contains an invalid reference-output resource instance`);
        }
        if (Object.hasOwn(ids, rawResource.index)) {
          fail(`${address} contains a duplicate reference-output key`);
        }
        ids[rawResource.index] = rawResource.values.id;
      }
    }
    if (Object.keys(ids).length === 0) {
      validateEmptyReferenceModule(plan, resourceType);
    }
    expected[resourceType] = ids;
  }
  const outputs = plannedValues.outputs;
  if (!isRecord(outputs) || !isRecord(outputs[INFRAWRIGHT_REFERENCE_OUTPUT])) {
    fail("reference output authorization requires the planned engine output");
  }
  const plannedOutput = outputs[INFRAWRIGHT_REFERENCE_OUTPUT];
  if (
    plannedOutput.sensitive !== true
    || !Object.hasOwn(plannedOutput, "value")
    || !terraformJsonEqual(plannedOutput.value, expected)
  ) {
    fail("planned engine reference output does not match provider-observed resource IDs");
  }
  return expected;
}

function validateEmptyReferenceModule(
  plan: Record<string, unknown>,
  resourceType: string,
): void {
  const configuration = plan.configuration;
  if (!isRecord(configuration) || !isRecord(configuration.root_module)) {
    fail("empty reference output authorization requires root-module configuration");
  }
  const moduleCalls = configuration.root_module.module_calls;
  if (!isRecord(moduleCalls) || !isRecord(moduleCalls[resourceType])) {
    fail(`empty reference output authorization requires module.${resourceType}`);
  }
  const module = moduleCalls[resourceType].module;
  if (!isRecord(module) || !Array.isArray(module.resources)) {
    fail(`empty reference output authorization requires module.${resourceType} resources`);
  }
  const matches = module.resources.filter((resource) => {
    return isRecord(resource)
      && resource.address === `${resourceType}.this`
      && resource.mode === "managed"
      && resource.type === resourceType
      && resource.name === "this";
  });
  if (matches.length !== 1) {
    fail(`empty reference output authorization requires ${resourceType}.this configuration`);
  }
}

function validateReferenceOutputChange(options: {
  readonly change: Record<string, unknown>;
  readonly plan: Record<string, unknown>;
  readonly resourceTypes: readonly string[];
}): void {
  const expected = referenceOutputValue(options.plan, options.resourceTypes);
  const actions = options.change.actions;
  if (
    !Array.isArray(actions)
    || actions.length !== 1
    || !["create", "update", "no-op"].includes(String(actions[0]))
  ) {
    fail("engine reference output permits only create, update, or no-op actions");
  }
  if (
    !Object.hasOwn(options.change, "after")
    || !terraformJsonEqual(options.change.after, expected)
  ) {
    fail("engine reference output does not match provider-observed resource IDs");
  }
  if (actions[0] === "create" && options.change.before !== null) {
    fail("engine reference output create must start from null");
  }
  if (
    actions[0] === "update"
    && !Object.hasOwn(options.change, "before")
  ) {
    fail("engine reference output update must bind its prior value");
  }
  if (
    actions[0] === "no-op"
    && (
      !Object.hasOwn(options.change, "before")
      || !terraformJsonEqual(options.change.before, expected)
    )
  ) {
    fail("engine reference output no-op must bind the provider-observed IDs");
  }
  if (Object.hasOwn(options.change, "after_unknown")) {
    validateBooleanMask(options.change.after_unknown, "output_changes after_unknown");
    if (booleanMaskHasTrue(options.change.after_unknown)) {
      fail("engine reference output must be fully known");
    }
  }
  for (const field of ["before_sensitive", "after_sensitive"] as const) {
    if (Object.hasOwn(options.change, field)) {
      validateBooleanMask(options.change[field], `output_changes ${field}`);
    }
  }
  if (options.change.after_sensitive !== true) {
    fail("engine reference output must remain sensitive");
  }
  if (
    (actions[0] === "update" || actions[0] === "no-op")
    && options.change.before_sensitive !== true
  ) {
    fail("engine reference output existing value must preserve sensitivity");
  }
}

function validateOutputChanges(
  plan: Record<string, unknown>,
  contract: AssessmentPlanContract,
): void {
  const value = plan.output_changes;
  const resourceTypes = contract.referenceOutputTypes ?? [];
  if (resourceTypes.length > 0) referenceOutputValue(plan, resourceTypes);
  if (value === undefined) {
    if (resourceTypes.length > 0) {
      fail("reference output contract requires output_changes evidence");
    }
    return;
  }
  if (!isRecord(value)) {
    fail("output_changes must be an object");
  }
  if (resourceTypes.length > 0 && !Object.hasOwn(value, INFRAWRIGHT_REFERENCE_OUTPUT)) {
    fail("reference output contract requires the engine output change");
  }
  for (const [name, change] of Object.entries(value)) {
    if (!isRecord(change) || !Array.isArray(change.actions)) {
      fail("output_changes entries must contain actions");
    }
    if (name === INFRAWRIGHT_REFERENCE_OUTPUT && resourceTypes.length > 0) {
      validateReferenceOutputChange({ change, plan, resourceTypes });
      continue;
    }
    if (change.actions.length !== 1 || change.actions[0] !== "no-op") {
      fail("non-no-op output changes are not supported by saved-plan assessment");
    }
    if (
      !Object.hasOwn(change, "before")
      || !Object.hasOwn(change, "after")
      || !terraformJsonEqual(change.before, change.after)
    ) {
      fail("output no-op values must be identical");
    }
    if (Object.hasOwn(change, "after_unknown")) {
      validateBooleanMask(change.after_unknown, "output_changes after_unknown");
      if (booleanMaskHasTrue(change.after_unknown)) {
        fail("output no-op must not contain unknown values");
      }
    }
    for (const field of ["before_sensitive", "after_sensitive"] as const) {
      if (Object.hasOwn(change, field)) {
        validateBooleanMask(change[field], `output_changes ${field}`);
      }
    }
    if (!terraformJsonEqual(
      change.before_sensitive ?? {},
      change.after_sensitive ?? {},
    )) {
      fail("output no-op sensitivity metadata must be identical");
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
export function validateAssessmentPlan(
  plan: unknown,
  contract: AssessmentPlanContract = {},
): asserts plan is Record<string, unknown> {
  if (!isRecord(plan)) {
    fail("plan must be an object");
  }
  if (typeof plan.format_version !== "string" || !FORMAT_VERSION.test(plan.format_version)) {
    fail("plan format_version must be a supported 1.x version");
  }
  if (
    plan.terraform_version !== undefined
    && plan.terraform_version !== null
    && typeof plan.terraform_version !== "string"
  ) {
    fail("plan terraform_version must be a string when present");
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
  validateOutputChanges(plan, contract);
  validateEmptyArray(plan, "action_invocations");
  validateEmptyArray(plan, "deferred_changes");
  validateEmptyArray(plan, "deferred_action_invocations");
  validateChecks(plan.checks);
}
