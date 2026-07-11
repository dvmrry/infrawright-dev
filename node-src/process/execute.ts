import path from "node:path";

import {
  schemaErrorDetails,
  validateRootTopology,
} from "../contracts/validators.js";
import { loadRootCatalog } from "../domain/catalog.js";
import { loadDeployment } from "../domain/deployment.js";
import { ProcessFailure } from "../domain/errors.js";
import { rootTopology } from "../domain/roots.js";
import type {
  ProcessSuccessResponse,
  RootsProcessRequest,
} from "./types.js";

function resolveContextPath(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate)
    ? candidate
    : path.resolve(workspace, candidate);
}

export async function executeRequest(
  request: RootsProcessRequest,
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
  const deployment = await loadDeployment(resolveContextPath(
    request.context.workspace,
    request.context.deployment,
  ));
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
