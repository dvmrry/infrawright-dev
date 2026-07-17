import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { validateSavedPlanAssessment } from "../node-src/contracts/validators.js";
import type { SavedPlanAssessmentCore } from "../node-src/domain/plan-assessment.js";
import {
  buildSavedPlanAssessmentErrorReport,
  buildSavedPlanAssessmentReport,
  formatConcretePlanPath,
} from "../node-src/domain/plan-report.js";
import { renderPythonCompatibleJson, type JsonValue } from "../node-src/json/python-compatible.js";

interface PythonPlanReportCase {
  readonly name: string;
  readonly input_json: string;
  readonly output_bytes: string;
}

interface PythonPlanReportAuthority {
  readonly authority: {
    readonly implementation: string;
    readonly python_version: string;
    readonly unicode_version: string;
  };
  readonly float_case: {
    readonly name: string;
    readonly output_bytes: string;
    readonly token: string;
  };
  readonly kind: string;
  readonly normalization: string;
  readonly path_case: {
    readonly input: readonly (readonly (string | number)[])[];
    readonly name: string;
    readonly output: readonly string[];
  };
  readonly producing_baseline: string;
  readonly report_cases: readonly PythonPlanReportCase[];
  readonly schema_version: number;
  readonly source_blobs: Record<string, string>;
}

const PLAN_REPORT_AUTHORITY_SHA256 =
  "df9d09b903bf60d34ad567f213bd1ddbb1e8bf2aaf1fc71c49be9a050a3e343c";
const planReportAuthorityBytes = readFileSync(
  path.join(process.cwd(), "node-tests", "fixtures", "python-plan-report-v1.json"),
);
assert.equal(
  createHash("sha256").update(planReportAuthorityBytes).digest("hex"),
  PLAN_REPORT_AUTHORITY_SHA256,
  "frozen CPython plan-report authority changed",
);
const planReportAuthority = JSON.parse(
  planReportAuthorityBytes.toString("utf8"),
) as PythonPlanReportAuthority;
assert.equal(planReportAuthority.kind, "infrawright.python-plan-report-authority");
assert.equal(planReportAuthority.schema_version, 1);
assert.equal(planReportAuthority.normalization, "none");
assert.equal(
  planReportAuthority.producing_baseline,
  "ef8b4622e79bdc2e8b3c54a52bc18c6c379ef13c",
);
assert.deepEqual(planReportAuthority.authority, {
  implementation: "CPython",
  python_version: "3.13.13",
  unicode_version: "15.1.0",
});
assert.deepEqual(planReportAuthority.source_blobs, {
  node_plan_report: "4077ba595ab6e58ad51265102b1166b925c3cdf4",
  node_python_compatible: "a95ef511c10bb1c727ca6a5f9616909acdea12c3",
  node_validators: "2e29d8025f857c38af48627ef67c03385af91679",
  python_ops: "f160a796f6078d96ee423d1ca7f1d169598c8160",
  python_paths: "63ffb562172405c27a880345cd85b93af7b1ba94",
  python_plan_eval: "f15e4f44193d517384065a1d320533ea74a47a15",
  test: "c93c39d46e0e354cf9096acfaf5c68b4c2f80bc2",
});

function reportAuthorityCase(name: string): PythonPlanReportCase {
  const matches = planReportAuthority.report_cases.filter((entry) => entry.name === name);
  assert.equal(matches.length, 1, `expected one frozen plan-report case named ${name}`);
  return matches[0]!;
}

function core(status: "clean" | "clean_with_tolerated_drift" | "blocked"): SavedPlanAssessmentCore {
  const counts = {
    clean: status === "clean" ? 1 : 0,
    tolerated: status === "clean_with_tolerated_drift" ? 1 : 0,
    blocked: status === "blocked" ? 1 : 0,
  };
  return {
    status,
    checked: 1,
    ...counts,
    policy_sha256: "a".repeat(64),
    roots: [{
      tenant: "tenant",
      label: "zpa_custom",
      members: ["zpa_sample"],
      status,
      plan: {
        sha256: "b".repeat(64),
        format_version: "1.2",
        terraform_version: "1.15.4",
      },
      plan_fingerprint: { version: 2, sha256: "c".repeat(64) },
      findings: status === "clean" ? [] : [{
        status,
        source: "resource_changes",
        address: 'zpa_sample.this["one"]',
        resource_type: "zpa_sample",
        actions: ["update"],
        paths: [["rules", 0, "map.key", "quote\"slash\\"]],
      }],
    }],
    stale_policy: [{
      resource_type: "zpa_sample",
      mode: "plan_tolerate",
      path: "unused",
    }],
  };
}

test("concrete plan path formatting matches Python", () => {
  assert.equal(planReportAuthority.path_case.name, "concrete-plan-paths");
  assert.deepEqual(
    planReportAuthority.path_case.input.map(formatConcretePlanPath),
    planReportAuthority.path_case.output,
  );
});

test("saved-plan report object and bytes match Python for each summary status", () => {
  for (const status of ["clean", "clean_with_tolerated_drift", "blocked"] as const) {
    const input = core(status);
    const findingPath = "rules[0].map.key.quote\"slash\\";
    const guidance = status === "blocked" ? [{
      lane: "absent_default",
      source: "resource_changes",
      address: 'zpa_sample.this["one"]',
      finding_path: findingPath,
      matched_plan_path: "rules[].map.key.quote\"slash\\",
      status_effect: "informational only; plan remains blocked",
      reason: "fixture",
    }] : [];
    const report = buildSavedPlanAssessmentReport({
      mode: "assert-adoptable",
      request: { tenant: "tenant", selectors: ["zpa/sample"], policy: "policy.json" },
      core: input,
      guidance: [{ tenant: "tenant", label: "zpa_custom", entries: guidance }],
    });
    const plan = {
      format_version: "1.2",
      terraform_version: "1.15.4",
      resource_changes: status === "clean" ? [] : [{
        address: 'zpa_sample.this["one"]',
        type: "zpa_sample",
      }],
    };
    const classification = {
      status,
      findings: input.roots[0]?.findings ?? [],
    };
    const authorityInput = JSON.stringify({
      mode: "assert-adoptable",
      tenant: "tenant",
      selectors: ["zpa/sample"],
      policy: "policy.json",
      policy_sha256: input.policy_sha256,
      root: input.roots[0],
      plan,
      classification,
      guidance,
      stale_policy: input.stale_policy,
      summary: input,
    });
    const frozen = reportAuthorityCase(status);
    assert.equal(authorityInput, frozen.input_json);
    assert.deepEqual(report, JSON.parse(frozen.output_bytes));
    assert.equal(validateSavedPlanAssessment(report), true);
    assert.equal(
      renderPythonCompatibleJson(report as unknown as JsonValue),
      frozen.output_bytes,
    );
  }
});

test("guidance must be JSON and joined to a blocked concrete finding", () => {
  const blocked = core("blocked");
  const base = {
    mode: "assert-adoptable" as const,
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    core: blocked,
  };
  for (const guidance of [
    [{ lane: "absent_default", source: "resource_changes", address: null }],
    [{
      lane: "absent_default",
      source: "resource_changes",
      address: 'zpa_sample.this["one"]',
      finding_path: "rules[0].map.key.quote\"slash\\",
      matched_plan_path: "rules[9].map.key.quote\"slash\\",
      status_effect: "informational",
    }],
    [{
      lane: "absent_default",
      source: "resource_changes",
      address: 'zpa_sample.this["one"]',
      finding_path: "wrong.path",
      matched_plan_path: "wrong.path",
      status_effect: "informational",
    }],
    [{
      lane: "absent_default",
      source: "resource_changes",
      address: 'zpa_sample.this["one"]',
      finding_path: "rules[0].map.key.quote\"slash\\",
      matched_plan_path: "rules[].map.key.quote\"slash\\",
      status_effect: "informational",
      value: Number.POSITIVE_INFINITY,
    }],
  ]) {
    assert.throws(
      () => buildSavedPlanAssessmentReport({
        ...base,
        guidance: [{ tenant: "tenant", label: "zpa_custom", entries: guidance }],
      }),
      (error: unknown) => {
        return error instanceof ProcessFailure
          && error.code === "INVALID_ASSESSMENT_GUIDANCE";
      },
    );
  }

  assert.throws(
    () => buildSavedPlanAssessmentReport({
      ...base,
      guidance: [{ tenant: "other", label: "root", entries: [] }],
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ASSESSMENT_GUIDANCE",
  );

  const concrete = "rules[0].map.key.quote\"slash\\";
  const schema = "rules[].map.key.quote\"slash\\";
  const absent = {
    lane: "absent_default",
    source: "resource_changes",
    address: 'zpa_sample.this["one"]',
    finding_path: concrete,
    matched_plan_path: schema,
    status_effect: "informational only; plan remains blocked",
  };
  const provider = { ...absent, lane: "provider_config" };
  const canonical = buildSavedPlanAssessmentReport({
    ...base,
    guidance: [{
      tenant: "tenant",
      label: "zpa_custom",
      entries: [absent, provider, absent],
    }],
  });
  assert.deepEqual(
    canonical.roots[0]?.guidance.map((entry) => entry.lane),
    ["provider_config", "absent_default"],
  );
});

test("report status is derived and inconsistent cores fail closed", () => {
  const input = core("clean");
  assert.throws(
    () => buildSavedPlanAssessmentReport({
      mode: "assert-adoptable",
      request: { tenant: null, selectors: [], policy: "policy.json" },
      core: { ...input, status: "blocked" },
    }),
    (error: unknown) => error instanceof ProcessFailure
      && error.code === "INVALID_ASSESSMENT_REPORT",
  );
});

test("clean import findings remain classification evidence but are omitted from v1 reports", () => {
  const input = core("clean");
  const root = input.roots[0];
  assert.ok(root !== undefined);
  const importFinding = {
    status: "clean" as const,
    source: "resource_changes" as const,
    address: 'zpa_sample.this["one"]',
    resource_type: "zpa_sample",
    actions: ["create"],
    paths: [],
  };
  const report = buildSavedPlanAssessmentReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: ["zpa_sample"], policy: null },
    core: {
      ...input,
      policy_sha256: null,
      stale_policy: [],
      roots: [{ ...root, findings: [importFinding] }],
    },
  });
  assert.equal(report.roots[0]?.status, "clean");
  assert.deepEqual(report.roots[0]?.findings, []);
  assert.equal(validateSavedPlanAssessment(report), true);
});

test("error report recomputes partial counts and leaves the source core unchanged", () => {
  const partial = core("blocked");
  const error = buildSavedPlanAssessmentErrorReport({
    mode: "assert-adoptable",
    request: { tenant: null, selectors: [], policy: "policy.json" },
    partial,
    error: {
      kind: "assessment_error",
      message: "sanitized assessment failure",
    },
  });
  assert.equal(partial.status, "blocked");
  assert.deepEqual(error.summary, {
    status: "error",
    checked: 1,
    clean: 0,
    tolerated: 0,
    blocked: 1,
  });
  assert.deepEqual(error.error, {
    kind: "assessment_error",
    message: "sanitized assessment failure",
  });
  assert.equal(validateSavedPlanAssessment(error), true);
});

test("large guidance remains reportable and finite floats render like Python", () => {
  const blocked = core("blocked");
  const findingPath = "rules[0].map.key.quote\"slash\\";
  const entries = Array.from({ length: 10_001 }, (_, index) => ({
    lane: "absent_default",
    source: "resource_changes",
    address: 'zpa_sample.this["one"]',
    finding_path: findingPath,
    matched_plan_path: "rules[].map.key.quote\"slash\\",
    status_effect: "informational only; plan remains blocked",
    rule: `rule-${String(index).padStart(5, "0")}`,
    observed_value: 0.5,
  }));
  const report = buildSavedPlanAssessmentReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    core: blocked,
    guidance: [{ tenant: "tenant", label: "zpa_custom", entries }],
  });
  assert.equal(report.roots[0]?.guidance.length, entries.length);
  assert.match(
    renderPythonCompatibleJson(report as unknown as JsonValue),
    /"observed_value": 0\.5/u,
  );
});

test("report rendering preserves Python float provenance from guidance JSON", () => {
  const blocked = core("blocked");
  const findingPath = "rules[0].map.key.quote\"slash\\";
  const report = buildSavedPlanAssessmentReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    core: blocked,
    guidance: [{
      tenant: "tenant",
      label: "zpa_custom",
      entries: [{
        lane: "absent_default",
        source: "resource_changes",
        address: 'zpa_sample.this["one"]',
        finding_path: findingPath,
        matched_plan_path: "rules[].map.key.quote\"slash\\",
        status_effect: "informational only; plan remains blocked",
        rule: "float-provenance",
        observed_value: new LosslessNumber("1.0"),
      }],
    }],
  });
  const rendered = renderPythonCompatibleJson(report as unknown as JsonValue);
  assert.equal(planReportAuthority.float_case.name, "guidance-float-provenance");
  assert.equal(planReportAuthority.float_case.token, "1.0");
  assert.match(rendered, /"observed_value": 1\.0/u);
  assert.equal(rendered, planReportAuthority.float_case.output_bytes);
});

test("assessment validator rejects contradictory report semantics", () => {
  const clean = buildSavedPlanAssessmentReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    core: core("clean"),
  });
  const error = buildSavedPlanAssessmentErrorReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    partial: core("clean"),
    error: { kind: "assessment_error", message: "fixture" },
  });

  const cleanWithError = JSON.parse(JSON.stringify(clean));
  cleanWithError.error = { kind: "assessment_error", message: "contradiction" };
  assert.equal(validateSavedPlanAssessment(cleanWithError), false);

  const cleanWithoutRoots = JSON.parse(JSON.stringify(clean));
  cleanWithoutRoots.roots = [];
  cleanWithoutRoots.summary = {
    status: "clean",
    checked: 999,
    clean: 999,
    tolerated: 0,
    blocked: 0,
  };
  assert.equal(validateSavedPlanAssessment(cleanWithoutRoots), false);

  const cleanWithForgedCounts = JSON.parse(JSON.stringify(clean));
  cleanWithForgedCounts.summary.checked = 999;
  cleanWithForgedCounts.summary.clean = 999;
  assert.equal(validateSavedPlanAssessment(cleanWithForgedCounts), false);

  const errorWithForgedCounts = JSON.parse(JSON.stringify(error));
  errorWithForgedCounts.summary.checked = 999;
  errorWithForgedCounts.summary.clean = 0;
  errorWithForgedCounts.summary.blocked = 777;
  assert.equal(validateSavedPlanAssessment(errorWithForgedCounts), false);

  const duplicatedRoot = JSON.parse(JSON.stringify(clean));
  duplicatedRoot.roots.push(structuredClone(duplicatedRoot.roots[0]));
  duplicatedRoot.summary.checked = 2;
  duplicatedRoot.summary.clean = 2;
  assert.equal(validateSavedPlanAssessment(duplicatedRoot), false);

  const reusedMember = JSON.parse(JSON.stringify(clean));
  const secondRoot = structuredClone(reusedMember.roots[0]);
  secondRoot.label = "other_root";
  reusedMember.roots.push(secondRoot);
  reusedMember.summary.checked = 2;
  reusedMember.summary.clean = 2;
  assert.equal(validateSavedPlanAssessment(reusedMember), false);

  for (const members of [[], ["zpa_sample", "zpa_sample"]]) {
    const invalidMembers = JSON.parse(JSON.stringify(clean));
    invalidMembers.roots[0].members = members;
    assert.equal(validateSavedPlanAssessment(invalidMembers), false);
  }

  const mismatchedTenant = JSON.parse(JSON.stringify(clean));
  mismatchedTenant.roots[0].tenant = "other";
  assert.equal(validateSavedPlanAssessment(mismatchedTenant), false);

  const staleUnknownType = JSON.parse(JSON.stringify(clean));
  staleUnknownType.stale_policy[0].resource_type = "zpa_other";
  assert.equal(validateSavedPlanAssessment(staleUnknownType), false);

  const duplicateStale = JSON.parse(JSON.stringify(clean));
  duplicateStale.stale_policy.push(structuredClone(duplicateStale.stale_policy[0]));
  assert.equal(validateSavedPlanAssessment(duplicateStale), false);

  const cleanWithBlockedFinding = JSON.parse(JSON.stringify(clean));
  cleanWithBlockedFinding.roots[0].findings = [{
    status: "blocked",
    source: "resource_changes",
    address: "zpa_sample.this",
    resource_type: "zpa_sample",
    actions: ["update"],
    paths: ["name"],
  }];
  assert.equal(validateSavedPlanAssessment(cleanWithBlockedFinding), false);

  const cleanWithGuidance = JSON.parse(JSON.stringify(clean));
  cleanWithGuidance.roots[0].guidance = [{
    lane: "absent_default",
    source: "resource_changes",
    address: "zpa_sample.this",
    finding_path: "name",
    matched_plan_path: "name",
    status_effect: "informational only; plan remains blocked",
  }];
  assert.equal(validateSavedPlanAssessment(cleanWithGuidance), false);

  const blockedWithUnjoinedGuidance = buildSavedPlanAssessmentReport({
    mode: "assert-adoptable",
    request: { tenant: "tenant", selectors: [], policy: "policy.json" },
    core: core("blocked"),
    guidance: [{
      tenant: "tenant",
      label: "zpa_custom",
      entries: [{
        lane: "absent_default",
        source: "resource_changes",
        address: 'zpa_sample.this["one"]',
        finding_path: "rules[0].map.key.quote\"slash\\",
        matched_plan_path: "rules[].map.key.quote\"slash\\",
        status_effect: "informational only; plan remains blocked",
      }],
    }],
  });
  const unjoinedGuidance = JSON.parse(JSON.stringify(blockedWithUnjoinedGuidance));
  unjoinedGuidance.roots[0].guidance[0].finding_path = "other";
  assert.equal(validateSavedPlanAssessment(unjoinedGuidance), false);
  const leakedSortKey = JSON.parse(JSON.stringify(blockedWithUnjoinedGuidance));
  leakedSortKey.roots[0].guidance[0].sort_key = ["internal"];
  assert.equal(validateSavedPlanAssessment(leakedSortKey), false);

  const normalWithUnboundPolicyEvidence = JSON.parse(JSON.stringify(clean));
  normalWithUnboundPolicyEvidence.request.policy = null;
  assert.equal(validateSavedPlanAssessment(normalWithUnboundPolicyEvidence), false);

  const stalePolicyWithoutPolicy = JSON.parse(JSON.stringify(clean));
  stalePolicyWithoutPolicy.request.policy = null;
  stalePolicyWithoutPolicy.request.policy_sha256 = null;
  assert.equal(validateSavedPlanAssessment(stalePolicyWithoutPolicy), false);

  for (const kind of ["no_saved_plans", "policy_error"]) {
    const impossiblePhase = JSON.parse(JSON.stringify(error));
    impossiblePhase.error.kind = kind;
    assert.equal(validateSavedPlanAssessment(impossiblePhase), false);
  }

  const policyErrorWithoutPolicy = JSON.parse(JSON.stringify(error));
  policyErrorWithoutPolicy.roots = [];
  policyErrorWithoutPolicy.summary = {
    status: "error",
    checked: 0,
    clean: 0,
    tolerated: 0,
    blocked: 0,
  };
  policyErrorWithoutPolicy.stale_policy = [];
  policyErrorWithoutPolicy.error.kind = "policy_error";
  policyErrorWithoutPolicy.request.policy = null;
  policyErrorWithoutPolicy.request.policy_sha256 = null;
  assert.equal(validateSavedPlanAssessment(policyErrorWithoutPolicy), false);

  const noPlansWithoutCompletedPolicy = structuredClone(policyErrorWithoutPolicy);
  noPlansWithoutCompletedPolicy.error.kind = "no_saved_plans";
  noPlansWithoutCompletedPolicy.request.policy = "policy.json";
  assert.equal(validateSavedPlanAssessment(noPlansWithoutCompletedPolicy), false);

  const errorWithoutDetail = JSON.parse(JSON.stringify(error));
  delete errorWithoutDetail.error;
  assert.equal(validateSavedPlanAssessment(errorWithoutDetail), false);

  const blockedWithoutBlockedRoot = JSON.parse(JSON.stringify(clean));
  blockedWithoutBlockedRoot.summary.status = "blocked";
  blockedWithoutBlockedRoot.summary.clean = 0;
  blockedWithoutBlockedRoot.summary.blocked = 1;
  assert.equal(validateSavedPlanAssessment(blockedWithoutBlockedRoot), false);

});
