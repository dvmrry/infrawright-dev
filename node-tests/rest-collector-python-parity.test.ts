import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  fetchResources,
  type CollectorAdapter,
  type HttpRequest,
  type HttpResponse,
  type HttpTransport,
} from "../node-src/collectors/rest.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

class RecordingTransport implements HttpTransport {
  readonly requests: HttpRequest[] = [];

  async request(request: HttpRequest): Promise<HttpResponse> {
    this.requests.push(request);
    return { body: Buffer.from("[]", "utf8"), headers: {}, status: 200 };
  }
}

const adapter: CollectorAdapter = {
  product: "sample",
  async acquire() {
    return { headers: { Accept: "application/json" } };
  },
  composeUrl(input) {
    return new URL(input.path, "https://sample.example/api/");
  },
};

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
    const root = await loadPackRoot({ packsRoot: directory });
    const transport = new RecordingTransport();
    const result = await fetchResources({
      adapters: new Map([["sample", adapter]]),
      context: { cloud: "", customerId: "customer" },
      environment: {},
      mode: "oneapi",
      outputDirectory: path.join(directory, "pulls"),
      root,
      selectors: ["sample_resource"],
      transport,
    });
    assert.deepEqual(result.processed, ["sample_resource"]);

    // urllib.parse.urlencode bytes frozen at archive baseline 7d54261c.
    assert.equal(
      transport.requests[0]?.url.search.slice(1),
      "integer=1&decimal=1.0&exponent=1.0&negative_zero=-0.0&tiny=1e-07",
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
