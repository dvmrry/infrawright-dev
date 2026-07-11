import type { ErrorObject } from "ajv/dist/2020.js";

import { sortedStrings } from "../json/python-compatible.js";

export const ZCC_PULL_MATERIALIZATION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-materialization-semantics";
export const ZCC_PULL_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-materialization-request-semantics";

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
    schemaPath: `#/${ZCC_PULL_MATERIALIZATION_SEMANTICS_KEYWORD}`,
    keyword: ZCC_PULL_MATERIALIZATION_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

export interface ZccPullMaterializationSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce publication partitions and joins to the fresh parity evidence. */
export const validateZccPullMaterializationSemantics:
  ZccPullMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const result = record(data);
    const publication = record(result?.publication);
    const verification = record(result?.verification);
    const parity = record(verification?.parity);
    const artifacts = record(parity?.artifacts);
    if (
      result === null
      || publication === null
      || verification === null
      || parity === null
      || artifacts === null
    ) {
      delete validateZccPullMaterializationSemantics.errors;
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

    if (result.resource_type !== verification.resource_type) {
      push(
        "/resource_type",
        "verification_join",
        "materialized resource must match verification evidence",
      );
    }
    if (result.tenant !== verification.tenant) {
      push(
        "/tenant",
        "verification_join",
        "materialized tenant must match verification evidence",
      );
    }

    const created = strings(publication.created);
    const reused = strings(publication.reused);
    if (created !== null && reused !== null) {
      const sortedCreated = sortedStrings(new Set(created));
      const sortedReused = sortedStrings(new Set(reused));
      if (!sameStrings(created, sortedCreated)) {
        push(
          "/publication/created",
          "publication_order",
          "created artifact names must be sorted and unique",
        );
      }
      if (!sameStrings(reused, sortedReused)) {
        push(
          "/publication/reused",
          "publication_order",
          "reused artifact names must be sorted and unique",
        );
      }
      if (created.some((name) => reused.includes(name))) {
        push(
          "/publication",
          "publication_partition",
          "created and reused artifacts must be disjoint",
        );
      }
      const applicable = result.resource_type === "zcc_trusted_network"
        ? ["imports", "lookup", "tfvars"]
        : ["imports", "tfvars"];
      const published = sortedStrings(new Set([...created, ...reused]));
      if (!sameStrings(published, applicable)) {
        push(
          "/publication",
          "publication_partition",
          "created and reused artifacts must partition every applicable artifact",
        );
      }
    }

    const applicableNames = result.resource_type === "zcc_trusted_network"
      ? ["imports", "lookup", "tfvars"]
      : ["imports", "tfvars"];
    for (const name of applicableNames) {
      const artifact = record(artifacts[name]);
      if (artifact !== null && artifact.status !== "match") {
        push(
          `/verification/parity/artifacts/${name}/status`,
          "verification_status",
          "completed materialization requires exact final artifact parity",
        );
      }
    }

    if (errors.length === 0) {
      delete validateZccPullMaterializationSemantics.errors;
      return true;
    }
    validateZccPullMaterializationSemantics.errors = errors;
    return false;
  };

/** Bind the materialization selection to the independently stored assertion. */
export const validateZccPullMaterializationRequestSemantics:
  ZccPullMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const input = record(data);
    const assertion = record(input?.assertion);
    if (input === null || assertion === null) {
      delete validateZccPullMaterializationRequestSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push({
        ...semanticError(
          `${dataContext?.instancePath ?? ""}${instancePath}`,
          rule,
          message,
        ),
        schemaPath:
          `#/${ZCC_PULL_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD}`,
        keyword: ZCC_PULL_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
      });
    };
    if (input.tenant !== assertion.tenant) {
      push(
        "/tenant",
        "assertion_join",
        "requested tenant must match the parity assertion",
      );
    }
    if (input.resource_type !== assertion.resource_type) {
      push(
        "/resource_type",
        "assertion_join",
        "requested resource must match the parity assertion",
      );
    }
    if (errors.length === 0) {
      delete validateZccPullMaterializationRequestSemantics.errors;
      return true;
    }
    validateZccPullMaterializationRequestSemantics.errors = errors;
    return false;
  };
