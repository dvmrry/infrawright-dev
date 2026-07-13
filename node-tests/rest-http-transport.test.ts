import assert from "node:assert/strict";
import { getCACertificates } from "node:tls";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import type { Dispatcher } from "undici";

import { probeRestHost, probeRestHosts } from "../node-src/collectors/rest-diagnostics.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  createRestHttpTransport,
  snapshotRestProxyEnvironment,
  type RestUndiciRequest,
} from "../node-src/io/rest-http-transport.js";

class TestBody implements AsyncIterable<unknown> {
  destroyed = 0;

  constructor(private readonly chunks: readonly Uint8Array[]) {}

  destroy(): void {
    this.destroyed += 1;
  }

  async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
    for (const chunk of this.chunks) yield chunk;
  }
}

function response(
  text: string,
  statusCode = 200,
  headers: Readonly<Record<string, string | readonly string[] | undefined>> = {},
) {
  return {
    body: new TestBody([Buffer.from(text, "utf8")]),
    headers,
    statusCode,
  };
}

function fakeDispatcher(options: {
  close?: () => Promise<void>;
  destroy?: () => Promise<void>;
} = {}): Dispatcher {
  return {
    close: options.close ?? (async () => undefined),
    destroy: options.destroy ?? (async () => undefined),
  } as unknown as Dispatcher;
}

async function captureFailure(
  run: () => unknown | Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  let caught: unknown;
  try {
    await run();
  } catch (error: unknown) {
    caught = error;
  }
  assert.ok(caught instanceof ProcessFailure);
  assert.equal(caught.code, code);
  return caught;
}

test("proxy snapshot uses lowercase precedence, HTTPS fallback, and explicit empties", () => {
  assert.deepEqual(snapshotRestProxyEnvironment({}), {
    httpProxy: "",
    httpsProxy: "",
    noProxy: "",
  });
  assert.deepEqual(snapshotRestProxyEnvironment({
    HTTP_PROXY: "http://upper.invalid:8080",
    http_proxy: "http://lower.invalid:8081",
    HTTPS_PROXY: "http://https-upper.invalid:8082",
    https_proxy: "",
    NO_PROXY: "upper.invalid",
    no_proxy: "lower.invalid",
  }), {
    httpProxy: "http://lower.invalid:8081/",
    httpsProxy: "",
    noProxy: "lower.invalid",
  });
  assert.deepEqual(snapshotRestProxyEnvironment({
    HTTP_PROXY: "http://proxy.invalid:8080",
  }), {
    httpProxy: "http://proxy.invalid:8080/",
    httpsProxy: "http://proxy.invalid:8080/",
    noProxy: "",
  });
});

test("configured CA is added to system trust without replacing it", async (t) => {
  const directory = await mkdtemp(path.join(tmpdir(), "infrawright-rest-ca-"));
  t.after(async () => rm(directory, { force: true, recursive: true }));
  const custom = getCACertificates("default")[0];
  assert.notEqual(custom, undefined);
  const bundle = path.join(directory, "custom.pem");
  await writeFile(bundle, custom ?? "", "utf8");
  let capturedCa: readonly string[] | undefined;
  const transport = await createRestHttpTransport(
    { REQUESTS_CA_BUNDLE: bundle },
    {
      createDispatcher(options) {
        capturedCa = (options.connect as { readonly ca?: readonly string[] } | undefined)?.ca;
        return fakeDispatcher();
      },
      httpRequest: async () => response("[]"),
    },
  );
  await transport.request({ method: "GET", url: new URL("https://example.test/") });
  await transport.close?.();
  assert.ok(Array.isArray(capturedCa));
  assert.ok((capturedCa?.length ?? 0) > 1);
  assert.equal(capturedCa?.includes(custom ?? ""), true);
});

test("invalid CA input fails before constructing a dispatcher", async (t) => {
  const directory = await mkdtemp(path.join(tmpdir(), "infrawright-rest-bad-ca-"));
  t.after(async () => rm(directory, { force: true, recursive: true }));
  const bundle = path.join(directory, "invalid.pem");
  await writeFile(bundle, "not a certificate", "utf8");
  let dispatchers = 0;
  await captureFailure(
    () => createRestHttpTransport(
      { SSL_CERT_FILE: bundle },
      {
        createDispatcher() {
          dispatchers += 1;
          return fakeDispatcher();
        },
      },
    ),
    "REST_CA_BUNDLE_FAILED",
  );
  assert.equal(dispatchers, 0);
});

test("transport persists scoped session cookies from legacy auth into data GETs", async () => {
  const observed: Array<{
    readonly method: string;
    readonly url: string;
    readonly headers: Readonly<Record<string, string>>;
  }> = [];
  const requester: RestUndiciRequest = async (url, options) => {
    observed.push({ method: options.method, url: url.toString(), headers: options.headers });
    return observed.length === 1
      ? response("{}", 200, {
          "set-cookie": [
            "JSESSIONID=session-value; Path=/api; Secure; HttpOnly",
            "narrow=ignored; Path=/different; Secure",
          ],
        })
      : response("[]");
  };
  const transport = await createRestHttpTransport({}, {
    createDispatcher: () => fakeDispatcher(),
    httpRequest: requester,
  });
  try {
    await transport.request({
      body: "{}",
      headers: { "content-type": "application/json" },
      method: "POST",
      url: new URL("https://zsapi.example.test/api/v1/authenticatedSession"),
    });
    await transport.request({
      method: "GET",
      url: new URL("https://zsapi.example.test/api/v1/urlCategories"),
    });
  } finally {
    await transport.close?.();
  }
  assert.equal(observed.length, 2);
  assert.equal(observed[1]?.headers.cookie, "JSESSIONID=session-value");
});

test("transport follows Python-style POST redirects, captures cookies, and strips cross-origin auth", async () => {
  const observed: Array<{
    readonly method: string;
    readonly url: string;
    readonly headers: Readonly<Record<string, string>>;
  }> = [];
  const requester: RestUndiciRequest = async (url, options) => {
    observed.push({ method: options.method, url: url.toString(), headers: options.headers });
    return observed.length === 1
      ? response("redirect", 302, {
          location: "https://other.example.test/final",
          "set-cookie": ["sid=value; Path=/; Secure"],
        })
      : response("done", 200);
  };
  const transport = await createRestHttpTransport({}, {
    createDispatcher: () => fakeDispatcher(),
    httpRequest: requester,
  });
  try {
    const result = await transport.request({
      body: "secret-body",
      headers: {
        authorization: "Bearer secret",
        "content-type": "application/x-www-form-urlencoded",
      },
      method: "POST",
      url: new URL("https://first.example.test/start"),
    });
    assert.equal(Buffer.from(result.body).toString("utf8"), "done");
  } finally {
    await transport.close?.();
  }
  assert.equal(observed[1]?.method, "GET");
  assert.equal(observed[1]?.headers.authorization, undefined);
  assert.equal(observed[1]?.headers.cookie, undefined);
  assert.equal(observed[1]?.headers["content-type"], undefined);
});

test("transport retries auth and data requests on 429 inside the production seam", async () => {
  let requests = 0;
  const waits: number[] = [];
  const transport = await createRestHttpTransport({}, {
    createDispatcher: () => fakeDispatcher(),
    httpRequest: async () => {
      requests += 1;
      if (requests === 1) return response("rate", 429, { "retry-after": "0.25" });
      if (requests === 2) return response("rate", 429);
      return response("ok", 200);
    },
    sleep(milliseconds) {
      waits.push(milliseconds);
    },
  });
  try {
    const result = await transport.request({
      body: "grant_type=client_credentials",
      method: "POST",
      url: new URL("https://tenant.zslogin.net/oauth2/v1/token"),
    });
    assert.equal(result.status, 200);
  } finally {
    await transport.close?.();
  }
  assert.equal(requests, 3);
  assert.deepEqual(waits, [250, 2_000]);
});

test("response limit fails loudly and destroys an oversized declared body", async () => {
  const body = new TestBody([Buffer.from("too large", "utf8")]);
  const transport = await createRestHttpTransport({}, {
    createDispatcher: () => fakeDispatcher(),
    httpRequest: async () => ({
      body,
      headers: { "content-length": "9" },
      statusCode: 200,
    }),
    responseLimitBytes: 4,
  });
  try {
    await captureFailure(
      () => transport.request({
        method: "GET",
        url: new URL("https://api.example.test/data"),
      }),
      "REST_HTTP_RESPONSE_LIMIT",
    );
  } finally {
    await transport.close?.();
  }
  assert.equal(body.destroyed, 1);
});

test("transport failures mask tenant identity and do not relay nested secrets", async () => {
  const secret = "nested-proxy-secret";
  const transport = await createRestHttpTransport({}, {
    createDispatcher: () => fakeDispatcher(),
    httpRequest: async () => {
      const error = new Error(secret) as Error & { code: string };
      error.code = "ECONNREFUSED";
      throw error;
    },
  });
  let failure: ProcessFailure;
  try {
    failure = await captureFailure(
      () => transport.request({
        method: "GET",
        url: new URL(
          "https://tenant.zslogin.net/zpa/customers/customer-secret/resource?token=secret",
        ),
      }),
      "REST_HTTP_TRANSPORT_FAILED",
    );
  } finally {
    await transport.close?.();
  }
  assert.equal(failure.message.includes(secret), false);
  assert.equal(failure.message.includes("tenant.zslogin.net"), false);
  assert.equal(failure.message.includes("customer-secret"), false);
  assert.match(failure.message, /<vanity>\.zslogin\.net/);
  assert.match(failure.message, /customers\/<customer-id>/);
  assert.equal(failure.message.includes("?token="), false);
});

test("diagnostic probe treats every HTTP status as successful connectivity", async () => {
  let closes = 0;
  const transport = {
    async close(): Promise<void> {
      closes += 1;
    },
    async request() {
      return { body: new Uint8Array(), headers: {}, status: 403 };
    },
  };
  const result = await probeRestHost("api.example.test", { transport });
  assert.deepEqual(result, {
    detail: "HTTP 403",
    host: "api.example.test",
    ok: true,
  });
  assert.equal(closes, 0, "caller-owned transports are not closed");
});

test("diagnostic host lists are unique and deterministic", async () => {
  const visited: string[] = [];
  const transport = {
    async request(input: { readonly url: URL }) {
      visited.push(input.url.hostname);
      return { body: new Uint8Array(), headers: {}, status: 401 };
    },
  };
  const results = await probeRestHosts(
    ["z.example.test", "a.example.test", "z.example.test"],
    { transport },
  );
  assert.deepEqual(visited, ["a.example.test", "z.example.test"]);
  assert.deepEqual(results.map((item) => item.host), [
    "a.example.test",
    "z.example.test",
  ]);
});
