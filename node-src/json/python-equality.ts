import { LosslessNumber } from "lossless-json";

import { sortedStrings } from "./python-compatible.js";

interface NumericValue {
  readonly kind: "integer" | "float";
  readonly integer?: bigint;
  readonly float?: number;
}

interface ExactDecimalValue {
  readonly coefficient: string;
  readonly exponent: bigint;
  readonly sign: -1 | 0 | 1;
}

const EXACT_DECIMAL = /^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$/;

export function isJsonRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object"
    && value !== null
    && !Array.isArray(value)
    && !(value instanceof LosslessNumber);
}

function numericValue(value: unknown): NumericValue | null {
  if (typeof value === "boolean") {
    return { kind: "integer", integer: value ? 1n : 0n };
  }
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    if (/^-?(?:0|[1-9][0-9]*)$/.test(token)) {
      return { kind: "integer", integer: BigInt(token) };
    }
    return { kind: "float", float: Number(token) };
  }
  if (typeof value === "number") {
    if (Number.isSafeInteger(value) && !Object.is(value, -0)) {
      return { kind: "integer", integer: BigInt(value) };
    }
    return { kind: "float", float: value };
  }
  return null;
}

function numericallyEqual(left: NumericValue, right: NumericValue): boolean {
  if (left.kind === "integer" && right.kind === "integer") {
    return left.integer === right.integer;
  }
  if (left.kind === "float" && right.kind === "float") {
    return left.float === right.float;
  }
  const integer = left.kind === "integer" ? left.integer : right.integer;
  const float = left.kind === "float" ? left.float : right.float;
  return integer !== undefined
    && float !== undefined
    && Number.isFinite(float)
    && Number.isInteger(float)
    && BigInt(float) === integer;
}

function exactDecimalValue(value: unknown): ExactDecimalValue | null {
  const token = value instanceof LosslessNumber
    ? value.toString()
    : typeof value === "number" && Number.isFinite(value)
      ? value.toString()
      : null;
  if (token === null) return null;
  const match = EXACT_DECIMAL.exec(token);
  if (match === null) return null;
  const fraction = match[3] ?? "";
  let coefficient = `${match[2]}${fraction}`.replace(/^0+/u, "");
  if (coefficient.length === 0) {
    return { coefficient: "0", exponent: 0n, sign: 0 };
  }
  let trailingZeros = 0;
  while (coefficient.endsWith("0")) {
    coefficient = coefficient.slice(0, -1);
    trailingZeros += 1;
  }
  return {
    coefficient,
    exponent: BigInt(match[4] ?? "0") - BigInt(fraction.length) + BigInt(trailingZeros),
    sign: match[1] === "-" ? -1 : 1,
  };
}

function exactDecimalsEqual(left: ExactDecimalValue, right: ExactDecimalValue): boolean {
  return left.sign === right.sign
    && left.coefficient === right.coefficient
    && left.exponent === right.exponent;
}

/** Match Python JSON equality without truncating integer tokens. */
export function pythonJsonEqual(left: unknown, right: unknown): boolean {
  const leftNumber = numericValue(left);
  const rightNumber = numericValue(right);
  if (leftNumber !== null || rightNumber !== null) {
    return leftNumber !== null
      && rightNumber !== null
      && numericallyEqual(leftNumber, rightNumber);
  }
  if (left === null || right === null) {
    return left === right;
  }
  if (typeof left === "string" || typeof right === "string") {
    return typeof left === "string" && left === right;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => pythonJsonEqual(value, right[index]));
  }
  if (isJsonRecord(left) || isJsonRecord(right)) {
    if (!isJsonRecord(left) || !isJsonRecord(right)) {
      return false;
    }
    const leftKeys = sortedStrings(Object.keys(left));
    const rightKeys = sortedStrings(Object.keys(right));
    return leftKeys.length === rightKeys.length
      && leftKeys.every((key, index) => key === rightKeys[index])
      && leftKeys.every((key) => pythonJsonEqual(left[key], right[key]));
  }
  return left === right;
}

/**
 * Compare Terraform JSON values without Python's bool/int coercion.
 *
 * Terraform may render an integer and its exactly equivalent floating-point
 * spelling for the same cty number, but JSON booleans remain a distinct cty
 * type and must never compare equal to 0 or 1.
 */
export function terraformJsonEqual(left: unknown, right: unknown): boolean {
  if (typeof left === "boolean" || typeof right === "boolean") {
    return typeof left === "boolean"
      && typeof right === "boolean"
      && left === right;
  }
  const leftNumber = numericValue(left);
  const rightNumber = numericValue(right);
  if (leftNumber !== null || rightNumber !== null) {
    return leftNumber !== null
      && rightNumber !== null
      && numericallyEqual(leftNumber, rightNumber);
  }
  if (left === null || right === null) {
    return left === right;
  }
  if (typeof left === "string" || typeof right === "string") {
    return typeof left === "string" && left === right;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => terraformJsonEqual(value, right[index]));
  }
  if (isJsonRecord(left) || isJsonRecord(right)) {
    if (!isJsonRecord(left) || !isJsonRecord(right)) {
      return false;
    }
    const leftKeys = sortedStrings(Object.keys(left));
    const rightKeys = sortedStrings(Object.keys(right));
    return leftKeys.length === rightKeys.length
      && leftKeys.every((key, index) => key === rightKeys[index])
      && leftKeys.every((key) => terraformJsonEqual(left[key], right[key]));
  }
  return left === right;
}

/**
 * Compare Terraform JSON evidence without binary floating-point coercion.
 *
 * This stricter contract is reserved for authorization boundaries that must
 * prove independently emitted plan surfaces carry the same exact cty number.
 * The broader parity/classification contract above intentionally retains the
 * established Python numeric-equality behavior.
 */
export function terraformJsonExactlyEqual(left: unknown, right: unknown): boolean {
  if (typeof left === "boolean" || typeof right === "boolean") {
    return typeof left === "boolean"
      && typeof right === "boolean"
      && left === right;
  }
  const leftNumber = exactDecimalValue(left);
  const rightNumber = exactDecimalValue(right);
  if (leftNumber !== null || rightNumber !== null) {
    return leftNumber !== null
      && rightNumber !== null
      && exactDecimalsEqual(leftNumber, rightNumber);
  }
  if (left === null || right === null) {
    return left === right;
  }
  if (typeof left === "string" || typeof right === "string") {
    return typeof left === "string" && left === right;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((value, index) => terraformJsonExactlyEqual(value, right[index]));
  }
  if (isJsonRecord(left) || isJsonRecord(right)) {
    if (!isJsonRecord(left) || !isJsonRecord(right)) {
      return false;
    }
    const leftKeys = sortedStrings(Object.keys(left));
    const rightKeys = sortedStrings(Object.keys(right));
    return leftKeys.length === rightKeys.length
      && leftKeys.every((key, index) => key === rightKeys[index])
      && leftKeys.every((key) => terraformJsonExactlyEqual(left[key], right[key]));
  }
  return left === right;
}
