import assert from "node:assert/strict";
import test from "node:test";

import {
  validateProcessRequest,
  validateProcessResponse,
  validateZccPullRefreshParity,
  validateZccPullRefreshParitySeed,
} from "../node-src/contracts/validators.js";
import { ProcessFailure } from "../node-src/domain/errors.js";
import {
  compareZccPullRefreshParityOperation,
  type ZccPullRefreshParity,
  type ZccPullRefreshParitySeed,
} from "../node-src/domain/zcc-pull-refresh-parity.js";
import {
  zccPullRefreshParityRequestSha,
  zccRefreshEvidenceDigest,
} from "../node-src/domain/zcc-pull-refresh-fingerprints.js";

const SHA = "1".repeat(64);
const SHA_TWO = "2".repeat(64);

function neutral() {
  const withoutEvidence = {
    source: { sha256: SHA, size_bytes: 2 },
    catalog: {
      kind: "infrawright.transform_catalog" as const,
      schema_version: 1 as const,
      sha256: "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a",
      sources_sha256: "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11",
    },
    root: {
      label: "zcc_forwarding_profile",
      members: ["zcc_forwarding_profile"],
      variable_name: "items",
    },
    baseline: {
      tfvars: { state: "present" as const, sha256: SHA, size_bytes: 4 },
      imports: { state: "present" as const, sha256: SHA, size_bytes: 5 },
      lookup: { state: "absent" as const },
      moves: { state: "absent" as const },
      pending_moves: { state: "absent" as const },
      alternate_hcl: { state: "absent" as const },
      generated_bindings: { state: "absent" as const },
    },
    desired: {
      tfvars: {
        state: "present" as const,
        media_type: "application/json",
        encoding: "utf-8" as const,
        sha256: SHA_TWO,
        size_bytes: 6,
      },
      imports: {
        state: "present" as const,
        media_type: "text/x-hcl",
        encoding: "utf-8" as const,
        sha256: SHA_TWO,
        size_bytes: 7,
      },
      lookup: { state: "absent" as const },
      moves: { state: "absent" as const },
      pending_moves: { state: "absent" as const },
      alternate_hcl: { state: "absent" as const },
      generated_bindings: { state: "absent" as const },
    },
    status: "ready" as const,
    unexpected_drops: [],
    moves: { safe_count: 0, suppressed_count: 0 },
    decision_sha256: SHA,
  };
  return {
    ...withoutEvidence,
    evidence_sha256: zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_path_neutral_evidence",
      schema_version: 1,
      ...withoutEvidence,
    }),
  };
}

function seed(): ZccPullRefreshParitySeed {
  const evidence = neutral();
  const binding = {
    request_sha256: SHA,
    binding_sha256: SHA_TWO,
    deployment_semantics_sha256: SHA,
    controls: {
      deployment: { state: "present" as const, sha256: SHA, size_bytes: 2 },
      root_catalog: { state: "present" as const, sha256: SHA_TWO, size_bytes: 3 },
    },
  };
  const withoutDigest = {
    kind: "infrawright.zcc_pull_refresh_parity_seed" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    reference: "materialized_twin" as const,
    product: "zcc" as const,
    resource_type: "zcc_forwarding_profile" as const,
    tenant: "tenant",
    bindings: { candidate: binding, reference_twin: binding },
    candidate: {
      ...evidence,
      baseline_fingerprint_sha256: SHA,
      transition_sha256: SHA_TWO,
    },
    reference_twin: evidence,
    differences: [],
    status: "ready" as const,
  };
  return {
    ...withoutDigest,
    seed_sha256: zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_parity_seed_digest",
      schema_version: 1,
      seed: withoutDigest,
    }),
  };
}

function parity(): ZccPullRefreshParity {
  const seeded = seed();
  const artifacts = Object.fromEntries(
    Object.entries(seeded.candidate.desired).map(([role, expected]) => [
      role,
      {
        expected,
        observed: expected.state === "absent"
          ? { state: "absent" }
          : {
              state: "present",
              sha256: expected.sha256,
              size_bytes: expected.size_bytes,
            },
        status: "match",
      },
    ]),
  ) as ZccPullRefreshParity["parity"]["artifacts"];
  const withoutDigest = {
    kind: "infrawright.zcc_pull_refresh_parity" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    reference: "materialized_twin" as const,
    product: "zcc" as const,
    resource_type: "zcc_forwarding_profile" as const,
    tenant: "tenant",
    seed: seeded,
    candidate: seeded.candidate,
    parity: {
      status: "equal" as const,
      matched: 7,
      mismatched: 0,
      missing: 0,
      unexpected: 0,
      artifacts,
    },
    status: "ready" as const,
  };
  return {
    ...withoutDigest,
    assertion_sha256: zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_parity_assertion_digest",
      schema_version: 1,
      assertion: withoutDigest,
    }),
  };
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function rehashEvidence(value: Record<string, any>): void {
  value.evidence_sha256 = zccRefreshEvidenceDigest({
    kind: "infrawright.zcc_pull_refresh_path_neutral_evidence",
    schema_version: 1,
    source: value.source,
    catalog: value.catalog,
    root: value.root,
    baseline: value.baseline,
    desired: value.desired,
    status: value.status,
    unexpected_drops: value.unexpected_drops,
    moves: value.moves,
    decision_sha256: value.decision_sha256,
  });
}

function rehashSeed(value: Record<string, any>): void {
  rehashEvidence(value.candidate as Record<string, any>);
  rehashEvidence(value.reference_twin as Record<string, any>);
  const { seed_sha256: _ignored, ...withoutDigest } = value;
  value.seed_sha256 = zccRefreshEvidenceDigest({
    kind: "infrawright.zcc_pull_refresh_parity_seed_digest",
    schema_version: 1,
    seed: withoutDigest,
  });
}

function wrappedAssertion(
  seeded: ZccPullRefreshParitySeed,
): ZccPullRefreshParity {
  const desired = seeded.candidate.desired;
  const artifacts = Object.fromEntries(Object.entries(desired).map(([role, expected]) => [
    role,
    {
      expected,
      observed: expected.state === "absent"
        ? { state: "absent" }
        : {
            state: "present",
            sha256: expected.sha256,
            size_bytes: expected.size_bytes,
          },
      status: "match",
    },
  ])) as ZccPullRefreshParity["parity"]["artifacts"];
  const withoutDigest = {
    kind: "infrawright.zcc_pull_refresh_parity" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    reference: "materialized_twin" as const,
    product: "zcc" as const,
    resource_type: seeded.resource_type,
    tenant: seeded.tenant,
    seed: seeded,
    candidate: seeded.candidate,
    parity: {
      status: "equal" as const,
      matched: 7,
      mismatched: 0,
      missing: 0,
      unexpected: 0,
      artifacts,
    },
    status: seeded.candidate.status === "ready"
      ? "ready" as const
      : "review_required" as const,
  };
  return {
    ...withoutDigest,
    assertion_sha256: zccRefreshEvidenceDigest({
      kind: "infrawright.zcc_pull_refresh_parity_assertion_digest",
      schema_version: 1,
      assertion: withoutDigest,
    }),
  };
}

function wrappedRequest(seeded: ZccPullRefreshParitySeed): unknown {
  return {
    kind: "infrawright.process_request",
    schema_version: 1,
    request_id: "semantic-rehash",
    operation: "compare_pull_artifacts",
    context: {
      workspace: "/tmp/candidate-refresh-parity",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    input: {
      mode: "refresh",
      reference: "materialized_twin",
      tenant: seeded.tenant,
      resource_type: seeded.resource_type,
      reference_context: {
        workspace: "/tmp/reference-refresh-parity",
        deployment: "deployment.json",
        root_catalog: "catalog.json",
      },
      seed: seeded,
    },
  };
}

function requestBoundSeed(): Record<string, any> {
  const value = clone(seed()) as unknown as Record<string, any>;
  const request = wrappedRequest(value as unknown as ZccPullRefreshParitySeed) as {
    context: { workspace: string; deployment: string; root_catalog: string };
    input: {
      tenant: string;
      resource_type: ZccPullRefreshParitySeed["resource_type"];
      reference_context: { workspace: string; deployment: string; root_catalog: string };
    };
  };
  value.bindings.candidate.request_sha256 = zccPullRefreshParityRequestSha({
    context: request.context,
    tenant: request.input.tenant,
    resourceType: request.input.resource_type,
  });
  value.bindings.reference_twin.request_sha256 = zccPullRefreshParityRequestSha({
    context: request.input.reference_context,
    tenant: request.input.tenant,
    resourceType: request.input.resource_type,
  });
  rehashSeed(value);
  return value;
}

test("refresh parity schemas and semantics reject redundant-field tampering", () => {
  const seeded = seed();
  assert.equal(validateZccPullRefreshParitySeed(seeded), true);
  for (const mutate of [
    (value: Record<string, any>) => { value.seed_sha256 = SHA; },
    (value: Record<string, any>) => { value.candidate.evidence_sha256 = SHA; },
    (value: Record<string, any>) => { value.differences = ["source"]; },
    (value: Record<string, any>) => { value.status = "review_required"; },
  ]) {
    const value = clone(seeded) as unknown as Record<string, any>;
    mutate(value);
    assert.equal(validateZccPullRefreshParitySeed(value), false);
  }

  const assertion = parity();
  assert.equal(validateZccPullRefreshParity(assertion), true);
  for (const mutate of [
    (value: Record<string, any>) => { value.assertion_sha256 = SHA; },
    (value: Record<string, any>) => { value.parity.matched = 6; },
    (value: Record<string, any>) => { value.parity.status = "different"; },
    (value: Record<string, any>) => {
      value.parity.artifacts.imports.status = "mismatch";
    },
  ]) {
    const value = clone(assertion) as unknown as Record<string, any>;
    mutate(value);
    assert.equal(validateZccPullRefreshParity(value), false);
  }
});

test("fully rehashed semantic contradictions fail standalone and wrapped contracts", () => {
  const presentJson = {
    state: "present",
    media_type: "application/json",
    encoding: "utf-8",
    sha256: SHA,
    size_bytes: 1,
  };
  const presentHcl = {
    state: "present",
    media_type: "text/x-hcl",
    encoding: "utf-8",
    sha256: SHA,
    size_bytes: 1,
  };
  const mutations: readonly ((value: Record<string, any>) => void)[] = [
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.root = {
          label: "zcc_group",
          members: ["zcc_forwarding_profile", "zcc_not_real"],
          variable_name: "zcc_forwarding_profile_items",
        };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.root = {
          label: "zcc_forwarding_profile",
          members: ["zcc_forwarding_profile", "zcc_trusted_network"],
          variable_name: "items",
        };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.baseline.tfvars = { state: "absent" };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.baseline.imports = { state: "absent" };
        twin.desired.imports = { state: "absent" };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.unexpected_drops = ["rehash-hidden-drop"];
        twin.status = "ready";
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.baseline.lookup = { state: "present", sha256: SHA, size_bytes: 1 };
        twin.desired.lookup = { ...presentJson };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.desired.moves = { ...presentHcl };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.moves.safe_count = 1;
        twin.desired.moves = { state: "absent" };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.desired.imports.media_type = "application/json";
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.moves = { safe_count: 50_000, suppressed_count: 1 };
        twin.desired.moves = { ...presentHcl };
      }
    },
    (value) => {
      for (const twin of [value.candidate, value.reference_twin]) {
        twin.baseline.moves = { state: "present", sha256: SHA, size_bytes: 1 };
      }
    },
  ];

  for (const mutate of mutations) {
    const value = requestBoundSeed();
    mutate(value);
    rehashSeed(value);
    const invalid = value as unknown as ZccPullRefreshParitySeed;
    const assertion = wrappedAssertion(invalid);
    assert.equal(validateZccPullRefreshParitySeed(invalid), false);
    assert.equal(validateZccPullRefreshParity(assertion), false);
    assert.equal(validateProcessRequest(wrappedRequest(invalid)), false);
    assert.equal(validateProcessResponse({
      kind: "infrawright.process_response",
      schema_version: 1,
      request_id: "semantic-rehash",
      operation: "compare_pull_artifacts",
      status: "ok",
      result: assertion,
      error: null,
      diagnostics: [],
    }), false);
  }
});

test("request contract joins outer and reference contexts to seed hashes", () => {
  const valid = requestBoundSeed() as unknown as ZccPullRefreshParitySeed;
  const request = wrappedRequest(valid) as Record<string, any>;
  assert.equal(validateZccPullRefreshParitySeed(valid), true);
  assert.equal(validateProcessRequest(request), true);

  const candidateMismatch = clone(request) as Record<string, any>;
  candidateMismatch.context.workspace = "/tmp/different-candidate";
  assert.equal(validateProcessRequest(candidateMismatch), false);

  const referenceMismatch = clone(request) as Record<string, any>;
  referenceMismatch.input.reference_context.workspace = "/tmp/different-reference";
  assert.equal(validateProcessRequest(referenceMismatch), false);

  const tenantMismatch = clone(request) as Record<string, any>;
  tenantMismatch.input.tenant = "different_tenant";
  assert.equal(validateProcessRequest(tenantMismatch), false);
});

test("direct comparison rejects every outer replay before workspace I/O", async () => {
  const seeded = requestBoundSeed() as unknown as ZccPullRefreshParitySeed;
  const base = {
    context: {
      workspace: "/tmp/candidate-refresh-parity",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    referenceContext: {
      workspace: "/tmp/reference-refresh-parity",
      deployment: "deployment.json",
      root_catalog: "catalog.json",
    },
    tenant: "tenant",
    resourceType: "zcc_forwarding_profile" as const,
    seed: seeded,
  };
  const replays = [
    { ...base, tenant: "replayed_tenant" },
    { ...base, resourceType: "zcc_web_privacy" as const },
    {
      ...base,
      context: {
        ...base.context,
        workspace: "/definitely/missing/direct-boundary-secret-candidate",
      },
    },
    {
      ...base,
      referenceContext: {
        ...base.referenceContext,
        workspace: "/definitely/missing/direct-boundary-secret-reference",
      },
    },
  ];
  for (const replay of replays) {
    await assert.rejects(
      compareZccPullRefreshParityOperation(replay),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "INVALID_REFRESH_PARITY_SEED"
        && !error.message.includes("direct-boundary-secret"),
    );
  }
});

async function expectSeedFailure(seedValue: unknown): Promise<void> {
  await assert.rejects(
    compareZccPullRefreshParityOperation({
      context: {
        workspace: "/definitely/missing/candidate",
        deployment: "deployment.json",
        root_catalog: "catalog.json",
      },
      referenceContext: {
        workspace: "/definitely/missing/reference",
        deployment: "deployment.json",
        root_catalog: "catalog.json",
      },
      tenant: "tenant",
      resourceType: "zcc_forwarding_profile",
      seed: seedValue as ZccPullRefreshParitySeed,
    }),
    (error: unknown) => error instanceof ProcessFailure
      && (
        error.code === "INVALID_REFRESH_PARITY_SEED"
        || error.code === "INVALID_REFRESH_PARITY_INPUT"
      )
      && !error.message.includes("direct-boundary-secret"),
  );
}

test("direct refresh parity seed boundary rejects cycles, depth, accessors, and budgets before I/O", async () => {
  const cyclic: Record<string, unknown> = { ...seed() };
  cyclic.self = cyclic;
  await expectSeedFailure(cyclic);

  let deep: unknown = "leaf";
  for (let index = 0; index < 130; index += 1) {
    deep = [deep];
  }
  await expectSeedFailure(deep);

  const accessor = clone(seed()) as unknown as Record<string, unknown>;
  Object.defineProperty(accessor, "status", {
    enumerable: true,
    get() {
      throw new Error("direct-boundary-secret");
    },
  });
  await expectSeedFailure(accessor);

  const huge = clone(seed()) as unknown as Record<string, unknown>;
  huge.padding = "x".repeat(600 * 1024);
  await expectSeedFailure(huge);

  const hugeArray = clone(seed()) as unknown as Record<string, unknown>;
  hugeArray.padding = Array.from({ length: 50_001 }, () => 0);
  await expectSeedFailure(hugeArray);
});
