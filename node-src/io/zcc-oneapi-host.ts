import { constants } from "node:fs";
import { open, type FileHandle } from "node:fs/promises";
import path from "node:path";
import { performance } from "node:perf_hooks";
import { X509Certificate } from "node:crypto";
import {
  createSecureContext,
  getCACertificates,
  type SecureContextOptions,
} from "node:tls";
import { types as utilTypes } from "node:util";

import {
  Client,
  EnvHttpProxyAgent,
  type Dispatcher,
} from "undici";

import { ProcessFailure } from "../domain/errors.js";
import {
  collectZccOneApiResource,
  ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
  type ZccCollectedArtifact,
} from "../domain/zcc-collector.js";
import {
  zccCollectorResource,
  type ZccCollectorResourceType,
} from "../domain/zcc-collector-catalog.js";
import {
  createZccOneApiAuthenticatedTransport,
  zccOneApiDiagnosticsSafe,
  type ZccOneApiHttpRequest,
  type ZccOneApiTransactionControl,
} from "./zcc-oneapi-transport.js";

export const ZCC_ONEAPI_TRANSACTION_TIMEOUT_MS = 300_000;
export const ZCC_ONEAPI_CLEANUP_TIMEOUT_MS = 5_000;
const ZCC_ONEAPI_CA_BUNDLE_LIMIT_BYTES = 4 * 1024 * 1024;
const ZCC_ONEAPI_MAX_ENVIRONMENT_BYTES = 128 * 1024;
const ZCC_ONEAPI_MAX_ENVIRONMENT_VALUE_BYTES = 64 * 1024;
const ZCC_ONEAPI_NETWORK_TIMEOUT_MS = 30_000;
const ZCC_ONEAPI_MAX_HEADER_BYTES = 16 * 1024;

export const ZCC_ONEAPI_HOST_ENVIRONMENT_NAMES = Object.freeze([
  "ZSCALER_CLIENT_ID",
  "ZSCALER_CLIENT_SECRET",
  "ZSCALER_VANITY_DOMAIN",
  "ZSCALER_CLOUD",
  "HTTP_PROXY",
  "http_proxy",
  "HTTPS_PROXY",
  "https_proxy",
  "NO_PROXY",
  "no_proxy",
  "REQUESTS_CA_BUNDLE",
  "SSL_CERT_FILE",
] as const);

const HOST_ENVIRONMENT_NAMES = new Set<string>(
  ZCC_ONEAPI_HOST_ENVIRONMENT_NAMES,
);

export interface ZccOneApiHostInput {
  readonly environment: Readonly<Record<string, string>>;
  readonly resourceType: string;
}

interface FrozenHostInput {
  readonly environment: Readonly<Record<string, string>>;
  readonly resourceType: ZccCollectorResourceType;
}

export interface ZccOneApiManagedTransaction
  extends ZccOneApiTransactionControl {
  finish(): void;
}

export interface ZccOneApiHostDependencies {
  /** Trusted test seam; production uses EnvHttpProxyAgent. */
  readonly createDispatcher?: (
    options: EnvHttpProxyAgent.Options,
  ) => Dispatcher;
  /** Trusted test seam; production uses undici.request. */
  readonly httpRequest?: ZccOneApiHttpRequest;
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" | "internal" = "domain",
  retryable = false,
): never {
  throw new ProcessFailure({ category, code, message, retryable });
}

function plainRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  if (
    value === null
    || typeof value !== "object"
    || utilTypes.isProxy(value)
    || Array.isArray(value)
  ) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function dataProperty(
  value: Readonly<Record<string, unknown>>,
  name: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, name);
  if (
    descriptor === undefined
    || !descriptor.enumerable
    || !("value" in descriptor)
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  return descriptor.value;
}

function snapshotEnvironment(
  value: unknown,
): Readonly<Record<string, string>> {
  if (!plainRecord(value)) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const snapshot = Object.create(null) as Record<string, string>;
  let totalBytes = 0;
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== "string" || !HOST_ENVIRONMENT_NAMES.has(key)) {
      return fail(
        "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
        "ZCC OneAPI host options are invalid",
      );
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
      || typeof descriptor.value !== "string"
      || descriptor.value.includes("\0")
      || !descriptor.value.isWellFormed()
    ) {
      return fail(
        "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
        "ZCC OneAPI host options are invalid",
      );
    }
    const valueBytes = Buffer.byteLength(descriptor.value, "utf8");
    totalBytes += Buffer.byteLength(key, "utf8") + valueBytes;
    if (
      valueBytes > ZCC_ONEAPI_MAX_ENVIRONMENT_VALUE_BYTES
      || totalBytes > ZCC_ONEAPI_MAX_ENVIRONMENT_BYTES
    ) {
      return fail(
        "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
        "ZCC OneAPI host options are invalid",
      );
    }
    snapshot[key] = descriptor.value;
  }
  return Object.freeze(snapshot);
}

function snapshotHostInput(value: ZccOneApiHostInput): FrozenHostInput {
  if (!plainRecord(value)) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const reflectedKeys = Reflect.ownKeys(value);
  if (reflectedKeys.some((key) => typeof key !== "string")) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const keys = (reflectedKeys as string[]).sort();
  if (
    keys.length !== 2
    || keys[0] !== "environment"
    || keys[1] !== "resourceType"
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const environment = snapshotEnvironment(dataProperty(value, "environment"));
  const resourceType = dataProperty(value, "resourceType");
  if (
    typeof resourceType !== "string"
    || !resourceType.isWellFormed()
    || resourceType.includes("\0")
    || Buffer.byteLength(resourceType, "utf8") > 4096
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  let resource: ReturnType<typeof zccCollectorResource>;
  try {
    resource = zccCollectorResource(resourceType);
  } catch {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  return Object.freeze({ environment, resourceType: resource.type });
}

function requiredEnvironment(
  environment: Readonly<Record<string, string>>,
  name: "ZSCALER_CLIENT_ID" | "ZSCALER_CLIENT_SECRET" | "ZSCALER_VANITY_DOMAIN",
): string {
  const value = environment[name];
  if (value === undefined || value.length === 0) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  return value;
}

function selectedEnvironment(
  environment: Readonly<Record<string, string>>,
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

function validatedProxyUrl(value: string): string {
  if (value === "") {
    return "";
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:")
    || parsed.hostname === ""
    || parsed.pathname !== "/"
    || parsed.search !== ""
    || parsed.hash !== ""
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  return parsed.toString();
}

export interface ZccOneApiProxySnapshot {
  readonly httpProxy: string;
  readonly httpsProxy: string;
  readonly noProxy: string;
}

/** Resolve proxy precedence once; explicit empty values prevent ambient fallback. */
export function snapshotZccOneApiProxyEnvironment(
  environment: Readonly<Record<string, string>>,
): ZccOneApiProxySnapshot {
  const http = selectedEnvironment(environment, "http_proxy", "HTTP_PROXY");
  const https = selectedEnvironment(environment, "https_proxy", "HTTPS_PROXY");
  const noProxy = selectedEnvironment(environment, "no_proxy", "NO_PROXY");
  const httpProxy = validatedProxyUrl(http.value);
  const httpsProxy = validatedProxyUrl(
    https.present ? https.value : httpProxy,
  );
  return Object.freeze({
    httpProxy,
    httpsProxy,
    noProxy: noProxy.value,
  });
}

function timeoutFailure(): never {
  return fail(
    "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    "ZCC OneAPI transaction exceeded its deadline",
    "io",
    true,
  );
}

/** Create one monotonic transaction shared by auth, data, retries, and parsing. */
export function createZccOneApiTransaction(
  timeoutMs = ZCC_ONEAPI_TRANSACTION_TIMEOUT_MS,
): ZccOneApiManagedTransaction {
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0 || timeoutMs > 600_000) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const controller = new AbortController();
  const deadline = performance.now() + timeoutMs;
  let finished = false;
  const timer = setTimeout(() => {
    controller.abort();
  }, timeoutMs);
  timer.unref();

  const checkpoint = (): void => {
    if (controller.signal.aborted || performance.now() >= deadline) {
      if (!controller.signal.aborted) {
        controller.abort();
      }
      timeoutFailure();
    }
  };

  return Object.freeze({
    checkpoint,
    finish(): void {
      if (!finished) {
        finished = true;
        clearTimeout(timer);
      }
    },
    now(): number {
      checkpoint();
      return performance.now();
    },
    signal: controller.signal,
    async sleep(milliseconds: number): Promise<void> {
      checkpoint();
      if (!Number.isFinite(milliseconds) || milliseconds < 0) {
        timeoutFailure();
      }
      await new Promise<void>((resolve, reject) => {
        let settled = false;
        const sleepTimer = setTimeout(() => {
          if (!settled) {
            settled = true;
            controller.signal.removeEventListener("abort", onAbort);
            resolve();
          }
        }, milliseconds);
        const onAbort = (): void => {
          if (!settled) {
            settled = true;
            clearTimeout(sleepTimer);
            controller.signal.removeEventListener("abort", onAbort);
            reject(new ProcessFailure({
              category: "io",
              code: "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
              message: "ZCC OneAPI transaction exceeded its deadline",
              retryable: true,
            }));
          }
        };
        controller.signal.addEventListener("abort", onAbort, { once: true });
        if (controller.signal.aborted) {
          onAbort();
        }
      });
      checkpoint();
    },
  });
}

async function readBoundedCaBundle(
  filePath: string,
): Promise<string> {
  let handle: FileHandle | null = null;
  try {
    const absolutePath = path.resolve(filePath);
    handle = await open(
      absolutePath,
      constants.O_RDONLY | constants.O_NONBLOCK,
    );
    const metadata = await handle.stat();
    if (!metadata.isFile() || metadata.size > ZCC_ONEAPI_CA_BUNDLE_LIMIT_BYTES) {
      return fail(
        "ZCC_ONEAPI_CA_BUNDLE_FAILED",
        "ZCC OneAPI CA bundle could not be loaded",
        "io",
      );
    }
    const target = Buffer.allocUnsafe(ZCC_ONEAPI_CA_BUNDLE_LIMIT_BYTES + 1);
    let consumed = 0;
    while (consumed <= ZCC_ONEAPI_CA_BUNDLE_LIMIT_BYTES) {
      const chunkSize = Math.min(64 * 1024, target.byteLength - consumed);
      const read = await handle.read(target, consumed, chunkSize, consumed);
      if (read.bytesRead === 0) {
        break;
      }
      consumed += read.bytesRead;
    }
    if (consumed > ZCC_ONEAPI_CA_BUNDLE_LIMIT_BYTES) {
      return fail(
        "ZCC_ONEAPI_CA_BUNDLE_FAILED",
        "ZCC OneAPI CA bundle could not be loaded",
        "io",
      );
    }
    return new TextDecoder("utf-8", { fatal: true }).decode(
      target.subarray(0, consumed),
    );
  } catch (error: unknown) {
    return fail(
      "ZCC_ONEAPI_CA_BUNDLE_FAILED",
      "ZCC OneAPI CA bundle could not be loaded",
      "io",
    );
  } finally {
    if (handle !== null) {
      try {
        await handle.close();
      } catch {
        // Any read/open/validation outcome remains static and fail-closed.
      }
    }
  }
}

async function trustedCaCertificates(
  environment: Readonly<Record<string, string>>,
): Promise<readonly string[]> {
  try {
    const defaults = [...getCACertificates("default")];
    const customPath = environment.REQUESTS_CA_BUNDLE
      || environment.SSL_CERT_FILE
      || "";
    if (customPath !== "") {
      const custom = await readBoundedCaBundle(customPath);
      const certificatePattern = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;
      const certificates = [...custom.matchAll(certificatePattern)].map(
        (match) => match[0],
      );
      const residue = custom.replace(certificatePattern, "");
      if (
        certificates.length === 0
        || residue.split(/\r?\n/).some((line) => {
          const trimmed = line.trim();
          return trimmed !== "" && !trimmed.startsWith("#");
        })
      ) {
        return fail(
          "ZCC_ONEAPI_CA_BUNDLE_FAILED",
          "ZCC OneAPI CA bundle could not be loaded",
          "io",
        );
      }
      for (const certificate of certificates) {
        try {
          new X509Certificate(certificate);
        } catch {
          return fail(
            "ZCC_ONEAPI_CA_BUNDLE_FAILED",
            "ZCC OneAPI CA bundle could not be loaded",
            "io",
          );
        }
      }
      defaults.push(...certificates);
    }
    const ca = Object.freeze(defaults);
    createSecureContext({ ca: [...ca], minVersion: "TLSv1.2" });
    return ca;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    return fail(
      "ZCC_ONEAPI_CA_BUNDLE_FAILED",
      "ZCC OneAPI CA bundle could not be loaded",
      "io",
    );
  }
}

/** Build the exact local dispatcher options used by the private host. */
export function zccOneApiDispatcherOptions(
  proxy: ZccOneApiProxySnapshot,
  ca: readonly string[],
  networkTimeoutMs = ZCC_ONEAPI_NETWORK_TIMEOUT_MS,
): EnvHttpProxyAgent.Options {
  if (
    !Number.isSafeInteger(networkTimeoutMs)
    || networkTimeoutMs <= 0
    || networkTimeoutMs > ZCC_ONEAPI_NETWORK_TIMEOUT_MS
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const tls: SecureContextOptions & { readonly timeout: number } = Object.freeze({
    ca: [...ca],
    minVersion: "TLSv1.2",
    rejectUnauthorized: true,
    timeout: networkTimeoutMs,
  });
  return Object.freeze({
    allowH2: false,
    bodyTimeout: networkTimeoutMs,
    clientFactory(origin: URL, options: object): Dispatcher {
      const connect = (options as { connect?: Client.Options["connect"] }).connect;
      return new Client(origin, {
        allowH2: false,
        bodyTimeout: networkTimeoutMs,
        ...(connect === undefined ? {} : { connect }),
        headersTimeout: networkTimeoutMs,
        maxHeaderSize: ZCC_ONEAPI_MAX_HEADER_BYTES,
        maxResponseSize: ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
        pipelining: 1,
      });
    },
    connect: tls,
    connections: 1,
    headersTimeout: networkTimeoutMs,
    httpProxy: proxy.httpProxy,
    httpsProxy: proxy.httpsProxy,
    maxHeaderSize: ZCC_ONEAPI_MAX_HEADER_BYTES,
    maxOrigins: 2,
    maxResponseSize: ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
    noProxy: proxy.noProxy,
    pipelining: 1,
    proxyTls: tls,
    requestTls: tls,
  });
}

function cleanupFailure(): ProcessFailure {
  return new ProcessFailure({
    category: "io",
    code: "ZCC_ONEAPI_CLEANUP_FAILED",
    message: "ZCC OneAPI transport cleanup failed",
  });
}

async function withinCleanupDeadline(
  work: Promise<void>,
  deadline: number,
): Promise<boolean> {
  const remaining = Math.max(0, deadline - performance.now());
  if (remaining === 0) {
    void work.catch(() => undefined);
    return false;
  }
  return new Promise<boolean>((resolve) => {
    let settled = false;
    const timer = setTimeout(() => {
      if (!settled) {
        settled = true;
        resolve(false);
      }
    }, remaining);
    void work.then(
      () => {
        if (!settled) {
          settled = true;
          clearTimeout(timer);
          resolve(true);
        }
      },
      () => {
        if (!settled) {
          settled = true;
          clearTimeout(timer);
          resolve(false);
        }
      },
    );
  });
}

/** Gracefully close, then destroy within one fixed cleanup window. */
export async function cleanupZccOneApiDispatcher(
  dispatcher: Dispatcher,
  cleanupTimeoutMs = ZCC_ONEAPI_CLEANUP_TIMEOUT_MS,
): Promise<void> {
  if (
    !Number.isSafeInteger(cleanupTimeoutMs)
    || cleanupTimeoutMs <= 0
    || cleanupTimeoutMs > ZCC_ONEAPI_CLEANUP_TIMEOUT_MS
  ) {
    return fail(
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
      "ZCC OneAPI host options are invalid",
    );
  }
  const deadline = performance.now() + cleanupTimeoutMs;
  let close: Promise<void>;
  try {
    close = dispatcher.close();
  } catch {
    close = Promise.reject(cleanupFailure());
  }
  if (await withinCleanupDeadline(close, deadline)) {
    return;
  }
  let destroy: Promise<void>;
  try {
    destroy = dispatcher.destroy();
  } catch {
    destroy = Promise.reject(cleanupFailure());
  }
  await withinCleanupDeadline(destroy, deadline);
  throw cleanupFailure();
}

function staticPrimaryFailure(error: unknown): ProcessFailure {
  if (error instanceof ProcessFailure) {
    return error;
  }
  return new ProcessFailure({
    category: "internal",
    code: "ZCC_ONEAPI_HOST_FAILED",
    message: "ZCC OneAPI private host failed",
  });
}

function assertZccOneApiHostDiagnosticsSafe(): void {
  if (!zccOneApiDiagnosticsSafe()) {
    return fail(
      "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
      "ZCC OneAPI in-process diagnostics isolation is unavailable",
      "io",
    );
  }
}

async function collectWithDependencies(
  input: ZccOneApiHostInput,
  dependencies: ZccOneApiHostDependencies,
): Promise<ZccCollectedArtifact> {
  assertZccOneApiHostDiagnosticsSafe();
  const options = snapshotHostInput(input);
  // Node filesystem promises cannot be interrupted reliably on mounted/FUSE
  // filesystems. Keep CA input byte-bounded, but begin the abortable network
  // transaction only after trust loading completes.
  const ca = await trustedCaCertificates(options.environment);
  const transaction = createZccOneApiTransaction();
  let dispatcher: Dispatcher | null = null;
  let adapter: ReturnType<typeof createZccOneApiAuthenticatedTransport> | null = null;
  let primary: ProcessFailure | null = null;
  let result: ZccCollectedArtifact | null = null;
  let cleanup: ProcessFailure | null = null;
  try {
    const proxy = snapshotZccOneApiProxyEnvironment(options.environment);
    const dispatcherOptions = zccOneApiDispatcherOptions(proxy, ca);
    try {
      dispatcher = dependencies.createDispatcher?.(dispatcherOptions)
        ?? new EnvHttpProxyAgent(dispatcherOptions);
    } catch {
      return fail(
        "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
        "ZCC OneAPI host options are invalid",
      );
    }
    assertZccOneApiHostDiagnosticsSafe();
    adapter = createZccOneApiAuthenticatedTransport({
      clientId: requiredEnvironment(options.environment, "ZSCALER_CLIENT_ID"),
      clientSecret: requiredEnvironment(
        options.environment,
        "ZSCALER_CLIENT_SECRET",
      ),
      cloud: options.environment.ZSCALER_CLOUD ?? "",
      dispatcher,
      ...(dependencies.httpRequest === undefined
        ? {}
        : { httpRequest: dependencies.httpRequest }),
      resourceType: options.resourceType,
      transaction,
      vanityDomain: requiredEnvironment(
        options.environment,
        "ZSCALER_VANITY_DOMAIN",
      ),
    });
    result = await collectZccOneApiResource({
      cloud: options.environment.ZSCALER_CLOUD ?? "",
      resourceType: options.resourceType,
      sleep: transaction.sleep,
      transport: adapter.transport,
    });
    transaction.checkpoint();
  } catch (error: unknown) {
    primary = staticPrimaryFailure(error);
  } finally {
    transaction.finish();
    adapter?.clearSecrets();
    if (dispatcher !== null) {
      try {
        await cleanupZccOneApiDispatcher(dispatcher);
      } catch (error: unknown) {
        cleanup = staticPrimaryFailure(error);
      }
    }
  }
  if (primary !== null) {
    throw primary;
  }
  if (cleanup !== null) {
    throw cleanup;
  }
  if (result === null) {
    return fail(
      "ZCC_ONEAPI_HOST_FAILED",
      "ZCC OneAPI private host failed",
      "internal",
    );
  }
  return result;
}

/** Private production host: credentials come only from the explicit snapshot. */
export async function collectZccOneApiResourceWithOneApi(
  input: ZccOneApiHostInput,
): Promise<ZccCollectedArtifact> {
  return collectWithDependencies(input, {});
}

/** Trusted private test seam; it is not imported by the process host. */
export async function collectZccOneApiResourceWithOneApiForTest(
  input: ZccOneApiHostInput,
  dependencies: ZccOneApiHostDependencies,
): Promise<ZccCollectedArtifact> {
  return collectWithDependencies(input, dependencies);
}
