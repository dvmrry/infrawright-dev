import assert from "node:assert/strict";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

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
  const replaceGuard = (root: string): void => {
    const lockPath = path.join(root, PUBLISHER_GUARD_BASENAME);
    rmSync(lockPath);
    mkdirSync(lockPath);
  };

  await t.test("cleanup failure after success", async () => {
    const root = temporaryRoot("infrawright-publisher-cleanup-failure-");
    try {
      const failure = await expectFailure(
        withPublisherGuard(root, async () => {
          replaceGuard(root);
        }),
        "PUBLISHER_GUARD_CLEANUP_FAILED",
      );
      assert.equal(failure.category, "io");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  for (const kind of ["process-failure", "unexpected-failure"] as const) {
    await t.test(kind, async () => {
      const root = temporaryRoot(`infrawright-publisher-cleanup-${kind}-`);
      try {
        const failure = await expectFailure(
          withPublisherGuard(root, async () => {
            replaceGuard(root);
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
      } finally {
        rmSync(root, { recursive: true, force: true });
      }
    });
  }
});
