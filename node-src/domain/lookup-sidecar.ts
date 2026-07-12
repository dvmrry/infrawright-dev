import { LosslessNumber } from "lossless-json";

import { renderPythonLosslessArtifactJson } from "../json/python-lossless-artifact.js";
import { sortedStrings } from "../json/python-compatible.js";
import { ProcessFailure } from "./errors.js";

const PYTHON_WHITESPACE_ONLY =
  /^[\u0009-\u000d\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$/u;

type JsonRecord = Record<string, unknown>;

function fail(message: string): never {
  throw new ProcessFailure({
    code: "INVALID_LOOKUP_SIDECAR",
    category: "domain",
    message,
  });
}

function safeRecord(entries: Iterable<readonly [string, unknown]>): JsonRecord {
  const output = Object.create(null) as JsonRecord;
  for (const [key, value] of entries) output[key] = value;
  return output;
}

function scalarIdentity(value: unknown): string | null {
  if (value === null || value === undefined || value === "") return null;
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isSafeInteger(value)) return String(value);
  if (value instanceof LosslessNumber) {
    return String(value);
  }
  return fail("lookup identity must be a scalar string or integer");
}

/** Render the shared provider-identity/projected-value lookup shape. */
export function renderLookupSidecar(options: {
  readonly identities: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly items: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
  readonly nameField: string;
}): string {
  const itemKeys = sortedStrings(Object.keys(options.items));
  const identityKeys = sortedStrings(Object.keys(options.identities));
  if (
    itemKeys.length !== identityKeys.length
    || itemKeys.some((key, index) => key !== identityKeys[index])
  ) {
    return fail("lookup identities do not exactly match projected items");
  }
  const byId = safeRecord([]);
  const keyById = safeRecord([]);
  for (const key of itemKeys) {
    const identity = options.identities[key];
    const item = options.items[key];
    if (identity === undefined || item === undefined) {
      return fail("lookup survivor is incomplete");
    }
    const merged = safeRecord([
      ...Object.keys(identity).map((name) => [name, identity[name]] as const),
      ...Object.keys(item).map((name) => [name, item[name]] as const),
    ]);
    const ident = scalarIdentity(merged.id);
    if (ident === null) continue;
    if (Object.hasOwn(keyById, ident)) {
      return fail("lookup identities must be unique");
    }
    const rawName = merged[options.nameField];
    byId[ident] = typeof rawName === "string" && !PYTHON_WHITESPACE_ONLY.test(rawName)
      ? rawName
      : "<unknown>";
    keyById[ident] = key;
  }
  return renderPythonLosslessArtifactJson(
    Object.keys(keyById).length === 0
      ? byId
      : { by_id: byId, key_by_id: keyById },
  );
}
