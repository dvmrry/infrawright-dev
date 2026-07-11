import assert from "node:assert/strict";
import test from "node:test";

import embeddedCatalog from "../catalogs/zcc-adoption-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  loadZccAdoptionCatalog,
  requireSupportedZccAdoptionCatalog,
  type ZccAdoptionProjection,
} from "../node-src/domain/zcc-adoption-catalog.js";
import {
  compileZccAdoptionProjection,
} from "../node-src/domain/zcc-adoption-projection.js";

function copyEmbedded(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(embeddedCatalog)) as Record<string, unknown>;
}

function resources(candidate: Record<string, unknown>): Array<Record<string, unknown>> {
  return candidate.resources as Array<Record<string, unknown>>;
}

function resource(
  candidate: Record<string, unknown>,
  resourceType: string,
): Record<string, unknown> {
  const found = resources(candidate).find((entry) => entry.type === resourceType);
  assert.notEqual(found, undefined);
  return found ?? {};
}

function objectField(
  candidate: Record<string, unknown>,
  field: string,
): Record<string, unknown> {
  return candidate[field] as Record<string, unknown>;
}

function expectFailure(code: string): (error: unknown) => boolean {
  return (error: unknown): boolean => {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, code);
    return true;
  };
}

function assertSorted(values: readonly string[]): void {
  assert.deepEqual(values, [...values].sort());
}

function assertProjectionSorted(projection: ZccAdoptionProjection): void {
  assertSorted(Object.keys(projection.attributes));
  assertSorted(Object.keys(projection.blocks));
  assertSorted(projection.computed_only_attributes);
  assertSorted(projection.computed_only_blocks);
  for (const block of Object.values(projection.blocks)) {
    assertProjectionSorted(block.projection);
  }
}

function assertDeepFrozen(value: unknown): void {
  if (value === null || typeof value !== "object") {
    return;
  }
  assert.ok(Object.isFrozen(value));
  for (const child of Object.values(value)) {
    assertDeepFrozen(child);
  }
}

test("embedded ZCC adoption catalog is exact, sorted, and deeply immutable", () => {
  const catalog = loadZccAdoptionCatalog();
  assert.deepEqual(
    JSON.parse(JSON.stringify(catalog)),
    embeddedCatalog,
  );
  assert.deepEqual(
    catalog.resources.map((entry) => entry.type),
    [
      "zcc_device_cleanup",
      "zcc_failopen_policy",
      "zcc_forwarding_profile",
      "zcc_trusted_network",
      "zcc_web_privacy",
    ],
  );
  assertSorted(catalog.source_files);
  assert.match(catalog.sources_sha256, /^[0-9a-f]{64}$/);
  for (const entry of catalog.resources) {
    assertSorted(Object.keys(entry.identity.identity_fields));
    assertSorted(Object.keys(entry.identity.identity_renames));
    assertProjectionSorted(entry.projection);
  }

  assert.equal(Object.getPrototypeOf(catalog), null);
  assert.equal(Object.getPrototypeOf(catalog.provider), null);
  assert.equal(Object.getPrototypeOf(catalog.resources[0]?.identity), null);
  assert.equal(
    Object.getPrototypeOf(catalog.resources[0]?.projection.attributes),
    null,
  );
  assert.equal(
    (catalog.resources[0]?.identity.identity_renames as Record<string, unknown>)
      .constructor,
    undefined,
  );
  assertDeepFrozen(catalog);
  assert.throws(() => {
    (catalog.source_files as string[]).push("zcc/untrusted.json");
  }, TypeError);
});

test("supported semantic copies return only the canonical immutable snapshot", () => {
  const copy = copyEmbedded();
  const candidate = Object.fromEntries(Object.entries(copy).reverse());
  const accepted = requireSupportedZccAdoptionCatalog(candidate);
  assert.equal(accepted, loadZccAdoptionCatalog());

  const candidateSourceFiles = candidate.source_files as string[];
  candidateSourceFiles[0] = "zcc/mutated.json";
  assert.notEqual(
    loadZccAdoptionCatalog().source_files[0],
    "zcc/mutated.json",
  );
});

test("catalog schema rejects unknown and malformed nested contract fields", () => {
  const cases: Array<readonly [string, (candidate: Record<string, unknown>) => void]> = [
    ["unknown provider field", (candidate) => {
      objectField(candidate, "provider").unexpected = true;
    }],
    ["malformed provider field", (candidate) => {
      objectField(candidate, "provider").version = 1;
    }],
    ["unknown resource field", (candidate) => {
      resources(candidate)[0]!.unexpected = true;
    }],
    ["malformed resource field", (candidate) => {
      resources(candidate)[0]!.type = "zcc_unknown";
    }],
    ["unknown identity field", (candidate) => {
      objectField(resources(candidate)[0]!, "identity").unexpected = true;
    }],
    ["malformed identity field", (candidate) => {
      objectField(resources(candidate)[0]!, "identity").key_fields = "id";
    }],
    ["unknown projection field", (candidate) => {
      objectField(resources(candidate)[0]!, "projection").unexpected = true;
    }],
    ["malformed projection field", (candidate) => {
      const projection = objectField(resources(candidate)[0]!, "projection");
      const attributes = objectField(projection, "attributes");
      objectField(attributes, "active").encoding = "bytes";
    }],
    ["unknown lookup field", (candidate) => {
      const lookup = resource(candidate, "zcc_trusted_network").lookup_source;
      assert.notEqual(lookup, null);
      (lookup as Record<string, unknown>).unexpected = true;
    }],
    ["malformed lookup field", (candidate) => {
      const lookup = resource(candidate, "zcc_trusted_network").lookup_source;
      assert.notEqual(lookup, null);
      (lookup as Record<string, unknown>).name_field = "Not_A_Field";
    }],
  ];

  for (const [label, mutate] of cases) {
    const candidate = copyEmbedded();
    mutate(candidate);
    assert.throws(
      () => requireSupportedZccAdoptionCatalog(candidate),
      expectFailure("INVALID_ZCC_ADOPTION_CATALOG"),
      label,
    );
  }
});

test("exact catalog gate rejects source and semantic contract mutations", () => {
  const cases: Array<readonly [
    string,
    string,
    (candidate: Record<string, unknown>) => void,
  ]> = [
    ["provider source", "INVALID_ZCC_ADOPTION_CATALOG", (candidate) => {
      objectField(candidate, "provider").source = "example/zcc";
    }],
    ["provider version", "INVALID_ZCC_ADOPTION_CATALOG", (candidate) => {
      objectField(candidate, "provider").version = "0.1.0-beta.2";
    }],
    ["resource order", "INVALID_ZCC_ADOPTION_CATALOG", (candidate) => {
      resources(candidate).reverse();
    }],
    ["projection", "UNSUPPORTED_ZCC_ADOPTION_CATALOG", (candidate) => {
      const projection = objectField(resources(candidate)[0]!, "projection");
      const attributes = objectField(projection, "attributes");
      objectField(attributes, "active").status = "required";
    }],
    ["identity", "UNSUPPORTED_ZCC_ADOPTION_CATALOG", (candidate) => {
      const identity = objectField(
        resource(candidate, "zcc_trusted_network"),
        "identity",
      );
      objectField(identity, "identity_renames").network_name = "network_label";
    }],
    ["lookup", "UNSUPPORTED_ZCC_ADOPTION_CATALOG", (candidate) => {
      const lookup = resource(candidate, "zcc_trusted_network").lookup_source;
      assert.notEqual(lookup, null);
      (lookup as Record<string, unknown>).name_field = "network_name";
    }],
    ["source digest", "UNSUPPORTED_ZCC_ADOPTION_CATALOG", (candidate) => {
      candidate.sources_sha256 = "0".repeat(64);
    }],
  ];

  for (const [label, code, mutate] of cases) {
    const candidate = copyEmbedded();
    mutate(candidate);
    assert.throws(
      () => requireSupportedZccAdoptionCatalog(candidate),
      expectFailure(code),
      label,
    );
  }
});

test("prototype-like map keys remain own data and cannot bypass the gate", () => {
  const candidate = copyEmbedded();
  const identity = objectField(
    resource(candidate, "zcc_trusted_network"),
    "identity",
  );
  const prototypeLike = JSON.parse(
    '{"constructor":"also_data"}',
  ) as Record<string, string>;
  assert.ok(Object.hasOwn(prototypeLike, "constructor"));
  identity.identity_renames = prototypeLike;

  assert.equal(({} as { polluted?: unknown }).polluted, undefined);
  assert.throws(
    () => requireSupportedZccAdoptionCatalog(candidate),
    expectFailure("UNSUPPORTED_ZCC_ADOPTION_CATALOG"),
  );
  assert.equal(({} as { polluted?: unknown }).polluted, undefined);
});

test("hostile catalog graphs collapse before validation without values or traps", () => {
  const secret = "CATALOG-GRAPH-SECRET-MUST-NOT-LEAK";
  const assertInvalid = (candidate: unknown): void => {
    let failure: unknown;
    try {
      requireSupportedZccAdoptionCatalog(candidate);
    } catch (error: unknown) {
      failure = error;
    }
    assert.ok(failure instanceof ProcessFailure);
    assert.equal(failure.code, "INVALID_ZCC_ADOPTION_CATALOG");
    assert.equal(failure instanceof RangeError, false);
    assert.equal(JSON.stringify({
      details: failure.details,
      message: failure.message,
    }).includes(secret), false);
  };

  const cyclic = copyEmbedded();
  cyclic.private_note = secret;
  cyclic.self = cyclic;
  assertInvalid(cyclic);

  const deep = copyEmbedded();
  let cursor: Record<string, unknown> = deep;
  for (let index = 0; index < 20_000; index += 1) {
    const child: Record<string, unknown> = {};
    cursor.child = child;
    cursor = child;
  }
  cursor.private_note = secret;
  assertInvalid(deep);

  let getterCalls = 0;
  const accessor = copyEmbedded();
  delete accessor.provider;
  Object.defineProperty(accessor, "provider", {
    enumerable: true,
    get() {
      getterCalls += 1;
      return { name: secret };
    },
  });
  assertInvalid(accessor);
  assert.equal(getterCalls, 0);

  let trapCalls = 0;
  const proxy = new Proxy(copyEmbedded(), {
    getPrototypeOf(target) {
      trapCalls += 1;
      return Reflect.getPrototypeOf(target);
    },
    ownKeys(target) {
      trapCalls += 1;
      return Reflect.ownKeys(target);
    },
  });
  assertInvalid(proxy);
  assert.equal(trapCalls, 0);

  assert.throws(
    () => compileZccAdoptionProjection({
      catalog: proxy as unknown as ReturnType<typeof loadZccAdoptionCatalog>,
      observedStates: [],
      rawItems: [],
      resourceType: "zcc_device_cleanup",
    }),
    expectFailure("INVALID_ZCC_ADOPTION_CATALOG"),
  );
  assert.equal(trapCalls, 0);
});
