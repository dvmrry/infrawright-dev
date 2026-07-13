import { LosslessNumber } from "lossless-json";
import { mkdir, unlink, writeFile } from "node:fs/promises";
import path from "node:path";

import {
  deriveImportMoves,
  renderGeneratedImports,
  renderHclQuotedString,
  renderMovedBlocks,
} from "./import-moves.js";
import type { PullTransformResult } from "./pull-transform.js";
import type { Deployment } from "./types.js";
import {
  deploymentConfigDir,
  deploymentImportsDir,
  deploymentTfvarsFormat,
} from "./deployment.js";
import {
  hclTfvarsCommentKey,
  renderTfvarsHcl,
  type HclTfvarsComments,
} from "./hcl-tfvars.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { canonicalPythonNumberToken } from "../json/python-number.js";
import { sortedStrings } from "../json/python-compatible.js";
import { readOptionalUtf8 } from "../io/files.js";

type JsonRecord = Record<string, unknown>;

export interface TransformReferenceSpec {
  readonly name_field: string;
  readonly referent: string;
}

export interface BindingContext {
  readonly bindReferences: boolean;
  readonly derived: ReadonlySet<string>;
  readonly generated: ReadonlySet<string>;
  readonly resourceRoots: Readonly<Record<string, string>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}

export interface GeneratedBindingsResult {
  readonly data: Readonly<{ readonly resources: Readonly<Record<string, unknown>> }>;
  readonly notes: readonly string[];
}

export interface TransformArtifactPaths {
  readonly config: string;
  readonly generatedBindings: string;
  readonly imports: string;
  readonly lookup: string;
  readonly moves: string;
  readonly staleConfig: string;
}

export interface TransformArtifactWriteResult {
  readonly paths: TransformArtifactPaths;
  readonly written: readonly string[];
  readonly removed: readonly string[];
}

function record(entries: Iterable<readonly [string, unknown]>): JsonRecord {
  return Object.fromEntries(entries) as JsonRecord;
}

function own(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function object(value: unknown): JsonRecord | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as JsonRecord
    : null;
}

/** Match the scalar spelling used by Python str() in transform identities. */
export function pythonTransformString(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "boolean") return value ? "True" : "False";
  if (value === null) return "None";
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token !== null) return token;
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) {
    return Object.is(value, -0) ? "0" : String(value);
  }
  throw new TypeError("transform identity must be a scalar JSON value");
}

function formatImportTemplate(
  template: string,
  original: Readonly<Record<string, unknown>>,
): string {
  return template.replace(/\{([^{}]+)\}/gu, (_match, field: string) => {
    if (!own(original, field)) {
      throw new TypeError(
        `import_id template ${JSON.stringify(template)} references missing field ${JSON.stringify(field)}`,
      );
    }
    return pythonTransformString(original[field]);
  });
}

export function renderTransformImports(options: {
  readonly resourceType: string;
  readonly originals: PullTransformResult["originals"];
  readonly template?: string;
}): string {
  const template = options.template ?? "{id}";
  const pairs = sortedStrings(Object.keys(options.originals)).map((key) => {
    const original = options.originals[key];
    if (original === undefined) {
      throw new TypeError(`missing original transform item ${JSON.stringify(key)}`);
    }
    return { key, importId: formatImportTemplate(template, original) };
  });
  return renderGeneratedImports(options.resourceType, pairs);
}

function lookupIdentity(value: unknown): string | null {
  if (value === null || value === undefined || value === "") return null;
  return pythonTransformString(value);
}

/** Render Python's transform lookup sidecar, including last-key-wins IDs. */
export function renderTransformLookup(options: {
  readonly items: PullTransformResult["items"];
  readonly originals: PullTransformResult["originals"];
  readonly nameField: string;
}): string {
  const byId: JsonRecord = Object.create(null) as JsonRecord;
  const keyById: JsonRecord = Object.create(null) as JsonRecord;
  for (const key of sortedStrings(Object.keys(options.items))) {
    const projected = options.items[key];
    if (projected === undefined) continue;
    const merged = record([
      ...Object.entries(options.originals[key] ?? {}),
      ...Object.entries(projected),
    ]);
    const ident = lookupIdentity(merged.id);
    if (ident === null) continue;
    const display = merged[options.nameField];
    byId[ident] = typeof display === "string" && display.trim().length > 0
      ? display
      : "<unknown>";
    keyById[ident] = key;
  }
  const payload = Object.keys(keyById).length === 0
    ? byId
    : record([["by_id", byId], ["key_by_id", keyById]]);
  return renderPythonLosslessArtifactJson(payload);
}

export function parseLookupSidecar(value: unknown): {
  readonly byId: Readonly<Record<string, string>>;
  readonly keyById: Readonly<Record<string, string>>;
} {
  const root = object(value);
  if (root === null) throw new TypeError("lookup sidecar must contain a JSON object");
  const nestedById = object(root.by_id);
  const nestedKeys = object(root.key_by_id);
  const rawById = nestedById ?? root;
  const byId: Record<string, string> = Object.create(null) as Record<string, string>;
  const keyById: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const [key, display] of Object.entries(rawById)) {
    byId[String(key)] = typeof display === "string" ? display : "<unknown>";
  }
  if (nestedKeys !== null) {
    for (const [key, itemKey] of Object.entries(nestedKeys)) {
      if (typeof itemKey === "string" && itemKey.length > 0) keyById[String(key)] = itemKey;
    }
  }
  return { byId, keyById };
}

function integerToken(value: unknown): string | null {
  if (value instanceof LosslessNumber && /^-?(?:0|[1-9][0-9]*)$/u.test(value.toString())) {
    return BigInt(value.toString()).toString();
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) return String(value);
  return null;
}

function zeroSentinel(value: unknown): boolean {
  const token = integerToken(value);
  return token !== null && BigInt(token) === 0n;
}

function bindableListElement(value: unknown): boolean {
  return (typeof value === "string" && value.length > 0)
    || integerToken(value) !== null;
}

function sameRoot(
  resourceType: string,
  referent: string,
  context: BindingContext,
): boolean {
  return resourceType !== referent
    && context.generated.has(resourceType)
    && context.generated.has(referent)
    && !context.derived.has(resourceType)
    && !context.derived.has(referent)
    && context.resourceRoots[resourceType] !== undefined
    && context.resourceRoots[resourceType] === context.resourceRoots[referent];
}

function fieldCandidates(
  items: PullTransformResult["items"],
  field: string,
): readonly { readonly key: string; readonly path: string; readonly value: unknown }[] {
  const candidates: Array<{ key: string; path: string; value: unknown }> = [];
  for (const key of sortedStrings(Object.keys(items))) {
    const item = items[key];
    if (item === undefined || !own(item, field)) continue;
    const value = item[field];
    if (value === null || value === undefined) continue;
    if (Array.isArray(value)) {
      value.forEach((child, index) => {
        if (child !== null && child !== undefined) {
          candidates.push({ key, path: `${field}[${index}]`, value: child });
        }
      });
    } else {
      candidates.push({ key, path: field, value });
    }
  }
  return candidates;
}

/** Pure same-root binding derivation; lookup reads stay in the caller. */
export function deriveGeneratedBindings(options: {
  readonly context: BindingContext;
  readonly items: PullTransformResult["items"];
  readonly lookupKeys: Readonly<Record<string, Readonly<Record<string, string>> | null>>;
  readonly resourceType: string;
}): GeneratedBindingsResult {
  const resources: Record<string, Record<string, unknown>> = Object.create(null) as Record<
    string,
    Record<string, unknown>
  >;
  const notes: string[] = [];
  let bound = 0;
  let skipped = 0;
  const reasons = new Map<string, number>();
  const count = (reason: string, amount = 1): void => {
    reasons.set(reason, (reasons.get(reason) ?? 0) + amount);
  };
  if (!options.context.bindReferences) {
    return { data: { resources }, notes };
  }
  for (const field of sortedStrings(Object.keys(options.context.references))) {
    const spec = options.context.references[field];
    if (spec === undefined) continue;
    const candidates = fieldCandidates(options.items, field);
    if (field.includes(".")) {
      if (candidates.length > 0) {
        count("nested_field_unsupported", candidates.length);
        skipped += candidates.length;
        notes.push(`${options.resourceType}.${field} skipped; nested reference fields are unsupported`);
      }
      continue;
    }
    if (options.resourceType === spec.referent) {
      if (candidates.length > 0) {
        count("self_reference", candidates.length);
        skipped += candidates.length;
        notes.push(`${options.resourceType}.${field} skipped; self-referential bindings would create a Terraform cycle`);
      }
      continue;
    }
    if (!sameRoot(options.resourceType, spec.referent, options.context)) continue;
    const keyMap = options.lookupKeys[spec.referent];
    if (keyMap === null || keyMap === undefined) {
      if (candidates.length > 0) {
        count("missing_lookup", candidates.length);
        skipped += candidates.length;
        notes.push(`${options.resourceType}.${field} skipped; lookup for ${spec.referent} is missing`);
      }
      continue;
    }
    if (Object.keys(keyMap).length === 0) {
      if (candidates.length > 0) {
        count("key_map_unavailable", candidates.length);
        skipped += candidates.length;
        notes.push(`${options.resourceType}.${field} skipped; lookup for ${spec.referent} has no key_by_id map`);
      }
      continue;
    }
    const resolve = (key: string, path: string, value: unknown): string | null => {
      const ident = pythonTransformString(value);
      const referentKey = keyMap[ident];
      if (referentKey === undefined) {
        count("id_absent");
        notes.push(`${options.resourceType}.${key}.${path} value ${JSON.stringify(ident)} skipped; id is absent from ${spec.referent} lookup`);
        return null;
      }
      if (referentKey.includes("${") || referentKey.includes("%{")) {
        count("unsafe_key");
        notes.push(`${options.resourceType}.${key}.${path} value ${JSON.stringify(ident)} skipped; referent key contains a template interpolation`);
        return null;
      }
      return `module.${spec.referent}.items[${renderHclQuotedString(referentKey)}].id`;
    };
    for (const key of sortedStrings(Object.keys(options.items))) {
      const item = options.items[key];
      if (item === undefined || !own(item, field)) continue;
      const value = item[field];
      let expression: string | null = null;
      if (Array.isArray(value)) {
        const bindable = value.filter((child) => !zeroSentinel(child));
        const hadZero = bindable.length !== value.length;
        if (!bindable.every(bindableListElement)) {
          count("unbindable_list");
          skipped += 1;
          notes.push(`${options.resourceType}.${key}.${field} skipped; list has null or unbindable elements`);
          continue;
        }
        const fragments: string[] = [];
        let boundAny = false;
        value.forEach((child, index) => {
          if (zeroSentinel(child)) return;
          const resolved = resolve(key, `${field}[${index}]`, child);
          if (resolved === null) {
            skipped += 1;
            fragments.push(renderHclQuotedString(pythonTransformString(child)));
          } else {
            bound += 1;
            boundAny = true;
            fragments.push(resolved);
          }
        });
        if (boundAny) expression = `[${fragments.join(", ")}]`;
        else if (hadZero && bindable.length === 0) expression = "[]";
      } else if (value !== null && value !== undefined) {
        expression = resolve(key, field, value);
        if (expression === null) skipped += 1;
        else bound += 1;
      }
      if (expression === null) continue;
      const address = `${options.resourceType}.${key}`;
      const fields = resources[address]
        ?? (resources[address] = Object.create(null) as Record<string, unknown>);
      fields[field] = {
        expression,
        reason: `group-local reference binding via ${spec.referent}.items`,
      };
    }
  }
  if (bound > 0 || skipped > 0) {
    const reasonText = sortedStrings(reasons.keys())
      .map((reason) => `${reason}=${String(reasons.get(reason))}`)
      .join(", ");
    notes.push(
      `${options.resourceType}: ${bound} bound, ${skipped} skipped${reasonText.length === 0 ? "" : ` (${reasonText})`}`,
    );
  }
  return { data: { resources }, notes };
}

export function renderGeneratedBindings(data: GeneratedBindingsResult["data"]): string {
  return renderPythonLosslessArtifactJson(data);
}

export function transformArtifactPaths(options: {
  readonly deployment: Deployment;
  readonly resourceType: string;
  readonly tenant: string;
}): TransformArtifactPaths {
  const format = deploymentTfvarsFormat(options.deployment);
  const configDirectory = deploymentConfigDir(options.deployment, options.tenant);
  const importsDirectory = deploymentImportsDir(options.deployment, options.tenant);
  const config = path.join(
    configDirectory,
    `${options.resourceType}${format === "hcl" ? ".auto.tfvars" : ".auto.tfvars.json"}`,
  );
  return {
    config,
    staleConfig: format === "hcl" ? `${config}.json` : config.slice(0, -".json".length),
    generatedBindings: path.join(
      configDirectory,
      `${options.resourceType}.generated.expressions.json`,
    ),
    imports: path.join(importsDirectory, `${options.resourceType}_imports.tf`),
    lookup: path.join(configDirectory, `${options.resourceType}.lookup.json`),
    moves: path.join(importsDirectory, `${options.resourceType}_moves.tf`),
  };
}

async function removeIfPresent(file: string): Promise<boolean> {
  try {
    await unlink(file);
    return true;
  } catch (error: unknown) {
    if (
      typeof error === "object"
      && error !== null
      && "code" in error
      && error.code === "ENOENT"
    ) {
      return false;
    }
    throw error;
  }
}

async function loadLookup(file: string): Promise<ReturnType<typeof parseLookupSidecar> | null> {
  const text = await readOptionalUtf8(file, `lookup for ${path.basename(file)}`);
  if (text === null) return null;
  return parseLookupSidecar(parseDataJsonLosslessly(text));
}

function systemConstant(value: string): boolean {
  return !value.startsWith("CUSTOM_")
    && value === value.toUpperCase()
    && /^[A-Z0-9_]+$/u.test(value);
}

function displayFor(value: unknown, mapping: Readonly<Record<string, string>>): string {
  const ident = pythonTransformString(value);
  return mapping[ident] ?? (systemConstant(ident) ? ident : "<unknown>");
}

async function deriveHclComments(options: {
  readonly configDirectory: string;
  readonly items: PullTransformResult["items"];
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}): Promise<HclTfvarsComments> {
  const comments: Record<string, string> = Object.create(null) as Record<string, string>;
  const lookups = new Map<string, ReturnType<typeof parseLookupSidecar> | null>();
  for (const itemKey of sortedStrings(Object.keys(options.items))) {
    const item = options.items[itemKey];
    if (item === undefined) continue;
    for (const field of sortedStrings(Object.keys(options.references))) {
      if (!own(item, field)) continue;
      const value = item[field];
      if (value === null || value === undefined) continue;
      const spec = options.references[field];
      if (spec === undefined) continue;
      let lookup = lookups.get(spec.referent);
      if (lookup === undefined && !lookups.has(spec.referent)) {
        lookup = await loadLookup(
          path.join(options.configDirectory, `${spec.referent}.lookup.json`),
        );
        lookups.set(spec.referent, lookup);
      }
      if (lookup === null || lookup === undefined) continue;
      const comment = (child: unknown): string => {
        return displayFor(child, lookup.byId).replaceAll("\n", " ").replaceAll("\r", " ");
      };
      if (Array.isArray(value)) {
        value.forEach((child, index) => {
          if (child !== null && child !== undefined) {
            comments[hclTfvarsCommentKey(itemKey, field, index)] = comment(child);
          }
        });
      } else {
        comments[hclTfvarsCommentKey(itemKey, field)] = comment(value);
      }
    }
  }
  return comments;
}

async function renderDeploymentTfvars(options: {
  readonly deployment: Deployment;
  readonly items: PullTransformResult["items"];
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
  readonly resourceType: string;
  readonly tenant: string;
  readonly variableName: string;
}): Promise<string> {
  if (deploymentTfvarsFormat(options.deployment) === "json") {
    return renderPythonLosslessArtifactJson(record([[options.variableName, options.items]]));
  }
  return renderTfvarsHcl(
    options.items,
    await deriveHclComments({
      configDirectory: deploymentConfigDir(options.deployment, options.tenant),
      items: options.items,
      references: options.references,
    }),
    options.variableName,
  );
}

async function lookupKeyMaps(options: {
  readonly configDirectory: string;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}): Promise<Readonly<Record<string, Readonly<Record<string, string>> | null>>> {
  const output: Record<string, Readonly<Record<string, string>> | null> = Object.create(null) as Record<
    string,
    Readonly<Record<string, string>> | null
  >;
  for (const spec of Object.values(options.references)) {
    if (own(output, spec.referent)) continue;
    const lookup = await loadLookup(
      path.join(options.configDirectory, `${spec.referent}.lookup.json`),
    );
    output[spec.referent] = lookup?.keyById ?? null;
  }
  return output;
}

/** Materialize the ordinary transform artifact set with Python's file lifecycle. */
export async function writeTransformArtifacts(options: {
  readonly bindingContext: BindingContext;
  readonly deployment: Deployment;
  readonly lookupNameField: string | null;
  readonly onDiagnostic?: (message: string) => void;
  readonly override: Readonly<Record<string, unknown>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
  readonly resourceType: string;
  readonly result: PullTransformResult;
  readonly tenant: string;
  readonly variableName: string;
}): Promise<TransformArtifactWriteResult> {
  const paths = transformArtifactPaths(options);
  const written: string[] = [];
  const removed: string[] = [];
  const note = options.onDiagnostic ?? (() => undefined);
  await mkdir(path.dirname(paths.config), { recursive: true });
  await mkdir(path.dirname(paths.imports), { recursive: true });

  if (options.lookupNameField !== null) {
    await writeFile(paths.lookup, renderTransformLookup({
      items: options.result.items,
      originals: options.result.originals,
      nameField: options.lookupNameField,
    }), "utf8");
    written.push(paths.lookup);
    note(`wrote ${paths.lookup}`);
  }

  const template = typeof options.override.import_id === "string"
    ? options.override.import_id
    : undefined;
  const newImports = renderTransformImports({
    resourceType: options.resourceType,
    originals: options.result.originals,
    ...(template === undefined ? {} : { template }),
  });
  const oldImports = await readOptionalUtf8(paths.imports, `${options.resourceType} imports`);
  const moves = oldImports === null
    ? { moves: [], suppressed: [] }
    : deriveImportMoves(options.resourceType, oldImports, newImports);
  if (moves.moves.length > 0) {
    await writeFile(paths.moves, renderMovedBlocks(options.resourceType, moves.moves), "utf8");
    written.push(paths.moves);
    note(
      `RENAME(S) DETECTED: ${moves.moves.length} item(s) re-keyed — moved blocks staged in ${paths.moves}; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)`,
    );
  } else if (await removeIfPresent(paths.moves)) {
    removed.push(paths.moves);
    note(`removed stale ${paths.moves} (no renames this run)`);
  }
  for (const suppression of moves.suppressed) {
    note(
      `SUPPRESSED RENAME CANDIDATE: ${options.resourceType} ${JSON.stringify(suppression.oldKey)} -> ${JSON.stringify(suppression.newKey)} (import_id ${JSON.stringify(suppression.importId)}, reason=${suppression.reason}); no moved block emitted`,
    );
  }

  if (await removeIfPresent(paths.staleConfig)) {
    removed.push(paths.staleConfig);
    note(`removed stale ${paths.staleConfig}`);
  }
  await writeFile(paths.config, await renderDeploymentTfvars({
    deployment: options.deployment,
    items: options.result.items,
    references: options.references,
    resourceType: options.resourceType,
    tenant: options.tenant,
    variableName: options.variableName,
  }), "utf8");
  written.push(paths.config);

  const binding = deriveGeneratedBindings({
    context: options.bindingContext,
    items: options.result.items,
    lookupKeys: await lookupKeyMaps({
      configDirectory: path.dirname(paths.config),
      references: options.references,
    }),
    resourceType: options.resourceType,
  });
  for (const message of binding.notes) note(`NOTE bindings: ${message}`);
  if (Object.keys(binding.data.resources).length > 0) {
    await writeFile(paths.generatedBindings, renderGeneratedBindings(binding.data), "utf8");
    written.push(paths.generatedBindings);
    note(`wrote ${paths.generatedBindings}`);
  } else if (await removeIfPresent(paths.generatedBindings)) {
    removed.push(paths.generatedBindings);
    note(`removed stale ${paths.generatedBindings}`);
  }

  await writeFile(paths.imports, newImports, "utf8");
  written.push(paths.imports);
  note(`wrote ${paths.config}`);
  note(`wrote ${paths.imports}`);
  return { paths, written, removed };
}

/** Derived resources write config only and intentionally create no imports. */
export async function writeDerivedTransformArtifact(options: {
  readonly deployment: Deployment;
  readonly items: PullTransformResult["items"];
  readonly onDiagnostic?: (message: string) => void;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
  readonly resourceType: string;
  readonly sourceType: string;
  readonly tenant: string;
  readonly variableName: string;
}): Promise<string> {
  const paths = transformArtifactPaths(options);
  await mkdir(path.dirname(paths.config), { recursive: true });
  if (await removeIfPresent(paths.staleConfig)) {
    options.onDiagnostic?.(`removed stale ${paths.staleConfig}`);
  }
  await writeFile(paths.config, await renderDeploymentTfvars({
    deployment: options.deployment,
    items: options.items,
    references: options.references,
    resourceType: options.resourceType,
    tenant: options.tenant,
    variableName: options.variableName,
  }), "utf8");
  options.onDiagnostic?.(
    `wrote ${paths.config} (derived from ${options.sourceType}; not importable — no imports)`,
  );
  return paths.config;
}
