import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const REPOSITORY = process.cwd();
const PREFLIGHT = path.join(
  REPOSITORY,
  "scripts",
  "build-environment-preflight.mjs",
);

interface LockedPackage {
  readonly version?: string;
  readonly integrity?: string;
  readonly optional?: boolean;
  readonly os?: readonly string[];
  readonly cpu?: readonly string[];
}

function shellLiteral(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}

async function fakeNpm(directory: string): Promise<string> {
  const implementation = path.join(directory, "fake-npm.mjs");
  const executable = path.join(directory, "npm");
  await writeFile(
    implementation,
    `import { appendFileSync } from "node:fs";
const args = process.argv.slice(2);
if (process.env.FAKE_NPM_LOG) appendFileSync(process.env.FAKE_NPM_LOG, JSON.stringify(args) + "\\n");
if (args[0] === "config" && args[1] === "get") {
  const key = args[2];
  const values = {
    registry: process.env.FAKE_NPM_REGISTRY ?? "https://registry.npmjs.org/",
    "replace-registry-host": process.env.FAKE_NPM_REPLACE ?? "always",
    omit: process.env.FAKE_NPM_OMIT ?? (process.env.NODE_ENV === "production" ? "dev" : ""),
    include: process.env.FAKE_NPM_INCLUDE ?? "",
    optional: process.env.FAKE_NPM_OPTIONAL ?? "true",
  };
  process.stdout.write(values[key] ?? process.env.FAKE_NPM_SCOPE_REGISTRY ?? "undefined");
} else if (args[0] === "view") {
  const packageSpec = args[1];
  if (packageSpec === process.env.FAKE_NPM_MISSING) {
    process.stderr.write("npm error code E404\\nnpm error 404 Not Found secret-token-from-registry");
    process.exit(1);
  }
  if (packageSpec === process.env.FAKE_NPM_AUTH_FAILURE) {
    process.stderr.write("npm error code E401\\nnpm error authentication token not found");
    process.exit(1);
  }
  const responses = JSON.parse(process.env.FAKE_NPM_RESPONSES ?? "{}");
  const response = responses[packageSpec];
  if (!response) {
    process.stderr.write("unexpected package lookup");
    process.exit(2);
  }
  process.stdout.write(JSON.stringify({ version: response.version, "dist.integrity": response.integrity }));
} else {
  process.stderr.write("unexpected npm command");
  process.exit(2);
}
`,
    "utf8",
  );
  await writeFile(
    executable,
    `#!/bin/sh\nexec ${shellLiteral(process.execPath)} ${shellLiteral(implementation)} "$@"\n`,
    "utf8",
  );
  await chmod(executable, 0o755);
  return executable;
}

async function lockedResponses(
  platform: string,
  arch: string,
): Promise<Record<string, { version: string; integrity: string }>> {
  const lock = JSON.parse(
    await readFile(path.join(REPOSITORY, "package-lock.json"), "utf8"),
  ) as { packages: Record<string, LockedPackage> };
  const responses: Record<string, { version: string; integrity: string }> = {};
  for (const [lockPath, item] of Object.entries(lock.packages)) {
    const marker = "node_modules/";
    const markerIndex = lockPath.lastIndexOf(marker);
    if (
      markerIndex < 0
      || item.version === undefined
      || item.integrity === undefined
    ) {
      continue;
    }
    if (
      item.optional === true
      && (item.os !== undefined || item.cpu !== undefined)
      && (
        (item.os !== undefined && !item.os.includes(platform))
        || (item.cpu !== undefined && !item.cpu.includes(arch))
      )
    ) {
      continue;
    }
    const name = lockPath.slice(markerIndex + marker.length);
    responses[`${name}@${item.version}`] = {
      integrity: item.integrity,
      version: item.version,
    };
  }
  return responses;
}

function runPreflight(
  arguments_: readonly string[],
  environment: NodeJS.ProcessEnv = {},
): { readonly status: number | null; readonly stdout: string; readonly stderr: string } {
  const result = spawnSync(process.execPath, [PREFLIGHT, ...arguments_], {
    cwd: REPOSITORY,
    encoding: "utf8",
    env: { ...process.env, ...environment },
    maxBuffer: 1024 * 1024,
    timeout: 30_000,
  });
  return {
    status: result.status,
    stderr: result.stderr,
    stdout: result.stdout,
  };
}

test("mirror manifest is derived from the pinned lockfile", () => {
  const result = runPreflight(["--manifest"]);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /Ordinary packages \(15\):/u);
  assert.match(result.stdout, /^  esbuild@0\.25\.12$/mu);
  assert.match(result.stdout, /^  typescript@7\.0\.2$/mu);
  const platformPackages = new Map([
    ["darwin/arm64", [
      "@esbuild/darwin-arm64@0.25.12",
      "@typescript/typescript-darwin-arm64@7.0.2",
    ]],
    ["darwin/x64", [
      "@esbuild/darwin-x64@0.25.12",
      "@typescript/typescript-darwin-x64@7.0.2",
    ]],
    ["linux/arm64", [
      "@esbuild/linux-arm64@0.25.12",
      "@typescript/typescript-linux-arm64@7.0.2",
    ]],
    ["linux/x64", [
      "@esbuild/linux-x64@0.25.12",
      "@typescript/typescript-linux-x64@7.0.2",
    ]],
  ]);
  const lines = result.stdout.split("\n");
  for (const [target, packages] of platformPackages) {
    const index = lines.indexOf(`  ${target}:`);
    assert.notEqual(index, -1, `missing manifest section for ${target}`);
    const actual: string[] = [];
    for (const line of lines.slice(index + 1)) {
      if (!line.startsWith("    ")) break;
      actual.push(line.trim());
    }
    assert.deepEqual(actual, packages);
  }
  assert.match(
    result.stdout,
    /esbuild@0\.25\.12 — may run nested npm and then download directly/u,
  );
});

test("restricted registry success verifies exact versions and integrity", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-preflight-"));
  try {
    const npm = await fakeNpm(directory);
    const responses = await lockedResponses("darwin", "arm64");
    const result = runPreflight([
      "--npm",
      npm,
      "--platform",
      "darwin",
      "--arch",
      "arm64",
    ], {
      FAKE_NPM_REGISTRY: "https://user:secret@artifactory.example/npm/",
      FAKE_NPM_RESPONSES: JSON.stringify(responses),
    });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /^Configured registry host: artifactory\.example$/mu);
    assert.match(result.stdout, /Source build environment is suitable/u);
    assert.doesNotMatch(`${result.stdout}${result.stderr}`, /user|secret/iu);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("missing mirrored platform package is exact, actionable, and redacted", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-missing-"));
  try {
    const npm = await fakeNpm(directory);
    const responses = await lockedResponses("darwin", "arm64");
    const missing = "@esbuild/darwin-arm64@0.25.12";
    const result = runPreflight([
      "--npm",
      npm,
      "--platform",
      "darwin",
      "--arch",
      "arm64",
    ], {
      FAKE_NPM_MISSING: missing,
      FAKE_NPM_REGISTRY: "https://build-user:registry-token@artifactory.example/npm/",
      FAKE_NPM_RESPONSES: JSON.stringify(responses),
    });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Source build unavailable in the configured registry/u);
    assert.match(result.stderr, new RegExp(`Missing mirrored build dependency: ${missing.replaceAll("/", "\\/")}`, "u"));
    assert.match(result.stderr, /prebuilt, checksum-verified dist\/infrawright-cli\.mjs/u);
    assert.doesNotMatch(
      `${result.stdout}${result.stderr}`,
      /build-user|registry-token|secret-token-from-registry/iu,
    );
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("public scoped-registry bypass is rejected without package resolution", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-policy-"));
  try {
    const npm = await fakeNpm(directory);
    const log = path.join(directory, "npm.log");
    const responses = await lockedResponses("linux", "x64");
    const result = runPreflight([
      "--npm",
      npm,
      "--platform",
      "linux",
      "--arch",
      "x64",
    ], {
      FAKE_NPM_LOG: log,
      FAKE_NPM_REGISTRY: "https://artifactory.example/npm/",
      FAKE_NPM_RESPONSES: JSON.stringify(responses),
      FAKE_NPM_SCOPE_REGISTRY: "https://registry.npmjs.org/",
    });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /configured to bypass the internal registry/u);
    const calls = (await readFile(log, "utf8")).trim().split("\n").map((line) => {
      return JSON.parse(line) as string[];
    });
    assert.equal(calls.some((arguments_) => arguments_[0] === "view"), false);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("safe registry-host replacement policies preserve the internal registry", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-rewrite-"));
  try {
    const npm = await fakeNpm(directory);
    const responses = await lockedResponses("linux", "x64");
    for (const replacement of ["npmjs", "registry.npmjs.org", "always"]) {
      const result = runPreflight([
        "--npm",
        npm,
        "--platform",
        "linux",
        "--arch",
        "x64",
      ], {
        FAKE_NPM_REGISTRY: "https://artifactory.example/npm/",
        FAKE_NPM_REPLACE: replacement,
        FAKE_NPM_RESPONSES: JSON.stringify(responses),
      });
      assert.equal(result.status, 0, `${replacement}: ${result.stderr}`);
      assert.match(result.stdout, /Source build environment is suitable/u);
    }
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("unsafe registry-host replacement fails before lookup without leaking configuration", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-unsafe-rewrite-"));
  try {
    const npm = await fakeNpm(directory);
    const log = path.join(directory, "npm.log");
    for (const replacement of ["never", "old-registry.example", "secret-token-value"]) {
      const result = runPreflight([
        "--npm",
        npm,
        "--platform",
        "linux",
        "--arch",
        "x64",
      ], {
        FAKE_NPM_LOG: log,
        FAKE_NPM_OMIT: replacement === "secret-token-value" ? "optional" : "",
        FAKE_NPM_REGISTRY: "https://artifactory.example/npm/",
        FAKE_NPM_REPLACE: replacement,
      });
      assert.equal(result.status, 1);
      assert.match(result.stderr, /leaves lockfile host registry\.npmjs\.org authoritative/u);
      assert.doesNotMatch(`${result.stdout}${result.stderr}`, /secret-token-value/u);
    }
    const calls = (await readFile(log, "utf8")).trim().split("\n").map((line) => {
      return JSON.parse(line) as string[];
    });
    assert.equal(calls.some((arguments_) => arguments_[0] === "view"), false);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("authentication errors containing not found are unresolved rather than missing", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-auth-"));
  try {
    const npm = await fakeNpm(directory);
    const responses = await lockedResponses("darwin", "arm64");
    const failed = "@esbuild/darwin-arm64@0.25.12";
    const result = runPreflight([
      "--npm",
      npm,
      "--platform",
      "darwin",
      "--arch",
      "arm64",
    ], {
      FAKE_NPM_AUTH_FAILURE: failed,
      FAKE_NPM_REGISTRY: "https://artifactory.example/npm/",
      FAKE_NPM_RESPONSES: JSON.stringify(responses),
    });
    assert.equal(result.status, 1);
    assert.match(
      result.stderr,
      new RegExp(`Registry resolution failed without a verified 404: ${failed.replaceAll("/", "\\/")}`, "u"),
    );
    assert.doesNotMatch(result.stderr, /Missing mirrored build dependency/u);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});

test("effective dev omission is rejected unless dev is explicitly included", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-build-dev-policy-"));
  try {
    const npm = await fakeNpm(directory);
    const log = path.join(directory, "npm.log");
    const baseEnvironment = {
      FAKE_NPM_LOG: log,
      FAKE_NPM_REGISTRY: "https://artifactory.example/npm/",
    };
    for (const environment of [
      { ...baseEnvironment, FAKE_NPM_OMIT: "dev" },
      { ...baseEnvironment, NODE_ENV: "production" },
    ]) {
      const result = runPreflight([
        "--npm",
        npm,
        "--platform",
        "linux",
        "--arch",
        "x64",
      ], environment);
      assert.equal(result.status, 1);
      assert.match(result.stderr, /omit required development build tools/u);
    }
    let calls = (await readFile(log, "utf8")).trim().split("\n").map((line) => {
      return JSON.parse(line) as string[];
    });
    assert.equal(calls.some((arguments_) => arguments_[0] === "view"), false);

    await writeFile(log, "", "utf8");
    const responses = await lockedResponses("linux", "x64");
    const included = runPreflight([
      "--npm",
      npm,
      "--platform",
      "linux",
      "--arch",
      "x64",
    ], {
      ...baseEnvironment,
      FAKE_NPM_INCLUDE: "dev",
      FAKE_NPM_OMIT: "dev",
      FAKE_NPM_RESPONSES: JSON.stringify(responses),
    });
    assert.equal(included.status, 0, included.stderr);
    calls = (await readFile(log, "utf8")).trim().split("\n").map((line) => {
      return JSON.parse(line) as string[];
    });
    assert.equal(calls.some((arguments_) => arguments_[0] === "view"), true);
  } finally {
    await rm(directory, { force: true, recursive: true });
  }
});
