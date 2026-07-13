import {
  activePackSelection,
  loadPackMetadata,
  providerForResource,
  validateActivePackSet,
  type PackManifest,
  type PackMetadata,
  type PackSelection,
  type PackSetDocument,
} from "./packs.js";
import {
  loadOverrides,
  loadProviderSchema,
  loadRegistry,
  loadResourceSchema,
  type LoadedOverrides,
  type LoadedRegistry,
  type ProviderSchema,
} from "./resources.js";
import type { JsonObject } from "./validation.js";

export interface LoadedResourceMetadata {
  readonly type: string;
  readonly product: string;
  readonly provider: string;
  readonly pack: string | null;
  readonly registry: Readonly<JsonObject>;
  readonly override: Readonly<JsonObject> | null;
}

export interface LoadedPackRoot {
  readonly root: string;
  readonly profile: PackSetDocument | null;
  readonly active: PackSelection;
  readonly packs: PackMetadata;
  readonly registry: LoadedRegistry;
  readonly overrides: LoadedOverrides;
  readonly resources: ReadonlyMap<string, LoadedResourceMetadata>;
  loadProviderSchema(provider: string): Promise<ProviderSchema>;
  loadResourceSchema(resourceType: string): Promise<Readonly<JsonObject>>;
}

function ownerForProvider(
  metadata: PackMetadata,
  provider: string,
): PackManifest | undefined {
  const owner = metadata.providerOwners[provider];
  return owner === undefined
    ? undefined
    : metadata.manifests.find((manifest) => manifest.name === owner);
}

function resourceMap(
  metadata: PackMetadata,
  registry: LoadedRegistry,
  overrides: LoadedOverrides,
): ReadonlyMap<string, LoadedResourceMetadata> {
  const resources = new Map<string, LoadedResourceMetadata>();
  for (const [resourceType, entry] of Object.entries(registry.entries)) {
    const provider = providerForResource(metadata, resourceType);
    const product = entry.product;
    if (typeof product !== "string") {
      throw new TypeError(`${resourceType} registry product is not a string`);
    }
    const override = overrides.entries[resourceType];
    resources.set(resourceType, {
      type: resourceType,
      product,
      provider,
      pack: ownerForProvider(metadata, provider)?.name ?? null,
      registry: entry,
      override: override ?? null,
    });
  }
  return resources;
}

export async function loadPackRoot(options: {
  readonly packsRoot: string;
  readonly profilePath?: string;
  readonly catalogPath?: string;
}): Promise<LoadedPackRoot> {
  let metadata: PackMetadata;
  let profile: PackSetDocument | null = null;
  let active: PackSelection;
  if (options.profilePath === undefined) {
    metadata = await loadPackMetadata(options.packsRoot);
    active = await activePackSelection(options.packsRoot);
  } else {
    const result = await validateActivePackSet({
      profilePath: options.profilePath,
      root: options.packsRoot,
      ...(options.catalogPath === undefined
        ? {}
        : { catalogPath: options.catalogPath }),
    });
    metadata = result.metadata;
    profile = result.profile;
    active = result.active;
  }
  const [registry, overrides] = await Promise.all([
    loadRegistry(metadata, active.packs),
    loadOverrides(metadata, active.packs),
  ]);
  return {
    root: metadata.root,
    profile,
    active,
    packs: metadata,
    registry,
    overrides,
    resources: resourceMap(metadata, registry, overrides),
    loadProviderSchema: async (provider: string) => {
      return loadProviderSchema(metadata, provider);
    },
    loadResourceSchema: async (resourceType: string) => {
      return loadResourceSchema(metadata, resourceType);
    },
  };
}
