import type { LoadedPackRoot } from "../metadata/loader.js";
import { sortedStrings } from "../json/python-compatible.js";
import { expandLoadedResources } from "./roots.js";

export interface TransformSelection {
  readonly resourceTypes: readonly string[];
  readonly notes: readonly string[];
}

type ReferenceGraph = ReadonlyMap<string, ReadonlySet<string>>;

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function mergedTransformReferences(
  root: LoadedPackRoot,
): Readonly<Record<string, Readonly<Record<string, unknown>>>> {
  const active = new Set(root.active.packs);
  const output: Record<string, Record<string, unknown>> = Object.create(null) as Record<
    string,
    Record<string, unknown>
  >;
  for (const manifest of root.packs.manifests) {
    if (!active.has(manifest.name) || !isRecord(manifest.data.references)) continue;
    for (const [resourceType, fields] of Object.entries(manifest.data.references)) {
      if (!isRecord(fields)) continue;
      const target = output[resourceType]
        ?? (Object.create(null) as Record<string, unknown>);
      for (const [field, reference] of Object.entries(fields)) target[field] = reference;
      output[resourceType] = target;
    }
  }
  return output;
}

export function mergedTransformLookupSources(
  root: LoadedPackRoot,
): Readonly<Record<string, Readonly<Record<string, unknown>>>> {
  const active = new Set(root.active.packs);
  const output: Record<string, Readonly<Record<string, unknown>>> = Object.create(null) as Record<
    string,
    Readonly<Record<string, unknown>>
  >;
  for (const manifest of root.packs.manifests) {
    if (!active.has(manifest.name) || !isRecord(manifest.data.lookup_sources)) continue;
    for (const [resourceType, source] of Object.entries(manifest.data.lookup_sources)) {
      if (isRecord(source)) output[resourceType] = source;
    }
  }
  return output;
}

function referenceGraph(options: {
  readonly root: LoadedPackRoot;
  readonly resourceTypes: readonly string[];
}): { readonly graph: ReferenceGraph; readonly indegree: Map<string, number> } {
  const selected = new Set(options.resourceTypes);
  const graph = new Map<string, Set<string>>();
  const indegree = new Map<string, number>();
  for (const resourceType of selected) {
    graph.set(resourceType, new Set());
    indegree.set(resourceType, 0);
  }
  const references = mergedTransformReferences(options.root);
  for (const referrer of sortedStrings(selected)) {
    const fields = references[referrer];
    if (fields === undefined) continue;
    for (const field of sortedStrings(Object.keys(fields))) {
      const specification = fields[field];
      if (!isRecord(specification) || typeof specification.referent !== "string") continue;
      const referent = specification.referent;
      if (!selected.has(referent)) continue;
      const children = graph.get(referent);
      if (children === undefined || children.has(referrer)) continue;
      children.add(referrer);
      indegree.set(referrer, (indegree.get(referrer) ?? 0) + 1);
    }
  }
  return { graph, indegree };
}

function referenceCycleMembers(
  nodes: readonly string[],
  graph: ReferenceGraph,
): string[] {
  const selected = new Set(nodes);
  let nextIndex = 0;
  const indexes = new Map<string, number>();
  const lowlinks = new Map<string, number>();
  const stack: string[] = [];
  const onStack = new Set<string>();
  const cycleMembers = new Set<string>();

  const visit = (node: string): void => {
    const index = nextIndex;
    indexes.set(node, index);
    lowlinks.set(node, index);
    nextIndex += 1;
    stack.push(node);
    onStack.add(node);

    for (const child of sortedStrings(graph.get(node) ?? [])) {
      if (!selected.has(child)) continue;
      if (!indexes.has(child)) {
        visit(child);
        lowlinks.set(
          node,
          Math.min(lowlinks.get(node) ?? index, lowlinks.get(child) ?? index),
        );
      } else if (onStack.has(child)) {
        lowlinks.set(
          node,
          Math.min(lowlinks.get(node) ?? index, indexes.get(child) ?? index),
        );
      }
    }

    if (lowlinks.get(node) !== indexes.get(node)) return;
    const component: string[] = [];
    while (stack.length > 0) {
      const child = stack.pop();
      if (child === undefined) break;
      onStack.delete(child);
      component.push(child);
      if (child === node) break;
    }
    if (component.length > 1) {
      for (const member of component) cycleMembers.add(member);
    } else if (graph.get(node)?.has(node) === true) {
      cycleMembers.add(node);
    }
  };

  for (const node of sortedStrings(selected)) {
    if (!indexes.has(node)) visit(node);
  }
  return sortedStrings(cycleMembers);
}

/** Match engine.ops.reference_order without writing its cycle note to stderr. */
export function referenceOrder(options: {
  readonly root: LoadedPackRoot;
  readonly resourceTypes: readonly string[];
}): TransformSelection {
  const resourceTypes = sortedStrings(new Set(options.resourceTypes));
  const { graph, indegree } = referenceGraph({
    root: options.root,
    resourceTypes,
  });
  const cycleMembers = referenceCycleMembers(resourceTypes, graph);
  const notes = cycleMembers.length === 0
    ? []
    : [
        `NOTE: reference order cycle detected among ${cycleMembers.join(", ")}; breaking alphabetically\n`,
      ];
  const remaining = new Set(resourceTypes);
  let ready = resourceTypes.filter((resourceType) => indegree.get(resourceType) === 0);
  const ordered: string[] = [];
  while (remaining.size > 0) {
    let resourceType: string;
    if (ready.length > 0) {
      const candidate = ready.shift();
      if (candidate === undefined) throw new Error("reference ordering lost a ready node");
      if (!remaining.has(candidate)) continue;
      resourceType = candidate;
    } else {
      resourceType = cycleMembers.find((member) => remaining.has(member))
        ?? sortedStrings(remaining)[0]
        ?? (() => { throw new Error("reference ordering lost all remaining nodes"); })();
    }
    remaining.delete(resourceType);
    ordered.push(resourceType);
    for (const child of sortedStrings(graph.get(resourceType) ?? [])) {
      const next = (indegree.get(child) ?? 0) - 1;
      indegree.set(child, next);
      if (next === 0 && remaining.has(child)) ready.push(child);
    }
    ready = sortedStrings(ready);
  }
  return Object.freeze({
    resourceTypes: Object.freeze(ordered),
    notes: Object.freeze(notes),
  });
}

/** Expand active generated selectors, then order referents before referrers. */
export function selectTransformResources(options: {
  readonly root: LoadedPackRoot;
  readonly selectors: readonly string[];
}): TransformSelection {
  return referenceOrder({
    root: options.root,
    resourceTypes: expandLoadedResources(options.root, options.selectors),
  });
}

/** Resolve the pull filename stem consumed by one generated transform resource. */
export function transformSourceType(
  root: LoadedPackRoot,
  resourceType: string,
): string {
  const resource = root.resources.get(resourceType);
  if (resource === undefined || resource.registry.generate !== true) {
    throw new TypeError(`unknown or non-generated transform resource ${JSON.stringify(resourceType)}`);
  }
  const derive = resource.registry.derive;
  if (isRecord(derive) && typeof derive.from === "string") return derive.from;
  return resourceType;
}
