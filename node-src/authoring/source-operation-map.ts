import { readFile } from "node:fs/promises";
import path from "node:path";

import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { baseResourceTokens, providerFromSchema } from "./openapi-resource-map.js";
import {
  factsEvidence,
  identifierWords,
  relativeSourceFiles,
  resourceFilesFromFacts,
  resourceFilesFromText,
  sdkClientCalls,
  sdkClientCallsFromFacts,
  textEvidence,
} from "./provider-source-evidence.js";
import { extractSdkPaths, matchOpenApiBySdkPath } from "./sdk-path-evidence.js";

const AMBIGUITY_DELTA = 5;
const SOURCE_CALL_AMBIGUITY_DELTA = 15;
const READ_DETAIL_AMBIGUITY_DELTA = 25;
const SDK_SCORE_FLOOR = 35;
const PACKAGE_SCORE_FLOOR = 35;
const RAW_SCORE_FLOOR = 70;
const SDK_PATH_SCORE = 1000;
const RELATIONSHIP_TOKENS = new Set([
  "assignment", "collaborator", "collaborators", "dependency", "dependencies",
  "mapping", "members", "membership", "repositories", "subscriber", "subscribers", "topics",
]);

function object(value: unknown): JsonObject { return isObject(value) ? value : {}; }
function array(value: unknown): readonly JsonObject[] { return Array.isArray(value) ? value.filter(isObject) : []; }
function canonical(value: string): string { return value.toLowerCase().replace(/[^a-z0-9]/gu, ""); }

export function operationAliases(operationId: string): readonly string[] {
  const raw = new Set([operationId]);
  for (const [pattern, replacement] of [["retrieve", "read"], ["retrieve", "get"], ["read", "retrieve"], ["get", "retrieve"]] as const) {
    raw.add(operationId.replace(new RegExp(pattern, "giu"), replacement));
  }
  for (const alias of [...raw]) if (alias.toLowerCase().startsWith("route")) raw.add(alias.slice(5));
  const aliases = new Set([...raw].map(canonical)); for (const alias of [...aliases]) aliases.add(`${alias}withresponse`);
  return sortedStrings([...aliases].filter(Boolean));
}

export function openApiOperationInventory(spec: JsonObject): readonly JsonObject[] {
  const output: JsonObject[] = [];
  for (const apiPath of sortedStrings(Object.keys(object(spec.paths)))) {
    for (const methodName of sortedStrings(Object.keys(object(object(spec.paths)[apiPath])))) {
      const operation = object(object(object(spec.paths)[apiPath])[methodName]);
      if (!isObject(object(spec.paths)[apiPath]) || Object.keys(operation).length === 0 && !isObject(object(object(spec.paths)[apiPath])[methodName])) continue;
      const method = methodName.toUpperCase(); const explicit = typeof operation.operationId === "string" && operation.operationId !== "";
      const operationId = explicit ? String(operation.operationId) : `${method} ${apiPath}`;
      output.push({ aliases: operationAliases(operationId), method, operation_id: operationId, operation_id_source: explicit ? "openapi" : "synthetic_path", path: apiPath });
    }
  }
  return output;
}

function baseTokens(resource: string, prefix: string): readonly string[] {
  const drop = new Set(["cloud", "apps", "asserts", "k6", "machine", "learning", "monitoring", "oncall", "synthetic", "trust", "zero"]);
  return baseResourceTokens(resource, prefix).filter((token) => !drop.has(token));
}
function pathParts(value: string): readonly string[] { return value.replace(/^\/+|\/+$/gu, "").split("/").filter(Boolean); }
function parameter(value: string): boolean { return value.startsWith("{") && value.endsWith("}"); }
function operationWords(value: string): readonly string[] { return identifierWords(value); }
function listOperation(id: string): boolean { const words = operationWords(id); return ["list", "search"].includes(words[0] ?? "") || (words[0] === "get" && words[1] === "all"); }
function pathWords(value: string): readonly string[] { return pathParts(value).filter((part) => !parameter(part)).flatMap((part) => part.split(/[^A-Za-z0-9]+/u).filter(Boolean).map((word) => word.toLowerCase())); }

function wordMatches(word: string, token: string): boolean {
  const normalized = canonical(token); const aliases = new Set([normalized, `${normalized}s`]);
  if (normalized.endsWith("y")) aliases.add(`${normalized.slice(0, -1)}ies`);
  if (normalized.endsWith("s")) aliases.add(normalized.slice(0, -1));
  if (normalized === "application") aliases.add("app").add("apps");
  if (["app", "apps"].includes(normalized)) aliases.add("application").add("applications");
  if (["repo", "repos", "repository", "repositories"].includes(normalized)) ["repo", "repos", "repository", "repositories"].forEach((item) => aliases.add(item));
  if (["org", "orgs", "organization", "organizations"].includes(normalized)) ["org", "orgs", "organization", "organizations"].forEach((item) => aliases.add(item));
  return aliases.has(canonical(word));
}

function operationMentions(operation: JsonObject, token: string): boolean {
  const normalized = canonical(token); if (!normalized) return false;
  return canonical(String(operation.path)).includes(normalized) || canonical(String(operation.operation_id)).includes(normalized)
    || pathWords(String(operation.path)).some((word) => wordMatches(word, normalized))
    || operationWords(String(operation.operation_id)).some((word) => wordMatches(word, normalized));
}

function pathSequenceScore(resource: string, prefix: string, operation: JsonObject): number {
  const tokens = baseTokens(resource, prefix); const words = pathWords(String(operation.path)); if (tokens.length === 0 || tokens.length > words.length) return 0;
  let best = 0; for (let start = 0; start <= words.length - tokens.length; start += 1) {
    if (!tokens.every((token, offset) => wordMatches(words[start + offset] ?? "", token))) continue;
    const terminal = start + tokens.length === words.length; if (tokens.length === 1 && !terminal) continue; best = Math.max(best, terminal ? 60 : 40);
  } return best;
}

function terminalScore(resource: string, prefix: string, operation: JsonObject): number {
  const tokens = baseTokens(resource, prefix); const parts = pathParts(String(operation.path)).filter((part) => !parameter(part));
  return tokens.length > 0 && parts.length > 0 && canonical(parts.at(-1) as string).includes(canonical(tokens.at(-1) as string)) ? 35 : 0;
}

function prefixScore(resource: string, prefix: string, operation: JsonObject): number {
  if (!prefix) return 0; const tokens = baseTokens(resource, prefix); const parts = pathParts(String(operation.path)).filter((part) => !parameter(part));
  return tokens.length > 0 && parts.length >= 2 && wordMatches(parts[0] as string, prefix) && wordMatches(parts[1] as string, tokens[0] as string) ? 30 : 0;
}

function scopeHints(schema: JsonObject): Readonly<Record<string, string>> {
  const attrs = object(object(schema.block).attributes); const output: Record<string, string> = {};
  const groups: Readonly<Record<string, readonly string[]>> = { account: ["account_id", "account_identifier", "account_tag"], user: ["user_id"], zone: ["zone_id", "zone_identifier", "zone_tag"] };
  for (const [scope, names] of Object.entries(groups)) { const present = names.filter((name) => isObject(attrs[name])).map((name) => object(attrs[name])); if (present.length > 0) output[scope] = present.some((item) => item.required) ? "required" : "optional"; }
  return output;
}

function operationScopes(operation: JsonObject): ReadonlySet<string> {
  const output = new Set<string>(); for (const part of pathParts(String(operation.path))) {
    const cleaned = part.replace(/^\{|\}$/gu, "").toLowerCase();
    if (["account_id", "account_identifier", "account_tag", "accounts"].includes(cleaned)) output.add("account");
    else if (["zone_id", "zone_identifier", "zone_tag", "zones"].includes(cleaned)) output.add("zone");
    else if (["user_id", "user"].includes(cleaned)) output.add("user");
  } return output;
}

function scopeScore(operation: JsonObject, scopes: Readonly<Record<string, string>>): number {
  const names = Object.keys(scopes); if (names.length === 0) return 0; const actual = operationScopes(operation); const required = names.filter((name) => scopes[name] === "required");
  if (required.length > 0) { if (required.some((name) => actual.has(name))) return 80; if (actual.size > 0) return -80; }
  if (names.length === 1) { if (actual.has(names[0] as string)) return 40; if (actual.size > 0) return -40; }
  return actual.size > 0 && !names.some((name) => actual.has(name)) ? -40 : 0;
}

function pathKind(operation: JsonObject): "detail" | "list" { const parts = pathParts(String(operation.path)); return parts.length > 0 && parameter(parts.at(-1) as string) ? "detail" : "list"; }
function actionShaped(value: string): boolean { const actions = new Set(["batch", "bulk", "export", "import", "preview", "review", "scan", "search", "trigger", "usage"]); return pathParts(value).some((part) => actions.has(part)); }

function chainTokens(call: JsonObject): readonly string[] {
  const drop = new Set(["api", "client", "cloudflare", "path", "paths", "zerotrust"]); const chain = Array.isArray(call.chain) ? call.chain.map(String) : [];
  const tokens = chain.flatMap(identifierWords).filter((token) => !drop.has(canonical(token))); return tokens.length > 0 ? tokens : chain;
}
function methodTokens(call: JsonObject): readonly string[] {
  const drop = new Set(["by", "fetch", "get", "list", "read", "search", "with"]); const words = identifierWords(String(call.method)); const extra: string[] = [];
  words.slice(0, -1).forEach((word, index) => { if (word === "ip" && ["address", "addresses"].includes(words[index + 1] ?? "")) extra.push("ips"); });
  return [...words, ...extra].filter((token) => !drop.has(token) && canonical(token).length >= 3);
}

function sdkCallScore(resource: string, prefix: string, operation: JsonObject, call: JsonObject, schema: JsonObject): number | undefined {
  if (operation.method !== "GET") return undefined; let score = 0; const chains = chainTokens(call); let chainHits = 0;
  for (const token of chains) if (operationMentions(operation, token)) { chainHits += 1; score += 30; }
  if (chains.length > 0 && chainHits < Math.min(2, chains.length)) return undefined;
  const methods = methodTokens(call); let methodHits = 0; for (const token of methods) if (operationMentions(operation, token)) { methodHits += 1; score += 22; }
  if (chains.length > 0 && chainHits === 0) return undefined;
  const tokens = baseTokens(resource, prefix); const resourceHits = tokens.filter((token) => operationMentions(operation, token)).length;
  const sequence = pathSequenceScore(resource, prefix, operation); const terminal = terminalScore(resource, prefix, operation); const exact = (operation.aliases as readonly string[]).includes(canonical(String(call.method)));
  if (chains.length === 0) { if (!exact && methodHits === 0 || tokens.length > 0 && resourceHits === 0 && sequence === 0 && terminal === 0) return undefined; if (exact) score += 110; else { score += 35 + methodHits * 18; score -= (methods.length - methodHits) * 20; } }
  else if (exact) score += 80;
  score -= (chains.length - chainHits) * 35; score += resourceHits * 8 + sequence + terminal + prefixScore(resource, prefix, operation) + scopeScore(operation, scopeHints(schema));
  const kind = pathKind(operation); const words = operationWords(String(operation.operation_id));
  if (call.source_role === "read") { score += kind === "detail" ? 30 : 5; if (words.some((word) => ["detail", "details", "get"].includes(word))) score += 10; if (listOperation(String(operation.operation_id))) score -= 20; if (actionShaped(String(operation.path))) score -= 25; }
  else if (call.source_role === "list") { score += kind === "list" ? 30 : -20; if (listOperation(String(operation.operation_id))) score += 15; if (actionShaped(String(operation.path))) score -= 20; }
  return score >= SDK_SCORE_FLOOR ? score : undefined;
}

function packageTokens(call: JsonObject): readonly string[] {
  const dropParts = new Set(["services", "zscaler", "v3"]); const drop = new Set(["by", "get", "id", "list", "or", "read", "search"]);
  const tokens = String(call.package_path ?? "").split("/").filter((part) => !dropParts.has(part)).flatMap(identifierWords);
  tokens.push(...identifierWords(String(call.package)), ...identifierWords(String(call.method))); return tokens.filter((token) => !drop.has(token) && canonical(token).length >= 3);
}

function packageCallScore(resource: string, prefix: string, operation: JsonObject, call: JsonObject, schema: JsonObject): number | undefined {
  if (operation.method !== "GET") return undefined; let score = 0; let hits = 0; for (const token of packageTokens(call)) if (operationMentions(operation, token)) { hits += 1; score += 18; } if (hits === 0) return undefined;
  score += baseTokens(resource, prefix).filter((token) => operationMentions(operation, token)).length * 10 + pathSequenceScore(resource, prefix, operation) + terminalScore(resource, prefix, operation) + prefixScore(resource, prefix, operation) + scopeScore(operation, scopeHints(schema));
  const lower = String(call.method).toLowerCase(); const hint = lower.includes("byid") || lower.includes("detail") ? "detail" : lower.startsWith("list") || lower.startsWith("search") || lower.includes("all") ? "list" : null; const kind = pathKind(operation);
  if (call.source_role === "read") { if (hint === "detail" && kind === "detail") score += 45; else if (hint === "list" && kind === "list") score += 30; else if (kind === "detail") score += 20; if (listOperation(String(operation.operation_id))) score -= 20; if (actionShaped(String(operation.path))) score -= 20; }
  else if (call.source_role === "list") { score += kind === "list" ? 35 : -25; if (listOperation(String(operation.operation_id))) score += 10; }
  return score >= PACKAGE_SCORE_FLOOR ? score : undefined;
}

function sequenceMatches(haystack: readonly string[], needle: readonly string[]): boolean { if (needle.length === 0 || needle.length > haystack.length) return false; for (let start = 0; start <= haystack.length - needle.length; start += 1) if (needle.every((token, offset) => wordMatches(haystack[start + offset] ?? "", token))) return true; return false; }
function rawCallScore(resource: string, prefix: string, operation: JsonObject, call: JsonObject, schema: JsonObject): number | undefined {
  if (operation.method !== call.method) return undefined; const callWords = pathWords(String(call.path)); const opWords = pathWords(String(operation.path)); if (callWords.length === 0 || opWords.length === 0 || !sequenceMatches(opWords, callWords) && !sequenceMatches(callWords, opWords)) return undefined;
  let score = 120 + callWords.length * 12 + (callWords.length === opWords.length ? 50 : 0) + pathSequenceScore(resource, prefix, operation) + terminalScore(resource, prefix, operation) + prefixScore(resource, prefix, operation) + scopeScore(operation, scopeHints(schema));
  if (pathKind(operation) === "detail") score += 20; if (actionShaped(String(operation.path))) score -= 10; return score >= RAW_SCORE_FLOOR ? score : undefined;
}

function candidateScore(resource: string, prefix: string, operation: JsonObject): number {
  let score = baseTokens(resource, prefix).filter((token) => canonical(String(operation.path)).includes(canonical(token))).length * 5;
  if (String(operation.path).includes("{")) score += 30; if (listOperation(String(operation.operation_id))) score -= 10; if (["get", "retrieve", "read", "routeget"].some((start) => String(operation.operation_id).toLowerCase().startsWith(start))) score += 10; if (String(operation.path).endsWith("/search") || String(operation.path).includes("/search/")) score -= 20; return score;
}
function listCandidateScore(resource: string, prefix: string, operation: JsonObject): number {
  let score = baseTokens(resource, prefix).filter((token) => canonical(String(operation.path)).includes(canonical(token))).length * 5; if (listOperation(String(operation.operation_id))) score += 20;
  const parts = pathParts(String(operation.path)); score += parts.length > 0 && parameter(parts.at(-1) as string) ? -20 : 15; if (["get", "retrieve", "read", "routeget"].some((start) => String(operation.operation_id).toLowerCase().startsWith(start))) score += 5; return score;
}

function hit(resource: string, prefix: string, operation: JsonObject, call: JsonObject | undefined, schema: JsonObject, kind: "package" | "raw" | "sdk" | "symbol", bonus?: number): JsonObject | undefined {
  let score: number | undefined = bonus; if (score === undefined && call) score = kind === "sdk" ? sdkCallScore(resource, prefix, operation, call, schema) : kind === "package" ? packageCallScore(resource, prefix, operation, call, schema) : rawCallScore(resource, prefix, operation, call, schema); if (score === undefined) return undefined;
  const output: JsonObject = { ...operation, list_score: listCandidateScore(resource, prefix, operation) + score, matched_aliases: call ? [kind === "raw" ? call.path : call.client_symbol] : [], path_kind: pathKind(operation), read_score: candidateScore(resource, prefix, operation) + score };
  if (call) { output.client_symbol = call.client_symbol; output.source_role = call.source_role; if (kind !== "raw") output.sdk_method = call.method; if (kind === "package") { output.sdk_package = call.package; output.sdk_package_path = call.package_path; } if (kind === "raw") output.raw_rest_path = call.path; }
  return output;
}

function sortHits(hits: JsonObject[], key = "read_score"): void { hits.sort((a, b) => Number(b[key]) - Number(a[key]) || comparePythonStrings(String(a.path), String(b.path)) || comparePythonStrings(String(a.operation_id), String(b.operation_id))); }
function dedupeHits(hits: readonly JsonObject[]): JsonObject[] {
  const groups = new Map<string, JsonObject>(); for (const item of hits) { const key = JSON.stringify([item.method, item.path, item.operation_id, item.path_kind, item.source_role ?? null]); const existing = groups.get(key); if (!existing) { groups.set(key, { ...item }); continue; }
    existing.read_score = Math.max(Number(existing.read_score), Number(item.read_score)); existing.list_score = Math.max(Number(existing.list_score), Number(item.list_score)); existing.matched_aliases = sortedStrings(new Set([...(existing.matched_aliases as string[] ?? []), ...(item.matched_aliases as string[] ?? [])])); const symbols = new Set([...(existing.alternate_client_symbols as string[] ?? []), existing.client_symbol, item.client_symbol].filter((value): value is string => typeof value === "string")); if (symbols.size > 0) existing.alternate_client_symbols = sortedStrings(symbols);
  } return [...groups.values()];
}

function selectHit(hits: readonly JsonObject[], role: "list" | "read"): readonly [JsonObject | undefined, readonly JsonObject[]] {
  const scoreKey = role === "list" ? "list_score" : "read_score"; const candidates = hits.filter((item) => item.source_role == null || item.source_role === role).filter((item) => role !== "list" || item.path_kind === "list"); sortHits(candidates, scoreKey); if (candidates.length === 0) return [undefined, []]; const best = candidates[0] as JsonObject;
  if (role === "read" && best.path_kind !== "detail") { const detail = candidates.slice(1).filter((item) => item.path_kind === "detail" && Number(best[scoreKey]) - Number(item[scoreKey]) <= READ_DETAIL_AMBIGUITY_DELTA); if (detail.length > 0) return [undefined, [best, ...detail.slice(0, 4)]]; }
  const ambiguous = candidates.slice(1).filter((item) => item.path_kind === best.path_kind && Number(best[scoreKey]) - Number(item[scoreKey]) <= (best.client_symbol && item.client_symbol ? SOURCE_CALL_AMBIGUITY_DELTA : AMBIGUITY_DELTA));
  return ambiguous.length > 0 ? [undefined, [best, ...ambiguous.slice(0, 4)]] : [best, []];
}

function relationship(resource: string, prefix: string): boolean { const tokens = baseTokens(resource, prefix); const joined = tokens.join("_"); return tokens.length >= 2 && (["secret_repositories", "variable_repositories", "role_team", "role_user", "team_assignment", "user_assignment", "repository_topics", "repository_collaborator", "sync_group_mapping"].some((phrase) => joined.includes(phrase)) || tokens.some((token) => RELATIONSHIP_TOKENS.has(token))); }
function relationshipHit(hits: readonly JsonObject[], resource: string, prefix: string): readonly [JsonObject | undefined, readonly JsonObject[]] {
  if (!relationship(resource, prefix)) return [undefined, []]; const tokens = baseTokens(resource, prefix).filter((token) => RELATIONSHIP_TOKENS.has(token)); let candidates = hits.filter((item) => item.source_role === "list"); const withTokens = candidates.filter((item) => tokens.some((token) => operationMentions(item, token))); if (withTokens.length > 0) candidates = withTokens; sortHits(candidates); if (candidates.length === 0) return [undefined, []]; const best = candidates[0] as JsonObject; const ambiguous = candidates.slice(1).filter((item) => Number(best.read_score) - Number(item.read_score) <= AMBIGUITY_DELTA); return ambiguous.length > 0 ? [undefined, [best, ...ambiguous.slice(0, 4)]] : [best, []];
}

function operationEntry(item: JsonObject, evidenceKind: string, files: readonly string[]): JsonObject {
  const openapi: JsonObject = { kind: "openapi_operation", method: item.method, operation_id: item.operation_id, path: item.path }; if (item.operation_id_source !== "openapi") openapi.operation_id_source = item.operation_id_source;
  const provider: JsonObject = { client_symbol: item.client_symbol ?? item.operation_id, kind: "provider_call", matched_aliases: item.matched_aliases ?? [], source_files: files };
  for (const key of ["sdk_method", "sdk_package", "sdk_package_path", "raw_rest_path", "source_role", "alternate_client_symbols"]) if (item[key]) provider[key] = item[key];
  const hops: JsonObject[] = [provider]; if (item.sdk_path_template) hops.push({ kind: "sdk_path", method: item.sdk_path_method ?? item.method, path_template: item.sdk_path_template, sdk_file: item.sdk_path_file ?? null }); hops.push(openapi);
  const output: JsonObject = { confidence: "high", evidence_kind: evidenceKind, hops, method: item.method, operation_id: item.operation_id, path: item.path, path_kind: item.path_kind }; if (item.operation_id_source !== "openapi") output.operation_id_source = item.operation_id_source; return output;
}
function candidateEntry(item: JsonObject): JsonObject { const output: JsonObject = { list_score: item.list_score, method: item.method, operation_id: item.operation_id, path: item.path, path_kind: item.path_kind, read_score: item.read_score }; if (item.operation_id_source !== "openapi") output.operation_id_source = item.operation_id_source; for (const key of ["client_symbol", "source_role", "alternate_client_symbols"]) if (item[key]) output[key] = item[key]; return output; }
function candidateOperation(item: JsonObject, kind: string, files: readonly string[]): JsonObject { return { ...operationEntry(item, kind, files), confidence: "low", list_score: item.list_score, read_score: item.read_score }; }

export async function deriveSourceOperationRegistry(options: {
  readonly schemaData: JsonObject; readonly openApi: JsonObject; readonly sourceRoot: string;
  readonly providerSource?: string; readonly resourcePrefix?: string; readonly resources?: readonly string[];
  readonly sourceFacts?: JsonObject; readonly sdkRoot?: string;
}): Promise<JsonObject> {
  if (options.sourceFacts !== undefined) {
    const required = ["files", "functions", "resource_registrations", "resource_references", "identifier_references", "read_callbacks", "selector_calls", "package_calls", "raw_rest_calls"];
    const malformed = required.filter((key) => !Array.isArray(options.sourceFacts?.[key]));
    if (malformed.length > 0) throw new Error(`malformed source facts: expected arrays for ${malformed.join(", ")}`);
  }
  const prefix = options.resourcePrefix ?? ""; const provider = providerFromSchema(options.schemaData, options.providerSource); let schemas = object(provider.resource_schemas);
  if (options.resources && options.resources.length > 0) { const wanted = sortedStrings(new Set(options.resources)); const missing = wanted.filter((resource) => !Object.hasOwn(schemas, resource)); if (missing.length > 0) throw new Error(`resources not found in provider schema: ${missing.join(", ")}`); schemas = Object.fromEntries(wanted.map((resource) => [resource, schemas[resource]])); }
  const resources = sortedStrings(Object.keys(schemas)); const filesByResource = options.sourceFacts ? await resourceFilesFromFacts(options.sourceRoot, resources, prefix, options.sourceFacts) : await resourceFilesFromText(options.sourceRoot, resources, prefix);
  const operations = openApiOperationInventory(options.openApi); const sdkExtracted = await extractSdkPaths(options.sdkRoot); const registry: Record<string, JsonObject> = {}; const diagnostics: JsonObject[] = []; let withFiles = 0;
  for (const resource of resources) {
    const absoluteFiles = filesByResource[resource] ?? []; const files = relativeSourceFiles(options.sourceRoot, absoluteFiles); if (absoluteFiles.length > 0) withFiles += 1;
    const evidence = options.sourceFacts ? await factsEvidence(options.sourceRoot, files, options.sourceFacts) : await textEvidence(options.sourceRoot, absoluteFiles); const identifiers = evidence.identifiers as ReadonlySet<string>; const sdkCalls = evidence.sdk_calls as readonly JsonObject[]; const packageCalls = evidence.package_calls as readonly JsonObject[]; const rawCalls = evidence.raw_rest_calls as readonly JsonObject[]; const hits: JsonObject[] = []; const unresolved: JsonObject[] = []; const actions: JsonObject[] = [];
    if (options.sdkRoot) {
      let texts = ""; if (!options.sourceFacts) texts = (await Promise.all(absoluteFiles.map((filename) => readFile(filename, "utf8")))).join("\n");
      const allCalls = options.sourceFacts ? sdkClientCallsFromFacts(files, options.sourceFacts, false) : sdkClientCalls(texts, false);
      for (const call of allCalls) { const symbol = String(call.client_symbol); const pathEvidence = sdkExtracted.evidence[symbol]; if (!pathEvidence) { const item = sdkExtracted.unresolved[symbol]; unresolved.push({ client_symbol: symbol, reason: item?.reason ?? "sdk_symbol_not_found", sdk_file: item?.sdk_file ?? null }); continue; }
        if (pathEvidence.method !== "GET") { actions.push({ client_symbol: symbol, method: pathEvidence.method, path_template: pathEvidence.path_template, sdk_file: pathEvidence.sdk_file }); continue; }
        const matched = matchOpenApiBySdkPath(operations, pathEvidence.path_template, "GET"); if (!matched.operation) { unresolved.push({ client_symbol: symbol, ...(matched.ambiguous.length > 0 ? { ambiguous_openapi_paths: matched.ambiguous.map((item) => item.path), reason: "openapi_path_ambiguous" } : { reason: "openapi_path_not_found" }), sdk_file: pathEvidence.sdk_file }); continue; }
        const operation = matched.operation; const relevant = pathSequenceScore(resource, prefix, operation) || terminalScore(resource, prefix, operation) || prefixScore(resource, prefix, operation) || baseTokens(resource, prefix).some((token) => operationMentions(operation, token)) || scopeScore(operation, scopeHints(object(schemas[resource]))) > 0; if (!relevant) continue;
        const item = hit(resource, prefix, operation, call, object(schemas[resource]), "sdk", SDK_PATH_SCORE); if (item) Object.assign(item, { sdk_path_file: pathEvidence.sdk_file, sdk_path_method: pathEvidence.method, sdk_path_template: pathEvidence.path_template, source_role: pathEvidence.source_role ?? call.source_role }); if (item) hits.push(item);
      }
    }
    for (const operation of operations) {
      if (operation.method !== "GET") continue; const aliases = sortedStrings((operation.aliases as string[]).filter((alias) => identifiers.has(alias))); if (aliases.length > 0) hits.push({ ...operation, list_score: listCandidateScore(resource, prefix, operation), matched_aliases: aliases, path_kind: pathKind(operation), read_score: candidateScore(resource, prefix, operation) });
      for (const call of sdkCalls) { const item = hit(resource, prefix, operation, call, object(schemas[resource]), "sdk"); if (item) hits.push(item); }
      for (const call of packageCalls) { const item = hit(resource, prefix, operation, call, object(schemas[resource]), "package"); if (item) hits.push(item); }
      for (const call of rawCalls) { const item = hit(resource, prefix, operation, call, object(schemas[resource]), "raw"); if (item) hits.push(item); }
    }
    sortHits(hits); const unique = dedupeHits(hits); sortHits(unique); const [read, readAmbiguous] = selectHit(unique, "read"); const [listing, listAmbiguous] = selectHit(unique, "list"); const [relation, relationAmbiguous] = relationshipHit(unique, resource, prefix);
    let status = "unmapped"; let reason: string | null = null; const source: JsonObject = { candidate_count: unique.length, files }; if (evidence.backend !== "text_scan") source.evidence_backend = evidence.backend;
    if (sdkCalls.length > 0) { source.client_call_count = sdkCalls.length; source.client_calls = sdkCalls.slice(0, 20).map((call) => call.client_symbol); }
    if (packageCalls.length > 0) { source.package_call_count = packageCalls.length; source.package_calls = packageCalls.slice(0, 20).map((call) => call.client_symbol); }
    if (rawCalls.length > 0) { source.raw_rest_call_count = rawCalls.length; source.raw_rest_calls = rawCalls.slice(0, 20).map((call) => call.client_symbol); }
    if (evidence.graphql_source) source.graphql = true; if (unresolved.length > 0) source.sdk_path_unresolved = unresolved; if (actions.length > 0) source.sdk_action_paths = actions;
    const entry: JsonObject = { product: prefix, reason: null, source, status, surface: prefix };
    if (readAmbiguous.length > 0) { status = "ambiguous_source_operation"; reason = status; entry.candidates = readAmbiguous.map((item) => candidateOperation(item, "read", files)); }
    else if (read) { status = "mapped"; entry.read = operationEntry(read, "read", files); if (listing && listing.path !== read.path) entry.list = operationEntry(listing, "list", files); if (listAmbiguous.length > 0) source.list_ambiguous = listAmbiguous.map(candidateEntry); }
    else if (relationAmbiguous.length > 0) { status = "ambiguous_source_operation"; reason = status; entry.candidates = relationAmbiguous.map((item) => candidateOperation(item, "relationship_list_read", files)); }
    else if (relation) { status = "mapped"; entry.read = operationEntry(relation, "relationship_list_read", files); source.relationship_list_read = true; }
    else if (evidence.graphql_source) { status = "graphql_source"; reason = status; }
    else reason = absoluteFiles.length === 0 ? "resource_file_not_found" : "no_source_operation_match";
    entry.status = status; entry.reason = reason; registry[resource] = entry; diagnostics.push({ ambiguous: readAmbiguous.map(candidateEntry), files, hits: unique.slice(0, 10).map(candidateEntry), reason, resource, status });
  }
  const mapped = Object.values(registry).filter((entry) => entry.status === "mapped").length; const ambiguous = diagnostics.filter((entry) => entry.status === "ambiguous_source_operation").length; const graphql = diagnostics.filter((entry) => entry.status === "graphql_source").length;
  return { diagnostics, registry, summary: { ambiguous, graphql_source: graphql, mapped, resources: resources.length, resources_with_source_files: withFiles, resources_without_source_files: resources.length - withFiles, unmapped: resources.length - mapped - ambiguous - graphql } };
}

function registrySignature(entry: JsonObject): JsonObject { const read = object(entry.read); const listing = object(entry.list); const source = object(entry.source); return { candidate_count: source.candidate_count ?? 0, client_call_count: source.client_call_count ?? 0, files: source.files ?? [], graphql: Boolean(source.graphql), list_operation_id: listing.operation_id ?? null, list_path: listing.path ?? null, package_call_count: source.package_call_count ?? 0, raw_rest_call_count: source.raw_rest_call_count ?? 0, read_evidence_kind: read.evidence_kind ?? null, read_operation_id: read.operation_id ?? null, read_path: read.path ?? null, reason: entry.reason ?? null, status: entry.status ?? null }; }

export function compareSourceOperationReports(control: JsonObject, candidate: JsonObject): JsonObject {
  const beforeRegistry = object(control.registry); const afterRegistry = object(candidate.registry); const resources = sortedStrings(new Set([...Object.keys(beforeRegistry), ...Object.keys(afterRegistry)])); const changes: JsonObject[] = []; let unchanged = 0; let statuses = 0; let reads = 0; let files = 0;
  for (const resource of resources) { const before = registrySignature(object(beforeRegistry[resource])); const after = registrySignature(object(afterRegistry[resource])); if (JSON.stringify(before) === JSON.stringify(after)) { unchanged += 1; continue; } if (before.status !== after.status) statuses += 1; if (before.read_path !== after.read_path) reads += 1; if (JSON.stringify(before.files) !== JSON.stringify(after.files)) files += 1; changes.push({ after, before, resource }); }
  return { changes, summary: { candidate: object(candidate.summary), changed: changes.length, control: object(control.summary), file_changes: files, read_path_changes: reads, resources: resources.length, status_changes: statuses, unchanged } };
}
