import { spawn } from "node:child_process";
import { createWriteStream } from "node:fs";
import {
  access,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { finished } from "node:stream/promises";

import { parseDataJsonLosslessly } from "../json/control.js";
import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { renderAuthoringJson, writeAuthoringJson } from "./json.js";
import {
  buildOpenApiResourceMap,
  roundPythonRatio4,
} from "./openapi-resource-map.js";
import { validateOpenApiDocument } from "./openapi.js";
import { deriveSourceOperationRegistry } from "./source-operation-map.js";

const DEFAULT_WORK_ROOT = path.join(os.tmpdir(), "infrawright-provider-probes");
const FETCH_TIMEOUT_MS = 60_000;
const HTTP_METHODS = new Set(["get", "post", "put", "patch", "delete"]);
const MAX_COMMAND_OUTPUT = 4_000;
const PROBE_SOURCE_MARKER = ".infrawright-provider-probe-source";

interface ProbeHost {
  readonly download: (url: string, destination: string) => Promise<void>;
  readonly run: (arguments_: readonly string[], options?: {
    readonly cwd?: string;
    readonly stdoutPath?: string;
  }) => Promise<string>;
}

type ArtifactWriter = (filename: string, contents: string) => Promise<void>;

export interface ProviderProbeResult {
  readonly artifacts: Readonly<Record<string, string>>;
  readonly inputs: {
    readonly openapi: string;
    readonly schema: string;
    readonly source_root: string;
  };
  readonly summary: JsonObject;
  readonly work_dir: string;
}

function section(recipe: JsonObject, name: string): JsonObject {
  const value = recipe[name];
  if (value === undefined || value === null) return {};
  if (!isObject(value)) throw new TypeError(`recipe ${name} must be an object`);
  return value;
}

function stringField(object: JsonObject, key: string, label: string): string | undefined {
  const value = object[key];
  if (value === undefined || value === null) return undefined;
  if (typeof value !== "string") throw new TypeError(`recipe ${label} must be a string`);
  return value;
}

export function validateProviderProbeRecipe(value: unknown): JsonObject {
  if (!isObject(value)) throw new TypeError("recipe root must be an object");
  for (const field of ["name", "provider_source", "provider_version", "resource_prefix", "api_prefix"]) {
    stringField(value, field, field);
  }
  const openApi = section(value, "openapi");
  const source = section(value, "source");
  const terraformSchema = section(value, "terraform_schema");
  const terraformProvider = section(value, "terraform_provider");
  const tools = section(value, "tools");
  for (const field of ["path", "url", "format"]) stringField(openApi, field, `openapi.${field}`);
  if (!openApi.path && !openApi.url) {
    throw new TypeError("recipe openapi must include path or url");
  }
  for (const field of ["path", "git", "ref", "subdir"]) stringField(source, field, `source.${field}`);
  if (!source.path && !source.git) {
    throw new TypeError("recipe source must include path or git");
  }
  if (source.git && !source.ref) {
    throw new TypeError("recipe source.ref is required when source.git is used");
  }
  stringField(terraformSchema, "path", "terraform_schema.path");
  for (const field of ["source", "version", "local_name"]) {
    stringField(terraformProvider, field, `terraform_provider.${field}`);
  }
  stringField(tools, "terraform", "tools.terraform");
  if (!terraformSchema.path) {
    if (!value.provider_source) {
      throw new TypeError("recipe provider_source is required when terraform_schema.path is omitted");
    }
    if (!value.provider_version && !terraformProvider.version) {
      throw new TypeError(
        "recipe provider_version or terraform_provider.version is required when terraform_schema.path is omitted",
      );
    }
  }
  return value;
}

async function readJson(filename: string): Promise<unknown> {
  return parseDataJsonLosslessly(await readFile(filename, "utf8"));
}

async function readObject(filename: string): Promise<JsonObject> {
  const value = await readJson(filename);
  if (!isObject(value)) throw new TypeError(`${filename} must contain a JSON object`);
  return value;
}

function boundedText(value: string, limit = MAX_COMMAND_OUTPUT): string {
  if (value.length <= limit) return value;
  return `... <truncated ${String(value.length - limit)} chars>\n${value.slice(-limit)}`;
}

function outputSection(label: string, value: string): string {
  const text = boundedText(value.trim());
  return `${label}:\n${text === "" ? "<empty>" : text}`;
}

async function defaultRunCommand(
  arguments_: readonly string[],
  options: { readonly cwd?: string; readonly stdoutPath?: string } = {},
): Promise<string> {
  if (arguments_.length === 0) throw new TypeError("command must not be empty");
  const executable = arguments_[0] as string;
  const child = spawn(executable, arguments_.slice(1), {
    ...(options.cwd === undefined ? {} : { cwd: options.cwd }),
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk: string) => { stderr += chunk; });
  let outputStream: ReturnType<typeof createWriteStream> | undefined;
  if (options.stdoutPath === undefined) {
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => { stdout += chunk; });
  } else {
    await mkdir(path.dirname(options.stdoutPath), { recursive: true });
    outputStream = createWriteStream(options.stdoutPath, { encoding: "utf8" });
    child.stdout.pipe(outputStream);
  }
  const outcome = await new Promise<{ readonly code: number | null; readonly signal: NodeJS.Signals | null }>(
    (resolve, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => resolve({ code, signal }));
    },
  );
  if (outputStream !== undefined) {
    await finished(outputStream);
    if (outcome.code !== 0) {
      try { stdout = await readFile(options.stdoutPath as string, "utf8"); } catch { stdout = ""; }
    }
  }
  if (outcome.code !== 0) {
    const status = outcome.code === null ? `signal ${String(outcome.signal)}` : String(outcome.code);
    throw new Error(
      `command failed (${status}): ${arguments_.join(" ")}\n`
      + `${outputSection("stdout", stdout)}\n${outputSection("stderr", stderr)}`,
    );
  }
  return stdout;
}

async function defaultDownload(url: string, destination: string): Promise<void> {
  await mkdir(path.dirname(destination), { recursive: true });
  const temporary = `${destination}.tmp`;
  try {
    const parsed = new URL(url);
    if (parsed.protocol === "file:") {
      await writeFile(temporary, await readFile(parsed), { mode: 0o600 });
    } else {
      const response = await fetch(parsed, { signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) });
      if (!response.ok) throw new Error(`HTTP ${String(response.status)}`);
      await writeFile(temporary, Buffer.from(await response.arrayBuffer()), { mode: 0o600 });
    }
    await rename(temporary, destination);
  } catch (error: unknown) {
    await rm(temporary, { force: true }).catch(() => undefined);
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`failed to fetch OpenAPI URL ${url} to ${destination}: ${message}`);
  }
}

const DEFAULT_HOST: ProbeHost = { download: defaultDownload, run: defaultRunCommand };

function recipePath(recipeFile: string, filename: string): string {
  return path.isAbsolute(filename)
    ? filename
    : path.resolve(path.dirname(path.resolve(recipeFile)), filename);
}

function isYamlPath(filename: string, explicitFormat?: string): boolean {
  if (explicitFormat) return ["yaml", "yml"].includes(explicitFormat.toLowerCase());
  const lowered = filename.toLowerCase();
  return lowered.endsWith(".yaml") || lowered.endsWith(".yml");
}

async function copyJsonInput(source: string, destination: string): Promise<string> {
  await writeAuthoringJson(await readJson(source), destination);
  return destination;
}

async function copyOpenApiJsonInput(source: string, destination: string): Promise<string> {
  try {
    return await copyJsonInput(source, destination);
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    throw new TypeError(
      `failed to parse OpenAPI as JSON from ${source}; set openapi.format to "yaml" if this input is YAML: ${message}`,
    );
  }
}

async function convertOpenApiYamlToJson(
  source: string,
  destination: string,
  host: ProbeHost,
): Promise<string> {
  const script = [
    "require 'yaml'; require 'json'; ",
    "STDOUT.write(JSON.pretty_generate(",
    "YAML.safe_load(File.read(ARGV[0]), ",
    "permitted_classes: [], permitted_symbols: [], aliases: true)))",
  ].join("");
  try {
    await host.run(["ruby", "-e", script, source], { stdoutPath: destination });
    await readJson(destination);
    return destination;
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    throw new TypeError(
      `failed to parse OpenAPI as YAML from ${source}; set openapi.format to "json" if this input is JSON: ${message}`,
    );
  }
}

async function prepareOpenApi(
  recipe: JsonObject,
  recipeFile: string,
  workDirectory: string,
  host: ProbeHost,
): Promise<string> {
  const specification = section(recipe, "openapi");
  const inputs = path.join(workDirectory, "inputs");
  await mkdir(inputs, { recursive: true });
  const raw = path.join(inputs, "openapi.raw");
  const output = path.join(inputs, "openapi.json");
  const format = stringField(specification, "format", "openapi.format");
  const local = stringField(specification, "path", "openapi.path");
  if (local) {
    const source = recipePath(recipeFile, local);
    return isYamlPath(source, format)
      ? convertOpenApiYamlToJson(source, output, host)
      : copyOpenApiJsonInput(source, output);
  }
  const url = stringField(specification, "url", "openapi.url");
  if (!url) throw new TypeError("recipe openapi must include path or url");
  await host.download(url, raw);
  return isYamlPath(url, format)
    ? convertOpenApiYamlToJson(raw, output, host)
    : copyOpenApiJsonInput(raw, output);
}

async function exists(filename: string): Promise<boolean> {
  try { await access(filename); return true; } catch { return false; }
}

async function replaceableProbeSource(root: string): Promise<boolean> {
  if (!await exists(root)) return true;
  if (!(await stat(root)).isDirectory()) return false;
  if ((await readdir(root)).length === 0) return true;
  return exists(path.join(root, PROBE_SOURCE_MARKER));
}

async function removeExistingProbeSource(root: string): Promise<void> {
  if (!await exists(root)) return;
  if (!await replaceableProbeSource(root)) {
    throw new TypeError(`refusing to replace existing provider source directory without probe marker: ${root}`);
  }
  await rm(root, { force: true, recursive: true });
}

async function prepareSource(
  recipe: JsonObject,
  recipeFile: string,
  workDirectory: string,
  host: ProbeHost,
): Promise<string> {
  const source = section(recipe, "source");
  const local = stringField(source, "path", "source.path");
  let root: string;
  if (local) {
    root = recipePath(recipeFile, local);
  } else {
    const git = stringField(source, "git", "source.git");
    const ref = stringField(source, "ref", "source.ref");
    if (!git || !ref) throw new TypeError("recipe source must include path or git");
    root = path.join(workDirectory, "source");
    await removeExistingProbeSource(root);
    await host.run(["git", "clone", "--depth", "1", "--branch", ref, git, root]);
    await writeFile(
      path.join(root, PROBE_SOURCE_MARKER),
      "owned by engine.provider_probe; safe to replace on next probe run\n",
      "utf8",
    );
  }
  const subdirectory = stringField(source, "subdir", "source.subdir");
  if (subdirectory) root = path.join(root, subdirectory);
  if (!await exists(root) || !(await stat(root)).isDirectory()) {
    throw new TypeError(`provider source root does not exist: ${root}`);
  }
  return root;
}

function providerLocalName(providerSource: string): string {
  return (providerSource.replace(/\/+$/u, "").split("/").at(-1) ?? "").replaceAll("-", "_");
}

function terraformSource(providerSource: string): string {
  const prefix = "registry.terraform.io/";
  return providerSource.startsWith(prefix) ? providerSource.slice(prefix.length) : providerSource;
}

export function terraformSchemaHcl(
  terraformProvider: JsonObject,
  providerSource: string,
  providerVersion?: string,
): string {
  const source = typeof terraformProvider.source === "string" && terraformProvider.source
    ? terraformProvider.source
    : terraformSource(providerSource);
  const version = typeof terraformProvider.version === "string" && terraformProvider.version
    ? terraformProvider.version
    : providerVersion;
  const localName = typeof terraformProvider.local_name === "string" && terraformProvider.local_name
    ? terraformProvider.local_name
    : providerLocalName(source);
  const lines = [
    "terraform {",
    "  required_providers {",
    `    ${localName} = {`,
    `      source = ${JSON.stringify(source)}`,
  ];
  if (version !== undefined) lines.push(`      version = ${JSON.stringify(version)}`);
  lines.push("    }", "  }", "}", "");
  return lines.join("\n");
}

async function prepareSchema(
  recipe: JsonObject,
  recipeFile: string,
  workDirectory: string,
  host: ProbeHost,
): Promise<string> {
  const schema = section(recipe, "terraform_schema");
  const inputs = path.join(workDirectory, "inputs");
  await mkdir(inputs, { recursive: true });
  const output = path.join(inputs, "provider-schema.json");
  const local = stringField(schema, "path", "terraform_schema.path");
  if (local) return copyJsonInput(recipePath(recipeFile, local), output);
  const providerSource = stringField(recipe, "provider_source", "provider_source");
  if (!providerSource) throw new TypeError("recipe must include provider_source");
  const terraformProvider = section(recipe, "terraform_provider");
  const terraformDirectory = path.join(workDirectory, "terraform-schema");
  await mkdir(terraformDirectory, { recursive: true });
  await writeFile(
    path.join(terraformDirectory, "main.tf"),
    terraformSchemaHcl(
      terraformProvider,
      providerSource,
      stringField(recipe, "provider_version", "provider_version"),
    ),
    "utf8",
  );
  const tools = section(recipe, "tools");
  const terraform = stringField(tools, "terraform", "tools.terraform") ?? "terraform";
  await host.run([terraform, "init", "-backend=false"], { cwd: terraformDirectory });
  await host.run([terraform, "providers", "schema", "-json"], {
    cwd: terraformDirectory,
    stdoutPath: output,
  });
  await readJson(output);
  return output;
}

function pythonFalseyJson(value: unknown): boolean {
  if (value === undefined || value === null || value === false || value === 0 || value === "") {
    return true;
  }
  if (Array.isArray(value)) return value.length === 0;
  return isObject(value) && Object.keys(value).length === 0;
}

export function openApiOperationProfile(openApi: JsonObject): JsonObject {
  let operations = 0;
  let getOperations = 0;
  let missingOperationIds = 0;
  const pathsValue = openApi.paths;
  if (!pythonFalseyJson(pathsValue) && !isObject(pathsValue)) {
    throw new TypeError("OpenAPI paths must be an object");
  }
  const paths = isObject(pathsValue) ? pathsValue : {};
  for (const [apiPath, pathItemValue] of Object.entries(paths)) {
    if (!pythonFalseyJson(pathItemValue) && !isObject(pathItemValue)) {
      throw new TypeError(`OpenAPI path item must be an object: ${apiPath}`);
    }
    const pathItem = isObject(pathItemValue) ? pathItemValue : {};
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!HTTP_METHODS.has(method.toLowerCase()) || !isObject(operation)) continue;
      operations += 1;
      if (method.toLowerCase() === "get") getOperations += 1;
      if (typeof operation.operationId !== "string" || operation.operationId === "") {
        missingOperationIds += 1;
      }
    }
  }
  return {
    get_operations: getOperations,
    missing_operation_ids: missingOperationIds,
    operation_id_coverage_ratio: operations === 0
      ? null
      : roundPythonRatio4(operations - missingOperationIds, operations),
    operations,
  };
}

function object(value: unknown, label: string): JsonObject {
  if (!isObject(value)) throw new TypeError(`${label} must be an object`);
  return value;
}

function warningCodes(report: JsonObject): readonly string[] {
  const codes: string[] = [];
  const collect = (value: unknown): void => {
    const container = isObject(value) ? value : {};
    const warnings = Array.isArray(container.warnings) ? container.warnings : [];
    for (const warning of warnings) {
      if (isObject(warning) && typeof warning.code === "string" && warning.code !== "") {
        codes.push(warning.code);
      }
    }
  };
  collect(report.coverage);
  for (const name of ["registry_read_coverage", "registry_fetch_coverage"]) {
    collect(isObject(report[name]) ? report[name] : {});
  }
  return sortedStrings(codes);
}

function providerMetadata(recipe: JsonObject): JsonObject {
  return {
    api_prefix: recipe.api_prefix ?? "/api/",
    name: recipe.name ?? null,
    provider_source: recipe.provider_source ?? null,
    provider_version: recipe.provider_version ?? null,
    resource_prefix: recipe.resource_prefix ?? "",
  } as JsonObject;
}

function buildSummary(
  recipe: JsonObject,
  sourceReport: JsonObject,
  openApiReport: JsonObject,
  profile: JsonObject,
): JsonObject {
  return {
    generic_openapi_map: object(openApiReport.summary, "openapi report summary"),
    openapi_operation_profile: profile,
    provider: providerMetadata(recipe),
    registry_fetch_coverage: object(
      object(openApiReport.registry_fetch_coverage, "registry fetch coverage").summary,
      "registry fetch coverage summary",
    ),
    registry_read_coverage: object(
      object(openApiReport.registry_read_coverage, "registry read coverage").summary,
      "registry read coverage summary",
    ),
    source_evidence: object(sourceReport.summary, "source report summary"),
    warning_codes: [...warningCodes(openApiReport)],
  } as JsonObject;
}

function artifactPaths(workDirectory: string): Record<string, string> {
  const artifacts = path.join(workDirectory, "artifacts");
  return {
    markdown: path.join(artifacts, "summary.md"),
    openapi_map: path.join(artifacts, "openapi-map.json"),
    source_diagnostics: path.join(artifacts, "source-diagnostics.json"),
    source_registry: path.join(artifacts, "source-registry.json"),
    summary: path.join(artifacts, "summary.json"),
  };
}

async function defaultArtifactWriter(filename: string, contents: string): Promise<void> {
  await writeFile(filename, contents, "utf8");
}

async function publishArtifactSet(
  workDirectory: string,
  artifacts: Readonly<Record<string, string>>,
  rendered: readonly { readonly name: string; readonly contents: string }[],
  writer: ArtifactWriter,
): Promise<void> {
  const finalDirectory = path.join(workDirectory, "artifacts");
  const stagedDirectory = await mkdtemp(path.join(workDirectory, ".provider-probe-artifacts-next-"));
  let previousDirectory: string | undefined;
  let previousMoved = false;
  let published = false;
  try {
    for (const artifact of rendered) {
      await writer(path.join(stagedDirectory, path.basename(artifacts[artifact.name] as string)), artifact.contents);
    }
    if (await exists(finalDirectory)) {
      previousDirectory = await mkdtemp(path.join(workDirectory, ".provider-probe-artifacts-old-"));
      await rm(previousDirectory, { force: true, recursive: true });
      await rename(finalDirectory, previousDirectory);
      previousMoved = true;
    }
    try {
      await rename(stagedDirectory, finalDirectory);
      published = true;
    } catch (error: unknown) {
      if (previousMoved && previousDirectory !== undefined && !await exists(finalDirectory)) {
        await rename(previousDirectory, finalDirectory);
        previousMoved = false;
      }
      throw error;
    }
    if (previousDirectory !== undefined) {
      await rm(previousDirectory, { force: true, recursive: true });
      previousMoved = false;
    }
  } finally {
    if (!published) await rm(stagedDirectory, { force: true, recursive: true });
    if (previousMoved && previousDirectory !== undefined && !await exists(finalDirectory)) {
      await rename(previousDirectory, finalDirectory);
    }
  }
}

function display(value: unknown): string {
  if (value === undefined || value === null) return "";
  return String(value);
}

function coverageDisplay(value: unknown): string {
  if (value === null) return "None";
  if (typeof value === "number" && Number.isInteger(value)) return `${String(value)}.0`;
  return display(value);
}

function summaryRow(label: string, data: JsonObject): string {
  const keys = ["resources", "mapped", "ambiguous", "unmapped", "matched", "coverage"];
  return `| ${label} | ${keys.map((key) => key === "coverage"
    ? coverageDisplay(data[key])
    : display(data[key])).join(" | ")} |`;
}

export function renderProviderProbeMarkdown(
  summary: JsonObject,
  artifacts?: Readonly<Record<string, string>>,
): string {
  const provider = object(summary.provider, "provider summary");
  const source = object(summary.source_evidence, "source evidence summary");
  const generic = object(summary.generic_openapi_map, "OpenAPI summary");
  const read = object(summary.registry_read_coverage, "read coverage summary");
  const profile = object(summary.openapi_operation_profile, "OpenAPI operation profile");
  const heading = provider.name || provider.resource_prefix || "unknown";
  const lines = [
    `# Provider Probe: ${String(heading)}`,
    "",
    `- Provider source: \`${display(provider.provider_source)}\``,
    `- Provider version: \`${display(provider.provider_version)}\``,
    `- Resource prefix: \`${display(provider.resource_prefix)}\``,
    `- API prefix: \`${display(provider.api_prefix)}\``,
    "",
    "## Coverage",
    "",
    "| Section | Resources | Mapped | Ambiguous | Unmapped | Matched | Coverage |",
    "|---|---:|---:|---:|---:|---:|---:|",
    summaryRow("source evidence", {
      ambiguous: source.ambiguous, mapped: source.mapped, resources: source.resources,
      unmapped: source.unmapped,
    } as JsonObject),
    summaryRow("generic OpenAPI map", {
      ambiguous: generic.ambiguous, matched: generic.matched, resources: generic.resources,
      unmapped: generic.unmatched,
    } as JsonObject),
    summaryRow("registry read coverage", {
      ambiguous: read.ambiguous, coverage: read.coverage_ratio, matched: read.matched,
      resources: read.read_resources, unmapped: read.unmatched,
    } as JsonObject),
    "",
    "## OpenAPI",
    "",
    `- Operations: \`${display(profile.operations)}\``,
    `- GET operations: \`${display(profile.get_operations)}\``,
    `- Missing operationIds: \`${display(profile.missing_operation_ids)}\``,
    `- operationId coverage: \`${coverageDisplay(profile.operation_id_coverage_ratio)}\``,
    "",
    "## Warnings",
    "",
  ];
  const warnings = Array.isArray(summary.warning_codes)
    ? summary.warning_codes.filter((item): item is string => typeof item === "string")
    : [];
  if (warnings.length === 0) lines.push("- none");
  else for (const code of warnings) lines.push(`- \`${code}\``);
  if (artifacts !== undefined) {
    lines.push("", "## Artifacts", "");
    for (const name of Object.keys(artifacts).sort(comparePythonStrings)) {
      lines.push(`- \`${name}\`: \`${String(artifacts[name])}\``);
    }
  }
  lines.push("");
  return lines.join("\n");
}

export async function runProviderProbe(options: {
  readonly artifactWriter?: ArtifactWriter;
  readonly host?: ProbeHost;
  readonly recipe: string;
  readonly workDirectory?: string;
}): Promise<ProviderProbeResult> {
  const host = options.host ?? DEFAULT_HOST;
  const recipe = validateProviderProbeRecipe(await readJson(options.recipe));
  const fallbackName = path.basename(options.recipe, path.extname(options.recipe));
  const name = typeof recipe.name === "string" && recipe.name !== "" ? recipe.name : fallbackName;
  const workDirectory = path.resolve(options.workDirectory ?? path.join(DEFAULT_WORK_ROOT, name));
  await mkdir(workDirectory, { recursive: true });
  const schema = await prepareSchema(recipe, options.recipe, workDirectory, host);
  const openapi = await prepareOpenApi(recipe, options.recipe, workDirectory, host);
  const sourceRoot = await prepareSource(recipe, options.recipe, workDirectory, host);
  const schemaData = await readObject(schema);
  const openApiData = await readObject(openapi);
  await validateOpenApiDocument(openApiData);
  const providerSource = typeof recipe.provider_source === "string" ? recipe.provider_source : undefined;
  const resourcePrefix = typeof recipe.resource_prefix === "string" ? recipe.resource_prefix : "";
  const sourceReport = await deriveSourceOperationRegistry({
    openApi: openApiData,
    ...(providerSource === undefined ? {} : { providerSource }),
    resourcePrefix,
    schemaData,
    sourceRoot,
  }) as JsonObject;
  const openApiReport = buildOpenApiResourceMap({
    apiPrefix: typeof recipe.api_prefix === "string" ? recipe.api_prefix : "/api/",
    openApi: openApiData,
    ...(providerSource === undefined ? {} : { providerSource }),
    registryData: object(sourceReport.registry, "source registry"),
    resourcePrefix,
    schemaData,
  }) as JsonObject;
  const summary = buildSummary(
    recipe,
    sourceReport,
    openApiReport,
    openApiOperationProfile(openApiData),
  );
  const artifacts = artifactPaths(workDirectory);
  const rendered = [
    {
      contents: renderAuthoringJson(object(sourceReport.registry, "source registry")),
      name: "source_registry",
    },
    {
      contents: renderAuthoringJson({
      diagnostics: sourceReport.diagnostics,
      summary: sourceReport.summary,
      }),
      name: "source_diagnostics",
    },
    { contents: renderAuthoringJson(openApiReport), name: "openapi_map" },
    { contents: renderAuthoringJson(summary), name: "summary" },
    { contents: renderProviderProbeMarkdown(summary, artifacts), name: "markdown" },
  ] as const;
  await publishArtifactSet(
    workDirectory,
    artifacts,
    rendered,
    options.artifactWriter ?? defaultArtifactWriter,
  );
  return {
    artifacts,
    inputs: { openapi, schema, source_root: sourceRoot },
    summary,
    work_dir: workDirectory,
  };
}
