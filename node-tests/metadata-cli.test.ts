import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");

function run(
  arguments_: readonly string[],
  environment?: Readonly<Record<string, string>>,
) {
  return spawnSync(process.execPath, [CLI, ...arguments_], {
    cwd: ROOT,
    encoding: "utf8",
    env: { ...process.env, ...environment },
  });
}

async function writeJson(pathname: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(pathname), { recursive: true });
  await writeFile(pathname, JSON.stringify(value));
}

test("metadata CLI validates the committed pack root without Python", () => {
  const result = run(["check-pack", "--pack", "zia"]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "validated packs: zia\n");
  assert.equal(result.stderr, "");
});

test("empty pack environment selections retain Python falsey fallback", () => {
  const packResult = run(["check-pack"], { INFRAWRIGHT_PACKS: "" });
  assert.equal(packResult.status, 0, packResult.stderr);
  assert.match(packResult.stdout, /validated packs: aws, cloudflare/);

  const setResult = run(["check-pack-set"], {
    INFRAWRIGHT_PACKS: "",
    INFRAWRIGHT_PACK_PROFILE: "",
  });
  assert.equal(setResult.status, 0, setResult.stderr);
  assert.match(setResult.stdout, /^validated pack set: packs=\[aws,cloudflare/);
});

test("metadata CLI preserves the requirements-unavailable exit contract", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-"));
  try {
    const requirements = path.join(directory, "requirements.json");
    await writeJson(requirements, {
      kind: "infrawright.pack-requirements",
      version: 1,
      packs: ["zia"],
      shared: ["zscaler"],
    });
    const result = run([
      "check-pack-set",
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
      "--requirements",
      requirements,
      "--root",
      directory,
    ]);
    assert.equal(result.status, 3, result.stderr);
    assert.equal(result.stdout, "requirements unavailable: packs=zia shared=zscaler\n");
    assert.equal(result.stderr, "");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("metadata CLI reports strict registry and override failures on stderr", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-"));
  try {
    await writeJson(path.join(directory, "sample", "pack.json"), {
      provider_prefixes: { sample_: "sample" },
    });
    await writeJson(path.join(directory, "sample", "registry.json"), {
      sample_resource: {
        product: "sample",
        fetch: { pagination: "single", path: "/items" },
      },
    });
    await writeJson(
      path.join(directory, "sample", "overrides", "sample_resource.json"),
      { rename: { old: "new" } },
    );
    const result = run(["check-pack", "--root", directory]);
    assert.equal(result.status, 1);
    assert.equal(result.stdout, "");
    assert.match(result.stderr, /unknown override key rename/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("deployment CLI exposes the existing module and tenant path contract", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    await writeJson(deployment, { overlay: "estate", tfvars_format: "hcl" });
    const moduleResult = run([
      "deployment",
      "--deployment",
      deployment,
      "module-dir",
    ]);
    assert.equal(moduleResult.status, 0, moduleResult.stderr);
    assert.equal(moduleResult.stdout, `${path.join("estate", "modules", "default")}\n`);
    const configResult = run([
      "deployment",
      "--deployment",
      deployment,
      "config-dir",
      "tenant-a",
    ]);
    assert.equal(configResult.status, 0, configResult.stderr);
    assert.equal(configResult.stdout, `${path.join("estate", "config", "tenant-a")}\n`);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("CLI distinguishes usage failures from metadata failures", () => {
  const usage = run(["deployment", "unknown"]);
  assert.equal(usage.status, 2);
  assert.match(usage.stderr, /^error: unknown deployment verb/);
  const help = run(["check-pack-set", "--help"]);
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /^usage:/);
});
