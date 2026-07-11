import zscalerCatalog from "../../catalogs/zscaler-root-catalog.v1.json" with { type: "json" };

import { ProcessFailure } from "./errors.js";
import type { RootCatalog } from "./types.js";
import { pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";

const embeddedResources = zscalerCatalog.resources as readonly {
  readonly generated: boolean;
  readonly provider: string;
  readonly type: string;
}[];

/** Exact generated ZCC member set accepted by the bundled root catalog. */
export const SUPPORTED_ZCC_ROOT_MEMBERS: readonly string[] = Object.freeze(
  sortedStrings(embeddedResources
    .filter((resource) => resource.provider === "zcc" && resource.generated)
    .map((resource) => resource.type)),
);

/** Every generated label reserved by the exact bundled all-Zscaler catalog. */
export const SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS: readonly string[] =
  Object.freeze(sortedStrings(embeddedResources
    .filter((resource) => resource.generated)
    .map((resource) => resource.type)));

function supportedZscalerCatalog(catalog: RootCatalog): boolean {
  return pythonJsonEqual(catalog, zscalerCatalog);
}

/**
 * Public assessment is intentionally narrower than the generic root APIs.
 * The embedded catalog identity binds the current all-Zscaler pack/registry
 * source digest, whose manifests contain no assessment-guidance rules.
 */
export function requireSupportedAssessmentCatalog(catalog: RootCatalog): void {
  if (!supportedZscalerCatalog(catalog)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ASSESSMENT_CATALOG",
      category: "domain",
      message: "saved-plan assessment requires the supported Zscaler catalog",
    });
  }
}

/** Bootstrap artifact paths are also meaningful only for the exact bundle catalog. */
export function requireSupportedZscalerCompileCatalog(catalog: RootCatalog): void {
  if (!supportedZscalerCatalog(catalog)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_COMPILE_CATALOG",
      category: "domain",
      message: "pull artifact compilation requires the supported Zscaler catalog",
    });
  }
}
