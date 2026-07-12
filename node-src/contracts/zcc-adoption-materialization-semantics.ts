import type { ErrorObject } from "ajv/dist/2020.js";

import { sortedStrings } from "../json/python-compatible.js";

export const ZCC_ADOPTION_MATERIALIZATION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-adoption-materialization-semantics";
export const ZCC_ADOPTION_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-adoption-materialization-request-semantics";

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

export interface ZccAdoptionMaterializationSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce result joins and the complete created/reused artifact partition. */
export const validateZccAdoptionMaterializationSemantics:
  ZccAdoptionMaterializationSemanticValidator = (
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
      delete validateZccAdoptionMaterializationSemantics.errors;
      return true;
    }

    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_ADOPTION_MATERIALIZATION_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    if (result.resource_type !== verification.resource_type) {
      push(
        "/resource_type",
        "verification_join",
        "materialized resource must match provider-observed verification",
      );
    }
    if (result.tenant !== verification.tenant) {
      push(
        "/tenant",
        "verification_join",
        "materialized tenant must match provider-observed verification",
      );
    }

    const applicable = result.resource_type === "zcc_trusted_network"
      ? ["imports", "lookup", "tfvars"]
      : ["imports", "tfvars"];
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
      const published = sortedStrings(new Set([...created, ...reused]));
      if (!sameStrings(published, applicable)) {
        push(
          "/publication",
          "publication_partition",
          "created and reused artifacts must partition every applicable artifact",
        );
      }
    }
    for (const name of applicable) {
      const artifact = record(artifacts[name]);
      if (artifact !== null && artifact.status !== "equal") {
        push(
          `/verification/parity/artifacts/${name}/status`,
          "verification_status",
          "completed materialization requires exact final adoption parity",
        );
      }
    }

    if (errors.length === 0) {
      delete validateZccAdoptionMaterializationSemantics.errors;
      return true;
    }
    validateZccAdoptionMaterializationSemantics.errors = errors;
    return false;
  };

/** Bind request coordinates to the complete independently stored assertion. */
export const validateZccAdoptionMaterializationRequestSemantics:
  ZccAdoptionMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const input = record(data);
    const assertion = record(input?.assertion);
    if (input === null || assertion === null) {
      delete validateZccAdoptionMaterializationRequestSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_ADOPTION_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    if (input.mode !== assertion.mode) {
      push("/mode", "assertion_join", "requested mode must match the assertion");
    }
    if (input.tenant !== assertion.tenant) {
      push("/tenant", "assertion_join", "requested tenant must match the assertion");
    }
    if (input.resource_type !== assertion.resource_type) {
      push(
        "/resource_type",
        "assertion_join",
        "requested resource must match the assertion",
      );
    }
    if (errors.length === 0) {
      delete validateZccAdoptionMaterializationRequestSemantics.errors;
      return true;
    }
    validateZccAdoptionMaterializationRequestSemantics.errors = errors;
    return false;
  };
