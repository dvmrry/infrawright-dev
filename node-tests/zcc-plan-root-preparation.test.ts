import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccAdoptionArtifactMaterialization,
  validateZccPlanRootPreparation,
} from "../node-src/contracts/validators.js";
import { zccPlanRootPreparationOperationResultErrors } from "../node-src/contracts/zcc-plan-root-preparation-semantics.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import { renderGeneratedImports } from "../node-src/domain/import-moves.js";
import {
  compileZccPlanRootPreparation,
  MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES,
  MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES,
  MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES,
  renderZccPlanRootMain,
  zccPlanRootTextArtifact,
  ZCC_PLAN_ROOT_PREPARATION_PROFILE,
  ZCC_PLAN_ROOT_RESOURCE_TYPES,
} from "../node-src/domain/zcc-plan-root-preparation.js";
import type { ZccAdoptionArtifactMaterialization } from "../node-src/domain/zcc-adoption-materialization.js";
import type { ZccPullResourceType } from "../node-src/domain/zcc-pull-artifacts.js";
import type { CompilePlanRootPreparationProcessRequest } from "../node-src/process/types.js";
import { prepareProcessResponseForEmission } from "../node-src/process/response-emission.js";

const REPO = process.cwd();
const CATALOG = path.join(REPO, "catalogs/zscaler-root-catalog.v1.json");
const TENANT = "plan_root";
const PROCESS_MAIN = path.join(REPO, ".node-test/node-src/process/main.js");
const PROCESS_BUNDLE = path.join(REPO, "dist/infrawright.mjs");

function digest(logicalPath: string, content: string) {
  return {
    path: logicalPath,
    sha256: createHash("sha256").update(content, "utf8").digest("hex"),
    size_bytes: Buffer.byteLength(content, "utf8"),
  };
}

function receipt(options: {
  readonly workspace: string;
  readonly resourceType: ZccPullResourceType;
  readonly rootLabel: string;
  readonly rootMembers: readonly ZccPullResourceType[];
}): ZccAdoptionArtifactMaterialization {
  const variableName = options.rootLabel === options.resourceType
    ? "items"
    : `${options.resourceType}_items`;
  const tfvarsPath = `config/${TENANT}/${options.resourceType}.auto.tfvars.json`;
  const importsPath = `imports/${TENANT}/${options.resourceType}_imports.tf`;
  const lookupPath = `config/${TENANT}/${options.resourceType}.lookup.json`;
  const tfvarsContent = `${JSON.stringify({ [variableName]: {} })}\n`;
  const importsContent = renderGeneratedImports(options.resourceType, [{
    key: "item",
    importId: `${options.resourceType}-id`,
  }]);
  const lookupContent = "{}\n";
  mkdirSync(path.join(options.workspace, `config/${TENANT}`), { recursive: true });
  mkdirSync(path.join(options.workspace, `imports/${TENANT}`), { recursive: true });
  writeFileSync(path.join(options.workspace, tfvarsPath), tfvarsContent);
  writeFileSync(path.join(options.workspace, importsPath), importsContent);
  if (options.resourceType === "zcc_trusted_network") {
    writeFileSync(path.join(options.workspace, lookupPath), lookupContent);
  }
  const tfvars = digest(tfvarsPath, tfvarsContent);
  const imports = digest(importsPath, importsContent);
  const lookup = digest(lookupPath, lookupContent);
  const equal = options.resourceType === "zcc_trusted_network" ? 3 : 2;
  const applicable = (value: ReturnType<typeof digest>) => ({
    candidate: value,
    reference: value,
    status: "equal" as const,
  });
  const verification = {
    kind: "infrawright.zcc_adoption_artifact_parity" as const,
    schema_version: 1 as const,
    mode: "bootstrap" as const,
    reference: "materialized" as const,
    product: "zcc" as const,
    resource_type: options.resourceType,
    tenant: TENANT,
    source: {
      path: `pulls/${TENANT}/${options.resourceType}.json`,
      sha256: createHash("sha256").update(options.resourceType).digest("hex"),
      size_bytes: options.resourceType.length,
    },
    catalog: {
      kind: "infrawright.adoption_catalog" as const,
      schema_version: 1 as const,
      sha256: "ba1690397bbe84e6284affee2cfe8300f0ebb230714c332d6ff93d04a42181e7",
      sources_sha256: "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11",
    },
    root: {
      label: options.rootLabel,
      members: options.rootMembers,
      variable_name: variableName,
    },
    status: "ready" as const,
    parity: {
      status: "equal" as const,
      equal,
      different: 0,
      artifacts: {
        tfvars: applicable(tfvars),
        imports: applicable(imports),
        lookup: options.resourceType === "zcc_trusted_network"
          ? applicable(lookup)
          : { candidate: null, reference: null, status: "not_applicable" as const },
      },
    },
  };
  const value: ZccAdoptionArtifactMaterialization = {
    kind: "infrawright.zcc_adoption_artifact_materialization",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: options.resourceType,
    tenant: TENANT,
    status: "complete",
    publication: {
      policy: "create_or_verify_exact",
      created: options.resourceType === "zcc_trusted_network"
        ? ["imports", "lookup", "tfvars"]
        : ["imports", "tfvars"],
      reused: [],
    },
    verification,
  };
  assert.equal(
    validateZccAdoptionArtifactMaterialization(value),
    true,
    JSON.stringify(validateZccAdoptionArtifactMaterialization.errors),
  );
  return value;
}

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly deploymentPath: string;
  readonly catalogPath: string;
  readonly rootLabel: string;
  readonly members: readonly ZccPullResourceType[];
  readonly receipts: readonly ZccAdoptionArtifactMaterialization[];
}

function fixture(options: {
  readonly grouped?: boolean;
  readonly moduleDir?: string;
} = {}): Fixture {
  const lexical = mkdtempSync(path.join(os.tmpdir(), "zcc-plan-root-"));
  const workspace = realpathSync(lexical);
  const members = options.grouped
    ? ZCC_PLAN_ROOT_RESOURCE_TYPES
    : ["zcc_device_cleanup"] as const;
  const rootLabel = options.grouped ? "zcc_bundle" : members[0];
  const deploymentPath = path.join(workspace, "deployment.json");
  const catalogPath = path.join(workspace, "root-catalog.json");
  const deployment = {
    overlay: ".",
    ...(options.moduleDir === undefined ? {} : { module_dir: options.moduleDir }),
    roots: options.grouped
      ? {
          zcc: {
            bind_references: false,
            groups: { [rootLabel]: [...members] },
          },
        }
      : {},
  };
  writeFileSync(deploymentPath, `${JSON.stringify(deployment)}\n`);
  copyFileSync(CATALOG, catalogPath);
  const moduleBase = options.moduleDir ?? "modules";
  for (const resourceType of members) {
    const directory = path.resolve(workspace, moduleBase, resourceType);
    mkdirSync(directory, { recursive: true });
    writeFileSync(path.join(directory, "main.tf"), `# ${resourceType}\n`);
  }
  const receipts = members.map((resourceType) => receipt({
    workspace,
    resourceType,
    rootLabel,
    rootMembers: members,
  }));
  return {
    root: lexical,
    workspace,
    deploymentPath,
    catalogPath,
    rootLabel,
    members,
    receipts,
  };
}

function compileOptions(value: Fixture) {
  return {
    workspace: value.workspace,
    deploymentPath: value.deploymentPath,
    catalogPath: value.catalogPath,
    profile: ZCC_PLAN_ROOT_PREPARATION_PROFILE,
    mode: "bootstrap" as const,
    tenant: TENANT,
    resourceType: value.members[0] ?? "zcc_device_cleanup",
    backend: "local" as const,
    materializations: value.receipts,
  };
}

function processRequest(value: Fixture): CompilePlanRootPreparationProcessRequest {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "plan-root-preparation",
    operation: "compile_plan_root_preparation",
    context: {
      workspace: value.workspace,
      deployment: "deployment.json",
      root_catalog: "root-catalog.json",
    },
    input: {
      profile: ZCC_PLAN_ROOT_PREPARATION_PROFILE,
      mode: "bootstrap",
      tenant: TENANT,
      resource_type: value.members[0] ?? "zcc_device_cleanup",
      backend: "local",
      materializations: value.receipts,
    },
  };
}

async function failureCode(action: () => Promise<unknown>): Promise<string> {
  try {
    await action();
  } catch (error: unknown) {
    assert.equal(error instanceof ProcessFailure, true);
    return (error as ProcessFailure).code;
  }
  assert.fail("expected operation failure");
}

test("compiles one exact singleton candidate without effects", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  assert.equal(
    validateZccPlanRootPreparation(outcome.result),
    true,
    JSON.stringify(validateZccPlanRootPreparation.errors),
  );
  const request = processRequest(value);
  assert.equal(validateProcessRequest(request), true, JSON.stringify(validateProcessRequest.errors));
  assert.deepEqual(zccPlanRootPreparationOperationResultErrors(request, outcome.result), []);
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: request.request_id,
    operation: request.operation,
    status: "ok",
    diagnostics: outcome.diagnostics,
    result: outcome.result,
    error: null,
  }), true, JSON.stringify(validateProcessResponse.errors));
  assert.equal(outcome.result.status, "candidate_only");
  assert.equal(outcome.result.qualification.plan, "not_performed");
  assert.equal(outcome.result.renderer.terraform_executed, false);
  assert.deepEqual(outcome.result.root.members, value.members);
  assert.equal(outcome.result.root.artifacts.staged_imports.length, 1);
  assert.equal(outcome.result.modules[0]?.module_provenance, "observed_unqualified");
  assert.equal(outcome.result.backend_marker.desired, null);
});

test("direct renderer has an independent exact singleton byte fixture", () => {
  assert.equal(renderZccPlanRootMain({
    tenant: "fixture",
    label: "zcc_device_cleanup",
    members: ["zcc_device_cleanup"],
    backend: "local",
    moduleSources: new Map([["zcc_device_cleanup", "../../modules/zcc_device_cleanup"]]),
  }), [
    "# GENERATED by engine.gen_env for tenant 'fixture' — do not edit.",
    "# Regenerate: make gen-env TENANT=fixture",
    "",
    "terraform {",
    '  required_version = ">= 1.5"',
    "  required_providers {",
    "    zcc = {",
    '      source = "zscaler/zcc"',
    "    }",
    "  }",
    "  # local state — opt into remote state with",
    "  # make gen-env TENANT=fixture BACKEND=azurerm",
    "}",
    "",
    'provider "zcc" {',
    "  # credentials via provider environment variables",
    "}",
    "",
    'variable "items" {',
    "  # opaque at the root; the module enforces the strict type.",
    "  type = any",
    "}",
    "",
    'module "zcc_device_cleanup" {',
    '  source = "../../modules/zcc_device_cleanup"',
    "  items  = var.items",
    "}",
    "",
  ].join("\n"));
});

test("expands one selector only with complete sorted exact-five receipts", async (t) => {
  const value = fixture({ grouped: true });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation({
    ...compileOptions(value),
    backend: "azurerm",
  });
  assert.equal(
    validateZccPlanRootPreparation(outcome.result),
    true,
    JSON.stringify(validateZccPlanRootPreparation.errors),
  );
  assert.deepEqual(outcome.result.root.members, ZCC_PLAN_ROOT_RESOURCE_TYPES);
  assert.equal(outcome.result.sources.length, 5);
  assert.equal(outcome.result.modules.length, 5);
  assert.equal(outcome.result.root.artifacts.staged_imports.length, 5);
  assert.equal(outcome.result.backend_marker.observed_state, "absent");
  assert.equal(outcome.result.backend_marker.desired?.content, "azurerm\n");
  assert.equal(outcome.diagnostics[0]?.code, "WHOLE_ROOT_SELECTION");
});

test("rejects malformed direct callers before filesystem work", async () => {
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      workspace: 1,
    } as never);
  }), "INVALID_PLAN_ROOT_INPUT");
  const proxy = new Proxy({}, {});
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation(proxy as never);
  }), "INVALID_PLAN_ROOT_INPUT");
  const accessor = Object.defineProperty({}, "workspace", {
    enumerable: true,
    get: () => "/should-not-be-read",
  });
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation(accessor as never);
  }), "INVALID_PLAN_ROOT_INPUT");
  const direct = {
    workspace: "/not-inspected",
    deploymentPath: "/not-inspected/deployment.json",
    catalogPath: "/not-inspected/catalog.json",
    profile: ZCC_PLAN_ROOT_PREPARATION_PROFILE,
    mode: "bootstrap",
    tenant: TENANT,
    resourceType: "zcc_device_cleanup",
    backend: "local",
  } as const;
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...direct,
      materializations: new Array(100_000).fill(null),
    } as never);
  }), "INVALID_PLAN_ROOT_INPUT");
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...direct,
      materializations: [{ wide: new Array(513).fill(null) }],
    } as never);
  }), "INVALID_PLAN_ROOT_INPUT");
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...direct,
      materializations: [{ value: "x".repeat((1024 * 1024) + 1) }],
    } as never);
  }), "INVALID_PLAN_ROOT_INPUT");
});

test("uses backend-marker refusal taxonomy for a local marker", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const target = path.join(value.workspace, `envs/${TENANT}/.backend`);
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, "azurerm\n");
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation(compileOptions(value));
  }), "PLAN_ROOT_BACKEND_MARKER_MISMATCH");
});

test("preflights per-file and grouped staged-import byte budgets", async (t) => {
  const singleton = fixture();
  t.after(() => rmSync(singleton.root, { recursive: true, force: true }));
  const oversized: any = structuredClone(singleton.receipts);
  const singletonImports = oversized[0].verification.parity.artifacts.imports;
  singletonImports.candidate.size_bytes = MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES + 1;
  singletonImports.reference.size_bytes = MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES + 1;
  assert.equal(validateZccAdoptionArtifactMaterialization(oversized[0]), true);
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...compileOptions(singleton),
      materializations: oversized,
    });
  }), "PLAN_ROOT_CANDIDATE_TOO_LARGE");

  const grouped = fixture({ grouped: true });
  t.after(() => rmSync(grouped.root, { recursive: true, force: true }));
  const aggregate: any = structuredClone(grouped.receipts);
  const each = Math.floor(MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES / aggregate.length) + 1;
  for (const item of aggregate) {
    const imports = item.verification.parity.artifacts.imports;
    imports.candidate.size_bytes = each;
    imports.reference.size_bytes = each;
    assert.equal(validateZccAdoptionArtifactMaterialization(item), true);
  }
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...compileOptions(grouped),
      materializations: aggregate,
    });
  }), "PLAN_ROOT_CANDIDATE_TOO_LARGE");
});

test("rejects root expression bindings and stale staged moves", async (t) => {
  for (const relativePath of [
    `envs/${TENANT}/zcc_device_cleanup/expression_bindings.tf`,
    `envs/${TENANT}/zcc_device_cleanup/zcc_device_cleanup_moves.tf`,
  ]) {
    const value = fixture();
    t.after(() => rmSync(value.root, { recursive: true, force: true }));
    const target = path.join(value.workspace, relativePath);
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(target, "# stale\n");
    assert.equal(await failureCode(async () => {
      await compileZccPlanRootPreparation(compileOptions(value));
    }), "UNSUPPORTED_PLAN_ROOT_SIDECAR", relativePath);
  }
});

test("refuses absolute deployment overlays instead of emitting checkout-specific paths", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  writeFileSync(value.deploymentPath, `${JSON.stringify({
    overlay: path.join(value.workspace, "overlay"),
    roots: {},
  })}\n`);
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation(compileOptions(value));
  }), "UNSUPPORTED_PLAN_ROOT_OVERLAY");
});

test("final synchronous CAS catches mutation after every async recheck", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const original = readFileSync(value.deploymentPath, "utf8");
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...compileOptions(value),
      hooks: {
        afterAsyncRechecks: () => {
          writeFileSync(value.deploymentPath, `${original} `);
        },
      },
    });
  }), "PLAN_ROOT_INPUT_CHANGED");
});

test("mutation and byte restoration inside the binding window is rejected", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const imports = path.join(
    value.workspace,
    `imports/${TENANT}/zcc_device_cleanup_imports.tf`,
  );
  const original = readFileSync(imports, "utf8");
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation({
      ...compileOptions(value),
      hooks: {
        afterInputsBound: () => {
          writeFileSync(imports, "# changed\n");
          writeFileSync(imports, original);
        },
      },
    });
  }), "PLAN_ROOT_INPUT_CHANGED");
});

test("HCL-sensitive module source paths fail closed", async (t) => {
  const value = fixture({ moduleDir: "modules-${unsafe}" });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  assert.equal(await failureCode(async () => {
    await compileZccPlanRootPreparation(compileOptions(value));
  }), "UNSUPPORTED_PLAN_ROOT_MODULE_SOURCE");
});

test("frozen profile refuses every deployment, receipt, marker, and content escape", async (t) => {
  const cases: readonly [string, (value: Fixture) => Promise<string>][] = [
    ["HCL tfvars", async (value) => {
      writeFileSync(value.deploymentPath, `${JSON.stringify({
        overlay: ".",
        tfvars_format: "hcl",
        roots: {},
      })}\n`);
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["reference bindings", async (value) => {
      writeFileSync(value.deploymentPath, `${JSON.stringify({
        overlay: ".",
        roots: { zcc: { bind_references: true } },
      })}\n`);
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["unsupported root member", async (value) => {
      writeFileSync(value.deploymentPath, `${JSON.stringify({
        overlay: ".",
        roots: {
          zcc: {
            groups: {
              unsupported: ["zcc_device_cleanup", "zcc_notification_template"],
            },
          },
        },
      })}\n`);
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["operator bindings", async (value) => {
      const target = path.join(value.workspace, `config/${TENANT}/zcc_device_cleanup.expressions.json`);
      writeFileSync(target, "[]\n");
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["generated bindings", async (value) => {
      const target = path.join(value.workspace, `config/${TENANT}/zcc_device_cleanup.generated.expressions.json`);
      writeFileSync(target, "[]\n");
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["HCL alternate", async (value) => {
      const target = path.join(value.workspace, `config/${TENANT}/zcc_device_cleanup.auto.tfvars`);
      writeFileSync(target, "items = {}\n");
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["local backend marker", async (value) => {
      const target = path.join(value.workspace, `envs/${TENANT}/.backend`);
      mkdirSync(path.dirname(target), { recursive: true });
      writeFileSync(target, "azurerm\n");
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["foreign azurerm marker", async (value) => {
      const target = path.join(value.workspace, `envs/${TENANT}/.backend`);
      mkdirSync(path.dirname(target), { recursive: true });
      writeFileSync(target, "local\n");
      return failureCode(() => compileZccPlanRootPreparation({
        ...compileOptions(value),
        backend: "azurerm",
      }));
    }],
    ["materialized digest mismatch", async (value) => {
      writeFileSync(
        path.join(value.workspace, `imports/${TENANT}/zcc_device_cleanup_imports.tf`),
        "# foreign\n",
      );
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["receipt root mismatch", async (value) => {
      const receipts: any = structuredClone(value.receipts);
      receipts[0].verification.root.label = "other";
      receipts[0].verification.root.variable_name = "zcc_device_cleanup_items";
      assert.equal(validateZccAdoptionArtifactMaterialization(receipts[0]), true);
      return failureCode(() => compileZccPlanRootPreparation({
        ...compileOptions(value),
        materializations: receipts,
      }));
    }],
    ["catalog mismatch", async (value) => {
      const catalog = JSON.parse(readFileSync(value.catalogPath, "utf8"));
      catalog.sources_sha256 = "0".repeat(64);
      writeFileSync(value.catalogPath, `${JSON.stringify(catalog)}\n`);
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
    ["missing module", async (value) => {
      rmSync(path.join(value.workspace, "modules/zcc_device_cleanup"), {
        recursive: true,
        force: true,
      });
      return failureCode(() => compileZccPlanRootPreparation(compileOptions(value)));
    }],
  ];
  for (const [name, run] of cases) {
    const value = fixture();
    t.after(() => rmSync(value.root, { recursive: true, force: true }));
    assert.notEqual(await run(value), "", name);
  }
  const grouped = fixture({ grouped: true });
  t.after(() => rmSync(grouped.root, { recursive: true, force: true }));
  assert.equal(await failureCode(() => compileZccPlanRootPreparation({
    ...compileOptions(grouped),
    materializations: grouped.receipts.slice(0, -1),
  })), "PLAN_ROOT_MATERIALIZATION_COVERAGE_MISMATCH");
});

test("process schema refuses aliases, products, refresh/raw receipts, and implicit backend", () => {
  const value = fixture();
  try {
    const base: any = processRequest(value);
    for (const mutate of [
      (request: any) => { request.input.resource_type = "zcc/device_cleanup"; },
      (request: any) => { request.input.resource_type = "zcc"; },
      (request: any) => { request.input.backend = undefined; delete request.input.backend; },
      (request: any) => { request.input.materializations[0].mode = "refresh"; },
      (request: any) => {
        request.input.materializations = [{
          kind: "infrawright.zcc_pull_refresh_materialization",
          schema_version: 1,
        }];
      },
    ]) {
      const request = structuredClone(base);
      mutate(request);
      assert.equal(validateProcessRequest(request), false);
    }
  } finally {
    rmSync(value.root, { recursive: true, force: true });
  }
});

test("same-directory module source preserves Python's ./dot edge", async (t) => {
  const value = fixture({ moduleDir: `envs/${TENANT}` });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  assert.equal(outcome.result.modules[0]?.source, "./.");
  assert.equal(outcome.result.root.artifacts.main_tf.content.includes('source = "./."'), true);
  assert.equal(validateZccPlanRootPreparation(outcome.result), true);
});

test("standalone semantics reject every redundant candidate tamper", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  const cases: readonly [string, (candidate: any) => void][] = [
    ["main bytes", (candidate) => { candidate.root.artifacts.main_tf.content += "# foreign\n"; }],
    ["main media type", (candidate) => { candidate.root.artifacts.main_tf.media_type = "text/plain"; }],
    ["staged media type", (candidate) => {
      candidate.root.artifacts.staged_imports[0].media_type = "text/plain";
    }],
    ["sidecar set", (candidate) => { candidate.absent_sidecars.pop(); }],
    ["resource roots", (candidate) => { candidate.topology.resource_roots.foreign = candidate.root.label; }],
    ["module source", (candidate) => { candidate.modules[0].source = "./foreign"; }],
    ["materialized path", (candidate) => {
      candidate.sources[0].materialized_artifacts.tfvars.path = "config/foreign.tfvars.json";
    }],
    ["summary", (candidate) => { candidate.summary.members = 2; }],
  ];
  for (const [name, mutate] of cases) {
    const candidate = structuredClone(outcome.result);
    mutate(candidate);
    assert.equal(validateZccPlanRootPreparation(candidate), false, name);
  }

  const foreignMarker: any = structuredClone(outcome.result);
  foreignMarker.topology.directories.envs = "foreign-envs";
  foreignMarker.backend_marker.path = "foreign-envs/.backend";
  assert.equal(validateZccPlanRootPreparation(foreignMarker), false);
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "forged-plan-root",
    operation: "compile_plan_root_preparation",
    status: "ok",
    diagnostics: [],
    result: foreignMarker,
    error: null,
  }), false);

  const nullTopology: any = structuredClone(outcome.result);
  nullTopology.topology.tenant = null;
  nullTopology.topology.directories = null;
  nullTopology.topology.roots[0].env_dir = null;
  assert.equal(validateZccPlanRootPreparation(nullTopology), false);
  assert.equal(
    validateZccPlanRootPreparation.errors?.some((error) => {
      return (error.params as { rule?: string }).rule === "tenant_directory_join";
    }),
    true,
  );
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "null-topology",
    operation: "compile_plan_root_preparation",
    status: "ok",
    diagnostics: [],
    result: nullTopology,
    error: null,
  }), false);

  const overBudget: any = structuredClone(nullTopology);
  overBudget.sources[0].provider_observed_source.path = "x".repeat(
    MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES + 1,
  );
  assert.equal(validateZccPlanRootPreparation(overBudget), false);
  assert.equal(
    validateZccPlanRootPreparation.errors?.some((error) => {
      return (error.params as { rule?: string }).rule === "candidate_byte_budget";
    }),
    true,
  );
  assert.equal(
    validateZccPlanRootPreparation.errors?.some((error) => {
      return (error.params as { rule?: string }).rule === "tenant_directory_join";
    }),
    true,
  );

  const request = processRequest(value);
  const echoedSource: any = structuredClone(outcome.result);
  echoedSource.sources[0].provider_observed_source.sha256 = "f".repeat(64);
  assert.equal(validateZccPlanRootPreparation(echoedSource), true);
  assert.equal(
    zccPlanRootPreparationOperationResultErrors(request, echoedSource).some(
      (error) => error.instancePath === "/sources",
    ),
    true,
  );
  const repointedControl: any = structuredClone(outcome.result);
  repointedControl.controls.deployment.path = "foreign.json";
  assert.equal(
    zccPlanRootPreparationOperationResultErrors(request, repointedControl).some(
      (error) => error.instancePath === "/controls",
    ),
    true,
  );
});

test("request semantics reject reordered or incomplete receipt joins", async (t) => {
  const value = fixture({ grouped: true });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const reversed: any = structuredClone(processRequest(value));
  reversed.input.materializations.reverse();
  assert.equal(validateProcessRequest(reversed), false);
  const missingSelected: any = structuredClone(processRequest(value));
  missingSelected.input.materializations.shift();
  assert.equal(validateProcessRequest(missingSelected), false);
});

test("operation join rejects a coherently relabeled root against retained receipts", async (t) => {
  const value = fixture({ grouped: true });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  const candidate: any = structuredClone(outcome.result);
  const oldLabel = candidate.root.label;
  const newLabel = "zcc_bundle_relabelled";
  const oldEnv = candidate.root.env_dir;
  const newEnv = path.posix.join(path.posix.dirname(oldEnv), newLabel);
  candidate.root.label = newLabel;
  candidate.root.env_dir = newEnv;
  candidate.topology.roots[0].label = newLabel;
  candidate.topology.roots[0].env_dir = newEnv;
  for (const member of candidate.root.members) {
    candidate.topology.resource_roots[member] = newLabel;
  }
  candidate.absent_sidecars = candidate.absent_sidecars.map((item: string) => {
    return item.startsWith(`${oldEnv}/`) ? `${newEnv}/${item.slice(oldEnv.length + 1)}` : item;
  }).sort();
  const moduleSources = new Map<string, string>(candidate.modules.map((module: any) => [
    module.resource_type,
    module.source,
  ]));
  candidate.root.artifacts.main_tf = zccPlanRootTextArtifact(
    `${newEnv}/main.tf`,
    "text/x-hcl",
    renderZccPlanRootMain({
      tenant: candidate.tenant,
      label: newLabel,
      members: candidate.root.members,
      backend: candidate.backend,
      moduleSources,
    }),
  );
  candidate.root.artifacts.staged_imports = candidate.root.artifacts.staged_imports.map(
    (artifact: any) => ({ ...artifact, path: `${newEnv}/${path.posix.basename(artifact.path)}` }),
  );
  assert.equal(validateZccPlanRootPreparation(candidate), true, JSON.stringify(validateZccPlanRootPreparation.errors));
  assert.equal(
    zccPlanRootPreparationOperationResultErrors(processRequest(value), candidate).some(
      (error) => error.instancePath === "/root",
    ),
    true,
  );
  assert.notEqual(oldLabel, newLabel);
});

test("digest size schema uses the 32 MiB per-file boundary", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  const boundary: any = structuredClone(outcome.result);
  boundary.controls.deployment.size_bytes = 33_554_432;
  assert.equal(validateZccPlanRootPreparation(boundary), true);
  boundary.controls.deployment.size_bytes += 1;
  assert.equal(validateZccPlanRootPreparation(boundary), false);
});

function invokeProcess(executable: string, request: unknown) {
  const environment = { ...process.env };
  delete environment.INFRAWRIGHT_TERRAFORM_EXECUTABLE;
  delete environment.INFRAWRIGHT_MATERIALIZE_OUTPUT_ROOT;
  return spawnSync(process.execPath, [executable], {
    cwd: REPO,
    env: environment,
    input: JSON.stringify(request),
    encoding: "utf8",
  });
}

test("public process exposes success and redacted refusal exit contracts", (t) => {
  const value = fixture({ grouped: true });
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const request = processRequest(value);
  const success = invokeProcess(PROCESS_MAIN, request);
  assert.equal(success.status, 0, success.stderr);
  const response = JSON.parse(success.stdout);
  assert.equal(response.operation, "compile_plan_root_preparation");
  assert.equal(response.result.root.members.length, 5);
  assert.equal(validateProcessResponse(response), true, JSON.stringify(validateProcessResponse.errors));

  const secret = "plan-root-private-request-secret";
  const malformed: any = structuredClone(request);
  malformed.input.secret = secret;
  const refused = invokeProcess(PROCESS_MAIN, malformed);
  assert.equal(refused.status, 2, refused.stderr);
  assert.equal(refused.stdout.includes(secret), false);
  assert.equal(JSON.parse(refused.stdout).error.code, "INVALID_REQUEST");
});

test("response-size refusal preserves validated request identity", async (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const outcome = await compileZccPlanRootPreparation(compileOptions(value));
  const success: any = {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "bounded-plan-root",
    operation: "compile_plan_root_preparation",
    status: "ok",
    diagnostics: outcome.diagnostics,
    result: outcome.result,
    error: null,
  };
  assert.equal(validateProcessResponse(success), true);
  const prepared = prepareProcessResponseForEmission(success, 1);
  assert.equal(prepared.oversized, true);
  assert.equal(prepared.response.request_id, "bounded-plan-root");
  assert.equal(prepared.response.operation, "compile_plan_root_preparation");
  assert.equal(prepared.response.status, "error");
  assert.equal(prepared.response.error?.code, "PROCESS_RESPONSE_TOO_LARGE");
  assert.equal(validateProcessResponse(prepared.response), true);
});

test("production parent bundle reaches the read-only compiler", (t) => {
  const value = fixture();
  t.after(() => rmSync(value.root, { recursive: true, force: true }));
  const build = spawnSync(process.execPath, ["scripts/build-node.mjs"], {
    cwd: REPO,
    encoding: "utf8",
  });
  assert.equal(build.status, 0, build.stderr);
  const execution = invokeProcess(PROCESS_BUNDLE, processRequest(value));
  assert.equal(execution.status, 0, execution.stderr);
  const response = JSON.parse(execution.stdout);
  assert.equal(response.operation, "compile_plan_root_preparation");
  assert.equal(response.result.renderer.terraform_executed, false);
});

test("main.tf and non-state-aware staged imports exactly match Python", async (t) => {
  const python = spawnSync(PYTHON_ORACLE, ["--version"], { encoding: "utf8" });
  const terraform = spawnSync("terraform", ["version", "-json"], { encoding: "utf8" });
  if (python.error !== undefined || terraform.error !== undefined) {
    t.skip("Python or Terraform is unavailable");
    return;
  }
  let terraformVersion = "";
  try {
    terraformVersion = JSON.parse(terraform.stdout).terraform_version;
  } catch {
    t.skip("Terraform version evidence is unavailable");
    return;
  }
  if (terraformVersion !== "1.15.4") {
    t.skip(`Terraform ${terraformVersion} is not the exact 1.15.4 reference`);
    return;
  }
  const cases = [
    { grouped: false, backend: "local" as const },
    { grouped: false, backend: "azurerm" as const, moduleDir: "module trees" },
    { grouped: true, backend: "local" as const, moduleDir: "components" },
    { grouped: true, backend: "azurerm" as const },
  ];
  for (const item of cases) {
    await t.test(
      `${item.grouped ? "grouped" : "singleton"}-${item.backend}-${item.moduleDir ?? "default"}`,
      async (subtest) => {
        const value = fixture({
          grouped: item.grouped,
          ...(item.moduleDir === undefined ? {} : { moduleDir: item.moduleDir }),
        });
        subtest.after(() => rmSync(value.root, { recursive: true, force: true }));
        const environment = {
          ...process.env,
          INFRAWRIGHT_DEPLOYMENT: value.deploymentPath,
          PYTHONPATH: process.env.PYTHONPATH === undefined
            ? REPO
            : `${REPO}${path.delimiter}${process.env.PYTHONPATH}`,
        };
        const selector = value.members[0] ?? "zcc_device_cleanup";
        const generation = spawnSync(PYTHON_ORACLE, [
          "-m",
          "engine.gen_env",
          TENANT,
          ...(item.backend === "azurerm" ? ["--backend", "azurerm"] : []),
          selector,
        ], {
          cwd: value.workspace,
          env: environment,
          encoding: "utf8",
        });
        assert.equal(generation.status, 0, generation.stderr);
        const staging = spawnSync(PYTHON_ORACLE, [
          "-m",
          "engine.ops",
          "stage-imports",
          "--tenant",
          TENANT,
          selector,
        ], {
          cwd: value.workspace,
          env: environment,
          encoding: "utf8",
        });
        assert.equal(staging.status, 0, staging.stderr);
        const outcome = await compileZccPlanRootPreparation({
          ...compileOptions(value),
          backend: item.backend,
        });
        const envDir = path.join(value.workspace, `envs/${TENANT}/${value.rootLabel}`);
        assert.equal(
          outcome.result.root.artifacts.main_tf.content,
          readFileSync(path.join(envDir, "main.tf"), "utf8"),
        );
        for (const artifact of outcome.result.root.artifacts.staged_imports) {
          assert.equal(
            artifact.content,
            readFileSync(path.join(value.workspace, artifact.path), "utf8"),
            artifact.path,
          );
        }
      },
    );
  }
});
