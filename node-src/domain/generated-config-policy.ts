import { LosslessNumber } from "lossless-json";

import type { LoadedPackRoot } from "../metadata/loader.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import {
  terraformAttributesForBlock,
  terraformBlockForSchema,
  terraformBlockIsSingle,
  terraformInputBlockTypes,
  terraformRequireObject,
} from "../metadata/terraform-schema.js";
import { terraformJsonEqual } from "../json/python-equality.js";
import { canonicalPythonNumberToken } from "../json/python-number.js";
import { renderHclQuotedString, parseHclQuotedString } from "./import-moves.js";
import { parsePolicyPath, policySelectorMatches, type ConcretePathSegment } from "./policy-paths.js";
import { matchesTransformDefault } from "./pull-transform.js";
import { DriftPolicy, type PolicyEntry } from "./drift-policy.js";
import { projectionFillValue, providerSchemaStatus } from "./state-project.js";

type JsonRecord = Record<string, unknown>;

export class GeneratedConfigPolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "GeneratedConfigPolicyError";
  }
}

interface OmitEntry {
  readonly entry: PolicyEntry | null;
  readonly mode: "pack_drop_if_default" | "projection_omit" | "projection_omit_if";
  readonly selector: ReturnType<typeof parsePolicyPath>;
  readonly values?: readonly unknown[];
}

function record(value: unknown): value is JsonRecord {
  return isObject(value) && !(value instanceof LosslessNumber);
}

function exactIndex(path: ReturnType<typeof parsePolicyPath>): boolean {
  return path.some((segment) => typeof segment === "number" || typeof segment === "bigint");
}

async function policyEntries(options: {
  readonly policy: DriftPolicy | null;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
}): Promise<{ readonly fills: readonly PolicyEntry[]; readonly omits: readonly OmitEntry[] }> {
  const override = options.root.resources?.get(options.resourceType)?.override;
  const dropDefaults = record(override?.drop_if_default)
    ? override.drop_if_default
    : {};
  const fills = options.policy?.entries(options.resourceType, "projection_fill") ?? [];
  const policyOmits = options.policy === null
    ? []
    : [
      ...options.policy.entries(options.resourceType, "projection_omit"),
      ...options.policy.entries(options.resourceType, "projection_omit_if"),
    ];
  if (fills.length === 0 && policyOmits.length === 0 && Object.keys(dropDefaults).length === 0) {
    return { fills: [], omits: [] };
  }
  const schema = await options.root.loadResourceSchema(options.resourceType);
  const omits: OmitEntry[] = [];
  if (options.policy !== null) {
    for (const mode of ["projection_omit", "projection_omit_if"] as const) {
      for (const entry of options.policy.entries(options.resourceType, mode)) {
        const selector = parsePolicyPath(entry.path);
        if (providerSchemaStatus({ path: selector, resourceType: options.resourceType, schema }) === "required") {
          throw new GeneratedConfigPolicyError(
            `${options.resourceType} generated import config policy cannot ${mode} required path ${String(entry.path)}`,
          );
        }
        if (!exactIndex(selector)) omits.push({ entry, mode, selector });
      }
    }
  }
  for (const path of Object.keys(dropDefaults).sort()) {
    const selector = parsePolicyPath(path);
    const status = providerSchemaStatus({ path: selector, resourceType: options.resourceType, schema });
    if (status !== "optional") {
      throw new GeneratedConfigPolicyError(
        `${options.resourceType} generated import config pack drop_if_default path ${path} is not optional (schema status ${status})`,
      );
    }
    if (!exactIndex(selector)) {
      omits.push({
        entry: null,
        mode: "pack_drop_if_default",
        selector,
        values: [dropDefaults[path]],
      });
    }
  }
  return {
    fills,
    omits,
  };
}

function parsedScalar(raw: string): { readonly known: boolean; readonly value?: unknown } {
  const text = raw.trim();
  if (text.startsWith('"')) {
    try {
      const parsed = parseHclQuotedString(text);
      return text.slice(parsed.end).trim().length === 0
        ? { known: true, value: parsed.value }
        : { known: false };
    } catch {
      return { known: false };
    }
  }
  if (text === "true") return { known: true, value: true };
  if (text === "false") return { known: true, value: false };
  if (text === "null") return { known: true, value: null };
  if (/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$/u.test(text)) {
    return { known: true, value: new LosslessNumber(text) };
  }
  return { known: false };
}

function heredocEnd(raw: string): string | null {
  return /^<<-?\s*([A-Za-z_][A-Za-z0-9_]*)$/u.exec(raw.trim())?.[1] ?? null;
}

function valueDepthDelta(text: string): number {
  let depth = 0;
  let escaped = false;
  let inString = false;
  for (let index = 0; index < text.length; index += 1) {
    const character = text[index] ?? "";
    if (escaped) {
      escaped = false;
      continue;
    }
    if (inString) {
      if (character === "\\") escaped = true;
      else if (character === '"') inString = false;
      continue;
    }
    if (character === '"') inString = true;
    else if (character === "#") break;
    else if (character === "/" && text[index + 1] === "/") break;
    else if ("{[(".includes(character)) depth += 1;
    else if ("}])".includes(character)) depth -= 1;
  }
  return depth;
}

function matchingOmit(
  path: readonly ConcretePathSegment[],
  parsed: { readonly known: boolean; readonly value?: unknown },
  entries: readonly OmitEntry[],
  resourceType: string,
  schema: Readonly<JsonObject>,
): OmitEntry | null {
  if (!parsed.known) return null;
  for (const candidate of entries) {
    const matches = candidate.mode === "pack_drop_if_default"
      ? policySelectorMatches(
        candidate.selector,
        path.filter((segment) => typeof segment !== "number"),
      )
      : policySelectorMatches(candidate.selector, path);
    if (!matches) continue;
    const status = providerSchemaStatus({
      path: candidate.selector,
      resourceType,
      schema,
    });
    if (status !== "optional") {
      const pathLabel = candidate.entry?.path ?? candidate.selector.join(".");
      throw new GeneratedConfigPolicyError(
        `${resourceType} generated import config policy matched non-optional path ${String(pathLabel)} (schema status ${status})`,
      );
    }
    if (candidate.mode === "projection_omit") return candidate;
    const values = candidate.mode === "pack_drop_if_default"
      ? candidate.values ?? []
      : Array.isArray(candidate.entry?.values) ? candidate.entry.values : [];
    const matchesValue = candidate.mode === "pack_drop_if_default"
      ? values.some((value) => matchesTransformDefault(parsed.value, value))
      : values.some((value) => terraformJsonEqual(parsed.value, value));
    if (matchesValue) return candidate;
  }
  return null;
}

interface StackEntry {
  readonly address?: string;
  readonly counts: Map<string, number>;
  readonly kind: "block" | "resource";
  readonly path: readonly ConcretePathSegment[];
  readonly present?: Set<string>;
  readonly resourceType?: string;
}

function resourceStart(line: string): { readonly address: string; readonly type: string } | null {
  const match = /^resource\s+"([^"]+)"\s+"([^"]+)"\s*\{\s*$/u.exec(line.trim());
  return match === null ? null : { address: `${match[1]}.${match[2]}`, type: match[1] ?? "" };
}

function blockStart(line: string): string | null {
  return /^([A-Za-z_][A-Za-z0-9_]*)\s*\{\s*$/u.exec(line.trim())?.[1] ?? null;
}

function attributeLine(line: string): { readonly name: string; readonly value: string } | null {
  const match = /^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$/u.exec(line.trim());
  return match === null ? null : { name: match[1] ?? "", value: match[2] ?? "" };
}

function renderHclValue(value: unknown, indent: number): string {
  if (value === null || value === undefined) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "string") return renderHclQuotedString(value);
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token === null) throw new GeneratedConfigPolicyError("cannot render non-finite projection_fill number");
    return token;
  }
  if (typeof value === "number" && Number.isFinite(value)) return JSON.stringify(value);
  if (Array.isArray(value)) {
    if (value.length === 0) return "[]";
    const child = " ".repeat(indent + 2);
    return `[\n${value.map((item) => `${child}${renderHclValue(item, indent + 2)},\n`).join("")}${" ".repeat(indent)}]`;
  }
  if (record(value)) {
    if (Object.keys(value).length === 0) return "{}";
    const child = " ".repeat(indent + 2);
    return `{\n${Object.keys(value).sort().map((key) => {
      return `${child}${renderHclQuotedString(key)} = ${renderHclValue(value[key], indent + 2)}\n`;
    }).join("")}${" ".repeat(indent)}}`;
  }
  throw new GeneratedConfigPolicyError("cannot render projection_fill value for generated config");
}

function renderHclBlock(
  name: string,
  block: JsonObject,
  value: JsonRecord,
  indent: number,
): string[] {
  const pad = " ".repeat(indent);
  const output = [`${pad}${name} {\n`];
  const attributes = terraformAttributesForBlock(block, name);
  const blocks = terraformInputBlockTypes(block, name);
  for (const key of Object.keys(value).sort()) {
    if (Object.hasOwn(attributes, key)) {
      output.push(`${" ".repeat(indent + 2)}${key} = ${renderHclValue(value[key], indent + 2)}\n`);
      continue;
    }
    const childType = blocks.get(key);
    if (childType === undefined) continue;
    const rawChildren = terraformBlockIsSingle(childType) ? [value[key]] : value[key];
    if (!Array.isArray(rawChildren)) continue;
    const childBlock = terraformRequireObject(childType.block, `${name}.${key}.block`);
    for (const child of rawChildren) {
      if (record(child) && Object.keys(child).length > 0) {
        output.push(...renderHclBlock(key, childBlock, child, indent + 2));
      }
    }
  }
  output.push(`${pad}}\n`);
  return output;
}

async function fillForResource(options: {
  readonly address: string;
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly fills: readonly PolicyEntry[];
  readonly policy: DriftPolicy | null;
  readonly present: Set<string>;
  readonly rawItems: ReadonlyMap<string, Readonly<Record<string, unknown>>> | null;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}): Promise<{ readonly count: number; readonly lines: readonly string[] }> {
  if (options.fills.length === 0) return { count: 0, lines: [] };
  if (options.policy === null) {
    throw new GeneratedConfigPolicyError(`${options.resourceType} generated import config policy entries require a policy`);
  }
  const key = options.addressToKey.get(options.address);
  if (key === undefined) {
    throw new GeneratedConfigPolicyError(`${options.resourceType} generated import config missing key for ${options.address}`);
  }
  const raw = options.rawItems?.get(key);
  if (raw === undefined) {
    throw new GeneratedConfigPolicyError(`${options.resourceType} generated import config projection_fill missing raw item for key ${key}`);
  }
  const block = terraformBlockForSchema(options.schema as JsonObject, options.resourceType);
  const blocks = terraformInputBlockTypes(block, options.resourceType);
  const attributes = terraformAttributesForBlock(block, options.resourceType);
  const lines: string[] = [];
  let count = 0;
  for (const entry of options.fills) {
    const before = lines.length;
    const target = parsePolicyPath(entry.path)[0];
    if (typeof target !== "string" || options.present.has(target)) continue;
    const value = projectionFillValue({
      entry,
      rawItem: raw,
      resourceType: options.resourceType,
      schema: options.schema,
    });
    if (value === undefined) continue;
    if (Object.hasOwn(attributes, target)) {
      lines.push(`  ${target} = ${renderHclValue(value, 2)}\n`);
    } else {
      const blockType = blocks.get(target);
      if (blockType === undefined) {
        throw new GeneratedConfigPolicyError(`${options.resourceType} projection_fill target ${target} is not a writable input`);
      }
      const values = terraformBlockIsSingle(blockType) ? [value] : value;
      if (!Array.isArray(values)) {
        throw new GeneratedConfigPolicyError(`${options.resourceType} projection_fill block ${target} did not shape to a list`);
      }
      const childBlock = terraformRequireObject(blockType.block, `${options.resourceType}.${target}.block`);
      for (const child of values) {
        if (record(child) && Object.keys(child).length > 0) {
          lines.push(...renderHclBlock(target, childBlock, child, 2));
        }
      }
    }
    if (lines.length > before) {
      options.present.add(target);
      options.policy.markMatched(entry);
      count += 1;
    }
  }
  return { count, lines };
}

interface RewriteResourceOptions {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly fills: readonly PolicyEntry[];
  readonly omits: readonly OmitEntry[];
  readonly policy: DriftPolicy | null;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
}

interface RewriteOptions {
  readonly generatedConfig: string;
  readonly resources: ReadonlyMap<string, RewriteResourceOptions>;
}

function generatedConfigContext(resources: ReadonlyMap<string, RewriteResourceOptions>): string {
  return resources.size === 1 ? `${[...resources.keys()][0]} ` : "";
}

async function rewriteGeneratedConfig(options: RewriteOptions): Promise<{
  readonly edits: number;
  readonly text: string;
}> {
  const context = generatedConfigContext(options.resources);
  const lines = options.generatedConfig.match(/.*(?:\n|$)/gu)?.filter((line) => line.length > 0) ?? [];
  const output: string[] = [];
  const stack: StackEntry[] = [];
  const seen = new Set<string>();
  let heredoc: string | null = null;
  let valueDepth = 0;
  let edits = 0;
  for (const line of lines) {
    const stripped = line.trim();
    if (heredoc !== null) {
      output.push(line);
      if (stripped === heredoc) heredoc = null;
      continue;
    }
    if (valueDepth !== 0) {
      output.push(line);
      valueDepth += valueDepthDelta(stripped);
      if (valueDepth <= 0) valueDepth = 0;
      continue;
    }
    const resource = resourceStart(line);
    if (resource !== null) {
      const resourceOptions = options.resources.get(resource.type);
      if (resourceOptions === undefined || !resourceOptions.addressToKey.has(resource.address)) {
        throw new GeneratedConfigPolicyError(`${context}generated import config contained unexpected resource block ${resource.address}`);
      }
      if (seen.has(resource.address)) {
        throw new GeneratedConfigPolicyError(`${context}generated import config contained duplicate resource block ${resource.address}`);
      }
      seen.add(resource.address);
      stack.push({
        address: resource.address,
        counts: new Map(),
        kind: "resource",
        path: [],
        present: new Set(),
        resourceType: resource.type,
      });
      output.push(line);
      continue;
    }
    if (stripped === "}") {
      const current = stack.at(-1);
      if (stack.length === 1 && current?.kind === "resource") {
        const resourceType = current.resourceType ?? "";
        const resourceOptions = options.resources.get(resourceType);
        if (resourceOptions === undefined) {
          throw new GeneratedConfigPolicyError(`generated import config contained unknown sibling resource type ${resourceType}`);
        }
        if (resourceOptions.policy === null && resourceOptions.fills.length > 0) {
          throw new GeneratedConfigPolicyError(`${resourceType} generated import config policy entries require a policy`);
        }
        const fill = await fillForResource({
          address: current.address ?? "",
          addressToKey: resourceOptions.addressToKey,
          fills: resourceOptions.fills,
          policy: resourceOptions.policy,
          present: current.present ?? new Set(),
          rawItems: resourceOptions.rawItems ?? null,
          resourceType,
          schema: resourceOptions.schema,
        });
        output.push(...fill.lines);
        edits += fill.count;
      }
      stack.pop();
      output.push(line);
      continue;
    }
    const block = blockStart(line);
    if (block !== null && stack.length > 0) {
      const parent = stack.at(-1) as StackEntry;
      if (stack.length === 1) parent.present?.add(block);
      const index = parent.counts.get(block) ?? 0;
      parent.counts.set(block, index + 1);
      stack.push({ counts: new Map(), kind: "block", path: [...parent.path, block, index] });
      output.push(line);
      continue;
    }
    const attribute = attributeLine(line);
    if (attribute !== null && stack.length > 0 && stack[0]?.kind === "resource") {
      const resourceType = stack[0].resourceType ?? "";
      const resourceOptions = options.resources.get(resourceType);
      if (resourceOptions === undefined) {
        throw new GeneratedConfigPolicyError(`generated import config contained unknown sibling resource type ${resourceType}`);
      }
      if (stack.length === 1) stack[0].present?.add(attribute.name);
      const path = [...(stack.at(-1)?.path ?? []), attribute.name];
      const match = matchingOmit(
        path,
        parsedScalar(attribute.value),
        resourceOptions.omits,
        resourceType,
        resourceOptions.schema,
      );
      if (match !== null) {
        if (match.entry !== null) resourceOptions.policy?.markMatched(match.entry);
        edits += 1;
        continue;
      }
      heredoc = heredocEnd(attribute.value);
      if (heredoc === null) valueDepth = Math.max(0, valueDepthDelta(attribute.value));
    }
    output.push(line);
  }
  const missing = [...options.resources.values()].flatMap((resource) => {
    return [...resource.addressToKey.keys()].filter((address) => !seen.has(address));
  }).sort();
  if (missing.length > 0) {
    throw new GeneratedConfigPolicyError(`${context}generated import config missing resource block(s): ${missing.join(", ")}`);
  }
  return { edits, text: output.join("") };
}

export interface GeneratedConfigPolicyResource {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly policy: DriftPolicy | null;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
}

/** Apply every resource's fill first, then omit/omit-if, in one generated file. */
export async function applyGeneratedConfigPolicies(options: {
  readonly generatedConfig: string;
  readonly resources: readonly GeneratedConfigPolicyResource[];
  readonly root: LoadedPackRoot;
}): Promise<{ readonly edits: number; readonly text: string }> {
  const resources = new Map<string, RewriteResourceOptions>();
  for (const resource of [...options.resources].sort((left, right) => {
    return left.resourceType.localeCompare(right.resourceType);
  })) {
    if (resources.has(resource.resourceType)) {
      throw new GeneratedConfigPolicyError(`duplicate generated-config policy resource type ${resource.resourceType}`);
    }
    const entries = await policyEntries({
      policy: resource.policy,
      resourceType: resource.resourceType,
      root: options.root,
    });
    if (entries.fills.length > 0 && resource.rawItems === undefined) {
      throw new GeneratedConfigPolicyError(`${resource.resourceType} generated import config projection_fill requires raw_items`);
    }
    resources.set(resource.resourceType, {
      addressToKey: resource.addressToKey,
      fills: entries.fills,
      omits: entries.omits,
      policy: resource.policy,
      ...(resource.rawItems === undefined ? {} : { rawItems: resource.rawItems }),
      resourceType: resource.resourceType,
      schema: entries.fills.length > 0 || entries.omits.length > 0
        ? await options.root.loadResourceSchema(resource.resourceType)
        : {},
    });
  }
  const hasFills = [...resources.values()].some((resource) => resource.fills.length > 0);
  const hasOmits = [...resources.values()].some((resource) => resource.omits.length > 0);
  if (!hasFills && !hasOmits) {
    if (resources.size > 1 && options.generatedConfig.length > 0) {
      return rewriteGeneratedConfig({ generatedConfig: options.generatedConfig, resources });
    }
    return { edits: 0, text: options.generatedConfig };
  }
  if (options.generatedConfig.length === 0) {
    throw new GeneratedConfigPolicyError(`${generatedConfigContext(resources)}generated import config is missing; projection policy cannot be applied safely`);
  }
  let text = options.generatedConfig;
  let edits = 0;
  if (hasFills) {
    const filled = await rewriteGeneratedConfig({
      generatedConfig: text,
      resources: new Map([...resources].map(([resourceType, resource]) => [resourceType, {
        ...resource,
        omits: [],
      }])),
    });
    text = filled.text;
    edits += filled.edits;
  }
  if (hasOmits) {
    const omitted = await rewriteGeneratedConfig({
      generatedConfig: text,
      resources: new Map([...resources].map(([resourceType, resource]) => [resourceType, {
        ...resource,
        fills: [],
      }])),
    });
    text = omitted.text;
    edits += omitted.edits;
  }
  return { edits, text };
}

/** Apply fill first, then omit/omit-if for one resource. */
export async function applyGeneratedConfigPolicy(options: {
  readonly addressToKey: ReadonlyMap<string, string>;
  readonly generatedConfig: string;
  readonly policy: DriftPolicy | null;
  readonly rawItems?: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
}): Promise<{ readonly edits: number; readonly text: string }> {
  return applyGeneratedConfigPolicies({
    generatedConfig: options.generatedConfig,
    resources: [{
      addressToKey: options.addressToKey,
      policy: options.policy,
      ...(options.rawItems === undefined ? {} : { rawItems: options.rawItems }),
      resourceType: options.resourceType,
    }],
    root: options.root,
  });
}
