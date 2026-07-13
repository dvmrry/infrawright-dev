import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();

test("the bundled CLI can load Undici and execute fetch without Python", async () => {
  const built = spawnSync(process.execPath, ["scripts/build-metadata-cli.mjs"], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(built.status, 0, built.stderr);

  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-bundle-fetch-"));
  try {
    const packs = path.join(directory, "packs");
    await mkdir(packs);
    const result = spawnSync(process.execPath, [
      path.join(ROOT, "dist", "infrawright-cli.mjs"),
      "fetch",
      "--tenant",
      "bundle-smoke",
      "--out",
      path.join(directory, "pulls"),
      "--root",
      packs,
      "--profile",
      path.join(ROOT, "packsets", "empty.json"),
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
    ], {
      cwd: ROOT,
      encoding: "utf8",
      env: {
        ...process.env,
        HTTP_PROXY: "",
        HTTPS_PROXY: "",
        NO_PROXY: "",
        PYTHON: path.join(directory, "python-must-not-run"),
        REQUESTS_CA_BUNDLE: "",
        SSL_CERT_FILE: "",
        http_proxy: "",
        https_proxy: "",
        no_proxy: "",
      },
    });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stderr, /fetch: auth mode = oneapi/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
