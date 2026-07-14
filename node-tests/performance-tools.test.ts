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
  const fetch = command === "fetch";
  return {
    command,
    command_duration_ms: duration,
    command_status: "success",
    format: "infrawright-performance-report",
    http: fetch ? [{
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
    selected_concurrency: fetch ? 1 : null,
    spans: [{
      duration_ms: duration,
      ...(fetch
        ? { logical_requests: 2, pages: 2 }
        : { terraform_commands: 5 }),
      phase: fetch ? "fetch.total" : "adopt.total",
      status: "success",
    }],
    summary: {
      http_requests: fetch ? 2 : 0,
      logical_requests: fetch ? 2 : 0,
      pages: fetch ? 2 : 0,
      rate_limited_requests: 0,
      retries: 0,
      retry_delay_ms: 0,
      terraform_commands: command === "adopt" ? 5 : 0,
    },
  };
}

async function writeManifest(
  runDirectory: string,
  roots: readonly [string, string][],
): Promise<void> {
  const result = run(MANIFEST, [
    ...roots.flatMap(([label, directory]) => ["--root", `${label}=${directory}`]),
    "--out", path.join(runDirectory, "artifacts.sha256.json"),
  ]);
  assert.equal(result.status, 0, result.stderr);
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
      await writeManifest(runDirectory, [["pulls", artifacts]]);
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
    assert.match(comparison.stdout, /\| candidate \| 0 \| 0 \| 0\.000 \|/);
    assert.doesNotMatch(comparison.stdout, new RegExp(directory.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&")));
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("artifact manifests bind empty roots and comparison rejects different scopes", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "performance-scope-"));
  try {
    for (const name of ["baseline", "candidate"]) {
      const runDirectory = path.join(directory, name);
      const pulls = path.join(runDirectory, "pulls");
      await mkdir(pulls, { recursive: true });
      await writeFile(path.join(pulls, "sample.json"), "[]\n", "utf8");
      await writeFile(
        path.join(runDirectory, "fetch.performance.json"),
        JSON.stringify(performanceReport("fetch", 10)),
      );
      if (name === "baseline") {
        const config = path.join(runDirectory, "config");
        await mkdir(config, { recursive: true });
        await writeManifest(runDirectory, [["pulls", pulls], ["config", config]]);
      } else {
        await writeManifest(runDirectory, [["pulls", pulls]]);
      }
    }
    const baseline = JSON.parse(await readFile(
      path.join(directory, "baseline", "artifacts.sha256.json"),
      "utf8",
    )) as { tree_sha256: string };
    const candidate = JSON.parse(await readFile(
      path.join(directory, "candidate", "artifacts.sha256.json"),
      "utf8",
    )) as { tree_sha256: string };
    assert.notEqual(candidate.tree_sha256, baseline.tree_sha256);
    const comparison = run(COMPARE, [
      "--variant", `baseline=${path.join(directory, "baseline")}`,
      "--variant", `candidate=${path.join(directory, "candidate")}`,
    ]);
    assert.equal(comparison.status, 2);
    assert.match(comparison.stderr, /does not cover the baseline artifact roots/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("comparison rejects failed, missing, duplicate, malformed, and tampered evidence", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "performance-invalid-"));
  try {
    const baseline = path.join(directory, "baseline");
    const baselinePulls = path.join(baseline, "pulls");
    await mkdir(baselinePulls, { recursive: true });
    await writeFile(path.join(baselinePulls, "sample.json"), "[]\n", "utf8");
    await writeFile(
      path.join(baseline, "fetch.performance.json"),
      JSON.stringify(performanceReport("fetch", 10)),
    );
    await writeManifest(baseline, [["pulls", baselinePulls]]);

    const cases: Array<{
      readonly label: string;
      readonly prepare: (runDirectory: string) => Promise<void>;
      readonly pattern: RegExp;
    }> = [
      {
        label: "failed",
        pattern: /not a successful Infrawright performance report/,
        prepare: async (runDirectory) => {
          const report = performanceReport("fetch", 1);
          report.command_status = "failed";
          await writeFile(path.join(runDirectory, "fetch.performance.json"), JSON.stringify(report));
        },
      },
      {
        label: "missing",
        pattern: /missing its Fetch performance report/,
        prepare: async (runDirectory) => {
          await writeFile(
            path.join(runDirectory, "adopt.performance.json"),
            JSON.stringify(performanceReport("adopt", 1)),
          );
        },
      },
      {
        label: "duplicate",
        pattern: /duplicate fetch performance report/,
        prepare: async (runDirectory) => {
          const report = JSON.stringify(performanceReport("fetch", 1));
          await writeFile(path.join(runDirectory, "fetch-a.performance.json"), report);
          await writeFile(path.join(runDirectory, "fetch-b.performance.json"), report);
        },
      },
      {
        label: "malformed",
        pattern: /summary http_requests is inconsistent/,
        prepare: async (runDirectory) => {
          const report = performanceReport("fetch", 1);
          report.summary.http_requests = 0;
          await writeFile(path.join(runDirectory, "fetch.performance.json"), JSON.stringify(report));
        },
      },
    ];
    for (const selected of cases) {
      const runDirectory = path.join(directory, selected.label);
      const pulls = path.join(runDirectory, "pulls");
      await mkdir(pulls, { recursive: true });
      await writeFile(path.join(pulls, "sample.json"), "[]\n", "utf8");
      await selected.prepare(runDirectory);
      await writeManifest(runDirectory, [["pulls", pulls]]);
      const comparison = run(COMPARE, [
        "--variant", `baseline=${baseline}`,
        "--variant", `${selected.label}=${runDirectory}`,
      ]);
      assert.equal(comparison.status, 2, selected.label);
      assert.match(comparison.stderr, selected.pattern, selected.label);
    }

    const tampered = path.join(directory, "tampered");
    const tamperedPulls = path.join(tampered, "pulls");
    await mkdir(tamperedPulls, { recursive: true });
    await writeFile(path.join(tamperedPulls, "sample.json"), "[]\n", "utf8");
    await writeFile(
      path.join(tampered, "fetch.performance.json"),
      JSON.stringify(performanceReport("fetch", 1)),
    );
    await writeManifest(tampered, [["pulls", tamperedPulls]]);
    const manifestPath = path.join(tampered, "artifacts.sha256.json");
    const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as { tree_sha256: string };
    manifest.tree_sha256 = "0".repeat(64);
    await writeFile(manifestPath, JSON.stringify(manifest));
    const comparison = run(COMPARE, [
      "--variant", `baseline=${baseline}`,
      "--variant", `tampered=${tampered}`,
    ]);
    assert.equal(comparison.status, 2);
    assert.match(comparison.stderr, /manifest digest does not match/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("comparison surfaces rate-limit and retry evidence", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "performance-rate-limit-"));
  try {
    for (const name of ["baseline", "candidate"]) {
      const runDirectory = path.join(directory, name);
      const pulls = path.join(runDirectory, "pulls");
      await mkdir(pulls, { recursive: true });
      await writeFile(path.join(pulls, "sample.json"), "[]\n", "utf8");
      const report = performanceReport("fetch", 10);
      if (name === "candidate") {
        report.http = [
          {
            classification: "list",
            duration_ms_p50: 1,
            duration_ms_p95: 1,
            duration_ms_total: 1,
            endpoint_family: "items",
            phase: "fetch",
            request_count: 1,
            retries: 1,
            retry_delay_ms: 250,
            status: 429,
          },
          {
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
          },
        ];
        report.summary.http_requests = 3;
        report.summary.rate_limited_requests = 1;
        report.summary.retries = 1;
        report.summary.retry_delay_ms = 250;
      }
      await writeFile(path.join(runDirectory, "fetch.performance.json"), JSON.stringify(report));
      await writeManifest(runDirectory, [["pulls", pulls]]);
    }
    const comparison = run(COMPARE, [
      "--variant", `baseline=${path.join(directory, "baseline")}`,
      "--variant", `candidate=${path.join(directory, "candidate")}`,
    ]);
    assert.equal(comparison.status, 0, comparison.stderr);
    assert.match(comparison.stdout, /\| candidate \| 1 \| 1 \| 250\.000 \|/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
