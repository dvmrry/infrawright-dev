import {
  createHash,
  createHmac,
  createSecretKey,
  type KeyObject,
} from "node:crypto";
import { types as utilTypes } from "node:util";

import { LosslessNumber } from "lossless-json";

import { isJsonRecord } from "../json/python-equality.js";
import { sortedStrings } from "../json/python-compatible.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import { ZCC_ADOPTION_CATALOG_SHA256 } from "./zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "./zcc-adoption-catalog.js";
import { ProcessFailure } from "./errors.js";

const MAX_PARITY_GRAPH_DEPTH = 128;
const SHA256_PATTERN = /^[0-9a-f]{64}$/;
const HMAC_CONTEXT = "infrawright.zcc-adoption-oracle-parity";

export const ZCC_ADOPTION_PARITY_RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

const COMPARISON_ROLES = [
  "identity",
  "observation",
  "projection",
  "tfvars",
  "imports",
  "lookup",
] as const;

type ZccAdoptionParityResourceType =
  typeof ZCC_ADOPTION_PARITY_RESOURCE_TYPES[number];
type ZccAdoptionParityRole = typeof COMPARISON_ROLES[number];

export type ZccAdoptionParityEvidenceClass =
  | "simulation"
  | "live_shared_observation"
  | "live_independent_executor";

/**
 * One in-memory executor result. Values and bytes are committed immediately
 * and never retained in or copied into the returned report.
 */
export interface ZccAdoptionParitySnapshotInput {
  readonly survivors: unknown;
  readonly observations: unknown;
  readonly projection: unknown;
  readonly artifacts: {
    readonly tfvars: Uint8Array;
    readonly imports: Uint8Array;
    readonly lookup: Uint8Array | null;
  };
}

export interface ZccAdoptionParityResourceInput {
  readonly resource_type: ZccAdoptionParityResourceType;
  readonly python_before: ZccAdoptionParitySnapshotInput;
  readonly node: ZccAdoptionParitySnapshotInput;
  readonly python_after: ZccAdoptionParitySnapshotInput | null;
}

export interface BuildZccAdoptionOracleParityOptions {
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  /**
   * A fresh, process-held 32-byte key used only for this report. A future
   * public operation must generate this key internally rather than accept it
   * across the public request boundary.
   */
  readonly commitmentKey: Uint8Array;
  /**
   * Public, reproducible build bindings; no tenant-derived bytes belong here.
   * A future public operation must derive these from its own executors.
   */
  readonly builds: {
    readonly python_before_sha256: string;
    readonly python_after_sha256: string | null;
    readonly node_sha256: string;
  };
  readonly resources: readonly ZccAdoptionParityResourceInput[];
}

export interface ZccAdoptionApplicableComparison {
  readonly python_before_hmac_sha256: string;
  readonly node_hmac_sha256: string;
  readonly python_after_hmac_sha256: string | null;
  readonly status: "match" | "mismatch" | "unstable_reference";
}

export interface ZccAdoptionNotApplicableComparison {
  readonly python_before_hmac_sha256: null;
  readonly node_hmac_sha256: null;
  readonly python_after_hmac_sha256: null;
  readonly status: "not_applicable";
}

export interface ZccAdoptionParityResourceReport {
  readonly resource_type: ZccAdoptionParityResourceType;
  readonly status: "equal" | "different" | "unstable_reference";
  readonly comparisons: {
    readonly identity: ZccAdoptionApplicableComparison;
    readonly observation: ZccAdoptionApplicableComparison;
    readonly projection: ZccAdoptionApplicableComparison;
    readonly tfvars: ZccAdoptionApplicableComparison;
    readonly imports: ZccAdoptionApplicableComparison;
    readonly lookup:
      | ZccAdoptionApplicableComparison
      | ZccAdoptionNotApplicableComparison;
  };
}

export interface ZccAdoptionOracleParityReport {
  readonly kind: "infrawright.zcc_adoption_oracle_parity";
  readonly schema_version: 1;
  readonly product: "zcc";
  readonly evidence_class: ZccAdoptionParityEvidenceClass;
  readonly bindings: {
    readonly catalog: {
      readonly kind: "infrawright.adoption_catalog";
      readonly schema_version: 1;
      readonly sha256: string;
      readonly sources_sha256: string;
    };
    readonly builds: {
      readonly python_before_sha256: string;
      readonly python_after_sha256: string | null;
      readonly node_sha256: string;
      readonly python_stability: "not_applicable" | "stable" | "unstable";
    };
  };
  readonly resources: readonly ZccAdoptionParityResourceReport[];
  /**
   * Deliberately omits cutover qualification. Only a later aggregator that
   * also verifies the downstream saved-plan/adoption gate may make that claim.
   */
  readonly summary: {
    readonly status: "equal" | "different" | "unstable_reference";
    /**
     * One aggregate bit replaces per-resource presence disclosure. It is
     * meaningful only for live evidence and is re-derived by the trusted
     * producer from equal, positive survivor/observation cardinalities.
     */
    readonly live_input_coverage:
      | "not_applicable"
      | "complete"
      | "incomplete";
    readonly total_roles: 30;
    readonly applicable: 26;
    readonly matched: number;
    readonly mismatched: number;
    readonly unstable_reference: number;
    readonly not_applicable: 4;
    /** V1 is comparison-only until a host-bound successor contract exists. */
    readonly projection_qualification: "not_qualified";
    readonly executor_qualification: "not_qualified";
  };
  /**
   * Plain SHA-256 of the complete already-redacted report body. This is an
   * integrity checksum, not evidence or producer authentication.
   */
  readonly report_sha256: string;
}

interface CommittedSnapshot {
  readonly inputPresent: boolean;
  readonly commitments: Readonly<
    Record<ZccAdoptionParityRole, string | null>
  >;
}

interface CommittedResource {
  readonly inputPresent: boolean;
  readonly report: ZccAdoptionParityResourceReport;
}

type JsonRecord = Readonly<Record<string, unknown>>;

function invalidInput(): never {
  throw new ProcessFailure({
    code: "INVALID_ZCC_ADOPTION_PARITY_INPUT",
    category: "domain",
    message: "ZCC adoption parity input violates the closed commitment contract",
  });
}

function hasExactKeys(
  value: JsonRecord,
  keys: readonly string[],
): boolean {
  const actual = sortedStrings(Object.keys(value));
  const expected = sortedStrings(keys);
  return actual.length === expected.length
    && actual.every((key, index) => key === expected[index]);
}

function inertInputRecord(
  value: unknown,
  keys: readonly string[],
): JsonRecord {
  if (
    typeof value !== "object"
    || value === null
    || Array.isArray(value)
    || value instanceof LosslessNumber
    || utilTypes.isProxy(value)
  ) {
    return invalidInput();
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return invalidInput();
  }
  const ownKeys = Reflect.ownKeys(value);
  if (ownKeys.some((key) => typeof key !== "string")) {
    return invalidInput();
  }
  const record = value as JsonRecord;
  if (!hasExactKeys(record, keys)) {
    return invalidInput();
  }
  for (const key of ownKeys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
      || descriptor.get !== undefined
      || descriptor.set !== undefined
    ) {
      return invalidInput();
    }
  }
  return record;
}

function inertInputArray(value: unknown): readonly unknown[] {
  if (
    !Array.isArray(value)
    || utilTypes.isProxy(value)
    || Object.getPrototypeOf(value) !== Array.prototype
  ) {
    return invalidInput();
  }
  const ownKeys = Reflect.ownKeys(value);
  if (
    ownKeys.length !== value.length + 1
    || !ownKeys.includes("length")
  ) {
    return invalidInput();
  }
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return invalidInput();
    }
  }
  return value;
}

function snapshotValue(value: unknown): unknown {
  try {
    const snapshot = snapshotPlainJsonGraph(value, {
      maxDepth: MAX_PARITY_GRAPH_DEPTH,
    });
    if (snapshot.ok) {
      return snapshot.value;
    }
  } catch {
    // Input values and thrown diagnostics are secret-bearing. Collapse every
    // graph failure to the fixed public error above.
  }
  return invalidInput();
}

function snapshotCollection(value: unknown): {
  readonly value: unknown;
  readonly count: number;
} {
  const snapshot = snapshotValue(value);
  if (Array.isArray(snapshot)) {
    return { value: snapshot, count: snapshot.length };
  }
  if (isJsonRecord(snapshot) && !(snapshot instanceof LosslessNumber)) {
    return { value: snapshot, count: Object.keys(snapshot).length };
  }
  return invalidInput();
}

function canonicalJson(value: unknown): string {
  if (value === null || typeof value === "boolean") {
    return value === null ? "null" : value ? "true" : "false";
  }
  if (typeof value === "string") {
    const encoded = JSON.stringify(value);
    return encoded === undefined ? invalidInput() : encoded;
  }
  if (value instanceof LosslessNumber) {
    return value.toString();
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return invalidInput();
    }
    if (Object.is(value, -0)) {
      return "-0";
    }
    const encoded = JSON.stringify(value);
    return encoded === undefined ? invalidInput() : encoded;
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => canonicalJson(entry)).join(",")}]`;
  }
  if (isJsonRecord(value)) {
    return `{${sortedStrings(Object.keys(value)).map((key) => {
      const child = value[key];
      if (child === undefined) {
        return invalidInput();
      }
      return `${canonicalJson(key)}:${canonicalJson(child)}`;
    }).join(",")}}`;
  }
  return invalidInput();
}

function standardUint8Array(value: unknown): Uint8Array {
  if (
    typeof value !== "object"
    || value === null
    || utilTypes.isProxy(value)
    || !utilTypes.isUint8Array(value)
  ) {
    return invalidInput();
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Uint8Array.prototype && prototype !== Buffer.prototype) {
    return invalidInput();
  }
  const bytes = value as Uint8Array;
  if (utilTypes.isSharedArrayBuffer(bytes.buffer)) {
    return invalidInput();
  }
  return bytes;
}

function copyBytes(value: unknown): Buffer {
  const bytes = standardUint8Array(value);
  const copy = Buffer.alloc(bytes.byteLength);
  copy.set(bytes);
  return copy;
}

function commitmentKey(value: unknown): KeyObject {
  const bytes = standardUint8Array(value);
  if (bytes.byteLength !== 32) {
    return invalidInput();
  }
  const copy = Buffer.alloc(32);
  copy.set(bytes);
  try {
    return createSecretKey(copy);
  } finally {
    copy.fill(0);
  }
}

function commitmentDomain(options: {
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly resourceType: ZccAdoptionParityResourceType;
  readonly role: ZccAdoptionParityRole;
  readonly media: "bytes" | "value";
}): Buffer {
  return Buffer.from(
    `${HMAC_CONTEXT}\u0000v1\u0000${options.evidenceClass}`
      + `\u0000${options.resourceType}\u0000${options.role}\u0000${options.media}`,
    "utf8",
  );
}

function hmacCommitment(options: {
  readonly key: KeyObject;
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly resourceType: ZccAdoptionParityResourceType;
  readonly role: ZccAdoptionParityRole;
  readonly media: "bytes" | "value";
  readonly payload: Buffer;
}): string {
  return createHmac("sha256", options.key)
    .update(commitmentDomain(options))
    .update(Buffer.of(0))
    .update(options.payload)
    .digest("hex");
}

function commitValue(options: {
  readonly key: KeyObject;
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly resourceType: ZccAdoptionParityResourceType;
  readonly role: ZccAdoptionParityRole;
  readonly value: unknown;
}): string {
  const payload = Buffer.from(canonicalJson(options.value), "utf8");
  try {
    return hmacCommitment({ ...options, media: "value", payload });
  } finally {
    payload.fill(0);
  }
}

function commitBytes(options: {
  readonly key: KeyObject;
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly resourceType: ZccAdoptionParityResourceType;
  readonly role: ZccAdoptionParityRole;
  readonly value: unknown;
}): string {
  const payload = copyBytes(options.value);
  try {
    return hmacCommitment({ ...options, media: "bytes", payload });
  } finally {
    payload.fill(0);
  }
}

function committedSnapshot(options: {
  readonly key: KeyObject;
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly resourceType: ZccAdoptionParityResourceType;
  readonly input: unknown;
}): CommittedSnapshot {
  const input = inertInputRecord(options.input, [
    "survivors",
    "observations",
    "projection",
    "artifacts",
  ]);
  const artifacts = inertInputRecord(input.artifacts, [
    "tfvars",
    "imports",
    "lookup",
  ]);
  const survivors = snapshotCollection(input.survivors);
  const observations = snapshotCollection(input.observations);
  if (survivors.count !== observations.count) {
    return invalidInput();
  }
  const projection = snapshotValue(input.projection);
  const lookup = artifacts.lookup === null
    ? null
    : commitBytes({
        ...options,
        role: "lookup",
        value: artifacts.lookup,
      });
  return {
    inputPresent: survivors.count > 0,
    commitments: {
      identity: commitValue({
        ...options,
        role: "identity",
        value: survivors.value,
      }),
      observation: commitValue({
        ...options,
        role: "observation",
        value: observations.value,
      }),
      projection: commitValue({
        ...options,
        role: "projection",
        value: projection,
      }),
      tfvars: commitBytes({
        ...options,
        role: "tfvars",
        value: artifacts.tfvars,
      }),
      imports: commitBytes({
        ...options,
        role: "imports",
        value: artifacts.imports,
      }),
      lookup,
    },
  };
}

function applicableComparison(options: {
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly before: string;
  readonly node: string;
  readonly after: string | null;
}): ZccAdoptionApplicableComparison {
  if (options.evidenceClass === "live_independent_executor") {
    if (options.after === null) {
      return invalidInput();
    }
    return {
      python_before_hmac_sha256: options.before,
      node_hmac_sha256: options.node,
      python_after_hmac_sha256: options.after,
      status: options.before !== options.after
        ? "unstable_reference"
        : options.before === options.node
          ? "match"
          : "mismatch",
    };
  }
  if (options.after !== null) {
    return invalidInput();
  }
  return {
    python_before_hmac_sha256: options.before,
    node_hmac_sha256: options.node,
    python_after_hmac_sha256: null,
    status: options.before === options.node ? "match" : "mismatch",
  };
}

function notApplicableComparison(): ZccAdoptionNotApplicableComparison {
  return {
    python_before_hmac_sha256: null,
    node_hmac_sha256: null,
    python_after_hmac_sha256: null,
    status: "not_applicable",
  };
}

function resourceReport(options: {
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly key: KeyObject;
  readonly input: unknown;
}): CommittedResource {
  const input = inertInputRecord(options.input, [
    "resource_type",
    "python_before",
    "node",
    "python_after",
  ]);
  if (
    typeof input.resource_type !== "string"
    || !(ZCC_ADOPTION_PARITY_RESOURCE_TYPES as readonly string[]).includes(
      input.resource_type,
    )
  ) {
    return invalidInput();
  }
  const resourceType = input.resource_type as ZccAdoptionParityResourceType;
  const before = committedSnapshot({
    ...options,
    resourceType,
    input: input.python_before,
  });
  const node = committedSnapshot({
    ...options,
    resourceType,
    input: input.node,
  });
  const after = input.python_after === null
    ? null
    : committedSnapshot({
        ...options,
        resourceType,
        input: input.python_after,
      });
  if (
    (options.evidenceClass === "live_independent_executor") !== (after !== null)
  ) {
    return invalidInput();
  }
  const lookupApplicable = resourceType === "zcc_trusted_network";
  for (const snapshot of [before, node, ...(after === null ? [] : [after])]) {
    if ((snapshot.commitments.lookup !== null) !== lookupApplicable) {
      return invalidInput();
    }
  }
  const comparisonFor = (
    role: Exclude<ZccAdoptionParityRole, "lookup">,
  ): ZccAdoptionApplicableComparison => {
    const beforeValue = before.commitments[role];
    const nodeValue = node.commitments[role];
    const afterValue = after?.commitments[role] ?? null;
    if (beforeValue === null || nodeValue === null) {
      return invalidInput();
    }
    return applicableComparison({
      evidenceClass: options.evidenceClass,
      before: beforeValue,
      node: nodeValue,
      after: afterValue,
    });
  };
  const lookup = lookupApplicable
    ? applicableComparison({
        evidenceClass: options.evidenceClass,
        before: before.commitments.lookup ?? invalidInput(),
        node: node.commitments.lookup ?? invalidInput(),
        after: after?.commitments.lookup ?? null,
      })
    : notApplicableComparison();
  const comparisons = {
    identity: comparisonFor("identity"),
    observation: comparisonFor("observation"),
    projection: comparisonFor("projection"),
    tfvars: comparisonFor("tfvars"),
    imports: comparisonFor("imports"),
    lookup,
  };
  const statuses = Object.values(comparisons).map((entry) => entry.status);
  return {
    inputPresent: before.inputPresent
      && node.inputPresent
      && (after?.inputPresent ?? true),
    report: {
      resource_type: resourceType,
      status: statuses.includes("unstable_reference")
        ? "unstable_reference"
        : statuses.includes("mismatch")
          ? "different"
          : "equal",
      comparisons,
    },
  };
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableCopy(entry)));
  }
  if (isJsonRecord(value)) {
    const output: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy(value[key]);
    }
    return Object.freeze(output);
  }
  return value;
}

function reportDigest(body: unknown): string {
  return createHash("sha256")
    .update(canonicalJson(body), "utf8")
    .digest("hex");
}

function publicSha(value: unknown): string {
  return typeof value === "string" && SHA256_PATTERN.test(value)
    ? value
    : invalidInput();
}

function buildBindings(
  evidenceClass: ZccAdoptionParityEvidenceClass,
  value: unknown,
): ZccAdoptionOracleParityReport["bindings"]["builds"] {
  const builds = inertInputRecord(value, [
    "python_before_sha256",
    "python_after_sha256",
    "node_sha256",
  ]);
  const before = publicSha(builds.python_before_sha256);
  const node = publicSha(builds.node_sha256);
  if (evidenceClass === "live_independent_executor") {
    const after = publicSha(builds.python_after_sha256);
    return {
      python_before_sha256: before,
      python_after_sha256: after,
      node_sha256: node,
      python_stability: before === after ? "stable" : "unstable",
    };
  }
  if (builds.python_after_sha256 !== null) {
    return invalidInput();
  }
  return {
    python_before_sha256: before,
    python_after_sha256: null,
    node_sha256: node,
    python_stability: "not_applicable",
  };
}

/**
 * Build a secret-safe comparison over the exact five-resource ZCC parity
 * scope. The key and every tenant-derived input remain process-owned and never
 * enter the report or an error diagnostic. The resulting digest and semantic
 * validation are integrity checks inside a trusted process or authenticated
 * CI artifact channel; they do not authenticate who produced the evidence.
 */
export function buildZccAdoptionOracleParity(
  options: BuildZccAdoptionOracleParityOptions,
): ZccAdoptionOracleParityReport {
  const input = inertInputRecord(options, [
    "evidenceClass",
    "commitmentKey",
    "builds",
    "resources",
  ]);
  const evidenceClass = input.evidenceClass;
  if (
    evidenceClass !== "simulation"
    && evidenceClass !== "live_shared_observation"
    && evidenceClass !== "live_independent_executor"
  ) {
    return invalidInput();
  }
  const resourcesInput = inertInputArray(input.resources);
  if (resourcesInput.length !== ZCC_ADOPTION_PARITY_RESOURCE_TYPES.length) {
    return invalidInput();
  }
  const keyed = commitmentKey(input.commitmentKey);
  const reportsByType = new Map<
    ZccAdoptionParityResourceType,
    CommittedResource
  >();
  for (const resource of resourcesInput) {
    const report = resourceReport({
      evidenceClass,
      key: keyed,
      input: resource,
    });
    if (reportsByType.has(report.report.resource_type)) {
      return invalidInput();
    }
    reportsByType.set(report.report.resource_type, report);
  }
  const committedResources = ZCC_ADOPTION_PARITY_RESOURCE_TYPES.map(
    (resourceType) => reportsByType.get(resourceType) ?? invalidInput(),
  );
  const resources = committedResources.map((resource) => resource.report);
  const builds = buildBindings(evidenceClass, input.builds);
  const applicable = resources.flatMap((resource) => {
    return Object.values(resource.comparisons).filter((comparison) => {
      return comparison.status !== "not_applicable";
    });
  });
  const matched = applicable.filter((entry) => entry.status === "match").length;
  const mismatched = applicable.filter(
    (entry) => entry.status === "mismatch",
  ).length;
  const unstable = applicable.filter(
    (entry) => entry.status === "unstable_reference",
  ).length;
  const allLiveInputsPresent = committedResources.every((resource) => {
    return resource.inputPresent;
  });
  const catalog = loadZccAdoptionCatalog();
  const body = {
    kind: "infrawright.zcc_adoption_oracle_parity" as const,
    schema_version: 1 as const,
    product: "zcc" as const,
    evidence_class: evidenceClass,
    bindings: {
      catalog: {
        kind: catalog.kind,
        schema_version: catalog.schema_version,
        sha256: ZCC_ADOPTION_CATALOG_SHA256,
        sources_sha256: catalog.sources_sha256,
      },
      builds,
    },
    resources,
    summary: {
      status: unstable > 0 || builds.python_stability === "unstable"
        ? "unstable_reference" as const
        : mismatched > 0
          ? "different" as const
          : "equal" as const,
      live_input_coverage: evidenceClass === "simulation"
        ? "not_applicable" as const
        : allLiveInputsPresent
          ? "complete" as const
          : "incomplete" as const,
      total_roles: 30 as const,
      applicable: 26 as const,
      matched,
      mismatched,
      unstable_reference: unstable,
      not_applicable: 4 as const,
      // The inputs and build hashes are caller assertions at this private
      // seam. V1 records comparisons but never upgrades them to qualification.
      projection_qualification: "not_qualified" as const,
      executor_qualification: "not_qualified" as const,
    },
  };
  const report = {
    ...body,
    report_sha256: reportDigest(body),
  };
  if (!validateZccAdoptionOracleParityReport(report)) {
    return invalidInput();
  }
  return immutableCopy(report) as ZccAdoptionOracleParityReport;
}

function exactReportRecord(
  value: unknown,
  keys: readonly string[],
): JsonRecord | null {
  if (!isJsonRecord(value) || value instanceof LosslessNumber) {
    return null;
  }
  return hasExactKeys(value, keys) ? value : null;
}

function semanticComparison(options: {
  readonly value: unknown;
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly applicable: boolean;
}): "match" | "mismatch" | "unstable_reference" | "not_applicable" | null {
  const comparison = exactReportRecord(options.value, [
    "python_before_hmac_sha256",
    "node_hmac_sha256",
    "python_after_hmac_sha256",
    "status",
  ]);
  if (comparison === null) {
    return null;
  }
  if (!options.applicable) {
    return comparison.python_before_hmac_sha256 === null
      && comparison.node_hmac_sha256 === null
      && comparison.python_after_hmac_sha256 === null
      && comparison.status === "not_applicable"
      ? "not_applicable"
      : null;
  }
  const before = comparison.python_before_hmac_sha256;
  const node = comparison.node_hmac_sha256;
  const after = comparison.python_after_hmac_sha256;
  if (
    typeof before !== "string"
    || !SHA256_PATTERN.test(before)
    || typeof node !== "string"
    || !SHA256_PATTERN.test(node)
  ) {
    return null;
  }
  let expected: "match" | "mismatch" | "unstable_reference";
  if (options.evidenceClass === "live_independent_executor") {
    if (typeof after !== "string" || !SHA256_PATTERN.test(after)) {
      return null;
    }
    expected = before !== after
      ? "unstable_reference"
      : before === node
        ? "match"
        : "mismatch";
  } else {
    if (after !== null) {
      return null;
    }
    expected = before === node ? "match" : "mismatch";
  }
  return comparison.status === expected ? expected : null;
}

/**
 * Validate both the closed report shape and every redundant semantic claim.
 * This function needs no tenant data or commitment key. It detects malformed
 * or accidentally corrupted reports; it does not authenticate evidence or its
 * producer and therefore belongs inside the trusted report channel.
 */
export function validateZccAdoptionOracleParityReport(
  candidate: unknown,
): candidate is ZccAdoptionOracleParityReport {
  try {
    const snapshotResult = snapshotPlainJsonGraph(candidate, {
      maxDepth: MAX_PARITY_GRAPH_DEPTH,
    });
    if (!snapshotResult.ok) {
      return false;
    }
    const report = exactReportRecord(snapshotResult.value, [
      "kind",
      "schema_version",
      "product",
      "evidence_class",
      "bindings",
      "resources",
      "summary",
      "report_sha256",
    ]);
    if (
      report === null
      || report.kind !== "infrawright.zcc_adoption_oracle_parity"
      || report.schema_version !== 1
      || report.product !== "zcc"
      || (
        report.evidence_class !== "simulation"
        && report.evidence_class !== "live_shared_observation"
        && report.evidence_class !== "live_independent_executor"
      )
      || typeof report.report_sha256 !== "string"
      || !SHA256_PATTERN.test(report.report_sha256)
    ) {
      return false;
    }
    const evidenceClass = report.evidence_class;
    const bindings = exactReportRecord(report.bindings, ["catalog", "builds"]);
    const catalog = exactReportRecord(bindings?.catalog, [
      "kind",
      "schema_version",
      "sha256",
      "sources_sha256",
    ]);
    const builds = exactReportRecord(bindings?.builds, [
      "python_before_sha256",
      "python_after_sha256",
      "node_sha256",
      "python_stability",
    ]);
    const supportedCatalog = loadZccAdoptionCatalog();
    if (
      bindings === null
      || catalog === null
      || builds === null
      || catalog.kind !== "infrawright.adoption_catalog"
      || catalog.schema_version !== 1
      || catalog.sha256 !== ZCC_ADOPTION_CATALOG_SHA256
      || catalog.sources_sha256 !== supportedCatalog.sources_sha256
      || typeof builds.python_before_sha256 !== "string"
      || !SHA256_PATTERN.test(builds.python_before_sha256)
      || typeof builds.node_sha256 !== "string"
      || !SHA256_PATTERN.test(builds.node_sha256)
    ) {
      return false;
    }
    let buildStable = true;
    if (evidenceClass === "live_independent_executor") {
      if (
        typeof builds.python_after_sha256 !== "string"
        || !SHA256_PATTERN.test(builds.python_after_sha256)
      ) {
        return false;
      }
      buildStable = builds.python_before_sha256 === builds.python_after_sha256;
      if (
        builds.python_stability !== (buildStable ? "stable" : "unstable")
      ) {
        return false;
      }
    } else if (
      builds.python_after_sha256 !== null
      || builds.python_stability !== "not_applicable"
    ) {
      return false;
    }
    if (!Array.isArray(report.resources) || report.resources.length !== 5) {
      return false;
    }
    let matched = 0;
    let mismatched = 0;
    let unstable = 0;
    let notApplicable = 0;
    for (let index = 0; index < report.resources.length; index += 1) {
      const resource = exactReportRecord(report.resources[index], [
        "resource_type",
        "status",
        "comparisons",
      ]);
      const expectedResource = ZCC_ADOPTION_PARITY_RESOURCE_TYPES[index];
      if (
        resource === null
        || resource.resource_type !== expectedResource
      ) {
        return false;
      }
      const comparisons = exactReportRecord(resource.comparisons, COMPARISON_ROLES);
      if (comparisons === null) {
        return false;
      }
      const statuses: string[] = [];
      for (const role of COMPARISON_ROLES) {
        const status = semanticComparison({
          value: comparisons[role],
          evidenceClass,
          applicable: role !== "lookup"
            || expectedResource === "zcc_trusted_network",
        });
        if (status === null) {
          return false;
        }
        statuses.push(status);
        if (status === "match") {
          matched += 1;
        } else if (status === "mismatch") {
          mismatched += 1;
        } else if (status === "unstable_reference") {
          unstable += 1;
        } else {
          notApplicable += 1;
        }
      }
      const expectedStatus = statuses.includes("unstable_reference")
        ? "unstable_reference"
        : statuses.includes("mismatch")
          ? "different"
          : "equal";
      if (resource.status !== expectedStatus) {
        return false;
      }
    }
    const summary = exactReportRecord(report.summary, [
      "status",
      "live_input_coverage",
      "total_roles",
      "applicable",
      "matched",
      "mismatched",
      "unstable_reference",
      "not_applicable",
      "projection_qualification",
      "executor_qualification",
    ]);
    if (summary === null) {
      return false;
    }
    const expectedLiveInputCoverage = evidenceClass === "simulation"
      ? "not_applicable"
      : summary.live_input_coverage === "complete"
          || summary.live_input_coverage === "incomplete"
        ? summary.live_input_coverage
        : null;
    if (summary.live_input_coverage !== expectedLiveInputCoverage) {
      return false;
    }
    const expectedStatus = unstable > 0 || !buildStable
      ? "unstable_reference"
      : mismatched > 0
        ? "different"
        : "equal";
    if (
      summary.status !== expectedStatus
      || summary.total_roles !== 30
      || summary.applicable !== 26
      || summary.matched !== matched
      || summary.mismatched !== mismatched
      || summary.unstable_reference !== unstable
      || summary.not_applicable !== notApplicable
      || notApplicable !== 4
      || matched + mismatched + unstable !== 26
      || summary.projection_qualification !== "not_qualified"
      || summary.executor_qualification !== "not_qualified"
    ) {
      return false;
    }
    const body: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of [
      "kind",
      "schema_version",
      "product",
      "evidence_class",
      "bindings",
      "resources",
      "summary",
    ]) {
      body[key] = report[key];
    }
    return reportDigest(body) === report.report_sha256;
  } catch {
    return false;
  }
}
