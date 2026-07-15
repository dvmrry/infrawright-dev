import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { renderEnvironmentSmokeTest } from "../node-src/domain/environment-generator.js";
import { loadedRootTopology } from "../node-src/domain/roots.js";
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

  await writeFile(path.join(referrer, "main.tf"), [
    'terraform { required_version = ">= 1.5" }',
    'data "terraform_remote_state" "zpa_segment_group" {',
    '  backend = "local"',
    '  config = {',
    `    path = ${JSON.stringify(path.join(referent, "terraform.tfstate"))}`,
    '  }',
    '}',
    'resource "terraform_data" "consumer" {',
    '  input = data.terraform_remote_state.zpa_segment_group.outputs.infrawright_reference_ids.zpa_segment_group["segment_one"]',
    '}',
    '',
  ].join("\n"), "utf8");
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
