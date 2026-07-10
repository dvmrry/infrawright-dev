import type {
  ChangedPathScope,
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

export interface ScopePathsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "scope_paths";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly paths: readonly string[];
  };
}

export type ProcessRequest = RootsProcessRequest | ScopePathsProcessRequest;

export interface ProcessError {
  readonly code: string;
  readonly category: ErrorCategory;
  readonly message: string;
  readonly retryable: boolean;
  readonly details: readonly ErrorDetail[];
}

export interface RootsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "roots";
  readonly status: "ok";
  readonly diagnostics: readonly WholeRootDiagnostic[];
  readonly result: RootTopology;
  readonly error: null;
}

export interface ScopePathsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "scope_paths";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ChangedPathScope;
  readonly error: null;
}

export type ProcessSuccessResponse =
  | RootsProcessSuccessResponse
  | ScopePathsProcessSuccessResponse;

export interface ProcessErrorResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string | null;
  readonly operation: "roots" | "scope_paths" | null;
  readonly status: "error";
  readonly diagnostics: readonly [];
  readonly result: null;
  readonly error: ProcessError;
}

export type ProcessResponse = ProcessSuccessResponse | ProcessErrorResponse;
