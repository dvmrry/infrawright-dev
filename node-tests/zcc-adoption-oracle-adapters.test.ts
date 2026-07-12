import assert from "node:assert/strict";
import {
  chmodSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  statSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import type {
  ZccAdoptionOracleCommandRequest,
  ZccAdoptionOracleShowRequest,
} from "../node-src/domain/zcc-adoption-oracle.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  createZccAdoptionOracleAdapters,
  DEFAULT_ZCC_ADOPTION_ORACLE_COMMAND_LIMITS,
  DEFAULT_ZCC_ADOPTION_ORACLE_SHOW_LIMITS,
  type ZccAdoptionOracleAdapterFactoryOptions,
} from "../node-src/io/zcc-adoption-oracle-adapters.js";

const PREFIX = "infrawright-zcc-oracle-";
const COMMAND_LIMITS = {
  timeoutMs: 2_000,
  maxStdoutBytes: 64 * 1024,
  maxStderrBytes: 4 * 1024,
} as const;
const SHOW_LIMITS = {
  timeoutMs: 2_000,
  maxStdoutBytes: 64 * 1024,
  maxStderrBytes: 4 * 1024,
} as const;

type FakeMode = "normal" | "nonzero" | "stdout" | "stderr" | "timeout";

interface Fixture {
  readonly fake: string;
  readonly invocationLog: string;
  readonly pluginCache: string;
  readonly root: string;
  readonly tempRoot: string;
}

interface ScratchPaths {
  readonly directory: string;
  readonly generatedConfig: string;
  readonly imports: string;
  readonly plan: string;
  readonly root: string;
  readonly state: string;
}

interface Invocation {
  readonly argv: string[];
  readonly cwd: string;
  readonly environment: Record<string, string>;
}

async function withFixture(
  mode: FakeMode,
  callback: (fixture: Fixture) => void | Promise<void>,
): Promise<void> {
  const lexicalRoot = mkdtempSync(path.join(tmpdir(), "zcc-oracle-adapters-"));
  chmodSync(lexicalRoot, 0o700);
  const root = realpathSync(lexicalRoot);
  const tempRoot = path.join(root, "private");
  const pluginCache = path.join(root, "plugin-cache");
  const invocationLog = path.join(root, "invocations.jsonl");
  mkdirSync(tempRoot, { mode: 0o700 });
  mkdirSync(pluginCache, { mode: 0o755 });
  const fake = path.join(root, "terraform-fake");
  const script = [
    `#!${process.execPath}`,
    'import { appendFileSync, chmodSync, writeFileSync } from "node:fs";',
    `const mode = ${JSON.stringify(mode)};`,
    `const log = ${JSON.stringify(invocationLog)};`,
    "const argv = process.argv.slice(2);",
    "appendFileSync(log, JSON.stringify({ argv, cwd: process.cwd(), environment: process.env }) + '\\n');",
    "const first = argv[0] ?? '';",
    "if (first === 'init' && mode === 'nonzero') {",
    "  process.stdout.write(process.env.ZSCALER_CLIENT_SECRET ?? 'missing');",
    "  process.stderr.write('nonzero-output-secret');",
    "  process.exit(29);",
    "}",
    "if (first === 'init' && mode === 'stdout') {",
    "  process.stdout.write('stdout-overflow-secret'.repeat(10000));",
    "  process.exit(0);",
    "}",
    "if (first === 'init' && mode === 'stderr') {",
    "  process.stderr.write('stderr-overflow-secret'.repeat(10000));",
    "  process.exit(0);",
    "}",
    "if (first === 'init' && mode === 'timeout') {",
    "  setInterval(() => {}, 1000);",
    "} else if (first === 'plan') {",
    "  const generated = argv.find((value) => value.startsWith('-generate-config-out='))?.slice(21);",
    "  const plan = argv.find((value) => value.startsWith('-out='))?.slice(5);",
    "  if (!generated || !plan) process.exit(31);",
    "  writeFileSync(generated, 'generated config bytes\\n', { mode: 0o666 });",
    "  writeFileSync(plan, 'opaque plan bytes\\n', { mode: 0o666 });",
    "  chmodSync(generated, 0o644); chmodSync(plan, 0o644);",
    "} else if (first === 'apply') {",
    "  writeFileSync('terraform.tfstate', 'opaque state bytes\\n', { mode: 0o666 });",
    "  chmodSync('terraform.tfstate', 0o644);",
    "} else if (first.startsWith('-chdir=') && argv[1] === 'show') {",
    "  process.stdout.write(JSON.stringify({ format_version: '1.2', complete: true, errored: false }));",
    "}",
  ].join("\n");
  writeFileSync(fake, script, { mode: 0o700 });
  chmodSync(fake, 0o700);
  try {
    await callback({ fake, invocationLog, pluginCache, root, tempRoot });
  } finally {
    rmSync(lexicalRoot, { force: true, recursive: true });
  }
}

function options(
  fixture: Fixture,
  overrides: Partial<ZccAdoptionOracleAdapterFactoryOptions> = {},
): ZccAdoptionOracleAdapterFactoryOptions {
  return {
    terraformExecutable: fixture.fake,
    tempRoot: fixture.tempRoot,
    environment: {
      ZSCALER_CLIENT_ID: "client-id-value",
      ZSCALER_CLIENT_SECRET: "provider-secret-value",
      ZSCALER_PRIVATE_KEY: "private-key-value",
      ZSCALER_VANITY_DOMAIN: "tenant-value",
      ZSCALER_CLOUD: "zscaler-value",
      HTTPS_PROXY: "https://proxy.invalid",
      NO_PROXY: "127.0.0.1",
    },
    pluginCacheDirectory: fixture.pluginCache,
    commandLimits: COMMAND_LIMITS,
    showLimits: SHOW_LIMITS,
    ...overrides,
  };
}

function pathsFor(directory: string): ScratchPaths {
  return {
    directory,
    generatedConfig: path.join(directory, "generated.tf"),
    imports: path.join(directory, "imports.tf"),
    plan: path.join(directory, "oracle.tfplan"),
    root: path.join(directory, "main.tf"),
    state: path.join(directory, "terraform.tfstate"),
  };
}

async function begin(
  fixture: Fixture,
  overrides: Partial<ZccAdoptionOracleAdapterFactoryOptions> = {},
) {
  const adapters = createZccAdoptionOracleAdapters(options(fixture, overrides));
  const directory = await adapters.temporary.create(PREFIX);
  const paths = pathsFor(directory);
  await adapters.files.writeText({
    path: paths.root,
    content: "terraform {}\n",
    mode: 0o600,
  });
  await adapters.files.writeText({
    path: paths.imports,
    content: 'import { id = "sensitive-import-id" }\n',
    mode: 0o600,
  });
  return { adapters, paths };
}

function commandRequest(
  fixture: Fixture,
  paths: ScratchPaths,
  stage: ZccAdoptionOracleCommandRequest["stage"],
): ZccAdoptionOracleCommandRequest {
  const argv = stage === "init"
    ? ["init", "-input=false", "-no-color"]
    : stage === "plan"
      ? [
          "plan",
          "-input=false",
          "-no-color",
          "-lock=false",
          `-generate-config-out=${paths.generatedConfig}`,
          `-out=${paths.plan}`,
        ]
      : [
          "apply",
          "-input=false",
          "-no-color",
          "-lock=false",
          paths.plan,
        ];
  const protectedPaths = stage === "init" || stage === "plan"
    ? [paths.root, paths.imports]
    : [paths.root, paths.imports, paths.generatedConfig, paths.plan];
  return {
    stage,
    executable: fixture.fake,
    cwd: paths.directory,
    argv,
    sensitiveTokens: stage === "init" ? [] : ["sensitive-import-id"],
    protectedPaths,
  };
}

function showRequest(
  fixture: Fixture,
  paths: ScratchPaths,
  stage: ZccAdoptionOracleShowRequest["stage"],
): ZccAdoptionOracleShowRequest {
  const snapshotPath = stage === "show-plan" ? paths.plan : paths.state;
  return {
    stage,
    executable: fixture.fake,
    cwd: paths.directory,
    argv: ["show", "-json", snapshotPath],
    snapshotPath,
    sensitiveTokens: ["sensitive-import-id"],
    protectedPaths: stage === "show-plan"
      ? [paths.root, paths.imports, paths.generatedConfig, paths.plan]
      : [
          paths.root,
          paths.imports,
          paths.generatedConfig,
          paths.plan,
          paths.state,
        ],
  };
}

async function captureFailure(
  promise: Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  try {
    await promise;
  } catch (error: unknown) {
    assert.ok(error instanceof ProcessFailure);
    assert.equal(error.code, code);
    return error;
  }
  assert.fail(`expected ${code}`);
}

function invocationRecords(fixture: Fixture): Invocation[] {
  return readFileSync(fixture.invocationLog, "utf8")
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line) as Invocation);
}

function observedEnvironment(value: Record<string, string>): Record<string, string> {
  const result = { ...value };
  if (process.platform === "darwin") {
    delete result.__CF_USER_TEXT_ENCODING;
  }
  return result;
}

test("runs exact argv in one private transaction with a complete stripped environment", async () => {
  await withFixture("normal", async (fixture) => {
    const poison = {
      TF_CLI_ARGS: process.env.TF_CLI_ARGS,
      TF_TOKEN_app_terraform_io: process.env.TF_TOKEN_app_terraform_io,
      UNRELATED_SECRET: process.env.UNRELATED_SECRET,
    };
    process.env.TF_CLI_ARGS = "-destroy";
    process.env.TF_TOKEN_app_terraform_io = "registry-poison";
    process.env.UNRELATED_SECRET = "parent-poison";
    try {
      const { adapters, paths } = await begin(fixture);
      assert.equal(lstatSync(paths.directory).mode & 0o777, 0o700);
      assert.equal(lstatSync(paths.root).mode & 0o777, 0o600);
      assert.equal(lstatSync(paths.imports).mode & 0o777, 0o600);

      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      const plan = await adapters.show.readJson(
        showRequest(fixture, paths, "show-plan"),
      );
      assert.deepEqual(plan, {
        complete: true,
        errored: false,
        format_version: "1.2",
      });
      assert.equal(lstatSync(paths.generatedConfig).mode & 0o777, 0o600);
      assert.equal(lstatSync(paths.plan).mode & 0o777, 0o600);
      await adapters.command.run(commandRequest(fixture, paths, "apply"));
      await adapters.show.readJson(showRequest(fixture, paths, "show-state"));
      assert.equal(lstatSync(paths.state).mode & 0o777, 0o600);

      const records = invocationRecords(fixture);
      assert.deepEqual(records.map((record) => record.argv), [
        ["init", "-input=false", "-no-color"],
        [
          "plan",
          "-input=false",
          "-no-color",
          "-lock=false",
          `-generate-config-out=${paths.generatedConfig}`,
          `-out=${paths.plan}`,
        ],
        [`-chdir=${paths.directory}`, "show", "-json", paths.plan],
        ["apply", "-input=false", "-no-color", "-lock=false", paths.plan],
        [`-chdir=${paths.directory}`, "show", "-json", paths.state],
      ]);
      const expectedEnvironment = {
        ZSCALER_CLIENT_ID: "client-id-value",
        ZSCALER_CLIENT_SECRET: "provider-secret-value",
        ZSCALER_PRIVATE_KEY: "private-key-value",
        ZSCALER_VANITY_DOMAIN: "tenant-value",
        ZSCALER_CLOUD: "zscaler-value",
        HTTPS_PROXY: "https://proxy.invalid",
        NO_PROXY: "127.0.0.1",
        CHECKPOINT_DISABLE: "1",
        LANG: "C",
        LC_ALL: "C",
        TF_IN_AUTOMATION: "1",
        HOME: path.join(paths.directory, ".home"),
        TMPDIR: path.join(paths.directory, ".tmp"),
        TF_DATA_DIR: path.join(paths.directory, ".terraform-data"),
        TF_PLUGIN_CACHE_DIR: fixture.pluginCache,
      };
      for (const record of records) {
        assert.equal(record.cwd, paths.directory);
        assert.deepEqual(observedEnvironment(record.environment), expectedEnvironment);
      }
      for (const privateDirectory of [
        expectedEnvironment.HOME,
        expectedEnvironment.TMPDIR,
        expectedEnvironment.TF_DATA_DIR,
      ]) {
        assert.equal(lstatSync(privateDirectory).mode & 0o777, 0o700);
      }

      await adapters.temporary.remove(paths.directory);
      assert.equal(lstatSync(fixture.tempRoot).isDirectory(), true);
      assert.equal(statSync(fixture.tempRoot).mode & 0o077, 0);
      assert.equal(
        (() => {
          try {
            lstatSync(paths.directory);
            return false;
          } catch {
            return true;
          }
        })(),
        true,
      );
      await captureFailure(
        adapters.temporary.create(PREFIX),
        "ZCC_ORACLE_ADAPTER_ALREADY_USED",
      );
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

test("binds plan and state snapshots and rejects later same-inode mutation", async () => {
  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      writeFileSync(paths.plan, "mutated plan bytes\n", { mode: 0o600 });
      await captureFailure(
        adapters.show.readJson(showRequest(fixture, paths, "show-plan")),
        "ZCC_ORACLE_FILE_CHANGED",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });

  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      await adapters.show.readJson(showRequest(fixture, paths, "show-plan"));
      await adapters.command.run(commandRequest(fixture, paths, "apply"));
      writeFileSync(paths.state, "mutated state bytes\n", { mode: 0o600 });
      await captureFailure(
        adapters.show.readJson(showRequest(fixture, paths, "show-state")),
        "ZCC_ORACLE_FILE_CHANGED",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });
});

test("show never establishes trust for outputs not bound by their producing stage", async () => {
  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      writeFileSync(paths.generatedConfig, "untrusted generated bytes\n", {
        mode: 0o600,
      });
      writeFileSync(paths.plan, "untrusted plan bytes\n", { mode: 0o600 });
      await captureFailure(
        adapters.show.readJson(showRequest(fixture, paths, "show-plan")),
        "UNBOUND_ZCC_ORACLE_FILE",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });

  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      await adapters.show.readJson(showRequest(fixture, paths, "show-plan"));
      writeFileSync(paths.state, "untrusted state bytes\n", { mode: 0o600 });
      await captureFailure(
        adapters.show.readJson(showRequest(fixture, paths, "show-state")),
        "UNBOUND_ZCC_ORACLE_FILE",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });
});

test("producing stages reject outputs that already exist", async () => {
  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      writeFileSync(paths.generatedConfig, "preexisting generated bytes\n", {
        mode: 0o600,
      });
      await captureFailure(
        adapters.command.run(commandRequest(fixture, paths, "plan")),
        "ZCC_ORACLE_OUTPUT_PREEXISTS",
      );
      assert.deepEqual(
        invocationRecords(fixture).map((record) => record.argv[0]),
        ["init"],
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });

  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      await adapters.show.readJson(showRequest(fixture, paths, "show-plan"));
      writeFileSync(paths.state, "preexisting state bytes\n", { mode: 0o600 });
      await captureFailure(
        adapters.command.run(commandRequest(fixture, paths, "apply")),
        "ZCC_ORACLE_OUTPUT_PREEXISTS",
      );
      assert.equal(
        invocationRecords(fixture).some((record) => record.argv[0] === "apply"),
        false,
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });
});

test("rejects scratch mutation, symlinks, and direct or protected path escape", async () => {
  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    try {
      writeFileSync(paths.root, "changed root\n", { mode: 0o600 });
      await captureFailure(
        adapters.command.run(commandRequest(fixture, paths, "init")),
        "ZCC_ORACLE_FILE_CHANGED",
      );
      await captureFailure(
        adapters.files.writeText({
          path: path.join(fixture.root, "escape.tf"),
          content: "secret",
          mode: 0o600,
        }),
        "INVALID_ZCC_ORACLE_WRITE",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });

  await withFixture("normal", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    const external = path.join(fixture.root, "external-plan");
    writeFileSync(external, "outside\n", { mode: 0o644 });
    try {
      await adapters.command.run(commandRequest(fixture, paths, "init"));
      await adapters.command.run(commandRequest(fixture, paths, "plan"));
      unlinkSync(paths.plan);
      symlinkSync(external, paths.plan);
      await captureFailure(
        adapters.show.readJson(showRequest(fixture, paths, "show-plan")),
        "UNSAFE_ZCC_ORACLE_FILE",
      );
      assert.equal(lstatSync(external).mode & 0o777, 0o644);

      const escaped = commandRequest(fixture, paths, "init");
      await captureFailure(
        adapters.command.run({
          ...escaped,
          protectedPaths: [paths.root, path.join(fixture.root, "escape.tf")],
        }),
        "INVALID_ZCC_ORACLE_PROTECTED_PATHS",
      );
    } finally {
      await adapters.temporary.remove(paths.directory);
    }
  });
});

test("rejects non-private or symlinked temp authorities and caller TF poisoning", async () => {
  await withFixture("normal", async (fixture) => {
    chmodSync(fixture.tempRoot, 0o750);
    const permissions = createZccAdoptionOracleAdapters(options(fixture));
    await captureFailure(
      permissions.temporary.create(PREFIX),
      "UNSAFE_ZCC_ORACLE_DIRECTORY",
    );

    chmodSync(fixture.tempRoot, 0o700);
    const linkedRoot = path.join(fixture.root, "private-link");
    symlinkSync(fixture.tempRoot, linkedRoot);
    const symlinked = createZccAdoptionOracleAdapters(
      options(fixture, { tempRoot: linkedRoot }),
    );
    await captureFailure(
      symlinked.temporary.create(PREFIX),
      "UNSAFE_ZCC_ORACLE_DIRECTORY",
    );

    assert.throws(
      () => createZccAdoptionOracleAdapters(
        options(fixture, {
          environment: { TF_TOKEN_app_terraform_io: "forbidden-secret" },
        }),
      ),
      (error: unknown) => {
        assert.ok(error instanceof ProcessFailure);
        assert.equal(error.code, "INVALID_ZCC_ORACLE_ADAPTER_ENVIRONMENT");
        assert.equal(JSON.stringify(error).includes("forbidden-secret"), false);
        return true;
      },
    );
  });
});

test("timeout, output limits, and nonzero exits preserve value-safe diagnostics", async (t) => {
  const cases = [
    {
      mode: "nonzero" as const,
      code: "TERRAFORM_COMMAND_FAILED",
      commandLimits: COMMAND_LIMITS,
    },
    {
      mode: "stdout" as const,
      code: "TERRAFORM_COMMAND_STDOUT_LIMIT",
      commandLimits: { ...COMMAND_LIMITS, maxStdoutBytes: 32 },
    },
    {
      mode: "stderr" as const,
      code: "TERRAFORM_COMMAND_STDERR_LIMIT",
      commandLimits: { ...COMMAND_LIMITS, maxStderrBytes: 32 },
    },
    {
      mode: "timeout" as const,
      code: "TERRAFORM_COMMAND_TIMEOUT",
      commandLimits: { ...COMMAND_LIMITS, timeoutMs: 75 },
    },
  ];
  for (const item of cases) {
    await t.test(item.mode, async () => {
      await withFixture(item.mode, async (fixture) => {
        const { adapters, paths } = await begin(fixture, {
          commandLimits: item.commandLimits,
        });
        try {
          const failure = await captureFailure(
            adapters.command.run(commandRequest(fixture, paths, "init")),
            item.code,
          );
          const diagnostic = [
            String(failure),
            JSON.stringify(failure),
            failure.stack ?? "",
          ].join("\n");
          for (const secret of [
            "provider-secret-value",
            "private-key-value",
            "sensitive-import-id",
            "nonzero-output-secret",
            "stdout-overflow-secret",
            "stderr-overflow-secret",
            fixture.tempRoot,
          ]) {
            assert.equal(diagnostic.includes(secret), false);
          }
        } finally {
          await adapters.temporary.remove(paths.directory);
        }
      });
    });
  }
});

test("private oracle timeouts match Python while callers may only tighten them", async () => {
  assert.equal(DEFAULT_ZCC_ADOPTION_ORACLE_COMMAND_LIMITS.timeoutMs, 300_000);
  assert.equal(DEFAULT_ZCC_ADOPTION_ORACLE_SHOW_LIMITS.timeoutMs, 300_000);

  await withFixture("normal", async (fixture) => {
    assert.doesNotThrow(() => createZccAdoptionOracleAdapters(options(fixture, {
      commandLimits: DEFAULT_ZCC_ADOPTION_ORACLE_COMMAND_LIMITS,
      showLimits: DEFAULT_ZCC_ADOPTION_ORACLE_SHOW_LIMITS,
    })));

    for (const [field, limits] of [
      ["commandLimits", {
        ...DEFAULT_ZCC_ADOPTION_ORACLE_COMMAND_LIMITS,
        timeoutMs: 300_001,
      }],
      ["showLimits", {
        ...DEFAULT_ZCC_ADOPTION_ORACLE_SHOW_LIMITS,
        timeoutMs: 300_001,
      }],
    ] as const) {
      assert.throws(
        () => createZccAdoptionOracleAdapters(options(fixture, {
          [field]: limits,
        })),
        (error: unknown) => {
          assert.ok(error instanceof ProcessFailure);
          assert.equal(error.code, "INVALID_ZCC_ORACLE_ADAPTER_LIMITS");
          return true;
        },
      );
    }
  });
});

test("cleanup removes the tree after a failed command and adapters remain spent", async () => {
  await withFixture("nonzero", async (fixture) => {
    const { adapters, paths } = await begin(fixture);
    await captureFailure(
      adapters.command.run(commandRequest(fixture, paths, "init")),
      "TERRAFORM_COMMAND_FAILED",
    );
    await adapters.temporary.remove(paths.directory);
    await assert.rejects(readFile(paths.root), { code: "ENOENT" });
    await captureFailure(
      adapters.command.run(commandRequest(fixture, paths, "init")),
      "ZCC_ORACLE_ADAPTER_ALREADY_USED",
    );
  });
});
