import type { ErrorObject } from "ajv/dist/2020.js";

import { sortedStrings } from "../json/python-compatible.js";
import {
  SUPPORTED_ZCC_ROOT_MEMBERS,
  SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS,
} from "../domain/zscaler-assessment.js";

export const ZCC_PULL_PARITY_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-parity-semantics";

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as JsonRecord
    : null;
}

function strings(value: unknown): readonly string[] | null {
  return Array.isArray(value)
    && value.every((item) => typeof item === "string")
    ? value
    : null;
}

function sameStrings(
  left: readonly string[],
  right: readonly string[],
): boolean {
  return left.length === right.length
    && left.every((value, index) => value === right[index]);
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ZCC_PULL_PARITY_SEMANTICS_KEYWORD}`,
    keyword: ZCC_PULL_PARITY_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

function layoutPrefix(pathValue: string, suffix: string): string | null {
  if (pathValue === suffix) {
    return "";
  }
  if (!pathValue.endsWith(suffix)) {
    return null;
  }
  const prefix = pathValue.slice(0, -suffix.length);
  return prefix.endsWith("/") ? prefix : null;
}

function digestEqual(left: JsonRecord, right: JsonRecord): boolean {
  return left.sha256 === right.sha256 && left.size_bytes === right.size_bytes;
}

export interface ZccPullParitySemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce the redundant joins and counts in a digest-only parity report. */
export const validateZccPullParitySemantics:
  ZccPullParitySemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const report = record(data);
    const source = record(report?.source);
    const root = record(report?.root);
    const candidate = record(report?.candidate);
    const parity = record(report?.parity);
    const artifacts = record(parity?.artifacts);
    if (
      report === null
      || source === null
      || root === null
      || candidate === null
      || parity === null
      || artifacts === null
    ) {
      delete validateZccPullParitySemantics.errors;
      return true;
    }

    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    const resourceType = report.resource_type;
    const tenant = report.tenant;
    if (typeof resourceType !== "string" || typeof tenant !== "string") {
      delete validateZccPullParitySemantics.errors;
      return true;
    }

    if (source.path !== `pulls/${tenant}/${resourceType}.json`) {
      push("/source/path", "source_path", "source path must match tenant and resource");
    }

    const members = strings(root.members);
    if (members !== null) {
      const expectedMembers = sortedStrings(new Set(members));
      if (!sameStrings(members, expectedMembers) || !members.includes(resourceType)) {
        push(
          "/root/members",
          "root_members",
          "root members must be sorted, unique, and include the resource",
        );
      }
      if (expectedMembers.some(
        (member) => !SUPPORTED_ZCC_ROOT_MEMBERS.includes(member),
      )) {
        push(
          "/root/members",
          "root_members",
          "root members must belong to the exact bundled ZCC catalog",
        );
      }
      if (
        typeof root.label === "string"
        && SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS.includes(root.label)
        && (expectedMembers.length !== 1 || expectedMembers[0] !== root.label)
      ) {
        push(
          "/root/label",
          "root_label",
          "a generated resource label can only denote its singleton root",
        );
      }
      const expectedVariable = root.label === resourceType
        ? "items"
        : `${resourceType}_items`;
      if (root.variable_name !== expectedVariable) {
        push(
          "/root/variable_name",
          "variable_name",
          "root variable name must match the resource/root relationship",
        );
      }
    }

    const drops = strings(candidate.unexpected_drops);
    if (drops !== null) {
      const expectedDrops = sortedStrings(new Set(drops));
      if (!sameStrings(drops, expectedDrops)) {
        push(
          "/candidate/unexpected_drops",
          "unexpected_drops",
          "unexpected drops must be sorted and unique",
        );
      }
    }

    const entries = ["tfvars", "imports", "lookup"] as const;
    let matched = 0;
    let mismatched = 0;
    let missing = 0;
    for (const name of entries) {
      const entry = record(artifacts[name]);
      if (entry === null) {
        continue;
      }
      if (entry.status === "not_applicable") {
        continue;
      }
      const expected = record(entry.expected);
      const observed = record(entry.observed);
      if (entry.status === "match") {
        matched += 1;
        if (expected !== null && observed !== null && !digestEqual(expected, observed)) {
          push(
            `/parity/artifacts/${name}/status`,
            "artifact_status",
            "match requires identical expected and observed digests",
          );
        }
      } else if (entry.status === "mismatch") {
        mismatched += 1;
        if (expected !== null && observed !== null && digestEqual(expected, observed)) {
          push(
            `/parity/artifacts/${name}/status`,
            "artifact_status",
            "mismatch requires different expected and observed digests",
          );
        }
      } else if (entry.status === "missing") {
        missing += 1;
      }
    }

    if (parity.matched !== matched) {
      push("/parity/matched", "parity_counts", "matched count is inconsistent");
    }
    if (parity.mismatched !== mismatched) {
      push("/parity/mismatched", "parity_counts", "mismatched count is inconsistent");
    }
    if (parity.missing !== missing) {
      push("/parity/missing", "parity_counts", "missing count is inconsistent");
    }
    const parityStatus = mismatched === 0 && missing === 0 ? "equal" : "different";
    if (parity.status !== parityStatus) {
      push("/parity/status", "parity_status", "parity status is inconsistent with artifacts");
    }
    const reportStatus = candidate.status === "ready" && parityStatus === "equal"
      ? "ready"
      : "review_required";
    if (report.status !== reportStatus) {
      push("/status", "report_status", "report status is inconsistent");
    }

    const tfvars = record(artifacts.tfvars);
    const imports = record(artifacts.imports);
    const lookup = record(artifacts.lookup);
    const tfvarsPath = typeof tfvars?.path === "string" ? tfvars.path : null;
    const importsPath = typeof imports?.path === "string" ? imports.path : null;
    const configPrefix = tfvarsPath === null
      ? null
      : layoutPrefix(
          tfvarsPath,
          `config/${tenant}/${resourceType}.auto.tfvars.json`,
        );
    const importsPrefix = importsPath === null
      ? null
      : layoutPrefix(
          importsPath,
          `imports/${tenant}/${resourceType}_imports.tf`,
        );
    if (
      configPrefix === null
      || importsPrefix === null
      || configPrefix !== importsPrefix
    ) {
      push(
        "/parity/artifacts",
        "artifact_layout",
        "artifact paths must share the canonical deployment layout",
      );
    }
    if (resourceType === "zcc_trusted_network") {
      const lookupPath = typeof lookup?.path === "string" ? lookup.path : null;
      const lookupPrefix = lookupPath === null
        ? null
        : layoutPrefix(
            lookupPath,
            `config/${tenant}/${resourceType}.lookup.json`,
          );
      if (lookupPrefix === null || lookupPrefix !== configPrefix) {
        push(
          "/parity/artifacts/lookup/path",
          "artifact_layout",
          "lookup path must share the canonical deployment layout",
        );
      }
    }

    if (errors.length === 0) {
      delete validateZccPullParitySemantics.errors;
      return true;
    }
    validateZccPullParitySemantics.errors = errors;
    return false;
  };
