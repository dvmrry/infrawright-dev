import type {
  RootTopology,
  WholeRootDiagnostic,
} from "../domain/types.js";
import type { ErrorCategory, ErrorDetail } from "../domain/errors.js";

export interface RootsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "roots";
  readonly context: {
    readonly workspace: string;
    readonly deployment: string;
    readonly root_catalog: string;
  };
  readonly input: {
    readonly tenant: string | null;
    readonly selectors: readonly string[];
  };
}

export interface ProcessError {
  readonly code: string;
  readonly category: ErrorCategory;
  readonly message: string;
  readonly retryable: boolean;
  readonly details: readonly ErrorDetail[];
}

export interface ProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "roots";
  readonly status: "ok";
  readonly diagnostics: readonly WholeRootDiagnostic[];
  readonly result: RootTopology;
  readonly error: null;
}

export interface ProcessErrorResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string | null;
  readonly operation: "roots" | null;
  readonly status: "error";
  readonly diagnostics: readonly [];
  readonly result: null;
  readonly error: ProcessError;
}

export type ProcessResponse = ProcessSuccessResponse | ProcessErrorResponse;
