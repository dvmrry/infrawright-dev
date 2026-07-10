import {
  schemaErrorDetails,
  validateProcessRequest,
  validateProcessResponse,
} from "../contracts/validators.js";
import { ProcessFailure } from "../domain/errors.js";
import { parseControlJson } from "../json/control.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";
import { executeRequest } from "./execute.js";
import type {
  ProcessErrorResponse,
  ProcessRequest,
  ProcessResponse,
} from "./types.js";

const MAX_REQUEST_BYTES = 1024 * 1024;

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

async function readRequest(): Promise<string> {
  const chunks: Buffer[] = [];
  let length = 0;
  for await (const rawChunk of process.stdin) {
    const chunk = Buffer.isBuffer(rawChunk)
      ? rawChunk
      : Buffer.from(rawChunk as Uint8Array);
    length += chunk.length;
    if (length > MAX_REQUEST_BYTES) {
      throw new ProcessFailure({
        code: "REQUEST_TOO_LARGE",
        category: "request",
        message: "request exceeds the 1 MiB limit",
      });
    }
    chunks.push(chunk);
  }
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(Buffer.concat(chunks));
  } catch {
    throw new ProcessFailure({
      code: "INVALID_UTF8",
      category: "request",
      message: "request is not valid UTF-8",
    });
  }
}

function requestIdentity(value: unknown): {
  requestId: string | null;
  operation: "roots" | "scope_paths" | null;
} {
  if (!isObject(value)) {
    return { requestId: null, operation: null };
  }
  const requestId = Object.hasOwn(value, "request_id")
    && typeof value.request_id === "string"
    && value.request_id.length <= 256
    ? value.request_id
    : null;
  return {
    requestId,
    operation: value.operation === "roots" || value.operation === "scope_paths"
      ? value.operation
      : null,
  };
}

function errorResponse(options: {
  failure: ProcessFailure;
  requestId: string | null;
  operation: "roots" | "scope_paths" | null;
}): ProcessErrorResponse {
  return {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: options.requestId,
    operation: options.operation,
    status: "error",
    diagnostics: [],
    result: null,
    error: {
      code: options.failure.code,
      category: options.failure.category,
      message: options.failure.message,
      retryable: options.failure.retryable,
      details: options.failure.details,
    },
  };
}

function exitCode(failure: ProcessFailure): number {
  return failure.category === "request" || failure.category === "domain" ? 2 : 1;
}

function emit(response: ProcessResponse): void {
  if (!validateProcessResponse(response)) {
    const fallback = errorResponse({
      failure: new ProcessFailure({
        code: "INVALID_PROCESS_RESPONSE",
        category: "internal",
        message: "process response failed its versioned schema",
      }),
      requestId: null,
      operation: null,
    });
    process.stdout.write(
      renderPythonCompatibleJson(fallback as unknown as JsonValue),
    );
    process.exitCode = 1;
    return;
  }
  process.stdout.write(
    renderPythonCompatibleJson(response as unknown as JsonValue),
  );
}

async function main(): Promise<void> {
  let parsed: unknown = null;
  try {
    const text = await readRequest();
    try {
      parsed = parseControlJson(text);
    } catch {
      throw new ProcessFailure({
        code: "INVALID_JSON",
        category: "request",
        message: "request is not valid JSON",
      });
    }
    if (!validateProcessRequest(parsed)) {
      throw new ProcessFailure({
        code: "INVALID_REQUEST",
        category: "request",
        message: "request does not match schema version 1",
        details: schemaErrorDetails(validateProcessRequest.errors),
      });
    }
    emit(await executeRequest(parsed as ProcessRequest));
  } catch (error: unknown) {
    const identity = requestIdentity(parsed);
    const failure = error instanceof ProcessFailure
      ? error
      : new ProcessFailure({
          code: "INTERNAL_ERROR",
          category: "internal",
          message: "internal process failure",
        });
    emit(errorResponse({
      failure,
      requestId: identity.requestId,
      operation: identity.operation,
    }));
    process.exitCode = exitCode(failure);
  }
}

await main();
