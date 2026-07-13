import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  adoptionIdentityItem,
  adoptionMetadata,
  deriveAdoptionIdentities,
  deriveAdoptionKey,
  type AdoptionMetadata,
} from "../node-src/domain/adoption-meta.js";
import { loadPackRoot, type LoadedResourceMetadata } from "../node-src/metadata/loader.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

const ROOT = process.cwd();

function resource(options?: {
  readonly adopt?: JsonObject;
  readonly override?: JsonObject;
}): LoadedResourceMetadata {
  return {
    type: "sample_resource",
    product: "sample",
    provider: "sample",
    pack: "sample",
    registry: {
      generate: true,
      product: "sample",
      ...(options?.adopt === undefined ? {} : { adopt: options.adopt }),
    },
    override: options?.override ?? null,
  };
}

function metadata(options?: Partial<AdoptionMetadata>): AdoptionMetadata {
  return {
    constantKey: null,
    identityFields: {},
    identityRenames: {},
    importId: "{id}",
    keyField: "name",
    skipIf: [],
    skipIfLte: [],
    ...options,
  };
}

test("all committed registry adoption entries resolve through generic metadata", async () => {
  const root = await loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  const explicit = [...root.resources.values()].filter((entry) => {
    return entry.registry.adopt !== undefined;
  });
  assert.equal(explicit.length, 31);
  for (const entry of explicit) {
    assert.doesNotThrow(() => adoptionMetadata(entry), entry.type);
  }
});

test("registry identity metadata takes precedence over transform fallback", () => {
  const resolved = adoptionMetadata(resource({
    adopt: {
      identity_fields: { ImportAlias: "details.ImportValue" },
      identity_renames: { RegistryOld: "RegistryNew" },
      import_id: "registry:{import_alias}",
      key_field: ["registry_new", "name"],
      skip_if: [{ system: true }],
      skip_if_lte: [{ order: 2 }],
    },
    override: {
      identity_fields: { legacy: "id" },
      import_id: "legacy:{id}",
      key_field: "legacy_name",
      renames: { legacy_old: "legacy_new" },
      skip_if: [{ legacy: true }],
      skip_if_lte: [{ order: 99 }],
    },
  }));
  assert.deepEqual(JSON.parse(JSON.stringify(resolved)), {
    constantKey: null,
    identityFields: { import_alias: "details.ImportValue" },
    identityRenames: { registry_old: "RegistryNew" },
    importId: "registry:{import_alias}",
    keyField: ["registry_new", "name"],
    skipIf: [{ system: true }],
    skipIfLte: [{ order: 2 }],
  });
});

test("identity shaping handles snake casing, renames, nested aliases, and import alias fallback", () => {
  const selected = resource({
    adopt: {
      identity_fields: { ImportId: "details.ExternalId" },
      identity_renames: { DisplayName: "name" },
      key_field: "name",
    },
  });
  const resolved = adoptionMetadata(selected);
  assert.equal(resolved.importId, "{import_id}");
  const item = adoptionIdentityItem({
    metadata: resolved,
    raw: {
      details: { externalId: "external-1" },
      displayName: "Renamed Item",
    },
    resourceType: selected.type,
  });
  assert.deepEqual(JSON.parse(JSON.stringify(item)), {
    details: { external_id: "external-1" },
    import_id: "external-1",
    name: "Renamed Item",
  });
  assert.throws(
    () => adoptionIdentityItem({
      metadata: resolved,
      raw: {
        details: { externalId: "external-1" },
        displayName: "Renamed Item",
        importId: "conflict",
      },
      resourceType: selected.type,
    }),
    /would overwrite existing field/,
  );
});

test("scalar, composite, constant, escaped-template, and non-ASCII identities match Python behavior", () => {
  const selected = resource({
    adopt: {
      import_id: "{{tenant}}:{type}:{id}",
      key_field: ["type", "name"],
    },
  });
  const result = deriveAdoptionIdentities({
    rawItems: [{ id: new LosslessNumber("9007199254740993"), name: "Rule One", type: "ACCESS" }],
    resource: selected,
  });
  assert.equal(result.identities[0]?.key, "access_rule_one");
  assert.equal(result.identities[0]?.importId, "{tenant}:ACCESS:9007199254740993");

  assert.equal(
    deriveAdoptionKey({ id: "fallback-7", name: "東京" }, metadata()),
    "id_fallback_7",
  );
  assert.throws(
    () => deriveAdoptionKey({ name: "東京" }, metadata()),
    /has no 'id' to fall back on/,
  );

  const singleton = deriveAdoptionIdentities({
    rawItems: [{ id: "only" }],
    resource: resource({ adopt: { constant_key: "settings", import_id: "{id}" } }),
  });
  assert.equal(singleton.identities[0]?.key, "settings");
  assert.throws(
    () => deriveAdoptionIdentities({
      rawItems: [{ id: "one" }, { id: "two" }],
      resource: resource({ adopt: { constant_key: "settings", import_id: "{id}" } }),
    }),
    /only valid for singleton adoption/,
  );
});

test("skip predicates retain wide-number precision and report their exact mode", () => {
  const selected = resource({
    adopt: {
      key_field: "name",
      skip_if: [{ system: true }],
      skip_if_lte: [{ order: new LosslessNumber("9007199254740992") }],
    },
  });
  const result = deriveAdoptionIdentities({
    rawItems: [
      { id: "system", name: "System", order: new LosslessNumber("99"), system: true },
      { id: "low", name: "Low", order: new LosslessNumber("9007199254740992") },
      { id: "high", name: "High", order: new LosslessNumber("9007199254740993") },
    ],
    resource: selected,
  });
  assert.deepEqual(result.skipped.map((entry) => entry.reason), ["skip_if", "skip_if_lte"]);
  assert.deepEqual(result.identities.map((entry) => entry.key), ["high"]);
});

test("duplicate keys and duplicate import IDs fail before any Oracle call", () => {
  assert.throws(
    () => deriveAdoptionIdentities({
      rawItems: [
        { id: "one", name: "Same" },
        { id: "two", name: "Same" },
      ],
      resource: resource(),
    }),
    /duplicate derived key/,
  );
  assert.throws(
    () => deriveAdoptionIdentities({
      rawItems: [
        { id: "same", name: "One" },
        { id: "same", name: "Two" },
      ],
      resource: resource(),
    }),
    /duplicate import_id/,
  );
});
