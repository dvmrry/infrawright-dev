import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { isObject, type JsonObject } from "../metadata/validation.js";

const IGNORED = new Set([".git", ".terraform", "acceptance", "node_modules", "vendor"]);
const SDK_RECEIVERS = new Set(["api", "client"]);

function object(value: unknown): JsonObject {
  return isObject(value) ? value : {};
}

function array(value: unknown): readonly JsonObject[] {
  return Array.isArray(value) ? value.filter(isObject) : [];
}

export function canonicalSourceSymbol(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9]/gu, "");
}

function relative(root: string, filename: string): string {
  return path.relative(root, filename).split(path.sep).join("/");
}

async function directoryExists(candidate: string): Promise<boolean> {
  try { return (await stat(candidate)).isDirectory(); } catch { return false; }
}

async function fileExists(candidate: string): Promise<boolean> {
  try { return (await stat(candidate)).isFile(); } catch { return false; }
}

export async function discoverProviderGoFiles(root: string): Promise<readonly string[]> {
  const files: string[] = [];
  async function walk(directory: string): Promise<void> {
    let entries;
    try { entries = await readdir(directory, { withFileTypes: true }); } catch { return; }
    entries.sort((a, b) => comparePythonStrings(a.name, b.name));
    for (const entry of entries) {
      const candidate = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        if (!IGNORED.has(entry.name)) await walk(candidate);
      } else if (entry.isFile() && entry.name.endsWith(".go")
        && !entry.name.endsWith("_test.go") && entry.name !== "sweep.go") files.push(candidate);
    }
  }
  await walk(root);
  return files.sort((a, b) => comparePythonStrings(relative(root, a), relative(root, b)));
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

export function goCodeWithoutCommentsAndStrings(text: string): string {
  const output: string[] = [];
  let index = 0;
  while (index < text.length) {
    const current = text[index] ?? ""; const next = text[index + 1] ?? "";
    if (current === "/" && next === "/") {
      const end = text.indexOf("\n", index + 2); if (end < 0) break;
      output.push("\n"); index = end + 1;
    } else if (current === "/" && next === "*") {
      const end = text.indexOf("*/", index + 2);
      const removed = end < 0 ? text.slice(index) : text.slice(index, end + 2);
      output.push("\n".repeat((removed.match(/\n/gu) ?? []).length)); index = end < 0 ? text.length : end + 2;
    } else if (["\"", "'", "`"].includes(current)) {
      const end = skipQuoted(text, index, current);
      output.push("\n".repeat((text.slice(index, end).match(/\n/gu) ?? []).length)); index = end;
    } else { output.push(current); index += 1; }
  }
  return output.join("");
}

export function goCodeWithoutComments(text: string): string {
  const output: string[] = [];
  let index = 0;
  while (index < text.length) {
    const current = text[index] ?? ""; const next = text[index + 1] ?? "";
    if (current === "/" && next === "/") {
      const end = text.indexOf("\n", index + 2); if (end < 0) break;
      output.push("\n"); index = end + 1;
    } else if (current === "/" && next === "*") {
      const end = text.indexOf("*/", index + 2);
      const removed = end < 0 ? text.slice(index) : text.slice(index, end + 2);
      output.push("\n".repeat((removed.match(/\n/gu) ?? []).length)); index = end < 0 ? text.length : end + 2;
    } else { output.push(current); index += 1; }
  }
  return output.join("");
}

export function goIdentifierTokens(text: string): ReadonlySet<string> {
  const tokens = new Set<string>();
  let index = 0;
  while (index < text.length) {
    const current = text[index] ?? ""; const next = text[index + 1] ?? "";
    if (current === "/" && next === "/") { index = text.indexOf("\n", index + 2); if (index < 0) break; continue; }
    if (current === "/" && next === "*") { const end = text.indexOf("*/", index + 2); index = end < 0 ? text.length : end + 2; continue; }
    if (["\"", "'", "`"].includes(current)) { index = skipQuoted(text, index, current); continue; }
    if (current === "_" || /\p{L}/u.test(current)) {
      const start = index; index += 1;
      while (index < text.length && ((text[index] ?? "") === "_" || /[\p{L}\p{N}]/u.test(text[index] ?? ""))) index += 1;
      const token = canonicalSourceSymbol(text.slice(start, index)); if (token !== "") tokens.add(token);
    } else index += 1;
  }
  return tokens;
}

export function identifierWords(value: string): readonly string[] {
  return value.replace(/([a-z0-9])([A-Z])/gu, "$1 $2").replace(/([A-Z]+)([A-Z][a-z])/gu, "$1 $2")
    .replace(/[^A-Za-z0-9]+/gu, " ").split(/\s+/u).filter(Boolean).map((word) => word.toLowerCase());
}

export function sdkMethodRole(method: string): "list" | "read" | null {
  const lower = method.toLowerCase(); const words = identifierWords(method);
  if (["Get", "Read", "Fetch"].includes(method) || ["get", "read", "fetch", "retrieve"].some((x) => lower.startsWith(x)) || ["get", "read", "retrieve"].some((x) => lower.endsWith(x))) return "read";
  if (["List", "Search"].includes(method) || ["list", "search"].some((x) => lower.startsWith(x) || lower.endsWith(x))) return "list";
  if (words.some((word) => word === "list" || word === "search")) return "list";
  if (words.some((word) => ["fetch", "get", "read", "retrieve"].includes(word))) return "read";
  return null;
}

export function sdkClientCalls(text: string, requireRole = true): readonly JsonObject[] {
  const code = goCodeWithoutCommentsAndStrings(text); const calls: Record<string, JsonObject> = {};
  for (const match of code.matchAll(/\b((?:[A-Za-z_][A-Za-z0-9_]*\.)*(?:api|client)(?:\.[A-Za-z_][A-Za-z0-9_]*){1,})\s*\(/gu)) {
    const parts = (match[1] ?? "").split(".");
    const indexes = parts.map((part, i) => SDK_RECEIVERS.has(part) ? i : -1).filter((i) => i >= 0);
    const suffix = parts.slice((indexes.at(-1) ?? parts.length) + 1); if (suffix.length === 0) continue;
    const method = suffix.at(-1) as string; const role = sdkMethodRole(method); if (role === null && requireRole) continue;
    const chain = suffix.slice(0, -1); const symbol = [...chain, method].join(".");
    calls[symbol] = { chain, client_symbol: symbol, method, source_role: role };
  }
  return sortedStrings(Object.keys(calls)).map((key) => calls[key] as JsonObject);
}

export function goImportAliases(text: string): Readonly<Record<string, string>> {
  const code = goCodeWithoutComments(text); const aliases: Record<string, string> = {};
  const blocks = [...code.matchAll(/\bimport\s*\((.*?)\)/gsu)].map((match) => match[1] ?? "");
  for (const match of code.matchAll(/\bimport\s+([A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"/gu)) {
    const importPath = match[2] as string; aliases[(match[1] ?? "").trim() || importPath.split("/").at(-1) as string] = importPath;
  }
  for (const block of blocks) for (const line of block.split("\n")) {
    const match = /^\s*([A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"/u.exec(line);
    if (match?.[2]) aliases[(match[1] ?? "").trim() || match[2].split("/").at(-1) as string] = match[2];
  }
  return aliases;
}

function packageMethodRole(method: string): "list" | "read" | null {
  const lower = method.toLowerCase(); const words = identifierWords(method);
  if (["get", "read", "fetch", "retrieve"].some((x) => lower.startsWith(x))) return lower.includes("all") || lower.includes("list") ? "list" : "read";
  if (["list", "search"].some((x) => lower.startsWith(x))) return "list";
  if (words.some((word) => word === "list" || word === "search")) return "list";
  return words.some((word) => ["fetch", "get", "read", "retrieve"].includes(word)) ? "read" : null;
}

function externalImport(importPath: string): boolean { return (importPath.split("/")[0] ?? "").includes("."); }

async function localImportDirectory(root: string, importPath: string | undefined): Promise<string | undefined> {
  if (!importPath) return undefined; const parts = importPath.split("/");
  for (let index = 0; index < parts.length; index += 1) {
    const candidate = path.join(root, ...parts.slice(index)); if (await directoryExists(candidate)) return candidate;
  }
  return undefined;
}

export async function packageCalls(text: string, root: string): Promise<readonly JsonObject[]> {
  const code = goCodeWithoutCommentsAndStrings(text); const imports = goImportAliases(text); const calls: Record<string, JsonObject> = {};
  for (const match of code.matchAll(/\b([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\(/gu)) {
    const pkg = match[1] as string; const method = match[2] as string; const importPath = imports[pkg];
    if (!importPath || !externalImport(importPath) || await localImportDirectory(root, importPath)) continue;
    const role = packageMethodRole(method); if (role === null) continue; const symbol = `${pkg}.${method}`;
    calls[symbol] = { client_symbol: symbol, method, package: pkg, package_path: importPath, source_role: role };
  }
  return sortedStrings(Object.keys(calls)).map((key) => calls[key] as JsonObject);
}

function decodeGoLiteral(value: string): string {
  if (value.startsWith("`") && value.endsWith("`")) return value.slice(1, -1);
  const inner = value.startsWith("\"") && value.endsWith("\"") ? value.slice(1, -1) : value;
  const encoded = Buffer.from(inner, "utf8").toString("latin1"); const output: string[] = [];
  const simple: Readonly<Record<string, string>> = { "\\": "\\", "\"": "\"", "'": "'", a: "\x07", b: "\b", f: "\f", n: "\n", r: "\r", t: "\t", v: "\v" };
  for (let index = 0; index < encoded.length; index += 1) {
    const current = encoded[index] as string; if (current !== "\\") { output.push(current); continue; }
    const escape = encoded[index + 1]; if (escape === undefined) throw new Error("truncated escape in Go string literal");
    if (simple[escape] !== undefined) { output.push(simple[escape]); index += 1; continue; }
    if (escape === "\n") { index += 1; continue; }
    const widths: Readonly<Record<string, number>> = { U: 8, u: 4, x: 2 }; const width = widths[escape];
    if (width !== undefined) {
      const digits = encoded.slice(index + 2, index + 2 + width); if (digits.length !== width || !/^[0-9A-Fa-f]+$/u.test(digits)) throw new Error(`invalid \\${escape} escape in Go string literal`);
      const codePoint = Number.parseInt(digits, 16); if (codePoint > 0x10ffff) throw new Error("invalid Unicode escape in Go string literal");
      output.push(String.fromCodePoint(codePoint)); index += width + 1; continue;
    }
    if (/[0-7]/u.test(escape)) {
      const digits = encoded.slice(index + 1).match(/^[0-7]{1,3}/u)?.[0] as string; output.push(String.fromCodePoint(Number.parseInt(digits, 8))); index += digits.length; continue;
    }
    output.push("\\", escape); index += 1;
  }
  return output.join("");
}

export function normalizeRawRestPath(value: string): string {
  let output = value.trim().replace(/%[#0 +\-]*[0-9]*(?:\.[0-9]+)?[bcdefgosqxXUvT]/gu, "{arg}");
  if (!output.startsWith("/")) output = `/${output}`; return output.replace(/\/{2,}/gu, "/");
}

export function rawRestCalls(text: string): readonly JsonObject[] {
  const code = goCodeWithoutComments(text); const calls = new Map<string, JsonObject>();
  const literal = '(?:"(?:\\\\.|[^"\\\\])*"|`[^`]*`)';
  const patterns = [
    new RegExp(`\\b(?<symbol>(?:[A-Za-z_][A-Za-z0-9_]*\\.)+NewRequest)\\s*\\(\\s*(?:"GET"|http\\.MethodGet)\\s*,\\s*fmt\\.Sprintf\\s*\\(\\s*(?<path>${literal})`, "gsu"),
    new RegExp(`\\b(?<symbol>(?:[A-Za-z_][A-Za-z0-9_]*\\.)+NewRequest)\\s*\\(\\s*(?:"GET"|http\\.MethodGet)\\s*,\\s*(?<path>${literal})`, "gsu"),
  ];
  for (const pattern of patterns) for (const match of code.matchAll(pattern)) {
    const symbol = match.groups?.symbol ?? ""; const restPath = normalizeRawRestPath(decodeGoLiteral(match.groups?.path ?? ""));
    calls.set(`${symbol}\0${restPath}`, { client_symbol: `${symbol} GET ${restPath}`, method: "GET", path: restPath, source_role: "read" });
  }
  return [...calls.entries()].sort(([a], [b]) => comparePythonStrings(a, b)).map(([, value]) => value);
}

export function isGraphqlSource(text: string): boolean {
  const code = goCodeWithoutComments(text); return /\bgithubv4\b/u.test(code) || /\bgraphql\s*:/u.test(code) || code.includes("github.com/shurcooL/githubv4");
}

interface SourceEntry { readonly path: string; readonly basename: string; readonly text: string }

async function sourceEntries(root: string): Promise<readonly SourceEntry[]> {
  const decoder = new TextDecoder("utf-8", { fatal: true }); const output: SourceEntry[] = [];
  for (const filename of await discoverProviderGoFiles(root)) {
    try { output.push({ basename: path.basename(filename), path: filename, text: decoder.decode(await readFile(filename)) }); } catch { continue; }
  }
  return output;
}

async function packageResourceFiles(directory: string): Promise<readonly string[]> {
  let entries; try { entries = await readdir(directory, { withFileTypes: true }); } catch { return []; }
  return entries.filter((entry) => entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go") && entry.name !== "sweep.go" && !entry.name.includes("datasource") && !entry.name.startsWith("data_source_"))
    .map((entry) => path.join(directory, entry.name)).sort(comparePythonStrings);
}

function bareResource(resource: string, prefix: string): string {
  return prefix !== "" && resource.startsWith(`${prefix}_`) ? resource.slice(prefix.length + 1) : resource;
}

export async function resourceFilesFromText(root: string, resources: readonly string[], prefix = ""): Promise<Readonly<Record<string, readonly string[]>>> {
  const entries = await sourceEntries(root); const output: Record<string, string[]> = Object.fromEntries(resources.map((resource) => [resource, []]));
  const functions = new Map<string, Set<string>>();
  for (const entry of entries) for (const match of goCodeWithoutCommentsAndStrings(entry.text).matchAll(/\bfunc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(/gu)) {
    const paths = functions.get(match[1] as string) ?? new Set<string>(); paths.add(entry.path); functions.set(match[1] as string, paths);
  }
  const registrations: Record<string, Set<string>> = Object.fromEntries(resources.map((resource) => [resource, new Set<string>()]));
  for (const entry of entries) {
    for (const match of entry.text.matchAll(/"([^"]+)"/gu)) if (output[match[1] ?? ""] !== undefined) output[match[1] as string]?.push(entry.path);
    const code = goCodeWithoutComments(entry.text);
    for (const match of code.matchAll(/"([^"]+)"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(/gu)) {
      const resource = match[1] ?? ""; const constructor = match[2] ?? ""; if (registrations[resource] === undefined || canonicalSourceSymbol(constructor).startsWith("datasource")) continue;
      for (const filename of functions.get(constructor) ?? []) registrations[resource]?.add(filename);
    }
    const imports = goImportAliases(entry.text);
    for (const match of code.matchAll(/"([^"]+)"\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\s*\(/gu)) {
      const resource = match[1] ?? ""; const constructor = match[3] ?? ""; if (registrations[resource] === undefined || canonicalSourceSymbol(constructor).startsWith("datasource")) continue;
      const directory = await localImportDirectory(root, imports[match[2] ?? ""]); if (directory) for (const filename of await packageResourceFiles(directory)) registrations[resource]?.add(filename);
    }
  }
  for (const resource of resources) {
    const exact = new Set<string>(); const bare = bareResource(resource, prefix);
    const names = new Set([`resource_${resource}.go`, `resource_${bare}.go`]);
    for (const entry of entries) if (names.has(entry.basename)) exact.add(entry.path);
    const service = path.join(root, "internal", "services", bare); if (await directoryExists(service)) for (const file of await discoverProviderGoFiles(service)) exact.add(file);
    for (const directory of ["resources", "datasources"]) { const candidate = path.join(root, "internal", "framework", directory, `${bare}.go`); if (await fileExists(candidate)) exact.add(candidate); }
    let paths = output[resource] ?? [];
    if (exact.size > 0 || (registrations[resource]?.size ?? 0) > 0) paths = paths.filter((filename) => !["provider.go", "main.go"].includes(path.basename(filename)) && !path.basename(filename).startsWith("data_source_"));
    paths.push(...exact, ...(registrations[resource] ?? []));
    const callbacks = new Set<string>();
    for (const filename of paths) {
      let text: string; try { text = await readFile(filename, "utf8"); } catch { continue; }
      for (const match of goCodeWithoutCommentsAndStrings(text).matchAll(/\bRead(?:Context|WithoutTimeout)?\s*:\s*([A-Za-z_][A-Za-z0-9_]*)/gu)) {
        for (const callback of functions.get(match[1] ?? "") ?? []) if (path.dirname(callback) === path.dirname(filename)) callbacks.add(callback);
      }
    }
    output[resource] = sortedStrings(new Set([...paths, ...callbacks]));
  }
  return output;
}

function factPath(root: string, value: unknown): string | undefined {
  if (typeof value !== "string" || value === "") return undefined;
  return path.normalize(path.isAbsolute(value) ? value : path.join(root, value.replaceAll("/", path.sep)));
}

export async function resourceFilesFromFacts(root: string, resources: readonly string[], prefix: string, facts: JsonObject): Promise<Readonly<Record<string, readonly string[]>>> {
  const output: Record<string, string[]> = Object.fromEntries(resources.map((resource) => [resource, []]));
  const registrationPaths: Record<string, Set<string>> = Object.fromEntries(resources.map((resource) => [resource, new Set<string>()]));
  const functions = new Map<string, Set<string>>();
  for (const item of array(facts.functions)) { const filename = factPath(root, item.file); if (typeof item.name === "string" && filename) { const set = functions.get(item.name) ?? new Set<string>(); set.add(filename); functions.set(item.name, set); } }
  const imports: Record<string, Record<string, string>> = {};
  const basenames = new Map<string, string[]>();
  for (const file of array(facts.files)) { const filename = factPath(root, file.path); if (!filename) continue; const rel = String(file.path ?? "").replaceAll("\\", "/"); imports[rel] = Object.fromEntries(array(file.imports).filter((item) => item.name && item.path).map((item) => [item.name, item.path])); const list = basenames.get(path.basename(filename)) ?? []; list.push(filename); basenames.set(path.basename(filename), list); }
  for (const reference of array(facts.resource_references)) { const resource = String(reference.resource ?? ""); const filename = factPath(root, reference.file); if (output[resource] && filename) output[resource]?.push(filename); }
  for (const registration of array(facts.resource_registrations)) {
    const resource = String(registration.resource ?? ""); const constructor = String(registration.constructor ?? ""); if (!output[resource] || canonicalSourceSymbol(constructor).startsWith("datasource")) continue;
    const pkg = String(registration.package ?? "").split(".")[0] ?? ""; const rel = String(registration.file ?? "").replaceAll("\\", "/"); const directory = await localImportDirectory(root, imports[rel]?.[pkg]);
    if (directory) for (const filename of await packageResourceFiles(directory)) registrationPaths[resource]?.add(filename);
    else for (const filename of functions.get(constructor) ?? []) registrationPaths[resource]?.add(filename);
  }
  for (const resource of resources) {
    const exact = new Set<string>(); const bare = bareResource(resource, prefix);
    for (const name of [`resource_${resource}.go`, `resource_${bare}.go`]) for (const filename of basenames.get(name) ?? []) exact.add(filename);
    for (const candidate of [path.join(root, `resource_${resource}.go`), path.join(root, resource.split("_", 1)[0] ?? "", `resource_${resource}.go`)]) if (await fileExists(candidate)) exact.add(candidate);
    const service = path.join(root, "internal", "services", bare); if (await directoryExists(service)) for (const file of await discoverProviderGoFiles(service)) exact.add(file);
    for (const directory of ["resources", "datasources"]) { const candidate = path.join(root, "internal", "framework", directory, `${bare}.go`); if (await fileExists(candidate)) exact.add(candidate); }
    let paths = [...(output[resource] ?? []), ...exact, ...(registrationPaths[resource] ?? [])];
    if (exact.size > 0 || (registrationPaths[resource]?.size ?? 0) > 0) paths = paths.filter((filename) => !["provider.go", "main.go"].includes(path.basename(filename)) && !path.basename(filename).startsWith("data_source_"));
    const normalized = new Set(paths.map((filename) => path.normalize(filename)));
    for (const callback of array(facts.read_callbacks)) { const file = factPath(root, callback.file); if (!file || !normalized.has(file)) continue; for (const target of functions.get(String(callback.function ?? "")) ?? []) if (path.dirname(target) === path.dirname(file)) paths.push(target); }
    output[resource] = sortedStrings(new Set(paths));
  }
  return output;
}

export async function textEvidence(root: string, files: readonly string[]): Promise<JsonObject> {
  const texts = await Promise.all(files.map((filename) => readFile(filename, "utf8"))); const joined = texts.join("\n");
  return { backend: "text_scan", graphql_source: isGraphqlSource(joined), identifiers: goIdentifierTokens(joined), package_calls: await packageCalls(joined, root), raw_rest_calls: rawRestCalls(joined), sdk_calls: sdkClientCalls(joined) };
}

function selectedFactFiles(files: readonly string[]): ReadonlySet<string> { return new Set(files.map((filename) => filename.replaceAll("\\", "/"))); }
function selectorParts(item: JsonObject): readonly string[] {
  return Array.isArray(item.parts) && item.parts.length > 0 ? item.parts.map(String) : String(item.symbol ?? "").split(".");
}

export async function factsEvidence(root: string, files: readonly string[], facts: JsonObject): Promise<JsonObject> {
  const selected = selectedFactFiles(files); const identifiers = new Set<string>();
  const sdk: Record<string, JsonObject> = {}; const packages: Record<string, JsonObject> = {}; const raw: Record<string, JsonObject> = {};
  for (const item of [...array(facts.functions), ...array(facts.identifier_references)]) if (selected.has(String(item.file ?? "").replaceAll("\\", "/"))) { const token = canonicalSourceSymbol(String(item.name ?? "")); if (token) identifiers.add(token); }
  for (const item of array(facts.selector_calls)) {
    if (!selected.has(String(item.file ?? "").replaceAll("\\", "/"))) continue; const parts = selectorParts(item);
    identifiers.add(canonicalSourceSymbol(String(item.symbol ?? ""))); for (const part of parts) identifiers.add(canonicalSourceSymbol(part));
    const indexes = parts.map((part, i) => SDK_RECEIVERS.has(part) ? i : -1).filter((i) => i >= 0); const suffix = parts.slice((indexes.at(-1) ?? parts.length) + 1); if (suffix.length === 0) continue;
    const method = suffix.at(-1) as string; const role = sdkMethodRole(method); if (role === null) continue; const chain = suffix.slice(0, -1); const symbol = [...chain, method].join("."); sdk[symbol] = { chain, client_symbol: symbol, method, source_role: role };
  }
  for (const item of array(facts.package_calls)) {
    if (!selected.has(String(item.file ?? "").replaceAll("\\", "/"))) continue; const pkg = String(item.package ?? ""); const importPath = String(item.import_path ?? ""); const method = String(item.method ?? "");
    for (const value of [method, String(item.symbol ?? "")]) { const token = canonicalSourceSymbol(value); if (token) identifiers.add(token); }
    if (!pkg || !importPath || !method || !externalImport(importPath) || await localImportDirectory(root, importPath)) continue;
    const role = packageMethodRole(method); if (role === null) continue; const symbol = String(item.symbol ?? `${pkg}.${method}`); packages[symbol] = { client_symbol: symbol, method, package: pkg, package_path: importPath, source_role: role };
  }
  for (const item of array(facts.raw_rest_calls)) {
    if (!selected.has(String(item.file ?? "").replaceAll("\\", "/"))) continue;
    const method = String(item.method ?? "").toUpperCase();
    const rawPath = String(item.path ?? "");
    if (!method || !rawPath) continue;
    const restPath = normalizeRawRestPath(rawPath);
    const symbol = String(item.symbol ?? "NewRequest");
    raw[`${symbol}\0${method}\0${restPath}`] = { client_symbol: `${symbol} ${method} ${restPath}`, method, path: restPath, source_role: "read" };
  }
  let graphql = array(facts.files).some((file) => selected.has(String(file.path ?? "").replaceAll("\\", "/")) && array(file.imports).some((item) => String(item.path ?? "").includes("githubv4")));
  graphql ||= array(facts.selector_calls).some((item) => selected.has(String(item.file ?? "").replaceAll("\\", "/")) && String(item.symbol ?? "").toLowerCase().includes("githubv4"));
  return { backend: "ast_facts", graphql_source: graphql, identifiers, package_calls: sortedStrings(Object.keys(packages)).map((key) => packages[key]), raw_rest_calls: sortedStrings(Object.keys(raw)).map((key) => raw[key]), sdk_calls: sortedStrings(Object.keys(sdk)).map((key) => sdk[key]) };
}

export function sdkClientCallsFromFacts(
  files: readonly string[], facts: JsonObject, requireRole = true,
): readonly JsonObject[] {
  const selected = selectedFactFiles(files); const calls: Record<string, JsonObject> = {};
  for (const item of array(facts.selector_calls)) {
    if (!selected.has(String(item.file ?? "").replaceAll("\\", "/"))) continue;
    const parts = selectorParts(item);
    const indexes = parts.map((part, index) => SDK_RECEIVERS.has(part) ? index : -1).filter((index) => index >= 0);
    if (indexes.length === 0) continue;
    const suffix = parts.slice((indexes.at(-1) as number) + 1); if (suffix.length === 0) continue;
    const method = suffix.at(-1) as string; const role = sdkMethodRole(method); if (role === null && requireRole) continue;
    const chain = suffix.slice(0, -1); const symbol = [...chain, method].join(".");
    calls[symbol] = { chain, client_symbol: symbol, method, source_role: role };
  }
  return sortedStrings(Object.keys(calls)).map((key) => calls[key] as JsonObject);
}

export function relativeSourceFiles(root: string, files: readonly string[]): readonly string[] {
  return files.map((filename) => relative(root, filename));
}
