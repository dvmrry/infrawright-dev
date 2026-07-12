import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
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
  validateZccAdoptionArtifactSet,
} from "../node-src/contracts/validators.js";
import { compileZccAdoptionArtifactsOperation } from "../node-src/domain/zcc-adoption-operation.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  CompileAdoptionArtifactsProcessRequest,
  CompileAdoptionArtifactsProcessSuccessResponse,
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
    readonly sensitive_values: unknown;
    readonly values: Readonly<Record<string, unknown>>;
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

function evidence(): Readonly<Record<ZccPullResourceType, ResourceEvidence>> {
  const lossless = parseDataJsonLosslessly(readFileSync(CORPUS, "utf8")) as {
    readonly cases: readonly {
      readonly expected: string;
      readonly resource_type: ZccPullResourceType;
      readonly raw_items: readonly unknown[];
    }[];
  };
  const ordinary = JSON.parse(readFileSync(CORPUS, "utf8")) as {
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
    const ordinaryCase = ordinary.cases.find((entry) => {
      return entry.expected === "success"
        && entry.resource_type === resourceType
        && entry.raw_items.length > 0;
    });
    assert.notEqual(losslessCase, undefined, resourceType);
    assert.notEqual(ordinaryCase, undefined, resourceType);
    const states = Object.create(null) as Record<string, {
      sensitive_values: unknown;
      values: Readonly<Record<string, unknown>>;
    }>;
    for (const observation of ordinaryCase?.observed_states ?? []) {
      states[observation.import_id] = {
        sensitive_values: observation.sensitive_values ?? {},
        values: { ...observation.values, id: observation.import_id },
      };
    }
    output[resourceType] = {
      rawText: `${stringifyLosslessJson(losslessCase?.raw_items)}\n`,
      states,
    };
  }
  const failopen = JSON.parse(readFileSync(FAILOPEN, "utf8")) as {
    readonly raw_items: readonly unknown[];
    readonly provider_state: Readonly<Record<string, {
      readonly sensitive_values?: unknown;
      readonly values: Readonly<Record<string, unknown>>;
    }>>;
  };
  const failopenStates = Object.create(null) as Record<string, {
    sensitive_values: unknown;
    values: Readonly<Record<string, unknown>>;
  }>;
  for (const [importId, state] of Object.entries(failopen.provider_state)) {
    failopenStates[importId] = {
      sensitive_values: state.sensitive_values ?? {},
      values: { ...state.values, id: importId },
    };
  }
  output.zcc_failopen_policy = {
    rawText: `${JSON.stringify(failopen.raw_items)}\n`,
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
    "      return {",
    "        address: entry.address, mode: 'managed', type: entry.resourceType, provider_name: provider,",
    "        values: { ...state.values, id: behavior === 'bad-state-id' ? 'wrong-id' : entry.importId },",
    "        sensitive_values: state.sensitive_values,",
    "      };",
    "    });",
    "    if (behavior === 'missing-state-resource') resources = resources.slice(1);",
    "    process.stdout.write(JSON.stringify({",
    "      format_version: '1.0', terraform_version: '1.15.4', checks: [],",
    "      values: { outputs: {}, root_module: { resources, child_modules: [] } },",
    "    }));",
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

function requireError(
  response: ProcessResponse,
): asserts response is ProcessErrorResponse {
  assert.equal(response.operation, "compile_adoption_artifacts");
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
