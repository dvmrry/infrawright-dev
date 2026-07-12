/**
 * Neutral exact-five collection contract shared by the public parent and the
 * private OneAPI child. This module intentionally contains no endpoint,
 * credential, transport, or publication implementation.
 */
export const ZCC_COLLECTION_RESOURCE_TYPES = Object.freeze([
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const);

export type ZccCollectionResourceType =
  typeof ZCC_COLLECTION_RESOURCE_TYPES[number];

export const ZCC_COLLECTION_CATALOG_SOURCES_SHA256 =
  "d4b8cbef8294e8cb7fd5b17b6efb120b5f8bdc09de8c10e506763547748b11fc";

export const ZCC_COLLECTION_HOST_ENVIRONMENT_NAMES = Object.freeze([
  "ZSCALER_CLIENT_ID",
  "ZSCALER_CLIENT_SECRET",
  "ZSCALER_VANITY_DOMAIN",
  "ZSCALER_CLOUD",
  "HTTP_PROXY",
  "http_proxy",
  "HTTPS_PROXY",
  "https_proxy",
  "NO_PROXY",
  "no_proxy",
  "REQUESTS_CA_BUNDLE",
  "SSL_CERT_FILE",
] as const);

export interface ZccCollectedArtifact {
  readonly canonical_json: string;
  readonly metadata: {
    readonly catalog_sources_sha256: string;
    readonly data_requests: number;
    readonly encoding: "utf-8";
    readonly item_count: number;
    readonly kind: "infrawright.zcc_collected_pull";
    readonly media_type: "application/json";
    readonly product: "zcc";
    readonly resource_type: ZccCollectionResourceType;
    readonly schema_version: 1;
    readonly sha256: string;
    readonly size_bytes: number;
    readonly transport_attempts: number;
  };
}

export function isZccCollectionResourceType(
  value: unknown,
): value is ZccCollectionResourceType {
  return typeof value === "string"
    && ZCC_COLLECTION_RESOURCE_TYPES.some((candidate) => candidate === value);
}
