import assert from "node:assert/strict";
import test from "node:test";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import {
  parsePolicyPath,
  policySelectorMatches,
} from "../node-src/domain/policy-paths.js";

function entry(path: string, extra: Record<string, unknown> = {}) {
  return { path, reason: "test", approved_by: "unit", ...extra };
}

test("policy paths retain exact indexes and wildcard only matches list indexes", () => {
  assert.deepEqual(parsePolicyPath('rules[*].tags["Name"]'), [
    "rules", "*", "tags", "Name",
  ]);
  assert.equal(policySelectorMatches(parsePolicyPath("rules[].status"), ["rules", 0, "status"]), true);
  assert.equal(policySelectorMatches(parsePolicyPath("rules[].status"), ["rules", "0", "status"]), false);
  assert.equal(policySelectorMatches(parsePolicyPath("rules[2].status"), ["rules", 2, "status"]), true);
  assert.deepEqual(parsePolicyPath(String.raw`rules[0].labels["a.b"]["q\"uote"]`), [
    "rules", 0, "labels", "a.b", 'q"uote',
  ]);
  assert.throws(() => parsePolicyPath("rules[١].status"));
});

test("plan tolerance matches updates and tracks stale entries", () => {
  const policy = new DriftPolicy({
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [entry("rules[].status")],
      },
    },
  });
  assert.deepEqual(policy.staleEntries({ modes: ["plan_tolerate"] }), [{
    resource_type: "sample_resource",
    mode: "plan_tolerate",
    path: "rules[].status",
  }]);
  assert.equal(
    policy.toleratesPlanPath("sample_resource", ["rules", 0, "status"], "update"),
    true,
  );
  assert.deepEqual(policy.staleEntries({ modes: ["plan_tolerate"] }), []);
});

test("canonical-equivalent exact selectors retain Python first-match order", () => {
  const policy = new DriftPolicy({
    version: 1,
    resource_types: {
      zpa_sample: {
        plan_tolerate: [
          { path: "field[0]", reason: "first", approved_by: "owner" },
          { path: "field[00]", reason: "alias", approved_by: "owner" },
        ],
      },
    },
  });
  assert.equal(policy.toleratesPlanPath("zpa_sample", ["field", 0], "update"), true);
  assert.deepEqual(policy.staleEntries({
    resourceTypes: new Set(["zpa_sample"]),
    modes: ["plan_tolerate"],
  }), [{
    resource_type: "zpa_sample",
    mode: "plan_tolerate",
    path: "field[00]",
  }]);
});

test("exact policy index cannot collide across path segment boundaries", () => {
  const policy = new DriftPolicy({
    version: 1,
    resource_types: {
      zpa_sample: {
        plan_tolerate: [{
          path: 'labels["x/string:y"]',
          reason: "map key",
          approved_by: "owner",
        }],
      },
    },
  });
  assert.equal(
    policy.toleratesPlanPath("zpa_sample", ["labels", "x", "y"], "update"),
    false,
  );
  assert.equal(
    policy.toleratesPlanPath("zpa_sample", ["labels", "x/string:y"], "update"),
    true,
  );
});

test("full policy validation rejects unsafe or ambiguous entries", () => {
  const invalid: unknown[] = [
    {},
    { version: true, resource_types: {} },
    { version: 2, resource_types: {} },
    { version: 1, resource_types: [], },
    { version: 1, resource_types: { bad: { surprise: [] } } },
    { version: 1, resource_types: { bad: { plan_tolerate: null } } },
    { version: 1, resource_types: { bad: { plan_tolerate: [entry("x", { actions: null })] } } },
    { version: 1, resource_types: { bad: { plan_tolerate: [entry("x", { actions: ["delete"] })] } } },
    { version: 1, resource_types: { bad: { plan_tolerate: [entry("x"), entry("x")] } } },
    { version: 1, resource_types: { bad: {
      projection_fill: [{ path: "x", source: "raw", reason: "r", approved_by: "a" }],
      projection_omit: [entry("x")],
    } } },
  ];
  for (const value of invalid) {
    assert.throws(() => new DriftPolicy(value));
  }
});

test("empty stale filters preserve the Python all-entry default", () => {
  const policy = new DriftPolicy({
    version: 1,
    resource_types: {
      sample_resource: {
        projection_omit: [entry("description")],
      },
    },
  });
  const expected = [{
    resource_type: "sample_resource",
    mode: "projection_omit",
    path: "description",
  }];
  assert.deepEqual(policy.staleEntries({ modes: [] }), expected);
  assert.deepEqual(policy.staleEntries({ resourceTypes: new Set() }), expected);
});

test("valid non-plan modes remain accepted by the complete validator", () => {
  assert.doesNotThrow(() => new DriftPolicy({
    version: 1,
    resource_types: {
      sample_resource: {
        projection_omit: [entry("description")],
        projection_sync: [{
          target_path: "categories",
          source_path: "raw_categories",
          reason: "test",
          approved_by: "unit",
        }],
        projection_fill: [{
          path: "profile",
          source: "raw_profile",
          reason: "test",
          approved_by: "unit",
        }],
        projection_omit_if: [entry("ports[].end", { values: [0, null] })],
      },
    },
  }));
});

test("policy validation preserves the frozen Python contract", () => {
  const valid = {
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [entry('rules[].tags["Name"]')],
      },
    },
  };
  const corpus: unknown[] = [
    null,
    valid,
    {},
    [],
    { version: 2, resource_types: {} },
    { version: 1, resource_types: { "bad-name": {} } },
    { version: 1, resource_types: { sample_resource: { unknown: [] } } },
    { version: 1, resource_types: { sample_resource: { plan_tolerate: null } } },
    { version: 1, resource_types: { sample_resource: { plan_tolerate: [entry("x", { actions: null })] } } },
    { version: 1, resource_types: { sample_resource: { plan_tolerate: [entry("x", { actions: [] })] } } },
    { version: 1, resource_types: { sample_resource: { plan_tolerate: [entry("x", { actions: ["update", "update"] })] } } },
    { version: 1, resource_types: { sample_resource: { projection_omit_if: [entry("x", { values: [] })] } } },
    { version: 1, resource_types: { sample_resource: { projection_omit_if: [entry("x", { values: [{}] })] } } },
    { version: 1, resource_types: { sample_resource: { projection_sync: [{
      target_path: "x",
      source_path: "x",
      reason: "r",
      approved_by: "a",
    }] } } },
    { version: 1, resource_types: { sample_resource: { projection_fill: [{
      path: "x[].y",
      source: "raw",
      reason: "r",
      approved_by: "a",
    }] } } },
    { version: 1, resource_types: { sample_resource: { projection_omit: [
      entry("x"),
      entry("x"),
    ] } } },
  ];
  const node = corpus.map((value) => {
    try {
      new DriftPolicy(value);
      return true;
    } catch {
      return false;
    }
  });
  // Frozen from engine.drift_policy at archive baseline 7d54261c. The exact
  // resurrection command and oracle authority are recorded in
  // docs/python-oracle-contracts.md.
  assert.deepEqual(node, [
    true,
    true,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
    false,
  ]);
});
