import assert from "node:assert/strict";
import { spawn } from "node:child_process";
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
import { join, relative } from "node:path";
import { performance } from "node:perf_hooks";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  resolveTerraformExecutable,
  runTerraformCommand,
  terraformExecutableCandidates,
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

interface ObservedLinuxProcessIdentity {
  readonly kind: "observed";
  readonly startTime: string;
  readonly state: string;
}

type LinuxProcessIdentity =
  | { readonly kind: "missing" }
  | { readonly kind: "unreadable" }
  | ObservedLinuxProcessIdentity;

async function linuxProcessIdentity(pid: number): Promise<LinuxProcessIdentity> {
  if (process.platform !== "linux") return { kind: "unreadable" };
  let stat: string;
  try {
    stat = await readFile(`/proc/${pid}/stat`, "utf8");
  } catch (error: unknown) {
    const code = error !== null && typeof error === "object" && "code" in error
      ? (error as { readonly code?: unknown }).code
      : undefined;
    return code === "ENOENT" || code === "ESRCH"
      ? { kind: "missing" }
      : { kind: "unreadable" };
  }
  const close = stat.lastIndexOf(")");
  const fields = close >= 0
    ? stat.slice(close + 2).trim().split(/\s+/u)
    : [];
  if (fields.length <= 19) return { kind: "unreadable" };
  return {
    kind: "observed",
    state: fields[0] ?? "",
    startTime: fields[19] ?? "",
  };
}

async function waitForProcessExit(
  pid: number,
  expectedLinuxStartTime?: string,
): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const deadline = Date.now() + 1_000;
    const check = async (): Promise<void> => {
      try {
        process.kill(pid, 0);
        if (process.platform === "linux") {
          const identity = await linuxProcessIdentity(pid);
          if (
            identity.kind === "missing"
            || (identity.kind === "observed" && identity.state === "Z")
            || (expectedLinuxStartTime !== undefined
              && identity.kind === "observed"
              && identity.startTime !== expectedLinuxStartTime)
          ) {
            resolve();
            return;
          }
        }
        if (Date.now() >= deadline) {
          reject(new Error("Terraform descendant survived process cleanup"));
        } else {
          setTimeout(() => void check(), 10);
        }
      } catch {
        resolve();
      }
    };
    void check();
  });
}

async function waitForFile(file: string): Promise<void> {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    try {
      await readFile(file);
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }
  throw new Error(`timed out waiting for ${file}`);
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

test("accepts CI-sized environments while retaining count and byte bounds", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, 'test "$CI_ENV_499" = "value"');
    const environment = Object.fromEntries(
      Array.from({ length: 500 }, (_, index) => [`CI_ENV_${index}`, "value"]),
    );
    assert.deepEqual(await runTerraformCommand({
      ...baseOptions(fixture, fake),
      environment,
      output: "discard",
    }), { kind: "discarded" });

    const tooMany = Object.fromEntries(
      Array.from({ length: 4097 }, (_, index) => [`CI_ENV_${index}`, "value"]),
    );
    await captureFailure(runTerraformCommand({
      ...baseOptions(fixture, fake),
      environment: tooMany,
      output: "discard",
    }), "INVALID_TERRAFORM_COMMAND_ENVIRONMENT");

    await captureFailure(runTerraformCommand({
      ...baseOptions(fixture, fake),
      environment: { OVERSIZED: "x".repeat(256 * 1024) },
      output: "discard",
    }), "INVALID_TERRAFORM_COMMAND_ENVIRONMENT");
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

test("inherit mode streams both channels and preserves nonzero failure", async () => {
  await withTemp(async (fixture) => {
    const success = executable(
      fixture.root,
      "printf '%s' 'visible-stdout'; printf '%s' 'visible-stderr' >&2",
    );
    let stdout = "";
    let stderr = "";
    const originalStdout = process.stdout.write;
    const originalStderr = process.stderr.write;
    process.stdout.write = ((chunk: string | Uint8Array) => {
      stdout += Buffer.from(chunk).toString("utf8");
      return true;
    }) as typeof process.stdout.write;
    process.stderr.write = ((chunk: string | Uint8Array) => {
      stderr += Buffer.from(chunk).toString("utf8");
      return true;
    }) as typeof process.stderr.write;
    try {
      assert.deepEqual(await runTerraformCommand({
        ...baseOptions(fixture, success),
        output: "inherit",
      }), { kind: "inherited" });
    } finally {
      process.stdout.write = originalStdout;
      process.stderr.write = originalStderr;
    }
    assert.equal(stdout.endsWith("visible-stdout"), true);
    assert.equal(stderr.includes("visible-stderr"), true);

    const failure = executable(fixture.root, "exit 37");
    await captureFailure(runTerraformCommand({
      ...baseOptions(fixture, failure),
      output: "inherit",
    }), "TERRAFORM_COMMAND_FAILED");
  });
});

test("inherit-stderr mode suppresses stdout, streams stderr, and preserves failure", async () => {
  await withTemp(async (fixture) => {
    const fake = executable(
      fixture.root,
      "printf '%s' 'hidden-stdout'; printf '%s' 'visible-stderr' >&2; exit \"${TF_EXIT:-0}\"",
    );
    let stdout = "";
    let stderr = "";
    const originalStdout = process.stdout.write;
    const originalStderr = process.stderr.write;
    process.stdout.write = ((chunk: string | Uint8Array) => {
      stdout += Buffer.from(chunk).toString("utf8");
      return true;
    }) as typeof process.stdout.write;
    process.stderr.write = ((chunk: string | Uint8Array) => {
      stderr += Buffer.from(chunk).toString("utf8");
      return true;
    }) as typeof process.stderr.write;
    try {
      assert.deepEqual(await runTerraformCommand({
        ...baseOptions(fixture, fake),
        output: "inherit-stderr",
      }), { kind: "inherited" });
      await captureFailure(runTerraformCommand({
        ...baseOptions(fixture, fake),
        environment: { TF_EXIT: "37" },
        output: "inherit-stderr",
      }), "TERRAFORM_COMMAND_FAILED");
    } finally {
      process.stdout.write = originalStdout;
      process.stderr.write = originalStderr;
    }
    assert.equal(stdout.includes("hidden-stdout"), false);
    assert.equal(stderr, "visible-stderrvisible-stderr");
  });
});

test("no-deadline and long practical deadlines do not inherit the old ten-minute ceiling", async () => {
  await withTemp(async (fixture) => {
    const delayed = executable(fixture.root, "sleep 0.05; exit 0");
    assert.deepEqual(await runTerraformCommand({
      ...baseOptions(fixture, delayed),
      limits: { ...LIMITS, timeoutMs: null },
      output: "discard",
    }), { kind: "discarded" });
    const immediate = executable(fixture.root, "exit 0");
    assert.deepEqual(await runTerraformCommand({
      ...baseOptions(fixture, immediate),
      limits: { ...LIMITS, timeoutMs: 86_400_000 },
      output: "discard",
    }), { kind: "discarded" });
    assert.deepEqual(await runTerraformCommand({
      ...baseOptions(fixture, immediate),
      limits: { ...LIMITS, timeoutMs: Number.MAX_SAFE_INTEGER },
      output: "discard",
    }), { kind: "discarded" });
  });
});

test("child timeout uses an independent monotonic clock", async (context) => {
  await withTemp(async (fixture) => {
    context.mock.method(performance, "now", () => 0);
    const blocked = executable(fixture.root, "while :; do sleep 1; done");
    await captureFailure(runTerraformCommand({
      ...baseOptions(fixture, blocked),
      limits: { ...LIMITS, timeoutMs: 30 },
      output: "discard",
    }), "TERRAFORM_COMMAND_TIMEOUT");
  });
});

test("Terraform executable candidates use POSIX and Windows path semantics", async () => {
  assert.deepEqual(terraformExecutableCandidates(
    "C:\\tools\\terraform.exe",
    {},
    { cwd: "D:\\work", platform: "win32" },
  ), ["C:\\tools\\terraform.exe"]);
  assert.deepEqual(terraformExecutableCandidates(
    "C:/tools/terraform.exe",
    {},
    { cwd: "D:\\work", platform: "win32" },
  ), ["C:\\tools\\terraform.exe"]);
  assert.deepEqual(terraformExecutableCandidates(
    "..\\bin\\terraform.exe",
    {},
    { cwd: "C:\\work\\repo", platform: "win32" },
  ), ["C:\\work\\bin\\terraform.exe"]);
  assert.deepEqual(terraformExecutableCandidates(
    "./bin/terraform",
    {},
    { cwd: "/work/repo", platform: "linux" },
  ), ["/work/repo/bin/terraform"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: "/first:/second" },
    { cwd: "/work", platform: "linux" },
  ), ["/first/terraform", "/second/terraform"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: ":/usr/bin" },
    { cwd: "/work", platform: "linux" },
  ), ["/work/terraform", "/usr/bin/terraform"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: "/usr/bin:" },
    { cwd: "/work", platform: "linux" },
  ), ["/usr/bin/terraform", "/work/terraform"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: "/usr/bin::/bin" },
    { cwd: "/work", platform: "linux" },
  ), ["/usr/bin/terraform", "/work/terraform", "/bin/terraform"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: "::/usr/bin::" },
    { cwd: "/work", platform: "linux" },
  ), [
    "/work/terraform",
    "/work/terraform",
    "/usr/bin/terraform",
    "/work/terraform",
    "/work/terraform",
  ]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    {},
    { cwd: "/work", platform: "linux" },
  ), []);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform\\literal",
    { PATH: "/first" },
    { cwd: "/work", platform: "linux" },
  ), ["/first/terraform\\literal"]);
  assert.deepEqual(terraformExecutableCandidates(
    "terraform",
    { PATH: "C:\\first;D:\\second", PATHEXT: ".EXE;.CMD" },
    { cwd: "C:\\work", platform: "win32" },
  ), [
    "C:\\first\\terraform.exe",
    "C:\\first\\terraform.cmd",
    "D:\\second\\terraform.exe",
    "D:\\second\\terraform.cmd",
  ]);
  for (const pathValue of [
    ";C:\\tools",
    "C:\\tools;",
    "C:\\first;;D:\\second",
    ";;C:\\tools;;",
  ]) {
    assert.deepEqual(terraformExecutableCandidates(
      "terraform",
      { PATH: pathValue, PATHEXT: ".EXE" },
      { cwd: "D:\\work", platform: "win32" },
    ), pathValue.split(";").filter((entry) => entry.length > 0).map((entry) => {
      return `${entry}\\terraform.exe`;
    }));
  }

  await withTemp(async (fixture) => {
    const fake = executable(fixture.root, "exit 0");
    assert.equal(
      await resolveTerraformExecutable(relative(process.cwd(), fake), process.env),
      fake,
    );
    const target = join(fixture.root, "terraform-from-path");
    writeFileSync(target, "#!/bin/sh\nexit 0\n", { mode: 0o700 });
    chmodSync(target, 0o700);
    assert.equal(
      await resolveTerraformExecutable("terraform-from-path", { PATH: fixture.root }),
      realpathSync(target),
    );
    const other = join(fixture.root, "other");
    mkdirSync(other);
    const fallback = join(other, "terraform-fallback");
    writeFileSync(fallback, "#!/bin/sh\nexit 0\n", { mode: 0o700 });
    chmodSync(fallback, 0o700);
    const originalCwd = process.cwd();
    try {
      process.chdir(fixture.cwd);
      assert.equal(
        await resolveTerraformExecutable(
          "terraform-fallback",
          { PATH: `:${other}` },
        ),
        realpathSync(fallback),
      );
    } finally {
      process.chdir(originalCwd);
    }
  });
});

test("Windows Terraform execution fails before spawning a child", async () => {
  await withTemp(async (fixture) => {
    const marker = join(fixture.root, "spawned");
    const fake = executable(fixture.root, `printf '%s' spawned > '${marker}'`);
    const platform = Object.getOwnPropertyDescriptor(process, "platform");
    assert.notEqual(platform, undefined);
    try {
      Object.defineProperty(process, "platform", {
        ...platform,
        value: "win32",
      });
      const failure = await captureFailure(runTerraformCommand({
        ...baseOptions(fixture, fake),
        output: "discard",
      }), "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM");
      assert.equal(
        failure.message,
        "Terraform execution through Infrawright is supported on Linux and macOS; Windows is not a supported operational platform.",
      );
    } finally {
      Object.defineProperty(process, "platform", platform ?? {});
    }
    await assert.rejects(readFile(marker));
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

test("termination signals reap every active Terraform group and retain signal exits", async (t) => {
  if (process.platform === "win32") {
    t.skip("POSIX process-group contract");
    return;
  }
  await withTemp(async (fixture) => {
    const runnerUrl = pathToFileURL(join(
      process.cwd(),
      ".node-test/node-src/io/terraform-command.js",
    )).href;
    for (const signal of ["SIGTERM", "SIGINT", "SIGHUP"] as const) {
      const directPidFile = join(fixture.root, `${signal}.direct.pid`);
      const descendantPidFile = join(fixture.root, `${signal}.descendant.pid`);
      const fake = executable(fixture.root, [
        `printf '%s' "$$" > '${directPidFile}'`,
        "sleep 31337 &",
        "descendant=$!",
        `printf '%s' "$descendant" > '${descendantPidFile}'`,
        "wait",
      ].join("\n"));
      const harnessFile = join(fixture.root, `${signal}.mjs`);
      writeFileSync(harnessFile, [
        `import { runTerraformCommand } from ${JSON.stringify(runnerUrl)};`,
        "await runTerraformCommand({",
        `  terraformExecutable: ${JSON.stringify(fake)},`,
        "  argv: [],",
        `  cwd: ${JSON.stringify(fixture.cwd)},`,
        "  environment: {},",
        "  limits: { timeoutMs: null, maxStdoutBytes: 65536, maxStderrBytes: 4096 },",
        '  output: "discard",',
        "});",
      ].join("\n"));
      const harness = spawn(process.execPath, [harnessFile], {
        env: { LANG: "C", LC_ALL: "C", TZ: "UTC" },
        stdio: "ignore",
      });
      const closed = new Promise<{
        readonly code: number | null;
        readonly signal: NodeJS.Signals | null;
      }>((resolve) => {
        harness.once("close", (code, observedSignal) => resolve({ code, signal: observedSignal }));
      });
      await Promise.all([waitForFile(directPidFile), waitForFile(descendantPidFile)]);
      const directPid = Number((await readFile(directPidFile, "utf8")).trim());
      const descendantPid = Number((await readFile(descendantPidFile, "utf8")).trim());
      assert.equal(Number.isSafeInteger(directPid) && directPid > 0, true);
      assert.equal(Number.isSafeInteger(descendantPid) && descendantPid > 0, true);
      const [directIdentity, descendantIdentity] = await Promise.all([
        linuxProcessIdentity(directPid),
        linuxProcessIdentity(descendantPid),
      ]);
      harness.kill(signal);
      assert.deepEqual(await closed, { code: null, signal });
      await Promise.all([
        waitForProcessExit(
          directPid,
          directIdentity.kind === "observed" ? directIdentity.startTime : undefined,
        ),
        waitForProcessExit(
          descendantPid,
          descendantIdentity.kind === "observed"
            ? descendantIdentity.startTime
            : undefined,
        ),
      ]);
    }
  });
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
            timeoutMs: 1.5,
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
