import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import zscalerCatalog from "../catalogs/zscaler-root-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import type { RootCatalog } from "../node-src/domain/types.js";
import { requireSupportedAssessmentCatalog } from "../node-src/domain/zscaler-assessment.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

interface RawGuidanceManifest {
  readonly data: Readonly<Record<string, unknown>>;
  readonly providerPrefixes: Readonly<Record<string, string>>;
}

const GUIDANCE_LANES = [
  ["provider_config", "requirements"],
  ["absent_defaults", "rules"],
  ["dynamic_schema", "rules"],
] as const;

function guidanceRules(
  data: Readonly<Record<string, unknown>>,
  group: string,
  field: string,
): readonly Readonly<Record<string, unknown>>[] {
  const rawGroup = data[group];
  if (rawGroup === undefined) return [];
  assert.ok(rawGroup !== null && typeof rawGroup === "object" && !Array.isArray(rawGroup));
  const rules = (rawGroup as Readonly<Record<string, unknown>>)[field];
  if (rules === undefined) return [];
  assert.ok(Array.isArray(rules), `${group}.${field} must be an array`);
  assert.ok(rules.every((rule) => {
    return rule !== null && typeof rule === "object" && !Array.isArray(rule);
  }), `${group}.${field} entries must be objects`);
  return rules as readonly Readonly<Record<string, unknown>>[];
}

function effectiveRuleProvider(
  manifest: RawGuidanceManifest,
  rule: Readonly<Record<string, unknown>>,
): string | undefined {
  const explicitProvider = rule.provider;
  if (explicitProvider !== undefined) {
    assert.equal(typeof explicitProvider, "string");
    return explicitProvider as string;
  }
  const providers = [...new Set(Object.values(manifest.providerPrefixes))].sort();
  return providers.length === 1 ? providers[0] : undefined;
}

function rawGuidanceCounts(
  manifests: readonly RawGuidanceManifest[],
  providers: readonly string[],
): Record<string, [number, number, number]> {
  const counts = Object.fromEntries(providers.map((provider) => {
    return [provider, [0, 0, 0]];
  })) as Record<string, [number, number, number]>;
  for (const manifest of manifests) {
    for (const [index, [group, field]] of GUIDANCE_LANES.entries()) {
      for (const rule of guidanceRules(manifest.data, group, field)) {
        const provider = effectiveRuleProvider(manifest, rule);
        const providerCounts = provider === undefined ? undefined : counts[provider];
        if (providerCounts !== undefined) {
          if (index === 0) providerCounts[0] += 1;
          if (index === 1) providerCounts[1] += 1;
          if (index === 2) providerCounts[2] += 1;
        }
      }
    }
  }
  return counts;
}

test("assessment accepts only the exact embedded Zscaler catalog", () => {
  requireSupportedAssessmentCatalog(zscalerCatalog as RootCatalog);
  for (const changed of [
    { ...zscalerCatalog, sources_sha256: "0".repeat(64) },
    { ...zscalerCatalog, declared_providers: ["zcc", "zia", "zpa"] },
    { ...zscalerCatalog, resources: zscalerCatalog.resources.slice(1) },
  ]) {
    assert.throws(
      () => requireSupportedAssessmentCatalog(changed as RootCatalog),
      (error: unknown) => error instanceof ProcessFailure
        && error.code === "UNSUPPORTED_ASSESSMENT_CATALOG",
    );
  }
});

test("supported Zscaler packs have no assessment-guidance rules", async () => {
  const providers = [...zscalerCatalog.declared_providers];
  const repository = process.cwd();
  const root = await loadPackRoot({
    packsRoot: path.join(repository, "packs"),
    profilePath: path.join(repository, "packsets", "full.json"),
    catalogPath: path.join(repository, "packsets", "full.json"),
  });
  assert.deepEqual(
    rawGuidanceCounts(root.packs.manifests, providers),
    Object.fromEntries(providers.map((provider) => [provider, [0, 0, 0]])),
  );
});

test("guidance authority follows explicit and unambiguous per-rule providers", () => {
  const manifests: RawGuidanceManifest[] = [
    {
      providerPrefixes: { aws_: "aws" },
      data: {
        provider_config: {
          requirements: [{ provider: "zia" }],
        },
      },
    },
    {
      providerPrefixes: { zpa_: "zpa" },
      data: {
        absent_defaults: {
          rules: [{}],
        },
      },
    },
    {
      providerPrefixes: { zcc_: "zcc", ztc_: "ztc" },
      data: {
        dynamic_schema: {
          rules: [{}],
        },
      },
    },
  ];
  assert.deepEqual(rawGuidanceCounts(manifests, ["zia", "zpa", "zcc", "ztc"]), {
    zia: [1, 0, 0],
    zpa: [0, 1, 0],
    zcc: [0, 0, 0],
    ztc: [0, 0, 0],
  });
});
