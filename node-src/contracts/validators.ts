import { Ajv2020, type ErrorObject, type ValidateFunction } from "ajv/dist/2020.js";

import processRequestSchema from "../../docs/schemas/process-request.schema.json" with { type: "json" };
import processResponseSchema from "../../docs/schemas/process-response.schema.json" with { type: "json" };
import changedPathScopeSchema from "../../docs/schemas/changed-path-scope.schema.json" with { type: "json" };
import rootCatalogSchema from "../../docs/schemas/root-catalog.schema.json" with { type: "json" };
import rootTopologySchema from "../../docs/schemas/root-topology.schema.json" with { type: "json" };
import type { ErrorDetail } from "../domain/errors.js";

const ajv = new Ajv2020({
  allErrors: true,
  coerceTypes: false,
  ownProperties: true,
  removeAdditional: false,
  strict: true,
  useDefaults: false,
});

ajv.addSchema(rootTopologySchema);
ajv.addSchema(changedPathScopeSchema);

export const validateProcessRequest: ValidateFunction = ajv.compile(
  processRequestSchema,
);
export const validateProcessResponse: ValidateFunction = ajv.compile(
  processResponseSchema,
);
export const validateRootCatalog: ValidateFunction = ajv.compile(
  rootCatalogSchema,
);
export const validateRootTopology: ValidateFunction = ajv.getSchema(
  rootTopologySchema.$id,
) as ValidateFunction;
export const validateChangedPathScope: ValidateFunction = ajv.getSchema(
  changedPathScopeSchema.$id,
) as ValidateFunction;

function errorMessage(error: ErrorObject): string {
  if (error.keyword === "additionalProperties") {
    return "additional property is not allowed";
  }
  return error.message ?? "schema validation failed";
}

export function schemaErrorDetails(
  errors: readonly ErrorObject[] | null | undefined,
): ErrorDetail[] {
  return (errors ?? []).map((error) => ({
    path: error.instancePath || "/",
    code: error.keyword,
    message: errorMessage(error),
  }));
}
