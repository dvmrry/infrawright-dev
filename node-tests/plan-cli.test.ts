import assert from "node:assert/strict";
import { spawnSync, type SpawnSyncReturns } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { chmod, cp, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { assertCliFailureExtendsLegacy } from "./cli-failure-assertions.js";

const ROOT = process.cwd();
const PROFILE = path.join(ROOT, "packsets", "zia.json");
const CATALOG = path.join(ROOT, "packsets", "full.json");
const FIRST = "zia_admin_users";
const SECOND = "zia_url_categories";
const LABEL = "zia_pair";
const AUTHORITY_SHA256 = "1aa9b78c6c00c569d6d285dd88aba748c22c5d55cd3979ea40bb28150c726681";
const PLAN_CLI_ROOT = "<PLAN_CLI_ROOT>";

interface FrozenBytes {
  readonly base64: string;
  readonly sha256: string;
  readonly size: number;
}

interface FrozenRecord {
  readonly arguments: readonly string[];
  readonly exit_status: number;
  readonly stderr: FrozenBytes;
  readonly stdout: FrozenBytes;
}

interface FrozenAuthority {
  readonly kind: string;
  readonly record_count: number;
  readonly records: readonly FrozenRecord[];
  readonly schema_version: number;
  readonly suite: string;
}

const authorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-plan-cli-v1.json",
));
assert.equal(
  createHash("sha256").update(authorityBytes).digest("hex"),
  AUTHORITY_SHA256,
  "frozen CPython plan CLI authority changed without re-adjudication",
);
const authority = JSON.parse(authorityBytes.toString("utf8")) as FrozenAuthority;
assert.equal(authority.kind, "python-engine-ops-delegation-authority");
assert.equal(authority.schema_version, 1);
assert.equal(authority.suite, "plan-cli");
assert.equal(authority.record_count, 9);
assert.equal(authority.records.length, authority.record_count);

const EXPECTED_AUTHORITY_ARGUMENTS = [
  ["-m", "engine.ops", "resources", "--order=references", SECOND],
  ["-m", "engine.ops", "roots", "--json", "--tenant", "tenant", SECOND],
  ["-m", "engine.ops", "plan-roots", "--json", "--tenant", "tenant", SECOND],
  ["-m", "engine.ops", "scope-paths", "--json", "--paths-json", "-"],
  ["-m", "engine.ops", "roots", "--json", "unknown_resource"],
  ["-m", "engine.ops", "plan-roots", "--json", "--tenant", "../bad"],
  [
    "-m",
    "engine.ops",
    "scope-paths",
    "--json",
    "--paths-json",
    `${PLAN_CLI_ROOT}/invalid-paths.json`,
  ],
  ["-m", "engine.ops", "roots", "--json"],
  ["-m", "engine.ops", "roots", "--json"],
] as const;
assert.deepEqual(
  authority.records.map((record) => record.arguments),
  EXPECTED_AUTHORITY_ARGUMENTS,
  "frozen plan CLI invocations changed order or identity",
);

function normalizeAuthorityPaths(value: string, workspace: string): string {
  const replacements = [[workspace, PLAN_CLI_ROOT]] as const;
  return [...replacements]
    .sort(([left], [right]) => right.length - left.length)
    .reduce((normalized, [from, to]) => normalized.replaceAll(from, to), value);
}

function frozenText(value: FrozenBytes): string {
  const bytes = Buffer.from(value.base64, "base64");
  assert.equal(bytes.length, value.size);
  assert.equal(createHash("sha256").update(bytes).digest("hex"), value.sha256);
  return bytes.toString("utf8");
}

function frozenRecord(index: number): FrozenRecord {
  const record = authority.records[index];
  assert.ok(record, `missing frozen plan CLI record ${index}`);
  return record;
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-plan-cli-"));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeText(file: string, text: string): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, text, "utf8");
}

async function reducedZiaRoot(parent: string): Promise<string> {
  const root = path.join(parent, "packs");
  await mkdir(path.join(root, "_shared"), { recursive: true });
  await cp(path.join(ROOT, "packs", "zia"), path.join(root, "zia"), { recursive: true });
  await cp(
    path.join(ROOT, "packs", "_shared", "zscaler"),
    path.join(root, "_shared", "zscaler"),
    { recursive: true },
  );
  return root;
}

function command(
  executable: string,
  arguments_: readonly string[],
  environment: NodeJS.ProcessEnv,
  input?: string,
): SpawnSyncReturns<string> {
  return spawnSync(executable, [...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
    env: environment,
    ...(input === undefined ? {} : { input }),
  });
}

test("real metadata CLI query bytes match Python on grouped materialized roots", async (context) => {
  const workspace = await temporaryDirectory(context);
  const packs = await reducedZiaRoot(workspace);
  const deployment = path.join(workspace, "deployment.json");
  await writeText(deployment, `${JSON.stringify({
    overlay: workspace,
    roots: { zia: { groups: { [LABEL]: [FIRST, SECOND] } } },
  }, null, 2)}\n`);
  await mkdir(path.join(workspace, "envs", "tenant", LABEL), { recursive: true });
  await writeText(path.join(workspace, "envs", "tenant", LABEL, "tfplan"), "opaque");

  const environment = {
    ...process.env,
    INFRAWRIGHT_DEPLOYMENT: deployment,
    INFRAWRIGHT_PACKS: packs,
  };
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const built = command(process.execPath, ["scripts/build-metadata-cli.mjs"], environment);
  assert.equal(built.status, 0, built.stderr);

  const cases = [
    {
      authorityIndex: 0,
      node: [cli, "resources", "--order=references", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG],
    },
    {
      authorityIndex: 1,
      node: [cli, "roots", "--tenant", "tenant", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    },
    {
      authorityIndex: 2,
      node: [cli, "plan-roots", "--tenant", "tenant", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    },
  ];
  for (const item of cases) {
    const frozen = frozenRecord(item.authorityIndex);
    const node = command(process.execPath, item.node, environment);
    assert.equal(frozen.exit_status, 0, frozenText(frozen.stderr));
    assert.equal(node.status, frozen.exit_status, node.stderr);
    assert.equal(
      normalizeAuthorityPaths(node.stdout, workspace),
      frozenText(frozen.stdout),
      item.node[1],
    );
    assert.equal(
      normalizeAuthorityPaths(node.stderr, workspace),
      frozenText(frozen.stderr),
      item.node[1],
    );
  }

  const changed = JSON.stringify([
    deployment,
    path.join(workspace, "config", "tenant", `${SECOND}.auto.tfvars.json`),
    path.join(workspace, "envs", "tenant", LABEL, "main.tf"),
  ]);
  const frozenScope = frozenRecord(3);
  const nodeScope = command(
    process.execPath,
    [cli, "scope-paths", "--paths-json", "-", "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    environment,
    changed,
  );
  assert.equal(frozenScope.exit_status, 0, frozenText(frozenScope.stderr));
  assert.equal(nodeScope.status, frozenScope.exit_status, nodeScope.stderr);
  assert.equal(
    normalizeAuthorityPaths(nodeScope.stdout, workspace),
    frozenText(frozenScope.stdout),
  );
  assert.equal(
    normalizeAuthorityPaths(nodeScope.stderr, workspace),
    frozenText(frozenScope.stderr),
  );
});

test("query validation and changed-path file failures retain legacy status classes", async (context) => {
  const workspace = await temporaryDirectory(context);
  const packs = await reducedZiaRoot(workspace);
  const deployment = path.join(workspace, "deployment.json");
  await writeText(deployment, `${JSON.stringify({ overlay: workspace, roots: {} }, null, 2)}\n`);
  const environment = {
    ...process.env,
    INFRAWRIGHT_DEPLOYMENT: deployment,
    INFRAWRIGHT_PACKS: packs,
  };
  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const built = command(process.execPath, ["scripts/build-metadata-cli.mjs"], environment);
  assert.equal(built.status, 0, built.stderr);
  const common = [
    "--root", packs,
    "--profile", PROFILE,
    "--catalog", CATALOG,
    "--deployment", deployment,
  ];
  const invalidPaths = path.join(workspace, "invalid-paths.json");
  await writeText(invalidPaths, '[""]\n');
  const validationCases = [
    {
      authorityIndex: 4,
      node: [cli, "roots", "--resource", "unknown_resource", ...common],
    },
    {
      authorityIndex: 5,
      node: [cli, "plan-roots", "--tenant", "../bad", ...common],
    },
    {
      authorityIndex: 6,
      node: [cli, "scope-paths", "--paths-json", invalidPaths, ...common],
    },
  ];
  for (const item of validationCases) {
    const frozen = frozenRecord(item.authorityIndex);
    const node = command(process.execPath, item.node, environment);
    assert.equal(node.status, frozen.exit_status, item.node[1]);
    assert.equal(
      normalizeAuthorityPaths(node.stdout, workspace),
      frozenText(frozen.stdout),
      item.node[1],
    );
    assert.equal(
      normalizeAuthorityPaths(node.stderr, workspace),
      frozenText(frozen.stderr),
      item.node[1],
    );
    assert.equal(node.status, 2, item.node[1]);
  }

  const invalidDeployment = path.join(workspace, "invalid-deployment.json");
  await writeText(invalidDeployment, '{"overlay":".","roots":[]}\n');
  const invalidRoot = path.join(workspace, "invalid-root.json");
  await writeText(invalidRoot, `${JSON.stringify({
    overlay: ".",
    roots: { zia: { groups: { bad_group: ["zia_not_real"] } } },
  })}\n`);
  for (const [authorityIndex, selected] of [
    [7, invalidDeployment],
    [8, invalidRoot],
  ] as const) {
    const selectedEnvironment = {
      ...environment,
      INFRAWRIGHT_DEPLOYMENT: selected,
    };
    const frozen = frozenRecord(authorityIndex);
    const node = command(
      process.execPath,
      [
        cli,
        "roots",
        "--root", packs,
        "--profile", PROFILE,
        "--catalog", CATALOG,
        "--deployment", selected,
      ],
      selectedEnvironment,
    );
    assert.equal(frozen.exit_status, 2, frozenText(frozen.stderr));
    assert.equal(node.status, frozen.exit_status, node.stderr);
    assert.equal(
      normalizeAuthorityPaths(node.stdout, workspace),
      frozenText(frozen.stdout),
    );
  }

  const malformed = path.join(workspace, "malformed.json");
  await writeText(malformed, "{bad\n");
  const malformedResult = command(
    process.execPath,
    [cli, "scope-paths", "--paths-json", malformed, ...common],
    environment,
  );
  assert.equal(malformedResult.status, 2);
  assert.equal(malformedResult.stdout, "");
  assert.match(malformedResult.stderr, /must contain a JSON array of changed paths/u);

  const missing = path.join(workspace, "missing.json");
  const missingResult = command(
    process.execPath,
    [cli, "scope-paths", "--paths-json", missing, ...common],
    environment,
  );
  assert.equal(missingResult.status, 1);
  assert.equal(missingResult.stdout, "");
  assert.match(missingResult.stderr, /ENOENT|no such file or directory/iu);
  assert.equal(missingResult.stderr.includes("must contain a JSON array"), false);
});

test("unsupported Windows plan refuses before preflight and preserves the saved pair", async (context) => {
  const workspace = await temporaryDirectory(context);
  const envRoot = path.join(workspace, "envs", "tenant", SECOND);
  const plan = path.join(envRoot, "tfplan");
  const fingerprint = path.join(envRoot, "tfplan.sources");
  await writeText(plan, "existing saved plan\n");
  await writeText(fingerprint, "existing fingerprint\n");

  const cli = path.join(ROOT, "dist", "infrawright-cli.mjs");
  const built = command(process.execPath, ["scripts/build-metadata-cli.mjs"], process.env);
  assert.equal(built.status, 0, built.stderr);
  const cliArguments = [
    "plan",
    "--tenant", "tenant",
    "--resource", SECOND,
    "--save",
    "--terraform", "--help",
    "--deployment", path.join(workspace, "missing-deployment.json"),
    "--root", path.join(workspace, "missing-packs"),
    "--profile", path.join(workspace, "missing-profile.json"),
    "--catalog", path.join(workspace, "missing-catalog.json"),
  ];
  const bootstrap = [
    "Object.defineProperty(process, 'platform', { configurable: true, value: 'win32' });",
    `process.argv = ${JSON.stringify([process.execPath, cli, ...cliArguments])};`,
    `await import(${JSON.stringify(pathToFileURL(cli).href)});`,
  ].join("\n");
  const result = command(
    process.execPath,
    ["--input-type=module", "--eval", bootstrap],
    process.env,
  );
  assert.equal(result.status, 1);
  assert.equal(result.stdout, "");
  assertCliFailureExtendsLegacy(
    result.stderr,
    "error: Terraform execution through Infrawright is supported on Linux and macOS; "
      + "Windows is not a supported operational platform.\n",
    {
      category: "domain",
      code: "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM",
      retryable: false,
    },
  );
  assert.equal(await readFile(plan, "utf8"), "existing saved plan\n");
  assert.equal(await readFile(fingerprint, "utf8"), "existing fingerprint\n");

  const helpBootstrap = [
    "Object.defineProperty(process, 'platform', { configurable: true, value: 'win32' });",
    `process.argv = ${JSON.stringify([process.execPath, cli, "plan", "--help"])};`,
    `await import(${JSON.stringify(pathToFileURL(cli).href)});`,
  ].join("\n");
  const help = command(
    process.execPath,
    ["--input-type=module", "--eval", helpBootstrap],
    process.env,
  );
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /^usage:\n/u);
});

test("all Slice-1 Make targets run with Python unavailable and fake Terraform", async (context) => {
  const workspace = await temporaryDirectory(context);
  const packs = await reducedZiaRoot(workspace);
  const deployment = path.join(workspace, "deployment.json");
  await writeText(deployment, `${JSON.stringify({
    overlay: workspace,
    roots: { zia: { groups: { [LABEL]: [FIRST, SECOND] } } },
  }, null, 2)}\n`);
  const envRoot = path.join(workspace, "envs", "tenant", LABEL);
  const moduleRoot = path.join(workspace, "modules");
  const main: string[] = [];
  for (const resourceType of [FIRST, SECOND]) {
    const moduleDirectory = path.join(moduleRoot, resourceType);
    await writeText(path.join(moduleDirectory, "main.tf"), "# fixture module\n");
    main.push(
      `module "${resourceType}" {`,
      `  source = "${path.relative(envRoot, moduleDirectory)}"`,
      `  items = var.${resourceType}_items`,
      "}",
      "",
    );
    await writeText(
      path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`),
      `{"${resourceType}_items":{}}\n`,
    );
  }
  await writeText(path.join(envRoot, "main.tf"), `${main.join("\n")}\n`);
  await writeText(path.join(envRoot, "unrelated.txt"), "keep\n");
  const paths = path.join(workspace, "paths.json");
  await writeText(paths, `${JSON.stringify([deployment])}\n`);

  const terraform = path.join(workspace, "terraform-fake");
  const terraformLog = path.join(workspace, "terraform.log");
  await writeText(terraform, [
    "#!/bin/sh",
    "printf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$FAKE_TF_LOG\"",
    "if [ \"$1\" = \"plan\" ]; then",
    "  printf '%s\\n' 'fake visible plan'",
    "  for arg in \"$@\"; do",
    "    case \"$arg\" in -out=*) printf '%s' 'opaque-plan' > \"${arg#-out=}\";; esac",
    "  done",
    "fi",
    "exit 0",
    "",
  ].join("\n"));
  await chmod(terraform, 0o700);

  const environment = {
    ...process.env,
    FAKE_TF_LOG: terraformLog,
    INFRAWRIGHT_DEPLOYMENT: deployment,
    INFRAWRIGHT_PACKS: packs,
  };
  const common = [
    `DEPLOYMENT=${deployment}`,
    `PACK_PROFILE=${PROFILE}`,
    `PACK_CATALOG=${CATALOG}`,
    "PYTHON=/python-must-not-run",
  ];
  const invocations = [
    ["roots", ...common, "TENANT=tenant", `RESOURCE=${SECOND}`],
    ["scope-paths", ...common, `PATHS_JSON=${paths}`],
    ["plan-roots", ...common, "TENANT=tenant", `RESOURCE=${SECOND}`],
    ["plan", ...common, "TENANT=tenant", `RESOURCE=${SECOND}`, "SAVE=1", `TF=${terraform}`],
  ];
  for (const arguments_ of invocations) {
    const result = command("make", arguments_, environment);
    assert.equal(result.status, 0, `${arguments_[0]}\n${result.stdout}\n${result.stderr}`);
    assert.equal(`${result.stdout}${result.stderr}`.includes("python-must-not-run"), false);
  }
  const calls = await readFile(terraformLog, "utf8");
  assert.match(calls, /init -input=false/u);
  assert.match(calls, new RegExp(
    `plan -input=false -var-file=${configPathPattern(workspace, FIRST)} `
      + `-var-file=${configPathPattern(workspace, SECOND)} -out=tfplan`,
    "u",
  ));
  assert.equal(await readFile(path.join(envRoot, "tfplan"), "utf8"), "opaque-plan");
  assert.match(await readFile(path.join(envRoot, "tfplan.sources"), "utf8"), /"version": 2/u);
  if (process.platform !== "win32") {
    assert.equal((await stat(path.join(envRoot, "tfplan"))).mode & 0o777, 0o600);
  }

  const cleaned = command("make", [
    "clean-plans",
    ...common,
    "TENANT=tenant",
    `RESOURCE=${SECOND}`,
  ], environment);
  assert.equal(cleaned.status, 0, `${cleaned.stdout}\n${cleaned.stderr}`);
  assert.equal(`${cleaned.stdout}${cleaned.stderr}`.includes("python-must-not-run"), false);
  await assert.rejects(readFile(path.join(envRoot, "tfplan")));
  await assert.rejects(readFile(path.join(envRoot, "tfplan.sources")));
  assert.equal(await readFile(path.join(envRoot, "unrelated.txt"), "utf8"), "keep\n");
});

function configPathPattern(workspace: string, resourceType: string): string {
  return path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`)
    .replaceAll(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}
