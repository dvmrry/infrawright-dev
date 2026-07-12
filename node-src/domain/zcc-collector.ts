import { createHash } from "node:crypto";
import { types as utilTypes } from "node:util";

import { isJsonRecord } from "../json/python-equality.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { parseZccPullDataJson } from "../json/zcc-pull-data.js";
import { ProcessFailure } from "./errors.js";
import {
  loadZccCollectorCatalog,
  zccCollectorResource,
  type ZccCollectorCatalogResource,
  type ZccCollectorResourceType,
} from "./zcc-collector-catalog.js";

export const ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES = 4 * 1024 * 1024;
const MAX_BODY_BYTES = ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES;
const MAX_OVERALL_BODY_BYTES = 4 * 1024 * 1024;
const MAX_FINAL_BYTES = 4 * 1024 * 1024;
const MAX_ITEMS = 50_000;
const MAX_LOGICAL_DATA_REQUESTS = 51;
const MAX_RETRIES = 5;
const RETRY_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 16_000] as const;
const MAX_RETRY_DELAY_MS = 30_000;
const DNS_LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
const DECIMAL_SECONDS = /^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/;
const UTF8_DECODER = new TextDecoder("utf-8", {
  fatal: true,
  // Preserve a leading BOM so the JSON contract rejects it like Python's
  // json.loads on an already-decoded string.
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

export interface ZccOneApiEndpoints {
  readonly audience: "https://api.zscaler.com";
  readonly dataBaseUrl: string;
  readonly tokenUrl: string;
}

export interface ZccCollectorDataRequest {
  readonly kind: "infrawright.zcc_oneapi_data_request";
  readonly method: "GET";
  readonly url: string;
}

export interface ZccCollectorTransportResponse {
  readonly body: Uint8Array;
  readonly retryAfter?: string | null;
  readonly status: number;
}

export type ZccCollectorTransport = (
  request: ZccCollectorDataRequest,
) => ZccCollectorTransportResponse | Promise<ZccCollectorTransportResponse>;

export type ZccCollectorSleep = (milliseconds: number) => void | Promise<void>;

export type ZccCollectorTrustedTransportFailureCode =
  | "INVALID_ZCC_ONEAPI_DATA_REQUEST"
  | "ZCC_ONEAPI_AUTH_HTTP_STATUS"
  | "ZCC_ONEAPI_AUTH_RATE_LIMITED"
  | "ZCC_ONEAPI_AUTH_RESPONSE_INVALID"
  | "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT"
  | "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED"
  | "ZCC_ONEAPI_DATA_RESPONSE_LIMIT"
  | "ZCC_ONEAPI_DATA_TRANSPORT_FAILED"
  | "ZCC_ONEAPI_REDIRECT_REFUSED"
  | "ZCC_ONEAPI_TRANSACTION_TIMEOUT";

const TRUSTED_TRANSPORT_FAILURES: Readonly<Record<
  ZccCollectorTrustedTransportFailureCode,
  Readonly<{
    readonly category: "domain" | "io";
    readonly message: string;
    readonly retryable: boolean;
  }>
>> = Object.freeze({
  INVALID_ZCC_ONEAPI_DATA_REQUEST: Object.freeze({
    category: "domain",
    message: "ZCC OneAPI data request is outside the private authority",
    retryable: false,
  }),
  ZCC_ONEAPI_AUTH_HTTP_STATUS: Object.freeze({
    category: "io",
    message: "ZCC OneAPI authentication returned an unsupported HTTP status",
    retryable: false,
  }),
  ZCC_ONEAPI_AUTH_RATE_LIMITED: Object.freeze({
    category: "io",
    message: "ZCC OneAPI authentication remained rate limited",
    retryable: true,
  }),
  ZCC_ONEAPI_AUTH_RESPONSE_INVALID: Object.freeze({
    category: "io",
    message: "ZCC OneAPI authentication returned an invalid response",
    retryable: false,
  }),
  ZCC_ONEAPI_AUTH_RESPONSE_LIMIT: Object.freeze({
    category: "io",
    message: "ZCC OneAPI authentication response exceeded its limit",
    retryable: false,
  }),
  ZCC_ONEAPI_AUTH_TRANSPORT_FAILED: Object.freeze({
    category: "io",
    message: "ZCC OneAPI authentication transport failed",
    retryable: true,
  }),
  ZCC_ONEAPI_DATA_RESPONSE_LIMIT: Object.freeze({
    category: "io",
    message: "ZCC OneAPI data response exceeded its limit",
    retryable: false,
  }),
  ZCC_ONEAPI_DATA_TRANSPORT_FAILED: Object.freeze({
    category: "io",
    message: "ZCC OneAPI data transport failed",
    retryable: true,
  }),
  ZCC_ONEAPI_REDIRECT_REFUSED: Object.freeze({
    category: "io",
    message: "ZCC OneAPI redirect was refused",
    retryable: false,
  }),
  ZCC_ONEAPI_TRANSACTION_TIMEOUT: Object.freeze({
    category: "io",
    message: "ZCC OneAPI transaction exceeded its deadline",
    retryable: true,
  }),
});

class TrustedTransportFailure extends Error {
  constructor(readonly code: ZccCollectorTrustedTransportFailureCode) {
    super("trusted private ZCC transport failure");
    this.name = "TrustedTransportFailure";
  }
}

/**
 * Throw one closed, secret-free adapter failure. The kernel recreates the
 * public ProcessFailure from its own table and never relays adapter text.
 */
export function throwZccCollectorTransportFailure(
  code: ZccCollectorTrustedTransportFailureCode,
): never {
  throw new TrustedTransportFailure(code);
}

function trustedTransportFailure(error: unknown): ProcessFailure | null {
  let code: ZccCollectorTrustedTransportFailureCode | null = null;
  if (error instanceof TrustedTransportFailure) {
    code = error.code;
  } else if (
    error instanceof ProcessFailure
    && error.code === "ZCC_ONEAPI_TRANSACTION_TIMEOUT"
  ) {
    code = error.code;
  }
  if (code === null || !Object.hasOwn(TRUSTED_TRANSPORT_FAILURES, code)) {
    return null;
  }
  const contract = TRUSTED_TRANSPORT_FAILURES[code];
  return new ProcessFailure({
    category: contract.category,
    code,
    message: contract.message,
    retryable: contract.retryable,
  });
}

export interface ZccCollectedArtifact {
  readonly canonical_json: string;
  readonly metadata: {
    readonly catalog_sources_sha256: string;
    readonly data_requests: number;
    readonly encoding: "utf-8";
    readonly item_count: number;
    readonly kind: "infrawright.zcc_collected_pull";
    readonly media_type: "application/json";
    readonly product: "zcc";
    readonly resource_type: ZccCollectorResourceType;
    readonly schema_version: 1;
    readonly sha256: string;
    readonly size_bytes: number;
    readonly transport_attempts: number;
  };
}

function fail(
  code: string,
  message: string,
  category: "domain" | "io" = "domain",
): never {
  throw new ProcessFailure({
    category,
    code,
    message,
  });
}

function inertRecord(value: unknown, code: string): Readonly<Record<string, unknown>> {
  if (
    typeof value !== "object"
    || value === null
    || utilTypes.isProxy(value)
    || Array.isArray(value)
  ) {
    return fail(code, "ZCC collector input must be plain inert data");
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return fail(code, "ZCC collector input must be plain inert data");
  }
  return value as Readonly<Record<string, unknown>>;
}

function intrinsicGetter<T>(
  getter: ((this: unknown) => unknown) | undefined,
  receiver: unknown,
): T {
  if (getter === undefined) {
    throw new TypeError("required Node 24 intrinsic is unavailable");
  }
  return Reflect.apply(getter, receiver, []) as T;
}

function inertKeys(
  value: Readonly<Record<string, unknown>>,
  code: string,
): string[] {
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string")) {
    return fail(code, "ZCC collector input must contain only inert data fields");
  }
  for (const key of keys as string[]) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return fail(code, "ZCC collector input must contain only inert data fields");
    }
  }
  return keys as string[];
}

function ownValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
  code: string,
): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (
    descriptor === undefined
    || !("value" in descriptor)
    || descriptor.enumerable !== true
  ) {
    return fail(code, "ZCC collector input must be plain inert data");
  }
  return descriptor.value;
}

function exactKeys(
  value: Readonly<Record<string, unknown>>,
  expected: readonly string[],
  code: string,
): void {
  const actual = inertKeys(value, code).sort();
  const sortedExpected = [...expected].sort();
  if (
    actual.length !== sortedExpected.length
    || actual.some((key, index) => key !== sortedExpected[index])
  ) {
    fail(code, "ZCC collector input has an unsupported shape");
  }
}

function primitiveString(value: unknown, code: string): string {
  if (
    typeof value !== "string"
    || !value.isWellFormed()
    || value.includes("\0")
    || Buffer.byteLength(value, "utf8") > 4096
  ) {
    return fail(code, "ZCC collector input contains an invalid string");
  }
  return value;
}

function normalizedLabel(value: unknown, code: string): string {
  const text = primitiveString(value, code).trim().toLowerCase();
  if (!text || !DNS_LABEL.test(text)) {
    return fail(code, "ZCC OneAPI labels must be DNS labels");
  }
  return text;
}

function normalizedCloud(value: unknown, code: string): string {
  const text = primitiveString(value, code).trim().toLowerCase();
  if (text === "" || text === "production") {
    return "";
  }
  if (!DNS_LABEL.test(text)) {
    return fail(code, "ZCC OneAPI cloud must be a DNS label");
  }
  return text;
}

/** Derive the exact Python-compatible OneAPI token and data authorities. */
export function deriveZccOneApiEndpoints(input: {
  readonly cloud: string;
  readonly vanityDomain: string;
}): ZccOneApiEndpoints {
  const code = "INVALID_ZCC_ONEAPI_ENDPOINT_INPUT";
  const record = inertRecord(input, code);
  exactKeys(record, ["cloud", "vanityDomain"], code);
  const cloud = normalizedCloud(ownValue(record, "cloud", code), code);
  const vanity = normalizedLabel(ownValue(record, "vanityDomain", code), code);
  const catalog = loadZccCollectorCatalog();
  const dataBaseUrl = cloud === ""
    ? catalog.oneapi.production_gateway
    : catalog.oneapi.cloud_gateway_template.replace("{cloud}", cloud);
  const tokenHost = cloud === ""
    ? catalog.oneapi.production_token_host_template.replace("{vanity}", vanity)
    : catalog.oneapi.cloud_token_host_template
      .replace("{vanity}", vanity)
      .replace("{cloud}", cloud);
  return Object.freeze({
    audience: catalog.oneapi.audience,
    dataBaseUrl: new URL(dataBaseUrl).toString().replace(/\/$/, ""),
    tokenUrl: new URL(catalog.oneapi.token_path, `${tokenHost}/`).toString(),
  });
}

function dataBaseUrl(cloud: string): string {
  const catalog = loadZccCollectorCatalog();
  return cloud === ""
    ? catalog.oneapi.production_gateway
    : catalog.oneapi.cloud_gateway_template.replace("{cloud}", cloud);
}

function dataRequest(
  resource: ZccCollectorCatalogResource,
  cloud: string,
  page: number | null,
): ZccCollectorDataRequest {
  const url = new URL(resource.path, `${dataBaseUrl(cloud)}/`);
  if (page !== null) {
    url.searchParams.set("page", String(page));
    url.searchParams.set("pageSize", String(resource.page_size));
  }
  return Object.freeze({
    kind: "infrawright.zcc_oneapi_data_request",
    method: resource.method,
    url: url.toString(),
  });
}

/** Pure bounded retry schedule for the collector's accepted decimal syntax. */
export function zccCollectorRetryDelayMs(
  attempt: number,
  retryAfter: string | null | undefined,
): number {
  if (!Number.isSafeInteger(attempt) || attempt < 0 || attempt >= MAX_RETRIES) {
    return fail("INVALID_ZCC_RETRY_ATTEMPT", "ZCC retry attempt is invalid");
  }
  if (retryAfter !== null && retryAfter !== undefined) {
    if (typeof retryAfter !== "string" || !retryAfter.isWellFormed()) {
      return fail("INVALID_ZCC_RETRY_AFTER", "ZCC retry metadata is invalid");
    }
    const text = retryAfter.trim();
    if (text !== "" && DECIMAL_SECONDS.test(text)) {
      const seconds = Number(text);
      if (Number.isFinite(seconds)) {
        return Math.max(0, Math.min(seconds * 1000, MAX_RETRY_DELAY_MS));
      }
      if (seconds === Number.POSITIVE_INFINITY) {
        return MAX_RETRY_DELAY_MS;
      }
      if (seconds === Number.NEGATIVE_INFINITY) {
        return 0;
      }
    }
  }
  return RETRY_DELAYS_MS[attempt] ?? MAX_RETRY_DELAY_MS;
}

class ResponseBudget {
  private consumed = 0;

  consume(body: Uint8Array): void {
    if (body.byteLength > MAX_BODY_BYTES) {
      fail("ZCC_COLLECTOR_RESPONSE_LIMIT", "ZCC response exceeds the body limit");
    }
    this.consumed += body.byteLength;
    if (this.consumed > MAX_OVERALL_BODY_BYTES) {
      fail("ZCC_COLLECTOR_RESPONSE_LIMIT", "ZCC responses exceed the overall limit");
    }
  }
}

function snapshotTransportBody(body: unknown): Uint8Array {
  const code = "INVALID_ZCC_COLLECTOR_RESPONSE";
  if (
    typeof body !== "object"
    || body === null
    || utilTypes.isProxy(body)
    || !utilTypes.isUint8Array(body)
  ) {
    return fail(code, "ZCC transport response is invalid");
  }
  const prototype = Object.getPrototypeOf(body) as unknown;
  if (prototype !== Uint8Array.prototype && prototype !== Buffer.prototype) {
    return fail(code, "ZCC transport response is invalid");
  }

  const backing = intrinsicGetter<unknown>(TYPED_ARRAY_BUFFER_GETTER, body);
  if (
    utilTypes.isSharedArrayBuffer(backing)
    || !utilTypes.isArrayBuffer(backing)
  ) {
    return fail(code, "ZCC transport response is invalid");
  }
  if (
    intrinsicGetter<boolean>(ARRAY_BUFFER_DETACHED_GETTER, backing)
    || intrinsicGetter<boolean>(ARRAY_BUFFER_RESIZABLE_GETTER, backing)
  ) {
    return fail(code, "ZCC transport response is invalid");
  }

  const byteLength = intrinsicGetter<number>(
    TYPED_ARRAY_BYTE_LENGTH_GETTER,
    body,
  );
  if (!Number.isSafeInteger(byteLength) || byteLength < 0) {
    return fail(code, "ZCC transport response is invalid");
  }
  if (byteLength > MAX_BODY_BYTES) {
    return fail(
      "ZCC_COLLECTOR_RESPONSE_LIMIT",
      "ZCC response exceeds the body limit",
    );
  }
  const copy = new Uint8Array(byteLength);
  Reflect.apply(TYPED_ARRAY_SET, copy, [body]);
  return copy;
}

function uncheckedSnapshotResponse(value: unknown): {
  readonly body: Uint8Array;
  readonly retryAfter: string | null | undefined;
  readonly status: number;
} {
  const code = "INVALID_ZCC_COLLECTOR_RESPONSE";
  const record = inertRecord(value, code);
  const keys = inertKeys(record, code).sort();
  const allowed = keys.length === 2
    ? ["body", "status"]
    : ["body", "retryAfter", "status"];
  if (
    keys.length < 2
    || keys.length > 3
    || keys.some((key, index) => key !== allowed[index])
  ) {
    return fail(code, "ZCC transport response has an unsupported shape");
  }
  const status = ownValue(record, "status", code);
  const body = ownValue(record, "body", code);
  const retryAfter = Object.hasOwn(record, "retryAfter")
    ? ownValue(record, "retryAfter", code)
    : undefined;
  if (
    !Number.isSafeInteger(status)
    || (status as number) < 100
    || (status as number) > 599
    || (
      retryAfter !== undefined
      && retryAfter !== null
      && (
        typeof retryAfter !== "string"
        || !retryAfter.isWellFormed()
        || Buffer.byteLength(retryAfter, "utf8") > 4096
      )
    )
  ) {
    return fail(code, "ZCC transport response is invalid");
  }
  return Object.freeze({
    body: snapshotTransportBody(body),
    retryAfter: retryAfter as string | null | undefined,
    status: status as number,
  });
}

function snapshotResponse(value: unknown): {
  readonly body: Uint8Array;
  readonly retryAfter: string | null | undefined;
  readonly status: number;
} {
  try {
    return uncheckedSnapshotResponse(value);
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && error.code === "ZCC_COLLECTOR_RESPONSE_LIMIT"
    ) {
      return fail(
        "ZCC_COLLECTOR_RESPONSE_LIMIT",
        "ZCC response exceeds the body limit",
      );
    }
    return fail(
      "INVALID_ZCC_COLLECTOR_RESPONSE",
      "ZCC transport response is invalid",
    );
  }
}

function parseResponseBody(body: Uint8Array): unknown {
  let text: string;
  try {
    text = UTF8_DECODER.decode(body);
  } catch {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response is not valid UTF-8 JSON");
  }
  let payload: unknown;
  try {
    const wrapper = parseZccPullDataJson(`[${text}]`);
    if (wrapper.length !== 1) {
      return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response is not one JSON value");
    }
    payload = wrapper[0];
    // Validate finite-number and plain-graph constraints for the complete
    // body, including envelope metadata that is not copied into the pull.
    renderPythonLosslessArtifactJson(payload);
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && error.code === "PULL_DATA_COMPLEXITY_LIMIT"
    ) {
      return fail("ZCC_COLLECTOR_RESPONSE_LIMIT", "ZCC response exceeds a structural limit");
    }
    if (error instanceof ProcessFailure && error.code.startsWith("ZCC_")) {
      throw error;
    }
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response is invalid JSON data");
  }
  return payload;
}

function objectItems(value: unknown): readonly Readonly<Record<string, unknown>>[] {
  if (!Array.isArray(value) || value.length > MAX_ITEMS) {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response must contain an item list");
  }
  if (!value.every((item) => isJsonRecord(item))) {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response items must be objects");
  }
  return value;
}

function pageItems(
  payload: unknown,
  resource: ZccCollectorCatalogResource,
): readonly Readonly<Record<string, unknown>>[] {
  if (resource.envelope === null) {
    return objectItems(payload);
  }
  if (!isJsonRecord(payload) || !Object.hasOwn(payload, resource.envelope)) {
    return fail(
      "INVALID_ZCC_COLLECTOR_RESPONSE",
      "ZCC response is missing the required item envelope",
    );
  }
  const descriptor = Object.getOwnPropertyDescriptor(payload, resource.envelope);
  if (descriptor === undefined || !("value" in descriptor)) {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC response envelope is invalid");
  }
  return objectItems(descriptor.value);
}

function singletonItems(payload: unknown): readonly Readonly<Record<string, unknown>>[] {
  if (Array.isArray(payload)) {
    return objectItems(payload);
  }
  if (!isJsonRecord(payload)) {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC singleton response is invalid");
  }
  return [payload];
}

async function safeTransportCall(
  transport: ZccCollectorTransport,
  request: ZccCollectorDataRequest,
): Promise<ReturnType<typeof snapshotResponse>> {
  let rawResponse: unknown;
  try {
    rawResponse = await transport(request);
  } catch (error: unknown) {
    const trusted = trustedTransportFailure(error);
    if (trusted !== null) {
      throw trusted;
    }
    return fail("ZCC_COLLECTOR_TRANSPORT_FAILURE", "ZCC transport failed", "io");
  }
  return snapshotResponse(rawResponse);
}

async function sleepForRetry(
  sleep: ZccCollectorSleep,
  milliseconds: number,
): Promise<void> {
  try {
    await sleep(milliseconds);
  } catch (error: unknown) {
    const trusted = trustedTransportFailure(error);
    if (trusted !== null) {
      throw trusted;
    }
    fail("ZCC_COLLECTOR_RETRY_CLOCK_FAILURE", "ZCC retry clock failed", "io");
  }
}

async function responseWithRetry(options: {
  readonly budget: ResponseBudget;
  readonly request: ZccCollectorDataRequest;
  readonly sleep: ZccCollectorSleep;
  readonly transport: ZccCollectorTransport;
  readonly transportAttempt: () => void;
}): Promise<Uint8Array> {
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt += 1) {
    options.transportAttempt();
    const response = await safeTransportCall(options.transport, options.request);
    options.budget.consume(response.body);
    if (response.status !== 429) {
      if (response.status !== 200) {
        return fail(
          "ZCC_COLLECTOR_HTTP_STATUS",
          "ZCC data request failed with HTTP status",
          "io",
        );
      }
      return response.body;
    }
    if (attempt === MAX_RETRIES) {
      return fail(
        "ZCC_COLLECTOR_RATE_LIMITED",
        "ZCC data request remained rate limited",
        "io",
      );
    }
    await sleepForRetry(
      options.sleep,
      zccCollectorRetryDelayMs(attempt, response.retryAfter),
    );
  }
  return fail("ZCC_COLLECTOR_INTERNAL", "ZCC retry state is unreachable");
}

function collectorOptions(input: unknown): {
  readonly cloud: string;
  readonly resourceType: ZccCollectorResourceType;
  readonly sleep: ZccCollectorSleep;
  readonly transport: ZccCollectorTransport;
} {
  const code = "INVALID_ZCC_COLLECTOR_INPUT";
  const record = inertRecord(input, code);
  exactKeys(record, ["cloud", "resourceType", "sleep", "transport"], code);
  const cloud = normalizedCloud(ownValue(record, "cloud", code), code);
  const resourceType = primitiveString(ownValue(record, "resourceType", code), code);
  const sleep = ownValue(record, "sleep", code);
  const transport = ownValue(record, "transport", code);
  const resource = zccCollectorResource(resourceType);
  if (typeof sleep !== "function" || typeof transport !== "function") {
    return fail(code, "ZCC collector transport and clock must be functions");
  }
  return Object.freeze({
    cloud,
    resourceType: resource.type,
    sleep: sleep as ZccCollectorSleep,
    transport: transport as ZccCollectorTransport,
  });
}

/**
 * Collect one source-bound ZCC resource through an injected authenticated
 * transport. No credentials, environment, network stack, or filesystem are
 * reachable from this private kernel.
 */
export async function collectZccOneApiResource(input: {
  readonly cloud: string;
  readonly resourceType: string;
  readonly sleep: ZccCollectorSleep;
  readonly transport: ZccCollectorTransport;
}): Promise<ZccCollectedArtifact> {
  const options = collectorOptions(input);
  const resource = zccCollectorResource(options.resourceType);
  const budget = new ResponseBudget();
  let dataRequests = 0;
  let transportAttempts = 0;
  const items: Readonly<Record<string, unknown>>[] = [];

  const request = async (page: number | null): Promise<unknown> => {
    dataRequests += 1;
    if (dataRequests > MAX_LOGICAL_DATA_REQUESTS) {
      return fail("ZCC_COLLECTOR_ITEM_LIMIT", "ZCC pagination exceeds the item bound");
    }
    const body = await responseWithRetry({
      budget,
      request: dataRequest(resource, options.cloud, page),
      sleep: options.sleep,
      transport: options.transport,
      transportAttempt: () => {
        transportAttempts += 1;
      },
    });
    return parseResponseBody(body);
  };

  if (resource.pagination === "single") {
    for (const item of singletonItems(await request(null))) {
      items.push(item);
    }
  } else {
    for (let page = 1; page <= MAX_LOGICAL_DATA_REQUESTS; page += 1) {
      const batch = pageItems(await request(page), resource);
      if (batch.length > (resource.page_size ?? 0)) {
        return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC page exceeds the requested size");
      }
      if (items.length + batch.length > MAX_ITEMS) {
        return fail("ZCC_COLLECTOR_ITEM_LIMIT", "ZCC pagination exceeds the item bound");
      }
      for (const item of batch) {
        items.push(item);
      }
      if (batch.length < (resource.page_size ?? 0)) {
        break;
      }
    }
  }

  let canonicalJson: string;
  try {
    canonicalJson = renderPythonLosslessArtifactJson(items);
  } catch {
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC pull cannot be rendered safely");
  }
  const sizeBytes = Buffer.byteLength(canonicalJson, "utf8");
  if (sizeBytes > MAX_FINAL_BYTES) {
    return fail("ZCC_COLLECTOR_RESPONSE_LIMIT", "ZCC pull exceeds the final size limit");
  }
  try {
    // Reuse the aggregate pull guard so limits apply across all pages, not
    // only to each response in isolation.
    parseZccPullDataJson(canonicalJson);
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && error.code === "PULL_DATA_COMPLEXITY_LIMIT"
    ) {
      return fail("ZCC_COLLECTOR_RESPONSE_LIMIT", "ZCC pull exceeds a structural limit");
    }
    return fail("INVALID_ZCC_COLLECTOR_RESPONSE", "ZCC pull cannot be parsed safely");
  }
  const catalog = loadZccCollectorCatalog();
  return Object.freeze({
    canonical_json: canonicalJson,
    metadata: Object.freeze({
      catalog_sources_sha256: catalog.sources_sha256,
      data_requests: dataRequests,
      encoding: "utf-8",
      item_count: items.length,
      kind: "infrawright.zcc_collected_pull",
      media_type: "application/json",
      product: "zcc",
      resource_type: options.resourceType,
      schema_version: 1,
      sha256: createHash("sha256").update(canonicalJson, "utf8").digest("hex"),
      size_bytes: sizeBytes,
      transport_attempts: transportAttempts,
    }),
  });
}
