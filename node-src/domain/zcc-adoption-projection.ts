import { createHash } from "node:crypto";

import { LosslessNumber } from "lossless-json";

import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import type {
  ZccAdoptionCatalog,
  ZccAdoptionCatalogResource,
  ZccAdoptionIdentityContract,
  ZccAdoptionProjection,
} from "./zcc-adoption-catalog.js";
import { requireSupportedZccAdoptionCatalog } from "./zcc-adoption-catalog.js";
import { ProcessFailure } from "./errors.js";
import { snakeName, slugifyTransformKey } from "./pull-transform.js";

type JsonRecord = Record<string, unknown>;
const MAX_ADOPTION_GRAPH_DEPTH = 128;

export interface ZccAdoptionStateObservation {
  /** The selected provider resource type for this state object. */
  readonly resource_type: string;
  /** Terraform's exact scratch resource instance address. */
  readonly address: string;
  /** Terraform's canonical provider registry identity. */
  readonly provider_name: string;
  /** The config key derived from the corresponding raw API identity. */
  readonly key: string;
  /** The import identifier expected for that config key. */
  readonly import_id: string;
  /** Provider-observed values from the matching Terraform state resource. */
  readonly values: unknown;
  /** Terraform's dynamic sensitive-value mask for `values`. */
  readonly sensitive_values?: unknown;
}

export interface ZccAdoptionIdentities {
  readonly identities: Readonly<
    Record<string, Readonly<Record<string, unknown>>>
  >;
  readonly import_ids: Readonly<Record<string, string>>;
}

export interface ZccAdoptionProjectionResult extends ZccAdoptionIdentities {
  readonly kind: "infrawright.zcc_adoption_projection";
  readonly schema_version: 1;
  readonly product: "zcc";
  readonly resource_type: string;
  readonly catalog: {
    readonly kind: "infrawright.adoption_catalog";
    readonly schema_version: 1;
    readonly sources_sha256: string;
  };
  readonly items: Readonly<
    Record<string, Readonly<Record<string, unknown>>>
  >;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function snapshotSupportedAdoptionGraph(value: unknown): unknown {
  try {
    const snapshot = snapshotPlainJsonGraph(value, {
      maxDepth: MAX_ADOPTION_GRAPH_DEPTH,
    });
    if (snapshot.ok) {
      return snapshot.value;
    }
  } catch {
    // Hostile descriptors and proxies are an invalid graph, never an internal
    // diagnostic. Do not retain or stringify the thrown value.
  }
  fail(
    "INVALID_ZCC_ADOPTION_INPUT",
    "adoption input exceeds the supported plain JSON graph contract",
  );
}

function hasOwn(record: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function isPlainDataRecord(value: unknown): value is JsonRecord {
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

function safeRecord(
  entries: Iterable<readonly [string, unknown]>,
): JsonRecord {
  const output: JsonRecord = Object.create(null) as JsonRecord;
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

function cloneJson(value: unknown, path: string): unknown {
  if (
    value === null
    || typeof value === "boolean"
    || typeof value === "string"
  ) {
    return value;
  }
  if (value instanceof LosslessNumber) {
    return new LosslessNumber(value.toString());
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        `${path} must contain finite JSON numbers`,
      );
    }
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((entry, index) => cloneJson(entry, `${path}[${index}]`));
  }
  if (isPlainDataRecord(value)) {
    return safeRecord(Object.keys(value).map((key) => {
      return [key, cloneJson(value[key], `${path}.${key}`)] as const;
    }));
  }
  return fail(
    "INVALID_ZCC_ADOPTION_INPUT",
    `${path} must contain JSON data properties only`,
  );
}

function immutableCopy(value: unknown): unknown {
  if (value instanceof LosslessNumber) {
    return Object.freeze(new LosslessNumber(value.toString()));
  }
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableCopy(entry)));
  }
  if (isPlainDataRecord(value)) {
    return Object.freeze(safeRecord(Object.keys(value).map((key) => {
      return [key, immutableCopy(value[key])] as const;
    })));
  }
  return value;
}

function snakeKeys(value: unknown, path = "$raw"): unknown {
  if (Array.isArray(value)) {
    return value.map((entry, index) => snakeKeys(entry, `${path}[${index}]`));
  }
  if (isPlainDataRecord(value)) {
    const output = safeRecord([]);
    for (const key of Object.keys(value)) {
      const normalized = snakeName(key);
      // Python's dict comprehension keeps the last value when two source keys
      // snake-case to the same name. Preserve that compatibility here; the
      // identity catalog is the authority over which normalized fields matter.
      Object.defineProperty(output, normalized, {
        configurable: true,
        enumerable: true,
        value: snakeKeys(value[key], `${path}.${key}`),
        writable: true,
      });
    }
    return output;
  }
  return cloneJson(value, path);
}

const MISSING = Symbol("missing");

function pathValue(record: JsonRecord, dottedPath: string): unknown {
  let current: unknown = record;
  for (const segment of dottedPath.split(".")) {
    if (!isPlainDataRecord(current) || !hasOwn(current, segment)) {
      return MISSING;
    }
    current = current[segment];
  }
  return current;
}

function pythonScalarString(value: unknown, label: string): string {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "boolean") {
    return value ? "True" : "False";
  }
  if (value === null) {
    return "None";
  }
  if (value instanceof LosslessNumber) {
    const token = value.toString();
    if (/^-?(?:0|[1-9][0-9]*)$/.test(token)) {
      return BigInt(token).toString(10);
    }
    return fail(
      "ZCC_ADOPTION_IDENTITY_FAILED",
      `${label} uses a non-integral JSON number whose Python spelling is not frozen`,
    );
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) {
    return String(value);
  }
  return fail(
    "ZCC_ADOPTION_IDENTITY_FAILED",
    `${label} must resolve to a JSON scalar`,
  );
}

function numberForLte(value: unknown): number | null {
  if (typeof value === "boolean" || value === null) {
    return null;
  }
  let number: number;
  if (value instanceof LosslessNumber) {
    number = Number(value.toString());
  } else if (typeof value === "number") {
    number = value;
  } else if (typeof value === "string" && value.trim() !== "") {
    const text = value.trim();
    if (!/^[+-]?(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?$/.test(text)) {
      return null;
    }
    number = Number(text.replaceAll("_", ""));
  } else {
    return null;
  }
  return Number.isFinite(number) ? number : null;
}

function shouldSkip(
  item: JsonRecord,
  identity: ZccAdoptionIdentityContract,
): boolean {
  for (const matcher of identity.skip_if) {
    const matches = Object.keys(matcher).every((field) => {
      const candidate = hasOwn(item, field) ? item[field] : null;
      return pythonJsonEqual(candidate, matcher[field]);
    });
    if (matches) {
      return true;
    }
  }
  for (const matcher of identity.skip_if_lte) {
    const matches = Object.keys(matcher).every((field) => {
      const threshold = numberForLte(matcher[field]);
      if (threshold === null) {
        return fail(
          "ZCC_ADOPTION_IDENTITY_FAILED",
          "adoption skip_if_lte contains a non-numeric threshold",
        );
      }
      const candidate = numberForLte(item[field]);
      return candidate !== null && candidate <= threshold;
    });
    if (matches) {
      return true;
    }
  }
  return false;
}

function identityItem(
  raw: unknown,
  resource: ZccAdoptionCatalogResource,
): JsonRecord {
  const snakeRaw = snakeKeys(raw);
  if (!isPlainDataRecord(snakeRaw)) {
    return fail(
      "INVALID_ZCC_ADOPTION_INPUT",
      "each raw adoption item must be a JSON object",
    );
  }
  const rawIdentity = safeRecord(Object.keys(snakeRaw).map((key) => {
    return [key, snakeRaw[key]] as const;
  }));
  const item = safeRecord(Object.keys(snakeRaw).map((key) => {
    return [key, snakeRaw[key]] as const;
  }));
  for (const oldName of sortedStrings(
    Object.keys(resource.identity.identity_renames),
  )) {
    if (!hasOwn(item, oldName)) {
      continue;
    }
    const newName = resource.identity.identity_renames[oldName];
    if (newName === undefined) {
      continue;
    }
    const value = item[oldName];
    delete item[oldName];
    Object.defineProperty(item, newName, {
      configurable: true,
      enumerable: true,
      value,
      writable: true,
    });
  }
  for (const alias of sortedStrings(
    Object.keys(resource.identity.identity_fields),
  )) {
    const path = resource.identity.identity_fields[alias];
    if (path === undefined) {
      continue;
    }
    let value = pathValue(rawIdentity, path);
    if (value === MISSING) {
      value = pathValue(item, path);
    }
    if (value === MISSING) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        `adoption identity field ${JSON.stringify(alias)} is missing`,
      );
    }
    if (hasOwn(item, alias) && !pythonJsonEqual(item[alias], value)) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        `adoption identity field ${JSON.stringify(alias)} would overwrite a different value`,
      );
    }
    Object.defineProperty(item, alias, {
      configurable: true,
      enumerable: true,
      value,
      writable: true,
    });
  }
  return item;
}

function deriveKey(
  item: JsonRecord,
  identity: ZccAdoptionIdentityContract,
): string {
  if (identity.constant_key !== null) {
    return identity.constant_key;
  }
  const parts = identity.key_fields.map((field) => {
    const value = pathValue(item, field);
    if (value === MISSING) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        `adoption key field ${JSON.stringify(field)} is missing`,
      );
    }
    return pythonScalarString(value, `adoption key field ${JSON.stringify(field)}`);
  });
  const slug = slugifyTransformKey(parts.join(" "));
  if (slug !== "") {
    return slug;
  }
  const fallback = pathValue(item, "id");
  if (fallback === MISSING || fallback === null) {
    return fail(
      "ZCC_ADOPTION_IDENTITY_FAILED",
      "derived adoption key is empty and the identity has no non-null id fallback",
    );
  }
  return `id_${slugifyTransformKey(pythonScalarString(fallback, "adoption id fallback"))}`;
}

function deriveImportId(
  item: JsonRecord,
  identity: ZccAdoptionIdentityContract,
): string {
  const parts: string[] = [];
  for (const segment of identity.import_id.segments) {
    if ("literal" in segment) {
      parts.push(segment.literal);
      continue;
    }
    const value = pathValue(item, segment.field);
    if (value === MISSING) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        `adoption import-id field ${JSON.stringify(segment.field)} is missing`,
      );
    }
    parts.push(pythonScalarString(
      value,
      `adoption import-id field ${JSON.stringify(segment.field)}`,
    ));
  }
  return parts.join("");
}

function catalogResource(
  catalog: ZccAdoptionCatalog,
  resourceType: string,
): ZccAdoptionCatalogResource {
  const resource = catalog.resources.find((entry) => entry.type === resourceType);
  if (resource === undefined) {
    return fail(
      "INVALID_ZCC_ADOPTION_INPUT",
      "adoption resource is absent from the supported ZCC catalog",
    );
  }
  return resource;
}

/**
 * Derive the exact Python adoption identity/key/import tuples without applying
 * transform overrides or projecting provider state.
 */
export function deriveZccAdoptionIdentities(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly resourceType: string;
  readonly rawItems: readonly unknown[];
}): ZccAdoptionIdentities {
  const rawItems = snapshotSupportedAdoptionGraph(
    options.rawItems,
  ) as readonly unknown[];
  const catalog = requireSupportedZccAdoptionCatalog(options.catalog);
  const resource = catalogResource(catalog, options.resourceType);
  if (!Array.isArray(rawItems)) {
    return fail(
      "INVALID_ZCC_ADOPTION_INPUT",
      "raw adoption input must be a JSON list",
    );
  }

  const candidates: JsonRecord[] = [];
  for (const raw of rawItems) {
    const item = identityItem(raw, resource);
    if (!shouldSkip(item, resource.identity)) {
      candidates.push(item);
    }
  }
  if (resource.identity.constant_key !== null && candidates.length > 1) {
    return fail(
      "ZCC_ADOPTION_IDENTITY_FAILED",
      "adoption constant_key is valid only for a singleton result",
    );
  }

  const identities = new Map<string, JsonRecord>();
  const importIds = new Map<string, string>();
  const keysByImportId = new Map<string, string>();
  for (const item of candidates) {
    const key = deriveKey(item, resource.identity);
    if (identities.has(key)) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        `duplicate derived adoption key ${JSON.stringify(key)}`,
      );
    }
    const importId = deriveImportId(item, resource.identity);
    if (keysByImportId.has(importId)) {
      return fail(
        "ZCC_ADOPTION_IDENTITY_FAILED",
        "duplicate adoption import identifier",
      );
    }
    identities.set(key, item);
    importIds.set(key, importId);
    keysByImportId.set(importId, key);
  }

  return immutableCopy({
    identities: safeRecord(sortedStrings(identities.keys()).map((key) => {
      return [key, identities.get(key)] as const;
    })),
    import_ids: safeRecord(sortedStrings(importIds.keys()).map((key) => {
      return [key, importIds.get(key)] as const;
    })),
  }) as ZccAdoptionIdentities;
}

function anySensitive(value: unknown): boolean {
  if (value === true) {
    return true;
  }
  if (Array.isArray(value)) {
    return value.some((entry) => anySensitive(entry));
  }
  if (isPlainDataRecord(value)) {
    return Object.keys(value).some((key) => anySensitive(value[key]));
  }
  return false;
}

function assertSensitiveMaskShape(value: unknown): void {
  const stack: { readonly root: boolean; readonly value: unknown }[] = [{
    root: true,
    value,
  }];
  while (stack.length > 0) {
    const current = stack.pop();
    if (current === undefined) {
      return fail(
        "ZCC_ADOPTION_PROJECTION_FAILED",
        "provider sensitive-value mask has an unsupported shape",
      );
    }
    if (
      current.value === null
      || typeof current.value === "boolean"
    ) {
      continue;
    }
    if (isPlainDataRecord(current.value)) {
      for (const key of Object.keys(current.value)) {
        stack.push({ root: false, value: current.value[key] });
      }
      continue;
    }
    if (Array.isArray(current.value) && !current.root) {
      for (const child of current.value) {
        stack.push({ root: false, value: child });
      }
      continue;
    }
    return fail(
      "ZCC_ADOPTION_PROJECTION_FAILED",
      "provider sensitive-value mask has an unsupported shape",
    );
  }
}

function sensitiveAttribute(mask: unknown, name: string): boolean {
  return isPlainDataRecord(mask) && anySensitive(mask[name]);
}

function singleSensitiveMask(mask: unknown, path: string): unknown {
  if (mask === true || isPlainDataRecord(mask)) {
    return mask;
  }
  if (Array.isArray(mask)) {
    if (mask.length === 0) {
      return {};
    }
    if (mask.length === 1) {
      return mask[0] || {};
    }
    return fail(
      "ZCC_ADOPTION_PROJECTION_FAILED",
      `single nested block has an unsupported sensitive shape at ${path}`,
    );
  }
  return {};
}

function listSensitiveMask(mask: unknown, index: number): unknown {
  if (Array.isArray(mask) && index < mask.length) {
    return mask[index] || {};
  }
  return isPlainDataRecord(mask) ? mask : {};
}

function projectionPath(path: string): string {
  return path === "" ? "<root>" : path;
}

function projectBlock(
  values: unknown,
  sensitiveValues: unknown,
  projection: ZccAdoptionProjection,
  path: string,
): JsonRecord {
  if (sensitiveValues === true) {
    return fail(
      "ZCC_ADOPTION_PROJECTION_FAILED",
      `sensitive input path ${projectionPath(path)} cannot be projected`,
    );
  }
  if (!isPlainDataRecord(values)) {
    return fail(
      "ZCC_ADOPTION_PROJECTION_FAILED",
      `provider state path ${projectionPath(path)} is not an object`,
    );
  }
  const output = safeRecord([]);
  for (const name of sortedStrings(Object.keys(projection.attributes))) {
    const contract = projection.attributes[name];
    if (contract === undefined) {
      continue;
    }
    const childPath = path === "" ? name : `${path}.${name}`;
    if (sensitiveAttribute(sensitiveValues, name)) {
      return fail(
        "ZCC_ADOPTION_PROJECTION_FAILED",
        `sensitive input path ${childPath} cannot be projected`,
      );
    }
    const present = hasOwn(values, name) && values[name] !== null;
    if (!present) {
      if (contract.status === "required") {
        return fail(
          "ZCC_ADOPTION_PROJECTION_FAILED",
          `required provider state path is missing: ${childPath}`,
        );
      }
      continue;
    }
    if (contract.provider_sensitive) {
      return fail(
        "ZCC_ADOPTION_PROJECTION_FAILED",
        `sensitive input path ${childPath} cannot be projected`,
      );
    }
    output[name] = cloneJson(values[name], `$state.${childPath}`);
  }

  for (const name of sortedStrings(Object.keys(projection.blocks))) {
    const contract = projection.blocks[name];
    if (contract === undefined) {
      continue;
    }
    const childPath = path === "" ? name : `${path}.${name}`;
    const present = hasOwn(values, name) && values[name] !== null;
    if (!present) {
      if (contract.status === "required") {
        return fail(
          "ZCC_ADOPTION_PROJECTION_FAILED",
          `required provider state path is missing: ${childPath}`,
        );
      }
      continue;
    }
    const value = values[name];
    const childSensitive = isPlainDataRecord(sensitiveValues)
      ? sensitiveValues[name]
      : {};
    if (childSensitive === true) {
      return fail(
        "ZCC_ADOPTION_PROJECTION_FAILED",
        `sensitive input path ${childPath} cannot be projected`,
      );
    }
    if (contract.cardinality === "single") {
      let single: unknown = value;
      if (Array.isArray(value)) {
        if (value.length === 0) {
          if (contract.status === "required") {
            return fail(
              "ZCC_ADOPTION_PROJECTION_FAILED",
              `required provider state path is missing: ${childPath}`,
            );
          }
          continue;
        }
        if (value.length !== 1 || !isPlainDataRecord(value[0])) {
          return fail(
            "ZCC_ADOPTION_PROJECTION_FAILED",
            `single nested block has an unsupported state shape at ${childPath}`,
          );
        }
        single = value[0];
      }
      if (!isPlainDataRecord(single)) {
        return fail(
          "ZCC_ADOPTION_PROJECTION_FAILED",
          `single nested block has an unsupported state shape at ${childPath}`,
        );
      }
      output[name] = projectBlock(
        single,
        singleSensitiveMask(childSensitive, childPath),
        contract.projection,
        childPath,
      );
      continue;
    }
    if (!Array.isArray(value)) {
      return fail(
        "ZCC_ADOPTION_PROJECTION_FAILED",
        `provider state path ${childPath} is not a list`,
      );
    }
    output[name] = value.map((entry, index) => {
      if (!isPlainDataRecord(entry)) {
        return fail(
          "ZCC_ADOPTION_PROJECTION_FAILED",
          `repeated provider state block member is not an object at ${childPath}[${index}]`,
        );
      }
      return projectBlock(
        entry,
        listSensitiveMask(childSensitive, index),
        contract.projection,
        `${childPath}[${index}]`,
      );
    });
  }
  return output;
}

function joinedObservations(
  identities: ZccAdoptionIdentities,
  observations: readonly ZccAdoptionStateObservation[],
  resourceType: string,
  providerName: string,
): ReadonlyMap<string, ZccAdoptionStateObservation> {
  if (!Array.isArray(observations)) {
    return fail(
      "INVALID_ZCC_ADOPTION_INPUT",
      "provider state observations must be a list",
    );
  }
  const byKey = new Map<string, ZccAdoptionStateObservation>();
  for (const observation of observations) {
    if (!isPlainDataRecord(observation)) {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "each provider state observation must be an object",
      );
    }
    if (typeof observation.resource_type !== "string") {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "provider state observation resource_type must be a string",
      );
    }
    if (observation.resource_type !== resourceType) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        "provider state observation resource type does not match the selected resource",
      );
    }
    if (typeof observation.provider_name !== "string") {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "provider state observation provider_name must be a string",
      );
    }
    if (observation.provider_name !== providerName) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        "provider state observation provider does not match the selected provider",
      );
    }
    if (typeof observation.address !== "string") {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "provider state observation address must be a string",
      );
    }
    if (typeof observation.key !== "string" || observation.key === "") {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "provider state observation key must be a non-empty string",
      );
    }
    if (typeof observation.import_id !== "string") {
      return fail(
        "INVALID_ZCC_ADOPTION_INPUT",
        "provider state observation import_id must be a string",
      );
    }
    const instanceName = createHash("sha1")
      .update(observation.key, "utf8")
      .digest("hex")
      .slice(0, 16);
    if (observation.address !== `${resourceType}.iw_${instanceName}`) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        "provider state observation address does not match its derived key",
      );
    }
    if (byKey.has(observation.key)) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        "duplicate provider state observation key",
      );
    }
    byKey.set(
      observation.key,
      observation as unknown as ZccAdoptionStateObservation,
    );
  }

  const expectedKeys = sortedStrings(Object.keys(identities.import_ids));
  const observedKeys = sortedStrings(byKey.keys());
  const missing = expectedKeys.filter((key) => !byKey.has(key));
  const unexpected = observedKeys.filter(
    (key) => !hasOwn(identities.import_ids, key),
  );
  if (missing.length !== 0 || unexpected.length !== 0) {
    return fail(
      "ZCC_ADOPTION_STATE_JOIN_FAILED",
      `provider state observations do not exactly match expected adoption keys (missing=${missing.length}, unexpected=${unexpected.length})`,
    );
  }
  for (const key of expectedKeys) {
    const observation = byKey.get(key);
    if (
      observation === undefined
      || observation.import_id !== identities.import_ids[key]
    ) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        `provider state observation import identifier does not match key ${JSON.stringify(key)}`,
      );
    }
  }
  return byKey;
}

/**
 * Join raw adoption identities to provider-observed state exactly, then apply
 * the schema-owned default projection (no drift/projection policy).
 *
 * The join key is the expected derived config key and its independently
 * derived import ID. Both must match exactly before any state values are read.
 */
export function compileZccAdoptionProjection(options: {
  readonly catalog: ZccAdoptionCatalog;
  readonly resourceType: string;
  readonly rawItems: readonly unknown[];
  readonly observedStates: readonly ZccAdoptionStateObservation[];
}): ZccAdoptionProjectionResult {
  const rawItems = snapshotSupportedAdoptionGraph(
    options.rawItems,
  ) as readonly unknown[];
  const observedStates = snapshotSupportedAdoptionGraph(
    options.observedStates,
  ) as readonly ZccAdoptionStateObservation[];
  const catalog = requireSupportedZccAdoptionCatalog(options.catalog);
  const resource = catalogResource(catalog, options.resourceType);
  const identities = deriveZccAdoptionIdentities({
    catalog,
    rawItems,
    resourceType: options.resourceType,
  });
  const observations = joinedObservations(
    identities,
    observedStates,
    resource.type,
    `registry.terraform.io/${catalog.provider.source}`,
  );
  for (const key of sortedStrings(Object.keys(identities.import_ids))) {
    const observation = observations.get(key);
    if (observation !== undefined) {
      assertSensitiveMaskShape(
        observation.sensitive_values === undefined
          ? {}
          : observation.sensitive_values,
      );
    }
  }
  const items = new Map<string, JsonRecord>();
  for (const key of sortedStrings(Object.keys(identities.import_ids))) {
    const observation = observations.get(key);
    if (observation === undefined) {
      return fail(
        "ZCC_ADOPTION_STATE_JOIN_FAILED",
        "provider state observation disappeared after the exact join",
      );
    }
    items.set(key, projectBlock(
      observation.values,
      observation.sensitive_values ?? {},
      resource.projection,
      "",
    ));
  }
  return immutableCopy({
    kind: "infrawright.zcc_adoption_projection",
    schema_version: 1,
    product: "zcc",
    resource_type: resource.type,
    catalog: {
      kind: catalog.kind,
      schema_version: catalog.schema_version,
      sources_sha256: catalog.sources_sha256,
    },
    identities: identities.identities,
    import_ids: identities.import_ids,
    items: safeRecord(sortedStrings(items.keys()).map((key) => {
      return [key, items.get(key)] as const;
    })),
  }) as ZccAdoptionProjectionResult;
}
