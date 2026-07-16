import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";

import { adoptionMetadata } from "../domain/adoption-meta.js";
import { sortedStrings } from "../json/python-compatible.js";
import { loadPackRoot, type LoadedPackRoot } from "../metadata/loader.js";
import {
  terraformAttributeType,
  terraformAttributesForBlock,
  terraformBlockForSchema,
  terraformBlockTypesForBlock,
  terraformClassifyAttributes,
  terraformInputBlockTypes,
  terraformRequireObject,
  terraformResourceInputAttributes,
  type TerraformTypeEncoding,
} from "../metadata/terraform-schema.js";
import {
  isIntegerJsonNumber,
  isObject,
  type JsonObject,
} from "../metadata/validation.js";

export const ZPA_EVIDENCE_KIND = "infrawright.zpa_provider_evidence";
export const ZPA_EVIDENCE_VERSION = 1;
export const ZPA_PROVIDER_REF = "v4.4.6";
export const ZPA_PROVIDER_COMMIT = "dcf12469a9a8f648be0691c74e9816fc94ec7ddc";
export const ZPA_PROVIDER_REPOSITORY = "https://github.com/zscaler/terraform-provider-zpa";
export const ZPA_RUNTIME_GATE = "terraform_runtime_evidence_required";

const HASH = /^[0-9a-f]{64}$/u;
const IMPORT_MODES = new Set(["numeric_or_alternate_lookup", "passthrough"]);
const NUMERIC_IMPORT_GRAMMARS = new Set([
  "base10_numeric_id_or_email_id",
  "base10_numeric_id_or_name",
  "base10_numeric_id_or_policy_name",
]);
const READ_IDENTITIES = new Set([
  "current_id_lookup_with_response_schema_id",
  "current_id_lookup_without_response_rebind",
  "current_id_matched_in_list",
  "response_id",
  "response_user_id",
]);
const SCHEMA_ID_SOURCES = new Set([
  "importer_seeded",
  "not_source_populated",
  "read_response_id",
]);
const NUMERIC_REQUIREMENT =
  "raw id must be accepted by Go strconv.ParseInt(id, 10, 64) (signed "
  + "64-bit, explicit base 10); otherwise the provider treats it as the "
  + "alternate lookup key";

const REPORT_KEYS = new Set([
  "kind", "local_inputs", "provider", "resources", "schema_version", "summary",
]);
const RESOURCE_KEYS = new Set([
  "exceptions", "fetch", "generated_config", "import", "read_identity",
  "resource_type", "source_evidence", "state_shape",
]);
const FETCH_KEYS = new Set(["optional_http_statuses", "pagination", "path"]);
const IMPORT_KEYS = new Set([
  "alternate_lookup", "engine_import_id_template", "grammar", "mode",
  "numeric_exactness_requirement",
]);
const READ_IDENTITY_KEYS = new Set(["schema_id_attribute", "terraform_instance_id"]);
const STATE_SHAPE_KEYS = new Set([
  "attribute_encodings", "block_nesting_modes", "counts", "required_input_paths",
  "sensitive_input_paths", "shape_sha256",
]);

export class ZpaProviderEvidenceError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ZpaProviderEvidenceError";
  }
}

export type ZpaProviderEvidenceReport = Readonly<JsonObject>;

export interface ZpaEvidenceGitHost {
  run(providerRoot: string, arguments_: readonly string[]): Promise<string>;
}

function fail(message: string): never {
  throw new ZpaProviderEvidenceError(message);
}

function record(value: unknown, label: string): JsonObject {
  if (!isObject(value)) fail(`${label} must contain an object`);
  return value;
}

function list(value: unknown, label: string): unknown[] {
  if (!Array.isArray(value)) fail(`${label} must be a list`);
  return value;
}

function string(value: unknown, label: string): string {
  if (typeof value !== "string") fail(`${label} must be a string`);
  return value;
}

function exactKeys(value: JsonObject, expected: ReadonlySet<string>, label: string): void {
  const keys = new Set(Object.keys(value));
  const missing = sortedStrings([...expected].filter((key) => !keys.has(key)));
  const unknown = sortedStrings([...keys].filter((key) => !expected.has(key)));
  if (missing.length > 0 || unknown.length > 0) {
    fail(`${label} keys differ (missing=${JSON.stringify(missing)} unknown=${JSON.stringify(unknown)})`);
  }
}

function equalJson(left: unknown, right: unknown): boolean {
  if (left === null || right === null) return left === right;
  if (typeof left !== typeof right) return false;
  if (typeof left !== "object") return Object.is(left, right);
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left)
      && Array.isArray(right)
      && left.length === right.length
      && left.every((item, index) => equalJson(item, right[index]));
  }
  if (!isObject(left) || !isObject(right)) return false;
  const leftKeys = sortedStrings(Object.keys(left));
  const rightKeys = sortedStrings(Object.keys(right));
  return leftKeys.length === rightKeys.length
    && leftKeys.every((key, index) => key === rightKeys[index])
    && leftKeys.every((key) => equalJson(left[key], right[key]));
}

function sha256(value: Buffer | string): string {
  return createHash("sha256").update(value).digest("hex");
}

async function fileBytes(filename: string): Promise<Buffer> {
  try {
    return await readFile(filename);
  } catch (error: unknown) {
    const detail = error instanceof Error ? error.message : String(error);
    return fail(`failed to read ${filename}: ${detail}`);
  }
}

async function isFile(filename: string): Promise<boolean> {
  try {
    return (await stat(filename)).isFile();
  } catch {
    return false;
  }
}

function safeRelativePath(value: unknown, label: string): string {
  const candidate = string(value, label);
  if (
    candidate.length === 0
    || path.posix.isAbsolute(candidate)
    || path.win32.isAbsolute(candidate)
    || candidate.split(/[\\/]/u).includes("..")
  ) {
    fail(`${label} must be a safe relative path`);
  }
  return candidate;
}

function canonicalJson(value: unknown): string {
  if (value === null || typeof value === "boolean" || typeof value === "number") {
    return JSON.stringify(value);
  }
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (!isObject(value)) fail("state-shape contains a non-JSON value");
  return `{${sortedStrings(Object.keys(value)).map((key) => {
    return `${JSON.stringify(key)}:${canonicalJson(value[key])}`;
  }).join(",")}}`;
}

function typeEncoding(value: TerraformTypeEncoding): string {
  if (typeof value === "string") return value;
  const [kind, inner] = value;
  if (kind === "object") {
    return `object({${sortedStrings(Object.keys(inner)).map((name) => {
      return `${name}:${typeEncoding(inner[name] as TerraformTypeEncoding)}`;
    }).join(",")}})`;
  }
  return `${kind}(${typeEncoding(inner)})`;
}

export function deriveZpaStateShape(schema: JsonObject): JsonObject {
  const counts = {
    input_attributes: 0,
    input_blocks: 0,
    computed_only_attributes: 0,
    computed_only_blocks: 0,
  };
  const encodings = new Map<string, number>();
  const modes = new Map<string, number>();
  const required: string[] = [];
  const sensitive: string[] = [];

  function walk(block: JsonObject, prefix: string, top: boolean): void {
    const label = prefix === "" ? "resource.block" : `resource.${prefix}block`;
    const classified = top
      ? terraformResourceInputAttributes(block, label)
      : terraformClassifyAttributes(block, label);
    const attributes = terraformAttributesForBlock(block, label);
    counts.computed_only_attributes += classified.computedOnly.length;
    for (const status of ["required", "optional"] as const) {
      for (const name of classified[status]) {
        const attribute = terraformRequireObject(
          attributes[name],
          `${label}.attributes.${name}`,
        );
        const itemPath = `${prefix}${name}`;
        counts.input_attributes += 1;
        const encoded = typeEncoding(
          terraformAttributeType(attribute, `${label}.attributes.${name}`),
        );
        encodings.set(encoded, (encodings.get(encoded) ?? 0) + 1);
        if (status === "required") required.push(itemPath);
        if (attribute.sensitive === true) sensitive.push(itemPath);
      }
    }
    const inputs = terraformInputBlockTypes(block, label);
    const allBlocks = terraformBlockTypesForBlock(block, label);
    counts.computed_only_blocks += Object.keys(allBlocks).filter((name) => !inputs.has(name)).length;
    for (const name of sortedStrings(inputs.keys())) {
      const blockType = inputs.get(name) as JsonObject;
      const itemPath = `${prefix}${name}`;
      counts.input_blocks += 1;
      const mode = string(blockType.nesting_mode, `${label}.block_types.${name}.nesting_mode`);
      modes.set(mode, (modes.get(mode) ?? 0) + 1);
      const minimum = blockType.min_items;
      if (isIntegerJsonNumber(minimum) && BigInt(minimum.toString()) >= 1n) {
        required.push(itemPath);
      }
      walk(
        terraformRequireObject(blockType.block, `${label}.block_types.${name}.block`),
        `${itemPath}[].`,
        false,
      );
    }
  }

  walk(terraformBlockForSchema(schema, "resource"), "", true);
  const shape: JsonObject = {
    attribute_encodings: Object.fromEntries(
      sortedStrings(encodings.keys()).map((name) => [name, encodings.get(name)]),
    ),
    block_nesting_modes: Object.fromEntries(
      sortedStrings(modes.keys()).map((name) => [name, modes.get(name)]),
    ),
    counts,
    required_input_paths: sortedStrings(required),
    sensitive_input_paths: sortedStrings(sensitive),
  };
  return { ...shape, shape_sha256: sha256(canonicalJson(shape)) };
}

function fetchResources(root: LoadedPackRoot): string[] {
  return sortedStrings([...root.resources.values()]
    .filter((resource) => resource.product === "zpa" && Object.hasOwn(resource.registry, "fetch"))
    .map((resource) => resource.type));
}

async function localInputs(
  root: LoadedPackRoot,
  repositoryRoot: string,
  resources: readonly string[],
): Promise<readonly JsonObject[]> {
  const pack = root.packs.manifests.find((manifest) => manifest.name === "zpa");
  if (pack === undefined) fail("active pack root does not contain zpa");
  const paths = [
    pack.path,
    path.join(pack.directory, "registry.json"),
    path.join(pack.directory, "schemas", "provider", "zpa.json"),
  ];
  for (const resourceType of resources) {
    const override = path.join(pack.directory, "overrides", `${resourceType}.json`);
    if (await isFile(override)) paths.push(override);
  }
  return Promise.all(sortedStrings(paths).map(async (filename) => ({
    path: path.relative(repositoryRoot, filename).split(path.sep).join("/"),
    sha256: sha256(await fileBytes(filename)),
  })));
}

function anchor(value: unknown, label: string): JsonObject {
  const result = record(value, label);
  exactKeys(result, new Set(["end_line", "function", "path", "sha256", "start_line", "url"]), label);
  const sourcePath = safeRelativePath(result.path, `${label}.path`);
  const functionName = string(result.function, `${label}.function`);
  if (functionName.length === 0) fail(`${label} is invalid`);
  if (
    typeof result.start_line !== "number"
    || !Number.isInteger(result.start_line)
    || typeof result.end_line !== "number"
    || !Number.isInteger(result.end_line)
    || result.start_line < 1
    || result.end_line < result.start_line
    || typeof result.sha256 !== "string"
    || !HASH.test(result.sha256)
  ) {
    fail(`${label} is invalid`);
  }
  const expectedUrl = `${ZPA_PROVIDER_REPOSITORY}/blob/${ZPA_PROVIDER_REF}/${sourcePath}`
    + `#L${String(result.start_line)}-L${String(result.end_line)}`;
  if (result.url !== expectedUrl) fail(`${label} URL is not pinned to its range`);
  return result;
}

export async function validateZpaProviderEvidenceLocal(options: {
  readonly report: unknown;
  readonly repositoryRoot: string;
}): Promise<ZpaProviderEvidenceReport> {
  const repositoryRoot = path.resolve(options.repositoryRoot);
  const report = record(options.report, "report");
  exactKeys(report, REPORT_KEYS, "report");
  if (report.kind !== ZPA_EVIDENCE_KIND || report.schema_version !== ZPA_EVIDENCE_VERSION) {
    fail("unsupported evidence report kind/version");
  }
  const provider = record(report.provider, "provider");
  exactKeys(provider, new Set(["commit", "ref", "repository", "source_files"]), "provider");
  if (
    provider.commit !== ZPA_PROVIDER_COMMIT
    || provider.ref !== ZPA_PROVIDER_REF
    || provider.repository !== ZPA_PROVIDER_REPOSITORY
  ) {
    fail("provider source pin is unsupported");
  }
  const root = await loadPackRoot({ packsRoot: path.join(repositoryRoot, "packs") });
  const expectedTypes = fetchResources(root);
  if (!equalJson(report.local_inputs, await localInputs(root, repositoryRoot, expectedTypes))) {
    fail("local pack/schema input bindings are stale");
  }
  const resources = list(report.resources, "resources");
  const resourceTypes = resources.map((item) => isObject(item) ? item.resource_type : undefined);
  if (!equalJson(resourceTypes, expectedTypes)) fail("fetch-backed resource set/order is stale");
  const sourcePaths = new Set<string>();
  for (const [index, rawItem] of resources.entries()) {
    const item = record(rawItem, `resources[${index}]`);
    const resourceType = string(item.resource_type, `resources[${index}].resource_type`);
    const label = `resources[${index}]`;
    exactKeys(item, RESOURCE_KEYS, label);
    const resource = root.resources.get(resourceType);
    if (resource === undefined) fail(`unknown evidence resource ${resourceType}`);
    const registryFetch = record(resource.registry.fetch, `${resourceType} registry fetch`);
    const fetch = record(item.fetch, `${label}.fetch`);
    exactKeys(fetch, FETCH_KEYS, `${label}.fetch`);
    const expectedFetch = {
      optional_http_statuses: Array.isArray(registryFetch.optional_http_statuses)
        ? registryFetch.optional_http_statuses
        : [],
      pagination: registryFetch.pagination ?? resource.product,
      path: registryFetch.path,
    };
    if (!equalJson(fetch, expectedFetch)) fail(`${resourceType} fetch metadata is stale`);
    const stateShape = record(item.state_shape, `${label}.state_shape`);
    exactKeys(stateShape, STATE_SHAPE_KEYS, `${label}.state_shape`);
    if (!equalJson(stateShape, deriveZpaStateShape(await root.loadResourceSchema(resourceType) as JsonObject))) {
      fail(`${resourceType} state-shape summary is stale`);
    }
    const imported = record(item.import, `${label}.import`);
    exactKeys(imported, IMPORT_KEYS, `${label}.import`);
    if (typeof imported.mode !== "string" || !IMPORT_MODES.has(imported.mode)) {
      fail(`${resourceType} import mode is unsupported`);
    }
    if (imported.engine_import_id_template !== adoptionMetadata(resource).importId) {
      fail(`${resourceType} engine import template is stale`);
    }
    if (imported.mode === "passthrough") {
      if (
        imported.grammar !== "opaque_provider_id"
        || imported.alternate_lookup !== null
        || imported.numeric_exactness_requirement !== "not_applicable"
      ) {
        fail(`${resourceType} passthrough import claim is inconsistent`);
      }
    } else if (
      typeof imported.grammar !== "string"
      || !NUMERIC_IMPORT_GRAMMARS.has(imported.grammar)
    ) {
      fail(`${resourceType} numeric import grammar is unsupported`);
    } else if (imported.numeric_exactness_requirement !== NUMERIC_REQUIREMENT) {
      fail(`${resourceType} numeric exactness requirement is unsupported`);
    } else if (typeof imported.alternate_lookup !== "string" || imported.alternate_lookup.length === 0) {
      fail(`${resourceType} alternate import claim is inconsistent`);
    }
    const identity = record(item.read_identity, `${label}.read_identity`);
    exactKeys(identity, READ_IDENTITY_KEYS, `${label}.read_identity`);
    if (
      typeof identity.terraform_instance_id !== "string"
      || !READ_IDENTITIES.has(identity.terraform_instance_id)
      || typeof identity.schema_id_attribute !== "string"
      || !SCHEMA_ID_SOURCES.has(identity.schema_id_attribute)
    ) {
      fail(`${resourceType} read identity claim is unsupported`);
    }
    const generated = record(item.generated_config, `${label}.generated_config`);
    exactKeys(generated, new Set(["qualification"]), `${label}.generated_config`);
    if (generated.qualification !== ZPA_RUNTIME_GATE) {
      fail(`${resourceType} overclaims generated-config evidence`);
    }
    const exceptions = list(item.exceptions, `${label}.exceptions`);
    if (
      exceptions.some((value) => typeof value !== "string")
      || !equalJson(exceptions, sortedStrings(new Set(exceptions as string[])))
    ) {
      fail(`${resourceType} exceptions must be sorted and unique`);
    }
    const evidence = record(item.source_evidence, `${label}.source_evidence`);
    exactKeys(evidence, new Set(["exceptions", "importer", "read_identity"]), `${label}.source_evidence`);
    const exceptionAnchors = record(evidence.exceptions, `${label}.source_evidence.exceptions`);
    if (!equalJson(sortedStrings(Object.keys(exceptionAnchors)), exceptions)) {
      fail(`${resourceType} exception anchors are incomplete`);
    }
    const anchors = [
      anchor(evidence.importer, `${label}.importer`),
      anchor(evidence.read_identity, `${label}.read_identity`),
      ...(exceptions as string[]).map((code) => anchor(exceptionAnchors[code], `${label}.${code}`)),
    ];
    for (const itemAnchor of anchors) sourcePaths.add(itemAnchor.path as string);
  }
  const sourceFiles = list(provider.source_files, "provider.source_files");
  const filePaths = sourceFiles.map((raw, index) => {
    const item = record(raw, `provider.source_files[${index}]`);
    exactKeys(item, new Set(["path", "sha256"]), `provider.source_files[${index}]`);
    const sourcePath = safeRelativePath(item.path, `provider.source_files[${index}].path`);
    if (typeof item.sha256 !== "string" || !HASH.test(item.sha256)) {
      fail("provider source-file binding is invalid");
    }
    return sourcePath;
  });
  if (
    !equalJson(filePaths, sortedStrings(new Set(filePaths)))
    || [...sourcePaths].some((sourcePath) => !filePaths.includes(sourcePath))
  ) {
    fail("provider source-file bindings are incomplete or unordered");
  }
  const expectedSummary = {
    fetch_backed_resources: resources.length,
    generated_config_runtime_gates: resources.length,
    numeric_or_alternate_importers: resources.filter((raw) => {
      return record(record(raw, "resource").import, "resource.import").mode
        === "numeric_or_alternate_lookup";
    }).length,
    passthrough_importers: resources.filter((raw) => {
      return record(record(raw, "resource").import, "resource.import").mode === "passthrough";
    }).length,
    resources_with_sensitive_inputs: resources.filter((raw) => {
      const state = record(record(raw, "resource").state_shape, "resource.state_shape");
      return Array.isArray(state.sensitive_input_paths) && state.sensitive_input_paths.length > 0;
    }).length,
    schema_id_not_source_populated: resources.filter((raw) => {
      const identity = record(record(raw, "resource").read_identity, "resource.read_identity");
      return identity.schema_id_attribute === "not_source_populated";
    }).length,
  };
  if (!equalJson(report.summary, expectedSummary)) fail("report summary is stale");
  return report;
}

async function defaultGit(providerRoot: string, arguments_: readonly string[]): Promise<string> {
  const child = spawn("git", ["-C", providerRoot, ...arguments_], {
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk: string) => { stdout += chunk; });
  child.stderr.on("data", (chunk: string) => { stderr += chunk; });
  const result = await new Promise<{ readonly code: number | null; readonly signal: string | null }>(
    (resolve, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => resolve({ code, signal }));
    },
  ).catch((error: unknown) => {
    const detail = error instanceof Error ? error.message : String(error);
    return fail(`provider git check failed: ${detail}`);
  });
  if (result.code !== 0) {
    const detail = `${stdout}${stderr}`.trim();
    fail(`provider git check failed: ${detail}`);
  }
  return stdout.trim();
}

const DEFAULT_GIT_HOST: ZpaEvidenceGitHost = { run: defaultGit };

function splitLinesWithEndings(bytes: Buffer): Buffer[] {
  const lines: Buffer[] = [];
  let start = 0;
  for (let index = 0; index < bytes.length; index += 1) {
    if (bytes[index] === 0x0a) {
      lines.push(bytes.subarray(start, index + 1));
      start = index + 1;
    }
  }
  if (start < bytes.length) lines.push(bytes.subarray(start));
  return lines;
}

export async function validateZpaProviderEvidenceSource(options: {
  readonly host?: ZpaEvidenceGitHost;
  readonly providerRoot: string;
  readonly report: ZpaProviderEvidenceReport;
}): Promise<ZpaProviderEvidenceReport> {
  const providerRoot = path.resolve(options.providerRoot);
  const host = options.host ?? DEFAULT_GIT_HOST;
  if (await host.run(providerRoot, ["rev-parse", "HEAD"]) !== ZPA_PROVIDER_COMMIT) {
    fail("provider checkout is not the pinned commit");
  }
  if (await host.run(providerRoot, ["rev-parse", `${ZPA_PROVIDER_REF}^{commit}`]) !== ZPA_PROVIDER_COMMIT) {
    fail("provider tag does not resolve to the pinned commit");
  }
  const provider = record(options.report.provider, "provider");
  const sourceFiles = list(provider.source_files, "provider.source_files").map((raw, index) => {
    const item = record(raw, `provider.source_files[${index}]`);
    return {
      path: safeRelativePath(item.path, `provider.source_files[${index}].path`),
      sha256: string(item.sha256, `provider.source_files[${index}].sha256`),
    };
  });
  const paths = sourceFiles.map((item) => item.path);
  if ((await host.run(providerRoot, ["status", "--porcelain", "--untracked-files=no", "--", ...paths])) !== "") {
    fail("provider evidence source files are modified");
  }
  const contents = new Map<string, Buffer>();
  for (const source of sourceFiles) {
    const filename = path.join(providerRoot, source.path);
    if (!await isFile(filename)) fail(`provider source binding failed: ${source.path}`);
    const bytes = await fileBytes(filename);
    if (sha256(bytes) !== source.sha256) fail(`provider source binding failed: ${source.path}`);
    contents.set(source.path, bytes);
  }
  for (const rawResource of list(options.report.resources, "resources")) {
    const evidence = record(record(rawResource, "resource").source_evidence, "resource.source_evidence");
    const exceptions = record(evidence.exceptions, "resource.source_evidence.exceptions");
    const anchors = [evidence.importer, evidence.read_identity, ...Object.values(exceptions)];
    for (const rawAnchor of anchors) {
      const itemAnchor = record(rawAnchor, "source anchor");
      const sourcePath = safeRelativePath(itemAnchor.path, "source anchor.path");
      const lines = splitLinesWithEndings(contents.get(sourcePath) ?? Buffer.alloc(0));
      const startLine = itemAnchor.start_line as number;
      const endLine = itemAnchor.end_line as number;
      if (endLine > lines.length) fail(`provider source range exceeds file: ${sourcePath}`);
      const selected = Buffer.concat(lines.slice(startLine - 1, endLine));
      if (sha256(selected) !== itemAnchor.sha256) {
        fail(`provider source range binding failed: ${sourcePath}:${String(startLine)}-${String(endLine)}`);
      }
    }
  }
  return options.report;
}

export async function readZpaProviderEvidence(filename: string): Promise<unknown> {
  try {
    return JSON.parse(await readFile(filename, "utf8")) as unknown;
  } catch (error: unknown) {
    const detail = error instanceof Error ? error.message : String(error);
    return fail(`failed to read ${filename}: ${detail}`);
  }
}

export async function auditZpaProviderEvidence(options: {
  readonly host?: ZpaEvidenceGitHost;
  readonly matrix?: string;
  readonly providerRoot?: string;
  readonly repositoryRoot: string;
}): Promise<ZpaProviderEvidenceReport> {
  const repositoryRoot = path.resolve(options.repositoryRoot);
  const matrix = options.matrix
    ?? path.join(repositoryRoot, "docs", "evidence", "zpa-provider-v4.4.6.json");
  const report = await validateZpaProviderEvidenceLocal({
    report: await readZpaProviderEvidence(matrix),
    repositoryRoot,
  });
  return options.providerRoot === undefined
    ? report
    : validateZpaProviderEvidenceSource({
      ...(options.host === undefined ? {} : { host: options.host }),
      providerRoot: options.providerRoot,
      report,
    });
}
