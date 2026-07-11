import { ProcessFailure } from "./errors.js";

const RESOURCE_TYPE = /^[a-z][a-z0-9_]*$/;
const MAX_GENERATED_IMPORT_PAIRS = 50_000;
const MAX_IMPORT_MOVE_CANDIDATES = 50_000;

export interface GeneratedImportPair {
  readonly key: string;
  readonly importId: string;
}

export interface ImportMove {
  readonly oldKey: string;
  readonly newKey: string;
}

export type ImportMoveSuppressionReason =
  | "ambiguous"
  | "duplicate_from"
  | "key_swap"
  | "destination_occupied";

export interface ImportMoveSuppression extends ImportMove {
  readonly importId: string;
  readonly reason: ImportMoveSuppressionReason;
}

export interface ImportMoveDerivation {
  readonly moves: readonly ImportMove[];
  readonly suppressed: readonly ImportMoveSuppression[];
}

export interface ParsedHclQuotedString {
  readonly value: string;
  readonly end: number;
}

function fail(code: string, message: string): never {
  throw new ProcessFailure({
    code,
    category: "domain",
    message,
  });
}

function requireResourceType(resourceType: string): void {
  if (typeof resourceType !== "string" || !RESOURCE_TYPE.test(resourceType)) {
    fail(
      "INVALID_IMPORT_RESOURCE_TYPE",
      "import resource type must be a canonical Terraform identifier",
    );
  }
}

function compareCodePoints(left: string, right: string): number {
  let leftIndex = 0;
  let rightIndex = 0;
  while (leftIndex < left.length && rightIndex < right.length) {
    const leftPoint = left.codePointAt(leftIndex) ?? 0;
    const rightPoint = right.codePointAt(rightIndex) ?? 0;
    if (leftPoint !== rightPoint) {
      return leftPoint - rightPoint;
    }
    leftIndex += leftPoint > 0xffff ? 2 : 1;
    rightIndex += rightPoint > 0xffff ? 2 : 1;
  }
  return (leftIndex < left.length ? 1 : 0)
    - (rightIndex < right.length ? 1 : 0);
}

function comparePairs(
  left: GeneratedImportPair,
  right: GeneratedImportPair,
): number {
  return compareCodePoints(left.key, right.key);
}

function compareMoves(left: ImportMove, right: ImportMove): number {
  return compareCodePoints(left.oldKey, right.oldKey)
    || compareCodePoints(left.newKey, right.newKey);
}

function compareSuppressions(
  left: ImportMoveSuppression,
  right: ImportMoveSuppression,
): number {
  return compareMoves(left, right)
    || compareCodePoints(left.importId, right.importId)
    || compareCodePoints(left.reason, right.reason);
}

/** Match engine.transform.hcl_string_literal for generated import addresses. */
export function renderHclQuotedString(value: string): string {
  if (typeof value !== "string") {
    return fail(
      "INVALID_HCL_QUOTED_STRING",
      "generated HCL string values must be strings",
    );
  }
  if (value.includes("\0") || !value.isWellFormed()) {
    return fail(
      "INVALID_HCL_QUOTED_STRING",
      "generated HCL string values contain an unsupported character",
    );
  }
  const escaped = value
    .replaceAll("\\", "\\\\")
    .replaceAll("\"", "\\\"")
    .replaceAll("\n", "\\n")
    .replaceAll("\r", "\\r")
    .replaceAll("\t", "\\t")
    // Use a callback because `$` has replacement-pattern semantics in the
    // string overload (`$$` means one literal dollar).
    .replaceAll("${", () => "$${")
    .replaceAll("%{", "%%{");
  return `"${escaped}"`;
}

/** Decode the deliberately small HCL quoted-string grammar emitted above. */
export function parseHclQuotedString(
  text: string,
  start = 0,
): ParsedHclQuotedString {
  if (
    typeof text !== "string"
    ||
    !Number.isSafeInteger(start)
    || start < 0
    || start >= text.length
    || text[start] !== "\""
  ) {
    return fail(
      "INVALID_HCL_QUOTED_STRING",
      "expected a generated HCL quoted string literal",
    );
  }

  const output: string[] = [];
  let index = start + 1;
  while (index < text.length) {
    const character = text[index];
    if (character === "\"") {
      return Object.freeze({ value: output.join(""), end: index + 1 });
    }
    if (character === "\\") {
      index += 1;
      if (index >= text.length) {
        return fail(
          "INVALID_HCL_QUOTED_STRING",
          "generated HCL string has an unterminated escape sequence",
        );
      }
      const escaped = text[index];
      if (escaped === "n") {
        output.push("\n");
      } else if (escaped === "r") {
        output.push("\r");
      } else if (escaped === "t") {
        output.push("\t");
      } else if (escaped === "\"" || escaped === "\\") {
        output.push(escaped);
      } else {
        return fail(
          "INVALID_HCL_QUOTED_STRING",
          "generated HCL string contains an unsupported escape sequence",
        );
      }
      index += 1;
      continue;
    }
    if (text.startsWith("$${", index)) {
      output.push("${");
      index += 3;
      continue;
    }
    if (text.startsWith("%%{", index)) {
      output.push("%{");
      index += 3;
      continue;
    }
    if (character === "\0") {
      return fail(
        "INVALID_HCL_QUOTED_STRING",
        "generated HCL string values cannot contain NUL bytes",
      );
    }
    output.push(character ?? "");
    index += 1;
  }
  return fail(
    "INVALID_HCL_QUOTED_STRING",
    "generated HCL string literal is unterminated",
  );
}

function requirePair(pair: GeneratedImportPair): void {
  if (
    typeof pair !== "object"
    || pair === null
    || Array.isArray(pair)
    || typeof pair.key !== "string"
    || typeof pair.importId !== "string"
  ) {
    fail(
      "INVALID_GENERATED_IMPORTS",
      "generated import keys and ids must be strings",
    );
  }
}

/** Render the byte-canonical import blocks emitted by engine.transform. */
export function renderGeneratedImports(
  resourceType: string,
  pairs: readonly GeneratedImportPair[],
): string {
  requireResourceType(resourceType);
  if (!Array.isArray(pairs)) {
    return fail(
      "INVALID_GENERATED_IMPORTS",
      "generated imports must be an array of key and id pairs",
    );
  }
  if (pairs.length > MAX_GENERATED_IMPORT_PAIRS) {
    return fail(
      "IMPORT_MOVE_LIMIT_EXCEEDED",
      "generated imports exceed the bounded pair contract",
    );
  }
  const keys = new Set<string>();
  for (const pair of pairs) {
    requirePair(pair);
    if (keys.has(pair.key)) {
      return fail(
        "INVALID_GENERATED_IMPORTS",
        "generated imports contain a duplicate Terraform address",
      );
    }
    keys.add(pair.key);
  }
  const blocks = [...pairs].sort(comparePairs).map((pair) => {
    return [
      "import {",
      `  to = module.${resourceType}.${resourceType}.this[${renderHclQuotedString(pair.key)}]`,
      `  id = ${renderHclQuotedString(pair.importId)}`,
      "}",
      "",
    ].join("\n");
  });
  return blocks.join("\n");
}

function expectLiteral(text: string, start: number, literal: string): number {
  if (!text.startsWith(literal, start)) {
    return fail(
      "INVALID_GENERATED_IMPORTS",
      "imports artifact is not a complete canonical generated import file",
    );
  }
  return start + literal.length;
}

/**
 * Parse only complete, byte-canonical Infrawright-generated import files.
 *
 * This intentionally rejects HCL that is semantically equivalent but was not
 * generated by Infrawright. The prior artifact becomes state-move evidence,
 * so comments, partial blocks, repeated addresses, and alternate formatting
 * must not be interpreted heuristically.
 */
export function parseGeneratedImports(
  resourceType: string,
  text: string,
): readonly GeneratedImportPair[] {
  requireResourceType(resourceType);
  if (typeof text !== "string") {
    return fail(
      "INVALID_GENERATED_IMPORTS",
      "generated imports must be canonical text",
    );
  }
  if (text.length === 0) {
    return Object.freeze([]);
  }

  const pairs: GeneratedImportPair[] = [];
  const keys = new Set<string>();
  let cursor = 0;
  while (cursor < text.length) {
    cursor = expectLiteral(
      text,
      cursor,
      `import {\n  to = module.${resourceType}.${resourceType}.this[`,
    );
    const parsedKey = parseHclQuotedString(text, cursor);
    cursor = expectLiteral(text, parsedKey.end, "]\n  id = ");
    const parsedId = parseHclQuotedString(text, cursor);
    cursor = expectLiteral(text, parsedId.end, "\n}\n");

    if (keys.has(parsedKey.value)) {
      return fail(
        "INVALID_GENERATED_IMPORTS",
        "generated imports contain a duplicate Terraform address",
      );
    }
    if (pairs.length >= MAX_GENERATED_IMPORT_PAIRS) {
      return fail(
        "IMPORT_MOVE_LIMIT_EXCEEDED",
        "generated imports exceed the bounded pair contract",
      );
    }
    keys.add(parsedKey.value);
    pairs.push(Object.freeze({
      key: parsedKey.value,
      importId: parsedId.value,
    }));

    if (cursor < text.length) {
      cursor = expectLiteral(text, cursor, "\n");
    }
  }

  if (renderGeneratedImports(resourceType, pairs) !== text) {
    return fail(
      "INVALID_GENERATED_IMPORTS",
      "imports artifact is not byte-canonical generated output",
    );
  }
  return Object.freeze(pairs);
}

function pairsByKey(
  pairs: readonly GeneratedImportPair[],
): ReadonlyMap<string, string> {
  return new Map(pairs.map((pair) => [pair.key, pair.importId]));
}

function keysByImportId(
  pairs: readonly GeneratedImportPair[],
): ReadonlyMap<string, readonly string[]> {
  const grouped = new Map<string, string[]>();
  for (const pair of pairs) {
    const keys = grouped.get(pair.importId);
    if (keys === undefined) {
      grouped.set(pair.importId, [pair.key]);
    } else {
      keys.push(pair.key);
    }
  }
  for (const keys of grouped.values()) {
    keys.sort(compareCodePoints);
  }
  return grouped;
}

function isKeySwap(
  oldKey: string,
  newKey: string,
  oldPairs: ReadonlyMap<string, string>,
  newPairs: ReadonlyMap<string, string>,
): boolean {
  return oldPairs.has(oldKey)
    && oldPairs.has(newKey)
    && newPairs.has(oldKey)
    && newPairs.has(newKey)
    && oldPairs.get(oldKey) === newPairs.get(newKey)
    && oldPairs.get(newKey) === newPairs.get(oldKey);
}

/** Derive only unambiguous, unoccupied state-address moves. */
export function deriveImportMoves(
  resourceType: string,
  oldImportsText: string,
  newImportsText: string,
): ImportMoveDerivation {
  const oldEntries = parseGeneratedImports(resourceType, oldImportsText);
  const newEntries = parseGeneratedImports(resourceType, newImportsText);
  const oldPairs = pairsByKey(oldEntries);
  const newPairs = pairsByKey(newEntries);
  const oldById = keysByImportId(oldEntries);
  const newById = keysByImportId(newEntries);

  const candidates: Array<ImportMove & { readonly importId: string }> = [];
  for (const importId of [...newById.keys()].sort(compareCodePoints)) {
    const oldKeys = oldById.get(importId) ?? [];
    const newKeys = newById.get(importId) ?? [];
    for (const oldKey of oldKeys) {
      for (const newKey of newKeys) {
        if (oldKey !== newKey) {
          if (candidates.length >= MAX_IMPORT_MOVE_CANDIDATES) {
            return fail(
              "IMPORT_MOVE_LIMIT_EXCEEDED",
              "import rename candidates exceed the bounded derivation contract",
            );
          }
          candidates.push({ oldKey, newKey, importId });
        }
      }
    }
  }

  const fromCounts = new Map<string, number>();
  for (const candidate of candidates) {
    fromCounts.set(candidate.oldKey, (fromCounts.get(candidate.oldKey) ?? 0) + 1);
  }

  const moves: ImportMove[] = [];
  const suppressed: ImportMoveSuppression[] = [];
  for (const candidate of candidates) {
    let reason: ImportMoveSuppressionReason | null = null;
    if ((oldById.get(candidate.importId)?.length ?? 0) > 1) {
      reason = "ambiguous";
    } else if ((fromCounts.get(candidate.oldKey) ?? 0) > 1) {
      reason = "duplicate_from";
    } else if (
      isKeySwap(candidate.oldKey, candidate.newKey, oldPairs, newPairs)
    ) {
      reason = "key_swap";
    } else if (
      oldPairs.has(candidate.newKey)
      || newPairs.has(candidate.oldKey)
    ) {
      reason = "destination_occupied";
    }

    if (reason === null) {
      moves.push(Object.freeze({
        oldKey: candidate.oldKey,
        newKey: candidate.newKey,
      }));
    } else {
      suppressed.push(Object.freeze({ ...candidate, reason }));
    }
  }

  moves.sort(compareMoves);
  suppressed.sort(compareSuppressions);
  return Object.freeze({
    moves: Object.freeze(moves),
    suppressed: Object.freeze(suppressed),
  });
}

/** Match engine.transform.render_moves byte-for-byte for derived moves. */
export function renderMovedBlocks(
  resourceType: string,
  moves: readonly ImportMove[],
): string {
  requireResourceType(resourceType);
  if (!Array.isArray(moves)) {
    return fail(
      "INVALID_IMPORT_MOVES",
      "import moves must be an array of address pairs",
    );
  }
  if (moves.length > MAX_IMPORT_MOVE_CANDIDATES) {
    return fail(
      "IMPORT_MOVE_LIMIT_EXCEEDED",
      "import moves exceed the bounded render contract",
    );
  }
  const blocks = moves.map((move) => {
    if (
      typeof move !== "object"
      || move === null
      || Array.isArray(move)
      || typeof move.oldKey !== "string"
      || typeof move.newKey !== "string"
    ) {
      return fail(
        "INVALID_IMPORT_MOVES",
        "move address keys must be strings",
      );
    }
    return [
      "moved {",
      `  from = module.${resourceType}.${resourceType}.this[${renderHclQuotedString(move.oldKey)}]`,
      `  to   = module.${resourceType}.${resourceType}.this[${renderHclQuotedString(move.newKey)}]`,
      "}",
      "",
    ].join("\n");
  });
  return blocks.join("\n");
}
