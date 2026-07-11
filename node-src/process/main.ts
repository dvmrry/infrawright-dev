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
  ProcessSuccessResponse,
} from "./types.js";

const MAX_REQUEST_BYTES = 1024 * 1024;
const MAX_RESPONSE_BYTES = 32 * 1024 * 1024;

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
  operation:
    | "roots"
    | "scope_paths"
    | "plan_roots"
    | "assess_saved_plans"
    | "compile_pull_artifacts"
    | "compare_pull_artifacts"
    | null;
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
    operation: value.operation === "roots"
      || value.operation === "scope_paths"
      || value.operation === "plan_roots"
      || value.operation === "assess_saved_plans"
      || value.operation === "compile_pull_artifacts"
      || value.operation === "compare_pull_artifacts"
      ? value.operation
      : null,
  };
}

function errorResponse(options: {
  failure: ProcessFailure;
  requestId: string | null;
  operation:
    | "roots"
    | "scope_paths"
    | "plan_roots"
    | "assess_saved_plans"
    | "compile_pull_artifacts"
    | "compare_pull_artifacts"
    | null;
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

function emit(response: ProcessResponse): boolean {
  if (!validateProcessResponse(response)) {
    return emitFallback(
      "INVALID_PROCESS_RESPONSE",
      "process response failed its versioned schema",
    );
  }
  const text = renderPythonCompatibleJson(response as unknown as JsonValue);
  if (Buffer.byteLength(text, "utf8") > MAX_RESPONSE_BYTES) {
    return emitFallback(
      "PROCESS_RESPONSE_TOO_LARGE",
      "process response exceeds the 32 MiB limit",
    );
  }
  process.stdout.write(text);
  return true;
}

function emitFallback(code: string, message: string): false {
  const fallback = errorResponse({
    failure: new ProcessFailure({
      code,
      category: "internal",
      message,
    }),
    requestId: null,
    operation: null,
  });
  process.stdout.write(
    renderPythonCompatibleJson(fallback as unknown as JsonValue),
  );
  process.exitCode = 1;
  return false;
}

function successExitCode(response: ProcessSuccessResponse): number {
  if (
    response.operation === "compile_pull_artifacts"
    || response.operation === "compare_pull_artifacts"
  ) {
    return response.result.status === "review_required" ? 3 : 0;
  }
  if (response.operation !== "assess_saved_plans") {
    return 0;
  }
  if (response.result.summary.status === "error") {
    return 1;
  }
  return response.result.summary.status === "blocked" ? 3 : 0;
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
    const configuredTerraform = process.env.INFRAWRIGHT_TERRAFORM_EXECUTABLE;
    const response = await executeRequest(parsed as ProcessRequest, {
      terraformExecutable: configuredTerraform === undefined
        || configuredTerraform.length === 0
        ? null
        : configuredTerraform,
    });
    if (emit(response)) {
      process.exitCode = successExitCode(response);
    }
  } catch (error: unknown) {
    const identity = requestIdentity(parsed);
    const failure = error instanceof ProcessFailure
      ? error
      : new ProcessFailure({
          code: "INTERNAL_ERROR",
          category: "internal",
          message: "internal process failure",
        });
    const emitted = emit(errorResponse({
      failure,
      requestId: identity.requestId,
      operation: identity.operation,
    }));
    if (emitted) {
      process.exitCode = exitCode(failure);
    }
  }
}

await main();
