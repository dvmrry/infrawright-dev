import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  linkSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullCollectionParity,
} from "../node-src/contracts/validators.js";
import {
  zccPullCollectionParityOperationResultErrors,
} from "../node-src/contracts/zcc-pull-collection-parity-semantics.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
  ZCC_COLLECTION_RESOURCE_TYPES,
  type ZccCollectionResourceType,
} from "../node-src/domain/zcc-collection-contract.js";
import {
  compareZccPullCollectionOperation,
  type CompareZccPullCollectionOptions,
  type ZccPullCollectionParity,
} from "../node-src/domain/zcc-pull-collection-parity.js";
import type { ZccPullCollectionReceipt } from "../node-src/domain/zcc-pull-collection.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";

const REPOSITORY = process.cwd();
const PROCESS_MAIN = path.join(REPOSITORY, ".node-test/node-src/process/main.js");
const TENANT = "collection_parity";

type ItemSet = Readonly<Record<ZccCollectionResourceType, readonly unknown[]>>;

interface Fixture {
  readonly root: string;
  readonly before: string;
  readonly node: string;
  readonly after: string;
}

function itemSet(tag = "stable"): ItemSet {
  return Object.fromEntries(ZCC_COLLECTION_RESOURCE_TYPES.map((resourceType, index) => [
    resourceType,
    [{ enabled: index % 2 === 0, id: `${tag}-${index}`, resource: resourceType }],
  ])) as unknown as ItemSet;
}

function emptyItemSet(): ItemSet {
  return Object.fromEntries(ZCC_COLLECTION_RESOURCE_TYPES.map((resourceType) => [
    resourceType,
    [],
  ])) as unknown as ItemSet;
}

function createFixture(): Fixture {
  const root = realpathSync(mkdtempSync(path.join(os.tmpdir(), "zcc-collection-parity-")));
  const before = path.join(root, "python-before");
  const node = path.join(root, "node");
  const after = path.join(root, "python-after");
  for (const workspace of [before, node, after]) mkdirSync(workspace);
  return { root, before, node, after };
}

function writeCollection(workspace: string, items: ItemSet): void {
  const directory = path.join(workspace, "pulls", TENANT);
  mkdirSync(directory, { recursive: true });
  for (const resourceType of ZCC_COLLECTION_RESOURCE_TYPES) {
    writeFileSync(
      path.join(directory, `${resourceType}.json`),
      renderPythonLosslessArtifactJson(items[resourceType]),
    );
  }
}

function writeWithActualPythonCollector(workspace: string, items: ItemSet): void {
  const script = String.raw`
import json
import os
import sys
from engine.collectors import rest
from engine import packs

resources = json.loads(os.environ["RESOURCES"])
items = json.loads(os.environ["ITEMS"])
rest.products_in_manifest = lambda: ["zcc"]
rest.load_manifest = lambda: resources
rest.manifest_entry = lambda resource_type: {"product": "zcc"}
class Collector(object):
    def acquire(self, auth_mode, env, ctx, opener):
        return "token"
packs.collector_for = lambda product: Collector()
rest.fetch_resource = lambda resource_type, auth_mode, ctx, token, opener: items[resource_type]
sys.exit(rest.fetch_all(
    "oneapi", {}, {}, object(), os.environ["OUT_DIR"], only=set(resources)))
`;
  const result = spawnSync("python3", ["-c", script], {
    cwd: REPOSITORY,
    env: {
      ...process.env,
      RESOURCES: JSON.stringify(ZCC_COLLECTION_RESOURCE_TYPES),
      ITEMS: JSON.stringify(items),
      OUT_DIR: path.join(workspace, "pulls", TENANT),
    },
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
}

function digest(file: string): { sha256: string; size_bytes: number; item_count: number } {
  const bytes = readFileSync(file);
  const parsed = JSON.parse(bytes.toString("utf8")) as readonly unknown[];
  return {
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.byteLength,
    item_count: parsed.length,
  };
}

function receipts(nodeWorkspace: string, tenant = TENANT): ZccPullCollectionReceipt[] {
  return ZCC_COLLECTION_RESOURCE_TYPES.map((resourceType) => {
    const tuple = digest(path.join(
      nodeWorkspace,
      "pulls",
      tenant,
      `${resourceType}.json`,
    ));
    return {
      kind: "infrawright.zcc_pull_collection",
      schema_version: 1,
      mode: "oneapi",
      product: "zcc",
      tenant,
      resource_type: resourceType,
      status: "complete",
      catalog_sources_sha256: ZCC_COLLECTION_CATALOG_SOURCES_SHA256,
      artifact: {
        path: `pulls/${tenant}/${resourceType}.json`,
        media_type: "application/json",
        encoding: "utf-8",
        ...tuple,
      },
      publication: {
        policy: "replace_or_verify_exact",
        action: "created",
      },
    };
  });
}

function options(
  fixture: Fixture,
  receiptSet = receipts(fixture.node),
): CompareZccPullCollectionOptions {
  return {
    context: {
      node_workspace: fixture.node,
      python_before_workspace: fixture.before,
      python_after_workspace: fixture.after,
    },
    reference: "python_stability_window",
    tenant: TENANT,
    receipts: receiptSet,
  };
}

async function withFixture(
  callback: (fixture: Fixture) => void | Promise<void>,
  items: ItemSet = itemSet(),
): Promise<void> {
  const fixture = createFixture();
  try {
    writeCollection(fixture.before, items);
    writeCollection(fixture.node, items);
    writeCollection(fixture.after, items);
    await callback(fixture);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
}

function isFailure(code: string): (error: unknown) => boolean {
  return (error) => error instanceof ProcessFailure && error.code === code;
}

function processRequest(fixture: Fixture): Record<string, unknown> {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "collection-parity",
    operation: "compare_zcc_pull_collection",
    context: {
      node_workspace: fixture.node,
      python_before_workspace: fixture.before,
      python_after_workspace: fixture.after,
    },
    input: {
      reference: "python_stability_window",
      tenant: TENANT,
      receipts: receipts(fixture.node),
    },
  };
}

function invoke(request: unknown) {
  return spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: REPOSITORY,
    input: `${JSON.stringify(request)}\n`,
    encoding: "utf8",
  });
}

test("exact-five Python writer window compares equal and binds the frozen catalog", async () => {
  const fixture = createFixture();
  try {
    const items = itemSet("python-writer");
    writeWithActualPythonCollector(fixture.before, items);
    writeCollection(fixture.node, items);
    writeWithActualPythonCollector(fixture.after, items);
    const result = await compareZccPullCollectionOperation(options(fixture));
    assert.equal(validateZccPullCollectionParity(result), true);
    assert.equal(result.catalog_sources_sha256, ZCC_COLLECTION_CATALOG_SOURCES_SHA256);
    assert.equal(result.status, "equal");
    assert.deepEqual(result.counts, {
      total: 5,
      equal: 5,
      different: 0,
      unstable_reference: 0,
    });
    assert.deepEqual(
      result.resources.map((resource) => resource.resource_type),
      ZCC_COLLECTION_RESOURCE_TYPES,
    );
    assert.equal(JSON.stringify(result).includes(fixture.root), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("stable Node mismatch is different while Python instability always wins", async () => {
  await withFixture(async (fixture) => {
    const target = path.join(
      fixture.node,
      "pulls",
      TENANT,
      "zcc_forwarding_profile.json",
    );
    writeFileSync(target, renderPythonLosslessArtifactJson([{ id: "node-different" }]));
    const different = await compareZccPullCollectionOperation(options(fixture));
    assert.equal(different.status, "different");
    assert.equal(different.counts.different, 1);
    assert.equal(different.resources[2]?.status, "different");

    writeFileSync(
      path.join(
        fixture.after,
        "pulls",
        TENANT,
        "zcc_forwarding_profile.json",
      ),
      readFileSync(target),
    );
    const unstable = await compareZccPullCollectionOperation(options(fixture));
    assert.equal(unstable.status, "unstable_reference");
    assert.equal(unstable.counts.unstable_reference, 1);
    assert.equal(unstable.counts.different, 0);
    assert.equal(unstable.resources[2]?.status, "unstable_reference");
  });
});

test("empty exact-five arrays are equal without a coverage or qualification claim", async () => {
  await withFixture(async (fixture) => {
    const result = await compareZccPullCollectionOperation(options(fixture));
    assert.equal(result.status, "equal");
    assert.equal(result.resources.every((resource) => resource.node.item_count === 0), true);
    assert.equal(Object.hasOwn(result, "qualification"), false);
    assert.equal(Object.hasOwn(result, "coverage"), false);
  }, emptyItemSet());
});

test("malformed receipt sets fail before any workspace I/O", async () => {
  await withFixture(async (fixture) => {
    const valid = receipts(fixture.node);
    const missingWorkspace = path.join(fixture.root, "does-not-exist");
    const base = {
      ...options(fixture, valid),
      context: {
        node_workspace: missingWorkspace,
        python_before_workspace: missingWorkspace,
        python_after_workspace: missingWorkspace,
      },
    };
    const mutations: unknown[] = [
      valid.slice(0, 4),
      [valid[1], valid[0], ...valid.slice(2)],
      [valid[0], valid[0], ...valid.slice(2)],
      valid.map((receipt, index) => index === 2
        ? { ...receipt, tenant: "foreign" }
        : receipt),
      valid.map((receipt, index) => index === 3
        ? { ...receipt, catalog_sources_sha256: "0".repeat(64) }
        : receipt),
    ];
    for (const mutation of mutations) {
      await assert.rejects(
        compareZccPullCollectionOperation({
          ...base,
          receipts: mutation,
        } as unknown as CompareZccPullCollectionOptions),
        (error: unknown) => error instanceof ProcessFailure
          && error.category === "request"
          && error.code !== "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION",
      );
    }
  });
});

test("receipt tuple mismatch fails closed after reading the Node artifact", async () => {
  await withFixture(async (fixture) => {
    const forged = structuredClone(receipts(fixture.node));
    const receipt = forged[0];
    assert.ok(receipt);
    (receipt.artifact as { sha256: string }).sha256 = "0".repeat(64);
    await assert.rejects(
      compareZccPullCollectionOperation(options(fixture, forged)),
      isFailure("ZCC_PULL_COLLECTION_RECEIPT_MISMATCH"),
    );
  });
});

test("artifact parser and byte budgets reject noncanonical, nonlist, duplicate, UTF-8, size, and item overflow", async () => {
  const cases: readonly { readonly name: string; readonly content: string | Buffer }[] = [
    { name: "noncanonical", content: '[{"b": 1, "a": 2}]\n' },
    { name: "nonlist", content: '{}\n' },
    { name: "duplicate", content: '[{"a": 1, "a": 2}]\n' },
    { name: "utf8", content: Buffer.from([0xff, 0xfe, 0xfd]) },
    { name: "size", content: Buffer.alloc((4 * 1024 * 1024) + 1, 0x20) },
    { name: "items", content: JSON.stringify(new Array(50_001).fill(0)) },
  ];
  for (const variant of cases) {
    await withFixture(async (fixture) => {
      writeFileSync(
        path.join(
          fixture.before,
          "pulls",
          TENANT,
          "zcc_device_cleanup.json",
        ),
        variant.content,
      );
      await assert.rejects(
        compareZccPullCollectionOperation(options(fixture)),
        (error: unknown) => error instanceof ProcessFailure
          && (error.category === "domain" || error.category === "io"),
        variant.name,
      );
    });
  }
});

test("same, nested, symlink, and root workspaces are rejected before artifact reads", async () => {
  await withFixture(async (fixture) => {
    const nested = path.join(fixture.before, "nested");
    mkdirSync(nested);
    const alias = path.join(fixture.root, "alias");
    symlinkSync(fixture.before, alias);
    const contexts = [
      {
        node_workspace: fixture.before,
        python_before_workspace: fixture.before,
        python_after_workspace: fixture.after,
      },
      {
        node_workspace: fixture.node,
        python_before_workspace: fixture.before,
        python_after_workspace: nested,
      },
      {
        node_workspace: fixture.node,
        python_before_workspace: alias,
        python_after_workspace: fixture.after,
      },
      {
        node_workspace: fixture.node,
        python_before_workspace: path.parse(fixture.root).root,
        python_after_workspace: fixture.after,
      },
    ];
    for (const context of contexts) {
      await assert.rejects(
        compareZccPullCollectionOperation({ ...options(fixture), context }),
        isFailure("INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION"),
      );
    }
  });
});

test("artifact symlinks are rejected even when their bytes are canonical", async () => {
  await withFixture(async (fixture) => {
    const target = path.join(
      fixture.before,
      "pulls",
      TENANT,
      "zcc_trusted_network.json",
    );
    rmSync(target);
    symlinkSync(
      path.join(
        fixture.node,
        "pulls",
        TENANT,
        "zcc_trusted_network.json",
      ),
      target,
    );
    await assert.rejects(
      compareZccPullCollectionOperation(options(fixture)),
      isFailure("INVALID_ZCC_PULL_COLLECTION_PARITY_ARTIFACT"),
    );
  });
});

test("artifact hardlinks cannot collapse independent comparison roles", async () => {
  await withFixture(async (fixture) => {
    const source = path.join(
      fixture.before,
      "pulls",
      TENANT,
      "zcc_trusted_network.json",
    );
    const target = path.join(
      fixture.after,
      "pulls",
      TENANT,
      "zcc_trusted_network.json",
    );
    rmSync(target);
    linkSync(source, target);
    await assert.rejects(
      compareZccPullCollectionOperation(options(fixture)),
      isFailure("INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION"),
    );
  });
});

test("file replacement and workspace rollover between phases fail the final CAS", async () => {
  await withFixture(async (fixture) => {
    const target = path.join(
      fixture.node,
      "pulls",
      TENANT,
      "zcc_web_privacy.json",
    );
    await assert.rejects(
      compareZccPullCollectionOperation({
        ...options(fixture),
        hooks: {
          afterInputsBound: () => {
            renameSync(target, `${target}.old`);
            writeFileSync(target, renderPythonLosslessArtifactJson([{ id: "mutated" }]));
          },
        },
      }),
      isFailure("ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED"),
    );
  });

  await withFixture(async (fixture) => {
    const moved = `${fixture.before}-moved`;
    await assert.rejects(
      compareZccPullCollectionOperation({
        ...options(fixture),
        hooks: {
          afterInputsBound: () => {
            renameSync(fixture.before, moved);
            mkdirSync(fixture.before);
          },
        },
      }),
      isFailure("ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED"),
    );
  });
});

test("move-away and restore of every directory level changes the bound input", async () => {
  const targets = [
    (fixture: Fixture): string => fixture.before,
    (fixture: Fixture): string => path.join(fixture.before, "pulls"),
    (fixture: Fixture): string => path.join(fixture.before, "pulls", TENANT),
  ];
  for (const selectTarget of targets) {
    await withFixture(async (fixture) => {
      const target = selectTarget(fixture);
      const moved = `${target}-moved`;
      await assert.rejects(
        compareZccPullCollectionOperation({
          ...options(fixture),
          hooks: {
            afterInputsBound: () => {
              renameSync(target, moved);
              renameSync(moved, target);
            },
          },
        }),
        isFailure("ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED"),
      );
    });
  }
});

test("the final synchronous CAS catches mutation after an early artifact recheck", async () => {
  const largeItems = {
    ...itemSet(),
    zcc_failopen_policy: [
      { padding: "x".repeat(900 * 1024) },
      { padding: "y".repeat(900 * 1024) },
      { padding: "z".repeat(900 * 1024) },
    ],
  } as ItemSet;
  await withFixture(async (fixture) => {
    const firstArtifact = path.join(
      fixture.before,
      "pulls",
      TENANT,
      "zcc_device_cleanup.json",
    );
    let mutationRan = false;
    await assert.rejects(
      compareZccPullCollectionOperation({
        ...options(fixture),
        hooks: {
          afterArtifactRechecked: (index) => {
            if (index === 0) {
              setImmediate(() => {
                writeFileSync(
                  firstArtifact,
                  renderPythonLosslessArtifactJson([{ id: "late-mutation" }]),
                );
                mutationRan = true;
              });
            }
          },
        },
      }),
      isFailure("ZCC_PULL_COLLECTION_PARITY_INPUT_CHANGED"),
    );
    assert.equal(mutationRan, true);
  }, largeItems);
});

test("standalone and process schemas reject semantic tampering and close extra fields", async () => {
  await withFixture(async (fixture) => {
    const result = await compareZccPullCollectionOperation(options(fixture));
    const statusTamper = structuredClone(result) as ZccPullCollectionParity;
    (statusTamper.resources[0] as { status: string }).status = "different";
    assert.equal(validateZccPullCollectionParity(statusTamper), false);
    const catalogTamper = structuredClone(result) as ZccPullCollectionParity;
    (catalogTamper as { catalog_sources_sha256: string }).catalog_sources_sha256 =
      "0".repeat(64);
    assert.equal(validateZccPullCollectionParity(catalogTamper), false);

    const request = processRequest(fixture);
    assert.equal(validateProcessRequest(request), true);
    assert.equal(validateProcessRequest({
      ...request,
      operation: "roots",
      input: { tenant: null, selectors: [] },
    }), false);
    const response = {
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: "collection-parity",
      operation: "compare_zcc_pull_collection",
      status: "ok",
      diagnostics: [],
      result,
      error: null,
    };
    assert.equal(validateProcessResponse(response), true);
    const joined = structuredClone(result) as ZccPullCollectionParity;
    (joined.resources[0]!.node as { sha256: string }).sha256 = "0".repeat(64);
    assert.notEqual(
      zccPullCollectionParityOperationResultErrors(
        request as unknown as Parameters<
          typeof zccPullCollectionParityOperationResultErrors
        >[0],
        joined,
      ).length,
      0,
    );
    assert.equal(validateProcessRequest({ ...request, evidence_class: "live" }), false);
    const requestTamper = structuredClone(request) as {
      input: { receipts: ZccPullCollectionReceipt[] };
    };
    requestTamper.input.receipts.reverse();
    assert.equal(validateProcessRequest(requestTamper), false);
  });
});

test("direct library input rejects proxy, accessor, and extra-key laundering", async () => {
  await withFixture(async (fixture) => {
    const valid = options(fixture);
    await assert.rejects(
      compareZccPullCollectionOperation(new Proxy(valid, {}) as CompareZccPullCollectionOptions),
      (error: unknown) => error instanceof ProcessFailure && error.category === "request",
    );
    const accessor = { ...valid } as Record<string, unknown>;
    Object.defineProperty(accessor, "tenant", {
      enumerable: true,
      get: () => TENANT,
    });
    await assert.rejects(
      compareZccPullCollectionOperation(accessor as unknown as CompareZccPullCollectionOptions),
      (error: unknown) => error instanceof ProcessFailure && error.category === "request",
    );
    await assert.rejects(
      compareZccPullCollectionOperation({
        ...valid,
        retained_status: "equal",
      } as unknown as CompareZccPullCollectionOptions),
      (error: unknown) => error instanceof ProcessFailure && error.category === "request",
    );
    const receiptExtra = structuredClone(valid.receipts) as unknown as Record<string, unknown>[];
    receiptExtra[0]!.caller_hash = "0".repeat(64);
    await assert.rejects(
      compareZccPullCollectionOperation({
        ...valid,
        receipts: receiptExtra as unknown as ZccPullCollectionReceipt[],
      }),
      (error: unknown) => error instanceof ProcessFailure && error.category === "request",
    );
  });
});

test("results and failures contain no workspace, body, or secret sentinels", async () => {
  await withFixture(async (fixture) => {
    const result = await compareZccPullCollectionOperation(options(fixture));
    const serialized = JSON.stringify(result);
    for (const workspace of [fixture.root, fixture.before, fixture.node, fixture.after]) {
      assert.equal(serialized.includes(workspace), false);
    }

    const secret = "COLLECTION-PARITY-SECRET-SENTINEL";
    writeFileSync(
      path.join(fixture.before, "pulls", TENANT, "zcc_web_privacy.json"),
      `{"secret":"${secret}"}\n`,
    );
    let failure: ProcessFailure | null = null;
    try {
      await compareZccPullCollectionOperation(options(fixture));
    } catch (error: unknown) {
      assert.ok(error instanceof ProcessFailure);
      failure = error;
    }
    assert.ok(failure);
    const errorText = JSON.stringify({
      code: failure.code,
      category: failure.category,
      message: failure.message,
      details: failure.details,
    });
    assert.equal(errorText.includes(secret), false);
    assert.equal(errorText.includes(fixture.root), false);
  });
});

test("public process maps success, parity, request, and I/O outcomes", async () => {
  await withFixture(async (fixture) => {
    const equalRequest = processRequest(fixture);
    const equal = invoke(equalRequest);
    assert.equal(equal.status, 0, equal.stderr);
    assert.equal(validateProcessResponse(JSON.parse(equal.stdout)), true);

    writeFileSync(
      path.join(
        fixture.node,
        "pulls",
        TENANT,
        "zcc_device_cleanup.json",
      ),
      renderPythonLosslessArtifactJson([{ id: "process-different" }]),
    );
    const differentRequest = processRequest(fixture);
    const different = invoke(differentRequest);
    assert.equal(different.status, 3, different.stderr);
    const differentResponse = JSON.parse(different.stdout);
    assert.equal(validateProcessResponse(differentResponse), true);
    assert.equal(differentResponse.result.status, "different");

    const nodeTarget = path.join(
      fixture.node,
      "pulls",
      TENANT,
      "zcc_device_cleanup.json",
    );
    const afterTarget = path.join(
      fixture.after,
      "pulls",
      TENANT,
      "zcc_device_cleanup.json",
    );
    writeFileSync(afterTarget, readFileSync(nodeTarget));
    const unstable = invoke(processRequest(fixture));
    assert.equal(unstable.status, 3, unstable.stderr);
    const unstableResponse = JSON.parse(unstable.stdout);
    assert.equal(validateProcessResponse(unstableResponse), true);
    assert.equal(unstableResponse.result.status, "unstable_reference");

    const invalid = structuredClone(differentRequest) as {
      context: {
        node_workspace: string;
        python_before_workspace: string;
        python_after_workspace: string;
      };
    };
    invalid.context.python_before_workspace = invalid.context.node_workspace;
    const rejected = invoke(invalid);
    assert.equal(rejected.status, 2, rejected.stderr);
    const errorResponse = JSON.parse(rejected.stdout);
    assert.equal(errorResponse.error.code, "INVALID_ZCC_PULL_COLLECTION_PARITY_ISOLATION");
    assert.equal(validateProcessResponse(errorResponse), true);

    const missingRequest = processRequest(fixture);
    rmSync(path.join(
      fixture.before,
      "pulls",
      TENANT,
      "zcc_device_cleanup.json",
    ));
    const missing = invoke(missingRequest);
    assert.equal(missing.status, 1, missing.stderr);
    const missingResponse = JSON.parse(missing.stdout);
    assert.equal(validateProcessResponse(missingResponse), true);
    assert.equal(missingResponse.error.category, "io");
  });
});
