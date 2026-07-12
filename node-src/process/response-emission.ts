import {
  pythonCompatibleJsonByteLength,
  type JsonValue,
} from "../json/python-compatible.js";
import { MAX_PROCESS_RESPONSE_BYTES } from "./limits.js";
import type { ProcessErrorResponse, ProcessResponse } from "./types.js";

export function prepareProcessResponseForEmission(
  response: ProcessResponse,
  maximumBytes = MAX_PROCESS_RESPONSE_BYTES,
): { readonly response: ProcessResponse; readonly oversized: boolean } {
  if (
    pythonCompatibleJsonByteLength(
      response as unknown as JsonValue,
      maximumBytes,
    ) <= maximumBytes
  ) {
    return { response, oversized: false };
  }
  const refusal: ProcessErrorResponse = {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: response.request_id,
    operation: response.operation,
    status: "error",
    diagnostics: [],
    result: null,
    error: {
      code: "PROCESS_RESPONSE_TOO_LARGE",
      category: "internal",
      message: "process response exceeds the 32 MiB limit",
      retryable: false,
      details: [],
    },
  };
  return { response: refusal, oversized: true };
}
