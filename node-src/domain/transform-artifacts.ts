import { LosslessNumber } from "lossless-json";
import {
  chmod,
  lstat,
  mkdir,
  mkdtemp,
  rename,
  rm,
  unlink,
  writeFile,
} from "node:fs/promises";
import path from "node:path";

import {
  deriveImportMoves,
  renderGeneratedImports,
  renderHclQuotedString,
  renderMovedBlocks,
  type ImportMoveDerivation,
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
import type { ReferenceBindingMode } from "./deployment.js";

type JsonRecord = Record<string, unknown>;

export interface TransformReferenceSpec {
  readonly name_field: string;
  readonly referent: string;
}

export interface BindingContext {
  readonly mode: ReferenceBindingMode;
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

export interface TransformLookupData {
  readonly byId: Readonly<Record<string, string>>;
  readonly keyById: Readonly<Record<string, string>>;
}

export interface TransformArtifactCompileOptions {
  readonly bindingContext: BindingContext;
  readonly deployment: Deployment;
  readonly lookupNameField: string | null;
  /**
   * Authoritative lookup data already compiled in the same transaction.
   * An explicit null suppresses a stale lookup sidecar on disk.
   */
  readonly lookupOverrides?: Readonly<Record<string, TransformLookupData | null>>;
  readonly onDiagnostic?: (message: string) => void;
  readonly override: Readonly<Record<string, unknown>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
  readonly resourceType: string;
  readonly result: PullTransformResult;
  readonly tenant: string;
  readonly variableName: string;
}

/** Opaque, fully preflighted transform output; pass this to the publish functions. */
export interface CompiledTransformArtifacts {
  readonly binding: GeneratedBindingsResult;
  readonly configText: string;
  readonly existingMoves: string | null;
  readonly lookupText: string | null;
  readonly moves: ImportMoveDerivation;
  readonly newImports: string;
  readonly onDiagnostic?: (message: string) => void;
  readonly paths: TransformArtifactPaths;
  readonly renderedMoves: string | null;
  readonly resourceType: string;
}

type BatchArtifactMutation = Readonly<{
  contents?: string;
  kind: "remove" | "write";
  resourceType: string;
  target: string;
}>;

type PreparedBatchArtifactMutation = BatchArtifactMutation & Readonly<{
  backupPath: string;
  stagePath: string | null;
}>;

type AppliedBatchArtifactMutation = PreparedBatchArtifactMutation & Readonly<{
  hadOriginal: boolean;
}>;

type BatchArtifactCommitHook = (
  mutation: Readonly<Pick<BatchArtifactMutation, "kind" | "resourceType" | "target">>,
  phase: "commit" | "rollback",
) => void | Promise<void>;

class BatchArtifactRollbackError extends AggregateError {
  constructor(
    errors: readonly unknown[],
    readonly transactionDirectories: readonly string[],
  ) {
    super(
      errors,
      `transform artifact batch publication and rollback both failed; recovery data preserved in ${transactionDirectories.join(", ")}`,
    );
    this.name = "BatchArtifactRollbackError";
  }
}

let batchArtifactCommitHook: BatchArtifactCommitHook | undefined;

/** @internal Test-only fault injection for batch publication rollback coverage. */
export function installTransformArtifactBatchCommitHookForTests(
  hook: BatchArtifactCommitHook,
): () => void {
  if (batchArtifactCommitHook !== undefined) {
    throw new Error("a transform artifact batch commit test hook is already installed");
  }
  batchArtifactCommitHook = hook;
  return () => {
    if (batchArtifactCommitHook === hook) batchArtifactCommitHook = undefined;
  };
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

/** Match Python str.format's field and doubled-brace behavior for import IDs. */
export function formatImportTemplate(
  template: string,
  original: Readonly<Record<string, unknown>>,
): string {
  let output = "";
  for (let index = 0; index < template.length;) {
    const character = template[index];
    if (character === "{" && template[index + 1] === "{") {
      output += "{";
      index += 2;
      continue;
    }
    if (character === "}" && template[index + 1] === "}") {
      output += "}";
      index += 2;
      continue;
    }
    if (character !== "{") {
      if (character === "}") {
        throw new TypeError(`invalid import_id template ${JSON.stringify(template)}`);
      }
      output += character ?? "";
      index += 1;
      continue;
    }
    const end = template.indexOf("}", index + 1);
    if (end < 0) throw new TypeError(`invalid import_id template ${JSON.stringify(template)}`);
    const field = template.slice(index + 1, end);
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/u.test(field) || !own(original, field)) {
      throw new TypeError(
        `import_id template ${JSON.stringify(template)} references missing field ${JSON.stringify(field)}`,
      );
    }
    output += pythonTransformString(original[field]);
    index = end + 1;
  }
  return output;
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

export function parseLookupSidecar(value: unknown): TransformLookupData {
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

function bindableReference(
  resourceType: string,
  referent: string,
  context: BindingContext,
): boolean {
  if (
    resourceType === referent
    || !context.generated.has(resourceType)
    || !context.generated.has(referent)
    || context.derived.has(resourceType)
    || context.derived.has(referent)
  ) {
    return false;
  }
  const referrerRoot = context.resourceRoots[resourceType];
  const referentRoot = context.resourceRoots[referent];
  if (referrerRoot === undefined || referentRoot === undefined) return false;
  return context.mode === "cross_state" || referrerRoot === referentRoot;
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
  if (options.context.mode === "disabled") {
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
    if (!bindableReference(options.resourceType, spec.referent, options.context)) continue;
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
      if (sameRoot(options.resourceType, spec.referent, options.context)) {
        return `module.${spec.referent}.items[${renderHclQuotedString(referentKey)}].id`;
      }
      const referentRoot = options.context.resourceRoots[spec.referent];
      if (referentRoot === undefined) {
        throw new TypeError(`cross-state reference ${spec.referent} has no deployment root`);
      }
      return `data.terraform_remote_state.${referentRoot}.outputs.infrawright_reference_ids.${spec.referent}[${renderHclQuotedString(referentKey)}]`;
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
        reason: sameRoot(options.resourceType, spec.referent, options.context)
          ? `group-local reference binding via ${spec.referent}.items`
          : `cross-state reference binding via ${spec.referent} root output`,
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

async function loadLookup(file: string): Promise<TransformLookupData | null> {
  const text = await readOptionalUtf8(file, `lookup for ${path.basename(file)}`);
  if (text === null) return null;
  return parseLookupSidecar(parseDataJsonLosslessly(text));
}

async function resolveLookup(options: {
  readonly configDirectory: string;
  readonly lookupOverrides?: Readonly<Record<string, TransformLookupData | null>>;
  readonly referent: string;
}): Promise<TransformLookupData | null> {
  if (
    options.lookupOverrides !== undefined
    && own(options.lookupOverrides, options.referent)
  ) {
    return options.lookupOverrides[options.referent] ?? null;
  }
  return loadLookup(
    path.join(options.configDirectory, `${options.referent}.lookup.json`),
  );
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
  readonly lookupOverrides?: Readonly<Record<string, TransformLookupData | null>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}): Promise<HclTfvarsComments> {
  const comments: Record<string, string> = Object.create(null) as Record<string, string>;
  const lookups = new Map<string, TransformLookupData | null>();
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
        lookup = await resolveLookup({
          configDirectory: options.configDirectory,
          ...(options.lookupOverrides === undefined
            ? {}
            : { lookupOverrides: options.lookupOverrides }),
          referent: spec.referent,
        });
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
  readonly lookupOverrides?: Readonly<Record<string, TransformLookupData | null>>;
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
      ...(options.lookupOverrides === undefined
        ? {}
        : { lookupOverrides: options.lookupOverrides }),
      references: options.references,
    }),
    options.variableName,
  );
}

async function lookupKeyMaps(options: {
  readonly configDirectory: string;
  readonly lookupOverrides?: Readonly<Record<string, TransformLookupData | null>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}): Promise<Readonly<Record<string, Readonly<Record<string, string>> | null>>> {
  const output: Record<string, Readonly<Record<string, string>> | null> = Object.create(null) as Record<
    string,
    Readonly<Record<string, string>> | null
  >;
  for (const spec of Object.values(options.references)) {
    if (own(output, spec.referent)) continue;
    const lookup = await resolveLookup({
      configDirectory: options.configDirectory,
      ...(options.lookupOverrides === undefined
        ? {}
        : { lookupOverrides: options.lookupOverrides }),
      referent: spec.referent,
    });
    output[spec.referent] = lookup?.keyById ?? null;
  }
  return output;
}

function compileLookup(options: TransformArtifactCompileOptions): {
  readonly data: TransformLookupData | null;
  readonly text: string | null;
} {
  if (options.lookupNameField === null) return { data: null, text: null };
  const text = renderTransformLookup({
    items: options.result.items,
    originals: options.result.originals,
    nameField: options.lookupNameField,
  });
  return {
    data: parseLookupSidecar(parseDataJsonLosslessly(text)),
    text,
  };
}

/**
 * Read and validate every input needed to publish one ordinary transform
 * artifact set. This function never creates, writes, renames, or removes a
 * filesystem entry.
 */
export async function compileTransformArtifacts(
  options: TransformArtifactCompileOptions,
): Promise<CompiledTransformArtifacts> {
  const paths = transformArtifactPaths(options);
  const lookupText = compileLookup(options).text;

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
  const renderedMoves = moves.moves.length === 0
    ? null
    : renderMovedBlocks(options.resourceType, moves.moves);
  const existingMoves = await readOptionalUtf8(paths.moves, `${options.resourceType} moves`);
  if (
    existingMoves !== null
    && renderedMoves !== null
    && existingMoves !== renderedMoves
  ) {
    throw new Error(
      `unresolved/conflicting move evidence for ${options.resourceType}: ${paths.moves} already contains a different migration; preserve or explicitly resolve it before generating another rename`,
    );
  }

  const configText = await renderDeploymentTfvars({
    deployment: options.deployment,
    items: options.result.items,
    ...(options.lookupOverrides === undefined
      ? {}
      : { lookupOverrides: options.lookupOverrides }),
    references: options.references,
    resourceType: options.resourceType,
    tenant: options.tenant,
    variableName: options.variableName,
  });
  const binding = deriveGeneratedBindings({
    context: options.bindingContext,
    items: options.result.items,
    lookupKeys: await lookupKeyMaps({
      configDirectory: path.dirname(paths.config),
      ...(options.lookupOverrides === undefined
        ? {}
        : { lookupOverrides: options.lookupOverrides }),
      references: options.references,
    }),
    resourceType: options.resourceType,
  });

  return Object.freeze({
    binding,
    configText,
    existingMoves,
    lookupText,
    moves,
    newImports,
    ...(options.onDiagnostic === undefined
      ? {}
      : { onDiagnostic: options.onDiagnostic }),
    paths,
    renderedMoves,
    resourceType: options.resourceType,
  });
}

/**
 * Compile a complete batch before the caller publishes any member. Fresh
 * lookup data from every member is authoritative for same-batch references.
 */
export async function compileTransformArtifactBatch(
  options: readonly TransformArtifactCompileOptions[],
): Promise<readonly CompiledTransformArtifacts[]> {
  const pathOwners = new Map<string, string>();
  const lookupsByConfigDirectory = new Map<
    string,
    Record<string, TransformLookupData | null>
  >();
  for (const item of options) {
    const paths = transformArtifactPaths(item);
    for (const outputPath of Object.values(paths)) {
      const owner = pathOwners.get(outputPath);
      if (owner !== undefined) {
        throw new Error(
          `transform artifact batch output collision: ${JSON.stringify(outputPath)} is owned by both ${JSON.stringify(owner)} and ${JSON.stringify(item.resourceType)}`,
        );
      }
      pathOwners.set(outputPath, item.resourceType);
    }
    const configDirectory = path.dirname(paths.config);
    let lookups = lookupsByConfigDirectory.get(configDirectory);
    if (lookups === undefined) {
      lookups = Object.create(null) as Record<string, TransformLookupData | null>;
      lookupsByConfigDirectory.set(configDirectory, lookups);
    }
    lookups[item.resourceType] = compileLookup(item).data;
  }

  return Promise.all(options.map((item) => {
    const configDirectory = path.dirname(transformArtifactPaths(item).config);
    return compileTransformArtifacts({
      ...item,
      lookupOverrides: {
        ...(item.lookupOverrides ?? {}),
        ...(lookupsByConfigDirectory.get(configDirectory) ?? {}),
      },
    });
  }));
}

/** Publish one fully compiled artifact set with the legacy file lifecycle. */
export async function publishCompiledTransformArtifacts(
  compiled: CompiledTransformArtifacts,
): Promise<TransformArtifactWriteResult> {
  const {
    binding,
    configText,
    existingMoves,
    lookupText,
    moves,
    newImports,
    paths,
    renderedMoves,
    resourceType,
  } = compiled;
  const written: string[] = [];
  const removed: string[] = [];
  const note = compiled.onDiagnostic ?? (() => undefined);

  await mkdir(path.dirname(paths.config), { recursive: true });
  await mkdir(path.dirname(paths.imports), { recursive: true });

  if (lookupText !== null) {
    await writeFile(paths.lookup, lookupText, "utf8");
    written.push(paths.lookup);
    note(`wrote ${paths.lookup}`);
  }

  if (existingMoves === null && renderedMoves !== null) {
    await writeFile(paths.moves, renderedMoves, "utf8");
    written.push(paths.moves);
    note(
      `RENAME(S) DETECTED: ${moves.moves.length} item(s) re-keyed — moved blocks staged in ${paths.moves}; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)`,
    );
  } else if (existingMoves !== null) {
    note(
      renderedMoves === null
        ? `preserved unresolved move evidence ${paths.moves} (no newly derived moves this run)`
        : `preserved byte-identical unresolved move evidence ${paths.moves}`,
    );
  }
  for (const suppression of moves.suppressed) {
    note(
      `SUPPRESSED RENAME CANDIDATE: ${resourceType} ${JSON.stringify(suppression.oldKey)} -> ${JSON.stringify(suppression.newKey)} (import_id ${JSON.stringify(suppression.importId)}, reason=${suppression.reason}); no moved block emitted`,
    );
  }

  if (await removeIfPresent(paths.staleConfig)) {
    removed.push(paths.staleConfig);
    note(`removed stale ${paths.staleConfig}`);
  }
  await writeFile(paths.config, configText, "utf8");
  written.push(paths.config);

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

function isMissingFileError(error: unknown): boolean {
  return typeof error === "object"
    && error !== null
    && "code" in error
    && error.code === "ENOENT";
}

async function assertRegularBatchArtifactTarget(target: string): Promise<void> {
  try {
    const metadata = await lstat(target);
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      throw new Error(
        `transform artifact batch target is not a regular file: ${target}`,
      );
    }
  } catch (error: unknown) {
    if (isMissingFileError(error)) return;
    throw error;
  }
}

function batchArtifactMutations(
  compiled: CompiledTransformArtifacts,
): readonly BatchArtifactMutation[] {
  const mutations: BatchArtifactMutation[] = [];
  if (compiled.lookupText !== null) {
    mutations.push({
      contents: compiled.lookupText,
      kind: "write",
      resourceType: compiled.resourceType,
      target: compiled.paths.lookup,
    });
  }
  if (compiled.existingMoves === null && compiled.renderedMoves !== null) {
    mutations.push({
      contents: compiled.renderedMoves,
      kind: "write",
      resourceType: compiled.resourceType,
      target: compiled.paths.moves,
    });
  }
  mutations.push({
    kind: "remove",
    resourceType: compiled.resourceType,
    target: compiled.paths.staleConfig,
  });
  mutations.push({
    contents: compiled.configText,
    kind: "write",
    resourceType: compiled.resourceType,
    target: compiled.paths.config,
  });
  if (Object.keys(compiled.binding.data.resources).length > 0) {
    mutations.push({
      contents: renderGeneratedBindings(compiled.binding.data),
      kind: "write",
      resourceType: compiled.resourceType,
      target: compiled.paths.generatedBindings,
    });
  } else {
    mutations.push({
      kind: "remove",
      resourceType: compiled.resourceType,
      target: compiled.paths.generatedBindings,
    });
  }
  mutations.push({
    contents: compiled.newImports,
    kind: "write",
    resourceType: compiled.resourceType,
    target: compiled.paths.imports,
  });
  return mutations;
}

async function removeTransactionDirectories(
  directories: readonly string[],
): Promise<readonly unknown[]> {
  const failures: unknown[] = [];
  for (const directory of directories) {
    try {
      await rm(directory, { force: true, recursive: true });
    } catch (error: unknown) {
      failures.push(error);
    }
  }
  return failures;
}

async function prepareBatchArtifactMutations(
  mutations: readonly BatchArtifactMutation[],
): Promise<Readonly<{
  mutations: readonly PreparedBatchArtifactMutation[];
  transactionDirectories: readonly string[];
}>> {
  const transactionDirectoryByParent = new Map<string, string>();
  const prepared: PreparedBatchArtifactMutation[] = [];
  try {
    for (const [index, mutation] of mutations.entries()) {
      const parent = path.dirname(mutation.target);
      await mkdir(parent, { recursive: true });
      let transactionDirectory = transactionDirectoryByParent.get(parent);
      if (transactionDirectory === undefined) {
        transactionDirectory = await mkdtemp(
          path.join(parent, ".infrawright-artifact-batch-"),
        );
        transactionDirectoryByParent.set(parent, transactionDirectory);
      }
      const stagePath = mutation.kind === "write"
        ? path.join(transactionDirectory, `stage-${index}`)
        : null;
      if (stagePath !== null) {
        if (mutation.contents === undefined) {
          throw new Error(`missing staged contents for ${mutation.target}`);
        }
        await writeFile(stagePath, mutation.contents, "utf8");
      }
      prepared.push({
        ...mutation,
        backupPath: path.join(transactionDirectory, `backup-${index}`),
        stagePath,
      });
    }
    return {
      mutations: prepared,
      transactionDirectories: [...transactionDirectoryByParent.values()],
    };
  } catch (error: unknown) {
    const cleanupFailures = await removeTransactionDirectories(
      [...transactionDirectoryByParent.values()],
    );
    if (cleanupFailures.length === 0) throw error;
    throw new AggregateError(
      [error, ...cleanupFailures],
      "transform artifact batch staging and cleanup both failed",
    );
  }
}

async function applyBatchArtifactMutations(
  mutations: readonly PreparedBatchArtifactMutation[],
): Promise<readonly AppliedBatchArtifactMutation[]> {
  const applied: AppliedBatchArtifactMutation[] = [];
  try {
    for (const mutation of mutations) {
      await batchArtifactCommitHook?.({
        kind: mutation.kind,
        resourceType: mutation.resourceType,
        target: mutation.target,
      }, "commit");
      let hadOriginal = false;
      try {
        await rename(mutation.target, mutation.backupPath);
        hadOriginal = true;
      } catch (error: unknown) {
        if (!isMissingFileError(error)) throw error;
      }
      const appliedMutation = { ...mutation, hadOriginal };
      applied.push(appliedMutation);
      const previous = hadOriginal ? await lstat(mutation.backupPath) : null;
      if (
        previous !== null
        && (!previous.isFile() || previous.isSymbolicLink())
      ) {
        throw new Error(
          `transform artifact batch target changed to a non-regular file: ${mutation.target}`,
        );
      }
      if (mutation.kind === "write") {
        if (mutation.stagePath === null) {
          throw new Error(`missing staged artifact for ${mutation.target}`);
        }
        if (previous !== null) {
          await chmod(mutation.stagePath, previous.mode & 0o7777);
        }
        await rename(mutation.stagePath, mutation.target);
      }
    }
    return applied;
  } catch (error: unknown) {
    const rollbackFailures: unknown[] = [];
    for (const mutation of [...applied].reverse()) {
      try {
        await batchArtifactCommitHook?.({
          kind: mutation.kind,
          resourceType: mutation.resourceType,
          target: mutation.target,
        }, "rollback");
        if (mutation.kind === "write") await removeIfPresent(mutation.target);
        if (mutation.hadOriginal) {
          await rename(mutation.backupPath, mutation.target);
        }
      } catch (rollbackError: unknown) {
        rollbackFailures.push(rollbackError);
      }
    }
    if (rollbackFailures.length === 0) throw error;
    throw new BatchArtifactRollbackError(
      [error, ...rollbackFailures],
      [...new Set(applied.map((mutation) => path.dirname(mutation.backupPath)))],
    );
  }
}

function completedBatchArtifactResult(
  compiled: CompiledTransformArtifacts,
  applied: readonly AppliedBatchArtifactMutation[],
): TransformArtifactWriteResult {
  const resourceMutations = applied.filter((mutation) => {
    return mutation.resourceType === compiled.resourceType;
  });
  const written = resourceMutations
    .filter((mutation) => mutation.kind === "write")
    .map((mutation) => mutation.target);
  const removed = resourceMutations
    .filter((mutation) => mutation.kind === "remove" && mutation.hadOriginal)
    .map((mutation) => mutation.target);
  const removedSet = new Set(removed);
  const note = compiled.onDiagnostic ?? (() => undefined);

  if (compiled.lookupText !== null) note(`wrote ${compiled.paths.lookup}`);
  if (compiled.existingMoves === null && compiled.renderedMoves !== null) {
    note(
      `RENAME(S) DETECTED: ${compiled.moves.moves.length} item(s) re-keyed — moved blocks staged in ${compiled.paths.moves}; copy into the env root alongside the imports file before plan/apply (RUNBOOK: Drift)`,
    );
  } else if (compiled.existingMoves !== null) {
    note(
      compiled.renderedMoves === null
        ? `preserved unresolved move evidence ${compiled.paths.moves} (no newly derived moves this run)`
        : `preserved byte-identical unresolved move evidence ${compiled.paths.moves}`,
    );
  }
  for (const suppression of compiled.moves.suppressed) {
    note(
      `SUPPRESSED RENAME CANDIDATE: ${compiled.resourceType} ${JSON.stringify(suppression.oldKey)} -> ${JSON.stringify(suppression.newKey)} (import_id ${JSON.stringify(suppression.importId)}, reason=${suppression.reason}); no moved block emitted`,
    );
  }
  if (removedSet.has(compiled.paths.staleConfig)) {
    note(`removed stale ${compiled.paths.staleConfig}`);
  }
  for (const message of compiled.binding.notes) note(`NOTE bindings: ${message}`);
  if (Object.keys(compiled.binding.data.resources).length > 0) {
    note(`wrote ${compiled.paths.generatedBindings}`);
  } else if (removedSet.has(compiled.paths.generatedBindings)) {
    note(`removed stale ${compiled.paths.generatedBindings}`);
  }
  note(`wrote ${compiled.paths.config}`);
  note(`wrote ${compiled.paths.imports}`);
  return { paths: compiled.paths, written, removed };
}

/**
 * Publish an already-preflighted batch as one rollback-capable filesystem
 * transaction in deterministic caller order.
 */
export async function publishCompiledTransformArtifactBatch(
  compiled: readonly CompiledTransformArtifacts[],
): Promise<readonly TransformArtifactWriteResult[]> {
  const mutations = compiled.flatMap((item) => batchArtifactMutations(item));
  const targetOwners = new Map<string, string>();
  for (const mutation of mutations) {
    const owner = targetOwners.get(mutation.target);
    if (owner !== undefined) {
      throw new Error(
        `transform artifact batch mutation collision: ${JSON.stringify(mutation.target)} is owned by both ${JSON.stringify(owner)} and ${JSON.stringify(mutation.resourceType)}`,
      );
    }
    targetOwners.set(mutation.target, mutation.resourceType);
  }
  await Promise.all(mutations.map((mutation) => {
    return assertRegularBatchArtifactTarget(mutation.target);
  }));
  const prepared = await prepareBatchArtifactMutations(mutations);
  let applied: readonly AppliedBatchArtifactMutation[];
  try {
    applied = await applyBatchArtifactMutations(prepared.mutations);
  } catch (error: unknown) {
    if (error instanceof BatchArtifactRollbackError) throw error;
    const cleanupFailures = await removeTransactionDirectories(
      prepared.transactionDirectories,
    );
    if (cleanupFailures.length === 0) throw error;
    throw new AggregateError(
      [error, ...cleanupFailures],
      "transform artifact batch publication failed and transaction cleanup also failed",
    );
  }
  const cleanupFailures = await removeTransactionDirectories(
    prepared.transactionDirectories,
  );
  if (cleanupFailures.length > 0) {
    throw new AggregateError(
      cleanupFailures,
      "transform artifact batch committed but transaction cleanup failed",
    );
  }
  return compiled.map((item) => completedBatchArtifactResult(item, applied));
}

/** Materialize one ordinary transform artifact set with the legacy lifecycle. */
export async function writeTransformArtifacts(
  options: TransformArtifactCompileOptions,
): Promise<TransformArtifactWriteResult> {
  return publishCompiledTransformArtifacts(await compileTransformArtifacts(options));
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
