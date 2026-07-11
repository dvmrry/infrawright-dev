import assert from "node:assert/strict";
import test from "node:test";

import {
  classifyZccRefreshTransition,
  ZCC_REFRESH_PAYLOAD_ORDER,
  type ZccRefreshContentState,
  type ZccRefreshObservedClass,
  type ZccRefreshPayloadRole,
  type ZccRefreshPendingMarkerState,
  type ZccRefreshTransitionAmbiguity,
  type ZccRefreshTransitionClassification,
  type ZccRefreshTransitionState,
} from "../node-src/domain/zcc-pull-refresh-transition.js";

const CURRENT_CLASSES = ["baseline", "desired", "foreign"] as const;
const MARKER_STATES = ["absent", "exact", "foreign"] as const;
const ABSENT = Object.freeze({ state: "absent" as const });

interface OracleObservation {
  readonly effective_order: readonly ZccRefreshPayloadRole[];
  readonly prefix_length: number;
  readonly observed: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshObservedClass>>;
}

interface OracleResolved extends OracleObservation {
  readonly state: ZccRefreshTransitionState;
  readonly remaining: readonly ZccRefreshPayloadRole[];
}

interface OracleAmbiguous extends OracleObservation {
  readonly state: "ambiguous";
  readonly reason: ZccRefreshTransitionAmbiguity;
  readonly role?: ZccRefreshPayloadRole | "alternate_hcl" | "generated_bindings";
}

type OracleResult = OracleResolved | OracleAmbiguous;

function present(seed: number): ZccRefreshContentState {
  const nibble = (seed % 15) + 1;
  return {
    state: "present",
    sha256: nibble.toString(16).repeat(64),
    size_bytes: seed + 1,
  };
}

function equalityMask(mask: number): Readonly<Record<ZccRefreshPayloadRole, boolean>> {
  const result = {} as Record<ZccRefreshPayloadRole, boolean>;
  for (const [index, role] of ZCC_REFRESH_PAYLOAD_ORDER.entries()) {
    result[role] = (mask & (1 << index)) !== 0;
  }
  return result;
}

function currentVector(index: number): Readonly<Record<
  ZccRefreshPayloadRole,
  typeof CURRENT_CLASSES[number]
>> {
  const result = {} as Record<
    ZccRefreshPayloadRole,
    typeof CURRENT_CLASSES[number]
  >;
  let remaining = index;
  for (const role of ZCC_REFRESH_PAYLOAD_ORDER) {
    result[role] = CURRENT_CLASSES[remaining % CURRENT_CLASSES.length]!;
    remaining = Math.floor(remaining / CURRENT_CLASSES.length);
  }
  return result;
}

function states(options: {
  readonly equal: Readonly<Record<ZccRefreshPayloadRole, boolean>>;
  readonly classes: Readonly<Record<
    ZccRefreshPayloadRole,
    typeof CURRENT_CLASSES[number]
  >>;
}): {
  readonly baseline: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
  readonly desired: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
  readonly current: Readonly<Record<ZccRefreshPayloadRole, ZccRefreshContentState>>;
} {
  const baseline = {} as Record<ZccRefreshPayloadRole, ZccRefreshContentState>;
  const desired = {} as Record<ZccRefreshPayloadRole, ZccRefreshContentState>;
  const current = {} as Record<ZccRefreshPayloadRole, ZccRefreshContentState>;
  for (const [index, role] of ZCC_REFRESH_PAYLOAD_ORDER.entries()) {
    const desiredState = options.equal[role] ? ABSENT : present(index + 1);
    const foreignState = present(index + 9);
    baseline[role] = ABSENT;
    desired[role] = desiredState;
    const selected = options.classes[role];
    current[role] = selected === "baseline"
      ? baseline[role]
      : selected === "desired"
        ? desiredState
        : foreignState;
  }
  return { baseline, desired, current };
}

function initialObserved(): Record<ZccRefreshPayloadRole, ZccRefreshObservedClass> {
  return {
    lookup: "foreign",
    moves: "foreign",
    tfvars: "foreign",
    imports: "foreign",
  };
}

function oracle(options: {
  readonly equal: Readonly<Record<ZccRefreshPayloadRole, boolean>>;
  readonly classes: Readonly<Record<
    ZccRefreshPayloadRole,
    typeof CURRENT_CLASSES[number]
  >>;
  readonly marker: ZccRefreshPendingMarkerState;
  readonly reserved?: "alternate_hcl" | "generated_bindings";
}): OracleResult {
  const effectiveOrder: ZccRefreshPayloadRole[] = [];
  const observed = initialObserved();
  let commonRoleChanged: ZccRefreshPayloadRole | undefined;
  let unknownRoleState: ZccRefreshPayloadRole | undefined;
  for (const role of ZCC_REFRESH_PAYLOAD_ORDER) {
    if (options.equal[role]) {
      observed[role] = options.classes[role] === "foreign" ? "foreign" : "common";
      if (observed[role] === "foreign" && commonRoleChanged === undefined) {
        commonRoleChanged = role;
      }
      continue;
    }
    effectiveOrder.push(role);
    observed[role] = options.classes[role];
    if (observed[role] === "foreign" && unknownRoleState === undefined) {
      unknownRoleState = role;
    }
  }

  if (options.marker === "foreign") {
    return {
      state: "ambiguous",
      reason: "foreign_marker",
      effective_order: effectiveOrder,
      prefix_length: 0,
      observed,
    };
  }
  if (options.reserved !== undefined) {
    return {
      state: "ambiguous",
      reason: "reserved_artifact",
      role: options.reserved,
      effective_order: effectiveOrder,
      prefix_length: 0,
      observed,
    };
  }
  if (commonRoleChanged !== undefined) {
    return {
      state: "ambiguous",
      reason: "common_role_changed",
      role: commonRoleChanged,
      effective_order: effectiveOrder,
      prefix_length: 0,
      observed,
    };
  }
  if (unknownRoleState !== undefined) {
    return {
      state: "ambiguous",
      reason: "unknown_role_state",
      role: unknownRoleState,
      effective_order: effectiveOrder,
      prefix_length: 0,
      observed,
    };
  }

  let prefixLength = 0;
  while (
    prefixLength < effectiveOrder.length
    && observed[effectiveOrder[prefixLength]!] === "desired"
  ) {
    prefixLength += 1;
  }
  if (
    effectiveOrder.slice(prefixLength).some((role) => observed[role] !== "baseline")
  ) {
    return {
      state: "ambiguous",
      reason: "non_prefix",
      effective_order: effectiveOrder,
      prefix_length: prefixLength,
      observed,
    };
  }

  const observation = {
    effective_order: effectiveOrder,
    prefix_length: prefixLength,
    observed,
  };
  const allBaseline = prefixLength === 0;
  const allDesired = prefixLength === effectiveOrder.length;
  const hasDesiredMoves = !options.equal.moves;
  if (options.marker === "exact") {
    return {
      state: allDesired ? "committed" : "pending_prefix",
      ...observation,
      remaining: effectiveOrder.slice(prefixLength),
    };
  }
  if (effectiveOrder.length === 0) {
    return hasDesiredMoves
      ? {
          state: "ambiguous",
          reason: "markerless_advanced_transition",
          ...observation,
        }
      : { state: "already_complete", ...observation, remaining: [] };
  }
  if (allBaseline) {
    return {
      state: "precommit",
      ...observation,
      remaining: effectiveOrder,
    };
  }
  if (allDesired && !hasDesiredMoves) {
    return { state: "already_complete", ...observation, remaining: [] };
  }
  if (allDesired) {
    return {
      state: "ambiguous",
      reason: "markerless_advanced_transition",
      ...observation,
    };
  }
  return {
    state: "ambiguous",
    reason: "markerless_advanced_transition",
    ...observation,
  };
}

function assertClassification(
  actual: ZccRefreshTransitionClassification,
  expected: OracleResult,
  context: string,
): void {
  assert.deepEqual(actual, expected, context);
  assert.equal(Object.isFrozen(actual), true, context);
  assert.equal(Object.isFrozen(actual.effective_order), true, context);
  assert.equal(Object.isFrozen(actual.observed), true, context);
  if (actual.state !== "ambiguous") {
    assert.equal(Object.isFrozen(actual.remaining), true, context);
  }
}

test("refresh transition classifier exhaustively matches the independent crash-state oracle", () => {
  let cases = 0;
  for (let mask = 0; mask < (1 << ZCC_REFRESH_PAYLOAD_ORDER.length); mask += 1) {
    const equal = equalityMask(mask);
    const vectorCount = CURRENT_CLASSES.length ** ZCC_REFRESH_PAYLOAD_ORDER.length;
    for (let vector = 0; vector < vectorCount; vector += 1) {
      const classes = currentVector(vector);
      const stateRecords = states({ equal, classes });
      for (const marker of MARKER_STATES) {
        const input = {
          ...stateRecords,
          reserved: {
            alternate_hcl: ABSENT,
            generated_bindings: ABSENT,
          },
          marker,
        } as const;
        const context = JSON.stringify({ mask, classes, marker });
        assertClassification(
          classifyZccRefreshTransition(input),
          oracle({ equal, classes, marker }),
          context,
        );
        cases += 1;
      }
    }
  }
  assert.equal(cases, 3_888);
});

test("each equality-collapsed common role fails closed when its bytes mutate", () => {
  const equal = equalityMask(0b1111);
  for (const role of ZCC_REFRESH_PAYLOAD_ORDER) {
    const classes = {
      lookup: "baseline",
      moves: "baseline",
      tfvars: "baseline",
      imports: "baseline",
      [role]: "foreign",
    } as const;
    assertClassification(
      classifyZccRefreshTransition({
        ...states({ equal, classes }),
        reserved: {
          alternate_hcl: ABSENT,
          generated_bindings: ABSENT,
        },
        marker: "absent",
      }),
      oracle({ equal, classes, marker: "absent" }),
      role,
    );
  }
});

test("both reserved artifact roles fail closed independently", () => {
  const equal = equalityMask(0);
  const classes = currentVector(0);
  const stateRecords = states({ equal, classes });
  for (const reserved of ["alternate_hcl", "generated_bindings"] as const) {
    const reservedStates = {
      alternate_hcl: ABSENT,
      generated_bindings: ABSENT,
      [reserved]: present(14),
    } as const;
    assertClassification(
      classifyZccRefreshTransition({
        ...stateRecords,
        reserved: reservedStates,
        marker: "absent",
      }),
      oracle({ equal, classes, marker: "absent", reserved }),
      reserved,
    );
  }
});

test("ambiguity precedence is marker, reserved, common, unknown, then prefix", () => {
  const equal = equalityMask(0b0010);
  const classes = {
    lookup: "foreign",
    moves: "foreign",
    tfvars: "baseline",
    imports: "baseline",
  } as const;
  const actual = classifyZccRefreshTransition({
    ...states({ equal, classes }),
    reserved: {
      alternate_hcl: present(13),
      generated_bindings: present(14),
    },
    marker: "foreign",
  });
  assertClassification(
    actual,
    oracle({ equal, classes, marker: "foreign", reserved: "alternate_hcl" }),
    "marker precedence",
  );
  assert.equal(actual.state, "ambiguous");
  assert.equal(actual.reason, "foreign_marker");
  assert.equal(actual.role, undefined);

  const reserved = classifyZccRefreshTransition({
    ...states({ equal, classes }),
    reserved: {
      alternate_hcl: present(13),
      generated_bindings: present(14),
    },
    marker: "absent",
  });
  assert.equal(reserved.state, "ambiguous");
  assert.equal(reserved.reason, "reserved_artifact");
  assert.equal(reserved.role, "alternate_hcl");

  const noReserved = {
    alternate_hcl: ABSENT,
    generated_bindings: ABSENT,
  } as const;
  const common = classifyZccRefreshTransition({
    ...states({ equal, classes }),
    reserved: noReserved,
    marker: "absent",
  });
  assert.equal(common.state, "ambiguous");
  assert.equal(common.reason, "common_role_changed");
  assert.equal(common.role, "moves");

  const unknownClasses = {
    lookup: "foreign",
    moves: "baseline",
    tfvars: "baseline",
    imports: "baseline",
  } as const;
  const unknown = classifyZccRefreshTransition({
    ...states({ equal, classes: unknownClasses }),
    reserved: noReserved,
    marker: "absent",
  });
  assert.equal(unknown.state, "ambiguous");
  assert.equal(unknown.reason, "unknown_role_state");
  assert.equal(unknown.role, "lookup");

  const nonPrefixClasses = {
    lookup: "baseline",
    moves: "baseline",
    tfvars: "desired",
    imports: "baseline",
  } as const;
  const nonPrefix = classifyZccRefreshTransition({
    ...states({ equal: equalityMask(0), classes: nonPrefixClasses }),
    reserved: noReserved,
    marker: "absent",
  });
  assert.equal(nonPrefix.state, "ambiguous");
  assert.equal(nonPrefix.reason, "non_prefix");
});

test("imports remains the final effective publication role", () => {
  assert.deepEqual(ZCC_REFRESH_PAYLOAD_ORDER, [
    "lookup",
    "moves",
    "tfvars",
    "imports",
  ]);
});
