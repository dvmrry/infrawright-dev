import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import zscalerCatalog from "../catalogs/zscaler-root-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import type { RootCatalog } from "../node-src/domain/types.js";
import { requireSupportedAssessmentCatalog } from "../node-src/domain/zscaler-assessment.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

function guidanceRuleCount(
  data: Readonly<Record<string, unknown>>,
  group: string,
  field: string,
): number {
  const rawGroup = data[group];
  if (rawGroup === undefined) return 0;
  assert.ok(rawGroup !== null && typeof rawGroup === "object" && !Array.isArray(rawGroup));
  const rules = (rawGroup as Readonly<Record<string, unknown>>)[field];
  if (rules === undefined) return 0;
  assert.ok(Array.isArray(rules), `${group}.${field} must be an array`);
  return rules.length;
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
  const counts = Object.fromEntries(providers.map((provider) => {
    const manifests = root.packs.manifests.filter((manifest) => {
      return Object.values(manifest.providerPrefixes).includes(provider);
    });
    assert.ok(manifests.length > 0, `missing manifest for ${provider}`);
    return [provider, manifests.reduce((total, manifest) => {
      return total
        + guidanceRuleCount(manifest.data, "provider_config", "requirements")
        + guidanceRuleCount(manifest.data, "absent_defaults", "rules")
        + guidanceRuleCount(manifest.data, "dynamic_schema", "rules");
    }, 0)];
  }));
  assert.deepEqual(counts, Object.fromEntries(providers.map((provider) => [provider, 0])));
});
