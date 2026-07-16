import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { access, chmod, cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  loadAdoptionPolicy,
  oracleBatchMode,
  runAdoptBatch,
  type AdoptionBatchStateLoader,
  type AdoptionStateLoader,
} from "../node-src/domain/adopt-runner.js";
import {
  extractAcceptedPlanState,
  oracleAddress,
  type OracleStateObject,
} from "../node-src/domain/import-oracle.js";
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
const ZIA_BATCH_MEMBERS = ["zia_rule_labels", "zia_url_categories"] as const;

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

function groupedZiaDeployment(overlay: string): Deployment {
  return {
    overlay,
    roots: {
      zia: {
        groups: { zia_adoption_batch: ZIA_BATCH_MEMBERS },
      },
    },
  };
}

function unsupportedZiaDeployment(overlay: string): Deployment {
  return {
    overlay,
    roots: {
      zia: {
        groups: {
          zia_unsupported_preflight: ["zia_rule_labels", "zia_url_filtering_rules"],
        },
      },
    },
  };
}

function slugZpaDeployment(overlay: string): Deployment {
  return {
    overlay,
    tfvars_format: "hcl",
    roots: {
      zpa: {
        bind_references: true,
        strategy: "slug",
      },
    },
  };
}

function groupedZpaReferenceDeployment(
  overlay: string,
  bindReferences = true,
): Deployment {
  return {
    overlay,
    roots: {
      zpa: {
        ...(bindReferences ? { bind_references: true } : {}),
        groups: {
          zpa_reference_batch: [
            "zpa_app_connector_group",
            "zpa_application_server",
            "zpa_server_group",
          ],
        },
        strategy: "explicit",
      },
    },
  };
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

async function snapshotTree(directory: string): Promise<Readonly<Record<string, string>>> {
  const files: Array<readonly [string, string]> = [];
  const visit = async (current: string): Promise<void> => {
    for (const entry of await readdir(current, { withFileTypes: true })) {
      const absolute = path.join(current, entry.name);
      if (entry.isDirectory()) {
        await visit(absolute);
      } else if (entry.isFile()) {
        files.push([path.relative(directory, absolute), await readFile(absolute, "utf8")]);
      }
    }
  };
  await visit(directory);
  return Object.fromEntries(files.sort(([left], [right]) => left.localeCompare(right)));
}

async function writeZiaBatchPulls(input: string): Promise<void> {
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "Batch Label" }]);
  await writeJson(path.join(input, "zia_url_categories.json"), [{
    configuredName: "Batch Category",
    customCategory: true,
    id: "CUSTOM_01",
    urls: ["batch.example"],
  }]);
}

function adoptionState(
  resourceType: string,
  importId: string,
): OracleStateObject {
  if (resourceType === "zia_rule_labels") {
    return {
      address: `${resourceType}.fixture`,
      sensitiveValues: {},
      values: { id: importId, name: "Batch Label" },
    };
  }
  if (resourceType === "zia_url_categories") {
    return {
      address: `${resourceType}.fixture`,
      sensitiveValues: {},
      values: {
        configured_name: "Batch Category",
        custom_category: true,
        id: importId,
        urls: ["batch.example"],
      },
    };
  }
  throw new TypeError(`unexpected adoption fixture resource ${resourceType}`);
}

const ziaBatchStateLoader: AdoptionStateLoader = async (request) => new Map(
  [...request.keyToImportId].map(([key, importId]) => [
    key,
    adoptionState(request.resourceType, importId),
  ]),
);

const ziaLogicalRootStateLoader: AdoptionBatchStateLoader = async (request) => new Map(
  request.resources.map((resource) => [
    resource.resourceType,
    new Map([...resource.keyToImportId].map(([key, importId]) => [
      key,
      adoptionState(resource.resourceType, importId),
    ])),
  ]),
);

async function writeZpaReferenceOrderingPulls(input: string): Promise<void> {
  await Promise.all([
    writeJson(path.join(input, "zpa_application_server.json"), [{
      address: "10.0.0.1",
      id: "server-id",
      name: "Server",
    }]),
    writeJson(path.join(input, "zpa_segment_group.json"), [{
      id: "sg-id",
      name: "Segment Group",
    }]),
    writeJson(path.join(input, "zpa_application_segment.json"), [{
      domain_names: ["app.example"],
      id: "app-id",
      name: "App",
      segment_group_id: "sg-id",
    }]),
  ]);
}

const zpaReferenceStateLoader: AdoptionStateLoader = async (request) => new Map(
  [...request.keyToImportId].map(([key, importId]) => {
    let values: Readonly<Record<string, unknown>>;
    if (request.resourceType === "zpa_application_server") {
      values = { address: "10.0.0.1", id: importId, name: "Server" };
    } else if (request.resourceType === "zpa_segment_group") {
      values = { id: importId, name: "Segment Group" };
    } else if (request.resourceType === "zpa_application_segment") {
      values = {
        domain_names: ["app.example"],
        id: importId,
        name: "App",
        segment_group_id: "sg-id",
      };
    } else {
      throw new TypeError(`unexpected ZPA reference fixture resource ${request.resourceType}`);
    }
    return [key, {
      address: `${request.resourceType}.fixture`,
      sensitiveValues: {},
      values,
    } satisfies OracleStateObject] as const;
  }),
);

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

function providerName(source: string): string {
  return source.split("/").length === 2
    ? `registry.terraform.io/${source}`
    : source;
}

function acceptedPlanStateLoader(options: {
  readonly providerState: Readonly<Record<string, unknown>>;
  readonly resourceType: string;
  readonly root: LoadedPackRoot;
}): AdoptionStateLoader {
  return async (request) => {
    const resource = options.root.resources.get(options.resourceType);
    assert.notEqual(resource, undefined);
    const expectedProvider = providerName(
      options.root.packs.providerSources[resource!.provider] ?? "",
    );
    const addressToKey = new Map<string, string>();
    const expectedImports = new Map<string, string>();
    const observations: Record<string, unknown>[] = [];
    const changes: Record<string, unknown>[] = [];
    for (const [key, importId] of [...request.keyToImportId].sort()) {
      const address = oracleAddress(options.resourceType, key);
      const state = record(
        options.providerState[importId],
        `${options.resourceType}.${importId}`,
      );
      const values = record(state.values, `${options.resourceType}.${importId}.values`);
      const sensitiveValues = state.sensitive_values ?? {};
      const observation = {
        address,
        mode: "managed",
        provider_name: expectedProvider,
        sensitive_values: sensitiveValues,
        type: options.resourceType,
        values,
      };
      addressToKey.set(address, key);
      expectedImports.set(address, importId);
      observations.push(observation);
      changes.push({
        address,
        change: {
          actions: ["no-op"],
          after: values,
          after_sensitive: sensitiveValues,
          after_unknown: {},
          before: values,
          before_sensitive: sensitiveValues,
          importing: { id: importId },
        },
        mode: "managed",
        provider_name: expectedProvider,
        type: options.resourceType,
      });
    }
    return extractAcceptedPlanState({
      addressToKey,
      expectedImports,
      plan: {
        applyable: true,
        complete: true,
        errored: false,
        format_version: "1.2",
        planned_values: { root_module: { resources: observations } },
        prior_state: {
          format_version: "1.0",
          terraform_version: "1.15.4",
          values: { root_module: { resources: observations } },
        },
        resource_changes: changes,
        terraform_version: "1.15.4",
      },
      providerName: expectedProvider,
      resourceType: options.resourceType,
    });
  };
}

test("logical-root Oracle batching is opt-in and validates its environment value", () => {
  assert.equal(oracleBatchMode({}), "per-resource-type");
  assert.equal(
    oracleBatchMode({ INFRAWRIGHT_ORACLE_BATCH_MODE: "per-resource-type" }),
    "per-resource-type",
  );
  assert.equal(
    oracleBatchMode({ INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" }),
    "logical-root",
  );
  assert.throws(
    () => oracleBatchMode({ INFRAWRIGHT_ORACLE_BATCH_MODE: "root" }),
    /must be per-resource-type or logical-root/u,
  );
});

test("logical-root adoption uses one batch Oracle and preserves per-resource artifact bytes", async (context) => {
  const root = await committedRoot();
  const policy = await loadAdoptionPolicy({ root });
  const legacyWorkspace = await temporaryDirectory(context, "infrawright-adopt-per-resource-");
  const batchWorkspace = await temporaryDirectory(context, "infrawright-adopt-logical-root-");
  const legacyInput = path.join(legacyWorkspace, "pulls");
  const batchInput = path.join(batchWorkspace, "pulls");
  await Promise.all([writeZiaBatchPulls(legacyInput), writeZiaBatchPulls(batchInput)]);

  let legacyCalls = 0;
  const legacy = await runAdoptBatch({
    batchStateLoader: async () => {
      throw new Error("per-resource mode must not invoke the batch Oracle");
    },
    deployment: groupedZiaDeployment(legacyWorkspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "per-resource-type" },
    inputDirectory: legacyInput,
    policy,
    root,
    selectors: [...ZIA_BATCH_MEMBERS],
    stateLoader: async (request) => {
      legacyCalls += 1;
      return ziaBatchStateLoader(request);
    },
    tenant: "tenant",
  });
  assert.deepEqual(legacy.failed, []);
  assert.equal(legacyCalls, ZIA_BATCH_MEMBERS.length);

  let batchCalls = 0;
  const batched = await runAdoptBatch({
    batchStateLoader: async (request) => {
      batchCalls += 1;
      assert.deepEqual(
        request.resources.map((resource) => resource.resourceType),
        [...ZIA_BATCH_MEMBERS],
      );
      return ziaLogicalRootStateLoader(request);
    },
    deployment: groupedZiaDeployment(batchWorkspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: batchInput,
    policy,
    root,
    selectors: [...ZIA_BATCH_MEMBERS],
    stateLoader: async () => {
      throw new Error("logical-root mode must not invoke the per-resource Oracle");
    },
    tenant: "tenant",
  });
  assert.deepEqual(batched.failed, []);
  assert.equal(batchCalls, 1);
  assert.deepEqual(
    [...batched.processed].sort(),
    [...legacy.processed].sort(),
  );
  assert.deepEqual(
    await snapshotTree(path.join(batchWorkspace, "config", "tenant")),
    await snapshotTree(path.join(legacyWorkspace, "config", "tenant")),
  );
  assert.deepEqual(
    await snapshotTree(path.join(batchWorkspace, "imports", "tenant")),
    await snapshotTree(path.join(legacyWorkspace, "imports", "tenant")),
  );
});

test("logical-root fallback preserves implicit root members, external referent order, and ZPA HCL bytes", async (context) => {
  const root = await committedRoot();
  const policy = await loadAdoptionPolicy({ root });
  const legacyWorkspace = await temporaryDirectory(context, "infrawright-adopt-zpa-order-legacy-");
  const batchWorkspace = await temporaryDirectory(context, "infrawright-adopt-zpa-order-batch-");
  const legacyInput = path.join(legacyWorkspace, "pulls");
  const batchInput = path.join(batchWorkspace, "pulls");
  await Promise.all([
    writeZpaReferenceOrderingPulls(legacyInput),
    writeZpaReferenceOrderingPulls(batchInput),
  ]);
  const selectors = [
    "zpa_application_server",
    "zpa_segment_group",
    "zpa_application_segment",
  ];

  const legacyOrder: string[] = [];
  const legacy = await runAdoptBatch({
    batchStateLoader: async () => {
      throw new Error("per-resource mode must not invoke the batch Oracle");
    },
    deployment: slugZpaDeployment(legacyWorkspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "per-resource-type" },
    inputDirectory: legacyInput,
    policy,
    root,
    selectors,
    stateLoader: async (request) => {
      legacyOrder.push(request.resourceType);
      return zpaReferenceStateLoader(request);
    },
    tenant: "tenant",
  });

  let batchCalls = 0;
  const optimizedOrder: string[] = [];
  const diagnostics: string[] = [];
  const optimized = await runAdoptBatch({
    batchStateLoader: async () => {
      batchCalls += 1;
      throw new Error("a root with a pending external referent must not batch");
    },
    deployment: slugZpaDeployment(batchWorkspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: batchInput,
    onDiagnostic: (message) => diagnostics.push(message),
    policy,
    root,
    selectors: ["zpa_application_server", "zpa_segment_group"],
    stateLoader: async (request) => {
      optimizedOrder.push(request.resourceType);
      return zpaReferenceStateLoader(request);
    },
    tenant: "tenant",
  });

  assert.deepEqual(legacy.failed, []);
  assert.deepEqual(optimized.failed, []);
  assert.equal(batchCalls, 0);
  assert.deepEqual(optimizedOrder, legacyOrder);
  assert.deepEqual(legacyOrder, selectors);
  assert.ok(diagnostics.some((message) => {
    return message.includes("selecting zpa_application_server selects whole root zpa_application");
  }));
  assert.deepEqual(
    await snapshotTree(path.join(batchWorkspace, "config", "tenant")),
    await snapshotTree(path.join(legacyWorkspace, "config", "tenant")),
  );
  assert.deepEqual(
    await snapshotTree(path.join(batchWorkspace, "imports", "tenant")),
    await snapshotTree(path.join(legacyWorkspace, "imports", "tenant")),
  );
  assert.match(
    await readFile(
      path.join(
        batchWorkspace,
        "config",
        "tenant",
        "zpa_application_segment.auto.tfvars",
      ),
      "utf8",
    ),
    /segment_group_id = "sg-id" # Segment Group/u,
  );
  assert.deepEqual(
    JSON.parse(await readFile(
      path.join(batchWorkspace, "config", "tenant", "zpa_application_server.lookup.json"),
      "utf8",
    )),
    {
      by_id: { "server-id": "Server" },
      key_by_id: { "server-id": "server" },
    },
  );
});

test("logical-root adoption batch publishes inferred referent lookups and nested bindings", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-zpa-reference-batch-");
  const input = path.join(workspace, "pulls");
  await Promise.all([
    writeJson(path.join(input, "zpa_app_connector_group.json"), [{
      id: "connector-id",
      latitude: "40.0",
      location: "dc-east",
      longitude: "-75.0",
      name: "Connector",
    }]),
    writeJson(path.join(input, "zpa_application_server.json"), [{
      address: "10.0.0.1",
      id: "server-id",
      name: "Server",
    }]),
    writeJson(path.join(input, "zpa_server_group.json"), [{
      appConnectorGroups: [{ id: "connector-id" }],
      dynamicDiscovery: false,
      enabled: true,
      id: "group-id",
      name: "Server Group",
      servers: [{ id: "server-id" }],
    }]),
  ]);
  const root = await committedRoot();
  let batchCalls = 0;
  const batchStateLoader: AdoptionBatchStateLoader = async (request) => {
    batchCalls += 1;
    return new Map(request.resources.map((resource) => [
        resource.resourceType,
        new Map([...resource.keyToImportId].map(([key, importId]) => {
          let values: Readonly<Record<string, unknown>>;
          if (resource.resourceType === "zpa_app_connector_group") {
            values = {
              id: importId,
              latitude: "40.0",
              location: "dc-east",
              longitude: "-75.0",
              name: "Connector",
            };
          } else if (resource.resourceType === "zpa_application_server") {
            values = { address: "10.0.0.1", id: importId, name: "Server" };
          } else if (resource.resourceType === "zpa_server_group") {
            values = {
              app_connector_groups: [{ id: ["connector-id"] }],
              dynamic_discovery: false,
              enabled: true,
              id: importId,
              name: "Server Group",
              servers: [{ id: ["server-id"] }],
            };
          } else {
            throw new TypeError(`unexpected ZPA reference batch resource ${resource.resourceType}`);
          }
          return [key, {
            address: `${resource.resourceType}.fixture`,
            sensitiveValues: {},
            values,
          } satisfies OracleStateObject] as const;
        })),
      ]));
  };
  const result = await runAdoptBatch({
    batchStateLoader,
    deployment: groupedZpaReferenceDeployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: [
      "zpa_app_connector_group",
      "zpa_application_server",
      "zpa_server_group",
    ],
    stateLoader: async () => {
      throw new Error("logical-root reference batch must not invoke the per-resource Oracle");
    },
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  assert.equal(batchCalls, 1);
  const configDirectory = path.join(workspace, "config", "tenant");
  for (const [resourceType, id, name, key] of [
    ["zpa_app_connector_group", "connector-id", "Connector", "connector"],
    ["zpa_application_server", "server-id", "Server", "server"],
    ["zpa_server_group", "group-id", "Server Group", "server_group"],
  ] as const) {
    assert.deepEqual(
      JSON.parse(await readFile(path.join(configDirectory, `${resourceType}.lookup.json`), "utf8")),
      { by_id: { [id]: name }, key_by_id: { [id]: key } },
    );
  }
  const binding = await readFile(
    path.join(configDirectory, "zpa_server_group.generated.expressions.json"),
    "utf8",
  );
  assert.match(binding, /"app_connector_groups\[0\]\.id"/u);
  assert.equal(binding.includes('module.zpa_app_connector_group.items[\\"connector\\"].id'), true);
  assert.match(binding, /"servers\[0\]\.id"/u);
  assert.equal(binding.includes('module.zpa_application_server.items[\\"server\\"].id'), true);

  const disabled = await runAdoptBatch({
    batchStateLoader,
    deployment: groupedZpaReferenceDeployment(workspace, false),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: [
      "zpa_app_connector_group",
      "zpa_application_server",
      "zpa_server_group",
    ],
    stateLoader: async () => {
      throw new Error("logical-root reference batch must not invoke the per-resource Oracle");
    },
    tenant: "tenant",
  });
  assert.deepEqual(disabled.failed, []);
  assert.equal(batchCalls, 2);
  for (const resourceType of [
    "zpa_app_connector_group",
    "zpa_application_server",
    "zpa_server_group",
  ]) {
    await assert.rejects(
      readFile(path.join(configDirectory, `${resourceType}.lookup.json`), "utf8"),
      { code: "ENOENT" },
    );
  }
  await assert.rejects(
    readFile(path.join(configDirectory, "zpa_server_group.generated.expressions.json"), "utf8"),
    { code: "ENOENT" },
  );
});

test("logical-root member failure leaves every member artifact unchanged", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-batch-atomic-");
  const input = path.join(workspace, "pulls");
  const config = path.join(workspace, "config", "tenant");
  const imports = path.join(workspace, "imports", "tenant");
  await writeZiaBatchPulls(input);
  await Promise.all([mkdir(config, { recursive: true }), mkdir(imports, { recursive: true })]);
  for (const resourceType of ZIA_BATCH_MEMBERS) {
    await writeFile(
      path.join(config, `${resourceType}.auto.tfvars.json`),
      `existing ${resourceType} config\n`,
    );
    await writeFile(
      path.join(imports, `${resourceType}_imports.tf`),
      `existing ${resourceType} imports\n`,
    );
  }
  const before = {
    config: await snapshotTree(config),
    imports: await snapshotTree(imports),
  };
  const result = await runAdoptBatch({
    batchStateLoader: async (request) => {
      const states = await ziaLogicalRootStateLoader(request);
      return new Map<string, ReadonlyMap<string, OracleStateObject>>([
        ["zia_rule_labels", states.get("zia_rule_labels")!],
        ["zia_url_categories", new Map()],
      ]);
    },
    deployment: groupedZiaDeployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: [...ZIA_BATCH_MEMBERS],
    stateLoader: async () => {
      throw new Error("logical-root mode must not invoke the per-resource Oracle");
    },
    tenant: "tenant",
  });
  assert.deepEqual([...result.failed].sort(), [...ZIA_BATCH_MEMBERS].sort());
  assert.deepEqual(result.processed, []);
  assert.deepEqual(await snapshotTree(config), before.config);
  assert.deepEqual(await snapshotTree(imports), before.imports);
});

test("logical-root unsupported preflight covers every member and invokes no Oracle", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-unsupported-root-");
  const input = path.join(workspace, "pulls");
  const config = path.join(workspace, "config", "tenant");
  const imports = path.join(workspace, "imports", "tenant");
  await Promise.all([
    writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "Safe Label" }]),
    writeJson(path.join(input, "zia_url_filtering_rules.json"), [
      { action: "ISOLATE", predefined: true },
      { action: "ISOLATE", id: 2, name: "Unsupported One", predefined: false },
      { action: "ISOLATE", id: 3, name: "Unsupported Two", predefined: false },
      { action: "BLOCK", id: 4, name: "Eligible Rule", predefined: false },
    ]),
    mkdir(config, { recursive: true }),
    mkdir(imports, { recursive: true }),
  ]);
  for (const resourceType of ["zia_rule_labels", "zia_url_filtering_rules"]) {
    await writeFile(
      path.join(config, `${resourceType}.auto.tfvars.json`),
      `existing ${resourceType} config\n`,
    );
    await writeFile(
      path.join(imports, `${resourceType}_imports.tf`),
      `existing ${resourceType} imports\n`,
    );
  }
  await writeFile(
    path.join(imports, "zia_rule_labels_moves.pending.json"),
    '{"safe_marker":"pending"}\n',
  );
  const before = {
    config: await snapshotTree(config),
    imports: await snapshotTree(imports),
  };
  const diagnostics: string[] = [];
  let batchCalls = 0;
  let resourceCalls = 0;
  const root = await committedRoot();
  const result = await runAdoptBatch({
    batchStateLoader: async () => {
      batchCalls += 1;
      throw new Error("unsupported root must not invoke the batch Oracle");
    },
    deployment: unsupportedZiaDeployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    onDiagnostic: (message) => diagnostics.push(message),
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: ["zia_rule_labels"],
    stateLoader: async () => {
      resourceCalls += 1;
      throw new Error("unsupported root must not invoke an isolation Oracle");
    },
    tenant: "tenant",
  });
  assert.equal(batchCalls, 0);
  assert.equal(resourceCalls, 0);
  assert.deepEqual([...result.failed].sort(), ["zia_rule_labels", "zia_url_filtering_rules"]);
  assert.deepEqual(result.processed, []);
  assert.deepEqual(await snapshotTree(config), before.config);
  assert.deepEqual(await snapshotTree(imports), before.imports);
  assert.ok(diagnostics.some((message) => {
    return message.includes("selecting zia_rule_labels selects whole root zia_unsupported_preflight");
  }));
  assert.ok(diagnostics.some((message) => message.startsWith(
    "unsupported zia_url_filtering_rules item",
  )));
  assert.equal(
    diagnostics.filter((message) => {
      return message.startsWith("unsupported zia_url_filtering_rules item");
    }).length,
    2,
  );
  const evidenceDiagnostics = diagnostics.filter((message) => {
    return message.startsWith("unsupported zia_url_filtering_rules rule for");
  });
  assert.equal(evidenceDiagnostics.length, 1);
  assert.match(evidenceDiagnostics[0] ?? "", /zscaler\/zia 4\.7\.26/u);
  assert.match(evidenceDiagnostics[0] ?? "", /fresh import Read cannot reconstruct/u);
  assert.match(evidenceDiagnostics[0] ?? "", /resource_zia_url_filtering_rules\.go#L52-L63/u);
  assert.match(evidenceDiagnostics[0] ?? "", /resource_zia_url_filtering_rules\.go#L536-L544/u);
  assert.equal(
    diagnostics.some((message) => message.includes("pending move transition")),
    false,
  );
  assert.ok(diagnostics.includes(
    "adopt counts zia_rule_labels: fetched=1 system_skipped=0 unsupported=0 eligible=1 published=0 failed=1",
  ));
  assert.ok(diagnostics.includes(
    "adopt counts zia_url_filtering_rules: fetched=4 system_skipped=1 unsupported=2 eligible=1 published=0 failed=1",
  ));
});

test("logical-root unsupported preflight precedes external-referent fallback", async (context) => {
  const workspace = await temporaryDirectory(
    context,
    "infrawright-adopt-unsupported-before-fallback-",
  );
  const input = path.join(workspace, "pulls");
  const config = path.join(workspace, "config", "tenant");
  await Promise.all([
    writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "Safe Label" }]),
    writeJson(path.join(input, "zia_url_categories.json"), [{
      configuredName: "External Category",
      customCategory: true,
      id: "CUSTOM_EXTERNAL",
      urls: ["external.example"],
    }]),
    writeJson(path.join(input, "zia_url_filtering_rules.json"), [{
      action: "ISOLATE",
      id: 2,
      name: "Managed Isolate",
      predefined: false,
    }]),
    mkdir(config, { recursive: true }),
  ]);
  const rootMembers = ["zia_rule_labels", "zia_url_filtering_rules"] as const;
  for (const resourceType of rootMembers) {
    await writeFile(
      path.join(config, `${resourceType}.auto.tfvars.json`),
      `existing ${resourceType} config\n`,
    );
  }
  const before = await snapshotTree(config);
  const resourceCalls: string[] = [];
  let batchCalls = 0;
  const root = await committedRoot();
  const result = await runAdoptBatch({
    batchStateLoader: async () => {
      batchCalls += 1;
      throw new Error("unsupported root must not invoke the batch Oracle");
    },
    deployment: unsupportedZiaDeployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: [
      "zia_rule_labels",
      "zia_url_filtering_rules",
      "zia_url_categories",
    ],
    stateLoader: async (request) => {
      resourceCalls.push(request.resourceType);
      assert.equal(request.resourceType, "zia_url_categories");
      return new Map([[
        "external_category",
        {
          address: "zia_url_categories.fixture",
          sensitiveValues: {},
          values: {
            configured_name: "External Category",
            custom_category: true,
            id: "CUSTOM_EXTERNAL",
            urls: ["external.example"],
          },
        },
      ]]);
    },
    tenant: "tenant",
  });
  assert.equal(batchCalls, 0);
  assert.deepEqual(resourceCalls, ["zia_url_categories"]);
  assert.deepEqual([...result.failed].sort(), [...rootMembers].sort());
  assert.deepEqual(result.processed, ["zia_url_categories"]);
  const after = await snapshotTree(config);
  for (const resourceType of rootMembers) {
    const file = `${resourceType}.auto.tfvars.json`;
    assert.equal(after[file], before[file]);
  }
});

test("per-resource unsupported failure leaves its artifacts untouched while another resource publishes", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-unsupported-resource-");
  const input = path.join(workspace, "pulls");
  const config = path.join(workspace, "config", "tenant");
  await Promise.all([
    writeJson(path.join(input, "zia_rule_labels.json"), [{ id: "1", name: "Safe Label" }]),
    writeJson(path.join(input, "zia_url_filtering_rules.json"), [
      { action: "ISOLATE", id: 2, name: "Managed Isolate", predefined: false },
      { action: "BLOCK", id: 3, name: "Eligible Rule", predefined: false },
    ]),
    mkdir(config, { recursive: true }),
  ]);
  const blockedPath = path.join(config, "zia_url_filtering_rules.auto.tfvars.json");
  await writeFile(blockedPath, "existing blocked config\n");
  const diagnostics: string[] = [];
  let oracleCalls = 0;
  const root = await committedRoot();
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "per-resource-type" },
    inputDirectory: input,
    onDiagnostic: (message) => diagnostics.push(message),
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: ["zia_rule_labels", "zia_url_filtering_rules"],
    stateLoader: async (request) => {
      oracleCalls += 1;
      assert.equal(request.resourceType, "zia_rule_labels");
      return new Map([["safe_label", {
        address: "zia_rule_labels.fixture",
        sensitiveValues: {},
        values: { id: "1", name: "Safe Label" },
      }]]);
    },
    tenant: "tenant",
  });
  assert.equal(oracleCalls, 1);
  assert.deepEqual(result.processed, ["zia_rule_labels"]);
  assert.deepEqual(result.failed, ["zia_url_filtering_rules"]);
  assert.equal(await readFile(blockedPath, "utf8"), "existing blocked config\n");
  await access(path.join(config, "zia_rule_labels.auto.tfvars.json"));
  assert.ok(diagnostics.includes(
    "adopt counts zia_rule_labels: fetched=1 system_skipped=0 unsupported=0 eligible=1 published=1 failed=0",
  ));
  assert.ok(diagnostics.includes(
    "adopt counts zia_url_filtering_rules: fetched=2 system_skipped=0 unsupported=1 eligible=1 published=0 failed=1",
  ));
});

test("logical-root Oracle failure isolates the responsible member without publishing", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-batch-isolation-");
  const input = path.join(workspace, "pulls");
  await writeZiaBatchPulls(input);
  const diagnostics: string[] = [];
  const isolated: string[] = [];
  const result = await runAdoptBatch({
    batchStateLoader: async () => {
      throw new Error("batched Terraform plan failed");
    },
    deployment: groupedZiaDeployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    onDiagnostic: (message) => diagnostics.push(message),
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: [...ZIA_BATCH_MEMBERS],
    stateLoader: async (request) => {
      isolated.push(request.resourceType);
      if (request.resourceType === "zia_url_categories") {
        throw new Error("isolated provider Read failed");
      }
      return ziaBatchStateLoader(request);
    },
    tenant: "tenant",
  });
  assert.deepEqual(isolated, [...ZIA_BATCH_MEMBERS]);
  assert.deepEqual([...result.failed].sort(), [...ZIA_BATCH_MEMBERS].sort());
  assert.deepEqual(result.processed, []);
  assert.ok(diagnostics.some((message) => {
    return message === "error: zia_url_categories: isolated provider Read failed";
  }));
  assert.ok(diagnostics.some((message) => {
    return /1 member failure\(s\) identified above/u.test(message);
  }));
  await assert.rejects(access(path.join(workspace, "config", "tenant")), { code: "ENOENT" });
  await assert.rejects(access(path.join(workspace, "imports", "tenant")), { code: "ENOENT" });
});

test("logical-root mode falls back to per-resource Oracles for ungrouped roots", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-batch-fallback-");
  const input = path.join(workspace, "pulls");
  await writeZiaBatchPulls(input);
  let batchCalls = 0;
  let perResourceCalls = 0;
  const result = await runAdoptBatch({
    batchStateLoader: async () => {
      batchCalls += 1;
      throw new Error("ungrouped resources must not invoke the batch Oracle");
    },
    deployment: deployment(workspace),
    environment: { INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root" },
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root: await committedRoot() }),
    root: await committedRoot(),
    selectors: [...ZIA_BATCH_MEMBERS],
    stateLoader: async (request) => {
      perResourceCalls += 1;
      return ziaBatchStateLoader(request);
    },
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  assert.equal(batchCalls, 0);
  assert.equal(perResourceCalls, ZIA_BATCH_MEMBERS.length);
  assert.deepEqual([...result.processed].sort(), [...ZIA_BATCH_MEMBERS].sort());
});

test("all four retained transform/adopt fixtures write byte-identical Python artifacts", async (context) => {
  const root = await committedRoot();
  for (const filename of PARITY_FIXTURES) {
    const fixturePath = path.join(PARITY, filename);
    const fixture = record(JSON.parse(await readFile(fixturePath, "utf8")), filename);
    const resourceType = String(fixture.resource_type);
    const rawItems = fixture.raw_items;
    assert.ok(Array.isArray(rawItems));
    const providerState = record(fixture.provider_state, `${filename}.provider_state`);
    const appliedStateLoader: AdoptionStateLoader = async (request) => {
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
    const expected = pythonExpected(fixturePath);
    const artifacts: Array<{ readonly imports: string; readonly tfvars: string }> = [];
    for (const [stateSource, stateLoader] of [
      ["applied-state", appliedStateLoader],
      ["accepted-plan", acceptedPlanStateLoader({ providerState, resourceType, root })],
    ] as const) {
      const workspace = await temporaryDirectory(
        context,
        `infrawright-adopt-${resourceType}-${stateSource}-`,
      );
      const input = path.join(workspace, "pulls");
      await writeJson(path.join(input, `${resourceType}.json`), rawItems);
      const result = await runAdoptBatch({
        deployment: deployment(workspace),
        inputDirectory: input,
        policy: await loadAdoptionPolicy({ root }),
        root,
        selectors: [resourceType],
        stateLoader,
        tenant: "tenant",
      });
      assert.deepEqual(result.failed, [], `${resourceType} ${stateSource}`);
      assert.deepEqual(result.processed, [resourceType], `${resourceType} ${stateSource}`);
      const actual = {
        imports: await readFile(
          path.join(workspace, "imports", "tenant", `${resourceType}_imports.tf`),
          "utf8",
        ),
        tfvars: await readFile(
          path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`),
          "utf8",
        ),
      };
      assert.deepEqual(actual, expected, `${resourceType} ${stateSource}`);
      artifacts.push(actual);
    }
    assert.deepEqual(artifacts[1], artifacts[0], `${resourceType} state-source artifacts`);
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

test("committed ZIA empty-string defaults are absent from adopted tfvars", async (context) => {
  const root = await committedRoot();
  const fixtures = [{
    resourceType: "zia_firewall_filtering_network_service",
    raw: { id: "42", name: "Example" },
    stateValues: { id: "42", name: "Example", tag: "" },
    fields: ["tag"],
  }, {
    resourceType: "zia_browser_control_policy",
    raw: {},
    stateValues: {
      id: "browser_settings",
      plugin_check_frequency: "",
    },
    fields: ["plugin_check_frequency"],
  }, {
    resourceType: "zia_dlp_dictionaries",
    raw: { id: "43", name: "Dictionary" },
    stateValues: {
      id: "43",
      confidence_level_for_predefined_dict: "",
      confidence_threshold: "",
    },
    fields: ["confidence_level_for_predefined_dict", "confidence_threshold"],
  }, {
    resourceType: "zia_http_header_profile",
    raw: { id: "44", name: "Header" },
    stateValues: {
      id: "44",
      name: "Header",
      http_header_profile_criteria: [{ header: "USERAGENT", operator: "", user_agent: "" }],
    },
    fields: ["operator", "user_agent"],
  }, {
    resourceType: "zia_location_management",
    raw: { id: "45", name: "Location" },
    stateValues: {
      id: "45",
      name: "Location",
      display_time_unit: "",
      sub_loc_scope: "",
      surrogate_refresh_time_unit: "",
    },
    fields: ["display_time_unit", "sub_loc_scope", "surrogate_refresh_time_unit"],
  }, {
    resourceType: "zia_ssl_inspection_rules",
    raw: { id: "46", name: "SSL", order: 1 },
    stateValues: {
      id: "46",
      name: "SSL",
      order: 1,
      action: [{
        type: "DO_NOT_DECRYPT",
        do_not_decrypt_sub_actions: [{ bypass_other_policies: true, min_tls_version: "" }],
      }],
    },
    fields: ["min_tls_version"],
  }] as const;

  for (const fixture of fixtures) {
    const workspace = await temporaryDirectory(
      context,
      `infrawright-adopt-${fixture.resourceType}-`,
    );
    const input = path.join(workspace, "pulls");
    await writeJson(path.join(input, `${fixture.resourceType}.json`), [fixture.raw]);
    const result = await runAdoptBatch({
      deployment: deployment(workspace),
      inputDirectory: input,
      policy: await loadAdoptionPolicy({ root }),
      root,
      selectors: [fixture.resourceType],
      stateLoader: async (request) => new Map(
        [...request.keyToImportId].map(([key]) => [key, {
          address: `${fixture.resourceType}.fixture`,
          sensitiveValues: {},
          values: fixture.stateValues,
        }]),
      ),
      tenant: "tenant",
    });
    assert.deepEqual(result.failed, [], fixture.resourceType);
    const tfvars = JSON.parse(await readFile(
      path.join(
        workspace,
        "config",
        "tenant",
        `${fixture.resourceType}.auto.tfvars.json`,
      ),
      "utf8",
    )) as Record<string, unknown>;
    const items = record(tfvars.items, `${fixture.resourceType}.items`);
    assert.equal(Object.keys(items).length, 1, fixture.resourceType);
    const adoptedItem = record(
      Object.values(items)[0],
      `${fixture.resourceType}.items[0]`,
    );
    const serialized = JSON.stringify(adoptedItem);
    for (const field of fixture.fields) {
      assert.equal(
        serialized.includes(`${JSON.stringify(field)}:`),
        false,
        `${fixture.resourceType}.${field}`,
      );
    }
  }
});

test("forwarding system rules are excluded before the Oracle without hiding managed rules", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-adopt-forwarding-system-");
  const input = path.join(workspace, "pulls");
  const resourceType = "zia_forwarding_control_rule";
  await writeJson(path.join(input, `${resourceType}.json`), [{
    id: "system",
    name: "Fallback Mode of ZPA Forwarding",
    order: -5,
    forwardMethod: "DIRECT",
  }, {
    id: "managed",
    name: "Managed Forwarding Rule",
    order: 1,
    forwardMethod: "DIRECT",
  }]);
  const root = await committedRoot();
  let oracleKeys: readonly string[] = [];
  const result = await runAdoptBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    policy: await loadAdoptionPolicy({ root }),
    root,
    selectors: [resourceType],
    stateLoader: async (request) => {
      oracleKeys = [...request.keyToImportId.keys()];
      return new Map(oracleKeys.map((key) => [key, {
        address: `${resourceType}.fixture`,
        sensitiveValues: {},
        values: {
          forward_method: "DIRECT",
          id: "managed",
          name: "Managed Forwarding Rule",
          order: 1,
        },
      }]));
    },
    tenant: "tenant",
  });

  assert.deepEqual(result.failed, []);
  assert.deepEqual(oracleKeys, ["managed_forwarding_rule"]);
  const tfvars = JSON.parse(await readFile(
    path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`),
    "utf8",
  )) as { readonly items: Readonly<Record<string, unknown>> };
  assert.deepEqual(Object.keys(tfvars.items), ["managed_forwarding_rule"]);
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

test("make adopt wires logical-root batching to one Python-disabled fake Terraform transaction", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-make-adopt-");
  const input = path.join(workspace, "pulls");
  await writeZiaBatchPulls(input);
  const deploymentPath = path.join(workspace, "deployment.json");
  await writeJson(deploymentPath, groupedZiaDeployment(workspace));
  const fakeTerraform = path.join(workspace, "terraform-fake.mjs");
  const fakeLog = path.join(workspace, "terraform.log");
  await writeFile(fakeTerraform, `#!/usr/bin/env node
import fs from "node:fs";
const args = process.argv.slice(2);
fs.appendFileSync(process.env.FAKE_TERRAFORM_LOG, args[0] + "\\n");
const imports = fs.existsSync("imports.tf") ? fs.readFileSync("imports.tf", "utf8") : "";
const instances = [...imports.matchAll(/to = ([^\\s]+)\\s+id = "([^"]+)"/gu)].map((match) => ({
  address: match[1],
  id: match[2],
  name: match[1].split(".")[1],
  type: match[1].split(".")[0],
}));
const values = (item) => item.type === "zia_rule_labels"
  ? { id: item.id, name: "Batch Label" }
  : { configured_name: "Batch Category", custom_category: true, id: item.id, urls: ["batch.example"] };
if (args[0] === "plan") {
  const generated = args.find((item) => item.startsWith("-generate-config-out="));
  if (generated) {
    const config = instances.map((item) => item.type === "zia_rule_labels"
      ? 'resource "zia_rule_labels" "' + item.name + '" {\\n  name = "Batch Label"\\n}\\n'
      : 'resource "zia_url_categories" "' + item.name + '" {\\n  configured_name = "Batch Category"\\n  custom_category = true\\n  urls = ["batch.example"]\\n}\\n').join("\\n");
    fs.writeFileSync(generated.slice(generated.indexOf("=") + 1), config);
  }
  const out = args.find((item) => item.startsWith("-out="));
  if (out) fs.writeFileSync(out.slice(5), "fake");
  process.exit(0);
}
if (args[0] === "show" && args.at(-1)?.endsWith(".tfplan")) {
  process.stdout.write(JSON.stringify({applyable:true,complete:true,errored:false,format_version:"1.2",terraform_version:"1.15.4",resource_changes:instances.map((item) => ({address:item.address,change:{actions:["no-op"],importing:{id:item.id}},mode:"managed",provider_name:"registry.terraform.io/zscaler/zia",type:item.type}))}));
  process.exit(0);
}
if (args[0] === "show") {
  process.stdout.write(JSON.stringify({format_version:"1.0",terraform_version:"1.15.4",values:{root_module:{resources:instances.map((item) => ({address:item.address,mode:"managed",provider_name:"registry.terraform.io/zscaler/zia",type:item.type,values:values(item),sensitive_values:{}}))}}}));
  process.exit(0);
}
process.exit(0);
  `);
  await chmod(fakeTerraform, 0o700);
  const packsRoot = await reducedPackRoot(workspace, "make-zia", ["zia"], ["zscaler"]);
  const built = spawnSync(process.execPath, ["scripts/build-metadata-cli.mjs"], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(built.status, 0, `${built.stdout}\n${built.stderr}`);
  const result = spawnSync("make", [
    "adopt",
    `IN=${input}`,
    "TENANT=tenant",
    `RESOURCE=${ZIA_BATCH_MEMBERS.join(" ")}`,
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
      FAKE_TERRAFORM_LOG: fakeLog,
      INFRAWRIGHT_DEPLOYMENT: deploymentPath,
      INFRAWRIGHT_ORACLE_BATCH_MODE: "logical-root",
      INFRAWRIGHT_PACKS: packsRoot,
    },
  });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  assert.equal(`${result.stdout}${result.stderr}`.includes("python-must-not-run"), false);
  assert.deepEqual((await readFile(fakeLog, "utf8")).trim().split("\n"), [
    "init",
    "plan",
    "show",
    "apply",
    "show",
  ]);
  for (const resourceType of ZIA_BATCH_MEMBERS) {
    await access(path.join(workspace, "config", "tenant", `${resourceType}.auto.tfvars.json`));
    await access(path.join(workspace, "imports", "tenant", `${resourceType}_imports.tf`));
  }
});
