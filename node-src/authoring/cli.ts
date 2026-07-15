import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

import {
  CliArgumentParseError,
  parseCommandArguments,
} from "../cli/arguments.js";
import { loadPackRoot } from "../metadata/loader.js";
import { validateOverride } from "../metadata/resources.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { renderAuthoringJson } from "./json.js";
import { writeAuthoringJson } from "./json.js";
import {
  apiItemsFrom,
  mergeApiMetadata,
  reconcileItems,
  resourceSchemaFromData,
} from "./reconcile-schema-api.js";
import { buildOpenApiResourceMap } from "./openapi-resource-map.js";
import { validateOpenApiDocument } from "./openapi.js";
import {
  compareSourceOperationReports,
  deriveSourceOperationRegistry,
} from "./source-operation-map.js";
import {
  evaluateSourceEvidence,
  renderSourceEvidenceMarkdown,
} from "./source-evidence-eval.js";
import {
  renderProviderProbeMarkdown,
  runProviderProbe,
} from "./provider-probe.js";
import { runVendorBoundaryAudit } from "./vendor-boundary.js";

export class AuthoringCliUsageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AuthoringCliUsageError";
  }
}

export const AUTHORING_COMMANDS = new Set([
  "audit-vendor-boundary",
  "openapi-map",
  "provider-probe",
  "reconcile",
  "source-evidence-eval",
  "source-operation-map",
]);

interface AuthoringCliContext {
  readonly environment: NodeJS.ProcessEnv;
  readonly repositoryRoot: string;
  readonly stderr: (text: string) => void;
  readonly stdout: (text: string) => void;
}

interface ParsedArguments {
  readonly flags: ReadonlySet<string>;
  readonly options: Readonly<Record<string, readonly string[]>>;
  readonly positional: readonly string[];
}

function parseArguments(
  arguments_: readonly string[],
  options: ReadonlySet<string>,
  repeatable: ReadonlySet<string> = new Set(),
  flags: ReadonlySet<string> = new Set(),
): ParsedArguments {
  try {
    const parsed = parseCommandArguments(arguments_, {
      allowPositionals: true,
      flags: [...flags],
      help: false,
      values: Object.fromEntries([...options].map((name) => [name, {
        multiple: repeatable.has(name),
      }])),
    });
    return {
      flags: parsed.flags,
      options: parsed.options,
      positional: parsed.positionals,
    };
  } catch (error: unknown) {
    if (error instanceof CliArgumentParseError) {
      throw new AuthoringCliUsageError(
        error.message.replace("may be specified only once", "may be passed only once"),
      );
    }
    throw error;
  }
}

function option(parsed: ParsedArguments, name: string): string | undefined {
  return parsed.options[name]?.at(-1);
}

function requiredOption(parsed: ParsedArguments, name: string): string {
  const value = option(parsed, name);
  if (value === undefined) throw new AuthoringCliUsageError(`${name} is required`);
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

async function readOpenApiObject(filename: string): Promise<JsonObject> {
  const value = await readObject(filename);
  await validateOpenApiDocument(value);
  return value;
}

async function writeJson(
  value: unknown,
  filename: string | undefined,
  stdout: (text: string) => void,
): Promise<void> {
  const rendered = renderAuthoringJson(value);
  if (filename === undefined) {
    stdout(rendered);
    return;
  }
  await mkdir(path.dirname(path.resolve(filename)), { recursive: true });
  await writeFile(filename, rendered, "utf8");
}

async function writeText(value: string, filename: string): Promise<void> {
  await mkdir(path.dirname(path.resolve(filename)), { recursive: true });
  await writeFile(filename, value, "utf8");
}

function resourceFilter(value: string | undefined): readonly string[] | undefined {
  if (value === undefined) return undefined;
  const resources = value.split(",").map((item) => item.trim()).filter(Boolean);
  return resources.length === 0 ? undefined : resources;
}

async function reconcileCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(
    arguments_,
    new Set([
      "--api", "--api-options", "--openapi", "--openapi-read", "--openapi-write",
      "--out", "--override", "--provider-source", "--schema",
    ]),
    new Set(["--api", "--api-options", "--openapi-read", "--openapi-write"]),
    new Set(["--fail-on-unknown"]),
  );
  const resourceType = parsed.positional[0];
  if (resourceType === undefined || parsed.positional.length !== 1) {
    throw new AuthoringCliUsageError("reconcile requires one resource type");
  }
  const apiPaths = parsed.options["--api"] ?? [];
  if (apiPaths.length === 0) throw new AuthoringCliUsageError("--api is required");

  const schemaPath = option(parsed, "--schema");
  const providerSource = option(parsed, "--provider-source");
  let resourceSchema: JsonObject;
  if (schemaPath !== undefined) {
    resourceSchema = resourceSchemaFromData(
      await readObject(schemaPath),
      resourceType,
      providerSource,
    );
  } else {
    const root = await loadPackRoot({
      packsRoot: context.environment.INFRAWRIGHT_PACKS
        || path.join(context.repositoryRoot, "packs"),
    });
    resourceSchema = await root.loadResourceSchema(resourceType) as JsonObject;
  }

  const items: JsonObject[] = [];
  for (const apiPath of apiPaths) {
    items.push(...apiItemsFrom(await readJson(apiPath), apiPath));
  }
  const optionDocuments = await Promise.all(
    (parsed.options["--api-options"] ?? []).map((filename) => readJson(filename)),
  );
  const openApiPath = option(parsed, "--openapi");
  const overridePath = option(parsed, "--override");
  const report = reconcileItems({
    apiMetadata: mergeApiMetadata({
      optionDocuments,
      ...(openApiPath === undefined ? {} : { openApi: await readOpenApiObject(openApiPath) }),
      openApiReadOperations: parsed.options["--openapi-read"] ?? [],
      openApiWriteOperations: parsed.options["--openapi-write"] ?? [],
    }),
    items,
    ...(overridePath === undefined
      ? {}
      : { override: validateOverride(await readJson(overridePath), overridePath) }),
    resourceSchema,
    resourceType,
  });
  await writeJson(report.asDict(), option(parsed, "--out"), context.stdout);
  if (parsed.flags.has("--fail-on-unknown") && report.hasUnknowns()) {
    context.stderr(`error: ${resourceType} has unknown API surface; review report\n`);
    return 4;
  }
  return 0;
}

async function openApiMapCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(arguments_, new Set([
    "--api-prefix", "--openapi", "--out", "--provider-source", "--registry",
    "--resource-prefix", "--schema",
  ]));
  if (parsed.positional.length !== 0) {
    throw new AuthoringCliUsageError("openapi-map does not accept positional arguments");
  }
  const registry = option(parsed, "--registry");
  const providerSource = option(parsed, "--provider-source");
  const report = buildOpenApiResourceMap({
    apiPrefix: option(parsed, "--api-prefix") ?? "/api/",
    openApi: await readOpenApiObject(requiredOption(parsed, "--openapi")),
    ...(providerSource === undefined ? {} : { providerSource }),
    ...(registry === undefined ? {} : { registryData: await readObject(registry) }),
    resourcePrefix: option(parsed, "--resource-prefix") ?? "",
    schemaData: await readObject(requiredOption(parsed, "--schema")),
  });
  await writeJson(report, option(parsed, "--out"), context.stdout);
  return 0;
}

async function sourceOperationMapCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(arguments_, new Set([
    "--diagnostics", "--openapi", "--out", "--provider-source", "--resource-prefix",
    "--resources", "--schema", "--sdk-root", "--source-facts", "--source-facts-compare",
    "--source-root",
  ]));
  if (parsed.positional.length !== 0) {
    throw new AuthoringCliUsageError("source-operation-map does not accept positional arguments");
  }
  const sourceFactsPath = option(parsed, "--source-facts");
  const providerSource = option(parsed, "--provider-source");
  const resources = resourceFilter(option(parsed, "--resources"));
  const sdkRoot = option(parsed, "--sdk-root");
  const shared = {
    openApi: await readOpenApiObject(requiredOption(parsed, "--openapi")),
    ...(providerSource === undefined ? {} : { providerSource }),
    resourcePrefix: option(parsed, "--resource-prefix") ?? "",
    ...(resources === undefined ? {} : { resources }),
    schemaData: await readObject(requiredOption(parsed, "--schema")),
    ...(sdkRoot === undefined ? {} : { sdkRoot }),
    sourceRoot: requiredOption(parsed, "--source-root"),
  };
  const report = await deriveSourceOperationRegistry({
    ...shared,
    ...(sourceFactsPath === undefined ? {} : { sourceFacts: await readObject(sourceFactsPath) }),
  });
  const comparisonPath = option(parsed, "--source-facts-compare");
  if (comparisonPath !== undefined) {
    if (sourceFactsPath === undefined) {
      throw new AuthoringCliUsageError("--source-facts-compare requires --source-facts");
    }
    const control = await deriveSourceOperationRegistry(shared);
    await writeJson(compareSourceOperationReports(control, report), comparisonPath, context.stdout);
  }
  const diagnosticsPath = option(parsed, "--diagnostics");
  if (diagnosticsPath !== undefined) {
    await writeJson(
      { diagnostics: report.diagnostics, summary: report.summary },
      diagnosticsPath,
      context.stdout,
    );
  }
  await writeJson(report.registry, option(parsed, "--out"), context.stdout);
  return 0;
}

async function generateSourceFacts(options: {
  readonly astToolDirectory: string;
  readonly output: string;
  readonly sourceRoot: string;
  readonly environment: NodeJS.ProcessEnv;
}): Promise<void> {
  await mkdir(path.dirname(path.resolve(options.output)), { recursive: true });
  await new Promise<void>((resolve, reject) => {
    const child = spawn(
      "go",
      ["run", ".", "--source-root", options.sourceRoot, "--out", options.output],
      { cwd: options.astToolDirectory, env: options.environment, stdio: "inherit" },
    );
    child.once("error", (error) => reject(new Error(`failed to run source-evidence-ast: ${error.message}`)));
    child.once("exit", (status, signal) => {
      if (status === 0) resolve();
      else reject(new Error(
        signal === null
          ? `source-evidence-ast failed with exit code ${String(status)}`
          : `source-evidence-ast terminated by signal ${signal}`,
      ));
    });
  });
}

async function sourceEvidenceEvalCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(
    arguments_,
    new Set([
      "--ast-tool-dir", "--openapi", "--out-dir", "--provider-source",
      "--resource-prefix", "--resources", "--schema", "--source-facts", "--source-root",
    ]),
    new Set(),
    new Set(["--fail-on-regression"]),
  );
  if (parsed.positional.length !== 0) {
    throw new AuthoringCliUsageError("source-evidence-eval does not accept positional arguments");
  }
  const outputDirectory = requiredOption(parsed, "--out-dir");
  await mkdir(outputDirectory, { recursive: true });
  const paths = {
    astReport: path.join(outputDirectory, "ast-report.json"),
    compare: path.join(outputDirectory, "source-facts-compare.json"),
    controlReport: path.join(outputDirectory, "control-report.json"),
    evaluation: path.join(outputDirectory, "source-evidence-eval.json"),
    facts: path.join(outputDirectory, "source-facts.json"),
    markdown: path.join(outputDirectory, "source-evidence-eval.md"),
  };
  const sourceRoot = requiredOption(parsed, "--source-root");
  const suppliedFacts = option(parsed, "--source-facts");
  const factsPath = suppliedFacts ?? paths.facts;
  if (suppliedFacts === undefined) {
    await generateSourceFacts({
      astToolDirectory: option(parsed, "--ast-tool-dir")
        ?? path.join(context.repositoryRoot, "tools", "source-evidence-ast"),
      environment: context.environment,
      output: factsPath,
      sourceRoot,
    });
  }
  const providerSource = option(parsed, "--provider-source");
  const resources = resourceFilter(option(parsed, "--resources"));
  const shared = {
    openApi: await readOpenApiObject(requiredOption(parsed, "--openapi")),
    ...(providerSource === undefined ? {} : { providerSource }),
    resourcePrefix: option(parsed, "--resource-prefix") ?? "",
    ...(resources === undefined ? {} : { resources }),
    schemaData: await readObject(requiredOption(parsed, "--schema")),
    sourceRoot,
  };
  const control = await deriveSourceOperationRegistry(shared);
  const candidate = await deriveSourceOperationRegistry({
    ...shared,
    sourceFacts: await readObject(factsPath),
  });
  const comparison = compareSourceOperationReports(control, candidate);
  const evaluation = evaluateSourceEvidence(control, candidate, comparison);
  evaluation.artifacts = {
    ast_report: paths.astReport,
    compare: paths.compare,
    control_report: paths.controlReport,
    evaluation: paths.evaluation,
    markdown: paths.markdown,
    source_facts: factsPath,
  };
  await Promise.all([
    writeJson(control, paths.controlReport, context.stdout),
    writeJson(candidate, paths.astReport, context.stdout),
    writeJson(comparison, paths.compare, context.stdout),
    writeJson(evaluation, paths.evaluation, context.stdout),
    writeText(renderSourceEvidenceMarkdown(evaluation), paths.markdown),
  ]);
  await writeJson(evaluation, undefined, context.stdout);
  const summary = isObject(evaluation.summary) ? evaluation.summary : {};
  return parsed.flags.has("--fail-on-regression") && Number(summary.regressions ?? 0) > 0
    ? 1
    : 0;
}

function truthy(value: string | undefined): boolean {
  return ["1", "true", "yes", "on"].includes((value ?? "").trim().toLowerCase());
}

async function providerProbeCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(
    arguments_,
    new Set(["--markdown", "--out", "--work-dir"]),
    new Set(),
    new Set(["--debug-traceback"]),
  );
  if (parsed.positional.length !== 1) {
    throw new AuthoringCliUsageError("provider-probe requires one recipe JSON path");
  }
  try {
    const result = await runProviderProbe({
      recipe: parsed.positional[0] as string,
      ...(option(parsed, "--work-dir") === undefined
        ? {}
        : { workDirectory: option(parsed, "--work-dir") as string }),
    });
    const output = option(parsed, "--out");
    if (output !== undefined) await writeAuthoringJson(result.summary, output);
    const markdown = option(parsed, "--markdown");
    if (markdown !== undefined) {
      await writeText(renderProviderProbeMarkdown(result.summary), markdown);
    }
    context.stdout(`wrote ${String(result.artifacts.summary)}\n`);
    context.stdout(`wrote ${String(result.artifacts.markdown)}\n`);
    return 0;
  } catch (error: unknown) {
    const debug = parsed.flags.has("--debug-traceback")
      || truthy(context.environment.INFRAWRIGHT_DEBUG_TRACEBACK);
    if (debug && error instanceof Error && error.stack !== undefined) {
      context.stderr(`${error.stack}\n`);
    }
    context.stderr(`error: ${error instanceof Error ? error.message : String(error)}\n`);
    return 2;
  }
}

async function vendorBoundaryCommand(
  arguments_: readonly string[],
  context: AuthoringCliContext,
): Promise<number> {
  const parsed = parseArguments(arguments_, new Set(["--allowlist", "--root"]));
  if (parsed.positional.length !== 0) {
    throw new AuthoringCliUsageError("audit-vendor-boundary does not accept positional arguments");
  }
  const result = await runVendorBoundaryAudit({
    ...(option(parsed, "--allowlist") === undefined
      ? {}
      : { allowlist: option(parsed, "--allowlist") as string }),
    root: option(parsed, "--root") ?? context.repositoryRoot,
  });
  if (result.stdout !== "") context.stdout(result.stdout);
  if (result.stderr !== "") context.stderr(result.stderr);
  return result.exitCode;
}

export async function runAuthoringCommand(options: {
  readonly arguments: readonly string[];
  readonly command: string;
  readonly environment?: NodeJS.ProcessEnv;
  readonly repositoryRoot: string;
  readonly stderr?: (text: string) => void;
  readonly stdout?: (text: string) => void;
}): Promise<number> {
  const context: AuthoringCliContext = {
    environment: options.environment ?? process.env,
    repositoryRoot: options.repositoryRoot,
    stderr: options.stderr ?? ((text) => process.stderr.write(text)),
    stdout: options.stdout ?? ((text) => process.stdout.write(text)),
  };
  if (options.command === "reconcile") return reconcileCommand(options.arguments, context);
  if (options.command === "openapi-map") return openApiMapCommand(options.arguments, context);
  if (options.command === "source-operation-map") {
    return sourceOperationMapCommand(options.arguments, context);
  }
  if (options.command === "source-evidence-eval") {
    return sourceEvidenceEvalCommand(options.arguments, context);
  }
  if (options.command === "provider-probe") {
    return providerProbeCommand(options.arguments, context);
  }
  if (options.command === "audit-vendor-boundary") {
    return vendorBoundaryCommand(options.arguments, context);
  }
  throw new AuthoringCliUsageError(`unknown authoring command ${options.command}`);
}
