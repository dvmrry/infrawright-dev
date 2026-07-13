import { PYTHON_ORACLE as PYTHON_ORACLE_EXECUTABLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";
import {
  transformPullItems,
  transformPullItemsKernel,
} from "../node-src/domain/pull-transform.js";
import {
  loadZccTransformCatalog,
  type TransformCatalogResource,
  type TransformProjection,
} from "../node-src/domain/transform-catalog.js";

const PYTHON_ORACLE = String.raw`
import json
import sys

from engine.transform import load_override, transform_items

results = []
for case in json.load(sys.stdin):
    resource_type = case["resource_type"]
    items, originals, drops = transform_items(
        case["raw_items"], resource_type, load_override(resource_type)
    )
    results.append({
        "drops": drops,
        "items": items,
        "originals": originals,
    })
json.dump(results, sys.stdout, indent=2, sort_keys=True)
sys.stdout.write("\n")
`;

function resource(
  type: string,
  keyFields: readonly string[],
  attributes: TransformProjection["attributes"],
): TransformCatalogResource {
  return {
    acknowledged_drops: [],
    html_unescape_passes: 0,
    import_id: { segments: [{ field: "id" }], template: "{id}" },
    invert_bool: [],
    key_fields: keyFields,
    lookup_source: null,
    projection: {
      attributes,
      blocks: {},
      silently_ignored_attributes: ["id"],
    },
    references: {},
    renames: {},
    split_csv: [],
    type,
  };
}

test("product-neutral float, set(string), and map(string) shapes match Python", () => {
  const source = String.raw`[
    {
      "resource_type": "zia_traffic_forwarding_static_ip",
      "raw_items": [{
        "id": "17",
        "ipAddress": "192.0.2.17",
        "latitude": 0.0,
        "longitude": -73.985664
      }]
    },
    {
      "resource_type": "zia_url_categories",
      "raw_items": [{
        "id": "CUSTOM_01",
        "configuredName": "Set Ordering",
        "dbCategorizedUrls": ["é.example", "a.example", "😀.example", "10", "2"]
      }]
    },
    {
      "resource_type": "zia_admin_roles",
      "raw_items": [{
        "id": "9",
        "name": "Map Coercion",
        "featurePermissions": {
          "zeta": true,
          "alpha": 2,
          "é": "literal"
        }
      }]
    },
    {
      "resource_type": "zpa_app_connector_group",
      "raw_items": [{
        "id": "216196257331291234",
        "name": "Connector Codes",
        "userCodes": ["β", "a", "😀", "10", "2"]
      }]
    }
  ]`;
  const cases = parseDataJsonLosslessly(source) as readonly {
    readonly raw_items: readonly unknown[];
    readonly resource_type: string;
  }[];
  const resources = [
    resource(
      "zia_traffic_forwarding_static_ip",
      ["ip_address"],
      { ip_address: "string", latitude: "number", longitude: "number" },
    ),
    resource(
      "zia_url_categories",
      ["configured_name"],
      { configured_name: "string", db_categorized_urls: ["set", "string"] },
    ),
    resource(
      "zia_admin_roles",
      ["name"],
      { name: "string", feature_permissions: ["map", "string"] },
    ),
    resource(
      "zpa_app_connector_group",
      ["name"],
      { name: "string", user_codes: ["set", "string"] },
    ),
  ] as const;
  const compatibility = loadZccTransformCatalog().python_compatibility;
  const actual = cases.map((fixture, index) => {
    const contract = resources[index];
    assert.notEqual(contract, undefined);
    return transformPullItemsKernel({
      compatibility,
      rawItems: fixture.raw_items,
      resource: contract as TransformCatalogResource,
    });
  });

  const python = spawnSync(PYTHON_ORACLE_EXECUTABLE, ["-c", PYTHON_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: source,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson(actual), python.stdout);

  const setItems = actual[1]?.items.set_ordering;
  assert.deepEqual(
    setItems?.db_categorized_urls,
    ["10", "2", "a.example", "é.example", "😀.example"],
  );
  const mapItems = actual[2]?.items.map_coercion;
  assert.deepEqual({ ...mapItems?.feature_permissions as object }, {
    alpha: "2",
    zeta: "true",
    é: "literal",
  });
});

test("numeric string coercion matches Python for Unicode digits and underscore grammar", () => {
  const source = JSON.stringify([{
    raw_items: [
      { id: "arabic_integer", autoPurgeDays: "١٢" },
      { id: "fullwidth_integer", autoPurgeDays: "１２" },
      { id: "devanagari_integer", autoPurgeDays: "१२" },
      { id: "mixed_valid_underscore", autoPurgeDays: "١_२" },
      { id: "arabic_float", autoPurgeDays: "١٢.٥" },
      { id: "fullwidth_exponent", autoPurgeDays: "１.５e２" },
      { id: "devanagari_exponent", autoPurgeDays: "१e-२" },
      { id: "mixed_float", autoPurgeDays: "١२.３e٤" },
      { id: "python_whitespace", autoPurgeDays: "\u3000١٢\u3000" },
      { id: "double_underscore", autoPurgeDays: "1__2" },
      { id: "exponent_underscore", autoPurgeDays: "1e_2" },
      { id: "before_decimal_underscore", autoPurgeDays: "1_.2" },
      { id: "after_decimal_underscore", autoPurgeDays: "1._2" },
      { id: "leading_underscore", autoPurgeDays: "_12" },
      { id: "trailing_underscore", autoPurgeDays: "12_" },
      { id: "unicode_double_underscore", autoPurgeDays: "١__٢" },
      { id: "unicode_exponent_underscore", autoPurgeDays: "١e_२" },
      { id: "unicode_before_decimal_underscore", autoPurgeDays: "١_.२" },
      { id: "unicode_after_decimal_underscore", autoPurgeDays: "١._२" },
      { id: "byte_order_mark_prefix", autoPurgeDays: "\ufeff12" },
      { id: "byte_order_mark_suffix", autoPurgeDays: "12\ufeff" },
      { id: "ordinary_nonnumeric", autoPurgeDays: "café 1" },
    ],
    resource_type: "zcc_device_cleanup",
  }]);
  const fixtures = parseDataJsonLosslessly(source) as readonly {
    readonly raw_items: readonly unknown[];
    readonly resource_type: string;
  }[];
  const fixture = fixtures[0];
  assert.notEqual(fixture, undefined);
  const actual = transformPullItems({
    catalog: loadZccTransformCatalog(),
    rawItems: fixture?.raw_items ?? [],
    resourceType: fixture?.resource_type ?? "",
  });

  const python = spawnSync(PYTHON_ORACLE_EXECUTABLE, ["-c", PYTHON_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: source,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson([actual]), python.stdout);

  assert.equal(actual.items.arabic_integer?.auto_purge_days, 12);
  assert.equal(
    (actual.items.fullwidth_exponent?.auto_purge_days as LosslessNumber).toString(),
    "150.0",
  );
  assert.equal(
    (actual.items.devanagari_exponent?.auto_purge_days as LosslessNumber).toString(),
    "0.01",
  );
  for (const [key, value] of [
    ["double_underscore", "1__2"],
    ["unicode_exponent_underscore", "١e_२"],
    ["byte_order_mark_prefix", "\ufeff12"],
    ["ordinary_nonnumeric", "café 1"],
  ] as const) {
    assert.equal(actual.items[key]?.auto_purge_days, value);
  }
});

test("set(string) sorting is stable and preserves duplicates like Python", () => {
  const contract = resource(
    "synthetic_set",
    ["id"],
    { values: ["set", "string"] },
  );
  const result = transformPullItemsKernel({
    compatibility: loadZccTransformCatalog().python_compatibility,
    rawItems: [{ id: "set", values: ["2", "10", "2", null, "😀", "é"] }],
    resource: contract,
  });
  assert.deepEqual(
    result.items.set?.values,
    [null, "10", "2", "2", "é", "😀"],
  );
  assert.throws(
    () => transformPullItemsKernel({
      compatibility: loadZccTransformCatalog().python_compatibility,
      rawItems: [{ id: "bad", values: [{}] }],
      resource: contract,
    }),
    /set\(string\).*non-string provider value/,
  );
});

test("map(string) treats prototype-like keys as inert data", () => {
  const contract = resource(
    "synthetic_map",
    ["id"],
    { values: ["map", "string"] },
  );
  const raw = parseDataJsonLosslessly(
    '[{"id":"map","values":{"__proto__":"safe","constructor":"also-safe"}}]',
  ) as readonly unknown[];
  const result = transformPullItemsKernel({
    compatibility: loadZccTransformCatalog().python_compatibility,
    rawItems: raw,
    resource: contract,
  });
  const values = result.items.map?.values as Record<string, unknown>;
  assert.equal(Object.getPrototypeOf(values), null);
  assert.equal(Object.hasOwn(values, "__proto__"), true);
  assert.equal(values.__proto__, "safe");
  assert.equal(values.constructor, "also-safe");
  assert.equal((Object.prototype as Record<string, unknown>).safe, undefined);
});
