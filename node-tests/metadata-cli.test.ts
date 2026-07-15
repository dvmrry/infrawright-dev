import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cp, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
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

test("check-pack preserves encounter-order last-wins selection", () => {
  const result = run([
    "check-pack",
    "PACK=zia",
    "--pack", "zpa",
    "PACK=zia",
  ]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "validated packs: zia\n");
});

test("metadata CLI preserves exact legacy option spellings", () => {
  const inlinePack = run(["check-pack", "--pack=zia"]);
  assert.equal(inlinePack.status, 2);
  assert.match(inlinePack.stderr, /unknown argument --pack=zia/u);

  const clusteredHelp = run(["check-pack", "-hh"]);
  assert.equal(clusteredHelp.status, 2);
  assert.match(clusteredHelp.stderr, /unknown argument -hh/u);

  const splitOrder = run(["resources", "--order", "references"]);
  assert.equal(splitOrder.status, 2);
  assert.match(splitOrder.stderr, /resources does not accept --order/u);

  const inlineOrder = run(["resources", "--order=references"]);
  assert.equal(inlineOrder.status, 0, inlineOrder.stderr);
});

test("ZIA admin-role evidence names the pinned SDK source path", async () => {
  const fixture = JSON.parse(await readFile(
    path.join(ROOT, "node-tests", "fixtures", "zia-adoption-classification-v4.7.26.json"),
    "utf8",
  )) as {
    readonly resources: {
      readonly zia_admin_roles: { readonly evidence: readonly string[] };
    };
  };
  assert.deepEqual(fixture.resources.zia_admin_roles.evidence, [
    "https://github.com/zscaler/zscaler-sdk-go/blob/v3.8.40/zscaler/zia/services/adminuserrolemgmt/roles/adminroles.go#L64-L65",
  ]);
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

test("check-pack rejects unsupported rules scoped to a stale provider source or pin", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-unsupported-scope-"));
  const registryPath = path.join(directory, "sample", "registry.json");
  const rule = {
    evidence: ["https://example.invalid/provider-source"],
    match: { action: "ISOLATE" },
    provider: { source: "example/sample", version: "1.2.3" },
    reason: "provider cannot round-trip this object",
  };
  try {
    await writeJson(path.join(directory, "sample", "pack.json"), {
      pin: "1.2.3",
      provider_prefixes: { sample_: "sample" },
      provider_sources: { sample: "example/sample" },
    });
    for (const [field, value, expected] of [
      ["source", "example/stale", /provider\.source.*does not match active provider source/u],
      ["version", "9.9.9", /provider\.version.*does not match active provider pin/u],
    ] as const) {
      await writeJson(registryPath, {
        sample_resource: {
          adopt: {
            unsupported_if: [{
              ...rule,
              provider: { ...rule.provider, [field]: value },
            }],
          },
          product: "sample",
        },
      });
      const result = run(["check-pack", "--pack", "sample", "--root", directory]);
      assert.equal(result.status, 1, `${field}: ${result.stderr}`);
      assert.equal(result.stdout, "", field);
      assert.match(result.stderr, expected, field);
    }
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

test("selected module generation and validation expand complete deployment roots", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-grouped-modules-"));
  try {
    const deployment = path.join(directory, "deployment.json");
    const modulesA = path.join(directory, "modules-a");
    const modulesB = path.join(directory, "modules-b");
    await writeJson(deployment, {
      overlay: directory,
      module_dir: modulesA,
      roots: {
        zpa: {
          groups: {
            zpa_custom: ["zpa_segment_group", "zpa_server_group"],
          },
        },
      },
    });
    const metadata = [
      "--deployment",
      deployment,
      "--root",
      path.join(ROOT, "packs"),
      "--profile",
      path.join(ROOT, "packsets", "full.json"),
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
    ];

    const generatedA = run([
      "modules",
      "generate",
      ...metadata,
      "--resource",
      "zpa_segment_group",
      "--terraform",
      "terraform",
    ]);
    assert.equal(generatedA.status, 0, generatedA.stderr);
    assert.match(generatedA.stdout, /^generated 2 module\(s\),/u);
    assert.match(generatedA.stderr, /selecting zpa_segment_group selects whole root zpa_custom/u);
    await Promise.all([
      stat(path.join(modulesA, "zpa_segment_group", "main.tf")),
      stat(path.join(modulesA, "zpa_server_group", "main.tf")),
    ]);
    const validatedA = run([
      "modules",
      "validate",
      ...metadata,
      "--resource",
      "zpa_segment_group",
    ]);
    assert.equal(validatedA.status, 0, validatedA.stderr);
    assert.equal(
      validatedA.stdout,
      `validated generated module tree ${modulesA}: 2 module(s)\n`,
    );

    const generatedB = run([
      "modules",
      "generate",
      ...metadata,
      "--out",
      modulesB,
      "--resource",
      "zpa_server_group",
      "--terraform",
      "terraform",
    ]);
    assert.equal(generatedB.status, 0, generatedB.stderr);
    assert.match(generatedB.stdout, /^generated 2 module\(s\),/u);
    await Promise.all([
      stat(path.join(modulesB, "zpa_segment_group", "main.tf")),
      stat(path.join(modulesB, "zpa_server_group", "main.tf")),
    ]);
    const validatedB = run([
      "modules",
      "validate",
      ...metadata,
      "--out",
      modulesB,
      "--resource",
      "zpa_server_group",
    ]);
    assert.equal(validatedB.status, 0, validatedB.stderr);
    assert.equal(
      validatedB.stdout,
      `validated generated module tree ${modulesB}: 2 module(s)\n`,
    );

    const environment = run([
      "gen-env",
      "--tenant",
      "tenant",
      ...metadata,
      "--resource",
      "zpa_segment_group",
      "--terraform",
      "terraform",
    ]);
    assert.equal(environment.status, 0, environment.stderr);
    const rootMain = await readFile(
      path.join(directory, "envs", "tenant", "zpa_custom", "main.tf"),
      "utf8",
    );
    assert.match(rootMain, /module "zpa_segment_group"/u);
    assert.match(rootMain, /module "zpa_server_group"/u);
    for (const member of ["zpa_segment_group", "zpa_server_group"]) {
      await stat(path.join(modulesA, member, "main.tf"));
    }
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
    const performanceReport = path.join(directory, "fetch-performance.json");
    await mkdir(packs, { recursive: true });
    const environment = {
      HTTP_PROXY: "",
      HTTPS_PROXY: "",
      NO_PROXY: "",
      PYTHON: path.join(directory, "python-must-not-run"),
      INFRAWRIGHT_PERFORMANCE_REPORT: performanceReport,
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
      "--concurrency",
      "4",
      ...metadata,
    ], environment);
    assert.equal(fetched.status, 0, fetched.stderr);
    assert.equal(fetched.stdout, "");
    assert.match(fetched.stderr, /fetch: auth mode = oneapi/);
    assert.equal((await stat(output)).isDirectory(), true);
    const performance = JSON.parse(await readFile(performanceReport, "utf8")) as {
      command: string;
      selected_concurrency: number;
      summary: { http_requests: number };
    };
    assert.equal(performance.command, "fetch");
    assert.equal(performance.selected_concurrency, 4);
    assert.equal(performance.summary.http_requests, 0);

    for (const value of ["0", "-1", "1.5", "65", "abc"]) {
      const rejected = run(["fetch", "--tenant", "tenant-a", "--concurrency", value]);
      assert.equal(rejected.status, 2, value);
      assert.match(rejected.stderr, /--concurrency/);
    }

    const diagnosed = run(["fetch-diag", ...metadata], environment);
    assert.equal(diagnosed.status, 0, diagnosed.stderr);
    assert.equal(diagnosed.stdout, "");
    assert.equal(diagnosed.stderr, "");
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("nonzero Fetch, Transform, and Adopt results outrank report-write failure", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-report-failure-"));
  try {
    const input = path.join(directory, "pulls");
    await mkdir(input, { recursive: true });
    await writeFile(path.join(input, "zia_advanced_settings.json"), "{}\n", "utf8");
    const metadata = [
      "--root",
      path.join(ROOT, "packs"),
      "--profile",
      path.join(ROOT, "packsets", "full.json"),
      "--catalog",
      path.join(ROOT, "packsets", "full.json"),
      "--resource",
      "zia_advanced_settings",
    ];
    const commands = [
      ["transform", "--in", input, "--tenant", "tenant", ...metadata],
      ["adopt", "--in", input, "--tenant", "tenant", ...metadata],
      ["fetch", "--tenant", "tenant", "--out", path.join(directory, "fetch"), ...metadata],
    ];
    for (const arguments_ of commands) {
      const result = run(arguments_, {
        INFRAWRIGHT_PERFORMANCE_REPORT: directory,
        ZSCALER_CLIENT_ID: "",
        ZSCALER_CLIENT_SECRET: "",
        ZSCALER_USE_LEGACY_CLIENT: "",
      });
      assert.equal(result.status, 1, `${arguments_[0]}: ${result.stderr}`);
      assert.match(
        result.stderr,
        /WARNING: unable to write performance report after command failure/u,
        String(arguments_[0]),
      );
      assert.doesNotMatch(
        result.stderr,
        /^error: unable to write performance report$/mu,
        String(arguments_[0]),
      );
      assert.match(result.stderr, /FAILED|must be a JSON LIST/u, String(arguments_[0]));
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("Make fetch targets invoke the Node CLI instead of Python", async () => {
  const makefile = await readFile(path.join(ROOT, "Makefile"), "utf8");
  const fetchStart = makefile.search(/^fetch:/m);
  const genEnvStart = makefile.search(/^gen-env:/m);
  assert.notEqual(fetchStart, -1, "Makefile must define fetch");
  assert.ok(genEnvStart > fetchStart, "gen-env must follow the fetch targets");
  const fetchBlock = makefile.slice(fetchStart, genEnvStart);
  assert.match(fetchBlock, /\$\(INFRAWRIGHT_CLI\) fetch --tenant/);
  assert.match(fetchBlock, /\$\(INFRAWRIGHT_CLI\) fetch-diag/);
  assert.match(fetchBlock, /FETCH_CONCURRENCY/);
  assert.doesNotMatch(fetchBlock, /\$\(PYTHON\)/);
});

test("fetch CLI rejects an unknown external provider source before transport setup", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-authority-"));
  try {
    const packs = path.join(directory, "packs");
    await cp(path.join(ROOT, "packs", "zia"), path.join(packs, "zia"), {
      recursive: true,
    });
    await cp(
      path.join(ROOT, "packs", "_shared", "zscaler"),
      path.join(packs, "_shared", "zscaler"),
      { recursive: true },
    );
    const manifestPath = path.join(packs, "zia", "pack.json");
    const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as Record<string, unknown>;
    manifest.provider_sources = { zia: "example/external-zia" };
    await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
    const registryPath = path.join(packs, "zia", "registry.json");
    const registry = JSON.parse(await readFile(registryPath, "utf8")) as Record<
      string,
      { adopt?: Record<string, unknown> }
    >;
    for (const entry of Object.values(registry)) {
      if (entry.adopt !== undefined) delete entry.adopt.unsupported_if;
    }
    await writeFile(registryPath, `${JSON.stringify(registry, null, 2)}\n`);
    const output = path.join(directory, "must-not-be-created");
    const metadata = [
      "--root", packs,
      "--profile", path.join(ROOT, "packsets", "zia.json"),
      "--catalog", path.join(ROOT, "packsets", "full.json"),
    ];
    for (const arguments_ of [
      ["fetch", "--tenant", "tenant-a", "--out", output, ...metadata],
      ["fetch-diag", ...metadata],
    ]) {
      const result = run(arguments_, {
        REQUESTS_CA_BUNDLE: path.join(directory, "must-not-be-read.pem"),
      });
      assert.equal(result.status, 2, result.stderr);
      assert.equal(result.stdout, "");
      assert.match(result.stderr, /example\/external-zia.*not available/u);
      assert.doesNotMatch(result.stderr, /CA bundle/);
    }
    await assert.rejects(stat(output), /ENOENT/u);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("fetch CLI rejects a resource that borrows another product's collector authority", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-cli-provider-authority-"));
  try {
    const packs = path.join(directory, "packs");
    await cp(path.join(ROOT, "packs", "zia"), path.join(packs, "zia"), {
      recursive: true,
    });
    await cp(
      path.join(ROOT, "packs", "_shared", "zscaler"),
      path.join(packs, "_shared", "zscaler"),
      { recursive: true },
    );
    const manifestPath = path.join(packs, "zia", "pack.json");
    const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as Record<string, unknown>;
    manifest.provider_prefixes = { zia_: "rogue" };
    manifest.provider_sources = { rogue: "example/rogue" };
    await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
    const registryPath = path.join(packs, "zia", "registry.json");
    const registry = JSON.parse(await readFile(registryPath, "utf8")) as Record<
      string,
      { adopt?: Record<string, unknown> }
    >;
    for (const entry of Object.values(registry)) {
      if (entry.adopt !== undefined) delete entry.adopt.unsupported_if;
    }
    await writeFile(registryPath, `${JSON.stringify(registry, null, 2)}\n`);
    const output = path.join(directory, "must-not-be-created");
    const result = run([
      "fetch",
      "--tenant", "tenant-a",
      "--resource", "zia_url_categories",
      "--out", output,
      "--root", packs,
      "--profile", path.join(ROOT, "packsets", "zia.json"),
      "--catalog", path.join(ROOT, "packsets", "full.json"),
    ], {
      REQUESTS_CA_BUNDLE: path.join(directory, "must-not-be-read.pem"),
      ZSCALER_CLIENT_ID: "must-not-be-read",
      ZSCALER_CLIENT_SECRET: "must-not-be-read",
      ZSCALER_VANITY_DOMAIN: "must-not-be-read",
    });
    assert.equal(result.status, 2, result.stderr);
    assert.equal(result.stdout, "");
    assert.match(
      result.stderr,
      /zia_url_categories.*provider source "example\/rogue".*not available/u,
    );
    assert.doesNotMatch(result.stderr, /CA bundle|authentication|token host/u);
    await assert.rejects(stat(output), /ENOENT/u);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
