import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();

function buildBundle(): void {
  const built = spawnSync(process.execPath, ["scripts/build-metadata-cli.mjs"], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(built.status, 0, built.stderr);
}

test("the bundled CLI can load Undici and execute fetch without Python", async () => {
  buildBundle();

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

test("the authoritative operational inventory expands only to built Node CLI routes", async () => {
  buildBundle();
  const document = await readFile(
    path.join(ROOT, "docs", "operational-runtime.md"),
    "utf8",
  );
  const start = "<!-- operational-command-inventory:start -->";
  const end = "<!-- operational-command-inventory:end -->";
  const blockStart = document.indexOf(start);
  const blockEnd = document.indexOf(end);
  assert.ok(blockStart >= 0 && blockEnd > blockStart, "missing command inventory markers");
  const rows = document.slice(blockStart + start.length, blockEnd)
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.startsWith("|") && !/^\|[-|]+\|$/u.test(line))
    .map((line) => line.slice(1, -1).split("|").map((cell) => {
      return cell.trim().replace(/^`|`$/gu, "");
    }));
  assert.deepEqual(rows.shift(), ["Surface", "Make target", "CLI route"]);
  assert.equal(rows.length, 22);

  const help = spawnSync(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "--help",
  ], { cwd: ROOT, encoding: "utf8" });
  assert.equal(help.status, 0, help.stderr);
  assert.equal(help.stderr, "");
  assert.match(help.stdout, /^usage:\n/u);

  for (const row of rows) {
    assert.equal(row.length, 3);
    const [, target, route] = row;
    assert.ok(target !== undefined && target !== "");
    assert.ok(route !== undefined && route !== "");
    const expanded = spawnSync("make", [
      "--no-print-directory",
      "--dry-run",
      "--always-make",
      target,
      "OVERLAY=",
      "TENANT=inventory-tenant",
      "IN=/tmp/infrawright-inventory-input",
      "PATHS_JSON=/tmp/infrawright-inventory-paths.json",
      `DEPLOYMENT=${path.join(ROOT, "deployment.json")}`,
      `PACK_PROFILE=${path.join(ROOT, "packsets", "full.json")}`,
      `PACK_CATALOG=${path.join(ROOT, "packsets", "full.json")}`,
      "INFRAWRIGHT_CLI=__INFRAWRIGHT_NODE_CLI__",
      "PYTHON=__PYTHON_RUNTIME_FORBIDDEN__",
      "NPM=__NPM_BUILD_ONLY__",
      "TF=__TERRAFORM__",
    ], { cwd: ROOT, encoding: "utf8" });
    assert.equal(expanded.status, 0, `${target}: ${expanded.stderr}`);
    const normalized = expanded.stdout
      .replace(/\\\r?\n/gu, " ")
      .replace(/["']/gu, "")
      .replace(/\s+/gu, " ");
    assert.equal(
      normalized.includes(`__INFRAWRIGHT_NODE_CLI__ ${route}`),
      true,
      `${target} did not route through ${route}:\n${expanded.stdout}`,
    );
    assert.doesNotMatch(normalized, /__PYTHON_RUNTIME_FORBIDDEN__/u, target);
    assert.doesNotMatch(
      normalized,
      /(^|[\s;&|()])python(?:3(?:\.\d+)?)?(?=$|[\s;&|()])/iu,
      target,
    );
    assert.doesNotMatch(normalized, /\s-m\s+engine(?:\.|\s)/iu, target);

    const command = route.split(" ")[0] ?? "";
    assert.match(
      help.stdout,
      new RegExp(`^  infrawright ${command.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&")}(?: |$)`, "mu"),
      `built CLI help omits ${command}`,
    );
  }
  assert.match(help.stdout, /infrawright modules <generate\|validate>/u);
  assert.match(help.stdout, /infrawright resources \[--order=references\]/u);

  const unknown = spawnSync(process.execPath, [
    path.join(ROOT, "dist", "infrawright-cli.mjs"),
    "not-a-command",
  ], { cwd: ROOT, encoding: "utf8" });
  assert.equal(unknown.status, 2);
  assert.match(unknown.stderr, /^error: unknown command not-a-command/u);
});
