import path from "node:path";

import {
  schemaErrorDetails,
  validateChangedPathScope,
  validatePlanRoots,
  validateRootTopology,
} from "../contracts/validators.js";
import { loadRootCatalog } from "../domain/catalog.js";
import { loadDeployment } from "../domain/deployment.js";
import { ProcessFailure } from "../domain/errors.js";
import { expandCatalogResources, rootTopology } from "../domain/roots.js";
import { changedPathScope } from "../domain/scope-paths.js";
import { planRoots } from "../domain/plan-roots.js";
import type {
  ProcessRequest,
  ProcessSuccessResponse,
} from "./types.js";

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

export async function executeRequest(
  request: ProcessRequest,
): Promise<ProcessSuccessResponse> {
  if (!path.isAbsolute(request.context.workspace)) {
    throw new ProcessFailure({
      code: "INVALID_WORKSPACE",
      category: "request",
      message: "context.workspace must be an absolute path",
    });
  }
  const catalog = await loadRootCatalog(resolveContextPath(
    request.context.workspace,
    request.context.root_catalog,
  ));
  if (request.operation === "plan_roots" && request.input.selectors.length > 0) {
    expandCatalogResources(catalog, request.input.selectors);
  }
  const deployment = await loadDeployment(resolveContextPath(
    request.context.workspace,
    request.context.deployment,
  ));
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
