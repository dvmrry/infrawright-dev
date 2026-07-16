import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

import { LosslessNumber } from "lossless-json";

import {
  assessmentGuidanceSource,
  collectAssessmentGuidance,
} from "../node-src/domain/assessment-guidance.js";
import type { AssessmentFinding } from "../node-src/domain/plan-assessment.js";
import { loadPackRoot } from "../node-src/metadata/loader.js";
import type { LoadedPackRoot } from "../node-src/metadata/loader.js";

function loadedRoot(data: Record<string, unknown>): LoadedPackRoot {
  return {
    packs: {
      manifests: [{
        name: "sample",
        directory: "/packs/sample",
        path: "/packs/sample/pack.json",
        data,
        providerPrefixes: { sample_: "sample" },
        providerSources: { sample: "example/sample" },
        requiresShared: [],
      }],
      providerPrefixes: { sample_: "sample" },
      providerSources: { sample: "example/sample" },
      providerOwners: { sample: "sample" },
      root: "/packs",
    },
    resources: new Map([[
      "sample_resource",
      {
        type: "sample_resource",
        product: "sample",
        provider: "sample",
        pack: "sample",
        registry: { generate: true },
        override: null,
      },
    ]]),
  } as unknown as LoadedPackRoot;
}

const PLAN = {
  resource_changes: [{
    address: 'sample_resource.this["one"]',
    type: "sample_resource",
    change: {
      actions: ["update"],
      before: {
        terraform_labels: {},
        rules: [{ id: 0 }],
        settings: [{ mode: "old" }],
      },
      after: {
        terraform_labels: { "goog-terraform-provisioned": "true" },
        rules: [{ id: 1 }],
        settings: [{ mode: "new" }],
      },
    },
  }],
};

const FINDINGS: readonly AssessmentFinding[] = [{
  status: "blocked",
  source: "resource_changes",
  address: 'sample_resource.this["one"]',
  resource_type: "sample_resource",
  actions: ["update"],
  paths: [
    ["rules", 0, "id"],
    ["settings", 0, "mode"],
    ["terraform_labels", "goog-terraform-provisioned"],
  ],
}];

test("original pack lanes join schema paths to exact blocked finding paths", () => {
  const root = loadedRoot({
    provider_config: {
      requirements: [{
        id: "sample_attribution",
        setting: "add_attribution",
        value: false,
        reason: "provider default",
        plan_paths: ["terraform_labels.goog-terraform-provisioned"],
        remediation: {
          kind: "provider_argument",
          mode: "required_external",
          evidence: "provider.md",
        },
      }],
    },
    absent_defaults: {
      rules: [{
        id: "sample_zero_id",
        path: "rules[].id",
        kind: "provider_absent_placeholder",
        observed_value: 0,
        action: "manual_review_required",
        evidence: "absent.md",
        reason: "provider placeholder",
        resource_type: "sample_resource",
      }],
    },
    dynamic_schema: {
      rules: [{
        id: "sample_dynamic_mode",
        provider_version_constraint: "1.0.0",
        path: "settings[].mode",
        kind: "provider_observed_projection_unsafe",
        ownership: "unknown",
        action: "manual_review_required",
        evidence: "dynamic.md",
        reason: "dynamic field",
        resource_type: "sample_resource",
      }],
    },
  });
  const result = collectAssessmentGuidance({
    source: assessmentGuidanceSource(root),
    tenant: "tenant",
    label: "sample_resource",
    members: ["sample_resource"],
    plan: PLAN,
    findings: FINDINGS,
  });
  assert.deepEqual(result.entries.map((entry) => ({
    lane: entry.lane,
    matched: entry.matched_plan_path,
    finding: entry.finding_path,
  })), [
    {
      lane: "provider_config",
      matched: "terraform_labels.goog-terraform-provisioned",
      finding: "terraform_labels.goog-terraform-provisioned",
    },
    {
      lane: "absent_default",
      matched: "rules[].id",
      finding: "rules[0].id",
    },
    {
      lane: "dynamic_schema",
      matched: "settings[].mode",
      finding: "settings[0].mode",
    },
  ]);
});

test("absent-default matching distinguishes JSON booleans from numbers", () => {
  const root = loadedRoot({
    absent_defaults: {
      rules: [{
        id: "sample_false_id",
        path: "rules[].id",
        kind: "provider_absent_placeholder",
        observed_value: false,
        action: "manual_review_required",
        evidence: "absent.md",
        reason: "wrong JSON type",
        resource_type: "sample_resource",
      }],
    },
  });
  const result = collectAssessmentGuidance({
    source: assessmentGuidanceSource(root),
    tenant: "tenant",
    label: "sample_resource",
    members: ["sample_resource"],
    plan: PLAN,
    findings: FINDINGS,
  });
  assert.deepEqual(result.entries, []);
});

test("provider guidance retains token-authored finite numeric defaults", () => {
  const root = loadedRoot({
    provider_config: {
      requirements: [{
        id: "sample_numeric_default",
        setting: "numeric_default",
        value: new LosslessNumber("1.0"),
        reason: "provider numeric default",
        plan_paths: ["terraform_labels.goog-terraform-provisioned"],
        remediation: {
          kind: "provider_argument",
          mode: "renderable_default",
          evidence: "provider.md",
          safety: {
            non_sensitive: true,
            not_tenant_specific: true,
            not_destructive: true,
          },
        },
      }],
    },
  });
  const result = collectAssessmentGuidance({
    source: assessmentGuidanceSource(root),
    tenant: "tenant",
    label: "sample_resource",
    members: ["sample_resource"],
    plan: PLAN,
    findings: FINDINGS,
  });
  assert.equal(
    (result.entries[0]?.expected_value as LosslessNumber).toString(),
    "1.0",
  );
});

test("a malformed guidance lane cannot suppress valid annotations from another lane", () => {
  const root = loadedRoot({
    provider_config: {
      requirements: [{
        id: "malformed",
        reason: "missing setting",
        plan_paths: ["terraform_labels.goog-terraform-provisioned"],
        remediation: { mode: "required_external" },
      }],
    },
    dynamic_schema: {
      rules: [{
        id: "sample_dynamic_mode",
        provider_version_constraint: "1.0.0",
        path: "settings[].mode",
        kind: "provider_observed_projection_unsafe",
        ownership: "unknown",
        action: "manual_review_required",
        evidence: "dynamic.md",
        reason: "dynamic field",
        resource_type: "sample_resource",
      }],
    },
  });
  const result = collectAssessmentGuidance({
    source: assessmentGuidanceSource(root),
    tenant: "tenant",
    label: "sample_resource",
    members: ["sample_resource"],
    plan: PLAN,
    findings: FINDINGS,
  });
  assert.deepEqual(result.entries.map((entry) => entry.lane), ["dynamic_schema"]);
});

test("lane validation rejects semantically invalid source evidence", () => {
  const invalidRules = [
    [{
      id: "bad_matrix",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "raw_api_only_provider_blind",
      ownership: "user_owned",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "invalid ownership",
      resource_type: "sample_resource",
    }],
    [{
      id: "bad_unknown",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "unknown key",
      resource_type: "sample_resource",
      invented: true,
    }],
    [{
      id: "bare_wildcard",
      provider_version_constraint: "1.0.0",
      path: "settings.*.mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "bare wildcard",
      resource_type: "sample_resource",
    }],
    [{
      id: "duplicate",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "duplicate",
      resource_type: "sample_resource",
    }, {
      id: "duplicate_again",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "duplicate",
      resource_type: "sample_resource",
    }],
    [{
      id: "type_scope",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "overlap",
      resource_type: "sample_resource",
    }, {
      id: "prefix_scope",
      provider_version_constraint: "1.0.0",
      path: "settings[].mode",
      kind: "provider_observed_projection_unsafe",
      ownership: "unknown",
      action: "manual_review_required",
      evidence: "dynamic.md",
      reason: "overlap",
      resource_prefix: "sample_",
    }],
  ];
  for (const rules of invalidRules) {
    const result = collectAssessmentGuidance({
      source: assessmentGuidanceSource(loadedRoot({ dynamic_schema: { rules } })),
      tenant: "tenant",
      label: "sample_resource",
      members: ["sample_resource"],
      plan: PLAN,
      findings: FINDINGS,
    });
    assert.deepEqual(result.entries, []);
  }
});

test("malformed metadata for another provider cannot suppress a valid lane", () => {
  const valid = loadedRoot({
    dynamic_schema: {
      rules: [{
        id: "sample_dynamic_mode",
        provider_version_constraint: "1.0.0",
        path: "settings[].mode",
        kind: "provider_observed_projection_unsafe",
        ownership: "unknown",
        action: "manual_review_required",
        evidence: "dynamic.md",
        reason: "dynamic field",
        resource_type: "sample_resource",
      }],
    },
  });
  const mixed = {
    ...valid,
    packs: {
      ...valid.packs,
      providerPrefixes: { sample_: "sample", other_: "other" },
      manifests: [...valid.packs.manifests, {
        name: "other",
        directory: "/packs/other",
        path: "/packs/other/pack.json",
        data: { dynamic_schema: { rules: [{ provider: "other", invented: true }] } },
        providerPrefixes: { other_: "other" },
        providerSources: { other: "example/other" },
        requiresShared: [],
      }],
    },
  } as LoadedPackRoot;
  const result = collectAssessmentGuidance({
    source: assessmentGuidanceSource(mixed),
    tenant: "tenant",
    label: "sample_resource",
    members: ["sample_resource"],
    plan: PLAN,
    findings: FINDINGS,
  });
  assert.deepEqual(result.entries.map((entry) => entry.rule), [
    "sample_dynamic_mode",
  ]);
});

test("the real pack loader supplies provider, absent-default, and dynamic guidance", async () => {
  const repository = process.cwd();
  const root = await loadPackRoot({
    packsRoot: path.join(repository, "packs"),
    profilePath: path.join(repository, "packsets", "full.json"),
    catalogPath: path.join(repository, "packsets", "full.json"),
  });
  const cases = [
    {
      resourceType: "google_bigquery_dataset",
      provider: "google",
      before: { terraform_labels: {} },
      after: { terraform_labels: { "goog-terraform-provisioned": "true" } },
      path: ["terraform_labels", "goog-terraform-provisioned"],
      expected: { lane: "provider_config", setting: "add_terraform_attribution_label" },
    },
    {
      resourceType: "aws_cloudwatch_log_group",
      provider: "aws",
      before: { name_prefix: "" },
      after: { name_prefix: null },
      path: ["name_prefix"],
      expected: { lane: "absent_default", rule: "aws_cloudwatch_log_group_empty_name_prefix" },
    },
    {
      resourceType: "cloudflare_dns_record",
      provider: "cloudflare",
      before: { data: { flags: "old" } },
      after: { data: { flags: "new" } },
      path: ["data", "flags"],
      expected: { lane: "dynamic_schema", rule: "cloudflare_dns_record_data_flags_dynamic" },
    },
  ] as const;
  const resources = new Map(root.resources);
  for (const selected of cases) {
    resources.set(selected.resourceType, {
      type: selected.resourceType,
      product: selected.provider,
      provider: selected.provider,
      pack: selected.provider,
      registry: { generate: true },
      override: null,
    });
  }
  const source = assessmentGuidanceSource({ ...root, resources });
  for (const selected of cases) {
    const address = `${selected.resourceType}.this["one"]`;
    const finding: AssessmentFinding = {
      status: "blocked",
      source: "resource_changes",
      address,
      resource_type: selected.resourceType,
      actions: ["update"],
      paths: [selected.path],
    };
    const result = collectAssessmentGuidance({
      source,
      tenant: "tenant",
      label: selected.resourceType,
      members: [selected.resourceType],
      plan: {
        resource_changes: [{
          address,
          type: selected.resourceType,
          change: {
            actions: ["update"],
            before: selected.before,
            after: selected.after,
          },
        }],
      },
      findings: [finding],
    });
    assert.equal(result.entries.length, 1, selected.resourceType);
    assert.deepEqual(
      Object.fromEntries(Object.keys(selected.expected).map((key) => {
        return [key, result.entries[0]?.[key]];
      })),
      selected.expected,
      selected.resourceType,
    );
  }
});
