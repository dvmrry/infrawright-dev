import type { LoadedResourceMetadata } from "../metadata/loader.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { pythonJsonEqual } from "../json/python-equality.js";
import { formatImportTemplate, pythonTransformString } from "./transform-artifacts.js";
import {
  slugifyTransformKey,
  snakeJsonKeys,
  snakeName,
  transformSkipMatchReason,
} from "./pull-transform.js";

export interface AdoptionMetadata {
  readonly constantKey: string | null;
  readonly identityFields: Readonly<Record<string, string>>;
  readonly identityRenames: Readonly<Record<string, string>>;
  readonly importId: string;
  readonly keyField: string | readonly string[];
  readonly skipIf: readonly Readonly<JsonObject>[];
  readonly skipIfLte: readonly Readonly<JsonObject>[];
}

export interface AdoptionIdentity {
  readonly importId: string;
  readonly item: Readonly<Record<string, unknown>>;
  readonly key: string;
  readonly raw: Readonly<Record<string, unknown>>;
}

export interface AdoptionIdentityResult {
  readonly identities: readonly AdoptionIdentity[];
  readonly skipped: readonly {
    readonly item: Readonly<Record<string, unknown>>;
    readonly reason: "skip_if" | "skip_if_lte";
  }[];
}

function record(value: unknown, label: string): Readonly<Record<string, unknown>> {
  if (!isObject(value)) throw new TypeError(`${label} must be a JSON object`);
  return value;
}

function stringMap(value: unknown, label: string): Readonly<Record<string, string>> {
  if (value === undefined || value === null) return {};
  if (!isObject(value)) throw new TypeError(`${label} must be an object`);
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, item] of Object.entries(value)) {
    if (typeof item !== "string") throw new TypeError(`${label}.${key} must be a string`);
    output[snakeName(key)] = item;
  }
  return output;
}

function matcherList(value: unknown, label: string): readonly Readonly<JsonObject>[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value) || value.some((item) => !isObject(item))) {
    throw new TypeError(`${label} must be a list of objects`);
  }
  return value as readonly Readonly<JsonObject>[];
}

/** Resolve registry adoption metadata before legacy transform identity fallback. */
export function adoptionMetadata(resource: LoadedResourceMetadata): AdoptionMetadata {
  const explicit = isObject(resource.registry.adopt) ? resource.registry.adopt : {};
  const override = resource.override ?? {};
  const explicitFields = Object.hasOwn(explicit, "identity_fields")
    ? explicit.identity_fields
    : override.identity_fields;
  const identityFields = stringMap(
    explicitFields,
    `${resource.type}.adopt.identity_fields`,
  );
  const importIdValue = Object.hasOwn(explicit, "import_id")
    ? explicit.import_id
    : Object.hasOwn(override, "import_id")
      ? override.import_id
      : Object.hasOwn(identityFields, "import_id")
        ? "{import_id}"
        : "{id}";
  if (typeof importIdValue !== "string") {
    throw new TypeError(`${resource.type}.adopt.import_id must be a string`);
  }
  const keyField = Object.hasOwn(explicit, "key_field")
    ? explicit.key_field
    : Object.hasOwn(override, "key_field")
      ? override.key_field
      : "name";
  if (
    typeof keyField !== "string"
    && (!Array.isArray(keyField) || keyField.some((field) => typeof field !== "string"))
  ) {
    throw new TypeError(`${resource.type}.adopt.key_field must be a string or list of strings`);
  }
  const renames = Object.hasOwn(explicit, "identity_renames")
    ? explicit.identity_renames
    : override.renames;
  const skipIf = Object.hasOwn(explicit, "skip_if")
    ? explicit.skip_if
    : override.skip_if;
  const skipIfLte = Object.hasOwn(explicit, "skip_if_lte")
    ? explicit.skip_if_lte
    : override.skip_if_lte;
  return {
    constantKey: typeof explicit.constant_key === "string" ? explicit.constant_key : null,
    identityFields,
    identityRenames: stringMap(renames, `${resource.type}.adopt.identity_renames`),
    importId: importIdValue,
    keyField: keyField as string | readonly string[],
    skipIf: matcherList(skipIf, `${resource.type}.adopt.skip_if`),
    skipIfLte: matcherList(skipIfLte, `${resource.type}.adopt.skip_if_lte`),
  };
}

function pathValue(
  value: Readonly<Record<string, unknown>>,
  rawPath: string,
): { readonly found: boolean; readonly value?: unknown } {
  let current: unknown = value;
  for (const rawSegment of rawPath.split(".")) {
    const segment = snakeName(rawSegment);
    if (!isObject(current) || !Object.hasOwn(current, segment)) return { found: false };
    current = current[segment];
  }
  return { found: true, value: current };
}

/** Shape a raw object for identity only; it never decides Terraform coverage. */
export function adoptionIdentityItem(options: {
  readonly metadata: AdoptionMetadata;
  readonly raw: unknown;
  readonly resourceType: string;
}): Readonly<Record<string, unknown>> {
  const rawItem = record(snakeJsonKeys(options.raw), `${options.resourceType} raw item`);
  const item: Record<string, unknown> = Object.assign(Object.create(null), rawItem);
  for (const oldName of Object.keys(options.metadata.identityRenames).sort()) {
    const oldSnake = snakeName(oldName);
    const newSnake = snakeName(options.metadata.identityRenames[oldName] ?? "");
    if (Object.hasOwn(item, oldSnake)) {
      item[newSnake] = item[oldSnake];
      delete item[oldSnake];
    }
  }
  for (const alias of Object.keys(options.metadata.identityFields).sort()) {
    const rawPath = options.metadata.identityFields[alias] ?? "";
    let selected = pathValue(rawItem, rawPath);
    if (!selected.found) selected = pathValue(item, rawPath);
    if (!selected.found) {
      throw new TypeError(
        `${options.resourceType} adopt.identity_fields.${alias} path ${JSON.stringify(rawPath)} missing from item`,
      );
    }
    if (Object.hasOwn(item, alias) && !pythonJsonEqual(item[alias], selected.value)) {
      throw new TypeError(
        `${options.resourceType} adopt.identity_fields.${alias} path ${JSON.stringify(rawPath)} would overwrite existing field ${JSON.stringify(alias)}`,
      );
    }
    item[alias] = selected.value;
  }
  return item;
}

function identityString(value: unknown): string {
  return pythonTransformString(value);
}

export function deriveAdoptionKey(
  item: Readonly<Record<string, unknown>>,
  metadata: AdoptionMetadata,
): string {
  if (metadata.constantKey !== null) {
    if (metadata.constantKey.length === 0) {
      throw new TypeError("adopt.constant_key must be a non-empty string");
    }
    return metadata.constantKey;
  }
  const fields = typeof metadata.keyField === "string"
    ? [metadata.keyField]
    : metadata.keyField;
  const parts = fields.map((field) => {
    const selected = pathValue(item, field);
    if (!selected.found) {
      throw new TypeError(
        `key field ${JSON.stringify(field)} missing from item; set adopt.key_field or override key_field`,
      );
    }
    return identityString(selected.value);
  });
  let key = slugifyTransformKey(parts.join(" "));
  if (key.length > 0) return key;
  if (!Object.hasOwn(item, "id") || item.id === null) {
    throw new TypeError(
      `derived key is empty for ${JSON.stringify(fields)} (value(s) ${JSON.stringify(parts)} have no ASCII letters/digits) and item has no 'id' to fall back on`,
    );
  }
  key = `id_${slugifyTransformKey(identityString(item.id))}`;
  return key;
}

/** Derive, validate, and de-duplicate a resource's raw adoption identities. */
export function deriveAdoptionIdentities(options: {
  readonly rawItems: readonly unknown[];
  readonly resource: LoadedResourceMetadata;
}): AdoptionIdentityResult {
  const metadata = adoptionMetadata(options.resource);
  const retained: Array<{
    readonly item: Readonly<Record<string, unknown>>;
    readonly raw: Readonly<Record<string, unknown>>;
  }> = [];
  const skipped: Array<{
    readonly item: Readonly<Record<string, unknown>>;
    readonly reason: "skip_if" | "skip_if_lte";
  }> = [];
  for (const raw of options.rawItems) {
    const rawItem = record(raw, `${options.resource.type} raw item`);
    const item = adoptionIdentityItem({ metadata, raw, resourceType: options.resource.type });
    const reason = transformSkipMatchReason(item, {
      skip_if: metadata.skipIf,
      skip_if_lte: metadata.skipIfLte,
    }, `${options.resource.type}.adopt`);
    if (reason !== null) skipped.push({ item, reason });
    else retained.push({ item, raw: rawItem });
  }
  if (metadata.constantKey !== null && retained.length > 1) {
    throw new TypeError(
      `${options.resource.type} adopt.constant_key ${JSON.stringify(metadata.constantKey)} is only valid for singleton adoption; read produced ${retained.length} items after skip predicates`,
    );
  }
  const keys = new Set<string>();
  const importIds = new Map<string, string>();
  const identities: AdoptionIdentity[] = [];
  for (const entry of retained) {
    const key = deriveAdoptionKey(entry.item, metadata);
    if (keys.has(key)) {
      throw new TypeError(`duplicate derived key ${JSON.stringify(key)} for ${options.resource.type}`);
    }
    const importId = formatImportTemplate(metadata.importId, entry.item);
    const prior = importIds.get(importId);
    if (prior !== undefined) {
      throw new TypeError(
        `${options.resource.type} duplicate import_id for keys ${JSON.stringify(prior)} and ${JSON.stringify(key)}`,
      );
    }
    keys.add(key);
    importIds.set(importId, key);
    identities.push({ importId, item: entry.item, key, raw: entry.raw });
  }
  return { identities, skipped };
}
