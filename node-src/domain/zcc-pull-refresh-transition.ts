import { ProcessFailure } from "./errors.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";

/** Payload publication order for a refresh transaction. Imports are last. */
export const ZCC_REFRESH_PAYLOAD_ORDER = [
  "lookup",
  "moves",
  "tfvars",
  "imports",
] as const;

export type ZccRefreshPayloadRole = typeof ZCC_REFRESH_PAYLOAD_ORDER[number];

/** Path-free content evidence used by the classifier, marker, and result. */
export type ZccRefreshContentState =
  | { readonly state: "absent" }
  | {
      readonly state: "present";
      readonly sha256: string;
      readonly size_bytes: number;
    };

export type ZccRefreshObservedClass =
  | "baseline"
  | "desired"
  | "common"
  | "foreign";

export type ZccRefreshPendingMarkerState = "absent" | "exact" | "foreign";

/** Content-free, immutable crash-recovery fence. */
export interface ZccPullRefreshPendingTransition {
  readonly kind: "infrawright.zcc_pull_refresh_pending_transition";
  readonly schema_version: 1;
  readonly mode: "refresh";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly candidate_request_sha256: string;
  readonly assertion_sha256: string;
  readonly baseline_fingerprint_sha256: string;
  readonly transition_sha256: string;
  readonly safe_move_count: number;
  readonly desired_move: ZccRefreshContentState;
}

export interface ZccRefreshTransitionClassificationInput {
  readonly baseline: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
  readonly desired: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
  readonly current: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
  readonly reserved: {
    readonly alternate_hcl: ZccRefreshContentState;
    readonly generated_bindings: ZccRefreshContentState;
  };
  readonly marker: ZccRefreshPendingMarkerState;
}

export type ZccRefreshTransitionState =
  | "precommit"
  | "pending_prefix"
  | "committed"
  | "already_complete";

export type ZccRefreshTransitionAmbiguity =
  | "foreign_marker"
  | "reserved_artifact"
  | "common_role_changed"
  | "unknown_role_state"
  | "non_prefix"
  | "markerless_advanced_transition";

interface ZccRefreshTransitionObservation {
  readonly effective_order: readonly ZccRefreshPayloadRole[];
  readonly prefix_length: number;
  readonly observed: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshObservedClass>>;
}

export interface ZccRefreshTransitionResolved
  extends ZccRefreshTransitionObservation {
  readonly state: ZccRefreshTransitionState;
  readonly remaining: readonly ZccRefreshPayloadRole[];
}

export interface ZccRefreshTransitionAmbiguous
  extends ZccRefreshTransitionObservation {
  readonly state: "ambiguous";
  readonly reason: ZccRefreshTransitionAmbiguity;
  readonly role?: ZccRefreshPayloadRole | "alternate_hcl" | "generated_bindings";
}

export type ZccRefreshTransitionClassification =
  | ZccRefreshTransitionResolved
  | ZccRefreshTransitionAmbiguous;

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function validState(value: unknown): value is ZccRefreshContentState {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const record = value as Readonly<Record<string, unknown>>;
  const state = Object.getOwnPropertyDescriptor(record, "state")?.value;
  if (state === "absent") {
    return Object.keys(record).length === 1;
  }
  const sha256 = Object.getOwnPropertyDescriptor(record, "sha256")?.value;
  const size = Object.getOwnPropertyDescriptor(record, "size_bytes")?.value;
  return state === "present"
    && typeof sha256 === "string"
    && /^[0-9a-f]{64}$/u.test(sha256)
    && Number.isSafeInteger(size)
    && (size as number) >= 0;
}

function sameState(left: ZccRefreshContentState, right: ZccRefreshContentState): boolean {
  return left.state === "absent"
    ? right.state === "absent"
    : right.state === "present"
      && left.sha256 === right.sha256
      && left.size_bytes === right.size_bytes;
}

function initialObserved(): Record<ZccRefreshPayloadRole, ZccRefreshObservedClass> {
  return {
    lookup: "foreign",
    moves: "foreign",
    tfvars: "foreign",
    imports: "foreign",
  };
}

function resultObservation(
  effectiveOrder: readonly ZccRefreshPayloadRole[],
  prefixLength: number,
  observed: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshObservedClass>>,
): ZccRefreshTransitionObservation {
  return {
    effective_order: Object.freeze([...effectiveOrder]),
    prefix_length: prefixLength,
    observed: Object.freeze({ ...observed }),
  };
}

function ambiguous(
  reason: ZccRefreshTransitionAmbiguity,
  effectiveOrder: readonly ZccRefreshPayloadRole[],
  prefixLength: number,
  observed: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshObservedClass>>,
  role?: ZccRefreshTransitionAmbiguous["role"],
): ZccRefreshTransitionAmbiguous {
  return Object.freeze({
    state: "ambiguous" as const,
    reason,
    ...resultObservation(effectiveOrder, prefixLength, observed),
    ...(role === undefined ? {} : { role }),
  });
}

/**
 * Classify a live transaction from asserted content evidence.
 *
 * After roles for which baseline equals desired are eliminated, the only
 * valid in-flight vector is `D[0:k] + B[k:n]`. The pending marker is the
 * durable fence that distinguishes an in-flight/committed move transition
 * from an unrelated markerless workspace state.
 */
export function classifyZccRefreshTransition(
  input: ZccRefreshTransitionClassificationInput,
): ZccRefreshTransitionClassification {
  if (
    input === null
    || typeof input !== "object"
    || input.baseline === null
    || typeof input.baseline !== "object"
    || input.desired === null
    || typeof input.desired !== "object"
    || input.current === null
    || typeof input.current !== "object"
    || input.reserved === null
    || typeof input.reserved !== "object"
    || !["absent", "exact", "foreign"].includes(input.marker)
  ) {
    return fail(
      "INVALID_REFRESH_TRANSITION_INPUT",
      "refresh transition classification input is invalid",
    );
  }
  for (const role of ZCC_REFRESH_PAYLOAD_ORDER) {
    if (
      !Object.prototype.hasOwnProperty.call(input.baseline, role)
      || !Object.prototype.hasOwnProperty.call(input.desired, role)
      || !Object.prototype.hasOwnProperty.call(input.current, role)
      || !validState(input.baseline[role])
      || !validState(input.desired[role])
      || !validState(input.current[role])
    ) {
      return fail(
        "INVALID_REFRESH_TRANSITION_INPUT",
        "refresh transition classification input is invalid",
      );
    }
  }
  if (
    !validState(input.reserved.alternate_hcl)
    || !validState(input.reserved.generated_bindings)
  ) {
    return fail(
      "INVALID_REFRESH_TRANSITION_INPUT",
      "refresh transition classification input is invalid",
    );
  }

  const observed = initialObserved();
  const effectiveOrder: ZccRefreshPayloadRole[] = [];
  let commonRoleChanged: ZccRefreshPayloadRole | null = null;
  let unknownRoleState: ZccRefreshPayloadRole | null = null;
  for (const role of ZCC_REFRESH_PAYLOAD_ORDER) {
    const baseline = input.baseline[role];
    const desired = input.desired[role];
    const current = input.current[role];
    if (sameState(baseline, desired)) {
      observed[role] = sameState(current, baseline) ? "common" : "foreign";
      if (observed[role] === "foreign" && commonRoleChanged === null) {
        commonRoleChanged = role;
      }
      continue;
    }
    effectiveOrder.push(role);
    observed[role] = sameState(current, desired)
      ? "desired"
      : sameState(current, baseline)
        ? "baseline"
        : "foreign";
    if (observed[role] === "foreign" && unknownRoleState === null) {
      unknownRoleState = role;
    }
  }

  if (input.marker === "foreign") {
    return ambiguous("foreign_marker", effectiveOrder, 0, observed);
  }
  if (input.reserved.alternate_hcl.state !== "absent") {
    return ambiguous(
      "reserved_artifact",
      effectiveOrder,
      0,
      observed,
      "alternate_hcl",
    );
  }
  if (input.reserved.generated_bindings.state !== "absent") {
    return ambiguous(
      "reserved_artifact",
      effectiveOrder,
      0,
      observed,
      "generated_bindings",
    );
  }
  if (commonRoleChanged !== null) {
    return ambiguous(
      "common_role_changed",
      effectiveOrder,
      0,
      observed,
      commonRoleChanged,
    );
  }
  if (unknownRoleState !== null) {
    return ambiguous(
      "unknown_role_state",
      effectiveOrder,
      0,
      observed,
      unknownRoleState,
    );
  }

  let prefixLength = 0;
  while (
    prefixLength < effectiveOrder.length
    && observed[effectiveOrder[prefixLength]!] === "desired"
  ) {
    prefixLength += 1;
  }
  for (let index = prefixLength; index < effectiveOrder.length; index += 1) {
    if (observed[effectiveOrder[index]!] !== "baseline") {
      return ambiguous("non_prefix", effectiveOrder, prefixLength, observed);
    }
  }

  const observation = resultObservation(effectiveOrder, prefixLength, observed);
  const allBaseline = prefixLength === 0;
  const allDesired = prefixLength === effectiveOrder.length;
  const hasDesiredMoves = input.desired.moves.state === "present";

  if (input.marker === "exact") {
    const state = allDesired ? "committed" : "pending_prefix";
    return Object.freeze({
      state,
      ...observation,
      remaining: Object.freeze(effectiveOrder.slice(prefixLength)),
    });
  }
  if (effectiveOrder.length === 0) {
    return hasDesiredMoves
      ? ambiguous("markerless_advanced_transition", effectiveOrder, prefixLength, observed)
      : Object.freeze({
          state: "already_complete" as const,
          ...observation,
          remaining: Object.freeze([]),
        });
  }
  if (allBaseline) {
    return Object.freeze({
      state: "precommit" as const,
      ...observation,
      remaining: Object.freeze([...effectiveOrder]),
    });
  }
  if (allDesired && !hasDesiredMoves) {
    return Object.freeze({
      state: "already_complete" as const,
      ...observation,
      remaining: Object.freeze([]),
    });
  }
  if (allDesired) {
    return ambiguous("markerless_advanced_transition", effectiveOrder, prefixLength, observed);
  }
  return ambiguous("markerless_advanced_transition", effectiveOrder, prefixLength, observed);
}
