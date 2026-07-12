import type { ErrorObject } from "ajv/dist/2020.js";

import { sortedStrings } from "../json/python-compatible.js";
import {
  SUPPORTED_ZCC_ROOT_MEMBERS,
  SUPPORTED_ZSCALER_GENERATED_ROOT_LABELS,
} from "../domain/zscaler-assessment.js";

export const ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-adoption-parity-semantics";

const ADOPTION_CATALOG_SHA256 =
  "ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7";
const CATALOG_SOURCES_SHA256 =
  "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11";

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
    && value.every((entry) => typeof entry === "string")
    ? value
    : null;
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length
    && left.every((entry, index) => entry === right[index]);
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD}`,
    keyword: ZCC_ADOPTION_PARITY_SEMANTICS_KEYWORD,
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

export interface ZccAdoptionParitySemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce every redundant provenance, coordinate, applicability, and count join. */
export const validateZccAdoptionParitySemantics:
  ZccAdoptionParitySemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const report = record(data);
    const source = record(report?.source);
    const catalog = record(report?.catalog);
    const root = record(report?.root);
    const parity = record(report?.parity);
    const artifacts = record(parity?.artifacts);
    if (
      report === null
      || source === null
      || catalog === null
      || root === null
      || parity === null
      || artifacts === null
    ) {
      delete validateZccAdoptionParitySemantics.errors;
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
      delete validateZccAdoptionParitySemantics.errors;
      return true;
    }

    if (
      catalog.kind !== "infrawright.adoption_catalog"
      || catalog.schema_version !== 1
      || catalog.sha256 !== ADOPTION_CATALOG_SHA256
      || catalog.sources_sha256 !== CATALOG_SOURCES_SHA256
    ) {
      push(
        "/catalog",
        "catalog_provenance",
        "parity must bind the exact adoption catalog and source evidence",
      );
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
      if (expectedMembers.some((member) => !SUPPORTED_ZCC_ROOT_MEMBERS.includes(member))) {
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

    let equal = 0;
    let different = 0;
    let invalidArtifactPath = false;
    const entries = ["tfvars", "imports", "lookup"] as const;
    for (const name of entries) {
      const entry = record(artifacts[name]);
      if (entry === null || entry.status === "not_applicable") {
        continue;
      }
      const candidate = record(entry.candidate);
      const reference = record(entry.reference);
      if (candidate === null || reference === null) {
        continue;
      }
      let invalidEntryPath = false;
      for (const side of ["candidate", "reference"] as const) {
        const logicalPath = side === "candidate"
          ? candidate.path
          : reference.path;
        if (
          typeof logicalPath === "string"
          && (logicalPath.includes("\0") || !logicalPath.isWellFormed())
        ) {
          invalidArtifactPath = true;
          invalidEntryPath = true;
          push(
            `/parity/artifacts/${name}/${side}/path`,
            "path_characters",
            "artifact paths must be well-formed and contain no NUL",
          );
        }
      }
      if (invalidEntryPath) {
        continue;
      }
      if (candidate.path !== reference.path) {
        push(
          `/parity/artifacts/${name}/reference/path`,
          "artifact_coordinates",
          "candidate and reference must use the same logical artifact path",
        );
      }
      const referencePresent = typeof reference.sha256 === "string"
        && typeof reference.size_bytes === "number";
      const referenceMissing = reference.sha256 === null
        && reference.size_bytes === null;
      if (!referencePresent && !referenceMissing) {
        push(
          `/parity/artifacts/${name}/reference`,
          "reference_digest",
          "reference digest and size must be present or absent together",
        );
      }
      const isEqual = referencePresent && digestEqual(candidate, reference);
      if (entry.status === "equal") {
        equal += 1;
        if (!isEqual) {
          push(
            `/parity/artifacts/${name}/status`,
            "artifact_status",
            "equal requires identical candidate and reference digests",
          );
        }
      } else if (entry.status === "different") {
        different += 1;
        if (isEqual) {
          push(
            `/parity/artifacts/${name}/status`,
            "artifact_status",
            "different requires a missing or unequal reference digest",
          );
        }
      }
    }
    if (!invalidArtifactPath) {
      if (parity.equal !== equal || parity.different !== different) {
        push(
          "/parity",
          "parity_counts",
          "artifact equality counts must match the per-role statuses",
        );
      }
      const parityStatus = different === 0 ? "equal" : "different";
      if (parity.status !== parityStatus) {
        push("/parity/status", "parity_status", "parity status is inconsistent");
      }
      const reportStatus = parityStatus === "equal" ? "ready" : "review_required";
      if (report.status !== reportStatus) {
        push("/status", "report_status", "report status is inconsistent");
      }
    }

    if (!invalidArtifactPath) {
      const tfvars = record(record(artifacts.tfvars)?.candidate);
      const imports = record(record(artifacts.imports)?.candidate);
      const lookup = record(record(artifacts.lookup)?.candidate);
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
      if (configPrefix === null || importsPrefix === null || configPrefix !== importsPrefix) {
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
            "/parity/artifacts/lookup/candidate/path",
            "artifact_layout",
            "lookup path must share the canonical deployment layout",
          );
        }
      }
    }

    if (errors.length === 0) {
      delete validateZccAdoptionParitySemantics.errors;
      return true;
    }
    validateZccAdoptionParitySemantics.errors = errors;
    return false;
  };
