import type { ErrorObject } from "ajv/dist/2020.js";

import { ZCC_COLLECTION_CATALOG_SOURCES_SHA256 } from "../domain/zcc-collection-contract.js";
import type { ZccPullCollectionReceipt } from "../domain/zcc-pull-collection.js";
import type { CollectZccPullProcessRequest } from "../process/types.js";

export const ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-collection-semantics";

function record(value: unknown): Readonly<Record<string, unknown>> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Readonly<Record<string, unknown>>
    : null;
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD}`,
    keyword: ZCC_PULL_COLLECTION_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

export interface ZccPullCollectionSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Bind v1 provenance and the derived path inside the standalone receipt. */
export const validateZccPullCollectionSemantics:
  ZccPullCollectionSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const receipt = record(data);
    const artifact = record(receipt?.artifact);
    if (receipt === null || artifact === null) {
      delete validateZccPullCollectionSemantics.errors;
      return true;
    }
    const prefix = dataContext?.instancePath ?? "";
    const errors: ErrorObject[] = [];
    if (receipt.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256) {
      errors.push(semanticError(
        `${prefix}/catalog_sources_sha256`,
        "catalog_provenance",
        "collection receipt must bind the exact collector source digest",
      ));
    }
    if (
      typeof receipt.tenant === "string"
      && typeof receipt.resource_type === "string"
      && artifact.path !== `pulls/${receipt.tenant}/${receipt.resource_type}.json`
    ) {
      errors.push(semanticError(
        `${prefix}/artifact/path`,
        "derived_path",
        "collection artifact path must be derived from tenant and resource",
      ));
    }
    validateZccPullCollectionSemantics.errors = errors;
    return errors.length === 0;
  };

/** Join the public request coordinates to its content-free receipt. */
export function zccPullCollectionOperationResultErrors(
  request: CollectZccPullProcessRequest,
  result: ZccPullCollectionReceipt,
): readonly ErrorObject[] {
  const errors: ErrorObject[] = [];
  if (
    result.kind !== "infrawright.zcc_pull_collection"
    || result.mode !== request.input.mode
    || result.publication.policy !== request.input.publication
  ) {
    errors.push(semanticError(
      "/",
      "operation_mode_publication",
      "collection receipt kind, mode, and publication must match the request",
    ));
  }
  if (result.tenant !== request.input.tenant) {
    errors.push(semanticError(
      "/tenant",
      "tenant",
      "collection receipt tenant must match the request",
    ));
  }
  if (result.resource_type !== request.input.resource_type) {
    errors.push(semanticError(
      "/resource_type",
      "resource_type",
      "collection receipt resource must match the request",
    ));
  }
  return errors;
}
