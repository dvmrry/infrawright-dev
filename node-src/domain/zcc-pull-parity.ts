import type {
  ZccPullArtifactSet,
  ZccPullResourceType,
} from "./zcc-pull-artifacts.js";
import {
  schemaErrorDetails,
  validateZccPullArtifactSet,
} from "../contracts/validators.js";
import { ProcessFailure } from "./errors.js";

export interface ZccPullArtifactDigest {
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccMaterializedPullArtifactDigests {
  readonly tfvars: ZccPullArtifactDigest | null;
  readonly imports: ZccPullArtifactDigest | null;
  readonly lookup: ZccPullArtifactDigest | null;
}

export interface ZccApplicablePullArtifactParity {
  readonly path: string;
  readonly expected: ZccPullArtifactDigest;
  readonly observed: ZccPullArtifactDigest | null;
  readonly status: "match" | "mismatch" | "missing";
}

export interface ZccNonApplicablePullArtifactParity {
  readonly path: null;
  readonly expected: null;
  readonly observed: null;
  readonly status: "not_applicable";
}

export type ZccPullArtifactParityEntry =
  | ZccApplicablePullArtifactParity
  | ZccNonApplicablePullArtifactParity;

export interface ZccPullArtifactParity {
  readonly kind: "infrawright.zcc_pull_artifact_parity";
  readonly schema_version: 1;
  readonly mode: "bootstrap";
  readonly reference: "materialized";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly source: ZccPullArtifactSet["source"];
  readonly catalog: ZccPullArtifactSet["catalog"];
  readonly root: ZccPullArtifactSet["root"];
  readonly candidate: {
    readonly status: ZccPullArtifactSet["status"];
    readonly unexpected_drops: readonly string[];
  };
  readonly status: "ready" | "review_required";
  readonly parity: {
    readonly status: "equal" | "different";
    readonly matched: number;
    readonly mismatched: number;
    readonly missing: number;
    readonly artifacts: {
      readonly tfvars: ZccApplicablePullArtifactParity;
      readonly imports: ZccApplicablePullArtifactParity;
      readonly lookup: ZccPullArtifactParityEntry;
    };
  };
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((item) => immutableCopy(item)));
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

function digestOf(
  artifact: ZccPullArtifactSet["artifacts"]["tfvars"],
): ZccPullArtifactDigest {
  return {
    sha256: artifact.sha256,
    size_bytes: artifact.size_bytes,
  };
}

function applicableParity(
  artifact: ZccPullArtifactSet["artifacts"]["tfvars"],
  observed: ZccPullArtifactDigest | null,
): ZccApplicablePullArtifactParity {
  const expected = digestOf(artifact);
  let status: ZccApplicablePullArtifactParity["status"];
  if (observed === null) {
    status = "missing";
  } else if (
    observed.sha256 === expected.sha256
    && observed.size_bytes === expected.size_bytes
  ) {
    status = "match";
  } else {
    status = "mismatch";
  }
  return {
    path: artifact.path,
    expected,
    observed,
    status,
  };
}

/** Build a value-safe, digest-only parity report from one trusted candidate. */
export function compareZccPullArtifactDigests(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly materialized: ZccMaterializedPullArtifactDigests;
}): ZccPullArtifactParity {
  if (!validateZccPullArtifactSet(options.candidate)) {
    throw new ProcessFailure({
      code: "INVALID_ZCC_ARTIFACT_CANDIDATE",
      category: "internal",
      message: "compiled pull artifact candidate failed its versioned contract",
      details: schemaErrorDetails(validateZccPullArtifactSet.errors),
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
        path: null,
        expected: null,
        observed: null,
        status: "not_applicable" as const,
      }
    : applicableParity(
        options.candidate.artifacts.lookup,
        options.materialized.lookup,
      );
  const applicable = [tfvars, imports, lookup].filter(
    (artifact): artifact is ZccApplicablePullArtifactParity => {
      return artifact.status !== "not_applicable";
    },
  );
  const matched = applicable.filter((artifact) => artifact.status === "match").length;
  const mismatched = applicable.filter(
    (artifact) => artifact.status === "mismatch",
  ).length;
  const missing = applicable.filter(
    (artifact) => artifact.status === "missing",
  ).length;
  const parityStatus = mismatched === 0 && missing === 0
    ? "equal"
    : "different";
  const report: ZccPullArtifactParity = {
    kind: "infrawright.zcc_pull_artifact_parity",
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
    candidate: {
      status: options.candidate.status,
      unexpected_drops: [...options.candidate.unexpected_drops],
    },
    status: options.candidate.status === "ready" && parityStatus === "equal"
      ? "ready"
      : "review_required",
    parity: {
      status: parityStatus,
      matched,
      mismatched,
      missing,
      artifacts: { tfvars, imports, lookup },
    },
  };
  return immutableCopy(report) as ZccPullArtifactParity;
}
