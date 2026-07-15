import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { renderAuthoringJson } from "../node-src/authoring/json.js";
import {
  openApiOperationProfile,
  renderProviderProbeMarkdown,
  runProviderProbe,
  terraformSchemaHcl,
  validateProviderProbeRecipe,
} from "../node-src/authoring/provider-probe.js";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
const ARTIFACT_NAMES = [
  "openapi-map.json",
  "source-diagnostics.json",
  "source-registry.json",
  "summary.json",
  "summary.md",
] as const;

interface Fixture {
  readonly openApi: string;
  readonly recipe: string;
  readonly root: string;
  readonly schema: string;
  readonly source: string;
}

async function jsonFile(filename: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(filename), { recursive: true });
  await writeFile(filename, `${JSON.stringify(value)}\n`, "utf8");
}

async function fixture(): Promise<Fixture> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-provider-probe-node-"));
  const schema = path.join(root, "schema.json");
  const openApi = path.join(root, "openapi.json");
  const source = path.join(root, "provider");
  const recipe = path.join(root, "recipe.json");
  await jsonFile(schema, {
    provider_schemas: {
      "registry.terraform.io/example/example": {
        resource_schemas: {
          example_folder: {
            block: {
              attributes: {
                name: { required: true, type: "string" },
              },
            },
          },
        },
      },
    },
  });
  await jsonFile(openApi, {
    openapi: "3.0.3",
    paths: {
      "/api/folders": {
        get: { operationId: "RouteGetFolders", responses: { 200: { description: "ok" } } },
        post: { responses: { 200: { description: "ok" } } },
      },
      "/api/folders/{uid}": {
        get: { operationId: "RouteGetFolder", responses: { 200: { description: "ok" } } },
        patch: { responses: { 200: { description: "ok" } } },
      },
    },
  });
  await mkdir(path.join(source, "internal"), { recursive: true });
  await writeFile(path.join(source, "internal", "resource_folder.go"), [
    "package internal",
    "",
    "func resourceFolder() {",
    '    resourceName := "example_folder"',
    "    _ = resourceName",
    "    client.Provisioning.GetFolders(ctx)",
    '    client.Provisioning.GetFolder("abc")',
    "}",
    "",
  ].join("\n"), "utf8");
  await jsonFile(recipe, {
    api_prefix: "/api/",
    name: "example",
    openapi: { format: "json", path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: { path: "provider" },
    terraform_schema: { path: "schema.json" },
  });
  return { openApi, recipe, root, schema, source };
}

async function artifactBytes(workDirectory: string): Promise<Readonly<Record<string, string>>> {
  const entries = await Promise.all(ARTIFACT_NAMES.map(async (name) => {
    return [name, await readFile(path.join(workDirectory, "artifacts", name), "utf8")] as const;
  }));
  return Object.fromEntries(entries);
}

test("local provider probe writes deterministic provider-readiness artifacts", async (context) => {
  const data = await fixture();
  const workDirectory = path.join(data.root, "work");
  context.after(async () => rm(data.root, { force: true, recursive: true }));

  const first = await runProviderProbe({ recipe: data.recipe, workDirectory });
  const firstBytes = await artifactBytes(workDirectory);
  const second = await runProviderProbe({ recipe: data.recipe, workDirectory });
  const secondBytes = await artifactBytes(workDirectory);

  assert.deepEqual(second.summary, first.summary);
  assert.deepEqual(secondBytes, firstBytes);
  assert.equal(first.inputs.schema, path.join(workDirectory, "inputs", "provider-schema.json"));
  assert.equal(first.inputs.openapi, path.join(workDirectory, "inputs", "openapi.json"));
  assert.equal(first.inputs.source_root, data.source);
  const sourceEvidence = first.summary.source_evidence as Record<string, unknown>;
  const readCoverage = first.summary.registry_read_coverage as Record<string, unknown>;
  assert.equal(sourceEvidence.mapped, 1);
  assert.equal(readCoverage.read_resources, 1);
  assert.equal(readCoverage.matched, 1);
  assert.equal(readCoverage.coverage_ratio, 1);
  assert.deepEqual(first.summary.openapi_operation_profile, {
    get_operations: 2,
    missing_operation_ids: 2,
    operation_id_coverage_ratio: 0.5,
    operations: 4,
  });
  assert.match(firstBytes["summary.json"] ?? "", /"coverage_ratio": 1\.0/u);
  assert.match(firstBytes["summary.json"] ?? "", /"operation_id_coverage_ratio": 0\.5/u);
  assert.match(firstBytes["summary.md"] ?? "", /# Provider Probe: example/u);
  assert.match(firstBytes["summary.md"] ?? "", /## Artifacts/u);
  const registry = JSON.parse(firstBytes["source-registry.json"] ?? "{}") as {
    readonly example_folder?: { readonly read?: { readonly path?: string } };
  };
  assert.equal(registry.example_folder?.read?.path, "/api/folders/{uid}");
});

const configuredPython = process.env.PYTHON?.trim();
const nodeOnlySuite = process.env.INFRAWRIGHT_NODE_ONLY_TESTS === "1";
test(
  "local provider probe artifacts remain byte-compatible with Python",
  {
    skip: nodeOnlySuite || !configuredPython
      ? "set PYTHON outside the Node-only suite to run the retained migration differential"
      : false,
  },
  async (context) => {
    const data = await fixture();
    const workDirectory = path.join(data.root, "differential-work");
    context.after(async () => rm(data.root, { force: true, recursive: true }));
    const python = spawnSync(configuredPython as string, [
      "-m", "engine.provider_probe", data.recipe, "--work-dir", workDirectory,
    ], { cwd: ROOT, encoding: "utf8", env: process.env });
    assert.equal(python.status, 0, python.stderr);
    const expected = await artifactBytes(workDirectory);

    await rm(workDirectory, { force: true, recursive: true });
    await runProviderProbe({ recipe: data.recipe, workDirectory });
    assert.deepEqual(await artifactBytes(workDirectory), expected);
  },
);

test("provider probe recipe validation remains fail-closed", () => {
  const valid = {
    openapi: { path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    source: { path: "provider" },
  };
  assert.equal(validateProviderProbeRecipe(valid), valid);
  const cases: readonly [unknown, RegExp][] = [
    [[], /root must be an object/u],
    [{ ...valid, openapi: "openapi.json" }, /openapi must be an object/u],
    [{ ...valid, openapi: {} }, /openapi must include path or url/u],
    [{ ...valid, openapi: { path: "", url: "" } }, /openapi must include path or url/u],
    [{ ...valid, source: {} }, /source must include path or git/u],
    [{ ...valid, source: { path: "", git: "" } }, /source must include path or git/u],
    [{ ...valid, source: { git: "https:\/\/example.test\/provider.git" } }, /source\.ref/u],
    [{ ...valid, source: { git: "https:\/\/example.test\/provider.git", ref: "" } }, /source\.ref/u],
    [{ ...valid, openapi: { path: [] } }, /openapi\.path must be a string/u],
    [{ openapi: { path: "openapi.json" }, source: { path: "provider" } }, /provider_source/u],
    [{
      openapi: { path: "openapi.json" },
      provider_source: "registry.terraform.io/example/example",
      source: { path: "provider" },
    }, /provider_version or terraform_provider\.version/u],
  ];
  for (const [value, pattern] of cases) {
    assert.throws(() => validateProviderProbeRecipe(value), pattern);
  }
});

test("Terraform schema HCL is deterministic and uses HCL-compatible JSON strings", () => {
  assert.equal(terraformSchemaHcl(
    { source: "example/example", version: "1.2.3" },
    "registry.terraform.io/ignored/ignored",
  ), [
    "terraform {",
    "  required_providers {",
    "    example = {",
    '      source = "example/example"',
    '      version = "1.2.3"',
    "    }",
    "  }",
    "}",
    "",
  ].join("\n"));
  assert.match(terraformSchemaHcl(
    {},
    "registry.terraform.io/example/example-provider",
    "2.0.0",
  ), /example_provider = \{/u);
});

test("OpenAPI operation profiling ignores path metadata and uses Python half-even rounding", () => {
  const methods = ["get", "post", "put", "patch", "delete"] as const;
  const paths: Record<string, unknown> = {};
  for (let index = 0; index < 32; index += 1) {
    paths[`/items/${String(index)}`] = {
      parameters: [{ in: "path", name: "id" }],
      [methods[index % methods.length] as string]: index === 0
        ? { operationId: "OnlyDocumentedOperation" }
        : {},
    };
  }
  assert.deepEqual(openApiOperationProfile({ paths }), {
    get_operations: 7,
    missing_operation_ids: 31,
    operation_id_coverage_ratio: 0.0312,
    operations: 32,
  });
  assert.deepEqual(openApiOperationProfile({ paths: {} }), {
    get_operations: 0,
    missing_operation_ids: 0,
    operation_id_coverage_ratio: null,
    operations: 0,
  });
  assert.deepEqual(openApiOperationProfile({ paths: [] }), {
    get_operations: 0,
    missing_operation_ids: 0,
    operation_id_coverage_ratio: null,
    operations: 0,
  });
  assert.throws(
    () => openApiOperationProfile({ paths: ["unexpected"] }),
    /OpenAPI paths must be an object/u,
  );
  assert.throws(
    () => openApiOperationProfile({ paths: { "/items": ["unexpected"] } }),
    /OpenAPI path item must be an object/u,
  );
  assert.match(
    renderAuthoringJson({ operation_id_coverage_ratio: 1 }),
    /"operation_id_coverage_ratio": 1\.0/u,
  );
  assert.match(
    renderProviderProbeMarkdown({
      generic_openapi_map: {},
      openapi_operation_profile: openApiOperationProfile({ paths: {} }),
      provider: {},
      registry_read_coverage: {},
      source_evidence: {},
      warning_codes: [],
    }),
    /operationId coverage: `None`/u,
  );
});

test("provider probe schema materialization uses the injected Terraform host", async (context) => {
  const data = await fixture();
  const workDirectory = path.join(data.root, "terraform-work");
  const recipe = path.join(data.root, "terraform-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await jsonFile(recipe, {
    api_prefix: "/api/",
    name: "example",
    openapi: { path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: { path: "provider" },
    tools: { terraform: "fake-terraform" },
  });
  const calls: Array<{
    readonly arguments: readonly string[];
    readonly cwd?: string;
    readonly stdoutPath?: string;
  }> = [];

  await runProviderProbe({
    host: {
      download: async () => { throw new Error("download must not run for local fixture"); },
      run: async (arguments_, options = {}) => {
        calls.push({
          arguments: [...arguments_],
          ...(options.cwd === undefined ? {} : { cwd: options.cwd }),
          ...(options.stdoutPath === undefined ? {} : { stdoutPath: options.stdoutPath }),
        });
        if (arguments_[1] === "providers") {
          assert.ok(options.stdoutPath);
          await writeFile(options.stdoutPath, await readFile(data.schema, "utf8"), "utf8");
        }
        return "";
      },
    },
    recipe,
    workDirectory,
  });

  const terraformDirectory = path.join(workDirectory, "terraform-schema");
  assert.deepEqual(calls, [
    {
      arguments: ["fake-terraform", "init", "-backend=false"],
      cwd: terraformDirectory,
    },
    {
      arguments: ["fake-terraform", "providers", "schema", "-json"],
      cwd: terraformDirectory,
      stdoutPath: path.join(workDirectory, "inputs", "provider-schema.json"),
    },
  ]);
  assert.equal(await readFile(path.join(terraformDirectory, "main.tf"), "utf8"), [
    "terraform {",
    "  required_providers {",
    "    example = {",
    '      source = "example/example"',
    '      version = "1.2.3"',
    "    }",
    "  }",
    "}",
    "",
  ].join("\n"));
});

test("provider probe refuses to replace an unmarked source checkout", async (context) => {
  const data = await fixture();
  const workDirectory = path.join(data.root, "protected-work");
  const sourceDirectory = path.join(workDirectory, "source");
  const keep = path.join(sourceDirectory, "keep.txt");
  const recipe = path.join(data.root, "git-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await mkdir(sourceDirectory, { recursive: true });
  await writeFile(keep, "do not delete\n", "utf8");
  await jsonFile(recipe, {
    api_prefix: "/api/",
    name: "example",
    openapi: { path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: { git: "https://example.test/provider.git", ref: "v1.2.3" },
    terraform_schema: { path: "schema.json" },
  });
  await assert.rejects(
    runProviderProbe({ recipe, workDirectory }),
    /refusing to replace existing provider source directory without probe marker/u,
  );
  assert.equal(await readFile(keep, "utf8"), "do not delete\n");
});

test("provider probe clones an exact ref, marks ownership, and replaces only its own checkout", async (context) => {
  const data = await fixture();
  const workDirectory = path.join(data.root, "clone-work");
  const recipe = path.join(data.root, "clone-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await jsonFile(recipe, {
    api_prefix: "/api/",
    name: "example",
    openapi: { path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: {
      git: "https://example.test/provider.git",
      ref: "v1.2.3",
      subdir: "provider",
    },
    terraform_schema: { path: "schema.json" },
  });
  const calls: string[][] = [];
  const host = {
    download: async () => { throw new Error("download must not run"); },
    run: async (arguments_: readonly string[]) => {
      calls.push([...arguments_]);
      const cloneRoot = arguments_.at(-1) as string;
      await mkdir(path.join(cloneRoot, "provider", "internal"), { recursive: true });
      await writeFile(
        path.join(cloneRoot, "provider", "internal", "resource_folder.go"),
        await readFile(path.join(data.source, "internal", "resource_folder.go"), "utf8"),
        "utf8",
      );
      return "";
    },
  };
  await runProviderProbe({ host, recipe, workDirectory });
  await writeFile(path.join(workDirectory, "source", "stale"), "replace me\n", "utf8");
  await runProviderProbe({ host, recipe, workDirectory });
  assert.deepEqual(calls, [
    ["git", "clone", "--depth", "1", "--branch", "v1.2.3", "https://example.test/provider.git", path.join(workDirectory, "source")],
    ["git", "clone", "--depth", "1", "--branch", "v1.2.3", "https://example.test/provider.git", path.join(workDirectory, "source")],
  ]);
  assert.equal(
    await readFile(path.join(workDirectory, "source", ".infrawright-provider-probe-source"), "utf8"),
    "owned by engine.provider_probe; safe to replace on next probe run\n",
  );
  await assert.rejects(readFile(path.join(workDirectory, "source", "stale"), "utf8"), /ENOENT/u);
});

test("provider probe YAML conversion stays safe and URL failures clean temporary files", async (context) => {
  const data = await fixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const ruby = spawnSync("ruby", ["--version"], { encoding: "utf8" });
  if (ruby.status === 0) {
    const unsafe = path.join(data.root, "unsafe.yaml");
    const unsafeRecipe = path.join(data.root, "unsafe-recipe.json");
    await writeFile(unsafe, "--- !ruby/object:Object {}\n", "utf8");
    await jsonFile(unsafeRecipe, {
      name: "unsafe",
      openapi: { format: "yaml", path: "unsafe.yaml" },
      provider_source: "registry.terraform.io/example/example",
      provider_version: "1.2.3",
      source: { path: "provider" },
      terraform_schema: { path: "schema.json" },
    });
    await assert.rejects(
      runProviderProbe({ recipe: unsafeRecipe, workDirectory: path.join(data.root, "unsafe-work") }),
      /failed to parse OpenAPI as YAML/u,
    );
  }

  const missingUrl = "file:///definitely/missing/infrawright-openapi.json";
  const urlRecipe = path.join(data.root, "url-recipe.json");
  const urlWork = path.join(data.root, "url-work");
  await jsonFile(urlRecipe, {
    name: "url",
    openapi: { format: "json", url: missingUrl },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    source: { path: "provider" },
    terraform_schema: { path: "schema.json" },
  });
  await assert.rejects(
    runProviderProbe({ recipe: urlRecipe, workDirectory: urlWork }),
    /failed to fetch OpenAPI URL .*openapi\.raw/u,
  );
  await assert.rejects(readFile(path.join(urlWork, "inputs", "openapi.raw.tmp")), /ENOENT/u);
});

test("provider-probe CLI and Make target stay Node-only when Python is a tripwire", async (context) => {
  const data = await fixture();
  const bin = path.join(data.root, "bin");
  const tripwire = path.join(bin, "python-must-not-run");
  const marker = path.join(data.root, "python-was-invoked");
  const cliWork = path.join(data.root, "cli-work");
  const makeWork = path.join(data.root, "make-work");
  const out = path.join(data.root, "summary-copy.json");
  const markdown = path.join(data.root, "summary-copy.md");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await mkdir(bin);
  await writeFile(tripwire, [
    "#!/bin/sh",
    'printf invoked > "$PYTHON_TRIPWIRE"',
    "exit 97",
    "",
  ].join("\n"), "utf8");
  await chmod(tripwire, 0o755);
  const environment = {
    ...process.env,
    PYTHON: tripwire,
    PYTHON_TRIPWIRE: marker,
  };

  const cli = spawnSync(process.execPath, [
    CLI, "provider-probe", data.recipe,
    "--work-dir", cliWork,
    "--out", out,
    "--markdown", markdown,
  ], { cwd: ROOT, encoding: "utf8", env: environment });
  assert.equal(cli.status, 0, cli.stderr);
  assert.match(cli.stdout, /wrote .*summary\.json/u);
  assert.equal(
    await readFile(out, "utf8"),
    await readFile(path.join(cliWork, "artifacts", "summary.json"), "utf8"),
  );
  assert.doesNotMatch(await readFile(markdown, "utf8"), /## Artifacts/u);

  const make = spawnSync("make", [
    "--no-print-directory",
    "provider-probe",
    `RECIPE=${data.recipe}`,
    `WORK_DIR=${makeWork}`,
    `INFRAWRIGHT_CLI=${process.execPath} ${CLI}`,
    `PYTHON=${tripwire}`,
  ], { cwd: ROOT, encoding: "utf8", env: environment });
  assert.equal(make.status, 0, make.stderr);
  assert.match(make.stdout, /wrote .*summary\.md/u);
  await assert.rejects(readFile(marker, "utf8"), /ENOENT/u);
});

test("provider-probe CLI keeps concise exit-2 errors and opt-in stacks", async (context) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-provider-probe-error-"));
  const recipe = path.join(root, "invalid.json");
  context.after(async () => rm(root, { force: true, recursive: true }));
  await writeFile(recipe, "{not-json", "utf8");
  const concise = spawnSync(process.execPath, [CLI, "provider-probe", recipe], {
    cwd: ROOT,
    encoding: "utf8",
  });
  assert.equal(concise.status, 2);
  assert.equal(concise.stdout, "");
  assert.match(concise.stderr, /^error: /u);
  assert.doesNotMatch(concise.stderr, /\n\s*at /u);

  const debug = spawnSync(
    process.execPath,
    [CLI, "provider-probe", recipe, "--debug-traceback"],
    { cwd: ROOT, encoding: "utf8" },
  );
  assert.equal(debug.status, 2);
  assert.match(debug.stderr, /\n\s*at /u);
  assert.match(debug.stderr, /\nerror: /u);
});
