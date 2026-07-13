import { PYTHON_ORACLE as PYTHON_ORACLE_EXECUTABLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import {
  parse as parseLosslessJson,
  stringify as stringifyLosslessJson,
} from "lossless-json";

import { transformPullItems } from "../node-src/domain/pull-transform.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";
import { terraformJsonEqual } from "../node-src/json/python-equality.js";

interface CorpusCase {
  readonly name: string;
  readonly resource_type: string;
  readonly raw_items: readonly unknown[];
  readonly expected?: unknown;
}

interface Corpus {
  readonly cases: readonly CorpusCase[];
}

const WORKSPACE = process.cwd();
const CORPUS_PATH = path.join(
  WORKSPACE,
  "node-tests/fixtures/zcc-transform-corpus.v1.json",
);

const PYTHON_ORACLE = String.raw`
import json
import sys

from engine.transform import load_override, transform_items

corpus = json.load(sys.stdin)
results = []
for case in corpus["cases"]:
    resource_type = case["resource_type"]
    # Intentionally load the pack override and provider schema through the
    # legacy transform path.  The generated Node transform catalog is not an
    # oracle input, so catalog-generator defects cannot make both sides agree.
    override = load_override(resource_type)
    items, originals, drops = transform_items(
        case["raw_items"], resource_type, override
    )
    results.append({
        "name": case["name"],
        "result": {
            "items": items,
            "originals": originals,
            "drops": drops,
        },
    })
json.dump(results, sys.stdout, sort_keys=True, separators=(",", ":"))
`;

test("Node ZCC transform matches the raw-schema Python oracle", async () => {
  const corpusText = await readFile(CORPUS_PATH, "utf8");
  const corpus = parseLosslessJson(corpusText) as Corpus;
  const demoTypes = [
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ] as const;
  const demoCases: CorpusCase[] = await Promise.all(demoTypes.map(async (resourceType) => {
    const raw = parseLosslessJson(await readFile(
      path.join(WORKSPACE, `tests/fixtures/demo/${resourceType}.json`),
      "utf8",
    )) as readonly unknown[];
    return {
      name: `committed-demo-${resourceType}`,
      resource_type: resourceType,
      raw_items: raw,
    };
  }));
  const allCases = [...corpus.cases, ...demoCases];
  const oracleInput = stringifyLosslessJson({ cases: allCases.map((fixture) => ({
    name: fixture.name,
    raw_items: fixture.raw_items,
    resource_type: fixture.resource_type,
  })) });
  const oracle = spawnSync(PYTHON_ORACLE_EXECUTABLE, ["-c", PYTHON_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: oracleInput,
    maxBuffer: 16 * 1024 * 1024,
  });
  assert.equal(oracle.status, 0, oracle.stderr);
  assert.equal(oracle.stderr, "");
  const expected = parseLosslessJson(oracle.stdout) as readonly {
    readonly name: string;
    readonly result: unknown;
  }[];
  assert.equal(expected.length, allCases.length);

  const catalog = loadZccTransformCatalog();
  for (const [index, fixture] of allCases.entries()) {
    const oracleCase = expected[index];
    assert.equal(oracleCase?.name, fixture.name);
    if (fixture.expected !== undefined) {
      assert.ok(
        terraformJsonEqual(oracleCase?.result, fixture.expected),
        `${fixture.name} frozen corpus result is stale`,
      );
    }
    const actual = transformPullItems({
      catalog,
      rawItems: fixture.raw_items,
      resourceType: fixture.resource_type,
    });
    assert.ok(
      terraformJsonEqual(actual, oracleCase?.result),
      `${fixture.name} differs from engine.transform using raw inputs`,
    );
  }
});
