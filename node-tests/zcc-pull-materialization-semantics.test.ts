import assert from "node:assert/strict";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullArtifactMaterialization,
} from "../node-src/contracts/validators.js";
import {
  compileZccPullArtifactSet,
  ZCC_TRANSFORM_CATALOG_SHA256,
  type ZccPullResourceType,
} from "../node-src/domain/zcc-pull-artifacts.js";
import { compareZccPullArtifactDigests } from "../node-src/domain/zcc-pull-parity.js";
import { loadZccTransformCatalog } from "../node-src/domain/transform-catalog.js";

function verification(resourceType: ZccPullResourceType) {
  const lookupPath = resourceType === "zcc_trusted_network"
    ? `config/tenant/${resourceType}.lookup.json`
    : null;
  const candidate = compileZccPullArtifactSet({
    catalog: loadZccTransformCatalog(),
    catalogSha256: ZCC_TRANSFORM_CATALOG_SHA256,
    rawItems: [],
    target: {
      tenant: "tenant",
      resourceType,
      rootLabel: resourceType,
      rootMembers: [resourceType],
      variableName: "items",
      configPath: `config/tenant/${resourceType}.auto.tfvars.json`,
      importsPath: `imports/tenant/${resourceType}_imports.tf`,
      lookupPath,
    },
    source: {
      path: `pulls/tenant/${resourceType}.json`,
      sha256: "a".repeat(64),
      size_bytes: 2,
    },
  });
  return compareZccPullArtifactDigests({
    candidate,
    materialized: {
      tfvars: {
        sha256: candidate.artifacts.tfvars.sha256,
        size_bytes: candidate.artifacts.tfvars.size_bytes,
      },
      imports: {
        sha256: candidate.artifacts.imports.sha256,
        size_bytes: candidate.artifacts.imports.size_bytes,
      },
      lookup: candidate.artifacts.lookup === null
        ? null
        : {
            sha256: candidate.artifacts.lookup.sha256,
            size_bytes: candidate.artifacts.lookup.size_bytes,
          },
    },
  });
}

function result(
  resourceType: ZccPullResourceType = "zcc_failopen_policy",
): Record<string, unknown> {
  const applicable = resourceType === "zcc_trusted_network"
    ? ["imports", "lookup", "tfvars"]
    : ["imports", "tfvars"];
  return {
    kind: "infrawright.zcc_pull_artifact_materialization",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: resourceType,
    tenant: "tenant",
    status: "complete",
    publication: {
      policy: "create_or_verify_exact",
      created: applicable,
      reused: [],
    },
    verification: verification(resourceType),
  };
}

function errorsFor(value: unknown): readonly string[] {
  assert.equal(validateZccPullArtifactMaterialization(value), false);
  return (validateZccPullArtifactMaterialization.errors ?? []).map((error) => {
    const params = error.params as { readonly rule?: unknown } | undefined;
    return String(params?.rule ?? error.keyword);
  });
}

test("materialization schema accepts exact created/reused partitions", () => {
  const fresh = result();
  assert.equal(
    validateZccPullArtifactMaterialization(fresh),
    true,
    JSON.stringify(validateZccPullArtifactMaterialization.errors),
  );

  const reused = result("zcc_trusted_network");
  reused.publication = {
    policy: "create_or_verify_exact",
    created: [],
    reused: ["imports", "lookup", "tfvars"],
  };
  assert.equal(
    validateZccPullArtifactMaterialization(reused),
    true,
    JSON.stringify(validateZccPullArtifactMaterialization.errors),
  );

  const mixed = result("zcc_trusted_network");
  mixed.publication = {
    policy: "create_or_verify_exact",
    created: ["tfvars"],
    reused: ["imports", "lookup"],
  };
  assert.equal(
    validateZccPullArtifactMaterialization(mixed),
    true,
    JSON.stringify(validateZccPullArtifactMaterialization.errors),
  );
});

test("materialization semantics reject false publication accounting", () => {
  const unsorted = result("zcc_trusted_network");
  (unsorted.publication as Record<string, unknown>).created = [
    "tfvars",
    "imports",
    "lookup",
  ];
  assert.ok(errorsFor(unsorted).includes("publication_order"));

  const overlap = result();
  overlap.publication = {
    policy: "create_or_verify_exact",
    created: ["imports", "tfvars"],
    reused: ["imports"],
  };
  assert.ok(errorsFor(overlap).includes("publication_partition"));

  const missing = result("zcc_trusted_network");
  missing.publication = {
    policy: "create_or_verify_exact",
    created: ["imports", "tfvars"],
    reused: [],
  };
  assert.ok(errorsFor(missing).includes("publication_partition"));
});

test("materialization semantics bind tenant/resource and ready verification", () => {
  const wrongTenant = result();
  wrongTenant.tenant = "other";
  assert.ok(errorsFor(wrongTenant).includes("verification_join"));

  const wrongResource = result();
  wrongResource.resource_type = "zcc_web_privacy";
  assert.ok(errorsFor(wrongResource).includes("verification_join"));

  const notReady = structuredClone(result());
  const parity = ((notReady.verification as Record<string, unknown>)
    .parity as Record<string, unknown>);
  const artifacts = parity.artifacts as Record<string, unknown>;
  const tfvars = artifacts.tfvars as Record<string, unknown>;
  tfvars.observed = null;
  tfvars.status = "missing";
  parity.matched = 1;
  parity.missing = 1;
  parity.status = "different";
  (notReady.verification as Record<string, unknown>).status = "review_required";
  assert.equal(validateZccPullArtifactMaterialization(notReady), false);
});

test("process schemas bind ready assertions and materialization results", () => {
  const assertion = verification("zcc_failopen_policy");
  const request = {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "materialize",
    operation: "materialize_pull_artifacts",
    context: {
      workspace: "/workspace",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "bootstrap",
      publication: "create_or_verify_exact",
      tenant: "tenant",
      resource_type: "zcc_failopen_policy",
      assertion,
    },
  };
  assert.equal(
    validateProcessRequest(request),
    true,
    JSON.stringify(validateProcessRequest.errors),
  );
  assert.equal(validateProcessResponse({
    kind: "infrawright.process_response",
    schema_version: 1,
    request_id: "materialize",
    operation: "materialize_pull_artifacts",
    status: "ok",
    diagnostics: [],
    result: result(),
    error: null,
  }), true, JSON.stringify(validateProcessResponse.errors));

  const blocked = structuredClone(request);
  (blocked.input.assertion as unknown as Record<string, unknown>).status =
    "review_required";
  assert.equal(validateProcessRequest(blocked), false);

  const wrongTenant = structuredClone(request);
  wrongTenant.input.tenant = "other";
  assert.equal(validateProcessRequest(wrongTenant), false);

  const wrongResource = structuredClone(request);
  wrongResource.input.resource_type = "zcc_web_privacy";
  assert.equal(validateProcessRequest(wrongResource), false);
});
