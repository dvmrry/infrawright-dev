import assert from "node:assert/strict";
import test from "node:test";

import {
  validateZccPullRefreshMaterialization,
  validateZccPullRefreshPendingTransition,
} from "../node-src/contracts/validators.js";

const SHA = "a".repeat(64);
const OTHER_SHA = "b".repeat(64);

function present(sha256 = SHA, sizeBytes = 1) {
  return { state: "present" as const, sha256, size_bytes: sizeBytes };
}

function marker() {
  return {
    kind: "infrawright.zcc_pull_refresh_pending_transition",
    schema_version: 1,
    mode: "refresh",
    product: "zcc",
    resource_type: "zcc_forwarding_profile",
    tenant: "refresh_semantics",
    candidate_request_sha256: SHA,
    assertion_sha256: OTHER_SHA,
    baseline_fingerprint_sha256: SHA,
    transition_sha256: OTHER_SHA,
    safe_move_count: 1,
    desired_move: present(OTHER_SHA, 42),
  } as const;
}

function materialization() {
  return {
    kind: "infrawright.zcc_pull_refresh_materialization",
    schema_version: 1,
    mode: "refresh",
    product: "zcc",
    resource_type: "zcc_forwarding_profile",
    tenant: "refresh_semantics",
    status: "awaiting_apply",
    publication: {
      policy: "replace_or_verify_exact_imports_last",
      advanced: ["moves", "tfvars", "imports"],
    },
    transition: {
      initial: "precommit",
      final: "committed",
      next_action: "apply_moves_then_ack",
    },
    verification: {
      candidate_request_sha256: SHA,
      assertion_sha256: OTHER_SHA,
      baseline_fingerprint_sha256: SHA,
      transition_sha256: OTHER_SHA,
      artifacts: {
        tfvars: present(SHA, 10),
        imports: present(OTHER_SHA, 20),
        lookup: { state: "absent" },
        moves: present(SHA, 30),
        pending_moves: present(OTHER_SHA, 40),
        alternate_hcl: { state: "absent" },
        generated_bindings: { state: "absent" },
      },
    },
  } as const;
}

function rules(errors: typeof validateZccPullRefreshMaterialization.errors): readonly string[] {
  return (errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown };
    return typeof params.rule === "string" ? params.rule : error.keyword;
  });
}

test("pending marker semantics bind move count to the desired move descriptor", () => {
  const valid = marker();
  assert.equal(validateZccPullRefreshPendingTransition(valid), true);

  const missingMove = structuredClone(valid) as unknown as {
    safe_move_count: number;
    desired_move: { state: "absent" };
  };
  missingMove.desired_move = { state: "absent" };
  assert.equal(validateZccPullRefreshPendingTransition(missingMove), false);
  assert.ok(rules(validateZccPullRefreshPendingTransition.errors).includes("move_state_join"));

  const unexpectedMove = structuredClone(valid) as unknown as {
    safe_move_count: number;
  };
  unexpectedMove.safe_move_count = 0;
  assert.equal(validateZccPullRefreshPendingTransition(unexpectedMove), false);
  assert.ok(rules(validateZccPullRefreshPendingTransition.errors).includes("move_state_join"));

  const emptyMoveBytes = structuredClone(valid) as unknown as {
    desired_move: { state: "present"; sha256: string; size_bytes: number };
  };
  emptyMoveBytes.desired_move.size_bytes = 0;
  assert.equal(validateZccPullRefreshPendingTransition(emptyMoveBytes), false);
});

test("refresh materialization semantics reject every cross-field state-machine split", () => {
  assert.equal(validateZccPullRefreshMaterialization(materialization()), true);

  const cases = [
    {
      rule: "transition_status_join",
      mutate(value: ReturnType<typeof materialization>): void {
        (value.transition as { final: string }).final = "already_complete";
      },
    },
    {
      rule: "next_action_join",
      mutate(value: ReturnType<typeof materialization>): void {
        (value.transition as { next_action: string }).next_action = "none";
      },
    },
    {
      rule: "move_fence_join",
      mutate(value: ReturnType<typeof materialization>): void {
        (value.verification.artifacts as { moves: { state: string } }).moves = {
          state: "absent",
        };
      },
    },
    {
      rule: "required_artifact_state",
      mutate(value: ReturnType<typeof materialization>): void {
        (value.verification.artifacts as { imports: { state: string } }).imports = {
          state: "absent",
        };
      },
    },
    {
      rule: "lookup_resource_join",
      mutate(value: ReturnType<typeof materialization>): void {
        (value as { resource_type: string }).resource_type = "zcc_trusted_network";
      },
    },
    {
      rule: "reserved_artifact_state",
      mutate(value: ReturnType<typeof materialization>): void {
        (
          value.verification.artifacts as unknown as {
            generated_bindings: ReturnType<typeof present>;
          }
        ).generated_bindings = present();
      },
    },
    {
      rule: "publication_order",
      mutate(value: ReturnType<typeof materialization>): void {
        (value.publication as unknown as { advanced: string[] }).advanced = [
          "imports",
          "tfvars",
        ];
      },
    },
  ] as const;

  for (const scenario of cases) {
    const value = structuredClone(materialization());
    scenario.mutate(value);
    assert.equal(validateZccPullRefreshMaterialization(value), false, scenario.rule);
    assert.ok(
      rules(validateZccPullRefreshMaterialization.errors).includes(scenario.rule),
      `${scenario.rule}: ${JSON.stringify(validateZccPullRefreshMaterialization.errors)}`,
    );
  }
});

test("complete no-move receipts require terminal no-action evidence", () => {
  const value = structuredClone(materialization()) as unknown as {
    status: string;
    publication: { advanced: string[] };
    transition: { initial: string; final: string; next_action: string };
    verification: {
      artifacts: {
        moves: { state: string };
        pending_moves: { state: string };
      };
    };
  };
  value.status = "complete";
  value.publication.advanced = [];
  value.transition = {
    initial: "already_complete",
    final: "already_complete",
    next_action: "none",
  };
  value.verification.artifacts.moves = { state: "absent" };
  value.verification.artifacts.pending_moves = { state: "absent" };
  assert.equal(
    validateZccPullRefreshMaterialization(value),
    true,
    JSON.stringify(validateZccPullRefreshMaterialization.errors),
  );

  value.publication.advanced = ["imports"];
  assert.equal(validateZccPullRefreshMaterialization(value), false);
  assert.ok(rules(validateZccPullRefreshMaterialization.errors).includes("terminal_publication"));

  value.publication.advanced = [];
  value.status = "awaiting_apply";
  value.transition.final = "committed";
  value.transition.next_action = "apply_moves_then_ack";
  value.verification.artifacts.moves = present() as unknown as { state: string };
  value.verification.artifacts.pending_moves = present() as unknown as { state: string };
  assert.equal(validateZccPullRefreshMaterialization(value), false);
  assert.ok(rules(validateZccPullRefreshMaterialization.errors).includes("already_complete_join"));
});

test("publication evidence cannot erase an asserted transition advance", () => {
  for (const initial of ["precommit", "pending_prefix"] as const) {
    const value = structuredClone(materialization()) as unknown as {
      publication: { advanced: string[] };
      transition: { initial: string };
    };
    value.transition.initial = initial;
    value.publication.advanced = [];
    assert.equal(
      validateZccPullRefreshMaterialization(value),
      false,
      initial,
    );
  }

  const complete = structuredClone(materialization()) as unknown as {
    status: string;
    publication: { advanced: string[] };
    transition: { initial: string; final: string; next_action: string };
    verification: {
      artifacts: {
        moves: { state: string };
        pending_moves: { state: string };
      };
    };
  };
  complete.status = "complete";
  complete.publication.advanced = ["moves"];
  complete.transition = {
    initial: "precommit",
    final: "already_complete",
    next_action: "none",
  };
  complete.verification.artifacts.moves = { state: "absent" };
  complete.verification.artifacts.pending_moves = { state: "absent" };
  assert.equal(validateZccPullRefreshMaterialization(complete), false);
});
