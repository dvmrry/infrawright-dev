import { createHash } from "node:crypto";
import path from "node:path";

import { validateZccPullArtifactSet } from "../contracts/validators.js";
import {
  deriveImportMoves,
  parseGeneratedImports,
  renderMovedBlocks,
  type ImportMoveSuppressionReason,
} from "./import-moves.js";
import { ProcessFailure } from "./errors.js";
import {
  zccRefreshBaselineFingerprint,
  zccRefreshTransitionFingerprint,
} from "./zcc-pull-refresh-fingerprints.js";
import { matchesPythonCollapsedRefreshLookup } from "./zcc-pull-refresh-lookup.js";
import type {
  ZccPullArtifactSet,
  ZccPullResourceType,
  ZccTextArtifact,
} from "./zcc-pull-artifacts.js";

export interface ZccRefreshBaselineArtifact {
  readonly path: string;
  readonly state: "present";
  readonly sha256: string;
  readonly size_bytes: number;
}

export interface ZccRefreshAbsentArtifact {
  readonly path: string;
  readonly state: "absent";
}

export type ZccRefreshBaselineState =
  | ZccRefreshBaselineArtifact
  | ZccRefreshAbsentArtifact;

export type ZccRefreshBaselineInputState =
  | ZccRefreshAbsentArtifact
  | {
      readonly path: string;
      readonly state: "present";
      readonly content: Uint8Array;
    };

export interface ZccRefreshMove {
  readonly from_key: string;
  readonly to_key: string;
}

export interface ZccRefreshMoveSuppression extends ZccRefreshMove {
  readonly reason: ImportMoveSuppressionReason;
}

export type ZccRefreshDesiredArtifact =
  | ZccRefreshAbsentArtifact
  | {
      readonly state: "present";
      readonly artifact: ZccTextArtifact;
    };

export interface ZccPullRefreshArtifactSet {
  readonly kind: "infrawright.zcc_pull_refresh_artifact_set";
  readonly schema_version: 1;
  readonly mode: "refresh";
  readonly product: "zcc";
  readonly resource_type: ZccPullResourceType;
  readonly tenant: string;
  readonly source: ZccPullArtifactSet["source"];
  readonly catalog: ZccPullArtifactSet["catalog"];
  readonly root: ZccPullArtifactSet["root"];
  readonly baseline: {
    readonly tfvars: ZccRefreshBaselineState;
    readonly imports: ZccRefreshBaselineArtifact;
    readonly lookup: ZccRefreshBaselineState;
    readonly moves: ZccRefreshAbsentArtifact;
    readonly pending_moves: ZccRefreshAbsentArtifact;
    readonly alternate_hcl: ZccRefreshAbsentArtifact;
    readonly generated_bindings: ZccRefreshAbsentArtifact;
    readonly fingerprint_sha256: string;
  };
  readonly status: "ready" | "review_required";
  readonly unexpected_drops: readonly string[];
  readonly moves: {
    readonly safe: readonly ZccRefreshMove[];
    readonly suppressed: readonly ZccRefreshMoveSuppression[];
  };
  readonly desired: {
    readonly tfvars: ZccRefreshDesiredArtifact;
    readonly imports: ZccRefreshDesiredArtifact;
    readonly lookup: ZccRefreshDesiredArtifact;
    readonly moves: ZccRefreshDesiredArtifact;
  };
  readonly transition_sha256: string;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function plainRecord(value: unknown): Readonly<Record<string, unknown>> | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as Readonly<Record<string, unknown>>
    : null;
}

function ownValue(
  value: Readonly<Record<string, unknown>>,
  key: string,
): unknown {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

function refreshCandidateImportIds(
  candidate: ZccPullArtifactSet,
): readonly string[] | null {
  try {
    return parseGeneratedImports(
      candidate.resource_type,
      candidate.artifacts.imports.content,
    ).map((entry) => entry.importId);
  } catch {
    return null;
  }
}

function isRefreshCandidate(candidate: unknown): boolean {
  const candidateRecord = plainRecord(candidate);
  if (candidateRecord === null) {
    return false;
  }
  const resourceType = ownValue(candidateRecord, "resource_type");
  if (typeof resourceType !== "string") {
    return false;
  }
  const candidateSnapshot = candidate as ZccPullArtifactSet;
  if (validateZccPullArtifactSet(candidate)) {
    return true;
  }
  const errors = validateZccPullArtifactSet.errors ?? [];
  const allowedRules = resourceType === "zcc_trusted_network"
    ? new Set(["import_ids", "lookup_join"])
    : new Set(["import_ids"]);
  if (
    errors.length === 0
    || errors.some((error) => {
      const params = error.params as { readonly rule?: unknown };
      return typeof params.rule !== "string" || !allowedRules.has(params.rule);
    })
  ) {
    return false;
  }
  const ids = refreshCandidateImportIds(candidateSnapshot);
  const duplicateIdsAreValid = ids !== null
    && ids.length > 1
    && ids.every((id) => !PYTHON_WHITESPACE_ONLY.test(id))
    && new Set(ids).size < ids.length;
  if (!duplicateIdsAreValid) {
    return false;
  }
  return resourceType !== "zcc_trusted_network"
    || matchesPythonCollapsedRefreshLookup({
      resourceType,
      importsContent: candidateSnapshot.artifacts.imports.content,
      lookupContent: candidateSnapshot.artifacts.lookup?.content,
      tfvarsContent: candidateSnapshot.artifacts.tfvars.content,
      variableName: candidateSnapshot.root.variable_name,
    });
}

function textArtifact(path: string, content: string): ZccTextArtifact {
  if (!content.isWellFormed()) {
    return fail(
      "INVALID_ZCC_REFRESH_CANDIDATE",
      "refresh artifact contains unsupported Unicode",
    );
  }
  const bytes = Buffer.from(content, "utf8");
  return {
    path,
    media_type: "text/x-hcl",
    encoding: "utf-8",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
    content,
  };
}

function expectedMovesPath(importsPath: string): string | null {
  return importsPath.endsWith("_imports.tf")
    ? importsPath.slice(0, -"_imports.tf".length) + "_moves.tf"
    : null;
}

function expectedPendingMovesPath(importsPath: string): string | null {
  return importsPath.endsWith("_imports.tf")
    ? importsPath.slice(0, -"_imports.tf".length) + "_moves.pending.json"
    : null;
}

function expectedLookupPath(candidate: ZccPullArtifactSet): string {
  return `${path.posix.dirname(candidate.artifacts.tfvars.path)}`
    + `/${candidate.resource_type}.lookup.json`;
}

function copyBaselineState(
  value: ZccRefreshBaselineInputState,
  expectedPath: string,
): ZccRefreshBaselineState {
  const input = plainRecord(value as unknown);
  const pathValue = input === null ? null : ownValue(input, "path");
  const stateValue = input === null ? null : ownValue(input, "state");
  const contentValue = input === null ? null : ownValue(input, "content");
  if (
    input === null
    || pathValue !== expectedPath
    || typeof pathValue !== "string"
    || !pathValue.isWellFormed()
    || pathValue.includes("\0")
  ) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh baseline paths do not match the compiled artifact target",
    );
  }
  if (stateValue === "absent") {
    return { path: pathValue, state: "absent" };
  }
  if (
    stateValue !== "present"
    || !(contentValue instanceof Uint8Array)
    || contentValue.byteLength > 32 * 1024 * 1024
  ) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh baseline evidence is invalid",
    );
  }
  const bytes = Buffer.from(contentValue);
  return {
    path: pathValue,
    state: "present",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
  };
}

function present(artifact: ZccTextArtifact): ZccRefreshDesiredArtifact {
  return { state: "present", artifact };
}

function safeRecord(
  entries: Iterable<readonly [string, unknown]>,
): Record<string, unknown> {
  const output: Record<string, unknown> = Object.create(null) as Record<
    string,
    unknown
  >;
  for (const [key, value] of entries) {
    Object.defineProperty(output, key, {
      configurable: true,
      enumerable: true,
      value,
      writable: true,
    });
  }
  return output;
}

function immutableCopy(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map((item) => immutableCopy(item)));
  }
  if (typeof value === "object" && value !== null) {
    const output = safeRecord([]);
    for (const key of Object.keys(value)) {
      output[key] = immutableCopy(
        (value as Readonly<Record<string, unknown>>)[key],
      );
    }
    return Object.freeze(output);
  }
  return value;
}

/**
 * Derive one read-only refresh desired state from a stable prior imports
 * snapshot and one already-compiled candidate. The baseline import IDs are
 * used in memory only; the result deliberately emits no redundant IDs.
 */
export function compileZccPullRefreshArtifactSet(options: {
  readonly candidate: ZccPullArtifactSet;
  readonly baselineImports: {
    readonly path: string;
    readonly content: string;
  };
  readonly baselineTfvars: ZccRefreshBaselineInputState;
  readonly baselineLookup: ZccRefreshBaselineInputState;
  readonly movesPath: string;
  readonly pendingMovesPath: string;
  readonly alternateHclPath: string;
  readonly generatedBindingsPath: string;
}): ZccPullRefreshArtifactSet {
  const input = plainRecord(options as unknown);
  const baselineImportsInput = plainRecord(
    input === null ? null : ownValue(input, "baselineImports"),
  );
  if (
    input === null
    || baselineImportsInput === null
    || typeof ownValue(baselineImportsInput, "path") !== "string"
    || typeof ownValue(baselineImportsInput, "content") !== "string"
    || plainRecord(ownValue(input, "baselineTfvars")) === null
    || plainRecord(ownValue(input, "baselineLookup")) === null
    || typeof ownValue(input, "movesPath") !== "string"
    || typeof ownValue(input, "pendingMovesPath") !== "string"
    || typeof ownValue(input, "alternateHclPath") !== "string"
    || typeof ownValue(input, "generatedBindingsPath") !== "string"
  ) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh constructor inputs are invalid",
    );
  }
  const candidateInput = ownValue(input, "candidate");
  const baselineImportsPath = ownValue(baselineImportsInput, "path") as string;
  const baselineImportsContent = ownValue(
    baselineImportsInput,
    "content",
  ) as string;
  const baselineTfvarsInput = ownValue(
    input,
    "baselineTfvars",
  ) as ZccRefreshBaselineInputState;
  const baselineLookupInput = ownValue(
    input,
    "baselineLookup",
  ) as ZccRefreshBaselineInputState;
  const movesPathInput = ownValue(input, "movesPath") as string;
  const pendingMovesPathInput = ownValue(input, "pendingMovesPath") as string;
  const alternateHclPathInput = ownValue(input, "alternateHclPath") as string;
  const generatedBindingsPathInput = ownValue(
    input,
    "generatedBindingsPath",
  ) as string;
  let candidate: ZccPullArtifactSet;
  try {
    candidate = structuredClone(candidateInput) as ZccPullArtifactSet;
  } catch {
    return fail(
      "INVALID_ZCC_REFRESH_CANDIDATE",
      "refresh candidate is not a supported artifact set",
    );
  }
  if (!isRefreshCandidate(candidate)) {
    return fail(
      "INVALID_ZCC_REFRESH_CANDIDATE",
      "refresh candidate is not a supported artifact set",
    );
  }
  const importsPath = candidate.artifacts.imports.path;
  const movesPath = movesPathInput;
  const pendingMovesPath = pendingMovesPathInput;
  const alternateHclPath = alternateHclPathInput;
  const generatedBindingsPath = generatedBindingsPathInput;
  const expectedAlternateHcl = candidate.artifacts.tfvars.path.endsWith(".json")
    ? candidate.artifacts.tfvars.path.slice(0, -".json".length)
    : null;
  const expectedGeneratedBindings = `${path.posix.dirname(
    candidate.artifacts.tfvars.path,
  )}/${candidate.resource_type}.generated.expressions.json`;
  if (
    baselineImportsPath !== importsPath
    || !baselineImportsPath.isWellFormed()
    || baselineImportsPath.includes("\0")
    || expectedMovesPath(importsPath) !== movesPath
    || !movesPath.isWellFormed()
    || movesPath.includes("\0")
    || expectedPendingMovesPath(importsPath) !== pendingMovesPath
    || !pendingMovesPath.isWellFormed()
    || pendingMovesPath.includes("\0")
    || expectedAlternateHcl !== alternateHclPath
    || !alternateHclPath.isWellFormed()
    || alternateHclPath.includes("\0")
    || expectedGeneratedBindings !== generatedBindingsPath
    || !generatedBindingsPath.isWellFormed()
    || generatedBindingsPath.includes("\0")
  ) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh baseline paths do not match the compiled artifact target",
    );
  }
  const baselineContent = baselineImportsContent;
  if (typeof baselineContent !== "string" || !baselineContent.isWellFormed()) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh imports baseline is not supported text",
    );
  }
  const baselineBytes = Buffer.from(baselineContent, "utf8");
  if (baselineBytes.length > 32 * 1024 * 1024) {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "refresh imports baseline exceeds the supported size",
    );
  }
  const baselineTfvars = copyBaselineState(
    baselineTfvarsInput,
    candidate.artifacts.tfvars.path,
  );
  const lookupPath = candidate.artifacts.lookup?.path
    ?? expectedLookupPath(candidate);
  const baselineLookup = copyBaselineState(baselineLookupInput, lookupPath);
  if (candidate.artifacts.lookup === null && baselineLookup.state !== "absent") {
    return fail(
      "INVALID_ZCC_REFRESH_BASELINE",
      "a non-applicable lookup baseline must be absent",
    );
  }
  const derivation = deriveImportMoves(
    candidate.resource_type,
    baselineContent,
    candidate.artifacts.imports.content,
  );
  const desiredImportIds = refreshCandidateImportIds(candidate);
  const duplicateDesiredImportIds = desiredImportIds !== null
    && new Set(desiredImportIds).size < desiredImportIds.length;
  if (
    duplicateDesiredImportIds
    && !derivation.suppressed.some((item) => item.reason === "duplicate_from")
  ) {
    return fail(
      "INVALID_ZCC_REFRESH_CANDIDATE",
      "duplicate desired import identities lack refresh review evidence",
    );
  }
  const safe = derivation.moves.map((move) => ({
    from_key: move.oldKey,
    to_key: move.newKey,
  }));
  const suppressed = derivation.suppressed.map((item) => ({
    from_key: item.oldKey,
    to_key: item.newKey,
    reason: item.reason,
  }));
  const desiredMoves: ZccRefreshDesiredArtifact = safe.length === 0
    ? { path: movesPath, state: "absent" }
    : {
        state: "present",
        artifact: textArtifact(
          movesPath,
          renderMovedBlocks(candidate.resource_type, derivation.moves),
        ),
      };
  const baselineStates = {
    tfvars: baselineTfvars,
    imports: {
      path: baselineImportsPath,
      state: "present" as const,
      sha256: createHash("sha256").update(baselineBytes).digest("hex"),
      size_bytes: baselineBytes.length,
    },
    lookup: baselineLookup,
    moves: { path: movesPath, state: "absent" as const },
    pending_moves: { path: pendingMovesPath, state: "absent" as const },
    alternate_hcl: { path: alternateHclPath, state: "absent" as const },
    generated_bindings: {
      path: generatedBindingsPath,
      state: "absent" as const,
    },
  };
  const baseline = {
    ...baselineStates,
    fingerprint_sha256: zccRefreshBaselineFingerprint(baselineStates),
  };
  const status: ZccPullRefreshArtifactSet["status"] = candidate.status === "ready"
      && candidate.unexpected_drops.length === 0
      && suppressed.length === 0
    ? "ready"
    : "review_required";
  const resultWithoutTransition = {
    kind: "infrawright.zcc_pull_refresh_artifact_set" as const,
    schema_version: 1 as const,
    mode: "refresh" as const,
    product: "zcc" as const,
    resource_type: candidate.resource_type,
    tenant: candidate.tenant,
    source: candidate.source,
    catalog: candidate.catalog,
    root: candidate.root,
    baseline,
    status,
    unexpected_drops: [...candidate.unexpected_drops],
    moves: { safe, suppressed },
    desired: {
      tfvars: present(candidate.artifacts.tfvars),
      imports: present(candidate.artifacts.imports),
      lookup: candidate.artifacts.lookup === null
        ? { path: lookupPath, state: "absent" as const }
        : present(candidate.artifacts.lookup),
      moves: desiredMoves,
    },
  };
  const result: ZccPullRefreshArtifactSet = {
    ...resultWithoutTransition,
    transition_sha256: zccRefreshTransitionFingerprint(
      resultWithoutTransition,
    ),
  };
  return immutableCopy(result) as ZccPullRefreshArtifactSet;
}
