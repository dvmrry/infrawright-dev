import type { ErrorObject } from "ajv/dist/2020.js";

import type { ZccAdoptionArtifactSet } from "../domain/zcc-adoption-artifacts.js";
import type { ZccAdoptionArtifactParity } from "../domain/zcc-adoption-artifact-parity.js";
import type { ZccAdoptionArtifactMaterialization } from "../domain/zcc-adoption-materialization.js";
import { pythonJsonEqual } from "../json/python-equality.js";
import type {
  CompareAdoptionArtifactsProcessRequest,
  CompileAdoptionArtifactsProcessRequest,
  MaterializeAdoptionArtifactsProcessRequest,
} from "../process/types.js";

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

/** Join the comparison request coordinates to its distinct parity result. */
export function zccAdoptionParityOperationResultErrors(
  request: CompareAdoptionArtifactsProcessRequest,
  result: ZccAdoptionArtifactParity,
): readonly ErrorObject[] {
  const errors: ErrorObject[] = [];
  if (
    request.operation !== "compare_adoption_artifacts"
    || request.input.mode !== "bootstrap"
    || request.input.reference !== "materialized"
    || result.kind !== "infrawright.zcc_adoption_artifact_parity"
    || result.mode !== request.input.mode
    || result.reference !== request.input.reference
  ) {
    errors.push(semanticError(
      "/",
      "operation_mode_reference",
      "adoption parity kind, mode, and reference must match the request",
    ));
  }
  if (result.tenant !== request.input.tenant) {
    errors.push(semanticError(
      "/tenant",
      "tenant",
      "adoption parity tenant must match the request",
    ));
  }
  if (result.resource_type !== request.input.resource_type) {
    errors.push(semanticError(
      "/resource_type",
      "resource_type",
      "adoption parity resource must match the request",
    ));
  }
  return errors;
}

/** Join the adoption publication request to its distinct content-free receipt. */
export function zccAdoptionMaterializationOperationResultErrors(
  request: MaterializeAdoptionArtifactsProcessRequest,
  result: ZccAdoptionArtifactMaterialization,
): readonly ErrorObject[] {
  const errors: ErrorObject[] = [];
  if (
    request.operation !== "materialize_adoption_artifacts"
    || request.input.mode !== "bootstrap"
    || request.input.publication !== "create_or_verify_exact"
    || result.kind !== "infrawright.zcc_adoption_artifact_materialization"
    || result.mode !== request.input.mode
    || result.publication.policy !== request.input.publication
  ) {
    errors.push(semanticError(
      "/",
      "operation_mode_publication",
      "adoption materialization kind, mode, and policy must match the request",
    ));
  }
  if (result.tenant !== request.input.tenant) {
    errors.push(semanticError(
      "/tenant",
      "tenant",
      "adoption materialization tenant must match the request",
    ));
  }
  if (result.resource_type !== request.input.resource_type) {
    errors.push(semanticError(
      "/resource_type",
      "resource_type",
      "adoption materialization resource must match the request",
    ));
  }
  if (!pythonJsonEqual(result.verification, request.input.assertion)) {
    errors.push(semanticError(
      "/verification",
      "assertion",
      "adoption materialization verification must equal the retained assertion",
    ));
  }
  return errors;
}
