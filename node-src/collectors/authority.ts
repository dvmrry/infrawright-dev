import { sortedStrings } from "../json/python-compatible.js";
import type { LoadedPackRoot } from "../metadata/loader.js";
import type { CollectorAdapter } from "./types.js";

export interface CollectorAdapterAuthorities {
  readonly byProviderSource: ReadonlyMap<string, CollectorAdapter>;
}

/**
 * Resolve selected resource adapters through pack-owned provider sources.
 *
 * The pack root declares its provider source, while the caller chooses the
 * concrete source-to-adapter bindings it is willing to execute. The adapter
 * still owns authentication and URL composition; registry metadata can select
 * only paths under that adapter's fixed provider host.
 */
export function resolveCollectorAdapters(options: {
  readonly authorities: CollectorAdapterAuthorities;
  readonly resourceTypes: readonly string[];
  readonly root: LoadedPackRoot;
}): ReadonlyMap<string, CollectorAdapter> {
  const selected = new Map<string, CollectorAdapter>();
  const sourcesByProduct = new Map<string, string>();
  for (const resourceType of sortedStrings(options.resourceTypes)) {
    const resource = options.root.resources.get(resourceType);
    if (resource === undefined) {
      throw new Error(`selected fetch resource ${JSON.stringify(resourceType)} is not active`);
    }
    const { product, provider } = resource;
    const owner = options.root.packs.providerOwners[provider];
    if (owner === undefined) {
      throw new Error(
        `selected fetch resource ${JSON.stringify(resourceType)} uses provider ${JSON.stringify(provider)} without an owning pack`,
      );
    }
    const manifest = options.root.packs.manifests.find((item) => item.name === owner);
    if (manifest === undefined) {
      throw new Error(
        `selected fetch resource ${JSON.stringify(resourceType)} uses provider ${JSON.stringify(provider)} without a loaded owning pack`,
      );
    }
    const providerSource = manifest.providerSources[provider];
    if (providerSource === undefined) {
      throw new Error(
        `pack ${JSON.stringify(owner)} owns selected fetch resource ${JSON.stringify(resourceType)} through provider ${JSON.stringify(provider)} without a provider source`,
      );
    }
    const adapter = options.authorities.byProviderSource.get(providerSource);
    if (adapter === undefined) {
      throw new Error(
        `selected fetch resource ${JSON.stringify(resourceType)} uses provider source ${JSON.stringify(providerSource)} and product ${JSON.stringify(product)}, but a matching collector adapter is not available; use a caller with a matching injected Node adapter`,
      );
    }
    if (adapter.product !== product) {
      throw new Error(
        `selected fetch resource ${JSON.stringify(resourceType)} declares product ${JSON.stringify(product)}, but provider source ${JSON.stringify(providerSource)} is bound to collector product ${JSON.stringify(adapter.product)}`,
      );
    }
    const priorSource = sourcesByProduct.get(product);
    if (priorSource !== undefined && priorSource !== providerSource) {
      throw new Error(
        `selected fetch product ${JSON.stringify(product)} spans provider sources ${JSON.stringify(priorSource)} and ${JSON.stringify(providerSource)}; one product cannot borrow authority across providers`,
      );
    }
    sourcesByProduct.set(product, providerSource);
    selected.set(product, adapter);
  }
  return selected;
}
