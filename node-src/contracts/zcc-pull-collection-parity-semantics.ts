import type { ErrorObject } from "ajv/dist/2020.js";

import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_RESOURCE_TYPES,
} from "../domain/zcc-collection-contract.js";
import type { ZccPullCollectionParity } from "../domain/zcc-pull-collection-parity.js";
import type { CompareZccPullCollectionProcessRequest } from "../process/types.js";

export const ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-collection-parity-semantics";
export const ZCC_PULL_COLLECTION_PARITY_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-collection-parity-request-semantics";

function record(value: unknown): Readonly<Record<string, unknown>> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Readonly<Record<string, unknown>>
    : null;
}

function semanticError(
  keyword: string,
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${keyword}`,
    keyword,
    params: { rule },
    message,
  };
}

interface SemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

function tupleEqual(left: unknown, right: unknown): boolean {
  const a = record(left);
  const b = record(right);
  return a !== null
    && b !== null
    && a.sha256 === b.sha256
    && a.size_bytes === b.size_bytes
    && a.item_count === b.item_count;
}

function derivedResourceStatus(resource: Readonly<Record<string, unknown>>): string {
  if (!tupleEqual(resource.before, resource.after)) return "unstable_reference";
  return tupleEqual(resource.before, resource.node) ? "equal" : "different";
}

export const validateZccPullCollectionParitySemantics: SemanticValidator = (
  _schema,
  data,
  _parentSchema,
  dataContext,
) => {
  const result = record(data);
  const resources = Array.isArray(result?.resources) ? result.resources : null;
  const counts = record(result?.counts);
  if (result === null || resources === null || counts === null) {
    delete validateZccPullCollectionParitySemantics.errors;
    return true;
  }
  const prefix = dataContext?.instancePath ?? "";
  const errors: ErrorObject[] = [];
  if (result.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256) {
    errors.push(semanticError(
      ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
      `${prefix}/catalog_sources_sha256`,
      "catalog_provenance",
      "collection parity must bind the frozen collector source digest",
    ));
  }
  const derivedCounts = {
    equal: 0,
    different: 0,
    unstable_reference: 0,
  };
  for (const [index, resourceType] of ZCC_COLLECTION_RESOURCE_TYPES.entries()) {
    const resource = record(resources[index]);
    if (resource === null) continue;
    if (resource.resource_type !== resourceType) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
        `${prefix}/resources/${index}/resource_type`,
        "resource_order",
        "collection parity resources must use the frozen catalog order",
      ));
    }
    if (
      typeof result.tenant === "string"
      && resource.path !== `pulls/${result.tenant}/${resourceType}.json`
    ) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
        `${prefix}/resources/${index}/path`,
        "derived_path",
        "collection parity resource path must be derived from tenant and resource",
      ));
    }
    const status = derivedResourceStatus(resource);
    if (resource.status !== status) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
        `${prefix}/resources/${index}/status`,
        "derived_resource_status",
        "collection parity resource status must be derived from all three tuples",
      ));
    }
    derivedCounts[status as keyof typeof derivedCounts] += 1;
  }
  const topStatus = derivedCounts.unstable_reference > 0
    ? "unstable_reference"
    : derivedCounts.different > 0
      ? "different"
      : "equal";
  if (result.status !== topStatus) {
    errors.push(semanticError(
      ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
      `${prefix}/status`,
      "derived_top_status",
      "collection parity status must be derived with reference instability precedence",
    ));
  }
  for (const key of ["equal", "different", "unstable_reference"] as const) {
    if (counts[key] !== derivedCounts[key]) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
        `${prefix}/counts/${key}`,
        "derived_count",
        "collection parity counts must be derived from resource statuses",
      ));
    }
  }
  validateZccPullCollectionParitySemantics.errors = errors;
  return errors.length === 0;
};

export const validateZccPullCollectionParityRequestSemantics: SemanticValidator = (
  _schema,
  data,
  _parentSchema,
  dataContext,
) => {
  const request = record(data);
  if (request?.operation !== "compare_zcc_pull_collection") {
    delete validateZccPullCollectionParityRequestSemantics.errors;
    return true;
  }
  const input = record(request.input);
  const receipts = Array.isArray(input?.receipts) ? input.receipts : null;
  if (input === null || receipts === null) {
    delete validateZccPullCollectionParityRequestSemantics.errors;
    return true;
  }
  const prefix = dataContext?.instancePath ?? "";
  const errors: ErrorObject[] = [];
  const tenant = typeof input.tenant === "string" ? input.tenant : null;
  for (const [index, resourceType] of ZCC_COLLECTION_RESOURCE_TYPES.entries()) {
    const receipt = record(receipts[index]);
    const artifact = record(receipt?.artifact);
    if (receipt === null || artifact === null) continue;
    if (
      receipt.resource_type !== resourceType
      || tenant === null
      || receipt.tenant !== tenant
      || receipt.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256
      || artifact.path !== `pulls/${tenant}/${resourceType}.json`
    ) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_REQUEST_SEMANTICS_KEYWORD,
        `${prefix}/input/receipts/${index}`,
        "receipt_scope",
        "collection parity receipts must match the complete ordered request scope",
      ));
    }
  }
  validateZccPullCollectionParityRequestSemantics.errors = errors;
  return errors.length === 0;
};

/** Bind a process request to the standalone parity result and Node receipts. */
export function zccPullCollectionParityOperationResultErrors(
  request: CompareZccPullCollectionProcessRequest,
  result: ZccPullCollectionParity,
): readonly ErrorObject[] {
  const errors: ErrorObject[] = [];
  if (
    result.reference !== request.input.reference
    || result.tenant !== request.input.tenant
    || result.catalog_sources_sha256 !== ZCC_COLLECTION_CATALOG_SOURCES_SHA256
    || request.input.receipts.some(
      (receipt) => receipt.catalog_sources_sha256 !== result.catalog_sources_sha256,
    )
  ) {
    errors.push(semanticError(
      ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
      "/",
      "request_scope",
      "collection parity result scope must match the request",
    ));
  }
  for (const [index, resourceType] of ZCC_COLLECTION_RESOURCE_TYPES.entries()) {
    const receipt = request.input.receipts[index];
    const resource = result.resources[index];
    if (
      receipt === undefined
      || resource === undefined
      || receipt.resource_type !== resourceType
      || resource.resource_type !== resourceType
      || resource.path !== receipt.artifact.path
      || resource.node.sha256 !== receipt.artifact.sha256
      || resource.node.size_bytes !== receipt.artifact.size_bytes
      || resource.node.item_count !== receipt.artifact.item_count
    ) {
      errors.push(semanticError(
        ZCC_PULL_COLLECTION_PARITY_SEMANTICS_KEYWORD,
        `/resources/${index}`,
        "receipt_result_join",
        "collection parity result must preserve the exact Node receipt join",
      ));
    }
  }
  return errors;
}
