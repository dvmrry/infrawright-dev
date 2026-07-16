import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { renderEnvironmentSmokeTest } from "../node-src/domain/environment-generator.js";
import {
  parseExpressionBindings,
  renderExpressionBindingsHcl,
} from "../node-src/domain/expression-bindings.js";
import { loadedRootTopology } from "../node-src/domain/roots.js";
import { validateAssessmentPlan } from "../node-src/domain/plan-contract.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

const TERRAFORM = process.env.TF || "terraform";
const ROOT = process.cwd();
const PACKS_ROOT = path.resolve(
  process.env.INFRAWRIGHT_PACKS?.trim() || path.join(ROOT, "packs"),
);
const PACK_PROFILE = path.resolve(
  process.env.PACK_PROFILE?.trim() || path.join(ROOT, "packsets", "full.json"),
);
const PACK_CATALOG = path.resolve(
  process.env.PACK_CATALOG?.trim() || path.join(ROOT, "packsets", "full.json"),
);

function terraform(directory: string, arguments_: readonly string[]): string {
  const result = spawnSync(TERRAFORM, arguments_, {
    cwd: directory,
    encoding: "utf8",
    env: { ...process.env, TF_IN_AUTOMATION: "1" },
  });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  return result.stdout;
}

test("real Terraform emits the reference-output shape accepted by assessment", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-reference-output-plan-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const moduleDirectory = path.join(workspace, "terraform_data");
  await mkdir(moduleDirectory, { recursive: true });
  await writeFile(path.join(moduleDirectory, "main.tf"), [
    "variable \"items\" { type = map(any) }",
    'resource "terraform_data" "this" {',
    "  for_each = var.items",
    "  input    = each.value",
    "}",
    'output "items" { value = terraform_data.this }',
    "",
  ].join("\n"), "utf8");
  const rootConfiguration = (outputExpression: string | null): string => [
    'terraform { required_version = ">= 1.8" }',
    'module "terraform_data" {',
    '  source = "./terraform_data"',
    "  items  = { one = null }",
    "}",
    "import {",
    '  to = module.terraform_data.terraform_data.this["one"]',
    '  id = "provider-id"',
    "}",
    ...(outputExpression === null
      ? []
      : [
          'output "infrawright_reference_ids" {',
          "  sensitive = true",
          "  value = {",
          `    terraform_data = ${outputExpression}`,
          "  }",
          "}",
        ]),
    "",
  ].join("\n");
  await writeFile(path.join(workspace, "main.tf"), rootConfiguration(
    "{ for key, item in module.terraform_data.items : key => item.id }",
  ), "utf8");
  terraform(workspace, ["init", "-backend=false", "-input=false"]);
  terraform(workspace, ["plan", "-out=tfplan", "-input=false"]);
  const plan = JSON.parse(terraform(workspace, ["show", "-json", "tfplan"])) as unknown;
  assert.doesNotThrow(() => validateAssessmentPlan(plan, {
    referenceOutputTypes: ["terraform_data"],
  }));
  assert.throws(() => validateAssessmentPlan(plan));

  terraform(workspace, ["apply", "-input=false", "tfplan"]);
  terraform(workspace, ["plan", "-out=second.tfplan", "-input=false"]);
  const second = JSON.parse(
    terraform(workspace, ["show", "-json", "second.tfplan"]),
  ) as unknown;
  assert.doesNotThrow(() => validateAssessmentPlan(second, {
    referenceOutputTypes: ["terraform_data"],
  }));

  await writeFile(
    path.join(workspace, "main.tf"),
    rootConfiguration('{ for key, item in module.terraform_data.items : key => "WRONG" }'),
    "utf8",
  );
  terraform(workspace, ["apply", "-auto-approve", "-input=false"]);
  terraform(workspace, ["plan", "-out=wrong.tfplan", "-input=false"]);
  const wrong = JSON.parse(
    terraform(workspace, ["show", "-json", "wrong.tfplan"]),
  ) as unknown;
  assert.throws(() => validateAssessmentPlan(wrong, {
    referenceOutputTypes: ["terraform_data"],
  }));

  await writeFile(path.join(workspace, "main.tf"), rootConfiguration(null), "utf8");
  terraform(workspace, ["plan", "-out=missing.tfplan", "-input=false"]);
  const missing = JSON.parse(
    terraform(workspace, ["show", "-json", "missing.tfplan"]),
  ) as unknown;
  assert.throws(() => validateAssessmentPlan(missing, {
    referenceOutputTypes: ["terraform_data"],
  }));
});

test("real Terraform empty referent output retains configuration authority", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-empty-reference-plan-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const moduleDirectory = path.join(workspace, "terraform_data");
  await mkdir(moduleDirectory, { recursive: true });
  await writeFile(path.join(moduleDirectory, "main.tf"), [
    "variable \"items\" { type = map(any) }",
    'resource "terraform_data" "this" {',
    "  for_each = var.items",
    "  input    = each.value",
    "}",
    'output "items" { value = terraform_data.this }',
    "",
  ].join("\n"), "utf8");
  await writeFile(path.join(workspace, "main.tf"), [
    'terraform { required_version = ">= 1.8" }',
    'module "terraform_data" {',
    '  source = "./terraform_data"',
    "  items  = {}",
    "}",
    'output "infrawright_reference_ids" {',
    "  sensitive = true",
    "  value = {",
    "    terraform_data = { for key, item in module.terraform_data.items : key => item.id }",
    "  }",
    "}",
    "",
  ].join("\n"), "utf8");
  terraform(workspace, ["init", "-backend=false", "-input=false"]);
  terraform(workspace, ["plan", "-out=tfplan", "-input=false"]);
  const plan = JSON.parse(terraform(workspace, ["show", "-json", "tfplan"])) as unknown;
  assert.doesNotThrow(() => validateAssessmentPlan(plan, {
    referenceOutputTypes: ["terraform_data"],
  }));
  assert.throws(() => validateAssessmentPlan(plan, {
    referenceOutputTypes: ["wrong_resource"],
  }));
});

test("local referent state is consumable before the referrer plan and converges", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-cross-state-terraform-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const referent = path.join(workspace, "referent");
  const referrer = path.join(workspace, "referrer");
  await mkdir(referent, { recursive: true });
  await mkdir(referrer, { recursive: true });
  await writeFile(path.join(referent, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'resource "terraform_data" "items" {',
    '  for_each = { segment_one = "provider-id-1" }',
    '  input    = each.value',
    '}',
    'output "infrawright_reference_ids" {',
    '  sensitive = true',
    '  value = {',
    '    zpa_segment_group = { for key, item in terraform_data.items : key => item.output }',
    '  }',
    '}',
    '',
  ].join("\n"), "utf8");
  terraform(referent, ["init", "-backend=false", "-input=false"]);
  terraform(referent, ["apply", "-auto-approve", "-input=false"]);

  const remoteBinding = parseExpressionBindings({
    resources: {
      "sample_resource.example": {
        "nested[0].id": {
          expression: 'data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]',
        },
      },
    },
  }, "sample_resource");
  await writeFile(path.join(referrer, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'variable "items" { type = any }',
    'data "terraform_remote_state" "zpa_segment_group" {',
    '  backend = "local"',
    '  config = {',
    `    path = ${JSON.stringify(path.join(referent, "terraform.tfstate"))}`,
    '  }',
    '}',
    renderExpressionBindingsHcl(remoteBinding),
    'resource "terraform_data" "consumer" {',
    '  input = local.infrawright_expression_bound_items["example"].nested',
    '}',
    '',
  ].join("\n"), "utf8");
  await writeFile(path.join(referrer, "terraform.tfvars.json"), JSON.stringify({
    items: { example: { nested: [{ id: "literal-id", sibling: "preserved" }] } },
  }), "utf8");
  terraform(referrer, ["init", "-backend=false", "-input=false"]);
  const first = terraform(referrer, ["plan", "-out=tfplan", "-input=false"]);
  assert.match(first, /1 to add/u);
  terraform(referrer, ["apply", "-auto-approve", "-input=false", "tfplan"]);
  const second = terraform(referrer, ["plan", "-detailed-exitcode", "-input=false"]);
  assert.match(second, /No changes/u);
});

test("generated azurerm smoke variables satisfy the overridden remote-state reader", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-cross-state-smoke-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const deployment = {
    overlay: ".",
    roots: { zpa: { cross_state_references: true } },
  } as const;
  const root = await loadPackRoot({
    packsRoot: PACKS_ROOT,
    profilePath: PACK_PROFILE,
    catalogPath: PACK_CATALOG,
  });
  const topology = loadedRootTopology({
    deployment,
    root,
    selectors: ["zpa_application_segment"],
    tenant: "tenant",
  }).topology;
  await writeFile(path.join(workspace, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'variable "items" { type = any }',
    'variable "infrawright_remote_state_backend_config" {',
    '  type      = any',
    '  sensitive = true',
    '}',
    'data "terraform_remote_state" "zpa_segment_group" {',
    '  backend = "azurerm"',
    '  config = merge(var.infrawright_remote_state_backend_config, {',
    '    key = "tenant/zpa_segment_group.tfstate"',
    '  })',
    '}',
    '',
  ].join("\n"), "utf8");
  const smoke = renderEnvironmentSmokeTest({
    backend: "azurerm",
    configFormat: "hcl",
    deployment,
    environmentDirectory: workspace,
    hasConfig: new Map([["zpa_application_segment", false]]),
    label: "zpa_application_segment",
    members: ["zpa_application_segment"],
    remoteStateReferences: [{
      key: "segment_one",
      resourceType: "zpa_segment_group",
      root: "zpa_segment_group",
    }],
    root,
    tenant: "tenant",
    topology,
  }).replace('mock_provider "zpa" {}\n\n', "");
  await mkdir(path.join(workspace, "tests"), { recursive: true });
  await writeFile(path.join(workspace, "tests", "smoke.tftest.hcl"), smoke, "utf8");
  terraform(workspace, ["init", "-backend=false", "-input=false"]);
  const result = terraform(workspace, ["test", "-no-color"]);
  assert.match(result, /Success! 1 passed, 0 failed/u);
});

test("generated exact-index bindings preserve list elements and converge", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-index-binding-terraform-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const bindings = parseExpressionBindings({
    resources: {
      "sample_resource.example": {
        "nested[0].id": { expression: "var.first_bound_id" },
        "nested[1].id": { expression: "var.bound_id" },
      },
    },
  }, "sample_resource");
  await writeFile(path.join(workspace, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'variable "items" { type = any }',
    renderExpressionBindingsHcl(bindings),
    'resource "terraform_data" "consumer" {',
    '  input = local.infrawright_expression_bound_items["example"].nested',
    '}',
    '',
  ].join("\n"), "utf8");
  await writeFile(path.join(workspace, "terraform.tfvars.json"), JSON.stringify({
    bound_id: "resolved-id",
    first_bound_id: "resolved-first-id",
    items: {
      example: {
        nested: [
          { id: "first", sibling: "kept-first" },
          { id: "literal-id", sibling: "kept-second" },
          { id: "third", sibling: "kept-third" },
        ],
      },
    },
  }), "utf8");
  terraform(workspace, ["init", "-backend=false", "-input=false"]);
  terraform(workspace, ["apply", "-auto-approve", "-input=false"]);
  const state = JSON.parse(terraform(workspace, ["show", "-json"])) as {
    readonly values?: { readonly root_module?: { readonly resources?: readonly [{ readonly values?: { readonly input?: unknown } }] } };
  };
  assert.deepEqual(state.values?.root_module?.resources?.[0]?.values?.input, [
    { id: "resolved-first-id", sibling: "kept-first" },
    { id: "resolved-id", sibling: "kept-second" },
    { id: "third", sibling: "kept-third" },
  ]);
  const second = terraform(workspace, ["plan", "-detailed-exitcode", "-input=false"]);
  assert.match(second, /No changes/u);
});

test("exact-index bindings satisfy provider-shaped list objects with set-valued IDs", async (context) => {
  const workspace = await mkdtemp(path.join(os.tmpdir(), "infrawright-index-binding-schema-"));
  context.after(() => rm(workspace, { force: true, recursive: true }));
  const ids = path.join(workspace, "ids");
  const consumer = path.join(workspace, "consumer");
  await mkdir(ids, { recursive: true });
  await mkdir(consumer, { recursive: true });
  await writeFile(path.join(ids, "main.tf"), [
    'output "first" { value = "resolved-first-id" }',
    'output "second" { value = "resolved-second-id" }',
    '',
  ].join("\n"), "utf8");
  await writeFile(path.join(consumer, "main.tf"), [
    'variable "items" {',
    '  type = map(object({',
    '    nested = list(object({',
    '      id      = set(string)',
    '      sibling = string',
    '    }))',
    '  }))',
    '}',
    'output "items" { value = var.items }',
    '',
  ].join("\n"), "utf8");
  const bindings = parseExpressionBindings({
    resources: {
      "sample_resource.example": {
        "nested[0].id": { expression: "[module.ids.first]" },
        "nested[1].id": { expression: "[module.ids.second]" },
      },
    },
  }, "sample_resource");
  await writeFile(path.join(workspace, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'variable "items" {',
    '  type = map(object({',
    '    nested = list(object({',
    '      id      = set(string)',
    '      sibling = string',
    '    }))',
    '  }))',
    '}',
    'module "ids" { source = "./ids" }',
    renderExpressionBindingsHcl(bindings),
    'module "consumer" {',
    '  source = "./consumer"',
    '  items  = local.infrawright_expression_bound_items',
    '}',
    'output "resolved" { value = module.consumer.items }',
    '',
  ].join("\n"), "utf8");
  await writeFile(path.join(workspace, "terraform.tfvars.json"), JSON.stringify({
    items: {
      example: {
        nested: [
          { id: ["literal-first"], sibling: "kept-first" },
          { id: ["literal-second"], sibling: "kept-second" },
          { id: ["literal-third"], sibling: "kept-third" },
        ],
      },
    },
  }), "utf8");
  terraform(workspace, ["init", "-backend=false", "-input=false"]);
  terraform(workspace, ["apply", "-auto-approve", "-input=false"]);
  const resolved = JSON.parse(terraform(workspace, ["output", "-json", "resolved"])) as {
    readonly example: { readonly nested: readonly { readonly id: readonly string[]; readonly sibling: string }[] };
  };
  assert.deepEqual(resolved.example.nested, [
    { id: ["resolved-first-id"], sibling: "kept-first" },
    { id: ["resolved-second-id"], sibling: "kept-second" },
    { id: ["literal-third"], sibling: "kept-third" },
  ]);
  const second = terraform(workspace, ["plan", "-detailed-exitcode", "-input=false"]);
  assert.match(second, /No changes/u);
});
