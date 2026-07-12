import { X509Certificate } from "node:crypto";
import { hasSubscribers } from "node:diagnostics_channel";
import { getCACertificates, type SecureContextOptions } from "node:tls";

import {
  EnvHttpProxyAgent,
  request as undiciRequest,
  type Dispatcher,
} from "undici";

import {
  parseZccOneApiTokenResponseJson,
  renderZccOneApiTokenForm,
  zccOneApiTokenLease,
  type ZccOneApiTokenLease,
} from "../domain/zcc-oneapi-auth.js";
import {
  ZIA_URL_CATEGORIES_PAGE_SIZE,
  ZIA_URL_CATEGORIES_PATH,
} from "../domain/zia-url-categories.js";
import { ProcessFailure } from "../domain/errors.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  type StableReadHooks,
} from "./bounded-files.js";

const ONEAPI_AUDIENCE = "https://api.zscaler.com" as const;
const REQUEST_TIMEOUT_MS = 30_000;
const TRANSACTION_TIMEOUT_MS = 300_000;
const MAX_RESPONSE_BYTES = 16 * 1024 * 1024;
const MAX_CA_BYTES = 4 * 1024 * 1024;
const MAX_ITEMS = 50_000;
const MAX_PAGES = 100_000;
const MAX_RETRIES = 5;
const DNS_LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
const DIAGNOSTIC_CHANNELS = [
  "net.client.socket",
  "undici:request:create",
  "undici:request:bodySent",
  "undici:request:bodyChunkSent",
  "undici:request:bodyChunkReceived",
  "undici:request:headers",
  "undici:request:trailers",
  "undici:request:error",
  "undici:client:beforeConnect",
  "undici:client:connected",
  "undici:client:connectError",
  "undici:client:sendHeaders",
  "undici:proxy:connected",
] as const;

export interface ZiaOneApiHttpRequest {
  readonly body?: string;
  readonly headers: Readonly<Record<string, string>>;
  readonly method: "GET" | "POST";
  readonly signal: AbortSignal;
  readonly url: string;
}

export interface ZiaOneApiHttpResponse {
  readonly body: Uint8Array;
  readonly retryAfter: string | null;
  readonly status: number;
}

export type ZiaOneApiRequester = (
  request: ZiaOneApiHttpRequest,
) => Promise<ZiaOneApiHttpResponse>;

export interface ZiaUrlCategoriesFetchDependencies {
  /** Trusted test seam; production callers omit it. */
  readonly caReadHooks?: StableReadHooks;
  readonly now?: () => number;
  readonly request?: ZiaOneApiRequester;
  readonly sleep?: (milliseconds: number) => Promise<void>;
}

function fail(code: string, message: string, retryable = false): never {
  throw new ProcessFailure({
    code,
    category: "io",
    message,
    retryable,
  });
}

function requiredEnvironment(
  environment: NodeJS.ProcessEnv,
  name: "ZSCALER_CLIENT_ID" | "ZSCALER_CLIENT_SECRET" | "ZSCALER_VANITY_DOMAIN",
): string {
  const value = environment[name];
  if (value === undefined || value === "" || value.includes("\0")) {
    return fail(
      "INVALID_ZIA_ONEAPI_ENVIRONMENT",
      `missing required environment variable ${name}`,
    );
  }
  return value;
}

function normalizedLabel(value: string, name: string): string {
  const normalized = value.trim().toLowerCase();
  if (!DNS_LABEL.test(normalized)) {
    return fail(
      "INVALID_ZIA_ONEAPI_ENVIRONMENT",
      `${name} must be a DNS label`,
    );
  }
  return normalized;
}

function normalizedCloud(value: string | undefined): string {
  const normalized = (value ?? "").trim().toLowerCase();
  if (normalized === "" || normalized === "production") return "";
  return normalizedLabel(normalized, "ZSCALER_CLOUD");
}

function endpoints(environment: NodeJS.ProcessEnv): {
  readonly dataUrl: string;
  readonly tokenUrl: string;
} {
  const cloud = normalizedCloud(environment.ZSCALER_CLOUD);
  const vanity = normalizedLabel(
    requiredEnvironment(environment, "ZSCALER_VANITY_DOMAIN"),
    "ZSCALER_VANITY_DOMAIN",
  );
  const gateway = cloud === ""
    ? "https://api.zsapi.net"
    : `https://api.${cloud}.zsapi.net`;
  const tokenHost = cloud === ""
    ? `https://${vanity}.zslogin.net`
    : `https://${vanity}.zslogin${cloud}.net`;
  return Object.freeze({
    dataUrl: `${gateway}/zia/api/v1/${ZIA_URL_CATEGORIES_PATH}`,
    tokenUrl: `${tokenHost}/oauth2/v1/token`,
  });
}

function retryDelay(attempt: number, retryAfter: string | null): number {
  if (retryAfter !== null) {
    const seconds = Number(retryAfter.trim());
    if (Number.isFinite(seconds)) {
      return Math.max(0, Math.min(seconds * 1000, 30_000));
    }
  }
  return Math.min(1000 * (2 ** attempt), 30_000);
}

function assertDiagnosticsSafe(): void {
  if (DIAGNOSTIC_CHANNELS.some((name) => hasSubscribers(name))) {
    return fail(
      "ZIA_ONEAPI_DIAGNOSTICS_UNSAFE",
      "OneAPI diagnostics subscribers could observe credentials",
    );
  }
}

async function trustedCa(
  environment: NodeJS.ProcessEnv,
  hooks?: StableReadHooks,
): Promise<readonly string[]> {
  const certificates = [...getCACertificates("default")];
  const filePath = environment.REQUESTS_CA_BUNDLE || environment.SSL_CERT_FILE;
  if (filePath === undefined || filePath === "") return certificates;
  let text: string;
  try {
    ({ text } = await readBoundedUtf8File(
      filePath,
      new ReadBudget({
        maxDirectories: 1,
        maxDirectoryEntries: 1,
        maxDepth: 0,
        maxFileBytes: BigInt(MAX_CA_BYTES),
        maxFiles: 1,
        maxTotalBytes: BigInt(MAX_CA_BYTES),
      }),
      { followSymlinks: true, ...(hooks === undefined ? {} : { hooks }) },
    ));
  } catch {
    return fail("ZIA_ONEAPI_CA_FAILED", "configured CA bundle could not be read");
  }
  const pattern = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;
  const custom = [...text.matchAll(pattern)].map((match) => match[0]);
  const residue = text.replace(pattern, "");
  if (
    custom.length === 0
    || residue.split(/\r?\n/).some((line) => {
      const value = line.trim();
      return value !== "" && !value.startsWith("#");
    })
  ) {
    return fail("ZIA_ONEAPI_CA_FAILED", "configured CA bundle is not valid PEM");
  }
  for (const certificate of custom) {
    try {
      new X509Certificate(certificate);
    } catch {
      return fail("ZIA_ONEAPI_CA_FAILED", "configured CA bundle is not valid PEM");
    }
  }
  certificates.push(...custom);
  return Object.freeze(certificates);
}

function proxyValue(
  environment: NodeJS.ProcessEnv,
  lower: "http_proxy" | "https_proxy" | "no_proxy",
  upper: "HTTP_PROXY" | "HTTPS_PROXY" | "NO_PROXY",
): string {
  return environment[lower] ?? environment[upper] ?? "";
}

async function realRequester(
  environment: NodeJS.ProcessEnv,
  caReadHooks?: StableReadHooks,
): Promise<{ readonly close: () => Promise<void>; readonly request: ZiaOneApiRequester }> {
  const ca = await trustedCa(environment, caReadHooks);
  const tls: SecureContextOptions = {
    ca: [...ca],
    minVersion: "TLSv1.2",
  };
  const httpProxy = proxyValue(environment, "http_proxy", "HTTP_PROXY");
  const httpsProxy = proxyValue(environment, "https_proxy", "HTTPS_PROXY") || httpProxy;
  const dispatcher = new EnvHttpProxyAgent({
    allowH2: false,
    bodyTimeout: REQUEST_TIMEOUT_MS,
    connect: tls,
    connections: 1,
    headersTimeout: REQUEST_TIMEOUT_MS,
    httpProxy,
    httpsProxy,
    maxResponseSize: MAX_RESPONSE_BYTES,
    noProxy: proxyValue(environment, "no_proxy", "NO_PROXY"),
    pipelining: 1,
    proxyTls: tls,
    requestTls: tls,
  });
  return {
    async close(): Promise<void> {
      try {
        await dispatcher.close();
      } catch {
        await dispatcher.destroy().catch(() => undefined);
      }
    },
    async request(input): Promise<ZiaOneApiHttpResponse> {
      assertDiagnosticsSafe();
      let response: Awaited<ReturnType<typeof undiciRequest>>;
      try {
        response = await undiciRequest(input.url, {
          ...(input.body === undefined ? {} : { body: input.body }),
          bodyTimeout: REQUEST_TIMEOUT_MS,
          dispatcher: dispatcher as Dispatcher,
          headers: { ...input.headers },
          headersTimeout: REQUEST_TIMEOUT_MS,
          method: input.method,
          signal: input.signal,
        });
      } catch {
        return fail("ZIA_ONEAPI_TRANSPORT_FAILED", "OneAPI request failed", true);
      }
      let body: Uint8Array;
      try {
        body = new Uint8Array(await response.body.arrayBuffer());
      } catch {
        return fail("ZIA_ONEAPI_RESPONSE_FAILED", "OneAPI response could not be read", true);
      }
      if (body.byteLength > MAX_RESPONSE_BYTES) {
        return fail("ZIA_ONEAPI_RESPONSE_LIMIT", "OneAPI response exceeded its limit");
      }
      const retryHeader = response.headers["retry-after"];
      return {
        body,
        retryAfter: typeof retryHeader === "string" ? retryHeader : null,
        status: response.statusCode,
      };
    },
  };
}

async function retryingRequest(options: {
  readonly request: ZiaOneApiRequester;
  readonly requestValue: ZiaOneApiHttpRequest;
  readonly sleep: (milliseconds: number) => Promise<void>;
}): Promise<ZiaOneApiHttpResponse> {
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt += 1) {
    const response = await options.request(options.requestValue);
    if (response.status !== 429) return response;
    if (attempt === MAX_RETRIES) {
      return fail("ZIA_ONEAPI_RATE_LIMITED", "OneAPI remained rate limited", true);
    }
    await options.sleep(retryDelay(attempt, response.retryAfter));
  }
  return fail("ZIA_ONEAPI_RATE_LIMITED", "OneAPI remained rate limited", true);
}

function decodeResponse(body: Uint8Array, message: string): string {
  try {
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(body);
  } catch {
    return fail("INVALID_ZIA_ONEAPI_UTF8", message);
  }
}

function parsePage(body: Uint8Array): readonly unknown[] {
  let value: unknown;
  try {
    value = parseDataJsonLosslessly(decodeResponse(
      body,
      "ZIA URL categories response is not valid UTF-8",
    ));
  } catch {
    return fail("INVALID_ZIA_URL_CATEGORY_RESPONSE", "ZIA URL categories returned invalid JSON");
  }
  if (!Array.isArray(value)) {
    return fail("INVALID_ZIA_URL_CATEGORY_RESPONSE", "ZIA URL categories did not return a list");
  }
  return value;
}

/** Collect real custom URL categories from the ZIA OneAPI endpoint. */
export async function collectZiaUrlCategories(
  environment: NodeJS.ProcessEnv,
  dependencies: ZiaUrlCategoriesFetchDependencies = {},
): Promise<readonly unknown[]> {
  assertDiagnosticsSafe();
  const endpoint = endpoints(environment);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TRANSACTION_TIMEOUT_MS);
  timer.unref();
  const now = dependencies.now ?? Date.now;
  const sleep = dependencies.sleep ?? ((milliseconds) => {
    return new Promise<void>((resolve, reject) => {
      let settled = false;
      const aborted = (): void => {
        if (settled) return;
        settled = true;
        clearTimeout(delay);
        controller.signal.removeEventListener("abort", aborted);
        reject(new ProcessFailure({
          code: "ZIA_ONEAPI_TIMEOUT",
          category: "io",
          message: "ZIA OneAPI transaction timed out",
          retryable: true,
        }));
      };
      const delay = setTimeout(() => {
        if (settled) return;
        settled = true;
        controller.signal.removeEventListener("abort", aborted);
        resolve();
      }, milliseconds);
      controller.signal.addEventListener("abort", aborted, { once: true });
      if (controller.signal.aborted) aborted();
    });
  });
  const real = dependencies.request === undefined
    ? await realRequester(environment, dependencies.caReadHooks)
    : null;
  const request = dependencies.request ?? real?.request;
  if (request === undefined) {
    clearTimeout(timer);
    return fail("ZIA_ONEAPI_TRANSPORT_FAILED", "OneAPI requester is unavailable");
  }
  let token: ZccOneApiTokenLease | null = null;
  const acquire = async (): Promise<ZccOneApiTokenLease> => {
    assertDiagnosticsSafe();
    let form: string;
    try {
      form = renderZccOneApiTokenForm({
        clientId: requiredEnvironment(environment, "ZSCALER_CLIENT_ID"),
        clientSecret: requiredEnvironment(environment, "ZSCALER_CLIENT_SECRET"),
      }, ONEAPI_AUDIENCE);
    } catch {
      return fail("ZIA_ONEAPI_AUTH_FAILED", "OneAPI credentials were invalid");
    }
    const response = await retryingRequest({
      request,
      requestValue: {
        body: form,
        headers: {
          accept: "application/json",
          "accept-encoding": "identity",
          "content-type": "application/x-www-form-urlencoded",
        },
        method: "POST",
        signal: controller.signal,
        url: endpoint.tokenUrl,
      },
      sleep,
    });
    if (response.status !== 200) {
      return fail("ZIA_ONEAPI_AUTH_FAILED", "OneAPI token request was rejected");
    }
    try {
      return zccOneApiTokenLease(
        parseZccOneApiTokenResponseJson(decodeResponse(
          response.body,
          "OneAPI token response is not valid UTF-8",
        )),
        now(),
      );
    } catch {
      return fail("ZIA_ONEAPI_AUTH_FAILED", "OneAPI token response was invalid");
    }
  };
  const accessToken = async (force = false): Promise<string> => {
    if (!force && token !== null && now() < token.refreshAtMs) return token.accessToken;
    token = await acquire();
    return token.accessToken;
  };

  const items: unknown[] = [];
  try {
    for (let page = 1; page <= MAX_PAGES; page += 1) {
      if (controller.signal.aborted) {
        return fail("ZIA_ONEAPI_TIMEOUT", "ZIA OneAPI transaction timed out", true);
      }
      const url = new URL(endpoint.dataUrl);
      url.searchParams.set("customOnly", "true");
      url.searchParams.set("page", String(page));
      url.searchParams.set("pageSize", String(ZIA_URL_CATEGORIES_PAGE_SIZE));
      const send = async (forceToken = false): Promise<ZiaOneApiHttpResponse> => {
        return retryingRequest({
          request,
          requestValue: {
            headers: {
              accept: "application/json",
              "accept-encoding": "identity",
              authorization: `Bearer ${await accessToken(forceToken)}`,
            },
            method: "GET",
            signal: controller.signal,
            url: url.toString(),
          },
          sleep,
        });
      };
      let response = await send();
      if (response.status === 401) response = await send(true);
      if (response.status !== 200) {
        return fail(
          "ZIA_URL_CATEGORY_FETCH_FAILED",
          `ZIA URL categories returned HTTP ${response.status}`,
          response.status >= 500,
        );
      }
      const pageItems = parsePage(response.body);
      if (items.length + pageItems.length > MAX_ITEMS) {
        return fail("ZIA_URL_CATEGORY_ITEM_LIMIT", "ZIA URL categories exceeded the item limit");
      }
      items.push(...pageItems);
      if (pageItems.length < ZIA_URL_CATEGORIES_PAGE_SIZE) {
        return Object.freeze(items);
      }
    }
    return fail("ZIA_URL_CATEGORY_PAGE_LIMIT", "ZIA URL categories exceeded the page limit");
  } finally {
    clearTimeout(timer);
    token = null;
    if (real !== null) await real.close();
  }
}
