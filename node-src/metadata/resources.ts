import { readdir, stat } from "node:fs/promises";
import path from "node:path";

import { readOptionalUtf8 } from "../io/files.js";
import { sortedStrings } from "../json/python-compatible.js";
import {
  manifestForProvider,
  providerForResource,
  type PackMetadata,
} from "./packs.js";
import {
  fail,
  isFiniteJsonNumber,
  isIntegerJsonNumber,
  isJsonScalar,
  isObject,
  readJson,
  rejectUnknownKeys,
  requireKeys,
  requireNonEmptyString,
  requireObject,
  validateStringMap,
  type JsonObject,
} from "./validation.js";

const REGISTRY_RESOURCE_KEYS = new Set([
  "adopt",
  "derive",
  "fetch",
  "generate",
  "product",
  "slug_group",
]);
const FETCH_KEYS = new Set([
  "envelope",
  "expand",
  "optional_http_statuses",
  "pagination",
  "path",
  "query",
]);
const PAGINATION_STYLES = new Set(["single", "zcc_v2", "zia", "zpa"]);
const FETCH_QUERY_KEYS = new Set(["query"]);
const DOT_PATH_SEGMENT = /^(?:\.|%2e){1,2}$/iu;
const SAFE_FETCH_PATH = /^(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/{}]|%[0-9A-Fa-f]{2})+$/u;
const DERIVE_KEYS = new Set(["from", "policy_type"]);
const ADOPT_KEYS = new Set([
  "constant_key",
  "identity_fields",
  "identity_renames",
  "import_id",
  "key_field",
  "skip_if",
  "skip_if_lte",
  "unsupported_if",
]);
const UNSUPPORTED_IF_KEYS = new Set([
  "evidence",
  "match",
  "provider",
  "reason",
]);
const UNSUPPORTED_PROVIDER_KEYS = new Set(["source", "version"]);
const OVERRIDE_KEYS = new Set([
  "acknowledged_drops",
  "defaults",
  "divide",
  "drop_if_default",
  "drops",
  "html_escape_fields",
  "identity_fields",
  "import_id",
  "invert_bool",
  "key_field",
  "merge_blocks",
  "no_html_unescape",
  "ranges",
  "references",
  "renames",
  "sample",
  "skip_if",
  "skip_if_lte",
  "sort_lists",
  "split_csv",
  "strip_prefix",
  "value_map",
]);

export interface LoadedRegistry {
  readonly entries: Readonly<Record<string, Readonly<JsonObject>>>;
  readonly sources: Readonly<Record<string, string>>;
}

export interface LoadedOverrides {
  readonly entries: Readonly<Record<string, Readonly<JsonObject>>>;
  readonly sources: Readonly<Record<string, string>>;
}

export interface ProviderSchema {
  readonly provider: string;
  readonly path: string;
  readonly data: Readonly<JsonObject>;
  readonly resourceSchemas: Readonly<Record<string, Readonly<JsonObject>>>;
}

async function isFile(candidate: string): Promise<boolean> {
  try {
    return (await stat(candidate)).isFile();
  } catch {
    return false;
  }
}

function validateQuery(value: unknown, source: string): void {
  if (!isObject(value)) fail(`${source} must be an object`);
  for (const [key, item] of Object.entries(value)) {
    if (!isJsonScalar(item)) {
      fail(`${source}.${key} must be a scalar query value`);
    }
  }
}

/** Return why a collector path-like value is unsafe for WHATWG URL composition. */
export function fetchPathSafetyViolation(value: string): string | null {
  if (value.includes("\\")) return "must not contain backslashes";
  if (value.includes("?") || value.includes("#")) {
    return "must not contain query or fragment delimiters ('?' or '#')";
  }
  if (!SAFE_FETCH_PATH.test(value)) {
    return "must contain only RFC 3986 path characters, valid percent escapes, and expansion braces";
  }
  if (value.split("/").some((segment) => DOT_PATH_SEGMENT.test(segment))) {
    return "must not contain raw or percent-encoded dot path segments";
  }
  return null;
}

/** Expansion values are quoted as one segment; only dot segments survive quoting. */
export function fetchExpansionSafetyViolation(value: string): string | null {
  return value === "." || value === ".."
    ? "must not be '.' or '..'"
    : null;
}

function validateFetchPathValue(value: string, source: string): void {
  const violation = fetchPathSafetyViolation(value);
  if (violation !== null) fail(`${source} ${violation}`);
}

function validateExpand(value: unknown, source: string): void {
  if (!isObject(value)) fail(`${source} must be an object`);
  for (const [key, values] of Object.entries(value)) {
    if (key.length === 0) fail(`${source} keys must be non-empty strings`);
    if (!Array.isArray(values)) fail(`${source}.${key} must be a list`);
    for (const [index, item] of values.entries()) {
      const label = `${source}.${key}[${index}]`;
      const expansion = requireNonEmptyString(item, label);
      const violation = fetchExpansionSafetyViolation(expansion);
      if (violation !== null) fail(`${label} ${violation}`);
    }
  }
}

function validateFetchExpansionShape(
  fetchPath: string,
  expand: unknown,
  source: string,
): void {
  const expansions = isObject(expand) ? Object.keys(expand) : [];
  if (expansions.length > 1) {
    fail(`${source}.expand supports exactly one placeholder`);
  }
  if (expansions.length === 0) {
    if (fetchPath.includes("{") || fetchPath.includes("}")) {
      fail(`${source}.path must not contain undeclared expansion braces`);
    }
    return;
  }
  const key = expansions[0];
  if (key === undefined) return;
  const token = `{${key}}`;
  if (!fetchPath.includes(token)) {
    fail(`${source}.expand key ${JSON.stringify(key)} is not present in path`);
  }
  const remainder = fetchPath.split(token).join("");
  if (remainder.includes("{") || remainder.includes("}")) {
    fail(`${source}.path must not contain undeclared expansion braces`);
  }
}

function validateStatuses(value: unknown, source: string): void {
  if (!Array.isArray(value)) fail(`${source} must be a list`);
  for (const [index, item] of value.entries()) {
    if (!isIntegerJsonNumber(item)) {
      fail(`${source}[${index}] must be an integer`);
    }
  }
}

function validateFetch(value: unknown, source: string): void {
  if (!isObject(value)) fail(`${source} must be an object`);
  rejectUnknownKeys(value, FETCH_KEYS, source);
  requireKeys(value, new Set(["pagination", "path"]), source);
  const pagination = requireNonEmptyString(value.pagination, `${source}.pagination`);
  if (!PAGINATION_STYLES.has(pagination)) {
    fail(
      `${source}.pagination unsupported value ${JSON.stringify(pagination)}; allowed values: ${sortedStrings(PAGINATION_STYLES).join(", ")}`,
    );
  }
  const fetchPath = requireNonEmptyString(value.path, `${source}.path`);
  validateFetchPathValue(fetchPath, `${source}.path`);
  if (Object.hasOwn(value, "envelope")) {
    requireNonEmptyString(value.envelope, `${source}.envelope`);
  }
  if (Object.hasOwn(value, "query")) validateQuery(value.query, `${source}.query`);
  if (Object.hasOwn(value, "expand")) validateExpand(value.expand, `${source}.expand`);
  validateFetchExpansionShape(fetchPath, value.expand, source);
  if (Object.hasOwn(value, "optional_http_statuses")) {
    validateStatuses(value.optional_http_statuses, `${source}.optional_http_statuses`);
  }
}

function validateDerive(value: unknown, source: string): void {
  if (!isObject(value)) fail(`${source} must be an object`);
  rejectUnknownKeys(value, DERIVE_KEYS, source);
  requireKeys(value, new Set(["from"]), source);
  requireNonEmptyString(value.from, `${source}.from`);
  if (Object.hasOwn(value, "policy_type")) {
    requireNonEmptyString(value.policy_type, `${source}.policy_type`);
  }
}

function snakeCase(name: string): string {
  return name
    .replace(/(.)([A-Z][a-z]+)/g, "$1_$2")
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .toLowerCase();
}

function validateSkipMatchers(
  data: JsonObject,
  source: string,
): readonly { readonly field: string; readonly snake: string }[] {
  const fields: { field: string; snake: string }[] = [];
  for (const key of ["skip_if", "skip_if_lte"] as const) {
    if (!Object.hasOwn(data, key)) continue;
    const matchers = data[key];
    if (!Array.isArray(matchers)) fail(`${source}.${key} must be a list`);
    for (const [index, matcher] of matchers.entries()) {
      const matcherPath = `${source}.${key}[${index}]`;
      if (!isObject(matcher)) fail(`${matcherPath} must be an object`);
      if (Object.keys(matcher).length === 0) fail(`${matcherPath} must not be empty`);
      for (const [field, value] of Object.entries(matcher)) {
        if (field.length === 0) {
          fail(`${matcherPath} field names must be non-empty strings`);
        }
        fields.push({ field, snake: snakeCase(field) });
        if (key === "skip_if_lte") {
          if (!isFiniteJsonNumber(value)) {
            fail(`${matcherPath}.${field} threshold must be a finite JSON number`);
          }
        } else if (!isJsonScalar(value)) {
          fail(`${matcherPath}.${field} value must be a scalar`);
        }
      }
    }
  }
  return fields;
}

function validateSkipRenameConflicts(
  data: JsonObject,
  source: string,
  fields: readonly { readonly field: string; readonly snake: string }[],
): void {
  const renames = isObject(data.renames) ? data.renames : data.identity_renames;
  if (!isObject(renames)) return;
  const renamed = new Set<string>();
  for (const [oldName, newName] of Object.entries(renames)) {
    renamed.add(snakeCase(oldName));
    if (typeof newName === "string") renamed.add(snakeCase(newName));
  }
  const conflicts = sortedStrings(
    new Set(fields.filter((entry) => renamed.has(entry.snake)).map((entry) => entry.field)),
  );
  if (conflicts.length > 0) {
    fail(
      `skip predicates in ${source} reference renamed field(s) ${conflicts.join(", ")}; skip predicates run on snake-cased raw input before transform or adoption identity renames, so keep skip fields independent of renames`,
    );
  }
}

function validateAdopt(value: unknown, source: string): void {
  if (!isObject(value)) fail(`${source} must be an object`);
  rejectUnknownKeys(value, ADOPT_KEYS, source);
  if (Object.hasOwn(value, "constant_key") && Object.hasOwn(value, "key_field")) {
    fail(`${source} cannot set both constant_key and key_field`);
  }
  if (Object.hasOwn(value, "constant_key") && !Object.hasOwn(value, "import_id")) {
    fail(`${source}.constant_key requires import_id`);
  }
  for (const key of ["constant_key", "import_id"]) {
    if (Object.hasOwn(value, key)) requireNonEmptyString(value[key], `${source}.${key}`);
  }
  if (Object.hasOwn(value, "key_field")) {
    if (typeof value.key_field === "string") {
      requireNonEmptyString(value.key_field, `${source}.key_field`);
    } else if (
      !Array.isArray(value.key_field)
      || value.key_field.length === 0
      || value.key_field.some((field) => typeof field !== "string" || field.length === 0)
    ) {
      fail(`${source}.key_field must be a non-empty string or list of non-empty strings`);
    }
  }
  for (const key of ["identity_renames", "identity_fields"]) {
    if (Object.hasOwn(value, key)) validateStringMap(value[key], `${source}.${key}`);
  }
  const skipFields = [...validateSkipMatchers(value, source)];
  if (Object.hasOwn(value, "unsupported_if")) {
    const rules = value.unsupported_if;
    if (!Array.isArray(rules) || rules.length === 0) {
      fail(`${source}.unsupported_if must be a non-empty list`);
    }
    const conditions = new Set<string>();
    for (const [index, rawRule] of rules.entries()) {
      const ruleSource = `${source}.unsupported_if[${index}]`;
      const rule = requireObject(rawRule, ruleSource);
      rejectUnknownKeys(rule, UNSUPPORTED_IF_KEYS, ruleSource);
      requireKeys(rule, UNSUPPORTED_IF_KEYS, ruleSource);
      const match = requireObject(rule.match, `${ruleSource}.match`);
      if (Object.keys(match).length === 0) fail(`${ruleSource}.match must not be empty`);
      for (const [field, expected] of Object.entries(match)) {
        if (field.length === 0) fail(`${ruleSource}.match field names must be non-empty strings`);
        if (!isJsonScalar(expected)) fail(`${ruleSource}.match.${field} must be a scalar`);
        skipFields.push({ field, snake: snakeCase(field) });
      }
      const condition = JSON.stringify(Object.fromEntries(
        Object.entries(match)
          .map(([field, expected]) => [snakeCase(field), expected] as const)
          .sort(([left], [right]) => left.localeCompare(right)),
      ));
      if (conditions.has(condition)) fail(`${source}.unsupported_if contains duplicate match ${condition}`);
      conditions.add(condition);
      const provider = requireObject(rule.provider, `${ruleSource}.provider`);
      rejectUnknownKeys(provider, UNSUPPORTED_PROVIDER_KEYS, `${ruleSource}.provider`);
      requireKeys(provider, UNSUPPORTED_PROVIDER_KEYS, `${ruleSource}.provider`);
      requireNonEmptyString(provider.source, `${ruleSource}.provider.source`);
      requireNonEmptyString(provider.version, `${ruleSource}.provider.version`);
      requireNonEmptyString(rule.reason, `${ruleSource}.reason`);
      if (!Array.isArray(rule.evidence) || rule.evidence.length === 0) {
        fail(`${ruleSource}.evidence must be a non-empty list`);
      }
      const evidence = new Set<string>();
      for (const [evidenceIndex, rawEvidence] of rule.evidence.entries()) {
        const item = requireNonEmptyString(
          rawEvidence,
          `${ruleSource}.evidence[${evidenceIndex}]`,
        );
        if (evidence.has(item)) fail(`${ruleSource}.evidence contains duplicate ${JSON.stringify(item)}`);
        evidence.add(item);
      }
    }
  }
  validateSkipRenameConflicts(value, source, skipFields);
}

export function validateRegistry(value: unknown, source: string): JsonObject {
  const data = requireObject(value, source);
  for (const [resourceType, rawEntry] of Object.entries(data)) {
    if (resourceType.length === 0) {
      fail(`${source} resource keys must be non-empty strings`);
    }
    const label = `${source}.${resourceType}`;
    if (!isObject(rawEntry)) fail(`${label} must be an object`);
    rejectUnknownKeys(rawEntry, REGISTRY_RESOURCE_KEYS, label);
    requireKeys(rawEntry, new Set(["product"]), label);
    requireNonEmptyString(rawEntry.product, `${label}.product`);
    if (Object.hasOwn(rawEntry, "generate") && typeof rawEntry.generate !== "boolean") {
      fail(`${label}.generate must be a boolean`);
    }
    if (Object.hasOwn(rawEntry, "slug_group") && typeof rawEntry.slug_group !== "boolean") {
      fail(`${label}.slug_group must be a boolean`);
    }
    if (Object.hasOwn(rawEntry, "fetch")) validateFetch(rawEntry.fetch, `${label}.fetch`);
    if (Object.hasOwn(rawEntry, "derive")) validateDerive(rawEntry.derive, `${label}.derive`);
    if (Object.hasOwn(rawEntry, "adopt")) {
      validateAdopt(rawEntry.adopt, `${label}.adopt`);
      if (
        Object.hasOwn(rawEntry, "derive")
        && isObject(rawEntry.adopt)
        && Object.hasOwn(rawEntry.adopt, "unsupported_if")
      ) {
        fail(`${label}.adopt.unsupported_if is not valid for a derived resource`);
      }
    }
  }
  return data;
}

export async function loadRegistry(
  metadata: PackMetadata,
  packNames?: readonly string[],
): Promise<LoadedRegistry> {
  const selected = packNames === undefined ? null : new Set(packNames);
  const entries: Record<string, Readonly<JsonObject>> = Object.create(null) as Record<
    string,
    Readonly<JsonObject>
  >;
  const sources: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const manifest of metadata.manifests) {
    if (selected !== null && !selected.has(manifest.name)) continue;
    const registryPath = path.join(manifest.directory, "registry.json");
    if (!(await isFile(registryPath))) continue;
    const registry = validateRegistry(
      await readJson(registryPath, {
        preserveNumericTokensUnderKeys: FETCH_QUERY_KEYS,
      }),
      registryPath,
    );
    for (const resourceType of Object.keys(registry)) {
      const prior = sources[resourceType];
      if (prior !== undefined) {
        fail(
          `${registryPath}: duplicate resource type ${JSON.stringify(resourceType)} already loaded from ${prior}`,
        );
      }
      const entry = registry[resourceType];
      if (!isObject(entry)) fail(`${registryPath}.${resourceType} must be an object`);
      entries[resourceType] = entry;
      sources[resourceType] = registryPath;
    }
  }
  return { entries, sources };
}

export function validateUnsupportedProviderScopes(
  metadata: PackMetadata,
  registry: LoadedRegistry,
): void {
  for (const [resourceType, entry] of Object.entries(registry.entries)) {
    const adopt = isObject(entry.adopt) ? entry.adopt : null;
    const rules = adopt !== null && Array.isArray(adopt.unsupported_if)
      ? adopt.unsupported_if
      : [];
    if (rules.length === 0) continue;
    const provider = providerForResource(metadata, resourceType);
    const owner = manifestForProvider(metadata, provider);
    const expectedSource = owner.providerSources[provider];
    const expectedVersion = owner.data.pin;
    for (const [index, rawRule] of rules.entries()) {
      if (!isObject(rawRule) || !isObject(rawRule.provider)) continue;
      const label = `${registry.sources[resourceType] ?? resourceType}.${resourceType}.adopt.unsupported_if[${index}].provider`;
      if (rawRule.provider.source !== expectedSource) {
        fail(
          `${label}.source ${JSON.stringify(rawRule.provider.source)} does not match active provider source ${JSON.stringify(expectedSource)}`,
        );
      }
      if (rawRule.provider.version !== expectedVersion) {
        fail(
          `${label}.version ${JSON.stringify(rawRule.provider.version)} does not match active provider pin ${JSON.stringify(expectedVersion)}`,
        );
      }
    }
  }
}

export function validateOverride(value: unknown, source: string): JsonObject {
  const data = requireObject(value, `override metadata in ${source}`);
  const unknown = sortedStrings(Object.keys(data).filter((key) => !OVERRIDE_KEYS.has(key)));
  if (unknown.length > 0) {
    fail(`unknown override key ${unknown[0]} in ${source}`);
  }
  const skipFields = validateSkipMatchers(data, source);
  validateSkipRenameConflicts(data, source, skipFields);
  return data;
}

export async function loadOverrides(
  metadata: PackMetadata,
  packNames?: readonly string[],
): Promise<LoadedOverrides> {
  const selected = packNames === undefined ? null : new Set(packNames);
  const entries: Record<string, Readonly<JsonObject>> = Object.create(null) as Record<
    string,
    Readonly<JsonObject>
  >;
  const sources: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const manifest of metadata.manifests) {
    if (selected !== null && !selected.has(manifest.name)) continue;
    const overridesDirectory = path.join(manifest.directory, "overrides");
    let names: string[];
    try {
      const candidates = (await readdir(overridesDirectory))
        .filter((name) => name.endsWith(".json"));
      const fileFlags = await Promise.all(
        candidates.map((name) => isFile(path.join(overridesDirectory, name))),
      );
      names = sortedStrings(
        candidates.filter((_name, index) => fileFlags[index]),
      );
    } catch (error: unknown) {
      if (
        typeof error === "object"
        && error !== null
        && "code" in error
        && error.code === "ENOENT"
      ) {
        continue;
      }
      throw error;
    }
    for (const name of names) {
      const overridePath = path.join(overridesDirectory, name);
      const resourceType = name.slice(0, -".json".length);
      const prior = sources[resourceType];
      if (prior !== undefined) {
        fail(
          `${overridePath}: duplicate override resource type ${JSON.stringify(resourceType)} already loaded from ${prior}`,
        );
      }
      entries[resourceType] = validateOverride(
        await readJson(overridePath, { preserveNumericTokens: true }),
        overridePath,
      );
      sources[resourceType] = overridePath;
    }
  }
  return { entries, sources };
}

export function providerSchemaPath(
  metadata: PackMetadata,
  provider: string,
): string {
  return path.join(
    manifestForProvider(metadata, provider).directory,
    "schemas",
    "provider",
    `${provider}.json`,
  );
}

export async function loadProviderSchema(
  metadata: PackMetadata,
  provider: string,
): Promise<ProviderSchema> {
  const schemaPath = providerSchemaPath(metadata, provider);
  const data = requireObject(await readJson(schemaPath), schemaPath);
  const rawResources = data.resource_schemas;
  if (!isObject(rawResources)) {
    fail(`${schemaPath}.resource_schemas must be an object`);
  }
  const resourceSchemas: Record<string, Readonly<JsonObject>> = Object.create(null) as Record<
    string,
    Readonly<JsonObject>
  >;
  for (const [resourceType, schema] of Object.entries(rawResources)) {
    if (!isObject(schema)) {
      fail(`${schemaPath}.resource_schemas.${resourceType} must be an object`);
    }
    resourceSchemas[resourceType] = schema;
  }
  return { provider, path: schemaPath, data, resourceSchemas };
}

export async function loadResourceSchema(
  metadata: PackMetadata,
  resourceType: string,
): Promise<Readonly<JsonObject>> {
  const provider = providerForResource(metadata, resourceType);
  const schema = await loadProviderSchema(metadata, provider);
  const resource = schema.resourceSchemas[resourceType];
  if (resource === undefined) {
    fail(
      `resource type ${JSON.stringify(resourceType)} not in ${provider} schema`,
    );
  }
  return resource;
}

export async function loadResourceMainOverride(
  metadata: PackMetadata,
  resourceType: string,
): Promise<string | null> {
  const provider = providerForResource(metadata, resourceType);
  const overridePath = path.join(
    manifestForProvider(metadata, provider).directory,
    "overrides",
    resourceType,
    "main.tf",
  );
  return readOptionalUtf8(overridePath, `${resourceType} main.tf override`);
}

export async function validatePackResources(
  metadata: PackMetadata,
  packNames?: readonly string[],
): Promise<{
  readonly registry: LoadedRegistry;
  readonly overrides: LoadedOverrides;
}> {
  const [registry, overrides] = await Promise.all([
    loadRegistry(metadata, packNames),
    loadOverrides(metadata, packNames),
  ]);
  validateUnsupportedProviderScopes(metadata, registry);
  return { registry, overrides };
}
