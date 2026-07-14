import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import {
  ProjectionError,
  projectProviderState,
  providerSchemaStatus,
  validateSensitiveMaskShape,
} from "../node-src/domain/state-project.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const SCHEMA: JsonObject = {
  block: {
    attributes: {
      computed_only: { computed: true, type: "string" },
      description: { optional: true, type: "string" },
      enabled: { optional: true, type: "bool" },
      filled: { optional: true, type: "string" },
      id: { computed: true, optional: true, type: "string" },
      labels: { optional: true, type: ["map", "string"] },
      name: { required: true, type: "string" },
      number_value: { optional: true, type: "number" },
      secret: { optional: true, sensitive: true, type: "string" },
      source_categories: { optional: true, type: ["set", "string"] },
      target_categories: { optional: true, type: ["set", "string"] },
    },
    block_types: {
      required_settings: {
        min_items: 1,
        nesting_mode: "single",
        block: {
          attributes: {
            mode: { required: true, type: "string" },
            computed_nested: { computed: true, type: "string" },
          },
        },
      },
      rules: {
        nesting_mode: "list",
        block: {
          attributes: {
            name: { required: true, type: "string" },
            order: { optional: true, type: "number" },
            computed_rule: { computed: true, type: "string" },
          },
        },
      },
      settings: {
        nesting_mode: "single",
        block: {
          attributes: {
            flag: { optional: true, type: "bool" },
            mode: { required: true, type: "string" },
          },
        },
      },
    },
  },
};

function root(schema: JsonObject = SCHEMA, override: JsonObject | null = null): LoadedPackRoot {
  return {
    loadResourceSchema: async () => schema,
    resources: new Map([["sample_resource", { override }]]),
  } as unknown as LoadedPackRoot;
}

function policy(resource: JsonObject): DriftPolicy {
  return new DriftPolicy({
    version: 1,
    resource_types: { sample_resource: resource },
  });
}

function plain(value: unknown): unknown {
  if (value instanceof LosslessNumber) return value.toString();
  if (Array.isArray(value)) return value.map(plain);
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, plain(child)]));
  }
  return value;
}

let committedRootPromise: Promise<LoadedPackRoot> | undefined;

function committedRoot(): Promise<LoadedPackRoot> {
  committedRootPromise ??= loadPackRoot({
    packsRoot: path.join(process.cwd(), "packs"),
    profilePath: path.join(process.cwd(), "packsets", "full.json"),
    catalogPath: path.join(process.cwd(), "packsets", "full.json"),
  });
  return committedRootPromise;
}

test("schema projection preserves writable false/zero/empty values and recursively removes computed state", async () => {
  const output = await projectProviderState({
    resourceType: "sample_resource",
    root: root(),
    stateValues: {
      computed_only: "drop",
      description: "",
      enabled: false,
      id: "provider-id",
      name: "Example",
      number_value: new LosslessNumber("9007199254740993"),
      required_settings: [{ computed_nested: "drop", mode: "strict" }],
      rules: [
        { computed_rule: "drop", name: "first", order: new LosslessNumber("0") },
        { name: "second" },
      ],
      settings: { flag: false, mode: "audit" },
    },
  });
  assert.deepEqual(plain(output), {
    description: "",
    enabled: false,
    name: "Example",
    number_value: "9007199254740993",
    required_settings: { mode: "strict" },
    rules: [{ name: "first", order: "0" }, { name: "second" }],
    settings: { flag: false, mode: "audit" },
  });
  assert.equal((output.number_value as LosslessNumber).toString(), "9007199254740993");
});

test("pack drop_if_default removes provider sentinels from projected tfvars", async () => {
  const output = await projectProviderState({
    resourceType: "sample_resource",
    root: root(SCHEMA, {
      drop_if_default: {
        number_value: 0,
        "rules.order": 0,
      },
    }),
    stateValues: {
      name: "Example",
      number_value: new LosslessNumber("0"),
      required_settings: [{ mode: "strict" }],
      rules: [
        { name: "first", order: new LosslessNumber("0") },
        { name: "second", order: new LosslessNumber("2") },
      ],
    },
  });

  assert.deepEqual(plain(output), {
    name: "Example",
    required_settings: { mode: "strict" },
    rules: [{ name: "first" }, { name: "second", order: "2" }],
  });
});

test("committed ZIA overrides remove provider empty-string sentinels", async () => {
  const loaded = await committedRoot();
  const network = await projectProviderState({
    resourceType: "zia_firewall_filtering_network_service",
    root: loaded,
    stateValues: { name: "Example", tag: "" },
  });
  assert.deepEqual(plain(network), { name: "Example" });

  const browser = await projectProviderState({
    resourceType: "zia_browser_control_policy",
    root: loaded,
    stateValues: { id: "browser_settings", plugin_check_frequency: "" },
  });
  assert.deepEqual(plain(browser), {});

  const retainedNetwork = await projectProviderState({
    resourceType: "zia_firewall_filtering_network_service",
    root: loaded,
    stateValues: { name: "Example", tag: "managed" },
  });
  assert.deepEqual(plain(retainedNetwork), { name: "Example", tag: "managed" });

  const retainedBrowser = await projectProviderState({
    resourceType: "zia_browser_control_policy",
    root: loaded,
    stateValues: { id: "browser_settings", plugin_check_frequency: "weekly" },
  });
  assert.deepEqual(plain(retainedBrowser), { plugin_check_frequency: "weekly" });
});

test("required attributes and required nested cardinality fail closed", async () => {
  await assert.rejects(
    () => projectProviderState({
      resourceType: "sample_resource",
      root: root(),
      stateValues: { required_settings: { mode: "strict" } },
    }),
    /required state path missing: name/,
  );
  await assert.rejects(
    () => projectProviderState({
      resourceType: "sample_resource",
      root: root(),
      stateValues: { name: "Example", required_settings: [] },
    }),
    /required state path missing: required_settings/,
  );
});

test("sensitivity masks are validated completely before projection and may be explicitly omitted", async () => {
  assert.throws(
    () => validateSensitiveMaskShape({ rules: [false] }, { rules: [] }),
    /unsupported sensitive mask shape/,
  );
  assert.throws(
    () => validateSensitiveMaskShape([{ secret: true }], [{ secret: "value" }]),
    /unsupported sensitive mask shape/,
  );
  await assert.rejects(
    () => projectProviderState({
      resourceType: "sample_resource",
      root: root(),
      sensitiveValues: { secret: true },
      stateValues: {
        name: "Example",
        required_settings: { mode: "strict" },
        secret: "do-not-write",
      },
    }),
    /sensitive input path secret/,
  );
  const omit = policy({
    projection_omit: [{
      path: "secret",
      reason: "secret supplied elsewhere",
      approved_by: "unit",
    }],
  });
  const output = await projectProviderState({
    policy: omit,
    resourceType: "sample_resource",
    root: root(),
    sensitiveValues: { secret: true },
    stateValues: {
      name: "Example",
      required_settings: { mode: "strict" },
      secret: "do-not-write",
    },
  });
  assert.equal(Object.hasOwn(output, "secret"), false);
  assert.deepEqual(omit.staleEntries({ modes: ["projection_omit"] }), []);
});

test("projection policy order is sync, fill, then conditional omit", async () => {
  const selected = policy({
    projection_sync: [{
      target_path: "target_categories",
      source_path: "source_categories",
      reason: "provider mirrors only one side",
      approved_by: "unit",
    }],
    projection_fill: [{
      path: "filled",
      source: "rawFilled",
      reason: "provider omits it",
      approved_by: "unit",
    }],
    projection_omit_if: [
      {
        path: "filled",
        values: ["DROP"],
        reason: "sentinel",
        approved_by: "unit",
      },
      {
        path: "number_value",
        values: [false],
        reason: "strict equality",
        approved_by: "unit",
      },
    ],
  });
  const output = await projectProviderState({
    policy: selected,
    rawItem: { rawFilled: "DROP" },
    resourceType: "sample_resource",
    root: root(),
    stateValues: {
      name: "Example",
      number_value: new LosslessNumber("0"),
      required_settings: { mode: "strict" },
      source_categories: ["ONE"],
      target_categories: [],
    },
  });
  assert.deepEqual(plain(output), {
    name: "Example",
    number_value: "0",
    required_settings: { mode: "strict" },
    source_categories: ["ONE"],
    target_categories: ["ONE"],
  });
  assert.deepEqual(selected.staleEntries().map((entry) => [entry.mode, entry.path]), [
    ["projection_omit_if", "number_value"],
  ]);
});

test("projection sync rejects type mismatches and repeated-block traversal", async () => {
  const mismatch = policy({
    projection_sync: [{
      target_path: "description",
      source_path: "enabled",
      reason: "invalid",
      approved_by: "unit",
    }],
  });
  await assert.rejects(
    () => projectProviderState({
      policy: mismatch,
      resourceType: "sample_resource",
      root: root(),
      stateValues: { enabled: true, name: "Example", required_settings: { mode: "strict" } },
    }),
    /schema types differ/,
  );

  const repeated = policy({
    projection_sync: [{
      target_path: "rules.name",
      source_path: "settings.mode",
      reason: "invalid",
      approved_by: "unit",
    }],
  });
  await assert.rejects(
    () => projectProviderState({
      policy: repeated,
      resourceType: "sample_resource",
      root: root(),
      stateValues: {
        name: "Example",
        required_settings: { mode: "strict" },
        rules: [{ name: "one" }],
        settings: { mode: "one" },
      },
    }),
    /is a repeated block/,
  );
});

test("schema status distinguishes writable, computed, required nested, and unknown paths", () => {
  assert.equal(providerSchemaStatus({ path: ["name"], resourceType: "sample_resource", schema: SCHEMA }), "required");
  assert.equal(providerSchemaStatus({ path: ["description"], resourceType: "sample_resource", schema: SCHEMA }), "optional");
  assert.equal(providerSchemaStatus({ path: ["computed_only"], resourceType: "sample_resource", schema: SCHEMA }), "computed_only");
  assert.equal(providerSchemaStatus({ path: ["required_settings"], requiredness: true, resourceType: "sample_resource", schema: SCHEMA }), "required");
  assert.equal(providerSchemaStatus({ path: ["missing"], resourceType: "sample_resource", schema: SCHEMA }), "unknown");
});
