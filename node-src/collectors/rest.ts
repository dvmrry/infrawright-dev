import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import { LosslessNumber } from "lossless-json";

import { parseDataJsonLosslessly } from "../json/control.js";
import { canonicalPythonNumberToken, pythonFiniteFloatToken } from "../json/python-number.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import {
  fetchExpansionSafetyViolation,
  fetchPathSafetyViolation,
} from "../metadata/resources.js";
import { isObject } from "../metadata/validation.js";
import { fetchProducts, selectFetchResources } from "./selection.js";
export { collectorMaxRetries, retryDelayMs } from "./retry.js";
import type {
  CollectorAdapter,
  CollectorAuthContext,
  CollectorAuthMode,
  CollectorContext,
  FetchResourcesOptions,
  FetchRunResult,
  HttpTransport,
} from "./types.js";

export { selectFetchResources } from "./selection.js";
export type {
  CollectorAdapter,
  CollectorAuthContext,
  CollectorAuthMode,
  CollectorContext,
  FetchResourcesOptions,
  FetchRunResult,
  HttpRequest,
  HttpResponse,
  HttpTransport,
} from "./types.js";

const ZIA_PAGE_SIZE = 1_000;
const ZIA_MAX_PAGES = 100_000;
const ZPA_PAGE_SIZE = 500;
const ZCC_V2_PAGE_SIZE = 100;
const ZCC_V2_MAX_PAGES = 100_000;
const UTF8 = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });

type PaginationStyle = "single" | "zcc_v2" | "zia" | "zpa";

export interface FetchEntry {
  readonly product: string;
  readonly path: string;
  readonly pagination: PaginationStyle;
  readonly envelope?: string;
  readonly expand?: Readonly<Record<string, readonly string[]>>;
  readonly optionalHttpStatuses: ReadonlySet<number>;
  readonly query: Readonly<Record<string, unknown>>;
}

export interface FetchResourceOptions {
  readonly adapter: CollectorAdapter;
  readonly auth: CollectorAuthContext;
  readonly context: CollectorContext;
  readonly entry: FetchEntry;
  readonly mode: CollectorAuthMode;
  readonly resourceType: string;
  readonly transport: HttpTransport;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function maskIdentifiers(text: string): string {
  return text
    .replace(/([/.]|^)([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi, "$1<vanity>$3")
    .replace(/(\/customers\/)[^/]+/gi, "$1<customer-id>");
}

function queryScalar(value: unknown): string {
  if (value === null) return "None";
  if (value === true) return "True";
  if (value === false) return "False";
  if (typeof value === "string") return value;
  if (value instanceof LosslessNumber) {
    return canonicalPythonNumberToken(value.toString()) ?? value.toString();
  }
  if (typeof value === "number") {
    return Number.isInteger(value)
      ? String(value)
      : (pythonFiniteFloatToken(value) ?? String(value));
  }
  throw new Error("fetch query values must be JSON scalars");
}

function percentEncode(value: string, spaceAsPlus: boolean): string {
  if (!value.isWellFormed()) {
    throw new Error("fetch URL components must be valid Unicode strings");
  }
  const bytes = new TextEncoder().encode(value);
  let output = "";
  for (const byte of bytes) {
    const unreserved = (
      (byte >= 0x41 && byte <= 0x5a)
      || (byte >= 0x61 && byte <= 0x7a)
      || (byte >= 0x30 && byte <= 0x39)
      || byte === 0x2d
      || byte === 0x2e
      || byte === 0x5f
      || byte === 0x7e
    );
    if (unreserved) output += String.fromCharCode(byte);
    else if (spaceAsPlus && byte === 0x20) output += "+";
    else output += `%${byte.toString(16).toUpperCase().padStart(2, "0")}`;
  }
  return output;
}

function withQuery(
  url: URL,
  base: Readonly<Record<string, unknown>>,
  additions: Readonly<Record<string, unknown>>,
): URL {
  const values = new Map<string, unknown>(Object.entries(base));
  for (const [key, value] of Object.entries(additions)) values.set(key, value);
  if (values.size === 0) return new URL(url);
  const query = [...values.entries()].map(([key, value]) => {
    return `${percentEncode(key, true)}=${percentEncode(queryScalar(value), true)}`;
  }).join("&");
  const output = new URL(url);
  output.search = `?${query}`;
  return output;
}

function baseUrl(url: URL): string {
  const output = new URL(url);
  output.search = "";
  output.hash = "";
  return output.toString();
}

async function getJson(options: {
  readonly auth: CollectorAuthContext;
  readonly query: Readonly<Record<string, unknown>>;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<unknown> {
  const requested = withQuery(options.url, options.query, {});
  const response = await options.transport.request({
    method: "GET",
    url: requested,
    headers: options.auth.headers,
  });
  if (response.status !== 200) {
    throw new Error(
      `GET ${maskIdentifiers(baseUrl(options.url))} returned HTTP ${response.status}`,
    );
  }
  let text: string;
  try {
    text = UTF8.decode(response.body);
  } catch {
    throw new Error(`GET ${maskIdentifiers(baseUrl(options.url))} returned invalid UTF-8`);
  }
  try {
    return parseDataJsonLosslessly(text);
  } catch {
    throw new Error(`GET ${maskIdentifiers(baseUrl(options.url))} returned invalid JSON`);
  }
}

async function requestPage(options: {
  readonly auth: CollectorAuthContext;
  readonly baseQuery: Readonly<Record<string, unknown>>;
  readonly pageQuery: Readonly<Record<string, unknown>>;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<unknown> {
  return getJson({
    auth: options.auth,
    query: Object.fromEntries(
      new Map([...Object.entries(options.baseQuery), ...Object.entries(options.pageQuery)]),
    ),
    transport: options.transport,
    url: options.url,
  });
}

function itemList(value: unknown, message: string): readonly unknown[] {
  if (!Array.isArray(value)) throw new Error(message);
  return value;
}

function pythonTruthy(value: unknown): boolean {
  if (value === null || value === false || value === undefined) return false;
  if (typeof value === "string" || Array.isArray(value)) return value.length > 0;
  if (typeof value === "number") return value !== 0;
  if (value instanceof LosslessNumber) return Number(value.toString()) !== 0;
  if (isObject(value)) return Object.keys(value).length > 0;
  return true;
}

async function paginateZia(options: {
  readonly auth: CollectorAuthContext;
  readonly entry: FetchEntry;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<readonly unknown[]> {
  const items: unknown[] = [];
  for (let page = 1; ; page += 1) {
    let payload = await requestPage({
      auth: options.auth,
      baseQuery: options.entry.query,
      pageQuery: { page, pageSize: ZIA_PAGE_SIZE },
      transport: options.transport,
      url: options.url,
    });
    if (options.entry.envelope !== undefined) {
      if (!isObject(payload)) {
        throw new Error(
          `ZIA ${maskIdentifiers(baseUrl(options.url))} expected response object with envelope ${JSON.stringify(options.entry.envelope)}`,
        );
      }
      if (!Object.hasOwn(payload, options.entry.envelope)) {
        throw new Error(
          `ZIA ${maskIdentifiers(baseUrl(options.url))} response missing envelope ${JSON.stringify(options.entry.envelope)}`,
        );
      }
      payload = payload[options.entry.envelope];
      if (!Array.isArray(payload)) {
        throw new Error(
          `ZIA ${maskIdentifiers(baseUrl(options.url))} envelope ${JSON.stringify(options.entry.envelope)} did not contain a list page`,
        );
      }
    }
    const batch = itemList(
      payload,
      `ZIA ${maskIdentifiers(baseUrl(options.url))} did not return a list page`,
    );
    items.push(...batch);
    if (batch.length < ZIA_PAGE_SIZE) return items;
    if (page >= ZIA_MAX_PAGES) {
      throw new Error(
        `ZIA ${maskIdentifiers(baseUrl(options.url))} exceeded max_pages=${ZIA_MAX_PAGES}; aborting runaway pagination`,
      );
    }
  }
}

function pythonInt(value: unknown): number {
  if (value instanceof LosslessNumber) {
    const parsed = Number(value.toString());
    if (!Number.isFinite(parsed)) throw new Error("invalid totalPages");
    return Math.trunc(parsed);
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new Error("invalid totalPages");
    return Math.trunc(value);
  }
  if (typeof value === "string" && /^[+-]?\d+$/.test(value.trim())) {
    return Number.parseInt(value, 10);
  }
  if (typeof value === "boolean") return value ? 1 : 0;
  throw new Error("invalid totalPages");
}

async function paginateZpa(options: {
  readonly auth: CollectorAuthContext;
  readonly entry: FetchEntry;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<readonly unknown[]> {
  const items: unknown[] = [];
  for (let page = 1; ; page += 1) {
    const payload = await requestPage({
      auth: options.auth,
      baseQuery: options.entry.query,
      pageQuery: { page, pagesize: ZPA_PAGE_SIZE },
      transport: options.transport,
      url: options.url,
    });
    if (!isObject(payload)) {
      throw new Error(`ZPA ${maskIdentifiers(baseUrl(options.url))} did not return an object page`);
    }
    const rawList = payload.list;
    const batch = !pythonTruthy(rawList)
      ? []
      : itemList(
          rawList,
          `ZPA ${maskIdentifiers(baseUrl(options.url))} list did not contain a list page`,
        );
    items.push(...batch);
    const rawTotal = payload.totalPages;
    const total = !pythonTruthy(rawTotal)
      ? 1
      : pythonInt(rawTotal);
    if (page >= (total || 1)) return items;
  }
}

async function paginateSingle(options: {
  readonly auth: CollectorAuthContext;
  readonly entry: FetchEntry;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<readonly unknown[]> {
  const payload = await getJson({
    auth: options.auth,
    query: options.entry.query,
    transport: options.transport,
    url: options.url,
  });
  return Array.isArray(payload) ? payload : [payload];
}

function numeric(value: unknown, fallback: number): number {
  if (value === undefined) return fallback;
  if (value instanceof LosslessNumber) return Number(value.toString());
  if (typeof value === "number") return value;
  throw new Error("ZCC v2 pagination count metadata must be numeric");
}

async function paginateZccV2(options: {
  readonly auth: CollectorAuthContext;
  readonly entry: FetchEntry;
  readonly transport: HttpTransport;
  readonly url: URL;
}): Promise<readonly unknown[]> {
  const items: unknown[] = [];
  let skip = 0;
  let page = 0;
  while (true) {
    const payload = await requestPage({
      auth: options.auth,
      baseQuery: options.entry.query,
      pageQuery: { skip, perPage: ZCC_V2_PAGE_SIZE },
      transport: options.transport,
      url: options.url,
    });
    if (!isObject(payload)) {
      throw new Error(`ZCC v2 ${maskIdentifiers(baseUrl(options.url))} did not return an object page`);
    }
    const rawItems = payload.items;
    const batch = !pythonTruthy(rawItems)
      ? []
      : itemList(
          rawItems,
          `ZCC v2 ${maskIdentifiers(baseUrl(options.url))} items did not contain a list page`,
        );
    items.push(...batch);
    const count = numeric(payload.count, 0);
    const total = numeric(payload.total, 0);
    const limit = numeric(payload.limit, ZCC_V2_PAGE_SIZE);
    if (count === 0 || batch.length === 0) break;
    if (limit > 0 && count < limit) break;
    if (total > 0 && items.length >= total) break;
    page += 1;
    if (page >= ZCC_V2_MAX_PAGES) {
      throw new Error(
        `ZCC v2 ${maskIdentifiers(baseUrl(options.url))} exceeded max_pages=${ZCC_V2_MAX_PAGES}; aborting runaway pagination`,
      );
    }
    skip += ZCC_V2_PAGE_SIZE;
  }
  return items;
}

function expandedPaths(entry: FetchEntry): string[] {
  const pathViolation = fetchPathSafetyViolation(entry.path);
  if (pathViolation !== null) {
    throw new Error(`fetch path ${pathViolation}`);
  }
  const expand = entry.expand ?? {};
  const keys = sortedStrings(Object.keys(expand));
  if (keys.length === 0) return [entry.path];
  if (keys.length !== 1) {
    throw new Error(`expand supports exactly one placeholder: ${JSON.stringify(keys)}`);
  }
  const key = keys[0];
  if (key === undefined) return [entry.path];
  const token = `{${key}}`;
  if (!entry.path.includes(token)) {
    throw new Error(
      `expand key ${JSON.stringify(key)} not present in path ${JSON.stringify(entry.path)}`,
    );
  }
  return (expand[key] ?? []).map((value) => {
    const violation = fetchExpansionSafetyViolation(value);
    if (violation !== null) {
      throw new Error(`fetch expansion ${JSON.stringify(key)} value ${violation}`);
    }
    return entry.path.split(token).join(percentEncode(value, false));
  });
}

/** Collect one registry resource through a product adapter and generic pager. */
export async function fetchResource(
  options: FetchResourceOptions,
): Promise<readonly unknown[]> {
  const output: unknown[] = [];
  for (const expandedPath of expandedPaths(options.entry)) {
    const url = options.adapter.composeUrl({
      mode: options.mode,
      context: options.context,
      path: expandedPath,
    });
    const pageOptions = {
      auth: options.auth,
      entry: options.entry,
      transport: options.transport,
      url,
    };
    const items = options.entry.pagination === "zia"
      ? await paginateZia(pageOptions)
      : options.entry.pagination === "zpa"
        ? await paginateZpa(pageOptions)
        : options.entry.pagination === "single"
          ? await paginateSingle(pageOptions)
          : await paginateZccV2(pageOptions);
    output.push(...items);
  }
  return output;
}

function fetchEntry(root: LoadedPackRoot, resourceType: string): FetchEntry {
  const resource = root.resources.get(resourceType);
  const raw = resource?.registry.fetch;
  if (resource === undefined || !isObject(raw)) {
    throw new Error(`${JSON.stringify(resourceType)} has no fetch entry in pack registry metadata`);
  }
  const pagination = raw.pagination;
  const fetchPath = raw.path;
  if (
    typeof pagination !== "string"
    || !["single", "zcc_v2", "zia", "zpa"].includes(pagination)
    || typeof fetchPath !== "string"
  ) {
    throw new Error(`${resourceType} has invalid fetch metadata`);
  }
  const query = isObject(raw.query) ? raw.query : {};
  let expand: Readonly<Record<string, readonly string[]>> | undefined;
  if (isObject(raw.expand)) {
    const values: Record<string, readonly string[]> = Object.create(null) as Record<
      string,
      readonly string[]
    >;
    for (const [key, value] of Object.entries(raw.expand)) {
      if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) {
        throw new Error(`${resourceType} has invalid fetch expansion metadata`);
      }
      values[key] = value as string[];
    }
    expand = values;
  }
  const optionalHttpStatuses = new Set<number>();
  if (Array.isArray(raw.optional_http_statuses)) {
    for (const value of raw.optional_http_statuses) {
      const status = value instanceof LosslessNumber
        ? Number(value.toString())
        : value;
      if (typeof status === "number" && Number.isInteger(status)) {
        optionalHttpStatuses.add(status);
      }
    }
  }
  return {
    product: resource.product,
    path: fetchPath,
    pagination: pagination as PaginationStyle,
    ...(typeof raw.envelope === "string" ? { envelope: raw.envelope } : {}),
    ...(expand === undefined ? {} : { expand }),
    optionalHttpStatuses,
    query,
  };
}

function httpStatus(message: string): number | null {
  const match = /HTTP (\d+)/.exec(message);
  return match === null ? null : Number.parseInt(match[1] ?? "", 10);
}

/** Render the same cause-specific remediation hints as the Python collector. */
export function failureHints(reasons: Iterable<string>, scoped = false): string[] {
  const blob = [...reasons].join(" ");
  const hints: string[] = [];
  if (blob.includes("auth failed:")) {
    hints.push(
      "hint: a product's auth FAILED, so all its resources were skipped. 'missing required env var' means that credential is not set; a token/signin HTTP error means the credential was rejected (rotate it or check the Zidentity/ZPA console).",
    );
  }
  if (blob.includes("returned HTTP 401") || blob.includes("returned HTTP 403")) {
    hints.push(
      "hint: HTTP 401/403 means the token was rejected or lacks scope (expired credential, or the API client is missing this product's role); re-issue credentials in the Zidentity console.",
    );
  }
  if (blob.includes("returned HTTP 404")) {
    hints.push(
      "hint: a 404 on ONE endpoint means that path/version is not mounted on the gateway for your cloud (try the v1 equivalent in the registry); 404s on EVERY endpoint of a product mean the API client lacks that product's entitlement (Zidentity console).",
    );
    if (scoped) {
      hints.push(
        "note: only= scoped this run, so the EVERY-endpoint entitlement heuristic above needs an unscoped fetch to be actionable (you are not seeing the full product's paths).",
      );
    }
  }
  if (blob.includes("returned HTTP 5")) {
    hints.push(
      "hint: an HTTP 5xx is a transient gateway/server error or outage; retry shortly, and check the Zscaler status page if it persists.",
    );
  }
  if (hints.length === 0) {
    hints.push("hint: check provider pack auth/proxy/TLS settings and collector diagnostics.");
  }
  hints.push("Successful pulls above are unaffected either way.");
  return hints;
}

function authIdentity(mode: CollectorAuthMode, product: string): string {
  return mode === "oneapi" ? "oneapi" : `${mode}:${product}`;
}

/** Execute the complete registry-driven fetch batch without invoking Python. */
export async function fetchResources(
  options: FetchResourcesOptions,
): Promise<FetchRunResult> {
  const write = options.onDiagnostic ?? (() => undefined);
  const wanted = selectFetchResources({
    root: options.root,
    selectors: options.selectors,
  });
  const neededProducts = new Set(
    wanted.map((resourceType) => fetchEntry(options.root, resourceType).product),
  );
  const authByIdentity = new Map<string, CollectorAuthContext>();
  const failedAuth = new Map<string, string>();
  const authByProduct = new Map<string, CollectorAuthContext>();
  const failedProducts = new Map<string, string>();

  for (const product of fetchProducts(options.root)) {
    if (!neededProducts.has(product)) continue;
    const identity = authIdentity(options.mode, product);
    const priorFailure = failedAuth.get(identity);
    if (priorFailure !== undefined) {
      failedProducts.set(product, priorFailure);
      continue;
    }
    const existing = authByIdentity.get(identity);
    if (existing !== undefined) {
      authByProduct.set(product, existing);
      continue;
    }
    const adapter = options.adapters.get(product);
    if (adapter === undefined) {
      const reason = `no collector adapter for product ${JSON.stringify(product)}`;
      failedAuth.set(identity, reason);
      failedProducts.set(product, reason);
      continue;
    }
    try {
      const auth = await adapter.acquire({
        mode: options.mode,
        environment: options.environment,
        context: options.context,
        transport: options.transport,
      });
      authByIdentity.set(identity, auth);
      authByProduct.set(product, auth);
    } catch (error: unknown) {
      const reason = messageOf(error);
      failedAuth.set(identity, reason);
      failedProducts.set(product, reason);
    }
  }

  await mkdir(options.outputDirectory, { recursive: true });
  const failed: Record<string, string> = Object.create(null) as Record<string, string>;
  const skipped: Record<string, string> = Object.create(null) as Record<string, string>;
  const processed: string[] = [];
  for (const resourceType of wanted) {
    const entry = fetchEntry(options.root, resourceType);
    const productFailure = failedProducts.get(entry.product);
    if (productFailure !== undefined) {
      failed[resourceType] = `auth failed: ${productFailure}`;
      continue;
    }
    const adapter = options.adapters.get(entry.product);
    const auth = authByProduct.get(entry.product);
    if (adapter === undefined || auth === undefined) {
      failed[resourceType] = `auth failed: no collector adapter for product ${JSON.stringify(entry.product)}`;
      continue;
    }
    let items: readonly unknown[];
    try {
      items = await fetchResource({
        adapter,
        auth,
        context: options.context,
        entry,
        mode: options.mode,
        resourceType,
        transport: options.transport,
      });
    } catch (error: unknown) {
      const reason = messageOf(error);
      const status = httpStatus(reason);
      if (status !== null && entry.optionalHttpStatuses.has(status)) {
        skipped[resourceType] = reason;
      } else {
        failed[resourceType] = reason;
      }
      continue;
    }
    const destination = path.join(options.outputDirectory, `${resourceType}.json`);
    await writeFile(destination, renderPythonLosslessArtifactJson(items), "utf8");
    processed.push(resourceType);
    write(`wrote ${destination} (${items.length} items)`);
  }

  const skippedNames = sortedStrings(Object.keys(skipped));
  if (skippedNames.length > 0) {
    write(`\n${skippedNames.length} resource(s) SKIPPED (known optional HTTP status):`);
    for (const resourceType of skippedNames) {
      write(`  ${resourceType}: ${skipped[resourceType]}`);
    }
  }
  const failedNames = sortedStrings(Object.keys(failed));
  if (failedNames.length > 0) {
    write(`\n${failedNames.length} resource(s) FAILED:`);
    for (const resourceType of failedNames) {
      write(`  ${resourceType}: ${failed[resourceType]}`);
    }
    for (const hint of failureHints(Object.values(failed), options.selectors.length > 0)) {
      write(hint);
    }
  }
  return {
    failed,
    processed: Object.freeze(processed),
    skipped,
  };
}
