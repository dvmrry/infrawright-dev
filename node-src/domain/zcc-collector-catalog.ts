import embeddedCatalogJson from "../../catalogs/zcc-collector-catalog.v1.json" with { type: "json" };

import { isJsonRecord, pythonJsonEqual } from "../json/python-equality.js";
import { sameStringSequence, sortedStrings } from "../json/python-compatible.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import { ProcessFailure } from "./errors.js";

const RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

const SOURCE_FILES = [
  "engine/collectors/rest/__init__.py",
  "packs/_shared/zscaler/collector.py",
  "packs/zcc/collector.py",
  "packs/zcc/pack.json",
  "packs/zcc/registry.json",
] as const;

const SOURCES_SHA256 =
  "d4b8cbef8294e8cb7fd5b17b6efb120b5f8bdc09de8c10e506763547748b11fc";

const RESOURCE_CONTRACTS = {
  zcc_device_cleanup: {
    envelope: null,
    page_size: null,
    pagination: "single",
    path: "zcc/papi/public/v1/getDeviceCleanupInfo",
  },
  zcc_failopen_policy: {
    envelope: null,
    page_size: 1000,
    pagination: "zia",
    path: "zcc/papi/public/v1/webFailOpenPolicy/listByCompany",
  },
  zcc_forwarding_profile: {
    envelope: null,
    page_size: 1000,
    pagination: "zia",
    path: "zcc/papi/public/v1/webForwardingProfile/listByCompany",
  },
  zcc_trusted_network: {
    envelope: "trustedNetworkContracts",
    page_size: 1000,
    pagination: "zia",
    path: "zcc/papi/public/v1/webTrustedNetwork/listByCompany",
  },
  zcc_web_privacy: {
    envelope: null,
    page_size: null,
    pagination: "single",
    path: "zcc/papi/public/v1/getWebPrivacyInfo",
  },
} as const;

export type ZccCollectorResourceType = typeof RESOURCE_TYPES[number];

export interface ZccCollectorCatalogResource {
  readonly envelope: "trustedNetworkContracts" | null;
  readonly method: "GET";
  readonly page_size: 1000 | null;
  readonly pagination: "single" | "zia";
  readonly path: string;
  readonly type: ZccCollectorResourceType;
}

export interface ZccCollectorCatalog {
  readonly kind: "infrawright.zcc_collector_catalog";
  readonly oneapi: {
    readonly audience: "https://api.zscaler.com";
    readonly cloud_gateway_template: "https://api.{cloud}.zsapi.net";
    readonly cloud_token_host_template: "https://{vanity}.zslogin{cloud}.net";
    readonly mode: "oneapi";
    readonly production_gateway: "https://api.zsapi.net";
    readonly production_token_host_template: "https://{vanity}.zslogin.net";
    readonly token_path: "/oauth2/v1/token";
  };
  readonly product: "zcc";
  readonly provider: {
    readonly source: "zscaler/zcc";
    readonly version: "0.1.0-beta.1";
  };
  readonly resources: readonly ZccCollectorCatalogResource[];
  readonly schema_version: 1;
  readonly source_files: readonly string[];
  readonly sources_sha256: string;
}

function invalidCatalog(message: string): never {
  throw new ProcessFailure({
    category: "domain",
    code: "INVALID_ZCC_COLLECTOR_CATALOG",
    message,
  });
}

function exactKeys(value: object, expected: readonly string[]): boolean {
  return sameStringSequence(sortedStrings(Object.keys(value)), expected);
}

function exactStrings(value: unknown, expected: readonly string[]): boolean {
  return Array.isArray(value)
    && value.every((item) => typeof item === "string")
    && sameStringSequence(value, expected);
}

function validResource(
  value: unknown,
  expectedType: ZccCollectorResourceType,
): value is ZccCollectorCatalogResource {
  if (!isJsonRecord(value) || !exactKeys(value, [
    "envelope", "method", "page_size", "pagination", "path", "type",
  ])) {
    return false;
  }
  const expected = RESOURCE_CONTRACTS[expectedType];
  return value.type === expectedType
    && value.method === "GET"
    && value.path === expected.path
    && value.envelope === expected.envelope
    && value.pagination === expected.pagination
    && value.page_size === expected.page_size;
}

function validatedCatalog(candidate: unknown): ZccCollectorCatalog {
  const snapshot = snapshotPlainJsonGraph(candidate, { maxDepth: 32 });
  if (!snapshot.ok || !isJsonRecord(snapshot.value) || !exactKeys(snapshot.value, [
    "kind", "oneapi", "product", "provider", "resources", "schema_version",
    "source_files", "sources_sha256",
  ])) {
    return invalidCatalog("ZCC collector catalog has an invalid top-level shape");
  }
  const value = snapshot.value;
  const oneapi = value.oneapi;
  const provider = value.provider;
  if (
    value.kind !== "infrawright.zcc_collector_catalog"
    || value.schema_version !== 1
    || value.product !== "zcc"
    || !isJsonRecord(oneapi)
    || !exactKeys(oneapi, [
      "audience", "cloud_gateway_template", "cloud_token_host_template", "mode",
      "production_gateway", "production_token_host_template", "token_path",
    ])
    || oneapi.audience !== "https://api.zscaler.com"
    || oneapi.cloud_gateway_template !== "https://api.{cloud}.zsapi.net"
    || oneapi.cloud_token_host_template !== "https://{vanity}.zslogin{cloud}.net"
    || oneapi.mode !== "oneapi"
    || oneapi.production_gateway !== "https://api.zsapi.net"
    || oneapi.production_token_host_template !== "https://{vanity}.zslogin.net"
    || oneapi.token_path !== "/oauth2/v1/token"
    || !isJsonRecord(provider)
    || !exactKeys(provider, ["source", "version"])
    || provider.source !== "zscaler/zcc"
    || provider.version !== "0.1.0-beta.1"
    || !Array.isArray(value.resources)
    || value.resources.length !== RESOURCE_TYPES.length
    || !value.resources.every((resource, index) => {
      const expected = RESOURCE_TYPES[index];
      return expected !== undefined && validResource(resource, expected);
    })
    || !exactStrings(value.source_files, SOURCE_FILES)
    || value.sources_sha256 !== SOURCES_SHA256
  ) {
    return invalidCatalog("ZCC collector catalog is malformed");
  }
  return snapshot.value as unknown as ZccCollectorCatalog;
}

const EMBEDDED_CATALOG = validatedCatalog(embeddedCatalogJson);

/** Return the private, immutable, exact-five ZCC collector contract. */
export function loadZccCollectorCatalog(): ZccCollectorCatalog {
  return EMBEDDED_CATALOG;
}

/** Accept only a semantic copy of the catalog embedded in this build. */
export function requireSupportedZccCollectorCatalog(
  candidate: unknown,
): ZccCollectorCatalog {
  const catalog = validatedCatalog(candidate);
  if (!pythonJsonEqual(catalog, EMBEDDED_CATALOG)) {
    throw new ProcessFailure({
      category: "domain",
      code: "UNSUPPORTED_ZCC_COLLECTOR_CATALOG",
      message: "collector requires the supported embedded ZCC catalog",
    });
  }
  return EMBEDDED_CATALOG;
}

export function zccCollectorResource(
  resourceType: string,
): ZccCollectorCatalogResource {
  const resource = EMBEDDED_CATALOG.resources.find((entry) => {
    return entry.type === resourceType;
  });
  if (resource === undefined) {
    throw new ProcessFailure({
      category: "domain",
      code: "UNSUPPORTED_ZCC_COLLECTOR_RESOURCE",
      message: "collector supports only the reviewed ZCC resource cohort",
    });
  }
  return resource;
}
