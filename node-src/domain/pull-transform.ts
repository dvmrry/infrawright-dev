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
import {
  pythonHtmlUnescapePasses,
} from "./python-html-unescape.js";
import { pythonLower151 } from "../json/python-lower-151.js";
import type {
  TransformCatalog,
  TransformCatalogResource,
  TransformProjection,
  TransformPrimitiveEncoding,
  TransformValueEncoding,
} from "./transform-catalog.js";
import { requireSupportedZccTransformCatalog } from "./transform-catalog.js";

type TransformRecord = Record<string, unknown>;

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

function snakeKeys(value: unknown, path = "$raw"): unknown {
  if (Array.isArray(value)) {
    return value.map((item, index) => snakeKeys(item, `${path}[${index}]`));
  }
  if (isPlainJsonRecord(value)) {
    const output = safeRecord([]);
    const originalKeys = new Map<string, string>();
    for (const key of Object.keys(value)) {
      const normalized = snakeName(key);
      const previous = originalKeys.get(normalized);
      if (previous !== undefined) {
        throw new TypeError(
          `snake_case key collision at ${path}: ${JSON.stringify(previous)} and ${JSON.stringify(key)} both map to ${JSON.stringify(normalized)}`,
        );
      }
      originalKeys.set(normalized, key);
      Object.defineProperty(output, normalized, {
        configurable: true,
        enumerable: true,
        value: snakeKeys(value[key], `${path}.${key}`),
        writable: true,
      });
    }
    return output;
  }
  return cloneJson(value);
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

function identityComponent(value: unknown, field: string): string {
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
    if (integer !== null) {
      return BigInt(integer).toString(10);
    }
  }
  throw new TypeError(
    `key field ${JSON.stringify(field)} must be a nonblank string or integral LosslessNumber`,
  );
}

function deriveKey(item: TransformRecord, resource: TransformCatalogResource): string {
  const parts = resource.key_fields.map((field) => {
    if (!hasOwn(item, field)) {
      throw new Error(
        `key field ${JSON.stringify(field)} missing from item; set key_field in the override map`,
      );
    }
    return identityComponent(item[field], field);
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
  const fallback = slugifyTransformKey(identityComponent(item.id, "id"));
  if (fallback === "") {
    throw new TypeError(
      "fallback key field \"id\" must contain at least one ASCII letter or digit",
    );
  }
  key = `id_${fallback}`;
  return key;
}

function unescapeDisplayFields(
  item: TransformRecord,
  resource: TransformCatalogResource,
  compatibility: TransformCatalog["python_compatibility"],
): void {
  for (const field of ["name", "description"] as const) {
    const value = item[field];
    if (typeof value === "string") {
      item[field] = pythonHtmlUnescapePasses(
        value,
        resource.html_unescape_passes,
        compatibility.html_unescape,
      );
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

function coercePrimitive(
  value: unknown,
  encoding: TransformPrimitiveEncoding,
): unknown {
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

function coerceValue(value: unknown, encoding: TransformValueEncoding): unknown {
  if (typeof encoding === "string") {
    return coercePrimitive(unwrapReference(value), encoding);
  }
  const kind = encoding[0];
  const inner = encoding[1];
  if (kind === "map") {
    if (!isPlainJsonRecord(value)) {
      return value;
    }
    return safeRecord(sortedStrings(Object.keys(value)).map((key) => {
      return [key, coercePrimitive(unwrapReference(value[key]), inner)] as const;
    }));
  }
  if (value === "") {
    return [];
  }
  let output: unknown[];
  if (Array.isArray(value)) {
    output = value.map((item) => coercePrimitive(unwrapReference(item), inner));
  } else if (value === null) {
    return value;
  } else {
    output = [coercePrimitive(unwrapReference(value), inner)];
  }
  if (kind === "set") {
    return output
      .map((item, index) => {
        if (item !== null && typeof item !== "string") {
          throw new TypeError(
            "set(string) coercion produced a non-string provider value",
          );
        }
        return {
          index,
          item,
          key: item ?? "",
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
  projection: TransformProjection,
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
  const ignored = new Set(projection.silently_ignored_attributes);
  for (const key of keys) {
    const currentPath = childPath(path, key);
    // `id` is the provider's universal stub discriminator even when an
    // individual nested schema omits it. Other id-suffixed keys still need
    // schema or acknowledgement evidence.
    const identityKey = key === "id";
    if (
      projection.attributes[key] === undefined
      && projection.blocks[key] === undefined
      && !ignored.has(key)
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
  projection: TransformProjection,
  path: string,
  drops: string[],
  acknowledgedDrops: ReadonlySet<string>,
): TransformRecord {
  const entries = new Map<string, unknown>();
  const ignored = new Set(projection.silently_ignored_attributes);
  for (const element of elements) {
    for (const key of sortedStrings(Object.keys(element))) {
      const value = element[key];
      if (value === null) {
        const memberPath = childPath(path, key);
        const identityKey = key === "id";
        if (
          projection.attributes[key] === undefined
          && projection.blocks[key] === undefined
          && !ignored.has(key)
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
  projection: TransformProjection,
  path: string,
  drops: string[],
  acknowledgedDrops: ReadonlySet<string>,
): TransformRecord {
  const output: Array<readonly [string, unknown]> = [];
  const ignored = new Set(projection.silently_ignored_attributes);
  for (const key of sortedStrings(Object.keys(item))) {
    const value = item[key];
    const currentPath = childPath(path, key);
    const encoding = projection.attributes[key];
    if (encoding !== undefined) {
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
              throw new TypeError(
                `block ${currentPath}[${index}] must be a JSON object`,
              );
            }
            elements.push(entry);
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
            throw new TypeError(
              `block ${currentPath}[${index}] must be a JSON object`,
            );
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
        output.push([
          key,
          elements.map((entry) => {
            return filterItem(
              entry,
              block.projection,
              manyPath,
              drops,
              acknowledgedDrops,
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
  projection: TransformProjection,
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
    output.push([key, encoding === undefined ? value : coerceValue(value, encoding)]);
  }
  return safeRecord(output);
}

function applyReachableOverrides(
  item: TransformRecord,
  resource: TransformCatalogResource,
): TransformRecord {
  const output = safeRecord(Object.keys(item).map((key) => [key, item[key]] as const));
  for (const oldName of sortedStrings(Object.keys(resource.renames))) {
    if (hasOwn(output, oldName)) {
      const newName = resource.renames[oldName];
      if (newName === undefined) {
        throw new TypeError(`rename for ${oldName} is missing`);
      }
      const value = output[oldName];
      if (oldName !== newName && hasOwn(output, newName)) {
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
  for (const field of sortedStrings(resource.split_csv)) {
    const value = output[field];
    if (typeof value === "string") {
      output[field] = value
        .split(",")
        .map((part) => part.trim())
        .filter((part) => part !== "");
    }
  }
  for (const field of sortedStrings(resource.invert_bool)) {
    if (hasOwn(output, field)) {
      const coerced = coerceBoolean(output[field]);
      if (typeof coerced === "boolean") {
        output[field] = !coerced;
      }
    }
  }
  return output;
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
  const { resource } = options;
  const items = new Map<string, Readonly<Record<string, unknown>>>();
  const originals = new Map<string, Readonly<Record<string, unknown>>>();
  const drops: string[] = [];
  const acknowledged = new Set(resource.acknowledged_drops);

  for (const raw of options.rawItems) {
    const snakeRaw = snakeKeys(raw);
    if (!isPlainJsonRecord(snakeRaw)) {
      throw new TypeError("each raw transform item must be a JSON object");
    }
    unescapeDisplayFields(snakeRaw, resource, options.compatibility);
    const normalized = applyReachableOverrides(snakeRaw, resource);
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
    );
    items.set(key, coerceItem(filtered, resource.projection));
    originals.set(key, normalized);
  }

  const reportedDrops = sortedStrings(
    new Set(drops.filter((drop) => !acknowledged.has(drop))),
  );
  return {
    items: safeRecord(items) as PullTransformResult["items"],
    originals: safeRecord(originals) as PullTransformResult["originals"],
    drops: reportedDrops,
  };
}
