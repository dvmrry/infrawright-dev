import { createHash } from "node:crypto";

import type { ErrorObject } from "ajv/dist/2020.js";

import {
  zccPullRefreshParityRequestSha,
  zccRefreshEvidenceDigest,
} from "../domain/zcc-pull-refresh-fingerprints.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../json/python-compatible.js";

export const ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-acknowledgement-request-semantics";
export const ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-acknowledgement-semantics";

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

function own(value: JsonRecord | null, key: string): unknown {
  return value === null
    ? undefined
    : Object.getOwnPropertyDescriptor(value, key)?.value;
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

function contentState(value: unknown): unknown {
  const state = record(value);
  if (own(state, "state") !== "present") {
    return { state: own(state, "state") };
  }
  return {
    state: "present",
    sha256: own(state, "sha256"),
    size_bytes: own(state, "size_bytes"),
  };
}

function pendingMarkerState(options: {
  readonly tenant: unknown;
  readonly resourceType: unknown;
  readonly candidateRequestSha256: unknown;
  readonly assertionSha256: unknown;
  readonly baselineFingerprintSha256: unknown;
  readonly transitionSha256: unknown;
  readonly safeMoveCount: unknown;
  readonly desiredMove: unknown;
}): unknown {
  const marker = {
    kind: "infrawright.zcc_pull_refresh_pending_transition",
    schema_version: 1,
    mode: "refresh",
    product: "zcc",
    resource_type: options.resourceType,
    tenant: options.tenant,
    candidate_request_sha256: options.candidateRequestSha256,
    assertion_sha256: options.assertionSha256,
    baseline_fingerprint_sha256: options.baselineFingerprintSha256,
    transition_sha256: options.transitionSha256,
    safe_move_count: options.safeMoveCount,
    desired_move: contentState(options.desiredMove),
  };
  try {
    const bytes = Buffer.from(
      renderPythonCompatibleJson(marker as unknown as JsonValue),
      "utf8",
    );
    return {
      state: "present",
      sha256: createHash("sha256").update(bytes).digest("hex"),
      size_bytes: bytes.length,
    };
  } catch {
    return null;
  }
}

function stringList(value: unknown): readonly string[] | null {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string")
    ? value
    : null;
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

export interface ZccPullRefreshAcknowledgementSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Bind an external acknowledgement to one exact ready publication. */
export const validateZccPullRefreshAcknowledgementRequestSemantics:
  ZccPullRefreshAcknowledgementSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const request = record(data);
    const input = record(own(request, "input"));
    if (
      request === null
      || input === null
      || own(request, "operation") !== "acknowledge_pull_refresh"
    ) {
      delete validateZccPullRefreshAcknowledgementRequestSemantics.errors;
      return true;
    }
    const context = record(own(request, "context"));
    const assertion = record(own(input, "assertion"));
    const seed = record(own(assertion, "seed"));
    const bindings = record(own(seed, "bindings"));
    const candidateBinding = record(own(bindings, "candidate"));
    const candidate = record(own(assertion, "candidate"));
    const desired = record(own(candidate, "desired"));
    const publication = record(own(input, "publication"));
    const publicationTransition = record(own(publication, "transition"));
    const verification = record(own(publication, "verification"));
    const artifacts = record(own(verification, "artifacts"));
    if (
      context === null
      || assertion === null
      || seed === null
      || candidateBinding === null
      || candidate === null
      || desired === null
      || publication === null
      || publicationTransition === null
      || verification === null
      || artifacts === null
    ) {
      delete validateZccPullRefreshAcknowledgementRequestSemantics.errors;
      return true;
    }

    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_REQUEST_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    const tenant = own(input, "tenant");
    const resourceType = own(input, "resource_type");
    if (
      tenant !== own(assertion, "tenant")
      || tenant !== own(publication, "tenant")
      || resourceType !== own(assertion, "resource_type")
      || resourceType !== own(publication, "resource_type")
    ) {
      push(
        "/input",
        "coordinate_join",
        "acknowledgement coordinates must match the assertion and publication",
      );
    }
    if (
      own(assertion, "status") !== "ready"
      || own(seed, "status") !== "ready"
      || own(candidate, "status") !== "ready"
      || own(record(own(assertion, "parity")), "status") !== "equal"
    ) {
      push(
        "/input/assertion",
        "ready_assertion",
        "acknowledgement requires a complete ready parity assertion",
      );
    }
    if (
      own(publication, "status") !== "awaiting_apply"
      || own(publicationTransition, "final") !== "committed"
      || own(publicationTransition, "next_action") !== "apply_moves_then_ack"
    ) {
      push(
        "/input/publication",
        "awaiting_publication",
        "acknowledgement requires an awaiting-apply publication receipt",
      );
    }

    let expectedRequestSha: string | null = null;
    try {
      expectedRequestSha = zccPullRefreshParityRequestSha({
        context: {
          workspace: own(context, "workspace") as string,
          deployment: own(context, "deployment") as string,
          root_catalog: own(context, "root_catalog") as string,
        },
        tenant: tenant as string,
        resourceType: resourceType as string,
      });
      if (
        own(candidateBinding, "request_sha256") !== expectedRequestSha
        || own(verification, "candidate_request_sha256") !== expectedRequestSha
      ) {
        push(
          "/context",
          "request_hash_join",
          "requested context must match the assertion and publication bindings",
        );
      }
    } catch {
      push(
        "/context",
        "request_hash_join",
        "requested context could not be bound to the acknowledgement",
      );
    }

    for (const [publicationField, assertionValue] of [
      ["assertion_sha256", own(assertion, "assertion_sha256")],
      ["baseline_fingerprint_sha256", own(candidate, "baseline_fingerprint_sha256")],
      ["transition_sha256", own(candidate, "transition_sha256")],
    ] as const) {
      if (own(verification, publicationField) !== assertionValue) {
        push(
          `/input/publication/verification/${publicationField}`,
          "publication_hash_join",
          "publication verification hashes must match the parity assertion",
        );
      }
    }
    for (const role of ["tfvars", "imports", "lookup", "moves"] as const) {
      if (!same(contentState(own(artifacts, role)), contentState(own(desired, role)))) {
        push(
          `/input/publication/verification/artifacts/${role}`,
          "publication_artifact_join",
          "publication artifact evidence must match the asserted desired state",
        );
      }
    }
    const expectedMarker = expectedRequestSha === null
      ? null
      : pendingMarkerState({
          tenant,
          resourceType,
          candidateRequestSha256: expectedRequestSha,
          assertionSha256: own(assertion, "assertion_sha256"),
          baselineFingerprintSha256: own(candidate, "baseline_fingerprint_sha256"),
          transitionSha256: own(candidate, "transition_sha256"),
          safeMoveCount: own(record(own(candidate, "moves")), "safe_count"),
          desiredMove: own(desired, "moves"),
        });
    if (
      expectedMarker === null
      || !same(contentState(own(artifacts, "pending_moves")), expectedMarker)
    ) {
      push(
        "/input/publication/verification/artifacts/pending_moves",
        "pending_marker_join",
        "publication pending-marker evidence must match the exact asserted transition marker",
      );
    }

    if (errors.length === 0) {
      delete validateZccPullRefreshAcknowledgementRequestSemantics.errors;
      return true;
    }
    validateZccPullRefreshAcknowledgementRequestSemantics.errors = errors;
    return false;
  };

/** Enforce the content-free acknowledgement result's retirement state machine. */
export const validateZccPullRefreshAcknowledgementSemantics:
  ZccPullRefreshAcknowledgementSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const result = record(data);
    const retirement = record(own(result, "retirement"));
    const verification = record(own(result, "verification"));
    const artifacts = record(own(verification, "artifacts"));
    if (
      result === null
      || retirement === null
      || verification === null
      || artifacts === null
    ) {
      delete validateZccPullRefreshAcknowledgementSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_ACKNOWLEDGEMENT_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };

    const initial = own(retirement, "initial");
    const expectedRemoved = initial === "awaiting_apply"
      ? ["moves", "pending_moves"]
      : initial === "retirement_prefix"
        ? ["pending_moves"]
        : initial === "already_retired"
          ? []
          : null;
    const removed = stringList(own(retirement, "removed"));
    if (expectedRemoved !== null && !same(removed, expectedRemoved)) {
      push(
        "/retirement/removed",
        "retirement_order",
        "removed roles must exactly follow the initial retirement state",
      );
    }

    const lookup = record(own(artifacts, "lookup"));
    const expectedLookup = own(result, "resource_type") === "zcc_trusted_network"
      ? "present"
      : "absent";
    if (own(lookup, "state") !== expectedLookup) {
      push(
        "/verification/artifacts/lookup",
        "lookup_resource_join",
        "lookup state must match the selected resource contract",
      );
    }
    for (const role of [
      "moves",
      "pending_moves",
      "alternate_hcl",
      "generated_bindings",
    ] as const) {
      if (own(record(own(artifacts, role)), "state") !== "absent") {
        push(
          `/verification/artifacts/${role}`,
          "retired_artifact_state",
          "retired and reserved artifacts must be absent",
        );
      }
    }
    for (const role of ["tfvars", "imports"] as const) {
      if (own(record(own(artifacts, role)), "state") !== "present") {
        push(
          `/verification/artifacts/${role}`,
          "required_artifact_state",
          "retirement requires exact desired tfvars and imports",
        );
      }
    }

    if (errors.length === 0) {
      delete validateZccPullRefreshAcknowledgementSemantics.errors;
      return true;
    }
    validateZccPullRefreshAcknowledgementSemantics.errors = errors;
    return false;
  };
