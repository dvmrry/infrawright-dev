import { readFile } from "node:fs/promises";
import { LosslessNumber } from "lossless-json";

import { sortedStrings } from "../json/python-compatible.js";

export type JsonObject = Record<string, unknown>;

export class MetadataError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MetadataError";
  }
}

export function fail(message: string): never {
  throw new MetadataError(message);
}

export function isObject(value: unknown): value is JsonObject {
  if (
    typeof value !== "object"
    || value === null
    || Array.isArray(value)
    || value instanceof LosslessNumber
  ) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null;
}

export function requireObject(value: unknown, path: string): JsonObject {
  if (!isObject(value)) {
    return fail(`${path} must contain a JSON object`);
  }
  return value;
}

export function rejectUnknownKeys(
  value: JsonObject,
  allowed: ReadonlySet<string>,
  path: string,
): void {
  const unknown = sortedStrings(
    Object.keys(value).filter((key) => !allowed.has(key)),
  );
  if (unknown.length > 0) {
    fail(`${path}: unknown key ${unknown[0]}`);
  }
}

export function requireKeys(
  value: JsonObject,
  required: ReadonlySet<string>,
  path: string,
): void {
  const missing = sortedStrings(
    [...required].filter((key) => !Object.hasOwn(value, key)),
  );
  if (missing.length > 0) {
    fail(`${path}: missing required key ${missing[0]}`);
  }
}

export function requireNonEmptyString(value: unknown, path: string): string {
  if (typeof value !== "string" || value.length === 0) {
    return fail(`${path} must be a non-empty string`);
  }
  return value;
}

export function validateStringMap(
  value: unknown,
  path: string,
): Readonly<Record<string, string>> {
  if (!isObject(value)) {
    return fail(`${path} must be an object`);
  }
  const output: Record<string, string> = Object.create(null) as Record<
    string,
    string
  >;
  for (const key of Object.keys(value)) {
    if (key.length === 0) {
      fail(`${path} keys must be non-empty strings`);
    }
    output[key] = requireNonEmptyString(value[key], `${path}.${key}`);
  }
  return output;
}

const JSON_INTEGER = /^-?(?:0|[1-9][0-9]*)$/;
const MIN_SAFE_INTEGER = BigInt(Number.MIN_SAFE_INTEGER);
const MAX_SAFE_INTEGER = BigInt(Number.MAX_SAFE_INTEGER);

function parseMetadataJson(text: string, preserveNumericTokens: boolean): unknown {
  const parseWithSource = JSON.parse as unknown as (
    source: string,
    reviver: (
      key: string,
      value: unknown,
      context: { readonly source?: string },
    ) => unknown,
  ) => unknown;
  return parseWithSource(
    text,
    (_key: string, value: unknown, context: { readonly source?: string }) => {
      if (typeof value !== "number" || context.source === undefined) return value;
      const token = context.source;
      if (preserveNumericTokens) return new LosslessNumber(token);
      if (JSON_INTEGER.test(token)) {
        const integer = BigInt(token);
        return integer >= MIN_SAFE_INTEGER && integer <= MAX_SAFE_INTEGER
          ? Number(integer)
          : new LosslessNumber(token);
      }
      return Number.isFinite(value) ? value : new LosslessNumber(token);
    },
  );
}

export async function readJson(
  path: string,
  options?: { readonly preserveNumericTokens?: boolean },
): Promise<unknown> {
  let text: string;
  try {
    text = await readFile(path, "utf8");
  } catch (error: unknown) {
    const detail = error instanceof Error ? error.message : String(error);
    return fail(`failed to read ${path}: ${detail}`);
  }
  try {
    return parseMetadataJson(text, options?.preserveNumericTokens === true);
  } catch (error: unknown) {
    const detail = error instanceof Error ? error.message : String(error);
    return fail(`failed to read ${path}: ${detail}`);
  }
}

export function isFiniteJsonNumber(
  value: unknown,
): value is number | LosslessNumber {
  return (typeof value === "number" && Number.isFinite(value))
    || (value instanceof LosslessNumber && Number.isFinite(Number(value.toString())));
}

export function isIntegerJsonNumber(
  value: unknown,
): value is number | LosslessNumber {
  return (typeof value === "number" && Number.isInteger(value))
    || (value instanceof LosslessNumber && JSON_INTEGER.test(value.toString()));
}

export function isJsonScalar(value: unknown): boolean {
  return value === null
    || typeof value === "string"
    || typeof value === "boolean"
    || typeof value === "number"
    || value instanceof LosslessNumber;
}
