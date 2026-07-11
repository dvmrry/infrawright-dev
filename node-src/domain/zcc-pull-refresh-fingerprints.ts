import { createHash } from "node:crypto";

import {
  sortedStrings,
  type JsonValue,
} from "../json/python-compatible.js";

const MAX_FINGERPRINT_JSON_DEPTH = 128;

function compactCanonicalJson(value: JsonValue): string {
  if (value === null || typeof value === "boolean") {
    return JSON.stringify(value);
  }
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || Object.is(value, -0)) {
      throw new TypeError("refresh fingerprints accept safe integers only");
    }
    return String(value);
  }
  if (typeof value === "string") {
    if (!value.isWellFormed()) {
      throw new TypeError("refresh fingerprints require well-formed Unicode");
    }
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => compactCanonicalJson(item)).join(",")}]`;
  }
  const objectValue = value as { readonly [key: string]: JsonValue };
  return `{${sortedStrings(Object.keys(objectValue)).map((key) => {
    const child = objectValue[key];
    if (child === undefined) {
      throw new TypeError("refresh fingerprint input is not JSON");
    }
    return `${compactCanonicalJson(key)}:${compactCanonicalJson(child)}`;
  }).join(",")}}`;
}

function snapshotJson(
  value: unknown,
  depth = 1,
  ancestors: Set<object> = new Set<object>(),
): JsonValue {
  if (
    value === null
    || typeof value === "boolean"
    || typeof value === "string"
    || typeof value === "number"
  ) {
    return value as JsonValue;
  }
  if (typeof value !== "object" || value === null) {
    throw new TypeError("refresh fingerprint input is not JSON");
  }
  if (depth > MAX_FINGERPRINT_JSON_DEPTH) {
    throw new TypeError("refresh fingerprint input exceeds the JSON depth limit");
  }
  if (ancestors.has(value)) {
    throw new TypeError("refresh fingerprint input must be acyclic JSON");
  }
  ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      return Array.from({ length: value.length }, (_, index) => {
        const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
        if (descriptor === undefined || !("value" in descriptor)) {
          throw new TypeError("refresh fingerprint input is not inert JSON");
        }
        return snapshotJson(descriptor.value, depth + 1, ancestors);
      });
    }
    const objectValue = record(value);
    if (Object.keys(objectValue).length === 0 && value !== objectValue) {
      throw new TypeError("refresh fingerprint input is not a JSON object");
    }
    const output: { [key: string]: JsonValue } = Object.create(null) as {
      [key: string]: JsonValue;
    };
    for (const key of Object.keys(objectValue)) {
      const descriptor = Object.getOwnPropertyDescriptor(objectValue, key);
      if (descriptor === undefined || !("value" in descriptor)) {
        throw new TypeError("refresh fingerprint input is not inert JSON");
      }
      output[key] = snapshotJson(descriptor.value, depth + 1, ancestors);
    }
    return output;
  } finally {
    ancestors.delete(value);
  }
}

function digest(value: unknown): string {
  return createHash("sha256")
    .update(compactCanonicalJson(snapshotJson(value)), "utf8")
    .digest("hex");
}

/** Hash one bounded, inert JSON value with the refresh canonical encoding. */
export function zccRefreshEvidenceDigest(value: unknown): string {
  return digest(value);
}

/** Bind the complete versioned refresh publication receipt for acknowledgement. */
export function zccPullRefreshPublicationReceiptSha(receipt: unknown): string {
  return zccRefreshEvidenceDigest({
    kind: "infrawright.zcc_pull_refresh_publication_receipt_digest",
    schema_version: 1,
    publication: receipt,
  });
}

/** Bind the raw two-phase parity invocation coordinates before filesystem I/O. */
export function zccPullRefreshParityRequestSha(options: {
  readonly context: {
    readonly workspace: string;
    readonly deployment: string;
    readonly root_catalog: string;
  };
  readonly tenant: string;
  readonly resourceType: string;
}): string {
  return digest({
    kind: "infrawright.zcc_pull_refresh_parity_request",
    schema_version: 1,
    context: options.context,
    tenant: options.tenant,
    resource_type: options.resourceType,
  });
}

function record(value: unknown): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return {};
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as Readonly<Record<string, unknown>>
    : {};
}

function ownValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
): unknown {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

function contentFreeDesiredState(value: unknown): unknown {
  const state = record(value);
  if (ownValue(state, "state") !== "present") {
    return {
      path: ownValue(state, "path"),
      state: ownValue(state, "state"),
    };
  }
  const artifact = record(ownValue(state, "artifact"));
  return {
    state: "present",
    artifact: {
      path: ownValue(artifact, "path"),
      media_type: ownValue(artifact, "media_type"),
      encoding: ownValue(artifact, "encoding"),
      sha256: ownValue(artifact, "sha256"),
      size_bytes: ownValue(artifact, "size_bytes"),
    },
  };
}

function contentFreeDesired(value: unknown): unknown {
  const desired = record(value);
  return {
    tfvars: contentFreeDesiredState(ownValue(desired, "tfvars")),
    imports: contentFreeDesiredState(ownValue(desired, "imports")),
    lookup: contentFreeDesiredState(ownValue(desired, "lookup")),
    moves: contentFreeDesiredState(ownValue(desired, "moves")),
  };
}

/** Hash all mutation targets and adjacent absence preconditions. */
export function zccRefreshBaselineFingerprint(value: {
  readonly tfvars: unknown;
  readonly imports: unknown;
  readonly lookup: unknown;
  readonly moves: unknown;
  readonly pending_moves: unknown;
  readonly alternate_hcl: unknown;
  readonly generated_bindings: unknown;
}): string {
  return digest({
    kind: "infrawright.zcc_refresh_baseline_fingerprint",
    schema_version: 1,
    states: {
      tfvars: value.tfvars,
      imports: value.imports,
      lookup: value.lookup,
      moves: value.moves,
      pending_moves: value.pending_moves,
      alternate_hcl: value.alternate_hcl,
      generated_bindings: value.generated_bindings,
    },
  });
}

/** Hash the complete deterministic refresh transition assertion. */
export function zccRefreshTransitionFingerprint(value: {
  readonly product: unknown;
  readonly resource_type: unknown;
  readonly tenant: unknown;
  readonly source: unknown;
  readonly catalog: unknown;
  readonly root: unknown;
  readonly baseline: unknown;
  readonly unexpected_drops: unknown;
  readonly moves: unknown;
  readonly desired: unknown;
}): string {
  return digest({
    kind: "infrawright.zcc_refresh_transition",
    schema_version: 1,
    product: value.product,
    resource_type: value.resource_type,
    tenant: value.tenant,
    source: value.source,
    catalog: value.catalog,
    root: value.root,
    baseline: value.baseline,
    unexpected_drops: value.unexpected_drops,
    moves: value.moves,
    desired: contentFreeDesired(value.desired),
  });
}
