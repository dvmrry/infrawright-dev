import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import type { JsonObject } from "../metadata/validation.js";

const HTTP_METHODS: Readonly<Record<string, string>> = {
  "http.MethodDelete": "DELETE",
  "http.MethodGet": "GET",
  "http.MethodHead": "HEAD",
  "http.MethodOptions": "OPTIONS",
  "http.MethodPatch": "PATCH",
  "http.MethodPost": "POST",
  "http.MethodPut": "PUT",
};
const SERVICE_SUFFIXES = ["ServiceOp", "Service", "Client", "API"] as const;
const IGNORED_DIRECTORIES = new Set([".git", "test", "testdata", "tests"]);
const FORMAT_VERB = /%[a-zA-Z]/gu;

export interface SdkPathEvidence extends JsonObject {
  readonly client_symbol: string;
  readonly method: string;
  readonly path_template: string;
  readonly sdk_file: string;
  readonly source_role: "list" | "read" | null;
}

export interface SdkPathUnresolved extends JsonObject {
  readonly sdk_file: string;
  readonly reason: "method_not_detected" | "path_template_not_found";
}

export interface ExtractedSdkPaths {
  readonly evidence: Readonly<Record<string, SdkPathEvidence>>;
  readonly unresolved: Readonly<Record<string, SdkPathUnresolved>>;
}

function portableRelative(root: string, candidate: string): string {
  return path.relative(root, candidate).split(path.sep).join("/");
}

export async function discoverSdkGoFiles(sdkRoot: string | undefined): Promise<readonly string[]> {
  if (sdkRoot === undefined || sdkRoot === "") return [];
  const files: string[] = [];
  async function walk(directory: string): Promise<void> {
    let entries;
    try {
      entries = await readdir(directory, { withFileTypes: true });
    } catch {
      return;
    }
    entries.sort((left, right) => comparePythonStrings(left.name, right.name));
    for (const entry of entries) {
      const candidate = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        if (!IGNORED_DIRECTORIES.has(entry.name)) await walk(candidate);
      } else if (entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) {
        files.push(candidate);
      }
    }
  }
  await walk(sdkRoot);
  return files.sort((left, right) => comparePythonStrings(
    portableRelative(sdkRoot, left), portableRelative(sdkRoot, right),
  ));
}

export function goCodeWithoutComments(text: string): string {
  const output: string[] = [];
  let index = 0;
  while (index < text.length) {
    const current = text[index] ?? "";
    const next = text[index + 1] ?? "";
    if (current === "/" && next === "/") {
      const end = text.indexOf("\n", index + 2);
      if (end < 0) break;
      output.push("\n");
      index = end + 1;
    } else if (current === "/" && next === "*") {
      const end = text.indexOf("*/", index + 2);
      const removed = end < 0 ? text.slice(index) : text.slice(index, end + 2);
      output.push("\n".repeat([...removed].filter((char) => char === "\n").length));
      index = end < 0 ? text.length : end + 2;
    } else {
      output.push(current);
      index += 1;
    }
  }
  return output.join("");
}

function skipQuoted(text: string, start: number, quote: string): number {
  let index = start + 1;
  while (index < text.length) {
    if (text[index] === "\\" && quote !== "`") index += 2;
    else if (text[index] === quote) return index + 1;
    else index += 1;
  }
  return index;
}

export function extractSdkBasePaths(code: string): Readonly<Record<string, string>> {
  const output: Record<string, string> = {};
  for (const match of code.matchAll(/\bconst\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"/gu)) {
    const name = match[1];
    if (name?.endsWith("BasePath")) output[name] = match[2] ?? "";
  }
  for (const block of code.matchAll(/\bconst\s*\(([^)]*)\)/gsu)) {
    for (const line of (block[1] ?? "").split("\n")) {
      const match = /([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"/u.exec(line);
      if (match?.[1]?.endsWith("BasePath")) output[match[1]] = match[2] ?? "";
    }
  }
  return output;
}

export function sdkReceiverServiceName(receiverType: string): string {
  for (const suffix of SERVICE_SUFFIXES) {
    if (receiverType.endsWith(suffix) && receiverType.length > suffix.length) {
      return receiverType.slice(0, -suffix.length);
    }
  }
  return receiverType;
}

export function extractBalancedGoBody(
  code: string,
  braceIndex: number,
): readonly [string | undefined, number] {
  let depth = 0;
  const start = braceIndex + 1;
  let index = braceIndex;
  while (index < code.length) {
    const current = code[index] ?? "";
    if (["\"", "'", "`"].includes(current)) {
      index = skipQuoted(code, index, current);
      continue;
    }
    if (current === "{") depth += 1;
    else if (current === "}") {
      depth -= 1;
      if (depth === 0) return [code.slice(start, index), index + 1];
    }
    index += 1;
  }
  return [undefined, code.length];
}

export function splitSdkReceiverFunctions(code: string): readonly JsonObject[] {
  const functions: JsonObject[] = [];
  const receiverPattern = /\bfunc\s*\(([^)]*)\)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(/gu;
  for (const match of code.matchAll(receiverPattern)) {
    const receiver = (match[1] ?? "").trim();
    const type = /\*?([A-Za-z_][A-Za-z0-9_]*)\s*$/u.exec(receiver)?.[1];
    if (type === undefined || match.index === undefined) continue;
    const brace = code.indexOf("{", match.index + match[0].length);
    if (brace < 0) continue;
    const [body] = extractBalancedGoBody(code, brace);
    if (body !== undefined) functions.push({
      body,
      method_name: match[2],
      service: sdkReceiverServiceName(type),
    });
  }
  return functions;
}

export function splitGoCallArguments(text: string): readonly string[] {
  const args: string[] = [];
  let depth = 0;
  let index = 0;
  let start = 0;
  while (index < text.length) {
    const current = text[index] ?? "";
    if (["\"", "'", "`"].includes(current)) {
      index = skipQuoted(text, index, current);
      continue;
    }
    if ("([{".includes(current)) depth += 1;
    else if (")]}`".includes(current)) depth -= 1;
    else if (current === "," && depth === 0) {
      args.push(text.slice(start, index));
      start = index + 1;
    }
    index += 1;
  }
  args.push(text.slice(start));
  return args.length === 1 && args[0]?.trim() === "" ? [] : args;
}

function argumentPlaceholder(argument: string): string {
  const value = argument.trim();
  if (value === "") return "{param}";
  if (value.startsWith("\"") && value.endsWith("\"")) return value.slice(1, -1);
  return /^[A-Za-z_][A-Za-z0-9_]*$/u.test(value) ? `{${value}}` : "{param}";
}

export function renderSdkFormatTemplate(
  format: string,
  baseValue: string,
  extraArguments: readonly string[],
): string {
  const output: string[] = [];
  let cursor = 0;
  let argumentIndex = 0;
  let consumedBase = false;
  for (const match of format.matchAll(FORMAT_VERB)) {
    const index = match.index ?? 0;
    output.push(format.slice(cursor, index));
    if (!consumedBase) {
      output.push(baseValue);
      consumedBase = true;
    } else {
      output.push(argumentPlaceholder(extraArguments[argumentIndex] ?? ""));
      argumentIndex += 1;
    }
    cursor = index + match[0].length;
  }
  output.push(format.slice(cursor));
  return output.join("");
}

function renderSprintf(
  expression: string,
  basePaths: Readonly<Record<string, string>>,
  currentPath: string | undefined,
): string | undefined {
  if (!expression.startsWith("fmt.Sprintf(") || !expression.endsWith(")")) return undefined;
  const args = splitGoCallArguments(expression.slice("fmt.Sprintf(".length, -1));
  const rawFormat = args[0]?.trim() ?? "";
  if (!rawFormat.startsWith("\"") || !rawFormat.endsWith("\"") || args.length < 2) return undefined;
  const format = rawFormat.slice(1, -1);
  const callArgs = args.slice(1).map((argument) => argument.trim());
  const first = callArgs[0] ?? "";
  if (basePaths[first] !== undefined) return renderSdkFormatTemplate(format, basePaths[first], callArgs.slice(1));
  if (currentPath !== undefined && first === "path") return renderSdkFormatTemplate(format, currentPath, callArgs.slice(1));
  return undefined;
}

export function findSdkPathAssignments(
  body: string,
  basePaths: Readonly<Record<string, string>>,
): readonly string[] {
  const assignments: string[] = [];
  let current: string | undefined;
  for (const match of body.matchAll(/\b([A-Za-z_][A-Za-z0-9_]*)\s*:?=\s*([^;\n]+)/gu)) {
    if (match[1] !== "path") continue;
    const expression = (match[2] ?? "").trim();
    const rendered = basePaths[expression] ?? renderSprintf(expression, basePaths, current);
    current = rendered;
    if (rendered !== undefined) assignments.push(rendered);
  }
  return assignments;
}

export function detectSdkRequestMethod(body: string): string | undefined {
  for (const match of body.matchAll(/\bNewRequest\s*\(([^)]*)\)/gu)) {
    const args = splitGoCallArguments(match[1] ?? "");
    if (args.length < 2) continue;
    const method = args[1]?.trim() ?? "";
    if (HTTP_METHODS[method] !== undefined) return HTTP_METHODS[method];
    if (method.startsWith("\"") && method.endsWith("\"")) return method.slice(1, -1).toUpperCase();
  }
  return undefined;
}

function methodRole(method: string): "list" | "read" | null {
  const lower = method.toLowerCase();
  if (["Get", "Read", "Fetch"].includes(method) || ["get", "read", "fetch"].some((prefix) => lower.startsWith(prefix))) return "read";
  if (["List", "Search"].includes(method) || ["list", "search"].some((prefix) => lower.startsWith(prefix))) return "list";
  return null;
}

export async function extractSdkPaths(sdkRoot: string | undefined): Promise<ExtractedSdkPaths> {
  const evidence: Record<string, SdkPathEvidence> = {};
  const unresolved: Record<string, SdkPathUnresolved> = {};
  if (sdkRoot === undefined || sdkRoot === "") return { evidence, unresolved };
  const decoder = new TextDecoder("utf-8", { fatal: true });
  for (const filename of await discoverSdkGoFiles(sdkRoot)) {
    let text: string;
    try {
      text = decoder.decode(await readFile(filename));
    } catch {
      continue;
    }
    const code = goCodeWithoutComments(text);
    const basePaths = extractSdkBasePaths(code);
    const sdkFile = portableRelative(sdkRoot, filename);
    for (const item of splitSdkReceiverFunctions(code)) {
      const service = String(item.service);
      const methodName = String(item.method_name);
      const symbol = `${service}.${methodName}`;
      const body = String(item.body);
      const assignments = findSdkPathAssignments(body, basePaths);
      const method = detectSdkRequestMethod(body);
      if (assignments.length === 0 && method === undefined) continue;
      if (assignments.length === 0) {
        unresolved[symbol] = { reason: "path_template_not_found", sdk_file: sdkFile };
      } else if (method === undefined) {
        unresolved[symbol] = { reason: "method_not_detected", sdk_file: sdkFile };
      } else {
        evidence[symbol] = {
          client_symbol: symbol,
          method,
          path_template: assignments.at(-1) as string,
          sdk_file: sdkFile,
          source_role: methodRole(methodName),
        };
      }
    }
  }
  return {
    evidence: Object.fromEntries(sortedStrings(Object.keys(evidence)).map((key) => [key, evidence[key] as SdkPathEvidence])),
    unresolved: Object.fromEntries(sortedStrings(Object.keys(unresolved)).map((key) => [key, unresolved[key] as SdkPathUnresolved])),
  };
}

export function normalizeSdkPathSegments(value: string): readonly string[] {
  return value.replace(/^\/+|\/+$/gu, "").split("/").filter(Boolean).map((part) => {
    return part.startsWith("{") && part.endsWith("}") ? "{param}" : part.toLowerCase();
  });
}

export function sdkPathSegmentsMatch(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((part, index) => {
    const other = right[index];
    return part === other || part === "{param}" || other === "{param}";
  });
}

export function matchOpenApiBySdkPath(
  operations: readonly JsonObject[],
  pathTemplate: string,
  method = "GET",
): { readonly ambiguous: readonly JsonObject[]; readonly operation?: JsonObject } {
  const template = normalizeSdkPathSegments(pathTemplate);
  const matches = operations.filter((operation) => operation.method === method
    && typeof operation.path === "string"
    && sdkPathSegmentsMatch(template, normalizeSdkPathSegments(operation.path)));
  if (matches.length === 1) return { ambiguous: [], operation: matches[0] as JsonObject };
  return { ambiguous: matches.length > 1 ? matches : [] };
}

export function matchSdkEvidenceToOpenApi(
  extracted: ExtractedSdkPaths,
  operations: readonly JsonObject[],
): JsonObject {
  const matched: JsonObject[] = [];
  const unresolved: JsonObject[] = Object.entries(extracted.unresolved).map(([symbol, item]) => ({
    client_symbol: symbol, ...item,
  }));
  for (const symbol of sortedStrings(Object.keys(extracted.evidence))) {
    const evidence = extracted.evidence[symbol] as SdkPathEvidence;
    const result = matchOpenApiBySdkPath(operations, evidence.path_template, evidence.method);
    if (result.operation !== undefined) matched.push({
      ...evidence, openapi_operation: result.operation,
    });
    else unresolved.push({
      ...evidence,
      candidates: result.ambiguous,
      reason: result.ambiguous.length > 0 ? "ambiguous_openapi_path" : "openapi_path_not_found",
    });
  }
  unresolved.sort((left, right) => comparePythonStrings(String(left.client_symbol), String(right.client_symbol)));
  return { matched, unresolved };
}
