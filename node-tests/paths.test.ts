import assert from "node:assert/strict";
import test from "node:test";

import {
  pythonPosixNormPath,
  pythonRelativeUnder,
} from "../node-src/domain/paths.js";

test("POSIX normalization matches Python edge cases", () => {
  const cases: ReadonlyArray<readonly [string, string]> = [
    ["", "."],
    ["./", "."],
    ["a/", "a"],
    ["a//b/../c", "a/c"],
    ["../a/../../b", "../../b"],
    ["/../../a", "/a"],
    ["//server/share/../x", "//server/x"],
    ["///server/share", "/server/share"],
  ];
  for (const [input, expected] of cases) {
    assert.equal(pythonPosixNormPath(input), expected, input);
  }
});

test("relative containment uses the supplied workspace", () => {
  assert.deepEqual(
    pythonRelativeUnder("artifacts/config/prod/x.auto.tfvars.json", "artifacts/config", "/tmp/workspace"),
    ["prod", "x.auto.tfvars.json"],
  );
  assert.equal(
    pythonRelativeUnder("../outside", "artifacts/config", "/tmp/workspace"),
    null,
  );
});
