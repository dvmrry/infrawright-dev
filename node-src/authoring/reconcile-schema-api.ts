import { LosslessNumber } from "lossless-json";

import {
  applyTransformOverridesForAuthoring,
  coerceTransformPrimitiveForAuthoring,
  snakeJsonKeysForAuthoring,
  snakeName,
  transformSkipMatchReason,
  transformValueMatchesDefaultForAuthoring,
} from "../domain/pull-transform.js";
import { pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  terraformAttributeType,
  terraformAttributesForBlock,
  terraformBlockForSchema,
  terraformBlockIsSingle,
  terraformBlockTypesForBlock,
  terraformClassifyAttributes,
  terraformInputBlockTypes,
  terraformRequireObject,
  terraformResourceInputAttributes,
  type TerraformTypeEncoding,
} from "../metadata/terraform-schema.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { apiMetadataFromOpenApi, type OpenApiFieldMap } from "./openapi.js";

export const RECONCILIATION_BUCKETS = [
  "kept", "renamed", "transformed", "defaulted", "relationship",
  "dropped_default", "dropped_override", "dropped_acknowledged",
  "dropped_known", "unknown", "shape_mismatch", "skipped",
] as const;
export type ReconciliationBucket = typeof RECONCILIATION_BUCKETS[number];

const TRANSFORM_KEYS = [
  "split_csv", "sort_lists", "references", "divide", "invert_bool",
  "value_map", "strip_prefix", "html_escape_fields",
] as const;
const READ_ONLY_NAMES = new Set([
  "_depth", "children", "created", "display", "display_url", "last_updated",
  "owner", "tagged_items", "url",
]);
const READ_ONLY_SUFFIXES = ["_count", "_url"] as const;
const FIELD_ALIASES: Readonly<Record<string, string>> = {
  address: "ip_address", color: "color_hex", face: "rack_face", time_zone: "timezone",
};

export type ApiFieldMetadata = Readonly<JsonObject>;
export type ApiMetadata = Readonly<Record<string, ApiFieldMetadata>>;

interface MutableReportEntry extends JsonObject {
  path: string;
  count: number;
  reasons: Record<string, number>;
  types: Record<string, number>;
}

export interface ReconciliationReportData extends JsonObject {
  readonly resource_type: string;
  readonly items: number;
}

function record(value: unknown, label: string): JsonObject {
  if (!isObject(value)) throw new TypeError(`${label} must be a JSON object`);
  return value;
}

function stringList(value: unknown): readonly string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

export function providerSchemaFromTerraformDump(
  data: JsonObject,
  resourceType: string,
  providerSource?: string,
): JsonObject {
  const providers = isObject(data.provider_schemas) ? data.provider_schemas : {};
  if (providerSource !== undefined) {
    let provider = providers[providerSource];
    if (!isObject(provider)) {
      const matches = Object.entries(providers)
        .filter(([source, schema]) => source.endsWith(`/${providerSource}`) && isObject(schema))
        .map(([, schema]) => schema as JsonObject);
      if (matches.length === 1) provider = matches[0];
    }
    if (!isObject(provider)) {
      throw new Error(`provider source ${JSON.stringify(providerSource)} not found in Terraform schema`);
    }
    return provider;
  }
  const matches = Object.values(providers).filter((candidate): candidate is JsonObject => {
    return isObject(candidate)
      && isObject(candidate.resource_schemas)
      && Object.hasOwn(candidate.resource_schemas, resourceType);
  });
  if (matches.length === 1) return matches[0] as JsonObject;
  if (matches.length === 0) {
    throw new Error(`resource type ${JSON.stringify(resourceType)} not found in Terraform schema`);
  }
  throw new TypeError(
    `resource type ${JSON.stringify(resourceType)} appears in multiple provider schemas; pass providerSource`,
  );
}

export function resourceSchemaFromData(
  data: JsonObject,
  resourceType: string,
  providerSource?: string,
): JsonObject {
  let schemas: JsonObject;
  if (isObject(data.resource_schemas)) schemas = data.resource_schemas;
  else if (isObject(data.provider_schemas)) {
    const provider = providerSchemaFromTerraformDump(data, resourceType, providerSource);
    schemas = isObject(provider.resource_schemas) ? provider.resource_schemas : {};
  } else {
    throw new TypeError("provider schema data must contain resource_schemas or provider_schemas");
  }
  return record(schemas[resourceType], `resource type ${JSON.stringify(resourceType)}`);
}

export function apiItemsFrom(value: unknown, source = "<api>"): readonly JsonObject[] {
  if (Array.isArray(value)) return value.map((item, index) => record(item, `${source}[${index}]`));
  if (isObject(value)) {
    if (Array.isArray(value.results)) {
      return value.results.map((item, index) => record(item, `${source}.results[${index}]`));
    }
    return [value];
  }
  throw new TypeError(`${source} must be a JSON object, list, or NetBox-style {results:[...]} wrapper`);
}

export function apiMetadataFromOptions(value: unknown, source = "<options>"): ApiMetadata {
  const options = record(value, source);
  const actions = isObject(options.actions) ? options.actions : {};
  const fields: Record<string, JsonObject> = {};
  for (const method of ["POST", "PUT", "PATCH"] as const) {
    const action = actions[method];
    if (!isObject(action)) continue;
    for (const name of sortedStrings(Object.keys(action))) {
      const meta = action[name];
      if (!isObject(meta)) continue;
      const key = snakeName(name);
      const merged: JsonObject = { ...(fields[key] ?? {}) };
      const methods = stringList(merged.methods).slice();
      if (!methods.includes(method)) methods.push(method);
      const snakeMeta = snakeJsonKeysForAuthoring(meta);
      if (isObject(snakeMeta)) Object.assign(merged, snakeMeta);
      merged.methods = methods;
      if (!merged.read_only) merged.writable = true;
      fields[key] = merged;
    }
  }
  return fields;
}

export function mergeApiMetadata(options: {
  readonly optionDocuments?: readonly unknown[];
  readonly openApi?: JsonObject;
  readonly openApiReadOperations?: readonly string[];
  readonly openApiWriteOperations?: readonly string[];
}): ApiMetadata {
  const fields: Record<string, JsonObject> = {};
  for (const document of options.optionDocuments ?? []) {
    Object.assign(fields, apiMetadataFromOptions(document));
  }
  if (options.openApi !== undefined) {
    Object.assign(fields, apiMetadataFromOpenApi(options.openApi, {
      ...(options.openApiReadOperations === undefined
        ? {} : { readOperations: options.openApiReadOperations }),
      ...(options.openApiWriteOperations === undefined
        ? {} : { writeOperations: options.openApiWriteOperations }),
    }) as OpenApiFieldMap);
  }
  return fields;
}

function jsonTypeName(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return "bool";
  if (value instanceof LosslessNumber) return value.toString().includes(".") || /e/iu.test(value.toString()) ? "float" : "int";
  if (typeof value === "number") return Number.isInteger(value) ? "int" : "float";
  if (typeof value === "string") return "string";
  if (Array.isArray(value)) return "list";
  if (isObject(value)) return "object";
  return typeof value;
}

function joinPath(prefix: string, name: string): string {
  return prefix === "" ? name : `${prefix}.${name}`;
}

function pathAliases(path: string): readonly string[] {
  const noBrackets = path.replaceAll("[]", "");
  return noBrackets === path ? [path] : [path, noBrackets];
}

function containsPath(paths: ReadonlySet<string>, path: string): boolean {
  return pathAliases(path).some((alias) => paths.has(alias));
}

function mappingValue(mapping: JsonObject, path: string): readonly [boolean, unknown] {
  for (const alias of pathAliases(path)) {
    if (Object.hasOwn(mapping, alias)) return [true, mapping[alias]];
  }
  return [false, undefined];
}

function isPrimitiveMatch(value: unknown, primitive: string): boolean {
  if (value === null) return true;
  if (primitive === "string") return typeof value === "string";
  if (primitive === "bool") return typeof value === "boolean";
  if (primitive === "number") {
    return (typeof value === "number" && Number.isFinite(value)) || value instanceof LosslessNumber;
  }
  return true;
}

function primitiveTransformReason(value: unknown, primitive: "bool" | "number" | "string"): string | undefined {
  const coerced = coerceTransformPrimitiveForAuthoring(value, primitive);
  if (!pythonJsonEqual(coerced, value) && isPrimitiveMatch(coerced, primitive)) {
    return `coerce_${jsonTypeName(value)}_to_${primitive}`;
  }
  return undefined;
}

function isReadOnlyPath(path: string): boolean {
  const leaf = path.slice(path.lastIndexOf(".") + 1);
  return READ_ONLY_NAMES.has(leaf) || READ_ONLY_SUFFIXES.some((suffix) => leaf.endsWith(suffix));
}

function aliasFor(
  key: string,
  keep: ReadonlySet<string>,
  computed: ReadonlySet<string>,
): readonly [string | undefined, "input" | "computed" | undefined, string | undefined] {
  const candidates: Array<readonly [string, string]> = [];
  if (FIELD_ALIASES[key] !== undefined) candidates.push([FIELD_ALIASES[key], "field_alias"] as const);
  candidates.push([`${key}_id`, "relationship_id"], [`${key}_ids`, "relationship_ids"], [`rack_${key}`, "field_alias"]);
  if (key.startsWith("vc_")) candidates.push([`virtual_chassis_${key.slice(3)}`, "field_alias"]);
  if (key.endsWith("4")) candidates.push([`${key.slice(0, -1)}v4`, "field_alias"]);
  if (key.endsWith("6")) candidates.push([`${key.slice(0, -1)}v6`, "field_alias"]);
  for (const [candidate, reason] of candidates) {
    if (keep.has(candidate)) return [candidate, "input", reason];
    if (computed.has(candidate)) return [candidate, "computed", reason];
  }
  return [undefined, undefined, undefined];
}

function isRelationshipValue(value: unknown): boolean {
  if (value === null) return true;
  if (isObject(value)) return Object.hasOwn(value, "id");
  return Array.isArray(value) && value.every((entry) => isObject(entry) && Object.hasOwn(entry, "id"));
}

export class ReconciliationReport {
  readonly resourceType: string;
  itemCount = 0;
  readonly #buckets: Record<ReconciliationBucket, Map<string, MutableReportEntry>>;

  constructor(resourceType: string) {
    this.resourceType = resourceType;
    this.#buckets = Object.fromEntries(
      RECONCILIATION_BUCKETS.map((bucket) => [bucket, new Map()]),
    ) as Record<ReconciliationBucket, Map<string, MutableReportEntry>>;
  }

  add(bucket: ReconciliationBucket, path: string, reason: string, value?: unknown): void {
    const entries = this.#buckets[bucket];
    let entry = entries.get(path);
    if (entry === undefined) {
      entry = { count: 0, path, reasons: {}, types: {} };
      entries.set(path, entry);
    }
    entry.count += 1;
    entry.reasons[reason] = (entry.reasons[reason] ?? 0) + 1;
    const type = jsonTypeName(value);
    entry.types[type] = (entry.types[type] ?? 0) + 1;
  }

  paths(bucket: ReconciliationBucket): ReadonlySet<string> {
    return new Set(this.#buckets[bucket].keys());
  }

  hasUnknowns(): boolean {
    return this.#buckets.unknown.size > 0 || this.#buckets.shape_mismatch.size > 0;
  }

  asDict(): ReconciliationReportData {
    const paths: Record<string, readonly JsonObject[]> = {};
    const observations: Record<string, number> = {};
    const uniquePaths: Record<string, number> = {};
    for (const bucket of RECONCILIATION_BUCKETS) {
      const entries = sortedStrings([...this.#buckets[bucket].keys()])
        .map((path) => this.#buckets[bucket].get(path) as MutableReportEntry);
      paths[bucket] = entries;
      observations[bucket] = entries.reduce((sum, entry) => sum + entry.count, 0);
      uniquePaths[bucket] = entries.length;
    }
    const droppedKnown = sortedStrings([...this.#buckets.dropped_known.keys()]);
    const providerGaps = sortedStrings([...this.#buckets.unknown.values()]
      .filter((entry) => Object.hasOwn(entry.reasons, "api_required_not_in_provider")
        || Object.hasOwn(entry.reasons, "api_writable_not_in_provider"))
      .map((entry) => entry.path));
    const reviewUnknown = sortedStrings([
      ...this.#buckets.unknown.keys(), ...this.#buckets.shape_mismatch.keys(),
    ]);
    return {
      items: this.itemCount,
      paths,
      resource_type: this.resourceType,
      suggestions: {
        acknowledged_drops: droppedKnown,
        provider_gaps: providerGaps,
        review_unknown: reviewUnknown,
      },
      summary: { observations, unique_paths: uniquePaths },
    };
  }
}

function addLeaves(
  report: ReconciliationReport,
  bucket: ReconciliationBucket,
  path: string,
  value: unknown,
  reason: string,
): void {
  if (isObject(value)) {
    const keys = sortedStrings(Object.keys(value));
    if (keys.length === 0) report.add(bucket, path, reason, value);
    else for (const key of keys) addLeaves(report, bucket, joinPath(path, key), value[key], reason);
    return;
  }
  if (Array.isArray(value)) {
    if (value.length === 0) report.add(bucket, path, reason, value);
    else if (value.every(isObject)) {
      for (const entry of value) addLeaves(report, bucket, `${path}[]`, entry, reason);
    } else report.add(bucket, path, reason, value);
    return;
  }
  report.add(bucket, path, reason, value);
}

function overrideBucket(
  override: JsonObject,
  path: string,
  value: unknown,
  allowAcknowledged: boolean,
): readonly [ReconciliationBucket | undefined, string | undefined] {
  const drops = new Set(stringList(override.drops));
  if (containsPath(drops, path)) return ["dropped_override", "override_drop"];
  const defaults = isObject(override.drop_if_default) ? override.drop_if_default : {};
  const [found, defaultValue] = mappingValue(defaults, path);
  if (found && transformValueMatchesDefaultForAuthoring(value, defaultValue)) {
    return ["dropped_default", "drop_if_default"];
  }
  const acknowledged = new Set(stringList(override.acknowledged_drops));
  if (allowAcknowledged && containsPath(acknowledged, path)) {
    return ["dropped_acknowledged", "acknowledged_drop"];
  }
  return [undefined, undefined];
}

function metadataForPath(metadata: ApiMetadata | undefined, path: string): ApiFieldMetadata | undefined {
  if (metadata === undefined) return undefined;
  for (const alias of pathAliases(path)) if (metadata[alias] !== undefined) return metadata[alias];
  return undefined;
}

function dropAbsent(report: ReconciliationReport, path: string, value: unknown): boolean {
  const reason = value === null ? "null_non_schema_field"
    : value === "" ? "empty_non_schema_string"
    : Array.isArray(value) && value.length === 0 ? "empty_non_schema_list"
    : isObject(value) && Object.keys(value).length === 0 ? "empty_non_schema_object"
    : undefined;
  if (reason === undefined) return false;
  report.add("dropped_known", path, reason, value);
  return true;
}

function markUnknownOrApiKnown(
  report: ReconciliationReport,
  path: string,
  value: unknown,
  metadata: ApiMetadata | undefined,
  fallback: string,
): void {
  if (dropAbsent(report, path, value)) return;
  const meta = metadataForPath(metadata, path);
  if (meta?.read_only) addLeaves(report, "dropped_known", path, value, "api_read_only");
  else if (meta?.response_only) addLeaves(report, "dropped_known", path, value, "api_response_only");
  else if (meta?.writable) {
    addLeaves(report, "unknown", path, value, meta.required
      ? "api_required_not_in_provider" : "api_writable_not_in_provider");
  } else if (meta !== undefined) addLeaves(report, "unknown", path, value, "api_spec_observed_not_in_provider");
  else if (isObject(value) && Object.hasOwn(value, "value") && Object.hasOwn(value, "label")) {
    report.add("dropped_known", path, "read_only_choice_object", value);
  } else addLeaves(report, "unknown", path, value, fallback);
}

function markOrWalkAttribute(
  report: ReconciliationReport,
  path: string,
  value: unknown,
  encoding: TerraformTypeEncoding,
  override: JsonObject,
  metadata: ApiMetadata | undefined,
): void {
  const [bucket, reason] = overrideBucket(override, path, value, false);
  if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, path, value, reason);
  else walkAttribute(report, path, value, encoding, override, metadata);
}

function walkObjectMembers(
  report: ReconciliationReport,
  path: string,
  value: unknown,
  members: Readonly<Record<string, TerraformTypeEncoding>>,
  override: JsonObject,
  metadata: ApiMetadata | undefined,
): void {
  if (!isObject(value)) {
    report.add("shape_mismatch", path, "expected_object", value);
    return;
  }
  const keys = sortedStrings(Object.keys(value));
  if (keys.length === 0) report.add("kept", path, "terraform_input_empty_object", value);
  for (const key of keys) {
    const child = joinPath(path, key);
    const encoding = members[key];
    if (encoding !== undefined) markOrWalkAttribute(report, child, value[key], encoding, override, metadata);
    else {
      const [bucket, reason] = overrideBucket(override, child, value[key], true);
      if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, child, value[key], reason);
      else markUnknownOrApiKnown(report, child, value[key], metadata, "undeclared_object_member");
    }
  }
}

function walkAttribute(
  report: ReconciliationReport,
  path: string,
  value: unknown,
  encoding: TerraformTypeEncoding,
  override: JsonObject,
  metadata: ApiMetadata | undefined,
): void {
  if (typeof encoding === "string") {
    if (isObject(value) && Object.hasOwn(value, "value") && Object.hasOwn(value, "label")) {
      report.add("transformed", path, "choice_value", value);
    } else if (isPrimitiveMatch(value, encoding)) report.add("kept", path, "terraform_input", value);
    else {
      const reason = primitiveTransformReason(value, encoding);
      report.add(reason === undefined ? "shape_mismatch" : "transformed", path, reason ?? `expected_${encoding}`, value);
    }
    return;
  }
  const [kind, inner] = encoding;
  if (kind === "object" && !Array.isArray(inner)) {
    walkObjectMembers(report, path, value, inner, override, metadata);
  } else if (kind === "map") {
    report.add(isObject(value) ? "kept" : "shape_mismatch", path, isObject(value) ? "terraform_input_map" : "expected_map", value);
  } else if (kind === "list" || kind === "set") {
    if (!Array.isArray(value)) {
      report.add("shape_mismatch", path, `expected_${kind}`, value);
    } else if (typeof inner === "string" && value.some(isObject)) {
      const convertible = value.every((entry) => isObject(entry)
        && ["slug", "name", "id"].some((key) => Object.hasOwn(entry, key)));
      report.add(convertible ? "transformed" : "shape_mismatch", path,
        convertible ? `object_list_to_${kind}_${inner}` : `expected_${kind}_of_${inner}`, value);
    } else if (Array.isArray(inner) && inner[0] === "object" && !Array.isArray(inner[1])) {
      for (const entry of value) walkObjectMembers(report, `${path}[]`, entry, inner[1], override, metadata);
      if (value.length === 0) report.add("kept", path, `terraform_input_empty_${kind}`, value);
    } else report.add("kept", path, `terraform_input_${kind}`, value);
  } else report.add("shape_mismatch", path, "unsupported_collection_kind", value);
}

function walkBlockValue(
  report: ReconciliationReport,
  path: string,
  value: unknown,
  blockType: JsonObject,
  override: JsonObject,
  metadata: ApiMetadata | undefined,
): void {
  const block = record(blockType.block, `${path}.block`);
  if (terraformBlockIsSingle(blockType)) {
    if (isObject(value)) walkBlock(report, path, value, block, override, false, metadata);
    else if (Array.isArray(value) && value.every(isObject)) {
      for (const entry of value) walkBlock(report, path, entry, block, override, false, metadata);
    } else report.add("shape_mismatch", path, "expected_single_block", value);
  } else if (Array.isArray(value)) {
    for (const entry of value) {
      if (isObject(entry)) walkBlock(report, `${path}[]`, entry, block, override, false, metadata);
      else report.add("shape_mismatch", `${path}[]`, "expected_block_object", entry);
    }
  } else if (isObject(value)) walkBlock(report, `${path}[]`, value, block, override, false, metadata);
  else report.add("shape_mismatch", path, "expected_block_list", value);
}

function walkBlock(
  report: ReconciliationReport,
  prefix: string,
  value: unknown,
  block: JsonObject,
  override: JsonObject,
  resourceTop: boolean,
  metadata: ApiMetadata | undefined,
): void {
  if (!isObject(value)) {
    report.add("shape_mismatch", prefix || "$item", "expected_object", value);
    return;
  }
  const classified = resourceTop
    ? terraformResourceInputAttributes(block, "resource.block")
    : terraformClassifyAttributes(block, prefix || "block");
  const keep = new Set([...classified.required, ...classified.optional]);
  const computed = new Set(classified.computedOnly);
  const attributes = terraformAttributesForBlock(block, prefix || "block");
  const blockTypes = terraformBlockTypesForBlock(block, prefix || "block");
  const inputBlocks = terraformInputBlockTypes(block, prefix || "block");
  for (const key of sortedStrings(Object.keys(value))) {
    const child = joinPath(prefix, key);
    const childValue = value[key];
    if (keep.has(key)) {
      const attribute = terraformRequireObject(attributes[key], `${child}.attribute`);
      markOrWalkAttribute(report, child, childValue, terraformAttributeType(attribute, child), override, metadata);
    } else if (computed.has(key) || Object.hasOwn(attributes, key)) {
      const [bucket, reason] = overrideBucket(override, child, childValue, true);
      if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, child, childValue, reason);
      else addLeaves(report, "dropped_known", child, childValue, "computed_only_attribute");
    } else if (inputBlocks.has(key)) {
      const [bucket, reason] = overrideBucket(override, child, childValue, false);
      if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, child, childValue, reason);
      else walkBlockValue(report, child, childValue, terraformRequireObject(blockTypes[key], child), override, metadata);
    } else if (Object.hasOwn(blockTypes, key)) {
      const [bucket, reason] = overrideBucket(override, child, childValue, true);
      if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, child, childValue, reason);
      else addLeaves(report, "dropped_known", child, childValue, "non_input_block");
    } else {
      const [bucket, reason] = overrideBucket(override, child, childValue, true);
      if (bucket !== undefined && reason !== undefined) addLeaves(report, bucket, child, childValue, reason);
      else if (isReadOnlyPath(child)) addLeaves(report, "dropped_known", child, childValue, "common_read_only");
      else {
        const [alias, aliasKind, aliasReason] = aliasFor(key, keep, computed);
        if (alias !== undefined && aliasKind === "input" && aliasReason?.startsWith("relationship") && isRelationshipValue(childValue)) {
          report.add("relationship", child, `relationship_id:${alias}`, childValue);
        } else if (alias !== undefined && aliasKind === "input") {
          report.add("transformed", child, `${aliasReason}:${alias}`, childValue);
        } else if (alias !== undefined && aliasKind === "computed") {
          addLeaves(report, "dropped_known", child, childValue, `computed_alias:${alias}`);
        } else markUnknownOrApiKnown(report, child, childValue, metadata, "no_schema_input_or_override");
      }
    }
  }
}

function recordOverrideActions(
  report: ReconciliationReport,
  raw: JsonObject,
  normalized: Readonly<Record<string, unknown>>,
  override: JsonObject,
): void {
  const renames = isObject(override.renames) ? override.renames : {};
  for (const oldName of sortedStrings(Object.keys(renames))) {
    const target = renames[oldName];
    if (Object.hasOwn(raw, oldName) && typeof target === "string") {
      report.add("renamed", oldName, `renamed_to:${target}`, raw[oldName]);
    }
  }
  for (const name of TRANSFORM_KEYS) {
    const configured = override[name];
    const fields = Array.isArray(configured) ? stringList(configured)
      : isObject(configured) ? Object.keys(configured) : [];
    for (const field of sortedStrings(fields)) {
      if (Object.hasOwn(raw, field)) report.add("transformed", field, name, raw[field]);
    }
  }
  for (const field of sortedStrings(stringList(override.drops))) {
    if (!field.includes(".") && Object.hasOwn(raw, field)) report.add("dropped_override", field, "override_drop", raw[field]);
  }
  const dropDefaults = isObject(override.drop_if_default) ? override.drop_if_default : {};
  for (const field of sortedStrings(Object.keys(dropDefaults))) {
    if (!field.includes(".") && Object.hasOwn(raw, field)
      && transformValueMatchesDefaultForAuthoring(raw[field], dropDefaults[field])) {
      report.add("dropped_default", field, "drop_if_default", raw[field]);
    }
  }
  const defaults = isObject(override.defaults) ? override.defaults : {};
  for (const field of sortedStrings(Object.keys(defaults))) {
    if (!Object.hasOwn(raw, field) && Object.hasOwn(normalized, field)) {
      report.add("defaulted", field, "override_default", normalized[field]);
    }
  }
}

export function reconcileItems(options: {
  readonly resourceType: string;
  readonly items: readonly unknown[];
  readonly resourceSchema: JsonObject;
  readonly override?: JsonObject;
  readonly apiMetadata?: ApiMetadata;
}): ReconciliationReport {
  const override = options.override ?? {};
  const report = new ReconciliationReport(options.resourceType);
  const block = terraformBlockForSchema(options.resourceSchema, options.resourceType);
  for (const [index, rawValue] of options.items.entries()) {
    report.itemCount += 1;
    const raw = record(rawValue, `items[${index}]`);
    const snakeRaw = record(snakeJsonKeysForAuthoring(raw), `items[${index}]`);
    const skipReason = transformSkipMatchReason(snakeRaw, override, options.resourceType);
    if (skipReason !== null) {
      report.add("skipped", "$item", skipReason, snakeRaw.name ?? snakeRaw.id);
      continue;
    }
    const normalized = applyTransformOverridesForAuthoring(snakeRaw, override, options.resourceType);
    recordOverrideActions(report, snakeRaw, normalized, override);
    walkBlock(report, "", normalized, block, override, true, options.apiMetadata);
  }
  return report;
}
