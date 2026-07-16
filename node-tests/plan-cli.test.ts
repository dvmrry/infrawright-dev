import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync, type SpawnSyncReturns } from "node:child_process";
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
      python: ["-m", "engine.ops", "resources", "--order=references", SECOND],
      node: [cli, "resources", "--order=references", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG],
    },
    {
      python: ["-m", "engine.ops", "roots", "--json", "--tenant", "tenant", SECOND],
      node: [cli, "roots", "--tenant", "tenant", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    },
    {
      python: ["-m", "engine.ops", "plan-roots", "--json", "--tenant", "tenant", SECOND],
      node: [cli, "plan-roots", "--tenant", "tenant", "--resource", SECOND, "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    },
  ];
  for (const item of cases) {
    const python = command(PYTHON_ORACLE, item.python, environment);
    const node = command(process.execPath, item.node, environment);
    assert.equal(python.status, 0, python.stderr);
    assert.equal(node.status, 0, node.stderr);
    assert.equal(node.stdout, python.stdout, item.node[1]);
    assert.equal(node.stderr, python.stderr, item.node[1]);
  }

  const changed = JSON.stringify([
    deployment,
    path.join(workspace, "config", "tenant", `${SECOND}.auto.tfvars.json`),
    path.join(workspace, "envs", "tenant", LABEL, "main.tf"),
  ]);
  const pythonScope = command(
    PYTHON_ORACLE,
    ["-m", "engine.ops", "scope-paths", "--json", "--paths-json", "-"],
    environment,
    changed,
  );
  const nodeScope = command(
    process.execPath,
    [cli, "scope-paths", "--paths-json", "-", "--root", packs, "--profile", PROFILE, "--catalog", CATALOG, "--deployment", deployment],
    environment,
    changed,
  );
  assert.equal(nodeScope.status, 0, nodeScope.stderr);
  assert.equal(nodeScope.stdout, pythonScope.stdout);
  assert.equal(nodeScope.stderr, pythonScope.stderr);
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
      python: ["-m", "engine.ops", "roots", "--json", "unknown_resource"],
      node: [cli, "roots", "--resource", "unknown_resource", ...common],
    },
    {
      python: ["-m", "engine.ops", "plan-roots", "--json", "--tenant", "../bad"],
      node: [cli, "plan-roots", "--tenant", "../bad", ...common],
    },
    {
      python: ["-m", "engine.ops", "scope-paths", "--json", "--paths-json", invalidPaths],
      node: [cli, "scope-paths", "--paths-json", invalidPaths, ...common],
    },
  ];
  for (const item of validationCases) {
    const python = command(PYTHON_ORACLE, item.python, environment);
    const node = command(process.execPath, item.node, environment);
    assert.equal(node.status, python.status, item.node[1]);
    assert.equal(node.stdout, python.stdout, item.node[1]);
    assert.equal(node.stderr, python.stderr, item.node[1]);
    assert.equal(node.status, 2, item.node[1]);
  }

  const invalidDeployment = path.join(workspace, "invalid-deployment.json");
  await writeText(invalidDeployment, '{"overlay":".","roots":[]}\n');
  const invalidRoot = path.join(workspace, "invalid-root.json");
  await writeText(invalidRoot, `${JSON.stringify({
    overlay: ".",
    roots: { zia: { groups: { bad_group: ["zia_not_real"] } } },
  })}\n`);
  for (const selected of [invalidDeployment, invalidRoot]) {
    const selectedEnvironment = {
      ...environment,
      INFRAWRIGHT_DEPLOYMENT: selected,
    };
    const python = command(
      PYTHON_ORACLE,
      ["-m", "engine.ops", "roots", "--json"],
      selectedEnvironment,
    );
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
    assert.equal(python.status, 2, python.stderr);
    assert.equal(node.status, python.status, node.stderr);
    assert.equal(node.stdout, python.stdout);
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
