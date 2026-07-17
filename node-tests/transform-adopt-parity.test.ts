import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  applyReportedDifferences,
  buildParityReport,
  compareTransformAdoptParityFixture,
  jsonDifferences,
  loadParityFixture,
  renderParityReport,
  validateParityFixture,
  type TransformAdoptParityFixture,
} from "../node-src/domain/transform-adopt-parity.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";

const ROOT = process.cwd();
const FIXTURE_DIR = path.join(ROOT, "tests", "fixtures", "parity");
const PACK_ROOT = loadPackRoot({ packsRoot: path.join(ROOT, "packs") });

async function context() {
  return { repositoryRoot: ROOT, root: await PACK_ROOT };
}

async function fixture(name: string): Promise<TransformAdoptParityFixture> {
  return loadParityFixture(path.join(FIXTURE_DIR, `${name}.json`), await context());
}

function object(value: unknown): Record<string, unknown> {
  assert.ok(value !== null && typeof value === "object" && !Array.isArray(value));
  return value as Record<string, unknown>;
}

async function fixturePaths(): Promise<readonly string[]> {
  return (await readdir(FIXTURE_DIR))
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => path.join(FIXTURE_DIR, name));
}

test("committed fixtures reproduce the complete frozen CPython authority", async () => {
  const authorityPath = path.join(
    ROOT,
    "node-tests",
    "fixtures",
    "python-transform-adopt-parity-v1.json",
  );
  const bytes = await readFile(authorityPath, "utf8");
  assert.equal(Buffer.byteLength(bytes), 13_486);
  assert.equal(
    createHash("sha256").update(bytes).digest("hex"),
    "87f4ef2c299c413fd87193a6f2e312fcbbcbef0f501af3ebeab32f54942127a8",
  );
  const authority = object(JSON.parse(bytes));
  assert.equal(authority.baseline, "9904cbaadd4c79b1b4b385abfe6edca05c57cfc8");
  assert.equal(authority.kind, "infrawright.python-transform-adopt-parity-authority");
  const sourceBlobs = object(authority.source_blobs);
  assert.equal(sourceBlobs.python_diagnostic, "f97e5d3271b763eeb81dfdc3ab607196603958b8");
  assert.equal(sourceBlobs.python_test, "3de109880e7c08d7fc5287ec6e45a2dd7df4a2f5");

  const parityContext = await context();
  const fixtures = await Promise.all((await fixturePaths()).map((source) => {
    return loadParityFixture(source, parityContext);
  }));
  const report = await buildParityReport(fixtures, parityContext);
  assert.equal(renderParityReport(report), authority.report);
  assert.equal(report.result, "evidence_gates");
  assert.deepEqual(report.summary, {
    fixtures: 4,
    equal: 3,
    classified_differences: 0,
    evidence_gate_fixtures: 1,
    review_required: 0,
    differences: 1,
    classified: 1,
    unclassified: 0,
    evidence_gates: 1,
    accepted: 0,
    stale_expectations: 0,
    unacknowledged_drops: 0,
    unaccounted_byte_differences: 0,
  });
});

test("unclassified and stale classifications require review", async () => {
  const parityContext = await context();
  const unclassified = await fixture("zia_dlp_engines_predefined_name");
  object(unclassified).expected_differences = [];
  const first = await compareTransformAdoptParityFixture(unclassified, parityContext);
  assert.equal(first.result, "review_required");
  assert.equal(object(first.summary).unclassified, 1);

  const stale = await fixture("zia_dlp_engines_predefined_name");
  const expected = object((stale.expected_differences as readonly unknown[])[0]);
  object(expected.adopt).value = "Different provider value";
  const second = await compareTransformAdoptParityFixture(stale, parityContext);
  assert.equal(second.result, "review_required");
  assert.equal(object(second.summary).unclassified, 1);
  assert.equal(object(second.summary).stale_expectations, 1);
});

test("provider-state coverage is exact", async () => {
  const parityContext = await context();
  const extra = await fixture("zcc_failopen_policy_inversion");
  object(extra.provider_state).unreferenced = { values: {}, sensitive_values: {} };
  await assert.rejects(
    compareTransformAdoptParityFixture(extra, parityContext),
    /unreferenced import id/u,
  );

  const missing = await fixture("zcc_failopen_policy_inversion");
  const state = object(missing.provider_state);
  object(missing).provider_state = { "other-policy": state["policy-001"] };
  await assert.rejects(
    compareTransformAdoptParityFixture(missing, parityContext),
    /missing import id policy-001/u,
  );
});

test("fixture schema and sanitization fail closed", async () => {
  const parityContext = await context();
  const unknown = await fixture("zcc_failopen_policy_inversion");
  object(unknown).unexpected = true;
  await assert.rejects(validateParityFixture(unknown, parityContext), /unknown key unexpected/u);

  const unsanitized = await fixture("zcc_failopen_policy_inversion");
  object(unsanitized.provenance).sanitized = false;
  await assert.rejects(validateParityFixture(unsanitized, parityContext), /sanitized must be true/u);

  const booleanVersion = await fixture("zcc_failopen_policy_inversion");
  object(booleanVersion).fixture_version = true;
  await assert.rejects(
    validateParityFixture(booleanVersion, parityContext),
    /unsupported fixture_version/u,
  );

  const wideInteger = await fixture("zcc_failopen_policy_inversion");
  object((wideInteger.raw_items as readonly unknown[])[0]).wideEvidence =
    new LosslessNumber("9".repeat(400));
  await validateParityFixture(wideInteger, parityContext);
});

test("fixture provenance is pinned to active pack and source evidence", async () => {
  const parityContext = await context();
  const wrongPin = await fixture("zcc_failopen_policy_inversion");
  object(wrongPin.provenance).provider_version = "different-version";
  await assert.rejects(
    validateParityFixture(wrongPin, parityContext),
    /does not match active zcc pack pin/u,
  );

  for (const source of [
    "https://github.com/zscaler/terraform-provider-zia/blob/main/source.go#L1",
    "https://github.com/zscaler/terraform-provider-zia/blob/v4.7.26",
  ]) {
    const wrongSource = await fixture("zia_dlp_engines_predefined_name");
    (object(wrongSource.provenance).sources as string[])[0] = source;
    await assert.rejects(
      validateParityFixture(wrongSource, parityContext),
      /GitHub blob ref pinned/u,
    );
  }

  const missingLocal = await fixture("zcc_failopen_policy_inversion");
  (object(missingLocal.provenance).local_sources as string[])[0] = "missing/source.json";
  await assert.rejects(validateParityFixture(missingLocal, parityContext), /does not exist/u);

  const undeclared = await fixture("zia_dlp_engines_predefined_name");
  const expectation = object((undeclared.expected_differences as readonly unknown[])[0]);
  (expectation.evidence as string[]).push("https://example.invalid/unpinned");
  await assert.rejects(
    validateParityFixture(undeclared, parityContext),
    /not declared by fixture provenance/u,
  );
});

test("strict diff distinguishes bool, numeric kind, signed zero, and escaped keys", () => {
  assert.equal(
    jsonDifferences(
      { items: { one: { enabled: false } } },
      { items: { one: { enabled: 0 } } },
    )[0]?.path,
    "/items/one/enabled",
  );
  assert.equal(
    jsonDifferences(
      { value: new LosslessNumber("1") },
      { value: new LosslessNumber("1.0") },
    )[0]?.path,
    "/value",
  );
  assert.equal(jsonDifferences({ value: -0 }, { value: 0 })[0]?.path, "/value");
  assert.equal(jsonDifferences({ items: { "a/b~c": 1 } }, { items: { "a/b~c": 2 } })[0]?.path, "/items/a~1b~0c");
});

test("DEL uses exact CPython escaping in artifact hashes and report bytes", async () => {
  const value = await fixture("zcc_failopen_policy_inversion");
  object(value).name = "zcc_del_boundary";
  object(value.provenance).note = "DEL \u007f boundary";
  object((value.raw_items as readonly unknown[])[0]).strictEnforcementPromptMessage = "\u007f";
  const state = object(object(value.provider_state)["policy-001"]);
  object(state.values).strict_enforcement_prompt_message = "\u007f";
  const report = await buildParityReport([value], await context());
  const rendered = renderParityReport(report);
  assert.equal(
    createHash("sha256").update(rendered).digest("hex"),
    "25d661a024d9c8ed85ad9f2c707e96dd210150e0127c69bb0e192c18c9cf2b4c",
  );
  assert.equal(rendered.includes("\u007f"), false);
  assert.equal(rendered.includes("\\u007f"), true);
  const outputs = object(object((report.fixtures as readonly unknown[])[0]).outputs);
  assert.equal(
    outputs.transform_sha256,
    "2b759590c5f2a861cc70545e7e9eea82b77fe5aaff9a1750c99a9b2fb545bc8d",
  );
  assert.equal(outputs.adopt_sha256, outputs.transform_sha256);
});

test("signed zero survives the complete fixture comparison", async () => {
  const parityContext = await context();
  const value = await fixture("zcc_failopen_policy_inversion");
  object((value.raw_items as readonly unknown[])[0]).captivePortalWebSecDisableMinutes =
    new LosslessNumber("-0.0");
  const state = object(object(value.provider_state)["policy-001"]);
  object(state.values).captive_portal_web_sec_disable_minutes = new LosslessNumber("0.0");
  const result = await compareTransformAdoptParityFixture(value, parityContext);
  assert.equal(result.result, "review_required");
  assert.equal(object(result.outputs).byte_equal, false);
  assert.equal(
    object((result.differences as readonly unknown[])[0]).path,
    "/items/policy_001/captive_portal_web_sec_disable_minutes",
  );
});

test("completeness guard catches total and partial comparator misses", async () => {
  const parityContext = await context();
  const total = await fixture("zia_dlp_engines_predefined_name");
  const missed = await compareTransformAdoptParityFixture(total, parityContext, {
    jsonDifferences: () => [],
  });
  assert.equal(missed.result, "review_required");
  assert.equal(object(missed.summary).unaccounted_byte_differences, 1);

  const partial = await fixture("zia_dlp_engines_predefined_name");
  object((partial.expected_differences as readonly unknown[])[0]).disposition = "accepted";
  object(object(object(partial.provider_state)["101"]).values).description = "Different provider description";
  const complete = await compareTransformAdoptParityFixture(partial, parityContext);
  const known = (complete.differences as readonly Record<string, unknown>[]).find((entry) => {
    return entry.path === "/items/predefined_engine/name";
  });
  assert.ok(known);
  const partialResult = await compareTransformAdoptParityFixture(partial, parityContext, {
    jsonDifferences: () => [{
      path: known.path as string,
      transform: known.transform as { readonly present: boolean; readonly value?: unknown },
      adopt: known.adopt as { readonly present: boolean; readonly value?: unknown },
    }],
  });
  assert.equal(partialResult.result, "review_required");
  assert.equal(object(partialResult.summary).accepted, 1);
  assert.equal(object(partialResult.summary).unaccounted_byte_differences, 1);
});

test("reported list differences reconstruct shorter and longer targets", () => {
  const transform = { items: { one: { values: [1, 2, 3] } } };
  const adopt = { items: { one: { values: [1, 4] } } };
  assert.deepEqual(applyReportedDifferences(transform, jsonDifferences(transform, adopt)), adopt);
  const extended = { items: { one: { values: [1, 2, 3, 4] } } };
  assert.deepEqual(applyReportedDifferences(adopt, jsonDifferences(adopt, extended)), extended);
});

test("accepted differences do not leave an evidence gate", async () => {
  const value = await fixture("zia_dlp_engines_predefined_name");
  object((value.expected_differences as readonly unknown[])[0]).disposition = "accepted";
  const result = await compareTransformAdoptParityFixture(value, await context());
  assert.equal(result.result, "classified_differences");
  assert.equal(object(result.summary).accepted, 1);
  assert.equal(object(result.summary).evidence_gates, 0);
});

test("thin CLI preserves evidence-gate and invalid-fixture exit contracts", async () => {
  const executable = path.join(ROOT, ".node-test", "node-src", "cli", "main.js");
  const evidenceGate = spawnSync(process.execPath, [
    executable,
    "transform-adopt-parity",
    ...(await fixturePaths()),
  ], { cwd: ROOT, encoding: "utf8", env: { ...process.env, PYTHON: "/usr/bin/false" } });
  assert.equal(evidenceGate.status, 1, evidenceGate.stderr);
  assert.equal(evidenceGate.stderr, "");
  assert.equal(object(JSON.parse(evidenceGate.stdout)).result, "evidence_gates");

  const invalid = spawnSync(process.execPath, [
    executable,
    "transform-adopt-parity",
    "does-not-exist.json",
  ], { cwd: ROOT, encoding: "utf8", env: { ...process.env, PYTHON: "/usr/bin/false" } });
  assert.equal(invalid.status, 2);
  assert.equal(invalid.stdout, "");
  assert.match(invalid.stderr, /does-not-exist\.json/u);
});
