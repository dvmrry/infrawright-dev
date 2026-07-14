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
  classifyAdoptionRawItems,
  deriveAdoptionIdentities,
  type AdoptionRawClassification,
} from "./adoption-meta.js";
import { DriftPolicy } from "./drift-policy.js";
import {
  createOracleCommandRunner,
  importProviderState,
  importProviderStates,
  oracleBatchResourceFamily,
  oracleTimeoutMs,
  type OracleBatchResourceRequest,
  type OracleBatchState,
  type OracleStateObject,
} from "./import-oracle.js";
import { loadedRootTopology, validateTenant } from "./roots.js";
import { projectProviderState } from "./state-project.js";
import {
  compileTransformArtifactBatch,
  publishCompiledTransformArtifactBatch,
  transformArtifactPaths,
  writeTransformArtifacts,
  type TransformArtifactCompileOptions,
  type TransformReferenceSpec,
} from "./transform-artifacts.js";
import {
  runTransformBatch,
  transformBindingContext,
  transformLookupNameField,
  transformReferenceSpecs,
} from "./transform-runner.js";
import {
  referenceOrder,
  selectTransformResources,
  transformSourceType,
} from "./transform-selection.js";
import type { PullTransformResult } from "./pull-transform.js";
import type { PerformanceRecorder, PerformanceStatus } from "../performance/recorder.js";

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

export type AdoptionBatchStateLoader = (options: {
  readonly resources: readonly OracleBatchResourceRequest[];
}) => Promise<OracleBatchState>;

export type OracleBatchMode = "logical-root" | "per-resource-type";

export function oracleBatchMode(environment: NodeJS.ProcessEnv): OracleBatchMode {
  const raw = environment.INFRAWRIGHT_ORACLE_BATCH_MODE?.trim();
  if (raw === undefined || raw.length === 0 || raw === "per-resource-type") {
    return "per-resource-type";
  }
  if (raw === "logical-root") return "logical-root";
  throw new TypeError(
    "INFRAWRIGHT_ORACLE_BATCH_MODE must be per-resource-type or logical-root",
  );
}

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

interface PreparedAdoptionItems {
  readonly counts: AdoptionItemCounts;
  readonly identityByKey: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly keyToImportId: ReadonlyMap<string, string>;
  readonly keyToRaw: ReadonlyMap<string, Readonly<Record<string, unknown>>>;
  readonly resource: LoadedResourceMetadata;
}

interface AdoptionItemCounts {
  readonly eligible: number;
  readonly fetched: number;
  readonly systemSkipped: number;
  readonly unsupported: number;
}

interface UnsupportedAdoptionItems {
  readonly classification: AdoptionRawClassification;
  readonly counts: AdoptionItemCounts;
  readonly resource: LoadedResourceMetadata;
}

type AdoptionPreflight =
  | { readonly status: "ready"; readonly prepared: PreparedAdoptionItems }
  | { readonly status: "unsupported"; readonly blocked: UnsupportedAdoptionItems };

function itemLabel(item: Readonly<Record<string, unknown>>): string {
  const value = item.name ?? item.id ?? "<unknown>";
  return JSON.stringify(value);
}

function itemCounts(
  fetched: number,
  classification: AdoptionRawClassification,
): AdoptionItemCounts {
  return {
    eligible: classification.eligible.length,
    fetched,
    systemSkipped: classification.skipped.length,
    unsupported: classification.unsupported.length,
  };
}

function writeUnsupportedDiagnostics(options: {
  readonly classification: AdoptionRawClassification;
  readonly resource: LoadedResourceMetadata;
  readonly write?: (message: string) => void;
}): void {
  if (options.write === undefined) return;
  for (const unsupported of options.classification.unsupported) {
    options.write(
      `unsupported ${options.resource.type} item ${itemLabel(unsupported.item)} (matched static unsupported rule)`,
    );
  }
  const matchedRules = new Set(
    options.classification.unsupported.map((unsupported) => unsupported.rule),
  );
  for (const rule of matchedRules) {
    options.write(
      `unsupported ${options.resource.type} rule for ${rule.provider.source} ${rule.provider.version}: ${rule.reason}; evidence=${JSON.stringify(rule.evidence)}`,
    );
  }
}

function writeTerminalCounts(options: {
  readonly counts: AdoptionItemCounts;
  readonly published: number;
  readonly resourceType: string;
  readonly write: (message: string) => void;
}): void {
  const failed = Math.max(0, options.counts.eligible - options.published);
  options.write(
    `adopt counts ${options.resourceType}: fetched=${options.counts.fetched} system_skipped=${options.counts.systemSkipped} unsupported=${options.counts.unsupported} eligible=${options.counts.eligible} published=${options.published} failed=${failed}`,
  );
}

function prepareAdoptionItems(options: {
  readonly onClassified?: (counts: AdoptionItemCounts) => void;
  readonly rawItems: readonly unknown[];
  readonly resource: LoadedResourceMetadata;
  readonly write?: (message: string) => void;
}): AdoptionPreflight {
  const classification = classifyAdoptionRawItems(options);
  const counts = itemCounts(options.rawItems.length, classification);
  options.onClassified?.(counts);
  for (const skipped of classification.skipped) {
    options.write?.(
      `skipped ${options.resource.type} item ${JSON.stringify(skipped.item.name ?? skipped.item.id)} (identity ${skipped.reason} matched)`,
    );
  }
  writeUnsupportedDiagnostics({
    classification,
    resource: options.resource,
    ...(options.write === undefined ? {} : { write: options.write }),
  });
  if (classification.unsupported.length > 0) {
    return {
      blocked: { classification, counts, resource: options.resource },
      status: "unsupported",
    };
  }
  const derived = deriveAdoptionIdentities({
    rawItems: classification.eligible,
    resource: options.resource,
  });
  return { status: "ready", prepared: {
    counts,
    identityByKey: new Map(derived.identities.map((item) => [item.key, item.item])),
    keyToImportId: new Map(derived.identities.map((item) => [item.key, item.importId])),
    keyToRaw: new Map(derived.identities.map((item) => [item.key, item.raw])),
    resource: options.resource,
  } };
}

async function projectAdoptionItems(options: {
  readonly performance?: PerformanceRecorder;
  readonly policy: DriftPolicy;
  readonly prepared: PreparedAdoptionItems;
  readonly root: LoadedPackRoot;
  readonly state: ReadonlyMap<string, OracleStateObject>;
}): Promise<PullTransformResult> {
  const { identityByKey, keyToImportId, keyToRaw, resource } = options.prepared;
  const originals: Record<string, Readonly<Record<string, unknown>>> = Object.create(null) as Record<
    string,
    Readonly<Record<string, unknown>>
  >;
  if (keyToImportId.size === 0) return { drops: [], items: {}, originals };
  const items: Record<string, Readonly<Record<string, unknown>>> = Object.create(null) as Record<
    string,
    Readonly<Record<string, unknown>>
  >;
  const missing = [...keyToImportId.keys()].filter((key) => !options.state.has(key)).sort();
  const unexpected = [...options.state.keys()].filter((key) => !keyToImportId.has(key)).sort();
  if (missing.length > 0 || unexpected.length > 0) {
    throw new TypeError(
      `${resource.type} adoption Oracle keys did not match requested identities (missing=${missing.join(", ") || "<none>"} unexpected=${unexpected.join(", ") || "<none>"})`,
    );
  }
  const projectionStarted = options.performance?.now() ?? 0;
  let projectionStatus: "failed" | "success" = "success";
  try {
    for (const key of [...options.state.keys()].sort()) {
      const observed = options.state.get(key);
      if (observed === undefined) continue;
      const rawItem = keyToRaw.get(key);
      const identity = identityByKey.get(key);
      if (identity === undefined) continue;
      originals[key] = identity;
      items[key] = await projectProviderState({
        policy: options.policy,
        resourceType: resource.type,
        root: options.root,
        sensitiveValues: observed.sensitiveValues,
        stateValues: observed.values,
        ...(rawItem === undefined ? {} : { rawItem }),
      });
    }
  } catch (error: unknown) {
    projectionStatus = "failed";
    throw error;
  } finally {
    options.performance?.recordSpan({
      durationMs: options.performance.durationSince(projectionStarted),
      instances: options.state.size,
      phase: "adopt.provider_state_projection",
      resourceFamily: resource.type,
      status: projectionStatus,
    });
  }
  return { drops: [], items, originals };
}

/** Derive identity, run provider state, and project one resource without writing. */
export async function adoptResourceItems(options: {
  readonly performance?: PerformanceRecorder;
  readonly policy: DriftPolicy;
  readonly rawItems: readonly unknown[];
  readonly resource: LoadedResourceMetadata;
  readonly root: LoadedPackRoot;
  readonly stateLoader: AdoptionStateLoader;
  readonly write?: (message: string) => void;
}): Promise<PullTransformResult> {
  const preflight = prepareAdoptionItems(options);
  if (preflight.status === "unsupported") {
    throw new TypeError(
      `${options.resource.type} contains ${preflight.blocked.counts.unsupported} unsupported item(s); no Oracle command or artifact publication is permitted`,
    );
  }
  return adoptPreparedResourceItems({
    ...(options.performance === undefined ? {} : { performance: options.performance }),
    policy: options.policy,
    prepared: preflight.prepared,
    root: options.root,
    stateLoader: options.stateLoader,
  });
}

async function adoptPreparedResourceItems(options: {
  readonly performance?: PerformanceRecorder;
  readonly policy: DriftPolicy;
  readonly prepared: PreparedAdoptionItems;
  readonly root: LoadedPackRoot;
  readonly stateLoader: AdoptionStateLoader;
}): Promise<PullTransformResult> {
  const prepared = options.prepared;
  if (prepared.keyToImportId.size === 0) {
    return projectAdoptionItems({
      ...(options.performance === undefined ? {} : { performance: options.performance }),
      policy: options.policy,
      prepared,
      root: options.root,
      state: new Map(),
    });
  }
  const state = await options.stateLoader({
    keyToImportId: prepared.keyToImportId,
    policy: options.policy,
    rawItems: prepared.keyToRaw,
    resourceType: prepared.resource.type,
  });
  return projectAdoptionItems({
    ...(options.performance === undefined ? {} : { performance: options.performance }),
    policy: options.policy,
    prepared,
    root: options.root,
    state,
  });
}

function variableNameFor(
  resourceType: string,
  resourceRoots: Readonly<Record<string, string>>,
): string {
  const root = resourceRoots[resourceType] ?? resourceType;
  return root === resourceType ? "items" : `${resourceType}_items`;
}

export interface RunAdoptBatchOptions {
  readonly batchStateLoader?: AdoptionBatchStateLoader;
  readonly deployment: Deployment;
  readonly environment?: NodeJS.ProcessEnv;
  readonly inputDirectory: string;
  readonly onDiagnostic?: (message: string) => void;
  readonly policy: DriftPolicy;
  readonly performance?: PerformanceRecorder;
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
  readonly stateLoader: AdoptionStateLoader;
  readonly tenant: string;
}

async function runAdoptBatchInner(
  options: RunAdoptBatchOptions,
): Promise<AdoptBatchResult> {
  validateTenant(options.tenant);
  const write = options.onDiagnostic ?? (() => undefined);
  const selection = selectTransformResources({ root: options.root, selectors: options.selectors });
  for (const note of selection.notes) write(note.trimEnd());
  const selectedTopology = loadedRootTopology({
    deployment: options.deployment,
    root: options.root,
    selectors: selection.resourceTypes,
    tenant: options.tenant,
  });
  for (const diagnostic of selectedTopology.diagnostics) write(`NOTE: ${diagnostic.message}`);
  const topology = selectedTopology.topology;
  const processed: string[] = [];
  const skipped: string[] = [];
  const failed: string[] = [];
  const mode = oracleBatchMode(options.environment ?? process.env);
  const operationOrder = mode === "logical-root"
    ? referenceOrder({
        resourceTypes: selectedTopology.topology.roots.flatMap((logicalRoot) => {
          return logicalRoot.members;
        }),
        root: options.root,
      })
    : selection;
  if (operationOrder !== selection) {
    for (const note of operationOrder.notes) write(note.trimEnd());
  }
  const selected = new Set(operationOrder.resourceTypes);
  const selectionIndex = new Map(
    operationOrder.resourceTypes.map((resourceType, index) => [resourceType, index] as const),
  );
  const handled = new Set<string>();
  const disabledBatchRoots = new Set<string>();
  const topologyRootByMember = new Map<string, (typeof topology.roots)[number]>();
  for (const logicalRoot of topology.roots) {
    for (const member of logicalRoot.members) topologyRootByMember.set(member, logicalRoot);
  }

  const recordResourceSpan = (
    resourceType: string,
    started: number,
    instances: number,
    status: PerformanceStatus,
  ): void => {
    options.performance?.recordSpan({
      durationMs: options.performance.durationSince(started),
      instances,
      phase: "adopt.resource",
      resourceFamily: resourceType,
      status,
    });
  };

  const tryLogicalRootBatch = async (trigger: string): Promise<boolean> => {
    if (mode !== "logical-root") return false;
    const logicalRoot = topologyRootByMember.get(trigger);
    if (logicalRoot === undefined || disabledBatchRoots.has(logicalRoot.label)) return false;
    const orderedRoot = referenceOrder({
      resourceTypes: logicalRoot.members,
      root: options.root,
    });
    for (const note of orderedRoot.notes) write(note.trimEnd());
    const candidates = orderedRoot.resourceTypes.filter((member) => {
      const resource = options.root.resources.get(member);
      return resource !== undefined && !isObject(resource.registry.derive);
    });
    if (candidates.length < 2) {
      disabledBatchRoots.add(logicalRoot.label);
      return false;
    }
    const batchStarted = options.performance?.now() ?? 0;
    const batchFamily = oracleBatchResourceFamily(candidates);
    let batchInstances = 0;
    const recordBatchSpan = (status: PerformanceStatus): void => {
      options.performance?.recordSpan({
        durationMs: options.performance.durationSince(batchStarted),
        instances: batchInstances,
        phase: "adopt.resource",
        resourceFamily: batchFamily,
        status,
      });
    };

    const prepared: Array<{
      readonly adoption: PreparedAdoptionItems;
      readonly resource: LoadedResourceMetadata;
      readonly resourceType: string;
    }> = [];
    const countsByResource = new Map<string, AdoptionItemCounts>();
    const missingResources = new Set<string>();
    const preflightDiagnostics: string[] = [];
    const preflightSpans: Array<{
      readonly durationMs: number;
      readonly instances: number;
      readonly resourceType: string;
    }> = [];
    const skippedBeforePreflight = skipped.length;
    const failedBeforePreflight = failed.length;
    const flushPreflight = (): void => {
      for (const diagnostic of preflightDiagnostics) write(diagnostic);
      for (const span of preflightSpans) {
        options.performance?.recordSpan({
          durationMs: span.durationMs,
          instances: span.instances,
          phase: "adopt.resource",
          resourceFamily: span.resourceType,
          status: "skipped",
        });
      }
    };
    let countsFinished = false;
    const finishCounts = (publishedByResource: ReadonlyMap<string, number> = new Map()): void => {
      if (countsFinished) return;
      countsFinished = true;
      for (const resourceType of candidates) {
        const counts = countsByResource.get(resourceType);
        if (counts === undefined) continue;
        writeTerminalCounts({
          counts,
          published: publishedByResource.get(resourceType) ?? 0,
          resourceType,
          write,
        });
      }
    };
    let preflightFailed = false;
    let unsupportedRoot = false;
    for (const resourceType of candidates) {
      handled.add(resourceType);
      const started = options.performance?.now() ?? 0;
      let instances = 0;
      try {
        const sourceType = transformSourceType(options.root, resourceType);
        const source = path.join(options.inputDirectory, `${sourceType}.json`);
        const text = await readOptionalUtf8(source, `${resourceType} adoption input`);
        if (text === null) {
          missingResources.add(resourceType);
          countsByResource.set(resourceType, {
            eligible: 0,
            fetched: 0,
            systemSkipped: 0,
            unsupported: 0,
          });
          skipped.push(resourceType);
          preflightDiagnostics.push(`skip ${resourceType} (no ${source})`);
          preflightSpans.push({
            durationMs: options.performance?.durationSince(started) ?? 0,
            instances,
            resourceType,
          });
          continue;
        }
        const rawItems = parseDataJsonLosslessly(text);
        if (!Array.isArray(rawItems)) throw new TypeError(`${source} must be a JSON LIST of items`);
        instances = rawItems.length;
        batchInstances += instances;
        const resource = options.root.resources.get(resourceType);
        if (resource === undefined) throw new TypeError(`unknown resource ${resourceType}`);
        if (resource.provider !== logicalRoot.provider) {
          throw new TypeError(
            `logical root ${logicalRoot.label} mixes provider ${String(logicalRoot.provider)} with ${resource.provider}`,
          );
        }
        const preflight = prepareAdoptionItems({
          onClassified: (counts) => countsByResource.set(resourceType, counts),
          rawItems,
          resource,
          write: (message) => preflightDiagnostics.push(message),
        });
        if (preflight.status === "unsupported") {
          unsupportedRoot = true;
          continue;
        }
        prepared.push({ adoption: preflight.prepared, resource, resourceType });
      } catch (error: unknown) {
        preflightFailed = true;
        failed.push(resourceType);
        preflightDiagnostics.push(
          `error: ${resourceType}: ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    }
    if (preflightFailed || unsupportedRoot) {
      for (const resourceType of candidates) {
        if (!missingResources.has(resourceType) && !failed.includes(resourceType)) {
          failed.push(resourceType);
        }
      }
      flushPreflight();
      finishCounts();
      recordBatchSpan("failed");
      return true;
    }
    for (const entry of prepared) {
      try {
        await assertNoPendingMoves({
          deployment: options.deployment,
          resourceType: entry.resourceType,
          tenant: options.tenant,
        });
      } catch (error: unknown) {
        preflightFailed = true;
        if (!failed.includes(entry.resourceType)) failed.push(entry.resourceType);
        preflightDiagnostics.push(
          `error: ${entry.resourceType}: ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    }
    if (preflightFailed) {
      for (const resourceType of candidates) {
        if (!missingResources.has(resourceType) && !failed.includes(resourceType)) {
          failed.push(resourceType);
        }
      }
      flushPreflight();
      finishCounts();
      recordBatchSpan("failed");
      return true;
    }
    const candidateSet = new Set(candidates);
    const triggerIndex = selectionIndex.get(trigger);
    if (triggerIndex === undefined) {
      throw new TypeError(`selected resource ${trigger} has no reference-order position`);
    }
    const hasPendingExternalReferent = candidates.some((resourceType) => {
      const resource = options.root.resources.get(resourceType);
      if (resource === undefined) return false;
      return Object.values(transformReferenceSpecs(options.root, resource)).some((reference) => {
        if (!selected.has(reference.referent) || candidateSet.has(reference.referent)) return false;
        const referentIndex = selectionIndex.get(reference.referent);
        return !handled.has(reference.referent)
          && (referentIndex === undefined || referentIndex >= triggerIndex);
      });
    });
    if (hasPendingExternalReferent) {
      // Classification is root-wide even when reference ordering prevents a
      // batched Oracle. Roll back preflight bookkeeping and keep the safe root
      // on the per-resource path only after proving it has no unsupported item.
      skipped.length = skippedBeforePreflight;
      failed.length = failedBeforePreflight;
      for (const resourceType of candidates) handled.delete(resourceType);
      disabledBatchRoots.add(logicalRoot.label);
      return false;
    }
    flushPreflight();
    if (prepared.length === 0) {
      finishCounts();
      recordBatchSpan("skipped");
      return true;
    }
    if (options.batchStateLoader === undefined) {
      for (const entry of prepared) {
        failed.push(entry.resourceType);
      }
      write(
        `error: logical root ${logicalRoot.label}: logical-root Oracle batching was requested but no batch state loader was configured`,
      );
      finishCounts();
      recordBatchSpan("failed");
      return true;
    }

    const batchResources = prepared.map((entry) => ({
      keyToImportId: entry.adoption.keyToImportId,
      policy: options.policy,
      rawItems: entry.adoption.keyToRaw,
      resourceType: entry.resourceType,
    }));
    let stateByResource: OracleBatchState;
    try {
      stateByResource = await options.batchStateLoader({ resources: batchResources });
    } catch (error: unknown) {
      let isolatedFailures = 0;
      for (const request of batchResources) {
        try {
          await options.stateLoader(request);
        } catch (memberError: unknown) {
          isolatedFailures += 1;
          write(
            `error: ${request.resourceType}: ${memberError instanceof Error ? memberError.message : String(memberError)}`,
          );
        }
      }
      for (const entry of prepared) {
        if (!failed.includes(entry.resourceType)) failed.push(entry.resourceType);
      }
      const detail = error instanceof Error ? error.message : String(error);
      write(
        isolatedFailures === 0
          ? `error: logical root ${logicalRoot.label}: batched Oracle failed after every member succeeded independently: ${detail}`
          : `error: logical root ${logicalRoot.label}: batched Oracle failed; ${isolatedFailures} member failure(s) identified above: ${detail}`,
      );
      finishCounts();
      recordBatchSpan("failed");
      return true;
    }

    try {
      const expectedResourceTypes = new Set(prepared.map((entry) => entry.resourceType));
      const unexpectedResourceTypes = [...stateByResource.keys()]
        .filter((resourceType) => !expectedResourceTypes.has(resourceType))
        .sort();
      if (unexpectedResourceTypes.length > 0) {
        throw new TypeError(
          `logical-root Oracle result ${logicalRoot.label} contained unexpected resources ${unexpectedResourceTypes.join(", ")}`,
        );
      }
      const artifactOptions: TransformArtifactCompileOptions[] = [];
      const publishedByResource = new Map<string, number>();
      for (const entry of prepared) {
        const state = stateByResource.get(entry.resourceType);
        if (state === undefined) {
          throw new TypeError(
            `${entry.resourceType} missing from logical-root Oracle result ${logicalRoot.label}`,
          );
        }
        const result = await projectAdoptionItems({
          ...(options.performance === undefined ? {} : { performance: options.performance }),
          policy: options.policy,
          prepared: entry.adoption,
          root: options.root,
          state,
        });
        await assertNoPendingMoves({
          deployment: options.deployment,
          resourceType: entry.resourceType,
          tenant: options.tenant,
        });
        const references: Readonly<Record<string, TransformReferenceSpec>> = transformReferenceSpecs(
          options.root,
          entry.resource,
        );
        artifactOptions.push({
          bindingContext: transformBindingContext({
            deployment: options.deployment,
            references,
            resource: entry.resource,
            resourceRoots: topology.resource_roots,
            root: options.root,
          }),
          deployment: options.deployment,
          lookupNameField: transformLookupNameField(options.root, entry.resource),
          onDiagnostic: write,
          override: { import_id: adoptionMetadata(entry.resource).importId },
          references,
          resourceType: entry.resourceType,
          result,
          tenant: options.tenant,
          variableName: variableNameFor(entry.resourceType, topology.resource_roots),
        });
        publishedByResource.set(entry.resourceType, Object.keys(result.items).length);
      }
      const artifactStarted = options.performance?.now() ?? 0;
      let artifactStatus: "failed" | "success" = "success";
      try {
        const compiled = await compileTransformArtifactBatch(artifactOptions);
        for (const entry of prepared) {
          await assertNoPendingMoves({
            deployment: options.deployment,
            resourceType: entry.resourceType,
            tenant: options.tenant,
          });
        }
        await publishCompiledTransformArtifactBatch(compiled);
      } catch (error: unknown) {
        artifactStatus = "failed";
        throw error;
      } finally {
        options.performance?.recordSpan({
          durationMs: options.performance.durationSince(artifactStarted),
          instances: artifactOptions.reduce((total, item) => {
            return total + Object.keys(item.result.items).length;
          }, 0),
          phase: "adopt.artifact_write",
          resourceFamily: batchFamily,
          status: artifactStatus,
        });
      }
      for (const entry of prepared) {
        processed.push(entry.resourceType);
      }
      finishCounts(publishedByResource);
      recordBatchSpan("success");
    } catch (error: unknown) {
      for (const entry of prepared) {
        if (!failed.includes(entry.resourceType)) failed.push(entry.resourceType);
      }
      write(
        `error: logical root ${logicalRoot.label}: ${error instanceof Error ? error.message : String(error)}`,
      );
      finishCounts();
      recordBatchSpan("failed");
    }
    return true;
  };

  for (const resourceType of operationOrder.resourceTypes) {
    if (handled.has(resourceType)) continue;
    if (await tryLogicalRootBatch(resourceType)) continue;
    if (handled.has(resourceType)) continue;
    const resourceStarted = options.performance?.now() ?? 0;
    let instanceCount = 0;
    let resourceStatus: PerformanceStatus = "success";
    let counts: AdoptionItemCounts | undefined;
    let published = 0;
    try {
      const sourceType = transformSourceType(options.root, resourceType);
      const source = path.join(options.inputDirectory, `${sourceType}.json`);
      const text = await readOptionalUtf8(source, `${resourceType} adoption input`);
      if (text === null) {
        resourceStatus = "skipped";
        skipped.push(resourceType);
        write(`skip ${resourceType} (no ${source})`);
        continue;
      }
      const rawItems = parseDataJsonLosslessly(text);
      if (!Array.isArray(rawItems)) throw new TypeError(`${source} must be a JSON LIST of items`);
      instanceCount = rawItems.length;
      const resource = options.root.resources.get(resourceType);
      if (resource === undefined) throw new TypeError(`unknown resource ${resourceType}`);
      if (isObject(resource.registry.derive)) {
        await assertNoPendingMoves({
          deployment: options.deployment,
          resourceType,
          tenant: options.tenant,
        });
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
        if (delegated.failed.length > 0) {
          resourceStatus = "failed";
          failed.push(resourceType);
        } else if (delegated.skipped.length > 0) {
          resourceStatus = "skipped";
          skipped.push(resourceType);
        } else {
          processed.push(resourceType);
        }
        continue;
      }
      const preflight = prepareAdoptionItems({
        onClassified: (value) => { counts = value; },
        rawItems,
        resource,
        write,
      });
      if (preflight.status === "unsupported") {
        resourceStatus = "failed";
        failed.push(resourceType);
        continue;
      }
      await assertNoPendingMoves({
        deployment: options.deployment,
        resourceType,
        tenant: options.tenant,
      });
      const result = await adoptPreparedResourceItems({
        ...(options.performance === undefined ? {} : { performance: options.performance }),
        policy: options.policy,
        prepared: preflight.prepared,
        root: options.root,
        stateLoader: options.stateLoader,
      });
      await assertNoPendingMoves({ deployment: options.deployment, resourceType, tenant: options.tenant });
      const references: Readonly<Record<string, TransformReferenceSpec>> = transformReferenceSpecs(options.root, resource);
      const artifactStarted = options.performance?.now() ?? 0;
      let artifactStatus: "failed" | "success" = "success";
      try {
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
      } catch (error: unknown) {
        artifactStatus = "failed";
        throw error;
      } finally {
        options.performance?.recordSpan({
          durationMs: options.performance.durationSince(artifactStarted),
          instances: Object.keys(result.items).length,
          phase: "adopt.artifact_write",
          resourceFamily: resourceType,
          status: artifactStatus,
        });
      }
      published = Object.keys(result.items).length;
      processed.push(resourceType);
    } catch (error: unknown) {
      resourceStatus = "failed";
      failed.push(resourceType);
      write(`error: ${resourceType}: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      if (counts !== undefined) {
        writeTerminalCounts({
          counts,
          published: resourceStatus === "success" ? published : 0,
          resourceType,
          write,
        });
      }
      options.performance?.recordSpan({
        durationMs: options.performance.durationSince(resourceStarted),
        instances: instanceCount,
        phase: "adopt.resource",
        resourceFamily: resourceType,
        status: resourceStatus,
      });
    }
  }
  if (failed.length > 0) write(`\nadopt FAILED for: ${failed.join(" ")}`);
  return { failed, processed, skipped };
}

/** Execute the real generic adoption batch target without invoking Python. */
export async function runAdoptBatch(
  options: RunAdoptBatchOptions,
): Promise<AdoptBatchResult> {
  const started = options.performance?.now() ?? 0;
  try {
    const result = await runAdoptBatchInner(options);
    options.performance?.recordSpan({
      durationMs: options.performance.durationSince(started),
      phase: "adopt.total",
      status: result.failed.length === 0 ? "success" : "failed",
    });
    return result;
  } catch (error: unknown) {
    options.performance?.recordSpan({
      durationMs: options.performance.durationSince(started),
      phase: "adopt.total",
      status: "failed",
    });
    throw error;
  }
}

export async function defaultAdoptionStateLoader(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly onDiagnostic?: (message: string) => void;
  readonly performance?: PerformanceRecorder;
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
    ...(options.performance === undefined ? {} : { performance: options.performance }),
    ...(options.onDiagnostic === undefined ? {} : { onDiagnostic: options.onDiagnostic }),
  });
}

export async function defaultAdoptionBatchStateLoader(options: {
  readonly environment: NodeJS.ProcessEnv;
  readonly onDiagnostic?: (message: string) => void;
  readonly performance?: PerformanceRecorder;
  readonly root: LoadedPackRoot;
  readonly terraformExecutable: string;
}): Promise<AdoptionBatchStateLoader> {
  const timeoutMs = oracleTimeoutMs(options.environment);
  const runner = createOracleCommandRunner({
    limits: {
      maxStderrBytes: 1024 * 1024,
      maxStdoutBytes: 8 * 1024 * 1024,
      timeoutMs,
    },
    terraformExecutable: options.terraformExecutable,
  });
  return async (request) => importProviderStates({
    environment: options.environment,
    resources: request.resources,
    root: options.root,
    runner,
    ...(options.performance === undefined ? {} : { performance: options.performance }),
    ...(options.onDiagnostic === undefined ? {} : { onDiagnostic: options.onDiagnostic }),
  });
}
