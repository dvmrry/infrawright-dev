import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { subscribe, unsubscribe } from "node:diagnostics_channel";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_RESOURCE_TYPES,
  type ZccCollectedArtifact,
  type ZccCollectionResourceType,
} from "../node-src/domain/zcc-collection-contract.js";
import {
  decodeZccCollectionFrame,
  encodeZccCollectionFrame,
  validateZccCollectionChildRequest,
  validateZccCollectionChildResponse,
  ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
  ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
  type ZccCollectionChildRequest,
} from "../node-src/io/zcc-collection-protocol.js";
import { runZccCollectionChild } from "../node-src/process/zcc-collector-child.js";
import type { JsonValue } from "../node-src/json/python-compatible.js";

function request(resourceType: ZccCollectionResourceType): ZccCollectionChildRequest {
  return {
    kind: "infrawright.zcc_collection_child_request",
    schema_version: 1,
    environment: {
      ZSCALER_CLIENT_ID: "client",
      ZSCALER_CLIENT_SECRET: "private-secret",
      ZSCALER_VANITY_DOMAIN: "tenant",
    },
    resource_type: resourceType,
  };
}

function requestFrame(resourceType: ZccCollectionResourceType): Buffer {
  return encodeZccCollectionFrame(
    request(resourceType) as unknown as JsonValue,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  );
}

async function *chunks(...values: readonly Uint8Array[]): AsyncIterable<unknown> {
  for (const value of values) yield value;
}

function requireBuffer(value: Buffer | null): Buffer {
  assert.ok(value !== null);
  return value;
}

function artifact(resourceType: ZccCollectionResourceType): ZccCollectedArtifact {
  const canonical = '[\n  {\n    "id": "one"\n  }\n]\n';
  return {
    canonical_json: canonical,
    metadata: {
      catalog_sources_sha256: ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
      data_requests: 1,
      encoding: "utf-8",
      item_count: 1,
      kind: "infrawright.zcc_collected_pull",
      media_type: "application/json",
      product: "zcc",
      resource_type: resourceType,
      schema_version: 1,
      sha256: createHash("sha256").update(canonical).digest("hex"),
      size_bytes: Buffer.byteLength(canonical),
      transport_attempts: 1,
    },
  };
}

test("directional v1 frames reject every truncation, trailing byte, UTF-8, duplicate, and direction fault", () => {
  const frame = requestFrame("zcc_web_privacy");
  const parsed = decodeZccCollectionFrame(
    frame,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  );
  assert.equal(validateZccCollectionChildRequest(parsed), true);
  for (let length = 0; length < frame.length; length += 1) {
    assert.throws(() => decodeZccCollectionFrame(
      frame.subarray(0, length),
      ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
      "request",
    ));
  }
  assert.throws(() => decodeZccCollectionFrame(
    Buffer.concat([frame, Buffer.from([0])]),
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  ));
  assert.throws(() => decodeZccCollectionFrame(
    frame,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "response",
  ));

  const duplicatePayload = Buffer.from('{"kind":"x","kind":"y"}');
  const duplicate = Buffer.alloc(12 + duplicatePayload.length);
  duplicate.write("IWRQv001", 0, "ascii");
  duplicate.writeUInt32BE(duplicatePayload.length, 8);
  duplicatePayload.copy(duplicate, 12);
  assert.throws(() => decodeZccCollectionFrame(
    duplicate,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  ));
  duplicate[12] = 0xff;
  assert.throws(() => decodeZccCollectionFrame(
    duplicate,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  ));

  const oversized = Buffer.alloc(12);
  oversized.write("IWRQv001", 0, "ascii");
  oversized.writeUInt32BE(ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES + 1, 8);
  assert.throws(() => decodeZccCollectionFrame(
    oversized,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  ));
  frame.fill(0);
});

test("child framing exercises the exact five and emits no secret or counter on closed errors", async () => {
  for (const resourceType of ZCC_COLLECTION_RESOURCE_TYPES) {
    const frame = requestFrame(resourceType);
    let output: Buffer | null = null;
    await runZccCollectionChild({
      input: chunks(frame.subarray(0, 7), frame.subarray(7)),
      collect: async (received) => {
        assert.equal(received.resource_type, resourceType);
        return artifact(resourceType);
      },
      write: async (value) => { output = Buffer.from(value); },
    });
    const captured = requireBuffer(output);
    const response = decodeZccCollectionFrame(
      captured,
      ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
      "response",
    );
    assert.equal(validateZccCollectionChildResponse(response), true);
    assert.equal((response as { status: string }).status, "ok");
    assert.equal(JSON.stringify(response).includes("private-secret"), false);
    frame.fill(0);
    captured.fill(0);
  }

  const frame = requestFrame("zcc_web_privacy");
  let output: Buffer | null = null;
  await runZccCollectionChild({
    input: chunks(frame),
    collect: async () => {
      throw new ProcessFailure({
        code: "UNTRUSTED_FAILURE",
        category: "io",
        message: "private-secret https://secret.invalid",
      });
    },
    write: async (value) => { output = Buffer.from(value); },
  });
  const captured = requireBuffer(output);
  const response = decodeZccCollectionFrame(
    captured,
    ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
    "response",
  );
  assert.deepEqual(response, { code: "ZCC_ONEAPI_HOST_FAILED" });
  assert.equal(JSON.stringify(response).includes("private-secret"), false);
  frame.fill(0);
  captured.fill(0);
});

test("diagnostics refusal occurs before fd3 is read", async () => {
  let reads = 0;
  let output: Buffer | null = null;
  const listener = (): void => undefined;
  subscribe("net.client.socket", listener);
  try {
    await runZccCollectionChild({
      input: {
        async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
          reads += 1;
          yield requestFrame("zcc_web_privacy");
        },
      },
      collect: async () => artifact("zcc_web_privacy"),
      write: async (value) => { output = Buffer.from(value); },
    });
  } finally {
    unsubscribe("net.client.socket", listener);
  }
  assert.equal(reads, 0);
  const captured = requireBuffer(output);
  assert.deepEqual(decodeZccCollectionFrame(
    captured,
    ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
    "response",
  ), { code: "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE" });
  captured.fill(0);
});

test("a sixth resource is rejected before collection", async () => {
  const invalid = {
    ...request("zcc_web_privacy"),
    resource_type: "zcc_sixth_resource",
  };
  const frame = encodeZccCollectionFrame(
    invalid as unknown as JsonValue,
    ZCC_COLLECTION_CHILD_REQUEST_LIMIT_BYTES,
    "request",
  );
  let calls = 0;
  let output: Buffer | null = null;
  await runZccCollectionChild({
    input: chunks(frame),
    collect: async () => {
      calls += 1;
      return artifact("zcc_web_privacy");
    },
    write: async (value) => { output = Buffer.from(value); },
  });
  assert.equal(calls, 0);
  const captured = requireBuffer(output);
  assert.deepEqual(decodeZccCollectionFrame(
    captured,
    ZCC_COLLECTION_CHILD_RESPONSE_LIMIT_BYTES,
    "response",
  ), { code: "INVALID_ZCC_COLLECTION_CHILD_REQUEST" });
  frame.fill(0);
  captured.fill(0);
});
