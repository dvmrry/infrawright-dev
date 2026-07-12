import {
  UCD151_ONLY_CASE_IGNORABLE_RANGES,
  UCD16_ONLY_CASED_RANGES,
  UCD16_ONLY_CASE_IGNORABLE_RANGES,
  UCD16_ONLY_LOWERCASE_SOURCE_RANGES,
} from "../generated/python-lower-15.1.js";

const CASED = /\p{Cased}/u;
const CASE_IGNORABLE = /\p{Case_Ignorable}/u;
const CAPITAL_SIGMA = 0x03a3;
const SMALL_SIGMA = "\u03c3";
const FINAL_SIGMA = "\u03c2";

function inRanges(
  codePoint: number,
  ranges: readonly number[],
): boolean {
  let low = 0;
  let high = (ranges.length >>> 1) - 1;
  while (low <= high) {
    const middle = (low + high) >>> 1;
    const first = ranges[middle * 2];
    const last = ranges[middle * 2 + 1];
    if (first === undefined || last === undefined) {
      throw new TypeError("generated Unicode ranges are malformed");
    }
    if (codePoint < first) {
      high = middle - 1;
    } else if (codePoint > last) {
      low = middle + 1;
    } else {
      return true;
    }
  }
  return false;
}

function requireUnicode16(): void {
  if (process.versions.unicode !== "16.0") {
    throw new TypeError(
      "Python 15.1 lowercase compatibility requires the Node Unicode 16.0 runtime",
    );
  }
}

function isCaseIgnorable(character: string, codePoint: number): boolean {
  if (inRanges(codePoint, UCD16_ONLY_CASE_IGNORABLE_RANGES)) {
    return false;
  }
  if (inRanges(codePoint, UCD151_ONLY_CASE_IGNORABLE_RANGES)) {
    return true;
  }
  return CASE_IGNORABLE.test(character);
}

function isCased(character: string, codePoint: number): boolean {
  return !inRanges(codePoint, UCD16_ONLY_CASED_RANGES)
    && CASED.test(character);
}

function hasCasedBefore(
  characters: readonly string[],
  codePoints: readonly number[],
  index: number,
): boolean {
  for (let cursor = index - 1; cursor >= 0; cursor -= 1) {
    const character = characters[cursor];
    const codePoint = codePoints[cursor];
    if (character === undefined || codePoint === undefined) {
      throw new TypeError("lowercase context is incomplete");
    }
    // Unicode's Final_Sigma context permits Case_Ignorable* between Sigma
    // and the nearest significant character. Some points are both
    // Case_Ignorable and Cased, so this test must come first.
    if (isCaseIgnorable(character, codePoint)) {
      continue;
    }
    return isCased(character, codePoint);
  }
  return false;
}

function hasCasedAfter(
  characters: readonly string[],
  codePoints: readonly number[],
  index: number,
): boolean {
  for (let cursor = index + 1; cursor < characters.length; cursor += 1) {
    const character = characters[cursor];
    const codePoint = codePoints[cursor];
    if (character === undefined || codePoint === undefined) {
      throw new TypeError("lowercase context is incomplete");
    }
    if (isCaseIgnorable(character, codePoint)) {
      continue;
    }
    return isCased(character, codePoint);
  }
  return false;
}

/**
 * Match Python 3.12/3.13 `str.lower` (Unicode 15.0/15.1) on Node 24's
 * Unicode 16.0 runtime. This is an internal migration compatibility seam,
 * not a general Unicode case-mapping API.
 */
export function pythonLower151(value: string): string {
  requireUnicode16();
  const characters = Array.from(value);
  const codePoints = characters.map(
    (character) => character.codePointAt(0) ?? -1,
  );
  let output = "";
  for (let index = 0; index < characters.length; index += 1) {
    const character = characters[index];
    const codePoint = codePoints[index];
    if (character === undefined || codePoint === undefined) {
      throw new TypeError("lowercase input is incomplete");
    }
    if (codePoint === CAPITAL_SIGMA) {
      output += hasCasedBefore(characters, codePoints, index)
        && !hasCasedAfter(characters, codePoints, index)
        ? FINAL_SIGMA
        : SMALL_SIGMA;
    } else if (inRanges(codePoint, UCD16_ONLY_LOWERCASE_SOURCE_RANGES)) {
      output += character;
    } else {
      // Per-code-point conversion preserves all unconditional full mappings
      // (including U+0130 -> i + dot) without invoking Node's Unicode 16
      // Final_Sigma context. That context is implemented above against 15.1.
      output += character.toLowerCase();
    }
  }
  return output;
}
