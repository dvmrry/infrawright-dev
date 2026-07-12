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
import type { ZccPullRefreshArtifactSet } from "../domain/zcc-pull-refresh.js";
import type { ZccPullArtifactMaterialization } from "../domain/zcc-pull-materialization.js";
import type { ZccPullRefreshMaterialization } from "../domain/zcc-pull-refresh-materialization.js";
import type { ZccPullRefreshAcknowledgement } from "../domain/zcc-pull-refresh-acknowledgement-operation.js";
import type { ZccAdoptionArtifactSet } from "../domain/zcc-adoption-artifacts.js";
import type { ZccAdoptionArtifactParity } from "../domain/zcc-adoption-artifact-parity.js";
import type { ZccAdoptionArtifactMaterialization } from "../domain/zcc-adoption-materialization.js";
import type { ZccPullArtifactParity } from "../domain/zcc-pull-parity.js";
import type {
  ZccPullRefreshParity,
  ZccPullRefreshParitySeed,
} from "../domain/zcc-pull-refresh-parity.js";
import type { ZccPullCollectionReceipt } from "../domain/zcc-pull-collection.js";
import type { ZccPullCollectionParity } from "../domain/zcc-pull-collection-parity.js";

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
    readonly mode: "bootstrap" | "refresh";
    readonly tenant: string;
    readonly resource_type:
      | "zcc_device_cleanup"
      | "zcc_failopen_policy"
      | "zcc_forwarding_profile"
      | "zcc_trusted_network"
      | "zcc_web_privacy";
  };
}

export interface CollectZccPullProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "collect_zcc_pull";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "oneapi";
    readonly publication: "replace_or_verify_exact";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
  };
}

export interface CompareZccPullCollectionProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_zcc_pull_collection";
  readonly context: {
    readonly node_workspace: string;
    readonly python_before_workspace: string;
    readonly python_after_workspace: string;
  };
  readonly input: {
    readonly reference: "python_stability_window";
    readonly tenant: string;
    readonly receipts: readonly ZccPullCollectionReceipt[];
  };
}

export interface CompileAdoptionArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compile_adoption_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
  };
}

export interface CompareAdoptionArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_adoption_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly reference: "materialized";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
  };
}

export interface MaterializeAdoptionArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "materialize_adoption_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly publication: "create_or_verify_exact";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly assertion: ZccAdoptionArtifactParity;
  };
}

export interface CompareBootstrapPullArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_pull_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly reference: "materialized";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
  };
}

export interface CompareRefreshPullArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_pull_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "refresh";
    readonly reference: "materialized_twin";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly reference_context: RootsProcessRequest["context"];
    readonly seed: ZccPullRefreshParitySeed;
  };
}

export type ComparePullArtifactsProcessRequest =
  | CompareBootstrapPullArtifactsProcessRequest
  | CompareRefreshPullArtifactsProcessRequest;

export interface SeedPullRefreshParityProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "seed_pull_refresh_parity";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "refresh";
    readonly reference: "materialized_twin";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly reference_context: RootsProcessRequest["context"];
  };
}

export interface MaterializeBootstrapPullArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "materialize_pull_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "bootstrap";
    readonly publication: "create_or_verify_exact";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly assertion: ZccPullArtifactParity;
  };
}

export interface MaterializeRefreshPullArtifactsProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "materialize_pull_artifacts";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "refresh";
    readonly publication: "replace_or_verify_exact_imports_last";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly assertion: ZccPullRefreshParity;
  };
}

export type MaterializePullArtifactsProcessRequest =
  | MaterializeBootstrapPullArtifactsProcessRequest
  | MaterializeRefreshPullArtifactsProcessRequest;

export interface AcknowledgePullRefreshProcessRequest {
  readonly kind: "infrawright.process_request";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "acknowledge_pull_refresh";
  readonly context: RootsProcessRequest["context"];
  readonly input: {
    readonly mode: "refresh";
    readonly policy: "retire_exact_after_external_acknowledgement";
    readonly tenant: string;
    readonly resource_type: CompilePullArtifactsProcessRequest["input"]["resource_type"];
    readonly assertion: ZccPullRefreshParity;
    readonly publication: ZccPullRefreshMaterialization;
    readonly acknowledgement: {
      readonly kind: "trusted_pipeline_assertion";
      readonly statement: "terraform_apply_succeeded";
    };
  };
}

export type ProcessRequest =
  | RootsProcessRequest
  | ScopePathsProcessRequest
  | PlanRootsProcessRequest
  | AssessSavedPlansProcessRequest
  | CompilePullArtifactsProcessRequest
  | CompileAdoptionArtifactsProcessRequest
  | CompareAdoptionArtifactsProcessRequest
  | MaterializeAdoptionArtifactsProcessRequest
  | SeedPullRefreshParityProcessRequest
  | ComparePullArtifactsProcessRequest
  | MaterializePullArtifactsProcessRequest
  | AcknowledgePullRefreshProcessRequest
  | CollectZccPullProcessRequest
  | CompareZccPullCollectionProcessRequest;

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

export interface CompilePullArtifactsProcessSuccessResponse<
  Result extends ZccPullArtifactSet | ZccPullRefreshArtifactSet = ZccPullArtifactSet,
> {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compile_pull_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: Result;
  readonly error: null;
}

export interface CollectZccPullProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "collect_zcc_pull";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccPullCollectionReceipt;
  readonly error: null;
}

export interface CompareZccPullCollectionProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_zcc_pull_collection";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccPullCollectionParity;
  readonly error: null;
}

export type CompilePullArtifactsRefreshProcessSuccessResponse =
  CompilePullArtifactsProcessSuccessResponse<ZccPullRefreshArtifactSet>;

export interface CompileAdoptionArtifactsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compile_adoption_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccAdoptionArtifactSet;
  readonly error: null;
}

export interface CompareAdoptionArtifactsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_adoption_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccAdoptionArtifactParity;
  readonly error: null;
}

export interface MaterializeAdoptionArtifactsProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "materialize_adoption_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccAdoptionArtifactMaterialization;
  readonly error: null;
}

export interface ComparePullArtifactsProcessSuccessResponse<
  Result extends ZccPullArtifactParity | ZccPullRefreshParity = ZccPullArtifactParity,
> {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "compare_pull_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: Result;
  readonly error: null;
}

export interface SeedPullRefreshParityProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "seed_pull_refresh_parity";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccPullRefreshParitySeed;
  readonly error: null;
}

export interface MaterializePullArtifactsProcessSuccessResponse<
  Result extends ZccPullArtifactMaterialization | ZccPullRefreshMaterialization =
    ZccPullArtifactMaterialization,
> {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "materialize_pull_artifacts";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: Result;
  readonly error: null;
}

export interface AcknowledgePullRefreshProcessSuccessResponse {
  readonly kind: "infrawright.process_response";
  readonly schema_version: 1;
  readonly request_id: string;
  readonly operation: "acknowledge_pull_refresh";
  readonly status: "ok";
  readonly diagnostics: readonly [];
  readonly result: ZccPullRefreshAcknowledgement;
  readonly error: null;
}

export type ProcessSuccessResponse =
  | RootsProcessSuccessResponse
  | ScopePathsProcessSuccessResponse
  | PlanRootsProcessSuccessResponse
  | AssessSavedPlansProcessSuccessResponse
  | CompilePullArtifactsProcessSuccessResponse<
      ZccPullArtifactSet | ZccPullRefreshArtifactSet
    >
  | CompileAdoptionArtifactsProcessSuccessResponse
  | CompareAdoptionArtifactsProcessSuccessResponse
  | MaterializeAdoptionArtifactsProcessSuccessResponse
  | SeedPullRefreshParityProcessSuccessResponse
  | ComparePullArtifactsProcessSuccessResponse<
      ZccPullArtifactParity | ZccPullRefreshParity
    >
  | MaterializePullArtifactsProcessSuccessResponse<
      ZccPullArtifactMaterialization | ZccPullRefreshMaterialization
    >
  | AcknowledgePullRefreshProcessSuccessResponse
  | CollectZccPullProcessSuccessResponse
  | CompareZccPullCollectionProcessSuccessResponse;

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
    | "compile_adoption_artifacts"
    | "compare_adoption_artifacts"
    | "materialize_adoption_artifacts"
    | "seed_pull_refresh_parity"
    | "compare_pull_artifacts"
    | "materialize_pull_artifacts"
    | "acknowledge_pull_refresh"
    | "collect_zcc_pull"
    | "compare_zcc_pull_collection"
    | null;
  readonly status: "error";
  readonly diagnostics: readonly [];
  readonly result: null;
  readonly error: ProcessError;
}

export type ProcessResponse = ProcessSuccessResponse | ProcessErrorResponse;
