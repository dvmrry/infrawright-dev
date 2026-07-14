import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { access, chmod, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  loadAdoptionPolicy,
  runAdoptBatch,
  type AdoptionStateLoader,
} from "../node-src/domain/adopt-runner.js";
import type { Deployment } from "../node-src/domain/types.js";
import { isObject } from "../node-src/metadata/validation.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import { PerformanceRecorder } from "../node-src/performance/recorder.js";

const ROOT = process.cwd();
const PARITY = path.join(ROOT, "tests", "fixtures", "parity");
const PARITY_FIXTURES = [
  "zcc_failopen_policy_inversion.json",
  "zia_dlp_engines_predefined_name.json",
  "zia_url_filtering_rules_zero_quota.json",
  "zpa_application_segment_microtenant.json",
] as const;

let loadedRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(profile = "full.json"): Promise<LoadedPackRoot> {
  if (profile !== "full.json") {
    return loadPackRoot({
      packsRoot: path.join(ROOT, "packs"),
      profilePath: path.join(ROOT, "packsets", profile),
      catalogPath: path.join(ROOT, "packsets", "full.json"),
    });
  }
  loadedRoot ??= loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  return loadedRoot;
}

function deployment(overlay: string): Deployment {
  return { overlay, roots: {} };
}

async function reducedPackRoot(
  parent: string,
  name: string,
  packs: readonly string[],
  shared: readonly string[],
): Promise<string> {
  const destination = path.join(parent, `packs-${name}`);
  await mkdir(destination, { recursive: true });
  for (const pack of packs) {
    await cp(path.join(ROOT, "packs", pack), path.join(destination, pack), { recursive: true });
  }
  for (const component of shared) {
    await mkdir(path.join(destination, "_shared"), { recursive: true });
    await cp(
      path.join(ROOT, "packs", "_shared", component),
      path.join(destination, "_shared", component),
      { recursive: true },
    );
  }
  return destination;
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  prefix: string,
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix));
  context.after(() => rm(directory, { force: true, recursive: true }));
  return directory;
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

function record(value: unknown, label: string): Readonly<Record<string, unknown>> {
  if (!isObject(value)) throw new TypeError(`${label} must be an object`);
  return value;
}

function pythonExpected(fixture: string): { readonly imports: string; readonly tfvars: string } {
  const source = [
    "import json, sys",
    "from engine import adopt, transform",
    "from engine.adoption_meta import adoption_entry",
    "from engine.drift_policy import DriftPolicy",
    "from engine.transform_adopt_parity import load_fixture, _fixture_state_loader",
    "data = load_fixture(sys.argv[1])",
    "policy = DriftPolicy.load_for_adoption(None)",
    "items, originals = adopt.adopt_items(data['raw_items'], data['resource_type'], policy=policy, state_loader=_fixture_state_loader(data['provider_state']))",
    "override = {'import_id': adoption_entry(data['resource_type'])['import_id']}",
    "print(json.dumps({'tfvars': transform.render_tfvars(items), 'imports': transform.render_imports(data['resource_type'], originals, override)}))",
  ].join("; ");
  const result = spawnSync(PYTHON_ORACLE, ["-c", source, fixture], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout) as { readonly imports: string; readonly tfvars: string };
}

test("all four retained transform/adopt fixtures write byte-identical Python artifacts", async (context) => {
  const root = await committedRoot();
  for (const filename of PARITY_FIXTURES) {
    const fixturePath = path.join(PARITY, filename);
    const fixture = record(JSON.parse(await readFile(fixturePath, "utf8")), filename);
    const resourceType = String(fixture.resource_type);
    const rawItems = fixture.raw_items;
    assert.ok(Array.isArray(rawItems));
    const providerState = record(fixture.provider_state, `${filename}.provider_state`);
    const workspace = await temporaryDirectory(context, `infrawright-adopt-${resourceType}-`);
    const input = path.join(workspace, "pulls");
    await writeJson(path.join(input, `${resourceType}.json`), rawItems);
    const stateLoader: AdoptionStateLoader = async (request) => {
      const requested = new Set(request.keyToImportId.values());
      assert.deepEqual(requested, new Set(Object.keys(providerState)));
      return new Map([...request.keyToImportId].map(([key, importId]) => {
        const state = record(providerState[importId], `${filename}.${importId}`);
        return [key, {
          address: `${resourceType}.fixture`,
          sensitiveValues: state.sensitive_values ?? {},
          values: record(state.values, `${filename}.${importId}.values`),
        }];
      }));
    };
    const result = await runAdoptBatch({
      deployment: deployment(workspace),
      inputDirectory: input,
      policy: await loadAdoptionPolicy({ root }),
      root,
      selectors: [resourceType],
      stateLoader,
      tenant: "tenant",
    });
    assert.deepEqual(result.failed, [], resourceType);
    assert.deepEqual(result.processed, [resourceType], resourceType);
    const expected = pythonExpected(fixturePath);
    assert.equal(
      await readFile(path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`), "utf8"),
      expected.tfvars,
      `${resourceType} tfvars`,
    );
    assert.equal(
      await readFile(path.join(workspace, "imports", "tenant", `${resourceType}_imports.tf`), "utf8"),
      expected.imports,
      `${resourceType} imports`,
    );
  }
});

test("provider-state survivors own tfvars while raw identity owns keys/imports/lookups", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-lookup-");
  const input = path.join(workspace, "pulls");
  await writeJson(path.join(input, "zia_url_categories.json"), [{
    configuredName: "Raw Name",
    customCategory: true,
    id: "CUSTOM_01",
    urls: ["raw.example"],
  }]);
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: ["zia_url_categories"],
    stateLoader: async () => new Map([["raw_name", {
      address: "zia_url_categories.fixture",
      sensitiveValues: {},
      values: {
        configured_name: "Provider Name",
        custom_category: true,
        id: "CUSTOM_01",
        urls: ["provider.example"],
      },
    }]]),
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const tfvars = await readFile(path.join(workspace, "config", "tenant", "zia_url_categories.auto.tfvars.json"), "utf8");
  assert.equal(tfvars.includes("Provider Name"), true);
  assert.equal(tfvars.includes("provider.example"), true);
  assert.equal(tfvars.includes("raw.example"), false);
  const imports = await readFile(path.join(workspace, "imports", "tenant", "zia_url_categories_imports.tf"), "utf8");
  assert.equal(imports.includes('["raw_name"]'), true);
  assert.equal(imports.includes('id = "CUSTOM_01"'), true);
  const lookup = JSON.parse(await readFile(path.join(workspace, "config", "tenant", "zia_url_categories.lookup.json"), "utf8")) as {
    by_id: Record<string, string>;
    key_by_id: Record<string, string>;
  };
  assert.deepEqual(lookup, {
    by_id: { CUSTOM_01: "Provider Name" },
    key_by_id: { CUSTOM_01: "raw_name" },
  });
});

test("Adopt preserves unresolved move evidence on an identical rerun", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-durable-moves-");
  const input = path.join(workspace, "pulls");
  const source = path.join(input, "zia_rule_labels.json");
  const root = await committedRoot();
  let currentName = "Original Name";
  const stateLoader: AdoptionStateLoader = async (request) => new Map(
    [...request.keyToImportId].map(([key]) => [key, {
      address: "zia_rule_labels.fixture",
      sensitiveValues: {},
      values: { id: "1", name: currentName },
    }]),
  );
  const options = {
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: ["zia_rule_labels"],
    stateLoader,
    tenant: "tenant",
  } as const;

  await writeJson(source, [{ id: "1", name: currentName }]);
  assert.deepEqual((await runAdoptBatch(options)).failed, []);
  currentName = "Renamed Thing";
  await writeJson(source, [{ id: "1", name: currentName }]);
  assert.deepEqual((await runAdoptBatch(options)).failed, []);
  const moves = path.join(workspace, "imports", "tenant", "zia_rule_labels_moves.tf");
  const moveBytes = await readFile(moves);

  assert.deepEqual((await runAdoptBatch(options)).failed, []);
  assert.deepEqual(await readFile(moves), moveBytes);
});

test("escaped-brace import templates survive the complete adoption artifact path", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-escaped-template-");
  const input = path.join(workspace, "pulls");
  const resourceType = "zia_url_categories";
  await writeJson(path.join(input, `${resourceType}.json`), [{
    configuredName: "Escaped",
    customCategory: true,
    id: "CUSTOM_07",
    urls: [],
  }]);
  const baseRoot = await committedRoot();
  const baseResource = baseRoot.resources.get(resourceType);
  assert.notEqual(baseResource, undefined);
  const resources = new Map(baseRoot.resources);
  resources.set(resourceType, {
    ...baseResource!,
    override: {
      ...record(baseResource!.override, "zia_url_categories.override"),
      import_id: "{{tenant}}:{id}",
    },
  });
  const root = { ...baseRoot, resources } as LoadedPackRoot;
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: [resourceType],
    stateLoader: async () => new Map([["escaped", {
      address: `${resourceType}.fixture`,
      sensitiveValues: {},
      values: { configured_name: "Escaped", custom_category: true, id: "CUSTOM_07", urls: [] },
    }]]),
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const imports = await readFile(path.join(workspace, "imports", "tenant", `${resourceType}_imports.tf`), "utf8");
  assert.equal(imports.includes('id = "{tenant}:CUSTOM_07"'), true);
});

test("batch aggregation retains successful outputs and skips missing pulls", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-batch-");
  const input = path.join(workspace, "pulls");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "Good" }]);
  await writeJson(path.join(input, "zia_url_categories.json"), [{ configuredName: "Bad", id: "BAD" }]);
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: ["zia_rule_labels", "zia_url_categories", "zia_ssl_inspection_rules"],
    stateLoader: async (request) => {
      if (request.resourceType === "zia_url_categories") throw new Error("fixture failure");
      return new Map([["good", {
        address: "zia_rule_labels.fixture",
        sensitiveValues: {},
        values: { id: "1", name: "Good" },
      }]]);
    },
    tenant: "tenant",
  });
  assert.deepEqual(result.processed, ["zia_rule_labels"]);
  assert.deepEqual(result.failed, ["zia_url_categories"]);
  assert.deepEqual(result.skipped, ["zia_ssl_inspection_rules"]);
  await access(path.join(workspace, "config", "tenant", "zia_rule_labels.auto.tfvars.json"));
});

test("Oracle coverage mismatch and pending move transitions block artifact mutation", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-transition-");
  const input = path.join(workspace, "pulls");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "One" }]);
  const root = await committedRoot();
  const base = {
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  } as const;
  const incomplete = await runAdoptBatch({
    ...base,
    stateLoader: async () => new Map(),
  });
  assert.deepEqual(incomplete.failed, ["zia_rule_labels"]);
  await assert.rejects(
    () => access(path.join(workspace, "config", "tenant", "zia_rule_labels.auto.tfvars.json")),
    /ENOENT/,
  );

  const pending = path.join(workspace, "imports", "tenant", "zia_rule_labels_moves.pending.json");
  await writeJson(pending, { transition: true });
  let called = false;
  const blocked = await runAdoptBatch({
    ...base,
    stateLoader: async () => {
      called = true;
      return new Map();
    },
  });
  assert.equal(called, false);
  assert.deepEqual(blocked.failed, ["zia_rule_labels"]);
  await rm(pending);

  const appeared = await runAdoptBatch({
    ...base,
    stateLoader: async () => {
      await writeJson(pending, { transition: true });
      return new Map([["one", {
        address: "zia_rule_labels.fixture",
        sensitiveValues: {},
        values: { id: "1", name: "One" },
      }]]);
    },
  });
  assert.deepEqual(appeared.failed, ["zia_rule_labels"]);
  await assert.rejects(
    () => access(path.join(workspace, "config", "tenant", "zia_rule_labels.auto.tfvars.json")),
    /ENOENT/,
  );
});

test("derived resources delegate to the generic transform path without invoking the Oracle", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-derived-");
  const input = path.join(workspace, "pulls");
  await writeJson(path.join(input, "zpa_policy_access_rule.json"), [
    { id: "two", name: "Two", ruleOrder: 2 },
    { id: "one", name: "One", ruleOrder: 1 },
  ]);
  let called = false;
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    stateLoader: async () => {
      called = true;
      return new Map();
    },
    tenant: "tenant",
  });
  assert.equal(called, false);
  assert.deepEqual(result.failed, []);
  assert.deepEqual(result.processed, ["zpa_policy_access_rule_reorder"]);
  await access(path.join(workspace, "config", "tenant", "zpa_policy_access_rule_reorder.auto.tfvars.json"));

  await writeJson(path.join(input, "zpa_policy_access_rule.json"), [
    { id: "missing-order", name: "Missing Order" },
  ]);
  const performance = new PerformanceRecorder();
  const malformed = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    performance,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    stateLoader: async () => {
      called = true;
      return new Map();
    },
    tenant: "tenant",
  });
  assert.deepEqual(malformed.failed, ["zpa_policy_access_rule_reorder"]);
  const report = performance.report({
    command: "adopt",
    commandDurationMs: 1,
    commandStatus: "failed",
  });
  assert.deepEqual(
    (report.spans as Array<{ phase: string; resource_family?: string; status: string }>).filter(
      (span) => span.phase === "adopt.resource",
    ).map((span) => [span.resource_family, span.status]),
    [["zpa_policy_access_rule_reorder", "failed"]],
  );

  await rm(path.join(workspace, "config", "tenant", "zpa_policy_access_rule_reorder.auto.tfvars.json"));
  const pending = path.join(workspace, "imports", "tenant", "zpa_policy_access_rule_reorder_moves.pending.json");
  await writeJson(pending, { transition: true });
  const blocked = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    stateLoader: async () => {
      called = true;
      return new Map();
    },
    tenant: "tenant",
  });
  assert.deepEqual(blocked.failed, ["zpa_policy_access_rule_reorder"]);
  await assert.rejects(
    () => access(path.join(workspace, "config", "tenant", "zpa_policy_access_rule_reorder.auto.tfvars.json")),
    /ENOENT/,
  );
});

test("empty, provider, Zscaler, full, and reduced profiles select cleanly with no pulls", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-profiles-");
  const cases = [
    { profile: "empty.json", packs: [], shared: [] },
    { profile: "zia.json", packs: ["zia"], shared: ["zscaler"] },
    { profile: "zscaler.json", packs: ["zcc", "zia", "zpa", "ztc"], shared: ["zscaler"] },
    { profile: "zcc.json", packs: ["zcc"], shared: ["zscaler"] },
  ] as const;
  const roots: Array<{ readonly profile: string; readonly root: LoadedPackRoot }> = [{
    profile: "full.json",
    root: await committedRoot(),
  }];
  for (const entry of cases) {
    const packsRoot = await reducedPackRoot(workspace, entry.profile, entry.packs, entry.shared);
    roots.push({
      profile: entry.profile,
      root: await loadPackRoot({
        packsRoot,
        profilePath: path.join(ROOT, "packsets", entry.profile),
        catalogPath: path.join(ROOT, "packsets", "full.json"),
      }),
    });
  }
  for (const entry of roots) {
    const selectedRoot = entry.root;
    const result = await runAdoptBatch({
      deployment: deployment(workspace),
      inputDirectory: path.join(workspace, entry.profile),
      policy: await loadAdoptionPolicy({ root: selectedRoot }),
      root: selectedRoot,
      selectors: [],
      stateLoader: async () => {
        throw new Error("empty pull profile must not invoke Oracle");
      },
      tenant: "tenant",
    });
    assert.deepEqual(result.failed, [], entry.profile);
    assert.equal(result.processed.length, 0, entry.profile);
  }
});

test("make adopt is Python-disabled and executes against injected fake Terraform", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-make-adopt-");
  const input = path.join(workspace, "pulls");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "77", name: "Make Fixture" }]);
  const deploymentPath = path.join(workspace, "deployment.json");
  await writeJson(deploymentPath, { overlay: workspace, roots: {} });
  const fakeTerraform = path.join(workspace, "terraform-fake.mjs");
  await writeFile(fakeTerraform, `#!/usr/bin/env node
import fs from "node:fs";
const args = process.argv.slice(2);
const imports = fs.existsSync("imports.tf") ? fs.readFileSync("imports.tf", "utf8") : "";
const address = /to = ([^\\s]+)/u.exec(imports)?.[1] ?? "zia_rule_labels.iw_unknown";
const name = address.split(".")[1];
if (args[0] === "plan") {
  const generated = args.find((item) => item.startsWith("-generate-config-out="));
  if (generated) fs.writeFileSync(generated.slice(generated.indexOf("=") + 1), 'resource "zia_rule_labels" "' + name + '" {\\n  name = "Make Fixture"\\n}\\n');
  const out = args.find((item) => item.startsWith("-out="));
  if (out) fs.writeFileSync(out.slice(5), "fake");
  process.exit(0);
}
if (args[0] === "show" && args.at(-1)?.endsWith(".tfplan")) {
  process.stdout.write(JSON.stringify({applyable:true,complete:true,errored:false,format_version:"1.2",terraform_version:"1.15.4",resource_changes:[{address,change:{actions:["no-op"],importing:{id:"77"}},mode:"managed",provider_name:"registry.terraform.io/zscaler/zia",type:"zia_rule_labels"}]}));
  process.exit(0);
}
if (args[0] === "show") {
  process.stdout.write(JSON.stringify({format_version:"1.0",terraform_version:"1.15.4",values:{root_module:{resources:[{address,mode:"managed",provider_name:"registry.terraform.io/zscaler/zia",type:"zia_rule_labels",values:{id:"77",name:"Make Fixture"},sensitive_values:{}}]}}}));
  process.exit(0);
}
process.exit(0);
`);
  await chmod(fakeTerraform, 0o700);
  const packsRoot = await reducedPackRoot(workspace, "make-zia", ["zia"], ["zscaler"]);
  const result = spawnSync("make", [
    "adopt",
    `IN=${input}`,
    "TENANT=tenant",
    'RESOURCE=zia_rule_labels',
    `TF=${fakeTerraform}`,
    `DEPLOYMENT=${deploymentPath}`,
    "PACK_PROFILE=packsets/zia.json",
    "PACK_CATALOG=packsets/full.json",
    "PYTHON=/python-must-not-run",
  ], {
    cwd: ROOT,
    encoding: "utf8",
    env: {
      ...process.env,
      INFRAWRIGHT_DEPLOYMENT: deploymentPath,
      INFRAWRIGHT_PACKS: packsRoot,
    },
  });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  assert.equal(`${result.stdout}${result.stderr}`.includes("python-must-not-run"), false);
  await access(path.join(workspace, "config", "tenant", "zia_rule_labels.auto.tfvars.json"));
  await access(path.join(workspace, "imports", "tenant", "zia_rule_labels_imports.tf"));
});
