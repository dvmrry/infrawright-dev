import assert from "node:assert/strict";
import test from "node:test";

import {
  collectorAuthMode,
  collectorContext,
  createZscalerCollectorAdapters,
  diagnosticHosts,
  fetchDebugLines,
  maskCollectorIdentifiers,
  normalizeLegacyBaseUrl,
  obfuscateZiaApiKey,
} from "../node-src/collectors/zscaler-adapters.js";
import {
  type HttpRequest,
  type HttpResponse,
  type HttpTransport,
} from "../node-src/collectors/types.js";

const EMPTY_CONTEXT = Object.freeze({ cloud: "", customerId: "" });

function response(status: number, body: unknown = {}): HttpResponse {
  return {
    body: new TextEncoder().encode(JSON.stringify(body)),
    headers: Object.freeze({}),
    status,
  };
}

class RecordingTransport implements HttpTransport {
  readonly requests: HttpRequest[] = [];
  readonly responses: HttpResponse[];

  constructor(...responses: HttpResponse[]) {
    this.responses = responses;
  }

  async request(request: HttpRequest): Promise<HttpResponse> {
    this.requests.push(request);
    const next = this.responses.shift();
    assert.notEqual(next, undefined, "test transport ran out of responses");
    return next as HttpResponse;
  }
}

test("collector auth mode matches the Python truthy vocabulary", () => {
  for (const value of ["1", "true", "TRUE", " yes ", "on"]) {
    assert.equal(collectorAuthMode({ ZSCALER_USE_LEGACY_CLIENT: value }), "legacy");
  }
  for (const value of [undefined, "", "0", "false", "disabled"]) {
    assert.equal(collectorAuthMode({ ZSCALER_USE_LEGACY_CLIENT: value }), "oneapi");
  }
});

test("collector context scopes customer id and ignores stale ZIA cloud in OneAPI", () => {
  assert.deepEqual(
    collectorContext({
      environment: {
        ZIA_CLOUD: "stale-zia",
        ZPA_CUSTOMER_ID: "customer",
        ZSCALER_CLOUD: "production",
      },
      mode: "oneapi",
      neededProducts: new Set(["zia"]),
    }),
    { cloud: "production", customerId: "customer" },
  );

  assert.throws(
    () => collectorContext({
      environment: {},
      neededProducts: new Set(["zpa"]),
    }),
    /missing required env var ZPA_CUSTOMER_ID/,
  );
});

test("legacy context validates host overrides and retains the ZPA cloud", () => {
  assert.deepEqual(
    collectorContext({
      environment: {
        ZIA_CLOUD: "zscalertwo",
        ZIA_LEGACY_BASE_URL: "https://ZIA.example.test/",
        ZPA_CLOUD: "ZPATWO",
        ZPA_CUSTOMER_ID: "customer",
        ZPA_LEGACY_BASE_URL: "https://ZPA.example.test:8443",
      },
      mode: "legacy",
      neededProducts: new Set(["zia", "zpa"]),
    }),
    {
      cloud: "zscalertwo",
      customerId: "customer",
      ziaLegacyBase: "https://zia.example.test",
      zpaCloud: "ZPATWO",
      zpaLegacyBase: "https://zpa.example.test:8443",
    },
  );
  for (const value of [
    "http://example.test",
    "https://user:secret@example.test",
    "https://example.test/path",
    "https://example.test?query=1",
    "https://example.test#fragment",
  ]) {
    assert.throws(
      () => normalizeLegacyBaseUrl("ZIA_LEGACY_BASE_URL", value),
      /ZIA_LEGACY_BASE_URL/,
    );
  }
});

test("all products share the exact OneAPI auth and compose their own URLs", async () => {
  const adapters = createZscalerCollectorAdapters();
  assert.deepEqual([...adapters.keys()], ["zia", "zpa", "zcc", "ztc"]);
  for (const product of adapters.keys()) {
    const transport = new RecordingTransport(response(200, { access_token: "token" }));
    const adapter = adapters.get(product);
    assert.notEqual(adapter, undefined);
    const auth = await adapter?.acquire({
      context: { cloud: "production", customerId: "123" },
      environment: {
        ZSCALER_CLIENT_ID: "client",
        ZSCALER_CLIENT_SECRET: "secret",
        ZSCALER_CLOUD: "production",
        ZSCALER_VANITY_DOMAIN: "tenant",
      },
      mode: "oneapi",
      transport,
    });
    assert.deepEqual(auth?.headers, {
      Accept: "application/json",
      Authorization: "Bearer token",
    });
    assert.equal(
      transport.requests[0]?.url.toString(),
      "https://tenant.zslogin.net/oauth2/v1/token",
    );
    assert.equal(
      transport.requests[0]?.body,
      "grant_type=client_credentials&client_id=client&client_secret=secret&audience=https%3A%2F%2Fapi.zscaler.com",
    );
  }

  const context = { cloud: "zscalertwo", customerId: "customer-7" };
  assert.equal(
    adapters.get("zia")?.composeUrl({ mode: "oneapi", context, path: "urlCategories" }).toString(),
    "https://api.zscalertwo.zsapi.net/zia/api/v1/urlCategories",
  );
  assert.equal(
    adapters.get("zpa")?.composeUrl({ mode: "oneapi", context, path: "segmentGroup" }).toString(),
    "https://api.zscalertwo.zsapi.net/zpa/mgmtconfig/v1/admin/customers/customer-7/segmentGroup",
  );
  assert.equal(
    adapters.get("zcc")?.composeUrl({ mode: "oneapi", context, path: "zcc/papi/public/v1/test" }).toString(),
    "https://api.zscalertwo.zsapi.net/zcc/papi/public/v1/test",
  );
  assert.equal(
    adapters.get("ztc")?.composeUrl({ mode: "oneapi", context, path: "/ztw/api/v1/test" }).toString(),
    "https://api.zscalertwo.zsapi.net/ztw/api/v1/test",
  );
});

test("OneAPI host inputs reject cloud and vanity smuggling", async () => {
  const adapter = createZscalerCollectorAdapters().get("zia");
  assert.ok(adapter);
  await assert.rejects(
    adapter.acquire({
      context: EMPTY_CONTEXT,
      environment: {
        ZSCALER_CLIENT_ID: "client",
        ZSCALER_CLIENT_SECRET: "secret",
        ZSCALER_CLOUD: ".attacker.test/x",
        ZSCALER_VANITY_DOMAIN: "tenant",
      },
      mode: "oneapi",
      transport: new RecordingTransport(),
    }),
    /ZSCALER_CLOUD must be a DNS label/,
  );
  await assert.rejects(
    adapter.acquire({
      context: EMPTY_CONTEXT,
      environment: {
        ZSCALER_CLIENT_ID: "client",
        ZSCALER_CLIENT_SECRET: "secret",
        ZSCALER_VANITY_DOMAIN: "tenant.attacker",
      },
      mode: "oneapi",
      transport: new RecordingTransport(),
    }),
    /ZSCALER_VANITY_DOMAIN must be a DNS label/,
  );
});

test("ZIA legacy auth obfuscates the key and relies on the transport cookie session", async () => {
  assert.equal(obfuscateZiaApiKey("0123456789ab", "1700001234567"), "2345673394a5");
  const transport = new RecordingTransport(response(200));
  const adapter = createZscalerCollectorAdapters().get("zia");
  const auth = await adapter?.acquire({
    context: { cloud: "zscalertwo", customerId: "" },
    environment: {
      ZIA_API_KEY: "0123456789ab",
      ZIA_PASSWORD: "password",
      ZIA_USERNAME: "user",
    },
    mode: "legacy",
    nowMs: 1_700_001_234_567,
    transport,
  });
  assert.deepEqual(auth?.headers, { Accept: "application/json" });
  assert.equal(
    transport.requests[0]?.url.toString(),
    "https://zsapi.zscalertwo.net/api/v1/authenticatedSession",
  );
  assert.deepEqual(JSON.parse(String(transport.requests[0]?.body)), {
    apiKey: "2345673394a5",
    password: "password",
    timestamp: "1700001234567",
    username: "user",
  });
  assert.equal(
    adapter?.composeUrl({
      context: { cloud: "zscalertwo", customerId: "" },
      mode: "legacy",
      path: "urlCategories",
    }).toString(),
    "https://zsapi.zscalertwo.net/api/v1/urlCategories",
  );
});

test("ZPA legacy auth and cloud bases match provider hosts", async () => {
  const adapter = createZscalerCollectorAdapters().get("zpa");
  for (const [cloud, expected] of [
    ["", "https://config.private.zscaler.com"],
    ["production", "https://config.private.zscaler.com"],
    ["zpatwo", "https://config.zpatwo.net"],
    ["beta", "https://config.zpabeta.net"],
    ["gov", "https://config.zpagov.net"],
    ["govus", "https://config.zpagov.us"],
  ] as const) {
    assert.equal(
      adapter?.composeUrl({
        context: { cloud: "", customerId: "customer", zpaCloud: cloud },
        mode: "legacy",
        path: "segmentGroup",
      }).origin,
      expected,
    );
  }
  assert.throws(
    () => adapter?.composeUrl({
      context: { cloud: "", customerId: "customer", zpaCloud: "private" },
      mode: "legacy",
      path: "segmentGroup",
    }),
    /unknown ZPA_CLOUD/,
  );

  const transport = new RecordingTransport(response(200, { access_token: "zpa-token" }));
  const auth = await adapter?.acquire({
    context: { cloud: "", customerId: "customer", zpaCloud: "ZPATWO" },
    environment: { ZPA_CLIENT_ID: "client", ZPA_CLIENT_SECRET: "secret" },
    mode: "legacy",
    transport,
  });
  assert.equal(transport.requests[0]?.url.toString(), "https://config.zpatwo.net/signin");
  assert.equal(transport.requests[0]?.body, "client_id=client&client_secret=secret");
  assert.deepEqual(auth?.headers, {
    Accept: "application/json",
    Authorization: "Bearer zpa-token",
  });
});

test("legacy ZCC and ZTC failures retain the product-scoping remediation", async () => {
  const adapters = createZscalerCollectorAdapters();
  const zcc = adapters.get("zcc");
  const ztc = adapters.get("ztc");
  assert.ok(zcc);
  assert.ok(ztc);
  await assert.rejects(
    zcc.acquire({
      context: EMPTY_CONTEXT,
      environment: {},
      mode: "legacy",
      transport: new RecordingTransport(),
    }),
    /ZCC has no legacy auth path.*RESOURCE="zia zpa"/,
  );
  await assert.rejects(
    ztc.acquire({
      context: EMPTY_CONTEXT,
      environment: {},
      mode: "legacy",
      transport: new RecordingTransport(),
    }),
    /ZTC legacy auth is not wired.*RESOURCE="zia zpa zcc"/,
  );
});

test("diagnostic helpers mask identities and derive the same hosts", () => {
  assert.equal(
    maskCollectorIdentifiers(
      "https://tenant.zsloginzscalertwo.net/zpa/customers/123/segmentGroup",
    ),
    "https://<vanity>.zsloginzscalertwo.net/zpa/customers/<customer-id>/segmentGroup",
  );
  const environment = {
    HTTPS_PROXY: "https://secret-proxy.example",
    ZPA_CUSTOMER_ID: "customer",
    ZSCALER_CLOUD: "zscalertwo",
    ZSCALER_VANITY_DOMAIN: "tenant",
  };
  const context = collectorContext({
    environment,
    mode: "oneapi",
    neededProducts: new Set(["zpa"]),
  });
  assert.deepEqual(
    fetchDebugLines({
      context,
      environment,
      mode: "oneapi",
      products: new Set(["zpa"]),
    }),
    [
      "fetch: auth mode = oneapi",
      "fetch: proxy = set",
      "fetch: ZSCALER_CLOUD = zscalertwo",
      "fetch: ZSCALER_VANITY_DOMAIN = set",
      "fetch: ZPA_CUSTOMER_ID = set",
      "fetch: token host = https://<vanity>.zsloginzscalertwo.net",
      "fetch: gateway = https://api.zscalertwo.zsapi.net",
      "fetch: (vanity/customer-id hidden; set FETCH_DEBUG=1 to show)",
    ],
  );
  assert.deepEqual(
    diagnosticHosts(environment, new Set(["zia", "zpa"])),
    ["api.zscalertwo.zsapi.net", "tenant.zsloginzscalertwo.net"],
  );
  assert.deepEqual(
    diagnosticHosts({
      ZIA_CLOUD: "zscalertwo",
      ZPA_CLOUD: "GOVUS",
      ZSCALER_USE_LEGACY_CLIENT: "1",
    }, new Set(["zia", "zpa", "zcc"])),
    ["config.zpagov.us", "zsapi.zscalertwo.net"],
  );
});
