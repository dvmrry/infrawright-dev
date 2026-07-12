import { parseControlJson } from "../json/control.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";
import {
  isZccCollectionResourceType,
  ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES,
  type ZccCollectionResourceType,
} from "../domain/zcc-collection-contract.js";

const REQUEST_MAGIC = Buffer.from("IWRQv001", "ascii");
const RESPONSE_MAGIC = Buffer.from("IWRPv001", "ascii");
const FRAME_HEADER_BYTES = 12;
export const ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES = 512 * 1024;
export const ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES = 10 * 1024 * 1024;

export interface ZccCollectionChildRequest {
  readonly kind: "infrawright.zcc_collection_child_request";
  readonly schema_version: 1;
  readonly environment: Readonly<Record<string, string>>;
  readonly resource_type: ZccCollectionResourceType;
}

export type ZccCollectionChildFailureCode =
  | "INVALID_ZCC_COLLECTION_CHILD_REQUEST"
  | "ZCC_ONEAPI_CA_BUNDLE_FAILED"
  | "ZCC_ONEAPI_CLEANUP_FAILED"
  | "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID"
  | "INVALID_ZCC_COLLECTOR_RESPONSE"
  | "ZCC_COLLECTOR_HTTP_STATUS"
  | "ZCC_COLLECTOR_ITEM_LIMIT"
  | "ZCC_COLLECTOR_RATE_LIMITED"
  | "ZCC_COLLECTOR_RESPONSE_LIMIT"
  | "ZCC_COLLECTOR_RETRY_CLOCK_FAILURE"
  | "ZCC_COLLECTOR_TRANSPORT_FAILURE"
  | "ZCC_ONEAPI_AUTH_HTTP_STATUS"
  | "ZCC_ONEAPI_AUTH_RATE_LIMITED"
  | "ZCC_ONEAPI_AUTH_RESPONSE_INVALID"
  | "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT"
  | "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED"
  | "ZCC_ONEAPI_DATA_RESPONSE_LIMIT"
  | "ZCC_ONEAPI_DATA_TRANSPORT_FAILED"
  | "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE"
  | "ZCC_ONEAPI_REDIRECT_REFUSED"
  | "ZCC_ONEAPI_TRANSACTION_TIMEOUT"
  | "ZCC_ONEAPI_HOST_FAILED";

export interface ZccCollectionChildSuccessResponse {
  readonly kind: "infrawright.zcc_collection_child_response";
  readonly schema_version: 1;
  readonly status: "ok";
  readonly artifact: {
    readonly body_base64: string;
    readonly catalog_sources_sha256: string;
    readonly data_requests: number;
    readonly item_count: number;
    readonly resource_type: ZccCollectionResourceType;
    readonly sha256: string;
    readonly size_bytes: number;
    readonly transport_attempts: number;
  };
}

export interface ZccCollectionChildErrorResponse {
  readonly code: ZccCollectionChildFailureCode;
}

export type ZccCollectionChildResponse =
  | ZccCollectionChildSuccessResponse
  | ZccCollectionChildErrorResponse;

function record(value: unknown): Readonly<Record<string, unknown>> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Readonly<Record<string, unknown>>
    : null;
}

function exactKeys(
  value: Readonly<Record<string, unknown>>,
  expected: readonly string[],
): boolean {
  const actual = Object.keys(value).sort();
  const sorted = [...expected].sort();
  return actual.length === sorted.length
    && actual.every((key, index) => key === sorted[index]);
}

function safeCounter(value: unknown, maximum: number): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
    && (value as number) <= maximum;
}

export function encodeZccCollectionFrame(
  value: JsonValue,
  maximumPayloadBytes: number,
  direction: "request" | "response",
): Buffer {
  const payload = Buffer.from(renderPythonCompatibleJson(value), "utf8");
  if (payload.length > maximumPayloadBytes) {
    payload.fill(0);
    throw new Error("collection child frame exceeds its limit");
  }
  const frame = Buffer.allocUnsafe(FRAME_HEADER_BYTES + payload.length);
  (direction === "request" ? REQUEST_MAGIC : RESPONSE_MAGIC).copy(frame, 0);
  frame.writeUInt32BE(payload.length, 8);
  payload.copy(frame, FRAME_HEADER_BYTES);
  payload.fill(0);
  return frame;
}

export function decodeZccCollectionFrame(
  frame: Uint8Array,
  maximumPayloadBytes: number,
  direction: "request" | "response",
): unknown {
  if (frame.byteLength < FRAME_HEADER_BYTES) {
    throw new Error("collection child frame is truncated");
  }
  const bytes = Buffer.from(frame.buffer, frame.byteOffset, frame.byteLength);
  const expectedMagic = direction === "request" ? REQUEST_MAGIC : RESPONSE_MAGIC;
  if (!bytes.subarray(0, 8).equals(expectedMagic)) {
    throw new Error("collection child frame has invalid magic");
  }
  const length = bytes.readUInt32BE(8);
  if (
    length > maximumPayloadBytes
    || frame.byteLength !== FRAME_HEADER_BYTES + length
  ) {
    throw new Error("collection child frame length is invalid");
  }
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      bytes.subarray(FRAME_HEADER_BYTES),
    );
  } catch {
    throw new Error("collection child frame is not UTF-8");
  }
  return parseControlJson(text);
}

export function validateZccCollectionChildRequest(
  value: unknown,
): value is ZccCollectionChildRequest {
  const root = record(value);
  if (
    root === null
    || !exactKeys(root, ["environment", "kind", "resource_type", "schema_version"])
    || root.kind !== "infrawright.zcc_collection_child_request"
    || root.schema_version !== 1
    || !isZccCollectionResourceType(root.resource_type)
  ) {
    return false;
  }
  const environment = record(root.environment);
  if (environment === null) {
    return false;
  }
  const allowed = new Set<string>(ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES);
  let total = 0;
  for (const [key, candidate] of Object.entries(environment)) {
    if (
      !allowed.has(key)
      || typeof candidate !== "string"
      || !candidate.isWellFormed()
      || candidate.includes("\0")
    ) {
      return false;
    }
    const size = Buffer.byteLength(candidate, "utf8");
    total += Buffer.byteLength(key, "utf8") + size;
    if (size > 64 * 1024 || total > 128 * 1024) {
      return false;
    }
  }
  return true;
}

const CHILD_FAILURE_CODES = new Set<ZccCollectionChildFailureCode>([
  "INVALID_ZCC_COLLECTION_CHILD_REQUEST",
  "ZCC_ONEAPI_CA_BUNDLE_FAILED",
  "ZCC_ONEAPI_CLEANUP_FAILED",
  "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID",
  "INVALID_ZCC_COLLECTOR_RESPONSE",
  "ZCC_COLLECTOR_HTTP_STATUS",
  "ZCC_COLLECTOR_ITEM_LIMIT",
  "ZCC_COLLECTOR_RATE_LIMITED",
  "ZCC_COLLECTOR_RESPONSE_LIMIT",
  "ZCC_COLLECTOR_RETRY_CLOCK_FAILURE",
  "ZCC_COLLECTOR_TRANSPORT_FAILURE",
  "ZCC_ONEAPI_AUTH_HTTP_STATUS",
  "ZCC_ONEAPI_AUTH_RATE_LIMITED",
  "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
  "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT",
  "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
  "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
  "ZCC_ONEAPI_DATA_TRANSPORT_FAILED",
  "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
  "ZCC_ONEAPI_REDIRECT_REFUSED",
  "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
  "ZCC_ONEAPI_HOST_FAILED",
]);

export function validateZccCollectionChildResponse(
  value: unknown,
): value is ZccCollectionChildResponse {
  const root = record(value);
  if (root === null) return false;
  if (exactKeys(root, ["code"])) {
    return typeof root.code === "string"
      && CHILD_FAILURE_CODES.has(root.code as ZccCollectionChildFailureCode);
  }
  if (
    !exactKeys(root, ["artifact", "kind", "schema_version", "status"])
    || root.kind !== "infrawright.zcc_collection_child_response"
    || root.schema_version !== 1
  ) return false;
  const artifact = record(root.artifact);
  return root.status === "ok"
    && artifact !== null
    && exactKeys(artifact, [
      "body_base64", "catalog_sources_sha256", "data_requests", "item_count",
      "resource_type", "sha256", "size_bytes", "transport_attempts",
    ])
    && typeof artifact.body_base64 === "string"
    && artifact.body_base64.length <= 8 * 1024 * 1024
    && typeof artifact.catalog_sources_sha256 === "string"
    && /^[0-9a-f]{64}$/.test(artifact.catalog_sources_sha256)
    && safeCounter(artifact.data_requests, 51)
    && safeCounter(artifact.item_count, 50_000)
    && isZccCollectionResourceType(artifact.resource_type)
    && typeof artifact.sha256 === "string"
    && /^[0-9a-f]{64}$/.test(artifact.sha256)
    && safeCounter(artifact.size_bytes, 4 * 1024 * 1024)
    && safeCounter(artifact.transport_attempts, 306);
}
