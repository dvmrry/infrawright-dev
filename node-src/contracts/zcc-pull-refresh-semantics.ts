import { createHash } from "node:crypto";
import path from "node:path";

import type { ErrorObject } from "ajv/dist/2020.js";

import {
  parseGeneratedImports,
  renderMovedBlocks,
} from "../domain/import-moves.js";
import {
  zccRefreshBaselineFingerprint,
  zccRefreshTransitionFingerprint,
} from "../domain/zcc-pull-refresh-fingerprints.js";
import {
  matchesPythonCollapsedRefreshLookup,
} from "../domain/zcc-pull-refresh-lookup.js";
import { validateZccPullArtifactSemantics } from "./zcc-pull-artifact-semantics.js";

export const ZCC_PULL_REFRESH_SEMANTICS_KEYWORD =
  "x-infrawright-zcc-pull-refresh-semantics";

const MAX_MOVE_CANDIDATES = 50_000;
const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as JsonRecord
    : null;
}

function ownValue(value: JsonRecord, key: string): unknown {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function semanticError(
  instancePath: string,
  rule: string,
  message: string,
): ErrorObject {
  return {
    instancePath,
    schemaPath: `#/${ZCC_PULL_REFRESH_SEMANTICS_KEYWORD}`,
    keyword: ZCC_PULL_REFRESH_SEMANTICS_KEYWORD,
    params: { rule },
    message,
  };
}

function compareCodePoints(left: string, right: string): number {
  let leftIndex = 0;
  let rightIndex = 0;
  while (leftIndex < left.length && rightIndex < right.length) {
    const leftPoint = left.codePointAt(leftIndex) ?? 0;
    const rightPoint = right.codePointAt(rightIndex) ?? 0;
    if (leftPoint !== rightPoint) {
      return leftPoint - rightPoint;
    }
    leftIndex += leftPoint > 0xffff ? 2 : 1;
    rightIndex += rightPoint > 0xffff ? 2 : 1;
  }
  return (leftIndex < left.length ? 1 : 0)
    - (rightIndex < right.length ? 1 : 0);
}

function moveKey(value: unknown): string | null {
  const move = record(value);
  const from = move === null ? null : ownValue(move, "from_key");
  const to = move === null ? null : ownValue(move, "to_key");
  return typeof from === "string" && typeof to === "string"
    ? `${from}\0${to}`
    : null;
}

function compareMoves(left: unknown, right: unknown): number {
  const leftMove = record(left) ?? {};
  const rightMove = record(right) ?? {};
  const leftFrom = stringValue(ownValue(leftMove, "from_key"));
  const rightFrom = stringValue(ownValue(rightMove, "from_key"));
  const leftTo = stringValue(ownValue(leftMove, "to_key"));
  const rightTo = stringValue(ownValue(rightMove, "to_key"));
  const leftReason = stringValue(ownValue(leftMove, "reason"));
  const rightReason = stringValue(ownValue(rightMove, "reason"));
  return compareCodePoints(leftFrom, rightFrom)
    || compareCodePoints(leftTo, rightTo)
    || compareCodePoints(leftReason, rightReason);
}

function sameOrder(values: readonly unknown[]): boolean {
  const sorted = [...values].sort(compareMoves);
  return values.every((value, index) => value === sorted[index]);
}

function allStringsWellFormed(value: unknown): boolean {
  if (typeof value === "string") {
    return value.isWellFormed();
  }
  if (Array.isArray(value)) {
    return value.every((item) => allStringsWellFormed(item));
  }
  const objectValue = record(value);
  return objectValue === null || Object.keys(objectValue).every((key) => {
    return key.isWellFormed() && allStringsWellFormed(ownValue(objectValue, key));
  });
}

function desiredArtifact(value: unknown): JsonRecord | null {
  const state = record(value);
  return state !== null && ownValue(state, "state") === "present"
    ? record(ownValue(state, "artifact"))
    : null;
}

function statePath(value: unknown): string | null {
  const state = record(value);
  if (state === null) {
    return null;
  }
  const artifact = desiredArtifact(state);
  const pathValue = artifact === null
    ? ownValue(state, "path")
    : ownValue(artifact, "path");
  return typeof pathValue === "string" ? pathValue : null;
}

function expectedSibling(importsPath: string, suffix: string): string | null {
  return importsPath.endsWith("_imports.tf")
    ? importsPath.slice(0, -"_imports.tf".length) + suffix
    : null;
}

function remapCandidatePath(instancePath: string): string {
  return instancePath
    .replace("/artifacts/tfvars", "/desired/tfvars/artifact")
    .replace("/artifacts/imports", "/desired/imports/artifact")
    .replace("/artifacts/lookup", "/desired/lookup/artifact");
}

function allowsDuplicateDesiredImportIds(
  resourceType: string,
  content: unknown,
): boolean {
  if (typeof content !== "string") {
    return false;
  }
  try {
    const ids = parseGeneratedImports(resourceType, content).map(
      (entry) => entry.importId,
    );
    return ids.length > 1
      && ids.every((id) => !PYTHON_WHITESPACE_ONLY.test(id))
      && new Set(ids).size < ids.length;
  } catch {
    return false;
  }
}

export interface ZccPullRefreshSemanticValidator {
  (
    schema: unknown,
    data: unknown,
    parentSchema?: unknown,
    dataContext?: { readonly instancePath: string },
  ): boolean;
  errors?: Partial<ErrorObject>[];
}

/** Enforce refresh path joins, move bytes, CAS hashes, and derived status. */
export const validateZccPullRefreshSemantics:
  ZccPullRefreshSemanticValidator = (
    _schema,
    data,
    _parentSchema,
    dataContext,
  ) => {
    const refresh = record(data);
    const baseline = record(refresh?.baseline);
    const moves = record(refresh?.moves);
    const desired = record(refresh?.desired);
    if (refresh === null || baseline === null || moves === null || desired === null) {
      delete validateZccPullRefreshSemantics.errors;
      return true;
    }

    const errors: ErrorObject[] = [];
    const push = (instancePath: string, rule: string, message: string): void => {
      errors.push(semanticError(
        `${dataContext?.instancePath ?? ""}${instancePath}`,
        rule,
        message,
      ));
    };

    if (!allStringsWellFormed(refresh)) {
      push("/", "well_formed_unicode", "refresh strings must contain well-formed Unicode");
    }

    const safe = ownValue(moves, "safe");
    const suppressed = ownValue(moves, "suppressed");
    const safeMoves = Array.isArray(safe) ? safe : [];
    const suppressedMoves = Array.isArray(suppressed) ? suppressed : [];
    if (safeMoves.length + suppressedMoves.length > MAX_MOVE_CANDIDATES) {
      push(
        "/moves",
        "move_bounds",
        "safe and suppressed move candidates exceed the aggregate limit",
      );
    }
    if (!sameOrder(safeMoves) || !sameOrder(suppressedMoves)) {
      push("/moves", "move_order", "move evidence must use canonical order");
    }
    const seenMoves = new Set<string>();
    for (const [collection, instancePath] of [
      [safeMoves, "/moves/safe"],
      [suppressedMoves, "/moves/suppressed"],
    ] as const) {
      for (const value of collection) {
        const key = moveKey(value);
        const entry = record(value);
        if (
          key === null
          || stringValue(ownValue(entry ?? {}, "from_key")).includes("\0")
          || stringValue(ownValue(entry ?? {}, "to_key")).includes("\0")
          || ownValue(entry ?? {}, "from_key") === ownValue(entry ?? {}, "to_key")
          || seenMoves.has(key)
        ) {
          push(instancePath, "move_identity", "move evidence must be unique and change keys");
          break;
        }
        seenMoves.add(key);
      }
    }

    const desiredTfvars = desiredArtifact(ownValue(desired, "tfvars"));
    const desiredImports = desiredArtifact(ownValue(desired, "imports"));
    const desiredLookupState = ownValue(desired, "lookup");
    const desiredLookup = desiredArtifact(desiredLookupState);
    const desiredMovesState = ownValue(desired, "moves");
    const desiredMoves = desiredArtifact(desiredMovesState);
    if (desiredTfvars !== null && desiredImports !== null) {
      const candidate = {
        kind: "infrawright.zcc_pull_artifact_set",
        schema_version: 1,
        mode: "bootstrap",
        product: ownValue(refresh, "product"),
        resource_type: ownValue(refresh, "resource_type"),
        tenant: ownValue(refresh, "tenant"),
        source: ownValue(refresh, "source"),
        catalog: ownValue(refresh, "catalog"),
        root: ownValue(refresh, "root"),
        status: Array.isArray(ownValue(refresh, "unexpected_drops"))
            && (ownValue(refresh, "unexpected_drops") as unknown[]).length === 0
          ? "ready"
          : "review_required",
        unexpected_drops: ownValue(refresh, "unexpected_drops"),
        artifacts: {
          tfvars: desiredTfvars,
          imports: desiredImports,
          lookup: desiredLookup,
        },
      };
      validateZccPullArtifactSemantics(true, candidate);
      const duplicateDesiredImportIds = allowsDuplicateDesiredImportIds(
        stringValue(ownValue(refresh, "resource_type")),
        ownValue(desiredImports, "content"),
      );
      const collapsedLookupValid = matchesPythonCollapsedRefreshLookup({
        resourceType: stringValue(ownValue(refresh, "resource_type")),
        importsContent: ownValue(desiredImports, "content"),
        lookupContent: desiredLookup === null
          ? null
          : ownValue(desiredLookup, "content"),
        tfvarsContent: ownValue(desiredTfvars, "content"),
        variableName: ownValue(record(ownValue(refresh, "root")) ?? {}, "variable_name"),
      });
      for (const candidateError of validateZccPullArtifactSemantics.errors ?? []) {
        const params = candidateError.params as { readonly rule?: unknown };
        if (
          params.rule === "import_ids"
          && duplicateDesiredImportIds
        ) {
          continue;
        }
        if (
          params.rule === "lookup_join"
          && duplicateDesiredImportIds
          && collapsedLookupValid
        ) {
          continue;
        }
        errors.push({
          ...(candidateError as ErrorObject),
          instancePath: `${dataContext?.instancePath ?? ""}${remapCandidatePath(
            String(candidateError.instancePath ?? ""),
          )}`,
          schemaPath: `#/${ZCC_PULL_REFRESH_SEMANTICS_KEYWORD}`,
          keyword: ZCC_PULL_REFRESH_SEMANTICS_KEYWORD,
        });
      }
    }

    const baselineTfvarsPath = statePath(ownValue(baseline, "tfvars"));
    const baselineImportsPath = statePath(ownValue(baseline, "imports"));
    const baselineLookupPath = statePath(ownValue(baseline, "lookup"));
    const baselineMovesPath = statePath(ownValue(baseline, "moves"));
    const baselinePendingPath = statePath(ownValue(baseline, "pending_moves"));
    const baselineAlternateHclPath = statePath(
      ownValue(baseline, "alternate_hcl"),
    );
    const baselineGeneratedBindingsPath = statePath(
      ownValue(baseline, "generated_bindings"),
    );
    const desiredTfvarsPath = statePath(ownValue(desired, "tfvars"));
    const desiredImportsPath = statePath(ownValue(desired, "imports"));
    const desiredLookupPath = statePath(desiredLookupState);
    const desiredMovesPath = statePath(desiredMovesState);
    if (
      baselineTfvarsPath !== desiredTfvarsPath
      || baselineImportsPath !== desiredImportsPath
      || baselineLookupPath !== desiredLookupPath
      || baselineMovesPath !== desiredMovesPath
    ) {
      push("/baseline", "baseline_join", "baseline and desired target paths must match");
    }
    if (
      [
        baselineTfvarsPath,
        baselineImportsPath,
        baselineLookupPath,
        baselineMovesPath,
        baselinePendingPath,
        baselineAlternateHclPath,
        baselineGeneratedBindingsPath,
        desiredTfvarsPath,
        desiredImportsPath,
        desiredLookupPath,
        desiredMovesPath,
      ].some((value) => value !== null && value.includes("\0"))
    ) {
      push("/baseline", "path_characters", "refresh target paths must not contain NUL");
    }
    const resourceType = stringValue(ownValue(refresh, "resource_type"));
    const canonicalLookupPath = baselineTfvarsPath === null
      ? null
      : `${path.posix.dirname(baselineTfvarsPath)}/${resourceType}.lookup.json`;
    if (
      baselineImportsPath === null
      || expectedSibling(baselineImportsPath, "_moves.tf") !== baselineMovesPath
      || expectedSibling(
        baselineImportsPath,
        "_moves.pending.json",
      ) !== baselinePendingPath
      || baselineTfvarsPath === null
      || baselineLookupPath !== canonicalLookupPath
      || desiredLookupPath !== canonicalLookupPath
      || !baselineTfvarsPath.endsWith(".json")
      || baselineTfvarsPath.slice(0, -".json".length)
        !== baselineAlternateHclPath
      || `${baselineTfvarsPath.slice(0, baselineTfvarsPath.lastIndexOf("/") + 1)}`
        + `${resourceType}.generated.expressions.json`
        !== baselineGeneratedBindingsPath
    ) {
      push("/baseline", "adjacent_paths", "refresh adjacent paths must be canonical siblings");
    }

    let desiredImportKeys: ReadonlySet<string> | null = null;
    let desiredHasDuplicateImportIds = false;
    if (desiredImports !== null) {
      const importsContent = ownValue(desiredImports, "content");
      if (typeof importsContent === "string") {
        try {
          const entries = parseGeneratedImports(resourceType, importsContent);
          desiredImportKeys = new Set(entries.map((entry) => entry.key));
          const ids = entries.map((entry) => entry.importId);
          desiredHasDuplicateImportIds = new Set(ids).size < ids.length;
        } catch {
          desiredImportKeys = null;
        }
      }
    }
    if (
      desiredHasDuplicateImportIds
      && !suppressedMoves.some((value) => {
        const entry = record(value);
        return entry !== null && ownValue(entry, "reason") === "duplicate_from";
      })
    ) {
      push(
        "/moves/suppressed",
        "duplicate_import_evidence",
        "duplicate desired import identities require duplicate_from review evidence",
      );
    }
    if (desiredImportKeys !== null) {
      const safeFrom = new Set<string>();
      const safeTo = new Set<string>();
      const suppressedFrom = new Set<string>();
      const suppressedTo = new Set<string>();
      let moveJoinValid = true;
      for (const value of safeMoves) {
        const move = record(value) ?? {};
        const from = stringValue(ownValue(move, "from_key"));
        const to = stringValue(ownValue(move, "to_key"));
        if (
          safeFrom.has(from)
          || safeTo.has(to)
          || desiredImportKeys.has(from)
          || !desiredImportKeys.has(to)
        ) {
          moveJoinValid = false;
        }
        safeFrom.add(from);
        safeTo.add(to);
      }
      if ([...safeFrom].some((from) => safeTo.has(from))) {
        moveJoinValid = false;
      }
      for (const value of suppressedMoves) {
        const move = record(value) ?? {};
        const from = stringValue(ownValue(move, "from_key"));
        const to = stringValue(ownValue(move, "to_key"));
        suppressedFrom.add(from);
        suppressedTo.add(to);
        if (!desiredImportKeys.has(to)) {
          moveJoinValid = false;
        }
      }
      if (
        [...safeFrom].some((value) => suppressedFrom.has(value))
        || [...safeTo].some((value) => suppressedTo.has(value))
        || [...safeTo].some((value) => suppressedFrom.has(value))
        || [...safeFrom].some((value) => suppressedTo.has(value))
      ) {
        moveJoinValid = false;
      }
      if (!moveJoinValid) {
        push(
          "/moves",
          "move_join",
          "move targets must join desired imports and safe moves must be one-to-one and disjoint",
        );
      }
    }
    let baselineBytes = 0;
    let baselineBoundsValid = true;
    for (const name of ["tfvars", "imports", "lookup"] as const) {
      const state = record(ownValue(baseline, name));
      if (state === null || ownValue(state, "state") !== "present") {
        continue;
      }
      const size = ownValue(state, "size_bytes");
      if (
        typeof size !== "number"
        || !Number.isSafeInteger(size)
        || size < 0
        || size > 32 * 1024 * 1024
      ) {
        baselineBoundsValid = false;
        break;
      }
      baselineBytes += size;
    }
    if (!baselineBoundsValid || baselineBytes > 96 * 1024 * 1024) {
      push(
        "/baseline",
        "baseline_bounds",
        "baseline evidence exceeds the per-file or aggregate byte limit",
      );
    }

    if (safeMoves.length === 0) {
      if (desiredMoves !== null) {
        push("/desired/moves", "moves_desired", "no safe moves requires desired absence");
      }
    } else if (desiredMoves === null) {
      push("/desired/moves", "moves_desired", "safe moves require a desired HCL artifact");
    } else {
      try {
        const expected = renderMovedBlocks(
          stringValue(ownValue(refresh, "resource_type")),
          safeMoves.map((value) => {
            const move = record(value) ?? {};
            return {
              oldKey: stringValue(ownValue(move, "from_key")),
              newKey: stringValue(ownValue(move, "to_key")),
            };
          }),
        );
        const content = ownValue(desiredMoves, "content");
        const pathValue = ownValue(desiredMoves, "path");
        const bytes = typeof content === "string" && content.isWellFormed()
          ? Buffer.from(content, "utf8")
          : null;
        if (
          content !== expected
          || pathValue !== baselineMovesPath
          || bytes === null
          || ownValue(desiredMoves, "size_bytes") !== bytes.length
          || ownValue(desiredMoves, "sha256")
            !== createHash("sha256").update(bytes).digest("hex")
        ) {
          push(
            "/desired/moves/artifact",
            "moves_bytes",
            "desired move bytes must exactly render the safe move list",
          );
        }
      } catch {
        push(
          "/desired/moves/artifact",
          "moves_bytes",
          "desired move bytes must exactly render the safe move list",
        );
      }
    }

    const unexpectedDrops = ownValue(refresh, "unexpected_drops");
    const expectedStatus = Array.isArray(unexpectedDrops)
        && unexpectedDrops.length === 0
        && suppressedMoves.length === 0
      ? "ready"
      : "review_required";
    if (ownValue(refresh, "status") !== expectedStatus) {
      push("/status", "derived_status", "refresh status must derive from drops and suppressions");
    }

    try {
      const states = {
        tfvars: ownValue(baseline, "tfvars"),
        imports: ownValue(baseline, "imports"),
        lookup: ownValue(baseline, "lookup"),
        moves: ownValue(baseline, "moves"),
        pending_moves: ownValue(baseline, "pending_moves"),
        alternate_hcl: ownValue(baseline, "alternate_hcl"),
        generated_bindings: ownValue(baseline, "generated_bindings"),
      };
      if (
        ownValue(baseline, "fingerprint_sha256")
        !== zccRefreshBaselineFingerprint(states)
      ) {
        push("/baseline/fingerprint_sha256", "baseline_fingerprint", "baseline fingerprint is invalid");
      }
      if (
        ownValue(refresh, "transition_sha256")
        !== zccRefreshTransitionFingerprint({
          product: ownValue(refresh, "product"),
          resource_type: ownValue(refresh, "resource_type"),
          tenant: ownValue(refresh, "tenant"),
          source: ownValue(refresh, "source"),
          catalog: ownValue(refresh, "catalog"),
          root: ownValue(refresh, "root"),
          baseline,
          unexpected_drops: unexpectedDrops,
          moves,
          desired,
        })
      ) {
        push("/transition_sha256", "transition_fingerprint", "transition fingerprint is invalid");
      }
    } catch {
      push("/transition_sha256", "transition_fingerprint", "refresh fingerprints cannot be recomputed");
    }

    if (errors.length === 0) {
      delete validateZccPullRefreshSemantics.errors;
    } else {
      validateZccPullRefreshSemantics.errors = errors;
    }
    return errors.length === 0;
  };
