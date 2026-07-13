import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { buildSync } from "esbuild";

import embeddedCatalog from "../catalogs/zcc-collector-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  collectZccOneApiResource,
  deriveZccOneApiEndpoints,
  throwZccCollectorTransportFailure,
  zccCollectorRetryDelayMs,
  type ZccCollectorDataRequest,
  type ZccCollectorTransportResponse,
} from "../node-src/domain/zcc-collector.js";
import {
  loadZccCollectorCatalog,
  requireSupportedZccCollectorCatalog,
} from "../node-src/domain/zcc-collector-catalog.js";

const RESOURCE_TYPES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const;

const EXPECTED_PATHS: Readonly<Record<typeof RESOURCE_TYPES[number], string>> = {
  zcc_device_cleanup: "zcc/papi/public/v1/getDeviceCleanupInfo",
  zcc_failopen_policy: "zcc/papi/public/v1/webFailOpenPolicy/listByCompany",
  zcc_forwarding_profile: "zcc/papi/public/v1/webForwardingProfile/listByCompany",
  zcc_trusted_network: "zcc/papi/public/v1/webTrustedNetwork/listByCompany",
  zcc_web_privacy: "zcc/papi/public/v1/getWebPrivacyInfo",
};

function bytes(text: string): Uint8Array {
  return Buffer.from(text, "utf8");
}

function response(
  text: string,
  status = 200,
  retryAfter?: string | null,
): ZccCollectorTransportResponse {
  return retryAfter === undefined
    ? { body: bytes(text), status }
    : { body: bytes(text), retryAfter, status };
}

async function captureFailure(
  run: () => unknown | Promise<unknown>,
  code: string,
  category: "domain" | "io" = "domain",
): Promise<ProcessFailure> {
  let failure: unknown;
  try {
    await run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof ProcessFailure);
  assert.equal(failure.code, code);
  assert.equal(failure.category, category);
  return failure;
}

function copyCatalog(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(embeddedCatalog)) as Record<string, unknown>;
}

test("collector catalog is exact, source-bound, immutable, and drift-closed", () => {
  const catalog = loadZccCollectorCatalog();
  assert.deepEqual(JSON.parse(JSON.stringify(catalog)), embeddedCatalog);
  assert.deepEqual(
    catalog.resources.map((resource) => resource.type),
    RESOURCE_TYPES,
  );
  assert.ok(Object.isFrozen(catalog));
  assert.ok(Object.isFrozen(catalog.resources));
  assert.ok(Object.isFrozen(catalog.resources[0]));

  for (const mutation of [
    (candidate: Record<string, unknown>) => {
      const resources = candidate.resources as Array<Record<string, unknown>>;
      const first = resources[0];
      assert.notEqual(first, undefined);
      if (first !== undefined) {
        first.path = EXPECTED_PATHS.zcc_web_privacy;
      }
    },
    (candidate: Record<string, unknown>) => {
      const resources = candidate.resources as Array<Record<string, unknown>>;
      const second = resources[1];
      assert.notEqual(second, undefined);
      if (second !== undefined) {
        second.pagination = "single";
        second.page_size = null;
      }
    },
  ]) {
    const candidate = copyCatalog();
    mutation(candidate);
    assert.throws(
      () => requireSupportedZccCollectorCatalog(candidate),
      (error: unknown) => {
        assert.ok(error instanceof ProcessFailure);
        assert.equal(error.code, "INVALID_ZCC_COLLECTOR_CATALOG");
        return true;
      },
    );
  }

  const unsupported = copyCatalog();
  unsupported.sources_sha256 = "0".repeat(64);
  assert.throws(
    () => requireSupportedZccCollectorCatalog(unsupported),
    (error: unknown) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "INVALID_ZCC_COLLECTOR_CATALOG");
      return true;
    },
  );
});

test("OneAPI endpoint derivation matches Python cloud and vanity rules", () => {
  assert.deepEqual(
    deriveZccOneApiEndpoints({ cloud: " PRODUCTION ", vanityDomain: "Acme-1" }),
    {
      audience: "https://api.zscaler.com",
      dataBaseUrl: "https://api.zsapi.net",
      tokenUrl: "https://acme-1.zslogin.net/oauth2/v1/token",
    },
  );
  assert.deepEqual(
    deriveZccOneApiEndpoints({ cloud: "Beta", vanityDomain: "Tenant" }),
    {
      audience: "https://api.zscaler.com",
      dataBaseUrl: "https://api.beta.zsapi.net",
      tokenUrl: "https://tenant.zsloginbeta.net/oauth2/v1/token",
    },
  );
  for (const input of [
    { cloud: "prod.example", vanityDomain: "tenant" },
    { cloud: "production", vanityDomain: "https://private.example" },
    { cloud: "production", vanityDomain: "private/path" },
  ]) {
    assert.throws(
      () => deriveZccOneApiEndpoints(input),
      (error: unknown) => {
        assert.ok(error instanceof ProcessFailure);
        assert.equal(error.message.includes("private"), false);
        return true;
      },
    );
  }
});

test("all five request paths and canonical fixture bytes match Python", async () => {
  for (const resourceType of RESOURCE_TYPES) {
    const fixturePath = path.join(
      process.cwd(),
      "tests/fixtures/demo",
      `${resourceType}.json`,
    );
    const fixture = readFileSync(fixturePath, "utf8");
    const body = resourceType === "zcc_trusted_network"
      ? `{"totalCount":2,"trustedNetworkContracts":${fixture}}`
      : fixture;
    const requests: ZccCollectorDataRequest[] = [];
    const result = await collectZccOneApiResource({
      cloud: "",
      resourceType,
      sleep: () => {
        assert.fail("fixture collection must not sleep");
      },
      transport(request) {
        requests.push(request);
        return response(body);
      },
    });
    const expectedQuery = resourceType === "zcc_device_cleanup"
      || resourceType === "zcc_web_privacy"
      ? ""
      : "?page=1&pageSize=1000";
    assert.deepEqual(requests, [{
      kind: "infrawright.zcc_oneapi_data_request",
      method: "GET",
      url: `https://api.zsapi.net/${EXPECTED_PATHS[resourceType]}${expectedQuery}`,
    }]);
    const python = spawnSync(PYTHON_ORACLE, ["-c", [
      "import json,sys",
      "with open(sys.argv[1], encoding='utf-8') as f: value=json.load(f)",
      "sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
    ].join("\n"), fixturePath], { encoding: "utf8" });
    assert.equal(python.status, 0, python.stderr);
    assert.equal(result.canonical_json, python.stdout);
    assert.equal(result.metadata.item_count, JSON.parse(fixture).length);
    assert.equal(result.metadata.data_requests, 1);
    assert.equal(result.metadata.transport_attempts, 1);
    assert.equal(
      result.metadata.sha256,
      createHash("sha256").update(result.canonical_json).digest("hex"),
    );
  }
});

test("singleton object/list compatibility, paged lists, envelope, and empties", async () => {
  const singletonObject = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_device_cleanup",
    sleep: () => undefined,
    transport: () => response('{"id":900719925474099312345}'),
  });
  assert.equal(
    singletonObject.canonical_json,
    '[\n  {\n    "id": 900719925474099312345\n  }\n]\n',
  );

  const singletonList = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_web_privacy",
    sleep: () => undefined,
    transport: () => response('[{"id":"1"},{"id":"2"}]'),
  });
  assert.equal(singletonList.metadata.item_count, 2);

  for (const resourceType of RESOURCE_TYPES) {
    const emptyBody = resourceType === "zcc_trusted_network"
      ? '{"trustedNetworkContracts":[]}'
      : "[]";
    const result = await collectZccOneApiResource({
      cloud: "",
      resourceType,
      sleep: () => undefined,
      transport: () => response(emptyBody),
    });
    assert.equal(result.canonical_json, "[]\n");
    assert.equal(result.metadata.item_count, 0);
  }
});

test("pagination stops on a short page and permits exactly 50,000 items", async () => {
  const fullPage = `[${Array.from({ length: 1000 }, () => "{}").join(",")}]`;
  const requests: string[] = [];
  const result = await collectZccOneApiResource({
    cloud: "beta",
    resourceType: "zcc_failopen_policy",
    sleep: () => undefined,
    transport(request) {
      requests.push(request.url);
      const page = Number(new URL(request.url).searchParams.get("page"));
      return response(page <= 50 ? fullPage : "[]");
    },
  });
  assert.equal(result.metadata.item_count, 50_000);
  assert.equal(result.metadata.data_requests, 51);
  assert.equal(result.metadata.transport_attempts, 51);
  assert.match(requests[0] ?? "", /api\.beta\.zsapi\.net.*page=1&pageSize=1000$/);
  assert.match(requests[50] ?? "", /page=51&pageSize=1000$/);

  const overflow = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_forwarding_profile",
      sleep: () => undefined,
      transport(request) {
        const page = Number(new URL(request.url).searchParams.get("page"));
        return response(page <= 50 ? fullPage : "[{}]");
      },
    }),
    "ZCC_COLLECTOR_ITEM_LIMIT",
  );
  assert.equal(overflow.message.includes("page=51"), false);
});

test("only 429 retries and the injected clock sees the exact schedule", async () => {
  const responses = [
    response("rate-limit-private", 429, "2.5"),
    response("rate-limit-private", 429, "Wed, 21 Oct 2015 07:28:00 GMT"),
    response("[]"),
  ];
  const waits: number[] = [];
  let attempts = 0;
  const result = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_failopen_policy",
    sleep(milliseconds) {
      waits.push(milliseconds);
    },
    transport() {
      const current = responses[attempts];
      attempts += 1;
      assert.notEqual(current, undefined);
      return current as ZccCollectorTransportResponse;
    },
  });
  assert.deepEqual(waits, [2500, 2000]);
  assert.equal(result.metadata.transport_attempts, 3);
  assert.equal(zccCollectorRetryDelayMs(0, "1e400"), 30_000);
  assert.equal(zccCollectorRetryDelayMs(0, "-1e400"), 0);
  assert.equal(zccCollectorRetryDelayMs(4, "31"), 30_000);
  assert.equal(zccCollectorRetryDelayMs(4, "not-numeric"), 16_000);

  const exhaustedWaits: number[] = [];
  const exhausted = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_failopen_policy",
      sleep(milliseconds) {
        exhaustedWaits.push(milliseconds);
      },
      transport: () => response("private-rate-body", 429),
    }),
    "ZCC_COLLECTOR_RATE_LIMITED",
    "io",
  );
  assert.deepEqual(exhaustedWaits, [1000, 2000, 4000, 8000, 16000]);
  assert.equal(exhausted.message.includes("private-rate-body"), false);

  let non429Attempts = 0;
  const non429 = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_failopen_policy",
      sleep: () => {
        assert.fail("non-429 responses must not sleep");
      },
      transport: () => {
        non429Attempts += 1;
        return response("private-service-body", 503, "30");
      },
    }),
    "ZCC_COLLECTOR_HTTP_STATUS",
    "io",
  );
  assert.equal(non429Attempts, 1);
  assert.equal(non429.message.includes("private-service-body"), false);
});

test("malformed, duplicate, nonfinite, scalar, envelope, and UTF-8 data fail closed", async () => {
  const cases: readonly [string, Uint8Array, string][] = [
    ["malformed", bytes('[{"private":"unterminated}]'), "INVALID_ZCC_COLLECTOR_RESPONSE"],
    ["duplicate", bytes('[{"private":1,"private":2}]'), "INVALID_ZCC_COLLECTOR_RESPONSE"],
    ["nonfinite", bytes('[{"private":1e9999}]'), "INVALID_ZCC_COLLECTOR_RESPONSE"],
    ["scalar-item", bytes("[1]"), "INVALID_ZCC_COLLECTOR_RESPONSE"],
    ["invalid-utf8", Uint8Array.from([0xff]), "INVALID_ZCC_COLLECTOR_RESPONSE"],
    [
      "deep",
      bytes(`[{"value":${"[".repeat(129)}0${"]".repeat(129)}}]`),
      "ZCC_COLLECTOR_RESPONSE_LIMIT",
    ],
    [
      "tokens",
      bytes(`[{"value":[${Array.from({ length: 125_001 }, () => "0").join(",")}]}]`),
      "ZCC_COLLECTOR_RESPONSE_LIMIT",
    ],
    [
      "numeric-token",
      bytes(`[{"value":${"1".repeat(1025)}}]`),
      "ZCC_COLLECTOR_RESPONSE_LIMIT",
    ],
  ];
  for (const [name, body, code] of cases) {
    const failure = await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => undefined,
        transport: () => ({ body, status: 200 }),
      }),
      code,
    );
    assert.equal(failure.message.includes("private"), false, name);
  }

  for (const body of ["[]", '{"items":[]}']) {
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_trusted_network",
        sleep: () => undefined,
        transport: () => response(body),
      }),
      "INVALID_ZCC_COLLECTOR_RESPONSE",
    );
  }
});

test("body, overall, item, and canonical output size limits are fail-closed", async () => {
  await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: () => undefined,
      transport: () => ({
        body: new Uint8Array(4 * 1024 * 1024 + 1),
        status: 200,
      }),
    }),
    "ZCC_COLLECTOR_RESPONSE_LIMIT",
  );

  const oversizedItemList = `[${Array.from({ length: 50_001 }, () => "{}").join(",")}]`;
  await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: () => undefined,
      transport: () => response(oversizedItemList),
    }),
    "INVALID_ZCC_COLLECTOR_RESPONSE",
  );

  const largePage = `[${Array.from({ length: 1000 }, () => {
    return JSON.stringify({ value: "a".repeat(2100) });
  }).join(",")}]`;
  await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_failopen_policy",
      sleep: () => undefined,
      transport: () => response(largePage),
    }),
    "ZCC_COLLECTOR_RESPONSE_LIMIT",
  );

  const unicodeItem = JSON.stringify({ value: "😀".repeat(30) });
  const compactUnicode = `[${Array.from({ length: 15_000 }, () => unicodeItem).join(",")}]`;
  assert.ok(Buffer.byteLength(compactUnicode) < 4 * 1024 * 1024);
  await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_web_privacy",
      sleep: () => undefined,
      transport: () => response(compactUnicode),
    }),
    "ZCC_COLLECTOR_RESPONSE_LIMIT",
  );
});

test("response snapshots use typed-array slots without caller hooks", async () => {
  const privateValue = "snapshot-private-value-314159";
  for (const source of [
    Buffer.from("[]", "utf8"),
    new Uint8Array(Buffer.from("[]", "utf8")),
  ]) {
    let trapCalls = 0;
    const body = new Proxy(source, {
      get() {
        trapCalls += 1;
        throw new ProcessFailure({
          category: "io",
          code: "PRIVATE_PROXY_GET",
          details: [{
            code: "PRIVATE",
            message: privateValue,
            path: privateValue,
          }],
          message: privateValue,
        });
      },
      getOwnPropertyDescriptor() {
        trapCalls += 1;
        throw new Error(privateValue);
      },
      getPrototypeOf() {
        trapCalls += 1;
        throw new Error(privateValue);
      },
      ownKeys() {
        trapCalls += 1;
        throw new Error(privateValue);
      },
    });
    const failure = await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: () => undefined,
        transport: () => ({
          body: body as unknown as Uint8Array,
          status: 200,
        }),
      }),
      "INVALID_ZCC_COLLECTOR_RESPONSE",
    );
    assert.equal(trapCalls, 0);
    assert.equal(JSON.stringify({
      details: failure.details,
      message: failure.message,
      stack: failure.stack,
    }).includes(privateValue), false);
  }

  for (const body of [
    Buffer.from("[]", "utf8"),
    new Uint8Array(Buffer.from("[]", "utf8")),
  ]) {
    let hookCalls = 0;
    Object.defineProperty(body, "byteLength", {
      configurable: true,
      get() {
        hookCalls += 1;
        throw new ProcessFailure({
          category: "io",
          code: "PRIVATE_BYTE_LENGTH",
          message: privateValue,
        });
      },
    });
    Object.defineProperty(body, Symbol.iterator, {
      configurable: true,
      get() {
        hookCalls += 1;
        throw new ProcessFailure({
          category: "io",
          code: "PRIVATE_ITERATOR",
          message: privateValue,
        });
      },
    });
    const result = await collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: () => undefined,
      transport: () => ({ body, status: 200 }),
    });
    assert.equal(result.canonical_json, "[]\n");
    assert.equal(hookCalls, 0);
  }

  const zeroLength = new Uint8Array(0);
  let iteratorCalls = 0;
  Object.defineProperty(zeroLength, Symbol.iterator, {
    configurable: true,
    value() {
      return {
        next() {
          iteratorCalls += 1;
          if (iteratorCalls > 1) {
            throw new Error(privateValue);
          }
          return { done: false, value: 0 };
        },
      };
    },
  });
  const zeroFailure = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: () => undefined,
      transport: () => ({ body: zeroLength, status: 200 }),
    }),
    "INVALID_ZCC_COLLECTOR_RESPONSE",
  );
  assert.equal(iteratorCalls, 0);
  assert.equal(JSON.stringify({
    details: zeroFailure.details,
    message: zeroFailure.message,
    stack: zeroFailure.stack,
  }).includes(privateValue), false);
});

test("response snapshots reject unstable typed-array backings", async () => {
  const detachedBuffer = new ArrayBuffer(2);
  const detachedView = new Uint8Array(detachedBuffer);
  structuredClone(detachedBuffer, { transfer: [detachedBuffer] });

  const resizableBuffer = new ArrayBuffer(2, { maxByteLength: 4 });
  const sharedBuffer = new SharedArrayBuffer(2);
  for (const body of [
    detachedView,
    new Uint8Array(resizableBuffer),
    new Uint8Array(sharedBuffer),
  ]) {
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: () => undefined,
        transport: () => ({ body, status: 200 }),
      }),
      "INVALID_ZCC_COLLECTOR_RESPONSE",
    );
  }
});

test("revoked proxy entry inputs fail with static declared errors", async () => {
  const privateValue = "revoked-input-private-value-271828";
  const endpointInput = Proxy.revocable({
    cloud: privateValue,
    vanityDomain: privateValue,
  }, {});
  endpointInput.revoke();
  const endpointFailure = await captureFailure(
    () => deriveZccOneApiEndpoints(endpointInput.proxy),
    "INVALID_ZCC_ONEAPI_ENDPOINT_INPUT",
  );

  const collectorInput = Proxy.revocable({
    cloud: privateValue,
    resourceType: privateValue,
    sleep: () => undefined,
    transport: () => response("[]"),
  }, {});
  collectorInput.revoke();
  const collectorFailure = await captureFailure(
    () => collectZccOneApiResource(collectorInput.proxy),
    "INVALID_ZCC_COLLECTOR_INPUT",
  );

  for (const failure of [endpointFailure, collectorFailure]) {
    assert.equal(JSON.stringify({
      details: failure.details,
      message: failure.message,
      stack: failure.stack,
    }).includes(privateValue), false);
  }
});

test("large integer tokens, Unicode, and HTML entities remain byte-lossless", async () => {
  const source = '[{"html":"&amp; <b>&#x1F600;</b>","id":900719925474099312345678901234567890,"name":"München 😀"}]';
  const result = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_failopen_policy",
    sleep: () => undefined,
    transport: () => response(source),
  });
  const python = spawnSync(PYTHON_ORACLE, ["-c", [
    "import json,sys",
    "value=json.loads(sys.stdin.read())",
    "sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
  ].join(";")], { encoding: "utf8", input: source });
  assert.equal(python.status, 0, python.stderr);
  assert.equal(result.canonical_json, python.stdout);
  assert.match(result.canonical_json, /900719925474099312345678901234567890/);
  assert.match(result.canonical_json, /&amp; <b>&#x1F600;<\/b>/);
  assert.match(result.canonical_json, /M\\u00fcnchen \\ud83d\\ude00/);
});

test("transport, response-body, and clock diagnostics never relay private values", async () => {
  const privateValue = "collector-private-value-94721";
  const failures = [
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => undefined,
        transport: () => {
          throw new Error(privateValue);
        },
      }),
      "ZCC_COLLECTOR_TRANSPORT_FAILURE",
      "io",
    ),
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => undefined,
        transport: () => new Proxy({}, {
          getPrototypeOf() {
            throw new Error(privateValue);
          },
        }) as unknown as ZccCollectorTransportResponse,
      }),
      "INVALID_ZCC_COLLECTOR_RESPONSE",
    ),
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => undefined,
        transport: () => {
          throw new ProcessFailure({
            category: "io",
            code: "PRIVATE_ADAPTER_FAILURE",
            details: [{
              code: "PRIVATE_DETAIL",
              message: privateValue,
              path: privateValue,
            }],
            message: privateValue,
          });
        },
      }),
      "ZCC_COLLECTOR_TRANSPORT_FAILURE",
      "io",
    ),
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => undefined,
        transport: () => response(privateValue, 500),
      }),
      "ZCC_COLLECTOR_HTTP_STATUS",
      "io",
    ),
    await captureFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_failopen_policy",
        sleep: () => {
          throw new Error(privateValue);
        },
        transport: () => response(privateValue, 429),
      }),
      "ZCC_COLLECTOR_RETRY_CLOCK_FAILURE",
      "io",
    ),
  ];
  for (const failure of failures) {
    assert.equal(JSON.stringify({
      details: failure.details,
      message: failure.message,
      stack: failure.stack,
    }).includes(privateValue), false);
  }
});

test("trusted transport failure lookup is runtime-closed against forged codes", async () => {
  const failure = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: () => undefined,
      transport: () => {
        return throwZccCollectorTransportFailure(
          "FORGED_PRIVATE_CODE" as never,
        );
      },
    }),
    "ZCC_COLLECTOR_TRANSPORT_FAILURE",
    "io",
  );
  assert.equal(failure.message, "ZCC transport failed");

  const privateValue = "private-timeout-cause";
  const timeout = await captureFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_failopen_policy",
      sleep: () => {
        throw new ProcessFailure({
          category: "io",
          code: "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
          details: [{
            code: privateValue,
            message: privateValue,
            path: privateValue,
          }],
          message: privateValue,
        });
      },
      transport: () => response("", 429),
    }),
    "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    "io",
  );
  assert.equal(timeout.message, "ZCC OneAPI transaction exceeded its deadline");
  assert.deepEqual(timeout.details, []);
  assert.equal(JSON.stringify(timeout).includes(privateValue), false);
});

test("production parent excludes private collector implementation and endpoints", () => {
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
    "catalogs/zcc-collector-catalog.v1.json",
    "node-src/domain/zcc-collector-catalog.ts",
    "node-src/domain/zcc-collector.ts",
    "node-src/domain/zcc-oneapi-auth.ts",
    "node-src/io/zcc-oneapi-host.ts",
    "node-src/io/zcc-oneapi-transport.ts",
    "tools/zcc_collector_catalog.py",
  ]) {
    assert.equal(
      inputs.some((input) => input.endsWith(privateInput)),
      false,
      privateInput,
    );
  }
  assert.equal(
    inputs.some((input) => input.includes("node_modules/undici/")),
    false,
    "private Undici transport dependency",
  );
  const bundle = build.outputFiles?.[0]?.text ?? "";
  for (const marker of [
    "infrawright.zcc_collector_catalog",
    "infrawright.zcc_collected_pull",
    "getDeviceCleanupInfo",
    "trustedNetworkContracts",
    "EnvHttpProxyAgent",
    "oauth2/v1/token",
  ]) {
    assert.equal(bundle.includes(marker), false, marker);
  }
  // Closed failure codes are intentionally present in the public parent so it
  // can reconstruct secret-free semantics from the child `{code}` envelope.
  assert.equal(bundle.includes("ZCC_COLLECTOR_TRANSPORT_FAILURE"), true);
  assert.equal(bundle.includes("ZCC_ONEAPI_DIAGNOSTICS_UNSAFE"), true);
});
