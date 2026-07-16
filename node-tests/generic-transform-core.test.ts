import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  deriveReorderItems,
  transformLoadedItems,
} from "../node-src/domain/pull-transform.js";
import {
  loadPackRoot,
  type LoadedResourceMetadata,
} from "../node-src/metadata/loader.js";
import type { JsonObject } from "../node-src/metadata/validation.js";

function resource(override: JsonObject): LoadedResourceMetadata {
  return {
    type: "sample_rule",
    product: "sample",
    provider: "sample",
    pack: "sample",
    registry: { generate: true, product: "sample" },
    override,
  };
}

const SCHEMA: JsonObject = {
  block: {
    attributes: {
      custom: { optional: true, type: "string" },
      defaulted: { optional: true, type: ["list", "string"] },
      drop_zero: { optional: true, type: "number" },
      id: { computed: true, optional: true, type: "number" },
      inverted: { optional: true, type: "bool" },
      name: { required: true, type: "string" },
      policy: { optional: true, type: "bool" },
      quota: { optional: true, type: "number" },
      reference: { optional: true, type: "string" },
      tags: { optional: true, type: ["list", "string"] },
      urls: { optional: true, type: ["list", "string"] },
    },
    block_types: {
      conditions: {
        nesting_mode: "list",
        block: {
          attributes: {
            id: { optional: true, type: "string" },
            name: { optional: true, type: "string" },
          },
        },
      },
    },
  },
};

test("loaded metadata drives the complete override order and schema projection", () => {
  const skipped: string[] = [];
  const result = transformLoadedItems({
    resource: resource({
      acknowledged_drops: ["unknown"],
      defaults: { defaulted: ["ANY"] },
      divide: { quota: new LosslessNumber("1024") },
      drop_if_default: { drop_zero: new LosslessNumber("0") },
      drops: ["discard", "conditions.name"],
      html_escape_fields: ["custom"],
      invert_bool: ["inverted"],
      key_field: "name",
      references: { reference: "unused" },
      renames: { display_name: "name" },
      skip_if: [{ predefined: true }],
      sort_lists: ["urls"],
      split_csv: ["tags"],
      strip_prefix: { tags: "COUNTRY_" },
      value_map: { policy: { NONE: false } },
    }),
    schema: SCHEMA,
    rawItems: [
      { displayName: "ignored", predefined: true },
      {
        conditions: [{ id: "1", name: "computed display" }],
        custom: "R&amp;D &amp;quot;x&amp;quot;",
        discard: "gone",
        displayName: "R&amp;amp;D",
        dropZero: "0",
        id: new LosslessNumber("9007199254740997"),
        inverted: new LosslessNumber("0"),
        policy: "NONE",
        quota: "2049",
        reference: { id: new LosslessNumber("9007199254740999"), name: "ref" },
        tags: "COUNTRY_US, COUNTRY_CA",
        unknown: "acknowledged",
        urls: ["z", "a"],
      },
    ],
    htmlUnescape: (value) => {
      return value
        .replaceAll("&amp;", "&")
        .replaceAll("&quot;", "\"");
    },
    unescapeHtml: true,
    onSkip: (_item, reason) => skipped.push(reason),
  });

  assert.deepEqual(skipped, ["skip_if"]);
  assert.deepEqual(result.drops, []);
  assert.deepEqual(JSON.parse(JSON.stringify(result.items)), {
    r_amp_amp_d: {
      conditions: [{ id: "1" }],
      custom: "R&amp;D &#34;x&#34;",
      defaulted: ["ANY"],
      inverted: true,
      name: "R&amp;amp;D",
      policy: false,
      quota: 2,
      reference: "9007199254740999",
      tags: ["US", "CA"],
      urls: ["a", "z"],
    },
  });
  assert.equal(
    result.originals.r_amp_amp_d?.id?.toString(),
    "9007199254740997",
  );
});

test("committed ZIA overrides drop raw empty-string sentinels", async () => {
  const root = await loadPackRoot({
    packsRoot: path.join(process.cwd(), "packs"),
    profilePath: path.join(process.cwd(), "packsets", "full.json"),
    catalogPath: path.join(process.cwd(), "packsets", "full.json"),
  });
  const fixtures = [{
    resourceType: "zia_firewall_filtering_network_service",
    raw: { id: "1", name: "Example", tag: "" },
    retained: { id: "1", name: "Example", tag: "managed" },
    field: "tag",
    retainedValue: "managed",
  }, {
    resourceType: "zia_browser_control_policy",
    raw: { id: "browser_settings", pluginCheckFrequency: "" },
    retained: { id: "browser_settings", pluginCheckFrequency: "weekly" },
    field: "plugin_check_frequency",
    retainedValue: "weekly",
  }] as const;

  for (const fixture of fixtures) {
    const loaded = root.resources.get(fixture.resourceType);
    assert.ok(loaded);
    const schema = await root.loadResourceSchema(fixture.resourceType);
    const dropped = transformLoadedItems({ resource: loaded, schema, rawItems: [fixture.raw] });
    const retained = transformLoadedItems({ resource: loaded, schema, rawItems: [fixture.retained] });
    const droppedItem = Object.values(dropped.items)[0];
    const retainedItem = Object.values(retained.items)[0];
    assert.ok(droppedItem);
    assert.ok(retainedItem);
    assert.equal(Object.hasOwn(droppedItem, fixture.field), false, fixture.resourceType);
    assert.equal(retainedItem[fixture.field], fixture.retainedValue, fixture.resourceType);
  }
});

test("committed ZIA overrides omit live-proven empty enums and retain real values", async () => {
  const root = await loadPackRoot({
    packsRoot: path.join(process.cwd(), "packs"),
    profilePath: path.join(process.cwd(), "packsets", "full.json"),
    catalogPath: path.join(process.cwd(), "packsets", "full.json"),
  });
  const cases = [{
    resourceType: "zia_dlp_dictionaries",
    empty: {
      id: "1",
      name: "Dictionary",
      confidenceLevelForPredefinedDict: "",
      confidenceThreshold: "",
    },
    nullable: {
      id: "1",
      name: "Dictionary",
      confidenceLevelForPredefinedDict: null,
      confidenceThreshold: null,
    },
    retained: {
      id: "1",
      name: "Dictionary",
      confidenceLevelForPredefinedDict: "HIGH",
      confidenceThreshold: "MEDIUM",
    },
    emptyExpected: { name: "Dictionary" },
    nullExpected: {
      confidence_level_for_predefined_dict: null,
      confidence_threshold: null,
      name: "Dictionary",
    },
    retainedExpected: {
      confidence_level_for_predefined_dict: "HIGH",
      confidence_threshold: "MEDIUM",
      name: "Dictionary",
    },
  }, {
    resourceType: "zia_http_header_profile",
    empty: {
      id: "2",
      name: "Header",
      httpHeaderProfileCriteria: [{ header: "USERAGENT", operator: "", userAgent: "" }],
    },
    nullable: {
      id: "2",
      name: "Header",
      httpHeaderProfileCriteria: [{ header: "USERAGENT", operator: null, userAgent: null }],
    },
    retained: {
      id: "2",
      name: "Header",
      httpHeaderProfileCriteria: [{
        header: "USERAGENT",
        operator: "UAVERSIONEQ",
        userAgent: "FIREFOX",
      }],
    },
    emptyExpected: {
      http_header_profile_criteria: [{ header: "USERAGENT" }],
      name: "Header",
    },
    nullExpected: {
      http_header_profile_criteria: [{ header: "USERAGENT", operator: null, user_agent: null }],
      name: "Header",
    },
    retainedExpected: {
      http_header_profile_criteria: [{
        header: "USERAGENT",
        operator: "UAVERSIONEQ",
        user_agent: "FIREFOX",
      }],
      name: "Header",
    },
  }, {
    resourceType: "zia_location_management",
    empty: {
      id: "3",
      name: "Location",
      displayTimeUnit: "",
      subLocScope: "",
      surrogateRefreshTimeUnit: "",
    },
    nullable: {
      id: "3",
      name: "Location",
      displayTimeUnit: null,
      subLocScope: null,
      surrogateRefreshTimeUnit: null,
    },
    retained: {
      id: "3",
      name: "Location",
      displayTimeUnit: "MINUTE",
      subLocScope: "SUB_LOCATION",
      surrogateRefreshTimeUnit: "HOUR",
    },
    emptyExpected: { name: "Location" },
    nullExpected: {
      display_time_unit: null,
      name: "Location",
      sub_loc_scope: null,
      surrogate_refresh_time_unit: null,
    },
    retainedExpected: {
      display_time_unit: "MINUTE",
      name: "Location",
      sub_loc_scope: "SUB_LOCATION",
      surrogate_refresh_time_unit: "HOUR",
    },
  }, {
    resourceType: "zia_ssl_inspection_rules",
    empty: {
      id: "4",
      name: "SSL",
      action: [{
        type: "DO_NOT_DECRYPT",
        doNotDecryptSubActions: [{ bypassOtherPolicies: true, minTlsVersion: "" }],
      }],
    },
    nullable: {
      id: "4",
      name: "SSL",
      action: [{
        type: "DO_NOT_DECRYPT",
        doNotDecryptSubActions: [{ bypassOtherPolicies: true, minTlsVersion: null }],
      }],
    },
    retained: {
      id: "4",
      name: "SSL",
      action: [{
        type: "DO_NOT_DECRYPT",
        doNotDecryptSubActions: [{ bypassOtherPolicies: true, minTlsVersion: "TLSV1_2" }],
      }],
    },
    emptyExpected: {
      action: [{
        do_not_decrypt_sub_actions: [{ bypass_other_policies: true }],
        type: "DO_NOT_DECRYPT",
      }],
      name: "SSL",
    },
    nullExpected: {
      action: [{
        do_not_decrypt_sub_actions: [{
          bypass_other_policies: true,
          min_tls_version: null,
        }],
        type: "DO_NOT_DECRYPT",
      }],
      name: "SSL",
    },
    retainedExpected: {
      action: [{
        do_not_decrypt_sub_actions: [{
          bypass_other_policies: true,
          min_tls_version: "TLSV1_2",
        }],
        type: "DO_NOT_DECRYPT",
      }],
      name: "SSL",
    },
  }] as const;

  for (const fixture of cases) {
    const loaded = root.resources.get(fixture.resourceType);
    assert.ok(loaded, fixture.resourceType);
    const schema = await root.loadResourceSchema(fixture.resourceType);
    const empty = transformLoadedItems({ resource: loaded, schema, rawItems: [fixture.empty] });
    const nullable = transformLoadedItems({ resource: loaded, schema, rawItems: [fixture.nullable] });
    const retained = transformLoadedItems({ resource: loaded, schema, rawItems: [fixture.retained] });
    assert.deepEqual(
      JSON.parse(JSON.stringify(Object.values(empty.items)[0])),
      fixture.emptyExpected,
      `${fixture.resourceType} empty`,
    );
    assert.deepEqual(
      JSON.parse(JSON.stringify(Object.values(nullable.items)[0])),
      fixture.nullExpected,
      `${fixture.resourceType} null`,
    );
    assert.deepEqual(
      JSON.parse(JSON.stringify(Object.values(retained.items)[0])),
      fixture.retainedExpected,
      `${fixture.resourceType} retained`,
    );
  }
});

test("generic schema shaping merges configured blocks and records conflicts", () => {
  const schema: JsonObject = {
    block: {
      attributes: { name: { required: true, type: "string" } },
      block_types: {
        groups: {
          nesting_mode: "set",
          block: {
            attributes: {
              ids: { optional: true, type: ["set", "string"] },
              mode: { optional: true, type: "string" },
            },
          },
        },
      },
    },
  };
  const result = transformLoadedItems({
    resource: resource({ merge_blocks: ["groups"] }),
    schema,
    rawItems: [{
      groups: [
        { ids: "b", mode: "first" },
        { ids: ["a"], mode: "second" },
      ],
      name: "Example",
    }],
  });
  assert.deepEqual(JSON.parse(JSON.stringify(result.items.example)), {
    groups: [{ ids: ["a", "b"], mode: "first" }],
    name: "Example",
  });
  assert.deepEqual(result.drops, [
    "groups[].mode (conflicting values across merged elements; kept first)",
  ]);
});

test("derived reorder requires complete rules and sorts numeric orders", () => {
  assert.deepEqual(JSON.parse(JSON.stringify(deriveReorderItems([
    { id: "b", ruleOrder: "10" },
    { id: "a", ruleOrder: "2" },
  ], { from: "sample_rule", policy_type: "ACCESS_POLICY" }))), {
    ACCESS_POLICY: {
      policy_type: "ACCESS_POLICY",
      rules: [
        { id: "a", order: "2" },
        { id: "b", order: "10" },
      ],
    },
  });
  assert.throws(
    () => deriveReorderItems(
      [{ id: "missing-order" }],
      { from: "sample_rule", policy_type: "ACCESS_POLICY" },
    ),
    /refusing to emit a partial reorder/,
  );
});

test("skip_if_lte preserves wide integers and Python decimal-string parsing", () => {
  const schema: JsonObject = {
    block: {
      attributes: {
        name: { required: true, type: "string" },
        order: { optional: true, type: "number" },
      },
    },
  };
  const result = transformLoadedItems({
    resource: resource({
      skip_if_lte: [{ order: new LosslessNumber("9007199254740992") }],
    }),
    schema,
    rawItems: [
      { name: "equal", order: new LosslessNumber("9007199254740992") },
      { name: "above", order: new LosslessNumber("9007199254740993") },
      { name: "hex", order: "0x10" },
      { name: "decimal string", order: "1_6" },
    ],
  });
  assert.deepEqual(Object.keys(result.items), ["above", "hex"]);

  const floats = transformLoadedItems({
    resource: resource({ skip_if_lte: [{ order: new LosslessNumber("1.5") }] }),
    schema,
    rawItems: [
      { name: "integer", order: new LosslessNumber("1") },
      { name: "fraction", order: "1.25" },
      { name: "greater", order: new LosslessNumber("2") },
    ],
  });
  assert.deepEqual(Object.keys(floats.items), ["greater"]);
});

test("null stubs recognize computed schema members without emitting them", () => {
  const childBlock = {
    attributes: {
      computed_name: { computed: true, type: "string" },
      id: { computed: true, type: "number" },
      setting: { optional: true, type: "string" },
    },
    block_types: {
      computed_details: {
        nesting_mode: "list",
        block: {
          attributes: { code: { computed: true, type: "string" } },
        },
      },
    },
  };
  const schema: JsonObject = {
    block: {
      attributes: { name: { required: true, type: "string" } },
      block_types: {
        many_child: { block: childBlock, nesting_mode: "list" },
        single_child: { block: childBlock, nesting_mode: "single" },
      },
    },
  };
  const stub = {
    computedDetails: [],
    computedName: "",
    id: new LosslessNumber("0"),
  };
  const result = transformLoadedItems({
    resource: resource({}),
    schema,
    rawItems: [{ manyChild: [stub], name: "Example", singleChild: stub }],
  });
  assert.deepEqual(JSON.parse(JSON.stringify(result.items)), {
    example: { many_child: [], name: "Example" },
  });
  assert.deepEqual(result.drops, []);
});

test("schema string coercion accepts safe native numbers produced internally", () => {
  const schema: JsonObject = {
    block: {
      attributes: {
        name: { required: true, type: "string" },
        quota: { optional: true, type: "string" },
      },
    },
  };
  const result = transformLoadedItems({
    resource: resource({ divide: { quota: new LosslessNumber("1024") } }),
    schema,
    rawItems: [{
      name: "Example",
      quota: new LosslessNumber("2048"),
    }],
  });
  assert.equal(result.items.example?.quota, "2");
  assert.throws(
    () => transformLoadedItems({
      resource: resource({}),
      schema,
      rawItems: [{ name: "Raw native", quota: 2 }],
    }),
    /raw transform numeric tokens must be LosslessNumber/,
  );
});
