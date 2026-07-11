import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import {
  parse as parseLosslessJson,
  stringify as stringifyLosslessJson,
} from "lossless-json";

import {
  compileZccPullArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
  type ZccArtifactTarget,
  type ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";

interface DifferentialCase {
  readonly name: string;
  readonly resource_type: ZccPullResourceType;
  readonly raw_items: readonly unknown[];
  readonly variable_name: string;
}

const WORKSPACE = process.cwd();
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

const PYTHON_ORACLE = String.raw`
import json
import sys

from engine import lookup
from engine.transform import (
    load_override,
    lookup_sidecar_items,
    render_imports,
    render_tfvars,
    transform_items,
)

cases = json.load(sys.stdin)
results = []
for case in cases:
    resource_type = case["resource_type"]
    override = load_override(resource_type)
    items, originals, drops = transform_items(
        case["raw_items"], resource_type, override
    )
    lookup_text = None
    source = lookup.lookup_sources().get(resource_type)
    if source is not None:
        survivors = lookup_sidecar_items(items, originals)
        by_id = lookup.build_lookup(survivors, source["name_field"])
        key_by_id = lookup.build_lookup_key_map(survivors)
        lookup_text = lookup.render_lookup(by_id, key_mapping=key_by_id)
    results.append({
        "name": case["name"],
        "tfvars": render_tfvars(items, var_name=case["variable_name"]),
        "imports": render_imports(resource_type, originals, override),
        "lookup": lookup_text,
        "drops": drops,
    })
json.dump(results, sys.stdout, sort_keys=True, separators=(",", ":"))
`;

function target(fixture: DifferentialCase): ZccArtifactTarget {
  const grouped = fixture.variable_name !== "items";
  return {
    tenant: "differential",
    resourceType: fixture.resource_type,
    rootLabel: grouped ? "zcc_bootstrap" : fixture.resource_type,
    rootMembers: grouped
      ? ["zcc_forwarding_profile", "zcc_trusted_network"]
      : [fixture.resource_type],
    variableName: fixture.variable_name,
    configPath: `config/differential/${fixture.resource_type}.auto.tfvars.json`,
    importsPath: `imports/differential/${fixture.resource_type}_imports.tf`,
    lookupPath: fixture.resource_type === "zcc_trusted_network"
      ? `config/differential/${fixture.resource_type}.lookup.json`
      : null,
  };
}

async function cases(): Promise<DifferentialCase[]> {
  const output: DifferentialCase[] = [];
  for (const resourceType of RESOURCES.slice(1)) {
    output.push({
      name: `demo-${resourceType}`,
      resource_type: resourceType,
      raw_items: parseLosslessJson(await readFile(
        path.join(WORKSPACE, `tests/fixtures/demo/${resourceType}.json`),
        "utf8",
      )) as readonly unknown[],
      variable_name: "items",
    });
  }
  const corpus = parseLosslessJson(await readFile(
    path.join(WORKSPACE, "node-tests/fixtures/zcc-transform-corpus.v1.json"),
    "utf8",
  )) as {
    readonly cases: readonly {
      readonly resource_type: string;
      readonly raw_items: readonly unknown[];
    }[];
  };
  const device = corpus.cases.find(
    (fixture) => fixture.resource_type === "zcc_device_cleanup",
  );
  assert.notEqual(device, undefined);
  output.push({
    name: "device-large-integer",
    resource_type: "zcc_device_cleanup",
    raw_items: device?.raw_items ?? [],
    variable_name: "items",
  });
  for (const resourceType of RESOURCES) {
    output.push({
      name: `empty-${resourceType}`,
      resource_type: resourceType,
      raw_items: [],
      variable_name: "items",
    });
  }
  output.push({
    name: "grouped-forwarding",
    resource_type: "zcc_forwarding_profile",
    raw_items: [{ id: "1", name: "Grouped" }],
    variable_name: "zcc_forwarding_profile_items",
  });
  output.push({
    name: "unicode-forwarding",
    resource_type: "zcc_forwarding_profile",
    raw_items: [{ id: "unicode-1", name: "東京" }],
    variable_name: "items",
  });
  output.push({
    name: "unicode-boundaries-trusted-tfvars-lookup",
    resource_type: "zcc_trusted_network",
    raw_items: [{
      id: "unicode-boundary-1",
      networkName: "ASCII ~ DEL \u007f C1 \u0080 NBSP \u00a0 astral \ud83d\ude00",
    }],
    variable_name: "items",
  });
  output.push({
    name: "escaped-import-identity",
    resource_type: "zcc_forwarding_profile",
    raw_items: [{
      id: 'quote"\\line\nrow\rcol\t${name}%{ if true }',
      name: "Escaping",
    }],
    variable_name: "items",
  });
  return output;
}

test("Node artifact compiler matches independent Python bytes for every ZCC resource", async () => {
  const fixtures = await cases();
  const oracle = spawnSync("python3", ["-c", PYTHON_ORACLE], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: stringifyLosslessJson(fixtures),
    maxBuffer: 32 * 1024 * 1024,
  });
  assert.equal(oracle.status, 0, oracle.stderr);
  assert.equal(oracle.stderr, "");
  const expected = JSON.parse(oracle.stdout) as readonly {
    readonly name: string;
    readonly tfvars: string;
    readonly imports: string;
    readonly lookup: string | null;
    readonly drops: readonly string[];
  }[];
  assert.equal(expected.length, fixtures.length);

  for (const [index, fixture] of fixtures.entries()) {
    const actual = compileZccPullArtifactSet({
      catalog: loadZccTransformCatalog(),
      catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
      rawItems: fixture.raw_items,
      target: target(fixture),
      source: {
        path: `pulls/differential/${fixture.resource_type}.json`,
        sha256: "2".repeat(64),
        size_bytes: 0,
      },
    });
    const oracleCase = expected[index];
    assert.equal(oracleCase?.name, fixture.name);
    assert.equal(actual.artifacts.tfvars.content, oracleCase?.tfvars, fixture.name);
    assert.equal(actual.artifacts.imports.content, oracleCase?.imports, fixture.name);
    assert.equal(actual.artifacts.lookup?.content ?? null, oracleCase?.lookup, fixture.name);
    assert.deepEqual(actual.unexpected_drops, oracleCase?.drops, fixture.name);
  }
});
