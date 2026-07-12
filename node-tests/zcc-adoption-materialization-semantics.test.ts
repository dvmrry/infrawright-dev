import assert from "node:assert/strict";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccAdoptionArtifactMaterialization,
} from "../node-src/contracts/validators.js";
import { zccAdoptionMaterializationOperationResultErrors } from "../node-src/contracts/zcc-adoption-operation-semantics.js";
import { compareZccAdoptionArtifactDigests } from "../node-src/domain/zcc-adoption-artifact-parity.js";
import {
  compileZccAdoptionArtifactSet,
  ZCC_ADOPTION_CATALOG_SHA256,
} from "../node-src/domain/zcc-adoption-artifacts.js";
import { loadZccAdoptionCatalog } from "../node-src/domain/zcc-adoption-catalog.js";
import type { ZccAdoptionArtifactMaterialization } from "../node-src/domain/zcc-adoption-materialization.js";
import type { MaterializeAdoptionArtifactsProcessRequest } from "../node-src/process/types.js";

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
        name: "Provider Identity",
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
      rootMembers: ["zcc_forwarding_profile", "zcc_trusted_network"],
      variableName: "zcc_trusted_network_items",
      configPath: "overlay/config/demo/zcc_trusted_network.auto.tfvars.json",
      importsPath: "overlay/imports/demo/zcc_trusted_network_imports.tf",
      lookupPath: "overlay/config/demo/zcc_trusted_network.lookup.json",
    },
  });
}

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

function receipt(): ZccAdoptionArtifactMaterialization {
  return {
    kind: "infrawright.zcc_adoption_artifact_materialization",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: "zcc_trusted_network",
    tenant: "demo",
    status: "complete",
    publication: {
      policy: "create_or_verify_exact",
      created: ["imports", "lookup", "tfvars"],
      reused: [],
    },
    verification: parity(),
  };
}

function semanticRules(): readonly string[] {
  return (validateZccAdoptionArtifactMaterialization.errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown } | undefined;
    return String(params?.rule ?? error.keyword);
  });
}

test("adoption materialization receipt is content-free and fully joined", () => {
  const result = receipt();
  assert.equal(
    validateZccAdoptionArtifactMaterialization(result),
    true,
    JSON.stringify(validateZccAdoptionArtifactMaterialization.errors),
  );
  const response = {
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "adoption-materialization-semantics",
    operation: "materialize_adoption_artifacts",
    status: "ok",
    diagnostics: [],
    result,
    error: null,
  };
  assert.equal(validateProcessResponse(response), true);
  assert.equal(JSON.stringify(result).includes("content"), false);
  assert.equal(validateProcessResponse({
    ...response,
    operation: "materialize_pull_artifacts",
  }), false);
});

test("receipt semantics enforce joins, ordering, partition, and exact parity", () => {
  const cases: readonly [string, (value: any) => void, string][] = [
    ["tenant", (value) => { value.tenant = "other"; }, "verification_join"],
    [
      "resource",
      (value) => { value.resource_type = "zcc_web_privacy"; },
      "verification_join",
    ],
    [
      "created order",
      (value) => { value.publication.created = ["tfvars", "lookup", "imports"]; },
      "publication_order",
    ],
    [
      "overlap",
      (value) => { value.publication.reused = ["imports"]; },
      "publication_partition",
    ],
    [
      "incomplete",
      (value) => { value.publication.created = ["imports", "tfvars"]; },
      "publication_partition",
    ],
    [
      "non-equal",
      (value) => { value.verification.parity.artifacts.lookup.status = "different"; },
      "verification_status",
    ],
    [
      "nested source provenance",
      (value) => { value.verification.source.path = "pulls/other/foreign.json"; },
      "source_path",
    ],
    [
      "nested parity counts",
      (value) => { value.verification.parity.equal = 2; },
      "parity_counts",
    ],
  ];
  for (const [name, mutate, rule] of cases) {
    const value = structuredClone(receipt());
    mutate(value);
    assert.equal(validateZccAdoptionArtifactMaterialization(value), false, name);
    assert.equal(semanticRules().includes(rule), true, `${name}:${semanticRules()}`);
  }
});

test("request requires one complete ready assertion joined to tenant and resource", () => {
  const request: MaterializeAdoptionArtifactsProcessRequest = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "adoption-materialization-request",
    operation: "materialize_adoption_artifacts",
    context: {
      workspace: "/workspace",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      publication: "create_or_verify_exact",
      tenant: "demo",
      resource_type: "zcc_trusted_network",
      assertion: parity(),
    },
  };
  assert.equal(
    validateProcessRequest(request),
    true,
    JSON.stringify(validateProcessRequest.errors),
  );
  assert.deepEqual(
    zccAdoptionMaterializationOperationResultErrors(request, receipt()),
    [],
  );
  const foreignVerification = structuredClone(receipt());
  (foreignVerification.verification.source as any).sha256 = "b".repeat(64);
  assert.deepEqual(
    zccAdoptionMaterializationOperationResultErrors(
      request,
      foreignVerification,
    ).map((error) => error.instancePath),
    ["/verification"],
  );
  for (const mutate of [
    (value: any) => { value.input.tenant = "other"; },
    (value: any) => { value.input.resource_type = "zcc_web_privacy"; },
    (value: any) => {
      value.input.assertion.status = "review_required";
      value.input.assertion.parity.status = "different";
      value.input.assertion.parity.equal = 2;
      value.input.assertion.parity.different = 1;
      value.input.assertion.parity.artifacts.lookup.status = "different";
      value.input.assertion.parity.artifacts.lookup.reference.sha256 = "0".repeat(64);
    },
    (value: any) => { value.input.candidate = "caller-selected"; },
    (value: any) => { value.context.output_root = "/caller-selected"; },
  ]) {
    const invalid = structuredClone(request);
    mutate(invalid);
    assert.equal(validateProcessRequest(invalid), false);
  }
});
