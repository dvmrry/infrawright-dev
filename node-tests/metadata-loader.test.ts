import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LosslessNumber } from "lossless-json";

import {
  activePackSelection,
  loadPackMetadata,
  PACK_SET_KIND,
  providerForResource,
  validateActivePackSet,
  validatePackAuthoring,
  validatePackSetDocument,
} from "../node-src/metadata/packs.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";
import {
  loadProviderSchema,
  loadResourceSchema,
  validateOverride,
  validateRegistry,
} from "../node-src/metadata/resources.js";

const ROOT = process.cwd();

async function json(pathname: string): Promise<unknown> {
  return JSON.parse(await readFile(pathname, "utf8")) as unknown;
}

async function writeJson(pathname: string, value: unknown): Promise<void> {
  await mkdir(path.dirname(pathname), { recursive: true });
  await writeFile(pathname, JSON.stringify(value));
}

test("committed pack metadata exposes the complete generic resource surface", async () => {
  const loaded = await loadPackRoot({
    packsRoot: path.join(ROOT, "packs"),
    profilePath: path.join(ROOT, "packsets", "full.json"),
    catalogPath: path.join(ROOT, "packsets", "full.json"),
  });
  const metadata = loaded.packs;
  assert.deepEqual(
    metadata.manifests.map((manifest) => manifest.name),
    ["aws", "cloudflare", "google", "netbox", "zcc", "zia", "zpa", "ztc"],
  );
  assert.deepEqual({ ...metadata.providerPrefixes }, {
    aws_: "aws",
    cloudflare_: "cloudflare",
    google_: "google",
    netbox_: "netbox",
    zcc_: "zcc",
    zia_: "zia",
    zpa_: "zpa",
    ztc_: "ztc",
  });
  const registry = loaded.registry;
  const overrides = loaded.overrides;
  assert.equal(Object.keys(registry.entries).length, 151);
  assert.equal(Object.keys(overrides.entries).length, 59);
  assert.equal(registry.entries.zia_url_categories?.product, "zia");
  assert.equal(overrides.entries.zia_url_categories?.key_field, "configured_name");
  assert.deepEqual(loaded.resources.get("zia_url_categories"), {
    type: "zia_url_categories",
    product: "zia",
    provider: "zia",
    pack: "zia",
    registry: registry.entries.zia_url_categories,
    override: overrides.entries.zia_url_categories,
  });
  assert.equal(
    typeof (await loaded.loadResourceSchema("zia_url_categories")).block,
    "object",
  );
});

test("provider schemas resolve through pack ownership and fail on misspellings", async () => {
  const metadata = await loadPackMetadata(path.join(ROOT, "packs"));
  const counts: Record<string, number> = {};
  for (const provider of ["zcc", "zia", "zpa", "ztc"]) {
    counts[provider] = Object.keys(
      (await loadProviderSchema(metadata, provider)).resourceSchemas,
    ).length;
  }
  assert.deepEqual(counts, { zcc: 7, zia: 74, zpa: 55, ztc: 16 });
  const category = await loadResourceSchema(metadata, "zia_url_categories");
  assert.equal(typeof category.block, "object");
  await assert.rejects(
    () => loadResourceSchema(metadata, "zia_url_categoriess"),
    /not in zia schema/,
  );
  assert.equal(providerForResource(metadata, "zia_url_categories"), "zia");
});

test("pack-set validation counts manifestless runtime directories fail-closed", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-packs-"));
  try {
    await mkdir(path.join(directory, "ghost"));
    const profile = path.join(directory, "profile.json");
    await writeJson(profile, {
      kind: PACK_SET_KIND,
      version: 1,
      packs: [],
      shared: [],
    });
    await assert.rejects(
      () => validateActivePackSet({ profilePath: profile, root: directory }),
      /undeclared packs: ghost/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("pack ownership and required shared components remain hard failures", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-packs-"));
  try {
    await writeJson(path.join(directory, "one", "pack.json"), {
      provider_prefixes: { one_: "same" },
      requires_shared: ["common"],
    });
    await writeJson(path.join(directory, "two", "pack.json"), {
      provider_prefixes: { two_: "same" },
    });
    await assert.rejects(
      () => validatePackAuthoring({ root: directory }),
      /provider "same" is declared by multiple packs: one, two/,
    );
    await rm(path.join(directory, "two"), { recursive: true, force: true });
    await assert.rejects(
      () => validatePackAuthoring({ root: directory }),
      /pack one requires missing shared component common/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("strict profile, registry, and override vocabularies reject silent typos", () => {
  assert.throws(
    () => validatePackSetDocument({
      kind: PACK_SET_KIND,
      version: 1,
      packs: ["two", "one"],
      shared: [],
    }, "profile.json", PACK_SET_KIND),
    /packs must be sorted/,
  );
  assert.throws(
    () => validateRegistry({
      sample_resource: {
        product: "sample",
        fetch: { path: "/items", pagination: "singel" },
      },
    }, "registry.json"),
    /unsupported value "singel"/,
  );
  assert.throws(
    () => validateRegistry({
      sample_resource: { product: "sample", slug_group: "false" },
    }, "registry.json"),
    /slug_group must be a boolean/,
  );
  assert.throws(
    () => validateOverride({ rename: { one: "two" } }, "override.json"),
    /unknown override key rename/,
  );
});

test("registry fetch paths reject inputs that WHATWG URLs would silently normalize", () => {
  const registry = (pathValue: string, expansion?: string) => ({
    sample_resource: {
      product: "sample",
      fetch: {
        pagination: "single",
        path: pathValue,
        ...(expansion === undefined ? {} : { expand: { item: [expansion] } }),
      },
    },
  });
  for (const value of [
    "items\\admin",
    "items?scope=admin",
    "items#admin",
    "items/../admin",
    "items/.%2E/admin",
    "items/%2e./admin",
  ]) {
    assert.throws(
      () => validateRegistry(registry(value), "registry.json"),
      /fetch\.path must not contain/,
      value,
    );
  }
  for (const value of ["items admin", "items/é", "items/%zz"]) {
    assert.throws(
      () => validateRegistry(registry(value), "registry.json"),
      /RFC 3986 path characters/,
      value,
    );
  }
  for (const value of ["items/{literal}", "items/{item}/{other}"]) {
    assert.throws(
      () => validateRegistry(
        registry(value, value.includes("{item}") ? "safe" : undefined),
        "registry.json",
      ),
      /undeclared expansion braces/,
      value,
    );
  }
  for (const value of [
    ".",
    "..",
  ]) {
    assert.throws(
      () => validateRegistry(registry("items/{item}", value), "registry.json"),
      /fetch\.expand\.item\[0\] must not be/,
      value,
    );
  }
  assert.doesNotThrow(() => {
    validateRegistry(registry("items/{item}", "slash/value"), "registry.json");
    validateRegistry(registry("items/{item}/{item}", "safe"), "registry.json");
    validateRegistry(registry("items/{item}", "nested/../value?#\\"), "registry.json");
    validateRegistry(registry("items/{item}", "%2e"), "registry.json");
  });
});

test("metadata loading preserves fetch query number tokens and wide integers", async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-numbers-"));
  try {
    await writeJson(path.join(directory, "sample", "pack.json"), {
      provider_prefixes: { sample_: "sample" },
    });
    await mkdir(path.join(directory, "sample", "overrides"), { recursive: true });
    await writeFile(
      path.join(directory, "sample", "registry.json"),
      '{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":{"safe":9007199254740991,"wide":9007199254740993,"decimal":1.0,"exponent":1e0,"negative_zero":-0.0}}}}',
    );
    await writeFile(
      path.join(directory, "sample", "overrides", "sample_resource.json"),
      '{"defaults":{"wide":9007199254740993}}',
    );
    const profile = path.join(directory, "profile.json");
    await writeFile(
      profile,
      '{"kind":"infrawright.pack-set","version":1,"packs":["sample"],"shared":[]}',
    );
    const loaded = await loadPackRoot({ packsRoot: directory, profilePath: profile });
    const fetch = loaded.resources.get("sample_resource")?.registry.fetch;
    assert.equal(typeof fetch, "object");
    const query = (fetch as { readonly query: Record<string, unknown> }).query;
    assert.equal((query.safe as LosslessNumber).toString(), "9007199254740991");
    assert.equal((query.wide as LosslessNumber).toString(), "9007199254740993");
    assert.equal((query.decimal as LosslessNumber).toString(), "1.0");
    assert.equal((query.exponent as LosslessNumber).toString(), "1e0");
    assert.equal((query.negative_zero as LosslessNumber).toString(), "-0.0");
    const defaults = loaded.resources.get("sample_resource")?.override?.defaults as
      | Record<string, unknown>
      | undefined;
    assert.equal((defaults?.wide as LosslessNumber).toString(), "9007199254740993");

    await writeFile(
      profile,
      '{"kind":"infrawright.pack-set","version":1.0,"packs":["sample"],"shared":[]}',
    );
    await assert.rejects(
      () => validateActivePackSet({ profilePath: profile, root: directory }),
      /version must be 1/,
    );

    await writeFile(
      path.join(directory, "sample", "registry.json"),
      '{"sample_resource":{"product":"sample","fetch":{"pagination":"single","path":"/items","query":9007199254740993}}}',
    );
    await assert.rejects(
      () => loadPackRoot({ packsRoot: directory }),
      /fetch\.query must be an object/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("all committed pack profiles load from physically reduced roots", async (context) => {
  const packsets = [
    "empty",
    "aws",
    "cloudflare",
    "google",
    "netbox",
    "zcc",
    "zia",
    "zpa",
    "ztc",
    "zscaler",
    "full",
  ];
  for (const profileName of packsets) {
    await context.test(profileName, async () => {
      const directory = await mkdtemp(path.join(os.tmpdir(), "infrawright-profile-"));
      try {
        const profilePath = path.join(ROOT, "packsets", `${profileName}.json`);
        const profile = await json(profilePath) as {
          readonly packs: readonly string[];
          readonly shared: readonly string[];
        };
        for (const name of profile.packs) {
          await cp(
            path.join(ROOT, "packs", name),
            path.join(directory, name),
            { recursive: true },
          );
        }
        for (const name of profile.shared) {
          await cp(
            path.join(ROOT, "packs", "_shared", name),
            path.join(directory, "_shared", name),
            { recursive: true },
          );
        }
        const loaded = await loadPackRoot({
          packsRoot: directory,
          profilePath,
          catalogPath: path.join(ROOT, "packsets", "full.json"),
        });
        assert.deepEqual(loaded.active, {
          packs: profile.packs,
          shared: profile.shared,
        });
        assert.deepEqual(await activePackSelection(directory), loaded.active);
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    });
  }
});
