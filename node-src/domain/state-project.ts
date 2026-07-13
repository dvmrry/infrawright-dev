import { LosslessNumber } from "lossless-json";

import type { LoadedPackRoot } from "../metadata/loader.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
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
  type TerraformClassifiedAttributes,
  type TerraformTypeEncoding,
} from "../metadata/terraform-schema.js";
import { terraformJsonEqual } from "../json/python-equality.js";
import {
  parsePolicyPath,
  policySelectorMatches,
  type ConcretePathSegment,
  type PolicyPathSegment,
} from "./policy-paths.js";
import { projectLoadedRawField } from "./pull-transform.js";
import { DriftPolicy, type PolicyEntry } from "./drift-policy.js";

type JsonRecord = Record<string, unknown>;

export class ProjectionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ProjectionError";
  }
}

function record(value: unknown): value is JsonRecord {
  return isObject(value) && !(value instanceof LosslessNumber);
}

function cloneJson(value: unknown): unknown {
  if (value instanceof LosslessNumber) return new LosslessNumber(value.toString());
  if (Array.isArray(value)) return value.map(cloneJson);
  if (record(value)) {
    return Object.fromEntries(Object.keys(value).map((key) => [key, cloneJson(value[key])]));
  }
  return value;
}

function pathText(path: readonly ConcretePathSegment[]): string {
  if (path.length === 0) return "<root>";
  const parts: string[] = [];
  for (const segment of path) {
    if (typeof segment === "number") {
      const prior = parts.pop() ?? "";
      parts.push(`${prior}[${String(segment)}]`);
    } else {
      parts.push(segment);
    }
  }
  return parts.join(".");
}

/** Require Terraform's value-aligned sensitivity-mask grammar. */
export function validateSensitiveMaskShape(mask: unknown, value: unknown): void {
  const stack: Array<{ mask: unknown; value: unknown; root: boolean }> = [
    { mask, value, root: true },
  ];
  while (stack.length > 0) {
    const current = stack.pop();
    if (current === undefined) break;
    if (current.mask === undefined || current.mask === null || typeof current.mask === "boolean") {
      continue;
    }
    if (record(current.mask)) {
      const valueRecord = record(current.value) ? current.value : null;
      if (valueRecord === null) throw new ProjectionError("unsupported sensitive mask shape");
      for (const [key, child] of Object.entries(current.mask)) {
        if (!Object.hasOwn(valueRecord, key)) {
          throw new ProjectionError("unsupported sensitive mask shape");
        }
        stack.push({ mask: child, value: valueRecord[key], root: false });
      }
      continue;
    }
    if (Array.isArray(current.mask) && !current.root) {
      const valueArray = Array.isArray(current.value) ? current.value : null;
      if (valueArray === null || valueArray.length !== current.mask.length) {
        throw new ProjectionError("unsupported sensitive mask shape");
      }
      current.mask.forEach((child, index) => {
        stack.push({ mask: child, value: valueArray[index], root: false });
      });
      continue;
    }
    throw new ProjectionError("unsupported sensitive mask shape");
  }
}

function anySensitive(value: unknown): boolean {
  if (value === true) return true;
  if (Array.isArray(value)) return value.some(anySensitive);
  if (record(value)) return Object.values(value).some(anySensitive);
  return false;
}

function sensitiveAttribute(mask: unknown, name: string): boolean {
  return record(mask) && anySensitive(mask[name]);
}

function classified(
  block: JsonObject,
  label: string,
  resourceTop: boolean,
): TerraformClassifiedAttributes {
  return resourceTop
    ? terraformResourceInputAttributes(block, label)
    : terraformClassifyAttributes(block, label);
}

function requiredBlock(blockType: JsonObject): boolean {
  const value = blockType.min_items;
  return typeof value === "number" && value >= 1;
}

function singleValue(value: unknown): JsonRecord | null {
  if (record(value)) return value;
  if (Array.isArray(value)) {
    if (value.length === 0) return null;
    if (value.length === 1 && record(value[0])) return value[0];
  }
  throw new ProjectionError("single nested block had unsupported state shape");
}

function singleSensitivity(mask: unknown, path: readonly ConcretePathSegment[]): unknown {
  if (mask === true || record(mask)) return mask;
  if (Array.isArray(mask)) {
    if (mask.length === 0) return {};
    if (mask.length === 1) return mask[0] ?? {};
    throw new ProjectionError(
      `single nested block had unsupported sensitive shape at ${pathText(path)}`,
    );
  }
  return {};
}

function listSensitivity(mask: unknown, index: number): unknown {
  if (Array.isArray(mask) && index < mask.length) return mask[index] ?? {};
  if (record(mask)) return mask;
  return {};
}

function projectBlock(options: {
  readonly block: JsonObject;
  readonly label: string;
  readonly mask: unknown;
  readonly path: readonly ConcretePathSegment[];
  readonly policy: DriftPolicy | null;
  readonly resourceTop: boolean;
  readonly resourceType: string;
  readonly values: unknown;
}): JsonRecord {
  if (options.mask === true) {
    throw new ProjectionError(
      `sensitive input path ${pathText(options.path)} cannot be written to generated tfvars without an explicit secret-handling policy`,
    );
  }
  if (!record(options.values)) {
    throw new ProjectionError(`state path ${pathText(options.path)} is not an object`);
  }
  const inputs = classified(options.block, options.label, options.resourceTop);
  const required = new Set(inputs.required);
  const optional = new Set(inputs.optional);
  const output: JsonRecord = Object.create(null) as JsonRecord;
  for (const name of [...required, ...optional].sort()) {
    const childPath = [...options.path, name];
    if (options.policy?.projectionOmits(options.resourceType, childPath)) {
      if (required.has(name)) {
        throw new ProjectionError(`policy cannot projection_omit required path ${pathText(childPath)}`);
      }
      continue;
    }
    if (sensitiveAttribute(options.mask, name)) {
      throw new ProjectionError(
        `sensitive input path ${pathText(childPath)} cannot be written to generated tfvars without an explicit secret-handling policy`,
      );
    }
    const value = options.values[name];
    if (!Object.hasOwn(options.values, name) || value === null || value === undefined) {
      if (required.has(name)) {
        throw new ProjectionError(`required state path missing: ${pathText(childPath)}`);
      }
      continue;
    }
    output[name] = cloneJson(value);
  }

  for (const [name, blockType] of terraformInputBlockTypes(options.block, options.label)) {
    const childPath = [...options.path, name];
    const required = requiredBlock(blockType);
    if (options.policy?.projectionOmits(options.resourceType, childPath)) {
      if (required) {
        throw new ProjectionError(`policy cannot projection_omit required block ${pathText(childPath)}`);
      }
      continue;
    }
    const value = options.values[name];
    if (!Object.hasOwn(options.values, name) || value === null || value === undefined) {
      if (required) throw new ProjectionError(`required state path missing: ${pathText(childPath)}`);
      continue;
    }
    const inner = terraformRequireObject(blockType.block, `${options.label}.block_types.${name}.block`);
    const childMask = record(options.mask) ? options.mask[name] : {};
    if (childMask === true) {
      throw new ProjectionError(
        `sensitive input path ${pathText(childPath)} cannot be written to generated tfvars without an explicit secret-handling policy`,
      );
    }
    if (terraformBlockIsSingle(blockType)) {
      const single = singleValue(value);
      if (single === null) {
        if (required) throw new ProjectionError(`required state path missing: ${pathText(childPath)}`);
        continue;
      }
      output[name] = projectBlock({
        block: inner,
        label: `${options.label}.block_types.${name}.block`,
        mask: singleSensitivity(childMask, childPath),
        path: childPath,
        policy: options.policy,
        resourceTop: false,
        resourceType: options.resourceType,
        values: single,
      });
      continue;
    }
    if (!Array.isArray(value)) {
      throw new ProjectionError(`state path ${pathText(childPath)} is not a list`);
    }
    output[name] = value.map((member, index) => {
      const memberPath = [...childPath, index];
      if (!record(member)) {
        throw new ProjectionError(`state path ${pathText(memberPath)} is not an object`);
      }
      return projectBlock({
        block: inner,
        label: `${options.label}.block_types.${name}.block`,
        mask: listSensitivity(childMask, index),
        path: memberPath,
        policy: options.policy,
        resourceTop: false,
        resourceType: options.resourceType,
        values: member,
      });
    });
  }
  return output;
}

function stripCollection(path: readonly PolicyPathSegment[]): readonly PolicyPathSegment[] {
  if (
    path.length > 0
    && (typeof path[0] === "number" || typeof path[0] === "bigint" || path[0] === "*")
  ) return path.slice(1);
  return path;
}

function schemaStatusEncoding(
  encoding: TerraformTypeEncoding,
  path: readonly PolicyPathSegment[],
  base: "required" | "optional",
): string {
  if (path.length === 0) return base;
  if (typeof encoding === "string") return "unknown";
  const [kind, inner] = encoding;
  if (kind === "list" || kind === "set") {
    return schemaStatusEncoding(inner, stripCollection(path), base);
  }
  if (kind === "map") return base;
  if (kind === "object" && typeof path[0] === "string" && Object.hasOwn(inner, path[0])) {
    return schemaStatusEncoding(inner[path[0]] as TerraformTypeEncoding, path.slice(1), base);
  }
  return "unknown";
}

function schemaStatusBlock(
  block: JsonObject,
  path: readonly PolicyPathSegment[],
  label: string,
  resourceTop: boolean,
  requiredness: boolean,
): string {
  if (path.length === 0) return "block";
  const segment = path[0];
  if (typeof segment !== "string" || segment === "*") return "unknown";
  const attributes = terraformAttributesForBlock(block, label);
  const inputs = classified(block, label, resourceTop);
  const required = new Set(inputs.required);
  const optional = new Set(inputs.optional);
  if (required.has(segment) || optional.has(segment)) {
    const base = required.has(segment) ? "required" : "optional";
    if (path.length === 1) return base;
    const attribute = terraformRequireObject(attributes[segment], `${label}.attributes.${segment}`);
    return schemaStatusEncoding(terraformAttributeType(attribute, `${label}.attributes.${segment}`), path.slice(1), base);
  }
  const allBlocks = terraformBlockTypesForBlock(block, label);
  const inputBlocks = terraformInputBlockTypes(block, label);
  const blockType = inputBlocks.get(segment);
  if (blockType !== undefined) {
    if (path.length === 1 && requiredness) return requiredBlock(blockType) ? "required" : "optional";
    const child = terraformRequireObject(blockType.block, `${label}.block_types.${segment}.block`);
    return schemaStatusBlock(child, stripCollection(path.slice(1)), `${label}.block_types.${segment}.block`, false, requiredness);
  }
  if (Object.hasOwn(attributes, segment) || Object.hasOwn(allBlocks, segment)) return "computed_only";
  return "unknown";
}

export function providerSchemaStatus(options: {
  readonly path: readonly PolicyPathSegment[];
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
  readonly requiredness?: boolean;
}): string {
  const block = terraformBlockForSchema(options.schema as JsonObject, options.resourceType);
  return schemaStatusBlock(
    block,
    options.path,
    options.resourceType,
    true,
    options.requiredness === true,
  );
}

function pathValue(value: unknown, path: readonly PolicyPathSegment[]): {
  readonly present: boolean;
  readonly value?: unknown;
} {
  let current = value;
  for (const segment of path) {
    if (typeof segment !== "string" || !record(current) || !Object.hasOwn(current, segment)) {
      return { present: false };
    }
    current = current[segment];
  }
  return { present: true, value: current };
}

function absentOrEmpty(value: unknown): boolean {
  return value === null
    || value === undefined
    || (Array.isArray(value) && value.length === 0)
    || (record(value) && Object.keys(value).length === 0);
}

function setPath(target: JsonRecord, path: readonly PolicyPathSegment[], value: unknown): void {
  if (path.length === 0 || path.some((segment) => typeof segment !== "string")) {
    throw new ProjectionError(`cannot write projection path ${JSON.stringify(path)}`);
  }
  let current = target;
  for (const segment of path.slice(0, -1) as string[]) {
    if (!Object.hasOwn(current, segment) || current[segment] === null) {
      current[segment] = Object.create(null) as JsonRecord;
    } else if (!record(current[segment])) {
      throw new ProjectionError(`cannot projection_sync through non-object path ${pathText(path as ConcretePathSegment[])}`);
    }
    current = current[segment] as JsonRecord;
  }
  current[path.at(-1) as string] = cloneJson(value);
}

function schemaTypeEncoding(
  encoding: TerraformTypeEncoding,
  path: readonly PolicyPathSegment[],
): TerraformTypeEncoding | null {
  if (path.length === 0) return encoding;
  if (typeof encoding === "string") return null;
  const [kind, inner] = encoding;
  if (kind === "list" || kind === "set") return schemaTypeEncoding(inner, stripCollection(path));
  if (kind === "map") return schemaTypeEncoding(inner, path.slice(1));
  if (kind === "object" && typeof path[0] === "string" && Object.hasOwn(inner, path[0])) {
    return schemaTypeEncoding(inner[path[0]] as TerraformTypeEncoding, path.slice(1));
  }
  return null;
}

function schemaTypeBlock(
  block: JsonObject,
  path: readonly PolicyPathSegment[],
  label: string,
  resourceTop: boolean,
): TerraformTypeEncoding | null {
  if (path.length === 0 || typeof path[0] !== "string") return null;
  const segment = path[0];
  const inputs = classified(block, label, resourceTop);
  const attributes = terraformAttributesForBlock(block, label);
  if (inputs.required.includes(segment) || inputs.optional.includes(segment)) {
    const attribute = terraformRequireObject(attributes[segment], `${label}.attributes.${segment}`);
    return schemaTypeEncoding(terraformAttributeType(attribute, `${label}.attributes.${segment}`), path.slice(1));
  }
  const blockType = terraformInputBlockTypes(block, label).get(segment);
  if (blockType === undefined) return null;
  return schemaTypeBlock(
    terraformRequireObject(blockType.block, `${label}.block_types.${segment}.block`),
    stripCollection(path.slice(1)),
    `${label}.block_types.${segment}.block`,
    false,
  );
}

function encodingKey(value: TerraformTypeEncoding | null): string {
  return JSON.stringify(value);
}

function guardSyncPath(
  block: JsonObject,
  path: readonly PolicyPathSegment[],
  label: string,
  resourceTop: boolean,
  field: string,
  rawPath: string,
  resourceType: string,
): void {
  if (path.length <= 1 || typeof path[0] !== "string") return;
  const segment = path[0];
  const inputs = classified(block, label, resourceTop);
  const attrs = terraformAttributesForBlock(block, label);
  if (inputs.required.includes(segment) || inputs.optional.includes(segment)) {
    const attr = terraformRequireObject(attrs[segment], `${label}.attributes.${segment}`);
    let encoding = terraformAttributeType(attr, `${label}.attributes.${segment}`);
    let rest = path.slice(1);
    while (rest.length > 0 && typeof encoding !== "string") {
      const [kind, inner] = encoding;
      if (kind === "list" || kind === "set") {
        throw new ProjectionError(
          `refusing to projection_sync ${field} ${rawPath} of ${resourceType}: non-terminal segment ${segment} is a ${kind}-typed attribute, not an object-shaped container`,
        );
      }
      if (kind === "map") {
        rest = rest.slice(1);
        encoding = inner;
      } else if (kind === "object" && typeof rest[0] === "string" && Object.hasOwn(inner, rest[0])) {
        encoding = inner[rest[0]] as TerraformTypeEncoding;
        rest = rest.slice(1);
      } else break;
    }
    return;
  }
  const blockType = terraformInputBlockTypes(block, label).get(segment);
  if (blockType !== undefined) {
    if (!terraformBlockIsSingle(blockType)) {
      throw new ProjectionError(
        `refusing to projection_sync ${field} ${rawPath} of ${resourceType}: non-terminal segment ${segment} is a repeated block, not an object-shaped container`,
      );
    }
    guardSyncPath(
      terraformRequireObject(blockType.block, `${label}.block_types.${segment}.block`),
      path.slice(1),
      `${label}.block_types.${segment}.block`,
      false,
      field,
      rawPath,
      resourceType,
    );
  }
}

function applyProjectionSync(options: {
  readonly block: JsonObject;
  readonly output: JsonRecord;
  readonly policy: DriftPolicy;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}): void {
  for (const entry of options.policy.entries(options.resourceType, "projection_sync")) {
    const targetText = String(entry.target_path);
    const sourceText = String(entry.source_path);
    const target = parsePolicyPath(targetText);
    const source = parsePolicyPath(sourceText);
    if (providerSchemaStatus({ path: target, resourceType: options.resourceType, schema: options.schema }) !== "required"
      && providerSchemaStatus({ path: target, resourceType: options.resourceType, schema: options.schema }) !== "optional") {
      throw new ProjectionError(`refusing to projection_sync target attribute ${targetText} of ${options.resourceType}: not a writable input attribute`);
    }
    guardSyncPath(options.block, target, options.resourceType, true, "target_path", targetText, options.resourceType);
    guardSyncPath(options.block, source, options.resourceType, true, "source_path", sourceText, options.resourceType);
    const targetType = schemaTypeBlock(options.block, target, options.resourceType, true);
    const sourceType = schemaTypeBlock(options.block, source, options.resourceType, true);
    if (encodingKey(targetType) !== encodingKey(sourceType)) {
      throw new ProjectionError(`refusing to projection_sync target ${targetText} from source ${sourceText} of ${options.resourceType}: schema types differ`);
    }
    const targetValue = pathValue(options.output, target);
    if (targetValue.present && !absentOrEmpty(targetValue.value)) continue;
    const sourceValue = pathValue(options.output, source);
    if (!sourceValue.present || absentOrEmpty(sourceValue.value)) continue;
    setPath(options.output, target, sourceValue.value);
    options.policy.markMatched(entry);
  }
}

function attributeSensitive(attribute: JsonObject): boolean {
  if (attribute.sensitive === true) return true;
  if (!record(attribute.nested_type)) return false;
  return Object.values(terraformAttributesForBlock(attribute.nested_type, "nested_type"))
    .some((value) => record(value) && attributeSensitive(value));
}

function blockContainsSensitive(block: JsonObject, label: string): boolean {
  if (Object.values(terraformAttributesForBlock(block, label))
    .some((value) => record(value) && attributeSensitive(value))) return true;
  for (const [name, raw] of Object.entries(terraformBlockTypesForBlock(block, label))) {
    if (!record(raw)) continue;
    const child = terraformRequireObject(raw.block, `${label}.block_types.${name}.block`);
    if (blockContainsSensitive(child, `${label}.block_types.${name}.block`)) return true;
  }
  return false;
}

function emptyFill(value: unknown): boolean {
  if (value === null || value === undefined || value === "") return true;
  if (Array.isArray(value)) return value.length === 0 || value.every(emptyFill);
  if (record(value)) return Object.keys(value).length === 0;
  return false;
}

export function projectionFillValue(options: {
  readonly entry: PolicyEntry;
  readonly rawItem: Readonly<Record<string, unknown>>;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}): unknown | undefined {
  const target = String(options.entry.path);
  const source = String(options.entry.source);
  const targetPath = parsePolicyPath(target);
  const status = providerSchemaStatus({
    path: targetPath,
    requiredness: true,
    resourceType: options.resourceType,
    schema: options.schema,
  });
  if (status !== "required" && status !== "optional") {
    throw new ProjectionError(`refusing to projection_fill path ${target} of ${options.resourceType}: not a writable input`);
  }
  const block = terraformBlockForSchema(options.schema as JsonObject, options.resourceType);
  const name = targetPath[0];
  if (typeof name !== "string") throw new ProjectionError(`invalid projection_fill path ${target}`);
  const attr = terraformAttributesForBlock(block, options.resourceType)[name];
  if (record(attr) && attributeSensitive(attr)) {
    throw new ProjectionError(`refusing to projection_fill sensitive path ${target} of ${options.resourceType}`);
  }
  const blockType = terraformInputBlockTypes(block, options.resourceType).get(name);
  if (blockType !== undefined) {
    const child = terraformRequireObject(blockType.block, `${options.resourceType}.block_types.${name}.block`);
    if (blockContainsSensitive(child, `${options.resourceType}.block_types.${name}.block`)) {
      throw new ProjectionError(`refusing to projection_fill sensitive block ${target} of ${options.resourceType}`);
    }
  }
  if (!Object.hasOwn(options.rawItem, source) || emptyFill(options.rawItem[source])) return undefined;
  const value = projectLoadedRawField({
    rawValue: options.rawItem[source],
    resourceType: options.resourceType,
    schema: options.schema,
    target: name,
  });
  return emptyFill(value) ? undefined : value;
}

function applyProjectionFill(options: {
  readonly output: JsonRecord;
  readonly policy: DriftPolicy;
  readonly rawItem: Readonly<Record<string, unknown>> | null;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}): void {
  const entries = options.policy.entries(options.resourceType, "projection_fill");
  if (entries.length > 0 && options.rawItem === null) {
    throw new ProjectionError(`${options.resourceType} projection_fill requires the raw API item`);
  }
  for (const entry of entries) {
    const target = parsePolicyPath(entry.path);
    if (pathValue(options.output, target).present) continue;
    const value = projectionFillValue({
      entry,
      rawItem: options.rawItem as Readonly<Record<string, unknown>>,
      resourceType: options.resourceType,
      schema: options.schema,
    });
    if (value === undefined) continue;
    setPath(options.output, target, value);
    options.policy.markMatched(entry);
  }
}

function leaf(value: unknown): boolean {
  return !Array.isArray(value) && !record(value);
}

function removeMatchingLeaves(
  value: unknown,
  selector: readonly PolicyPathSegment[],
  values: readonly unknown[],
  path: readonly ConcretePathSegment[] = [],
): number {
  let removed = 0;
  if (record(value)) {
    for (const key of Object.keys(value).sort()) {
      const child = value[key];
      const childPath = [...path, key];
      if (leaf(child) && policySelectorMatches(selector, childPath)
        && values.some((candidate) => terraformJsonEqual(child, candidate))) {
        delete value[key];
        removed += 1;
      } else {
        removed += removeMatchingLeaves(child, selector, values, childPath);
      }
    }
  } else if (Array.isArray(value)) {
    for (let index = value.length - 1; index >= 0; index -= 1) {
      const child = value[index];
      const childPath = [...path, index];
      if (leaf(child) && policySelectorMatches(selector, childPath)
        && values.some((candidate) => terraformJsonEqual(child, candidate))) {
        value.splice(index, 1);
        removed += 1;
      } else {
        removed += removeMatchingLeaves(child, selector, values, childPath);
      }
    }
  }
  return removed;
}

function applyProjectionOmitIf(options: {
  readonly output: JsonRecord;
  readonly policy: DriftPolicy;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}): void {
  for (const entry of options.policy.entries(options.resourceType, "projection_omit_if")) {
    const selector = parsePolicyPath(entry.path);
    if (providerSchemaStatus({ path: selector, resourceType: options.resourceType, schema: options.schema }) === "required") {
      throw new ProjectionError(`refusing to conditionally omit required attribute ${String(entry.path)} of ${options.resourceType}`);
    }
    const values = Array.isArray(entry.values) ? entry.values : [];
    if (removeMatchingLeaves(options.output, selector, values) > 0) {
      options.policy.markMatched(entry);
    }
  }
}

/** Project one provider-observed resource state object into module input shape. */
export async function projectProviderState(options: {
  readonly rawItem?: Readonly<Record<string, unknown>>;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
  readonly sensitiveValues?: unknown;
  readonly stateValues: unknown;
  readonly policy?: DriftPolicy;
}): Promise<Readonly<Record<string, unknown>>> {
  validateSensitiveMaskShape(options.sensitiveValues ?? {}, options.stateValues);
  const schema = await options.root.loadResourceSchema(options.resourceType);
  const block = terraformBlockForSchema(schema as JsonObject, options.resourceType);
  const policy = options.policy ?? null;
  const output = projectBlock({
    block,
    label: options.resourceType,
    mask: options.sensitiveValues ?? {},
    path: [],
    policy,
    resourceTop: true,
    resourceType: options.resourceType,
    values: options.stateValues,
  });
  if (policy !== null) {
    applyProjectionSync({ block, output, policy, resourceType: options.resourceType, schema });
    applyProjectionFill({
      output,
      policy,
      rawItem: options.rawItem ?? null,
      resourceType: options.resourceType,
      schema,
    });
    applyProjectionOmitIf({ output, policy, resourceType: options.resourceType, schema });
  }
  return output;
}
