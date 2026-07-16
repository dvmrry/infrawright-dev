import assert from "node:assert/strict";
import { mkdir, mkdtemp, realpath, rm, symlink } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  pythonPosixNormPath,
  pythonPosixRealpath,
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

test("non-strict realpath canonicalizes prefixes before symlink loops", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-path-"));
  try {
    const realParent = path.join(directory, "real-parent");
    await mkdir(realParent);
    const aliasParent = path.join(directory, "alias-parent");
    await symlink(realParent, aliasParent);
    await symlink("b", path.join(realParent, "a"));
    await symlink("a", path.join(realParent, "b"));
    const candidate = path.join(aliasParent, "a", "deleted-child");
    // Python 3.13 canonicalizes the existing parent and alias but retains the
    // first unresolved loop member. Freeze that semantic shape while deriving
    // only the platform-specific canonical temporary prefix at runtime.
    assert.equal(
      pythonPosixRealpath(candidate),
      path.join(await realpath(directory), "real-parent", "a", "deleted-child"),
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
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
