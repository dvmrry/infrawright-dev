import assert from "node:assert/strict";
import { access, chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  assertImportOnlyPlan,
  createOracleCommandRunner,
  importProviderState,
  oracleAddress,
  oracleTimeoutMs,
  renderOracleImports,
  renderOracleRoot,
  type OracleCommandRequest,
  type OracleCommandRunner,
} from "../node-src/domain/import-oracle.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

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
    resource_changes: [{
      address: ADDRESS,
      change: { actions, importing: { id: IMPORT_ID } },
      mode: "managed",
      type: RESOURCE,
    }],
  };
}

function state(include = true): unknown {
  return {
    values: {
      root_module: {
        resources: include ? [{
          address: ADDRESS,
          mode: "managed",
          sensitive_values: { description: false, id: false },
          type: RESOURCE,
          values: { configured_name: "Example", description: "Read", id: IMPORT_ID },
        }] : [],
      },
    },
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
  const output = await importProviderState({
    environment: { PATH: "/unused" },
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
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
});

test("the scratch apply is unreachable for create, update, replace, destroy, drift, or incomplete coverage", async () => {
  const root = await committedRoot();
  for (const invalid of [
    plan(["create"]),
    plan(["update"]),
    plan(["delete", "create"]),
    plan(["delete"]),
    { resource_drift: [{ address: ADDRESS }], resource_changes: [
      { address: ADDRESS, change: { actions: ["no-op"], importing: { id: IMPORT_ID } } },
    ] },
    { resource_changes: [] },
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
  await importProviderState({
    keyToImportId: new Map([[KEY, IMPORT_ID]]),
    policy: selected,
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
    /did not return state for key/,
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
      expectedAddresses: new Set([ADDRESS]),
      plan: { resource_changes: [{ address: ADDRESS, change: { actions: ["no-op"] } }] },
      resourceType: RESOURCE,
    }),
    /not import-only/,
  );
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

test("Oracle timeout configuration is bounded and rejects invalid values", () => {
  assert.equal(oracleTimeoutMs({}), 300_000);
  assert.equal(oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "0.25" }), 250);
  assert.throws(() => oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "0" }), /positive number/);
  assert.throws(() => oracleTimeoutMs({ INFRAWRIGHT_ORACLE_TIMEOUT_SECONDS: "601" }), /600 second bound/);
});
