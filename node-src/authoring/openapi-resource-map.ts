import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import {
  terraformBlockForSchema,
  terraformInputBlockTypes,
  terraformResourceInputAttributes,
} from "../metadata/terraform-schema.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import { apiMetadataFromOpenApi } from "./openapi.js";
import { reconciliationFieldAlias } from "./reconcile-schema-api.js";

const HTTP_METHODS = new Set(["get", "post", "put", "patch", "delete"]);
const IRREGULAR_PLURALS: Readonly<Record<string, string>> = {
  address: "addresses", chassis: "chassis",
};
const RESOURCE_SEGMENT_ALIASES: Readonly<Record<string, Readonly<Record<string, readonly string[]>>>> = {
  ztc: {
    "dns-forwarding-gateway": ["dns-gateways"],
    "forwarding-gateway": ["gateways"],
    "ip-pool-groups": ["ip-groups"],
    "provisioning-url": ["prov-url"],
    "traffic-forwarding-dns-rule": ["ec-dns"],
    "traffic-forwarding-log-rule": ["self"],
    "traffic-forwarding-rule": ["ec-rdr"],
  },
};
const ACTION_RESOURCE_ALIASES: Readonly<Record<string, JsonObject>> = {
  ztc_activation_status: {
    read_operations: ["GET:/ecAdminActivateStatus"],
    surface: "ecAdminActivateStatus",
    write_operations: ["PUT:/ecAdminActivateStatus/activate"],
  },
};
const PRODUCT_MARKERS: Readonly<Record<string, readonly string[]>> = {
  zia: ["zia", "internet access"],
  zpa: ["zpa", "private access"],
  zcc: ["zcc", "client connector"],
  ztc: ["ztc", "ztw", "zcloudconnector", "cloud & branch connector", "cloud and branch connector"],
};
const SURFACE_HINT = /(?:^|_)(?:url|uri|host|endpoint|token|auth|cloud|region|realm)(?:$|_)/u;

function object(value: unknown): JsonObject {
  return isObject(value) ? value : {};
}

function objects(value: unknown): readonly JsonObject[] {
  return Array.isArray(value) ? value.filter(isObject) : [];
}

export function providerFromSchema(data: JsonObject, providerSource?: string): JsonObject {
  if (isObject(data.resource_schemas)) return data;
  const providers = object(data.provider_schemas);
  if (providerSource !== undefined) {
    let provider = providers[providerSource];
    if (!isObject(provider)) {
      const matches = Object.entries(providers)
        .filter(([source, value]) => source.endsWith(`/${providerSource}`) && isObject(value))
        .map(([, value]) => value as JsonObject);
      if (matches.length === 1) provider = matches[0];
    }
    if (!isObject(provider)) throw new Error(`provider source ${JSON.stringify(providerSource)} not found`);
    return provider;
  }
  const values = Object.values(providers).filter(isObject);
  if (values.length === 1) return values[0] as JsonObject;
  throw new TypeError("schema has multiple providers; pass providerSource");
}

export function openApiMethods(pathItem: unknown): readonly string[] {
  return sortedStrings(Object.keys(object(pathItem)).filter((key) => HTTP_METHODS.has(key.toLowerCase())));
}

function stripPrefix(value: string, prefix: string): string {
  return prefix !== "" && value.startsWith(prefix) ? value.slice(prefix.length) : value;
}

export function openApiPathParts(path: string, apiPrefix: string): readonly string[] {
  return stripPrefix(path, apiPrefix).replace(/^\/+|\/+$/gu, "").split("/").filter(Boolean);
}

function isParameter(part: string): boolean {
  return part.startsWith("{") && part.endsWith("}");
}

export function canonicalOpenApiPathParts(path: string): readonly string[] {
  return path.replace(/^\/+|\/+$/gu, "").split("/").filter(Boolean)
    .map((part) => isParameter(part) ? "{}" : part);
}

export function collectionPaths(spec: JsonObject, apiPrefix: string): readonly string[] {
  const paths = object(spec.paths);
  return sortedStrings(Object.keys(paths)).filter((path) => {
    if (!path.startsWith(apiPrefix)) return false;
    const parts = openApiPathParts(path, apiPrefix);
    if (parts.length === 0 || isParameter(parts.at(-1) ?? "")) return false;
    const methods = openApiMethods(paths[path]);
    return methods.includes("get") || methods.includes("post");
  });
}

function readPaths(spec: JsonObject, apiPrefix: string): readonly JsonObject[] {
  const paths = object(spec.paths);
  return sortedStrings(Object.keys(paths)).filter((path) => path.startsWith(apiPrefix)
    && openApiMethods(paths[path]).includes("get"))
    .map((path) => ({ openapi_path: path, parts: canonicalOpenApiPathParts(stripPrefix(path, apiPrefix)) }));
}

export function fetchPathVariants(
  fetchPath: string,
  product: string,
  apiPrefix = "/",
): readonly JsonObject[] {
  const parts = canonicalOpenApiPathParts(fetchPath);
  const apiParts = canonicalOpenApiPathParts(apiPrefix);
  const variants: JsonObject[] = parts.length > 0 ? [{ parts, variant: "exact" }] : [];
  if (apiParts.length > 0 && apiParts.every((part, index) => parts[index] === part)) {
    variants.push({ parts: parts.slice(apiParts.length), variant: "api_prefix_stripped" });
  }
  for (const item of [...variants]) {
    const candidate = item.parts as readonly string[];
    if (product !== "" && candidate[0]?.toLowerCase() === product.toLowerCase()) {
      variants.push({
        parts: candidate.slice(1),
        variant: item.variant === "exact" ? "product_prefix_stripped" : `${String(item.variant)}_product_prefix_stripped`,
      });
    }
  }
  const seen = new Set<string>();
  return variants.filter((item) => {
    const candidate = item.parts as readonly string[];
    const key = JSON.stringify([candidate, item.variant]);
    if (candidate.length === 0 || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function pathMatch(left: readonly string[], right: readonly string[]): "exact" | "suffix" | undefined {
  const equal = (a: readonly string[], b: readonly string[]): boolean => a.length === b.length
    && a.every((part, index) => part === b[index] || part === "{}" || b[index] === "{}");
  if (equal(left, right)) return "exact";
  if (left.length > 0 && right.length >= left.length && equal(right.slice(-left.length), left)) return "suffix";
  return undefined;
}

export function matchRegistryPath(
  spec: JsonObject,
  apiPrefix: string,
  fetchPath: string,
  product: string,
): JsonObject | undefined {
  const matches: JsonObject[] = [];
  for (const variant of fetchPathVariants(fetchPath, product, apiPrefix)) {
    for (const read of readPaths(spec, apiPrefix)) {
      const match = pathMatch(variant.parts as readonly string[], read.parts as readonly string[]);
      if (match !== undefined) matches.push({
        match, openapi_path: read.openapi_path, variant: variant.variant,
      });
    }
  }
  const matchRank: Readonly<Record<string, number>> = { exact: 0, suffix: 1 };
  const variantRank: Readonly<Record<string, number>> = {
    exact: 0, api_prefix_stripped: 1, product_prefix_stripped: 2,
    api_prefix_stripped_product_prefix_stripped: 3,
  };
  matches.sort((left, right) => (matchRank[String(left.match)] ?? 2) - (matchRank[String(right.match)] ?? 2)
    || (variantRank[String(left.variant)] ?? 4) - (variantRank[String(right.variant)] ?? 4)
    || comparePythonStrings(String(left.openapi_path), String(right.openapi_path)));
  return matches[0];
}

function productText(spec: JsonObject): string {
  const parts = [String(object(spec.info).title ?? "")];
  parts.push(...objects(spec.servers).map((server) => String(server.url ?? "")));
  return parts.join(" ").toLowerCase();
}

function detectedProducts(spec: JsonObject): ReadonlySet<string> {
  const text = productText(spec);
  return new Set(Object.entries(PRODUCT_MARKERS)
    .filter(([, markers]) => markers.some((marker) => text.includes(marker)))
    .map(([product]) => product));
}

function productMatches(spec: JsonObject, prefix: string): boolean {
  if (PRODUCT_MARKERS[prefix] === undefined) return true;
  const detected = detectedProducts(spec);
  return detected.size === 0 || detected.has(prefix);
}

function detailPaths(spec: JsonObject, collectionPath: string): readonly string[] {
  const separator = collectionPath.endsWith("/") ? "" : "/";
  const escaped = collectionPath.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
  const pattern = new RegExp(`^${escaped}${separator}\\{[^/]+\\}/?$`, "u");
  return sortedStrings(Object.keys(object(spec.paths)).filter((path) => pattern.test(path)));
}

export function pluralizeToken(token: string): string {
  if (IRREGULAR_PLURALS[token] !== undefined) return IRREGULAR_PLURALS[token];
  if (token.endsWith("y") && token.length > 1 && !"aeiou".includes(token.at(-2) ?? "")) return `${token.slice(0, -1)}ies`;
  if (["s", "x", "ch", "sh"].some((suffix) => token.endsWith(suffix))) return `${token}es`;
  return `${token}s`;
}

function pluralizeSlug(slug: string): string {
  const parts = slug.split("-");
  if (parts.length > 0) parts[parts.length - 1] = pluralizeToken(parts.at(-1) ?? "");
  return parts.join("-");
}

function singularizeToken(token: string): string {
  if (token === "addresses") return "address";
  if (token.endsWith("ies") && token.length > 3) return `${token.slice(0, -3)}y`;
  if (token.endsWith("ches") || token.endsWith("shes") || token.endsWith("xes") || token.endsWith("ses")) return token.slice(0, -2);
  if (token.endsWith("s") && !token.endsWith("ss")) return token.slice(0, -1);
  return token;
}

function singularizeSlug(slug: string): string {
  const parts = slug.split("-");
  if (parts.length > 0) parts[parts.length - 1] = singularizeToken(parts.at(-1) ?? "");
  return parts.join("-");
}

export function baseResourceTokens(resourceType: string, resourcePrefix: string): readonly string[] {
  const prefix = `${resourcePrefix}_`;
  const base = resourcePrefix !== "" && resourceType.startsWith(prefix) ? resourceType.slice(prefix.length) : resourceType;
  return base.split("_").filter(Boolean);
}

export function canonicalSegmentSlug(value: string): string {
  return value.replace(/([a-z0-9])([A-Z])/gu, "$1-$2")
    .replace(/([A-Z]+)([A-Z][a-z])/gu, "$1-$2")
    .replace(/[^A-Za-z0-9]+/gu, "-").replace(/^-+|-+$/gu, "").toLowerCase();
}

function slugCandidates(resourceType: string, prefix: string): ReadonlyMap<string, number> {
  const tokens = baseResourceTokens(resourceType, prefix);
  const candidates = new Map<string, number>();
  for (let start = 0; start < tokens.length; start += 1) {
    const slug = tokens.slice(start).join("-");
    const baseScore = start === 0 ? 120 : 80 - start;
    candidates.set(slug, candidates.get(slug) ?? baseScore - 8);
    candidates.set(pluralizeSlug(slug), candidates.get(pluralizeSlug(slug)) ?? baseScore);
  }
  const aliases = RESOURCE_SEGMENT_ALIASES[prefix]?.[tokens.join("-")] ?? [];
  for (const alias of aliases) candidates.set(alias, Math.max(candidates.get(alias) ?? 0, 150));
  return candidates;
}

function schemaInputs(schema: JsonObject): readonly [ReadonlySet<string>, ReadonlySet<string>] {
  const block = terraformBlockForSchema(schema, "resource");
  const classified = terraformResourceInputAttributes(block, "resource.block");
  return [new Set([...classified.required, ...classified.optional, ...terraformInputBlockTypes(block, "resource.block").keys()]), new Set(classified.computedOnly)];
}

function hasMethod(spec: JsonObject, path: unknown, method: string): boolean {
  return typeof path === "string" && openApiMethods(object(spec.paths)[path]).includes(method.toLowerCase());
}

function matchResource(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  prefix: string,
  apiPrefix: string,
): JsonObject {
  const tokens = baseResourceTokens(resourceType, prefix);
  const candidates: JsonObject[] = [];
  const slugs = slugCandidates(resourceType, prefix);
  for (const collection of collectionPaths(spec, apiPrefix)) {
    const parts = openApiPathParts(collection, apiPrefix);
    const segment = canonicalSegmentSlug(parts.at(-1) ?? "");
    const baseScore = slugs.get(segment);
    if (baseScore === undefined) continue;
    const details = detailPaths(spec, collection);
    const detail = details[0];
    const appHint = parts[0] === tokens[0] || singularizeSlug(parts[0] ?? "") === tokens[0] ? 12 : 0;
    const [inputs] = schemaInputs(schema);
    const surfaceHint = parts[0] === "dcim" && inputs.has("device_id") ? 25
      : parts[0] === "virtualization" && inputs.has("virtual_machine_id") ? 25 : 0;
    let methodScore = hasMethod(spec, detail, "get") ? 10 : 0;
    if (hasMethod(spec, collection, "post")) methodScore += 6;
    if (hasMethod(spec, detail, "put") || hasMethod(spec, detail, "patch")) methodScore += 6;
    const confidence = segment === pluralizeSlug(tokens.join("-")) ? "exact_plural" : "suffix_plural";
    let score = baseScore + appHint + surfaceHint + methodScore;
    if (confidence === "suffix_plural" && appHint === 0) score -= 60;
    candidates.push({
      collection_path: collection, confidence, detail_path: detail ?? null,
      matched_segment: segment, score, surface: parts[0] ?? null,
    });
  }
  candidates.sort((left, right) => Number(right.score) - Number(left.score)
    || comparePythonStrings(String(left.collection_path), String(right.collection_path)));
  if (candidates.length === 0) return { candidates: [], reason: "no_openapi_collection_path_match", status: "unmatched" };
  const top = candidates[0] as JsonObject;
  const tied = candidates.filter((candidate) => candidate.score === top.score);
  if (tied.length > 1) return { candidates: tied.slice(0, 5), reason: "multiple_equal_score_matches", status: "ambiguous" };
  let status = "matched";
  let reason: string | null = null;
  if (Number(top.score) < 60) { status = "unmatched"; reason = "low_confidence_suffix_match"; }
  else if (top.detail_path === null) { status = "unmatched"; reason = "matched_collection_has_no_standard_detail_path"; }
  return {
    candidates: candidates.slice(0, 5), collection_path: status === "matched" ? top.collection_path : null,
    confidence: top.confidence, detail_path: status === "matched" ? top.detail_path : null,
    reason, score: top.score,
    status, surface: status === "matched" ? top.surface : null,
  };
}

function topLevelMetadata(
  spec: JsonObject,
  schema: JsonObject,
  readOperations: readonly string[],
  writeOperations: readonly string[],
): JsonObject {
  const metadata = apiMetadataFromOpenApi(spec, { readOperations, writeOperations });
  const [inputs, computed] = schemaInputs(schema);
  const top = (field: string): boolean => !field.replaceAll("[]", ".").includes(".");
  const read = sortedStrings(Object.entries(metadata).filter(([path, meta]) => top(path) && meta.readable).map(([path]) => path));
  const write = sortedStrings(Object.entries(metadata).filter(([path, meta]) => top(path) && meta.writable).map(([path]) => path));
  const responseOnly = sortedStrings(Object.entries(metadata).filter(([path, meta]) => top(path) && (meta.response_only || meta.read_only)).map(([path]) => path));
  const aliases: JsonObject[] = [];
  const gaps: string[] = [];
  for (const path of write) {
    const field = path.replaceAll("[]", "").split(".")[0] ?? path;
    if (inputs.has(field)) continue;
    const [alias, kind, reason] = reconciliationFieldAlias(field, inputs, computed);
    if (alias !== undefined && kind === "input") aliases.push({ api_path: path, reason, terraform_path: alias });
    else if (!computed.has(field)) gaps.push(path);
  }
  return {
    aliased_top_level_paths: aliases,
    provider_gap_top_level_paths: gaps,
    read_operations: readOperations,
    read_top_level_paths: read,
    response_only_top_level_paths: responseOnly,
    summary: {
      aliased_top_level: aliases.length, provider_gap_top_level: gaps.length,
      read_top_level: read.length, response_only_top_level: responseOnly.length,
      write_top_level: write.length,
    },
    write_operations: writeOperations,
    write_top_level_paths: write,
  };
}

function staticContract(spec: JsonObject, schema: JsonObject, collection: string, detail: string): JsonObject {
  const read = hasMethod(spec, detail, "get") ? [`GET:${detail}`] : [];
  const write: string[] = [];
  if (hasMethod(spec, collection, "post")) write.push(`POST:${collection}`);
  if (hasMethod(spec, detail, "put")) write.push(`PUT:${detail}`);
  if (hasMethod(spec, detail, "patch")) write.push(`PATCH:${detail}`);
  return topLevelMetadata(spec, schema, read, write);
}

function staticActionContract(
  spec: JsonObject,
  schema: JsonObject,
  writeOperations: readonly string[],
): JsonObject {
  const contract = topLevelMetadata(spec, schema, [], writeOperations);
  return {
    aliased_top_level_paths: contract.aliased_top_level_paths,
    provider_gap_top_level_paths: contract.provider_gap_top_level_paths,
    summary: {
      aliased_top_level: object(contract.summary).aliased_top_level,
      provider_gap_top_level: object(contract.summary).provider_gap_top_level,
      write_top_level: object(contract.summary).write_top_level,
    },
    write_operations: writeOperations,
    write_top_level_paths: contract.write_top_level_paths,
  };
}

function parentSlugCandidates(field: string, objectTokens: readonly string[]): ReadonlySet<string> {
  const base = field.endsWith("_id") ? field.slice(0, -3) : field;
  const tokens = base.split("_").filter(Boolean);
  if (tokens.length === 0) return new Set();
  const slug = tokens.join("-");
  const output = new Set([slug, pluralizeSlug(slug)]);
  if (tokens[0] === "parent" && tokens.length > 1) {
    const parent = tokens.slice(1).join("-"); output.add(parent); output.add(pluralizeSlug(parent));
  }
  if (tokens.join("_") === "ip_range") output.add("ip-ranges");
  if (tokens.join("_") === "virtual_machine") output.add("virtual-machines");
  if (tokens[0] === "group" && objectTokens.length > 0) {
    output.add(`${objectTokens.join("-")}-groups`);
    output.add(`${pluralizeSlug(objectTokens.join("-"))}-groups`);
  }
  return output;
}

function allocationAction(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  prefix: string,
  apiPrefix: string,
): JsonObject | undefined {
  const tokens = baseResourceTokens(resourceType, prefix);
  if (tokens[0] !== "available" || tokens.length < 2) return undefined;
  const objectTokens = tokens.slice(1);
  const slug = objectTokens.join("-");
  const wanted = new Set([`available-${pluralizeSlug(slug)}`]);
  if (slug === "ip" || slug === "ip-address") wanted.add("available-ips");
  const [inputs, computed] = schemaInputs(schema);
  const parentFields = sortedStrings([...inputs].filter((field) => field.endsWith("_id") && !computed.has(field)));
  const parents = new Map<string, string[]>();
  for (const field of parentFields) for (const candidate of parentSlugCandidates(field, objectTokens)) {
    const fields = parents.get(candidate) ?? []; fields.push(field); parents.set(candidate, fields);
  }
  const actions: JsonObject[] = [];
  for (const path of sortedStrings(Object.keys(object(spec.paths)))) {
    if (!path.startsWith(apiPrefix) || !path.endsWith("/") || !hasMethod(spec, path, "post")) continue;
    const parts = openApiPathParts(path, apiPrefix);
    if (parts.length < 3 || !wanted.has(parts.at(-1) ?? "") || !isParameter(parts.at(-2) ?? "")) continue;
    const parent = parts.at(-3) ?? "";
    const fields = sortedStrings(parents.get(parent) ?? []);
    if (parents.size > 0 && fields.length === 0) continue;
    actions.push({
      action_segment: parts.at(-1), operation: `POST:${path}`,
      parent_collection_segment: parent, parent_id_fields: fields, path,
    });
  }
  if (actions.length === 0) return undefined;
  const writes = actions.map((action) => String(action.operation));
  return {
    actions, candidates: [], canonical_resource: resourceType.replace("_available_", "_"),
    collection_path: null, detail_path: null, reason: "parent_scoped_openapi_action",
    special_type: "allocation_action", static_contract: staticActionContract(spec, schema, writes),
    status: "special", surface: openApiPathParts(String(actions[0]?.path), apiPrefix)[0] ?? null,
  };
}

function parentCollections(spec: JsonObject, parentSlug: string, apiPrefix: string): readonly JsonObject[] {
  const wanted = pluralizeSlug(parentSlug);
  return collectionPaths(spec, apiPrefix).flatMap((collection) => {
    const parts = openApiPathParts(collection, apiPrefix);
    if (parts.at(-1) !== wanted) return [];
    const detail = detailPaths(spec, collection)[0];
    return detail !== undefined && (hasMethod(spec, detail, "patch") || hasMethod(spec, detail, "put"))
      ? [{ collection, detail, surface: parts[0] }] : [];
  });
}

function primaryIpAssignment(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  prefix: string,
  apiPrefix: string,
): JsonObject | undefined {
  const tokens = baseResourceTokens(resourceType, prefix);
  const [inputs] = schemaInputs(schema);
  if (tokens.slice(-2).join("_") !== "primary_ip" || !inputs.has("ip_address_id")) return undefined;
  const parents: Array<readonly [string, string]> = [];
  if (inputs.has("device_id")) parents.push(["device_id", "device"]);
  if (inputs.has("virtual_machine_id")) parents.push(["virtual_machine_id", "virtual-machine"]);
  const assignments: JsonObject[] = [];
  for (const [field, slug] of parents) for (const parent of parentCollections(spec, slug, apiPrefix)) {
    const detail = String(parent.detail);
    const writes = [hasMethod(spec, detail, "patch") ? `PATCH:${detail}` : null, hasMethod(spec, detail, "put") ? `PUT:${detail}` : null].filter((item): item is string => item !== null);
    const metadata = apiMetadataFromOpenApi(spec, { readOperations: [`GET:${detail}`], writeOperations: writes });
    const writable = sortedStrings(Object.entries(metadata).filter(([path, meta]) => ["primary_ip4", "primary_ip6"].includes(path) && meta.writable).map(([path]) => path));
    if (writable.length > 0) assignments.push({
      ip_address_id_field: "ip_address_id", parent_collection_path: parent.collection,
      parent_detail_path: detail, parent_id_field: field,
      surface: parent.surface, version_field: inputs.has("ip_address_version") ? "ip_address_version" : null,
      write_fields: writable, write_operations: writes,
    });
  }
  if (assignments.length === 0) return undefined;
  return {
    assignments, candidates: [], canonical_parent_resource: `${prefix}_${assignments[0]?.parent_id_field === "device_id" ? "device" : "virtual_machine"}`,
    collection_path: null, detail_path: null, reason: "parent_field_assignment",
    special_type: "derived_relationship", status: "special", surface: assignments[0]?.surface,
  };
}

function primaryMacAssignment(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  prefix: string,
  apiPrefix: string,
): JsonObject | undefined {
  const tokens = baseResourceTokens(resourceType, prefix);
  const [inputs] = schemaInputs(schema);
  if (tokens.slice(-3).join("_") !== "primary_mac_address" || !inputs.has("interface_id") || !inputs.has("mac_address_id")) return undefined;
  const device = tokens.slice(0, 2).join("_") === "device_interface";
  const virtual = tokens.slice(0, 3).join("_") === "virtual_machine_interface";
  if (!device && !virtual) return undefined;
  const expectedSurface = device ? "dcim" : "virtualization";
  const assignments: JsonObject[] = [];
  for (const parent of parentCollections(spec, "interface", apiPrefix)) {
    if (parent.surface !== expectedSurface) continue;
    const detail = String(parent.detail);
    const writes = [hasMethod(spec, detail, "patch") ? `PATCH:${detail}` : null, hasMethod(spec, detail, "put") ? `PUT:${detail}` : null].filter((item): item is string => item !== null);
    const metadata = apiMetadataFromOpenApi(spec, { readOperations: [`GET:${detail}`], writeOperations: writes });
    const writable = sortedStrings(Object.entries(metadata).filter(([path, meta]) => path === "primary_mac_address" && meta.writable).map(([path]) => path));
    if (writable.length > 0) assignments.push({
      mac_address_id_field: "mac_address_id", parent_collection_path: parent.collection,
      parent_detail_path: detail, parent_id_field: "interface_id", surface: parent.surface,
      write_fields: writable, write_operations: writes,
    });
  }
  if (assignments.length === 0) return undefined;
  return {
    assignments, candidates: [], canonical_child_resource: `${prefix}_mac_address`,
    canonical_parent_resource: device ? `${prefix}_device_interface` : `${prefix}_interface`,
    collection_path: null, detail_path: null, reason: "parent_field_assignment",
    special_type: "derived_relationship", status: "special", surface: assignments[0]?.surface,
  };
}

function aliasedAction(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  apiPrefix: string,
): JsonObject | undefined {
  const alias = ACTION_RESOURCE_ALIASES[resourceType];
  if (alias === undefined) return undefined;
  const read = (alias.read_operations as readonly string[]).filter((operation) => {
    const [method, path] = operation.split(":", 2) as [string, string];
    return path.startsWith(apiPrefix) && hasMethod(spec, path, method);
  });
  const write = (alias.write_operations as readonly string[]).filter((operation) => {
    const [method, path] = operation.split(":", 2) as [string, string];
    return path.startsWith(apiPrefix) && hasMethod(spec, path, method);
  });
  if (read.length === 0 && write.length === 0) return undefined;
  return {
    candidates: [], collection_path: null, detail_path: null,
    read_operations: read, reason: "provider_resource_maps_to_openapi_action",
    special_type: "aliased_action", static_contract: topLevelMetadata(spec, schema, read, write),
    status: "special", surface: alias.surface, write_operations: write,
  };
}

function specialResource(
  spec: JsonObject,
  resourceType: string,
  schema: JsonObject,
  prefix: string,
  apiPrefix: string,
): JsonObject | undefined {
  return allocationAction(spec, resourceType, schema, prefix, apiPrefix)
    ?? primaryIpAssignment(spec, resourceType, schema, prefix, apiPrefix)
    ?? primaryMacAssignment(spec, resourceType, schema, prefix, apiPrefix)
    ?? aliasedAction(spec, resourceType, schema, apiPrefix);
}

function profile(spec: JsonObject, apiPrefix: string): JsonObject {
  const paths = sortedStrings(Object.keys(object(spec.paths)).filter((path) => path.startsWith(apiPrefix)));
  const first: Record<string, number> = {};
  const collections: Record<string, number> = {};
  for (const path of paths) {
    const concrete = openApiPathParts(path, apiPrefix).filter((part) => !isParameter(part)).map(canonicalSegmentSlug);
    if (concrete.length === 0) continue;
    first[concrete[0] as string] = (first[concrete[0] as string] ?? 0) + 1;
    const last = concrete.at(-1) as string;
    collections[last] = (collections[last] ?? 0) + 1;
  }
  const top = (counts: Record<string, number>): readonly JsonObject[] => Object.entries(counts)
    .sort((left, right) => right[1] - left[1] || comparePythonStrings(left[0], right[0])).slice(0, 25)
    .map(([segment, count]) => ({ paths: count, segment }));
  return {
    path_count_for_api_prefix: paths.length,
    servers: objects(spec.servers).map((server) => server.url).filter(Boolean),
    title: object(spec.info).title ?? null,
    top_collection_segments: top(collections), top_first_segments: top(first),
  };
}

function providerHints(provider: JsonObject): readonly JsonObject[] {
  const attributes = object(object(object(provider.provider).block).attributes);
  return sortedStrings(Object.keys(attributes)).filter((name) => SURFACE_HINT.test(name)).map((name) => {
    const meta = object(attributes[name]);
    return { description: meta.description ?? null, name, sensitive: Boolean(meta.sensitive) };
  });
}

function family(resourceType: string, prefix: string): string {
  return baseResourceTokens(resourceType, prefix)[0] ?? "unknown";
}

function roundRatio4(numerator: number, denominator: number): number {
  if (denominator === 0) return 0;
  const scaled = BigInt(numerator) * 10_000n;
  const divisor = BigInt(denominator);
  let quotient = scaled / divisor;
  const twiceRemainder = (scaled % divisor) * 2n;
  if (twiceRemainder > divisor || (twiceRemainder === divisor && quotient % 2n !== 0n)) {
    quotient += 1n;
  }
  return Number(quotient) / 10_000;
}

function coverageDiagnostics(
  summary: JsonObject,
  families: Readonly<Record<string, Record<string, number>>>,
  openApiProfile: JsonObject,
  hints: readonly JsonObject[],
): JsonObject {
  const total = Number(summary.resources);
  const covered = Number(summary.matched) + Number(summary.special);
  const ratio = total === 0 ? 0 : covered / total;
  const warnings: JsonObject[] = [];
  if (openApiProfile.path_count_for_api_prefix === 0) warnings.push({ code: "api_prefix_matches_no_paths", message: "The selected API prefix matches zero OpenAPI paths. Check whether the spec stores the product base path in servers[] instead of paths[]." });
  if (total > 0 && ratio < 0.25) warnings.push({ code: "low_openapi_resource_coverage", coverage_ratio: roundRatio4(covered, total), message: "Fewer than 25% of Terraform resources mapped to this OpenAPI document. This often means the spec is the wrong product surface, only a partial surface, or the provider contains orchestration resources that do not map to CRUD collections." });
  if (total > 0 && hints.length > 0 && ratio < 0.75) warnings.push({ code: "provider_config_suggests_multiple_surfaces", hint_attributes: hints.map((hint) => hint.name), message: "Provider configuration exposes URL/token/cloud-style knobs while OpenAPI coverage is incomplete. Classify resources by surface before field-level reconciliation." });
  const uncovered = sortedStrings(Object.keys(families)).flatMap((name) => {
    const counts = families[name] as Record<string, number>;
    const count = Object.values(counts).reduce((sum, value) => sum + value, 0);
    return count > 0 && (counts.matched ?? 0) + (counts.special ?? 0) === 0
      ? [{ family: name, resources: count, statuses: Object.fromEntries(sortedStrings(Object.keys(counts)).map((key) => [key, counts[key]])) }]
      : [];
  });
  if (uncovered.length > 0) warnings.push({ code: "uncovered_resource_families", families: uncovered.slice(0, 50), message: "At least one Terraform resource family had no mapped OpenAPI CRUD endpoint." });
  return {
    coverage_ratio: roundRatio4(covered, total), covered_resources: covered,
    family_coverage: Object.fromEntries(sortedStrings(Object.keys(families)).map((name) => [name, Object.fromEntries(sortedStrings(Object.keys(families[name] ?? {})).map((key) => [key, families[name]?.[key]]))])),
    warnings,
  };
}

function registryCoverage(
  spec: JsonObject,
  apiPrefix: string,
  prefix: string,
  registry: JsonObject,
  key: "fetch" | "read",
): JsonObject {
  const resources: JsonObject[] = [];
  const matchesProduct = productMatches(spec, prefix);
  for (const resource of sortedStrings(Object.keys(registry))) {
    const entry = object(registry[resource]);
    if (entry.product !== prefix) continue;
    if (key === "read" && entry.status && entry.status !== "mapped") {
      resources.push({ reason: entry.reason ?? entry.status, resource, status: entry.status });
      continue;
    }
    const pathEntry = object(entry[key]);
    if (typeof pathEntry.path !== "string") continue;
    const match = matchesProduct ? matchRegistryPath(spec, apiPrefix, pathEntry.path, prefix) : undefined;
    const item: JsonObject = { [`${key}_path`]: pathEntry.path, resource };
    if (key === "fetch") item.pagination = pathEntry.pagination ?? prefix;
    if (pathEntry.operation_id) item.operation_id = pathEntry.operation_id;
    if (pathEntry.path_kind) item.path_kind = pathEntry.path_kind;
    Object.assign(item, match === undefined ? {
      reason: matchesProduct ? "fetch_path_not_found_in_openapi_get_paths" : "openapi_product_mismatch",
      status: "unmatched",
    } : { ...match, status: "matched" });
    resources.push(item);
  }
  const matched = resources.filter((item) => item.status === "matched").length;
  const ambiguous = resources.filter((item) => item.status === "ambiguous_source_operation").length;
  const total = resources.length;
  const warnings: JsonObject[] = [];
  const label = key === "fetch" ? "registry_fetch" : "registry_read";
  if (total > 0 && !matchesProduct) warnings.push({ code: key === "fetch" ? "registry_openapi_product_mismatch" : `${label}_openapi_product_mismatch`, detected_products: sortedStrings([...detectedProducts(spec)]), message: "The OpenAPI document advertises a different known product than the resource prefix; registry path suffix matches were suppressed." });
  const nonMapped = resources.filter((item) => !["matched", "unmatched"].includes(String(item.status)));
  if (key === "read" && nonMapped.length > 0) warnings.push({ code: "registry_read_entries_not_mapped", message: "At least one source evidence entry did not produce a selected read path; inspect the source diagnostics before OpenAPI path matching.", resources: nonMapped.slice(0, 50).map((item) => item.resource) });
  const missing = resources.filter((item) => item.status === "unmatched");
  if (missing.length > 0) warnings.push({ code: `${label}_paths_missing_from_openapi`, message: "At least one registry path was not present as an OpenAPI GET path.", resources: missing.slice(0, 50).map((item) => item.resource) });
  const summary: JsonObject = {
    [key === "fetch" ? "fetch_resources" : "read_resources"]: total,
    matched, unmatched: total - matched - ambiguous,
    coverage_ratio: total === 0 ? null : roundRatio4(matched, total),
  };
  if (key === "read") summary.ambiguous = ambiguous;
  return { resources, summary, warnings };
}

function operationPath(operation: unknown): string | null {
  if (typeof operation !== "string") return null;
  return operation.includes(":") ? operation.slice(operation.indexOf(":") + 1) : operation;
}

function surfaceRecord(resource: JsonObject, provider: string | undefined, prefix: string): JsonObject {
  const status = String(resource.status);
  const candidates = Array.isArray(resource.candidates) ? resource.candidates : [];
  const surface = (resource.surface ?? prefix) || null;
  if (status === "matched") {
    const reads = object(resource.static_contract).read_operations;
    const read = Array.isArray(reads) ? reads[0] : undefined;
    return { adapter_required: false, ambiguity_reason: null, api_surface: surface, confidence: resource.confidence,
      evidence: [{ collection_path: resource.collection_path, detail_path: resource.detail_path, kind: "generic_crud_candidate", matched_segment: resource.matched_segment ?? null, score: resource.score }],
      match_status: "matched", provider: provider ?? null, read_operation: read ?? null, read_path: operationPath(read), resource_type: resource.resource, source: "generic_crud" };
  }
  if (status === "ambiguous") return { adapter_required: false, ambiguity_reason: resource.reason, api_surface: surface, confidence: resource.confidence ?? null, evidence: [{ candidates, kind: "generic_crud_candidates" }], match_status: "ambiguous", provider: provider ?? null, read_operation: null, read_path: null, resource_type: resource.resource, source: "generic_crud" };
  if (status === "special") {
    const reads = Array.isArray(resource.read_operations) ? resource.read_operations : [];
    return { adapter_required: true, ambiguity_reason: resource.reason, api_surface: surface, confidence: "static_adapter", evidence: [{ actions: resource.actions ?? [], kind: "special_resource_match", read_operations: reads, reason: resource.reason, special_type: resource.special_type, write_operations: resource.write_operations ?? [] }], match_status: "action_shaped", provider: provider ?? null, read_operation: reads[0] ?? null, read_path: operationPath(reads[0]), resource_type: resource.resource, source: "generic_crud" };
  }
  const adapter = resource.reason === "matched_collection_has_no_standard_detail_path";
  return { adapter_required: adapter, ambiguity_reason: resource.reason, api_surface: surface, confidence: resource.confidence ?? null, evidence: [{ candidates, kind: "generic_crud_miss", reason: resource.reason }], match_status: adapter ? "adapter_required" : "missing", provider: provider ?? null, read_operation: null, read_path: null, resource_type: resource.resource, source: "generic_crud" };
}

function registrySurface(item: JsonObject, provider: string | undefined, prefix: string, key: "fetch" | "read"): JsonObject {
  const matched = item.status === "matched";
  const path = matched ? item.openapi_path ?? item.read_path : null;
  const source = key === "fetch" ? "registry_fetch" : "source_read_registry";
  const ambiguous = item.status === "ambiguous_source_operation";
  const unsupported = key === "read" && item.status === "graphql_source";
  return {
    adapter_required: unsupported,
    ambiguity_reason: matched ? null : item.reason ?? item.status,
    api_surface: prefix || null,
    confidence: matched ? (key === "fetch" ? "registry_fetch" : "source_read") : null,
    evidence: [{
      ...(key === "fetch" ? { fetch_path: item.fetch_path, pagination: item.pagination } : { operation_id: item.operation_id, path_kind: item.path_kind, read_path: item.read_path }),
      kind: key === "fetch" ? "registry_fetch_path" : "source_read_registry",
      match: item.match ?? null, openapi_path: item.openapi_path ?? null,
      reason: item.reason ?? null, variant: item.variant ?? null,
    }],
    match_status: matched ? "matched" : ambiguous ? "ambiguous" : unsupported ? "unsupported_for_now" : "missing",
    provider: provider ?? null,
    read_operation: matched ? item.operation_id ?? (typeof path === "string" ? `GET:${path}` : null) : null,
    read_path: path,
    resource_type: item.resource,
    source,
  };
}

function surfaceMap(
  provider: string | undefined,
  prefix: string,
  resources: readonly JsonObject[],
  fetchCoverage: JsonObject,
  readCoverage: JsonObject,
  coverageWarnings: readonly JsonObject[],
): JsonObject {
  const records = resources.map((item) => surfaceRecord(item, provider, prefix));
  records.push(...objects(fetchCoverage.resources).map((item) => registrySurface(item, provider, prefix, "fetch")));
  records.push(...objects(readCoverage.resources).map((item) => registrySurface(item, provider, prefix, "read")));
  records.sort((a, b) => comparePythonStrings(
    [a.resource_type, a.source, a.match_status, a.read_path ?? "", a.read_operation ?? ""].join("\0"),
    [b.resource_type, b.source, b.match_status, b.read_path ?? "", b.read_operation ?? ""].join("\0"),
  ));
  const bySource: Record<string, Record<string, number>> = {};
  const byStatus: Record<string, number> = {};
  for (const item of records) {
    const source = String(item.source); const status = String(item.match_status);
    bySource[source] ??= {};
    (bySource[source] as Record<string, number>)[status] = ((bySource[source] as Record<string, number>)[status] ?? 0) + 1;
    byStatus[status] = (byStatus[status] ?? 0) + 1;
  }
  const diagnostics = coverageWarnings.map((warning) => ({ code: warning.code, message: warning.message, source: "generic_crud" }));
  for (const [source, coverage] of [["registry_fetch", fetchCoverage], ["source_read_registry", readCoverage]] as const) {
    diagnostics.push(...objects(coverage.warnings).map((warning) => ({ code: warning.code, message: warning.message, source })));
  }
  diagnostics.sort((a, b) => comparePythonStrings(
    `${a.source}\0${String(a.code ?? "")}`,
    `${b.source}\0${String(b.code ?? "")}`,
  ));
  return {
    diagnostics, records, schema_version: 1,
    summary: {
      by_source: Object.fromEntries(sortedStrings(Object.keys(bySource)).map((source) => [source, Object.fromEntries(sortedStrings(Object.keys(bySource[source] ?? {})).map((status) => [status, bySource[source]?.[status]]))])),
      by_status: Object.fromEntries(sortedStrings(Object.keys(byStatus)).map((status) => [status, byStatus[status]])),
      records: records.length,
    },
  };
}

export function buildOpenApiResourceMap(options: {
  readonly schemaData: JsonObject;
  readonly openApi: JsonObject;
  readonly providerSource?: string;
  readonly resourcePrefix?: string;
  readonly apiPrefix?: string;
  readonly registryData?: JsonObject;
}): JsonObject {
  const prefix = options.resourcePrefix ?? "";
  const apiPrefix = options.apiPrefix ?? "/api/";
  const provider = providerFromSchema(options.schemaData, options.providerSource);
  const schemas = object(provider.resource_schemas);
  const resources: JsonObject[] = [];
  const summary: JsonObject = { ambiguous: 0, matched: 0, resources: Object.keys(schemas).length, special: 0, static_provider_gap_resources: 0, unmatched: 0 };
  const families: Record<string, Record<string, number>> = {};
  const surfaces: Record<string, number> = {};
  for (const resourceType of sortedStrings(Object.keys(schemas))) {
    const schema = object(schemas[resourceType]);
    let match = matchResource(options.openApi, resourceType, schema, prefix, apiPrefix);
    if (match.status !== "matched") match = specialResource(options.openApi, resourceType, schema, prefix, apiPrefix) ?? match;
    const item: JsonObject = { resource: resourceType, ...match };
    summary[String(match.status)] = Number(summary[String(match.status)] ?? 0) + 1;
    const resourceFamily = family(resourceType, prefix);
    families[resourceFamily] ??= {};
    (families[resourceFamily] as Record<string, number>)[String(match.status)] = ((families[resourceFamily] as Record<string, number>)[String(match.status)] ?? 0) + 1;
    if (match.status === "matched") item.static_contract = staticContract(options.openApi, schema, String(match.collection_path), String(match.detail_path));
    if (Array.isArray(object(item.static_contract).provider_gap_top_level_paths) && (object(item.static_contract).provider_gap_top_level_paths as unknown[]).length > 0) summary.static_provider_gap_resources = Number(summary.static_provider_gap_resources) + 1;
    if (match.status === "matched" || match.status === "special") {
      const surface = String(item.surface ?? "unknown"); surfaces[surface] = (surfaces[surface] ?? 0) + 1;
    }
    resources.push(item);
  }
  const openApiProfile = profile(options.openApi, apiPrefix);
  const hints = providerHints(provider);
  const coverage = coverageDiagnostics(summary, families, openApiProfile, hints);
  const registry = options.registryData ?? {};
  const fetchCoverage = registryCoverage(options.openApi, apiPrefix, prefix, registry, "fetch");
  const readCoverage = registryCoverage(options.openApi, apiPrefix, prefix, registry, "read");
  return {
    api_prefix: apiPrefix,
    coverage,
    openapi: {
      path_count: Object.keys(object(options.openApi.paths)).length,
      profile: openApiProfile,
      schema_count: Object.keys(object(object(options.openApi.components).schemas)).length,
      version: options.openApi.openapi ?? options.openApi.swagger ?? null,
    },
    provider_config_hints: hints,
    provider_source: options.providerSource ?? null,
    registry_fetch_coverage: fetchCoverage,
    registry_read_coverage: readCoverage,
    resource_prefix: prefix,
    resources,
    summary,
    surface_map: surfaceMap(options.providerSource, prefix, resources, fetchCoverage, readCoverage, objects(coverage.warnings)),
    surfaces: Object.fromEntries(sortedStrings(Object.keys(surfaces)).map((name) => [name, surfaces[name]])),
  };
}
