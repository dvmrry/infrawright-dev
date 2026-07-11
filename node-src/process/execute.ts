import path from "node:path";

import {
  schemaErrorDetails,
  validateChangedPathScope,
  validatePlanRoots,
  validateRootTopology,
  validateSavedPlanAssessment,
  validateZccPullArtifactSet,
} from "../contracts/validators.js";
import {
  loadBoundAssessmentRootCatalog,
  loadRootCatalog,
} from "../domain/catalog.js";
import {
  loadBoundAssessmentDeployment,
  loadDeployment,
} from "../domain/deployment.js";
import type { BoundAssessmentControlFile } from "../domain/control-evidence.js";
import { ProcessFailure } from "../domain/errors.js";
import { expandCatalogResources, rootTopology } from "../domain/roots.js";
import { changedPathScope } from "../domain/scope-paths.js";
import { planRoots } from "../domain/plan-roots.js";
import { resolveSavedPlanAssessment } from "../domain/plan-assessment-inputs.js";
import { assessSavedPlansReport } from "../domain/plan-assessment.js";
import { requireSupportedAssessmentCatalog } from "../domain/zscaler-assessment.js";
import { compileZccPullArtifactsOperation } from "../domain/zcc-pull-operation.js";
import type {
  ProcessRequest,
  ProcessSuccessResponse,
} from "./types.js";

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

function copyRequest(request: ProcessRequest): ProcessRequest {
  const base = {
    kind: request.kind,
    schema_version: request.schema_version,
    request_id: request.request_id,
    context: {
      workspace: request.context.workspace,
      deployment: request.context.deployment,
      root_catalog: request.context.root_catalog,
    },
  } as const;
  if (request.operation === "scope_paths") {
    return {
      ...base,
      operation: "scope_paths",
      input: { paths: [...request.input.paths] },
    };
  }
  if (request.operation === "assess_saved_plans") {
    return {
      ...base,
      operation: "assess_saved_plans",
      input: {
        mode: request.input.mode,
        tenant: request.input.tenant,
        selectors: [...request.input.selectors],
        backend_config: request.input.backend_config,
        policy: request.input.policy,
      },
    };
  }
  if (request.operation === "compile_pull_artifacts") {
    return {
      ...base,
      operation: "compile_pull_artifacts",
      input: {
        mode: request.input.mode,
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
      },
    };
  }
  return {
    ...base,
    operation: request.operation,
    input: {
      tenant: request.input.tenant,
      selectors: [...request.input.selectors],
    },
  };
}

export async function executeRequest(
  request: ProcessRequest,
  dependencies: { readonly terraformExecutable: string | null },
): Promise<ProcessSuccessResponse> {
  request = copyRequest(request);
  const terraformExecutable = dependencies.terraformExecutable;
  if (!path.isAbsolute(request.context.workspace)) {
    throw new ProcessFailure({
      code: "INVALID_WORKSPACE",
      category: "request",
      message: "context.workspace must be an absolute path",
    });
  }
  if (request.operation === "compile_pull_artifacts") {
    const result = await compileZccPullArtifactsOperation({
      workspace: request.context.workspace,
      deploymentPath: resolveContextPath(
        request.context.workspace,
        request.context.deployment,
      ),
      catalogPath: resolveContextPath(
        request.context.workspace,
        request.context.root_catalog,
      ),
      tenant: request.input.tenant,
      resourceType: request.input.resource_type,
    });
    if (!validateZccPullArtifactSet(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "compile_pull_artifacts produced a result outside its versioned schema",
        details: schemaErrorDetails(validateZccPullArtifactSet.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "compile_pull_artifacts",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  const catalogPath = resolveContextPath(
    request.context.workspace,
    request.context.root_catalog,
  );
  const boundCatalog = request.operation === "assess_saved_plans"
    ? await loadBoundAssessmentRootCatalog(catalogPath)
    : null;
  const catalog = boundCatalog === null
    ? await loadRootCatalog(catalogPath)
    : boundCatalog.catalog;
  const catalogControl: BoundAssessmentControlFile | null =
    boundCatalog?.file ?? null;
  if (
    (request.operation === "plan_roots"
      || request.operation === "assess_saved_plans")
    && request.input.selectors.length > 0
  ) {
    expandCatalogResources(catalog, request.input.selectors);
  }
  if (request.operation === "assess_saved_plans") {
    requireSupportedAssessmentCatalog(catalog);
    if (terraformExecutable === null) {
      throw new ProcessFailure({
        code: "TERRAFORM_NOT_CONFIGURED",
        category: "io",
        message: "saved-plan assessment requires trusted Terraform configuration",
      });
    }
  }
  const deploymentPath = resolveContextPath(
    request.context.workspace,
    request.context.deployment,
  );
  const boundDeployment = request.operation === "assess_saved_plans"
    ? await loadBoundAssessmentDeployment(deploymentPath)
    : null;
  const deployment = boundDeployment === null
    ? await loadDeployment(deploymentPath)
    : boundDeployment.deployment;
  const deploymentControl: BoundAssessmentControlFile | null =
    boundDeployment?.file ?? null;
  if (request.operation === "assess_saved_plans") {
    const trustedTerraform = terraformExecutable;
    if (trustedTerraform === null) {
      throw new ProcessFailure({
        code: "TERRAFORM_NOT_CONFIGURED",
        category: "io",
        message: "saved-plan assessment requires trusted Terraform configuration",
      });
    }
    if (catalogControl === null || deploymentControl === null) {
      throw new ProcessFailure({
        code: "MISSING_ASSESSMENT_CONTROL_BINDING",
        category: "internal",
        message: "saved-plan assessment control inputs were not bound",
      });
    }
    const resolved = await resolveSavedPlanAssessment({
      workspace: request.context.workspace,
      deployment,
      catalog,
      tenant: request.input.tenant,
      selectors: request.input.selectors,
      terraformExecutable: trustedTerraform,
      backendConfig: request.input.backend_config,
      policyPath: request.input.policy,
      controlFiles: [catalogControl, deploymentControl],
    });
    const outcome = await assessSavedPlansReport({
      assessment: resolved.assessment,
      mode: request.input.mode,
      request: {
        tenant: request.input.tenant,
        selectors: request.input.selectors,
        policy: request.input.policy,
      },
    });
    if (!validateSavedPlanAssessment(outcome.report)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "assess_saved_plans produced a result outside its versioned schema",
        details: schemaErrorDetails(validateSavedPlanAssessment.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "assess_saved_plans",
      status: "ok",
      diagnostics: resolved.diagnostics,
      result: outcome.report,
      error: null,
    };
  }
  if (request.operation === "scope_paths") {
    const result = changedPathScope({
      paths: request.input.paths,
      workspace: request.context.workspace,
      deploymentPath: resolveContextPath(
        request.context.workspace,
        request.context.deployment,
      ),
      deployment,
      catalog,
    });
    if (!validateChangedPathScope(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "scope_paths produced a result outside its versioned schema",
        details: schemaErrorDetails(validateChangedPathScope.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "scope_paths",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "plan_roots") {
    const { result, diagnostics } = await planRoots({
      workspace: request.context.workspace,
      deployment,
      catalog,
      tenant: request.input.tenant,
      selectors: request.input.selectors,
    });
    if (!validatePlanRoots(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "plan_roots produced a result outside its versioned schema",
        details: schemaErrorDetails(validatePlanRoots.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "plan_roots",
      status: "ok",
      diagnostics,
      result,
      error: null,
    };
  }
  const { topology, diagnostics } = rootTopology({
    catalog,
    deployment,
    tenant: request.input.tenant,
    selectors: request.input.selectors,
  });
  if (!validateRootTopology(topology)) {
    throw new ProcessFailure({
      code: "INVALID_OPERATION_RESULT",
      category: "internal",
      message: "roots produced a result outside its versioned schema",
      details: schemaErrorDetails(validateRootTopology.errors),
    });
  }
  return {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: request.request_id,
    operation: "roots",
    status: "ok",
    diagnostics,
    result: topology,
    error: null,
  };
}
