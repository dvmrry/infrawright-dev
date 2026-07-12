import {
  schemaErrorDetails,
  validateZccAdoptionArtifactSet,
} from "../contracts/validators.js";
import type { ZccAdoptionArtifactSet } from "./zcc-adoption-artifacts.js";
import { ProcessFailure } from "./errors.js";
import type {
  ZccMaterializedPullArtifactDigests,
  ZccPullArtifactDigest,
} from "./zcc-pull-parity.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";

export interface ZccAdoptionParityArtifactDigest {
  readonly path: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccAdoptionParityReferenceDigest {
  readonly path: string;
  readonly sha256: string | null;
  readonly size_bytes: number | null;
}

export interface ZccApplicableAdoptionArtifactParity {
  readonly candidate: ZccAdoptionParityArtifactDigest;
  readonly reference: ZccAdoptionParityReferenceDigest;
  readonly status: "equal" | "different";
}

export interface ZccNonApplicableAdoptionArtifactParity {
  readonly candidate: null;
  readonly reference: null;
  readonly status: "not_applicable";
}

export type ZccAdoptionArtifactParityEntry =
  | ZccApplicableAdoptionArtifactParity
  | ZccNonApplicableAdoptionArtifactParity;

/**
 * Content-free parity between one provider-observed candidate and the stable
 * materialized bootstrap reference selected by the process workspace.
 */
export interface ZccAdoptionArtifactParity {
  readonly kind: "infrawright.zcc_adoption_artifact_parity";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly reference: "materialized";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly source: ZccAdoptionArtifactSet["source"];
  readonly catalog: ZccAdoptionArtifactSet["catalog"];
  readonly root: ZccAdoptionArtifactSet["root"];
  readonly status: "ready" | "review_required";
  readonly parity: {
    readonly status: "equal" | "different";
    readonly equal: number;
    readonly different: number;
    readonly artifacts: {
      readonly tfvars: ZccApplicableAdoptionArtifactParity;
      readonly imports: ZccApplicableAdoptionArtifactParity;
      readonly lookup: ZccAdoptionArtifactParityEntry;
    };
  };
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((entry) => immutableCopy(entry)));
  }
  if (typeof value === "object" && value !== null) {
    const output: Record<string, unknown> = Object.create(null) as Record<
      string,
      unknown
    >;
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy(
        (value as Readonly<Record<string, unknown>>)[key],
      );
    }
    return Object.freeze(output);
  }
  return value;
}

function candidateDigest(
  artifact: ZccAdoptionArtifactSet["artifacts"]["tfvars"],
): ZccAdoptionParityArtifactDigest {
  return {
    path: artifact.path,
    sha256: artifact.sha256,
    size_bytes: artifact.size_bytes,
  };
}

function referenceDigest(
  path: string,
  digest: ZccPullArtifactDigest | null,
): ZccAdoptionParityReferenceDigest {
  return {
    path,
    sha256: digest?.sha256 ?? null,
    size_bytes: digest?.size_bytes ?? null,
  };
}

function applicableParity(
  artifact: ZccAdoptionArtifactSet["artifacts"]["tfvars"],
  reference: ZccPullArtifactDigest | null,
): ZccApplicableAdoptionArtifactParity {
  const candidate = candidateDigest(artifact);
  const materialized = referenceDigest(artifact.path, reference);
  const equal = reference !== null
    && candidate.sha256 === reference.sha256
    && candidate.size_bytes === reference.size_bytes;
  return {
    candidate,
    reference: materialized,
    status: equal ? "equal" : "different",
  };
}

/** Compare only bounded artifact coordinates and digests; never retain bytes. */
export function compareZccAdoptionArtifactDigests(options: {
  readonly candidate: ZccAdoptionArtifactSet;
  readonly materialized: ZccMaterializedPullArtifactDigests;
}): ZccAdoptionArtifactParity {
  if (!validateZccAdoptionArtifactSet(options.candidate)) {
    throw new ProcessFailure({
      code: "INVALID_ZCC_ADOPTION_CANDIDATE",
      category: "internal",
      message: "provider-observed candidate failed its versioned contract",
      details: schemaErrorDetails(validateZccAdoptionArtifactSet.errors),
    });
  }

  const tfvars = applicableParity(
    options.candidate.artifacts.tfvars,
    options.materialized.tfvars,
  );
  const imports = applicableParity(
    options.candidate.artifacts.imports,
    options.materialized.imports,
  );
  const lookup = options.candidate.artifacts.lookup === null
    ? {
        candidate: null,
        reference: null,
        status: "not_applicable" as const,
      }
    : applicableParity(
        options.candidate.artifacts.lookup,
        options.materialized.lookup,
      );
  const applicable = [tfvars, imports, lookup].filter(
    (entry): entry is ZccApplicableAdoptionArtifactParity => {
      return entry.status !== "not_applicable";
    },
  );
  const equal = applicable.filter((entry) => entry.status === "equal").length;
  const different = applicable.length - equal;
  const parityStatus = different === 0 ? "equal" : "different";

  return immutableCopy({
    kind: "infrawright.zcc_adoption_artifact_parity",
    schema_version: 1,
    mode: "bootstrap",
    reference: "materialized",
    product: "zcc",
    resource_type: options.candidate.resource_type,
    tenant: options.candidate.tenant,
    source: { ...options.candidate.source },
    catalog: { ...options.candidate.catalog },
    root: {
      ...options.candidate.root,
      members: [...options.candidate.root.members],
    },
    status: parityStatus === "equal" ? "ready" : "review_required",
    parity: {
      status: parityStatus,
      equal,
      different,
      artifacts: { tfvars, imports, lookup },
    },
  }) as ZccAdoptionArtifactParity;
}
