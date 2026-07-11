import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullRefreshArtifactSet,
} from "../node-src/contracts/validators.js";
import {
  renderGeneratedImports,
  type GeneratedImportPair,
} from "../node-src/domain/import-moves.js";
import {
  compileZccPullRefreshArtifactsOperation,
} from "../node-src/domain/zcc-pull-operation.js";
import type {
  ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import type {
  ZccPullRefreshArtifactSet,
  ZccRefreshDesiredArtifact,
} from "../node-src/domain/zcc-pull-refresh.js";

const WORKSPACE = process.cwd();
const ROOT_CATALOG = path.join(
  WORKSPACE,
  "catalogs/zscaler-root-catalog.v1.json",
);
const TENANT = "refresh_differential";
const RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[];

interface RefreshCase {
  readonly name: string;
  readonly resourceType: ZccPullResourceType;
  readonly baseline: readonly unknown[];
  readonly next: readonly unknown[];
  readonly roots?: Readonly<Record<string, unknown>>;
  readonly priorImports?: readonly GeneratedImportPair[];
  readonly expectedProcessExit?: number;
}

interface Twin {
  readonly workspace: string;
  readonly overlay: string;
  readonly deploymentPath: string;
  readonly pullPath: string;
}

interface PythonMoves {
  readonly safe: readonly {
    readonly from_key: string;
    readonly to_key: string;
  }[];
  readonly suppressed: readonly {
    readonly from_key: string;
    readonly to_key: string;
    readonly reason: string;
  }[];
  readonly rendered: string;
}

function demo(resourceType: ZccPullResourceType): readonly unknown[] {
  return JSON.parse(readFileSync(
    path.join(WORKSPACE, `tests/fixtures/demo/${resourceType}.json`),
    "utf8",
  )) as readonly unknown[];
}

function cases(): readonly RefreshCase[] {
  const output: RefreshCase[] = [];
  for (const resourceType of RESOURCES) {
    const raw = demo(resourceType);
    output.push({
      name: `unchanged-${resourceType}`,
      resourceType,
      baseline: raw,
      next: raw,
    });
    output.push({
      name: `empty-${resourceType}`,
      resourceType,
      baseline: [],
      next: [],
    });
  }

  output.push({
    name: "add-and-remove-are-not-renames",
    resourceType: "zcc_forwarding_profile",
    baseline: [
      { id: "keep-id", name: "Keep" },
      { id: "remove-id", name: "Remove" },
    ],
    next: [
      { id: "keep-id", name: "Keep" },
      { id: "add-id", name: "Add" },
    ],
  });
  output.push({
    name: "same-id-forwarding-rename",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "rename-id", name: "Before" }],
    next: [{ id: "rename-id", name: "After" }],
  });
  output.push({
    name: "trusted-network-html-unicode-csv-rename",
    resourceType: "zcc_trusted_network",
    baseline: [{
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
    }],
    next: [{
      active: true,
      conditionType: "1",
      dnsSearchDomains: "corp.example.test,lab.example.test",
      dnsServers: "10.0.0.53,10.0.1.53",
      hostnames: "nas.example.test",
      id: "trusted-id",
      networkName: "R&amp;D HQ, 東京",
      resolvedIpsForHostname: "10.0.0.21,10.0.0.22",
      ssids: "Corp,Guest",
      trustedDhcpServers: "10.0.0.1,10.0.1.1",
      trustedEgressIps: "198.51.100.10,198.51.100.11",
      trustedGateways: "10.0.0.1,10.0.1.1",
      trustedSubnets: "10.0.0.0/24,10.0.1.0/24",
    }],
  });
  output.push({
    name: "grouped-variable-with-bindings-disabled",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "grouped-id", name: "Grouped" }],
    next: [{ id: "grouped-id", name: "Grouped" }],
    roots: {
      zcc: {
        bind_references: false,
        groups: {
          zcc_edge: ["zcc_forwarding_profile", "zcc_trusted_network"],
        },
      },
    },
  });
  output.push({
    name: "unicode-fallback-to-ascii-key",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "unicode-id", name: "東京" }],
    next: [{ id: "unicode-id", name: "Tokyo" }],
  });
  output.push({
    name: "hcl-escaped-prior-key-and-import-id",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "placeholder", name: "Placeholder" }],
    next: [{
      id: "id\"\\line\nrow\rcol\t${name}%{ if true }",
      name: "Escaped Destination",
    }],
    priorImports: [{
      key: "old\"\\line\nrow\rcol\t${name}%{ if true }",
      importId: "id\"\\line\nrow\rcol\t${name}%{ if true }",
    }],
  });
  output.push({
    name: "duplicate-desired-identity-preserves-duplicate-from-evidence",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "duplicate-id", name: "Before" }],
    next: [
      { id: "duplicate-id", name: "After Alpha" },
      { id: "duplicate-id", name: "After Beta" },
    ],
    expectedProcessExit: 3,
  });
  output.push({
    name: "trusted-lookup-duplicate-identity-collapses-like-python",
    resourceType: "zcc_trusted_network",
    baseline: [{ id: "duplicate-id", networkName: "Before" }],
    next: [
      { id: "duplicate-id", networkName: "After Alpha" },
      { id: "duplicate-id", networkName: "After Beta" },
    ],
    expectedProcessExit: 3,
  });
  output.push({
    name: "key-swap-is-suppressed",
    resourceType: "zcc_forwarding_profile",
    baseline: [
      { id: "swap-a", name: "Alpha" },
      { id: "swap-b", name: "Beta" },
    ],
    next: [
      { id: "swap-a", name: "Beta" },
      { id: "swap-b", name: "Alpha" },
    ],
  });
  output.push({
    name: "occupied-destination-is-suppressed",
    resourceType: "zcc_forwarding_profile",
    baseline: [
      { id: "moving-id", name: "Before" },
      { id: "removed-id", name: "Occupied" },
    ],
    next: [{ id: "moving-id", name: "Occupied" }],
  });
  output.push({
    name: "ambiguous-prior-identity-is-suppressed",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "ambiguous-id", name: "Placeholder" }],
    next: [{ id: "ambiguous-id", name: "Destination" }],
    priorImports: [
      { key: "old_a", importId: "ambiguous-id" },
      { key: "old_b", importId: "ambiguous-id" },
    ],
  });
  output.push({
    name: "safe-and-suppressed-moves-coexist",
    resourceType: "zcc_forwarding_profile",
    baseline: [{ id: "safe-id", name: "Placeholder" }],
    next: [
      { id: "safe-id", name: "Safe Next" },
      { id: "ambiguous-id", name: "Ambiguous Next" },
    ],
    priorImports: [
      { key: "safe_old", importId: "safe-id" },
      { key: "ambiguous_old_a", importId: "ambiguous-id" },
      { key: "ambiguous_old_b", importId: "ambiguous-id" },
    ],
  });
  return output;
}

function createTwin(prefix: string, roots: Readonly<Record<string, unknown>>): Twin {
  const workspace = mkdtempSync(path.join(os.tmpdir(), `${prefix}-workspace-`));
  const overlay = mkdtempSync(path.join(os.tmpdir(), `${prefix}-overlay-`));
  const deploymentPath = path.join(workspace, "deployment.json");
  const pullPath = path.join(workspace, "pulls", TENANT, "placeholder.json");
  mkdirSync(path.dirname(pullPath), { recursive: true });
  writeFileSync(deploymentPath, `${JSON.stringify({ overlay, roots })}\n`);
  return { workspace, overlay, deploymentPath, pullPath };
}

function removeTwin(twin: Twin): void {
  rmSync(twin.workspace, { recursive: true, force: true });
  rmSync(twin.overlay, { recursive: true, force: true });
}

function artifactPaths(twin: Twin, resourceType: ZccPullResourceType) {
  return {
    tfvars: path.join(
      twin.overlay,
      "config",
      TENANT,
      `${resourceType}.auto.tfvars.json`,
    ),
    imports: path.join(
      twin.overlay,
      "imports",
      TENANT,
      `${resourceType}_imports.tf`,
    ),
    lookup: path.join(
      twin.overlay,
      "config",
      TENANT,
      `${resourceType}.lookup.json`,
    ),
    moves: path.join(
      twin.overlay,
      "imports",
      TENANT,
      `${resourceType}_moves.tf`,
    ),
  };
}

function writeRaw(
  twin: Twin,
  resourceType: ZccPullResourceType,
  raw: readonly unknown[],
): string {
  const pullPath = path.join(
    twin.workspace,
    "pulls",
    TENANT,
    `${resourceType}.json`,
  );
  writeFileSync(pullPath, `${JSON.stringify(raw)}\n`);
  return pullPath;
}

function runPythonTransform(
  twin: Twin,
  resourceType: ZccPullResourceType,
  raw: readonly unknown[],
): void {
  const pullPath = writeRaw(twin, resourceType, raw);
  const pythonPath = process.env.PYTHONPATH;
  const child = spawnSync(
    "python3",
    ["-m", "engine.transform", resourceType, pullPath, TENANT],
    {
      cwd: twin.workspace,
      encoding: "utf8",
      maxBuffer: 32 * 1024 * 1024,
      env: {
        ...process.env,
        INFRAWRIGHT_DEPLOYMENT: twin.deploymentPath,
        PYTHONPATH: pythonPath === undefined || pythonPath.length === 0
          ? WORKSPACE
          : `${WORKSPACE}${path.delimiter}${pythonPath}`,
      },
    },
  );
  assert.equal(child.signal, null, child.error?.message);
  assert.equal(child.status, 0, child.stderr);
}

function copyBaseline(
  pythonTwin: Twin,
  nodeTwin: Twin,
  resourceType: ZccPullResourceType,
): void {
  const source = artifactPaths(pythonTwin, resourceType);
  const destination = artifactPaths(nodeTwin, resourceType);
  for (const kind of ["tfvars", "imports", "lookup"] as const) {
    if (!existsSync(source[kind])) {
      continue;
    }
    mkdirSync(path.dirname(destination[kind]), { recursive: true });
    copyFileSync(source[kind], destination[kind]);
  }
}

function replacePriorImports(
  twin: Twin,
  resourceType: ZccPullResourceType,
  pairs: readonly GeneratedImportPair[],
): string {
  const text = renderGeneratedImports(resourceType, pairs);
  const importsPath = artifactPaths(twin, resourceType).imports;
  mkdirSync(path.dirname(importsPath), { recursive: true });
  writeFileSync(importsPath, text);
  return text;
}

function snapshotTree(root: string): Readonly<Record<string, string>> {
  const output: Record<string, string> = Object.create(null) as Record<
    string,
    string
  >;
  function visit(directory: string): void {
    for (const name of readdirSync(directory).sort()) {
      const absolute = path.join(directory, name);
      const relative = path.relative(root, absolute);
      const metadata = lstatSync(absolute);
      if (metadata.isDirectory()) {
        output[`${relative}/`] = "directory";
        visit(absolute);
      } else if (metadata.isFile()) {
        output[relative] = createHash("sha256")
          .update(readFileSync(absolute))
          .digest("hex");
      } else {
        output[relative] = `other:${metadata.mode}`;
      }
    }
  }
  visit(root);
  return output;
}

function pythonMoves(
  resourceType: ZccPullResourceType,
  priorImports: string,
  desiredImports: string,
): PythonMoves {
  const source = String.raw`
import json
import sys

from engine.transform import derive_moves_with_diagnostics, render_moves

payload = json.load(sys.stdin)
result = derive_moves_with_diagnostics(payload["prior"], payload["desired"])
json.dump({
    "safe": [
        {"from_key": old_key, "to_key": new_key}
        for old_key, new_key in result.moves
    ],
    "suppressed": [
        {
            "from_key": item.old_key,
            "to_key": item.new_key,
            "reason": item.reason,
        }
        for item in result.suppressed
    ],
    "rendered": render_moves(payload["resource_type"], result.moves),
}, sys.stdout, ensure_ascii=False, sort_keys=True)
`;
  const child = spawnSync("python3", ["-c", source], {
    cwd: WORKSPACE,
    encoding: "utf8",
    input: JSON.stringify({
      resource_type: resourceType,
      prior: priorImports,
      desired: desiredImports,
    }),
    maxBuffer: 32 * 1024 * 1024,
  });
  assert.equal(child.status, 0, child.stderr);
  return JSON.parse(child.stdout) as PythonMoves;
}

function desiredContent(
  desired: ZccRefreshDesiredArtifact,
): string | null {
  return desired.state === "present" ? desired.artifact.content : null;
}

function invokeRefreshProcess(
  twin: Twin,
  resourceType: ZccPullResourceType,
): { readonly status: number | null; readonly result: ZccPullRefreshArtifactSet } {
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "duplicate-from-differential",
    operation: "compile_pull_artifacts",
    context: {
      workspace: twin.workspace,
      deployment: twin.deploymentPath,
      root_catalog: path.join(twin.workspace, "catalog.json"),
    },
    input: {
      mode: "refresh",
      tenant: TENANT,
      resource_type: resourceType,
    },
  } as const;
  assert.equal(
    validateProcessRequest(request),
    true,
    JSON.stringify(validateProcessRequest.errors),
  );
  const child = spawnSync(
    process.execPath,
    [path.join(WORKSPACE, ".node-test/node-src/process/main.js")],
    {
      cwd: WORKSPACE,
      encoding: "utf8",
      input: JSON.stringify(request),
      env: { ...process.env, INFRAWRIGHT_TERRAFORM_EXECUTABLE: "" },
    },
  );
  assert.equal(child.signal, null, child.error?.message);
  assert.equal(child.stderr, "");
  const response = JSON.parse(child.stdout) as {
    readonly status: string;
    readonly result: ZccPullRefreshArtifactSet;
  };
  assert.equal(
    validateProcessResponse(response),
    true,
    JSON.stringify(validateProcessResponse.errors),
  );
  assert.equal(response.status, "ok");
  assert.equal(
    validateZccPullRefreshArtifactSet(response.result),
    true,
    JSON.stringify(validateZccPullRefreshArtifactSet.errors),
  );
  return { status: child.status, result: response.result };
}

function compareDesired(
  fixture: RefreshCase,
  candidate: ZccPullRefreshArtifactSet,
  pythonTwin: Twin,
): void {
  const expected = artifactPaths(pythonTwin, fixture.resourceType);
  for (const kind of ["tfvars", "imports", "lookup", "moves"] as const) {
    const expectedContent = existsSync(expected[kind])
      ? readFileSync(expected[kind], "utf8")
      : null;
    assert.equal(
      desiredContent(candidate.desired[kind]),
      expectedContent,
      `${fixture.name}: ${kind} bytes`,
    );
  }
}

test("read-only refresh compilation matches the Python run-two writer", async (t) => {
  for (const fixture of cases()) {
    await t.test(fixture.name, async () => {
      const roots = fixture.roots ?? {};
      const pythonTwin = createTwin("zcc-refresh-python", roots);
      const nodeTwin = createTwin("zcc-refresh-node", roots);
      try {
        runPythonTransform(
          pythonTwin,
          fixture.resourceType,
          fixture.baseline,
        );
        copyBaseline(pythonTwin, nodeTwin, fixture.resourceType);

        let priorImports = readFileSync(
          artifactPaths(nodeTwin, fixture.resourceType).imports,
          "utf8",
        );
        if (fixture.priorImports !== undefined) {
          priorImports = replacePriorImports(
            pythonTwin,
            fixture.resourceType,
            fixture.priorImports,
          );
          replacePriorImports(
            nodeTwin,
            fixture.resourceType,
            fixture.priorImports,
          );
        }

        runPythonTransform(
          pythonTwin,
          fixture.resourceType,
          fixture.next,
        );
        writeRaw(nodeTwin, fixture.resourceType, fixture.next);
        copyFileSync(
          ROOT_CATALOG,
          path.join(nodeTwin.workspace, "catalog.json"),
        );

        const beforeWorkspace = snapshotTree(nodeTwin.workspace);
        const beforeOverlay = snapshotTree(nodeTwin.overlay);
        const candidate = await compileZccPullRefreshArtifactsOperation({
          workspace: nodeTwin.workspace,
          deploymentPath: nodeTwin.deploymentPath,
          catalogPath: path.join(nodeTwin.workspace, "catalog.json"),
          tenant: TENANT,
          resourceType: fixture.resourceType,
        });
        assert.deepEqual(
          snapshotTree(nodeTwin.workspace),
          beforeWorkspace,
          `${fixture.name}: workspace writes`,
        );
        assert.deepEqual(
          snapshotTree(nodeTwin.overlay),
          beforeOverlay,
          `${fixture.name}: overlay writes`,
        );

        compareDesired(fixture, candidate, pythonTwin);
        const desiredImports = readFileSync(
          artifactPaths(pythonTwin, fixture.resourceType).imports,
          "utf8",
        );
        const moveOracle = pythonMoves(
          fixture.resourceType,
          priorImports,
          desiredImports,
        );
        assert.deepEqual(
          candidate.moves.safe.map((move) => ({
            from_key: move.from_key,
            to_key: move.to_key,
          })),
          moveOracle.safe,
          `${fixture.name}: safe moves`,
        );
        assert.deepEqual(
          candidate.moves.suppressed.map((move) => ({
            from_key: move.from_key,
            to_key: move.to_key,
            reason: move.reason,
          })),
          moveOracle.suppressed,
          `${fixture.name}: suppressed moves`,
        );
        assert.equal(
          desiredContent(candidate.desired.moves),
          moveOracle.rendered === "" ? null : moveOracle.rendered,
          `${fixture.name}: rendered moves`,
        );
        assert.equal(
          candidate.status,
          candidate.unexpected_drops.length === 0
              && candidate.moves.suppressed.length === 0
            ? "ready"
            : "review_required",
          `${fixture.name}: derived status`,
        );
        if (fixture.expectedProcessExit !== undefined) {
          assert.equal(candidate.status, "review_required");
          assert.deepEqual(candidate.moves.suppressed.map((item) => ({
            from_key: item.from_key,
            to_key: item.to_key,
            reason: item.reason,
          })), [
            {
              from_key: "before",
              to_key: "after_alpha",
              reason: "duplicate_from",
            },
            {
              from_key: "before",
              to_key: "after_beta",
              reason: "duplicate_from",
            },
          ]);
          assert.equal(candidate.desired.moves.state, "absent");
          if (fixture.resourceType === "zcc_trusted_network") {
            assert.equal(candidate.desired.lookup.state, "present");
            if (candidate.desired.lookup.state === "present") {
              assert.equal(
                candidate.desired.lookup.artifact.content,
                "{\n"
                + "  \"by_id\": {\n"
                + "    \"duplicate-id\": \"After Beta\"\n"
                + "  },\n"
                + "  \"key_by_id\": {\n"
                + "    \"duplicate-id\": \"after_beta\"\n"
                + "  }\n"
                + "}\n",
              );
            }
          }
          const invocation = invokeRefreshProcess(
            nodeTwin,
            fixture.resourceType,
          );
          assert.equal(invocation.status, fixture.expectedProcessExit);
          assert.deepEqual(
            JSON.parse(JSON.stringify(invocation.result)),
            JSON.parse(JSON.stringify(candidate)),
          );
        }
      } finally {
        removeTwin(pythonTwin);
        removeTwin(nodeTwin);
      }
    });
  }
});
