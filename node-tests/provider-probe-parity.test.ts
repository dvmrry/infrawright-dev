import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmod, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { runProviderProbe } from "../node-src/authoring/provider-probe.js";
import {
  createProviderProbeFixture,
  providerProbeArtifactBytes,
  writeProviderProbeJson,
} from "./provider-probe-fixture.js";
import { PYTHON_ORACLE } from "./python-oracle.js";

const ROOT = process.cwd();

test("local provider probe artifacts remain byte-compatible with Python", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = `${data.root}/differential-work`;
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  const python = spawnSync(PYTHON_ORACLE, [
    "-m", "engine.provider_probe", data.recipe, "--work-dir", workDirectory,
  ], { cwd: ROOT, encoding: "utf8", env: process.env });
  assert.equal(python.status, 0, python.stderr);
  const expected = await providerProbeArtifactBytes(workDirectory);

  await rm(workDirectory, { force: true, recursive: true });
  await runProviderProbe({ recipe: data.recipe, workDirectory });
  assert.deepEqual(await providerProbeArtifactBytes(workDirectory), expected);
});

test("empty recipe primaries use the same fallbacks and bytes as Python", async (context) => {
  const data = await createProviderProbeFixture();
  const sourceRepository = path.join(data.root, "source-repository");
  const targetSchema = path.join(data.root, "target-schema.json");
  const terraform = path.join(data.root, "fake-terraform");
  const recipe = path.join(data.root, "falsey-recipe.json");
  const workDirectory = path.join(data.root, "falsey-differential-work");
  context.after(async () => rm(data.root, { force: true, recursive: true }));

  await mkdir(path.join(sourceRepository, "internal"), { recursive: true });
  await writeFile(
    path.join(sourceRepository, "internal", "resource_folder.go"),
    await readFile(path.join(data.source, "internal", "resource_folder.go")),
  );
  for (const arguments_ of [
    ["init", "-q", sourceRepository],
    ["-C", sourceRepository, "add", "."],
    [
      "-C", sourceRepository,
      "-c", "user.name=Infrawright Test",
      "-c", "user.email=infrawright@example.test",
      "commit", "-q", "-m", "fixture",
    ],
    ["-C", sourceRepository, "tag", "v1.2.3"],
  ]) {
    const git = spawnSync("git", arguments_, { encoding: "utf8" });
    assert.equal(git.status, 0, git.stderr);
  }
  await writeProviderProbeJson(targetSchema, {
    provider_schemas: {
      "registry.terraform.io/example/multi-part-provider": {
        resource_schemas: {
          example_folder: {
            block: { attributes: { name: { required: true, type: "string" } } },
          },
        },
      },
    },
  });
  await writeFile(terraform, [
    "#!/usr/bin/env node",
    'import { readFileSync } from "node:fs";',
    `if (process.argv[2] === "providers") process.stdout.write(readFileSync(${JSON.stringify(targetSchema)}, "utf8"));`,
    "",
  ].join("\n"), "utf8");
  await chmod(terraform, 0o755);
  await writeProviderProbeJson(recipe, {
    name: "",
    openapi: {
      format: "",
      path: "",
      url: pathToFileURL(data.openApi).href,
    },
    provider_source: "registry.terraform.io/example/multi-part-provider",
    provider_version: "1.2.3",
    resource_prefix: "example",
    source: {
      git: sourceRepository,
      path: "",
      ref: "v1.2.3",
      subdir: "",
    },
    terraform_provider: { local_name: "", source: "", version: "" },
    terraform_schema: { path: "" },
    tools: { terraform },
  });

  const python = spawnSync(PYTHON_ORACLE, [
    "-m", "engine.provider_probe", recipe, "--work-dir", workDirectory,
  ], { cwd: ROOT, encoding: "utf8", env: process.env });
  assert.equal(python.status, 0, python.stderr);
  const expected = await providerProbeArtifactBytes(workDirectory);

  await rm(workDirectory, { force: true, recursive: true });
  await runProviderProbe({ recipe, workDirectory });
  assert.deepEqual(await providerProbeArtifactBytes(workDirectory), expected);
  assert.match(
    await readFile(path.join(workDirectory, "terraform-schema", "main.tf"), "utf8"),
    /multi_part_provider = \{[\s\S]*source = "example\/multi-part-provider"[\s\S]*version = "1\.2\.3"/u,
  );
});
