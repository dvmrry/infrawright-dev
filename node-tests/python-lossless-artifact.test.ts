import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";
import {
  renderPythonLosslessArtifactJson,
} from "../node-src/json/python-lossless-artifact.js";

function expectInvalid(run: () => unknown, secret?: string): ProcessFailure {
  let failure: unknown;
  try {
    run();
  } catch (error: unknown) {
    failure = error;
  }
  assert.ok(failure instanceof ProcessFailure);
  assert.equal(failure.code, "INVALID_ARTIFACT_JSON");
  assert.equal(failure.category, "domain");
  if (secret !== undefined) {
    assert.equal(failure.message.includes(secret), false);
  }
  return failure;
}

test("lossless artifact renderer matches Python bytes for integer JSON", () => {
  const source = [
    "{",
    '"2":"two",',
    '"10":"ten",',
    '"ascii":"é/\\\\\\\"\\n",',
    '"astral":"😀",',
    '"bmp":"",',
    '"huge":900719925474099312345678901234567890,',
    '"negative_zero":-0,',
    '"nested":[true,null,9007199254740991]',
    "}",
  ].join("");
  const python = spawnSync(
    PYTHON_ORACLE,
    [
      "-c",
      "import json,sys; value=json.loads(sys.stdin.read()); sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
    ],
    { input: source, encoding: "utf8" },
  );
  assert.equal(python.status, 0, python.stderr);
  const rendered = renderPythonLosslessArtifactJson(
    parseDataJsonLosslessly(source),
  );
  assert.equal(rendered, python.stdout);
  assert.match(rendered, /900719925474099312345678901234567890/);
  assert.match(rendered, /"negative_zero": 0/);
  assert.match(rendered, /\\u00e9/);
  assert.match(rendered, /\\ud83d\\ude00/);
  assert.ok(rendered.indexOf('"10"') < rendered.indexOf('"2"'));
  assert.ok(rendered.indexOf('"bmp"') < rendered.indexOf('"huge"'));
});

test("ASCII and Unicode escape boundaries match Python in keys and values", () => {
  const boundaryPoints = [
    0x00,
    0x07,
    0x08,
    0x09,
    0x0a,
    0x0b,
    0x0c,
    0x0d,
    0x0e,
    0x1f,
    0x20,
    0x21,
    0x22,
    0x2f,
    0x5c,
    0x7e,
    0x7f,
    0x80,
    0x85,
    0x9f,
    0xa0,
    0x2028,
    0x1f600,
  ] as const;
  const value: Record<string, unknown> = Object.create(null) as Record<
    string,
    unknown
  >;
  for (const point of boundaryPoints) {
    const character = String.fromCodePoint(point);
    value[character] = character;
  }
  value.sequence = boundaryPoints.map((point) => String.fromCodePoint(point)).join("");

  const source = JSON.stringify(value);
  const python = spawnSync(
    PYTHON_ORACLE,
    [
      "-c",
      "import json,sys; value=json.loads(sys.stdin.read()); sys.stdout.write(json.dumps(value, indent=2, sort_keys=True)+'\\n')",
    ],
    { input: source, encoding: "utf8" },
  );
  assert.equal(python.status, 0, python.stderr);
  const rendered = renderPythonLosslessArtifactJson(value);
  assert.equal(rendered, python.stdout);
  assert.match(rendered, /"\\u007f": "\\u007f"/);
  assert.match(rendered, /"\\u0080": "\\u0080"/);
  assert.match(rendered, /"~": "~"/);
});

test("safe native integers and integral lossless tokens render canonically", () => {
  const shared = { value: new LosslessNumber("-0") };
  const value: Record<string, unknown> = Object.create(null) as Record<
    string,
    unknown
  >;
  Object.defineProperty(value, "__proto__", {
    enumerable: true,
    value: shared,
  });
  Object.defineProperty(value, "constructor", {
    enumerable: true,
    value: shared,
  });
  value.maximum = Number.MAX_SAFE_INTEGER;
  value.minimum = Number.MIN_SAFE_INTEGER;
  value.native_negative_zero = -0;
  value.unbounded = new LosslessNumber(
    "-900719925474099312345678901234567890",
  );
  assert.equal(
    renderPythonLosslessArtifactJson(value),
    [
      "{",
      '  "__proto__": {',
      '    "value": 0',
      "  },",
      '  "constructor": {',
      '    "value": 0',
      "  },",
      '  "maximum": 9007199254740991,',
      '  "minimum": -9007199254740991,',
      '  "native_negative_zero": 0,',
      '  "unbounded": -900719925474099312345678901234567890',
      "}",
      "",
    ].join("\n"),
  );
});

test("finite lossless floats match Python bytes across notation boundaries", () => {
  const source = [
    "0.0",
    "-0.0",
    "1e0",
    "1e-4",
    "1e-5",
    "1e15",
    "1e16",
    "1e20",
    "0.00009999999999999999",
    "100000000000000.1",
    "1.0000000000000002",
    "1e-999",
    "5e-324",
    "1.7976931348623157e308",
  ];
  const python = spawnSync(
    PYTHON_ORACLE,
    [
      "-c",
      "import json,sys; value=json.loads(sys.stdin.read()); sys.stdout.write(json.dumps(value, indent=2)+'\\n')",
    ],
    { input: `[${source.join(",")}]`, encoding: "utf8" },
  );
  assert.equal(python.status, 0, python.stderr);
  assert.equal(
    renderPythonLosslessArtifactJson(
      source.map((token) => new LosslessNumber(token)),
    ),
    python.stdout,
  );
});

test("finite float spelling matches Python across deterministic binary64 values", () => {
  const buffer = new ArrayBuffer(8);
  const view = new DataView(buffer);
  const mask = (1n << 64n) - 1n;
  let state = 0x9e3779b97f4a7c15n;
  const tokens: string[] = [];
  for (let index = 0; index < 2_048; index += 1) {
    state = (state * 6_364_136_223_846_793_005n + 1_442_695_040_888_963_407n) & mask;
    view.setBigUint64(0, state, false);
    const value = view.getFloat64(0, false);
    if (!Number.isFinite(value)) {
      continue;
    }
    let token = Object.is(value, -0) ? "-0.0" : String(value);
    if (!/[.eE]/.test(token)) {
      token += ".0";
    }
    tokens.push(token);
  }
  const python = spawnSync(
    PYTHON_ORACLE,
    [
      "-c",
      "import json,sys; value=json.loads(sys.stdin.read()); sys.stdout.write(json.dumps(value, separators=(',', ':')))",
    ],
    { input: `[${tokens.join(",")}]`, encoding: "utf8" },
  );
  assert.equal(python.status, 0, python.stderr);
  const rendered = renderPythonLosslessArtifactJson(
    tokens.map((token) => new LosslessNumber(token)),
  );
  assert.deepEqual(
    JSON.parse(rendered),
    JSON.parse(python.stdout),
  );
  // JSON.parse numeric equality alone would not catch exponent padding or
  // fixed/scientific notation differences, so also compare compact tokens.
  const compactNode = rendered.replace(/\s+/g, "");
  assert.equal(compactNode, python.stdout);
});

test("native floats, non-finite lexemes, and unsafe native numbers fail", () => {
  expectInvalid(() => renderPythonLosslessArtifactJson(1.5));
  expectInvalid(() => renderPythonLosslessArtifactJson(Number.NaN));
  expectInvalid(() => renderPythonLosslessArtifactJson(Number.POSITIVE_INFINITY));
  expectInvalid(() => {
    renderPythonLosslessArtifactJson(Number.MAX_SAFE_INTEGER + 1);
  });
  expectInvalid(() => renderPythonLosslessArtifactJson(new LosslessNumber("1e400")));
});

test("non-JSON containers, hidden state, and cycles fail closed", () => {
  expectInvalid(() => renderPythonLosslessArtifactJson(undefined));
  expectInvalid(() => renderPythonLosslessArtifactJson(1n));
  expectInvalid(() => renderPythonLosslessArtifactJson(new Date(0)));
  expectInvalid(() => renderPythonLosslessArtifactJson([, 1]));

  const extraArray = [1];
  Object.defineProperty(extraArray, "private-secret", { value: true });
  expectInvalid(
    () => renderPythonLosslessArtifactJson(extraArray),
    "private-secret",
  );

  let getterCalled = false;
  const accessor: Record<string, unknown> = {};
  Object.defineProperty(accessor, "sensitive-value", {
    enumerable: true,
    get() {
      getterCalled = true;
      return "must-not-be-read";
    },
  });
  expectInvalid(
    () => renderPythonLosslessArtifactJson(accessor),
    "sensitive-value",
  );
  assert.equal(getterCalled, false);

  const cyclic: { self?: unknown } = {};
  cyclic.self = cyclic;
  expectInvalid(() => renderPythonLosslessArtifactJson(cyclic));

  const throwingProxy = new Proxy({}, {
    ownKeys() {
      throw new ProcessFailure({
        code: "CALLER_FAILURE",
        category: "domain",
        message: "sensitive-proxy-value",
      });
    },
  });
  expectInvalid(
    () => renderPythonLosslessArtifactJson(throwingProxy),
    "sensitive-proxy-value",
  );
});
