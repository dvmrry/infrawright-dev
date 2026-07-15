import type { LoadedPackRoot } from "../metadata/loader.js";
import { isObject } from "../metadata/validation.js";
import { comparePythonStrings, sortedStrings } from "../json/python-compatible.js";
import { deploymentReferenceBindingMode } from "./deployment.js";
import { mergedTransformReferences } from "./transform-selection.js";
import type { Deployment, RootTopology } from "./types.js";

export interface CrossStateReferenceEdge {
  readonly field: string;
  readonly referrer: string;
  readonly referrerRoot: string;
  readonly referent: string;
  readonly referentRoot: string;
}

export interface CrossStateReferenceTopology {
  readonly edges: readonly CrossStateReferenceEdge[];
  readonly dependenciesByRoot: ReadonlyMap<string, ReadonlySet<string>>;
  readonly outputsByRoot: ReadonlyMap<string, ReadonlySet<string>>;
}

/** Expand selected state roots through their complete referent dependency set. */
export function crossStateDependencyClosure(
  selectedRoots: Iterable<string>,
  dependenciesByRoot: ReadonlyMap<string, ReadonlySet<string>>,
): readonly string[] {
  const selected = new Set(selectedRoots);
  const pending = sortedStrings(selected);
  while (pending.length > 0) {
    const current = pending.shift() ?? "";
    for (const dependency of sortedStrings(dependenciesByRoot.get(current) ?? [])) {
      if (selected.has(dependency)) continue;
      selected.add(dependency);
      pending.push(dependency);
      pending.sort(comparePythonStrings);
    }
  }
  return sortedStrings(selected);
}

function generatedNonDerived(root: LoadedPackRoot, resourceType: string): boolean {
  const resource = root.resources.get(resourceType);
  return resource?.registry.generate === true && !isObject(resource.registry.derive);
}

function add(
  values: Map<string, Set<string>>,
  key: string,
  value: string,
): void {
  const selected = values.get(key) ?? new Set<string>();
  selected.add(value);
  values.set(key, selected);
}

function frozenSets(values: ReadonlyMap<string, ReadonlySet<string>>): ReadonlyMap<string, ReadonlySet<string>> {
  return new Map(
    sortedStrings(values.keys()).map((key) => [
      key,
      new Set(sortedStrings(values.get(key) ?? [])),
    ]),
  );
}

function cyclePath(dependencies: ReadonlyMap<string, ReadonlySet<string>>): readonly string[] | null {
  const state = new Map<string, "visiting" | "done">();
  const stack: string[] = [];
  const visit = (root: string): readonly string[] | null => {
    state.set(root, "visiting");
    stack.push(root);
    for (const dependency of sortedStrings(dependencies.get(root) ?? [])) {
      if (state.get(dependency) === "visiting") {
        const start = stack.indexOf(dependency);
        return [...stack.slice(start), dependency];
      }
      if (state.get(dependency) === undefined) {
        const found = visit(dependency);
        if (found !== null) return found;
      }
    }
    stack.pop();
    state.set(root, "done");
    return null;
  };
  const nodes = new Set<string>();
  for (const [root, targets] of dependencies) {
    nodes.add(root);
    for (const target of targets) nodes.add(target);
  }
  for (const root of sortedStrings(nodes)) {
    if (state.get(root) !== undefined) continue;
    const found = visit(root);
    if (found !== null) return found;
  }
  return null;
}

/** Resolve the pack-declared edges that cross deployment state boundaries. */
export function crossStateReferenceTopology(options: {
  readonly deployment: Deployment;
  readonly root: LoadedPackRoot;
  readonly topology: RootTopology;
}): CrossStateReferenceTopology {
  const edges: CrossStateReferenceEdge[] = [];
  const dependenciesByRoot = new Map<string, Set<string>>();
  const outputsByRoot = new Map<string, Set<string>>();
  const references = mergedTransformReferences(options.root);
  for (const referrer of sortedStrings(Object.keys(references))) {
    const referrerResource = options.root.resources.get(referrer);
    if (
      referrerResource === undefined
      || deploymentReferenceBindingMode(options.deployment, referrerResource.provider) !== "cross_state"
    ) {
      continue;
    }
    if (!generatedNonDerived(options.root, referrer)) {
      throw new TypeError(
        `cross-state reference referrer ${referrer} must be a generated non-derived resource`,
      );
    }
    const referrerRoot = options.topology.resource_roots[referrer];
    if (referrerRoot === undefined) {
      throw new TypeError(`cross-state reference referrer ${referrer} has no deployment root`);
    }
    const fields = references[referrer] ?? {};
    for (const field of sortedStrings(Object.keys(fields))) {
      const specification = fields[field];
      if (!isObject(specification) || typeof specification.referent !== "string") continue;
      const referent = specification.referent;
      if (!generatedNonDerived(options.root, referent)) {
        throw new TypeError(
          `cross-state reference ${referrer}.${field} targets ${referent}, which is not a generated non-derived resource`,
        );
      }
      const referentRoot = options.topology.resource_roots[referent];
      if (referentRoot === undefined) {
        throw new TypeError(
          `cross-state reference ${referrer}.${field} targets ${referent}, which has no deployment root`,
        );
      }
      if (referrerRoot === referentRoot) continue;
      edges.push({ field, referrer, referrerRoot, referent, referentRoot });
      add(dependenciesByRoot, referrerRoot, referentRoot);
      add(outputsByRoot, referentRoot, referent);
    }
  }
  const cycle = cyclePath(dependenciesByRoot);
  if (cycle !== null) {
    throw new TypeError(
      `cross-state reference cycle detected: ${cycle.join(" -> ")}; explicitly group every member of the cycle into one state root`,
    );
  }
  edges.sort((left, right) => {
    return comparePythonStrings(left.referrer, right.referrer)
      || comparePythonStrings(left.field, right.field)
      || comparePythonStrings(left.referent, right.referent);
  });
  return {
    edges,
    dependenciesByRoot: frozenSets(dependenciesByRoot),
    outputsByRoot: frozenSets(outputsByRoot),
  };
}
