import assert from "node:assert/strict";
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";
import test from "node:test";

import { ZCC_ADOPTION_CATALOG_SHA256 } from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import { runZccAdoptionOracle } from "../node-src/domain/zcc-adoption-oracle.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  createZccAdoptionOracleAdapters,
  ZCC_ADOPTION_ORACLE_TRANSACTION_TIMEOUT_MS,
} from "../node-src/io/zcc-adoption-oracle-adapters.js";

interface Invocation {
  readonly argv: readonly string[];
  readonly cwd: string;
  readonly environment: Readonly<Record<string, string>>;
}

test("private oracle core and concrete adapter complete one exact transaction", async () => {
  const lexicalRoot = mkdtempSync(path.join(tmpdir(), "zcc-oracle-integration-"));
  chmodSync(lexicalRoot, 0o700);
  const root = realpathSync(lexicalRoot);
  const tempRoot = path.join(root, "private");
  const invocationLog = path.join(root, "invocations.jsonl");
  const fake = path.join(root, "terraform-fake");
  mkdirSync(tempRoot, { mode: 0o700 });
  const script = [
    `#!${process.execPath}`,
    'import { createHash } from "node:crypto";',
    'import { appendFileSync, readFileSync, writeFileSync } from "node:fs";',
    `const log = ${JSON.stringify(invocationLog)};`,
    "const argv = process.argv.slice(2);",
    "appendFileSync(log, JSON.stringify({ argv, cwd: process.cwd(), environment: process.env }) + '\\n');",
    "const imports = () => readFileSync('imports.tf', 'utf8');",
    "const identity = () => {",
    "  const text = imports();",
    "  const address = /(?:^|\\n)\\s*to = ([^\\n]+)/.exec(text)?.[1];",
    "  const importId = /(?:^|\\n)\\s*id = \\\"([^\\\"]+)\\\"/.exec(text)?.[1];",
    "  if (!address || !importId) process.exit(71);",
    "  return { address, importId };",
    "};",
    "if (argv[0] === 'init') {",
    "  const lock = readFileSync('.terraform.lock.hcl', 'utf8');",
    "  const lockSha = createHash('sha256').update(lock, 'utf8').digest('hex');",
    "  if (lockSha !== '9a097955041338130f344c525e10a3f34513eef307678df5e80abcf604ee60fa') process.exit(74);",
    "  const root = readFileSync('main.tf', 'utf8');",
    "  if (!root.includes('required_version = \"= 1.15.4\"')) process.exit(75);",
    "  process.exit(0);",
    "} else if (argv[0] === 'plan') {",
    "  const generated = argv.find((value) => value.startsWith('-generate-config-out='))?.slice(21);",
    "  const plan = argv.find((value) => value.startsWith('-out='))?.slice(5);",
    "  if (!generated || !plan) process.exit(72);",
    "  writeFileSync(generated, 'resource generated {}\\n');",
    "  writeFileSync(plan, 'opaque saved plan\\n');",
    "} else if (argv[0] === 'apply') {",
    "  writeFileSync('terraform.tfstate', 'opaque local state\\n');",
    "} else if (argv[0]?.startsWith('-chdir=') && argv[1] === 'show') {",
    "  const { address, importId } = identity();",
    "  const common = { address, mode: 'managed', type: 'zcc_web_privacy', provider_name: 'registry.terraform.io/zscaler/zcc' };",
    "  const snapshot = argv[3] ?? '';",
    "  const output = snapshot.endsWith('oracle.tfplan')",
    "    ? { format_version: '1.2', terraform_version: '1.15.4', applyable: true, complete: true, errored: false, resource_changes: [{ ...common, change: { actions: ['no-op'], importing: { id: importId } } }] }",
    "    : { format_version: '1.0', terraform_version: '1.15.4', values: { root_module: { resources: [{ ...common, values: { active: true, collect_user_info: false, id: importId }, sensitive_values: {} }] } } };",
    "  process.stdout.write(JSON.stringify(output));",
    "} else {",
    "  process.exit(73);",
    "}",
  ].join("\n");
  writeFileSync(fake, script, { mode: 0o700 });
  chmodSync(fake, 0o700);

  const poison = process.env.TF_CLI_ARGS;
  process.env.TF_CLI_ARGS = "-destroy";
  try {
    const result = await runZccAdoptionOracle({
      catalog: loadZccAdoptionCatalog(),
      catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
      rawItems: [{ id: "privacy-1" }],
      source: {
        path: "pulls/demo/zcc_web_privacy.json",
        sha256: "a".repeat(64),
        size_bytes: 1,
      },
      target: {
        tenant: "demo",
        resourceType: "zcc_web_privacy",
        rootLabel: "zcc_web_privacy",
        rootMembers: ["zcc_web_privacy"],
        variableName: "items",
        configPath: "config/demo/zcc_web_privacy.auto.tfvars.json",
        importsPath: "imports/demo/zcc_web_privacy_imports.tf",
        lookupPath: null,
      },
      terraformExecutable: fake,
    }, createZccAdoptionOracleAdapters({
      terraformExecutable: fake,
      tempRoot,
      environment: {},
    }));

    assert.equal(result.resource_type, "zcc_web_privacy");
    assert.equal(
      result.artifacts.tfvars.content,
      '{\n  "items": {\n    "privacy_1": {\n      "active": true,\n      "collect_user_info": false\n    }\n  }\n}\n',
    );
    assert.match(result.artifacts.imports.content, /id = "privacy-1"/);

    const invocations = readFileSync(invocationLog, "utf8")
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line) as Invocation);
    assert.deepEqual(invocations.map((entry) => entry.argv[0]), [
      "init",
      "plan",
      expectShowPrefix(invocations[2]),
      "apply",
      expectShowPrefix(invocations[4]),
    ]);
    assert.deepEqual(invocations[0]?.argv, [
      "init",
      "-backend=false",
      "-input=false",
      "-no-color",
      "-lockfile=readonly",
    ]);
    const transactionDirectories = new Set(invocations.map((entry) => entry.cwd));
    assert.equal(transactionDirectories.size, 1);
    const transactionDirectory = invocations[0]?.cwd;
    assert.equal(typeof transactionDirectory, "string");
    assert.equal(existsSync(transactionDirectory ?? ""), false);
    for (const invocation of invocations) {
      assert.equal("TF_CLI_ARGS" in invocation.environment, false);
      assert.equal(invocation.environment.TF_IN_AUTOMATION, "1");
      assert.equal(invocation.environment.LANG, "C");
      assert.equal(invocation.environment.LC_ALL, "C");
    }
  } finally {
    if (poison === undefined) {
      delete process.env.TF_CLI_ARGS;
    } else {
      process.env.TF_CLI_ARGS = poison;
    }
    rmSync(lexicalRoot, { force: true, recursive: true });
  }
});

test("core preserves timeout over simultaneous protection failure and still cleans up", async (t) => {
  const lexicalRoot = mkdtempSync(path.join(tmpdir(), "zcc-oracle-timeout-"));
  chmodSync(lexicalRoot, 0o700);
  const root = realpathSync(lexicalRoot);
  const tempRoot = path.join(root, "private");
  const invocationLog = path.join(root, "invocations.jsonl");
  const fake = path.join(root, "terraform-fake");
  mkdirSync(tempRoot, { mode: 0o700 });
  const script = [
    `#!${process.execPath}`,
    'import { appendFileSync, writeFileSync } from "node:fs";',
    `const log = ${JSON.stringify(invocationLog)};`,
    "const argv = process.argv.slice(2);",
    "appendFileSync(log, JSON.stringify({ argv, cwd: process.cwd(), environment: process.env }) + '\\n');",
    "if (argv[0] === 'init') {",
    "  writeFileSync('.terraform.lock.hcl', 'core-protection-secret');",
    "  setInterval(() => {}, 1000);",
    "} else {",
    "  process.exit(76);",
    "}",
  ].join("\n");
  writeFileSync(fake, script, { mode: 0o700 });
  chmodSync(fake, 0o700);

  let nowCalls = 0;
  t.mock.method(performance, "now", () => {
    nowCalls += 1;
    return nowCalls === 1
      ? 0
      : ZCC_ADOPTION_ORACLE_TRANSACTION_TIMEOUT_MS - 1_000;
  });
  try {
    let failure: unknown;
    try {
      await runZccAdoptionOracle({
        catalog: loadZccAdoptionCatalog(),
        catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
        rawItems: [{ id: "timeout-secret-id" }],
        source: {
          path: "pulls/demo/zcc_web_privacy.json",
          sha256: "b".repeat(64),
          size_bytes: 1,
        },
        target: {
          tenant: "demo",
          resourceType: "zcc_web_privacy",
          rootLabel: "zcc_web_privacy",
          rootMembers: ["zcc_web_privacy"],
          variableName: "items",
          configPath: "config/demo/zcc_web_privacy.auto.tfvars.json",
          importsPath: "imports/demo/zcc_web_privacy_imports.tf",
          lookupPath: null,
        },
        terraformExecutable: fake,
      }, createZccAdoptionOracleAdapters({
        terraformExecutable: fake,
        tempRoot,
        environment: {},
      }));
    } catch (error: unknown) {
      failure = error;
    }
    assert.ok(failure instanceof ProcessFailure);
    assert.equal(failure.code, "ZCC_ADOPTION_ORACLE_TIMEOUT");
    assert.deepEqual(failure.details, [{
      path: "protection",
      code: "ZCC_ORACLE_COMMAND_PROTECTION_FAILED",
      message: "protected files also changed around the timed-out Terraform command",
    }]);
    assert.equal(JSON.stringify(failure).includes("timeout-secret-id"), false);
    assert.equal(JSON.stringify(failure).includes("core-protection-secret"), false);
    assert.equal(JSON.stringify(failure).includes(tempRoot), false);

    const invocations = readFileSync(invocationLog, "utf8")
      .split("\n")
      .filter(Boolean)
      .map((line) => JSON.parse(line) as Invocation);
    assert.equal(invocations.length, 1);
    assert.equal(invocations[0]?.argv[0], "init");
    const transactionDirectory = invocations[0]?.cwd ?? "";
    assert.equal(existsSync(transactionDirectory), false);
    assert.equal(existsSync(tempRoot), true);
  } finally {
    rmSync(lexicalRoot, { force: true, recursive: true });
  }
});

function expectShowPrefix(invocation: Invocation | undefined): string {
  assert.notEqual(invocation, undefined);
  assert.match(invocation?.argv[0] ?? "", /^-chdir=/);
  assert.equal(invocation?.argv[1], "show");
  assert.equal(invocation?.argv[2], "-json");
  return invocation?.argv[0] ?? "";
}
