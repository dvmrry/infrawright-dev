import { ProcessFailure } from "./errors.js";
import type {
  Deployment,
  RootCatalog,
  RootCatalogResource,
  RootProviderConfig,
  RootTopology,
  WholeRootDiagnostic,
} from "./types.js";
import { pythonPosixJoin } from "./paths.js";
import { sortedStrings } from "../json/python-compatible.js";

const ROOT_LABEL = /^[a-z0-9_]+$/;
const VALID_TENANT = /^[A-Za-z0-9_.-]+$/;

interface CatalogIndex {
  readonly resources: ReadonlyMap<string, RootCatalogResource>;
  readonly generated: ReadonlySet<string>;
  readonly derived: ReadonlySet<string>;
  readonly providers: ReadonlySet<string>;
}

interface Resolution {
  readonly labelsToMembers: ReadonlyMap<string, readonly string[]>;
  readonly typeToLabel: ReadonlyMap<string, string>;
}

function domainError(message: string, code = "INVALID_ROOT_CONFIGURATION"): never {
  throw new ProcessFailure({ code, category: "domain", message });
}

export function validateTenant(tenant: string): void {
  if (!VALID_TENANT.test(tenant) || tenant === "." || tenant === "..") {
    domainError(
      `TENANT must match [A-Za-z0-9_.-]+ and not be . or .. (got '${tenant}')`,
      "INVALID_TENANT",
    );
  }
}

function indexCatalog(catalog: RootCatalog): CatalogIndex {
  const resources = new Map(
    catalog.resources.map((resource) => [resource.type, resource]),
  );
  return {
    resources,
    generated: new Set(
      catalog.resources
        .filter((resource) => resource.generated)
        .map((resource) => resource.type),
    ),
    derived: new Set(
      catalog.resources
        .filter((resource) => resource.generated && resource.derived)
        .map((resource) => resource.type),
    ),
    providers: new Set(catalog.declared_providers),
  };
}

function validateGroupLabel(
  label: string,
  generated: ReadonlySet<string>,
  usedLabels: Set<string>,
  provider: string,
): void {
  if (!ROOT_LABEL.test(label)) {
    domainError(
      `roots.${provider} group label '${label}' must match [a-z0-9_]+`,
    );
  }
  if (generated.has(label)) {
    domainError(
      `roots.${provider} group label '${label}' collides with a generated resource type`,
    );
  }
  if (usedLabels.has(label)) {
    domainError(
      `roots.${provider} group label '${label}' collides with another provider group`,
    );
  }
  usedLabels.add(label);
}

function validateMember(
  provider: string,
  resourceType: string,
  index: CatalogIndex,
): void {
  if (index.derived.has(resourceType)) {
    domainError(
      `roots.${provider} member ${resourceType} is a derived type; derived types keep per-resource roots so IMPORTS_ONLY sequencing works`,
    );
  }
  if (!index.generated.has(resourceType)) {
    domainError(
      `roots.${provider} references unknown generated resource type '${resourceType}'`,
    );
  }
  const actual = index.resources.get(resourceType)?.provider;
  if (actual !== provider) {
    domainError(
      `roots.${provider} member '${resourceType}' belongs to provider ${actual ?? "unknown"}`,
    );
  }
}

function explicitGroups(
  provider: string,
  config: RootProviderConfig,
  index: CatalogIndex,
  labelsToMembers: Map<string, readonly string[]>,
  typeToLabel: Map<string, string>,
  usedLabels: Set<string>,
  explicitMembers: Map<string, string>,
): void {
  const groups = config.groups ?? {};
  for (const label of sortedStrings(Object.keys(groups))) {
    validateGroupLabel(label, index.generated, usedLabels, provider);
    const members = sortedStrings(groups[label] ?? []);
    for (const member of members) {
      validateMember(provider, member, index);
      const previous = explicitMembers.get(member);
      if (previous !== undefined) {
        domainError(
          `${member} appears in more than one roots group (${previous} and ${label})`,
        );
      }
      explicitMembers.set(member, label);
    }
    for (const member of members) {
      labelsToMembers.delete(member);
      typeToLabel.set(member, label);
    }
    labelsToMembers.set(label, members);
  }
}

function slugGroups(
  provider: string,
  index: CatalogIndex,
  labelsToMembers: Map<string, readonly string[]>,
  typeToLabel: Map<string, string>,
  usedLabels: Set<string>,
): void {
  const groups = new Map<string, string[]>();
  for (const resourceType of sortedStrings(index.generated)) {
    if (index.derived.has(resourceType)) {
      continue;
    }
    if (typeToLabel.get(resourceType) !== resourceType) {
      continue;
    }
    const resource = index.resources.get(resourceType);
    if (resource?.provider !== provider) {
      continue;
    }
    if (resource.slug_label === null) {
      domainError(
        `resource type ${resourceType} has no declared prefix for provider ${provider}`,
      );
    }
    const members = groups.get(resource.slug_label) ?? [];
    members.push(resourceType);
    groups.set(resource.slug_label, members);
  }
  for (const label of sortedStrings(groups.keys())) {
    const members = sortedStrings(groups.get(label) ?? []);
    if (members.length < 2) {
      continue;
    }
    validateGroupLabel(label, index.generated, usedLabels, provider);
    for (const member of members) {
      labelsToMembers.delete(member);
      typeToLabel.set(member, label);
    }
    labelsToMembers.set(label, members);
  }
}

function resolveRoots(deployment: Deployment, index: CatalogIndex): Resolution {
  const labelsToMembers = new Map<string, readonly string[]>();
  const typeToLabel = new Map<string, string>();
  for (const resourceType of sortedStrings(index.generated)) {
    labelsToMembers.set(resourceType, [resourceType]);
    typeToLabel.set(resourceType, resourceType);
  }
  const providerNames = sortedStrings(Object.keys(deployment.roots));
  if (providerNames.length === 0) {
    return { labelsToMembers, typeToLabel };
  }
  const usedLabels = new Set<string>();
  const explicitMembers = new Map<string, string>();
  for (const provider of providerNames) {
    if (!index.providers.has(provider)) {
      domainError(`roots.${provider} is not a declared provider prefix value`);
    }
    explicitGroups(
      provider,
      deployment.roots[provider] ?? {},
      index,
      labelsToMembers,
      typeToLabel,
      usedLabels,
      explicitMembers,
    );
  }
  for (const provider of providerNames) {
    const config = deployment.roots[provider] ?? {};
    if ((config.strategy ?? "explicit") === "slug") {
      slugGroups(
        provider,
        index,
        labelsToMembers,
        typeToLabel,
        usedLabels,
      );
    }
  }
  return { labelsToMembers, typeToLabel };
}

function expandResources(
  selectors: readonly string[],
  index: CatalogIndex,
): string[] {
  if (selectors.length === 0) {
    return sortedStrings(index.generated);
  }
  const selected = new Set<string>();
  const unknown: string[] = [];
  for (const selector of selectors) {
    if (index.generated.has(selector)) {
      selected.add(selector);
      continue;
    }
    if (index.resources.has(selector)) {
      unknown.push(selector);
      continue;
    }
    const productMatches = Array.from(index.resources.values())
      .filter(
        (resource) => resource.generated && resource.product === selector,
      )
      .map((resource) => resource.type);
    if (productMatches.length > 0) {
      for (const match of productMatches) {
        selected.add(match);
      }
      continue;
    }
    const slash = selector.indexOf("/");
    if (slash >= 0) {
      const provider = selector.slice(0, slash);
      const bare = selector.slice(slash + 1);
      const pathMatches = Array.from(index.resources.values())
        .filter(
          (resource) => resource.generated
            && resource.provider === provider
            && resource.bare_name === bare,
        )
        .map((resource) => resource.type);
      if (pathMatches.length > 0) {
        for (const match of pathMatches) {
          selected.add(match);
        }
        continue;
      }
    }
    unknown.push(selector);
  }
  if (unknown.length > 0) {
    domainError(
      `unknown or non-generated resource selector(s): ${sortedStrings(unknown).join(", ")}`,
      "UNKNOWN_RESOURCE_SELECTOR",
    );
  }
  return sortedStrings(selected);
}

export function expandCatalogResources(
  catalog: RootCatalog,
  selectors: readonly string[],
): string[] {
  return expandResources(selectors, indexCatalog(catalog));
}

function tenantPath(
  deployment: Deployment,
  tenant: string,
  kind: "config" | "imports" | "envs",
): string {
  const overlay = deployment.overlay;
  if (typeof overlay !== "string") {
    domainError("deployment overlay must be a string when tenant paths are requested");
  }
  const relative = pythonPosixJoin(kind, tenant);
  return overlay === "." ? relative : pythonPosixJoin(overlay, relative);
}

export function rootTopology(options: {
  catalog: RootCatalog;
  deployment: Deployment;
  tenant: string | null;
  selectors: readonly string[];
}): { topology: RootTopology; diagnostics: WholeRootDiagnostic[] } {
  if (options.tenant !== null) {
    validateTenant(options.tenant);
  }
  const index = indexCatalog(options.catalog);
  const resolution = resolveRoots(options.deployment, index);
  const selectedResources = expandResources(options.selectors, index);
  const selected = new Set(selectedResources);
  const labels = options.selectors.length === 0
    ? sortedStrings(resolution.labelsToMembers.keys())
    : sortedStrings(new Set(
        selectedResources.map((resource) => {
          const label = resolution.typeToLabel.get(resource);
          if (label === undefined) {
            return domainError(`unknown generated resource type '${resource}'`);
          }
          return label;
        }),
      ));
  const diagnostics: WholeRootDiagnostic[] = [];
  const roots = labels.map((label) => {
    const members = sortedStrings(resolution.labelsToMembers.get(label) ?? []);
    const selectedMembers = members.filter((member) => selected.has(member));
    const additionalMembers = members.filter((member) => !selected.has(member));
    if (selectedMembers.length > 0 && additionalMembers.length > 0) {
      diagnostics.push({
        level: "note",
        code: "WHOLE_ROOT_SELECTION",
        message: `selecting ${selectedMembers.join(", ")} selects whole root ${label}; also operating on ${additionalMembers.join(", ")}`,
        selected_members: selectedMembers,
        root: label,
        additional_members: additionalMembers,
      });
    }
    return {
      label,
      provider: members.length > 0
        ? index.resources.get(members[0] ?? "")?.provider ?? null
        : null,
      members,
      env_dir: options.tenant === null
        ? null
        : pythonPosixJoin(
            tenantPath(options.deployment, options.tenant, "envs"),
            label,
          ),
    };
  });
  const resourceRootEntries: Array<readonly [string, string]> = [];
  for (const root of roots) {
    for (const member of root.members) {
      resourceRootEntries.push([member, root.label]);
    }
  }
  const resourceRoots = Object.fromEntries(resourceRootEntries) as Record<
    string,
    string
  >;
  return {
    topology: {
      kind: "infrawright.root_topology",
      schema_version: 1,
      tenant: options.tenant,
      selectors: Array.from(options.selectors),
      directories: options.tenant === null
        ? null
        : {
            config: tenantPath(options.deployment, options.tenant, "config"),
            imports: tenantPath(options.deployment, options.tenant, "imports"),
            envs: tenantPath(options.deployment, options.tenant, "envs"),
          },
      roots,
      resource_roots: resourceRoots,
    },
    diagnostics,
  };
}
