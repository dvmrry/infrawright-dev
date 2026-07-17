import { LosslessNumber } from "lossless-json";

import {
  parsePolicyPath,
  POLICY_WILDCARD,
  PolicyPathError,
  policyPathHasWildcardOrIndex,
  policyPathsEqual,
  policySelectorMatches,
  type ConcretePathSegment,
} from "./policy-paths.js";
import { terraformJsonExactlyEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";

const TOP_LEVEL_KEYS = new Set(["version", "resource_types"]);
const RESOURCE_KEYS = new Set([
  "projection_omit",
  "projection_sync",
  "projection_fill",
  "projection_omit_if",
  "plan_tolerate",
]);
const COMMON_KEYS = new Set(["path", "reason", "approved_by", "ticket"]);
const MODES = [
  "projection_omit",
  "projection_sync",
  "projection_fill",
  "projection_omit_if",
  "plan_tolerate",
] as const;
type PolicyMode = typeof MODES[number];
export type PolicyEntry = Record<string, unknown>;

const MAX_POLICY_ENTRIES = 50_000;
const MAX_PLAN_TOLERATE_WILDCARDS_PER_RESOURCE = 1_000;

interface CompiledPlanTolerate {
  readonly entry: PolicyEntry;
  readonly order: number;
  readonly selector: readonly (string | number | bigint)[];
}

export class DriftPolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DriftPolicyError";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function fail(message: string): never {
  throw new DriftPolicyError(message);
}

/** Accept only the exact JSON-number value one, including lossless spellings. */
export function isSupportedDriftPolicyVersion(value: unknown): boolean {
  return terraformJsonExactlyEqual(value, 1);
}

function rejectUnknownKeys(
  object: Record<string, unknown>,
  allowed: ReadonlySet<string>,
  where: string,
): void {
  const unknown = sortedStrings(Object.keys(object).filter((key) => !allowed.has(key)));
  if (unknown.length > 0) {
    fail(`${where} has unknown key ${unknown[0]}`);
  }
}

function entriesFor(
  data: Record<string, unknown>,
  resourceType: string,
  mode: PolicyMode,
): PolicyEntry[] {
  const resources = data.resource_types;
  if (!isRecord(resources)) {
    return [];
  }
  const resource = resources[resourceType];
  if (!isRecord(resource)) {
    return [];
  }
  const entries = resource[mode];
  return Array.isArray(entries) ? entries as PolicyEntry[] : [];
}

function entryKeys(mode: PolicyMode): Set<string> {
  if (mode === "projection_sync") {
    return new Set(["target_path", "source_path", "reason", "approved_by", "ticket"]);
  }
  if (mode === "projection_fill") {
    return new Set(["path", "source", "reason", "approved_by", "ticket"]);
  }
  if (mode === "projection_omit_if") {
    return new Set([...COMMON_KEYS, "values"]);
  }
  if (mode === "plan_tolerate") {
    return new Set([...COMMON_KEYS, "actions"]);
  }
  return COMMON_KEYS;
}

function requiredStrings(mode: PolicyMode): readonly string[] {
  if (mode === "projection_sync") {
    return ["target_path", "source_path", "reason", "approved_by"];
  }
  if (mode === "projection_fill") {
    return ["path", "source", "reason", "approved_by"];
  }
  return ["path", "reason", "approved_by"];
}

function requireString(entry: PolicyEntry, key: string, context: string): string {
  const value = entry[key];
  if (typeof value !== "string" || value.length === 0) {
    return fail(`${context} missing ${key}`);
  }
  return value;
}

function isJsonScalar(value: unknown): boolean {
  return value === null
    || typeof value === "string"
    || (typeof value === "number" && Number.isFinite(value))
    || typeof value === "boolean"
    || value instanceof LosslessNumber;
}

function numericScalarMarker(value: number | LosslessNumber): string {
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    if (/^-?(?:0|[1-9][0-9]*)$/u.test(token)) {
      return `integer:${String(BigInt(token))}`;
    }
    const numeric = Number(token);
    return Number.isFinite(numeric) && Number.isInteger(numeric)
      ? `integer:${String(BigInt(numeric))}`
      : `float:${String(numeric)}`;
  }
  if (Number.isSafeInteger(value) && !Object.is(value, -0)) {
    return `integer:${String(BigInt(value))}`;
  }
  return Number.isInteger(value)
    ? `integer:${String(BigInt(value))}`
    : `float:${String(value)}`;
}

function jsonScalarMarker(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return `boolean:${String(value)}`;
  if (typeof value === "string") return `string:${JSON.stringify(value)}`;
  if (typeof value === "number" || value instanceof LosslessNumber) {
    return `number:${numericScalarMarker(value)}`;
  }
  return fail("drift policy scalar marker received a non-scalar value");
}

function policyPath(value: unknown) {
  try {
    return parsePolicyPath(value);
  } catch (error: unknown) {
    if (error instanceof PolicyPathError) {
      return fail(error.message);
    }
    throw error;
  }
}

function pathMarker(path: readonly (string | number | bigint)[]): string {
  return JSON.stringify(path.map((segment) => {
    return typeof segment === "bigint"
      ? ["bigint", String(segment)]
      : [typeof segment, segment];
  }));
}

function validateEntry(
  source: string,
  resourceType: string,
  mode: PolicyMode,
  entry: unknown,
): string {
  const context = `${source} ${mode} entry for ${resourceType}`;
  if (!isRecord(entry)) {
    return fail(`${context} must be an object`);
  }
  rejectUnknownKeys(entry, entryKeys(mode), context);
  for (const key of requiredStrings(mode)) {
    requireString(entry, key, context);
  }
  if (Object.hasOwn(entry, "ticket")) {
    const ticket = entry.ticket;
    if (typeof ticket !== "string" || ticket.length === 0) {
      fail(`${context} has invalid ticket`);
    }
  }

  if (mode === "projection_sync") {
    const targetText = requireString(entry, "target_path", context);
    const sourceText = requireString(entry, "source_path", context);
    const target = policyPath(targetText);
    const sourcePath = policyPath(sourceText);
    if (policyPathsEqual(target, sourcePath)) {
      fail(`${source} projection_sync entry for ${resourceType} target_path and source_path must differ`);
    }
    if (policyPathHasWildcardOrIndex(target)) {
      fail(`${source} projection_sync entry for ${resourceType} target_path must not contain wildcard or index selectors`);
    }
    if (policyPathHasWildcardOrIndex(sourcePath)) {
      fail(`${source} projection_sync entry for ${resourceType} source_path must not contain wildcard or index selectors`);
    }
    return `projection_sync\0${targetText}`;
  }

  const pathText = requireString(entry, "path", context);
  const parsed = policyPath(pathText);
  if (mode === "projection_fill") {
    const sourceText = requireString(entry, "source", context);
    const sourcePath = policyPath(sourceText);
    if (parsed.length !== 1) {
      fail(`${source} projection_fill entry for ${resourceType} path must be a single top-level name`);
    }
    if (sourcePath.length !== 1) {
      fail(`${source} projection_fill entry for ${resourceType} source must be a single top-level raw API name`);
    }
    if (policyPathHasWildcardOrIndex(parsed)) {
      fail(`${source} projection_fill entry for ${resourceType} path must not contain wildcard or index selectors`);
    }
    if (policyPathHasWildcardOrIndex(sourcePath)) {
      fail(`${source} projection_fill entry for ${resourceType} source must not contain wildcard or index selectors`);
    }
    return `projection_fill\0${pathText}`;
  }
  if (mode === "projection_omit_if") {
    const values = entry.values;
    if (!Array.isArray(values) || values.length === 0) {
      fail(`${source} projection_omit_if entry for ${resourceType} values must be a non-empty JSON list`);
    }
    if (!values.every(isJsonScalar)) {
      fail(`${source} projection_omit_if entry for ${resourceType} values must contain only JSON scalars`);
    }
    return `projection_omit_if\0${pathText}\0${JSON.stringify(values.map(jsonScalarMarker))}`;
  }
  if (mode === "plan_tolerate") {
    const rawActions = Object.hasOwn(entry, "actions")
      ? entry.actions
      : ["update"];
    if (!Array.isArray(rawActions)) {
      fail(`${source} plan_tolerate entries for ${resourceType} actions must be a list`);
    }
    if (rawActions.length === 0) {
      fail(`${source} plan_tolerate entry for ${resourceType} actions must not be empty`);
    }
    const seen = new Set<string>();
    for (const action of rawActions) {
      if (typeof action !== "string" || action.length === 0) {
        fail(`${source} plan_tolerate entry for ${resourceType} has invalid action`);
      }
      if (action !== "update") {
        fail(`${source} plan_tolerate entry for ${resourceType} has unsupported action ${JSON.stringify(action)}`);
      }
      if (seen.has(action)) {
        fail(`${source} plan_tolerate entry for ${resourceType} has duplicate action ${JSON.stringify(action)}`);
      }
      seen.add(action);
    }
    return `plan_tolerate\0${pathText}\0${sortedStrings(seen).join("\0")}`;
  }
  return `projection_omit\0${pathText}\0${pathMarker(parsed)}`;
}

function validatePolicy(data: unknown, source: string): Record<string, unknown> {
  if (!isRecord(data)) {
    return fail(`${source}: drift policy must be an object`);
  }
  rejectUnknownKeys(data, TOP_LEVEL_KEYS, `${source} top-level drift policy`);
  if (!Object.hasOwn(data, "version")) {
    fail(`${source}: drift policy missing version`);
  }
  if (!isSupportedDriftPolicyVersion(data.version)) {
    fail(`${source}: unsupported drift policy version`);
  }
  if (!Object.hasOwn(data, "resource_types")) {
    fail(`${source}: drift policy missing resource_types`);
  }
  if (!isRecord(data.resource_types)) {
    fail(`${source}: resource_types must be an object`);
  }
  let entryCount = 0;
  for (const resourceType of sortedStrings(Object.keys(data.resource_types))) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(resourceType)) {
      fail(`${source}: invalid resource type ${JSON.stringify(resourceType)}`);
    }
    const resource = data.resource_types[resourceType];
    if (!isRecord(resource)) {
      fail(`${source}: policy for ${resourceType} must be an object`);
    }
    rejectUnknownKeys(resource, RESOURCE_KEYS, `${source} policy for ${resourceType}`);
    for (const mode of MODES) {
      const rawEntries = Object.hasOwn(resource, mode) ? resource[mode] : [];
      if (!Array.isArray(rawEntries)) {
        fail(`${source} ${mode} entries for ${resourceType} must be a list`);
      }
      entryCount += rawEntries.length;
      if (entryCount > MAX_POLICY_ENTRIES) {
        fail(`${source}: drift policy exceeds the entry-count limit`);
      }
      const scopes = new Set<string>();
      for (const entry of rawEntries) {
        const scope = validateEntry(source, resourceType, mode, entry);
        if (scopes.has(scope)) {
          const display = isRecord(entry)
            ? entry.path ?? entry.target_path
            : "unknown";
          fail(`${source} duplicate ${mode} entry for ${resourceType} path ${String(display)}`);
        }
        scopes.add(scope);
      }
    }
    const fill = new Map<string, string>();
    for (const entry of entriesFor(data, resourceType, "projection_fill")) {
      const text = entry.path as string;
      fill.set(pathMarker(policyPath(text)), text);
    }
    for (const entry of entriesFor(data, resourceType, "projection_omit")) {
      const text = entry.path as string;
      if (fill.has(pathMarker(policyPath(text)))) {
        fail(`${source} projection_fill and projection_omit entries for ${resourceType} conflict on path ${text}`);
      }
    }
  }
  return data;
}

export interface StalePolicyEntry {
  readonly resource_type: string;
  readonly mode: PolicyMode;
  readonly path: string;
}

export class DriftPolicy {
  readonly data: Record<string, unknown>;
  readonly source: string;
  private readonly matched = new WeakSet<object>();
  private readonly exactPlanTolerate = new Map<
    string,
    ReadonlyMap<string, CompiledPlanTolerate>
  >();
  private readonly wildcardPlanTolerate = new Map<
    string,
    readonly CompiledPlanTolerate[]
  >();

  constructor(data: unknown = null, source = "<memory>") {
    this.source = source;
    this.data = validatePolicy(
      data === null ? { version: 1, resource_types: {} } : data,
      source,
    );
    const resources = this.data.resource_types as Record<string, unknown>;
    for (const resourceType of Object.keys(resources)) {
      const exact = new Map<string, CompiledPlanTolerate>();
      const wildcard: CompiledPlanTolerate[] = [];
      for (const [order, entry] of this.entries(
        resourceType,
        "plan_tolerate",
      ).entries()) {
        const selector = policyPath(entry.path);
        const compiled = { entry, order, selector };
        if (selector.includes(POLICY_WILDCARD)) {
          wildcard.push(compiled);
        } else if (!exact.has(pathMarker(selector))) {
          // Python walks entries in declaration order. Textually distinct
          // selectors such as field[0] and field[00] can canonicalize to the
          // same path, so retain the first and leave later aliases stale.
          exact.set(pathMarker(selector), compiled);
        }
      }
      if (wildcard.length > MAX_PLAN_TOLERATE_WILDCARDS_PER_RESOURCE) {
        fail(`${source}: plan_tolerate wildcard entries exceed the per-resource limit`);
      }
      this.exactPlanTolerate.set(resourceType, exact);
      this.wildcardPlanTolerate.set(resourceType, wildcard);
    }
  }

  entries(resourceType: string, mode: PolicyMode): PolicyEntry[] {
    return [...entriesFor(this.data, resourceType, mode)];
  }

  markMatched(entry: PolicyEntry): void {
    this.matched.add(entry);
  }

  projectionOmits(
    resourceType: string,
    path: readonly ConcretePathSegment[],
  ): boolean {
    for (const entry of this.entries(resourceType, "projection_omit")) {
      if (policySelectorMatches(policyPath(entry.path), path)) {
        this.matched.add(entry);
        return true;
      }
    }
    return false;
  }

  toleratesPlanPath(
    resourceType: string,
    path: readonly ConcretePathSegment[],
    action: string,
  ): boolean {
    if (action !== "update") {
      return false;
    }
    const candidates: CompiledPlanTolerate[] = [];
    const exact = this.exactPlanTolerate.get(resourceType)?.get(pathMarker(path));
    if (exact !== undefined && policySelectorMatches(exact.selector, path)) {
      candidates.push(exact);
    }
    for (const candidate of this.wildcardPlanTolerate.get(resourceType) ?? []) {
      if (policySelectorMatches(candidate.selector, path)) {
        candidates.push(candidate);
      }
    }
    candidates.sort((left, right) => left.order - right.order);
    const matched = candidates[0];
    if (matched !== undefined) {
      this.matched.add(matched.entry);
      return true;
    }
    return false;
  }

  staleEntries(options: {
    resourceTypes?: ReadonlySet<string>;
    modes?: readonly PolicyMode[];
  } = {}): StalePolicyEntry[] {
    const modes = options.modes === undefined || options.modes.length === 0
      ? MODES
      : options.modes;
    const stale: StalePolicyEntry[] = [];
    const resources = this.data.resource_types as Record<string, unknown>;
    for (const resourceType of sortedStrings(Object.keys(resources))) {
      if (
        options.resourceTypes !== undefined
        && options.resourceTypes.size > 0
        && !options.resourceTypes.has(resourceType)
      ) {
        continue;
      }
      for (const mode of modes) {
        for (const entry of this.entries(resourceType, mode)) {
          if (!this.matched.has(entry)) {
            stale.push({
              resource_type: resourceType,
              mode,
              path: String(entry.path ?? entry.target_path),
            });
          }
        }
      }
    }
    return stale;
  }
}
