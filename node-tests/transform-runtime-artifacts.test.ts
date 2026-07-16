import assert from "node:assert/strict";
import {
  access,
  copyFile,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { decodeHTML } from "entities";

import {
  runTransformBatch,
  transformLookupNameField,
} from "../node-src/domain/transform-runner.js";
import { stageImports } from "../node-src/domain/import-staging.js";
import { planEnvironmentRoots } from "../node-src/domain/plan-lifecycle.js";
import { pythonHtmlUnescapeGeneric } from "../node-src/domain/python-html-unescape.js";
import {
  compileTransformArtifactBatch,
  compileTransformArtifacts,
  deriveGeneratedBindings,
  installTransformArtifactBatchCommitHookForTests,
  publishCompiledTransformArtifactBatch,
  publishCompiledTransformArtifacts,
  renderGeneratedBindings,
  renderTransformLookup,
  transformArtifactPaths,
  writeTransformArtifacts,
  type TransformArtifactCompileOptions,
} from "../node-src/domain/transform-artifacts.js";
import { transformLoadedItems } from "../node-src/domain/pull-transform.js";
import type { Deployment } from "../node-src/domain/types.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const DEMO_INPUT = path.join(ROOT, "tests", "fixtures", "demo");
const DEMO_EXPECTED = path.join(ROOT, "tests", "fixtures", "demo-expected");
const TRANSFORM_FIXTURES = path.join(ROOT, "tests", "fixtures", "transform");

const DEMO_RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
  "zia_bandwidth_control_rule",
  "zia_cloud_app_control_rule",
  "zia_dlp_web_rules",
  "zia_location_management",
  "zia_rule_labels",
  "zia_ssl_inspection_rules",
  "zia_url_categories",
  "zia_url_filtering_rules",
  "zpa_app_connector_group",
  "zpa_application_segment",
  "zpa_application_server",
  "zpa_microtenant_controller",
  "zpa_policy_access_rule",
  "zpa_segment_group",
  "zpa_server_group",
] as const;

const FIXTURE_RESOURCES = [
  "zia_cloud_app_control_rule",
  "zia_location_management",
  "zia_ssl_inspection_rules",
  "zia_url_categories",
  "zpa_application_segment",
  "zpa_segment_group",
  "zpa_server_group",
] as const;

let loadedRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(): Promise<LoadedPackRoot> {
  loadedRoot ??= loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  return loadedRoot;
}

function deployment(overlay: string, options?: {
  readonly hcl?: boolean;
  readonly roots?: Deployment["roots"];
}): Deployment {
  return {
    overlay,
    ...(options?.hcl === true ? { tfvars_format: "hcl" } : {}),
    roots: options?.roots ?? {},
  };
}

function artifactCompileOptions(options: {
  readonly deployment?: Deployment;
  readonly lookupNameField?: string | null;
  readonly override?: Readonly<Record<string, unknown>>;
  readonly references?: TransformArtifactCompileOptions["references"];
  readonly resourceType?: string;
  readonly result?: TransformArtifactCompileOptions["result"];
  readonly workspace: string;
}): TransformArtifactCompileOptions {
  const resourceType = options.resourceType ?? "sample_resource";
  const references = options.references ?? {};
  return {
    bindingContext: {
      mode: "disabled",
      derived: new Set(),
      generated: new Set([resourceType]),
      resourceRoots: { [resourceType]: resourceType },
      references,
    },
    deployment: options.deployment ?? deployment(options.workspace),
    lookupNameField: options.lookupNameField === undefined ? "name" : options.lookupNameField,
    override: options.override ?? {},
    references,
    resourceType,
    result: options.result ?? {
      drops: [],
      items: { example: { name: "Example" } },
      originals: { example: { id: "id-1", name: "Example" } },
    },
    tenant: "tenant",
    variableName: "items",
  };
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  prefix: string,
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix));
  context.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

async function snapshotFlatDirectory(directory: string): Promise<Readonly<Record<string, string>>> {
  return Object.fromEntries(await Promise.all(
    (await readdir(directory)).sort().map(async (name) => [
      name,
      await readFile(path.join(directory, name), "utf8"),
    ] as const),
  ));
}

async function exists(file: string): Promise<boolean> {
  try {
    await access(file);
    return true;
  } catch {
    return false;
  }
}

async function text(file: string): Promise<string> {
  return readFile(file, "utf8");
}

async function artifactSnapshot(
  paths: ReturnType<typeof transformArtifactPaths>,
): Promise<Readonly<Record<string, string | null>>> {
  const entries = await Promise.all(Object.entries(paths).map(async ([key, file]) => {
    try {
      return [key, await readFile(file, "utf8")] as const;
    } catch (error: unknown) {
      if (
        typeof error === "object"
        && error !== null
        && "code" in error
        && error.code === "ENOENT"
      ) {
        return [key, null] as const;
      }
      throw error;
    }
  }));
  return Object.fromEntries(entries) as Readonly<Record<string, string | null>>;
}

test("runTransformBatch materializes all 20 demo fixture goldens exactly", async (context) => {
  const output = await temporaryDirectory(context, "infrawright-demo-runtime-");
  const result = await runTransformBatch({
    deployment: deployment(output),
    inputDirectory: DEMO_INPUT,
    root: await committedRoot(),
    selectors: [...DEMO_RESOURCES],
    tenant: "demo",
  });

  assert.deepEqual(result.failed, []);
  assert.deepEqual(result.skipped, []);
  assert.deepEqual(new Set(result.processed), new Set(DEMO_RESOURCES));
  for (const resourceType of DEMO_RESOURCES) {
    assert.equal(
      await text(path.join(output, "config", "demo", `${resourceType}.auto.tfvars.json`)),
      await text(path.join(DEMO_EXPECTED, `${resourceType}.tfvars.json`)),
      `${resourceType} tfvars`,
    );
    assert.equal(
      await text(path.join(output, "imports", "demo", `${resourceType}_imports.tf`)),
      await text(path.join(DEMO_EXPECTED, `${resourceType}_imports.tf`)),
      `${resourceType} imports`,
    );
  }
});

test("runTransformBatch materializes all seven detailed transform goldens exactly", async (context) => {
  const output = await temporaryDirectory(context, "infrawright-transform-goldens-");
  const root = await committedRoot();
  for (const resourceType of FIXTURE_RESOURCES) {
    const input = path.join(output, "input", resourceType);
    await mkdir(input, { recursive: true });
    await copyFile(
      path.join(TRANSFORM_FIXTURES, resourceType, "api.json"),
      path.join(input, `${resourceType}.json`),
    );
    const result = await runTransformBatch({
      deployment: deployment(output),
      inputDirectory: input,
      root,
      selectors: [resourceType],
      tenant: "tenant",
    });
    assert.deepEqual(result.failed, [], resourceType);
    assert.deepEqual(result.skipped, [], resourceType);
    assert.deepEqual(result.processed, [resourceType], resourceType);
    assert.equal(
      await text(path.join(output, "config", "tenant", `${resourceType}.auto.tfvars.json`)),
      await text(path.join(TRANSFORM_FIXTURES, resourceType, "expected.auto.tfvars.json")),
      `${resourceType} tfvars`,
    );
    assert.equal(
      await text(path.join(output, "imports", "tenant", `${resourceType}_imports.tf`)),
      await text(path.join(TRANSFORM_FIXTURES, resourceType, "expected_imports.tf")),
      `${resourceType} imports`,
    );
  }
});

test("all 74 active overrides compile and accept an empty transform", async () => {
  const root = await committedRoot();
  const overridden = [...root.resources.values()].filter((resource) => {
    return resource.override !== null;
  });
  assert.equal(overridden.length, 74);
  for (const resource of overridden) {
    const result = transformLoadedItems({
      htmlUnescape: decodeHTML,
      rawItems: [],
      resource,
      schema: await root.loadResourceSchema(resource.type),
      unescapeHtml: false,
    });
    assert.deepEqual(Object.keys(result.items), [], resource.type);
    assert.deepEqual(Object.keys(result.originals), [], resource.type);
    assert.deepEqual(result.drops, [], resource.type);
  }
});

test("lookup rendering is sorted, survivor-based, unknown-safe, and last-key-wins", () => {
  assert.equal(renderTransformLookup({
    items: {
      alpha: { configured_name: "Alpha projected" },
      beta: { configured_name: "   " },
      omega: { configured_name: "Omega" },
    },
    originals: {
      alpha: { configured_name: "Raw Alpha", id: "CUSTOM_01" },
      beta: { id: "CUSTOM_02" },
      omega: { id: "CUSTOM_01" },
    },
    nameField: "configured_name",
  }), "{\n"
    + "  \"by_id\": {\n"
    + "    \"CUSTOM_01\": \"Omega\",\n"
    + "    \"CUSTOM_02\": \"<unknown>\"\n"
    + "  },\n"
    + "  \"key_by_id\": {\n"
    + "    \"CUSTOM_01\": \"omega\",\n"
    + "    \"CUSTOM_02\": \"beta\"\n"
    + "  }\n"
    + "}\n");
});

test("transform artifact compilation performs no filesystem mutation", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-compile-");
  const options = artifactCompileOptions({ workspace });
  const paths = transformArtifactPaths(options);

  const compiled = await compileTransformArtifacts(options);

  assert.deepEqual(compiled.paths, paths);
  assert.equal(await exists(path.join(workspace, "config")), false);
  assert.equal(await exists(path.join(workspace, "imports")), false);

  await publishCompiledTransformArtifacts(compiled);
  assert.equal(await exists(paths.config), true);
  assert.equal(await exists(paths.imports), true);
  assert.equal(await exists(paths.lookup), true);
});

test("artifact batch preflights every compile before publishing any member", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-preflight-");
  const valid = artifactCompileOptions({
    resourceType: "sample_first",
    workspace,
  });
  const invalid = artifactCompileOptions({
    override: { import_id: "{missing}" },
    resourceType: "sample_second",
    workspace,
  });

  await assert.rejects(
    compileTransformArtifactBatch([valid, invalid]),
    /references missing field "missing"/u,
  );
  assert.equal(await exists(path.join(workspace, "config")), false);
  assert.equal(await exists(path.join(workspace, "imports")), false);
});

test("compile and publish preserve legacy artifact bytes and lifecycle", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-parity-");
  const legacyOptions = artifactCompileOptions({
    workspace: path.join(workspace, "legacy"),
  });
  const splitOptions = artifactCompileOptions({
    workspace: path.join(workspace, "split"),
  });

  const legacy = await writeTransformArtifacts(legacyOptions);
  const split = await publishCompiledTransformArtifacts(
    await compileTransformArtifacts(splitOptions),
  );
  const legacyPaths = transformArtifactPaths(legacyOptions);
  const splitPaths = transformArtifactPaths(splitOptions);
  for (const key of ["config", "imports", "lookup"] as const) {
    assert.equal(await text(splitPaths[key]), await text(legacyPaths[key]), key);
  }
  assert.deepEqual(
    split.written.map((file) => path.basename(file)),
    legacy.written.map((file) => path.basename(file)),
  );
  assert.deepEqual(split.removed, []);
  assert.deepEqual(legacy.removed, []);
});

test("batch publication rolls every member back when a later member commit fails", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-rollback-");
  const diagnostics: string[] = [];
  const options = ["sample_first", "sample_second"].map((resourceType) => ({
    ...artifactCompileOptions({ resourceType, workspace }),
    onDiagnostic: (message: string) => {
      diagnostics.push(message);
    },
  }));
  const seed = await compileTransformArtifactBatch(options);
  for (const item of seed) {
    await mkdir(path.dirname(item.paths.config), { recursive: true });
    await mkdir(path.dirname(item.paths.imports), { recursive: true });
    await writeFile(item.paths.config, `old config for ${item.resourceType}\n`, "utf8");
    await writeFile(item.paths.staleConfig, `old stale config for ${item.resourceType}\n`, "utf8");
    await writeFile(item.paths.generatedBindings, `old bindings for ${item.resourceType}\n`, "utf8");
    await writeFile(item.paths.imports, item.newImports, "utf8");
    await writeFile(item.paths.lookup, `old lookup for ${item.resourceType}\n`, "utf8");
  }
  const before = await Promise.all(seed.map((item) => artifactSnapshot(item.paths)));
  const compiled = await compileTransformArtifactBatch(options);
  const removeHook = installTransformArtifactBatchCommitHookForTests((mutation) => {
    if (
      mutation.resourceType === "sample_second"
      && mutation.target === compiled[1]?.paths.config
    ) {
      throw new Error("injected member-2 commit failure");
    }
  });
  try {
    await assert.rejects(
      publishCompiledTransformArtifactBatch(compiled),
      /injected member-2 commit failure/u,
    );
  } finally {
    removeHook();
  }

  assert.deepEqual(
    await Promise.all(seed.map((item) => artifactSnapshot(item.paths))),
    before,
  );
  assert.deepEqual(diagnostics, []);
});

test("rollback failure preserves transaction backups for operator recovery", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-recovery-");
  const options = ["sample_first", "sample_second"].map((resourceType) => {
    return artifactCompileOptions({ resourceType, workspace });
  });
  const compiled = await compileTransformArtifactBatch(options);
  for (const item of compiled) {
    await mkdir(path.dirname(item.paths.config), { recursive: true });
    await mkdir(path.dirname(item.paths.imports), { recursive: true });
    await writeFile(item.paths.config, `old config for ${item.resourceType}\n`, "utf8");
    await writeFile(item.paths.imports, item.newImports, "utf8");
  }
  const first = compiled[0]!;
  const second = compiled[1]!;
  const removeHook = installTransformArtifactBatchCommitHookForTests((mutation, phase) => {
    if (phase === "commit" && mutation.target === second.paths.config) {
      throw new Error("injected commit failure");
    }
    if (phase === "rollback" && mutation.target === first.paths.config) {
      throw new Error("injected rollback failure");
    }
  });
  try {
    await assert.rejects(
      publishCompiledTransformArtifactBatch(compiled),
      /recovery data preserved/u,
    );
  } finally {
    removeHook();
  }

  const parent = path.dirname(first.paths.config);
  const transactionDirectories = (await readdir(parent, { withFileTypes: true }))
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(".infrawright-artifact-batch-"))
    .map((entry) => path.join(parent, entry.name));
  assert.equal(transactionDirectories.length, 1);
  const recoveryContents = await Promise.all(
    (await readdir(transactionDirectories[0]!)).map((name) => {
      return readFile(path.join(transactionDirectories[0]!, name), "utf8");
    }),
  );
  assert.ok(recoveryContents.includes("old config for sample_first\n"));
});

test("batch publication rejects a directory target without mutating or deleting it", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-directory-");
  const options = ["sample_first", "sample_second"].map((resourceType) => {
    return artifactCompileOptions({ resourceType, workspace });
  });
  const compiled = await compileTransformArtifactBatch(options);
  const first = compiled[0]!;
  const second = compiled[1]!;
  await mkdir(path.dirname(first.paths.config), { recursive: true });
  await mkdir(path.dirname(first.paths.imports), { recursive: true });
  await writeFile(first.paths.config, "first artifact before failure\n", "utf8");
  await writeFile(first.paths.imports, first.newImports, "utf8");
  const firstBefore = await artifactSnapshot(first.paths);
  await mkdir(second.paths.config, { recursive: true });
  const sentinel = path.join(second.paths.config, "sentinel.txt");
  await writeFile(sentinel, "directory must survive\n", "utf8");

  await assert.rejects(
    publishCompiledTransformArtifactBatch(compiled),
    /batch target is not a regular file/u,
  );

  assert.deepEqual(await artifactSnapshot(first.paths), firstBefore);
  assert.equal(await text(sentinel), "directory must survive\n");
});

test("successful batch publication preserves sequential writer bytes and lifecycle", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-batch-parity-");
  const legacyWorkspace = path.join(workspace, "legacy");
  const batchWorkspace = path.join(workspace, "batch");
  const resourceTypes = ["sample_first", "sample_second"] as const;
  const legacyOptions = resourceTypes.map((resourceType) => {
    return artifactCompileOptions({ resourceType, workspace: legacyWorkspace });
  });
  const batchOptions = resourceTypes.map((resourceType) => {
    return artifactCompileOptions({ resourceType, workspace: batchWorkspace });
  });
  for (const options of [...legacyOptions, ...batchOptions]) {
    const paths = transformArtifactPaths(options);
    await mkdir(path.dirname(paths.config), { recursive: true });
    await writeFile(paths.staleConfig, "stale opposite-format config\n", "utf8");
    await writeFile(paths.generatedBindings, "stale generated bindings\n", "utf8");
  }

  const legacyResults = [];
  for (const options of legacyOptions) {
    legacyResults.push(await writeTransformArtifacts(options));
  }
  const batchResults = await publishCompiledTransformArtifactBatch(
    await compileTransformArtifactBatch(batchOptions),
  );

  for (const [index, resourceType] of resourceTypes.entries()) {
    const legacyPaths = transformArtifactPaths(legacyOptions[index]!);
    const batchPaths = transformArtifactPaths(batchOptions[index]!);
    assert.deepEqual(
      await artifactSnapshot(batchPaths),
      await artifactSnapshot(legacyPaths),
      resourceType,
    );
    assert.deepEqual(
      batchResults[index]?.written.map((file) => path.relative(batchWorkspace, file)),
      legacyResults[index]?.written.map((file) => path.relative(legacyWorkspace, file)),
      `${resourceType} written lifecycle`,
    );
    assert.deepEqual(
      batchResults[index]?.removed.map((file) => path.relative(batchWorkspace, file)),
      legacyResults[index]?.removed.map((file) => path.relative(legacyWorkspace, file)),
      `${resourceType} removed lifecycle`,
    );
  }
});

test("batch compilation uses new lookup results for grouped bindings and HCL comments", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-artifact-lookups-");
  const hclDeployment = deployment(workspace, { hcl: true });
  const references = {
    group_id: { name_field: "name", referent: "sample_group" },
  } as const;
  const generated = new Set(["sample_group", "sample_item"]);
  const resourceRoots = {
    sample_group: "sample_root",
    sample_item: "sample_root",
  } as const;
  const referrer: TransformArtifactCompileOptions = {
    ...artifactCompileOptions({
      deployment: hclDeployment,
      lookupNameField: null,
      references,
      resourceType: "sample_item",
      result: {
        drops: [],
        items: { item: { group_id: "new-id", name: "Item" } },
        originals: { item: { id: "item-id", name: "Item" } },
      },
      workspace,
    }),
    bindingContext: {
      mode: "same_root",
      derived: new Set(),
      generated,
      references,
      resourceRoots,
    },
  };
  const referent: TransformArtifactCompileOptions = {
    ...artifactCompileOptions({
      deployment: hclDeployment,
      resourceType: "sample_group",
      result: {
        drops: [],
        items: { new_key: { name: "Fresh Group" } },
        originals: { new_key: { id: "new-id", name: "Fresh Group" } },
      },
      workspace,
    }),
    bindingContext: {
      mode: "same_root",
      derived: new Set(),
      generated,
      references: {},
      resourceRoots,
    },
  };
  const staleLookup = transformArtifactPaths(referent).lookup;
  await writeJson(staleLookup, {
    by_id: { "new-id": "Stale Group" },
    key_by_id: { "new-id": "stale_key" },
  });

  const compiled = await compileTransformArtifactBatch([referrer, referent]);
  await publishCompiledTransformArtifactBatch(compiled);

  const referrerPaths = transformArtifactPaths(referrer);
  assert.match(await text(referrerPaths.config), /group_id\s+= "new-id"\s+# Fresh Group/u);
  assert.match(
    await text(referrerPaths.generatedBindings),
    /module\.sample_group\.items\[\\"new_key\\"\]\.id/u,
  );
});

test("generic Python HTML unescape covers named, prefix, numeric, invalid, and two-pass inputs", () => {
  assert.equal(
    pythonHtmlUnescapeGeneric(
      "&NotEqualTilde; &notit; &#x80; &#1; &#xD800; &#xFDD0;",
    ),
    "≂̸ ¬it; €  � ",
  );
  assert.equal(pythonHtmlUnescapeGeneric("&amp;lt;"), "&lt;");
  assert.equal(
    pythonHtmlUnescapeGeneric(pythonHtmlUnescapeGeneric("&amp;lt;")),
    "<",
  );
});

test("unresolved move evidence survives reruns, stages for plan, and rejects conflicts atomically", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-moves-");
  const input = path.join(workspace, "input");
  const source = path.join(input, "zia_rule_labels.json");
  const root = await committedRoot();
  await writeJson(source, [{ id: 7, name: "Original Name" }]);
  const options = {
    deployment: deployment(workspace),
    inputDirectory: input,
    root,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  } as const;
  assert.deepEqual((await runTransformBatch(options)).failed, []);
  const imports = path.join(workspace, "imports", "tenant", "zia_rule_labels_imports.tf");
  const originalImports = await text(imports);

  await writeJson(source, [{ id: 7, name: "Renamed Thing" }]);
  assert.deepEqual((await runTransformBatch(options)).failed, []);
  const moves = path.join(workspace, "imports", "tenant", "zia_rule_labels_moves.tf");
  const moveText = "moved {\n"
    + "  from = module.zia_rule_labels.zia_rule_labels.this[\"original_name\"]\n"
    + "  to   = module.zia_rule_labels.zia_rule_labels.this[\"renamed_thing\"]\n"
    + "}\n";
  assert.equal(await text(moves), moveText);

  assert.deepEqual((await runTransformBatch(options)).failed, []);
  assert.equal(await text(moves), moveText);

  const environmentRoot = path.join(
    workspace,
    "envs",
    "tenant",
    "zia_rule_labels",
  );
  await mkdir(environmentRoot, { recursive: true });
  await writeFile(path.join(environmentRoot, "main.tf"), "terraform {}\n", "utf8");
  assert.deepEqual(await stageImports({
    deployment: options.deployment,
    root,
    selectors: ["zia_rule_labels"],
    stateAware: false,
    tenant: "tenant",
    workspace,
  }), { sources: 2, staged: 2 });
  assert.equal(
    await text(path.join(environmentRoot, "zia_rule_labels_moves.tf")),
    moveText,
  );
  const planned: string[] = [];
  assert.deepEqual(await planEnvironmentRoots({
    deployment: options.deployment,
    importsOnly: false,
    root,
    save: false,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
    terraform: {
      initialize: async () => undefined,
      plan: async (request) => {
        planned.push(request.directory);
      },
    },
    workspace,
  }), { planned: 1 });
  assert.deepEqual(planned, [environmentRoot]);

  await writeFile(imports, originalImports, "utf8");
  assert.deepEqual((await runTransformBatch(options)).failed, []);
  assert.equal(await text(moves), moveText);

  const paths = transformArtifactPaths({
    deployment: options.deployment,
    resourceType: "zia_rule_labels",
    tenant: "tenant",
  });
  await writeFile(paths.lookup, "lookup-before-conflict\n", "utf8");
  await writeFile(paths.generatedBindings, "binding-before-conflict\n", "utf8");
  const protectedArtifacts = [
    paths.config,
    paths.lookup,
    paths.generatedBindings,
    paths.imports,
    paths.moves,
  ] as const;
  const before = await Promise.all(protectedArtifacts.map((file) => text(file)));
  const diagnostics: string[] = [];
  await writeJson(source, [{ id: 7, name: "Another Rename" }]);
  const conflict = await runTransformBatch({
    ...options,
    onDiagnostic: (message) => diagnostics.push(message),
  });
  assert.deepEqual(conflict.failed, ["zia_rule_labels"]);
  assert.match(diagnostics.join("\n"), /unresolved\/conflicting move evidence/u);
  assert.deepEqual(
    await Promise.all(protectedArtifacts.map((file) => text(file))),
    before,
  );
});

test("same-root references materialize generated binding JSON on the first batch", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-bindings-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zpa_segment_group.json"), [{
    enabled: true,
    id: "sg-1",
    name: "Segment One",
  }]);
  await writeJson(path.join(input, "zpa_application_segment.json"), [{
    domainNames: ["app.example.com"],
    id: "app-1",
    name: "App One",
    segmentGroupId: "sg-1",
  }]);
  const roots = {
    zpa: {
      bind_references: true,
      groups: {
        zpa_custom: ["zpa_application_segment", "zpa_segment_group"],
      },
    },
  } as const;
  const result = await runTransformBatch({
    deployment: deployment(workspace, { roots }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_application_segment", "zpa_segment_group"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  assert.equal(
    await text(path.join(
      workspace,
      "config",
      "tenant",
      "zpa_application_segment.generated.expressions.json",
    )),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_application_segment.app_one\": {\n"
      + "      \"segment_group_id\": {\n"
      + "        \"expression\": \"module.zpa_segment_group.items[\\\"segment_one\\\"].id\",\n"
      + "        \"reason\": \"group-local reference binding via zpa_segment_group.items\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
});

test("nested pack references emit deterministic concrete indexed binding paths", () => {
  const result = deriveGeneratedBindings({
    context: {
      derived: new Set<string>(),
      generated: new Set(["zpa_app_connector_group", "zpa_server_group"]),
      mode: "cross_state",
      references: {
        "server_groups.id": {
          name_field: "name",
          referent: "zpa_server_group",
        },
      },
      resourceRoots: {
        zpa_app_connector_group: "zpa_app_connector_group",
        zpa_server_group: "zpa_server_group",
      },
    },
    items: {
      connector_one: {
        server_groups: [
          { id: ["sg-2", "sg-1"], name: "Second and first" },
          { id: ["sg-3"], name: "Third" },
        ],
      },
    },
    lookupKeys: {
      zpa_server_group: {
        "sg-1": "server_one",
        "sg-2": "server_two",
        "sg-3": "server_three",
      },
    },
    resourceType: "zpa_app_connector_group",
  });

  assert.equal(
    renderGeneratedBindings(result.data),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_app_connector_group.connector_one\": {\n"
      + "      \"server_groups[0].id\": {\n"
      + "        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_two\\\"], data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_one\\\"]]\",\n"
      + "        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n"
      + "      },\n"
      + "      \"server_groups[1].id\": {\n"
      + "        \"expression\": \"[data.terraform_remote_state.zpa_server_group.outputs.infrawright_reference_ids.zpa_server_group[\\\"server_three\\\"]]\",\n"
      + "        \"reason\": \"cross-state reference binding via zpa_server_group root output\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
  assert.deepEqual(result.notes, ["zpa_app_connector_group: 3 bound, 0 skipped"]);
});

test("nested pack references retain unresolved diagnostics without suppressing resolved siblings", () => {
  const result = deriveGeneratedBindings({
    context: {
      derived: new Set<string>(),
      generated: new Set(["zpa_app_connector_group", "zpa_server_group"]),
      mode: "same_root",
      references: {
        "server_groups.id": {
          name_field: "name",
          referent: "zpa_server_group",
        },
      },
      resourceRoots: {
        zpa_app_connector_group: "zpa_app",
        zpa_server_group: "zpa_app",
      },
    },
    items: {
      connector_one: {
        server_groups: [{ id: ["sg-known", "sg-missing"] }],
      },
    },
    lookupKeys: {
      zpa_server_group: { "sg-known": "known" },
    },
    resourceType: "zpa_app_connector_group",
  });

  assert.equal(
    renderGeneratedBindings(result.data),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_app_connector_group.connector_one\": {\n"
      + "      \"server_groups[0].id\": {\n"
      + "        \"expression\": \"[module.zpa_server_group.items[\\\"known\\\"].id, \\\"sg-missing\\\"]\",\n"
      + "        \"reason\": \"group-local reference binding via zpa_server_group.items\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
  assert.deepEqual(result.notes, [
    "zpa_app_connector_group.connector_one.server_groups[0].id[1] value \"sg-missing\" skipped; id is absent from zpa_server_group lookup",
    "zpa_app_connector_group: 1 bound, 1 skipped (id_absent=1)",
  ]);
});

test("top-level generated reference binding output remains byte-compatible", () => {
  const result = deriveGeneratedBindings({
    context: {
      derived: new Set<string>(),
      generated: new Set(["zpa_application_segment", "zpa_segment_group"]),
      mode: "same_root",
      references: {
        segment_group_id: {
          name_field: "name",
          referent: "zpa_segment_group",
        },
      },
      resourceRoots: {
        zpa_application_segment: "zpa_custom",
        zpa_segment_group: "zpa_custom",
      },
    },
    items: { app_one: { segment_group_id: "sg-1" } },
    lookupKeys: { zpa_segment_group: { "sg-1": "segment_one" } },
    resourceType: "zpa_application_segment",
  });

  assert.equal(
    renderGeneratedBindings(result.data),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_application_segment.app_one\": {\n"
      + "      \"segment_group_id\": {\n"
      + "        \"expression\": \"module.zpa_segment_group.items[\\\"segment_one\\\"].id\",\n"
      + "        \"reason\": \"group-local reference binding via zpa_segment_group.items\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
  assert.deepEqual(result.notes, ["zpa_application_segment: 1 bound, 0 skipped"]);
});

test("opt-in cross-state references bind singleton roots without grouping", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-cross-state-bindings-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zpa_segment_group.json"), [{
    enabled: true,
    id: "sg-1",
    name: "Segment One",
  }]);
  await writeJson(path.join(input, "zpa_application_segment.json"), [{
    domainNames: ["app.example.com"],
    id: "app-1",
    name: "App One",
    segmentGroupId: "sg-1",
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace, {
      roots: { zpa: { cross_state_references: true } },
    }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_application_segment", "zpa_segment_group"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  assert.equal(
    await text(path.join(
      workspace,
      "config",
      "tenant",
      "zpa_application_segment.generated.expressions.json",
    )),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_application_segment.app_one\": {\n"
      + "      \"segment_group_id\": {\n"
      + "        \"expression\": \"data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group[\\\"segment_one\\\"]\",\n"
      + "        \"reason\": \"cross-state reference binding via zpa_segment_group root output\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
});

test("committed ZPA nested references emit exact indexed cross-state bindings", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-zpa-nested-bindings-");
  const input = path.join(workspace, "input");
  await mkdir(input, { recursive: true });
  await copyFile(
    path.join(ROOT, "tests", "fixtures", "transform", "zpa_application_segment", "api.json"),
    path.join(input, "zpa_application_segment.json"),
  );
  await copyFile(
    path.join(ROOT, "tests", "fixtures", "transform", "zpa_server_group", "api.json"),
    path.join(input, "zpa_server_group.json"),
  );
  await writeJson(path.join(input, "zpa_app_connector_group.json"), [{
    id: "216199618143660001",
    latitude: "40.0",
    location: "fictional-dc-east",
    longitude: "-75.0",
    name: "Fictional Connector Group 1",
  }]);
  await writeJson(path.join(input, "zpa_application_server.json"), [
    { address: "10.0.1.10", id: "216199618143770001", name: "fictional-server-01.example.test" },
    { address: "10.0.1.11", id: "216199618143770002", name: "fictional-server-02.example.test" },
  ]);
  const result = await runTransformBatch({
    deployment: deployment(workspace, {
      roots: { zpa: { cross_state_references: true } },
    }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: [
      "zpa_app_connector_group",
      "zpa_application_segment",
      "zpa_application_server",
      "zpa_server_group",
    ],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const application = await text(path.join(
    workspace,
    "config",
    "tenant",
    "zpa_application_segment.generated.expressions.json",
  ));
  assert.match(application, /"server_groups\[0\]\.id"/u);
  assert.match(application, /terraform_remote_state\.zpa_server_group/u);
  const serverGroup = await text(path.join(
    workspace,
    "config",
    "tenant",
    "zpa_server_group.generated.expressions.json",
  ));
  assert.match(serverGroup, /"app_connector_groups\[0\]\.id"/u);
  assert.match(serverGroup, /terraform_remote_state\.zpa_app_connector_group/u);
  assert.match(serverGroup, /"servers\[0\]\.id"/u);
  assert.match(serverGroup, /terraform_remote_state\.zpa_application_server/u);

  const configDirectory = path.join(workspace, "config", "tenant");
  assert.deepEqual(
    JSON.parse(await text(path.join(configDirectory, "zpa_app_connector_group.lookup.json"))),
    {
      by_id: { "216199618143660001": "Fictional Connector Group 1" },
      key_by_id: { "216199618143660001": "fictional_connector_group_1" },
    },
  );
  assert.deepEqual(
    JSON.parse(await text(path.join(configDirectory, "zpa_application_server.lookup.json"))),
    {
      by_id: {
        "216199618143770001": "fictional-server-01.example.test",
        "216199618143770002": "fictional-server-02.example.test",
      },
      key_by_id: {
        "216199618143770001": "fictional_server_01_example_test",
        "216199618143770002": "fictional_server_02_example_test",
      },
    },
  );
  assert.deepEqual(
    JSON.parse(await text(path.join(configDirectory, "zpa_server_group.lookup.json"))),
    {
      by_id: { "216199618143550001": "Fictional Server Group Alpha" },
      key_by_id: { "216199618143550001": "fictional_server_group_alpha" },
    },
  );

  const disabled = await runTransformBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: [
      "zpa_app_connector_group",
      "zpa_application_segment",
      "zpa_application_server",
      "zpa_server_group",
    ],
    tenant: "tenant",
  });
  assert.deepEqual(disabled.failed, []);
  for (const resourceType of [
    "zpa_app_connector_group",
    "zpa_application_segment",
    "zpa_application_server",
    "zpa_server_group",
  ]) {
    assert.equal(
      await exists(path.join(configDirectory, `${resourceType}.generated.expressions.json`)),
      false,
    );
    assert.equal(await exists(path.join(configDirectory, `${resourceType}.lookup.json`)), false);
  }

  const freshDisabled = path.join(workspace, "fresh-disabled");
  assert.deepEqual((await runTransformBatch({
    deployment: deployment(freshDisabled),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: [
      "zpa_app_connector_group",
      "zpa_application_segment",
      "zpa_application_server",
      "zpa_server_group",
    ],
    tenant: "tenant",
  })).failed, []);
  assert.deepEqual(
    await snapshotFlatDirectory(configDirectory),
    await snapshotFlatDirectory(path.join(freshDisabled, "config", "tenant")),
  );
});

test("reference lookup inference is opt-in, unique, and subordinate to explicit sources", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-reference-lookup-inference-");
  const packs = path.join(workspace, "packs");
  await writeJson(path.join(packs, "sample", "pack.json"), {
    lookup_sources: {
      sample_explicit: { name_field: "display" },
    },
    provider_prefixes: { sample_: "sample" },
    references: {
      sample_referrer_one: {
        conflict: { name_field: "name", referent: "sample_conflict" },
        explicit: { name_field: "name", referent: "sample_explicit" },
      },
      sample_referrer_two: {
        conflict: { name_field: "label", referent: "sample_conflict" },
        explicit: { name_field: "label", referent: "sample_explicit" },
      },
    },
  });
  await writeJson(path.join(packs, "sample", "registry.json"), {
    sample_conflict: { generate: true, product: "sample" },
    sample_explicit: { generate: true, product: "sample" },
    sample_referrer_one: { generate: true, product: "sample" },
    sample_referrer_two: { generate: true, product: "sample" },
  });
  const attributes = {
    display: { optional: true, type: "string" },
    id: { computed: true, type: "string" },
    label: { optional: true, type: "string" },
    name: { optional: true, type: "string" },
  };
  await writeJson(path.join(packs, "sample", "schemas", "provider", "sample.json"), {
    resource_schemas: Object.fromEntries([
      "sample_conflict",
      "sample_explicit",
      "sample_referrer_one",
      "sample_referrer_two",
    ].map((resourceType) => [resourceType, { block: { attributes } }])),
  });
  const root = await loadPackRoot({ packsRoot: packs });
  const conflict = root.resources.get("sample_conflict");
  const explicit = root.resources.get("sample_explicit");
  assert.notEqual(conflict, undefined);
  assert.notEqual(explicit, undefined);
  const disabledDeployment = deployment(workspace);
  const enabledDeployment = deployment(workspace, {
    roots: { sample: { cross_state_references: true } },
  });

  assert.equal(transformLookupNameField(root, conflict!, disabledDeployment), null);
  assert.equal(transformLookupNameField(root, explicit!, disabledDeployment), "display");
  assert.equal(transformLookupNameField(root, explicit!, enabledDeployment), "display");
  assert.throws(
    () => transformLookupNameField(root, conflict!, enabledDeployment),
    /conflicting inferred reference lookup name fields.*sample_referrer_two\.conflict.*sample_referrer_one\.conflict.*explicit lookup_sources/u,
  );
});

test("cross-state list bindings preserve predefined ZIA values as literals", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-cross-state-list-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_url_categories.json"), [{
    configuredName: "Finance",
    customCategory: true,
    id: "CUSTOM_01",
    urls: ["finance.example.com"],
  }]);
  await writeJson(path.join(input, "zia_url_filtering_rules.json"), [{
    action: "BLOCK",
    id: 7,
    name: "Block Finance",
    order: 1,
    protocols: ["ANY_RULE"],
    urlCategories: ["CUSTOM_01", "GAMBLING"],
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace, {
      roots: { zia: { cross_state_references: true } },
    }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zia_url_categories", "zia_url_filtering_rules"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const binding = await text(path.join(
    workspace,
    "config",
    "tenant",
    "zia_url_filtering_rules.generated.expressions.json",
  ));
  assert.match(
    binding,
    /\[data\.terraform_remote_state\.zia_url_categories\.outputs\.infrawright_reference_ids\.zia_url_categories\[\\"finance\\"\], \\"GAMBLING\\"\]/u,
  );
});

test("HCL deployment writes lookup-derived comments", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-hcl-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_url_categories.json"), [{
    configuredName: "Finance\nOps",
    customCategory: true,
    id: "CUSTOM_01",
    urls: ["finance.example.com"],
  }]);
  await writeJson(path.join(input, "zia_url_filtering_rules.json"), [{
    action: "BLOCK",
    id: 7,
    name: "Block Finance",
    order: 1,
    protocols: ["ANY_RULE"],
    urlCategories: ["CUSTOM_01", "GAMBLING"],
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace, { hcl: true }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zia_url_filtering_rules", "zia_url_categories"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const rendered = await text(path.join(
    workspace,
    "config",
    "tenant",
    "zia_url_filtering_rules.auto.tfvars",
  ));
  assert.match(rendered, /"CUSTOM_01", # Finance Ops/);
  assert.match(rendered, /"GAMBLING", *# GAMBLING/);
  assert.equal(rendered.startsWith(
    "# Generated by infrawright. Do not edit; regenerate via make transform/adopt.\n",
  ), true);
});

test("derived reorder writes config only", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-derived-");
  const input = path.join(workspace, "input");
  await copyFile(
    path.join(DEMO_INPUT, "zpa_policy_access_rule.json"),
    path.join(await mkdir(input, { recursive: true }).then(() => input), "zpa_policy_access_rule.json"),
  );
  const result = await runTransformBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    tenant: "tenant",
  });
  assert.deepEqual(result, {
    failed: [],
    processed: ["zpa_policy_access_rule_reorder"],
    skipped: [],
  });
  const config = path.join(
    workspace,
    "config",
    "tenant",
    "zpa_policy_access_rule_reorder.auto.tfvars.json",
  );
  assert.match(await text(config), /"ACCESS_POLICY"/);
  assert.equal(await exists(path.join(
    workspace,
    "imports",
    "tenant",
    "zpa_policy_access_rule_reorder_imports.tf",
  )), false);

  await rm(config);
  let checkedImmediatelyBeforeWrite = false;
  const blocked = await runTransformBatch({
    beforeArtifactWrite: async () => {
      checkedImmediatelyBeforeWrite = true;
      throw new Error("pending move transition");
    },
    deployment: deployment(workspace),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    tenant: "tenant",
  });
  assert.equal(checkedImmediatelyBeforeWrite, true);
  assert.deepEqual(blocked.failed, ["zpa_policy_access_rule_reorder"]);
  assert.equal(await exists(config), false);
});

test("DROPS_CHECK records failure only after writing tfvars and imports", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-drops-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{
    id: 7,
    name: "Example",
    newlyObservedField: "must-review",
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace),
    environment: { DROPS_CHECK: "1" },
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  });
  assert.deepEqual(result, {
    dropCheckFailed: ["zia_rule_labels"],
    failed: ["zia_rule_labels"],
    processed: [],
    skipped: [],
  });
  assert.equal(await exists(path.join(
    workspace,
    "config",
    "tenant",
    "zia_rule_labels.auto.tfvars.json",
  )), true);
  assert.equal(await exists(path.join(
    workspace,
    "imports",
    "tenant",
    "zia_rule_labels_imports.tf",
  )), true);
});

test("switching tfvars formats removes the stale opposite artifact", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-stale-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: 7, name: "Example" }]);
  const root = await committedRoot();
  const common = {
    inputDirectory: input,
    root,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  } as const;
  const jsonPath = path.join(
    workspace,
    "config",
    "tenant",
    "zia_rule_labels.auto.tfvars.json",
  );
  const hclPath = jsonPath.slice(0, -".json".length);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace),
  })).failed, []);
  assert.equal(await exists(jsonPath), true);
  assert.equal(await exists(hclPath), false);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace, { hcl: true }),
  })).failed, []);
  assert.equal(await exists(jsonPath), false);
  assert.equal(await exists(hclPath), true);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace),
  })).failed, []);
  assert.equal(await exists(jsonPath), true);
  assert.equal(await exists(hclPath), false);
});

test("artifact paths retain the flat tenant/resource layout", () => {
  assert.deepEqual(transformArtifactPaths({
    deployment: deployment("overlay"),
    resourceType: "zia_rule_labels",
    tenant: "tenant",
  }), {
    config: path.join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars.json"),
    generatedBindings: path.join(
      "overlay",
      "config",
      "tenant",
      "zia_rule_labels.generated.expressions.json",
    ),
    imports: path.join("overlay", "imports", "tenant", "zia_rule_labels_imports.tf"),
    lookup: path.join("overlay", "config", "tenant", "zia_rule_labels.lookup.json"),
    moves: path.join("overlay", "imports", "tenant", "zia_rule_labels_moves.tf"),
    staleConfig: path.join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars"),
  });
});

test("batch artifacts consume the same later-pack metadata merge as ordering", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-merge-");
  const packs = path.join(workspace, "packs");
  await writeJson(path.join(packs, "alpha", "pack.json"), {
    lookup_sources: { sample_old: { name_field: "name" } },
    provider_prefixes: { sample_: "sample" },
    references: {
      sample_referrer: {
        target: { name_field: "name", referent: "sample_old" },
      },
    },
  });
  await writeJson(path.join(packs, "alpha", "registry.json"), {
    sample_old: { generate: true, product: "sample" },
    sample_referrer: { generate: true, product: "sample" },
  });
  await writeJson(path.join(packs, "alpha", "schemas", "provider", "sample.json"), {
    resource_schemas: {
      sample_old: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
        } },
      },
      sample_referrer: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
          target: { optional: true, type: "string" },
        } },
      },
    },
  });
  await writeJson(path.join(packs, "beta", "pack.json"), {
    lookup_sources: {
      other_referent: { name_field: "name" },
      sample_referrer: { name_field: "name" },
    },
    provider_prefixes: { other_: "other" },
    references: {
      sample_referrer: {
        target: { name_field: "name", referent: "other_referent" },
      },
    },
  });
  await writeJson(path.join(packs, "beta", "registry.json"), {
    other_referent: { generate: true, product: "other" },
  });
  await writeJson(path.join(packs, "beta", "schemas", "provider", "other.json"), {
    resource_schemas: {
      other_referent: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
        } },
      },
    },
  });
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "other_referent.json"), [{ id: "o1", name: "Other" }]);
  await writeJson(path.join(input, "sample_referrer.json"), [{
    id: "s1",
    name: "Sample",
    target: "o1",
  }]);
  const result = await runTransformBatch({
    deployment: deployment(path.join(workspace, "out"), { hcl: true }),
    inputDirectory: input,
    root: await loadPackRoot({ packsRoot: packs }),
    selectors: ["sample_referrer", "other_referent"],
    tenant: "tenant",
  });
  assert.deepEqual(result.processed, ["other_referent", "sample_referrer"]);
  assert.match(
    await text(path.join(
      workspace,
      "out",
      "config",
      "tenant",
      "sample_referrer.auto.tfvars",
    )),
    /target = "o1" # Other/,
  );
  assert.equal(await exists(path.join(
    workspace,
    "out",
    "config",
    "tenant",
    "sample_referrer.lookup.json",
  )), true);
});
