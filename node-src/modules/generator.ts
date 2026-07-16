import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import { manifestForProvider } from "../metadata/packs.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import {
  terraformAttributeType as attributeType,
  terraformAttributesForBlock as attributesForBlock,
  terraformBlockForSchema as blockForSchema,
  terraformBlockIsSingle as blockIsSingle,
  terraformBlockTypesForBlock as blockTypesForBlock,
  terraformBooleanField as booleanField,
  terraformClassifyAttributes as classifyAttributes,
  terraformInputBlockTypes as inputBlockTypes,
  terraformRequireObject as requireObject,
  terraformResourceInputAttributes as resourceInputAttributes,
} from "../metadata/terraform-schema.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";

export const EXPECTED_MODULE_FILES = Object.freeze([
  "main.tf",
  "variables.tf",
  "outputs.tf",
  "versions.tf",
  "README.md",
  "tests/defaults.tftest.hcl",
  "tests/sample.auto.tfvars.json",
] as const);

export type ModuleFileName = typeof EXPECTED_MODULE_FILES[number];
export type HclFormatter = (source: string) => Promise<string>;

export interface RenderedModule {
  readonly resourceType: string;
  readonly files: ReadonlyMap<ModuleFileName, string>;
}

export interface GeneratedModule {
  readonly resourceType: string;
  readonly files: readonly string[];
}

interface RenderContext {
  readonly resourceType: string;
  readonly provider: string;
  readonly providerSource: string;
  readonly providerPin: string;
  readonly schema: JsonObject;
  readonly mainOverride: string | null;
  readonly sampleOverride: JsonObject | null;
}

function schemaError(message: string): never {
  throw new TypeError(message);
}

function hclType(encoding: unknown, indent = 4): string {
  if (typeof encoding === "string") {
    if (encoding === "string" || encoding === "bool" || encoding === "number") {
      return encoding;
    }
    return schemaError(`unsupported primitive type encoding: ${JSON.stringify(encoding)}`);
  }
  if (!Array.isArray(encoding) || encoding.length !== 2) {
    return schemaError(`unsupported type encoding: ${JSON.stringify(encoding)}`);
  }
  const kind = encoding[0];
  const inner = encoding[1];
  if (kind === "object" && isObject(inner)) {
    const pad = " ".repeat(indent + 2);
    const lines = sortedStrings(Object.keys(inner)).map((name) => {
      return `${pad}${name} = optional(${hclType(inner[name], indent + 2)})`;
    });
    return `object({\n${lines.join("\n")}\n${" ".repeat(indent)}})`;
  }
  if (kind === "list" || kind === "set" || kind === "map") {
    if (typeof inner === "string") return `${kind}(${hclType(inner)})`;
    if (Array.isArray(inner)) return `${kind}(${hclType(inner, indent)})`;
  }
  return schemaError(`unsupported type encoding: ${JSON.stringify(encoding)}`);
}

function blockObjectType(block: JsonObject, indent: number, label: string): string {
  const classified = classifyAttributes(block, label);
  const attributes = attributesForBlock(block, label);
  const pad = " ".repeat(indent + 2);
  const lines: string[] = [];
  for (const name of [...classified.required, ...classified.optional]) {
    const attribute = requireObject(attributes[name], `${label}.attributes.${name}`);
    lines.push(`${pad}${name} = optional(${hclType(attributeType(attribute, `${label}.attributes.${name}`), indent + 2)})`);
  }
  for (const [name, blockType] of inputBlockTypes(block, label)) {
    lines.push(`${pad}${name} = optional(${blockInputType(blockType, indent + 2, `${label}.block_types.${name}`)})`);
  }
  return `object({\n${lines.join("\n")}\n${" ".repeat(indent)}})`;
}

function blockInputType(blockType: JsonObject, indent: number, label: string): string {
  const block = requireObject(blockType.block, `${label}.block`);
  const inner = blockObjectType(block, indent, `${label}.block`);
  const mode = blockType.nesting_mode;
  if (blockIsSingle(blockType)) return inner;
  if (mode === "list" || mode === "set") return `${mode}(${inner})`;
  return schemaError(`unsupported nesting_mode ${JSON.stringify(mode)}`);
}

function renderBlockBody(
  block: JsonObject,
  reference: string,
  indent: number,
  label: string,
  topLevel = false,
): string[] {
  const pad = " ".repeat(indent);
  const classified = topLevel
    ? resourceInputAttributes(block, label)
    : classifyAttributes(block, label);
  const lines = [...classified.required, ...classified.optional].map((name) => {
    return `${pad}${name} = ${reference}.${name}`;
  });
  for (const [name, blockType] of inputBlockTypes(block, label)) {
    const source = `${reference}.${name}`;
    const iterable = blockIsSingle(blockType)
      ? `${source} == null ? [] : [${source}]`
      : `${source} == null ? [] : ${source}`;
    const child = requireObject(blockType.block, `${label}.block_types.${name}.block`);
    lines.push("");
    lines.push(`${pad}dynamic "${name}" {`);
    lines.push(`${pad}  for_each = ${iterable}`);
    lines.push(`${pad}  content {`);
    lines.push(...renderBlockBody(
      child,
      `${name}.value`,
      indent + 4,
      `${label}.block_types.${name}.block`,
    ));
    lines.push(`${pad}  }`);
    lines.push(`${pad}}`);
  }
  return lines;
}

function encodingHasSensitive(encoding: unknown, attribute?: JsonObject): boolean {
  if (attribute !== undefined && booleanField(attribute, "sensitive")) return true;
  if (!Array.isArray(encoding) || encoding.length !== 2) return false;
  const kind = encoding[0];
  const inner = encoding[1];
  if (kind === "list" || kind === "set" || kind === "map") {
    return encodingHasSensitive(inner);
  }
  if (kind === "object" && isObject(inner)) {
    return Object.values(inner).some((member) => encodingHasSensitive(member));
  }
  return false;
}

function blockHasSensitive(block: JsonObject, label: string): boolean {
  const attributes = attributesForBlock(block, label);
  for (const name of Object.keys(attributes)) {
    const attribute = requireObject(attributes[name], `${label}.attributes.${name}`);
    if (encodingHasSensitive(attributeType(attribute, `${label}.attributes.${name}`), attribute)) {
      return true;
    }
  }
  const nested = blockTypesForBlock(block, label);
  return Object.keys(nested).some((name) => {
    const blockType = requireObject(nested[name], `${label}.block_types.${name}`);
    return blockHasSensitive(
      requireObject(blockType.block, `${label}.block_types.${name}.block`),
      `${label}.block_types.${name}.block`,
    );
  });
}

function header(provider: string): string {
  return `# GENERATED by iw modules generate from packs/${provider}/schemas/provider/${provider}.json — do not edit.\n# Regenerate: make gen-modules\n\n`;
}

function renderMain(context: RenderContext): string {
  if (context.mainOverride !== null) return context.mainOverride;
  const block = blockForSchema(context.schema, context.resourceType);
  const body = renderBlockBody(block, "each.value", 2, `${context.resourceType}.block`, true);
  return `${header(context.provider)}resource "${context.resourceType}" "this" {\n  for_each = var.items\n\n${body.join("\n")}\n}\n`;
}

function renderVariables(context: RenderContext): string {
  const block = blockForSchema(context.schema, context.resourceType);
  const classified = resourceInputAttributes(block, `${context.resourceType}.block`);
  const attributes = attributesForBlock(block, `${context.resourceType}.block`);
  const lines: string[] = [];
  for (const name of classified.required) {
    const attribute = requireObject(attributes[name], `${context.resourceType}.block.attributes.${name}`);
    lines.push(`    ${name} = ${hclType(attributeType(attribute, `${context.resourceType}.block.attributes.${name}`))}`);
  }
  for (const name of classified.optional) {
    const attribute = requireObject(attributes[name], `${context.resourceType}.block.attributes.${name}`);
    lines.push(`    ${name} = optional(${hclType(attributeType(attribute, `${context.resourceType}.block.attributes.${name}`))})`);
  }
  for (const [name, blockType] of inputBlockTypes(block, `${context.resourceType}.block`)) {
    lines.push(`    ${name} = optional(${blockInputType(blockType, 4, `${context.resourceType}.block.block_types.${name}`)})`);
  }
  return `${header(context.provider)}variable "items" {\n  description = "${context.resourceType} instances, keyed by a stable identifier."\n  type = map(object({\n${lines.join("\n")}\n  }))\n}\n`;
}

function emitsNameToId(schema: JsonObject, resourceType: string): boolean {
  const attributes = attributesForBlock(
    blockForSchema(schema, resourceType),
    `${resourceType}.block`,
  );
  const name = attributes.name;
  return name !== undefined
    && booleanField(requireObject(name, `${resourceType}.block.attributes.name`), "required")
    && Object.hasOwn(attributes, "id");
}

function renderOutputs(context: RenderContext): string {
  const block = blockForSchema(context.schema, context.resourceType);
  const attributes = attributesForBlock(block, `${context.resourceType}.block`);
  const deprecated = sortedStrings(Object.keys(attributes).filter((name) => {
    return booleanField(
      requireObject(attributes[name], `${context.resourceType}.block.attributes.${name}`),
      "deprecated",
    );
  }));
  const sensitiveLine = blockHasSensitive(block, `${context.resourceType}.block`)
    ? "  sensitive   = true\n"
    : "";
  let output: string;
  if (deprecated.length > 0) {
    const kept = sortedStrings(Object.keys(attributes).filter((name) => {
      return !booleanField(
        requireObject(attributes[name], `${context.resourceType}.block.attributes.${name}`),
        "deprecated",
      );
    }));
    const members = [
      ...kept,
      ...sortedStrings(Object.keys(blockTypesForBlock(block, `${context.resourceType}.block`))),
    ];
    const projection = members.map((member) => `      ${member} = v.${member}`).join("\n");
    output = `${header(context.provider)}output "items" {\n  description = "All managed ${context.resourceType} resources (excludes deprecated: ${deprecated.join(", ")}), keyed as in var.items."\n${sensitiveLine}  value = {\n    for k, v in ${context.resourceType}.this : k => {\n${projection}\n    }\n  }\n}\n`;
  } else {
    output = `${header(context.provider)}output "items" {\n  description = "All managed ${context.resourceType} resources, keyed as in var.items."\n${sensitiveLine}  value = ${context.resourceType}.this\n}\n`;
  }
  if (emitsNameToId(context.schema, context.resourceType)) {
    output += `\noutput "name_to_id" {\n  description = "Map of resource name to provider-assigned id."\n  value       = { for k, v in ${context.resourceType}.this : v.name => v.id... }\n}\n`;
  }
  return output;
}

function renderVersions(context: RenderContext): string {
  return `${header(context.provider)}terraform {\n  required_providers {\n    ${context.provider} = {\n      source = "${context.providerSource}"\n      version = "${context.providerPin}"\n    }\n  }\n}\n`;
}

function renderReadme(context: RenderContext): string {
  return `# ${context.resourceType} (generated module)\n\nManages \`${context.resourceType}\` via a typed \`items\` map. GENERATED — do not edit by\nhand (AGENTS.md rule 6). Regenerate with \`iw modules generate\` or \`make gen-modules\`.\n`;
}

function sampleValue(encoding: unknown): unknown {
  if (typeof encoding === "string") {
    if (encoding === "string") return "example";
    if (encoding === "bool") return true;
    if (encoding === "number") return 1;
    return "example";
  }
  if (Array.isArray(encoding) && encoding.length === 2) {
    const kind = encoding[0];
    const inner = encoding[1];
    if (kind === "list" || kind === "set") return [sampleValue(inner)];
    if (kind === "map") return { example: sampleValue(inner) };
    if (kind === "object" && isObject(inner)) {
      const output: JsonObject = {};
      for (const name of sortedStrings(Object.keys(inner))) {
        output[name] = sampleValue(inner[name]);
      }
      return output;
    }
  }
  return [];
}

function sampleItem(block: JsonObject, label: string): JsonObject {
  const item: JsonObject = {};
  const classified = classifyAttributes(block, label);
  const attributes = attributesForBlock(block, label);
  for (const name of classified.required) {
    const attribute = requireObject(attributes[name], `${label}.attributes.${name}`);
    item[name] = sampleValue(attributeType(attribute, `${label}.attributes.${name}`));
  }
  for (const [name, blockType] of inputBlockTypes(block, label)) {
    const minimum = blockType.min_items;
    if (typeof minimum === "number" && minimum >= 1) {
      const child = requireObject(blockType.block, `${label}.block_types.${name}.block`);
      const inner = sampleItem(child, `${label}.block_types.${name}.block`);
      item[name] = blockIsSingle(blockType) ? inner : [inner];
    }
  }
  return item;
}

function renderSample(context: RenderContext): string {
  const block = blockForSchema(context.schema, context.resourceType);
  const item = sampleItem(block, `${context.resourceType}.block`);
  if (context.sampleOverride !== null) Object.assign(item, context.sampleOverride);
  return renderPythonLosslessArtifactJson({ items: { example: item } });
}

function renderTest(context: RenderContext): string {
  return `# GENERATED smoke test — plan against a mocked provider; no credentials.\nmock_provider "${context.provider}" {}\n\nrun "defaults_plan" {\n  command = plan\n\n  assert {\n    condition     = length(var.items) == 1\n    error_message = "sample fixture must contain exactly one item"\n  }\n}\n`;
}

async function renderContext(
  root: LoadedPackRoot,
  resourceType: string,
): Promise<RenderContext> {
  const resource = root.resources.get(resourceType);
  if (resource === undefined) {
    throw new Error(`unknown active resource type ${JSON.stringify(resourceType)}`);
  }
  if (resource.registry.generate !== true) {
    throw new Error(`resource type ${JSON.stringify(resourceType)} is not generated`);
  }
  const manifest = manifestForProvider(root.packs, resource.provider);
  const providerSource = root.packs.providerSources[resource.provider];
  const ownerSource = manifest.providerSources[resource.provider];
  if (providerSource === undefined || ownerSource === undefined) {
    throw new Error(`provider ${JSON.stringify(resource.provider)} has no source in pack metadata`);
  }
  if (providerSource !== ownerSource) {
    throw new Error(`provider ${JSON.stringify(resource.provider)} has contradictory source metadata`);
  }
  const providerPin = manifest.data.pin;
  if (typeof providerPin !== "string" || providerPin.length === 0) {
    throw new Error(`provider ${JSON.stringify(resource.provider)} has no version pin in pack metadata`);
  }
  const sample = resource.override?.sample;
  if (sample !== undefined && !isObject(sample)) {
    throw new TypeError(`${resourceType} override sample must be an object`);
  }
  return {
    resourceType,
    provider: resource.provider,
    providerSource,
    providerPin,
    schema: requireObject(
      await root.loadResourceSchema(resourceType),
      `${resourceType} resource schema`,
    ),
    mainOverride: await root.loadResourceMainOverride(resourceType),
    sampleOverride: sample === undefined ? null : sample,
  };
}

export async function renderModuleFiles(
  root: LoadedPackRoot,
  resourceType: string,
): Promise<RenderedModule> {
  const context = await renderContext(root, resourceType);
  const files = new Map<ModuleFileName, string>([
    ["main.tf", renderMain(context)],
    ["variables.tf", renderVariables(context)],
    ["outputs.tf", renderOutputs(context)],
    ["versions.tf", renderVersions(context)],
    ["README.md", renderReadme(context)],
    ["tests/defaults.tftest.hcl", renderTest(context)],
    ["tests/sample.auto.tfvars.json", renderSample(context)],
  ]);
  return { resourceType, files };
}

export function activeGeneratedResourceTypes(root: LoadedPackRoot): readonly string[] {
  return sortedStrings([...root.resources.values()]
    .filter((resource) => resource.registry.generate === true)
    .map((resource) => resource.type));
}

export function terraformHclFormatter(options?: {
  readonly executable?: string;
  readonly environment?: NodeJS.ProcessEnv;
}): HclFormatter {
  const environment = options?.environment ?? process.env;
  const executable = options?.executable || environment.TF || "terraform";
  return async (source: string): Promise<string> => {
    return new Promise<string>((resolve, reject) => {
      const child = spawn(executable, ["fmt", "-"], {
        env: environment,
        stdio: ["pipe", "pipe", "pipe"],
      });
      const stdout: Buffer[] = [];
      const stderr: Buffer[] = [];
      child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk));
      child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
      child.once("error", reject);
      child.once("close", (code) => {
        if (code === 0) {
          resolve(Buffer.concat(stdout).toString("utf8"));
          return;
        }
        const detail = Buffer.concat(stderr).toString("utf8").trim();
        reject(new Error(
          `${executable} fmt failed with exit ${String(code)}${detail.length === 0 ? "" : `: ${detail}`}`,
        ));
      });
      child.stdin.end(Buffer.from(source, "utf8"));
    });
  };
}

function needsTerraformFormat(file: ModuleFileName): boolean {
  return file.endsWith(".tf") || file.endsWith(".tftest.hcl");
}

export async function generateModule(
  root: LoadedPackRoot,
  resourceType: string,
  options: {
    readonly outputRoot: string;
    readonly formatHcl: HclFormatter;
    readonly onWrite?: (path: string) => void;
  },
): Promise<GeneratedModule> {
  const rendered = await renderModuleFiles(root, resourceType);
  const base = path.join(options.outputRoot, resourceType);
  await mkdir(path.join(base, "tests"), { recursive: true });
  const written: string[] = [];
  for (const relative of sortedStrings(rendered.files.keys()) as ModuleFileName[]) {
    const source = rendered.files.get(relative);
    if (source === undefined) throw new Error(`renderer omitted ${relative}`);
    const output = needsTerraformFormat(relative)
      ? await options.formatHcl(source)
      : source;
    const destination = path.join(base, relative);
    await writeFile(destination, output, "utf8");
    written.push(destination);
    options.onWrite?.(destination);
  }
  return { resourceType, files: written };
}

export async function generateActiveModules(
  root: LoadedPackRoot,
  options: {
    readonly outputRoot: string;
    readonly formatHcl: HclFormatter;
    readonly onWrite?: (path: string) => void;
  },
): Promise<readonly GeneratedModule[]> {
  const generated: GeneratedModule[] = [];
  for (const resourceType of activeGeneratedResourceTypes(root)) {
    generated.push(await generateModule(root, resourceType, options));
  }
  return generated;
}

export async function validateGeneratedModuleTree(
  moduleRoot: string,
  resourceTypes: readonly string[],
): Promise<readonly string[]> {
  const { stat } = await import("node:fs/promises");
  const missing: string[] = [];
  for (const resourceType of resourceTypes) {
    for (const relative of EXPECTED_MODULE_FILES) {
      const candidate = path.join(moduleRoot, resourceType, relative);
      try {
        if (!(await stat(candidate)).isFile()) {
          missing.push(path.join(resourceType, relative));
        }
      } catch (error: unknown) {
        if (
          typeof error === "object"
          && error !== null
          && "code" in error
          && error.code === "ENOENT"
        ) {
          missing.push(path.join(resourceType, relative));
        } else {
          throw error;
        }
      }
    }
  }
  if (missing.length > 0) {
    const preview = missing.slice(0, 20).map((item) => `  - ${item}`).join("\n");
    const extra = missing.length > 20 ? `\n  ... ${missing.length - 20} more` : "";
    throw new Error(
      `generated module tree ${moduleRoot} is missing ${missing.length} expected file(s):\n${preview}${extra}`,
    );
  }
  return [...resourceTypes];
}
