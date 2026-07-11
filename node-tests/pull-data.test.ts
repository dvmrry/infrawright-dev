import assert from "node:assert/strict";
import test from "node:test";

import { stringify as stringifyLosslessly } from "lossless-json";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { parseZccPullDataJson } from "../node-src/json/zcc-pull-data.js";

function captureFailure(run: () => unknown): ProcessFailure {
  let failure: unknown;
  try {
    run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof ProcessFailure);
  assert.equal(failure.category, "domain");
  return failure;
}

test("ZCC pull parser preserves all provider numeric lexemes", () => {
  const source = [
    "[",
    "{",
    '"huge":900719925474099312345678901234567890,',
    '"negative_zero":-0,',
    '"decimal":1.2500,',
    '"exponent":1e+20',
    "}",
    "]",
  ].join("");
  assert.equal(stringifyLosslessly(parseZccPullDataJson(source)), source);
  assert.deepEqual(parseZccPullDataJson("[]"), []);
});

test("malformed, duplicate-key, and non-array input has value-free errors", () => {
  const invalid = [
    "not-private-input",
    '{"private-key":1}',
    '[{"private-key":1,"private-key":2}]',
    '[{"private-key":"unterminated}]',
  ];
  for (const source of invalid) {
    const failure = captureFailure(() => parseZccPullDataJson(source));
    assert.equal(failure.code, "INVALID_PULL_DATA_JSON");
    assert.equal(failure.message.includes("private"), false);
  }
});

test("pull nesting is accepted through 128 levels and rejected at 129", () => {
  const accepted = `${"[".repeat(128)}0${"]".repeat(128)}`;
  assert.doesNotThrow(() => parseZccPullDataJson(accepted));

  const rejected = `${"[".repeat(129)}0${"]".repeat(129)}`;
  const failure = captureFailure(() => parseZccPullDataJson(rejected));
  assert.equal(failure.code, "PULL_DATA_COMPLEXITY_LIMIT");
});

test("top-level pull item count is bounded before parsing", () => {
  const accepted = `[${Array.from({ length: 50_000 }, () => "0").join(",")}]`;
  assert.equal(parseZccPullDataJson(accepted).length, 50_000);

  const rejected = `[${Array.from({ length: 50_001 }, () => "0").join(",")}]`;
  const failure = captureFailure(() => parseZccPullDataJson(rejected));
  assert.equal(failure.code, "PULL_DATA_COMPLEXITY_LIMIT");
});

test("individual string and numeric tokens have exact bounded ceilings", () => {
  const acceptedString = "a".repeat(1024 * 1024);
  const parsedString = parseZccPullDataJson(`["${acceptedString}"]`);
  assert.equal(parsedString[0], acceptedString);

  const stringFailure = captureFailure(() => {
    parseZccPullDataJson(`["${acceptedString}a"]`);
  });
  assert.equal(stringFailure.code, "PULL_DATA_COMPLEXITY_LIMIT");

  const acceptedNumber = "1".repeat(1024);
  assert.equal(
    stringifyLosslessly(parseZccPullDataJson(`[${acceptedNumber}]`)),
    `[${acceptedNumber}]`,
  );
  const numberFailure = captureFailure(() => {
    parseZccPullDataJson(`[${acceptedNumber}1]`);
  });
  assert.equal(numberFailure.code, "PULL_DATA_COMPLEXITY_LIMIT");
});

test("total lexical token count is bounded before graph construction", () => {
  const nestedItems = Array.from({ length: 125_001 }, () => "0").join(",");
  const failure = captureFailure(() => {
    parseZccPullDataJson(`[[${nestedItems}]]`);
  });
  assert.equal(failure.code, "PULL_DATA_COMPLEXITY_LIMIT");
});
