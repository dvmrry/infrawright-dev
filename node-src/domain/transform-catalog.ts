import embeddedCatalogJson from "../../catalogs/zcc-transform-catalog.v1.json" with { type: "json" };

import { validateTransformCatalog, schemaErrorDetails } from "../contracts/validators.js";
import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";

export type TransformPrimitiveEncoding = "bool" | "number" | "string";
export type TransformValueEncoding =
  | TransformPrimitiveEncoding
  | readonly ["list", TransformPrimitiveEncoding]
  | readonly ["set" | "map", "string"];

export interface TransformProjectionBlock {
  readonly cardinality: "many" | "single";
  readonly projection: TransformProjection;
}

export interface TransformProjection {
  readonly attributes: Readonly<Record<string, TransformValueEncoding>>;
  readonly blocks: Readonly<Record<string, TransformProjectionBlock>>;
  readonly silently_ignored_attributes: readonly string[];
}

export type TransformImportIdSegment =
  | { readonly literal: string }
  | { readonly field: string };

export interface TransformReference {
  readonly referent: string;
  readonly name_field: string;
}

export interface TransformCatalogResource {
  readonly type: string;
  readonly key_fields: readonly string[];
  readonly html_unescape_passes: 0 | 2;
  readonly acknowledged_drops: readonly string[];
  readonly invert_bool: readonly string[];
  readonly renames: Readonly<Record<string, string>>;
  readonly skip_if?: readonly Readonly<Record<string, unknown>>[];
  readonly sort_lists?: readonly string[];
  readonly split_csv: readonly string[];
  readonly projection: TransformProjection;
  readonly import_id: {
    readonly template: string;
    readonly segments: readonly TransformImportIdSegment[];
  };
  readonly lookup_source: {
    readonly name_field: string;
  } | null;
  readonly references: Readonly<Record<string, TransformReference>>;
}

export interface TransformHtmlUnescapeCompatibility {
  readonly entities: Readonly<Record<string, string>>;
  readonly invalid_codepoints: readonly number[];
  readonly invalid_references: Readonly<Record<string, string>>;
}

export interface TransformCatalog {
  readonly kind: "infrawright.transform_catalog";
  readonly schema_version: 1;
  readonly product: "zcc";
  readonly resources: readonly TransformCatalogResource[];
  readonly python_compatibility: {
    readonly html_unescape: TransformHtmlUnescapeCompatibility;
  };
  readonly source_files: readonly string[];
  readonly sources_sha256: string;
}

const ZCC_RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

function invalidCatalog(
  message: string,
  details = schemaErrorDetails(validateTransformCatalog.errors),
): never {
  throw new ProcessFailure({
    code: "INVALID_TRANSFORM_CATALOG",
    category: "domain",
    message,
    details,
  });
}

function assertSortedUnique(values: readonly string[], label: string): void {
  const expected = sortedStrings(new Set(values));
  if (
    expected.length !== values.length
    || expected.some((value, index) => value !== values[index])
  ) {
    invalidCatalog(`${label} must be sorted and unique`, []);
  }
}

function assertZccCatalogInvariants(catalog: TransformCatalog): void {
  assertSortedUnique(catalog.source_files, "source_files");
  const resourceTypes = catalog.resources.map((resource) => resource.type);
  if (
    resourceTypes.length !== ZCC_RESOURCE_TYPES.length
    || ZCC_RESOURCE_TYPES.some((resourceType, index) => {
      return resourceTypes[index] !== resourceType;
    })
  ) {
    invalidCatalog(
      `resources must be exactly: ${ZCC_RESOURCE_TYPES.join(", ")}`,
      [],
    );
  }
}

/**
 * Copy JSON contract data into frozen null-prototype maps.  This keeps names
 * such as `__proto__` and `constructor` as ordinary own keys and prevents a
 * caller from changing the catalog after it has passed the semantic gate.
 */
export function immutableTransformContractCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(
      value.map((item) => immutableTransformContractCopy(item)),
    );
  }
  if (isJsonRecord(value)) {
    const output: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      output[key] = immutableTransformContractCopy(value[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

function validatedCatalog(candidate: unknown): TransformCatalog {
  if (!validateTransformCatalog(candidate)) {
    invalidCatalog("transform catalog does not match schema version 1");
  }
  const catalog = candidate as TransformCatalog;
  assertZccCatalogInvariants(catalog);
  return immutableTransformContractCopy(catalog) as TransformCatalog;
}

// Capture the committed data once.  Runtime consumers never discover pack,
// schema, or override files, so a standalone bundle has the same semantics as
// a checkout and there is no mutable filesystem control plane.
const EMBEDDED_ZCC_TRANSFORM_CATALOG = validatedCatalog(embeddedCatalogJson);

/** Return the validated, deeply immutable transform catalog in the bundle. */
export function loadZccTransformCatalog(): TransformCatalog {
  return EMBEDDED_ZCC_TRANSFORM_CATALOG;
}

/**
 * Validate an externally supplied value and require exact semantic equality
 * with the committed catalog.  On success the canonical immutable snapshot is
 * returned rather than the caller-owned object.
 */
export function requireSupportedZccTransformCatalog(
  candidate: unknown,
): TransformCatalog {
  const catalog = validatedCatalog(candidate);
  if (!pythonJsonEqual(catalog, EMBEDDED_ZCC_TRANSFORM_CATALOG)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_TRANSFORM_CATALOG",
      category: "domain",
      message: "transform requires the supported embedded ZCC catalog",
    });
  }
  return EMBEDDED_ZCC_TRANSFORM_CATALOG;
}
