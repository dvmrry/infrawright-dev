import type { ErrorObject } from "ajv/dist/2020.js";

import { zccPullRefreshParityRequestSha } from "../domain/zcc-pull-refresh-fingerprints.js";

export const ZCC_PULL_REFRESH_PENDING_TRANSITION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-pending-transition-semantics";
export const ZCC_PULL_REFRESH_MATERIALIZATION_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-materialization-semantics";
export const ZCC_PULL_REFRESH_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-materialization-request-semantics";

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

export interface ZccPullRefreshMaterializationSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** A move fence must describe exactly whether safe move bytes are expected. */
export const validateZccPullRefreshPendingTransitionSemantics:
  ZccPullRefreshMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const marker = record(data);
    const desiredMove = record(marker?.desired_move);
    if (marker === null || desiredMove === null) {
      delete validateZccPullRefreshPendingTransitionSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const count = marker.safe_move_count;
    const state = desiredMove.state;
    if (
      typeof count === "number"
      && ((count === 0 && state !== "absent") || (count > 0 && state !== "present"))
    ) {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_PENDING_TRANSITION_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}/desired_move`,
        "move_state_join",
        "desired move state must be present exactly when safe_move_count is positive",
      ));
    }
    if (
      state === "present"
      && typeof desiredMove.size_bytes === "number"
      && desiredMove.size_bytes < 1
    ) {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_PENDING_TRANSITION_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}/desired_move/size_bytes`,
        "move_size",
        "a present desired move artifact must be non-empty",
      ));
    }
    if (errors.length === 0) {
      delete validateZccPullRefreshPendingTransitionSemantics.errors;
      return true;
    }
    validateZccPullRefreshPendingTransitionSemantics.errors = errors;
    return false;
  };

/** Enforce the imports-last report's cross-field state machine. */
export const validateZccPullRefreshMaterializationSemantics:
  ZccPullRefreshMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const result = record(data);
    const publication = record(result?.publication);
    const transition = record(result?.transition);
    const verification = record(result?.verification);
    const artifacts = record(verification?.artifacts);
    if (
      result === null
      || publication === null
      || transition === null
      || verification === null
      || artifacts === null
    ) {
      delete validateZccPullRefreshMaterializationSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_MATERIALIZATION_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };

    const awaitingApply = result.status === "awaiting_apply";
    if (
      transition.final
      !== (awaitingApply ? "committed" : "already_complete")
    ) {
      push(
        "/status",
        "transition_status_join",
        "materialization status must match the final transition state",
      );
    }
    if (
      transition.next_action
      !== (awaitingApply ? "apply_moves_then_ack" : "none")
    ) {
      push(
        "/transition/next_action",
        "next_action_join",
        "next action must follow the final transition state",
      );
    }

    const moves = record(artifacts.moves);
    const pendingMoves = record(artifacts.pending_moves);
    if (
      moves !== null
      && pendingMoves !== null
      && (
        awaitingApply
          ? moves.state !== "present"
            || pendingMoves.state !== "present"
            || typeof moves.size_bytes !== "number"
            || moves.size_bytes < 1
            || typeof pendingMoves.size_bytes !== "number"
            || pendingMoves.size_bytes < 1
          : moves.state !== "absent" || pendingMoves.state !== "absent"
      )
    ) {
      push(
        "/verification/artifacts",
        "move_fence_join",
        "move and pending-marker states must match the final transition state",
      );
    }
    for (const role of ["tfvars", "imports"] as const) {
      const state = record(artifacts[role]);
      if (state !== null && state.state !== "present") {
        push(
          `/verification/artifacts/${role}`,
          "required_artifact_state",
          "completed publication requires tfvars and imports to be present",
        );
      }
    }
    const lookup = record(artifacts.lookup);
    if (
      lookup !== null
      && lookup.state !== (
        result.resource_type === "zcc_trusted_network" ? "present" : "absent"
      )
    ) {
      push(
        "/verification/artifacts/lookup",
        "lookup_resource_join",
        "lookup state must match the selected resource contract",
      );
    }
    for (const role of ["alternate_hcl", "generated_bindings"] as const) {
      const state = record(artifacts[role]);
      if (state !== null && state.state !== "absent") {
        push(
          `/verification/artifacts/${role}`,
          "reserved_artifact_state",
          "reserved refresh artifacts must remain absent",
        );
      }
    }

    const advanced = strings(publication.advanced);
    if (advanced !== null) {
      const order = ["lookup", "moves", "tfvars", "imports"];
      const positions = advanced.map((role) => order.indexOf(role));
      if (positions.some((position, index) => index > 0 && position <= positions[index - 1]!)) {
        push(
          "/publication/advanced",
          "publication_order",
          "advanced roles must preserve lookup, moves, tfvars, imports order",
        );
      }
      if (
        (transition.initial === "committed"
          || transition.initial === "already_complete")
        && advanced.length !== 0
      ) {
        push(
          "/publication/advanced",
          "terminal_publication",
          "an initially terminal transition must not advance payload roles",
        );
      }
      if (
        (transition.initial === "precommit"
          || transition.initial === "pending_prefix")
        && advanced.length === 0
      ) {
        push(
          "/publication/advanced",
          "active_publication",
          "an initially active transition must advance at least one payload role",
        );
      }
      if (!awaitingApply && advanced.includes("moves")) {
        push(
          "/publication/advanced",
          "move_publication_join",
          "a complete no-move transition cannot advance a move artifact",
        );
      }
    }
    if (transition.initial === "already_complete" && awaitingApply) {
      push(
        "/transition/final",
        "already_complete_join",
        "an already-complete transition cannot await move application",
      );
    }

    if (errors.length === 0) {
      delete validateZccPullRefreshMaterializationSemantics.errors;
      return true;
    }
    validateZccPullRefreshMaterializationSemantics.errors = errors;
    return false;
  };

/** Bind refresh publication coordinates to the complete ready assertion. */
export const validateZccPullRefreshMaterializationRequestSemantics:
  ZccPullRefreshMaterializationSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const request = record(data);
    const input = record(request?.input);
    if (
      request === null
      || input === null
      || request.operation !== "materialize_pull_artifacts"
      || input.mode !== "refresh"
    ) {
      delete validateZccPullRefreshMaterializationRequestSemantics.errors;
      return true;
    }
    const context = record(request.context);
    const assertion = record(input.assertion);
    const seed = record(assertion?.seed);
    const bindings = record(seed?.bindings);
    const candidateBinding = record(bindings?.candidate);
    if (
      context === null
      || assertion === null
      || candidateBinding === null
    ) {
      delete validateZccPullRefreshMaterializationRequestSemantics.errors;
      return true;
    }
    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        ZCC_PULL_REFRESH_MATERIALIZATION_REQUEST_SEMANTICS_KEYWORD,
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };
    if (input.tenant !== assertion.tenant) {
      push(
        "/input/tenant",
        "assertion_join",
        "requested tenant must match the refresh parity assertion",
      );
    }
    if (input.resource_type !== assertion.resource_type) {
      push(
        "/input/resource_type",
        "assertion_join",
        "requested resource must match the refresh parity assertion",
      );
    }
    try {
      const expectedRequestSha = zccPullRefreshParityRequestSha({
        context: {
          workspace: context.workspace as string,
          deployment: context.deployment as string,
          root_catalog: context.root_catalog as string,
        },
        tenant: input.tenant as string,
        resourceType: input.resource_type as string,
      });
      if (candidateBinding.request_sha256 !== expectedRequestSha) {
        push(
          "/context",
          "request_hash_join",
          "requested context must match the candidate binding in the assertion",
        );
      }
    } catch {
      push(
        "/context",
        "request_hash_join",
        "requested context could not be bound to the assertion",
      );
    }
    if (errors.length === 0) {
      delete validateZccPullRefreshMaterializationRequestSemantics.errors;
      return true;
    }
    validateZccPullRefreshMaterializationRequestSemantics.errors = errors;
    return false;
  };
