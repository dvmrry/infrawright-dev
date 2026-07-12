import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  validateProcessResponse,
  validateZccAdoptionArtifactParity,
  validateZccAdoptionArtifactSet,
} from "../node-src/contracts/validators.js";
import {
  zccAdoptionOperationResultErrors,
  zccAdoptionParityOperationResultErrors,
} from "../node-src/contracts/zcc-adoption-operation-semantics.js";
import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import { compareZccAdoptionArtifactDigests } from "../node-src/domain/zcc-adoption-artifact-parity.js";
import type {
  CompareAdoptionArtifactsProcessRequest,
  CompileAdoptionArtifactsProcessRequest,
} from "../node-src/process/types.js";

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

function parity() {
  const value = candidate();
  return compareZccAdoptionArtifactDigests({
    candidate: value,
    materialized: {
      tfvars: {
        sha256: value.artifacts.tfvars.sha256,
        size_bytes: value.artifacts.tfvars.size_bytes,
      },
      imports: {
        sha256: value.artifacts.imports.sha256,
        size_bytes: value.artifacts.imports.size_bytes,
      },
      lookup: value.artifacts.lookup === null
        ? null
        : {
            sha256: value.artifacts.lookup.sha256,
            size_bytes: value.artifacts.lookup.size_bytes,
          },
    },
  });
}

function semanticRules(value: unknown): readonly string[] {
  assert.equal(validateZccAdoptionArtifactParity(value), false);
  return (validateZccAdoptionArtifactParity.errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown } | undefined;
    return String(params?.rule ?? error.keyword);
  });
}

test("adoption parity schema accepts only exact content-free joined coordinates", () => {
  const value = parity();
  assert.equal(
    validateZccAdoptionArtifactParity(value),
    true,
    JSON.stringify(validateZccAdoptionArtifactParity.errors),
  );
  assert.equal(value.status, "ready");
  assert.equal(value.parity.equal, 3);
  assert.equal(JSON.stringify(value).includes("content"), false);

  const wrongCatalog = structuredClone(value) as any;
  wrongCatalog.catalog.sources_sha256 = "0".repeat(64);
  assert.ok(semanticRules(wrongCatalog).includes("catalog_provenance"));

  const wrongSource = structuredClone(value) as any;
  wrongSource.source.path = "pulls/other/zcc_trusted_network.json";
  assert.ok(semanticRules(wrongSource).includes("source_path"));

  const wrongRoot = structuredClone(value) as any;
  wrongRoot.root.variable_name = "items";
  assert.ok(semanticRules(wrongRoot).includes("variable_name"));

  const wrongCoordinate = structuredClone(value) as any;
  wrongCoordinate.parity.artifacts.tfvars.reference.path =
    "overlay/config/demo/other.auto.tfvars.json";
  assert.ok(semanticRules(wrongCoordinate).includes("artifact_coordinates"));

  const wrongStatus = structuredClone(value) as any;
  wrongStatus.parity.artifacts.tfvars.reference.sha256 = "0".repeat(64);
  assert.ok(semanticRules(wrongStatus).includes("artifact_status"));

  const halfMissing = structuredClone(value) as any;
  halfMissing.parity.artifacts.tfvars.reference.sha256 = null;
  assert.ok(semanticRules(halfMissing).includes("reference_digest"));

  const wrongCounts = structuredClone(value) as any;
  wrongCounts.parity.equal = 2;
  assert.ok(semanticRules(wrongCounts).includes("parity_counts"));

  const wrongTop = structuredClone(value) as any;
  wrongTop.status = "review_required";
  assert.ok(semanticRules(wrongTop).includes("report_status"));

  const wrongLayout = structuredClone(value) as any;
  wrongLayout.parity.artifacts.imports.candidate.path =
    "elsewhere/imports/demo/zcc_trusted_network_imports.tf";
  wrongLayout.parity.artifacts.imports.reference.path =
    wrongLayout.parity.artifacts.imports.candidate.path;
  assert.ok(semanticRules(wrongLayout).includes("artifact_layout"));

  const wrongLookupApplicability = structuredClone(value) as any;
  wrongLookupApplicability.parity.artifacts.lookup = {
    candidate: null,
    reference: null,
    status: "not_applicable",
  };
  assert.equal(validateZccAdoptionArtifactParity(wrongLookupApplicability), false);
});

test("adoption parity operation binds request and rejects pull-result cross-pairs", () => {
  const result = parity();
  const request: CompareAdoptionArtifactsProcessRequest = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "adoption-parity-semantics",
    operation: "compare_adoption_artifacts",
    context: {
      workspace: "/workspace",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      reference: "materialized",
      tenant: "demo",
      resource_type: "zcc_trusted_network",
    },
  };
  assert.deepEqual(zccAdoptionParityOperationResultErrors(request, result), []);
  assert.deepEqual(
    zccAdoptionParityOperationResultErrors({
      ...request,
      input: { ...request.input, tenant: "other" },
    }, result).map((error) => error.instancePath),
    ["/tenant"],
  );
  assert.deepEqual(
    zccAdoptionParityOperationResultErrors({
      ...request,
      input: { ...request.input, resource_type: "zcc_web_privacy" },
    }, result).map((error) => error.instancePath),
    ["/resource_type"],
  );

  const response = {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: request.request_id,
    operation: "compare_adoption_artifacts",
    status: "ok",
    diagnostics: [],
    result,
    error: null,
  };
  assert.equal(validateProcessResponse(response), true);
  assert.equal(validateProcessResponse({
    ...response,
    operation: "compare_pull_artifacts",
  }), false);
  assert.equal(validateProcessResponse({
    ...response,
    operation: "compile_adoption_artifacts",
  }), false);
});
