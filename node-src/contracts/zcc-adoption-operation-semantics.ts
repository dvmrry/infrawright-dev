import type { ErrorObject } from "ajv/dist/2020.js";

import type { ZccAdoptionArtifactSet } from "../domain/zcc-adoption-artifacts.js";
import type { CompileAdoptionArtifactsProcessRequest } from "../process/types.js";

const KEYWORD = "x-infrawright-zcc-adoption-operation-result-semantics";

function semanticError(path: string, rule: string, message: string): ErrorObject {
  return {
    instancePath: path,
    schemaPath: `#/${KEYWORD}`,
    keyword: KEYWORD,
    params: { rule },
    message,
  };
}

/** Join the public request coordinates to the provider-observed result. */
export function zccAdoptionOperationResultErrors(
  request: CompileAdoptionArtifactsProcessRequest,
  result: ZccAdoptionArtifactSet,
): readonly ErrorObject[] {
  const errors: ErrorObject[] = [];
  if (
    request.operation !== "compile_adoption_artifacts"
    || request.input.mode !== "bootstrap"
    || result.kind !== "infrawright.zcc_adoption_artifact_set"
    || result.mode !== request.input.mode
  ) {
    errors.push(semanticError(
      "/",
      "operation_mode",
      "adoption result kind and mode must match the requested operation",
    ));
  }
  if (result.tenant !== request.input.tenant) {
    errors.push(semanticError(
      "/tenant",
      "tenant",
      "adoption result tenant must match the request",
    ));
  }
  if (result.resource_type !== request.input.resource_type) {
    errors.push(semanticError(
      "/resource_type",
      "resource_type",
      "adoption result resource must match the request",
    ));
  }
  return errors;
}
