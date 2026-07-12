import { types as utilTypes } from "node:util";

import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "./errors.js";

type JsonRecord = Record<string, unknown>;

export interface TerraformSchemaAttribute {
  readonly computed?: boolean;
  readonly deprecated?: boolean | string;
  readonly optional?: boolean;
  readonly required?: boolean;
  readonly sensitive?: boolean;
  readonly type?: unknown;
}

export interface TerraformSchemaBlockType {
  readonly block: TerraformSchemaBlock;
  readonly max_items?: number;
  readonly min_items?: number;
  readonly nesting_mode: string;
}

export interface TerraformSchemaBlock {
  readonly attributes?: Readonly<Record<string, TerraformSchemaAttribute>>;
  readonly block_types?: Readonly<Record<string, TerraformSchemaBlockType>>;
}

function fail(message: string): never {
  throw new ProcessFailure({
    code: "PROVIDER_STATE_PROJECTION_FAILED",
    category: "domain",
    message,
  });
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || utilTypes.isProxy(value)
  ) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function safeRecord(): JsonRecord {
  return Object.create(null) as JsonRecord;
}

function hasOwn(value: object, name: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, name);
}

function cloneJson(value: unknown, path: string): unknown {
  if (
    value === null
    || typeof value === "string"
    || typeof value === "boolean"
    || value instanceof LosslessNumber
  ) {
    return value instanceof LosslessNumber
      ? new LosslessNumber(value.toString())
      : value;
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) {
      return fail(`provider state contains an unsafe number at ${path}`);
    }
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((entry, index) => cloneJson(entry, `${path}[${index}]`));
  }
  if (isRecord(value)) {
    const output = safeRecord();
    for (const name of Object.keys(value).sort()) {
      output[name] = cloneJson(value[name], `${path}.${name}`);
    }
    return output;
  }
  return fail(`provider state contains a non-JSON value at ${path}`);
}

function anySensitive(value: unknown): boolean {
  if (value === true) return true;
  if (Array.isArray(value)) return value.some((entry) => anySensitive(entry));
  if (isRecord(value)) {
    return Object.keys(value).some((name) => anySensitive(value[name]));
  }
  return false;
}

function assertSensitiveShape(mask: unknown, value: unknown, path: string): void {
  if (mask === undefined || mask === null || typeof mask === "boolean") return;
  if (Array.isArray(mask)) {
    if (!Array.isArray(value) || mask.length !== value.length) {
      return fail(`provider sensitive mask does not match state at ${path}`);
    }
    for (let index = 0; index < mask.length; index += 1) {
      assertSensitiveShape(mask[index], value[index], `${path}[${index}]`);
    }
    return;
  }
  if (isRecord(mask)) {
    if (!isRecord(value)) {
      return fail(`provider sensitive mask does not match state at ${path}`);
    }
    for (const name of Object.keys(mask)) {
      if (!hasOwn(value, name)) {
        return fail(`provider sensitive mask does not match state at ${path}.${name}`);
      }
      assertSensitiveShape(mask[name], value[name], `${path}.${name}`);
    }
    return;
  }
  return fail(`provider sensitive mask has an unsupported shape at ${path}`);
}

function inputAttribute(
  name: string,
  attribute: TerraformSchemaAttribute,
  resourceTop: boolean,
): boolean {
  if (attribute.deprecated && !attribute.required) return false;
  if (resourceTop && name === "id" && attribute.computed) return false;
  return attribute.required === true || attribute.optional === true;
}

function blockHasInputs(block: TerraformSchemaBlock): boolean {
  for (const [name, attribute] of Object.entries(block.attributes ?? {})) {
    if (inputAttribute(name, attribute, false)) return true;
  }
  return Object.values(block.block_types ?? {}).some((entry) => {
    return blockHasInputs(entry.block);
  });
}

function singleBlock(blockType: TerraformSchemaBlockType): boolean {
  return blockType.nesting_mode === "single" || blockType.max_items === 1;
}

function singleValue(value: unknown, path: string): Readonly<Record<string, unknown>> | null {
  if (isRecord(value)) return value;
  if (Array.isArray(value)) {
    if (value.length === 0) return null;
    if (value.length === 1 && isRecord(value[0])) return value[0];
  }
  return fail(`single provider block has an unsupported shape at ${path}`);
}

function singleMask(mask: unknown, path: string): unknown {
  if (mask === true || isRecord(mask)) return mask;
  if (Array.isArray(mask)) {
    if (mask.length === 0) return {};
    if (mask.length === 1) return mask[0] ?? {};
    return fail(`single provider sensitive mask has an unsupported shape at ${path}`);
  }
  return {};
}

function listMask(mask: unknown, index: number): unknown {
  if (Array.isArray(mask)) return mask[index] ?? {};
  return isRecord(mask) ? mask : {};
}

function projectBlock(options: {
  readonly block: TerraformSchemaBlock;
  readonly path: string;
  readonly resourceTop: boolean;
  readonly sensitive: unknown;
  readonly values: unknown;
}): JsonRecord {
  const { block, path, resourceTop, sensitive, values } = options;
  if (sensitive === true) {
    return fail(`sensitive provider input cannot be projected at ${path}`);
  }
  if (!isRecord(values)) {
    return fail(`provider state path is not an object at ${path}`);
  }
  const output = safeRecord();
  for (const name of Object.keys(block.attributes ?? {}).sort()) {
    const attribute = block.attributes?.[name];
    if (attribute === undefined || !inputAttribute(name, attribute, resourceTop)) {
      continue;
    }
    const childPath = `${path}.${name}`;
    if (attribute.sensitive || (isRecord(sensitive) && anySensitive(sensitive[name]))) {
      return fail(`sensitive provider input cannot be projected at ${childPath}`);
    }
    if (!hasOwn(values, name) || values[name] === null) {
      if (attribute.required) {
        return fail(`required provider state path is missing: ${childPath}`);
      }
      continue;
    }
    output[name] = cloneJson(values[name], childPath);
  }

  for (const name of Object.keys(block.block_types ?? {}).sort()) {
    const blockType = block.block_types?.[name];
    if (blockType === undefined || !blockHasInputs(blockType.block)) continue;
    const childPath = `${path}.${name}`;
    const required = (blockType.min_items ?? 0) >= 1;
    if (!hasOwn(values, name) || values[name] === null) {
      if (required) return fail(`required provider state path is missing: ${childPath}`);
      continue;
    }
    const value = values[name];
    const childSensitive = isRecord(sensitive) ? sensitive[name] : {};
    if (childSensitive === true) {
      return fail(`sensitive provider input cannot be projected at ${childPath}`);
    }
    if (singleBlock(blockType)) {
      const member = singleValue(value, childPath);
      if (member === null) {
        if (required) return fail(`required provider state path is missing: ${childPath}`);
        continue;
      }
      output[name] = projectBlock({
        block: blockType.block,
        path: childPath,
        resourceTop: false,
        sensitive: singleMask(childSensitive, childPath),
        values: member,
      });
      continue;
    }
    if (!Array.isArray(value)) {
      return fail(`provider state path is not a list at ${childPath}`);
    }
    output[name] = value.map((member, index) => {
      return projectBlock({
        block: blockType.block,
        path: `${childPath}[${index}]`,
        resourceTop: false,
        sensitive: listMask(childSensitive, index),
        values: member,
      });
    });
  }
  return output;
}

/** Project provider-observed Terraform state into module-input shape. */
export function projectProviderState(options: {
  readonly schema: TerraformSchemaBlock;
  readonly sensitiveValues?: unknown;
  readonly values: unknown;
}): Readonly<Record<string, unknown>> {
  assertSensitiveShape(options.sensitiveValues ?? {}, options.values, "$state");
  return projectBlock({
    block: options.schema,
    path: "$state",
    resourceTop: true,
    sensitive: options.sensitiveValues ?? {},
    values: options.values,
  });
}
