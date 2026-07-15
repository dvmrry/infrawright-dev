import { LosslessNumber } from "lossless-json";

import { parseDataJsonLosslessly } from "../json/control.js";
import { canonicalPythonNumberToken, pythonFiniteFloatToken } from "../json/python-number.js";
import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { readOptionalUtf8 } from "../io/files.js";
import {
  terraformAttributeType,
  terraformAttributesForBlock,
  terraformBlockForSchema,
  terraformBlockIsSingle,
  terraformBlockTypesForBlock,
  terraformBooleanField,
  terraformRequireObject,
  type TerraformTypeEncoding,
} from "../metadata/terraform-schema.js";
import type { JsonObject } from "../metadata/validation.js";
import { parseHclQuotedString } from "./import-moves.js";

const PATH_SEGMENT = /^[A-Za-z_][A-Za-z0-9_]*$/u;
const EXACT_VAR_EXPR = /^var\.([A-Za-z_][A-Za-z0-9_]*)$/u;
const IDENT = String.raw`[A-Za-z_][A-Za-z0-9_]*`;
const HCL_STRING = String.raw`"(?:[^"\\$%]|\$(?!\{)|%(?!\{)|\\["\\nrt])*"`;
const NUMERIC_INDEX = String.raw`\[[0-9]+\]`;
const SELECTOR_TAIL = String.raw`(?:\.${IDENT}|\[${HCL_STRING}\]|${NUMERIC_INDEX})*`;
const MODULE_SELECTOR = String.raw`module\.${IDENT}${SELECTOR_TAIL}`;
const DATA_SELECTOR = String.raw`data\.${IDENT}\.${IDENT}${SELECTOR_TAIL}`;
const LIST_ELEMENT = String.raw`(?:${MODULE_SELECTOR}|${DATA_SELECTOR}|${HCL_STRING})`;
// Python `re` and JavaScript disagree on `\s` (notably U+FEFF). Freeze the
// Python whitespace set so the migration keeps one expression grammar.
const PYTHON_WHITESPACE = String.raw`[\x09-\x0d\x1c-\x20\x85\xa0\u1680\u2000-\u200a\u2028-\u2029\u202f\u205f\u3000]`;
const PYTHON_WHITESPACE_CHARACTER = new RegExp(`^${PYTHON_WHITESPACE}$`, "u");
const ALLOWED_EXPRESSIONS = [
  new RegExp(String.raw`^var\.${IDENT}$`, "u"),
  new RegExp(String.raw`^local\.${IDENT}$`, "u"),
  new RegExp(String.raw`^${DATA_SELECTOR}$`, "u"),
  new RegExp(String.raw`^${MODULE_SELECTOR}$`, "u"),
  new RegExp(
    String.raw`^\[${PYTHON_WHITESPACE}*(?:${LIST_ELEMENT}(?:${PYTHON_WHITESPACE}*,${PYTHON_WHITESPACE}*${LIST_ELEMENT})*)?${PYTHON_WHITESPACE}*\]$`,
    "u",
  ),
] as const;
const CONTROL_CHARACTERS = /[\x00-\x1f\x7f]/u;

type JsonRecord = Readonly<Record<string, unknown>>;

export interface ExpressionBinding {
  readonly address: string;
  readonly key: string;
  readonly path: string;
  readonly pathParts: readonly (string | number)[];
  readonly expression: string;
  readonly sensitive: boolean;
  readonly reason: string | null;
}

export class HclExpression {
  readonly expression: string;

  constructor(expression: string) {
    this.expression = validateExpression(expression, "HclExpression");
  }
}

function record(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pythonJsonString(value: string): string {
  return JSON.stringify(value).replace(/[\u007f-\uffff]/g, (character) => {
    return `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`;
  });
}

function hclKey(value: string): string {
  return PATH_SEGMENT.test(value) ? value : pythonJsonString(value);
}

export function validateExpression(expression: unknown, context: string): string {
  if (typeof expression !== "string" || expression.length === 0) {
    throw new TypeError(`${context} expression must be a non-empty string`);
  }
  if (CONTROL_CHARACTERS.test(expression)) {
    throw new TypeError(`${context} expression must not contain control characters`);
  }
  if (!ALLOWED_EXPRESSIONS.some((pattern) => pattern.test(expression))) {
    throw new TypeError(
      `${context} expression ${JSON.stringify(expression)} is outside the v1 allowlist (allowed roots: var., local., data., module.)`,
    );
  }
  return expression;
}

function renderPath(parts: readonly (string | number)[]): string {
  return parts.map((part, index) => {
    if (typeof part === "number") return `[${part}]`;
    return index === 0 ? part : `.${part}`;
  }).join("");
}

function parsePath(value: unknown, context: string): readonly (string | number)[] {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError(`${context} path must be a non-empty attribute path`);
  }
  const parts: Array<string | number> = [];
  let offset = 0;
  while (offset < value.length) {
    if (parts.length > 0 && value[offset] === ".") offset += 1;
    const attribute = /^[A-Za-z_][A-Za-z0-9_]*/u.exec(value.slice(offset))?.[0];
    if (attribute === undefined) {
      throw new TypeError(
        `${context} path ${JSON.stringify(value)} has an unsupported segment; use dotted attributes and exact canonical numeric list selectors`,
      );
    }
    parts.push(attribute);
    offset += attribute.length;
    while (value[offset] === "[") {
      const close = value.indexOf("]", offset + 1);
      if (close < 0) {
        throw new TypeError(`${context} path ${JSON.stringify(value)} has an unterminated list selector`);
      }
      const token = value.slice(offset + 1, close);
      if (!/^(?:0|[1-9][0-9]*)$/u.test(token)) {
        throw new TypeError(
          `${context} path ${JSON.stringify(value)} has unsupported list selector ${JSON.stringify(token)}; use an exact canonical non-negative index`,
        );
      }
      const index = Number(token);
      if (!Number.isSafeInteger(index)) {
        throw new TypeError(`${context} path ${JSON.stringify(value)} list selector exceeds the safe integer range`);
      }
      parts.push(index);
      offset = close + 1;
    }
    if (offset < value.length && value[offset] !== ".") {
      throw new TypeError(
        `${context} path ${JSON.stringify(value)} has an unsupported segment; use dotted attributes and exact canonical numeric list selectors`,
      );
    }
  }
  return parts;
}

function parseBinding(
  address: unknown,
  bindingPath: unknown,
  value: unknown,
  resourceType: string,
): ExpressionBinding {
  const context = `${String(address)}.${String(bindingPath)}`;
  if (!record(value)) throw new TypeError(`${context} binding must be an object`);
  const allowed = new Set(["expression", "sensitive", "reason"]);
  const unknown = sortedStrings(Object.keys(value).filter((key) => !allowed.has(key)));
  if (unknown.length > 0) {
    throw new TypeError(`${context} binding has unknown key(s): ${unknown.join(", ")}`);
  }
  if (!Object.hasOwn(value, "expression")) {
    throw new TypeError(`${context} binding is missing expression`);
  }
  const sensitive = Object.hasOwn(value, "sensitive") ? value.sensitive : false;
  if (typeof sensitive !== "boolean") throw new TypeError(`${context} sensitive must be a boolean`);
  const reason = value.reason ?? null;
  if (reason !== null && typeof reason !== "string") {
    throw new TypeError(`${context} reason must be a string when present`);
  }
  const prefix = `${resourceType}.`;
  if (typeof address !== "string" || !address.startsWith(prefix)) {
    throw new TypeError(`${context} address must be ${prefix}<key>`);
  }
  const key = address.slice(prefix.length);
  if (key.length === 0) throw new TypeError(`${context} address has empty resource key`);
  if (CONTROL_CHARACTERS.test(key)) {
    throw new TypeError(`${context} resource key must not contain control characters`);
  }
  const pathParts = parsePath(bindingPath, context);
  return {
    address,
    key,
    path: renderPath(pathParts),
    pathParts,
    expression: validateExpression(value.expression, context),
    sensitive,
    reason,
  };
}

/** Parse one resource type's operator or generated expression-binding document. */
export function parseExpressionBindings(
  data: unknown,
  resourceType: string,
): readonly ExpressionBinding[] {
  if (data === null || data === undefined) return [];
  if (!record(data)) throw new TypeError("expression bindings must be a JSON object");
  const unknown = sortedStrings(Object.keys(data).filter((key) => key !== "resources"));
  if (unknown.length > 0) {
    throw new TypeError(`expression bindings have unknown top-level key(s): ${unknown.join(", ")}`);
  }
  const resources = Object.hasOwn(data, "resources") ? data.resources : {};
  if (!record(resources)) throw new TypeError("expression bindings resources must be an object");
  const bindings: ExpressionBinding[] = [];
  const seen = new Set<string>();
  for (const address of sortedStrings(Object.keys(resources))) {
    const paths = resources[address];
    if (!record(paths)) throw new TypeError(`${address} bindings must be an object`);
    for (const bindingPath of sortedStrings(Object.keys(paths))) {
      const binding = parseBinding(address, bindingPath, paths[bindingPath], resourceType);
      const identity = JSON.stringify([binding.key, binding.path]);
      if (seen.has(identity)) {
        throw new TypeError(`duplicate expression binding for ${binding.address}.${binding.path}`);
      }
      seen.add(identity);
      bindings.push(binding);
    }
  }
  return bindings;
}

export async function loadExpressionBindings(
  file: string,
  resourceType: string,
): Promise<readonly ExpressionBinding[]> {
  const text = await readOptionalUtf8(file, `${resourceType} expression bindings`);
  if (text === null) return [];
  return parseExpressionBindings(parseDataJsonLosslessly(text), resourceType);
}

export function expressionVariables(
  bindings: readonly ExpressionBinding[],
): Readonly<Record<string, boolean>> {
  const variables: Record<string, boolean> = Object.create(null) as Record<string, boolean>;
  for (const binding of bindings) {
    const match = EXACT_VAR_EXPR.exec(binding.expression);
    if (match === null) continue;
    const name = match[1] ?? "";
    variables[name] = (variables[name] ?? false) || binding.sensitive;
  }
  return Object.fromEntries(sortedStrings(Object.keys(variables)).map((name) => [name, variables[name] ?? false]));
}

function cloneJson(value: unknown): unknown {
  if (value instanceof LosslessNumber) return new LosslessNumber(value.toString());
  if (Array.isArray(value)) return value.map(cloneJson);
  if (record(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, cloneJson(item)]));
  }
  return value;
}

/** Validate binding paths against items and replace leaves with expression sentinels. */
export function applyExpressionBindings(
  items: Readonly<Record<string, unknown>>,
  bindings: readonly ExpressionBinding[],
): Readonly<Record<string, unknown>> {
  const output = cloneJson(items);
  if (!record(output)) throw new TypeError("expression binding items must be an object");
  for (const binding of bindings) {
    if (!Object.hasOwn(output, binding.key)) {
      throw new TypeError(`expression binding references unknown resource address ${binding.address}`);
    }
    let current: unknown = output[binding.key];
    for (const part of binding.pathParts.slice(0, -1)) {
      if (typeof part === "number") {
        if (!Array.isArray(current)) {
          throw new TypeError(`expression binding ${binding.address}.${binding.path} indexes a non-list value`);
        }
        if (part >= current.length) {
          throw new TypeError(`expression binding ${binding.address}.${binding.path} has out-of-range list index [${part}]`);
        }
        current = current[part];
        continue;
      }
      if (Array.isArray(current)) {
        throw new TypeError(
          `expression binding ${binding.address}.${binding.path} traverses a list at ${part}; use an exact numeric list selector`,
        );
      }
      if (!record(current) || !Object.hasOwn(current, part)) {
        throw new TypeError(`expression binding ${binding.address}.${binding.path} has missing parent path`);
      }
      current = current[part];
    }
    const leaf = binding.pathParts.at(-1);
    if (typeof leaf === "number") {
      if (!Array.isArray(current)) {
        throw new TypeError(`expression binding ${binding.address}.${binding.path} indexes a non-list value`);
      }
      if (leaf >= current.length) {
        throw new TypeError(`expression binding ${binding.address}.${binding.path} has out-of-range list index [${leaf}]`);
      }
      current[leaf] = new HclExpression(binding.expression);
      continue;
    }
    if (Array.isArray(current)) {
      throw new TypeError(
        `expression binding ${binding.address}.${binding.path} traverses a list at ${leaf ?? "<leaf>"}; use an exact numeric list selector`,
      );
    }
    if (!record(current)) {
      throw new TypeError(`expression binding ${binding.address}.${binding.path} parent is not an object`);
    }
    if (leaf === undefined || !Object.hasOwn(current, leaf)) {
      throw new TypeError(`expression binding ${binding.address}.${binding.path} has missing target leaf`);
    }
    (current as Record<string, unknown>)[leaf] = new HclExpression(binding.expression);
  }
  return output;
}

type BindingSchemaCursor =
  | { readonly kind: "block"; readonly block: JsonObject; readonly label: string }
  | { readonly kind: "encoding"; readonly encoding: TerraformTypeEncoding; readonly label: string }
  | { readonly kind: "block-list"; readonly block: JsonObject; readonly label: string }
  | { readonly kind: "block-set"; readonly label: string };

function schemaPathError(binding: ExpressionBinding, message: string): never {
  throw new TypeError(`expression binding ${binding.address}.${binding.path} ${message}`);
}

function encodingCursor(
  binding: ExpressionBinding,
  cursor: Extract<BindingSchemaCursor, { readonly kind: "encoding" }>,
  part: string | number,
): BindingSchemaCursor {
  const encoding = cursor.encoding;
  if (typeof encoding === "string") {
    return schemaPathError(binding, `traverses scalar ${cursor.label}`);
  }
  const [kind, inner] = encoding;
  if (kind === "list") {
    if (typeof part !== "number") {
      return schemaPathError(binding, `traverses list ${cursor.label} without an exact numeric selector`);
    }
    return { kind: "encoding", encoding: inner, label: `${cursor.label}[${part}]` };
  }
  if (kind === "set") {
    return schemaPathError(binding, `cannot traverse unordered set ${cursor.label}; bind the complete set leaf`);
  }
  if (kind === "map") {
    return schemaPathError(binding, `cannot traverse map ${cursor.label}; bind the complete map leaf`);
  }
  if (typeof part !== "string") {
    return schemaPathError(binding, `indexes object ${cursor.label} as a list`);
  }
  const member = (inner as Readonly<Record<string, TerraformTypeEncoding>>)[part];
  if (member === undefined) return schemaPathError(binding, `references unknown schema path ${cursor.label}.${part}`);
  return { kind: "encoding", encoding: member, label: `${cursor.label}.${part}` };
}

function blockCursor(
  binding: ExpressionBinding,
  cursor: Extract<BindingSchemaCursor, { readonly kind: "block" }>,
  part: string | number,
): BindingSchemaCursor {
  if (typeof part !== "string") {
    return schemaPathError(binding, `indexes object ${cursor.label} as a list`);
  }
  const attributes = terraformAttributesForBlock(cursor.block, cursor.label);
  if (Object.hasOwn(attributes, part)) {
    const attribute = terraformRequireObject(attributes[part], `${cursor.label}.attributes.${part}`);
    if (!terraformBooleanField(attribute, "required") && !terraformBooleanField(attribute, "optional")) {
      return schemaPathError(binding, `targets computed-only attribute ${cursor.label}.${part}`);
    }
    return {
      kind: "encoding",
      encoding: terraformAttributeType(attribute, `${cursor.label}.attributes.${part}`),
      label: `${cursor.label}.${part}`,
    };
  }
  const blockTypes = terraformBlockTypesForBlock(cursor.block, cursor.label);
  if (!Object.hasOwn(blockTypes, part)) {
    return schemaPathError(binding, `references unknown schema path ${cursor.label}.${part}`);
  }
  const blockType = terraformRequireObject(blockTypes[part], `${cursor.label}.block_types.${part}`);
  const child = terraformRequireObject(blockType.block, `${cursor.label}.block_types.${part}.block`);
  const label = `${cursor.label}.${part}`;
  if (terraformBlockIsSingle(blockType)) return { kind: "block", block: child, label };
  if (blockType.nesting_mode === "list") return { kind: "block-list", block: child, label };
  if (blockType.nesting_mode === "set") return { kind: "block-set", label };
  return schemaPathError(binding, `cannot traverse ${String(blockType.nesting_mode)} block ${label}`);
}

/** Validate target paths against the provider schema, including native-HCL configs. */
export function validateExpressionBindingSchemaPaths(
  schema: Readonly<JsonObject>,
  resourceType: string,
  bindings: readonly ExpressionBinding[],
): void {
  const rootBlock = terraformBlockForSchema(schema as JsonObject, resourceType);
  for (const binding of bindings) {
    let cursor: BindingSchemaCursor = { kind: "block", block: rootBlock, label: resourceType };
    for (const part of binding.pathParts) {
      if (cursor.kind === "block") {
        cursor = blockCursor(binding, cursor, part);
      } else if (cursor.kind === "encoding") {
        cursor = encodingCursor(binding, cursor, part);
      } else if (cursor.kind === "block-list") {
        if (typeof part !== "number") {
          schemaPathError(binding, `traverses list block ${cursor.label} without an exact numeric selector`);
        }
        cursor = { kind: "block", block: cursor.block, label: `${cursor.label}[${part}]` };
      } else {
        schemaPathError(binding, `cannot traverse unordered set block ${cursor.label}; bind the complete block leaf`);
      }
    }
  }
}

export function renderExpressionHclValue(value: unknown, indent = 0): string {
  if (value instanceof HclExpression) return value.expression;
  if (typeof value === "string") return pythonJsonString(value);
  if (value === true) return "true";
  if (value === false) return "false";
  if (value === null) return "null";
  if (value instanceof LosslessNumber) {
    const token = canonicalPythonNumberToken(value.toString());
    if (token === null) throw new TypeError(`cannot render ${String(value)} as HCL`);
    return token;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError(`cannot render ${String(value)} as HCL`);
    if (Number.isSafeInteger(value)) return String(value);
    const token = pythonFiniteFloatToken(value);
    if (token === null) throw new TypeError(`cannot render ${String(value)} as HCL`);
    return token;
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => renderExpressionHclValue(item, indent)).join(", ")}]`;
  }
  if (record(value)) {
    const pad = " ".repeat(indent);
    const childPad = " ".repeat(indent + 2);
    const lines = ["{"];
    for (const key of sortedStrings(Object.keys(value))) {
      lines.push(`${childPad}${hclKey(key)} = ${renderExpressionHclValue(value[key], indent + 2)}`);
    }
    lines.push(`${pad}}`);
    return lines.join("\n");
  }
  throw new TypeError(`cannot render ${String(value)} as HCL`);
}

export function toTerraformJsonValue(value: unknown): unknown {
  if (value instanceof HclExpression) return `\${${value.expression}}`;
  if (value instanceof LosslessNumber) return value;
  if (Array.isArray(value)) return value.map(toTerraformJsonValue);
  if (record(value)) {
    return Object.fromEntries(sortedStrings(Object.keys(value)).map((key) => [key, toTerraformJsonValue(value[key])]));
  }
  return value;
}

type BindingTreeValue = string | BindingTree;

interface BindingTree {
  kind: "attributes" | "indices" | null;
  children: Map<string | number, BindingTreeValue>;
}

function emptyBindingTree(): BindingTree {
  return { kind: null, children: new Map() };
}

function bindingTreeChild(
  tree: BindingTree,
  part: string | number,
  binding: ExpressionBinding,
): BindingTree {
  const kind = typeof part === "number" ? "indices" : "attributes";
  if (tree.kind !== null && tree.kind !== kind) {
    throw new TypeError(`conflicting expression binding shape below ${binding.address}.${binding.path}`);
  }
  tree.kind = kind;
  const existing = tree.children.get(part);
  if (existing === undefined) {
    const child = emptyBindingTree();
    tree.children.set(part, child);
    return child;
  }
  if (typeof existing === "string") {
    throw new TypeError(`conflicting expression binding below ${binding.address}.${binding.path}`);
  }
  return existing;
}

function bindingTree(bindings: readonly ExpressionBinding[]): Readonly<Record<string, BindingTree>> {
  const output: Record<string, BindingTree> = Object.create(null) as Record<string, BindingTree>;
  for (const binding of bindings) {
    let current = output[binding.key] ?? (output[binding.key] = emptyBindingTree());
    for (const part of binding.pathParts.slice(0, -1)) {
      current = bindingTreeChild(current, part, binding);
    }
    const leaf = binding.pathParts.at(-1);
    if (leaf === undefined) throw new TypeError(`empty expression binding path for ${binding.address}`);
    const kind = typeof leaf === "number" ? "indices" : "attributes";
    if (current.kind !== null && current.kind !== kind) {
      throw new TypeError(`conflicting expression binding shape below ${binding.address}.${binding.path}`);
    }
    current.kind = kind;
    if (current.children.has(leaf)) {
      throw new TypeError(`conflicting expression binding below ${binding.address}.${binding.path}`);
    }
    current.children.set(leaf, binding.expression);
  }
  return output;
}

function renderMerge(baseExpression: string, tree: BindingTree, indent: number): string {
  if (tree.kind === "indices") return renderListEdits(baseExpression, tree, indent);
  const pad = " ".repeat(indent);
  const innerPad = " ".repeat(indent + 2);
  const lines = [`merge(${baseExpression}, {`];
  const names = sortedStrings([...tree.children.keys()].filter((part): part is string => typeof part === "string"));
  for (const name of names) {
    const value = tree.children.get(name);
    if (typeof value === "string") {
      lines.push(`${innerPad}${name} = ${value}`);
    } else if (value !== undefined) {
      const childReference = `${baseExpression}.${name}`;
      const childBase = value.kind === "indices"
        ? childReference
        : `try(${childReference}, null) == null ? {} : ${childReference}`;
      lines.push(`${innerPad}${name} = ${renderMerge(childBase, value, indent + 2)}`);
    }
  }
  lines.push(`${pad}})`);
  return lines.join("\n");
}

function renderListEdits(baseExpression: string, tree: BindingTree, indent: number): string {
  const indexes = [...tree.children.keys()]
    .filter((part): part is number => typeof part === "number")
    .sort((left, right) => left - right);
  let rendered = baseExpression;
  for (const index of indexes) {
    const value = tree.children.get(index);
    if (value === undefined) continue;
    const replacement = typeof value === "string"
      ? value
      : renderMerge(`${rendered}[${index}]`, value, indent + 2);
    rendered = `concat(slice(${rendered}, 0, ${index}), [${replacement}], slice(${rendered}, ${index + 1}, length(${rendered})))`;
  }
  return rendered;
}

/** Render the exact root-layer HCL merge contract used by Python gen_env. */
export function renderExpressionBindingsHcl(
  bindings: readonly ExpressionBinding[],
  options?: { readonly itemsVariable?: string; readonly localName?: string },
): string {
  if (bindings.length === 0) return "";
  const itemsVariable = options?.itemsVariable ?? "items";
  const localName = options?.localName ?? "infrawright_expression_bound_items";
  if (!PATH_SEGMENT.test(itemsVariable)) throw new TypeError("items_var must be a Terraform identifier");
  if (!PATH_SEGMENT.test(localName)) throw new TypeError("local_name must be a Terraform identifier");
  const sections = [
    "# GENERATED by engine.gen_env from expression bindings — do not edit.",
    "# Regenerate: make gen-env TENANT=<tenant>",
    "",
  ];
  const variables = expressionVariables(bindings);
  for (const name of sortedStrings(Object.keys(variables))) {
    sections.push(`variable "${name}" {`, "  type = string");
    if (variables[name] === true) sections.push("  sensitive = true");
    sections.push("}", "");
  }
  sections.push("locals {", `  ${localName} = merge(var.${itemsVariable}, {`);
  const trees = bindingTree(bindings);
  for (const key of sortedStrings(Object.keys(trees))) {
    const tree = trees[key];
    if (tree === undefined) continue;
    const rendered = renderMerge(`var.${itemsVariable}[${pythonJsonString(key)}]`, tree, 4)
      .replaceAll("\n", "\n    ");
    sections.push(`    ${pythonJsonString(key)} = ${rendered}`);
  }
  sections.push("  })", "}", "");
  return sections.join("\n");
}

export function mergeExpressionBindingLayers(
  layers: readonly (readonly ExpressionBinding[])[],
): readonly ExpressionBinding[] {
  const selected = new Map<string, ExpressionBinding>();
  for (const layer of layers) {
    for (const binding of layer) selected.set(JSON.stringify([binding.key, binding.path]), binding);
  }
  return [...selected.values()].sort((left, right) => {
    return comparePythonStrings(left.key, right.key) || comparePythonStrings(left.path, right.path);
  });
}

/** Return module names referenced outside quoted strings. */
export function expressionModuleTargets(expression: string): readonly string[] {
  const targets = new Set<string>();
  let index = 0;
  let inString = false;
  let escaped = false;
  while (index < expression.length) {
    const character = expression[index] ?? "";
    if (inString) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === '"') inString = false;
      index += 1;
      continue;
    }
    if (character === '"') {
      inString = true;
      index += 1;
      continue;
    }
    if (expression.startsWith("module.", index)) {
      const start = index + "module.".length;
      let end = start;
      while (end < expression.length && /[A-Za-z0-9_]/u.test(expression[end] ?? "")) end += 1;
      if (end > start) targets.add(expression.slice(start, end));
      index = end;
      continue;
    }
    index += 1;
  }
  return sortedStrings(targets);
}

export interface RemoteStateReference {
  readonly key: string;
  readonly resourceType: string;
  readonly root: string;
}

/** Return canonical Infrawright remote-state selectors outside quoted strings. */
export function expressionRemoteStateReferences(
  expression: string,
): readonly RemoteStateReference[] {
  const prefix = "data.terraform_remote_state.";
  const selected = new Map<string, RemoteStateReference>();
  let index = 0;
  let inString = false;
  let escaped = false;
  while (index < expression.length) {
    const character = expression[index] ?? "";
    if (inString) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === '"') inString = false;
      index += 1;
      continue;
    }
    if (character === '"') {
      inString = true;
      index += 1;
      continue;
    }
    if (!expression.startsWith(prefix, index)) {
      index += 1;
      continue;
    }
    const match = new RegExp(
      String.raw`^data\.terraform_remote_state\.(${IDENT})\.outputs\.infrawright_reference_ids\.(${IDENT})\[`,
      "u",
    ).exec(expression.slice(index));
    if (match === null) {
      throw new TypeError(
        "Infrawright terraform_remote_state expressions must use the canonical infrawright_reference_ids resource/key selector",
      );
    }
    const root = match[1] ?? "";
    const resourceType = match[2] ?? "";
    const quoted = parseHclQuotedString(expression, index + match[0].length);
    if (expression[quoted.end] !== "]") {
      throw new TypeError("cross-state reference key must end with a closing bracket");
    }
    let boundary = quoted.end + 1;
    while (
      boundary < expression.length
      && PYTHON_WHITESPACE_CHARACTER.test(expression[boundary] ?? "")
    ) {
      boundary += 1;
    }
    const next = expression[boundary];
    if (next !== undefined && next !== "," && next !== "]") {
      throw new TypeError(
        "Infrawright terraform_remote_state expressions must end after the canonical resource/key selector",
      );
    }
    const reference = { key: quoted.value, resourceType, root };
    selected.set(JSON.stringify([root, resourceType, quoted.value]), reference);
    index = quoted.end + 1;
  }
  return [...selected.values()].sort((left, right) => {
    return comparePythonStrings(left.root, right.root)
      || comparePythonStrings(left.resourceType, right.resourceType)
      || comparePythonStrings(left.key, right.key);
  });
}
