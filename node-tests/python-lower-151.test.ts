import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import {
  PYTHON_LOWER_151_RUNTIME_DELTAS,
  PYTHON_LOWER_151_UCD_SOURCES,
} from "../node-src/generated/python-lower-15.1.js";
import { pythonLower151 } from "../node-src/json/python-lower-151.js";
import {
  snakeName,
  slugifyTransformKey,
} from "../node-src/domain/pull-transform.js";

const HASH_CONTRACT = "infrawright-python-lower-15.1-exhaustive-v1\0";
const HASH_VECTORS = [
  (character: string): string => character,
  (character: string): string => `A\u03a3${character}`,
  (character: string): string => `A\u03a3${character}A`,
  (character: string): string => `${character}\u03a3`,
  (character: string): string => `A${character}\u03a3`,
] as const;

const PYTHON_3_13_UCD_15_1_EXHAUSTIVE_DIGEST =
  "93acb44d32a0d2dffc6d8151c78420d4f35aea2764a74cfa939b315eb68f5db1";

function expanded(ranges: readonly number[]): number[] {
  const output: number[] = [];
  for (let index = 0; index < ranges.length; index += 2) {
    const first = ranges[index];
    const last = ranges[index + 1];
    assert.notEqual(first, undefined);
    assert.notEqual(last, undefined);
    for (
      let codePoint = first ?? 0;
      codePoint <= (last ?? -1);
      codePoint += 1
    ) {
      output.push(codePoint);
    }
  }
  return output;
}

function currentRuntimeDelta(): (
  typeof PYTHON_LOWER_151_RUNTIME_DELTAS
)[keyof typeof PYTHON_LOWER_151_RUNTIME_DELTAS] {
  const version = process.versions.unicode;
  assert.ok(version === "16.0" || version === "17.0");
  return PYTHON_LOWER_151_RUNTIME_DELTAS[version];
}

function nodeExhaustiveDigest(): string {
  const digest = createHash("sha256");
  const length = Buffer.allocUnsafe(4);
  digest.update(HASH_CONTRACT, "utf8");
  for (let codePoint = 0; codePoint <= 0x10ffff; codePoint += 1) {
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
      continue;
    }
    const character = String.fromCodePoint(codePoint);
    digest.update(codePoint.toString(16).padStart(6, "0"), "ascii");
    for (const vector of HASH_VECTORS) {
      const payload = Buffer.from(pythonLower151(vector(character)), "utf8");
      length.writeUInt32BE(payload.length);
      digest.update(length);
      digest.update(payload);
    }
  }
  return digest.digest("hex");
}

test("generated runtime deltas are closed over the reviewed Node 24 Unicode tables", () => {
  assert.deepEqual(Object.keys(PYTHON_LOWER_151_UCD_SOURCES), [
    "15.1.0",
    "16.0.0",
    "17.0.0",
  ]);
  assert.deepEqual(Object.keys(PYTHON_LOWER_151_RUNTIME_DELTAS), [
    "16.0",
    "17.0",
  ]);
  const unicode16 = PYTHON_LOWER_151_RUNTIME_DELTAS["16.0"];
  const unicode17 = PYTHON_LOWER_151_RUNTIME_DELTAS["17.0"];
  assert.deepEqual(unicode16.runtime_only_lowercase_source_ranges, [
    0x1c89, 0x1c89,
    0xa7cb, 0xa7cc,
    0xa7da, 0xa7da,
    0xa7dc, 0xa7dc,
    0x10d50, 0x10d65,
  ]);
  assert.deepEqual(unicode17.runtime_only_lowercase_source_ranges, [
    0x1c89, 0x1c89,
    0xa7cb, 0xa7cc,
    0xa7ce, 0xa7ce,
    0xa7d2, 0xa7d2,
    0xa7d4, 0xa7d4,
    0xa7da, 0xa7da,
    0xa7dc, 0xa7dc,
    0x10d50, 0x10d65,
    0x16ea0, 0x16eb8,
  ]);
  for (const [delta, expected] of [
    [unicode16, [27, 52, 0, 43]],
    [unicode17, [55, 107, 1, 88]],
  ] as const) {
    assert.equal(
      expanded(delta.runtime_only_lowercase_source_ranges).length,
      expected[0],
    );
    assert.deepEqual(delta.python_only_lowercase_source_ranges, []);
    assert.deepEqual(delta.changed_common_lowercase_source_ranges, []);
    assert.equal(expanded(delta.runtime_only_cased_ranges).length, expected[1]);
    assert.equal(expanded(delta.python_only_cased_ranges).length, expected[2]);
    assert.equal(
      expanded(delta.runtime_only_case_ignorable_ranges).length,
      expected[3],
    );
    assert.deepEqual(delta.python_only_case_ignorable_ranges, [
      0x1171e,
      0x1171e,
    ]);
  }
  assert.deepEqual(unicode16.python_only_cased_ranges, []);
  assert.deepEqual(unicode17.python_only_cased_ranges, [0x295, 0x295]);
});

test("Python lowercase preserves every direct mapping source added by this runtime", () => {
  const additions = expanded(
    currentRuntimeDelta().runtime_only_lowercase_source_ranges,
  );
  assert.ok(additions.includes(0xa7cb));
  for (const codePoint of additions) {
    const character = String.fromCodePoint(codePoint);
    assert.equal(pythonLower151(character), character, `U+${codePoint.toString(16)}`);
    assert.notEqual(
      character.toLowerCase(),
      character,
      `Node must expose the reviewed runtime delta at U+${codePoint.toString(16)}`,
    );
  }
  assert.equal(pythonLower151("\ua7ce"), "\ua7ce");
  assert.equal(pythonLower151("\u{16ea0}"), "\u{16ea0}");
  assert.equal(pythonLower151("\u0130"), "i\u0307");
});

test("Final Sigma uses Unicode 15.1 Cased and Case_Ignorable context", () => {
  for (const [input, expected] of [
    ["\u1c89\u03a3", "\u1c89\u03c3"],
    ["A\u03a3\u1c89", "a\u03c2\u1c89"],
    ["A\u0897\u03a3", "a\u0897\u03c3"],
    ["A\u03a3\u0897A", "a\u03c2\u0897a"],
    ["\ua7ce\u03a3", "\ua7ce\u03c3"],
    ["A\u03a3\ua7ce", "a\u03c2\ua7ce"],
    ["\u0295\u03a3", "\u0295\u03c2"],
    ["A\u03a3\u0295", "a\u03c3\u0295"],
    ["A\u1acf\u03a3", "a\u1acf\u03c3"],
    ["A\u03a3\u1acfA", "a\u03c2\u1acfa"],
    ["A\u{1171e}\u03a3", "a\u{1171e}\u03c2"],
    ["A\u03a3\u{1171e}A", "a\u03c3\u{1171e}a"],
  ] as const) {
    assert.equal(pythonLower151(input), expected);
  }

  // U+02B0 is both Cased and Case_Ignorable. Final_Sigma must ignore it
  // before considering whether the next significant character is Cased.
  assert.equal(pythonLower151("1\u02b0\u03a3"), "1\u02b0\u03c3");
  assert.equal(pythonLower151("A\u03a3\u02b01A"), "a\u03c2\u02b01a");
});

test("snake regex matches Python dot boundaries and Unicode code points", () => {
  for (const [input, expected] of [
    ["\rFoo", "\r_foo"],
    ["\nFoo", "\nfoo"],
    ["\u2028Foo", "\u2028_foo"],
    ["\u2029Foo", "\u2029_foo"],
    ["\u{1f600}Foo", "\u{1f600}_foo"],
    ["\ud800Foo", "\ud800_foo"],
  ] as const) {
    assert.equal(snakeName(input), expected);
  }
  assert.equal(snakeName("\ua7cbName"), "\ua7cb_name");
});

test("slug output and collisions retain Python expansion behavior", () => {
  assert.equal(slugifyTransformKey("\u0130"), "i");
  assert.equal(slugifyTransformKey("A\u03a3C"), "a_c");
  assert.equal(slugifyTransformKey("\ua7cb"), "");
});

test("lowercase helper rejects Node Unicode runtime drift", () => {
  const descriptor = Object.getOwnPropertyDescriptor(process.versions, "unicode");
  assert.notEqual(descriptor, undefined);
  try {
    Object.defineProperty(process.versions, "unicode", {
      ...descriptor,
      value: "18.0",
    });
    assert.throws(
      () => pythonLower151("A"),
      /requires a reviewed Node Unicode 16\.0 or 17\.0 runtime delta/,
    );
  } finally {
    Object.defineProperty(process.versions, "unicode", descriptor ?? {});
  }
  assert.equal(pythonLower151("A"), "a");
});

test("all Unicode scalars match the frozen CPython 3.13 UCD 15.1 contract", {
  timeout: 120_000,
}, () => {
  const actual = nodeExhaustiveDigest();
  assert.equal(actual, PYTHON_3_13_UCD_15_1_EXHAUSTIVE_DIGEST);
});
