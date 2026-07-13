import assert from "node:assert/strict";
import test from "node:test";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import {
  applyGeneratedConfigPolicy,
  GeneratedConfigPolicyError,
} from "../node-src/domain/generated-config-policy.js";
import type { LoadedPackRoot } from "../node-src/metadata/loader.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const SCHEMA: JsonObject = {
  block: {
    attributes: {
      description: { optional: true, type: "string" },
      filled: { optional: true, type: ["list", "string"] },
      name: { required: true, type: "string" },
    },
    block_types: {
      rules: {
        nesting_mode: "list",
        block: {
          attributes: {
            label: { required: true, type: "string" },
            order: { optional: true, type: "number" },
          },
        },
      },
    },
  },
};

function root(): LoadedPackRoot {
  return { loadResourceSchema: async () => SCHEMA } as unknown as LoadedPackRoot;
}

function policy(resource: JsonObject): DriftPolicy {
  return new DriftPolicy({
    version: 1,
    resource_types: { sample_resource: resource },
  });
}

const ADDRESS = "sample_resource.iw_deadbeef";
const GENERATED = `resource "sample_resource" "iw_deadbeef" {
  description = "DROP"
  name        = "Example"

  rules {
    label = "one"
    order = 0
  }
  rules {
    label = "two"
    order = 2
  }
}
`;

test("generated config removes optional scalar and wildcard block leaves, then fills raw evidence", async () => {
  const selected = policy({
    projection_fill: [{
      path: "filled",
      source: "rawFilled",
      reason: "provider omitted it",
      approved_by: "unit",
    }],
    projection_omit: [{
      path: "description",
      reason: "provider default",
      approved_by: "unit",
    }],
    projection_omit_if: [{
      path: "rules[*].order",
      values: [0],
      reason: "sentinel",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED,
    policy: selected,
    rawItems: new Map([["example", { rawFilled: ["one", "two"] }]]),
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 3);
  assert.equal(result.text.includes("description"), false);
  assert.equal(result.text.includes("order = 0"), false);
  assert.equal(result.text.includes("order = 2"), true);
  assert.equal(result.text.includes('filled = [\n    "one",\n    "two",\n  ]'), true);
  assert.deepEqual(selected.staleEntries(), []);
});

test("exact-index generated-config omits are intentionally deferred to state projection", async () => {
  const selected = policy({
    projection_omit: [{
      path: "rules[0].order",
      reason: "exact plan index",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED,
    policy: selected,
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 0);
  assert.equal(result.text, GENERATED);
  assert.equal(selected.staleEntries().length, 1);
});

test("a value added by projection_fill is subsequently eligible for projection_omit_if", async () => {
  const selected = policy({
    projection_fill: [{
      path: "description",
      source: "rawDescription",
      reason: "provider omitted it",
      approved_by: "unit",
    }],
    projection_omit_if: [{
      path: "description",
      values: ["DROP"],
      reason: "sentinel",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: GENERATED.replace('  description = "DROP"\n', ""),
    policy: selected,
    rawItems: new Map([["example", { rawDescription: "DROP" }]]),
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.edits, 2);
  assert.equal(result.text.includes("description"), false);
  assert.deepEqual(selected.staleEntries(), []);
});

test("required-path, missing-config, missing-raw, missing-address, duplicate, and unexpected blocks fail closed", async () => {
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED,
      policy: policy({ projection_omit: [{ path: "name", reason: "bad", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /cannot projection_omit required path name/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: "",
      policy: policy({ projection_omit: [{ path: "description", reason: "test", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /generated import config is missing/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED,
      policy: policy({ projection_fill: [{ path: "filled", source: "rawFilled", reason: "test", approved_by: "unit" }] }),
      resourceType: "sample_resource",
      root: root(),
    }),
    /projection_fill requires raw_items/,
  );

  const active = policy({ projection_omit: [{ path: "description", reason: "test", approved_by: "unit" }] });
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"], ["sample_resource.iw_missing", "missing"]]),
      generatedConfig: GENERATED,
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /missing resource block/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: `${GENERATED}${GENERATED}`,
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /duplicate resource block/,
  );
  await assert.rejects(
    () => applyGeneratedConfigPolicy({
      addressToKey: new Map([[ADDRESS, "example"]]),
      generatedConfig: GENERATED.replaceAll("iw_deadbeef", "unexpected"),
      policy: active,
      resourceType: "sample_resource",
      root: root(),
    }),
    /unexpected resource block/,
  );
});

test("unknown compound values are preserved rather than treated as omit-if scalar evidence", async () => {
  const generated = GENERATED.replace(
    'description = "DROP"',
    'description = upper("DROP")',
  );
  const selected = policy({
    projection_omit_if: [{
      path: "description",
      values: ["DROP"],
      reason: "only literal evidence is safe",
      approved_by: "unit",
    }],
  });
  const result = await applyGeneratedConfigPolicy({
    addressToKey: new Map([[ADDRESS, "example"]]),
    generatedConfig: generated,
    policy: selected,
    resourceType: "sample_resource",
    root: root(),
  });
  assert.equal(result.text, generated);
  assert.equal(result.edits, 0);
  assert.equal(selected.staleEntries().length, 1);
});

test("policy error type remains stable for callers", () => {
  const error = new GeneratedConfigPolicyError("example");
  assert.equal(error.name, "GeneratedConfigPolicyError");
});
