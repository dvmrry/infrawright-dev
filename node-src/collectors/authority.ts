import { sortedStrings } from "../json/python-compatible.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import type { CollectorAdapter } from "./types.js";

export interface CollectorAdapterAuthorities {
  readonly byProviderSource: ReadonlyMap<string, CollectorAdapter>;
}

/**
 * Resolve selected product adapters through pack-owned provider sources.
 *
 * The pack root declares its provider source, while the caller chooses the
 * concrete source-to-adapter bindings it is willing to execute. The adapter
 * still owns authentication and URL composition; registry metadata can select
 * only paths under that adapter's fixed provider host.
 */
export function resolveCollectorAdapters(options: {
  readonly authorities: CollectorAdapterAuthorities;
  readonly products: ReadonlySet<string>;
  readonly root: LoadedPackRoot;
}): ReadonlyMap<string, CollectorAdapter> {
  const selected = new Map<string, CollectorAdapter>();
  for (const product of sortedStrings(options.products)) {
    const owner = options.root.packs.providerOwners[product];
    if (owner === undefined) {
      throw new Error(
        `selected fetch product ${JSON.stringify(product)} has no owning provider pack`,
      );
    }
    const manifest = options.root.packs.manifests.find((item) => item.name === owner);
    if (manifest === undefined) {
      throw new Error(
        `selected fetch product ${JSON.stringify(product)} has no loaded provider pack`,
      );
    }
    const providerSource = manifest.providerSources[product];
    if (providerSource === undefined) {
      throw new Error(
        `pack ${JSON.stringify(owner)} declares selected fetch product ${JSON.stringify(product)} without a provider source`,
      );
    }
    const adapter = options.authorities.byProviderSource.get(providerSource);
    if (adapter === undefined) {
      throw new Error(
        `collector adapter for provider source ${JSON.stringify(providerSource)} and product ${JSON.stringify(product)} is not available; use a caller with a matching injected Node adapter`,
      );
    }
    if (adapter.product !== product) {
      throw new Error(
        `collector adapter for provider source ${JSON.stringify(providerSource)} is bound to product ${JSON.stringify(adapter.product)}, not selected product ${JSON.stringify(product)}`,
      );
    }
    selected.set(product, adapter);
  }
  return selected;
}
