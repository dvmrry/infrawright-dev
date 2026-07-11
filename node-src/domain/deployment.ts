import { ProcessFailure } from "./errors.js";
import type { Deployment, RootProviderConfig } from "./types.js";
import { readOptionalUtf8 } from "../io/files.js";
import { parseControlJson } from "../json/control.js";
import { sortedStrings } from "../json/python-compatible.js";

const ROOT_LABEL = /^[a-z0-9_]+$/;
const PROVIDER_KEYS = new Set(["strategy", "groups", "bind_references"]);

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pythonTruthy(value: unknown): boolean {
  if (
    value === undefined
    || value === null
    || value === false
    || value === 0
    || value === ""
  ) {
    return false;
  }
  if (Array.isArray(value)) {
    return value.length > 0;
  }
  if (isObject(value)) {
    return Object.keys(value).length > 0;
  }
  return true;
}

function malformed(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_DEPLOYMENT",
    category: "domain",
    message,
  });
}

function validateGroups(
  value: unknown,
  provider: string,
): Readonly<Record<string, readonly string[]>> {
  if (!isObject(value)) {
    return malformed(`roots.${provider}.groups must be an object`);
  }
  const groups: Record<string, readonly string[]> = Object.create(null) as Record<
    string,
    readonly string[]
  >;
  for (const label of sortedStrings(Object.keys(value))) {
    if (!ROOT_LABEL.test(label)) {
      return malformed(
        `roots.${provider} group labels must match [a-z0-9_]+`,
      );
    }
    const members = value[label];
    if (!Array.isArray(members)) {
      return malformed(`roots.${provider}.groups.${label} must be a list`);
    }
    if (members.length === 0) {
      return malformed(`roots.${provider}.groups.${label} must not be empty`);
    }
    for (const [index, member] of members.entries()) {
      if (typeof member !== "string" || member.length === 0) {
        return malformed(
          `roots.${provider}.groups.${label}[${index}] must be a non-empty string`,
        );
      }
    }
    groups[label] = members as string[];
  }
  return groups;
}

function validateRootConfig(
  value: unknown,
  provider: string,
): RootProviderConfig {
  if (!isObject(value)) {
    return malformed(`roots.${provider} must be an object`);
  }
  const unknown = sortedStrings(
    Object.keys(value).filter((key) => !PROVIDER_KEYS.has(key)),
  );
  if (unknown.length > 0) {
    return malformed(
      `roots.${provider} has unknown key(s): ${unknown.join(", ")}`,
    );
  }
  const strategy = value.strategy;
  if (strategy !== undefined && strategy !== "explicit" && strategy !== "slug") {
    return malformed(
      `roots.${provider}.strategy must be 'explicit' or 'slug'`,
    );
  }
  if (
    value.bind_references !== undefined
    && typeof value.bind_references !== "boolean"
  ) {
    return malformed(`roots.${provider}.bind_references must be a bool`);
  }
  const output: {
    strategy?: "explicit" | "slug";
    groups?: Readonly<Record<string, readonly string[]>>;
    bind_references?: boolean;
  } = {};
  if (strategy !== undefined) {
    output.strategy = strategy;
  }
  if (value.groups !== undefined) {
    output.groups = validateGroups(value.groups, provider);
  }
  if (typeof value.bind_references === "boolean") {
    output.bind_references = value.bind_references;
  }
  return output;
}

function validateDeployment(value: unknown): Deployment {
  if (!isObject(value)) {
    return malformed("deployment must contain a JSON object");
  }
  let roots: Readonly<Record<string, RootProviderConfig>> = {};
  if (Object.hasOwn(value, "roots")) {
    if (!isObject(value.roots)) {
      return malformed("deployment roots must be an object");
    }
    const output: Record<string, RootProviderConfig> = Object.create(null) as Record<
      string,
      RootProviderConfig
    >;
    for (const provider of sortedStrings(Object.keys(value.roots))) {
      if (provider.length === 0) {
        return malformed("deployment roots keys must be non-empty strings");
      }
      output[provider] = validateRootConfig(value.roots[provider], provider);
    }
    roots = output;
  }
  const overlay = Object.hasOwn(value, "overlay") ? value.overlay : undefined;
  const moduleDir = Object.hasOwn(value, "module_dir")
    ? value.module_dir
    : undefined;
  return {
    overlay: pythonTruthy(overlay) ? overlay : ".",
    ...(pythonTruthy(moduleDir) ? { module_dir: moduleDir } : {}),
    roots,
  };
}

export async function loadDeployment(path: string): Promise<Deployment> {
  const text = await readOptionalUtf8(path, "deployment");
  if (text === null || text.trim().length === 0) {
    return { overlay: ".", roots: {} };
  }
  try {
    return validateDeployment(parseControlJson(text));
  } catch (error: unknown) {
    if (error instanceof ProcessFailure) {
      throw error;
    }
    throw new ProcessFailure({
      code: "INVALID_DEPLOYMENT",
      category: "domain",
      message: "deployment is not valid JSON",
    });
  }
}
