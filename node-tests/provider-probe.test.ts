import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { renderAuthoringJson } from "../node-src/authoring/json.js";
import { buildOpenApiResourceMap } from "../node-src/authoring/openapi-resource-map.js";
import {
  openApiOperationProfile,
  renderProviderProbeMarkdown,
  runProviderProbe,
  terraformSchemaHcl,
  validateProviderProbeRecipe,
} from "../node-src/authoring/provider-probe.js";
import {
  createProviderProbeFixture,
  providerProbeArtifactBytes,
  writeProviderProbeJson,
} from "./provider-probe-fixture.js";

const ROOT = process.cwd();
const CLI = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");

test("committed provider recipes pin remote OpenAPI URLs", async () => {
  for (const name of ["digitalocean", "github"]) {
    const recipe = JSON.parse(await readFile(
      path.join(ROOT, "docs", "recipes", "providers", `${name}.json`),
      "utf8",
    )) as { readonly openapi: { readonly url: string } };
    assert.doesNotMatch(recipe.openapi.url, /\/(?:main|master)\//u, name);
  }
});

test("local provider probe writes deterministic provider-readiness artifacts", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "work");
  context.after(async () => rm(data.root, { force: true, recursive: true }));

  const first = await runProviderProbe({ recipe: data.recipe, workDirectory });
  const firstBytes = await providerProbeArtifactBytes(workDirectory);
  const second = await runProviderProbe({ recipe: data.recipe, workDirectory });
  const secondBytes = await providerProbeArtifactBytes(workDirectory);

  assert.deepEqual(second.summary, first.summary);
  assert.deepEqual(secondBytes, firstBytes);
  assert.equal(first.inputs.schema, path.join(workDirectory, "inputs", "provider-schema.json"));
  assert.equal(first.inputs.openapi, path.join(workDirectory, "inputs", "openapi.json"));
  assert.equal(first.inputs.source_root, data.source);
  const sourceEvidence = first.summary.source_evidence as Record<string, unknown>;
  const readCoverage = first.summary.registry_read_coverage as Record<string, unknown>;
  assert.equal(sourceEvidence.mapped, 1);
  assert.equal(readCoverage.read_resources, 3);
  assert.equal(readCoverage.matched, 1);
  assert.equal(readCoverage.coverage_ratio, 0.3333);
  assert.deepEqual(first.summary.openapi_operation_profile, {
    get_operations: 2,
    missing_operation_ids: 2,
    operation_id_coverage_ratio: 0.5,
    operations: 4,
  });
  assert.match(firstBytes["summary.json"] ?? "", /"coverage_ratio": 0\.3333/u);
  assert.match(firstBytes["summary.json"] ?? "", /"operation_id_coverage_ratio": 0\.5/u);
  assert.match(firstBytes["summary.md"] ?? "", /# Provider Probe: example/u);
  assert.match(firstBytes["summary.md"] ?? "", /## Artifacts/u);
  const registry = JSON.parse(firstBytes["source-registry.json"] ?? "{}") as {
    readonly example_folder?: { readonly read?: { readonly path?: string } };
  };
  assert.equal(registry.example_folder?.read?.path, "/api/folders/{uid}");
});

test("artifact publication preserves the complete prior set when a staged write fails", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "atomic-work");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await runProviderProbe({ recipe: data.recipe, workDirectory });
  const accepted = await providerProbeArtifactBytes(workDirectory);
  let writes = 0;

  await assert.rejects(
    runProviderProbe({
      artifactWriter: async (filename, contents) => {
        writes += 1;
        if (writes === 3) throw new Error("injected staged artifact failure");
        await writeFile(filename, contents, "utf8");
      },
      recipe: data.recipe,
      workDirectory,
    }),
    /injected staged artifact failure/u,
  );

  assert.equal(writes, 3);
  assert.deepEqual(await providerProbeArtifactBytes(workDirectory), accepted);
  const workEntries = await readdir(workDirectory);
  assert.equal(
    workEntries.some((name) => name.startsWith(".provider-probe-artifacts-")),
    false,
  );
});

test("source-read surface evidence emits Python nulls for every absent optional field", () => {
  const report = buildOpenApiResourceMap({
    apiPrefix: "/api/",
    openApi: { openapi: "3.0.3", paths: {} },
    providerSource: "registry.terraform.io/example/example",
    registryData: {
      example_ambiguous: {
        product: "example",
        reason: "ambiguous_source_operation",
        status: "ambiguous_source_operation",
      },
      example_graphql: {
        product: "example",
        reason: "graphql_source",
        status: "graphql_source",
      },
      example_missing: {
        product: "example",
        read: { path: "/api/missing" },
        status: "mapped",
      },
    },
    resourcePrefix: "example",
    schemaData: {
      provider_schemas: {
        "registry.terraform.io/example/example": { resource_schemas: {} },
      },
    },
  });
  assert.doesNotThrow(() => renderAuthoringJson(report));
  const surfaceMap = report.surface_map as {
    readonly records: readonly {
      readonly evidence: readonly Record<string, unknown>[];
      readonly source: string;
    }[];
  };
  const sourceRead = surfaceMap.records.filter((item) => item.source === "source_read_registry");
  assert.equal(sourceRead.length, 3);
  for (const record of sourceRead) {
    assert.notEqual(record.evidence[0]?.operation_id, undefined);
    assert.notEqual(record.evidence[0]?.path_kind, undefined);
    assert.notEqual(record.evidence[0]?.read_path, undefined);
  }
});

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
  assert.equal(terraformSchemaHcl(
    { local_name: "", source: "", version: "" },
    "registry.terraform.io/example/multi-part-provider",
    "1.2.3",
  ), [
    "terraform {",
    "  required_providers {",
    "    multi_part_provider = {",
    '      source = "example/multi-part-provider"',
    '      version = "1.2.3"',
    "    }",
    "  }",
    "}",
    "",
  ].join("\n"));
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
  const binaryTiePaths = Object.fromEntries(Array.from({ length: 160 }, (_, index) => [
    `/binary-tie/${String(index)}`,
    { get: index === 0 ? { operationId: "OnlyDocumentedOperation" } : {} },
  ]));
  assert.equal(
    openApiOperationProfile({ paths: binaryTiePaths }).operation_id_coverage_ratio,
    0.0063,
  );
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
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "terraform-work");
  const recipe = path.join(data.root, "terraform-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await writeProviderProbeJson(recipe, {
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

test("provider probe command failures retain bounded streamed and file output", async (context) => {
  const data = await createProviderProbeFixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const recipe = path.join(data.root, "command-failure-recipe.json");
  const terraform = path.join(data.root, "fake-terraform");
  await writeProviderProbeJson(recipe, {
    name: "command-failure",
    openapi: { path: "openapi.json" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: { path: "provider" },
    tools: { terraform },
  });
  await writeFile(terraform, [
    "#!/usr/bin/env node",
    'const { writeFileSync } = require("node:fs");',
    'writeFileSync(1, "x".repeat(70000) + "STDOUT-SIGNAL\\n");',
    'writeFileSync(2, "y".repeat(70000) + "STDERR-SIGNAL\\n");',
    "process.exit(7);",
    "",
  ].join("\n"), "utf8");
  await chmod(terraform, 0o755);
  await assert.rejects(
    runProviderProbe({ recipe, workDirectory: path.join(data.root, "streamed-failure") }),
    (error: unknown) => {
      assert.ok(error instanceof Error);
      assert.match(error.message, /command failed \(7\)/u);
      assert.match(error.message, /stdout:\n\.\.\. <truncated \d+ chars>/u);
      assert.match(error.message, /STDOUT-SIGNAL/u);
      assert.match(error.message, /stderr:\n\.\.\. <truncated \d+ chars>/u);
      assert.match(error.message, /STDERR-SIGNAL/u);
      return true;
    },
  );

  await writeFile(terraform, [
    "#!/usr/bin/env node",
    'if (process.argv[2] === "init") process.exit(0);',
    'process.stdout.write("FILE-STDOUT-SIGNAL\\n");',
    'process.stderr.write("FILE-STDERR-SIGNAL\\n");',
    "process.exit(9);",
    "",
  ].join("\n"), "utf8");
  await assert.rejects(
    runProviderProbe({ recipe, workDirectory: path.join(data.root, "file-failure") }),
    (error: unknown) => {
      assert.ok(error instanceof Error);
      assert.match(error.message, /command failed \(9\)/u);
      assert.match(error.message, /FILE-STDOUT-SIGNAL/u);
      assert.match(error.message, /FILE-STDERR-SIGNAL/u);
      return true;
    },
  );
});

test("empty recipe primaries fall back to URL, git, Terraform, and derived HCL values", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "falsey-work");
  const recipe = path.join(data.root, "falsey-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await writeProviderProbeJson(recipe, {
    api_prefix: "/api/",
    name: "falsey",
    openapi: { format: "", path: "", url: "https://example.test/openapi.json" },
    provider_source: "registry.terraform.io/example/multi-part-provider",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: {
      git: "https://example.test/provider.git",
      path: "",
      ref: "v1.2.3",
    },
    terraform_provider: { local_name: "", source: "", version: "" },
    terraform_schema: { path: "" },
    tools: { terraform: "fake-terraform" },
  });
  const calls: string[][] = [];
  const downloads: string[] = [];
  await runProviderProbe({
    host: {
      download: async (url, destination) => {
        downloads.push(url);
        await writeFile(destination, await readFile(data.openApi));
      },
      run: async (arguments_, options = {}) => {
        calls.push([...arguments_]);
        if (arguments_[0] === "git") {
          const cloneRoot = arguments_.at(-1) as string;
          await mkdir(path.join(cloneRoot, "internal"), { recursive: true });
          await writeFile(
            path.join(cloneRoot, "internal", "resource_folder.go"),
            await readFile(path.join(data.source, "internal", "resource_folder.go")),
          );
        }
        if (arguments_[1] === "providers") {
          assert.ok(options.stdoutPath);
          await writeProviderProbeJson(options.stdoutPath, {
            provider_schemas: {
              "registry.terraform.io/example/multi-part-provider": {
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
        }
        return "";
      },
    },
    recipe,
    workDirectory,
  });
  assert.deepEqual(downloads, ["https://example.test/openapi.json"]);
  assert.deepEqual(calls.map((call) => call.slice(0, 2)), [
    ["fake-terraform", "init"],
    ["fake-terraform", "providers"],
    ["git", "clone"],
  ]);
  assert.match(
    await readFile(path.join(workDirectory, "terraform-schema", "main.tf"), "utf8"),
    /multi_part_provider = \{[\s\S]*source = "example\/multi-part-provider"[\s\S]*version = "1\.2\.3"/u,
  );
});

test("provider probe refuses to replace an unmarked source checkout", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "protected-work");
  const sourceDirectory = path.join(workDirectory, "source");
  const keep = path.join(sourceDirectory, "keep.txt");
  const recipe = path.join(data.root, "git-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await mkdir(sourceDirectory, { recursive: true });
  await writeFile(keep, "do not delete\n", "utf8");
  await writeProviderProbeJson(recipe, {
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

test("provider probe clones the requested branch or tag and replaces only its own checkout", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = path.join(data.root, "clone-work");
  const recipe = path.join(data.root, "clone-recipe.json");
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await writeProviderProbeJson(recipe, {
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
  const data = await createProviderProbeFixture();
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const implicitYaml = path.join(data.root, "implicit-yaml.txt");
  const implicitYamlRecipe = path.join(data.root, "implicit-yaml-recipe.json");
  await writeFile(implicitYaml, "openapi: 3.0.3\npaths: {}\n", "utf8");
  await writeProviderProbeJson(implicitYamlRecipe, {
    name: "implicit-yaml",
    openapi: { path: "implicit-yaml.txt" },
    provider_source: "registry.terraform.io/example/example",
    provider_version: "1.2.3",
    source: { path: "provider" },
    terraform_schema: { path: "schema.json" },
  });
  await assert.rejects(
    runProviderProbe({
      recipe: implicitYamlRecipe,
      workDirectory: path.join(data.root, "implicit-yaml-work"),
    }),
    /failed to parse OpenAPI as JSON.*openapi\.format/u,
  );
  const ruby = spawnSync("ruby", ["--version"], { encoding: "utf8" });
  if (ruby.status === 0) {
    const unsafe = path.join(data.root, "unsafe.yaml");
    const unsafeRecipe = path.join(data.root, "unsafe-recipe.json");
    await writeFile(unsafe, "--- !ruby/object:Object {}\n", "utf8");
    await writeProviderProbeJson(unsafeRecipe, {
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
  await writeProviderProbeJson(urlRecipe, {
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
  const data = await createProviderProbeFixture();
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
