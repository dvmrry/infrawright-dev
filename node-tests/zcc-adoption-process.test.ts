import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  linkSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";
import test from "node:test";
import { stringify as stringifyLosslessJson } from "lossless-json";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccAdoptionArtifactParity,
  validateZccAdoptionArtifactMaterialization,
  validateZccAdoptionArtifactSet,
} from "../node-src/contracts/validators.js";
import {
  compareZccAdoptionArtifactsOperation,
  compileZccAdoptionArtifactsOperation,
  materializeZccAdoptionArtifactsOperation,
} from "../node-src/domain/zcc-adoption-operation.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  CompileAdoptionArtifactsProcessRequest,
  CompileAdoptionArtifactsProcessSuccessResponse,
  CompareAdoptionArtifactsProcessRequest,
  CompareAdoptionArtifactsProcessSuccessResponse,
  MaterializeAdoptionArtifactsProcessRequest,
  MaterializeAdoptionArtifactsProcessSuccessResponse,
  ProcessErrorResponse,
  ProcessResponse,
} from "../node-src/process/types.js";

const REPO = process.cwd();
const TENANT = "adoption_process";
const ROOT_CATALOG = path.join(REPO, "catalogs/zscaler-root-catalog.v1.json");
const CORPUS = path.join(
  REPO,
  "node-tests/fixtures/zcc-adoption-projection-corpus.v1.json",
);
const FAILOPEN = path.join(
  REPO,
  "tests/fixtures/parity/zcc_failopen_policy_inversion.json",
);
const PROCESS_BUNDLE = path.join(REPO, "dist/infrawright.mjs");
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

interface ResourceEvidence {
  readonly rawText: string;
  readonly states: Readonly<Record<string, {
    readonly sensitiveJson: string;
    readonly valuesJson: string;
    readonly wrongIdValuesJson: string;
  }>>;
}

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly tempRoot: string;
  readonly fake: string;
  readonly log: string;
  readonly evidence: Readonly<Record<ZccPullResourceType, ResourceEvidence>>;
}

type FakeBehavior =
  | "ok"
  | "fail-init"
  | "bad-plan-envelope"
  | "bad-plan-provider"
  | "bad-state-id"
  | "bad-state-provider"
  | "missing-state-resource";

function renderLosslessJson(value: unknown): string {
  const rendered = stringifyLosslessJson(value);
  assert.notEqual(rendered, undefined);
  return rendered as string;
}

function evidence(): Readonly<Record<ZccPullResourceType, ResourceEvidence>> {
  const lossless = parseDataJsonLosslessly(readFileSync(CORPUS, "utf8")) as {
    readonly cases: readonly {
      readonly expected: string;
      readonly resource_type: ZccPullResourceType;
      readonly raw_items: readonly unknown[];
      readonly observed_states: readonly {
        readonly import_id: string;
        readonly sensitive_values?: unknown;
        readonly values: Readonly<Record<string, unknown>>;
      }[];
    }[];
  };
  const output = Object.create(null) as Record<
    ZccPullResourceType,
    ResourceEvidence
  >;
  for (const resourceType of RESOURCES.filter(
    (value) => value !== "zcc_failopen_policy",
  )) {
    const losslessCase = lossless.cases.find((entry) => {
      return entry.expected === "success"
        && entry.resource_type === resourceType
        && entry.raw_items.length > 0;
    });
    assert.notEqual(losslessCase, undefined, resourceType);
    const states = Object.create(null) as Record<string, {
      sensitiveJson: string;
      valuesJson: string;
      wrongIdValuesJson: string;
    }>;
    for (const observation of losslessCase?.observed_states ?? []) {
      states[observation.import_id] = {
        sensitiveJson: renderLosslessJson(
          observation.sensitive_values ?? {},
        ),
        valuesJson: renderLosslessJson({
          ...observation.values,
          id: observation.import_id,
        }),
        wrongIdValuesJson: renderLosslessJson({
          ...observation.values,
          id: "wrong-id",
        }),
      };
    }
    output[resourceType] = {
      rawText: `${renderLosslessJson(losslessCase?.raw_items)}\n`,
      states,
    };
  }
  const failopen = parseDataJsonLosslessly(readFileSync(FAILOPEN, "utf8")) as {
    readonly raw_items: readonly unknown[];
    readonly provider_state: Readonly<Record<string, {
      readonly sensitive_values?: unknown;
      readonly values: Readonly<Record<string, unknown>>;
    }>>;
  };
  const failopenStates = Object.create(null) as Record<string, {
    sensitiveJson: string;
    valuesJson: string;
    wrongIdValuesJson: string;
  }>;
  for (const [importId, state] of Object.entries(failopen.provider_state)) {
    failopenStates[importId] = {
      sensitiveJson: renderLosslessJson(state.sensitive_values ?? {}),
      valuesJson: renderLosslessJson({ ...state.values, id: importId }),
      wrongIdValuesJson: renderLosslessJson({
        ...state.values,
        id: "wrong-id",
      }),
    };
  }
  output.zcc_failopen_policy = {
    rawText: `${renderLosslessJson(failopen.raw_items)}\n`,
    states: failopenStates,
  };
  return Object.freeze(output);
}

const EVIDENCE = evidence();

function writeFake(options: {
  readonly executable: string;
  readonly log: string;
  readonly behavior?: FakeBehavior;
  readonly mutation?: { readonly path: string; readonly content: string };
}): void {
  const stateMap = Object.fromEntries(RESOURCES.map((resourceType) => [
    resourceType,
    EVIDENCE[resourceType].states,
  ]));
  const behavior = options.behavior ?? "ok";
  const script = [
    `#!${process.execPath}`,
    'import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";',
    'import path from "node:path";',
    `const behavior = ${JSON.stringify(behavior)};`,
    `const mutation = ${JSON.stringify(options.mutation ?? null)};`,
    `const stateMap = ${JSON.stringify(stateMap)};`,
    `const log = ${JSON.stringify(options.log)};`,
    "const argv = process.argv.slice(2);",
    "appendFileSync(log, JSON.stringify({ argv, cwd: process.cwd(), environment: process.env }) + '\\n');",
    "const offset = argv[0]?.startsWith('-chdir=') ? 1 : 0;",
    "const command = argv[offset];",
    "const rest = argv.slice(offset + 1);",
    "const imports = () => {",
    "  const text = readFileSync('imports.tf', 'utf8');",
    "  return text.split(/(?:^|\\n)import \\{\\n/).slice(1).map((block) => {",
    "    const address = /^  to = ([^\\n]+)$/m.exec(block)?.[1];",
    "    const literal = /^  id = (\"(?:[^\"\\\\]|\\\\.)*\")$/m.exec(block)?.[1];",
    "    if (!address || !literal) process.exit(71);",
    "    return { address, importId: JSON.parse(literal), resourceType: address.split('.')[0] };",
    "  });",
    "};",
    "if (command === 'init') {",
    "  if (behavior === 'fail-init') {",
    "    process.stderr.write('provider-child-secret credential-secret import-secret scratch-secret');",
    "    process.exit(77);",
    "  }",
    "  if (existsSync('.terraform.lock.hcl')) {",
    "    const lock = readFileSync('.terraform.lock.hcl', 'utf8');",
    "    if (!lock.includes('registry.terraform.io/zscaler/zcc')) process.exit(74);",
    "  }",
    "} else if (command === 'plan') {",
    "  const generated = rest.find((value) => value.startsWith('-generate-config-out='))?.slice(21);",
    "  const plan = rest.find((value) => value.startsWith('-out='))?.slice(5);",
    "  if (!plan) process.exit(72);",
    "  if (generated) writeFileSync(generated, 'resource generated {}\\n');",
    "  writeFileSync(plan, 'opaque saved plan\\n');",
    "} else if (command === 'apply') {",
    "  writeFileSync('terraform.tfstate', 'opaque local state\\n');",
    "  if (mutation) { mkdirSync(path.dirname(mutation.path), { recursive: true }); writeFileSync(mutation.path, mutation.content); }",
    "} else if (command === 'show') {",
    "  const imported = imports();",
    "  const snapshot = rest[1] ?? '';",
    "  const plan = snapshot.endsWith('oracle.tfplan');",
    "  const badProvider = (plan && behavior === 'bad-plan-provider') || (!plan && behavior === 'bad-state-provider');",
    "  const provider = badProvider ? 'registry.terraform.io/other/provider' : 'registry.terraform.io/zscaler/zcc';",
    "  if (plan) {",
    "    const result = {",
    "      format_version: '1.2', terraform_version: '1.15.4',",
    "      applyable: true, complete: behavior !== 'bad-plan-envelope', errored: false,",
    "      checks: [], deferred_changes: [], action_invocations: [], deferred_action_invocations: [],",
    "      resource_drift: [], output_changes: {}, errors: [], diagnostics: [],",
    "      resource_changes: imported.map((entry) => ({",
    "        address: entry.address, mode: 'managed', type: entry.resourceType, provider_name: provider,",
    "        change: { actions: ['no-op'], importing: { id: entry.importId } },",
    "      })),",
    "    };",
    "    process.stdout.write(JSON.stringify(result));",
    "  } else {",
    "    let resources = imported.map((entry) => {",
    "      const state = stateMap[entry.resourceType]?.[entry.importId];",
    "      if (!state) process.exit(75);",
    "      const valuesJson = behavior === 'bad-state-id' ? state.wrongIdValuesJson : state.valuesJson;",
    "      return '{'",
    "        + '\"address\":' + JSON.stringify(entry.address)",
    "        + ',\"mode\":\"managed\"'",
    "        + ',\"type\":' + JSON.stringify(entry.resourceType)",
    "        + ',\"provider_name\":' + JSON.stringify(provider)",
    "        + ',\"values\":' + valuesJson",
    "        + ',\"sensitive_values\":' + state.sensitiveJson",
    "        + '}';",
    "    });",
    "    if (behavior === 'missing-state-resource') resources = resources.slice(1);",
    "    process.stdout.write('{\"format_version\":\"1.0\",\"terraform_version\":\"1.15.4\",\"checks\":[],\"values\":{\"outputs\":{},\"root_module\":{\"resources\":[' + resources.join(',') + '],\"child_modules\":[]}}}');",
    "  }",
    "} else { process.exit(73); }",
  ].join("\n");
  writeFileSync(options.executable, script, { mode: 0o700 });
  chmodSync(options.executable, 0o700);
}

function deployment(grouped = false): unknown {
  return grouped
    ? {
        overlay: "stack",
        roots: {
          zcc: {
            bind_references: false,
            groups: { zcc_bundle: [...RESOURCES] },
          },
        },
      }
    : { overlay: ".", roots: {} };
}

function createFixture(options: {
  readonly deployment?: unknown;
  readonly behavior?: FakeBehavior;
  readonly mutation?: { readonly path: string; readonly content: string };
} = {}): Fixture {
  const lexical = mkdtempSync(path.join(os.tmpdir(), "zcc-adoption-process-"));
  chmodSync(lexical, 0o700);
  const root = realpathSync(lexical);
  const workspace = path.join(root, "workspace");
  const tempRoot = path.join(root, "oracle-private");
  const fake = path.join(root, "terraform-fake");
  const log = path.join(root, "invocations.jsonl");
  mkdirSync(workspace, { mode: 0o700 });
  mkdirSync(tempRoot, { mode: 0o700 });
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "catalog.json");
  writeFileSync(
    deploymentPath,
    `${JSON.stringify(options.deployment ?? deployment())}\n`,
  );
  copyFileSync(ROOT_CATALOG, catalogPath);
  const pullRoot = path.join(workspace, "pulls", TENANT);
  mkdirSync(pullRoot, { recursive: true });
  for (const resourceType of RESOURCES) {
    writeFileSync(
      path.join(pullRoot, `${resourceType}.json`),
      EVIDENCE[resourceType].rawText,
    );
  }
  writeFake({
    executable: fake,
    log,
    ...(options.behavior === undefined ? {} : { behavior: options.behavior }),
    ...(options.mutation === undefined ? {} : { mutation: options.mutation }),
  });
  return {
    root,
    workspace,
    deploymentPath,
    catalogPath,
    tempRoot,
    fake,
    log,
    evidence: EVIDENCE,
  };
}

function request(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): CompileAdoptionArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `adopt-${resourceType}`,
    operation: "compile_adoption_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      tenant: TENANT,
      resource_type: resourceType,
    },
  };
}

function compareRequest(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
): CompareAdoptionArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `compare-adopt-${resourceType}`,
    operation: "compare_adoption_artifacts",
    context: {
      workspace: fixture.workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      reference: "materialized",
      tenant: TENANT,
      resource_type: resourceType,
    },
  };
}

function materializeRequest(
  fixture: Fixture,
  workspace: string,
  resourceType: ZccPullResourceType,
  assertion: CompareAdoptionArtifactsProcessSuccessResponse["result"],
): MaterializeAdoptionArtifactsProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `materialize-adopt-${resourceType}`,
    operation: "materialize_adoption_artifacts",
    context: {
      workspace,
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      publication: "create_or_verify_exact",
      tenant: TENANT,
      resource_type: resourceType,
      assertion,
    },
  };
}

function invoke(
  compileRequest: unknown,
  authority: { readonly fake: string; readonly tempRoot: string } | null,
  extraEnvironment: Readonly<Record<string, string>> = {},
): {
  readonly status: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly response: ProcessResponse;
} {
  const env: NodeJS.ProcessEnv = {
    PATH: process.env.PATH,
    ...extraEnvironment,
  };
  if (authority !== null) {
    env.INFRAWRIGHT_TERRAFORM_EXECUTABLE = authority.fake;
    env.INFRAWRIGHT_ZCC_ADOPTION_TEMP_ROOT = authority.tempRoot;
  }
  const result = spawnSync(process.execPath, [PROCESS_BUNDLE], {
    cwd: REPO,
    encoding: "utf8",
    input: JSON.stringify(compileRequest),
    env,
  });
  assert.equal(result.signal, null, result.error?.message);
  assert.doesNotThrow(() => JSON.parse(result.stdout), result.stderr);
  return {
    status: result.status,
    stdout: result.stdout,
    stderr: result.stderr,
    response: JSON.parse(result.stdout) as ProcessResponse,
  };
}

function requireSuccess(
  response: ProcessResponse,
): asserts response is CompileAdoptionArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "compile_adoption_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireCompareSuccess(
  response: ProcessResponse,
): asserts response is CompareAdoptionArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "compare_adoption_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "compile_adoption_artifacts");
  assert.equal(response.status, "error");
  assert.notEqual(response.error, null);
  assert.equal(response.result, null);
}

function requireCompareError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "compare_adoption_artifacts");
  assert.equal(response.status, "error");
  assert.notEqual(response.error, null);
  assert.equal(response.result, null);
}

function requireMaterializeSuccess(
  response: ProcessResponse,
): asserts response is MaterializeAdoptionArtifactsProcessSuccessResponse {
  assert.equal(response.operation, "materialize_adoption_artifacts");
  assert.equal(response.status, "ok");
  assert.equal(response.error, null);
  assert.notEqual(response.result, null);
}

function requireMaterializeError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "materialize_adoption_artifacts");
  assert.equal(response.status, "error");
  assert.notEqual(response.error, null);
  assert.equal(response.result, null);
}

function artifactPaths(
  workspace: string,
  result: CompileAdoptionArtifactsProcessSuccessResponse["result"],
): readonly string[] {
  return [
    path.join(workspace, result.artifacts.tfvars.path),
    path.join(workspace, result.artifacts.imports.path),
    ...(result.artifacts.lookup === null
      ? []
      : [path.join(workspace, result.artifacts.lookup.path)]),
  ];
}

const build = spawnSync(process.execPath, ["scripts/build-node.mjs"], {
  cwd: REPO,
  encoding: "utf8",
});
assert.equal(build.status, 0, build.stderr || build.stdout);

test("public adoption request is closed and host authority is out of band", () => {
  const fixture = createFixture();
  try {
    const valid = request(fixture, "zcc_web_privacy");
    assert.equal(validateProcessRequest(valid), true);
    for (const [location, field] of [
      ["input", "source_path"],
      ["input", "terraform_executable"],
      ["input", "credentials"],
      ["input", "provider_state"],
      ["input", "timeout"],
      ["input", "catalog"],
      ["input", "policy"],
      ["context", "temp_root"],
      ["context", "lock"],
    ] as const) {
      const copy = structuredClone(valid) as unknown as Record<string, Record<string, unknown>>;
      copy[location]![field] = "caller-selected-secret";
      assert.equal(validateProcessRequest(copy), false, `${location}.${field}`);
    }
    const refresh = structuredClone(valid) as unknown as {
      input: { mode: string };
    };
    refresh.input.mode = "refresh";
    assert.equal(validateProcessRequest(refresh), false);

    const callerState = structuredClone(valid) as unknown as {
      input: CompileAdoptionArtifactsProcessRequest["input"] & {
        provider_state?: string;
      };
    };
    callerState.input.provider_state = "caller-selected-secret";
    const rejected = invoke(
      callerState,
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
    );
    assert.equal(rejected.status, 2);
    requireError(rejected.response);
    assert.equal(rejected.response.error.code, "INVALID_REQUEST");
    assert.equal(rejected.stdout.includes("caller-selected-secret"), false);

    const missing = invoke(valid, null, { ZSCALER_CLIENT_SECRET: "credential-secret" });
    assert.equal(missing.status, 1);
    assert.equal(missing.stderr, "");
    requireError(missing.response);
    assert.equal(missing.response.error.code, "ZCC_ADOPTION_HOST_NOT_CONFIGURED");
    assert.equal(missing.stdout.includes("credential-secret"), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("empty adoption is effect-free after host configuration", () => {
  const fixture = createFixture();
  try {
    writeFileSync(
      path.join(fixture.workspace, "pulls", TENANT, "zcc_web_privacy.json"),
      "[]\n",
    );
    const invocation = invoke(
      request(fixture, "zcc_web_privacy"),
      { fake: "/not/a/real/terraform", tempRoot: "/not/a/real/temp-root" },
    );
    assert.equal(invocation.status, 0, invocation.stdout);
    requireSuccess(invocation.response);
    assert.equal(invocation.response.result.artifacts.tfvars.content.includes("{}"), true);
    assert.equal(existsSync(fixture.log), false);
    for (const artifact of artifactPaths(fixture.workspace, invocation.response.result)) {
      assert.equal(existsSync(artifact), false, artifact);
    }
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("all five public results are schema-valid, provider-observed, and write nothing", () => {
  const fixture = createFixture();
  try {
    for (const resourceType of RESOURCES) {
      const invocation = invoke(
        request(fixture, resourceType),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
        {
          ZSCALER_CLIENT_SECRET: "credential-secret",
          TF_CLI_ARGS: "-destroy",
          TF_PLUGIN_CACHE_DIR: "/untrusted/plugin-cache",
        },
      );
      assert.equal(invocation.status, 0, invocation.stdout);
      assert.equal(invocation.stderr, "");
      assert.equal(validateProcessResponse(invocation.response), true);
      requireSuccess(invocation.response);
      assert.equal(validateZccAdoptionArtifactSet(invocation.response.result), true);
      assert.equal(invocation.response.result.resource_type, resourceType);
      assert.equal(invocation.response.result.tenant, TENANT);
      assert.equal(invocation.response.result.mode, "bootstrap");
      const crossPaired = structuredClone(invocation.response) as unknown as {
        operation: string;
      };
      crossPaired.operation = "compile_pull_artifacts";
      assert.equal(validateProcessResponse(crossPaired), false);
      for (const artifact of artifactPaths(fixture.workspace, invocation.response.result)) {
        assert.equal(existsSync(artifact), false, artifact);
      }
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
      assert.equal(invocation.stdout.includes("credential-secret"), false);
    }
    const invocations = readFileSync(fixture.log, "utf8")
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line) as { environment: Record<string, string> });
    for (const child of invocations) {
      assert.equal(child.environment.ZSCALER_CLIENT_SECRET, "credential-secret");
      assert.equal("TF_CLI_ARGS" in child.environment, false);
      assert.equal("TF_PLUGIN_CACHE_DIR" in child.environment, false);
    }
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("provider plan and state joins fail closed with bounded value-free errors", () => {
  const cases: readonly [FakeBehavior, string, number][] = [
    ["bad-plan-envelope", "ZCC_ADOPTION_ORACLE_PLAN_REJECTED", 2],
    ["bad-plan-provider", "ZCC_ADOPTION_ORACLE_PLAN_REJECTED", 2],
    ["bad-state-id", "ZCC_ADOPTION_ORACLE_STATE_REJECTED", 2],
    ["bad-state-provider", "ZCC_ADOPTION_ORACLE_STATE_REJECTED", 2],
    ["missing-state-resource", "ZCC_ADOPTION_ORACLE_STATE_REJECTED", 2],
    ["fail-init", "ZCC_ADOPTION_ORACLE_INIT_FAILED", 1],
  ];
  for (const [behavior, expectedCode, expectedExit] of cases) {
    const fixture = createFixture({ behavior });
    try {
      const invocation = invoke(
        request(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
        { ZSCALER_CLIENT_SECRET: "credential-secret" },
      );
      assert.equal(invocation.status, expectedExit, behavior);
      assert.equal(invocation.stderr, "", behavior);
      requireError(invocation.response);
      assert.equal(invocation.response.error.code, expectedCode, behavior);
      for (const secret of [
        "credential-secret",
        "provider-child-secret",
        "privacy-1",
        fixture.tempRoot,
      ]) {
        assert.equal(invocation.stdout.includes(secret), false, `${behavior}:${secret}`);
      }
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("HCL, same-root bindings, unsupported resources, and prior transitions are refused", () => {
  const hcl = createFixture({ deployment: { overlay: ".", tfvars_format: "hcl", roots: {} } });
  try {
    const result = invoke(
      request(hcl, "zcc_web_privacy"),
      { fake: hcl.fake, tempRoot: hcl.tempRoot },
    );
    requireError(result.response);
    assert.equal(result.response.error.code, "UNSUPPORTED_TFVARS_FORMAT");
    assert.equal(existsSync(hcl.log), false);
  } finally {
    rmSync(hcl.root, { recursive: true, force: true });
  }

  const bound = createFixture({
    deployment: {
      overlay: ".",
      roots: {
        zcc: {
          bind_references: true,
          groups: {
            zcc_pair: ["zcc_forwarding_profile", "zcc_trusted_network"],
          },
        },
      },
    },
  });
  try {
    const result = invoke(
      request(bound, "zcc_forwarding_profile"),
      { fake: bound.fake, tempRoot: bound.tempRoot },
    );
    requireError(result.response);
    assert.equal(result.response.error.code, "UNSUPPORTED_GROUP_BINDINGS");
    assert.equal(existsSync(bound.log), false);
  } finally {
    rmSync(bound.root, { recursive: true, force: true });
  }

  const unsupported = createFixture();
  try {
    const invalid = structuredClone(request(unsupported, "zcc_web_privacy")) as unknown as {
      input: { resource_type: string };
    };
    invalid.input.resource_type = "zcc_zia_posture";
    const result = invoke(invalid, { fake: unsupported.fake, tempRoot: unsupported.tempRoot });
    assert.equal(result.status, 2);
    requireError(result.response);
    assert.equal(result.response.error.code, "INVALID_REQUEST");
    assert.equal(existsSync(unsupported.log), false);
  } finally {
    rmSync(unsupported.root, { recursive: true, force: true });
  }

  for (const [suffix, expectedCode] of [
    ["_imports.tf", "BOOTSTRAP_IMPORTS_EXIST"],
    ["_moves.tf", "BOOTSTRAP_MOVES_EXIST"],
    ["_moves.pending.json", "UNSUPPORTED_PENDING_MOVES"],
  ] as const) {
    const fixture = createFixture();
    try {
      const target = path.join(
        fixture.workspace,
        "imports",
        TENANT,
        `zcc_web_privacy${suffix}`,
      );
      mkdirSync(path.dirname(target), { recursive: true });
      writeFileSync(target, "preexisting-secret");
      const result = invoke(
        request(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      requireError(result.response);
      assert.equal(result.response.error.code, expectedCode, suffix);
      assert.equal(result.stdout.includes("preexisting-secret"), false);
      assert.equal(existsSync(fixture.log), false);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("caller inputs and bootstrap preconditions are rechecked after the oracle", () => {
  const mutations = [
    {
      name: "pull",
      target: (fixture: Fixture) => path.join(
        fixture.workspace,
        "pulls",
        TENANT,
        "zcc_web_privacy.json",
      ),
      code: "RAW_PULL_CHANGED",
      exit: 1,
    },
    {
      name: "deployment",
      target: (fixture: Fixture) => fixture.deploymentPath,
      code: "COMPILE_CONTROL_CHANGED",
      exit: 1,
    },
    {
      name: "catalog",
      target: (fixture: Fixture) => fixture.catalogPath,
      code: "COMPILE_CONTROL_CHANGED",
      exit: 1,
    },
    {
      name: "imports",
      target: (fixture: Fixture) => path.join(
        fixture.workspace,
        "imports",
        TENANT,
        "zcc_web_privacy_imports.tf",
      ),
      code: "BOOTSTRAP_IMPORTS_EXIST",
      exit: 2,
    },
    {
      name: "moves",
      target: (fixture: Fixture) => path.join(
        fixture.workspace,
        "imports",
        TENANT,
        "zcc_web_privacy_moves.tf",
      ),
      code: "BOOTSTRAP_MOVES_EXIST",
      exit: 2,
    },
    {
      name: "pending",
      target: (fixture: Fixture) => path.join(
        fixture.workspace,
        "imports",
        TENANT,
        "zcc_web_privacy_moves.pending.json",
      ),
      code: "BOOTSTRAP_PENDING_MOVES_CHANGED",
      exit: 1,
    },
  ] as const;
  for (const mutation of mutations) {
    const seed = createFixture();
    const target = mutation.target(seed);
    writeFake({
      executable: seed.fake,
      log: seed.log,
      mutation: { path: target, content: "race-secret" },
    });
    try {
      const result = invoke(
        request(seed, "zcc_web_privacy"),
        { fake: seed.fake, tempRoot: seed.tempRoot },
      );
      assert.equal(result.status, mutation.exit, mutation.name);
      requireError(result.response);
      assert.equal(result.response.error.code, mutation.code, mutation.name);
      assert.equal(result.stdout.includes("race-secret"), false);
      assert.deepEqual(readdirSync(seed.tempRoot), []);
      const tfvars = path.join(
        seed.workspace,
        "config",
        TENANT,
        "zcc_web_privacy.auto.tfvars.json",
      );
      assert.equal(existsSync(tfvars), false);
    } finally {
      rmSync(seed.root, { recursive: true, force: true });
    }
  }
});

test("operation timeout stays primary and verified cleanup completes", async (t) => {
  const fixture = createFixture();
  let calls = 0;
  t.mock.method(performance, "now", () => {
    calls += 1;
    return calls === 1 ? 0 : 300_000;
  });
  try {
    await assert.rejects(
      compileZccAdoptionArtifactsOperation({
        workspace: fixture.workspace,
        deploymentPath: fixture.deploymentPath,
        catalogPath: fixture.catalogPath,
        tenant: TENANT,
        resourceType: "zcc_web_privacy",
        hostAuthority: {
          terraformExecutable: fixture.fake,
          tempRoot: fixture.tempRoot,
          environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ADOPTION_ORACLE_TIMEOUT"
        && !JSON.stringify(error).includes("credential-secret")
        && !JSON.stringify(error).includes(fixture.tempRoot),
    );
    assert.deepEqual(readdirSync(fixture.tempRoot), []);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

function cloneWorkspace(
  fixture: Fixture,
  name: string,
  deploymentValue: unknown,
): string {
  const workspace = path.join(fixture.root, name);
  mkdirSync(path.join(workspace, "pulls", TENANT), { recursive: true });
  writeFileSync(
    path.join(workspace, "deployment.json"),
    `${JSON.stringify(deploymentValue)}\n`,
  );
  copyFileSync(ROOT_CATALOG, path.join(workspace, "catalog.json"));
  for (const resourceType of RESOURCES) {
    writeFileSync(
      path.join(workspace, "pulls", TENANT, `${resourceType}.json`),
      EVIDENCE[resourceType].rawText,
    );
  }
  return workspace;
}

function pythonAdopt(
  workspace: string,
  fake: string,
  resourceType: ZccPullResourceType,
): void {
  const result = spawnSync(
    process.env.PYTHON ?? "python3",
    [
      "-m",
      "engine.adopt",
      resourceType,
      path.join(workspace, "pulls", TENANT, `${resourceType}.json`),
      TENANT,
    ],
    {
      cwd: workspace,
      encoding: "utf8",
      env: {
        PATH: process.env.PATH,
        PYTHONPATH: REPO,
        INFRAWRIGHT_PACKS: path.join(REPO, "packs"),
        INFRAWRIGHT_DEPLOYMENT: path.join(workspace, "deployment.json"),
        TF: fake,
      },
    },
  );
  assert.equal(result.status, 0, result.stderr || result.stdout);
}

function readPythonArtifacts(
  workspace: string,
  candidate: CompileAdoptionArtifactsProcessSuccessResponse["result"],
): Readonly<Record<"tfvars" | "imports" | "lookup", string | null>> {
  return {
    tfvars: readFileSync(path.join(workspace, candidate.artifacts.tfvars.path), "utf8"),
    imports: readFileSync(path.join(workspace, candidate.artifacts.imports.path), "utf8"),
    lookup: candidate.artifacts.lookup === null
      ? null
      : readFileSync(path.join(workspace, candidate.artifacts.lookup.path), "utf8"),
  };
}

function singletonReferencePath(
  fixture: Fixture,
  resourceType: ZccPullResourceType,
  role: "tfvars" | "imports" | "lookup",
): string {
  if (role === "imports") {
    return path.join(
      fixture.workspace,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    );
  }
  const suffix = role === "tfvars" ? ".auto.tfvars.json" : ".lookup.json";
  return path.join(
    fixture.workspace,
    "config",
    TENANT,
    `${resourceType}${suffix}`,
  );
}

function replaceParentWithExactHardlinks(
  parent: string,
  retainedNames: readonly string[],
): void {
  const original = `${parent}.bound-original`;
  renameSync(parent, original);
  mkdirSync(parent, { mode: 0o700 });
  for (const name of retainedNames) {
    const source = path.join(original, name);
    const target = path.join(parent, name);
    const before = statSync(source, { bigint: true });
    linkSync(source, target);
    const after = statSync(target, { bigint: true });
    assert.equal(after.dev, before.dev, name);
    assert.equal(after.ino, before.ino, name);
  }
}

test("bundled all-five executor matches Python before and after in grouped and singleton roots", () => {
  for (const grouped of [false, true]) {
    const fixture = createFixture();
    const deploymentValue = deployment(grouped);
    try {
      for (const resourceType of RESOURCES) {
        const pythonBefore = cloneWorkspace(
          fixture,
          `python-before-${grouped}-${resourceType}`,
          deploymentValue,
        );
        const nodeWorkspace = cloneWorkspace(
          fixture,
          `node-${grouped}-${resourceType}`,
          deploymentValue,
        );
        const pythonAfter = cloneWorkspace(
          fixture,
          `python-after-${grouped}-${resourceType}`,
          deploymentValue,
        );
        pythonAdopt(pythonBefore, fixture.fake, resourceType);

        const nodeRequest: CompileAdoptionArtifactsProcessRequest = {
          ...request(fixture, resourceType),
          context: {
            workspace: nodeWorkspace,
            deployment: "deployment.json",
            root_catalog: "catalog.json",
          },
        };
        const invocation = invoke(
          nodeRequest,
          { fake: fixture.fake, tempRoot: fixture.tempRoot },
        );
        assert.equal(invocation.status, 0, invocation.stdout);
        requireSuccess(invocation.response);
        pythonAdopt(pythonAfter, fixture.fake, resourceType);

        const before = readPythonArtifacts(pythonBefore, invocation.response.result);
        const after = readPythonArtifacts(pythonAfter, invocation.response.result);
        assert.deepEqual(after, before, `${grouped}:${resourceType}:python-stability`);
        assert.equal(
          invocation.response.result.artifacts.tfvars.content,
          before.tfvars,
          `${grouped}:${resourceType}:tfvars`,
        );
        assert.equal(
          invocation.response.result.artifacts.imports.content,
          before.imports,
          `${grouped}:${resourceType}:imports`,
        );
        assert.equal(
          invocation.response.result.artifacts.lookup?.content ?? null,
          before.lookup,
          `${grouped}:${resourceType}:lookup`,
        );
        if (resourceType === "zcc_device_cleanup") {
          const exactProviderInteger = "900719925474099312345678902";
          for (const [side, content] of [
            ["node", invocation.response.result.artifacts.tfvars.content],
            ["python-before", before.tfvars],
            ["python-after", after.tfvars],
          ] as const) {
            assert.equal(
              content?.includes(
                `"auto_purge_days": ${exactProviderInteger}`,
              ),
              true,
              `${grouped}:${side}:exact-provider-integer`,
            );
            assert.doesNotMatch(
              content ?? "",
              /"auto_purge_days": [^,\n]*[eE][+-]?[0-9]+/,
              `${grouped}:${side}:rounded-provider-integer`,
            );
          }
        }
        for (const descriptor of [
          invocation.response.result.artifacts.tfvars,
          invocation.response.result.artifacts.imports,
          ...(invocation.response.result.artifacts.lookup === null
            ? []
            : [invocation.response.result.artifacts.lookup]),
        ]) {
          assert.equal(
            descriptor.sha256,
            createHash("sha256").update(descriptor.content).digest("hex"),
          );
          assert.equal(descriptor.size_bytes, Buffer.byteLength(descriptor.content));
          assert.equal(existsSync(path.join(nodeWorkspace, descriptor.path)), false);
        }
      }
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("public adoption comparer request is closed and host authority stays out of band", () => {
  const fixture = createFixture();
  try {
    const valid = compareRequest(fixture, "zcc_web_privacy");
    assert.equal(validateProcessRequest(valid), true);
    for (const [location, field] of [
      ["input", "candidate"],
      ["input", "provider_state"],
      ["input", "source_path"],
      ["input", "terraform_executable"],
      ["input", "credentials"],
      ["input", "timeout"],
      ["input", "catalog_sha256"],
      ["context", "temp_root"],
      ["context", "output_root"],
    ] as const) {
      const copy = structuredClone(valid) as unknown as Record<
        string,
        Record<string, unknown>
      >;
      copy[location]![field] = "caller-selected-secret";
      assert.equal(validateProcessRequest(copy), false, `${location}.${field}`);
    }
    const missing = invoke(valid, null, {
      ZSCALER_CLIENT_SECRET: "credential-secret",
    });
    assert.equal(missing.status, 1);
    requireCompareError(missing.response);
    assert.equal(missing.response.error.code, "ZCC_ADOPTION_HOST_NOT_CONFIGURED");
    assert.equal(missing.stdout.includes("credential-secret"), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("empty materialized adoption comparison is effect-free and provider-free", () => {
  const fixture = createFixture();
  try {
    const pull = path.join(
      fixture.workspace,
      "pulls",
      TENANT,
      "zcc_web_privacy.json",
    );
    writeFileSync(pull, "[]\n");
    pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
    rmSync(fixture.log, { force: true });
    const invocation = invoke(
      compareRequest(fixture, "zcc_web_privacy"),
      { fake: "/not/a/real/terraform", tempRoot: "/not/a/real/temp-root" },
    );
    assert.equal(invocation.status, 0, invocation.stdout);
    requireCompareSuccess(invocation.response);
    assert.equal(invocation.response.result.status, "ready");
    assert.equal(existsSync(fixture.log), false);
    assert.equal(invocation.stdout.includes("content"), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("empty adoption materialization is provider-free but still publishes asserted bytes", () => {
  const fixture = createFixture();
  try {
    const pull = path.join(
      fixture.workspace,
      "pulls",
      TENANT,
      "zcc_web_privacy.json",
    );
    writeFileSync(pull, "[]\n");
    pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
    const comparison = invoke(
      compareRequest(fixture, "zcc_web_privacy"),
      { fake: "/not/a/real/terraform", tempRoot: "/not/a/real/temp-root" },
    );
    assert.equal(comparison.status, 0, comparison.stdout);
    requireCompareSuccess(comparison.response);
    const target = cloneWorkspace(fixture, "empty-materialization-target", deployment());
    writeFileSync(
      path.join(target, "pulls", TENANT, "zcc_web_privacy.json"),
      "[]\n",
    );
    rmSync(fixture.log, { force: true });
    const publication = invoke(
      materializeRequest(
        fixture,
        target,
        "zcc_web_privacy",
        comparison.response.result,
      ),
      { fake: "/not/a/real/terraform", tempRoot: "/not/a/real/temp-root" },
      { INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: target },
    );
    assert.equal(publication.status, 0, publication.stdout);
    requireMaterializeSuccess(publication.response);
    assert.deepEqual(
      publication.response.result.publication.created,
      ["imports", "tfvars"],
    );
    assert.equal(existsSync(fixture.log), false);
    assert.equal(
      existsSync(path.join(
        target,
        "config",
        TENANT,
        "zcc_web_privacy.auto.tfvars.json",
      )),
      true,
    );
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("bundled comparer proves all five Python references in singleton and grouped roots", () => {
  for (const grouped of [false, true]) {
    const fixture = createFixture({ deployment: deployment(grouped) });
    try {
      for (const resourceType of RESOURCES) {
        pythonAdopt(fixture.workspace, fixture.fake, resourceType);
      }
      for (const resourceType of RESOURCES) {
        const artifactRoot = grouped
          ? path.join(fixture.workspace, "stack")
          : fixture.workspace;
        const materializedPaths = [
          path.join(
            artifactRoot,
            "config",
            TENANT,
            `${resourceType}.auto.tfvars.json`,
          ),
          path.join(
            artifactRoot,
            "imports",
            TENANT,
            `${resourceType}_imports.tf`,
          ),
          ...(resourceType === "zcc_trusted_network"
            ? [path.join(
                artifactRoot,
                "config",
                TENANT,
                `${resourceType}.lookup.json`,
              )]
            : []),
        ];
        const before = materializedPaths.map((artifactPath) => {
          const metadata = statSync(artifactPath, { bigint: true });
          return {
            content: readFileSync(artifactPath),
            dev: metadata.dev,
            ino: metadata.ino,
            mtimeNs: metadata.mtimeNs,
          };
        });
        const invocation = invoke(
          compareRequest(fixture, resourceType),
          { fake: fixture.fake, tempRoot: fixture.tempRoot },
          { ZSCALER_CLIENT_SECRET: "credential-secret" },
        );
        assert.equal(invocation.status, 0, `${grouped}:${resourceType}:${invocation.stdout}`);
        assert.equal(invocation.stderr, "");
        assert.equal(validateProcessResponse(invocation.response), true);
        requireCompareSuccess(invocation.response);
        assert.equal(
          validateZccAdoptionArtifactParity(invocation.response.result),
          true,
          JSON.stringify(validateZccAdoptionArtifactParity.errors),
        );
        assert.equal(invocation.response.result.status, "ready");
        assert.equal(invocation.response.result.parity.status, "equal");
        assert.equal(
          invocation.response.result.parity.equal,
          resourceType === "zcc_trusted_network" ? 3 : 2,
        );
        assert.equal(invocation.response.result.parity.different, 0);
        assert.equal(
          invocation.response.result.parity.artifacts.lookup.status,
          resourceType === "zcc_trusted_network" ? "equal" : "not_applicable",
        );
        const crossPairedPull = structuredClone(invocation.response) as unknown as {
          operation: string;
        };
        crossPairedPull.operation = "compare_pull_artifacts";
        assert.equal(validateProcessResponse(crossPairedPull), false);
        const crossPairedCompile = structuredClone(invocation.response) as unknown as {
          operation: string;
        };
        crossPairedCompile.operation = "compile_adoption_artifacts";
        assert.equal(validateProcessResponse(crossPairedCompile), false);
        assert.equal(invocation.stdout.includes("content"), false);
        assert.equal(invocation.stdout.includes("credential-secret"), false);
        assert.equal(invocation.stdout.includes("900719925474099312345678902"), false);
        assert.deepEqual(readdirSync(fixture.tempRoot), []);
        for (const [index, artifactPath] of materializedPaths.entries()) {
          const metadata = statSync(artifactPath, { bigint: true });
          assert.deepEqual(readFileSync(artifactPath), before[index]?.content);
          assert.equal(metadata.dev, before[index]?.dev);
          assert.equal(metadata.ino, before[index]?.ino);
          assert.equal(metadata.mtimeNs, before[index]?.mtimeNs);
        }
        for (const entry of [
          invocation.response.result.parity.artifacts.tfvars,
          invocation.response.result.parity.artifacts.imports,
          ...(invocation.response.result.parity.artifacts.lookup.status
              === "not_applicable"
            ? []
            : [invocation.response.result.parity.artifacts.lookup]),
        ]) {
          assert.notEqual(entry.candidate, null);
          assert.notEqual(entry.reference, null);
          assert.equal(entry.candidate?.path, entry.reference?.path);
          assert.equal(entry.candidate?.sha256, entry.reference?.sha256);
          assert.equal(entry.candidate?.size_bytes, entry.reference?.size_bytes);
        }
      }
      const exactIntegerReference = readFileSync(
        path.join(
          fixture.workspace,
          grouped ? "stack/config" : "config",
          TENANT,
          "zcc_device_cleanup.auto.tfvars.json",
        ),
        "utf8",
      );
      assert.match(
        exactIntegerReference,
        /"auto_purge_days": 900719925474099312345678902/,
      );
      assert.doesNotMatch(
        exactIntegerReference,
        /"auto_purge_days": [^,\n]*[eE][+-]?[0-9]+/,
      );
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("bundled materializer reproduces Python before and after for all five singleton and grouped roots", () => {
  for (const grouped of [false, true]) {
    const fixture = createFixture();
    const deploymentValue = deployment(grouped);
    try {
      const pythonBefore = cloneWorkspace(
        fixture,
        `materialize-python-before-${grouped}`,
        deploymentValue,
      );
      const nodeWorkspace = cloneWorkspace(
        fixture,
        `materialize-node-${grouped}`,
        deploymentValue,
      );
      const pythonAfter = cloneWorkspace(
        fixture,
        `materialize-python-after-${grouped}`,
        deploymentValue,
      );
      const nodeOutputRoot = grouped
        ? path.join(nodeWorkspace, "stack")
        : nodeWorkspace;
      mkdirSync(nodeOutputRoot, { recursive: true });
      const oracleFixture: Fixture = {
        ...fixture,
        workspace: pythonBefore,
        deploymentPath: path.join(pythonBefore, "deployment.json"),
        catalogPath: path.join(pythonBefore, "catalog.json"),
      };
      const assertions = new Map<
        ZccPullResourceType,
        CompareAdoptionArtifactsProcessSuccessResponse["result"]
      >();
      const expected = new Map<ZccPullResourceType, Map<string, Buffer>>();

      for (const resourceType of RESOURCES) {
        pythonAdopt(pythonBefore, fixture.fake, resourceType);
      }
      for (const resourceType of RESOURCES) {
        const comparison = invoke(
          compareRequest(oracleFixture, resourceType),
          { fake: fixture.fake, tempRoot: fixture.tempRoot },
          { ZSCALER_CLIENT_SECRET: "credential-secret" },
        );
        assert.equal(comparison.status, 0, comparison.stdout);
        requireCompareSuccess(comparison.response);
        assert.equal(comparison.response.result.status, "ready");
        assertions.set(resourceType, comparison.response.result);
        const bytes = new Map<string, Buffer>();
        for (const [name, entry] of Object.entries(
          comparison.response.result.parity.artifacts,
        )) {
          if (entry.status !== "not_applicable") {
            bytes.set(
              name,
              readFileSync(path.join(pythonBefore, entry.candidate.path)),
            );
          }
        }
        expected.set(resourceType, bytes);
      }

      for (const resourceType of RESOURCES) {
        const assertion = assertions.get(resourceType);
        assert.notEqual(assertion, undefined);
        const request = materializeRequest(
          fixture,
          nodeWorkspace,
          resourceType,
          assertion as CompareAdoptionArtifactsProcessSuccessResponse["result"],
        );
        assert.equal(
          validateProcessRequest(request),
          true,
          JSON.stringify(validateProcessRequest.errors),
        );
        const logBefore = existsSync(fixture.log)
          ? readFileSync(fixture.log, "utf8").trim().split("\n").filter(Boolean).length
          : 0;
        const first = invoke(
          request,
          { fake: fixture.fake, tempRoot: fixture.tempRoot },
          {
            INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: nodeOutputRoot,
            ZSCALER_CLIENT_SECRET: "credential-secret",
          },
        );
        assert.equal(first.status, 0, `${grouped}:${resourceType}:${first.stdout}`);
        assert.equal(first.stderr, "");
        assert.equal(validateProcessResponse(first.response), true);
        requireMaterializeSuccess(first.response);
        assert.equal(
          validateZccAdoptionArtifactMaterialization(first.response.result),
          true,
          JSON.stringify(validateZccAdoptionArtifactMaterialization.errors),
        );
        assert.deepEqual(first.response.result.verification, assertion);
        const applicable = resourceType === "zcc_trusted_network"
          ? ["imports", "lookup", "tfvars"]
          : ["imports", "tfvars"];
        assert.deepEqual(first.response.result.publication.created, applicable);
        assert.deepEqual(first.response.result.publication.reused, []);
        const logAfter = readFileSync(fixture.log, "utf8")
          .trim()
          .split("\n")
          .filter(Boolean)
          .length;
        assert.equal(logAfter > logBefore, true, `${grouped}:${resourceType}:fresh-oracle`);
        assert.equal(first.stdout.includes("content"), false);
        assert.equal(first.stdout.includes("credential-secret"), false);
        assert.equal(first.stdout.includes("900719925474099312345678902"), false);
        assert.equal(first.stdout.includes(fixture.tempRoot), false);
        assert.equal(first.stdout.includes(nodeOutputRoot), false);
        const importId = Object.keys(fixture.evidence[resourceType].states)[0];
        if (importId !== undefined) {
          assert.equal(first.stdout.includes(importId), false);
        }

        const expectedBytes = expected.get(resourceType);
        assert.notEqual(expectedBytes, undefined);
        for (const [name, entry] of Object.entries(
          first.response.result.verification.parity.artifacts,
        )) {
          if (entry.status === "not_applicable") {
            continue;
          }
          const target = path.join(nodeWorkspace, entry.candidate.path);
          assert.deepEqual(
            readFileSync(target),
            expectedBytes?.get(name),
            `${grouped}:${resourceType}:${name}`,
          );
        }

        const retry = invoke(
          request,
          { fake: fixture.fake, tempRoot: fixture.tempRoot },
          {
            INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: nodeOutputRoot,
            ZSCALER_CLIENT_SECRET: "credential-secret",
          },
        );
        assert.equal(retry.status, 0, retry.stdout);
        requireMaterializeSuccess(retry.response);
        assert.deepEqual(retry.response.result.publication.created, []);
        assert.deepEqual(retry.response.result.publication.reused, applicable);
      }

      for (const resourceType of RESOURCES) {
        pythonAdopt(pythonAfter, fixture.fake, resourceType);
        const assertion = assertions.get(resourceType);
        assert.notEqual(assertion, undefined);
        for (const [name, entry] of Object.entries(
          assertion?.parity.artifacts ?? {},
        )) {
          if (entry.status === "not_applicable") {
            continue;
          }
          const after = readFileSync(path.join(pythonAfter, entry.candidate.path));
          assert.deepEqual(
            after,
            expected.get(resourceType)?.get(name),
            `${grouped}:${resourceType}:${name}:python-after`,
          );
        }
      }
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
      assert.equal(
        existsSync(path.join(nodeOutputRoot, ".infrawright.publisher.lock")),
        false,
      );
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("public adoption materializer is closed, assertion-bound, and fails before final writes", () => {
  const fixture = createFixture();
  try {
    const oracle = cloneWorkspace(fixture, "materialize-contract-oracle", deployment());
    const target = cloneWorkspace(fixture, "materialize-contract-target", deployment());
    pythonAdopt(oracle, fixture.fake, "zcc_web_privacy");
    const oracleFixture: Fixture = {
      ...fixture,
      workspace: oracle,
      deploymentPath: path.join(oracle, "deployment.json"),
      catalogPath: path.join(oracle, "catalog.json"),
    };
    const comparison = invoke(
      compareRequest(oracleFixture, "zcc_web_privacy"),
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
      { ZSCALER_CLIENT_SECRET: "credential-secret" },
    );
    assert.equal(comparison.status, 0, comparison.stdout);
    requireCompareSuccess(comparison.response);
    const valid = materializeRequest(
      fixture,
      target,
      "zcc_web_privacy",
      comparison.response.result,
    );
    assert.equal(validateProcessRequest(valid), true);
    for (const [location, field] of [
      ["input", "candidate"],
      ["input", "provider_state"],
      ["input", "source_path"],
      ["input", "terraform_executable"],
      ["input", "credentials"],
      ["input", "timeout"],
      ["input", "output_root"],
      ["context", "temp_root"],
      ["context", "output_root"],
    ] as const) {
      const copy = structuredClone(valid) as unknown as Record<
        string,
        Record<string, unknown>
      >;
      copy[location]![field] = "caller-selected-secret";
      assert.equal(validateProcessRequest(copy), false, `${location}.${field}`);
    }

    const missingOracle = invoke(valid, null, {
      INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: target,
      ZSCALER_CLIENT_SECRET: "credential-secret",
    });
    assert.equal(missingOracle.status, 1);
    requireMaterializeError(missingOracle.response);
    assert.equal(missingOracle.response.error.code, "ZCC_ADOPTION_HOST_NOT_CONFIGURED");

    const missingOutput = invoke(
      valid,
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
      { ZSCALER_CLIENT_SECRET: "credential-secret" },
    );
    assert.equal(missingOutput.status, 1);
    requireMaterializeError(missingOutput.response);
    assert.equal(
      missingOutput.response.error.code,
      "MATERIALIZE_OUTPUT_ROOT_NOT_CONFIGURED",
    );

    const wrongResource = structuredClone(valid) as any;
    wrongResource.input.resource_type = "zcc_device_cleanup";
    const rejectedResource = invoke(
      wrongResource,
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
      {
        INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: target,
        ZSCALER_CLIENT_SECRET: "credential-secret",
      },
    );
    assert.equal(rejectedResource.status, 2);
    requireMaterializeError(rejectedResource.response);
    assert.equal(rejectedResource.response.error.code, "INVALID_REQUEST");

    const nonReady = structuredClone(valid) as any;
    const tfvars = nonReady.input.assertion.parity.artifacts.tfvars;
    tfvars.reference.sha256 = "0".repeat(64);
    tfvars.status = "different";
    nonReady.input.assertion.parity.equal -= 1;
    nonReady.input.assertion.parity.different += 1;
    nonReady.input.assertion.parity.status = "different";
    nonReady.input.assertion.status = "review_required";
    const rejectedReview = invoke(
      nonReady,
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
      {
        INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: target,
        ZSCALER_CLIENT_SECRET: "credential-secret",
      },
    );
    assert.equal(rejectedReview.status, 2);
    requireMaterializeError(rejectedReview.response);
    assert.equal(rejectedReview.response.error.code, "INVALID_REQUEST");

    writeFileSync(
      path.join(target, "pulls", TENANT, "zcc_web_privacy.json"),
      `${fixture.evidence.zcc_web_privacy.rawText.trim()} \n`,
    );
    const mismatch = invoke(
      valid,
      { fake: fixture.fake, tempRoot: fixture.tempRoot },
      {
        INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: target,
        ZSCALER_CLIENT_SECRET: "credential-secret",
      },
    );
    assert.equal(mismatch.status, 2, mismatch.stdout);
    requireMaterializeError(mismatch.response);
    assert.equal(
      mismatch.response.error.code,
      "MATERIALIZATION_ASSERTION_MISMATCH",
    );
    assert.equal(existsSync(path.join(target, "config")), false);
    assert.equal(existsSync(path.join(target, "imports")), false);
    assert.equal(mismatch.stdout.includes("credential-secret"), false);
    assert.equal(mismatch.stdout.includes(fixture.tempRoot), false);
    assert.equal(mismatch.stdout.includes(target), false);
    assert.equal(mismatch.stdout.includes("content"), false);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("public adoption materializer enforces guard, exact authority, and no replacement", () => {
  for (const kind of ["busy", "ancestor", "foreign", "pending"] as const) {
    const fixture = createFixture({ deployment: deployment(true) });
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_trusted_network");
      const comparison = invoke(
        compareRequest(fixture, "zcc_trusted_network"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
        { ZSCALER_CLIENT_SECRET: "credential-secret" },
      );
      assert.equal(comparison.status, 0, comparison.stdout);
      requireCompareSuccess(comparison.response);

      const target = cloneWorkspace(fixture, `materialize-${kind}-target`, deployment(true));
      const outputRoot = path.join(target, "stack");
      mkdirSync(outputRoot, { recursive: true });
      const request = materializeRequest(
        fixture,
        target,
        "zcc_trusted_network",
        comparison.response.result,
      );
      const imports = path.join(
        outputRoot,
        "imports",
        TENANT,
        "zcc_trusted_network_imports.tf",
      );
      if (kind === "busy") {
        writeFileSync(path.join(outputRoot, ".infrawright.publisher.lock"), "stale\n");
      } else if (kind === "foreign") {
        mkdirSync(path.dirname(imports), { recursive: true });
        writeFileSync(imports, "foreign artifact bytes\n");
      } else if (kind === "pending") {
        const pending = imports.replace("_imports.tf", "_moves.pending.json");
        mkdirSync(path.dirname(pending), { recursive: true });
        writeFileSync(pending, "{}\n");
      }
      const providerCallsBefore = readFileSync(fixture.log, "utf8")
        .trim()
        .split("\n")
        .filter(Boolean)
        .length;
      const invocation = invoke(
        request,
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
        {
          INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT: kind === "ancestor"
            ? target
            : outputRoot,
          ZSCALER_CLIENT_SECRET: "credential-secret",
        },
      );
      assert.equal(invocation.status, kind === "pending" ? 2 : 1, invocation.stdout);
      requireMaterializeError(invocation.response);
      assert.equal(
        invocation.response.error.code,
        kind === "busy"
          ? "OUTPUT_ROOT_BUSY"
          : kind === "ancestor"
            ? "OUTPUT_ROOT_NOT_ARTIFACT_AUTHORITY"
            : kind === "foreign"
              ? "MATERIALIZATION_TARGET_MISMATCH"
              : "UNSUPPORTED_MATERIALIZATION_RESIDUE",
      );
      if (kind === "busy") {
        assert.equal(invocation.response.error.retryable, true);
        const providerCallsAfter = readFileSync(fixture.log, "utf8")
          .trim()
          .split("\n")
          .filter(Boolean)
          .length;
        assert.equal(providerCallsAfter, providerCallsBefore);
        assert.equal(
          readFileSync(path.join(outputRoot, ".infrawright.publisher.lock"), "utf8"),
          "stale\n",
        );
      }
      if (kind === "foreign") {
        assert.equal(readFileSync(imports, "utf8"), "foreign artifact bytes\n");
      }
      assert.equal(
        existsSync(path.join(
          outputRoot,
          "config",
          TENANT,
          "zcc_trusted_network.auto.tfvars.json",
        )),
        false,
      );
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("adoption publication rechecks candidate-only source and controls at write boundaries", async () => {
  for (const phase of ["source", "control", "postpublish"] as const) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
      const comparison = invoke(
        compareRequest(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
        { ZSCALER_CLIENT_SECRET: "credential-secret" },
      );
      assert.equal(comparison.status, 0, comparison.stdout);
      requireCompareSuccess(comparison.response);
      const target = cloneWorkspace(
        fixture,
        `materialize-recheck-${phase}`,
        deployment(),
      );
      const pull = path.join(target, "pulls", TENANT, "zcc_web_privacy.json");
      const deploymentPath = path.join(target, "deployment.json");
      const mutate = (): void => {
        if (phase === "control") {
          writeFileSync(deploymentPath, `${JSON.stringify({ overlay: ".", roots: {}, note: "changed" })}\n`);
        } else {
          writeFileSync(pull, `${fixture.evidence.zcc_web_privacy.rawText.trim()} \n`);
        }
      };
      try {
        await materializeZccAdoptionArtifactsOperation({
          workspace: target,
          deploymentPath,
          catalogPath: path.join(target, "catalog.json"),
          tenant: TENANT,
          resourceType: "zcc_web_privacy",
          assertion: comparison.response.result,
          hostAuthority: {
            terraformExecutable: fixture.fake,
            tempRoot: fixture.tempRoot,
            environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
          },
          outputRoot: target,
          materializationHooks: phase === "postpublish"
            ? { beforePostpublishRecheck: mutate }
            : { afterStaged: mutate },
        });
        assert.fail(`expected ${phase} recheck failure`);
      } catch (error: unknown) {
        assert.equal(error instanceof ProcessFailure, true);
        assert.equal(
          (error as ProcessFailure).code,
          phase === "postpublish"
            ? "MATERIALIZATION_INDETERMINATE"
            : phase === "control"
              ? "COMPILE_CONTROL_CHANGED"
              : "RAW_PULL_CHANGED",
        );
        if (phase === "postpublish") {
          assert.equal((error as ProcessFailure).retryable, true);
        }
      }
      const tfvars = path.join(
        target,
        "config",
        TENANT,
        "zcc_web_privacy.auto.tfvars.json",
      );
      const imports = path.join(
        target,
        "imports",
        TENANT,
        "zcc_web_privacy_imports.tf",
      );
      assert.equal(existsSync(tfvars), phase === "postpublish");
      assert.equal(existsSync(imports), phase === "postpublish");
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("missing and mismatched materialized roles return content-free review status", () => {
  const cases = [
    ["zcc_web_privacy", "tfvars", "missing"],
    ["zcc_web_privacy", "tfvars", "mismatch"],
    ["zcc_web_privacy", "imports", "missing"],
    ["zcc_web_privacy", "imports", "mismatch"],
    ["zcc_trusted_network", "lookup", "missing"],
    ["zcc_trusted_network", "lookup", "mismatch"],
  ] as const satisfies readonly [
    ZccPullResourceType,
    "tfvars" | "imports" | "lookup",
    "missing" | "mismatch",
  ][];
  for (const [resourceType, role, state] of cases) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, resourceType);
      const target = singletonReferencePath(fixture, resourceType, role);
      if (state === "missing") {
        rmSync(target);
      } else {
        writeFileSync(target, "materialized-reference-secret\n");
      }
      const invocation = invoke(
        compareRequest(fixture, resourceType),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      assert.equal(invocation.status, 3, `${resourceType}:${role}:${state}`);
      requireCompareSuccess(invocation.response);
      assert.equal(invocation.response.result.status, "review_required");
      assert.equal(invocation.response.result.parity.status, "different");
      const entry = invocation.response.result.parity.artifacts[role];
      assert.equal(entry.status, "different");
      assert.notEqual(entry.reference, null);
      assert.equal(
        entry.reference?.sha256,
        state === "missing" ? null : createHash("sha256")
          .update("materialized-reference-secret\n")
          .digest("hex"),
      );
      assert.equal(invocation.stdout.includes("materialized-reference-secret"), false);
      assert.equal(invocation.stdout.includes("content"), false);
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("lookup applicability and unsupported bootstrap-adjacent artifacts fail closed", () => {
  const stale = createFixture();
  try {
    pythonAdopt(stale.workspace, stale.fake, "zcc_web_privacy");
    const staleLookup = singletonReferencePath(
      stale,
      "zcc_web_privacy",
      "lookup",
    );
    writeFileSync(staleLookup, "stale-lookup-secret\n");
    const invocation = invoke(
      compareRequest(stale, "zcc_web_privacy"),
      { fake: stale.fake, tempRoot: stale.tempRoot },
    );
    assert.equal(invocation.status, 2);
    requireCompareError(invocation.response);
    assert.equal(invocation.response.error.code, "UNSUPPORTED_COMPARE_LOOKUP_ARTIFACT");
    assert.equal(invocation.stdout.includes("stale-lookup-secret"), false);
  } finally {
    rmSync(stale.root, { recursive: true, force: true });
  }

  for (const [suffix, expectedCode] of [
    ["_moves.tf", "UNSUPPORTED_COMPARE_MOVES"],
    ["_moves.pending.json", "UNSUPPORTED_PENDING_MOVES"],
  ] as const) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
      const adjacent = path.join(
        fixture.workspace,
        "imports",
        TENANT,
        `zcc_web_privacy${suffix}`,
      );
      writeFileSync(adjacent, "unsupported-adjacent-secret\n");
      const invocation = invoke(
        compareRequest(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      requireCompareError(invocation.response);
      assert.equal(invocation.response.error.code, expectedCode);
      assert.equal(invocation.stdout.includes("unsupported-adjacent-secret"), false);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }

  for (const [relativePath, expectedCode] of [
    [
      `config/${TENANT}/zcc_web_privacy.auto.tfvars`,
      "UNSUPPORTED_COMPARE_HCL_ARTIFACT",
    ],
    [
      `config/${TENANT}/zcc_web_privacy.generated.expressions.json`,
      "UNSUPPORTED_COMPARE_GENERATED_BINDINGS",
    ],
  ] as const) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
      const adjacent = path.join(fixture.workspace, relativePath);
      writeFileSync(adjacent, "unsupported-adjacent-secret\n");
      const invocation = invoke(
        compareRequest(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      requireCompareError(invocation.response);
      assert.equal(invocation.response.error.code, expectedCode);
      assert.equal(invocation.stdout.includes("unsupported-adjacent-secret"), false);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }

  const hcl = createFixture({
    deployment: { overlay: ".", tfvars_format: "hcl", roots: {} },
  });
  try {
    const invocation = invoke(
      compareRequest(hcl, "zcc_web_privacy"),
      { fake: hcl.fake, tempRoot: hcl.tempRoot },
    );
    requireCompareError(invocation.response);
    assert.equal(invocation.response.error.code, "UNSUPPORTED_TFVARS_FORMAT");
    assert.equal(existsSync(hcl.log), false);
  } finally {
    rmSync(hcl.root, { recursive: true, force: true });
  }

  const bound = createFixture({
    deployment: {
      overlay: ".",
      roots: {
        zcc: {
          strategy: "explicit",
          bind_references: true,
          groups: {
            zcc_bootstrap: [
              "zcc_forwarding_profile",
              "zcc_trusted_network",
            ],
          },
        },
      },
    },
  });
  try {
    const invocation = invoke(
      compareRequest(bound, "zcc_forwarding_profile"),
      { fake: bound.fake, tempRoot: bound.tempRoot },
    );
    requireCompareError(invocation.response);
    assert.equal(invocation.response.error.code, "UNSUPPORTED_GROUP_BINDINGS");
    assert.equal(existsSync(bound.log), false);
  } finally {
    rmSync(bound.root, { recursive: true, force: true });
  }
});

test("comparison rechecks source, controls, and materialized references after oracle cleanup", () => {
  const cases = [
    {
      name: "pull",
      target: (fixture: Fixture) => path.join(
        fixture.workspace,
        "pulls",
        TENANT,
        "zcc_web_privacy.json",
      ),
      code: "RAW_PULL_CHANGED",
    },
    {
      name: "deployment",
      target: (fixture: Fixture) => fixture.deploymentPath,
      code: "COMPILE_CONTROL_CHANGED",
    },
    {
      name: "catalog",
      target: (fixture: Fixture) => fixture.catalogPath,
      code: "COMPILE_CONTROL_CHANGED",
    },
    {
      name: "tfvars",
      target: (fixture: Fixture) => singletonReferencePath(
        fixture,
        "zcc_web_privacy",
        "tfvars",
      ),
      code: "COMPARE_ARTIFACT_CHANGED",
    },
    {
      name: "imports",
      target: (fixture: Fixture) => singletonReferencePath(
        fixture,
        "zcc_web_privacy",
        "imports",
      ),
      code: "COMPARE_ARTIFACT_CHANGED",
    },
  ] as const;
  for (const mutation of cases) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
      writeFake({
        executable: fixture.fake,
        log: fixture.log,
        mutation: {
          path: mutation.target(fixture),
          content: "comparison-race-secret",
        },
      });
      const invocation = invoke(
        compareRequest(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      assert.equal(invocation.status, 1, mutation.name);
      requireCompareError(invocation.response);
      assert.equal(invocation.response.error.code, mutation.code, mutation.name);
      assert.equal(invocation.stdout.includes("comparison-race-secret"), false);
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }

  const lookup = createFixture();
  try {
    pythonAdopt(lookup.workspace, lookup.fake, "zcc_trusted_network");
    writeFake({
      executable: lookup.fake,
      log: lookup.log,
      mutation: {
        path: singletonReferencePath(
          lookup,
          "zcc_trusted_network",
          "lookup",
        ),
        content: "comparison-lookup-race-secret",
      },
    });
    const invocation = invoke(
      compareRequest(lookup, "zcc_trusted_network"),
      { fake: lookup.fake, tempRoot: lookup.tempRoot },
    );
    assert.equal(invocation.status, 1);
    requireCompareError(invocation.response);
    assert.equal(invocation.response.error.code, "COMPARE_ARTIFACT_CHANGED");
    assert.equal(invocation.stdout.includes("comparison-lookup-race-secret"), false);
    assert.deepEqual(readdirSync(lookup.tempRoot), []);
  } finally {
    rmSync(lookup.root, { recursive: true, force: true });
  }
});

test("comparison rejects parent replacement despite exact hard-linked references", async () => {
  const cases = [
    {
      name: "ordinary-config-with-unsupported-absence",
      resourceType: "zcc_web_privacy",
      parentKind: "config",
      retainedNames: ["zcc_web_privacy.auto.tfvars.json"],
      removeNames: [],
      absentNames: [
        "zcc_web_privacy.auto.tfvars",
        "zcc_web_privacy.generated.expressions.json",
        "zcc_web_privacy.lookup.json",
      ],
    },
    {
      name: "ordinary-imports",
      resourceType: "zcc_web_privacy",
      parentKind: "imports",
      retainedNames: ["zcc_web_privacy_imports.tf"],
      removeNames: [],
      absentNames: [
        "zcc_web_privacy_moves.tf",
        "zcc_web_privacy_moves.pending.json",
      ],
    },
    {
      name: "trusted-tfvars-and-lookup",
      resourceType: "zcc_trusted_network",
      parentKind: "config",
      retainedNames: [
        "zcc_trusted_network.auto.tfvars.json",
        "zcc_trusted_network.lookup.json",
      ],
      removeNames: [],
      absentNames: [
        "zcc_trusted_network.auto.tfvars",
        "zcc_trusted_network.generated.expressions.json",
      ],
    },
    {
      name: "missing-tfvars",
      resourceType: "zcc_web_privacy",
      parentKind: "config",
      retainedNames: [],
      removeNames: ["zcc_web_privacy.auto.tfvars.json"],
      absentNames: [
        "zcc_web_privacy.auto.tfvars.json",
        "zcc_web_privacy.auto.tfvars",
        "zcc_web_privacy.generated.expressions.json",
        "zcc_web_privacy.lookup.json",
      ],
    },
    {
      name: "missing-trusted-lookup",
      resourceType: "zcc_trusted_network",
      parentKind: "config",
      retainedNames: ["zcc_trusted_network.auto.tfvars.json"],
      removeNames: ["zcc_trusted_network.lookup.json"],
      absentNames: [
        "zcc_trusted_network.lookup.json",
        "zcc_trusted_network.auto.tfvars",
        "zcc_trusted_network.generated.expressions.json",
      ],
    },
  ] as const satisfies readonly {
    readonly name: string;
    readonly resourceType: ZccPullResourceType;
    readonly parentKind: "config" | "imports";
    readonly retainedNames: readonly string[];
    readonly removeNames: readonly string[];
    readonly absentNames: readonly string[];
  }[];

  for (const regression of cases) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, regression.resourceType);
      const parent = path.join(
        fixture.workspace,
        regression.parentKind,
        TENANT,
      );
      for (const name of regression.removeNames) {
        rmSync(path.join(parent, name));
      }
      await assert.rejects(
        compareZccAdoptionArtifactsOperation({
          workspace: fixture.workspace,
          deploymentPath: fixture.deploymentPath,
          catalogPath: fixture.catalogPath,
          tenant: TENANT,
          resourceType: regression.resourceType,
          hostAuthority: {
            terraformExecutable: fixture.fake,
            tempRoot: fixture.tempRoot,
            environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
          },
          adoptionHooks: {
            afterOracle() {
              replaceParentWithExactHardlinks(parent, regression.retainedNames);
              for (const name of regression.absentNames) {
                assert.equal(existsSync(path.join(parent, name)), false, name);
              }
            },
          },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "COMPARE_ARTIFACT_PARENT_CHANGED"
          && error.category === "io"
          && !JSON.stringify(error).includes(parent),
        regression.name,
      );
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }
});

test("comparison preserves missing-parent review semantics", async () => {
  const fixture = createFixture();
  try {
    pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
    const missingParent = path.join(fixture.workspace, "config", TENANT);
    rmSync(missingParent, { recursive: true });
    const result = await compareZccAdoptionArtifactsOperation({
      workspace: fixture.workspace,
      deploymentPath: fixture.deploymentPath,
      catalogPath: fixture.catalogPath,
      tenant: TENANT,
      resourceType: "zcc_web_privacy",
      hostAuthority: {
        terraformExecutable: fixture.fake,
        tempRoot: fixture.tempRoot,
        environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
      },
    });
    assert.equal(result.status, "review_required");
    assert.equal(result.parity.status, "different");
    assert.equal(result.parity.artifacts.tfvars.status, "different");
    assert.equal(result.parity.artifacts.tfvars.reference?.sha256, null);
    assert.equal(existsSync(missingParent), false);
    assert.deepEqual(readdirSync(fixture.tempRoot), []);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("comparison accepts a disjoint external artifact overlay", async () => {
  const fixture = createFixture();
  try {
    const externalOverlay = path.join(fixture.root, "external-artifacts");
    mkdirSync(externalOverlay, { mode: 0o700 });
    writeFileSync(
      fixture.deploymentPath,
      `${JSON.stringify({ overlay: externalOverlay, roots: {} })}\n`,
    );
    pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
    const result = await compareZccAdoptionArtifactsOperation({
      workspace: fixture.workspace,
      deploymentPath: fixture.deploymentPath,
      catalogPath: fixture.catalogPath,
      tenant: TENANT,
      resourceType: "zcc_web_privacy",
      hostAuthority: {
        terraformExecutable: fixture.fake,
        tempRoot: fixture.tempRoot,
        environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
      },
    });
    assert.equal(result.status, "ready");
    assert.equal(result.parity.status, "equal");
    assert.equal(
      validateZccAdoptionArtifactParity(result),
      true,
      JSON.stringify(validateZccAdoptionArtifactParity.errors),
    );
    assert.deepEqual(readdirSync(fixture.tempRoot), []);
  } finally {
    rmSync(fixture.root, { recursive: true, force: true });
  }
});

test("comparison preserves oracle error and timeout precedence with verified cleanup", async (t) => {
  for (const [behavior, expectedCode, expectedExit] of [
    ["fail-init", "ZCC_ADOPTION_ORACLE_INIT_FAILED", 1],
    ["bad-plan-envelope", "ZCC_ADOPTION_ORACLE_PLAN_REJECTED", 2],
    ["bad-state-provider", "ZCC_ADOPTION_ORACLE_STATE_REJECTED", 2],
    ["missing-state-resource", "ZCC_ADOPTION_ORACLE_STATE_REJECTED", 2],
  ] as const) {
    const fixture = createFixture();
    try {
      pythonAdopt(fixture.workspace, fixture.fake, "zcc_web_privacy");
      writeFake({ executable: fixture.fake, log: fixture.log, behavior });
      const invocation = invoke(
        compareRequest(fixture, "zcc_web_privacy"),
        { fake: fixture.fake, tempRoot: fixture.tempRoot },
      );
      assert.equal(invocation.status, expectedExit, behavior);
      requireCompareError(invocation.response);
      assert.equal(invocation.response.error.code, expectedCode, behavior);
      assert.equal(invocation.stdout.includes("provider-child-secret"), false);
      assert.deepEqual(readdirSync(fixture.tempRoot), []);
    } finally {
      rmSync(fixture.root, { recursive: true, force: true });
    }
  }

  const timeout = createFixture();
  pythonAdopt(timeout.workspace, timeout.fake, "zcc_web_privacy");
  let calls = 0;
  t.mock.method(performance, "now", () => {
    calls += 1;
    return calls === 1 ? 0 : 300_000;
  });
  try {
    await assert.rejects(
      compareZccAdoptionArtifactsOperation({
        workspace: timeout.workspace,
        deploymentPath: timeout.deploymentPath,
        catalogPath: timeout.catalogPath,
        tenant: TENANT,
        resourceType: "zcc_web_privacy",
        hostAuthority: {
          terraformExecutable: timeout.fake,
          tempRoot: timeout.tempRoot,
          environment: { ZSCALER_CLIENT_SECRET: "credential-secret" },
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "ZCC_ADOPTION_ORACLE_TIMEOUT"
        && !JSON.stringify(error).includes("credential-secret")
        && !JSON.stringify(error).includes(timeout.tempRoot),
    );
    assert.deepEqual(readdirSync(timeout.tempRoot), []);
  } finally {
    rmSync(timeout.root, { recursive: true, force: true });
  }
});
