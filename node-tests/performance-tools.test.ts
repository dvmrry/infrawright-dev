import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();
const MANIFEST = path.join(ROOT, "scripts", "performance-artifact-manifest.mjs");
const COMPARE = path.join(ROOT, "scripts", "compare-performance-reports.mjs");

function run(script: string, arguments_: readonly string[]) {
  return spawnSync(process.execPath, [script, ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
  });
}

function performanceReport(command: "adopt" | "fetch", duration: number) {
  return {
    command,
    command_duration_ms: duration,
    command_status: "success",
    format: "infrawright-performance-report",
    http: command === "fetch" ? [{
      classification: "list",
      duration_ms_p50: 1,
      duration_ms_p95: 1,
      duration_ms_total: 2,
      endpoint_family: "items",
      phase: "fetch",
      request_count: 2,
      retries: 0,
      retry_delay_ms: 0,
      status: 200,
    }] : [],
    selected_concurrency: command === "fetch" ? 1 : null,
    spans: [],
    summary: {
      http_requests: command === "fetch" ? 2 : 0,
      logical_requests: command === "fetch" ? 2 : 0,
      pages: command === "fetch" ? 2 : 0,
      rate_limited_requests: 0,
      retries: 0,
      retry_delay_ms: 0,
      terraform_commands: command === "adopt" ? 5 : 0,
    },
  };
}

test("performance tools hash exact artifacts and compare sanitized reports", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "performance-tools-"));
  try {
    const runs = ["baseline", "candidate"];
    for (const name of runs) {
      const runDirectory = path.join(directory, name);
      const artifacts = path.join(runDirectory, "pulls");
      await mkdir(artifacts, { recursive: true });
      await writeFile(path.join(artifacts, "sample_a.json"), "[{\"id\":1}]\n", "utf8");
      await writeFile(path.join(runDirectory, "fetch.performance.json"), JSON.stringify(
        performanceReport("fetch", name === "baseline" ? 100 : 50),
      ));
      await writeFile(path.join(runDirectory, "adopt.performance.json"), JSON.stringify(
        performanceReport("adopt", name === "baseline" ? 200 : 120),
      ));
      const manifest = run(MANIFEST, [
        "--root", `pulls=${artifacts}`,
        "--out", path.join(runDirectory, "artifacts.sha256.json"),
      ]);
      assert.equal(manifest.status, 0, manifest.stderr);
    }
    const baselineManifest = JSON.parse(await readFile(
      path.join(directory, "baseline", "artifacts.sha256.json"),
      "utf8",
    )) as { tree_sha256: string };
    const candidateManifest = JSON.parse(await readFile(
      path.join(directory, "candidate", "artifacts.sha256.json"),
      "utf8",
    )) as { tree_sha256: string };
    assert.equal(candidateManifest.tree_sha256, baselineManifest.tree_sha256);

    const comparison = run(COMPARE, [
      "--variant", `baseline=${path.join(directory, "baseline")}`,
      "--variant", `candidate=${path.join(directory, "candidate")}`,
    ]);
    assert.equal(comparison.status, 0, comparison.stderr);
    assert.match(comparison.stdout, /\| baseline \| 100\.000 \| 200\.000 \| 300\.000 \| 2 \| 0 \| 2 \| 5 \| baseline \|/);
    assert.match(comparison.stdout, /\| candidate \| 50\.000 \| 120\.000 \| 170\.000 \| 2 \| 0 \| 2 \| 5 \| yes \|/);
    assert.doesNotMatch(comparison.stdout, new RegExp(directory.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&")));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
