import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { LosslessNumber, stringify as stringifyLosslessly } from "lossless-json";

import {
  parseControlJson,
  parseDataJsonLosslessly,
  PythonJsonDecodeError,
} from "../node-src/json/control.js";
import { readJson } from "../node-src/metadata/validation.js";
import {
  comparePythonStrings,
  pythonCompatibleJsonByteLength,
  renderPythonCompatibleJson,
  sameStringSequence,
  sortedStrings,
  type JsonValue,
} from "../node-src/json/python-compatible.js";
import {
  terraformJsonEqual,
  terraformJsonExactlyEqual,
} from "../node-src/json/python-equality.js";
import { snapshotPlainJsonGraph } from "../node-src/json/supported-json-graph.js";

test("shared Python string semantics preserve exact sequence and code-point order", () => {
  assert.equal(sameStringSequence(["a", "a", "b"], ["a", "a", "b"]), true);
  assert.equal(sameStringSequence(["a"], ["a", "b"]), false);
  assert.equal(sameStringSequence(["a", "b"], ["b", "a"]), false);
  assert.equal(sameStringSequence(["a", "a"], ["a", "b"]), false);
  assert.ok(comparePythonStrings("\ue000", "\u{10000}") < 0);
  assert.ok(comparePythonStrings("\u{10000}", "\ue000") > 0);
});

test("integer-only compatibility renderer matches Python bytes", () => {
  const value = {
    "2": "two",
    "10": "ten",
    ascii: "é/\\\"\n",
    astral: "😀",
    bmp: "",
    nested: [true, null, 9007199254740991],
  } as const;
  // Exact json.dumps bytes frozen at archive baseline 7d54261c. Do not replace
  // this byte contract with parsed-JSON equality.
  const expected = String.raw`{
  "10": "ten",
  "2": "two",
  "ascii": "\u00e9/\\\"\n",
  "astral": "\ud83d\ude00",
  "bmp": "\ue000",
  "nested": [
    true,
    null,
    9007199254740991
  ]
}
`;
  assert.equal(
    renderPythonCompatibleJson(value as unknown as JsonValue),
    expected,
  );
  assert.ok(expected.indexOf('"10"') < expected.indexOf('"2"'));
  assert.match(expected, /\\u00e9/);
  assert.match(expected, /\\ud83d\\ude00/);
  assert.equal(
    pythonCompatibleJsonByteLength(value as unknown as JsonValue),
    Buffer.byteLength(expected, "utf8"),
  );
  assert.equal(
    pythonCompatibleJsonByteLength(
      value as unknown as JsonValue,
      Buffer.byteLength(expected, "utf8"),
    ),
    Buffer.byteLength(expected, "utf8"),
  );
  assert.equal(
    pythonCompatibleJsonByteLength(
      value as unknown as JsonValue,
      Buffer.byteLength(expected, "utf8") - 1,
    ),
    Buffer.byteLength(expected, "utf8"),
  );
});

test("control parser rejects duplicate keys and unsafe integers", () => {
  assert.throws(() => parseControlJson('{"a":1,"a":2}'));
  assert.throws(() => parseControlJson('{"a":1,"a":1}'));
  assert.throws(() => parseControlJson('{"a":1,"\\u0061":1}'));
  assert.throws(
    () => parseControlJson(
      '{"__proto__":{"first":1},"__proto__":{"second":2}}',
    ),
  );
  assert.throws(() => parseControlJson('{"id":9007199254740993}'));
  assert.deepEqual(parseControlJson('{"id":9007199254740991}'), {
    id: 9007199254740991,
  });
});

test("JSON parsers reject unpaired UTF-16 surrogates with raw-token offsets", () => {
  const rejected = [
    { name: "high value", source: '{"value":"\\ud800"}', position: 10 },
    { name: "low value", source: '{"value":"\\udfff"}', position: 10 },
    { name: "high key", source: '{"\\ud800":true}', position: 2 },
    { name: "low key", source: '{"\\udfff":true}', position: 2 },
    { name: "adjacent highs", source: '{"value":"\\ud800\\ud800"}', position: 10 },
    { name: "high before scalar", source: '{"value":"\\ud800x"}', position: 10 },
    { name: "high key before replacement key", source: '{"\\ud800":1,"�":2}', position: 2 },
    { name: "high key before low key", source: '{"\\ud800":1,"\\udfff":2}', position: 2 },
    { name: "repeated high keys", source: '{"\\ud800":1,"\\ud800":2}', position: 2 },
  ];
  const standardEscapes = [
    String.raw`\"`, String.raw`\\`, String.raw`\/`, String.raw`\b`,
    String.raw`\f`, String.raw`\n`, String.raw`\r`, String.raw`\t`,
  ];
  for (const escape of standardEscapes) {
    rejected.push(
      { name: `high value before ${escape}`, source: String.raw`{"value":"\ud800${escape}\udc00"}`, position: 10 },
      { name: `high key before ${escape}`, source: String.raw`{"\ud800${escape}\udc00":true}`, position: 2 },
    );
  }
  for (const parser of [parseControlJson, parseDataJsonLosslessly]) {
    for (const vector of rejected) {
      assert.throws(
        () => parser(vector.source),
        (error: unknown) => {
          assert.ok(error instanceof PythonJsonDecodeError, vector.name);
          assert.equal(error.message, `Unpaired UTF-16 surrogate: line 1 column ${vector.position + 1} (char ${vector.position})`);
          assert.equal(error.position, vector.position);
          return true;
        },
      );
    }
  }
});

test("JSON parsers retain valid UTF-16 pairs, astral text, and replacement text", () => {
  for (const parser of [parseControlJson, parseDataJsonLosslessly]) {
    assert.doesNotThrow(() => parser('{"value":"\\ud83d\\ude00"}'));
    assert.doesNotThrow(() => parser('{"value":"😀"}'));
    assert.doesNotThrow(() => parser('{"value":"�"}'));
    assert.doesNotThrow(() => parser(`{"${"\ud800"}\\ude00":true}`));
    assert.doesNotThrow(() => parser(`{"\\ud800${"\udc00"}":true}`));
  }
});

test("control parsers retain structural errors ahead of recorded surrogates", () => {
  const cases = [
    { source: String.raw`{"value":"\ud800",}`, reason: "Expecting property name enclosed in double quotes", position: 18 },
    { source: String.raw`{"value":"\ud800" "next":1}`, reason: "Expecting ',' delimiter", position: 18 },
    { source: String.raw`{"value":"\ud800"} garbage`, reason: "Extra data", position: 19 },
    { source: String.raw`{"value":"\ud800","next":?}`, reason: "Expecting value", position: 25 },
  ] as const;
  for (const parser of [parseControlJson, parseDataJsonLosslessly]) {
    for (const vector of cases) {
      assert.throws(() => parser(vector.source), (error: unknown) => {
        assert.ok(error instanceof PythonJsonDecodeError);
        assert.equal(error.position, vector.position);
        assert.match(error.message, new RegExp(`^${vector.reason}:`));
        return true;
      });
    }
  }
});

test("metadata parsing rejects escape-separated surrogates only after JSON.parse", async (context) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-surrogate-"));
  context.after(async () => rm(root, { force: true, recursive: true }));
  const standardEscapes = [
    String.raw`\"`, String.raw`\\`, String.raw`\/`, String.raw`\b`,
    String.raw`\f`, String.raw`\n`, String.raw`\r`, String.raw`\t`,
  ];
  for (const [index, escape] of standardEscapes.entries()) {
    for (const source of [
      String.raw`{"value":"\ud800${escape}\udc00"}`,
      String.raw`{"\ud800${escape}\udc00":true}`,
    ]) {
      const file = path.join(root, `${index}-${source.includes("value") ? "value" : "key"}.json`);
      await writeFile(file, source, "utf8");
      await assert.rejects(readJson(file), /Unpaired UTF-16 surrogate: line 1/);
    }
  }
  const syntaxFile = path.join(root, "syntax.json");
  await writeFile(syntaxFile, String.raw`{"value":"\ud800","next":?}`, "utf8");
  await assert.rejects(readJson(syntaxFile), (error: unknown) => {
    return error instanceof Error && !error.message.includes("Unpaired UTF-16 surrogate");
  });
});

test("surrogate positions retain UTF-16 offsets across astral and multiline input", () => {
  const astral = `{"😀":"\\ud800"}`;
  const multiline = "{\n\"value\":\"\\ud800\"}";
  for (const parser of [parseControlJson, parseDataJsonLosslessly]) {
    assert.throws(() => parser(astral), (error: unknown) => {
      assert.ok(error instanceof PythonJsonDecodeError);
      assert.equal(error.position, 7);
      return true;
    });
    assert.throws(() => parser(multiline), (error: unknown) => {
      assert.ok(error instanceof PythonJsonDecodeError);
      assert.equal(error.message, "Unpaired UTF-16 surrogate: line 2 column 10 (char 11)");
      return true;
    });
  }
});

test("JSON parsers reject adversarial nesting before recursive parsing", () => {
  const nested = `${"[".repeat(129)}0${"]".repeat(129)}`;
  assert.throws(() => parseControlJson(nested), /nesting exceeds/);
  assert.throws(() => parseDataJsonLosslessly(nested), /nesting exceeds/);
  assert.doesNotThrow(() => parseControlJson('{"text":"[[[{{{"}'));
});

test("data parser preserves numeric lexemes beyond JavaScript precision", () => {
  const source = "{\"a\":9007199254740992,\"b\":9007199254740993,\"f\":-0.0}";
  const parsed = parseDataJsonLosslessly(source);
  assert.equal(stringifyLosslessly(parsed), source);
  assert.throws(() => parseDataJsonLosslessly('{"a":1,"a":1}'));
  assert.throws(
    () => parseDataJsonLosslessly(
      '{"__proto__":{"first":1},"__proto__":{"second":2}}',
    ),
  );
});

test("exact Terraform evidence equality avoids binary rounding", () => {
  const values = parseDataJsonLosslessly(
    "[1,1.0,10e-1,0.10e1,9007199254740992.0,9007199254740993.0,1e100000,10e99999]",
  ) as readonly unknown[];
  assert.equal(terraformJsonExactlyEqual(values[0], values[1]), true);
  assert.equal(terraformJsonExactlyEqual(values[1], values[2]), true);
  assert.equal(terraformJsonExactlyEqual(values[2], values[3]), true);
  assert.equal(terraformJsonExactlyEqual(values[4], values[5]), false);
  assert.equal(terraformJsonExactlyEqual(values[6], values[7]), true);
  assert.equal(terraformJsonExactlyEqual(true, values[0]), false);

  // Existing parity and plan-classification callers retain Python's numeric
  // equality contract; only the accepted-plan authorization gate is exact.
  assert.equal(terraformJsonEqual(values[4], values[5]), true);
});

test("compatibility renderer preserves Python float spelling and numeric tokens", () => {
  assert.equal(
    renderPythonCompatibleJson({ value: 1.0 / 2 } as JsonValue),
    "{\n  \"value\": 0.5\n}\n",
  );
  assert.equal(
    renderPythonCompatibleJson({ value: -0 } as JsonValue),
    "{\n  \"value\": -0.0\n}\n",
  );
  assert.equal(
    renderPythonCompatibleJson({ value: 1e-6 } as JsonValue),
    "{\n  \"value\": 1e-06\n}\n",
  );
  assert.equal(
    renderPythonCompatibleJson({ value: 1e20 } as JsonValue),
    "{\n  \"value\": 1e+20\n}\n",
  );
  assert.equal(
    renderPythonCompatibleJson({
      value: new LosslessNumber("1.0"),
    } as JsonValue),
    "{\n  \"value\": 1.0\n}\n",
  );
});

test("Python string ordering handles a large common-prefix set without sort-key retention", () => {
  const prefix = "😀".repeat(120);
  const values = Array.from({ length: 25_000 }, (_, index) => {
    return `${prefix}${String(24_999 - index).padStart(5, "0")}`;
  });
  const sorted = sortedStrings(values);
  assert.equal(sorted[0], `${prefix}00000`);
  assert.equal(sorted.at(-1), `${prefix}24999`);
});

test("plain JSON snapshots enforce numeric-token and wide-record budgets", () => {
  const numeric = new LosslessNumber("9".repeat(100_000));
  assert.deepEqual(snapshotPlainJsonGraph([numeric, numeric], {
    maxDepth: 16,
    maxNodes: 512,
    maxProperties: 512,
    maxStringBytes: 8,
  }), { ok: false });
  const wide = Object.fromEntries(Array.from({ length: 513 }, (_, index) => {
    return [`key_${index}`, null];
  }));
  assert.deepEqual(snapshotPlainJsonGraph(wide, {
    maxDepth: 16,
    maxNodes: 512,
    maxProperties: 512,
    maxStringBytes: 1024 * 1024,
  }), { ok: false });
});
