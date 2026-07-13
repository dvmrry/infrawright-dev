import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  access,
  chmod,
  cp,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  createImportStagingTerraform,
  stageImports,
  unstageImports,
  type ImportStagingTerraform,
} from "../node-src/domain/import-staging.js";
import {
  filterGeneratedImports,
  renderGeneratedImports,
  renderHclQuotedString,
} from "../node-src/domain/import-moves.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const ZIA_RESOURCE = "zia_url_categories";
let fullRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(profile = "full.json", packsRoot = path.join(ROOT, "packs")): Promise<LoadedPackRoot> {
  if (profile === "full.json" && packsRoot === path.join(ROOT, "packs")) {
    fullRoot ??= loadPackRoot({
      packsRoot,
      profilePath: path.join(ROOT, "packsets", profile),
      catalogPath: path.join(ROOT, "packsets", "full.json"),
    });
    return fullRoot;
  }
  return loadPackRoot({
    packsRoot,
    profilePath: path.join(ROOT, "packsets", profile),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  prefix: string,
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeText(file: string, text: string): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, text, "utf8");
}

async function reducedPackRootForProfile(parent: string, profile: string): Promise<string> {
  const document = JSON.parse(
    await readFile(path.join(ROOT, "packsets", profile), "utf8"),
  ) as { readonly packs: readonly string[]; readonly shared: readonly string[] };
  const destination = path.join(parent, `packs-${profile.replace(/\.json$/u, "")}`);
  await mkdir(destination, { recursive: true });
  for (const name of document.packs) {
    await cp(path.join(ROOT, "packs", name), path.join(destination, name), { recursive: true });
  }
  for (const name of document.shared) {
    await mkdir(path.join(destination, "_shared"), { recursive: true });
    await cp(
      path.join(ROOT, "packs", "_shared", name),
      path.join(destination, "_shared", name),
      { recursive: true },
    );
  }
  return destination;
}

function importAddress(resourceType: string, key: string): string {
  return `module.${resourceType}.${resourceType}.this[${renderHclQuotedString(key)}]`;
}

function imports(resourceType: string, keys: readonly string[]): string {
  return renderGeneratedImports(
    resourceType,
    keys.map((key, index) => ({ key, importId: `id-${String(index)}` })),
  );
}

function pythonFilter(text: string, addresses: readonly string[]): {
  readonly text: string;
  readonly kept: number;
  readonly skipped: number;
} {
  const result = spawnSync("python3", [
    "-c",
    [
      "import json, sys",
      "from engine.filter_imports import filter_imports",
      "payload = json.load(sys.stdin)",
      "text, kept, skipped = filter_imports(payload['text'], payload['addresses'])",
      "json.dump({'text': text, 'kept': kept, 'skipped': skipped}, sys.stdout)",
    ].join("\n"),
  ], {
    cwd: ROOT,
    encoding: "utf8",
    input: JSON.stringify({ addresses, text }),
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout) as { text: string; kept: number; skipped: number };
}

test("generated import filtering matches Python across strings and surrounding HCL", () => {
  const managed = imports("zia_fake", ["managed"]);
  const kept = imports("zia_fake", ["keep"]);
  const dangerous = renderGeneratedImports("zia_fake", [{
    key: "line\nkey\ttail\\\" }",
    importId: "abc}def\nwith\ttab\\tail",
  }]);
  const cases = [
    {
      text: imports("zia_fake", ["already_managed", "needs_import"]),
      addresses: [importAddress("zia_fake", "already_managed")],
    },
    { text: dangerous, addresses: [importAddress("zia_fake", "line\nkey\ttail\\\" }")] },
    { text: dangerous, addresses: [] },
    {
      text: `resource "x" "y" {\n  value = "not an import } block"\n}\n${managed}locals {\n  keep = true\n}\n${kept}# tail\n`,
      addresses: [importAddress("zia_fake", "managed")],
    },
    { text: "resource \"x\" \"y\" {\n  value = \"abc}def\"\n}\n", addresses: ["resource.x.y"] },
    { text: `\u00a0${managed}\u3000`, addresses: [importAddress("zia_fake", "managed")] },
    { text: `# not a Python line\r${managed}`, addresses: [importAddress("zia_fake", "managed")] },
    { text: `# not a Python line\u2028${managed}`, addresses: [importAddress("zia_fake", "managed")] },
  ] as const;
  for (const item of cases) {
    assert.deepEqual(filterGeneratedImports(item.text, item.addresses), pythonFilter(item.text, item.addresses));
  }
});

test("generated import filtering rejects malformed and unterminated strings", () => {
  for (const text of [
    'import {\n  to = module.zia_fake.zia_fake.this["danger"]\n  id = "abc}def"\n',
    'import {\n  to = module.zia_fake.zia_fake.this["danger"]\n  id = "bad\\u0020escape"\n}\n',
  ]) {
    assert.throws(
      () => filterGeneratedImports(text, [importAddress("zia_fake", "danger")]),
      /generated import block/,
    );
  }
});

test("ordinary staging copies exact imports and moves and reports missing roots", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-stage-ordinary-");
  const root = await committedRoot();
  const deployment = { overlay: workspace, roots: {} } as const;
  const sourceImports = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`);
  const sourceMoves = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_moves.tf`);
  const importsText = imports(ZIA_RESOURCE, ["one"]);
  const movesText = "moved {\n  from = x.old\n  to = x.new\n}\n";
  await writeText(sourceImports, importsText);
  await writeText(sourceMoves, movesText);
  const diagnostics: string[] = [];
  const missing = await stageImports({
    deployment,
    onDiagnostic: (message) => diagnostics.push(message),
    root,
    selectors: [ZIA_RESOURCE],
    stateAware: false,
    tenant: "tenant",
    workspace,
  });
  assert.deepEqual(missing, { sources: 2, staged: 0 });
  assert.match(diagnostics.join("\n"), /run make gen-env/);

  const environmentRoot = path.join(workspace, "envs", "tenant", ZIA_RESOURCE);
  await mkdir(environmentRoot, { recursive: true });
  const result = await stageImports({
    deployment,
    root,
    selectors: [`zia/url_categories`],
    stateAware: false,
    tenant: "tenant",
    workspace,
  });
  assert.deepEqual(result, { sources: 2, staged: 2 });
  assert.equal(await readFile(path.join(environmentRoot, path.basename(sourceImports)), "utf8"), importsText);
  assert.equal(await readFile(path.join(environmentRoot, path.basename(sourceMoves)), "utf8"), movesText);
});

test("staging fails when no selected source artifact exists", async () => {
  await assert.rejects(
    stageImports({
      deployment: { overlay: ".", roots: {} },
      root: await committedRoot(),
      selectors: [ZIA_RESOURCE],
      stateAware: false,
      tenant: "tenant",
      workspace: os.tmpdir(),
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "NO_IMPORT_ARTIFACTS"
      && /transform or make adopt/.test(error.message),
  );
});

test("group selection stages every member into one root", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-stage-group-");
  const deployment = {
    overlay: workspace,
    roots: {
      zpa: {
        groups: {
          zpa_custom: ["zpa_segment_group", "zpa_server_group"],
        },
      },
    },
  } as const;
  await writeText(
    path.join(workspace, "imports", "tenant", "zpa_segment_group_imports.tf"),
    imports("zpa_segment_group", ["segment"]),
  );
  await writeText(
    path.join(workspace, "imports", "tenant", "zpa_server_group_moves.tf"),
    "# server group move\n",
  );
  const rootDirectory = path.join(workspace, "envs", "tenant", "zpa_custom");
  await mkdir(rootDirectory, { recursive: true });
  const diagnostics: string[] = [];
  const result = await stageImports({
    deployment,
    onDiagnostic: (message) => diagnostics.push(message),
    root: await committedRoot(),
    selectors: ["zpa_segment_group"],
    stateAware: false,
    tenant: "tenant",
    workspace,
  });
  assert.deepEqual(result, { sources: 2, staged: 2 });
  assert.match(diagnostics[0] ?? "", /selects whole root zpa_custom/);
  await access(path.join(rootDirectory, "zpa_segment_group_imports.tf"));
  await access(path.join(rootDirectory, "zpa_server_group_moves.tf"));
});

test("state-aware staging filters exact state and preserves moves independently", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-stage-state-");
  const root = await committedRoot();
  const deployment = { overlay: workspace, roots: {} } as const;
  const sourceImports = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`);
  const sourceMoves = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_moves.tf`);
  const text = imports(ZIA_RESOURCE, ["managed", "new"]);
  await writeText(sourceImports, text);
  await writeText(sourceMoves, "# moves stay independent\n");
  const rootDirectory = path.join(workspace, "envs", "tenant", ZIA_RESOURCE);
  await writeText(path.join(rootDirectory, "main.tf"), 'terraform {\r  backend "azurerm" {}\r}\r');
  const calls: Array<{ readonly kind: "init" | "list"; readonly request: unknown }> = [];
  const terraform: ImportStagingTerraform = {
    initialize: async (request) => { calls.push({ kind: "init", request }); },
    listState: async (request) => {
      calls.push({ kind: "list", request });
      return { success: true, stdout: `${importAddress(ZIA_RESOURCE, "managed")}\n` };
    },
  };
  await assert.rejects(
    stageImports({
      deployment,
      root,
      selectors: [ZIA_RESOURCE],
      stateAware: true,
      tenant: "tenant",
      terraform,
      workspace,
    }),
    /BACKEND_CONFIG/,
  );
  assert.equal(calls.length, 0);

  const backend = path.join(workspace, "backend.hcl");
  await writeText(backend, "storage_account_name = \"example\"\n");
  const result = await stageImports({
    backendConfig: "backend.hcl",
    deployment,
    root,
    selectors: [ZIA_RESOURCE],
    stateAware: true,
    tenant: "tenant",
    terraform,
    workspace,
  });
  assert.deepEqual(result, { sources: 2, staged: 2 });
  assert.equal(calls.length, 2);
  assert.deepEqual(calls.map((item) => item.kind), ["init", "list"]);
  assert.deepEqual(calls[0]?.request, {
    backendConfig: backend,
    directory: rootDirectory,
    label: ZIA_RESOURCE,
    tenant: "tenant",
  });
  const staged = await readFile(path.join(rootDirectory, `${ZIA_RESOURCE}_imports.tf`), "utf8");
  assert.equal(
    staged,
    filterGeneratedImports(text, [importAddress(ZIA_RESOURCE, "managed")]).text,
  );
  assert.equal(
    await readFile(path.join(rootDirectory, `${ZIA_RESOURCE}_moves.tf`), "utf8"),
    "# moves stay independent\n",
  );
});

test("state-aware empty delta removes stale imports and failed state-list keeps all", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-stage-empty-");
  const root = await committedRoot();
  const deployment = { overlay: workspace, roots: {} } as const;
  const source = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`);
  const destination = path.join(workspace, "envs", "tenant", ZIA_RESOURCE, `${ZIA_RESOURCE}_imports.tf`);
  const text = imports(ZIA_RESOURCE, ["one"]);
  await writeText(source, text);
  await writeText(destination, "stale\n");
  const allManaged: ImportStagingTerraform = {
    initialize: async () => undefined,
    listState: async () => ({ success: true, stdout: `${importAddress(ZIA_RESOURCE, "one")}\n` }),
  };
  assert.deepEqual(await stageImports({
    deployment,
    root,
    selectors: [ZIA_RESOURCE],
    stateAware: true,
    tenant: "tenant",
    terraform: allManaged,
    workspace,
  }), { sources: 1, staged: 0 });
  await assert.rejects(access(destination));

  const noState: ImportStagingTerraform = {
    initialize: async () => undefined,
    listState: async () => ({ success: false, stdout: "ignored" }),
  };
  assert.deepEqual(await stageImports({
    deployment,
    root,
    selectors: [ZIA_RESOURCE],
    stateAware: true,
    tenant: "tenant",
    terraform: noState,
    workspace,
  }), { sources: 1, staged: 1 });
  assert.equal(await readFile(destination, "utf8"), text);
});

test("unstaging removes selected member copies only and preserves source artifacts", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-unstage-");
  const deployment = {
    overlay: workspace,
    roots: {
      zpa: {
        groups: {
          zpa_custom: ["zpa_segment_group", "zpa_server_group"],
        },
      },
    },
  } as const;
  const environmentRoot = path.join(workspace, "envs", "tenant", "zpa_custom");
  const source = path.join(workspace, "imports", "tenant", "zpa_segment_group_imports.tf");
  await writeText(source, "source\n");
  for (const resourceType of ["zpa_segment_group", "zpa_server_group"]) {
    await writeText(path.join(environmentRoot, `${resourceType}_imports.tf`), "staged\n");
    await writeText(path.join(environmentRoot, `${resourceType}_moves.tf`), "staged\n");
  }
  await writeText(path.join(environmentRoot, "main.tf"), "keep\n");
  const diagnostics: string[] = [];
  assert.deepEqual(await unstageImports({
    deployment,
    onDiagnostic: (message) => diagnostics.push(message),
    root: await committedRoot(),
    selectors: ["zpa_segment_group"],
    tenant: "tenant",
    workspace,
  }), { removed: 4 });
  assert.match(diagnostics[0] ?? "", /selects whole root zpa_custom/);
  assert.equal(await readFile(source, "utf8"), "source\n");
  assert.equal(await readFile(path.join(environmentRoot, "main.tf"), "utf8"), "keep\n");
  assert.deepEqual(await unstageImports({
    deployment,
    root: await committedRoot(),
    selectors: ["zpa_segment_group"],
    tenant: "tenant",
    workspace,
  }), { removed: 0 });
});

async function prepareDifferentialWorkspace(
  workspace: string,
  importsText = imports(ZIA_RESOURCE, ["managed", "new"]),
): Promise<void> {
  await writeText(
    path.join(workspace, "deployment.json"),
    `${JSON.stringify({ overlay: ".", roots: {} }, null, 2)}\n`,
  );
  await writeText(
    path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`),
    importsText,
  );
  await mkdir(path.join(workspace, "envs", "tenant", ZIA_RESOURCE), { recursive: true });
}

function runPythonStateAwareStage(workspace: string, address: string): void {
  const python = spawnSync("python3", [
    "-c",
    [
      "import sys",
      "from engine import ops",
      "class Result:",
      "    returncode = 0",
      `    stdout = ${JSON.stringify(`${address}\n`)}.encode('utf-8')`,
      "ops._check_call = lambda *args, **kwargs: 0",
      "ops.subprocess.run = lambda *args, **kwargs: Result()",
      `raise SystemExit(ops.cmd_stage_imports({'tenant': 'tenant', 'selectors': ['${ZIA_RESOURCE}'], 'state_aware': True, 'backend_config': None}))`,
    ].join("\n"),
  ], {
    cwd: workspace,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: path.join(workspace, "deployment.json"),
      INFRAWRIGHT_PACKS: path.join(ROOT, "packs"),
      INFRAWRIGHT_PACK_PROFILE: path.join(ROOT, "packsets", "full.json"),
      PYTHONPATH: ROOT,
    },
  });
  assert.equal(python.status, 0, python.stderr);
}

test("state-aware staging tree matches Python with injected state output", async (context) => {
  const pythonWorkspace = await temporaryDirectory(context, "infrawright-stage-python-");
  const nodeWorkspace = await temporaryDirectory(context, "infrawright-stage-node-");
  await Promise.all([
    prepareDifferentialWorkspace(pythonWorkspace),
    prepareDifferentialWorkspace(nodeWorkspace),
  ]);
  const address = importAddress(ZIA_RESOURCE, "managed");
  runPythonStateAwareStage(pythonWorkspace, address);
  await stageImports({
    deployment: { overlay: ".", roots: {} },
    root: await committedRoot(),
    selectors: [ZIA_RESOURCE],
    stateAware: true,
    tenant: "tenant",
    terraform: {
      initialize: async () => undefined,
      listState: async () => ({ success: true, stdout: `${address}\n` }),
    },
    workspace: nodeWorkspace,
  });
  const relative = path.join("envs", "tenant", ZIA_RESOURCE, `${ZIA_RESOURCE}_imports.tf`);
  assert.equal(
    await readFile(path.join(nodeWorkspace, relative), "utf8"),
    await readFile(path.join(pythonWorkspace, relative), "utf8"),
  );
});

test("state-aware file decoding matches Python for CR, CRLF, and UTF-8 BOM", async (context) => {
  const canonical = imports(ZIA_RESOURCE, ["managed", "new"]);
  const cases = [
    { label: "cr", text: canonical.replaceAll("\n", "\r") },
    { label: "crlf", text: canonical.replace("\n", "\r\n") },
    { label: "bom", text: `\ufeff${canonical}` },
  ] as const;
  const address = importAddress(ZIA_RESOURCE, "managed");
  for (const item of cases) {
    const pythonWorkspace = await temporaryDirectory(context, `infrawright-stage-${item.label}-python-`);
    const nodeWorkspace = await temporaryDirectory(context, `infrawright-stage-${item.label}-node-`);
    await Promise.all([
      prepareDifferentialWorkspace(pythonWorkspace, item.text),
      prepareDifferentialWorkspace(nodeWorkspace, item.text),
    ]);
    const sourceRelative = path.join("imports", "tenant", `${ZIA_RESOURCE}_imports.tf`);
    const sourceBefore = await readFile(path.join(nodeWorkspace, sourceRelative));
    runPythonStateAwareStage(pythonWorkspace, address);
    await stageImports({
      deployment: { overlay: ".", roots: {} },
      root: await committedRoot(),
      selectors: [ZIA_RESOURCE],
      stateAware: true,
      tenant: "tenant",
      terraform: {
        initialize: async () => undefined,
        listState: async () => ({ success: true, stdout: `${address}\n` }),
      },
      workspace: nodeWorkspace,
    });
    const stagedRelative = path.join("envs", "tenant", ZIA_RESOURCE, `${ZIA_RESOURCE}_imports.tf`);
    assert.equal(
      await readFile(path.join(nodeWorkspace, stagedRelative), "utf8"),
      await readFile(path.join(pythonWorkspace, stagedRelative), "utf8"),
      item.label,
    );
    assert.deepEqual(await readFile(path.join(nodeWorkspace, sourceRelative)), sourceBefore);
  }
});

test("staging metadata works for full, provider, Zscaler, empty, and reduced roots", async (context) => {
  const profileParent = await temporaryDirectory(context, "infrawright-stage-profiles-");
  const ziaPacks = await reducedPackRootForProfile(profileParent, "zia.json");
  const zscalerPacks = await reducedPackRootForProfile(profileParent, "zscaler.json");
  const variants: Array<{ readonly label: string; readonly root: LoadedPackRoot }> = [
    { label: "full", root: await committedRoot() },
    { label: "provider", root: await committedRoot("zia.json", ziaPacks) },
    { label: "zscaler", root: await committedRoot("zscaler.json", zscalerPacks) },
  ];
  const reducedParent = await temporaryDirectory(context, "infrawright-stage-reduced-packs-");
  const reduced = path.join(reducedParent, "packs");
  await mkdir(path.join(reduced, "_shared"), { recursive: true });
  await cp(path.join(ROOT, "packs", "zia"), path.join(reduced, "zia"), { recursive: true });
  await cp(
    path.join(ROOT, "packs", "_shared", "zscaler"),
    path.join(reduced, "_shared", "zscaler"),
    { recursive: true },
  );
  variants.push({ label: "reduced", root: await committedRoot("zia.json", reduced) });
  for (const variant of variants) {
    const workspace = await temporaryDirectory(context, `infrawright-stage-${variant.label}-`);
    await writeText(
      path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`),
      imports(ZIA_RESOURCE, [variant.label]),
    );
    await mkdir(path.join(workspace, "envs", "tenant", ZIA_RESOURCE), { recursive: true });
    assert.deepEqual(await stageImports({
      deployment: { overlay: workspace, roots: {} },
      root: variant.root,
      selectors: [ZIA_RESOURCE],
      stateAware: false,
      tenant: "tenant",
      workspace,
    }), { sources: 1, staged: 1 });
  }
  const emptyPacks = await reducedPackRootForProfile(profileParent, "empty.json");
  const empty = await committedRoot("empty.json", emptyPacks);
  await assert.rejects(stageImports({
    deployment: { overlay: ".", roots: {} },
    root: empty,
    selectors: [],
    stateAware: false,
    tenant: "tenant",
    workspace: reducedParent,
  }), /nothing to stage/);
});

test("generic Terraform staging adapter passes backend key and tolerates absent state", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-stage-terraform-");
  const executable = path.join(workspace, "terraform-fake");
  const log = path.join(workspace, "terraform.log");
  await writeText(executable, [
    "#!/bin/sh",
    "printf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$FAKE_TF_LOG\"",
    "if [ \"$1 $2\" = \"state list\" ]; then exit \"${FAKE_STATE_STATUS:-0}\"; fi",
    "exit 0",
    "",
  ].join("\n"));
  await chmod(executable, 0o755);
  const directory = path.join(workspace, "root");
  await mkdir(directory);
  const request = {
    backendConfig: path.join(workspace, "backend.hcl"),
    directory,
    label: "grouped",
    tenant: "tenant",
  } as const;
  const adapter = createImportStagingTerraform({
    environment: { FAKE_STATE_STATUS: "1", FAKE_TF_LOG: log, PATH: process.env.PATH ?? "" },
    terraformExecutable: executable,
  });
  await adapter.initialize(request);
  assert.deepEqual(await adapter.listState(request), { success: false, stdout: "" });
  const calls = await readFile(log, "utf8");
  assert.match(calls, /init -input=false -reconfigure/);
  assert.match(calls, /-backend-config=key=tenant\/grouped\.tfstate/);
  assert.match(calls, /state list/);
});

test("make stage-imports and unstage-imports are Python-disabled", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-make-stage-");
  const deployment = path.join(workspace, "deployment.json");
  await writeText(deployment, `${JSON.stringify({ overlay: workspace, roots: {} }, null, 2)}\n`);
  const source = path.join(workspace, "imports", "tenant", `${ZIA_RESOURCE}_imports.tf`);
  await writeText(source, imports(ZIA_RESOURCE, ["managed", "new"]));
  await mkdir(path.join(workspace, "envs", "tenant", ZIA_RESOURCE), { recursive: true });
  const executable = path.join(workspace, "terraform-fake");
  await writeText(executable, [
    "#!/bin/sh",
    `if [ \"$1 $2\" = \"state list\" ]; then printf '%s\\n' '${importAddress(ZIA_RESOURCE, "managed")}'; fi`,
    "exit 0",
    "",
  ].join("\n"));
  await chmod(executable, 0o755);
  const packsRoot = await reducedPackRootForProfile(workspace, "zia.json");
  const common = [
    "TENANT=tenant",
    `RESOURCE=${ZIA_RESOURCE}`,
    "PACK_PROFILE=packsets/zia.json",
    "PACK_CATALOG=packsets/full.json",
    "PYTHON=/python-must-not-run",
  ];
  const staged = spawnSync("make", [
    "stage-imports",
    ...common,
    "STATE_AWARE=1",
    `TF=${executable}`,
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deployment,
      INFRAWRIGHT_PACKS: packsRoot,
    },
  });
  assert.equal(staged.status, 0, `${staged.stdout}\n${staged.stderr}`);
  assert.equal(`${staged.stdout}${staged.stderr}`.includes("python-must-not-run"), false);
  const destination = path.join(workspace, "envs", "tenant", ZIA_RESOURCE, `${ZIA_RESOURCE}_imports.tf`);
  assert.equal(
    await readFile(destination, "utf8"),
    filterGeneratedImports(
      await readFile(source, "utf8"),
      [importAddress(ZIA_RESOURCE, "managed")],
    ).text,
  );

  const unstaged = spawnSync("make", ["unstage-imports", ...common], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deployment,
      INFRAWRIGHT_PACKS: packsRoot,
    },
  });
  assert.equal(unstaged.status, 0, `${unstaged.stdout}\n${unstaged.stderr}`);
  assert.equal(`${unstaged.stdout}${unstaged.stderr}`.includes("python-must-not-run"), false);
  await assert.rejects(access(destination));
  assert.equal(await readFile(source, "utf8"), imports(ZIA_RESOURCE, ["managed", "new"]));
});
