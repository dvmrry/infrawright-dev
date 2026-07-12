import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  ZCC_ADOPTION_CATALOG_SHA256,
  type ZccAdoptionArtifactSet,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import {
  ZCC_ADOPTION_TERRAFORM_VERSION,
  zccAdoptionProviderLock,
} from "../node-src/domain/zcc-adoption-provider-lock.js";
import {
  runZccAdoptionOracle,
  type ZccAdoptionOracleAdapters,
  type ZccAdoptionOracleCommandRequest,
  type ZccAdoptionOracleCommandStage,
  type ZccAdoptionOracleRequest,
  type ZccAdoptionOracleShowRequest,
  type ZccAdoptionOracleShowStage,
  type ZccAdoptionOracleWriteRequest,
} from "../node-src/domain/zcc-adoption-oracle.js";
import {
  deriveZccAdoptionIdentities,
} from "../node-src/domain/zcc-adoption-projection.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import type {
  ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";

const DIRECTORY = "/private/infrawright-zcc-oracle-test";
const PROVIDER_NAME = "registry.terraform.io/zscaler/zcc";
const TERRAFORM = "/trusted/bin/terraform";
const SOURCE_DIGEST = "7".repeat(64);
const SECRET = "ORACLE-SECRET-MUST-NOT-LEAK-91b7";
const STRUCTURAL_FIXTURE_PATH = path.join(
  process.cwd(),
  "node-tests/fixtures/terraform-import-structure-v1.15.4.json",
);

interface ResourceCase {
  readonly resourceType: ZccPullResourceType;
  readonly rawItems: readonly unknown[];
  readonly values: Readonly<Record<string, unknown>>;
  readonly sensitiveValues?: unknown;
}

interface ExpectedImport {
  readonly address: string;
  readonly importId: string;
  readonly key: string;
}

interface TerraformStructuralFixture {
  readonly fixture_version: 1;
  readonly provenance: {
    readonly status: "terraform_core_structural_only";
    readonly scope: string;
    readonly terraform_version: "1.15.4";
    readonly provider: "terraform.io/builtin/terraform";
    readonly resource_type: "terraform_data";
    readonly external_provider_downloads: false;
    readonly credentials_used: false;
  };
  readonly plan: Record<string, unknown>;
  readonly state: Record<string, unknown>;
}

const CASES: readonly ResourceCase[] = [
  {
    resourceType: "zcc_device_cleanup",
    rawItems: [{ id: "900719925474099312345678901" }],
    values: {
      active: true,
      auto_purge_days: new LosslessNumber("900719925474099312345678901"),
      id: "900719925474099312345678901",
    },
    sensitiveValues: { active: false, auto_purge_days: false, id: false },
  },
  {
    resourceType: "zcc_failopen_policy",
    rawItems: [{ id: "failopen-1" }],
    values: { active: true, enable_fail_open: false, id: "failopen-1" },
  },
  {
    resourceType: "zcc_forwarding_profile",
    rawItems: [{
      id: "9007199254740993",
      name: "Forwarding Profile",
    }],
    values: {
      active: true,
      id: "9007199254740993",
      name: "Forwarding Profile",
      trusted_network_ids: [new LosslessNumber("900719925474099312345678901")],
    },
  },
  {
    resourceType: "zcc_trusted_network",
    rawItems: [{
      id: "9007199254740995",
      networkName: "Trusted Network",
    }],
    values: {
      active: true,
      condition_type: "DNS",
      id: "9007199254740995",
      name: "Trusted Network",
    },
  },
  {
    resourceType: "zcc_web_privacy",
    rawItems: [{ id: "privacy-1" }],
    values: { active: true, collect_user_info: false, id: "privacy-1" },
  },
] as const;

function scratchAddress(resourceType: string, key: string): string {
  const digest = createHash("sha1")
    .update(key, "utf8")
    .digest("hex")
    .slice(0, 16);
  return `${resourceType}.iw_${digest}`;
}

function request(resourceCase: ResourceCase): ZccAdoptionOracleRequest {
  const resourceType = resourceCase.resourceType;
  return {
    catalog: loadZccAdoptionCatalog(),
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    rawItems: resourceCase.rawItems,
    source: {
      path: `pulls/demo/${resourceType}.json`,
      sha256: SOURCE_DIGEST,
      size_bytes: 123,
    },
    target: {
      tenant: "demo",
      resourceType,
      rootLabel: resourceType,
      rootMembers: [resourceType],
      variableName: "items",
      configPath: `config/demo/${resourceType}.auto.tfvars.json`,
      importsPath: `imports/demo/${resourceType}_imports.tf`,
      lookupPath: resourceType === "zcc_trusted_network"
        ? `config/demo/${resourceType}.lookup.json`
        : null,
    },
    terraformExecutable: TERRAFORM,
  };
}

function expectedImports(resourceCase: ResourceCase): readonly ExpectedImport[] {
  const identities = deriveZccAdoptionIdentities({
    catalog: loadZccAdoptionCatalog(),
    rawItems: resourceCase.rawItems,
    resourceType: resourceCase.resourceType,
  });
  return Object.keys(identities.import_ids).sort().map((key) => {
    const importId = identities.import_ids[key];
    assert.notEqual(importId, undefined);
    return {
      address: scratchAddress(resourceCase.resourceType, key),
      importId: importId ?? "",
      key,
    };
  });
}

function planFor(
  resourceCase: ResourceCase,
  imports = expectedImports(resourceCase),
): Record<string, unknown> {
  return {
    format_version: "1.2",
    terraform_version: ZCC_ADOPTION_TERRAFORM_VERSION,
    complete: true,
    errored: false,
    applyable: true,
    resource_changes: imports.map((entry) => ({
      address: entry.address,
      mode: "managed",
      type: resourceCase.resourceType,
      provider_name: PROVIDER_NAME,
      change: {
        actions: ["no-op"],
        importing: { id: entry.importId },
      },
    })),
  };
}

function stateFor(
  resourceCase: ResourceCase,
  imports = expectedImports(resourceCase),
): Record<string, unknown> {
  return {
    format_version: "1.0",
    terraform_version: ZCC_ADOPTION_TERRAFORM_VERSION,
    values: {
      root_module: {
        resources: imports.map((entry) => ({
          address: entry.address,
          mode: "managed",
          type: resourceCase.resourceType,
          provider_name: PROVIDER_NAME,
          values: resourceCase.values,
          sensitive_values: resourceCase.sensitiveValues ?? {},
        })),
      },
    },
  };
}

async function loadTerraformStructuralFixture(): Promise<TerraformStructuralFixture> {
  return JSON.parse(
    await readFile(STRUCTURAL_FIXTURE_PATH, "utf8"),
  ) as TerraformStructuralFixture;
}

class FakeOracle {
  readonly events: string[] = [];
  readonly writes: ZccAdoptionOracleWriteRequest[] = [];
  readonly commands: ZccAdoptionOracleCommandRequest[] = [];
  readonly shows: ZccAdoptionOracleShowRequest[] = [];

  plan: unknown;
  state: unknown;
  failCommand: ZccAdoptionOracleCommandStage | null = null;
  failShow: ZccAdoptionOracleShowStage | null = null;
  failCreate = false;
  failCleanup = false;
  failWrite = false;
  directory = DIRECTORY;
  onCommand: ((request: ZccAdoptionOracleCommandRequest) => void) | null = null;
  onCleanup: (() => void) | null = null;

  constructor(resourceCase: ResourceCase) {
    this.plan = planFor(resourceCase);
    this.state = stateFor(resourceCase);
  }

  adapters(): ZccAdoptionOracleAdapters {
    return {
      temporary: {
        create: async (prefix) => {
          this.events.push(`temp:create:${prefix}`);
          if (this.failCreate) {
            throw new Error(SECRET);
          }
          return this.directory;
        },
        remove: async (directory) => {
          this.events.push(`temp:remove:${directory}`);
          this.onCleanup?.();
          if (this.failCleanup) {
            throw new Error(SECRET);
          }
        },
      },
      files: {
        writeText: async (write) => {
          this.events.push(`write:${write.path}`);
          this.writes.push(write);
          if (this.failWrite) {
            throw new Error(SECRET);
          }
        },
      },
      command: {
        run: async (command) => {
          this.events.push(`command:${command.stage}`);
          this.commands.push(command);
          this.onCommand?.(command);
          if (this.failCommand === command.stage) {
            throw new Error(SECRET);
          }
          // Command stdout is deliberately outside the core contract. A fake
          // returning a forged value proves it cannot substitute for show JSON.
          return { stdout: `forged-${SECRET}` } as never;
        },
      },
      show: {
        readJson: async (show) => {
          this.events.push(`show:${show.stage}`);
          this.shows.push(show);
          if (this.failShow === show.stage) {
            throw new Error(SECRET);
          }
          return show.stage === "show-plan" ? this.plan : this.state;
        },
      },
    };
  }
}

async function expectFailure(
  run: () => Promise<unknown>,
  code: string,
): Promise<ProcessFailure> {
  let thrown: unknown;
  try {
    await run();
  } catch (error: unknown) {
    thrown = error;
  }
  assert.ok(thrown instanceof ProcessFailure);
  assert.equal(thrown.code, code);
  assert.equal(JSON.stringify(thrown).includes(SECRET), false);
  assert.equal(thrown.message.includes(SECRET), false);
  return thrown;
}

test("all five resources run the exact private transaction and compile artifacts", async () => {
  for (const resourceCase of CASES) {
    const fake = new FakeOracle(resourceCase);
    const result = await runZccAdoptionOracle(
      request(resourceCase),
      fake.adapters(),
    );
    assert.equal(result.resource_type, resourceCase.resourceType);
    assert.equal(result.mode, "bootstrap");
    assert.equal(result.catalog.sha256, ZCC_ADOPTION_CATALOG_SHA256);
    assert.deepEqual(
      fake.events.map((entry) => entry.replace(DIRECTORY, "<temp>")),
      [
        "temp:create:infrawright-zcc-oracle-",
        "write:<temp>/main.tf",
        "write:<temp>/imports.tf",
        "write:<temp>/.terraform.lock.hcl",
        "command:init",
        "command:plan",
        "show:show-plan",
        "command:apply",
        "show:show-state",
        "temp:remove:<temp>",
      ],
      resourceCase.resourceType,
    );
    assert.equal(fake.writes.every((entry) => entry.mode === 0o600), true);
  }
});

test("Terraform omitempty shapes and explicit empty controls both succeed", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const omittedPlan = planFor(resourceCase);
  for (const field of [
    "checks",
    "deferred_changes",
    "action_invocations",
    "deferred_action_invocations",
    "resource_drift",
    "output_changes",
  ]) {
    assert.equal(Object.hasOwn(omittedPlan, field), false);
  }
  const omittedState = stateFor(resourceCase);
  assert.equal(Object.hasOwn(omittedState, "checks"), false);
  const omittedValues = omittedState.values as Record<string, unknown>;
  const omittedRoot = omittedValues.root_module as Record<string, unknown>;
  assert.equal(Object.hasOwn(omittedValues, "outputs"), false);
  assert.equal(Object.hasOwn(omittedRoot, "child_modules"), false);
  await runZccAdoptionOracle(
    request(resourceCase),
    new FakeOracle(resourceCase).adapters(),
  );

  const explicit = new FakeOracle(resourceCase);
  explicit.plan = {
    ...planFor(resourceCase),
    checks: [],
    deferred_changes: [],
    action_invocations: [],
    deferred_action_invocations: [],
    resource_drift: [],
    output_changes: {},
  };
  const explicitState = structuredClone(stateFor(resourceCase));
  explicitState.checks = [];
  const values = explicitState.values as Record<string, unknown>;
  values.outputs = {};
  const root = values.root_module as Record<string, unknown>;
  root.child_modules = [];
  const stateResource = (root.resources as Record<string, unknown>[])[0];
  assert.notEqual(stateResource, undefined);
  stateResource!.tainted = false;
  explicit.state = explicitState;
  await runZccAdoptionOracle(request(resourceCase), explicit.adapters());
});

test("Terraform 1.15.4 structural import evidence traverses the oracle gates", async () => {
  const fixture = await loadTerraformStructuralFixture();
  assert.equal(fixture.fixture_version, 1);
  assert.equal(fixture.provenance.status, "terraform_core_structural_only");
  assert.equal(fixture.provenance.terraform_version, "1.15.4");
  assert.equal(fixture.provenance.provider, "terraform.io/builtin/terraform");
  assert.equal(fixture.provenance.resource_type, "terraform_data");
  assert.equal(fixture.provenance.external_provider_downloads, false);
  assert.equal(fixture.provenance.credentials_used, false);
  assert.match(fixture.provenance.scope, /not ZCC provider/);

  assert.equal(fixture.plan.format_version, "1.2");
  assert.equal(fixture.plan.terraform_version, "1.15.4");
  assert.equal(fixture.plan.applyable, true);
  assert.equal(fixture.plan.complete, true);
  assert.equal(fixture.plan.errored, false);
  for (const omitted of [
    "checks",
    "deferred_changes",
    "action_invocations",
    "deferred_action_invocations",
    "resource_drift",
    "output_changes",
  ]) {
    assert.equal(Object.hasOwn(fixture.plan, omitted), false, omitted);
  }

  const capturedChanges = fixture.plan.resource_changes;
  assert.ok(Array.isArray(capturedChanges));
  assert.equal(capturedChanges.length, 1);
  const capturedChange = capturedChanges[0];
  assert.ok(capturedChange !== null && typeof capturedChange === "object");
  const capturedChangeRecord = capturedChange as Record<string, unknown>;
  assert.equal(capturedChangeRecord.address, "terraform_data.fixture");
  assert.equal(capturedChangeRecord.mode, "managed");
  assert.equal(capturedChangeRecord.type, "terraform_data");
  assert.equal(
    capturedChangeRecord.provider_name,
    "terraform.io/builtin/terraform",
  );
  const capturedDelta = capturedChangeRecord.change;
  assert.ok(capturedDelta !== null && typeof capturedDelta === "object");
  const capturedDeltaRecord = capturedDelta as Record<string, unknown>;
  assert.deepEqual(capturedDeltaRecord.actions, ["no-op"]);
  assert.deepEqual(capturedDeltaRecord.importing, {
    id: "structural-fixture-id",
  });

  assert.equal(fixture.state.format_version, "1.0");
  assert.equal(fixture.state.terraform_version, "1.15.4");
  assert.equal(Object.hasOwn(fixture.state, "checks"), false);
  const capturedStateValues = fixture.state.values as Record<string, unknown>;
  const capturedStateRoot = capturedStateValues.root_module as Record<string, unknown>;
  const capturedStateResources = capturedStateRoot.resources;
  assert.ok(Array.isArray(capturedStateResources));
  assert.equal(capturedStateResources.length, 1);
  const capturedStateResource = capturedStateResources[0];
  assert.ok(capturedStateResource !== null && typeof capturedStateResource === "object");
  assert.equal(
    Object.hasOwn(capturedStateResource, "sensitive_values"),
    true,
  );

  // The fixture retains only Terraform-core structural evidence. Substitute
  // the resource/provider-specific fields before driving the ZCC boundary;
  // this does not turn the fixture into ZCC provider or tenant evidence.
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const imports = expectedImports(resourceCase);
  const imported = imports[0];
  assert.notEqual(imported, undefined);
  if (imported === undefined) {
    return;
  }

  const plan = structuredClone(fixture.plan);
  plan.resource_changes = [{
    ...capturedChangeRecord,
    address: imported.address,
    type: resourceCase.resourceType,
    name: imported.address.slice(imported.address.indexOf(".") + 1),
    provider_name: PROVIDER_NAME,
    change: {
      ...capturedDeltaRecord,
      importing: { id: imported.importId },
    },
  }];

  const state = structuredClone(fixture.state);
  const stateValues = state.values as Record<string, unknown>;
  const stateRoot = stateValues.root_module as Record<string, unknown>;
  stateRoot.resources = [{
    ...(capturedStateResource as Record<string, unknown>),
    address: imported.address,
    type: resourceCase.resourceType,
    name: imported.address.slice(imported.address.indexOf(".") + 1),
    provider_name: PROVIDER_NAME,
    values: resourceCase.values,
    sensitive_values: resourceCase.sensitiveValues ?? {},
  }];

  const fake = new FakeOracle(resourceCase);
  fake.plan = plan;
  fake.state = state;
  const result = await runZccAdoptionOracle(request(resourceCase), fake.adapters());
  assert.equal(result.resource_type, resourceCase.resourceType);
  assert.match(result.artifacts.tfvars.content, /collect_user_info/);
});

test("root pins reviewed Terraform while imports remain Python-byte-identical", async () => {
  const importId = 'quote"\\line\nrow\rcol\t${name}%{ if true }';
  const resourceCase: ResourceCase = {
    resourceType: "zcc_forwarding_profile",
    rawItems: [{
      id: importId,
      name: "Escaping",
    }],
    values: { id: importId, name: "Escaping" },
  };
  const fake = new FakeOracle(resourceCase);
  await runZccAdoptionOracle(request(resourceCase), fake.adapters());

  assert.equal(
    fake.writes[0]?.content,
    "terraform {\n"
      + '  required_version = "= 1.15.4"\n'
      + "  required_providers {\n"
      + "    zcc = {\n"
      + '      source = "zscaler/zcc"\n'
      + '      version = "0.1.0-beta.1"\n'
      + "    }\n"
      + "  }\n"
      + "}\n\n"
      + 'provider "zcc" {\n'
      + "  # credentials via provider environment variables\n"
      + "}\n",
  );
  const imported = expectedImports(resourceCase)[0];
  assert.notEqual(imported, undefined);
  // The root baseline intentionally differs from Python; the scratch-address
  // and string escaping below remain byte-identical to render_import_blocks.
  assert.equal(
    fake.writes[1]?.content,
    "import {\n"
      + `  to = ${imported?.address}\n`
      + '  id = "quote\\"\\\\line\\nrow\\rcol\\t$${name}%%{ if true }"\n'
      + "}\n",
  );
  assert.equal(fake.writes[2]?.path, `${DIRECTORY}/.terraform.lock.hcl`);
  assert.equal(fake.writes[2]?.content, zccAdoptionProviderLock());

  assert.deepEqual(fake.commands.map((entry) => entry.argv), [
    [
      "init",
      "-backend=false",
      "-input=false",
      "-no-color",
      "-lockfile=readonly",
    ],
    [
      "plan",
      "-input=false",
      "-no-color",
      "-lock=false",
      `-generate-config-out=${DIRECTORY}/generated.tf`,
      `-out=${DIRECTORY}/oracle.tfplan`,
    ],
    [
      "apply",
      "-input=false",
      "-no-color",
      "-lock=false",
      `${DIRECTORY}/oracle.tfplan`,
    ],
  ]);
  assert.deepEqual(fake.shows.map((entry) => entry.argv), [
    ["show", "-json", `${DIRECTORY}/oracle.tfplan`],
    ["show", "-json", `${DIRECTORY}/terraform.tfstate`],
  ]);
  assert.deepEqual(fake.commands[0]?.sensitiveTokens, []);
  assert.deepEqual(
    fake.commands[1]?.sensitiveTokens,
    [imported?.importId],
  );
  assert.deepEqual(fake.commands[1]?.protectedPaths, [
    `${DIRECTORY}/main.tf`,
    `${DIRECTORY}/imports.tf`,
    `${DIRECTORY}/.terraform.lock.hcl`,
  ]);
  assert.deepEqual(fake.shows[0]?.protectedPaths, [
    `${DIRECTORY}/main.tf`,
    `${DIRECTORY}/imports.tf`,
    `${DIRECTORY}/.terraform.lock.hcl`,
    `${DIRECTORY}/generated.tf`,
    `${DIRECTORY}/oracle.tfplan`,
  ]);
  assert.deepEqual(fake.commands[2]?.protectedPaths, fake.shows[0]?.protectedPaths);
});

test("lossless provider numbers survive state projection exactly", async () => {
  const resourceCase = CASES[0];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const result = await runZccAdoptionOracle(
    request(resourceCase),
    new FakeOracle(resourceCase).adapters(),
  );
  assert.match(
    result.artifacts.tfvars.content,
    /900719925474099312345678901/,
  );
});

test("an empty derived identity set performs no Terraform or temporary effects", async () => {
  for (const template of CASES) {
    const resourceCase = { ...template, rawItems: [] };
    const fake = new FakeOracle(resourceCase);
    const result = await runZccAdoptionOracle(request(resourceCase), fake.adapters());
    assert.deepEqual(fake.events, []);
    assert.equal(result.artifacts.tfvars.content, '{\n  "items": {}\n}\n');
    assert.equal(result.artifacts.imports.content, "");
  }
});

test("every non-exact import plan shape is rejected before apply", async (t) => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const base = planFor(resourceCase);
  const firstChange = (base.resource_changes as Record<string, unknown>[])[0];
  assert.notEqual(firstChange, undefined);
  const variants: readonly [string, (plan: Record<string, unknown>) => void][] = [
    ["future format", (plan) => { plan.format_version = "1.3"; }],
    ["old format", (plan) => { plan.format_version = "1.1"; }],
    ["wrong Terraform", (plan) => { plan.terraform_version = "1.15.5"; }],
    ["missing Terraform", (plan) => { delete plan.terraform_version; }],
    ["incomplete", (plan) => { plan.complete = false; }],
    ["errored", (plan) => { plan.errored = true; }],
    ["not applyable", (plan) => { plan.applyable = false; }],
    ["drift", (plan) => { plan.resource_drift = [{ address: SECRET }]; }],
    ["outputs", (plan) => { plan.output_changes = { secret: SECRET }; }],
    ["diagnostics", (plan) => { plan.diagnostics = [{ detail: SECRET }]; }],
    ["checks", (plan) => { plan.checks = [{ status: "fail", detail: SECRET }]; }],
    ["deferred changes", (plan) => {
      plan.deferred_changes = [{ reason: SECRET }];
    }],
    ["action invocations", (plan) => {
      plan.action_invocations = [{ address: SECRET }];
    }],
    ["deferred action invocations", (plan) => {
      plan.deferred_action_invocations = [{ address: SECRET }];
    }],
    ["checks wrong type", (plan) => { plan.checks = {}; }],
    ["deferred changes wrong type", (plan) => {
      plan.deferred_changes = null;
    }],
    ["action invocations wrong type", (plan) => {
      plan.action_invocations = {};
    }],
    ["deferred action invocations wrong type", (plan) => {
      plan.deferred_action_invocations = {};
    }],
    ["resource drift wrong type", (plan) => { plan.resource_drift = {}; }],
    ["output changes wrong type", (plan) => { plan.output_changes = []; }],
    ["missing", (plan) => { plan.resource_changes = []; }],
    ["extra", (plan) => {
      const changes = plan.resource_changes as unknown[];
      plan.resource_changes = [...changes, { ...firstChange, address: SECRET }];
    }],
    ["wrong action", (plan) => {
      const change = (plan.resource_changes as Record<string, unknown>[])[0];
      assert.notEqual(change, undefined);
      change!.change = { actions: ["update"], importing: { id: SECRET } };
    }],
    ["wrong provider", (plan) => {
      const change = (plan.resource_changes as Record<string, unknown>[])[0];
      assert.notEqual(change, undefined);
      change!.provider_name = SECRET;
    }],
    ["wrong import id", (plan) => {
      const change = (plan.resource_changes as Record<string, unknown>[])[0];
      assert.notEqual(change, undefined);
      change!.change = { actions: ["no-op"], importing: { id: SECRET } };
    }],
  ];
  for (const [name, mutate] of variants) {
    await t.test(name, async () => {
      const fake = new FakeOracle(resourceCase);
      const candidate = structuredClone(base);
      mutate(candidate);
      fake.plan = candidate;
      await expectFailure(
        () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
        "ZCC_ADOPTION_ORACLE_PLAN_REJECTED",
      );
      assert.equal(
        fake.commands.some((entry) => entry.stage === "apply"),
        false,
      );
      assert.equal(fake.events.at(-1), `temp:remove:${DIRECTORY}`);
    });
  }
});

test("state extraction accepts only the exact root managed resource join", async (t) => {
  const resourceCase = CASES[2];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const variants: readonly [string, (state: Record<string, unknown>) => void][] = [
    ["future format", (state) => { state.format_version = "1.1"; }],
    ["wrong Terraform", (state) => { state.terraform_version = "1.15.5"; }],
    ["missing Terraform", (state) => { delete state.terraform_version; }],
    ["missing root resource", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      root.resources = [];
    }],
    ["extra root resource", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resources = root.resources as unknown[];
      root.resources = [...resources, { ...(resources[0] as object), address: SECRET }];
    }],
    ["child module", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      root.child_modules = [{ address: SECRET, resources: [] }];
    }],
    ["wrong provider", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.provider_name = SECRET;
    }],
    ["wrong resource type", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.type = SECRET;
    }],
    ["missing returned provider identity", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      const providerValues = resource!.values as Record<string, unknown>;
      delete providerValues.id;
    }],
    ["wrong returned provider identity", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      const providerValues = resource!.values as Record<string, unknown>;
      providerValues.id = SECRET;
    }],
    ["data mode", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.mode = "data";
    }],
    ["state checks", (state) => {
      state.checks = [{ status: "fail", detail: SECRET }];
    }],
    ["state checks wrong type", (state) => { state.checks = {}; }],
    ["deposed instance", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.deposed_key = null;
    }],
    ["tainted instance", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.tainted = true;
    }],
    ["invalid tainted marker", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.tainted = null;
    }],
    ["missing sensitivity evidence", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      delete resource!.sensitive_values;
    }],
    ["invalid sensitivity evidence", (state) => {
      const values = state.values as Record<string, unknown>;
      const root = values.root_module as Record<string, unknown>;
      const resource = (root.resources as Record<string, unknown>[])[0];
      assert.notEqual(resource, undefined);
      resource!.sensitive_values = false;
    }],
  ];
  for (const [name, mutate] of variants) {
    await t.test(name, async () => {
      const fake = new FakeOracle(resourceCase);
      const candidate = structuredClone(stateFor(resourceCase));
      mutate(candidate);
      fake.state = candidate;
      await expectFailure(
        () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
        "ZCC_ADOPTION_ORACLE_STATE_REJECTED",
      );
      assert.equal(
        fake.commands.some((entry) => entry.stage === "apply"),
        true,
      );
      assert.equal(fake.events.at(-1), `temp:remove:${DIRECTORY}`);
    });
  }
});

test("object and top-level true sensitive masks fail only at projection", async (t) => {
  for (const [name, sensitiveValues] of [
    ["object mask", { collect_user_info: true, id: false }],
    ["top-level true mask", true],
  ] as const) {
    await t.test(name, async () => {
      const resourceCase: ResourceCase = {
        resourceType: "zcc_web_privacy",
        rawItems: [{ id: SECRET }],
        values: { collect_user_info: true, id: SECRET },
        sensitiveValues,
      };
      const fake = new FakeOracle(resourceCase);
      await expectFailure(
        () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
        "ZCC_ADOPTION_ORACLE_ARTIFACT_FAILED",
      );
      assert.equal(fake.events.at(-1), `temp:remove:${DIRECTORY}`);
    });
  }
});

test("input and show graphs are frozen across injected mutation hooks", async () => {
  const mutableRaw = {
    id: "9007199254740997",
    name: "Original Profile",
  };
  const resourceCase: ResourceCase = {
    resourceType: "zcc_forwarding_profile",
    rawItems: [mutableRaw],
    values: {
      id: "9007199254740997",
      name: "Original Profile",
    },
  };
  const mutableRequest = request(resourceCase) as {
    catalog: ZccAdoptionOracleRequest["catalog"];
    catalogSha256: string;
    rawItems: unknown[];
    source: { path: string; sha256: string; size_bytes: number };
    target: ZccAdoptionOracleRequest["target"];
    terraformExecutable: string;
  };
  const fake = new FakeOracle(resourceCase);
  const mutablePlan = fake.plan as Record<string, unknown>;
  const mutableState = fake.state as Record<string, unknown>;
  fake.onCommand = (command) => {
    if (command.stage === "init") {
      mutableRaw.name = "Mutated Profile";
      mutableRequest.source.path = SECRET;
    }
    if (command.stage === "apply") {
      mutablePlan.complete = false;
    }
  };
  fake.onCleanup = () => {
    const values = mutableState.values as Record<string, unknown>;
    const root = values.root_module as Record<string, unknown>;
    const resource = (root.resources as Record<string, unknown>[])[0];
    const providerValues = resource?.values as Record<string, unknown>;
    providerValues.name = "Mutated After Projection";
  };

  const result = await runZccAdoptionOracle(mutableRequest, fake.adapters());
  assert.match(result.artifacts.tfvars.content, /Original Profile/);
  assert.doesNotMatch(result.artifacts.tfvars.content, /Mutated/);
  assert.equal(result.source.path, "pulls/demo/zcc_forwarding_profile.json");
});

test("effect failures are stage-coded and value-free", async (t) => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  for (const [name, configure, code] of [
    [
      "temporary create",
      (fake: FakeOracle) => { fake.failCreate = true; },
      "ZCC_ADOPTION_ORACLE_TEMP_FAILED",
    ],
    [
      "write",
      (fake: FakeOracle) => { fake.failWrite = true; },
      "ZCC_ADOPTION_ORACLE_WRITE_FAILED",
    ],
    [
      "init",
      (fake: FakeOracle) => { fake.failCommand = "init"; },
      "ZCC_ADOPTION_ORACLE_INIT_FAILED",
    ],
    [
      "plan",
      (fake: FakeOracle) => { fake.failCommand = "plan"; },
      "ZCC_ADOPTION_ORACLE_PLAN_FAILED",
    ],
    [
      "show plan",
      (fake: FakeOracle) => { fake.failShow = "show-plan"; },
      "ZCC_ADOPTION_ORACLE_PLAN_SHOW_FAILED",
    ],
    [
      "apply",
      (fake: FakeOracle) => { fake.failCommand = "apply"; },
      "ZCC_ADOPTION_ORACLE_APPLY_FAILED",
    ],
    [
      "show state",
      (fake: FakeOracle) => { fake.failShow = "show-state"; },
      "ZCC_ADOPTION_ORACLE_STATE_SHOW_FAILED",
    ],
  ] as const) {
    await t.test(name, async () => {
      const fake = new FakeOracle(resourceCase);
      configure(fake);
      await expectFailure(
        () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
        code,
      );
      if (name === "temporary create") {
        assert.equal(fake.events.some((entry) => entry.startsWith("temp:remove")), false);
      } else {
        assert.equal(fake.events.at(-1), `temp:remove:${DIRECTORY}`);
      }
    });
  }
});

test("the host transaction timeout survives stage mapping and still cleans up", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const fake = new FakeOracle(resourceCase);
  fake.onCommand = (command) => {
    if (command.stage === "plan") {
      throw new ProcessFailure({
        code: "ZCC_ADOPTION_ORACLE_TIMEOUT",
        category: "io",
        message: "ZCC adoption oracle transaction exceeded its execution deadline",
      });
    }
  };
  await expectFailure(
    () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
    "ZCC_ADOPTION_ORACLE_TIMEOUT",
  );
  assert.equal(fake.events.at(-1), `temp:remove:${DIRECTORY}`);
});

test("cleanup failure cannot mask a primary failure", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const fake = new FakeOracle(resourceCase);
  fake.plan = { ...planFor(resourceCase), complete: false };
  fake.failCleanup = true;
  const thrown = await expectFailure(
    () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
    "ZCC_ADOPTION_ORACLE_PLAN_REJECTED",
  );
  assert.deepEqual(thrown.details, [{
    path: "cleanup",
    code: "ZCC_ADOPTION_ORACLE_CLEANUP_FAILED",
    message: "private oracle cleanup also failed",
  }]);
});

test("an invalid temporary path is still returned to its creating authority", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const fake = new FakeOracle(resourceCase);
  fake.directory = "relative-private-directory";
  await expectFailure(
    () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
    "ZCC_ADOPTION_ORACLE_TEMP_FAILED",
  );
  assert.equal(
    fake.events.at(-1),
    "temp:remove:relative-private-directory",
  );
});

test("cleanup failure after success becomes the terminal stage failure", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const fake = new FakeOracle(resourceCase);
  fake.failCleanup = true;
  await expectFailure(
    () => runZccAdoptionOracle(request(resourceCase), fake.adapters()),
    "ZCC_ADOPTION_ORACLE_CLEANUP_FAILED",
  );
});

test("invalid request graphs fail before effects and without caller values", async () => {
  const resourceCase = CASES[4];
  assert.notEqual(resourceCase, undefined);
  if (resourceCase === undefined) {
    return;
  }
  const cyclic: Record<string, unknown> = { id: SECRET };
  cyclic.self = cyclic;
  const unsafe = { ...request(resourceCase), rawItems: [cyclic] };
  const fake = new FakeOracle(resourceCase);
  await expectFailure(
    () => runZccAdoptionOracle(unsafe, fake.adapters()),
    "ZCC_ADOPTION_ORACLE_INPUT_FAILED",
  );
  assert.deepEqual(fake.events, []);
});

function assertArtifactSet(value: ZccAdoptionArtifactSet): void {
  assert.equal(value.kind, "infrawright.zcc_adoption_artifact_set");
}

// Compile-time guard: the transaction returns only the existing safe artifact
// contract, never raw plan/state/provider output.
void assertArtifactSet;
