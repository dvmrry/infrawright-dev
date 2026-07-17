import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { chmod, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

import { runProviderProbe } from "../node-src/authoring/provider-probe.js";
import {
  createProviderProbeFixture,
  PROVIDER_PROBE_ARTIFACT_NAMES,
  providerProbeArtifactBytes,
  writeProviderProbeJson,
} from "./provider-probe-fixture.js";

const ROOT = process.cwd();

interface FrozenProviderProbeCase {
  readonly artifacts: Readonly<Record<string, string>>;
  readonly name: string;
}

interface FrozenProviderProbeAuthority {
  readonly authority: {
    readonly implementation: string;
    readonly python_version: string;
    readonly unicode_version: string;
  };
  readonly cases: readonly FrozenProviderProbeCase[];
  readonly kind: string;
  readonly normalization: string;
  readonly producing_baseline: string;
  readonly schema_version: number;
  readonly source_blobs: Readonly<Record<string, string>>;
}

const PROVIDER_PROBE_AUTHORITY_SHA256 =
  "235cdbad249822ee70f3b947feffbc802af3a357a8fdf5d108f2454b78838824";
const providerProbeAuthorityBytes = readFileSync(path.join(
  ROOT,
  "node-tests",
  "fixtures",
  "python-provider-probe-v1.json",
));
assert.equal(
  createHash("sha256").update(providerProbeAuthorityBytes).digest("hex"),
  PROVIDER_PROBE_AUTHORITY_SHA256,
  "frozen CPython provider-probe authority changed",
);
const providerProbeAuthority = JSON.parse(
  providerProbeAuthorityBytes.toString("utf8"),
) as FrozenProviderProbeAuthority;
assert.equal(providerProbeAuthority.kind, "infrawright.python-provider-probe-authority");
assert.equal(providerProbeAuthority.schema_version, 1);
assert.equal(
  providerProbeAuthority.producing_baseline,
  "501bd09384aa2e825342083141abc11789ed9bb1",
);
assert.equal(
  providerProbeAuthority.normalization,
  "replace the ephemeral fixture root absolute path with <fixture-root>; no other byte normalization",
);
assert.deepEqual(providerProbeAuthority.authority, {
  implementation: "CPython",
  python_version: "3.13.13",
  unicode_version: "15.1.0",
});
assert.deepEqual(providerProbeAuthority.source_blobs, {
  node_authoring_json: "1949cc56686c39b141f66c152be5457b67fd820b",
  node_json_control: "cf977f680817aae2105f8e601d290a32461e4782",
  node_metadata_validation: "c6111184b0c2aa8b65883be73ac9150506547f4a",
  node_openapi: "241e8e2234516f239ec673d7ec2ac5ff362c9bae",
  node_openapi_resource_map: "aa0ade348b3c8b0208f4248b0c1a968f03e727ab",
  node_provider_probe: "539729822e6048062b4859d11fb5d3da2cbec5f7",
  node_python_compatible: "a95ef511c10bb1c727ca6a5f9616909acdea12c3",
  node_reconcile_schema_api: "719b0cdaf1bc3f2577c53e1b278081c22e9b9322",
  node_sdk_path_evidence: "81360bfcb29c7693305c9020169bc46d429ec16d",
  node_source_operation_map: "11aed805437e75cc6d3d5904b075bc69b17ba02b",
  python_openapi_resource_map: "0f02f07a880efe4ab23a4ecf89c74cc0834eea01",
  python_provider_probe: "19e7b869f828c3ba8ec3cbcd7d54f29437583ee4",
  python_reconcile_schema_api: "1980aa6546e3cb1e919278bbed0fbb425902ac48",
  python_sdk_path_evidence: "3afe340e6202dd5b8470805de9d9becb207ff503",
  python_source_operation_map: "ed730b0757d3fc406f54a9c2bf96a649db9f3292",
  python_tfschema: "91cdb7cdd4e7d7e325fa7d679377de0e230ad989",
  test: "d0823abbf490c05a4b7d8f2e0246fa027cfb1ce1",
  test_helper: "504be5f8f13026dfb38fb1cf5d020b0a38aec818",
});

function authorityCase(name: string): FrozenProviderProbeCase {
  const matches = providerProbeAuthority.cases.filter((entry) => entry.name === name);
  assert.equal(matches.length, 1, `expected one frozen provider-probe case named ${name}`);
  return matches[0]!;
}

function normalizeArtifacts(
  artifacts: Readonly<Record<string, string>>,
  fixtureRoot: string,
): Readonly<Record<string, string>> {
  return Object.fromEntries(Object.entries(artifacts).map(([name, bytes]) => [
    name,
    bytes.replaceAll(fixtureRoot, "<fixture-root>"),
  ]));
}

function assertExactArtifacts(
  actual: Readonly<Record<string, string>>,
  expected: Readonly<Record<string, string>>,
): void {
  assert.deepEqual(Object.keys(actual).sort(), [...PROVIDER_PROBE_ARTIFACT_NAMES].sort());
  assert.deepEqual(Object.keys(expected).sort(), [...PROVIDER_PROBE_ARTIFACT_NAMES].sort());
  for (const name of PROVIDER_PROBE_ARTIFACT_NAMES) {
    assert.equal(actual[name], expected[name], `${name} differs from frozen CPython bytes`);
  }
}

test("local provider probe artifacts remain byte-compatible with Python", async (context) => {
  const data = await createProviderProbeFixture();
  const workDirectory = `${data.root}/differential-work`;
  context.after(async () => rm(data.root, { force: true, recursive: true }));
  await runProviderProbe({ recipe: data.recipe, workDirectory });
  assertExactArtifacts(
    normalizeArtifacts(await providerProbeArtifactBytes(workDirectory), data.root),
    authorityCase("local-provider-probe").artifacts,
  );
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

  await runProviderProbe({ recipe, workDirectory });
  assertExactArtifacts(
    normalizeArtifacts(await providerProbeArtifactBytes(workDirectory), data.root),
    authorityCase("empty-recipe-primaries").artifacts,
  );
  assert.match(
    await readFile(path.join(workDirectory, "terraform-schema", "main.tf"), "utf8"),
    /multi_part_provider = \{[\s\S]*source = "example\/multi-part-provider"[\s\S]*version = "1\.2\.3"/u,
  );
});
