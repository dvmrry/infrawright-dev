import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { validateZccAdoptionArtifactSet } from "../node-src/contracts/validators.js";
import { zccAdoptionOperationResultErrors } from "../node-src/contracts/zcc-adoption-operation-semantics.js";
import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import type { CompileAdoptionArtifactsProcessRequest } from "../node-src/process/types.js";

function candidate() {
  return compileZccAdoptionArtifactSet({
    catalog: loadZccAdoptionCatalog(),
    catalogSha256: ZCC_ADOPTION_CATALOG_SHA256,
    rawItems: [{
      active: true,
      conditionType: 1,
      id: "tn-1",
      networkName: "Raw &amp; Identity",
    }],
    observedStates: [{
      address: "zcc_trusted_network.iw_4265560b890c8eb2",
      import_id: "tn-1",
      key: "raw_amp_identity",
      provider_name: "registry.terraform.io/zscaler/zcc",
      resource_type: "zcc_trusted_network",
      sensitive_values: {},
      values: {
        active: true,
        condition_type: "1",
        id: "tn-1",
        name: "Provider & Identity",
      },
    }],
    source: {
      path: "pulls/demo/zcc_trusted_network.json",
      sha256: "a".repeat(64),
      size_bytes: 41,
    },
    target: {
      tenant: "demo",
      resourceType: "zcc_trusted_network",
      rootLabel: "zcc_bundle",
      rootMembers: [
        "zcc_forwarding_profile",
        "zcc_trusted_network",
      ],
      variableName: "zcc_trusted_network_items",
      configPath: "overlay/config/demo/zcc_trusted_network.auto.tfvars.json",
      importsPath: "overlay/imports/demo/zcc_trusted_network_imports.tf",
      lookupPath: "overlay/config/demo/zcc_trusted_network.lookup.json",
    },
  });
}

function replaceContent(
  descriptor: Record<string, unknown>,
  content: string,
): void {
  descriptor.content = content;
  descriptor.size_bytes = Buffer.byteLength(content, "utf8");
  descriptor.sha256 = createHash("sha256").update(content).digest("hex");
}

test("provider-observed adoption artifact schema binds all candidate joins", () => {
  const value = candidate();
  assert.equal(
    validateZccAdoptionArtifactSet(value),
    true,
    JSON.stringify(validateZccAdoptionArtifactSet.errors),
  );

  for (const mutate of [
    (copy: any) => { copy.catalog.sha256 = "0".repeat(64); },
    (copy: any) => { copy.source.path = "pulls/other/zcc_trusted_network.json"; },
    (copy: any) => { copy.root.variable_name = "items"; },
    (copy: any) => { copy.artifacts.tfvars.sha256 = "0".repeat(64); },
    (copy: any) => {
      replaceContent(
        copy.artifacts.tfvars,
        '{\n  "zcc_trusted_network_items": {}\n}\n',
      );
    },
    (copy: any) => {
      replaceContent(copy.artifacts.imports, "");
    },
    (copy: any) => { copy.artifacts.lookup = null; },
  ]) {
    const copy = structuredClone(value);
    mutate(copy);
    assert.equal(validateZccAdoptionArtifactSet(copy), false);
  }
});

test("operation result semantics bind operation, mode, tenant, and resource", () => {
  const result = candidate();
  const request: CompileAdoptionArtifactsProcessRequest = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "adoption-semantics",
    operation: "compile_adoption_artifacts",
    context: {
      workspace: "/workspace",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      tenant: "demo",
      resource_type: "zcc_trusted_network",
    },
  };
  assert.deepEqual(zccAdoptionOperationResultErrors(request, result), []);

  const wrongTenant: CompileAdoptionArtifactsProcessRequest = {
    ...request,
    input: { ...request.input, tenant: "other" },
  };
  assert.deepEqual(
    zccAdoptionOperationResultErrors(wrongTenant, result).map((error) => error.instancePath),
    ["/tenant"],
  );
  const wrongResource: CompileAdoptionArtifactsProcessRequest = {
    ...request,
    input: { ...request.input, resource_type: "zcc_web_privacy" },
  };
  assert.deepEqual(
    zccAdoptionOperationResultErrors(wrongResource, result).map((error) => error.instancePath),
    ["/resource_type"],
  );
});
