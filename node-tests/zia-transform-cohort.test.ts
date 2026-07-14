import { PYTHON_ORACLE as PYTHON_ORACLE_EXECUTABLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { buildSync } from "esbuild";
import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  loadZiaTransformCohortCatalog,
  requireSupportedZiaTransformCohortCatalog,
  transformZiaCohortItems,
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
const EXPECTED_CATALOG_SHA256 =
  "a19c179f747f0402c55b7e617cdaed748fb2dcb722f92fa3d0afab8ac4d7716e";

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
    rawItems: fixtureCase.raw_items,
    resourceType: fixtureCase.resource_type,
  });
}

test("embedded ZIA cohort is exact, source-bound, closed, and immutable", () => {
  const text = readFileSync(CATALOG_PATH, "utf8");
  assert.equal(
    createHash("sha256").update(text).digest("hex"),
    EXPECTED_CATALOG_SHA256,
  );
  const catalog = loadZiaTransformCohortCatalog();
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

test("catalog semantics fail closed while serialization stays authoring-only", () => {
  const committed = readFileSync(CATALOG_PATH, "utf8");
  const minified = JSON.stringify(JSON.parse(committed));
  assert.notEqual(`${minified}\n`, committed);
  const accepted = requireSupportedZiaTransformCohortCatalog(
    JSON.parse(minified),
  );
  assert.equal(accepted, loadZiaTransformCohortCatalog());

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
  const python = spawnSync(PYTHON_ORACLE_EXECUTABLE, ["-c", PYTHON_ORACLE], {
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

test("ZIA nested map keys and unknown drops retain live Python Unicode bytes", () => {
  const fixtureCase: FixtureCase = {
    name: "admin_roles_python_unicode_regressions",
    resource_type: "zia_admin_roles",
    raw_items: [{
      id: "11",
      name: "Python Unicode Contract",
      featurePermissions: {
        "\ua7cb": "python-key",
      },
      "\u2028Future": "must-report",
    }],
  };
  const fixture: Fixture = {
    kind: "infrawright.zia_transform_cohort_fixture",
    schema_version: 1,
    cases: [fixtureCase],
  };
  const source = JSON.stringify(fixture);
  const result = transformCase(fixtureCase);
  const tfvars = renderPythonLosslessArtifactJson({ items: result.items });
  const actual = [{
    name: fixtureCase.name,
    result,
    tfvars,
  }];
  const python = spawnSync(PYTHON_ORACLE_EXECUTABLE, ["-c", PYTHON_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: source,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson(actual), python.stdout);

  const item = result.items.python_unicode_contract;
  const original = result.originals.python_unicode_contract;
  assert.notEqual(item, undefined);
  assert.notEqual(original, undefined);
  assert.equal(
    Object.hasOwn(item?.feature_permissions as object, "\ua7cb"),
    true,
  );
  assert.equal(
    Object.hasOwn(original?.feature_permissions as object, "\ua7cb"),
    true,
  );
  assert.equal(
    (item?.feature_permissions as Record<string, unknown>)["\ua7cb"],
    "python-key",
  );
  assert.equal(
    (original?.feature_permissions as Record<string, unknown>)["\ua7cb"],
    "python-key",
  );
  assert.match(tfvars, /"\\ua7cb": "python-key"/u);
  assert.deepEqual(result.drops, ["\u2028_future"]);
});

test("unsupported resources and unsafe numeric inputs fail closed", () => {
  assert.throws(
    () => transformZiaCohortItems({
      catalog: loadZiaTransformCohortCatalog(),
      rawItems: [],
      resourceType: "zia_url_filtering_rules",
    }),
    expectProcessFailure("UNSUPPORTED_ZIA_TRANSFORM_RESOURCE"),
  );

  for (const latitude of [1.5, new LosslessNumber("1e400")]) {
    assert.throws(
      () => transformZiaCohortItems({
        catalog: loadZiaTransformCohortCatalog(),
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

test("production bundle exposes public ZCC adoption but excludes private ZIA contracts", () => {
  const build = buildSync({
    bundle: true,
    entryPoints: ["node-src/process/main.ts"],
    format: "esm",
    logLevel: "silent",
    metafile: true,
    platform: "node",
    target: "node24",
    write: false,
  });
  const inputs = Object.keys(build.metafile?.inputs ?? {});
  for (const privateInput of [
    "catalogs/zia-transform-cohort.v1.json",
    "docs/schemas/transform-resource-cohort.schema.json",
    "node-src/domain/zia-transform-cohort-validator.ts",
    "node-src/domain/zia-transform-cohort.ts",
  ]) {
    assert.equal(
      inputs.some((input) => input.endsWith(privateInput)),
      false,
      privateInput,
    );
  }
  for (const publicInput of [
    "catalogs/zcc-adoption-catalog.v1.json",
    "docs/schemas/zcc-adoption-artifact-set.schema.json",
    "docs/schemas/zcc-adoption-artifact-parity.schema.json",
    "docs/schemas/zcc-adoption-artifact-materialization.schema.json",
    "node-src/contracts/zcc-adoption-materialization-semantics.ts",
    "node-src/contracts/zcc-adoption-parity-semantics.ts",
    "node-src/domain/zcc-adoption-artifact-parity.ts",
    "node-src/domain/zcc-adoption-materialization.ts",
    "node-src/domain/zcc-adoption-provider-lock.ts",
    "node-src/domain/zcc-adoption-oracle.ts",
    "node-src/domain/zcc-adoption-operation.ts",
    "node-src/io/zcc-adoption-oracle-adapters.ts",
  ]) {
    assert.equal(
      inputs.some((input) => input.endsWith(publicInput)),
      true,
      publicInput,
    );
  }

  const bundle = build.outputFiles?.[0]?.text ?? "";
  assert.equal(
    bundle.includes("sort_lists"),
    true,
    "the reviewed generic sort_lists kernel branch remains bundled",
  );
  for (const privateMarker of [
    "infrawright.transform_resource_cohort",
    "https://infrawright.local/schemas/transform-resource-cohort.schema.json",
    "zia/overrides/zia_traffic_forwarding_static_ip.json",
    "zia/overrides/zia_url_categories.json",
  ]) {
    assert.equal(bundle.includes(privateMarker), false, privateMarker);
  }
  for (const publicMarker of [
    "compile_adoption_artifacts",
    "compare_adoption_artifacts",
    "materialize_adoption_artifacts",
    "infrawright.zcc_adoption_artifact_set",
    "infrawright.zcc_adoption_artifact_parity",
    "infrawright.zcc_adoption_artifact_materialization",
    "9a097955041338130f344c525e10a3f34513eef307678df5e80abcf604ee60fa",
    "ZCC_ADOPTION_ORACLE_TIMEOUT",
    "h1:3Vp8Z76hEGPoZpwE0nSSqHwaJc1j+zX6KndDI2dAfsE=",
  ]) {
    assert.equal(bundle.includes(publicMarker), true, publicMarker);
  }
});
