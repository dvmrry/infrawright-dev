import { lstat, readFile } from "node:fs/promises";
import path from "node:path";
import { LosslessNumber } from "lossless-json";

import type { LoadedPackRoot, LoadedResourceMetadata } from "../metadata/loader.js";
import { isObject } from "../metadata/validation.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import { readOptionalUtf8 } from "../io/files.js";
import type { Deployment } from "./types.js";
import {
  adoptionMetadata,
  deriveAdoptionIdentities,
} from "./adoption-meta.js";
import { DriftPolicy } from "./drift-policy.js";
import {
  createOracleCommandRunner,
  importProviderState,
  oracleTimeoutMs,
  type OracleStateObject,
} from "./import-oracle.js";
import { loadedRootTopology, validateTenant } from "./roots.js";
import { projectProviderState } from "./state-project.js";
import {
  transformArtifactPaths,
  writeTransformArtifacts,
  type TransformReferenceSpec,
} from "./transform-artifacts.js";
import {
  runTransformBatch,
  transformBindingContext,
  transformLookupNameField,
  transformReferenceSpecs,
} from "./transform-runner.js";
import { selectTransformResources, transformSourceType } from "./transform-selection.js";
import type { PullTransformResult } from "./pull-transform.js";

export interface AdoptBatchResult {
  readonly failed: readonly string[];
  readonly processed: readonly string[];
  readonly skipped: readonly string[];
}

export type AdoptionStateLoader = (options: {
  readonly keyToImportId: ReadonlyMap<string, string>;
  readonly policy: DriftPolicy;
  readonly rawItems: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resourceType: string;
}) => Promise<ReadonlyMap<string, OracleStateObject>>;

function cloneJson(value: unknown): unknown {
  if (value instanceof LosslessNumber) return new LosslessNumber(value.toString());
  if (Array.isArray(value)) return value.map(cloneJson);
  if (isObject(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, cloneJson(item)]));
  }
  return value;
}

function emptyPolicy(): Record<string, unknown> {
  return { version: 1, resource_types: {} };
}

function mergePolicyData(base: unknown, override: unknown): unknown {
  const output = isObject(cloneJson(base)) ? cloneJson(base) as Record<string, unknown> : emptyPolicy();
  if (!isObject(override)) return override;
  if (output.version !== (override.version ?? 1)) return override;
  output.version = override.version ?? output.version ?? 1;
  const resources = isObject(output.resource_types)
    ? output.resource_types
    : (output.resource_types = {} as Record<string, unknown>);
  const incoming = isObject(override.resource_types) ? override.resource_types : {};
  for (const [resourceType, rawConfig] of Object.entries(incoming)) {
    if (!isObject(rawConfig)) {
      resources[resourceType] = rawConfig;
      continue;
    }
    const target = isObject(resources[resourceType])
      ? resources[resourceType] as Record<string, unknown>
      : (resources[resourceType] = {} as Record<string, unknown>) as Record<string, unknown>;
    for (const [mode, rawEntries] of Object.entries(rawConfig)) {
      const existing = Array.isArray(target[mode]) ? target[mode] as unknown[] : [];
      target[mode] = [...existing, ...(
        Array.isArray(rawEntries) ? rawEntries.map(cloneJson) : [cloneJson(rawEntries)]
      )];
    }
  }
  return output;
}

function packPolicyData(root: LoadedPackRoot): unknown {
  let output: unknown = emptyPolicy();
  const active = new Set(root.active.packs);
  for (const manifest of root.packs.manifests) {
    if (!active.has(manifest.name) || !Object.hasOwn(manifest.data, "drift_policy")) continue;
    output = mergePolicyData(output, manifest.data.drift_policy);
  }
  return output;
}

export async function loadAdoptionPolicy(options: {
  readonly path?: string;
  readonly root: LoadedPackRoot;
}): Promise<DriftPolicy> {
  const base = packPolicyData(options.root);
  if (options.path === undefined) return new DriftPolicy(base, "pack drift policy");
  const text = await readFile(options.path, "utf8");
  const user = parseDataJsonLosslessly(text);
  new DriftPolicy(user, options.path);
  return new DriftPolicy(
    mergePolicyData(base, user),
    `${options.path} merged with pack drift policy`,
  );
}

async function pendingMoves(options: {
  readonly deployment: Deployment;
  readonly resourceType: string;
  readonly tenant: string;
}): Promise<string | null> {
  const imports = transformArtifactPaths(options).imports;
  const pending = imports.endsWith("_imports.tf")
    ? `${imports.slice(0, -"_imports.tf".length)}_moves.pending.json`
    : `${imports}.moves.pending.json`;
  try {
    await lstat(pending);
    return pending;
  } catch (error: unknown) {
    if (typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT") {
      return null;
    }
    throw error;
  }
}

async function assertNoPendingMoves(options: {
  readonly deployment: Deployment;
  readonly resourceType: string;
  readonly tenant: string;
}): Promise<void> {
  if (await pendingMoves(options) !== null) {
    throw new Error(
      `pending move transition for ${options.resourceType} must be applied and acknowledged before transform or adopt can run`,
    );
  }
}

/** Derive identity, run provider state, and project one resource without writing. */
export async function adoptResourceItems(options: {
  readonly policy: DriftPolicy;
  readonly rawItems: readonly unknown[];
  readonly resource: LoadedResourceMetadata;
  readonly root: LoadedPackRoot;
  readonly stateLoader: AdoptionStateLoader;
  readonly write?: (message: string) => void;
}): Promise<PullTransformResult> {
  const derived = deriveAdoptionIdentities({ rawItems: options.rawItems, resource: options.resource });
  for (const skipped of derived.skipped) {
    options.write?.(
      `skipped ${options.resource.type} item ${JSON.stringify(skipped.item.name ?? skipped.item.id)} (identity ${skipped.reason} matched)`,
    );
  }
  const keyToImportId = new Map(derived.identities.map((item) => [item.key, item.importId]));
  const keyToRaw = new Map(derived.identities.map((item) => [item.key, item.raw]));
  const identityByKey = new Map(derived.identities.map((item) => [item.key, item.item]));
  const originals: Record<string, Readonly<Record<string, unknown>>> = Object.create(null) as Record<
    string,
    Readonly<Record<string, unknown>>
  >;
  if (keyToImportId.size === 0) return { drops: [], items: {}, originals };
  const state = await options.stateLoader({
    keyToImportId,
    policy: options.policy,
    rawItems: keyToRaw,
    resourceType: options.resource.type,
  });
  const items: Record<string, Readonly<Record<string, unknown>>> = Object.create(null) as Record<
    string,
    Readonly<Record<string, unknown>>
  >;
  const missing = [...keyToImportId.keys()].filter((key) => !state.has(key)).sort();
  const unexpected = [...state.keys()].filter((key) => !keyToImportId.has(key)).sort();
  if (missing.length > 0 || unexpected.length > 0) {
    throw new TypeError(
      `${options.resource.type} adoption Oracle keys did not match requested identities (missing=${missing.join(", ") || "<none>"} unexpected=${unexpected.join(", ") || "<none>"})`,
    );
  }
  for (const key of [...state.keys()].sort()) {
    const observed = state.get(key);
    if (observed === undefined) continue;
    const rawItem = keyToRaw.get(key);
    const identity = identityByKey.get(key);
    if (identity === undefined) continue;
    originals[key] = identity;
    items[key] = await projectProviderState({
      policy: options.policy,
      resourceType: options.resource.type,
      root: options.root,
      sensitiveValues: observed.sensitiveValues,
      stateValues: observed.values,
      ...(rawItem === undefined ? {} : { rawItem }),
    });
  }
  return { drops: [], items, originals };
}

function variableNameFor(
  resourceType: string,
  resourceRoots: Readonly<Record<string, string>>,
): string {
  const root = resourceRoots[resourceType] ?? resourceType;
  return root === resourceType ? "items" : `${resourceType}_items`;
}

/** Execute the real generic adoption batch target without invoking Python. */
export async function runAdoptBatch(options: {
  readonly deployment: Deployment;
  readonly inputDirectory: string;
  readonly onDiagnostic?: (message: string) => void;
  readonly policy: DriftPolicy;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly stateLoader: AdoptionStateLoader;
  readonly tenant: string;
}): Promise<AdoptBatchResult> {
  validateTenant(options.tenant);
  const write = options.onDiagnostic ?? (() => undefined);
  const selection = selectTransformResources({ root: options.root, selectors: options.selectors });
  for (const note of selection.notes) write(note.trimEnd());
  const topology = loadedRootTopology({
    deployment: options.deployment,
    root: options.root,
    selectors: selection.resourceTypes,
    tenant: options.tenant,
  }).topology;
  const processed: string[] = [];
  const skipped: string[] = [];
  const failed: string[] = [];
  for (const resourceType of selection.resourceTypes) {
    try {
      const sourceType = transformSourceType(options.root, resourceType);
      const source = path.join(options.inputDirectory, `${sourceType}.json`);
      const text = await readOptionalUtf8(source, `${resourceType} adoption input`);
      if (text === null) {
        skipped.push(resourceType);
        write(`skip ${resourceType} (no ${source})`);
        continue;
      }
      const rawItems = parseDataJsonLosslessly(text);
      if (!Array.isArray(rawItems)) throw new TypeError(`${source} must be a JSON LIST of items`);
      const resource = options.root.resources.get(resourceType);
      if (resource === undefined) throw new TypeError(`unknown resource ${resourceType}`);
      await assertNoPendingMoves({ deployment: options.deployment, resourceType, tenant: options.tenant });
      if (isObject(resource.registry.derive)) {
        const delegated = await runTransformBatch({
          beforeArtifactWrite: async (selectedResourceType) => assertNoPendingMoves({
            deployment: options.deployment,
            resourceType: selectedResourceType,
            tenant: options.tenant,
          }),
          deployment: options.deployment,
          inputDirectory: options.inputDirectory,
          onDiagnostic: write,
          root: options.root,
          selectors: [resourceType],
          tenant: options.tenant,
        });
        if (delegated.failed.length > 0) failed.push(resourceType);
        else processed.push(resourceType);
        continue;
      }
      const result = await adoptResourceItems({
        policy: options.policy,
        rawItems,
        resource,
        root: options.root,
        stateLoader: options.stateLoader,
        write,
      });
      await assertNoPendingMoves({ deployment: options.deployment, resourceType, tenant: options.tenant });
      const references: Readonly<Record<string, TransformReferenceSpec>> = transformReferenceSpecs(options.root, resource);
      await writeTransformArtifacts({
        bindingContext: transformBindingContext({
          deployment: options.deployment,
          references,
          resource,
          resourceRoots: topology.resource_roots,
          root: options.root,
        }),
        deployment: options.deployment,
        lookupNameField: transformLookupNameField(options.root, resource),
        onDiagnostic: write,
        override: { import_id: adoptionMetadata(resource).importId },
        references,
        resourceType,
        result,
        tenant: options.tenant,
        variableName: variableNameFor(resourceType, topology.resource_roots),
      });
      processed.push(resourceType);
    } catch (error: unknown) {
      failed.push(resourceType);
      write(`error: ${resourceType}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  if (failed.length > 0) write(`\nadopt FAILED for: ${failed.join(" ")}`);
  return { failed, processed, skipped };
}

export async function defaultAdoptionStateLoader(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly onDiagnostic?: (message: string) => void;
  readonly root: LoadedPackRoot;
  readonly terraformExecutable: string;
}): Promise<AdoptionStateLoader> {
  const timeoutMs = oracleTimeoutMs(options.environment);
  const runner = createOracleCommandRunner({
    limits: {
      maxStderrBytes: 1024 * 1024,
      maxStdoutBytes: 8 * 1024 * 1024,
      timeoutMs,
    },
    terraformExecutable: options.terraformExecutable,
  });
  return async (request) => importProviderState({
    environment: options.environment,
    keyToImportId: request.keyToImportId,
    policy: request.policy,
    rawItems: request.rawItems,
    resourceType: request.resourceType,
    root: options.root,
    runner,
    ...(options.onDiagnostic === undefined ? {} : { onDiagnostic: options.onDiagnostic }),
  });
}
