import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import {
  compileZccAdoptionProjection,
  type ZccAdoptionStateObservation,
} from "../node-src/domain/zcc-adoption-projection.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

const PROVIDER_NAME = "registry.terraform.io/zscaler/zcc";

function address(resourceType: string, key: string): string {
  const digest = createHash("sha1").update(key, "utf8").digest("hex").slice(0, 16);
  return `${resourceType}.iw_${digest}`;
}

function observation(options: {
  readonly resourceType: string;
  readonly key: string;
  readonly importId: string;
  readonly values: unknown;
  readonly sensitiveValues?: unknown;
}): ZccAdoptionStateObservation {
  return {
    address: address(options.resourceType, options.key),
    import_id: options.importId,
    key: options.key,
    provider_name: PROVIDER_NAME,
    resource_type: options.resourceType,
    sensitive_values: options.sensitiveValues === undefined
      ? {}
      : options.sensitiveValues,
    values: options.values,
  };
}

function expectFailure(
  run: () => unknown,
  code: string,
  secret: string,
): ProcessFailure {
  let thrown: unknown;
  try {
    run();
  } catch (error: unknown) {
    thrown = error;
  }
  assert.ok(thrown instanceof ProcessFailure);
  assert.equal(thrown.code, code);
  assert.equal(thrown instanceof RangeError, false);
  const diagnostic = JSON.stringify({
    code: thrown.code,
    details: thrown.details,
    message: thrown.message,
  });
  assert.equal(diagnostic.includes(secret), false);
  return thrown;
}

function compile(options: {
  readonly resourceType: string;
  readonly rawItems: readonly unknown[];
  readonly observedStates: readonly ZccAdoptionStateObservation[];
}) {
  return compileZccAdoptionProjection({
    catalog: loadZccAdoptionCatalog(),
    ...options,
  });
}

test("hostile adoption graphs fail iteratively without values or RangeError", () => {
  const secret = "GRAPH-SECRET-MUST-NOT-LEAK";
  const cyclicRaw: Record<string, unknown> = { id: "cycle", note: secret };
  cyclicRaw.self = cyclicRaw;
  expectFailure(
    () => compile({
      observedStates: [],
      rawItems: [cyclicRaw],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );

  const cyclicMask: Record<string, unknown> = { note: secret };
  cyclicMask.self = cyclicMask;
  expectFailure(
    () => compile({
      rawItems: [{ id: "cycle-mask" }],
      observedStates: [observation({
        importId: "cycle-mask",
        key: "cycle_mask",
        resourceType: "zcc_web_privacy",
        sensitiveValues: cyclicMask,
        values: { collect_user_info: false },
      })],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );

  const deepRoot: Record<string, unknown> = { id: "deep", note: secret };
  let cursor = deepRoot;
  for (let index = 0; index < 20_000; index += 1) {
    const child: Record<string, unknown> = {};
    cursor.child = child;
    cursor = child;
  }
  expectFailure(
    () => compile({
      observedStates: [],
      rawItems: [deepRoot],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );
});

test("accessor-backed and non-plain sensitive masks fail without access", () => {
  const secret = "ACCESSOR-SECRET-MUST-NOT-LEAK";
  let getterCalls = 0;
  const accessorMask: Record<string, unknown> = {};
  Object.defineProperty(accessorMask, "collect_user_info", {
    enumerable: true,
    get() {
      getterCalls += 1;
      return secret;
    },
  });
  expectFailure(
    () => compile({
      rawItems: [{ id: "accessor" }],
      observedStates: [observation({
        importId: "accessor",
        key: "accessor",
        resourceType: "zcc_web_privacy",
        sensitiveValues: accessorMask,
        values: { collect_user_info: false },
      })],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );
  assert.equal(getterCalls, 0);

  expectFailure(
    () => compile({
      rawItems: [{ id: "nonplain" }],
      observedStates: [observation({
        importId: "nonplain",
        key: "nonplain",
        resourceType: "zcc_web_privacy",
        sensitiveValues: new Map([["secret", secret]]),
        values: { collect_user_info: false },
      })],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );
});

test("plain-JSON scalar and root-list sensitive masks fail value-safely", () => {
  const secret = "SCALAR-MASK-SECRET-MUST-NOT-LEAK";
  const invalidMasks: readonly unknown[] = [
    [],
    secret,
    7,
    { collect_user_info: secret },
    { collect_user_info: [] },
    { collect_user_info: {} },
    { computed_only: [{ nested: 9 }] },
  ];
  for (const sensitiveValues of invalidMasks) {
    expectFailure(
      () => compile({
        rawItems: [{ id: "mask-shape" }],
        observedStates: [observation({
          importId: "mask-shape",
          key: "mask_shape",
          resourceType: "zcc_web_privacy",
          sensitiveValues,
          values: { collect_user_info: false },
        })],
        resourceType: "zcc_web_privacy",
      }),
      "ZCC_ADOPTION_PROJECTION_FAILED",
      secret,
    );
  }

  for (const sensitiveValues of [
    { forwarding_profile_actions: [] },
    { forwarding_profile_actions: [false, false] },
  ]) {
    expectFailure(
      () => compile({
        rawItems: [{ id: "mask-cardinality", name: "Mask Cardinality" }],
        observedStates: [observation({
          importId: "mask-cardinality",
          key: "mask_cardinality",
          resourceType: "zcc_forwarding_profile",
          sensitiveValues,
          values: {
            forwarding_profile_actions: [{
              action_type: 1,
              custom_pac: secret,
            }],
            name: "Mask Cardinality",
          },
        })],
        resourceType: "zcc_forwarding_profile",
      }),
      "ZCC_ADOPTION_PROJECTION_FAILED",
      secret,
    );
  }
});

test("valid false, null, empty-object, and nested-list masks remain supported", () => {
  for (const sensitiveValues of [false, null, {}] as const) {
    const result = compile({
      rawItems: [{ id: "valid-mask" }],
      observedStates: [observation({
        importId: "valid-mask",
        key: "valid_mask",
        resourceType: "zcc_web_privacy",
        sensitiveValues,
        values: { collect_user_info: false },
      })],
      resourceType: "zcc_web_privacy",
    });
    assert.equal(result.items.valid_mask?.collect_user_info, false);
  }

  const nested = compile({
    rawItems: [{ id: "nested-mask", name: "Nested Mask" }],
    observedStates: [observation({
      importId: "nested-mask",
      key: "nested_mask",
      resourceType: "zcc_forwarding_profile",
      sensitiveValues: { forwarding_profile_actions: [{}] },
      values: {
        forwarding_profile_actions: [{ action_type: 1 }],
        name: "Nested Mask",
      },
    })],
    resourceType: "zcc_forwarding_profile",
  });
  assert.equal(
    (nested.items.nested_mask?.forwarding_profile_actions as readonly unknown[])
      .length,
    1,
  );

  const emptyNested = compile({
    rawItems: [{ id: "empty-mask", name: "Empty Mask" }],
    observedStates: [observation({
      importId: "empty-mask",
      key: "empty_mask",
      resourceType: "zcc_forwarding_profile",
      sensitiveValues: { forwarding_profile_actions: [] },
      values: { forwarding_profile_actions: [], name: "Empty Mask" },
    })],
    resourceType: "zcc_forwarding_profile",
  });
  assert.deepEqual(
    emptyNested.items.empty_mask?.forwarding_profile_actions,
    [],
  );
});

test("cross-branch proxies cannot erase a true sensitive sibling mask", () => {
  const secret = "PROXY-CROSS-BRANCH-SECRET-MUST-NOT-LEAK";
  const proxyThatWouldClear = (
    target: Record<string, unknown>,
    mask: Record<string, unknown>,
    calls: { count: number },
  ): Record<string, unknown> => new Proxy(target, {
    get(targetValue, property, receiver) {
      calls.count += 1;
      mask.collect_user_info = false;
      return Reflect.get(targetValue, property, receiver);
    },
    getOwnPropertyDescriptor(targetValue, property) {
      calls.count += 1;
      mask.collect_user_info = false;
      return Reflect.getOwnPropertyDescriptor(targetValue, property);
    },
    getPrototypeOf(targetValue) {
      calls.count += 1;
      mask.collect_user_info = false;
      return Reflect.getPrototypeOf(targetValue);
    },
    ownKeys(targetValue) {
      calls.count += 1;
      mask.collect_user_info = false;
      return Reflect.ownKeys(targetValue);
    },
  });

  const valuesMask = { collect_user_info: true };
  const valuesCalls = { count: 0 };
  const valuesProxy = proxyThatWouldClear(
    { collect_user_info: secret },
    valuesMask,
    valuesCalls,
  );
  expectFailure(
    () => compile({
      rawItems: [{ id: "proxy-values" }],
      observedStates: [observation({
        importId: "proxy-values",
        key: "proxy_values",
        resourceType: "zcc_web_privacy",
        sensitiveValues: valuesMask,
        values: valuesProxy,
      })],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );
  assert.equal(valuesCalls.count, 0);
  assert.equal(valuesMask.collect_user_info, true);

  const rawMask = { collect_user_info: true };
  const rawCalls = { count: 0 };
  const rawProxy = proxyThatWouldClear(
    { id: "proxy-raw", private_note: secret },
    rawMask,
    rawCalls,
  );
  expectFailure(
    () => compile({
      rawItems: [rawProxy],
      observedStates: [observation({
        importId: "proxy-raw",
        key: "proxy_raw",
        resourceType: "zcc_web_privacy",
        sensitiveValues: rawMask,
        values: { collect_user_info: secret },
      })],
      resourceType: "zcc_web_privacy",
    }),
    "INVALID_ZCC_ADOPTION_INPUT",
    secret,
  );
  assert.equal(rawCalls.count, 0);
  assert.equal(rawMask.collect_user_info, true);
});

test("state identity joins bind resource, provider, and scratch address", () => {
  const secret = "STATE-IDENTITY-SECRET-MUST-NOT-LEAK";
  const base = observation({
    importId: "identity",
    key: "identity",
    resourceType: "zcc_web_privacy",
    values: { collect_user_info: secret },
  });
  for (const changed of [
    { ...base, resource_type: "zcc_failopen_policy" },
    { ...base, provider_name: "registry.terraform.io/foreign/provider" },
    { ...base, address: "zcc_web_privacy.iw_0000000000000000" },
  ]) {
    expectFailure(
      () => compile({
        rawItems: [{ id: "identity" }],
        observedStates: [changed],
        resourceType: "zcc_web_privacy",
      }),
      "ZCC_ADOPTION_STATE_JOIN_FAILED",
      secret,
    );
  }
});

test("repeated block non-objects fail closed instead of disappearing", () => {
  const secret = "REPEATED-BLOCK-SECRET-MUST-NOT-LEAK";
  expectFailure(
    () => compile({
      rawItems: [{ id: "repeated", name: "Repeated" }],
      observedStates: [observation({
        importId: "repeated",
        key: "repeated",
        resourceType: "zcc_forwarding_profile",
        values: {
          forwarding_profile_actions: [
            { action_type: 1 },
            secret,
          ],
          name: "Repeated",
        },
      })],
      resourceType: "zcc_forwarding_profile",
    }),
    "ZCC_ADOPTION_PROJECTION_FAILED",
    secret,
  );
});

test("a null id cannot rescue an empty non-ASCII key", () => {
  expectFailure(
    () => compile({
      observedStates: [],
      rawItems: [{ id: null, name: "東京" }],
      resourceType: "zcc_forwarding_profile",
    }),
    "ZCC_ADOPTION_IDENTITY_FAILED",
    "東京",
  );
});
