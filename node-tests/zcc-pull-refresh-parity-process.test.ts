import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  cpSync,
  copyFileSync,
  existsSync,
  lstatSync,
  linkSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  readdirSync,
  rmSync,
  renameSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { createHash } from "node:crypto";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullRefreshParity,
  validateZccPullRefreshParitySeed,
} from "../node-src/contracts/validators.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  ZccPullRefreshParity,
  ZccPullRefreshParitySeed,
} from "../node-src/domain/zcc-pull-refresh-parity.js";
import type { ProcessResponse } from "../node-src/process/types.js";
import {
  compareZccPullRefreshParityOperation,
  seedZccPullRefreshParityOperation,
} from "../node-src/domain/zcc-pull-refresh-parity.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

const REPO = process.cwd();
const PROCESS_MAIN = path.join(REPO, ".node-test/node-src/process/main.js");
const ROOT_CATALOG = path.join(REPO, "catalogs/zscaler-root-catalog.v1.json");
const TENANT = "refresh_parity";
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

interface Twin {
  readonly workspace: string;
  readonly deployment: string;
  readonly catalog: string;
}

function raw(resourceType: ZccPullResourceType): string {
  return resourceType === "zcc_device_cleanup"
    ? '[{"active":"1","id":"device-1"}]\n'
    : readFileSync(
        path.join(REPO, `tests/fixtures/demo/${resourceType}.json`),
        "utf8",
      );
}

function twin(prefix: string): Twin {
  const workspace = realpathSync(mkdtempSync(path.join(os.tmpdir(), `${prefix}-`)));
  const deployment = path.join(workspace, "deployment.json");
  const catalog = path.join(workspace, "catalog.json");
  writeFileSync(deployment, '{"overlay":".","roots":{}}\n');
  copyFileSync(ROOT_CATALOG, catalog);
  return { workspace, deployment, catalog };
}

function pullPath(twinValue: Twin, resourceType: ZccPullResourceType): string {
  return path.join(
    twinValue.workspace,
    "pulls",
    TENANT,
    `${resourceType}.json`,
  );
}

function writePull(twinValue: Twin, resourceType: ZccPullResourceType): void {
  writePullValue(twinValue, resourceType, raw(resourceType));
}

function writePullValue(
  twinValue: Twin,
  resourceType: ZccPullResourceType,
  value: string | readonly unknown[],
): void {
  const destination = pullPath(twinValue, resourceType);
  mkdirSync(path.dirname(destination), { recursive: true });
  writeFileSync(
    destination,
    typeof value === "string" ? value : `${JSON.stringify(value)}\n`,
  );
}

function runPython(twinValue: Twin, resourceType: ZccPullResourceType): void {
  const pythonPath = process.env.PYTHONPATH;
  const run = spawnSync(
    PYTHON_ORACLE,
    ["-m", "engine.transform", resourceType, pullPath(twinValue, resourceType), TENANT],
    {
      cwd: twinValue.workspace,
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
      env: {
        ...process.env,
        INFRAWRIGHT_DEPLOYMENT: twinValue.deployment,
        PYTHONPATH: pythonPath === undefined || pythonPath.length === 0
          ? REPO
          : `${REPO}${path.delimiter}${pythonPath}`,
      },
    },
  );
  assert.equal(run.signal, null, run.error?.message);
  assert.equal(run.status, 0, run.stderr);
}

function context(twinValue: Twin) {
  return {
    workspace: twinValue.workspace,
    deployment: "deployment.json",
    root_catalog: "catalog.json",
  } as const;
}

function invoke(request: unknown): {
  readonly status: number | null;
  readonly stdout: string;
  readonly stderr: string;
  readonly response: ProcessResponse;
} {
  assert.equal(
    validateProcessRequest(request),
    true,
    JSON.stringify(validateProcessRequest.errors),
  );
  const run = spawnSync(process.execPath, [PROCESS_MAIN], {
    cwd: REPO,
    input: JSON.stringify(request),
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    env: { ...process.env, INFRAWRIGHT_TERRAFORM_EXECUTABLE: "" },
  });
  assert.equal(run.signal, null, run.error?.message);
  assert.doesNotThrow(() => JSON.parse(run.stdout), run.stderr);
  const response = JSON.parse(run.stdout) as ProcessResponse;
  assert.equal(
    validateProcessResponse(response),
    true,
    JSON.stringify(validateProcessResponse.errors),
  );
  return { status: run.status, stdout: run.stdout, stderr: run.stderr, response };
}

function seedRequest(candidate: Twin, reference: Twin, resourceType: ZccPullResourceType) {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `seed-${resourceType}`,
    operation: "seed_pull_refresh_parity",
    context: context(candidate),
    input: {
      mode: "refresh",
      reference: "materialized_twin",
      tenant: TENANT,
      resource_type: resourceType,
      reference_context: context(reference),
    },
  } as const;
}

function compareRequest(
  candidate: Twin,
  reference: Twin,
  resourceType: ZccPullResourceType,
  seed: ZccPullRefreshParitySeed,
) {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: `compare-${resourceType}`,
    operation: "compare_pull_artifacts",
    context: context(candidate),
    input: {
      mode: "refresh",
      reference: "materialized_twin",
      tenant: TENANT,
      resource_type: resourceType,
      reference_context: context(reference),
      seed,
    },
  } as const;
}

function resultSeed(response: ProcessResponse): ZccPullRefreshParitySeed {
  assert.equal(response.status, "ok");
  assert.equal(response.operation, "seed_pull_refresh_parity");
  assert.equal(response.result?.kind, "infrawright.zcc_pull_refresh_parity_seed");
  return response.result as ZccPullRefreshParitySeed;
}

function resultParity(response: ProcessResponse): ZccPullRefreshParity {
  assert.equal(response.status, "ok");
  assert.equal(response.operation, "compare_pull_artifacts");
  assert.equal(response.result?.kind, "infrawright.zcc_pull_refresh_parity");
  return response.result as ZccPullRefreshParity;
}

function snapshot(root: string): Readonly<Record<string, string>> {
  const output: Record<string, string> = Object.create(null) as Record<string, string>;
  function visit(directory: string): void {
    for (const name of readdirSync(directory).sort()) {
      const absolute = path.join(directory, name);
      const relative = path.relative(root, absolute);
      const metadata = lstatSync(absolute);
      if (metadata.isDirectory()) {
        output[`${relative}/`] = "directory";
        visit(absolute);
      } else if (metadata.isFile()) {
        output[relative] = createHash("sha256").update(readFileSync(absolute)).digest("hex");
      } else {
        output[relative] = "special";
      }
    }
  }
  visit(root);
  return output;
}

function replaceDirectoryWithExactCopy(directory: string): void {
  const backup = `${directory}.bound-copy`;
  renameSync(directory, backup);
  cpSync(backup, directory, { recursive: true });
}

function directSeedOptions(
  candidate: Twin,
  reference: Twin,
  resourceType: ZccPullResourceType,
) {
  return {
    context: context(candidate),
    referenceContext: context(reference),
    tenant: TENANT,
    resourceType,
  } as const;
}

test("public seed/run-two/compare proves all five actual Python refresh writers", () => {
  for (const resourceType of RESOURCES) {
    const candidate = twin(`zcc-refresh-candidate-${resourceType}`);
    const reference = twin(`zcc-refresh-reference-${resourceType}`);
    try {
      writePull(candidate, resourceType);
      writePull(reference, resourceType);
      runPython(candidate, resourceType);
      runPython(reference, resourceType);
      const candidateBeforeSeed = snapshot(candidate.workspace);
      const referenceBeforeSeed = snapshot(reference.workspace);
      const seeded = invoke(seedRequest(candidate, reference, resourceType));
      assert.equal(seeded.status, 0, seeded.stdout);
      assert.equal(seeded.stderr, "");
      const seed = resultSeed(seeded.response);
      assert.equal(validateZccPullRefreshParitySeed(seed), true);
      assert.equal(seed.status, "ready");
      assert.deepEqual(seed.differences, []);
      assert.deepEqual(snapshot(candidate.workspace), candidateBeforeSeed);
      assert.deepEqual(snapshot(reference.workspace), referenceBeforeSeed);
      assert.equal(seeded.stdout.includes("content"), false);
      assert.equal(seeded.stdout.includes("from_key"), false);
      assert.equal(seeded.stdout.includes(candidate.workspace), false);
      assert.equal(seeded.stdout.includes(reference.workspace), false);

      runPython(reference, resourceType);
      const candidateBeforeCompare = snapshot(candidate.workspace);
      const referenceBeforeCompare = snapshot(reference.workspace);
      const compared = invoke(compareRequest(candidate, reference, resourceType, seed));
      assert.equal(compared.status, 0, compared.stdout);
      assert.equal(compared.stderr, "");
      const parity = resultParity(compared.response);
      assert.equal(validateZccPullRefreshParity(parity), true);
      assert.equal(parity.status, "ready");
      assert.equal(parity.parity.status, "equal");
      assert.equal(parity.parity.matched, 7);
      assert.equal(parity.parity.mismatched, 0);
      assert.equal(parity.parity.missing, 0);
      assert.equal(parity.parity.unexpected, 0);
      assert.deepEqual(snapshot(candidate.workspace), candidateBeforeCompare);
      assert.deepEqual(snapshot(reference.workspace), referenceBeforeCompare);
      assert.equal(compared.stdout.includes("content"), false);
      assert.equal(compared.stdout.includes("from_key"), false);
    } finally {
      rmSync(candidate.workspace, { recursive: true, force: true });
      rmSync(reference.workspace, { recursive: true, force: true });
    }
  }
});

test("two-phase parity proves ready rename, lookup, grouping, Unicode, HCL, and empty cases", () => {
  const trustedBaseline = [{
    active: true,
    conditionType: "1",
    dnsSearchDomains: "corp.example.test,lab.example.test",
    dnsServers: "10.0.0.53,10.0.1.53",
    hostnames: "nas.example.test",
    id: "trusted-id",
    networkName: "R&amp;D, 東京",
    resolvedIpsForHostname: "10.0.0.21,10.0.0.22",
    ssids: "Corp,Guest",
    trustedDhcpServers: "10.0.0.1,10.0.1.1",
    trustedEgressIps: "198.51.100.10,198.51.100.11",
    trustedGateways: "10.0.0.1,10.0.1.1",
    trustedSubnets: "10.0.0.0/24,10.0.1.0/24",
  }];
  const scenarios = [
    {
      name: "forwarding-rename",
      resourceType: "zcc_forwarding_profile" as const,
      baseline: [{ id: "rename-id", name: "Before" }],
      next: [{ id: "rename-id", name: "After" }],
      safeMoves: 1,
    },
    {
      name: "trusted-html-unicode-csv-rename",
      resourceType: "zcc_trusted_network" as const,
      baseline: trustedBaseline,
      next: [{ ...trustedBaseline[0], networkName: "R&amp;D HQ, 東京" }],
      safeMoves: 1,
    },
    {
      name: "grouped-bindings-disabled",
      resourceType: "zcc_forwarding_profile" as const,
      baseline: [{ id: "grouped-id", name: "Grouped" }],
      next: [{ id: "grouped-id", name: "Grouped" }],
      safeMoves: 0,
      roots: {
        zcc: {
          bind_references: false,
          groups: {
            zcc_edge: ["zcc_forwarding_profile", "zcc_trusted_network"],
          },
        },
      },
    },
    {
      name: "unicode-key-fallback",
      resourceType: "zcc_forwarding_profile" as const,
      baseline: [{ id: "unicode-id", name: "東京" }],
      next: [{ id: "unicode-id", name: "Tokyo" }],
      safeMoves: 1,
    },
    {
      name: "hcl-escaped-id",
      resourceType: "zcc_forwarding_profile" as const,
      baseline: [{ id: "placeholder", name: "Placeholder" }],
      next: [{
        id: "id\"\\line\nrow\rcol\t${name}%{ if true }",
        name: "Escaped Destination",
      }],
      safeMoves: 0,
    },
    {
      name: "empty",
      resourceType: "zcc_forwarding_profile" as const,
      baseline: [],
      next: [],
      safeMoves: 0,
    },
  ] as const;

  for (const scenario of scenarios) {
    const candidate = twin(`zcc-refresh-${scenario.name}-candidate`);
    const reference = twin(`zcc-refresh-${scenario.name}-reference`);
    try {
      if ("roots" in scenario) {
        const deployment = `${JSON.stringify({ overlay: ".", roots: scenario.roots })}\n`;
        writeFileSync(candidate.deployment, deployment);
        writeFileSync(reference.deployment, deployment);
      }
      writePullValue(candidate, scenario.resourceType, scenario.baseline);
      writePullValue(reference, scenario.resourceType, scenario.baseline);
      runPython(candidate, scenario.resourceType);
      runPython(reference, scenario.resourceType);
      writePullValue(candidate, scenario.resourceType, scenario.next);
      writePullValue(reference, scenario.resourceType, scenario.next);
      const seeded = invoke(seedRequest(candidate, reference, scenario.resourceType));
      assert.equal(seeded.status, 0, `${scenario.name}: ${seeded.stdout}`);
      const seed = resultSeed(seeded.response);
      assert.equal(seed.status, "ready", scenario.name);
      assert.equal(seed.candidate.moves.safe_count, scenario.safeMoves, scenario.name);
      assert.equal(seed.candidate.moves.suppressed_count, 0, scenario.name);
      runPython(reference, scenario.resourceType);
      const compared = invoke(
        compareRequest(candidate, reference, scenario.resourceType, seed),
      );
      assert.equal(compared.status, 0, `${scenario.name}: ${compared.stdout}`);
      const parity = resultParity(compared.response);
      assert.equal(parity.status, "ready", scenario.name);
      assert.equal(parity.parity.matched, 7, scenario.name);
      assert.equal(
        parity.parity.artifacts.moves.expected.state,
        scenario.safeMoves === 0 ? "absent" : "present",
        scenario.name,
      );
    } finally {
      rmSync(candidate.workspace, { recursive: true, force: true });
      rmSync(reference.workspace, { recursive: true, force: true });
    }
  }
});

test("suppressed rename evidence produces a content-free review seed and exit 3", () => {
  const candidate = twin("zcc-refresh-suppressed-candidate");
  const reference = twin("zcc-refresh-suppressed-reference");
  const resourceType = "zcc_forwarding_profile";
  try {
    const baseline = [{ id: "duplicate-id", name: "Before" }];
    const next = [
      { id: "duplicate-id", name: "After Alpha" },
      { id: "duplicate-id", name: "After Beta" },
    ];
    writePullValue(candidate, resourceType, baseline);
    writePullValue(reference, resourceType, baseline);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    writePullValue(candidate, resourceType, next);
    writePullValue(reference, resourceType, next);
    const invocation = invoke(seedRequest(candidate, reference, resourceType));
    assert.equal(invocation.status, 3, invocation.stdout);
    const seed = resultSeed(invocation.response);
    assert.equal(seed.status, "review_required");
    assert.equal(seed.candidate.moves.safe_count, 0);
    assert.equal(seed.candidate.moves.suppressed_count, 2);
    assert.equal(invocation.stdout.includes("from_key"), false);
    assert.equal(invocation.stdout.includes("to_key"), false);
    assert.equal(invocation.stdout.includes("duplicate-id"), false);
    const compare = compareRequest(candidate, reference, resourceType, seed);
    assert.equal(validateProcessRequest(compare), false);
  } finally {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
  }
});

test("refresh parity classifies mismatch, missing, and unexpected roles", () => {
  const candidate = twin("zcc-refresh-class-candidate");
  const reference = twin("zcc-refresh-class-reference");
  const resourceType = "zcc_forwarding_profile";
  try {
    writePull(candidate, resourceType);
    writePull(reference, resourceType);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    const seed = resultSeed(invoke(seedRequest(candidate, reference, resourceType)).response);
    runPython(reference, resourceType);
    const tfvars = path.join(
      reference.workspace,
      "config",
      TENANT,
      `${resourceType}.auto.tfvars.json`,
    );
    const imports = path.join(
      reference.workspace,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    );
    const pending = imports.slice(0, -"_imports.tf".length) + "_moves.pending.json";
    assert.equal(existsSync(tfvars), true);
    unlinkSync(tfvars);
    writeFileSync(imports, "different-reference-bytes\n");
    writeFileSync(pending, "unexpected-reference-bytes\n");
    const invocation = invoke(compareRequest(candidate, reference, resourceType, seed));
    assert.equal(invocation.status, 3, invocation.stdout);
    assert.equal(invocation.stderr, "");
    const parity = resultParity(invocation.response);
    assert.equal(parity.status, "review_required");
    assert.equal(parity.parity.artifacts.tfvars.status, "missing");
    assert.equal(parity.parity.artifacts.imports.status, "mismatch");
    assert.equal(parity.parity.artifacts.pending_moves.status, "unexpected");
    assert.equal(invocation.stdout.includes("different-reference-bytes"), false);
    assert.equal(invocation.stdout.includes("unexpected-reference-bytes"), false);
  } finally {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
  }
});

test("refresh parity supports disjoint canonical external-overlay authorities", () => {
  const candidate = twin("zcc-refresh-external-candidate");
  const reference = twin("zcc-refresh-external-reference");
  const candidateOverlay = realpathSync(mkdtempSync(path.join(os.tmpdir(), "zcc-refresh-c-overlay-")));
  const referenceOverlay = realpathSync(mkdtempSync(path.join(os.tmpdir(), "zcc-refresh-r-overlay-")));
  const resourceType = "zcc_trusted_network";
  try {
    writeFileSync(
      candidate.deployment,
      `${JSON.stringify({ overlay: candidateOverlay, roots: {} })}\n`,
    );
    writeFileSync(
      reference.deployment,
      `${JSON.stringify({ overlay: referenceOverlay, roots: {} })}\n`,
    );
    writePull(candidate, resourceType);
    writePull(reference, resourceType);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    const seed = resultSeed(invoke(seedRequest(candidate, reference, resourceType)).response);
    assert.equal(seed.status, "ready");
    runPython(reference, resourceType);
    const parity = resultParity(
      invoke(compareRequest(candidate, reference, resourceType, seed)).response,
    );
    assert.equal(parity.status, "ready");
    assert.equal(parity.parity.matched, 7);
  } finally {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
    rmSync(candidateOverlay, { recursive: true, force: true });
    rmSync(referenceOverlay, { recursive: true, force: true });
  }
});

test("seed rejects workspace overlap, symlink aliases, and cross-twin hard links", () => {
  const candidate = twin("zcc-refresh-isolation-candidate");
  const reference = twin("zcc-refresh-isolation-reference");
  const resourceType = "zcc_forwarding_profile";
  const alias = `${candidate.workspace}-alias`;
  try {
    writePull(candidate, resourceType);
    writePull(reference, resourceType);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);

    const overlap = invoke(seedRequest(candidate, candidate, resourceType));
    assert.equal(overlap.status, 2);
    assert.equal(overlap.response.status, "error");

    symlinkSync(candidate.workspace, alias);
    const aliased = seedRequest(candidate, reference, resourceType) as unknown as {
      context: { workspace: string; deployment: string; root_catalog: string };
    };
    aliased.context.workspace = alias;
    const aliasResult = invoke(aliased);
    assert.equal(aliasResult.status, 2);

    const candidateImports = path.join(
      candidate.workspace,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    );
    const referenceImports = path.join(
      reference.workspace,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    );
    unlinkSync(referenceImports);
    linkSync(candidateImports, referenceImports);
    const hardLinked = invoke(seedRequest(candidate, reference, resourceType));
    assert.equal(hardLinked.status, 2);
    assert.equal(hardLinked.response.status, "error");
  } finally {
    rmSync(alias, { recursive: true, force: true });
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
  }
});

test("final comparison rejects candidate baseline and reference binding drift", () => {
  const candidate = twin("zcc-refresh-stale-candidate");
  const reference = twin("zcc-refresh-stale-reference");
  const resourceType = "zcc_forwarding_profile";
  try {
    writePull(candidate, resourceType);
    writePull(reference, resourceType);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    const seed = resultSeed(invoke(seedRequest(candidate, reference, resourceType)).response);
    runPython(reference, resourceType);

    const candidateImports = path.join(
      candidate.workspace,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    );
    writeFileSync(candidateImports, `${readFileSync(candidateImports, "utf8")}\n`);
    const staleCandidate = invoke(compareRequest(candidate, reference, resourceType, seed));
    assert.equal(staleCandidate.status, 2);

    runPython(candidate, resourceType);
    const freshSeed = resultSeed(invoke(seedRequest(candidate, reference, resourceType)).response);
    writeFileSync(reference.deployment, '{ "overlay": ".", "roots": {} }\n');
    const staleReference = invoke(
      compareRequest(candidate, reference, resourceType, freshSeed),
    );
    assert.equal(staleReference.status, 2);
  } finally {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
  }
});

test("reference races and special files are I/O failures, never parity", async () => {
  const candidate = twin("zcc-refresh-race-candidate");
  const reference = twin("zcc-refresh-race-reference");
  const resourceType = "zcc_forwarding_profile";
  try {
    writePull(candidate, resourceType);
    writePull(reference, resourceType);
    runPython(candidate, resourceType);
    runPython(reference, resourceType);
    const seed = resultSeed(invoke(seedRequest(candidate, reference, resourceType)).response);
    runPython(reference, resourceType);
    const tfvars = path.join(
      reference.workspace,
      "config",
      TENANT,
      `${resourceType}.auto.tfvars.json`,
    );
    const originalTfvars = readFileSync(tfvars);
    await assert.rejects(
      compareZccPullRefreshParityOperation({
        context: context(candidate),
        referenceContext: context(reference),
        tenant: TENANT,
        resourceType,
        seed,
        hooks: {
          afterReferenceBound: () => {
            writeFileSync(tfvars, "race-private-value\n");
          },
        },
      }),
      (error: unknown) => error instanceof ProcessFailure
        && error.category === "io"
        && !error.message.includes("race-private-value"),
    );

    writeFileSync(tfvars, originalTfvars);
    unlinkSync(tfvars);
    symlinkSync(reference.deployment, tfvars);
    const special = invoke(compareRequest(candidate, reference, resourceType, seed));
    assert.equal(special.status, 1, special.stdout);
    assert.equal(special.response.status, "error");
  } finally {
    rmSync(candidate.workspace, { recursive: true, force: true });
    rmSync(reference.workspace, { recursive: true, force: true });
  }
});

test("strict parity preflight rejects aliases and missing baselines before content parsing", async () => {
  const resourceType = "zcc_forwarding_profile";
  const privateValue = "strict-preflight-private-value";
  const cases = [
    {
      name: "catalog-symlink",
      mutate(candidate: Twin): void {
        const secret = path.join(candidate.workspace, "private-catalog.json");
        writeFileSync(secret, `{\"${privateValue}\":`);
        unlinkSync(candidate.catalog);
        symlinkSync(secret, candidate.catalog);
      },
    },
    {
      name: "deployment-symlink",
      mutate(candidate: Twin): void {
        const secret = path.join(candidate.workspace, "private-deployment.json");
        writeFileSync(secret, `{\"${privateValue}\":`);
        unlinkSync(candidate.deployment);
        symlinkSync(secret, candidate.deployment);
      },
    },
    {
      name: "source-symlink",
      mutate(candidate: Twin): void {
        const source = pullPath(candidate, resourceType);
        const secret = path.join(candidate.workspace, "private-source.json");
        writeFileSync(secret, `{\"${privateValue}\":`);
        unlinkSync(source);
        symlinkSync(secret, source);
      },
    },
    {
      name: "target-parent-symlink-before-source-parse",
      mutate(candidate: Twin): void {
        const source = pullPath(candidate, resourceType);
        writeFileSync(source, `{\"${privateValue}\":`);
        const parent = path.join(candidate.workspace, "imports", TENANT);
        const secretParent = path.join(candidate.workspace, "private-import-parent");
        renameSync(parent, secretParent);
        symlinkSync(secretParent, parent);
      },
    },
    {
      name: "missing-baseline-before-source-parse",
      mutate(candidate: Twin): void {
        writeFileSync(pullPath(candidate, resourceType), `{\"${privateValue}\":`);
        unlinkSync(path.join(
          candidate.workspace,
          "imports",
          TENANT,
          `${resourceType}_imports.tf`,
        ));
      },
    },
  ] as const;

  for (const scenario of cases) {
    const candidate = twin(`zcc-refresh-preflight-${scenario.name}-candidate`);
    const reference = twin(`zcc-refresh-preflight-${scenario.name}-reference`);
    try {
      writePull(candidate, resourceType);
      writePull(reference, resourceType);
      runPython(candidate, resourceType);
      runPython(reference, resourceType);
      scenario.mutate(candidate);
      await assert.rejects(
        seedZccPullRefreshParityOperation(
          directSeedOptions(candidate, reference, resourceType),
        ),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "INVALID_REFRESH_PARITY_ISOLATION"
          && !error.message.includes(privateValue),
      );
    } finally {
      rmSync(candidate.workspace, { recursive: true, force: true });
      rmSync(reference.workspace, { recursive: true, force: true });
    }
  }
});

test("target-parent and ancestor identity replacement fail despite exact artifact bytes", async () => {
  const resourceType = "zcc_forwarding_profile";
  const cases = [
    {
      name: "copied-parent",
      replace(candidate: Twin): void {
        replaceDirectoryWithExactCopy(path.join(candidate.workspace, "imports", TENANT));
      },
      expectedCodes: new Set([
        "PRIOR_IMPORTS_CHANGED",
        "REFRESH_IMPORTS_CHANGED",
        "REFRESH_PARITY_TARGET_PARENT_CHANGED",
      ]),
    },
    {
      name: "replaced-ancestor-preserved-parent-and-files",
      replace(candidate: Twin): void {
        const importsRoot = path.join(candidate.workspace, "imports");
        const tenantParent = path.join(importsRoot, TENANT);
        const heldParent = path.join(candidate.workspace, ".held-imports-tenant");
        const oldRoot = path.join(candidate.workspace, ".bound-imports-root");
        renameSync(tenantParent, heldParent);
        renameSync(importsRoot, oldRoot);
        mkdirSync(importsRoot);
        renameSync(heldParent, tenantParent);
      },
      expectedCodes: new Set(["REFRESH_PARITY_TARGET_PARENT_CHANGED"]),
    },
  ] as const;

  for (const scenario of cases) {
    const candidate = twin(`zcc-refresh-parent-${scenario.name}-candidate`);
    const reference = twin(`zcc-refresh-parent-${scenario.name}-reference`);
    try {
      writePull(candidate, resourceType);
      writePull(reference, resourceType);
      runPython(candidate, resourceType);
      runPython(reference, resourceType);
      await assert.rejects(
        seedZccPullRefreshParityOperation({
          ...directSeedOptions(candidate, reference, resourceType),
          hooks: {
            beforeCandidateFinalCas: () => scenario.replace(candidate),
          },
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.category === "io"
          && scenario.expectedCodes.has(error.code),
      );
    } finally {
      rmSync(candidate.workspace, { recursive: true, force: true });
      rmSync(reference.workspace, { recursive: true, force: true });
    }
  }
});

test("seed bindings reject exact-byte parent and ancestor replay before comparison", async () => {
  const resourceType = "zcc_forwarding_profile";
  const cases = [
    {
      name: "config-parent-copy",
      replace(candidate: Twin): void {
        replaceDirectoryWithExactCopy(path.join(candidate.workspace, "config", TENANT));
      },
    },
    {
      name: "imports-parent-copy",
      replace(candidate: Twin): void {
        replaceDirectoryWithExactCopy(path.join(candidate.workspace, "imports", TENANT));
      },
    },
    {
      name: "imports-ancestor-with-preserved-parent-and-files",
      replace(candidate: Twin): void {
        const importsRoot = path.join(candidate.workspace, "imports");
        const tenantParent = path.join(importsRoot, TENANT);
        const heldParent = path.join(candidate.workspace, ".held-seeded-imports-tenant");
        const oldRoot = path.join(candidate.workspace, ".seeded-imports-root");
        renameSync(tenantParent, heldParent);
        renameSync(importsRoot, oldRoot);
        mkdirSync(importsRoot);
        renameSync(heldParent, tenantParent);
      },
    },
  ] as const;

  for (const scenario of cases) {
    const candidate = twin(`zcc-refresh-replay-${scenario.name}-candidate`);
    const reference = twin(`zcc-refresh-replay-${scenario.name}-reference`);
    try {
      writePull(candidate, resourceType);
      writePull(reference, resourceType);
      runPython(candidate, resourceType);
      runPython(reference, resourceType);
      const seeded = await seedZccPullRefreshParityOperation(
        directSeedOptions(candidate, reference, resourceType),
      );
      scenario.replace(candidate);
      runPython(reference, resourceType);
      await assert.rejects(
        compareZccPullRefreshParityOperation({
          ...directSeedOptions(candidate, reference, resourceType),
          seed: seeded,
        }),
        (error: unknown) => error instanceof ProcessFailure
          && error.code === "REFRESH_PARITY_SEED_STALE",
      );
    } finally {
      rmSync(candidate.workspace, { recursive: true, force: true });
      rmSync(reference.workspace, { recursive: true, force: true });
    }
  }
});

test("candidate-last and reference final CAS reject raw, control, and baseline mutation", async () => {
  const resourceType = "zcc_forwarding_profile";
  const privateValue = "final-cas-private-value";
  const mutations = [
    {
      name: "raw",
      mutate(target: Twin): void {
        const source = pullPath(target, resourceType);
        writeFileSync(source, `${readFileSync(source, "utf8")} ${privateValue}`);
      },
    },
    {
      name: "control",
      mutate(target: Twin): void {
        writeFileSync(
          target.catalog,
          `${readFileSync(target.catalog, "utf8")} ${privateValue}`,
        );
      },
    },
    {
      name: "baseline",
      mutate(target: Twin): void {
        const imports = path.join(
          target.workspace,
          "imports",
          TENANT,
          `${resourceType}_imports.tf`,
        );
        writeFileSync(imports, `${readFileSync(imports, "utf8")} ${privateValue}`);
      },
    },
  ] as const;

  for (const side of ["candidate", "reference"] as const) {
    for (const mutation of mutations) {
      const candidate = twin(`zcc-refresh-cas-${side}-${mutation.name}-candidate`);
      const reference = twin(`zcc-refresh-cas-${side}-${mutation.name}-reference`);
      try {
        writePull(candidate, resourceType);
        writePull(reference, resourceType);
        runPython(candidate, resourceType);
        runPython(reference, resourceType);
        const seeded = await seedZccPullRefreshParityOperation(
          directSeedOptions(candidate, reference, resourceType),
        );
        runPython(reference, resourceType);
        await assert.rejects(
          compareZccPullRefreshParityOperation({
            ...directSeedOptions(candidate, reference, resourceType),
            seed: seeded,
            hooks: side === "candidate"
              ? { beforeCandidateFinalCas: () => mutation.mutate(candidate) }
              : { beforeReferenceFinalCas: () => mutation.mutate(reference) },
          }),
          (error: unknown) => error instanceof ProcessFailure
            && error.category === "io"
            && !error.message.includes(privateValue),
        );
      } finally {
        rmSync(candidate.workspace, { recursive: true, force: true });
        rmSync(reference.workspace, { recursive: true, force: true });
      }
    }
  }
});
