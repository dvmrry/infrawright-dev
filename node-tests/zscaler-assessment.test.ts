import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import zscalerCatalog from "../catalogs/zscaler-root-catalog.v1.json" with { type: "json" };
import { ProcessFailure } from "../node-src/domain/errors.js";
import type { RootCatalog } from "../node-src/domain/types.js";
import { requireSupportedAssessmentCatalog } from "../node-src/domain/zscaler-assessment.js";

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

test("supported Zscaler packs have no guidance lanes hidden from Node", () => {
  const providers = [...zscalerCatalog.declared_providers];
  const result = spawnSync("python3", ["-c", [
    "import json",
    "import sys",
    "from engine import packs",
    "providers = json.load(sys.stdin)",
    "print(json.dumps({p: [",
    "  len(packs.provider_config_requirements(p)),",
    "  len(packs.absent_default_rules(p)),",
    "  len(packs.dynamic_schema_rules(p)),",
    "] for p in providers}, sort_keys=True))",
  ].join("\n")], {
    encoding: "utf8",
    input: JSON.stringify(providers),
  });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(
    JSON.parse(result.stdout),
    Object.fromEntries(providers.map((provider) => [provider, [0, 0, 0]])),
  );
});
