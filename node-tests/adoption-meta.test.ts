import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  adoptionIdentityItem,
  adoptionMetadata,
  classifyAdoptionRawItems,
  deriveAdoptionIdentities,
  deriveAdoptionKey,
  type AdoptionMetadata,
} from "../node-src/domain/adoption-meta.js";
import { adoptResourceItems } from "../node-src/domain/adopt-runner.js";
import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { transformLoadedItems } from "../node-src/domain/pull-transform.js";
import { formatImportTemplate } from "../node-src/domain/transform-artifacts.js";
import { loadPackRoot, type LoadedPackRoot, type LoadedResourceMetadata } from "../node-src/metadata/loader.js";
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
  assert.equal(explicit.length, 33);
  for (const entry of explicit) {
    assert.doesNotThrow(() => adoptionMetadata(entry), entry.type);
  }
});

test("corrected ZIA Transform and Adopt identities produce the same keys and import IDs", async () => {
  const root = await loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  const cases: ReadonlyArray<{
    readonly resourceType: string;
    readonly rawItems: readonly JsonObject[];
    readonly expected: readonly { readonly key: string; readonly importId: string }[];
  }> = [
    {
      resourceType: "zia_dc_exclusions",
      rawItems: [{
        dcid: new LosslessNumber("77"),
        dcName: { id: new LosslessNumber("77"), name: "Primary" },
      }],
      expected: [{ key: "77", importId: "77" }],
    },
    {
      resourceType: "zia_risk_profiles",
      rawItems: [{ id: new LosslessNumber("9007199254740993"), profileName: "Mutable" }],
      expected: [{ key: "9007199254740993", importId: "9007199254740993" }],
    },
    {
      resourceType: "zia_subscription_alert",
      rawItems: [{ id: new LosslessNumber("102"), email: "optional@example.invalid" }],
      expected: [{ key: "102", importId: "102" }],
    },
    {
      resourceType: "zia_traffic_forwarding_vpn_credentials",
      rawItems: [{ id: new LosslessNumber("103"), type: "UFQDN", fqdn: "mutable.example" }],
      expected: [{ key: "103", importId: "103" }],
    },
    {
      resourceType: "zia_casb_dlp_rules",
      rawItems: [
        { id: new LosslessNumber("11"), name: "Duplicate", type: "OFLCASB_DLP_ITSM" },
        { id: new LosslessNumber("12"), name: "Duplicate", type: "OFLCASB_DLP_ITSM" },
      ],
      expected: [
        { key: "oflcasb_dlp_itsm_11", importId: "OFLCASB_DLP_ITSM:11" },
        { key: "oflcasb_dlp_itsm_12", importId: "OFLCASB_DLP_ITSM:12" },
      ],
    },
    {
      resourceType: "zia_casb_malware_rules",
      rawItems: [{ id: new LosslessNumber("21"), type: "OFLCASB_AVP_ITSM" }],
      expected: [{
        key: "oflcasb_avp_itsm_21",
        importId: "OFLCASB_AVP_ITSM:21",
      }],
    },
  ];

  for (const fixture of cases) {
    const resource = root.resources.get(fixture.resourceType);
    assert.notEqual(resource, undefined, fixture.resourceType);
    const selected = resource as LoadedResourceMetadata;
    const transformed = transformLoadedItems({
      rawItems: fixture.rawItems,
      resource: selected,
      schema: await root.loadResourceSchema(fixture.resourceType),
    });
    const adopted = deriveAdoptionIdentities({
      rawItems: fixture.rawItems,
      resource: selected,
    });
    const transformIdentities = Object.keys(transformed.items).map((key) => ({
      key,
      importId: formatImportTemplate(
        adoptionMetadata(selected).importId,
        transformed.originals[key] ?? {},
      ),
    }));
    assert.deepEqual(transformIdentities, fixture.expected, `${fixture.resourceType} Transform`);
    assert.deepEqual(
      adopted.identities.map(({ key, importId }) => ({ key, importId })),
      fixture.expected,
      `${fixture.resourceType} Adopt`,
    );
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

function unsupportedRule(match: JsonObject): JsonObject {
  return {
    evidence: ["https://example.invalid/provider-source"],
    match,
    provider: { source: "example/sample", version: "1.2.3" },
    reason: "provider cannot round-trip this object",
  };
}

test("raw classification runs system skips then strict unsupported checks before identity", () => {
  const selected = resource({
    adopt: {
      identity_fields: { import_id: "details.missing" },
      key_field: "missing_name",
      skip_if: [{ system: true }],
      unsupported_if: [unsupportedRule({ action: "ISOLATE" })],
    },
  });
  const classified = classifyAdoptionRawItems({
    rawItems: [
      { action: "ISOLATE", system: true },
      { action: "ISOLATE", system: false },
      { action: "BLOCK", system: false },
    ],
    resource: selected,
  });
  assert.equal(classified.skipped.length, 1);
  assert.equal(classified.unsupported.length, 1);
  assert.equal(classified.eligible.length, 1);
  assert.throws(
    () => deriveAdoptionIdentities({
      rawItems: [{ action: "ISOLATE", system: false }],
      resource: selected,
    }),
    /contains 1 item\(s\) unsupported by provider/u,
  );

  const strict = resource({
    adopt: { unsupported_if: [unsupportedRule({ marker: new LosslessNumber("1") })] },
  });
  const strictResult = classifyAdoptionRawItems({
    rawItems: [
      { id: "bool", marker: true, name: "Boolean" },
      { id: "number", marker: new LosslessNumber("1"), name: "Number" },
    ],
    resource: strict,
  });
  assert.deepEqual(strictResult.eligible.map((item) => item.id), ["bool"]);
  assert.deepEqual(strictResult.unsupported.map((entry) => entry.item.id), ["number"]);
});

test("committed ZIA classification metadata matches source-backed Fetch-shaped fixtures", async () => {
  const root = await loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  const fixture = parseDataJsonLosslessly(await readFile(
    path.join(ROOT, "node-tests", "fixtures", "zia-adoption-classification-v4.7.26.json"),
    "utf8",
  )) as {
    readonly resources: Readonly<Record<string, {
      readonly keep: readonly JsonObject[];
      readonly skip?: readonly JsonObject[];
      readonly system_skip?: readonly JsonObject[];
      readonly unsupported?: readonly JsonObject[];
    }>>;
  };
  for (const [resourceType, evidence] of Object.entries(fixture.resources)) {
    const selected = root.resources.get(resourceType);
    assert.notEqual(selected, undefined, resourceType);
    const skipped = [...(evidence.skip ?? []), ...(evidence.system_skip ?? [])];
    const unsupported = evidence.unsupported ?? [];
    const classified = classifyAdoptionRawItems({
      rawItems: [...skipped, ...unsupported, ...evidence.keep],
      resource: selected as LoadedResourceMetadata,
    });
    assert.equal(classified.skipped.length, skipped.length, `${resourceType} system skip`);
    assert.equal(classified.unsupported.length, unsupported.length, `${resourceType} unsupported`);
    assert.equal(classified.eligible.length, evidence.keep.length, `${resourceType} keep`);
    const schema = await root.loadResourceSchema(resourceType);
    for (const item of skipped) {
      assert.equal(
        Object.keys(transformLoadedItems({
          rawItems: [item],
          resource: selected as LoadedResourceMetadata,
          schema,
        }).items).length,
        0,
        `${resourceType} Transform system skip`,
      );
    }
    for (const item of [...unsupported, ...evidence.keep]) {
      assert.equal(
        Object.keys(transformLoadedItems({
          rawItems: [item],
          resource: selected as LoadedResourceMetadata,
          schema,
        }).items).length,
        1,
        `${resourceType} Transform retains user-owned item`,
      );
    }
  }
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

test("present malformed legacy identity metadata fails before invoking the Oracle", async () => {
  for (const override of [
    { import_id: 7 },
    { key_field: null },
  ]) {
    let called = false;
    await assert.rejects(
      () => adoptResourceItems({
        policy: new DriftPolicy({ version: 1, resource_types: {} }),
        rawItems: [{ id: "UNINTENDED", name: "Wrong Default" }],
        resource: resource({ override }),
        root: {} as LoadedPackRoot,
        stateLoader: async () => {
          called = true;
          return new Map();
        },
      }),
      /adopt\.(?:import_id|key_field) must be/,
    );
    assert.equal(called, false);
  }
});
