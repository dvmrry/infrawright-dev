import path from "node:path";

import { ProcessFailure } from "./errors.js";
import { pythonPosixJoin } from "./paths.js";
import { planRoots } from "./plan-roots.js";
import type {
  SavedPlanAssessmentOptions,
  SavedPlanAssessmentRootInput,
} from "./plan-assessment.js";
import type { Deployment, RootCatalog, WholeRootDiagnostic } from "./types.js";
import type { TerraformShowLimits } from "../io/terraform-show.js";

export interface ResolveSavedPlanAssessmentOptions {
  readonly workspace: string;
  readonly deployment: Deployment;
  readonly catalog: RootCatalog;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly terraformExecutable: string;
  readonly backendConfig: string | null;
  readonly policyPath: string | null;
  readonly terraformShowLimits?: TerraformShowLimits;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

function resolve(workspace: string, candidate: string): string {
  return path.isAbsolute(candidate) ? candidate : path.resolve(workspace, candidate);
}

function copyDeployment(deployment: Deployment): Deployment {
  const roots: Record<string, Deployment["roots"][string]> = Object.create(null) as Record<
    string,
    Deployment["roots"][string]
  >;
  for (const [provider, config] of Object.entries(deployment.roots)) {
    const groups = config.groups === undefined
      ? undefined
      : Object.fromEntries(Object.entries(config.groups).map(([label, members]) => {
          return [label, [...members]];
        }));
    Object.defineProperty(roots, provider, {
      configurable: true,
      enumerable: true,
      value: {
        ...(config.strategy === undefined ? {} : { strategy: config.strategy }),
        ...(groups === undefined ? {} : { groups }),
        ...(config.bind_references === undefined
          ? {}
          : { bind_references: config.bind_references }),
      },
      writable: true,
    });
  }
  return {
    overlay: deployment.overlay,
    ...(deployment.module_dir === undefined
      ? {}
      : { module_dir: deployment.module_dir }),
    ...(deployment.tfvars_format === undefined
      ? {}
      : { tfvars_format: deployment.tfvars_format }),
    roots,
  };
}

function copyCatalog(catalog: RootCatalog): RootCatalog {
  return {
    kind: catalog.kind,
    schema_version: catalog.schema_version,
    declared_providers: [...catalog.declared_providers],
    resources: catalog.resources.map((resource) => ({ ...resource })),
    source_files: [...catalog.source_files],
    sources_sha256: catalog.sources_sha256,
  };
}

function tfvarsSuffix(deployment: Deployment): string {
  const format = deployment.tfvars_format === undefined
    ? "json"
    : deployment.tfvars_format;
  if (format === "json") {
    return ".auto.tfvars.json";
  }
  if (format === "hcl") {
    return ".auto.tfvars";
  }
  return fail(
    "INVALID_DEPLOYMENT",
    "deployment tfvars_format must be 'json' or 'hcl' for assessment",
  );
}

function configDirectory(deployment: Deployment, tenant: string): string {
  if (typeof deployment.overlay !== "string") {
    return fail("INVALID_DEPLOYMENT", "deployment overlay must be a string for assessment");
  }
  return deployment.overlay === "."
    ? pythonPosixJoin("config", tenant)
    : pythonPosixJoin(deployment.overlay, "config", tenant);
}

/** Resolve public topology/artifact inputs into the narrow assessment core. */
export interface ResolvedSavedPlanAssessment {
  readonly assessment: SavedPlanAssessmentOptions;
  readonly diagnostics: readonly WholeRootDiagnostic[];
}

export async function resolveSavedPlanAssessment(
  options: ResolveSavedPlanAssessmentOptions,
): Promise<ResolvedSavedPlanAssessment> {
  const captured = {
    workspace: options.workspace,
    deployment: copyDeployment(options.deployment),
    catalog: copyCatalog(options.catalog),
    tenant: options.tenant,
    selectors: [...options.selectors],
    terraformExecutable: options.terraformExecutable,
    backendConfig: options.backendConfig,
    policyPath: options.policyPath,
    ...(options.terraformShowLimits === undefined
      ? {}
      : { terraformShowLimits: { ...options.terraformShowLimits } }),
  };
  if (!path.isAbsolute(captured.workspace)) {
    return fail("INVALID_WORKSPACE", "assessment workspace must be absolute");
  }
  const materialized = await planRoots({
    workspace: captured.workspace,
    deployment: captured.deployment,
    catalog: captured.catalog,
    tenant: captured.tenant,
    selectors: captured.selectors,
  });
  const selected = materialized.result.roots.filter((root) => {
    return root.artifacts.tfplan.exists;
  });
  const suffix = selected.length === 0
    ? null
    : tfvarsSuffix(captured.deployment);
  const roots: SavedPlanAssessmentRootInput[] = selected.map((root) => {
    const configDir = configDirectory(captured.deployment, root.tenant);
    return {
      tenant: root.tenant,
      label: root.label,
      members: root.members,
      envDir: resolve(captured.workspace, root.env_dir),
      savedPlanPath: resolve(captured.workspace, root.artifacts.tfplan.path),
      fingerprintPath: resolve(
        captured.workspace,
        root.artifacts.tfplan_sources.path,
      ),
      varFiles: root.members.map((member) => resolve(
        captured.workspace,
        pythonPosixJoin(configDir, `${member}${suffix as string}`),
      )),
    };
  });
  return {
    assessment: {
      terraformExecutable: captured.terraformExecutable,
      roots,
      backendConfig: captured.backendConfig === null
        ? null
        : resolve(captured.workspace, captured.backendConfig),
      policyPath: captured.policyPath === null
        ? null
        : resolve(captured.workspace, captured.policyPath),
      ...(captured.terraformShowLimits === undefined
        ? {}
        : { terraformShowLimits: captured.terraformShowLimits }),
    },
    diagnostics: materialized.diagnostics.map((diagnostic) => ({
      ...diagnostic,
      selected_members: [...diagnostic.selected_members],
      additional_members: [...diagnostic.additional_members],
    })),
  };
}

export async function resolveSavedPlanAssessmentOptions(
  options: ResolveSavedPlanAssessmentOptions,
): Promise<SavedPlanAssessmentOptions> {
  return (await resolveSavedPlanAssessment(options)).assessment;
}
