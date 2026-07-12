import { createReadStream, createWriteStream } from "node:fs";
import { pathToFileURL } from "node:url";

import { ProcessFailure } from "../domain/errors.js";
import type { ZccCollectedArtifact } from "../domain/zcc-collection-contract.js";
import { collectZccOneApiResourceWithOneApi } from "../io/zcc-oneapi-host.js";
import { zccOneApiDiagnosticsSafe } from "../io/zcc-oneapi-transport.js";
import {
  decodeZccCollectionFrame,
  encodeZccCollectionFrame,
  validateZccCollectionChildRequest,
  ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
  ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
  type ZccCollectionChildFailureCode,
  type ZccCollectionChildRequest,
  type ZccCollectionChildResponse,
} from "../io/zcc-collection-protocol.js";
import type { JsonValue } from "../json/python-compatible.js";

const MAX_FRAME_BYTES = ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES + 12;
const MAX_CHUNKS = 32 * 1024;

const CHILD_FAILURE_MAP: Readonly<Record<string, ZccCollectionChildFailureCode>> =
  Object.freeze({
    INVALID_ZCC_ONEAPI_HOST_OPTIONS: "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID",
    ZCC_ONEAPI_CA_BUNDLE_FAILED: "ZCC_ONEAPI_CA_BUNDLE_FAILED",
    ZCC_ONEAPI_CLEANUP_FAILED: "ZCC_ONEAPI_CLEANUP_FAILED",
    ZCC_ONEAPI_DIAGNOSTICS_UNSAFE: "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
    ZCC_ONEAPI_HOST_FAILED: "ZCC_ONEAPI_HOST_FAILED",
    ZCC_ONEAPI_TRANSACTION_TIMEOUT: "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    INVALID_ZCC_COLLECTOR_RESPONSE: "INVALID_ZCC_COLLECTOR_RESPONSE",
    ZCC_COLLECTOR_HTTP_STATUS: "ZCC_COLLECTOR_HTTP_STATUS",
    ZCC_COLLECTOR_ITEM_LIMIT: "ZCC_COLLECTOR_ITEM_LIMIT",
    ZCC_COLLECTOR_RATE_LIMITED: "ZCC_COLLECTOR_RATE_LIMITED",
    ZCC_COLLECTOR_RESPONSE_LIMIT: "ZCC_COLLECTOR_RESPONSE_LIMIT",
    ZCC_COLLECTOR_RETRY_CLOCK_FAILURE: "ZCC_COLLECTOR_RETRY_CLOCK_FAILURE",
    ZCC_COLLECTOR_TRANSPORT_FAILURE: "ZCC_COLLECTOR_TRANSPORT_FAILURE",
    INVALID_ZCC_ONEAPI_DATA_REQUEST: "ZCC_ONEAPI_HOST_FAILED",
    ZCC_ONEAPI_AUTH_HTTP_STATUS: "ZCC_ONEAPI_AUTH_HTTP_STATUS",
    ZCC_ONEAPI_AUTH_RATE_LIMITED: "ZCC_ONEAPI_AUTH_RATE_LIMITED",
    ZCC_ONEAPI_AUTH_RESPONSE_INVALID: "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
    ZCC_ONEAPI_AUTH_RESPONSE_LIMIT: "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT",
    ZCC_ONEAPI_AUTH_TRANSPORT_FAILED: "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
    ZCC_ONEAPI_DATA_RESPONSE_LIMIT: "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
    ZCC_ONEAPI_DATA_TRANSPORT_FAILED: "ZCC_ONEAPI_DATA_TRANSPORT_FAILED",
    ZCC_ONEAPI_REDIRECT_REFUSED: "ZCC_ONEAPI_REDIRECT_REFUSED",
  });

async function readOneFrame(input: AsyncIterable<unknown>): Promise<Buffer> {
  const target = Buffer.allocUnsafe(MAX_FRAME_BYTES);
  let length = 0;
  let chunks = 0;
  try {
    for await (const raw of input) {
      chunks += 1;
      if (chunks > MAX_CHUNKS || !(raw instanceof Uint8Array)) {
        throw new Error("invalid child request stream");
      }
      const chunk = Buffer.from(raw.buffer, raw.byteOffset, raw.byteLength);
      if (chunk.length > target.length - length) {
        throw new Error("child request stream exceeds its bound");
      }
      chunk.copy(target, length);
      length += chunk.length;
    }
    return Buffer.from(target.subarray(0, length));
  } finally {
    target.fill(0);
  }
}

function childError(code: ZccCollectionChildFailureCode): ZccCollectionChildResponse {
  return { code };
}

function childSuccess(artifact: ZccCollectedArtifact): ZccCollectionChildResponse {
  const bytes = Buffer.from(artifact.canonical_json, "utf8");
  try {
    return {
      kind: "infrawright.zcc_collection_child_response",
      schema_version: 1,
      status: "ok",
      artifact: {
        body_base64: bytes.toString("base64"),
        catalog_sources_sha256: artifact.metadata.catalog_sources_sha256,
        data_requests: artifact.metadata.data_requests,
        item_count: artifact.metadata.item_count,
        resource_type: artifact.metadata.resource_type,
        sha256: artifact.metadata.sha256,
        size_bytes: artifact.metadata.size_bytes,
        transport_attempts: artifact.metadata.transport_attempts,
      },
    };
  } finally {
    bytes.fill(0);
  }
}

function staticFailure(error: unknown): ZccCollectionChildFailureCode {
  if (
    error instanceof ProcessFailure
    && Object.hasOwn(CHILD_FAILURE_MAP, error.code)
  ) {
    return CHILD_FAILURE_MAP[error.code] ?? "ZCC_ONEAPI_HOST_FAILED";
  }
  return "ZCC_ONEAPI_HOST_FAILED";
}

export interface ZccCollectionChildIo {
  readonly input: AsyncIterable<unknown>;
  readonly write: (frame: Buffer) => Promise<void>;
  readonly collect?: (
    input: ZccCollectionChildRequest,
  ) => Promise<ZccCollectedArtifact>;
}

/** Run one private child transaction. Diagnostics are checked before fd3. */
export async function runZccCollectionChild(io: ZccCollectionChildIo): Promise<void> {
  let response: ZccCollectionChildResponse;
  if (!zccOneApiDiagnosticsSafe()) {
    response = childError("ZCC_ONEAPI_DIAGNOSTICS_UNSAFE");
  } else {
    let requestFrame: Buffer | null = null;
    try {
      requestFrame = await readOneFrame(io.input);
      const parsed = decodeZccCollectionFrame(
        requestFrame,
        ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
        "request",
      );
      if (!validateZccCollectionChildRequest(parsed)) {
        response = childError("INVALID_ZCC_COLLECTION_CHILD_REQUEST");
      } else {
        const request: ZccCollectionChildRequest = Object.freeze({
          kind: parsed.kind,
          schema_version: parsed.schema_version,
          environment: Object.freeze({ ...parsed.environment }),
          resource_type: parsed.resource_type,
        });
        // Best-effort mutable-byte clearing. Parsed JavaScript strings cannot
        // be zeroized, so keep their lifetime scoped to this one child.
        requestFrame.fill(0);
        requestFrame = null;
        const artifact = await (io.collect ?? (async (value) => {
          return collectZccOneApiResourceWithOneApi({
            environment: value.environment,
            resourceType: value.resource_type,
          });
        }))(request);
        response = childSuccess(artifact);
      }
    } catch (error: unknown) {
      response = childError(
        error instanceof ProcessFailure
          ? staticFailure(error)
          : "INVALID_ZCC_COLLECTION_CHILD_REQUEST",
      );
    } finally {
      requestFrame?.fill(0);
    }
  }
  const frame = encodeZccCollectionFrame(
    response as unknown as JsonValue,
    ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
    "response",
  );
  try {
    await io.write(frame);
  } finally {
    frame.fill(0);
  }
}

async function writeFrame(frame: Buffer): Promise<void> {
  const output = createWriteStream("", { fd: 4, autoClose: false });
  await new Promise<void>((resolve, reject) => {
    output.once("error", reject);
    output.end(frame, () => resolve());
  });
}

if (
  process.argv[1] === "-"
  || (
    process.argv[1] !== undefined
    && pathToFileURL(process.argv[1]).href === import.meta.url
  )
) {
  const input = createReadStream("", { fd: 3, autoClose: false });
  try {
    await runZccCollectionChild({ input, write: writeFrame });
    process.exitCode = 0;
  } catch {
    process.exitCode = 1;
  } finally {
    input.destroy();
  }
}
