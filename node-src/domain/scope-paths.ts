import { ProcessFailure } from "./errors.js";
import {
  pythonPosixJoin,
  pythonPosixNormPath,
  pythonRelativeUnder,
  sameContractPath,
} from "./paths.js";
import { rootTopology } from "./roots.js";
import type {
  ChangedPathKind,
  ChangedPathMatch,
  ChangedPathScope,
  Deployment,
  RootCatalog,
  RootTopologyRoot,
} from "./types.js";
import { sortedStrings } from "../json/python-compatible.js";

const CONFIG_SUFFIXES = [
  ".generated.expressions.json",
  ".expressions.json",
  ".auto.tfvars.json",
  ".auto.tfvars",
  ".lookup.json",
] as const;
const IMPORT_SUFFIXES = ["_imports.tf", "_moves.tf"] as const;

function domainError(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_CHANGED_PATHS",
    category: "domain",
    message,
  });
}

function artifactRoot(deployment: Deployment, kind: string): string {
  if (typeof deployment.overlay !== "string") {
    return domainError("deployment overlay must be a string when paths are scoped");
  }
  return deployment.overlay === "."
    ? kind
    : pythonPosixJoin(deployment.overlay, kind);
}

function moduleRoot(deployment: Deployment): string {
  if (deployment.module_dir !== undefined) {
    if (typeof deployment.module_dir !== "string") {
      return domainError("deployment module_dir must be a string when paths are scoped");
    }
    return deployment.module_dir;
  }
  if (typeof deployment.overlay !== "string") {
    return domainError("deployment overlay must be a string when paths are scoped");
  }
  return deployment.overlay === "."
    ? "modules"
    : pythonPosixJoin(deployment.overlay, "modules", "default");
}

function resourceFromArtifact(
  name: string,
  suffixes: readonly string[],
  resources: ReadonlySet<string>,
): string | null {
  const longestFirst = [...suffixes].sort((left, right) => right.length - left.length);
  for (const suffix of longestFirst) {
    if (!name.endsWith(suffix)) {
      continue;
    }
    const resource = name.slice(0, -suffix.length);
    if (resources.has(resource)) {
      return resource;
    }
  }
  return null;
}

function scopeOnePath(options: {
  path: string;
  workspace: string;
  deploymentPath: string;
  deployment: Deployment;
  resources: ReadonlySet<string>;
  rootsByLabel: ReadonlyMap<string, RootTopologyRoot>;
  resourceRoots: Readonly<Record<string, string>>;
}): ChangedPathMatch | null {
  const matchedResources = new Set<string>();
  const kinds = new Set<ChangedPathKind>();
  const tenants = new Set<string>();

  if (sameContractPath(options.path, options.deploymentPath, options.workspace)) {
    for (const resource of options.resources) {
      matchedResources.add(resource);
    }
    kinds.add("deployment");
  }

  let relative = pythonRelativeUnder(
    options.path,
    artifactRoot(options.deployment, "config"),
    options.workspace,
  );
  if (relative !== null && relative.length === 2) {
    const resource = resourceFromArtifact(
      relative[1] ?? "",
      CONFIG_SUFFIXES,
      options.resources,
    );
    if (resource !== null) {
      matchedResources.add(resource);
      tenants.add(relative[0] ?? "");
      kinds.add("config");
    }
  }

  relative = pythonRelativeUnder(
    options.path,
    artifactRoot(options.deployment, "imports"),
    options.workspace,
  );
  if (relative !== null && relative.length === 2) {
    const resource = resourceFromArtifact(
      relative[1] ?? "",
      IMPORT_SUFFIXES,
      options.resources,
    );
    if (resource !== null) {
      matchedResources.add(resource);
      tenants.add(relative[0] ?? "");
      kinds.add("imports");
    }
  }

  relative = pythonRelativeUnder(
    options.path,
    artifactRoot(options.deployment, "envs"),
    options.workspace,
  );
  if (relative !== null && relative.length >= 2) {
    const root = options.rootsByLabel.get(relative[1] ?? "");
    if (root !== undefined) {
      for (const member of root.members) {
        matchedResources.add(member);
      }
      tenants.add(relative[0] ?? "");
      kinds.add("env_root");
    }
  }

  relative = pythonRelativeUnder(
    options.path,
    moduleRoot(options.deployment),
    options.workspace,
  );
  if (relative !== null && relative.length > 0) {
    const resource = relative[0] ?? "";
    if (options.resources.has(resource)) {
      matchedResources.add(resource);
      kinds.add("module");
    }
  }

  if (matchedResources.size === 0) {
    return null;
  }
  const resources = sortedStrings(matchedResources);
  return {
    path: options.path,
    kinds: sortedStrings(kinds) as ChangedPathKind[],
    tenants: sortedStrings(tenants),
    resources,
    roots: sortedStrings(new Set(resources.map((resource) => {
      const label = options.resourceRoots[resource];
      if (label === undefined) {
        return domainError(`generated resource '${resource}' has no logical root`);
      }
      return label;
    }))),
  };
}

export function changedPathScope(options: {
  paths: readonly string[];
  workspace: string;
  deploymentPath: string;
  deployment: Deployment;
  catalog: RootCatalog;
}): ChangedPathScope {
  if (!Array.isArray(options.paths)) {
    return domainError("changed paths must be a JSON array or repeated --path");
  }
  const normalized: string[] = [];
  for (const [index, candidate] of options.paths.entries()) {
    if (typeof candidate !== "string" || candidate.length === 0) {
      return domainError(`changed path at index ${index} must be a non-empty string`);
    }
    if (candidate.includes("\0")) {
      return domainError(`changed path at index ${index} contains an embedded null character`);
    }
    normalized.push(pythonPosixNormPath(candidate));
  }
  const paths = sortedStrings(new Set(normalized));
  const { topology } = rootTopology({
    catalog: options.catalog,
    deployment: options.deployment,
    tenant: null,
    selectors: [],
  });
  const rootsByLabel = new Map(topology.roots.map((root) => [root.label, root]));
  const resources = new Set(Object.keys(topology.resource_roots));
  const pathMatches: ChangedPathMatch[] = [];
  const unmatchedPaths: string[] = [];
  for (const changedPath of paths) {
    const match = scopeOnePath({
      path: changedPath,
      workspace: options.workspace,
      deploymentPath: options.deploymentPath,
      deployment: options.deployment,
      resources,
      rootsByLabel,
      resourceRoots: topology.resource_roots,
    });
    if (match === null) {
      unmatchedPaths.push(changedPath);
    } else {
      pathMatches.push(match);
    }
  }

  const affectedResources = sortedStrings(new Set(
    pathMatches.flatMap((match) => match.resources),
  ));
  const rootPaths = new Map<string, Set<string>>();
  const rootResources = new Map<string, Set<string>>();
  for (const match of pathMatches) {
    for (const label of match.roots) {
      const matchedPaths = rootPaths.get(label) ?? new Set<string>();
      matchedPaths.add(match.path);
      rootPaths.set(label, matchedPaths);
      const matchedMembers = rootResources.get(label) ?? new Set<string>();
      for (const resource of match.resources) {
        if (topology.resource_roots[resource] === label) {
          matchedMembers.add(resource);
        }
      }
      rootResources.set(label, matchedMembers);
    }
  }

  return {
    kind: "infrawright.changed_path_scope",
    schema_version: 1,
    paths,
    path_matches: pathMatches,
    unmatched_paths: unmatchedPaths,
    affected_resources: affectedResources,
    affected_roots: sortedStrings(rootPaths.keys()).map((label) => {
      const root = rootsByLabel.get(label);
      if (root === undefined) {
        return domainError(`logical root '${label}' is missing from topology`);
      }
      return {
        label,
        provider: root.provider,
        members: root.members,
        matched_resources: sortedStrings(rootResources.get(label) ?? []),
        paths: sortedStrings(rootPaths.get(label) ?? []),
      };
    }),
  };
}
