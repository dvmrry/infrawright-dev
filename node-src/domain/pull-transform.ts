import {
  LosslessNumber,
} from "lossless-json";

import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  pythonHtmlUnescapePasses,
} from "./python-html-unescape.js";
import type {
  TransformCatalog,
  TransformCatalogResource,
  TransformProjection,
  TransformPrimitiveEncoding,
  TransformValueEncoding,
} from "./transform-catalog.js";
import { requireSupportedZccTransformCatalog } from "./transform-catalog.js";

type TransformRecord = Record<string, unknown>;

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
    const token = value.toString();
    if (!/^-?[0-9]+$/.test(token)) {
      throw new TypeError(
        "transform accepts integral JSON numbers only; float compatibility is not yet frozen",
      );
    }
    const canonical = BigInt(token).toString(10);
    // LosslessNumber instances are mutable objects. Copy even an already
    // canonical token so caller-owned raw input cannot mutate the result.
    return new LosslessNumber(canonical);
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
  const half = name.replace(/(.)([A-Z][a-z]+)/g, "$1_$2");
  return half.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
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
  return value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "");
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
  catalog: TransformCatalog,
): void {
  for (const field of ["name", "description"] as const) {
    const value = item[field];
    if (typeof value === "string") {
      item[field] = pythonHtmlUnescapePasses(
        value,
        resource.html_unescape_passes,
        catalog.python_compatibility.html_unescape,
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

function parsePythonInteger(value: string): number | LosslessNumber | null {
  const stripped = value.trim();
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

function isPythonFloatString(value: string): boolean {
  const stripped = value.trim().replaceAll("_", "");
  if (!/^[+-]?(?:(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?|inf(?:inity)?|nan)$/i.test(stripped)) {
    return false;
  }
  return true;
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
      return identityComponent(value, "coercion input");
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
    if (/[^\x00-\x7f]/u.test(value) && /\p{Decimal_Number}/u.test(value)) {
      throw new TypeError(
        "transform numeric coercion does not yet support Unicode decimal strings",
      );
    }
    if (isPythonFloatString(value)) {
      throw new TypeError(
        "transform numeric coercion accepts integers only; float compatibility is not yet frozen",
      );
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
  const inner = encoding[1];
  if (value === "") {
    return [];
  }
  if (Array.isArray(value)) {
    return value.map((item) => coercePrimitive(unwrapReference(item), inner));
  }
  if (value === null) {
    return null;
  }
  return [coercePrimitive(unwrapReference(value), inner)];
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
  return integer !== null && BigInt(integer) === 0n;
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
      if (Array.isArray(encoding) && encoding[0] === "list") {
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
 * JavaScript conversion.  Only integral tokens are accepted at checkpoint 1.
 */
export function transformPullItems(options: {
  readonly catalog: TransformCatalog;
  readonly rawItems: readonly unknown[];
  readonly resourceType: string;
}): PullTransformResult {
  const catalog = requireSupportedZccTransformCatalog(options.catalog);
  const resource = catalogResource(catalog, options.resourceType);
  const items = new Map<string, Readonly<Record<string, unknown>>>();
  const originals = new Map<string, Readonly<Record<string, unknown>>>();
  const drops: string[] = [];
  const acknowledged = new Set(resource.acknowledged_drops);

  for (const raw of options.rawItems) {
    const snakeRaw = snakeKeys(raw);
    if (!isPlainJsonRecord(snakeRaw)) {
      throw new TypeError("each raw transform item must be a JSON object");
    }
    unescapeDisplayFields(snakeRaw, resource, catalog);
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
