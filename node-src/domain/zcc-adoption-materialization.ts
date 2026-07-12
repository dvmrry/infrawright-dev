import { types as utilTypes } from "node:util";

import {
  schemaErrorDetails,
  validateZccAdoptionArtifactParity,
  validateZccAdoptionArtifactSet,
} from "../contracts/validators.js";
import { snapshotPlainJsonGraph } from "../json/supported-json-graph.js";
import type { ZccAdoptionArtifactParity } from "./zcc-adoption-artifact-parity.js";
import { compareZccAdoptionArtifactDigests } from "./zcc-adoption-artifact-parity.js";
import type { ZccAdoptionArtifactSet } from "./zcc-adoption-artifacts.js";
import { ProcessFailure } from "./errors.js";
import {
  materializeReadyZccBootstrapArtifacts,
  type ZccPullMaterializationHooks,
  type ZccPullMaterializedArtifactName,
} from "./zcc-pull-materialization.js";

export interface ZccAdoptionArtifactMaterialization {
  readonly kind: "infrawright.zcc_adoption_artifact_materialization";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly product: "zcc";
  readonly resource_type: ZccAdoptionArtifactSet["resource_type"];
  readonly tenant: string;
  readonly status: "complete";
  readonly publication: {
    readonly policy: "create_or_verify_exact";
    readonly created: readonly ZccPullMaterializedArtifactName[];
    readonly reused: readonly ZccPullMaterializedArtifactName[];
  };
  readonly verification: ZccAdoptionArtifactParity;
}

function invalidInput(): never {
  throw new ProcessFailure({
    code: "INVALID_MATERIALIZATION_INPUT",
    category: "domain",
    message: "adoption materialization inputs must be acyclic inert values",
  });
}

function inertRecord(
  value: unknown,
  allowed: ReadonlySet<string>,
): Readonly<Record<string, PropertyDescriptor>> {
  if (
    typeof value !== "object"
    || value === null
    || utilTypes.isProxy(value)
  ) {
    return invalidInput();
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return invalidInput();
  }
  const keys = Reflect.ownKeys(value);
  if (keys.some((key) => typeof key !== "string" || !allowed.has(key))) {
    return invalidInput();
  }
  const output: Record<string, PropertyDescriptor> = Object.create(null) as Record<
    string,
    PropertyDescriptor
  >;
  for (const key of keys as string[]) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined || !("value" in descriptor)) {
      return invalidInput();
    }
    output[key] = descriptor;
  }
  return output;
}

function requiredValue(
  descriptors: Readonly<Record<string, PropertyDescriptor>>,
  key: string,
): unknown {
  const descriptor = descriptors[key];
  return descriptor === undefined ? invalidInput() : descriptor.value;
}

const HOOK_NAMES = new Set([
  "afterPreflight",
  "afterDirectoriesReady",
  "afterTempOpen",
  "afterStaged",
  "beforePrepublishRecheck",
  "beforePublish",
  "afterLink",
  "afterPublish",
  "beforePostpublishRecheck",
]);

function snapshotHooks(value: unknown): ZccPullMaterializationHooks | undefined {
  if (value === undefined) {
    return undefined;
  }
  const descriptors = inertRecord(value, HOOK_NAMES);
  const output: Record<string, unknown> = Object.create(null) as Record<
    string,
    unknown
  >;
  for (const [key, descriptor] of Object.entries(descriptors)) {
    if (typeof descriptor.value !== "function") {
      return invalidInput();
    }
    output[key] = descriptor.value;
  }
  return Object.freeze(output) as ZccPullMaterializationHooks;
}

function stableJson(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => stableJson(entry)).join(",")}]`;
  }
  const record = value as Readonly<Record<string, unknown>>;
  return `{${Object.keys(record).sort().map((key) => {
    return `${JSON.stringify(key)}:${stableJson(record[key])}`;
  }).join(",")}}`;
}

function inertSnapshot(value: unknown): unknown {
  const snapshot = snapshotPlainJsonGraph(value, { maxDepth: 64 });
  if (!snapshot.ok) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_INPUT",
      category: "domain",
      message: "adoption materialization inputs must be acyclic inert JSON",
    });
  }
  return snapshot.value;
}

/** Snapshot and reject a malformed or non-ready assertion before operation I/O. */
export function snapshotReadyZccAdoptionMaterializationAssertion(
  value: unknown,
): ZccAdoptionArtifactParity {
  const assertion = inertSnapshot(value) as ZccAdoptionArtifactParity;
  if (!validateZccAdoptionArtifactParity(assertion)) {
    throw new ProcessFailure({
      code: "INVALID_MATERIALIZATION_ASSERTION",
      category: "domain",
      message: "the adoption parity assertion failed its versioned contract",
      details: schemaErrorDetails(validateZccAdoptionArtifactParity.errors),
    });
  }
  if (assertion.status !== "ready" || assertion.parity.status !== "equal") {
    throw new ProcessFailure({
      code: "MATERIALIZATION_ASSERTION_MISMATCH",
      category: "domain",
      message: "only a complete ready adoption parity assertion can authorize publication",
    });
  }
  return assertion;
}

function snapshotCandidate(value: unknown): ZccAdoptionArtifactSet {
  const candidate = inertSnapshot(value) as ZccAdoptionArtifactSet;
  if (!validateZccAdoptionArtifactSet(candidate)) {
    throw new ProcessFailure({
      code: "INVALID_ZCC_ADOPTION_CANDIDATE",
      category: "internal",
      message: "provider-observed candidate failed its versioned contract",
      details: schemaErrorDetails(validateZccAdoptionArtifactSet.errors),
    });
  }
  return candidate;
}

function cleanParity(candidate: ZccAdoptionArtifactSet): ZccAdoptionArtifactParity {
  return compareZccAdoptionArtifactDigests({
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

/** Publish one freshly provider-observed, independently asserted bootstrap set. */
export async function materializeReadyZccAdoptionArtifacts(options: {
  readonly outputRoot: string;
  readonly pathBase: string;
  readonly candidate: ZccAdoptionArtifactSet;
  readonly assertion: ZccAdoptionArtifactParity;
  readonly recheckInputs: () => Promise<void>;
  readonly hooks?: ZccPullMaterializationHooks;
}): Promise<ZccAdoptionArtifactMaterialization> {
  // Snapshot every caller-retained value and callback before publication can
  // await filesystem work.
  const descriptors = inertRecord(options, new Set([
    "outputRoot",
    "pathBase",
    "candidate",
    "assertion",
    "recheckInputs",
    "hooks",
  ]));
  const candidate = snapshotCandidate(
    requiredValue(descriptors, "candidate"),
  );
  const assertion = snapshotReadyZccAdoptionMaterializationAssertion(
    requiredValue(descriptors, "assertion"),
  );
  const verification = cleanParity(candidate);
  if (
    verification.status !== "ready"
    || verification.parity.status !== "equal"
    || stableJson(assertion) !== stableJson(verification)
  ) {
    throw new ProcessFailure({
      code: "MATERIALIZATION_ASSERTION_MISMATCH",
      category: "domain",
      message: "the ready adoption parity assertion does not exactly match the fresh candidate",
    });
  }
  const outputRoot = requiredValue(descriptors, "outputRoot");
  const pathBase = requiredValue(descriptors, "pathBase");
  const recheckInputs = requiredValue(descriptors, "recheckInputs");
  if (
    typeof outputRoot !== "string"
    || typeof pathBase !== "string"
    || typeof recheckInputs !== "function"
  ) {
    return invalidInput();
  }
  const recheck = recheckInputs as () => Promise<void>;
  const hooks = snapshotHooks(descriptors.hooks?.value);
  return materializeReadyZccBootstrapArtifacts({
    outputRoot,
    pathBase,
    candidate,
    asserted: verification,
    recheckInputs: recheck,
    cleanVerification: cleanParity,
    verificationReady: (current) => {
      return current.status === "ready" && current.parity.status === "equal";
    },
    buildResult: ({ candidate: current, created, reused, verification: final }) => ({
      kind: "infrawright.zcc_adoption_artifact_materialization",
      schema_version: 1,
      mode: "bootstrap",
      product: "zcc",
      resource_type: current.resource_type,
      tenant: current.tenant,
      status: "complete",
      publication: {
        policy: "create_or_verify_exact",
        created,
        reused,
      },
      verification: final,
    }),
    ...(hooks === undefined ? {} : { hooks }),
  });
}
