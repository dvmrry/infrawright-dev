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
  importProviderStates,
  oracleAddress,
  oracleBatchResourceFamily,
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
const BATCH_RESOURCE = "zia_cloud_app_control_rule";
const BATCH_KEY = "second";
const BATCH_IMPORT_ID = "SECOND_01";
const BATCH_ADDRESS = oracleAddress(BATCH_RESOURCE, BATCH_KEY);

let loadedRoot: Promise<LoadedPackRoot> | undefined;

test("Oracle batch performance labels are deterministic and bounded", () => {
  assert.equal(oracleBatchResourceFamily(["zia_url_categories"]), "zia_url_categories");
  assert.equal(
    oracleBatchResourceFamily(["zia_url_categories", "zia_rule_labels"]),
    oracleBatchResourceFamily(["zia_rule_labels", "zia_url_categories"]),
  );
  assert.match(
    oracleBatchResourceFamily(["zia_url_categories", "zia_rule_labels"]),
    /^oracle_batch\.zia_rule_labels\.zia_url_categories$/u,
  );
  const wide = Array.from({ length: 40 }, (_, index) => {
    return `zia_resource_family_${String(index).padStart(3, "0")}_with_a_long_name`;
  });
  const label = oracleBatchResourceFamily(wide);
  assert.ok(label.length <= 256);
  assert.match(label, /^oracle_batch_40_[0-9a-f]{16}$/u);
  assert.equal(label, oracleBatchResourceFamily([...wide].reverse()));
});

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

function batchResourceObservation(options: {
  readonly address: string;
  readonly resourceType: string;
  readonly values: Readonly<Record<string, unknown>>;
}): Record<string, unknown> {
  return {
    address: options.address,
    mode: "managed",
    provider_name: "registry.terraform.io/zscaler/zia",
    sensitive_values: {},
    type: options.resourceType,
    values: options.values,
  };
}

function batchState(): Record<string, unknown> {
  return {
    format_version: "1.0",
    terraform_version: "1.15.4",
    values: {
      root_module: {
        resources: [
          batchResourceObservation({ address: ADDRESS, resourceType: RESOURCE, values: DEFAULT_VALUES }),
          batchResourceObservation({
            address: BATCH_ADDRESS,
            resourceType: BATCH_RESOURCE,
            values: { id: BATCH_IMPORT_ID, name: "Second" },
          }),
        ],
      },
    },
  };
}

function batchPlanWithObservedState(): Record<string, unknown> {
  const stateValue = batchState();
  const resources = (((stateValue.values as Record<string, unknown>).root_module as Record<
    string,
    unknown
  >).resources as Record<string, unknown>[]);
  return {
    applyable: true,
    complete: true,
    errored: false,
    format_version: "1.2",
    planned_values: (stateValue.values as Record<string, unknown>),
    prior_state: stateValue,
    resource_changes: resources.map((resource) => {
      const importId = resource.address === ADDRESS ? IMPORT_ID : BATCH_IMPORT_ID;
      return {
        address: resource.address,
        change: {
          actions: ["no-op"],
          after: resource.values,
          after_sensitive: resource.sensitive_values,
          after_unknown: {},
          before: resource.values,
          before_sensitive: resource.sensitive_values,
          importing: { id: importId },
        },
        mode: "managed",
        provider_name: resource.provider_name,
        type: resource.type,
      };
    }),
    terraform_version: "1.15.4",
  };
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

test("provider batch shares one Oracle transaction and splits accepted or applied state by type", async () => {
  const generated = `resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n}\n\n`
    + `resource "${BATCH_RESOURCE}" "${BATCH_ADDRESS.split(".")[1]}" {\n  name = "Second"\n}\n`;
  const requests = [{
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    resourceType: RESOURCE,
  }, {
    keyToImportId: new Map([[BATCH_KEY, BATCH_IMPORT_ID]]),
    resourceType: BATCH_RESOURCE,
  }] as const;
  const acceptedFake = new FakeTerraform(generated);
  acceptedFake.plan = batchPlanWithObservedState();
  const accepted = await importProviderStates({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" },
    resources: requests,
    root: await committedRoot(),
    runner: acceptedFake,
  });
  assert.deepEqual(acceptedFake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "show-plan",
  ]);
  assert.deepEqual([...accepted.get(RESOURCE)?.keys() ?? []], [KEY]);
  assert.deepEqual([...accepted.get(BATCH_RESOURCE)?.keys() ?? []], [BATCH_KEY]);

  const appliedFake = new FakeTerraform(generated);
  appliedFake.plan = batchPlanWithObservedState();
  appliedFake.state = batchState();
  const applied = await importProviderStates({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "applied-state" },
    resources: requests,
    root: await committedRoot(),
    runner: appliedFake,
  });
  assert.deepEqual(appliedFake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "show-plan",
    "apply-imports",
    "show-state",
  ]);
  assert.ok(terraformJsonEqual(
    accepted.get(RESOURCE)?.get(KEY)?.values,
    applied.get(RESOURCE)?.get(KEY)?.values,
  ));
  assert.ok(terraformJsonEqual(
    accepted.get(BATCH_RESOURCE)?.get(BATCH_KEY)?.values,
    applied.get(BATCH_RESOURCE)?.get(BATCH_KEY)?.values,
  ));
});

test("provider batch validates each address type, provider, and import ID before scratch Apply", async () => {
  const root = await committedRoot();
  const generated = `resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n}\n\n`
    + `resource "${BATCH_RESOURCE}" "${BATCH_ADDRESS.split(".")[1]}" {\n  name = "Second"\n}\n`;
  for (const mutate of [
    (change: Record<string, unknown>) => { change.type = RESOURCE; },
    (change: Record<string, unknown>) => {
      change.provider_name = "registry.terraform.io/example/wrong";
    },
    (change: Record<string, unknown>) => {
      (change.change as Record<string, unknown>).importing = { id: "WRONG" };
    },
  ]) {
    const fake = new FakeTerraform(generated);
    const candidate = batchPlanWithObservedState();
    const change = (candidate.resource_changes as Record<string, unknown>[])[1]!;
    mutate(change);
    fake.plan = candidate;
    await assert.rejects(
      () => importProviderStates({
        resources: [{ keyToImportId: new Map([[KEY, IMPORT_ID]]), resourceType: RESOURCE }, {
          keyToImportId: new Map([[BATCH_KEY, BATCH_IMPORT_ID]]),
          resourceType: BATCH_RESOURCE,
        }],
        root,
        runner: fake,
      }),
      /not the exact requested import/u,
    );
    assert.equal(fake.requests.some((request) => request.debugName === "apply-imports"), false);
  }
});

test("exact-import rejection points to retained diagnostics without leaking plan evidence", async () => {
  const root = await committedRoot();
  const fake = new FakeTerraform();
  const candidate = plan(["update"]) as Record<string, unknown>;
  const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<
    string,
    unknown
  >;
  Object.assign(change, {
    after: { message: "new secret", nested: { "tenant-secret-key": "new value" } },
    before: { message: "old secret", nested: { "tenant-secret-key": "old value" } },
  });
  fake.plan = candidate;

  let rejection: unknown;
  try {
    await importProviderState({
      keyToImportId: new Map([[KEY, IMPORT_ID]]),
      resourceType: RESOURCE,
      root,
      runner: fake,
    });
  } catch (error: unknown) {
    rejection = error;
  }

  assert.ok(rejection instanceof Error);
  assert.match(rejection.message, /INFRAWRIGHT_KEEP_ORACLE=1/u);
  for (const secret of [
    IMPORT_ID,
    "new secret",
    "old secret",
    "tenant-secret-key",
    "new value",
    "old value",
  ]) {
    assert.equal(rejection.message.includes(secret), false);
  }
  assert.equal(fake.requests.some((request) => request.debugName === "apply-imports"), false);
});

test("exact-import rejection redacts an unexpected plan address", () => {
  const unexpectedAddress = "tenant-controlled-address-secret";
  const candidate = plan() as Record<string, unknown>;
  (candidate.resource_changes as Record<string, unknown>[])[0]!.address = unexpectedAddress;

  let rejection: unknown;
  try {
    assertImportOnlyPlan({
      expectedImports: new Map([[ADDRESS, IMPORT_ID]]),
      plan: candidate,
      providerName: "registry.terraform.io/zscaler/zia",
      resourceType: RESOURCE,
    });
  } catch (error: unknown) {
    rejection = error;
  }

  assert.ok(rejection instanceof Error);
  assert.match(rejection.message, /<unexpected address>/u);
  assert.match(rejection.message, /INFRAWRIGHT_KEEP_ORACLE=1/u);
  assert.equal(rejection.message.includes(unexpectedAddress), false);
  assert.equal(rejection.message.includes(IMPORT_ID), false);
});

test("provider batch applies per-type generated-config policy with at most one corrected plan", async () => {
  const generated = `resource "${RESOURCE}" "${ADDRESS.split(".")[1]}" {\n  configured_name = "Example"\n  description = "DROP"\n}\n\n`
    + `resource "${BATCH_RESOURCE}" "${BATCH_ADDRESS.split(".")[1]}" {\n  name = "Second"\n}\n`;
  const fake = new FakeTerraform(generated);
  fake.failGeneratedPlan = true;
  fake.plan = batchPlanWithObservedState();
  fake.state = batchState();
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
  await importProviderStates({
    resources: [{
      keyToImportId: new Map([[KEY, IMPORT_ID]]),
      policy: selected,
      resourceType: RESOURCE,
    }, {
      keyToImportId: new Map([[BATCH_KEY, BATCH_IMPORT_ID]]),
      resourceType: BATCH_RESOURCE,
    }],
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
  assert.equal(fake.requests.filter((request) => request.debugName === "plan-imports").length, 1);
  assert.deepEqual(selected.staleEntries({ modes: ["projection_omit"] }), []);
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

test("accepted-plan wrapper rejects extra expected-import addresses", () => {
  assert.throws(
    () => extractAcceptedPlanState({
      addressToKey: new Map([[ADDRESS, KEY]]),
      expectedImports: new Map([
        [ADDRESS, IMPORT_ID],
        [`${RESOURCE}.iw_extra`, "EXTRA"],
      ]),
      plan: {},
      providerName: "registry.terraform.io/zscaler/zia",
      resourceType: RESOURCE,
    }),
    /address maps did not match.*unexpected=zia_url_categories\.iw_extra/u,
  );
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
    ["lossless decimal mismatch", (candidate) => {
      const [before, after] = parseDataJsonLosslessly(
        '[{"quota":9007199254740992.0},{"quota":9007199254740993.0}]',
      ) as readonly Record<string, unknown>[];
      const change = ((candidate.resource_changes as Record<string, unknown>[])[0]!.change) as Record<string, unknown>;
      change.before = before;
      change.after = after;
      const planned = candidate.planned_values as Record<string, unknown>;
      const plannedRoot = planned.root_module as Record<string, unknown>;
      (plannedRoot.resources as Record<string, unknown>[])[0]!.values = after;
      const prior = candidate.prior_state as Record<string, unknown>;
      const priorValues = prior.values as Record<string, unknown>;
      const priorRoot = priorValues.root_module as Record<string, unknown>;
      (priorRoot.resources as Record<string, unknown>[])[0]!.values = before;
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

test("pack drop_if_default edits generated config and forces a second plan", async () => {
  const resourceType = "zia_url_filtering_rules";
  const key = "quota";
  const importId = "QUOTA_01";
  const address = oracleAddress(resourceType, key);
  const generated = `resource "${resourceType}" "${address.split(".")[1]}" {\n  id         = "${importId}"\n  name       = "Quota"\n  size_quota = 0\n}\n`;
  const fake = new FakeTerraform(generated);
  fake.failGeneratedPlan = true;
  fake.plan = {
    applyable: true,
    complete: true,
    errored: false,
    format_version: "1.2",
    resource_changes: [{
      address,
      change: { actions: ["no-op"], importing: { id: importId } },
      mode: "managed",
      provider_name: "registry.terraform.io/zscaler/zia",
      type: resourceType,
    }],
    terraform_version: "1.15.4",
  };
  fake.state = {
    format_version: "1.0",
    terraform_version: "1.15.4",
    values: {
      root_module: {
        resources: [batchResourceObservation({
          address,
          resourceType,
          values: { id: importId, name: "Quota" },
        })],
      },
    },
  };

  const output = await importProviderState({
    keyToImportId: new Map([[key, importId]]),
    resourceType,
    root: await committedRoot(),
    runner: fake,
  });

  assert.deepEqual([...output.keys()], [key]);
  assert.deepEqual(fake.requests.map((request) => request.debugName), [
    "init",
    "plan-generate-config",
    "plan-imports",
    "show-plan",
    "apply-imports",
    "show-state",
  ]);
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
  let now = 0;
  const performance = new PerformanceRecorder({ now: () => now++ });
  const output = await importProviderState({
    environment: { INFRAWRIGHT_ORACLE_STATE_SOURCE: "accepted-plan" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    performance,
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
  const report = performance.report({
    command: "adopt",
    commandDurationMs: 20,
    commandStatus: "success",
  });
  assert.equal((report.summary as { terraform_commands: number }).terraform_commands, 4);
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
