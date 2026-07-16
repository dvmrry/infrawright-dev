import path from "node:path";

import { ProcessFailure } from "./errors.js";
import type { BoundAssessmentControlFile } from "./control-evidence.js";
import { pythonPosixJoin } from "./paths.js";
import { loadedPlanRoots, planRoots } from "./plan-roots.js";
import { crossStateReferenceTopology } from "./reference-topology.js";
import { loadedRootTopology } from "./roots.js";
import { transformArtifactPaths } from "./transform-artifacts.js";
import type {
  SavedPlanAssessmentOptions,
  SavedPlanAssessmentRootInput,
} from "./plan-assessment.js";
import type { Deployment, RootCatalog, WholeRootDiagnostic } from "./types.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import type { TerraformShowLimits } from "../io/terraform-show.js";
import { sameStringSequence, sortedStrings } from "../json/python-compatible.js";

export interface ResolveSavedPlanAssessmentOptions {
  readonly workspace: string;
  readonly deployment: Deployment;
  readonly catalog: RootCatalog;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly terraformExecutable: string;
  readonly backendConfig: string | null;
  readonly policyPath: string | null;
  readonly controlFiles?: readonly BoundAssessmentControlFile[];
  readonly terraformShowLimits?: TerraformShowLimits;
}

export interface ResolveLoadedSavedPlanAssessmentOptions {
  readonly workspace: string;
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
  readonly terraformExecutable: string;
  readonly backendConfig: string | null;
  readonly policyPath: string | null;
  readonly controlFiles?: readonly BoundAssessmentControlFile[];
  readonly terraformShowLimits?: TerraformShowLimits;
}

export interface SavedPlanAssessmentContext {
  readonly workspace: string;
  readonly deployment: Deployment;
  readonly catalog: RootCatalog;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
}

export interface LoadedSavedPlanAssessmentContext {
  readonly workspace: string;
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly tenant: string | null;
  readonly selectors: readonly string[];
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
        ...(config.cross_state_references === undefined
          ? {}
          : { cross_state_references: config.cross_state_references }),
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

export function copySavedPlanAssessmentContext(
  context: SavedPlanAssessmentContext,
): SavedPlanAssessmentContext {
  return {
    workspace: context.workspace,
    deployment: copyDeployment(context.deployment),
    catalog: copyCatalog(context.catalog),
    tenant: context.tenant,
    selectors: [...context.selectors],
  };
}

export function copyLoadedSavedPlanAssessmentContext(
  context: LoadedSavedPlanAssessmentContext,
): LoadedSavedPlanAssessmentContext {
  return {
    workspace: context.workspace,
    deployment: copyDeployment(context.deployment),
    root: context.root,
    tenant: context.tenant,
    selectors: [...context.selectors],
  };
}

async function materializeSavedPlanAssessmentRoots(
  supplied: SavedPlanAssessmentContext,
): Promise<{
  readonly roots: readonly SavedPlanAssessmentRootInput[];
  readonly diagnostics: readonly WholeRootDiagnostic[];
}> {
  const context = copySavedPlanAssessmentContext(supplied);
  if (!path.isAbsolute(context.workspace)) {
    return fail("INVALID_WORKSPACE", "assessment workspace must be absolute");
  }
  const materialized = await planRoots({
    workspace: context.workspace,
    deployment: context.deployment,
    catalog: context.catalog,
    tenant: context.tenant,
    selectors: context.selectors,
  });
  const selected = materialized.result.roots.filter((root) => {
    return root.artifacts.tfplan.exists;
  });
  const suffix = selected.length === 0
    ? null
    : tfvarsSuffix(context.deployment);
  const roots = selected.map((root): SavedPlanAssessmentRootInput => {
    const configDir = configDirectory(context.deployment, root.tenant);
    return {
      tenant: root.tenant,
      label: root.label,
      members: [...root.members],
      envDir: resolve(context.workspace, root.env_dir),
      savedPlanPath: resolve(context.workspace, root.artifacts.tfplan.path),
      fingerprintPath: resolve(
        context.workspace,
        root.artifacts.tfplan_sources.path,
      ),
      varFiles: root.members.map((member) => resolve(
        context.workspace,
        pythonPosixJoin(configDir, `${member}${suffix as string}`),
      )),
    };
  });
  return {
    roots,
    diagnostics: materialized.diagnostics.map((diagnostic) => ({
      ...diagnostic,
      selected_members: [...diagnostic.selected_members],
      additional_members: [...diagnostic.additional_members],
    })),
  };
}

function sameAssessmentRoots(
  left: readonly SavedPlanAssessmentRootInput[],
  right: readonly SavedPlanAssessmentRootInput[],
): boolean {
  return left.length === right.length && left.every((root, index) => {
    const other = right[index];
    return other !== undefined
      && root.tenant === other.tenant
      && root.label === other.label
      && sameStringSequence(root.members, other.members)
      && root.envDir === other.envDir
      && root.savedPlanPath === other.savedPlanPath
      && root.fingerprintPath === other.fingerprintPath
      && sameStringSequence(root.varFiles, other.varFiles)
      && sameStringSequence(
        root.referenceOutputTypes ?? [],
        other.referenceOutputTypes ?? [],
      );
  });
}

export async function recheckSavedPlanAssessmentContext(
  context: SavedPlanAssessmentContext,
  expectedRoots: readonly SavedPlanAssessmentRootInput[],
): Promise<void> {
  let current: readonly SavedPlanAssessmentRootInput[];
  try {
    current = (await materializeSavedPlanAssessmentRoots(context)).roots;
  } catch {
    return fail(
      "ASSESSMENT_CONTEXT_CHANGED",
      "saved-plan assessment context changed during assessment",
    );
  }
  if (!sameAssessmentRoots(expectedRoots, current)) {
    return fail(
      "ASSESSMENT_CONTEXT_CHANGED",
      "saved-plan assessment context changed during assessment",
    );
  }
}

export async function materializeLoadedSavedPlanAssessmentRoots(
  supplied: LoadedSavedPlanAssessmentContext,
): Promise<{
  readonly roots: readonly SavedPlanAssessmentRootInput[];
  readonly diagnostics: readonly WholeRootDiagnostic[];
}> {
  const context = copyLoadedSavedPlanAssessmentContext(supplied);
  if (!path.isAbsolute(context.workspace)) {
    return fail("INVALID_WORKSPACE", "assessment workspace must be absolute");
  }
  const selected = await loadedPlanRoots({
    workspace: context.workspace,
    deployment: context.deployment,
    root: context.root,
    tenant: context.tenant,
    selectors: context.selectors,
  });
  const fullTopology = loadedRootTopology({
    deployment: context.deployment,
    root: context.root,
    tenant: null,
    selectors: [],
  }).topology;
  const referenceOutputs = crossStateReferenceTopology({
    deployment: context.deployment,
    root: context.root,
    topology: fullTopology,
  }).outputsByRoot;
  return {
    roots: selected.result.roots
      .filter((root) => root.artifacts.tfplan.exists)
      .map((root): SavedPlanAssessmentRootInput => ({
        tenant: root.tenant,
        label: root.label,
        members: [...root.members],
        envDir: resolve(context.workspace, root.env_dir),
        savedPlanPath: resolve(context.workspace, root.artifacts.tfplan.path),
        fingerprintPath: resolve(
          context.workspace,
          root.artifacts.tfplan_sources.path,
        ),
        varFiles: root.members.map((resourceType) => resolve(
          context.workspace,
          transformArtifactPaths({
            deployment: context.deployment,
            resourceType,
            tenant: root.tenant,
          }).config,
        )),
        ...((referenceOutputs.get(root.label)?.size ?? 0) === 0
          ? {}
          : {
              referenceOutputTypes: sortedStrings(
                referenceOutputs.get(root.label) ?? [],
              ),
            }),
      })),
    diagnostics: selected.diagnostics.map((diagnostic) => ({
      ...diagnostic,
      selected_members: [...diagnostic.selected_members],
      additional_members: [...diagnostic.additional_members],
    })),
  };
}

export async function recheckLoadedSavedPlanAssessmentContext(
  context: LoadedSavedPlanAssessmentContext,
  expectedRoots: readonly SavedPlanAssessmentRootInput[],
): Promise<void> {
  let current: readonly SavedPlanAssessmentRootInput[];
  try {
    current = (await materializeLoadedSavedPlanAssessmentRoots(context)).roots;
  } catch {
    return fail(
      "ASSESSMENT_CONTEXT_CHANGED",
      "saved-plan assessment context changed during assessment",
    );
  }
  if (!sameAssessmentRoots(expectedRoots, current)) {
    return fail(
      "ASSESSMENT_CONTEXT_CHANGED",
      "saved-plan assessment context changed during assessment",
    );
  }
}

/** Resolve public topology/artifact inputs into the narrow assessment core. */
export interface ResolvedSavedPlanAssessment {
  readonly assessment: SavedPlanAssessmentOptions;
  readonly diagnostics: readonly WholeRootDiagnostic[];
}

export async function resolveSavedPlanAssessment(
  options: ResolveSavedPlanAssessmentOptions,
): Promise<ResolvedSavedPlanAssessment> {
  const context = copySavedPlanAssessmentContext({
    workspace: options.workspace,
    deployment: options.deployment,
    catalog: options.catalog,
    tenant: options.tenant,
    selectors: options.selectors,
  });
  const captured = {
    context,
    terraformExecutable: options.terraformExecutable,
    backendConfig: options.backendConfig,
    policyPath: options.policyPath,
    controlFiles: (options.controlFiles ?? []).map((file) => ({
      path: file.path,
      digest: file.digest === null ? null : { ...file.digest },
    })),
    ...(options.terraformShowLimits === undefined
      ? {}
      : { terraformShowLimits: { ...options.terraformShowLimits } }),
  };
  const materialized = await materializeSavedPlanAssessmentRoots(context);
  return {
    assessment: {
      terraformExecutable: captured.terraformExecutable,
      roots: materialized.roots,
      backendConfig: captured.backendConfig === null
        ? null
        : resolve(context.workspace, captured.backendConfig),
      policyPath: captured.policyPath === null
        ? null
        : resolve(context.workspace, captured.policyPath),
      controlFiles: captured.controlFiles,
      context,
      ...(captured.terraformShowLimits === undefined
        ? {}
        : { terraformShowLimits: captured.terraformShowLimits }),
    },
    diagnostics: materialized.diagnostics,
  };
}

export async function resolveSavedPlanAssessmentOptions(
  options: ResolveSavedPlanAssessmentOptions,
): Promise<SavedPlanAssessmentOptions> {
  return (await resolveSavedPlanAssessment(options)).assessment;
}

/** Resolve the real active pack/deployment topology for operational CLI use. */
export async function resolveLoadedSavedPlanAssessment(
  options: ResolveLoadedSavedPlanAssessmentOptions,
): Promise<ResolvedSavedPlanAssessment> {
  const context = copyLoadedSavedPlanAssessmentContext({
    workspace: options.workspace,
    deployment: options.deployment,
    root: options.root,
    tenant: options.tenant,
    selectors: options.selectors,
  });
  const selected = await materializeLoadedSavedPlanAssessmentRoots(context);
  return {
    assessment: {
      terraformExecutable: options.terraformExecutable,
      roots: selected.roots,
      backendConfig: options.backendConfig === null
        ? null
        : resolve(context.workspace, options.backendConfig),
      policyPath: options.policyPath === null
        ? null
        : resolve(context.workspace, options.policyPath),
      controlFiles: (options.controlFiles ?? []).map((file) => ({
        path: file.path,
        digest: file.digest === null ? null : { ...file.digest },
        ...(file.identity === undefined
          ? {}
          : { identity: file.identity === null ? null : { ...file.identity } }),
        ...(file.followSymlinks === undefined
          ? {}
          : { followSymlinks: file.followSymlinks }),
      })),
      loadedContext: context,
      ...(options.terraformShowLimits === undefined
        ? {}
        : { terraformShowLimits: { ...options.terraformShowLimits } }),
    },
    diagnostics: selected.diagnostics.map((diagnostic) => ({
      ...diagnostic,
      selected_members: [...diagnostic.selected_members],
      additional_members: [...diagnostic.additional_members],
    })),
  };
}
