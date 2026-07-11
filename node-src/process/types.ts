import type {
  ChangedPathScope,
  PlanRoots,
  RootTopology,
  WholeRootDiagnostic,
} from "../domain/types.js";
import type { ErrorCategory, ErrorDetail } from "../domain/errors.js";
import type {
  AssessmentMode,
  SavedPlanAssessmentReport,
} from "../domain/plan-report.js";
import type { ZccPullArtifactSet } from "../domain/zcc-pull-artifacts.js";

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

export interface PlanRootsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "plan_roots";
  readonly context: RootsProcessRequest["context"];
  readonly input: RootsProcessRequest["input"];
}

export interface AssessSavedPlansProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "assess_saved_plans";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: AssessmentMode;
    readonly tenant: string | null;
    readonly selectors: readonly string[];
    readonly backend_config: string | null;
    readonly policy: string | null;
  };
}

export interface CompilePullArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compile_pull_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly tenant: string;
    readonly resource_type:
      | "zcc_device_cleanup"
      | "zcc_failopen_policy"
      | "zcc_forwarding_profile"
      | "zcc_trusted_network"
      | "zcc_web_privacy";
  };
}

export type ProcessRequest =
  | RootsProcessRequest
  | ScopePathsProcessRequest
  | PlanRootsProcessRequest
  | AssessSavedPlansProcessRequest
  | CompilePullArtifactsProcessRequest;

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

export interface PlanRootsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "plan_roots";
  readonly status: "ok";
  readonly diagnostics: readonly WholeRootDiagnostic[];
  readonly result: PlanRoots;
  readonly error: null;
}

export interface AssessSavedPlansProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "assess_saved_plans";
  readonly status: "ok";
  readonly diagnostics: readonly WholeRootDiagnostic[];
  readonly result: SavedPlanAssessmentReport;
  readonly error: null;
}

export interface CompilePullArtifactsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compile_pull_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccPullArtifactSet;
  readonly error: null;
}

export type ProcessSuccessResponse =
  | RootsProcessSuccessResponse
  | ScopePathsProcessSuccessResponse
  | PlanRootsProcessSuccessResponse
  | AssessSavedPlansProcessSuccessResponse
  | CompilePullArtifactsProcessSuccessResponse;

export interface ProcessErrorResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string | null;
  readonly operation:
    | "roots"
    | "scope_paths"
    | "plan_roots"
    | "assess_saved_plans"
    | "compile_pull_artifacts"
    | null;
  readonly status: "error";
  readonly diagnostics: readonly [];
  readonly result: null;
  readonly error: ProcessError;
}

export type ProcessResponse = ProcessSuccessResponse | ProcessErrorResponse;
