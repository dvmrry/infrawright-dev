import { X509Certificate } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import { getCACertificates } from "node:tls";

import {
  EnvHttpProxyAgent,
  errors as undiciErrors,
  parseCookie,
  request as undiciRequest,
  type Cookie,
  type Dispatcher,
} from "undici";

import type {
  HttpRequest,
  HttpResponse,
  HttpTransport,
} from "../collectors/types.js";
import { collectorMaxRetries, retryDelayMs } from "../collectors/retry.js";
import { ProcessFailure } from "../domain/errors.js";

export const REST_HTTP_TIMEOUT_MS = 30_000;
export const REST_HTTP_RESPONSE_LIMIT_BYTES = 64 * 1024 * 1024;

const CA_BUNDLE_LIMIT_BYTES = 4 * 1024 * 1024;
const MAX_REDIRECTS = 10;

export interface RestProxyEnvironment {
  readonly httpProxy: string;
  readonly httpsProxy: string;
  readonly noProxy: string;
}

export interface RestHttpTransportOptions {
  /** Exclude the configured bundle for the system-trust leg of diagnostics. */
  readonly includeCustomCa?: boolean;
  readonly maxRedirects?: number;
  readonly requestTimeoutMs?: number;
  readonly responseLimitBytes?: number;
  /** Test seam; production uses EnvHttpProxyAgent. */
  readonly createDispatcher?: (options: EnvHttpProxyAgent.Options) => Dispatcher;
  /** Test seam; production uses Undici. */
  readonly httpRequest?: RestUndiciRequest;
  readonly sleep?: (milliseconds: number) => void | Promise<void>;
}

interface RestUndiciResponse {
  readonly statusCode: number;
  readonly headers: Readonly<Record<string, string | readonly string[] | undefined>>;
  readonly body: AsyncIterable<unknown> & { destroy(error?: Error): unknown };
}

export type RestUndiciRequest = (
  url: URL,
  options: {
    readonly body?: string | Uint8Array;
    readonly bodyTimeout: number;
    readonly dispatcher: Dispatcher;
    readonly headers: Readonly<Record<string, string>>;
    readonly headersTimeout: number;
    readonly method: "GET" | "POST";
    readonly signal: AbortSignal;
  },
) => Promise<RestUndiciResponse>;

interface StoredCookie {
  readonly creation: number;
  readonly domain: string;
  readonly expiresAt: number | null;
  readonly hostOnly: boolean;
  readonly name: string;
  readonly path: string;
  readonly secure: boolean;
  readonly value: string;
}

function ioFailure(
  code: string,
  message: string,
  retryable = false,
): never {
  throw new ProcessFailure({ category: "io", code, message, retryable });
}

function selectedEnvironment(
  environment: Readonly<Record<string, string | undefined>>,
  lower: "http_proxy" | "https_proxy" | "no_proxy",
  upper: "HTTP_PROXY" | "HTTPS_PROXY" | "NO_PROXY",
): { readonly present: boolean; readonly value: string } {
  if (Object.hasOwn(environment, lower)) {
    return { present: true, value: environment[lower] ?? "" };
  }
  if (Object.hasOwn(environment, upper)) {
    return { present: true, value: environment[upper] ?? "" };
  }
  return { present: false, value: "" };
}

function validProxyUrl(value: string): string {
  if (value === "") return "";
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return ioFailure(
      "INVALID_REST_PROXY",
      "HTTP proxy configuration must be an http:// or https:// URL",
    );
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:")
    || parsed.hostname === ""
    || parsed.pathname !== "/"
    || parsed.search !== ""
    || parsed.hash !== ""
  ) {
    return ioFailure(
      "INVALID_REST_PROXY",
      "HTTP proxy configuration must be an http:// or https:// host URL",
    );
  }
  return parsed.toString();
}

/** Match urllib/environment proxy precedence, including explicit empty values. */
export function snapshotRestProxyEnvironment(
  environment: Readonly<Record<string, string | undefined>>,
): RestProxyEnvironment {
  const http = selectedEnvironment(environment, "http_proxy", "HTTP_PROXY");
  const https = selectedEnvironment(environment, "https_proxy", "HTTPS_PROXY");
  const noProxy = selectedEnvironment(environment, "no_proxy", "NO_PROXY");
  const httpProxy = validProxyUrl(http.value);
  return Object.freeze({
    httpProxy,
    httpsProxy: validProxyUrl(https.present ? https.value : httpProxy),
    noProxy: noProxy.value,
  });
}

async function customCaCertificates(filePath: string): Promise<readonly string[]> {
  try {
    const metadata = await stat(filePath);
    if (!metadata.isFile() || metadata.size > CA_BUNDLE_LIMIT_BYTES) {
      return ioFailure(
        "REST_CA_BUNDLE_FAILED",
        "configured CA bundle could not be loaded",
      );
    }
    const content = await readFile(filePath, "utf8");
    if (Buffer.byteLength(content, "utf8") > CA_BUNDLE_LIMIT_BYTES) {
      return ioFailure(
        "REST_CA_BUNDLE_FAILED",
        "configured CA bundle could not be loaded",
      );
    }
    const certificatePattern = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;
    const certificates = [...content.matchAll(certificatePattern)].map(
      (match) => match[0],
    );
    const residue = content.replace(certificatePattern, "");
    if (
      certificates.length === 0
      || residue.split(/\r?\n/).some((line) => {
        const trimmed = line.trim();
        return trimmed !== "" && !trimmed.startsWith("#");
      })
    ) {
      return ioFailure(
        "REST_CA_BUNDLE_FAILED",
        "configured CA bundle could not be loaded",
      );
    }
    for (const certificate of certificates) new X509Certificate(certificate);
    return certificates;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return ioFailure(
      "REST_CA_BUNDLE_FAILED",
      "configured CA bundle could not be loaded",
    );
  }
}

async function trustedCertificates(
  environment: Readonly<Record<string, string | undefined>>,
  includeCustomCa: boolean,
): Promise<readonly string[]> {
  const certificates = [...getCACertificates("default")];
  const customPath = includeCustomCa
    ? environment.REQUESTS_CA_BUNDLE || environment.SSL_CERT_FILE || ""
    : "";
  if (customPath !== "") {
    certificates.push(...await customCaCertificates(customPath));
  }
  return certificates;
}

function validateBoundedInteger(
  value: number,
  label: string,
  maximum: number,
): number {
  if (!Number.isSafeInteger(value) || value <= 0 || value > maximum) {
    throw new TypeError(`${label} must be a positive bounded integer`);
  }
  return value;
}

function maskIdentifiers(text: string): string {
  return text
    .replace(/([/.]|^)([^/.]+)(\.zslogin[a-z0-9]*\.net)/gi, "$1<vanity>$3")
    .replace(/(\/customers\/)[^/?#]+/gi, "$1<customer-id>");
}

function requestLocation(url: URL): string {
  const safe = new URL(url);
  safe.search = "";
  safe.hash = "";
  safe.username = "";
  safe.password = "";
  return maskIdentifiers(safe.toString());
}

function failureKind(error: unknown): "certificate" | "timeout" | "connection" {
  const code = typeof error === "object" && error !== null && "code" in error
    ? String(error.code)
    : "";
  if (/CERT|TLS|SSL|SELF_SIGNED|UNABLE_TO_VERIFY/i.test(code)) return "certificate";
  if (
    error instanceof undiciErrors.ConnectTimeoutError
    || error instanceof undiciErrors.HeadersTimeoutError
    || error instanceof undiciErrors.BodyTimeoutError
    || error instanceof DOMException && error.name === "TimeoutError"
    || /TIMEOUT|TIMEDOUT/i.test(code)
  ) {
    return "timeout";
  }
  return "connection";
}

function connectionFailure(url: URL, error: unknown): never {
  const kind = failureKind(error);
  const hint = kind === "certificate"
    ? "corporate TLS inspection? set REQUESTS_CA_BUNDLE to the exported proxy root CA"
    : kind === "timeout"
      ? "request timed out; check HTTPS_PROXY/NO_PROXY and outbound connectivity"
      : "check HTTPS_PROXY/NO_PROXY, DNS, and outbound connectivity";
  return ioFailure(
    "REST_HTTP_TRANSPORT_FAILED",
    `cannot reach ${requestLocation(url)} (${kind} failure)\nhint: ${hint}`,
    kind !== "certificate",
  );
}

function headerValue(
  headers: Readonly<Record<string, string | readonly string[] | undefined>>,
  name: string,
): string | null {
  const value = headers[name] ?? headers[name.toLowerCase()];
  if (typeof value === "string") return value;
  return Array.isArray(value) ? value[0] ?? null : null;
}

function responseHeaders(
  headers: Readonly<Record<string, string | readonly string[] | undefined>>,
): Readonly<Record<string, string | readonly string[] | undefined>> {
  const output: Record<string, string | readonly string[] | undefined> = Object.create(null) as Record<
    string,
    string | readonly string[] | undefined
  >;
  for (const [name, value] of Object.entries(headers)) {
    output[name.toLowerCase()] = Array.isArray(value) ? Object.freeze([...value]) : value;
  }
  return Object.freeze(output);
}

function responseStatus(value: number): number {
  if (!Number.isSafeInteger(value) || value < 100 || value > 599) {
    return ioFailure("INVALID_REST_HTTP_RESPONSE", "HTTP response status is invalid");
  }
  return value;
}

function destroyBody(body: RestUndiciResponse["body"]): void {
  try {
    body.destroy();
  } catch {
    // Cleanup is best effort after the request has already failed.
  }
}

async function readBoundedBody(
  response: RestUndiciResponse,
  limit: number,
): Promise<Uint8Array> {
  const declared = headerValue(response.headers, "content-length");
  if (declared !== null && /^(?:0|[1-9][0-9]*)$/.test(declared)) {
    const length = Number(declared);
    if (!Number.isSafeInteger(length) || length > limit) {
      destroyBody(response.body);
      return ioFailure(
        "REST_HTTP_RESPONSE_LIMIT",
        "HTTP response exceeded the collection size limit",
      );
    }
  }
  const chunks: Uint8Array[] = [];
  let total = 0;
  let complete = false;
  try {
    for await (const raw of response.body) {
      if (!(raw instanceof Uint8Array)) {
        return ioFailure(
          "INVALID_REST_HTTP_RESPONSE",
          "HTTP response body is invalid",
        );
      }
      total += raw.byteLength;
      if (total > limit) {
        return ioFailure(
          "REST_HTTP_RESPONSE_LIMIT",
          "HTTP response exceeded the collection size limit",
        );
      }
      chunks.push(Uint8Array.from(raw));
    }
    complete = true;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return ioFailure(
      "REST_HTTP_TRANSPORT_FAILED",
      "HTTP response body could not be read",
      true,
    );
  } finally {
    if (!complete) destroyBody(response.body);
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return output;
}

function defaultCookiePath(url: URL): string {
  const path = url.pathname;
  if (!path.startsWith("/") || path === "/") return "/";
  const lastSlash = path.lastIndexOf("/");
  return lastSlash <= 0 ? "/" : path.slice(0, lastSlash);
}

function domainMatches(host: string, domain: string): boolean {
  return host === domain || host.endsWith(`.${domain}`);
}

function pathMatches(requestPath: string, cookiePath: string): boolean {
  if (requestPath === cookiePath) return true;
  if (!requestPath.startsWith(cookiePath)) return false;
  return cookiePath.endsWith("/") || requestPath[cookiePath.length] === "/";
}

function expiry(cookie: Cookie, now: number): number | null {
  if (cookie.maxAge !== undefined) {
    return now + cookie.maxAge * 1000;
  }
  if (cookie.expires instanceof Date) return cookie.expires.getTime();
  if (typeof cookie.expires === "number") return cookie.expires;
  return null;
}

class CookieJar {
  private readonly cookies = new Map<string, StoredCookie>();
  private nextCreation = 0;

  capture(url: URL, headers: HttpResponse["headers"], now = Date.now()): void {
    const raw = headers["set-cookie"];
    const values = typeof raw === "string" ? [raw] : Array.isArray(raw) ? raw : [];
    for (const value of values) {
      const parsed = parseCookie(value);
      if (parsed === null || parsed.name === "") continue;
      const responseHost = url.hostname.toLowerCase();
      const requestedDomain = parsed.domain?.replace(/^\./, "").toLowerCase();
      if (requestedDomain !== undefined && !domainMatches(responseHost, requestedDomain)) {
        continue;
      }
      const domain = requestedDomain ?? responseHost;
      const cookiePath = parsed.path?.startsWith("/") === true
        ? parsed.path
        : defaultCookiePath(url);
      const key = `${parsed.name}\0${domain}\0${cookiePath}`;
      const expiresAt = expiry(parsed, now);
      if (parsed.maxAge !== undefined && parsed.maxAge <= 0 || expiresAt !== null && expiresAt <= now) {
        this.cookies.delete(key);
        continue;
      }
      const previous = this.cookies.get(key);
      this.cookies.set(key, {
        creation: previous?.creation ?? this.nextCreation++,
        domain,
        expiresAt,
        hostOnly: requestedDomain === undefined,
        name: parsed.name,
        path: cookiePath,
        secure: parsed.secure === true,
        value: parsed.value,
      });
    }
  }

  header(url: URL, now = Date.now()): string | null {
    const host = url.hostname.toLowerCase();
    const path = url.pathname || "/";
    const matches: StoredCookie[] = [];
    for (const [key, cookie] of this.cookies) {
      if (cookie.expiresAt !== null && cookie.expiresAt <= now) {
        this.cookies.delete(key);
        continue;
      }
      if (
        (cookie.hostOnly ? host !== cookie.domain : !domainMatches(host, cookie.domain))
        || !pathMatches(path, cookie.path)
        || cookie.secure && url.protocol !== "https:"
      ) {
        continue;
      }
      matches.push(cookie);
    }
    matches.sort((left, right) => {
      return right.path.length - left.path.length || left.creation - right.creation;
    });
    return matches.length === 0
      ? null
      : matches.map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
  }
}

function redirectStatus(status: number): boolean {
  return status === 301 || status === 302 || status === 303
    || status === 307 || status === 308;
}

const REAL_HTTP_REQUEST: RestUndiciRequest = async (url, options) => {
  const response = await undiciRequest(url, {
    ...options,
    headers: { ...options.headers },
  });
  response.body.on("error", () => undefined);
  return response as unknown as RestUndiciResponse;
};

/**
 * Build the ordinary network transport used by the registry-driven collector.
 * It owns proxy/TLS setup, redirects, 429 retries, response bounds, and the
 * cookie jar needed by legacy ZIA session authentication.
 */
export async function createRestHttpTransport(
  environment: Readonly<Record<string, string | undefined>>,
  options: RestHttpTransportOptions = {},
): Promise<HttpTransport> {
  const timeoutMs = validateBoundedInteger(
    options.requestTimeoutMs ?? REST_HTTP_TIMEOUT_MS,
    "request timeout",
    10 * 60 * 1000,
  );
  const responseLimit = validateBoundedInteger(
    options.responseLimitBytes ?? REST_HTTP_RESPONSE_LIMIT_BYTES,
    "response limit",
    1024 * 1024 * 1024,
  );
  const maxRedirects = options.maxRedirects ?? MAX_REDIRECTS;
  if (!Number.isSafeInteger(maxRedirects) || maxRedirects < 0 || maxRedirects > 20) {
    throw new TypeError("max redirects must be between 0 and 20");
  }
  const ca = await trustedCertificates(environment, options.includeCustomCa !== false);
  const proxy = snapshotRestProxyEnvironment(environment);
  const tls = {
    ca: [...ca],
    minVersion: "TLSv1.2" as const,
    rejectUnauthorized: true,
  };
  const dispatcherOptions: EnvHttpProxyAgent.Options = {
    connect: tls,
    httpProxy: proxy.httpProxy,
    httpsProxy: proxy.httpsProxy,
    noProxy: proxy.noProxy,
    proxyTls: tls,
    requestTls: tls,
  };
  const dispatcher = options.createDispatcher?.(dispatcherOptions)
    ?? new EnvHttpProxyAgent(dispatcherOptions);
  const requestWire = options.httpRequest ?? REAL_HTTP_REQUEST;
  const sleep = options.sleep ?? ((milliseconds: number) => {
    return new Promise<void>((resolve) => setTimeout(resolve, milliseconds));
  });
  const jar = new CookieJar();
  let closed = false;

  const requestOnce = async (input: HttpRequest): Promise<HttpResponse> => {
    let url = new URL(input.url.toString());
    let method = input.method;
    let body = input.body;
    let baseHeaders = { ...(input.headers ?? {}) };
    for (let redirect = 0; ; redirect += 1) {
      if (redirect > maxRedirects) {
        return ioFailure(
          "REST_HTTP_REDIRECT_LIMIT",
          `too many redirects while requesting ${requestLocation(input.url)}`,
        );
      }
      const headers = { ...baseHeaders };
      const cookie = jar.header(url);
      if (cookie !== null && !Object.keys(headers).some((name) => name.toLowerCase() === "cookie")) {
        headers.cookie = cookie;
      }
      const selectedTimeout = input.timeoutMs ?? timeoutMs;
      validateBoundedInteger(selectedTimeout, "request timeout", 10 * 60 * 1000);
      let raw: RestUndiciResponse;
      try {
        raw = await requestWire(url, {
          ...(body === undefined ? {} : { body }),
          bodyTimeout: selectedTimeout,
          dispatcher,
          headers,
          headersTimeout: selectedTimeout,
          method,
          signal: AbortSignal.timeout(selectedTimeout),
        });
      } catch (error: unknown) {
        return connectionFailure(url, error);
      }
      const status = responseStatus(raw.statusCode);
      const normalizedHeaders = responseHeaders(raw.headers);
      jar.capture(url, normalizedHeaders);
      if (!redirectStatus(status)) {
        return Object.freeze({
          body: await readBoundedBody(raw, responseLimit),
          headers: normalizedHeaders,
          status,
        });
      }
      const location = headerValue(normalizedHeaders, "location");
      destroyBody(raw.body);
      if (location === null) {
        return ioFailure(
          "INVALID_REST_HTTP_RESPONSE",
          "redirect response is missing a location header",
        );
      }
      let next: URL;
      try {
        next = new URL(location, url);
      } catch {
        return ioFailure(
          "INVALID_REST_HTTP_RESPONSE",
          "redirect response has an invalid location header",
        );
      }
      if (next.protocol !== "https:" && next.protocol !== "http:") {
        return ioFailure(
          "REST_HTTP_REDIRECT_REFUSED",
          "redirect response selected an unsupported URL scheme",
        );
      }
      if (next.origin !== url.origin) {
        baseHeaders = Object.fromEntries(
          Object.entries(baseHeaders).filter(([name]) => {
            const lower = name.toLowerCase();
            return lower !== "authorization" && lower !== "cookie";
          }),
        );
      }
      if (status === 303 || (status === 301 || status === 302) && method === "POST") {
        method = "GET";
        body = undefined;
        baseHeaders = Object.fromEntries(
          Object.entries(baseHeaders).filter(([name]) => {
            return name.toLowerCase() !== "content-type"
              && name.toLowerCase() !== "content-length";
          }),
        );
      }
      url = next;
    }
  };

  return Object.freeze({
    async close(): Promise<void> {
      if (closed) return;
      closed = true;
      try {
        await dispatcher.close();
      } catch {
        try {
          await dispatcher.destroy();
        } catch {
          return ioFailure(
            "REST_HTTP_CLEANUP_FAILED",
            "HTTP transport cleanup failed",
          );
        }
      }
    },
    async request(request: HttpRequest): Promise<HttpResponse> {
      if (closed) {
        return ioFailure("REST_HTTP_CLOSED", "HTTP transport is already closed");
      }
      const maximumRetries = collectorMaxRetries();
      for (let attempt = 0; attempt <= maximumRetries; attempt += 1) {
        const response = await requestOnce(request);
        if (response.status !== 429 || attempt === maximumRetries) return response;
        const retryAfter = headerValue(response.headers, "retry-after");
        try {
          await sleep(retryDelayMs(attempt, retryAfter));
        } catch {
          return ioFailure(
            "REST_HTTP_RETRY_CLOCK_FAILED",
            "HTTP retry clock failed",
            true,
          );
        }
      }
      return ioFailure("REST_HTTP_INTERNAL", "HTTP retry state is unreachable");
    },
  });
}
