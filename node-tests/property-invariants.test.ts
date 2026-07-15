import assert from "node:assert/strict";
import test from "node:test";

import fc from "fast-check";

import {
  CliArgumentParseError,
  parseCommandArguments,
} from "../node-src/cli/arguments.js";
import { decodeLocalRefToken } from "../node-src/authoring/openapi.js";
import {
  parseGeneratedImports,
  renderGeneratedImports,
} from "../node-src/domain/import-moves.js";

const PROPERTY_OPTIONS = Object.freeze({ numRuns: 500, seed: 0x1f4a_2026 });
const safeText = fc.string({ maxLength: 64 }).filter((value) => {
  return !value.includes("\0") && value.isWellFormed();
});

test("parseArgs adapter preserves repeatable string values exactly and in order", () => {
  fc.assert(fc.property(
    fc.array(safeText, { maxLength: 24 }),
    (values) => {
      const arguments_ = values.flatMap((value) => ["--value", value]);
      const parsed = parseCommandArguments(arguments_, {
        values: { "--value": { allowEmpty: true, multiple: true } },
      });
      assert.deepEqual(parsed.options["--value"] ?? [], values);
    },
  ), PROPERTY_OPTIONS);
});

test("parseArgs adapter rejects repeated singleton options", () => {
  fc.assert(fc.property(safeText, safeText, (first, second) => {
    assert.throws(
      () => parseCommandArguments(["--value", first, "--value", second], {
        values: { "--value": { multiple: false } },
      }),
      (error: unknown) => error instanceof CliArgumentParseError
        && error.message === "--value may be specified only once",
    );
  }), PROPERTY_OPTIONS);
});

test("parseArgs adapter preserves legacy diagnostics and order-aware help", () => {
  assert.throws(
    () => parseCommandArguments(["--value"], { values: { "--value": {} } }),
    (error: unknown) => error instanceof CliArgumentParseError
      && error.message === "--value requires a value",
  );
  for (const arguments_ of [
    ["-x"],
    ["-xyz"],
    ["-hh"],
    ["unexpected"],
    ["--"],
    ["--value=inline"],
  ]) {
    assert.throws(
      () => parseCommandArguments(arguments_, {}),
      (error: unknown) => error instanceof CliArgumentParseError
        && error.message === `unknown argument ${arguments_[0]}`,
    );
  }
  const help = parseCommandArguments(["--help", "--unknown"], {});
  assert.equal(help.flags.has("--help"), true);
  const value = parseCommandArguments(["--value", "--help"], {
    values: { "--value": {} },
  });
  assert.equal(value.flags.has("--help"), false);
  assert.deepEqual(value.options["--value"], ["--help"]);
  const inline = parseCommandArguments(["--order=references"], {
    values: { "--order": { allowedValues: ["references"], inlineOnly: true } },
  });
  assert.deepEqual(inline.options["--order"], ["references"]);
  assert.throws(
    () => parseCommandArguments(["--order", "references"], {
      values: { "--order": { allowedValues: ["references"], inlineOnly: true } },
    }),
    (error: unknown) => error instanceof CliArgumentParseError
      && error.message === "unknown argument --order",
  );
  assert.throws(
    () => parseCommandArguments(["--order=bad", "--help"], {
      values: { "--order": { allowedValues: ["references"], inlineOnly: true } },
    }),
    (error: unknown) => error instanceof CliArgumentParseError
      && error.message === "unknown argument --order=bad",
  );
});

test("parseArgs adapter retains mixed option and positional occurrence order", () => {
  const parsed = parseCommandArguments([
    "PACK=first",
    "--pack", "second",
    "PACK=third",
  ], {
    allowPositionals: true,
    values: { "--pack": {} },
  });
  assert.deepEqual(parsed.occurrences, [
    { kind: "positional", value: "PACK=first" },
    { kind: "option", name: "--pack", value: "second" },
    { kind: "positional", value: "PACK=third" },
  ]);
});

test("JSON Pointer local-ref token decoding reverses RFC 6901 escaping", () => {
  fc.assert(fc.property(safeText, (value) => {
    const encoded = value.replaceAll("~", "~0").replaceAll("/", "~1");
    assert.equal(decodeLocalRefToken(encoded), value);
  }), PROPERTY_OPTIONS);
});

test("canonical generated imports round-trip arbitrary safe HCL strings", () => {
  const pairs = fc.uniqueArray(
    fc.record({ importId: safeText, key: safeText }),
    { maxLength: 40, selector: (pair) => pair.key },
  );
  fc.assert(fc.property(pairs, (input) => {
    const rendered = renderGeneratedImports("zia_property_fixture", input);
    const parsed = parseGeneratedImports("zia_property_fixture", rendered);
    assert.equal(renderGeneratedImports("zia_property_fixture", parsed), rendered);
    assert.deepEqual(
      new Map(parsed.map((pair) => [pair.key, pair.importId])),
      new Map(input.map((pair) => [pair.key, pair.importId])),
    );
  }), PROPERTY_OPTIONS);
});
