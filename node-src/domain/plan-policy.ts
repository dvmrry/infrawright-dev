import path from "node:path";

import { DriftPolicy } from "./drift-policy.js";
import { ProcessFailure } from "./errors.js";
import {
  ReadBudget,
  readBoundedFileBytes,
  sha256StableFile,
  type StableFileDigest,
} from "../io/bounded-files.js";
import {
  parseControlJson,
  PythonJsonDecodeError,
} from "../json/control.js";

export interface BoundDriftPolicy {
  readonly path: string | null;
  readonly file: StableFileDigest | null;
  readonly policy: DriftPolicy;
}

export class DriftPolicyLoadFailure extends ProcessFailure {
  readonly file: StableFileDigest;

  constructor(file: StableFileDigest, message: string) {
    super({
      code: "INVALID_DRIFT_POLICY",
      category: "domain",
      message,
    });
    this.name = "DriftPolicyLoadFailure";
    this.file = file;
  }
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

export async function loadBoundDriftPolicy(
  policyPath: string | null,
  budget: ReadBudget,
): Promise<BoundDriftPolicy> {
  if (policyPath === null) {
    return { path: null, file: null, policy: new DriftPolicy(null) };
  }
  if (!path.isAbsolute(policyPath)) {
    return fail(
      "UNRESOLVED_POLICY_PATH",
      "saved-plan policy requires a resolved absolute path",
    );
  }
  const source = await readBoundedFileBytes(policyPath, budget);
  try {
    const text = new TextDecoder("utf-8", {
      fatal: true,
      ignoreBOM: true,
    }).decode(source.bytes);
    return {
      path: policyPath,
      file: source.digest,
      policy: new DriftPolicy(parseControlJson(text), "<policy>"),
    };
  } catch (error: unknown) {
    throw new DriftPolicyLoadFailure(
      source.digest,
      error instanceof PythonJsonDecodeError
        ? error.message
        : "saved-plan drift policy is invalid",
    );
  }
}

export async function recheckBoundDriftPolicy(
  bound: BoundDriftPolicy,
  budget: ReadBudget,
): Promise<void> {
  if (bound.path === null || bound.file === null) {
    return;
  }
  const current = await sha256StableFile(bound.path, budget);
  if (
    current.sha256 !== bound.file.sha256
    || current.size !== bound.file.size
  ) {
    fail("DRIFT_POLICY_CHANGED", "saved-plan drift policy changed during assessment");
  }
}
