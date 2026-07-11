import type { TransformHtmlUnescapeCompatibility } from "./transform-catalog.js";

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
