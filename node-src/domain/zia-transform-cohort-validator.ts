import {
  Ajv2020,
  type ErrorObject,
  type ValidateFunction,
} from "ajv/dist/2020.js";

import transformCatalogSchema from "../../docs/schemas/transform-catalog.schema.json" with { type: "json" };
import transformResourceCohortSchema from "../../docs/schemas/transform-resource-cohort.schema.json" with { type: "json" };
import type { ErrorDetail } from "./errors.js";

const ajv = new Ajv2020({
  allErrors: true,
  coerceTypes: false,
  ownProperties: true,
  removeAdditional: false,
  strict: true,
  useDefaults: false,
});

// The private cohort schema reuses only the generic projection/import
// definitions from the established transform schema. This validator is not
// registered with the shared process-contract AJV instance.
ajv.addSchema(transformCatalogSchema);

export const validatePrivateZiaTransformCohort: ValidateFunction = ajv.compile(
  transformResourceCohortSchema,
);

function errorMessage(error: ErrorObject): string {
  return error.keyword === "additionalProperties"
    ? "additional property is not allowed"
    : error.message ?? "schema validation failed";
}

export function privateZiaTransformCohortErrorDetails(): ErrorDetail[] {
  const errors = validatePrivateZiaTransformCohort.errors ?? [];
  const details = errors.slice(0, 64).map((error) => ({
    path: error.instancePath || "/",
    code: error.keyword,
    message: errorMessage(error),
  }));
  if (errors.length > details.length) {
    details.push({
      path: "/",
      code: "schema_errors_truncated",
      message: "additional schema errors were omitted",
    });
  }
  return details;
}
