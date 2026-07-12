import embeddedCatalogJson from "../../catalogs/zia-transform-cohort.v1.json" with { type: "json" };

import { pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";
import {
  transformPullItemsKernel,
  type PullTransformResult,
} from "./pull-transform.js";
import {
  immutableTransformContractCopy,
  type TransformCatalog,
  type TransformCatalogResource,
} from "./transform-catalog.js";
import {
  privateZiaTransformCohortErrorDetails,
  validatePrivateZiaTransformCohort,
} from "./zia-transform-cohort-validator.js";

export type ZiaTransformCohortResourceType =
  | "zia_admin_roles"
  | "zia_traffic_forwarding_static_ip"
  | "zia_url_categories";

export interface ZiaTransformCohortCatalog {
  readonly kind: "infrawright.transform_resource_cohort";
  readonly schema_version: 1;
  readonly product: "zia";
  readonly resources: readonly TransformCatalogResource[];
  readonly source_files: readonly string[];
  readonly sources_sha256: string;
}

const ZIA_RESOURCE_TYPES = [
  "zia_admin_roles",
  "zia_traffic_forwarding_static_ip",
  "zia_url_categories",
] as const;

const NO_HTML_UNESCAPE_COMPATIBILITY: TransformCatalog["python_compatibility"] =
  Object.freeze({
    html_unescape: Object.freeze({
      entities: Object.freeze(Object.create(null) as Record<string, string>),
      invalid_codepoints: Object.freeze([] as number[]),
      invalid_references: Object.freeze(
        Object.create(null) as Record<string, string>,
      ),
    }),
  });

function invalidCatalog(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_ZIA_TRANSFORM_COHORT",
    category: "domain",
    message,
    details: privateZiaTransformCohortErrorDetails(),
  });
}

function assertSortedUnique(values: readonly string[], label: string): void {
  const expected = sortedStrings(new Set(values));
  if (
    expected.length !== values.length
    || expected.some((value, index) => value !== values[index])
  ) {
    invalidCatalog(`${label} must be sorted and unique`);
  }
}

function validatedCatalog(candidate: unknown): ZiaTransformCohortCatalog {
  if (!validatePrivateZiaTransformCohort(candidate)) {
    invalidCatalog("ZIA transform cohort does not match schema version 1");
  }
  const catalog = candidate as ZiaTransformCohortCatalog;
  assertSortedUnique(catalog.source_files, "source_files");
  const resourceTypes = catalog.resources.map((resource) => resource.type);
  if (
    resourceTypes.length !== ZIA_RESOURCE_TYPES.length
    || ZIA_RESOURCE_TYPES.some((resourceType, index) => {
      return resourceTypes[index] !== resourceType;
    })
  ) {
    invalidCatalog(
      `resources must be exactly: ${ZIA_RESOURCE_TYPES.join(", ")}`,
    );
  }
  if (catalog.resources.some((resource) => resource.html_unescape_passes !== 0)) {
    invalidCatalog("ZIA cohort resources must not apply HTML unescaping");
  }
  return immutableTransformContractCopy(catalog) as ZiaTransformCohortCatalog;
}

const EMBEDDED_ZIA_TRANSFORM_COHORT = validatedCatalog(embeddedCatalogJson);

/** Return the exact private three-resource ZIA transform catalog. */
export function loadZiaTransformCohortCatalog(): ZiaTransformCohortCatalog {
  return EMBEDDED_ZIA_TRANSFORM_COHORT;
}

/** Accept only the exact semantics committed with the private cohort. */
export function requireSupportedZiaTransformCohortCatalog(
  candidate: unknown,
): ZiaTransformCohortCatalog {
  const catalog = validatedCatalog(candidate);
  if (!pythonJsonEqual(catalog, EMBEDDED_ZIA_TRANSFORM_COHORT)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ZIA_TRANSFORM_COHORT",
      category: "domain",
      message: "transform requires the supported embedded ZIA cohort",
    });
  }
  return EMBEDDED_ZIA_TRANSFORM_COHORT;
}

/**
 * Private, pure transform for exactly the reviewed ZIA cohort. It consumes
 * already-pulled JSON values and performs no process, network, or file work.
 */
export function transformZiaCohortItems(options: {
  readonly catalog: ZiaTransformCohortCatalog;
  readonly rawItems: readonly unknown[];
  readonly resourceType: string;
}): PullTransformResult {
  const catalog = requireSupportedZiaTransformCohortCatalog(options.catalog);
  const resource = catalog.resources.find((entry) => {
    return entry.type === options.resourceType;
  });
  if (resource === undefined) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ZIA_TRANSFORM_RESOURCE",
      category: "domain",
      message: `resource type ${JSON.stringify(options.resourceType)} is not in the ZIA transform cohort`,
    });
  }
  return transformPullItemsKernel({
    compatibility: NO_HTML_UNESCAPE_COMPATIBILITY,
    rawItems: options.rawItems,
    resource,
  });
}
