import { createHash } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";

import { LosslessNumber } from "lossless-json";

import type { LoadedPackRoot } from "../metadata/loader.js";
import { manifestForProvider } from "../metadata/packs.js";
import { isObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import {
  comparePythonStrings,
  sortedStrings,
} from "../json/python-compatible.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { adoptResourceItems, loadAdoptionPolicy } from "./adopt-runner.js";
import type { OracleStateObject } from "./import-oracle.js";
import { transformResourceItems } from "./transform-runner.js";

export const TRANSFORM_ADOPT_PARITY_REPORT_KIND = "infrawright.transform_adopt_parity";
export const TRANSFORM_ADOPT_PARITY_REPORT_VERSION = 1;
export const TRANSFORM_ADOPT_PARITY_FIXTURE_VERSION = 1;

const FIXTURE_KEYS = new Set([
  "fixture_version", "name", "resource_type", "provenance", "raw_items",
  "provider_state", "expected_differences",
]);
const PROVENANCE_KEYS = new Set([
  "status", "provider_version", "sources", "dependency_sources",
  "local_sources", "sanitized", "note",
]);
const DEPENDENCY_SOURCE_KEYS = new Set(["name", "version", "url"]);
const STATE_KEYS = new Set(["values", "sensitive_values"]);
const EXPECTATION_KEYS = new Set([
  "path", "transform", "adopt", "classification", "disposition", "reason", "evidence",
]);
const SIDE_KEYS = new Set(["present", "value"]);
const PROVENANCE_STATUSES = new Set(["source_derived", "sanitized_live"]);
const CLASSIFICATIONS = new Set([
  "semantic_mismatch", "validation_asymmetry", "representational_difference",
  "provider_normalization", "other",
]);
const DISPOSITIONS = new Set(["accepted", "evidence_gate"]);

type JsonRecord = Record<string, unknown>;

export class TransformAdoptParityError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TransformAdoptParityError";
  }
}

export interface TransformAdoptParityContext {
  readonly repositoryRoot?: string;
  readonly root: LoadedPackRoot;
}

export interface ParitySide {
  readonly present: boolean;
  readonly value?: unknown;
}

export interface ParityExpectation {
  readonly path: string;
  readonly transform: ParitySide;
  readonly adopt: ParitySide;
  readonly classification: string;
  readonly disposition: string;
  readonly reason: string;
  readonly evidence: readonly string[];
}

export interface TransformAdoptParityFixture {
  readonly fixture_version: 1;
  readonly name: string;
  readonly resource_type: string;
  readonly provenance: Readonly<JsonRecord>;
  readonly raw_items: readonly Readonly<JsonRecord>[];
  readonly provider_state: Readonly<Record<string, Readonly<JsonRecord>>>;
  readonly expected_differences: readonly ParityExpectation[];
}

export interface ParityDifference extends Record<string, unknown> {
  readonly path: string;
  readonly transform: ParitySide;
  readonly adopt: ParitySide;
}

function fail(message: string): never {
  throw new TransformAdoptParityError(message);
}

function record(value: unknown, where: string): JsonRecord {
  if (!isObject(value)) fail(`${where} must be an object`);
  return value as JsonRecord;
}

function rejectUnknownKeys(value: JsonRecord, allowed: ReadonlySet<string>, where: string): void {
  const unknown = sortedStrings(Object.keys(value).filter((key) => !allowed.has(key)));
  if (unknown[0] !== undefined) fail(`${where} has unknown key ${unknown[0]}`);
}

function requireKeys(value: JsonRecord, required: ReadonlySet<string>, where: string): void {
  const missing = sortedStrings([...required].filter((key) => !Object.hasOwn(value, key)));
  if (missing[0] !== undefined) fail(`${where} is missing required key ${missing[0]}`);
}

function nonEmptyString(value: unknown, where: string): string {
  if (typeof value !== "string" || value.length === 0) fail(`${where} must be a non-empty string`);
  return value;
}

function validateJsonValue(value: unknown, where: string): void {
  if (value === null || typeof value === "string" || typeof value === "boolean") return;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) fail(`${where} contains a non-finite number`);
    return;
  }
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    if (/^-?(?:0|[1-9][0-9]*)$/u.test(token)) return;
    if (!Number.isFinite(Number(token))) fail(`${where} contains a non-finite number`);
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => validateJsonValue(item, `${where}[${index}]`));
    return;
  }
  if (isObject(value)) {
    for (const [key, item] of Object.entries(value)) validateJsonValue(item, `${where}.${key}`);
    return;
  }
  fail(`${where} contains unsupported JSON value ${typeof value}`);
}

function validateSide(value: unknown, where: string): ParitySide {
  const side = record(value, where);
  rejectUnknownKeys(side, SIDE_KEYS, where);
  requireKeys(side, new Set(["present"]), where);
  if (typeof side.present !== "boolean") fail(`${where}.present must be a boolean`);
  if (side.present && !Object.hasOwn(side, "value")) {
    fail(`${where}.value is required when present is true`);
  }
  if (!side.present && Object.hasOwn(side, "value")) {
    fail(`${where}.value must be absent when present is false`);
  }
  if (Object.hasOwn(side, "value")) validateJsonValue(side.value, `${where}.value`);
  return Object.hasOwn(side, "value")
    ? { present: side.present, value: side.value }
    : { present: side.present };
}

function pinnedGithubSource(url: string, repository: string | null, version: string): boolean {
  const prefix = "https://github.com/";
  if (!url.startsWith(prefix)) return false;
  const remainder = url.slice(prefix.length);
  const marker = "/blob/";
  const markerIndex = remainder.indexOf(marker);
  if (markerIndex <= 0) return false;
  const actualRepository = remainder.slice(0, markerIndex);
  const source = remainder.slice(markerIndex + marker.length);
  const slash = source.indexOf("/");
  if (slash <= 0 || (repository !== null && actualRepository !== repository)) return false;
  const ref = source.slice(0, slash);
  const sourcePath = source.slice(slash + 1);
  return (ref === version || ref === `v${version}`)
    && sourcePath.length > 0
    && !sourcePath.startsWith("#");
}

async function fileExists(source: string): Promise<boolean> {
  try {
    return (await stat(source)).isFile();
  } catch (error: unknown) {
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) return false;
    throw error;
  }
}

function repositoryRoot(context: TransformAdoptParityContext): string {
  return path.resolve(context.repositoryRoot ?? path.dirname(context.root.root));
}

async function validateProvenance(
  value: unknown,
  resourceType: string,
  context: TransformAdoptParityContext,
  where: string,
): Promise<JsonRecord> {
  const provenance = record(value, where);
  rejectUnknownKeys(provenance, PROVENANCE_KEYS, where);
  requireKeys(provenance, PROVENANCE_KEYS, where);
  if (typeof provenance.status !== "string" || !PROVENANCE_STATUSES.has(provenance.status)) {
    fail(`${where}.status must be one of ${sortedStrings(PROVENANCE_STATUSES).join(", ")}`);
  }
  const providerVersion = nonEmptyString(provenance.provider_version, `${where}.provider_version`);
  const resource = context.root.resources.get(resourceType);
  if (resource === undefined) fail(`unknown active resource type ${resourceType}`);
  const manifest = manifestForProvider(context.root.packs, resource.provider);
  const pin = manifest.data.pin;
  if (typeof pin !== "string" || pin.length === 0) {
    fail(`${where} resource provider ${resource.provider} has no pack pin`);
  }
  if (providerVersion !== pin) {
    fail(`${where}.provider_version ${providerVersion} does not match active ${resource.provider} pack pin ${pin}`);
  }

  for (const field of ["sources", "local_sources"] as const) {
    const values = provenance[field];
    if (!Array.isArray(values) || values.length === 0) fail(`${where}.${field} must be a non-empty list`);
    const strings = values.map((item, index) => nonEmptyString(item, `${where}.${field}[${index}]`));
    if (strings.length !== new Set(strings).size) fail(`${where}.${field} must not contain duplicates`);
  }
  const sources = provenance.sources as readonly string[];
  sources.forEach((source, index) => {
    if (!pinnedGithubSource(source, null, providerVersion)) {
      fail(`${where}.sources[${index}] must use a GitHub blob ref pinned to provider version ${providerVersion}`);
    }
  });

  const dependencies = provenance.dependency_sources;
  if (!Array.isArray(dependencies)) fail(`${where}.dependency_sources must be a list`);
  const dependencyUrls = new Set<string>();
  dependencies.forEach((value, index) => {
    const label = `${where}.dependency_sources[${index}]`;
    const dependency = record(value, label);
    rejectUnknownKeys(dependency, DEPENDENCY_SOURCE_KEYS, label);
    requireKeys(dependency, DEPENDENCY_SOURCE_KEYS, label);
    const name = nonEmptyString(dependency.name, `${label}.name`);
    const version = nonEmptyString(dependency.version, `${label}.version`);
    const url = nonEmptyString(dependency.url, `${label}.url`);
    if (!pinnedGithubSource(url, name, version)) {
      fail(`${label}.url must reference ${name} at version ${version}`);
    }
    if (dependencyUrls.has(url)) fail(`${where}.dependency_sources must not contain duplicate URLs`);
    dependencyUrls.add(url);
  });

  const root = repositoryRoot(context);
  for (const [index, source] of (provenance.local_sources as readonly string[]).entries()) {
    const normalized = path.normalize(source);
    if (path.isAbsolute(source) || normalized === ".." || normalized.startsWith(`..${path.sep}`)) {
      fail(`${where}.local_sources[${index}] must stay within the repository`);
    }
    if (!(await fileExists(path.join(root, normalized)))) {
      fail(`${where}.local_sources[${index}] does not exist: ${source}`);
    }
  }
  if (provenance.sanitized !== true) {
    fail(`${where}.sanitized must be true; live/private state is not accepted`);
  }
  nonEmptyString(provenance.note, `${where}.note`);
  return provenance;
}

function validateExpectations(
  value: unknown,
  allowedEvidence: ReadonlySet<string>,
  where: string,
): readonly ParityExpectation[] {
  if (!Array.isArray(value)) fail(`${where} must be a list`);
  const seen = new Set<string>();
  return value.map((raw, index) => {
    const label = `${where}[${index}]`;
    const entry = record(raw, label);
    rejectUnknownKeys(entry, EXPECTATION_KEYS, label);
    requireKeys(entry, EXPECTATION_KEYS, label);
    const pointer = typeof entry.path === "string" ? entry.path : fail(`${label}.path must be an RFC 6901 JSON pointer`);
    if (pointer.length > 0 && !pointer.startsWith("/")) fail(`${label}.path must be an RFC 6901 JSON pointer`);
    if (seen.has(pointer)) fail(`${where} contains duplicate path ${pointer}`);
    seen.add(pointer);
    const classification = typeof entry.classification === "string" ? entry.classification : "";
    if (!CLASSIFICATIONS.has(classification)) {
      fail(`${label}.classification must be one of ${sortedStrings(CLASSIFICATIONS).join(", ")}`);
    }
    const disposition = typeof entry.disposition === "string" ? entry.disposition : "";
    if (!DISPOSITIONS.has(disposition)) {
      fail(`${label}.disposition must be one of ${sortedStrings(DISPOSITIONS).join(", ")}`);
    }
    const evidence = entry.evidence;
    if (!Array.isArray(evidence) || evidence.length === 0) fail(`${label}.evidence must be a non-empty list`);
    const sources = evidence.map((item, evidenceIndex) => {
      const source = nonEmptyString(item, `${label}.evidence[${evidenceIndex}]`);
      if (!allowedEvidence.has(source)) {
        fail(`${label}.evidence[${evidenceIndex}] is not declared by fixture provenance`);
      }
      return source;
    });
    if (sources.length !== new Set(sources).size) fail(`${label}.evidence must not contain duplicates`);
    return {
      path: pointer,
      transform: validateSide(entry.transform, `${label}.transform`),
      adopt: validateSide(entry.adopt, `${label}.adopt`),
      classification,
      disposition,
      reason: nonEmptyString(entry.reason, `${label}.reason`),
      evidence: sources,
    };
  });
}

export async function validateTransformAdoptParityFixture(
  value: unknown,
  context: TransformAdoptParityContext,
  where = "parity fixture",
): Promise<TransformAdoptParityFixture> {
  const fixture = record(value, where);
  rejectUnknownKeys(fixture, FIXTURE_KEYS, where);
  requireKeys(fixture, FIXTURE_KEYS, where);
  const version = fixture.fixture_version;
  const versionIsOne = version === 1
    || (version instanceof LosslessNumber && version.toString() === "1");
  if (!versionIsOne) fail(`${where} has unsupported fixture_version ${String(version)}`);
  const name = nonEmptyString(fixture.name, `${where}.name`);
  const resourceType = nonEmptyString(fixture.resource_type, `${where}.resource_type`);
  if (!context.root.resources.has(resourceType)) fail(`unknown active resource type ${resourceType}`);
  const provenance = await validateProvenance(fixture.provenance, resourceType, context, `${where}.provenance`);
  if (!Array.isArray(fixture.raw_items) || fixture.raw_items.length === 0) {
    fail(`${where}.raw_items must be a non-empty list`);
  }
  const rawItems = fixture.raw_items.map((item, index) => {
    const raw = record(item, `${where}.raw_items[${index}]`);
    validateJsonValue(raw, `${where}.raw_items[${index}]`);
    return raw;
  });
  const providerState = record(fixture.provider_state, `${where}.provider_state`);
  if (Object.keys(providerState).length === 0) fail(`${where}.provider_state must be a non-empty object`);
  for (const [importId, rawState] of Object.entries(providerState)) {
    nonEmptyString(importId, `${where}.provider_state key`);
    const label = `${where}.provider_state.${importId}`;
    const state = record(rawState, label);
    rejectUnknownKeys(state, STATE_KEYS, label);
    requireKeys(state, new Set(["values"]), label);
    const values = record(state.values, `${label}.values`);
    validateJsonValue(values, `${label}.values`);
    if (Object.hasOwn(state, "sensitive_values")) validateJsonValue(state.sensitive_values, `${label}.sensitive_values`);
  }
  const declaredEvidence = new Set<string>([
    ...(provenance.sources as readonly string[]),
    ...(provenance.local_sources as readonly string[]),
    ...((provenance.dependency_sources as readonly JsonRecord[]).map((item) => item.url as string)),
  ]);
  return {
    fixture_version: 1,
    name,
    resource_type: resourceType,
    provenance,
    raw_items: rawItems,
    provider_state: providerState as Readonly<Record<string, Readonly<JsonRecord>>>,
    expected_differences: validateExpectations(
      fixture.expected_differences,
      declaredEvidence,
      `${where}.expected_differences`,
    ),
  };
}

export async function loadTransformAdoptParityFixture(
  source: string,
  context: TransformAdoptParityContext,
): Promise<TransformAdoptParityFixture> {
  let data: unknown;
  try {
    data = parseDataJsonLosslessly(await readFile(source, "utf8"));
  } catch (error: unknown) {
    if (error instanceof TransformAdoptParityError) throw error;
    fail(`${source} is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
  return validateTransformAdoptParityFixture(data, context, source);
}

function cloneJson(value: unknown): unknown {
  if (value instanceof LosslessNumber) return new LosslessNumber(value.toString());
  if (Array.isArray(value)) return value.map(cloneJson);
  if (isObject(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, cloneJson(item)]));
  }
  return value;
}

function numberKind(value: unknown): "integer" | "float" | null {
  if (value instanceof LosslessNumber) {
    return /^-?(?:0|[1-9][0-9]*)$/u.test(value.toString()) ? "integer" : "float";
  }
  if (typeof value !== "number") return null;
  return Number.isInteger(value) && !Object.is(value, -0) ? "integer" : "float";
}

function strictJsonEqual(left: unknown, right: unknown): boolean {
  if (typeof left === "boolean" || typeof right === "boolean") return left === right;
  const leftNumber = numberKind(left);
  const rightNumber = numberKind(right);
  if (leftNumber !== null || rightNumber !== null) {
    return leftNumber === rightNumber
      && strictCanonicalJson(left) === strictCanonicalJson(right);
  }
  if (left === null || right === null) return left === right;
  if (typeof left === "string" || typeof right === "string") return left === right;
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left) && Array.isArray(right)
      && left.length === right.length
      && left.every((item, index) => strictJsonEqual(item, right[index]));
  }
  if (isObject(left) || isObject(right)) {
    if (!isObject(left) || !isObject(right)) return false;
    const leftKeys = sortedStrings(Object.keys(left));
    const rightKeys = sortedStrings(Object.keys(right));
    return leftKeys.length === rightKeys.length
      && leftKeys.every((key, index) => key === rightKeys[index])
      && leftKeys.every((key) => strictJsonEqual(left[key], right[key]));
  }
  return left === right;
}

function strictCanonicalJson(value: unknown): string {
  validateJsonValue(value, "JSON value");
  return renderPythonLosslessArtifactJson(value).trimEnd();
}

function pointerSegment(value: string | number): string {
  return String(value).replaceAll("~", "~0").replaceAll("/", "~1");
}

function jsonPointer(pathSegments: readonly (string | number)[]): string {
  return pathSegments.length === 0 ? "" : `/${pathSegments.map(pointerSegment).join("/")}`;
}

function side(present: boolean, value?: unknown): ParitySide {
  return present ? { present, value } : { present };
}

export function transformAdoptJsonDifferences(
  transform: unknown,
  adopt: unknown,
  segments: readonly (string | number)[] = [],
): readonly ParityDifference[] {
  const leftNumber = numberKind(transform);
  const rightNumber = numberKind(adopt);
  const typeMismatch = leftNumber !== null || rightNumber !== null
    ? leftNumber !== rightNumber
    : Array.isArray(transform) !== Array.isArray(adopt)
      || (transform === null) !== (adopt === null)
      || typeof transform !== typeof adopt;
  if (typeMismatch) {
    return [{ path: jsonPointer(segments), transform: side(true, transform), adopt: side(true, adopt) }];
  }
  if (isObject(transform) && isObject(adopt)) {
    const output: ParityDifference[] = [];
    for (const key of sortedStrings(new Set([...Object.keys(transform), ...Object.keys(adopt)]))) {
      if (!Object.hasOwn(transform, key)) {
        output.push({ path: jsonPointer([...segments, key]), transform: side(false), adopt: side(true, adopt[key]) });
      } else if (!Object.hasOwn(adopt, key)) {
        output.push({ path: jsonPointer([...segments, key]), transform: side(true, transform[key]), adopt: side(false) });
      } else {
        output.push(...transformAdoptJsonDifferences(transform[key], adopt[key], [...segments, key]));
      }
    }
    return output;
  }
  if (Array.isArray(transform) && Array.isArray(adopt)) {
    const output: ParityDifference[] = [];
    for (let index = 0; index < Math.max(transform.length, adopt.length); index += 1) {
      if (index >= transform.length) {
        output.push({ path: jsonPointer([...segments, index]), transform: side(false), adopt: side(true, adopt[index]) });
      } else if (index >= adopt.length) {
        output.push({ path: jsonPointer([...segments, index]), transform: side(true, transform[index]), adopt: side(false) });
      } else {
        output.push(...transformAdoptJsonDifferences(transform[index], adopt[index], [...segments, index]));
      }
    }
    return output;
  }
  return strictJsonEqual(transform, adopt)
    ? []
    : [{ path: jsonPointer(segments), transform: side(true, transform), adopt: side(true, adopt) }];
}

function pointerTokens(pointer: string): readonly string[] {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) fail("difference path is not a JSON pointer");
  return pointer.slice(1).split("/").map((token) => token.replaceAll("~1", "/").replaceAll("~0", "~"));
}

function listIndex(token: string, length: number, allowAppend: boolean): number {
  if (!/^[+-]?[0-9]+$/u.test(token)) fail(`difference path list index ${token} is invalid`);
  const index = Number(token);
  if (!Number.isSafeInteger(index) || index < 0 || index > length || (!allowAppend && index === length)) {
    fail(`difference path list index ${token} is out of range`);
  }
  return index;
}

function pointerParent(root: unknown, tokens: readonly string[]): readonly [JsonRecord | unknown[], string] {
  let current = root;
  for (const token of tokens.slice(0, -1)) {
    if (isObject(current)) {
      if (!Object.hasOwn(current, token)) fail(`difference path parent ${token} is missing`);
      current = current[token];
    } else if (Array.isArray(current)) {
      current = current[listIndex(token, current.length, false)];
    } else {
      fail(`difference path traverses a scalar at ${token}`);
    }
  }
  if (!isObject(current) && !Array.isArray(current)) fail("difference path parent is not a container");
  const token = tokens.at(-1);
  if (token === undefined) fail("difference path has no parent token");
  return [current, token];
}

function setPointer(root: unknown, pointer: string, value: unknown): unknown {
  const tokens = pointerTokens(pointer);
  if (tokens.length === 0) return cloneJson(value);
  const [parent, token] = pointerParent(root, tokens);
  if (Array.isArray(parent)) {
    const index = listIndex(token, parent.length, true);
    if (index === parent.length) parent.push(cloneJson(value));
    else parent[index] = cloneJson(value);
  } else {
    parent[token] = cloneJson(value);
  }
  return root;
}

function deletePointer(root: unknown, pointer: string): unknown {
  const tokens = pointerTokens(pointer);
  if (tokens.length === 0) fail("difference cannot delete the report root");
  const [parent, token] = pointerParent(root, tokens);
  if (Array.isArray(parent)) parent.splice(listIndex(token, parent.length, false), 1);
  else {
    if (!Object.hasOwn(parent, token)) fail(`difference delete path ${pointer} is missing`);
    delete parent[token];
  }
  return root;
}

export function replayTransformAdoptDifferences(
  transformPayload: unknown,
  differences: readonly ParityDifference[],
): unknown {
  let reconstructed = cloneJson(transformPayload);
  for (const entry of differences) {
    if (entry.adopt.present) reconstructed = setPointer(reconstructed, entry.path, entry.adopt.value);
  }
  for (const entry of [...differences].reverse()) {
    if (!entry.adopt.present) reconstructed = deletePointer(reconstructed, entry.path);
  }
  return reconstructed;
}

function differenceKey(entry: Pick<ParityDifference, "path" | "transform" | "adopt">): string {
  return strictCanonicalJson({ path: entry.path, transform: entry.transform, adopt: entry.adopt });
}

function renderedItems(items: Readonly<Record<string, unknown>>): readonly [string, string] {
  const rendered = renderPythonLosslessArtifactJson({ items });
  return [rendered, createHash("sha256").update(rendered, "utf8").digest("hex")];
}

function fixtureStateLoader(fixture: TransformAdoptParityFixture) {
  return async (options: {
    readonly keyToImportId: ReadonlyMap<string, string>;
  }): Promise<ReadonlyMap<string, OracleStateObject>> => {
    const requested = new Set([...options.keyToImportId.values()].map(String));
    const available = new Set(Object.keys(fixture.provider_state));
    const missing = sortedStrings([...requested].filter((value) => !available.has(value)));
    const extra = sortedStrings([...available].filter((value) => !requested.has(value)));
    if (missing[0] !== undefined) fail(`provider_state is missing import id ${missing[0]}`);
    if (extra[0] !== undefined) fail(`provider_state has unreferenced import id ${extra[0]}`);
    return new Map([...options.keyToImportId].map(([key, importId]) => {
      const state = fixture.provider_state[String(importId)];
      if (state === undefined) fail(`provider_state is missing import id ${String(importId)}`);
      return [key, {
        address: "fixture",
        sensitiveValues: state.sensitive_values ?? {},
        values: state.values as Readonly<Record<string, unknown>>,
      }];
    }));
  };
}

export async function compareTransformAdoptParityFixture(
  input: TransformAdoptParityFixture,
  context: TransformAdoptParityContext,
  options: {
    readonly jsonDifferences?: typeof transformAdoptJsonDifferences;
  } = {},
): Promise<Readonly<JsonRecord>> {
  const fixture = await validateTransformAdoptParityFixture(input, context);
  const resource = context.root.resources.get(fixture.resource_type);
  if (resource === undefined) fail(`unknown active resource type ${fixture.resource_type}`);
  const transformed = await transformResourceItems({
    rawItems: fixture.raw_items,
    resource,
    root: context.root,
  });
  const adopted = await adoptResourceItems({
    policy: await loadAdoptionPolicy({ root: context.root }),
    rawItems: fixture.raw_items,
    resource,
    root: context.root,
    stateLoader: fixtureStateLoader(fixture),
  });
  const [transformRendered, transformSha] = renderedItems(transformed.items);
  const [adoptRendered, adoptSha] = renderedItems(adopted.items);
  const transformPayload = { items: transformed.items };
  const adoptPayload = { items: adopted.items };
  const actual = (options.jsonDifferences ?? transformAdoptJsonDifferences)(
    transformPayload,
    adoptPayload,
  );
  const reconstructed = replayTransformAdoptDifferences(transformPayload, actual);
  const reconstructedRecord = record(reconstructed, "reconstructed parity payload");
  const reconstructedItems = record(reconstructedRecord.items, "reconstructed parity payload.items");
  const [reconstructedRendered] = renderedItems(reconstructedItems);
  const unaccountedByteDifference = reconstructedRendered !== adoptRendered;
  const expected = new Map(fixture.expected_differences.map((entry) => [differenceKey(entry), entry]));
  const differences = actual.map((entry) => {
    const classification = expected.get(differenceKey(entry));
    if (classification === undefined) return { ...entry, status: "unclassified" };
    expected.delete(differenceKey(entry));
    return {
      ...entry,
      status: "classified",
      classification: classification.classification,
      disposition: classification.disposition,
      reason: classification.reason,
      evidence: classification.evidence,
    };
  });
  const stale = [...expected.values()].sort((left, right) => {
    const byPath = comparePythonStrings(left.path, right.path);
    return byPath !== 0
      ? byPath
      : comparePythonStrings(strictCanonicalJson(left), strictCanonicalJson(right));
  });
  const unclassified = differences.filter((entry) => entry.status === "unclassified").length;
  const classified = differences.length - unclassified;
  const evidenceGates = differences.filter((entry) => (
    "disposition" in entry && entry.disposition === "evidence_gate"
  )).length;
  const accepted = differences.filter((entry) => (
    "disposition" in entry && entry.disposition === "accepted"
  )).length;
  const drops = sortedStrings(transformed.drops);
  const result = unclassified > 0 || stale.length > 0 || drops.length > 0 || unaccountedByteDifference
    ? "review_required"
    : evidenceGates > 0 ? "evidence_gates"
      : differences.length > 0 ? "classified_differences" : "equal";
  return {
    name: fixture.name,
    resource_type: fixture.resource_type,
    provenance: fixture.provenance,
    result,
    outputs: {
      byte_equal: transformRendered === adoptRendered,
      unaccounted_byte_difference: unaccountedByteDifference,
      transform_sha256: transformSha,
      adopt_sha256: adoptSha,
    },
    differences,
    stale_expectations: stale,
    transform_unacknowledged_drops: drops,
    summary: {
      differences: differences.length,
      classified,
      unclassified,
      evidence_gates: evidenceGates,
      accepted,
      stale_expectations: stale.length,
      unacknowledged_drops: drops.length,
      unaccounted_byte_differences: unaccountedByteDifference ? 1 : 0,
    },
  };
}

export async function buildTransformAdoptParityReport(
  fixtures: readonly TransformAdoptParityFixture[],
  context: TransformAdoptParityContext,
): Promise<Readonly<JsonRecord>> {
  const names = new Set<string>();
  const results: JsonRecord[] = [];
  for (const fixture of fixtures) {
    const validated = await validateTransformAdoptParityFixture(fixture, context);
    if (names.has(validated.name)) fail(`duplicate fixture name ${validated.name}`);
    names.add(validated.name);
    results.push({ ...(await compareTransformAdoptParityFixture(validated, context)) });
  }
  results.sort((left, right) => comparePythonStrings(String(left.name), String(right.name)));
  const count = (key: string): number => results.filter((entry) => entry.result === key).length;
  const sum = (key: string): number => results.reduce((total, entry) => {
    const summary = record(entry.summary, "fixture summary");
    const value = summary[key];
    return total + (typeof value === "number" ? value : 0);
  }, 0);
  const summary: JsonRecord = {
    fixtures: results.length,
    equal: count("equal"),
    classified_differences: count("classified_differences"),
    evidence_gate_fixtures: count("evidence_gates"),
    review_required: count("review_required"),
    differences: sum("differences"),
    classified: sum("classified"),
    unclassified: sum("unclassified"),
    evidence_gates: sum("evidence_gates"),
    accepted: sum("accepted"),
    stale_expectations: sum("stale_expectations"),
    unacknowledged_drops: sum("unacknowledged_drops"),
    unaccounted_byte_differences: sum("unaccounted_byte_differences"),
  };
  return {
    kind: TRANSFORM_ADOPT_PARITY_REPORT_KIND,
    report_version: TRANSFORM_ADOPT_PARITY_REPORT_VERSION,
    result: (summary.review_required as number) > 0 ? "review_required"
      : (summary.evidence_gate_fixtures as number) > 0 ? "evidence_gates"
        : (summary.classified_differences as number) > 0 ? "classified_differences" : "equal",
    summary,
    fixtures: results,
  };
}

export function renderTransformAdoptParityReport(report: Readonly<JsonRecord>): string {
  return renderPythonLosslessArtifactJson(report);
}

// Concise aliases for callers migrating from the Python diagnostic module.
export { TransformAdoptParityError as ParityFixtureError };
export const validateParityFixture = validateTransformAdoptParityFixture;
export const loadParityFixture = loadTransformAdoptParityFixture;
export const jsonDifferences = transformAdoptJsonDifferences;
export const applyReportedDifferences = replayTransformAdoptDifferences;
export const compareParityFixture = compareTransformAdoptParityFixture;
export const buildParityReport = buildTransformAdoptParityReport;
export const renderParityReport = renderTransformAdoptParityReport;
