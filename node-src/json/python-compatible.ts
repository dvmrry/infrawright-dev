import { LosslessNumber } from "lossless-json";

import {
  canonicalPythonNumberToken,
  pythonFiniteFloatToken,
} from "./python-number.js";

type JsonObject = { readonly [key: string]: JsonValue };
export type JsonValue =
  | null
  | boolean
  | number
  | LosslessNumber
  | string
  | readonly JsonValue[]
  | JsonObject;

export function comparePythonStrings(
  left: string,
  right: string,
): number {
  let leftIndex = 0;
  let rightIndex = 0;
  while (leftIndex < left.length && rightIndex < right.length) {
    const leftPoint = left.codePointAt(leftIndex) ?? 0;
    const rightPoint = right.codePointAt(rightIndex) ?? 0;
    const delta = leftPoint - rightPoint;
    if (delta !== 0) {
      return delta;
    }
    leftIndex += leftPoint > 0xffff ? 2 : 1;
    rightIndex += rightPoint > 0xffff ? 2 : 1;
  }
  return (leftIndex < left.length ? 1 : 0) - (rightIndex < right.length ? 1 : 0);
}

export function sameStringSequence(
  left: readonly string[],
  right: readonly string[],
): boolean {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

export function sortedStrings(values: Iterable<string>): string[] {
  return Array.from(values).sort(comparePythonStrings);
}

function encodeString(value: string): string {
  return JSON.stringify(value).replace(/[\u0080-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function encodedStringLength(value: string, maximum: number): number {
  let length = 2;
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (
      code === 0x08
      || code === 0x09
      || code === 0x0a
      || code === 0x0c
      || code === 0x0d
      || code === 0x22
      || code === 0x5c
    ) {
      length += 2;
    } else if (code < 0x20 || code >= 0x80) {
      length += 6;
    } else {
      length += 1;
    }
    if (length > maximum) {
      return maximum + 1;
    }
  }
  return length;
}

function encodeNumber(value: number | LosslessNumber): string {
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token !== null) return token;
    throw new TypeError(
      "the Python-compatible renderer accepts finite JSON numbers only",
    );
  }
  if (!Number.isFinite(value)) {
    throw new TypeError(
      "the Python-compatible renderer accepts finite JSON numbers only",
    );
  }
  if (Number.isSafeInteger(value) && !Object.is(value, -0)) return String(value);
  const token = pythonFiniteFloatToken(value);
  if (token !== null) return token;
  throw new TypeError(
    "the Python-compatible renderer accepts finite JSON numbers only",
  );
}

function encodedLength(value: JsonValue, level: number, maximum: number): number {
  if (value === null) {
    return 4;
  }
  if (typeof value === "boolean") {
    return value ? 4 : 5;
  }
  if (typeof value === "number" || value instanceof LosslessNumber) {
    return encodeNumber(value).length;
  }
  if (typeof value === "string") {
    return encodedStringLength(value, maximum);
  }
  const currentIndent = level * 2;
  const childIndent = currentIndent + 2;
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return 2;
    }
    let length = 4 + currentIndent + ((value.length - 1) * 2);
    for (const item of value) {
      length += childIndent + encodedLength(item, level + 1, maximum);
      if (length > maximum) {
        return maximum + 1;
      }
    }
    return length;
  }
  const objectValue = value as JsonObject;
  const keys = Object.keys(objectValue);
  if (keys.length === 0) {
    return 2;
  }
  let length = 4 + currentIndent + ((keys.length - 1) * 2);
  for (const key of keys) {
    const child = objectValue[key];
    if (child === undefined) {
      throw new TypeError("undefined is not a JSON value");
    }
    length += childIndent
      + encodedStringLength(key, maximum)
      + 2
      + encodedLength(child, level + 1, maximum);
    if (length > maximum) {
      return maximum + 1;
    }
  }
  return length;
}

function encode(value: JsonValue, level: number): string {
  if (value === null) {
    return "null";
  }
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number" || value instanceof LosslessNumber) {
    return encodeNumber(value);
  }
  if (typeof value === "string") {
    return encodeString(value);
  }
  const currentIndent = "  ".repeat(level);
  const childIndent = "  ".repeat(level + 1);
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return "[]";
    }
    return [
      "[",
      value.map((item) => `${childIndent}${encode(item, level + 1)}`).join(",\n"),
      `${currentIndent}]`,
    ].join("\n");
  }
  const objectValue = value as JsonObject;
  const entries = sortedStrings(Object.keys(objectValue)).map((key) => {
    const child = objectValue[key];
    if (child === undefined) {
      throw new TypeError("undefined is not a JSON value");
    }
    return `${childIndent}${encodeString(key)}: ${encode(child, level + 1)}`;
  });
  if (entries.length === 0) {
    return "{}";
  }
  return ["{", entries.join(",\n"), `${currentIndent}}`].join("\n");
}

/** Match json.dumps(..., indent=2, sort_keys=True) for supported JSON numbers. */
export function renderPythonCompatibleJson(value: JsonValue): string {
  return `${encode(value, 0)}\n`;
}

/**
 * Measure the exact ASCII bytes emitted by renderPythonCompatibleJson.
 * Returns maximumBytes + 1 as soon as the rendered value cannot fit.
 */
export function pythonCompatibleJsonByteLength(
  value: JsonValue,
  maximumBytes = Number.MAX_SAFE_INTEGER - 1,
): number {
  if (
    !Number.isSafeInteger(maximumBytes)
    || maximumBytes < 0
    || maximumBytes >= Number.MAX_SAFE_INTEGER
  ) {
    throw new TypeError("maximumBytes must be a non-negative safe byte limit");
  }
  const body = encodedLength(value, 0, maximumBytes);
  return body >= maximumBytes ? maximumBytes + 1 : body + 1;
}
