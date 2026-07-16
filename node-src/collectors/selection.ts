import type { LoadedPackRoot } from "../metadata/loader.js";
import { isObject } from "../metadata/validation.js";
import { sortedStrings } from "../json/python-compatible.js";

function fetchResourceTypes(root: LoadedPackRoot): string[] {
  return sortedStrings(
    [...root.resources.values()]
      .filter((resource) => isObject(resource.registry.fetch))
      .map((resource) => resource.type),
  );
}

/** Return the active product names that own at least one fetch entry. */
export function fetchProducts(root: LoadedPackRoot): string[] {
  return sortedStrings(new Set(
    [...root.resources.values()]
      .filter((resource) => isObject(resource.registry.fetch))
      .map((resource) => resource.product),
  ));
}

/**
 * Expand collector selectors using the original registry as the only resource
 * authority. Product selectors expand to all of that product's fetch entries;
 * derived resources select their fetch-bearing source.
 */
export function selectFetchResources(options: {
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
}): string[] {
  const fetchable = fetchResourceTypes(options.root);
  if (options.selectors.length === 0) return fetchable;

  const fetchableSet = new Set(fetchable);
  const products = fetchProducts(options.root);
  const productSet = new Set(products);
  const selected = new Set<string>();
  const unknown = new Set<string>();

  for (const selector of options.selectors) {
    if (productSet.has(selector)) {
      for (const resource of options.root.resources.values()) {
        if (
          resource.product === selector
          && isObject(resource.registry.fetch)
        ) {
          selected.add(resource.type);
        }
      }
      continue;
    }

    if (fetchableSet.has(selector)) {
      selected.add(selector);
      continue;
    }

    const resource = options.root.resources.get(selector);
    const derive = resource?.registry.derive;
    if (isObject(derive) && typeof derive.from === "string") {
      if (fetchableSet.has(derive.from)) selected.add(derive.from);
      else unknown.add(derive.from);
      continue;
    }
    unknown.add(selector);
  }

  if (unknown.size > 0) {
    throw new Error(
      `unknown resource type(s)/product(s): ${sortedStrings(unknown).join(", ")}\n`
      + `valid products: ${products.join(", ")}\n`
      + `valid resources: ${fetchable.join(", ")}`,
    );
  }
  return sortedStrings(selected);
}
