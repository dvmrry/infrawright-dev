import { createHash } from "node:crypto";
import path from "node:path";

import { LosslessNumber } from "lossless-json";

import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";
import {
  transformPullItems,
  type PullTransformResult,
} from "./pull-transform.js";
import {
  requireSupportedZccTransformCatalog,
  type TransformCatalog,
  type TransformCatalogResource,
  type TransformImportIdSegment,
} from "./transform-catalog.js";

export type ZccPullResourceType =
  | "zcc_device_cleanup"
  | "zcc_failopen_policy"
  | "zcc_forwarding_profile"
  | "zcc_trusted_network"
  | "zcc_web_privacy";

export interface ZccPullSource {
  readonly path: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccArtifactTarget {
  readonly tenant: string;
  readonly resourceType: string;
  readonly rootLabel: string;
  readonly rootMembers: readonly string[];
  readonly variableName: string;
  readonly configPath: string;
  readonly importsPath: string;
  readonly lookupPath: string | null;
}

export interface ZccTextArtifact {
  readonly path: string;
  readonly media_type: "application/json" | "text/x-hcl";
  readonly encoding: "utf-8";
  readonly sha256: string;
  readonly size_bytes: number;
  readonly content: string;
}

export interface ZccPullArtifactSet {
  readonly kind: "infrawright.zcc_pull_artifact_set";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly source: {
    readonly path: string;
    readonly sha256: string;
    readonly size_bytes: number;
  };
  readonly catalog: {
    readonly kind: "infrawright.transform_catalog";
    readonly schema_version: 1;
    readonly sha256: string;
    readonly sources_sha256: string;
  };
  readonly root: {
    readonly label: string;
    readonly members: readonly string[];
    readonly variable_name: string;
  };
  readonly status: "ready" | "review_required";
  readonly unexpected_drops: readonly string[];
  readonly artifacts: {
    readonly tfvars: ZccTextArtifact;
    readonly imports: ZccTextArtifact;
    readonly lookup: ZccTextArtifact | null;
  };
}

const TENANT = /^(?!\.{1,2}$)[A-Za-z0-9_.-]+$/;
const ROOT_LABEL = /^[a-z0-9_]+$/;
const ROOT_MEMBER = /^zcc_[a-z0-9_]+$/;
const SHA256 = /^[0-9a-f]{64}$/;
const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;
export const ZCC_TRANSFORM_CATALOG_SHA256 =
  "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a";
const ZCC_RESOURCE_TYPES = new Set<string>([
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
]);

type JsonRecord = Record<string, unknown>;

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function hasOwn(record: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function safeRecord(entries: Iterable<readonly [string, unknown]>): JsonRecord {
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

function catalogResource(
  catalog: TransformCatalog,
  resourceType: string,
): TransformCatalogResource {
  const resource = catalog.resources.find((entry) => entry.type === resourceType);
  if (resource === undefined) {
    return fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "artifact target resource is absent from the supported catalog",
    );
  }
  return resource;
}

function assertPath(pathValue: string, basename: string, label: string): void {
  if (
    pathValue.length === 0
    || pathValue.includes("\0")
    || path.posix.basename(pathValue) !== basename
  ) {
    fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      `${label} must name the supported bootstrap artifact`,
    );
  }
}

function layoutPrefix(pathValue: string, suffix: string): string | null {
  if (pathValue === suffix) {
    return "";
  }
  if (!pathValue.endsWith(suffix)) {
    return null;
  }
  const prefix = pathValue.slice(0, -suffix.length);
  return prefix.endsWith("/") ? prefix : null;
}

function prefixedPath(prefix: string, suffix: string): string {
  return `${prefix}${suffix}`;
}

function validateSource(source: ZccPullSource): void {
  if (source.path.length === 0 || source.path.includes("\0")) {
    fail("INVALID_ZCC_PULL_SOURCE", "pull source path is invalid");
  }
  if (!SHA256.test(source.sha256)) {
    fail("INVALID_ZCC_PULL_SOURCE", "pull source digest is invalid");
  }
  if (!Number.isSafeInteger(source.size_bytes) || source.size_bytes < 0) {
    fail("INVALID_ZCC_PULL_SOURCE", "pull source size is invalid");
  }
}

function validateTarget(
  target: ZccArtifactTarget,
  resource: TransformCatalogResource,
): void {
  if (!TENANT.test(target.tenant)) {
    fail("INVALID_ZCC_ARTIFACT_TARGET", "artifact target tenant is invalid");
  }
  if (!ZCC_RESOURCE_TYPES.has(target.resourceType)) {
    fail("INVALID_ZCC_ARTIFACT_TARGET", "artifact target resource is unsupported");
  }
  if (!ROOT_LABEL.test(target.rootLabel)) {
    fail("INVALID_ZCC_ARTIFACT_TARGET", "artifact target root label is invalid");
  }
  if (
    target.rootMembers.length === 0
    || target.rootMembers.some((member) => !ROOT_MEMBER.test(member))
  ) {
    fail("INVALID_ZCC_ARTIFACT_TARGET", "artifact target root members are invalid");
  }
  const expectedMembers = sortedStrings(new Set(target.rootMembers));
  if (
    expectedMembers.length !== target.rootMembers.length
    || expectedMembers.some((member, index) => member !== target.rootMembers[index])
    || !expectedMembers.includes(target.resourceType)
  ) {
    fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "artifact target root members must be sorted, unique, and include the resource",
    );
  }
  const expectedVariable = target.rootLabel === target.resourceType
    ? "items"
    : `${target.resourceType}_items`;
  if (target.variableName !== expectedVariable) {
    fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "artifact target variable name is inconsistent with its root",
    );
  }
  assertPath(
    target.configPath,
    `${target.resourceType}.auto.tfvars.json`,
    "config path",
  );
  assertPath(
    target.importsPath,
    `${target.resourceType}_imports.tf`,
    "imports path",
  );
  const configPrefix = layoutPrefix(
    target.configPath,
    `config/${target.tenant}/${target.resourceType}.auto.tfvars.json`,
  );
  const importsPrefix = layoutPrefix(
    target.importsPath,
    `imports/${target.tenant}/${target.resourceType}_imports.tf`,
  );
  if (configPrefix === null || importsPrefix === null || configPrefix !== importsPrefix) {
    fail(
      "INVALID_ZCC_ARTIFACT_TARGET",
      "artifact target paths must share the canonical deployment layout",
    );
  }
  if (resource.lookup_source === null) {
    if (target.lookupPath !== null) {
      fail(
        "INVALID_ZCC_ARTIFACT_TARGET",
        "non-lookup resources must not nominate a lookup path",
      );
    }
  } else {
    if (target.lookupPath === null) {
      fail(
        "INVALID_ZCC_ARTIFACT_TARGET",
        "lookup resource target is missing its lookup path",
      );
    }
    assertPath(
      target.lookupPath,
      `${target.resourceType}.lookup.json`,
      "lookup path",
    );
    if (
      target.lookupPath !== prefixedPath(
        configPrefix,
        `config/${target.tenant}/${target.resourceType}.lookup.json`,
      )
    ) {
      fail(
        "INVALID_ZCC_ARTIFACT_TARGET",
        "lookup target must share the canonical deployment layout",
      );
    }
  }
}

function canonicalInteger(value: LosslessNumber): string | null {
  const token = value.toString();
  if (!/^-?(?:0|[1-9][0-9]*)$/.test(token)) {
    return null;
  }
  return BigInt(token).toString(10);
}

function pythonScalarString(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "boolean") {
    return value ? "True" : "False";
  }
  if (value instanceof LosslessNumber) {
    const integer = canonicalInteger(value);
    if (integer !== null) {
      return integer;
    }
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) {
    return Object.is(value, -0) ? "0" : String(value);
  }
  return fail(
    "INVALID_ZCC_PULL_DATA",
    "pull import identity must be a supported scalar",
  );
}

function renderImportId(
  original: Readonly<Record<string, unknown>>,
  segments: readonly TransformImportIdSegment[],
): string {
  let output = "";
  for (const segment of segments) {
    if ("literal" in segment) {
      output += segment.literal;
      continue;
    }
    if (!hasOwn(original, segment.field)) {
      return fail(
        "INVALID_ZCC_PULL_DATA",
        "pull item is missing a required import identity field",
      );
    }
    output += pythonScalarString(original[segment.field]);
  }
  return output;
}

function renderImportIdentities(
  resource: TransformCatalogResource,
  originals: PullTransformResult["originals"],
): ReadonlyMap<string, string> {
  const identities = new Map<string, string>();
  const seen = new Set<string>();
  for (const key of sortedStrings(Object.keys(originals))) {
    const original = originals[key];
    if (original === undefined) {
      return fail("INVALID_ZCC_PULL_DATA", "pull import identity is missing");
    }
    const importId = renderImportId(original, resource.import_id.segments);
    if (PYTHON_WHITESPACE_ONLY.test(importId)) {
      return fail(
        "INVALID_ZCC_PULL_DATA",
        "pull import identity must not be empty or whitespace",
      );
    }
    if (seen.has(importId)) {
      return fail(
        "INVALID_ZCC_PULL_DATA",
        "pull import identities must be unique",
      );
    }
    seen.add(importId);
    identities.set(key, importId);
  }
  return identities;
}

function hclStringLiteral(value: string): string {
  if (value.includes("\0")) {
    return fail(
      "INVALID_ZCC_PULL_DATA",
      "pull import identity contains an unsupported character",
    );
  }
  const escaped = value
    .replaceAll("\\", "\\\\")
    .replaceAll('"', '\\"')
    .replaceAll("\n", "\\n")
    .replaceAll("\r", "\\r")
    .replaceAll("\t", "\\t")
    .replaceAll("${", () => "$${")
    .replaceAll("%{", "%%{");
  return `"${escaped}"`;
}

function renderImports(
  resource: TransformCatalogResource,
  identities: ReadonlyMap<string, string>,
): string {
  const blocks: string[] = [];
  for (const key of sortedStrings(identities.keys())) {
    const importId = identities.get(key);
    if (importId === undefined) {
      return fail("INVALID_ZCC_PULL_DATA", "pull import identity is missing");
    }
    blocks.push(
      `import {\n`
      + `  to = module.${resource.type}.${resource.type}.this[${hclStringLiteral(key)}]\n`
      + `  id = ${hclStringLiteral(importId)}\n`
      + `}\n`,
    );
  }
  return blocks.join("\n");
}

function lookupIdentity(value: unknown): string | null {
  if (value === null || value === undefined || value === "") {
    return null;
  }
  return pythonScalarString(value);
}

function renderLookup(
  resource: TransformCatalogResource,
  result: PullTransformResult,
): string | null {
  if (resource.lookup_source === null) {
    return null;
  }
  const byId = safeRecord([]);
  const keyById = safeRecord([]);
  const seen = new Set<string>();
  for (const key of sortedStrings(Object.keys(result.items))) {
    const original = result.originals[key];
    const item = result.items[key];
    if (original === undefined || item === undefined) {
      return fail("INVALID_ZCC_PULL_DATA", "pull lookup survivor is incomplete");
    }
    const merged = safeRecord([
      ...Object.keys(original).map((name) => [name, original[name]] as const),
      ...Object.keys(item).map((name) => [name, item[name]] as const),
    ]);
    const ident = lookupIdentity(merged.id);
    if (ident === null) {
      continue;
    }
    if (seen.has(ident)) {
      return fail(
        "INVALID_ZCC_PULL_DATA",
        "pull lookup identities must be unique",
      );
    }
    seen.add(ident);
    const rawName = merged[resource.lookup_source.name_field];
    const display = typeof rawName === "string" && !PYTHON_WHITESPACE_ONLY.test(rawName)
      ? rawName
      : "<unknown>";
    Object.defineProperty(byId, ident, {
      configurable: true,
      enumerable: true,
      value: display,
      writable: true,
    });
    Object.defineProperty(keyById, ident, {
      configurable: true,
      enumerable: true,
      value: key,
      writable: true,
    });
  }
  const payload = Object.keys(keyById).length === 0
    ? byId
    : safeRecord([
        ["by_id", byId],
        ["key_by_id", keyById],
      ]);
  return renderPythonLosslessArtifactJson(payload);
}

function textArtifact(
  pathValue: string,
  mediaType: ZccTextArtifact["media_type"],
  content: string,
): ZccTextArtifact {
  const bytes = Buffer.from(content, "utf8");
  return {
    path: pathValue,
    media_type: mediaType,
    encoding: "utf-8",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
    content,
  };
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((item) => immutableCopy(item)));
  }
  if (typeof value === "object" && value !== null) {
    const output = safeRecord([]);
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy((value as Readonly<Record<string, unknown>>)[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

/**
 * Compile one already-fetched ZCC pull into an immutable bootstrap artifact
 * set.  This operation is intentionally pure: it does not read previous
 * imports, write files, derive moves/bindings, or support HCL tfvars.
 */
export function compileZccPullArtifactSet(options: {
  readonly catalog: TransformCatalog;
  readonly catalogSha256: string;
  readonly rawItems: readonly unknown[];
  readonly target: ZccArtifactTarget;
  readonly source: ZccPullSource;
}): ZccPullArtifactSet {
  const catalog = requireSupportedZccTransformCatalog(options.catalog);
  if (options.catalogSha256 !== ZCC_TRANSFORM_CATALOG_SHA256) {
    fail(
      "UNSUPPORTED_TRANSFORM_CATALOG",
      "artifact compilation requires the exact committed transform catalog bytes",
    );
  }
  const resource = catalogResource(catalog, options.target.resourceType);
  validateSource(options.source);
  validateTarget(options.target, resource);
  if (
    options.source.path
    !== `pulls/${options.target.tenant}/${options.target.resourceType}.json`
  ) {
    fail(
      "INVALID_ZCC_PULL_SOURCE",
      "pull source path must use the canonical tenant and resource layout",
    );
  }

  let result: PullTransformResult;
  let importIdentities: ReadonlyMap<string, string>;
  let tfvarsContent: string;
  let importsContent: string;
  let lookupContent: string | null;
  try {
    result = transformPullItems({
      catalog,
      rawItems: options.rawItems,
      resourceType: options.target.resourceType,
    });
    // Validate every import identity before rendering any artifact. Besides
    // making imports unambiguous, this prevents a lookup sidecar from silently
    // collapsing distinct Terraform keys onto one provider object.
    importIdentities = renderImportIdentities(resource, result.originals);
    importsContent = renderImports(resource, importIdentities);
    lookupContent = renderLookup(resource, result);
    tfvarsContent = renderPythonLosslessArtifactJson(safeRecord([
      [options.target.variableName, result.items],
    ]));
  } catch (error: unknown) {
    if (error instanceof ProcessFailure && error.code === "INVALID_ZCC_PULL_DATA") {
      throw error;
    }
    throw new ProcessFailure({
      code: "INVALID_ZCC_PULL_DATA",
      category: "domain",
      message: "ZCC pull data cannot be compiled under the supported contract",
    });
  }

  const artifactSet: ZccPullArtifactSet = {
    kind: "infrawright.zcc_pull_artifact_set",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: options.target.resourceType as ZccPullResourceType,
    tenant: options.target.tenant,
    source: {
      path: options.source.path,
      sha256: options.source.sha256,
      size_bytes: options.source.size_bytes,
    },
    catalog: {
      kind: catalog.kind,
      schema_version: catalog.schema_version,
      sha256: options.catalogSha256,
      sources_sha256: catalog.sources_sha256,
    },
    root: {
      label: options.target.rootLabel,
      members: [...options.target.rootMembers],
      variable_name: options.target.variableName,
    },
    status: result.drops.length === 0 ? "ready" : "review_required",
    unexpected_drops: [...result.drops],
    artifacts: {
      tfvars: textArtifact(
        options.target.configPath,
        "application/json",
        tfvarsContent,
      ),
      imports: textArtifact(
        options.target.importsPath,
        "text/x-hcl",
        importsContent,
      ),
      lookup: lookupContent === null || options.target.lookupPath === null
        ? null
        : textArtifact(
            options.target.lookupPath,
            "application/json",
            lookupContent,
          ),
    },
  };
  return immutableCopy(artifactSet) as ZccPullArtifactSet;
}
