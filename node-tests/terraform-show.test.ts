import assert from "node:assert/strict";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  terraformShowPlan,
  type TerraformShowLimits,
} from "../node-src/io/terraform-show.js";

const LIMITS: TerraformShowLimits = {
  timeoutMs: 2_000,
  maxStdoutBytes: 64 * 1024,
  maxStderrBytes: 4 * 1024,
};

interface Fixture {
  readonly root: string;
  readonly envDir: string;
  readonly planPath: string;
}

async function withTemp(
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "terraform-show-"));
  try {
    const envDir = join(root, "env");
    const planPath = join(root, "snapshot");
    mkdirSync(envDir);
    writeFileSync(planPath, "opaque plan bytes\n", { mode: 0o600 });
    await callback({ root, envDir, planPath });
  } finally {
    rmSync(root, { force: true, recursive: true });
  }
}

function executable(root: string, body: string): string {
  const file = join(root, `terraform-${Math.random().toString(16).slice(2)}`);
  writeFileSync(file, `#!/bin/sh\n${body}\n`, { mode: 0o700 });
  chmodSync(file, 0o700);
  return file;
}

function options(fixture: Fixture, terraformExecutable: string) {
  return {
    terraformExecutable,
    envDir: fixture.envDir,
    snapshotPath: fixture.planPath,
    limits: LIMITS,
  } as const;
}

function assertFailure(error: unknown, code: string): boolean {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return true;
}

test("runs a fixed Terraform show invocation with a stripped environment", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, [
      'if [ "${TF_CLI_ARGS_show+x}" = x ]; then exit 31; fi',
      `if [ "$1" != "-chdir=${fixture.envDir}" ]; then exit 32; fi`,
      'if [ "$2" != show ] || [ "$3" != -json ]; then exit 33; fi',
      `if [ "$4" != "${fixture.planPath}" ]; then exit 34; fi`,
      `printf '%s' '{"format_version":"1.2","complete":true,`
        + `"errored":false,"value":9007199254740993}'`,
    ].join("\n"));
    process.env.TF_CLI_ARGS_show = "malicious-parent-value";
    try {
      const plan = await terraformShowPlan(options(fixture, fake));
      assert.equal(
        (plan as { value: { toString(): string } }).value.toString(),
        "9007199254740993",
      );
    } finally {
      delete process.env.TF_CLI_ARGS_show;
    }
  });
});

test("child stdout, stderr, paths, and secrets never enter failures", async (t) => {
  for (const [name, body, limits, code] of [
    [
      "nonzero",
      "printf '%s' 'stderr-secret-41fd' >&2; exit 19",
      LIMITS,
      "TERRAFORM_SHOW_FAILED",
    ],
    [
      "stdout limit",
      `printf '%s' '${"stdout-secret-7c2a".repeat(100)}'`,
      { ...LIMITS, maxStdoutBytes: 32 },
      "TERRAFORM_SHOW_STDOUT_LIMIT",
    ],
    [
      "stderr limit",
      `printf '%s' '${"stderr-secret-9b10".repeat(100)}' >&2`,
      { ...LIMITS, maxStderrBytes: 32 },
      "TERRAFORM_SHOW_STDERR_LIMIT",
    ],
    [
      "invalid json",
      "printf '%s' 'json-secret-f3b8-not-json'",
      LIMITS,
      "INVALID_TERRAFORM_SHOW_JSON",
    ],
  ] as const) {
    await t.test(name, async () => {
      await withTemp(async (fixture) => {
        const fake = executable(fixture.root, body);
        let failure: unknown;
        try {
          await terraformShowPlan({
            ...options(fixture, fake),
            limits,
          });
        } catch (error: unknown) {
          failure = error;
        }
        assertFailure(failure, code);
        const diagnostic = JSON.stringify(failure);
        assert.equal(diagnostic.includes("secret"), false);
        assert.equal(diagnostic.includes(fixture.root), false);
      });
    });
  }
});

test("timeouts kill a non-terminating child", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, "while :; do :; done");
    await assert.rejects(
      terraformShowPlan({
        ...options(fixture, fake),
        limits: { ...LIMITS, timeoutMs: 25 },
      }),
      (error: unknown) => assertFailure(error, "TERRAFORM_SHOW_TIMEOUT"),
    );
  });
});

test("timeouts and success both reap inherited descendant process groups", async (t) => {
  if (process.platform === "win32") {
    t.skip("POSIX process-group contract");
    return;
  }
  await t.test("timeout with inherited pipes", async () => {
    await withTemp(async (fixture) => {
      const fake = executable(
        fixture.root,
        "(while :; do :; done) & wait",
      );
      const started = Date.now();
      await assert.rejects(
        terraformShowPlan({
          ...options(fixture, fake),
          limits: { ...LIMITS, timeoutMs: 50 },
        }),
        (error: unknown) => assertFailure(error, "TERRAFORM_SHOW_TIMEOUT"),
      );
      assert.ok(Date.now() - started < 1_000);
    });
  });

  await t.test("successful parent with detached descendant", async () => {
    await withTemp(async (fixture) => {
      const pidFile = join(fixture.root, "descendant.pid");
      const fake = executable(fixture.root, [
        `(while :; do :; done) >/dev/null 2>&1 &`,
        `printf '%s' "$!" > '${pidFile}'`,
        `printf '%s' '{"format_version":"1.2","complete":true,"errored":false}'`,
      ].join("\n"));
      await terraformShowPlan(options(fixture, fake));
      const pid = Number((await readFile(pidFile, "utf8")).trim());
      assert.equal(Number.isInteger(pid), true);
      await new Promise<void>((resolve, reject) => {
        const deadline = Date.now() + 1_000;
        const check = (): void => {
          try {
            process.kill(pid, 0);
            if (Date.now() >= deadline) {
              reject(new Error("descendant process survived Terraform show"));
            } else {
              setTimeout(check, 10);
            }
          } catch {
            resolve();
          }
        };
        check();
      });
    });
  });
});

test("invalid UTF-8 and unsafe executable or snapshot paths fail closed", async (t) => {
  await t.test("invalid UTF-8", async () => {
    await withTemp(async (fixture) => {
      const fake = executable(fixture.root, "printf '\\377'");
      await assert.rejects(
        terraformShowPlan(options(fixture, fake)),
        (error: unknown) => assertFailure(error, "INVALID_TERRAFORM_SHOW_UTF8"),
      );
    });
  });

  await t.test("executable symlink", async () => {
    await withTemp(async (fixture) => {
      const fake = executable(fixture.root, "exit 0");
      const link = join(fixture.root, "terraform-link");
      symlinkSync(fake, link);
      await assert.rejects(
        terraformShowPlan(options(fixture, link)),
        (error: unknown) => assertFailure(error, "UNTRUSTED_TERRAFORM_EXECUTABLE"),
      );
    });
  });

  await t.test("snapshot symlink", async () => {
    await withTemp(async (fixture) => {
      const fake = executable(fixture.root, "exit 0");
      const link = join(fixture.root, "snapshot-link");
      symlinkSync(fixture.planPath, link);
      await assert.rejects(
        terraformShowPlan({ ...options(fixture, fake), snapshotPath: link }),
        (error: unknown) => assertFailure(error, "INVALID_PLAN_SNAPSHOT"),
      );
    });
  });
});

test("Terraform show resource limits have fixed upper and lower bounds", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, "exit 0");
    for (const limits of [
      { ...LIMITS, timeoutMs: 0 },
      { ...LIMITS, timeoutMs: 10 * 60 * 1000 + 1 },
      { ...LIMITS, maxStdoutBytes: 512 * 1024 * 1024 + 1 },
      { ...LIMITS, maxStderrBytes: 16 * 1024 * 1024 + 1 },
    ]) {
      await assert.rejects(
        terraformShowPlan({ ...options(fixture, fake), limits }),
        (error: unknown) => assertFailure(error, "INVALID_TERRAFORM_SHOW_LIMIT"),
      );
    }
  });
});

test("NUL-bearing paths cannot escape the structured failure boundary", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, "exit 0");
    await assert.rejects(
      terraformShowPlan({
        ...options(fixture, fake),
        envDir: `${fixture.envDir}\0secret-path`,
      }),
      (error: unknown) => assertFailure(error, "UNRESOLVED_TERRAFORM_SHOW_PATH"),
    );
  });
});
