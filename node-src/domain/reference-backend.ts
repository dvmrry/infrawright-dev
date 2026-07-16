import {
  ReadBudget,
  readBoundedUtf8File,
  type BoundedReadLimits,
} from "../io/bounded-files.js";
import { parseControlJson } from "../json/control.js";
import { ProcessFailure } from "./errors.js";

export const REFERENCE_BACKEND_VARIABLE = "infrawright_remote_state_backend_config";
export const REFERENCE_BACKEND_ENVIRONMENT = `TF_VAR_${REFERENCE_BACKEND_VARIABLE}`;

const MAX_REFERENCE_BACKEND_CONFIG_BYTES = 64 * 1024;
const REFERENCE_BACKEND_READ_LIMITS: BoundedReadLimits = {
  maxFiles: 1,
  maxDirectories: 1,
  maxDirectoryEntries: 1,
  maxDepth: 0,
  maxTotalBytes: BigInt(MAX_REFERENCE_BACKEND_CONFIG_BYTES),
  maxFileBytes: BigInt(MAX_REFERENCE_BACKEND_CONFIG_BYTES),
};
const STRING_FIELDS = new Set([
  "container_name",
  "resource_group_name",
  "storage_account_name",
  "subscription_id",
  "tenant_id",
]);
const BOOLEAN_FIELDS = new Set([
  "lookup_blob_endpoint",
  "use_azuread_auth",
  "use_cli",
  "use_msi",
  "use_oidc",
]);

type BackendValue = boolean | string;

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Derive the non-secret azurerm address object consumed by generated
 * terraform_remote_state blocks. Credential material remains environment-owned.
 */
export async function referenceBackendEnvironment(
  backendConfig: string,
): Promise<Readonly<Record<string, string>>> {
  let text: string;
  let size: bigint;
  try {
    const source = await readBoundedUtf8File(
      backendConfig,
      new ReadBudget(REFERENCE_BACKEND_READ_LIMITS),
      { followSymlinks: true },
    );
    text = source.text;
    size = source.digest.size;
  } catch (error: unknown) {
    if (
      error instanceof ProcessFailure
      && (
        error.code === "FILE_LIMIT_EXCEEDED"
        || error.code === "INVALID_UTF8"
        || error.code === "NOT_REGULAR_FILE"
      )
    ) {
      return fail(
        "INVALID_REFERENCE_BACKEND_CONFIG",
        `cross-state backend config must be a UTF-8 regular JSON file no larger than ${String(MAX_REFERENCE_BACKEND_CONFIG_BYTES)} bytes`,
      );
    }
    return fail("REFERENCE_BACKEND_CONFIG_READ_FAILED", "unable to read cross-state backend config", "io");
  }
  if (size <= 0n) {
    return fail(
      "INVALID_REFERENCE_BACKEND_CONFIG",
      `cross-state backend config must be between 1 and ${String(MAX_REFERENCE_BACKEND_CONFIG_BYTES)} bytes`,
    );
  }
  let parsed: unknown;
  try {
    parsed = parseControlJson(text);
  } catch {
    return fail(
      "INVALID_REFERENCE_BACKEND_CONFIG",
      "cross-state azurerm BACKEND_CONFIG must be a JSON object; HCL backend files remain supported when cross-state references are disabled",
    );
  }
  if (!isObject(parsed) || Object.keys(parsed).length === 0) {
    return fail("INVALID_REFERENCE_BACKEND_CONFIG", "cross-state backend config must contain a non-empty JSON object");
  }
  const config: Record<string, BackendValue> = Object.create(null) as Record<string, BackendValue>;
  for (const key of Object.keys(parsed).sort()) {
    if (!STRING_FIELDS.has(key) && !BOOLEAN_FIELDS.has(key)) {
      return fail(
        "UNSAFE_REFERENCE_BACKEND_CONFIG",
        "cross-state backend config contains an unsupported field; only reviewed non-secret AzureRM address and behavior fields are allowed, state keys are derived per root, and credentials must come from the environment",
      );
    }
    const value = parsed[key];
    if (STRING_FIELDS.has(key) && (typeof value !== "string" || value.length === 0)) {
      return fail(
        "INVALID_REFERENCE_BACKEND_CONFIG",
        `cross-state backend config field ${JSON.stringify(key)} must be a non-empty string`,
      );
    }
    if (BOOLEAN_FIELDS.has(key) && typeof value !== "boolean") {
      return fail(
        "INVALID_REFERENCE_BACKEND_CONFIG",
        `cross-state backend config field ${JSON.stringify(key)} must be a boolean`,
      );
    }
    config[key] = value as BackendValue;
  }
  return Object.freeze({
    [REFERENCE_BACKEND_ENVIRONMENT]: JSON.stringify(config),
  });
}
