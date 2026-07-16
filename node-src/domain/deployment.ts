import { ProcessFailure } from "./errors.js";
import {
  bindOptionalAssessmentControlText,
  type BoundAssessmentControlFile,
} from "./control-evidence.js";
import type { Deployment, RootProviderConfig } from "./types.js";
import { readOptionalUtf8 } from "../io/files.js";
import { parseControlJson } from "../json/control.js";
import { sortedStrings } from "../json/python-compatible.js";
import path from "node:path";

const ROOT_LABEL = /^[a-z0-9_]+$/;
const PROVIDER_KEYS = new Set([
  "strategy",
  "groups",
  "bind_references",
  "cross_state_references",
]);

export type ReferenceBindingMode = "disabled" | "same_root" | "cross_state";

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
  if (
    value.cross_state_references !== undefined
    && typeof value.cross_state_references !== "boolean"
  ) {
    return malformed(`roots.${provider}.cross_state_references must be a bool`);
  }
  if (value.bind_references === true && value.cross_state_references === true) {
    return malformed(
      `roots.${provider}.bind_references and cross_state_references are mutually exclusive`,
    );
  }
  const output: {
    strategy?: "explicit" | "slug";
    groups?: Readonly<Record<string, readonly string[]>>;
    bind_references?: boolean;
    cross_state_references?: boolean;
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
  if (typeof value.cross_state_references === "boolean") {
    output.cross_state_references = value.cross_state_references;
  }
  return output;
}

export function deploymentReferenceBindingMode(
  deployment: Deployment,
  provider: string,
): ReferenceBindingMode {
  const config = deployment.roots[provider];
  if (config?.cross_state_references === true) return "cross_state";
  if (config?.bind_references === true) return "same_root";
  return "disabled";
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
  const tfvarsFormat = Object.hasOwn(value, "tfvars_format")
    ? value.tfvars_format
    : undefined;
  return {
    overlay: pythonTruthy(overlay) ? overlay : ".",
    ...(pythonTruthy(moduleDir) ? { module_dir: moduleDir } : {}),
    ...(tfvarsFormat === undefined ? {} : { tfvars_format: tfvarsFormat }),
    roots,
  };
}

function deploymentFromText(text: string | null): Deployment {
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

export async function loadDeployment(path: string): Promise<Deployment> {
  return deploymentFromText(await readOptionalUtf8(path, "deployment"));
}

export function deploymentPath(options?: {
  readonly explicit?: string;
  readonly environment?: NodeJS.ProcessEnv;
  readonly cwd?: string;
}): string {
  const environment = options?.environment ?? process.env;
  const selected = options?.explicit || environment.INFRAWRIGHT_DEPLOYMENT;
  return selected || path.join(options?.cwd ?? process.cwd(), "deployment.json");
}

export function deploymentOverlay(deployment: Deployment): string {
  if (typeof deployment.overlay !== "string") {
    return malformed("deployment overlay must be a string");
  }
  return deployment.overlay || ".";
}

export function deploymentTfvarsFormat(deployment: Deployment): "json" | "hcl" {
  const value = deployment.tfvars_format ?? "json";
  if (value !== "json" && value !== "hcl") {
    return malformed("deployment tfvars_format must be 'json' or 'hcl'");
  }
  return value;
}

export function deploymentModuleDir(deployment: Deployment): string {
  if (deployment.module_dir !== undefined) {
    if (typeof deployment.module_dir !== "string") {
      return malformed("deployment module_dir must be a string");
    }
    if (deployment.module_dir.length > 0) return deployment.module_dir;
  }
  const overlay = deploymentOverlay(deployment);
  return overlay === "." ? "modules" : path.join(overlay, "modules", "default");
}

export function deploymentTenantRoot(
  deployment: Deployment,
  _tenant: string,
): string {
  return deploymentOverlay(deployment);
}

function deploymentTenantPath(
  deployment: Deployment,
  tenant: string,
  kind: "config" | "imports" | "envs",
): string {
  const relative = path.join(kind, tenant);
  const root = deploymentTenantRoot(deployment, tenant);
  return root === "." ? relative : path.join(root, relative);
}

export function deploymentConfigDir(
  deployment: Deployment,
  tenant: string,
): string {
  return deploymentTenantPath(deployment, tenant, "config");
}

export function deploymentImportsDir(
  deployment: Deployment,
  tenant: string,
): string {
  return deploymentTenantPath(deployment, tenant, "imports");
}

export function deploymentEnvsDir(
  deployment: Deployment,
  tenant: string,
): string {
  return deploymentTenantPath(deployment, tenant, "envs");
}

export function deploymentPullsDir(tenant: string): string {
  return path.join("pulls", tenant);
}

export async function loadBoundAssessmentDeployment(
  path: string,
  options?: { readonly followSymlinks?: boolean },
): Promise<{
  readonly deployment: Deployment;
  readonly file: BoundAssessmentControlFile;
}> {
  const source = await bindOptionalAssessmentControlText(path, options);
  return {
    deployment: deploymentFromText(source.text),
    file: source.file,
  };
}
