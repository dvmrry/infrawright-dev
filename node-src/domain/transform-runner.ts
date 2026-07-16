import path from "node:path";

import type { LoadedPackRoot, LoadedResourceMetadata } from "../metadata/loader.js";
import { isObject, type JsonObject } from "../metadata/validation.js";
import {
  terraformBlockForSchema,
  terraformResourceInputAttributes,
} from "../metadata/terraform-schema.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";
import { readOptionalUtf8 } from "../io/files.js";
import type { Deployment } from "./types.js";
import { deploymentReferenceBindingMode } from "./deployment.js";
import { pythonHtmlUnescapeGeneric } from "./python-html-unescape.js";
import { loadedRootTopology, validateTenant } from "./roots.js";
import {
  deriveReorderItems,
  transformLoadedItems,
  type PullTransformResult,
} from "./pull-transform.js";
import {
  mergedTransformLookupSources,
  mergedTransformReferences,
  selectTransformResources,
  transformSourceType,
} from "./transform-selection.js";
import {
  writeDerivedTransformArtifact,
  writeTransformArtifacts,
  type BindingContext,
  type TransformReferenceSpec,
} from "./transform-artifacts.js";

export interface TransformBatchResult {
  readonly dropCheckFailed?: readonly string[];
  readonly failed: readonly string[];
  readonly processed: readonly string[];
  readonly skipped: readonly string[];
}

export function transformReferenceSpecs(
  root: LoadedPackRoot,
  resource: LoadedResourceMetadata,
): Readonly<Record<string, TransformReferenceSpec>> {
  const resourceReferences = mergedTransformReferences(root)[resource.type];
  if (!isObject(resourceReferences)) return {};
  const output: Record<string, TransformReferenceSpec> = Object.create(null) as Record<
    string,
    TransformReferenceSpec
  >;
  for (const [field, raw] of Object.entries(resourceReferences)) {
    if (
      isObject(raw)
      && typeof raw.referent === "string"
      && typeof raw.name_field === "string"
    ) {
      output[field] = { referent: raw.referent, name_field: raw.name_field };
    }
  }
  return output;
}

export function transformLookupNameField(
  root: LoadedPackRoot,
  resource: LoadedResourceMetadata,
  deployment: Deployment,
): string | null {
  const resourceSource = mergedTransformLookupSources(root)[resource.type];
  if (isObject(resourceSource) && typeof resourceSource.name_field === "string") {
    return resourceSource.name_field;
  }

  const inferred = new Map<string, string[]>();
  for (const reference of inboundLookupReferences(root, resource)) {
    if (deploymentReferenceBindingMode(deployment, reference.provider) === "disabled") continue;
    const sources = inferred.get(reference.nameField) ?? [];
    sources.push(reference.source);
    inferred.set(reference.nameField, sources);
  }
  if (inferred.size === 0) return null;
  if (inferred.size > 1) {
    const conflicts = sortedStrings(inferred.keys()).map((field) => {
      return `${JSON.stringify(field)} from ${sortedStrings(inferred.get(field) ?? []).join(", ")}`;
    });
    throw new TypeError(
      `${resource.type} has conflicting inferred reference lookup name fields: ${conflicts.join("; ")}; declare an explicit lookup_sources entry`,
    );
  }
  return inferred.keys().next().value ?? null;
}

interface InboundLookupReference {
  readonly nameField: string;
  readonly provider: string;
  readonly source: string;
}

function inboundLookupReferences(
  root: LoadedPackRoot,
  resource: LoadedResourceMetadata,
): readonly InboundLookupReference[] {
  const output: InboundLookupReference[] = [];
  const references = mergedTransformReferences(root);
  for (const referrer of sortedStrings(Object.keys(references))) {
    const referrerResource = root.resources.get(referrer);
    if (
      referrerResource === undefined
      || referrer === resource.type
      || referrerResource.registry.generate !== true
      || isObject(referrerResource.registry.derive)
    ) {
      continue;
    }
    const fields = references[referrer];
    if (fields === undefined) continue;
    for (const field of sortedStrings(Object.keys(fields))) {
      const spec = fields[field];
      if (
        !isObject(spec)
        || spec.referent !== resource.type
        || typeof spec.name_field !== "string"
      ) {
        continue;
      }
      output.push({
        nameField: spec.name_field,
        provider: referrerResource.provider,
        source: `${referrer}.${field}`,
      });
    }
  }
  return output;
}

/** Whether reference metadata owns removal of this resource's inferred lookup sidecar. */
export function transformHasInferredLookupLifecycle(
  root: LoadedPackRoot,
  resource: LoadedResourceMetadata,
): boolean {
  return inboundLookupReferences(root, resource).length > 0;
}

function shouldUnescape(root: LoadedPackRoot, resourceType: string): boolean {
  const active = new Set(root.active.packs);
  return root.packs.manifests.some((manifest) => {
    if (!active.has(manifest.name) || !Array.isArray(manifest.data.unescape_products)) {
      return false;
    }
    return manifest.data.unescape_products.some((prefix) => {
      return typeof prefix === "string" && resourceType.startsWith(prefix);
    });
  });
}

/** Run the production transform semantics for one already-loaded resource. */
export async function transformResourceItems(options: {
  readonly rawItems: readonly unknown[];
  readonly resource: LoadedResourceMetadata;
  readonly root: LoadedPackRoot;
  readonly schema?: Readonly<JsonObject>;
}): Promise<PullTransformResult> {
  return transformLoadedItems({
    resource: options.resource,
    schema: options.schema ?? await options.root.loadResourceSchema(options.resource.type),
    rawItems: options.rawItems,
    htmlUnescape: pythonHtmlUnescapeGeneric,
    unescapeHtml: shouldUnescape(options.root, options.resource.type),
  });
}

async function knownHoldPaths(
  root: LoadedPackRoot,
  resourceType: string,
): Promise<ReadonlySet<string>> {
  const output = new Set<string>();
  for (const component of root.active.shared) {
    const source = path.join(root.root, "_shared", component, "adoption_status.json");
    const text = await readOptionalUtf8(source, `${component} adoption status`);
    if (text === null) continue;
    const data = parseDataJsonLosslessly(text);
    if (!isObject(data) || !isObject(data.known_holds)) continue;
    const holds = data.known_holds[resourceType];
    if (!Array.isArray(holds)) continue;
    for (const hold of holds) {
      if (isObject(hold) && typeof hold.path === "string") output.add(hold.path);
    }
  }
  return output;
}

export function transformBindingContext(options: {
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly resource: LoadedResourceMetadata;
  readonly resourceRoots: Readonly<Record<string, string>>;
  readonly references: Readonly<Record<string, TransformReferenceSpec>>;
}): BindingContext {
  const generated = new Set<string>();
  const derived = new Set<string>();
  for (const resource of options.root.resources.values()) {
    if (resource.registry.generate === true) generated.add(resource.type);
    if (resource.registry.generate === true && isObject(resource.registry.derive)) {
      derived.add(resource.type);
    }
  }
  return {
    mode: deploymentReferenceBindingMode(options.deployment, options.resource.provider),
    generated,
    derived,
    resourceRoots: options.resourceRoots,
    references: options.references,
  };
}

function warnIfSlim(options: {
  readonly rawItems: readonly unknown[];
  readonly resourceType: string;
  readonly schema: Readonly<JsonObject>;
  readonly write: (message: string) => void;
}): void {
  if (options.rawItems.length === 0) return;
  const block = terraformBlockForSchema(options.schema as JsonObject, options.resourceType);
  const classified = terraformResourceInputAttributes(block, options.resourceType);
  const expected = classified.required.length + classified.optional.length;
  if (expected === 0 || !options.rawItems.every(isObject)) return;
  const average = options.rawItems.reduce((total, item) => {
    return total + Object.keys(item as JsonObject).length;
  }, 0) / options.rawItems.length;
  if (average < expected / 3) {
    options.write(
      `WARNING: ${options.resourceType} input looks slim (avg ${average.toFixed(1)} keys vs ${expected} schema inputs); did the fetcher use the list endpoint instead of detail?`,
    );
  }
}

function reportedDrops(options: {
  readonly drops: readonly string[];
  readonly held: ReadonlySet<string>;
  readonly override: Readonly<JsonObject>;
  readonly resourceType: string;
  readonly write: (message: string) => void;
}): readonly string[] {
  const held = sortedStrings(options.drops.filter((item) => options.held.has(item)));
  const unexpected = sortedStrings(options.drops.filter((item) => !options.held.has(item)));
  for (const field of held) options.write(`known-held ${options.resourceType}.${field}`);
  for (const field of unexpected) options.write(`dropped ${options.resourceType}.${field}`);
  if (unexpected.length > 0) {
    options.write(
      `${unexpected.length} unacknowledged dropped field(s) above — NEW API surface for ${options.resourceType}. Confirm each against the provider read/expand, then add the safe ones to acknowledged_drops in packs/<provider>/overrides/${options.resourceType}.json (a dropped field can be write-REQUIRED under another schema name — the signingCertId class — so verify before acknowledging). DROPS_CHECK=1 makes this exit 4.`,
    );
    const existing = Array.isArray(options.override.acknowledged_drops)
      ? options.override.acknowledged_drops.filter((item): item is string => typeof item === "string")
      : [];
    const snippet = renderPythonLosslessArtifactJson({
      acknowledged_drops: sortedStrings(new Set([...existing, ...unexpected])),
    }).trimEnd();
    options.write(
      `Exact paths from this run (merge into packs/<provider>/overrides/${options.resourceType}.json only after verification):\n${snippet}`,
    );
  }
  return unexpected;
}

/** Execute the real batch transform target without invoking Python. */
export async function runTransformBatch(options: {
  readonly beforeArtifactWrite?: (resourceType: string) => Promise<void>;
  readonly deployment: Deployment;
  readonly environment?: NodeJS.ProcessEnv;
  readonly inputDirectory: string;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly tenant: string;
}): Promise<TransformBatchResult> {
  validateTenant(options.tenant);
  const write = options.onDiagnostic ?? (() => undefined);
  const selection = selectTransformResources({ root: options.root, selectors: options.selectors });
  for (const note of selection.notes) write(note.trimEnd());
  const topology = loadedRootTopology({
    root: options.root,
    deployment: options.deployment,
    tenant: options.tenant,
    selectors: [],
  }).topology;
  const processed: string[] = [];
  const skipped: string[] = [];
  const failed: string[] = [];
  const dropCheckFailed: string[] = [];
  for (const resourceType of selection.resourceTypes) {
    const sourceType = transformSourceType(options.root, resourceType);
    const sourcePath = path.join(options.inputDirectory, `${sourceType}.json`);
    const text = await readOptionalUtf8(sourcePath, `${resourceType} transform input`);
    if (text === null) {
      skipped.push(resourceType);
      write(`skip ${resourceType} (no ${sourcePath})`);
      continue;
    }
    try {
      const raw = parseDataJsonLosslessly(text);
      if (!Array.isArray(raw)) {
        throw new TypeError(
          `${sourcePath} must be a JSON LIST of items — re-run make fetch TENANT=${options.tenant} RESOURCE=${resourceType}; if it persists the fetcher wrote an envelope instead of the item list`,
        );
      }
      const resource = options.root.resources.get(resourceType);
      if (resource === undefined) throw new TypeError(`unknown resource ${resourceType}`);
      const references = transformReferenceSpecs(options.root, resource);
      const rootLabel = topology.resource_roots[resourceType] ?? resourceType;
      const variableName = rootLabel === resourceType ? "items" : `${resourceType}_items`;
      const derive = resource.registry.derive;
      if (isObject(derive)) {
        await options.beforeArtifactWrite?.(resourceType);
        await writeDerivedTransformArtifact({
          deployment: options.deployment,
          items: deriveReorderItems(raw, derive),
          onDiagnostic: write,
          references,
          resourceType,
          sourceType,
          tenant: options.tenant,
          variableName,
        });
        processed.push(resourceType);
        continue;
      }
      const schema = await options.root.loadResourceSchema(resourceType);
      warnIfSlim({ rawItems: raw, resourceType, schema, write });
      const result = await transformResourceItems({
        rawItems: raw,
        resource,
        root: options.root,
        schema,
      });
      await options.beforeArtifactWrite?.(resourceType);
      await writeTransformArtifacts({
        bindingContext: transformBindingContext({
          deployment: options.deployment,
          root: options.root,
          resource,
          resourceRoots: topology.resource_roots,
          references,
        }),
        deployment: options.deployment,
        lookupNameField: transformLookupNameField(options.root, resource, options.deployment),
        removeLookupWhenAbsent: transformHasInferredLookupLifecycle(options.root, resource),
        onDiagnostic: write,
        override: resource.override ?? {},
        references,
        resourceType,
        result,
        tenant: options.tenant,
        variableName,
      });
      const unexpected = reportedDrops({
        drops: result.drops,
        held: await knownHoldPaths(options.root, resourceType),
        override: resource.override ?? {},
        resourceType,
        write,
      });
      if (unexpected.length > 0 && options.environment?.DROPS_CHECK) {
        failed.push(resourceType);
        dropCheckFailed.push(resourceType);
      } else {
        processed.push(resourceType);
      }
    } catch (error: unknown) {
      failed.push(resourceType);
      write(`error: ${resourceType}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  if (failed.length > 0) write(`\ntransform FAILED for: ${failed.join(" ")}`);
  return {
    ...(dropCheckFailed.length === 0 ? {} : { dropCheckFailed }),
    failed,
    processed,
    skipped,
  };
}
