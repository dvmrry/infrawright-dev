import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { validateZccPullArtifactSet } from "../node-src/contracts/validators.js";
import { renderPythonLosslessArtifactJson } from "../node-src/json/python-lossless-artifact.js";
import { sortedStrings } from "../node-src/json/python-compatible.js";

const CATALOG_SHA256 =
  "3900a4d12cd49af7bc8d80248b9c184fa8047ca1987654965a81de87c600937a";
const CATALOG_SOURCES_SHA256 =
  "90452e9199dcc4dbf578e9f3af21ae2fb35517eb5d3af0f1a193f2e5ed92ed11";

function descriptor(
  path: string,
  mediaType: "application/json" | "text/x-hcl",
  content: string,
): Record<string, unknown> {
  const bytes = Buffer.from(content, "utf8");
  return {
    path,
    media_type: mediaType,
    encoding: "utf-8",
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size_bytes: bytes.length,
    content,
  };
}

function replaceContent(descriptorValue: unknown, content: string): void {
  const artifact = descriptorValue as Record<string, unknown>;
  Object.assign(
    artifact,
    descriptor(
      String(artifact.path),
      artifact.media_type as "application/json" | "text/x-hcl",
      content,
    ),
  );
}

function hclStringLiteral(value: string): string {
  return `"${value
    .replaceAll("\\", "\\\\")
    .replaceAll('"', '\\"')
    .replaceAll("\n", "\\n")
    .replaceAll("\r", "\\r")
    .replaceAll("\t", "\\t")
    .replaceAll("${", () => "$${")
    .replaceAll("%{", "%%{")}"`;
}

function importContent(
  resource: string,
  blocks: readonly { readonly key: string; readonly id: string }[],
): string {
  return blocks.map((block) => {
    return `import {\n`
      + `  to = module.${resource}.${resource}.this[${hclStringLiteral(block.key)}]\n`
      + `  id = ${hclStringLiteral(block.id)}\n`
      + `}\n`;
  }).join("\n");
}

function artifactSet(options: {
  resource?: string;
  prefix?: string;
  rootLabel?: string;
  members?: string[];
  items?: Record<string, Record<string, unknown>>;
  imports?: readonly { readonly key: string; readonly id: string }[];
  unexpectedDrops?: string[];
} = {}): Record<string, unknown> {
  const resource = options.resource ?? "zcc_failopen_policy";
  const prefix = options.prefix ?? "overlay";
  const rootLabel = options.rootLabel ?? resource;
  const members = options.members ?? [resource];
  const variableName = rootLabel === resource
    ? "items"
    : `${resource}_items`;
  const withPrefix = (suffix: string) => prefix.length === 0
    ? suffix
    : prefix.endsWith("/")
      ? `${prefix}${suffix}`
      : `${prefix}/${suffix}`;
  const items = options.items ?? {
    first: { enabled: true, id: "1" },
  };
  const imports = options.imports ?? sortedStrings(Object.keys(items)).map(
    (key, index) => ({ key, id: String(index + 1) }),
  );
  const tfvars = renderPythonLosslessArtifactJson({ [variableName]: items });
  const lookup = resource === "zcc_trusted_network"
    ? descriptor(
        withPrefix(`config/tenant/${resource}.lookup.json`),
        "application/json",
        Object.keys(items).length === 0
          ? "{}\n"
          : renderPythonLosslessArtifactJson({
              by_id: Object.fromEntries(
                imports.map((block) => [block.id, `name-${block.key}`]),
              ),
              key_by_id: Object.fromEntries(
                imports.map((block) => [block.id, block.key]),
              ),
            }),
      )
    : null;
  const unexpectedDrops = options.unexpectedDrops ?? [];
  return {
    kind: "infrawright.zcc_pull_artifact_set",
    schema_version: 1,
    mode: "bootstrap",
    product: "zcc",
    resource_type: resource,
    tenant: "tenant",
    source: {
      path: `pulls/tenant/${resource}.json`,
      sha256: "a".repeat(64),
      size_bytes: 2,
    },
    catalog: {
      kind: "infrawright.transform_catalog",
      schema_version: 1,
      sha256: CATALOG_SHA256,
      sources_sha256: CATALOG_SOURCES_SHA256,
    },
    root: { label: rootLabel, members, variable_name: variableName },
    status: unexpectedDrops.length === 0 ? "ready" : "review_required",
    unexpected_drops: unexpectedDrops,
    artifacts: {
      tfvars: descriptor(
        withPrefix(`config/tenant/${resource}.auto.tfvars.json`),
        "application/json",
        tfvars,
      ),
      imports: descriptor(
        withPrefix(`imports/tenant/${resource}_imports.tf`),
        "text/x-hcl",
        importContent(resource, imports),
      ),
      lookup,
    },
  };
}

function assertValid(value: unknown): void {
  assert.equal(
    validateZccPullArtifactSet(value),
    true,
    JSON.stringify(validateZccPullArtifactSet.errors),
  );
}

function errorsFor(value: unknown): readonly string[] {
  assert.equal(validateZccPullArtifactSet(value), false);
  return (validateZccPullArtifactSet.errors ?? []).map((candidate) => {
    const params = candidate.params as { readonly rule?: unknown } | undefined;
    return String(params?.rule ?? candidate.keyword);
  });
}

test("semantic validator accepts standalone, grouped, empty, and escaped sets", () => {
  assertValid(artifactSet());
  assertValid(artifactSet({
    resource: "zcc_trusted_network",
    prefix: "",
    rootLabel: "zcc_network_root",
    members: ["zcc_forwarding_profile", "zcc_trusted_network"],
  }));
  for (const prefix of ["/", "//", "///"]) {
    assertValid(artifactSet({
      resource: "zcc_trusted_network",
      prefix,
    }));
  }
  assertValid(artifactSet({
    resource: "zcc_trusted_network",
    items: {},
    imports: [],
  }));

  const key = 'quote"\\line\nrow\rcol\t${name}%{ directive }';
  assertValid(artifactSet({
    items: { [key]: { id: "same" } },
    imports: [{ key, id: key }],
  }));
});

test("semantic validator pins provenance, source, root, and sorted drops", () => {
  const badCatalog = structuredClone(artifactSet());
  (badCatalog.catalog as Record<string, unknown>).sha256 = "0".repeat(64);
  assert.ok(errorsFor(badCatalog).includes("catalog_provenance"));

  const badSources = structuredClone(artifactSet());
  (badSources.catalog as Record<string, unknown>).sources_sha256 = "0".repeat(64);
  assert.ok(errorsFor(badSources).includes("catalog_provenance"));

  const badSource = structuredClone(artifactSet());
  (badSource.source as Record<string, unknown>).path =
    "pulls/other/zcc_failopen_policy.json";
  assert.ok(errorsFor(badSource).includes("source_path"));

  const unsortedMembers = artifactSet({
    rootLabel: "zcc_root",
    members: ["zcc_web_privacy", "zcc_failopen_policy"],
  });
  assert.ok(errorsFor(unsortedMembers).includes("root_members"));

  const absent = artifactSet({ members: ["zcc_web_privacy"] });
  assert.ok(errorsFor(absent).includes("root_members"));

  const generatedLabelGroup = artifactSet({
    rootLabel: "zcc_failopen_policy",
    members: ["zcc_failopen_policy", "zcc_trusted_network"],
  });
  assert.ok(errorsFor(generatedLabelGroup).includes("root_label"));

  const crossProviderGeneratedLabel = artifactSet({
    rootLabel: "zia_admin_users",
    members: ["zcc_failopen_policy"],
  });
  assert.ok(errorsFor(crossProviderGeneratedLabel).includes("root_label"));

  const unknownMember = artifactSet({
    rootLabel: "zcc_group",
    members: ["zcc_failopen_policy", "zcc_totally_fake"],
  });
  assert.ok(errorsFor(unknownMember).includes("root_members"));

  const badVariable = structuredClone(artifactSet());
  (badVariable.root as Record<string, unknown>).variable_name = "other";
  assert.ok(errorsFor(badVariable).includes("variable_name"));

  const unsortedDrops = artifactSet({ unexpectedDrops: ["z", "a"] });
  assert.ok(errorsFor(unsortedDrops).includes("unexpected_drops"));
});

test("semantic validator binds descriptor bytes and deployment layout", () => {
  const badBytes = structuredClone(artifactSet());
  const tfvars = (badBytes.artifacts as Record<string, unknown>)
    .tfvars as Record<string, unknown>;
  tfvars.content = `${String(tfvars.content)} `;
  const byteErrors = errorsFor(badBytes);
  assert.ok(byteErrors.includes("artifact_bytes"));
  assert.ok(byteErrors.includes("canonical_json"));

  const badTfvarsPath = structuredClone(artifactSet());
  ((badTfvarsPath.artifacts as Record<string, unknown>)
    .tfvars as Record<string, unknown>).path =
      "overlay/config/other/zcc_failopen_policy.auto.tfvars.json";
  assert.ok(errorsFor(badTfvarsPath).includes("artifact_layout"));

  const splitPrefix = structuredClone(artifactSet());
  ((splitPrefix.artifacts as Record<string, unknown>)
    .imports as Record<string, unknown>).path =
      "other/imports/tenant/zcc_failopen_policy_imports.tf";
  assert.ok(errorsFor(splitPrefix).includes("artifact_layout"));

  const splitLexicalRoot = structuredClone(artifactSet({
    resource: "zcc_trusted_network",
    prefix: "//",
  }));
  ((splitLexicalRoot.artifacts as Record<string, unknown>)
    .imports as Record<string, unknown>).path =
      "/imports/tenant/zcc_trusted_network_imports.tf";
  assert.ok(errorsFor(splitLexicalRoot).includes("artifact_layout"));

  const splitLookupRoot = structuredClone(artifactSet({
    resource: "zcc_trusted_network",
    prefix: "//",
  }));
  ((splitLookupRoot.artifacts as Record<string, unknown>)
    .lookup as Record<string, unknown>).path =
      "/config/tenant/zcc_trusted_network.lookup.json";
  assert.ok(errorsFor(splitLookupRoot).includes("artifact_layout"));
});

test("tfvars must be one object variable whose item values are objects", () => {
  const arrayVariable = structuredClone(artifactSet());
  const arrayTfvars = (arrayVariable.artifacts as Record<string, unknown>).tfvars;
  replaceContent(arrayTfvars, renderPythonLosslessArtifactJson({ items: [] }));
  assert.ok(errorsFor(arrayVariable).includes("tfvars_envelope"));

  const arrayItem = structuredClone(artifactSet());
  const itemTfvars = (arrayItem.artifacts as Record<string, unknown>).tfvars;
  replaceContent(
    itemTfvars,
    renderPythonLosslessArtifactJson({ items: { first: [] } }),
  );
  assert.ok(errorsFor(arrayItem).includes("tfvars_items"));

  const secondVariable = structuredClone(artifactSet());
  const secondTfvars = (secondVariable.artifacts as Record<string, unknown>).tfvars;
  replaceContent(
    secondTfvars,
    renderPythonLosslessArtifactJson({ items: {}, other: {} }),
  );
  assert.ok(errorsFor(secondVariable).includes("tfvars_envelope"));
});

test("imports use the closed grammar and join tfvars keys and unique IDs", () => {
  const empty = structuredClone(artifactSet());
  replaceContent((empty.artifacts as Record<string, unknown>).imports, "");
  assert.ok(errorsFor(empty).includes("imports_join"));

  const redirected = structuredClone(artifactSet());
  replaceContent(
    (redirected.artifacts as Record<string, unknown>).imports,
    "import {\n"
      + "  to = module.zcc_web_privacy.zcc_web_privacy.this[\"first\"]\n"
      + "  id = \"1\"\n"
      + "}\n",
  );
  assert.ok(errorsFor(redirected).includes("imports_grammar"));

  const arbitraryHcl = structuredClone(artifactSet());
  replaceContent(
    (arbitraryHcl.artifacts as Record<string, unknown>).imports,
    "locals { stolen = file(\"/etc/passwd\") }\n",
  );
  assert.ok(errorsFor(arbitraryHcl).includes("imports_grammar"));

  const duplicateId = artifactSet({
    items: { first: {}, second: {} },
    imports: [{ key: "first", id: "same" }, { key: "second", id: "same" }],
  });
  assert.ok(errorsFor(duplicateId).includes("import_ids"));

  const duplicateKey = artifactSet({
    imports: [{ key: "first", id: "1" }, { key: "first", id: "2" }],
  });
  assert.ok(errorsFor(duplicateKey).includes("imports_join"));

  const blankId = artifactSet({
    imports: [{ key: "first", id: ` \t${String.fromCharCode(0x1c)}` }],
  });
  assert.ok(errorsFor(blankId).includes("import_ids"));

  const wrongKey = artifactSet({ imports: [{ key: "other", id: "1" }] });
  assert.ok(errorsFor(wrongKey).includes("imports_join"));
});

test("trusted-network lookup exactly joins tfvars keys and import identities", () => {
  const missing = artifactSet({ resource: "zcc_trusted_network" });
  (missing.artifacts as Record<string, unknown>).lookup = null;
  assert.ok(errorsFor(missing).includes("lookup_artifact"));

  const empty = artifactSet({ resource: "zcc_trusted_network" });
  replaceContent((empty.artifacts as Record<string, unknown>).lookup, "{}\n");
  assert.ok(errorsFor(empty).includes("lookup_join"));

  const wrongId = artifactSet({ resource: "zcc_trusted_network" });
  replaceContent(
    (wrongId.artifacts as Record<string, unknown>).lookup,
    renderPythonLosslessArtifactJson({
      by_id: { other: "name-first" },
      key_by_id: { other: "first" },
    }),
  );
  assert.ok(errorsFor(wrongId).includes("lookup_join"));

  const wrongKey = artifactSet({ resource: "zcc_trusted_network" });
  replaceContent(
    (wrongKey.artifacts as Record<string, unknown>).lookup,
    renderPythonLosslessArtifactJson({
      by_id: { "1": "name-first" },
      key_by_id: { "1": "other" },
    }),
  );
  assert.ok(errorsFor(wrongKey).includes("lookup_join"));

  const divergentMaps = artifactSet({ resource: "zcc_trusted_network" });
  replaceContent(
    (divergentMaps.artifacts as Record<string, unknown>).lookup,
    renderPythonLosslessArtifactJson({
      by_id: { "1": "name-first" },
      key_by_id: { "2": "first" },
    }),
  );
  assert.ok(errorsFor(divergentMaps).includes("lookup_join"));

  const nonString = artifactSet({ resource: "zcc_trusted_network" });
  replaceContent(
    (nonString.artifacts as Record<string, unknown>).lookup,
    renderPythonLosslessArtifactJson({
      by_id: { "1": 1 },
      key_by_id: { "1": "first" },
    }),
  );
  assert.ok(errorsFor(nonString).includes("lookup_shape"));

  const extra = artifactSet();
  (extra.artifacts as Record<string, unknown>).lookup = descriptor(
    "overlay/config/tenant/zcc_failopen_policy.lookup.json",
    "application/json",
    "{}\n",
  );
  assert.ok(errorsFor(extra).includes("lookup_artifact"));
});
