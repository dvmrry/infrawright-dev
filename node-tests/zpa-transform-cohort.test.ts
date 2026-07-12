import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { buildSync } from "esbuild";

import embeddedCatalog from "../catalogs/zpa-transform-cohort-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import { transformZpaCohortItems } from "../node-src/domain/zpa-pull-transform.js";
import {
  loadZpaTransformCohortCatalog,
  requireSupportedZpaTransformCohortCatalog,
} from "../node-src/domain/zpa-transform-cohort-catalog.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";

interface CorpusCase {
  readonly name: string;
  readonly raw_items: readonly unknown[];
  readonly resource_type: string;
}

interface Corpus {
  readonly cases: readonly CorpusCase[];
}

const CORPUS_PATH = path.join(
  process.cwd(),
  "node-tests/fixtures/zpa-transform-cohort.v1.json",
);

const PYTHON_ORACLE = String.raw`
import json
import sys

from engine.transform import load_override, render_tfvars, transform_items

results = []
for case in json.load(sys.stdin)["cases"]:
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

function copyCatalog(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(embeddedCatalog)) as Record<string, unknown>;
}

function transformCase(fixture: CorpusCase) {
  return transformZpaCohortItems({
    catalog: loadZpaTransformCohortCatalog(),
    rawItems: fixture.raw_items,
    resourceType: fixture.resource_type,
  });
}

function expectProcessFailure(code: string): (error: unknown) => boolean {
  return (error: unknown): boolean => {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, code);
    return true;
  };
}

test("private ZPA cohort catalog is exact, source-bound, and immutable", () => {
  const catalog = loadZpaTransformCohortCatalog();
  assert.deepEqual(JSON.parse(JSON.stringify(catalog)), embeddedCatalog);
  assert.deepEqual(
    catalog.resources.map((resource) => resource.type),
    ["zpa_pra_console_controller", "zpa_pra_portal_controller"],
  );
  assert.deepEqual(catalog.absent_override_files, [
    "packs/zpa/overrides/zpa_pra_console_controller.json",
    "packs/zpa/overrides/zpa_pra_portal_controller.json",
  ]);
  assert.equal(
    catalog.python_compatibility_source,
    "catalogs/zcc-transform-catalog.v1.json",
  );
  assert.match(catalog.sources_sha256, /^[0-9a-f]{64}$/);
  assert.ok(Object.isFrozen(catalog));
  assert.ok(Object.isFrozen(catalog.resources));
  assert.ok(Object.isFrozen(catalog.resources[0]?.projection.attributes));
});

test("catalog mutations and unsupported resources fail closed", () => {
  const drifted = copyCatalog();
  drifted.sources_sha256 = "0".repeat(64);
  assert.throws(
    () => requireSupportedZpaTransformCohortCatalog(drifted),
    expectProcessFailure("UNSUPPORTED_ZPA_TRANSFORM_COHORT_CATALOG"),
  );

  const malformed = copyCatalog();
  malformed.resources = [];
  assert.throws(
    () => requireSupportedZpaTransformCohortCatalog(malformed),
    expectProcessFailure("INVALID_ZPA_TRANSFORM_COHORT_CATALOG"),
  );

  assert.throws(
    () => transformZpaCohortItems({
      catalog: loadZpaTransformCohortCatalog(),
      rawItems: [],
      resourceType: "zpa_application_server",
    }),
    expectProcessFailure("UNSUPPORTED_ZPA_TRANSFORM_RESOURCE"),
  );

  const reordered = copyCatalog();
  const resources = reordered.resources as Array<Record<string, unknown>>;
  const consoleResource = resources[0];
  assert.notEqual(consoleResource, undefined);
  const projection = consoleResource?.projection as Record<string, unknown>;
  const attributes = projection.attributes as Record<string, unknown>;
  const blocks = projection.blocks as Record<string, unknown>;
  projection.attributes = Object.fromEntries(Object.entries(attributes).reverse());
  projection.blocks = Object.fromEntries(Object.entries(blocks).reverse());
  const accepted = requireSupportedZpaTransformCohortCatalog(reordered);
  assert.equal(accepted, loadZpaTransformCohortCatalog());
  assert.deepEqual(
    Object.keys(accepted.resources[0]?.projection.attributes ?? {}),
    Object.keys(embeddedCatalog.resources[0]?.projection.attributes ?? {}),
  );
  assert.deepEqual(
    Object.keys(accepted.resources[0]?.projection.blocks ?? {}),
    Object.keys(embeddedCatalog.resources[0]?.projection.blocks ?? {}),
  );

  const reorderedArray = copyCatalog();
  reorderedArray.source_files = [
    ...(reorderedArray.source_files as readonly string[]),
  ].reverse();
  assert.throws(
    () => requireSupportedZpaTransformCohortCatalog(reorderedArray),
    expectProcessFailure("INVALID_ZPA_TRANSFORM_COHORT_CATALOG"),
  );
});

test("private ZPA cohort results and tfvars bytes match the real Python transform", () => {
  const corpusText = readFileSync(CORPUS_PATH, "utf8");
  const corpus = parseDataJsonLosslessly(corpusText) as Corpus;
  const actual = corpus.cases.map((fixture) => {
    const result = transformCase(fixture);
    return {
      name: fixture.name,
      result,
      tfvars: renderPythonLosslessArtifactJson({ items: result.items }),
    };
  });

  const python = spawnSync("python3", ["-c", PYTHON_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: corpusText,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson(actual), python.stdout);
  assert.deepEqual(actual.map((entry) => entry.result.drops), [[], []]);

  const consoleResult = actual[0]?.result;
  assert.equal(consoleResult?.items.console_one?.name, "Console & One");
  assert.equal(consoleResult?.items.console_one?.description, "A > B & C");
  assert.equal(
    Object.hasOwn(consoleResult?.items.console_one ?? {}, "id"),
    false,
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(consoleResult?.items.console_one?.pra_application)),
    [{ id: "216196257331291240" }],
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(consoleResult?.items.console_one?.pra_portals)),
    [
      { id: ["216196257331291242", "216196257331291244"] },
      { id: ["216196257331291243"] },
    ],
  );

  const portal = actual[1]?.result;
  assert.deepEqual(portal?.items.portal_one?.approval_reviewers, [
    "10@example.com",
    "2@example.com",
    "2@example.com",
    "a@example.com",
    "é@example.com",
    "😀@example.com",
  ]);
  assert.equal(
    portal?.items.portal_one?.certificate_id,
    "900719925474099312345",
  );
  assert.equal(portal?.items.portal_one?.enabled, true);
  assert.equal(portal?.items.portal_one?.user_notification_enabled, false);
  assert.deepEqual(
    portal?.items.scalar_reviewers?.approval_reviewers,
    ["single@example.com"],
  );
  assert.deepEqual(portal?.items.empty_reviewers?.approval_reviewers, []);
});

test("ZPA unknown fields retain live Python Unicode result and tfvars bytes", () => {
  const fixtureCase: CorpusCase = {
    name: "zpa_python_unicode_regressions",
    resource_type: "zpa_pra_console_controller",
    raw_items: [{
      id: "unicode-1",
      name: "Stable ASCII Identity",
      "\ua7cbNoise": "unicode-15-lower",
      "\u2028Future": "regex-dot-boundary",
    }],
  };
  const source = JSON.stringify({ cases: [fixtureCase] });
  const result = transformCase(fixtureCase);
  const actual = [{
    name: fixtureCase.name,
    result,
    tfvars: renderPythonLosslessArtifactJson({ items: result.items }),
  }];
  const python = spawnSync("python3", ["-c", PYTHON_ORACLE], {
    cwd: process.cwd(),
    encoding: "utf8",
    input: source,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(python.stderr, "");
  assert.equal(renderPythonLosslessArtifactJson(actual), python.stdout);
  assert.deepEqual(result.drops, ["\u2028_future", "\ua7cb_noise"]);
  assert.equal(
    result.originals.stable_ascii_identity?.["\ua7cb_noise"],
    "unicode-15-lower",
  );
  assert.equal(
    result.originals.stable_ascii_identity?.["\u2028_future"],
    "regex-dot-boundary",
  );
  assert.equal(
    Object.hasOwn(result.items.stable_ascii_identity ?? {}, "\ua7cb_noise"),
    false,
  );
  assert.equal(
    Object.hasOwn(result.items.stable_ascii_identity ?? {}, "\u2028_future"),
    false,
  );
  assert.equal(
    actual[0]?.tfvars,
    '{\n  "items": {\n    "stable_ascii_identity": {\n      "name": "Stable ASCII Identity"\n    }\n  }\n}\n',
  );
});

test("native and non-finite numeric input is rejected before projection", () => {
  const catalog = loadZpaTransformCohortCatalog();
  assert.throws(
    () => transformZpaCohortItems({
      catalog,
      rawItems: [{ id: "1", microtenantId: 1, name: "Native Number" }],
      resourceType: "zpa_pra_console_controller",
    }),
    /raw transform numeric tokens must be LosslessNumber/,
  );

  const nonFinite = parseDataJsonLosslessly(
    '[{"id":"2","microtenantId":1e9999,"name":"Nonfinite"}]',
  ) as readonly unknown[];
  assert.throws(
    () => transformZpaCohortItems({
      catalog,
      rawItems: nonFinite,
      resourceType: "zpa_pra_console_controller",
    }),
    /finite losslessly parsed JSON numbers/,
  );
});

test("unknown provider fields remain visible as unacknowledged drops", () => {
  const rawItems = parseDataJsonLosslessly(
    '[{"futureProviderField":"must-not-disappear","id":"3","name":"Drop Probe"}]',
  ) as readonly unknown[];
  const result = transformZpaCohortItems({
    catalog: loadZpaTransformCohortCatalog(),
    rawItems,
    resourceType: "zpa_pra_console_controller",
  });
  assert.deepEqual(result.drops, ["future_provider_field"]);
  assert.equal(
    result.originals.drop_probe?.future_provider_field,
    "must-not-disappear",
  );
  assert.equal(
    Object.hasOwn(result.items.drop_probe ?? {}, "future_provider_field"),
    false,
  );
});

test("production bundle excludes private ZPA contracts and evidence", () => {
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
    "catalogs/zpa-transform-cohort-catalog.v1.json",
    "docs/evidence/zpa-provider-v4.4.6.json",
    "packs/zpa/schemas/provider/zpa.json",
    "node-src/domain/zpa-pull-transform.ts",
    "node-src/domain/zpa-transform-cohort-catalog.ts",
  ]) {
    assert.equal(
      inputs.some((input) => input.endsWith(privateInput)),
      false,
      privateInput,
    );
  }

  const bundle = build.outputFiles?.[0]?.text ?? "";
  for (const privateMarker of [
    "infrawright.zpa_transform_cohort_catalog",
    "terraform_runtime_evidence_required",
    "dcf12469a9a8f648be0691c74e9816fc94ec7ddc",
    "c99a93a3a739d52be297289139715631547e5058ec9b2a5a98b6b98d3d60d778",
    "5220340cb10060d75cffebf1407be02a366646781d5bcafe322c91ecea1954e6",
    "UNSUPPORTED_ZPA_TRANSFORM_RESOURCE",
    "packs/zpa/overrides/zpa_pra_console_controller.json",
    "packs/zpa/schemas/provider/zpa.json",
  ]) {
    assert.equal(bundle.includes(privateMarker), false, privateMarker);
  }
});
