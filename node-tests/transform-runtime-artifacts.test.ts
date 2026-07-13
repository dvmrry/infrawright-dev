import assert from "node:assert/strict";
import {
  access,
  copyFile,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { decodeHTML } from "entities";

import { runTransformBatch } from "../node-src/domain/transform-runner.js";
import { pythonHtmlUnescapeGeneric } from "../node-src/domain/python-html-unescape.js";
import {
  renderTransformLookup,
  transformArtifactPaths,
} from "../node-src/domain/transform-artifacts.js";
import { transformLoadedItems } from "../node-src/domain/pull-transform.js";
import type { Deployment } from "../node-src/domain/types.js";
import { loadPackRoot, type LoadedPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const DEMO_INPUT = path.join(ROOT, "tests", "fixtures", "demo");
const DEMO_EXPECTED = path.join(ROOT, "tests", "fixtures", "demo-expected");
const TRANSFORM_FIXTURES = path.join(ROOT, "tests", "fixtures", "transform");

const DEMO_RESOURCES = [
  "zcc_device_cleanup",
  "zcc_failopen_policy",
  "zcc_forwarding_profile",
  "zcc_trusted_network",
  "zcc_web_privacy",
  "zia_bandwidth_control_rule",
  "zia_cloud_app_control_rule",
  "zia_dlp_web_rules",
  "zia_location_management",
  "zia_rule_labels",
  "zia_ssl_inspection_rules",
  "zia_url_categories",
  "zia_url_filtering_rules",
  "zpa_app_connector_group",
  "zpa_application_segment",
  "zpa_application_server",
  "zpa_microtenant_controller",
  "zpa_policy_access_rule",
  "zpa_segment_group",
  "zpa_server_group",
] as const;

const FIXTURE_RESOURCES = [
  "zia_cloud_app_control_rule",
  "zia_location_management",
  "zia_ssl_inspection_rules",
  "zia_url_categories",
  "zpa_application_segment",
  "zpa_segment_group",
  "zpa_server_group",
] as const;

let loadedRoot: Promise<LoadedPackRoot> | undefined;

function committedRoot(): Promise<LoadedPackRoot> {
  loadedRoot ??= loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  return loadedRoot;
}

function deployment(overlay: string, options?: {
  readonly hcl?: boolean;
  readonly roots?: Deployment["roots"];
}): Deployment {
  return {
    overlay,
    ...(options?.hcl === true ? { tfvars_format: "hcl" } : {}),
    roots: options?.roots ?? {},
  };
}

async function temporaryDirectory(
  context: { after(callback: () => Promise<unknown> | unknown): void },
  prefix: string,
): Promise<string> {
  const directory = await mkdtemp(path.join(os.tmpdir(), prefix));
  context.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

async function writeJson(file: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

async function exists(file: string): Promise<boolean> {
  try {
    await access(file);
    return true;
  } catch {
    return false;
  }
}

async function text(file: string): Promise<string> {
  return readFile(file, "utf8");
}

test("runTransformBatch materializes all 20 demo fixture goldens exactly", async (context) => {
  const output = await temporaryDirectory(context, "infrawright-demo-runtime-");
  const result = await runTransformBatch({
    deployment: deployment(output),
    inputDirectory: DEMO_INPUT,
    root: await committedRoot(),
    selectors: [...DEMO_RESOURCES],
    tenant: "demo",
  });

  assert.deepEqual(result.failed, []);
  assert.deepEqual(result.skipped, []);
  assert.deepEqual(new Set(result.processed), new Set(DEMO_RESOURCES));
  for (const resourceType of DEMO_RESOURCES) {
    assert.equal(
      await text(path.join(output, "config", "demo", `${resourceType}.auto.tfvars.json`)),
      await text(path.join(DEMO_EXPECTED, `${resourceType}.tfvars.json`)),
      `${resourceType} tfvars`,
    );
    assert.equal(
      await text(path.join(output, "imports", "demo", `${resourceType}_imports.tf`)),
      await text(path.join(DEMO_EXPECTED, `${resourceType}_imports.tf`)),
      `${resourceType} imports`,
    );
  }
});

test("runTransformBatch materializes all seven detailed transform goldens exactly", async (context) => {
  const output = await temporaryDirectory(context, "infrawright-transform-goldens-");
  const root = await committedRoot();
  for (const resourceType of FIXTURE_RESOURCES) {
    const input = path.join(output, "input", resourceType);
    await mkdir(input, { recursive: true });
    await copyFile(
      path.join(TRANSFORM_FIXTURES, resourceType, "api.json"),
      path.join(input, `${resourceType}.json`),
    );
    const result = await runTransformBatch({
      deployment: deployment(output),
      inputDirectory: input,
      root,
      selectors: [resourceType],
      tenant: "tenant",
    });
    assert.deepEqual(result.failed, [], resourceType);
    assert.deepEqual(result.skipped, [], resourceType);
    assert.deepEqual(result.processed, [resourceType], resourceType);
    assert.equal(
      await text(path.join(output, "config", "tenant", `${resourceType}.auto.tfvars.json`)),
      await text(path.join(TRANSFORM_FIXTURES, resourceType, "expected.auto.tfvars.json")),
      `${resourceType} tfvars`,
    );
    assert.equal(
      await text(path.join(output, "imports", "tenant", `${resourceType}_imports.tf`)),
      await text(path.join(TRANSFORM_FIXTURES, resourceType, "expected_imports.tf")),
      `${resourceType} imports`,
    );
  }
});

test("all 59 active overrides compile and accept an empty transform", async () => {
  const root = await committedRoot();
  const overridden = [...root.resources.values()].filter((resource) => {
    return resource.override !== null;
  });
  assert.equal(overridden.length, 59);
  for (const resource of overridden) {
    const result = transformLoadedItems({
      htmlUnescape: decodeHTML,
      rawItems: [],
      resource,
      schema: await root.loadResourceSchema(resource.type),
      unescapeHtml: false,
    });
    assert.deepEqual(Object.keys(result.items), [], resource.type);
    assert.deepEqual(Object.keys(result.originals), [], resource.type);
    assert.deepEqual(result.drops, [], resource.type);
  }
});

test("lookup rendering is sorted, survivor-based, unknown-safe, and last-key-wins", () => {
  assert.equal(renderTransformLookup({
    items: {
      alpha: { configured_name: "Alpha projected" },
      beta: { configured_name: "   " },
      omega: { configured_name: "Omega" },
    },
    originals: {
      alpha: { configured_name: "Raw Alpha", id: "CUSTOM_01" },
      beta: { id: "CUSTOM_02" },
      omega: { id: "CUSTOM_01" },
    },
    nameField: "configured_name",
  }), "{\n"
    + "  \"by_id\": {\n"
    + "    \"CUSTOM_01\": \"Omega\",\n"
    + "    \"CUSTOM_02\": \"<unknown>\"\n"
    + "  },\n"
    + "  \"key_by_id\": {\n"
    + "    \"CUSTOM_01\": \"omega\",\n"
    + "    \"CUSTOM_02\": \"beta\"\n"
    + "  }\n"
    + "}\n");
});

test("generic Python HTML unescape covers named, prefix, numeric, invalid, and two-pass inputs", () => {
  assert.equal(
    pythonHtmlUnescapeGeneric(
      "&NotEqualTilde; &notit; &#x80; &#1; &#xD800; &#xFDD0;",
    ),
    "≂̸ ¬it; €  � ",
  );
  assert.equal(pythonHtmlUnescapeGeneric("&amp;lt;"), "&lt;");
  assert.equal(
    pythonHtmlUnescapeGeneric(pythonHtmlUnescapeGeneric("&amp;lt;")),
    "<",
  );
});

test("a console rename creates a moved block and the next stable run removes it", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-moves-");
  const input = path.join(workspace, "input");
  const source = path.join(input, "zia_rule_labels.json");
  const root = await committedRoot();
  await writeJson(source, [{ id: 7, name: "Original Name" }]);
  const options = {
    deployment: deployment(workspace),
    inputDirectory: input,
    root,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  } as const;
  assert.deepEqual((await runTransformBatch(options)).failed, []);

  await writeJson(source, [{ id: 7, name: "Renamed Thing" }]);
  assert.deepEqual((await runTransformBatch(options)).failed, []);
  const moves = path.join(workspace, "imports", "tenant", "zia_rule_labels_moves.tf");
  assert.equal(await text(moves),
    "moved {\n"
      + "  from = module.zia_rule_labels.zia_rule_labels.this[\"original_name\"]\n"
      + "  to   = module.zia_rule_labels.zia_rule_labels.this[\"renamed_thing\"]\n"
      + "}\n");

  assert.deepEqual((await runTransformBatch(options)).failed, []);
  assert.equal(await exists(moves), false);
});

test("same-root references materialize generated binding JSON on the first batch", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-bindings-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zpa_segment_group.json"), [{
    enabled: true,
    id: "sg-1",
    name: "Segment One",
  }]);
  await writeJson(path.join(input, "zpa_application_segment.json"), [{
    domainNames: ["app.example.com"],
    id: "app-1",
    name: "App One",
    segmentGroupId: "sg-1",
  }]);
  const roots = {
    zpa: {
      bind_references: true,
      groups: {
        zpa_custom: ["zpa_application_segment", "zpa_segment_group"],
      },
    },
  } as const;
  const result = await runTransformBatch({
    deployment: deployment(workspace, { roots }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_application_segment", "zpa_segment_group"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  assert.equal(
    await text(path.join(
      workspace,
      "config",
      "tenant",
      "zpa_application_segment.generated.expressions.json",
    )),
    "{\n"
      + "  \"resources\": {\n"
      + "    \"zpa_application_segment.app_one\": {\n"
      + "      \"segment_group_id\": {\n"
      + "        \"expression\": \"module.zpa_segment_group.items[\\\"segment_one\\\"].id\",\n"
      + "        \"reason\": \"group-local reference binding via zpa_segment_group.items\"\n"
      + "      }\n"
      + "    }\n"
      + "  }\n"
      + "}\n",
  );
});

test("HCL deployment writes lookup-derived comments", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-hcl-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_url_categories.json"), [{
    configuredName: "Finance\nOps",
    customCategory: true,
    id: "CUSTOM_01",
    urls: ["finance.example.com"],
  }]);
  await writeJson(path.join(input, "zia_url_filtering_rules.json"), [{
    action: "BLOCK",
    id: 7,
    name: "Block Finance",
    order: 1,
    protocols: ["ANY_RULE"],
    urlCategories: ["CUSTOM_01", "GAMBLING"],
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace, { hcl: true }),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zia_url_filtering_rules", "zia_url_categories"],
    tenant: "tenant",
  });
  assert.deepEqual(result.failed, []);
  const rendered = await text(path.join(
    workspace,
    "config",
    "tenant",
    "zia_url_filtering_rules.auto.tfvars",
  ));
  assert.match(rendered, /"CUSTOM_01", # Finance Ops/);
  assert.match(rendered, /"GAMBLING", *# GAMBLING/);
  assert.equal(rendered.startsWith(
    "# Generated by infrawright. Do not edit; regenerate via make transform/adopt.\n",
  ), true);
});

test("derived reorder writes config only", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-derived-");
  const input = path.join(workspace, "input");
  await copyFile(
    path.join(DEMO_INPUT, "zpa_policy_access_rule.json"),
    path.join(await mkdir(input, { recursive: true }).then(() => input), "zpa_policy_access_rule.json"),
  );
  const result = await runTransformBatch({
    deployment: deployment(workspace),
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zpa_policy_access_rule_reorder"],
    tenant: "tenant",
  });
  assert.deepEqual(result, {
    failed: [],
    processed: ["zpa_policy_access_rule_reorder"],
    skipped: [],
  });
  const config = path.join(
    workspace,
    "config",
    "tenant",
    "zpa_policy_access_rule_reorder.auto.tfvars.json",
  );
  assert.match(await text(config), /"ACCESS_POLICY"/);
  assert.equal(await exists(path.join(
    workspace,
    "imports",
    "tenant",
    "zpa_policy_access_rule_reorder_imports.tf",
  )), false);
});

test("DROPS_CHECK records failure only after writing tfvars and imports", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-drops-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{
    id: 7,
    name: "Example",
    newlyObservedField: "must-review",
  }]);
  const result = await runTransformBatch({
    deployment: deployment(workspace),
    environment: { DROPS_CHECK: "1" },
    inputDirectory: input,
    root: await committedRoot(),
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  });
  assert.deepEqual(result, { failed: ["zia_rule_labels"], processed: [], skipped: [] });
  assert.equal(await exists(path.join(
    workspace,
    "config",
    "tenant",
    "zia_rule_labels.auto.tfvars.json",
  )), true);
  assert.equal(await exists(path.join(
    workspace,
    "imports",
    "tenant",
    "zia_rule_labels_imports.tf",
  )), true);
});

test("switching tfvars formats removes the stale opposite artifact", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-stale-");
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "zia_rule_labels.json"), [{ id: 7, name: "Example" }]);
  const root = await committedRoot();
  const common = {
    inputDirectory: input,
    root,
    selectors: ["zia_rule_labels"],
    tenant: "tenant",
  } as const;
  const jsonPath = path.join(
    workspace,
    "config",
    "tenant",
    "zia_rule_labels.auto.tfvars.json",
  );
  const hclPath = jsonPath.slice(0, -".json".length);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace),
  })).failed, []);
  assert.equal(await exists(jsonPath), true);
  assert.equal(await exists(hclPath), false);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace, { hcl: true }),
  })).failed, []);
  assert.equal(await exists(jsonPath), false);
  assert.equal(await exists(hclPath), true);

  assert.deepEqual((await runTransformBatch({
    ...common,
    deployment: deployment(workspace),
  })).failed, []);
  assert.equal(await exists(jsonPath), true);
  assert.equal(await exists(hclPath), false);
});

test("artifact paths retain the flat tenant/resource layout", () => {
  assert.deepEqual(transformArtifactPaths({
    deployment: deployment("overlay"),
    resourceType: "zia_rule_labels",
    tenant: "tenant",
  }), {
    config: path.join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars.json"),
    generatedBindings: path.join(
      "overlay",
      "config",
      "tenant",
      "zia_rule_labels.generated.expressions.json",
    ),
    imports: path.join("overlay", "imports", "tenant", "zia_rule_labels_imports.tf"),
    lookup: path.join("overlay", "config", "tenant", "zia_rule_labels.lookup.json"),
    moves: path.join("overlay", "imports", "tenant", "zia_rule_labels_moves.tf"),
    staleConfig: path.join("overlay", "config", "tenant", "zia_rule_labels.auto.tfvars"),
  });
});

test("batch artifacts consume the same later-pack metadata merge as ordering", async (context) => {
  const workspace = await temporaryDirectory(context, "infrawright-runtime-merge-");
  const packs = path.join(workspace, "packs");
  await writeJson(path.join(packs, "alpha", "pack.json"), {
    lookup_sources: { sample_old: { name_field: "name" } },
    provider_prefixes: { sample_: "sample" },
    references: {
      sample_referrer: {
        target: { name_field: "name", referent: "sample_old" },
      },
    },
  });
  await writeJson(path.join(packs, "alpha", "registry.json"), {
    sample_old: { generate: true, product: "sample" },
    sample_referrer: { generate: true, product: "sample" },
  });
  await writeJson(path.join(packs, "alpha", "schemas", "provider", "sample.json"), {
    resource_schemas: {
      sample_old: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
        } },
      },
      sample_referrer: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
          target: { optional: true, type: "string" },
        } },
      },
    },
  });
  await writeJson(path.join(packs, "beta", "pack.json"), {
    lookup_sources: {
      other_referent: { name_field: "name" },
      sample_referrer: { name_field: "name" },
    },
    provider_prefixes: { other_: "other" },
    references: {
      sample_referrer: {
        target: { name_field: "name", referent: "other_referent" },
      },
    },
  });
  await writeJson(path.join(packs, "beta", "registry.json"), {
    other_referent: { generate: true, product: "other" },
  });
  await writeJson(path.join(packs, "beta", "schemas", "provider", "other.json"), {
    resource_schemas: {
      other_referent: {
        block: { attributes: {
          id: { computed: true, type: "string" },
          name: { required: true, type: "string" },
        } },
      },
    },
  });
  const input = path.join(workspace, "input");
  await writeJson(path.join(input, "other_referent.json"), [{ id: "o1", name: "Other" }]);
  await writeJson(path.join(input, "sample_referrer.json"), [{
    id: "s1",
    name: "Sample",
    target: "o1",
  }]);
  const result = await runTransformBatch({
    deployment: deployment(path.join(workspace, "out"), { hcl: true }),
    inputDirectory: input,
    root: await loadPackRoot({ packsRoot: packs }),
    selectors: ["sample_referrer", "other_referent"],
    tenant: "tenant",
  });
  assert.deepEqual(result.processed, ["other_referent", "sample_referrer"]);
  assert.match(
    await text(path.join(
      workspace,
      "out",
      "config",
      "tenant",
      "sample_referrer.auto.tfvars",
    )),
    /target = "o1" # Other/,
  );
  assert.equal(await exists(path.join(
    workspace,
    "out",
    "config",
    "tenant",
    "sample_referrer.lookup.json",
  )), true);
});
