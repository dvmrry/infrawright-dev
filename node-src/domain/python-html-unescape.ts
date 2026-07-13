import type { TransformHtmlUnescapeCompatibility } from "./transform-catalog.js";
import { decodeHTML } from "entities";

const CHARACTER_REFERENCE = /&(#[0-9]+;?|#[xX][0-9a-fA-F]+;?|[^\t\n\f <&#;]{1,32};?)/g;

function hasOwn(record: Readonly<Record<string, string>>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function replaceCharacterReference(
  reference: string,
  tables: TransformHtmlUnescapeCompatibility,
  invalidCodepoints: ReadonlySet<number>,
): string {
  if (reference.startsWith("#")) {
    const hexadecimal = reference[1] === "x" || reference[1] === "X";
    const digits = reference
      .slice(hexadecimal ? 2 : 1)
      .replace(/;$/, "");
    const codepoint = BigInt(`${hexadecimal ? "0x" : ""}${digits}`);
    const decimal = codepoint.toString(10);
    if (hasOwn(tables.invalid_references, decimal)) {
      return tables.invalid_references[decimal] ?? "";
    }
    if (
      (codepoint >= 0xd800n && codepoint <= 0xdfffn)
      || codepoint > 0x10ffffn
    ) {
      return "\ufffd";
    }
    const safeCodepoint = Number(codepoint);
    if (invalidCodepoints.has(safeCodepoint)) {
      return "";
    }
    return String.fromCodePoint(safeCodepoint);
  }

  const exact = tables.entities[reference];
  if (hasOwn(tables.entities, reference) && exact !== undefined) {
    return exact;
  }
  for (let length = reference.length - 1; length > 1; length -= 1) {
    const prefix = reference.slice(0, length);
    const value = tables.entities[prefix];
    if (hasOwn(tables.entities, prefix) && value !== undefined) {
      return `${value}${reference.slice(length)}`;
    }
  }
  return `&${reference}`;
}

/** Match Python's html.unescape using the exact serialized stdlib tables. */
export function pythonHtmlUnescape(
  value: string,
  tables: TransformHtmlUnescapeCompatibility,
): string {
  if (!value.includes("&")) {
    return value;
  }
  const invalidCodepoints = new Set(tables.invalid_codepoints);
  return value.replace(CHARACTER_REFERENCE, (_match, reference: string) => {
    return replaceCharacterReference(reference, tables, invalidCodepoints);
  });
}

export function pythonHtmlUnescapePasses(
  value: string,
  passes: 0 | 2,
  tables: TransformHtmlUnescapeCompatibility,
): string {
  let transformed = value;
  for (let index = 0; index < passes; index += 1) {
    transformed = pythonHtmlUnescape(transformed, tables);
  }
  return transformed;
}

function pythonInvalidCodepoint(codepoint: bigint): boolean {
  return (codepoint >= 1n && codepoint <= 8n)
    || codepoint === 11n
    || (codepoint >= 14n && codepoint <= 31n)
    || codepoint === 127n
    || (codepoint >= 0xfdd0n && codepoint <= 0xfdefn)
    || (codepoint >= 0xfffen && (codepoint & 0xffffn) >= 0xfffen);
}

/** Python html.unescape without sourcing tables from a product transition catalog. */
export function pythonHtmlUnescapeGeneric(value: string): string {
  if (!value.includes("&")) return value;
  return value.replace(CHARACTER_REFERENCE, (matched, reference: string) => {
    if (!reference.startsWith("#")) return decodeHTML(matched);
    const hexadecimal = reference[1] === "x" || reference[1] === "X";
    const digits = reference.slice(hexadecimal ? 2 : 1).replace(/;$/u, "");
    const codepoint = BigInt(`${hexadecimal ? "0x" : ""}${digits}`);
    // WHATWG replacements take precedence over Python's invalid-codepoint set.
    if (codepoint === 0n || codepoint === 13n || (codepoint >= 128n && codepoint <= 159n)) {
      return decodeHTML(matched);
    }
    if (pythonInvalidCodepoint(codepoint)) return "";
    if (
      (codepoint >= 0xd800n && codepoint <= 0xdfffn)
      || codepoint > 0x10ffffn
    ) {
      return "\ufffd";
    }
    return String.fromCodePoint(Number(codepoint));
  });
}
