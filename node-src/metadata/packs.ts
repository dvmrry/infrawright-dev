import { readdir, stat } from "node:fs/promises";
import path from "node:path";
import { LosslessNumber } from "lossless-json";

import { DriftPolicy } from "../domain/drift-policy.js";
import { sameStringSequence, sortedStrings } from "../json/python-compatible.js";
import {
  fail,
  isObject,
  MetadataError,
  readJson,
  rejectUnknownKeys,
  requireKeys,
  requireNonEmptyString,
  requireObject,
  validateStringMap,
  type JsonObject,
} from "./validation.js";

export const PACK_SET_KIND = "infrawright.pack-set";
export const REQUIREMENTS_KIND = "infrawright.pack-requirements";
export const PACK_SET_VERSION = 1;

const PACK_SET_KEYS = new Set(["kind", "version", "packs", "shared"]);
const COMPONENT_NAME = /^[a-z0-9][a-z0-9_-]*$/;
const IMPORTABLE_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const MANIFEST_KEYS = new Set([
  "absent_defaults",
  "drift_policy",
  "dynamic_schema",
  "lookup_sources",
  "pin",
  "provider_config",
  "provider_prefixes",
  "provider_sources",
  "references",
  "requires_shared",
  "scope_segments",
  "sensitive_required",
  "unescape_products",
  "vendor",
]);
const MANIFEST_OBJECT_KEYS = [
  "absent_defaults",
  "drift_policy",
  "dynamic_schema",
  "lookup_sources",
  "provider_config",
  "provider_prefixes",
  "provider_sources",
  "references",
  "scope_segments",
  "sensitive_required",
] as const;
const MANIFEST_STRING_KEYS = ["pin", "vendor"] as const;
const MANIFEST_LIST_KEYS = ["requires_shared", "unescape_products"] as const;

export interface PackSelection {
  readonly packs: readonly string[];
  readonly shared: readonly string[];
}

export interface PackSetDocument extends PackSelection {
  readonly kind: typeof PACK_SET_KIND | typeof REQUIREMENTS_KIND;
  readonly version: 1;
}

export interface PackManifest {
  readonly name: string;
  readonly directory: string;
  readonly path: string;
  readonly data: Readonly<JsonObject>;
  readonly providerPrefixes: Readonly<Record<string, string>>;
  readonly providerSources: Readonly<Record<string, string>>;
  readonly requiresShared: readonly string[];
}

export interface PackMetadata {
  readonly root: string;
  readonly manifests: readonly PackManifest[];
  readonly providerPrefixes: Readonly<Record<string, string>>;
  readonly providerSources: Readonly<Record<string, string>>;
  readonly providerOwners: Readonly<Record<string, string>>;
}

export interface ActivePackSetResult {
  readonly profile: PackSetDocument;
  readonly active: PackSelection;
  readonly metadata: PackMetadata;
}

export interface RequirementsResult {
  readonly requirements: PackSetDocument;
  readonly active: PackSelection;
  readonly missing: PackSelection;
  readonly available: boolean;
}

async function isDirectory(candidate: string): Promise<boolean> {
  try {
    return (await stat(candidate)).isDirectory();
  } catch {
    return false;
  }
}

async function isFile(candidate: string): Promise<boolean> {
  try {
    return (await stat(candidate)).isFile();
  } catch {
    return false;
  }
}

function validateNames(value: unknown, label: string): string[] {
  if (!Array.isArray(value)) {
    return fail(`${label} must be a list`);
  }
  const names: string[] = [];
  const seen = new Set<string>();
  for (const [index, item] of value.entries()) {
    if (typeof item !== "string" || !COMPONENT_NAME.test(item)) {
      fail(`${label}[${index}] must be a lowercase pack name`);
    }
    if (seen.has(item)) {
      fail(`${label} duplicates ${JSON.stringify(item)}`);
    }
    seen.add(item);
    names.push(item);
  }
  if (!sameStringSequence(names, sortedStrings(names))) {
    fail(`${label} must be sorted`);
  }
  return names;
}

export function validatePackSetDocument(
  value: unknown,
  source: string,
  expectedKind: typeof PACK_SET_KIND | typeof REQUIREMENTS_KIND,
): PackSetDocument {
  const data = requireObject(value, source);
  rejectUnknownKeys(data, PACK_SET_KEYS, source);
  requireKeys(data, PACK_SET_KEYS, source);
  if (data.kind !== expectedKind) {
    fail(`${source}.kind must be ${JSON.stringify(expectedKind)}`);
  }
  const versionIsOne = data.version === PACK_SET_VERSION
    || (data.version instanceof LosslessNumber && data.version.toString() === "1");
  if (!versionIsOne) {
    fail(`${source}.version must be ${PACK_SET_VERSION}`);
  }
  return {
    kind: expectedKind,
    version: PACK_SET_VERSION,
    packs: validateNames(data.packs, `${source}.packs`),
    shared: validateNames(data.shared, `${source}.shared`),
  };
}

export async function loadPackSetDocument(
  source: string,
  expectedKind: typeof PACK_SET_KIND | typeof REQUIREMENTS_KIND,
): Promise<PackSetDocument> {
  const absolute = path.resolve(source);
  return validatePackSetDocument(
    await readJson(absolute, { preserveNumericTokens: true }),
    absolute,
    expectedKind,
  );
}

async function discoverDirectories(root: string): Promise<string[]> {
  try {
    const names = await readdir(root);
    const directoryFlags = await Promise.all(
      names.map((name) => isDirectory(path.join(root, name))),
    );
    return sortedStrings(names.filter((_name, index) => directoryFlags[index]));
  } catch (error: unknown) {
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) {
      return [];
    }
    throw error;
  }
}

export async function activePackSelection(root: string): Promise<PackSelection> {
  const absolute = path.resolve(root);
  const directories = await discoverDirectories(absolute);
  const sharedRoot = path.join(absolute, "_shared");
  return {
    packs: directories.filter((name) => name !== "_shared"),
    shared: await discoverDirectories(sharedRoot),
  };
}

function requireObjectKey(data: JsonObject, key: string, source: string): void {
  if (Object.hasOwn(data, key) && !isObject(data[key])) {
    fail(`${source}.${key} must be an object`);
  }
}

function validateRuleGroup(data: JsonObject, key: string, source: string): void {
  if (!Object.hasOwn(data, key)) return;
  const group = data[key];
  if (!isObject(group)) fail(`${source}.${key} must be an object`);
  rejectUnknownKeys(group, new Set(["rules"]), `${source}.${key}`);
  requireKeys(group, new Set(["rules"]), `${source}.${key}`);
  if (!Array.isArray(group.rules)) {
    fail(`${source}.${key}.rules must be a list`);
  }
}

function validateLookupSources(value: JsonObject, source: string): void {
  for (const [resourceType, item] of Object.entries(value)) {
    if (resourceType.length === 0) fail(`${source} keys must be non-empty strings`);
    if (!isObject(item)) fail(`${source}.${resourceType} must be an object`);
    rejectUnknownKeys(item, new Set(["name_field"]), `${source}.${resourceType}`);
    requireKeys(item, new Set(["name_field"]), `${source}.${resourceType}`);
    requireNonEmptyString(item.name_field, `${source}.${resourceType}.name_field`);
  }
}

function validateReferences(value: JsonObject, source: string): void {
  for (const [resourceType, rawFields] of Object.entries(value)) {
    if (resourceType.length === 0) fail(`${source} keys must be non-empty strings`);
    if (!isObject(rawFields)) fail(`${source}.${resourceType} must be an object`);
    for (const [field, rawReference] of Object.entries(rawFields)) {
      if (field.length === 0) {
        fail(`${source}.${resourceType} keys must be non-empty strings`);
      }
      const label = `${source}.${resourceType}.${field}`;
      if (!isObject(rawReference)) fail(`${label} must be an object`);
      const keys = new Set(["name_field", "referent"]);
      rejectUnknownKeys(rawReference, keys, label);
      requireKeys(rawReference, keys, label);
      requireNonEmptyString(rawReference.name_field, `${label}.name_field`);
      requireNonEmptyString(rawReference.referent, `${label}.referent`);
    }
  }
}

function validateProviderConfig(value: JsonObject, source: string): void {
  rejectUnknownKeys(value, new Set(["requirements"]), source);
  requireKeys(value, new Set(["requirements"]), source);
  if (!Array.isArray(value.requirements)) {
    fail(`${source}.requirements must be a list`);
  }
}

export function validatePackManifest(value: unknown, source: string): JsonObject {
  const data = requireObject(value, source);
  rejectUnknownKeys(data, MANIFEST_KEYS, source);
  for (const key of MANIFEST_OBJECT_KEYS) requireObjectKey(data, key, source);
  for (const key of MANIFEST_STRING_KEYS) {
    if (Object.hasOwn(data, key)) {
      requireNonEmptyString(data[key], `${source}.${key}`);
    }
  }
  for (const key of MANIFEST_LIST_KEYS) {
    if (Object.hasOwn(data, key) && !Array.isArray(data[key])) {
      fail(`${source}.${key} must be a list`);
    }
  }
  validateStringMap(data.provider_prefixes ?? {}, `${source}.provider_prefixes`);
  validateStringMap(data.provider_sources ?? {}, `${source}.provider_sources`);
  validateStringMap(data.scope_segments ?? {}, `${source}.scope_segments`);
  if (Array.isArray(data.unescape_products)) {
    for (const [index, item] of data.unescape_products.entries()) {
      requireNonEmptyString(item, `${source}.unescape_products[${index}]`);
    }
  }
  if (Array.isArray(data.requires_shared)) {
    const dependencies: string[] = [];
    const seen = new Set<string>();
    for (const [index, item] of data.requires_shared.entries()) {
      if (typeof item !== "string" || !COMPONENT_NAME.test(item)) {
        fail(
          `${source}.requires_shared[${index}] must be a lowercase shared-component name`,
        );
      }
      if (seen.has(item)) {
        fail(`${source}.requires_shared duplicates ${JSON.stringify(item)}`);
      }
      seen.add(item);
      dependencies.push(item);
    }
    if (!sameStringSequence(dependencies, sortedStrings(dependencies))) {
      fail(`${source}.requires_shared must be sorted`);
    }
  }
  if (isObject(data.lookup_sources)) {
    validateLookupSources(data.lookup_sources, `${source}.lookup_sources`);
  }
  if (isObject(data.references)) {
    validateReferences(data.references, `${source}.references`);
  }
  for (const key of ["absent_defaults", "dynamic_schema", "sensitive_required"]) {
    validateRuleGroup(data, key, source);
  }
  if (Object.hasOwn(data, "drift_policy")) {
    try {
      new DriftPolicy(data.drift_policy, `${source}.drift_policy`);
    } catch (error: unknown) {
      const detail = error instanceof Error ? error.message : String(error);
      fail(detail);
    }
  }
  if (isObject(data.provider_config)) {
    validateProviderConfig(data.provider_config, `${source}.provider_config`);
  }
  return data;
}

function manifestRecord(
  name: string,
  directory: string,
  manifestPath: string,
  data: JsonObject,
): PackManifest {
  return {
    name,
    directory,
    path: manifestPath,
    data,
    providerPrefixes: validateStringMap(
      data.provider_prefixes ?? {},
      `${manifestPath}.provider_prefixes`,
    ),
    providerSources: validateStringMap(
      data.provider_sources ?? {},
      `${manifestPath}.provider_sources`,
    ),
    requiresShared: Array.isArray(data.requires_shared)
      ? data.requires_shared as string[]
      : [],
  };
}

export async function loadPackMetadata(root: string): Promise<PackMetadata> {
  const absolute = path.resolve(root);
  const manifests: PackManifest[] = [];
  for (const name of await discoverDirectories(absolute)) {
    if (name === "_shared") continue;
    const directory = path.join(absolute, name);
    const manifestPath = path.join(directory, "pack.json");
    if (!(await isFile(manifestPath))) continue;
    const data = validatePackManifest(await readJson(manifestPath), manifestPath);
    manifests.push(manifestRecord(name, directory, manifestPath, data));
  }

  const prefixes: Record<string, string> = Object.create(null) as Record<string, string>;
  const sources: Record<string, string> = Object.create(null) as Record<string, string>;
  const providerOwners: Record<string, string> = Object.create(null) as Record<
    string,
    string
  >;
  const prefixOwners = new Map<string, string>();
  for (const manifest of manifests) {
    for (const prefix of sortedStrings(Object.keys(manifest.providerPrefixes))) {
      const prior = prefixOwners.get(prefix);
      if (prior !== undefined && prior !== manifest.name) {
        fail(
          `provider prefix ${JSON.stringify(prefix)} is declared by multiple packs: ${prior}, ${manifest.name}`,
        );
      }
      const provider = manifest.providerPrefixes[prefix];
      if (provider === undefined) continue;
      prefixOwners.set(prefix, manifest.name);
      prefixes[prefix] = provider;
      const providerPrior = providerOwners[provider];
      if (providerPrior !== undefined && providerPrior !== manifest.name) {
        fail(
          `provider ${JSON.stringify(provider)} is declared by multiple packs: ${providerPrior}, ${manifest.name}`,
        );
      }
      providerOwners[provider] = manifest.name;
    }
    Object.assign(sources, manifest.providerSources);
  }
  return {
    root: absolute,
    manifests,
    providerPrefixes: prefixes,
    providerSources: sources,
    providerOwners,
  };
}

export async function validateSharedDependencies(
  metadata: PackMetadata,
  packNames?: readonly string[],
): Promise<void> {
  const selected = packNames === undefined ? null : new Set(packNames);
  for (const manifest of metadata.manifests) {
    if (selected !== null && !selected.has(manifest.name)) continue;
    for (const dependency of manifest.requiresShared) {
      const sharedRoot = path.join(metadata.root, "_shared");
      if (!(await isDirectory(path.join(sharedRoot, dependency)))) {
        fail(
          `pack ${manifest.name} requires missing shared component ${dependency} under ${sharedRoot}`,
        );
      }
    }
  }
}

function selectionDelta(
  expected: readonly string[],
  actual: readonly string[],
): { readonly missing: string[]; readonly extra: string[] } {
  const expectedSet = new Set(expected);
  const actualSet = new Set(actual);
  return {
    missing: sortedStrings([...expectedSet].filter((name) => !actualSet.has(name))),
    extra: sortedStrings([...actualSet].filter((name) => !expectedSet.has(name))),
  };
}

function validateKnownSelection(
  selection: PackSelection,
  catalog: PackSelection,
  label: string,
): void {
  const errors: string[] = [];
  for (const key of ["packs", "shared"] as const) {
    const known = new Set(catalog[key]);
    const unknown = sortedStrings(selection[key].filter((name) => !known.has(name)));
    if (unknown.length > 0) errors.push(`unknown ${key}: ${unknown.join(", ")}`);
  }
  if (errors.length > 0) {
    fail(`${label} is outside the pack catalog; ${errors.join("; ")}`);
  }
}

export async function validateActivePackSet(options: {
  readonly profilePath: string;
  readonly root: string;
  readonly catalogPath?: string;
}): Promise<ActivePackSetResult> {
  const profile = await loadPackSetDocument(options.profilePath, PACK_SET_KIND);
  if (options.catalogPath !== undefined) {
    const catalog = await loadPackSetDocument(options.catalogPath, PACK_SET_KIND);
    validateKnownSelection(profile, catalog, path.resolve(options.profilePath));
  }
  const active = await activePackSelection(options.root);
  const packDelta = selectionDelta(profile.packs, active.packs);
  const sharedDelta = selectionDelta(profile.shared, active.shared);
  const errors: string[] = [];
  if (packDelta.missing.length > 0) {
    errors.push(`missing packs: ${packDelta.missing.join(", ")}`);
  }
  if (packDelta.extra.length > 0) {
    errors.push(`undeclared packs: ${packDelta.extra.join(", ")}`);
  }
  if (sharedDelta.missing.length > 0) {
    errors.push(`missing shared: ${sharedDelta.missing.join(", ")}`);
  }
  if (sharedDelta.extra.length > 0) {
    errors.push(`undeclared shared: ${sharedDelta.extra.join(", ")}`);
  }
  if (errors.length > 0) fail(`pack set mismatch; ${errors.join("; ")}`);
  const metadata = await loadPackMetadata(options.root);
  await validateSharedDependencies(metadata, profile.packs);
  return { profile, active, metadata };
}

export async function checkPackRequirements(options: {
  readonly requirementsPath: string;
  readonly root: string;
  readonly catalogPath?: string;
}): Promise<RequirementsResult> {
  const requirements = await loadPackSetDocument(
    options.requirementsPath,
    REQUIREMENTS_KIND,
  );
  if (options.catalogPath !== undefined) {
    const catalog = await loadPackSetDocument(options.catalogPath, PACK_SET_KIND);
    validateKnownSelection(
      requirements,
      catalog,
      path.resolve(options.requirementsPath),
    );
  }
  const active = await activePackSelection(options.root);
  const activePacks = new Set(active.packs);
  const activeShared = new Set(active.shared);
  const missing = {
    packs: requirements.packs.filter((name) => !activePacks.has(name)),
    shared: requirements.shared.filter((name) => !activeShared.has(name)),
  };
  return {
    requirements,
    active,
    missing,
    available: missing.packs.length === 0 && missing.shared.length === 0,
  };
}

export function providerForResource(
  metadata: PackMetadata,
  resourceType: string,
): string {
  for (const prefix of sortedStrings(Object.keys(metadata.providerPrefixes)).sort(
    (left, right) => right.length - left.length,
  )) {
    if (resourceType.startsWith(prefix)) {
      const provider = metadata.providerPrefixes[prefix];
      if (provider !== undefined) return provider;
    }
  }
  return resourceType.split("_", 1)[0] ?? resourceType;
}

export function manifestForProvider(
  metadata: PackMetadata,
  provider: string,
): PackManifest {
  const owner = metadata.providerOwners[provider];
  if (owner === undefined) {
    return fail(`no pack declares provider ${JSON.stringify(provider)}`);
  }
  const manifest = metadata.manifests.find((item) => item.name === owner);
  if (manifest === undefined) {
    return fail(`no manifest found for provider owner ${JSON.stringify(owner)}`);
  }
  return manifest;
}

export function packDirectoryForProvider(
  metadata: PackMetadata,
  provider: string,
): string {
  return manifestForProvider(metadata, provider).directory;
}

export async function validatePackAuthoring(options: {
  readonly root: string;
  readonly pack?: string;
}): Promise<{ readonly names: readonly string[]; readonly metadata: PackMetadata }> {
  const root = path.resolve(options.root);
  if (options.pack === "_shared") {
    fail("_shared is a reserved component root, not a pack");
  }
  const metadata = await loadPackMetadata(root);
  const names = options.pack === undefined
    ? metadata.manifests.map((manifest) => manifest.name)
    : [options.pack];
  if (options.pack !== undefined) {
    const manifest = metadata.manifests.find((item) => item.name === options.pack);
    if (manifest === undefined) {
      fail(`unknown pack ${JSON.stringify(options.pack)} under ${root}`);
    }
  }
  for (const name of names) {
    if (
      !IMPORTABLE_NAME.test(name)
      && await isFile(path.join(root, name, "collector.py"))
    ) {
      fail(
        `pack ${name} cannot expose a Python collector: its directory name is not an importable identifier`,
      );
    }
  }
  await validateSharedDependencies(metadata, names);
  return { names, metadata };
}

export { MetadataError };
