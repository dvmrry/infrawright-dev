import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import { validateTransformResourceCohort } from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  loadZiaTransformCohortCatalog,
  requireSupportedZiaTransformCohortCatalog,
  transformZiaCohortItems,
  ZIA_TRANSFORM_COHORT_SHA256,
} from "../node-src/domain/zia-transform-cohort.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";

const WORKSPACE = process.cwd();
const CATALOG_PATH = path.join(
  WORKSPACE,
  "catalogs/zia-transform-cohort.v1.json",
);
const FIXTURE_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/zia-transform-cohort.v1.json",
);

const PYTHON_ORACLE = String.raw`
import json
import sys

from engine.transform import load_override, render_tfvars, transform_items

fixture = json.load(sys.stdin)
results = []
for case in fixture["cases"]:
    items, originals, drops = transform_items(
        case["raw_items"],
        case["resource_type"],
        load_override(case["resource_type"]),
    )
    results.append({
        "name": case["name"],
        "result": {
            "drops": drops,
            "items": items,
            "originals": originals,
        },
        "tfvars": render_tfvars(items),
    })
json.dump(results, sys.stdout, indent=2, sort_keys=True)
sys.stdout.write("\n")
`;

interface FixtureCase {
  readonly name: string;
  readonly raw_items: readonly unknown[];
  readonly resource_type: string;
}

interface Fixture {
  readonly kind: string;
  readonly schema_version: number | LosslessNumber;
  readonly cases: readonly FixtureCase[];
}

function expectProcessFailure(code: string): (error: unknown) => boolean {
  return (error: unknown): boolean => {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, code);
    return true;
  };
}

function copyCatalog(): Record<string, unknown> {
  return JSON.parse(
    readFileSync(CATALOG_PATH, "utf8"),
  ) as Record<string, unknown>;
}

function transformCase(fixtureCase: FixtureCase) {
  return transformZiaCohortItems({
    catalog: loadZiaTransformCohortCatalog(),
    catalogSha256: ZIA_TRANSFORM_COHORT_SHA256,
    rawItems: fixtureCase.raw_items,
    resourceType: fixtureCase.resource_type,
  });
}

test("embedded ZIA cohort is exact, source-bound, closed, and immutable", () => {
  const text = readFileSync(CATALOG_PATH, "utf8");
  assert.equal(
    createHash("sha256").update(text).digest("hex"),
    ZIA_TRANSFORM_COHORT_SHA256,
  );
  const catalog = loadZiaTransformCohortCatalog();
  assert.equal(validateTransformResourceCohort(catalog), true);
  assert.deepEqual(
    catalog.resources.map((resource) => resource.type),
    [
      "zia_admin_roles",
      "zia_traffic_forwarding_static_ip",
      "zia_url_categories",
    ],
  );
  assert.deepEqual(catalog.source_files, [...catalog.source_files].sort());
  assert.match(catalog.sources_sha256, /^[0-9a-f]{64}$/);
  assert.equal(Object.getPrototypeOf(catalog), null);
  assert.ok(Object.isFrozen(catalog));
  assert.ok(Object.isFrozen(catalog.resources));
  assert.throws(() => {
    (catalog.resources as unknown[]).push({});
  }, TypeError);
});

test("catalog byte and semantic gates fail closed", () => {
  assert.throws(
    () => transformZiaCohortItems({
      catalog: loadZiaTransformCohortCatalog(),
      catalogSha256: "0".repeat(64),
      rawItems: [],
      resourceType: "zia_admin_roles",
    }),
    expectProcessFailure("UNSUPPORTED_ZIA_TRANSFORM_COHORT"),
  );

  const mutated = copyCatalog();
  mutated.sources_sha256 = "0".repeat(64);
  assert.throws(
    () => requireSupportedZiaTransformCohortCatalog(mutated),
    expectProcessFailure("UNSUPPORTED_ZIA_TRANSFORM_COHORT"),
  );

  const malformed = copyCatalog();
  const resources = malformed.resources as Array<Record<string, unknown>>;
  const urlCategories = resources.find((resource) => {
    return resource.type === "zia_url_categories";
  });
  assert.notEqual(urlCategories, undefined);
  if (urlCategories !== undefined) {
    urlCategories.sort_lists = ["urls", "urls"];
  }
  assert.throws(
    () => requireSupportedZiaTransformCohortCatalog(malformed),
    expectProcessFailure("INVALID_ZIA_TRANSFORM_COHORT"),
  );
});

test("representative and edge fixtures match real Python results and tfvars bytes", () => {
  const source = readFileSync(FIXTURE_PATH, "utf8");
  const fixture = parseDataJsonLosslessly(source) as Fixture;
  assert.equal(fixture.kind, "infrawright.zia_transform_cohort_fixture");
  assert.equal(String(fixture.schema_version), "1");

  const actual = fixture.cases.map((fixtureCase) => {
    const result = transformCase(fixtureCase);
    return {
      name: fixtureCase.name,
      result,
      tfvars: renderPythonLosslessArtifactJson({ items: result.items }),
    };
  });
  const python = spawnSync("python3", ["-c", PYTHON_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: source,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson(actual), python.stdout);

  const byName = new Map(actual.map((entry) => [entry.name, entry.result]));
  assert.deepEqual(
    byName.get("static_ip_float_and_drop_edges")?.drops,
    ["static_ip_id", "unexpected_field"],
  );
  assert.deepEqual(
    byName.get("url_categories_collection_and_drop_edges")?.drops,
    ["category_id", "mystery_field"],
  );
  assert.deepEqual(
    byName.get("admin_roles_map_numeric_and_drop_edges")?.drops,
    ["brand_new_api_field", "role_id"],
  );

  const url = byName.get("url_categories_representative")
    ?.items.set_ordering;
  assert.deepEqual(url?.db_categorized_urls, [
    "10.example",
    "2.example",
    "2.example",
    "é.example",
    "😀.example",
  ]);
  assert.deepEqual(url?.urls, [
    "a.example",
    "z.example",
    "é.example",
    "😀.example",
  ]);
  assert.deepEqual(
    byName.get("url_categories_collection_and_drop_edges")
      ?.items.collection_edges?.urls,
    ["z.example", "2", "a.example"],
  );

  const admin = byName.get("admin_roles_representative")
    ?.items.map_coercion;
  assert.deepEqual(
    { ...admin?.feature_permissions as object },
    JSON.parse(
      '{"10":"false","__proto__":"safe","alpha":"2","constructor":"also-safe","zeta":"true","é":"literal"}',
    ),
  );
  assert.deepEqual(admin?.permissions, [
    null,
    "10",
    "2",
    "2",
    "a",
    "β",
    "😀",
  ]);
});

test("unsupported resources and unsafe numeric inputs fail closed", () => {
  assert.throws(
    () => transformZiaCohortItems({
      catalog: loadZiaTransformCohortCatalog(),
      catalogSha256: ZIA_TRANSFORM_COHORT_SHA256,
      rawItems: [],
      resourceType: "zia_url_filtering_rules",
    }),
    expectProcessFailure("UNSUPPORTED_ZIA_TRANSFORM_RESOURCE"),
  );

  for (const latitude of [1.5, new LosslessNumber("1e400")]) {
    assert.throws(
      () => transformZiaCohortItems({
        catalog: loadZiaTransformCohortCatalog(),
        catalogSha256: ZIA_TRANSFORM_COHORT_SHA256,
        rawItems: [{
          id: "17",
          ipAddress: "192.0.2.17",
          latitude,
        }],
        resourceType: "zia_traffic_forwarding_static_ip",
      }),
      /finite losslessly parsed JSON numbers|native JavaScript numbers|raw transform numeric tokens/,
    );
  }

  assert.throws(
    () => transformZiaCohortItems({
      catalog: loadZiaTransformCohortCatalog(),
      catalogSha256: ZIA_TRANSFORM_COHORT_SHA256,
      rawItems: [{
        configuredName: "Malformed Set",
        dbCategorizedUrls: [{}],
        id: "CUSTOM_BAD",
      }],
      resourceType: "zia_url_categories",
    }),
    /set\(string\) coercion produced a non-string provider value/,
  );
});
