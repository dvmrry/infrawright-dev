import { readdir, stat } from "node:fs/promises";
import path from "node:path";

import { ProcessFailure } from "./errors.js";
import { pythonPosixJoin } from "./paths.js";
import {
  expandCatalogResources,
  loadedRootTopology,
  rootTopology,
  validateTenant,
} from "./roots.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import type {
  Deployment,
  MaterializedPlanRoot,
  PlanRoots,
  RootCatalog,
  RootTopology,
  RootTopologyRoot,
  WholeRootDiagnostic,
} from "./types.js";
import { sortedStrings } from "../json/python-compatible.js";

function deploymentError(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_DEPLOYMENT",
    category: "domain",
    message,
  });
}

function resolveWorkspacePath(workspace: string, candidate: string): string {
  return path.posix.isAbsolute(candidate)
    ? candidate
    : pythonPosixJoin(workspace, candidate);
}

async function isDirectory(workspace: string, candidate: string): Promise<boolean> {
  try {
    return (await stat(resolveWorkspacePath(workspace, candidate))).isDirectory();
  } catch {
    return false;
  }
}

async function isFile(workspace: string, candidate: string): Promise<boolean> {
  try {
    return (await stat(resolveWorkspacePath(workspace, candidate))).isFile();
  } catch {
    return false;
  }
}

async function directoryNames(workspace: string, candidate: string): Promise<string[]> {
  try {
    return sortedStrings(await readdir(resolveWorkspacePath(workspace, candidate)));
  } catch {
    throw new ProcessFailure({
      code: "READ_FAILED",
      category: "io",
      message: "unable to enumerate materialized environment roots",
    });
  }
}

function envBase(deployment: Deployment): string {
  if (typeof deployment.overlay !== "string") {
    return deploymentError("deployment overlay must be a string when plan roots are enumerated");
  }
  return deployment.overlay === "."
    ? "envs"
    : pythonPosixJoin(deployment.overlay, "envs");
}

interface DiscoveredRoot {
  readonly tenant: string;
  readonly path: string;
  readonly root: RootTopologyRoot;
}

async function discover(options: {
  workspace: string;
  deployment: Deployment;
  tenant: string | null;
  rootsByLabel: ReadonlyMap<string, RootTopologyRoot>;
}): Promise<DiscoveredRoot[]> {
  const base = envBase(options.deployment);
  const tenantNames = options.tenant === null
    ? await (async () => {
        if (!await isDirectory(options.workspace, base)) {
          return [];
        }
        return directoryNames(options.workspace, base);
      })()
    : [options.tenant];
  const discovered: DiscoveredRoot[] = [];
  for (const tenant of tenantNames) {
    const tenantDir = pythonPosixJoin(base, tenant);
    if (!await isDirectory(options.workspace, tenantDir)) {
      continue;
    }
    for (const label of await directoryNames(options.workspace, tenantDir)) {
      const root = options.rootsByLabel.get(label);
      if (root === undefined) {
        continue;
      }
      const rootPath = pythonPosixJoin(tenantDir, label);
      if (await isDirectory(options.workspace, rootPath)) {
        discovered.push({ tenant, path: rootPath, root });
      }
    }
  }
  return discovered;
}

async function planRootsFromTopologies(options: {
  workspace: string;
  deployment: Deployment;
  tenant: string | null;
  selectors: readonly string[];
  all: RootTopology;
  selected: { topology: RootTopology; diagnostics: readonly WholeRootDiagnostic[] };
}): Promise<{ result: PlanRoots; diagnostics: WholeRootDiagnostic[] }> {
  if (options.tenant !== null) {
    validateTenant(options.tenant);
  }
  const all = options.all;
  const selected = options.selected;
  const selectedLabels = new Set(selected.topology.roots.map((root) => root.label));
  const diagnosticsByLabel = new Map(
    selected.diagnostics.map((diagnostic) => [diagnostic.root, diagnostic]),
  );
  const discovered = await discover({
    workspace: options.workspace,
    deployment: options.deployment,
    tenant: options.tenant,
    rootsByLabel: new Map(all.roots.map((root) => [root.label, root])),
  });
  const diagnostics: WholeRootDiagnostic[] = [];
  const roots: MaterializedPlanRoot[] = [];
  for (const entry of discovered) {
    if (!selectedLabels.has(entry.root.label)) {
      continue;
    }
    validateTenant(entry.tenant);
    const diagnostic = diagnosticsByLabel.get(entry.root.label);
    if (diagnostic !== undefined) {
      diagnostics.push(diagnostic);
    }
    const tfplanPath = pythonPosixJoin(entry.path, "tfplan");
    const sourcesPath = pythonPosixJoin(entry.path, "tfplan.sources");
    const planExists = await isFile(options.workspace, tfplanPath);
    const sourcesExist = await isFile(options.workspace, sourcesPath);
    const artifactState = planExists && sourcesExist
      ? "complete"
      : planExists || sourcesExist
      ? "incomplete"
      : "absent";
    roots.push({
      tenant: entry.tenant,
      label: entry.root.label,
      provider: entry.root.provider,
      members: entry.root.members,
      env_dir: entry.path,
      artifact_state: artifactState,
      artifacts: {
        tfplan: { path: tfplanPath, exists: planExists },
        tfplan_sources: { path: sourcesPath, exists: sourcesExist },
      },
    });
  }
  return {
    result: {
      kind: "infrawright.plan_roots",
      schema_version: 1,
      request: {
        tenant: options.tenant,
        selectors: Array.from(options.selectors),
      },
      roots,
    },
    diagnostics,
  };
}

export async function planRoots(options: {
  workspace: string;
  deployment: Deployment;
  catalog: RootCatalog;
  tenant: string | null;
  selectors: readonly string[];
}): Promise<{ result: PlanRoots; diagnostics: WholeRootDiagnostic[] }> {
  if (options.selectors.length > 0) {
    // Preserve the historical explicit validation before root resolution.
    expandCatalogResources(options.catalog, options.selectors);
  }
  return planRootsFromTopologies({
    ...options,
    all: rootTopology({
      catalog: options.catalog,
      deployment: options.deployment,
      tenant: null,
      selectors: [],
    }).topology,
    selected: rootTopology({
      catalog: options.catalog,
      deployment: options.deployment,
      tenant: null,
      selectors: options.selectors,
    }),
  });
}

/** Enumerate materialized roots from the active pack metadata loader. */
export async function loadedPlanRoots(options: {
  workspace: string;
  deployment: Deployment;
  root: LoadedPackRoot;
  tenant: string | null;
  selectors: readonly string[];
}): Promise<{ result: PlanRoots; diagnostics: WholeRootDiagnostic[] }> {
  return planRootsFromTopologies({
    ...options,
    all: loadedRootTopology({
      deployment: options.deployment,
      root: options.root,
      tenant: null,
      selectors: [],
    }).topology,
    selected: loadedRootTopology({
      deployment: options.deployment,
      root: options.root,
      tenant: null,
      selectors: options.selectors,
    }),
  });
}
