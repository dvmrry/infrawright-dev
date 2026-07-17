import assert from "node:assert/strict";
import { cp, mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { resolveCollectorAdapters } from "../node-src/collectors/authority.js";
import { fetchResources } from "../node-src/collectors/rest.js";
import type {
  HttpRequest,
  HttpResponse,
  HttpTransport,
} from "../node-src/collectors/types.js";
import {
  collectorContext,
  createZscalerCollectorAdaptersByProviderSource,
} from "../node-src/collectors/zscaler-adapters.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();

function jsonResponse(value: unknown): HttpResponse {
  return {
    body: Buffer.from(JSON.stringify(value), "utf8"),
    headers: {},
    status: 200,
  };
}

class ZscalerTransport implements HttpTransport {
  readonly requests: HttpRequest[] = [];

  async request(request: HttpRequest): Promise<HttpResponse> {
    this.requests.push(request);
    if (request.method === "POST" && request.url.pathname === "/oauth2/v1/token") {
      return jsonResponse({ access_token: "fixture-token" });
    }
    if (
      request.method === "GET"
      && request.url.pathname === "/zcc/papi/public/v1/webTrustedNetwork/listByCompany"
    ) {
      return jsonResponse({
        trustedNetworkContracts: [{ id: "zcc-1", name: "Trusted Network" }],
      });
    }
    if (request.method === "GET" && request.url.pathname === "/ztw/api/v1/networkServices") {
      return jsonResponse([{ id: "ztc-1", name: "HTTPS", ports: [443] }]);
    }
    throw new Error(`unexpected request ${request.method} ${request.url.pathname}`);
  }
}

async function pythonArtifacts(directory: string): Promise<string[]> {
  const output: string[] = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const pathname = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "__pycache__") output.push(pathname);
      else output.push(...await pythonArtifacts(pathname));
    } else if (entry.name.endsWith(".py") || entry.name.endsWith(".pyc")) {
      output.push(pathname);
    }
  }
  return output;
}

test("generic Fetch collects real ZCC and ZTC registries from a Python-free external root", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-zscaler-fetch-"));
  const packsRoot = path.join(directory, "packs");
  try {
    await Promise.all([
      cp(path.join(ROOT, "packs", "zcc"), path.join(packsRoot, "zcc"), {
        recursive: true,
      }),
      cp(path.join(ROOT, "packs", "ztc"), path.join(packsRoot, "ztc"), {
        recursive: true,
      }),
      cp(
        path.join(ROOT, "packs", "_shared", "zscaler"),
        path.join(packsRoot, "_shared", "zscaler"),
        {
          recursive: true,
        },
      ),
    ]);
    assert.deepEqual(await pythonArtifacts(packsRoot), []);
    const root = await loadPackRoot({ packsRoot });
    const adapters = resolveCollectorAdapters({
      authorities: {
        byProviderSource: createZscalerCollectorAdaptersByProviderSource(),
      },
      resourceTypes: ["zcc_trusted_network", "ztc_network_services"],
      root,
    });
    const products = new Set(adapters.keys());
    const environment = {
      ZSCALER_CLIENT_ID: "fixture-client",
      ZSCALER_CLIENT_SECRET: "fixture-secret",
      ZSCALER_VANITY_DOMAIN: "fixture",
    };
    const transport = new ZscalerTransport();
    const outputDirectory = path.join(directory, "pulls");
    const result = await fetchResources({
      adapters,
      concurrency: 2,
      context: collectorContext({ environment, neededProducts: products }),
      environment,
      mode: "oneapi",
      outputDirectory,
      root,
      selectors: ["zcc_trusted_network", "ztc_network_services"],
      transport,
    });
    assert.deepEqual({ ...result.failed }, {});
    assert.deepEqual(result.processed, ["zcc_trusted_network", "ztc_network_services"]);
    assert.deepEqual({ ...result.skipped }, {});
    assert.equal(
      transport.requests.filter((request) => request.method === "POST").length,
      1,
      "OneAPI authentication must be shared across selected products",
    );
    assert.deepEqual(
      transport.requests.filter((request) => request.method === "GET").map((request) => {
        return `${request.url.pathname}${request.url.search}`;
      }).sort(),
      [
        "/zcc/papi/public/v1/webTrustedNetwork/listByCompany?page=1&pageSize=1000",
        "/ztw/api/v1/networkServices?page=1&pageSize=1000",
      ],
    );
    assert.equal(
      await readFile(path.join(outputDirectory, "zcc_trusted_network.json"), "utf8"),
      '[\n  {\n    "id": "zcc-1",\n    "name": "Trusted Network"\n  }\n]\n',
    );
    assert.equal(
      await readFile(path.join(outputDirectory, "ztc_network_services.json"), "utf8"),
      '[\n  {\n    "id": "ztc-1",\n    "name": "HTTPS",\n    "ports": [\n      443\n    ]\n  }\n]\n',
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
