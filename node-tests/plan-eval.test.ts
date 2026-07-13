import { PYTHON_ORACLE } from "./python-oracle.js";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
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

function update(before: unknown, after: unknown): unknown {
  return {
    address: "sample_resource.this",
    type: "sample_resource",
    change: { actions: ["update"], before, after },
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
  const python = spawnSync(PYTHON_ORACLE, ["-c", [
    "import json,sys",
    "from engine.drift_policy import DriftPolicy",
    "from engine.plan_eval import classify_plan",
    "value=json.load(sys.stdin)",
    "print(json.dumps(classify_plan(value['plan'], DriftPolicy(value['policy'])), sort_keys=True))",
  ].join("; ")], {
    input: JSON.stringify({ plan, policy: policyData }),
    encoding: "utf8",
  });
  assert.equal(python.status, 0, python.stderr);
  assert.deepEqual(actual, JSON.parse(python.stdout));
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
  const plans = [
    {
      format_version: "1.2",
      complete: true,
      errored: false,
      resource_changes: [update({ status: "UP" }, { status: "DOWN" })],
    },
    { format_version: "1.2", complete: true, errored: false, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["delete", "create"], before: {}, after: {} },
    }] },
    { format_version: "1.2", complete: true, errored: false, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: {
        actions: ["update"],
        before: { name: "same" },
        after: { name: "same" },
        after_unknown: { token: true },
      },
    }] },
    {
      format_version: "1.2",
      complete: true,
      errored: false,
      resource_changes: [],
      resource_drift: [update({ rules: [{ id: 1 }] }, { rules: [{ id: 2 }] })],
    },
    { format_version: "1.2", complete: true, errored: false, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["forget"], before: {}, after: null },
    }] },
    { format_version: "1.2", complete: true, errored: false, resource_changes: [{
      address: "sample_resource.this",
      type: "sample_resource",
      change: { actions: ["create", "forget"], before: {}, after: {} },
    }] },
  ];
  for (const plan of plans) {
    const python = spawnSync(PYTHON_ORACLE, [
      "-c",
      "import json,sys; from engine.plan_eval import classify_plan; print(json.dumps(classify_plan(json.load(sys.stdin)), sort_keys=True))",
    ], { input: JSON.stringify(plan), encoding: "utf8" });
    assert.equal(python.status, 0, python.stderr);
    assert.deepEqual(classifyPlan(plan), JSON.parse(python.stdout));
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
  const python = spawnSync(PYTHON_ORACLE, [
    "-c",
    [
      "import json,sys",
      "from engine.drift_policy import DriftPolicy",
      "from engine.plan_eval import classify_plan",
      "data=json.load(sys.stdin)",
      "p=DriftPolicy(data['policy'])",
      "r=classify_plan(data['plan'], policy=p)",
      "s=[{'resource_type':a,'mode':b,'path':c} for a,b,c in p.stale_entries(resource_types={'sample_resource'}, modes=('plan_tolerate',))]",
      "print(json.dumps({'result':r,'stale':s}, sort_keys=True))",
    ].join(";"),
  ], { input: JSON.stringify({ plan, policy: policyData }), encoding: "utf8" });
  assert.equal(python.status, 0, python.stderr);
  assert.deepEqual({ result: nodeResult, stale: nodeStale }, JSON.parse(python.stdout));
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
  const pairs = [
    ["9007199254740992", "9007199254740993"],
    ["9007199254740993", "9007199254740993.0"],
    ["1", "1.0"],
    ["true", "1"],
    ["false", "0"],
    ["-0.0", "0"],
    ["1e400", "1e400"],
    ["1e400", "-1e400"],
  ];
  for (const [before, after] of pairs) {
    const source = `{"format_version":"1.2","complete":true,"errored":false,"resource_changes":[{"address":"sample_resource.this","type":"sample_resource","change":{"actions":["update"],"before":{"value":${before}},"after":{"value":${after}}}}]}`;
    const nodeResult = classifyPlan(parseDataJsonLosslessly(source));
    const python = spawnSync(PYTHON_ORACLE, [
      "-c",
      "import json,sys; from engine.plan_eval import classify_plan; print(json.dumps(classify_plan(json.load(sys.stdin)), sort_keys=True))",
    ], { input: source, encoding: "utf8" });
    assert.equal(python.status, 0, python.stderr);
    assert.deepEqual(nodeResult, JSON.parse(python.stdout), `${before} -> ${after}`);
  }
});

test("tolerated status constant remains version-compatible", () => {
  assert.equal(TOLERATED, "clean_with_tolerated_drift");
});
