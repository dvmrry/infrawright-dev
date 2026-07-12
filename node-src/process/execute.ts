import path from "node:path";

import {
  schemaErrorDetails,
  validateChangedPathScope,
  validatePlanRoots,
  validateRootTopology,
  validateSavedPlanAssessment,
  validateZccAdoptionArtifactParity,
  validateZccAdoptionArtifactSet,
  validateZccPullArtifactMaterialization,
  validateZccPullArtifactSet,
  validateZccPullRefreshArtifactSet,
  validateZccPullRefreshParity,
  validateZccPullRefreshParitySeed,
  validateZccPullArtifactParity,
  validateZccPullRefreshAcknowledgement,
  validateZccPullRefreshMaterialization,
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
import {
  compareZccPullArtifactsOperation,
  compileZccPullArtifactsOperation,
  compileZccPullRefreshArtifactsOperation,
  materializeZccPullArtifactsOperation,
} from "../domain/zcc-pull-operation.js";
import {
  compareZccPullRefreshParityOperation,
  seedZccPullRefreshParityOperation,
} from "../domain/zcc-pull-refresh-parity.js";
import { materializeZccPullRefreshOperation } from "../domain/zcc-pull-refresh-publisher-operation.js";
import {
  acknowledgeZccPullRefreshOperation,
} from "../domain/zcc-pull-refresh-acknowledgement-operation.js";
import { zccPullRefreshPublicationReceiptSha } from "../domain/zcc-pull-refresh-fingerprints.js";
import {
  compareZccAdoptionArtifactsOperation,
  compileZccAdoptionArtifactsOperation,
} from "../domain/zcc-adoption-operation.js";
import type { ZccAdoptionOracleHostAuthority } from "../domain/zcc-adoption-operation.js";
import {
  zccAdoptionOperationResultErrors,
  zccAdoptionParityOperationResultErrors,
} from "../contracts/zcc-adoption-operation-semantics.js";
import { withPublisherGuard } from "../io/publisher-guard.js";
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
  if (request.operation === "compile_adoption_artifacts") {
    return {
      ...base,
      operation: "compile_adoption_artifacts",
      input: {
        mode: "bootstrap",
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
      },
    };
  }
  if (request.operation === "compare_adoption_artifacts") {
    return {
      ...base,
      operation: "compare_adoption_artifacts",
      input: {
        mode: "bootstrap",
        reference: "materialized",
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
      },
    };
  }
  if (request.operation === "compare_pull_artifacts") {
    if (request.input.mode === "refresh") {
      return {
        ...base,
        operation: "compare_pull_artifacts",
        input: {
          mode: "refresh",
          reference: "materialized_twin",
          tenant: request.input.tenant,
          resource_type: request.input.resource_type,
          reference_context: { ...request.input.reference_context },
          seed: structuredClone(request.input.seed),
        },
      };
    }
    return {
      ...base,
      operation: "compare_pull_artifacts",
      input: {
        mode: "bootstrap",
        reference: "materialized",
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
      },
    };
  }
  if (request.operation === "seed_pull_refresh_parity") {
    return {
      ...base,
      operation: "seed_pull_refresh_parity",
      input: {
        mode: request.input.mode,
        reference: request.input.reference,
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
        reference_context: { ...request.input.reference_context },
      },
    };
  }
  if (request.operation === "materialize_pull_artifacts") {
    if (request.input.mode === "refresh") {
      return {
        ...base,
        operation: "materialize_pull_artifacts",
        input: {
          mode: "refresh",
          publication: "replace_or_verify_exact_imports_last",
          tenant: request.input.tenant,
          resource_type: request.input.resource_type,
          assertion: structuredClone(request.input.assertion),
        },
      };
    }
    return {
      ...base,
      operation: "materialize_pull_artifacts",
      input: {
        mode: "bootstrap",
        publication: "create_or_verify_exact",
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
        assertion: structuredClone(request.input.assertion),
      },
    };
  }
  if (request.operation === "acknowledge_pull_refresh") {
    return {
      ...base,
      operation: "acknowledge_pull_refresh",
      input: {
        mode: request.input.mode,
        policy: request.input.policy,
        tenant: request.input.tenant,
        resource_type: request.input.resource_type,
        assertion: structuredClone(request.input.assertion),
        publication: structuredClone(request.input.publication),
        acknowledgement: {
          kind: request.input.acknowledgement.kind,
          statement: request.input.acknowledgement.statement,
        },
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
  dependencies: {
    readonly terraformExecutable: string | null;
    readonly materializeOutputRoot: string | null;
    readonly allowExternalApplyAcknowledgement?: boolean;
    readonly zccAdoptionOracle?: ZccAdoptionOracleHostAuthority | null;
  },
): Promise<ProcessSuccessResponse> {
  request = copyRequest(request);
  const terraformExecutable = dependencies.terraformExecutable;
  const zccAdoptionOracle = dependencies.zccAdoptionOracle === undefined
      || dependencies.zccAdoptionOracle === null
    ? null
    : Object.freeze({
        terraformExecutable: dependencies.zccAdoptionOracle.terraformExecutable,
        tempRoot: dependencies.zccAdoptionOracle.tempRoot,
        environment: Object.freeze({
          ...dependencies.zccAdoptionOracle.environment,
        }),
      });
  if (
    [
      request.context.workspace,
      request.context.deployment,
      request.context.root_catalog,
    ].some((candidate) => candidate.includes("\0") || !candidate.isWellFormed())
  ) {
    throw new ProcessFailure({
      code: "INVALID_CONTEXT_PATH",
      category: "request",
      message: "process context paths must contain supported Unicode",
    });
  }
  if (!path.isAbsolute(request.context.workspace)) {
    throw new ProcessFailure({
      code: "INVALID_WORKSPACE",
      category: "request",
      message: "context.workspace must be an absolute path",
    });
  }
  if (request.operation === "compile_pull_artifacts") {
    const options = {
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
    };
    const result = request.input.mode === "refresh"
      ? await compileZccPullRefreshArtifactsOperation(options)
      : await compileZccPullArtifactsOperation(options);
    const validateResult = request.input.mode === "refresh"
      ? validateZccPullRefreshArtifactSet
      : validateZccPullArtifactSet;
    if (!validateResult(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "compile_pull_artifacts produced a result outside its versioned schema",
        details: schemaErrorDetails(validateResult.errors),
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
  if (request.operation === "compile_adoption_artifacts") {
    const result = await compileZccAdoptionArtifactsOperation({
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
      hostAuthority: zccAdoptionOracle,
    });
    const bindingErrors = zccAdoptionOperationResultErrors(request, result);
    if (!validateZccAdoptionArtifactSet(result) || bindingErrors.length > 0) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "compile_adoption_artifacts produced a result outside its versioned contract",
        details: [
          ...schemaErrorDetails(validateZccAdoptionArtifactSet.errors),
          ...schemaErrorDetails(bindingErrors),
        ],
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "compile_adoption_artifacts",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "compare_adoption_artifacts") {
    const result = await compareZccAdoptionArtifactsOperation({
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
      hostAuthority: zccAdoptionOracle,
    });
    const bindingErrors = zccAdoptionParityOperationResultErrors(request, result);
    if (!validateZccAdoptionArtifactParity(result) || bindingErrors.length > 0) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "compare_adoption_artifacts produced a result outside its versioned contract",
        details: [
          ...schemaErrorDetails(validateZccAdoptionArtifactParity.errors),
          ...schemaErrorDetails(bindingErrors),
        ],
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "compare_adoption_artifacts",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "compare_pull_artifacts") {
    const result = request.input.mode === "refresh"
      ? await compareZccPullRefreshParityOperation({
          context: request.context,
          referenceContext: request.input.reference_context,
          tenant: request.input.tenant,
          resourceType: request.input.resource_type,
          seed: request.input.seed,
        })
      : await compareZccPullArtifactsOperation({
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
    const validateResult = request.input.mode === "refresh"
      ? validateZccPullRefreshParity
      : validateZccPullArtifactParity;
    if (!validateResult(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "compare_pull_artifacts produced a result outside its versioned schema",
        details: schemaErrorDetails(validateResult.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "compare_pull_artifacts",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "seed_pull_refresh_parity") {
    const result = await seedZccPullRefreshParityOperation({
      context: request.context,
      referenceContext: request.input.reference_context,
      tenant: request.input.tenant,
      resourceType: request.input.resource_type,
    });
    if (!validateZccPullRefreshParitySeed(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "seed_pull_refresh_parity produced a result outside its versioned schema",
        details: schemaErrorDetails(validateZccPullRefreshParitySeed.errors),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "seed_pull_refresh_parity",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "materialize_pull_artifacts") {
    if (dependencies.materializeOutputRoot === null) {
      throw new ProcessFailure({
        code: "MATERIALIZE_OUTPUT_ROOT_NOT_CONFIGURED",
        category: "io",
        message: "artifact materialization requires a trusted output root",
      });
    }
    const outputRoot = dependencies.materializeOutputRoot;
    const result = await withPublisherGuard(outputRoot, async () => {
      return request.input.mode === "refresh"
        ? materializeZccPullRefreshOperation({
            context: request.context,
            tenant: request.input.tenant,
            resourceType: request.input.resource_type,
            assertion: request.input.assertion,
            outputRoot,
          })
        : materializeZccPullArtifactsOperation({
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
            assertion: request.input.assertion,
            outputRoot,
          });
    });
    const validateResult = request.input.mode === "refresh"
      ? validateZccPullRefreshMaterialization
      : validateZccPullArtifactMaterialization;
    if (!validateResult(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "materialize_pull_artifacts produced a result outside its versioned schema",
        details: schemaErrorDetails(
          validateResult.errors,
        ),
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "materialize_pull_artifacts",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
  }
  if (request.operation === "acknowledge_pull_refresh") {
    const allowExternalApplyAcknowledgement =
      dependencies.allowExternalApplyAcknowledgement === true;
    if (!allowExternalApplyAcknowledgement) {
      throw new ProcessFailure({
        code: "EXTERNAL_APPLY_ACKNOWLEDGEMENT_REQUIRED",
        category: "domain",
        message: "refresh retirement requires the trusted external-apply acknowledgement capability",
      });
    }
    if (dependencies.materializeOutputRoot === null) {
      throw new ProcessFailure({
        code: "REFRESH_ACKNOWLEDGEMENT_OUTPUT_ROOT_NOT_CONFIGURED",
        category: "io",
        message: "refresh acknowledgement requires a trusted output root",
      });
    }
    const outputRoot = dependencies.materializeOutputRoot;
    const result = await withPublisherGuard(outputRoot, async () => {
      return acknowledgeZccPullRefreshOperation({
        context: request.context,
        tenant: request.input.tenant,
        resourceType: request.input.resource_type,
        assertion: request.input.assertion,
        publication: request.input.publication,
        acknowledgement: request.input.acknowledgement,
        policy: request.input.policy,
        outputRoot,
        allowExternalApplyAcknowledgement,
      });
    });
    if (!validateZccPullRefreshAcknowledgement(result)) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "acknowledge_pull_refresh produced a result outside its versioned schema",
        details: schemaErrorDetails(validateZccPullRefreshAcknowledgement.errors),
      });
    }
    const expectedReceiptSha = zccPullRefreshPublicationReceiptSha(
      request.input.publication,
    );
    if (result.verification.publication_receipt_sha256 !== expectedReceiptSha) {
      throw new ProcessFailure({
        code: "INVALID_OPERATION_RESULT",
        category: "internal",
        message: "acknowledge_pull_refresh did not bind the publication receipt",
      });
    }
    return {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: request.request_id,
      operation: "acknowledge_pull_refresh",
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
