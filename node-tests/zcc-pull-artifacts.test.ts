import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import { parse as parseLosslessJson } from "lossless-json";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { validateZccPullArtifactSet } from "../node-src/contracts/validators.js";
import {
  compileZccPullArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
  type ZccArtifactTarget,
  type ZccPullArtifactSet,
  type ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";
import { parseZccPullDataJson } from "../node-src/json/zcc-pull-data.js";

const WORKSPACE = process.cwd();
const SOURCE_DIGEST = "1".repeat(64);

function target(
  resourceType: ZccPullResourceType,
  options: { readonly grouped?: boolean } = {},
): ZccArtifactTarget {
  const grouped = options.grouped === true;
  return {
    tenant: "demo",
    resourceType,
    rootLabel: grouped ? "zcc_bootstrap" : resourceType,
    rootMembers: grouped
      ? ["zcc_forwarding_profile", "zcc_trusted_network"]
      : [resourceType],
    variableName: grouped ? `${resourceType}_items` : "items",
    configPath: `overlay/config/demo/${resourceType}.auto.tfvars.json`,
    importsPath: `overlay/imports/demo/${resourceType}_imports.tf`,
    lookupPath: resourceType === "zcc_trusted_network"
      ? `overlay/config/demo/${resourceType}.lookup.json`
      : null,
  };
}

function compile(
  resourceType: ZccPullResourceType,
  rawItems: readonly unknown[],
  options: { readonly grouped?: boolean } = {},
): ZccPullArtifactSet {
  const result = compileZccPullArtifactSet({
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems,
    target: target(resourceType, options),
    source: {
      path: `pulls/demo/${resourceType}.json`,
      sha256: SOURCE_DIGEST,
      size_bytes: 123,
    },
  });
  assert.equal(
    validateZccPullArtifactSet(result),
    true,
    JSON.stringify(validateZccPullArtifactSet.errors),
  );
  return result;
}

function verifyDescriptor(
  artifact: ZccPullArtifactSet["artifacts"]["tfvars"],
): void {
  const bytes = Buffer.from(artifact.content, "utf8");
  assert.equal(artifact.size_bytes, bytes.length);
  assert.equal(
    artifact.sha256,
    createHash("sha256").update(bytes).digest("hex"),
  );
  assert.equal(artifact.encoding, "utf-8");
}

test("all committed ZCC demos compile to exact Python tfvars and import bytes", async () => {
  const resources = [
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ] as const;
  for (const resourceType of resources) {
    const rawText = await readFile(
      path.join(WORKSPACE, `tests/fixtures/demo/${resourceType}.json`),
      "utf8",
    );
    const result = compile(resourceType, parseZccPullDataJson(rawText));
    const expectedTfvars = await readFile(
      path.join(
        WORKSPACE,
        `tests/fixtures/demo-expected/${resourceType}.tfvars.json`,
      ),
      "utf8",
    );
    const expectedImports = await readFile(
      path.join(
        WORKSPACE,
        `tests/fixtures/demo-expected/${resourceType}_imports.tf`,
      ),
      "utf8",
    );
    assert.equal(result.artifacts.tfvars.content, expectedTfvars, resourceType);
    assert.equal(result.artifacts.imports.content, expectedImports, resourceType);
    assert.equal(result.status, "ready", resourceType);
    assert.deepEqual(result.unexpected_drops, [], resourceType);
    verifyDescriptor(result.artifacts.tfvars);
    verifyDescriptor(result.artifacts.imports);
    if (resourceType === "zcc_trusted_network") {
      const expectedLookup = await readFile(
        path.join(
          WORKSPACE,
          "demo/config/demo/zcc_trusted_network.lookup.json",
        ),
        "utf8",
      );
      assert.notEqual(result.artifacts.lookup, null);
      assert.equal(result.artifacts.lookup?.content, expectedLookup);
      if (result.artifacts.lookup !== null) {
        verifyDescriptor(result.artifacts.lookup);
      }
    } else {
      assert.equal(result.artifacts.lookup, null);
    }
  }
});

test("device-cleanup preserves arbitrary integers and reports new surface", async () => {
  const corpus = parseLosslessJson(await readFile(
    path.join(WORKSPACE, "node-tests/fixtures/zcc-transform-corpus.v1.json"),
    "utf8",
  )) as {
    readonly cases: readonly {
      readonly resource_type: string;
      readonly raw_items: readonly unknown[];
    }[];
  };
  const fixture = corpus.cases.find(
    (candidate) => candidate.resource_type === "zcc_device_cleanup",
  );
  assert.notEqual(fixture, undefined);
  const result = compile("zcc_device_cleanup", fixture?.raw_items ?? []);
  assert.match(
    result.artifacts.tfvars.content,
    /"auto_removal_days": 900719925474099312345/,
  );
  assert.match(result.artifacts.imports.content, /id = "900719925474099312345"/);
  assert.equal(result.status, "review_required");
  assert.deepEqual(result.unexpected_drops, ["server_noise"]);
});

test("empty pulls emit complete bootstrap artifacts, including empty lookup", () => {
  const resources = [
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ] as const;
  for (const resourceType of resources) {
    const result = compile(resourceType, parseZccPullDataJson("[]"));
    assert.equal(result.artifacts.tfvars.content, '{\n  "items": {}\n}\n');
    assert.equal(result.artifacts.imports.content, "");
    assert.equal(
      result.artifacts.lookup?.content ?? null,
      resourceType === "zcc_trusted_network" ? "{}\n" : null,
    );
    assert.equal(result.status, "ready");
  }
});

test("trusted-network artifact validation preserves lexical root overlays", () => {
  const resourceType = "zcc_trusted_network";
  for (const prefix of ["/", "//", "///"]) {
    const result = compileZccPullArtifactSet({
      catalog: loadZccTransformCatalog(),
      catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
      rawItems: [],
      target: {
        ...target(resourceType),
        configPath: `${prefix}config/demo/${resourceType}.auto.tfvars.json`,
        importsPath: `${prefix}imports/demo/${resourceType}_imports.tf`,
        lookupPath: `${prefix}config/demo/${resourceType}.lookup.json`,
      },
      source: {
        path: `pulls/demo/${resourceType}.json`,
        sha256: SOURCE_DIGEST,
        size_bytes: 0,
      },
    });
    assert.equal(
      result.artifacts.tfvars.path,
      `${prefix}config/demo/${resourceType}.auto.tfvars.json`,
    );
    assert.equal(
      result.artifacts.imports.path,
      `${prefix}imports/demo/${resourceType}_imports.tf`,
    );
    assert.equal(
      result.artifacts.lookup?.path,
      `${prefix}config/demo/${resourceType}.lookup.json`,
    );
    assert.equal(
      validateZccPullArtifactSet(result),
      true,
      JSON.stringify(validateZccPullArtifactSet.errors),
    );
  }
});

test("explicit grouping namespaces only the tfvars variable", () => {
  const result = compile(
    "zcc_forwarding_profile",
    parseZccPullDataJson('[{"id":"1","name":"Grouped"}]'),
    { grouped: true },
  );
  assert.equal(result.root.label, "zcc_bootstrap");
  assert.deepEqual(result.root.members, [
    "zcc_forwarding_profile",
    "zcc_trusted_network",
  ]);
  assert.equal(result.root.variable_name, "zcc_forwarding_profile_items");
  assert.match(
    result.artifacts.tfvars.content,
    /^\{\n  "zcc_forwarding_profile_items": \{/,
  );
  assert.match(
    result.artifacts.imports.content,
    /module\.zcc_forwarding_profile\.zcc_forwarding_profile\.this/,
  );
});

test("JSON and imports preserve Python Unicode and HCL escaping", () => {
  const unicode = compile(
    "zcc_forwarding_profile",
    parseZccPullDataJson('[{"id":"unicode-1","name":"東京"}]'),
  );
  assert.match(unicode.artifacts.tfvars.content, /"name": "\\u6771\\u4eac"/);
  assert.match(unicode.artifacts.imports.content, /this\["id_unicode_1"\]/);

  const importId = 'quote"\\line\nrow\rcol\t${name}%{ if true }';
  const escaped = compile(
    "zcc_forwarding_profile",
    parseZccPullDataJson(JSON.stringify([{ id: importId, name: "Escaping" }])),
  );
  assert.match(escaped.artifacts.imports.content, /quote\\"\\\\line\\nrow\\rcol\\t/);
  assert.match(escaped.artifacts.imports.content, /\$\$\{name\}%%\{ if true \}/);
});

test("blank and duplicate import identities fail closed without values", () => {
  const duplicateSecret = "same-secret-import-id";
  const duplicate = parseZccPullDataJson(JSON.stringify([
    { id: duplicateSecret, networkName: "First distinct key" },
    { id: duplicateSecret, networkName: "Second distinct key" },
  ]));
  assert.throws(
    () => compile("zcc_trusted_network", duplicate),
    (error: unknown) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "INVALID_ZCC_PULL_DATA");
      assert.equal(error.message.includes(duplicateSecret), false);
      return true;
    },
  );

  const blankIdentities = [
    "",
    " \t\r\n",
    "\u001c\u001f\u0085\u00a0\u1680\u2007\u2028\u202f\u205f\u3000",
  ] as const;
  for (const [index, id] of blankIdentities.entries()) {
    const marker = `blank-secret-${index}`;
    const raw = parseZccPullDataJson(JSON.stringify([{
      id,
      networkName: marker,
    }]));
    assert.throws(
      () => compile("zcc_trusted_network", raw),
      (error: unknown) => {
        assert.ok(error instanceof ProcessFailure);
        assert.equal(error.code, "INVALID_ZCC_PULL_DATA");
        assert.equal(error.message.includes(marker), false);
        return true;
      },
    );
  }
});

test("lookup is survivor-only, projected-name-first, sorted, and prototype-safe", () => {
  const raw = parseZccPullDataJson(
    '[{"id":"2","networkName":"Beta"},'
    + '{"id":"1","networkName":"Alpha","constructor":"data",'
    + '"__proto__":{"polluted":true}}]',
  );
  const result = compile("zcc_trusted_network", raw);
  assert.equal(
    result.artifacts.lookup?.content,
    '{\n'
    + '  "by_id": {\n'
    + '    "1": "Alpha",\n'
    + '    "2": "Beta"\n'
    + '  },\n'
    + '  "key_by_id": {\n'
    + '    "1": "alpha",\n'
    + '    "2": "beta"\n'
    + '  }\n'
    + '}\n',
  );
  assert.equal((Object.prototype as { polluted?: unknown }).polluted, undefined);
  assert.equal(result.status, "review_required");
  assert.deepEqual(result.unexpected_drops, ["__proto__", "constructor"]);
});

test("result is immutable and carries complete source and catalog provenance", () => {
  const result = compile(
    "zcc_failopen_policy",
    parseZccPullDataJson('[{"id":"1"}]'),
  );
  assert.ok(Object.isFrozen(result));
  assert.ok(Object.isFrozen(result.artifacts));
  assert.ok(Object.isFrozen(result.root.members));
  assert.equal(Object.getPrototypeOf(result), null);
  assert.equal(result.source.sha256, SOURCE_DIGEST);
  assert.equal(result.source.size_bytes, 123);
  assert.equal(result.catalog.sha256, ZCC_TRANSFORM_CATALOG_SHA256);
  assert.equal(
    result.catalog.sources_sha256,
    loadZccTransformCatalog().sources_sha256,
  );
  assert.throws(() => {
    (result.root.members as string[]).push("zcc_untrusted");
  }, TypeError);
});

test("exported catalog provenance matches the committed catalog bytes", async () => {
  const bytes = await readFile(
    path.join(WORKSPACE, "catalogs/zcc-transform-catalog.v1.json"),
  );
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    ZCC_TRANSFORM_CATALOG_SHA256,
  );
});

test("target, source, and catalog provenance inconsistencies fail closed", () => {
  const rawItems = parseZccPullDataJson("[]");
  const base = {
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems,
    target: target("zcc_web_privacy"),
    source: {
      path: "pulls/demo/zcc_web_privacy.json",
      sha256: SOURCE_DIGEST,
      size_bytes: 2,
    },
  };
  assert.throws(
    () => compileZccPullArtifactSet({ ...base, catalogSha256: "0".repeat(64) }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "UNSUPPORTED_TRANSFORM_CATALOG",
  );
  assert.throws(
    () => compileZccPullArtifactSet({
      ...base,
      source: { ...base.source, sha256: "bad" },
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_PULL_SOURCE",
  );
  assert.throws(
    () => compileZccPullArtifactSet({
      ...base,
      source: { ...base.source, path: "pulls/other/zcc_web_privacy.json" },
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_PULL_SOURCE",
  );
  assert.throws(
    () => compileZccPullArtifactSet({
      ...base,
      target: { ...base.target, variableName: "items_wrong" },
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_ARTIFACT_TARGET",
  );
});

test("raw transform and import errors expose no pull values", () => {
  const secret = "tenant-secret-value";
  const duplicate = parseZccPullDataJson(JSON.stringify([
    { id: secret },
    { id: secret },
  ]));
  assert.throws(
    () => compile("zcc_device_cleanup", duplicate),
    (error: unknown) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "INVALID_ZCC_PULL_DATA");
      assert.doesNotMatch(error.message, new RegExp(secret));
      return true;
    },
  );

  const nul = parseZccPullDataJson(JSON.stringify([{
    id: `hidden${String.fromCharCode(0)}value`,
  }]));
  assert.throws(
    () => compile("zcc_device_cleanup", nul),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ZCC_PULL_DATA"
      && !error.message.includes("hidden"),
  );
});
