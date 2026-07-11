import embeddedCatalogJson from "../../catalogs/zcc-adoption-catalog.v1.json" with { type: "json" };

import {
  schemaErrorDetails,
  validateZccAdoptionCatalog,
} from "../contracts/validators.js";
import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";

export type ZccAdoptionPrimitiveEncoding = "bool" | "number" | "string";
export type ZccAdoptionValueEncoding =
  | ZccAdoptionPrimitiveEncoding
  | readonly ["list", ZccAdoptionPrimitiveEncoding];

export interface ZccAdoptionAttributeProjection {
  readonly encoding: ZccAdoptionValueEncoding;
  readonly provider_sensitive: boolean;
  readonly status: "required" | "optional";
}

export interface ZccAdoptionBlockProjection {
  readonly cardinality: "many" | "single";
  readonly nesting_mode: "list" | "set" | "single";
  readonly projection: ZccAdoptionProjection;
  readonly status: "required" | "optional";
}

export interface ZccAdoptionProjection {
  readonly attributes: Readonly<
    Record<string, ZccAdoptionAttributeProjection>
  >;
  readonly blocks: Readonly<Record<string, ZccAdoptionBlockProjection>>;
  readonly computed_only_attributes: readonly string[];
  readonly computed_only_blocks: readonly string[];
}

export type ZccAdoptionImportIdSegment =
  | { readonly literal: string }
  | { readonly field: string };

export interface ZccAdoptionIdentityContract {
  readonly constant_key: string | null;
  readonly identity_fields: Readonly<Record<string, string>>;
  readonly identity_renames: Readonly<Record<string, string>>;
  readonly import_id: {
    readonly segments: readonly ZccAdoptionImportIdSegment[];
    readonly template: string;
  };
  readonly key_fields: readonly string[];
  readonly skip_if: readonly Readonly<
    Record<string, null | boolean | number | string>
  >[];
  readonly skip_if_lte: readonly Readonly<Record<string, number>>[];
}

export interface ZccAdoptionCatalogResource {
  readonly identity: ZccAdoptionIdentityContract;
  readonly lookup_source: {
    readonly name_field: string;
  } | null;
  readonly projection: ZccAdoptionProjection;
  readonly type: string;
}

export interface ZccAdoptionCatalog {
  readonly kind: "infrawright.adoption_catalog";
  readonly schema_version: 1;
  readonly product: "zcc";
  readonly provider: {
    readonly name: "zcc";
    readonly source: string;
    readonly version: string;
  };
  readonly resources: readonly ZccAdoptionCatalogResource[];
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
  details = schemaErrorDetails(validateZccAdoptionCatalog.errors),
): never {
  throw new ProcessFailure({
    code: "INVALID_ZCC_ADOPTION_CATALOG",
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

function assertUnique(values: readonly string[], label: string): void {
  if (new Set(values).size !== values.length) {
    invalidCatalog(`${label} must be unique`, []);
  }
}

function assertProjectionInvariants(
  projection: ZccAdoptionProjection,
  label: string,
): void {
  assertSortedUnique(
    projection.computed_only_attributes,
    `${label}.computed_only_attributes`,
  );
  assertSortedUnique(
    projection.computed_only_blocks,
    `${label}.computed_only_blocks`,
  );
  const inputAttributes = new Set(Object.keys(projection.attributes));
  if (projection.computed_only_attributes.some((name) => inputAttributes.has(name))) {
    invalidCatalog(`${label} repeats an attribute in both projection classes`, []);
  }
  const inputBlocks = new Set(Object.keys(projection.blocks));
  if (projection.computed_only_blocks.some((name) => inputBlocks.has(name))) {
    invalidCatalog(`${label} repeats a block in both projection classes`, []);
  }
  for (const name of sortedStrings(Object.keys(projection.blocks))) {
    const block = projection.blocks[name];
    if (block !== undefined) {
      assertProjectionInvariants(block.projection, `${label}.blocks.${name}`);
    }
  }
}

function assertZccCatalogInvariants(catalog: ZccAdoptionCatalog): void {
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
  for (const resource of catalog.resources) {
    assertUnique(
      resource.identity.key_fields,
      `${resource.type}.identity.key_fields`,
    );
    assertProjectionInvariants(resource.projection, resource.type);
  }
}

function immutableContractCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableContractCopy(entry)));
  }
  if (isJsonRecord(value)) {
    const output: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      output[key] = immutableContractCopy(value[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

function validatedCatalog(candidate: unknown): ZccAdoptionCatalog {
  if (!validateZccAdoptionCatalog(candidate)) {
    invalidCatalog("ZCC adoption catalog does not match schema version 1");
  }
  const catalog = candidate as ZccAdoptionCatalog;
  assertZccCatalogInvariants(catalog);
  return immutableContractCopy(catalog) as ZccAdoptionCatalog;
}

const EMBEDDED_ZCC_ADOPTION_CATALOG = validatedCatalog(embeddedCatalogJson);

/** Return the validated, deeply immutable ZCC adoption contract. */
export function loadZccAdoptionCatalog(): ZccAdoptionCatalog {
  return EMBEDDED_ZCC_ADOPTION_CATALOG;
}

/** Accept only exact semantic copies of the catalog embedded in the bundle. */
export function requireSupportedZccAdoptionCatalog(
  candidate: unknown,
): ZccAdoptionCatalog {
  const catalog = validatedCatalog(candidate);
  if (!pythonJsonEqual(catalog, EMBEDDED_ZCC_ADOPTION_CATALOG)) {
    throw new ProcessFailure({
      code: "UNSUPPORTED_ZCC_ADOPTION_CATALOG",
      category: "domain",
      message: "adoption requires the supported embedded ZCC catalog",
    });
  }
  return EMBEDDED_ZCC_ADOPTION_CATALOG;
}
