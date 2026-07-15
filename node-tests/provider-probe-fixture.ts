import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

export const PROVIDER_PROBE_ARTIFACT_NAMES = [
  "openapi-map.json",
  "source-diagnostics.json",
  "source-registry.json",
  "summary.json",
  "summary.md",
] as const;

export interface ProviderProbeFixture {
  readonly openApi: string;
  readonly recipe: string;
  readonly root: string;
  readonly schema: string;
  readonly source: string;
}

export async function writeProviderProbeJson(filename: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(filename), { recursive: true });
  await writeFile(filename, `${JSON.stringify(value)}\n`, "utf8");
}

export async function createProviderProbeFixture(): Promise<ProviderProbeFixture> {
  const root = await mkdtemp(path.join(os.tmpdir(), "infrawright-provider-probe-node-"));
  const schema = path.join(root, "schema.json");
  const openApi = path.join(root, "openapi.json");
  const source = path.join(root, "provider");
  const recipe = path.join(root, "recipe.json");
  await writeProviderProbeJson(schema, {
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
          example_graphql: { block: { attributes: {} } },
          example_missing: { block: { attributes: {} } },
        },
      },
    },
  });
  await writeProviderProbeJson(openApi, {
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
  await writeFile(path.join(source, "internal", "resource_graphql.go"), [
    "package internal",
    "",
    'import "github.com/shurcooL/githubv4"',
    "",
    "func resourceGraphql() {",
    '    resourceName := "example_graphql"',
    "    _ = resourceName",
    "    githubv4.NewRequest()",
    "}",
    "",
  ].join("\n"), "utf8");
  await writeProviderProbeJson(recipe, {
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

export async function providerProbeArtifactBytes(
  workDirectory: string,
): Promise<Readonly<Record<string, string>>> {
  const entries = await Promise.all(PROVIDER_PROBE_ARTIFACT_NAMES.map(async (name) => {
    return [name, await readFile(path.join(workDirectory, "artifacts", name), "utf8")] as const;
  }));
  return Object.fromEntries(entries);
}
