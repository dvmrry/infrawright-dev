import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { stringify as stringifyLosslessly } from "lossless-json";

import {
  parseControlJson,
  parseDataJsonLosslessly,
} from "../node-src/json/control.js";
import {
  renderPythonCompatibleJson,
  type JsonValue,
} from "../node-src/json/python-compatible.js";

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
    "python3",
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

test("initial compatibility renderer refuses floats instead of changing bytes", () => {
  assert.throws(
    () => renderPythonCompatibleJson({ value: 1.0 / 2 } as JsonValue),
    /safe integers only/,
  );
  assert.throws(
    () => renderPythonCompatibleJson({ value: -0 } as JsonValue),
    /safe integers only/,
  );
});
