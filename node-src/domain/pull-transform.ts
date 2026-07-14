import {
  LosslessNumber,
} from "lossless-json";

import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import {
  comparePythonStrings,
  sortedStrings,
} from "../json/python-compatible.js";
import {
  canonicalPythonNumberToken,
  pythonFiniteFloatToken,
} from "../json/python-number.js";
import { pythonHtmlUnescape } from "./python-html-unescape.js";
import { pythonLower151 } from "../json/python-lower-151.js";
import type { LoadedResourceMetadata } from "../metadata/loader.js";
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
  type TerraformTypeEncoding,
} from "../metadata/terraform-schema.js";
import type {
  TransformCatalog,
  TransformCatalogResource,
} from "./transform-catalog.js";
import { requireSupportedZccTransformCatalog } from "./transform-catalog.js";

type TransformRecord = Record<string, unknown>;

interface RuntimeProjectionBlock {
  readonly cardinality: "many" | "single";
  readonly merge: boolean;
  readonly projection: RuntimeProjection;
}

interface RuntimeProjection {
  readonly attributes: Readonly<Record<string, TerraformTypeEncoding>>;
  readonly blocks: Readonly<Record<string, RuntimeProjectionBlock>>;
  readonly knownMembers: readonly string[];
  readonly silentlyIgnoredAttributes: readonly string[];
  readonly strictFrozenCompatibility: boolean;
}

interface RuntimeTransformResource {
  readonly type: string;
  readonly projection: RuntimeProjection;
  readonly override: Readonly<JsonObject>;
  readonly htmlUnescapePasses: 0 | 2;
  readonly strictFrozenCompatibility: boolean;
}

// Unicode 15.1 Decimal_Number zero code points, matching the Python 3.13
// authoring oracle. Every Nd block is one contiguous run of ten values.
const PYTHON_DECIMAL_ZEROS = [
  0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6,
  0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0,
  0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80,
  0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900,
  0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x11066,
  0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650, 0x116c0,
  0x11730, 0x118e0, 0x11950, 0x11c50, 0x11d50, 0x11da0, 0x11f50, 0x16a60,
  0x16ac0, 0x16b50, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6,
  0x1e140, 0x1e2f0, 0x1e4f0, 0x1e950, 0x1fbf0,
] as const;

export interface PullTransformResult {
  readonly items: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly originals: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly drops: readonly string[];
}

function hasOwn(record: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function isPlainJsonRecord(value: unknown): value is TransformRecord {
  if (!isJsonRecord(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return false;
  }
  if (Object.getOwnPropertySymbols(value).length !== 0) {
    return false;
  }
  return Object.values(Object.getOwnPropertyDescriptors(value)).every(
    (descriptor) => {
      return descriptor.enumerable === true
        && hasOwn(descriptor, "value")
        && descriptor.get === undefined
        && descriptor.set === undefined;
    },
  );
}

function safeRecord(entries: Iterable<readonly [string, unknown]>): TransformRecord {
  const output: TransformRecord = Object.create(null) as TransformRecord;
  for (const [key, value] of entries) {
    Object.defineProperty(output, key, {
      configurable: true,
      enumerable: true,
      value,
      writable: true,
    });
  }
  return output;
}

function cloneJson(value: unknown): unknown {
  if (
    value === null
    || typeof value === "boolean"
    || typeof value === "string"
  ) {
    return value;
  }
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token === null) {
      throw new TypeError(
        "transform accepts only finite losslessly parsed JSON numbers",
      );
    }
    // LosslessNumber instances are mutable objects. Copy even an already
    // canonical token so caller-owned raw input cannot mutate the result.
    return new LosslessNumber(token);
  }
  if (typeof value === "number") {
    throw new TypeError(
      "raw transform numeric tokens must be LosslessNumber values parsed from JSON",
    );
  }
  if (Array.isArray(value)) {
    return value.map((item) => cloneJson(item));
  }
  if (isPlainJsonRecord(value)) {
    return safeRecord(
      Object.keys(value).map((key) => [key, cloneJson(value[key])] as const),
    );
  }
  throw new TypeError("transform input must contain JSON values only");
}

export function snakeName(name: string): string {
  const half = name.replace(/([^\n])([A-Z][a-z]+)/gu, "$1_$2");
  return pythonLower151(
    half.replace(/([a-z0-9])([A-Z])/gu, "$1_$2"),
  );
}

function snakeKeys(
  value: unknown,
  path = "$raw",
  strictCollisions = true,
): unknown {
  if (Array.isArray(value)) {
    return value.map((item, index) => {
      return snakeKeys(item, `${path}[${index}]`, strictCollisions);
    });
  }
  if (isPlainJsonRecord(value)) {
    const output = safeRecord([]);
    const originalKeys = new Map<string, string>();
    for (const key of Object.keys(value)) {
      const normalized = snakeName(key);
      const previous = originalKeys.get(normalized);
      if (strictCollisions && previous !== undefined) {
        throw new TypeError(
          `snake_case key collision at ${path}: ${JSON.stringify(previous)} and ${JSON.stringify(key)} both map to ${JSON.stringify(normalized)}`,
        );
      }
      originalKeys.set(normalized, key);
      Object.defineProperty(output, normalized, {
        configurable: true,
        enumerable: true,
        value: snakeKeys(value[key], `${path}.${key}`, strictCollisions),
        writable: true,
      });
    }
    return output;
  }
  return cloneJson(value);
}

/** Recursively snake-case a losslessly parsed JSON value using Python rules. */
export function snakeJsonKeys(value: unknown): unknown {
  return snakeKeys(value, "$raw", false);
}

export function slugifyTransformKey(value: string): string {
  return pythonLower151(value)
    .replace(/[^a-z0-9]+/gu, "_")
    .replace(/^_+|_+$/gu, "");
}

function losslessIntegerToken(value: unknown): string | null {
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    return /^-?[0-9]+$/.test(token) ? token : null;
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) {
    return String(value);
  }
  return null;
}

function stringArray(
  value: unknown,
  label: string,
): readonly string[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
    throw new TypeError(`${label} must be a list of strings`);
  }
  return value;
}

function stringMap(value: unknown, label: string): Readonly<Record<string, string>> {
  if (value === undefined || value === null) return Object.freeze({});
  if (!isObject(value)) throw new TypeError(`${label} must be an object`);
  for (const [key, item] of Object.entries(value)) {
    if (typeof item !== "string") {
      throw new TypeError(`${label}.${key} must be a string`);
    }
  }
  return value as Readonly<Record<string, string>>;
}

function keyFields(resource: RuntimeTransformResource): readonly string[] {
  const field = resource.override.key_field;
  if (field === undefined || field === null) return ["name"];
  if (typeof field === "string") return [field];
  return stringArray(field, `${resource.type}.override.key_field`);
}

function identityComponent(
  value: unknown,
  field: string,
  strict: boolean,
): string {
  if (strict) {
    if (typeof value === "string") {
      if (value.trim() === "") {
        throw new TypeError(
          `key field ${JSON.stringify(field)} must not be blank`,
        );
      }
      return value;
    }
    if (value instanceof LosslessNumber) {
      const integer = losslessIntegerToken(value);
      if (integer !== null) return BigInt(integer).toString(10);
    }
    throw new TypeError(
      `key field ${JSON.stringify(field)} must be a nonblank string or integral LosslessNumber`,
    );
  }
  if (typeof value === "string") return value;
  if (typeof value === "boolean") return value ? "True" : "False";
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token !== null) return token;
  }
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (value === null) return "None";
  return String(value);
}

function deriveKey(item: TransformRecord, resource: RuntimeTransformResource): string {
  const fields = keyFields(resource);
  const parts = fields.map((field) => {
    if (!hasOwn(item, field)) {
      throw new Error(
        `key field ${JSON.stringify(field)} missing from item; set key_field in the override map`,
      );
    }
    return identityComponent(
      item[field],
      field,
      resource.strictFrozenCompatibility,
    );
  });
  let key = slugifyTransformKey(parts.join(" "));
  if (key !== "") {
    return key;
  }
  if (!hasOwn(item, "id")) {
    throw new Error(
      `derived key is empty and item has no 'id' to fall back on`,
    );
  }
  const fallback = slugifyTransformKey(identityComponent(
    item.id,
    "id",
    resource.strictFrozenCompatibility,
  ));
  if (resource.strictFrozenCompatibility && fallback === "") {
    throw new TypeError(
      "fallback key field \"id\" must contain at least one ASCII letter or digit",
    );
  }
  key = `id_${fallback}`;
  return key;
}

function unescapeDisplayFields(
  item: TransformRecord,
  resource: RuntimeTransformResource,
  htmlUnescape: ((value: string) => string) | undefined,
): void {
  if (resource.htmlUnescapePasses === 0) return;
  if (htmlUnescape === undefined) {
    throw new TypeError(
      `${resource.type} requires Python-compatible HTML unescape metadata`,
    );
  }
  for (const field of ["name", "description"] as const) {
    const value = item[field];
    if (typeof value === "string") {
      item[field] = htmlUnescape(htmlUnescape(value));
    }
  }
}

function coerceBoolean(value: unknown): unknown {
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "string") {
    const lower = value.toLowerCase();
    if (lower === "true" || lower === "1") {
      return true;
    }
    if (lower === "false" || lower === "0") {
      return false;
    }
    return value;
  }
  const integer = losslessIntegerToken(value);
  if (integer !== null) {
    return BigInt(integer) !== 0n;
  }
  return value;
}

function pythonDecimalDigit(codePoint: number): number | null {
  let low = 0;
  let high = PYTHON_DECIMAL_ZEROS.length - 1;
  let zero = -1;
  while (low <= high) {
    const middle = (low + high) >>> 1;
    const candidate = PYTHON_DECIMAL_ZEROS[middle] ?? 0;
    if (candidate <= codePoint) {
      zero = candidate;
      low = middle + 1;
    } else {
      high = middle - 1;
    }
  }
  const value = codePoint - zero;
  return zero >= 0 && value >= 0 && value <= 9 ? value : null;
}

function normalizePythonDecimalDigits(value: string): string {
  let output = "";
  for (const character of value) {
    const digit = pythonDecimalDigit(character.codePointAt(0) ?? -1);
    output += digit === null ? character : String(digit);
  }
  return output;
}

function isPythonNumericWhitespace(codePoint: number): boolean {
  return (codePoint >= 0x09 && codePoint <= 0x0d)
    || codePoint === 0x20
    || codePoint === 0x85
    || codePoint === 0xa0
    || codePoint === 0x1680
    || (codePoint >= 0x2000 && codePoint <= 0x200a)
    || codePoint === 0x2028
    || codePoint === 0x2029
    || codePoint === 0x202f
    || codePoint === 0x205f
    || codePoint === 0x3000;
}

function trimPythonNumericWhitespace(value: string): string {
  let start = 0;
  let end = value.length;
  while (start < end) {
    const codePoint = value.codePointAt(start) ?? -1;
    if (!isPythonNumericWhitespace(codePoint)) {
      break;
    }
    start += codePoint > 0xffff ? 2 : 1;
  }
  while (end > start) {
    const last = value.charCodeAt(end - 1);
    const width = last >= 0xdc00 && last <= 0xdfff ? 2 : 1;
    const codePoint = value.codePointAt(end - width) ?? -1;
    if (!isPythonNumericWhitespace(codePoint)) {
      break;
    }
    end -= width;
  }
  return value.slice(start, end);
}

function parsePythonInteger(value: string): number | LosslessNumber | null {
  const stripped = normalizePythonDecimalDigits(trimPythonNumericWhitespace(value));
  if (!/^[+-]?[0-9](?:_?[0-9])*$/.test(stripped)) {
    return null;
  }
  const integer = BigInt(stripped.replaceAll("_", ""));
  if (
    integer >= BigInt(Number.MIN_SAFE_INTEGER)
    && integer <= BigInt(Number.MAX_SAFE_INTEGER)
  ) {
    return Number(integer);
  }
  return new LosslessNumber(integer.toString(10));
}

function normalizedPythonFloatString(value: string): string | null {
  const stripped = normalizePythonDecimalDigits(trimPythonNumericWhitespace(value));
  if (!/^[+-]?(?:(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?|inf(?:inity)?|nan)$/i.test(stripped)) {
    return null;
  }
  return stripped.replaceAll("_", "");
}

function coercePrimitive(value: unknown, encoding: "bool" | "number" | "string"): unknown {
  if (encoding === "string") {
    if (typeof value === "boolean") {
      return value ? "true" : "false";
    }
    if (value instanceof LosslessNumber) {
      const token = canonicalPythonNumberToken(value.toString());
      if (token === null) {
        throw new TypeError("transform string coercion requires a finite JSON number");
      }
      return token;
    }
    if (typeof value === "number") {
      if (!Number.isFinite(value)) {
        throw new TypeError("transform string coercion requires a finite number");
      }
      if (Number.isSafeInteger(value)) return String(value);
      const token = pythonFiniteFloatToken(value);
      if (token === null) {
        throw new TypeError("transform string coercion requires a finite number");
      }
      return token;
    }
    return value;
  }
  if (encoding === "number") {
    if (typeof value !== "string") {
      return value;
    }
    const integer = parsePythonInteger(value);
    if (integer !== null) {
      return integer;
    }
    const floatText = normalizedPythonFloatString(value);
    if (floatText !== null) {
      const token = pythonFiniteFloatToken(
        Number(floatText),
      );
      if (token === null) {
        throw new TypeError(
          "transform numeric coercion accepts finite numbers only",
        );
      }
      return new LosslessNumber(token);
    }
    return value;
  }
  return coerceBoolean(value);
}

function unwrapReference(value: unknown): unknown {
  return isPlainJsonRecord(value) && hasOwn(value, "id") ? value.id : value;
}

function pythonSetSortKey(value: unknown): string {
  if (value === null) return "";
  if (typeof value === "string") return value;
  if (typeof value === "boolean") return value ? "True" : "False";
  if (value instanceof LosslessNumber) {
    return canonicalPythonNumberToken(value.toString()) ?? value.toString();
  }
  if (typeof value === "number") return String(value);
  if (Array.isArray(value)) {
    return `[${value.map((item) => pythonSetSortKey(item)).join(", ")}]`;
  }
  if (isPlainJsonRecord(value)) {
    return `{${Object.keys(value).map((key) => {
      return `${JSON.stringify(key)}: ${pythonSetSortKey(value[key])}`;
    }).join(", ")}}`;
  }
  return String(value);
}

function coerceObjectMembers(
  value: unknown,
  members: Readonly<Record<string, TerraformTypeEncoding>>,
  strictFrozenCompatibility: boolean,
): unknown {
  if (!isPlainJsonRecord(value)) return value;
  return safeRecord(sortedStrings(Object.keys(value)).flatMap((key) => {
    const member = members[key];
    return member === undefined
      ? []
      : [[key, coerceValue(value[key], member, strictFrozenCompatibility)] as const];
  }));
}

function coerceValue(
  value: unknown,
  encoding: TerraformTypeEncoding,
  strictFrozenCompatibility = false,
): unknown {
  if (typeof encoding === "string") {
    return coercePrimitive(unwrapReference(value), encoding);
  }
  const kind = encoding[0];
  const inner = encoding[1];
  if (kind === "object") {
    return coerceObjectMembers(
      value,
      inner as Readonly<Record<string, TerraformTypeEncoding>>,
      strictFrozenCompatibility,
    );
  }
  const childEncoding = inner as TerraformTypeEncoding;
  if (kind === "map") {
    if (!isPlainJsonRecord(value)) {
      return value;
    }
    return safeRecord(sortedStrings(Object.keys(value)).map((key) => {
      return [
        key,
        coerceValue(value[key], childEncoding, strictFrozenCompatibility),
      ] as const;
    }));
  }
  if (value === "") {
    return [];
  }
  let output: unknown[];
  if (Array.isArray(value)) {
    output = value.map((item) => {
      return coerceValue(item, childEncoding, strictFrozenCompatibility);
    });
  } else if (value === null) {
    return value;
  } else {
    output = [coerceValue(value, childEncoding, strictFrozenCompatibility)];
  }
  if (kind === "set") {
    if (
      strictFrozenCompatibility
      && childEncoding === "string"
      && output.some((item) => item !== null && typeof item !== "string")
    ) {
      throw new TypeError(
        "set(string) coercion produced a non-string provider value",
      );
    }
    return output
      .map((item, index) => {
        return {
          index,
          item,
          key: pythonSetSortKey(item),
        };
      })
      .sort((left, right) => {
        return comparePythonStrings(left.key, right.key) || left.index - right.index;
      })
      .map(({ item }) => item);
  }
  return output;
}

function nullStubValue(value: unknown): boolean {
  if (typeof value === "boolean") {
    return false;
  }
  if (value === null || value === "" || value === "0") {
    return true;
  }
  if (Array.isArray(value) && value.length === 0) {
    return true;
  }
  const integer = losslessIntegerToken(value);
  if (integer !== null) {
    return BigInt(integer) === 0n;
  }
  return value instanceof LosslessNumber
    && Number(value.toString()) === 0;
}

function isNullObject(
  value: unknown,
  projection: RuntimeProjection,
  path: string,
  acknowledgedDrops: ReadonlySet<string>,
): boolean {
  if (!isPlainJsonRecord(value) || Object.keys(value).length === 0) {
    return false;
  }
  const keys = Object.keys(value);
  if (!hasOwn(value, "id") && !keys.every((key) => key.endsWith("id"))) {
    return false;
  }
  const known = new Set(projection.knownMembers);
  for (const key of keys) {
    const currentPath = childPath(path, key);
    // `id` is the provider's universal stub discriminator even when an
    // individual nested schema omits it. Other id-suffixed keys still need
    // schema or acknowledgement evidence.
    const identityKey = key === "id";
    if (
      !known.has(key)
      && !identityKey
      && !acknowledgedDrops.has(currentPath)
    ) {
      return false;
    }
  }
  return keys.every((key) => nullStubValue(value[key]));
}

function childPath(path: string, key: string): string {
  return path === "" ? key : `${path}.${key}`;
}

function mergeSingleBlockElements(
  elements: readonly TransformRecord[],
  projection: RuntimeProjection,
  path: string,
  drops: string[],
  acknowledgedDrops: ReadonlySet<string>,
): TransformRecord {
  const entries = new Map<string, unknown>();
  const known = new Set(projection.knownMembers);
  for (const element of elements) {
    for (const key of sortedStrings(Object.keys(element))) {
      const value = element[key];
      if (value === null) {
        const memberPath = childPath(path, key);
        const identityKey = key === "id";
        if (
          !known.has(key)
          && !identityKey
          && !acknowledgedDrops.has(memberPath)
          && !drops.includes(memberPath)
        ) {
          drops.push(memberPath);
        }
        continue;
      }
      const encoding = projection.attributes[key];
      if (
        Array.isArray(encoding)
        && (encoding[0] === "list" || encoding[0] === "set")
      ) {
        const current = entries.get(key);
        const bucket = Array.isArray(current) ? current : [];
        if (value !== "") {
          bucket.push(...(Array.isArray(value) ? value : [value]));
        }
        entries.set(key, bucket);
      } else if (!entries.has(key)) {
        entries.set(key, value);
      } else if (
        encoding !== undefined
        && !pythonJsonEqual(entries.get(key), value)
      ) {
        drops.push(
          `${path}.${key} (conflicting values across merged elements; kept first)`,
        );
      }
    }
  }
  return safeRecord(entries);
}

function filterItem(
  item: TransformRecord,
  projection: RuntimeProjection,
  path: string,
  drops: string[],
  acknowledgedDrops: ReadonlySet<string>,
  overrideDrops: ReadonlySet<string>,
  overrideDropDefaults: Readonly<Record<string, unknown>>,
): TransformRecord {
  const output: Array<readonly [string, unknown]> = [];
  const ignored = new Set(projection.silentlyIgnoredAttributes);
  for (const key of sortedStrings(Object.keys(item))) {
    const value = item[key];
    const currentPath = childPath(path, key);
    const encoding = projection.attributes[key];
    if (encoding !== undefined) {
      const dotted = currentPath.replaceAll("[]", "");
      if (overrideDrops.has(dotted)) continue;
      if (
        hasOwn(overrideDropDefaults, dotted)
        && matchesTransformDefault(value, overrideDropDefaults[dotted])
      ) {
        continue;
      }
      output.push([key, value]);
      continue;
    }
    const block = projection.blocks[key];
    if (block !== undefined) {
      if (block.cardinality === "single") {
        let single = value;
        if (Array.isArray(single)) {
          if (single.length === 0) {
            continue;
          }
          const elements: TransformRecord[] = [];
          for (const [index, entry] of single.entries()) {
            if (!isPlainJsonRecord(entry)) {
              if (projection.strictFrozenCompatibility) {
                throw new TypeError(
                  `block ${currentPath}[${index}] must be a JSON object`,
                );
              }
              continue;
            }
            elements.push(entry);
          }
          if (elements.length === 0) {
            drops.push(currentPath);
            continue;
          }
          single = elements.length === 1
            ? elements[0]
            : mergeSingleBlockElements(
              elements,
              block.projection,
              currentPath,
              drops,
              acknowledgedDrops,
            );
        }
        if (isPlainJsonRecord(single)) {
          if (!isNullObject(
            single,
            block.projection,
            currentPath,
            acknowledgedDrops,
          )) {
            output.push([
              key,
              filterItem(
                single,
                block.projection,
                currentPath,
                drops,
                acknowledgedDrops,
                overrideDrops,
                overrideDropDefaults,
              ),
            ]);
          }
        } else {
          drops.push(currentPath);
        }
        continue;
      }

      const manyPath = `${currentPath}[]`;
      if (Array.isArray(value)) {
        const elements: TransformRecord[] = [];
        for (const [index, entry] of value.entries()) {
          if (!isPlainJsonRecord(entry)) {
            if (projection.strictFrozenCompatibility) {
              throw new TypeError(
                `block ${currentPath}[${index}] must be a JSON object`,
              );
            }
            continue;
          }
          if (!isNullObject(
            entry,
            block.projection,
            manyPath,
            acknowledgedDrops,
          )) {
            elements.push(entry);
          }
        }
        const shaped = block.merge && elements.length > 1
          ? [mergeSingleBlockElements(
            elements,
            block.projection,
            manyPath,
            drops,
            acknowledgedDrops,
          )]
          : elements;
        output.push([
          key,
          shaped.map((entry) => {
            return filterItem(
              entry,
              block.projection,
              manyPath,
              drops,
              acknowledgedDrops,
              overrideDrops,
              overrideDropDefaults,
            );
          }),
        ]);
      } else if (isPlainJsonRecord(value)) {
        output.push([
          key,
          isNullObject(
            value,
            block.projection,
            manyPath,
            acknowledgedDrops,
          )
            ? []
            : [filterItem(
              value,
              block.projection,
              manyPath,
              drops,
              acknowledgedDrops,
              overrideDrops,
              overrideDropDefaults,
            )],
        ]);
      } else {
        drops.push(currentPath);
      }
      continue;
    }
    if (!ignored.has(key)) {
      drops.push(currentPath);
    }
  }
  return safeRecord(output);
}

function coerceItem(
  item: TransformRecord,
  projection: RuntimeProjection,
): TransformRecord {
  const output: Array<readonly [string, unknown]> = [];
  for (const key of sortedStrings(Object.keys(item))) {
    const value = item[key];
    const block = projection.blocks[key];
    if (block !== undefined) {
      if (block.cardinality === "single") {
        output.push([
          key,
          isPlainJsonRecord(value)
            ? coerceItem(value, block.projection)
            : value,
        ]);
      } else {
        output.push([
          key,
          Array.isArray(value)
            ? value.map((entry) => {
              return isPlainJsonRecord(entry)
                ? coerceItem(entry, block.projection)
                : entry;
            })
            : value,
        ]);
      }
      continue;
    }
    const encoding = projection.attributes[key];
    output.push([
      key,
      encoding === undefined
        ? value
        : coerceValue(value, encoding, projection.strictFrozenCompatibility),
    ]);
  }
  return safeRecord(output);
}

function objectMap(value: unknown, label: string): Readonly<JsonObject> {
  if (value === undefined || value === null) return Object.freeze({});
  if (!isObject(value)) throw new TypeError(`${label} must be an object`);
  return value;
}

function integerValue(value: unknown): bigint | null {
  const token = losslessIntegerToken(value);
  if (token !== null) return BigInt(token);
  return null;
}

export function matchesTransformDefault(value: unknown, defaultValue: unknown): boolean {
  const defaultInteger = integerValue(defaultValue);
  let comparable = value;
  if (defaultInteger !== null && typeof value === "string") {
    const parsed = parsePythonInteger(value);
    if (parsed !== null) comparable = parsed;
  }
  return pythonJsonEqual(comparable, defaultValue);
}

function divideInteger(value: bigint, divisor: bigint): bigint {
  const quotient = value / divisor;
  const remainder = value % divisor;
  return remainder !== 0n && ((value < 0n) !== (divisor < 0n))
    ? quotient - 1n
    : quotient;
}

function dividedValue(value: unknown, divisorValue: unknown, label: string): unknown {
  const divisor = integerValue(divisorValue);
  if (divisor === null || divisor === 0n) {
    throw new TypeError(`${label} must be a non-zero integer`);
  }
  let candidate = value;
  if (typeof candidate === "string") {
    const parsed = parsePythonInteger(candidate);
    if (parsed === null) return value;
    candidate = parsed;
  }
  if (typeof candidate === "boolean") return value;
  const integer = integerValue(candidate);
  if (integer === null) return value;
  const divided = divideInteger(integer, divisor);
  return divided >= BigInt(Number.MIN_SAFE_INTEGER)
    && divided <= BigInt(Number.MAX_SAFE_INTEGER)
    ? Number(divided)
    : new LosslessNumber(divided.toString(10));
}

function goHtmlEscape(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("'", "&#39;")
    .replaceAll("\"", "&#34;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeHtmlFields(
  item: TransformRecord,
  resource: RuntimeTransformResource,
  htmlUnescape: ((value: string) => string) | undefined,
): void {
  for (const field of stringArray(
    resource.override.html_escape_fields,
    `${resource.type}.override.html_escape_fields`,
  )) {
    const value = item[field];
    if (typeof value !== "string") continue;
    if (htmlUnescape === undefined) {
      throw new TypeError(
        `${resource.type} HTML escaping requires a Python-compatible HTML decoder`,
      );
    }
    const unescaped = htmlUnescape(htmlUnescape(value));
    item[field] = goHtmlEscape(unescaped);
  }
}

function applyReachableOverrides(
  item: TransformRecord,
  resource: RuntimeTransformResource,
): TransformRecord {
  const output = safeRecord(Object.keys(item).map((key) => [key, item[key]] as const));
  const override = resource.override;
  const renames = stringMap(override.renames, `${resource.type}.override.renames`);
  for (const oldName of sortedStrings(Object.keys(renames))) {
    if (hasOwn(output, oldName)) {
      const newName = renames[oldName];
      if (newName === undefined) {
        throw new TypeError(`rename for ${oldName} is missing`);
      }
      const value = output[oldName];
      if (
        resource.strictFrozenCompatibility
        && oldName !== newName
        && hasOwn(output, newName)
      ) {
        throw new TypeError(
          `rename destination collision: ${JSON.stringify(oldName)} cannot overwrite existing ${JSON.stringify(newName)}`,
        );
      }
      delete output[oldName];
      Object.defineProperty(output, newName, {
        configurable: true,
        enumerable: true,
        value,
        writable: true,
      });
    }
  }
  for (const field of sortedStrings(stringArray(
    override.split_csv,
    `${resource.type}.override.split_csv`,
  ))) {
    const value = output[field];
    if (typeof value === "string") {
      output[field] = value
        .split(",")
        .map((part) => part.trim())
        .filter((part) => part !== "");
    }
  }
  for (const field of sortedStrings(stringArray(
    override.sort_lists,
    `${resource.type}.override.sort_lists`,
  ))) {
    const value = output[field];
    if (
      Array.isArray(value)
      && value.every((item) => typeof item === "string")
    ) {
      output[field] = [...value].sort(comparePythonStrings);
    }
  }
  for (const field of sortedStrings(stringArray(
    override.drops,
    `${resource.type}.override.drops`,
  ))) {
    delete output[field];
  }
  const references = objectMap(
    override.references,
    `${resource.type}.override.references`,
  );
  for (const field of sortedStrings(Object.keys(references))) {
    if (!hasOwn(output, field)) continue;
    const value = output[field];
    output[field] = Array.isArray(value)
      ? value.map((item) => unwrapReference(item))
      : unwrapReference(value);
  }
  const divide = objectMap(override.divide, `${resource.type}.override.divide`);
  for (const field of sortedStrings(Object.keys(divide))) {
    if (hasOwn(output, field)) {
      output[field] = dividedValue(
        output[field],
        divide[field],
        `${resource.type}.override.divide.${field}`,
      );
    }
  }
  for (const field of sortedStrings(stringArray(
    override.invert_bool,
    `${resource.type}.override.invert_bool`,
  ))) {
    if (hasOwn(output, field)) {
      const coerced = coerceBoolean(output[field]);
      if (typeof coerced === "boolean") {
        output[field] = !coerced;
      }
    }
  }
  const valueMap = objectMap(override.value_map, `${resource.type}.override.value_map`);
  for (const field of sortedStrings(Object.keys(valueMap))) {
    const mapping = objectMap(valueMap[field], `${resource.type}.override.value_map.${field}`);
    const value = output[field];
    if (typeof value === "string" && hasOwn(mapping, value)) {
      output[field] = cloneJson(mapping[value]);
    }
  }
  const stripPrefix = stringMap(
    override.strip_prefix,
    `${resource.type}.override.strip_prefix`,
  );
  for (const field of sortedStrings(Object.keys(stripPrefix))) {
    const prefix = stripPrefix[field] ?? "";
    const value = output[field];
    if (typeof value === "string" && value.startsWith(prefix)) {
      output[field] = value.slice(prefix.length);
    } else if (Array.isArray(value)) {
      output[field] = value.map((item) => {
        return typeof item === "string" && item.startsWith(prefix)
          ? item.slice(prefix.length)
          : item;
      });
    }
  }
  const defaults = objectMap(override.defaults, `${resource.type}.override.defaults`);
  for (const field of sortedStrings(Object.keys(defaults))) {
    const value = output[field];
    if (!hasOwn(output, field) || value === null || value === "" || (Array.isArray(value) && value.length === 0)) {
      output[field] = cloneJson(defaults[field]);
    }
  }
  const dropDefaults = objectMap(
    override.drop_if_default,
    `${resource.type}.override.drop_if_default`,
  );
  for (const field of sortedStrings(Object.keys(dropDefaults))) {
    if (hasOwn(output, field) && matchesTransformDefault(output[field], dropDefaults[field])) {
      delete output[field];
    }
  }
  return output;
}

function skipMatchers(value: unknown, label: string): readonly JsonObject[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new TypeError(`${label} must be a list`);
  return value.map((matcher, index) => {
    if (!isObject(matcher) || Object.keys(matcher).length === 0) {
      throw new TypeError(`${label}[${index}] must be a non-empty object`);
    }
    return matcher;
  });
}

type LteNumber =
  | { readonly integer: bigint; readonly kind: "integer" }
  | { readonly float: number; readonly kind: "float" };

function lteNumber(value: unknown): LteNumber | null {
  if (value === null || typeof value === "boolean") return null;
  if (value instanceof LosslessNumber) {
    const token = losslessIntegerToken(value);
    if (token !== null) return { integer: BigInt(token), kind: "integer" };
    const float = Number(value.toString());
    return Number.isFinite(float) ? { float, kind: "float" } : null;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) return null;
    return Number.isSafeInteger(value)
      ? { integer: BigInt(value), kind: "integer" }
      : { float: value, kind: "float" };
  }
  if (typeof value === "string") {
    const normalized = normalizedPythonFloatString(value);
    if (normalized === null) return null;
    const float = Number(normalized);
    return Number.isFinite(float) ? { float, kind: "float" } : null;
  }
  return null;
}

function numberIsLte(value: LteNumber, threshold: LteNumber): boolean {
  if (value.kind === "integer" && threshold.kind === "integer") {
    return value.integer <= threshold.integer;
  }
  if (value.kind === "float" && threshold.kind === "float") {
    return value.float <= threshold.float;
  }
  if (value.kind === "integer" && threshold.kind === "float") {
    return value.integer <= BigInt(Math.floor(threshold.float));
  }
  return BigInt(Math.ceil((value as { readonly float: number }).float))
    <= (threshold as { readonly integer: bigint }).integer;
}

function skipMatchReason(
  item: TransformRecord,
  resource: RuntimeTransformResource,
): "skip_if" | "skip_if_lte" | null {
  return transformSkipMatchReason(item, resource.override, resource.type);
}

/** Evaluate the transform/adoption skip vocabulary against a snake-cased item. */
export function transformSkipMatchReason(
  item: Readonly<Record<string, unknown>>,
  metadata: Readonly<JsonObject>,
  label: string,
): "skip_if" | "skip_if_lte" | null {
  for (const matcher of skipMatchers(
    metadata.skip_if,
    `${label}.skip_if`,
  )) {
    if (Object.entries(matcher).every(([field, expected]) => {
      return pythonJsonEqual(item[snakeName(field)], expected);
    })) {
      return "skip_if";
    }
  }
  for (const matcher of skipMatchers(
    metadata.skip_if_lte,
    `${label}.skip_if_lte`,
  )) {
    if (Object.entries(matcher).every(([field, thresholdValue]) => {
      const threshold = lteNumber(thresholdValue);
      if (threshold === null) {
        throw new TypeError(`skip_if_lte threshold for ${JSON.stringify(field)} must be numeric`);
      }
      const value = lteNumber(item[snakeName(field)]);
      return value !== null && numberIsLte(value, threshold);
    })) {
      return "skip_if_lte";
    }
  }
  return null;
}

/** Shape one raw API value through the ordinary loaded-resource schema kernel. */
export function projectLoadedRawField(options: {
  readonly rawValue: unknown;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
  readonly target: string;
}): unknown | undefined {
  const block = terraformBlockForSchema(
    options.schema as JsonObject,
    options.resourceType,
  );
  const projection = compileProjection(
    block,
    `${options.resourceType}.block`,
    { mergeBlocks: new Set(), topLevel: true },
  );
  const shaped = safeRecord([[
    options.target,
    snakeKeys(options.rawValue, `$raw.${options.target}`, false),
  ]]);
  const filtered = filterItem(
    shaped,
    projection,
    "",
    [],
    new Set(),
    new Set(),
    safeRecord([]),
  );
  const coerced = coerceItem(filtered, projection);
  return hasOwn(coerced, options.target) ? coerced[options.target] : undefined;
}

function runtimeProjectionFromCatalog(
  projection: TransformCatalogResource["projection"],
): RuntimeProjection {
  const blocks: Record<string, RuntimeProjectionBlock> = Object.create(null) as Record<
    string,
    RuntimeProjectionBlock
  >;
  for (const [name, block] of Object.entries(projection.blocks)) {
    blocks[name] = {
      cardinality: block.cardinality,
      merge: false,
      projection: runtimeProjectionFromCatalog(block.projection),
    };
  }
  return {
    attributes: projection.attributes as Readonly<Record<string, TerraformTypeEncoding>>,
    blocks,
    knownMembers: sortedStrings(new Set([
      ...Object.keys(projection.attributes),
      ...Object.keys(projection.blocks),
      ...projection.silently_ignored_attributes,
    ])),
    silentlyIgnoredAttributes: projection.silently_ignored_attributes,
    strictFrozenCompatibility: true,
  };
}

function runtimeResourceFromCatalog(
  resource: TransformCatalogResource,
): RuntimeTransformResource {
  const override: JsonObject = {
    acknowledged_drops: resource.acknowledged_drops,
    invert_bool: resource.invert_bool,
    key_field: resource.key_fields.length === 1
      ? resource.key_fields[0]
      : resource.key_fields,
    renames: resource.renames,
    skip_if: resource.skip_if ?? [],
    sort_lists: resource.sort_lists ?? [],
    split_csv: resource.split_csv,
  };
  return {
    type: resource.type,
    projection: runtimeProjectionFromCatalog(resource.projection),
    override,
    htmlUnescapePasses: resource.html_unescape_passes,
    strictFrozenCompatibility: true,
  };
}

function compileProjection(
  block: JsonObject,
  label: string,
  options: {
    readonly mergeBlocks: ReadonlySet<string>;
    readonly topLevel: boolean;
  },
): RuntimeProjection {
  const classified = options.topLevel
    ? terraformResourceInputAttributes(block, label)
    : terraformClassifyAttributes(block, label);
  const rawAttributes = terraformAttributesForBlock(block, label);
  const attributes: Record<string, TerraformTypeEncoding> = Object.create(null) as Record<
    string,
    TerraformTypeEncoding
  >;
  for (const name of [...classified.required, ...classified.optional]) {
    attributes[name] = terraformAttributeType(
      terraformRequireObject(rawAttributes[name], `${label}.attributes.${name}`),
      `${label}.attributes.${name}`,
    );
  }
  const blocks: Record<string, RuntimeProjectionBlock> = Object.create(null) as Record<
    string,
    RuntimeProjectionBlock
  >;
  for (const [name, blockType] of terraformInputBlockTypes(block, label)) {
    const childLabel = `${label}.block_types.${name}.block`;
    blocks[name] = {
      cardinality: terraformBlockIsSingle(blockType) ? "single" : "many",
      merge: options.topLevel && options.mergeBlocks.has(name),
      projection: compileProjection(
        terraformRequireObject(blockType.block, childLabel),
        childLabel,
        { mergeBlocks: new Set(), topLevel: false },
      ),
    };
  }
  const id = rawAttributes.id;
  const rawBlockTypes = terraformBlockTypesForBlock(block, label);
  const silentlyIgnoredAttributes = options.topLevel
    && id !== undefined
    && terraformRequireObject(id, `${label}.attributes.id`).computed === true
    ? ["id"]
    : [];
  return {
    attributes,
    blocks,
    knownMembers: sortedStrings(new Set([
      ...Object.keys(rawAttributes),
      ...Object.keys(rawBlockTypes),
    ])),
    silentlyIgnoredAttributes,
    strictFrozenCompatibility: false,
  };
}

function validateLoadedOverride(
  resourceType: string,
  override: Readonly<JsonObject>,
  block: JsonObject,
): void {
  const divide = objectMap(override.divide, `${resourceType}.override.divide`);
  for (const [field, divisor] of Object.entries(divide)) {
    if (integerValue(divisor) === 0n) {
      throw new TypeError(`divide divisor for ${JSON.stringify(field)} must be non-zero`);
    }
  }
  const renames = stringMap(override.renames, `${resourceType}.override.renames`);
  const topDrops = stringArray(override.drops, `${resourceType}.override.drops`)
    .filter((field) => !field.includes("."));
  const conflicts = sortedStrings(topDrops.filter((field) => hasOwn(renames, field)));
  if (conflicts.length > 0) {
    throw new TypeError(
      `drops uses pre-rename name(s) ${conflicts.join(", ")} — renames run first; drop the NEW name instead`,
    );
  }
  const dottedSorts = stringArray(
    override.sort_lists,
    `${resourceType}.override.sort_lists`,
  ).filter((field) => field.includes("."));
  if (dottedSorts.length > 0) {
    throw new TypeError(
      `sort_lists does not support nested (dotted) paths: ${dottedSorts.join(", ")}`,
    );
  }
  const dropDefaults = objectMap(
    override.drop_if_default,
    `${resourceType}.override.drop_if_default`,
  );
  const dotted = [
    ...stringArray(override.drops, `${resourceType}.override.drops`),
    ...Object.keys(dropDefaults),
  ].filter((field) => field.includes("."));
  for (const field of sortedStrings(dotted)) {
    const segments = field.split(".");
    let current = block;
    for (const segment of segments.slice(0, -1)) {
      const blockType = terraformInputBlockTypes(current, resourceType).get(segment);
      if (blockType === undefined) {
        throw new TypeError(
          `dotted path ${JSON.stringify(field)}: ${JSON.stringify(segment)} is not a nested block in the ${resourceType} schema`,
        );
      }
      current = terraformRequireObject(blockType.block, `${resourceType}.${field}.${segment}`);
    }
    const last = segments.at(-1) ?? "";
    if (!hasOwn(terraformAttributesForBlock(current, resourceType), last)) {
      throw new TypeError(
        `dotted path ${JSON.stringify(field)}: ${JSON.stringify(last)} is not an attribute of that block in the ${resourceType} schema`,
      );
    }
  }
}

function executeTransform(options: {
  readonly rawItems: readonly unknown[];
  readonly resource: RuntimeTransformResource;
  readonly htmlUnescape?: (value: string) => string;
  readonly onSkip?: (item: unknown, reason: "skip_if" | "skip_if_lte") => void;
}): PullTransformResult {
  const { resource } = options;
  const items = new Map<string, Readonly<Record<string, unknown>>>();
  const originals = new Map<string, Readonly<Record<string, unknown>>>();
  const drops: string[] = [];
  const acknowledged = new Set(stringArray(
    resource.override.acknowledged_drops,
    `${resource.type}.override.acknowledged_drops`,
  ));
  const nestedDrops = new Set(stringArray(
    resource.override.drops,
    `${resource.type}.override.drops`,
  ).filter((field) => field.includes(".")));
  const dropDefaults = objectMap(
    resource.override.drop_if_default,
    `${resource.type}.override.drop_if_default`,
  );
  const nestedDropDefaults = safeRecord(Object.entries(dropDefaults)
    .filter(([field]) => field.includes(".")));

  for (const raw of options.rawItems) {
    const snakeRaw = snakeKeys(raw, "$raw", resource.strictFrozenCompatibility);
    if (!isPlainJsonRecord(snakeRaw)) {
      throw new TypeError("each raw transform item must be a JSON object");
    }
    unescapeDisplayFields(snakeRaw, resource, options.htmlUnescape);
    const skipReason = skipMatchReason(snakeRaw, resource);
    if (skipReason !== null) {
      options.onSkip?.(snakeRaw, skipReason);
      continue;
    }
    const normalized = applyReachableOverrides(snakeRaw, resource);
    escapeHtmlFields(normalized, resource, options.htmlUnescape);
    const key = deriveKey(normalized, resource);
    if (items.has(key)) {
      throw new Error(
        `duplicate derived key ${JSON.stringify(key)} for ${resource.type}; set a different key_field in the override map`,
      );
    }
    const filtered = filterItem(
      normalized,
      resource.projection,
      "",
      drops,
      acknowledged,
      nestedDrops,
      nestedDropDefaults,
    );
    items.set(key, coerceItem(filtered, resource.projection));
    originals.set(key, normalized);
  }
  return {
    items: safeRecord(items) as PullTransformResult["items"],
    originals: safeRecord(originals) as PullTransformResult["originals"],
    drops: sortedStrings(new Set(drops.filter((drop) => !acknowledged.has(drop)))),
  };
}

function catalogResource(
  catalog: TransformCatalog,
  resourceType: string,
): TransformCatalogResource {
  const resource = catalog.resources.find((entry) => entry.type === resourceType);
  if (resource === undefined) {
    throw new Error(`resource type ${JSON.stringify(resourceType)} is not in the transform catalog`);
  }
  return resource;
}

/**
 * Pure detail-pull transform for the five catalogued ZCC resources.
 *
 * The ordering mirrors engine.transform.transform_items exactly for the
 * reachable override vocabulary captured by the closed transform catalog.
 * Raw data must come from a lossless JSON parser: every JSON number token is
 * required to be a LosslessNumber so `1` cannot be confused with `1.0` after
 * JavaScript conversion. Finite float lexemes are canonicalized through the
 * same binary64 model and spelling used by Python's JSON implementation.
 */
export function transformPullItems(options: {
  readonly catalog: TransformCatalog;
  readonly rawItems: readonly unknown[];
  readonly resourceType: string;
}): PullTransformResult {
  const catalog = requireSupportedZccTransformCatalog(options.catalog);
  const resource = catalogResource(catalog, options.resourceType);
  return transformPullItemsKernel({
    compatibility: catalog.python_compatibility,
    rawItems: options.rawItems,
    resource,
  });
}

/**
 * Product-neutral pure transform seam. Callers must supply a structurally
 * validated catalog resource; public operations retain the exact embedded-ZCC
 * gate in `transformPullItems`.
 *
 * @internal Catalog differential and future product-catalog integration only.
 */
export function transformPullItemsKernel(options: {
  readonly compatibility: TransformCatalog["python_compatibility"];
  readonly rawItems: readonly unknown[];
  readonly resource: TransformCatalogResource;
}): PullTransformResult {
  return executeTransform({
    rawItems: options.rawItems,
    resource: runtimeResourceFromCatalog(options.resource),
    htmlUnescape: (value: string) => {
      return pythonHtmlUnescape(value, options.compatibility.html_unescape);
    },
  });
}

export interface TransformLoadedItemsOptions {
  readonly resource: LoadedResourceMetadata;
  readonly schema: Readonly<JsonObject>;
  readonly rawItems: readonly unknown[];
  /** One Python-compatible html.unescape pass. The engine applies two. */
  readonly htmlUnescape?: (value: string) => string;
  /** True only when the owning pack lists this resource prefix for unescape. */
  readonly unescapeHtml?: boolean;
  readonly onSkip?: (item: unknown, reason: "skip_if" | "skip_if_lte") => void;
}

/** Transform already-collected items directly from active pack metadata. */
export function transformLoadedItems(
  options: TransformLoadedItemsOptions,
): PullTransformResult {
  const override: Readonly<JsonObject> = options.resource.override
    ?? Object.freeze({} as JsonObject);
  const block = terraformBlockForSchema(
    options.schema as JsonObject,
    options.resource.type,
  );
  validateLoadedOverride(options.resource.type, override, block);
  const mergeBlocks = new Set(stringArray(
    override.merge_blocks,
    `${options.resource.type}.override.merge_blocks`,
  ));
  const noHtmlUnescape = override.no_html_unescape === true;
  const resource: RuntimeTransformResource = {
    type: options.resource.type,
    override,
    projection: compileProjection(
      block,
      `${options.resource.type}.block`,
      { mergeBlocks, topLevel: true },
    ),
    htmlUnescapePasses: options.unescapeHtml === true && !noHtmlUnescape ? 2 : 0,
    strictFrozenCompatibility: false,
  };
  return executeTransform({
    rawItems: options.rawItems,
    resource,
    ...(options.htmlUnescape === undefined
      ? {}
      : { htmlUnescape: options.htmlUnescape }),
    ...(options.onSkip === undefined ? {} : { onSkip: options.onSkip }),
  });
}

function compareDerivedRules(
  left: Readonly<{ id: string; order: string }>,
  right: Readonly<{ id: string; order: string }>,
): number {
  const leftInteger = parsePythonInteger(left.order);
  const rightInteger = parsePythonInteger(right.order);
  const leftToken = leftInteger === null ? null : integerValue(leftInteger);
  const rightToken = rightInteger === null ? null : integerValue(rightInteger);
  if (leftToken !== null && rightToken !== null) {
    if (leftToken < rightToken) return -1;
    if (leftToken > rightToken) return 1;
  } else if (leftToken !== null) {
    return -1;
  } else if (rightToken !== null) {
    return 1;
  } else {
    const byOrder = comparePythonStrings(left.order, right.order);
    if (byOrder !== 0) return byOrder;
  }
  return comparePythonStrings(left.id, right.id);
}

/** Port of the registry-driven, config-only reorder derivation. */
export function deriveReorderItems(
  rawItems: readonly unknown[],
  derive: Readonly<JsonObject>,
): Readonly<Record<string, Readonly<Record<string, unknown>>>> {
  const source = derive.from;
  const policyType = derive.policy_type;
  if (typeof source !== "string" || source.length === 0) {
    throw new TypeError("derive.from must be a non-empty string");
  }
  if (typeof policyType !== "string" || policyType.length === 0) {
    throw new TypeError("derive.policy_type must be a non-empty string");
  }
  const rules: Array<{ id: string; order: string }> = [];
  for (const raw of rawItems) {
    const item = snakeKeys(raw, "$raw", false);
    if (!isPlainJsonRecord(item)) {
      throw new TypeError("each derived source item must be a JSON object");
    }
    const id = item.id;
    const order = item.rule_order;
    if (id === undefined || id === null || order === undefined || order === null) {
      const missing = id === undefined || id === null ? "id" : "rule_order";
      throw new Error(
        `cannot derive the reorder resource from ${source}: a source rule is missing ${missing} — refusing to emit a partial reorder`,
      );
    }
    rules.push({
      id: identityComponent(id, "id", false),
      order: identityComponent(order, "rule_order", false),
    });
  }
  rules.sort(compareDerivedRules);
  if (rules.length === 0) return Object.freeze({});
  return safeRecord([[
    policyType,
    safeRecord([
      ["policy_type", policyType],
      ["rules", rules.map((rule) => safeRecord([
        ["id", rule.id],
        ["order", rule.order],
      ]))],
    ]),
  ]]) as Readonly<Record<string, Readonly<Record<string, unknown>>>>;
}
