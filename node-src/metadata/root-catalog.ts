import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";

import type {
  RootCatalog,
  RootCatalogResource,
} from "../domain/types.js";
import {
  renderPythonCompatibleJson,
  sortedStrings,
  type JsonValue,
} from "../json/python-compatible.js";
import type { LoadedPackRoot } from "./loader.js";
import { fail } from "./validation.js";

function selectedProviders(
  root: LoadedPackRoot,
  requested: readonly string[] | undefined,
): string[] {
  const declared = sortedStrings(new Set(Object.values(root.packs.providerPrefixes)));
  const selected = requested === undefined || requested.length === 0
    ? declared
    : sortedStrings(new Set(requested));
  const declaredSet = new Set(declared);
  const unknown = selected.filter((provider) => !declaredSet.has(provider));
  if (unknown.length > 0) {
    fail(`unknown provider(s): ${unknown.join(", ")}`);
  }
  return selected;
}

function matchingPrefix(
  root: LoadedPackRoot,
  resourceType: string,
  provider: string,
): string | null {
  const prefixes = Object.entries(root.packs.providerPrefixes)
    .filter(([prefix, owner]) => {
      return owner === provider && resourceType.startsWith(prefix);
    })
    .sort(([left], [right]) => right.length - left.length);
  return prefixes[0]?.[0] ?? null;
}

function resourceShape(
  root: LoadedPackRoot,
  resourceType: string,
): RootCatalogResource {
  const loaded = root.resources.get(resourceType);
  if (loaded === undefined) fail(`unknown active resource type ${resourceType}`);
  const prefix = matchingPrefix(root, resourceType, loaded.provider);
  const bareName = prefix === null ? resourceType : resourceType.slice(prefix.length);
  const slugPart = prefix === null ? null : bareName.split("_", 1)[0] ?? "";
  const base = {
    bare_name: bareName,
    derived: Boolean(loaded.registry.derive),
    generated: Boolean(loaded.registry.generate),
    product: loaded.product,
    provider: loaded.provider,
  };
  const suffix = {
    slug_label: prefix === null || slugPart === null ? null : `${prefix}${slugPart}`,
    type: resourceType,
  };
  return Object.hasOwn(loaded.registry, "slug_group")
    ? { ...base, slug_group: loaded.registry.slug_group === true, ...suffix }
    : { ...base, ...suffix };
}

function portableRelative(root: string, file: string): string {
  return path.relative(root, file).split(path.sep).join("/");
}

async function sourceEvidence(
  root: LoadedPackRoot,
  providers: ReadonlySet<string>,
): Promise<{
  readonly files: string[];
  readonly sha256: string;
}> {
  const selectedPaths: string[] = [];
  for (const manifest of root.packs.manifests) {
    const owned = Object.values(manifest.providerPrefixes);
    if (!owned.some((provider) => providers.has(provider))) continue;
    selectedPaths.push(manifest.path);
    const registryPath = path.join(manifest.directory, "registry.json");
    try {
      await readFile(registryPath);
      selectedPaths.push(registryPath);
    } catch (error: unknown) {
      if (
        typeof error !== "object"
        || error === null
        || !("code" in error)
        || error.code !== "ENOENT"
      ) {
        throw error;
      }
    }
  }
  selectedPaths.sort((left, right) => {
    const leftRelative = portableRelative(root.root, left);
    const rightRelative = portableRelative(root.root, right);
    return leftRelative < rightRelative ? -1 : leftRelative > rightRelative ? 1 : 0;
  });
  const digest = createHash("sha256");
  const files: string[] = [];
  for (const file of selectedPaths) {
    const relative = portableRelative(root.root, file);
    const content = await readFile(file);
    files.push(relative);
    digest.update(relative, "utf8");
    digest.update(Buffer.of(0));
    digest.update(content);
    digest.update(Buffer.of(0));
  }
  return { files, sha256: digest.digest("hex") };
}

/** Derive the versioned compatibility catalog from validated pack metadata. */
export async function buildRootCatalog(
  root: LoadedPackRoot,
  requestedProviders?: readonly string[],
): Promise<RootCatalog> {
  const providers = selectedProviders(root, requestedProviders);
  const providerSet = new Set(providers);
  const resources = sortedStrings(root.resources.keys())
    .filter((resourceType) => {
      return providerSet.has(root.resources.get(resourceType)?.provider ?? "");
    })
    .map((resourceType) => resourceShape(root, resourceType));
  const evidence = await sourceEvidence(root, providerSet);
  return {
    declared_providers: providers,
    kind: "infrawright.root_catalog",
    resources,
    schema_version: 1,
    source_files: evidence.files,
    sources_sha256: evidence.sha256,
  };
}

export async function renderRootCatalog(
  root: LoadedPackRoot,
  requestedProviders?: readonly string[],
): Promise<string> {
  return renderPythonCompatibleJson(
    await buildRootCatalog(root, requestedProviders) as unknown as JsonValue,
  );
}
