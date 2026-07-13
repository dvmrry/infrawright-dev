import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
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

test("module CLI generates and validates one real module without Python", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-modules-"));
  try {
    const common = [
      "--resource",
      "zpa_segment_group",
      "--out",
      directory,
      "--root",
      path.join(ROOT, "packs"),
      "--profile",
      path.join(ROOT, "packsets", "full.json"),
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
    ];
    const generated = run(
      ["modules", "generate", ...common, "--terraform", "terraform"],
      { PYTHON: path.join(directory, "python-must-not-run") },
    );
    assert.equal(generated.status, 0, generated.stderr);
    assert.equal(
      generated.stdout,
      `generated 1 module(s), 7 file(s), in ${directory}\n`,
    );
    assert.match(generated.stderr, /zpa_segment_group\/main\.tf/);
    const validated = run(
      ["modules", "validate", ...common],
      { PYTHON: path.join(directory, "python-must-not-run") },
    );
    assert.equal(validated.status, 0, validated.stderr);
    assert.equal(
      validated.stdout,
      `validated generated module tree ${directory}: 1 module(s)\n`,
    );
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

test("fetch and fetch-diag run through Node with Python unavailable", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-fetch-"));
  try {
    const packs = path.join(directory, "packs");
    const output = path.join(directory, "pulls", "tenant-a");
    await mkdir(packs, { recursive: true });
    const environment = {
      HTTP_PROXY: "",
      HTTPS_PROXY: "",
      NO_PROXY: "",
      PYTHON: path.join(directory, "python-must-not-run"),
      REQUESTS_CA_BUNDLE: "",
      SSL_CERT_FILE: "",
      http_proxy: "",
      https_proxy: "",
      no_proxy: "",
    };
    const metadata = [
      "--root",
      packs,
      "--profile",
      path.join(ROOT, "packsets", "empty.json"),
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
    ];
    const fetched = run([
      "fetch",
      "--tenant",
      "tenant-a",
      "--out",
      output,
      ...metadata,
    ], environment);
    assert.equal(fetched.status, 0, fetched.stderr);
    assert.equal(fetched.stdout, "");
    assert.match(fetched.stderr, /fetch: auth mode = oneapi/);
    assert.equal((await stat(output)).isDirectory(), true);

    const diagnosed = run(["fetch-diag", ...metadata], environment);
    assert.equal(diagnosed.status, 0, diagnosed.stderr);
    assert.equal(diagnosed.stdout, "");
    assert.equal(diagnosed.stderr, "");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("Make fetch targets invoke the Node CLI instead of Python", async () => {
  const makefile = await readFile(path.join(ROOT, "Makefile"), "utf8");
  const fetchBlock = makefile.slice(
    makefile.indexOf("fetch: metadata-cli"),
    makefile.indexOf("gen-env:"),
  );
  assert.match(fetchBlock, /\$\(INFRAWRIGHT_CLI\) fetch --tenant/);
  assert.match(fetchBlock, /\$\(INFRAWRIGHT_CLI\) fetch-diag/);
  assert.doesNotMatch(fetchBlock, /\$\(PYTHON\)/);
});
