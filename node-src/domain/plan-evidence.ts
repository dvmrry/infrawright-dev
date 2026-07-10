import { rm } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import {
  PLAN_FINGERPRINT_VERSION,
  planFingerprintV2,
  type PlanFingerprintInput,
  type PlanFingerprintV2,
} from "./plan-fingerprint.js";
import {
  ReadBudget,
  readBoundedUtf8File,
  sha256StableFile,
  snapshotStableFile,
  type StableFileDigest,
  type StableFileSnapshot,
} from "../io/bounded-files.js";
import { parseControlJson } from "../json/control.js";

const SHA256_HEX = /^[0-9a-f]{64}$/;

export interface BoundFileDigest extends StableFileDigest {
  readonly path: string;
}

export interface SavedPlanFingerprintFile extends StableFileDigest {
  readonly fingerprint: PlanFingerprintV2;
}

/**
 * Internal evidence captured before Terraform is allowed to inspect a saved
 * plan. This is intentionally not a process or report contract.
 */
export interface SavedPlanEvidence {
  readonly fingerprintInput: PlanFingerprintInput;
  readonly fingerprintPath: string;
  readonly fingerprintFile: SavedPlanFingerprintFile;
  readonly originalPlan: BoundFileDigest;
  readonly snapshotDirectory: string;
  readonly snapshot: StableFileSnapshot;
}

export interface PrepareSavedPlanEvidenceOptions {
  readonly savedPlanPath: string;
  readonly fingerprintPath: string;
  readonly fingerprintInput: PlanFingerprintInput;
  readonly snapshotDirectory: string;
  readonly fingerprintBudget: ReadBudget;
  readonly savedPlanBudget: ReadBudget;
}

export interface RecheckSavedPlanEvidenceOptions {
  readonly evidence: SavedPlanEvidence;
  readonly fingerprintBudget: ReadBudget;
  readonly savedPlanBudget: ReadBudget;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function sameDigest(left: StableFileDigest, right: StableFileDigest): boolean {
  return left.sha256 === right.sha256 && left.size === right.size;
}

function sameFingerprint(
  left: PlanFingerprintV2,
  right: PlanFingerprintV2,
): boolean {
  return left.version === right.version && left.sha256 === right.sha256;
}

function requireAbsolutePath(value: string): void {
  if (!path.isAbsolute(value)) {
    fail(
      "UNRESOLVED_EVIDENCE_PATH",
      "saved-plan evidence requires resolved absolute paths",
    );
  }
}

function copyResolvedFingerprintInput(
  input: PlanFingerprintInput,
): PlanFingerprintInput {
  requireAbsolutePath(input.envDir);
  for (const varFile of input.varFiles) {
    requireAbsolutePath(varFile);
  }
  if (input.backendConfig !== undefined && input.backendConfig !== null) {
    requireAbsolutePath(input.backendConfig);
  }
  return {
    envDir: input.envDir,
    varFiles: [...input.varFiles],
    memberTypes: [...input.memberTypes],
    ...(input.backendConfig === undefined
      ? {}
      : { backendConfig: input.backendConfig }),
    ...(input.backendKey === undefined ? {} : { backendKey: input.backendKey }),
  };
}

function validateFingerprint(value: unknown): PlanFingerprintV2 {
  if (
    typeof value !== "object"
    || value === null
    || Array.isArray(value)
  ) {
    return fail(
      "INVALID_PLAN_SOURCES",
      "saved-plan fingerprint does not match the version 2 contract",
    );
  }
  const record = value as Readonly<Record<string, unknown>>;
  const keys = Object.keys(record).sort();
  if (
    keys.length !== 2
    || keys[0] !== "sha256"
    || keys[1] !== "version"
    || record.version !== PLAN_FINGERPRINT_VERSION
    || typeof record.sha256 !== "string"
    || !SHA256_HEX.test(record.sha256)
  ) {
    return fail(
      "INVALID_PLAN_SOURCES",
      "saved-plan fingerprint does not match the version 2 contract",
    );
  }
  return { version: PLAN_FINGERPRINT_VERSION, sha256: record.sha256 };
}

export async function readSavedPlanFingerprint(
  fingerprintPath: string,
  budget: ReadBudget,
): Promise<SavedPlanFingerprintFile> {
  requireAbsolutePath(fingerprintPath);
  const file = await readBoundedUtf8File(fingerprintPath, budget);
  let parsed: unknown;
  try {
    parsed = parseControlJson(file.text);
  } catch {
    return fail(
      "INVALID_PLAN_SOURCES_JSON",
      "saved-plan fingerprint is not valid contract JSON",
    );
  }
  return { ...file.digest, fingerprint: validateFingerprint(parsed) };
}

async function currentFingerprint(
  input: PlanFingerprintInput,
  budget: ReadBudget,
): Promise<PlanFingerprintV2> {
  try {
    return await planFingerprintV2(input, budget);
  } catch {
    return fail(
      "SOURCE_FINGERPRINT_FAILED",
      "unable to fingerprint current plan inputs",
    );
  }
}

function requireCurrentSources(
  declared: PlanFingerprintV2,
  current: PlanFingerprintV2,
): void {
  if (!sameFingerprint(declared, current)) {
    fail(
      "STALE_PLAN_SOURCES",
      "saved plan does not match the current plan inputs",
    );
  }
}

function requireBoundFile(
  actual: StableFileDigest,
  expected: StableFileDigest,
  code: string,
  message: string,
): void {
  if (!sameDigest(actual, expected)) {
    fail(code, message);
  }
}

/**
 * Bind the saved plan to its strict v2 source fingerprint and to a private,
 * immutable-by-convention snapshot. Inputs are checked on both sides of the
 * copy so a caller never receives evidence assembled across a visible change.
 */
export async function prepareSavedPlanEvidence(
  options: PrepareSavedPlanEvidenceOptions,
): Promise<SavedPlanEvidence> {
  requireAbsolutePath(options.savedPlanPath);
  requireAbsolutePath(options.fingerprintPath);
  requireAbsolutePath(options.snapshotDirectory);
  const fingerprintInput = copyResolvedFingerprintInput(options.fingerprintInput);

  const declaredBefore = await readSavedPlanFingerprint(
    options.fingerprintPath,
    options.fingerprintBudget,
  );
  requireCurrentSources(
    declaredBefore.fingerprint,
    await currentFingerprint(fingerprintInput, options.fingerprintBudget),
  );

  let snapshot: StableFileSnapshot | null = null;
  try {
    snapshot = await snapshotStableFile({
      sourcePath: options.savedPlanPath,
      privateDirectory: options.snapshotDirectory,
      budget: options.savedPlanBudget,
    });
    const snapshotCheck = await sha256StableFile(
      snapshot.path,
      options.savedPlanBudget,
    );
    requireBoundFile(
      snapshotCheck,
      snapshot,
      "PLAN_SNAPSHOT_CHANGED",
      "saved-plan snapshot changed while evidence was prepared",
    );

    const declaredAfter = await readSavedPlanFingerprint(
      options.fingerprintPath,
      options.fingerprintBudget,
    );
    if (
      !sameDigest(declaredBefore, declaredAfter)
      || !sameFingerprint(declaredBefore.fingerprint, declaredAfter.fingerprint)
    ) {
      fail(
        "PLAN_SOURCES_CHANGED",
        "saved-plan fingerprint changed while evidence was prepared",
      );
    }
    requireCurrentSources(
      declaredBefore.fingerprint,
      await currentFingerprint(fingerprintInput, options.fingerprintBudget),
    );

    const originalCheck = await sha256StableFile(
      options.savedPlanPath,
      options.savedPlanBudget,
    );
    requireBoundFile(
      originalCheck,
      snapshot,
      "SAVED_PLAN_CHANGED",
      "saved plan changed while evidence was prepared",
    );

    return {
      fingerprintInput,
      fingerprintPath: options.fingerprintPath,
      fingerprintFile: declaredBefore,
      originalPlan: {
        path: options.savedPlanPath,
        sha256: snapshot.sha256,
        size: snapshot.size,
      },
      snapshotDirectory: options.snapshotDirectory,
      snapshot,
    };
  } catch (error: unknown) {
    if (snapshot !== null) {
      await rm(snapshot.path, { force: true }).catch(() => undefined);
    }
    throw error;
  }
}

/** Revalidate every content binding captured by prepareSavedPlanEvidence. */
export async function recheckSavedPlanEvidence(
  options: RecheckSavedPlanEvidenceOptions,
): Promise<void> {
  const { evidence } = options;
  const originalBefore = await sha256StableFile(
    evidence.originalPlan.path,
    options.savedPlanBudget,
  );
  requireBoundFile(
    originalBefore,
    evidence.originalPlan,
    "SAVED_PLAN_CHANGED",
    "saved plan changed after evidence was prepared",
  );

  const declaredBefore = await readSavedPlanFingerprint(
    evidence.fingerprintPath,
    options.fingerprintBudget,
  );
  if (
    !sameDigest(declaredBefore, evidence.fingerprintFile)
    || !sameFingerprint(
      declaredBefore.fingerprint,
      evidence.fingerprintFile.fingerprint,
    )
  ) {
    fail(
      "PLAN_SOURCES_CHANGED",
      "saved-plan fingerprint changed after evidence was prepared",
    );
  }
  requireCurrentSources(
    evidence.fingerprintFile.fingerprint,
    await currentFingerprint(evidence.fingerprintInput, options.fingerprintBudget),
  );

  const snapshotCheck = await sha256StableFile(
    evidence.snapshot.path,
    options.savedPlanBudget,
  );
  requireBoundFile(
    snapshotCheck,
    evidence.snapshot,
    "PLAN_SNAPSHOT_CHANGED",
    "saved-plan snapshot changed after evidence was prepared",
  );

  const declaredAfter = await readSavedPlanFingerprint(
    evidence.fingerprintPath,
    options.fingerprintBudget,
  );
  if (
    !sameDigest(declaredAfter, evidence.fingerprintFile)
    || !sameFingerprint(
      declaredAfter.fingerprint,
      evidence.fingerprintFile.fingerprint,
    )
  ) {
    fail(
      "PLAN_SOURCES_CHANGED",
      "saved-plan fingerprint changed after evidence was prepared",
    );
  }
  requireCurrentSources(
    evidence.fingerprintFile.fingerprint,
    await currentFingerprint(evidence.fingerprintInput, options.fingerprintBudget),
  );

  const originalAfter = await sha256StableFile(
    evidence.originalPlan.path,
    options.savedPlanBudget,
  );
  requireBoundFile(
    originalAfter,
    evidence.originalPlan,
    "SAVED_PLAN_CHANGED",
    "saved plan changed after evidence was prepared",
  );
}

export async function cleanupSavedPlanEvidence(
  evidence: SavedPlanEvidence,
): Promise<void> {
  const expectedName = /^plan-[0-9a-f]{32}$/;
  if (
    !path.isAbsolute(evidence.snapshotDirectory)
    || path.dirname(evidence.snapshot.path) !== evidence.snapshotDirectory
    || !expectedName.test(path.basename(evidence.snapshot.path))
  ) {
    fail(
      "INVALID_SNAPSHOT_BINDING",
      "saved-plan snapshot is not bound to its private directory",
    );
  }
  try {
    await rm(evidence.snapshot.path, { force: true });
  } catch {
    fail(
      "SNAPSHOT_CLEANUP_FAILED",
      "unable to remove saved-plan snapshot",
    );
  }
}
