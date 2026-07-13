import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { LosslessNumber, stringify as stringifyLosslessly } from "lossless-json";

import {
  parseControlJson,
  parseDataJsonLosslessly,
} from "../node-src/json/control.js";
import {
  comparePythonStrings,
  pythonCompatibleJsonByteLength,
  renderPythonCompatibleJson,
  sameStringSequence,
  sortedStrings,
  type JsonValue,
} from "../node-src/json/python-compatible.js";
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
  const python = spawnSync(
    PYTHON_ORACLE,
    [
      "-c",
      "import json,sys; value=json.loads(sys.stdin.read()); sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
    ],
    { input: JSON.stringify(value), encoding: "utf8" },
  );
  assert.equal(python.status, 0, python.stderr);
  assert.equal(
    renderPythonCompatibleJson(value as unknown as JsonValue),
    python.stdout,
  );
  assert.ok(python.stdout.indexOf('"10"') < python.stdout.indexOf('"2"'));
  assert.match(python.stdout, /\\u00e9/);
  assert.match(python.stdout, /\\ud83d\\ude00/);
  assert.equal(
    pythonCompatibleJsonByteLength(value as unknown as JsonValue),
    Buffer.byteLength(python.stdout, "utf8"),
  );
  assert.equal(
    pythonCompatibleJsonByteLength(
      value as unknown as JsonValue,
      Buffer.byteLength(python.stdout, "utf8"),
    ),
    Buffer.byteLength(python.stdout, "utf8"),
  );
  assert.equal(
    pythonCompatibleJsonByteLength(
      value as unknown as JsonValue,
      Buffer.byteLength(python.stdout, "utf8") - 1,
    ),
    Buffer.byteLength(python.stdout, "utf8"),
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
