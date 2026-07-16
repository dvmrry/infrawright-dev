import {
  Ajv2020,
  type ErrorObject,
  type ValidateFunction,
} from "ajv/dist/2020.js";

import changedPathScopeSchema from "../../docs/schemas/changed-path-scope.schema.json" with { type: "json" };
import planRootsSchema from "../../docs/schemas/plan-roots.schema.json" with { type: "json" };
import rootCatalogSchema from "../../docs/schemas/root-catalog.schema.json" with { type: "json" };
import rootTopologySchema from "../../docs/schemas/root-topology.schema.json" with { type: "json" };
import savedPlanAssessmentSchema from "../../docs/schemas/saved-plan-assessment.schema.json" with { type: "json" };
import type { ErrorDetail } from "../domain/errors.js";
import {
  ASSESSMENT_SEMANTICS_KEYWORD,
  validateAssessmentSemantics,
} from "./saved-plan-assessment-semantics.js";

const ajv = new Ajv2020({
  allErrors: true,
  coerceTypes: false,
  ownProperties: true,
  removeAdditional: false,
  strict: true,
  useDefaults: false,
});

ajv.addKeyword({
  keyword: ASSESSMENT_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateAssessmentSemantics,
});

for (const schema of [
  rootTopologySchema,
  changedPathScopeSchema,
  planRootsSchema,
  savedPlanAssessmentSchema,
]) {
  ajv.addSchema(schema);
}

function requiredSchema(id: string): ValidateFunction {
  const validator = ajv.getSchema(id);
  if (validator === undefined) {
    throw new Error(`required JSON schema ${JSON.stringify(id)} is not registered`);
  }
  return validator;
}

export const validateRootCatalog: ValidateFunction = ajv.compile(
  rootCatalogSchema,
);
export const validateRootTopology = requiredSchema(rootTopologySchema.$id);
export const validateChangedPathScope = requiredSchema(changedPathScopeSchema.$id);
export const validatePlanRoots = requiredSchema(planRootsSchema.$id);
export const validateSavedPlanAssessment = requiredSchema(
  savedPlanAssessmentSchema.$id,
);

function errorMessage(error: ErrorObject): string {
  if (error.keyword === "additionalProperties") {
    return "additional property is not allowed";
  }
  return error.message ?? "schema validation failed";
}

export function schemaErrorDetails(
  errors: readonly ErrorObject[] | null | undefined,
): ErrorDetail[] {
  const allErrors = errors ?? [];
  const details = allErrors.slice(0, 64).map((error) => ({
    path: error.instancePath || "/",
    code: error.keyword,
    message: errorMessage(error),
  }));
  if (allErrors.length > details.length) {
    details.push({
      path: "/",
      code: "schema_errors_truncated",
      message: "additional schema errors were omitted",
    });
  }
  return details;
}
