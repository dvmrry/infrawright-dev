import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

import {
  BLOCKED,
  CLEAN,
  classifyPlan,
  diffPaths,
  IDENTITY_CHANGE,
  OPAQUE_UPDATE,
  pythonJsonEqual,
  SENSITIVITY_CHANGE,
  TOLERATED,
} from "../node-src/domain/plan-eval.js";
import { DriftPolicy } from "../node-src/domain/drift-policy.js";
import {
  AssessmentPlanError,
  validateAssessmentPlan,
} from "../node-src/domain/plan-contract.js";
import { parseDataJsonLosslessly } from "../node-src/json/control.js";

interface PythonPlanEvalCase {
  readonly group: string;
  readonly name: string;
  readonly input_json: string;
  readonly result: unknown;
  readonly stale: unknown;
}

interface PythonPlanEvalAuthority {
  readonly kind: string;
  readonly version: number;
  readonly baseline: string;
  readonly authority: {
    readonly implementation: string;
    readonly python: string;
    readonly unicode: string;
  };
  readonly source_blobs: Record<string, string>;
  readonly normalization: string;
  readonly cases: readonly PythonPlanEvalCase[];
}

const PLAN_EVAL_AUTHORITY_SHA256 =
  "83924f81dc073e2dc9fef5f20ec96331fa674db09de9ab3bfac9b8770df0eaf8";
const planEvalAuthorityBytes = readFileSync(
  path.join(process.cwd(), "node-tests", "fixtures", "python-plan-eval-v1.json"),
);
assert.equal(
  createHash("sha256").update(planEvalAuthorityBytes).digest("hex"),
  PLAN_EVAL_AUTHORITY_SHA256,
  "frozen CPython plan-eval authority changed",
);
const planEvalAuthority = JSON.parse(
  planEvalAuthorityBytes.toString("utf8"),
) as PythonPlanEvalAuthority;
assert.equal(planEvalAuthority.kind, "infrawright.python-plan-eval-authority");
assert.equal(planEvalAuthority.version, 1);
assert.equal(planEvalAuthority.baseline, "397a30c1dc6996283729648d16c1e258ec3627ec");
assert.equal(planEvalAuthority.normalization, "none");
assert.deepEqual(planEvalAuthority.authority, {
  implementation: "cpython",
  python: "3.13.13",
  unicode: "15.1.0",
});
assert.deepEqual(planEvalAuthority.source_blobs, {
  test: "396c74bb12ab34b66a7bac2ba4944a93f1bf4abe",
  python_plan_eval: "f15e4f44193d517384065a1d320533ea74a47a15",
  python_drift_policy: "852517958dc18f37019f369a08ab9bfbd91441c9",
  python_paths: "63ffb562172405c27a880345cd85b93af7b1ba94",
  node_plan_eval: "af72faf37582142d51f1bf3e854ae94ccb9fdc0a",
  node_drift_policy: "ac6f61ece107213e23a5ef9533fa2477448915d1",
});
assert.equal(planEvalAuthority.cases.length, 16);

function authorityCase(name: string): PythonPlanEvalCase {
  const matches = planEvalAuthority.cases.filter((entry) => entry.name === name);
  assert.equal(matches.length, 1, `expected one frozen plan-eval case named ${name}`);
  return matches[0]!;
}

function update(before: unknown, after: unknown): unknown {
  return {
    address: "sample_resource.this",
    type: "sample_resource",
    change: { actions: ["update"], before, after },
  };
}

function referenceOutputPlan(
  action: "create" | "update" | "no-op" = "create",
): Record<string, unknown> {
  const value = { zpa_segment_group: { segment_one: "72059380790653545" } };
  return {
    format_version: "1.2",
    complete: true,
    errored: false,
    planned_values: {
      outputs: {
        infrawright_reference_ids: { sensitive: true, value },
      },
      root_module: {
        child_modules: [{
          address: "module.zpa_segment_group",
          resources: [{
            address: 'module.zpa_segment_group.zpa_segment_group.this["segment_one"]',
            index: "segment_one",
            mode: "managed",
            type: "zpa_segment_group",
            values: { id: "72059380790653545", name: "Segment One" },
          }],
        }],
      },
    },
    resource_changes: [],
    output_changes: {
      infrawright_reference_ids: {
        actions: [action],
        before: action === "create"
          ? null
          : action === "no-op"
          ? value
          : { zpa_segment_group: {} },
        after: value,
        before_sensitive: action === "create" ? false : true,
        after_sensitive: true,
        after_unknown: false,
      },
    },
  };
}

test("plan classification preserves clean, blocked, import, and opaque semantics", () => {
  assert.equal(classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [],
  }).status, CLEAN);
  assert.equal(classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [update({ a: 1 }, { a: 2 })],
  }).status, BLOCKED);
  const opaque = classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [update({ a: "same" }, { a: "same" })],
  });
  assert.deepEqual(opaque.findings[0]?.paths, [[OPAQUE_UPDATE]]);
  assert.equal(classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["create"], importing: { id: "1" } },
    }],
  }).status, CLEAN);
  assert.equal(classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["no-op"],
        before: { id: "1" },
        after: { id: "1" },
        importing: { id: "1" },
      },
    }],
  }).status, CLEAN);
});

test("only the bound provider-observed reference output may change", () => {
  const contract = { referenceOutputTypes: ["zpa_segment_group"] };
  for (const action of ["create", "update", "no-op"] as const) {
    assert.equal(classifyPlan(referenceOutputPlan(action), null, contract).status, CLEAN);
  }
  assert.throws(() => classifyPlan(referenceOutputPlan(), null), AssessmentPlanError);

  const missing = referenceOutputPlan();
  delete (missing.output_changes as Record<string, unknown>).infrawright_reference_ids;
  assert.throws(() => classifyPlan(missing, null, contract), AssessmentPlanError);

  const wrongNoOp = referenceOutputPlan("no-op");
  const wrongChanges = wrongNoOp.output_changes as Record<string, Record<string, unknown>>;
  const wrong = { zpa_segment_group: { segment_one: "wrong" } };
  wrongChanges.infrawright_reference_ids!.before = wrong;
  wrongChanges.infrawright_reference_ids!.after = wrong;
  const wrongValues = wrongNoOp.planned_values as Record<string, Record<string, unknown>>;
  const wrongOutputs = wrongValues.outputs as Record<string, Record<string, unknown>>;
  wrongOutputs.infrawright_reference_ids!.value = wrong;
  assert.throws(() => classifyPlan(wrongNoOp, null, contract), AssessmentPlanError);

  const mutations: Array<(plan: Record<string, unknown>) => void> = [
    (plan) => {
      const changes = plan.output_changes as Record<string, unknown>;
      changes.other = changes.infrawright_reference_ids;
      delete changes.infrawright_reference_ids;
    },
    (plan) => {
      const changes = plan.output_changes as Record<string, Record<string, unknown>>;
      changes.infrawright_reference_ids!.after = {
        zpa_segment_group: { segment_one: "wrong" },
      };
    },
    (plan) => {
      const changes = plan.output_changes as Record<string, Record<string, unknown>>;
      changes.infrawright_reference_ids!.after_unknown = true;
    },
    (plan) => {
      const changes = plan.output_changes as Record<string, Record<string, unknown>>;
      changes.infrawright_reference_ids!.after_sensitive = false;
    },
    (plan) => {
      const values = plan.planned_values as Record<string, Record<string, unknown>>;
      const outputs = values.outputs as Record<string, Record<string, unknown>>;
      outputs.infrawright_reference_ids!.sensitive = false;
    },
    (plan) => {
      const values = plan.planned_values as Record<string, Record<string, unknown>>;
      const root = values.root_module as Record<string, unknown>;
      const children = root.child_modules as unknown[];
      root.child_modules = [...children, structuredClone(children[0])];
    },
    (plan) => {
      const changes = plan.output_changes as Record<string, Record<string, unknown>>;
      changes.infrawright_reference_ids!.actions = ["delete"];
      changes.infrawright_reference_ids!.after = null;
    },
  ];
  for (const mutate of mutations) {
    const candidate = referenceOutputPlan();
    mutate(candidate);
    assert.throws(() => classifyPlan(candidate, null, contract), AssessmentPlanError);
  }
});

test("a bound empty reference output requires the configured generated resource", () => {
  const value = { zpa_segment_group: {} };
  const candidate = {
    format_version: "1.2",
    complete: true,
    errored: false,
    planned_values: {
      outputs: {
        infrawright_reference_ids: { sensitive: true, value },
      },
      root_module: {},
    },
    configuration: {
      root_module: {
        module_calls: {
          zpa_segment_group: {
            module: {
              resources: [{
                address: "zpa_segment_group.this",
                mode: "managed",
                type: "zpa_segment_group",
                name: "this",
              }],
            },
          },
        },
      },
    },
    resource_changes: [],
    output_changes: {
      infrawright_reference_ids: {
        actions: ["create"],
        before: null,
        after: value,
        before_sensitive: true,
        after_sensitive: true,
        after_unknown: false,
      },
    },
  };
  const contract = { referenceOutputTypes: ["zpa_segment_group"] };
  assert.equal(classifyPlan(candidate, null, contract).status, CLEAN);
  const malformed = structuredClone(candidate);
  const calls = malformed.configuration.root_module.module_calls as Record<
    string,
    unknown
  >;
  delete calls.zpa_segment_group;
  assert.throws(() => classifyPlan(malformed, null, contract), AssessmentPlanError);

  const mismatchedChild = structuredClone(candidate);
  mismatchedChild.planned_values.root_module = {
    child_modules: [{
      address: "module.zpa_segment_group",
      resources: [{
        address: "module.zpa_segment_group.terraform_data.other",
        index: "other",
        mode: "managed",
        type: "terraform_data",
        values: { id: "unrelated" },
      }],
    }],
  };
  delete (
    mismatchedChild.configuration.root_module.module_calls as Record<string, unknown>
  ).zpa_segment_group;
  assert.throws(
    () => classifyPlan(mismatchedChild, null, contract),
    AssessmentPlanError,
  );
});

test("diff paths matches Python missing-null and nested list behavior", () => {
  assert.deepEqual(diffPaths({ a: [{ b: 1 }] }, { a: [{ b: 2 }] }), [["a", 0, "b"]]);
  assert.deepEqual(diffPaths({}, { missing: null }), []);
  assert.deepEqual(diffPaths([], [null]), []);
});

test("prototype-like own keys cannot disappear behind tolerated drift", () => {
  const before = JSON.parse('{"a":0}');
  const after = JSON.parse('{"a":1,"__proto__":{}}');
  assert.deepEqual(diffPaths(before, after), [["__proto__"], ["a"]]);
  const policyData = {
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [{
          path: "a",
          reason: "fixture",
          approved_by: "unit",
        }],
      },
    },
  };
  const plan = {
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [update(before, after)],
  };
  const actual = classifyPlan(plan, new DriftPolicy(policyData));
  assert.equal(actual.status, BLOCKED);
  assert.deepEqual(actual.findings[0]?.paths, [["__proto__"]]);
  const frozen = authorityCase("prototype-like-own-key-remains-blocked");
  assert.equal(frozen.group, "prototype-like own keys cannot disappear behind tolerated drift");
  const frozenInput = JSON.parse(frozen.input_json) as {
    readonly plan: unknown;
    readonly policy: unknown;
  };
  assert.deepEqual(
    classifyPlan(frozenInput.plan, new DriftPolicy(frozenInput.policy)),
    frozen.result,
  );
  assert.deepEqual(actual, frozen.result);
});

test("lossless number equality follows Python integer/float behavior", () => {
  const values = parseDataJsonLosslessly(
    '[9007199254740993,9007199254740993.0,1,1.0,true,false,0,-0.0]',
  ) as unknown[];
  assert.equal(pythonJsonEqual(values[0], values[1]), false);
  assert.equal(pythonJsonEqual(values[2], values[3]), true);
  assert.equal(pythonJsonEqual(values[4], values[2]), true);
  assert.equal(pythonJsonEqual(values[5], values[6]), true);
  assert.equal(pythonJsonEqual(values[6], values[7]), true);
});

test("valid-plan corpus matches Python classifier exactly", () => {
  const cases = planEvalAuthority.cases.filter(
    (entry) => entry.group === "valid-plan corpus matches Python classifier exactly",
  );
  assert.equal(cases.length, 6);
  for (const frozen of cases) {
    const plan = parseDataJsonLosslessly(frozen.input_json);
    assert.deepEqual(classifyPlan(plan), frozen.result, frozen.name);
  }
});

test("current Terraform forget actions remain visible and blocked", () => {
  const forget = classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["forget"], before: {}, after: null },
    }],
  });
  assert.equal(forget.status, BLOCKED);
  assert.deepEqual(forget.findings[0]?.paths, [["<unsupported_action>"]]);

  const replacement = classifyPlan({
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["create", "forget"], before: {}, after: {} },
    }],
  });
  assert.equal(replacement.status, BLOCKED);
  assert.deepEqual(replacement.findings[0]?.paths, [["<create>"]]);
});

test("identity and sensitivity deltas cannot hide behind tolerated drift", () => {
  const policyData = {
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [{
          path: "status",
          reason: "verified normalization",
          approved_by: "unit",
        }],
      },
    },
  };
  for (const source of ["resource_changes", "resource_drift"] as const) {
    const base = {
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["update"],
        before: { status: "before" },
        after: { status: "after" },
      },
    };
    const identity = classifyPlan({
      format_version: "1.2",
      complete: true,
      errored: false,
      resource_changes: source === "resource_changes" ? [{
        ...base,
        change: {
          ...base.change,
          before_identity: { id: "old" },
          after_identity: { id: "new" },
        },
      }] : [],
      resource_drift: source === "resource_drift" ? [{
        ...base,
        change: {
          ...base.change,
          before_identity: { id: "old" },
          after_identity: { id: "new" },
        },
      }] : [],
    }, new DriftPolicy(policyData));
    assert.equal(identity.status, BLOCKED);
    assert.deepEqual(identity.findings[0]?.paths, [[IDENTITY_CHANGE]]);

    const sensitivity = classifyPlan({
      format_version: "1.2",
      complete: true,
      errored: false,
      resource_changes: source === "resource_changes" ? [{
        ...base,
        change: {
          ...base.change,
          before_sensitive: { secret: true },
          after_sensitive: {},
        },
      }] : [],
      resource_drift: source === "resource_drift" ? [{
        ...base,
        change: {
          ...base.change,
          before_sensitive: { secret: true },
          after_sensitive: {},
        },
      }] : [],
    }, new DriftPolicy(policyData));
    assert.equal(sensitivity.status, BLOCKED);
    assert.deepEqual(sensitivity.findings[0]?.paths, [[SENSITIVITY_CHANGE]]);

    const unchanged = classifyPlan({
      format_version: "1.2",
      complete: true,
      errored: false,
      resource_changes: source === "resource_changes" ? [{
        ...base,
        change: {
          ...base.change,
          before_identity: { id: "same" },
          after_identity: { id: "same" },
          before_sensitive: { secret: true },
          after_sensitive: { secret: true },
        },
      }] : [],
      resource_drift: source === "resource_drift" ? [{
        ...base,
        change: {
          ...base.change,
          before_identity: { id: "same" },
          after_identity: { id: "same" },
          before_sensitive: { secret: true },
          after_sensitive: { secret: true },
        },
      }] : [],
    }, new DriftPolicy(policyData));
    assert.equal(unchanged.status, TOLERATED);
  }
});

test("policy classification and stale tracking match Python", () => {
  const policyData = {
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [
          {
            path: "rules[].status",
            actions: ["update"],
            reason: "verified normalization",
            approved_by: "unit",
          },
          {
            path: "unused",
            reason: "stale probe",
            approved_by: "unit",
          },
        ],
      },
    },
  };
  const plan = {
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [update(
      { rules: [{ status: "UP", name: "same" }] },
      { rules: [{ status: "DOWN", name: "same" }] },
    )],
  };
  const policy = new DriftPolicy(policyData);
  const nodeResult = classifyPlan(plan, policy);
  assert.equal(nodeResult.status, TOLERATED);
  const nodeStale = policy.staleEntries({
    resourceTypes: new Set(["sample_resource"]),
    modes: ["plan_tolerate"],
  });
  const frozen = authorityCase("wildcard-tolerance-with-unused-stale-entry");
  const frozenInput = JSON.parse(frozen.input_json) as {
    readonly plan: unknown;
    readonly policy: unknown;
  };
  const frozenPolicy = new DriftPolicy(frozenInput.policy);
  const frozenResult = classifyPlan(frozenInput.plan, frozenPolicy);
  const frozenStale = frozenPolicy.staleEntries({
    resourceTypes: new Set(["sample_resource"]),
    modes: ["plan_tolerate"],
  });
  assert.deepEqual({ result: frozenResult, stale: frozenStale }, {
    result: frozen.result,
    stale: frozen.stale,
  });
  assert.deepEqual({ result: nodeResult, stale: nodeStale }, {
    result: frozen.result,
    stale: frozen.stale,
  });
});

test("partial tolerance reports only unmatched paths in Python order", () => {
  const policy = new DriftPolicy({
    version: 1,
    resource_types: {
      sample_resource: {
        plan_tolerate: [{
          path: "rules[2].status",
          reason: "test",
          approved_by: "unit",
        }],
      },
    },
  });
  const plan = {
    format_version: "1.2",
    complete: true,
    errored: false,
    resource_changes: [update(
      { rules: Array.from({ length: 11 }, () => ({ status: "before" })) },
      { rules: Array.from({ length: 11 }, (_value, index) => ({
        status: index === 2 || index === 10 ? "after" : "before",
      })) },
    )],
  };
  assert.deepEqual(classifyPlan(plan).findings[0]?.paths, [
    ["rules", 10, "status"],
    ["rules", 2, "status"],
  ]);
  const result = classifyPlan(plan, policy);
  assert.equal(result.status, BLOCKED);
  assert.deepEqual(result.findings[0]?.paths, [["rules", 10, "status"]]);
});

test("assessment plan validation rejects malformed or ambiguous plan shapes", () => {
  const complete = {
    format_version: "1.2",
    complete: true,
    errored: false,
  } as const;
  const valid = {
    ...complete,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["update"], before: {}, after: {} },
    }],
  };
  assert.doesNotThrow(() => validateAssessmentPlan(valid));
  assert.equal(classifyPlan({
    ...complete,
    output_changes: {
      summary: { actions: ["no-op"], before: "same", after: "same" },
    },
    checks: [{
      status: "unknown",
      instances: [{ status: "pass" }, { status: "unknown" }],
    }],
  }).status, CLEAN);
  assert.doesNotThrow(() => validateAssessmentPlan({
    ...complete,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["create"],
        importing: { identity: { account_id: "example" } },
      },
    }],
  }));
  assert.doesNotThrow(() => validateAssessmentPlan({
    ...complete,
    resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["no-op"],
        before: {},
        after: {},
        importing: {},
      },
    }],
  }));
  const invalid: unknown[] = [
    {},
    { ...complete, complete: false, resource_changes: [] },
    { ...complete, errored: true, resource_changes: [] },
    {
      ...complete,
      output_changes: { summary: { actions: ["update"] } },
    },
    {
      ...complete,
      output_changes: {
        summary: { actions: ["no-op"], before: "before", after: "after" },
      },
    },
    {
      ...complete,
      output_changes: {
        summary: {
          actions: ["no-op"],
          before: "same",
          after: "same",
          after_unknown: true,
        },
      },
    },
    {
      ...complete,
      output_changes: {
        summary: {
          actions: ["no-op"],
          before: "same",
          after: "same",
          before_sensitive: false,
          after_sensitive: true,
        },
      },
    },
    { ...complete, output_changes: [] },
    {
      ...complete,
      action_invocations: [{}],
    },
    { ...complete, action_invocations: {} },
    {
      ...complete,
      deferred_changes: [{}],
    },
    {
      ...complete,
      deferred_action_invocations: [{}],
    },
    { ...complete, deferred_action_invocations: {} },
    {
      ...complete,
      checks: [{ status: "fail" }],
    },
    {
      ...complete,
      checks: [{ status: "pass", instances: [{ status: "error" }] }],
    },
    {
      ...complete,
      checks: [{ status: "future" }],
    },
    { ...complete, checks: {} },
    { ...complete, format_version: "2.0", resource_changes: [] },
    { format_version: "1.2", errored: false, resource_changes: [] },
    { ...complete, resource_changes: {} },
    { ...complete, resource_changes: [{}] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: [] },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["update", "read"], before: {}, after: {} },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["update", "update"], before: {}, after: {} },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["update"] },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["no-op"], before: { id: 1 }, after: { id: 2 } },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["update"],
        before: { status: "before" },
        after: { status: "after" },
        after_unknown: { token: "true" },
      },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["update"],
        before: {},
        after: {},
        before_sensitive: { secret: "true" },
      },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["no-op"],
        before: {},
        after: {},
        after_unknown: { token: true },
      },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["no-op"],
        before: {},
        after: {},
        before_identity: { id: "old" },
        after_identity: { id: "new" },
      },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      importing: { id: "synthetic" },
      change: { actions: ["create"] },
    }] },
    { ...complete, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["create"], importing: "secret-id" },
    }] },
  ];
  for (const plan of invalid) {
    assert.throws(() => validateAssessmentPlan(plan), AssessmentPlanError);
    assert.throws(() => classifyPlan(plan), AssessmentPlanError);
  }
});

test("no-op consistency retains lossless Python numeric equality", () => {
  const compatible = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"resource_changes":[{"address":"sample_resource.this",'
      + '"type":"sample_resource","change":{"actions":["no-op"],'
      + '"before":{"value":1},"after":{"value":1.0}}}]}',
  );
  assert.equal(classifyPlan(compatible).status, CLEAN);

  const unequal = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"resource_changes":[{"address":"sample_resource.this",'
      + '"type":"sample_resource","change":{"actions":["no-op"],'
      + '"before":{"value":9007199254740993},'
      + '"after":{"value":9007199254740993.0}}}]}',
  );
  assert.throws(() => classifyPlan(unequal), AssessmentPlanError);
});

test("no-op contract keeps Terraform booleans distinct from numbers", () => {
  const resource = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"resource_changes":[{"address":"sample_resource.this",'
      + '"type":"sample_resource","change":{"actions":["no-op"],'
      + '"before":{"value":true},"after":{"value":1}}}]}',
  );
  const output = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"output_changes":{"value":{"actions":["no-op"],'
      + '"before":false,"after":0}}}',
  );
  assert.throws(() => classifyPlan(resource), AssessmentPlanError);
  assert.throws(() => classifyPlan(output), AssessmentPlanError);

  const sameTyped = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"resource_changes":[{"address":"sample_resource.this",'
      + '"type":"sample_resource","change":{"actions":["no-op"],'
      + '"before":{"flag":true,"count":1},'
      + '"after":{"flag":true,"count":1.0}}}]}',
  );
  assert.equal(classifyPlan(sameTyped).status, CLEAN);
});

test("import markers cannot hide no-op sensitivity changes", () => {
  const plan = parseDataJsonLosslessly(
    '{"format_version":"1.2","complete":true,"errored":false,'
      + '"resource_changes":[{"address":"sample_resource.this",'
      + '"type":"sample_resource","change":{"actions":["no-op"],'
      + '"before":{"secret":"same"},"after":{"secret":"same"},'
      + '"before_sensitive":{"secret":true},"after_sensitive":{},'
      + '"importing":{"id":"x"}}}]}',
  );
  assert.throws(() => classifyPlan(plan), AssessmentPlanError);
});

test("lossless numeric classification corpus matches Python", () => {
  const cases = planEvalAuthority.cases.filter(
    (entry) => entry.group === "lossless numeric classification corpus matches Python",
  );
  assert.equal(cases.length, 8);
  for (const frozen of cases) {
    const nodeResult = classifyPlan(parseDataJsonLosslessly(frozen.input_json));
    assert.deepEqual(nodeResult, frozen.result, frozen.name);
  }
});

test("tolerated status constant remains version-compatible", () => {
  assert.equal(TOLERATED, "clean_with_tolerated_drift");
});
