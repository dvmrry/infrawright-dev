import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

import { ProcessFailure } from "../node-src/domain/errors.js";
import { validateSavedPlanAssessment } from "../node-src/contracts/validators.js";
import type { SavedPlanAssessmentCore } from "../node-src/domain/plan-assessment.js";
import {
  buildSavedPlanAssessmentErrorReport,
  buildSavedPlanAssessmentReport,
  formatConcretePlanPath,
} from "../node-src/domain/plan-report.js";
import { renderPythonCompatibleJson, type JsonValue } from "../node-src/json/python-compatible.js";

const PYTHON_REPORT = String.raw`
import json
import sys
from engine import ops

i = json.loads(sys.stdin.read())
report = ops._new_assessment_report(i["mode"], {
    "tenant": i["tenant"],
    "selectors": i["selectors"],
    "policy": i["policy"],
})
report["request"]["policy_sha256"] = i["policy_sha256"]
ops._append_root_assessment(
    report, i["root"]["tenant"], i["root"]["label"],
    i["root"]["members"], i["plan"], i["classification"],
    i["root"]["plan"]["sha256"], i["root"]["plan_fingerprint"],
    guidance=i["guidance"],
)
report["stale_policy"] = i["stale_policy"]
s = i["summary"]
ops._finish_assessment_report(report, s["clean"], s["tolerated"], s["blocked"])
sys.stdout.write(json.dumps(report, indent=2, sort_keys=True) + "\n")
`;

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
  const paths = [
    [],
    ["rules", 0, "id"],
    [0, "id"],
    ["rules", "[]", "id"],
    ["rules", "*", "id"],
    ["map.key", "quote\"slash\\"],
  ] as const;
  const python = spawnSync("python3", [
    "-c",
    "import json,sys; from engine.paths import format_path; print(json.dumps([format_path(p) for p in json.load(sys.stdin)]))",
  ], { input: JSON.stringify(paths), encoding: "utf8" });
  assert.equal(python.status, 0, python.stderr);
  assert.deepEqual(paths.map(formatConcretePlanPath), JSON.parse(python.stdout));
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
    const python = spawnSync("python3", ["-c", PYTHON_REPORT], {
      input: JSON.stringify({
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
      }),
      encoding: "utf8",
    });
    assert.equal(python.status, 0, python.stderr);
    assert.deepEqual(report, JSON.parse(python.stdout));
    assert.equal(validateSavedPlanAssessment(report), true);
    assert.equal(
      renderPythonCompatibleJson(report as unknown as JsonValue),
      python.stdout,
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
