import { readFile, stat } from "node:fs/promises";

import { parseControlJson } from "../json/control.js";
import { ProcessFailure } from "./errors.js";

export const REFERENCE_BACKEND_VARIABLE = "infrawright_remote_state_backend_config";
export const REFERENCE_BACKEND_ENVIRONMENT = `TF_VAR_${REFERENCE_BACKEND_VARIABLE}`;

const MAX_REFERENCE_BACKEND_CONFIG_BYTES = 64 * 1024;
const FORBIDDEN_KEYS = new Set([
  "access_key",
  "key",
  "sas_token",
]);

type BackendValue = boolean | number | string;

function fail(code: string, message: string, category: "domain" | "io" = "domain"): never {
  throw new ProcessFailure({ code, category, message });
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function credentialBearingKey(key: string): boolean {
  const normalized = key.toLowerCase();
  return FORBIDDEN_KEYS.has(normalized)
    || normalized.includes("certificate")
    || normalized.includes("password")
    || normalized.includes("secret")
    || normalized.endsWith("_token");
}

/**
 * Derive the non-secret azurerm address object consumed by generated
 * terraform_remote_state blocks. Authentication remains environment-owned.
 */
export async function referenceBackendEnvironment(
  backendConfig: string,
): Promise<Readonly<Record<string, string>>> {
  let size: number;
  try {
    const metadata = await stat(backendConfig);
    if (!metadata.isFile()) {
      return fail("INVALID_REFERENCE_BACKEND_CONFIG", "cross-state backend config must be a regular JSON file");
    }
    size = metadata.size;
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) throw error;
    return fail("REFERENCE_BACKEND_CONFIG_READ_FAILED", "unable to read cross-state backend config", "io");
  }
  if (size <= 0 || size > MAX_REFERENCE_BACKEND_CONFIG_BYTES) {
    return fail(
      "INVALID_REFERENCE_BACKEND_CONFIG",
      `cross-state backend config must be between 1 and ${String(MAX_REFERENCE_BACKEND_CONFIG_BYTES)} bytes`,
    );
  }
  let parsed: unknown;
  try {
    const text = await readFile(backendConfig, "utf8");
    if (Buffer.byteLength(text, "utf8") > MAX_REFERENCE_BACKEND_CONFIG_BYTES) {
      return fail("INVALID_REFERENCE_BACKEND_CONFIG", "cross-state backend config changed while it was read");
    }
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
    if (credentialBearingKey(key)) {
      return fail(
        "UNSAFE_REFERENCE_BACKEND_CONFIG",
        `cross-state backend config must not contain ${JSON.stringify(key)}; state keys are derived per root and credentials must come from the environment`,
      );
    }
    const value = parsed[key];
    if (
      (typeof value !== "string" || value.length === 0)
      && typeof value !== "boolean"
      && (typeof value !== "number" || !Number.isFinite(value))
    ) {
      return fail(
        "INVALID_REFERENCE_BACKEND_CONFIG",
        `cross-state backend config field ${JSON.stringify(key)} must be a non-empty string, finite number, or boolean`,
      );
    }
    config[key] = value as BackendValue;
  }
  return Object.freeze({
    [REFERENCE_BACKEND_ENVIRONMENT]: JSON.stringify(config),
  });
}
