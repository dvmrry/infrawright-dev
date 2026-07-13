import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { resolvePythonOracle } from "./python-oracle.js";

async function fakePython(
  directory: string,
  name: string,
  python = "3.13",
  unicode = "15.1.0",
  marker?: string,
): Promise<string> {
  const executable = path.join(directory, name);
  const markerLine = marker === undefined
    ? ""
    : `printf '%s\\n' invoked >> ${JSON.stringify(marker)}\n`;
  await writeFile(
    executable,
    "#!/bin/sh\n"
      + markerLine
      + `case "$*" in *unicodedata*) printf '%s' '${JSON.stringify({
        python,
        unicode,
      })}' ;; *) printf '%s\\n' selected ;; esac\n`,
    "utf8",
  );
  await chmod(executable, 0o755);
  return executable;
}

test("explicit PYTHON wins over a different system python3", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-python-oracle-"));
  try {
    const marker = path.join(directory, "system-python3-used");
    const selected = await fakePython(directory, "selected python", "3.12", "15.0.0");
    await fakePython(directory, "python3", "3.14", "16.0.0", marker);
    const oracle = resolvePythonOracle({
      environment: { ...process.env, PATH: directory, PYTHON: selected },
    });
    assert.deepEqual(oracle, {
      executable: selected,
      pythonVersion: "3.12",
      unicodeVersion: "15.0.0",
    });
    await assert.rejects(readFile(marker, "utf8"), /ENOENT/u);
    const child = spawnSync(oracle.executable, ["-c", "cohort"], {
      encoding: "utf8",
    });
    assert.equal(child.status, 0, child.stderr);
    assert.equal(child.stdout, "selected\n");
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("blank PYTHON falls back from unavailable python3 to python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-python-fallback-"));
  try {
    const fallback = await fakePython(directory, "python");
    const oracle = resolvePythonOracle({
      environment: { ...process.env, PATH: directory, PYTHON: "  " },
    });
    assert.equal(oracle.executable, "python");
    assert.equal(path.join(directory, oracle.executable), fallback);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("implicit unsupported python3 falls through to supported python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-python-version-fallback-"));
  try {
    await fakePython(directory, "python3", "3.9", "13.0.0");
    await fakePython(directory, "python", "3.13", "15.1.0");
    const oracle = resolvePythonOracle({
      environment: { ...process.env, PATH: directory, PYTHON: "" },
    });
    assert.deepEqual(oracle, {
      executable: "python",
      pythonVersion: "3.13",
      unicodeVersion: "15.1.0",
    });
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("explicit missing PYTHON does not fall through", () => {
  assert.throws(
    () => resolvePythonOracle({
      environment: {
        ...process.env,
        PATH: process.env.PATH,
        PYTHON: "/definitely/missing/python oracle",
      },
    }),
    /unsupported Python migration oracle.*ENOENT.*Set PYTHON/u,
  );
});

test("unsupported Python and Unicode versions are actionable", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-python-unsupported-"));
  try {
    const unsupported = await fakePython(directory, "python 3.14", "3.14", "16.0.0");
    assert.throws(
      () => resolvePythonOracle({
        environment: { ...process.env, PYTHON: unsupported },
      }),
      /found Python 3\.14 \/ UCD 16\.0\.0.*Python 3\.12\/UCD 15\.0\.0 or Python 3\.13\/UCD 15\.1\.0/u,
    );
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});
