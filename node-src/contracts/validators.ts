import {
  Ajv2020,
  type ErrorObject,
  type ValidateFunction,
} from "ajv/dist/2020.js";

import processRequestSchema from "../../docs/schemas/process-request.schema.json" with { type: "json" };
import processResponseSchema from "../../docs/schemas/process-response.schema.json" with { type: "json" };
import changedPathScopeSchema from "../../docs/schemas/changed-path-scope.schema.json" with { type: "json" };
import planRootsSchema from "../../docs/schemas/plan-roots.schema.json" with { type: "json" };
import rootCatalogSchema from "../../docs/schemas/root-catalog.schema.json" with { type: "json" };
import rootTopologySchema from "../../docs/schemas/root-topology.schema.json" with { type: "json" };
import savedPlanAssessmentSchema from "../../docs/schemas/saved-plan-assessment.schema.json" with { type: "json" };
import transformCatalogSchema from "../../docs/schemas/transform-catalog.schema.json" with { type: "json" };
import zccAdoptionCatalogSchema from "../../docs/schemas/zcc-adoption-catalog.schema.json" with { type: "json" };
import zccAdoptionArtifactSetSchema from "../../docs/schemas/zcc-adoption-artifact-set.schema.json" with { type: "json" };
import zccAdoptionArtifactParitySchema from "../../docs/schemas/zcc-adoption-artifact-parity.schema.json" with { type: "json" };
import zccAdoptionArtifactMaterializationSchema from "../../docs/schemas/zcc-adoption-artifact-materialization.schema.json" with { type: "json" };
import zccPullArtifactSetSchema from "../../docs/schemas/zcc-pull-artifact-set.schema.json" with { type: "json" };
import zccPullRefreshArtifactSetSchema from "../../docs/schemas/zcc-pull-refresh-artifact-set.schema.json" with { type: "json" };
import zccPullArtifactParitySchema from "../../docs/schemas/zcc-pull-artifact-parity.schema.json" with { type: "json" };
import zccPullArtifactMaterializationSchema from "../../docs/schemas/zcc-pull-artifact-materialization.schema.json" with { type: "json" };
import zccPullRefreshParitySeedSchema from "../../docs/schemas/zcc-pull-refresh-parity-seed.schema.json" with { type: "json" };
import zccPullRefreshParitySchema from "../../docs/schemas/zcc-pull-refresh-parity.schema.json" with { type: "json" };
import zccPullRefreshMaterializationSchema from "../../docs/schemas/zcc-pull-refresh-materialization.schema.json" with { type: "json" };
import zccPullRefreshPendingTransitionSchema from "../../docs/schemas/zcc-pull-refresh-pending-transition.schema.json" with { type: "json" };
import zccPullRefreshAcknowledgementSchema from "../../docs/schemas/zcc-pull-refresh-acknowledgement.schema.json" with { type: "json" };
import zccPullCollectionSchema from "../../docs/schemas/zcc-pull-collection.schema.json" with { type: "json" };
import zccPullCollectionParitySchema from "../../docs/schemas/zcc-pull-collection-parity.schema.json" with { type: "json" };
import type { ErrorDetail } from "../domain/errors.js";
import {
  ASSESSMENT_SEMANTICS_KEYWORD,
  validateAssessmentSemantics,
} from "./saved-plan-assessment-semantics.js";
import {
  ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD,
  validateZccPullArtifactSemantics,
} from "./zcc-pull-artifact-semantics.js";
import {
  ZCC_PULL_REFRESH_SEMANTICS_KEYWORD,
  validateZccPullRefreshSemantics,
} from "./zcc-pull-refresh-semantics.js";
import {
  ZCC_PULL_PARITY_SEMANTICS_KEYWORD,
  validateZccPullParitySemantics,
} from "./zcc-pull-parity-semantics.js";
import {
  ZCC_PULL_MATERIALIZATION_SEMANTICS_KEYWORD,
  ZCC_PULL_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  validateZccPullMaterializationRequestSemantics,
  validateZccPullMaterializationSemantics,
} from "./zcc-pull-materialization-semantics.js";
import {
  ZCC_PULL_REFRESH_PARITY_SEED_SEMANTICS_KEYWORD,
  ZCC_PULL_REFRESH_PARITY_SEMANTICS_KEYWORD,
  ZCC_PULL_REFRESH_PARITY_REQUEST_SEMANTICS_KEYWORD,
  validateZccPullRefreshParitySeedSemantics,
  validateZccPullRefreshParitySemantics,
  validateZccPullRefreshParityRequestSemantics,
} from "./zcc-pull-refresh-parity-semantics.js";
import {
  ZCC_PULL_REFRESH_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  ZCC_PULL_REFRESH_MATERIALIZATION_SEMANTICS_KEYWORD,
  ZCC_PULL_REFRESH_PENDING_TRANSITION_SEMANTICS_KEYWORD,
  validateZccPullRefreshMaterializationRequestSemantics,
  validateZccPullRefreshMaterializationSemantics,
  validateZccPullRefreshPendingTransitionSemantics,
} from "./zcc-pull-refresh-materialization-semantics.js";
import {
  ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_REQUEST_SEMANTICS_KEYWORD,
  ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_SEMANTICS_KEYWORD,
  validateZccPullRefreshAcknowledgementRequestSemantics,
  validateZccPullRefreshAcknowledgementSemantics,
} from "./zcc-pull-refresh-acknowledgement-semantics.js";
import {
  ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD,
  validateZccAdoptionParitySemantics,
} from "./zcc-adoption-parity-semantics.js";
import {
  ZCC_ADOPTION_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  ZCC_ADOPTION_MATERIALIZATION_SEMANTICS_KEYWORD,
  validateZccAdoptionMaterializationRequestSemantics,
  validateZccAdoptionMaterializationSemantics,
} from "./zcc-adoption-materialization-semantics.js";
import {
  ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD,
  validateZccPullCollectionSemantics,
} from "./zcc-pull-collection-semantics.js";
import {
  ZCC_PULL_COLLECTION_PARITY_REQUEST_SEMANTICS_KEYWORD,
  ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
  validateZccPullCollectionParityRequestSemantics,
  validateZccPullCollectionParitySemantics,
} from "./zcc-pull-collection-parity-semantics.js";

const AJV_OPTIONS = {
  coerceTypes: false,
  ownProperties: true,
  removeAdditional: false,
  strict: true,
  useDefaults: false,
} as const;

const ajv = new Ajv2020({
  ...AJV_OPTIONS,
  allErrors: true,
});

// Request diagnostics are caller-facing and must remain bounded by the request
// rather than multiplying one malformed array into hundreds of thousands of
// Ajv errors. Other validators retain all-errors behavior for controlled
// internal artifacts and test diagnostics.
const requestAjv = new Ajv2020({
  ...AJV_OPTIONS,
  allErrors: false,
});

ajv.addKeyword({
  keyword: ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullCollectionSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullCollectionSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullCollectionParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullCollectionParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_COLLECTION_PARITY_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullCollectionParityRequestSemantics,
});

ajv.addKeyword({
  keyword: ASSESSMENT_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateAssessmentSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PARITY_SEED_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshParitySeedSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PARITY_SEED_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshParitySeedSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshMaterializationRequestSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshAcknowledgementRequestSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullArtifactSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PENDING_TRANSITION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshPendingTransitionSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_MATERIALIZATION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshMaterializationSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_MATERIALIZATION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshMaterializationSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshAcknowledgementSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_REFRESH_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullParitySemantics,
});

ajv.addKeyword({
  keyword: ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccAdoptionParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccAdoptionParitySemantics,
});

ajv.addKeyword({
  keyword: ZCC_ADOPTION_MATERIALIZATION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccAdoptionMaterializationSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_ADOPTION_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccAdoptionMaterializationRequestSemantics,
});

ajv.addKeyword({
  keyword: ZCC_PULL_MATERIALIZATION_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullMaterializationSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_REFRESH_PARITY_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullRefreshParityRequestSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_ARTIFACT_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullArtifactSemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_PARITY_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullParitySemantics,
});

requestAjv.addKeyword({
  keyword: ZCC_PULL_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
  schemaType: "boolean",
  type: "object",
  errors: true,
  validate: validateZccPullMaterializationRequestSemantics,
});

ajv.addSchema(rootTopologySchema);
ajv.addSchema(changedPathScopeSchema);
ajv.addSchema(planRootsSchema);
ajv.addSchema(savedPlanAssessmentSchema);
ajv.addSchema(transformCatalogSchema);
ajv.addSchema(zccAdoptionCatalogSchema);
ajv.addSchema(zccPullArtifactSetSchema);
ajv.addSchema(zccAdoptionArtifactSetSchema);
ajv.addSchema(zccAdoptionArtifactParitySchema);
ajv.addSchema(zccAdoptionArtifactMaterializationSchema);
ajv.addSchema(zccPullRefreshArtifactSetSchema);
ajv.addSchema(zccPullArtifactParitySchema);
ajv.addSchema(zccPullArtifactMaterializationSchema);
ajv.addSchema(zccPullRefreshParitySeedSchema);
ajv.addSchema(zccPullRefreshParitySchema);
ajv.addSchema(zccPullRefreshPendingTransitionSchema);
ajv.addSchema(zccPullRefreshMaterializationSchema);
ajv.addSchema(zccPullRefreshAcknowledgementSchema);
ajv.addSchema(zccPullCollectionSchema);
ajv.addSchema(zccPullCollectionParitySchema);
requestAjv.addSchema(zccPullArtifactParitySchema);
requestAjv.addSchema(zccPullArtifactSetSchema);
requestAjv.addSchema(zccAdoptionArtifactParitySchema);
requestAjv.addSchema(zccPullRefreshParitySeedSchema);
requestAjv.addSchema(zccPullRefreshParitySchema);
requestAjv.addSchema(zccPullRefreshMaterializationSchema);
requestAjv.addSchema(zccPullCollectionSchema);
requestAjv.addSchema(zccPullCollectionParitySchema);

export const validateProcessRequest: ValidateFunction = requestAjv.compile(
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
export const validatePlanRoots: ValidateFunction = ajv.getSchema(
  planRootsSchema.$id,
) as ValidateFunction;
export const validateSavedPlanAssessment: ValidateFunction = ajv.getSchema(
  savedPlanAssessmentSchema.$id,
) as ValidateFunction;
export const validateTransformCatalog: ValidateFunction = ajv.getSchema(
  transformCatalogSchema.$id,
) as ValidateFunction;
export const validateZccAdoptionCatalog: ValidateFunction = ajv.getSchema(
  zccAdoptionCatalogSchema.$id,
) as ValidateFunction;
export const validateZccAdoptionArtifactSet: ValidateFunction = ajv.getSchema(
  zccAdoptionArtifactSetSchema.$id,
) as ValidateFunction;
export const validateZccAdoptionArtifactParity: ValidateFunction = ajv.getSchema(
  zccAdoptionArtifactParitySchema.$id,
) as ValidateFunction;
export const validateZccAdoptionArtifactMaterialization: ValidateFunction =
  ajv.getSchema(zccAdoptionArtifactMaterializationSchema.$id) as ValidateFunction;
export const validateZccPullArtifactSet: ValidateFunction = ajv.getSchema(
  zccPullArtifactSetSchema.$id,
) as ValidateFunction;
export const validateZccPullRefreshArtifactSet: ValidateFunction = ajv.getSchema(
  zccPullRefreshArtifactSetSchema.$id,
) as ValidateFunction;
export const validateZccPullArtifactParity: ValidateFunction = ajv.getSchema(
  zccPullArtifactParitySchema.$id,
) as ValidateFunction;
export const validateZccPullArtifactMaterialization: ValidateFunction =
  ajv.getSchema(zccPullArtifactMaterializationSchema.$id) as ValidateFunction;
export const validateZccPullRefreshParitySeed: ValidateFunction = ajv.getSchema(
  zccPullRefreshParitySeedSchema.$id,
) as ValidateFunction;
export const validateZccPullRefreshParity: ValidateFunction = ajv.getSchema(
  zccPullRefreshParitySchema.$id,
) as ValidateFunction;
export const validateZccPullRefreshPendingTransition: ValidateFunction =
  ajv.getSchema(zccPullRefreshPendingTransitionSchema.$id) as ValidateFunction;
export const validateZccPullRefreshMaterialization: ValidateFunction =
  ajv.getSchema(zccPullRefreshMaterializationSchema.$id) as ValidateFunction;
export const validateZccPullRefreshAcknowledgement: ValidateFunction =
  ajv.getSchema(zccPullRefreshAcknowledgementSchema.$id) as ValidateFunction;
export const validateZccPullCollection: ValidateFunction = ajv.getSchema(
  zccPullCollectionSchema.$id,
) as ValidateFunction;
export const validateZccPullCollectionParity: ValidateFunction = ajv.getSchema(
  zccPullCollectionParitySchema.$id,
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
