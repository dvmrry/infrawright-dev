import assert from "node:assert/strict";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  realpathSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  runTerraformCommand,
  type TerraformCommandLimits,
} from "../node-src/io/terraform-command.js";

const LIMITS: TerraformCommandLimits = {
  timeoutMs: 2_000,
  maxStdoutBytes: 64 * 1024,
  maxStderrBytes: 4 * 1024,
};

interface Fixture {
  readonly root: string;
  readonly cwd: string;
}

async function withTemp(
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "terraform-command-"));
  try {
    const canonicalRoot = realpathSync(root);
    const cwd = join(canonicalRoot, "work");
    mkdirSync(cwd);
    await callback({ root: canonicalRoot, cwd });
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

function baseOptions(fixture: Fixture, terraformExecutable: string) {
  return {
    terraformExecutable,
    argv: [] as readonly string[],
    cwd: fixture.cwd,
    environment: {},
    limits: LIMITS,
  } as const;
}

function assertFailure(error: unknown, code: string): ProcessFailure {
  assert.ok(error instanceof ProcessFailure);
  assert.equal(error.code, code);
  return error;
}

async function captureFailure(
  promise: Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  try {
    await promise;
  } catch (error: unknown) {
    return assertFailure(error, code);
  }
  assert.fail(`expected ${code}`);
}

async function waitForProcessExit(pid: number): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const deadline = Date.now() + 1_000;
    const check = (): void => {
      try {
        process.kill(pid, 0);
        if (Date.now() >= deadline) {
          reject(new Error("Terraform descendant survived process cleanup"));
        } else {
          setTimeout(check, 10);
        }
      } catch {
        resolve();
      }
    };
    check();
  });
}

test("runs exact argv, cwd, stdin, and allowlisted environment without a shell", async () => {
  await withTemp(async (fixture) => {
    const probe = join(fixture.root, "probe.mjs");
    writeFileSync(probe, [
      'import { readFileSync } from "node:fs";',
      "const stdin = readFileSync(0);",
      "process.stdout.write(JSON.stringify({",
      "  argv: process.argv.slice(2),",
      "  cwd: process.cwd(),",
      "  environment: process.env,",
      "  stdinBytes: stdin.length,",
      "}));",
    ].join("\n"));
    const argv = [
      probe,
      "plan",
      "value with spaces",
      "literal;$(printf shell-was-used)",
    ] as const;
    const environment = {
      CHECKPOINT_DISABLE: "1",
      LANG: "C",
    } as const;
    const poison = {
      TF_CLI_ARGS: process.env.TF_CLI_ARGS,
      TF_CLI_ARGS_plan: process.env.TF_CLI_ARGS_plan,
      TF_LOG: process.env.TF_LOG,
      TF_VAR_poison: process.env.TF_VAR_poison,
    };
    process.env.TF_CLI_ARGS = "-destroy";
    process.env.TF_CLI_ARGS_plan = "-out=poisoned";
    process.env.TF_LOG = "TRACE";
    process.env.TF_VAR_poison = "parent-secret";
    try {
      const result = await runTerraformCommand({
        terraformExecutable: process.execPath,
        argv,
        cwd: fixture.cwd,
        environment,
        limits: LIMITS,
        output: "capture",
      });
      const payload = JSON.parse(result.stdout.toString("utf8")) as {
        argv: string[];
        cwd: string;
        environment: Record<string, string>;
        stdinBytes: number;
      };
      assert.deepEqual(payload.argv, argv.slice(1));
      assert.equal(payload.cwd, fixture.cwd);
      const observedEnvironment = { ...payload.environment };
      // macOS inserts this user-encoding process metadata after spawn even
      // when the supplied env is empty; it is not inherited Terraform state.
      if (process.platform === "darwin") {
        assert.match(
          observedEnvironment.__CF_USER_TEXT_ENCODING ?? "",
          /^0x[0-9A-F]+:0x[0-9A-F]+:0x[0-9A-F]+$/i,
        );
        delete observedEnvironment.__CF_USER_TEXT_ENCODING;
      }
      assert.deepEqual(observedEnvironment, environment);
      assert.equal(payload.stdinBytes, 0);
    } finally {
      for (const [key, value] of Object.entries(poison)) {
        if (value === undefined) {
          delete process.env[key];
        } else {
          process.env[key] = value;
        }
      }
    }
  });
});

test("discard mode returns no child output", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(
      fixture.root,
      "printf '%s' 'successful-secret-output'",
    );
    const result = await runTerraformCommand({
      ...baseOptions(fixture, fake),
      output: "discard",
    });
    assert.deepEqual(result, { kind: "discarded" });
  });
});

test("snapshots trusted caller options before executable inspection yields", async () => {
  await withTemp(async (fixture) => {
    const probe = join(fixture.root, "snapshot-probe.mjs");
    writeFileSync(probe, [
      "process.stdout.write(JSON.stringify({",
      "  argv: process.argv.slice(2),",
      "  cwd: process.cwd(),",
      "  observed: process.env.OBSERVED,",
      "}));",
    ].join("\n"));
    const argv = [probe, "original-argument"];
    const environment = { OBSERVED: "original-environment" };
    const limits = { ...LIMITS };
    const options = {
      terraformExecutable: process.execPath,
      argv,
      cwd: fixture.cwd,
      environment,
      limits,
      output: "capture" as const,
    };
    const command = runTerraformCommand(options);
    options.terraformExecutable = join(fixture.root, "mutated-executable");
    options.cwd = join(fixture.root, "mutated-cwd");
    argv[1] = "mutated-argument";
    environment.OBSERVED = "mutated-environment";
    limits.maxStdoutBytes = 1;

    const result = await command;
    const payload = JSON.parse(result.stdout.toString("utf8")) as {
      argv: string[];
      cwd: string;
      observed: string;
    };
    assert.deepEqual(payload, {
      argv: ["original-argument"],
      cwd: fixture.cwd,
      observed: "original-environment",
    });
  });
});

test("child stdout, stderr, environment values, and paths never enter failures", async (t) => {
  const cases = [
    {
      name: "nonzero exit",
      body: "printf '%s' \"$TF_TEST_SECRET\"; printf '%s' \"$TF_TEST_SECRET\" >&2; exit 19",
      code: "TERRAFORM_COMMAND_FAILED",
      limits: LIMITS,
    },
    {
      name: "stdout limit",
      body: "while :; do printf '%s' \"$TF_TEST_SECRET\"; done",
      code: "TERRAFORM_COMMAND_STDOUT_LIMIT",
      limits: { ...LIMITS, maxStdoutBytes: 32 },
    },
    {
      name: "stderr limit",
      body: "while :; do printf '%s' \"$TF_TEST_SECRET\" >&2; done",
      code: "TERRAFORM_COMMAND_STDERR_LIMIT",
      limits: { ...LIMITS, maxStderrBytes: 32 },
    },
  ] as const;
  for (const item of cases) {
    await t.test(item.name, async () => {
      await withTemp(async (fixture) => {
        const fake = executable(fixture.root, item.body);
        const secret = `child-secret-${item.name.replaceAll(" ", "-")}`;
        const failure = await captureFailure(
          runTerraformCommand({
            ...baseOptions(fixture, fake),
            environment: { TF_TEST_SECRET: secret },
            limits: item.limits,
            output: "capture",
          }),
          item.code,
        );
        const diagnostic = [
          String(failure),
          JSON.stringify(failure),
          failure.stack ?? "",
        ].join("\n");
        assert.equal(diagnostic.includes(secret), false);
        assert.equal(diagnostic.includes(fixture.root), false);
      });
    });
  }
});

test("timeout, overflow, nonzero exit, and success reap descendant groups", async (t) => {
  if (process.platform === "win32") {
    t.skip("POSIX process-group contract");
    return;
  }
  const cases = [
    {
      name: "timeout",
      tail: "wait",
      output: "discard" as const,
      limits: { ...LIMITS, timeoutMs: 500 },
      code: "TERRAFORM_COMMAND_TIMEOUT",
    },
    {
      name: "stdout overflow",
      tail: "while :; do printf '%s' 'overflow-secret'; done",
      output: "discard" as const,
      limits: { ...LIMITS, maxStdoutBytes: 32 },
      code: "TERRAFORM_COMMAND_STDOUT_LIMIT",
    },
    {
      name: "nonzero exit",
      tail: "exit 23",
      output: "discard" as const,
      limits: LIMITS,
      code: "TERRAFORM_COMMAND_FAILED",
    },
    {
      name: "success with inherited pipes",
      tail: "exit 0",
      output: "discard" as const,
      limits: LIMITS,
      code: null,
    },
  ] as const;
  for (const item of cases) {
    await t.test(item.name, async () => {
      await withTemp(async (fixture) => {
        const pidFile = join(fixture.root, "descendant.pid");
        const fake = executable(fixture.root, [
          "(while :; do :; done) &",
          "descendant=$!",
          `printf '%s' "$descendant" > '${pidFile}'`,
          item.tail,
        ].join("\n"));
        const command = runTerraformCommand({
          ...baseOptions(fixture, fake),
          limits: item.limits,
          output: item.output,
        });
        if (item.code === null) {
          assert.deepEqual(await command, { kind: "discarded" });
        } else {
          await captureFailure(command, item.code);
        }
        const pid = Number((await readFile(pidFile, "utf8")).trim());
        assert.equal(Number.isSafeInteger(pid) && pid > 0, true);
        await waitForProcessExit(pid);
      });
    });
  }
});

test("unresolved, missing, non-executable, and symlink executables fail closed", async (t) => {
  await withTemp(async (fixture) => {
    await t.test("relative executable", async () => {
      await captureFailure(
        runTerraformCommand({
          ...baseOptions(fixture, "terraform"),
          output: "discard",
        }),
        "UNRESOLVED_TERRAFORM_COMMAND_PATH",
      );
    });
    await t.test("missing executable", async () => {
      await captureFailure(
        runTerraformCommand({
          ...baseOptions(fixture, join(fixture.root, "missing")),
          output: "discard",
        }),
        "UNTRUSTED_TERRAFORM_EXECUTABLE",
      );
    });
    await t.test("non-executable file", async () => {
      const file = join(fixture.root, "not-executable");
      writeFileSync(file, "opaque", { mode: 0o600 });
      await captureFailure(
        runTerraformCommand({
          ...baseOptions(fixture, file),
          output: "discard",
        }),
        "UNTRUSTED_TERRAFORM_EXECUTABLE",
      );
    });
    await t.test("executable symlink", async () => {
      const fake = executable(fixture.root, "exit 0");
      const link = join(fixture.root, "terraform-link");
      symlinkSync(fake, link);
      await captureFailure(
        runTerraformCommand({
          ...baseOptions(fixture, link),
          output: "discard",
        }),
        "UNTRUSTED_TERRAFORM_EXECUTABLE",
      );
    });
  });
});

test("argument, environment, output, and limit bounds reject hostile inputs", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, "exit 0");
    const sparse = new Array<string>(1);
    const hostileCases: Array<{
      readonly options: Parameters<typeof runTerraformCommand>[0];
      readonly code: string;
    }> = [
      {
        options: {
          ...baseOptions(fixture, fake),
          argv: sparse,
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          argv: ["plan\0secret"],
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          environment: { TF_TOKEN: "secret\0suffix" },
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          environment: new Proxy({}, {
            getPrototypeOf: () => {
              throw new Error("proxy-secret");
            },
          }),
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          limits: Object.defineProperty({}, "timeoutMs", {
            enumerable: true,
            get: () => {
              throw new Error("limit-secret");
            },
          }) as TerraformCommandLimits,
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_LIMIT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          limits: { ...LIMITS, timeoutMs: 0 },
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_LIMIT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          limits: {
            ...LIMITS,
            timeoutMs: 10 * 60 * 1000 + 1,
          },
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_LIMIT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          limits: {
            ...LIMITS,
            maxStderrBytes: 16 * 1024 * 1024 + 1,
          },
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_LIMIT",
      },
      {
        options: {
          ...baseOptions(fixture, fake),
          limits: {
            ...LIMITS,
            maxStdoutBytes: 8 * 1024 * 1024 + 1,
          },
          output: "discard",
        },
        code: "INVALID_TERRAFORM_COMMAND_LIMIT",
      },
    ];
    for (const item of hostileCases) {
      await captureFailure(runTerraformCommand(item.options), item.code);
    }
  });
});
