import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { Ajv2020 } from "ajv/dist/2020.js";
import { LosslessNumber } from "lossless-json";

import paritySchema from "../docs/schemas/zcc-adoption-oracle-parity.schema.json" with { type: "json" };
import {
  buildZccAdoptionOracleParity,
  validateZccAdoptionOracleParityReport,
  ZCC_ADOPTION_PARITY_RESOURCE_TYPES,
  type BuildZccAdoptionOracleParityOptions,
  type ZccAdoptionParityEvidenceClass,
  type ZccAdoptionParityResourceInput,
  type ZccAdoptionParitySnapshotInput,
  type ZccAdoptionOracleParityReport,
} from "../node-src/domain/zcc-adoption-parity.js";
import { ProcessFailure } from "../node-src/domain/errors.js";

const SHA_A = "a".repeat(64);
const SHA_B = "b".repeat(64);
const SHA_C = "c".repeat(64);
const V1_KNOWN_ANSWER_KEY = Buffer.from(
  "000102030405060708090a0b0c0d0e0f"
    + "101112131415161718191a1b1c1d1e1f",
  "hex",
);

const ajv = new Ajv2020({ allErrors: true, strict: false });
const validateSchema = ajv.compile(paritySchema);

function bytes(value: string): Uint8Array {
  return Buffer.from(value, "utf8");
}

function canonicalClosedReportJson(value: unknown): string {
  if (value === null || typeof value === "boolean") {
    return value === null ? "null" : value ? "true" : "false";
  }
  if (typeof value === "string" || typeof value === "number") {
    const encoded = JSON.stringify(value);
    assert.notEqual(encoded, undefined);
    return encoded!;
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalClosedReportJson).join(",")}]`;
  }
  assert.equal(typeof value, "object");
  assert.notEqual(value, null);
  const record = value as Record<string, unknown>;
  return `{${Object.keys(record).sort().map((key) => {
    return `${canonicalClosedReportJson(key)}:${canonicalClosedReportJson(record[key])}`;
  }).join(",")}}`;
}

function rehashClosedReport(report: Record<string, unknown>): void {
  const body = { ...report };
  delete body.report_sha256;
  report.report_sha256 = createHash("sha256")
    .update(canonicalClosedReportJson(body), "utf8")
    .digest("hex");
}

function snapshot(options: {
  readonly resourceType: string;
  readonly marker?: string | undefined;
  readonly empty?: boolean | undefined;
  readonly sameOpaqueValue?: boolean | undefined;
}): ZccAdoptionParitySnapshotInput {
  const marker = options.marker ?? "stable";
  const opaque = ["1"];
  return {
    survivors: options.empty
      ? []
      : options.sameOpaqueValue
        ? opaque
        : [{ secret_id: `${options.resourceType}-${marker}-id` }],
    observations: options.empty
      ? []
      : options.sameOpaqueValue
        ? opaque
        : [{ secret_state: `${options.resourceType}-${marker}-state` }],
    projection: options.sameOpaqueValue
      ? opaque
      : { secret_projection: `${options.resourceType}-${marker}-projection` },
    artifacts: {
      tfvars: bytes(options.sameOpaqueValue
        ? '["1"]'
        : `${options.resourceType}-${marker}-tfvars`),
      imports: bytes(options.sameOpaqueValue
        ? '["1"]'
        : `${options.resourceType}-${marker}-imports`),
      lookup: options.resourceType === "zcc_trusted_network"
        ? bytes(options.sameOpaqueValue
          ? '["1"]'
          : `${options.resourceType}-${marker}-lookup`)
        : null,
    },
  };
}

function resourceInput(options: {
  readonly resourceType: typeof ZCC_ADOPTION_PARITY_RESOURCE_TYPES[number];
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly empty?: boolean | undefined;
  readonly sameOpaqueValue?: boolean | undefined;
  readonly nodeMarker?: string | undefined;
  readonly afterMarker?: string | undefined;
}): ZccAdoptionParityResourceInput {
  return {
    resource_type: options.resourceType,
    python_before: snapshot({
      resourceType: options.resourceType,
      empty: options.empty,
      sameOpaqueValue: options.sameOpaqueValue,
    }),
    node: snapshot({
      resourceType: options.resourceType,
      marker: options.nodeMarker,
      empty: options.empty,
      sameOpaqueValue: options.sameOpaqueValue,
    }),
    python_after: options.evidenceClass === "live_independent_executor"
      ? snapshot({
          resourceType: options.resourceType,
          marker: options.afterMarker,
          empty: options.empty,
          sameOpaqueValue: options.sameOpaqueValue,
        })
      : null,
  };
}

function buildOptions(options: {
  readonly evidenceClass: ZccAdoptionParityEvidenceClass;
  readonly key?: Uint8Array;
  readonly emptyResource?: string;
  readonly sameOpaqueValue?: boolean;
  readonly pythonAfterSha?: string | null;
  readonly resourceOverride?: (
    resource: ZccAdoptionParityResourceInput,
  ) => ZccAdoptionParityResourceInput;
}): BuildZccAdoptionOracleParityOptions {
  const resources = ZCC_ADOPTION_PARITY_RESOURCE_TYPES.map((resourceType) => {
    const resource = resourceInput({
      resourceType,
      evidenceClass: options.evidenceClass,
      empty: options.emptyResource === resourceType,
      sameOpaqueValue: options.sameOpaqueValue,
    });
    return options.resourceOverride?.(resource) ?? resource;
  });
  return {
    evidenceClass: options.evidenceClass,
    commitmentKey: options.key ?? Buffer.alloc(32, 0x4a),
    builds: {
      python_before_sha256: SHA_A,
      python_after_sha256: options.evidenceClass === "live_independent_executor"
        ? options.pythonAfterSha ?? SHA_A
        : options.pythonAfterSha ?? null,
      node_sha256: SHA_B,
    },
    resources,
  };
}

function build(
  evidenceClass: ZccAdoptionParityEvidenceClass,
): ZccAdoptionOracleParityReport {
  return buildZccAdoptionOracleParity(buildOptions({ evidenceClass }));
}

function knownAnswerOptions(
  nodeProjection: unknown = -0,
): BuildZccAdoptionOracleParityOptions {
  const unicodeValue = [{ "😀": "𝄞", "é": "café", a: "雪" }];
  const reorderedUnicodeValue = [{ a: "雪", "é": "café", "😀": "𝄞" }];
  const unicodeCanonical = '[{"a":"雪","é":"café","😀":"𝄞"}]';
  const snapshotFor = (
    resourceType: typeof ZCC_ADOPTION_PARITY_RESOURCE_TYPES[number],
    side: "python" | "node",
  ): ZccAdoptionParitySnapshotInput => {
    const knownAnswerResource = resourceType === "zcc_device_cleanup";
    return {
      survivors: knownAnswerResource
        ? side === "python"
          ? unicodeValue
          : reorderedUnicodeValue
        : ["1"],
      observations: knownAnswerResource
        ? [new LosslessNumber("1e0")]
        : ["1"],
      projection: knownAnswerResource
        ? side === "python"
          ? -0
          : nodeProjection
        : ["1"],
      artifacts: {
        // For the first resource, these bytes exactly equal the canonical
        // identity value payload so the vector also pins the media framing.
        tfvars: bytes(knownAnswerResource ? unicodeCanonical : '["1"]'),
        imports: bytes('["1"]'),
        lookup: resourceType === "zcc_trusted_network"
          ? bytes('["1"]')
          : null,
      },
    };
  };
  return {
    evidenceClass: "simulation",
    commitmentKey: V1_KNOWN_ANSWER_KEY,
    builds: {
      python_before_sha256: SHA_A,
      python_after_sha256: null,
      node_sha256: SHA_B,
    },
    resources: ZCC_ADOPTION_PARITY_RESOURCE_TYPES.map((resourceType) => ({
      resource_type: resourceType,
      python_before: snapshotFor(resourceType, "python"),
      node: snapshotFor(resourceType, "node"),
      python_after: null,
    })),
  };
}

function expectInputFailure(
  run: () => unknown,
  secret = "SECRET-MUST-NOT-LEAK",
): void {
  let thrown: unknown;
  try {
    run();
  } catch (error: unknown) {
    thrown = error;
  }
  assert.ok(thrown instanceof ProcessFailure);
  assert.equal(thrown.code, "INVALID_ZCC_ADOPTION_PARITY_INPUT");
  assert.equal(JSON.stringify({
    message: thrown.message,
    details: thrown.details,
  }).includes(secret), false);
}

test("schema and semantic validator accept the deeply immutable exact report", () => {
  const report = build("live_independent_executor");
  assert.equal(validateSchema(report), true, JSON.stringify(validateSchema.errors));
  assert.equal(validateZccAdoptionOracleParityReport(report), true);
  assert.deepEqual(
    report.resources.map((resource) => resource.resource_type),
    [...ZCC_ADOPTION_PARITY_RESOURCE_TYPES],
  );
  assert.equal(report.summary.total_roles, 30);
  assert.equal(report.summary.applicable, 26);
  assert.equal(report.summary.matched, 26);
  assert.equal(report.summary.not_applicable, 4);
  assert.equal(Object.isFrozen(report), true);
  assert.equal(Object.isFrozen(report.bindings), true);
  assert.equal(Object.isFrozen(report.resources), true);
  assert.equal(Object.isFrozen(report.resources[0]?.comparisons), true);
  assert.throws(() => {
    (report.summary as unknown as Record<string, unknown>).matched = 0;
  }, TypeError);
});

test("v1 records evidence classes but never qualifies caller assertions", () => {
  const simulation = build("simulation");
  assert.equal(simulation.summary.status, "equal");
  assert.equal(simulation.summary.live_input_coverage, "not_applicable");
  assert.equal(simulation.summary.projection_qualification, "not_qualified");
  assert.equal(simulation.summary.executor_qualification, "not_qualified");
  assert.equal("cutover_qualification" in simulation.summary, false);

  const shared = build("live_shared_observation");
  assert.equal(shared.summary.live_input_coverage, "complete");
  assert.equal(shared.summary.projection_qualification, "not_qualified");
  assert.equal(shared.summary.executor_qualification, "not_qualified");
  assert.equal("cutover_qualification" in shared.summary, false);

  const independent = build("live_independent_executor");
  assert.equal(independent.summary.live_input_coverage, "complete");
  assert.equal(independent.bindings.builds.python_stability, "stable");
  assert.equal(independent.summary.projection_qualification, "not_qualified");
  assert.equal(independent.summary.executor_qualification, "not_qualified");
  assert.equal("cutover_qualification" in independent.summary, false);

  const emptyLive = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "live_shared_observation",
    emptyResource: "zcc_failopen_policy",
  }));
  assert.equal("input_presence" in emptyLive.resources[1]!, false);
  assert.equal(emptyLive.summary.live_input_coverage, "incomplete");
  assert.equal(emptyLive.summary.status, "equal");
  assert.equal(emptyLive.summary.projection_qualification, "not_qualified");
});

test("every evidence class rejects survivor and observation cardinality mismatch", () => {
  for (const evidenceClass of [
    "simulation",
    "live_shared_observation",
    "live_independent_executor",
  ] as const) {
    expectInputFailure(() => buildZccAdoptionOracleParity(buildOptions({
      evidenceClass,
      resourceOverride(resource) {
        if (resource.resource_type !== "zcc_device_cleanup") {
          return resource;
        }
        const cardinalityMismatch = (): ZccAdoptionParitySnapshotInput => ({
          survivors: [{ identity: "same" }],
          observations: [{ state: "same-1" }, { state: "same-2" }],
          projection: { projected: "same" },
          artifacts: {
            tfvars: bytes("same-tfvars"),
            imports: bytes("same-imports"),
            lookup: null,
          },
        });
        return {
          ...resource,
          python_before: cardinalityMismatch(),
          node: cardinalityMismatch(),
          python_after: evidenceClass === "live_independent_executor"
            ? cardinalityMismatch()
            : null,
        };
      },
    })));
  }

  const validShared = build("live_shared_observation");
  assert.equal(validShared.summary.live_input_coverage, "complete");
  assert.equal(validShared.summary.projection_qualification, "not_qualified");
  const validIndependent = build("live_independent_executor");
  assert.equal(validIndependent.summary.live_input_coverage, "complete");
  assert.equal(validIndependent.summary.executor_qualification, "not_qualified");
});

test("low-entropy values are keyed, domain-separated, and key-specific", () => {
  const keyA = Buffer.alloc(32, 0x11);
  const keyB = Buffer.alloc(32, 0x12);
  const first = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "simulation",
    key: keyA,
    sameOpaqueValue: true,
  }));
  const second = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "simulation",
    key: keyB,
    sameOpaqueValue: true,
  }));
  const comparisons = first.resources[0]?.comparisons;
  assert.notEqual(comparisons, undefined);
  if (comparisons === undefined) {
    return;
  }
  const plainDigest = createHash("sha256").update('["1"]').digest("hex");
  assert.notEqual(
    comparisons.identity.python_before_hmac_sha256,
    plainDigest,
  );
  const domains = [
    comparisons.identity.python_before_hmac_sha256,
    comparisons.observation.python_before_hmac_sha256,
    comparisons.projection.python_before_hmac_sha256,
    comparisons.tfvars.python_before_hmac_sha256,
    comparisons.imports.python_before_hmac_sha256,
  ];
  assert.equal(new Set(domains).size, domains.length);
  assert.notEqual(
    first.resources[0]?.comparisons.identity.python_before_hmac_sha256,
    second.resources[0]?.comparisons.identity.python_before_hmac_sha256,
  );
  assert.notEqual(first.report_sha256, second.report_sha256);
  assert.deepEqual(keyA, Buffer.alloc(32, 0x11));
});

test("independent evidence fails closed on output or build instability", () => {
  const unstableOutput = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "live_independent_executor",
    resourceOverride(resource) {
      if (resource.resource_type !== "zcc_device_cleanup") {
        return resource;
      }
      return {
        ...resource,
        python_after: {
          ...resource.python_after,
          projection: { changed_reference: true },
        } as ZccAdoptionParitySnapshotInput,
      };
    },
  }));
  assert.equal(
    unstableOutput.resources[0]?.comparisons.projection.status,
    "unstable_reference",
  );
  assert.equal(unstableOutput.summary.unstable_reference, 1);
  assert.equal(unstableOutput.summary.status, "unstable_reference");
  assert.equal(unstableOutput.summary.executor_qualification, "not_qualified");

  const unstableBuild = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "live_independent_executor",
    pythonAfterSha: SHA_C,
  }));
  assert.equal(unstableBuild.summary.unstable_reference, 0);
  assert.equal(unstableBuild.bindings.builds.python_stability, "unstable");
  assert.equal(unstableBuild.summary.status, "unstable_reference");
  assert.equal(unstableBuild.summary.executor_qualification, "not_qualified");

  const mismatch = buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "live_independent_executor",
    resourceOverride(resource) {
      if (resource.resource_type !== "zcc_web_privacy") {
        return resource;
      }
      return {
        ...resource,
        node: {
          ...resource.node,
          projection: { node_differs: true },
        },
      };
    },
  }));
  assert.equal(mismatch.resources[4]?.comparisons.projection.status, "mismatch");
  assert.equal(mismatch.summary.mismatched, 1);
  assert.equal(mismatch.summary.status, "different");
});

test("semantic validation rejects summary, role, digest, and scope tampering", () => {
  const report = build("live_independent_executor");
  const independentlyRehashed = structuredClone(report) as unknown as Record<
    string,
    unknown
  >;
  rehashClosedReport(independentlyRehashed);
  assert.equal(independentlyRehashed.report_sha256, report.report_sha256);

  const summary = structuredClone(report) as unknown as {
    summary: { matched: number; mismatched: number };
  };
  summary.summary.matched = 25;
  summary.summary.mismatched = 1;
  assert.equal(validateSchema(summary), true, JSON.stringify(validateSchema.errors));
  assert.equal(validateZccAdoptionOracleParityReport(summary), false);

  const role = structuredClone(report) as unknown as {
    resources: [{ comparisons: { identity: { status: string } } }];
  };
  role.resources[0]!.comparisons.identity.status = "mismatch";
  assert.equal(validateSchema(role), true, JSON.stringify(validateSchema.errors));
  assert.equal(validateZccAdoptionOracleParityReport(role), false);

  const digest = structuredClone(report) as unknown as {
    report_sha256: string;
  };
  digest.report_sha256 = SHA_C;
  assert.equal(validateZccAdoptionOracleParityReport(digest), false);

  const qualification = structuredClone(report) as unknown as {
    summary: { projection_qualification: string };
  };
  qualification.summary.projection_qualification = "qualified";
  assert.equal(validateSchema(qualification), false);
  rehashClosedReport(qualification as unknown as Record<string, unknown>);
  assert.equal(validateZccAdoptionOracleParityReport(qualification), false);

  const executorQualification = structuredClone(report) as unknown as {
    summary: { executor_qualification: string };
  };
  executorQualification.summary.executor_qualification = "qualified";
  assert.equal(validateSchema(executorQualification), false);
  rehashClosedReport(
    executorQualification as unknown as Record<string, unknown>,
  );
  assert.equal(
    validateZccAdoptionOracleParityReport(executorQualification),
    false,
  );

  const reordered = structuredClone(report) as unknown as {
    resources: ZccAdoptionOracleParityReport["resources"][number][];
  };
  [reordered.resources[0], reordered.resources[1]] = [
    reordered.resources[1]!,
    reordered.resources[0]!,
  ];
  assert.equal(validateSchema(reordered), false);
  assert.equal(validateZccAdoptionOracleParityReport(reordered), false);

  const extra = structuredClone(report) as unknown as Record<string, unknown>;
  extra.tenant = "must-not-be-accepted";
  assert.equal(validateSchema(extra), false);
  assert.equal(validateZccAdoptionOracleParityReport(extra), false);

  const cutover = structuredClone(report) as unknown as {
    summary: Record<string, unknown>;
  };
  cutover.summary.cutover_qualification = "qualified";
  assert.equal(validateSchema(cutover), false);
  assert.equal(validateZccAdoptionOracleParityReport(cutover), false);

  const aggregate = structuredClone(
    build("live_shared_observation"),
  ) as unknown as {
    summary: { live_input_coverage: string };
  };
  aggregate.summary.live_input_coverage = "incomplete";
  assert.equal(
    validateSchema(aggregate),
    true,
    JSON.stringify(validateSchema.errors),
  );
  assert.equal(validateZccAdoptionOracleParityReport(aggregate), false);

  const perResourcePresence = structuredClone(report) as unknown as {
    resources: [Record<string, unknown>];
  };
  perResourcePresence.resources[0]!.input_presence = "non_empty";
  assert.equal(validateSchema(perResourcePresence), false);
  assert.equal(
    validateZccAdoptionOracleParityReport(perResourcePresence),
    false,
  );
});

test("builder requires every exact resource once and trusted-network-only lookup", () => {
  const duplicate = buildOptions({ evidenceClass: "simulation" });
  expectInputFailure(() => buildZccAdoptionOracleParity({
    ...duplicate,
    resources: [...duplicate.resources.slice(0, 4), duplicate.resources[0]!],
  }));

  const missing = buildOptions({ evidenceClass: "simulation" });
  expectInputFailure(() => buildZccAdoptionOracleParity({
    ...missing,
    resources: missing.resources.slice(0, 4),
  }));

  const unexpectedLookup = buildOptions({
    evidenceClass: "simulation",
    resourceOverride(resource) {
      if (resource.resource_type !== "zcc_failopen_policy") {
        return resource;
      }
      return {
        ...resource,
        node: {
          ...resource.node,
          artifacts: {
            ...resource.node.artifacts,
            lookup: bytes("SECRET-MUST-NOT-LEAK"),
          },
        },
      };
    },
  });
  expectInputFailure(
    () => buildZccAdoptionOracleParity(unexpectedLookup),
  );

  const missingLookup = buildOptions({
    evidenceClass: "simulation",
    resourceOverride(resource) {
      if (resource.resource_type !== "zcc_trusted_network") {
        return resource;
      }
      return {
        ...resource,
        python_before: {
          ...resource.python_before,
          artifacts: { ...resource.python_before.artifacts, lookup: null },
        },
      };
    },
  });
  expectInputFailure(() => buildZccAdoptionOracleParity(missingLookup));
});

test("report and diagnostics expose no key, tenant data, paths, IDs, state, bytes, lengths, or child diagnostics", () => {
  const secrets = [
    "tenant-label-secret",
    "/secret/tenant/path",
    "provider-import-id-secret",
    "provider-state-secret",
    "artifact-bytes-secret",
    "child-diagnostic-secret",
  ];
  const key = Buffer.from("0123456789abcdef0123456789abcdef", "utf8");
  const resources = ZCC_ADOPTION_PARITY_RESOURCE_TYPES.map((resourceType) => {
    const secretValue = {
      tenant: secrets[0],
      path: secrets[1],
      id: secrets[2],
      state: secrets[3],
      diagnostics: [secrets[5]],
    };
    const evidence: ZccAdoptionParitySnapshotInput = {
      survivors: [secretValue],
      observations: [secretValue],
      projection: secretValue,
      artifacts: {
        tfvars: bytes(secrets[4]!),
        imports: bytes(`${secrets[4]}-imports`),
        lookup: resourceType === "zcc_trusted_network"
          ? bytes(`${secrets[4]}-lookup`)
          : null,
      },
    };
    return {
      resource_type: resourceType,
      python_before: evidence,
      node: evidence,
      python_after: null,
    };
  });
  const report = buildZccAdoptionOracleParity({
    evidenceClass: "live_shared_observation",
    commitmentKey: key,
    builds: {
      python_before_sha256: SHA_A,
      python_after_sha256: null,
      node_sha256: SHA_B,
    },
    resources,
  });
  const text = JSON.stringify(report);
  for (const secret of [...secrets, key.toString("hex")]) {
    assert.equal(text.includes(secret), false, secret);
  }
  const forbiddenKeys = new Set([
    "tenant",
    "path",
    "id",
    "state",
    "bytes",
    "length",
    "size_bytes",
    "diagnostics",
  ]);
  const inspect = (value: unknown): void => {
    if (Array.isArray(value)) {
      value.forEach(inspect);
      return;
    }
    if (typeof value !== "object" || value === null) {
      return;
    }
    for (const [name, child] of Object.entries(value)) {
      assert.equal(forbiddenKeys.has(name), false, name);
      inspect(child);
    }
  };
  inspect(report);
});

test("builder rejects bad keys, after-pass inconsistencies, and hostile values without leaking input", () => {
  for (const size of [0, 31, 33]) {
    expectInputFailure(() => buildZccAdoptionOracleParity(buildOptions({
      evidenceClass: "simulation",
      key: Buffer.alloc(size),
    })));
  }
  expectInputFailure(() => buildZccAdoptionOracleParity(buildOptions({
    evidenceClass: "simulation",
    pythonAfterSha: SHA_A,
  })));

  const noAfter = buildOptions({ evidenceClass: "live_independent_executor" });
  expectInputFailure(() => buildZccAdoptionOracleParity({
    ...noAfter,
    resources: noAfter.resources.map((resource, index) => {
      return index === 0 ? { ...resource, python_after: null } : resource;
    }),
  }));

  let getterCalls = 0;
  const hostile: Record<string, unknown> = {};
  Object.defineProperty(hostile, "SECRET-MUST-NOT-LEAK", {
    enumerable: true,
    get() {
      getterCalls += 1;
      return "SECRET-MUST-NOT-LEAK";
    },
  });
  const hostileOptions = buildOptions({
    evidenceClass: "simulation",
    resourceOverride(resource) {
      return resource.resource_type === "zcc_device_cleanup"
        ? {
            ...resource,
            node: { ...resource.node, projection: hostile },
          }
        : resource;
    },
  });
  expectInputFailure(
    () => buildZccAdoptionOracleParity(hostileOptions),
  );
  assert.equal(getterCalls, 0);
});

test("canonical value commitments ignore record insertion order", () => {
  const options = buildOptions({
    evidenceClass: "simulation",
    resourceOverride(resource) {
      if (resource.resource_type !== "zcc_device_cleanup") {
        return resource;
      }
      return {
        ...resource,
        python_before: {
          ...resource.python_before,
          survivors: [{ b: 2, a: 1 }],
        },
        node: {
          ...resource.node,
          survivors: [{ a: 1, b: 2 }],
        },
      };
    },
  });
  const report = buildZccAdoptionOracleParity(options);
  assert.equal(report.resources[0]?.comparisons.identity.status, "match");
});

test("v1 fixed known-answer vectors freeze Unicode, media, number, and report encoding", () => {
  const report = buildZccAdoptionOracleParity(knownAnswerOptions());
  const first = report.resources[0]?.comparisons;
  assert.notEqual(first, undefined);
  if (first === undefined) {
    return;
  }
  assert.equal(
    first.identity.python_before_hmac_sha256,
    "4448b82d8ee04a1e8f934508f1960a96b88e10adaaabe7eb8739a8b08dfd6dbb",
  );
  assert.equal(first.identity.status, "match");
  assert.equal(
    first.observation.python_before_hmac_sha256,
    "78560b006d6c28056b481b7ae93707714909f53f9665c9c54afb96ab0a6ae64e",
  );
  assert.equal(
    first.projection.python_before_hmac_sha256,
    "2cf99c544cd1ff72ec5ea32c3ab1f9758cd3af1c5140788a80cce720e8e5b488",
  );
  assert.equal(
    first.tfvars.python_before_hmac_sha256,
    "b05cf553487ebdede9f7e71cf5d32ddf756dd80e88831207e03ec495d1c92b6d",
  );
  assert.notEqual(
    first.identity.python_before_hmac_sha256,
    first.tfvars.python_before_hmac_sha256,
  );
  assert.equal(
    report.report_sha256,
    "3e9d030b18bb9813b3a450dff23ec2adce976580ee3799f3952d90fccf6c0782",
  );

  const positiveZero = buildZccAdoptionOracleParity(knownAnswerOptions(0));
  assert.equal(
    positiveZero.resources[0]?.comparisons.projection.node_hmac_sha256,
    "ed30c84bf34528f3e4ea9f8c60ecb20855e6ba51aa0bfe57c5c38335f81bc833",
  );
  assert.equal(
    positiveZero.resources[0]?.comparisons.projection.status,
    "mismatch",
  );

  const losslessNegativeZero = buildZccAdoptionOracleParity(
    knownAnswerOptions(new LosslessNumber("-0")),
  );
  assert.equal(
    losslessNegativeZero.resources[0]?.comparisons.projection.node_hmac_sha256,
    "2cf99c544cd1ff72ec5ea32c3ab1f9758cd3af1c5140788a80cce720e8e5b488",
  );
  assert.equal(
    losslessNegativeZero.resources[0]?.comparisons.projection.status,
    "match",
  );

  const losslessFloat = buildZccAdoptionOracleParity(
    knownAnswerOptions(new LosslessNumber("-0.0")),
  );
  assert.equal(
    losslessFloat.resources[0]?.comparisons.projection.node_hmac_sha256,
    "60e53b99e550cc23eadf8a1093f6dc6e4dcddedb127e863b60ba9005542dbecb",
  );
  assert.equal(losslessFloat.resources[0]?.comparisons.projection.status, "mismatch");

  const losslessExponent = buildZccAdoptionOracleParity(
    knownAnswerOptions(new LosslessNumber("1e0")),
  );
  assert.equal(
    losslessExponent.resources[0]?.comparisons.projection.node_hmac_sha256,
    "154965d19783f0df39bbf0cc37061bb21a7b040d41db39da6b6d08a330ddcf0b",
  );
});
