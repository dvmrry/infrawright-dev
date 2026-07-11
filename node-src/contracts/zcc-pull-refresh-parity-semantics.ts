import type { ErrorObject } from "ajv/dist/2020.js";

import {
  zccPullRefreshParityRequestSha,
  zccRefreshEvidenceDigest,
} from "../domain/zcc-pull-refresh-fingerprints.js";
import {
  SUPPORTED_ZCC_ROOT_MEMBERS,
  SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS,
} from "../domain/zscaler-assessment.js";
import { sortedStrings } from "../json/python-compatible.js";

export const ZCC_PULL_REFRESH_PARITY_SEED_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-parity-seed-semantics";
export const ZCC_PULL_REFRESH_PARITY_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-parity-semantics";
export const ZCC_PULL_REFRESH_PARITY_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-parity-request-semantics";

type JsonRecord = Record<string, unknown>;
const ROLES = [
  "tfvars", "imports", "lookup", "moves", "pending_moves",
  "alternate_hcl", "generated_bindings",
] as const;
const TRUSTED_NETWORK = "zcc_trusted_network";
const MAX_MOVE_CANDIDATES = 50_000;
const CATALOG_SHA256 =
  "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a";
const CATALOG_SOURCES_SHA256 =
  "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11";

function record(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as JsonRecord
    : null;
}

function own(value: JsonRecord | null, key: string): unknown {
  return value === null ? undefined : Object.getOwnPropertyDescriptor(value, key)?.value;
}

function semanticError(
  keyword: string,
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${keyword}`,
    keyword,
    params: { rule },
    message,
  };
}

function safeDigest(value: unknown): string | null {
  try {
    return zccRefreshEvidenceDigest(value);
  } catch {
    return null;
  }
}

function same(left: unknown, right: unknown): boolean {
  const leftDigest = safeDigest(left);
  return leftDigest !== null && leftDigest === safeDigest(right);
}

function neutralFields(value: JsonRecord): JsonRecord {
  return {
    source: own(value, "source"),
    catalog: own(value, "catalog"),
    root: own(value, "root"),
    baseline: own(value, "baseline"),
    desired: own(value, "desired"),
    status: own(value, "status"),
    unexpected_drops: own(value, "unexpected_drops"),
    moves: own(value, "moves"),
    decision_sha256: own(value, "decision_sha256"),
  };
}

function evidenceDigest(value: JsonRecord): string | null {
  return safeDigest({
    kind: "infrawright.zcc_pull_refresh_path_neutral_evidence",
    schema_version: 1,
    ...neutralFields(value),
  });
}

function expectedDifferences(
  candidate: JsonRecord,
  reference: JsonRecord,
  candidateBinding?: JsonRecord | null,
  referenceBinding?: JsonRecord | null,
): string[] {
  const output: string[] = [];
  for (const name of ["source", "catalog", "root", "status", "unexpected_drops"] as const) {
    if (!same(own(candidate, name), own(reference, name))) {
      output.push(name);
    }
  }
  const candidateBaseline = record(own(candidate, "baseline"));
  const referenceBaseline = record(own(reference, "baseline"));
  const candidateDesired = record(own(candidate, "desired"));
  const referenceDesired = record(own(reference, "desired"));
  for (const role of ROLES) {
    if (!same(own(candidateBaseline, role), own(referenceBaseline, role))) {
      output.push(`baseline.${role}`);
    }
    if (!same(own(candidateDesired, role), own(referenceDesired, role))) {
      output.push(`desired.${role}`);
    }
  }
  const candidateMoves = record(own(candidate, "moves"));
  const referenceMoves = record(own(reference, "moves"));
  for (const count of ["safe_count", "suppressed_count"] as const) {
    if (own(candidateMoves, count) !== own(referenceMoves, count)) {
      output.push(`moves.${count}`);
    }
  }
  if (own(candidate, "decision_sha256") !== own(reference, "decision_sha256")) {
    output.push("decision_sha256");
  }
  const candidateControls = record(own(candidateBinding ?? null, "controls"));
  const referenceControls = record(own(referenceBinding ?? null, "controls"));
  if (
    own(candidateBinding ?? null, "deployment_semantics_sha256")
    !== own(referenceBinding ?? null, "deployment_semantics_sha256")
  ) {
    output.push("controls.deployment");
  }
  if (!same(
    own(candidateControls, "root_catalog"),
    own(referenceControls, "root_catalog"),
  )) {
    output.push("controls.root_catalog");
  }
  return sortedStrings(new Set(output));
}

function seedWithoutDigest(seed: JsonRecord): JsonRecord {
  const output: JsonRecord = Object.create(null) as JsonRecord;
  for (const key of Object.keys(seed)) {
    if (key !== "seed_sha256") {
      output[key] = own(seed, key);
    }
  }
  return output;
}

function seedDigest(seed: JsonRecord): string | null {
  return safeDigest({
    kind: "infrawright.zcc_pull_refresh_parity_seed_digest",
    schema_version: 1,
    seed: seedWithoutDigest(seed),
  });
}

function stringList(value: unknown): readonly string[] | null {
  return Array.isArray(value) && value.every((item) => typeof item === "string")
    ? value
    : null;
}

function stateIs(value: unknown, expected: "present" | "absent"): boolean {
  return own(record(value), "state") === expected;
}

function validateEvidenceProfile(options: {
  readonly name: "candidate" | "reference_twin";
  readonly evidence: JsonRecord;
  readonly resourceType: string;
  readonly push: (path: string, rule: string, message: string) => void;
}): void {
  const prefix = `/${options.name}`;
  const evidence = options.evidence;
  const catalog = record(own(evidence, "catalog"));
  if (
    own(catalog, "kind") !== "infrawright.transform_catalog"
    || own(catalog, "schema_version") !== 1
    || own(catalog, "sha256") !== CATALOG_SHA256
    || own(catalog, "sources_sha256") !== CATALOG_SOURCES_SHA256
  ) {
    options.push(`${prefix}/catalog`, "catalog", "catalog evidence is not the exact bundled contract");
  }

  const root = record(own(evidence, "root"));
  const members = stringList(own(root, "members")) ?? [];
  const expectedMembers = sortedStrings(new Set(members));
  const label = own(root, "label");
  const expectedVariable = label === options.resourceType
    ? "items"
    : `${options.resourceType}_items`;
  if (
    !same(members, expectedMembers)
    || !members.includes(options.resourceType)
    || members.some((member) => !SUPPORTED_ZCC_ROOT_MEMBERS.includes(member))
    || own(root, "variable_name") !== expectedVariable
    || (
      typeof label === "string"
      && SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS.includes(label)
      && (members.length !== 1 || members[0] !== label)
    )
  ) {
    options.push(`${prefix}/root`, "root", "root evidence is outside the exact ZCC contract");
  }

  const drops = stringList(own(evidence, "unexpected_drops")) ?? [];
  if (!same(drops, sortedStrings(new Set(drops)))) {
    options.push(`${prefix}/unexpected_drops`, "drop_order", "unexpected drops must be sorted and unique");
  }
  const moves = record(own(evidence, "moves"));
  const safeCount = own(moves, "safe_count");
  const suppressedCount = own(moves, "suppressed_count");
  if (
    typeof safeCount !== "number"
    || typeof suppressedCount !== "number"
    || safeCount + suppressedCount > MAX_MOVE_CANDIDATES
  ) {
    options.push(`${prefix}/moves`, "move_bounds", "aggregate move evidence exceeds its bound");
  }
  const expectedStatus = drops.length === 0 && suppressedCount === 0
    ? "ready"
    : "review_required";
  if (own(evidence, "status") !== expectedStatus) {
    options.push(`${prefix}/status`, "status", "evidence status is inconsistent with drops and suppressions");
  }

  const baseline = record(own(evidence, "baseline"));
  const desired = record(own(evidence, "desired"));
  if (!stateIs(own(baseline, "tfvars"), "present")) {
    options.push(`${prefix}/baseline/tfvars`, "baseline_profile", "run-one tfvars must be present");
  }
  if (!stateIs(own(baseline, "imports"), "present")) {
    options.push(`${prefix}/baseline/imports`, "baseline_profile", "run-one imports must be present");
  }
  for (const role of ["moves", "pending_moves", "alternate_hcl", "generated_bindings"] as const) {
    if (!stateIs(own(baseline, role), "absent")) {
      options.push(`${prefix}/baseline/${role}`, "baseline_profile", "unsupported baseline role must be absent");
    }
  }
  const lookupState = options.resourceType === TRUSTED_NETWORK ? "present" : "absent";
  if (!stateIs(own(baseline, "lookup"), lookupState)) {
    options.push(`${prefix}/baseline/lookup`, "lookup_profile", "baseline lookup applicability is inconsistent");
  }

  if (!stateIs(own(desired, "tfvars"), "present")) {
    options.push(`${prefix}/desired/tfvars`, "desired_profile", "desired tfvars must be present");
  }
  if (!stateIs(own(desired, "imports"), "present")) {
    options.push(`${prefix}/desired/imports`, "desired_profile", "desired imports must be present");
  }
  if (!stateIs(own(desired, "lookup"), lookupState)) {
    options.push(`${prefix}/desired/lookup`, "lookup_profile", "desired lookup applicability is inconsistent");
  }
  for (const role of ["pending_moves", "alternate_hcl", "generated_bindings"] as const) {
    if (!stateIs(own(desired, role), "absent")) {
      options.push(`${prefix}/desired/${role}`, "desired_profile", "reserved desired role must be absent");
    }
  }
  const desiredMovesPresent = stateIs(own(desired, "moves"), "present");
  if (desiredMovesPresent !== (typeof safeCount === "number" && safeCount > 0)) {
    options.push(`${prefix}/desired/moves`, "move_presence", "move presence is inconsistent with safe_count");
  }
  for (const [role, mediaType] of [
    ["tfvars", "application/json"],
    ["imports", "text/x-hcl"],
    ["lookup", "application/json"],
    ["moves", "text/x-hcl"],
  ] as const) {
    const state = record(own(desired, role));
    if (
      own(state, "state") === "present"
      && (
        own(state, "media_type") !== mediaType
        || own(state, "encoding") !== "utf-8"
      )
    ) {
      options.push(`${prefix}/desired/${role}`, "media", "desired artifact media contract is inconsistent");
    }
  }
}

export interface ZccPullRefreshParitySemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string; readonly rootData?: unknown },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

export const validateZccPullRefreshParityRequestSemantics:
  ZccPullRefreshParitySemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const input = record(data);
    const request = record(dataContext?.rootData);
    const candidateContext = record(own(request, "context"));
    const referenceContext = record(own(input, "reference_context"));
    const seed = record(own(input, "seed"));
    const bindings = record(own(seed, "bindings"));
    const candidateBinding = record(own(bindings, "candidate"));
    const referenceBinding = record(own(bindings, "reference_twin"));
    if (
      input === null || request === null || candidateContext === null
      || referenceContext === null || seed === null || candidateBinding === null
      || referenceBinding === null
    ) {
      delete validateZccPullRefreshParityRequestSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const prefix = dataContext?.instancePath ?? "";
    const push = (path: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_PARITY_REQUEST_SEMANTICS_KEYWORD,
        `${prefix}${path}`,
        rule,
        message,
      ));
    };
    const tenant = own(input, "tenant");
    const resourceType = own(input, "resource_type");
    if (
      tenant !== own(seed, "tenant")
      || resourceType !== own(seed, "resource_type")
    ) {
      push("/seed", "request_join", "refresh parity request does not join its seed");
    }
    try {
      const candidateSha = zccPullRefreshParityRequestSha({
        context: candidateContext as unknown as {
          readonly workspace: string;
          readonly deployment: string;
          readonly root_catalog: string;
        },
        tenant: String(tenant),
        resourceType: String(resourceType),
      });
      const referenceSha = zccPullRefreshParityRequestSha({
        context: referenceContext as unknown as {
          readonly workspace: string;
          readonly deployment: string;
          readonly root_catalog: string;
        },
        tenant: String(tenant),
        resourceType: String(resourceType),
      });
      if (
        candidateSha !== own(candidateBinding, "request_sha256")
        || referenceSha !== own(referenceBinding, "request_sha256")
      ) {
        push("/seed", "context_join", "refresh parity contexts do not join the seed");
      }
    } catch {
      push("/seed", "context_join", "refresh parity contexts are not hashable");
    }
    if (errors.length === 0) {
      delete validateZccPullRefreshParityRequestSemantics.errors;
      return true;
    }
    validateZccPullRefreshParityRequestSemantics.errors = errors;
    return false;
  };

export const validateZccPullRefreshParitySeedSemantics:
  ZccPullRefreshParitySemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const seed = record(data);
    const candidate = record(own(seed, "candidate"));
    const reference = record(own(seed, "reference_twin"));
    if (seed === null || candidate === null || reference === null) {
      delete validateZccPullRefreshParitySeedSemantics.errors;
      return true;
    }
    const prefix = dataContext?.instancePath ?? "";
    const errors: ErrorObject[] = [];
    const push = (path: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_PARITY_SEED_SEMANTICS_KEYWORD,
        `${prefix}${path}`,
        rule,
        message,
      ));
    };
    if (own(candidate, "evidence_sha256") !== evidenceDigest(candidate)) {
      push("/candidate/evidence_sha256", "evidence_digest", "candidate evidence digest is inconsistent");
    }
    if (own(reference, "evidence_sha256") !== evidenceDigest(reference)) {
      push("/reference_twin/evidence_sha256", "evidence_digest", "reference evidence digest is inconsistent");
    }
    const resourceType = own(seed, "resource_type");
    for (const [name, evidence] of [
      ["candidate", candidate],
      ["reference_twin", reference],
    ] as const) {
      validateEvidenceProfile({
        name,
        evidence,
        resourceType: String(resourceType),
        push,
      });
    }
    const bindings = record(own(seed, "bindings"));
    const expectedDelta = expectedDifferences(
      candidate,
      reference,
      record(own(bindings, "candidate")),
      record(own(bindings, "reference_twin")),
    );
    if (!same(own(seed, "differences"), expectedDelta)) {
      push("/differences", "differences", "seed differences are inconsistent");
    }
    const candidateMoves = record(own(candidate, "moves"));
    const referenceMoves = record(own(reference, "moves"));
    const expectedStatus = own(candidate, "status") === "ready"
        && own(reference, "status") === "ready"
        && own(candidateMoves, "suppressed_count") === 0
        && own(referenceMoves, "suppressed_count") === 0
        && expectedDelta.length === 0
      ? "ready"
      : "review_required";
    if (own(seed, "status") !== expectedStatus) {
      push("/status", "status", "seed status is inconsistent");
    }
    if (own(seed, "seed_sha256") !== seedDigest(seed)) {
      push("/seed_sha256", "seed_digest", "seed digest is inconsistent");
    }
    if (errors.length === 0) {
      delete validateZccPullRefreshParitySeedSemantics.errors;
      return true;
    }
    validateZccPullRefreshParitySeedSemantics.errors = errors;
    return false;
  };

function entryStatus(expected: JsonRecord, observed: JsonRecord): string {
  if (own(expected, "state") === "absent") {
    return own(observed, "state") === "absent" ? "match" : "unexpected";
  }
  if (own(observed, "state") === "absent") {
    return "missing";
  }
  return own(expected, "sha256") === own(observed, "sha256")
      && own(expected, "size_bytes") === own(observed, "size_bytes")
    ? "match"
    : "mismatch";
}

function assertionWithoutDigest(assertion: JsonRecord): JsonRecord {
  const output: JsonRecord = Object.create(null) as JsonRecord;
  for (const key of Object.keys(assertion)) {
    if (key !== "assertion_sha256") {
      output[key] = own(assertion, key);
    }
  }
  return output;
}

export const validateZccPullRefreshParitySemantics:
  ZccPullRefreshParitySemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const assertion = record(data);
    const seed = record(own(assertion, "seed"));
    const candidate = record(own(assertion, "candidate"));
    const parity = record(own(assertion, "parity"));
    const artifacts = record(own(parity, "artifacts"));
    if (
      assertion === null || seed === null || candidate === null
      || parity === null || artifacts === null
    ) {
      delete validateZccPullRefreshParitySemantics.errors;
      return true;
    }
    const prefix = dataContext?.instancePath ?? "";
    const errors: ErrorObject[] = [];
    const push = (path: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_PARITY_SEMANTICS_KEYWORD,
        `${prefix}${path}`,
        rule,
        message,
      ));
    };
    if (
      own(assertion, "tenant") !== own(seed, "tenant")
      || own(assertion, "resource_type") !== own(seed, "resource_type")
      || !same(candidate, own(seed, "candidate"))
    ) {
      push("/seed", "seed_join", "assertion does not exactly join its seed");
    }
    const desired = record(own(candidate, "desired"));
    const counts = { match: 0, mismatch: 0, missing: 0, unexpected: 0 };
    for (const role of ROLES) {
      const entry = record(own(artifacts, role));
      const expected = record(own(entry, "expected"));
      const observed = record(own(entry, "observed"));
      if (entry === null || expected === null || observed === null) {
        continue;
      }
      if (!same(expected, own(desired, role))) {
        push(`/parity/artifacts/${role}/expected`, "expected_join", "expected role does not join candidate evidence");
      }
      const expectedStatus = entryStatus(expected, observed);
      if (own(entry, "status") !== expectedStatus) {
        push(`/parity/artifacts/${role}/status`, "artifact_status", "artifact status is inconsistent");
      }
      counts[expectedStatus as keyof typeof counts] += 1;
    }
    for (const [field, status] of [
      ["matched", "match"],
      ["mismatched", "mismatch"],
      ["missing", "missing"],
      ["unexpected", "unexpected"],
    ] as const) {
      if (own(parity, field) !== counts[status]) {
        push(`/parity/${field}`, "counts", "parity count is inconsistent");
      }
    }
    const parityStatus = counts.match === 7 ? "equal" : "different";
    if (own(parity, "status") !== parityStatus) {
      push("/parity/status", "parity_status", "parity status is inconsistent");
    }
    const status = own(candidate, "status") === "ready" && parityStatus === "equal"
      ? "ready"
      : "review_required";
    if (own(assertion, "status") !== status) {
      push("/status", "status", "assertion status is inconsistent");
    }
    const digest = safeDigest({
      kind: "infrawright.zcc_pull_refresh_parity_assertion_digest",
      schema_version: 1,
      assertion: assertionWithoutDigest(assertion),
    });
    if (own(assertion, "assertion_sha256") !== digest) {
      push("/assertion_sha256", "assertion_digest", "assertion digest is inconsistent");
    }
    if (errors.length === 0) {
      delete validateZccPullRefreshParitySemantics.errors;
      return true;
    }
    validateZccPullRefreshParitySemantics.errors = errors;
    return false;
  };
