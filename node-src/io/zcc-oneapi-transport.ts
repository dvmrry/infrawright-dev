import { types as utilTypes } from "node:util";

import {
  errors as undiciErrors,
  request as undiciRequest,
  type Dispatcher,
} from "undici";

import {
  parseZccOneApiTokenResponseJson,
  renderZccOneApiTokenForm,
  snapshotZccOneApiCredentials,
  zccOneApiTokenLease,
  ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES,
  type ZccOneApiCredentials,
  type ZccOneApiTokenLease,
} from "../domain/zcc-oneapi-auth.js";
import {
  deriveZccOneApiEndpoints,
  throwZccCollectorTransportFailure,
  ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
  zccCollectorRetryDelayMs,
  type ZccCollectorDataRequest,
  type ZccCollectorTransport,
  type ZccCollectorTransportResponse,
} from "../domain/zcc-collector.js";
import {
  zccCollectorResource,
  type ZccCollectorResourceType,
} from "../domain/zcc-collector-catalog.js";

const REQUEST_TIMEOUT_MS = 30_000;
const MAX_AUTH_RETRIES = 5;
const MAX_PAGED_REQUESTS = 51;
const MAX_RESPONSE_CHUNKS = 32 * 1024;
const UTF8_DECODER = new TextDecoder("utf-8", {
  fatal: true,
  ignoreBOM: true,
});
const TYPED_ARRAY_PROTOTYPE = Object.getPrototypeOf(
  Uint8Array.prototype,
) as object;
const TYPED_ARRAY_BUFFER_GETTER = Object.getOwnPropertyDescriptor(
  TYPED_ARRAY_PROTOTYPE,
  "buffer",
)?.get;
const TYPED_ARRAY_BYTE_LENGTH_GETTER = Object.getOwnPropertyDescriptor(
  TYPED_ARRAY_PROTOTYPE,
  "byteLength",
)?.get;
const ARRAY_BUFFER_DETACHED_GETTER = Object.getOwnPropertyDescriptor(
  ArrayBuffer.prototype,
  "detached",
)?.get;
const ARRAY_BUFFER_RESIZABLE_GETTER = Object.getOwnPropertyDescriptor(
  ArrayBuffer.prototype,
  "resizable",
)?.get;
const TYPED_ARRAY_SET = Uint8Array.prototype.set;

class ResponseLimitFailure extends Error {}
class ResponseShapeFailure extends Error {}

export interface ZccOneApiTransactionControl {
  readonly signal: AbortSignal;
  checkpoint(): void;
  now(): number;
  sleep(milliseconds: number): Promise<void>;
}

export interface ZccOneApiResponseBody extends AsyncIterable<unknown> {
  destroy(error?: Error): unknown;
}

export interface ZccOneApiHttpResponse {
  readonly body: ZccOneApiResponseBody;
  readonly headers: Readonly<Record<string, string | readonly string[] | undefined>>;
  readonly statusCode: number;
}

export interface ZccOneApiHttpRequestOptions {
  readonly body?: string;
  readonly bodyTimeout: number;
  readonly dispatcher: Dispatcher;
  readonly headers: Readonly<Record<string, string>>;
  readonly headersTimeout: number;
  readonly method: "GET" | "POST";
  readonly signal: AbortSignal;
  readonly throwOnError: false;
}

export type ZccOneApiHttpRequest = (
  url: string,
  options: ZccOneApiHttpRequestOptions,
) => Promise<ZccOneApiHttpResponse>;

export interface ZccOneApiTransportStats {
  readonly auth_requests: number;
  readonly wire_data_requests: number;
}

export interface ZccOneApiAuthenticatedTransport {
  readonly transport: ZccCollectorTransport;
  clearSecrets(): void;
  stats(): ZccOneApiTransportStats;
}

export interface ZccOneApiTransportOptions extends ZccOneApiCredentials {
  readonly cloud: string;
  readonly dispatcher: Dispatcher;
  readonly resourceType: ZccCollectorResourceType;
  readonly transaction: ZccOneApiTransactionControl;
  readonly vanityDomain: string;
  /** Trusted test seam; production callers omit it. */
  readonly httpRequest?: ZccOneApiHttpRequest;
}

function intrinsicGetter<T>(
  getter: ((this: unknown) => unknown) | undefined,
  receiver: unknown,
): T {
  if (getter === undefined) {
    throw new ResponseShapeFailure();
  }
  return Reflect.apply(getter, receiver, []) as T;
}

function copyChunkWithinLimit(
  value: unknown,
  target: Uint8Array,
  offset: number,
): number {
  if (
    value === null
    || typeof value !== "object"
    || utilTypes.isProxy(value)
    || !utilTypes.isUint8Array(value)
  ) {
    throw new ResponseShapeFailure();
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Uint8Array.prototype && prototype !== Buffer.prototype) {
    throw new ResponseShapeFailure();
  }
  const backing = intrinsicGetter<unknown>(TYPED_ARRAY_BUFFER_GETTER, value);
  if (
    utilTypes.isSharedArrayBuffer(backing)
    || !utilTypes.isArrayBuffer(backing)
    || intrinsicGetter<boolean>(ARRAY_BUFFER_DETACHED_GETTER, backing)
    || intrinsicGetter<boolean>(ARRAY_BUFFER_RESIZABLE_GETTER, backing)
  ) {
    throw new ResponseShapeFailure();
  }
  const length = intrinsicGetter<number>(TYPED_ARRAY_BYTE_LENGTH_GETTER, value);
  if (!Number.isSafeInteger(length) || length < 0) {
    throw new ResponseShapeFailure();
  }
  if (length > target.byteLength - offset) {
    throw new ResponseLimitFailure();
  }
  if (length !== 0) {
    Reflect.apply(TYPED_ARRAY_SET, target, [value, offset]);
  }
  return length;
}

function responseStatus(response: ZccOneApiHttpResponse): number {
  const status = response.statusCode;
  if (!Number.isSafeInteger(status) || status < 100 || status > 599) {
    throw new ResponseShapeFailure();
  }
  return status;
}

function responseHeader(
  response: ZccOneApiHttpResponse,
  name: "content-length" | "retry-after",
): string | null {
  const value = response.headers[name];
  if (
    typeof value !== "string"
    || !value.isWellFormed()
    || Buffer.byteLength(value, "utf8") > 4096
  ) {
    return null;
  }
  return value;
}

function destroyResponseBody(body: ZccOneApiResponseBody): void {
  try {
    body.destroy();
  } catch {
    // Cleanup is best effort here. The request will already fail closed.
  }
}

function preflightContentLength(
  response: ZccOneApiHttpResponse,
  limit: number,
): void {
  const header = responseHeader(response, "content-length");
  if (header === null || !/^(?:0|[1-9][0-9]*)$/.test(header)) {
    return;
  }
  const value = Number(header);
  if (!Number.isSafeInteger(value) || value > limit) {
    throw new ResponseLimitFailure();
  }
}

async function readBoundedBody(
  response: ZccOneApiHttpResponse,
  limit: number,
  transaction: ZccOneApiTransactionControl,
): Promise<Uint8Array> {
  const target = new Uint8Array(limit);
  let consumed = 0;
  let chunkCount = 0;
  try {
    preflightContentLength(response, limit);
    for await (const rawChunk of response.body) {
      transaction.checkpoint();
      chunkCount += 1;
      if (chunkCount > MAX_RESPONSE_CHUNKS) {
        throw new ResponseLimitFailure();
      }
      consumed += copyChunkWithinLimit(rawChunk, target, consumed);
    }
    transaction.checkpoint();
  } catch (error: unknown) {
    destroyResponseBody(response.body);
    throw error;
  }
  return target.slice(0, consumed);
}

const REAL_HTTP_REQUEST: ZccOneApiHttpRequest = async (url, options) => {
  const response = await undiciRequest(url, {
    ...options,
    headers: { ...options.headers },
  });
  return response as unknown as ZccOneApiHttpResponse;
};

function isResponseLimitError(error: unknown): boolean {
  return error instanceof ResponseLimitFailure
    || error instanceof undiciErrors.ResponseExceededMaxSizeError;
}

function failRequest(
  phase: "auth" | "data",
  error: unknown,
  transaction: ZccOneApiTransactionControl,
): never {
  try {
    transaction.checkpoint();
  } catch {
    return throwZccCollectorTransportFailure("ZCC_ONEAPI_TRANSACTION_TIMEOUT");
  }
  if (isResponseLimitError(error)) {
    return throwZccCollectorTransportFailure(
      phase === "auth"
        ? "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT"
        : "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
    );
  }
  return throwZccCollectorTransportFailure(
    phase === "auth"
      ? "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED"
      : "ZCC_ONEAPI_DATA_TRANSPORT_FAILED",
  );
}

function redirect(status: number): boolean {
  return status >= 300 && status <= 399;
}

function allowedDataUrls(
  resourceType: ZccCollectorResourceType,
  dataBaseUrl: string,
): ReadonlySet<string> {
  const resource = zccCollectorResource(resourceType);
  const urls = new Set<string>();
  if (resource.pagination === "single") {
    urls.add(new URL(resource.path, `${dataBaseUrl}/`).toString());
  } else {
    for (let page = 1; page <= MAX_PAGED_REQUESTS; page += 1) {
      const url = new URL(resource.path, `${dataBaseUrl}/`);
      url.searchParams.set("page", String(page));
      url.searchParams.set("pageSize", String(resource.page_size));
      urls.add(url.toString());
    }
  }
  return urls;
}

function authorizedRequest(
  request: ZccCollectorDataRequest,
  allowedUrls: ReadonlySet<string>,
): boolean {
  if (request === null || typeof request !== "object" || utilTypes.isProxy(request)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(request);
  if (prototype !== Object.prototype && prototype !== null) {
    return false;
  }
  const reflectedKeys = Reflect.ownKeys(request);
  if (reflectedKeys.some((key) => typeof key !== "string")) {
    return false;
  }
  const keys = (reflectedKeys as string[]).sort();
  if (
    keys.length !== 3
    || keys[0] !== "kind"
    || keys[1] !== "method"
    || keys[2] !== "url"
  ) {
    return false;
  }
  for (const key of keys) {
    if (typeof key !== "string") {
      return false;
    }
    const descriptor = Object.getOwnPropertyDescriptor(request, key);
    if (
      descriptor === undefined
      || !descriptor.enumerable
      || !("value" in descriptor)
    ) {
      return false;
    }
  }
  return request.kind === "infrawright.zcc_oneapi_data_request"
    && request.method === "GET"
    && allowedUrls.has(request.url);
}

function emptyResponse(
  status: number,
  retryAfter: string | null,
): ZccCollectorTransportResponse {
  return Object.freeze({
    body: new Uint8Array(),
    retryAfter,
    status,
  });
}

/** Create the private authenticated adapter injected into the collector kernel. */
export function createZccOneApiAuthenticatedTransport(
  options: ZccOneApiTransportOptions,
): ZccOneApiAuthenticatedTransport {
  const endpoints = deriveZccOneApiEndpoints({
    cloud: options.cloud,
    vanityDomain: options.vanityDomain,
  });
  let credentials: ZccOneApiCredentials | null = snapshotZccOneApiCredentials({
    clientId: options.clientId,
    clientSecret: options.clientSecret,
  });
  const transaction = options.transaction;
  const dispatcher = options.dispatcher;
  const httpRequest = options.httpRequest ?? REAL_HTTP_REQUEST;
  const allowedUrls = allowedDataUrls(options.resourceType, endpoints.dataBaseUrl);
  let lease: ZccOneApiTokenLease | null = null;
  let refreshPromise: Promise<ZccOneApiTokenLease> | null = null;
  let authRequests = 0;
  let wireDataRequests = 0;

  const acquireToken = async (): Promise<ZccOneApiTokenLease> => {
    const currentCredentials = credentials;
    if (currentCredentials === null) {
      return throwZccCollectorTransportFailure(
        "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
      );
    }
    let form: string;
    try {
      form = renderZccOneApiTokenForm(currentCredentials, endpoints.audience);
    } catch {
      return throwZccCollectorTransportFailure(
        "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
      );
    }
    for (let attempt = 0; attempt <= MAX_AUTH_RETRIES; attempt += 1) {
      transaction.checkpoint();
      let response: ZccOneApiHttpResponse;
      try {
        authRequests += 1;
        response = await httpRequest(endpoints.tokenUrl, {
          body: form,
          bodyTimeout: REQUEST_TIMEOUT_MS,
          dispatcher,
          headers: {
            accept: "application/json",
            "accept-encoding": "identity",
            "content-type": "application/x-www-form-urlencoded",
          },
          headersTimeout: REQUEST_TIMEOUT_MS,
          method: "POST",
          signal: transaction.signal,
          throwOnError: false,
        });
        transaction.checkpoint();
      } catch (error: unknown) {
        return failRequest("auth", error, transaction);
      }
      let status: number;
      try {
        status = responseStatus(response);
      } catch (error: unknown) {
        destroyResponseBody(response.body);
        return failRequest("auth", error, transaction);
      }
      if (redirect(status)) {
        destroyResponseBody(response.body);
        return throwZccCollectorTransportFailure("ZCC_ONEAPI_REDIRECT_REFUSED");
      }
      if (status === 429) {
        const retryAfter = responseHeader(response, "retry-after");
        destroyResponseBody(response.body);
        if (attempt === MAX_AUTH_RETRIES) {
          return throwZccCollectorTransportFailure(
            "ZCC_ONEAPI_AUTH_RATE_LIMITED",
          );
        }
        try {
          await transaction.sleep(zccCollectorRetryDelayMs(attempt, retryAfter));
        } catch (error: unknown) {
          return failRequest("auth", error, transaction);
        }
        continue;
      }
      if (status !== 200) {
        destroyResponseBody(response.body);
        return throwZccCollectorTransportFailure("ZCC_ONEAPI_AUTH_HTTP_STATUS");
      }
      let body: Uint8Array;
      try {
        body = await readBoundedBody(
          response,
          ZCC_ONEAPI_TOKEN_RESPONSE_LIMIT_BYTES,
          transaction,
        );
      } catch (error: unknown) {
        return failRequest("auth", error, transaction);
      }
      let text: string;
      try {
        text = UTF8_DECODER.decode(body);
      } catch {
        return throwZccCollectorTransportFailure(
          "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
        );
      }
      try {
        return zccOneApiTokenLease(
          parseZccOneApiTokenResponseJson(text),
          transaction.now(),
        );
      } catch {
        return throwZccCollectorTransportFailure(
          "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
        );
      }
    }
    return throwZccCollectorTransportFailure("ZCC_ONEAPI_AUTH_RATE_LIMITED");
  };

  const token = async (force: boolean): Promise<ZccOneApiTokenLease> => {
    transaction.checkpoint();
    if (!force && lease !== null && transaction.now() < lease.refreshAtMs) {
      return lease;
    }
    if (refreshPromise !== null) {
      return refreshPromise;
    }
    refreshPromise = acquireToken();
    try {
      lease = await refreshPromise;
      return lease;
    } finally {
      refreshPromise = null;
    }
  };

  const sendData = async (
    request: ZccCollectorDataRequest,
    accessToken: string,
  ): Promise<ZccOneApiHttpResponse> => {
    transaction.checkpoint();
    try {
      wireDataRequests += 1;
      const response = await httpRequest(request.url, {
        bodyTimeout: REQUEST_TIMEOUT_MS,
        dispatcher,
        headers: {
          accept: "application/json",
          "accept-encoding": "identity",
          authorization: `Bearer ${accessToken}`,
        },
        headersTimeout: REQUEST_TIMEOUT_MS,
        method: "GET",
        signal: transaction.signal,
        throwOnError: false,
      });
      transaction.checkpoint();
      return response;
    } catch (error: unknown) {
      return failRequest("data", error, transaction);
    }
  };

  const responseForKernel = async (
    response: ZccOneApiHttpResponse,
  ): Promise<ZccCollectorTransportResponse> => {
    let status: number;
    try {
      status = responseStatus(response);
    } catch (error: unknown) {
      destroyResponseBody(response.body);
      return failRequest("data", error, transaction);
    }
    if (redirect(status)) {
      destroyResponseBody(response.body);
      return throwZccCollectorTransportFailure("ZCC_ONEAPI_REDIRECT_REFUSED");
    }
    const retryAfter = responseHeader(response, "retry-after");
    if (status !== 200) {
      destroyResponseBody(response.body);
      return emptyResponse(status, retryAfter);
    }
    let body: Uint8Array;
    try {
      body = await readBoundedBody(
        response,
        ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
        transaction,
      );
    } catch (error: unknown) {
      return failRequest("data", error, transaction);
    }
    return Object.freeze({ body, retryAfter, status });
  };

  const transport: ZccCollectorTransport = async (request) => {
    if (!authorizedRequest(request, allowedUrls)) {
      return throwZccCollectorTransportFailure("INVALID_ZCC_ONEAPI_DATA_REQUEST");
    }
    const initialLease = await token(false);
    let response = await sendData(request, initialLease.accessToken);
    let status: number;
    try {
      status = responseStatus(response);
    } catch (error: unknown) {
      destroyResponseBody(response.body);
      return failRequest("data", error, transaction);
    }
    if (status === 401) {
      destroyResponseBody(response.body);
      if (lease?.accessToken === initialLease.accessToken) {
        lease = null;
      }
      const refreshed = await token(true);
      response = await sendData(request, refreshed.accessToken);
    }
    return responseForKernel(response);
  };

  return Object.freeze({
    clearSecrets(): void {
      credentials = null;
      lease = null;
      refreshPromise = null;
    },
    stats(): ZccOneApiTransportStats {
      return Object.freeze({
        auth_requests: authRequests,
        wire_data_requests: wireDataRequests,
      });
    },
    transport,
  });
}
