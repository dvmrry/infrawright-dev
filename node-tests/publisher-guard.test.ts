import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  PUBLISHER_GUARD_BASENAME,
  withPublisherGuard,
} from "../node-src/io/publisher-guard.js";

function temporaryRoot(prefix: string): string {
  return realpathSync(mkdtempSync(path.join(os.tmpdir(), prefix)));
}

function deferred(): {
  readonly promise: Promise<void>;
  readonly resolve: () => void;
} {
  let resolve: (() => void) | undefined;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return {
    promise,
    resolve: () => {
      assert.notEqual(resolve, undefined);
      resolve?.();
    },
  };
}

async function expectFailure(
  operation: Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  try {
    await operation;
    assert.fail(`expected ${code}`);
  } catch (error: unknown) {
    assert.equal(error instanceof ProcessFailure, true);
    const failure = error as ProcessFailure;
    assert.equal(failure.code, code);
    return failure;
  }
}

test("publisher guard fails fast for the same physical output root", async () => {
  const root = temporaryRoot("infrawright-publisher-same-");
  const entered = deferred();
  const release = deferred();
  try {
    const first = withPublisherGuard(root, async () => {
      assert.deepEqual(readdirSync(root), [PUBLISHER_GUARD_BASENAME]);
      entered.resolve();
      await release.promise;
    });
    await entered.promise;

    const failure = await expectFailure(
      withPublisherGuard(root, async () => undefined),
      "OUTPUT_ROOT_BUSY",
    );
    assert.equal(failure.category, "io");
    assert.equal(failure.retryable, true);
    assert.deepEqual(readdirSync(root), [PUBLISHER_GUARD_BASENAME]);

    release.resolve();
    await first;
    assert.deepEqual(readdirSync(root), []);
  } finally {
    release.resolve();
    rmSync(root, { recursive: true, force: true });
  }
});

test("publisher guard permits concurrent mutations in disjoint roots", async () => {
  const left = temporaryRoot("infrawright-publisher-left-");
  const right = temporaryRoot("infrawright-publisher-right-");
  const bothEntered = deferred();
  let active = 0;
  let maximum = 0;
  const run = (root: string): Promise<void> => {
    return withPublisherGuard(root, async () => {
      active += 1;
      maximum = Math.max(maximum, active);
      if (active === 2) {
        bothEntered.resolve();
      }
      await bothEntered.promise;
      active -= 1;
    });
  };
  try {
    await Promise.all([run(left), run(right)]);
    assert.equal(maximum, 2);
    assert.deepEqual(readdirSync(left), []);
    assert.deepEqual(readdirSync(right), []);
  } finally {
    rmSync(left, { recursive: true, force: true });
    rmSync(right, { recursive: true, force: true });
  }
});

test("publisher guard never waits on or auto-breaks a stale guard", async () => {
  const root = temporaryRoot("infrawright-publisher-stale-");
  const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
  const stale = "owned by a prior job\n";
  try {
    writeFileSync(lockPath, stale, { mode: 0o600 });
    await expectFailure(
      withPublisherGuard(root, async () => undefined),
      "OUTPUT_ROOT_BUSY",
    );
    assert.equal(readFileSync(lockPath, "utf8"), stale);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("publisher guard is removed after success and mutation failures", async (t) => {
  for (const kind of ["success", "process-failure", "unexpected-failure"] as const) {
    await t.test(kind, async () => {
      const root = temporaryRoot(`infrawright-publisher-cleanup-${kind}-`);
      const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
      try {
        if (kind === "success") {
          assert.equal(
            await withPublisherGuard(root, async () => "complete"),
            "complete",
          );
        } else if (kind === "process-failure") {
          const failure = await expectFailure(
            withPublisherGuard(root, async () => {
              throw new ProcessFailure({
                code: "MUTATION_FAILED",
                category: "domain",
                message: "mutation failed",
              });
            }),
            "MUTATION_FAILED",
          );
          assert.equal(failure.category, "domain");
        } else {
          await assert.rejects(
            withPublisherGuard(root, async () => {
              throw new Error("unexpected mutation failure");
            }),
            /unexpected mutation failure/,
          );
        }
        assert.equal(existsSync(lockPath), false);
      } finally {
        rmSync(root, { recursive: true, force: true });
      }
    });
  }
});

test("publisher guard cleanup failures are terminal without masking a primary failure", async (t) => {
  const foreign = "foreign-publisher-guard\n";
  const replaceGuard = (root: string): string => {
    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    const ownedPath = `${lockPath}.owned`;
    renameSync(lockPath, ownedPath);
    writeFileSync(lockPath, foreign, { mode: 0o600 });
    return ownedPath;
  };

  await t.test("cleanup failure after success", async () => {
    const root = temporaryRoot("infrawright-publisher-cleanup-failure-");
    try {
      let ownedPath = "";
      const failure = await expectFailure(
        withPublisherGuard(root, async () => {
          ownedPath = replaceGuard(root);
        }),
        "PUBLISHER_GUARD_CLEANUP_FAILED",
      );
      assert.equal(failure.category, "io");
      assert.equal(readFileSync(path.join(root, PUBLISHER_GUARD_BASENAME), "utf8"), foreign);
      assert.equal(readFileSync(ownedPath).length, 0);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  for (const kind of ["process-failure", "unexpected-failure"] as const) {
    await t.test(kind, async () => {
      const root = temporaryRoot(`infrawright-publisher-cleanup-${kind}-`);
      try {
        let ownedPath = "";
        const failure = await expectFailure(
          withPublisherGuard(root, async () => {
            ownedPath = replaceGuard(root);
            if (kind === "process-failure") {
              throw new ProcessFailure({
                code: "MUTATION_FAILED",
                category: "domain",
                message: "mutation failed",
              });
            }
            throw new Error("unexpected mutation failure");
          }),
          kind === "process-failure" ? "MUTATION_FAILED" : "INTERNAL_ERROR",
        );
        assert.deepEqual(failure.details, [{
          path: "$",
          code: "PUBLISHER_GUARD_CLEANUP_FAILED",
          message: "publisher guard could not be removed safely",
        }]);
        assert.equal(readFileSync(path.join(root, PUBLISHER_GUARD_BASENAME), "utf8"), foreign);
        assert.equal(readFileSync(ownedPath).length, 0);
      } finally {
        rmSync(root, { recursive: true, force: true });
      }
    });
  }
});

test("publisher guard tracks thrown-value presence across cleanup failure", async () => {
  for (const [name, thrown] of [
    ["null", null],
    ["undefined", undefined],
    ["primitive", "non-error-thrown-value"],
  ] as const) {
    const root = temporaryRoot(`infrawright-publisher-thrown-${name}-`);
    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    const ownedPath = `${lockPath}.owned`;
    try {
      const failure = await expectFailure(
        withPublisherGuard(root, async () => {
          renameSync(lockPath, ownedPath);
          writeFileSync(lockPath, `${name}-foreign\n`, { mode: 0o600 });
          throw thrown;
        }),
        "INTERNAL_ERROR",
      );
      assert.equal(failure.category, "internal");
      assert.deepEqual(failure.details, [{
        path: "$",
        code: "PUBLISHER_GUARD_CLEANUP_FAILED",
        message: "publisher guard could not be removed safely",
      }]);
      assert.equal(readFileSync(lockPath, "utf8"), `${name}-foreign\n`);
      assert.equal(readFileSync(ownedPath).length, 0);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }
});

test("publisher guard refuses root rollover without unlinking another owner", {
  skip: process.platform === "win32",
}, async (t) => {
  await t.test("original guard inode rebound beneath a replacement root", async () => {
    const root = temporaryRoot("infrawright-publisher-root-rebound-");
    const escaped = `${root}.escaped`;
    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    try {
      const failure = await expectFailure(
        withPublisherGuard(root, async () => {
          renameSync(root, escaped);
          mkdirSync(root);
          renameSync(
            path.join(escaped, PUBLISHER_GUARD_BASENAME),
            lockPath,
          );
        }),
        "PUBLISHER_GUARD_CLEANUP_FAILED",
      );
      assert.equal(failure.category, "io");
      assert.equal(existsSync(lockPath), true);
      assert.equal(readFileSync(lockPath).length, 0);
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(escaped, { recursive: true, force: true });
    }
  });

  await t.test("a replacement-root publisher remains visible to a third publisher", async () => {
    const root = temporaryRoot("infrawright-publisher-root-foreign-");
    const escaped = `${root}.escaped`;
    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    const secondEntered = deferred();
    const releaseSecond = deferred();
    const state: { second?: Promise<void> } = {};
    try {
      const failure = await expectFailure(
        withPublisherGuard(root, async () => {
          renameSync(root, escaped);
          mkdirSync(root);
          state.second = withPublisherGuard(root, async () => {
            secondEntered.resolve();
            await releaseSecond.promise;
          });
          await secondEntered.promise;
        }),
        "PUBLISHER_GUARD_CLEANUP_FAILED",
      );
      assert.equal(failure.category, "io");
      assert.equal(existsSync(lockPath), true);
      assert.equal(
        readFileSync(path.join(escaped, PUBLISHER_GUARD_BASENAME)).length,
        0,
      );
      const third = await expectFailure(
        withPublisherGuard(root, async () => undefined),
        "OUTPUT_ROOT_BUSY",
      );
      assert.equal(third.retryable, true);
      assert.equal(existsSync(lockPath), true);

      releaseSecond.resolve();
      assert.notEqual(state.second, undefined);
      await state.second;
      assert.equal(existsSync(lockPath), false);
    } finally {
      releaseSecond.resolve();
      await state.second?.catch(() => undefined);
      rmSync(root, { recursive: true, force: true });
      rmSync(escaped, { recursive: true, force: true });
    }
  });
});

test("terminated publisher leaves a stale guard that is never auto-broken", {
  skip: process.platform === "win32",
}, async () => {
  const root = temporaryRoot("infrawright-publisher-killed-");
  const moduleUrl = pathToFileURL(
    path.join(process.cwd(), ".node-test/node-src/io/publisher-guard.js"),
  ).href;
  const script = `
    import { withPublisherGuard } from ${JSON.stringify(moduleUrl)};
    await withPublisherGuard(process.argv[1], async () => {
      process.stdout.write("held\\n");
      setInterval(() => undefined, 1000);
      await new Promise(() => undefined);
    });
  `;
  const child = spawn(
    process.execPath,
    ["--input-type=module", "-e", script, root],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  try {
    const [chunk] = await once(child.stdout, "data", {
      signal: AbortSignal.timeout(5_000),
    });
    assert.equal(String(chunk), "held\n");
    const exited = once(child, "exit", { signal: AbortSignal.timeout(5_000) });
    assert.equal(child.kill("SIGKILL"), true);
    await exited;

    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    assert.equal(existsSync(lockPath), true);
    await expectFailure(
      withPublisherGuard(root, async () => undefined),
      "OUTPUT_ROOT_BUSY",
    );
    assert.equal(existsSync(lockPath), true);
  } finally {
    child.kill("SIGKILL");
    rmSync(root, { recursive: true, force: true });
  }
});
