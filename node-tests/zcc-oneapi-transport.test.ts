import assert from "node:assert/strict";
import test from "node:test";

import type { Dispatcher } from "undici";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  collectZccOneApiResource,
  ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES,
  type ZccCollectorDataRequest,
} from "../node-src/domain/zcc-collector.js";
import {
  createZccOneApiAuthenticatedTransport,
  type ZccOneApiHttpRequest,
  type ZccOneApiHttpRequestOptions,
  type ZccOneApiHttpResponse,
  type ZccOneApiResponseBody,
  type ZccOneApiTransactionControl,
} from "../node-src/io/zcc-oneapi-transport.js";
import { createZccOneApiTransaction } from "../node-src/io/zcc-oneapi-host.js";

const TOKEN_URL = "https://tenant.zslogin.net/oauth2/v1/token";
const DATA_URL =
  "https://api.zsapi.net/zcc/papi/public/v1/getDeviceCleanupInfo";
const DATA_REQUEST: ZccCollectorDataRequest = Object.freeze({
  kind: "infrawright.zcc_oneapi_data_request",
  method: "GET",
  url: DATA_URL,
});
const FAKE_DISPATCHER = {} as Dispatcher;

class TestBody implements ZccOneApiResponseBody {
  destroyed = 0;

  constructor(private readonly chunks: readonly unknown[]) {}

  destroy(): void {
    this.destroyed += 1;
  }

  async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
    for (const chunk of this.chunks) {
      yield chunk;
    }
  }
}

function bytes(text: string): Uint8Array {
  return Buffer.from(text, "utf8");
}

function httpResponse(
  chunks: readonly unknown[],
  statusCode = 200,
  headers: Readonly<Record<string, string | readonly string[] | undefined>> = {},
): ZccOneApiHttpResponse & { readonly body: TestBody } {
  return {
    body: new TestBody(chunks),
    headers,
    statusCode,
  };
}

function tokenResponse(
  token = "token-one",
  expiresIn: number | string = 60,
): ZccOneApiHttpResponse & { readonly body: TestBody } {
  return httpResponse([
    bytes(JSON.stringify({
      access_token: token,
      expires_in: expiresIn,
      token_type: "Bearer",
    })),
  ]);
}

interface TestTransaction extends ZccOneApiTransactionControl {
  readonly sleeps: number[];
  setNow(value: number): void;
}

function testTransaction(initialNow = 12.75): TestTransaction {
  const controller = new AbortController();
  let now = initialNow;
  const sleeps: number[] = [];
  return {
    checkpoint(): void {
      if (controller.signal.aborted) {
        throw new ProcessFailure({
          category: "io",
          code: "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
          message: "static timeout",
        });
      }
    },
    now(): number {
      return now;
    },
    setNow(value: number): void {
      now = value;
    },
    signal: controller.signal,
    async sleep(milliseconds: number): Promise<void> {
      sleeps.push(milliseconds);
    },
    sleeps,
  };
}

interface CapturedCall {
  readonly options: ZccOneApiHttpRequestOptions;
  readonly url: string;
}

function queuedRequester(
  responses: Array<ZccOneApiHttpResponse | Error>,
  calls: CapturedCall[],
): ZccOneApiHttpRequest {
  return async (url, options) => {
    calls.push({ options, url });
    const next = responses.shift();
    assert.notEqual(next, undefined, "unexpected HTTP request");
    if (next instanceof Error) {
      throw next;
    }
    return next as ZccOneApiHttpResponse;
  };
}

function adapterOptions(
  httpRequest: ZccOneApiHttpRequest,
  transaction = testTransaction(),
) {
  return {
    adapter: createZccOneApiAuthenticatedTransport({
      clientId: "client-id",
      clientSecret: "client-secret",
      cloud: "",
      dispatcher: FAKE_DISPATCHER,
      httpRequest,
      resourceType: "zcc_device_cleanup" as const,
      transaction,
      vanityDomain: "tenant",
    }),
    transaction,
  };
}

async function captureProcessFailure(
  run: () => unknown | Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  let failure: unknown;
  try {
    await run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof ProcessFailure);
  assert.equal(failure.code, code);
  return failure;
}

async function capturePrivateCode(
  run: () => unknown | Promise<unknown>,
  code: string,
): Promise<unknown> {
  let failure: unknown;
  try {
    await run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof Error);
  assert.equal((failure as { code?: unknown }).code, code);
  return failure;
}

test("real adapter sends exact OAuth form and one authorized Bearer request", async () => {
  const calls: CapturedCall[] = [];
  const requester = queuedRequester([
    tokenResponse(),
    httpResponse([bytes('{"id":"1"}')]),
  ], calls);
  const { adapter, transaction } = adapterOptions(requester);
  const result = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_device_cleanup",
    sleep: transaction.sleep,
    transport: adapter.transport,
  });
  assert.equal(result.canonical_json, '[\n  {\n    "id": "1"\n  }\n]\n');
  assert.equal(calls.length, 2);
  const auth = calls[0];
  const data = calls[1];
  assert.notEqual(auth, undefined);
  assert.notEqual(data, undefined);
  if (auth === undefined || data === undefined) {
    return;
  }
  assert.equal(auth.url, TOKEN_URL);
  assert.equal(auth.options.method, "POST");
  assert.deepEqual(auth.options.headers, {
    accept: "application/json",
    "accept-encoding": "identity",
    "content-type": "application/x-www-form-urlencoded",
  });
  assert.deepEqual([...new URLSearchParams(auth.options.body)], [
    ["grant_type", "client_credentials"],
    ["client_id", "client-id"],
    ["client_secret", "client-secret"],
    ["audience", "https://api.zscaler.com"],
  ]);
  assert.equal(data.url, DATA_URL);
  assert.equal(data.options.method, "GET");
  assert.deepEqual(data.options.headers, {
    accept: "application/json",
    "accept-encoding": "identity",
    authorization: "Bearer token-one",
  });
  assert.equal(auth.options.signal, data.options.signal);
  assert.deepEqual(adapter.stats(), {
    auth_requests: 1,
    wire_data_requests: 1,
  });
});

test("authentication 429 retry is bounded and uses the collector schedule", async () => {
  const calls: CapturedCall[] = [];
  const responses = Array.from({ length: 6 }, (_unused, index) => {
    return httpResponse([], 429, index === 0 ? { "retry-after": "0.25" } : {});
  });
  const { adapter, transaction } = adapterOptions(
    queuedRequester(responses, calls),
  );
  const failure = await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: transaction.sleep,
      transport: adapter.transport,
    }),
    "ZCC_ONEAPI_AUTH_RATE_LIMITED",
  );
  assert.equal(failure.retryable, true);
  assert.equal(calls.length, 6);
  assert.deepEqual(transaction.sleeps, [250, 2_000, 4_000, 8_000, 16_000]);
  assert.ok(responses.every((response) => response.body.destroyed === 1));
});

test("authentication requires status 200 and discards every error body", async () => {
  for (const status of [201, 400, 401, 500]) {
    const rejected = httpResponse([bytes("private error body")], status);
    const { adapter, transaction } = adapterOptions(
      queuedRequester([rejected], []),
    );
    await captureProcessFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: transaction.sleep,
        transport: adapter.transport,
      }),
      "ZCC_ONEAPI_AUTH_HTTP_STATUS",
    );
    assert.equal(rejected.body.destroyed, 1);
  }
});

test("data 429 remains kernel-owned rather than being retried by the adapter", async () => {
  const calls: CapturedCall[] = [];
  const rateLimited = httpResponse([], 429, { "retry-after": "0" });
  const { adapter, transaction } = adapterOptions(queuedRequester([
    tokenResponse(),
    rateLimited,
    httpResponse([bytes('{"id":"1"}')]),
  ], calls));
  const result = await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_device_cleanup",
    sleep: transaction.sleep,
    transport: adapter.transport,
  });
  assert.equal(result.metadata.transport_attempts, 2);
  assert.deepEqual(transaction.sleeps, [0]);
  assert.deepEqual(adapter.stats(), {
    auth_requests: 1,
    wire_data_requests: 2,
  });
  assert.equal(rateLimited.body.destroyed, 1);
  assert.equal(calls.filter((call) => call.url === TOKEN_URL).length, 1);
});

test("one data 401 refreshes and replays once without a loop", async () => {
  const calls: CapturedCall[] = [];
  const firstUnauthorized = httpResponse([], 401);
  const { adapter, transaction } = adapterOptions(queuedRequester([
    tokenResponse("token-one"),
    firstUnauthorized,
    tokenResponse("token-two"),
    httpResponse([bytes('{"id":"1"}')]),
  ], calls));
  await collectZccOneApiResource({
    cloud: "",
    resourceType: "zcc_device_cleanup",
    sleep: transaction.sleep,
    transport: adapter.transport,
  });
  assert.equal(firstUnauthorized.body.destroyed, 1);
  assert.deepEqual(
    calls.filter((call) => call.url === DATA_URL).map((call) => {
      return call.options.headers.authorization;
    }),
    ["Bearer token-one", "Bearer token-two"],
  );
  assert.deepEqual(adapter.stats(), {
    auth_requests: 2,
    wire_data_requests: 2,
  });

  const secondCalls: CapturedCall[] = [];
  const second = adapterOptions(queuedRequester([
    tokenResponse("first"),
    httpResponse([], 401),
    tokenResponse("second"),
    httpResponse([], 401),
  ], secondCalls));
  await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: second.transaction.sleep,
      transport: second.adapter.transport,
    }),
    "ZCC_COLLECTOR_HTTP_STATUS",
  );
  assert.deepEqual(second.adapter.stats(), {
    auth_requests: 2,
    wire_data_requests: 2,
  });
});

test("lazy lifetime refresh uses the fractional monotonic clock", async () => {
  const calls: CapturedCall[] = [];
  const transaction = testTransaction(10.5);
  const { adapter } = adapterOptions(queuedRequester([
    tokenResponse("one"),
    httpResponse([bytes("[]")]),
    tokenResponse("two"),
    httpResponse([bytes("[]")]),
  ], calls), transaction);
  await adapter.transport(DATA_REQUEST);
  transaction.setNow(30_011);
  await adapter.transport(DATA_REQUEST);
  assert.deepEqual(adapter.stats(), {
    auth_requests: 2,
    wire_data_requests: 2,
  });
});

test("redirects are refused without forwarding credentials", async () => {
  const authCalls: CapturedCall[] = [];
  const authRedirect = httpResponse([], 302, { location: "https://evil.invalid" });
  const auth = adapterOptions(queuedRequester([authRedirect], authCalls));
  await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: auth.transaction.sleep,
      transport: auth.adapter.transport,
    }),
    "ZCC_ONEAPI_REDIRECT_REFUSED",
  );
  assert.equal(authCalls.length, 1);
  assert.equal(authRedirect.body.destroyed, 1);

  const dataCalls: CapturedCall[] = [];
  const dataRedirect = httpResponse([], 307, { location: "https://evil.invalid" });
  const data = adapterOptions(queuedRequester([
    tokenResponse(),
    dataRedirect,
  ], dataCalls));
  await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: data.transaction.sleep,
      transport: data.adapter.transport,
    }),
    "ZCC_ONEAPI_REDIRECT_REFUSED",
  );
  assert.equal(dataCalls.length, 2);
  assert.equal(dataRedirect.body.destroyed, 1);
});

test("token JSON is fatal-UTF8, duplicate-key closed, and body bounded", async () => {
  const cases: Array<{
    expected: string;
    response: ZccOneApiHttpResponse & { readonly body: TestBody };
  }> = [
    {
      expected: "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
      response: httpResponse([new Uint8Array([0xff])]),
    },
    {
      expected: "ZCC_ONEAPI_AUTH_RESPONSE_INVALID",
      response: httpResponse([bytes(
        '{"access_token":"one","access_token":"two","expires_in":60}',
      )]),
    },
    {
      expected: "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT",
      response: httpResponse([new Uint8Array(64 * 1024 + 1)]),
    },
    {
      expected: "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT",
      response: httpResponse([new Uint8Array(64 * 1024), new Uint8Array(1)]),
    },
    {
      expected: "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT",
      response: httpResponse([], 200, { "content-length": "65537" }),
    },
  ];
  for (const entry of cases) {
    const calls: CapturedCall[] = [];
    const { adapter, transaction } = adapterOptions(
      queuedRequester([entry.response], calls),
    );
    await captureProcessFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: transaction.sleep,
        transport: adapter.transport,
      }),
      entry.expected,
    );
    assert.equal(
      entry.response.body.destroyed,
      entry.expected === "ZCC_ONEAPI_AUTH_RESPONSE_LIMIT" ? 1 : 0,
    );
  }
});

test("data streaming enforces the cap before copy and bounds fragmentation metadata", async () => {
  const exact = new Uint8Array(ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES);
  const exactCalls: CapturedCall[] = [];
  const exactAdapter = adapterOptions(queuedRequester([
    tokenResponse(),
    httpResponse([exact]),
  ], exactCalls));
  const exactResult = await exactAdapter.adapter.transport(DATA_REQUEST);
  assert.equal(exactResult.body.byteLength, ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES);

  for (const chunks of [
    [new Uint8Array(ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES + 1)],
    [new Uint8Array(ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES), new Uint8Array(1)],
  ]) {
    const calls: CapturedCall[] = [];
    const oversized = httpResponse(chunks);
    const { adapter } = adapterOptions(queuedRequester([
      tokenResponse(),
      oversized,
    ], calls));
    await capturePrivateCode(
      () => adapter.transport(DATA_REQUEST),
      "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
    );
    assert.equal(oversized.body.destroyed, 1);
  }

  const fragmented = httpResponse([
    ...Array.from({ length: 20_000 }, () => new Uint8Array()),
    bytes("[]"),
  ]);
  const fragmentedAdapter = adapterOptions(queuedRequester([
    tokenResponse(),
    fragmented,
  ], []));
  const fragmentedResult = await fragmentedAdapter.adapter.transport(DATA_REQUEST);
  assert.deepEqual([...fragmentedResult.body], [...bytes("[]")]);

  const excessiveFragmentation = httpResponse(
    Array.from({ length: 32 * 1024 + 1 }, () => new Uint8Array()),
  );
  const excessiveAdapter = adapterOptions(queuedRequester([
    tokenResponse(),
    excessiveFragmentation,
  ], [])).adapter;
  await capturePrivateCode(
    () => excessiveAdapter.transport(DATA_REQUEST),
    "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
  );
  assert.equal(excessiveFragmentation.body.destroyed, 1);
});

test("data content length preflight rejects without iterating the body", async () => {
  let iterated = false;
  const body: ZccOneApiResponseBody = {
    destroy() {
      return undefined;
    },
    async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
      iterated = true;
      yield bytes("[]");
    },
  };
  const response: ZccOneApiHttpResponse = {
    body,
    headers: { "content-length": String(ZCC_COLLECTOR_RESPONSE_LIMIT_BYTES + 1) },
    statusCode: 200,
  };
  const adapter = adapterOptions(queuedRequester([
    tokenResponse(),
    response,
  ], [])).adapter;
  await capturePrivateCode(
    () => adapter.transport(DATA_REQUEST),
    "ZCC_ONEAPI_DATA_RESPONSE_LIMIT",
  );
  assert.equal(iterated, false);
});

test("request authority rejects alternate origins, paths, queries, symbols, and proxies before auth", async () => {
  let requests = 0;
  const { adapter } = adapterOptions(async () => {
    requests += 1;
    return tokenResponse();
  });
  const withSymbol = {
    ...DATA_REQUEST,
    [Symbol("secret")]: "value",
  };
  const revoked = Proxy.revocable({ ...DATA_REQUEST }, {});
  revoked.revoke();
  for (const request of [
    { ...DATA_REQUEST, url: "https://evil.invalid/" },
    { ...DATA_REQUEST, url: `${DATA_URL}/extra` },
    { ...DATA_REQUEST, url: `${DATA_URL}?page=1` },
    withSymbol,
    revoked.proxy,
  ]) {
    await capturePrivateCode(
      () => adapter.transport(request as ZccCollectorDataRequest),
      "INVALID_ZCC_ONEAPI_DATA_REQUEST",
    );
  }
  assert.equal(requests, 0);
});

test("network failures and nested causes never relay credentials or tokens", async () => {
  const secret = "private-client-secret";
  const calls: CapturedCall[] = [];
  const authFailure = adapterOptions(queuedRequester([
    new Error(`${secret} https://tenant.zslogin.net`),
  ], calls));
  const failure = await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: authFailure.transaction.sleep,
      transport: authFailure.adapter.transport,
    }),
    "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
  );
  assert.equal(JSON.stringify({
    details: failure.details,
    message: failure.message,
    stack: failure.stack,
  }).includes(secret), false);

  const dataFailure = adapterOptions(queuedRequester([
    tokenResponse(secret),
    new Error(secret),
  ], []));
  const second = await captureProcessFailure(
    () => collectZccOneApiResource({
      cloud: "",
      resourceType: "zcc_device_cleanup",
      sleep: dataFailure.transaction.sleep,
      transport: dataFailure.adapter.transport,
    }),
    "ZCC_ONEAPI_DATA_TRANSPORT_FAILED",
  );
  assert.equal(JSON.stringify(second).includes(secret), false);
  assert.equal(second.message.includes(secret), false);
});

test("one overall deadline aborts request, body, auth-sleep, and kernel-sleep stalls", async () => {
  const stalledRequest = async (
    _url: string,
    options: ZccOneApiHttpRequestOptions,
  ): Promise<ZccOneApiHttpResponse> => {
    return new Promise<ZccOneApiHttpResponse>((_resolve, reject) => {
      const onAbort = (): void => reject(new Error("private stalled request"));
      options.signal.addEventListener("abort", onAbort, { once: true });
      if (options.signal.aborted) {
        onAbort();
      }
    });
  };
  const requestTransaction = createZccOneApiTransaction(20);
  try {
    const requestAdapter = createZccOneApiAuthenticatedTransport({
      clientId: "client-id",
      clientSecret: "client-secret",
      cloud: "",
      dispatcher: FAKE_DISPATCHER,
      httpRequest: stalledRequest,
      resourceType: "zcc_device_cleanup",
      transaction: requestTransaction,
      vanityDomain: "tenant",
    });
    await captureProcessFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: requestTransaction.sleep,
        transport: requestAdapter.transport,
      }),
      "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    );
  } finally {
    requestTransaction.finish();
  }

  const bodyTransaction = createZccOneApiTransaction(20);
  const stalledBody: ZccOneApiResponseBody = {
    destroy() {
      return undefined;
    },
    async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
      await new Promise<void>((_resolve, reject) => {
        const onAbort = (): void => reject(new Error("private stalled body"));
        bodyTransaction.signal.addEventListener("abort", onAbort, { once: true });
        if (bodyTransaction.signal.aborted) {
          onAbort();
        }
      });
    },
  };
  try {
    const bodyAdapter = createZccOneApiAuthenticatedTransport({
      clientId: "client-id",
      clientSecret: "client-secret",
      cloud: "",
      dispatcher: FAKE_DISPATCHER,
      httpRequest: queuedRequester([
        tokenResponse(),
        { body: stalledBody, headers: {}, statusCode: 200 },
      ], []),
      resourceType: "zcc_device_cleanup",
      transaction: bodyTransaction,
      vanityDomain: "tenant",
    });
    await captureProcessFailure(
      () => collectZccOneApiResource({
        cloud: "",
        resourceType: "zcc_device_cleanup",
        sleep: bodyTransaction.sleep,
        transport: bodyAdapter.transport,
      }),
      "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    );
  } finally {
    bodyTransaction.finish();
  }

  for (const retryPhase of ["auth", "data"] as const) {
    const transaction = createZccOneApiTransaction(20);
    try {
      const requester = retryPhase === "auth"
        ? queuedRequester([httpResponse([], 429)], [])
        : queuedRequester([tokenResponse(), httpResponse([], 429)], []);
      const adapter = createZccOneApiAuthenticatedTransport({
        clientId: "client-id",
        clientSecret: "client-secret",
        cloud: "",
        dispatcher: FAKE_DISPATCHER,
        httpRequest: requester,
        resourceType: "zcc_device_cleanup",
        transaction,
        vanityDomain: "tenant",
      });
      await captureProcessFailure(
        () => collectZccOneApiResource({
          cloud: "",
          resourceType: "zcc_device_cleanup",
          sleep: transaction.sleep,
          transport: adapter.transport,
        }),
        "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
      );
    } finally {
      transaction.finish();
    }
  }
});
