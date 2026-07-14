import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
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
import { PerformanceRecorder } from "../node-src/performance/recorder.js";

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

class DelayedPathTransport implements HttpTransport {
  active = 0;
  maxActive = 0;
  readonly requests: string[] = [];

  constructor(
    private readonly responses: ReadonlyMap<string, HttpResponse>,
    private readonly delays: ReadonlyMap<string, number>,
  ) {}

  async request(request: HttpRequest): Promise<HttpResponse> {
    const pathname = request.url.pathname;
    this.requests.push(pathname);
    this.active += 1;
    this.maxActive = Math.max(this.maxActive, this.active);
    try {
      const delay = this.delays.get(pathname) ?? 0;
      if (delay > 0) {
        await new Promise<void>((resolve) => setTimeout(resolve, delay));
      } else {
        await Promise.resolve();
      }
      const value = this.responses.get(pathname);
      assert.notEqual(value, undefined, `unexpected request ${pathname}`);
      return value as HttpResponse;
    } finally {
      this.active -= 1;
    }
  }
}

async function snapshotDirectory(directory: string): Promise<Readonly<Record<string, string>>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  for (const name of (await readdir(directory)).sort()) {
    output[name] = await readFile(path.join(directory, name), "utf8");
  }
  return output;
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

test("fetch query float tokens match Python urllib encoding after registry load", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "rest-query-numbers-"));
  try {
    const packDirectory = path.join(directory, "sample");
    await mkdir(packDirectory, { recursive: true });
    await writeFile(
      path.join(packDirectory, "pack.json"),
      JSON.stringify({ provider_prefixes: { sample_: "sample" } }),
      "utf8",
    );
    const queryJson = '{"integer":1,"decimal":1.0,"exponent":1e0,"negative_zero":-0.0,"tiny":1e-7}';
    await writeFile(
      path.join(packDirectory, "registry.json"),
      `{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"items","query":${queryJson}}}}`,
      "utf8",
    );
    const isolatedRoot = await loadPackRoot({ packsRoot: directory });
    const transport = new QueueTransport([response([])]);
    const result = await fetchResources({
      adapters: new Map([["sample", adapter()]]),
      context,
      environment: {},
      mode: "oneapi",
      outputDirectory: path.join(directory, "pulls"),
      root: isolatedRoot,
      selectors: ["sample_resource"],
      transport,
    });
    assert.deepEqual(result.processed, ["sample_resource"]);

    const oracle = spawnSync(PYTHON_ORACLE, [
      "-c",
      "import json, sys, urllib.parse; print(urllib.parse.urlencode(json.loads(sys.argv[1])))",
      queryJson,
    ], { encoding: "utf8" });
    assert.equal(oracle.status, 0, oracle.stderr);
    assert.equal(
      transport.requests[0]?.url.search.slice(1),
      oracle.stdout.trim(),
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("direct collector calls reject unsafe paths before URL composition", async () => {
  for (const fetchPath of ["items\\admin", "items?scope=1", "items/../admin", "items/%2e/admin"]) {
    await assert.rejects(
      fetchResource({
        adapter: adapter(),
        auth,
        context,
        entry: entry("single", { path: fetchPath }),
        mode: "oneapi",
        resourceType: "sample",
        transport: new QueueTransport([]),
      }),
      /fetch path must not contain/,
      fetchPath,
    );
  }
  for (const unsafeEntry of [
    entry("single", { path: "items/{literal}" }),
    entry("single", {
      path: "items/{item}/{other}",
      expand: { item: ["safe"] },
    }),
  ]) {
    await assert.rejects(
      fetchResource({
        adapter: adapter(),
        auth,
        context,
        entry: unsafeEntry,
        mode: "oneapi",
        resourceType: "sample",
        transport: new QueueTransport([]),
      }),
      /undeclared expansion braces/,
    );
  }
  for (const value of [".", ".."]) {
    await assert.rejects(
      fetchResource({
        adapter: adapter(),
        auth,
        context,
        entry: entry("single", {
          path: "items/{item}",
          expand: { item: [value] },
        }),
        mode: "oneapi",
        resourceType: "sample",
        transport: new QueueTransport([]),
      }),
      /fetch expansion "item" value must not be/,
      value,
    );
  }
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

test("bounded resource workers overlap without changing bytes, outcomes, auth, or diagnostics", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "rest-concurrency-"));
  try {
    const packDirectory = path.join(directory, "packs", "sample");
    await mkdir(packDirectory, { recursive: true });
    await writeFile(
      path.join(packDirectory, "pack.json"),
      JSON.stringify({ provider_prefixes: { sample_: "sample" } }),
      "utf8",
    );
    const registry = Object.fromEntries("abcdef".split("").map((suffix) => [
      `sample_${suffix}`,
      {
        product: "sample",
        fetch: {
          pagination: "single",
          path: `items-${suffix}`,
          ...(suffix === "e" ? { optional_http_statuses: [403] } : {}),
        },
      },
    ]));
    await writeFile(
      path.join(packDirectory, "registry.json"),
      JSON.stringify(registry),
      "utf8",
    );
    const isolatedRoot = await loadPackRoot({ packsRoot: path.join(directory, "packs") });
    const output = path.join(directory, "pulls");
    await mkdir(output, { recursive: true });
    await writeFile(path.join(output, "sample_e.json"), "stale optional\n", "utf8");
    await writeFile(path.join(output, "sample_f.json"), "stale failed\n", "utf8");
    const responses = new Map<string, HttpResponse>("abcdef".split("").map((suffix, index) => [
      `/api/items-${suffix}`,
      suffix === "e"
        ? response({}, 403)
        : suffix === "f"
          ? response({}, 503)
          : response([{ id: index + 1 }, { id: `${suffix}-second` }]),
    ]));
    const selectors = ["sample"];
    let baselineResult;
    let baselineDiagnostics: string[] = [];
    let baselineFiles: Readonly<Record<string, string>> = {};

    for (const concurrency of [1, 2, 4, 8]) {
      let acquisitions = 0;
      const selectedAdapter: CollectorAdapter = {
        ...adapter("sample"),
        async acquire() {
          acquisitions += 1;
          return auth;
        },
      };
      const delays = new Map<string, number>("abcdef".split("").map((suffix, index) => [
        `/api/items-${suffix}`,
        concurrency === 1 ? 0 : (6 - index) * 3,
      ]));
      const transport = new DelayedPathTransport(responses, delays);
      const performance = new PerformanceRecorder();
      const diagnostics: string[] = [];
      const result = await fetchResources({
        adapters: new Map([["sample", selectedAdapter]]),
        concurrency,
        context,
        environment: {},
        mode: "oneapi",
        onDiagnostic: (message) => diagnostics.push(message),
        outputDirectory: output,
        performance,
        root: isolatedRoot,
        selectors,
        transport,
      });
      const files = await snapshotDirectory(output);
      assert.equal(acquisitions, 1);
      assert.ok(transport.maxActive <= Math.min(concurrency, 6));
      assert.equal(transport.requests.length, 6);
      const report = performance.report({
        command: "fetch",
        commandDurationMs: 100,
        commandStatus: "failed",
      });
      assert.equal(report.selected_concurrency, concurrency);
      assert.equal((report.summary as { logical_requests: number }).logical_requests, 6);
      assert.equal((report.summary as { pages: number }).pages, 6);
      assert.deepEqual(
        (report.spans as Array<{ phase: string; resource_family?: string }>).filter(
          (span) => span.phase === "fetch.resource",
        ).map((span) => span.resource_family),
        "abcdef".split("").map((suffix) => `sample_${suffix}`),
      );
      if (concurrency === 1) {
        assert.equal(transport.maxActive, 1);
        assert.deepEqual(transport.requests, "abcdef".split("").map((suffix) => {
          return `/api/items-${suffix}`;
        }));
        baselineResult = result;
        baselineDiagnostics = diagnostics;
        baselineFiles = files;
      } else {
        assert.ok(transport.maxActive > 1);
        assert.deepEqual(result, baselineResult);
        assert.deepEqual(diagnostics, baselineDiagnostics);
        assert.deepEqual(files, baselineFiles);
      }
    }
    assert.equal(
      baselineFiles["sample_e.json"],
      "stale optional\n",
    );
    assert.equal(
      baselineFiles["sample_f.json"],
      "stale failed\n",
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("fetch concurrency rejects invalid library values before authentication", async () => {
  const packRoot = await root();
  for (const concurrency of [0, -1, 1.5, Number.NaN, 65]) {
    let acquisitions = 0;
    await assert.rejects(fetchResources({
      adapters: new Map([["zia", {
        ...adapter("zia"),
        async acquire() {
          acquisitions += 1;
          return auth;
        },
      }]]),
      concurrency,
      context,
      environment: {},
      mode: "oneapi",
      outputDirectory: os.tmpdir(),
      root: packRoot,
      selectors: ["zia_advanced_settings"],
      transport: new QueueTransport([]),
    }), /fetch concurrency must be a positive integer/);
    assert.equal(acquisitions, 0);
  }
});

test("bounded scheduling rotates products instead of draining one product first", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "rest-product-fairness-"));
  try {
    for (const product of ["alpha", "beta"]) {
      const packDirectory = path.join(directory, "packs", product);
      await mkdir(packDirectory, { recursive: true });
      await writeFile(
        path.join(packDirectory, "pack.json"),
        JSON.stringify({ provider_prefixes: { [`${product}_`]: product } }),
        "utf8",
      );
      await writeFile(
        path.join(packDirectory, "registry.json"),
        JSON.stringify(Object.fromEntries(["a", "b"].map((suffix) => [
          `${product}_${suffix}`,
          { product, fetch: { pagination: "single", path: `${product}-${suffix}` } },
        ]))),
        "utf8",
      );
    }
    const isolatedRoot = await loadPackRoot({ packsRoot: path.join(directory, "packs") });
    const paths = ["/api/alpha-a", "/api/alpha-b", "/api/beta-a", "/api/beta-b"];
    const serial = new DelayedPathTransport(
      new Map(paths.map((pathname) => [pathname, response([])])),
      new Map(),
    );
    const adapters = new Map<string, CollectorAdapter>([
      ["alpha", adapter("alpha")],
      ["beta", adapter("beta")],
    ]);
    await fetchResources({
      adapters,
      concurrency: 1,
      context,
      environment: {},
      mode: "oneapi",
      outputDirectory: path.join(directory, "serial"),
      root: isolatedRoot,
      selectors: [],
      transport: serial,
    });
    assert.deepEqual(serial.requests, paths);
    const transport = new DelayedPathTransport(
      new Map(paths.map((pathname) => [pathname, response([])])),
      new Map(paths.map((pathname) => [pathname, 5])),
    );
    await fetchResources({
      adapters,
      concurrency: 2,
      context,
      environment: {},
      mode: "oneapi",
      outputDirectory: path.join(directory, "pulls"),
      root: isolatedRoot,
      selectors: [],
      transport,
    });
    assert.deepEqual(new Set(transport.requests.slice(0, 2)), new Set([
      "/api/alpha-a",
      "/api/beta-a",
    ]));
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
