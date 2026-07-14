import assert from "node:assert/strict";
import { access, chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  assertImportOnlyPlan,
  createOracleCommandRunner,
  extractAcceptedPlanState,
  importProviderState,
  oracleAddress,
  oracleStateSource,
  oracleTimeoutMs,
  renderOracleImports,
  renderOracleRoot,
  type OracleCommandRequest,
  type OracleCommandRunner,
} from "../node-src/domain/import-oracle.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import { terraformJsonEqual } from "../node-src/json/python-equality.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";
import { PerformanceRecorder } from "../node-src/performance/recorder.js";

const ROOT = process.cwd();
const RESOURCE = "zia_url_categories";
const KEY = "example";
const IMPORT_ID = "CUSTOM_01";
const ADDRESS = oracleAddress(RESOURCE, KEY);

let loadedRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(): Promise<LoadedPackRoot> {
  loadedRoot ??= loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  return loadedRoot;
}

function plan(actions: readonly string[] = ["no-op"]): unknown {
  return {
    applyable: true,
    complete: true,
    errored: false,
    format_version: "1.2",
    resource_changes: [{
      address: ADDRESS,
      change: { actions, importing: { id: IMPORT_ID } },
      mode: "managed",
      provider_name: "registry.terraform.io/zscaler/zia",
      type: RESOURCE,
    }],
    terraform_version: "1.15.4",
  };
}

const DEFAULT_VALUES = { configured_name: "Example", description: "Read", id: IMPORT_ID };
const DEFAULT_SENSITIVE = { description: false, id: false };

function resourceObservation(
  values: Readonly<Record<string, unknown>>,
  sensitiveValues: unknown,
): Record<string, unknown> {
  return {
    address: ADDRESS,
    mode: "managed",
    provider_name: "registry.terraform.io/zscaler/zia",
    sensitive_values: sensitiveValues,
    type: RESOURCE,
    values,
  };
}

function state(
  include = true,
  values: Readonly<Record<string, unknown>> = DEFAULT_VALUES,
  sensitiveValues: unknown = DEFAULT_SENSITIVE,
): unknown {
  return {
    format_version: "1.0",
    terraform_version: "1.15.4",
    values: {
      root_module: {
        resources: include ? [resourceObservation(values, sensitiveValues)] : [],
      },
    },
  };
}

function planWithObservedState(
  values: Readonly<Record<string, unknown>> = DEFAULT_VALUES,
  sensitiveValues: unknown = DEFAULT_SENSITIVE,
): Record<string, unknown> {
  const output = plan() as Record<string, unknown>;
  const change = ((output.resource_changes as Record<string, unknown>[])[0]!.change) as Record<
    string,
    unknown
  >;
  Object.assign(change, {
    after: values,
    after_sensitive: sensitiveValues,
    after_unknown: {},
    before: values,
    before_sensitive: sensitiveValues,
  });
  output.planned_values = {
    root_module: { resources: [resourceObservation(values, sensitiveValues)] },
  };
  output.prior_state = state(true, values, sensitiveValues);
  return output;
}

class FakeTerraform implements OracleCommandRunner {
  readonly requests: OracleCommandRequest[] = [];
  readonly generated: string;
  plan: unknown = plan();
  state: unknown = state();
  failGeneratedPlan = false;

  constructor(generated?: string) {
    this.generated = generated ?? `resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n}\n`;
  }

  async run(request: OracleCommandRequest): Promise<{ readonly stdout: string }> {
    this.requests.push(request);
    if (request.debugName === "plan-generate-config") {
      const target = request.argv.find((argument) => argument.startsWith("-generate-config-out="));
      assert.notEqual(target, undefined);
      await writeFile(target!.slice("-generate-config-out=".length), this.generated);
      if (this.failGeneratedPlan) {
        throw new ProcessFailure({
          code: "TERRAFORM_COMMAND_FAILED",
          category: "domain",
          message: `provider rejected ${IMPORT_ID}`,
        });
      }
    }
    if (request.debugName === "show-plan") return { stdout: JSON.stringify(this.plan) };
    if (request.debugName === "show-state") return { stdout: JSON.stringify(this.state) };
    return { stdout: "" };
  }
}

test("generic Oracle renders provider pin/config and escaped deterministic imports", async () => {
  const root = await committedRoot();
  const main = await renderOracleRoot({ provider: "zia", root });
  assert.equal(main.includes('source = "zscaler/zia"'), true);
  assert.equal(main.includes('version = "4.7.26"'), true);
  assert.equal(main.includes("backend"), false);
  assert.equal(main.includes("cloud {"), false);

  const rendered = renderOracleImports(RESOURCE, new Map([
    ["second", "line\n${unsafe}%{also}"],
    [KEY, IMPORT_ID],
  ]));
  assert.equal(rendered.indexOf(ADDRESS) < rendered.indexOf(oracleAddress(RESOURCE, "second")), true);
  assert.equal(rendered.includes('id = "line\\n$${unsafe}%%{also}"'), true);
});

test("fake Terraform executes the exact local import/read transaction and cleans scratch data", async () => {
  const fake = new FakeTerraform();
  let now = 0;
  const performance = new PerformanceRecorder({ now: () => now++ });
  const output = await importProviderState({
    environment: { PATH: "/unused" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    performance,
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: fake,
  });
  assert.deepEqual([...output.keys()], [KEY]);
  assert.deepEqual({ ...output.get(KEY)?.values }, {
    configured_name: "Example",
    description: "Read",
    id: IMPORT_ID,
  });
  assert.deepEqual(fake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "show-plan",
    "apply-imports",
    "show-state",
  ]);
  const apply = fake.requests.find((request) => request.debugName === "apply-imports");
  assert.notEqual(apply, undefined);
  assert.equal(apply?.cwd, fake.requests[0]?.cwd);
  assert.equal(apply?.environment.TF_DATA_DIR, path.join(apply?.cwd ?? "", ".terraform"));
  assert.deepEqual(apply?.sensitiveTokens, [IMPORT_ID]);
  await assert.rejects(() => access(apply?.cwd ?? ""), /ENOENT/);
  const report = performance.report({
    command: "adopt",
    commandDurationMs: 20,
    commandStatus: "success",
  });
  assert.equal((report.summary as { terraform_commands: number }).terraform_commands, 5);
  const spans = report.spans as Array<{
    corrected_plan?: boolean;
    phase: string;
    terraform_commands?: number;
  }>;
  assert.deepEqual(spans.filter((span) => span.phase.startsWith("oracle.")).map(
    (span) => span.phase,
  ), [
    "oracle.corrected_plan",
    "oracle.generated_config_plan",
    "oracle.generated_config_policy",
    "oracle.init",
    "oracle.plan_show",
    "oracle.scratch_apply",
    "oracle.state_show",
    "oracle.state_source",
  ]);
  assert.deepEqual(
    spans.find((span) => span.phase === "oracle.corrected_plan"),
    {
      corrected_plan: false,
      duration_ms: 0,
      phase: "oracle.corrected_plan",
      resource_family: RESOURCE,
      status: "skipped",
      terraform_commands: 0,
    },
  );
});

test("accepted-plan state source skips scratch Apply and state show only for exact evidence", async () => {
  const values = parseDataJsonLosslessly(`{
    "configured_name": "Provider normalized",
    "description": null,
    "enabled": true,
    "large": 900719925474099312345678901,
    "ordered": ["first", "second"],
    "set_values": ["alpha", "beta"],
    "nested": [{"computed_default": "provider", "optional": null}]
  }`) as Readonly<Record<string, unknown>>;
  const sensitiveValues = {
    configured_name: false,
    nested: [{ computed_default: false, optional: true }],
  };
  const appliedFake = new FakeTerraform();
  appliedFake.plan = planWithObservedState(values, sensitiveValues);
  appliedFake.state = state(true, values, sensitiveValues);
  const applied = await importProviderState({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "applied-state" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: appliedFake,
  });

  const acceptedFake = new FakeTerraform();
  acceptedFake.plan = planWithObservedState(values, sensitiveValues);
  acceptedFake.state = state(false);
  let now = 0;
  const performance = new PerformanceRecorder({ now: () => now++ });
  const accepted = await importProviderState({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    performance,
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: acceptedFake,
  });

  assert.ok(terraformJsonEqual(accepted.get(KEY)?.values, applied.get(KEY)?.values));
  assert.ok(terraformJsonEqual(
    accepted.get(KEY)?.sensitiveValues,
    applied.get(KEY)?.sensitiveValues,
  ));
  assert.deepEqual(acceptedFake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "show-plan",
  ]);
  const report = performance.report({
    command: "adopt",
    commandDurationMs: 20,
    commandStatus: "success",
  });
  assert.equal((report.summary as { terraform_commands: number }).terraform_commands, 3);
  const spans = report.spans as Array<Record<string, unknown>>;
  assert.deepEqual(spans.find((span) => span.phase === "oracle.state_source"), {
    duration_ms: 0,
    oracle_state_source: "accepted-plan",
    phase: "oracle.state_source",
    resource_family: RESOURCE,
    status: "success",
    terraform_commands: 0,
  });
  for (const phase of ["oracle.scratch_apply", "oracle.state_show"]) {
    assert.deepEqual(spans.find((span) => span.phase === phase), {
      duration_ms: 0,
      phase,
      resource_family: RESOURCE,
      status: "skipped",
      terraform_commands: 0,
    });
  }
});

test("accepted-plan extractor matches the retained Terraform plan/state fixture", async () => {
  const source = parseDataJsonLosslessly(await readFile(
    path.join(ROOT, "node-tests", "fixtures", "terraform-import-structure-v1.15.4.json"),
    "utf8",
  )) as Record<string, unknown>;
  const fixturePlan = source.plan;
  const fixtureState = source.state as Record<string, unknown>;
  const address = "terraform_data.fixture";
  const output = extractAcceptedPlanState({
    addressToKey: new Map([[address, "fixture"]]),
    expectedImports: new Map([[address, "structural-fixture-id"]]),
    plan: fixturePlan,
    providerName: "terraform.io/builtin/terraform",
    resourceType: "terraform_data",
  });
  const stateResource = (((fixtureState.values as Record<string, unknown>).root_module as Record<
    string,
    unknown
  >).resources as Record<string, unknown>[])[0]!;
  assert.ok(terraformJsonEqual(output.get("fixture")?.values, stateResource.values));
  assert.ok(terraformJsonEqual(
    output.get("fixture")?.sensitiveValues,
    stateResource.sensitive_values,
  ));
});

test("accepted-plan state source rejects unknown, incomplete, or inconsistent evidence", async () => {
  const root = await committedRoot();
  const variants: Array<readonly [string, (candidate: Record<string, unknown>) => void]> = [
    ["unknown", (candidate) => {
      const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<string, unknown>;
      change.after_unknown = { nested: [{ computed_default: true }] };
    }],
    ["malformed unknown", (candidate) => {
      const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<string, unknown>;
      change.after_unknown = "unknown";
    }],
    ["planned mismatch", (candidate) => {
      const planned = candidate.planned_values as Record<string, unknown>;
      const rootModule = planned.root_module as Record<string, unknown>;
      const resource = (rootModule.resources as Record<string, unknown>[])[0]!;
      resource.values = { ...DEFAULT_VALUES, description: "different" };
    }],
    ["prior mismatch", (candidate) => {
      const prior = candidate.prior_state as Record<string, unknown>;
      const values = prior.values as Record<string, unknown>;
      const rootModule = values.root_module as Record<string, unknown>;
      const resource = (rootModule.resources as Record<string, unknown>[])[0]!;
      resource.values = { ...DEFAULT_VALUES, description: "different" };
    }],
    ["sensitivity mismatch", (candidate) => {
      const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<string, unknown>;
      change.after_sensitive = { description: true, id: false };
    }],
    ["bool-number mismatch", (candidate) => {
      const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<string, unknown>;
      change.after = { ...DEFAULT_VALUES, enabled: 1 };
      change.before = { ...DEFAULT_VALUES, enabled: true };
    }],
    ["missing planned state", (candidate) => {
      delete candidate.planned_values;
    }],
    ["missing prior state", (candidate) => {
      delete candidate.prior_state;
    }],
  ];
  for (const [name, mutate] of variants) {
    const fake = new FakeTerraform();
    const candidate = planWithObservedState();
    mutate(candidate);
    fake.plan = candidate;
    await assert.rejects(
      () => importProviderState({
        environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" },
        keyToImportId: new Map([[KEY, IMPORT_ID]]),
        resourceType: RESOURCE,
        root,
        runner: fake,
      }),
      /unknown|malformed|inconsistent|complete planned and prior state/u,
      name,
    );
    assert.equal(fake.requests.some((request) => request.debugName === "apply-imports"), false, name);
    assert.equal(fake.requests.some((request) => request.debugName === "show-state"), false, name);
  }
});

test("Oracle state-source selection is strict and invalid values fail before Terraform", async () => {
  const root = await committedRoot();
  assert.equal(oracleStateSource({}), "applied-state");
  assert.equal(oracleStateSource({ INFRAWRIGHT_ORACLE_STATE_SOURCE: "  " }), "applied-state");
  assert.equal(
    oracleStateSource({ INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" }),
    "accepted-plan",
  );
  assert.throws(
    () => oracleStateSource({ INFRAWRIGHT_ORACLE_STATE_SOURCE: "state" }),
    /must be applied-state or accepted-plan/u,
  );
  const fake = new FakeTerraform();
  await assert.rejects(
    () => importProviderState({
      environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "state" },
      keyToImportId: new Map([[KEY, IMPORT_ID]]),
      resourceType: RESOURCE,
      root,
      runner: fake,
    }),
    /must be applied-state or accepted-plan/u,
  );
  assert.deepEqual(fake.requests, []);
});

test("the scratch apply is unreachable for create, update, replace, destroy, drift, or incomplete coverage", async () => {
  const root = await committedRoot();
  const unsafeStatus = plan() as Record<string, unknown>;
  unsafeStatus.complete = false;
  const errored = plan() as Record<string, unknown>;
  errored.errored = true;
  const notApplyable = plan() as Record<string, unknown>;
  notApplyable.applyable = false;
  const wrongImport = plan() as Record<string, unknown>;
  ((wrongImport.resource_changes as Record<string, unknown>[])[0]!.change as Record<string, unknown>).importing = { id: "WRONG" };
  const wrongMode = plan() as Record<string, unknown>;
  (wrongMode.resource_changes as Record<string, unknown>[])[0]!.mode = "data";
  const wrongType = plan() as Record<string, unknown>;
  (wrongType.resource_changes as Record<string, unknown>[])[0]!.type = "zia_wrong";
  const wrongProvider = plan() as Record<string, unknown>;
  (wrongProvider.resource_changes as Record<string, unknown>[])[0]!.provider_name = "registry.terraform.io/example/wrong";
  const duplicate = plan() as Record<string, unknown>;
  duplicate.resource_changes = [
    ...(duplicate.resource_changes as unknown[]),
    ...(duplicate.resource_changes as unknown[]),
  ];
  const deferred = plan() as Record<string, unknown>;
  deferred.deferred_changes = [{ reason: "provider deferred" }];
  const diagnostics = plan() as Record<string, unknown>;
  diagnostics.diagnostics = [{ summary: "not safe" }];
  for (const invalid of [
    plan(["create"]),
    plan(["update"]),
    plan(["delete", "create"]),
    plan(["delete"]),
    { ...(plan() as Record<string, unknown>), resource_drift: [{ address: ADDRESS }] },
    { ...(plan() as Record<string, unknown>), resource_changes: [] },
    unsafeStatus,
    errored,
    notApplyable,
    wrongImport,
    wrongMode,
    wrongType,
    wrongProvider,
    duplicate,
    deferred,
    diagnostics,
  ]) {
    const fake = new FakeTerraform();
    fake.plan = invalid;
    await assert.rejects(
      () => importProviderState({
        keyToImportId: new Map([[KEY, IMPORT_ID]]),
        resourceType: RESOURCE,
        root,
        runner: fake,
      }),
      /refusing to apply the scratch plan/,
    );
    assert.equal(fake.requests.some((request) => request.debugName === "apply-imports"), false);
    await assert.rejects(() => access(fake.requests[0]?.cwd ?? ""), /ENOENT/);
  }
});

test("policy edits generated config and forces a second plan before authorization", async () => {
  const fake = new FakeTerraform(`resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n  description     = "DROP"\n}\n`);
  fake.failGeneratedPlan = true;
  const selected = new DriftPolicy({
    version: 1,
    resource_types: {
      [RESOURCE]: {
        projection_omit: [{
          path: "description",
          reason: "provider validation default",
          approved_by: "unit",
        }],
      },
    },
  });
  let now = 0;
  const performance = new PerformanceRecorder({ now: () => now++ });
  await importProviderState({
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    policy: selected,
    performance,
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: fake,
  });
  assert.deepEqual(fake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "plan-imports",
    "show-plan",
    "apply-imports",
    "show-state",
  ]);
  assert.deepEqual(selected.staleEntries({ modes: ["projection_omit"] }), []);
  const report = performance.report({
    command: "adopt",
    commandDurationMs: 20,
    commandStatus: "success",
  });
  assert.equal((report.summary as { terraform_commands: number }).terraform_commands, 6);
  const corrected = (report.spans as Array<Record<string, unknown>>).find(
    (span) => span.phase === "oracle.corrected_plan",
  );
  assert.equal(corrected?.corrected_plan, true);
  assert.equal(corrected?.terraform_commands, 1);
});

test("accepted-plan mode uses the corrected plan and skips only Apply/state show", async () => {
  const fake = new FakeTerraform(`resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n  description     = "DROP"\n}\n`);
  fake.failGeneratedPlan = true;
  fake.plan = planWithObservedState();
  const selected = new DriftPolicy({
    version: 1,
    resource_types: {
      [RESOURCE]: {
        projection_omit: [{
          path: "description",
          reason: "provider validation default",
          approved_by: "unit",
        }],
      },
    },
  });
  const output = await importProviderState({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    policy: selected,
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: fake,
  });
  assert.deepEqual([...output.keys()], [KEY]);
  assert.deepEqual(fake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "plan-imports",
    "show-plan",
  ]);
});

test("missing state, duplicate import IDs, and malformed plan coverage fail closed", async () => {
  const root = await committedRoot();
  const fake = new FakeTerraform();
  fake.state = state(false);
  await assert.rejects(
    () => importProviderState({
      keyToImportId: new Map([[KEY, IMPORT_ID]]),
      resourceType: RESOURCE,
      root,
      runner: fake,
    }),
    /non-exact root state/,
  );
  await assert.rejects(
    () => importProviderState({
      keyToImportId: new Map([["one", IMPORT_ID], ["two", IMPORT_ID]]),
      resourceType: RESOURCE,
      root,
      runner: new FakeTerraform(),
    }),
    /duplicate import_id/,
  );
  assert.throws(
    () => assertImportOnlyPlan({
      expectedImports: new Map([[ADDRESS, IMPORT_ID]]),
      plan: { ...(plan() as Record<string, unknown>), resource_changes: [{
        address: ADDRESS,
        change: { actions: ["no-op"] },
        mode: "managed",
        provider_name: "registry.terraform.io/zscaler/zia",
        type: RESOURCE,
      }] },
      providerName: "registry.terraform.io/zscaler/zia",
      resourceType: RESOURCE,
    }),
    /not the exact requested import/,
  );
  const exact = plan() as Record<string, unknown>;
  const first = (exact.resource_changes as Record<string, unknown>[])[0]!;
  exact.resource_changes = [first, first];
  assert.throws(
    () => assertImportOnlyPlan({
      expectedImports: new Map([
        [ADDRESS, IMPORT_ID],
        [oracleAddress(RESOURCE, "other"), "OTHER"],
      ]),
      plan: exact,
      providerName: "registry.terraform.io/zscaler/zia",
      resourceType: RESOURCE,
    }),
    /not the exact requested import/,
  );
});

test("state extraction rejects extra, child, wrong-mode, wrong-provider, and malformed observations", async () => {
  const root = await committedRoot();
  const variants: unknown[] = [];
  const extra = state() as Record<string, unknown>;
  const extraRoot = ((extra.values as Record<string, unknown>).root_module as Record<string, unknown>);
  extraRoot.resources = [...(extraRoot.resources as unknown[]), {
    address: `${RESOURCE}.extra`,
    mode: "managed",
    provider_name: "registry.terraform.io/zscaler/zia",
    sensitive_values: {},
    type: RESOURCE,
    values: { id: "extra" },
  }];
  variants.push(extra);
  const child = state() as Record<string, unknown>;
  ((child.values as Record<string, unknown>).root_module as Record<string, unknown>).child_modules = [{ resources: [] }];
  variants.push(child);
  for (const [field, value] of [
    ["mode", "data"],
    ["type", "zia_wrong"],
    ["provider_name", "registry.terraform.io/example/wrong"],
    ["values", null],
    ["sensitive_values", null],
  ] as const) {
    const candidate = state() as Record<string, unknown>;
    const resources = ((candidate.values as Record<string, unknown>).root_module as Record<string, unknown>).resources as Record<string, unknown>[];
    resources[0]![field] = value;
    variants.push(candidate);
  }
  for (const candidate of variants) {
    const fake = new FakeTerraform();
    fake.state = candidate;
    await assert.rejects(
      () => importProviderState({
        keyToImportId: new Map([[KEY, IMPORT_ID]]),
        resourceType: RESOURCE,
        root,
        runner: fake,
      }),
      /non-exact|malformed/,
    );
    assert.equal(fake.requests.some((request) => request.debugName === "show-state"), true);
  }
});

test("retained Oracle workdir is explicit and warns that it contains sensitive material", async (context) => {
  const fake = new FakeTerraform();
  const diagnostics: string[] = [];
  await importProviderState({
    keepWorkdir: true,
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    onDiagnostic: (message) => diagnostics.push(message),
    resourceType: RESOURCE,
    root: await committedRoot(),
    runner: fake,
  });
  const directory = fake.requests[0]?.cwd;
  assert.notEqual(directory, undefined);
  context.after(() => rm(directory ?? "", { force: true, recursive: true }));
  await access(directory ?? "");
  assert.equal(diagnostics.length, 1);
  assert.match(diagnostics[0] ?? "", /unencrypted provider state/);
});

test("real command adapter bounds output and never returns import IDs in failures", async (context) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-oracle-command-"));
  context.after(() => rm(directory, { force: true, recursive: true }));
  const executable = path.join(directory, "fake-terraform");
  await writeFile(executable, `#!/bin/sh\nprintf '%s\\n' '${IMPORT_ID}' >&2\nexit 9\n`);
  await chmod(executable, 0o700);
  const runner = createOracleCommandRunner({
    limits: { maxStderrBytes: 1024, maxStdoutBytes: 1024, timeoutMs: 5_000 },
    terraformExecutable: executable,
  });
  let thrown: unknown;
  try {
    await runner.run({
      argv: ["plan"],
      cwd: directory,
      debugName: "redaction-check",
      environment: {},
      output: "capture",
      sensitiveTokens: [IMPORT_ID],
    });
  } catch (error: unknown) {
    thrown = error;
  }
  assert.ok(thrown instanceof ProcessFailure);
  assert.equal(thrown.message.includes(IMPORT_ID), false);
  assert.match(thrown.message, /provider diagnostics and import IDs were redacted/);
});

test("Oracle timeout defaults to five minutes and accepts any positive practical duration", () => {
  assert.equal(oracleTimeoutMs({}), 300_000);
  assert.equal(oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "0.25" }), 250);
  assert.equal(oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "601" }), 601_000);
  assert.equal(oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "86400" }), 86_400_000);
  assert.throws(() => oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "0" }), /positive number/);
  assert.throws(
    () => oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "1e100" }),
    /numeric range/,
  );
});

test("Oracle preserves the Windows operational-platform refusal", async (context) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-oracle-platform-"));
  context.after(() => rm(directory, { force: true, recursive: true }));
  const executable = path.join(directory, "fake-terraform");
  await writeFile(executable, "#!/bin/sh\nexit 0\n");
  await chmod(executable, 0o700);
  const runner = createOracleCommandRunner({ terraformExecutable: executable });
  const platform = Object.getOwnPropertyDescriptor(process, "platform");
  assert.notEqual(platform, undefined);
  try {
    Object.defineProperty(process, "platform", { ...platform, value: "win32" });
    await assert.rejects(runner.run({
      argv: ["plan"],
      cwd: directory,
      debugName: "platform-check",
      environment: {},
      output: "discard",
      sensitiveTokens: [],
    }), (error: unknown) => {
      assert.ok(error instanceof ProcessFailure);
      assert.equal(error.code, "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM");
      assert.equal(
        error.message,
        "Terraform execution through Infrawright is supported on Linux and macOS; Windows is not a supported operational platform.",
      );
      return true;
    });
  } finally {
    Object.defineProperty(process, "platform", platform ?? {});
  }
});
