import { decodeHTML } from "entities";

const CHARACTER_REFERENCE = /&(#[0-9]+;?|#[xX][0-9a-fA-F]+;?|[^\t\n\f <&#;]{1,32};?)/g;

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
