import assert from "node:assert/strict";
import test from "node:test";

import embeddedCatalog from "../catalogs/zcc-transform-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import { validateTransformCatalog } from "../node-src/contracts/validators.js";
import {
  loadZccTransformCatalog,
  requireSupportedZccTransformCatalog,
} from "../node-src/domain/transform-catalog.js";

function copyEmbedded(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(embeddedCatalog)) as Record<string, unknown>;
}

function expectFailure(code: string): (error: unknown) => boolean {
  return (error: unknown): boolean => {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, code);
    return true;
  };
}

test("embedded ZCC transform catalog is exact, sorted, and immutable", () => {
  const catalog = loadZccTransformCatalog();
  assert.deepEqual(
    catalog.resources.map((resource) => resource.type),
    [
      "zcc_device_cleanup",
      "zcc_failopen_policy",
      "zcc_forwarding_profile",
      "zcc_trusted_network",
      "zcc_web_privacy",
    ],
  );
  assert.deepEqual(
    catalog.source_files,
    [...catalog.source_files].sort(),
  );
  assert.match(catalog.sources_sha256, /^[0-9a-f]{64}$/);
  assert.equal(Object.getPrototypeOf(catalog), null);
  assert.equal(Object.getPrototypeOf(catalog.resources[0]?.renames), null);
  assert.equal(
    (catalog.resources[0]?.renames as Record<string, unknown>).constructor,
    undefined,
  );
  assert.ok(Object.isFrozen(catalog));
  assert.ok(Object.isFrozen(catalog.resources));
  assert.ok(Object.isFrozen(catalog.python_compatibility.html_unescape.entities));
  assert.throws(() => {
    (catalog.source_files as string[]).push("packs/zcc/untrusted.json");
  }, TypeError);
});

test("supported catalog gate returns only the canonical immutable snapshot", () => {
  const candidate = copyEmbedded();
  const accepted = requireSupportedZccTransformCatalog(candidate);
  assert.equal(accepted, loadZccTransformCatalog());

  const sourceFiles = candidate.source_files as string[];
  sourceFiles[0] = "packs/zcc/mutated.json";
  assert.notEqual(
    loadZccTransformCatalog().source_files[0],
    "packs/zcc/mutated.json",
  );
});

test("catalog schema is closed and rejects malformed contracts", () => {
  const extra = copyEmbedded();
  extra.unexpected = true;
  assert.throws(
    () => requireSupportedZccTransformCatalog(extra),
    expectFailure("INVALID_TRANSFORM_CATALOG"),
  );

  const missing = copyEmbedded();
  delete missing.python_compatibility;
  assert.throws(
    () => requireSupportedZccTransformCatalog(missing),
    expectFailure("INVALID_TRANSFORM_CATALOG"),
  );
});

test("catalog schema admits only the lifted string collection encodings", () => {
  for (const encoding of [["set", "string"], ["map", "string"]] as const) {
    const candidate = copyEmbedded();
    const resources = candidate.resources as Array<{
      projection: { attributes: Record<string, unknown> };
    }>;
    const attributes = resources[0]?.projection.attributes;
    assert.notEqual(attributes, undefined);
    if (attributes !== undefined) {
      attributes.active = [...encoding];
    }
    assert.equal(validateTransformCatalog(candidate), true);
    assert.throws(
      () => requireSupportedZccTransformCatalog(candidate),
      expectFailure("UNSUPPORTED_TRANSFORM_CATALOG"),
    );
  }

  for (const encoding of [["set", "number"], ["map", "bool"]] as const) {
    const candidate = copyEmbedded();
    const resources = candidate.resources as Array<{
      projection: { attributes: Record<string, unknown> };
    }>;
    const attributes = resources[0]?.projection.attributes;
    assert.notEqual(attributes, undefined);
    if (attributes !== undefined) {
      attributes.active = [...encoding];
    }
    assert.equal(validateTransformCatalog(candidate), false);
  }
});

test("catalog gate rejects source, resource, projection, and compatibility mutations", () => {
  const digest = copyEmbedded();
  digest.sources_sha256 = "0".repeat(64);

  const sourceOrder = copyEmbedded();
  (sourceOrder.source_files as unknown[]).reverse();

  const resourceOrder = copyEmbedded();
  (resourceOrder.resources as unknown[]).reverse();

  const projection = copyEmbedded();
  const projectionResources = projection.resources as Array<{
    projection: { attributes: Record<string, unknown> };
  }>;
  const firstAttribute = Object.keys(
    projectionResources[0]?.projection.attributes ?? {},
  )[0];
  assert.notEqual(firstAttribute, undefined);
  if (firstAttribute !== undefined && projectionResources[0] !== undefined) {
    projectionResources[0].projection.attributes[firstAttribute] = "string";
    const embeddedAttributes = embeddedCatalog.resources[0]?.projection
      .attributes as Record<string, unknown> | undefined;
    if (
      projectionResources[0].projection.attributes[firstAttribute]
      === embeddedAttributes?.[firstAttribute]
    ) {
      projectionResources[0].projection.attributes[firstAttribute] = "number";
    }
  }

  const compatibility = copyEmbedded();
  const compatibilityTable = compatibility.python_compatibility as {
    html_unescape: { entities: Record<string, string> };
  };
  const entity = Object.keys(compatibilityTable.html_unescape.entities)[0];
  assert.notEqual(entity, undefined);
  if (entity !== undefined) {
    compatibilityTable.html_unescape.entities[entity] += "x";
  }

  for (const candidate of [digest, projection, compatibility]) {
    assert.throws(
      () => requireSupportedZccTransformCatalog(candidate),
      expectFailure("UNSUPPORTED_TRANSFORM_CATALOG"),
    );
  }
  for (const candidate of [sourceOrder, resourceOrder]) {
    assert.throws(
      () => requireSupportedZccTransformCatalog(candidate),
      expectFailure("INVALID_TRANSFORM_CATALOG"),
    );
  }
});

test("prototype-like map keys are ordinary own data and cannot pass the gate", () => {
  const candidate = copyEmbedded();
  const resources = candidate.resources as Array<{
    renames: Record<string, string>;
  }>;
  const resource = resources[0];
  assert.notEqual(resource, undefined);
  if (resource !== undefined) {
    resource.renames = JSON.parse(
      '{"constructor":"also_data"}',
    ) as Record<string, string>;
  }
  assert.equal(({} as { polluted?: unknown }).polluted, undefined);
  assert.throws(
    () => requireSupportedZccTransformCatalog(candidate),
    expectFailure("UNSUPPORTED_TRANSFORM_CATALOG"),
  );
  assert.equal(({} as { polluted?: unknown }).polluted, undefined);
});
