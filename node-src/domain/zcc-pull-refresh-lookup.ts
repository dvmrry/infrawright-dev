import { parseGeneratedImports } from "./import-moves.js";
import { parseDataJsonLosslessly } from "../json/control.js";
import {
  sameStringSequence,
  sortedStrings,
} from "../json/python-compatible.js";

const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

type JsonRecord = Record<string, unknown>;

function record(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const prototype = Object.getPrototypeOf(value) as unknown;
  return prototype === Object.prototype || prototype === null
    ? value as JsonRecord
    : null;
}

function ownValue(value: JsonRecord, key: string): unknown {
  return Object.getOwnPropertyDescriptor(value, key)?.value;
}

/**
 * Verify Python lookup.build_lookup/build_lookup_key_map duplicate-ID behavior.
 * Canonical imports are key-sorted, so the last key for an ID deterministically
 * overwrites both lookup maps during refresh-only evidence compilation.
 */
export function matchesPythonCollapsedRefreshLookup(options: {
  readonly resourceType: string;
  readonly importsContent: unknown;
  readonly lookupContent: unknown;
  readonly tfvarsContent: unknown;
  readonly variableName: unknown;
}): boolean {
  if (
    options.resourceType !== "zcc_trusted_network"
    || typeof options.importsContent !== "string"
    || typeof options.lookupContent !== "string"
    || typeof options.tfvarsContent !== "string"
    || typeof options.variableName !== "string"
  ) {
    return false;
  }
  try {
    const entries = parseGeneratedImports(
      options.resourceType,
      options.importsContent,
    );
    const ids = entries.map((entry) => entry.importId);
    if (new Set(ids).size === ids.length) {
      return false;
    }
    const expectedKeys = new Map<string, string>();
    for (const entry of entries) {
      expectedKeys.set(entry.importId, entry.key);
    }

    const envelope = record(parseDataJsonLosslessly(options.lookupContent));
    if (
      envelope === null
      || !sameStringSequence(
        sortedStrings(Object.keys(envelope)),
        ["by_id", "key_by_id"],
      )
    ) {
      return false;
    }
    const byId = record(ownValue(envelope, "by_id"));
    const keyById = record(ownValue(envelope, "key_by_id"));
    const tfvars = record(parseDataJsonLosslessly(options.tfvarsContent));
    const items = tfvars === null
      ? null
      : record(ownValue(tfvars, options.variableName));
    if (byId === null || keyById === null || items === null) {
      return false;
    }
    const expectedIds = sortedStrings(expectedKeys.keys());
    if (
      !sameStringSequence(sortedStrings(Object.keys(byId)), expectedIds)
      || !sameStringSequence(sortedStrings(Object.keys(keyById)), expectedIds)
    ) {
      return false;
    }
    return expectedIds.every((id) => {
      const expectedKey = expectedKeys.get(id);
      const item = expectedKey === undefined
        ? null
        : record(ownValue(items, expectedKey));
      const rawDisplay = item === null ? null : ownValue(item, "name");
      const expectedDisplay = typeof rawDisplay === "string"
          && !PYTHON_WHITESPACE_ONLY.test(rawDisplay)
        ? rawDisplay
        : "<unknown>";
      return ownValue(byId, id) === expectedDisplay
        && ownValue(keyById, id) === expectedKey;
    });
  } catch {
    return false;
  }
}
