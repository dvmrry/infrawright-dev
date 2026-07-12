import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "../domain/errors.js";
import { canonicalPythonNumberToken } from "./python-number.js";
import { sortedStrings } from "./python-compatible.js";

const INVALID_ARTIFACT_JSON_MESSAGE =
  "artifact JSON must contain plain JSON values with finite lossless numbers";

function invalidArtifactJson(): never {
  throw new ProcessFailure({
    code: "INVALID_ARTIFACT_JSON",
    category: "domain",
    message: INVALID_ARTIFACT_JSON_MESSAGE,
  });
}

function encodePythonString(value: string): string {
  const encoded = JSON.stringify(value);
  if (encoded === undefined) {
    return invalidArtifactJson();
  }
  // Python's ensure_ascii encoder escapes DEL (U+007F) as well as every
  // non-ASCII code unit. JSON.stringify already handles U+0000-U+001F, but
  // deliberately leaves U+007F literal, so include that boundary here.
  return encoded.replace(/[\u007f-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function encodeNumber(value: number | LosslessNumber): string {
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token === null) {
      return invalidArtifactJson();
    }
    return token;
  }
  if (!Number.isSafeInteger(value)) {
    return invalidArtifactJson();
  }
  return Object.is(value, -0) ? "0" : String(value);
}

function isDataDescriptor(
  descriptor: PropertyDescriptor | undefined,
): descriptor is PropertyDescriptor & { readonly value: unknown } {
  return descriptor !== undefined
    && descriptor.enumerable === true
    && Object.prototype.hasOwnProperty.call(descriptor, "value")
    && descriptor.get === undefined
    && descriptor.set === undefined;
}

function encodeArray(
  value: readonly unknown[],
  level: number,
  ancestors: WeakSet<object>,
): string {
  if (Object.getPrototypeOf(value) !== Array.prototype) {
    return invalidArtifactJson();
  }
  const descriptors = Object.getOwnPropertyDescriptors(value);
  const expectedKeys = new Set(["length"]);
  const children: unknown[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const key = String(index);
    expectedKeys.add(key);
    const descriptor = descriptors[key];
    if (!isDataDescriptor(descriptor)) {
      return invalidArtifactJson();
    }
    children.push(descriptor.value);
  }
  if (
    Reflect.ownKeys(descriptors).some((key) => {
      return typeof key !== "string" || !expectedKeys.has(key);
    })
  ) {
    return invalidArtifactJson();
  }
  if (children.length === 0) {
    return "[]";
  }
  const currentIndent = "  ".repeat(level);
  const childIndent = "  ".repeat(level + 1);
  return [
    "[",
    children.map((item) => {
      return `${childIndent}${encode(item, level + 1, ancestors)}`;
    }).join(",\n"),
    `${currentIndent}]`,
  ].join("\n");
}

function encodeRecord(
  value: object,
  level: number,
  ancestors: WeakSet<object>,
): string {
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return invalidArtifactJson();
  }
  const descriptors = Object.getOwnPropertyDescriptors(value);
  if (Reflect.ownKeys(descriptors).some((key) => typeof key !== "string")) {
    return invalidArtifactJson();
  }
  const entries = sortedStrings(Object.keys(descriptors)).map((key) => {
    const descriptor = descriptors[key];
    if (!isDataDescriptor(descriptor)) {
      return invalidArtifactJson();
    }
    return [key, descriptor.value] as const;
  });
  if (entries.length === 0) {
    return "{}";
  }
  const currentIndent = "  ".repeat(level);
  const childIndent = "  ".repeat(level + 1);
  return [
    "{",
    entries.map(([key, child]) => {
      return `${childIndent}${encodePythonString(key)}: ${encode(child, level + 1, ancestors)}`;
    }).join(",\n"),
    `${currentIndent}}`,
  ].join("\n");
}

function encode(
  value: unknown,
  level: number,
  ancestors: WeakSet<object>,
): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "string") {
    return encodePythonString(value);
  }
  if (typeof value === "number" || value instanceof LosslessNumber) {
    return encodeNumber(value);
  }
  if (typeof value !== "object") {
    return invalidArtifactJson();
  }
  if (ancestors.has(value)) {
    return invalidArtifactJson();
  }
  ancestors.add(value);
  try {
    return Array.isArray(value)
      ? encodeArray(value, level, ancestors)
      : encodeRecord(value, level, ancestors);
  } finally {
    ancestors.delete(value);
  }
}

/**
 * Match Python
 * `json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\\n"`
 * for the finite lossless-number artifact contract. Unlike process-control
 * JSON, this renderer preserves arbitrary-size integral tokens and reproduces
 * Python's binary64 spelling for float lexemes produced by pull parsing.
 */
export function renderPythonLosslessArtifactJson(value: unknown): string {
  try {
    return `${encode(value, 0, new WeakSet<object>())}\n`;
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && error.code === "INVALID_ARTIFACT_JSON"
      && error.message === INVALID_ARTIFACT_JSON_MESSAGE
    ) {
      throw error;
    }
    return invalidArtifactJson();
  }
}
