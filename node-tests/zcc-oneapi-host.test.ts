import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { subscribe, unsubscribe } from "node:diagnostics_channel";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer as createHttpServer } from "node:http";
import { createServer as createHttpsServer } from "node:https";
import {
  connect as connectTcp,
  createServer as createNetServer,
  type Socket,
} from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import type { Duplex } from "node:stream";
import { connect as connectTls, getCACertificates } from "node:tls";
import test from "node:test";

import {
  Client,
  EnvHttpProxyAgent,
  request as undiciRequest,
  type Dispatcher,
} from "undici";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  cleanupZccOneApiDispatcher,
  collectZccOneApiResourceWithOneApiForTest,
  createZccOneApiTransaction,
  snapshotZccOneApiProxyEnvironment,
  zccOneApiDispatcherOptions,
  type ZccOneApiHostInput,
} from "../node-src/io/zcc-oneapi-host.js";
import type {
  ZccOneApiHttpRequest,
  ZccOneApiHttpResponse,
  ZccOneApiResponseBody,
} from "../node-src/io/zcc-oneapi-transport.js";

class TestBody implements ZccOneApiResponseBody {
  destroyed = 0;

  constructor(private readonly chunks: readonly Uint8Array[]) {}

  destroy(): void {
    this.destroyed += 1;
  }

  async *[Symbol.asyncIterator](): AsyncIterator<unknown> {
    for (const chunk of this.chunks) {
      yield chunk;
    }
  }
}

function response(text: string, statusCode = 200): ZccOneApiHttpResponse {
  return {
    body: new TestBody([Buffer.from(text, "utf8")]),
    headers: {},
    statusCode,
  };
}

function successfulRequester(): ZccOneApiHttpRequest {
  return async (url) => {
    return url.endsWith("/oauth2/v1/token")
      ? response(JSON.stringify({
        access_token: "token",
        expires_in: 60,
        token_type: "Bearer",
      }))
      : response('{"id":"1"}');
  };
}

function environment(
  extra: Readonly<Record<string, string>> = {},
): Readonly<Record<string, string>> {
  return {
    ZSCALER_CLIENT_ID: "client-id",
    ZSCALER_CLIENT_SECRET: "client-secret",
    ZSCALER_VANITY_DOMAIN: "tenant",
    ...extra,
  };
}

function hostInput(
  extra: Readonly<Record<string, string>> = {},
): ZccOneApiHostInput {
  return {
    environment: environment(extra),
    resourceType: "zcc_device_cleanup",
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

function directTlsClient(port: number, certificate: Buffer): Client {
  return new Client("https://tenant.zslogin.net", {
    allowH2: false,
    connect(options, callback): void {
      const socket = connectTls({
        ALPNProtocols: ["http/1.1"],
        ca: certificate,
        host: "127.0.0.1",
        minVersion: "TLSv1.2",
        port,
        rejectUnauthorized: true,
        servername: options.servername ?? "tenant.zslogin.net",
      });
      let settled = false;
      const onError = (error: Error): void => {
        if (!settled) {
          settled = true;
          callback(error, null);
        }
      };
      socket.once("error", onError);
      socket.once("secureConnect", () => {
        if (!settled) {
          settled = true;
          socket.off("error", onError);
          callback(null, socket);
        }
      });
    },
    pipelining: 1,
  });
}

async function captureFailure(
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

async function listen(
  server:
    | ReturnType<typeof createHttpServer>
    | ReturnType<typeof createHttpsServer>
    | ReturnType<typeof createNetServer>,
): Promise<number> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  assert.notEqual(address, null);
  assert.equal(typeof address, "object");
  return typeof address === "object" && address !== null ? address.port : 0;
}

async function closeServer(
  server:
    | ReturnType<typeof createHttpServer>
    | ReturnType<typeof createHttpsServer>
    | ReturnType<typeof createNetServer>,
): Promise<void> {
  await new Promise<void>((resolve) => server.close(() => resolve()));
}

test("proxy snapshot has deterministic precedence, fallback, and explicit empties", () => {
  assert.deepEqual(snapshotZccOneApiProxyEnvironment({}), {
    httpProxy: "",
    httpsProxy: "",
    noProxy: "",
  });
  assert.deepEqual(snapshotZccOneApiProxyEnvironment({
    HTTP_PROXY: "http://upper.invalid:8080",
    http_proxy: "http://lower.invalid:8081",
    NO_PROXY: "upper.invalid",
    no_proxy: "lower.invalid",
  }), {
    httpProxy: "http://lower.invalid:8081/",
    httpsProxy: "http://lower.invalid:8081/",
    noProxy: "lower.invalid",
  });
  assert.deepEqual(snapshotZccOneApiProxyEnvironment({
    HTTP_PROXY: "http://proxy.invalid:8080",
    HTTPS_PROXY: "",
  }), {
    httpProxy: "http://proxy.invalid:8080/",
    httpsProxy: "",
    noProxy: "",
  });
});

test("proxy values and credentials never enter validation diagnostics", async () => {
  const secret = "proxy-user-secret";
  const failure = await captureFailure(
    () => Promise.resolve(snapshotZccOneApiProxyEnvironment({
      HTTPS_PROXY: `http://${secret}@proxy.invalid/path`,
    })),
    "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
  );
  assert.equal(failure.message.includes(secret), false);
  assert.equal(JSON.stringify(failure).includes(secret), false);

  const revoked = Proxy.revocable(hostInput(), {});
  revoked.revoke();
  const symbolInput = {
    ...hostInput(),
    [Symbol(secret)]: true,
  };
  for (const candidate of [revoked.proxy, symbolInput]) {
    const inputFailure = await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(candidate as never, {
        createDispatcher: () => fakeDispatcher(),
        httpRequest: successfulRequester(),
      }),
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
    );
    assert.equal(inputFailure.message.includes(secret), false);
  }

  for (const invalidEnvironment of [
    { ...environment(), NODE_TLS_REJECT_UNAUTHORIZED: "0" },
    { ...environment(), ZSCALER_CLIENT_SECRET: "" },
    {
      ZSCALER_CLIENT_ID: "client-id",
      ZSCALER_CLIENT_SECRET: secret,
    },
  ]) {
    const inputFailure = await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest({
        environment: invalidEnvironment,
        resourceType: "zcc_device_cleanup",
      }, {
        createDispatcher: () => fakeDispatcher(),
        httpRequest: successfulRequester(),
      }),
      "INVALID_ZCC_ONEAPI_HOST_OPTIONS",
    );
    assert.equal(inputFailure.message.includes(secret), false);
  }
});

test("diagnostics preflight precedes host input, CA, dispatcher, and network", {
  concurrency: false,
}, async () => {
  const secret = "hostile-ca-input-secret";
  let inputReads = 0;
  let dispatchers = 0;
  let requests = 0;
  const input = new Proxy({}, {
    getOwnPropertyDescriptor() {
      inputReads += 1;
      throw new Error(secret);
    },
    ownKeys() {
      inputReads += 1;
      throw new Error(secret);
    },
  }) as ZccOneApiHostInput;
  const listener = (): void => undefined;
  subscribe("net.client.socket", listener);
  try {
    const failure = await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(input, {
        createDispatcher: () => {
          dispatchers += 1;
          return fakeDispatcher();
        },
        httpRequest: async () => {
          requests += 1;
          return response("{}");
        },
      }),
      "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
    );
    assert.equal(failure.category, "io");
    assert.equal(failure.retryable, false);
    assert.equal(inputReads, 0);
    assert.equal(dispatchers, 0);
    assert.equal(requests, 0);
    assert.equal(JSON.stringify(failure).includes(secret), false);
  } finally {
    unsubscribe("net.client.socket", listener);
  }
});

test("subscriber planted by dispatcher keeps the diagnostics classification", {
  concurrency: false,
}, async () => {
  let closeCalls = 0;
  let requests = 0;
  const listener = (): void => undefined;
  let planted = false;
  try {
    const failure = await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(hostInput(), {
        createDispatcher: () => {
          planted = true;
          subscribe("net.client.socket", listener);
          return fakeDispatcher({
            close: async () => {
              closeCalls += 1;
            },
          });
        },
        httpRequest: async () => {
          requests += 1;
          return response("{}");
        },
      }),
      "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
    );
    assert.equal(failure.category, "io");
    assert.equal(failure.retryable, false);
    assert.equal(closeCalls, 1);
    assert.equal(requests, 0);
  } finally {
    if (planted) {
      unsubscribe("net.client.socket", listener);
    }
  }
});

test("dispatcher applies additive CA and strict TLS to every connection path", async () => {
  const ca = getCACertificates("default").slice(0, 2);
  const options = zccOneApiDispatcherOptions({
    httpProxy: "",
    httpsProxy: "",
    noProxy: "",
  }, ca);
  assert.equal(options.httpProxy, "");
  assert.equal(options.httpsProxy, "");
  assert.equal(options.noProxy, "");
  for (const tls of [options.connect, options.requestTls, options.proxyTls]) {
    assert.equal(typeof tls, "object");
    assert.deepEqual((tls as { ca?: unknown }).ca, ca);
    assert.equal((tls as { rejectUnauthorized?: unknown }).rejectUnauthorized, true);
    assert.equal((tls as { minVersion?: unknown }).minVersion, "TLSv1.2");
  }
  assert.equal(options.connections, 1);
  assert.equal(options.pipelining, 1);
  assert.equal(options.allowH2, false);
  assert.equal(options.maxOrigins, 2);
  assert.equal(options.maxResponseSize, 4 * 1024 * 1024);
  assert.equal(typeof options.clientFactory, "function");
  const proxyClient = options.clientFactory?.(
    new URL("http://127.0.0.1:8080"),
    { connect: options.connect },
  );
  assert.ok(proxyClient instanceof Client);
  await proxyClient?.destroy();
});

test("EnvHttpProxyAgent cannot re-read ambient proxy state after construction", async () => {
  let proxyRequests = 0;
  const target = createHttpServer((_request, reply) => {
    reply.end("direct");
  });
  const proxy = createHttpServer((_request, reply) => {
    proxyRequests += 1;
    reply.statusCode = 502;
    reply.end("proxied");
  });
  const targetPort = await listen(target);
  const proxyPort = await listen(proxy);
  const proxySnapshot = snapshotZccOneApiProxyEnvironment({
    HTTP_PROXY: `http://127.0.0.1:${proxyPort}`,
    NO_PROXY: "127.0.0.1",
  });
  const agent = new EnvHttpProxyAgent(
    zccOneApiDispatcherOptions(proxySnapshot, []),
  );
  const previous = {
    HTTP_PROXY: process.env.HTTP_PROXY,
    NO_PROXY: process.env.NO_PROXY,
    http_proxy: process.env.http_proxy,
    no_proxy: process.env.no_proxy,
  };
  try {
    process.env.HTTP_PROXY = "http://ambient.invalid:9999";
    process.env.http_proxy = "http://ambient-lower.invalid:9998";
    process.env.NO_PROXY = "";
    process.env.no_proxy = "";
    const result = await undiciRequest(`http://127.0.0.1:${targetPort}/`, {
      dispatcher: agent,
    });
    assert.equal(await result.body.text(), "direct");
    assert.equal(proxyRequests, 0);
  } finally {
    for (const [name, value] of Object.entries(previous)) {
      if (value === undefined) {
        delete process.env[name];
      } else {
        process.env[name] = value;
      }
    }
    await agent.destroy();
    await closeServer(target);
    await closeServer(proxy);
  }
});

test("net diagnostics block real direct and proxy OAuth before TLS succeeds", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "infrawright-oneapi-real-"));
  t.after(async () => rm(root, { force: true, recursive: true }));
  const keyPath = path.join(root, "server.key");
  const certificatePath = path.join(root, "server.pem");
  const generated = spawnSync("openssl", [
    "req",
    "-x509",
    "-newkey",
    "rsa:2048",
    "-sha256",
    "-nodes",
    "-keyout",
    keyPath,
    "-out",
    certificatePath,
    "-days",
    "1",
    "-subj",
    "/CN=tenant.zslogin.net",
    "-addext",
    "subjectAltName=DNS:tenant.zslogin.net",
  ], { encoding: "utf8" });
  assert.equal(generated.status, 0, generated.stderr);
  const [key, certificate] = await Promise.all([
    readFile(keyPath),
    readFile(certificatePath),
  ]);
  let observed: {
    readonly body: string;
    readonly contentType: string | undefined;
    readonly method: string | undefined;
    readonly url: string | undefined;
  } | null = null;
  const target = createHttpsServer({ cert: certificate, key }, (request, reply) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      observed = {
        body: Buffer.concat(chunks).toString("utf8"),
        contentType: request.headers["content-type"],
        method: request.method,
        url: request.url,
      };
      reply.statusCode = 201;
      reply.end("ignored private error body");
    });
  });
  const tunnels = new Set<Duplex>();
  const proxy = createHttpServer((_request, reply) => {
    reply.statusCode = 405;
    reply.end();
  });
  let proxyConnects = 0;
  let proxyAuthorization: string | undefined;
  const targetPort = await listen(target);
  const directInput = hostInput({ REQUESTS_CA_BUNDLE: certificatePath });
  let directDispatchers = 0;
  const directListener = (): void => undefined;
  subscribe("net.client.socket", directListener);
  try {
    await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(directInput, {
        createDispatcher: () => {
          directDispatchers += 1;
          return directTlsClient(targetPort, certificate);
        },
      }),
      "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
    );
    assert.equal(directDispatchers, 0);
    assert.equal(observed, null);
  } finally {
    unsubscribe("net.client.socket", directListener);
  }
  await captureFailure(
    () => collectZccOneApiResourceWithOneApiForTest(directInput, {
      createDispatcher: () => {
        directDispatchers += 1;
        return directTlsClient(targetPort, certificate);
      },
    }),
    "ZCC_ONEAPI_AUTH_HTTP_STATUS",
  );
  assert.equal(directDispatchers, 1);
  assert.notEqual(observed, null);
  observed = null;
  proxy.on("connect", (request, clientSocket, head) => {
    proxyConnects += 1;
    proxyAuthorization = request.headers["proxy-authorization"];
    const upstream = connectTcp(targetPort, "127.0.0.1", () => {
      clientSocket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      if (head.byteLength !== 0) {
        upstream.write(head);
      }
      clientSocket.pipe(upstream);
      upstream.pipe(clientSocket);
    });
    tunnels.add(clientSocket);
    tunnels.add(upstream);
    clientSocket.on("close", () => tunnels.delete(clientSocket));
    upstream.on("close", () => tunnels.delete(upstream));
    clientSocket.on("error", () => undefined);
    upstream.on("error", () => clientSocket.destroy());
  });
  const proxyPort = await listen(proxy);
  const proxySecret = "proxy-secret";
  const proxiedInput = hostInput({
    HTTPS_PROXY: `http://proxy-user:${proxySecret}@127.0.0.1:${proxyPort}`,
    REQUESTS_CA_BUNDLE: certificatePath,
  });
  try {
    const listener = (): void => undefined;
    subscribe("net.client.socket", listener);
    try {
      const blocked = await captureFailure(
        () => collectZccOneApiResourceWithOneApiForTest(proxiedInput, {}),
        "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
      );
      assert.equal(JSON.stringify(blocked).includes(proxySecret), false);
      assert.equal(proxyConnects, 0);
      assert.equal(observed, null);
    } finally {
      unsubscribe("net.client.socket", listener);
    }
    await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(proxiedInput, {}),
      "ZCC_ONEAPI_AUTH_HTTP_STATUS",
    );
    assert.equal(proxyConnects, 1);
    assert.equal(
      proxyAuthorization,
      `Basic ${Buffer.from(`proxy-user:${proxySecret}`).toString("base64")}`,
    );
    const captured = observed as {
      readonly body: string;
      readonly contentType: string | undefined;
      readonly method: string | undefined;
      readonly url: string | undefined;
    } | null;
    assert.notEqual(captured, null);
    if (captured === null) {
      assert.fail("local TLS server did not receive OAuth");
    }
    assert.equal(captured.method, "POST");
    assert.equal(captured.url, "/oauth2/v1/token");
    assert.equal(
      captured.contentType,
      "application/x-www-form-urlencoded",
    );
    assert.deepEqual([...new URLSearchParams(captured.body)], [
      ["grant_type", "client_credentials"],
      ["client_id", "client-id"],
      ["client_secret", "client-secret"],
      ["audience", "https://api.zscaler.com"],
    ]);
  } finally {
    for (const socket of tunnels) {
      socket.destroy();
    }
    await closeServer(proxy);
    await closeServer(target);
  }
});

test("proxy CONNECT headers are bounded by the custom client factory", async () => {
  const sockets = new Set<Socket>();
  const proxy = createNetServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    // Intentionally accept the CONNECT bytes without ever returning headers.
  });
  const port = await listen(proxy);
  const agent = new EnvHttpProxyAgent(zccOneApiDispatcherOptions({
    httpProxy: `http://127.0.0.1:${port}/`,
    httpsProxy: `http://127.0.0.1:${port}/`,
    noProxy: "",
  }, [], 25));
  const started = Date.now();
  try {
    await assert.rejects(
      undiciRequest("https://example.invalid/", {
        dispatcher: agent,
        signal: AbortSignal.timeout(2_000),
      }),
    );
    // Undici clamps this phase to approximately one second even when the
    // private test seam selects a smaller value. The two-second outer abort
    // distinguishes the custom CONNECT client from the 300-second default.
    assert.ok(Date.now() - started < 1_750);
  } finally {
    await agent.destroy();
    for (const socket of sockets) {
      socket.destroy();
    }
    await closeServer(proxy);
  }
});

test("private host loads a custom CA additively and closes once", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "infrawright-oneapi-ca-"));
  t.after(async () => rm(root, { force: true, recursive: true }));
  const custom = getCACertificates("default")[0];
  assert.notEqual(custom, undefined);
  const bundle = path.join(root, "corporate.pem");
  await writeFile(bundle, custom ?? "", "utf8");
  let closeCalls = 0;
  let destroyCalls = 0;
  let capturedCa: unknown;
  const result = await collectZccOneApiResourceWithOneApiForTest(
    hostInput({ REQUESTS_CA_BUNDLE: bundle }),
    {
      createDispatcher(options) {
        capturedCa = (options.connect as { ca?: unknown }).ca;
        return fakeDispatcher({
          close: async () => {
            closeCalls += 1;
          },
          destroy: async () => {
            destroyCalls += 1;
          },
        });
      },
      httpRequest: successfulRequester(),
    },
  );
  assert.equal(result.metadata.item_count, 1);
  assert.ok(Array.isArray(capturedCa));
  assert.ok((capturedCa as unknown[]).length > 1);
  assert.equal((capturedCa as unknown[]).includes(custom), true);
  assert.equal(closeCalls, 1);
  assert.equal(destroyCalls, 0);
});

test("invalid, oversized, and FIFO CA inputs fail statically without network", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "infrawright-oneapi-bad-ca-"));
  t.after(async () => rm(root, { force: true, recursive: true }));
  const invalid = path.join(root, "invalid.pem");
  const oversized = path.join(root, "oversized.pem");
  await writeFile(invalid, "not a certificate", "utf8");
  await writeFile(oversized, Buffer.alloc(4 * 1024 * 1024 + 1));
  let requests = 0;
  for (const bundle of [invalid, oversized]) {
    await captureFailure(
      () => collectZccOneApiResourceWithOneApiForTest(
        hostInput({ REQUESTS_CA_BUNDLE: bundle }),
        {
          createDispatcher: () => fakeDispatcher(),
          httpRequest: async () => {
            requests += 1;
            return response("{}");
          },
        },
      ),
      "ZCC_ONEAPI_CA_BUNDLE_FAILED",
    );
  }
  assert.equal(requests, 0);

  if (process.platform !== "win32") {
    const fifo = path.join(root, "bundle.fifo");
    const created = spawnSync("mkfifo", [fifo]);
    if (created.status === 0) {
      const started = Date.now();
      await captureFailure(
        () => collectZccOneApiResourceWithOneApiForTest(
          hostInput({ REQUESTS_CA_BUNDLE: fifo }),
          {
            createDispatcher: () => fakeDispatcher(),
            httpRequest: successfulRequester(),
          },
        ),
        "ZCC_ONEAPI_CA_BUNDLE_FAILED",
      );
      assert.ok(Date.now() - started < 1_000);
    }
  }
});

test("transaction deadline aborts sleeps and cleanup is independently bounded", async () => {
  const transaction = createZccOneApiTransaction(20);
  try {
    const failure = await captureFailure(
      () => transaction.sleep(1_000),
      "ZCC_ONEAPI_TRANSACTION_TIMEOUT",
    );
    assert.equal(failure.retryable, true);
    assert.equal(transaction.signal.aborted, true);
  } finally {
    transaction.finish();
  }

  let closeCalls = 0;
  let destroyCalls = 0;
  const started = Date.now();
  await captureFailure(
    () => cleanupZccOneApiDispatcher(fakeDispatcher({
      close: () => {
        closeCalls += 1;
        return new Promise<void>(() => undefined);
      },
      destroy: async () => {
        destroyCalls += 1;
      },
    }), 25),
    "ZCC_ONEAPI_CLEANUP_FAILED",
  );
  assert.ok(Date.now() - started < 500);
  assert.equal(closeCalls, 1);
  assert.equal(destroyCalls, 1);
});

test("primary collection failure wins over cleanup failure and stays secret-free", async () => {
  const secret = "nested-network-secret";
  const failure = await captureFailure(
    () => collectZccOneApiResourceWithOneApiForTest(hostInput(), {
      createDispatcher: () => fakeDispatcher({
        close: async () => {
          throw new Error(secret);
        },
        destroy: async () => undefined,
      }),
      httpRequest: async () => {
        throw new Error(secret);
      },
    }),
    "ZCC_ONEAPI_AUTH_TRANSPORT_FAILED",
  );
  assert.equal(JSON.stringify({
    details: failure.details,
    message: failure.message,
    stack: failure.stack,
  }).includes(secret), false);
});
