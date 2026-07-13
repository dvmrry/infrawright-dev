import type { LoadedPackRoot } from "../metadata/loader.js";
import { LosslessNumber } from "lossless-json";
import {
  isJsonRecord,
  terraformJsonEqual,
} from "../json/python-equality.js";
import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { canonicalPythonNumberToken } from "../json/python-number.js";
import type { AssessmentFinding } from "./plan-assessment.js";
import {
  BLOCKED,
  diffPaths,
  truthyPaths,
  type PlanPath,
} from "./plan-eval.js";
import { normalizePolicyPath, parsePolicyPath } from "./policy-paths.js";
import {
  formatConcretePlanPath,
  formatSchemaPlanPath,
  type AssessmentGuidanceGroup,
} from "./plan-report.js";

const STATUS_EFFECT = "informational only; plan remains blocked";

interface RawManifestGuidance {
  readonly providers: readonly string[];
  readonly providerConfig: readonly Readonly<Record<string, unknown>>[];
  readonly absentDefaults: readonly Readonly<Record<string, unknown>>[];
  readonly dynamicSchema: readonly Readonly<Record<string, unknown>>[];
}

/** Immutable metadata snapshot consumed only by explanatory guidance. */
export interface AssessmentGuidanceSource {
  readonly providersByResource: ReadonlyMap<string, string>;
  readonly providerPrefixes: Readonly<Record<string, string>>;
  readonly manifests: readonly RawManifestGuidance[];
}

interface CandidatePath {
  readonly source: AssessmentFinding["source"];
  readonly address: string;
  readonly resourceType: string;
  readonly before: unknown;
  readonly path: PlanPath;
  readonly formatted: string;
}

type GuidanceEntry = Readonly<Record<string, unknown>>;

const LANE_ORDER: Readonly<Record<string, number>> = {
  provider_config: 0,
  absent_default: 1,
  dynamic_schema: 2,
};
const PROVIDER_REMEDIATION_KEYS = new Set(["kind", "mode", "evidence", "safety"]);
const PROVIDER_MODES = new Set([
  "diagnostic_only",
  "required_external",
  "renderable_default",
]);
const ABSENT_KINDS = new Set([
  "api_absent",
  "api_explicit_default",
  "provider_absent_placeholder",
  "terraform_schema_optional_default",
  "real_configured_falsey",
  "provider_server_side_singleton_default",
  "paid_disabled_or_api_boundary_default",
]);
const ABSENT_ACTIONS = new Set([
  "diagnostic_only",
  "manual_review_required",
  "preserve_explicit_falsey",
]);
const ABSENT_ACCEPTED_KEYS = new Set([
  "id", "provider", "resource_type", "resource_prefix", "path", "kind",
  "observed_value", "action", "evidence", "reason", "plan_path",
  "raw_api_path", "provider_state_path",
]);
const ABSENT_KIND_ACTIONS: Readonly<Record<string, ReadonlySet<string>>> = {
  api_absent: new Set(["diagnostic_only", "manual_review_required"]),
  api_explicit_default: new Set(["diagnostic_only", "manual_review_required"]),
  provider_absent_placeholder: new Set([
    "diagnostic_only", "manual_review_required",
  ]),
  terraform_schema_optional_default: new Set([
    "diagnostic_only", "manual_review_required",
  ]),
  real_configured_falsey: new Set([
    "preserve_explicit_falsey", "diagnostic_only", "manual_review_required",
  ]),
  provider_server_side_singleton_default: new Set([
    "diagnostic_only", "manual_review_required",
  ]),
  paid_disabled_or_api_boundary_default: new Set([
    "diagnostic_only", "manual_review_required",
  ]),
};
const ABSENT_OBSERVED_KINDS = new Set([
  "provider_absent_placeholder",
  "api_explicit_default",
  "terraform_schema_optional_default",
]);
const DYNAMIC_KINDS = new Set([
  "provider_state_only",
  "provider_computed_map",
  "freeform_object",
  "opaque_json_blob",
  "map_key_discovered_after_import",
  "unstable_collection_identity",
  "schema_unknown_but_provider_observed",
  "raw_api_only_provider_blind",
  "provider_observed_projection_unsafe",
]);
const DYNAMIC_OWNERSHIPS = new Set([
  "user_owned",
  "provider_computed",
  "server_owned",
  "unknown",
]);
const DYNAMIC_ACTIONS = new Set(["diagnostic_only", "manual_review_required"]);
const DYNAMIC_ACCEPTED_KEYS = new Set([
  "id", "provider", "provider_version_constraint", "resource_type",
  "resource_prefix", "path", "kind", "ownership", "action", "evidence",
  "reason", "raw_api_path", "projected_path", "plan_path",
]);
const DYNAMIC_KIND_OWNERSHIPS: Readonly<Record<string, ReadonlySet<string>>> = {
  provider_state_only: new Set(["provider_computed", "server_owned", "unknown"]),
  provider_computed_map: new Set(["provider_computed", "server_owned", "unknown"]),
  freeform_object: new Set([
    "user_owned", "provider_computed", "server_owned", "unknown",
  ]),
  opaque_json_blob: new Set(["provider_computed", "server_owned", "unknown"]),
  map_key_discovered_after_import: new Set([
    "provider_computed", "server_owned", "unknown",
  ]),
  unstable_collection_identity: new Set([
    "provider_computed", "server_owned", "unknown",
  ]),
  schema_unknown_but_provider_observed: new Set([
    "user_owned", "provider_computed", "server_owned", "unknown",
  ]),
  raw_api_only_provider_blind: new Set(["unknown"]),
  provider_observed_projection_unsafe: new Set([
    "provider_computed", "server_owned", "unknown",
  ]),
};

function records(value: unknown): readonly Readonly<Record<string, unknown>>[] {
  return Array.isArray(value) && value.every(isJsonRecord) ? value : [];
}

function groupRules(
  data: Readonly<Record<string, unknown>>,
  group: string,
  field: string,
): readonly Readonly<Record<string, unknown>>[] {
  const raw = data[group];
  return isJsonRecord(raw) ? records(raw[field]) : [];
}

/** Snapshot original active pack guidance without inventing a second catalog. */
export function assessmentGuidanceSource(
  root: LoadedPackRoot,
): AssessmentGuidanceSource {
  const providersByResource = new Map<string, string>();
  for (const [resourceType, resource] of root.resources) {
    providersByResource.set(resourceType, resource.provider);
  }
  const manifests = root.packs.manifests.map((manifest): RawManifestGuidance => {
    const providers = sortedStrings(new Set(Object.values(manifest.providerPrefixes)));
    return Object.freeze({
      providers,
      providerConfig: [...groupRules(
        manifest.data,
        "provider_config",
        "requirements",
      )],
      absentDefaults: [...groupRules(
        manifest.data,
        "absent_defaults",
        "rules",
      )],
      dynamicSchema: [...groupRules(
        manifest.data,
        "dynamic_schema",
        "rules",
      )],
    });
  });
  return Object.freeze({
    providersByResource,
    providerPrefixes: Object.freeze({ ...root.packs.providerPrefixes }),
    manifests: Object.freeze(manifests),
  });
}

function inferredProvider(
  raw: Readonly<Record<string, unknown>>,
  manifest: RawManifestGuidance,
): string | null {
  if (typeof raw.provider === "string" && raw.provider.length > 0) {
    return raw.provider;
  }
  return manifest.providers.length === 1 ? manifest.providers[0] ?? null : null;
}

function text(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new TypeError(`${field} must be a non-empty string`);
  }
  return value.trim();
}

function optionalStrings(value: unknown, field: string): readonly string[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || !value.every((item) => {
    return typeof item === "string" && item.trim().length > 0;
  })) {
    throw new TypeError(`${field} must be a list of non-empty strings`);
  }
  return sortedStrings(new Set(value.map((item) => item.trim())));
}

function reportValue(value: unknown, depth = 0): unknown {
  if (depth > 64) throw new TypeError("guidance value is too deeply nested");
  if (value instanceof LosslessNumber) {
    if (canonicalPythonNumberToken(value.toString()) === null) {
      throw new TypeError("guidance number cannot be represented exactly");
    }
    return new LosslessNumber(value.toString());
  }
  if (
    value === null
    || typeof value === "string"
    || typeof value === "boolean"
  ) {
    return value;
  }
  if (
    typeof value === "number"
    && Number.isFinite(value)
    && (!Number.isInteger(value) || Number.isSafeInteger(value))
    && !Object.is(value, -0)
  ) {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((child) => reportValue(child, depth + 1));
  }
  if (isJsonRecord(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, child]) => {
      return [key, reportValue(child, depth + 1)];
    }));
  }
  throw new TypeError("guidance value is not JSON-compatible");
}

function splitReportPath(value: string, field: string): string[] {
  const parts: string[] = [];
  let buffer = "";
  let quoted = false;
  let escaped = false;
  for (const character of value) {
    if (escaped) {
      buffer += character;
      escaped = false;
    } else if (character === "\\" && quoted) {
      buffer += character;
      escaped = true;
    } else if (character === '"') {
      buffer += character;
      quoted = !quoted;
    } else if (character === "." && !quoted) {
      parts.push(buffer);
      buffer = "";
    } else {
      buffer += character;
    }
  }
  if (quoted) throw new TypeError(`${field} contains an unterminated quote`);
  parts.push(buffer);
  return parts;
}

function schemaPath(value: unknown, field: string): string {
  const raw = text(value, field);
  if (raw === "<root>") return raw;
  const segments: string[] = [];
  for (const part of splitReportPath(raw, field)) {
    if (part.length === 0) throw new TypeError(`${field} contains an empty segment`);
    if (!part.includes("[") && !part.includes("]")) {
      if (part === "*") {
        throw new TypeError(`${field} contains a bare wildcard segment`);
      }
      segments.push(part);
      continue;
    }
    segments.push(...normalizePolicyPath(parsePolicyPath(part, field)));
  }
  const rendered: string[] = [];
  for (const segment of segments) {
    if (segment === "[]") {
      if (rendered.length === 0) rendered.push("[]");
      else rendered[rendered.length - 1] = `${rendered.at(-1) ?? ""}[]`;
    } else {
      rendered.push(segment);
    }
  }
  return rendered.length === 0 ? "<root>" : rendered.join(".");
}

function planRecords(
  plan: Readonly<Record<string, unknown>>,
  resourceType: string,
): CandidatePath[] {
  const candidates: CandidatePath[] = [];
  for (const source of ["resource_changes", "resource_drift"] as const) {
    const changes = plan[source];
    if (!Array.isArray(changes)) continue;
    for (const raw of changes) {
      if (
        !isJsonRecord(raw)
        || raw.type !== resourceType
        || typeof raw.address !== "string"
        || !isJsonRecord(raw.change)
        || !Array.isArray(raw.change.actions)
        || !raw.change.actions.includes("update")
      ) {
        continue;
      }
      const paths = new Map<string, PlanPath>();
      for (const candidate of [
        ...diffPaths(raw.change.before, raw.change.after),
        ...truthyPaths(raw.change.after_unknown),
      ]) {
        paths.set(JSON.stringify(candidate), candidate);
      }
      for (const candidate of paths.values()) {
        candidates.push({
          source,
          address: raw.address,
          resourceType,
          before: raw.change.before,
          path: candidate,
          formatted: formatSchemaPlanPath(candidate),
        });
      }
    }
  }
  return candidates.sort((left, right) => {
    for (const [leftPart, rightPart] of [
      [left.source, right.source],
      [left.address, right.address],
      [left.formatted, right.formatted],
      [left.path.map(String).join("\0"), right.path.map(String).join("\0")],
    ] as const) {
      const compared = comparePythonStrings(leftPart, rightPart);
      if (compared !== 0) return compared;
    }
    return 0;
  });
}

function providerConfigGuidance(
  source: AssessmentGuidanceSource,
  plan: Readonly<Record<string, unknown>>,
  resourceType: string,
): GuidanceEntry[] {
  const provider = source.providersByResource.get(resourceType);
  if (provider === undefined) throw new TypeError("unknown guidance resource");
  const candidates = planRecords(plan, resourceType);
  const output: GuidanceEntry[] = [];
  const seenSettings = new Set<string>();
  for (const manifest of source.manifests) {
    for (const raw of manifest.providerConfig) {
      const requirementProvider = inferredProvider(raw, manifest);
      if (requirementProvider !== provider) continue;
      text(raw.id, "provider_config.id");
      const setting = text(raw.setting, "provider_config.setting");
      const reason = text(raw.reason, "provider_config.reason");
      if (!Array.isArray(raw.plan_paths) || raw.plan_paths.length === 0) {
        throw new TypeError("provider_config.plan_paths must be a non-empty list");
      }
      const paths = new Set(raw.plan_paths.map((item) => {
        return schemaPath(item, "provider_config.plan_path");
      }));
      const resourceTypes = new Set(optionalStrings(
        raw.resource_types,
        "provider_config.resource_types",
      ));
      const resourcePrefixes = optionalStrings(
        raw.resource_prefixes,
        "provider_config.resource_prefixes",
      );
      if (resourceTypes.size > 0 && !resourceTypes.has(resourceType)) continue;
      if (
        resourcePrefixes.length > 0
        && !resourcePrefixes.some((prefix) => resourceType.startsWith(prefix))
      ) {
        continue;
      }
      const remediation = raw.remediation;
      const mode = remediation === undefined
        ? "diagnostic_only"
        : isJsonRecord(remediation)
        ? text(remediation.mode, "provider_config.remediation.mode")
        : (() => { throw new TypeError("provider_config.remediation must be an object"); })();
      if (!PROVIDER_MODES.has(mode)) {
        throw new TypeError("provider_config remediation mode is invalid");
      }
      if (isJsonRecord(remediation)) {
        if (Object.keys(remediation).some((key) => !PROVIDER_REMEDIATION_KEYS.has(key))) {
          throw new TypeError("provider_config remediation contains an unknown key");
        }
        if (remediation.kind !== "provider_argument") {
          throw new TypeError("provider_config remediation kind is invalid");
        }
      }
      if (!Object.hasOwn(raw, "value") && mode !== "required_external") {
        throw new TypeError("provider_config value is required");
      }
      if (mode === "renderable_default") {
        const value = raw.value;
        if (
          typeof value !== "boolean"
          && (typeof value !== "number" || !Number.isFinite(value))
          && (
            !(value instanceof LosslessNumber)
            || canonicalPythonNumberToken(value.toString()) === null
          )
        ) {
          throw new TypeError("provider_config renderable value is invalid");
        }
        if (raw.resource_types !== undefined || raw.resource_prefixes !== undefined) {
          throw new TypeError("provider_config renderable default must be global");
        }
        if (
          !isJsonRecord(remediation?.safety)
          || remediation.safety.non_sensitive !== true
          || remediation.safety.not_tenant_specific !== true
          || remediation.safety.not_destructive !== true
          || typeof remediation.evidence !== "string"
          || remediation.evidence.trim().length === 0
        ) {
          throw new TypeError("provider_config renderable safety evidence is invalid");
        }
      }
      const settingKey = `${provider}\0${setting}`;
      if (seenSettings.has(settingKey)) {
        throw new TypeError("provider_config setting is duplicated");
      }
      seenSettings.add(settingKey);
      if (mode !== "required_external" && mode !== "renderable_default") continue;
      const evidence = isJsonRecord(remediation) && typeof remediation.evidence === "string"
        ? remediation.evidence
        : "";
      for (const candidate of candidates) {
        if (!paths.has(candidate.formatted)) continue;
        output.push({
          lane: "provider_config",
          provider,
          resource_type: resourceType,
          address: candidate.address,
          source: candidate.source,
          matched_plan_path: candidate.formatted,
          status_effect: STATUS_EFFECT,
          setting,
          expected_value: reportValue(raw.value ?? null),
          mode,
          reason,
          evidence,
        });
      }
    }
  }
  return output;
}

function scopedProvider(
  resourceType: string,
  providerPrefixes: Readonly<Record<string, string>>,
): string | null {
  const prefixes = Object.keys(providerPrefixes).sort((left, right) => {
    return right.length - left.length || comparePythonStrings(left, right);
  });
  const prefix = prefixes.find((candidate) => resourceType.startsWith(candidate));
  return prefix === undefined ? null : providerPrefixes[prefix] ?? null;
}

function requireKnownKeys(
  raw: Readonly<Record<string, unknown>>,
  accepted: ReadonlySet<string>,
  lane: string,
): void {
  const unknown = sortedStrings(Object.keys(raw).filter((key) => !accepted.has(key)));
  if (unknown.length > 0) throw new TypeError(`${lane} contains unknown key ${unknown[0]}`);
}

function validateScope(
  raw: Readonly<Record<string, unknown>>,
  provider: string,
  source: AssessmentGuidanceSource,
  lane: string,
): { readonly kind: "type" | "prefix"; readonly value: string } {
  const hasType = Object.hasOwn(raw, "resource_type");
  const hasPrefix = Object.hasOwn(raw, "resource_prefix");
  if (hasType === hasPrefix) {
    throw new TypeError(`${lane} requires exactly one resource scope`);
  }
  if (hasType) {
    const resourceType = text(raw.resource_type, `${lane}.resource_type`);
    if (scopedProvider(resourceType, source.providerPrefixes) !== provider) {
      throw new TypeError(`${lane}.resource_type is outside its provider scope`);
    }
    return { kind: "type", value: resourceType };
  }
  const resourcePrefix = text(raw.resource_prefix, `${lane}.resource_prefix`);
  if (source.providerPrefixes[resourcePrefix] !== provider) {
    throw new TypeError(`${lane}.resource_prefix is outside its provider scope`);
  }
  return { kind: "prefix", value: resourcePrefix };
}

interface ValidatedLaneRule {
  readonly raw: Readonly<Record<string, unknown>>;
  readonly provider: string;
  readonly path: string;
  readonly scope: { readonly kind: "type" | "prefix"; readonly value: string };
  readonly version: string | null;
}

function validateLaneRule(
  raw: Readonly<Record<string, unknown>>,
  provider: string,
  source: AssessmentGuidanceSource,
  lane: "absent_default" | "dynamic_schema",
): ValidatedLaneRule {
  const accepted = lane === "absent_default"
    ? ABSENT_ACCEPTED_KEYS
    : DYNAMIC_ACCEPTED_KEYS;
  requireKnownKeys(raw, accepted, lane);
  text(raw.id, `${lane}.id`);
  text(raw.provider, `${lane}.provider`);
  const path = schemaPath(raw.path, `${lane}.path`);
  text(raw.evidence, `${lane}.evidence`);
  text(raw.reason, `${lane}.reason`);
  const scope = validateScope(raw, provider, source, lane);
  for (const field of lane === "absent_default"
    ? ["raw_api_path", "provider_state_path"]
    : ["raw_api_path", "projected_path"]) {
    if (raw[field] !== undefined) text(raw[field], `${lane}.${field}`);
  }
  if (raw.plan_path !== undefined) {
    if (lane === "absent_default") schemaPath(raw.plan_path, `${lane}.plan_path`);
    else text(raw.plan_path, `${lane}.plan_path`);
  }
  const kind = text(raw.kind, `${lane}.kind`);
  const action = text(raw.action, `${lane}.action`);
  let version: string | null = null;
  let normalizedRaw: Readonly<Record<string, unknown>> = {
    ...raw,
    path,
    ...(lane === "absent_default" && raw.plan_path !== undefined
      ? { plan_path: schemaPath(raw.plan_path, `${lane}.plan_path`) }
      : {}),
  };
  if (lane === "absent_default") {
    if (!ABSENT_KINDS.has(kind) || !ABSENT_ACTIONS.has(action)) {
      throw new TypeError("absent_default rule vocabulary is invalid");
    }
    if (!(ABSENT_KIND_ACTIONS[kind]?.has(action) ?? false)) {
      throw new TypeError("absent_default kind/action combination is invalid");
    }
    if (
      (ABSENT_OBSERVED_KINDS.has(kind) || action === "preserve_explicit_falsey")
      && !Object.hasOwn(raw, "observed_value")
    ) {
      throw new TypeError("absent_default observed value is required");
    }
  } else {
    const ownership = text(raw.ownership, "dynamic_schema.ownership");
    version = text(
      raw.provider_version_constraint,
      "dynamic_schema.provider_version_constraint",
    );
    if (
      !DYNAMIC_KINDS.has(kind)
      || !DYNAMIC_OWNERSHIPS.has(ownership)
      || !DYNAMIC_ACTIONS.has(action)
    ) {
      throw new TypeError("dynamic_schema rule vocabulary is invalid");
    }
    if (!(DYNAMIC_KIND_OWNERSHIPS[kind]?.has(ownership) ?? false)) {
      throw new TypeError("dynamic_schema kind/ownership combination is invalid");
    }
    normalizedRaw = {
      ...normalizedRaw,
      id: text(raw.id, `${lane}.id`),
      provider,
      provider_version_constraint: version,
      kind,
      ownership,
      action,
    };
  }
  return { raw: normalizedRaw, provider, path, scope, version };
}

function providerLaneRules(
  source: AssessmentGuidanceSource,
  provider: string,
  lane: "absent_default" | "dynamic_schema",
): ValidatedLaneRule[] {
  const selected: Readonly<Record<string, unknown>>[] = [];
  for (const manifest of source.manifests) {
    const rules = lane === "absent_default"
      ? manifest.absentDefaults
      : manifest.dynamicSchema;
    for (const raw of rules) {
      const candidateProvider = inferredProvider(raw, manifest);
      if (candidateProvider !== provider) continue;
      selected.push({ ...raw, provider: candidateProvider });
    }
  }
  const validated = selected.map((raw) => {
    return validateLaneRule(raw, provider, source, lane);
  });
  const identities = new Set<string>();
  for (const rule of validated) {
    const identity = JSON.stringify([
      rule.provider,
      rule.version,
      rule.scope.kind,
      rule.scope.value,
      rule.path,
    ]);
    if (identities.has(identity)) throw new TypeError(`${lane} rule is duplicated`);
    identities.add(identity);
  }
  for (const typeRule of validated.filter((rule) => rule.scope.kind === "type")) {
    for (const prefixRule of validated.filter((rule) => {
      return rule.scope.kind === "prefix"
        && rule.provider === typeRule.provider
        && rule.version === typeRule.version
        && rule.path === typeRule.path;
    })) {
      if (typeRule.scope.value.startsWith(prefixRule.scope.value)) {
        throw new TypeError(`${lane} resource scopes overlap`);
      }
    }
  }
  return validated;
}

function valueAtPath(value: unknown, candidate: PlanPath): {
  readonly present: boolean;
  readonly value: unknown;
} {
  let current = value;
  for (const segment of candidate) {
    if (typeof segment === "number") {
      if (!Array.isArray(current) || segment < 0 || segment >= current.length) {
        return { present: false, value: null };
      }
      current = current[segment];
    } else if (isJsonRecord(current) && Object.hasOwn(current, segment)) {
      current = current[segment];
    } else {
      return { present: false, value: null };
    }
  }
  return { present: true, value: current };
}

function ruleGuidance(
  source: AssessmentGuidanceSource,
  plan: Readonly<Record<string, unknown>>,
  resourceType: string,
  lane: "absent_default" | "dynamic_schema",
): GuidanceEntry[] {
  const provider = source.providersByResource.get(resourceType);
  if (provider === undefined) throw new TypeError("unknown guidance resource");
  const candidates = planRecords(plan, resourceType);
  const output: GuidanceEntry[] = [];
  const rules = providerLaneRules(source, provider, lane);
  for (const validated of rules) {
      const raw = validated.raw;
      if (raw.action !== "manual_review_required") continue;
      const exactType = raw.resource_type;
      const prefix = raw.resource_prefix;
      if (
        (typeof exactType === "string" && exactType !== resourceType)
        || (
          typeof exactType !== "string"
          && (typeof prefix !== "string" || !resourceType.startsWith(prefix))
        )
      ) {
        continue;
      }
      const rule = text(raw.id, `${lane}.id`);
      const matchedPath = schemaPath(
        raw.plan_path ?? raw.path,
        `${lane}.path`,
      );
      const kind = text(raw.kind, `${lane}.kind`);
      const reason = text(raw.reason, `${lane}.reason`);
      const evidence = text(raw.evidence, `${lane}.evidence`);
      for (const candidate of candidates) {
        if (candidate.formatted !== matchedPath) continue;
        if (lane === "absent_default" && Object.hasOwn(raw, "observed_value")) {
          const observed = valueAtPath(candidate.before, candidate.path);
          if (
            !observed.present
            || !terraformJsonEqual(observed.value, raw.observed_value)
          ) {
            continue;
          }
        }
        if (lane === "absent_default") {
          output.push({
            lane,
            provider,
            resource_type: resourceType,
            address: candidate.address,
            source: candidate.source,
            matched_plan_path: matchedPath,
            status_effect: STATUS_EFFECT,
            rule,
            kind,
            action: "manual_review_required",
            observed_value: reportValue(raw.observed_value ?? null),
            reason,
            evidence,
          });
        } else {
          output.push({
            lane,
            provider,
            resource_type: resourceType,
            address: candidate.address,
            source: candidate.source,
            matched_plan_path: matchedPath,
            status_effect: STATUS_EFFECT,
            rule,
            kind,
            ownership: text(raw.ownership, "dynamic_schema.ownership"),
            action: "manual_review_required",
            provider_version_constraint:
              typeof raw.provider_version_constraint === "string"
                ? raw.provider_version_constraint
                : null,
            reason,
            evidence,
          });
        }
      }
  }
  return output;
}

function safeCollect(operation: () => GuidanceEntry[]): GuidanceEntry[] {
  try {
    return operation();
  } catch {
    return [];
  }
}

function joinBlockedFindings(
  findings: readonly AssessmentFinding[],
  annotations: readonly GuidanceEntry[],
): GuidanceEntry[] {
  const output: GuidanceEntry[] = [];
  for (const finding of findings) {
    if (finding.status !== BLOCKED) continue;
    for (const findingPath of finding.paths) {
      const matched = formatSchemaPlanPath(findingPath);
      for (const annotation of annotations) {
        if (
          annotation.source === finding.source
          && annotation.address === finding.address
          && annotation.matched_plan_path === matched
        ) {
          output.push({
            ...annotation,
            finding_path: formatConcretePlanPath(findingPath),
          });
        }
      }
    }
  }
  return output.sort((left, right) => {
    const lane = String(left.lane ?? "");
    const otherLane = String(right.lane ?? "");
    const leftKey: readonly (number | string)[] = lane === "provider_config"
      ? [
          LANE_ORDER[lane] ?? 99,
          String(left.provider ?? ""),
          String(left.setting ?? ""),
          String(left.matched_plan_path ?? ""),
        ]
      : [
          LANE_ORDER[lane] ?? 99,
          String(left.provider ?? ""),
          String(left.resource_type ?? ""),
          String(left.matched_plan_path ?? ""),
          String(left.rule ?? ""),
        ];
    const rightKey: readonly (number | string)[] = otherLane === "provider_config"
      ? [
          LANE_ORDER[otherLane] ?? 99,
          String(right.provider ?? ""),
          String(right.setting ?? ""),
          String(right.matched_plan_path ?? ""),
        ]
      : [
          LANE_ORDER[otherLane] ?? 99,
          String(right.provider ?? ""),
          String(right.resource_type ?? ""),
          String(right.matched_plan_path ?? ""),
          String(right.rule ?? ""),
        ];
    for (let index = 0; index < Math.max(leftKey.length, rightKey.length); index += 1) {
      const leftPart = leftKey[index] ?? "";
      const rightPart = rightKey[index] ?? "";
      if (leftPart === rightPart) continue;
      if (typeof leftPart === "number" && typeof rightPart === "number") {
        return leftPart - rightPart;
      }
      return comparePythonStrings(String(leftPart), String(rightPart));
    }
    return 0;
  });
}

/** Collect explanatory entries without changing the supplied classification. */
export function collectAssessmentGuidance(options: {
  readonly source: AssessmentGuidanceSource;
  readonly tenant: string;
  readonly label: string;
  readonly members: readonly string[];
  readonly plan: Readonly<Record<string, unknown>>;
  readonly findings: readonly AssessmentFinding[];
}): AssessmentGuidanceGroup {
  const annotations: GuidanceEntry[] = [];
  for (const resourceType of options.members) {
    annotations.push(...safeCollect(() => providerConfigGuidance(
      options.source,
      options.plan,
      resourceType,
    )));
    annotations.push(...safeCollect(() => ruleGuidance(
      options.source,
      options.plan,
      resourceType,
      "absent_default",
    )));
    annotations.push(...safeCollect(() => ruleGuidance(
      options.source,
      options.plan,
      resourceType,
      "dynamic_schema",
    )));
  }
  return {
    tenant: options.tenant,
    label: options.label,
    entries: joinBlockedFindings(options.findings, annotations),
  };
}
