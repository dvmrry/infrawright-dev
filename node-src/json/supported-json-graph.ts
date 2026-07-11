import { LosslessNumber } from "lossless-json";

export interface SupportedJsonGraphOptions {
  readonly maxDepth: number;
  /**
   * Preserve the original pull-compiler preflight when false.  Strict callers
   * set this to true to require an acyclic graph made only from JSON scalars,
   * dense arrays, and descriptor-safe plain records.
   */
  readonly requirePlainJson: boolean;
}

type Visit = {
  readonly kind: "enter";
  readonly value: unknown;
  readonly depth: number;
} | {
  readonly kind: "leave";
  readonly value: object;
};

type SnapshotVisit = {
  readonly kind: "enter";
  readonly value: unknown;
  readonly depth: number;
  readonly assign: (value: unknown) => void;
} | {
  readonly kind: "leave";
  readonly source: object;
  readonly snapshot: object;
};

export type PlainJsonGraphSnapshot =
  | { readonly ok: true; readonly value: unknown }
  | { readonly ok: false };

const JSON_NUMBER =
  /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/;

function losslessNumberIsJson(value: LosslessNumber): boolean {
  return JSON_NUMBER.test(value.toString());
}

function snapshotLosslessNumber(value: object): LosslessNumber | null {
  if (Object.getPrototypeOf(value) !== LosslessNumber.prototype) {
    return null;
  }
  const keys = Reflect.ownKeys(value);
  if (
    keys.length !== 2
    || !keys.includes("isLosslessNumber")
    || !keys.includes("value")
  ) {
    return null;
  }
  const marker = Object.getOwnPropertyDescriptor(value, "isLosslessNumber");
  const token = Object.getOwnPropertyDescriptor(value, "value");
  if (
    marker === undefined
    || !("value" in marker)
    || marker.enumerable !== true
    || marker.value !== true
    || token === undefined
    || !("value" in token)
    || token.enumerable !== true
    || typeof token.value !== "string"
    || !JSON_NUMBER.test(token.value)
  ) {
    return null;
  }
  return Object.freeze(new LosslessNumber(token.value));
}

interface StrictArrayShape {
  readonly children: readonly { readonly key: string; readonly value: unknown }[];
  readonly length: number;
}

function strictArrayShape(
  value: readonly unknown[],
): StrictArrayShape | null {
  if (Object.getPrototypeOf(value) !== Array.prototype) {
    return null;
  }
  const ownKeys = Reflect.ownKeys(value);
  const lengthDescriptor = Object.getOwnPropertyDescriptor(value, "length");
  if (
    lengthDescriptor === undefined
    || !("value" in lengthDescriptor)
    || !Number.isSafeInteger(lengthDescriptor.value)
    || lengthDescriptor.value < 0
  ) {
    return null;
  }
  const length = lengthDescriptor.value as number;
  if (
    ownKeys.length !== length + 1
    || ownKeys.some((key) => typeof key !== "string")
    || !ownKeys.includes("length")
  ) {
    return null;
  }
  const children: { key: string; value: unknown }[] = [];
  for (let index = 0; index < length; index += 1) {
    const key = String(index);
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return null;
    }
    children.push({ key, value: descriptor.value });
  }
  const expected = new Set(["length", ...children.map((entry) => entry.key)]);
  if (ownKeys.some((key) => typeof key !== "string" || !expected.has(key))) {
    return null;
  }
  return { children, length };
}

function strictRecordChildren(
  value: object,
): readonly { readonly key: string; readonly value: unknown }[] | null {
  const prototype = Object.getPrototypeOf(value) as unknown;
  if (prototype !== Object.prototype && prototype !== null) {
    return null;
  }
  const ownKeys = Reflect.ownKeys(value);
  const children: { key: string; value: unknown }[] = [];
  for (const key of ownKeys) {
    if (typeof key !== "string" || !key.isWellFormed()) {
      return null;
    }
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (
      descriptor === undefined
      || !("value" in descriptor)
      || descriptor.enumerable !== true
    ) {
      return null;
    }
    children.push({ key, value: descriptor.value });
  }
  return children;
}

/**
 * Iteratively validate graph shape before any recursive clone/projection.
 *
 * The false `requirePlainJson` mode is intentionally byte-for-byte compatible
 * with the former ZCC pull artifact preflight.  It remains available only so
 * extracting this helper does not silently tighten that existing boundary.
 */
export function supportedJsonGraph(
  value: unknown,
  options: SupportedJsonGraphOptions,
): boolean {
  if (
    !Number.isSafeInteger(options.maxDepth)
    || options.maxDepth < 1
  ) {
    return false;
  }
  const ancestors = new Set<object>();
  const stack: Visit[] = [{ kind: "enter", value, depth: 1 }];
  while (stack.length > 0) {
    const visit = stack.pop();
    if (visit === undefined) {
      return false;
    }
    if (visit.kind === "leave") {
      ancestors.delete(visit.value);
      continue;
    }
    const current = visit.value;
    if (typeof current === "string") {
      if (!current.isWellFormed()) {
        return false;
      }
      continue;
    }
    if (current instanceof LosslessNumber) {
      if (options.requirePlainJson && !losslessNumberIsJson(current)) {
        return false;
      }
      continue;
    }
    if (typeof current !== "object" || current === null) {
      if (
        options.requirePlainJson
        && current !== null
        && typeof current !== "boolean"
        && !(typeof current === "number" && Number.isFinite(current))
      ) {
        return false;
      }
      continue;
    }
    if (visit.depth > options.maxDepth || ancestors.has(current)) {
      return false;
    }

    let children: readonly { readonly key: string; readonly value: unknown }[];
    if (options.requirePlainJson) {
      const strictChildren = Array.isArray(current)
        ? strictArrayShape(current)?.children ?? null
        : strictRecordChildren(current);
      if (strictChildren === null) {
        return false;
      }
      children = strictChildren;
    } else if (Array.isArray(current)) {
      const legacyChildren: { key: string; value: unknown }[] = [];
      for (let index = 0; index < current.length; index += 1) {
        const key = String(index);
        const descriptor = Object.getOwnPropertyDescriptor(current, key);
        if (descriptor === undefined || !("value" in descriptor)) {
          return false;
        }
        legacyChildren.push({ key, value: descriptor.value });
      }
      children = legacyChildren;
    } else {
      const prototype = Object.getPrototypeOf(current) as unknown;
      if (prototype !== Object.prototype && prototype !== null) {
        return false;
      }
      const legacyChildren: { key: string; value: unknown }[] = [];
      for (const key of Object.keys(current)) {
        if (!key.isWellFormed()) {
          return false;
        }
        const descriptor = Object.getOwnPropertyDescriptor(current, key);
        if (descriptor === undefined || !("value" in descriptor)) {
          return false;
        }
        legacyChildren.push({ key, value: descriptor.value });
      }
      children = legacyChildren;
    }

    ancestors.add(current);
    stack.push({ kind: "leave", value: current });
    for (let index = children.length - 1; index >= 0; index -= 1) {
      const child = children[index];
      if (child === undefined) {
        return false;
      }
      stack.push({
        kind: "enter",
        value: child.value,
        depth: visit.depth + 1,
      });
    }
  }
  return true;
}

/**
 * Validate and snapshot a hostile graph in one descriptor-read pass.
 *
 * The returned graph contains no caller-owned records, arrays, descriptors,
 * proxies, or LosslessNumber instances. It is deeply frozen before return, so
 * later identity/projection work cannot race a graph that changed after its
 * validation.
 */
export function snapshotPlainJsonGraph(
  value: unknown,
  options: { readonly maxDepth: number },
): PlainJsonGraphSnapshot {
  if (!Number.isSafeInteger(options.maxDepth) || options.maxDepth < 1) {
    return { ok: false };
  }
  const root: { value?: unknown } = {};
  const ancestors = new Set<object>();
  const stack: SnapshotVisit[] = [{
    kind: "enter",
    value,
    depth: 1,
    assign: (snapshot) => {
      root.value = snapshot;
    },
  }];
  while (stack.length > 0) {
    const visit = stack.pop();
    if (visit === undefined) {
      return { ok: false };
    }
    if (visit.kind === "leave") {
      Object.freeze(visit.snapshot);
      ancestors.delete(visit.source);
      continue;
    }
    const current = visit.value;
    if (typeof current === "string") {
      if (!current.isWellFormed()) {
        return { ok: false };
      }
      visit.assign(current);
      continue;
    }
    if (current === null || typeof current === "boolean") {
      visit.assign(current);
      continue;
    }
    if (typeof current === "number") {
      if (!Number.isFinite(current)) {
        return { ok: false };
      }
      visit.assign(current);
      continue;
    }
    if (typeof current !== "object") {
      return { ok: false };
    }

    const lossless = snapshotLosslessNumber(current);
    if (lossless !== null) {
      visit.assign(lossless);
      continue;
    }
    if (visit.depth > options.maxDepth || ancestors.has(current)) {
      return { ok: false };
    }
    const arrayShape = Array.isArray(current)
      ? strictArrayShape(current)
      : null;
    const children = Array.isArray(current)
      ? arrayShape?.children ?? null
      : strictRecordChildren(current);
    if (children === null) {
      return { ok: false };
    }
    const snapshot: unknown[] | Record<string, unknown> = Array.isArray(current)
      ? new Array(arrayShape?.length ?? 0) as unknown[]
      : Object.create(null) as Record<string, unknown>;
    visit.assign(snapshot);
    ancestors.add(current);
    stack.push({ kind: "leave", source: current, snapshot });
    for (let index = children.length - 1; index >= 0; index -= 1) {
      const child = children[index];
      if (child === undefined) {
        return { ok: false };
      }
      stack.push({
        kind: "enter",
        value: child.value,
        depth: visit.depth + 1,
        assign: (childSnapshot, key = child.key) => {
          Object.defineProperty(snapshot, key, {
            configurable: true,
            enumerable: true,
            value: childSnapshot,
            writable: true,
          });
        },
      });
    }
  }
  if (!("value" in root)) {
    return { ok: false };
  }
  return Object.freeze({ ok: true, value: root.value });
}
