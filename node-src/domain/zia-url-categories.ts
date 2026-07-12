import { createHash } from "node:crypto";
import { types as utilTypes } from "node:util";

import { LosslessNumber } from "lossless-json";

import ziaPackJson from "../../packs/zia/pack.json" with { type: "json" };
import ziaOverrideJson from "../../packs/zia/overrides/zia_url_categories.json" with { type: "json" };
import ziaRegistryJson from "../../packs/zia/registry.json" with { type: "json" };
import ziaSchemaJson from "../../packs/zia/schemas/provider/zia.json" with { type: "json" };

import { renderGeneratedImports } from "./import-moves.js";
import { renderLookupSidecar } from "./lookup-sidecar.js";
import {
  projectProviderState,
  type TerraformSchemaBlock,
} from "./provider-state-projection.js";
import { slugifyTransformKey, snakeName } from "./python-identifiers.js";
import { ProcessFailure } from "./errors.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { comparePythonStrings } from "../json/python-compatible.js";

export const ZIA_URL_CATEGORIES_RESOURCE_TYPE = "zia_url_categories";
export const ZIA_PROVIDER_NAME = "zia";
export const ZIA_PROVIDER_SOURCE = "zscaler/zia";
export const ZIA_PROVIDER_VERSION = "4.7.26";
export const ZIA_URL_CATEGORIES_PAGE_SIZE = 1000;
export const ZIA_URL_CATEGORIES_PATH = "urlCategories";

type JsonRecord = Record<string, unknown>;

export interface ZiaUrlCategoryIdentity {
  readonly address: string;
  readonly importId: string;
  readonly key: string;
  readonly original: Readonly<Record<string, unknown>>;
}

export interface ZiaUrlCategoryStateObservation {
  readonly address: string;
  readonly importId: string;
  readonly key: string;
  readonly providerName: string;
  readonly resourceType: string;
  readonly sensitiveValues: unknown;
  readonly values: unknown;
}

export interface ZiaUrlCategoryArtifactContents {
  readonly imports: string;
  readonly lookup: string;
  readonly pull: string;
  readonly tfvars: string;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
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

function safeRecord(entries: Iterable<readonly [string, unknown]>): JsonRecord {
  const output = Object.create(null) as JsonRecord;
  for (const [name, value] of entries) output[name] = value;
  return output;
}

function cloneAndSnake(value: unknown, path = "$raw"): unknown {
  if (
    value === null
    || typeof value === "string"
    || typeof value === "boolean"
    || typeof value === "number"
  ) {
    return value;
  }
  if (value instanceof LosslessNumber) return new LosslessNumber(value.toString());
  if (Array.isArray(value)) {
    return value.map((entry, index) => cloneAndSnake(entry, `${path}[${index}]`));
  }
  if (!isRecord(value)) {
    return fail("INVALID_ZIA_URL_CATEGORY_INPUT", `unsupported JSON value at ${path}`);
  }
  const output = safeRecord([]);
  const sources = new Map<string, string>();
  for (const name of Object.keys(value)) {
    const normalized = snakeName(name);
    const previous = sources.get(normalized);
    if (previous !== undefined) {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_INPUT",
        `snake_case key collision at ${path}: ${JSON.stringify(previous)} and ${JSON.stringify(name)}`,
      );
    }
    sources.set(normalized, name);
    output[normalized] = cloneAndSnake(value[name], `${path}.${name}`);
  }
  return output;
}

function requiredIdentityString(value: unknown, field: string): string {
  if (typeof value === "string" && value.length > 0 && value.isWellFormed()) {
    return value;
  }
  return fail(
    "INVALID_ZIA_URL_CATEGORY_IDENTITY",
    `ZIA URL category ${field} must be a non-empty string`,
  );
}

function scratchAddress(key: string): string {
  const suffix = createHash("sha1").update(key, "utf8").digest("hex").slice(0, 16);
  return `${ZIA_URL_CATEGORIES_RESOURCE_TYPE}.iw_${suffix}`;
}

function assertEmbeddedSources(): TerraformSchemaBlock {
  const registry = ziaRegistryJson as unknown as Readonly<Record<string, unknown>>;
  const entry = registry[ZIA_URL_CATEGORIES_RESOURCE_TYPE];
  if (
    !isRecord(entry)
    || !isRecord(entry.fetch)
    || entry.generate !== true
    || entry.product !== ZIA_PROVIDER_NAME
    || entry.fetch.path !== ZIA_URL_CATEGORIES_PATH
    || entry.fetch.pagination !== "zia"
    || !isRecord(entry.fetch.query)
    || entry.fetch.query.customOnly !== "true"
  ) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_SOURCE",
      "embedded ZIA URL-category registry entry is not the supported source",
    );
  }
  const pack = ziaPackJson as unknown as Readonly<Record<string, unknown>>;
  const providerSources = pack.provider_sources;
  const lookupSources = pack.lookup_sources;
  if (
    pack.pin !== ZIA_PROVIDER_VERSION
    || !isRecord(providerSources)
    || providerSources.zia !== ZIA_PROVIDER_SOURCE
    || !isRecord(lookupSources)
    || !isRecord(lookupSources.zia_url_categories)
    || lookupSources.zia_url_categories.name_field !== "configured_name"
  ) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_SOURCE",
      "embedded ZIA pack metadata is not the supported source",
    );
  }
  const override = ziaOverrideJson as unknown as Readonly<Record<string, unknown>>;
  if (
    override.key_field !== "configured_name"
    || override.import_id !== "{id}"
  ) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_SOURCE",
      "embedded ZIA URL-category identity override is not supported",
    );
  }
  const schema = ziaSchemaJson as unknown as Readonly<Record<string, unknown>>;
  const resources = schema.resource_schemas;
  const resource = isRecord(resources) ? resources[ZIA_URL_CATEGORIES_RESOURCE_TYPE] : null;
  if (!isRecord(resource) || !isRecord(resource.block)) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_SOURCE",
      "embedded ZIA provider schema is missing URL categories",
    );
  }
  return resource.block as unknown as TerraformSchemaBlock;
}

const ZIA_URL_CATEGORIES_SCHEMA = assertEmbeddedSources();

/** Derive the exact URL-category config key, import ID, and scratch address. */
export function deriveZiaUrlCategoryIdentities(
  rawItems: readonly unknown[],
): readonly ZiaUrlCategoryIdentity[] {
  if (!Array.isArray(rawItems)) {
    return fail("INVALID_ZIA_URL_CATEGORY_INPUT", "ZIA URL-category pull must be a list");
  }
  const keys = new Set<string>();
  const importIds = new Set<string>();
  const addresses = new Set<string>();
  const identities: ZiaUrlCategoryIdentity[] = [];
  for (const raw of rawItems) {
    const original = cloneAndSnake(raw);
    if (!isRecord(original)) {
      return fail("INVALID_ZIA_URL_CATEGORY_INPUT", "each URL category must be an object");
    }
    const configuredName = requiredIdentityString(
      original.configured_name,
      "configured_name",
    );
    const importId = requiredIdentityString(original.id, "id");
    let key = slugifyTransformKey(configuredName);
    if (key === "") key = `id_${slugifyTransformKey(importId)}`;
    if (key === "" || key === "id_") {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_IDENTITY",
        "ZIA URL-category identity does not contain an ASCII key component",
      );
    }
    const address = scratchAddress(key);
    if (keys.has(key) || importIds.has(importId) || addresses.has(address)) {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_IDENTITY",
        "ZIA URL-category identities are not unique",
      );
    }
    keys.add(key);
    importIds.add(importId);
    addresses.add(address);
    identities.push(Object.freeze({ address, importId, key, original }));
  }
  identities.sort((left, right) => comparePythonStrings(left.key, right.key));
  return Object.freeze(identities);
}

/** Join exact Oracle observations and render the four persisted PR-1 artifacts. */
export function compileZiaUrlCategoryArtifacts(options: {
  readonly observations: readonly ZiaUrlCategoryStateObservation[];
  readonly rawItems: readonly unknown[];
}): ZiaUrlCategoryArtifactContents {
  const identities = deriveZiaUrlCategoryIdentities(options.rawItems);
  if (!Array.isArray(options.observations)) {
    return fail(
      "INVALID_ZIA_URL_CATEGORY_OBSERVATION",
      "provider observations must be a list",
    );
  }
  const expected = new Map(identities.map((identity) => [identity.key, identity]));
  const observations = new Map<string, ZiaUrlCategoryStateObservation>();
  for (const observation of options.observations) {
    if (
      !isRecord(observation)
      || typeof observation.key !== "string"
      || observations.has(observation.key)
    ) {
      return fail(
        "INVALID_ZIA_URL_CATEGORY_OBSERVATION",
        "provider observations contain an invalid or duplicate key",
      );
    }
    const identity = expected.get(observation.key);
    if (
      identity === undefined
      || observation.address !== identity.address
      || observation.importId !== identity.importId
      || observation.resourceType !== ZIA_URL_CATEGORIES_RESOURCE_TYPE
      || observation.providerName !== `registry.terraform.io/${ZIA_PROVIDER_SOURCE}`
      || !isRecord(observation.values)
      || observation.values.category_id !== identity.importId
    ) {
      return fail(
        "ZIA_URL_CATEGORY_STATE_JOIN_FAILED",
        "provider observation does not match its URL-category identity",
      );
    }
    observations.set(
      observation.key,
      observation as unknown as ZiaUrlCategoryStateObservation,
    );
  }
  if (observations.size !== expected.size) {
    return fail(
      "ZIA_URL_CATEGORY_STATE_JOIN_FAILED",
      "provider observations do not exactly cover the URL-category identities",
    );
  }

  const items = safeRecord([]) as Record<string, Readonly<Record<string, unknown>>>;
  const originals = safeRecord([]) as Record<string, Readonly<Record<string, unknown>>>;
  for (const identity of identities) {
    const observation = observations.get(identity.key);
    if (observation === undefined) {
      return fail("ZIA_URL_CATEGORY_STATE_JOIN_FAILED", "provider observation disappeared");
    }
    items[identity.key] = projectProviderState({
      schema: ZIA_URL_CATEGORIES_SCHEMA,
      sensitiveValues: observation.sensitiveValues,
      values: observation.values,
    });
    originals[identity.key] = identity.original;
  }

  return Object.freeze({
    imports: renderGeneratedImports(
      ZIA_URL_CATEGORIES_RESOURCE_TYPE,
      identities.map(({ importId, key }) => ({ importId, key })),
    ),
    lookup: renderLookupSidecar({
      identities: originals,
      items,
      nameField: "configured_name",
    }),
    pull: renderPythonLosslessArtifactJson(options.rawItems),
    tfvars: renderPythonLosslessArtifactJson({ items }),
  });
}
