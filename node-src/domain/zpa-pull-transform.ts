import type { PullTransformResult } from "./pull-transform.js";
import { transformPullItemsKernel } from "./pull-transform.js";
import { loadZccTransformCatalog } from "./transform-catalog.js";
import { ProcessFailure } from "./errors.js";
import {
  requireSupportedZpaTransformCohortCatalog,
  type ZpaTransformCohortCatalog,
} from "./zpa-transform-cohort-catalog.js";

/**
 * Private already-pulled-JSON transform for the exact embedded ZPA cohort.
 *
 * This seam performs no HTTP, collection, publication, Terraform, adoption,
 * or generated-configuration work. Callers remain responsible for treating a
 * non-empty `drops` result as a failed evidence gate.
 *
 * @internal Differential migration only.
 */
export function transformZpaCohortItems(options: {
  readonly catalog: ZpaTransformCohortCatalog;
  readonly rawItems: readonly unknown[];
  readonly resourceType: string;
}): PullTransformResult {
  const catalog = requireSupportedZpaTransformCohortCatalog(options.catalog);
  const resource = catalog.resources.find((entry) => {
    return entry.type === options.resourceType;
  });
  if (resource === undefined) {
    throw new ProcessFailure({
      category: "domain",
      code: "UNSUPPORTED_ZPA_TRANSFORM_RESOURCE",
      message: `resource type ${JSON.stringify(options.resourceType)} is not in the private ZPA transform cohort`,
    });
  }
  return transformPullItemsKernel({
    compatibility: loadZccTransformCatalog().python_compatibility,
    rawItems: options.rawItems,
    resource,
  });
}
