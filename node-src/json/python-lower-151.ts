import {
  PYTHON_LOWER_151_RUNTIME_DELTAS,
} from "../generated/python-lower-15.1.js";

const CASED = /\p{Cased}/u;
const CASE_IGNORABLE = /\p{Case_Ignorable}/u;
const CAPITAL_SIGMA = 0x03a3;
const SMALL_SIGMA = "\u03c3";
const FINAL_SIGMA = "\u03c2";

interface PythonLowerRuntimeDelta {
  readonly changed_common_lowercase_source_ranges: readonly number[];
  readonly python_only_cased_ranges: readonly number[];
  readonly python_only_case_ignorable_ranges: readonly number[];
  readonly python_only_lowercase_source_ranges: readonly number[];
  readonly runtime_only_cased_ranges: readonly number[];
  readonly runtime_only_case_ignorable_ranges: readonly number[];
  readonly runtime_only_lowercase_source_ranges: readonly number[];
  readonly runtime_ucd_version: string;
}

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

function requireRuntimeDelta(): PythonLowerRuntimeDelta {
  const version = process.versions.unicode;
  if (version === "16.0" || version === "17.0") {
    const delta = PYTHON_LOWER_151_RUNTIME_DELTAS[version];
    const expectedUcd = version === "16.0" ? "16.0.0" : "17.0.0";
    if (
      delta.runtime_ucd_version !== expectedUcd
      || delta.python_only_lowercase_source_ranges.length !== 0
      || delta.changed_common_lowercase_source_ranges.length !== 0
    ) {
      throw new TypeError("generated Python lowercase runtime delta is unsupported");
    }
    return delta;
  }
  throw new TypeError(
    "Python 15.1 lowercase compatibility requires a reviewed Node Unicode 16.0 or 17.0 runtime delta",
  );
}

function isCaseIgnorable(
  character: string,
  codePoint: number,
  delta: PythonLowerRuntimeDelta,
): boolean {
  if (inRanges(codePoint, delta.runtime_only_case_ignorable_ranges)) {
    return false;
  }
  if (inRanges(codePoint, delta.python_only_case_ignorable_ranges)) {
    return true;
  }
  return CASE_IGNORABLE.test(character);
}

function isCased(
  character: string,
  codePoint: number,
  delta: PythonLowerRuntimeDelta,
): boolean {
  if (inRanges(codePoint, delta.runtime_only_cased_ranges)) {
    return false;
  }
  if (inRanges(codePoint, delta.python_only_cased_ranges)) {
    return true;
  }
  return CASED.test(character);
}

function hasCasedBefore(
  characters: readonly string[],
  codePoints: readonly number[],
  index: number,
  delta: PythonLowerRuntimeDelta,
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
    if (isCaseIgnorable(character, codePoint, delta)) {
      continue;
    }
    return isCased(character, codePoint, delta);
  }
  return false;
}

function hasCasedAfter(
  characters: readonly string[],
  codePoints: readonly number[],
  index: number,
  delta: PythonLowerRuntimeDelta,
): boolean {
  for (let cursor = index + 1; cursor < characters.length; cursor += 1) {
    const character = characters[cursor];
    const codePoint = codePoints[cursor];
    if (character === undefined || codePoint === undefined) {
      throw new TypeError("lowercase context is incomplete");
    }
    if (isCaseIgnorable(character, codePoint, delta)) {
      continue;
    }
    return isCased(character, codePoint, delta);
  }
  return false;
}

/**
 * Match Python 3.12/3.13 `str.lower` (Unicode 15.0/15.1) on the explicitly
 * reviewed Unicode 16.0 and 17.0 runtimes shipped by Node 24. This is an
 * internal migration compatibility seam, not a general Unicode case-mapping
 * API.
 */
export function pythonLower151(value: string): string {
  const delta = requireRuntimeDelta();
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
      output += hasCasedBefore(characters, codePoints, index, delta)
        && !hasCasedAfter(characters, codePoints, index, delta)
        ? FINAL_SIGMA
        : SMALL_SIGMA;
    } else if (
      inRanges(codePoint, delta.runtime_only_lowercase_source_ranges)
    ) {
      output += character;
    } else {
      // Per-code-point conversion preserves all unconditional full mappings
      // (including U+0130 -> i + dot) without invoking the runtime's newer
      // Final_Sigma context. That context is implemented above against 15.1.
      output += character.toLowerCase();
    }
  }
  return output;
}
