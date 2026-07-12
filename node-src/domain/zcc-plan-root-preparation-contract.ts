import { createHash } from "node:crypto";
import path from "node:path";

import {
  renderPythonCompatibleJson,
  sortedStrings,
  type JsonValue,
} from "../json/python-compatible.js";
import { PLAN_FINGERPRINT_VERSION } from "./plan-fingerprint.js";
import { pythonPosixJoin } from "./paths.js";
import { ProcessFailure } from "./errors.js";
import type { RootTopology } from "./types.js";
import type { ZccAdoptionArtifactMaterialization } from "./zcc-adoption-materialization.js";
import type { ZccPullResourceType } from "./zcc-pull-artifacts.js";

export const ZCC_PLAN_ROOT_PREPARATION_PROFILE =
  "zcc_exact_five_adoption_json_no_bindings_v1" as const;
export const ZCC_PLAN_ROOT_RENDERER_PROFILE =
  "python_gen_env_terraform_1_15_4_fmt_compatible_v1" as const;
export const MAX_ZCC_PLAN_ROOT_HCL_ARTIFACT_BYTES = 8 * 1024 * 1024;
export const MAX_ZCC_PLAN_ROOT_STAGED_IMPORT_BYTES = 16 * 1024 * 1024;
export const MAX_ZCC_PLAN_ROOT_CANDIDATE_JSON_BYTES = 24 * 1024 * 1024;
export const ZCC_PLAN_ROOT_RESOURCE_TYPES = Object.freeze([
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
] as const satisfies readonly ZccPullResourceType[]);

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

export function zccPlanRootSha256(text: string): string {
  return createHash("sha256").update(text, "utf8").digest("hex");
}

export function zccPlanRootTreeSha256(files: readonly {
  readonly path: string;
  readonly sha256: string;
}[]): string {
  return zccPlanRootSha256(renderPythonCompatibleJson({
    files: files.map((file) => [file.path, file.sha256]),
    version: PLAN_FINGERPRINT_VERSION,
  } as JsonValue));
}

export function zccPlanRootMaterializationSha256(
  receipt: ZccAdoptionArtifactMaterialization,
): string {
  return zccPlanRootSha256(
    renderPythonCompatibleJson(receipt as unknown as JsonValue),
  );
}

export function zccPlanRootModuleSource(
  envDir: string,
  moduleLogicalPath: string,
): string {
  let source = path.posix.relative(envDir, moduleLogicalPath) || ".";
  if (!source.startsWith("../") && !source.startsWith("./") && !source.startsWith("/")) {
    source = `./${source}`;
  }
  if (
    source.includes("\0")
    || source.length > 4096
    || !source.isWellFormed()
    || /["\\\u0000-\u001f\u007f]/u.test(source)
    || source.includes("${")
    || source.includes("%{")
  ) {
    return fail(
      "UNSUPPORTED_PLAN_ROOT_MODULE_SOURCE",
      "derived module source contains HCL-sensitive path characters",
    );
  }
  return source;
}

export function zccPlanRootAbsentSidecarPaths(
  topology: RootTopology,
  members: readonly ZccPullResourceType[],
  envDir: string,
): string[] {
  if (topology.directories === null) {
    return fail("INVALID_PLAN_ROOT_TOPOLOGY", "tenant directories are unresolved");
  }
  const paths: string[] = [pythonPosixJoin(envDir, "expression_bindings.tf")];
  for (const resourceType of members) {
    paths.push(
      pythonPosixJoin(envDir, `${resourceType}_moves.tf`),
      pythonPosixJoin(
        topology.directories.config,
        `${resourceType}.auto.tfvars`,
      ),
      pythonPosixJoin(
        topology.directories.config,
        `${resourceType}.expressions.json`,
      ),
      pythonPosixJoin(
        topology.directories.config,
        `${resourceType}.generated.expressions.json`,
      ),
      pythonPosixJoin(
        topology.directories.imports,
        `${resourceType}_moves.pending.json`,
      ),
      pythonPosixJoin(
        topology.directories.imports,
        `${resourceType}_moves.tf`,
      ),
    );
    if (resourceType !== "zcc_trusted_network") {
      paths.push(pythonPosixJoin(
        topology.directories.config,
        `${resourceType}.lookup.json`,
      ));
    }
  }
  return sortedStrings(paths);
}

/** Render the exact Terraform-1.15.4-formatted no-bindings engine.gen_env bytes. */
export function renderZccPlanRootMain(options: {
  readonly tenant: string;
  readonly label: string;
  readonly members: readonly ZccPullResourceType[];
  readonly backend: "local" | "azurerm";
  readonly moduleSources: ReadonlyMap<string, string>;
}): string {
  const members = sortedStrings(options.members);
  const backendLines = options.backend === "azurerm"
    ? (
        '  backend "azurerm" {\n'
        + "    # Partial configuration. Storage details come from a\n"
        + "    # work-side file at init: make plan BACKEND_CONFIG=<file>\n"
        + "    # (copy backend.conf.example). The state key is derived\n"
        + `    # per root: ${options.tenant}/${options.label}.tfstate\n`
        + "  }\n"
      )
    : (
        "  # local state — opt into remote state with\n"
        + `  # make gen-env TENANT=${options.tenant} BACKEND=azurerm\n`
      );
  const blocks = members.map((resourceType) => {
    const variableName = options.label === resourceType
      ? "items"
      : `${resourceType}_items`;
    const source = options.moduleSources.get(resourceType);
    if (source === undefined) {
      return fail("PLAN_ROOT_MODULE_SOURCE_MISSING", "module source binding is incomplete");
    }
    return (
      `variable "${variableName}" {\n`
      + "  # opaque at the root; the module enforces the strict type.\n"
      + "  type = any\n"
      + "}\n\n"
      + `module "${resourceType}" {\n`
      + `  source = "${source}"\n`
      + `  items  = var.${variableName}\n`
      + "}"
    );
  });
  return (
    `# GENERATED by engine.gen_env for tenant '${options.tenant}' — do not edit.\n`
    + `# Regenerate: make gen-env TENANT=${options.tenant}\n\n`
    + "terraform {\n"
    + '  required_version = ">= 1.5"\n'
    + "  required_providers {\n"
    + "    zcc = {\n"
    + '      source = "zscaler/zcc"\n'
    + "    }\n"
    + "  }\n"
    + backendLines
    + "}\n\n"
    + 'provider "zcc" {\n'
    + "  # credentials via provider environment variables\n"
    + "}\n\n"
    + blocks.join("\n\n")
    + "\n"
  );
}
