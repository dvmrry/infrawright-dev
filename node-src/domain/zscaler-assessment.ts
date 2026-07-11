import zscalerCatalog from "../../catalogs/zscaler-root-catalog.v1.json" with { type: "json" };

import { ProcessFailure } from "./errors.js";
import type { RootCatalog } from "./types.js";
import { pythonJsonEqual } from "../json/python-equality.js";

/**
 * Public assessment is intentionally narrower than the generic root APIs.
 * The embedded catalog identity binds the current all-Zscaler pack/registry
 * source digest, whose manifests contain no assessment-guidance rules.
 */
export function requireSupportedAssessmentCatalog(catalog: RootCatalog): void {
  if (!pythonJsonEqual(catalog, zscalerCatalog)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ASSESSMENT_CATALOG",
      category: "domain",
      message: "saved-plan assessment requires the supported Zscaler catalog",
    });
  }
}
