import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { runZccCollectionChildProcess } from "../node-src/io/zcc-collection-child-runner.js";

function identity(file: string) {
  const bytes = readFileSync(file);
  return {
    path: file,
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
  };
}

async function withScript(
  source: string,
  callback: (file: string) => Promise<void>,
): Promise<void> {
  const lexical = mkdtempSync(path.join(os.tmpdir(), "zcc-child-runner-"));
  const root = realpathSync(lexical);
  const file = path.join(root, "child.mjs");
  try {
    writeFileSync(file, source, { mode: 0o700 });
    await callback(file);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

const STATIC_ERROR_CHILD = String.raw`
import { writeSync } from "node:fs";
const payload = Buffer.from('{"code":"ZCC_ONEAPI_DIAGNOSTICS_UNSAFE"}', 'utf8');
const frame = Buffer.alloc(12 + payload.length);
frame.write('IWRPv001', 0, 'ascii');
frame.writeUInt32BE(payload.length, 8);
payload.copy(frame, 12);
writeSync(4, frame);
`;

test("closed spawn environment, argv, stdio, and EPIPE arbitration stay exact", async () => {
  await withScript(STATIC_ERROR_CHILD, async (file) => {
    const previous = process.env.NODE_OPTIONS;
    process.env.NODE_OPTIONS = "--inspect=127.0.0.1:0";
    let inspected = false;
    try {
      await assert.rejects(
        runZccCollectionChildProcess({
          environment: {
            ZSCALER_CLIENT_ID: "x".repeat(64 * 1024),
            ZSCALER_CLIENT_SECRET: "private-secret",
            ZSCALER_VANITY_DOMAIN: "tenant",
          },
          resourceType: "zcc_web_privacy",
          runner: {
            childIdentity: identity(file),
            spawnProcess: ((command, args, options) => {
              inspected = true;
              assert.equal(command, process.execPath);
              assert.deepEqual(args, [
                "--disable-sigusr1", "--input-type=module", "-",
              ]);
              assert.equal(options?.shell, false);
              assert.deepEqual(options?.env, { LANG: "C", LC_ALL: "C", TZ: "UTC" });
              assert.deepEqual(options?.stdio, [
                "pipe", "ignore", "ignore", "pipe", "pipe",
              ]);
              return spawn(command, args, options);
            }) as typeof spawn,
          },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE"
          && !JSON.stringify(error).includes("private-secret"),
      );
    } finally {
      if (previous === undefined) delete process.env.NODE_OPTIONS;
      else process.env.NODE_OPTIONS = previous;
    }
    assert.equal(inspected, true);
  });
});

test("missing, tampered, symlinked, and special child paths fail before spawn", async () => {
  await withScript(STATIC_ERROR_CHILD, async (file) => {
    let spawns = 0;
    const countSpawn = (() => {
      spawns += 1;
      return spawn(process.execPath, []);
    }) as unknown as typeof spawn;
    const base = identity(file);
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: { ZSCALER_CLIENT_SECRET: "private-secret" },
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: { ...base, sha256: "0".repeat(64) },
          spawnProcess: countSpawn,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_COLLECTION_CHILD_INTEGRITY_FAILED",
    );
    const alias = path.join(path.dirname(file), "child-link.mjs");
    symlinkSync(file, alias);
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: { ZSCALER_CLIENT_SECRET: "private-secret" },
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: { ...base, path: alias },
          spawnProcess: countSpawn,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_COLLECTION_CHILD_INTEGRITY_FAILED",
    );
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: { ZSCALER_CLIENT_SECRET: "private-secret" },
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: { ...base, path: `${file}.missing` },
          spawnProcess: countSpawn,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_COLLECTION_CHILD_INTEGRITY_FAILED",
    );
    assert.equal(spawns, 0);
  });
});

test("pathname replacement after verification cannot receive fd3 credentials", async () => {
  await withScript(STATIC_ERROR_CHILD, async (file) => {
    const marker = path.join(path.dirname(file), "replacement-ran");
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: {
          ZSCALER_CLIENT_SECRET: "private-secret",
        },
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: identity(file),
          spawnProcess: ((
            command: string,
            args: readonly string[] | undefined,
            options: Parameters<typeof spawn>[2],
          ) => {
            writeFileSync(file, `
              import { createReadStream, writeFileSync } from "node:fs";
              for await (const chunk of createReadStream("", { fd: 3 })) {
                writeFileSync(${JSON.stringify(marker)}, chunk);
              }
            `);
            return spawn(command, args ?? [], options);
          }) as unknown as typeof spawn,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
    );
    assert.equal(
      (() => {
        try { readFileSync(marker); return true; }
        catch { return false; }
      })(),
      false,
    );
  });
});

test("outer deadline kills stalled child and maps to the retryable transaction timeout", async () => {
  await withScript("setInterval(() => undefined, 1000);\n", async (file) => {
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: {},
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: identity(file),
          timeoutMs: 40,
          reapTimeoutMs: 1_000,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ONEAPI_TRANSACTION_TIMEOUT"
        && error.retryable,
    );
  });
});

test("partial and trailing child output fail the closed protocol", async () => {
  for (const source of [
    'import { writeSync } from "node:fs"; writeSync(4, Buffer.from("IWRPv001"));\n',
    String.raw`
import { writeSync } from "node:fs";
const payload = Buffer.from('{"code":"ZCC_ONEAPI_DIAGNOSTICS_UNSAFE"}', 'utf8');
const frame = Buffer.alloc(13 + payload.length);
frame.write('IWRPv001', 0, 'ascii'); frame.writeUInt32BE(payload.length, 8);
payload.copy(frame, 12); frame[frame.length - 1] = 1; writeSync(4, frame);
`,
  ]) {
    await withScript(source, async (file) => {
      await assert.rejects(
        runZccCollectionChildProcess({
          environment: {},
          resourceType: "zcc_web_privacy",
          runner: { childIdentity: identity(file) },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "ZCC_COLLECTION_CHILD_PROTOCOL_FAILED",
      );
    });
  }
});

test("max private write and multi-megabyte response drain concurrently without deadlock", async () => {
  await withScript(String.raw`
import { createReadStream, writeSync } from "node:fs";
for await (const _chunk of createReadStream("", { fd: 3, autoClose: false })) {}
const response = {
  kind: "infrawright.zcc_collection_child_response",
  schema_version: 1,
  status: "ok",
  artifact: {
    body_base64: "A".repeat(6 * 1024 * 1024),
    catalog_sources_sha256: "0".repeat(64),
    data_requests: 1,
    item_count: 1,
    resource_type: "zcc_web_privacy",
    sha256: "0".repeat(64),
    size_bytes: 0,
    transport_attempts: 1
  }
};
const payload = Buffer.from(JSON.stringify(response), "utf8");
const frame = Buffer.alloc(12 + payload.length);
frame.write("IWRPv001", 0, "ascii"); frame.writeUInt32BE(payload.length, 8);
payload.copy(frame, 12);
let offset = 0;
while (offset < frame.length) offset += writeSync(4, frame, offset, frame.length - offset);
`, async (file) => {
    const response = await runZccCollectionChildProcess({
      environment: {
        ZSCALER_CLIENT_ID: "x".repeat(60 * 1024),
        ZSCALER_CLIENT_SECRET: "y".repeat(60 * 1024),
        ZSCALER_VANITY_DOMAIN: "tenant",
      },
      resourceType: "zcc_web_privacy",
      runner: { childIdentity: identity(file), timeoutMs: 5_000 },
    });
    assert.equal(response.artifact.body_base64.length, 6 * 1024 * 1024);
  });
});

test("a complete frame cannot outrun nonzero exit or stalled child cleanup", async () => {
  const nonzero = `${STATIC_ERROR_CHILD}\nprocess.exitCode = 7;\n`;
  await withScript(nonzero, async (file) => {
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: {},
        resourceType: "zcc_web_privacy",
        runner: { childIdentity: identity(file) },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_COLLECTION_CHILD_PROTOCOL_FAILED",
    );
  });
  await withScript(`${STATIC_ERROR_CHILD}\nsetInterval(() => undefined, 1000);\n`, async (file) => {
    await assert.rejects(
      runZccCollectionChildProcess({
        environment: {},
        resourceType: "zcc_web_privacy",
        runner: {
          childIdentity: identity(file),
          timeoutMs: 40,
          reapTimeoutMs: 1_000,
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ONEAPI_TRANSACTION_TIMEOUT"
        && error.retryable,
    );
  });
});

test("closed collector failures preserve category and retryability without diagnostics", async () => {
  const cases = [
    ["INVALID_ZCC_COLLECTOR_RESPONSE", "domain", false],
    ["ZCC_COLLECTOR_HTTP_STATUS", "io", false],
    ["ZCC_COLLECTOR_ITEM_LIMIT", "domain", false],
    ["ZCC_COLLECTOR_RATE_LIMITED", "io", true],
    ["ZCC_COLLECTOR_RESPONSE_LIMIT", "domain", false],
    ["ZCC_COLLECTOR_RETRY_CLOCK_FAILURE", "io", false],
    ["ZCC_COLLECTOR_TRANSPORT_FAILURE", "io", true],
  ] as const;
  for (const [code, category, retryable] of cases) {
    await withScript(STATIC_ERROR_CHILD.replace(
      "ZCC_ONEAPI_DIAGNOSTICS_UNSAFE",
      code,
    ), async (file) => {
      await assert.rejects(
        runZccCollectionChildProcess({
          environment: {},
          resourceType: "zcc_web_privacy",
          runner: { childIdentity: identity(file) },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === code
          && error.category === category
          && error.retryable === retryable
          && error.details.length === 0,
      );
    });
  }
});

test("the actual public-Undici child runs as a relocated integrity-bound sibling", () => {
  const built = spawnSync(process.execPath, ["scripts/build-node.mjs"], {
    cwd: process.cwd(),
    encoding: "utf8",
  });
  assert.equal(built.status, 0, built.stderr);
  const lexical = mkdtempSync(path.join(os.tmpdir(), "zcc-relocated-pair-"));
  const root = realpathSync(lexical);
  try {
    const parent = path.join(root, "infrawright.mjs");
    const child = path.join(root, "infrawright-zcc-collector-child.mjs");
    copyFileSync(path.join(process.cwd(), "dist/infrawright.mjs"), parent);
    copyFileSync(
      path.join(process.cwd(), "dist/infrawright-zcc-collector-child.mjs"),
      child,
    );
    const workspace = path.join(root, "workspace");
    writeFileSync(path.join(root, ".keep"), "");
    mkdirSync(workspace);
    const request = JSON.stringify({
      kind: "infrawright.process_request",
      schema_version: 1,
      request_id: "relocated-child",
      operation: "collect_zcc_pull",
      context: {
        workspace: realpathSync(workspace),
        deployment: "unused.json",
        root_catalog: "unused.json",
      },
      input: {
        mode: "oneapi",
        publication: "replace_or_verify_exact",
        tenant: "test",
        resource_type: "zcc_web_privacy",
      },
    });
    const invoked = spawnSync(process.execPath, [parent], {
      cwd: root,
      input: request,
      encoding: "utf8",
      env: {
        LANG: "C",
        LC_ALL: "C",
        TZ: "UTC",
        INFRAWRIGHT_ZCC_PULL_OUTPUT_ROOT: realpathSync(workspace),
      },
    });
    assert.equal(invoked.status, 1, invoked.stderr);
    const response = JSON.parse(invoked.stdout) as {
      readonly error: { readonly code: string };
    };
    assert.equal(response.error.code, "ZCC_ONEAPI_HOST_CONFIGURATION_INVALID");

    writeFileSync(child, `${readFileSync(child, "utf8")}\n`);
    const tampered = spawnSync(process.execPath, [parent], {
      cwd: root,
      input: request,
      encoding: "utf8",
      env: {
        LANG: "C",
        LC_ALL: "C",
        TZ: "UTC",
        INFRAWRIGHT_ZCC_PULL_OUTPUT_ROOT: realpathSync(workspace),
        ZSCALER_CLIENT_SECRET: "private-secret",
      },
    });
    assert.equal(tampered.status, 1, tampered.stderr);
    assert.equal(
      (JSON.parse(tampered.stdout) as { error: { code: string } }).error.code,
      "ZCC_COLLECTION_CHILD_INTEGRITY_FAILED",
    );
    assert.equal(tampered.stdout.includes("private-secret"), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

async function waitForFile(file: string): Promise<void> {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    try {
      readFileSync(file);
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }
  throw new Error("timed out waiting for child pid file");
}

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

test("SIGTERM, SIGINT, and SIGHUP kill the direct child and redeliver the signal", async () => {
  if (process.platform === "win32") return;
  await withScript("setInterval(() => undefined, 1000);\n", async (childFile) => {
    const runnerUrl = pathToFileURL(path.join(
      process.cwd(),
      ".node-test/node-src/io/zcc-collection-child-runner.js",
    )).href;
    for (const signal of ["SIGTERM", "SIGINT", "SIGHUP"] as const) {
      const pidFile = path.join(path.dirname(childFile), `${signal}.pid`);
      const harnessFile = path.join(path.dirname(childFile), `${signal}.mjs`);
      const childIdentity = identity(childFile);
      writeFileSync(harnessFile, `
        import { readFileSync, writeFileSync } from "node:fs";
        import { createHash } from "node:crypto";
        import { spawn } from "node:child_process";
        import { runZccCollectionChildProcess } from ${JSON.stringify(runnerUrl)};
        const childPath = ${JSON.stringify(childFile)};
        const bytes = readFileSync(childPath);
        await runZccCollectionChildProcess({
          environment: {},
          resourceType: "zcc_web_privacy",
          runner: {
            childIdentity: ${JSON.stringify(childIdentity)},
            timeoutMs: 10000,
            spawnProcess(command, args, options) {
              const child = spawn(command, args, options);
              writeFileSync(${JSON.stringify(pidFile)}, String(child.pid));
              return child;
            }
          }
        });
      `);
      const harness = spawn(process.execPath, [harnessFile], {
        env: { LANG: "C", LC_ALL: "C", TZ: "UTC" },
        stdio: "ignore",
      });
      const closed = new Promise<{
        code: number | null;
        signal: NodeJS.Signals | null;
      }>((resolve) => {
        harness.once("close", (code, observedSignal) => {
          resolve({ code, signal: observedSignal });
        });
      });
      await waitForFile(pidFile);
      const directChildPid = Number(readFileSync(pidFile, "utf8"));
      assert.equal(Number.isSafeInteger(directChildPid), true);
      harness.kill(signal);
      const outcome = await closed;
      assert.equal(outcome.code, null);
      assert.equal(outcome.signal, signal);
      const deadline = Date.now() + 2_000;
      while (processExists(directChildPid) && Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      assert.equal(processExists(directChildPid), false);
    }
  });
});
