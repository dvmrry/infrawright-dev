import embeddedCatalogJson from "../../catalogs/zpa-transform-cohort-catalog.v1.json" with { type: "json" };

import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";
import {
  immutableTransformContractCopy,
  type TransformCatalogResource,
  type TransformValueEncoding,
} from "./transform-catalog.js";

const RESOURCE_TYPES = [
  "zpa_pra_console_controller",
  "zpa_pra_portal_controller",
] as const;

const SOURCE_FILES = [
  "catalogs/zcc-transform-catalog.v1.json",
  "docs/evidence/zpa-provider-v4.4.6.json",
  "packs/zpa/pack.json",
  "packs/zpa/registry.json",
  "packs/zpa/schemas/provider/zpa.json",
] as const;

const ABSENT_OVERRIDE_FILES = [
  "packs/zpa/overrides/zpa_pra_console_controller.json",
  "packs/zpa/overrides/zpa_pra_portal_controller.json",
] as const;

export type ZpaTransformCohortResourceType = typeof RESOURCE_TYPES[number];

export interface ZpaTransformCohortResource extends TransformCatalogResource {
  readonly type: ZpaTransformCohortResourceType;
  readonly provider_evidence: {
    readonly fetch: {
      readonly optional_http_statuses: readonly number[];
      readonly pagination: "zpa";
      readonly path: string;
    };
    readonly generated_config_qualification: "terraform_runtime_evidence_required";
    readonly state_shape_sha256: string;
  };
}

export interface ZpaTransformCohortCatalog {
  readonly absent_override_files: readonly string[];
  readonly kind: "infrawright.zpa_transform_cohort_catalog";
  readonly product: "zpa";
  readonly provider: {
    readonly evidence_commit: string;
    readonly source: "zscaler/zpa";
    readonly version: "4.4.6";
  };
  readonly python_compatibility_source: "catalogs/zcc-transform-catalog.v1.json";
  readonly resources: readonly ZpaTransformCohortResource[];
  readonly schema_version: 1;
  readonly source_files: readonly string[];
  readonly sources_sha256: string;
}

function invalidCatalog(message: string): never {
  throw new ProcessFailure({
    category: "domain",
    code: "INVALID_ZPA_TRANSFORM_COHORT_CATALOG",
    message,
  });
}

function exactKeys(value: object, expected: readonly string[]): boolean {
  const actual = sortedStrings(Object.keys(value));
  return actual.length === expected.length
    && expected.every((key, index) => key === actual[index]);
}

function exactStrings(value: unknown, expected: readonly string[]): boolean {
  return Array.isArray(value)
    && value.length === expected.length
    && expected.every((item, index) => item === value[index]);
}

function stringArray(value: unknown): value is readonly string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

function validEncoding(value: unknown): value is TransformValueEncoding {
  if (value === "bool" || value === "number" || value === "string") {
    return true;
  }
  return Array.isArray(value)
    && value.length === 2
    && (
      (value[0] === "list"
        && (value[1] === "bool" || value[1] === "number" || value[1] === "string"))
      || ((value[0] === "set" || value[0] === "map") && value[1] === "string")
    );
}

function validProjection(value: unknown): boolean {
  if (!isJsonRecord(value) || !exactKeys(value, [
    "attributes", "blocks", "silently_ignored_attributes",
  ])) {
    return false;
  }
  const attributes = value.attributes;
  const blocks = value.blocks;
  const ignored = value.silently_ignored_attributes;
  if (
    !isJsonRecord(attributes)
    || !Object.values(attributes).every(validEncoding)
    || !exactStrings(Object.keys(attributes), sortedStrings(Object.keys(attributes)))
    || !isJsonRecord(blocks)
    || !exactStrings(Object.keys(blocks), sortedStrings(Object.keys(blocks)))
    || !stringArray(ignored)
    || !exactStrings(ignored, sortedStrings(new Set(ignored)))
  ) {
    return false;
  }
  return Object.values(blocks).every((block) => {
    return isJsonRecord(block)
      && exactKeys(block, ["cardinality", "projection"])
      && (block.cardinality === "single" || block.cardinality === "many")
      && validProjection(block.projection);
  });
}

function validResource(
  value: unknown,
  expectedType: ZpaTransformCohortResourceType,
): value is ZpaTransformCohortResource {
  if (!isJsonRecord(value) || !exactKeys(value, [
    "acknowledged_drops", "html_unescape_passes", "import_id", "invert_bool",
    "key_fields", "lookup_source", "projection", "provider_evidence",
    "references", "renames", "split_csv", "type",
  ])) {
    return false;
  }
  const evidence = value.provider_evidence;
  const fetch = isJsonRecord(evidence) ? evidence.fetch : null;
  if (
    value.type !== expectedType
    || value.html_unescape_passes !== 2
    || !exactStrings(value.key_fields, ["name"])
    || !stringArray(value.acknowledged_drops)
    || !stringArray(value.invert_bool)
    || !stringArray(value.split_csv)
    || value.lookup_source !== null
    || !isJsonRecord(value.import_id)
    || !pythonJsonEqual(value.import_id, {
      segments: [{ field: "id" }],
      template: "{id}",
    })
    || !isJsonRecord(value.references)
    || Object.keys(value.references).length !== 0
    || !isJsonRecord(value.renames)
    || Object.keys(value.renames).length !== 0
    || !validProjection(value.projection)
    || !isJsonRecord(evidence)
    || !exactKeys(evidence, [
      "fetch", "generated_config_qualification", "state_shape_sha256",
    ])
    || evidence.generated_config_qualification !== "terraform_runtime_evidence_required"
    || typeof evidence.state_shape_sha256 !== "string"
    || !/^[0-9a-f]{64}$/.test(evidence.state_shape_sha256)
    || !isJsonRecord(fetch)
    || !exactKeys(fetch, ["optional_http_statuses", "pagination", "path"])
    || fetch.pagination !== "zpa"
    || typeof fetch.path !== "string"
    || !Array.isArray(fetch.optional_http_statuses)
    || !fetch.optional_http_statuses.every(Number.isInteger)
  ) {
    return false;
  }
  return true;
}

function validatedCatalog(candidate: unknown): ZpaTransformCohortCatalog {
  if (!isJsonRecord(candidate) || !exactKeys(candidate, [
    "absent_override_files", "kind", "product", "provider",
    "python_compatibility_source", "resources", "schema_version",
    "source_files", "sources_sha256",
  ])) {
    return invalidCatalog("ZPA transform cohort catalog has an invalid top-level shape");
  }
  const provider = candidate.provider;
  if (
    candidate.kind !== "infrawright.zpa_transform_cohort_catalog"
    || candidate.schema_version !== 1
    || candidate.product !== "zpa"
    || candidate.python_compatibility_source !== SOURCE_FILES[0]
    || !exactStrings(candidate.source_files, SOURCE_FILES)
    || !exactStrings(candidate.absent_override_files, ABSENT_OVERRIDE_FILES)
    || typeof candidate.sources_sha256 !== "string"
    || !/^[0-9a-f]{64}$/.test(candidate.sources_sha256)
    || !isJsonRecord(provider)
    || !exactKeys(provider, ["evidence_commit", "source", "version"])
    || provider.source !== "zscaler/zpa"
    || provider.version !== "4.4.6"
    || typeof provider.evidence_commit !== "string"
    || !/^[0-9a-f]{40}$/.test(provider.evidence_commit)
    || !Array.isArray(candidate.resources)
    || candidate.resources.length !== RESOURCE_TYPES.length
    || !candidate.resources.every((resource, index) => {
      const expected = RESOURCE_TYPES[index];
      return expected !== undefined && validResource(resource, expected);
    })
  ) {
    return invalidCatalog("ZPA transform cohort catalog is malformed");
  }
  return immutableTransformContractCopy(candidate) as ZpaTransformCohortCatalog;
}

const EMBEDDED_CATALOG = validatedCatalog(embeddedCatalogJson);

/** Return the private, exact two-resource ZPA transform cohort. */
export function loadZpaTransformCohortCatalog(): ZpaTransformCohortCatalog {
  return EMBEDDED_CATALOG;
}

/** Accept only a semantic copy of the catalog embedded in this build. */
export function requireSupportedZpaTransformCohortCatalog(
  candidate: unknown,
): ZpaTransformCohortCatalog {
  const catalog = validatedCatalog(candidate);
  if (!pythonJsonEqual(catalog, EMBEDDED_CATALOG)) {
    throw new ProcessFailure({
      category: "domain",
      code: "UNSUPPORTED_ZPA_TRANSFORM_COHORT_CATALOG",
      message: "transform requires the supported embedded ZPA cohort catalog",
    });
  }
  return EMBEDDED_CATALOG;
}
