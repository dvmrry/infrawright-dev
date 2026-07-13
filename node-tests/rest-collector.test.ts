import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  failureHints,
  fetchResource,
  fetchResources,
  retryDelayMs,
  selectFetchResources,
  type CollectorAdapter,
  type FetchEntry,
  type HttpRequest,
  type HttpResponse,
  type HttpTransport,
} from "../node-src/collectors/rest.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

function response(value: unknown, status = 200): HttpResponse {
  return {
    status,
    headers: {},
    body: Buffer.from(JSON.stringify(value), "utf8"),
  };
}

class QueueTransport implements HttpTransport {
  readonly requests: HttpRequest[] = [];

  constructor(private readonly responses: HttpResponse[]) {}

  async request(request: HttpRequest): Promise<HttpResponse> {
    this.requests.push(request);
    const next = this.responses.shift();
    assert.notEqual(next, undefined, `unexpected request ${request.url}`);
    return next as HttpResponse;
  }
}

function adapter(product = "sample", acquisitions?: string[]): CollectorAdapter {
  return {
    product,
    async acquire() {
      acquisitions?.push(product);
      return { headers: { Accept: "application/json", Authorization: "Bearer shared" } };
    },
    composeUrl(input) {
      return new URL(input.path, `https://${product}.example/api/`);
    },
  };
}

function entry(
  pagination: FetchEntry["pagination"],
  extras: Partial<FetchEntry> = {},
): FetchEntry {
  return {
    product: "sample",
    path: "items",
    pagination,
    query: {},
    optionalHttpStatuses: new Set(),
    ...extras,
  };
}

const context = { cloud: "", customerId: "customer" };
const auth = { headers: { Accept: "application/json" } };

test("all four pagination styles preserve their registry-owned response shapes", async () => {
  const ziaFirst = Array.from({ length: 1_000 }, (_unused, id) => ({ id }));
  const zia = new QueueTransport([
    response({ values: ziaFirst }),
    response({ values: [{ id: 1_000 }] }),
  ]);
  const ziaItems = await fetchResource({
    adapter: adapter(),
    auth,
    context,
    entry: entry("zia", { envelope: "values", query: { customOnly: "true" } }),
    mode: "oneapi",
    resourceType: "sample",
    transport: zia,
  });
  assert.equal(ziaItems.length, 1_001);
  assert.deepEqual(
    zia.requests.map((request) => request.url.search),
    ["?customOnly=true&page=1&pageSize=1000", "?customOnly=true&page=2&pageSize=1000"],
  );

  const zpa = new QueueTransport([
    response({ list: [{ id: "1" }], totalPages: 2 }),
    response({ list: [{ id: "2" }], totalPages: 2 }),
  ]);
  assert.deepEqual(await fetchResource({
    adapter: adapter(),
    auth,
    context,
    entry: entry("zpa"),
    mode: "oneapi",
    resourceType: "sample",
    transport: zpa,
  }), [{ id: "1" }, { id: "2" }]);
  assert.deepEqual(
    zpa.requests.map((request) => request.url.search),
    ["?page=1&pagesize=500", "?page=2&pagesize=500"],
  );

  const single = new QueueTransport([response({ id: "1" })]);
  assert.deepEqual(await fetchResource({
    adapter: adapter(),
    auth,
    context,
    entry: entry("single"),
    mode: "oneapi",
    resourceType: "sample",
    transport: single,
  }), [{ id: "1" }]);

  const v2 = new QueueTransport([
    response({ items: [{ id: "1" }], count: 100, limit: 100, total: 2 }),
    response({ items: [{ id: "2" }], count: 1, limit: 100, total: 2 }),
  ]);
  assert.deepEqual(await fetchResource({
    adapter: adapter(),
    auth,
    context,
    entry: entry("zcc_v2"),
    mode: "oneapi",
    resourceType: "sample",
    transport: v2,
  }), [{ id: "1" }, { id: "2" }]);
  assert.deepEqual(
    v2.requests.map((request) => request.url.search),
    ["?skip=0&perPage=100", "?skip=100&perPage=100"],
  );
});

test("expanded paths are percent-quoted and concatenated in registry order", async () => {
  const transport = new QueueTransport([
    response([{ id: "1" }]),
    response([{ id: "2" }]),
  ]);
  const items = await fetchResource({
    adapter: adapter(),
    auth,
    context,
    entry: entry("single", {
      path: "rules/{kind}/again/{kind}",
      expand: { kind: ["A B", "slash/value"] },
    }),
    mode: "oneapi",
    resourceType: "sample",
    transport,
  });
  assert.deepEqual(items, [{ id: "1" }, { id: "2" }]);
  assert.deepEqual(
    transport.requests.map((request) => request.url.pathname),
    [
      "/api/rules/A%20B/again/A%20B",
      "/api/rules/slash%2Fvalue/again/slash%2Fvalue",
    ],
  );
});

test("configured envelopes fail closed when missing or non-list", async () => {
  for (const [payload, message] of [
    [{ other: [] }, /missing envelope/],
    [{ values: {} }, /did not contain a list/],
  ] as const) {
    await assert.rejects(
      fetchResource({
        adapter: adapter(),
        auth,
        context,
        entry: entry("zia", { envelope: "values" }),
        mode: "oneapi",
        resourceType: "sample",
        transport: new QueueTransport([response(payload)]),
      }),
      message,
    );
  }
});

let loadedRoot: Promise<LoadedPackRoot> | undefined;

function root(): Promise<LoadedPackRoot> {
  loadedRoot ??= loadPackRoot({
    packsRoot: path.join(process.cwd(), "packs"),
    profilePath: path.join(process.cwd(), "packsets", "full.json"),
    catalogPath: path.join(process.cwd(), "packsets", "full.json"),
  });
  return loadedRoot;
}

test("selectors use original active registry metadata and derived resources fetch their source", async () => {
  const packRoot = await root();
  const all = selectFetchResources({ root: packRoot, selectors: [] });
  assert.equal(all.length, 92);
  assert.deepEqual(
    Object.fromEntries(["zia", "zpa", "zcc", "ztc"].map((product) => {
      return [product, selectFetchResources({ root: packRoot, selectors: [product] }).length];
    })),
    { zia: 56, zpa: 16, zcc: 5, ztc: 15 },
  );
  assert.deepEqual(
    selectFetchResources({
      root: packRoot,
      selectors: ["zpa_policy_access_rule_reorder"],
    }),
    ["zpa_policy_access_rule"],
  );
  const zcc = selectFetchResources({ root: packRoot, selectors: ["zcc"] });
  assert.deepEqual(zcc, [
    "zcc_device_cleanup",
    "zcc_failopen_policy",
    "zcc_forwarding_profile",
    "zcc_trusted_network",
    "zcc_web_privacy",
  ]);
  assert.throws(
    () => selectFetchResources({ root: packRoot, selectors: ["unknown"] }),
    /valid products: zcc, zia, zpa, ztc/,
  );
});

test("batch shares OneAPI auth, skips optional statuses, writes Python bytes, and preserves stale skips", async () => {
  const packRoot = await root();
  const directory = await mkdtemp(path.join(os.tmpdir(), "rest-collector-"));
  try {
    const stale = path.join(directory, "zia_extranet.json");
    await writeFile(stale, "stale\n", "utf8");
    const acquisitions: string[] = [];
    const adapters = new Map<string, CollectorAdapter>([
      ["zia", adapter("zia", acquisitions)],
      ["zpa", adapter("zpa", acquisitions)],
    ]);
    const transport = new QueueTransport([
      response({}, 403),
      response({ list: [{ id: 9007199254740992 }], totalPages: 1 }),
    ]);
    const diagnostics: string[] = [];
    const result = await fetchResources({
      adapters,
      context,
      environment: {},
      mode: "oneapi",
      onDiagnostic: (message) => diagnostics.push(message),
      outputDirectory: directory,
      root: packRoot,
      selectors: ["zia_extranet", "zpa_segment_group"],
      transport,
    });
    assert.deepEqual(acquisitions, ["zia"]);
    assert.deepEqual(result.processed, ["zpa_segment_group"]);
    assert.deepEqual(Object.keys(result.failed), []);
    assert.deepEqual(Object.keys(result.skipped), ["zia_extranet"]);
    assert.equal(await readFile(stale, "utf8"), "stale\n");
    assert.equal(
      await readFile(path.join(directory, "zpa_segment_group.json"), "utf8"),
      '[\n  {\n    "id": 9007199254740992\n  }\n]\n',
    );
    assert.ok(diagnostics.some((message) => message.includes("1 resource(s) SKIPPED")));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("shared OneAPI authentication failure is isolated into every selected product result", async () => {
  const packRoot = await root();
  const directory = await mkdtemp(path.join(os.tmpdir(), "rest-auth-"));
  try {
    let zpaAcquires = 0;
    const rejecting: CollectorAdapter = {
      ...adapter("zia"),
      async acquire() {
        throw new Error("token request failed: HTTP 401");
      },
    };
    const zpa: CollectorAdapter = {
      ...adapter("zpa"),
      async acquire() {
        zpaAcquires += 1;
        return auth;
      },
    };
    const result = await fetchResources({
      adapters: new Map([["zia", rejecting], ["zpa", zpa]]),
      context,
      environment: {},
      mode: "oneapi",
      outputDirectory: directory,
      root: packRoot,
      selectors: ["zia_advanced_settings", "zpa_segment_group"],
      transport: new QueueTransport([]),
    });
    assert.equal(zpaAcquires, 0);
    assert.deepEqual(Object.keys(result.failed), [
      "zia_advanced_settings",
      "zpa_segment_group",
    ]);
    assert.match(result.failed.zpa_segment_group ?? "", /^auth failed:/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("retry schedule and failure hints retain Python collector behavior", () => {
  assert.deepEqual(
    [0, 1, 2, 3, 4].map((attempt) => retryDelayMs(attempt, null)),
    [1_000, 2_000, 4_000, 8_000, 16_000],
  );
  assert.equal(retryDelayMs(0, "0.25"), 250);
  assert.equal(retryDelayMs(0, "999"), 30_000);
  assert.equal(retryDelayMs(0, ""), 1_000);
  assert.equal(retryDelayMs(0, "0x10"), 1_000);
  assert.equal(retryDelayMs(0, "NaN"), 0);
  assert.equal(retryDelayMs(0, "Infinity"), 30_000);
  assert.equal(retryDelayMs(0, "-Infinity"), 0);
  assert.equal(retryDelayMs(0, "NaN"), 0);
  const hints = failureHints(["GET endpoint returned HTTP 404"], true);
  assert.ok(hints.some((hint) => hint.includes("ONE endpoint")));
  assert.ok(hints.some((hint) => hint.includes("only= scoped")));
  assert.equal(hints.at(-1), "Successful pulls above are unaffected either way.");
});
